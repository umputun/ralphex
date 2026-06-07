---
name: ralphex
description: Run ralphex autonomous plan execution with progress monitoring
allowed-tools: read bash
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

Ask the user inline which installation method they prefer, then guide through it. **Do not proceed until `which ralphex` succeeds.**

## Step 1: Check for Plan Argument

Arguments arrive appended as user input (pi has no `$ARGUMENTS` placeholder). If the user named a plan file:
- validate it exists with pi's `read` tool (or `bash` `test -f`), then skip plan selection in Step 3
- if no plan was named: ask for plan selection in Step 3

## Step 2: Ask Execution Mode

Ask the user inline which execution mode ralphex should use (pi is interactive — ask directly and wait for the reply). Offer:
- **Full (Recommended)**: Task execution + Claude review + Codex loop + final Claude review
- **Review**: Skip tasks, run full review pipeline (Claude + Codex + Claude)
- **Codex-only**: Skip tasks and first Claude review, run only Codex loop

## Step 3: Plan Selection (if no plan named)

**If Full mode selected:**
- List candidate plans with pi `bash`: `ls -t docs/plans/*.md` (excludes completed/; `-t` sorts newest first)
- Plan is REQUIRED
- Ask the user inline to pick one of the up-to-4 most recent plans
- Present the most recent first with a "(Recommended)" note
- User MUST select one

**If Review or Codex-only mode selected:**
- List candidate plans with pi `bash`: `ls -t docs/plans/**/*.md docs/plans/*.md 2>/dev/null` (includes completed/ for context, newest first)
- Plan is OPTIONAL
- Ask the user inline to pick one of the up-to-4 most recent plans, or "None"
- Present the most recent first with a "(Recommended)" note
- "None" means: review existing changes without a plan file
- If user selects "None", run without a plan file

## Step 4: Ask Max Iterations

Ask the user inline for the maximum number of task iterations:
- **50 (Recommended)**: Default - suitable for most plans
- **25**: Shorter plans or quick iterations
- **100**: Large plans with many tasks

## Step 5: Launch ralphex in Background

Build the command:

```bash
ralphex \
  [--review]              # if user selected "Review" mode
  [--codex-only]          # if user selected "Codex-only" mode
  [--max-iterations N]    # from user selection (25, 50, or 100)
  [plan-file]             # from argument OR plan selection (omit if "None" selected)
```

Launch it detached so it survives this session, using pi's `bash` tool. Redirect output and record the PID for later status checks:

```bash
nohup ralphex [flags] [plan-file] >/dev/null 2>&1 &
echo "ralphex PID: $!"
```

**Save the printed PID** — needed for status checks later.

**Determine progress filename** based on mode and plan selection:
- Full mode + plan: `.ralphex/progress/progress-{plan-stem}.txt`
- Review mode + plan: `.ralphex/progress/progress-{plan-stem}-review.txt`
- Codex-only + plan: `.ralphex/progress/progress-{plan-stem}-codex.txt`
- Full mode + no plan: `.ralphex/progress/progress.txt`
- Review mode + no plan: `.ralphex/progress/progress-review.txt`
- Codex-only + no plan: `.ralphex/progress/progress-codex.txt`

Where `{plan-stem}` is the plan filename without extension (e.g., `fix-bugs` from `fix-bugs.md`).

## Step 6: Confirm Launch

1. Wait 10-15 seconds for initialization (e.g., pi `bash`: `sleep 12`)
2. Read last 20 lines of progress file: `tail -20 [progress-filename]`
3. Confirm ralphex started by checking for "Plan:", "Branch:", "Started:" lines
4. Report launch confirmation:

```
ralphex started. PID: [pid]

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

1. Check whether the process is still alive with pi `bash`: `kill -0 [pid] 2>/dev/null && echo running || echo exited` (use the PID from Step 5)
2. Read last 40 lines of progress file (use filename from Step 5)

**If process still running:**
- Report current phase from progress file:
  - "task iteration N" → Task Execution phase
  - "codex iteration N" → Codex External Review phase
  - "review pass 1/2" → Claude Review phase
- Show recent activity lines

**If process exited:**
- A `Completed:` footer in the progress file → success, report "ralphex completed successfully"
- A `Failed:` footer (or abnormal last lines) → failure, report "ralphex failed"
- Read final lines of progress file for summary

**After reporting status, STOP. Do not offer to do anything else.**

## Constraints

- This command is ONLY for launching and monitoring ralphex
- Do NOT offer to help with code, commits, PRs, or anything else
- Do NOT make suggestions or recommendations beyond status reporting
- Do NOT take any actions on the codebase
- After launch confirmation: wait for user to explicitly request status check
- After status check: report and stop

## Nested Agent Sessions

ralphex automatically strips the `CLAUDECODE` env var from child processes, so it runs cleanly even when launched from inside another agent session. If a nested-session error is somehow encountered, ralphex detects it via error pattern matching and exits gracefully instead of looping.

Running from a standalone terminal is still recommended for the best experience.
