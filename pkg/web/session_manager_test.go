package web

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/config"
	"github.com/umputun/ralphex/pkg/progress"
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
		path1 := filepath.Join(dir, "progress-plan1.txt")
		path2 := filepath.Join(dir, "progress-plan2.txt")
		createProgressFile(t, path1, "docs/plan1.md", "main", "full")
		createProgressFile(t, path2, "docs/plan2.md", "feature", "review")

		m := NewSessionManager()
		ids, err := m.Discover(dir)

		require.NoError(t, err)
		assert.Len(t, ids, 2)
		id1 := sessionIDFromPath(path1)
		id2 := sessionIDFromPath(path2)
		assert.Contains(t, ids, id1)
		assert.Contains(t, ids, id2)

		// verify sessions were created
		s1 := m.Get(id1)
		require.NotNil(t, s1)
		assert.Equal(t, "docs/plan1.md", s1.GetMetadata().PlanPath)

		s2 := m.Get(id2)
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
		path := filepath.Join(dir, "progress-valid.txt")
		createProgressFile(t, path, "plan.md", "main", "full")

		m := NewSessionManager()
		ids, err := m.Discover(dir)

		require.NoError(t, err)
		assert.Len(t, ids, 1)
		assert.Contains(t, ids, sessionIDFromPath(path))
	})

	t.Run("updates existing sessions", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "progress-test.txt")
		createProgressFile(t, path, "plan.md", "main", "full")

		m := NewSessionManager()

		// first discovery
		_, err := m.Discover(dir)
		require.NoError(t, err)

		id := sessionIDFromPath(path)
		s := m.Get(id)
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
		path := filepath.Join(dir, "progress-test.txt")
		createProgressFile(t, path, "plan.md", "main", "full")

		_, err := m.Discover(dir)
		require.NoError(t, err)

		id := sessionIDFromPath(path)
		s := m.Get(id)
		assert.NotNil(t, s)
		assert.Equal(t, id, s.ID)
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

	id := sessionIDFromPath(filepath.Join(dir, "progress-test.txt"))
	require.NotNil(t, m.Get(id))

	m.Remove(id)

	assert.Nil(t, m.Get(id))
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
	t.Run("includes base name and hash", func(t *testing.T) {
		got := sessionIDFromPath("/tmp/progress-my-plan.txt")
		assert.True(t, strings.HasPrefix(got, "my-plan-"))

		lastDash := strings.LastIndex(got, "-")
		require.NotEqual(t, -1, lastDash)
		suffix := got[lastDash+1:]
		assert.Len(t, suffix, 16)
		_, err := strconv.ParseUint(suffix, 16, 64)
		assert.NoError(t, err)
	})

	t.Run("different paths produce different IDs", func(t *testing.T) {
		id1 := sessionIDFromPath("/tmp/progress-test.txt")
		id2 := sessionIDFromPath("/other/progress-test.txt")
		assert.NotEqual(t, id1, id2)
	})

	t.Run("same path is stable", func(t *testing.T) {
		path := "/tmp/progress-simple.txt"
		id1 := sessionIDFromPath(path)
		id2 := sessionIDFromPath(path)
		assert.Equal(t, id1, id2)
	})
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

	t.Run("returns true for active progress logger", func(t *testing.T) {
		dir := t.TempDir()
		planPath := filepath.Join(dir, "plan.md")
		require.NoError(t, os.WriteFile(planPath, []byte("# plan"), 0o600))

		oldWd, err := os.Getwd()
		require.NoError(t, err)
		require.NoError(t, os.Chdir(dir))
		t.Cleanup(func() {
			_ = os.Chdir(oldWd)
		})

		logger, err := progress.NewLogger(progress.Config{
			PlanFile: planPath,
			Mode:     "full",
			Branch:   "main",
		}, testColors())
		require.NoError(t, err)
		t.Cleanup(func() {
			_ = logger.Close()
		})

		active, err := IsActive(logger.Path())
		require.NoError(t, err)
		assert.True(t, active)
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

func TestLoadProgressFileIntoSession(t *testing.T) {
	t.Run("loads completed session content without panic", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "progress-test.txt")

		content := `# Ralphex Progress Log
Plan: docs/plan.md
Branch: main
Mode: full
Started: 2026-01-22 10:00:00
------------------------------------------------------------

--- Task 1 ---
[26-01-22 10:00:01] executing task
[26-01-22 10:00:02] task output line 1
[26-01-22 10:00:03] task output line 2
--- Review ---
[26-01-22 10:00:04] review started
[26-01-22 10:00:05] <<<RALPHEX:REVIEW_DONE>>>
`
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

		session := NewSession("test", path)
		defer session.Close()

		// should not panic and should process the file
		loadProgressFileIntoSession(path, session)
	})

	t.Run("handles missing file gracefully", func(t *testing.T) {
		session := NewSession("test", "/nonexistent/file.txt")
		defer session.Close()

		// should not panic
		loadProgressFileIntoSession("/nonexistent/file.txt", session)
	})

	t.Run("skips header lines", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "progress-test.txt")

		content := `# Ralphex Progress Log
Plan: docs/plan.md
Branch: main
Mode: full
Started: 2026-01-22 10:00:00
------------------------------------------------------------
[26-01-22 10:00:01] first real line
`
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

		session := NewSession("test", path)
		defer session.Close()

		// should not panic
		loadProgressFileIntoSession(path, session)
	})
}

