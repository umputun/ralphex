# Kill orphaned child processes on normal exit

## Overview
When a claude/codex/custom session exits normally, `watchForCancel` takes the `pg.done` branch
and exits without killing the process group. If the child process leaves descendants behind
(node subagents, MCP servers, tools), they become orphans reparented to PID 1 and accumulate
across iterations, eventually exhausting memory.

Fix (Unix/macOS/Linux): always kill the process group after `cmd.Wait()` returns, using a
`sync.Once` guard to prevent races with the cancellation goroutine. Also optimize
`killProcessGroup` to early-return when SIGTERM gets ESRCH (group already gone) to avoid a
100ms sleep on every normal exit. Windows gets a best-effort direct-process kill (no process
groups on Windows).

## Context
- Bug location: `pkg/executor/procgroup_unix.go:53-60` — `watchForCancel` only kills on cancel
- Affects all executors: claude (`executor.go:92`), codex (`codex.go:57`), custom (`custom.go:46`)
- Existing test gap: `procgroup_test.go` only tests cancellation kill, not normal-exit cleanup
- Confirmed by codex (GPT-5) analysis: bug is real, fix is safe (ESRCH handles empty groups)
- `killOnce` is a **separate** `sync.Once` from the existing `once` field — `once` guards
  `cmd.Wait()` idempotency, `killOnce` guards `killProcessGroup()` idempotency

## Development Approach
- Testing approach: Regular (code first, then tests)
- Complete each task fully before moving to the next
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Add post-Wait process group kill with sync.Once guard

**Files:**
- Modify: `pkg/executor/procgroup_unix.go`

- [ ] add `killOnce sync.Once` field to `processGroupCleanup` struct (separate from existing `once` which guards Wait idempotency)
- [ ] optimize `killProcessGroup()`: early-return when SIGTERM gets `ESRCH` (group already gone), skip the 100ms sleep and SIGKILL. This avoids adding 100ms overhead to every normal-exit iteration
- [ ] wrap `killProcessGroup()` call in `watchForCancel` with `pg.killOnce.Do()`
- [ ] add `pg.killOnce.Do(pg.killProcessGroup)` in `Wait()` inside the existing `pg.once.Do` closure, after `close(pg.done)`. This catches orphaned descendants on normal exit
- [ ] run `go test ./pkg/executor/... -race` — must pass before task 2

### Task 2: Update Windows stub

**Files:**
- Modify: `pkg/executor/procgroup_windows.go`

- [ ] add `pg.killOnce.Do(pg.killProcess)` in Windows `Wait()` after `close(pg.done)`, matching the Unix fix — kills the direct process on normal exit (Windows doesn't have process groups, so this is best-effort for the direct child only)
- [ ] add `killOnce sync.Once` field to Windows `processGroupCleanup` struct
- [ ] run `GOOS=windows GOARCH=amd64 go build ./...` — must compile

### Task 3: Add normal-exit regression test

**Files:**
- Modify: `pkg/executor/procgroup_test.go`

- [ ] add `TestExecClaudeRunner_KillsOrphansOnNormalExit` test: spawn bash that starts `sleep 300 &`, prints child PID, then exits immediately. After `wait()` returns, poll for child PID death. Reuse existing `readChildPID` and `processExists` helpers
- [ ] add comment explaining that `Setsid: true` + background `sleep` keeps the child in the same process group as the parent, so `-pgid` kill reaches it
- [ ] run `go test ./pkg/executor/... -race` — must pass before task 4

### Task 4: Verify acceptance criteria

- [ ] run `make test` — all tests pass
- [ ] run `make lint` — no linter issues
- [ ] run `make fmt` — code is formatted
- [ ] `GOOS=windows GOARCH=amd64 go build ./...` — Windows cross-compile succeeds

### Task 5: Move plan to completed

- [ ] move this plan to `docs/plans/completed/`
