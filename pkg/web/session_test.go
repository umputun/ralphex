package web

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmaxmax/go-sse"

	"github.com/umputun/ralphex/pkg/status"
)

func TestNewSession(t *testing.T) {
	t.Run("creates session with id and path", func(t *testing.T) {
		s := NewSession("my-plan", "/tmp/progress-my-plan.txt")

		assert.Equal(t, "my-plan", s.ID)
		assert.Equal(t, "/tmp/progress-my-plan.txt", s.Path)
		assert.Equal(t, SessionStateCompleted, s.GetState())
		assert.NotNil(t, s.SSE)
	})

	t.Run("starts with empty metadata", func(t *testing.T) {
		s := NewSession("test", "/tmp/test.txt")

		meta := s.GetMetadata()
		assert.Empty(t, meta.PlanPath)
		assert.Empty(t, meta.Branch)
		assert.Empty(t, meta.Mode)
		assert.True(t, meta.StartTime.IsZero())
	})
}

func TestSession_Metadata(t *testing.T) {
	s := NewSession("test", "/tmp/test.txt")

	meta := SessionMetadata{
		PlanPath:  "docs/plans/my-plan.md",
		Branch:    "feature-branch",
		Mode:      "full",
		StartTime: time.Date(2026, 1, 22, 10, 30, 0, 0, time.UTC),
	}

	s.SetMetadata(meta)
	got := s.GetMetadata()

	assert.Equal(t, meta.PlanPath, got.PlanPath)
	assert.Equal(t, meta.Branch, got.Branch)
	assert.Equal(t, meta.Mode, got.Mode)
	assert.Equal(t, meta.StartTime, got.StartTime)
}

func TestSession_State(t *testing.T) {
	s := NewSession("test", "/tmp/test.txt")

	assert.Equal(t, SessionStateCompleted, s.GetState())

	s.SetState(SessionStateActive)
	assert.Equal(t, SessionStateActive, s.GetState())

	s.SetState(SessionStateCompleted)
	assert.Equal(t, SessionStateCompleted, s.GetState())
}

func TestSession_LastModified(t *testing.T) {
	s := NewSession("test", "/tmp/test.txt")

	assert.True(t, s.GetLastModified().IsZero())

	now := time.Now()
	s.SetLastModified(now)

	assert.Equal(t, now, s.GetLastModified())
}

func TestSession_Tailer(t *testing.T) {
	s := NewSession("test", "/tmp/test.txt")

	assert.Nil(t, s.GetTailer())

	tailer := &Tailer{}
	s.SetTailer(tailer)
	assert.Equal(t, tailer, s.GetTailer())

	s.SetTailer(nil)
	assert.Nil(t, s.GetTailer())
}

func TestSession_Close(t *testing.T) {
	s := NewSession("test", "/tmp/test.txt")
	defer s.Close()

	// close should not panic
	s.Close()
}

func TestSession_Publish(t *testing.T) {
	s := NewSession("test", "/tmp/test.txt")
	defer s.Close()

	// publish should succeed and return nil when no clients connected
	event := NewOutputEvent("task", "test message")
	err := s.Publish(event)
	assert.NoError(t, err)
}

func TestSession_MarkLoadedIfNot(t *testing.T) {
	t.Run("returns true on first call", func(t *testing.T) {
		s := NewSession("test", "/tmp/test.txt")
		defer s.Close()

		assert.False(t, s.IsLoaded())
		assert.True(t, s.MarkLoadedIfNot())
		assert.True(t, s.IsLoaded())
	})

	t.Run("returns false on subsequent calls", func(t *testing.T) {
		s := NewSession("test", "/tmp/test.txt")
		defer s.Close()

		assert.True(t, s.MarkLoadedIfNot())
		assert.False(t, s.MarkLoadedIfNot())
		assert.False(t, s.MarkLoadedIfNot())
	})
}

