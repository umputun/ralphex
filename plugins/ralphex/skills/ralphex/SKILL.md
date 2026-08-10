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
- if argument provided: validate the file exists with an exact file read, skip plan selection in Step 4
- if no argument: will ask for plan selection in Step 4

Treat the selected plan as a path, never as an option. If its text begins with `-`, resolve it to an absolute path with an argv-safe filesystem operation and validate that exact resolved file again. If safe normalization is unavailable or ambiguous, reject the path and ask for an explicit `./...` or absolute path.

## Step 2: Ask Executor

Use Codex's native interactive question tool:
- header: "Executor"
- question: "Which executor should ralphex use?"
- options:
  - label: "Configured (Recommended)"
    description: "Use ralphex's effective config; Claude Code is the default when executor is unset"
  - label: "Codex"
    description: "Add --codex; codex runs tasks, internal reviews, and finalize while external review is skipped"

`--codex` is the first-class Codex executor flag. It is not the same as the deprecated `--codex-only` alias for `--external-only`. Never combine `--codex` with `--external-only` or `--codex-only`.

## Step 3: Ask Execution Mode

Use Codex's native interactive question tool:
- header: "Mode"
- question: "Which execution mode should ralphex use?"
- options:
  - label: "Full pipeline (Recommended)"
    description: "Run tasks, internal reviews, and finalize; configured external review runs only with a non-Codex executor"
  - label: "Review pipeline"
    description: "Add --review; review current-branch changes and allow agents to fix and commit findings"
  - label: "External review"
    description: "Add --external-only; skip tasks and first internal review, then fix findings and run the post-external review"

If Codex executor was selected, do not offer "External review" because first-class `--codex` skips that phase and the flags are incompatible. Offer only "Full pipeline" and "Review pipeline".

If "Configured" executor and "External review" are selected, inspect the effective ralphex config before proceeding. Respect `RALPHEX_CONFIG_DIR` when set and local `.ralphex/config` overrides. If the effective config contains `executor = codex`, do not launch an incompatible command; ask the user to choose Full/Review with Codex or change the config explicitly.

## Step 4: Plan Selection (if no argument provided)

**If Full pipeline selected:**
- Search for `docs/plans/*.md` with exact filesystem tools (excludes completed/)
- Plan is REQUIRED
- Preserve the Claude workflow's oldest-first result handling: REVERSE the list to get most recent first
- Offer up to 4 most recent plans
- First option (most recent) should have "(Recommended)" suffix
- Because a Codex question can show at most 3 options, use sequential questions when needed: offer the first 2 plans plus "More plans", then offer the remaining plans. Do not drop or rename any plan choice.
- User MUST select one

**If Review pipeline or External review mode selected:**
- Search for `docs/plans/**/*.md` with exact filesystem tools (includes completed/ for context)
- Plan is OPTIONAL
- Preserve the Claude workflow's oldest-first result handling: REVERSE the list to get most recent first
- Offer up to 4 most recent plans PLUS "None" at the end
- First plan option (most recent) should have "(Recommended)" suffix
- "None" option description: "Review existing changes without a plan file"
- Because a Codex question can show at most 3 options, use sequential questions when needed: offer the first 2 plans plus "More choices", then offer the remaining plan choices and "None" across further questions as necessary. Do not drop or rename any choice.
- If user selects "None", run without plan file

## Step 5: Ask Max Iterations

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

## Step 6: Fail-Closed Launch Preflight

### Repository-local executable overrides (every mode)

Read `.ralphex/config` directly when it exists. Reject the launch if the file is unreadable, malformed, changes while being inspected, or contains any active non-empty assignment for:

- `claude_command`
- `codex_command`
- `custom_review_script`
- `vcs_command`

These values select executables or scripts that the background run would invoke. Do not offer a proceed/override choice. Report the blocking keys and values, then stop. If the file passes, record its content hash (or an equivalent exact-content snapshot); if it is absent, record that exact absence. Use this baseline for the immediate pre-launch revalidation in Step 7.

### Review checkout (Review pipeline and External review only)

Run this step only for Review pipeline or External review mode. Both modes operate on the current checkout. Their review agents can edit files and create commits while fixing findings.

Before any ralphex process starts:

