package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/status"
)

func TestParseProgressLine(t *testing.T) {
	t.Run("timestamped output line", func(t *testing.T) {
		parsed, inHeader := parseProgressLine("[26-01-22 10:30:45] Hello world", false)
		assert.False(t, inHeader)
		assert.Equal(t, ParsedLineTimestamp, parsed.Type)
		assert.Equal(t, "Hello world", parsed.Text)
		assert.Equal(t, EventTypeOutput, parsed.EventType)
		assert.Empty(t, parsed.Signal)
		assert.Equal(t, 2026, parsed.Timestamp.Year())
		assert.Equal(t, time.January, parsed.Timestamp.Month())
		assert.Equal(t, 22, parsed.Timestamp.Day())
	})

	t.Run("section header", func(t *testing.T) {
		parsed, inHeader := parseProgressLine("--- task iteration 1 ---", false)
		assert.False(t, inHeader)
		assert.Equal(t, ParsedLineSection, parsed.Type)
		assert.Equal(t, "task iteration 1", parsed.Section)
		assert.Equal(t, "task iteration 1", parsed.Text)
		assert.Equal(t, status.PhaseTask, parsed.Phase)
	})

	t.Run("review section header", func(t *testing.T) {
		parsed, inHeader := parseProgressLine("--- Review Iteration 2 ---", false)
		assert.False(t, inHeader)
		assert.Equal(t, ParsedLineSection, parsed.Type)
		assert.Equal(t, "Review Iteration 2", parsed.Section)
		assert.Equal(t, status.PhaseReview, parsed.Phase)
	})

	t.Run("codex section header", func(t *testing.T) {
		parsed, inHeader := parseProgressLine("--- Codex Review ---", false)
		assert.False(t, inHeader)
		assert.Equal(t, ParsedLineSection, parsed.Type)
		assert.Equal(t, "Codex Review", parsed.Section)
		assert.Equal(t, status.PhaseCodex, parsed.Phase)
	})

	t.Run("error line", func(t *testing.T) {
		parsed, inHeader := parseProgressLine("[26-01-22 10:30:45] ERROR: something failed", false)
		assert.False(t, inHeader)
		assert.Equal(t, ParsedLineTimestamp, parsed.Type)
		assert.Equal(t, EventTypeError, parsed.EventType)
		assert.Equal(t, "ERROR: something failed", parsed.Text)
	})

	t.Run("warning line", func(t *testing.T) {
		parsed, inHeader := parseProgressLine("[26-01-22 10:30:45] WARN: be careful", false)
		assert.False(t, inHeader)
		assert.Equal(t, ParsedLineTimestamp, parsed.Type)
		assert.Equal(t, EventTypeWarn, parsed.EventType)
		assert.Equal(t, "WARN: be careful", parsed.Text)
	})

	t.Run("signal line with ralphex prefix", func(t *testing.T) {
		parsed, inHeader := parseProgressLine("[26-01-22 10:30:45] <<<RALPHEX:ALL_TASKS_DONE>>>", false)
		assert.False(t, inHeader)
		assert.Equal(t, ParsedLineTimestamp, parsed.Type)
		assert.Equal(t, EventTypeSignal, parsed.EventType)
		assert.Equal(t, "COMPLETED", parsed.Signal)
	})

	t.Run("signal line with review done", func(t *testing.T) {
		parsed, inHeader := parseProgressLine("[26-01-22 10:30:45] <<<RALPHEX:REVIEW_DONE>>>", false)
		assert.False(t, inHeader)
		assert.Equal(t, EventTypeSignal, parsed.EventType)
		assert.Equal(t, "REVIEW_DONE", parsed.Signal)
	})

	t.Run("signal line with codex review done", func(t *testing.T) {
		parsed, inHeader := parseProgressLine("[26-01-22 10:30:45] <<<RALPHEX:CODEX_REVIEW_DONE>>>", false)
		assert.False(t, inHeader)
		assert.Equal(t, EventTypeSignal, parsed.EventType)
		assert.Equal(t, "CODEX_REVIEW_DONE", parsed.Signal)
	})

	t.Run("plain line without timestamp", func(t *testing.T) {
		parsed, inHeader := parseProgressLine("plain text line", false)
		assert.False(t, inHeader)
		assert.Equal(t, ParsedLinePlain, parsed.Type)
		assert.Equal(t, "plain text line", parsed.Text)
		assert.Equal(t, EventTypeOutput, parsed.EventType)
	})

	t.Run("header separator exits header mode", func(t *testing.T) {
		parsed, inHeader := parseProgressLine("------------------------------------------------------------", true)
		assert.False(t, inHeader, "should exit header mode after separator")
		assert.Equal(t, ParsedLineSkip, parsed.Type)
	})

	t.Run("header lines are skipped while in header", func(t *testing.T) {
		parsed, inHeader := parseProgressLine("Plan: /path/to/plan.md", true)
		assert.True(t, inHeader, "should remain in header mode")
		assert.Equal(t, ParsedLineSkip, parsed.Type)
	})

	t.Run("header separator does not affect non-header mode", func(t *testing.T) {
		parsed, inHeader := parseProgressLine("------------------------------------------------------------", false)
		assert.False(t, inHeader)
		assert.Equal(t, ParsedLineSkip, parsed.Type)
	})

	t.Run("short dash line is not separator", func(t *testing.T) {
		parsed, inHeader := parseProgressLine("---", false)
		assert.False(t, inHeader)
		// "---" doesn't match section regex (needs " text ") and is not a timestamp
		assert.Equal(t, ParsedLinePlain, parsed.Type)
	})
}

