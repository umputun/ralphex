package processor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/config"
	"github.com/umputun/ralphex/pkg/executor"
	"github.com/umputun/ralphex/pkg/processor/mocks"
	"github.com/umputun/ralphex/pkg/processor/phase"
	"github.com/umputun/ralphex/pkg/status"
)

// newMockExecutor creates a mock executor with predefined results.
func newMockExecutor(results []executor.Result) *mocks.ExecutorMock {
	idx := 0
	return &mocks.ExecutorMock{
		RunFunc: func(_ context.Context, _ string) executor.Result {
			if idx >= len(results) {
				return executor.Result{Error: errors.New("no more mock results")}
			}
			result := results[idx]
			idx++
			return result
		},
	}
}

// newMockLogger creates a mock logger with no-op implementations.
func newRunnerMockLogger(path string) *mocks.LoggerMock {
	return &mocks.LoggerMock{
		PrintFunc:          func(_ string, _ ...any) {},
		PrintRawFunc:       func(_ string, _ ...any) {},
		PrintSectionFunc:   func(_ status.Section) {},
		PrintAlignedFunc:   func(_ string) {},
		LogQuestionFunc:    func(_ string, _ []string) {},
		LogAnswerFunc:      func(_ string) {},
		LogDraftReviewFunc: func(_, _ string) {},
		PathFunc:           func() string { return path },
	}
}

type testTaskPhase struct {
	runFunc                  func(ctx context.Context) error
	validatePlanHasTasksFunc func() error
}

func (p testTaskPhase) Run(ctx context.Context) error {
	if p.runFunc == nil {
		return nil
	}
	return p.runFunc(ctx)
}

func (p testTaskPhase) ValidatePlanHasTasks() error {
	if p.validatePlanHasTasksFunc == nil {
		return nil
	}
	return p.validatePlanHasTasksFunc()
}

type testReviewPhase struct {
	firstFunc func(ctx context.Context) error
	loopFunc  func(ctx context.Context, prefix string) error
}

func (p testReviewPhase) First(ctx context.Context) error {
	if p.firstFunc == nil {
		return nil
	}
	return p.firstFunc(ctx)
}

func (p testReviewPhase) Loop(ctx context.Context, prefix string) error {
	if p.loopFunc == nil {
		return nil
	}
	return p.loopFunc(ctx, prefix)
}

type testExternalReviewPhase struct {
	toolValue   string
	hadFindings bool
	runErr      error
	runFunc     func(ctx context.Context) error
}

func (p testExternalReviewPhase) Tool() string {
	if p.toolValue == "" {
		return "codex"
	}
	return p.toolValue
}

func (p testExternalReviewPhase) Run(ctx context.Context) (phase.ExternalReviewOutcome, error) {
	if p.runFunc != nil {
		if err := p.runFunc(ctx); err != nil {
			return phase.ExternalReviewOutcome{}, err
		}
	}
	return phase.ExternalReviewOutcome{HadFindings: p.hadFindings}, p.runErr
}

type testFinalizePhase struct {
	runFunc func(ctx context.Context) error
}

func (p testFinalizePhase) Run(ctx context.Context) error {
	if p.runFunc == nil {
		return nil
	}
	return p.runFunc(ctx)
}

type testPlanCreationPhase struct {
	runFunc func(ctx context.Context) error
}

func (p testPlanCreationPhase) Run(ctx context.Context) error {
	if p.runFunc == nil {
		return nil
	}
	return p.runFunc(ctx)
}

func TestRunner_Run_UnknownMode(t *testing.T) {
	log := newRunnerMockLogger("")
	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)

	r := NewWithExecutors(Config{Mode: "invalid"}, log, Executors{Task: claude, External: codex}, &status.PhaseHolder{})
	err := r.Run(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown mode")
}

func TestRunner_RunFull_NoPlanFile(t *testing.T) {
	log := newRunnerMockLogger("")
	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)

	r := NewWithExecutors(Config{Mode: ModeFull}, log, Executors{Task: claude, External: codex}, &status.PhaseHolder{})
	err := r.Run(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "plan file required")
}

func TestRunner_NewWithExecutors_NilPhaseHolder(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n### Task 1: first\n- [x] done"), 0o600))

	log := newRunnerMockLogger("progress.txt")
	task := newMockExecutor([]executor.Result{{Output: "done", Signal: status.Completed}})
	cfg := Config{Mode: ModeTasksOnly, PlanFile: planFile, MaxIterations: 1, AppConfig: testAppConfig(t)}
	r := NewWithExecutors(cfg, log, Executors{Task: task}, nil)

	require.NotNil(t, r.phaseHolder)
	require.NoError(t, r.Run(t.Context()))
	assert.Equal(t, status.PhaseTask, r.phaseHolder.Get())
}

func TestRunner_RunFull_Success(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	// all tasks complete - no [ ] checkboxes
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n### Task 1: first\n- [x] done"), 0o600))

	log := newRunnerMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "task done", Signal: status.Completed},    // task phase completes
		{Output: "review done", Signal: status.ReviewDone}, // first review
		{Output: "review done", Signal: status.ReviewDone}, // pre-codex review loop
		{Output: "fixed issues"},                           // codex eval iter 1 — findings fixed
		{Output: "done", Signal: status.CodexDone},         // codex eval iter 2 — no more findings
		{Output: "review done", Signal: status.ReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Output: "found issue in foo.go"}, // codex iteration 1 — finds issues
		{Output: "no issues found"},       // codex iteration 2 — clean
	})

	cfg := Config{
		Mode: ModeFull, PlanFile: planFile, MaxIterations: 50,
		IterationDelayMs: 1, CodexEnabled: true, AppConfig: testAppConfig(t),
	}
	r := NewWithExecutors(cfg, log, Executors{Task: claude, External: codex}, &status.PhaseHolder{})
	err := r.Run(t.Context())

	require.NoError(t, err)
	assert.Len(t, codex.RunCalls(), 2)
}

