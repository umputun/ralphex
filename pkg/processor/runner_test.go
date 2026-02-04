package processor_test

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
	"github.com/umputun/ralphex/pkg/processor"
	"github.com/umputun/ralphex/pkg/processor/mocks"
)

// testAppConfig loads config with embedded defaults for testing.
func testAppConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load(t.TempDir())
	require.NoError(t, err)
	return cfg
}

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
func newMockLogger(path string) *mocks.LoggerMock {
	return &mocks.LoggerMock{
		SetPhaseFunc:       func(_ processor.Phase) {},
		PrintFunc:          func(_ string, _ ...any) {},
		PrintRawFunc:       func(_ string, _ ...any) {},
		PrintSectionFunc:   func(_ processor.Section) {},
		PrintAlignedFunc:   func(_ string) {},
		LogQuestionFunc:    func(_ string, _ []string) {},
		LogAnswerFunc:      func(_ string) {},
		LogDraftReviewFunc: func(_, _ string) {},
		PathFunc:           func() string { return path },
	}
}

func TestRunner_Run_UnknownMode(t *testing.T) {
	log := newMockLogger("")
	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)

	r := processor.NewWithExecutors(processor.Config{Mode: "invalid"}, log, claude, codex)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown mode")
}

func TestRunner_RunFull_NoPlanFile(t *testing.T) {
	log := newMockLogger("")
	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)

	r := processor.NewWithExecutors(processor.Config{Mode: processor.ModeFull}, log, claude, codex)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "plan file required")
}

func TestRunner_RunFull_Success(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	// all tasks complete - no [ ] checkboxes
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n- [x] Task 1"), 0o600))

	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "task done", Signal: processor.SignalCompleted},    // task phase completes
		{Output: "review done", Signal: processor.SignalReviewDone}, // first review
		{Output: "review done", Signal: processor.SignalReviewDone}, // pre-codex review loop
		{Output: "done", Signal: processor.SignalCodexDone},         // codex evaluation
		{Output: "review done", Signal: processor.SignalReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Output: "found issue in foo.go"}, // codex finds issues
	})

	cfg := processor.Config{Mode: processor.ModeFull, PlanFile: planFile, MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.NoError(t, err)
	assert.Len(t, codex.RunCalls(), 1)
}

func TestRunner_RunFull_NoCodexFindings(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n- [x] Task 1"), 0o600))

	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "task done", Signal: processor.SignalCompleted},
		{Output: "review done", Signal: processor.SignalReviewDone}, // first review
		{Output: "review done", Signal: processor.SignalReviewDone}, // pre-codex review loop
		{Output: "review done", Signal: processor.SignalReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Output: ""}, // codex finds nothing
	})

	cfg := processor.Config{Mode: processor.ModeFull, PlanFile: planFile, MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.NoError(t, err)
}

func TestRunner_RunReviewOnly_Success(t *testing.T) {
	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "review done", Signal: processor.SignalReviewDone}, // first review
		{Output: "review done", Signal: processor.SignalReviewDone}, // pre-codex review loop
		{Output: "done", Signal: processor.SignalCodexDone},         // codex evaluation
		{Output: "review done", Signal: processor.SignalReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Output: "found issue"},
	})

	cfg := processor.Config{Mode: processor.ModeReview, MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.NoError(t, err)
	assert.Len(t, codex.RunCalls(), 1)
}

func TestRunner_RunCodexOnly_Success(t *testing.T) {
	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "done", Signal: processor.SignalCodexDone},         // codex evaluation
		{Output: "review done", Signal: processor.SignalReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Output: "found issue"},
	})

	cfg := processor.Config{Mode: processor.ModeCodexOnly, MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.NoError(t, err)
	assert.Len(t, codex.RunCalls(), 1)
}

func TestRunner_RunCodexOnly_NoFindings(t *testing.T) {
	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "review done", Signal: processor.SignalReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Output: ""}, // no findings
	})

	cfg := processor.Config{Mode: processor.ModeCodexOnly, MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.NoError(t, err)
}

func TestRunner_CodexDisabled_SkipsCodexPhase(t *testing.T) {
	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "review done", Signal: processor.SignalReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor(nil)

	cfg := processor.Config{Mode: processor.ModeCodexOnly, MaxIterations: 50, CodexEnabled: false, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.NoError(t, err)
	assert.Empty(t, codex.RunCalls(), "codex should not be called when disabled")
}

func TestRunner_RunTasksOnly_Success(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	// all tasks complete - no [ ] checkboxes
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n- [x] Task 1"), 0o600))

	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "task done", Signal: processor.SignalCompleted}, // task phase completes
	})
	codex := newMockExecutor(nil)

	cfg := processor.Config{Mode: processor.ModeTasksOnly, PlanFile: planFile, MaxIterations: 50, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.NoError(t, err)
	assert.Empty(t, codex.RunCalls(), "codex should not be called in tasks-only mode")
	assert.Len(t, claude.RunCalls(), 1)
}

