package git

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)
		assert.NotNil(t, svc)

		// resolve symlinks for consistent path comparison (macOS /var -> /private/var)
		expected, err := filepath.EvalSymlinks(dir)
		require.NoError(t, err)
		assert.Equal(t, expected, svc.Root())
	})

	t.Run("fails on non-repo", func(t *testing.T) {
		dir := t.TempDir()
		_, err := NewService(dir, noopServiceLogger())
		assert.Error(t, err)
	})

	t.Run("accepts custom vcs command", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger(), "git")
		require.NoError(t, err)
		assert.NotNil(t, svc)

		// verify it works normally with explicit "git"
		branch, err := svc.CurrentBranch()
		require.NoError(t, err)
		assert.NotEmpty(t, branch)
	})

	t.Run("defaults to git when vcs command is empty", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger(), "")
		require.NoError(t, err)
		assert.NotNil(t, svc)
	})

	t.Run("defaults to git when no vcs command provided", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)
		assert.NotNil(t, svc)
	})

	t.Run("fails with invalid vcs command", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		_, err := NewService(dir, noopServiceLogger(), "nonexistent-vcs")
		require.Error(t, err)
	})
}

func TestService_IsDefaultBranch(t *testing.T) {
	t.Run("returns true for master with empty default", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		isDefault, err := svc.IsDefaultBranch("")
		require.NoError(t, err)
		assert.True(t, isDefault)
	})

	t.Run("returns true for main with empty default", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		err = svc.CreateBranch("main")
		require.NoError(t, err)

		isDefault, err := svc.IsDefaultBranch("")
		require.NoError(t, err)
		assert.True(t, isDefault)
	})

	t.Run("returns true for master with explicit default", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		isDefault, err := svc.IsDefaultBranch("master")
		require.NoError(t, err)
		assert.True(t, isDefault)
	})

	t.Run("returns true for develop branch", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		err = svc.CreateBranch("develop")
		require.NoError(t, err)

		isDefault, err := svc.IsDefaultBranch("develop")
		require.NoError(t, err)
		assert.True(t, isDefault)
	})

	t.Run("returns true for trunk branch", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		err = svc.CreateBranch("trunk")
		require.NoError(t, err)

		isDefault, err := svc.IsDefaultBranch("trunk")
		require.NoError(t, err)
		assert.True(t, isDefault)
	})

	t.Run("strips origin prefix from default branch", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		// on master, default branch "origin/master" should match after stripping prefix
		isDefault, err := svc.IsDefaultBranch("origin/master")
		require.NoError(t, err)
		assert.True(t, isDefault)
	})

	t.Run("returns false for feature branch", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		err = svc.CreateBranch("feature-test")
		require.NoError(t, err)

		isDefault, err := svc.IsDefaultBranch("master")
		require.NoError(t, err)
		assert.False(t, isDefault)
	})

	t.Run("returns false for detached HEAD", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		hash, err := svc.HeadHash()
		require.NoError(t, err)

		// checkout commit directly via git CLI to create detached HEAD
		runGit(t, dir, "checkout", hash)

		// re-open service to pick up detached HEAD state
		svc, err = NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		isDefault, err := svc.IsDefaultBranch("")
		require.NoError(t, err)
		assert.False(t, isDefault)
	})
}

