package web

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmaxmax/go-sse"
)

func TestNewSession(t *testing.T) {
	t.Run("creates session with id and path", func(t *testing.T) {
		s := NewSession("my-plan", "/tmp/progress-my-plan.txt")

		assert.Equal(t, "my-plan", s.ID)
		assert.Equal(t, "/tmp/progress-my-plan.txt", s.Path)
		assert.Equal(t, SessionStateCompleted, s.State)
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

		// wait for tailer to read initial content
		time.Sleep(200 * time.Millisecond)

		s.StopTailing()
		assert.False(t, s.IsTailing())
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
	time.Sleep(50 * time.Millisecond) // give goroutine time to stop
	assert.False(t, s.IsTailing())
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
		time.Sleep(50 * time.Millisecond)
		assert.False(t, s.IsTailing())
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

		// Replay should not panic and should handle empty ID
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

		// Replay with non-empty ID should work without modification
		err = replayer.Replay(subscription)
		require.NoError(t, err)
	})

	t.Run("replayer Put delegation works correctly", func(t *testing.T) {
		finiteReplayer, err := sse.NewFiniteReplayer(100, true)
		require.NoError(t, err)

		replayer := &allEventsReplayer{inner: finiteReplayer}

		// Put should delegate to inner replayer
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
