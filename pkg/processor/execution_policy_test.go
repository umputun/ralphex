package processor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/config"
	"github.com/umputun/ralphex/pkg/executor"
	"github.com/umputun/ralphex/pkg/status"
)

func TestExecutionPolicy_RunReturnsPerCallTimeoutState(t *testing.T) {
	log := newMockLogger()
	appCfg := testAppConfig(t)
	appCfg.SessionTimeout = 20 * time.Millisecond
	appCfg.SessionTimeoutSet = true
	policy := newRetryPolicy(retryPolicyOpts{cfg: Config{AppConfig: appCfg}, log: log})

	blockingRun := func(ctx context.Context, _ string) executor.Result {
		<-ctx.Done()
		return executor.Result{Error: ctx.Err(), Signal: status.Completed}
	}
	result := policy.Run(t.Context(), blockingRun, "prompt", "claude")
	require.NoError(t, result.Result.Error)
	assert.Empty(t, result.Result.Signal)
	assert.True(t, result.TimedOut)

	successRun := func(_ context.Context, _ string) executor.Result {
		return executor.Result{Output: "ok", Signal: status.Completed}
	}
	result = policy.Run(t.Context(), successRun, "prompt", "claude")
	require.NoError(t, result.Result.Error)
	assert.Equal(t, "ok", result.Result.Output)
	assert.False(t, result.TimedOut)
}

func TestExecutionPolicy_RunRetriesLimitErrors(t *testing.T) {
	log := newMockLogger()
	policy := newRetryPolicy(retryPolicyOpts{log: log, waitOnLimit: time.Millisecond})

	calls := 0
	run := func(_ context.Context, _ string) executor.Result {
		calls++
		if calls == 1 {
			return executor.Result{Error: &executor.LimitPatternError{Pattern: "limit", HelpCmd: "usage"}}
		}
		return executor.Result{Output: "done"}
	}

	result := policy.Run(t.Context(), run, "prompt", "claude")
	require.NoError(t, result.Result.Error)
	assert.Equal(t, "done", result.Result.Output)
	assert.Equal(t, 2, calls)

	var logged bool
	for _, call := range log.PrintCalls() {
		if strings.Contains(call.Format, "rate limit detected") {
			logged = true
		}
	}
	assert.True(t, logged)
}

func TestExecutionPolicy_RunWithLimitRetryRetryOnLimitError(t *testing.T) {
	log := newMockLogger()
	policy := newRetryPolicy(retryPolicyOpts{log: log, waitOnLimit: 10 * time.Millisecond})

	callCount := 0
	run := func(_ context.Context, _ string) executor.Result {
		callCount++
		if callCount == 1 {
			return executor.Result{Error: &executor.LimitPatternError{Pattern: "You've hit your limit", HelpCmd: "claude /usage"}}
		}
		return executor.Result{Output: "success", Signal: status.Completed}
	}

	result := policy.Run(t.Context(), run, "test prompt", "claude")

	require.NoError(t, result.Result.Error)
	assert.Equal(t, "success", result.Result.Output)
	assert.Equal(t, 2, callCount)
	assertLogContains(t, log, "rate limit detected")
}

func TestExecutionPolicy_RunMapsRetryPatternToTimedOut(t *testing.T) {
	log := newMockLogger()
	policy := newRetryPolicy(retryPolicyOpts{log: log})

	result := policy.Run(t.Context(), func(_ context.Context, _ string) executor.Result {
		return executor.Result{Error: &executor.RetryPatternError{Pattern: "FYA_TRANSIENT_TIMEOUT", HelpCmd: "claude /usage"}}
	}, "test prompt", "claude")

	require.NoError(t, result.Result.Error)
	assert.True(t, result.TimedOut)
	assertLogContains(t, log, "transient %s error detected")
}

func TestExecutionPolicy_RunWithLimitRetryNoRetryWhenWaitZero(t *testing.T) {
	policy := newRetryPolicy(retryPolicyOpts{log: newMockLogger()})

	callCount := 0
	run := func(_ context.Context, _ string) executor.Result {
		callCount++
		return executor.Result{Error: &executor.LimitPatternError{Pattern: "You've hit your limit", HelpCmd: "claude /usage"}}
	}

	result := policy.Run(t.Context(), run, "test prompt", "claude")

	require.Error(t, result.Result.Error)
	var limitErr *executor.LimitPatternError
	require.ErrorAs(t, result.Result.Error, &limitErr)
	assert.Equal(t, "You've hit your limit", limitErr.Pattern)
	assert.Equal(t, 1, callCount)
}

