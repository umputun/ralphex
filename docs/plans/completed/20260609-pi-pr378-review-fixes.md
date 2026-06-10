# PR #378 Review Fixes — pi provider support

## Overview

Address the `CHANGES_REQUESTED` review on
[PR #378](https://github.com/umputun/ralphex/pull/378) (branch
`pi-provider-support`). The wrapper is the keeper; the review asks for three
changes before merge:

1. **Drop the `assets/pi/` skill tree.** Every other provider (codex, copilot,
   gemini, agy, opencode) is wrapper-only. `assets/pi/` is ~1k hand-copied
   lines from `assets/claude/skills/` with no sync mechanism, so it would
   silently go stale as the claude skills evolve. Keep only the wrapper plus
   one-line provider bullets, and drop the out-of-proportion `## pi
   Integration` H2 sections.
2. **Stop putting the prompt back on pi's argv.** The wrapper reads the prompt
   from stdin to dodge the command-line length limit, then appends it as a
   positional arg to `pi` — a large review prompt with a full diff can blow
   past Linux's 128 KB per-arg cap before pi runs. `pi --mode json --print`
   reads the prompt from stdin (verified), so pass it via stdin.
3. **(reviewer left this; we are fixing it)** stderr lines are re-emitted as
   `content_block_delta` and run through ralphex's signal detection, so a
   stderr line containing a literal `<<<RALPHEX:...>>>` could be misread as a
   real completion signal. Neutralize the signal token in stderr while keeping
   error/limit pattern detection intact.

## Context (from discovery)

Files/components involved:
- `scripts/pi-as-claude/pi-as-claude.sh` — the wrapper (prompt delivery,
  stderr re-emission)
- `scripts/pi-as-claude/pi-as-claude_test.sh` — wrapper unit tests (mock `pi`,
  positional-arg assertions, `PI_EXTRA_ARGS` ordering)
- `scripts/pi-as-claude/pi-as-claude_docs_test.sh` — cross-doc assertions,
  including several that reference `assets/pi/skills/`
- `assets/pi/` — entire tree to delete (`README.md`, `skills_test.sh`, four
  `skills/*/SKILL.md`)
- `README.md` — `## pi Integration (Optional)` section (~lines 1409–1435)
- `llms.txt` — `## pi Integration (Optional)` section (~lines 333–354)
- `docs/custom-providers.md` — `### Companion pi skills` subsection (~line 415)
- `CLAUDE.md` — plugin-version workflow bullet (~line 493) mentions the pi
  skills manifest rationale

Verified facts:
- `pi --mode json --print` reads the prompt from **stdin** when no positional
  message is given (tested: piped text appears as the user message). The fix
  mirrors `copilot-as-claude.sh`, which writes the prompt to a temp file and
  redirects `copilot ... < "$prompt_file"`.
- Signal marker prefix is the literal `<<<RALPHEX:` (`pkg/status/status.go`,
  `pkg/progress/progress.go:462`). Error/limit patterns (rate-limit text,
  `API Error:`, etc.) never contain that token, so neutralizing only
  `<<<RALPHEX:` in stderr preserves error detection.
- Shell test suites are **not** wired into CI (`ci.yml` runs only
  `ralphex-dk.sh --test`); `assets/pi/skills_test.sh` is referenced nowhere in
  CI or Makefile. Removal is clean. shellcheck is run manually per project
  convention.

What stays (do NOT remove — these are wrapper docs, not skill docs):
- `CLAUDE.md` project-structure line for `scripts/pi-as-claude/` and the pi
  entry in the alternative-providers paragraph
- `llms.txt` Requirements `pi` bullet and the pi `claude_command` wrapper notes
- `docs/custom-providers.md` pi **wrapper** section (only the
  `### Companion pi skills` subsection is removed)
- `scripts/pi-as-claude/README.md` (wrapper README — unchanged)

## Development Approach

- **Testing approach**: Regular (code-first, then update the bash test suites
  to match), as chosen by the user. For shell scripts, "tests" means the bash
  test suites plus `shellcheck` (required by project convention after editing
  any `.sh`).
- Complete each task fully before moving to the next.
- Make small, focused changes; keep scope to the three review asks.
- After each shell edit, run `shellcheck` on the touched script.
- All affected bash test suites must pass before moving to the next task.

## Testing Strategy

- **Unit tests**: bash test suites in `scripts/pi-as-claude/`. Update
  assertions to match new behavior in the same task as the code change.
- **shellcheck**: run on any edited `.sh` file (project rule).
- **No Go changes**: no Go tests affected, but run `go build ./...` once at the
  end as a sanity check (no Go code touched, so this should be a no-op pass).
