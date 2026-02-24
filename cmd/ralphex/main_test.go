package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/config"
	"github.com/umputun/ralphex/pkg/git"
	gitmocks "github.com/umputun/ralphex/pkg/git/mocks"
	"github.com/umputun/ralphex/pkg/notify"
	"github.com/umputun/ralphex/pkg/plan"
	"github.com/umputun/ralphex/pkg/processor"
	"github.com/umputun/ralphex/pkg/progress"
	"github.com/umputun/ralphex/pkg/status"
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

// skipIfClaudeNotAvailable loads config (read-only) and skips test if configured claude command is not in PATH.
// uses LoadReadOnly to avoid installing defaults to real user config directory during tests.
func skipIfClaudeNotAvailable(t *testing.T) {
	t.Helper()
	cfg, err := config.LoadReadOnly("")
	if err != nil {
		t.Skipf("failed to load config: %v", err)
	}
	claudeCmd := cfg.ClaudeCommand
	if claudeCmd == "" {
		claudeCmd = "claude"
	}
	if _, err := exec.LookPath(claudeCmd); err != nil {
		t.Skipf("%s not installed", claudeCmd)
	}
}

func TestPromptPlanDescription(t *testing.T) {
	colors := testColors()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "normal_input", input: "add user authentication\n", expected: "add user authentication"},
		{name: "input_with_whitespace", input: "  add caching  \n", expected: "add caching"},
		{name: "empty_input", input: "\n", expected: ""},
		{name: "only_whitespace", input: "   \n", expected: ""},
		{name: "multiword_description", input: "implement health check endpoint with metrics\n", expected: "implement health check endpoint with metrics"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reader := strings.NewReader(tc.input)
			result := plan.PromptDescription(context.Background(), reader, colors)
			assert.Equal(t, tc.expected, result)
		})
	}

	t.Run("eof_returns_empty", func(t *testing.T) {
		// empty reader simulates EOF (Ctrl+D)
		reader := strings.NewReader("")
		result := plan.PromptDescription(context.Background(), reader, colors)
		assert.Empty(t, result)
	})

	t.Run("context_canceled_returns_empty", func(t *testing.T) {
		// canceled context simulates Ctrl+C
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel immediately
		reader := strings.NewReader("some input\n")
		result := plan.PromptDescription(ctx, reader, colors)
		assert.Empty(t, result)
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
		{name: "external_only_flag", opts: opts{ExternalOnly: true}, expected: processor.ModeCodexOnly},
		{name: "both_external_and_codex_flags", opts: opts{ExternalOnly: true, CodexOnly: true}, expected: processor.ModeCodexOnly},
		{name: "codex_only_takes_precedence_over_review", opts: opts{Review: true, CodexOnly: true}, expected: processor.ModeCodexOnly},
		{name: "external_only_takes_precedence_over_review", opts: opts{Review: true, ExternalOnly: true}, expected: processor.ModeCodexOnly},
		{name: "tasks_only_flag", opts: opts{TasksOnly: true}, expected: processor.ModeTasksOnly},
		{name: "tasks_only_takes_precedence_over_codex", opts: opts{TasksOnly: true, CodexOnly: true}, expected: processor.ModeTasksOnly},
		{name: "tasks_only_takes_precedence_over_external", opts: opts{TasksOnly: true, ExternalOnly: true}, expected: processor.ModeTasksOnly},
		{name: "tasks_only_takes_precedence_over_review", opts: opts{TasksOnly: true, Review: true}, expected: processor.ModeTasksOnly},
		{name: "plan_flag", opts: opts{PlanDescription: "add caching"}, expected: processor.ModePlan},
		{name: "plan_takes_precedence_over_review", opts: opts{PlanDescription: "add caching", Review: true}, expected: processor.ModePlan},
		{name: "plan_takes_precedence_over_codex", opts: opts{PlanDescription: "add caching", CodexOnly: true}, expected: processor.ModePlan},
		{name: "plan_takes_precedence_over_external", opts: opts{PlanDescription: "add caching", ExternalOnly: true}, expected: processor.ModePlan},
		{name: "plan_takes_precedence_over_tasks_only", opts: opts{PlanDescription: "add caching", TasksOnly: true}, expected: processor.ModePlan},
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
		// skip if configured claude command is not installed
		skipIfClaudeNotAvailable(t)

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
		// skip if configured claude command is not installed
		skipIfClaudeNotAvailable(t)

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

func TestAutoPlanModeDetection(t *testing.T) {
	t.Run("feature_branch_with_no_plans_still_errors", func(t *testing.T) {
		// skip if configured claude command is not installed
		skipIfClaudeNotAvailable(t)

		dir := setupTestRepo(t)
		origDir, err := os.Getwd()
		require.NoError(t, err)
		err = os.Chdir(dir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		// create empty plans dir
		require.NoError(t, os.MkdirAll("docs/plans", 0o750))

		// create and switch to a feature branch
		gitSvc, err := git.NewService(".", testColors().Info())
		require.NoError(t, err)
		require.NoError(t, gitSvc.CreateBranch("feature-test"))

		// run without arguments - should error because we're on feature branch
		o := opts{MaxIterations: 1}
		err = run(context.Background(), o)
		require.Error(t, err)
		// should still get the no plans found error, not auto-plan-mode
		assert.ErrorIs(t, err, plan.ErrNoPlansFound, "should return ErrNoPlansFound on feature branch")
	})

	t.Run("review_mode_skips_auto_plan_mode", func(t *testing.T) {
		// skip if configured claude command is not installed
		skipIfClaudeNotAvailable(t)

		dir := setupTestRepo(t)
		origDir, err := os.Getwd()
		require.NoError(t, err)
		err = os.Chdir(dir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		// create empty plans dir
		require.NoError(t, os.MkdirAll("docs/plans", 0o750))

		// run in review mode with canceled context - should not trigger auto-plan-mode
		// plan is optional in review mode, so it proceeds (then fails on canceled context)
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel immediately to avoid actual execution

		o := opts{Review: true, MaxIterations: 1}
		err = run(ctx, o)
		// error should be from context cancellation or runner, not "no plans found"
		// this verifies auto-plan-mode is skipped for --review flag
		require.Error(t, err)
		assert.NotErrorIs(t, err, plan.ErrNoPlansFound, "review mode should skip auto-plan-mode")
	})

	t.Run("codex_only_mode_skips_auto_plan_mode", func(t *testing.T) {
		// skip if configured claude command is not installed
		skipIfClaudeNotAvailable(t)

		dir := setupTestRepo(t)
		origDir, err := os.Getwd()
		require.NoError(t, err)
		err = os.Chdir(dir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		// create empty plans dir
		require.NoError(t, os.MkdirAll("docs/plans", 0o750))

		// run in codex-only mode with canceled context - should not trigger auto-plan-mode
		// plan is optional in codex-only mode, so it proceeds (then fails on canceled context)
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel immediately to avoid actual execution

		o := opts{CodexOnly: true, MaxIterations: 1}
		err = run(ctx, o)
		// error should be from context cancellation or runner, not "no plans found"
		// this verifies auto-plan-mode is skipped for --codex-only flag
		require.Error(t, err)
		assert.NotErrorIs(t, err, plan.ErrNoPlansFound, "codex-only mode should skip auto-plan-mode")
	})

	t.Run("external_only_mode_skips_auto_plan_mode", func(t *testing.T) {
		// skip if configured claude command is not installed
		skipIfClaudeNotAvailable(t)

		dir := setupTestRepo(t)
		origDir, err := os.Getwd()
		require.NoError(t, err)
		err = os.Chdir(dir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		// create empty plans dir
		require.NoError(t, os.MkdirAll("docs/plans", 0o750))

		// run in external-only mode with canceled context - should not trigger auto-plan-mode
		// plan is optional in external-only mode, so it proceeds (then fails on canceled context)
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel immediately to avoid actual execution

		o := opts{ExternalOnly: true, MaxIterations: 1}
		err = run(ctx, o)
		// error should be from context cancellation or runner, not "no plans found"
		// this verifies auto-plan-mode is skipped for --external-only flag
		require.Error(t, err)
		assert.NotErrorIs(t, err, plan.ErrNoPlansFound, "external-only mode should skip auto-plan-mode")
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

func TestCreateRunner(t *testing.T) {
	t.Run("creates_runner_without_panic", func(t *testing.T) {
		tmpDir := t.TempDir()
		oldWd, wdErr := os.Getwd()
		require.NoError(t, wdErr)
		require.NoError(t, os.Chdir(tmpDir))
		t.Cleanup(func() { _ = os.Chdir(oldWd) })

		cfg := &config.Config{IterationDelayMs: 5000, TaskRetryCount: 3, CodexEnabled: false}
		o := opts{MaxIterations: 100, Debug: true, NoColor: true}

		colors := testColors()
		holder := &status.PhaseHolder{}
		log, err := progress.NewLogger(progress.Config{PlanFile: "", Mode: "full", Branch: "test", NoColor: true}, colors, holder)
		require.NoError(t, err)
		defer log.Close()

		req := executePlanRequest{PlanFile: "/path/to/plan.md", Mode: processor.ModeFull, Config: cfg, DefaultBranch: "master"}
		runner := createRunner(req, o, log, holder)
		assert.NotNil(t, runner)
	})

	t.Run("codex_only_mode_creates_runner_without_panic", func(t *testing.T) {
		tmpDir := t.TempDir()
		oldWd, wdErr := os.Getwd()
		require.NoError(t, wdErr)
		require.NoError(t, os.Chdir(tmpDir))
		t.Cleanup(func() { _ = os.Chdir(oldWd) })

		cfg := &config.Config{CodexEnabled: false} // explicitly disabled in config
		o := opts{MaxIterations: 50}

		colors := testColors()
		holder := &status.PhaseHolder{}
		log, err := progress.NewLogger(progress.Config{PlanFile: "", Mode: "codex", Branch: "test", NoColor: true}, colors, holder)
		require.NoError(t, err)
		defer log.Close()

		// tests that codex-only mode code path runs without panic
		req := executePlanRequest{Mode: processor.ModeCodexOnly, Config: cfg, DefaultBranch: "main"}
		runner := createRunner(req, o, log, holder)
		assert.NotNil(t, runner)
	})
}

func TestResolveDefaultBranch(t *testing.T) {
	tests := []struct {
		name         string
		cliRef       string
		configBranch string
		autoDetect   string
		expected     string
	}{
		{name: "cli_flag_wins", cliRef: "abc1234", configBranch: "develop", autoDetect: "main", expected: "abc1234"},
		{name: "config_when_no_flag", cliRef: "", configBranch: "develop", autoDetect: "main", expected: "develop"},
		{name: "auto_detect_when_nothing_set", cliRef: "", configBranch: "", autoDetect: "main", expected: "main"},
		{name: "cli_flag_commit_hash", cliRef: "deadbeef", configBranch: "", autoDetect: "master", expected: "deadbeef"},
		{name: "all_empty", cliRef: "", configBranch: "", autoDetect: "", expected: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := resolveDefaultBranch(tc.cliRef, tc.configBranch, tc.autoDetect)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestSkipFinalizeFlag(t *testing.T) {
	t.Run("skip_finalize_disables_in_runner", func(t *testing.T) {
		tmpDir := t.TempDir()
		oldWd, wdErr := os.Getwd()
		require.NoError(t, wdErr)
		require.NoError(t, os.Chdir(tmpDir))
		t.Cleanup(func() { _ = os.Chdir(oldWd) })

		cfg := &config.Config{FinalizeEnabled: true}
		o := opts{SkipFinalize: true, MaxIterations: 50}

		// apply the same override as run() does
		if o.SkipFinalize {
			cfg.FinalizeEnabled = false
		}

		colors := testColors()
		holder := &status.PhaseHolder{}
		log, err := progress.NewLogger(progress.Config{Mode: "full", Branch: "test", NoColor: true}, colors, holder)
		require.NoError(t, err)
		defer log.Close()

		// verify createRunner receives the overridden config
		req := executePlanRequest{Mode: processor.ModeFull, Config: cfg, DefaultBranch: "main"}
		runner := createRunner(req, o, log, holder)
		assert.NotNil(t, runner)
		assert.False(t, cfg.FinalizeEnabled, "skip-finalize should override config")
	})

	t.Run("no_skip_finalize_preserves_config", func(t *testing.T) {
		cfg := &config.Config{FinalizeEnabled: true}
		o := opts{SkipFinalize: false}
		if o.SkipFinalize {
			cfg.FinalizeEnabled = false
		}
		assert.True(t, cfg.FinalizeEnabled, "config should be preserved when skip-finalize not set")
	})
}

func TestGetCurrentBranch(t *testing.T) {
	t.Run("returns_branch_name", func(t *testing.T) {
		dir := setupTestRepo(t)
		gitSvc, err := git.NewService(dir, testColors().Info())
		require.NoError(t, err)

		branch := getCurrentBranch(gitSvc)
		assert.Equal(t, "master", branch)
	})

	t.Run("returns_unknown_on_error", func(t *testing.T) {
		// create a repo but then break it by removing .git
		dir := setupTestRepo(t)
		gitSvc, err := git.NewService(dir, testColors().Info())
		require.NoError(t, err)

		// close and remove git dir to simulate error
		require.NoError(t, os.RemoveAll(filepath.Join(dir, ".git")))

		// getCurrentBranch should return "unknown" on error
		branch := getCurrentBranch(gitSvc)
		assert.Equal(t, "unknown", branch)
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

func TestToRelPath(t *testing.T) {
	// toRelPath uses filepath.Rel with resolved symlinks, so we need real paths.
	// use t.TempDir, chdir into it, then build absolute paths using Getwd
	// (same way plan.Select uses filepath.Abs which calls Getwd).
	tmpDir := t.TempDir()
	origDir, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmpDir))
	t.Cleanup(func() { require.NoError(t, os.Chdir(origDir)) })

	// use Getwd to get the resolved cwd (same as filepath.Abs would)
	cwd, err := os.Getwd()
	require.NoError(t, err)

	t.Run("converts_absolute_to_relative", func(t *testing.T) {
		absPath := filepath.Join(cwd, "docs", "plans", "feature.md")
		result := toRelPath(absPath)
		assert.Equal(t, filepath.Join("docs", "plans", "feature.md"), result)
		assert.False(t, filepath.IsAbs(result), "path should be relative, got: %s", result)
	})

	t.Run("converts_absolute_completed_path", func(t *testing.T) {
		absPath := filepath.Join(cwd, "docs", "plans", "completed", "feature.md")
		result := toRelPath(absPath)
		assert.Equal(t, filepath.Join("docs", "plans", "completed", "feature.md"), result)
		assert.False(t, filepath.IsAbs(result), "path should be relative, got: %s", result)
	})

	t.Run("keeps_relative_path_as_is", func(t *testing.T) {
		result := toRelPath("docs/plans/feature.md")
		assert.Equal(t, "docs/plans/feature.md", result)
	})

	t.Run("handles_path_outside_cwd", func(t *testing.T) {
		result := toRelPath("/some/other/project/plan.md")
		assert.NotEmpty(t, result)
	})
}

// noopLogger returns a no-op git.Logger for tests using moq-generated mock.
func noopLogger() *gitmocks.LoggerMock {
	return &gitmocks.LoggerMock{
		PrintfFunc: func(string, ...any) (int, error) { return 0, nil },
	}
}

func TestEnsureRepoHasCommits(t *testing.T) {
	t.Run("returns nil for repo with commits", func(t *testing.T) {
		dir := setupTestRepo(t)
		gitSvc, err := git.NewService(dir, noopLogger())
		require.NoError(t, err)

		var stdout bytes.Buffer
		err = ensureRepoHasCommits(context.Background(), gitSvc, strings.NewReader(""), &stdout)
		assert.NoError(t, err)
	})

	t.Run("creates commit when user answers yes", func(t *testing.T) {
		dir := initEmptyRepo(t)

		// create a file so there's something to commit
		err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test\n"), 0o600)
		require.NoError(t, err)

		gitSvc, err := git.NewService(dir, noopLogger())
		require.NoError(t, err)

		// verify no commits before
		hasCommits, err := gitSvc.HasCommits()
		require.NoError(t, err)
		assert.False(t, hasCommits)

		var stdout bytes.Buffer
		err = ensureRepoHasCommits(context.Background(), gitSvc, strings.NewReader("y\n"), &stdout)
		require.NoError(t, err)

		// verify commit was created
		hasCommits, err = gitSvc.HasCommits()
		require.NoError(t, err)
		assert.True(t, hasCommits)

		// verify output
		assert.Contains(t, stdout.String(), "repository has no commits")
		assert.Contains(t, stdout.String(), "created initial commit")
	})

	t.Run("returns error when user answers no", func(t *testing.T) {
		dir := initEmptyRepo(t)

		gitSvc, err := git.NewService(dir, noopLogger())
		require.NoError(t, err)

		var stdout bytes.Buffer
		err = ensureRepoHasCommits(context.Background(), gitSvc, strings.NewReader("n\n"), &stdout)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no commits - please create initial commit manually")
	})

	t.Run("returns error on EOF", func(t *testing.T) {
		dir := initEmptyRepo(t)

		gitSvc, err := git.NewService(dir, noopLogger())
		require.NoError(t, err)

		var stdout bytes.Buffer
		err = ensureRepoHasCommits(context.Background(), gitSvc, strings.NewReader(""), &stdout)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no commits - please create initial commit manually")
	})

	t.Run("returns error when no files to commit", func(t *testing.T) {
		dir := initEmptyRepo(t)

		// no files created - empty repo

		gitSvc, err := git.NewService(dir, noopLogger())
		require.NoError(t, err)

		var stdout bytes.Buffer
		err = ensureRepoHasCommits(context.Background(), gitSvc, strings.NewReader("y\n"), &stdout)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "create initial commit")
	})

	t.Run("returns error when context canceled", func(t *testing.T) {
		dir := initEmptyRepo(t)

		gitSvc, err := git.NewService(dir, noopLogger())
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel immediately

		var stdout bytes.Buffer
		err = ensureRepoHasCommits(ctx, gitSvc, strings.NewReader("y\n"), &stdout)
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
	})
}

func TestTasksOnlyModeBranchCreation(t *testing.T) {
	t.Run("tasks_only_creates_branch_for_plan", func(t *testing.T) {
		skipIfClaudeNotAvailable(t)

		dir := setupTestRepo(t)
		origDir, err := os.Getwd()
		require.NoError(t, err)
		err = os.Chdir(dir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		// create plans dir and plan file, then commit them
		require.NoError(t, os.MkdirAll("docs/plans", 0o750))
		planPath := filepath.Join(dir, "docs", "plans", "test-plan.md")
		require.NoError(t, os.WriteFile(planPath, []byte("# Test Plan\n\n## Tasks\n\n- [ ] task 1\n"), 0o600))

		// commit the plan file so branch creation doesn't fail due to uncommitted changes
		runGit(t, dir, "add", "docs/plans/test-plan.md")
		runGit(t, dir, "commit", "-m", "add test plan")

		// run with tasks-only mode in background
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		done := make(chan struct{})
		go func() {
			defer close(done)
			o := opts{TasksOnly: true, PlanFile: planPath, MaxIterations: 1}
			_ = run(ctx, o)
		}()

		// verify branch was created (branch name derived from plan filename)
		require.Eventually(t, func() bool {
			gitSvc, err := git.NewService(dir, testColors().Info())
			if err != nil {
				return false
			}
			branch, err := gitSvc.CurrentBranch()
			return err == nil && branch == "test-plan"
		}, 3*time.Second, 100*time.Millisecond, "tasks-only mode should create branch for plan")

		cancel()
		<-done
	})

	t.Run("review_mode_does_not_create_branch", func(t *testing.T) {
		skipIfClaudeNotAvailable(t)

		dir := setupTestRepo(t)
		origDir, err := os.Getwd()
		require.NoError(t, err)
		err = os.Chdir(dir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		// create plans dir and plan file, then commit them
		require.NoError(t, os.MkdirAll("docs/plans", 0o750))
		planPath := filepath.Join(dir, "docs", "plans", "review-plan.md")
		require.NoError(t, os.WriteFile(planPath, []byte("# Review Plan\n"), 0o600))

		runGit(t, dir, "add", "docs/plans/review-plan.md")
		runGit(t, dir, "commit", "-m", "add review plan")

		// run with review mode in background
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		done := make(chan struct{})
		go func() {
			defer close(done)
			o := opts{Review: true, PlanFile: planPath, MaxIterations: 1}
			_ = run(ctx, o)
		}()

		// verify branch was NOT created (still on master) - wait briefly then check
		time.Sleep(500 * time.Millisecond)
		gitSvc, err := git.NewService(dir, testColors().Info())
		require.NoError(t, err)
		branch, err := gitSvc.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "master", branch, "review mode should not create branch")

		cancel()
		<-done
	})

	t.Run("codex_only_mode_does_not_create_branch", func(t *testing.T) {
		skipIfClaudeNotAvailable(t)

		dir := setupTestRepo(t)
		origDir, err := os.Getwd()
		require.NoError(t, err)
		err = os.Chdir(dir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		// create plans dir and plan file, then commit them
		require.NoError(t, os.MkdirAll("docs/plans", 0o750))
		planPath := filepath.Join(dir, "docs", "plans", "codex-plan.md")
		require.NoError(t, os.WriteFile(planPath, []byte("# Codex Plan\n"), 0o600))

		runGit(t, dir, "add", "docs/plans/codex-plan.md")
		runGit(t, dir, "commit", "-m", "add codex plan")

		// run with codex-only mode in background
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		done := make(chan struct{})
		go func() {
			defer close(done)
			o := opts{CodexOnly: true, PlanFile: planPath, MaxIterations: 1}
			_ = run(ctx, o)
		}()

		// verify branch was NOT created (still on master) - wait briefly then check
		time.Sleep(500 * time.Millisecond)
		gitSvc, err := git.NewService(dir, testColors().Info())
		require.NoError(t, err)
		branch, err := gitSvc.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "master", branch, "codex-only mode should not create branch")

		cancel()
		<-done
	})

	t.Run("external_only_mode_does_not_create_branch", func(t *testing.T) {
		skipIfClaudeNotAvailable(t)

		dir := setupTestRepo(t)
		origDir, err := os.Getwd()
		require.NoError(t, err)
		err = os.Chdir(dir)
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		// create plans dir and plan file, then commit them
		require.NoError(t, os.MkdirAll("docs/plans", 0o750))
		planPath := filepath.Join(dir, "docs", "plans", "external-plan.md")
		require.NoError(t, os.WriteFile(planPath, []byte("# External Plan\n"), 0o600))

		runGit(t, dir, "add", "docs/plans/external-plan.md")
		runGit(t, dir, "commit", "-m", "add external plan")

		// run with external-only mode in background
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		done := make(chan struct{})
		go func() {
			defer close(done)
			o := opts{ExternalOnly: true, PlanFile: planPath, MaxIterations: 1}
			_ = run(ctx, o)
		}()

		// verify branch was NOT created (still on master) - wait briefly then check
		time.Sleep(500 * time.Millisecond)
		gitSvc, err := git.NewService(dir, testColors().Info())
		require.NoError(t, err)
		branch, err := gitSvc.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "master", branch, "external-only mode should not create branch")

		cancel()
		<-done
	})
}

func TestModeRequiresBranch(t *testing.T) {
	// tests the modeRequiresBranch helper function used for both branch creation and plan-move
	tests := []struct {
		mode     processor.Mode
		expected bool
	}{
		{processor.ModeFull, true},
		{processor.ModeTasksOnly, true},
		{processor.ModeReview, false},
		{processor.ModeCodexOnly, false},
		{processor.ModePlan, false},
	}

	for _, tc := range tests {
		t.Run(string(tc.mode), func(t *testing.T) {
			result := modeRequiresBranch(tc.mode)
			assert.Equal(t, tc.expected, result, "mode %s should return %v", tc.mode, tc.expected)
		})
	}
}

func TestStderrLog(t *testing.T) {
	// verify stderrLog has Print method with correct signature
	var log stderrLog
	log.Print("test %s %d", "message", 42)
}

func TestNotificationServiceCreation(t *testing.T) {
	t.Run("nil_service_when_no_channels", func(t *testing.T) {
		// run() creates notify service from config.NotifyParams.
		// with default config (no channels), notifySvc should be nil.
		// this is tested indirectly - existing tests call run() which now creates notifySvc.
		// nil service is nil-safe on Send(), so existing tests pass without changes.
		svc, err := notify.New(notify.Params{}, stderrLog{})
		require.NoError(t, err)
		assert.Nil(t, svc)
	})

	t.Run("error_on_misconfigured_channel", func(t *testing.T) {
		// missing required fields should return error (fail fast at startup)
		svc, err := notify.New(notify.Params{
			Channels: []string{"telegram"},
			// missing TelegramToken and TelegramChat
		}, stderrLog{})
		require.Error(t, err)
		assert.Nil(t, svc)
		assert.Contains(t, err.Error(), "telegram")
	})

	t.Run("nil_service_send_is_noop", func(t *testing.T) {
		// verify nil-safe Send doesn't panic
		var svc *notify.Service
		svc.Send(context.Background(), notify.Result{Status: "success"})
	})
}

func TestExecutePlanRequestHasNotifySvc(t *testing.T) {
	// verify the struct has NotifySvc field and it works with nil
	req := executePlanRequest{
		NotifySvc: nil,
	}
	assert.Nil(t, req.NotifySvc)

	// verify nil-safe call through the struct
	req.NotifySvc.Send(context.Background(), notify.Result{Status: "success"})
}

// runGit executes a git command in the given directory and fails the test on error.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v failed: %s", args, out)
}

// setupTestRepo creates a test git repository with an initial commit.
func setupTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	runGit(t, dir, "init")
	runGit(t, dir, "checkout", "-B", "master")
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "config", "user.name", "test")
	runGit(t, dir, "config", "commit.gpgsign", "false")

	readme := filepath.Join(dir, "README.md")
	err := os.WriteFile(readme, []byte("# Test\n"), 0o600)
	require.NoError(t, err)

	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-m", "initial commit")

	return dir
}

// initEmptyRepo creates a git repo with no commits (for testing ensureRepoHasCommits).
func initEmptyRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "checkout", "-B", "master")
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "config", "user.name", "test")
	runGit(t, dir, "config", "commit.gpgsign", "false")
	return dir
}

func TestConfigDirCustomPath(t *testing.T) {
	t.Run("custom_config_dir_installs_defaults", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgDir := filepath.Join(tmpDir, "custom-config")

		cfg, err := config.Load(cfgDir)
		require.NoError(t, err)
		assert.NotNil(t, cfg)

		// verify defaults were installed to the custom directory
		assert.FileExists(t, filepath.Join(cfgDir, "config"))
		assert.DirExists(t, filepath.Join(cfgDir, "prompts"))
		assert.DirExists(t, filepath.Join(cfgDir, "agents"))
	})

	t.Run("reset_with_custom_dir", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgDir := filepath.Join(tmpDir, "reset-config")

		// first load to install defaults
		_, err := config.Load(cfgDir)
		require.NoError(t, err)
		assert.FileExists(t, filepath.Join(cfgDir, "config"))

		// reset with "y" answers to all prompts
		stdin := strings.NewReader("y\ny\ny\n")
		var stdout bytes.Buffer
		_, err = config.Reset(cfgDir, stdin, &stdout)
		require.NoError(t, err)
		// freshly installed defaults are skipped (already match), verify reset ran against custom dir
		assert.Contains(t, stdout.String(), cfgDir)
		assert.FileExists(t, filepath.Join(cfgDir, "config"))
		assert.DirExists(t, filepath.Join(cfgDir, "prompts"))
		assert.DirExists(t, filepath.Join(cfgDir, "agents"))
	})

	t.Run("run_reset_with_custom_dir", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgDir := filepath.Join(tmpDir, "run-reset-config")

		// first load to install defaults
		_, err := config.Load(cfgDir)
		require.NoError(t, err)

		// exercise runReset directly with mock stdin/stdout
		stdin := strings.NewReader("y\ny\ny\n")
		var stdout bytes.Buffer
		err = runReset(cfgDir, stdin, &stdout)
		require.NoError(t, err)
		assert.FileExists(t, filepath.Join(cfgDir, "config"))
		assert.DirExists(t, filepath.Join(cfgDir, "prompts"))
		assert.DirExists(t, filepath.Join(cfgDir, "agents"))
	})
}

func TestDumpDefaults(t *testing.T) {
	t.Run("extracts_files_to_target_dir", func(t *testing.T) {
		tmpDir := filepath.Join(t.TempDir(), "defaults")
		err := dumpDefaults(tmpDir)
		require.NoError(t, err)

		// verify config exists
		assert.FileExists(t, filepath.Join(tmpDir, "config"))

		// verify specific prompt file exists
		assert.FileExists(t, filepath.Join(tmpDir, "prompts", "task.txt"))

		// verify specific agent file exists
		assert.FileExists(t, filepath.Join(tmpDir, "agents", "quality.txt"))
	})

	t.Run("config_has_raw_content", func(t *testing.T) {
		tmpDir := filepath.Join(t.TempDir(), "defaults")
		require.NoError(t, dumpDefaults(tmpDir))

		data, err := os.ReadFile(filepath.Join(tmpDir, "config")) //nolint:gosec // test
		require.NoError(t, err)
		assert.Contains(t, string(data), "claude_command")
		// raw content should have uncommented lines
		hasUncommented := false
		for line := range strings.SplitSeq(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
				hasUncommented = true
				break
			}
		}
		assert.True(t, hasUncommented, "config should have raw (uncommented) content")
	})

	t.Run("error_on_invalid_path", func(t *testing.T) {
		tmpDir := t.TempDir()
		blockingFile := filepath.Join(tmpDir, "blocker")
		require.NoError(t, os.WriteFile(blockingFile, []byte("file"), 0o600))

		err := dumpDefaults(filepath.Join(blockingFile, "sub"))
		require.Error(t, err)
	})
}

func TestHandleEarlyFlags(t *testing.T) {
	t.Run("no_flags_continues", func(t *testing.T) {
		done, err := handleEarlyFlags(opts{})
		require.NoError(t, err)
		assert.False(t, done)
	})

	t.Run("dump_defaults_exits", func(t *testing.T) {
		tmpDir := filepath.Join(t.TempDir(), "defaults")
		done, err := handleEarlyFlags(opts{DumpDefaults: tmpDir})
		require.NoError(t, err)
		assert.True(t, done)
		assert.FileExists(t, filepath.Join(tmpDir, "config"))
	})

	t.Run("dump_defaults_error", func(t *testing.T) {
		tmpDir := t.TempDir()
		blocker := filepath.Join(tmpDir, "blocker")
		require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o600))

		done, err := handleEarlyFlags(opts{DumpDefaults: filepath.Join(blocker, "sub")})
		require.Error(t, err)
		assert.True(t, done)
	})
}

