package processor

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/umputun/ralphex/pkg/config"
)

// agentRefPattern matches {{agent:name}} template syntax
var agentRefPattern = regexp.MustCompile(`\{\{agent:([a-zA-Z0-9_-]+)\}\}`)

// getGoal returns the goal string based on whether a plan file is configured.
func (r *Runner) getGoal() string {
	if r.cfg.PlanFile == "" {
		return "current branch vs " + r.getDefaultBranch()
	}
	return "implementation of plan at " + r.resolvePlanFilePath()
}

// getPlanFileRef returns plan file reference or fallback text for prompts.
func (r *Runner) getPlanFileRef() string {
	if r.cfg.PlanFile == "" {
		return "(no plan file - reviewing current branch)"
	}
	return r.resolvePlanFilePath()
}

// resolvePlanFilePath returns the actual path to the plan file, checking if it was moved to completed/.
// returns original path if file exists there, completed/ path if moved, or original path as fallback.
func (r *Runner) resolvePlanFilePath() string {
	if r.cfg.PlanFile == "" {
		return ""
	}

	// check if file exists at original location
	_, err := os.Stat(r.cfg.PlanFile)
	if err == nil {
		return r.cfg.PlanFile
	}
	if !os.IsNotExist(err) {
		// permission or other error - return original path
		return r.cfg.PlanFile
	}

	// check if file was moved to completed/ subdirectory
	completedPath := filepath.Join(filepath.Dir(r.cfg.PlanFile), "completed", filepath.Base(r.cfg.PlanFile))
	if _, err := os.Stat(completedPath); err == nil {
		return completedPath
	}

	// fall back to original path
	return r.cfg.PlanFile
}

// getProgressFileRef returns progress file reference or fallback text for prompts.
func (r *Runner) getProgressFileRef() string {
	if r.cfg.ProgressPath == "" {
		return "(no progress file available)"
	}
	return r.cfg.ProgressPath
}

// replaceBaseVariables replaces common template variables in prompts.
// supported: {{PLAN_FILE}}, {{PROGRESS_FILE}}, {{GOAL}}, {{DEFAULT_BRANCH}}
// this is the core replacement function used by all prompt builders.
func (r *Runner) replaceBaseVariables(prompt string) string {
	result := prompt
	result = strings.ReplaceAll(result, "{{PLAN_FILE}}", r.getPlanFileRef())
	result = strings.ReplaceAll(result, "{{PROGRESS_FILE}}", r.getProgressFileRef())
	result = strings.ReplaceAll(result, "{{GOAL}}", r.getGoal())
	result = strings.ReplaceAll(result, "{{DEFAULT_BRANCH}}", r.getDefaultBranch())
	return result
}

// getDiffInstruction returns the appropriate git diff command based on iteration.
// first iteration: compares default branch to HEAD (all changes in feature branch)
// subsequent iterations: shows uncommitted changes only (fixes from previous iteration)
func (r *Runner) getDiffInstruction(isFirstIteration bool) string {
	if isFirstIteration {
		return fmt.Sprintf("git diff %s...HEAD", r.getDefaultBranch())
	}
	return "git diff"
}

// replaceVariablesWithIteration replaces all template variables including iteration-aware ones.
// supported: {{PLAN_FILE}}, {{PROGRESS_FILE}}, {{GOAL}}, {{DEFAULT_BRANCH}}, {{DIFF_INSTRUCTION}}, {{agent:name}}
// this variant is used when iteration context is needed (e.g., custom review prompts).
func (r *Runner) replaceVariablesWithIteration(prompt string, isFirstIteration bool) string {
	result := r.replaceBaseVariables(prompt)
	result = strings.ReplaceAll(result, "{{DIFF_INSTRUCTION}}", r.getDiffInstruction(isFirstIteration))
	result = r.expandAgentReferences(result)
	return result
}

// formatAgentExpansion creates the Task tool instruction for an agent, respecting frontmatter overrides.
func (r *Runner) formatAgentExpansion(prompt string, opts config.Options) string {
	subagent := "general-purpose"
	if opts.AgentType != "" {
		subagent = opts.AgentType
	}

	var modelClause string
	if opts.Model != "" {
		modelClause = " with model=" + opts.Model
	}

	return fmt.Sprintf(`Use the Task tool%s to launch a %s agent with this prompt:
"%s"

Report findings only - no positive observations.`, modelClause, subagent, prompt)
}

