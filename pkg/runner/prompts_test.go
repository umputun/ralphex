package runner

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildTaskPrompt(t *testing.T) {
	prompt := buildTaskPrompt("docs/plans/test.md", "progress-test.txt")

	assert.Contains(t, prompt, "docs/plans/test.md")
	assert.Contains(t, prompt, "progress-test.txt")
	assert.Contains(t, prompt, "<<<RALPHEX:ALL_TASKS_DONE>>>")
	assert.Contains(t, prompt, "<<<RALPHEX:TASK_FAILED>>>")
	assert.Contains(t, prompt, "ONE Task section per iteration")
	assert.Contains(t, prompt, "STOP HERE")
}

func TestBuildFirstReviewPrompt(t *testing.T) {
	t.Run("with plan file", func(t *testing.T) {
		prompt := buildFirstReviewPrompt("docs/plans/test.md")

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
		prompt := buildFirstReviewPrompt("")

		assert.Contains(t, prompt, "current branch vs master")
		assert.Contains(t, prompt, "<<<RALPHEX:REVIEW_DONE>>>")
	})
}

func TestBuildSecondReviewPrompt(t *testing.T) {
	t.Run("with plan file", func(t *testing.T) {
		prompt := buildSecondReviewPrompt("docs/plans/test.md")

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
		prompt := buildSecondReviewPrompt("")

		assert.Contains(t, prompt, "current branch vs master")
	})
}

func TestBuildCodexEvaluationPrompt(t *testing.T) {
	findings := "Issue 1: Missing error check in foo.go:42"

	prompt := buildCodexEvaluationPrompt(findings)

	assert.Contains(t, prompt, findings)
	assert.Contains(t, prompt, "<<<RALPHEX:CODEX_REVIEW_DONE>>>")
	assert.Contains(t, prompt, "Codex (GPT-5.2)")
	assert.Contains(t, prompt, "Valid issues")
	assert.Contains(t, prompt, "Invalid/irrelevant issues")
}

func TestBuildContinuePrompt(t *testing.T) {
	t.Run("short output", func(t *testing.T) {
		prompt := buildContinuePrompt("short output")

		assert.Contains(t, prompt, "Continue from where you left off")
		assert.Contains(t, prompt, "short output")
		assert.Contains(t, prompt, "<<<RALPHEX:ALL_TASKS_DONE>>>")
		assert.Contains(t, prompt, "<<<RALPHEX:TASK_FAILED>>>")
	})

	t.Run("long output truncated", func(t *testing.T) {
		// use 'z' to avoid matching other letters in the prompt template
		longOutput := make([]byte, 1000)
		for i := range longOutput {
			longOutput[i] = 'z'
		}

		prompt := buildContinuePrompt(string(longOutput))

		// should only contain last 500 chars
		assert.Contains(t, prompt, "Previous Output (last 500 chars)")
		// count z's - should be exactly 500
		count := 0
		for _, c := range prompt {
			if c == 'z' {
				count++
			}
		}
		assert.Equal(t, 500, count)
	})
}
