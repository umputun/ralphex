package web

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
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
//	Started: 2026-01-22 10:30:00
//	------------------------------------------------------------
func ParseProgressHeader(path string) (SessionMetadata, error) {
	f, err := os.Open(path) //nolint:gosec // path from user-controlled glob pattern, acceptable for session discovery
	if err != nil {
		return SessionMetadata{}, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	var meta SessionMetadata
	reader := bufio.NewReader(f)

	for {
		line, readErr := reader.ReadString('\n')
		line = trimLineEnding(line)

		// stop at separator line; check readErr even when breaking
		if line != "" && strings.HasPrefix(line, "---") {
			if readErr != nil && !errors.Is(readErr, io.EOF) {
				return SessionMetadata{}, fmt.Errorf("read file: %w", readErr)
			}
			break
		}

		// parse key-value pairs (process line before checking error,
		// as ReadString may return partial data alongside an error)
		if val, found := strings.CutPrefix(line, "Plan: "); found {
			meta.PlanPath = val
		} else if val, found := strings.CutPrefix(line, "Branch: "); found {
			meta.Branch = val
		} else if val, found := strings.CutPrefix(line, "Mode: "); found {
			meta.Mode = val
		} else if val, found := strings.CutPrefix(line, "Started: "); found {
			// header timestamps are written in local time without a zone offset
			if t, parseErr := time.ParseInLocation("2006-01-02 15:04:05", val, time.Local); parseErr == nil {
				meta.StartTime = t
			}
		}

		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return SessionMetadata{}, fmt.Errorf("read file: %w", readErr)
			}
			break // EOF after processing final line
		}
	}

	return meta, nil
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
	var bytesRead int64

	for {
		line, readErr := reader.ReadString('\n')
		// count raw bytes including the delimiter before trimming, so LF, CRLF,
		// and the final no-trailing-newline read all count correctly
		bytesRead += int64(len(line))
		line = trimLineEnding(line)

		if line != "" {
			var parsed ParsedLine
			parsed, inHeader = parseProgressLine(line, inHeader)
			phase, pendingSection = m.processProgressLine(session, parsed, phase, pendingSection)
		}

		if readErr != nil {
			break // EOF or read error; best-effort loading, errors silently ignored
		}
	}

	if pendingSection != "" {
		m.emitPendingSection(session, pendingSection, phase, time.Now())
	}

	session.setLastOffset(bytesRead)
}

// processProgressLine handles a single parsed progress line,
// updating phase and pendingSection state and publishing events as needed.
func (m *SessionManager) processProgressLine(session *Session, parsed ParsedLine,
	phase status.Phase, pendingSection string) (status.Phase, string) {
	switch parsed.Type {
	case ParsedLineSkip:
		return phase, pendingSection
	case ParsedLineSection:
		if pendingSection != "" {
			m.emitPendingSection(session, pendingSection, phase, time.Now())
		}
		phase = parsed.Phase
		// defer emitting section until we see a timestamped event
		return phase, parsed.Section
	case ParsedLineTimestamp:
		// emit pending section with this event's timestamp (for accurate durations)
		if pendingSection != "" {
			m.emitPendingSection(session, pendingSection, phase, parsed.Timestamp)
			pendingSection = ""
		}
		event := Event{
			Type:      parsed.EventType,
			Phase:     phase,
			Text:      parsed.Text,
			Timestamp: parsed.Timestamp,
			Signal:    parsed.Signal,
		}
		if event.Type == EventTypeOutput {
			if stats, ok := parseDiffStats(event.Text); ok {
				session.SetDiffStats(stats)
			}
		}
		_ = session.Publish(event)
	case ParsedLinePlain:
		_ = session.Publish(Event{
			Type:      EventTypeOutput,
			Phase:     phase,
			Text:      parsed.Text,
			Timestamp: time.Now(),
		})
	}
	return phase, pendingSection
}

// emitPendingSection publishes section and task_start events for a pending section.
// task_start is emitted before section for task iteration sections.
func (m *SessionManager) emitPendingSection(session *Session, sectionName string, phase status.Phase, ts time.Time) {
	// emit task_start event for task iteration sections
	if matches := taskIterationRegex.FindStringSubmatch(sectionName); matches != nil {
		taskNum, err := strconv.Atoi(matches[1])
		if err != nil {
			// log parse error but continue - section will still be emitted
			log.Printf("[WARN] failed to parse task number from section %q: %v", sectionName, err)
		} else {
			if err := session.Publish(Event{
				Type:      EventTypeTaskStart,
				Phase:     phase,
				TaskNum:   taskNum,
				Text:      sectionName,
				Timestamp: ts,
			}); err != nil {
				log.Printf("[WARN] failed to publish task_start event: %v", err)
			}
		}
	}

	if err := session.Publish(Event{
		Type:      EventTypeSection,
		Phase:     phase,
		Section:   sectionName,
		Text:      sectionName,
		Timestamp: ts,
	}); err != nil {
		log.Printf("[WARN] failed to publish section event: %v", err)
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
