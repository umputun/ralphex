# Detect completed progress files and start fresh

## Overview

When a progress file already has a "Completed:" footer (written by `Close()`), it means the previous run finished successfully. A new run reusing the same plan name (e.g., `bug-fix.md`) should start fresh — truncate and write a new header — instead of blindly appending unrelated content.

**Problem:** PR #130 changed progress files to append on restart. But if two unrelated plans share a filename (e.g., both called `bug-fix.md`), the second run appends to the first run's completed progress file, mixing unrelated content.

**Fix:** After acquiring the file lock, check whether the existing file has a completion footer. If yes, truncate and start fresh. If no (crashed/interrupted run), append with restart separator as before.

## Context

- Core file: `pkg/progress/progress.go` — `NewLogger()` (line 140), `Close()` writes footer (line 511)
- Completion footer format: `Completed: 2006-01-02 15:04:05 (elapsed)` — written by `Close()` at line 517
- File opened with `O_APPEND|O_CREATE|O_WRONLY` at line 155
- Lock acquired before stat to prevent TOCTOU race (lines 162-168)
- `f.Truncate(0)` works with O_APPEND — resets file size, next write goes to offset 0

## Development Approach

- **testing approach**: TDD — add test for the new behavior first
- single function change in `NewLogger` + one helper function
- no rotation, no renaming — just truncate in place

## Implementation Steps

### Task 1: Add completion detection and truncate-on-completed logic

**Files:**
- Modify: `pkg/progress/progress.go`
- Modify: `pkg/progress/progress_test.go`

- [x] add `isProgressCompleted(path string) bool` method — opens file read-only, reads last ~256 bytes, checks for `"Completed:"` substring, closes
- [x] in `NewLogger`, after lock + stat with `fi.Size() > 0`: call `isProgressCompleted(progressPath)`
  - if true: `f.Truncate(0)` then fall through to "write full header" path (set `restart = false`)
  - if false: existing restart separator behavior (set `restart = true`)
- [x] write test `TestNewLogger_FreshStartAfterCompleted` — create logger, write content, close (writes footer), create second logger with same config, verify: old content gone, no restart separator, fresh header written
- [x] write test `TestNewLogger_AppendOnRestart` already exists — verify it still passes (interrupted run without footer → append behavior preserved)
- [x] write test `TestIsProgressCompleted` — file with footer returns true, file without footer returns false, empty file returns false, nonexistent file returns false
- [x] run `go test ./pkg/progress/` — must pass

### Task 2: Verify acceptance criteria

- [x] run full test suite: `go test ./...`
- [x] run linter: `golangci-lint run`
- [x] run formatter: `~/.claude/format.sh`

### Task 3: Update documentation

- [x] update CLAUDE.md if needed (progress file behavior description)
- [x] move this plan to `docs/plans/completed/`

## Technical Details

**Decision logic in `NewLogger` after lock acquisition:**

```
lock acquired → stat file
  size == 0 → write full header (existing)
  size > 0 AND isProgressCompleted → truncate(0), write full header
  size > 0 AND NOT isProgressCompleted → write restart separator (existing)
```

**`isProgressCompleted` implementation:**
- open file read-only (advisory flock doesn't block reads)
- seek to max(0, fileSize - 256)
- read remainder, check `strings.Contains(chunk, "Completed:")`
- close file, return result

**Why truncation is safe for the web dashboard:**
- Tailer only runs for active sessions (file locked)
- If completion footer exists, lock was released → tailer already stopped
- `refreshLoop` (5s interval) stops tailers for unlocked files
- Race window is negligible (new run starts long after previous one completes)
