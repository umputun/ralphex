---
description: Run ralphex autonomous plan execution with progress monitoring
argument-hint: 'optional plan file path'
allowed-tools: [Bash, Read, AskUserQuestion, TaskOutput, Glob]
---

# ralphex - Autonomous Plan Execution

**SCOPE**: This command ONLY launches ralphex, monitors progress, and reports status. Do NOT take any other actions.

## Step 0: Verify CLI Installation

Check if ralphex CLI is installed:
```bash
which ralphex
```

**If not found**, guide installation based on platform:

- **macOS (Homebrew)**: `brew install umputun/apps/ralphex`
- **Linux (Debian/Ubuntu)**: download `.deb` from https://github.com/umputun/ralphex/releases
- **Linux (RHEL/Fedora)**: download `.rpm` from https://github.com/umputun/ralphex/releases
- **Any platform with Go**: `go install github.com/umputun/ralphex/cmd/ralphex@latest`

Use AskUserQuestion to confirm installation method, then guide through it. **Do not proceed until `which ralphex` succeeds.**

## Step 1: Check for Plan Argument

Check `$ARGUMENTS` for optional plan file path:
- if argument provided: validate file exists using Read tool, skip plan selection in Step 3
- if no argument: will ask for plan selection in Step 3

## Step 2: Ask Execution Mode

Use AskUserQuestion:
- header: "Mode"
- question: "Which execution mode should ralphex use?"
- options:
  - label: "Full (Recommended)"
    description: "Task execution + Claude review + Codex loop + final Claude review"
  - label: "Review"
    description: "Skip tasks, run full review pipeline (Claude + Codex + Claude)"
  - label: "Codex-only"
    description: "Skip tasks and first Claude review, run only Codex loop"

## Step 3: Plan Selection (if no argument provided)

**If Full mode selected:**
- Use Glob: `docs/plans/*.md` (excludes completed/)
- Plan is REQUIRED
- **IMPORTANT**: Glob returns oldest-first, so REVERSE the list to get most recent first
- Build AskUserQuestion with up to 4 most recent plans
- First option (most recent) should have "(Recommended)" suffix
- User MUST select one

**If Review or Codex-only mode selected:**
- Use Glob: `docs/plans/**/*.md` (includes completed/ for context)
- Plan is OPTIONAL
- **IMPORTANT**: Glob returns oldest-first, so REVERSE the list to get most recent first
- Build AskUserQuestion with up to 4 most recent plans PLUS "None" option at the end
- First plan option (most recent) should have "(Recommended)" suffix
- "None" option description: "Review existing changes without a plan file"
- If user selects "None", run without plan file

## Step 4: Ask Max Iterations

Use AskUserQuestion:
- header: "Iterations"
- question: "Maximum number of task iterations?"
- options:
  - label: "50 (Recommended)"
    description: "Default - suitable for most plans"
  - label: "25"
    description: "Shorter plans or quick iterations"
  - label: "100"
    description: "Large plans with many tasks"

## Step 5: Launch ralphex in Background

Build and run the command:

```bash
ralphex \
  [--review]              # if user selected "Review" mode
  [--codex-only]          # if user selected "Codex-only" mode
  [--max-iterations N]    # from user selection (25, 50, or 100)
  [plan-file]             # from argument OR plan selection (omit if "None" selected)
```

Run using Bash tool with `run_in_background: true`. **Save the task_id from the response** - needed for status checks later.

**Determine progress filename** based on mode and plan selection:
- Full mode + plan: `.ralphex/progress/progress-{plan-stem}.txt`
- Review mode + plan: `.ralphex/progress/progress-{plan-stem}-review.txt`
- Codex-only + plan: `.ralphex/progress/progress-{plan-stem}-codex.txt`
- Full mode + no plan: `.ralphex/progress/progress.txt`
- Review mode + no plan: `.ralphex/progress/progress-review.txt`
- Codex-only + no plan: `.ralphex/progress/progress-codex.txt`

Where `{plan-stem}` is the plan filename without extension (e.g., `fix-bugs` from `fix-bugs.md`).

## Step 6: Confirm Launch

1. Wait 10-15 seconds for initialization
2. Read last 20 lines of progress file: `tail -20 [progress-filename]`
3. Confirm ralphex started by checking for "Plan:", "Branch:", "Started:" lines
4. Report launch confirmation:

```
ralphex started. Task ID: [task_id]

Plan: [plan file from progress file]
Branch: [branch from progress file]
Mode: [mode from progress file]
Progress file: [progress-filename]

Manual monitoring:
  tail -f [progress-filename]      # live stream
  tail -50 [progress-filename]     # recent activity

ralphex runs autonomously (can take hours). Process continues if you close this conversation.
Ask "check ralphex" to get status update.
```

**STOP HERE after reporting launch status. Do not continue monitoring automatically.**

## Step 7: Progress Check (only on explicit user request)

If user explicitly asks "check ralphex", "ralphex status", or "how is ralphex doing":

1. Use TaskOutput tool with `block: false` to check process status (use task_id from Step 5)
2. Read last 40 lines of progress file (use filename from Step 5)

**If process still running:**
- Report current phase from progress file:
  - "task iteration N" → Task Execution phase
  - "codex iteration N" → Codex External Review phase
  - "review pass 1/2" → Claude Review phase
- Show recent activity lines

**If process exited (TaskOutput shows completion):**
- Exit code 0 → success, report "ralphex completed successfully"
- Exit code non-zero → failure, report "ralphex failed"
- Read final lines of progress file for summary

**After reporting status, STOP. Do not offer to do anything else.**

## Constraints

- This command is ONLY for launching and monitoring ralphex
- Do NOT offer to help with code, commits, PRs, or anything else
- Do NOT make suggestions or recommendations beyond status reporting
- Do NOT take any actions on the codebase
- After launch confirmation: wait for user to explicitly request status check
- After status check: report and stop
