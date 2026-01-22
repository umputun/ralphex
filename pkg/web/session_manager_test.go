package web

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSessionManager(t *testing.T) {
	m := NewSessionManager()

	assert.NotNil(t, m.sessions)
	assert.Empty(t, m.All())
}

func TestSessionManager_Discover(t *testing.T) {
	t.Run("finds progress files", func(t *testing.T) {
		dir := t.TempDir()

		// create test progress files
		createProgressFile(t, filepath.Join(dir, "progress-plan1.txt"), "docs/plan1.md", "main", "full")
		createProgressFile(t, filepath.Join(dir, "progress-plan2.txt"), "docs/plan2.md", "feature", "review")

		m := NewSessionManager()
		ids, err := m.Discover(dir)

		require.NoError(t, err)
		assert.Len(t, ids, 2)
		assert.Contains(t, ids, "plan1")
		assert.Contains(t, ids, "plan2")

		// verify sessions were created
		s1 := m.Get("plan1")
		require.NotNil(t, s1)
		assert.Equal(t, "docs/plan1.md", s1.GetMetadata().PlanPath)

		s2 := m.Get("plan2")
		require.NotNil(t, s2)
		assert.Equal(t, "docs/plan2.md", s2.GetMetadata().PlanPath)
	})

	t.Run("returns empty for no matches", func(t *testing.T) {
		dir := t.TempDir()

		m := NewSessionManager()
		ids, err := m.Discover(dir)

		require.NoError(t, err)
		assert.Empty(t, ids)
	})

	t.Run("ignores non-matching files", func(t *testing.T) {
		dir := t.TempDir()

		// create non-matching files
		require.NoError(t, os.WriteFile(filepath.Join(dir, "other.txt"), []byte("test"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "progress.txt"), []byte("test"), 0o600))

		// create matching file
		createProgressFile(t, filepath.Join(dir, "progress-valid.txt"), "plan.md", "main", "full")

		m := NewSessionManager()
		ids, err := m.Discover(dir)

		require.NoError(t, err)
		assert.Len(t, ids, 1)
		assert.Contains(t, ids, "valid")
	})

	t.Run("updates existing sessions", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "progress-test.txt")
		createProgressFile(t, path, "plan.md", "main", "full")

		m := NewSessionManager()

		// first discovery
		_, err := m.Discover(dir)
		require.NoError(t, err)

		s := m.Get("test")
		require.NotNil(t, s)
		assert.Equal(t, "main", s.GetMetadata().Branch)

		// update the file
		createProgressFile(t, path, "plan.md", "feature", "review")

		// second discovery
		_, err = m.Discover(dir)
		require.NoError(t, err)

		// should update metadata
		assert.Equal(t, "feature", s.GetMetadata().Branch)
	})
}

func TestSessionManager_Get(t *testing.T) {
	m := NewSessionManager()

	t.Run("returns nil for missing session", func(t *testing.T) {
		assert.Nil(t, m.Get("nonexistent"))
	})

	t.Run("returns session after discover", func(t *testing.T) {
		dir := t.TempDir()
		createProgressFile(t, filepath.Join(dir, "progress-test.txt"), "plan.md", "main", "full")

		_, err := m.Discover(dir)
		require.NoError(t, err)

		s := m.Get("test")
		assert.NotNil(t, s)
		assert.Equal(t, "test", s.ID)
	})
}

func TestSessionManager_All(t *testing.T) {
	dir := t.TempDir()
	createProgressFile(t, filepath.Join(dir, "progress-a.txt"), "a.md", "main", "full")
	createProgressFile(t, filepath.Join(dir, "progress-b.txt"), "b.md", "main", "full")

	m := NewSessionManager()
	_, err := m.Discover(dir)
	require.NoError(t, err)

	all := m.All()
	assert.Len(t, all, 2)
}

func TestSessionManager_Remove(t *testing.T) {
	dir := t.TempDir()
	createProgressFile(t, filepath.Join(dir, "progress-test.txt"), "plan.md", "main", "full")

	m := NewSessionManager()
	_, err := m.Discover(dir)
	require.NoError(t, err)

	require.NotNil(t, m.Get("test"))

	m.Remove("test")

	assert.Nil(t, m.Get("test"))
}

