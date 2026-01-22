package web

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSession(t *testing.T) {
	t.Run("creates session with id and path", func(t *testing.T) {
		s := NewSession("my-plan", "/tmp/progress-my-plan.txt")

		assert.Equal(t, "my-plan", s.ID)
		assert.Equal(t, "/tmp/progress-my-plan.txt", s.Path)
		assert.Equal(t, SessionStateCompleted, s.State)
		assert.NotNil(t, s.Buffer)
		assert.NotNil(t, s.Hub)
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

	// add some events
	s.Buffer.Add(NewOutputEvent("task", "test"))
	require.Equal(t, 1, s.Buffer.Count())

	s.Close()

	// buffer should be cleared
	assert.Equal(t, 0, s.Buffer.Count())
}
