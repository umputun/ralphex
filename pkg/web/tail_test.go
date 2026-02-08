package web

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/status"
)

func TestNewTailer(t *testing.T) {
	t.Run("creates tailer with default config", func(t *testing.T) {
		tailer := NewTailer("/tmp/test.txt", TailerConfig{})

		assert.Equal(t, "/tmp/test.txt", tailer.path)
		assert.Equal(t, 100*time.Millisecond, tailer.config.PollInterval)
		assert.Equal(t, status.PhaseTask, tailer.config.InitialPhase)
		assert.False(t, tailer.running)
	})

	t.Run("uses provided config", func(t *testing.T) {
		cfg := TailerConfig{
			PollInterval: 200 * time.Millisecond,
			InitialPhase: status.PhaseReview,
		}
		tailer := NewTailer("/tmp/test.txt", cfg)

		assert.Equal(t, 200*time.Millisecond, tailer.config.PollInterval)
		assert.Equal(t, status.PhaseReview, tailer.config.InitialPhase)
	})
}

func TestTailer_ParseLine(t *testing.T) {
	tailer := NewTailer("/tmp/test.txt", DefaultTailerConfig())
	tailer.inHeader = false // skip header handling for these tests

	t.Run("parses timestamped line", func(t *testing.T) {
		event := tailer.parseLine("[26-01-22 10:30:45] Hello world")

		require.NotNil(t, event)
		assert.Equal(t, EventTypeOutput, event.Type)
		assert.Equal(t, "Hello world", event.Text)
		assert.Equal(t, status.PhaseTask, event.Phase)
		assert.Equal(t, 2026, event.Timestamp.Year())
		assert.Equal(t, time.January, event.Timestamp.Month())
		assert.Equal(t, 22, event.Timestamp.Day())
	})

	t.Run("parses section header", func(t *testing.T) {
		event := tailer.parseLine("--- task iteration 1 ---")

		require.NotNil(t, event)
		assert.Equal(t, EventTypeSection, event.Type)
		assert.Equal(t, "task iteration 1", event.Section)
		assert.Equal(t, "task iteration 1", event.Text)
	})

	t.Run("detects error lines", func(t *testing.T) {
		event := tailer.parseLine("[26-01-22 10:30:45] ERROR: something went wrong")

		require.NotNil(t, event)
		assert.Equal(t, EventTypeError, event.Type)
		assert.Equal(t, "ERROR: something went wrong", event.Text)
	})

	t.Run("detects warning lines", func(t *testing.T) {
		event := tailer.parseLine("[26-01-22 10:30:45] WARN: be careful")

		require.NotNil(t, event)
		assert.Equal(t, EventTypeWarn, event.Type)
		assert.Equal(t, "WARN: be careful", event.Text)
	})

	t.Run("detects signal lines", func(t *testing.T) {
		event := tailer.parseLine("[26-01-22 10:30:45] <<<RALPHEX:COMPLETED>>>")

		require.NotNil(t, event)
		assert.Equal(t, EventTypeSignal, event.Type)
		assert.Equal(t, "COMPLETED", event.Signal)
	})

	t.Run("handles plain line without timestamp", func(t *testing.T) {
		event := tailer.parseLine("plain text line")

		require.NotNil(t, event)
		assert.Equal(t, EventTypeOutput, event.Type)
		assert.Equal(t, "plain text line", event.Text)
	})

	t.Run("skips header lines", func(t *testing.T) {
		tailer := NewTailer("/tmp/test.txt", DefaultTailerConfig())
		tailer.inHeader = true

		event := tailer.parseLine("Plan: /path/to/plan.md")
		assert.Nil(t, event)

		event = tailer.parseLine("Branch: main")
		assert.Nil(t, event)
	})

	t.Run("exits header mode on separator", func(t *testing.T) {
		tailer := NewTailer("/tmp/test.txt", DefaultTailerConfig())
		tailer.inHeader = true

		event := tailer.parseLine("------------------------------------------------------------")
		assert.Nil(t, event)
		assert.False(t, tailer.inHeader)

		// now regular lines should be parsed
		event = tailer.parseLine("[26-01-22 10:30:45] Hello")
		require.NotNil(t, event)
		assert.Equal(t, "Hello", event.Text)
	})
}

