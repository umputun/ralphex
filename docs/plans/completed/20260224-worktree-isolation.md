# Plan: Git Worktree Isolation for Parallel Plan Execution

Enable running multiple ralphex instances on the same repo simultaneously by executing each plan in an isolated git worktree. When enabled, ralphex creates a temporary worktree at `.ralphex/worktrees/<plan-name>`, chdirs into it, and runs the full pipeline there. On completion (success, failure, or SIGINT), the worktree is removed and the branch is preserved for PR.

Closes #155

## Overview

- `--worktree` CLI flag forces worktree mode for a single run
- `use_worktree` config option (global + local, off by default for backward compatibility)
- Worktrees created at `.ralphex/worktrees/<plan-name>` inside the main repo
- After creation, `os.Chdir` into the worktree so all CWD-relative paths work naturally
- Progress logger created **before** chdir so progress files stay in main repo's `.ralphex/progress/`
- Main repo `Service` kept for plan file moves (`MovePlanToCompleted`) and `DiffStats`
- Worktree auto-removed on all exit paths (defer + SIGINT cleanup)
- Web dashboard sees worktree progress via explicit `--watch` dirs (auto-discovery deferred to future work)

## Validation Commands

- `make test`
- `make lint`

## Context

**Architecture compatibility (already works without changes):**
- Git operations use `git rev-parse --show-toplevel` which returns worktree root
- Claude/Codex executors inherit CWD (no `cmd.Dir` set)
- Flock and session IDs use absolute paths, no collisions
- `.git` startup check passes (worktrees have a `.git` file, not directory; `os.Stat` succeeds for both)
- `GetDefaultBranch` reads shared refs, works across worktrees
- `newExternalBackend` uses `git rev-parse --show-toplevel` inside worktrees (verified by existing test `"opens git worktree"` in `external_test.go`)

**Cross-boundary issues to handle:**
- `MovePlanToCompleted` uses `toRelative` against repo root - plan file is in main repo, not worktree
- `DiffStats` should run from worktree service (has correct HEAD) before worktree cleanup
- SIGINT force-exit (5s timeout) bypasses defers - worktree cleanup must be in interrupt handler
- `EnsureIgnored` needs to add `.ralphex/worktrees/` pattern to main repo's `.gitignore`

**Ordering constraints:**
- `config.Load` runs before chdir (uses `os.Getwd` for local `.ralphex/config` detection) - this is correct, config comes from main repo
- `startInterruptWatcher` is set up at line 153 before config load - worktree cleanup added via mutable closure variable
- Progress logger must be created before chdir so files land in main repo's `.ralphex/progress/`

## Development Approach

- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**
- **CRITICAL: update this plan file when scope changes during implementation**
- Run tests after each change
- Maintain backward compatibility (worktree mode off by default)

## Progress Tracking

- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix
- Update plan if implementation deviates from original scope

## Implementation Steps

### Task 1: Add worktree operations to git backend

Add `git worktree add/remove/prune` support to the git backend interface and external implementation.

**Files:**
- Modify: `pkg/git/service.go`
- Modify: `pkg/git/external.go`
- Modify: `pkg/git/external_test.go`
- Modify: `pkg/git/service_test.go`

- [x] Add `addWorktree(path, branch string, createBranch bool) error` and `removeWorktree(path string) error` to `backend` interface
- [x] Add `pruneWorktrees() error` to `backend` interface
- [x] Implement `addWorktree` in `externalBackend` - when `createBranch=true` runs `git worktree add <path> -b <branch>`, when `createBranch=false` runs `git worktree add <path> <branch>` (reuse existing branch)
- [x] Implement `removeWorktree` in `externalBackend` - runs `git worktree remove --force <path>`
- [x] Implement `pruneWorktrees` in `externalBackend` - runs `git worktree prune`
- [x] Add public `CreateWorktreeForPlan(planFile string) (worktreePath string, err error)` to `Service`:
  - Guard: return error if not on main/master (same guard as `CreateBranchForPlan` line 128)
  - Derive branch name from plan file via `plan.ExtractBranchName` (no `ralphex/` prefix, consistent with existing convention)
  - Worktree path: `.ralphex/worktrees/<branch-name>` resolved as absolute from `s.repo.Root()`
  - Run `pruneWorktrees()` first to clean stale entries
  - If worktree dir already exists: return error "worktree already exists at <path>, another instance may be running"
  - If branch already exists and is checked out elsewhere: return error
  - If branch exists but not checked out: use `git worktree add <path> <branch>` (without `-b`)
  - Auto-commit plan file if it has uncommitted changes (replicate `CreateBranchForPlan` lines 170-179 logic)
  - Return absolute worktree path
