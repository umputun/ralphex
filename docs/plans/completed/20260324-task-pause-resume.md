# Task Pause+Resume via SIGQUIT

## Overview

Extend existing Ctrl+\ (SIGQUIT) break signal from external review loop to task execution loop. When the user presses Ctrl+\ during a task iteration, the current Claude session is canceled, ralphex pauses with "press Enter to continue, Ctrl+C to abort", and on Enter the same task re-runs with a fresh session that re-reads the plan file. This lets users edit the plan mid-execution and have changes picked up reliably, without adding new dependencies or interactive stdin complexity.

Related: PR #246 review, issue #247 comment.

## Context

- `cmd/ralphex/break_unix.go` — current SIGQUIT listener, closes channel once (one-shot)
- `cmd/ralphex/break_windows.go` — returns nil (SIGQUIT not available, no change needed)
- `cmd/ralphex/main.go` — sets `r.SetBreakCh()` from break signal
- `pkg/processor/runner.go` — Runner struct, `breakCh` field, `breakContext()`, `isManualBreak()`, `runTaskPhase()`, `runExternalReviewLoop()`
- Break signal currently only used in `runExternalReviewLoop` via `breakContext()`
- Task loop (`runTaskPhase`) has no break support; prompt is built once before the loop

## Solution Overview

1. Change break signal from close-once to send-on-channel (repeatable)
2. Remove `isManualBreak` — detect break by comparing child vs parent context errors
3. Add `pauseHandler` callback to Runner for "press Enter" UX
4. Add break support to `runTaskPhase` with per-iteration `breakContext`

No new dependencies. ~100-150 lines of code changes.

## Technical Details

- Break channel: `chan struct{}` with buffer size 1, sends one value per SIGQUIT. Goroutine loops on signal channel: `for { <-sig; select { case ch <- struct{}{}: default: } }` — no `signal.Stop` between iterations
- `breakContext`: drains one value from channel (instead of waiting for close), cancels derived context
- Break detection: remove `isManualBreak()` entirely. Instead, detect break by checking `loopCtx.Err() != nil && parentCtx.Err() == nil` — if child context was canceled but parent is still alive, it was a manual break. This eliminates the double-drain race where `breakContext` consumes the channel value and `isManualBreak` finds nothing
- External review loop: unchanged behavior (one break = exit loop), but uses new send semantics and new break detection
- Task loop: `breakContext` created per iteration. Cleanup ordering is critical: create `loopCtx` → run iteration → check `loopCtx.Err() != nil && ctx.Err() == nil` → call `loopCancel()`. Must check error before cancel, otherwise cancel self-triggers the check. Must call cancel on every path to avoid goroutine leak from `breakContext`'s internal goroutine
- Sticky signal drain: before creating a new `breakContext` after pause+resume, drain any pending value from `breakCh` (non-blocking select). A SIGQUIT received during the pause prompt (when no `breakContext` is active) stays buffered and would immediately cancel the next iteration without draining
- Abort semantics: when pause handler returns false (or is nil), `runTaskPhase` must return a sentinel error (e.g., `ErrUserAborted`) — NOT nil. `nil` from `runTaskPhase` means "all tasks completed", which would cause `runFull` to continue into review/finalize on a partially executed plan. Mode entrypoints (`runFull`, `runTasksOnly`) should handle `ErrUserAborted` as a clean non-error exit
- `pauseHandler`: `func(ctx context.Context) bool` callback (context-aware). The handler must read stdin in a goroutine and `select` on `ctx.Done()` to respond to Ctrl+C (SIGINT). Plain `bufio.Scanner(os.Stdin).Scan()` blocks regardless of context cancellation — the only escape would be the 5s force-exit watcher
- Windows: no change, `startBreakSignal()` returns nil, pause feature not available (same as current break)
- Docker/piped stdin: stdin read returns false immediately on EOF, so pause = abort (safe default)

## Development Approach

- **testing approach**: regular (code first, then tests)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Testing Strategy

- unit tests: test send-on-channel break signal, test pauseHandler integration in runner, test re-run-same-task behavior
- existing break tests in `runner_test.go` must be adapted for new channel semantics
- abort semantics: test that `ErrUserAborted` from task phase stops full-mode pipeline before review
- sticky signal: test that SIGQUIT during pause does not auto-cancel the next resumed iteration
- Ctrl+C during pause: test that context-aware handler returns promptly on context cancellation

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix

## Implementation Steps

### Task 1: Change break signal to send-on-channel

**Files:**
- Modify: `cmd/ralphex/break_unix.go`
- Modify: `cmd/ralphex/break_windows.go`