func TestRunner_RunFull_NoCodexFindings(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n### Task 1: first\n- [x] done"), 0o600))

	log := newRunnerMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "task done", Signal: status.Completed},
		{Output: "review done", Signal: status.ReviewDone}, // first review
		{Output: "review done", Signal: status.ReviewDone}, // pre-codex review loop
		// codex returns empty → no findings → post-codex review skipped
	})
	codex := newMockExecutor([]executor.Result{
		{Output: ""}, // codex finds nothing
	})

	cfg := Config{Mode: ModeFull, PlanFile: planFile, MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}
	r := NewWithExecutors(cfg, log, Executors{Task: claude, External: codex}, &status.PhaseHolder{})
	err := r.Run(t.Context())

	require.NoError(t, err)
}

func TestRunner_RunFull_CodexExecutor_SkipsExternalReview(t *testing.T) {
	// when --codex is in effect, cfg.AppConfig.Executor == ExecutorCodex and
	// cfg.AppConfig.ExternalReviewTool is forced to "none" by the CLI layer.
	// runFull must route through the external phase tool=="none" skip
	// so the external executor is never invoked.
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n### Task 1: first\n- [x] done"), 0o600))

	log := newRunnerMockLogger("progress.txt")
	task := newMockExecutor([]executor.Result{
		{Output: "task done", Signal: status.Completed},    // task phase
		{Output: "review done", Signal: status.ReviewDone}, // first review
		{Output: "review done", Signal: status.ReviewDone}, // pre-codex review loop
	})
	external := newMockExecutor(nil) // must never be called

	appCfg := testAppConfig(t)
	appCfg.Executor = config.ExecutorCodex
	appCfg.ExternalReviewTool = "none"

	cfg := Config{
		Mode:          ModeFull,
		PlanFile:      planFile,
		MaxIterations: 50,
		CodexEnabled:  true, // would normally enable external review, but ExternalReviewTool="none" wins
		AppConfig:     appCfg,
	}
	r := NewWithExecutors(cfg, log, Executors{Task: task, External: external}, &status.PhaseHolder{})
	err := r.Run(t.Context())

	require.NoError(t, err)
	assert.Len(t, task.RunCalls(), 3, "task executor should run for task phase, first review, and pre-codex review loop")
	assert.Empty(t, external.RunCalls(), "external executor must never be called when --codex forces ExternalReviewTool=none")
}

func TestRunner_RunReviewOnly_Success(t *testing.T) {
	log := newRunnerMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "review done", Signal: status.ReviewDone}, // first review
		{Output: "review done", Signal: status.ReviewDone}, // pre-codex review loop
		{Output: "fixed issues"},                           // codex eval iter 1 — findings fixed
		{Output: "done", Signal: status.CodexDone},         // codex eval iter 2 — no more findings
		{Output: "review done", Signal: status.ReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Output: "found issue"},     // codex iteration 1
		{Output: "no issues found"}, // codex iteration 2
	})

	cfg := Config{Mode: ModeReview, MaxIterations: 50, IterationDelayMs: 1, CodexEnabled: true, AppConfig: testAppConfig(t)}
	r := NewWithExecutors(cfg, log, Executors{Task: claude, External: codex}, &status.PhaseHolder{})
	err := r.Run(t.Context())

	require.NoError(t, err)
	assert.Len(t, codex.RunCalls(), 2)
}

func TestRunner_RunCodexOnly_Success(t *testing.T) {
	log := newRunnerMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "fixed issues"},                           // codex eval iter 1 — findings fixed
		{Output: "done", Signal: status.CodexDone},         // codex eval iter 2 — no more findings
		{Output: "review done", Signal: status.ReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Output: "found issue"},     // codex iteration 1
		{Output: "no issues found"}, // codex iteration 2
	})

	cfg := Config{Mode: ModeCodexOnly, MaxIterations: 50, IterationDelayMs: 1, CodexEnabled: true, AppConfig: testAppConfig(t)}
	r := NewWithExecutors(cfg, log, Executors{Task: claude, External: codex}, &status.PhaseHolder{})
	err := r.Run(t.Context())

	require.NoError(t, err)
	assert.Len(t, codex.RunCalls(), 2)
}

func TestRunner_RunCodexOnly_NoFindings(t *testing.T) {
	log := newRunnerMockLogger("progress.txt")
	// codex returns empty → no findings → post-codex review skipped
	claude := newMockExecutor(nil)
	codex := newMockExecutor([]executor.Result{
		{Output: ""}, // no findings
	})

	cfg := Config{Mode: ModeCodexOnly, MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}
	r := NewWithExecutors(cfg, log, Executors{Task: claude, External: codex}, &status.PhaseHolder{})
	err := r.Run(t.Context())

	require.NoError(t, err)
}

func TestRunner_MaxExternalIterations_ExplicitLimit(t *testing.T) {
	log := newRunnerMockLogger("progress.txt")
	// codex loop: 2 iterations (each = codex + claude eval), then post-codex review
	claude := newMockExecutor([]executor.Result{
		{Output: "still issues"},                           // codex eval iter 1 (no CodexDone)
		{Output: "still issues"},                           // codex eval iter 2 (no CodexDone)
		{Output: "review done", Signal: status.ReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Output: "found issue 1"},
		{Output: "found issue 2"},
	})

	cfg := Config{
		Mode: ModeCodexOnly, MaxIterations: 50, IterationDelayMs: 1,
		MaxExternalIterations: 2, CodexEnabled: true, AppConfig: testAppConfig(t),
	}
	r := NewWithExecutors(cfg, log, Executors{Task: claude, External: codex}, &status.PhaseHolder{})
	err := r.Run(t.Context())

	require.NoError(t, err)
	assert.Len(t, codex.RunCalls(), 2, "codex should be called exactly MaxExternalIterations times")
}

func TestRunner_MaxExternalIterations_DerivedFormula(t *testing.T) {
	log := newRunnerMockLogger("progress.txt")
	// with MaxIterations=15 and MaxExternalIterations=0 (auto): derived = max(3, 15/5) = 3
	claude := newMockExecutor([]executor.Result{
		{Output: "still issues"},                           // codex eval iter 1
		{Output: "still issues"},                           // codex eval iter 2
		{Output: "still issues"},                           // codex eval iter 3
		{Output: "review done", Signal: status.ReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Output: "found issue 1"},
		{Output: "found issue 2"},
		{Output: "found issue 3"},
	})

	cfg := Config{
		Mode: ModeCodexOnly, MaxIterations: 15, IterationDelayMs: 1,
		MaxExternalIterations: 0, CodexEnabled: true, AppConfig: testAppConfig(t),
	}
	r := NewWithExecutors(cfg, log, Executors{Task: claude, External: codex}, &status.PhaseHolder{})
	err := r.Run(t.Context())

	require.NoError(t, err)
	assert.Len(t, codex.RunCalls(), 3, "codex should use derived formula: max(3, 15/5) = 3")
}