1. Resolve the named current branch with `git symbolic-ref --quiet --short HEAD`. Detached HEAD or an unresolved/empty branch is a hard failure.
2. Require `git status --porcelain=v1` to be empty, including staged, tracked, and untracked changes.
3. Resolve the exact base ref ralphex will use: repo-local `default_branch`, then global ralphex config, then the repository's remote/default-branch evidence. If the effective base is missing, conflicting, or ambiguous, stop.
4. Verify the base resolves to a commit and the current named branch is not that default branch.
5. Require a non-empty committed `base...HEAD` file diff. An uncommitted diff, an ahead commit with no file delta, or an empty diff does not qualify.
6. On any failure, report the observed branch, base, status, and diff condition, then stop. Do not offer "Proceed anyway", switch branches, stash, commit, or modify the checkout.
7. Record the exact branch, resolved base commit, clean-status result, and committed-diff evidence for the immediate pre-launch revalidation in Step 7.

## Step 7: Launch ralphex in Background

Build the argument vector:

```text
["ralphex",
  "--codex",              # only if user selected Codex executor
  "--review",             # only if user selected Review pipeline
  "--external-only",      # only if user selected External review
  "--max-iterations", N,  # from user selection (25, 50, or 100)
  "--", plan-file]        # append both only when a plan was selected
```

The executor and mode flags are alternatives; include only the flags selected above. `--` must immediately precede a positional plan path so even a normalized leading-dash filename cannot become an option. Omit both `--` and the plan item when no plan was selected.

Prefer a process tool that accepts an argv array so the plan path is passed as one opaque argument. Never concatenate a plan path into a shell command. If only a shell terminal is available, POSIX-single-quote every dynamic argument (including the plan path), escaping every embedded single quote with the shell sequence `'"'"'`; stop if a value cannot be represented safely. Never use `eval`.

**Determine progress filename** based on mode and plan selection:
- Full mode + plan: `.ralphex/progress/progress-{plan-stem}.txt`
- Review mode + plan: `.ralphex/progress/progress-{plan-stem}-review.txt`
- External review + plan: `.ralphex/progress/progress-{plan-stem}-codex.txt`
- Full mode + no plan: `.ralphex/progress/progress.txt`
- Review mode + no plan: `.ralphex/progress/progress-review.txt`
- External review + no plan: `.ralphex/progress/progress-codex.txt`

Where `{plan-stem}` is the plan filename without extension (e.g., `fix-bugs` from `fix-bugs.md`).

Before launch, record whether this progress file exists and capture its current content hash or equivalent file identity/size evidence. Also record the launch time.

Immediately before spawning the process, repeat every applicable Step 6 check:

1. Re-read `.ralphex/config`; require the same safe content/hash and no executable-bearing override.
2. For Review pipeline or External review, require the same named branch and resolved base commit, a still-clean status, and a still-non-empty committed `base...HEAD` diff.
3. If anything changed or cannot be revalidated, stop without launching. Do not reuse the earlier result.

Only after this second gate passes, run with Codex's native background terminal. **Save the returned session ID** - it is the Codex-native equivalent of Claude's background task ID and is needed for status checks later.

## Step 8: Confirm Launch

1. Wait 10-15 seconds for initialization
2. Poll the saved background session and read its liveness or completed exit status.
3. Verify the progress file was created or changed after the recorded launch baseline. Read the new/current last 20 lines with an argv-safe file tool; shell fallback is `tail -n 20 -- '<safely-quoted-progress-path>'`.
4. Confirm launch only when both are true:
   - the session is still running, or it exited with a known status; and
   - the current launch produced fresh progress evidence after the baseline (new file, changed content, a fresh restart marker, or timestamped activity).
5. Existing `Plan:`, `Branch:`, or `Started:` headers alone are not proof; they may belong to an earlier run. If the session exited non-zero, report launch failure with the exit code and fresh progress tail. If it exited zero before confirmation, report that it already completed rather than saying it is running. If session state or fresh progress cannot be verified, report launch as unconfirmed.
6. For a live confirmed session, report:

```
ralphex started. Session ID: [session-id]

Plan: [plan file from progress file]
Branch: [branch from progress file]
Mode: [mode from progress file]
Progress file: [progress-filename]

Manual monitoring:
  tail -f -- '<progress-filename>'       # live stream
  tail -n 50 -- '<progress-filename>'    # recent activity

ralphex runs autonomously (can take hours). Process continues if you close this conversation.
Ask "check ralphex" to get status update.
```

**STOP HERE after reporting launch status. Do not continue monitoring automatically.**

## Step 9: Progress Check (only on explicit user request)

If user explicitly asks "check ralphex", "ralphex status", or "how is ralphex doing":

1. Poll the saved Codex background terminal session without blocking (use the session ID from Step 7)
2. Read last 40 lines of progress file (use filename from Step 7)

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
