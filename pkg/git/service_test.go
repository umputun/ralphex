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
}

func TestService_IsMainBranch(t *testing.T) {
	t.Run("returns true for master branch", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		isMain, err := svc.IsMainBranch()
		require.NoError(t, err)
		assert.True(t, isMain)
	})

	t.Run("returns true for main branch", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		err = svc.CreateBranch("main")
		require.NoError(t, err)

		isMain, err := svc.IsMainBranch()
		require.NoError(t, err)
		assert.True(t, isMain)
	})

	t.Run("returns false for feature branch", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		err = svc.CreateBranch("feature-test")
		require.NoError(t, err)

		isMain, err := svc.IsMainBranch()
		require.NoError(t, err)
		assert.False(t, isMain)
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

		isMain, err := svc.IsMainBranch()
		require.NoError(t, err)
		assert.False(t, isMain)
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
		dir := setupExternalTestRepo(t)
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

		err = svc.CreateBranchForPlan(planFile)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "worktree has uncommitted changes")
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

		err = svc.CreateBranchForPlan(planFile)
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

		err = svc.CreateBranchForPlan(planFile)
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

func TestService_EnsureIgnored(t *testing.T) {
	t.Run("adds pattern to gitignore", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		log := &mockLogger{}
		svc, err := NewService(dir, log)
		require.NoError(t, err)

		err = svc.EnsureIgnored(".ralphex/progress/", ".ralphex/progress/progress-test.txt")
		require.NoError(t, err)
		assert.Len(t, log.logs, 1)
		assert.Contains(t, log.logs[0], ".ralphex/progress/", "log message should contain pattern")

		// verify pattern was added to .gitignore
		gitignorePath := filepath.Join(dir, ".gitignore")
		content, err := os.ReadFile(gitignorePath) //nolint:gosec // test file
		require.NoError(t, err)
		assert.Contains(t, string(content), ".ralphex/progress/")
	})

	t.Run("does nothing if already ignored", func(t *testing.T) {
		dir := setupExternalTestRepo(t)

		// create gitignore with pattern
		gitignorePath := filepath.Join(dir, ".gitignore")
		err := os.WriteFile(gitignorePath, []byte(".ralphex/progress/\n"), 0o600)
		require.NoError(t, err)

		log := &mockLogger{}
		svc, err := NewService(dir, log)
		require.NoError(t, err)

		// create probe path so git can check it
		require.NoError(t, os.MkdirAll(filepath.Join(dir, ".ralphex", "progress"), 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".ralphex", "progress", "progress-test.txt"), []byte("test"), 0o600))

		err = svc.EnsureIgnored(".ralphex/progress/", ".ralphex/progress/progress-test.txt")
		require.NoError(t, err)
		assert.Empty(t, log.logs, "log should not be called if already ignored")

		// verify gitignore wasn't modified (no duplicate pattern)
		content, err := os.ReadFile(gitignorePath) //nolint:gosec // test file
		require.NoError(t, err)
		assert.Equal(t, ".ralphex/progress/\n", string(content))
	})

	t.Run("creates gitignore if missing", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
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
		dir := setupExternalTestRepo(t)
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

		wtPath, planNeedsCommit, err := svc.CreateWorktreeForPlan(planFile)
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

		wtPath, planNeedsCommit, err := svc.CreateWorktreeForPlan(planFile)
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

	t.Run("fails when not on main/master", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		svc, err := NewService(dir, noopServiceLogger())
		require.NoError(t, err)

		// switch to feature branch
		require.NoError(t, svc.CreateBranch("feature"))

		planFile := filepath.Join(dir, "docs", "plans", "feature.md")
		_, _, err = svc.CreateWorktreeForPlan(planFile)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "requires main/master branch")
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
		wtPath, planNeedsCommit, err := svc.CreateWorktreeForPlan(planFile)
		require.NoError(t, err)
		assert.True(t, planNeedsCommit, "untracked plan file should need commit")

		// switch back to master for second attempt
		require.NoError(t, svc.repo.checkoutBranch("master"))

		// second attempt should fail
		_, _, err = svc.CreateWorktreeForPlan(planFile)
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

		wtPath, planNeedsCommit, err := svc.CreateWorktreeForPlan(planFile)
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

		wtPath, planNeedsCommit, err := svc.CreateWorktreeForPlan(planFile)
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

		wtPath, planNeedsCommit, err := svc.CreateWorktreeForPlan(planFile)
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

		wtPath, planNeedsCommit, err := svc.CreateWorktreeForPlan(planFile)
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
		wtPath, planNeedsCommit, err := svc.CreateWorktreeForPlan(planFile)
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

		wtPath, planNeedsCommit, err := svc.CreateWorktreeForPlan(planFile)
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

		wtPath, planNeedsCommit, err := svc.CreateWorktreeForPlan(planFile)
		require.NoError(t, err)
		assert.True(t, planNeedsCommit)

		// remove worktree
		err = svc.RemoveWorktree(wtPath)
		require.NoError(t, err)

		// branch should still exist
		assert.True(t, svc.repo.branchExists("preserve-branch"))
	})
}

func TestService_CommitIgnoreChanges(t *testing.T) {
	t.Run("commits dirty gitignore", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		log := &mockLogger{}
		svc, err := NewService(dir, log)
		require.NoError(t, err)

		// add a pattern to .gitignore (makes it dirty)
		err = svc.EnsureIgnored(".ralphex/progress/", ".ralphex/progress/progress-test.txt")
		require.NoError(t, err)

		// verify .gitignore is dirty
		changed, err := svc.repo.fileHasChanges(".gitignore")
		require.NoError(t, err)
		assert.True(t, changed)

		// commit the changes
		err = svc.CommitIgnoreChanges()
		require.NoError(t, err)

		// verify .gitignore is clean
		changed, err = svc.repo.fileHasChanges(".gitignore")
		require.NoError(t, err)
		assert.False(t, changed)

		assert.GreaterOrEqual(t, len(log.logs), 2, "should have log for EnsureIgnored and CommitIgnoreChanges")
	})

	t.Run("no-op when gitignore is clean", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		log := &mockLogger{}
		svc, err := NewService(dir, log)
		require.NoError(t, err)

		err = svc.CommitIgnoreChanges()
		require.NoError(t, err)
		assert.Empty(t, log.logs, "should not log when nothing to commit")
	})

	t.Run("no-op when gitignore does not exist", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		// ensure no .gitignore exists
		_ = os.Remove(filepath.Join(dir, ".gitignore"))

		log := &mockLogger{}
		svc, err := NewService(dir, log)
		require.NoError(t, err)

		err = svc.CommitIgnoreChanges()
		require.NoError(t, err)
		assert.Empty(t, log.logs)
	})

	t.Run("does not commit pre-staged files", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		log := &mockLogger{}
		svc, err := NewService(dir, log)
		require.NoError(t, err)

		// create and stage an unrelated file
		require.NoError(t, os.WriteFile(filepath.Join(dir, "other.txt"), []byte("data"), 0o600))
		require.NoError(t, svc.repo.add("other.txt"))

		// make .gitignore dirty
		err = svc.EnsureIgnored(".ralphex/progress/", ".ralphex/progress/progress-test.txt")
		require.NoError(t, err)

		// commit should only commit .gitignore, not other.txt
		err = svc.CommitIgnoreChanges()
		require.NoError(t, err)

		// other.txt should still have staged changes (not committed)
		changed, err := svc.repo.fileHasChanges("other.txt")
		require.NoError(t, err)
		assert.True(t, changed, "other.txt should still be staged/dirty, not committed")
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