func TestRunner_CodexDisabled_SkipsCodexPhase(t *testing.T) {
	log := newRunnerMockLogger("progress.txt")
	// codex disabled → no findings → post-codex review skipped
	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)

	cfg := Config{Mode: ModeCodexOnly, MaxIterations: 50, CodexEnabled: false, AppConfig: testAppConfig(t)}
	r := NewWithExecutors(cfg, log, Executors{Task: claude, External: codex}, &status.PhaseHolder{})
	err := r.Run(t.Context())

	require.NoError(t, err)
	assert.Empty(t, codex.RunCalls(), "codex should not be called when disabled")
}

func TestRunner_RunTasksOnly_Success(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	// all tasks complete - no [ ] checkboxes
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n### Task 1: first\n- [x] done"), 0o600))

	log := newRunnerMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "task done", Signal: status.Completed}, // task phase completes
	})
	codex := newMockExecutor(nil)

	cfg := Config{Mode: ModeTasksOnly, PlanFile: planFile, MaxIterations: 50, AppConfig: testAppConfig(t)}
	r := NewWithExecutors(cfg, log, Executors{Task: claude, External: codex}, &status.PhaseHolder{})
	err := r.Run(t.Context())

	require.NoError(t, err)
	assert.Empty(t, codex.RunCalls(), "codex should not be called in tasks-only mode")
	assert.Len(t, claude.RunCalls(), 1)
}

func TestRunner_RunTasksOnly_NoPlanFile(t *testing.T) {
	log := newRunnerMockLogger("")
	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)

	r := NewWithExecutors(Config{Mode: ModeTasksOnly}, log, Executors{Task: claude, External: codex}, &status.PhaseHolder{})
	err := r.Run(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "plan file required")
}

func TestRunner_RunTasksOnly_TaskPhaseError(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n### Task 1: first\n- [ ] todo"), 0o600))

	log := newRunnerMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "error", Signal: status.Failed}, // first try
		{Output: "error", Signal: status.Failed}, // retry
	})
	codex := newMockExecutor(nil)

	cfg := Config{Mode: ModeTasksOnly, PlanFile: planFile, MaxIterations: 10, AppConfig: testAppConfig(t)}
	r := NewWithExecutors(cfg, log, Executors{Task: claude, External: codex}, &status.PhaseHolder{})
	err := r.Run(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "FAILED signal")
}

func TestRunner_RunTasksOnly_NoReviews(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n### Task 1: first\n- [x] done\n### Task 2: second\n- [x] done"), 0o600))

	log := newRunnerMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "task done", Signal: status.Completed},
	})
	codex := newMockExecutor(nil)

	cfg := Config{
		Mode:          ModeTasksOnly,
		PlanFile:      planFile,
		MaxIterations: 50,
		CodexEnabled:  true, // enabled but should not run in tasks-only mode
		AppConfig:     testAppConfig(t),
	}
	r := NewWithExecutors(cfg, log, Executors{Task: claude, External: codex}, &status.PhaseHolder{})
	err := r.Run(t.Context())

	require.NoError(t, err)
	// verify no review or codex phases ran - only task phase
	assert.Len(t, claude.RunCalls(), 1, "only task phase should run")
	assert.Empty(t, codex.RunCalls(), "codex should not run in tasks-only mode")
}

func TestRunner_CodexPhase_Error(t *testing.T) {
	log := newRunnerMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "review done", Signal: status.ReviewDone}, // first review
		{Output: "review done", Signal: status.ReviewDone}, // pre-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Error: errors.New("codex error")},
	})

	cfg := Config{Mode: ModeReview, MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}
	r := NewWithExecutors(cfg, log, Executors{Task: claude, External: codex}, &status.PhaseHolder{})
	err := r.Run(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "codex")
	assert.Len(t, codex.RunCalls(), 1, "codex should be called once")
}

func TestRunner_ClaudeExecution_Error(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n### Task 1: first\n- [ ] todo"), 0o600))

	log := newRunnerMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Error: errors.New("claude error")},
	})
	codex := newMockExecutor(nil)

	cfg := Config{Mode: ModeFull, PlanFile: planFile, MaxIterations: 10, AppConfig: testAppConfig(t)}
	r := NewWithExecutors(cfg, log, Executors{Task: claude, External: codex}, &status.PhaseHolder{})
	err := r.Run(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "claude execution")
}

func TestRunner_RunFull_NoTaskSections(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Overview\n## Goals\n- describe architecture"), 0o600))

	log := newRunnerMockLogger("progress.txt")
	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)

	cfg := Config{Mode: ModeFull, PlanFile: planFile, MaxIterations: 10, AppConfig: testAppConfig(t)}
	r := NewWithExecutors(cfg, log, Executors{Task: claude, External: codex}, &status.PhaseHolder{})
	err := r.Run(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no executable task sections")
	assert.Empty(t, claude.RunCalls(), "claude must not be invoked when plan has no task sections")
	assert.Empty(t, codex.RunCalls(), "codex must not be invoked when plan has no task sections")
}

func TestRunner_RunTasksOnly_NoTaskSections(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Overview\n## Goals\n- describe architecture"), 0o600))

	log := newRunnerMockLogger("progress.txt")
	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)

	cfg := Config{Mode: ModeTasksOnly, PlanFile: planFile, MaxIterations: 10, AppConfig: testAppConfig(t)}
	r := NewWithExecutors(cfg, log, Executors{Task: claude, External: codex}, &status.PhaseHolder{})
	err := r.Run(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no executable task sections")
	assert.Empty(t, claude.RunCalls(), "claude must not be invoked when plan has no task sections")
}