func TestSessionManager_DiscoverLoadsCompletedSessionContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "progress-completed.txt")

	// create a progress file with content (simulating a completed session)
	content := `# Ralphex Progress Log
Plan: docs/plan.md
Branch: main
Mode: full
Started: 2026-01-22 10:00:00
------------------------------------------------------------

--- Task 1 ---
[26-01-22 10:00:01] task output
[26-01-22 10:00:02] <<<RALPHEX:ALL_TASKS_DONE>>>
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	m := NewSessionManager()
	defer m.Close()

	// discover the session (it's not locked, so will be completed)
	_, err := m.Discover(dir)
	require.NoError(t, err)

	sessionID := sessionIDFromPath(path)
	session := m.Get(sessionID)
	require.NotNil(t, session)

	// verify the session state is completed
	assert.Equal(t, SessionStateCompleted, session.GetState())

	// verify the session is marked as loaded (content was published to SSE server)
	assert.True(t, session.IsLoaded(), "completed session should be marked as loaded")
}

func TestSessionManager_EvictOldCompleted(t *testing.T) {
	t.Run("evicts oldest completed sessions when limit exceeded", func(t *testing.T) {
		dir := t.TempDir()
		m := NewSessionManager()

		// create more than MaxCompletedSessions progress files
		numSessions := MaxCompletedSessions + 5
		paths := make([]string, numSessions)

		for i := range numSessions {
			path := filepath.Join(dir, "progress-plan"+strconv.Itoa(i)+".txt")
			// use different start times so we can predict which ones get evicted
			startTime := time.Date(2026, 1, 1, 10, 0, i, 0, time.UTC)
			content := `# Ralphex Progress Log
Plan: plan` + strconv.Itoa(i) + `.md
Branch: main
Mode: full
Started: ` + startTime.Format("2006-01-02 15:04:05") + `
------------------------------------------------------------
`
			require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
			paths[i] = path
		}

		// discover all sessions
		_, err := m.Discover(dir)
		require.NoError(t, err)

		// verify only MaxCompletedSessions remain (oldest should be evicted)
		all := m.All()
		assert.LessOrEqual(t, len(all), MaxCompletedSessions, "should not exceed MaxCompletedSessions")
	})

	t.Run("does not evict when under limit", func(t *testing.T) {
		dir := t.TempDir()
		m := NewSessionManager()

		// create fewer than MaxCompletedSessions
		numSessions := 5
		for i := range numSessions {
			path := filepath.Join(dir, "progress-small"+strconv.Itoa(i)+".txt")
			createProgressFile(t, path, "plan"+strconv.Itoa(i)+".md", "main", "full")
		}

		_, err := m.Discover(dir)
		require.NoError(t, err)

		// all sessions should remain
		all := m.All()
		assert.Len(t, all, numSessions)
	})
}

func TestSessionManager_RefreshStates(t *testing.T) {
	t.Run("skips non-tailing sessions", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "progress-test.txt")
		createProgressFile(t, path, "plan.md", "main", "full")

		m := NewSessionManager()
		_, err := m.Discover(dir)
		require.NoError(t, err)

		sessionID := sessionIDFromPath(path)
		session := m.Get(sessionID)
		require.NotNil(t, session)

		// session is not tailing
		assert.False(t, session.IsTailing())
		initialState := session.GetState()

		// RefreshStates should skip non-tailing sessions
		m.RefreshStates()

		// state should remain unchanged
		assert.Equal(t, initialState, session.GetState())
	})

	t.Run("marks unlocked tailing session as completed", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "progress-test.txt")
		createProgressFile(t, path, "plan.md", "main", "full")

		m := NewSessionManager()
		_, err := m.Discover(dir)
		require.NoError(t, err)

		sessionID := sessionIDFromPath(path)
		session := m.Get(sessionID)
		require.NotNil(t, session)

		// manually start tailing and set to active
		err = session.StartTailing(true)
		require.NoError(t, err)
		assert.True(t, session.IsTailing())

		// RefreshStates should detect the unlocked file and mark as completed
		m.RefreshStates()

		// since file is not locked by another process, it should be marked completed
		time.Sleep(50 * time.Millisecond) // give goroutine time to stop
		assert.Equal(t, SessionStateCompleted, session.GetState())
		assert.False(t, session.IsTailing())
	})
}

func testColors() *progress.Colors {
	return progress.NewColors(config.ColorConfig{
		Task:       "0,255,0",
		Review:     "0,255,255",
		Codex:      "255,0,255",
		ClaudeEval: "100,200,255",
		Warn:       "255,255,0",
		Error:      "255,0,0",
		Signal:     "255,100,100",
		Timestamp:  "138,138,138",
		Info:       "180,180,180",
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