func TestRunner_RunTasksOnly_NoPlanFile(t *testing.T) {
	log := newMockLogger("")
	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)

	r := processor.NewWithExecutors(processor.Config{Mode: processor.ModeTasksOnly}, log, claude, codex)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "plan file required")
}

func TestRunner_RunTasksOnly_TaskPhaseError(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n- [ ] Task 1"), 0o600))

	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "error", Signal: processor.SignalFailed}, // first try
		{Output: "error", Signal: processor.SignalFailed}, // retry
	})
	codex := newMockExecutor(nil)

	cfg := processor.Config{Mode: processor.ModeTasksOnly, PlanFile: planFile, MaxIterations: 10, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "FAILED signal")
}

func TestRunner_RunTasksOnly_NoReviews(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n- [x] Task 1\n- [x] Task 2"), 0o600))

	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "task done", Signal: processor.SignalCompleted},
	})
	codex := newMockExecutor(nil)

	cfg := processor.Config{
		Mode:          processor.ModeTasksOnly,
		PlanFile:      planFile,
		MaxIterations: 50,
		CodexEnabled:  true, // enabled but should not run in tasks-only mode
		AppConfig:     testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.NoError(t, err)
	// verify no review or codex phases ran - only task phase
	assert.Len(t, claude.RunCalls(), 1, "only task phase should run")
	assert.Empty(t, codex.RunCalls(), "codex should not run in tasks-only mode")
}

func TestRunner_TaskPhase_FailedSignal(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "error", Signal: processor.SignalFailed}, // first try
		{Output: "error", Signal: processor.SignalFailed}, // retry
	})
	codex := newMockExecutor(nil)

	cfg := processor.Config{Mode: processor.ModeFull, PlanFile: planFile, MaxIterations: 10, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "FAILED signal")
}

func TestRunner_TaskPhase_MaxIterations(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n- [ ] Task 1"), 0o600))

	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "working..."},
		{Output: "still working..."},
		{Output: "more work..."},
	})
	codex := newMockExecutor(nil)

	cfg := processor.Config{Mode: processor.ModeFull, PlanFile: planFile, MaxIterations: 3, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "max iterations")
}

func TestRunner_TaskPhase_ContextCanceled(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	log := newMockLogger("progress.txt")
	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)

	cfg := processor.Config{Mode: processor.ModeFull, PlanFile: planFile, MaxIterations: 10, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(ctx)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestRunner_ClaudeReview_FailedSignal(t *testing.T) {
	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "error", Signal: processor.SignalFailed},
	})
	codex := newMockExecutor(nil)

	cfg := processor.Config{Mode: processor.ModeReview, MaxIterations: 50, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "FAILED signal")
}

func TestRunner_CodexPhase_Error(t *testing.T) {
	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "review done", Signal: processor.SignalReviewDone}, // first review
		{Output: "review done", Signal: processor.SignalReviewDone}, // pre-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Error: errors.New("codex error")},
	})

	cfg := processor.Config{Mode: processor.ModeReview, MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "codex")
	assert.Len(t, codex.RunCalls(), 1, "codex should be called once")
}

func TestRunner_ClaudeExecution_Error(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Error: errors.New("claude error")},
	})
	codex := newMockExecutor(nil)

	cfg := processor.Config{Mode: processor.ModeFull, PlanFile: planFile, MaxIterations: 10, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "claude execution")
}

func TestRunner_ConfigValues(t *testing.T) {
	tests := []struct {
		name               string
		iterationDelayMs   int
		taskRetryCount     int
		expectedDelay      time.Duration
		expectedRetryCount int
	}{
		{
			name:               "default values",
			iterationDelayMs:   0,
			taskRetryCount:     0,
			expectedDelay:      processor.DefaultIterationDelay,
			expectedRetryCount: 1,
		},
		{
			name:               "custom delay",
			iterationDelayMs:   500,
			taskRetryCount:     0,
			expectedDelay:      500 * time.Millisecond,
			expectedRetryCount: 1,
		},
		{
			name:               "custom retry count",
			iterationDelayMs:   0,
			taskRetryCount:     3,
			expectedDelay:      processor.DefaultIterationDelay,
			expectedRetryCount: 3,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			log := newMockLogger("")
			claude := newMockExecutor(nil)
			codex := newMockExecutor(nil)

			cfg := processor.Config{
				IterationDelayMs: tc.iterationDelayMs,
				TaskRetryCount:   tc.taskRetryCount,
			}
			r := processor.NewWithExecutors(cfg, log, claude, codex)

			testCfg := r.TestConfig()
			assert.Equal(t, tc.expectedDelay, testCfg.IterationDelay)
			assert.Equal(t, tc.expectedRetryCount, testCfg.TaskRetryCount)
		})
	}
}