- **No e2e**: web dashboard e2e tests are unaffected.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.

## What Goes Where

- **Implementation Steps** (`[ ]`): wrapper edits, doc edits, file deletions,
  test-suite updates — all in-repo.
- **Post-Completion** (no checkboxes): pushing the branch, replying to the
  reviewer, and the note about the immutable completed plan file.

## Implementation Steps

### Task 1: Pass the prompt to pi via stdin, not argv

- [x] in `scripts/pi-as-claude/pi-as-claude.sh`, remove `pi_args+=("$prompt")`
      (the line that appends the prompt as a positional arg)
- [x] write the (possibly adapter-prepended) prompt to a temp file created with
      `mktemp` and cleaned up in the existing `cleanup()` trap (mirror
      `copilot-as-claude.sh`: `prompt_file=$(mktemp)` →
      `printf '%s' "$prompt" > "$prompt_file"` → add to `rm -f` in cleanup)
- [x] change the background invocation from
      `pi "${pi_args[@]}" 2>"$stderr_file" > "$stdout_pipe" &` to redirect the
      prompt file on stdin:
      `pi "${pi_args[@]}" < "$prompt_file" 2>"$stderr_file" > "$stdout_pipe" &`
- [x] keep `PI_EXTRA_ARGS` handling intact — it now produces the final entries
      of `pi_args` (no positional prompt follows it anymore); update the
      surrounding comment that says "before the prompt"