func TestTailer_PhaseFromSection(t *testing.T) {
	tests := []struct {
		name     string
		section  string
		expected status.Phase
	}{
		{"task section", "task iteration 1", status.PhaseTask},
		{"review section", "review iteration 2", status.PhaseReview},
		{"codex section", "codex analysis", status.PhaseCodex},
		{"claude-eval section", "claude-eval phase", status.PhaseClaudeEval},
		{"claude eval section", "claude eval phase", status.PhaseClaudeEval},
		{"uppercase task", "TASK Phase", status.PhaseTask},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			line := "--- " + tt.section + " ---"
			parsed, _ := parseProgressLine(line, false)
			assert.Equal(t, ParsedLineSection, parsed.Type)
			assert.Equal(t, tt.expected, parsed.Phase)
		})
	}
}

func TestTailer_StartStop(t *testing.T) {
	t.Run("starts and stops tailing", func(t *testing.T) {
		tmpDir := t.TempDir()
		progressFile := filepath.Join(tmpDir, "progress-test.txt")

		// create a progress file with content
		content := `# Ralphex Progress Log
Plan: test.md
Branch: main
Mode: full
Started: 2026-01-22 10:30:00
------------------------------------------------------------

[26-01-22 10:30:01] First line
`
		err := os.WriteFile(progressFile, []byte(content), 0o600)
		require.NoError(t, err)

		tailer := NewTailer(progressFile, TailerConfig{
			PollInterval: 10 * time.Millisecond,
			InitialPhase: status.PhaseTask,
		})

		err = tailer.Start(true)
		require.NoError(t, err)
		assert.True(t, tailer.IsRunning())

		// wait for events
		var events []Event
		timeout := time.After(500 * time.Millisecond)
	loop:
		for {
			select {
			case event := <-tailer.Events():
				events = append(events, event)
			case <-timeout:
				break loop
			}
		}

		tailer.Stop()
		assert.False(t, tailer.IsRunning())

		// should have received at least the first line
		require.GreaterOrEqual(t, len(events), 1)
		found := false
		for _, e := range events {
			if e.Text == "First line" {
				found = true
				break
			}
		}
		assert.True(t, found, "should have received 'First line' event")
	})

	t.Run("tails new content", func(t *testing.T) {
		tmpDir := t.TempDir()
		progressFile := filepath.Join(tmpDir, "progress-test.txt")

		// create initial file
		initial := `# Ralphex Progress Log
Plan: test.md
Branch: main
Mode: full
Started: 2026-01-22 10:30:00
------------------------------------------------------------

`
		err := os.WriteFile(progressFile, []byte(initial), 0o600)
		require.NoError(t, err)

		tailer := NewTailer(progressFile, TailerConfig{
			PollInterval: 10 * time.Millisecond,
			InitialPhase: status.PhaseTask,
		})

		err = tailer.Start(true)
		require.NoError(t, err)

		// append new content
		f, err := os.OpenFile(progressFile, os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // test file path
		require.NoError(t, err)
		_, err = f.WriteString("[26-01-22 10:30:02] New line added\n")
		require.NoError(t, err)
		f.Close()

		// wait for the new event
		var found bool
		timeout := time.After(500 * time.Millisecond)
	loop:
		for !found {
			select {
			case event := <-tailer.Events():
				if event.Text == "New line added" {
					found = true
				}
			case <-timeout:
				break loop
			}
		}

		tailer.Stop()
		assert.True(t, found, "should have received 'New line added' event")
	})

	t.Run("start from end skips existing content", func(t *testing.T) {
		tmpDir := t.TempDir()
		progressFile := filepath.Join(tmpDir, "progress-test.txt")

		content := `# Ralphex Progress Log
Plan: test.md
Branch: main
Mode: full
Started: 2026-01-22 10:30:00
------------------------------------------------------------

[26-01-22 10:30:01] Existing line
`
		err := os.WriteFile(progressFile, []byte(content), 0o600)
		require.NoError(t, err)

		tailer := NewTailer(progressFile, TailerConfig{
			PollInterval: 10 * time.Millisecond,
			InitialPhase: status.PhaseTask,
		})

		// start from end (not fromStart)
		err = tailer.Start(false)
		require.NoError(t, err)

		// should not receive the existing line
		select {
		case event := <-tailer.Events():
			t.Errorf("unexpected event: %+v", event)
		case <-time.After(100 * time.Millisecond):
			// expected - no events
		}

		// now append new content
		f, err := os.OpenFile(progressFile, os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // test file path
		require.NoError(t, err)
		_, err = f.WriteString("[26-01-22 10:30:02] New line\n")
		require.NoError(t, err)
		f.Close()

		// should receive the new line
		select {
		case event := <-tailer.Events():
			assert.Equal(t, "New line", event.Text)
		case <-time.After(500 * time.Millisecond):
			t.Error("expected to receive 'New line' event")
		}

		tailer.Stop()
	})

	t.Run("fails on non-existent file", func(t *testing.T) {
		tailer := NewTailer("/nonexistent/file.txt", DefaultTailerConfig())

		err := tailer.Start(true)
		require.Error(t, err)
		assert.False(t, tailer.IsRunning())
	})

	t.Run("start is idempotent", func(t *testing.T) {
		tmpDir := t.TempDir()
		progressFile := filepath.Join(tmpDir, "progress-test.txt")
		err := os.WriteFile(progressFile, []byte("test"), 0o600)
		require.NoError(t, err)

		tailer := NewTailer(progressFile, DefaultTailerConfig())

		err = tailer.Start(true)
		require.NoError(t, err)

		// second start should be no-op
		err = tailer.Start(true)
		require.NoError(t, err)

		tailer.Stop()
	})
}

func TestTailer_Stop(t *testing.T) {
	t.Run("stop before start is safe", func(t *testing.T) {
		tailer := NewTailer("/nonexistent", DefaultTailerConfig())

		// should not panic
		tailer.Stop()
		assert.False(t, tailer.IsRunning())
	})

	t.Run("concurrent stop calls are safe", func(t *testing.T) {
		tmpDir := t.TempDir()
		progressFile := filepath.Join(tmpDir, "progress-test.txt")
		err := os.WriteFile(progressFile, []byte("test"), 0o600)
		require.NoError(t, err)

		tailer := NewTailer(progressFile, TailerConfig{
			PollInterval: 10 * time.Millisecond,
		})

		err = tailer.Start(true)
		require.NoError(t, err)

		// launch multiple concurrent stop calls
		done := make(chan struct{})
		for range 10 {
			go func() {
				tailer.Stop()
				done <- struct{}{}
			}()
		}

		// wait for all to complete
		for range 10 {
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("timeout waiting for concurrent stops")
			}
		}

		assert.False(t, tailer.IsRunning())
	})

	t.Run("stop blocks until goroutine exits", func(t *testing.T) {
		tmpDir := t.TempDir()
		progressFile := filepath.Join(tmpDir, "progress-test.txt")
		err := os.WriteFile(progressFile, []byte("test"), 0o600)
		require.NoError(t, err)

		tailer := NewTailer(progressFile, TailerConfig{
			PollInterval: 10 * time.Millisecond,
		})

		err = tailer.Start(true)
		require.NoError(t, err)
		assert.True(t, tailer.IsRunning())

		tailer.Stop()

		// immediately after Stop returns, IsRunning should be false
		assert.False(t, tailer.IsRunning())
	})
}

func TestDetectEventType(t *testing.T) {
	tests := []struct {
		text     string
		expected EventType
	}{
		{"ERROR: something failed", EventTypeError},
		{"error: lowercase", EventTypeError},
		{"WARN: be careful", EventTypeWarn},
		{"warn: lowercase", EventTypeWarn},
		{"<<<RALPHEX:COMPLETED>>>", EventTypeSignal},
		{"ALL_TASKS_DONE", EventTypeSignal},
		{"normal output", EventTypeOutput},
	}

	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			result := detectEventType(tt.text)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractSignalFromText(t *testing.T) {
	tests := []struct {
		text     string
		expected string
	}{
		{"<<<RALPHEX:COMPLETED>>>", "COMPLETED"},
		{"<<<RALPHEX:FAILED>>>", "FAILED"},
		{"<<<RALPHEX:REVIEW_DONE>>>", "REVIEW_DONE"},
		{"ALL_TASKS_DONE", "COMPLETED"},
		{"TASK_FAILED", "FAILED"},
		{"REVIEW_DONE", "REVIEW_DONE"},
		{"CODEX_REVIEW_DONE", "CODEX_REVIEW_DONE"},
		{"COMPLETED", "COMPLETED"},
		{"FAILED", "FAILED"},
		{"some text <<<RALPHEX:SIGNAL>>> more text", "SIGNAL"},
		{"no signal here", ""},
		{"<<<RALPHEX:incomplete", ""},
	}

	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			result := extractSignalFromText(tt.text)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTailer_ParseLineDeferred(t *testing.T) {
	t.Run("defers section until timestamped line", func(t *testing.T) {
		tailer := NewTailer("/tmp/test.txt", DefaultTailerConfig())
		tailer.inHeader = false
		tailer.deferSections = true

		// section header alone should not produce events yet
		events := tailer.parseLineDeferred("--- task iteration 1 ---")
		assert.Empty(t, events)
		assert.Equal(t, "task iteration 1", tailer.pendingSection)

		// timestamped line should emit section + content
		events = tailer.parseLineDeferred("[26-01-22 10:30:45] Hello world")
		require.Len(t, events, 3) // TaskStart + Section + Output
		assert.Equal(t, EventTypeTaskStart, events[0].Type)
		assert.Equal(t, 1, events[0].TaskNum)
		assert.Equal(t, EventTypeSection, events[1].Type)
		assert.Equal(t, "task iteration 1", events[1].Section)
		assert.Equal(t, EventTypeOutput, events[2].Type)
		assert.Equal(t, "Hello world", events[2].Text)

		// all three events should share the same timestamp from the content line
		assert.Equal(t, events[2].Timestamp, events[0].Timestamp)
		assert.Equal(t, events[2].Timestamp, events[1].Timestamp)
		assert.Empty(t, tailer.pendingSection)
	})

	t.Run("defers section until plain line", func(t *testing.T) {
		tailer := NewTailer("/tmp/test.txt", DefaultTailerConfig())
		tailer.inHeader = false
		tailer.deferSections = true

		events := tailer.parseLineDeferred("--- review iteration 1 ---")
		assert.Empty(t, events)

		// plain line triggers deferred emission with time.Now()
		events = tailer.parseLineDeferred("some plain text")
		require.Len(t, events, 2) // Section + Output (review sections don't produce TaskStart)
		assert.Equal(t, EventTypeSection, events[0].Type)
		assert.Equal(t, "review iteration 1", events[0].Section)
		assert.Equal(t, status.PhaseReview, events[0].Phase)
		assert.Equal(t, EventTypeOutput, events[1].Type)
		assert.Equal(t, "some plain text", events[1].Text)
	})

	t.Run("consecutive sections flush previous", func(t *testing.T) {
		tailer := NewTailer("/tmp/test.txt", DefaultTailerConfig())
		tailer.inHeader = false
		tailer.deferSections = true

		// first section - queued
		events := tailer.parseLineDeferred("--- task iteration 1 ---")
		assert.Empty(t, events)

		// second section - should flush the first
		events = tailer.parseLineDeferred("--- task iteration 2 ---")
		require.Len(t, events, 2) // TaskStart + Section for task 1
		assert.Equal(t, EventTypeTaskStart, events[0].Type)
		assert.Equal(t, 1, events[0].TaskNum)
		assert.Equal(t, EventTypeSection, events[1].Type)
		assert.Equal(t, "task iteration 1", events[1].Section)

		// pending should now be task 2
		assert.Equal(t, "task iteration 2", tailer.pendingSection)
	})

	t.Run("skips header lines", func(t *testing.T) {
		tailer := NewTailer("/tmp/test.txt", DefaultTailerConfig())
		tailer.inHeader = true
		tailer.deferSections = true

		events := tailer.parseLineDeferred("Plan: /path/to/plan.md")
		assert.Empty(t, events)

		events = tailer.parseLineDeferred("------------------------------------------------------------")
		assert.Empty(t, events)
		assert.False(t, tailer.inHeader)
	})

	t.Run("updates phase from section", func(t *testing.T) {
		tailer := NewTailer("/tmp/test.txt", DefaultTailerConfig())
		tailer.inHeader = false
		tailer.deferSections = true

		_ = tailer.parseLineDeferred("--- codex analysis ---")
		events := tailer.parseLineDeferred("[26-01-22 10:30:45] codex output")

		require.Len(t, events, 2) // Section + Output
		assert.Equal(t, status.PhaseCodex, events[0].Phase)
		assert.Equal(t, status.PhaseCodex, events[1].Phase)
	})
}

func TestTailer_EmitPendingSection(t *testing.T) {
	ts := time.Date(2026, 1, 22, 10, 30, 0, 0, time.UTC)

	t.Run("task iteration emits TaskStart and Section", func(t *testing.T) {
		tailer := NewTailer("/tmp/test.txt", DefaultTailerConfig())
		tailer.pendingSection = "task iteration 3"
		tailer.pendingPhase = status.PhaseTask

		events := tailer.emitPendingSection(ts)
		require.Len(t, events, 2)

		assert.Equal(t, EventTypeTaskStart, events[0].Type)
		assert.Equal(t, 3, events[0].TaskNum)
		assert.Equal(t, status.PhaseTask, events[0].Phase)
		assert.Equal(t, ts, events[0].Timestamp)

		assert.Equal(t, EventTypeSection, events[1].Type)
		assert.Equal(t, "task iteration 3", events[1].Section)
		assert.Equal(t, status.PhaseTask, events[1].Phase)
		assert.Equal(t, ts, events[1].Timestamp)

		// pending should be cleared
		assert.Empty(t, tailer.pendingSection)
	})

	t.Run("non-task section emits Section only", func(t *testing.T) {
		tailer := NewTailer("/tmp/test.txt", DefaultTailerConfig())
		tailer.pendingSection = "review iteration 1"
		tailer.pendingPhase = status.PhaseReview

		events := tailer.emitPendingSection(ts)
		require.Len(t, events, 1)

		assert.Equal(t, EventTypeSection, events[0].Type)
		assert.Equal(t, "review iteration 1", events[0].Section)
		assert.Equal(t, status.PhaseReview, events[0].Phase)
		assert.Equal(t, ts, events[0].Timestamp)
	})

	t.Run("empty pending returns nil", func(t *testing.T) {
		tailer := NewTailer("/tmp/test.txt", DefaultTailerConfig())
		tailer.pendingSection = ""

		events := tailer.emitPendingSection(ts)
		assert.Nil(t, events)
	})

	t.Run("case-insensitive task iteration match", func(t *testing.T) {
		tailer := NewTailer("/tmp/test.txt", DefaultTailerConfig())
		tailer.pendingSection = "Task Iteration 5"
		tailer.pendingPhase = status.PhaseTask

		events := tailer.emitPendingSection(ts)
		require.Len(t, events, 2)
		assert.Equal(t, EventTypeTaskStart, events[0].Type)
		assert.Equal(t, 5, events[0].TaskNum)
	})
}

func TestTailer_SendEvent(t *testing.T) {
	t.Run("enqueues when channel has space", func(t *testing.T) {
		tailer := NewTailer("/tmp/test.txt", DefaultTailerConfig())
		event := Event{Type: EventTypeOutput, Text: "hello"}

		tailer.sendEvent(event)

		select {
		case got := <-tailer.eventCh:
			assert.Equal(t, "hello", got.Text)
		default:
			t.Fatal("expected event in channel")
		}
	})

	t.Run("drops non-priority event when channel full", func(t *testing.T) {
		// create tailer with tiny channel for testing
		tailer := &Tailer{
			eventCh: make(chan Event, 1),
		}

		// fill the channel
		tailer.eventCh <- Event{Type: EventTypeOutput, Text: "filler"}

		// non-priority event should be silently dropped
		tailer.sendEvent(Event{Type: EventTypeOutput, Text: "dropped"})

		got := <-tailer.eventCh
		assert.Equal(t, "filler", got.Text, "original event should remain")
	})

	t.Run("priority event displaces when channel full", func(t *testing.T) {
		tailer := &Tailer{
			eventCh: make(chan Event, 1),
		}

		// fill with non-priority
		tailer.eventCh <- Event{Type: EventTypeOutput, Text: "old"}

		// priority event should displace the old one
		tailer.sendEvent(Event{Type: EventTypeSection, Text: "important"})

		got := <-tailer.eventCh
		assert.Equal(t, "important", got.Text, "priority event should displace old event")
	})
}

func TestIsPriorityEvent(t *testing.T) {
	tests := []struct {
		name     string
		evType   EventType
		expected bool
	}{
		{"section is priority", EventTypeSection, true},
		{"task_start is priority", EventTypeTaskStart, true},
		{"task_end is priority", EventTypeTaskEnd, true},
		{"signal is priority", EventTypeSignal, true},
		{"output is not priority", EventTypeOutput, false},
		{"error is not priority", EventTypeError, false},
		{"warn is not priority", EventTypeWarn, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, isPriorityEvent(tt.evType))
		})
	}
}

func TestNormalizeTokenSignal(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"ALL_TASKS_DONE", "COMPLETED"},
		{"TASK_FAILED", "FAILED"},
		{"ALL_TASKS_FAILED", "FAILED"},
		{"REVIEW_DONE", "REVIEW_DONE"},
		{"CODEX_REVIEW_DONE", "CODEX_REVIEW_DONE"},
		{"UNKNOWN_SIGNAL", "UNKNOWN_SIGNAL"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := normalizeTokenSignal(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
