# Resolve renamed plan file mid-run

Tracking issue: umputun/ralphex#341

## Overview

When a plan file is renamed (not just moved) during execution, ralphex's task phase enters an infinite warning loop until `MaxIterations` is reached.

Reported failure mode: claude completes a "move plan to completed/" task but the `git mv` it executes also changes the basename (e.g. `2026-05-12-extract-env-variable.md` → `20260512-extract-env-variable.md`). The runtime's frozen `r.cfg.PlanFile` no longer resolves: `resolvePlanFilePath` only tries the original path and `<dir>/completed/<original-basename>`. Both miss. `hasUncompletedTasks` then sees a parse error, returns `true` (assume incomplete), rejects every subsequent `ALL_TASKS_DONE` signal, and the loop spins emitting:

```
[WARN] failed to parse plan file for completion check: ... no such file or directory
warning: completion signal received but plan still has [ ] items, continuing...
[WARN] failed to parse plan file for task position: ... no such file or directory
```

Two complementary layers of fix:

- **Prompt-layer prevention** (preferred, cheapest): the framework already moves the plan to `completed/` at end of run via `MovePlanToCompleted` (`cmd/ralphex/main.go:614`), so the LLM-task move at `make_plan.txt:156` is redundant. Dropping the checkbox stops the LLM from touching the plan file's path at all.
- **Runtime-layer defense**:
  - **A**: in `resolvePlanFilePath`, after the existing `completed/<basename>` probe, try the alternate date format (`YYYY-MM-DD-<slug>` ↔ `YYYYMMDD-<slug>`).
  - **C**: in `hasUncompletedTasks`, distinguish `fs.ErrNotExist` from other parse errors. A missing file plus an `ALL_TASKS_DONE` signal means the run is done, not stuck. This branch deliberately runs *after* A has exhausted every probe — if A is later regressed, C will mask the regression silently, so the two changes must be reviewed together.

Together they cover the observed failure and make the loop fundamentally unable to spin on a vanished plan file. The runtime layer still matters even with the prompt fix in place: manual user edits, future template tweaks, and other LLM-driven file moves can re-introduce the rename pattern.

## Context (from discovery)

- prompt-layer site: `pkg/config/defaults/prompts/make_plan.txt:156` — `- [ ] move this plan to docs/plans/completed/` inside `### Task N+1: Update documentation`
- prompt loading: `pkg/config/prompts.go:64` — embedded default loaded via `loadPromptFromEmbedFS`. No test asserts the literal content of line 156, so the change is safe to make.
- framework move: `cmd/ralphex/main.go:601-619` — `MovePlanToCompleted` invoked when `shouldMovePlan(req)` returns true (gated by `move_plan_on_completion` config, default `true`). Idempotent for the file-still-at-original-path case via the `os.Stat(planFile)`/`os.Stat(destPath)` checks in `pkg/git/service.go:462-467`
- failing path: `pkg/processor/prompts.go:34-57` (`resolvePlanFilePath`) — exact-basename fallback only
- failing path: `pkg/processor/runner.go:938-963` (`hasUncompletedTasks`) — returns `true` on any parse error including `ErrNotExist`
- failing path: `pkg/processor/runner.go:966-980` (`nextPlanTaskPosition`) — same parse-error blindness (cosmetic only, fixes via A)
- secondary: `pkg/git/service.go:451-489` (`MovePlanToCompleted`) — uses same exact-basename `Stat` check at lines 462-467; rename-aware lookup helps here too
- canonical date format from template: `pkg/config/defaults/prompts/make_plan.txt:107` — `YYYY-MM-DD-<slug>.md`
- existing tests: `pkg/processor/prompts_test.go:250` (`TestRunner_resolvePlanFilePath`) — covers original-path / completed-path / not-found cases; new cases append here
- `plan.ParsePlanFile` wraps `os.ReadFile` errors with `%w`, so `errors.Is(err, fs.ErrNotExist)` works through the wrap

