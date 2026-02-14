# Move progress files to .ralphex/progress/

## Overview
- Move progress files from project root to `.ralphex/progress/` subdirectory
- Eliminates visual clutter and IDE search interference (GitHub issue #105)
- Breaking change for file creation — new progress files always go to `.ralphex/progress/`
- Web dashboard retains backward compat — discovers files in both old (root) and new locations
- `.ralphex/` already exists as the project-local config directory, so progress is a natural fit

## Context (from discovery)
- `progressFilename()` in `pkg/progress/progress.go:520` is the single source of truth for path construction
- `NewLogger` already calls `MkdirAll` on parent dir (line 147), so `.ralphex/progress/` creation is automatic
- `{{PROGRESS_FILE}}` template variable uses `logger.Path()` which returns `file.Name()` — auto-adjusts
- `isProgressFile()` in `pkg/web/watcher.go:237` uses `filepath.Base()` — unaffected by directory change
- Watcher already recurses into subdirs, `.ralphex` is not in `skipDirs` — picks up both locations
- File locking works on file handle, path-independent — unaffected

## Development Approach
- **testing approach**: regular (code first, then tests)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**
- run tests after each change

## Implementation Steps

### Task 1: Change progress file path construction

**Files:**
- Modify: `pkg/progress/progress.go`
- Modify: `pkg/progress/progress_test.go`

- [x] add `progressDir` constant `.ralphex/progress` at package level in `progress.go`
- [x] update `progressFilename()` to prefix all returned paths with `filepath.Join(progressDir, ...)`
- [x] update tests in `progress_test.go` for `progressFilename()` to expect `.ralphex/progress/` prefix
- [x] update any tests for `NewLogger` that check file paths
- [x] run `go test ./pkg/progress/...` — must pass before next task

### Task 2: Update gitignore pattern

**Files:**
- Modify: `cmd/ralphex/main.go`
- Modify: `cmd/ralphex/main_test.go`
- Modify: `pkg/git/service_test.go`

- [x] change `EnsureIgnored("progress*.txt", "progress-test.txt")` to `EnsureIgnored(".ralphex/progress/", ".ralphex/progress/progress-test.txt")` at lines 255 and 550
- [x] update tests in `main_test.go` that reference progress gitignore patterns
- [x] update tests in `service_test.go` that reference progress gitignore patterns
- [x] run `go test ./cmd/ralphex/... ./pkg/git/...` — must pass before next task

### Task 3: Verify web dashboard backward compat (no code changes needed)

The watcher (`pkg/web/watcher.go`) already recurses into subdirs via `DiscoverRecursive` and `isProgressFile()` uses `filepath.Base()`, so it naturally picks up files in both old (root) and new (`.ralphex/progress/`) locations without code changes.

`Discover(dir)` is always called with the specific directory containing the file (via `filepath.Dir(path)`), so it correctly globs `progress-*.txt` in whichever directory the file lives in.

**Files:**
- Modify: `pkg/web/session_manager_test.go`

- [x] add test for discovering files in `.ralphex/progress/` subdirectory
- [x] add test for discovering files in both old (root) and new locations simultaneously
- [x] run `go test ./pkg/web/...` — must pass before next task

### Task 4: Update project .gitignore and documentation

**Files:**
- Modify: `.gitignore`
- Modify: `CLAUDE.md`
- Modify: `README.md`

- [x] replace `progress*.txt` with `.ralphex/progress/` in `.gitignore`
- [x] keep e2e testdata exceptions (`!e2e/testdata/progress-*.txt`)
- [x] update CLAUDE.md references to progress file location
- [x] update README.md if it mentions progress file location
- [x] update any `tail -f progress-*.txt` examples in docs to use `.ralphex/progress/`

### Task 5: Verify acceptance criteria

- [x] run full unit test suite: `go test ./...`
- [x] run linter: `golangci-lint run`
- [x] verify `.ralphex/progress/` directory is created on execution
- [x] verify progress files appear in `.ralphex/progress/` not project root
- [x] verify web dashboard discovers files in both old and new locations

### Task 6: [Final] Update documentation

- [x] respond to GitHub issue #105 with the change
- [x] move this plan to `docs/plans/completed/`

## Technical Details

**Path construction change:**
```
before: progress-my-plan.txt
after:  .ralphex/progress/progress-my-plan.txt
```

**Gitignore change:**
```
before: progress*.txt
after:  .ralphex/progress/
```

**Discovery (backward compat):**
```
before: <dir>/progress-*.txt
after:  <dir>/progress-*.txt + <dir>/.ralphex/progress/progress-*.txt
```

**Watcher:** no changes needed — already recurses into `.ralphex/progress/` and `isProgressFile()` matches by `filepath.Base()`

## Post-Completion

**Manual verification:**
- run e2e test with toy project to verify progress files land in `.ralphex/progress/`
- verify `--serve` web dashboard picks up files from both old and new locations
- verify `tail -f .ralphex/progress/progress-*.txt` works for monitoring
