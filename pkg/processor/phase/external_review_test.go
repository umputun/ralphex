package phase

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/executor"
	"github.com/umputun/ralphex/pkg/status"
)

type externalReviewPhaseTestOpts struct {
	cfg      Config
	review   Executor
	external Executor
	custom   *executor.CustomExecutor
	log      *mockLogger
}

func externalReviewPhaseFromRunner(t *testing.T, opts externalReviewPhaseTestOpts) (*externalReviewPhase, *mockLogger) {
	t.Helper()
	if opts.cfg.AppConfig == nil {
		opts.cfg.AppConfig = testAppConfig(t)
	}
	if opts.log == nil {
		opts.log = newMockLogger("progress.txt")
	}
	if opts.review == nil {
		opts.review = newTaskPhaseMockExecutor(nil)
	}
	if opts.external == nil {
		opts.external = newTaskPhaseMockExecutor(nil)
	}
	r := newTestRunner(testRunnerOpts{cfg: opts.cfg, log: opts.log, execs: Executors{Task: opts.review, External: opts.external, Custom: opts.custom}, holder: &status.PhaseHolder{}})
	phase, ok := r.phases.external.(*externalReviewPhase)
	require.True(t, ok)
	return phase, opts.log
}

func TestExternalReviewPhaseTool(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{name: "codex enabled default", cfg: Config{CodexEnabled: true, AppConfig: testAppConfig(t)}, want: "codex"},
		{name: "disabled backward compat", cfg: Config{CodexEnabled: false, AppConfig: testAppConfig(t)}, want: "none"},
		{name: "explicit override", cfg: explicitExternalToolConfig(t, "codex", false), want: "codex"},
		{name: "configured none", cfg: configuredExternalToolConfig(t, "none"), want: "none"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			phase, _ := externalReviewPhaseFromRunner(t, externalReviewPhaseTestOpts{cfg: tc.cfg})

			assert.Equal(t, tc.want, phase.Tool())
		})
	}
}

func TestExternalReviewPhaseRunCodexNoFindings(t *testing.T) {
	review := newTaskPhaseMockExecutor([]executor.Result{{Output: "done", Signal: status.CodexDone}})
	external := newTaskPhaseMockExecutor([]executor.Result{{Output: "found issue"}})
	phase, _ := externalReviewPhaseFromRunner(t, externalReviewPhaseTestOpts{
		cfg: Config{MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}, review: review, external: external,
	})

	outcome, err := phase.Run(t.Context())

	require.NoError(t, err)
	assert.False(t, outcome.HadFindings)
	assert.Len(t, external.RunCalls(), 1)
	assert.Len(t, review.RunCalls(), 1)
}

func TestExternalReviewPhaseRunCodexFindings(t *testing.T) {
	review := newTaskPhaseMockExecutor([]executor.Result{{Output: "fixed"}, {Output: "done", Signal: status.CodexDone}})
	external := newTaskPhaseMockExecutor([]executor.Result{{Output: "found issue"}, {Output: "clean"}})
	phase, _ := externalReviewPhaseFromRunner(t, externalReviewPhaseTestOpts{
		cfg: Config{MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}, review: review, external: external,
	})

	outcome, err := phase.Run(t.Context())

	require.NoError(t, err)
	assert.True(t, outcome.HadFindings)
	assert.Len(t, external.RunCalls(), 2)
	assert.Len(t, review.RunCalls(), 2)
}

func TestExternalReviewPhaseRunCustomSuccess(t *testing.T) {
	review := newTaskPhaseMockExecutor([]executor.Result{{Output: "done", Signal: status.CodexDone}})
	custom := &executor.CustomExecutor{Script: "/path/to/script.sh"}
	custom.SetRunner(&mockCustomRunnerImpl{results: []executor.Result{{Output: "found issue in foo.go:10"}}})
	appCfg := testAppConfig(t)
	appCfg.ExternalReviewTool = "custom"
	appCfg.CustomReviewScript = custom.Script
	phase, _ := externalReviewPhaseFromRunner(t, externalReviewPhaseTestOpts{
		cfg: Config{MaxIterations: 50, CodexEnabled: true, AppConfig: appCfg}, review: review, custom: custom,
	})

	outcome, err := phase.Run(t.Context())

	require.NoError(t, err)
	assert.False(t, outcome.HadFindings)
	assert.Len(t, review.RunCalls(), 1)
}

func TestExternalReviewPhaseRunCustomNoDuplicateOutput(t *testing.T) {
	log := newMockLogger("progress.txt")
	custom := &executor.CustomExecutor{Script: "/path/to/script.sh", OutputHandler: func(text string) { log.PrintAligned(text) }}
	custom.SetRunner(&mockCustomRunnerImpl{results: []executor.Result{{Output: "issue in foo.go:10\n"}}})
	appCfg := testAppConfig(t)
	appCfg.ExternalReviewTool = "custom"
	appCfg.CustomReviewScript = custom.Script
	phase, _ := externalReviewPhaseFromRunner(t, externalReviewPhaseTestOpts{
		cfg:    Config{MaxIterations: 50, CodexEnabled: true, AppConfig: appCfg},
		review: newTaskPhaseMockExecutor([]executor.Result{{Output: "done", Signal: status.CodexDone}}),
		custom: custom,
		log:    log,
	})

	_, err := phase.Run(t.Context())

	require.NoError(t, err)
	count := 0
	for _, call := range log.PrintAlignedCalls() {
		if strings.Contains(call.Text, "issue in foo.go:10") {
			count++
		}
	}
	assert.Equal(t, 1, count, "custom review output should be streamed once and not duplicated by summary")
}