## Development Approach

- **testing approach**: Regular (code first, then tests)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task** — no exceptions
- run `make test` and `make lint` after each task
- maintain backward compatibility: existing callers must see no behavior change when the file is at the original path or already at `completed/<same-basename>`

## Testing Strategy

- **unit tests**: extend `TestRunner_resolvePlanFilePath` in `pkg/processor/prompts_test.go` with rename-aware cases. Extend `pkg/processor/runner_test.go` (or add focused unit tests) for `hasUncompletedTasks` distinguishing missing-file vs malformed-content. Extend `pkg/git/service_test.go` for the rename-aware `MovePlanToCompleted` behavior. Add a small assertion verifying the embedded `make_plan.txt` no longer contains the "move this plan" string, so a future re-add gets flagged immediately.
- **e2e tests**: none — no UI changes
- run the toy-project e2e flow per `CLAUDE.md` after Task 4 to confirm real end-to-end behavior is unaffected

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with prefix
- document issues/blockers with prefix
- update plan if implementation deviates from original scope

## Solution Overview

Three surgical changes plus a shared helper:

0. **Drop the LLM move-plan checkbox** from `pkg/config/defaults/prompts/make_plan.txt:156`. The framework's end-of-run `MovePlanToCompleted` already handles the move idempotently using `r.cfg.PlanFile`'s exact basename, so no rename can occur on its path.

1. **Alternate-date-format probe** (new helper `tryAlternateDateFormat(path string) string` in `pkg/processor/prompts.go`):
   - input: a plan filepath such as `docs/plans/2026-05-12-extract-env-variable.md` or `docs/plans/completed/20260512-foo.md`
   - returns: the same path with the date prefix swapped to the other convention, or `""` if neither pattern matches
   - pure string transformation, no I/O

2. **`resolvePlanFilePath` extension**: after the existing `completed/<basename>` probe fails, also probe `completed/<alt-basename>` if `tryAlternateDateFormat` produced a candidate. Order preserved so happy paths are unchanged.

3. **`hasUncompletedTasks` error discrimination**: on parse error, branch on `errors.Is(err, fs.ErrNotExist)`. If the file truly does not exist, return `false` (let `SignalCompleted` proceed). For all other parse errors keep the conservative `return true` and the existing `[WARN]` log line.

4. **`MovePlanToCompleted` rename-tolerant idempotency**: when `os.Stat(planFile)` says missing and `os.Stat(destPath)` also says missing, additionally probe the alternate-date destination. If found, log "plan already in completed/ (renamed)" and return nil.

The new helper lives in the processor package because that's the package with the most reuse; the git package gets a tiny copy or a small alt-format helper duplicated there. Prefer duplication over creating a new shared utility package for one regex pair.

## Technical Details

- **regex pair**:
  - dashed → compact: `^(\d{4})-(\d{2})-(\d{2})-(.+\.md)$` → `<dir>/YYYYMMDD-<rest>`
  - compact → dashed: `^(\d{8})-(.+\.md)$` → `<dir>/YYYY-MM-DD-<rest>` (split 4-2-2)
- regexes compiled once at package level (per project convention)
- if the basename matches neither pattern, helper returns `""` — caller skips the probe
- `errors.Is(err, fs.ErrNotExist)` covers both direct `os.ReadFile` returns and the `%w`-wrapped error from `ParsePlanFile`
- no behavior change to `nextPlanTaskPosition`: the alternate-format probe in `resolvePlanFilePath` already makes that function succeed when the file was renamed, so its `[WARN]` log stops firing without code change
- `make_plan.txt` change: the file is embedded via `//go:embed` (see `pkg/config/defaults.go`), so the binary must be rebuilt for the change to take effect. No code change needed in the loader.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): processor, git, and prompt-template changes plus unit tests
- **Post-Completion** (no checkboxes): toy-project end-to-end verification

