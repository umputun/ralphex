# Idle Timeout for Claude Sessions

## Overview

Add `--idle-timeout` flag that kills Claude sessions when no output is received for a specified duration. Addresses #248 where Claude's process hangs after completing work (no output, no exit). Unlike `--session-timeout` (fixed wall-clock limit), idle timeout resets on each output line and only fires when the session goes silent.

## Context

- `pkg/executor/executor.go` — `ClaudeExecutor` struct, `Run()`, `parseStream()`, `readLines()` callback
- `pkg/config/values.go` — `Values` struct with `SessionTimeout`/`SessionTimeoutSet` pattern
- `pkg/processor/runner.go` — `New()` sets executor fields from config, `runWithSessionTimeout()` wraps calls
- `cmd/ralphex/main.go` — CLI flags, validation, config assembly
- `pkg/config/defaults/config` — embedded default config file

## Solution Overview

Self-contained in the executor layer. `ClaudeExecutor.IdleTimeout` field controls a `time.AfterFunc` timer created in `Run()`. A `touch` closure captures the timer and is called at the top of the `readLines` handler to reset it on each line. `parseStream` signature stays unchanged — no timer plumbing through parser code. When the timer fires, it cancels a derived context, which triggers process group kill via `watchForCancel`, closing the pipe and unblocking `readLines`.

Coexists with `--session-timeout`: session timeout is the outer hard wall-clock limit (applied in Runner), idle timeout is the inner "no activity" trigger (applied in executor). Both independent, both disabled by default.

## Technical Details

- `ClaudeExecutor.IdleTimeout time.Duration` — new field, zero = disabled
- In `Run()`: if `IdleTimeout > 0`, derive `idleCtx, idleCancel := context.WithCancel(ctx)`, create `time.AfterFunc(IdleTimeout, idleCancel)`, define `touch := func() { timer.Reset(e.IdleTimeout) }`. Pass `idleCtx` to `runner.Run()` so process group watches it. Call `touch()` at the top of the `readLines` handler inside `parseStream` — captured via closure, no signature change to `parseStream`
- Timer fires → cancel → `watchForCancel` kills process group → pipe closes → `ReadString` unblocks → `readLines` returns context error
- Config: `idle_timeout` in INI config file, parsed same as `session_timeout` (duration string)
- CLI: `--idle-timeout` flag (e.g., `--idle-timeout 5m`), same pattern as `--session-timeout`

## Development Approach

- **testing approach**: regular (code first, then tests)
- complete each task fully before moving to the next
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Testing Strategy

- unit tests: test idle timer fires when no output, test timer resets on output, test coexistence with session timeout
- existing executor tests must continue to pass unchanged

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix

## Implementation Steps

### Task 1: Add IdleTimeout to ClaudeExecutor with closure-based timer in Run

**Files:**
- Modify: `pkg/executor/executor.go`
- Modify: `pkg/executor/executor_test.go`

- [x] add `IdleTimeout time.Duration` field to `ClaudeExecutor` struct
- [x] in `Run()`, before `runner.Run()`: if `IdleTimeout > 0`, create `idleCtx, idleCancel := context.WithCancel(ctx)`, start `timer := time.AfterFunc(e.IdleTimeout, idleCancel)`, defer `timer.Stop()`, define `touch := func() { timer.Reset(e.IdleTimeout) }`. Pass `idleCtx` to `runner.Run()`. If idle timeout not configured, `touch` is a no-op: `touch := func() {}`
- [x] in `parseStream`, call `touch()` at the top of the `readLines` handler (before empty-line check), so all pipe activity resets the timer. `parseStream` signature stays unchanged — `touch` is captured via closure from `Run()`
- [x] handle idle timeout in `Run()`'s error path after `wait()`: check `idleCtx.Err() != nil && ctx.Err() == nil` — if true, it's an idle timeout (not a real error). Clear the error and return result with signal (same pattern as `runWithSessionTimeout`)
- [x] write test: idle timeout fires when executor produces no output after initial output (mock runner that blocks after sending one line)
- [x] write test: idle timeout does NOT fire when output is continuous (mock runner that sends lines within the timeout window)
- [x] write test: idle timeout disabled when IdleTimeout is zero (default behavior unchanged)
- [x] run `go test ./pkg/executor/...` — must pass before next task

### Task 2: Add idle_timeout to config, CLI, and wire to executor

**Files:**
- Modify: `pkg/config/values.go`
- Modify: `pkg/config/config.go`
- Modify: `pkg/config/config_test.go`
- Modify: `pkg/config/values_test.go`
- Modify: `cmd/ralphex/main.go`
- Modify: `pkg/processor/runner.go`
- Modify: `pkg/config/defaults/config`

- [x] add `IdleTimeout time.Duration` and `IdleTimeoutSet bool` fields to `Values` struct (same pattern as `SessionTimeout`)
- [x] add `parseIdleTimeout` method to `valuesLoader` (same pattern as `parseSessionTimeout`)
- [x] call `parseIdleTimeout` from `parseValuesFromBytes`
- [x] add idle timeout to `mergeExtraFrom` in `values.go` (same pattern as `SessionTimeout` merge)
- [x] add `IdleTimeout time.Duration` and `IdleTimeoutSet bool` fields to `Config` struct in `config.go`
- [x] add mapping in `loadConfigFromDirs` from `values.IdleTimeout`/`values.IdleTimeoutSet` to `Config` fields (same pattern as `SessionTimeout`)
- [x] add `--idle-timeout` CLI flag to options struct with description
- [x] add validation: `--idle-timeout must be non-negative`
- [x] wire CLI flag in `applyCLIOverrides` (same pattern as `SessionTimeout`)
- [x] in `New()` of `runner.go`, set `claudeExec.IdleTimeout = cfg.AppConfig.IdleTimeout`
- [x] add `idle_timeout` to embedded default config file with commented-out entry
- [x] write config tests: parse idle_timeout from INI, default disabled, local overrides global
- [x] write CLI validation test: negative value rejected
- [x] run `go test ./pkg/config/... ./cmd/ralphex/... ./pkg/processor/...` — must pass before next task

### Task 3: Verify acceptance criteria

- [x] verify `--idle-timeout 5m` kills a hanging claude session after 5 minutes of no output
- [x] verify active sessions (continuous output) are not affected by idle timeout
- [x] verify `--session-timeout` and `--idle-timeout` coexist correctly
- [x] verify idle timeout disabled by default (zero value)
- [x] verify Windows builds: `GOOS=windows GOARCH=amd64 go build ./...`
- [x] run full test suite: `go test ./...`
- [x] run linter: `golangci-lint run --max-issues-per-linter=0 --max-same-issues=0`

### Task 4: [Final] Update documentation

- [x] update CLAUDE.md — add idle timeout to Key Patterns and Configuration sections
- [x] update llms.txt — add `--idle-timeout` to usage docs
- [x] update README.md — add idle timeout to configuration section
- [x] update comment on issue #248 referencing the new feature
- [x] move this plan to `docs/plans/completed/`