func TestService_CreateBranchForPlan(t *testing.T) {
	t.Run("returns nil on feature branch", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		// create and switch to feature branch
		err = svc.CreateBranch("feature-test")
		require.NoError(t, err)

		log := &mockLogger{}
		svc.log = log

		err = svc.CreateBranchForPlan(filepath.Join(dir, "docs", "plans", "feature.md"), "master")
		require.NoError(t, err)

		// should not have logged anything (no branch created)
		assert.Empty(t, log.logs)

		// should still be on feature-test
		branch, err := svc.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "feature-test", branch)
	})

	t.Run("creates branch from plan file name", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		log := &mockLogger{}
		svc, err := NewService(dir, log)
		require.NoError(t, err)

		// create plan file
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "add-feature.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

		err = svc.CreateBranchForPlan(planFile, "master")
		require.NoError(t, err)

		// should have created branch
		branch, err := svc.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "add-feature", branch)

		// should have logged creation
		assert.Len(t, log.logs, 2) // creating branch + committing plan
	})

	t.Run("switches to existing branch", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		// create the branch first but stay on master
		err = svc.CreateBranch("existing-feature")
		require.NoError(t, err)
		err = svc.repo.checkoutBranch("master")
		require.NoError(t, err)

		log := &mockLogger{}
		svc.log = log

		// create plan file with matching name
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "existing-feature.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

		err = svc.CreateBranchForPlan(planFile, "master")
		require.NoError(t, err)

		// should have switched to existing branch
		branch, err := svc.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "existing-feature", branch)

		// first log should mention "switching"
		assert.Contains(t, log.logs[0], "switching")
	})

	t.Run("fails with other uncommitted changes", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
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

		err = svc.CreateBranchForPlan(planFile, "master")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "worktree has uncommitted changes")
		assert.Contains(t, err.Error(), "other.txt")
	})

	t.Run("auto-commits plan file if only dirty file", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		log := &mockLogger{}
		svc, err := NewService(dir, log)
		require.NoError(t, err)

		// create untracked plan file (the only dirty file)
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "new-feature.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# New Feature Plan"), 0o600))

		err = svc.CreateBranchForPlan(planFile, "master")
		require.NoError(t, err)

		// should have created branch and committed plan
		assert.Len(t, log.logs, 2)
		assert.Contains(t, log.logs[1], "committing plan file")

		// verify plan was committed
		hasChanges, err := svc.repo.fileHasChanges(planFile)
		require.NoError(t, err)
		assert.False(t, hasChanges, "plan file should be committed")
	})

	t.Run("does not commit if plan already committed", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		// create and commit plan file while on master
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "committed-feature.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))
		require.NoError(t, svc.repo.add(planFile))
		require.NoError(t, svc.repo.commit("add plan"))

		log := &mockLogger{}
		svc.log = log

		err = svc.CreateBranchForPlan(planFile, "master")
		require.NoError(t, err)

		// should only have one log (creating branch, no committing)
		assert.Len(t, log.logs, 1)
		assert.Contains(t, log.logs[0], "creating branch")
	})

	t.Run("strips date prefix from branch name", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		// create plan file with date prefix
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "2024-01-15-add-auth.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

		err = svc.CreateBranchForPlan(planFile, "master")
		require.NoError(t, err)

		// branch name should not have date prefix
		branch, err := svc.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "add-auth", branch)
	})

	t.Run("creates branch from develop as default branch", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		// switch to develop branch (simulating gitflow default)
		require.NoError(t, svc.CreateBranch("develop"))

		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "gitflow-feature.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

		log := &mockLogger{}
		svc.log = log

		err = svc.CreateBranchForPlan(planFile, "develop")
		require.NoError(t, err)

		branch, err := svc.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "gitflow-feature", branch)
		assert.Len(t, log.logs, 2) // creating branch + committing plan
	})

	t.Run("skips branch creation with origin prefix default", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		// switch to feature branch
		require.NoError(t, svc.CreateBranch("feature-x"))

		log := &mockLogger{}
		svc.log = log

		// default branch is "origin/master" but we're on feature-x, should skip
		err = svc.CreateBranchForPlan(filepath.Join(dir, "docs", "plans", "feature.md"), "origin/master")
		require.NoError(t, err)
		assert.Empty(t, log.logs) // no branch created
	})
}

