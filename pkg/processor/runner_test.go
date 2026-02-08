package processor_test

import (
	"context"
	"errors"
	"io"
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
	"github.com/umputun/ralphex/pkg/status"
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

func TestRunner_Run_UnknownMode(t *testing.T) {
	log := newMockLogger("")
	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)

	r := processor.NewWithExecutors(processor.Config{Mode: "invalid"}, log, claude, codex, nil, &status.PhaseHolder{})
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown mode")
}

func TestRunner_RunFull_NoPlanFile(t *testing.T) {
	log := newMockLogger("")
	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)

	r := processor.NewWithExecutors(processor.Config{Mode: processor.ModeFull}, log, claude, codex, nil, &status.PhaseHolder{})
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
		{Output: "task done", Signal: status.Completed},    // task phase completes
		{Output: "review done", Signal: status.ReviewDone}, // first review
		{Output: "review done", Signal: status.ReviewDone}, // pre-codex review loop
		{Output: "done", Signal: status.CodexDone},         // codex evaluation
		{Output: "review done", Signal: status.ReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Output: "found issue in foo.go"}, // codex finds issues
	})

	cfg := processor.Config{Mode: processor.ModeFull, PlanFile: planFile, MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
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
		{Output: "task done", Signal: status.Completed},
		{Output: "review done", Signal: status.ReviewDone}, // first review
		{Output: "review done", Signal: status.ReviewDone}, // pre-codex review loop
		{Output: "review done", Signal: status.ReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Output: ""}, // codex finds nothing
	})

	cfg := processor.Config{Mode: processor.ModeFull, PlanFile: planFile, MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	err := r.Run(context.Background())

	require.NoError(t, err)
}

func TestRunner_RunReviewOnly_Success(t *testing.T) {
	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "review done", Signal: status.ReviewDone}, // first review
		{Output: "review done", Signal: status.ReviewDone}, // pre-codex review loop
		{Output: "done", Signal: status.CodexDone},         // codex evaluation
		{Output: "review done", Signal: status.ReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Output: "found issue"},
	})

	cfg := processor.Config{Mode: processor.ModeReview, MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	err := r.Run(context.Background())

	require.NoError(t, err)
	assert.Len(t, codex.RunCalls(), 1)
}

func TestRunner_RunCodexOnly_Success(t *testing.T) {
	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "done", Signal: status.CodexDone},         // codex evaluation
		{Output: "review done", Signal: status.ReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Output: "found issue"},
	})

	cfg := processor.Config{Mode: processor.ModeCodexOnly, MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	err := r.Run(context.Background())

	require.NoError(t, err)
	assert.Len(t, codex.RunCalls(), 1)
}

func TestRunner_RunCodexOnly_NoFindings(t *testing.T) {
	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "review done", Signal: status.ReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Output: ""}, // no findings
	})

	cfg := processor.Config{Mode: processor.ModeCodexOnly, MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	err := r.Run(context.Background())

	require.NoError(t, err)
}

func TestRunner_CodexDisabled_SkipsCodexPhase(t *testing.T) {
	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "review done", Signal: status.ReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor(nil)

	cfg := processor.Config{Mode: processor.ModeCodexOnly, MaxIterations: 50, CodexEnabled: false, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
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
		{Output: "task done", Signal: status.Completed}, // task phase completes
	})
	codex := newMockExecutor(nil)

	cfg := processor.Config{Mode: processor.ModeTasksOnly, PlanFile: planFile, MaxIterations: 50, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	err := r.Run(context.Background())

	require.NoError(t, err)
	assert.Empty(t, codex.RunCalls(), "codex should not be called in tasks-only mode")
	assert.Len(t, claude.RunCalls(), 1)
}

