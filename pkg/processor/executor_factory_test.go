package processor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/config"
	"github.com/umputun/ralphex/pkg/executor"
	"github.com/umputun/ralphex/pkg/processor/mocks"
	"github.com/umputun/ralphex/pkg/status"
)

func TestRunner_TaskRetryCountUsesConfig(t *testing.T) {
	tests := []struct {
		name          string
		retryCount    int
		expectedCalls int
	}{
		{name: "default retry count", retryCount: 0, expectedCalls: 2},
		{name: "custom retry count", retryCount: 3, expectedCalls: 4},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			planFile := filepath.Join(t.TempDir(), "plan.md")
			require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n### Task 1: first\n- [ ] todo"), 0o600))

			results := make([]executor.Result, tc.expectedCalls)
			for i := range results {
				results[i] = executor.Result{Output: "failed", Signal: status.Failed}
			}
			task := newMockExecutor(results)
			appCfg := testAppConfig(t)
			appCfg.TaskRetryCountSet = false
			cfg := Config{
				Mode: ModeTasksOnly, PlanFile: planFile, MaxIterations: 10, IterationDelayMs: 1,
				TaskRetryCount: tc.retryCount, AppConfig: appCfg,
			}
			r := NewWithExecutors(cfg, newRunnerMockLogger(""), Executors{Task: task}, &status.PhaseHolder{})

			err := r.Run(t.Context())

			require.Error(t, err)
			assert.Contains(t, err.Error(), "FAILED signal")
			assert.Len(t, task.RunCalls(), tc.expectedCalls)
		})
	}
}

func TestRunner_New_CodexNotInstalled_AutoDisables(t *testing.T) {
	log := newRunnerMockLogger("progress.txt")

	appCfg := testAppConfig(t)
	appCfg.CodexCommand = "/nonexistent/path/to/codex" // command that doesn't exist

	cfg := Config{
		Mode:          ModeCodexOnly,
		MaxIterations: 50,
		CodexEnabled:  true,
		AppConfig:     appCfg,
	}

	// use New (not NewWithExecutors) to trigger LookPath check
	r := New(cfg, log, &status.PhaseHolder{})

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
	log := newRunnerMockLogger("progress.txt")

	appCfg := testAppConfig(t)
	appCfg.CodexCommand = "/nonexistent/path/to/codex" // command that doesn't exist
	appCfg.ExternalReviewTool = "custom"               // using custom, not codex
	appCfg.CustomReviewScript = "/path/to/script.sh"

	cfg := Config{
		Mode:          ModeCodexOnly,
		MaxIterations: 50,
		CodexEnabled:  true,
		AppConfig:     appCfg,
	}

	// use New (not NewWithExecutors) to trigger LookPath check
	r := New(cfg, log, &status.PhaseHolder{})

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
	log := newRunnerMockLogger("progress.txt")

	appCfg := testAppConfig(t)
	appCfg.CodexCommand = "/nonexistent/path/to/codex" // command that doesn't exist
	appCfg.ExternalReviewTool = "none"                 // external review disabled

	cfg := Config{
		Mode:          ModeCodexOnly,
		MaxIterations: 50,
		CodexEnabled:  true,
		AppConfig:     appCfg,
	}

	// use New (not NewWithExecutors) to trigger LookPath check
	r := New(cfg, log, &status.PhaseHolder{})

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

func TestParseModelEffort(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		model  string
		effort string
	}{
		{name: "empty", input: "", model: "", effort: ""},
		{name: "model only", input: "opus", model: "opus", effort: ""},
		{name: "model and effort", input: "opus:high", model: "opus", effort: "high"},
		{name: "effort only", input: ":high", model: "", effort: "high"},
		{name: "trailing colon", input: "opus:", model: "opus", effort: ""},
		{name: "full model id with effort", input: "claude-sonnet-4-6:medium", model: "claude-sonnet-4-6", effort: "medium"},
		{name: "multiple colons — split on first", input: "opus:high:extra", model: "opus", effort: "high:extra"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			model, effort := ParseModelEffort(tc.input)
			assert.Equal(t, tc.model, model)
			assert.Equal(t, tc.effort, effort)
		})
	}
}

