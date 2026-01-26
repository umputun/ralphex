package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/config"
	"github.com/umputun/ralphex/pkg/git"
	"github.com/umputun/ralphex/pkg/processor"
	"github.com/umputun/ralphex/pkg/progress"
)

// testColors returns a Colors instance for testing.
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

func TestDetermineMode(t *testing.T) {
	tests := []struct {
		name     string
		opts     opts
		expected processor.Mode
	}{
		{name: "default_is_full", opts: opts{}, expected: processor.ModeFull},
		{name: "review_flag", opts: opts{Review: true}, expected: processor.ModeReview},
		{name: "codex_only_flag", opts: opts{CodexOnly: true}, expected: processor.ModeCodexOnly},
		{name: "codex_only_takes_precedence", opts: opts{Review: true, CodexOnly: true}, expected: processor.ModeCodexOnly},
		{name: "plan_flag", opts: opts{PlanDescription: "add caching"}, expected: processor.ModePlan},
		{name: "plan_takes_precedence_over_review", opts: opts{PlanDescription: "add caching", Review: true}, expected: processor.ModePlan},
		{name: "plan_takes_precedence_over_codex", opts: opts{PlanDescription: "add caching", CodexOnly: true}, expected: processor.ModePlan},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := determineMode(tc.opts)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestIsWatchOnlyMode(t *testing.T) {
	tests := []struct {
		name            string
		opts            opts
		configWatchDirs []string
		expected        bool
	}{
		{name: "serve_with_watch_and_no_plan", opts: opts{Serve: true, Watch: []string{"/tmp"}}, configWatchDirs: nil, expected: true},
		{name: "serve_with_config_watch_and_no_plan", opts: opts{Serve: true}, configWatchDirs: []string{"/home"}, expected: true},
		{name: "serve_without_watch", opts: opts{Serve: true}, configWatchDirs: nil, expected: false},
		{name: "no_serve_with_watch", opts: opts{Watch: []string{"/tmp"}}, configWatchDirs: nil, expected: false},
		{name: "serve_with_plan_file", opts: opts{Serve: true, Watch: []string{"/tmp"}, PlanFile: "plan.md"}, configWatchDirs: nil, expected: false},
		{name: "serve_with_plan_description", opts: opts{Serve: true, Watch: []string{"/tmp"}, PlanDescription: "add feature"}, configWatchDirs: nil, expected: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := isWatchOnlyMode(tc.opts, tc.configWatchDirs)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestPlanFlagConflict(t *testing.T) {
	t.Run("returns_error_when_plan_and_planfile_both_set", func(t *testing.T) {
		o := opts{
			PlanDescription: "add caching",
			PlanFile:        "docs/plans/some-plan.md",
		}
		err := run(context.Background(), o)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "--plan flag conflicts")
	})

	t.Run("no_error_when_only_plan_flag_set", func(t *testing.T) {
		// this test will fail at a later point (missing git repo etc), but not at validation
		o := opts{PlanDescription: "add caching"}
		err := run(context.Background(), o)
		// should fail at git repo check, not at validation
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "--plan flag conflicts")
	})

	t.Run("no_error_when_only_planfile_set", func(t *testing.T) {
		// this test will fail at a later point (file not found etc), but not at validation
		o := opts{PlanFile: "nonexistent-plan.md"}
		err := run(context.Background(), o)
		// should fail at git repo check, not at validation
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "--plan flag conflicts")
	})
}

func TestPlanModeIntegration(t *testing.T) {
	t.Run("plan_mode_requires_git_repo", func(t *testing.T) {
		// skip if claude not installed - this test requires claude to pass dependency check
		if _, err := exec.LookPath("claude"); err != nil {
			t.Skip("claude not installed")
		}

		// run from a non-git directory
		tmpDir := t.TempDir()
		origDir, err := os.Getwd()
		require.NoError(t, err)
		err = os.Chdir(tmpDir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		o := opts{PlanDescription: "add caching feature"}
		err = run(context.Background(), o)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no .git directory")
	})

	t.Run("plan_mode_runs_from_git_repo", func(t *testing.T) {
		// create a test git repo
		dir := setupTestRepo(t)
		origDir, err := os.Getwd()
		require.NoError(t, err)
		err = os.Chdir(dir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		// run in plan mode - will fail at claude execution but should pass validation and setup
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel immediately to stop execution

		o := opts{PlanDescription: "add caching feature", MaxIterations: 1}
		err = run(ctx, o)

		// should fail with context canceled, not validation errors
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "--plan flag conflicts")
		assert.NotContains(t, err.Error(), "no .git directory")
	})

	t.Run("plan_mode_progress_file_naming", func(t *testing.T) {
		// skip if claude not installed - this test requires claude to pass dependency check
		if _, err := exec.LookPath("claude"); err != nil {
			t.Skip("claude not installed")
		}

		// test that progress filename is generated correctly for plan mode
		// the actual file creation is tested by the integration test with real runner

		// verify progress filename function handles plan mode correctly
		// note: progressFilename is not exported, but progress.Config with PlanDescription
		// is used in runPlanMode - this test verifies the wiring is correct by checking
		// that the run() routes to runPlanMode without validation errors
		dir := setupTestRepo(t)
		origDir, err := os.Getwd()
		require.NoError(t, err)
		err = os.Chdir(dir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		// create docs/plans directory to avoid config loading errors
		require.NoError(t, os.MkdirAll("docs/plans", 0o750))

		// run with immediate cancel - should fail at executor, not validation
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		o := opts{PlanDescription: "test plan description", MaxIterations: 1}
		err = run(ctx, o)

		// error should be from plan creation (context canceled), not from config or validation
		require.Error(t, err)
		assert.Contains(t, err.Error(), "plan creation")
	})
}

func TestCheckClaudeDep(t *testing.T) {
	t.Run("uses_configured_command", func(t *testing.T) {
		cfg := &config.Config{ClaudeCommand: "nonexistent-command-12345"}
		err := checkClaudeDep(cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nonexistent-command-12345")
	})

	t.Run("falls_back_to_claude_when_empty", func(t *testing.T) {
		cfg := &config.Config{ClaudeCommand: ""}
		err := checkClaudeDep(cfg)
		// may pass or fail depending on whether claude is installed
		// but error message should reference "claude" not empty string
		if err != nil {
			assert.Contains(t, err.Error(), "claude")
		}
	})
}

func TestPreparePlanFile(t *testing.T) {
	colors := testColors()

	t.Run("returns_absolute_path", func(t *testing.T) {
		tmpDir := t.TempDir()
		planFile := filepath.Join(tmpDir, "test-plan.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Test"), 0o600))

		result, err := preparePlanFile(context.Background(), planSelector{
			PlanFile: planFile, Optional: false, PlansDir: tmpDir, Colors: colors,
		})
		require.NoError(t, err)
		assert.True(t, filepath.IsAbs(result))
	})

	t.Run("returns_error_for_missing_plan_in_task_mode", func(t *testing.T) {
		tmpDir := t.TempDir()
		_, err := preparePlanFile(context.Background(), planSelector{
			PlanFile: "", Optional: false, PlansDir: tmpDir, Colors: colors,
		})
		require.Error(t, err)
		// error comes from selectPlanWithFzf when no .md files found
		assert.Contains(t, err.Error(), "no plans found")
	})

	t.Run("returns_empty_for_review_mode_without_plan", func(t *testing.T) {
		result, err := preparePlanFile(context.Background(), planSelector{
			PlanFile: "", Optional: true, PlansDir: "", Colors: colors,
		})
		require.NoError(t, err)
		assert.Empty(t, result)
	})
}

func TestCreateRunner(t *testing.T) {
	t.Run("maps_config_correctly", func(t *testing.T) {
		cfg := &config.Config{
			IterationDelayMs: 5000,
			TaskRetryCount:   3,
			CodexEnabled:     false,
		}
		o := opts{MaxIterations: 100, Debug: true, NoColor: true}

		// create a dummy logger for the test
		colors := testColors()
		log, err := progress.NewLogger(progress.Config{PlanFile: "", Mode: "full", Branch: "test", NoColor: true}, colors)
		require.NoError(t, err)
		defer log.Close()

		runner := createRunner(cfg, o, "/path/to/plan.md", processor.ModeFull, log)
		assert.NotNil(t, runner)
	})

	t.Run("codex_only_mode_forces_codex_enabled", func(t *testing.T) {
		cfg := &config.Config{CodexEnabled: false} // explicitly disabled in config
		o := opts{MaxIterations: 50}

		colors := testColors()
		log, err := progress.NewLogger(progress.Config{PlanFile: "", Mode: "codex", Branch: "test", NoColor: true}, colors)
		require.NoError(t, err)
		defer log.Close()

		// in codex-only mode, CodexEnabled should be forced to true
		runner := createRunner(cfg, o, "", processor.ModeCodexOnly, log)
		assert.NotNil(t, runner)
		// we can't directly check runner internals, but this tests the code path runs without panic
	})
}

func TestSelectPlan(t *testing.T) {
	colors := testColors()

	t.Run("returns provided plan file if exists", func(t *testing.T) {
		tmpDir := t.TempDir()
		planFile := filepath.Join(tmpDir, "test-plan.md")
		err := os.WriteFile(planFile, []byte("# Test Plan"), 0o600)
		require.NoError(t, err)

		result, err := selectPlan(context.Background(), planSelector{
			PlanFile: planFile, Optional: false, PlansDir: tmpDir, Colors: colors,
		})
		require.NoError(t, err)
		assert.Equal(t, planFile, result)
	})

	t.Run("returns error if plan file not found", func(t *testing.T) {
		_, err := selectPlan(context.Background(), planSelector{
			PlanFile: "/nonexistent/plan.md", Optional: false, PlansDir: "", Colors: colors,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "plan file not found")
	})

	t.Run("returns empty string for optional mode with no plan", func(t *testing.T) {
		result, err := selectPlan(context.Background(), planSelector{
			PlanFile: "", Optional: true, PlansDir: "", Colors: colors,
		})
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

func TestExtractBranchName(t *testing.T) {
	tests := []struct {
		name     string
		planFile string
		expected string
	}{
		{name: "simple_filename", planFile: "add-feature.md", expected: "add-feature"},
		{name: "with_path", planFile: "docs/plans/add-feature.md", expected: "add-feature"},
		{name: "date_prefix", planFile: "2024-01-15-feature.md", expected: "feature"},
		{name: "complex_date_prefix", planFile: "2024-01-15-12-30-my-feature.md", expected: "my-feature"},
		{name: "numeric_only_keeps_name", planFile: "12345.md", expected: "12345"},
		{name: "with_path_and_date", planFile: "docs/plans/2024-01-15-add-tests.md", expected: "add-tests"},
		{name: "trailing_dashes_trimmed", planFile: "2024---feature.md", expected: "feature"},
		{name: "all_numeric_returns_original", planFile: "2024-01-15.md", expected: "2024-01-15"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := extractBranchName(tc.planFile)
			assert.Equal(t, tc.expected, result)
		})
	}
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

	t.Run("auto_commits_plan_when_only_uncommitted_file", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := git.Open(dir)
		require.NoError(t, err)

		// create plan file as the only uncommitted file
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "auto-commit-test.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Auto Commit Test Plan\n"), 0o600))

		// should create branch and auto-commit the plan
		err = createBranchIfNeeded(repo, planFile, colors)
		require.NoError(t, err)

		// verify we're on the new branch
		branch, err := repo.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "auto-commit-test", branch)

		// verify plan was committed (worktree should be clean)
		dirty, err := repo.IsDirty()
		require.NoError(t, err)
		assert.False(t, dirty, "worktree should be clean after auto-commit")
	})

	t.Run("returns_error_with_helpful_message_when_other_files_uncommitted", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := git.Open(dir)
		require.NoError(t, err)

		// create plan file
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "error-test.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Error Test Plan\n"), 0o600))

		// create another uncommitted file
		require.NoError(t, os.WriteFile(filepath.Join(dir, "other.txt"), []byte("other content"), 0o600))

		// should return an error with helpful message
		err = createBranchIfNeeded(repo, planFile, colors)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot create branch")
		assert.Contains(t, err.Error(), "uncommitted changes")
		assert.Contains(t, err.Error(), "git stash")
		assert.Contains(t, err.Error(), "ralphex --review")
	})

	t.Run("returns_error_when_tracked_file_modified", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := git.Open(dir)
		require.NoError(t, err)

		// create plan file
		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "modified-test.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Modified Test Plan\n"), 0o600))

		// modify an existing tracked file
		require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Modified\n"), 0o600))

		// should return an error
		err = createBranchIfNeeded(repo, planFile, colors)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "uncommitted changes")
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
		assert.Contains(t, string(content), "progress*.txt")
	})

	t.Run("skips_when_already_ignored", func(t *testing.T) {
		dir := setupTestRepo(t)

		// create gitignore with pattern already present
		gitignore := filepath.Join(dir, ".gitignore")
		err := os.WriteFile(gitignore, []byte("progress*.txt\n"), 0o600)
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
		assert.Equal(t, "progress*.txt\n", string(content))
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
		assert.Contains(t, string(content), "progress*.txt")
	})
}