func TestService_MovePlanToCompleted(t *testing.T) {
	t.Run("moves tracked file", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		// create and commit plan file
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "feature.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))
		require.NoError(t, svc.repo.add(planFile))
		require.NoError(t, svc.repo.commit("add plan"))

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
		dir := setupExternalTestRepo(t)
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
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		// create plan file
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "feature.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))
		require.NoError(t, svc.repo.add(planFile))
		require.NoError(t, svc.repo.commit("add plan"))

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
		dir := setupExternalTestRepo(t)
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
		dir := setupExternalTestRepo(t)
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
		runGit(t, dir, "init")
		runGit(t, dir, "config", "user.email", "test@test.com")
		runGit(t, dir, "config", "user.name", "test")
		runGit(t, dir, "config", "commit.gpgsign", "false")

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

	t.Run("creates initial commit with trailer when configured", func(t *testing.T) {
		dir := t.TempDir()
		runGit(t, dir, "init")
		runGit(t, dir, "config", "user.email", "test@test.com")
		runGit(t, dir, "config", "user.name", "test")
		runGit(t, dir, "config", "commit.gpgsign", "false")

		require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test"), 0o600))

		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)
		svc.SetCommitTrailer("Co-authored-by: ralphex <noreply@ralphex.com>")

		err = svc.EnsureHasCommits(func() bool { return true })
		require.NoError(t, err)

		// verify trailer in commit message
		out := runGit(t, dir, "log", "-1", "--format=%B")
		assert.Contains(t, out, "Co-authored-by: ralphex <noreply@ralphex.com>")
	})

	t.Run("returns error when user declines", func(t *testing.T) {
		// create empty repo (no commits)
		dir := t.TempDir()
		runGit(t, dir, "init")
		runGit(t, dir, "config", "user.email", "test@test.com")
		runGit(t, dir, "config", "user.name", "test")
		runGit(t, dir, "config", "commit.gpgsign", "false")

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
		runGit(t, dir, "init")
		runGit(t, dir, "config", "user.email", "test@test.com")
		runGit(t, dir, "config", "user.name", "test")
		runGit(t, dir, "config", "commit.gpgsign", "false")

		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		promptFn := func() bool { return true }

		err = svc.EnsureHasCommits(promptFn)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no files to commit")
	})
}

func TestService_EnsureLocalGitignore(t *testing.T) {
	t.Run("creates .ralphex/.gitignore", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		log := &mockLogger{}
		svc, err := NewService(dir, log)
		require.NoError(t, err)

		err = svc.EnsureLocalGitignore()
		require.NoError(t, err)
		assert.Len(t, log.logs, 1)
		assert.Contains(t, log.logs[0], ".ralphex/.gitignore")

		gitignorePath := filepath.Join(dir, ".ralphex", ".gitignore")
		content, err := os.ReadFile(gitignorePath) //nolint:gosec // test file
		require.NoError(t, err)
		assert.Equal(t, ".gitignore\nprogress/\nworktrees/\n", string(content))
	})

	t.Run("idempotent when content matches", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		log := &mockLogger{}
		svc, err := NewService(dir, log)
		require.NoError(t, err)

		require.NoError(t, os.MkdirAll(filepath.Join(dir, ".ralphex"), 0o750))
		require.NoError(t, os.WriteFile(
			filepath.Join(dir, ".ralphex", ".gitignore"),
			[]byte(".gitignore\nprogress/\nworktrees/\n"), 0o600))

		err = svc.EnsureLocalGitignore()
		require.NoError(t, err)
		assert.Empty(t, log.logs, "should not log when content already matches")
	})

	t.Run("overwrites stale content", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		log := &mockLogger{}
		svc, err := NewService(dir, log)
		require.NoError(t, err)

		require.NoError(t, os.MkdirAll(filepath.Join(dir, ".ralphex"), 0o750))
		require.NoError(t, os.WriteFile(
			filepath.Join(dir, ".ralphex", ".gitignore"),
			[]byte("old-content\n"), 0o600))

		err = svc.EnsureLocalGitignore()
		require.NoError(t, err)
		assert.Len(t, log.logs, 1)

		content, err := os.ReadFile(filepath.Join(dir, ".ralphex", ".gitignore")) //nolint:gosec // test file
		require.NoError(t, err)
		assert.Equal(t, ".gitignore\nprogress/\nworktrees/\n", string(content))
	})

	t.Run("creates .ralphex dir if missing", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		_, err = os.Stat(filepath.Join(dir, ".ralphex"))
		assert.True(t, os.IsNotExist(err))

		err = svc.EnsureLocalGitignore()
		require.NoError(t, err)

		info, err := os.Stat(filepath.Join(dir, ".ralphex"))
		require.NoError(t, err)
		assert.True(t, info.IsDir())
	})

	t.Run("does not modify root .gitignore", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		rootGitignore := filepath.Join(dir, ".gitignore")
		_, err = os.Stat(rootGitignore)
		rootExistedBefore := !os.IsNotExist(err)

		err = svc.EnsureLocalGitignore()
		require.NoError(t, err)

		if !rootExistedBefore {
			_, err = os.Stat(rootGitignore)
			assert.True(t, os.IsNotExist(err), "root .gitignore should not be created")
		}
	})
}