func TestIsResetOnly(t *testing.T) {
	t.Run("reset_only", func(t *testing.T) {
		assert.True(t, isResetOnly(opts{Reset: true}))
	})

	t.Run("reset_with_plan_file", func(t *testing.T) {
		assert.False(t, isResetOnly(opts{Reset: true, PlanFile: "plan.md"}))
	})

	t.Run("reset_with_dump_defaults", func(t *testing.T) {
		assert.False(t, isResetOnly(opts{Reset: true, DumpDefaults: "/tmp/dir"}))
	})

	t.Run("reset_with_review", func(t *testing.T) {
		assert.False(t, isResetOnly(opts{Reset: true, Review: true}))
	})
}

func TestResolveVersion(t *testing.T) {
	t.Run("ldflags_set", func(t *testing.T) {
		orig := revision
		t.Cleanup(func() { revision = orig })
		revision = "v1.2.3-abc1234"
		assert.Equal(t, "v1.2.3-abc1234", resolveVersion())
	})

	t.Run("fallback_to_build_info", func(t *testing.T) {
		orig := revision
		t.Cleanup(func() { revision = orig })
		revision = "unknown"
		// in test context, debug.ReadBuildInfo returns (devel) module version
		// but VCS info should be available from the git repo
		v := resolveVersion()
		assert.NotEmpty(t, v)
	})
}