func TestResolveCodexModelEffort(t *testing.T) {
	tests := []struct {
		name       string
		spec       string
		defModel   string
		defEffort  string
		wantModel  string
		wantEffort string
		wantMax    bool
	}{
		{name: "empty spec keeps defaults", spec: "", defModel: "gpt-5.5", defEffort: "xhigh", wantModel: "gpt-5.5", wantEffort: "xhigh"},
		{name: "model only", spec: "gpt-5.6", defModel: "gpt-5.5", defEffort: "xhigh", wantModel: "gpt-5.6", wantEffort: "xhigh"},
		{name: "model and effort", spec: "gpt-5.6:high", defModel: "gpt-5.5", defEffort: "xhigh", wantModel: "gpt-5.6", wantEffort: "high"},
		{name: "effort only keeps default model", spec: ":low", defModel: "gpt-5.5", defEffort: "xhigh", wantModel: "gpt-5.5", wantEffort: "low"},
		{name: "trailing colon keeps default effort", spec: "gpt-5.6:", defModel: "gpt-5.5", defEffort: "xhigh", wantModel: "gpt-5.6", wantEffort: "xhigh"},
		{name: "max effort dropped, default kept", spec: "gpt-5.6:max", defModel: "gpt-5.5", defEffort: "xhigh", wantModel: "gpt-5.6", wantEffort: "xhigh", wantMax: true},
		{name: "max effort case-insensitive", spec: ":MAX", defModel: "gpt-5.5", defEffort: "xhigh", wantModel: "gpt-5.5", wantEffort: "xhigh", wantMax: true},
		{name: "empty spec with empty defaults stays empty", spec: "", defModel: "", defEffort: "", wantModel: "", wantEffort: ""},
		{name: "model-only spec with empty default effort", spec: "gpt-5.6", defModel: "", defEffort: "", wantModel: "gpt-5.6", wantEffort: ""},
		{name: "effort-only spec with empty default model", spec: ":low", defModel: "", defEffort: "", wantModel: "", wantEffort: "low"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			model, effort, maxDropped := ResolveCodexModelEffort(tc.spec, tc.defModel, tc.defEffort)
			assert.Equal(t, tc.wantModel, model)
			assert.Equal(t, tc.wantEffort, effort)
			assert.Equal(t, tc.wantMax, maxDropped)
		})
	}
}

func effectiveReviewExecutor(execs Executors) Executor {
	if execs.Review != nil {
		return execs.Review
	}
	return execs.Task
}

