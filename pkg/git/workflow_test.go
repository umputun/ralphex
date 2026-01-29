package git

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testLogger returns a logger function and a slice to capture log messages.
func testLogger() (func(string, ...any) (int, error), *[]string) {
	var logs []string
	return func(format string, args ...any) (int, error) {
		logs = append(logs, format)
		return 0, nil
	}, &logs
}

// noopLogger returns a no-op logger.
func noopLogger() func(string, ...any) (int, error) {
	return func(string, ...any) (int, error) { return 0, nil }
}

func TestNewWorkflow(t *testing.T) {
	dir := setupTestRepo(t)
	repo, err := Open(dir)
	require.NoError(t, err)

	wf := NewWorkflow(repo, noopLogger())
	assert.NotNil(t, wf)
	assert.Equal(t, repo, wf.Repo())
}

func TestWorkflow_CreateBranchForPlan(t *testing.T) {
	t.Run("returns nil on feature branch", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		// create and switch to feature branch
		err = repo.CreateBranch("feature-test")
		require.NoError(t, err)

		log, logs := testLogger()
		wf := NewWorkflow(repo, log)

		err = wf.CreateBranchForPlan(filepath.Join(dir, "docs", "plans", "feature.md"))
		require.NoError(t, err)

		// should not have logged anything (no branch created)
		assert.Empty(t, *logs)

		// should still be on feature-test
		branch, err := repo.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "feature-test", branch)
	})

	t.Run("creates branch from plan file name", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		log, logs := testLogger()
		wf := NewWorkflow(repo, log)

		// create plan file
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "add-feature.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

		err = wf.CreateBranchForPlan(planFile)
		require.NoError(t, err)

		// should have created branch
		branch, err := repo.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "add-feature", branch)

		// should have logged creation
		assert.Len(t, *logs, 2) // creating branch + committing plan
	})

	t.Run("switches to existing branch", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		// create the branch first but stay on master
		err = repo.CreateBranch("existing-feature")
		require.NoError(t, err)
		err = repo.CheckoutBranch("master")
		require.NoError(t, err)

		log, logs := testLogger()
		wf := NewWorkflow(repo, log)

		// create plan file with matching name
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "existing-feature.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

		err = wf.CreateBranchForPlan(planFile)
		require.NoError(t, err)

		// should have switched to existing branch
		branch, err := repo.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "existing-feature", branch)

		// first log should mention "switching"
		assert.Contains(t, (*logs)[0], "switching")
	})

	t.Run("fails with other uncommitted changes", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		wf := NewWorkflow(repo, noopLogger())

		// create plan file
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "feature.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

		// create another uncommitted file
		otherFile := filepath.Join(dir, "other.txt")
		require.NoError(t, os.WriteFile(otherFile, []byte("other content"), 0o600))

		err = wf.CreateBranchForPlan(planFile)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "worktree has uncommitted changes")
	})

	t.Run("auto-commits plan file if only dirty file", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		log, logs := testLogger()
		wf := NewWorkflow(repo, log)

		// create untracked plan file (the only dirty file)
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "new-feature.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# New Feature Plan"), 0o600))

		err = wf.CreateBranchForPlan(planFile)
		require.NoError(t, err)

		// should have created branch and committed plan
		assert.Len(t, *logs, 2)
		assert.Contains(t, (*logs)[1], "committing plan file")

		// verify plan was committed
		hasChanges, err := repo.FileHasChanges(planFile)
		require.NoError(t, err)
		assert.False(t, hasChanges, "plan file should be committed")
	})

	t.Run("does not commit if plan already committed", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		// create and commit plan file while on master
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "committed-feature.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))
		require.NoError(t, repo.Add(planFile))
		require.NoError(t, repo.Commit("add plan"))

		log, logs := testLogger()
		wf := NewWorkflow(repo, log)

		err = wf.CreateBranchForPlan(planFile)
		require.NoError(t, err)

		// should only have one log (creating branch, no committing)
		assert.Len(t, *logs, 1)
		assert.Contains(t, (*logs)[0], "creating branch")
	})

	t.Run("strips date prefix from branch name", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		wf := NewWorkflow(repo, noopLogger())

		// create plan file with date prefix
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "2024-01-15-add-auth.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

		err = wf.CreateBranchForPlan(planFile)
		require.NoError(t, err)

		// branch name should not have date prefix
		branch, err := repo.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "add-auth", branch)
	})
}

func TestWorkflow_MovePlanToCompleted(t *testing.T) {
	t.Run("moves tracked file", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		// create and commit plan file
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "feature.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))
		require.NoError(t, repo.Add(planFile))
		require.NoError(t, repo.Commit("add plan"))

		log, logs := testLogger()
		wf := NewWorkflow(repo, log)

		err = wf.MovePlanToCompleted(planFile)
		require.NoError(t, err)

		// original file should not exist
		_, err = os.Stat(planFile)
		assert.True(t, os.IsNotExist(err))

		// completed file should exist
		completedPath := filepath.Join(plansDir, "completed", "feature.md")
		_, err = os.Stat(completedPath)
		require.NoError(t, err)

		// should have logged the move
		assert.Len(t, *logs, 1)
		assert.Contains(t, (*logs)[0], "moved plan")
	})

	t.Run("moves untracked file", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		// create untracked plan file
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "untracked-feature.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

		wf := NewWorkflow(repo, noopLogger())

		err = wf.MovePlanToCompleted(planFile)
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
		repo, err := Open(dir)
		require.NoError(t, err)

		// create plan file
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "feature.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))
		require.NoError(t, repo.Add(planFile))
		require.NoError(t, repo.Commit("add plan"))

		// verify completed dir doesn't exist
		completedDir := filepath.Join(plansDir, "completed")
		_, err = os.Stat(completedDir)
		require.True(t, os.IsNotExist(err))

		wf := NewWorkflow(repo, noopLogger())

		err = wf.MovePlanToCompleted(planFile)
		require.NoError(t, err)

		// completed dir should now exist
		info, err := os.Stat(completedDir)
		require.NoError(t, err)
		assert.True(t, info.IsDir())
	})
}

func TestWorkflow_EnsureHasCommits(t *testing.T) {
	t.Run("returns nil when repo has commits", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		wf := NewWorkflow(repo, noopLogger())
		promptCalled := false
		promptFn := func() bool {
			promptCalled = true
			return true
		}

		err = wf.EnsureHasCommits(promptFn)
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

		repo, err := Open(dir)
		require.NoError(t, err)

		wf := NewWorkflow(repo, noopLogger())
		promptCalled := false
		promptFn := func() bool {
			promptCalled = true
			return true
		}

		err = wf.EnsureHasCommits(promptFn)
		require.NoError(t, err)

		// prompt should have been called
		assert.True(t, promptCalled)

		// repo should now have commits
		hasCommits, err := repo.HasCommits()
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

		repo, err := Open(dir)
		require.NoError(t, err)

		wf := NewWorkflow(repo, noopLogger())
		promptFn := func() bool { return false }

		err = wf.EnsureHasCommits(promptFn)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no commits")
	})

	t.Run("returns error when no files to commit", func(t *testing.T) {
		// create empty repo with no files
		dir := t.TempDir()
		_, err := git.PlainInit(dir, false)
		require.NoError(t, err)

		repo, err := Open(dir)
		require.NoError(t, err)

		wf := NewWorkflow(repo, noopLogger())
		promptFn := func() bool { return true }

		err = wf.EnsureHasCommits(promptFn)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no files to commit")
	})
}
