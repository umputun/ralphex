# Add session_timeout Config Option

## Overview
Add a per-claude-session timeout as a safety net for hanging sessions. When the agent runs a blocking
operation (e.g., starts a server in tests that never terminates), the session currently hangs
indefinitely. A configurable timeout kills the session and lets ralphex move to the next iteration.

Related: #224

## Context
- `ClaudeExecutor.Run()` in `pkg/executor/executor.go` accepts a context but no timeout is applied
- All claude calls go through `runWithLimitRetry()` in `pkg/processor/runner.go`
- Codex has `codex_timeout_ms` but it's passed as a CLI arg to the codex binary, not a Go context timeout
- The `wait_on_limit` config uses duration strings (e.g., "1h", "30m") - same pattern for `session_timeout`
- Config uses `*Set` flags to distinguish explicit zero from "not set" in merge logic
- `isManualBreak` pattern (runner.go:760) shows how to distinguish child context cancellation from parent

## Solution Overview
- Add `session_timeout` config option (duration string, default "" = disabled)
- Apply timeout in the **runner layer** (not inside executor): `runWithLimitRetry` creates a child
  context with `context.WithTimeout` before each `claude.Run()` call
- On timeout: check if parent context is still alive (same pattern as `isManualBreak`). If parent is
  alive but child timed out, it's a session timeout, not a cancellation
- Process group is killed via existing `newProcessGroupCleanup`, session moves to next iteration
- Log a warning to progress file so the next iteration's agent can adjust
- Use `time.Duration` consistently throughout (follow `WaitOnLimit` pattern, not `CodexTimeoutMs`)
- Claude-only; codex and custom executors are not affected

### Timeout behavior per phase
- **Task phase**: session timeout counts as a failed iteration, task loop continues to next iteration
  (counts against `max_iterations`). No special retry logic needed
- **Review phases** (first/second claude review): session timeout is treated as a non-completing review,
  review loop continues to next iteration
- **External review eval** (claude evaluating codex findings): session timeout, eval loop continues
- **Plan creation**: session timeout, plan creation loop continues
- **Finalize**: session timeout is logged but finalize is best-effort anyway, not retried

## Development Approach
- Testing approach: Regular (code first, then tests)
- Complete each task fully before moving to the next
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Add session_timeout to config layer

**Files:**
- Modify: `pkg/config/values.go`
- Modify: `pkg/config/config.go`
- Modify: `pkg/config/defaults/config`
- Modify: `pkg/config/values_test.go`
- Modify: `pkg/config/config_test.go`

- [x] add `SessionTimeout time.Duration` and `SessionTimeoutSet bool` to `Values` struct in `values.go`
- [x] add `parseSessionTimeout` method following `parseWaitOnLimit` pattern (parse duration string)
- [x] add parsing call in `parseValuesFromBytes` for `session_timeout` key
- [x] add merge logic in `mergeFrom` following `WaitOnLimit`/`WaitOnLimitSet` pattern
- [x] add `SessionTimeout time.Duration` to `Config` struct in `config.go`
- [x] add assembly in `loadConfigFromDirs` to copy `SessionTimeout` from values to config
- [x] add `session_timeout` entry to embedded defaults config (commented out, default disabled)
- [x] write tests for parsing `session_timeout` from config bytes (valid durations, empty, invalid)
- [x] write tests for merge behavior (explicit zero overrides inherited value)
- [x] run `go test ./pkg/config/...` - must pass before task 2

### Task 2: Add CLI flag for session-timeout

**Files:**
- Modify: `cmd/ralphex/main.go`
- Modify: `cmd/ralphex/main_test.go`

- [x] add `SessionTimeout string` CLI flag to `opts` struct with `long:"session-timeout"` and `default:""`
- [x] add override logic in `applyCLIOverrides` (parse duration string, set config field)
- [x] write test in `main_test.go` following `TestWaitFlag` pattern
- [x] run `go test ./...` - must pass before task 3

### Task 3: Apply timeout in runner layer

**Files:**
- Modify: `pkg/processor/runner.go`
- Modify: `pkg/processor/runner_test.go`

- [x] in `runWithLimitRetry`, if `r.cfg.AppConfig.SessionTimeout > 0`, wrap ctx with `context.WithTimeout` before each `run()` call
- [x] after `run()` returns, check: if child context timed out but parent context is alive, it's a session timeout
- [x] on session timeout: log warning to progress file with timeout duration (e.g., "session timed out after 30m, the agent may have started a blocking operation")
- [x] return the result as-is (callers treat it as non-completing, loop continues naturally)
- [x] write test: mock executor that blocks forever, verify timeout fires and warning is logged
- [x] write test: verify timeout=0 does not add deadline to context
- [x] write test: verify parent context cancellation is not misidentified as session timeout
- [x] run `go test ./pkg/processor/...` - must pass before task 4

### Task 4: Verify acceptance criteria

- [x] verify timeout=0 (default) does not change behavior (backward compat)
- [x] verify timeout > 0 kills hanging claude session after deadline
- [x] verify session timeout in task phase continues to next iteration (not abort)
- [x] verify session timeout in review phase continues review loop (not abort)
- [x] verify `runWithLimitRetry` does not retry on session timeout (only on LimitPatternError)
- [x] run full test suite: `go test ./...`
- [x] run linter: `golangci-lint run --max-issues-per-linter=0 --max-same-issues=0`
- [x] verify test coverage meets 80%+

### Task 5: Update documentation

- [x] update CLAUDE.md with `session_timeout` config option description
- [x] update README.md usage section if config options are documented there
- [x] update `llms.txt` with `session_timeout` in customization section
- [x] update `docs/custom-providers.md` line 331 (currently says "ralphex doesn't impose a timeout on claude sessions")
- [x] move this plan to `docs/plans/completed/`