func TestExecutionPolicy_RunWithLimitRetryContextCancelledDuringWait(t *testing.T) {
	policy := newRetryPolicy(retryPolicyOpts{log: newMockLogger(), waitOnLimit: 10 * time.Second})
	ctx, cancel := context.WithCancel(t.Context())

	callCount := 0
	run := func(_ context.Context, _ string) executor.Result {
		callCount++
		cancel()
		return executor.Result{Error: &executor.LimitPatternError{Pattern: "limit hit", HelpCmd: "claude /usage"}}
	}

	result := policy.Run(ctx, run, "test prompt", "claude")

	require.Error(t, result.Result.Error)
	require.ErrorIs(t, result.Result.Error, context.Canceled)
	assert.Equal(t, 1, callCount)
}

func TestExecutionPolicy_RunWithLimitRetryPatternMatchErrorNotRetried(t *testing.T) {
	policy := newRetryPolicy(retryPolicyOpts{log: newMockLogger(), waitOnLimit: 10 * time.Millisecond})

	callCount := 0
	run := func(_ context.Context, _ string) executor.Result {
		callCount++
		return executor.Result{Error: &executor.PatternMatchError{Pattern: "API Error", HelpCmd: "claude /usage"}}
	}

	result := policy.Run(t.Context(), run, "test prompt", "claude")

	require.Error(t, result.Result.Error)
	var patternErr *executor.PatternMatchError
	require.ErrorAs(t, result.Result.Error, &patternErr)
	assert.Equal(t, "API Error", patternErr.Pattern)
	assert.Equal(t, 1, callCount)
}

func TestExecutionPolicy_RunWithLimitRetryMultipleRetries(t *testing.T) {
	policy := newRetryPolicy(retryPolicyOpts{log: newMockLogger(), waitOnLimit: time.Millisecond})

	callCount := 0
	run := func(_ context.Context, _ string) executor.Result {
		callCount++
		if callCount <= 3 {
			return executor.Result{Error: &executor.LimitPatternError{Pattern: "rate limit", HelpCmd: "claude /usage"}}
		}
		return executor.Result{Output: "finally done"}
	}

	result := policy.Run(t.Context(), run, "test prompt", "claude")

	require.NoError(t, result.Result.Error)
	assert.Equal(t, "finally done", result.Result.Output)
	assert.Equal(t, 4, callCount)
}

func TestExecutionPolicy_RunWithLimitRetryNoErrorPassesThrough(t *testing.T) {
	policy := newRetryPolicy(retryPolicyOpts{log: newMockLogger(), waitOnLimit: time.Hour})

	callCount := 0
	run := func(_ context.Context, _ string) executor.Result {
		callCount++
		return executor.Result{Output: "success", Signal: status.Completed}
	}

	result := policy.Run(t.Context(), run, "test prompt", "claude")

	require.NoError(t, result.Result.Error)
	assert.Equal(t, "success", result.Result.Output)
	assert.Equal(t, status.Completed, result.Result.Signal)
	assert.Equal(t, 1, callCount)
}

func TestExecutionPolicy_SessionTimeoutBlockingExecutor(t *testing.T) {
	log := newMockLogger()
	appCfg := testAppConfig(t)
	appCfg.SessionTimeout = 50 * time.Millisecond
	appCfg.SessionTimeoutSet = true
	policy := newRetryPolicy(retryPolicyOpts{cfg: Config{AppConfig: appCfg}, log: log})

	run := func(ctx context.Context, _ string) executor.Result {
		<-ctx.Done()
		return executor.Result{Error: ctx.Err()}
	}

	start := time.Now()
	result := policy.runWithSessionTimeout(t.Context(), run, "test prompt", "claude")
	elapsed := time.Since(start)

	assert.Less(t, elapsed, 500*time.Millisecond)
	require.NoError(t, result.Result.Error)
	assert.True(t, result.TimedOut)
	assertLogContains(t, log, "session timed out")
}

func TestExecutionPolicy_SessionTimeoutZeroDoesNotAddDeadline(t *testing.T) {
	log := newMockLogger()
	appCfg := testAppConfig(t)
	policy := newRetryPolicy(retryPolicyOpts{cfg: Config{AppConfig: appCfg}, log: log})

	var hasDeadline bool
	run := func(ctx context.Context, _ string) executor.Result {
		_, hasDeadline = ctx.Deadline()
		return executor.Result{Output: "success", Signal: status.Completed}
	}

	result := policy.runWithSessionTimeout(t.Context(), run, "test prompt", "claude")

	require.NoError(t, result.Result.Error)
	assert.Equal(t, "success", result.Result.Output)
	assert.False(t, hasDeadline)
	for _, call := range log.PrintCalls() {
		assert.NotContains(t, call.Format, "session timed out")
	}
}

