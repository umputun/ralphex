package processor

import (
	"fmt"
)

// buildTaskPrompt creates the prompt for executing a single task.
func (r *Runner) buildTaskPrompt(progressPath string) string {
	return fmt.Sprintf(`Read the plan file at %s. Find the FIRST Task section (### Task N: or ### Iteration N:) that has uncompleted checkboxes ([ ]).

NOTE: Progress is logged to %s - this file contains detailed execution steps and can be reviewed for debugging.

CRITICAL CONSTRAINT: Complete ONE Task section per iteration.
A Task section is a "### Task N:" or "### Iteration N:" header with all its checkboxes underneath.
Complete ALL checkboxes in that section, then STOP.
Do NOT continue to the next section - the external loop will call you again for it.

PHASE 1 - IMPLEMENT:
- Read the plan's Overview and Context sections to understand the work
- Implement ALL items in the current Task section (all [ ] checkboxes under it)
- Write tests for the implementation

PHASE 2 - VALIDATE:
- Run the test and lint commands specified in the plan (e.g., "cargo test", "go test ./...", etc.)
- Fix any failures, repeat until all validation passes

PHASE 3 - COMPLETE (after validation passes):
- Edit the plan file: change [ ] to [x] for ALL checkboxes in this section
- Commit all changes with message: feat: <brief task description>
- Check if any [ ] checkboxes remain in other sections
- If NO more [ ] checkboxes in the entire plan, output exactly: <<<RALPHEX:ALL_TASKS_DONE>>>
- If more sections have [ ] checkboxes, STOP HERE - do not continue

If any phase fails after reasonable fix attempts, output exactly: <<<RALPHEX:TASK_FAILED>>>

REMINDER: ONE section (Task/Iteration) per loop cycle. After commit, STOP and let the loop handle the next section.`, r.cfg.PlanFile, progressPath)
}

// buildFirstReviewPrompt creates the prompt for first review pass - address all findings.
func (r *Runner) buildFirstReviewPrompt() string {
	goal := "implementation of plan at " + r.cfg.PlanFile
	if r.cfg.PlanFile == "" {
		goal = "current branch vs master"
	}

	return fmt.Sprintf(`Code review of: %s

## Step 1: Get Branch Diff

Run: `+"`git diff master...HEAD`"+`

## Step 2: Launch ALL 8 Review Agents IN PARALLEL

Use the Task tool to launch all 8 agents in parallel (multiple Task calls in one response).

Agents to launch:
1. qa-expert - "Review for bugs, security issues, race conditions"
2. go-test-expert - "Review test coverage and quality"
3. go-smells-expert - "Review for code smells and non-idiomatic patterns"
4. go-simplify-expert - "Review for over-engineering and unnecessary complexity"
5. go-error-auditor - "Review error handling completeness"
6. go-docs-analyzer - "Review code documentation accuracy"
7. implementation-reviewer - "Verify code achieves the goal: %s"
8. documentation-expert - "Check if README.md, CLAUDE.md need updates"

Each agent prompt should include the diff and instruct: "Report problems only - no positive observations."

## Step 3: Collect, Verify, and Fix Findings

After agents complete:

### 3.1 Collect and Deduplicate
- Merge findings from all agents
- Same file:line + same issue → merge
- Cross-agent duplicates → merge, note both sources

### 3.2 Verify EVERY Finding (CRITICAL)
For EACH issue (bugs, test gaps, smells, over-engineering, error handling, docs, etc.):
1. Read actual code at file:line
2. Check full context (20-30 lines around)
3. Verify issue is real, not a false positive
4. Check for existing mitigations

Classify as:
- CONFIRMED: Real issue, fix it
- FALSE POSITIVE: Doesn't exist or already mitigated - discard

IMPORTANT: Pre-existing issues (linter errors, failed tests) should also be fixed.
Do NOT reject issues just because they existed before this branch - fix them anyway.

### 3.3 Fix All Confirmed Issues
1. Fix all CONFIRMED issues (all types: bugs, tests, smells, docs, etc.)
2. Run tests and linter to verify fixes - ALL tests must pass, ALL linter issues resolved
3. Commit fixes: `+"`git commit -m \"fix: address code review findings\"`"+`

## Step 4: Signal Completion

After fixing all confirmed issues (or if none found), output exactly: <<<RALPHEX:REVIEW_DONE>>>

Then STOP. Do not continue. The external loop will run additional review passes to verify.

If unable to fix issues after reasonable attempts: <<<RALPHEX:TASK_FAILED>>>`, goal, goal)
}

