# Drop internal go-git backend

## Overview

Remove the go-git library backend, making the external git CLI backend the only implementation. This eliminates a class of false-positive bugs in go-git's `wt.Status()` (upstream issues closed as "not planned"), removes ~40 transitive dependencies, and simplifies the codebase by ~2,400 lines.

Related to #91.

## Context

- `pkg/git/git.go` (680 lines) — go-git backend, entire file to delete
- `pkg/git/external.go` (467 lines) — external backend, becomes the only backend
- `pkg/git/service.go` — public API with `backend` interface and `WithExternalGit()` option
- `pkg/git/git_test.go` (1,623 lines) — go-git tests, entire file to delete
- `pkg/git/external_test.go` — has `openBothBackends()` and 5 cross-backend comparison tests
- `pkg/git/service_test.go` — uses `setupTestRepo()` (go-git) 27 times + 1 direct go-git call
- `cmd/ralphex/main.go:426-437` — `openGitService()` with backend selection
- `cmd/ralphex/main_test.go` — has its own `setupTestRepo()` using go-git (13 calls + 8 inline go-git operations)
- `pkg/config/config.go:55` — `GitBackend` field in Config struct
- `pkg/config/values.go` — `GitBackend` field parsing and merging
- `pkg/config/values_test.go` — 4 GitBackend-specific test functions
- `pkg/config/defaults/config:83-87` — embedded config template for `git_backend`

**Docker:** both Dockerfiles install `git` via `apk add`, so external backend works in containers. No Docker changes needed.

## Development Approach

- **testing approach**: regular (code first, then tests)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**
- **CRITICAL: update this plan file when scope changes during implementation**
- run tests after each change

## Testing Strategy

- **unit tests**: existing external_test.go tests cover the external backend thoroughly
- **service_test.go**: rewrite to use `setupExternalTestRepo`/`runGit` instead of go-git helpers
- **main_test.go**: rewrite `setupTestRepo()` and inline go-git calls to use git CLI
- **cross-backend tests**: delete (only one backend remains)
- **e2e tests**: no changes needed (they test the web UI, not git internals)

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix

## Implementation Steps

### Task 1: Rewrite service_test.go to use git CLI helpers

**Files:**
- Modify: `pkg/git/service_test.go`

service_test.go calls `setupTestRepo()` (defined in git_test.go, uses go-git) 27 times and `openRepo()` once. It also imports go-git directly for the detached HEAD test (lines 112-114).

- [x] replace all `setupTestRepo(t)` calls with `setupExternalTestRepo(t)`
- [x] replace the detached HEAD test (line 109-114) — use `runGit(t, dir, "checkout", hash)` instead of go-git worktree checkout
- [x] remove `openRepo(dir)` call (line 110) — not needed with git CLI approach
- [x] remove go-git imports (`go-git/go-git/v5`, `go-git/go-git/v5/plumbing`)
- [x] run `go test ./pkg/git/...` — must pass before next task

### Task 2: Rewrite main_test.go to use git CLI helpers

**Files:**
- Modify: `cmd/ralphex/main_test.go`

main_test.go has its own `setupTestRepo()` (line 860) using `gogit.PlainInit`, plus 13 call sites and ~8 inline `gogit.PlainOpen`/`wt.Add`/`wt.Commit` operations (lines 521, 552, 566, 580, 596, 629-636, 675-682, 719-726, 763-770).

- [x] rewrite `setupTestRepo()` helper to use `exec.Command("git", ...)` instead of go-git
- [x] replace all inline `gogit.PlainInit` calls with `git init` via exec
- [x] replace all inline `gogit.PlainOpen`/`wt.Add`/`wt.Commit` with `git add`/`git commit` via exec
- [x] remove go-git imports (`gogit "github.com/go-git/go-git/v5"`, `plumbing/object`)
- [x] run `go test ./cmd/ralphex/...` — must pass before next task

### Task 3: Delete go-git backend and tests

**Files:**
- Delete: `pkg/git/git.go`
- Delete: `pkg/git/git_test.go`

- [x] delete `pkg/git/git.go` (680 lines — `repo` type and all go-git operations)
- [x] delete `pkg/git/git_test.go` (1,623 lines — all go-git backend tests)
- [x] run `go test ./pkg/git/...` — must pass before next task

### Task 4: Remove cross-backend tests from external_test.go

