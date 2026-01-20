package processor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/executor"
	"github.com/umputun/ralphex/pkg/progress"
)

// mockExecutor is a test double for Executor interface.
type mockExecutor struct {
	results []executor.Result
	calls   []string
	idx     int
}

func (m *mockExecutor) Run(_ context.Context, prompt string) executor.Result {
	m.calls = append(m.calls, prompt)
	if m.idx >= len(m.results) {
		return executor.Result{Error: errors.New("no more mock results")}
	}
	result := m.results[m.idx]
	m.idx++
	return result
}

// mockLogger is a test double for Logger interface.
type mockLogger struct {
	messages []string
	phase    progress.Phase
	path     string
}

func (m *mockLogger) SetPhase(phase progress.Phase) {
	m.phase = phase
}

func (m *mockLogger) Print(format string, args ...any) {
	m.messages = append(m.messages, format)
}

func (m *mockLogger) PrintRaw(format string, _ ...any) {
	m.messages = append(m.messages, format)
}

func (m *mockLogger) PrintAligned(text string) {
	m.messages = append(m.messages, text)
}

func (m *mockLogger) Path() string {
	return m.path
}

func TestRunner_Run_UnknownMode(t *testing.T) {
	log := &mockLogger{}
	claude := &mockExecutor{}
	codex := &mockExecutor{}

	r := NewWithExecutors(Config{Mode: "invalid"}, log, claude, codex)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown mode")
}

func TestRunner_RunFull_NoPlanFile(t *testing.T) {
	log := &mockLogger{}
	claude := &mockExecutor{}
	codex := &mockExecutor{}

	r := NewWithExecutors(Config{Mode: ModeFull}, log, claude, codex)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "plan file required")
}

func TestRunner_RunFull_Success(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	// all tasks complete - no [ ] checkboxes
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n- [x] Task 1"), 0o600))

	log := &mockLogger{path: "progress.txt"}
	claude := &mockExecutor{
		results: []executor.Result{
			{Output: "task done", Signal: SignalCompleted},    // task phase completes
			{Output: "review done", Signal: SignalReviewDone}, // first review
			{Output: "review done", Signal: SignalReviewDone}, // pre-codex review loop
			{Output: "done", Signal: SignalCodexDone},         // codex evaluation
			{Output: "review done", Signal: SignalReviewDone}, // post-codex review loop
		},
	}
	codex := &mockExecutor{
		results: []executor.Result{
			{Output: "found issue in foo.go"}, // codex finds issues
		},
	}

	r := NewWithExecutors(Config{Mode: ModeFull, PlanFile: planFile, MaxIterations: 50}, log, claude, codex)
	err := r.Run(context.Background())

	require.NoError(t, err)
	assert.Len(t, codex.calls, 1)
}

func TestRunner_RunFull_NoCodexFindings(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n- [x] Task 1"), 0o600))

	log := &mockLogger{path: "progress.txt"}
	claude := &mockExecutor{
		results: []executor.Result{
			{Output: "task done", Signal: SignalCompleted},
			{Output: "review done", Signal: SignalReviewDone}, // first review
			{Output: "review done", Signal: SignalReviewDone}, // pre-codex review loop
			{Output: "review done", Signal: SignalReviewDone}, // post-codex review loop
		},
	}
	codex := &mockExecutor{
		results: []executor.Result{
			{Output: ""}, // codex finds nothing
		},
	}

	r := NewWithExecutors(Config{Mode: ModeFull, PlanFile: planFile, MaxIterations: 50}, log, claude, codex)
	err := r.Run(context.Background())

	require.NoError(t, err)
}

func TestRunner_RunReviewOnly_Success(t *testing.T) {
	log := &mockLogger{path: "progress.txt"}
	claude := &mockExecutor{
		results: []executor.Result{
			{Output: "review done", Signal: SignalReviewDone}, // first review
			{Output: "review done", Signal: SignalReviewDone}, // pre-codex review loop
			{Output: "done", Signal: SignalCodexDone},         // codex evaluation
			{Output: "review done", Signal: SignalReviewDone}, // post-codex review loop
		},
	}
	codex := &mockExecutor{
		results: []executor.Result{
			{Output: "found issue"},
		},
	}

	r := NewWithExecutors(Config{Mode: ModeReview, MaxIterations: 50}, log, claude, codex)
	err := r.Run(context.Background())

	require.NoError(t, err)
	assert.Len(t, codex.calls, 1)
}