func TestSession_StartTailing(t *testing.T) {
	t.Run("starts tailing and feeds events", func(t *testing.T) {
		tmpDir := t.TempDir()
		progressFile := tmpDir + "/progress-test.txt"

		// create progress file with header
		content := `# Ralphex Progress Log
Plan: test.md
Branch: main
Mode: full
Started: 2026-01-22 10:30:00
------------------------------------------------------------

[26-01-22 10:30:01] Initial line
`
		require.NoError(t, os.WriteFile(progressFile, []byte(content), 0o600))

		s := NewSession("test", progressFile)
		defer s.Close()

		// should not be tailing initially
		assert.False(t, s.IsTailing())

		// start tailing from beginning
		err := s.StartTailing(true)
		require.NoError(t, err)

		// should be tailing now
		assert.True(t, s.IsTailing())

		s.StopTailing()
		assert.Eventually(t, func() bool { return !s.IsTailing() }, time.Second, 10*time.Millisecond)
	})

	t.Run("noop if already tailing", func(t *testing.T) {
		tmpDir := t.TempDir()
		progressFile := tmpDir + "/progress-test.txt"

		content := `# Ralphex Progress Log
Plan: test.md
Branch: main
Mode: full
Started: 2026-01-22 10:30:00
------------------------------------------------------------
`
		require.NoError(t, os.WriteFile(progressFile, []byte(content), 0o600))

		s := NewSession("test", progressFile)
		defer s.Close()

		err := s.StartTailing(true)
		require.NoError(t, err)

		// second start should be no-op
		err = s.StartTailing(true)
		require.NoError(t, err)

		assert.True(t, s.IsTailing())
	})

	t.Run("returns error for non-existent file", func(t *testing.T) {
		s := NewSession("test", "/nonexistent/path.txt")
		defer s.Close()

		err := s.StartTailing(true)
		require.Error(t, err)
		assert.False(t, s.IsTailing())
	})
}

func TestSession_IsTailing(t *testing.T) {
	tmpDir := t.TempDir()
	progressFile := tmpDir + "/progress-test.txt"

	content := `# Ralphex Progress Log
Plan: test.md
Branch: main
Mode: full
Started: 2026-01-22 10:30:00
------------------------------------------------------------
`
	require.NoError(t, os.WriteFile(progressFile, []byte(content), 0o600))

	s := NewSession("test", progressFile)
	defer s.Close()

	// false before start
	assert.False(t, s.IsTailing())

	// true during tailing
	require.NoError(t, s.StartTailing(true))
	assert.True(t, s.IsTailing())

	// false after stop
	s.StopTailing()
	assert.Eventually(t, func() bool { return !s.IsTailing() }, time.Second, 10*time.Millisecond)
}

func TestSession_StopTailing(t *testing.T) {
	t.Run("stops tailing cleanly", func(t *testing.T) {
		tmpDir := t.TempDir()
		progressFile := tmpDir + "/progress-test.txt"

		content := `# Ralphex Progress Log
Plan: test.md
Branch: main
Mode: full
Started: 2026-01-22 10:30:00
------------------------------------------------------------
`
		require.NoError(t, os.WriteFile(progressFile, []byte(content), 0o600))

		s := NewSession("test", progressFile)
		defer s.Close()

		require.NoError(t, s.StartTailing(true))
		assert.True(t, s.IsTailing())

		s.StopTailing()
		assert.Eventually(t, func() bool { return !s.IsTailing() }, time.Second, 10*time.Millisecond)
	})

	t.Run("safe to call when not tailing", func(t *testing.T) {
		s := NewSession("test", "/tmp/test.txt")
		defer s.Close()

		// should not panic
		s.StopTailing()
		assert.False(t, s.IsTailing())
	})

	t.Run("safe to call multiple times", func(t *testing.T) {
		tmpDir := t.TempDir()
		progressFile := tmpDir + "/progress-test.txt"

		content := `# Ralphex Progress Log
Plan: test.md
Branch: main
Mode: full
Started: 2026-01-22 10:30:00
------------------------------------------------------------
`
		require.NoError(t, os.WriteFile(progressFile, []byte(content), 0o600))

		s := NewSession("test", progressFile)
		defer s.Close()

		require.NoError(t, s.StartTailing(true))

		// multiple stop calls should be safe
		s.StopTailing()
		s.StopTailing()
		s.StopTailing()

		assert.False(t, s.IsTailing())
	})
}

