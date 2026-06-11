package web

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"github.com/umputun/ralphex/pkg/status"
)

// ParseProgressHeader reads the header section of a progress file and extracts metadata.
// the header format is:
//
//	# Ralphex Progress Log
//	Plan: path/to/plan.md
//	Branch: feature-branch
//	Mode: full
//	Executor: codex
//	Plan model: opus:high
//	Task model: gpt-5.5:high
//	Review model: gpt-5.5:low
//	Started: 2026-01-22 10:30:00
//	------------------------------------------------------------
//
// the Executor and model lines are optional — they appear only when the
// corresponding parameter was set for the run.
//
// the second return value reports whether the terminating separator line was
// observed, which means the header is fully written. during a truncate+rewrite
// the header is emitted across several writes, so a mid-write read can return
// incomplete metadata (zero StartTime, missing fields); callers should use the
// complete flag to decide whether to trust the parsed metadata.
func ParseProgressHeader(path string) (meta SessionMetadata, complete bool, err error) {
	f, err := os.Open(path) //nolint:gosec // path from user-controlled glob pattern, acceptable for session discovery
	if err != nil {
		return SessionMetadata{}, false, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	reader := bufio.NewReader(f)

	for {
		line, readErr := reader.ReadString('\n')
		line = trimLineEnding(line)

		// stop at separator line; check readErr even when breaking
		if line != "" && strings.HasPrefix(line, "---") {
			if readErr != nil && !errors.Is(readErr, io.EOF) {
				return SessionMetadata{}, false, fmt.Errorf("read file: %w", readErr)
			}
			complete = true
			break
		}

		// parse key-value pairs (process line before checking error,
		// as ReadString may return partial data alongside an error)
		parseHeaderField(&meta, line)

		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return SessionMetadata{}, false, fmt.Errorf("read file: %w", readErr)
			}
			break // EOF after processing final line
		}
	}

	return meta, complete, nil
}

// parseHeaderField applies a single header key-value line to meta.
// unknown lines are ignored.
func parseHeaderField(meta *SessionMetadata, line string) {
	fields := []struct {
		prefix string
		dst    *string
	}{
		{"Plan: ", &meta.PlanPath},
		{"Branch: ", &meta.Branch},
		{"Mode: ", &meta.Mode},
		{"Executor: ", &meta.Executor},
		{"Plan model: ", &meta.PlanModel},
		{"Task model: ", &meta.TaskModel},
		{"Review model: ", &meta.ReviewModel},
	}
	for _, f := range fields {
		if val, found := strings.CutPrefix(line, f.prefix); found {
			*f.dst = val
			return
		}
	}
	if val, found := strings.CutPrefix(line, "Started: "); found {
		// header timestamps are written in local time without a zone offset
		if t, parseErr := time.ParseInLocation("2006-01-02 15:04:05", val, time.Local); parseErr == nil {
			meta.StartTime = t
		}
	}
}

// loadProgressFileIntoSession reads a progress file and publishes events to the session's SSE server.
// used for completed sessions that were discovered after they finished.
// errors are silently ignored since this is best-effort loading.
// records the total bytes consumed into session.lastOffset so a later Reactivate()
// can resume tailing from after the loaded content instead of re-emitting it.
func (m *SessionManager) loadProgressFileIntoSession(path string, session *Session) {
	f, err := os.Open(path) //nolint:gosec // path from user-controlled glob pattern, acceptable for session discovery
	if err != nil {
		return
	}
	defer f.Close()

	reader := bufio.NewReader(f)
	inHeader := true
	phase := status.PhaseTask
	var pendingSection string // section header waiting for first timestamped event
	var currentTask int       // active task number for task_end boundary events
	var bytesRead int64

	for {
		line, readErr := reader.ReadString('\n')
		// a partial trailing line (no newline delimiter) means the writer is
		// mid-write — the flock-race recovery path is the realistic case where
		// loader is invoked on a still-being-written file. counting and
		// publishing the partial would advance lastOffset past the partial
		// bytes; a later Reactivate would then resume reading the suffix of
		// the writer's eventual completed line as if it were a separate event,
		// reintroducing the corruption this PR is meant to eliminate.
		if readErr != nil && line != "" {
			break
		}
		// count raw bytes including the delimiter before trimming, so LF, CRLF,
		// and the final empty read all count correctly
		bytesRead += int64(len(line))
		line = trimLineEnding(line)

		if line != "" {
			var parsed ParsedLine
			parsed, inHeader = parseProgressLine(line, inHeader)
			phase, pendingSection, currentTask = m.processProgressLine(session, parsed, phase, pendingSection, currentTask)
		}

		if readErr != nil {
			break // EOF or read error; best-effort loading, errors silently ignored
		}
	}

	if pendingSection != "" {
		var events []Event
		events, currentTask = buildPendingSectionEvents(pendingSection, phase, time.Now(), currentTask)
		m.publishEvents(session, events)
	}

	session.setLastOffset(bytesRead)
	// record the phase the parser ended on so a later Reactivate resumes with
	// the correct phase rather than the tailer's default (PhaseTask).
	session.setLastPhase(phase)
	// record the active task so a later Reactivate emits its task_end when the
	// next task iteration or phase transition arrives.
	session.setLastTask(currentTask)
}