func TestRunner_BuildCodexPrompt_CompletedDir(t *testing.T) {
	tmpDir := t.TempDir()
	plansDir := filepath.Join(tmpDir, "docs", "plans")
	completedDir := filepath.Join(plansDir, "completed")
	require.NoError(t, os.MkdirAll(completedDir, 0o700))

	// file is in completed/, but config references original path
	originalPath := filepath.Join(plansDir, "plan.md")
	completedPath := filepath.Join(completedDir, "plan.md")
	require.NoError(t, os.WriteFile(completedPath, []byte("# Plan"), 0o600))

	log := newRunnerMockLogger("")
	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)

	cfg := Config{PlanFile: originalPath, AppConfig: testAppConfig(t)}
	r := NewWithExecutors(cfg, log, Executors{Task: claude, External: codex}, &status.PhaseHolder{})

	locator := newPlanLocator(r.cfg)
	prompts := newPromptBuilder(promptBuilderOpts{cfg: r.cfg, log: r.log, locator: locator})
	prompt := prompts.CodexReviewPrompt(true, "")

	assert.Contains(t, prompt, completedPath)
	assert.NotContains(t, prompt, originalPath)
}

func TestRunner_ErrorPatternMatch_ClaudeInTaskPhase(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n### Task 1: first\n- [ ] todo"), 0o600))

	log := newRunnerMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "You've hit your limit", Error: &executor.PatternMatchError{Pattern: "You've hit your limit", HelpCmd: "claude /usage"}},
	})
	codex := newMockExecutor(nil)

	cfg := Config{Mode: ModeFull, PlanFile: planFile, MaxIterations: 10, AppConfig: testAppConfig(t)}
	r := NewWithExecutors(cfg, log, Executors{Task: claude, External: codex}, &status.PhaseHolder{})
	err := r.Run(t.Context())

	require.Error(t, err)
	var patternErr *executor.PatternMatchError
	require.ErrorAs(t, err, &patternErr)
	assert.Equal(t, "You've hit your limit", patternErr.Pattern)
	assert.Equal(t, "claude /usage", patternErr.HelpCmd)

	// verify logging
	var foundErrorLog, foundHelpLog bool
	for _, call := range log.PrintCalls() {
		if strings.Contains(call.Format, "error: detected") && strings.Contains(call.Format, "%s output") {
			foundErrorLog = true
		}
		if strings.Contains(call.Format, "for more information") {
			foundHelpLog = true
		}
	}
	assert.True(t, foundErrorLog, "should log error message with detected pattern")
	assert.True(t, foundHelpLog, "should log help command")
}

func TestRunner_LimitPatternMatch_ClaudeInTaskPhase_NoWait(t *testing.T) {
	// verifies that LimitPatternError is handled gracefully by retryPolicy
	// when waitOnLimit == 0, same as PatternMatchError (logs error + help, returns error)
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n### Task 1: first\n- [ ] todo"), 0o600))

	log := newRunnerMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "You've hit your limit", Error: &executor.LimitPatternError{Pattern: "You've hit your limit", HelpCmd: "claude /usage"}},
	})
	codex := newMockExecutor(nil)

	cfg := Config{Mode: ModeFull, PlanFile: planFile, MaxIterations: 10, AppConfig: testAppConfig(t)}
	r := NewWithExecutors(cfg, log, Executors{Task: claude, External: codex}, &status.PhaseHolder{})
	err := r.Run(t.Context())

	require.Error(t, err)
	var limitErr *executor.LimitPatternError
	require.ErrorAs(t, err, &limitErr)
	assert.Equal(t, "You've hit your limit", limitErr.Pattern)
	assert.Equal(t, "claude /usage", limitErr.HelpCmd)

	// verify retryPolicy logging
	var foundErrorLog, foundHelpLog bool
	for _, call := range log.PrintCalls() {
		if strings.Contains(call.Format, "error: detected") && strings.Contains(call.Format, "%s output") {
			foundErrorLog = true
		}
		if strings.Contains(call.Format, "for more information") {
			foundHelpLog = true
		}
	}
	assert.True(t, foundErrorLog, "should log error message with detected pattern")
	assert.True(t, foundHelpLog, "should log help command")
}

func TestRunner_CodexAndPostReview_ShortCircuitWhenCodexExecutorDisablesExternal(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n### Task 1: first\n- [x] done"), 0o600))

	log := newRunnerMockLogger("progress.txt")
	codexTask := newMockExecutor([]executor.Result{
		{Output: "task done", Signal: status.Completed},
		{Output: "review done", Signal: status.ReviewDone},
		{Output: "review done", Signal: status.ReviewDone},
	})

	appCfg := testAppConfig(t)
	appCfg.Executor = config.ExecutorCodex
	appCfg.ExternalReviewTool = "none"
	cfg := Config{
		Mode:          ModeFull,
		PlanFile:      planFile,
		MaxIterations: 50,
		CodexEnabled:  false,
		AppConfig:     appCfg,
	}
	r := NewWithExecutors(cfg, log, Executors{Task: codexTask}, &status.PhaseHolder{})
	err := r.Run(t.Context())

	require.NoError(t, err)

	for _, call := range log.PrintSectionCalls() {
		assert.NotContains(t, call.Section.Label, "codex external review",
			"codex executor mode should not print the external review section when external review is disabled")
	}

	var foundDisabledMsg bool
	for _, call := range log.PrintCalls() {
		if strings.Contains(call.Format, "external review disabled") {
			foundDisabledMsg = true
			break
		}
	}
	assert.True(t, foundDisabledMsg, "should log 'external review disabled' message")
}

func TestRunner_CodexAndPostReview_ShortCircuitWhenClaudeModeDisablesExternal(t *testing.T) {
	// the short-circuit at runExternalAndPostReview must fire for claude mode too when
	// external review is disabled — otherwise dashboards briefly show PhaseCodex and an
	// external review section header followed by contradictory log lines.
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n### Task 1: first\n- [x] done"), 0o600))

	log := newRunnerMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "task done", Signal: status.Completed},    // task phase
		{Output: "review done", Signal: status.ReviewDone}, // first review
		{Output: "review done", Signal: status.ReviewDone}, // pre-codex review loop
	})
	codex := newMockExecutor(nil)

	holder := &status.PhaseHolder{}
	cfg := Config{
		Mode:          ModeFull,
		PlanFile:      planFile,
		MaxIterations: 50,
		CodexEnabled:  false, // makes the external review phase return tool "none"
		AppConfig:     testAppConfig(t),
	}
	r := NewWithExecutors(cfg, log, Executors{Task: claude, External: codex}, holder)
	err := r.Run(t.Context())

	require.NoError(t, err)

	for _, call := range log.PrintSectionCalls() {
		assert.NotContains(t, call.Section.Label, "codex external review",
			"claude mode with external review disabled must not print the codex section header")
	}

	var foundDisabledMsg bool
	for _, call := range log.PrintCalls() {
		if strings.Contains(call.Format, "external review disabled") {
			foundDisabledMsg = true
			break
		}
	}
	assert.True(t, foundDisabledMsg, "should log 'external review disabled' message")
}

