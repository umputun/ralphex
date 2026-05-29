package phase

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/config"
	"github.com/umputun/ralphex/pkg/executor"
	"github.com/umputun/ralphex/pkg/status"
)

type reviewPhaseTestOpts struct {
	cfg  Config
	exec Executor
	log  *mockLogger
}

func reviewPhaseFromRunner(t *testing.T, opts reviewPhaseTestOpts) (*reviewPhase, *mockLogger) {
	t.Helper()
	if opts.cfg.AppConfig == nil {
		opts.cfg.AppConfig = testAppConfig(t)
	}
	if opts.log == nil {
		opts.log = newMockLogger("progress.txt")
	}
	if opts.exec == nil {
		opts.exec = newTaskPhaseMockExecutor(nil)
	}
	r := newTestRunner(testRunnerOpts{cfg: opts.cfg, log: opts.log, execs: Executors{Task: opts.exec, Review: opts.exec}, holder: &status.PhaseHolder{}})
	phase, ok := r.phases.review.(*reviewPhase)
	require.True(t, ok)
	return phase, opts.log
}

func TestReviewPhase_First_FailedSignal(t *testing.T) {
	exec := newTaskPhaseMockExecutor([]executor.Result{{Output: "error", Signal: status.Failed}})
	phase, _ := reviewPhaseFromRunner(t, reviewPhaseTestOpts{cfg: Config{MaxIterations: 50}, exec: exec})

	err := phase.First(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "FAILED signal")
}

func TestReviewPhase_First_PatternMatchError(t *testing.T) {
	exec := newTaskPhaseMockExecutor([]executor.Result{{Error: &executor.PatternMatchError{Pattern: "limit", HelpCmd: "usage"}}})
	phase, log := reviewPhaseFromRunner(t, reviewPhaseTestOpts{cfg: Config{MaxIterations: 50}, exec: exec})

	err := phase.First(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "limit")
	assertLogContains(t, log, "detected")
}

func TestReviewPhase_Loop_PatternMatchError(t *testing.T) {
	exec := newTaskPhaseMockExecutor([]executor.Result{{Error: &executor.PatternMatchError{Pattern: "boom", HelpCmd: "usage"}}})
	phase, log := reviewPhaseFromRunner(t, reviewPhaseTestOpts{cfg: Config{MaxIterations: 50}, exec: exec})

	err := phase.Loop(t.Context(), "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
	assertLogContains(t, log, "detected")
}

func TestWrapExecutorError(t *testing.T) {
	t.Run("nil error returns nil", func(t *testing.T) {
		assert.NoError(t, wrapExecutorError(newScriptedTestPolicy(newMockLogger("")), nil, "claude"))
	})

	t.Run("pattern match wraps as pattern handling", func(t *testing.T) {
		policy := newScriptedTestPolicy(newMockLogger(""))
		patternErr := &executor.PatternMatchError{Pattern: "rate limit", HelpCmd: "usage"}
		err := wrapExecutorError(policy, patternErr, "codex")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "codex pattern handling:")
		assert.Contains(t, err.Error(), "rate limit")
	})

	t.Run("plain error wraps as execution", func(t *testing.T) {
		err := wrapExecutorError(newScriptedTestPolicy(newMockLogger("")), errors.New("boom"), "claude")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "claude execution:")
		assert.Contains(t, err.Error(), "boom")
	})
}

func TestReviewPhase_Loop_NoCommitExit(t *testing.T) {
	exec := newTaskPhaseMockExecutor([]executor.Result{{Output: "looked at code, nothing to fix"}})
	phase, log := reviewPhaseFromRunner(t, reviewPhaseTestOpts{cfg: Config{MaxIterations: 50}, exec: exec})
	phase.git.deps.Git = &gitCheckerMock{
		HeadHashFunc:        func() (string, error) { return "abc123def456abc123def456abc123def456abcd", nil },
		DiffFingerprintFunc: func() (string, error) { return "unchanged-diff", nil },
	}

	err := phase.Loop(t.Context(), "")

	require.NoError(t, err)
	assert.Len(t, exec.RunCalls(), 1)
	assertLogContains(t, log, "no changes detected")
}

func TestReviewPhase_Loop_CommitDetectedContinues(t *testing.T) {
	exec := newTaskPhaseMockExecutor([]executor.Result{{Output: "fixed issues"}, {Output: "review done", Signal: status.ReviewDone}})
	phase, _ := reviewPhaseFromRunner(t, reviewPhaseTestOpts{cfg: Config{MaxIterations: 50}, exec: exec})
	hashes := []string{
		"aaaa00000000000000000000000000000000aaaa",
		"bbbb00000000000000000000000000000000bbbb",
		"bbbb00000000000000000000000000000000bbbb",
	}
	idx := 0
	gitMock := &gitCheckerMock{HeadHashFunc: func() (string, error) {
		require.Less(t, idx, len(hashes), "unexpected extra HeadHash call #%d", idx)
		hash := hashes[idx]
		idx++
		return hash, nil
	}}
	phase.git.deps.Git = gitMock

	err := phase.Loop(t.Context(), "")

	require.NoError(t, err)
	assert.Len(t, exec.RunCalls(), 2)
	assert.Len(t, gitMock.HeadHashCalls(), 3)
}

