package processor

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/config"
)

func TestRunner_buildTaskPrompt(t *testing.T) {
	r := &Runner{cfg: Config{PlanFile: "docs/plans/test.md", ProgressPath: "progress-test.txt", AppConfig: testAppConfig(t)}, log: newMockLogger("")}
	prompt := r.buildTaskPrompt()

	assert.Contains(t, prompt, "docs/plans/test.md")
	assert.Contains(t, prompt, "progress-test.txt")
	assert.Contains(t, prompt, "<<<RALPHEX:ALL_TASKS_DONE>>>")
	assert.Contains(t, prompt, "<<<RALPHEX:TASK_FAILED>>>")
	assert.Contains(t, prompt, "ONE Task section per iteration")
	assert.Contains(t, prompt, "STOP HERE")
}

func TestRunner_buildFirstReviewPrompt(t *testing.T) {
	t.Run("with plan file and progress path", func(t *testing.T) {
		r := &Runner{cfg: Config{PlanFile: "docs/plans/test.md", ProgressPath: "progress-test.txt", AppConfig: testAppConfig(t)}, log: newMockLogger("")}
		prompt := r.buildFirstReviewPrompt()

		assert.Contains(t, prompt, "docs/plans/test.md")
		assert.Contains(t, prompt, "progress-test.txt") // progress file should be substituted
		assert.Contains(t, prompt, "git diff master...HEAD")
		assert.Contains(t, prompt, "<<<RALPHEX:REVIEW_DONE>>>")
		assert.Contains(t, prompt, "<<<RALPHEX:TASK_FAILED>>>")
		// verify expanded agent content from the 5 agents
		assert.Contains(t, prompt, "Use the Task tool to launch a general-purpose agent")
		assert.Contains(t, prompt, "security issues")          // from quality agent
		assert.Contains(t, prompt, "achieves the stated goal") // from implementation agent
		assert.Contains(t, prompt, "test coverage")            // from testing agent
	})

	t.Run("without plan file", func(t *testing.T) {
		r := &Runner{cfg: Config{PlanFile: "", ProgressPath: "progress.txt", AppConfig: testAppConfig(t)}, log: newMockLogger("")}
		prompt := r.buildFirstReviewPrompt()

		assert.Contains(t, prompt, "current branch vs master")
		assert.Contains(t, prompt, "progress.txt")
		assert.Contains(t, prompt, "<<<RALPHEX:REVIEW_DONE>>>")
	})
}

func TestRunner_buildSecondReviewPrompt(t *testing.T) {
	t.Run("with plan file and progress path", func(t *testing.T) {
		r := &Runner{cfg: Config{PlanFile: "docs/plans/test.md", ProgressPath: "progress-test.txt", AppConfig: testAppConfig(t)}, log: newMockLogger("")}
		prompt := r.buildSecondReviewPrompt()

		assert.Contains(t, prompt, "docs/plans/test.md")
		assert.Contains(t, prompt, "progress-test.txt") // progress file should be substituted
		assert.Contains(t, prompt, "git diff master...HEAD")
		assert.Contains(t, prompt, "<<<RALPHEX:REVIEW_DONE>>>")
		assert.Contains(t, prompt, "<<<RALPHEX:TASK_FAILED>>>")
		// verify expanded agent content from quality and implementation agents
		assert.Contains(t, prompt, "Use the Task tool to launch a general-purpose agent")
		assert.Contains(t, prompt, "security issues")          // from quality agent
		assert.Contains(t, prompt, "achieves the stated goal") // from implementation agent
		// should NOT have testing agent (only 2 agents for second pass)
		assert.NotContains(t, prompt, "test coverage")
	})

	t.Run("without plan file", func(t *testing.T) {
		r := &Runner{cfg: Config{PlanFile: "", ProgressPath: "progress.txt", AppConfig: testAppConfig(t)}, log: newMockLogger("")}
		prompt := r.buildSecondReviewPrompt()

		assert.Contains(t, prompt, "current branch vs master")
		assert.Contains(t, prompt, "progress.txt")
	})
}