**Files:**
- Modify: `pkg/git/external_test.go`

- [x] delete `openBothBackends()` helper (lines 813-822)
- [x] delete all 5 `TestCrossBackend_*` functions (lines 824-1109)
- [x] run `go test ./pkg/git/...` — must pass before next task

### Task 5: Simplify service.go — remove backend selection

**Files:**
- Modify: `pkg/git/service.go`

- [x] remove `Option` type, `serviceConfig` struct, and `WithExternalGit()` function (lines 50-64)
- [x] simplify `NewService()` — always call `newExternalBackend(path)`, remove opts parameter
- [x] update `NewService` signature: `NewService(path string, log Logger) (*Service, error)`
- [x] update `backend` interface comment (line 22) — remove go-git mention
- [x] update package comment in git.go (now in external.go or service.go) — remove go-git references
- [x] run `go test ./pkg/git/...` — must pass before next task

### Task 6: Update all NewService callers

**Files:**
- Modify: `cmd/ralphex/main.go`
- Modify: `pkg/git/service_test.go`
- Modify: `cmd/ralphex/main_test.go`

- [x] simplify `openGitService()` in main.go — remove `cfg.GitBackend` check, remove `git.WithExternalGit()`, call `git.NewService(".", colors.Info())`
- [x] update all `NewService(dir, log, ...)` calls to `NewService(dir, log)` in service_test.go
- [x] update `NewService` calls in main_test.go if they pass options
- [x] run `go test ./...` — must pass before next task

### Task 7: Remove GitBackend config field

**Files:**
- Modify: `pkg/config/config.go`
- Modify: `pkg/config/values.go`
- Modify: `pkg/config/values_test.go`
- Modify: `pkg/config/defaults/config`

- [x] remove `GitBackend` field from Config struct in config.go (line 55) and its assignment (~line 238)
- [x] remove `GitBackend` field from Values struct in values.go
- [x] remove `git_backend` key parsing in values.go (~lines 208-210)
- [x] remove `git_backend` merge logic in values.go (~lines 327-329)
- [x] delete GitBackend test functions from values_test.go (~lines 1170-1230)
- [x] remove `git_backend` section from embedded defaults config (lines 80-87)
- [x] run `go test ./...` — must pass before next task

### Task 8: Remove go-git dependencies and cleanup

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`

- [x] run `go mod tidy` to remove unused go-git dependencies
- [x] verify go-git packages are gone: `grep go-git go.mod` should return nothing
- [x] run `go test ./...` — full test suite must pass
- [x] run `make lint` — must pass

### Task 9: Update documentation

**Files:**
- Modify: `CLAUDE.md`
- Modify: `llms.txt`

- [x] update CLAUDE.md — remove go-git references, remove `git_backend` config docs, simplify git package description
- [x] update `llms.txt` — remove `git_backend` config option and go-git references
- [x] move this plan to `docs/plans/completed/`

## Technical Details

**NewService signature change:**
```go
// before
func NewService(path string, log Logger, opts ...Option) (*Service, error)

// after
func NewService(path string, log Logger) (*Service, error)
```

**Test helper migration (pkg/git/):**
- `setupTestRepo(t)` (go-git, in git_test.go) → `setupExternalTestRepo(t)` (git CLI, in external_test.go)
- `openRepo(dir)` (go-git) → not needed, use `newExternalBackend(dir)` if backend access required
- `runGit(t, dir, args...)` already exists in external_test.go for git CLI operations

**Test helper migration (cmd/ralphex/):**
- rewrite `setupTestRepo()` in main_test.go to use `exec.Command("git", ...)` pattern
- replace inline `gogit.PlainOpen`/`wt.Add`/`wt.Commit` with git CLI commands

**Detached HEAD test rewrite (service_test.go):**
```go
// before (go-git)
r, err := openRepo(dir)
wt, err := r.gitRepo.Worktree()
err = wt.Checkout(&git.CheckoutOptions{Hash: plumbing.NewHash(hash)})

// after (git CLI)
runGit(t, dir, "checkout", hash)
```

## Post-Completion

**Manual verification:**
- run ralphex on a real project (e.g., weblist) to verify git operations work
- verify `ralphex --reset` still works (config template updated)
- verify Docker build succeeds (`git` binary already installed in both Dockerfiles)