func TestRunWithWorktree(t *testing.T) {
	t.Run("creates_worktree_and_restores_cwd", func(t *testing.T) {
		skipIfClaudeNotAvailable(t)

		dir := setupTestRepo(t)
		origDir, err := os.Getwd()
		require.NoError(t, err)
		require.NoError(t, os.Chdir(dir))
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		// resolve dir through symlinks (macOS /var → /private/var)
		resolvedDir, err := filepath.EvalSymlinks(dir)
		require.NoError(t, err)

		// create and commit plan file
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "docs", "plans"), 0o750))
		planPath := filepath.Join(dir, "docs", "plans", "wt-test.md")
		require.NoError(t, os.WriteFile(planPath, []byte("# WT Test\n\n- [ ] task 1\n"), 0o600))
		runGit(t, dir, "add", "docs/plans/wt-test.md")
		runGit(t, dir, "commit", "-m", "add wt test plan")

		gitSvc, err := git.NewService(dir, noopLogger())
		require.NoError(t, err)

		colors := testColors()
		cfg := &config.Config{WorktreeEnabled: true}
		wtCleanup := &worktreeCleanupFn{}

		// cancel context immediately to stop executePlan fast
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err = runWithWorktree(ctx, opts{MaxIterations: 1, NoColor: true}, executePlanRequest{
			PlanFile: planPath, Mode: processor.ModeFull, GitSvc: gitSvc, Config: cfg,
			Colors: colors, DefaultBranch: "master", WtCleanup: wtCleanup,
		})
		// should fail with context canceled from the runner
		require.Error(t, err)

		// verify CWD restored to original (compare resolved paths due to macOS symlinks)
		cwd, cwdErr := os.Getwd()
		require.NoError(t, cwdErr)
		assert.Equal(t, resolvedDir, cwd, "cwd should be restored after runWithWorktree")

		// verify worktree directory cleaned up
		wtPath := filepath.Join(dir, ".ralphex", "worktrees", "wt-test")
		assert.NoDirExists(t, wtPath, "worktree should be removed after runWithWorktree")

		// verify branch was preserved (worktree creates the branch)
		assert.True(t, branchExists(t, dir, "wt-test"), "branch should be preserved after worktree removal")
	})

	t.Run("populates_worktree_cleanup_ptr", func(t *testing.T) {
		skipIfClaudeNotAvailable(t)

		dir := setupTestRepo(t)
		origDir, err := os.Getwd()
		require.NoError(t, err)
		require.NoError(t, os.Chdir(dir))
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		// create and commit plan file
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "docs", "plans"), 0o750))
		planPath := filepath.Join(dir, "docs", "plans", "wt-ptr.md")
		require.NoError(t, os.WriteFile(planPath, []byte("# WT Ptr\n\n- [ ] task 1\n"), 0o600))
		runGit(t, dir, "add", "docs/plans/wt-ptr.md")
		runGit(t, dir, "commit", "-m", "add wt ptr plan")

		gitSvc, err := git.NewService(dir, noopLogger())
		require.NoError(t, err)

		colors := testColors()
		cfg := &config.Config{WorktreeEnabled: true}

		called := false
		wtCleanup := &worktreeCleanupFn{fn: func() { called = true }}

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_ = runWithWorktree(ctx, opts{MaxIterations: 1, NoColor: true}, executePlanRequest{
			PlanFile: planPath, Mode: processor.ModeFull, GitSvc: gitSvc, Config: cfg,
			Colors: colors, DefaultBranch: "master", WtCleanup: wtCleanup,
		})

		// the cleanup fn should have been overwritten by runWithWorktree
		assert.False(t, called, "original cleanup should not have been called (replaced by runWithWorktree)")
	})

	t.Run("worktree_creates_branch", func(t *testing.T) {
		skipIfClaudeNotAvailable(t)

		dir := setupTestRepo(t)
		origDir, err := os.Getwd()
		require.NoError(t, err)
		require.NoError(t, os.Chdir(dir))
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		// create and commit plan file
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "docs", "plans"), 0o750))
		planPath := filepath.Join(dir, "docs", "plans", "wt-branch.md")
		require.NoError(t, os.WriteFile(planPath, []byte("# WT Branch\n\n- [ ] task 1\n"), 0o600))
		runGit(t, dir, "add", "docs/plans/wt-branch.md")
		runGit(t, dir, "commit", "-m", "add wt branch plan")

		gitSvc, err := git.NewService(dir, noopLogger())
		require.NoError(t, err)

		colors := testColors()
		cfg := &config.Config{WorktreeEnabled: true}
		wtCleanup := &worktreeCleanupFn{}

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_ = runWithWorktree(ctx, opts{MaxIterations: 1, NoColor: true}, executePlanRequest{
			PlanFile: planPath, Mode: processor.ModeFull, GitSvc: gitSvc, Config: cfg,
			Colors: colors, DefaultBranch: "master", WtCleanup: wtCleanup,
		})

		// branch should be preserved after worktree cleanup
		assert.True(t, branchExists(t, dir, "wt-branch"), "branch should exist after worktree removal")
	})
}

