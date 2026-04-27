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
		require.Len(t, events, 2) // section + Output (review sections don't produce TaskStart)
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

		require.Len(t, events, 2) // section + Output
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

func TestTailer_Offset(t *testing.T) {
	t.Run("offset reflects bytes consumed with LF endings", func(t *testing.T) {
		tmpDir := t.TempDir()
		progressFile := filepath.Join(tmpDir, "progress-test.txt")

		content := `# Ralphex Progress Log
Plan: test.md
Branch: main
Mode: full
Started: 2026-01-22 10:30:00
------------------------------------------------------------

[26-01-22 10:30:01] line one
[26-01-22 10:30:02] line two
`
		err := os.WriteFile(progressFile, []byte(content), 0o600)
		require.NoError(t, err)

		tailer := NewTailer(progressFile, TailerConfig{
			PollInterval: 10 * time.Millisecond,
			InitialPhase: status.PhaseTask,
		})
		require.NoError(t, tailer.Start(true))
		defer tailer.Stop()

		// wait for events to be consumed
		deadline := time.Now().Add(500 * time.Millisecond)
		for time.Now().Before(deadline) {
			if tailer.Offset() == int64(len(content)) {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}

		assert.Equal(t, int64(len(content)), tailer.Offset(),
			"offset should match raw file size including LF bytes")
	})

	t.Run("offset reflects bytes consumed with CRLF endings", func(t *testing.T) {
		tmpDir := t.TempDir()
		progressFile := filepath.Join(tmpDir, "progress-test.txt")

		// deliberately use CRLF line endings
		content := "# Ralphex Progress Log\r\n" +
			"Plan: test.md\r\n" +
			"------------------------------------------------------------\r\n" +
			"\r\n" +
			"[26-01-22 10:30:01] line one\r\n" +
			"[26-01-22 10:30:02] line two\r\n"

		err := os.WriteFile(progressFile, []byte(content), 0o600)
		require.NoError(t, err)

		tailer := NewTailer(progressFile, TailerConfig{
			PollInterval: 10 * time.Millisecond,
			InitialPhase: status.PhaseTask,
		})
		require.NoError(t, tailer.Start(true))
		defer tailer.Stop()

		deadline := time.Now().Add(500 * time.Millisecond)
		for time.Now().Before(deadline) {
			if tailer.Offset() == int64(len(content)) {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}

		assert.Equal(t, int64(len(content)), tailer.Offset(),
			"offset should include CR+LF bytes, not just LF")
	})

	t.Run("offset is zero before start", func(t *testing.T) {
		tailer := NewTailer("/tmp/nonexistent", DefaultTailerConfig())
		assert.Equal(t, int64(0), tailer.Offset())
	})
}

func TestTailer_StartFromOffset(t *testing.T) {
	t.Run("resumes from byte offset and only emits new content", func(t *testing.T) {
		tmpDir := t.TempDir()
		progressFile := filepath.Join(tmpDir, "progress-test.txt")

		prefix := `# Ralphex Progress Log
Plan: test.md
Branch: main
Mode: full
Started: 2026-01-22 10:30:00
------------------------------------------------------------

[26-01-22 10:30:01] before resume
`
		err := os.WriteFile(progressFile, []byte(prefix), 0o600)
		require.NoError(t, err)

		tailer := NewTailer(progressFile, TailerConfig{
			PollInterval: 10 * time.Millisecond,
			InitialPhase: status.PhaseTask,
		})
		require.NoError(t, tailer.StartFromOffset(int64(len(prefix))))
		defer tailer.Stop()

		// nothing pre-existing should appear
		select {
		case ev := <-tailer.Events():
			t.Fatalf("unexpected event before append: %+v", ev)
		case <-time.After(80 * time.Millisecond):
		}

		// append new content
		f, err := os.OpenFile(progressFile, os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // test file path
		require.NoError(t, err)
		_, err = f.WriteString("[26-01-22 10:30:02] after resume\n")
		require.NoError(t, err)
		require.NoError(t, f.Close())

		var found bool
		timeout := time.After(500 * time.Millisecond)
	loop:
		for !found {
			select {
			case ev := <-tailer.Events():
				assert.NotEqual(t, "before resume", ev.Text,
					"pre-offset content should not be emitted")
				if ev.Text == "after resume" {
					found = true
				}
			case <-timeout:
				break loop
			}
		}
		assert.True(t, found, "should have received 'after resume' event")
	})

	t.Run("offset beyond file size falls back to reading from beginning", func(t *testing.T) {
		// models the case where a progress file was truncated for reuse
		// (executor drops "Completed:" files and restarts). the stored
		// lastOffset from the previous run is larger than the new file
		// size, so StartFromOffset must treat this as "new file generation"
		// and read from the start rather than seeking to EOF and skipping
		// the new run's header and early lines.
		tmpDir := t.TempDir()
		progressFile := filepath.Join(tmpDir, "progress-test.txt")

		content := `# Ralphex Progress Log
Plan: test.md
Branch: main
Mode: full
Started: 2026-01-22 10:30:00
------------------------------------------------------------

[26-01-22 10:30:01] hello
`
		require.NoError(t, os.WriteFile(progressFile, []byte(content), 0o600))

		tailer := NewTailer(progressFile, TailerConfig{
			PollInterval: 10 * time.Millisecond,
			InitialPhase: status.PhaseTask,
		})

		require.NoError(t, tailer.StartFromOffset(99999))
		defer tailer.Stop()

		var got string
		timeout := time.After(500 * time.Millisecond)
	loop:
		for {
			select {
			case ev := <-tailer.Events():
				if ev.Text == "hello" {
					got = ev.Text
					break loop
				}
			case <-timeout:
				break loop
			}
		}
		assert.Equal(t, "hello", got,
			"expected tailer to read from beginning after detecting truncation")
	})

	t.Run("zero offset falls back to seek-to-end", func(t *testing.T) {
		tmpDir := t.TempDir()
		progressFile := filepath.Join(tmpDir, "progress-test.txt")

		content := `# Ralphex Progress Log
Plan: test.md
Branch: main
Mode: full
Started: 2026-01-22 10:30:00
------------------------------------------------------------

[26-01-22 10:30:01] existing line
`
		require.NoError(t, os.WriteFile(progressFile, []byte(content), 0o600))

		tailer := NewTailer(progressFile, TailerConfig{
			PollInterval: 10 * time.Millisecond,
			InitialPhase: status.PhaseTask,
		})
		require.NoError(t, tailer.StartFromOffset(0))
		defer tailer.Stop()

		// existing content should not be emitted (seek to end)
		select {
		case ev := <-tailer.Events():
			t.Fatalf("unexpected event: %+v", ev)
		case <-time.After(100 * time.Millisecond):
		}

		// appended content should appear
		f, err := os.OpenFile(progressFile, os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // test file path
		require.NoError(t, err)
		_, err = f.WriteString("[26-01-22 10:30:02] new line\n")
		require.NoError(t, err)
		require.NoError(t, f.Close())

		select {
		case ev := <-tailer.Events():
			assert.Equal(t, "new line", ev.Text)
		case <-time.After(500 * time.Millisecond):
			t.Fatal("expected 'new line' event")
		}
	})

	t.Run("negative offset falls back to seek-to-end", func(t *testing.T) {
		tmpDir := t.TempDir()
		progressFile := filepath.Join(tmpDir, "progress-test.txt")

		content := "[26-01-22 10:30:01] preexisting\n"
		require.NoError(t, os.WriteFile(progressFile, []byte(content), 0o600))

		tailer := NewTailer(progressFile, TailerConfig{
			PollInterval: 10 * time.Millisecond,
			InitialPhase: status.PhaseTask,
		})
		require.NoError(t, tailer.StartFromOffset(-42))
		defer tailer.Stop()

		select {
		case ev := <-tailer.Events():
			t.Fatalf("unexpected event on negative-offset start: %+v", ev)
		case <-time.After(100 * time.Millisecond):
		}
	})

	t.Run("fails on non-existent file", func(t *testing.T) {
		tailer := NewTailer("/nonexistent/file.txt", DefaultTailerConfig())

		err := tailer.StartFromOffset(10)
		require.Error(t, err)
		assert.False(t, tailer.IsRunning())
	})

	t.Run("start is idempotent", func(t *testing.T) {
		tmpDir := t.TempDir()
		progressFile := filepath.Join(tmpDir, "progress-test.txt")
		require.NoError(t, os.WriteFile(progressFile, []byte("hello\n"), 0o600))

		tailer := NewTailer(progressFile, TailerConfig{PollInterval: 10 * time.Millisecond})

		require.NoError(t, tailer.StartFromOffset(1))
		// second call while running should be a no-op
		require.NoError(t, tailer.StartFromOffset(3))

		tailer.Stop()
	})
}

func TestTailer_Phase(t *testing.T) {
	t.Run("phase reflects last section header processed", func(t *testing.T) {
		tmpDir := t.TempDir()
		progressFile := filepath.Join(tmpDir, "progress-test.txt")

		content := `# Ralphex Progress Log
Plan: test.md
Branch: main
Mode: full
Started: 2026-01-22 10:30:00
------------------------------------------------------------

--- task iteration 1 ---
[26-01-22 10:30:01] working on task
--- review iteration 1 ---
[26-01-22 10:30:05] reviewing
`
		require.NoError(t, os.WriteFile(progressFile, []byte(content), 0o600))

		tailer := NewTailer(progressFile, TailerConfig{
			PollInterval: 10 * time.Millisecond,
			InitialPhase: status.PhaseTask,
		})
		require.NoError(t, tailer.Start(true))
		defer tailer.Stop()

		// consume events until both sections are seen, then check phase
		deadline := time.After(500 * time.Millisecond)
		var sawReview bool
		for !sawReview {
			select {
			case ev := <-tailer.Events():
				if ev.Phase == status.PhaseReview {
					sawReview = true
				}
			case <-deadline:
				t.Fatalf("tailer never advanced to review phase; got phase=%q", tailer.Phase())
			}
		}
		assert.Equal(t, status.PhaseReview, tailer.Phase(),
			"tailer phase should reflect last section header")
	})

	t.Run("phase defaults to configured initial phase before any section", func(t *testing.T) {
		tailer := NewTailer("/tmp/nonexistent", TailerConfig{
			InitialPhase: status.PhaseCodex,
		})
		assert.Equal(t, status.PhaseCodex, tailer.Phase())
	})
}

func TestTailer_StartFromOffset_TruncationResetsStalePhase(t *testing.T) {
	// models the Reactivate-after-truncation case: the previous run ended in
	// PhaseCodex, Session.lastPhase was captured, startTailerLocked seeded
	// InitialPhase=PhaseCodex into the new tailer, then the progress file was
	// truncated for a fresh run. StartFromOffset's truncation fallback must
	// reset the parser phase so pre-section lines in the new file carry
	// PhaseTask, not the stale PhaseCodex.
	tmpDir := t.TempDir()
	progressFile := filepath.Join(tmpDir, "progress-test.txt")

	content := `# Ralphex Progress Log
Plan: test.md
Branch: main
Mode: full
Started: 2026-01-22 10:30:00
------------------------------------------------------------

[26-01-22 10:30:01] starting task execution phase
`
	require.NoError(t, os.WriteFile(progressFile, []byte(content), 0o600))

	tailer := NewTailer(progressFile, TailerConfig{
		PollInterval: 10 * time.Millisecond,
		InitialPhase: status.PhaseCodex, // stale phase from previous run
	})

	require.NoError(t, tailer.StartFromOffset(99999))
	defer tailer.Stop()

	var got Event
	timeout := time.After(500 * time.Millisecond)
loop:
	for {
		select {
		case ev := <-tailer.Events():
			if ev.Text == "starting task execution phase" {
				got = ev
				break loop
			}
		case <-timeout:
			break loop
		}
	}
	require.Equal(t, "starting task execution phase", got.Text,
		"expected tailer to emit fresh-file line after truncation fallback")
	assert.Equal(t, status.PhaseTask, got.Phase,
		"truncation fallback must reset stale phase to PhaseTask for fresh file")
}

func TestTailer_PendingSection_Accessor(t *testing.T) {
	t.Run("returns pending section and phase when deferred", func(t *testing.T) {
		// file ends with a section header but no subsequent line, so the
		// section event remains pending inside the tailer until the next line.
		tmpDir := t.TempDir()
		progressFile := filepath.Join(tmpDir, "progress-test.txt")

		content := `# Ralphex Progress Log
Plan: test.md
Branch: main
Mode: full
Started: 2026-01-22 10:30:00
------------------------------------------------------------

[26-01-22 10:30:01] before section
--- review iteration 2 ---
`
		require.NoError(t, os.WriteFile(progressFile, []byte(content), 0o600))

		tailer := NewTailer(progressFile, TailerConfig{
			PollInterval: 10 * time.Millisecond,
			InitialPhase: status.PhaseTask,
		})
		require.NoError(t, tailer.Start(true))
		defer tailer.Stop()

		// wait until the tailer has consumed the whole file
		expected := int64(len(content))
		require.Eventually(t, func() bool { return tailer.Offset() == expected },
			2*time.Second, 20*time.Millisecond, "tailer should advance to EOF")

		// drain any deferred events that did fire (e.g. "before section") so
		// we can later confirm pendingSection was not flushed
		drainEvents(tailer, 80*time.Millisecond)

		section, phase := tailer.PendingSection()
		assert.Equal(t, "review iteration 2", section,
			"expected deferred section header to remain pending")
		assert.Equal(t, status.PhaseReview, phase,
			"expected pending phase to reflect the deferred section header")
	})

	t.Run("returns empty when nothing pending", func(t *testing.T) {
		tmpDir := t.TempDir()
		progressFile := filepath.Join(tmpDir, "progress-test.txt")
		require.NoError(t, os.WriteFile(progressFile, []byte(""), 0o600))

		tailer := NewTailer(progressFile, TailerConfig{
			PollInterval: 10 * time.Millisecond,
		})
		require.NoError(t, tailer.Start(true))
		defer tailer.Stop()

		section, phase := tailer.PendingSection()
		assert.Empty(t, section)
		assert.Empty(t, phase)
	})
}

func TestTailer_StartFromOffset_PreservesPendingSection(t *testing.T) {
	// models the flock-race recovery path: a previous tailer read a section
	// header (pendingSection set) but was stopped before the next line
	// arrived. the resumed tailer must emit the pending section/task-start
	// events when new content arrives, instead of silently dropping them.
	tmpDir := t.TempDir()
	progressFile := filepath.Join(tmpDir, "progress-test.txt")

	header := `# Ralphex Progress Log
Plan: test.md
Branch: main
Mode: full
Started: 2026-01-22 10:30:00
------------------------------------------------------------

[26-01-22 10:30:01] pre-section line
--- task iteration 7 ---
`
	require.NoError(t, os.WriteFile(progressFile, []byte(header), 0o600))

	// simulate resume: pending section pre-seeded via config
	tailer := NewTailer(progressFile, TailerConfig{
		PollInterval:   10 * time.Millisecond,
		InitialPhase:   status.PhaseTask,
		PendingSection: "task iteration 7",
		PendingPhase:   status.PhaseTask,
	})
	require.NoError(t, tailer.StartFromOffset(int64(len(header))))
	defer tailer.Stop()

	// nothing should arrive until a new line appears
	select {
	case ev := <-tailer.Events():
		t.Fatalf("unexpected event before new line: %+v", ev)
	case <-time.After(80 * time.Millisecond):
	}

	// append a timestamped line after the pending section header
	f, err := os.OpenFile(progressFile, os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // test file path
	require.NoError(t, err)
	_, err = f.WriteString("[26-01-22 10:30:02] post-section line\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	var sawTaskStart, sawSection, sawPostLine bool
	timeout := time.After(1 * time.Second)
loop:
	for !sawTaskStart || !sawSection || !sawPostLine {
		select {
		case ev := <-tailer.Events():
			switch ev.Type { //nolint:exhaustive // only interested in these event types
			case EventTypeTaskStart:
				assert.Equal(t, 7, ev.TaskNum)
				assert.Equal(t, status.PhaseTask, ev.Phase)
				sawTaskStart = true
			case EventTypeSection:
				assert.Equal(t, "task iteration 7", ev.Section)
				sawSection = true
			case EventTypeOutput:
				if ev.Text == "post-section line" {
					sawPostLine = true
				}
			}
		case <-timeout:
			break loop
		}
	}
	assert.True(t, sawTaskStart, "expected TaskStart event for pending section")
	assert.True(t, sawSection, "expected Section event for pending section")
	assert.True(t, sawPostLine, "expected post-section output event")
}

func TestTailer_StartFromOffset_NoPendingSectionDoesNotDefer(t *testing.T) {
	// regression guard: when config.PendingSection is empty, StartFromOffset
	// must behave as before - deferSections=false, section headers parsed
	// inline. previously this was unconditional; now it's gated by config.
	tmpDir := t.TempDir()
	progressFile := filepath.Join(tmpDir, "progress-test.txt")

	header := `# Ralphex Progress Log
Plan: test.md
Branch: main
Mode: full
Started: 2026-01-22 10:30:00
------------------------------------------------------------

[26-01-22 10:30:01] before offset
`
	require.NoError(t, os.WriteFile(progressFile, []byte(header), 0o600))

	tailer := NewTailer(progressFile, TailerConfig{
		PollInterval: 10 * time.Millisecond,
		InitialPhase: status.PhaseTask,
		// intentionally no PendingSection
	})
	require.NoError(t, tailer.StartFromOffset(int64(len(header))))
	defer tailer.Stop()

	// append a section header followed by a line - section should emit
	// immediately (not deferred) because no pending-section carryover.
	f, err := os.OpenFile(progressFile, os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // test file path
	require.NoError(t, err)
	_, err = f.WriteString("--- task iteration 3 ---\n[26-01-22 10:30:02] after\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	// collect up to 5 events or until we see the expected ones
	var sawSection, sawOutput bool
	timeout := time.After(500 * time.Millisecond)
loop:
	for !sawSection || !sawOutput {
		select {
		case ev := <-tailer.Events():
			if ev.Type == EventTypeSection && ev.Section == "task iteration 3" {
				sawSection = true
			}
			if ev.Type == EventTypeOutput && ev.Text == "after" {
				sawOutput = true
			}
		case <-timeout:
			break loop
		}
	}
	assert.True(t, sawSection, "expected Section event to fire inline when no pending carryover")
	assert.True(t, sawOutput, "expected output event after the section header")
}

// drainEvents consumes any events waiting on the tailer's channel for the
// given window, discarding them. used by tests that need to inspect tailer
// state (e.g. PendingSection) without stalling on the event buffer.
func drainEvents(tailer *Tailer, window time.Duration) {
	deadline := time.After(window)
	for {
		select {
		case <-tailer.Events():
		case <-deadline:
			return
		}
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
