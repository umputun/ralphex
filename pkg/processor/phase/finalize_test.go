package phase

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/executor"
	"github.com/umputun/ralphex/pkg/status"
)

type finalizePhaseTestOpts struct {
	cfg  Config
	exec Executor
	log  *mockLogger
}

func finalizePhaseFromRunner(t *testing.T, opts finalizePhaseTestOpts) (*finalizePhase, *mockLogger, *status.PhaseHolder) {
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
	holder := &status.PhaseHolder{}
	r := newTestRunner(testRunnerOpts{cfg: opts.cfg, log: opts.log, execs: Executors{Task: opts.exec, Review: opts.exec}, holder: holder})
	phase, ok := r.phases.finalize.(*finalizePhase)
	require.True(t, ok)
	return phase, opts.log, holder
}

func TestFinalizePhase_RunEnabled(t *testing.T) {
	exec := newTaskPhaseMockExecutor([]executor.Result{{Output: "finalize done"}})
	phase, log, holder := finalizePhaseFromRunner(t, finalizePhaseTestOpts{
		cfg: Config{MaxIterations: 50, FinalizeEnabled: true}, exec: exec,
	})

	err := phase.Run(t.Context())

	require.NoError(t, err)
	assert.Len(t, exec.RunCalls(), 1)
	assert.Equal(t, status.PhaseFinalize, holder.Get())
	assertFinalizeSectionPrinted(t, log)
}

func TestFinalizePhase_SkippedWhenDisabled(t *testing.T) {
	exec := newTaskPhaseMockExecutor(nil)
	phase, _, _ := finalizePhaseFromRunner(t, finalizePhaseTestOpts{
		cfg: Config{MaxIterations: 50, FinalizeEnabled: false}, exec: exec,
	})

	err := phase.Run(t.Context())

	require.NoError(t, err)
	assert.Empty(t, exec.RunCalls())
}

func TestFinalizePhase_FailureDoesNotBlockSuccess(t *testing.T) {
	exec := newTaskPhaseMockExecutor([]executor.Result{{Error: errors.New("finalize error")}})
	phase, log, _ := finalizePhaseFromRunner(t, finalizePhaseTestOpts{
		cfg: Config{MaxIterations: 50, FinalizeEnabled: true}, exec: exec,
	})

	err := phase.Run(t.Context())

	require.NoError(t, err)
	assertLogContains(t, log, "finalize step failed")
}

func TestFinalizePhase_FailedSignalDoesNotBlockSuccess(t *testing.T) {
	exec := newTaskPhaseMockExecutor([]executor.Result{{Output: "failed", Signal: status.Failed}})
	phase, log, _ := finalizePhaseFromRunner(t, finalizePhaseTestOpts{
		cfg: Config{MaxIterations: 50, FinalizeEnabled: true}, exec: exec,
	})

	err := phase.Run(t.Context())

	require.NoError(t, err)
	assertLogContains(t, log, "finalize step reported failure")
}

func TestFinalizePhase_ContextCancellationPropagates(t *testing.T) {
	exec := newTaskPhaseMockExecutor([]executor.Result{{Error: context.Canceled}})
	phase, _, _ := finalizePhaseFromRunner(t, finalizePhaseTestOpts{
		cfg: Config{MaxIterations: 50, FinalizeEnabled: true}, exec: exec,
	})

	err := phase.Run(t.Context())

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestFinalizePhase_LimitPatternWithoutWaitLogsAndContinues(t *testing.T) {
	exec := newTaskPhaseMockExecutor([]executor.Result{{Error: &executor.LimitPatternError{Pattern: "limit", HelpCmd: "usage"}}})
	phase, log, _ := finalizePhaseFromRunner(t, finalizePhaseTestOpts{
		cfg: Config{MaxIterations: 50, FinalizeEnabled: true}, exec: exec,
	})

	err := phase.Run(t.Context())

	require.NoError(t, err)
	assert.Len(t, exec.RunCalls(), 1)
	assertLogContains(t, log, "detected")
}

func assertFinalizeSectionPrinted(t *testing.T, log *mockLogger) {
	t.Helper()
	for _, call := range log.PrintSectionCalls() {
		if strings.Contains(call.Section.Label, "finalize") {
			return
		}
	}
	assert.Fail(t, "finalize section header was not printed")
}