func TestExternalReviewPhaseRunCustomNotConfigured(t *testing.T) {
	appCfg := testAppConfig(t)
	appCfg.ExternalReviewTool = "custom"
	phase, _ := externalReviewPhaseFromRunner(t, externalReviewPhaseTestOpts{
		cfg: Config{MaxIterations: 50, CodexEnabled: true, AppConfig: appCfg},
	})

	_, err := phase.Run(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "custom review script not configured")
}

func TestExternalReviewPhaseRunBreakChannelExitsEarly(t *testing.T) {
	breakCh := make(chan struct{}, 1)
	external := &executorMock{RunFunc: func(ctx context.Context, _ string) executor.Result {
		breakCh <- struct{}{}
		<-ctx.Done()
		return executor.Result{Error: ctx.Err()}
	}}
	phase, log := externalReviewPhaseFromRunner(t, externalReviewPhaseTestOpts{
		cfg: Config{MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}, external: external,
	})
	phase.breaks.deps.BreakCh = breakCh

	outcome, err := phase.Run(t.Context())

	require.NoError(t, err)
	assert.False(t, outcome.HadFindings)
	assertLogContains(t, log, "manual break requested")
}

func TestExternalReviewPhaseShowSummary(t *testing.T) {
	log := newMockLogger("")
	phase, _ := externalReviewPhaseFromRunner(t, externalReviewPhaseTestOpts{log: log})

	phase.showSummary("codex", "line 1\n\nline 2\n```go\ncode")

	require.Len(t, log.PrintCalls(), 1)
	assert.Equal(t, "%s findings:", log.PrintCalls()[0].Format)
	assert.Equal(t, []any{"codex"}, log.PrintCalls()[0].Args)
	aligned := log.PrintAlignedCalls()
	require.Len(t, aligned, 2)
	assert.Equal(t, "  line 1", aligned[0].Text)
	assert.Equal(t, "  line 2", aligned[1].Text)
}

func TestExternalReviewPhaseRunPatternError(t *testing.T) {
	external := newTaskPhaseMockExecutor([]executor.Result{{Error: &executor.PatternMatchError{Pattern: "limit", HelpCmd: "usage"}}})
	phase, log := externalReviewPhaseFromRunner(t, externalReviewPhaseTestOpts{
		cfg: Config{MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}, external: external,
	})

	_, err := phase.Run(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "limit")
	assertLogContains(t, log, "detected")
}

func TestExternalReviewPhaseRunClaudeEvalError(t *testing.T) {
	review := newTaskPhaseMockExecutor([]executor.Result{{Error: errors.New("eval failed")}})
	external := newTaskPhaseMockExecutor([]executor.Result{{Output: "found issue"}})
	phase, _ := externalReviewPhaseFromRunner(t, externalReviewPhaseTestOpts{
		cfg: Config{MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}, review: review, external: external,
	})

	_, err := phase.Run(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "claude execution")
}

func explicitExternalToolConfig(t *testing.T, tool string, codexEnabled bool) Config {
	t.Helper()
	appCfg := testAppConfig(t)
	appCfg.ExternalReviewTool = tool
	return Config{MaxIterations: 50, CodexEnabled: codexEnabled, ExternalReviewToolSet: true, AppConfig: appCfg}
}

func configuredExternalToolConfig(t *testing.T, tool string) Config {
	t.Helper()
	appCfg := testAppConfig(t)
	appCfg.ExternalReviewTool = tool
	return Config{MaxIterations: 50, CodexEnabled: true, AppConfig: appCfg}
}

type mockCustomRunnerImpl struct {
	results []executor.Result
	idx     int
}

func (m *mockCustomRunnerImpl) Run(_ context.Context, _, _ string) (io.Reader, func() error, error) {
	if m.idx >= len(m.results) {
		return nil, nil, errors.New("no more mock results")
	}
	result := m.results[m.idx]
	m.idx++
	return strings.NewReader(result.Output), func() error { return result.Error }, nil
}