- [x] Add public `RemoveWorktree(path string) error` to `Service`
- [x] Write tests for `addWorktree`/`removeWorktree`/`pruneWorktrees` in `external_test.go` using real git repos in `t.TempDir()`
- [x] Write tests for `CreateWorktreeForPlan` and `RemoveWorktree` in `service_test.go` covering: happy path, already-exists error, not-on-main error, branch-exists-reuse
- [x] Run `make test` - must pass before task 2

### Task 2: Add config option and CLI flag

Add `use_worktree` config option and `--worktree` CLI flag.

**Files:**
- Modify: `pkg/config/values.go`
- Modify: `pkg/config/config.go`
- Modify: `pkg/config/defaults/config`
- Modify: `pkg/config/values_test.go`
- Modify: `cmd/ralphex/main.go`

- [x] Add `WorktreeEnabled bool` and `WorktreeEnabledSet bool` to `Values` struct
- [x] Add parsing for `use_worktree` in `parseValuesFromBytes` (follow `finalize_enabled` pattern)
- [x] Add merge logic in `mergeFrom` for `WorktreeEnabled`/`WorktreeEnabledSet`
- [x] Add `WorktreeEnabled bool` and `WorktreeEnabledSet bool` to `Config` struct
- [x] Populate in config loading (follow existing pattern)
- [x] Add commented `# use_worktree = false` to `pkg/config/defaults/config`
- [x] Add `Worktree bool` field to `opts` struct with `long:"worktree" description:"run in isolated git worktree"`
- [x] Add override in `run()`: `if o.Worktree { cfg.WorktreeEnabled = true }`
- [x] Write tests for config parsing and merge of `use_worktree` option
- [x] Run `make test` - must pass before task 3

### Task 3: Integrate worktree lifecycle into main execution flow

Wire up worktree creation, chdir, execution, and cleanup in `cmd/ralphex/main.go`. This task replaces `CreateBranchForPlan` with `CreateWorktreeForPlan` when worktree mode is active.

**Files:**
- Modify: `cmd/ralphex/main.go`

- [x] Add worktree mode guard: only enable for `ModeFull` and `ModeTasksOnly` (review/plan/external modes skip worktree since they operate on existing branch changes or explore the main repo)
- [x] Handle `--plan` continuation: in `runPlanMode`, when worktree is enabled, the "continue with plan implementation" path (line 636) must use `CreateWorktreeForPlan` + chdir instead of `CreateBranchForPlan`
- [x] Resolve `planFile` to absolute path early (before any chdir) via `filepath.Abs`
- [x] Create progress logger **before** chdir so progress files land in main repo's `.ralphex/progress/` (currently `NewLogger` is inside `executePlan`; extract it or pass pre-created logger)
- [x] Add `.ralphex/worktrees/` to `EnsureIgnored` call (alongside `.ralphex/progress/`) **after** worktree creation (moved from before to avoid HasChangesOtherThan conflict)
- [x] In `run()`, add worktree creation block (replaces `CreateBranchForPlan` when active):
  - If `cfg.WorktreeEnabled && planFile != "" && modeRequiresBranch(mode)`:
    - Call `gitSvc.CreateWorktreeForPlan(planFile)` to get worktree path
    - Store original CWD via `os.Getwd()`
    - `os.Chdir(worktreePath)`
    - Open `worktreeGitSvc` via `git.NewService(".", colors.Info())`
    - Register defer: `os.Chdir(origDir)` then `gitSvc.RemoveWorktree(wtPath)`
  - Else: use existing `CreateBranchForPlan` flow unchanged
- [x] Handle interrupt cleanup: use a mutable `var worktreeCleanup func()` closure variable initialized to no-op, populated after worktree creation. Pass combined cleanup to `startInterruptWatcher`: `func() { restoreTerminal(); worktreeCleanup() }`. Since `startInterruptWatcher` captures the closure variable by reference via the wrapping function, it will see the populated value.
- [x] Add `MainGitSvc *git.Service` field to `executePlanRequest` for cross-boundary operations. In worktree mode, `GitSvc` = worktree service, `MainGitSvc` = main repo service. In normal mode, both point to the same service.
- [x] In `executePlan`: use `MainGitSvc` for `MovePlanToCompleted` (plan file is in main repo)
- [x] In `executePlan`: run `DiffStats` from worktree `GitSvc` (has correct HEAD with committed changes) before cleanup defer runs
- [x] Write tests in `cmd/ralphex/main_test.go`: verify worktree created, chdir happens, cleanup removes worktree, non-worktree modes skip worktree
- [x] Run `make test` - must pass before task 4

