package git

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockLogger implements Logger interface for testing.
type mockLogger struct {
	logs []string
}

func (m *mockLogger) Printf(format string, args ...any) (int, error) {
	m.logs = append(m.logs, fmt.Sprintf(format, args...))
	return 0, nil
}

// noopLogger returns a no-op logger.
func noopServiceLogger() Logger {
	return &mockLogger{}
}

func TestNewService(t *testing.T) {
	t.Run("opens valid repo", func(t *testing.T) {
		dir := setupTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)
		assert.NotNil(t, svc)
		assert.Equal(t, dir, svc.Root())
	})

	t.Run("fails on non-repo", func(t *testing.T) {
		dir := t.TempDir()
		_, err := NewService(dir, noopServiceLogger())
		assert.Error(t, err)
	})
}

func TestService_CreateBranchForPlan(t *testing.T) {
	t.Run("returns nil on feature branch", func(t *testing.T) {
		dir := setupTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		// create and switch to feature branch
		err = svc.CreateBranch("feature-test")
		require.NoError(t, err)

		log := &mockLogger{}
		svc.log = log

		err = svc.CreateBranchForPlan(filepath.Join(dir, "docs", "plans", "feature.md"))
		require.NoError(t, err)

		// should not have logged anything (no branch created)
		assert.Empty(t, log.logs)

		// should still be on feature-test
		branch, err := svc.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "feature-test", branch)
	})

	t.Run("creates branch from plan file name", func(t *testing.T) {
		dir := setupTestRepo(t)
		log := &mockLogger{}
		svc, err := NewService(dir, log)
		require.NoError(t, err)

		// create plan file
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "add-feature.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

		err = svc.CreateBranchForPlan(planFile)
		require.NoError(t, err)

		// should have created branch
		branch, err := svc.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "add-feature", branch)

		// should have logged creation
		assert.Len(t, log.logs, 2) // creating branch + committing plan
	})

	t.Run("switches to existing branch", func(t *testing.T) {
		dir := setupTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		// create the branch first but stay on master
		err = svc.CreateBranch("existing-feature")
		require.NoError(t, err)
		err = svc.repo.CheckoutBranch("master")
		require.NoError(t, err)

		log := &mockLogger{}
		svc.log = log

		// create plan file with matching name
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "existing-feature.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

		err = svc.CreateBranchForPlan(planFile)
		require.NoError(t, err)

		// should have switched to existing branch
		branch, err := svc.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "existing-feature", branch)

		// first log should mention "switching"
		assert.Contains(t, log.logs[0], "switching")
	})

	t.Run("fails with other uncommitted changes", func(t *testing.T) {
		dir := setupTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		// create plan file
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "feature.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

		// create another uncommitted file
		otherFile := filepath.Join(dir, "other.txt")
		require.NoError(t, os.WriteFile(otherFile, []byte("other content"), 0o600))

		err = svc.CreateBranchForPlan(planFile)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "worktree has uncommitted changes")
	})

	t.Run("auto-commits plan file if only dirty file", func(t *testing.T) {
		dir := setupTestRepo(t)
		log := &mockLogger{}
		svc, err := NewService(dir, log)
		require.NoError(t, err)

		// create untracked plan file (the only dirty file)
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "new-feature.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# New Feature Plan"), 0o600))

		err = svc.CreateBranchForPlan(planFile)
		require.NoError(t, err)

		// should have created branch and committed plan
		assert.Len(t, log.logs, 2)
		assert.Contains(t, log.logs[1], "committing plan file")

		// verify plan was committed
		hasChanges, err := svc.repo.FileHasChanges(planFile)
		require.NoError(t, err)
		assert.False(t, hasChanges, "plan file should be committed")
	})

	t.Run("does not commit if plan already committed", func(t *testing.T) {
		dir := setupTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		// create and commit plan file while on master
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "committed-feature.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))
		require.NoError(t, svc.repo.Add(planFile))
		require.NoError(t, svc.repo.Commit("add plan"))

		log := &mockLogger{}
		svc.log = log

		err = svc.CreateBranchForPlan(planFile)
		require.NoError(t, err)

		// should only have one log (creating branch, no committing)
		assert.Len(t, log.logs, 1)
		assert.Contains(t, log.logs[0], "creating branch")
	})

	t.Run("strips date prefix from branch name", func(t *testing.T) {
		dir := setupTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		// create plan file with date prefix
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "2024-01-15-add-auth.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

		err = svc.CreateBranchForPlan(planFile)
		require.NoError(t, err)

		// branch name should not have date prefix
		branch, err := svc.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "add-auth", branch)
	})
}

