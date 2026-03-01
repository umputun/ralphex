# Wait on Rate Limit with Retry

## Overview
- Add optional `--wait=<duration>` CLI flag and `wait_on_limit` config option for rate limit retry behavior
- When a rate limit pattern is detected in executor output, instead of exiting, ralphex waits for the specified duration and retries the same call
- Without `--wait` (or `wait_on_limit`), behavior is identical to today — limit patterns cause graceful exit just like error patterns
- Introduces separate `claude_limit_patterns` and `codex_limit_patterns` config options that can overlap with error patterns
- Priority: limit patterns checked first. If match AND wait > 0 → wait+retry. If match AND wait == 0 → exit (same as error pattern). Error patterns → always exit
- Note: limit patterns intentionally overlap with error patterns by default. The `wait_on_limit` setting acts as the toggle — when set, limit matches trigger retry instead of exit; when unset, they fall through to error pattern behavior

## Context
- Current implementation: `PatternMatchError` in `pkg/executor/executor.go`, `checkErrorPatterns()` helper
- 7 call sites in `pkg/processor/runner.go` that handle `PatternMatchError` via `handlePatternMatchError()`
- `runFinalize()` is a special case — best-effort, pattern match logged but doesn't fail
- `CustomExecutor` reuses `CodexErrorPatterns` — same approach for limit patterns
- Config parsed in `pkg/config/values.go` as comma-separated strings
- No CLI flags for patterns currently — they come from config only
- CLI flags defined via jessevdk/go-flags in `cmd/ralphex/main.go`

## Development Approach
- **testing approach**: Regular (code first, then tests)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**
- run tests after each change

## Testing Strategy
- unit tests for new error types and pattern checking in pkg/executor
- unit tests for config parsing of new fields (limit patterns, wait_on_limit duration)
- unit tests for retry wrapper in runner.go (mock executor returning LimitPatternError, verify retry behavior)
- integration: existing tests must continue passing unchanged

## Progress Tracking
- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix

## Implementation Steps

### Task 1: Add LimitPatternError type and limit pattern checking to executors

**Files:**
- Modify: `pkg/executor/executor.go`
- Modify: `pkg/executor/codex.go`
- Modify: `pkg/executor/custom.go`
- Modify: `pkg/executor/executor_test.go`
- Modify: `pkg/executor/codex_test.go`
- Modify: `pkg/executor/custom_test.go`

- [x] add `LimitPatternError` type alongside `PatternMatchError` in `executor.go` (same fields: Pattern, HelpCmd)
- [x] rename `checkErrorPatterns()` to `matchPattern()` (generic helper) — returns first matching pattern string or empty. Keep `checkErrorPatterns` as a thin wrapper calling `matchPattern` for backward compat if needed, or just rename all call sites
- [x] add `LimitPatterns []string` field to `ClaudeExecutor` struct
- [x] in `ClaudeExecutor.Run()`: check limit patterns BEFORE error patterns using `matchPattern()` — if limit match, return `LimitPatternError`; otherwise fall through to existing error pattern check returning `PatternMatchError`
- [x] add `LimitPatterns []string` field to `CodexExecutor` struct with same priority logic in `Run()`
- [x] add `LimitPatterns []string` field to `CustomExecutor` struct with same priority logic in `Run()`
- [x] write tests for `LimitPatternError.Error()` method
- [x] write tests for `matchPattern()` — empty patterns, no match, match, case-insensitive
- [x] write tests for `ClaudeExecutor.Run()` — limit pattern takes precedence over error pattern when same string matches both
- [x] write tests for `CodexExecutor.Run()` — limit pattern match returns `LimitPatternError`
- [x] write tests for `CustomExecutor.Run()` — limit pattern match returns `LimitPatternError`
- [x] run `go test ./pkg/executor/...` — must pass

### Task 2: Add config fields and parsing for limit patterns and wait duration

**Files:**
- Modify: `pkg/config/config.go`
- Modify: `pkg/config/values.go`
- Modify: `pkg/config/defaults/config`
- Modify: `pkg/config/values_test.go`

- [x] add `ClaudeLimitPatterns []string`, `CodexLimitPatterns []string`, `WaitOnLimit time.Duration` fields to `config.Config`/`Values`
- [x] add `WaitOnLimitSet bool` flag to distinguish explicit `0` from "not set" (same pattern as `CodexEnabledSet`)
- [x] parse `claude_limit_patterns`, `codex_limit_patterns` as comma-separated strings in `values.go` (same as error patterns)
- [x] parse `wait_on_limit` as duration string via `time.ParseDuration()` in `values.go`
- [x] add merge logic for new fields in `mergeExtraFrom()` — same replace semantics as error patterns
- [x] add default entries to `pkg/config/defaults/config`: `claude_limit_patterns = You've hit your limit` and `codex_limit_patterns = Rate limit,quota exceeded` and `wait_on_limit =` (empty/disabled)
- [x] write tests for parsing limit patterns (single, multiple, whitespace trimming, empty)
- [x] write tests for parsing `wait_on_limit` duration (valid durations like "1h", "30m", "1h30m", empty, zero)
- [x] write tests for merge behavior (local overrides global for limit patterns and wait_on_limit)
- [x] run `go test ./pkg/config/...` — must pass

### Task 3: Add CLI flag and wire limit patterns to executors