func TestWorktreeMode_SkippedForNonBranchModes(t *testing.T) {
	// worktree mode guard: cfg.WorktreeEnabled && planFile != "" && modeRequiresBranch(mode)
	// for modes that don't require a branch, worktree should not be activated.
	// this is tested via modeRequiresBranch which already has coverage.
	// here we verify the guard condition explicitly.

	t.Run("worktree_skipped_for_review_mode", func(t *testing.T) {
		skipIfClaudeNotAvailable(t)

		dir := setupTestRepo(t)
		origDir, err := os.Getwd()
		require.NoError(t, err)
		require.NoError(t, os.Chdir(dir))
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		require.NoError(t, os.MkdirAll("docs/plans", 0o750))
		planPath := filepath.Join(dir, "docs", "plans", "wt-skip.md")
		require.NoError(t, os.WriteFile(planPath, []byte("# WT Skip\n"), 0o600))
		runGit(t, dir, "add", "docs/plans/wt-skip.md")
		runGit(t, dir, "commit", "-m", "add wt skip plan")

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		o := opts{Worktree: true, Review: true, PlanFile: planPath, MaxIterations: 1, NoColor: true}
		_ = run(ctx, o)

		// no worktree directory should exist
		wtPath := filepath.Join(dir, ".ralphex", "worktrees", "wt-skip")
		assert.NoDirExists(t, wtPath, "review mode should not create worktree")

		// should stay on master
		gitSvc, gitErr := git.NewService(dir, noopLogger())
		require.NoError(t, gitErr)
		branch, brErr := gitSvc.CurrentBranch()
		require.NoError(t, brErr)
		assert.Equal(t, "master", branch, "review mode should stay on master")
	})
}