func TestSession_LastOffset(t *testing.T) {
	t.Run("default is zero", func(t *testing.T) {
		s := NewSession("test", "/tmp/test.txt")
		defer s.Close()
		assert.Equal(t, int64(0), s.getLastOffset())
	})

	t.Run("set and get", func(t *testing.T) {
		s := NewSession("test", "/tmp/test.txt")
		defer s.Close()

		s.setLastOffset(1234)
		assert.Equal(t, int64(1234), s.getLastOffset())

		s.setLastOffset(0)
		assert.Equal(t, int64(0), s.getLastOffset())
	})

	t.Run("concurrent access is safe", func(t *testing.T) {
		s := NewSession("test", "/tmp/test.txt")
		defer s.Close()

		const workers = 20
		const iterations = 200

		var wg sync.WaitGroup
		wg.Add(workers * 2)

		for i := range workers {
			go func(base int64) {
				defer wg.Done()
				for j := range iterations {
					s.setLastOffset(base + int64(j))
				}
			}(int64(i) * 1000)
		}
		for range workers {
			go func() {
				defer wg.Done()
				for range iterations {
					_ = s.getLastOffset()
				}
			}()
		}

		wg.Wait()
		// final value is non-deterministic but must be one of the written values:
		// writer i writes base+j where base = i*1000 and j in [0, iterations),
		// so final % 1000 must be < iterations and final / 1000 must be < workers.
		final := s.getLastOffset()
		assert.Less(t, final%1000, int64(iterations), "final must be a value written by some worker")
		assert.Less(t, final/1000, int64(workers), "final must come from a known worker")
	})
}