func TestRunner_Finalize_RunsInReviewOnlyMode(t *testing.T) {
	log := newRunnerMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "review done", Signal: status.ReviewDone}, // first review
		{Output: "review done", Signal: status.ReviewDone}, // pre-codex review loop
		// codex disabled → no findings → post-codex review skipped
		{Output: "finalize done"}, // finalize step
	})
	codex := newMockExecutor(nil)

	cfg := Config{
		Mode:            ModeReview,
		MaxIterations:   50,
		CodexEnabled:    false,
		FinalizeEnabled: true,
		AppConfig:       testAppConfig(t),
	}
	r := NewWithExecutors(cfg, log, Executors{Task: claude, External: codex}, &status.PhaseHolder{})
	err := r.Run(t.Context())

	require.NoError(t, err)
	// verify finalize ran (3 claude calls: first review + pre-codex loop + finalize)
	assert.Len(t, claude.RunCalls(), 3)
}

func TestRunner_Finalize_RunsInCodexOnlyMode(t *testing.T) {
	log := newRunnerMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		// codex disabled → no findings → post-codex review skipped
		{Output: "finalize done"}, // finalize step
	})
	codex := newMockExecutor(nil)

	cfg := Config{
		Mode:            ModeCodexOnly,
		MaxIterations:   50,
		CodexEnabled:    false,
		FinalizeEnabled: true,
		AppConfig:       testAppConfig(t),
	}
	r := NewWithExecutors(cfg, log, Executors{Task: claude, External: codex}, &status.PhaseHolder{})
	err := r.Run(t.Context())

	require.NoError(t, err)
	// verify finalize ran (1 claude call: finalize only)
	assert.Len(t, claude.RunCalls(), 1)
}

func TestRunner_Finalize_CodexExecutor_RunsAllPhasesThroughSharedInstance(t *testing.T) {
	// under --codex, task / review / finalize all run through a single shared codex
	// executor with MultiAgent=true. pin the call sequence so a future split into
	// distinct instances would regress visibly.
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n### Task 1: first\n- [x] done"), 0o600))

	log := newRunnerMockLogger("progress.txt")

	// one shared codex mock for task + both reviews + finalize.
	codexExec := newMockExecutor([]executor.Result{
		{Output: "task done", Signal: status.Completed},    // task phase
		{Output: "review done", Signal: status.ReviewDone}, // first review
		{Output: "review done", Signal: status.ReviewDone}, // second review
		{Output: "finalize done"},                          // finalize step
	})

	appCfg := testAppConfig(t)
	appCfg.Executor = config.ExecutorCodex
	appCfg.ExternalReviewTool = "none" // codex mode disables external review
	cfg := Config{
		Mode:                  ModeFull,
		PlanFile:              planFile,
		MaxIterations:         50,
		FinalizeEnabled:       true,
		ExternalReviewToolSet: true,
		AppConfig:             appCfg,
	}
	r := NewWithExecutors(cfg, log,
		Executors{Task: codexExec, Review: codexExec, External: nil},
		&status.PhaseHolder{})
	err := r.Run(t.Context())

	require.NoError(t, err)
	assert.Len(t, codexExec.RunCalls(), 4, "shared codex executor must be invoked for task + both reviews + finalize")
}

func TestRunner_CodexAndPostReview_PipelineOrder(t *testing.T) {
	tests := []struct {
		name          string
		mode          Mode
		planFile      bool
		claudeResults []executor.Result
		codexResults  []executor.Result
		expClaude     int // expected claude call count
		expCodex      int // expected codex call count
		expPhases     []status.Phase
	}{
		{
			name: "codex-only runs codex then review then finalize",
			mode: ModeCodexOnly,
			claudeResults: []executor.Result{
				{Output: "fixed issues"},                           // codex eval iter 1 — findings fixed
				{Output: "done", Signal: status.CodexDone},         // codex eval iter 2 — no more findings
				{Output: "review done", Signal: status.ReviewDone}, // post-codex review loop
				{Output: "finalize done"},                          // finalize step
			},
			codexResults: []executor.Result{
				{Output: "found issue"},     // codex iteration 1
				{Output: "no issues found"}, // codex iteration 2
			},
			expClaude: 4,
			expCodex:  2,
			expPhases: []status.Phase{
				status.PhaseCodex,                         // initial codex phase
				status.PhaseClaudeEval, status.PhaseCodex, // iter 1 eval+restore
				status.PhaseClaudeEval, status.PhaseCodex, // iter 2 eval+restore
				status.PhaseReview, status.PhaseFinalize,
			},
		},
		{
			name: "review-only runs first review then codex then review then finalize",
			mode: ModeReview,
			claudeResults: []executor.Result{
				{Output: "review done", Signal: status.ReviewDone}, // first review
				{Output: "review done", Signal: status.ReviewDone}, // pre-codex review loop
				{Output: "fixed issues"},                           // codex eval iter 1 — findings fixed
				{Output: "done", Signal: status.CodexDone},         // codex eval iter 2 — no more findings
				{Output: "review done", Signal: status.ReviewDone}, // post-codex review loop
				{Output: "finalize done"},                          // finalize step
			},
			codexResults: []executor.Result{
				{Output: "found issue"},     // codex iteration 1
				{Output: "no issues found"}, // codex iteration 2
			},
			expClaude: 6,
			expCodex:  2,
			// review phase set once at start (covers first review + pre-codex loop),
			// then codex loop (2 iterations), then review, then finalize
			expPhases: []status.Phase{
				status.PhaseReview,                        // first review + pre-codex loop
				status.PhaseCodex,                         // initial codex phase
				status.PhaseClaudeEval, status.PhaseCodex, // iter 1 eval+restore
				status.PhaseClaudeEval, status.PhaseCodex, // iter 2 eval+restore
				status.PhaseReview, status.PhaseFinalize,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var phases []status.Phase
			holder := &status.PhaseHolder{}
			holder.OnChange(func(_, newPhase status.Phase) {
				phases = append(phases, newPhase)
			})

			log := newRunnerMockLogger("progress.txt")
			claude := newMockExecutor(tc.claudeResults)
			codex := newMockExecutor(tc.codexResults)

			var planFile string
			if tc.planFile {
				tmpDir := t.TempDir()
				planFile = filepath.Join(tmpDir, "plan.md")
				require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n### Task 1: first\n- [x] done"), 0o600))
			}

			cfg := Config{
				Mode:             tc.mode,
				PlanFile:         planFile,
				MaxIterations:    50,
				IterationDelayMs: 1,
				CodexEnabled:     true,
				FinalizeEnabled:  true,
				AppConfig:        testAppConfig(t),
			}
			r := NewWithExecutors(cfg, log, Executors{Task: claude, External: codex}, holder)
			err := r.Run(t.Context())

			require.NoError(t, err)
			assert.Len(t, claude.RunCalls(), tc.expClaude)
			assert.Len(t, codex.RunCalls(), tc.expCodex)
			assert.Equal(t, tc.expPhases, phases, "phase transitions should match expected order")
		})
	}
}

