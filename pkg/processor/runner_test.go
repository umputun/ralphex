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
	err := r.Run(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown mode")
}

func TestRunner_RunFull_NoPlanFile(t *testing.T) {
	log := newMockLogger("")
	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)

	r := processor.NewWithExecutors(processor.Config{Mode: processor.ModeFull}, log, claude, codex, nil, &status.PhaseHolder{})
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

	require.NoError(t, err)
}

func TestRunner_MaxExternalIterations_ExplicitLimit(t *testing.T) {
	log := newMockLogger("progress.txt")
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

	cfg := processor.Config{
		Mode: processor.ModeCodexOnly, MaxIterations: 50, IterationDelayMs: 1,
		MaxExternalIterations: 2, CodexEnabled: true, AppConfig: testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	err := r.Run(t.Context())

	require.NoError(t, err)
	assert.Len(t, codex.RunCalls(), 2, "codex should be called exactly MaxExternalIterations times")
}

func TestRunner_MaxExternalIterations_DerivedFormula(t *testing.T) {
	log := newMockLogger("progress.txt")
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

	cfg := processor.Config{
		Mode: processor.ModeCodexOnly, MaxIterations: 15, IterationDelayMs: 1,
		MaxExternalIterations: 0, CodexEnabled: true, AppConfig: testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	err := r.Run(t.Context())

	require.NoError(t, err)
	assert.Len(t, codex.RunCalls(), 3, "codex should use derived formula: max(3, 15/5) = 3")
}

func TestRunner_CodexDisabled_SkipsCodexPhase(t *testing.T) {
	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "review done", Signal: status.ReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor(nil)

	cfg := processor.Config{Mode: processor.ModeCodexOnly, MaxIterations: 50, CodexEnabled: false, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

	require.NoError(t, err)
	assert.Empty(t, codex.RunCalls(), "codex should not be called in tasks-only mode")
	assert.Len(t, claude.RunCalls(), 1)
}

func TestRunner_RunTasksOnly_NoPlanFile(t *testing.T) {
	log := newMockLogger("")
	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)

	r := processor.NewWithExecutors(processor.Config{Mode: processor.ModeTasksOnly}, log, claude, codex, nil, &status.PhaseHolder{})
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

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

	cfg := processor.Config{Mode: processor.ModeFull, PlanFile: planFile, MaxIterations: 3, IterationDelayMs: 1, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	err := r.Run(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "max iterations")
}

func TestRunner_TaskPhase_ContextCanceled(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

	ctx, cancel := context.WithCancel(t.Context())
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
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "max plan iterations")
}

func TestRunner_RunPlan_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
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
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

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
	// verifies that LimitPatternError is handled gracefully via handlePatternMatchError
	// when waitOnLimit == 0, same as PatternMatchError (logs error + help, returns error)
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n- [ ] Task 1"), 0o600))

	log := newMockLogger("progress.txt")
	claude := newMockExecutor([]executor.Result{
		{Output: "You've hit your limit", Error: &executor.LimitPatternError{Pattern: "You've hit your limit", HelpCmd: "claude /usage"}},
	})
	codex := newMockExecutor(nil)

	cfg := processor.Config{Mode: processor.ModeFull, PlanFile: planFile, MaxIterations: 10, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	err := r.Run(t.Context())

	require.Error(t, err)
	var limitErr *executor.LimitPatternError
	require.ErrorAs(t, err, &limitErr)
	assert.Equal(t, "You've hit your limit", limitErr.Pattern)
	assert.Equal(t, "claude /usage", limitErr.HelpCmd)

	// verify logging via handlePatternMatchError
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
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

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
			err := r.Run(t.Context())

			require.NoError(t, err)
			assert.Len(t, claude.RunCalls(), tc.expClaude)
			assert.Len(t, codex.RunCalls(), tc.expCodex)
			assert.Equal(t, tc.expPhases, phases, "phase transitions should match expected order")
		})
	}
}