// expandAgentReferences replaces {{agent:name}} patterns with Task tool instructions.
// returns prompt unchanged if AppConfig is nil or no agents are configured.
// missing agents log a warning and leave the reference as-is for visibility.
func (r *Runner) expandAgentReferences(prompt string) string {
	if r.cfg.AppConfig == nil {
		return prompt
	}
	agents := r.cfg.AppConfig.CustomAgents
	if len(agents) == 0 {
		return prompt
	}

	// build agent lookup map
	agentMap := make(map[string]config.CustomAgent, len(agents))
	for _, agent := range agents {
		agentMap[agent.Name] = agent
	}

	return agentRefPattern.ReplaceAllStringFunc(prompt, func(match string) string {
		// extract name directly from match: {{agent:NAME}} -> NAME
		name := match[8 : len(match)-2] // skip "{{agent:" and "}}"

		agent, ok := agentMap[name]
		if !ok {
			r.log.Print("[WARN] agent %q not found, leaving reference unexpanded", name)
			return match
		}

		r.log.Print("agent %q: %s", name, agent.Options)

		// expand variables in agent content (no agent expansion to avoid recursion)
		agentPrompt := r.replaceBaseVariables(agent.Prompt)

		return r.formatAgentExpansion(agentPrompt, agent.Options)
	})
}

// replacePromptVariables replaces all template variables including agent references.
// supported: {{PLAN_FILE}}, {{PROGRESS_FILE}}, {{GOAL}}, {{DEFAULT_BRANCH}}, {{agent:name}}
// note: {{CODEX_OUTPUT}} and {{PLAN_DESCRIPTION}} are handled by specific build functions.
func (r *Runner) replacePromptVariables(prompt string) string {
	result := r.replaceBaseVariables(prompt)
	result = r.expandAgentReferences(result)
	return result
}

// getDefaultBranch returns the default branch name or "master" as fallback.
func (r *Runner) getDefaultBranch() string {
	if r.cfg.DefaultBranch == "" {
		return "master"
	}
	return r.cfg.DefaultBranch
}

// buildCodexEvaluationPrompt creates the prompt for claude to evaluate codex review output.
// uses the codex prompt loaded from config (either user-provided or embedded default).
// agent references ({{agent:name}}) are expanded via replacePromptVariables.
func (r *Runner) buildCodexEvaluationPrompt(codexOutput string) string {
	prompt := r.replacePromptVariables(r.cfg.AppConfig.CodexPrompt)
	return strings.ReplaceAll(prompt, "{{CODEX_OUTPUT}}", codexOutput)
}

// buildPlanPrompt creates the prompt for interactive plan creation.
// uses the make_plan prompt loaded from config (either user-provided or embedded default).
// replaces {{PLAN_DESCRIPTION}} plus all base variables.
func (r *Runner) buildPlanPrompt() string {
	prompt := r.cfg.AppConfig.MakePlanPrompt
	prompt = strings.ReplaceAll(prompt, "{{PLAN_DESCRIPTION}}", r.cfg.PlanDescription)
	return r.replaceBaseVariables(prompt)
}

// buildCustomReviewPrompt creates the prompt for custom review tool execution.
// uses the custom_review prompt loaded from config with {{DIFF_INSTRUCTION}} expanded.
// claudeResponse from previous iteration is appended if present.
func (r *Runner) buildCustomReviewPrompt(isFirst bool, claudeResponse string) string {
	prompt := r.replaceVariablesWithIteration(r.cfg.AppConfig.CustomReviewPrompt, isFirst)

	if claudeResponse != "" {
		prompt = fmt.Sprintf(`%s

---
PREVIOUS REVIEW CONTEXT:
Claude (previous reviewer) responded to your findings:

%s

Re-evaluate considering Claude's arguments. If Claude's fixes are correct, acknowledge them.
If Claude's arguments are invalid, explain why the issues still exist.`, prompt, claudeResponse)
	}

	return prompt
}

// buildCustomEvaluationPrompt creates the prompt for claude to evaluate custom review tool output.
// uses the custom_eval prompt loaded from config (either user-provided or embedded default).
// agent references ({{agent:name}}) are expanded via replacePromptVariables.
func (r *Runner) buildCustomEvaluationPrompt(customOutput string) string {
	prompt := r.replacePromptVariables(r.cfg.AppConfig.CustomEvalPrompt)
	return strings.ReplaceAll(prompt, "{{CUSTOM_OUTPUT}}", customOutput)
}