func TestService_MovePlanToCompleted(t *testing.T) {
	t.Run("moves tracked file", func(t *testing.T) {
		dir := setupTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		// create and commit plan file
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "feature.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))
		require.NoError(t, svc.repo.Add(planFile))
		require.NoError(t, svc.repo.Commit("add plan"))

		log := &mockLogger{}
		svc.log = log

		err = svc.MovePlanToCompleted(planFile)
		require.NoError(t, err)

		// original file should not exist
		_, err = os.Stat(planFile)
		assert.True(t, os.IsNotExist(err))

		// completed file should exist
		completedPath := filepath.Join(plansDir, "completed", "feature.md")
		_, err = os.Stat(completedPath)
		require.NoError(t, err)

		// should have logged the move
		assert.Len(t, log.logs, 1)
		assert.Contains(t, log.logs[0], "moved plan")
	})

	t.Run("moves untracked file", func(t *testing.T) {
		dir := setupTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		// create untracked plan file
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "untracked-feature.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

		err = svc.MovePlanToCompleted(planFile)
		require.NoError(t, err)

		// original file should not exist
		_, err = os.Stat(planFile)
		assert.True(t, os.IsNotExist(err))

		// completed file should exist
		completedPath := filepath.Join(plansDir, "completed", "untracked-feature.md")
		_, err = os.Stat(completedPath)
		require.NoError(t, err)
	})

	t.Run("creates completed directory", func(t *testing.T) {
		dir := setupTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		// create plan file
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "feature.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))
		require.NoError(t, svc.repo.Add(planFile))
		require.NoError(t, svc.repo.Commit("add plan"))

		// verify completed dir doesn't exist
		completedDir := filepath.Join(plansDir, "completed")
		_, err = os.Stat(completedDir)
		require.True(t, os.IsNotExist(err))

		err = svc.MovePlanToCompleted(planFile)
		require.NoError(t, err)

		// completed dir should now exist
		info, err := os.Stat(completedDir)
		require.NoError(t, err)
		assert.True(t, info.IsDir())
	})

	t.Run("returns nil if already moved to completed", func(t *testing.T) {
		dir := setupTestRepo(t)
		log := &mockLogger{}
		svc, err := NewService(dir, log)
		require.NoError(t, err)

		// create completed directory with plan file already there (simulating prior move)
		plansDir := filepath.Join(dir, "docs", "plans")
		completedDir := filepath.Join(plansDir, "completed")
		require.NoError(t, os.MkdirAll(completedDir, 0o750))
		completedPath := filepath.Join(completedDir, "already-moved.md")
		require.NoError(t, os.WriteFile(completedPath, []byte("# Plan"), 0o600))

		// source file does not exist
		planFile := filepath.Join(plansDir, "already-moved.md")
		_, err = os.Stat(planFile)
		require.True(t, os.IsNotExist(err))

		// should return nil (not error)
		err = svc.MovePlanToCompleted(planFile)
		require.NoError(t, err)

		// should have logged skip message
		require.Len(t, log.logs, 1)
		assert.Contains(t, log.logs[0], "already in completed")
	})
}