func TestRunWithWorktree_UntrackedPlan(t *testing.T) {
	skipIfClaudeNotAvailable(t)

	dir := setupTestRepo(t)
	origDir, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	resolvedDir, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)

	// create plan file but do NOT commit it (untracked)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "docs", "plans"), 0o750))
	planPath := filepath.Join(dir, "docs", "plans", "wt-untracked.md")
	require.NoError(t, os.WriteFile(planPath, []byte("# WT Untracked\n\n- [ ] task 1\n"), 0o600))

	gitSvc, err := git.NewService(dir, noopLogger())
	require.NoError(t, err)

	colors := testColors()
	cfg := &config.Config{WorktreeEnabled: true}
	wtCleanup := &worktreeCleanupFn{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = runWithWorktree(ctx, opts{MaxIterations: 1, NoColor: true}, executePlanRequest{
		PlanFile: planPath, Mode: processor.ModeFull, GitSvc: gitSvc, Config: cfg,
		Colors: colors, DefaultBranch: "master", WtCleanup: wtCleanup,
	})
	// should fail with context canceled from the runner, but plan should be committed on branch
	require.Error(t, err)

	// verify CWD restored
	cwd, cwdErr := os.Getwd()
	require.NoError(t, cwdErr)
	assert.Equal(t, resolvedDir, cwd, "cwd should be restored after runWithWorktree")

	// verify branch was created and plan was committed there
	assert.True(t, branchExists(t, dir, "wt-untracked"), "branch should exist")

	// verify worktree cleaned up
	wtPath := filepath.Join(dir, ".ralphex", "worktrees", "wt-untracked")
	assert.NoDirExists(t, wtPath, "worktree should be removed")
}

