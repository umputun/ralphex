package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/git"
	"github.com/umputun/ralphex/pkg/progress"
)

// testColors returns a Colors instance for testing.
func testColors() *progress.Colors {
	return progress.NewColors(progress.ColorConfig{
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

func TestSelectPlan(t *testing.T) {
	colors := testColors()

	t.Run("returns provided plan file if exists", func(t *testing.T) {
		tmpDir := t.TempDir()
		planFile := filepath.Join(tmpDir, "test-plan.md")
		err := os.WriteFile(planFile, []byte("# Test Plan"), 0o600)
		require.NoError(t, err)

		result, err := selectPlan(context.Background(), planFile, false, tmpDir, colors)
		require.NoError(t, err)
		assert.Equal(t, planFile, result)
	})

	t.Run("returns error if plan file not found", func(t *testing.T) {
		_, err := selectPlan(context.Background(), "/nonexistent/plan.md", false, "", colors)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "plan file not found")
	})

	t.Run("returns empty string for optional mode with no plan", func(t *testing.T) {
		result, err := selectPlan(context.Background(), "", true, "", colors)
		require.NoError(t, err)
		assert.Empty(t, result)
	})
}

func TestSelectPlanWithFzf(t *testing.T) {
	colors := testColors()

	t.Run("returns error if plans directory not found", func(t *testing.T) {
		_, err := selectPlanWithFzf(context.Background(), "/nonexistent/plans", colors)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "plans directory not found")
	})

	t.Run("auto-selects single plan file", func(t *testing.T) {
		tmpDir := t.TempDir()
		planFile := filepath.Join(tmpDir, "single-plan.md")
		err := os.WriteFile(planFile, []byte("# Single Plan"), 0o600)
		require.NoError(t, err)

		result, err := selectPlanWithFzf(context.Background(), tmpDir, colors)
		require.NoError(t, err)
		assert.Equal(t, planFile, result)
	})
}

func TestCheckDependencies(t *testing.T) {
	t.Run("returns nil for existing dependencies", func(t *testing.T) {
		err := checkDependencies("ls") // ls should exist on all unix systems
		require.NoError(t, err)
	})

	t.Run("returns error for missing dependency", func(t *testing.T) {
		err := checkDependencies("nonexistent-command-12345")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found in PATH")
	})
}

func TestCreateBranchIfNeeded(t *testing.T) {
	colors := testColors()

	t.Run("on_feature_branch_does_nothing", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := git.Open(dir)
		require.NoError(t, err)

		// create and switch to feature branch
		err = repo.CreateBranch("feature-test")
		require.NoError(t, err)

		// should return nil without creating new branch
		err = createBranchIfNeeded(repo, "docs/plans/some-plan.md", colors)
		require.NoError(t, err)

		// verify still on feature-test
		branch, err := repo.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "feature-test", branch)
	})

	t.Run("on_master_creates_branch", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := git.Open(dir)
		require.NoError(t, err)

		// verify on master
		branch, err := repo.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "master", branch)

		// should create branch from plan filename
		err = createBranchIfNeeded(repo, "docs/plans/add-feature.md", colors)
		require.NoError(t, err)

		// verify switched to new branch
		branch, err = repo.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "add-feature", branch)
	})

	t.Run("switches_to_existing_branch", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := git.Open(dir)
		require.NoError(t, err)

		// create branch first
		err = repo.CreateBranch("existing-feature")
		require.NoError(t, err)

		// switch back to master
		err = repo.CheckoutBranch("master")
		require.NoError(t, err)

		// should switch to existing branch without error
		err = createBranchIfNeeded(repo, "docs/plans/existing-feature.md", colors)
		require.NoError(t, err)

		branch, err := repo.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "existing-feature", branch)
	})

	t.Run("strips_date_prefix", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := git.Open(dir)
		require.NoError(t, err)

		// plan file with date prefix
		err = createBranchIfNeeded(repo, "docs/plans/2024-01-15-feature.md", colors)
		require.NoError(t, err)

		branch, err := repo.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "feature", branch)
	})

	t.Run("handles_plain_filename", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := git.Open(dir)
		require.NoError(t, err)

		err = createBranchIfNeeded(repo, "add-tests.md", colors)
		require.NoError(t, err)

		branch, err := repo.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "add-tests", branch)
	})

	t.Run("handles_numeric_only_prefix", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := git.Open(dir)
		require.NoError(t, err)

		// edge case: plan with complex date prefix
		err = createBranchIfNeeded(repo, "docs/plans/2024-01-15-12-30-my-feature.md", colors)
		require.NoError(t, err)

		branch, err := repo.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "my-feature", branch)
	})
}