func TestRunner_RunTasksOnly_NoPlanFile(t *testing.T) {
	log := newMockLogger("")
	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)

	r := processor.NewWithExecutors(processor.Config{Mode: processor.ModeTasksOnly}, log, claude, codex, nil, &status.PhaseHolder{})
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
		{Output: "error", Signal: status.Failed}, // first try
		{Output: "error", Signal: status.Failed}, // retry
	})
	codex := newMockExecutor(nil)

	cfg := processor.Config{Mode: processor.ModeTasksOnly, PlanFile: planFile, MaxIterations: 10, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
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
		{Output: "task done", Signal: status.Completed},
	})
	codex := newMockExecutor(nil)

	cfg := processor.Config{
		Mode:          processor.ModeTasksOnly,
		PlanFile:      planFile,
		MaxIterations: 50,
		CodexEnabled:  true, // enabled but should not run in tasks-only mode
		AppConfig:     testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
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
		{Output: "error", Signal: status.Failed}, // first try
		{Output: "error", Signal: status.Failed}, // retry
	})
	codex := newMockExecutor(nil)

	cfg := processor.Config{Mode: processor.ModeFull, PlanFile: planFile, MaxIterations: 10, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
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
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
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
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	err := r.Run(ctx)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestRunner_ClaudeReview_FailedSignal(t *testing.T) {
	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "error", Signal: status.Failed},
	})
	codex := newMockExecutor(nil)

	cfg := processor.Config{Mode: processor.ModeReview, MaxIterations: 50, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "FAILED signal")
}

func TestRunner_CodexPhase_Error(t *testing.T) {
	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "review done", Signal: status.ReviewDone}, // first review
		{Output: "review done", Signal: status.ReviewDone}, // pre-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Error: errors.New("codex error")},
	})

	cfg := processor.Config{Mode: processor.ModeReview, MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
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
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
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
			r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})

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
			r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})

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
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})

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
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})

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
		{Output: "error", Signal: status.Failed}, // first try
		{Output: "error", Signal: status.Failed}, // retry 1
		{Output: "error", Signal: status.Failed}, // retry 2
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
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
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
		{Output: "plan created", Signal: status.PlanReady},
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
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
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
		{Output: questionSignal},                           // first iteration - asks question
		{Output: "plan created", Signal: status.PlanReady}, // second iteration - completes
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
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
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
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
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
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	// don't set input collector
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "input collector required")
}