func TestService_GetDefaultBranch(t *testing.T) {
	t.Run("returns detected default branch", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		branch := svc.GetDefaultBranch()
		assert.Equal(t, "master", branch)
	})

	t.Run("returns main when main branch exists", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		// create main branch
		err = svc.CreateBranch("main")
		require.NoError(t, err)

		branch := svc.GetDefaultBranch()
		assert.Equal(t, "main", branch)
	})
}

func TestService_DiffStats(t *testing.T) {
	t.Run("returns zero stats when on same branch", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		stats, err := svc.DiffStats("master")
		require.NoError(t, err)
		assert.Equal(t, 0, stats.Files)
		assert.Equal(t, 0, stats.Additions)
		assert.Equal(t, 0, stats.Deletions)
	})

	t.Run("returns zero stats for nonexistent branch", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		stats, err := svc.DiffStats("nonexistent")
		require.NoError(t, err)
		assert.Equal(t, 0, stats.Files)
	})

	t.Run("returns stats for changes on feature branch", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		// create feature branch
		err = svc.CreateBranch("feature")
		require.NoError(t, err)

		// add a new file
		newFile := filepath.Join(dir, "feature.txt")
		require.NoError(t, os.WriteFile(newFile, []byte("line1\nline2\n"), 0o600))
		require.NoError(t, svc.repo.add("feature.txt"))
		require.NoError(t, svc.repo.commit("add feature file"))

		stats, err := svc.DiffStats("master")
		require.NoError(t, err)
		assert.Equal(t, 1, stats.Files)
		assert.Equal(t, 2, stats.Additions)
		assert.Equal(t, 0, stats.Deletions)
	})

	t.Run("returns stats using commit hash as base ref", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		// get initial commit hash to use as base ref
		baseHash := strings.TrimSpace(runGit(t, dir, "rev-parse", "HEAD"))

		// create feature branch with changes
		err = svc.CreateBranch("feature")
		require.NoError(t, err)

		newFile := filepath.Join(dir, "feature.txt")
		require.NoError(t, os.WriteFile(newFile, []byte("line1\nline2\nline3\n"), 0o600))
		require.NoError(t, svc.repo.add("feature.txt"))
		require.NoError(t, svc.repo.commit("add feature file"))

		// use commit hash instead of branch name
		stats, err := svc.DiffStats(baseHash)
		require.NoError(t, err)
		assert.Equal(t, 1, stats.Files)
		assert.Equal(t, 3, stats.Additions)
		assert.Equal(t, 0, stats.Deletions)

		// also works with short hash (7 chars)
		shortHash := baseHash[:7]
		stats, err = svc.DiffStats(shortHash)
		require.NoError(t, err)
		assert.Equal(t, 1, stats.Files)
		assert.Equal(t, 3, stats.Additions)
		assert.Equal(t, 0, stats.Deletions)
	})
}