func TestExecutionPolicy_SessionTimeoutParentCancelNotMisidentified(t *testing.T) {
	log := newMockLogger()
	appCfg := testAppConfig(t)
	appCfg.SessionTimeout = 10 * time.Second
	appCfg.SessionTimeoutSet = true
	policy := newRetryPolicy(retryPolicyOpts{cfg: Config{AppConfig: appCfg}, log: log})
	ctx, cancel := context.WithCancel(t.Context())

	run := func(runCtx context.Context, _ string) executor.Result {
		cancel()
		<-runCtx.Done()
		return executor.Result{Error: runCtx.Err()}
	}

	result := policy.runWithSessionTimeout(ctx, run, "test prompt", "claude")

	require.Error(t, result.Result.Error)
	assert.False(t, result.TimedOut)
	for _, call := range log.PrintCalls() {
		assert.NotContains(t, call.Format, "session timed out")
	}
}

func TestExecutionPolicy_SessionTimeoutIntegrationWithLimitRetry(t *testing.T) {
	appCfg := testAppConfig(t)
	appCfg.SessionTimeout = 50 * time.Millisecond
	appCfg.SessionTimeoutSet = true
	policy := newRetryPolicy(retryPolicyOpts{cfg: Config{AppConfig: appCfg}, log: newMockLogger()})

	run := func(ctx context.Context, _ string) executor.Result {
		<-ctx.Done()
		return executor.Result{Error: ctx.Err()}
	}

	result := policy.Run(t.Context(), run, "test prompt", "claude")

	require.NoError(t, result.Result.Error)
	assert.True(t, result.TimedOut)
}

func TestExecutionPolicy_SessionTimeoutGatedByExecutorAndToolName(t *testing.T) {
	tests := []struct {
		name        string
		executor    string
		toolName    string
		wantApplied bool
	}{
		{name: "default executor mode, claude tool", executor: config.ExecutorClaude, toolName: "claude", wantApplied: true},
		{name: "default executor mode, codex external review", executor: config.ExecutorClaude, toolName: "codex", wantApplied: false},
		{name: "default executor mode, custom external review", executor: config.ExecutorClaude, toolName: "custom", wantApplied: false},
		{name: "codex mode, claude tool", executor: config.ExecutorCodex, toolName: "claude", wantApplied: true},
		{name: "codex mode, codex tool", executor: config.ExecutorCodex, toolName: "codex", wantApplied: true},
		{name: "codex mode, custom external review", executor: config.ExecutorCodex, toolName: "custom", wantApplied: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			appCfg := testAppConfig(t)
			appCfg.SessionTimeout = 50 * time.Millisecond
			appCfg.SessionTimeoutSet = true
			appCfg.Executor = tt.executor
			policy := newRetryPolicy(retryPolicyOpts{cfg: Config{AppConfig: appCfg}, log: newMockLogger()})

			var hasDeadline bool
			run := func(ctx context.Context, _ string) executor.Result {
				_, hasDeadline = ctx.Deadline()
				return executor.Result{Output: "done", Signal: status.ReviewDone}
			}

			result := policy.runWithSessionTimeout(t.Context(), run, "test prompt", tt.toolName)

			require.NoError(t, result.Result.Error)
			assert.Equal(t, "done", result.Result.Output)
			assert.Equal(t, tt.wantApplied, hasDeadline)
		})
	}
}

func TestExecutionPolicy_ExternalReviewBypassPreservesIdleTimeoutDiagnostic(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
	}{
		{name: "codex external review", toolName: "codex"},
		{name: "custom external review", toolName: "custom"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := newMockLogger()
			appCfg := testAppConfig(t)
			appCfg.SessionTimeout = 50 * time.Millisecond
			appCfg.SessionTimeoutSet = true
			appCfg.Executor = config.ExecutorClaude
			policy := newRetryPolicy(retryPolicyOpts{cfg: Config{AppConfig: appCfg}, log: log})

			var hadDeadline bool
			run := func(ctx context.Context, _ string) executor.Result {
				_, hadDeadline = ctx.Deadline()
				return executor.Result{Output: "partial output", IdleTimedOut: true}
			}

			result := policy.runWithSessionTimeout(t.Context(), run, "test prompt", tt.toolName)

			assert.False(t, hadDeadline)
			assert.True(t, result.Result.IdleTimedOut)
			assertLogContains(t, log, "idle timed out")
		})
	}
}

func TestExecutionPolicy_SessionTimeoutClearsSignalOnTimeout(t *testing.T) {
	appCfg := testAppConfig(t)
	appCfg.SessionTimeout = 50 * time.Millisecond
	appCfg.SessionTimeoutSet = true
	policy := newRetryPolicy(retryPolicyOpts{cfg: Config{AppConfig: appCfg}, log: newMockLogger()})

	run := func(ctx context.Context, _ string) executor.Result {
		<-ctx.Done()
		return executor.Result{Output: "partial output", Signal: status.Completed, Error: ctx.Err()}
	}

	result := policy.runWithSessionTimeout(t.Context(), run, "test prompt", "claude")

	require.NoError(t, result.Result.Error)
	assert.Empty(t, result.Result.Signal)
	assert.True(t, result.TimedOut)
}