func TestRunner_CodexAndPostReview_CommitPendingPrefix(t *testing.T) {
	t.Run("prefix applied when external review enabled", func(t *testing.T) {
		log := newMockLogger("progress.txt")

		var capturedPrompts []string
		claude := &mocks.ExecutorMock{
			RunFunc: func(_ context.Context, prompt string) executor.Result {
				capturedPrompts = append(capturedPrompts, prompt)
				switch len(capturedPrompts) {
				case 1: // codex evaluation
					return executor.Result{Output: "done", Signal: status.CodexDone}
				case 2: // post-codex review loop
					return executor.Result{Output: "review done", Signal: status.ReviewDone}
				default:
					return executor.Result{Error: errors.New("unexpected call")}
				}
			},
		}
		codex := newMockExecutor([]executor.Result{{Output: "found issue"}})

		cfg := processor.Config{Mode: processor.ModeCodexOnly, MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}
		r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
		err := r.Run(t.Context())

		require.NoError(t, err)
		require.Len(t, capturedPrompts, 2)
		assert.Contains(t, capturedPrompts[1], "IMPORTANT: Before starting the review, run `git status`")
		assert.Contains(t, capturedPrompts[1], "fix: address code review findings")
	})

	t.Run("no prefix when external review disabled", func(t *testing.T) {
		log := newMockLogger("progress.txt")

		var capturedPrompts []string
		claude := &mocks.ExecutorMock{
			RunFunc: func(_ context.Context, prompt string) executor.Result {
				capturedPrompts = append(capturedPrompts, prompt)
				return executor.Result{Output: "review done", Signal: status.ReviewDone}
			},
		}
		codex := newMockExecutor(nil)

		cfg := processor.Config{Mode: processor.ModeCodexOnly, MaxIterations: 50, CodexEnabled: false, AppConfig: testAppConfig(t)}
		r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
		err := r.Run(t.Context())

		require.NoError(t, err)
		require.Len(t, capturedPrompts, 1)
		assert.NotContains(t, capturedPrompts[0], "IMPORTANT: Before starting the review, run `git status`")
	})
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
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

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
	err := r.Run(t.Context())

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

	// mock git checker returns same hash and diff both times (no changes made)
	gitMock := &mocks.GitCheckerMock{
		HeadHashFunc:        func() (string, error) { return "abc123def456abc123def456abc123def456abcd", nil },
		DiffFingerprintFunc: func() (string, error) { return "unchanged-diff", nil },
	}

	cfg := processor.Config{Mode: processor.ModeReview, MaxIterations: 50, CodexEnabled: false, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	r.SetGitChecker(gitMock)
	err := r.Run(t.Context())

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
		DiffFingerprintFunc: func() (string, error) { return "constant-diff", nil },
	}

	cfg := processor.Config{Mode: processor.ModeReview, MaxIterations: 50, IterationDelayMs: 1, CodexEnabled: false, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	r.SetGitChecker(gitMock)
	err := r.Run(t.Context())

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
	cfg := processor.Config{Mode: processor.ModeReview, MaxIterations: 30, IterationDelayMs: 1, CodexEnabled: false, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	err := r.Run(t.Context())

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
		HeadHashFunc:        func() (string, error) { return "", errors.New("git HEAD error") },
		DiffFingerprintFunc: func() (string, error) { return "", errors.New("git diff error") },
	}

	cfg := processor.Config{Mode: processor.ModeReview, MaxIterations: 30, IterationDelayMs: 1, CodexEnabled: false, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	r.SetGitChecker(gitMock)
	err := r.Run(t.Context())

	require.NoError(t, err)
	// first review + pre-codex loop (1 iteration) + post-codex loop (3 iterations, max reached)
	assert.Len(t, claude.RunCalls(), 5)
}

// TestRunner_SleepWithContext_CancelDuringDelay verifies that context cancellation
// during iteration delay causes prompt exit (not blocking for the full delay).
func TestRunner_SleepWithContext_CancelDuringDelay(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("- [ ] task 1"), 0o600))

	// use a long iteration delay to make the difference obvious
	const longDelay = 5000 // 5 seconds

	// executor returns no signal (no completion), so runner will loop and hit sleepWithContext
	claude := newMockExecutor([]executor.Result{
		{Output: "working on it"},
	})
	codex := newMockExecutor(nil)
	log := newMockLogger("progress.txt")

	cfg := processor.Config{
		Mode:             processor.ModeFull,
		PlanFile:         planFile,
		MaxIterations:    50,
		IterationDelayMs: longDelay,
		AppConfig:        testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})

	// cancel context after a short delay (50ms) — well before iteration delay (5s)
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := r.Run(ctx)
	elapsed := time.Since(start)

	require.ErrorIs(t, err, context.Canceled)
	// should exit well before the 5s iteration delay
	assert.Less(t, elapsed, time.Duration(longDelay)*time.Millisecond,
		"should exit promptly on cancellation, not wait for full iteration delay")
}