// processProgressLine handles a single parsed progress line,
// updating phase, pendingSection, and currentTask state and publishing events as needed.
func (m *SessionManager) processProgressLine(session *Session, parsed ParsedLine,
	phase status.Phase, pendingSection string, currentTask int) (status.Phase, string, int) {
	switch parsed.Type {
	case ParsedLineSkip:
		return phase, pendingSection, currentTask
	case ParsedLineSection:
		if pendingSection != "" {
			var events []Event
			events, currentTask = buildPendingSectionEvents(pendingSection, phase, time.Now(), currentTask)
			m.publishEvents(session, events)
		}
		phase = parsed.Phase
		// defer emitting section until we see a timestamped event
		return phase, parsed.Section, currentTask
	case ParsedLineTimestamp:
		// emit pending section with this event's timestamp (for accurate durations)
		if pendingSection != "" {
			var events []Event
			events, currentTask = buildPendingSectionEvents(pendingSection, phase, parsed.Timestamp, currentTask)
			m.publishEvents(session, events)
			pendingSection = ""
		}
		event := eventFromParsed(parsed, phase)
		if event.Type == EventTypeOutput {
			if stats, ok := parseDiffStats(event.Text); ok {
				session.SetDiffStats(stats)
			}
		}
		m.publishEvent(session, event)
	case ParsedLinePlain:
		m.publishEvent(session, eventFromParsed(parsed, phase))
	}
	return phase, pendingSection, currentTask
}

func (m *SessionManager) publishEvents(session *Session, events []Event) {
	for _, event := range events {
		m.publishEvent(session, event)
	}
}

func (m *SessionManager) publishEvent(session *Session, event Event) {
	if err := session.Publish(event); err != nil {
		log.Printf("[WARN] failed to publish %s event: %v", event.Type, err)
	}
}

// phaseFromSection determines the phase from a section name.
// checks "codex"/"custom" before "review" because external review sections should be PhaseCodex.
func phaseFromSection(name string) status.Phase {
	nameLower := strings.ToLower(name)
	switch {
	case strings.Contains(nameLower, "task"):
		return status.PhaseTask
	case strings.Contains(nameLower, "codex"), strings.Contains(nameLower, "custom"):
		return status.PhaseCodex
	case strings.Contains(nameLower, "review"):
		return status.PhaseReview
	case strings.Contains(nameLower, "claude-eval"), strings.Contains(nameLower, "claude eval"):
		return status.PhaseClaudeEval
	default:
		return status.PhaseTask
	}
}

// trimLineEnding removes trailing line ending to match bufio.ScanLines semantics:
// strips \n, \r\n, or a bare trailing \r (which ScanLines drops via dropCR at EOF).
// unlike strings.TrimRight("\r\n"), this preserves embedded \r characters in content.
func trimLineEnding(line string) string {
	n := len(line)
	if n > 0 && line[n-1] == '\n' {
		n--
	}
	if n > 0 && line[n-1] == '\r' {
		n--
	}
	return line[:n]
}