func TestSetupRunnerLogger(t *testing.T) {
	t.Run("returns_base_logger_when_serve_disabled", func(t *testing.T) {
		colors := testColors()
		baseLog, err := progress.NewLogger(progress.Config{
			PlanFile: "",
			Mode:     "test",
			Branch:   "test",
			NoColor:  true,
		}, colors)
		require.NoError(t, err)
		defer baseLog.Close()

		o := opts{Serve: false}
		params := webDashboardParams{
			BaseLog: baseLog,
			Port:    8080,
			Colors:  colors,
		}

		result, err := setupRunnerLogger(context.Background(), o, params)
		require.NoError(t, err)
		assert.Equal(t, baseLog, result, "should return the base logger unchanged")
	})

	t.Run("returns_broadcast_logger_when_serve_enabled", func(t *testing.T) {
		colors := testColors()
		baseLog, err := progress.NewLogger(progress.Config{
			PlanFile: "",
			Mode:     "test",
			Branch:   "test",
			NoColor:  true,
		}, colors)
		require.NoError(t, err)
		defer baseLog.Close()

		o := opts{Serve: true, Port: 0} // port 0 to let system assign available port
		params := webDashboardParams{
			BaseLog:  baseLog,
			Port:     0, // system-assigned port
			PlanFile: "",
			Branch:   "test",
			Colors:   colors,
		}

		result, err := setupRunnerLogger(t.Context(), o, params)
		require.NoError(t, err)
		// result should be different from baseLog (it's a BroadcastLogger wrapper)
		assert.NotEqual(t, baseLog, result, "should return a broadcast logger, not the base logger")
	})
}