func TestService_CreateWorktreeForPlan(t *testing.T) {
	t.Run("creates worktree with new branch", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		log := &mockLogger{}
		svc, err := NewService(dir, log)
		require.NoError(t, err)

		// create plan file
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "add-worktree.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

		wtPath, planNeedsCommit, err := svc.CreateWorktreeForPlan(planFile, "master")
		require.NoError(t, err)
		assert.True(t, planNeedsCommit, "untracked plan file should need commit")
		assert.Contains(t, wtPath, filepath.Join(".ralphex", "worktrees", "add-worktree"))

		// verify worktree exists and is on the correct branch
		wtSvc, err := NewService(wtPath, noopServiceLogger())
		require.NoError(t, err)
		branch, err := wtSvc.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "add-worktree", branch)

		// cleanup
		require.NoError(t, svc.RemoveWorktree(wtPath))
	})

	t.Run("creates worktree with existing branch", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		// create the branch first but stay on master
		require.NoError(t, svc.CreateBranch("existing-feature"))
		require.NoError(t, svc.repo.checkoutBranch("master"))

		// create plan file with matching name
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "existing-feature.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))
		require.NoError(t, svc.repo.add(planFile))
		require.NoError(t, svc.repo.commit("add plan"))

		log := &mockLogger{}
		svc.log = log

		wtPath, planNeedsCommit, err := svc.CreateWorktreeForPlan(planFile, "master")
		require.NoError(t, err)
		assert.False(t, planNeedsCommit, "already-committed plan file should not need commit")

		// verify worktree uses existing branch
		wtSvc, err := NewService(wtPath, noopServiceLogger())
		require.NoError(t, err)
		branch, err := wtSvc.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "existing-feature", branch)

		assert.Contains(t, log.logs[0], "existing branch")

		// cleanup
		require.NoError(t, svc.RemoveWorktree(wtPath))
	})

	t.Run("fails when not on default branch", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		// switch to feature branch
		require.NoError(t, svc.CreateBranch("feature"))

		planFile := filepath.Join(dir, "docs", "plans", "feature.md")
		_, _, err = svc.CreateWorktreeForPlan(planFile, "master")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "requires master branch")
	})

	t.Run("fails with fallback error when empty default branch on feature", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		// switch to feature branch
		require.NoError(t, svc.CreateBranch("feature"))

		planFile := filepath.Join(dir, "docs", "plans", "feature.md")
		_, _, err = svc.CreateWorktreeForPlan(planFile, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "requires main/master branch")
	})

	t.Run("fails when not on develop default branch", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		// switch to feature branch
		require.NoError(t, svc.CreateBranch("feature"))

		planFile := filepath.Join(dir, "docs", "plans", "feature.md")
		_, _, err = svc.CreateWorktreeForPlan(planFile, "develop")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "requires develop branch")
	})

	t.Run("succeeds from develop when develop is default", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		// switch to develop branch
		require.NoError(t, svc.CreateBranch("develop"))

		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "develop-feature.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

		wtPath, planNeedsCommit, err := svc.CreateWorktreeForPlan(planFile, "develop")
		require.NoError(t, err)
		assert.Contains(t, wtPath, "develop-feature")
		assert.True(t, planNeedsCommit, "untracked plan file should need commit")

		// cleanup
		require.NoError(t, svc.RemoveWorktree(wtPath))
	})

	t.Run("fails with other uncommitted changes", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		// create plan file
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "feature.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

		// create another uncommitted file
		require.NoError(t, os.WriteFile(filepath.Join(dir, "other.txt"), []byte("other"), 0o600))

		_, _, err = svc.CreateWorktreeForPlan(planFile, "master")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot create worktree")
		assert.Contains(t, err.Error(), "uncommitted changes")
		assert.Contains(t, err.Error(), "other.txt")
	})

	t.Run("fails when worktree already exists", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		// create plan file
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "dup-worktree.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

		// create first worktree
		wtPath, planNeedsCommit, err := svc.CreateWorktreeForPlan(planFile, "master")
		require.NoError(t, err)
		assert.True(t, planNeedsCommit, "untracked plan file should need commit")

		// switch back to master for second attempt
		require.NoError(t, svc.repo.checkoutBranch("master"))

		// second attempt should fail
		_, _, err = svc.CreateWorktreeForPlan(planFile, "master")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "worktree already exists")

		// cleanup
		require.NoError(t, svc.RemoveWorktree(wtPath))
	})

	t.Run("auto-commits plan file", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		log := &mockLogger{}
		svc, err := NewService(dir, log)
		require.NoError(t, err)

		// create untracked plan file
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "new-feature.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# New Feature"), 0o600))

		wtPath, planNeedsCommit, err := svc.CreateWorktreeForPlan(planFile, "master")
		require.NoError(t, err)
		assert.True(t, planNeedsCommit, "untracked plan file should need commit")

		// verify plan file was copied into worktree
		wtPlanFile := filepath.Join(wtPath, "docs", "plans", "new-feature.md")
		_, statErr := os.Stat(wtPlanFile)
		assert.NoError(t, statErr, "plan file should exist in worktree")

		// cleanup
		require.NoError(t, svc.RemoveWorktree(wtPath))
	})

	t.Run("does not commit plan on main", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		// record HEAD before creating worktree
		headBefore, err := svc.repo.headHash()
		require.NoError(t, err)

		// create untracked plan file
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "no-commit-on-main.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Regression Test"), 0o600))

		wtPath, planNeedsCommit, err := svc.CreateWorktreeForPlan(planFile, "master")
		require.NoError(t, err)
		assert.True(t, planNeedsCommit)

		// main repo HEAD must not have advanced (plan is NOT committed on main)
		headAfter, err := svc.repo.headHash()
		require.NoError(t, err)
		assert.Equal(t, headBefore, headAfter, "HEAD on main must not change after CreateWorktreeForPlan")

		// cleanup
		require.NoError(t, svc.RemoveWorktree(wtPath))
	})

	t.Run("fails when branch is checked out in another worktree", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		// create plan file and first worktree
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "branch-conflict.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

		wtPath, planNeedsCommit, err := svc.CreateWorktreeForPlan(planFile, "master")
		require.NoError(t, err)
		assert.True(t, planNeedsCommit, "untracked plan file should need commit")
		defer svc.RemoveWorktree(wtPath) //nolint:errcheck // cleanup

		// try to create second worktree at different path but same branch.
		// use AddWorktree directly to bypass dir-exists check.
		secondPath := filepath.Join(dir, ".ralphex", "worktrees", "branch-conflict-2")
		err = svc.repo.addWorktree(secondPath, "branch-conflict", false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "already used by worktree")
	})

	t.Run("strips date prefix from branch name", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "2024-01-15-add-auth.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

		wtPath, planNeedsCommit, err := svc.CreateWorktreeForPlan(planFile, "master")
		require.NoError(t, err)
		assert.True(t, planNeedsCommit, "untracked plan file should need commit")
		assert.Contains(t, wtPath, "add-auth")

		// verify branch name
		wtSvc, err := NewService(wtPath, noopServiceLogger())
		require.NoError(t, err)
		branch, err := wtSvc.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "add-auth", branch)

		// cleanup
		require.NoError(t, svc.RemoveWorktree(wtPath))
	})
}