func TestRunner_RunPlan_FailedSignal(t *testing.T) {
	log := newMockLogger("progress-plan.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "error", Signal: status.Failed},
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
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
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
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
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
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
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
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
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
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
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
	r := processor.New(cfg, log, &status.PhaseHolder{})

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

func TestRunner_New_CodexNotInstalled_CustomReviewStillWorks(t *testing.T) {
	log := newMockLogger("progress.txt")

	appCfg := testAppConfig(t)
	appCfg.CodexCommand = "/nonexistent/path/to/codex" // command that doesn't exist
	appCfg.ExternalReviewTool = "custom"               // using custom, not codex
	appCfg.CustomReviewScript = "/path/to/script.sh"

	cfg := processor.Config{
		Mode:          processor.ModeCodexOnly,
		MaxIterations: 50,
		CodexEnabled:  true,
		AppConfig:     appCfg,
	}

	// use processor.New (not NewWithExecutors) to trigger LookPath check
	r := processor.New(cfg, log, &status.PhaseHolder{})

	// verify NO warning was logged (custom reviews don't need codex binary)
	var foundWarning bool
	for _, call := range log.PrintCalls() {
		if strings.Contains(call.Format, "codex not found") {
			foundWarning = true
			break
		}
	}
	assert.False(t, foundWarning, "should NOT log warning about codex when using custom external review")

	// verify runner was created
	assert.NotNil(t, r, "runner should be created")
}

func TestRunner_New_CodexNotInstalled_NoneReviewStillWorks(t *testing.T) {
	log := newMockLogger("progress.txt")

	appCfg := testAppConfig(t)
	appCfg.CodexCommand = "/nonexistent/path/to/codex" // command that doesn't exist
	appCfg.ExternalReviewTool = "none"                 // external review disabled

	cfg := processor.Config{
		Mode:          processor.ModeCodexOnly,
		MaxIterations: 50,
		CodexEnabled:  true,
		AppConfig:     appCfg,
	}

	// use processor.New (not NewWithExecutors) to trigger LookPath check
	r := processor.New(cfg, log, &status.PhaseHolder{})

	// verify NO warning was logged (no external review means no codex needed)
	var foundWarning bool
	for _, call := range log.PrintCalls() {
		if strings.Contains(call.Format, "codex not found") {
			foundWarning = true
			break
		}
	}
	assert.False(t, foundWarning, "should NOT log warning about codex when external review is disabled")

	// verify runner was created
	assert.NotNil(t, r, "runner should be created")
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
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
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
		{Output: "review done", Signal: status.ReviewDone}, // first review
		{Output: "review done", Signal: status.ReviewDone}, // pre-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Output: "Rate limit exceeded", Error: &executor.PatternMatchError{Pattern: "rate limit", HelpCmd: "codex /status"}},
	})

	cfg := processor.Config{Mode: processor.ModeReview, MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
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
		{Output: "review done", Signal: status.ReviewDone},                                                              // first review
		{Output: "rate limited", Error: &executor.PatternMatchError{Pattern: "rate limited", HelpCmd: "claude /usage"}}, // review loop hits rate limit
	})
	codex := newMockExecutor(nil)

	cfg := processor.Config{Mode: processor.ModeReview, MaxIterations: 50, CodexEnabled: false, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
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
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
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
		{Output: planDraftSignal},                          // first iteration - emits draft
		{Output: "plan created", Signal: status.PlanReady}, // second iteration - completes
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
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
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
		{Output: planDraftSignal},                          // first iteration - initial draft
		{Output: revisedDraftSignal},                       // second iteration - revised draft
		{Output: "plan created", Signal: status.PlanReady}, // third iteration - completes
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
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
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
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
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
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
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
		{Output: malformedDraftSignal},                     // first iteration - malformed draft
		{Output: "plan created", Signal: status.PlanReady}, // second iteration - completes anyway
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
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
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
		{Output: questionSignal},                           // first iteration - question
		{Output: planDraftSignal},                          // second iteration - draft
		{Output: "plan created", Signal: status.PlanReady}, // third iteration - completes
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
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
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
		{Output: "task done", Signal: status.Completed},    // task phase
		{Output: "review done", Signal: status.ReviewDone}, // first review
		{Output: "review done", Signal: status.ReviewDone}, // pre-codex review loop
		{Output: "review done", Signal: status.ReviewDone}, // post-codex review loop (codex disabled)
		{Output: "finalize done"},                          // finalize step
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
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
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
		{Output: "task done", Signal: status.Completed},    // task phase
		{Output: "review done", Signal: status.ReviewDone}, // first review
		{Output: "review done", Signal: status.ReviewDone}, // pre-codex review loop
		{Output: "review done", Signal: status.ReviewDone}, // post-codex review loop (codex disabled)
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
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
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
		{Output: "task done", Signal: status.Completed},    // task phase
		{Output: "review done", Signal: status.ReviewDone}, // first review
		{Output: "review done", Signal: status.ReviewDone}, // pre-codex review loop
		{Output: "review done", Signal: status.ReviewDone}, // post-codex review loop (codex disabled)
		{Error: errors.New("finalize error")},              // finalize fails
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
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
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
		{Output: "task done", Signal: status.Completed},    // task phase
		{Output: "review done", Signal: status.ReviewDone}, // first review
		{Output: "review done", Signal: status.ReviewDone}, // pre-codex review loop
		{Output: "review done", Signal: status.ReviewDone}, // post-codex review loop (codex disabled)
		{Output: "failed", Signal: status.Failed},          // finalize reports FAILED signal
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
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
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
		{Output: "review done", Signal: status.ReviewDone}, // first review
		{Output: "review done", Signal: status.ReviewDone}, // pre-codex review loop
		{Output: "review done", Signal: status.ReviewDone}, // post-codex review loop (codex disabled)
		{Output: "finalize done"},                          // finalize step
	})
	codex := newMockExecutor(nil)

	cfg := processor.Config{
		Mode:            processor.ModeReview,
		MaxIterations:   50,
		CodexEnabled:    false,
		FinalizeEnabled: true,
		AppConfig:       testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	err := r.Run(context.Background())

	require.NoError(t, err)
	// verify finalize ran (4 claude calls total)
	assert.Len(t, claude.RunCalls(), 4)
}