func TestRunner_NextPlanTaskPosition(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected int
	}{
		{name: "first task uncompleted", content: "# Plan\n### Task 1: setup\n- [ ] do thing\n### Task 2: build\n- [ ] build it", expected: 1},
		{name: "second task uncompleted", content: "# Plan\n### Task 1: setup\n- [x] done\n### Task 2: build\n- [ ] build it", expected: 2},
		{name: "all done", content: "# Plan\n### Task 1: setup\n- [x] done\n### Task 2: build\n- [x] built", expected: 0},
		{name: "inserted task 2.5", content: "# Plan\n### Task 1: setup\n- [x] done\n### Task 2: api\n- [x] done\n### Task 2.5: middleware\n- [ ] add it\n### Task 3: tests\n- [ ] test", expected: 3},
		{name: "retry same task", content: "# Plan\n### Task 1: setup\n- [x] done\n### Task 2: build\n- [x] first\n- [ ] second\n### Task 3: test\n- [ ] test", expected: 2},
		{name: "header-only task skipped", content: "# Plan\n### Task 1: setup\n- [x] done\n### Task 2: notes\n### Task 3: build\n- [ ] build it", expected: 3},
		{name: "no tasks", content: "# Plan\nJust some text", expected: 0},
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

			assert.Equal(t, tc.expected, r.TestNextPlanTaskPosition())
		})
	}
}

func TestRunner_NextPlanTaskPosition_MissingFile(t *testing.T) {
	log := newMockLogger("")
	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)

	cfg := processor.Config{PlanFile: "/nonexistent/plan.md"}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})

	assert.Equal(t, 0, r.TestNextPlanTaskPosition(), "missing file should return 0")
}

func TestRunner_NextPlanTaskPosition_EmptyPlanFile(t *testing.T) {
	log := newMockLogger("")
	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)

	cfg := processor.Config{PlanFile: ""}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})

	assert.Equal(t, 0, r.TestNextPlanTaskPosition(), "empty plan file path should return 0")
}

func TestRunner_TaskPhase_UsesPlanTaskPosition(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	// task 1 done, task 2 uncompleted - position should be 2
	planContent := "# Plan\n### Task 1: setup\n- [x] done\n### Task 2: build\n- [ ] build it"
	require.NoError(t, os.WriteFile(planFile, []byte(planContent), 0o600))

	log := newMockLogger("progress.txt")
	// first call: task runs, signals completed; but plan still has [ ] items
	// so runner continues, and on second iteration, plan is updated (simulate by updating file)
	callCount := 0
	claude := &mocks.ExecutorMock{
		RunFunc: func(_ context.Context, _ string) executor.Result {
			callCount++
			if callCount == 1 {
				// simulate task 2 completion: update plan file and signal completed
				updated := strings.ReplaceAll(planContent, "- [ ] build it", "- [x] build it")
				_ = os.WriteFile(planFile, []byte(updated), 0o600)
				return executor.Result{Output: "task done", Signal: status.Completed}
			}
			return executor.Result{Error: errors.New("no more mock results")}
		},
	}
	codex := newMockExecutor(nil)

	cfg := processor.Config{Mode: processor.ModeTasksOnly, PlanFile: planFile, MaxIterations: 50, AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	err := r.Run(t.Context())

	require.NoError(t, err)

	// verify section was printed with task position 2 (not loop counter 1)
	require.NotEmpty(t, log.PrintSectionCalls())
	var foundTaskSection bool
	for _, call := range log.PrintSectionCalls() {
		if call.Section.Iteration == 2 && strings.Contains(call.Section.Label, "task iteration 2") {
			foundTaskSection = true
			break
		}
	}
	assert.True(t, foundTaskSection, "should print section with task position 2, got sections: %v",
		func() []string {
			calls := log.PrintSectionCalls()
			labels := make([]string, 0, len(calls))
			for _, c := range calls {
				labels = append(labels, c.Section.Label)
			}
			return labels
		}())
}

func TestRunner_RunWithLimitRetry_RetryOnLimitError(t *testing.T) {
	log := newMockLogger("")
	claude := newMockExecutor(nil) // not used directly
	codex := newMockExecutor(nil)

	appCfg := testAppConfig(t)
	appCfg.WaitOnLimit = 10 * time.Millisecond
	appCfg.WaitOnLimitSet = true

	cfg := processor.Config{AppConfig: appCfg}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})

	// mock run function: returns LimitPatternError on first call, success on second
	callCount := 0
	mockRun := func(_ context.Context, _ string) executor.Result {
		callCount++
		if callCount == 1 {
			return executor.Result{Error: &executor.LimitPatternError{Pattern: "You've hit your limit", HelpCmd: "claude /usage"}}
		}
		return executor.Result{Output: "success", Signal: status.Completed}
	}

	result := r.TestRunWithLimitRetry(t.Context(), mockRun, "test prompt", "claude")

	require.NoError(t, result.Error)
	assert.Equal(t, "success", result.Output)
	assert.Equal(t, 2, callCount, "should retry after limit error")

	// verify rate limit message was logged
	var foundLog bool
	for _, call := range log.PrintCalls() {
		if strings.Contains(call.Format, "rate limit detected") {
			foundLog = true
			break
		}
	}
	assert.True(t, foundLog, "should log rate limit detection message")
}

