package processor

import "time"

// TestRunnerConfig provides test access to runner's internal configuration.
// this file is only compiled during test builds (`go test`).
type TestRunnerConfig struct {
	IterationDelay time.Duration
	TaskRetryCount int
}

// TestConfig returns internal configuration values for testing.
func (r *Runner) TestConfig() TestRunnerConfig {
	return TestRunnerConfig{
		IterationDelay: r.iterationDelay,
		TaskRetryCount: r.taskRetryCount,
	}
}

// TestHasUncompletedTasks exposes hasUncompletedTasks for testing.
func (r *Runner) TestHasUncompletedTasks() bool {
	return r.hasUncompletedTasks()
}

// TestBuildCodexPrompt exposes buildCodexPrompt for testing.
func (r *Runner) TestBuildCodexPrompt(isFirst bool, claudeResponse string) string {
	return r.buildCodexPrompt(isFirst, claudeResponse)
}