func TestRunner_Finalize_RunsInCodexOnlyMode(t *testing.T) {
	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "review done", Signal: status.ReviewDone}, // post-codex review loop (codex disabled)
		{Output: "finalize done"},                          // finalize step
	})
	codex := newMockExecutor(nil)

	cfg := processor.Config{
		Mode:            processor.ModeCodexOnly,
		MaxIterations:   50,
		CodexEnabled:    false,
		FinalizeEnabled: true,
		AppConfig:       testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	err := r.Run(context.Background())

	require.NoError(t, err)
	// verify finalize ran (2 claude calls total)
	assert.Len(t, claude.RunCalls(), 2)
}

func TestRunner_CodexAndPostReview_PipelineOrder(t *testing.T) {
	tests := []struct {
		name          string
		mode          processor.Mode
		planFile      bool
		claudeResults []executor.Result
		codexResults  []executor.Result
		expClaude     int // expected claude call count
		expCodex      int // expected codex call count
		expPhases     []status.Phase
	}{
		{
			name: "codex-only runs codex then review then finalize",
			mode: processor.ModeCodexOnly,
			claudeResults: []executor.Result{
				{Output: "done", Signal: status.CodexDone},         // codex evaluation
				{Output: "review done", Signal: status.ReviewDone}, // post-codex review loop
				{Output: "finalize done"},                          // finalize step
			},
			codexResults: []executor.Result{
				{Output: "found issue"},
			},
			expClaude: 3,
			expCodex:  1,
			expPhases: []status.Phase{status.PhaseCodex, status.PhaseClaudeEval, status.PhaseCodex, status.PhaseReview, status.PhaseFinalize},
		},
		{
			name: "review-only runs first review then codex then review then finalize",
			mode: processor.ModeReview,
			claudeResults: []executor.Result{
				{Output: "review done", Signal: status.ReviewDone}, // first review
				{Output: "review done", Signal: status.ReviewDone}, // pre-codex review loop
				{Output: "done", Signal: status.CodexDone},         // codex evaluation
				{Output: "review done", Signal: status.ReviewDone}, // post-codex review loop
				{Output: "finalize done"},                          // finalize step
			},
			codexResults: []executor.Result{
				{Output: "found issue"},
			},
			expClaude: 5,
			expCodex:  1,
			// review phase set once at start (covers first review + pre-codex loop),
			// then codex → claude-eval → codex (within codex loop), then review, then finalize
			expPhases: []status.Phase{status.PhaseReview, status.PhaseCodex, status.PhaseClaudeEval, status.PhaseCodex, status.PhaseReview, status.PhaseFinalize},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var phases []status.Phase
			holder := &status.PhaseHolder{}
			holder.OnChange(func(_, newPhase status.Phase) {
				phases = append(phases, newPhase)
			})

			log := newMockLogger("progress.txt")
			claude := newMockExecutor(tc.claudeResults)
			codex := newMockExecutor(tc.codexResults)

			var planFile string
			if tc.planFile {
				tmpDir := t.TempDir()
				planFile = filepath.Join(tmpDir, "plan.md")
				require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n- [x] Task 1"), 0o600))
			}

			cfg := processor.Config{
				Mode:            tc.mode,
				PlanFile:        planFile,
				MaxIterations:   50,
				CodexEnabled:    true,
				FinalizeEnabled: true,
				AppConfig:       testAppConfig(t),
			}
			r := processor.NewWithExecutors(cfg, log, claude, codex, nil, holder)
			err := r.Run(context.Background())

			require.NoError(t, err)
			assert.Len(t, claude.RunCalls(), tc.expClaude)
			assert.Len(t, codex.RunCalls(), tc.expCodex)
			assert.Equal(t, tc.expPhases, phases, "phase transitions should match expected order")
		})
	}
}