func TestRunner_HasUncompletedTasks(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected bool
	}{
		{
			name:     "all tasks completed",
			content:  "# Plan\n- [x] Task 1\n- [x] Task 2",
			expected: false,
		},
		{
			name:     "has uncompleted task",
			content:  "# Plan\n- [x] Task 1\n- [ ] Task 2",
			expected: true,
		},
		{
			name:     "no checkboxes",
			content:  "# Plan\nJust some text",
			expected: false,
		},
		{
			name:     "uncompleted in nested list",
			content:  "# Plan\n- [x] Task 1\n  - [ ] Subtask",
			expected: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			planFile := filepath.Join(tmpDir, "plan.md")
			require.NoError(t, os.WriteFile(planFile, []byte(tc.content), 0o600))

			log := newMockLogger("")
			claude := newMockExecutor(nil)
			codex := newMockExecutor(nil)

			cfg := processor.Config{PlanFile: planFile}
			r := processor.NewWithExecutors(cfg, log, claude, codex)

			assert.Equal(t, tc.expected, r.TestHasUncompletedTasks())
		})
	}
}

func TestRunner_HasUncompletedTasks_CompletedDir(t *testing.T) {
	tmpDir := t.TempDir()
	plansDir := filepath.Join(tmpDir, "docs", "plans")
	completedDir := filepath.Join(plansDir, "completed")
	require.NoError(t, os.MkdirAll(completedDir, 0o700))

	// file is in completed/, but config references original path
	originalPath := filepath.Join(plansDir, "plan.md")
	completedPath := filepath.Join(completedDir, "plan.md")
	require.NoError(t, os.WriteFile(completedPath, []byte("# Plan\n- [ ] Task 1"), 0o600))

	log := newMockLogger("")
	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)

	cfg := processor.Config{PlanFile: originalPath}
	r := processor.NewWithExecutors(cfg, log, claude, codex)

	assert.True(t, r.TestHasUncompletedTasks())
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

	log := newMockLogger("")
	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)

	cfg := processor.Config{PlanFile: originalPath}
	r := processor.NewWithExecutors(cfg, log, claude, codex)

	prompt := r.TestBuildCodexPrompt(true, "")

	assert.Contains(t, prompt, completedPath)
	assert.NotContains(t, prompt, originalPath)
}

func TestRunner_TaskRetryCount_UsedCorrectly(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n- [ ] Task 1"), 0o600))

	log := newMockLogger("progress.txt")

	// test with TaskRetryCount=2 - should retry twice before failing
	claude := newMockExecutor([]executor.Result{
		{Output: "error", Signal: processor.SignalFailed}, // first try
		{Output: "error", Signal: processor.SignalFailed}, // retry 1
		{Output: "error", Signal: processor.SignalFailed}, // retry 2
	})
	codex := newMockExecutor(nil)

	cfg := processor.Config{
		Mode:           processor.ModeFull,
		PlanFile:       planFile,
		MaxIterations:  10,
		TaskRetryCount: 2,
		// use 1ms delay for faster tests
		IterationDelayMs: 1,
		AppConfig:        testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "FAILED signal")
	// should have tried 3 times: initial + 2 retries
	assert.Len(t, claude.RunCalls(), 3)
}

// newMockInputCollector creates a mock input collector with predefined answers.
func newMockInputCollector(answers []string) *mocks.InputCollectorMock {
	idx := 0
	return &mocks.InputCollectorMock{
		AskQuestionFunc: func(_ context.Context, _ string, _ []string) (string, error) {
			if idx >= len(answers) {
				return "", errors.New("no more mock answers")
			}
			answer := answers[idx]
			idx++
			return answer, nil
		},
	}
}

func TestRunner_RunPlan_Success(t *testing.T) {
	log := newMockLogger("progress-plan.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "plan created", Signal: processor.SignalPlanReady},
	})
	codex := newMockExecutor(nil)
	inputCollector := newMockInputCollector(nil)

	cfg := processor.Config{
		Mode:             processor.ModePlan,
		PlanDescription:  "add health check endpoint",
		MaxIterations:    50,
		IterationDelayMs: 1,
		AppConfig:        testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	r.SetInputCollector(inputCollector)
	err := r.Run(context.Background())

	require.NoError(t, err)
	assert.Len(t, claude.RunCalls(), 1)
}