func TestRunner_buildCodexEvaluationPrompt(t *testing.T) {
	findings := "Issue 1: Missing error check in foo.go:42"

	r := &Runner{cfg: Config{AppConfig: testAppConfig(t)}, log: newMockLogger("")}
	prompt := r.buildCodexEvaluationPrompt(findings)

	assert.Contains(t, prompt, findings)
	assert.Contains(t, prompt, "<<<RALPHEX:CODEX_REVIEW_DONE>>>")
	assert.Contains(t, prompt, "Codex (GPT-5.2)")
	assert.Contains(t, prompt, "Valid issues")
	assert.Contains(t, prompt, "Invalid/irrelevant issues")
}

func TestRunner_buildTaskPrompt_CustomPrompt(t *testing.T) {
	appCfg := &config.Config{
		TaskPrompt: "Custom task prompt for {{PLAN_FILE}} with progress at {{PROGRESS_FILE}}",
	}
	r := &Runner{cfg: Config{PlanFile: "docs/plans/test.md", ProgressPath: "progress-test.txt", AppConfig: appCfg}}
	prompt := r.buildTaskPrompt()

	assert.Equal(t, "Custom task prompt for docs/plans/test.md with progress at progress-test.txt", prompt)
	// verify it doesn't contain default prompt content
	assert.NotContains(t, prompt, "<<<RALPHEX:ALL_TASKS_DONE>>>")
}

func TestRunner_buildFirstReviewPrompt_CustomPrompt(t *testing.T) {
	appCfg := &config.Config{
		ReviewFirstPrompt: "Custom first review for {{GOAL}}",
	}

	t.Run("with plan file", func(t *testing.T) {
		r := &Runner{cfg: Config{PlanFile: "docs/plans/test.md", AppConfig: appCfg}}
		prompt := r.buildFirstReviewPrompt()

		assert.Equal(t, "Custom first review for implementation of plan at docs/plans/test.md", prompt)
	})

	t.Run("without plan file", func(t *testing.T) {
		r := &Runner{cfg: Config{PlanFile: "", AppConfig: appCfg}}
		prompt := r.buildFirstReviewPrompt()

		assert.Equal(t, "Custom first review for current branch vs master", prompt)
	})
}

func TestRunner_buildSecondReviewPrompt_CustomPrompt(t *testing.T) {
	appCfg := &config.Config{
		ReviewSecondPrompt: "Custom second review for {{GOAL}}",
	}
	r := &Runner{cfg: Config{PlanFile: "docs/plans/test.md", AppConfig: appCfg}}
	prompt := r.buildSecondReviewPrompt()

	assert.Equal(t, "Custom second review for implementation of plan at docs/plans/test.md", prompt)
}

func TestRunner_buildCodexEvaluationPrompt_CustomPrompt(t *testing.T) {
	appCfg := &config.Config{
		CodexPrompt: "Custom codex evaluation with output: {{CODEX_OUTPUT}} for {{GOAL}}",
	}
	r := &Runner{cfg: Config{PlanFile: "docs/plans/test.md", AppConfig: appCfg}}
	prompt := r.buildCodexEvaluationPrompt("found bug in main.go")

	assert.Equal(t, "Custom codex evaluation with output: found bug in main.go for implementation of plan at docs/plans/test.md", prompt)
}