func TestSessionManager_Close(t *testing.T) {
	dir := t.TempDir()
	createProgressFile(t, filepath.Join(dir, "progress-a.txt"), "a.md", "main", "full")
	createProgressFile(t, filepath.Join(dir, "progress-b.txt"), "b.md", "main", "full")

	m := NewSessionManager()
	_, err := m.Discover(dir)
	require.NoError(t, err)

	assert.Len(t, m.All(), 2)

	m.Close()

	assert.Empty(t, m.All())
}

func TestSessionIDFromPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/tmp/progress-my-plan.txt", "my-plan"},
		{"/path/to/progress-test.txt", "test"},
		{"progress-simple.txt", "simple"},
		{"/dir/progress-multi-word-name.txt", "multi-word-name"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := sessionIDFromPath(tt.path)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsActive(t *testing.T) {
	t.Run("returns false for unlocked file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "progress-test.txt")
		createProgressFile(t, path, "plan.md", "main", "full")

		active, err := IsActive(path)
		require.NoError(t, err)
		assert.False(t, active)
	})

	t.Run("returns true for locked file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "progress-test.txt")
		createProgressFile(t, path, "plan.md", "main", "full")

		// acquire lock
		f, err := os.Open(path) //nolint:gosec // test file path
		require.NoError(t, err)
		defer f.Close()

		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		require.NoError(t, err)

		// check from another file descriptor
		active, err := IsActive(path)
		require.NoError(t, err)
		assert.True(t, active)

		// release lock
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	})

	t.Run("returns error for missing file", func(t *testing.T) {
		_, err := IsActive("/nonexistent/path")
		assert.Error(t, err)
	})
}

func TestParseProgressHeader(t *testing.T) {
	t.Run("parses all fields", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "progress-test.txt")

		content := `# Ralphex Progress Log
Plan: docs/plans/my-plan.md
Branch: feature-branch
Mode: full
Started: 2026-01-22 10:30:00
------------------------------------------------------------

[26-01-22 10:30:05] Some output
`
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

		meta, err := ParseProgressHeader(path)
		require.NoError(t, err)

		assert.Equal(t, "docs/plans/my-plan.md", meta.PlanPath)
		assert.Equal(t, "feature-branch", meta.Branch)
		assert.Equal(t, "full", meta.Mode)
		assert.Equal(t, time.Date(2026, 1, 22, 10, 30, 0, 0, time.UTC), meta.StartTime)
	})

	t.Run("handles review-only mode", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "progress-test.txt")

		content := `# Ralphex Progress Log
Plan: (no plan - review only)
Branch: main
Mode: review
Started: 2026-01-22 11:00:00
------------------------------------------------------------
`
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

		meta, err := ParseProgressHeader(path)
		require.NoError(t, err)

		assert.Equal(t, "(no plan - review only)", meta.PlanPath)
		assert.Equal(t, "review", meta.Mode)
	})

	t.Run("handles missing fields gracefully", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "progress-test.txt")

		content := `# Ralphex Progress Log
Branch: main
------------------------------------------------------------
`
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

		meta, err := ParseProgressHeader(path)
		require.NoError(t, err)

		assert.Empty(t, meta.PlanPath)
		assert.Equal(t, "main", meta.Branch)
		assert.Empty(t, meta.Mode)
		assert.True(t, meta.StartTime.IsZero())
	})

	t.Run("returns error for missing file", func(t *testing.T) {
		_, err := ParseProgressHeader("/nonexistent/path")
		assert.Error(t, err)
	})
}

// helper to create a progress file with standard header
func createProgressFile(t *testing.T, path, plan, branch, mode string) {
	t.Helper()
	content := `# Ralphex Progress Log
Plan: ` + plan + `
Branch: ` + branch + `
Mode: ` + mode + `
Started: 2026-01-22 10:00:00
------------------------------------------------------------

`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}
