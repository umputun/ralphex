package processor

import (
	"fmt"
	"regexp"
	"strings"
)

// agentRefPattern matches {{agent:name}} template syntax
var agentRefPattern = regexp.MustCompile(`\{\{agent:([a-zA-Z0-9_-]+)\}\}`)

// agentExpansionTemplate is the wrapper for expanded agent references
const agentExpansionTemplate = `Use the Task tool to launch a general-purpose agent with this prompt:
"%s"

Report findings only - no positive observations.`

// getGoal returns the goal string based on whether a plan file is configured.
func (r *Runner) getGoal() string {
	if r.cfg.PlanFile == "" {
		return "current branch vs master"
	}
	return "implementation of plan at " + r.cfg.PlanFile
}

// getPlanFileRef returns plan file reference or fallback text for prompts.
func (r *Runner) getPlanFileRef() string {
	if r.cfg.PlanFile == "" {
		return "(no plan file - reviewing current branch)"
	}
	return r.cfg.PlanFile
}

// getProgressFileRef returns progress file reference or fallback text for prompts.
func (r *Runner) getProgressFileRef() string {
	if r.cfg.ProgressPath == "" {
		return "(no progress file available)"
	}
	return r.cfg.ProgressPath
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
	agentMap := make(map[string]string, len(agents))
	for _, agent := range agents {
		agentMap[agent.Name] = agent.Prompt
	}

	return agentRefPattern.ReplaceAllStringFunc(prompt, func(match string) string {
		// extract name directly from match: {{agent:NAME}} -> NAME
		name := match[8 : len(match)-2] // skip "{{agent:" and "}}"

		agentPrompt, ok := agentMap[name]
		if !ok {
			r.log.Print("[WARN] agent %q not found, leaving reference unexpanded", name)
			return match
		}

		return fmt.Sprintf(agentExpansionTemplate, agentPrompt)
	})
}

// replacePromptVariables replaces template variables in custom prompts.
// supported variables: {{PLAN_FILE}}, {{PROGRESS_FILE}}, {{GOAL}}, {{agent:name}}
// note: {{CODEX_OUTPUT}} is handled separately in buildCodexEvaluationPrompt
func (r *Runner) replacePromptVariables(prompt string) string {
	result := prompt
	result = strings.ReplaceAll(result, "{{PLAN_FILE}}", r.getPlanFileRef())
	result = strings.ReplaceAll(result, "{{PROGRESS_FILE}}", r.getProgressFileRef())
	result = strings.ReplaceAll(result, "{{GOAL}}", r.getGoal())

	// expand agent references
	result = r.expandAgentReferences(result)

	return result
}

// buildTaskPrompt creates the prompt for executing a single task.
// uses the task prompt loaded from config (either user-provided or embedded default).
// agent references ({{agent:name}}) are expanded via replacePromptVariables.
func (r *Runner) buildTaskPrompt() string {
	return r.replacePromptVariables(r.cfg.AppConfig.TaskPrompt)
}

// buildFirstReviewPrompt creates the prompt for first review pass - address all findings.
// uses the loaded prompt template (user-provided or embedded default).
// agent references ({{agent:name}}) are expanded via replacePromptVariables.
func (r *Runner) buildFirstReviewPrompt() string {
	return r.replacePromptVariables(r.cfg.AppConfig.ReviewFirstPrompt)
}

// buildSecondReviewPrompt creates the prompt for second review pass - critical/major only.
// uses the second review prompt loaded from config (either user-provided or embedded default).
// agent references ({{agent:name}}) are expanded via replacePromptVariables.
func (r *Runner) buildSecondReviewPrompt() string {
	return r.replacePromptVariables(r.cfg.AppConfig.ReviewSecondPrompt)
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
// replaces {{PLAN_DESCRIPTION}} and {{PROGRESS_FILE}} variables.
func (r *Runner) buildPlanPrompt() string {
	prompt := r.cfg.AppConfig.MakePlanPrompt
	prompt = strings.ReplaceAll(prompt, "{{PLAN_DESCRIPTION}}", r.cfg.PlanDescription)
	prompt = strings.ReplaceAll(prompt, "{{PROGRESS_FILE}}", r.getProgressFileRef())
	return prompt
}