func TestRunner_Finalize_ContextCancellationPropagates(t *testing.T) {
	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "review done", Signal: status.ReviewDone}, // first review
		{Output: "review done", Signal: status.ReviewDone}, // pre-codex review loop
		{Output: "review done", Signal: status.ReviewDone}, // post-codex review loop (codex disabled)
		{Error: context.Canceled},                          // finalize step - context canceled
	})
	codex := newMockExecutor(nil)

	cfg := processor.Config{
		Mode:            processor.ModeReview,
		MaxIterations:   50,
		CodexEnabled:    false,
		FinalizeEnabled: true,
		AppConfig:       testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	err := r.Run(context.Background())

	// run should fail with context canceled error
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestRunner_ExternalReviewTool_CodexEnabled(t *testing.T) {
	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "done", Signal: processor.SignalCodexDone},         // codex evaluation
		{Output: "review done", Signal: processor.SignalReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Output: "found issue"},
	})

	appCfg := testAppConfig(t)
	// explicitly set to codex (though it's the default)
	appCfg.ExternalReviewTool = "codex"

	cfg := processor.Config{
		Mode:          processor.ModeCodexOnly,
		MaxIterations: 50,
		CodexEnabled:  true,
		AppConfig:     appCfg,
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	err := r.Run(context.Background())

	require.NoError(t, err)
	assert.Len(t, codex.RunCalls(), 1, "codex should be called when external_review_tool=codex")
}

func TestRunner_ExternalReviewTool_None(t *testing.T) {
	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "review done", Signal: processor.SignalReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor(nil)

	appCfg := testAppConfig(t)
	appCfg.ExternalReviewTool = "none"

	cfg := processor.Config{
		Mode:          processor.ModeCodexOnly,
		MaxIterations: 50,
		CodexEnabled:  true, // enabled but tool is none
		AppConfig:     appCfg,
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	err := r.Run(context.Background())

	require.NoError(t, err)
	assert.Empty(t, codex.RunCalls(), "codex should not be called when external_review_tool=none")
}

func TestRunner_ExternalReviewTool_BackwardCompat_CodexDisabled(t *testing.T) {
	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "review done", Signal: processor.SignalReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor(nil)

	appCfg := testAppConfig(t)
	// external_review_tool is "codex" (default), but CodexEnabled is false
	appCfg.ExternalReviewTool = "codex"

	cfg := processor.Config{
		Mode:          processor.ModeCodexOnly,
		MaxIterations: 50,
		CodexEnabled:  false, // this should override external_review_tool
		AppConfig:     appCfg,
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	err := r.Run(context.Background())

	require.NoError(t, err)
	assert.Empty(t, codex.RunCalls(), "codex should not be called when CodexEnabled=false (backward compat)")
}

func TestRunner_ExternalReviewTool_Custom_Success(t *testing.T) {
	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "done", Signal: processor.SignalCodexDone},         // custom evaluation
		{Output: "review done", Signal: processor.SignalReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor(nil)

	appCfg := testAppConfig(t)
	appCfg.ExternalReviewTool = "custom"
	appCfg.CustomReviewScript = "/path/to/script.sh"

	// create a mock custom executor
	customExec := &executor.CustomExecutor{
		Script: appCfg.CustomReviewScript,
		OutputHandler: func(text string) {
			log.PrintAligned(text)
		},
	}
	// mock the runner to simulate custom executor behavior
	customResultIdx := 0
	customResults := []executor.Result{
		{Output: "found issue in foo.go:10"},
	}

	// override the runner for this test to use custom
	mockCustomRunner := &mockCustomRunnerImpl{
		results: customResults,
		idx:     &customResultIdx,
	}
	customExec.SetRunner(mockCustomRunner)

	cfg := processor.Config{
		Mode:          processor.ModeCodexOnly,
		MaxIterations: 50,
		CodexEnabled:  true,
		AppConfig:     appCfg,
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex, customExec, &status.PhaseHolder{})
	err := r.Run(context.Background())

	require.NoError(t, err)
	assert.Empty(t, codex.RunCalls(), "codex should not be called when external_review_tool=custom")
	assert.Len(t, claude.RunCalls(), 2, "claude should be called for evaluation and post-review")
}

func TestRunner_ExternalReviewTool_Custom_NotConfigured(t *testing.T) {
	log := newMockLogger("progress.txt")
	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)

	appCfg := testAppConfig(t)
	appCfg.ExternalReviewTool = "custom"
	// CustomReviewScript is not set

	cfg := processor.Config{
		Mode:          processor.ModeCodexOnly,
		MaxIterations: 50,
		CodexEnabled:  true,
		AppConfig:     appCfg,
	}
	// no custom executor passed
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "custom review script not configured")
}

