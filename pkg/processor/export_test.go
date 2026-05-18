package processor

import (
	"context"
	"sync"
	"time"

	"github.com/umputun/ralphex/pkg/executor"
)

// ResetClaudeMdHintOnceForTest resets the package-level sync.Once that gates the
// CLAUDE.md setup hint so each test can exercise the first-emit path.
func ResetClaudeMdHintOnceForTest() {
	claudeMdHintOnce = sync.Once{}
}

// EmitClaudeMdSetupHintForTest exposes the hint helper for unit tests.
func EmitClaudeMdSetupHintForTest(log Logger) {
	maybeEmitClaudeMdSetupHint(log)
}

// TestRunnerConfig provides test access to runner's internal configuration.
// this file is only compiled during test builds (`go test`).
type TestRunnerConfig struct {
	IterationDelay time.Duration
	TaskRetryCount int
	WaitOnLimit    time.Duration
}

// TestConfig returns internal configuration values for testing.
func (r *Runner) TestConfig() TestRunnerConfig {
	return TestRunnerConfig{
		IterationDelay: r.iterationDelay,
		TaskRetryCount: r.taskRetryCount,
		WaitOnLimit:    r.waitOnLimit,
	}
}

// TestRunWithLimitRetry exposes runWithLimitRetry for testing.
func (r *Runner) TestRunWithLimitRetry(ctx context.Context, run func(context.Context, string) executor.Result,
	prompt, toolName string) executor.Result {
	return r.runWithLimitRetry(ctx, run, prompt, toolName)
}

// TestHasUncompletedTasks exposes hasUncompletedTasks for testing.
func (r *Runner) TestHasUncompletedTasks() bool {
	return r.hasUncompletedTasks()
}

// TestValidatePlanHasTasks exposes validatePlanHasTasks for testing.
func (r *Runner) TestValidatePlanHasTasks() error {
	return r.validatePlanHasTasks()
}

// TestBuildCodexPrompt exposes buildCodexPrompt for testing.
func (r *Runner) TestBuildCodexPrompt(isFirst bool, claudeResponse string) string {
	return r.buildCodexPrompt(isFirst, claudeResponse)
}

// TestNextPlanTaskPosition exposes nextPlanTaskPosition for testing.
func (r *Runner) TestNextPlanTaskPosition() int {
	return r.nextPlanTaskPosition()
}

// TestRunWithSessionTimeout exposes runWithSessionTimeout for testing.
func (r *Runner) TestRunWithSessionTimeout(ctx context.Context, run func(context.Context, string) executor.Result,
	prompt, toolName string) executor.Result {
	return r.runWithSessionTimeout(ctx, run, prompt, toolName)
}

// TestSetTaskPhaseOverride sets a function that replaces runTaskPhase for testing abort handling.
func (r *Runner) TestSetTaskPhaseOverride(fn func(ctx context.Context) error) {
	r.taskPhaseOverride = fn
}

// TestDrainBreakCh exposes drainBreakCh for testing.
func (r *Runner) TestDrainBreakCh() {
	r.drainBreakCh()
}

// TestTaskExecutor returns the task-phase executor for inspection.
func (r *Runner) TestTaskExecutor() Executor {
	return r.task
}

// TestReviewExecutor returns the review-phase executor for inspection.
func (r *Runner) TestReviewExecutor() Executor {
	return r.review
}

// TestExternalExecutor returns the external review executor for inspection.
func (r *Runner) TestExternalExecutor() Executor {
	return r.external
}
