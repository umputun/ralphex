package processor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/config"
)

func TestRunner_replacePromptVariables_TaskPrompt(t *testing.T) {
	appCfg := testAppConfig(t)
	r := &Runner{cfg: Config{PlanFile: "docs/plans/test.md", ProgressPath: "progress-test.txt", AppConfig: appCfg}, log: newMockLogger("")}
	prompt := r.replacePromptVariables(appCfg.TaskPrompt)

	assert.Contains(t, prompt, "docs/plans/test.md")
	assert.Contains(t, prompt, "progress-test.txt")
	assert.Contains(t, prompt, "<<<RALPHEX:ALL_TASKS_DONE>>>")
	assert.Contains(t, prompt, "<<<RALPHEX:TASK_FAILED>>>")
	assert.Contains(t, prompt, "ONE Task section per iteration")
	assert.Contains(t, prompt, "STOP HERE")
}

func TestRunner_replacePromptVariables_ReviewFirstPrompt(t *testing.T) {
	t.Run("with plan file and progress path", func(t *testing.T) {
		appCfg := testAppConfig(t)
		r := &Runner{cfg: Config{PlanFile: "docs/plans/test.md", ProgressPath: "progress-test.txt", DefaultBranch: "main", AppConfig: appCfg}, log: newMockLogger("")}
		prompt := r.replacePromptVariables(appCfg.ReviewFirstPrompt)

		assert.Contains(t, prompt, "docs/plans/test.md")
		assert.Contains(t, prompt, "progress-test.txt") // progress file should be substituted
		assert.Contains(t, prompt, "git diff main...HEAD")
		assert.Contains(t, prompt, "<<<RALPHEX:REVIEW_DONE>>>")
		assert.Contains(t, prompt, "<<<RALPHEX:TASK_FAILED>>>")
		// verify expanded agent content from the 5 agents
		assert.Contains(t, prompt, "Use the Task tool to launch a general-purpose agent")
		assert.Contains(t, prompt, "security issues")          // from quality agent
		assert.Contains(t, prompt, "achieves the stated goal") // from implementation agent
		assert.Contains(t, prompt, "test coverage")            // from testing agent
		// verify no unsubstituted template variables remain
		assert.NotContains(t, prompt, "{{DEFAULT_BRANCH}}")
	})

	t.Run("without plan file uses default branch in goal", func(t *testing.T) {
		appCfg := testAppConfig(t)
		r := &Runner{cfg: Config{PlanFile: "", ProgressPath: "progress.txt", DefaultBranch: "trunk", AppConfig: appCfg}, log: newMockLogger("")}
		prompt := r.replacePromptVariables(appCfg.ReviewFirstPrompt)

		assert.Contains(t, prompt, "current branch vs trunk")
		assert.Contains(t, prompt, "progress.txt")
		assert.Contains(t, prompt, "<<<RALPHEX:REVIEW_DONE>>>")
	})

	t.Run("fallback to master when default branch not set", func(t *testing.T) {
		appCfg := testAppConfig(t)
		r := &Runner{cfg: Config{PlanFile: "", ProgressPath: "progress.txt", AppConfig: appCfg}, log: newMockLogger("")}
		prompt := r.replacePromptVariables(appCfg.ReviewFirstPrompt)

		assert.Contains(t, prompt, "current branch vs master")
	})
}

func TestRunner_replacePromptVariables_ReviewSecondPrompt(t *testing.T) {
	t.Run("with plan file and progress path", func(t *testing.T) {
		appCfg := testAppConfig(t)
		r := &Runner{cfg: Config{PlanFile: "docs/plans/test.md", ProgressPath: "progress-test.txt", DefaultBranch: "main", AppConfig: appCfg}, log: newMockLogger("")}
		prompt := r.replacePromptVariables(appCfg.ReviewSecondPrompt)

		assert.Contains(t, prompt, "docs/plans/test.md")
		assert.Contains(t, prompt, "progress-test.txt") // progress file should be substituted
		assert.Contains(t, prompt, "git diff main...HEAD")
		assert.Contains(t, prompt, "<<<RALPHEX:REVIEW_DONE>>>")
		assert.Contains(t, prompt, "<<<RALPHEX:TASK_FAILED>>>")
		// verify expanded agent content from quality and implementation agents
		assert.Contains(t, prompt, "Use the Task tool to launch a general-purpose agent")
		assert.Contains(t, prompt, "security issues")          // from quality agent
		assert.Contains(t, prompt, "achieves the stated goal") // from implementation agent
		// should NOT have testing agent (only 2 agents for second pass)
		assert.NotContains(t, prompt, "test coverage")
		// verify no unsubstituted template variables remain
		assert.NotContains(t, prompt, "{{DEFAULT_BRANCH}}")
	})

	t.Run("without plan file uses default branch in goal", func(t *testing.T) {
		appCfg := testAppConfig(t)
		r := &Runner{cfg: Config{PlanFile: "", ProgressPath: "progress.txt", DefaultBranch: "develop", AppConfig: appCfg}, log: newMockLogger("")}
		prompt := r.replacePromptVariables(appCfg.ReviewSecondPrompt)

		assert.Contains(t, prompt, "current branch vs develop")
		assert.Contains(t, prompt, "progress.txt")
	})
}