func TestRunner_RunPlan_WithQuestion(t *testing.T) {
	log := newMockLogger("progress-plan.txt")
	questionSignal := `Let me ask a question.

<<<RALPHEX:QUESTION>>>
{"question": "Which cache backend?", "options": ["Redis", "In-memory", "File-based"]}
<<<RALPHEX:END>>>`

	claude := newMockExecutor([]executor.Result{
		{Output: questionSignal},                                    // first iteration - asks question
		{Output: "plan created", Signal: processor.SignalPlanReady}, // second iteration - completes
	})
	codex := newMockExecutor(nil)
	inputCollector := newMockInputCollector([]string{"Redis"})

	cfg := processor.Config{
		Mode:             processor.ModePlan,
		PlanDescription:  "add caching layer",
		MaxIterations:    50,
		IterationDelayMs: 1,
		AppConfig:        testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	r.SetInputCollector(inputCollector)
	err := r.Run(context.Background())

	require.NoError(t, err)
	assert.Len(t, claude.RunCalls(), 2)
	assert.Len(t, inputCollector.AskQuestionCalls(), 1)
	assert.Equal(t, "Which cache backend?", inputCollector.AskQuestionCalls()[0].Question)
	assert.Equal(t, []string{"Redis", "In-memory", "File-based"}, inputCollector.AskQuestionCalls()[0].Options)
}

func TestRunner_RunPlan_NoPlanDescription(t *testing.T) {
	log := newMockLogger("")
	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)
	inputCollector := newMockInputCollector(nil)

	cfg := processor.Config{Mode: processor.ModePlan, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	r.SetInputCollector(inputCollector)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "plan description required")
}

func TestRunner_RunPlan_NoInputCollector(t *testing.T) {
	log := newMockLogger("")
	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)

	cfg := processor.Config{Mode: processor.ModePlan, PlanDescription: "test", AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	// don't set input collector
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "input collector required")
}

func TestRunner_RunPlan_FailedSignal(t *testing.T) {
	log := newMockLogger("progress-plan.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "error", Signal: processor.SignalFailed},
	})
	codex := newMockExecutor(nil)
	inputCollector := newMockInputCollector(nil)

	cfg := processor.Config{
		Mode:             processor.ModePlan,
		PlanDescription:  "test",
		MaxIterations:    50,
		IterationDelayMs: 1,
		AppConfig:        testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	r.SetInputCollector(inputCollector)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "FAILED signal")
}

func TestRunner_RunPlan_MaxIterations(t *testing.T) {
	log := newMockLogger("progress-plan.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "exploring..."},
		{Output: "still exploring..."},
		{Output: "more exploring..."},
		{Output: "continuing..."},
		{Output: "still going..."},
	})
	codex := newMockExecutor(nil)
	inputCollector := newMockInputCollector(nil)

	// maxPlanIterations = max(5, 10/5) = 5
	cfg := processor.Config{
		Mode:             processor.ModePlan,
		PlanDescription:  "test",
		MaxIterations:    10,
		IterationDelayMs: 1,
		AppConfig:        testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	r.SetInputCollector(inputCollector)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "max plan iterations")
}

func TestRunner_RunPlan_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	log := newMockLogger("")
	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)
	inputCollector := newMockInputCollector(nil)

	cfg := processor.Config{
		Mode:             processor.ModePlan,
		PlanDescription:  "test",
		MaxIterations:    50,
		IterationDelayMs: 1,
		AppConfig:        testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	r.SetInputCollector(inputCollector)
	err := r.Run(ctx)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestRunner_RunPlan_ClaudeError(t *testing.T) {
	log := newMockLogger("progress-plan.txt")
	claude := newMockExecutor([]executor.Result{
		{Error: errors.New("claude error")},
	})
	codex := newMockExecutor(nil)
	inputCollector := newMockInputCollector(nil)

	cfg := processor.Config{
		Mode:             processor.ModePlan,
		PlanDescription:  "test",
		MaxIterations:    50,
		IterationDelayMs: 1,
		AppConfig:        testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	r.SetInputCollector(inputCollector)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "claude execution")
}

func TestRunner_RunPlan_InputCollectorError(t *testing.T) {
	log := newMockLogger("progress-plan.txt")
	questionSignal := `<<<RALPHEX:QUESTION>>>
{"question": "Which backend?", "options": ["A", "B"]}
<<<RALPHEX:END>>>`

	claude := newMockExecutor([]executor.Result{
		{Output: questionSignal},
	})
	codex := newMockExecutor(nil)
	inputCollector := &mocks.InputCollectorMock{
		AskQuestionFunc: func(_ context.Context, _ string, _ []string) (string, error) {
			return "", errors.New("input error")
		},
	}

	cfg := processor.Config{
		Mode:             processor.ModePlan,
		PlanDescription:  "test",
		MaxIterations:    50,
		IterationDelayMs: 1,
		AppConfig:        testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	r.SetInputCollector(inputCollector)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "collect answer")
}

func TestRunner_New_CodexNotInstalled_AutoDisables(t *testing.T) {
	log := newMockLogger("progress.txt")

	appCfg := testAppConfig(t)
	appCfg.CodexCommand = "/nonexistent/path/to/codex" // command that doesn't exist

	cfg := processor.Config{
		Mode:          processor.ModeCodexOnly,
		MaxIterations: 50,
		CodexEnabled:  true,
		AppConfig:     appCfg,
	}

	// use processor.New (not NewWithExecutors) to trigger LookPath check
	r := processor.New(cfg, log)

	// verify warning was logged with error details
	var foundWarning bool
	for _, call := range log.PrintCalls() {
		// format includes %v for error, so check format string
		if strings.Contains(call.Format, "codex not found") && strings.Contains(call.Format, "%v") {
			foundWarning = true
			break
		}
	}
	assert.True(t, foundWarning, "should log warning about codex not found with error details")

	// verify runner was created (auto-disable happens at construction time)
	assert.NotNil(t, r, "runner should be created even when codex not found")
}

func TestRunner_ErrorPatternMatch_ClaudeInTaskPhase(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n- [ ] Task 1"), 0o600))

	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "You've hit your limit", Error: &executor.PatternMatchError{Pattern: "You've hit your limit", HelpCmd: "claude /usage"}},
	})
	codex := newMockExecutor(nil)

	cfg := processor.Config{Mode: processor.ModeFull, PlanFile: planFile, MaxIterations: 10, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

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

func TestRunner_ErrorPatternMatch_CodexInReviewPhase(t *testing.T) {
	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "review done", Signal: processor.SignalReviewDone}, // first review
		{Output: "review done", Signal: processor.SignalReviewDone}, // pre-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Output: "Rate limit exceeded", Error: &executor.PatternMatchError{Pattern: "rate limit", HelpCmd: "codex /status"}},
	})

	cfg := processor.Config{Mode: processor.ModeReview, MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.Error(t, err)
	var patternErr *executor.PatternMatchError
	require.ErrorAs(t, err, &patternErr)
	assert.Equal(t, "rate limit", patternErr.Pattern)
	assert.Equal(t, "codex /status", patternErr.HelpCmd)

	// verify logging mentions codex
	var foundErrorLog bool
	for _, call := range log.PrintCalls() {
		if strings.Contains(call.Format, "error: detected") && strings.Contains(call.Format, "%s output") {
			foundErrorLog = true
		}
	}
	assert.True(t, foundErrorLog, "should log error message with codex")
}