- [x] update the header comment block: the build-args comment ("prompt as
      positional arg") and any wording implying the prompt is on argv should
      now describe stdin delivery, noting it avoids the per-arg length cap
- [x] update `scripts/pi-as-claude/pi-as-claude_test.sh`: change the mock `pi`
      to capture stdin (e.g. `cat > "$TMPDIR_TEST/pi_stdin"`) instead of the
      last positional arg; rename/retarget the "prompt passed as positional
      arg" assertion to verify the prompt arrives via stdin
- [x] update the `PI_EXTRA_ARGS` ordering test: it currently asserts extra args
      precede the prompt positional arg — change it to assert the extra args
      are present in `pi_args` and that no prompt positional arg is appended
- [x] run `shellcheck scripts/pi-as-claude/pi-as-claude.sh` — no warnings
- [x] run `bash scripts/pi-as-claude/pi-as-claude_test.sh` — all pass before
      next task

### Task 2: Neutralize stray RALPHEX signals on stderr

- [x] in the stderr re-emission loop (the `while IFS= read -r err_line` block),
      replace the literal `<<<RALPHEX:` token in each stderr line with a benign
      variant before emitting (e.g. insert a space: `<<< RALPHEX:`) so it can no
      longer match signal detection, while leaving all other text — including
      rate-limit / `API Error:` phrases — intact for error/limit pattern checks
- [x] use a shell-native substitution (e.g. bash parameter expansion
      `${err_line//<<<RALPHEX:/<<< RALPHEX:}`) so no extra `jq`/`sed` pass is
      added; keep the existing `jq -Rc` JSON-encoding of the result
- [x] add a comment explaining why: stderr is emitted for error/limit detection
      only, and a literal signal token on stderr must not be mistaken for a real
      completion signal
- [x] add a test in `pi-as-claude_test.sh`: feed a stderr line containing
      `<<<RALPHEX:ALL_TASKS_DONE>>>` via `MOCK_STDERR_FILE` and assert the
      emitted stream contains no intact `<<<RALPHEX:ALL_TASKS_DONE>>>` token
- [x] add a companion test: a stderr line with a rate-limit phrase (e.g.
      `You've hit your usage limit`) is still emitted verbatim as a
      `content_block_delta` so error/limit detection keeps working
- [x] run `shellcheck scripts/pi-as-claude/pi-as-claude.sh` — no warnings
- [x] run `bash scripts/pi-as-claude/pi-as-claude_test.sh` — all pass

### Task 3: Remove the assets/pi skill tree and its doc sections

- [x] `git rm -r assets/pi` (removes `README.md`, `skills_test.sh`, and the four
      `skills/*/SKILL.md` files)
- [x] remove the entire `## pi Integration (Optional)` section from `README.md`
      (heading through the trailing plugin-manifest note, ~lines 1409–1435)
- [x] remove the entire `## pi Integration (Optional)` section from `llms.txt`
      (heading, the `---` separators around it, through the manifest note,
      ~lines 333–354) — keep the wrapper bullet in Requirements and the
      `claude_command` wrapper notes
- [x] remove the `### Companion pi skills` subsection from
      `docs/custom-providers.md` (keep the pi **wrapper** section above it)
- [x] update the `CLAUDE.md` plugin-version workflow bullet (~line 493): drop
      the clause about pi skills not triggering a plugin bump; restore it to the
      pre-PR wording that gates only `assets/claude/`
- [x] verify the surviving wrapper references stay: `CLAUDE.md`
      project-structure `scripts/pi-as-claude/` line and providers paragraph,
      `llms.txt` Requirements pi bullet, `docs/custom-providers.md` pi wrapper
      section, `scripts/pi-as-claude/README.md`
- [x] write tests: this task has no code logic — covered by the doc-test update
      in Task 4 (the cross-doc assertions are the regression guard)

### Task 4: Update the docs test suite

- [x] in `scripts/pi-as-claude/pi-as-claude_docs_test.sh`, remove the
      assertions that reference removed content: `assets/pi/skills/` in
      `docs/custom-providers.md`, `README.md`, and `llms.txt`; the
      `/skill:ralphex-plan` README assertion; and the `assets/pi/` manifest
      rationale assertion in `CLAUDE.md`
- [x] keep the wrapper-path assertions (`scripts/pi-as-claude/` and
      `scripts/pi-as-claude/pi-as-claude.sh`) — those still hold
- [x] add an assertion (optional but recommended) guarding that `## pi
      Integration` no longer appears in `README.md` / `llms.txt`, to prevent
      reintroduction
- [x] run `shellcheck scripts/pi-as-claude/pi-as-claude_docs_test.sh` — no
      warnings
- [x] run `bash scripts/pi-as-claude/pi-as-claude_docs_test.sh` — all pass

### Task N-1: Verify acceptance criteria

- [x] grep the repo for dangling references:
      `grep -rn "assets/pi\|## pi Integration" --include='*.md' --include='*.txt'
      --include='*.sh' . | grep -v docs/plans/completed` returns nothing
      (only the active plan file and the docs-test negative assertions match —
      no real doc/wrapper content)
- [x] confirm `assets/pi/` no longer exists (`ls assets/pi` errors)
- [x] re-run both pi shell test suites — all pass (58 + 26)
- [x] run `shellcheck` on the wrapper and both test scripts — clean (only
      SC2329 info notes for trap-invoked handlers, a pre-existing false positive)
- [x] run `go build ./...` — passes (no Go changes; sanity check)
- [x] re-read the diff vs `master`: changes are limited to the wrapper, its two
      test suites, the four doc files, and the `assets/pi/` deletion

### Task N: Update documentation

- [x] no extra docs needed beyond Task 3's edits — the wrapper README and
      provider bullets already document pi as a wrapper-only provider; confirm
      they read coherently after the skill sections are gone (fixed one stale
      "prompt as a positional argument" line in docs/custom-providers.md to
      describe stdin delivery, matching the Task 1 wrapper change)

*Note: ralphex automatically moves completed plans to `docs/plans/completed/`*

## Technical Details

- **stdin delivery**: temp file + `< "$prompt_file"` is preferred over
  `printf ... | pi` here because `pi` already runs in the background with
  `> "$stdout_pipe"` and SIGTERM forwarding via `$pi_pid`; a redirect keeps the
  existing background/PID structure unchanged, whereas a pipe would complicate
  capturing `$!`. This matches `copilot-as-claude.sh:186`.
- **stderr sanitization**: bash parameter expansion
  `${err_line//<<<RALPHEX:/<<< RALPHEX:}` neutralizes every signal token on a
  line with no extra process spawn. Only `<<<RALPHEX:` is rewritten; the rest of
  the line (including `<<<RALPHEX:END>>>` halves) is broken at the prefix, which
  is sufficient because `detectSignal` keys on the full `<<<RALPHEX:NAME>>>`
  token.
- **Section boundaries**: confirm exact start/end line numbers at edit time
  (the numbers above are from discovery and may shift); match on the heading
  text, not line numbers.

## Post-Completion

*Items requiring manual intervention or external systems — informational only*

- **Immutable completed plan**: `docs/plans/completed/20260607-pi-provider-support.md`
  was added by this branch and documents building the `assets/pi/` skills.
  Per project rule ("completed plans are immutable, a historical record"), do
  **not** edit it to reflect this removal — it records what was done, and the
  review-driven removal is a separate, later event captured by this plan. Flag
  for the reviewer if they prefer otherwise.
- **Push and reply**: after verification, push `pi-provider-support` and reply
  on PR #378 summarizing the three changes (skills removed, prompt via stdin,
  stderr signal neutralized).
- **CHANGELOG**: do not touch — changelog updates are a release-time step.