func TestRunner_RunWithLimitRetry_NoRetryWhenWaitZero(t *testing.T) {
	log := newMockLogger("")
	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)

	// waitOnLimit is zero (default) - no retry
	cfg := processor.Config{AppConfig: testAppConfig(t)}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})

	// verify wait is zero
	assert.Zero(t, r.TestConfig().WaitOnLimit)

	callCount := 0
	mockRun := func(_ context.Context, _ string) executor.Result {
		callCount++
		return executor.Result{Error: &executor.LimitPatternError{Pattern: "You've hit your limit", HelpCmd: "claude /usage"}}
	}

	result := r.TestRunWithLimitRetry(t.Context(), mockRun, "test prompt", "claude")

	require.Error(t, result.Error)
	var limitErr *executor.LimitPatternError
	require.ErrorAs(t, result.Error, &limitErr)
	assert.Equal(t, "You've hit your limit", limitErr.Pattern)
	assert.Equal(t, 1, callCount, "should not retry when wait is zero")
}

func TestRunner_RunWithLimitRetry_ContextCancelledDuringWait(t *testing.T) {
	log := newMockLogger("")
	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)

	appCfg := testAppConfig(t)
	appCfg.WaitOnLimit = 10 * time.Second // long wait
	appCfg.WaitOnLimitSet = true

	cfg := processor.Config{AppConfig: appCfg}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})

	ctx, cancel := context.WithCancel(t.Context())

	callCount := 0
	mockRun := func(_ context.Context, _ string) executor.Result {
		callCount++
		// cancel context right after first call to simulate interruption during wait
		cancel()
		return executor.Result{Error: &executor.LimitPatternError{Pattern: "limit hit", HelpCmd: "claude /usage"}}
	}

	result := r.TestRunWithLimitRetry(ctx, mockRun, "test prompt", "claude")

	require.Error(t, result.Error)
	require.ErrorIs(t, result.Error, context.Canceled)
	assert.Equal(t, 1, callCount, "should not retry when context canceled")
}

func TestRunner_RunWithLimitRetry_PatternMatchErrorNotRetried(t *testing.T) {
	log := newMockLogger("")
	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)

	appCfg := testAppConfig(t)
	appCfg.WaitOnLimit = 10 * time.Millisecond
	appCfg.WaitOnLimitSet = true

	cfg := processor.Config{AppConfig: appCfg}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})

	callCount := 0
	mockRun := func(_ context.Context, _ string) executor.Result {
		callCount++
		return executor.Result{Error: &executor.PatternMatchError{Pattern: "API Error", HelpCmd: "claude /usage"}}
	}

	result := r.TestRunWithLimitRetry(t.Context(), mockRun, "test prompt", "claude")

	require.Error(t, result.Error)
	var patternErr *executor.PatternMatchError
	require.ErrorAs(t, result.Error, &patternErr)
	assert.Equal(t, "API Error", patternErr.Pattern)
	assert.Equal(t, 1, callCount, "should not retry PatternMatchError")
}

func TestRunner_RunWithLimitRetry_MultipleRetries(t *testing.T) {
	log := newMockLogger("")
	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)

	appCfg := testAppConfig(t)
	appCfg.WaitOnLimit = 5 * time.Millisecond
	appCfg.WaitOnLimitSet = true

	cfg := processor.Config{AppConfig: appCfg}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})

	callCount := 0
	mockRun := func(_ context.Context, _ string) executor.Result {
		callCount++
		if callCount <= 3 {
			return executor.Result{Error: &executor.LimitPatternError{Pattern: "rate limit", HelpCmd: "claude /usage"}}
		}
		return executor.Result{Output: "finally done"}
	}

	result := r.TestRunWithLimitRetry(t.Context(), mockRun, "test prompt", "claude")

	require.NoError(t, result.Error)
	assert.Equal(t, "finally done", result.Output)
	assert.Equal(t, 4, callCount, "should retry multiple times until success")
}