func TestRunner_ErrorPatternMatch_ClaudeInReviewLoop(t *testing.T) {
	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "review done", Signal: processor.SignalReviewDone},                                                     // first review
		{Output: "rate limited", Error: &executor.PatternMatchError{Pattern: "rate limited", HelpCmd: "claude /usage"}}, // review loop hits rate limit
	})
	codex := newMockExecutor(nil)

	cfg := processor.Config{Mode: processor.ModeReview, MaxIterations: 50, CodexEnabled: false, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.Error(t, err)
	var patternErr *executor.PatternMatchError
	require.ErrorAs(t, err, &patternErr)
	assert.Equal(t, "rate limited", patternErr.Pattern)
	assert.Equal(t, "claude /usage", patternErr.HelpCmd)
}

func TestRunner_ErrorPatternMatch_ClaudeInPlanCreation(t *testing.T) {
	log := newMockLogger("progress-plan.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "hit limit", Error: &executor.PatternMatchError{Pattern: "hit limit", HelpCmd: "claude /usage"}},
	})
	codex := newMockExecutor(nil)
	inputCollector := newMockInputCollector(nil)

	cfg := processor.Config{
		Mode:             processor.ModePlan,
		PlanDescription:  "test",
		MaxIterations:    50,
		IterationDelayMs: 1,
		AppConfig:        testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	r.SetInputCollector(inputCollector)
	err := r.Run(context.Background())

	require.Error(t, err)
	var patternErr *executor.PatternMatchError
	require.ErrorAs(t, err, &patternErr)
}

// newMockInputCollectorWithDraftReview creates a mock input collector with predefined answers and draft review responses.
func newMockInputCollectorWithDraftReview(answers []string, draftResponses []struct {
	action   string
	feedback string
	err      error
}) *mocks.InputCollectorMock {
	answerIdx := 0
	draftIdx := 0
	return &mocks.InputCollectorMock{
		AskQuestionFunc: func(_ context.Context, _ string, _ []string) (string, error) {
			if answerIdx >= len(answers) {
				return "", errors.New("no more mock answers")
			}
			answer := answers[answerIdx]
			answerIdx++
			return answer, nil
		},
		AskDraftReviewFunc: func(_ context.Context, _ string, _ string) (string, string, error) {
			if draftIdx >= len(draftResponses) {
				return "", "", errors.New("no more mock draft responses")
			}
			resp := draftResponses[draftIdx]
			draftIdx++
			return resp.action, resp.feedback, resp.err
		},
	}
}

