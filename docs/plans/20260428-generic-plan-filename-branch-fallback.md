# Generic Plan Filename: Use Parent Directory as Branch Name

## Overview

`ExtractBranchName` in `pkg/plan/plan.go` derives a git branch name from the plan file path by
taking `filepath.Base` and stripping the `.md` extension and date prefix. For OpenSpec-style
layouts where the meaningful identity lives in the parent directory — e.g.
`openspec/changes/add-dark-mode/tasks.md` — this produces the useless branch name `tasks`
instead of `add-dark-mode`.

Fix: when the stripped filename is a known generic name (`tasks`, `plan`, `plans`, `index`, `readme`),
fall back to the basename of the parent directory, applying date-prefix stripping to it via a
dedicated `stripDirDatePrefix` helper (stricter regex than the file-level one). Match is case-insensitive.

Closes issue #306 (third gap; #309 covered the header-pattern gap).

## Context

- `pkg/plan/plan.go` — `ExtractBranchName` function, `datePrefixRe`/`dirDatePrefixRe` patterns,
  `stripDatePrefix`/`stripDirDatePrefix` helpers, `genericPlanFilenames` set
- `pkg/plan/plan_test.go` — `TestExtractBranchName` table-driven tests
- Callers: `pkg/git/service.go` — `preparePlanBranch` and `CommitPlanFile` both call
  `plan.ExtractBranchName`; no caller changes needed

## Development Approach

- **testing approach**: Regular (code first, then tests)
- Change is isolated to `pkg/plan/plan.go`; no callers change
- No config, no flags — hardcoded generic-name set for now

## Solution Overview

1. Extract the date-strip logic from `ExtractBranchName` into a private `stripDatePrefix(name)` helper so it can be reused for both the filename and the fallback directory name.
2. Add a package-level `genericPlanFilenames` set (map[string]bool) with entries: `tasks`, `plan`, `plans`, `index`, `readme`. A brief comment explains the rationale.
3. In `ExtractBranchName`, after computing the stripped filename, do a **case-insensitive** lookup:
   - If generic: compute `dir := stripDatePrefix(filepath.Base(filepath.Dir(planFile)))`. Use `dir`
     as the branch name unless `dir` is `.`, `/`, empty, or itself a generic name (nested case →
     keep original filename as the fallback of last resort).
   - Otherwise: return the stripped filename as before.

## Implementation Steps

### Task 1: Refactor ExtractBranchName with generic-filename fallback

**Files:**
- Modify: `pkg/plan/plan.go`
- Modify: `pkg/plan/plan_test.go`

- [x] extract `stripDatePrefix(name string) string` helper from the existing inline logic in
      `ExtractBranchName`
- [x] add package-level `genericPlanFilenames map[string]bool` with entries `tasks`, `plan`,
      `plans`, `index`, `readme`; add comment explaining these are filenames where the directory
      name carries the identity
- [x] in `ExtractBranchName`, after computing `branchName`, check
      `genericPlanFilenames[strings.ToLower(branchName)]`
- [x] on match: compute `dir := stripDatePrefix(filepath.Base(filepath.Dir(planFile)))`;
      return `dir` unless `dir` is empty, `.`, `/`, or itself generic (fall back to `branchName`)
- [x] write/update table-driven test cases in `TestExtractBranchName`:
      - `openspec/changes/add-dark-mode/tasks.md` → `add-dark-mode`
      - `openspec/changes/add-dark-mode/plan.md` → `add-dark-mode`
      - `openspec/changes/2024-01-15-add-dark-mode/tasks.md` → `add-dark-mode` (date stripped from dir)
      - `Tasks.md` (uppercase, bare) → `Tasks` (no dir fallback; dir is `.`)
      - `foo/tasks/tasks.md` (dir itself is generic) → `tasks` (nested generic, keep original)
      - `tasks.md` (bare filename) → `tasks` (dir is `.`, no fallback)
      - `docs/plans/2024-01-15-feature.md` → `feature` (non-generic, unchanged path)
      - `TASKS.md` (bare uppercase) → `TASKS` (case-insensitive match, dir is `.`, no fallback)
      - existing cases must continue to pass
- [x] run `make test` — must pass
- [x] run `make lint`

### Task 2: Update documentation

**Files:**
- Modify: `CLAUDE.md`

- [x] add a sentence to the `ExtractBranchName` / git package description in CLAUDE.md noting
      the generic-filename fallback behaviour (one line, in the Key Patterns or Git Package API
      section)