func TestRunner_CodexAndPostReview_InjectedExternalNoFindingsSkipsPostReview(t *testing.T) {
	log := newRunnerMockLogger("progress.txt")
	cfg := Config{Mode: ModeCodexOnly, MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}
	r := NewWithExecutors(cfg, log, Executors{Task: newMockExecutor(nil)}, &status.PhaseHolder{})

	var reviewCalled, finalizeCalled bool
	r.phases.external = testExternalReviewPhase{toolValue: "codex"}
	r.phases.review = testReviewPhase{loopFunc: func(context.Context, string) error {
		reviewCalled = true
		return nil
	}}
	r.phases.finalize = testFinalizePhase{runFunc: func(context.Context) error {
		finalizeCalled = true
		return nil
	}}

	err := r.Run(t.Context())

	require.NoError(t, err)
	assert.False(t, reviewCalled)
	assert.True(t, finalizeCalled)
}

func TestRunner_ExternalAndPostReview_UsesToolSpecificSectionLabel(t *testing.T) {
	log := newRunnerMockLogger("progress.txt")
	cfg := Config{Mode: ModeCodexOnly, MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}
	r := NewWithExecutors(cfg, log, Executors{Task: newMockExecutor(nil)}, &status.PhaseHolder{})

	r.phases.external = testExternalReviewPhase{toolValue: "custom"}
	r.phases.finalize = testFinalizePhase{}

	err := r.Run(t.Context())

	require.NoError(t, err)
	sectionCalls := log.PrintSectionCalls()
	labels := make([]string, 0, len(sectionCalls))
	for _, call := range sectionCalls {
		labels = append(labels, call.Section.Label)
	}
	assert.Contains(t, labels, "custom external review")
}

func TestRunner_CodexAndPostReview_InjectedExternalFindingsRunsPostReview(t *testing.T) {
	log := newRunnerMockLogger("progress.txt")
	cfg := Config{Mode: ModeCodexOnly, MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}
	r := NewWithExecutors(cfg, log, Executors{Task: newMockExecutor(nil)}, &status.PhaseHolder{})

	var reviewPrefix string
	var finalizeCalled bool
	r.phases.external = testExternalReviewPhase{toolValue: "codex", hadFindings: true}
	r.phases.review = testReviewPhase{loopFunc: func(_ context.Context, prefix string) error {
		reviewPrefix = prefix
		return nil
	}}
	r.phases.finalize = testFinalizePhase{runFunc: func(context.Context) error {
		finalizeCalled = true
		return nil
	}}

	err := r.Run(t.Context())

	require.NoError(t, err)
	assert.Contains(t, reviewPrefix, "fix: address code review findings")
	assert.True(t, finalizeCalled)
}

func TestRunner_CodexAndPostReview_CommitPendingPrefix(t *testing.T) {
	t.Run("prefix applied when external review had findings", func(t *testing.T) {
		log := newRunnerMockLogger("progress.txt")

		var capturedPrompts []string
		claude := &mocks.ExecutorMock{
			RunFunc: func(_ context.Context, prompt string) executor.Result {
				capturedPrompts = append(capturedPrompts, prompt)
				switch len(capturedPrompts) {
				case 1: // codex eval iter 1 — findings fixed
					return executor.Result{Output: "fixed issues"}
				case 2: // codex eval iter 2 — no more findings
					return executor.Result{Output: "done", Signal: status.CodexDone}
				case 3: // post-codex review loop
					return executor.Result{Output: "review done", Signal: status.ReviewDone}
				default:
					return executor.Result{Error: errors.New("unexpected call")}
				}
			},
		}
		codex := newMockExecutor([]executor.Result{
			{Output: "found issue"},     // codex iteration 1
			{Output: "no issues found"}, // codex iteration 2
		})

		cfg := Config{
			Mode: ModeCodexOnly, MaxIterations: 50, IterationDelayMs: 1,
			CodexEnabled: true, AppConfig: testAppConfig(t),
		}
		r := NewWithExecutors(cfg, log, Executors{Task: claude, External: codex}, &status.PhaseHolder{})
		err := r.Run(t.Context())

		require.NoError(t, err)
		require.Len(t, capturedPrompts, 3)
		assert.Contains(t, capturedPrompts[2], "IMPORTANT: Before starting the review, run `git status`")
		assert.Contains(t, capturedPrompts[2], "fix: address code review findings")
	})

	t.Run("no prefix when external review disabled", func(t *testing.T) {
		log := newRunnerMockLogger("progress.txt")

		// codex disabled → no findings → post-codex review skipped → claude not called
		claude := newMockExecutor(nil)
		codex := newMockExecutor(nil)

		cfg := Config{Mode: ModeCodexOnly, MaxIterations: 50, CodexEnabled: false, AppConfig: testAppConfig(t)}
		r := NewWithExecutors(cfg, log, Executors{Task: claude, External: codex}, &status.PhaseHolder{})
		err := r.Run(t.Context())

		require.NoError(t, err)
		assert.Empty(t, claude.RunCalls(), "claude should not be called when codex disabled")
	})
}