func TestRunner_replacePromptVariables_NoAgentWarningsInEmbeddedPrompts(t *testing.T) {
	// regression test for issue #98: comment lines in embedded prompts contained {{agent:name}}
	// which triggered "agent not found" warnings after stripComments was removed in #90
	appCfg := testAppConfig(t)
	log := newMockLogger("")
	r := &Runner{cfg: Config{PlanFile: "docs/plans/test.md", ProgressPath: "progress.txt", DefaultBranch: "main", AppConfig: appCfg}, log: log}

	r.replacePromptVariables(appCfg.ReviewFirstPrompt)
	r.replacePromptVariables(appCfg.ReviewSecondPrompt)

	// verify no "not found" warnings were logged
	for _, call := range log.PrintCalls() {
		assert.NotContains(t, call.Format, "not found", "unexpected agent warning: %s", call.Format)
	}
}

func TestRunner_buildCodexEvaluationPrompt(t *testing.T) {
	findings := "Issue 1: Missing error check in foo.go:42"

	r := &Runner{cfg: Config{AppConfig: testAppConfig(t)}, log: newMockLogger("")}
	prompt := r.buildCodexEvaluationPrompt(findings)

	assert.Contains(t, prompt, findings)
	assert.Contains(t, prompt, "<<<RALPHEX:CODEX_REVIEW_DONE>>>")
	assert.Contains(t, prompt, "Codex reviewed the code")
	assert.Contains(t, prompt, "Valid issues")
	assert.Contains(t, prompt, "Invalid/irrelevant issues")
}

func TestRunner_replacePromptVariables_CustomTaskPrompt(t *testing.T) {
	appCfg := &config.Config{
		TaskPrompt: "Custom task prompt for {{PLAN_FILE}} with progress at {{PROGRESS_FILE}}",
	}
	r := &Runner{cfg: Config{PlanFile: "docs/plans/test.md", ProgressPath: "progress-test.txt", AppConfig: appCfg}}
	prompt := r.replacePromptVariables(appCfg.TaskPrompt)

	assert.Equal(t, "Custom task prompt for docs/plans/test.md with progress at progress-test.txt", prompt)
	// verify it doesn't contain default prompt content
	assert.NotContains(t, prompt, "<<<RALPHEX:ALL_TASKS_DONE>>>")
}

func TestRunner_replacePromptVariables_CustomReviewFirstPrompt(t *testing.T) {
	appCfg := &config.Config{
		ReviewFirstPrompt: "Custom first review for {{GOAL}}",
	}

	t.Run("with plan file", func(t *testing.T) {
		r := &Runner{cfg: Config{PlanFile: "docs/plans/test.md", AppConfig: appCfg}}
		prompt := r.replacePromptVariables(appCfg.ReviewFirstPrompt)

		assert.Equal(t, "Custom first review for implementation of plan at docs/plans/test.md", prompt)
	})

	t.Run("without plan file uses default branch", func(t *testing.T) {
		r := &Runner{cfg: Config{PlanFile: "", DefaultBranch: "main", AppConfig: appCfg}}
		prompt := r.replacePromptVariables(appCfg.ReviewFirstPrompt)

		assert.Equal(t, "Custom first review for current branch vs main", prompt)
	})

	t.Run("without plan file fallback to master", func(t *testing.T) {
		r := &Runner{cfg: Config{PlanFile: "", AppConfig: appCfg}}
		prompt := r.replacePromptVariables(appCfg.ReviewFirstPrompt)

		assert.Equal(t, "Custom first review for current branch vs master", prompt)
	})
}