func TestReviewPhase_Loop_GitCheckerNilSkipsNoCommitCheck(t *testing.T) {
	exec := newTaskPhaseMockExecutor([]executor.Result{{Output: "looking"}, {Output: "looking"}, {Output: "looking"}})
	phase, _ := reviewPhaseFromRunner(t, reviewPhaseTestOpts{cfg: Config{MaxIterations: 30}, exec: exec})

	err := phase.Loop(t.Context(), "")

	require.NoError(t, err)
	assert.Len(t, exec.RunCalls(), 3)
}

func TestReviewPhase_Loop_GitCheckerErrorSkipsNoCommitCheck(t *testing.T) {
	exec := newTaskPhaseMockExecutor([]executor.Result{{Output: "looking"}, {Output: "looking"}, {Output: "looking"}})
	phase, _ := reviewPhaseFromRunner(t, reviewPhaseTestOpts{cfg: Config{MaxIterations: 30}, exec: exec})
	phase.git.deps.Git = &gitCheckerMock{
		HeadHashFunc:        func() (string, error) { return "", errors.New("git HEAD error") },
		DiffFingerprintFunc: func() (string, error) { return "", errors.New("git diff error") },
	}

	err := phase.Loop(t.Context(), "")

	require.NoError(t, err)
	assert.Len(t, exec.RunCalls(), 3)
}

func TestReviewPhase_First_TimeoutWarningStillLogged(t *testing.T) {
	log := newMockLogger("progress.txt")
	exec := newTaskPhaseMockExecutor(nil)
	phase, _ := reviewPhaseFromRunner(t, reviewPhaseTestOpts{cfg: Config{MaxIterations: 50}, exec: exec, log: log})
	phase.policy = newScriptedTestPolicy(log, ExecutionResult{TimedOut: true})

	err := phase.First(t.Context())

	require.NoError(t, err)
	assert.Len(t, exec.RunCalls(), 1)
	assertLogContains(t, log, "session timed out")
}

func TestReviewPhase_Loop_TimeoutContinues(t *testing.T) {
	log := newMockLogger("progress.txt")
	exec := newTaskPhaseMockExecutor(nil)
	phase, _ := reviewPhaseFromRunner(t, reviewPhaseTestOpts{cfg: Config{MaxIterations: 50}, exec: exec, log: log})
	phase.policy = newScriptedTestPolicy(log,
		ExecutionResult{TimedOut: true},
		ExecutionResult{Result: executor.Result{Output: "review done", Signal: status.ReviewDone}},
	)

	err := phase.Loop(t.Context(), "")

	require.NoError(t, err)
	assert.Len(t, exec.RunCalls(), 2)
	assertLogContains(t, log, "retrying review iteration")
}

func TestReviewPhase_First_CodexTimeoutSurfacesAsError(t *testing.T) {
	exec := newTaskPhaseMockExecutor(nil)
	appCfg := testAppConfig(t)
	appCfg.Executor = config.ExecutorCodex
	phase, _ := reviewPhaseFromRunner(t, reviewPhaseTestOpts{cfg: Config{MaxIterations: 50, AppConfig: appCfg}, exec: exec})
	phase.policy = newScriptedTestPolicy(newMockLogger(""), ExecutionResult{Result: executor.Result{Output: "partial output"}, TimedOut: true})

	err := phase.First(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "first review pass timed out")
}

func TestReviewPhase_Loop_DoneSignalStopsBeforeTimeoutHandling(t *testing.T) {
	log := newMockLogger("progress.txt")
	exec := newTaskPhaseMockExecutor(nil)
	phase, _ := reviewPhaseFromRunner(t, reviewPhaseTestOpts{cfg: Config{MaxIterations: 50}, exec: exec, log: log})
	phase.policy = newScriptedTestPolicy(log, ExecutionResult{Result: executor.Result{Signal: status.ReviewDone}, TimedOut: true})

	err := phase.Loop(t.Context(), "")

	require.NoError(t, err)
	assert.Len(t, exec.RunCalls(), 1)
	assertLogNotContains(t, log, "retrying review iteration")
}

func assertLogContains(t *testing.T, log *mockLogger, text string) {
	t.Helper()
	for _, call := range log.PrintCalls() {
		if strings.Contains(call.Format, text) {
			return
		}
	}
	assert.Failf(t, "missing log", "expected log containing %q", text)
}

func assertLogNotContains(t *testing.T, log *mockLogger, text string) {
	t.Helper()
	for _, call := range log.PrintCalls() {
		if strings.Contains(call.Format, text) {
			assert.Failf(t, "unexpected log", "did not expect log containing %q", text)
		}
	}
}