func TestGetCurrentBranch(t *testing.T) {
	t.Run("returns_branch_name", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := git.Open(dir)
		require.NoError(t, err)

		branch := getCurrentBranch(repo)
		assert.Equal(t, "master", branch)
	})

	t.Run("returns_unknown_on_error", func(t *testing.T) {
		// create a repo but then break it by removing .git
		dir := setupTestRepo(t)
		repo, err := git.Open(dir)
		require.NoError(t, err)

		// close and remove git dir to simulate error
		require.NoError(t, os.RemoveAll(filepath.Join(dir, ".git")))

		// getCurrentBranch should return "unknown" on error
		branch := getCurrentBranch(repo)
		assert.Equal(t, "unknown", branch)
	})
}

func TestSetupGitForExecution(t *testing.T) {
	colors := testColors()

	t.Run("returns_nil_for_empty_plan_file", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := git.Open(dir)
		require.NoError(t, err)

		err = setupGitForExecution(repo, "", processor.ModeFull, colors)
		require.NoError(t, err)
	})

	t.Run("creates_branch_for_full_mode", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := git.Open(dir)
		require.NoError(t, err)

		// change to test repo dir for gitignore
		origDir, err := os.Getwd()
		require.NoError(t, err)
		err = os.Chdir(dir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		err = setupGitForExecution(repo, "docs/plans/new-feature.md", processor.ModeFull, colors)
		require.NoError(t, err)

		// verify branch was created
		branch, err := repo.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "new-feature", branch)
	})

	t.Run("skips_branch_for_review_mode", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := git.Open(dir)
		require.NoError(t, err)

		// change to test repo dir for gitignore
		origDir, err := os.Getwd()
		require.NoError(t, err)
		err = os.Chdir(dir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		err = setupGitForExecution(repo, "docs/plans/some-plan.md", processor.ModeReview, colors)
		require.NoError(t, err)

		// verify still on master (no branch created)
		branch, err := repo.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "master", branch)
	})
}