func TestMovePlanToCompleted(t *testing.T) {
	colors := testColors()

	t.Run("moves_tracked_file_and_commits", func(t *testing.T) {
		dir := setupTestRepo(t)

		// change to test repo dir (movePlanToCompleted uses relative paths)
		origDir, err := os.Getwd()
		require.NoError(t, err)
		err = os.Chdir(dir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		repo, err := git.Open(".")
		require.NoError(t, err)

		// create plans directory and plan file
		err = os.MkdirAll(filepath.Join("docs", "plans"), 0o750)
		require.NoError(t, err)

		planFile := filepath.Join("docs", "plans", "test-plan.md")
		err = os.WriteFile(planFile, []byte("# Test Plan\n"), 0o600)
		require.NoError(t, err)

		// stage and commit the plan
		err = repo.Add(planFile)
		require.NoError(t, err)
		err = repo.Commit("add test plan")
		require.NoError(t, err)

		// move plan to completed
		err = movePlanToCompleted(repo, planFile, colors)
		require.NoError(t, err)

		// verify old file removed
		_, err = os.Stat(planFile)
		assert.True(t, os.IsNotExist(err))

		// verify new file exists
		completedFile := filepath.Join("docs", "plans", "completed", "test-plan.md")
		_, err = os.Stat(completedFile)
		require.NoError(t, err)
	})

	t.Run("creates_completed_directory", func(t *testing.T) {
		dir := setupTestRepo(t)

		// change to test repo dir
		origDir, err := os.Getwd()
		require.NoError(t, err)
		err = os.Chdir(dir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		repo, err := git.Open(".")
		require.NoError(t, err)

		// create plans directory without completed subdir
		err = os.MkdirAll(filepath.Join("docs", "plans"), 0o750)
		require.NoError(t, err)

		planFile := filepath.Join("docs", "plans", "new-plan.md")
		err = os.WriteFile(planFile, []byte("# New Plan\n"), 0o600)
		require.NoError(t, err)

		// stage and commit
		err = repo.Add(planFile)
		require.NoError(t, err)
		err = repo.Commit("add new plan")
		require.NoError(t, err)

		// verify completed dir doesn't exist
		completedDir := filepath.Join("docs", "plans", "completed")
		_, err = os.Stat(completedDir)
		assert.True(t, os.IsNotExist(err))

		// move plan
		err = movePlanToCompleted(repo, planFile, colors)
		require.NoError(t, err)

		// verify completed directory was created
		info, err := os.Stat(completedDir)
		require.NoError(t, err)
		assert.True(t, info.IsDir())
	})

	t.Run("moves_untracked_file", func(t *testing.T) {
		dir := setupTestRepo(t)

		// change to test repo dir
		origDir, err := os.Getwd()
		require.NoError(t, err)
		err = os.Chdir(dir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		repo, err := git.Open(".")
		require.NoError(t, err)

		// create plans directory and untracked plan file
		err = os.MkdirAll(filepath.Join("docs", "plans"), 0o750)
		require.NoError(t, err)

		planFile := filepath.Join("docs", "plans", "untracked-plan.md")
		err = os.WriteFile(planFile, []byte("# Untracked Plan\n"), 0o600)
		require.NoError(t, err)

		// don't stage the file, just move it
		err = movePlanToCompleted(repo, planFile, colors)
		require.NoError(t, err)

		// verify old file removed
		_, err = os.Stat(planFile)
		assert.True(t, os.IsNotExist(err))

		// verify new file exists
		completedFile := filepath.Join("docs", "plans", "completed", "untracked-plan.md")
		_, err = os.Stat(completedFile)
		require.NoError(t, err)
	})

	t.Run("moves_file_with_absolute_path", func(t *testing.T) {
		dir := setupTestRepo(t)

		// resolve symlinks for consistent paths (macOS /var -> /private/var)
		dir, err := filepath.EvalSymlinks(dir)
		require.NoError(t, err)

		// change to test repo dir
		origDir, err := os.Getwd()
		require.NoError(t, err)
		err = os.Chdir(dir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		repo, err := git.Open(".")
		require.NoError(t, err)

		// create plans directory and plan file
		err = os.MkdirAll(filepath.Join("docs", "plans"), 0o750)
		require.NoError(t, err)

		planFile := filepath.Join(dir, "docs", "plans", "abs-plan.md")
		err = os.WriteFile(planFile, []byte("# Absolute Path Plan\n"), 0o600)
		require.NoError(t, err)

		// stage and commit
		err = repo.Add(planFile)
		require.NoError(t, err)
		err = repo.Commit("add abs plan")
		require.NoError(t, err)

		// move using absolute path (simulates normalized path from run())
		err = movePlanToCompleted(repo, planFile, colors)
		require.NoError(t, err)

		// verify old file removed
		_, err = os.Stat(planFile)
		assert.True(t, os.IsNotExist(err))

		// verify new file exists
		completedFile := filepath.Join(dir, "docs", "plans", "completed", "abs-plan.md")
		_, err = os.Stat(completedFile)
		require.NoError(t, err)
	})
}

func TestEnsureGitignore(t *testing.T) {
	colors := testColors()

	t.Run("adds_pattern_when_not_ignored", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := git.Open(dir)
		require.NoError(t, err)

		// save original working directory
		origDir, err := os.Getwd()
		require.NoError(t, err)

		// change to test repo dir (ensureGitignore uses relative .gitignore path)
		err = os.Chdir(dir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		// ensure gitignore
		err = ensureGitignore(repo, colors)
		require.NoError(t, err)

		// verify .gitignore was created with the pattern
		content, err := os.ReadFile(filepath.Join(dir, ".gitignore")) //nolint:gosec // test file in temp dir
		require.NoError(t, err)
		assert.Contains(t, string(content), "progress-*.txt")
	})

	t.Run("skips_when_already_ignored", func(t *testing.T) {
		dir := setupTestRepo(t)

		// create gitignore with pattern already present
		gitignore := filepath.Join(dir, ".gitignore")
		err := os.WriteFile(gitignore, []byte("progress-*.txt\n"), 0o600)
		require.NoError(t, err)

		repo, err := git.Open(dir)
		require.NoError(t, err)

		// save original working directory
		origDir, err := os.Getwd()
		require.NoError(t, err)

		err = os.Chdir(dir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		// ensure gitignore - should be a no-op
		err = ensureGitignore(repo, colors)
		require.NoError(t, err)

		// verify content unchanged (no duplicate pattern)
		content, err := os.ReadFile(gitignore) //nolint:gosec // test file in temp dir
		require.NoError(t, err)
		assert.Equal(t, "progress-*.txt\n", string(content))
	})

	t.Run("creates_gitignore_if_missing", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := git.Open(dir)
		require.NoError(t, err)

		// verify no .gitignore exists
		gitignore := filepath.Join(dir, ".gitignore")
		_, err = os.Stat(gitignore)
		assert.True(t, os.IsNotExist(err))

		// save original working directory
		origDir, err := os.Getwd()
		require.NoError(t, err)

		err = os.Chdir(dir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		// ensure gitignore
		err = ensureGitignore(repo, colors)
		require.NoError(t, err)

		// verify .gitignore was created
		_, err = os.Stat(gitignore)
		require.NoError(t, err)

		// verify content
		content, err := os.ReadFile(gitignore) //nolint:gosec // test file in temp dir
		require.NoError(t, err)
		assert.Contains(t, string(content), "progress-*.txt")
	})
}

// setupTestRepo creates a test git repository with an initial commit.
func setupTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// init repo
	repo, err := gogit.PlainInit(dir, false)
	require.NoError(t, err)

	// create a file
	readme := filepath.Join(dir, "README.md")
	err = os.WriteFile(readme, []byte("# Test\n"), 0o600)
	require.NoError(t, err)

	// stage and commit
	wt, err := repo.Worktree()
	require.NoError(t, err)

	_, err = wt.Add("README.md")
	require.NoError(t, err)

	_, err = wt.Commit("initial commit", &gogit.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "test@test.com"},
	})
	require.NoError(t, err)

	return dir
}