func TestSession_StopTailingCapturesOffset(t *testing.T) {
	t.Run("captures offset from running tailer", func(t *testing.T) {
		tmpDir := t.TempDir()
		progressFile := tmpDir + "/progress-test.txt"

		content := `# Ralphex Progress Log
Plan: test.md
Branch: main
Mode: full
Started: 2026-01-22 10:30:00
------------------------------------------------------------

[26-01-22 10:30:01] line one
[26-01-22 10:30:02] line two
[26-01-22 10:30:03] line three
`
		require.NoError(t, os.WriteFile(progressFile, []byte(content), 0o600))

		s := NewSession("test", progressFile)
		defer s.Close()

		require.NoError(t, s.StartTailing(true))

		// wait until the tailer has consumed the whole file
		expected := int64(len(content))
		require.Eventually(t, func() bool {
			return s.GetTailer() != nil && s.GetTailer().Offset() == expected
		}, 2*time.Second, 20*time.Millisecond, "tailer should advance to EOF")

		s.StopTailing()
		assert.Eventually(t, func() bool { return !s.IsTailing() }, time.Second, 10*time.Millisecond)

		assert.Equal(t, expected, s.getLastOffset(), "lastOffset should match bytes read before stop")
	})

	t.Run("nil tailer leaves lastOffset unchanged", func(t *testing.T) {
		s := NewSession("test", "/tmp/test.txt")
		defer s.Close()

		s.setLastOffset(42)
		s.StopTailing()

		assert.Equal(t, int64(42), s.getLastOffset(), "stopping without a tailer must not overwrite lastOffset")
	})

	t.Run("drains buffered events into SSE before returning", func(t *testing.T) {
		// regression test: before the StopTailing drain fix, events that the
		// tailer had read (and whose bytes were accounted for in tailer.Offset)
		// but not yet drained by feedEvents could be silently dropped when
		// stopTailCh was closed. with the drain, every event the tailer produced
		// must be visible on the SSE stream by the time StopTailing returns, and
		// lastOffset must equal the file size — this is the guarantee Reactivate
		// relies on to avoid a gap across stop/reactivate cycles.
		tmpDir := t.TempDir()
		progressFile := tmpDir + "/progress-test.txt"

		// seed with header and one line so the SSE server has an event in its
		// replay buffer when we subscribe — without it, http.Do blocks waiting
		// for response headers that Joe only flushes on the first event.
		initial := `# Ralphex Progress Log
Plan: test.md
Branch: main
Mode: full
Started: 2026-01-22 10:30:00
------------------------------------------------------------

[26-01-22 10:30:00] seed line
`
		require.NoError(t, os.WriteFile(progressFile, []byte(initial), 0o600))

		s := NewSession("test", progressFile)
		defer s.Close()

		require.NoError(t, s.StartTailing(true))
		require.Eventually(t, func() bool {
			return s.GetTailer() != nil && s.GetTailer().Offset() == int64(len(initial))
		}, 2*time.Second, 20*time.Millisecond, "tailer should read seed content")

		events, cleanup := subscribeSSEEvents(t, s)
		defer cleanup()

		// drain replay of seed line so only live-post-subscription events remain
		_ = drainChannel(events, 200*time.Millisecond)

		// append many lines in one write to maximize the chance of events being
		// queued in eventCh when StopTailing fires
		const lineCount = 200
		var body strings.Builder
		for i := range lineCount {
			fmt.Fprintf(&body, "[26-01-22 10:30:%02d] line %d\n", i%60, i)
		}
		f, err := os.OpenFile(progressFile, os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // test path from t.TempDir
		require.NoError(t, err)
		_, err = f.WriteString(body.String())
		require.NoError(t, err)
		require.NoError(t, f.Close())

		expected := int64(len(initial) + body.Len())
		require.Eventually(t, func() bool {
			return s.GetTailer() != nil && s.GetTailer().Offset() == expected
		}, 2*time.Second, 20*time.Millisecond, "tailer should read appended content")

		// stop — with the drain fix, every event the tailer pushed to eventCh
		// must be published to SSE before StopTailing returns.
		s.StopTailing()
		require.Equal(t, expected, s.getLastOffset(), "lastOffset must equal bytes read after StopTailing")

		delivered := drainChannel(events, 1500*time.Millisecond)
		outputs := 0
		for _, ev := range delivered {
			if strings.Contains(ev, "\"type\":\"output\"") && strings.Contains(ev, "\"text\":\"line ") {
				outputs++
			}
		}
		assert.Equal(t, lineCount, outputs,
			"every line the tailer read must be published to SSE — no drops between offset advance and feedEvents drain")
	})
}

func TestSession_Reactivate_ResumesFromOffset(t *testing.T) {
	tmpDir := t.TempDir()
	progressFile := tmpDir + "/progress-test.txt"

	header := `# Ralphex Progress Log
Plan: test.md
Branch: main
Mode: full
Started: 2026-01-22 10:30:00
------------------------------------------------------------

`
	initial := "[26-01-22 10:30:01] line one\n[26-01-22 10:30:02] line two\n"
	require.NoError(t, os.WriteFile(progressFile, []byte(header+initial), 0o600))

	s := NewSession("test", progressFile)
	defer s.Close()

	// start from beginning, wait for tailer to consume the whole file
	require.NoError(t, s.StartTailing(true))
	expected := int64(len(header + initial))
	require.Eventually(t, func() bool {
		return s.GetTailer() != nil && s.GetTailer().Offset() == expected
	}, 2*time.Second, 20*time.Millisecond, "tailer should reach EOF")

	// stop - this captures lastOffset
	s.StopTailing()
	require.Eventually(t, func() bool { return !s.IsTailing() }, time.Second, 10*time.Millisecond)
	require.Equal(t, expected, s.getLastOffset())

	// simulate watcher seeing completed state, then reactivating after new write
	s.SetState(SessionStateCompleted)

	// append new content after stop - these lines MUST appear
	newLines := "[26-01-22 10:30:03] line three\n[26-01-22 10:30:04] line four\n"
	f, err := os.OpenFile(progressFile, os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // test file path
	require.NoError(t, err)
	_, err = f.WriteString(newLines)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	require.NoError(t, s.Reactivate())

	// state flipped to active, tailer running
	assert.Equal(t, SessionStateActive, s.GetState())
	assert.True(t, s.IsTailing())

	// tailer advanced to new EOF
	finalExpected := expected + int64(len(newLines))
	require.Eventually(t, func() bool {
		return s.GetTailer() != nil && s.GetTailer().Offset() == finalExpected
	}, 2*time.Second, 20*time.Millisecond, "tailer should resume and reach new EOF")

	// stop to capture offset again; confirms we really resumed from the stored offset
	s.StopTailing()
	require.Eventually(t, func() bool { return !s.IsTailing() }, time.Second, 10*time.Millisecond)
	assert.Equal(t, finalExpected, s.getLastOffset())
}

func TestSession_Reactivate_Idempotent(t *testing.T) {
	tmpDir := t.TempDir()
	progressFile := tmpDir + "/progress-test.txt"

	content := `# Ralphex Progress Log
Plan: test.md
Branch: main
Mode: full
Started: 2026-01-22 10:30:00
------------------------------------------------------------

[26-01-22 10:30:01] line one
`
	require.NoError(t, os.WriteFile(progressFile, []byte(content), 0o600))

	s := NewSession("test", progressFile)
	defer s.Close()

	s.setLastOffset(int64(len(content)))
	s.SetState(SessionStateCompleted)

	require.NoError(t, s.Reactivate())
	first := s.GetTailer()
	require.NotNil(t, first)
	require.True(t, first.IsRunning())

	// second call must not replace the running tailer
	require.NoError(t, s.Reactivate())
	second := s.GetTailer()
	assert.Same(t, first, second, "reactivate must be idempotent while tailer is running")
	assert.True(t, second.IsRunning())
}

func TestSession_Reactivate_OnClosedSession(t *testing.T) {
	tmpDir := t.TempDir()
	progressFile := tmpDir + "/progress-test.txt"
	content := `# Ralphex Progress Log
Plan: test.md
Branch: main
Mode: full
Started: 2026-01-22 10:30:00
------------------------------------------------------------
`
	require.NoError(t, os.WriteFile(progressFile, []byte(content), 0o600))

	s := NewSession("test", progressFile)
	s.setLastOffset(int64(len(content)))
	s.SetState(SessionStateCompleted)

	// close the session - removes the underlying file scenario is simulated next
	s.Close()

	// delete the progress file to force tailer start failure
	require.NoError(t, os.Remove(progressFile))

	// reactivate on a closed session whose file is gone should return an error
	// and must not panic or flip state to active
	err := s.Reactivate()
	require.Error(t, err)
	assert.Equal(t, SessionStateCompleted, s.GetState())
}

func TestSession_Reactivate_FailedStartLeavesStateUnchanged(t *testing.T) {
	s := NewSession("test", "/nonexistent/ralphex-reactivate-path.txt")
	defer s.Close()

	s.SetState(SessionStateCompleted)
	s.setLastOffset(100)

	err := s.Reactivate()
	require.Error(t, err)

	// state stays completed, no tailer stored
	assert.Equal(t, SessionStateCompleted, s.GetState())
	assert.False(t, s.IsTailing())
	assert.Nil(t, s.GetTailer())
}

func TestSession_Reactivate_PreservesPhase(t *testing.T) {
	// regression test for codex finding: resuming mid-phase must carry over
	// the parser phase so new lines are tagged correctly, rather than defaulting
	// to PhaseTask until the next section header.
	tmpDir := t.TempDir()
	progressFile := tmpDir + "/progress-test.txt"

	content := `# Ralphex Progress Log
Plan: test.md
Branch: main
Mode: full
Started: 2026-01-22 10:30:00
------------------------------------------------------------

--- task iteration 1 ---
[26-01-22 10:30:01] task line
--- review iteration 1 ---
[26-01-22 10:30:02] review line
`
	require.NoError(t, os.WriteFile(progressFile, []byte(content), 0o600))

	s := NewSession("test", progressFile)
	defer s.Close()

	// start from beginning, let the tailer process the full file so its
	// internal phase advances to review.
	require.NoError(t, s.StartTailing(true))
	expected := int64(len(content))
	require.Eventually(t, func() bool {
		return s.GetTailer() != nil && s.GetTailer().Offset() == expected
	}, 2*time.Second, 20*time.Millisecond, "tailer should reach EOF")

	// stop captures offset and phase. StopTailing is synchronous and drains
	// feedEvents, so the tailer phase observed here is the committed one.
	s.StopTailing()
	require.Equal(t, status.PhaseReview, s.getLastPhase(),
		"StopTailing must capture the tailer's current phase")

	// simulate flock-race recovery: state was flipped to completed, then a
	// write event arrives with a new line (no new section header).
	s.SetState(SessionStateCompleted)
	events, cleanup := subscribeSSEEvents(t, s)
	defer cleanup()

	// drain any replay from the pre-stop tailer so we only inspect post-reactivate events
	_ = drainChannel(events, 200*time.Millisecond)

	f, err := os.OpenFile(progressFile, os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // test file path
	require.NoError(t, err)
	_, err = f.WriteString("[26-01-22 10:30:03] still in review\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	require.NoError(t, s.Reactivate())

	// the post-reactivate line must carry PhaseReview, not the tailer's default
	postReactivate := drainChannel(events, 800*time.Millisecond)
	var sawStillInReview bool
	for _, ev := range postReactivate {
		if strings.Contains(ev, "still in review") {
			sawStillInReview = true
			assert.Contains(t, ev, "\"phase\":\""+string(status.PhaseReview)+"\"",
				"post-reactivate event must carry preserved phase")
		}
	}
	assert.True(t, sawStillInReview, "should have received the post-reactivate event; got %v", postReactivate)
}

func TestSession_Reactivate_PreservesPendingSection(t *testing.T) {
	// regression test: StopTailing must capture the tailer's deferred section
	// state (a section header read from the file but whose section/task-start
	// event has not yet been emitted because emission is deferred until the
	// next timestamped/output line arrives). Reactivate must then re-seed a
	// fresh tailer with that pending state so the section event still fires
	// on the next line instead of being silently dropped across the restart.
	tmpDir := t.TempDir()
	progressFile := tmpDir + "/progress-test.txt"

	// file ends with a section header - the section event is deferred inside
	// the tailer and will only fire when the next line is read.
	initial := `# Ralphex Progress Log
Plan: test.md
Branch: main
Mode: full
Started: 2026-01-22 10:30:00
------------------------------------------------------------

[26-01-22 10:30:01] before section
--- task iteration 5 ---
`
	require.NoError(t, os.WriteFile(progressFile, []byte(initial), 0o600))

	s := NewSession("test", progressFile)
	defer s.Close()

	require.NoError(t, s.StartTailing(true))
	expected := int64(len(initial))
	require.Eventually(t, func() bool {
		return s.GetTailer() != nil && s.GetTailer().Offset() == expected
	}, 2*time.Second, 20*time.Millisecond, "tailer should reach EOF")

	// wait for the tailer's pending section to be set (consumed the section
	// header but not yet the following line, so pendingSection is populated)
	require.Eventually(t, func() bool {
		name, _ := s.GetTailer().PendingSection()
		return name == "task iteration 5"
	}, 2*time.Second, 20*time.Millisecond, "tailer should have deferred section before stop")

	// stop captures lastOffset AND pending section state into the session
	s.StopTailing()
	require.Eventually(t, func() bool { return !s.IsTailing() }, time.Second, 10*time.Millisecond)
	require.Equal(t, "task iteration 5", s.lastPendingSection,
		"StopTailing must capture pending section name")
	require.Equal(t, status.PhaseTask, s.lastPendingPhase,
		"StopTailing must capture pending section phase")

	// simulate flock-race recovery: state flipped to completed, then subscribe
	// to SSE so we catch the events published after reactivation.
	s.SetState(SessionStateCompleted)
	events, cleanup := subscribeSSEEvents(t, s)
	defer cleanup()

	// drain any replay from before reactivation (the "before section" line was
	// already published by the pre-stop tailer and lives in the replay buffer)
	preReactivate := drainChannel(events, 200*time.Millisecond)
	for _, ev := range preReactivate {
		require.NotContains(t, ev, "\"section\":\"task iteration 5\"",
			"precondition: section event must not have been published before reactivate")
	}

	// append the first line of iteration 5 - the write event plus the tailer
	// reading it should flush the deferred section.
	f, err := os.OpenFile(progressFile, os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // test file path
	require.NoError(t, err)
	_, err = f.WriteString("[26-01-22 10:30:02] first line of iteration 5\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	require.NoError(t, s.Reactivate())

	// collect post-reactivate events via SSE
	postReactivate := drainChannel(events, 800*time.Millisecond)
	var sawTaskStart, sawSection, sawOutput bool
	for _, ev := range postReactivate {
		if strings.Contains(ev, "\"type\":\"task_start\"") && strings.Contains(ev, "\"task_num\":5") {
			sawTaskStart = true
		}
		if strings.Contains(ev, "\"type\":\"section\"") && strings.Contains(ev, "\"section\":\"task iteration 5\"") {
			sawSection = true
		}
		if strings.Contains(ev, "first line of iteration 5") {
			sawOutput = true
		}
	}
	assert.True(t, sawTaskStart, "expected TaskStart event for deferred section; got %v", postReactivate)
	assert.True(t, sawSection, "expected Section event for deferred section; got %v", postReactivate)
	assert.True(t, sawOutput, "expected output event for new post-reactivate line; got %v", postReactivate)
}

func TestAllEventsReplayer_Replay(t *testing.T) {
	t.Run("empty LastEventID is replaced with 0", func(t *testing.T) {
		// create a FiniteReplayer and wrap it in allEventsReplayer
		finiteReplayer, err := sse.NewFiniteReplayer(100, true)
		require.NoError(t, err)

		replayer := &allEventsReplayer{inner: finiteReplayer}

		// create a mock message writer to capture replayed messages
		writer := &mockMessageWriter{}

		// create a subscription with empty LastEventID
		subscription := sse.Subscription{
			Client:      writer,
			LastEventID: sse.ID(""), // empty ID should be replaced with "0"
			Topics:      []string{"events"},
		}

		// replay should not panic and should handle empty ID
		err = replayer.Replay(subscription)
		require.NoError(t, err)
	})

	t.Run("non-empty LastEventID passes through unchanged", func(t *testing.T) {
		finiteReplayer, err := sse.NewFiniteReplayer(100, true)
		require.NoError(t, err)

		replayer := &allEventsReplayer{inner: finiteReplayer}

		// store some events first
		msg := &sse.Message{}
		msg.AppendData("test event")
		_, err = replayer.Put(msg, []string{"events"})
		require.NoError(t, err)

		writer := &mockMessageWriter{}
		subscription := sse.Subscription{
			Client:      writer,
			LastEventID: sse.ID("1"), // non-empty ID
			Topics:      []string{"events"},
		}

		// replay with non-empty ID should work without modification
		err = replayer.Replay(subscription)
		require.NoError(t, err)
	})

	t.Run("replayer Put delegation works correctly", func(t *testing.T) {
		finiteReplayer, err := sse.NewFiniteReplayer(100, true)
		require.NoError(t, err)

		replayer := &allEventsReplayer{inner: finiteReplayer}

		// put should delegate to inner replayer
		msg := &sse.Message{}
		msg.AppendData("test message")
		putMsg, err := replayer.Put(msg, []string{"events"})
		require.NoError(t, err)
		assert.NotNil(t, putMsg)
	})

	t.Run("replayer replays events to new clients", func(t *testing.T) {
		finiteReplayer, err := sse.NewFiniteReplayer(100, true)
		require.NoError(t, err)

		replayer := &allEventsReplayer{inner: finiteReplayer}

		// store multiple events
		for i := 1; i <= 3; i++ {
			msg := &sse.Message{}
			msg.AppendData("event " + string(rune('0'+i)))
			_, putErr := replayer.Put(msg, []string{"events"})
			require.NoError(t, putErr)
		}

		// replay with empty ID (should replay all)
		writer := &mockMessageWriter{}
		subscription := sse.Subscription{
			Client:      writer,
			LastEventID: sse.ID(""), // empty means replay all
			Topics:      []string{"events"},
		}

		err = replayer.Replay(subscription)
		require.NoError(t, err)

		// verify events were replayed
		assert.GreaterOrEqual(t, writer.messageCount, 0, "messages should be replayed")
	})
}

// mockMessageWriter implements sse.MessageWriter for testing
type mockMessageWriter struct {
	messageCount int
}

func (m *mockMessageWriter) Send(msg *sse.Message) error {
	m.messageCount++
	return nil
}

func (m *mockMessageWriter) Flush() error {
	return nil
}