func TestHandlePostExecution(t *testing.T) {
	colors := testColors()

	t.Run("moves_plan_on_full_mode_completion", func(t *testing.T) {
		dir := setupTestRepo(t)

		// change to test repo dir
		origDir, err := os.Getwd()
		require.NoError(t, err)
		err = os.Chdir(dir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		repo, err := git.Open(".")
		require.NoError(t, err)

		// create plan file
		err = os.MkdirAll(filepath.Join("docs", "plans"), 0o750)
		require.NoError(t, err)
		planFile := filepath.Join("docs", "plans", "test-feature.md")
		err = os.WriteFile(planFile, []byte("# Test Plan\n"), 0o600)
		require.NoError(t, err)

		// stage and commit
		err = repo.Add(planFile)
		require.NoError(t, err)
		err = repo.Commit("add plan")
		require.NoError(t, err)

		// handlePostExecution should move the plan
		handlePostExecution(repo, planFile, processor.ModeFull, colors)

		// verify plan was moved
		_, err = os.Stat(planFile)
		assert.True(t, os.IsNotExist(err), "original plan should be gone")

		completedFile := filepath.Join("docs", "plans", "completed", "test-feature.md")
		_, err = os.Stat(completedFile)
		require.NoError(t, err, "plan should be in completed dir")
	})

	t.Run("skips_move_on_review_mode", func(t *testing.T) {
		dir := setupTestRepo(t)

		// change to test repo dir
		origDir, err := os.Getwd()
		require.NoError(t, err)
		err = os.Chdir(dir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		repo, err := git.Open(".")
		require.NoError(t, err)

		// create plan file
		err = os.MkdirAll(filepath.Join("docs", "plans"), 0o750)
		require.NoError(t, err)
		planFile := filepath.Join("docs", "plans", "review-test.md")
		err = os.WriteFile(planFile, []byte("# Test Plan\n"), 0o600)
		require.NoError(t, err)

		// handlePostExecution with review mode should NOT move the plan
		handlePostExecution(repo, planFile, processor.ModeReview, colors)

		// verify plan was NOT moved
		_, err = os.Stat(planFile)
		require.NoError(t, err, "plan should still exist in original location")
	})

	t.Run("skips_move_on_empty_plan_file", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := git.Open(dir)
		require.NoError(t, err)

		// handlePostExecution with empty plan should not panic
		handlePostExecution(repo, "", processor.ModeFull, colors)
		// no error means success
	})
}

func TestValidateFlags(t *testing.T) {
	tests := []struct {
		name    string
		opts    opts
		wantErr bool
		errMsg  string
	}{
		{name: "no_flags_is_valid", opts: opts{}, wantErr: false},
		{name: "plan_flag_only_is_valid", opts: opts{PlanDescription: "add feature"}, wantErr: false},
		{name: "plan_file_only_is_valid", opts: opts{PlanFile: "docs/plans/test.md"}, wantErr: false},
		{name: "both_plan_and_planfile_conflicts", opts: opts{PlanDescription: "add feature", PlanFile: "docs/plans/test.md"}, wantErr: true, errMsg: "conflicts"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateFlags(tc.opts)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestPrintStartupInfo(t *testing.T) {
	colors := testColors()

	t.Run("prints_plan_info_for_full_mode", func(t *testing.T) {
		info := startupInfo{
			PlanFile:      "/path/to/plan.md",
			Branch:        "feature-branch",
			Mode:          processor.ModeFull,
			MaxIterations: 50,
			ProgressPath:  "progress.txt",
		}
		// this doesn't return anything, just verify it doesn't panic
		printStartupInfo(info, colors)
	})

	t.Run("prints_no_plan_for_review_mode", func(t *testing.T) {
		info := startupInfo{
			PlanFile:      "",
			Branch:        "test-branch",
			Mode:          processor.ModeReview,
			MaxIterations: 50,
			ProgressPath:  "progress-review.txt",
		}
		// verify it doesn't panic with empty plan
		printStartupInfo(info, colors)
	})
}

func TestFindRecentPlan(t *testing.T) {
	t.Run("finds_recently_modified_file", func(t *testing.T) {
		dir := t.TempDir()
		startTime := time.Now()

		// create a plan file
		planFile := filepath.Join(dir, "new-plan.md")
		err := os.WriteFile(planFile, []byte("# New Plan"), 0o600)
		require.NoError(t, err)

		// explicitly set mod time to be after startTime
		futureTime := startTime.Add(time.Second)
		err = os.Chtimes(planFile, futureTime, futureTime)
		require.NoError(t, err)

		result := findRecentPlan(dir, startTime)
		assert.Equal(t, planFile, result)
	})

	t.Run("returns_empty_for_old_files", func(t *testing.T) {
		dir := t.TempDir()
		startTime := time.Now()

		// create a plan file
		planFile := filepath.Join(dir, "old-plan.md")
		err := os.WriteFile(planFile, []byte("# Old Plan"), 0o600)
		require.NoError(t, err)

		// set mod time to be before startTime
		pastTime := startTime.Add(-time.Hour)
		err = os.Chtimes(planFile, pastTime, pastTime)
		require.NoError(t, err)

		result := findRecentPlan(dir, startTime)
		assert.Empty(t, result)
	})

	t.Run("returns_most_recent_of_multiple_files", func(t *testing.T) {
		dir := t.TempDir()
		startTime := time.Now()

		// create first file with earlier mod time
		plan1 := filepath.Join(dir, "plan1.md")
		err := os.WriteFile(plan1, []byte("# Plan 1"), 0o600)
		require.NoError(t, err)
		time1 := startTime.Add(time.Second)
		err = os.Chtimes(plan1, time1, time1)
		require.NoError(t, err)

		// create second file with later mod time
		plan2 := filepath.Join(dir, "plan2.md")
		err = os.WriteFile(plan2, []byte("# Plan 2"), 0o600)
		require.NoError(t, err)
		time2 := startTime.Add(2 * time.Second)
		err = os.Chtimes(plan2, time2, time2)
		require.NoError(t, err)

		result := findRecentPlan(dir, startTime)
		assert.Equal(t, plan2, result)
	})

	t.Run("returns_empty_for_nonexistent_directory", func(t *testing.T) {
		result := findRecentPlan("/nonexistent/directory", time.Now())
		assert.Empty(t, result)
	})

	t.Run("returns_empty_for_empty_directory", func(t *testing.T) {
		dir := t.TempDir()
		result := findRecentPlan(dir, time.Now())
		assert.Empty(t, result)
	})

	t.Run("ignores_non_md_files", func(t *testing.T) {
		dir := t.TempDir()
		startTime := time.Now()

		// create non-md file with future mod time
		txtFile := filepath.Join(dir, "notes.txt")
		err := os.WriteFile(txtFile, []byte("notes"), 0o600)
		require.NoError(t, err)
		futureTime := startTime.Add(time.Second)
		err = os.Chtimes(txtFile, futureTime, futureTime)
		require.NoError(t, err)

		result := findRecentPlan(dir, startTime)
		assert.Empty(t, result)
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