func TestRunner_RunCodexOnly_Success(t *testing.T) {
	log := &mockLogger{path: "progress.txt"}
	claude := &mockExecutor{
		results: []executor.Result{
			{Output: "done", Signal: SignalCodexDone},         // codex evaluation
			{Output: "review done", Signal: SignalReviewDone}, // post-codex review loop
		},
	}
	codex := &mockExecutor{
		results: []executor.Result{
			{Output: "found issue"},
		},
	}

	r := NewWithExecutors(Config{Mode: ModeCodexOnly, MaxIterations: 50}, log, claude, codex)
	err := r.Run(context.Background())

	require.NoError(t, err)
	assert.Len(t, codex.calls, 1)
}

func TestRunner_RunCodexOnly_NoFindings(t *testing.T) {
	log := &mockLogger{path: "progress.txt"}
	claude := &mockExecutor{
		results: []executor.Result{
			{Output: "review done", Signal: SignalReviewDone}, // post-codex review loop
		},
	}
	codex := &mockExecutor{
		results: []executor.Result{
			{Output: ""}, // no findings
		},
	}

	r := NewWithExecutors(Config{Mode: ModeCodexOnly, MaxIterations: 50}, log, claude, codex)
	err := r.Run(context.Background())

	require.NoError(t, err)
}

func TestRunner_TaskPhase_FailedSignal(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

	log := &mockLogger{path: "progress.txt"}
	claude := &mockExecutor{
		results: []executor.Result{
			{Output: "error", Signal: SignalFailed}, // first try
			{Output: "error", Signal: SignalFailed}, // retry
		},
	}
	codex := &mockExecutor{}

	r := NewWithExecutors(Config{Mode: ModeFull, PlanFile: planFile, MaxIterations: 10}, log, claude, codex)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "FAILED signal")
}

func TestRunner_TaskPhase_MaxIterations(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n- [ ] Task 1"), 0o600))

	log := &mockLogger{path: "progress.txt"}
	claude := &mockExecutor{
		results: []executor.Result{
			{Output: "working..."},
			{Output: "still working..."},
			{Output: "more work..."},
		},
	}
	codex := &mockExecutor{}

	r := NewWithExecutors(Config{Mode: ModeFull, PlanFile: planFile, MaxIterations: 3}, log, claude, codex)
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

	log := &mockLogger{path: "progress.txt"}
	claude := &mockExecutor{}
	codex := &mockExecutor{}

	r := NewWithExecutors(Config{Mode: ModeFull, PlanFile: planFile, MaxIterations: 10}, log, claude, codex)
	err := r.Run(ctx)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestRunner_ClaudeReview_FailedSignal(t *testing.T) {
	log := &mockLogger{path: "progress.txt"}
	claude := &mockExecutor{
		results: []executor.Result{
			{Output: "error", Signal: SignalFailed},
		},
	}
	codex := &mockExecutor{}

	r := NewWithExecutors(Config{Mode: ModeReview, MaxIterations: 50}, log, claude, codex)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "FAILED signal")
}

func TestRunner_CodexPhase_Error(t *testing.T) {
	log := &mockLogger{path: "progress.txt"}
	claude := &mockExecutor{
		results: []executor.Result{
			{Output: "review done", Signal: SignalReviewDone}, // first review
			{Output: "review done", Signal: SignalReviewDone}, // pre-codex review loop
		},
	}
	codex := &mockExecutor{
		results: []executor.Result{
			{Error: errors.New("codex error")},
		},
	}

	r := NewWithExecutors(Config{Mode: ModeReview, MaxIterations: 50}, log, claude, codex)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "codex")
}

func TestRunner_ClaudeExecution_Error(t *testing.T) {
	tmpDir := t.TempDir()
	planFile := filepath.Join(tmpDir, "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

	log := &mockLogger{path: "progress.txt"}
	claude := &mockExecutor{
		results: []executor.Result{
			{Error: errors.New("claude error")},
		},
	}
	codex := &mockExecutor{}

	r := NewWithExecutors(Config{Mode: ModeFull, PlanFile: planFile, MaxIterations: 10}, log, claude, codex)
	err := r.Run(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "claude execution")
}

func TestRunner_hasUncompletedTasks(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("with uncompleted tasks", func(t *testing.T) {
		planFile := filepath.Join(tmpDir, "uncompleted.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n- [ ] Task 1\n- [x] Task 2"), 0o600))

		r := &Runner{cfg: Config{PlanFile: planFile}}
		assert.True(t, r.hasUncompletedTasks())
	})

	t.Run("all tasks completed", func(t *testing.T) {
		planFile := filepath.Join(tmpDir, "completed.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n- [x] Task 1\n- [x] Task 2"), 0o600))

		r := &Runner{cfg: Config{PlanFile: planFile}}
		assert.False(t, r.hasUncompletedTasks())
	})

	t.Run("missing file", func(t *testing.T) {
		r := &Runner{cfg: Config{PlanFile: "/nonexistent/file.md"}}
		assert.True(t, r.hasUncompletedTasks())
	})
}