func TestRunner_RunWithLimitRetry_NoErrorPassesThrough(t *testing.T) {
	log := newMockLogger("")
	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)

	appCfg := testAppConfig(t)
	appCfg.WaitOnLimit = time.Hour
	appCfg.WaitOnLimitSet = true

	cfg := processor.Config{AppConfig: appCfg}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})

	callCount := 0
	mockRun := func(_ context.Context, _ string) executor.Result {
		callCount++
		return executor.Result{Output: "success", Signal: status.Completed}
	}

	result := r.TestRunWithLimitRetry(t.Context(), mockRun, "test prompt", "claude")

	require.NoError(t, result.Error)
	assert.Equal(t, "success", result.Output)
	assert.Equal(t, status.Completed, result.Signal)
	assert.Equal(t, 1, callCount, "should not retry on success")
}

func TestRunner_WaitOnLimit_PopulatedFromConfig(t *testing.T) {
	log := newMockLogger("")
	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)

	appCfg := testAppConfig(t)
	appCfg.WaitOnLimit = 45 * time.Minute
	appCfg.WaitOnLimitSet = true

	cfg := processor.Config{AppConfig: appCfg}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})

	assert.Equal(t, 45*time.Minute, r.TestConfig().WaitOnLimit)
}

func TestRunner_WaitOnLimit_ZeroWhenNoConfig(t *testing.T) {
	log := newMockLogger("")
	claude := newMockExecutor(nil)
	codex := newMockExecutor(nil)

	cfg := processor.Config{} // no AppConfig
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})

	assert.Zero(t, r.TestConfig().WaitOnLimit)
}