### Task 4: Make progress logger path absolute

Ensure progress file paths are absolute so they work correctly when passed to Claude/Codex running in a worktree.

**Files:**
- Modify: `pkg/progress/progress.go`
- Modify: `pkg/progress/progress_test.go`

- [x] In `NewLogger`, call `filepath.Abs` on the progress file path before opening
  - `Logger.Path()` will return absolute path usable from any CWD
  - Non-worktree mode unaffected (absolute paths work everywhere)
- [x] Write test: `Logger.Path()` returns absolute path
- [x] Write test: verify `filepath.Abs` on relative path produces expected absolute path
- [x] Run `make test` - must pass before task 5

### Task 5: Handle edge cases and error recovery

Ensure graceful handling of stale worktrees and concurrent access.

**Files:**
- Modify: `pkg/git/service.go`
- Modify: `pkg/git/service_test.go`

- [x] In `CreateWorktreeForPlan`: if worktree dir exists, return descriptive error (for v1, don't auto-cleanup; user can remove manually or wait for other run to complete)
- [x] In `CreateWorktreeForPlan`: if branch is checked out in another worktree, return descriptive error
- [x] In `RemoveWorktree`: handle case where worktree was already removed (no error)
- [x] In `RemoveWorktree`: handle case where worktree dir doesn't exist (no error)
- [x] Write tests for error cases: existing worktree, branch conflict, already-removed
- [x] Run `make test` - must pass before task 6

### Task 6: Verify acceptance criteria

- [x] Verify: `--worktree` flag creates worktree and executes in it
- [x] Verify: `use_worktree = true` in config works
- [x] Verify: default behavior unchanged (worktree off by default)
- [x] Verify: worktree auto-removed on completion
- [x] Verify: branch preserved after worktree removal
- [x] Verify: `--review`, `--plan`, `--external-only` modes unaffected
- [x] Cross-compile verify: `GOOS=windows GOARCH=amd64 go build ./...`
- [x] Run full test suite: `make test`
- [x] Run linter: `make lint`
- [x] Verify test coverage meets 80%+ (85.4%)

### Task 7: [Final] Update documentation

- [x] Update README.md with `--worktree` flag and `use_worktree` config option
- [x] Update CLAUDE.md with worktree-related patterns and files
- [x] Update `llms.txt` with `--worktree` usage example
- [x] N/A: progress files stay in main repo's `.ralphex/progress/`, no extra `--watch` config needed
- [x] Move this plan to `docs/plans/completed/`

## Technical Details

**Worktree naming:**
- Branch: `<plan-name>` (derived from plan filename via `plan.ExtractBranchName`, same convention as existing `CreateBranchForPlan`)
- Directory: `.ralphex/worktrees/<plan-name>` (absolute, resolved from main repo root)

**Execution flow with worktree enabled:**
1. `run()` loads config, resolves plan file to absolute path
2. Create progress logger (writes to main repo's `.ralphex/progress/`)
3. `gitSvc.CreateWorktreeForPlan(planFile)` → creates `.ralphex/worktrees/<name>` with branch
4. `os.Chdir(worktreePath)` → switch into worktree
5. Open `worktreeGitSvc` from new CWD
6. `executePlan(ctx, ...)` runs normally (Claude/Codex inherit worktree CWD)
7. On completion: `DiffStats` from `worktreeGitSvc` (correct HEAD), `MovePlanToCompleted` from `mainGitSvc`
8. Cleanup (defer + interrupt): `os.Chdir(origDir)` then `mainGitSvc.RemoveWorktree(wtPath)`

**Interrupt handler cleanup:**
- `var worktreeCleanup func()` initialized to no-op before `startInterruptWatcher`
- Populated with actual cleanup after worktree creation
- `startInterruptWatcher` receives `func() { restoreTerminal(); worktreeCleanup() }`
- Closure captures variable by reference, sees populated value on force-exit

**Config precedence:**
- CLI `--worktree` → forces on for this run
- Local `.ralphex/config` `use_worktree = true` → project default
- Global `~/.config/ralphex/config` `use_worktree = true` → user default
- Embedded default: `false`

## Post-Completion

**Future enhancements (not in scope):**
- Web dashboard auto-discovery of worktree progress dirs
- `ralphex --worktree-list` / `ralphex --worktree-clean` management commands
- Parallel plan execution from a single ralphex invocation (launch multiple worktrees)
- Auto-cleanup of stale worktrees (v1 returns error, user removes manually)