func TestRunner_PlanModePreservesRejectedPlanSentinel(t *testing.T) {
	r := NewWithExecutors(
		Config{Mode: ModePlan, AppConfig: testAppConfig(t)},
		newRunnerMockLogger("progress.txt"),
		Executors{},
		&status.PhaseHolder{},
	)
	r.phases.planCreation = testPlanCreationPhase{runFunc: func(_ context.Context) error {
		return ErrUserRejectedPlan
	}}

	err := r.Run(t.Context())

	require.Error(t, err)
	assert.Equal(t, ErrUserRejectedPlan, err)
}

func TestRunner_SetInputCollector_ReachesConcretePlanPhase(t *testing.T) {
	log := newRunnerMockLogger("progress.txt")
	exec := newMockExecutor([]executor.Result{
		{Output: `<<<RALPHEX:QUESTION>>>
{"question":"Choose storage","options":["sqlite","postgres"]}
<<<RALPHEX:END>>>`},
		{Signal: status.PlanReady},
	})
	collector := &mocks.InputCollectorMock{
		AskQuestionFunc: func(_ context.Context, question string, options []string) (string, error) {
			assert.Equal(t, "Choose storage", question)
			assert.Equal(t, []string{"sqlite", "postgres"}, options)
			return "sqlite", nil
		},
		AskDraftReviewFunc: func(_ context.Context, _ string, _ string) (string, string, error) {
			return "accept", "", nil
		},
	}

	cfg := Config{Mode: ModePlan, PlanDescription: "create plan", MaxIterations: 5, AppConfig: testAppConfig(t)}
	r := NewWithExecutors(cfg, log, Executors{Task: exec}, &status.PhaseHolder{})
	r.SetInputCollector(collector)

	err := r.Run(t.Context())

	require.NoError(t, err)
	assert.Len(t, collector.AskQuestionCalls(), 1)
}

func TestRunner_SetPauseHandler_ReachesConcreteTaskPhase(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n### Task 1: first\n- [ ] todo"), 0o600))

	breakCh := make(chan struct{}, 1)
	pauseCalled := make(chan struct{}, 1)
	exec := &mocks.ExecutorMock{RunFunc: func(ctx context.Context, _ string) executor.Result {
		breakCh <- struct{}{}
		<-ctx.Done()
		return executor.Result{Error: ctx.Err()}
	}}

	cfg := Config{Mode: ModeTasksOnly, PlanFile: planFile, MaxIterations: 5, AppConfig: testAppConfig(t)}
	r := NewWithExecutors(cfg, newRunnerMockLogger("progress.txt"), Executors{Task: exec}, &status.PhaseHolder{})
	r.SetBreakCh(breakCh)
	r.SetPauseHandler(func(context.Context) bool {
		pauseCalled <- struct{}{}
		return false
	})

	err := r.Run(t.Context())

	require.ErrorIs(t, err, ErrUserAborted)
	assert.Len(t, pauseCalled, 1)
}

func TestRunner_SleepWithContext_CancelDuringDelay(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n### Task 1: first\n- [ ] todo"), 0o600))

	// use a long iteration delay to make the difference obvious
	const longDelay = 5000 // 5 seconds

	// executor returns no signal (no completion), so runner will loop and hit sleepWithContext
	var cancel context.CancelFunc
	claude := &mocks.ExecutorMock{RunFunc: func(_ context.Context, _ string) executor.Result {
		cancel()
		return executor.Result{Output: "working on it"}
	}}
	codex := newMockExecutor(nil)
	log := newRunnerMockLogger("progress.txt")

	cfg := Config{
		Mode:             ModeFull,
		PlanFile:         planFile,
		MaxIterations:    50,
		IterationDelayMs: longDelay,
		AppConfig:        testAppConfig(t),
	}
	r := NewWithExecutors(cfg, log, Executors{Task: claude, External: codex}, &status.PhaseHolder{})

	ctx, cancelFunc := context.WithCancel(t.Context())
	cancel = cancelFunc
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("runner did not exit promptly on cancellation")
	}
}

func TestRunner_FullMode_ErrUserAborted_SkipsReview(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n### Task 1: first\n- [ ] todo"), 0o600))

	log := newRunnerMockLogger("progress.txt")

	claude := newMockExecutor(nil) // should not be called for review
	codex := newMockExecutor(nil)  // should not be called

	cfg := Config{
		Mode: ModeFull, MaxIterations: 50, CodexEnabled: true,
		PlanFile: planFile, AppConfig: testAppConfig(t),
	}
	r := NewWithExecutors(cfg, log, Executors{Task: claude, External: codex}, &status.PhaseHolder{})
	taskPhase := testTaskPhase{runFunc: func(_ context.Context) error {
		return ErrUserAborted
	}}
	r.phases.task = taskPhase
	r.phases.taskValidator = taskPhase

	err := r.Run(t.Context())
	require.ErrorIs(t, err, ErrUserAborted, "ErrUserAborted should propagate to caller")

	// verify no executor was called (task phase was overridden, review phase skipped)
	assert.Empty(t, claude.RunCalls(), "claude should not run when task phase aborts")
	assert.Empty(t, codex.RunCalls(), "codex should not run when task phase aborts")

	// verify abort log message
	var foundAbort bool
	for _, call := range log.PrintCalls() {
		if strings.Contains(call.Format, "aborted by user") {
			foundAbort = true
			break
		}
	}
	assert.True(t, foundAbort, "should log abort message")
}

func TestRunner_TasksOnly_ErrUserAborted_CleanExit(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n### Task 1: first\n- [ ] todo"), 0o600))

	log := newRunnerMockLogger("progress.txt")

	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)

	cfg := Config{
		Mode: ModeTasksOnly, MaxIterations: 50,
		PlanFile: planFile, AppConfig: testAppConfig(t),
	}
	r := NewWithExecutors(cfg, log, Executors{Task: claude, External: codex}, &status.PhaseHolder{})
	taskPhase := testTaskPhase{runFunc: func(_ context.Context) error {
		return ErrUserAborted
	}}
	r.phases.task = taskPhase
	r.phases.taskValidator = taskPhase

	err := r.Run(t.Context())
	require.ErrorIs(t, err, ErrUserAborted, "ErrUserAborted should propagate to caller in tasks-only mode")
}