func TestRunner_New_ModelEffortWiring(t *testing.T) {
	log := newRunnerMockLogger("progress.txt")

	tests := []struct {
		name         string
		taskModel    string
		reviewModel  string
		wantTask     [2]string // {model, effort}
		wantReview   [2]string
		sameExecutor bool // true when review falls back to task executor
	}{
		{name: "empty specs", taskModel: "", reviewModel: "", wantTask: [2]string{"", ""}, wantReview: [2]string{"", ""}, sameExecutor: true},
		{name: "task model only, review empty", taskModel: "opus", reviewModel: "", wantTask: [2]string{"opus", ""}, wantReview: [2]string{"opus", ""}, sameExecutor: true},
		{name: "task model with effort, review empty", taskModel: "opus:high", reviewModel: "", wantTask: [2]string{"opus", "high"}, wantReview: [2]string{"opus", "high"}, sameExecutor: true},
		{name: "effort only, review empty", taskModel: ":medium", reviewModel: "", wantTask: [2]string{"", "medium"}, wantReview: [2]string{"", "medium"}, sameExecutor: true},
		{name: "trailing colon equivalent to plain model", taskModel: "opus", reviewModel: "opus:", wantTask: [2]string{"opus", ""}, wantReview: [2]string{"opus", ""}, sameExecutor: true},
		{name: "same model different effort — separate executor", taskModel: "opus", reviewModel: "opus:high", wantTask: [2]string{"opus", ""}, wantReview: [2]string{"opus", "high"}, sameExecutor: false},
		{name: "different model and effort — separate executor", taskModel: "opus:high", reviewModel: "sonnet:medium", wantTask: [2]string{"opus", "high"}, wantReview: [2]string{"sonnet", "medium"}, sameExecutor: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{
				Mode:          ModeReview,
				MaxIterations: 50,
				CodexEnabled:  false,
				TaskModel:     tc.taskModel,
				ReviewModel:   tc.reviewModel,
				AppConfig:     testAppConfig(t),
			}
			_, execs := (&executorFactory{}).Build(cfg, log)

			taskExec, ok := execs.Task.(*executor.ClaudeExecutor)
			require.True(t, ok, "task executor should be *executor.ClaudeExecutor")
			assert.Equal(t, tc.wantTask[0], taskExec.Model, "task model")
			assert.Equal(t, tc.wantTask[1], taskExec.Effort, "task effort")

			reviewExec, ok := effectiveReviewExecutor(execs).(*executor.ClaudeExecutor)
			require.True(t, ok, "review executor should be *executor.ClaudeExecutor")
			assert.Equal(t, tc.wantReview[0], reviewExec.Model, "review model")
			assert.Equal(t, tc.wantReview[1], reviewExec.Effort, "review effort")

			if tc.sameExecutor {
				assert.Same(t, taskExec, reviewExec, "review executor should be the same instance as task executor when specs equivalent")
			} else {
				assert.NotSame(t, taskExec, reviewExec, "review executor should be a distinct instance when specs differ")
			}
		})
	}
}

func TestRunner_New_CodexModelEffortWiring(t *testing.T) {
	log := newRunnerMockLogger("progress.txt")

	tests := []struct {
		name         string
		taskModel    string
		reviewModel  string
		wantTask     [2]string // {model, effort}
		wantReview   [2]string
		sameExecutor bool
	}{
		{name: "empty specs use codex config defaults", taskModel: "", reviewModel: "", wantTask: [2]string{"gpt-5.5", "xhigh"}, wantReview: [2]string{"gpt-5.5", "xhigh"}, sameExecutor: true},
		{name: "task model only", taskModel: "gpt-5.6", reviewModel: "", wantTask: [2]string{"gpt-5.6", "xhigh"}, wantReview: [2]string{"gpt-5.6", "xhigh"}, sameExecutor: true},
		{name: "task model with effort", taskModel: "gpt-5.6:high", reviewModel: "", wantTask: [2]string{"gpt-5.6", "high"}, wantReview: [2]string{"gpt-5.6", "high"}, sameExecutor: true},
		{name: "effort only keeps default model", taskModel: ":low", reviewModel: "", wantTask: [2]string{"gpt-5.5", "low"}, wantReview: [2]string{"gpt-5.5", "low"}, sameExecutor: true},
		{name: "review model differs in effort — separate executor", taskModel: "gpt-5.6", reviewModel: "gpt-5.6:low", wantTask: [2]string{"gpt-5.6", "xhigh"}, wantReview: [2]string{"gpt-5.6", "low"}, sameExecutor: false},
		{name: "review model differs in model — separate executor", taskModel: "gpt-5.6:high", reviewModel: "gpt-5.5:low", wantTask: [2]string{"gpt-5.6", "high"}, wantReview: [2]string{"gpt-5.5", "low"}, sameExecutor: false},
		{name: "review model only leaves task at default", taskModel: "", reviewModel: "gpt-5.6:low", wantTask: [2]string{"gpt-5.5", "xhigh"}, wantReview: [2]string{"gpt-5.6", "low"}, sameExecutor: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			appCfg := testAppConfig(t)
			appCfg.Executor = config.ExecutorCodex
			appCfg.CodexModel = "gpt-5.5"
			appCfg.CodexReasoningEffort = "xhigh"
			cfg := Config{
				Mode:          ModeReview,
				MaxIterations: 50,
				TaskModel:     tc.taskModel,
				ReviewModel:   tc.reviewModel,
				AppConfig:     appCfg,
			}
			_, execs := (&executorFactory{}).Build(cfg, log)

			taskExec, ok := execs.Task.(*executor.CodexExecutor)
			require.True(t, ok, "task executor should be *executor.CodexExecutor")
			assert.Equal(t, tc.wantTask[0], taskExec.Model, "task model")
			assert.Equal(t, tc.wantTask[1], taskExec.ReasoningEffort, "task effort")

			reviewExec, ok := effectiveReviewExecutor(execs).(*executor.CodexExecutor)
			require.True(t, ok, "review executor should be *executor.CodexExecutor")
			assert.Equal(t, tc.wantReview[0], reviewExec.Model, "review model")
			assert.Equal(t, tc.wantReview[1], reviewExec.ReasoningEffort, "review effort")

			if tc.sameExecutor {
				assert.Same(t, taskExec, reviewExec, "review executor should be the same instance when specs equivalent")
			} else {
				assert.NotSame(t, taskExec, reviewExec, "review executor should be a distinct instance when specs differ")
			}
		})
	}
}

