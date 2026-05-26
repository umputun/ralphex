package processor

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/umputun/ralphex/pkg/config"
	"github.com/umputun/ralphex/pkg/executor"
)

// agentRefPattern matches {{agent:name}} template syntax
var agentRefPattern = regexp.MustCompile(`\{\{agent:([a-zA-Z0-9_-]+)\}\}`)

// getGoal returns the goal string based on whether a plan file is configured.
func (b *promptBuilder) getGoal() string {
	if b.cfg.PlanFile == "" {
		return "current branch vs " + b.getDefaultBranch()
	}
	return "implementation of plan at " + b.locator.Path()
}

// getPlanFileRef returns plan file reference or fallback text for prompts.
func (b *promptBuilder) getPlanFileRef() string {
	if b.cfg.PlanFile == "" {
		return "(no plan file - reviewing current branch)"
	}
	return b.locator.Path()
}

// getProgressFileRef returns progress file reference or fallback text for prompts.
func (b *promptBuilder) getProgressFileRef() string {
	if b.cfg.ProgressPath == "" {
		return "(no progress file available)"
	}
	return b.cfg.ProgressPath
}

// replaceBaseVariables replaces common template variables in prompts.
// supported: {{PLAN_FILE}}, {{PROGRESS_FILE}}, {{GOAL}}, {{DEFAULT_BRANCH}}, {{PLANS_DIR}}
// this is the core replacement function used by all prompt builders.
// replaces common template variables shared across all prompt types.
// does not append trailer instruction — callers are responsible for calling appendCommitTrailerInstruction
// once on the final assembled prompt, to avoid duplication when expanding agent references.
func (b *promptBuilder) replaceBaseVariables(prompt string) string {
	result := prompt
	result = strings.ReplaceAll(result, "{{PLAN_FILE}}", b.getPlanFileRef())
	result = strings.ReplaceAll(result, "{{PROGRESS_FILE}}", b.getProgressFileRef())
	result = strings.ReplaceAll(result, "{{GOAL}}", b.getGoal())
	result = strings.ReplaceAll(result, "{{DEFAULT_BRANCH}}", b.getDefaultBranch())
	result = strings.ReplaceAll(result, "{{PLANS_DIR}}", b.getPlansDir())
	return result
}

// appendCommitTrailerInstruction appends trailer instruction to prompt when commit_trailer is configured.
// returns prompt unchanged when commit_trailer is empty or AppConfig is nil.
func (b *promptBuilder) appendCommitTrailerInstruction(prompt string) string {
	if b.cfg.AppConfig == nil || b.cfg.AppConfig.CommitTrailer == "" {
		return prompt
	}
	return prompt + "\n\nWhen making git commits, add the following trailer" +
		" after a blank line at the end of the commit message:\n" +
		b.cfg.AppConfig.CommitTrailer
}

// getDiffInstruction returns the appropriate git diff command based on iteration.
// first iteration: compares default branch to HEAD (all changes in feature branch)
// subsequent iterations: shows uncommitted changes only (fixes from previous iteration)
func (b *promptBuilder) getDiffInstruction(isFirstIteration bool) string {
	if isFirstIteration {
		return fmt.Sprintf("git diff %s...HEAD", b.getDefaultBranch())
	}
	return "git diff"
}