func TestService_CommitPlanFile(t *testing.T) {
	t.Run("commits plan file in worktree", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		log := &mockLogger{}
		svc, err := NewService(dir, log)
		require.NoError(t, err)

		// create plan file
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "commit-test.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Commit Test Plan"), 0o600))

		// create worktree (plan is copied in)
		wtPath, planNeedsCommit, err := svc.CreateWorktreeForPlan(planFile, "master")
		require.NoError(t, err)
		assert.True(t, planNeedsCommit)

		// open worktree git service and commit plan (pass main repo root for path resolution)
		wtSvc, err := NewService(wtPath, log)
		require.NoError(t, err)
		err = wtSvc.CommitPlanFile(planFile, svc.Root())
		require.NoError(t, err)

		// verify plan was committed on the feature branch, not on main
		wtBranch, err := wtSvc.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "commit-test", wtBranch)

		// main repo should still be clean (plan not committed there)
		mainHasChanges, err := svc.repo.fileHasChanges(planFile)
		require.NoError(t, err)
		assert.True(t, mainHasChanges, "plan file should still be uncommitted in main repo")

		// cleanup
		require.NoError(t, svc.RemoveWorktree(wtPath))
	})
}

func TestService_RemoveWorktree(t *testing.T) {
	t.Run("removes existing worktree", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		log := &mockLogger{}
		svc, err := NewService(dir, log)
		require.NoError(t, err)

		// create plan and worktree
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "rm-test.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

		wtPath, planNeedsCommit, err := svc.CreateWorktreeForPlan(planFile, "master")
		require.NoError(t, err)
		assert.True(t, planNeedsCommit)

		log.logs = nil // reset logs
		err = svc.RemoveWorktree(wtPath)
		require.NoError(t, err)

		// verify worktree removed
		_, err = os.Stat(wtPath)
		assert.True(t, os.IsNotExist(err))
		assert.Contains(t, log.logs[0], "removed worktree")
	})

	t.Run("no-op when path does not exist", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		log := &mockLogger{}
		svc, err := NewService(dir, log)
		require.NoError(t, err)

		err = svc.RemoveWorktree("/nonexistent/path")
		require.NoError(t, err)
		assert.Empty(t, log.logs) // nothing should be logged
	})

	t.Run("preserves branch after removal", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		// create worktree
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "preserve-branch.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

		wtPath, planNeedsCommit, err := svc.CreateWorktreeForPlan(planFile, "master")
		require.NoError(t, err)
		assert.True(t, planNeedsCommit)

		// remove worktree
		err = svc.RemoveWorktree(wtPath)
		require.NoError(t, err)

		// branch should still exist
		assert.True(t, svc.repo.branchExists("preserve-branch"))
	})
}

