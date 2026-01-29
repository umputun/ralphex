package web

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/config"
)

func TestNewPlanRunner(t *testing.T) {
	cfg := &config.Config{}
	runner := NewPlanRunner(cfg, nil)

	assert.NotNil(t, runner)
	assert.NotNil(t, runner.sessions)
	assert.Equal(t, cfg, runner.config)
}

func TestPlanRunner_StartPlan(t *testing.T) {
	t.Run("validates directory exists", func(t *testing.T) {
		runner := NewPlanRunner(&config.Config{}, nil)

		_, err := runner.StartPlan("/nonexistent/path", "test plan")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "directory")
	})

	t.Run("validates directory is git repo", func(t *testing.T) {
		tmpDir := t.TempDir()
		runner := NewPlanRunner(&config.Config{}, nil)

		_, err := runner.StartPlan(tmpDir, "test plan")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "git")
	})

	t.Run("creates session for valid git repo", func(t *testing.T) {
		tmpDir := createTestGitRepo(t)
		cfg := testConfig(t)
		cfg.PlansDir = filepath.Join(tmpDir, "docs", "plans")
		runner := NewPlanRunner(cfg, nil)

		session, err := runner.StartPlan(tmpDir, "test plan description")
		require.NoError(t, err)
		require.NotNil(t, session)

		assert.NotEmpty(t, session.ID)
		assert.Equal(t, SessionStateActive, session.GetState())

		// verify input collector is set
		assert.NotNil(t, session.GetInputCollector())

		// cleanup and wait for goroutine to finish
		_ = runner.CancelPlan(session.ID)
		time.Sleep(100 * time.Millisecond)
	})
}