func TestParseProgressLine_TableDriven(t *testing.T) {
	tests := []struct {
		name         string
		line         string
		inHeader     bool
		wantType     ParsedLineType
		wantInHeader bool
		wantText     string
		wantSection  string
		wantEvtType  EventType
		wantSignal   string
	}{
		{"timestamped output", "[26-01-22 10:30:45] output", false, ParsedLineTimestamp, false, "output", "", EventTypeOutput, ""},
		{"timestamped error", "[26-01-22 10:30:45] ERROR: fail", false, ParsedLineTimestamp, false, "ERROR: fail", "", EventTypeError, ""},
		{"timestamped warn", "[26-01-22 10:30:45] WARN: caution", false, ParsedLineTimestamp, false, "WARN: caution", "", EventTypeWarn, ""},
		{"timestamped signal", "[26-01-22 10:30:45] <<<RALPHEX:FAILED>>>", false, ParsedLineTimestamp, false, "<<<RALPHEX:FAILED>>>", "", EventTypeSignal, "FAILED"},
		{"task section", "--- task iteration 3 ---", false, ParsedLineSection, false, "task iteration 3", "task iteration 3", EventTypeOutput, ""},
		{"review section", "--- Review ---", false, ParsedLineSection, false, "Review", "Review", EventTypeOutput, ""},
		{"codex section", "--- Codex Analysis ---", false, ParsedLineSection, false, "Codex Analysis", "Codex Analysis", EventTypeOutput, ""},
		{"claude-eval section", "--- Claude-Eval ---", false, ParsedLineSection, false, "Claude-Eval", "Claude-Eval", EventTypeOutput, ""},
		{"plain text", "hello world", false, ParsedLinePlain, false, "hello world", "", EventTypeOutput, ""},
		{"header separator", "------------------------------------------------------------", true, ParsedLineSkip, false, "", "", EventTypeOutput, ""},
		{"header line", "Branch: main", true, ParsedLineSkip, true, "", "", EventTypeOutput, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, gotInHeader := parseProgressLine(tt.line, tt.inHeader)
			assert.Equal(t, tt.wantType, parsed.Type, "line type")
			assert.Equal(t, tt.wantInHeader, gotInHeader, "inHeader state")
			if tt.wantText != "" {
				assert.Equal(t, tt.wantText, parsed.Text, "text")
			}
			if tt.wantSection != "" {
				assert.Equal(t, tt.wantSection, parsed.Section, "section")
			}
			if tt.wantType == ParsedLineTimestamp {
				assert.Equal(t, tt.wantEvtType, parsed.EventType, "event type")
			}
			if tt.wantSignal != "" {
				assert.Equal(t, tt.wantSignal, parsed.Signal, "signal")
			}
		})
	}
}

