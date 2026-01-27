package web

import (
	"os"
	"os/exec"
	"path/filepath"
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
