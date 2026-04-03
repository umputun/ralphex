# Fix worktree plan commit case mismatch (#265)

## Overview

When using `--plan` mode with `use_worktree = true`, if the plan's branch already exists from a previous attempt, ralphex fails to commit the plan file. The root cause is a filename case mismatch on macOS case-insensitive APFS: the previous commit tracked the file with one case (e.g., uppercase B), but the new plan file path uses different case (lowercase b). `git add` receives the wrong-case path and silently fails to stage the file, causing `git commit` to fail with "no changes added to commit".

Related: [GitHub Issue #265](https://github.com/umputun/ralphex/issues/265)

## Context

- `pkg/git/service.go` — `CommitPlanFile` (line 315): computes `localPlan` from main repo path, preserving caller's case
- `pkg/git/service.go` — `copyToWorktree` (line 347): writes plan to worktree using main repo's case; on case-insensitive FS, overwrites existing file but directory entry retains original case
- `pkg/git/external.go` — `add()` (line 298): passes path to `git add` without case canonicalization
- `pkg/git/external.go` — `hasChangesOtherThan()` (line 289): uses exact `==` comparison to exclude plan file from dirty list — case mismatch causes false positives
- `pkg/git/external.go` — `toRelative()` (line 478): preserves caller's basename case (only resolves symlinks on directory portion)
- `cmd/ralphex/main.go` — `runWithWorktree()` (line 693): calls `CommitPlanFile(req.PlanFile, ...)` using main repo path

## Solution Overview

Add a `resolveFilesystemCase` helper that reads the parent directory to find the actual filename case on disk, then apply it in `CommitPlanFile` and `CreateBranchForPlan` before calling `git add`. Additionally, fix `hasChangesOtherThan` to use case-insensitive comparison when excluding the plan file.

This is the minimal fix that addresses the reported bug, the same bug class in the non-worktree path (`CreateBranchForPlan`), and the adjacent case-sensitivity issue in dirty-file detection. No changes to `CommitPlanFile` signature or `copyToWorktree` are needed.

## Technical Details

**`resolveFilesystemCase(path string) string`** — standalone unexported function in `pkg/git/external.go`. Reads `os.ReadDir(dir)` and finds a case-insensitive match for the basename via `strings.EqualFold`. Returns the path with the actual on-disk case. Falls back to the original path if directory can't be read or no match is found. This is a standalone function (not on `backend` interface) because it's pure filesystem logic with no git operations.

Note: the `os.ReadDir` + `EqualFold` approach works identically on both case-sensitive (Linux) and case-insensitive (macOS) filesystems — it always scans directory entries and matches by fold, regardless of OS behavior.

**`CommitPlanFile`** — after computing `localPlan`, call `resolveFilesystemCase` to canonicalize the path before passing to `add()`.

**`CreateBranchForPlan`** — same fix: call `resolveFilesystemCase(planFile)` before `s.repo.add(planFile)` at line 248. Same bug class, different code path (non-worktree mode with existing branch).

**`hasChangesOtherThan`** — replace `filePath == rel` with `strings.EqualFold(filePath, rel)` to handle case-insensitive plan file exclusion.

## Development Approach

- **testing approach**: TDD (tests first)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task**
- run tests after each change

## Testing Strategy

- **unit tests**: required for every task
- tests use real git repos via `setupExternalTestRepo` helper (not mocks)
- test the case-mismatch scenario by creating files with specific case, committing, then operating with different case

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix

## Implementation Steps

### Task 1: Add resolveFilesystemCase helper with tests (TDD)

**Files:**
- Modify: `pkg/git/external.go`
- Modify: `pkg/git/external_test.go`

- [x] write test `Test_resolveFilesystemCase` in `pkg/git/external_test.go` (table-driven):
  - case "returns actual case when basename differs": create file `Foo-Bar.md`, call with `foo-bar.md`, expect path ending in `Foo-Bar.md`
  - case "returns original path when no match": call with nonexistent file, expect unchanged path
  - case "returns original path when file matches exactly": create `exact.md`, call with `exact.md`, expect `exact.md`
  - case "returns original path when directory unreadable": call with path in nonexistent dir
  - note: all tests work on both case-sensitive and case-insensitive FS because the function scans dir entries with `EqualFold`
- [x] run tests — new tests must FAIL (red phase)
- [x] implement standalone unexported `resolveFilesystemCase(path string) string` in `pkg/git/external.go`:
  - `os.ReadDir(filepath.Dir(path))` to list directory entries
  - `strings.EqualFold(entry.Name(), filepath.Base(path))` to find match
  - return `filepath.Join(dir, entry.Name())` on match, original path on fallback
- [x] run tests — must PASS (green phase)
- [x] run `go test ./pkg/git/...` — all tests must pass

### Task 2: Fix CommitPlanFile and CreateBranchForPlan to resolve filesystem case (TDD)

**Files:**
- Modify: `pkg/git/service.go`
- Modify: `pkg/git/service_test.go`

- [x] write test `TestService_CommitPlanFile` subtest "commits plan file with case-mismatched path" in `pkg/git/service_test.go`:
  - create plan file `docs/plans/Case-Test.md` on master
  - create worktree from master (new branch)
  - in worktree, call `CommitPlanFile` with lowercase path `docs/plans/case-test.md` (different case)
  - verify commit succeeds and plan is committed on feature branch
  - cleanup worktree
- [x] write test `TestService_CreateBranchForPlan` subtest "commits plan file with case-mismatched path" in `pkg/git/service_test.go`:
  - create plan file `docs/plans/Branch-Case.md` on master
  - call `CreateBranchForPlan` with lowercase path `docs/plans/branch-case.md`
  - verify branch created and plan committed
- [x] run tests — new tests must FAIL (red phase)
- [x] in `CommitPlanFile` (`pkg/git/service.go`), add `localPlan = resolveFilesystemCase(localPlan)` before `s.repo.add(localPlan)` (standalone function, not via backend interface)
- [x] in `CreateBranchForPlan` (`pkg/git/service.go`), add `planFile = resolveFilesystemCase(planFile)` before `s.repo.add(planFile)` in the `planHasChanges` block
- [x] run tests — must PASS (green phase)
- [x] run `go test ./pkg/git/...` — all tests must pass

### Task 3: Fix hasChangesOtherThan case-insensitive comparison (TDD)

**Files:**
- Modify: `pkg/git/external.go`
- Modify: `pkg/git/external_test.go`

- [x] write test `TestExternalBackend_hasChangesOtherThan` subtest "excludes plan file with different case" in `pkg/git/external_test.go`:
  - create a test repo, add and commit a file `docs/plans/My-Plan.md`
  - modify the file (make it dirty)
  - call `hasChangesOtherThan` with lowercase path `docs/plans/my-plan.md`
  - verify the file is excluded from the dirty list (empty result)
- [x] run tests — new test must FAIL (red phase)
- [x] in `hasChangesOtherThan` (`pkg/git/external.go:289`), replace `filePath == rel` with `strings.EqualFold(filePath, rel)`
- [x] run tests — must PASS (green phase)
- [x] run `go test ./pkg/git/...` — all tests must pass

### Task 4: Verify acceptance criteria

- [x] verify the full case-mismatch scenario: worktree + existing branch + different case plan file → commit succeeds
- [x] run full unit test suite: `go test ./...`
- [x] run linter: `golangci-lint run --max-issues-per-linter=0 --max-same-issues=0`
- [x] run formatters: `~/.claude/format.sh`
- [x] verify test coverage for changed files meets 80%+

### Task 5: [Final] Finalize

- [x] move this plan to `docs/plans/completed/`

## Post-Completion

**Manual verification:**
- test on macOS with case-insensitive APFS (the reported environment)
- create a plan, abort, re-create with different case, verify worktree commit succeeds