**Files:**
- Modify: `cmd/ralphex/main.go`
- Modify: `cmd/ralphex/main_test.go` (if exists, otherwise skip)

- [x] add `--wait` CLI flag (type `time.Duration`, default 0) to CLI options struct
- [x] wire CLI `--wait` to config `WaitOnLimit` with CLI-takes-precedence logic (same as other CLI overrides)
- [x] wire `ClaudeLimitPatterns` to `ClaudeExecutor.LimitPatterns` in runner creation (`createRunner` or `processor.New`)
- [x] wire `CodexLimitPatterns` to `CodexExecutor.LimitPatterns` and `CustomExecutor.LimitPatterns` (reuse codex patterns for custom, same as error patterns)
- [x] add test verifying `--wait` CLI flag overrides `wait_on_limit` config value (follow existing pattern for CLI override tests)
- [x] run `go test ./...` — must pass

### Task 4: Add retry wrapper and update all Run() call sites in runner

**Files:**
- Modify: `pkg/processor/runner.go`
- Modify: `pkg/processor/runner_test.go`

- [x] add `waitOnLimit` field to `Runner` struct, populated from config in `New()`
- [x] add `runWithLimitRetry()` method that wraps an executor `Run()` call: checks result for `LimitPatternError`, if match AND `waitOnLimit > 0` logs message with wait duration, sleeps (context-aware via `sleepWithContext`), retries same call; if match AND `waitOnLimit == 0` returns result as-is (existing exit behavior); unlimited retries, loop until success or context cancellation
- [x] update `runTaskPhase()` — wrap `r.claude.Run()` call with `runWithLimitRetry()`
- [x] update `runClaudeReview()` — wrap `r.claude.Run()` call with `runWithLimitRetry()`
- [x] update `runClaudeReviewLoop()` — wrap `r.claude.Run()` call with `runWithLimitRetry()`
- [x] update `runExternalReviewLoop()` — wrap external tool `cfg.runReview()` call with `runWithLimitRetry()`
- [x] update `runExternalReviewLoop()` — wrap claude eval `r.claude.Run()` call with `runWithLimitRetry()`
- [x] update `runPlanCreation()` — wrap `r.claude.Run()` call with `runWithLimitRetry()`
- [x] update `runFinalize()` — best-effort semantics: if limit pattern AND wait > 0, retry; if limit pattern AND wait == 0, log and return nil (preserve existing behavior)
- [x] write tests for `runWithLimitRetry()`: mock executor returns LimitPatternError on first call, success on second — verify retry happens
- [x] write tests for `runWithLimitRetry()`: wait_on_limit == 0, LimitPatternError returned as-is (no retry)
- [x] write tests for `runWithLimitRetry()`: context cancelled during wait — returns context error
- [x] write tests for `runWithLimitRetry()`: PatternMatchError (not limit) — returned as-is, no retry
- [x] run `go test ./pkg/processor/...` — must pass

### Task 5: Update documentation and config template

**Files:**
- Modify: `pkg/config/defaults/config` (comments for new fields already added in Task 2)
- Modify: `README.md`
- Modify: `CLAUDE.md`
- Modify: `llms.txt`

- [x] update README.md — document `--wait` flag, `wait_on_limit` config option, limit pattern config options, and the overlap/priority behavior
- [x] update CLAUDE.md — document new config fields under Configuration section and Error Pattern Detection section
- [x] update llms.txt — add `--wait` to usage examples and customization section

### Task 6: Verify acceptance criteria
- [x] run full test suite: `go test ./...`
- [x] run linter: `make lint`
- [x] verify test coverage meets 80%+ for new code

### Task 7: [Final] Move plan to completed
- [x] move this plan to `docs/plans/completed/`

## Technical Details

**New types in pkg/executor:**
- `LimitPatternError{Pattern, HelpCmd}` — parallel to `PatternMatchError`
- `matchPattern(output, patterns) string` — generic helper extracted from `checkErrorPatterns`, reused for both error and limit patterns

**Priority in executor Run():**
```
1. check limit patterns via matchPattern() → LimitPatternError
2. check error patterns via matchPattern() → PatternMatchError
```

**Retry wrapper signature:**
```go
func (r *Runner) runWithLimitRetry(ctx context.Context, run func(context.Context, string) executor.Result, prompt, toolName string) executor.Result
```
- accepts a `run` function (works for claude, codex, custom executors)
- returns `executor.Result` — caller handles signals and other errors as before
- internally: loop { run → check LimitPatternError → if limit AND wait > 0 → log, sleep, continue; else break and return result }

**Config fields:**
- `wait_on_limit` — duration string (e.g., "1h", "30m"), parsed via `time.ParseDuration()`
- `claude_limit_patterns` — comma-separated, same format as error patterns
- `codex_limit_patterns` — comma-separated, same format as error patterns

**Config defaults:**
- `claude_limit_patterns = You've hit your limit`
- `codex_limit_patterns = Rate limit,quota exceeded`
- `wait_on_limit =` (empty, disabled by default)

**Log output on limit retry:**
```
rate limit detected: "You've hit your limit" in claude output, waiting 1h0m0s before retry...
```

## Post-Completion

**Manual verification:**
- test with actual rate limit scenario (hit Claude limit, verify wait+retry works)
- verify Ctrl+C during wait period terminates cleanly
