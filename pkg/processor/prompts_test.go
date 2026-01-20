package processor

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRunner_buildTaskPrompt(t *testing.T) {
	r := &Runner{cfg: Config{PlanFile: "docs/plans/test.md"}}
	prompt := r.buildTaskPrompt("progress-test.txt")

	assert.Contains(t, prompt, "docs/plans/test.md")
	assert.Contains(t, prompt, "progress-test.txt")
	assert.Contains(t, prompt, "<<<RALPHEX:ALL_TASKS_DONE>>>")
	assert.Contains(t, prompt, "<<<RALPHEX:TASK_FAILED>>>")
	assert.Contains(t, prompt, "ONE Task section per iteration")
	assert.Contains(t, prompt, "STOP HERE")
}

func TestRunner_buildFirstReviewPrompt(t *testing.T) {
	t.Run("with plan file", func(t *testing.T) {
		r := &Runner{cfg: Config{PlanFile: "docs/plans/test.md"}}
		prompt := r.buildFirstReviewPrompt()

		assert.Contains(t, prompt, "docs/plans/test.md")
		assert.Contains(t, prompt, "git diff master...HEAD")
		assert.Contains(t, prompt, "<<<RALPHEX:REVIEW_DONE>>>")
		assert.Contains(t, prompt, "<<<RALPHEX:TASK_FAILED>>>")
		// verify 8 agents are listed
		assert.Contains(t, prompt, "qa-expert")
		assert.Contains(t, prompt, "go-test-expert")
		assert.Contains(t, prompt, "go-smells-expert")
		assert.Contains(t, prompt, "go-simplify-expert")
		assert.Contains(t, prompt, "go-error-auditor")
		assert.Contains(t, prompt, "go-docs-analyzer")
		assert.Contains(t, prompt, "implementation-reviewer")
		assert.Contains(t, prompt, "documentation-expert")
	})

	t.Run("without plan file", func(t *testing.T) {
		r := &Runner{cfg: Config{PlanFile: ""}}
		prompt := r.buildFirstReviewPrompt()

		assert.Contains(t, prompt, "current branch vs master")
		assert.Contains(t, prompt, "<<<RALPHEX:REVIEW_DONE>>>")
	})
}

func TestRunner_buildSecondReviewPrompt(t *testing.T) {
	t.Run("with plan file", func(t *testing.T) {
		r := &Runner{cfg: Config{PlanFile: "docs/plans/test.md"}}
		prompt := r.buildSecondReviewPrompt()

		assert.Contains(t, prompt, "docs/plans/test.md")
		assert.Contains(t, prompt, "git diff master...HEAD")
		assert.Contains(t, prompt, "<<<RALPHEX:REVIEW_DONE>>>")
		assert.Contains(t, prompt, "<<<RALPHEX:TASK_FAILED>>>")
		assert.Contains(t, prompt, "qa-expert")
		assert.Contains(t, prompt, "implementation-reviewer")
		// should NOT have all 8 agents (only 2 for second pass)
		assert.NotContains(t, prompt, "go-smells-expert")
	})

	t.Run("without plan file", func(t *testing.T) {
		r := &Runner{cfg: Config{PlanFile: ""}}
		prompt := r.buildSecondReviewPrompt()

		assert.Contains(t, prompt, "current branch vs master")
	})
}

func TestRunner_buildCodexEvaluationPrompt(t *testing.T) {
	findings := "Issue 1: Missing error check in foo.go:42"

	r := &Runner{cfg: Config{}}
	prompt := r.buildCodexEvaluationPrompt(findings)

	assert.Contains(t, prompt, findings)
	assert.Contains(t, prompt, "<<<RALPHEX:CODEX_REVIEW_DONE>>>")
	assert.Contains(t, prompt, "Codex (GPT-5.2)")
	assert.Contains(t, prompt, "Valid issues")
	assert.Contains(t, prompt, "Invalid/irrelevant issues")
}