func TestRunner_replacePromptVariables(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		planFile     string
		progressPath string
		expected     string
	}{
		{name: "plan file variable", input: "Plan: {{PLAN_FILE}}", planFile: "docs/plans/test.md", progressPath: "", expected: "Plan: docs/plans/test.md"},
		{name: "progress file variable", input: "Progress: {{PROGRESS_FILE}}", planFile: "docs/plans/test.md", progressPath: "prog.txt", expected: "Progress: prog.txt"},
		{name: "goal variable", input: "Goal: {{GOAL}}", planFile: "docs/plans/test.md", progressPath: "", expected: "Goal: implementation of plan at docs/plans/test.md"},
		{name: "multiple variables", input: "{{PLAN_FILE}} -> {{PROGRESS_FILE}}", planFile: "docs/plans/test.md", progressPath: "p.txt", expected: "docs/plans/test.md -> p.txt"},
		{name: "no variables", input: "plain text", planFile: "docs/plans/test.md", progressPath: "", expected: "plain text"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &Runner{cfg: Config{PlanFile: tc.planFile, ProgressPath: tc.progressPath}}
			result := r.replacePromptVariables(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestRunner_replacePromptVariables_NoGoal(t *testing.T) {
	r := &Runner{cfg: Config{PlanFile: ""}}
	result := r.replacePromptVariables("Goal: {{GOAL}}")
	assert.Equal(t, "Goal: current branch vs master", result)
}

func TestRunner_getPlanFileRef(t *testing.T) {
	t.Run("with plan file", func(t *testing.T) {
		r := &Runner{cfg: Config{PlanFile: "docs/plans/test.md"}}
		assert.Equal(t, "docs/plans/test.md", r.getPlanFileRef())
	})

	t.Run("without plan file", func(t *testing.T) {
		r := &Runner{cfg: Config{PlanFile: ""}}
		assert.Equal(t, "(no plan file - reviewing current branch)", r.getPlanFileRef())
	})
}

func TestRunner_getProgressFileRef(t *testing.T) {
	t.Run("with progress path", func(t *testing.T) {
		r := &Runner{cfg: Config{ProgressPath: "progress-test.txt"}}
		assert.Equal(t, "progress-test.txt", r.getProgressFileRef())
	})

	t.Run("without progress path", func(t *testing.T) {
		r := &Runner{cfg: Config{ProgressPath: ""}}
		assert.Equal(t, "(no progress file available)", r.getProgressFileRef())
	})
}

func TestRunner_replacePromptVariables_Fallbacks(t *testing.T) {
	t.Run("empty plan file uses fallback", func(t *testing.T) {
		r := &Runner{cfg: Config{PlanFile: "", ProgressPath: "progress.txt"}}
		result := r.replacePromptVariables("Plan: {{PLAN_FILE}}")
		assert.Equal(t, "Plan: (no plan file - reviewing current branch)", result)
	})

	t.Run("empty progress path uses fallback", func(t *testing.T) {
		r := &Runner{cfg: Config{PlanFile: "test.md", ProgressPath: ""}}
		result := r.replacePromptVariables("Progress: {{PROGRESS_FILE}}")
		assert.Equal(t, "Progress: (no progress file available)", result)
	})

	t.Run("both empty use fallbacks", func(t *testing.T) {
		r := &Runner{cfg: Config{PlanFile: "", ProgressPath: ""}}
		result := r.replacePromptVariables("Plan: {{PLAN_FILE}}, Progress: {{PROGRESS_FILE}}, Goal: {{GOAL}}")
		assert.Equal(t, "Plan: (no plan file - reviewing current branch), Progress: (no progress file available), Goal: current branch vs master", result)
	})
}

func TestRunner_expandAgentReferences_SingleAgent(t *testing.T) {
	appCfg := &config.Config{
		CustomAgents: []config.CustomAgent{{Name: "security-scanner", Prompt: "scan for security vulnerabilities"}},
	}
	r := &Runner{cfg: Config{AppConfig: appCfg}, log: newMockLogger("")}

	prompt := "Check code:\n{{agent:security-scanner}}\nDone."
	result := r.expandAgentReferences(prompt)

	assert.Contains(t, result, "Use the Task tool to launch a general-purpose agent with this prompt:")
	assert.Contains(t, result, "scan for security vulnerabilities")
	assert.Contains(t, result, "Report findings only - no positive observations.")
	assert.NotContains(t, result, "{{agent:security-scanner}}")
}

func TestRunner_expandAgentReferences_MultipleAgents(t *testing.T) {
	appCfg := &config.Config{
		CustomAgents: []config.CustomAgent{
			{Name: "agent-a", Prompt: "first agent prompt"},
			{Name: "agent-b", Prompt: "second agent prompt"},
		},
	}
	r := &Runner{cfg: Config{AppConfig: appCfg}, log: newMockLogger("")}

	prompt := "Run {{agent:agent-a}} then {{agent:agent-b}}."
	result := r.expandAgentReferences(prompt)

	assert.Contains(t, result, "first agent prompt")
	assert.Contains(t, result, "second agent prompt")
	assert.NotContains(t, result, "{{agent:agent-a}}")
	assert.NotContains(t, result, "{{agent:agent-b}}")
}

func TestRunner_expandAgentReferences_MissingAgent(t *testing.T) {
	appCfg := &config.Config{
		CustomAgents: []config.CustomAgent{{Name: "existing", Prompt: "exists"}},
	}
	log := newMockLogger("")
	r := &Runner{cfg: Config{AppConfig: appCfg}, log: log}

	prompt := "Run {{agent:missing-agent}} now."
	result := r.expandAgentReferences(prompt)

	// missing agent should remain unexpanded
	assert.Contains(t, result, "{{agent:missing-agent}}")
	assert.NotContains(t, result, "Use the Task tool")

	// verify warning was logged
	calls := log.PrintCalls()
	require.Len(t, calls, 1)
	assert.Contains(t, calls[0].Format, "[WARN]")
	assert.Contains(t, calls[0].Format, "not found")
}

func TestRunner_expandAgentReferences_NilAppConfig(t *testing.T) {
	r := &Runner{cfg: Config{AppConfig: nil}}
	prompt := "Run {{agent:test}} now."
	result := r.expandAgentReferences(prompt)
	assert.Equal(t, prompt, result)
}

func TestRunner_expandAgentReferences_EmptySlice(t *testing.T) {
	appCfg := &config.Config{CustomAgents: []config.CustomAgent{}}
	r := &Runner{cfg: Config{AppConfig: appCfg}}

	prompt := "Run {{agent:test}} now."
	result := r.expandAgentReferences(prompt)

	// empty agents slice, prompt unchanged
	assert.Equal(t, prompt, result)
}

func TestRunner_expandAgentReferences_NilAgentsSlice(t *testing.T) {
	appCfg := &config.Config{CustomAgents: nil}
	r := &Runner{cfg: Config{AppConfig: appCfg}}

	prompt := "Run {{agent:some-agent}} now."
	result := r.expandAgentReferences(prompt)

	// nil agents slice, prompt unchanged
	assert.Equal(t, prompt, result)
}

func TestRunner_expandAgentReferences_NoReferences(t *testing.T) {
	appCfg := &config.Config{
		CustomAgents: []config.CustomAgent{{Name: "scanner", Prompt: "scan code"}},
	}
	r := &Runner{cfg: Config{AppConfig: appCfg}, log: newMockLogger("")}

	prompt := "Plain prompt without agent references."
	result := r.expandAgentReferences(prompt)

	assert.Equal(t, prompt, result)
}

func TestRunner_expandAgentReferences_MixedVariables(t *testing.T) {
	appCfg := &config.Config{
		CustomAgents: []config.CustomAgent{{Name: "reviewer", Prompt: "review the code"}},
	}
	r := &Runner{cfg: Config{PlanFile: "docs/plans/test.md", ProgressPath: "progress.txt", AppConfig: appCfg}, log: newMockLogger("")}

	// test that agent refs work alongside other variables in replacePromptVariables
	prompt := "Plan: {{PLAN_FILE}}, Goal: {{GOAL}}, Agent: {{agent:reviewer}}"
	result := r.replacePromptVariables(prompt)

	assert.Contains(t, result, "Plan: docs/plans/test.md")
	assert.Contains(t, result, "Goal: implementation of plan at docs/plans/test.md")
	assert.Contains(t, result, "review the code")
	assert.NotContains(t, result, "{{agent:reviewer}}")
}

func TestRunner_expandAgentReferences_DuplicateReferences(t *testing.T) {
	appCfg := &config.Config{
		CustomAgents: []config.CustomAgent{{Name: "scanner", Prompt: "scan for issues"}},
	}
	r := &Runner{cfg: Config{AppConfig: appCfg}, log: newMockLogger("")}

	prompt := "First: {{agent:scanner}}\nSecond: {{agent:scanner}}"
	result := r.expandAgentReferences(prompt)

	// both references should be expanded
	assert.NotContains(t, result, "{{agent:scanner}}")
	// count occurrences of expansion
	assert.Equal(t, 2, strings.Count(result, "Use the Task tool to launch a general-purpose agent"))
	assert.Equal(t, 2, strings.Count(result, "scan for issues"))
}

func TestRunner_expandAgentReferences_SpecialCharactersInPrompt(t *testing.T) {
	appCfg := &config.Config{
		CustomAgents: []config.CustomAgent{
			{Name: "regex-agent", Prompt: "check for patterns like {{PLAN_FILE}} and $variables\nwith newlines\tand tabs"},
		},
	}
	r := &Runner{cfg: Config{AppConfig: appCfg}, log: newMockLogger("")}

	prompt := "Run {{agent:regex-agent}} now."
	result := r.expandAgentReferences(prompt)

	// prompt with special characters preserves newlines and tabs
	assert.NotContains(t, result, "{{agent:regex-agent}}")
	assert.Contains(t, result, "Use the Task tool to launch a general-purpose agent")
	assert.Contains(t, result, "{{PLAN_FILE}}")
	assert.Contains(t, result, "$variables")
	// verify actual newlines/tabs are preserved (not escaped as \n \t)
	assert.Contains(t, result, "\n")
	assert.Contains(t, result, "\t")
}

func TestRunner_expandAgentReferences_CaseSensitivity(t *testing.T) {
	appCfg := &config.Config{
		CustomAgents: []config.CustomAgent{{Name: "Scanner", Prompt: "uppercase name"}},
	}

	t.Run("lowercase reference does not match uppercase agent", func(t *testing.T) {
		r := &Runner{cfg: Config{AppConfig: appCfg}, log: newMockLogger("")}
		prompt := "Run {{agent:scanner}} now."
		result := r.expandAgentReferences(prompt)

		assert.Contains(t, result, "{{agent:scanner}}")
		assert.NotContains(t, result, "uppercase name")
	})

	t.Run("exact case matches", func(t *testing.T) {
		r := &Runner{cfg: Config{AppConfig: appCfg}, log: newMockLogger("")}
		prompt := "Run {{agent:Scanner}} now."
		result := r.expandAgentReferences(prompt)

		assert.NotContains(t, result, "{{agent:Scanner}}")
		assert.Contains(t, result, "uppercase name")
	})
}

func TestRunner_expandAgentReferences_PercentInPrompt(t *testing.T) {
	appCfg := &config.Config{
		CustomAgents: []config.CustomAgent{
			{Name: "perf", Prompt: "check if CPU is below 80% and memory under 90%"},
		},
	}
	r := &Runner{cfg: Config{AppConfig: appCfg}, log: newMockLogger("")}

	prompt := "Run {{agent:perf}} now."
	result := r.expandAgentReferences(prompt)

	assert.Contains(t, result, "80%")
	assert.Contains(t, result, "90%")
	assert.NotContains(t, result, "{{agent:perf}}")
}

func TestRunner_buildPlanPrompt(t *testing.T) {
	t.Run("substitutes plan description and progress file", func(t *testing.T) {
		appCfg := testAppConfig(t)
		r := &Runner{cfg: Config{
			PlanDescription: "add user authentication with OAuth",
			ProgressPath:    "progress-plan-test.txt",
			AppConfig:       appCfg,
		}, log: newMockLogger("")}

		prompt := r.buildPlanPrompt()

		// verify template substitution
		assert.Contains(t, prompt, "add user authentication with OAuth")
		assert.Contains(t, prompt, "progress-plan-test.txt")
		// verify no unsubstituted variables
		assert.NotContains(t, prompt, "{{PLAN_DESCRIPTION}}")
		assert.NotContains(t, prompt, "{{PROGRESS_FILE}}")
	})

	t.Run("uses progress file fallback when empty", func(t *testing.T) {
		appCfg := testAppConfig(t)
		r := &Runner{cfg: Config{
			PlanDescription: "add feature",
			ProgressPath:    "", // empty progress path
			AppConfig:       appCfg,
		}, log: newMockLogger("")}

		prompt := r.buildPlanPrompt()

		assert.Contains(t, prompt, "add feature")
		assert.Contains(t, prompt, "(no progress file available)")
	})

	t.Run("preserves prompt structure", func(t *testing.T) {
		appCfg := testAppConfig(t)
		r := &Runner{cfg: Config{
			PlanDescription: "test plan",
			ProgressPath:    "progress.txt",
			AppConfig:       appCfg,
		}, log: newMockLogger("")}

		prompt := r.buildPlanPrompt()

		// verify key structural elements from make_plan.txt are present
		assert.Contains(t, prompt, "QUESTION")
		assert.Contains(t, prompt, "PLAN_READY")
		assert.Contains(t, prompt, "docs/plans/")
	})

	t.Run("custom prompt", func(t *testing.T) {
		appCfg := &config.Config{
			MakePlanPrompt: "Create plan for: {{PLAN_DESCRIPTION}}\nLog: {{PROGRESS_FILE}}",
		}
		r := &Runner{cfg: Config{
			PlanDescription: "custom feature",
			ProgressPath:    "custom-progress.txt",
			AppConfig:       appCfg,
		}, log: newMockLogger("")}

		prompt := r.buildPlanPrompt()

		assert.Equal(t, "Create plan for: custom feature\nLog: custom-progress.txt", prompt)
	})
}