func TestRunner_ReviewClaude_UsedForReviewPhases(t *testing.T) {
	log := newRunnerMockLogger("progress.txt")

	// task executor should NOT be called in review-only mode
	taskClaude := newMockExecutor(nil)

	// review executor handles all review phases
	reviewClaude := newMockExecutor([]executor.Result{
		{Output: "review done", Signal: status.ReviewDone}, // first review
		{Output: "review done", Signal: status.ReviewDone}, // pre-codex review loop
		{Output: "fixed issues"},                           // codex eval iter 1 — findings fixed
		{Output: "done", Signal: status.CodexDone},         // codex eval iter 2 — no more findings
		{Output: "review done", Signal: status.ReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Output: "found issue"},     // codex iteration 1 — findings trigger eval + post-codex review
		{Output: "no issues found"}, // codex iteration 2 — clean
	})

	cfg := Config{Mode: ModeReview, MaxIterations: 50, IterationDelayMs: 1, CodexEnabled: true, AppConfig: testAppConfig(t)}
	r := NewWithExecutors(cfg, log, Executors{
		Task: taskClaude, Review: reviewClaude, External: codex,
	}, &status.PhaseHolder{})
	err := r.Run(t.Context())

	require.NoError(t, err)
	assert.Empty(t, taskClaude.RunCalls(), "task executor should not be called in review-only mode")
	assert.Len(t, reviewClaude.RunCalls(), 5, "review executor should handle all review phases including codex eval")
}

func TestRunner_ReviewClaude_NilFallsBackToTaskExecutor(t *testing.T) {
	log := newRunnerMockLogger("progress.txt")

	// when Review is nil, all review calls should go to Task executor
	claude := newMockExecutor([]executor.Result{
		{Output: "review done", Signal: status.ReviewDone}, // first review
		{Output: "review done", Signal: status.ReviewDone}, // pre-codex review loop
		{Output: "fixed issues"},                           // codex eval iter 1 — findings fixed
		{Output: "done", Signal: status.CodexDone},         // codex eval iter 2 — no more findings
		{Output: "review done", Signal: status.ReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Output: "found issue"},     // codex iteration 1 — findings trigger eval + post-codex review
		{Output: "no issues found"}, // codex iteration 2 — clean
	})

	cfg := Config{Mode: ModeReview, MaxIterations: 50, IterationDelayMs: 1, CodexEnabled: true, AppConfig: testAppConfig(t)}
	r := NewWithExecutors(cfg, log, Executors{
		Task: claude, Review: nil, External: codex,
	}, &status.PhaseHolder{})
	err := r.Run(t.Context())

	require.NoError(t, err)
	assert.Len(t, claude.RunCalls(), 5, "task executor should handle all review phases when ReviewClaude is nil")
}

func TestRunner_ReviewPromptIsSharedAcrossExecutors(t *testing.T) {
	// both claude and codex executors read the same ReviewFirstPrompt / ReviewSecondPrompt.
	// per-executor invocation syntax is handled by formatAgentExpansion at expansion time,
	// not by maintaining duplicate prompt files.
	tests := []struct {
		name     string
		executor string
	}{
		{name: "claude executor", executor: config.ExecutorClaude},
		{name: "codex executor", executor: config.ExecutorCodex},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			appCfg := testAppConfig(t)
			appCfg.Executor = tc.executor
			appCfg.ReviewFirstPrompt = "FIRST_REVIEW_PROMPT"
			appCfg.ReviewSecondPrompt = "SECOND_REVIEW_PROMPT"

			log := newRunnerMockLogger("progress.txt")
			task := newMockExecutor([]executor.Result{
				{Output: "review done", Signal: status.ReviewDone}, // first review
				{Output: "review done", Signal: status.ReviewDone}, // pre-external review loop
				{Output: "done", Signal: status.CodexDone},         // claude eval (only hit in claude mode)
			})
			external := newMockExecutor([]executor.Result{
				{Output: ""}, // empty output → external loop exits without claude eval (claude mode only)
			})

			cfg := Config{
				Mode: ModeReview, MaxIterations: 50, IterationDelayMs: 1,
				CodexEnabled: tc.executor == config.ExecutorClaude, AppConfig: appCfg,
			}
			r := NewWithExecutors(cfg, log,
				Executors{Task: task, External: external},
				&status.PhaseHolder{})
			require.NoError(t, r.Run(t.Context()))

			calls := task.RunCalls()
			require.GreaterOrEqual(t, len(calls), 2, "expected at least 2 review calls")
			assert.Contains(t, calls[0].Prompt, "FIRST_REVIEW_PROMPT", "first review must use shared prompt")
			assert.Contains(t, calls[1].Prompt, "SECOND_REVIEW_PROMPT", "second review must use shared prompt")
		})
	}
}

// testAppConfig loads config with embedded defaults for testing.
func testAppConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load(t.TempDir())
	require.NoError(t, err)
	return cfg
}

// newMockLogger creates a moq-generated logger mock with no-op implementations.
func newMockLogger() *mocks.LoggerMock {
	return &mocks.LoggerMock{
		PrintFunc:          func(_ string, _ ...any) {},
		PrintRawFunc:       func(_ string, _ ...any) {},
		PrintSectionFunc:   func(_ status.Section) {},
		PrintAlignedFunc:   func(_ string) {},
		LogQuestionFunc:    func(_ string, _ []string) {},
		LogAnswerFunc:      func(_ string) {},
		LogDraftReviewFunc: func(_, _ string) {},
		PathFunc:           func() string { return "" },
	}
}

func assertLogContains(t *testing.T, log *mocks.LoggerMock, text string) {
	t.Helper()
	for _, call := range log.PrintCalls() {
		if strings.Contains(call.Format, text) {
			return
		}
	}
	t.Fatalf("expected log containing %q, got %#v", text, log.PrintCalls())
}