func TestExternalReviewPhaseStalemateBreaksAfterPatience(t *testing.T) {
	log := newMockLogger("progress.txt")
	review := newTaskPhaseMockExecutor([]executor.Result{{Output: "rejected findings"}, {Output: "rejected findings again"}})
	external := newTaskPhaseMockExecutor([]executor.Result{{Output: "found issue"}, {Output: "found issue"}})
	phase, _ := externalReviewPhaseFromRunner(t, externalReviewPhaseTestOpts{
		cfg:    Config{MaxIterations: 50, MaxExternalIterations: 5, CodexEnabled: true, ReviewPatience: 2, AppConfig: testAppConfig(t)},
		review: review, external: external, log: log,
	})
	phase.git.deps.Git = &gitCheckerMock{
		HeadHashFunc:        func() (string, error) { return "abc123def456abc123def456abc123def456abcd", nil },
		DiffFingerprintFunc: func() (string, error) { return "unchanged-diff", nil },
	}

	outcome, err := phase.Run(t.Context())

	require.NoError(t, err)
	assert.True(t, outcome.HadFindings)
	assert.Len(t, external.RunCalls(), 2)
	assert.Len(t, review.RunCalls(), 2)
	assertLogContains(t, log, "stalemate detected")
}

func TestExternalReviewPhaseTimeoutRetriesNextIteration(t *testing.T) {
	tests := []struct {
		name        string
		firstOutput string
	}{
		{name: "empty output", firstOutput: ""},
		{name: "partial output", firstOutput: "partial findings before timeout..."},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			log := newMockLogger("progress.txt")
			review := newTaskPhaseMockExecutor(nil)
			external := newTaskPhaseMockExecutor(nil)
			phase, _ := externalReviewPhaseFromRunner(t, externalReviewPhaseTestOpts{
				cfg:    Config{MaxIterations: 50, MaxExternalIterations: 3, CodexEnabled: true, AppConfig: testAppConfig(t)},
				review: review, external: external, log: log,
			})
			phase.policy = newScriptedTestPolicy(log,
				ExecutionResult{Result: executor.Result{Output: tc.firstOutput}, TimedOut: true},
				ExecutionResult{Result: executor.Result{Output: "found issue in foo.go:42"}},
				ExecutionResult{Result: executor.Result{Output: "done", Signal: status.CodexDone}},
			)

			_, err := phase.Run(t.Context())

			require.NoError(t, err)
			assert.Len(t, external.RunCalls(), 2)
			assert.Len(t, review.RunCalls(), 1)
			assertLogContains(t, log, "session timed out, retrying on next iteration")
		})
	}
}

func TestExternalReviewPhaseTimeoutSkipsStalemate(t *testing.T) {
	log := newMockLogger("progress.txt")
	review := newTaskPhaseMockExecutor(nil)
	external := newTaskPhaseMockExecutor(nil)
	phase, _ := externalReviewPhaseFromRunner(t, externalReviewPhaseTestOpts{
		cfg:    Config{MaxIterations: 50, MaxExternalIterations: 5, CodexEnabled: true, ReviewPatience: 2, AppConfig: testAppConfig(t)},
		review: review, external: external, log: log,
	})
	phase.policy = newScriptedTestPolicy(log,
		ExecutionResult{Result: executor.Result{Output: "found issue in foo.go:10"}},
		ExecutionResult{Result: executor.Result{Output: "partial output"}, TimedOut: true},
		ExecutionResult{Result: executor.Result{Output: "found issue in bar.go:20"}},
		ExecutionResult{Result: executor.Result{Output: "partial output"}, TimedOut: true},
		ExecutionResult{Result: executor.Result{Output: "no issues found"}},
		ExecutionResult{Result: executor.Result{Output: "done", Signal: status.CodexDone}},
	)
	phase.git.deps.Git = &gitCheckerMock{
		HeadHashFunc:        func() (string, error) { return "abc123def456abc123def456abc123def456abcd", nil },
		DiffFingerprintFunc: func() (string, error) { return "unchanged-diff", nil },
	}

	_, err := phase.Run(t.Context())

	require.NoError(t, err)
	assert.Len(t, external.RunCalls(), 3)
	assertLogContains(t, log, "claude eval session timed out")
	for _, call := range log.PrintCalls() {
		assert.NotContains(t, call.Format, "stalemate detected")
	}
}

func TestExternalReviewPhaseKeepsBranchDiffAfterTimeout(t *testing.T) {
	var prompts []string
	review := newTaskPhaseMockExecutor(nil)
	external := &executorMock{RunFunc: func(_ context.Context, prompt string) executor.Result {
		prompts = append(prompts, prompt)
		return executor.Result{}
	}}
	phase, _ := externalReviewPhaseFromRunner(t, externalReviewPhaseTestOpts{
		cfg: Config{MaxIterations: 50, CodexEnabled: true, AppConfig: testAppConfig(t)}, review: review, external: external,
	})
	phase.policy = newScriptedTestPolicy(newMockLogger(""),
		ExecutionResult{Result: executor.Result{Output: "found issue"}},
		ExecutionResult{TimedOut: true},
		ExecutionResult{Result: executor.Result{Output: "found issue"}},
		ExecutionResult{Result: executor.Result{Output: "done", Signal: status.CodexDone}},
	)

	_, err := phase.Run(t.Context())

	require.NoError(t, err)
	require.Len(t, prompts, 2)
	assert.Contains(t, prompts[0], "git diff")
	assert.Contains(t, prompts[1], "git diff")
	assert.NotContains(t, prompts[1], "PREVIOUS REVIEW CONTEXT")
}