func TestRunner_New_ExecutorRouting(t *testing.T) {
	log := newRunnerMockLogger("progress.txt")
	holder := &status.PhaseHolder{}

	t.Run("default executor: claude for task/review, codex for external", func(t *testing.T) {
		appCfg := testAppConfig(t)
		appCfg.Executor = config.ExecutorClaude
		cfg := Config{Mode: ModeReview, MaxIterations: 50, CodexEnabled: false, AppConfig: appCfg}
		_, execs := (&executorFactory{}).Build(cfg, log)

		_, ok := execs.Task.(*executor.ClaudeExecutor)
		assert.True(t, ok, "task executor should be *executor.ClaudeExecutor when Executor is default")

		_, ok = effectiveReviewExecutor(execs).(*executor.ClaudeExecutor)
		assert.True(t, ok, "review executor should be *executor.ClaudeExecutor when Executor is default")

		externalExec, ok := execs.External.(*executor.CodexExecutor)
		assert.True(t, ok, "external executor should be *executor.CodexExecutor by default")
		assert.Equal(t, "read-only", externalExec.Sandbox, "external codex review keeps the read-only default")
	})

	t.Run("Executor=codex: codex for task/review, nil for external", func(t *testing.T) {
		appCfg := testAppConfig(t)
		appCfg.Executor = config.ExecutorCodex
		cfg := Config{Mode: ModeReview, MaxIterations: 50, CodexEnabled: false, AppConfig: appCfg}
		_, execs := (&executorFactory{}).Build(cfg, log)

		taskExec, ok := execs.Task.(*executor.CodexExecutor)
		assert.True(t, ok, "task executor should be *executor.CodexExecutor when Executor=codex")
		assert.Equal(t, "danger-full-access", taskExec.Sandbox, "first-class codex task execution must allow git metadata writes by default")

		reviewExec, ok := effectiveReviewExecutor(execs).(*executor.CodexExecutor)
		assert.True(t, ok, "review executor should be codex when Executor=codex")
		assert.Equal(t, "danger-full-access", reviewExec.Sandbox, "first-class codex review fixes must allow git metadata writes by default")

		assert.Nil(t, execs.External, "external executor should be nil when Executor=codex")
	})

	t.Run("Executor=codex respects explicit sandbox config", func(t *testing.T) {
		appCfg := testAppConfig(t)
		appCfg.Executor = config.ExecutorCodex
		appCfg.CodexSandbox = "workspace-write"
		appCfg.CodexSandboxSet = true
		cfg := Config{Mode: ModeReview, MaxIterations: 50, CodexEnabled: false, AppConfig: appCfg}
		_, execs := (&executorFactory{}).Build(cfg, log)

		taskExec, ok := execs.Task.(*executor.CodexExecutor)
		require.True(t, ok, "task executor should be *executor.CodexExecutor when Executor=codex")
		assert.Equal(t, "workspace-write", taskExec.Sandbox)

		reviewExec, ok := effectiveReviewExecutor(execs).(*executor.CodexExecutor)
		require.True(t, ok, "review executor should be *executor.CodexExecutor when Executor=codex")
		assert.Equal(t, "workspace-write", reviewExec.Sandbox)
	})

	t.Run("zero-value Executors literal constructs usable runner", func(t *testing.T) {
		cfg := Config{Mode: ModeReview, AppConfig: testAppConfig(t)}
		r := NewWithExecutors(cfg, log, Executors{}, holder)
		require.NotNil(t, r)
	})
}

