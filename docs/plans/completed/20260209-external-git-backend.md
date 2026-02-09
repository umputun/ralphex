# External Git Backend

## Overview

Add a configurable git backend option (`git_backend = internal | external`) to use the real `git` CLI instead of the go-git library for all git operations. This addresses a class of problems where go-git behaves differently from native git (false dirty detection, symlink handling, gitignore edge cases — issues #28, #77).

The existing go-git backend remains the default. Setting `git_backend = external` switches to shelling out to the `git` binary for every operation.

## Context

- `pkg/git/service.go` — public `Service` type, delegates to internal `repo`
- `pkg/git/git.go` — internal `repo` type with all go-git operations (~690 lines)
- `pkg/config/config.go` — `Config` struct, `Values` struct, INI loading
- `pkg/config/defaults/config` — embedded default config
- `cmd/ralphex/main.go:166` — production call site: `git.NewService(".", colors.Info())`
- `cmd/ralphex/main_test.go` — ~15 test call sites of `git.NewService`
- Known go-git issues: #28 (false positives on symlinks/gitignored files), #77 (false dirty worktree)

## Development Approach

- **testing approach**: regular (code first, then tests)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**
- **CRITICAL: update this plan file when scope changes during implementation**
- run tests after each change
- maintain backward compatibility — default behavior unchanged

## Testing Strategy

- **unit tests**: required for every task
- existing `git_test.go` and `service_test.go` tests must continue passing unchanged
- `external_test.go` uses real temp repos with real `git` binary (same approach as existing tests)
- cross-backend comparison test: run identical operations through both backends, assert same results

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope

## Implementation Steps

### Task 1: Extract backend interface and rename repo

Refactor the internal `repo` type into a `backend` interface so `Service` can work with either implementation. Pure refactor — no behavior change.

**Files:**
- Modify: `pkg/git/service.go`
- Modify: `pkg/git/git.go`

- [x] define `backend` interface in `service.go` with all methods currently called on `repo` by `Service`:
  - `Root() string`
  - `headHash() (string, error)`
  - `HasCommits() (bool, error)`
  - `CurrentBranch() (string, error)`
  - `IsMainBranch() (bool, error)`
  - `GetDefaultBranch() string`
  - `BranchExists(name string) bool`
  - `CreateBranch(name string) error`
  - `CheckoutBranch(name string) error`
  - `IsDirty() (bool, error)`
  - `FileHasChanges(path string) (bool, error)`
  - `HasChangesOtherThan(path string) (bool, error)`
  - `IsIgnored(path string) (bool, error)`
  - `Add(path string) error`
  - `MoveFile(src, dst string) error`
  - `Commit(msg string) error`
  - `CreateInitialCommit(msg string) error`
  - `diffStats(baseBranch string) (DiffStats, error)`
  - note: helper methods (`toRelative`, `normalizeToRelative`, `fileHasChanges`, `getAuthor`, `resolveToCommit`, etc.) remain internal to each backend implementation and are not part of the interface
- [x] move `DiffStats` type to `service.go` alongside the interface (shared by both backends)
- [x] change `Service.repo` field from `*repo` to `backend`
- [x] verify `repo` type satisfies `backend` interface (compile-time check)
- [x] run `go test ./pkg/git/...` — must pass with no changes to tests
- [x] run `go test ./...` — full suite must pass

### Task 2: Implement externalBackend

Create `externalBackend` that shells out to the `git` CLI for all operations.

**Files:**
- Create: `pkg/git/external.go`
- Create: `pkg/git/external_test.go`

- [x] create `externalBackend` struct with `path string` field (repo root)
- [x] add `newExternalBackend(path string) (*externalBackend, error)` constructor that validates path via `git rev-parse --show-toplevel`
- [x] add `run(args ...string) (string, error)` helper method using `exec.Command` with `Dir` set to `path`
- [x] implement all `backend` interface methods:
  - `Root()` — return stored path
  - `headHash()` — `git rev-parse HEAD`
  - `HasCommits()` — `git rev-parse HEAD`, check exit code
  - `CurrentBranch()` — `git symbolic-ref --short HEAD`, exit 1 = detached
  - `IsMainBranch()` — `CurrentBranch()` then string check
  - `GetDefaultBranch()` — `git symbolic-ref refs/remotes/origin/HEAD`, fallback to `git show-ref`
  - `BranchExists(name)` — `git show-ref --verify refs/heads/<name>`
  - `CreateBranch(name)` — `git checkout -b <name>`
  - `CheckoutBranch(name)` — `git checkout <name>`
  - `IsDirty()` — `git status --porcelain`, any output = dirty
  - `FileHasChanges(path)` — `git status --porcelain -- <path>`
  - `HasChangesOtherThan(path)` — `git status --porcelain`, filter out path
  - `IsIgnored(path)` — `git check-ignore -q <path>`, exit 0 = ignored
  - `Add(path)` — `git add <path>`
  - `MoveFile(src, dst)` — `git mv <src> <dst>`
  - `Commit(msg)` — `git commit -m <msg>`
  - `CreateInitialCommit(msg)` — `git add -A && git commit -m <msg>`
  - `diffStats(baseBranch)` — `git diff --numstat <base>...HEAD`, parse output
- [x] write tests in `external_test.go` for each method using real temp repos (success cases)
- [x] write tests for error/edge cases (no commits, detached HEAD, missing branch, empty status)
- [x] run `go test ./pkg/git/...` — must pass

### Task 3: Wire up config option

Add `git_backend` config field and pass it through to `NewService`.

**Files:**
- Modify: `pkg/config/config.go`
- Modify: `pkg/config/values.go`
- Modify: `pkg/config/defaults/config`
- Modify: `pkg/git/service.go`
- Modify: `cmd/ralphex/main.go`

- [x] add `GitBackend string` field to `Config` struct in `config.go`
- [x] add `GitBackend string` field to `Values` struct in `values.go`
- [x] add parsing in `parseValuesFromBytes()`: read `git_backend` key
- [x] add merge logic in `mergeFrom()`: override if non-empty
- [x] add assembly in `loadConfigFromDirs()`: copy `GitBackend` from values to config
- [x] add default in `pkg/config/defaults/config`: `# git_backend = internal` (commented out)
- [x] add functional option to `NewService`: `NewService(path string, log Logger, opts ...Option)` with `WithExternalGit()` option. This avoids breaking 15+ existing call sites with a bare bool parameter. Existing callers remain unchanged (default = internal backend)
- [x] inside `NewService`, pick backend based on option
- [x] update caller in `cmd/ralphex/main.go`: pass `git.WithExternalGit()` when `cfg.GitBackend == "external"`
- [x] add test case in `values_test.go` for `git_backend` parsing and merge behavior
- [x] run `go test ./...` — full suite must pass
- [x] run linter: `golangci-lint run`

### Task 4: Add cross-backend comparison test

Add comparison tests into `external_test.go` (one test file per source file rule) that run the same operations through both backends and assert identical results.

**Files:**
- Modify: `pkg/git/external_test.go`

- [x] add test function that initializes a temp repo with known state (files, branches, commits)
- [x] run each `backend` method through both `repo` (go-git) and `externalBackend` (git CLI)
- [x] assert identical results for: `Root`, `headHash`, `HasCommits`, `CurrentBranch`, `IsMainBranch`, `GetDefaultBranch`, `BranchExists`, `IsDirty`, `FileHasChanges`, `HasChangesOtherThan`, `IsIgnored`, `diffStats`
- [x] test with multiple repo states: clean, dirty, untracked files, gitignored files
- [x] run `go test ./pkg/git/...` — must pass

### Task 5: Verify acceptance criteria

- [x] verify default behavior unchanged (internal backend, no config needed)
- [x] verify `git_backend = external` works end-to-end
- [x] verify all edge cases handled (no commits, detached HEAD, gitignored files)
- [x] run full unit test suite: `go test ./...`
- [x] run linter: `golangci-lint run`
- [x] verify test coverage for new code meets 80%+

### Task 6: [Final] Update documentation

- [x] update CLAUDE.md — mention `git_backend` config option and `externalBackend`
- [x] update README.md if git backend option is user-facing enough to document
- [x] move this plan to `docs/plans/completed/`

## Technical Details

**Config values**: `git_backend` accepts `internal` (default, go-git library) or `external` (git CLI). Any other value treated as `internal`.

**externalBackend.run() helper**: wraps `exec.Command`, sets `Dir` to repo root, returns trimmed stdout. Stderr included in error message on failure.

**git status --porcelain parsing**: each line is `XY path` where X=staging, Y=worktree. For `HasChangesOtherThan`, filter lines whose path doesn't match the excluded file.

**git diff --numstat parsing**: tab-separated `additions\tdeletions\tfilename` per line. Sum additions/deletions, count lines for file count.

**Author handling**: git CLI uses `.gitconfig` automatically — no need to replicate `getAuthor()` logic.

**CreateInitialCommit equivalence**: `git add -A` natively respects `.gitignore`, matching the internal backend's per-file `IsIgnored()` check. No special handling needed.

**Git binary validation**: the `newExternalBackend()` constructor calls `git rev-parse --show-toplevel`, which implicitly validates the git CLI is available. If `git` is not on PATH, this fails with a clear error.

**NewService API**: uses functional options pattern (`WithExternalGit()`) to avoid breaking existing callers. Existing `NewService(path, log)` calls continue working unchanged — only `cmd/ralphex/main.go` conditionally passes the option based on config.

## Post-Completion

**Manual verification:**
- test with a real project that has symlinks (reproduces #28)
- test with a project that triggers #77 false dirty detection
- verify Docker images still work (git binary available in containers)