func TestService_FileHasChanges(t *testing.T) {
	t.Run("returns true for dirty file", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, &mockLogger{})
		require.NoError(t, err)

		require.NoError(t, os.WriteFile(filepath.Join(dir, "dirty.txt"), []byte("data"), 0o600))
		changed, err := svc.FileHasChanges("dirty.txt")
		require.NoError(t, err)
		assert.True(t, changed)
	})

	t.Run("returns false for clean file", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, &mockLogger{})
		require.NoError(t, err)

		// README.md was committed in setupExternalTestRepo
		changed, err := svc.FileHasChanges("README.md")
		require.NoError(t, err)
		assert.False(t, changed)
	})

	t.Run("returns false for nonexistent file", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, &mockLogger{})
		require.NoError(t, err)

		changed, err := svc.FileHasChanges("nonexistent.txt")
		require.NoError(t, err)
		assert.False(t, changed)
	})
}

func TestService_formatDirtyFiles(t *testing.T) {
	svc := &Service{}

	t.Run("single file", func(t *testing.T) {
		result := svc.formatDirtyFiles([]string{"file.txt"})
		assert.Equal(t, "  file.txt", result)
	})

	t.Run("few files", func(t *testing.T) {
		result := svc.formatDirtyFiles([]string{"a.txt", "b.txt", "c.txt"})
		assert.Equal(t, "  a.txt\n  b.txt\n  c.txt", result)
	})

	t.Run("exactly 10 files", func(t *testing.T) {
		files := make([]string, 10)
		for i := range files {
			files[i] = fmt.Sprintf("file%d.txt", i)
		}
		result := svc.formatDirtyFiles(files)
		assert.NotContains(t, result, "more")
		assert.Contains(t, result, "file9.txt")
	})

	t.Run("11 files truncates with and-more suffix", func(t *testing.T) {
		files := make([]string, 11)
		for i := range files {
			files[i] = fmt.Sprintf("file%d.txt", i)
		}
		result := svc.formatDirtyFiles(files)
		assert.Contains(t, result, "file9.txt")
		assert.NotContains(t, result, "file10.txt")
		assert.Contains(t, result, "... and 1 more")
	})

	t.Run("15 files shows 10 plus count", func(t *testing.T) {
		files := make([]string, 15)
		for i := range files {
			files[i] = fmt.Sprintf("file%d.txt", i)
		}
		result := svc.formatDirtyFiles(files)
		assert.Contains(t, result, "... and 5 more")
	})

	t.Run("empty input", func(t *testing.T) {
		assert.Empty(t, svc.formatDirtyFiles(nil))
		assert.Empty(t, svc.formatDirtyFiles([]string{}))
	})
}