func TestRunner_RunPlan_PlanDraft_AcceptFlow(t *testing.T) {
	log := newMockLogger("progress-plan.txt")
	planDraftSignal := `Let me create a plan for you.

<<<RALPHEX:PLAN_DRAFT>>>
# Test Plan

## Overview
This is a test plan.

## Tasks
- [ ] Task 1
<<<RALPHEX:END>>>`

	claude := newMockExecutor([]executor.Result{
		{Output: planDraftSignal},                                   // first iteration - emits draft
		{Output: "plan created", Signal: processor.SignalPlanReady}, // second iteration - completes
	})
	codex := newMockExecutor(nil)
	inputCollector := newMockInputCollectorWithDraftReview(nil, []struct {
		action   string
		feedback string
		err      error
	}{
		{action: "accept", feedback: "", err: nil},
	})

	cfg := processor.Config{
		Mode:             processor.ModePlan,
		PlanDescription:  "add health endpoint",
		MaxIterations:    50,
		IterationDelayMs: 1,
		AppConfig:        testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	r.SetInputCollector(inputCollector)
	err := r.Run(context.Background())

	require.NoError(t, err)
	assert.Len(t, claude.RunCalls(), 2)
	assert.Len(t, inputCollector.AskDraftReviewCalls(), 1)
	assert.Contains(t, inputCollector.AskDraftReviewCalls()[0].PlanContent, "# Test Plan")
}

func TestRunner_RunPlan_PlanDraft_ReviseFlow(t *testing.T) {
	log := newMockLogger("progress-plan.txt")
	planDraftSignal := `<<<RALPHEX:PLAN_DRAFT>>>
# Initial Plan
## Tasks
- [ ] Task 1
<<<RALPHEX:END>>>`

	revisedDraftSignal := `<<<RALPHEX:PLAN_DRAFT>>>
# Revised Plan
## Tasks
- [ ] Task 1
- [ ] Task 2 (added per feedback)
<<<RALPHEX:END>>>`

	claude := newMockExecutor([]executor.Result{
		{Output: planDraftSignal},                                   // first iteration - initial draft
		{Output: revisedDraftSignal},                                // second iteration - revised draft
		{Output: "plan created", Signal: processor.SignalPlanReady}, // third iteration - completes
	})
	codex := newMockExecutor(nil)
	inputCollector := newMockInputCollectorWithDraftReview(nil, []struct {
		action   string
		feedback string
		err      error
	}{
		{action: "revise", feedback: "please add a second task", err: nil},
		{action: "accept", feedback: "", err: nil},
	})

	cfg := processor.Config{
		Mode:             processor.ModePlan,
		PlanDescription:  "add health endpoint",
		MaxIterations:    50,
		IterationDelayMs: 1,
		AppConfig:        testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	r.SetInputCollector(inputCollector)
	err := r.Run(context.Background())

	require.NoError(t, err)
	assert.Len(t, claude.RunCalls(), 3)
	assert.Len(t, inputCollector.AskDraftReviewCalls(), 2)

	// verify feedback was passed to claude in second call
	secondPrompt := claude.RunCalls()[1].Prompt
	assert.Contains(t, secondPrompt, "please add a second task")
	assert.Contains(t, secondPrompt, "PREVIOUS DRAFT FEEDBACK")
}

func TestRunner_RunPlan_PlanDraft_RejectFlow(t *testing.T) {
	log := newMockLogger("progress-plan.txt")
	planDraftSignal := `<<<RALPHEX:PLAN_DRAFT>>>
# Test Plan
## Tasks
- [ ] Task 1
<<<RALPHEX:END>>>`

	claude := newMockExecutor([]executor.Result{
		{Output: planDraftSignal}, // first iteration - emits draft
	})
	codex := newMockExecutor(nil)
	inputCollector := newMockInputCollectorWithDraftReview(nil, []struct {
		action   string
		feedback string
		err      error
	}{
		{action: "reject", feedback: "", err: nil},
	})

	cfg := processor.Config{
		Mode:             processor.ModePlan,
		PlanDescription:  "add health endpoint",
		MaxIterations:    50,
		IterationDelayMs: 1,
		AppConfig:        testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	r.SetInputCollector(inputCollector)
	err := r.Run(context.Background())

	require.Error(t, err)
	require.ErrorIs(t, err, processor.ErrUserRejectedPlan)
	assert.Len(t, claude.RunCalls(), 1)
	assert.Len(t, inputCollector.AskDraftReviewCalls(), 1)
}

func TestRunner_RunPlan_PlanDraft_AskDraftReviewError(t *testing.T) {
	log := newMockLogger("progress-plan.txt")
	planDraftSignal := `<<<RALPHEX:PLAN_DRAFT>>>
# Test Plan
<<<RALPHEX:END>>>`

	claude := newMockExecutor([]executor.Result{
		{Output: planDraftSignal},
	})
	codex := newMockExecutor(nil)
	inputCollector := newMockInputCollectorWithDraftReview(nil, []struct {
		action   string
		feedback string
		err      error
	}{
		{action: "", feedback: "", err: errors.New("draft review error")},
	})

	cfg := processor.Config{
		Mode:             processor.ModePlan,
		PlanDescription:  "test",
		MaxIterations:    50,
		IterationDelayMs: 1,
		AppConfig:        testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	r.SetInputCollector(inputCollector)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "collect draft review")
}

func TestRunner_RunPlan_PlanDraft_MalformedSignal(t *testing.T) {
	log := newMockLogger("progress-plan.txt")
	// malformed - missing END marker
	malformedDraftSignal := `<<<RALPHEX:PLAN_DRAFT>>>
# Test Plan
This content has no END marker`

	claude := newMockExecutor([]executor.Result{
		{Output: malformedDraftSignal},                              // first iteration - malformed draft
		{Output: "plan created", Signal: processor.SignalPlanReady}, // second iteration - completes anyway
	})
	codex := newMockExecutor(nil)
	inputCollector := newMockInputCollectorWithDraftReview(nil, nil)

	cfg := processor.Config{
		Mode:             processor.ModePlan,
		PlanDescription:  "test",
		MaxIterations:    50,
		IterationDelayMs: 1,
		AppConfig:        testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	r.SetInputCollector(inputCollector)
	err := r.Run(context.Background())

	require.NoError(t, err)
	// should log warning but continue
	var foundWarning bool
	for _, call := range log.PrintCalls() {
		if strings.Contains(call.Format, "warning") && strings.Contains(call.Format, "%v") {
			foundWarning = true
			break
		}
	}
	assert.True(t, foundWarning, "should log warning about malformed signal")
}

func TestRunner_RunPlan_PlanDraft_WithQuestionThenDraft(t *testing.T) {
	log := newMockLogger("progress-plan.txt")
	questionSignal := `<<<RALPHEX:QUESTION>>>
{"question": "Which framework?", "options": ["Gin", "Chi", "Echo"]}
<<<RALPHEX:END>>>`

	planDraftSignal := `<<<RALPHEX:PLAN_DRAFT>>>
# Plan with Gin
## Tasks
- [ ] Set up Gin router
<<<RALPHEX:END>>>`

	claude := newMockExecutor([]executor.Result{
		{Output: questionSignal},                                    // first iteration - question
		{Output: planDraftSignal},                                   // second iteration - draft
		{Output: "plan created", Signal: processor.SignalPlanReady}, // third iteration - completes
	})
	codex := newMockExecutor(nil)
	inputCollector := newMockInputCollectorWithDraftReview([]string{"Gin"}, []struct {
		action   string
		feedback string
		err      error
	}{
		{action: "accept", feedback: "", err: nil},
	})

	cfg := processor.Config{
		Mode:             processor.ModePlan,
		PlanDescription:  "add API endpoints",
		MaxIterations:    50,
		IterationDelayMs: 1,
		AppConfig:        testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	r.SetInputCollector(inputCollector)
	err := r.Run(context.Background())

	require.NoError(t, err)
	assert.Len(t, claude.RunCalls(), 3)
	assert.Len(t, inputCollector.AskQuestionCalls(), 1)
	assert.Len(t, inputCollector.AskDraftReviewCalls(), 1)
}

func TestRunner_Finalize_RunsWhenEnabled(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n- [x] Task 1"), 0o600))

	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "task done", Signal: processor.SignalCompleted},    // task phase
		{Output: "review done", Signal: processor.SignalReviewDone}, // first review
		{Output: "review done", Signal: processor.SignalReviewDone}, // pre-codex review loop
		{Output: "review done", Signal: processor.SignalReviewDone}, // post-codex review loop (codex disabled)
		{Output: "finalize done"},                                   // finalize step
	})
	codex := newMockExecutor(nil)

	cfg := processor.Config{
		Mode:            processor.ModeFull,
		PlanFile:        planFile,
		MaxIterations:   50,
		CodexEnabled:    false,
		FinalizeEnabled: true,
		AppConfig:       testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.NoError(t, err)
	// verify finalize step ran (5 claude calls total)
	assert.Len(t, claude.RunCalls(), 5)

	// verify finalize section was printed
	var foundFinalizeSection bool
	for _, call := range log.PrintSectionCalls() {
		if strings.Contains(call.Section.Label, "finalize") {
			foundFinalizeSection = true
			break
		}
	}
	assert.True(t, foundFinalizeSection, "should print finalize section header")
}

func TestRunner_Finalize_SkippedWhenDisabled(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n- [x] Task 1"), 0o600))

	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "task done", Signal: processor.SignalCompleted},    // task phase
		{Output: "review done", Signal: processor.SignalReviewDone}, // first review
		{Output: "review done", Signal: processor.SignalReviewDone}, // pre-codex review loop
		{Output: "review done", Signal: processor.SignalReviewDone}, // post-codex review loop (codex disabled)
	})
	codex := newMockExecutor(nil)

	cfg := processor.Config{
		Mode:            processor.ModeFull,
		PlanFile:        planFile,
		MaxIterations:   50,
		CodexEnabled:    false,
		FinalizeEnabled: false, // disabled
		AppConfig:       testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.NoError(t, err)
	// verify finalize step did NOT run (only 4 claude calls)
	assert.Len(t, claude.RunCalls(), 4)
}

func TestRunner_Finalize_FailureDoesNotBlockSuccess(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n- [x] Task 1"), 0o600))

	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "task done", Signal: processor.SignalCompleted},    // task phase
		{Output: "review done", Signal: processor.SignalReviewDone}, // first review
		{Output: "review done", Signal: processor.SignalReviewDone}, // pre-codex review loop
		{Output: "review done", Signal: processor.SignalReviewDone}, // post-codex review loop (codex disabled)
		{Error: errors.New("finalize error")},                       // finalize fails
	})
	codex := newMockExecutor(nil)

	cfg := processor.Config{
		Mode:            processor.ModeFull,
		PlanFile:        planFile,
		MaxIterations:   50,
		CodexEnabled:    false,
		FinalizeEnabled: true,
		AppConfig:       testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	// run should succeed despite finalize failure
	require.NoError(t, err)

	// verify finalize error was logged
	var foundErrorLog bool
	for _, call := range log.PrintCalls() {
		if strings.Contains(call.Format, "finalize step failed") {
			foundErrorLog = true
			break
		}
	}
	assert.True(t, foundErrorLog, "should log finalize failure")
}