// newCapturingLogger returns a logger mock that captures every Print invocation
// into the returned slice pointer. used by tests asserting hint emission.
func newCapturingLogger(captured *[]string) *mocks.LoggerMock {
	return &mocks.LoggerMock{
		PrintFunc: func(format string, args ...any) {
			*captured = append(*captured, fmt.Sprintf(format, args...))
		},
		PrintRawFunc:       func(_ string, _ ...any) {},
		PrintSectionFunc:   func(_ status.Section) {},
		PrintAlignedFunc:   func(_ string) {},
		LogQuestionFunc:    func(_ string, _ []string) {},
		LogAnswerFunc:      func(_ string) {},
		LogDraftReviewFunc: func(_, _ string) {},
		PathFunc:           func() string { return "" },
	}
}

// setIsolatedHome points the home-directory env vars at an empty temp dir and
// resets the hint sync.Once so each subtest can exercise the first-emit path
// without leaking state. covers both Unix (HOME) and Windows (USERPROFILE /
// HOMEDRIVE+HOMEPATH) since os.UserHomeDir() reads different vars per platform —
// the test-safety rule forbids touching the real ~/.config/ralphex/.
func setIsolatedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")
	claudeMdHintOnce = sync.Once{}
	return home
}

func TestRunner_New_PassClaudeMd_PropagatesToCodexExecutor(t *testing.T) {
	setIsolatedHome(t)
	log := newRunnerMockLogger("")

	appCfg := testAppConfig(t)
	appCfg.Executor = config.ExecutorCodex
	appCfg.PassClaudeMd = true
	cfg := Config{Mode: ModeReview, MaxIterations: 50, CodexEnabled: false, AppConfig: appCfg}

	_, execs := (&executorFactory{}).Build(cfg, log)
	codexExec, ok := execs.Task.(*executor.CodexExecutor)
	require.True(t, ok, "task executor should be *executor.CodexExecutor when Executor=codex")
	assert.True(t, codexExec.PassClaudeMd, "PassClaudeMd should propagate from cfg.AppConfig to CodexExecutor")
}

func TestRunner_New_PassClaudeMdFalse_DoesNotSetField(t *testing.T) {
	setIsolatedHome(t)
	log := newRunnerMockLogger("")

	appCfg := testAppConfig(t)
	appCfg.Executor = config.ExecutorCodex
	appCfg.PassClaudeMd = false
	cfg := Config{Mode: ModeReview, MaxIterations: 50, CodexEnabled: false, AppConfig: appCfg}

	_, execs := (&executorFactory{}).Build(cfg, log)
	codexExec, ok := execs.Task.(*executor.CodexExecutor)
	require.True(t, ok, "task executor should be *executor.CodexExecutor when Executor=codex")
	assert.False(t, codexExec.PassClaudeMd, "PassClaudeMd should be false when cfg disabled")
}