// buildSecondReviewPrompt creates the prompt for second review pass - critical/major only.
func (r *Runner) buildSecondReviewPrompt() string {
	goal := "implementation of plan at " + r.cfg.PlanFile
	if r.cfg.PlanFile == "" {
		goal = "current branch vs master"
	}

	return fmt.Sprintf(`Second code review pass of: %s

## Step 1: Get Branch Diff

Run: `+"`git diff master...HEAD`"+`

## Step 2: Launch Review Agents IN PARALLEL

Use Task tool to launch these 2 agents in parallel:
1. qa-expert - "Review for CRITICAL bugs, security issues only"
2. implementation-reviewer - "Verify code achieves the goal: %s"

Focus only on critical and major issues. Ignore style/minor issues.

## Step 3: Verify and Evaluate Findings

### 3.1 Verify Each Finding
For each issue reported:
1. Read actual code at file:line
2. Verify issue is real (not false positive)
3. Check if it's truly critical/major severity

### 3.2 Act on Verified Findings

IMPORTANT: Pre-existing issues (linter errors, failed tests) should also be fixed.
Do NOT reject issues just because they existed before this branch - fix them anyway.

**If NO verified critical or major findings**: Output <<<RALPHEX:REVIEW_DONE>>>

**If findings exist**:
1. Fix verified critical/major issues only
2. Run tests and linter - ALL tests must pass, ALL linter issues resolved
3. Commit fixes: `+"`git commit -m \"fix: address code review findings\"`"+`
4. Do NOT output the signal - the external loop will run another review iteration

If unable to fix: <<<RALPHEX:TASK_FAILED>>>`, goal, goal)
}

// buildCodexEvaluationPrompt creates the prompt for claude to evaluate codex review output.
func (r *Runner) buildCodexEvaluationPrompt(codexOutput string) string {
	return fmt.Sprintf(`External code review evaluation.

Codex (GPT-5.2) reviewed the code and found:

---
%s
---

## Your Task

Analyze each finding critically:

1. **Valid issues**: Fix them (edit files, run tests/linter to verify)
2. **Invalid/irrelevant issues**: Explain why they don't apply - your explanation will be passed to Codex for re-evaluation

IMPORTANT: Pre-existing issues (linter errors, failed tests) should also be fixed.
Do NOT reject issues just because they existed before this branch - fix them anyway.

## After Evaluation

**If there were actionable issues to fix:**
- Fix them, run tests/linter to verify - ALL tests must pass, ALL linter issues resolved
- Do NOT commit yet - more codex iterations may follow
- STOP and let the external loop run codex again

**If Codex reports NO actionable issues** (empty output, "no issues found", "NO ISSUES FOUND"):
- Run `+"`git diff`"+` to review ALL uncommitted changes (accumulated fixes from multiple iterations)
- Commit all fixes with message: "fix: address codex review findings"
- Output exactly: <<<RALPHEX:CODEX_REVIEW_DONE>>>

CRITICAL: Never run codex commands yourself. The external loop handles codex execution.`, codexOutput)
}

// buildContinuePrompt creates a prompt to continue after previous iteration.
func buildContinuePrompt(previousOutput string) string {
	output := previousOutput
	if len(output) > 500 {
		output = output[len(output)-500:]
	}

	return fmt.Sprintf(`Continue from where you left off.

## Previous Output (last 500 chars)

%s

Continue executing tasks. Remember to output <<<RALPHEX:ALL_TASKS_DONE>>> when done or <<<RALPHEX:TASK_FAILED>>> if blocked.`, output)
}