func TestRunner_Finalize_LimitPatternWithWaitRetries(t *testing.T) {
	log := newMockLogger("progress.txt")

	callCount := 0
	claude := &mocks.ExecutorMock{
		RunFunc: func(_ context.Context, _ string) executor.Result {
			callCount++
			if callCount == 1 {
				// first call during finalize: codex review loop (returns ReviewDone)
				return executor.Result{Output: "review done", Signal: status.ReviewDone}
			}
			if callCount == 2 {
				// finalize: limit error on first attempt
				return executor.Result{Error: &executor.LimitPatternError{Pattern: "limit hit", HelpCmd: "claude /usage"}}
			}
			// finalize: success on retry
			return executor.Result{Output: "finalize done", Signal: status.ReviewDone}
		},
	}
	codex := newMockExecutor(nil)

	appCfg := testAppConfig(t)
	appCfg.WaitOnLimit = 10 * time.Millisecond
	appCfg.WaitOnLimitSet = true

	cfg := processor.Config{
		Mode:            processor.ModeCodexOnly,
		MaxIterations:   50,
		CodexEnabled:    false, // skip codex phase
		FinalizeEnabled: true,
		AppConfig:       appCfg,
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	err := r.Run(t.Context())

	require.NoError(t, err)
	assert.Equal(t, 3, callCount, "should retry finalize after limit error")
}

func TestRunner_Finalize_LimitPatternWithoutWaitLogsAndContinues(t *testing.T) {
	log := newMockLogger("progress.txt")

	callCount := 0
	claude := &mocks.ExecutorMock{
		RunFunc: func(_ context.Context, _ string) executor.Result {
			callCount++
			if callCount == 1 {
				return executor.Result{Output: "review done", Signal: status.ReviewDone}
			}
			// finalize: limit error, no wait configured
			return executor.Result{Error: &executor.LimitPatternError{Pattern: "limit hit", HelpCmd: "claude /usage"}}
		},
	}
	codex := newMockExecutor(nil)

	cfg := processor.Config{
		Mode:            processor.ModeCodexOnly,
		MaxIterations:   50,
		CodexEnabled:    false,
		FinalizeEnabled: true,
		AppConfig:       testAppConfig(t), // default: WaitOnLimit = 0
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	err := r.Run(t.Context())

	require.NoError(t, err, "finalize limit error should not block success (best-effort)")
	assert.Equal(t, 2, callCount, "should not retry when wait is zero")

	// verify limit log message (handlePatternMatchError logs "error: detected %q in %s output")
	var foundLog bool
	for _, call := range log.PrintCalls() {
		if strings.Contains(call.Format, "detected") {
			foundLog = true
			break
		}
	}
	assert.True(t, foundLog, "should log limit pattern message via handlePatternMatchError")
}

func TestRunner_ExternalReviewLoop_StalemateDetection_BreaksAfterN(t *testing.T) {
	log := newMockLogger("progress.txt")

	// codex-only flow: external review loop → post-codex claude review loop
	// with ReviewPatience=2, loop should break after 2 unchanged rounds
	// external loop: codex run → claude eval (no signal, no commit) → codex run → claude eval (no signal, no commit) → stalemate break
	claude := newMockExecutor([]executor.Result{
		{Output: "rejected findings, no changes"},   // claude eval iteration 1 - no CodexDone signal
		{Output: "rejected findings again"},         // claude eval iteration 2 - no CodexDone signal (stalemate break here)
		{Output: "done", Signal: status.ReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Output: "found issue in foo.go:10"}, // codex iteration 1
		{Output: "found issue in foo.go:10"}, // codex iteration 2
	})

	// git checker returns same hash and diff every time (no changes made by claude)
	gitMock := &mocks.GitCheckerMock{
		HeadHashFunc:        func() (string, error) { return "abc123def456abc123def456abc123def456abcd", nil },
		DiffFingerprintFunc: func() (string, error) { return "unchanged-diff", nil },
	}

	cfg := processor.Config{
		Mode: processor.ModeCodexOnly, MaxIterations: 50, IterationDelayMs: 1, CodexEnabled: true,
		ReviewPatience: 2, AppConfig: testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	r.SetGitChecker(gitMock)
	err := r.Run(t.Context())

	require.NoError(t, err)
	assert.Len(t, codex.RunCalls(), 2, "codex should run exactly 2 times before stalemate")
	assert.Len(t, claude.RunCalls(), 3, "claude: 2 evals + 1 post-codex review")

	// verify stalemate log message
	var foundStalemate bool
	for _, call := range log.PrintCalls() {
		if strings.Contains(call.Format, "stalemate detected") && strings.Contains(call.Format, "unchanged rounds") {
			foundStalemate = true
			break
		}
	}
	assert.True(t, foundStalemate, "should log stalemate detection message")
}

func TestRunner_ExternalReviewLoop_StalemateDetection_ResetsOnCommit(t *testing.T) {
	log := newMockLogger("progress.txt")

	// with ReviewPatience=2, if hash changes on round 2, counter resets and needs 2 more unchanged
	// round 1: unchanged (counter=1), round 2: changed (counter=0), round 3: codex done
	claude := newMockExecutor([]executor.Result{
		{Output: "rejected findings, no changes"},          // claude eval iteration 1 (unchanged)
		{Output: "fixed the issue, committed"},             // claude eval iteration 2 (hash changed)
		{Output: "done", Signal: status.CodexDone},         // claude eval iteration 3 (codex done signal)
		{Output: "review done", Signal: status.ReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Output: "found issue in foo.go:10"}, // codex iteration 1
		{Output: "found issue in bar.go:20"}, // codex iteration 2
		{Output: "found issue in baz.go:30"}, // codex iteration 3
	})

	// git checker: same hash for round 1 (before+after), different hash for round 2
	hashIdx := 0
	hashes := []string{
		"aaaa00000000000000000000000000000000aaaa", // round 1 before
		"aaaa00000000000000000000000000000000aaaa", // round 1 after (unchanged)
		"aaaa00000000000000000000000000000000aaaa", // round 2 before
		"bbbb00000000000000000000000000000000bbbb", // round 2 after (changed - reset)
		"bbbb00000000000000000000000000000000bbbb", // round 3 before (codex done, no after call)
	}
	gitMock := &mocks.GitCheckerMock{
		HeadHashFunc: func() (string, error) {
			if hashIdx >= len(hashes) {
				return "ffff00000000000000000000000000000000ffff", nil
			}
			h := hashes[hashIdx]
			hashIdx++
			return h, nil
		},
		DiffFingerprintFunc: func() (string, error) { return "constant-diff", nil },
	}

	cfg := processor.Config{
		Mode: processor.ModeCodexOnly, MaxIterations: 50, IterationDelayMs: 1, CodexEnabled: true,
		ReviewPatience: 2, AppConfig: testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	r.SetGitChecker(gitMock)
	err := r.Run(t.Context())

	require.NoError(t, err)
	assert.Len(t, codex.RunCalls(), 3, "codex should run 3 times (no stalemate)")
	assert.Len(t, claude.RunCalls(), 4, "claude: 3 evals + 1 post-codex review")

	// verify no stalemate log
	var foundStalemate bool
	for _, call := range log.PrintCalls() {
		if strings.Contains(call.Format, "stalemate detected") {
			foundStalemate = true
			break
		}
	}
	assert.False(t, foundStalemate, "should not log stalemate when counter was reset")
}

func TestRunner_ExternalReviewLoop_StalemateDetection_ResetsOnDiffChange(t *testing.T) {
	log := newMockLogger("progress.txt")

	// with ReviewPatience=2, if diff fingerprint changes on round 2 (working tree edits without commit),
	// counter resets and needs 2 more unchanged rounds. HEAD stays the same throughout (no commits).
	// round 1: unchanged (counter=1), round 2: diff changed (counter=0), round 3: codex done
	claude := newMockExecutor([]executor.Result{
		{Output: "rejected findings, no changes"},          // claude eval iteration 1 (no edits)
		{Output: "fixed the issue"},                        // claude eval iteration 2 (edited files, no commit)
		{Output: "done", Signal: status.CodexDone},         // claude eval iteration 3 (codex done signal)
		{Output: "review done", Signal: status.ReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Output: "found issue in foo.go:10"}, // codex iteration 1
		{Output: "found issue in bar.go:20"}, // codex iteration 2
		{Output: "found issue in baz.go:30"}, // codex iteration 3
	})

	// git checker: HEAD never changes (no commits), but diff fingerprint changes on round 2
	diffIdx := 0
	diffs := []string{
		"diff-aaa", // round 1 before
		"diff-aaa", // round 1 after (unchanged - stalemate round)
		"diff-aaa", // round 2 before
		"diff-bbb", // round 2 after (changed - claude edited files)
		"diff-bbb", // round 3 before (codex done, no after call)
	}
	gitMock := &mocks.GitCheckerMock{
		HeadHashFunc: func() (string, error) { return "abc123def456abc123def456abc123def456abcd", nil },
		DiffFingerprintFunc: func() (string, error) {
			if diffIdx >= len(diffs) {
				return "diff-zzz", nil
			}
			d := diffs[diffIdx]
			diffIdx++
			return d, nil
		},
	}

	cfg := processor.Config{
		Mode: processor.ModeCodexOnly, MaxIterations: 50, IterationDelayMs: 1, CodexEnabled: true,
		ReviewPatience: 2, AppConfig: testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	r.SetGitChecker(gitMock)
	err := r.Run(t.Context())

	require.NoError(t, err)
	assert.Len(t, codex.RunCalls(), 3, "codex should run 3 times (no stalemate, diff change reset counter)")
	assert.Len(t, claude.RunCalls(), 4, "claude: 3 evals + 1 post-codex review")

	// verify no stalemate log
	var foundStalemate bool
	for _, call := range log.PrintCalls() {
		if strings.Contains(call.Format, "stalemate detected") {
			foundStalemate = true
			break
		}
	}
	assert.False(t, foundStalemate, "should not log stalemate when diff fingerprint changed")
}

func TestRunner_ExternalReviewLoop_StalemateDetection_DisabledWhenZero(t *testing.T) {
	log := newMockLogger("progress.txt")

	// with ReviewPatience=0 (disabled), loop runs to max iterations even with unchanged HEAD
	// max external iterations = max(3, 50/5) = 10, but we limit to 3 via MaxExternalIterations
	claude := newMockExecutor([]executor.Result{
		{Output: "rejected findings"},               // claude eval iteration 1
		{Output: "rejected findings"},               // claude eval iteration 2
		{Output: "rejected findings"},               // claude eval iteration 3
		{Output: "done", Signal: status.ReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Output: "found issue"}, // codex iteration 1
		{Output: "found issue"}, // codex iteration 2
		{Output: "found issue"}, // codex iteration 3
	})

	// git checker returns same hash and diff (would trigger stalemate if enabled)
	gitMock := &mocks.GitCheckerMock{
		HeadHashFunc:        func() (string, error) { return "abc123def456abc123def456abc123def456abcd", nil },
		DiffFingerprintFunc: func() (string, error) { return "unchanged-diff", nil },
	}

	cfg := processor.Config{
		Mode: processor.ModeCodexOnly, MaxIterations: 50, IterationDelayMs: 1, CodexEnabled: true,
		ReviewPatience: 0, MaxExternalIterations: 3, AppConfig: testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	r.SetGitChecker(gitMock)
	err := r.Run(t.Context())

	require.NoError(t, err)
	assert.Len(t, codex.RunCalls(), 3, "codex should run all 3 iterations (stalemate disabled)")
	assert.Len(t, claude.RunCalls(), 4, "claude: 3 evals + 1 post-codex review")

	// verify no stalemate log
	var foundStalemate bool
	for _, call := range log.PrintCalls() {
		if strings.Contains(call.Format, "stalemate detected") {
			foundStalemate = true
			break
		}
	}
	assert.False(t, foundStalemate, "should not log stalemate when ReviewPatience=0")
}

func TestRunner_ExternalReviewLoop_StalemateDetection_NilGitChecker(t *testing.T) {
	log := newMockLogger("progress.txt")

	// with ReviewPatience=2 and nil git checker, stalemate detection should be skipped gracefully
	// headHash() returns "" when git is nil, and stalemate check requires headBefore != ""
	// max external iterations limited to 3
	claude := newMockExecutor([]executor.Result{
		{Output: "rejected findings"},               // claude eval iteration 1
		{Output: "rejected findings"},               // claude eval iteration 2
		{Output: "rejected findings"},               // claude eval iteration 3
		{Output: "done", Signal: status.ReviewDone}, // post-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Output: "found issue"}, // codex iteration 1
		{Output: "found issue"}, // codex iteration 2
		{Output: "found issue"}, // codex iteration 3
	})

	// no git checker set (nil)
	cfg := processor.Config{
		Mode: processor.ModeCodexOnly, MaxIterations: 50, IterationDelayMs: 1, CodexEnabled: true,
		ReviewPatience: 2, MaxExternalIterations: 3, AppConfig: testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	// deliberately NOT calling r.SetGitChecker()
	err := r.Run(t.Context())

	require.NoError(t, err)
	assert.Len(t, codex.RunCalls(), 3, "codex should run all 3 iterations (git checker nil)")
	assert.Len(t, claude.RunCalls(), 4, "claude: 3 evals + 1 post-codex review")

	// verify no stalemate log
	var foundStalemate bool
	for _, call := range log.PrintCalls() {
		if strings.Contains(call.Format, "stalemate detected") {
			foundStalemate = true
			break
		}
	}
	assert.False(t, foundStalemate, "should not log stalemate when git checker is nil")
}

func TestRunner_ExternalReviewLoop_BreakChannel_ExitsEarly(t *testing.T) {
	log := newMockLogger("progress.txt")

	// break channel is closed during codex execution, causing context cancellation.
	// codex-only flow: codex run (break fires) → loop exits → post-codex claude review.
	breakCh := make(chan struct{})

	claude := &mocks.ExecutorMock{
		RunFunc: func(_ context.Context, _ string) executor.Result {
			return executor.Result{Output: "done", Signal: status.ReviewDone}
		},
	}
	codex := &mocks.ExecutorMock{
		RunFunc: func(ctx context.Context, _ string) executor.Result {
			close(breakCh) // trigger break during codex execution
			<-ctx.Done()   // wait for context cancellation
			return executor.Result{Error: ctx.Err()}
		},
	}

	cfg := processor.Config{
		Mode: processor.ModeCodexOnly, MaxIterations: 50, CodexEnabled: true,
		MaxExternalIterations: 5, AppConfig: testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	r.SetBreakCh(breakCh)

	err := r.Run(t.Context())
	require.NoError(t, err)

	// codex called once (interrupted by break)
	assert.Len(t, codex.RunCalls(), 1, "codex should run once before break interrupts it")

	// claude called once for post-codex review (after break exits external loop)
	assert.Len(t, claude.RunCalls(), 1, "claude should be called once for post-codex review")

	// verify break log message
	var foundBreak bool
	for _, call := range log.PrintCalls() {
		if strings.Contains(call.Format, "manual break requested") {
			foundBreak = true
			break
		}
	}
	assert.True(t, foundBreak, "should log manual break message")
}

func TestRunner_ExternalReviewLoop_NilBreakChannel_RunsNormally(t *testing.T) {
	log := newMockLogger("progress.txt")

	// nil break channel: loop runs to completion based on CodexDone signal
	claude := newMockExecutor([]executor.Result{
		{Output: "no issues", Signal: status.CodexDone}, // claude eval (codex done)
		{Output: "done", Signal: status.ReviewDone},     // post-codex review loop
	})
	codex := newMockExecutor([]executor.Result{
		{Output: "found issue in foo.go:10"}, // codex iteration 1
	})

	cfg := processor.Config{
		Mode: processor.ModeCodexOnly, MaxIterations: 50, CodexEnabled: true,
		MaxExternalIterations: 5, AppConfig: testAppConfig(t),
	}
	r := processor.NewWithExecutors(cfg, log, claude, codex, nil, &status.PhaseHolder{})
	// deliberately NOT calling r.SetBreakCh() — nil channel

	err := r.Run(t.Context())
	require.NoError(t, err)

	assert.Len(t, codex.RunCalls(), 1, "codex should run once")
	assert.Len(t, claude.RunCalls(), 2, "claude: 1 eval + 1 post-codex review")

	// verify no break log
	var foundBreak bool
	for _, call := range log.PrintCalls() {
		if strings.Contains(call.Format, "manual break") {
			foundBreak = true
			break
		}
	}
	assert.False(t, foundBreak, "should not log manual break with nil channel")
}