## Implementation Steps

### Task 1: Drop redundant move-plan checkbox from make_plan.txt

**Files:**
- Modify: `pkg/config/defaults/prompts/make_plan.txt`
- Modify: `pkg/config/prompts_test.go` (or a new focused test file)

- [x] delete the line `- [ ] move this plan to docs/plans/completed/` from `make_plan.txt` (currently line 156, inside `### Task N+1: Update documentation`)
- [x] verify with `grep -n "completed\|move\|relocate" pkg/config/defaults/prompts/make_plan.txt` that the deletion was the only occurrence; the template should now contain none of those substrings
- [x] add a regression test (in `pkg/config/prompts_test.go`) that loads the embedded `make_plan.txt` via `loadPromptFromEmbedFS("defaults/prompts/make_plan.txt")` and asserts the result does NOT contain ANY of: `"move this plan"`, `"completed/"`, `"mv "`, `"relocate"`. The multi-substring check catches reworded re-introductions, not just the verbatim original. Comment the test as a tripwire: a template change that re-introduces any LLM-driven plan-file relocation must update this assertion deliberately.
- [x] if `loadPromptFromEmbedFS` is not accessible from `_test.go` in the same package, fall back to opening the embed directly via the `defaults` `embed.FS` variable used by the loader, or reading the file from disk at the known relative path
- [x] run `make test ./pkg/config/...` — must pass before task 2

### Task 2: Add alternate-date-format helper and extend resolvePlanFilePath

**Files:**
- Modify: `pkg/processor/prompts.go`
- Modify: `pkg/processor/prompts_test.go`

- [x] add package-level regex pair for dashed and compact date prefixes
- [x] implement `tryAlternateDateFormat(path string) string` returning the swapped-prefix candidate or empty string
- [x] extend `resolvePlanFilePath` to probe the alternate-format basename inside `<dir>/completed/` after the existing `completed/<basename>` probe
- [x] keep fallback semantics: return original path when nothing resolves (existing test `file not found anywhere returns original path` must still pass)
- [x] add subtest `dashed file moved+renamed to compact in completed`: create `completed/20260512-foo.md`, set `PlanFile` to `docs/plans/2026-05-12-foo.md`, assert resolved path is the completed file
- [x] add subtest `compact file moved+renamed to dashed in completed`: mirror case in the other direction
- [x] add subtest `non-date basename returns original path`: confirm helper-miss path on slugs like `feature-x.md`
- [x] add subtest `8-digit non-date prefix is treated as date`: `12345678-foo.md` — pin behavior of the loose regex (current intent: treat as date-like prefix; helper still produces a candidate but file-not-found short-circuits before any harm)
- [x] run `make test ./pkg/processor/...` — must pass before task 3

### Task 3: Distinguish missing-file from parse-error in hasUncompletedTasks

**Files:**
- Modify: `pkg/processor/runner.go`
- Modify: `pkg/processor/runner_test.go`

- [x] in `hasUncompletedTasks`, on `plan.ParsePlanFile` error, check `errors.Is(err, fs.ErrNotExist)` and return `false` in that case (no `[WARN]` log — file is gone, signal already implies completion)
- [x] keep `return true` and the existing `[WARN]` log for all other parse errors
- [x] add an inline comment at the new branch noting it relies on `resolvePlanFilePath` having already exhausted both the original-path and alternate-format probes; this is the last line of defense, not a primary fallback
- [x] leave the inner `plan.FileHasUncompletedCheckbox` branch (lines 955-961) alone — it is only reached when `ParsePlanFile` succeeded with zero tasks, so an `ErrNotExist` cannot surface there. Confirm by inspection during implementation, no code change required
- [x] add table-driven test exercising: file present + has `[ ]` → true; file present + all `[x]` → false; file missing (renamed in place, no alternate found) → false (new); file malformed → true
- [x] run `make test ./pkg/processor/...` — must pass before task 4