func TestRunner_New_CodexExecutor_TaskAndReviewShareInstance(t *testing.T) {
	// under --codex, task and review use the SAME codex executor instance with
	// MultiAgent=true. enabling multi_agent for every phase means any prompt
	// (task, review, finalize) can use {{agent:...}} expansions if customized,
	// without paying for two separate codex configurations.
	setIsolatedHome(t)
	log := newRunnerMockLogger("")

	appCfg := testAppConfig(t)
	appCfg.Executor = config.ExecutorCodex
	cfg := Config{Mode: ModeReview, MaxIterations: 50, CodexEnabled: false, AppConfig: appCfg}

	_, execs := (&executorFactory{}).Build(cfg, log)

	taskExec, ok := execs.Task.(*executor.CodexExecutor)
	require.True(t, ok, "task executor should be *executor.CodexExecutor when Executor=codex")
	reviewExec, ok := effectiveReviewExecutor(execs).(*executor.CodexExecutor)
	require.True(t, ok, "review executor should be *executor.CodexExecutor when Executor=codex")

	assert.Same(t, taskExec, reviewExec, "task and review must be the same shared codex instance")
	assert.True(t, taskExec.MultiAgent, "codex executor must have MultiAgent=true so any phase can spawn sub-agents")
}

func TestRunner_ClaudeMdSetupHint_EmitsOnce(t *testing.T) {
	home := setIsolatedHome(t)

	// arrange: ~/.claude/CLAUDE.md exists, ~/.codex/AGENTS.md does not
	claudeDir := filepath.Join(home, ".claude")
	require.NoError(t, os.MkdirAll(claudeDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(claudeDir, "CLAUDE.md"), []byte("user CLAUDE.md\n"), 0o600))

	var captured []string
	log := newCapturingLogger(&captured)

	// emit twice; only the first call should record output
	maybeEmitClaudeMdSetupHint(log)
	maybeEmitClaudeMdSetupHint(log)

	require.Len(t, captured, 1, "hint should emit exactly once")
	assert.Contains(t, captured[0], "~/.claude/CLAUDE.md exists but ~/.codex/AGENTS.md does not")
	assert.Contains(t, captured[0], "ln -s ~/.claude/CLAUDE.md ~/.codex/AGENTS.md")
}

func TestRunner_ClaudeMdSetupHint_SkippedWhenCodexAgentsMdExists(t *testing.T) {
	home := setIsolatedHome(t)

	claudeDir := filepath.Join(home, ".claude")
	require.NoError(t, os.MkdirAll(claudeDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(claudeDir, "CLAUDE.md"), []byte("user CLAUDE.md\n"), 0o600))

	codexDir := filepath.Join(home, ".codex")
	require.NoError(t, os.MkdirAll(codexDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(codexDir, "AGENTS.md"), []byte("user AGENTS.md\n"), 0o600))

	var captured []string
	log := newCapturingLogger(&captured)
	maybeEmitClaudeMdSetupHint(log)

	assert.Empty(t, captured, "hint must not emit when ~/.codex/AGENTS.md already exists")
}

func TestRunner_ClaudeMdSetupHint_SkippedWhenClaudeMdMissing(t *testing.T) {
	setIsolatedHome(t)

	var captured []string
	log := newCapturingLogger(&captured)
	maybeEmitClaudeMdSetupHint(log)

	assert.Empty(t, captured, "hint must not emit when ~/.claude/CLAUDE.md does not exist")
}

func TestRunner_ClaudeMdSetupHint_NotFiredWhenExecutorIsNotCodex(t *testing.T) {
	home := setIsolatedHome(t)

	claudeDir := filepath.Join(home, ".claude")
	require.NoError(t, os.MkdirAll(claudeDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(claudeDir, "CLAUDE.md"), []byte("user CLAUDE.md\n"), 0o600))

	var captured []string
	log := newCapturingLogger(&captured)
	holder := &status.PhaseHolder{}

	// claude executor with PassClaudeMd=true should NOT fire hint (gated by Executor=codex)
	appCfg := testAppConfig(t)
	appCfg.Executor = config.ExecutorClaude
	appCfg.PassClaudeMd = true
	cfg := Config{Mode: ModeReview, MaxIterations: 50, CodexEnabled: false, AppConfig: appCfg}

	_ = New(cfg, log, holder)
	assert.Empty(t, captured, "hint must not fire when Executor is not codex")
}