func TestParseProgressLine_ParityTailVsReplay(t *testing.T) {
	// verify that live tailing (Tailer.parseLine) and manual parseProgressLine produce
	// equivalent results for the same input lines.

	progressContent := `# Ralphex Progress Log
Plan: docs/plan.md
Branch: main
Mode: full
Started: 2026-01-22 10:00:00
------------------------------------------------------------

--- Task Iteration 1 ---
[26-01-22 10:00:01] starting first task
[26-01-22 10:00:02] working on implementation
[26-01-22 10:00:03] ERROR: compilation failed
[26-01-22 10:00:04] WARN: retrying build
--- Review ---
[26-01-22 10:00:05] reviewing code changes
[26-01-22 10:00:06] <<<RALPHEX:REVIEW_DONE>>>
--- Codex Review ---
[26-01-22 10:00:07] codex analysis in progress
[26-01-22 10:00:08] <<<RALPHEX:CODEX_REVIEW_DONE>>>
--- Claude-Eval ---
[26-01-22 10:00:09] evaluating results
[26-01-22 10:00:10] <<<RALPHEX:ALL_TASKS_DONE>>>
`

	dir := t.TempDir()
	path := filepath.Join(dir, "progress-parity.txt")
	require.NoError(t, os.WriteFile(path, []byte(progressContent), 0o600))

	// collect events from live tailing (Tailer)
	tailer := NewTailer(path, TailerConfig{PollInterval: 10 * time.Millisecond, InitialPhase: status.PhaseTask})
	require.NoError(t, tailer.Start(true))

	var liveEvents []Event
	timeout := time.After(2 * time.Second)
loop:
	for {
		select {
		case event := <-tailer.Events():
			liveEvents = append(liveEvents, event)
		case <-timeout:
			break loop
		}
	}
	tailer.Stop()

	// simulate replay path: walk lines through parseProgressLine manually, building events
	// the same way loadProgressFileIntoSession does
	var replayEvents []Event
	inHeader := true
	phase := status.PhaseTask

	for line := range strings.SplitSeq(progressContent, "\n") {
		if line == "" {
			continue
		}

		parsed, newInHeader := parseProgressLine(line, inHeader)
		inHeader = newInHeader

		switch parsed.Type {
		case ParsedLineSkip:
			continue
		case ParsedLineSection:
			phase = parsed.Phase
			replayEvents = append(replayEvents, Event{
				Type:    EventTypeSection,
				Phase:   phase,
				Section: parsed.Section,
				Text:    parsed.Text,
			})
		case ParsedLineTimestamp:
			replayEvents = append(replayEvents, Event{
				Type:      parsed.EventType,
				Phase:     phase,
				Text:      parsed.Text,
				Timestamp: parsed.Timestamp,
				Signal:    parsed.Signal,
			})
		case ParsedLinePlain:
			replayEvents = append(replayEvents, Event{
				Type:  EventTypeOutput,
				Phase: phase,
				Text:  parsed.Text,
			})
		}
	}

	// both should have events
	require.NotEmpty(t, liveEvents, "live tailer should produce events")
	require.NotEmpty(t, replayEvents, "replay should produce events")

	// extract only timestamped content events (section timestamps differ by design -
	// tailer uses time.Now(), replay uses parsed timestamp or skips)
	liveTimestamped := filterTimestampedEvents(liveEvents)
	replayTimestamped := filterTimestampedEvents(replayEvents)

	require.Len(t, replayTimestamped, len(liveTimestamped),
		"live and replay should produce same number of timestamped events")

	for i := range liveTimestamped {
		live := liveTimestamped[i]
		replay := replayTimestamped[i]

		assert.Equal(t, live.Text, replay.Text, "event %d text mismatch", i)
		assert.Equal(t, live.Type, replay.Type, "event %d type mismatch for text %q", i, live.Text)
		assert.Equal(t, live.Phase, replay.Phase, "event %d phase mismatch for text %q", i, live.Text)
		assert.Equal(t, live.Signal, replay.Signal, "event %d signal mismatch for text %q", i, live.Text)
		assert.Equal(t, live.Timestamp.Unix(), replay.Timestamp.Unix(),
			"event %d timestamp mismatch for text %q", i, live.Text)
	}

	// verify both paths detect the same sections
	liveSections := filterSectionEvents(liveEvents)
	replaySections := filterSectionEvents(replayEvents)
	require.Len(t, replaySections, len(liveSections),
		"live and replay should detect same number of sections")

	for i := range liveSections {
		assert.Equal(t, liveSections[i].Section, replaySections[i].Section,
			"section %d name mismatch", i)
		assert.Equal(t, liveSections[i].Phase, replaySections[i].Phase,
			"section %d phase mismatch", i)
	}
}

// filterTimestampedEvents returns only events from timestamped lines
// (output, error, warn, signal types, excluding section and task_start).
func filterTimestampedEvents(events []Event) []Event {
	var result []Event
	for _, e := range events {
		switch e.Type {
		case EventTypeOutput, EventTypeError, EventTypeWarn, EventTypeSignal:
			result = append(result, e)
		case EventTypeSection, EventTypeTaskStart, EventTypeTaskEnd, EventTypeIterationStart:
			// skip non-content events
		}
	}
	return result
}

// filterSectionEvents returns only section events.
func filterSectionEvents(events []Event) []Event {
	var result []Event
	for _, e := range events {
		if e.Type == EventTypeSection {
			result = append(result, e)
		}
	}
	return result
}

func TestIsHeaderSeparator(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"------------------------------------------------------------", true},
		{"------------------------------", true},
		{"---------------------", true},
		{"---", false},
		{"--------------------", false},
		{"--- section name ---", false},
		{"abc", false},
		{"", false},
		{"--", false},
		{"--------- text ---------", false},
	}

	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			assert.Equal(t, tt.want, isHeaderSeparator(tt.line))
		})
	}
}
