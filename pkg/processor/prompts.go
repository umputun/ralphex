package processor

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/umputun/ralphex/pkg/config"
	"github.com/umputun/ralphex/pkg/executor"
	"github.com/umputun/ralphex/pkg/plan"
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

// resolvePlanFilePath returns the actual path to the plan file, checking if it was moved or renamed.
// probes in order: original path, <dir>/<alt-date-basename> (in-place rename),
// completed/<basename> (moved), completed/<alt-date-basename> (moved + renamed).
// the YYYY-MM-DD ↔ YYYYMMDD swap handles LLM-driven date-format renames.
// returns the first path that exists, or the original path as fallback.
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

	dir := filepath.Dir(r.cfg.PlanFile)
	base := filepath.Base(r.cfg.PlanFile)
	altBase := plan.AltDateBasename(base)

	// check if file was renamed in place to the alternate date format (same directory)
	// done before completed/ probes so a current renamed file wins over a stale completed/ copy
	if altBase != "" {
		if _, err := os.Stat(filepath.Join(dir, altBase)); err == nil {
			return filepath.Join(dir, altBase)
		}
	}

	// check if file was moved to completed/ subdirectory
	completedPath := filepath.Join(dir, "completed", base)
	if _, err := os.Stat(completedPath); err == nil {
		return completedPath
	}

	// check if file was moved and renamed to the alternate date format in completed/
	if altBase != "" {
		altCompletedPath := filepath.Join(dir, "completed", altBase)
		if _, err := os.Stat(altCompletedPath); err == nil {
			return altCompletedPath
		}
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
// supported: {{PLAN_FILE}}, {{PROGRESS_FILE}}, {{GOAL}}, {{DEFAULT_BRANCH}}, {{PLANS_DIR}}
// this is the core replacement function used by all prompt builders.
// replaces common template variables shared across all prompt types.
// does not append trailer instruction — callers are responsible for calling appendCommitTrailerInstruction
// once on the final assembled prompt, to avoid duplication when expanding agent references.
func (r *Runner) replaceBaseVariables(prompt string) string {
	result := prompt
	result = strings.ReplaceAll(result, "{{PLAN_FILE}}", r.getPlanFileRef())
	result = strings.ReplaceAll(result, "{{PROGRESS_FILE}}", r.getProgressFileRef())
	result = strings.ReplaceAll(result, "{{GOAL}}", r.getGoal())
	result = strings.ReplaceAll(result, "{{DEFAULT_BRANCH}}", r.getDefaultBranch())
	result = strings.ReplaceAll(result, "{{PLANS_DIR}}", r.getPlansDir())
	return result
}

// appendCommitTrailerInstruction appends trailer instruction to prompt when commit_trailer is configured.
// returns prompt unchanged when commit_trailer is empty or AppConfig is nil.
func (r *Runner) appendCommitTrailerInstruction(prompt string) string {
	if r.cfg.AppConfig == nil || r.cfg.AppConfig.CommitTrailer == "" {
		return prompt
	}
	return prompt + "\n\nWhen making git commits, add the following trailer" +
		" after a blank line at the end of the commit message:\n" +
		r.cfg.AppConfig.CommitTrailer
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

// buildPreviousContext returns the PREVIOUS REVIEW CONTEXT block for external review prompts.
// returns empty string on first iteration (no prior response), formatted context block on subsequent iterations.
func (r *Runner) buildPreviousContext(claudeResponse string) string {
	if claudeResponse == "" {
		return ""
	}
	return fmt.Sprintf(`---
PREVIOUS REVIEW CONTEXT:
Claude (previous reviewer) responded to your findings:

%s

Re-evaluate considering Claude's arguments. If Claude's fixes are correct, acknowledge them.
If Claude's arguments are invalid, explain why the issues still exist.`, claudeResponse)
}

// replaceVariablesWithIteration replaces all template variables including iteration-aware ones.
// supported: {{PLAN_FILE}}, {{PROGRESS_FILE}}, {{GOAL}}, {{DEFAULT_BRANCH}}, {{PLANS_DIR}},
// {{DIFF_INSTRUCTION}}, {{PREVIOUS_REVIEW_CONTEXT}}, {{agent:name}}
// this variant is used when iteration context is needed (e.g., external review prompts).
func (r *Runner) replaceVariablesWithIteration(prompt string, isFirstIteration bool, claudeResponse string) string {
	result := r.replaceBaseVariables(prompt)
	result = strings.ReplaceAll(result, "{{DIFF_INSTRUCTION}}", r.getDiffInstruction(isFirstIteration))
	result = r.expandAgentReferences(result) // expand agents before inserting external content
	result = strings.ReplaceAll(result, "{{PREVIOUS_REVIEW_CONTEXT}}", r.buildPreviousContext(claudeResponse))
	return r.appendCommitTrailerInstruction(result)
}

// reviewContextInstruction returns the lead-in prepended to every review agent
// body. agent body files describe WHAT to review; this supplies WHERE — each
// spawned agent runs in a fresh context with no pointer to the branch diff or
// changed files unless told to fetch them itself.
func (r *Runner) reviewContextInstruction() string {
	branch := r.getDefaultBranch()
	return fmt.Sprintf("First run `git diff %s...HEAD` and `git diff --stat %s...HEAD` to get the "+
		"changes, then read the changed source files in full context.\n\n", branch, branch)
}

// formatAgentExpansion creates the agent invocation block for an agent, respecting frontmatter overrides.
// claude executor produces a Task tool instruction; codex executor produces a spawn_agent block.
// the review-context lead-in is prepended so the spawned agent knows which diff to review.
func (r *Runner) formatAgentExpansion(prompt string, opts config.Options) string {
	prompt = r.reviewContextInstruction() + prompt
	if r.cfg.isCodexExecutor() {
		return r.formatAgentExpansionCodex(prompt)
	}
	return r.formatAgentExpansionClaude(prompt, opts)
}

// formatAgentExpansionClaude builds the Task-tool prose used by claude executor.
func (r *Runner) formatAgentExpansionClaude(prompt string, opts config.Options) string {
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

// formatAgentExpansionCodex builds the spawn_agent block used by codex executor.
// frontmatter Model/AgentType overrides do not apply: codex registers a single
// reviewer agent globally; per-call behavior is driven by the inlined task body.
// the agent body is escaped so apostrophes (don't, what's) and backslashes inside
// it do not terminate the surrounding single-quoted task='...' literal. the agent
// name is shared with pkg/executor.CodexReviewerAgentName so the spawn_agent call
// here stays in sync with the codex -c agents.<name>.description registration.
// fork_context / wait_agent guidance lives once in codexReviewGuidance (prepended
// to review prompts by prependCodexReviewGuidance) rather than being repeated per
// agent block — keeps the spawn_agent example compact and avoids the same
// directives being duplicated 5× per review iteration.
func (r *Runner) formatAgentExpansionCodex(prompt string) string {
	return fmt.Sprintf(`spawn_agent(agent='%s', task='%s')

Report findings only - no positive observations.`, executor.CodexReviewerAgentName, r.escapeCodexSingleQuoted(prompt))
}

// codexReviewGuidance is the section-level directive block prepended to review
// prompts by prependCodexReviewGuidance when the codex executor is active. it
// covers two codex multi_agent quirks that affect EVERY review iteration:
//
//   - spawn_agent: codex auto-tries fork_context=true with an explicit
//     agent_type, which codex's own API rejects ("Full-history forked agents
//     inherit the parent agent type..."). without the directive each iteration
//     wastes ~5 spawn round-trips before codex retries cleanly.
//   - wait_agent: when a spawned sub-agent dies mid-tool-call (observed during
//     parallel exec batches), codex never registers its termination and the
//     parent loops on wait_agent for the full 1-hour outer timeout. with the
//     directive the parent re-spawns once after the first 10-min wait window
//     instead of waiting 40+ min before deciding the agent is gone.
//
// the block lives at ralphex level so users with customized review prompts
// (that hard-code agent lists inline instead of using {{agent:NAME}} expansion)
// still get the directives — the per-agent expander doesn't fire for them.
const codexReviewGuidance = `=== Codex orchestration directives ===

spawn_agent: pass ONLY the agent and task arguments. Do NOT set fork_context.
Codex rejects fork_context=true paired with an explicit agent_type and the
wasted retry round-trips delay every launch.

wait_agent: if the call returns with timed_out=true and one or more requested
agents are missing from the status map, those agents are dead — do not keep
waiting. Re-spawn the missing agents ONCE in a single replacement batch. If
the replacement batch also returns missing agents, proceed with available
results and note the missing agent in your findings. Total budget: at most
one re-spawn per dead agent.

=======================================

`

// prependCodexReviewGuidance returns prompt with codexReviewGuidance prepended
// when the codex executor is active; otherwise returns prompt unchanged. used
// to inject codex multi_agent orchestration directives into review prompts at
// build time so the directives are present regardless of whether the user
// kept the embedded review_first/review_second templates or replaced them with
// hard-coded inline agent lists.
func (r *Runner) prependCodexReviewGuidance(prompt string) string {
	if !r.cfg.isCodexExecutor() {
		return prompt
	}
	return codexReviewGuidance + prompt
}

// codexTaskGuidance is the directive block prepended to the task-execution
// prompt by prependCodexTaskGuidance when the codex executor is active. it
// tells codex that ralphex's task prompt is authoritative so that any
// auto-activating codex skill whose workflow overlaps this prompt cannot
// compete with ralphex's orchestration or flood the progress stream with
// recited skill text. deliberately generic — it names no specific skill,
// since which skills a user has installed varies per machine.
const codexTaskGuidance = `=== Codex task-execution directives ===

The instructions in this prompt are the complete and authoritative
task-execution workflow. One of your skills may auto-activate because this
prompt resembles work it handles; if any such skill defines its own workflow
that overlaps or conflicts with the steps below, do NOT follow that skill —
this prompt takes precedence. Following a competing workflow duplicates or
contradicts these steps. Your built-in tools (file edits, shell commands,
etc.) remain available — use them normally.

=======================================

`

// prependCodexTaskGuidance returns prompt with codexTaskGuidance prepended when
// the codex executor is active; otherwise returns prompt unchanged. injected at
// build time so the directive applies whether the user kept the embedded task
// prompt or replaced it with a customized one.
func (r *Runner) prependCodexTaskGuidance(prompt string) string {
	if !r.cfg.isCodexExecutor() {
		return prompt
	}
	return codexTaskGuidance + prompt
}

// escapeCodexSingleQuoted escapes the characters that would otherwise break a
// Python-style single-quoted string literal — which is what spawn_agent(task='...')
// expects. supported escapes: backslash (must be escaped first so subsequent
// escapes are not double-processed), single quote, tab, carriage return, newline.
// other control characters (\b, \f, \v, unicode escapes) are not escaped because
// the project's default agent bodies do not contain them; if a custom agent embeds
// such characters, extend this set to cover them.
func (r *Runner) escapeCodexSingleQuoted(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	s = strings.ReplaceAll(s, "\t", `\t`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
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

		// under codex syntax, formatAgentExpansionCodex collapses every {{agent:name}} into
		// the same spawn_agent(agent='reviewer', task=...) call — frontmatter Model/AgentType
		// are intentionally discarded. warn so users do not silently lose per-agent overrides.
		if r.cfg.isCodexExecutor() && (agent.Model != "" || agent.AgentType != "") {
			r.warnCodexFrontmatterDiscarded(name, agent.Options)
		}

		// expand variables in agent content (no agent expansion to avoid recursion)
		agentPrompt := r.replaceBaseVariables(agent.Prompt)

		return r.formatAgentExpansion(agentPrompt, agent.Options)
	})
}

// warnCodexFrontmatterDiscarded logs a one-time-per-agent warning when codex
// mode drops a per-agent Model/AgentType override.
func (r *Runner) warnCodexFrontmatterDiscarded(name string, opts config.Options) {
	if r.codexFrontmatterWarned == nil {
		r.codexFrontmatterWarned = make(map[string]bool)
	}
	if r.codexFrontmatterWarned[name] {
		return
	}
	r.codexFrontmatterWarned[name] = true
	r.log.Print("[WARN] codex mode ignores frontmatter overrides for agent %q (model=%q agent=%q); a single shared codex agent is used", name, opts.Model, opts.AgentType)
}

// replacePromptVariables replaces all template variables including agent references.
// supported: {{PLAN_FILE}}, {{PROGRESS_FILE}}, {{GOAL}}, {{DEFAULT_BRANCH}}, {{PLANS_DIR}}, {{agent:name}}
// note: {{CODEX_OUTPUT}} and {{PLAN_DESCRIPTION}} are handled by specific build functions.
func (r *Runner) replacePromptVariables(prompt string) string {
	result := r.replaceBaseVariables(prompt)
	result = r.expandAgentReferences(result)
	return r.appendCommitTrailerInstruction(result)
}

// getDefaultBranch returns the default branch name or "master" as fallback.
func (r *Runner) getDefaultBranch() string {
	if r.cfg.DefaultBranch == "" {
		return "master"
	}
	return r.cfg.DefaultBranch
}

// getPlansDir returns the plans directory or "docs/plans" as fallback.
func (r *Runner) getPlansDir() string {
	if r.cfg.AppConfig == nil || r.cfg.AppConfig.PlansDir == "" {
		return "docs/plans"
	}
	return r.cfg.AppConfig.PlansDir
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
	result := r.replaceBaseVariables(prompt)
	return r.appendCommitTrailerInstruction(result)
}

// buildCustomReviewPrompt creates the prompt for custom review tool execution.
// uses the custom_review prompt loaded from config with all variables expanded,
// including {{PREVIOUS_REVIEW_CONTEXT}} for iteration context.
func (r *Runner) buildCustomReviewPrompt(isFirst bool, claudeResponse string) string {
	return r.replaceVariablesWithIteration(r.cfg.AppConfig.CustomReviewPrompt, isFirst, claudeResponse)
}

// buildCustomEvaluationPrompt creates the prompt for claude to evaluate custom review tool output.
// uses the custom_eval prompt loaded from config (either user-provided or embedded default).
// agent references ({{agent:name}}) are expanded via replacePromptVariables.
func (r *Runner) buildCustomEvaluationPrompt(customOutput string) string {
	prompt := r.replacePromptVariables(r.cfg.AppConfig.CustomEvalPrompt)
	return strings.ReplaceAll(prompt, "{{CUSTOM_OUTPUT}}", customOutput)
}