func TestRunner_ClaudeMdSetupHint_NotFiredWhenPassClaudeMdFalse(t *testing.T) {
	home := setIsolatedHome(t)

	claudeDir := filepath.Join(home, ".claude")
	require.NoError(t, os.MkdirAll(claudeDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(claudeDir, "CLAUDE.md"), []byte("user CLAUDE.md\n"), 0o600))

	var captured []string
	log := newCapturingLogger(&captured)
	holder := &status.PhaseHolder{}

	appCfg := testAppConfig(t)
	appCfg.Executor = config.ExecutorCodex
	appCfg.PassClaudeMd = false
	cfg := Config{Mode: ModeReview, MaxIterations: 50, CodexEnabled: false, AppConfig: appCfg}

	_ = New(cfg, log, holder)
	assert.Empty(t, captured, "hint must not fire when PassClaudeMd is false")
}

func TestRunner_ClaudeMdSetupHint_FiredOnceAcrossMultipleRunnerConstructions(t *testing.T) {
	home := setIsolatedHome(t)

	claudeDir := filepath.Join(home, ".claude")
	require.NoError(t, os.MkdirAll(claudeDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(claudeDir, "CLAUDE.md"), []byte("user CLAUDE.md\n"), 0o600))

	var captured []string
	log := newCapturingLogger(&captured)
	holder := &status.PhaseHolder{}

	appCfg := testAppConfig(t)
	appCfg.Executor = config.ExecutorCodex
	appCfg.PassClaudeMd = true
	cfg := Config{Mode: ModeReview, MaxIterations: 50, CodexEnabled: false, AppConfig: appCfg}

	// build two runners back to back; hint should emit on the first only
	_ = New(cfg, log, holder)
	_ = New(cfg, log, holder)

	require.Len(t, captured, 1, "hint must emit once per process across multiple runner constructions")
	assert.Contains(t, captured[0], "ln -s ~/.claude/CLAUDE.md ~/.codex/AGENTS.md")
}

func TestRunner_WaitOnLimit_RetriesLimitFromConfig(t *testing.T) {
	planFile := filepath.Join(t.TempDir(), "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n### Task 1: first\n- [x] done"), 0o600))

	appCfg := testAppConfig(t)
	appCfg.WaitOnLimit = time.Millisecond
	appCfg.WaitOnLimitSet = true
	task := newMockExecutor([]executor.Result{
		{Error: &executor.LimitPatternError{Pattern: "limit", HelpCmd: "usage"}},
		{Output: "task done", Signal: status.Completed},
	})
	cfg := Config{Mode: ModeTasksOnly, PlanFile: planFile, MaxIterations: 10, AppConfig: appCfg}
	r := NewWithExecutors(cfg, newRunnerMockLogger(""), Executors{Task: task}, &status.PhaseHolder{})

	err := r.Run(t.Context())

	require.NoError(t, err)
	assert.Len(t, task.RunCalls(), 2)
}

func TestRunner_WaitOnLimit_ZeroReturnsLimitError(t *testing.T) {
	planFile := filepath.Join(t.TempDir(), "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte("# Plan\n### Task 1: first\n- [ ] todo"), 0o600))

	appCfg := testAppConfig(t)
	appCfg.TaskRetryCountSet = false
	task := newMockExecutor([]executor.Result{{Error: &executor.LimitPatternError{Pattern: "limit", HelpCmd: "usage"}}})
	cfg := Config{Mode: ModeTasksOnly, PlanFile: planFile, MaxIterations: 10, AppConfig: appCfg}
	r := NewWithExecutors(cfg, newRunnerMockLogger(""), Executors{Task: task}, &status.PhaseHolder{})

	err := r.Run(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "pattern handling")
	assert.Len(t, task.RunCalls(), 1)
}