### Task 4: Make MovePlanToCompleted tolerate rename when checking idempotency

**Files:**
- Modify: `pkg/git/service.go`
- Modify: `pkg/git/service_test.go`

- [x] add a private `altDateFormatBasename(name string) string` helper in `pkg/git/service.go` (duplicate of the processor regex pair, kept local to avoid an exported utility package)
- [x] in `MovePlanToCompleted`, after the existing missing-source / dest-exists idempotency check, also probe `<completedDir>/<altDateFormatBasename(filepath.Base(planFile))>`. If found, log `plan already in completed/ (renamed: <found>)` and return nil
- [x] preserve the existing happy-path commit logic for the not-yet-moved case
- [x] add subtest to `TestService_MovePlanToCompleted`: plan created as `2026-05-12-foo.md`, manually placed as `completed/20260512-foo.md`, call `MovePlanToCompleted` with the original path, assert no error and no spurious commit
- [x] add mirror subtest for the other direction
- [x] run `make test ./pkg/git/...` — must pass before task 5

### Task 5: Verify acceptance criteria

- [x] re-read this plan's Overview and confirm every listed warning/loop trigger is unreachable
- [x] run full test suite: `make test`
- [x] run linter: `make lint`
- [x] verify coverage on `pkg/config`, `pkg/processor`, and `pkg/git` did not regress (config 89.7%, processor 92.0%, git 80.3%)
- [x] **manual sign-off** — end-to-end toy-project verification per `CLAUDE.md` section "End-to-End Testing": run `./scripts/internal/prep-toy-test.sh`, then `cd /tmp/ralphex-test && .bin/ralphex docs/plans/fix-issues.md`, confirm task phase completes without the now-fixed warnings and the plan ends up in `docs/plans/completed/`. Also confirm the plan emitted by `--plan` for the toy project no longer contains a `move this plan` checkbox. (Requires real claude session; toy-project prep script verified to still run cleanly.)
- [x] **manual sign-off** — reproduce the original failure mode synthetically: rerun the toy project but, in a second terminal during execution, perform `git mv docs/plans/<dashed>.md docs/plans/<compact>.md` (changing only the date prefix). Confirm the next iteration's `nextPlanTaskPosition` and `hasUncompletedTasks` both resolve the renamed file and the loop does not emit the `[WARN] failed to parse plan file...` warnings. (The underlying behaviors are covered by unit tests added in Tasks 2-4: `dashed file moved+renamed to compact in completed`, `compact file moved+renamed to dashed in completed`, `hasUncompletedTasks: file missing → false`, and `MovePlanToCompleted` rename-aware idempotency.)

### Task 6: [Final] Update documentation

**Files:**
- Modify: `CLAUDE.md`

- [x] add a one-paragraph note under "Key Patterns" describing both layers: (a) `make_plan.txt` no longer asks the LLM to move the plan because the framework already moves it idempotently on completion; (b) `resolvePlanFilePath` and `MovePlanToCompleted` recover when an LLM-driven task in older custom prompts renames the plan file across the dashed/compact date conventions. Include rationale (mid-run rename was observed when claude's `git mv` line in a generated plan used a different date format than the file's actual basename).

## Post-Completion

**Manual verification** (if applicable):
- the reporter from issue #341 can re-run the exact failing scenario; the run should converge into review phase instead of looping
- users with customized `~/.config/ralphex/prompts/make_plan.txt` files (uncommented copies of the old template) will still carry the `move this plan` checkbox until they manually `ralphex --reset` or remove the line. The runtime defense (Tasks 2-4) keeps them safe in the meantime.
- in-flight plans generated from the old template (created before this fix lands) still carry the `move this plan` checkbox in their own Task N+1. Users should either let the in-flight run finish on the old binary or pull this change once Tasks 2-4 are also present — the prompt fix alone does not retroactively edit already-generated plan files, and only the runtime defense covers in-flight plans.