- [x] change `startBreakSignal()` return type from `<-chan struct{}` to `chan struct{}`
- [x] replace close-channel goroutine with a loop: `for { <-sig; select { case ch <- struct{}{}: default: } }` — no `signal.Stop` between iterations
- [x] use buffered channel (size 1) to avoid blocking if no reader is ready
- [x] update `break_windows.go` return type to match (still returns nil)
- [x] write test in `cmd/ralphex/break_unix_test.go` for repeated SIGQUIT sends (verify multiple values can be consumed)
- [x] run tests — must pass before next task

### Task 2: Adapt Runner break mechanics and add abort sentinel

**Files:**
- Modify: `pkg/processor/runner.go`
- Modify: `pkg/processor/runner_test.go`

- [x] update `breakContext()` to drain one value from channel instead of waiting for close
- [x] remove `isManualBreak()` — replace all call sites with inline check: `loopCtx.Err() != nil && parentCtx.Err() == nil` (child canceled but parent alive = manual break). This eliminates the double-drain race where `breakContext` consumes the channel value
- [x] update `runExternalReviewLoop` to use new break detection pattern instead of `isManualBreak`
- [x] add `ErrUserAborted` sentinel error. `runTaskPhase` returns it on pause abort; `runFull`/`runTasksOnly` handle it as clean non-error exit (don't fall through to review/finalize)
- [x] add `drainBreakCh()` helper — non-blocking select drain of one pending value from `breakCh`. Called before creating a new `breakContext` after pause+resume to prevent sticky signals from immediately canceling the next iteration
- [x] update existing break-related tests in `runner_test.go` to use send instead of close and new detection pattern
- [x] write test: `ErrUserAborted` from task phase does not trigger review in full mode
- [x] run tests — must pass before next task

### Task 3: Add pauseHandler and task loop break support

**Files:**
- Modify: `pkg/processor/runner.go`
- Modify: `pkg/processor/runner_test.go`

- [x] add `pauseHandler func(ctx context.Context) bool` field to Runner struct (context-aware signature)
- [x] add `SetPauseHandler(fn func(ctx context.Context) bool)` setter method
- [x] in `runTaskPhase`, wrap `runWithLimitRetry` call with per-iteration `breakContext`. Cleanup ordering: create `loopCtx` → run iteration → check `loopCtx.Err() != nil && ctx.Err() == nil` → call `loopCancel()`. Must check error before cancel to avoid false positive. Must cancel on every path to avoid goroutine leak
- [x] on manual break detected: call `drainBreakCh()` to clear any sticky signal, then call `pauseHandler(ctx)`. If handler returns true, decrement `i` to preserve iteration budget (task identity comes from re-reading the plan file, not loop counter) and `continue`. If handler returns false or is nil, return `ErrUserAborted`
- [x] write test: break during task iteration triggers pause, handler returns true → same iteration budget preserved, loop continues
- [x] write test: break during task iteration, handler returns false → returns `ErrUserAborted`
- [x] write test: break during task iteration, no handler set → returns `ErrUserAborted` (backward compat)
- [x] write test: SIGQUIT during pause prompt does not auto-cancel the resumed iteration (sticky signal drain)
- [x] run tests — must pass before next task

### Task 4: Wire pause handler in main

**Files:**
- Modify: `cmd/ralphex/main.go`

- [x] after `r.SetBreakCh(breakCh)`, add `r.SetPauseHandler(...)` with context-aware stdin handler
- [x] handler prints `"\nsession interrupted. press Enter to continue, Ctrl+C to abort\n"` to stdout
- [x] handler reads stdin in a goroutine and selects on `ctx.Done()` — plain `bufio.Scanner.Scan()` blocks regardless of context, so must use goroutine+select to respond to Ctrl+C (SIGINT) promptly instead of relying on the 5s force-exit watcher
- [x] note: handler is a thin stdin wrapper, tested indirectly via Task 3's mock-based pauseHandler tests and Task 5's acceptance verification
- [x] run tests — must pass before next task

### Task 5: Verify acceptance criteria

- [x] verify Ctrl+\ during task iteration cancels claude session and pauses
- [x] verify pressing Enter re-runs same task with fresh session
- [x] verify plan file changes are picked up after pause+resume
- [x] verify Ctrl+C during pause aborts cleanly
- [x] verify external review break still works as before
- [x] verify Windows builds: `GOOS=windows GOARCH=amd64 go build ./...`
- [x] run full test suite: `go test ./...`
- [x] run linter: `golangci-lint run --max-issues-per-linter=0 --max-same-issues=0`

### Task 6: [Final] Update CLAUDE.md and llms.txt with pause+resume docs

- [x] update CLAUDE.md — add pause+resume to Key Patterns section
- [x] update llms.txt — add Ctrl+\ task pause to usage docs
- [x] update README.md — add "steering mid-run" section (or add to existing tips/FAQ) explaining Ctrl+\ pause, plan editing, and resume workflow
- [x] move this plan to `docs/plans/completed/`