// mockCustomRunnerImpl is a mock implementation of executor.CustomRunner for testing.
type mockCustomRunnerImpl struct {
	results []executor.Result
	idx     *int
}

func (m *mockCustomRunnerImpl) Run(_ context.Context, _, _ string) (io.Reader, func() error, error) {
	if *m.idx >= len(m.results) {
		return nil, nil, errors.New("no more mock results")
	}
	result := m.results[*m.idx]
	*m.idx++
	return strings.NewReader(result.Output), func() error { return result.Error }, nil
}

func TestRunner_ReviewLoop_NoCommitExit(t *testing.T) {
	log := newMockLogger("progress.txt")

	// ModeReview flow: first review → pre-codex review loop → codex (disabled) → post-codex review loop
	claude := newMockExecutor([]executor.Result{
		{Output: "review done", Signal: status.ReviewDone}, // first review
		{Output: "review done", Signal: status.ReviewDone}, // pre-codex review loop (exits immediately)
		{Output: "looked at code, nothing to fix"},         // post-codex review loop iteration - no signal
	})
	codex := newMockExecutor(nil)

	// mock git checker returns same hash both times (no commits made)
	gitMock := &mocks.GitCheckerMock{
		HeadHashFunc: func() (string, error) {
			return "abc123def456abc123def456abc123def456abcd", nil
		},
	}

	cfg := processor.Config{Mode: processor.ModeReview, MaxIterations: 50, CodexEnabled: false, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	r.SetGitChecker(gitMock)
	err := r.Run(context.Background())

	require.NoError(t, err)
	assert.Len(t, claude.RunCalls(), 3)

	// verify "no changes detected" was logged
	var foundNoChanges bool
	for _, call := range log.PrintCalls() {
		if strings.Contains(call.Format, "no changes detected") {
			foundNoChanges = true
			break
		}
	}
	assert.True(t, foundNoChanges, "should log no changes detected")
}

func TestRunner_ReviewLoop_CommitDetected_ContinuesLoop(t *testing.T) {
	log := newMockLogger("progress.txt")

	// ModeReview flow: first review → pre-codex review loop → codex (disabled) → post-codex review loop
	claude := newMockExecutor([]executor.Result{
		{Output: "review done", Signal: status.ReviewDone}, // first review
		{Output: "review done", Signal: status.ReviewDone}, // pre-codex review loop (exits immediately)
		{Output: "fixed issues"},                           // post-codex review loop iteration 1 - no signal
		{Output: "review done", Signal: status.ReviewDone}, // post-codex review loop iteration 2 - done
	})
	codex := newMockExecutor(nil)

	// mock git checker: hash changes between before/after calls within an iteration
	// simulating that claude made a commit during the review
	hashes := []string{
		"aaaa00000000000000000000000000000000aaaa", // pre-codex loop: headBefore (REVIEW_DONE exits before headAfter)
		"aaaa00000000000000000000000000000000aaaa", // post-codex loop iter 1: headBefore
		"bbbb00000000000000000000000000000000bbbb", // post-codex loop iter 1: headAfter (different = commit detected)
		"bbbb00000000000000000000000000000000bbbb", // post-codex loop iter 2: headBefore (REVIEW_DONE exits)
	}
	hashIdx := 0
	gitMock := &mocks.GitCheckerMock{
		HeadHashFunc: func() (string, error) {
			require.Less(t, hashIdx, len(hashes), "unexpected extra HeadHash call #%d", hashIdx)
			h := hashes[hashIdx]
			hashIdx++
			return h, nil
		},
	}

	cfg := processor.Config{Mode: processor.ModeReview, MaxIterations: 50, CodexEnabled: false, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	r.SetGitChecker(gitMock)
	err := r.Run(context.Background())

	require.NoError(t, err)
	assert.Len(t, claude.RunCalls(), 4)
	assert.Len(t, gitMock.HeadHashCalls(), 4, "expected exactly 4 HeadHash calls")
}

func TestRunner_ReviewLoop_GitCheckerNil_SkipsNoCommitCheck(t *testing.T) {
	log := newMockLogger("progress.txt")

	// ModeReview flow: first review → pre-codex review loop → codex (disabled) → post-codex review loop
	// max review iterations = max(3, 30/10) = 3 per loop
	claude := newMockExecutor([]executor.Result{
		{Output: "review done", Signal: status.ReviewDone}, // first review
		{Output: "review done", Signal: status.ReviewDone}, // pre-codex review loop (exits immediately)
		{Output: "looking at code"},                        // post-codex review loop 1
		{Output: "looking at code"},                        // post-codex review loop 2
		{Output: "looking at code"},                        // post-codex review loop 3
	})
	codex := newMockExecutor(nil)

	// no git checker - nil
	cfg := processor.Config{Mode: processor.ModeReview, MaxIterations: 30, CodexEnabled: false, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	err := r.Run(context.Background())

	require.NoError(t, err)
	// first review + pre-codex loop (1 iteration) + post-codex loop (3 iterations, max reached)
	assert.Len(t, claude.RunCalls(), 5)
}

func TestRunner_ReviewLoop_GitCheckerError_SkipsNoCommitCheck(t *testing.T) {
	log := newMockLogger("progress.txt")

	// ModeReview flow: first review → pre-codex review loop → codex (disabled) → post-codex review loop
	// max review iterations = max(3, 30/10) = 3 per loop
	claude := newMockExecutor([]executor.Result{
		{Output: "review done", Signal: status.ReviewDone}, // first review
		{Output: "review done", Signal: status.ReviewDone}, // pre-codex review loop (exits immediately)
		{Output: "looking at code"},                        // post-codex review loop 1
		{Output: "looking at code"},                        // post-codex review loop 2
		{Output: "looking at code"},                        // post-codex review loop 3
	})
	codex := newMockExecutor(nil)

	// git checker always returns error — should degrade gracefully (run to max iterations)
	gitMock := &mocks.GitCheckerMock{
		HeadHashFunc: func() (string, error) {
			return "", errors.New("git HEAD error")
		},
	}

	cfg := processor.Config{Mode: processor.ModeReview, MaxIterations: 30, CodexEnabled: false, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	r.SetGitChecker(gitMock)
	err := r.Run(context.Background())

	require.NoError(t, err)
	// first review + pre-codex loop (1 iteration) + post-codex loop (3 iterations, max reached)
	assert.Len(t, claude.RunCalls(), 5)
}
