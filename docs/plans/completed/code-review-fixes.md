# Code Review Fixes

## Overview

Fix 14 confirmed issues from comprehensive code review. No high-severity bugs — these are structural improvements covering signal duplication, progress parsing duplication, dead code removal, magic numbers, stale comments, missing error logging, and minor cleanups.

False positives excluded: createRunner params (#7), event dropping (#8c), unlockFile discard (#8e), Config overload (#11), Colors dependency (#14), Error/Warn test helpers (#16), naming conventions (#18), repetitive parsing justified (#20).

## Development Approach

- **testing approach**: regular (code first, then tests)
- complete each task fully before moving to the next
- small, focused changes — each task is one logical concern
- **CRITICAL: every task MUST include new/updated tests** for code changes
- **CRITICAL: all tests must pass before starting next task**
- run tests after each change
- maintain backward compatibility

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix

## Implementation Steps

### Task 1: Fix stale comment and extract magic numbers in runner.go

Low-risk, purely cosmetic/readability changes.

- [x] fix stale comment at `pkg/processor/runner.go:567` — change "500 chars" to "5000 chars"
- [x] extract named constants for iteration ratios and limits:
  - `minReviewIterations = 3` (line 392)
  - `reviewIterationDivisor = 10` (line 392)
  - `minCodexIterations = 3` (line 437)
  - `codexIterationDivisor = 5` (line 437)
  - `minPlanIterations = 5` (line 682)
  - `planIterationDivisor = 5` (line 682)
  - `maxCodexSummaryLen = 5000` (line 577)
- [x] verify existing tests still pass: `go test ./pkg/processor/...`

### Task 2: Remove dead code

Remove confirmed dead exported functions/types with no production callers.

- [x] remove `IsPlanDraft()` from `pkg/processor/signals.go:57-59` (no production callers) and its test `TestIsPlanDraft` from `signals_test.go:272`
- [x] remove `IsTerminalSignal()` from `pkg/processor/signals.go:37-39` (no production callers) and its test `TestIsTerminalSignal` from `signals_test.go:10`
- [x] remove `NewErrorEvent()` from `pkg/web/event.go:62-69` (no production callers)
- [x] remove `NewWarnEvent()` from `pkg/web/event.go:71-79` (no production callers)
- [x] remove tests for `NewErrorEvent`/`NewWarnEvent` in `event_test.go` if any
- [x] remove deprecated Phase type alias and constants from `pkg/progress/progress.go:21-34` and the `processor` import if no longer needed
- [x] run `go test ./pkg/processor/... ./pkg/web/... ./pkg/progress/...`

### Task 3: Fix redundant condition in defaults.go

- [x] remove redundant `if len(differentFiles) > 0` at `pkg/config/defaults.go:341` — always true after early return at line 335
- [x] run `go test ./pkg/config/...`

### Task 4: Add missing error logging in session_manager.go

- [x] add `log.Printf` at `pkg/web/session_manager.go:68` where comment says "log error but continue" but no logging exists
- [x] add `log.Printf` at `pkg/web/session_manager.go:74-75` (same pattern, also missing logging)
- [x] write integration-style test: create a deliberately broken/unreadable progress file, run Discover, capture log output, assert error is logged and other sessions still processed
- [x] run `go test ./pkg/web/...`

### Task 5: Convert standalone functions to methods

Per project guidelines: functions called only from methods must be methods.

- [x] convert `loadProgressFileIntoSession(path string, session *Session)` at `pkg/web/session_manager.go:440` to `(*SessionManager).loadProgressFileIntoSession(path string, session *Session)`
- [x] convert `emitPendingSection(session *Session, ...)` at `pkg/web/session_manager.go:549` to `(*SessionManager).emitPendingSection(session *Session, ...)` — or make it a method on `*Session` since it only publishes to session
- [x] update all call sites within session_manager.go
- [x] run `go test ./pkg/web/...`

### Task 6: Deduplicate maxScannerBuffer constant

- [x] move `maxScannerBuffer` to a shared location — either add to `pkg/executor/executor.go` as exported `MaxScannerBuffer` and import from `pkg/web/session_manager.go`, or create a small shared const (evaluate which is cleaner given import direction)
- [x] remove duplicate definition from `pkg/web/session_manager.go:27`
- [x] run `go test ./pkg/executor/... ./pkg/web/...`

### Task 7: Eliminate hardcoded signals in executor.detectSignal()

Circular dependency: processor imports executor (runner.go:14), so executor cannot import processor. Signal constants must move to a shared package.

- [x] create `pkg/signals/signals.go` with signal constants (moved from `pkg/processor/signals.go`)
- [x] keep helper functions (`IsReviewDone`, `IsCodexDone`, `IsPlanReady`, regex patterns) in `pkg/processor/signals.go` — they import `pkg/signals` for constants
- [x] update `pkg/processor/signals.go` to use `signals.Signal*` constants instead of local definitions
- [x] update `pkg/executor/executor.go:detectSignal()` to use `signals.Signal*` constants instead of hardcoded strings
- [x] update `pkg/web/broadcast_logger.go:extractTerminalSignal()` to use `signals.Signal*` constants
- [x] update all test files that reference `processor.Signal*` to use `signals.Signal*`
- [x] add regression tests verifying behavior preservation:
  - `executor.detectSignal` still detects all terminal/review/plan signals
  - `broadcast_logger.extractTerminalSignal` still maps to COMPLETED/FAILED/REVIEW_DONE/CODEX_REVIEW_DONE
  - `tail.extractSignalFromText` normalization unchanged for all signal types
- [x] run `go test ./pkg/signals/... ./pkg/executor/... ./pkg/processor/... ./pkg/web/...`

### Task 8: Deduplicate progress file parsing in web package

`loadProgressFileIntoSession` (session_manager.go:440-527) duplicates parsing logic from `tail.go:parseLine`. Extract shared helper.

- [x] create a shared `parseProgressLine()` function in `pkg/web/` that both `tail.go:parseLine` and `loadProgressFileIntoSession` can call — handles timestamp parsing, section detection, signal extraction, event type detection
- [x] refactor `tail.go:parseLine` to use the shared function
- [x] refactor `loadProgressFileIntoSession` to use the shared function
- [x] ensure header-skipping logic (specific to file replay) stays in `loadProgressFileIntoSession`
- [x] write tests for `parseProgressLine()` covering: timestamped lines, section headers, plain lines, signal lines, error/warn detection
- [x] add parity test: feed identical lines through live parsing (tail) and replay parsing (loadProgressFileIntoSession), assert both produce equivalent events
- [x] run `go test ./pkg/web/...`

### Task 9: Extract review pipeline helper in runner.go

`runFull`, `runReviewOnly`, `runCodexOnly` share identical codex+review+finalize blocks.

- [x] extract `runCodexAndPostReview(ctx) error` method covering: set PhaseCodex → print section → runCodexLoop → set PhaseReview → runClaudeReviewLoop → runFinalize
- [x] refactor `runFull` to call task phase → first review → pre-codex review loop → `runCodexAndPostReview`
- [x] refactor `runReviewOnly` to call first review → pre-codex review loop → `runCodexAndPostReview`
- [x] refactor `runCodexOnly` to call `runCodexAndPostReview`
- [x] add regression test: verify runCodexOnly and runReviewOnly each call finalize exactly once and in expected order (after codex loop and post-codex review)
- [x] verify all existing runner tests still pass: `go test ./pkg/processor/...`

### Task 10: Verify acceptance criteria

- [x] verify all 14 issues addressed
- [x] run full test suite: `go test ./...`
- [x] run linter: `golangci-lint run --max-issues-per-linter=0 --max-same-issues=0`
- [x] run formatter: `make fmt`
- [x] verify test coverage hasn't decreased

### Task 11: [Final] Update documentation

- [x] update CLAUDE.md if any patterns changed (signal package, shared constants)
- [x] move this plan to `docs/plans/completed/`

## Technical Notes

**Signal deduplication approach:** Circular dependency confirmed (processor imports executor at runner.go:14). Signal constants must live in `pkg/signals/signals.go` — a new shared package imported by both processor and executor. Helper functions (IsReviewDone, etc.) stay in processor since they depend on processor-level logic.

**Progress parsing deduplication:** The shared function should return a parsed result struct (event type, phase, text, timestamp, signal, section) that callers convert to Event objects. This keeps the shared function free of SSE/session concerns.

**Review pipeline:** The extracted method should handle the "codex → post-codex claude review → finalize" trio since that's the exact block duplicated 3 times. The pre-codex review and first review vary between modes so they stay in the individual methods.

## Post-Completion

**Manual verification:**
- run e2e test with toy project to verify streaming output still works after web package refactoring
- verify web dashboard still displays signals correctly after signal constant changes