func TestRunner_replacePromptVariables_CustomReviewSecondPrompt(t *testing.T) {
	appCfg := &config.Config{
		ReviewSecondPrompt: "Custom second review for {{GOAL}}",
	}
	r := &Runner{cfg: Config{PlanFile: "docs/plans/test.md", AppConfig: appCfg}}
	prompt := r.replacePromptVariables(appCfg.ReviewSecondPrompt)

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
	t.Run("fallback to master when default branch not set", func(t *testing.T) {
		r := &Runner{cfg: Config{PlanFile: ""}}
		result := r.replacePromptVariables("Goal: {{GOAL}}")
		assert.Equal(t, "Goal: current branch vs master", result)
	})

	t.Run("uses configured default branch", func(t *testing.T) {
		r := &Runner{cfg: Config{PlanFile: "", DefaultBranch: "trunk"}}
		result := r.replacePromptVariables("Goal: {{GOAL}}")
		assert.Equal(t, "Goal: current branch vs trunk", result)
	})
}

func TestRunner_replacePromptVariables_DefaultBranch(t *testing.T) {
	t.Run("replaces DEFAULT_BRANCH variable", func(t *testing.T) {
		r := &Runner{cfg: Config{DefaultBranch: "main"}}
		result := r.replacePromptVariables("git diff {{DEFAULT_BRANCH}}...HEAD")
		assert.Equal(t, "git diff main...HEAD", result)
	})

	t.Run("fallback to master when not configured", func(t *testing.T) {
		r := &Runner{cfg: Config{}}
		result := r.replacePromptVariables("git diff {{DEFAULT_BRANCH}}...HEAD")
		assert.Equal(t, "git diff master...HEAD", result)
	})
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

func TestRunner_resolvePlanFilePath(t *testing.T) {
	t.Run("empty plan file returns empty", func(t *testing.T) {
		r := &Runner{cfg: Config{PlanFile: ""}}
		assert.Empty(t, r.resolvePlanFilePath())
	})

	t.Run("file exists at original location", func(t *testing.T) {
		tmpDir := t.TempDir()
		planPath := filepath.Join(tmpDir, "docs", "plans", "test.md")
		require.NoError(t, os.MkdirAll(filepath.Dir(planPath), 0o700))
		require.NoError(t, os.WriteFile(planPath, []byte("# plan"), 0o600))

		r := &Runner{cfg: Config{PlanFile: planPath}}
		assert.Equal(t, planPath, r.resolvePlanFilePath())
	})

	t.Run("file moved to completed directory", func(t *testing.T) {
		tmpDir := t.TempDir()
		plansDir := filepath.Join(tmpDir, "docs", "plans")
		completedDir := filepath.Join(plansDir, "completed")
		require.NoError(t, os.MkdirAll(completedDir, 0o700))

		originalPath := filepath.Join(plansDir, "test.md")
		completedPath := filepath.Join(completedDir, "test.md")
		require.NoError(t, os.WriteFile(completedPath, []byte("# plan"), 0o600))

		r := &Runner{cfg: Config{PlanFile: originalPath}}
		assert.Equal(t, completedPath, r.resolvePlanFilePath())
	})

	t.Run("file not found anywhere returns original path", func(t *testing.T) {
		r := &Runner{cfg: Config{PlanFile: "/nonexistent/path/plan.md"}}
		assert.Equal(t, "/nonexistent/path/plan.md", r.resolvePlanFilePath())
	})

	t.Run("getPlanFileRef uses resolved path", func(t *testing.T) {
		tmpDir := t.TempDir()
		plansDir := filepath.Join(tmpDir, "docs", "plans")
		completedDir := filepath.Join(plansDir, "completed")
		require.NoError(t, os.MkdirAll(completedDir, 0o700))

		originalPath := filepath.Join(plansDir, "test.md")
		completedPath := filepath.Join(completedDir, "test.md")
		require.NoError(t, os.WriteFile(completedPath, []byte("# plan"), 0o600))

		r := &Runner{cfg: Config{PlanFile: originalPath}}
		assert.Equal(t, completedPath, r.getPlanFileRef())
	})

	t.Run("getGoal uses resolved path", func(t *testing.T) {
		tmpDir := t.TempDir()
		plansDir := filepath.Join(tmpDir, "docs", "plans")
		completedDir := filepath.Join(plansDir, "completed")
		require.NoError(t, os.MkdirAll(completedDir, 0o700))

		originalPath := filepath.Join(plansDir, "test.md")
		completedPath := filepath.Join(completedDir, "test.md")
		require.NoError(t, os.WriteFile(completedPath, []byte("# plan"), 0o600))

		r := &Runner{cfg: Config{PlanFile: originalPath}}
		assert.Contains(t, r.getGoal(), completedPath)
		assert.NotContains(t, r.getGoal(), originalPath)
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
			{Name: "regex-agent", Prompt: "check for patterns and $variables\nwith newlines\tand tabs"},
		},
	}
	r := &Runner{cfg: Config{AppConfig: appCfg}, log: newMockLogger("")}

	prompt := "Run {{agent:regex-agent}} now."
	result := r.expandAgentReferences(prompt)

	// prompt with special characters preserves newlines and tabs
	assert.NotContains(t, result, "{{agent:regex-agent}}")
	assert.Contains(t, result, "Use the Task tool to launch a general-purpose agent")
	assert.Contains(t, result, "$variables")
	// verify actual newlines/tabs are preserved (not escaped as \n \t)
	assert.Contains(t, result, "\n")
	assert.Contains(t, result, "\t")
}

func TestRunner_expandAgentReferences_ExpandsVariablesInContent(t *testing.T) {
	t.Run("expands all template variables in agent content", func(t *testing.T) {
		appCfg := &config.Config{
			CustomAgents: []config.CustomAgent{
				{Name: "review", Prompt: "review changes on {{DEFAULT_BRANCH}}, plan: {{PLAN_FILE}}, goal: {{GOAL}}"},
			},
		}
		r := &Runner{cfg: Config{PlanFile: "docs/plan.md", DefaultBranch: "main", AppConfig: appCfg}, log: newMockLogger("")}

		prompt := "Run {{agent:review}}"
		result := r.expandAgentReferences(prompt)

		assert.Contains(t, result, "review changes on main")
		assert.Contains(t, result, "plan: docs/plan.md")
		assert.Contains(t, result, "goal: implementation of plan at docs/plan.md")
		assert.NotContains(t, result, "{{DEFAULT_BRANCH}}")
		assert.NotContains(t, result, "{{PLAN_FILE}}")
		assert.NotContains(t, result, "{{GOAL}}")
	})

	t.Run("uses fallbacks when config values not set", func(t *testing.T) {
		appCfg := &config.Config{
			CustomAgents: []config.CustomAgent{
				{Name: "review", Prompt: "diff {{DEFAULT_BRANCH}}..HEAD"},
			},
		}
		r := &Runner{cfg: Config{AppConfig: appCfg}, log: newMockLogger("")}

		prompt := "Run {{agent:review}}"
		result := r.expandAgentReferences(prompt)

		assert.Contains(t, result, "diff master..HEAD")
	})
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

func TestRunner_expandAgentReferences_WithModelAndAgentType(t *testing.T) {
	t.Run("both model and agent type", func(t *testing.T) {
		appCfg := &config.Config{
			CustomAgents: []config.CustomAgent{
				{Name: "docs", Prompt: "Check docs.", Options: config.Options{Model: "haiku", AgentType: "code-reviewer"}},
			},
		}
		r := &Runner{cfg: Config{AppConfig: appCfg}, log: newMockLogger("")}

		result := r.expandAgentReferences("Launch {{agent:docs}}")
		assert.Contains(t, result, "model=haiku")
		assert.Contains(t, result, "code-reviewer")
		assert.Contains(t, result, "Check docs.")
		assert.NotContains(t, result, "general-purpose")
	})

	t.Run("model only uses default agent type", func(t *testing.T) {
		appCfg := &config.Config{
			CustomAgents: []config.CustomAgent{
				{Name: "lint", Prompt: "Lint code.", Options: config.Options{Model: "sonnet"}},
			},
		}
		r := &Runner{cfg: Config{AppConfig: appCfg}, log: newMockLogger("")}

		result := r.expandAgentReferences("Run {{agent:lint}}")
		assert.Contains(t, result, "model=sonnet")
		assert.Contains(t, result, "general-purpose")
		assert.Contains(t, result, "Lint code.")
	})

	t.Run("agent type only uses no model clause", func(t *testing.T) {
		appCfg := &config.Config{
			CustomAgents: []config.CustomAgent{
				{Name: "review", Prompt: "Review code.", Options: config.Options{AgentType: "code-reviewer"}},
			},
		}
		r := &Runner{cfg: Config{AppConfig: appCfg}, log: newMockLogger("")}

		result := r.expandAgentReferences("Run {{agent:review}}")
		assert.NotContains(t, result, "model=")
		assert.Contains(t, result, "code-reviewer")
		assert.Contains(t, result, "Review code.")
	})

	t.Run("no overrides uses defaults", func(t *testing.T) {
		appCfg := &config.Config{
			CustomAgents: []config.CustomAgent{
				{Name: "basic", Prompt: "Basic check."},
			},
		}
		r := &Runner{cfg: Config{AppConfig: appCfg}, log: newMockLogger("")}

		result := r.expandAgentReferences("Run {{agent:basic}}")
		assert.NotContains(t, result, "model=")
		assert.Contains(t, result, "general-purpose")
		assert.Contains(t, result, "Basic check.")
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

	t.Run("uses custom plans dir from config", func(t *testing.T) {
		appCfg := testAppConfig(t)
		appCfg.PlansDir = "custom/plans"
		r := &Runner{cfg: Config{
			PlanDescription: "test plan",
			ProgressPath:    "progress.txt",
			AppConfig:       appCfg,
		}, log: newMockLogger("")}

		prompt := r.buildPlanPrompt()

		assert.Contains(t, prompt, "custom/plans/")
		assert.NotContains(t, prompt, "{{PLANS_DIR}}")
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

func TestRunner_getDiffInstruction(t *testing.T) {
	t.Run("first iteration uses branch diff", func(t *testing.T) {
		r := &Runner{cfg: Config{DefaultBranch: "main"}}
		result := r.getDiffInstruction(true)
		assert.Equal(t, "git diff main...HEAD", result)
	})

	t.Run("subsequent iteration uses uncommitted diff", func(t *testing.T) {
		r := &Runner{cfg: Config{DefaultBranch: "main"}}
		result := r.getDiffInstruction(false)
		assert.Equal(t, "git diff", result)
	})

	t.Run("uses default branch fallback", func(t *testing.T) {
		r := &Runner{cfg: Config{}}
		result := r.getDiffInstruction(true)
		assert.Equal(t, "git diff master...HEAD", result)
	})
}

func TestRunner_replaceVariablesWithIteration(t *testing.T) {
	t.Run("replaces DIFF_INSTRUCTION for first iteration", func(t *testing.T) {
		r := &Runner{cfg: Config{DefaultBranch: "main"}}
		result := r.replaceVariablesWithIteration("Run: {{DIFF_INSTRUCTION}}", true)
		assert.Equal(t, "Run: git diff main...HEAD", result)
	})

	t.Run("replaces DIFF_INSTRUCTION for subsequent iteration", func(t *testing.T) {
		r := &Runner{cfg: Config{DefaultBranch: "main"}}
		result := r.replaceVariablesWithIteration("Run: {{DIFF_INSTRUCTION}}", false)
		assert.Equal(t, "Run: git diff", result)
	})

	t.Run("replaces all variables together", func(t *testing.T) {
		r := &Runner{cfg: Config{
			PlanFile:      "docs/plans/test.md",
			ProgressPath:  "progress.txt",
			DefaultBranch: "develop",
		}}
		prompt := "Plan: {{PLAN_FILE}}, Progress: {{PROGRESS_FILE}}, Goal: {{GOAL}}, Branch: {{DEFAULT_BRANCH}}, Diff: {{DIFF_INSTRUCTION}}"
		result := r.replaceVariablesWithIteration(prompt, true)

		assert.Contains(t, result, "Plan: docs/plans/test.md")
		assert.Contains(t, result, "Progress: progress.txt")
		assert.Contains(t, result, "Goal: implementation of plan at docs/plans/test.md")
		assert.Contains(t, result, "Branch: develop")
		assert.Contains(t, result, "Diff: git diff develop...HEAD")
		assert.NotContains(t, result, "{{")
	})

	t.Run("expands agent references", func(t *testing.T) {
		appCfg := &config.Config{
			CustomAgents: []config.CustomAgent{{Name: "test-agent", Prompt: "test prompt"}},
		}
		r := &Runner{cfg: Config{DefaultBranch: "main", AppConfig: appCfg}, log: newMockLogger("")}
		result := r.replaceVariablesWithIteration("Diff: {{DIFF_INSTRUCTION}}, Agent: {{agent:test-agent}}", true)

		assert.Contains(t, result, "Diff: git diff main...HEAD")
		assert.Contains(t, result, "test prompt")
		assert.NotContains(t, result, "{{agent:test-agent}}")
	})

	t.Run("handles prompt without DIFF_INSTRUCTION", func(t *testing.T) {
		r := &Runner{cfg: Config{DefaultBranch: "main"}}
		result := r.replaceVariablesWithIteration("Plan: {{PLAN_FILE}}", true)
		assert.Contains(t, result, "(no plan file - reviewing current branch)")
	})
}

func TestRunner_buildCustomReviewPrompt(t *testing.T) {
	t.Run("first iteration uses branch diff", func(t *testing.T) {
		appCfg := testAppConfig(t)
		r := &Runner{cfg: Config{
			PlanFile:      "docs/plans/test.md",
			DefaultBranch: "main",
			AppConfig:     appCfg,
		}, log: newMockLogger("")}

		prompt := r.buildCustomReviewPrompt(true, "")

		assert.Contains(t, prompt, "git diff main...HEAD")
		assert.Contains(t, prompt, "docs/plans/test.md")
		assert.NotContains(t, prompt, "{{DIFF_INSTRUCTION}}")
		assert.NotContains(t, prompt, "{{PLAN_FILE}}")
	})

	t.Run("subsequent iteration uses uncommitted diff", func(t *testing.T) {
		appCfg := testAppConfig(t)
		r := &Runner{cfg: Config{
			DefaultBranch: "main",
			AppConfig:     appCfg,
		}, log: newMockLogger("")}

		prompt := r.buildCustomReviewPrompt(false, "")

		assert.Contains(t, prompt, "git diff")
		assert.NotContains(t, prompt, "main...HEAD")
	})

	t.Run("appends claude response context when present", func(t *testing.T) {
		appCfg := testAppConfig(t)
		r := &Runner{cfg: Config{
			DefaultBranch: "main",
			AppConfig:     appCfg,
		}, log: newMockLogger("")}

		prompt := r.buildCustomReviewPrompt(false, "I fixed the null pointer issue")

		assert.Contains(t, prompt, "PREVIOUS REVIEW CONTEXT")
		assert.Contains(t, prompt, "I fixed the null pointer issue")
		assert.Contains(t, prompt, "Re-evaluate considering Claude's arguments")
	})

	t.Run("custom prompt template", func(t *testing.T) {
		appCfg := &config.Config{
			CustomReviewPrompt: "Review {{GOAL}} using {{DIFF_INSTRUCTION}}. Branch: {{DEFAULT_BRANCH}}",
		}
		r := &Runner{cfg: Config{
			PlanFile:      "docs/plans/feature.md",
			DefaultBranch: "develop",
			AppConfig:     appCfg,
		}, log: newMockLogger("")}

		prompt := r.buildCustomReviewPrompt(true, "")

		assert.Contains(t, prompt, "implementation of plan at docs/plans/feature.md")
		assert.Contains(t, prompt, "git diff develop...HEAD")
		assert.Contains(t, prompt, "Branch: develop")
	})
}

func TestRunner_buildCustomEvaluationPrompt(t *testing.T) {
	t.Run("replaces CUSTOM_OUTPUT variable", func(t *testing.T) {
		appCfg := testAppConfig(t)
		r := &Runner{cfg: Config{
			PlanFile:      "docs/plans/test.md",
			DefaultBranch: "main",
			AppConfig:     appCfg,
		}, log: newMockLogger("")}

		customOutput := "Found issue in foo.go:10 - potential null pointer"
		prompt := r.buildCustomEvaluationPrompt(customOutput)

		assert.Contains(t, prompt, customOutput)
		assert.NotContains(t, prompt, "{{CUSTOM_OUTPUT}}")
	})

	t.Run("replaces base variables", func(t *testing.T) {
		appCfg := testAppConfig(t)
		r := &Runner{cfg: Config{
			PlanFile:      "docs/plans/feature.md",
			DefaultBranch: "main",
			AppConfig:     appCfg,
		}, log: newMockLogger("")}

		prompt := r.buildCustomEvaluationPrompt("test output")

		assert.Contains(t, prompt, "docs/plans/feature.md")
		assert.NotContains(t, prompt, "{{PLAN_FILE}}")
	})

	t.Run("custom prompt template", func(t *testing.T) {
		appCfg := &config.Config{
			CustomEvalPrompt: "Evaluate output: {{CUSTOM_OUTPUT}}. Goal: {{GOAL}}",
		}
		r := &Runner{cfg: Config{
			PlanFile:      "docs/plans/test.md",
			DefaultBranch: "main",
			AppConfig:     appCfg,
		}, log: newMockLogger("")}

		prompt := r.buildCustomEvaluationPrompt("security issue found")

		assert.Equal(t, "Evaluate output: security issue found. Goal: implementation of plan at docs/plans/test.md", prompt)
	})
}