func TestService_SetCommitTrailer(t *testing.T) {
	t.Run("stores trailer value", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)
		svc.SetCommitTrailer("Co-authored-by: test <test@example.com>")
		assert.Equal(t, "Co-authored-by: test <test@example.com>", svc.trailer)
	})

	t.Run("clears trailer with empty string", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)
		svc.SetCommitTrailer("something")
		svc.SetCommitTrailer("")
		assert.Empty(t, svc.trailer)
	})
}

func TestService_appendTrailer(t *testing.T) {
	svc := &Service{}

	t.Run("returns message unchanged when trailer is empty", func(t *testing.T) {
		assert.Equal(t, "some commit msg", svc.appendTrailer("some commit msg"))
	})

	t.Run("appends trailer with blank line", func(t *testing.T) {
		svc.trailer = "Co-authored-by: bot <bot@example.com>"
		result := svc.appendTrailer("feat: add feature")
		assert.Equal(t, "feat: add feature\n\nCo-authored-by: bot <bot@example.com>", result)
	})

	t.Run("appends trailer to multi-line message", func(t *testing.T) {
		svc.trailer = "Signed-off-by: user"
		result := svc.appendTrailer("fix: bug\n\ndetailed description")
		assert.Equal(t, "fix: bug\n\ndetailed description\n\nSigned-off-by: user", result)
	})
}

func TestService_CommitWithTrailer(t *testing.T) {
	t.Run("trailer appears in commit log", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)
		svc.SetCommitTrailer("Co-authored-by: ralphex <noreply@ralphex.com>")

		// create and commit a plan file via CommitPlanFile
		plansDir := filepath.Join(svc.Root(), "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "trailer-test.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

		err = svc.CommitPlanFile(planFile, svc.Root())
		require.NoError(t, err)

		// verify trailer in commit message
		out := runGit(t, svc.Root(), "log", "-1", "--format=%B")
		assert.Contains(t, out, "add plan: trailer-test")
		assert.Contains(t, out, "Co-authored-by: ralphex <noreply@ralphex.com>")
	})

	t.Run("no trailer when not configured", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)
		// no SetCommitTrailer call

		plansDir := filepath.Join(svc.Root(), "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "no-trailer.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

		err = svc.CommitPlanFile(planFile, svc.Root())
		require.NoError(t, err)

		out := runGit(t, svc.Root(), "log", "-1", "--format=%B")
		assert.Contains(t, out, "add plan: no-trailer")
		assert.NotContains(t, out, "Co-authored-by")
	})

	t.Run("trailer in CreateBranchForPlan", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)
		svc.SetCommitTrailer("Co-authored-by: ralphex <noreply@ralphex.com>")

		// create an untracked plan file so CreateBranchForPlan auto-commits it
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "branch-trailer.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

		err = svc.CreateBranchForPlan(planFile, "master")
		require.NoError(t, err)

		out := runGit(t, dir, "log", "-1", "--format=%B")
		assert.Contains(t, out, "add plan: branch-trailer")
		assert.Contains(t, out, "Co-authored-by: ralphex <noreply@ralphex.com>")
	})

	t.Run("trailer in MovePlanToCompleted", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)
		svc.SetCommitTrailer("Signed-off-by: test")

		// create and commit a plan file first
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "move-trailer.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))
		require.NoError(t, svc.repo.add(planFile))
		require.NoError(t, svc.repo.commit("add plan"))

		err = svc.MovePlanToCompleted(planFile)
		require.NoError(t, err)

		out := runGit(t, dir, "log", "-1", "--format=%B")
		assert.Contains(t, out, "move completed plan: move-trailer.md")
		assert.Contains(t, out, "Signed-off-by: test")
	})
}