func TestService_EnsureHasCommits(t *testing.T) {
	t.Run("returns nil when repo has commits", func(t *testing.T) {
		dir := setupTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		promptCalled := false
		promptFn := func() bool {
			promptCalled = true
			return true
		}

		err = svc.EnsureHasCommits(promptFn)
		require.NoError(t, err)

		// prompt should not have been called
		assert.False(t, promptCalled)
	})

	t.Run("creates initial commit when user accepts", func(t *testing.T) {
		// create empty repo (no commits)
		dir := t.TempDir()
		_, err := git.PlainInit(dir, false)
		require.NoError(t, err)

		// create a file to commit
		require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test"), 0o600))

		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		promptCalled := false
		promptFn := func() bool {
			promptCalled = true
			return true
		}

		err = svc.EnsureHasCommits(promptFn)
		require.NoError(t, err)

		// prompt should have been called
		assert.True(t, promptCalled)

		// repo should now have commits
		hasCommits, err := svc.HasCommits()
		require.NoError(t, err)
		assert.True(t, hasCommits)
	})

	t.Run("returns error when user declines", func(t *testing.T) {
		// create empty repo (no commits)
		dir := t.TempDir()
		_, err := git.PlainInit(dir, false)
		require.NoError(t, err)

		// create a file so we're not completely empty
		require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test"), 0o600))

		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		promptFn := func() bool { return false }

		err = svc.EnsureHasCommits(promptFn)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no commits")
	})

	t.Run("returns error when no files to commit", func(t *testing.T) {
		// create empty repo with no files
		dir := t.TempDir()
		_, err := git.PlainInit(dir, false)
		require.NoError(t, err)

		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		promptFn := func() bool { return true }

		err = svc.EnsureHasCommits(promptFn)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no files to commit")
	})
}

func TestService_EnsureIgnored(t *testing.T) {
	t.Run("adds pattern to gitignore", func(t *testing.T) {
		dir := setupTestRepo(t)
		log := &mockLogger{}
		svc, err := NewService(dir, log)
		require.NoError(t, err)

		err = svc.EnsureIgnored("progress*.txt", "progress-test.txt")
		require.NoError(t, err)
		assert.Len(t, log.logs, 1)
		assert.Contains(t, log.logs[0], "progress*.txt", "log message should contain pattern")

		// verify pattern was added to .gitignore
		gitignorePath := filepath.Join(dir, ".gitignore")
		content, err := os.ReadFile(gitignorePath) //nolint:gosec // test file
		require.NoError(t, err)
		assert.Contains(t, string(content), "progress*.txt")
	})

	t.Run("does nothing if already ignored", func(t *testing.T) {
		dir := setupTestRepo(t)

		// create gitignore with pattern
		gitignorePath := filepath.Join(dir, ".gitignore")
		err := os.WriteFile(gitignorePath, []byte("progress*.txt\n"), 0o600)
		require.NoError(t, err)

		log := &mockLogger{}
		svc, err := NewService(dir, log)
		require.NoError(t, err)

		err = svc.EnsureIgnored("progress*.txt", "progress-test.txt")
		require.NoError(t, err)
		assert.Empty(t, log.logs, "log should not be called if already ignored")

		// verify gitignore wasn't modified (no duplicate pattern)
		content, err := os.ReadFile(gitignorePath) //nolint:gosec // test file
		require.NoError(t, err)
		assert.Equal(t, "progress*.txt\n", string(content))
	})

	t.Run("creates gitignore if missing", func(t *testing.T) {
		dir := setupTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		// verify no .gitignore exists
		gitignorePath := filepath.Join(dir, ".gitignore")
		_, err = os.Stat(gitignorePath)
		assert.True(t, os.IsNotExist(err))

		err = svc.EnsureIgnored("*.log", "test.log")
		require.NoError(t, err)

		// verify .gitignore was created
		content, err := os.ReadFile(gitignorePath) //nolint:gosec // test file
		require.NoError(t, err)
		assert.Contains(t, string(content), "*.log")
	})

	t.Run("appends to existing gitignore", func(t *testing.T) {
		dir := setupTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		// create gitignore with existing content
		gitignorePath := filepath.Join(dir, ".gitignore")
		err = os.WriteFile(gitignorePath, []byte("*.log\n"), 0o600)
		require.NoError(t, err)

		err = svc.EnsureIgnored("*.tmp", "test.tmp")
		require.NoError(t, err)

		// verify both patterns exist
		content, err := os.ReadFile(gitignorePath) //nolint:gosec // test file
		require.NoError(t, err)
		assert.Contains(t, string(content), "*.log")
		assert.Contains(t, string(content), "*.tmp")
	})
}