func TestRunner_Finalize_FailedSignalDoesNotBlockSuccess(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n- [x] Task 1"), 0o600))

	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "task done", Signal: processor.SignalCompleted},    // task phase
		{Output: "review done", Signal: processor.SignalReviewDone}, // first review
		{Output: "review done", Signal: processor.SignalReviewDone}, // pre-codex review loop
		{Output: "review done", Signal: processor.SignalReviewDone}, // post-codex review loop (codex disabled)
		{Output: "failed", Signal: processor.SignalFailed},          // finalize reports FAILED signal
	})
	codex := newMockExecutor(nil)

	cfg := processor.Config{
		Mode:            processor.ModeFull,
		PlanFile:        planFile,
		MaxIterations:   50,
		CodexEnabled:    false,
		FinalizeEnabled: true,
		AppConfig:       testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	// run should succeed despite finalize FAILED signal
	require.NoError(t, err)

	// verify finalize failure was logged
	var foundFailureLog bool
	for _, call := range log.PrintCalls() {
		if strings.Contains(call.Format, "finalize step reported failure") {
			foundFailureLog = true
			break
		}
	}
	assert.True(t, foundFailureLog, "should log finalize failure signal")
}

func TestRunner_Finalize_RunsInReviewOnlyMode(t *testing.T) {
	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "review done", Signal: processor.SignalReviewDone}, // first review
		{Output: "review done", Signal: processor.SignalReviewDone}, // pre-codex review loop
		{Output: "review done", Signal: processor.SignalReviewDone}, // post-codex review loop (codex disabled)
		{Output: "finalize done"},                                   // finalize step
	})
	codex := newMockExecutor(nil)

	cfg := processor.Config{
		Mode:            processor.ModeReview,
		MaxIterations:   50,
		CodexEnabled:    false,
		FinalizeEnabled: true,
		AppConfig:       testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.NoError(t, err)
	// verify finalize ran (4 claude calls total)
	assert.Len(t, claude.RunCalls(), 4)
}