// buildPreviousContext returns the PREVIOUS REVIEW CONTEXT block for external review prompts.
// returns empty string on first iteration (no prior response), formatted context block on subsequent iterations.
func (b *promptBuilder) buildPreviousContext(claudeResponse string) string {
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
func (b *promptBuilder) replaceVariablesWithIteration(prompt string, isFirstIteration bool, claudeResponse string) string {
	result := b.replaceBaseVariables(prompt)
	result = strings.ReplaceAll(result, "{{DIFF_INSTRUCTION}}", b.getDiffInstruction(isFirstIteration))
	result = b.expandAgentReferences(result) // expand agents before inserting external content
	result = strings.ReplaceAll(result, "{{PREVIOUS_REVIEW_CONTEXT}}", b.buildPreviousContext(claudeResponse))
	return b.appendCommitTrailerInstruction(result)
}

// reviewContextInstruction returns the lead-in prepended to every review agent
// body. agent body files describe WHAT to review; this supplies WHERE — each
// spawned agent runs in a fresh context with no pointer to the branch diff or
// changed files unless told to fetch them itself.
func (b *promptBuilder) reviewContextInstruction() string {
	branch := b.getDefaultBranch()
	return fmt.Sprintf("First run `git diff %s...HEAD` and `git diff --stat %s...HEAD` to get the "+
		"changes, then read the changed source files in full context.\n\n", branch, branch)
}

// formatAgentExpansion creates the agent invocation block for an agent, respecting frontmatter overrides.
// claude executor produces a Task tool instruction; codex executor produces a spawn_agent block.
// the review-context lead-in is prepended so the spawned agent knows which diff to review.
func (b *promptBuilder) formatAgentExpansion(prompt string, opts config.Options) string {
	prompt = b.reviewContextInstruction() + prompt
	if b.cfg.isCodexExecutor() {
		return b.formatAgentExpansionCodex(prompt)
	}
	return b.formatAgentExpansionClaude(prompt, opts)
}

// formatAgentExpansionClaude builds the Task-tool prose used by claude executor.
func (b *promptBuilder) formatAgentExpansionClaude(prompt string, opts config.Options) string {
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
func (b *promptBuilder) formatAgentExpansionCodex(prompt string) string {
	return fmt.Sprintf(`spawn_agent(agent='%s', task='%s')

Report findings only - no positive observations.`, executor.CodexReviewerAgentName, b.escapeCodexSingleQuoted(prompt))
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
func (b *promptBuilder) prependCodexReviewGuidance(prompt string) string {
	if !b.cfg.isCodexExecutor() {
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
func (b *promptBuilder) prependCodexTaskGuidance(prompt string) string {
	if !b.cfg.isCodexExecutor() {
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
func (b *promptBuilder) escapeCodexSingleQuoted(s string) string {
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
func (b *promptBuilder) expandAgentReferences(prompt string) string {
	if b.cfg.AppConfig == nil {
		return prompt
	}
	agents := b.cfg.AppConfig.CustomAgents
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
			b.log.Print("[WARN] agent %q not found, leaving reference unexpanded", name)
			return match
		}

		b.log.Print("agent %q: %s", name, agent.Options)

		// under codex syntax, formatAgentExpansionCodex collapses every {{agent:name}} into
		// the same spawn_agent(agent='reviewer', task=...) call — frontmatter Model/AgentType
		// are intentionally discarded. warn so users do not silently lose per-agent overrides.
		if b.cfg.isCodexExecutor() && (agent.Model != "" || agent.AgentType != "") {
			b.warnCodexFrontmatterDiscarded(name, agent.Options)
		}

		// expand variables in agent content (no agent expansion to avoid recursion)
		agentPrompt := b.replaceBaseVariables(agent.Prompt)

		return b.formatAgentExpansion(agentPrompt, agent.Options)
	})
}

// warnCodexFrontmatterDiscarded logs a one-time-per-agent warning when codex
// mode drops a per-agent Model/AgentType override.
func (b *promptBuilder) warnCodexFrontmatterDiscarded(name string, opts config.Options) {
	if b.codexFrontmatterWarned == nil {
		b.codexFrontmatterWarned = make(map[string]bool)
	}
	if b.codexFrontmatterWarned[name] {
		return
	}
	b.codexFrontmatterWarned[name] = true
	b.log.Print("[WARN] codex mode ignores frontmatter overrides for agent %q (model=%q agent=%q); a single shared codex agent is used", name, opts.Model, opts.AgentType)
}

// replacePromptVariables replaces all template variables including agent references.
// supported: {{PLAN_FILE}}, {{PROGRESS_FILE}}, {{GOAL}}, {{DEFAULT_BRANCH}}, {{PLANS_DIR}}, {{agent:name}}
// note: {{CODEX_OUTPUT}} and {{PLAN_DESCRIPTION}} are handled by specific build functions.
func (b *promptBuilder) replacePromptVariables(prompt string) string {
	result := b.replaceBaseVariables(prompt)
	result = b.expandAgentReferences(result)
	return b.appendCommitTrailerInstruction(result)
}

// getDefaultBranch returns the default branch name or "master" as fallback.
func (b *promptBuilder) getDefaultBranch() string {
	if b.cfg.DefaultBranch == "" {
		return "master"
	}
	return b.cfg.DefaultBranch
}

// getPlansDir returns the plans directory or "docs/plans" as fallback.
func (b *promptBuilder) getPlansDir() string {
	if b.cfg.AppConfig == nil || b.cfg.AppConfig.PlansDir == "" {
		return "docs/plans"
	}
	return b.cfg.AppConfig.PlansDir
}
