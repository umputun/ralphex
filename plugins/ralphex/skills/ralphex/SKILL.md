---
name: ralphex
description: Run Ralphex autonomous plan execution with progress monitoring.
---

# ralphex - Autonomous Plan Execution

## Interactive Choice Contract

For every choice below, try Codex's native interactive question tool first. If the tool is unavailable, errors, or does not block for an answer, ask the same question with the same options in chat, end the turn, and wait for the user's reply. Never infer or select a default on the user's behalf.

**SCOPE**: This skill ONLY launches ralphex, monitors progress, and reports status. Do NOT take any other actions.

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

Use Codex's native interactive question tool to confirm the installation method, then guide through it. If all four methods cannot fit in one question, ask sequential questions so every method remains available. **Do not proceed until `which ralphex` succeeds.**

## Step 1: Check for Plan Argument

Check the skill invocation for an optional plan file path:
- if argument provided: validate the file exists with an exact file read, skip plan selection in Step 3
- if no argument: will ask for plan selection in Step 3

## Step 2: Ask Execution Mode

Use Codex's native interactive question tool:
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
- Search for `docs/plans/*.md` with exact filesystem tools (excludes completed/)
- Plan is REQUIRED
- Preserve the Claude workflow's oldest-first result handling: REVERSE the list to get most recent first
- Offer up to 4 most recent plans
- First option (most recent) should have "(Recommended)" suffix
- Because a Codex question can show at most 3 options, use sequential questions when needed: offer the first 2 plans plus "More plans", then offer the remaining plans. Do not drop or rename any plan choice.
- User MUST select one

**If Review or Codex-only mode selected:**
- Search for `docs/plans/**/*.md` with exact filesystem tools (includes completed/ for context)
- Plan is OPTIONAL
- Preserve the Claude workflow's oldest-first result handling: REVERSE the list to get most recent first
- Offer up to 4 most recent plans PLUS "None" at the end
- First plan option (most recent) should have "(Recommended)" suffix
- "None" option description: "Review existing changes without a plan file"
- Because a Codex question can show at most 3 options, use sequential questions when needed: offer the first 2 plans plus "More choices", then offer the remaining plan choices and "None" across further questions as necessary. Do not drop or rename any choice.
- If user selects "None", run without plan file

## Step 4: Ask Max Iterations

Use Codex's native interactive question tool:
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

Run with Codex's native background terminal. **Save the returned session ID** - it is the Codex-native equivalent of Claude's background task ID and is needed for status checks later.

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
ralphex started. Session ID: [session-id]

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

1. Poll the saved Codex background terminal session without blocking (use the session ID from Step 5)
2. Read last 40 lines of progress file (use filename from Step 5)

**If process still running:**
- Report current phase from progress file:
  - "task iteration N" → Task Execution phase
  - "codex iteration N" → Codex External Review phase
  - "review pass 1/2" → Claude Review phase
- Show recent activity lines

**If process exited (the background terminal shows completion):**
- Exit code 0 → success, report "ralphex completed successfully"
- Exit code non-zero → failure, report "ralphex failed"
- Read final lines of progress file for summary

**After reporting status, STOP. Do not offer to do anything else.**

## Constraints

- This skill is ONLY for launching and monitoring ralphex
- Do NOT offer to help with code, commits, PRs, or anything else
- Do NOT make suggestions or recommendations beyond status reporting
- Do NOT take any actions on the codebase
- After launch confirmation: wait for user to explicitly request status check
- After status check: report and stop

## Nested Claude Code Sessions

ralphex automatically strips the `CLAUDECODE` env var from child processes, allowing it to run from inside Codex when the configured workflow launches Claude Code. If the nested session error is somehow encountered, ralphex detects it via error pattern matching and exits gracefully instead of looping.

Running from a standalone terminal is still recommended for the best experience.