func TestRunner_Finalize_RunsInCodexOnlyMode(t *testing.T) {
	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "review done", Signal: processor.SignalReviewDone}, // post-codex review loop (codex disabled)
		{Output: "finalize done"},                                   // finalize step
	})
	codex := newMockExecutor(nil)

	cfg := processor.Config{
		Mode:            processor.ModeCodexOnly,
		MaxIterations:   50,
		CodexEnabled:    false,
		FinalizeEnabled: true,
		AppConfig:       testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	require.NoError(t, err)
	// verify finalize ran (2 claude calls total)
	assert.Len(t, claude.RunCalls(), 2)
}

func TestRunner_Finalize_ContextCancellationPropagates(t *testing.T) {
	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "review done", Signal: processor.SignalReviewDone}, // first review
		{Output: "review done", Signal: processor.SignalReviewDone}, // pre-codex review loop
		{Output: "review done", Signal: processor.SignalReviewDone}, // post-codex review loop (codex disabled)
		{Error: context.Canceled},                                   // finalize step - context canceled
	})
	codex := newMockExecutor(nil)

	cfg := processor.Config{
		Mode:            processor.ModeReview,
		MaxIterations:   50,
		CodexEnabled:    false,
		FinalizeEnabled: true,
		AppConfig:       testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex)
	err := r.Run(context.Background())

	// run should fail with context canceled error
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}