func TestPlanRunner_CancelPlan(t *testing.T) {
	t.Run("cancels existing plan", func(t *testing.T) {
		tmpDir := createTestGitRepo(t)
		cfg := testConfig(t)
		cfg.PlansDir = filepath.Join(tmpDir, "docs", "plans")
		runner := NewPlanRunner(cfg, nil)

		session, err := runner.StartPlan(tmpDir, "test plan")
		require.NoError(t, err)

		err = runner.CancelPlan(session.ID)
		require.NoError(t, err)

		// wait for goroutine cleanup
		time.Sleep(100 * time.Millisecond)
	})

	t.Run("returns error for non-existent session", func(t *testing.T) {
		runner := NewPlanRunner(&config.Config{}, nil)

		err := runner.CancelPlan("nonexistent-id")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestPlanRunner_GetSession(t *testing.T) {
	t.Run("returns nil for non-existent session", func(t *testing.T) {
		runner := NewPlanRunner(&config.Config{}, nil)

		session := runner.GetSession("nonexistent")
		assert.Nil(t, session)
	})

	t.Run("returns session after starting plan", func(t *testing.T) {
		tmpDir := createTestGitRepo(t)
		cfg := testConfig(t)
		cfg.PlansDir = filepath.Join(tmpDir, "docs", "plans")
		runner := NewPlanRunner(cfg, nil)

		started, err := runner.StartPlan(tmpDir, "test plan")
		require.NoError(t, err)

		got := runner.GetSession(started.ID)
		assert.Equal(t, started, got)

		_ = runner.CancelPlan(started.ID)
		time.Sleep(100 * time.Millisecond)
	})
}

func TestPlanRunner_GetAllSessions(t *testing.T) {
	t.Run("returns empty slice initially", func(t *testing.T) {
		runner := NewPlanRunner(&config.Config{}, nil)

		sessions := runner.GetAllSessions()
		assert.Empty(t, sessions)
	})

	t.Run("returns started sessions", func(t *testing.T) {
		tmpDir := createTestGitRepo(t)
		cfg := testConfig(t)
		cfg.PlansDir = filepath.Join(tmpDir, "docs", "plans")
		runner := NewPlanRunner(cfg, nil)

		session, err := runner.StartPlan(tmpDir, "test plan")
		require.NoError(t, err)

		sessions := runner.GetAllSessions()
		assert.Len(t, sessions, 1)
		assert.Equal(t, session.ID, sessions[0].ID)

		_ = runner.CancelPlan(session.ID)
		time.Sleep(100 * time.Millisecond)
	})
}

func TestPlanRunner_GetResumableSessions_UsesWatchDirs(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	writePlanProgress(t, filepath.Join(dir1, "progress-plan-one.txt"), "feature one")
	writePlanProgress(t, filepath.Join(dir2, "progress-plan-two.txt"), "feature two")

	cfg := testConfig(t)
	cfg.ProjectDirs = []string{dir1} // duplicate on purpose
	cfg.WatchDirs = []string{dir1, dir2}

	runner := NewPlanRunner(cfg, nil)

	sessions, err := runner.GetResumableSessions()
	require.NoError(t, err)
	assert.Len(t, sessions, 2)

	dirs := make(map[string]bool)
	descs := make(map[string]bool)
	for _, s := range sessions {
		dirs[s.Dir] = true
		descs[s.PlanDescription] = true
	}
	assert.True(t, dirs[dir1])
	assert.True(t, dirs[dir2])
	assert.True(t, descs["feature one"])
	assert.True(t, descs["feature two"])
}

// createTestGitRepo creates a temporary directory with a git repo initialized.
func createTestGitRepo(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()

	// init git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	require.NoError(t, cmd.Run())

	// configure git for commits
	cmd = exec.Command("git", "config", "user.email", "test@test.com")
	cmd.Dir = tmpDir
	_ = cmd.Run()

	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = tmpDir
	_ = cmd.Run()

	// create initial commit
	readmePath := filepath.Join(tmpDir, "README.md")
	require.NoError(t, os.WriteFile(readmePath, []byte("# Test"), 0o600))

	cmd = exec.Command("git", "add", "README.md")
	cmd.Dir = tmpDir
	_ = cmd.Run()

	cmd = exec.Command("git", "commit", "-m", "Initial commit")
	cmd.Dir = tmpDir
	_ = cmd.Run()

	return tmpDir
}

func writePlanProgress(t *testing.T, path, description string) {
	t.Helper()
	content := `# Ralphex Progress Log
Plan: ` + description + `
Branch: main
Mode: plan
Started: 2026-01-25 10:30:00
------------------------------------------------------------

[26-01-25 10:30:01] Working...
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func TestPlanRunner_ResumePlan(t *testing.T) {
	t.Run("returns error for non-existent progress file", func(t *testing.T) {
		runner := NewPlanRunner(&config.Config{}, nil)

		_, err := runner.ResumePlan("/nonexistent/progress.txt")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "progress file error")
	})

	t.Run("returns error when path is a directory", func(t *testing.T) {
		tmpDir := t.TempDir()
		runner := NewPlanRunner(&config.Config{}, nil)

		_, err := runner.ResumePlan(tmpDir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a file")
	})

	t.Run("returns error for non-plan mode progress file", func(t *testing.T) {
		tmpDir := t.TempDir()
		progressPath := filepath.Join(tmpDir, "progress-test.txt")
		progressContent := `# Ralphex Progress Log
Plan: docs/plans/feature.md
Branch: main
Mode: full
Started: 2026-01-25 10:30:00
------------------------------------------------------------

[26-01-25 10:30:01] Working...
`
		require.NoError(t, os.WriteFile(progressPath, []byte(progressContent), 0o600))

		runner := NewPlanRunner(&config.Config{}, nil)

		_, err := runner.ResumePlan(progressPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a plan mode session")
	})

	t.Run("returns error when progress file has no mode", func(t *testing.T) {
		tmpDir := t.TempDir()
		progressPath := filepath.Join(tmpDir, "progress-test.txt")
		// missing Mode: header (mode is empty)
		progressContent := `# Ralphex Progress Log
Plan: docs/plans/feature.md
Branch: main
Started: 2026-01-25 10:30:00
------------------------------------------------------------

[26-01-25 10:30:01] Working...
`
		require.NoError(t, os.WriteFile(progressPath, []byte(progressContent), 0o600))

		runner := NewPlanRunner(&config.Config{}, nil)

		_, err := runner.ResumePlan(progressPath)
		require.Error(t, err)
		// empty mode is treated as "not a plan mode session"
		assert.Contains(t, err.Error(), "not a plan mode session")
	})

	t.Run("returns error when directory is not a git repo", func(t *testing.T) {
		tmpDir := t.TempDir()
		progressPath := filepath.Join(tmpDir, "progress-plan-test.txt")
		progressContent := `# Ralphex Progress Log
Plan: add authentication
Branch: main
Mode: plan
Started: 2026-01-25 10:30:00
------------------------------------------------------------

[26-01-25 10:30:01] Working...
`
		require.NoError(t, os.WriteFile(progressPath, []byte(progressContent), 0o600))

		runner := NewPlanRunner(&config.Config{}, nil)

		_, err := runner.ResumePlan(progressPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a git repository")
	})

	t.Run("successfully resumes valid plan session", func(t *testing.T) {
		tmpDir := createTestGitRepo(t)
		cfg := testConfig(t)
		cfg.PlansDir = filepath.Join(tmpDir, "docs", "plans")
		sm := NewSessionManager()
		defer sm.Close()
		runner := NewPlanRunner(cfg, sm)

		progressPath := filepath.Join(tmpDir, "progress-plan-test.txt")
		progressContent := `# Ralphex Progress Log
Plan: add authentication
Branch: main
Mode: plan
Started: 2026-01-25 10:30:00
------------------------------------------------------------

[26-01-25 10:30:01] Working...
`
		require.NoError(t, os.WriteFile(progressPath, []byte(progressContent), 0o600))

		session, err := runner.ResumePlan(progressPath)
		require.NoError(t, err)
		require.NotNil(t, session)

		assert.Equal(t, SessionStateActive, session.GetState())
		assert.NotNil(t, session.GetInputCollector())

		meta := session.GetMetadata()
		assert.Equal(t, "plan", meta.Mode)
		assert.Equal(t, "add authentication", meta.PlanPath)

		// cleanup
		_ = runner.CancelPlan(session.ID)
		time.Sleep(100 * time.Millisecond)
	})

	t.Run("reuses existing session from session manager", func(t *testing.T) {
		tmpDir := createTestGitRepo(t)
		cfg := testConfig(t)
		cfg.PlansDir = filepath.Join(tmpDir, "docs", "plans")
		sm := NewSessionManager()
		defer sm.Close()
		runner := NewPlanRunner(cfg, sm)

		progressPath := filepath.Join(tmpDir, "progress-plan-test.txt")
		progressContent := `# Ralphex Progress Log
Plan: add authentication
Branch: main
Mode: plan
Started: 2026-01-25 10:30:00
------------------------------------------------------------

[26-01-25 10:30:01] Working...
`
		require.NoError(t, os.WriteFile(progressPath, []byte(progressContent), 0o600))

		// pre-register a session
		existingSession := NewSession(sessionIDFromPath(progressPath), progressPath)
		sm.Register(existingSession)

		session, err := runner.ResumePlan(progressPath)
		require.NoError(t, err)
		require.NotNil(t, session)

		// should be the same session
		assert.Equal(t, existingSession.ID, session.ID)

		// cleanup
		_ = runner.CancelPlan(session.ID)
		time.Sleep(100 * time.Millisecond)
	})

	t.Run("falls back to original branch on error", func(t *testing.T) {
		tmpDir := createTestGitRepo(t)
		cfg := testConfig(t)
		cfg.PlansDir = filepath.Join(tmpDir, "docs", "plans")
		runner := NewPlanRunner(cfg, nil)

		progressPath := filepath.Join(tmpDir, "progress-plan-test.txt")
		progressContent := `# Ralphex Progress Log
Plan: add authentication
Branch: feature-branch
Mode: plan
Started: 2026-01-25 10:30:00
------------------------------------------------------------

[26-01-25 10:30:01] Working...
`
		require.NoError(t, os.WriteFile(progressPath, []byte(progressContent), 0o600))

		session, err := runner.ResumePlan(progressPath)
		require.NoError(t, err)
		require.NotNil(t, session)

		// cleanup
		_ = runner.CancelPlan(session.ID)
		time.Sleep(100 * time.Millisecond)
	})
}

func TestPlanRunner_cleanupSession(t *testing.T) {
	t.Run("removes session from tracking", func(t *testing.T) {
		runner := NewPlanRunner(&config.Config{}, nil)

		// manually add a session to tracking (simulate what StartPlan does)
		session := NewSession("test-session-id", "/tmp/progress.txt")
		defer session.Close()
		session.SetState(SessionStateActive)

		runner.mu.Lock()
		runner.sessions["test-session-id"] = &runningPlan{
			session: session,
			cancel:  func() {},
			dir:     "/tmp",
		}
		runner.mu.Unlock()

		// verify session exists
		assert.NotNil(t, runner.GetSession("test-session-id"))

		// cleanup session
		runner.cleanupSession("test-session-id")

		// verify session is removed
		assert.Nil(t, runner.GetSession("test-session-id"))
	})

	t.Run("sets session state to completed", func(t *testing.T) {
		runner := NewPlanRunner(&config.Config{}, nil)

		// manually add a session to tracking
		session := NewSession("test-session-id", "/tmp/progress.txt")
		defer session.Close()
		session.SetState(SessionStateActive)

		runner.mu.Lock()
		runner.sessions["test-session-id"] = &runningPlan{
			session: session,
			cancel:  func() {},
			dir:     "/tmp",
		}
		runner.mu.Unlock()

		// cleanup session
		runner.cleanupSession("test-session-id")

		// verify state changed
		assert.Equal(t, SessionStateCompleted, session.GetState())
	})

	t.Run("handles non-existent session gracefully", func(t *testing.T) {
		runner := NewPlanRunner(&config.Config{}, nil)

		// should not panic
		runner.cleanupSession("nonexistent-id")
	})
}

func TestPlanRunner_GetResumableSessions(t *testing.T) {
	t.Run("returns nil when config is nil", func(t *testing.T) {
		runner := NewPlanRunner(nil, nil)

		sessions, err := runner.GetResumableSessions()
		require.NoError(t, err)
		assert.Nil(t, sessions)
	})
}

func TestGenerateSessionID(t *testing.T) {
	t.Run("generates unique IDs", func(t *testing.T) {
		id1 := generateSessionID("test description")
		time.Sleep(time.Nanosecond) // ensure timestamp difference
		id2 := generateSessionID("test description")

		assert.NotEqual(t, id1, id2)
	})

	t.Run("sanitizes special characters", func(t *testing.T) {
		id := generateSessionID("Hello! @World# $Test%")

		// should not contain special characters (except dash)
		assert.NotContains(t, id, "!")
		assert.NotContains(t, id, "@")
		assert.NotContains(t, id, "#")
		assert.NotContains(t, id, "$")
		assert.NotContains(t, id, "%")
	})

	t.Run("truncates long descriptions", func(t *testing.T) {
		longDesc := "This is a very long description that exceeds twenty characters"
		id := generateSessionID(longDesc)

		// ID should be based on first 20 chars + timestamp
		// first part should not be longer than 20 chars (plus dashes)
		parts := strings.Split(id, "-")
		// join all parts except the last (timestamp)
		descPart := strings.Join(parts[:len(parts)-1], "-")
		assert.LessOrEqual(t, len(descPart), 20)
	})
}

func TestUniqueDirs(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name:     "removes duplicates",
			input:    []string{"/a", "/b", "/a", "/c", "/b"},
			expected: []string{"/a", "/b", "/c"},
		},
		{
			name:     "removes empty strings",
			input:    []string{"/a", "", "/b", ""},
			expected: []string{"/a", "/b"},
		},
		{
			name:     "handles empty input",
			input:    []string{},
			expected: []string{},
		},
		{
			name:     "handles all empty strings",
			input:    []string{"", "", ""},
			expected: []string{},
		},
		{
			name:     "preserves order",
			input:    []string{"/c", "/a", "/b"},
			expected: []string{"/c", "/a", "/b"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := uniqueDirs(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// testConfig returns a minimal config for testing.
func testConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		ClaudeCommand: "echo", // use echo as dummy command for testing
		PlansDir:      t.TempDir(),
		Colors: config.ColorConfig{
			Task:       "0,255,0",
			Review:     "0,255,255",
			Codex:      "255,0,255",
			ClaudeEval: "100,200,255",
			Warn:       "255,255,0",
			Error:      "255,0,0",
			Signal:     "0,255,0",
			Timestamp:  "150,150,150",
			Info:       "200,200,200",
		},
	}
}