func TestRunWithWorktree_CreateWorktreeError(t *testing.T) {
	dir := setupTestRepo(t)
	origDir, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	// create plan file and commit it
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "docs", "plans"), 0o750))
	planPath := filepath.Join(dir, "docs", "plans", "wt-fail.md")
	require.NoError(t, os.WriteFile(planPath, []byte("# WT Fail\n"), 0o600))
	runGit(t, dir, "add", "docs/plans/wt-fail.md")
	runGit(t, dir, "commit", "-m", "add wt fail plan")

	gitSvc, err := git.NewService(dir, noopLogger())
	require.NoError(t, err)

	// pre-create worktree dir to force "already exists" error
	wtPath := filepath.Join(dir, ".ralphex", "worktrees", "wt-fail")
	require.NoError(t, os.MkdirAll(wtPath, 0o750))

	colors := testColors()
	cfg := &config.Config{WorktreeEnabled: true}
	wtCleanup := &worktreeCleanupFn{}

	ctx := context.Background()
	err = runWithWorktree(ctx, opts{MaxIterations: 1, NoColor: true}, executePlanRequest{
		PlanFile: planPath, Mode: processor.ModeFull, GitSvc: gitSvc, Config: cfg,
		Colors: colors, DefaultBranch: "master", WtCleanup: wtCleanup,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create worktree")
}

func TestEnsureGitIgnored(t *testing.T) {
	t.Run("odd_pairs_returns_error", func(t *testing.T) {
		dir := setupTestRepo(t)
		gitSvc, err := git.NewService(dir, noopLogger())
		require.NoError(t, err)
		err = ensureGitIgnored(gitSvc, "pattern-only")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "requires pairs")
	})

	t.Run("commits_when_gitignore_clean", func(t *testing.T) {
		dir := setupTestRepo(t)
		gitSvc, err := git.NewService(dir, noopLogger())
		require.NoError(t, err)

		err = ensureGitIgnored(gitSvc, ".ralphex/progress/", ".ralphex/progress/test-probe")
		require.NoError(t, err)

		// verify .gitignore was committed (no uncommitted changes)
		hasChanges, chErr := gitSvc.FileHasChanges(".gitignore")
		require.NoError(t, chErr)
		assert.False(t, hasChanges, ".gitignore should be committed")
	})

	t.Run("skips_commit_when_gitignore_dirty", func(t *testing.T) {
		dir := setupTestRepo(t)
		gitSvc, err := git.NewService(dir, noopLogger())
		require.NoError(t, err)

		// make .gitignore dirty first
		igPath := filepath.Join(dir, ".gitignore")
		require.NoError(t, os.WriteFile(igPath, []byte("some-user-pattern\n"), 0o600))

		err = ensureGitIgnored(gitSvc, ".ralphex/progress/", ".ralphex/progress/test-probe")
		require.NoError(t, err)

		// .gitignore should still have uncommitted changes (not auto-committed)
		hasChanges, chErr := gitSvc.FileHasChanges(".gitignore")
		require.NoError(t, chErr)
		assert.True(t, hasChanges, ".gitignore should remain dirty when it was dirty before")
	})
}

// branchExists checks if a branch exists in the given git repository.
func branchExists(t *testing.T, dir, branch string) bool {
	t.Helper()
	cmd := exec.Command("git", "branch", "--list", branch)
	cmd.Dir = dir
	out, err := cmd.Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(out)) != ""
}
