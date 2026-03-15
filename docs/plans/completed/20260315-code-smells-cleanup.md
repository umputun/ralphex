# Code Smells Cleanup

## Overview
- address code smells identified through parallel smells analysis (5 agents), source verification, and Codex GPT-5.4 second opinion
- 14 tasks (12 code changes + 2 verification/docs) across 6 packages: trivial fixes, small refactors, and medium refactors
- all changes are behavior-preserving — no functional changes, only structure/style improvements
- improves maintainability, thread-safety clarity, and adherence to project conventions

## Context (from discovery)
- files/components involved:
  - `cmd/ralphex/main.go` (1094 lines) — CLI entry point
  - `pkg/config/values.go` (704 lines) — INI config parsing
  - `pkg/processor/runner.go` (1113 lines) — orchestration loop
  - `pkg/processor/signals.go` (117 lines) — signal detection helpers
  - `pkg/executor/executor.go` (~430 lines) — claude CLI execution
  - `pkg/input/input.go` — terminal input collector
  - `pkg/web/session.go`, `session_manager.go` (587 lines), `dashboard.go`, `server.go` — web dashboard
  - `pkg/git/external.go` — VCS CLI backend
- analysis methodology: 5 parallel go-smells-expert agents → source verification → Codex review → brainstorm filtering
- 57 initial findings reduced to 13 actionable tasks after filtering false positives and borderlines

## Solution Overview
- trivial tasks first (one-line fixes, dead code removal, comment fixes)
- small refactors next (extract helpers, unexport symbols, convert to methods)
- medium refactors last (split large functions, restructure types)
- each task is independent within its tier — no cross-task dependencies except ordering by risk

## Development Approach
- **testing approach**: regular (code first, then tests) — most changes are mechanical refactors
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: this is refactoring only — zero behavioral changes allowed.** All changes must be fully invisible to application users. No output differences, no API changes, no timing changes. This is the primary focus of every review.
- **CRITICAL: prefer methods over standalone functions.** When the plan says "extract helper", this means extract as a method on the relevant struct by default. Only use standalone functions when justified (e.g., no struct receiver available, like in `cmd/ralphex/main.go`).
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task** — no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run tests after each change
- maintain backward compatibility — all changes are behavior-preserving

## Testing Strategy
- **unit tests**: verify behavior unchanged after each refactor
- **existing test suites**: must remain green throughout — `go test ./...`
- **new tests**: only where refactoring creates new testable units (e.g., `parseCommaSeparated`, extracted helpers)
- **e2e tests**: run `go test -tags=e2e ./e2e/...` after web package changes (tasks 5, 11, 13, 14)

## Progress Tracking
- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope

## Implementation Steps

### Task 1: Trivial one-line fixes

**Files:**
- Modify: `pkg/executor/executor.go`
- Modify: `cmd/ralphex/main.go`

- [x] change `fmt.Printf("[debug] non-JSON line: %s\n", line)` to `log.Printf("[debug] non-JSON line: %s", line)` in `parseStream` at `executor.go:270`
- [x] replace `len(rel) > 6 && rel[:6] == "../../"` with `strings.HasPrefix(rel, "../../")` in `toRelPath` at `main.go:992`
- [x] reformat `isResetOnly` at `main.go:1002` — break 9-condition boolean from single line to multi-line (one condition per line)
- [x] run `go test ./pkg/executor/... ./cmd/ralphex/...` — must pass before next task

### Task 2: Remove unused Collector interface

**Files:**
- Modify: `pkg/input/input.go`

- [x] remove `//go:generate moq` directive at line 54
- [x] remove `Collector` interface definition (lines 56-68)
- [x] verify no references exist: `grep -r "input\.Collector" .` should return nothing
- [x] run `go test ./pkg/input/...` — must pass before next task

### Task 3: Fix logging convention in dashboard

**Files:**
- Modify: `pkg/web/dashboard.go`

- [x] change `fmt.Fprintf(os.Stderr, "warning: watcher error: %v\n", watchErr)` to `log.Printf("[WARN] watcher error: %v", watchErr)` at line ~137
- [x] change `fmt.Fprintf(os.Stderr, "warning: web server error during execution: %v\n", srvErr)` to `log.Printf("[WARN] web server error during execution: %v", srvErr)` at line ~146
- [x] run `go test ./pkg/web/...` — must pass before next task

### Task 4: Lowercase comments on unexported methods

**Files:**
- Modify: `pkg/git/external.go`

- [x] lowercase first word of godoc-style comments on all unexported methods: `root`, `headHash`, `hasCommits`, `currentBranch`, `getDefaultBranch`, `branchExists`, `createBranch`, `checkoutBranch`, `isDirty`, `fileHasChanges`, `isIgnored`, `add`, `moveFile`, `commit`, `createInitialCommit`, `addWorktree`, `removeWorktree`, `pruneWorktrees` (e.g., `// Root returns...` → `// root returns...`)
- [x] run `go test ./pkg/git/...` — must pass before next task

### Task 5: Extract parseCommaSeparated method

**Files:**
- Modify: `pkg/config/values.go`
- Modify: `pkg/config/values_test.go` (if test additions needed)

- [x] add method `func (vl *valuesLoader) parseCommaSeparated(section *ini.Section, key string) []string` that reads a key, splits by comma, trims whitespace, filters empty strings
- [x] replace 5 comma-split blocks in `parseValuesFromBytes` with one-liner calls: `claude_error_patterns`, `codex_error_patterns`, `claude_limit_patterns`, `codex_limit_patterns`, `watch_dirs`
- [x] replace comma-split blocks in `parseNotifyValues` and `parseNotifyDestValues` — note: 3 blocks (`notify_channels`, `notify_webhook_urls`, `notify_email_to`) also set `*Set` flags, so the `*Set = true` assignment must remain outside the helper call
- [x] write tests for `parseCommaSeparated` — empty key, single value, multiple values, whitespace trimming, empty strings filtered
- [x] run `go test ./pkg/config/...` — must pass before next task

### Task 6: Convert config standalone functions to methods

**Files:**
- Modify: `pkg/config/values.go`

- [x] convert `parseNotifyValues(section, values)` to `(vl *valuesLoader) parseNotifyValues(section, values)` — update call in `parseValuesFromBytes`
- [x] convert `parseNotifyDestValues(section, values)` to `(vl *valuesLoader) parseNotifyDestValues(section, values)` — update call in `parseNotifyValues`
- [x] convert `parseWaitOnLimit(section, values)` to `(vl *valuesLoader) parseWaitOnLimit(section, values)` — update call in `parseValuesFromBytes`
- [x] run `go test ./pkg/config/...` — must pass before next task

### Task 7: Unexport internal-only signals symbols

**Files:**
- Modify: `pkg/processor/signals.go`
- Modify: `pkg/processor/runner.go`
- Modify: `pkg/processor/signals_test.go`
- Modify: `pkg/processor/runner_test.go`

- [x] rename `QuestionPayload` → `questionPayload` in signals.go and all references
- [x] rename `IsReviewDone` → `isReviewDone`, `IsCodexDone` → `isCodexDone`, `IsPlanReady` → `isPlanReady`
- [x] rename `ParseQuestionPayload` → `parseQuestionPayload`, `ParsePlanDraftPayload` → `parsePlanDraftPayload`
- [x] rename `ErrNoQuestionSignal` → `errNoQuestionSignal`, `ErrNoPlanDraftSignal` → `errNoPlanDraftSignal`
- [x] note: signal constants (`SignalCompleted`, `SignalFailed`, etc.) are aliases to `status.*` constants from `pkg/status` — intentionally left exported since they mirror the shared status package API
- [x] convert godoc comments to lowercase for all renamed symbols
- [x] update all references in `runner.go` and test files
- [x] run `go test ./pkg/processor/...` — must pass before next task

### Task 8: Group NewWithExecutors parameters

**Files:**
- Modify: `pkg/processor/runner.go`
- Modify: `pkg/processor/runner_test.go`

- [x] add `Executors` struct with fields: `Claude Executor`, `Codex Executor`, `Custom *executor.CustomExecutor`
- [x] change `NewWithExecutors(cfg Config, log Logger, claude, codex Executor, custom *executor.CustomExecutor, holder *status.PhaseHolder)` to `NewWithExecutors(cfg Config, log Logger, execs Executors, holder *status.PhaseHolder)`
- [x] update function body to use `execs.Claude`, `execs.Codex`, `execs.Custom`
- [x] update all test call sites (~90 in runner_test.go) to use `Executors{...}` struct literal — use batch find-replace
- [x] run `go test ./pkg/processor/...` — must pass before next task

### Task 9: Convert dashboard standalone functions to methods

**Files:**
- Modify: `pkg/web/dashboard.go`
- Modify: `pkg/web/dashboard_test.go`

- [x] convert `setupWatchMode(ctx, port, host, dirs)` to `(d *Dashboard) setupWatchMode(ctx, dirs)` — use `d.port`, `d.host` instead of params
- [x] convert `printWatchInfo(dirs, port, host, colors)` to `(d *Dashboard) printWatchInfo(dirs)` — use `d.port`, `d.host`, `d.colors`
- [x] convert `monitorErrors(ctx, srvErrCh, watchErrCh, colors)` to `(d *Dashboard) monitorErrors(ctx, srvErrCh, watchErrCh)` — use `d.colors`
- [x] update call sites in `RunWatchOnly` to use method calls
- [x] update tests to call methods on Dashboard instances instead of standalone functions
- [x] run `go test ./pkg/web/...` — must pass before next task

### Task 10: Split executePlan into focused helpers

**Files:**
- Modify: `cmd/ralphex/main.go`

- [x] extract unexported `setupProgressLogger(...)` helper from logger setup section (~15 lines)
- [x] extract unexported `sendNotifications(...)` helper from notification section (~10 lines)
- [x] extract unexported `displayStats(...)` helper from stats display section (~15 lines)
- [x] extract unexported `keepDashboardAlive(...)` helper from post-completion dashboard section (~20 lines)
- [x] verify `executePlan` reads as a sequential orchestrator calling these helpers
- [x] run `go test ./cmd/ralphex/...` — must pass before next task

### Task 11: Clean up Session field visibility

Hygiene refactor: enforce consistent access patterns. `SetState`/`GetState` and `SetMetadata`/`GetMetadata` accessors already exist and are used by external callers. Direct `.State`/`.Metadata` access is only from within `Session` methods (lock-protected) and one test assertion (`server_test.go`). Making fields lowercase enforces the accessor pattern.

**Files:**
- Modify: `pkg/web/session.go`
- Modify: `pkg/web/server_test.go`

- [x] make mutable fields private: `State` → `state`, `Metadata` → `metadata`, `Tailer` → `tailer`
- [x] add `GetTailer()` / `SetTailer()` thread-safe accessors (matching existing `GetState`/`SetState` pattern)
- [x] document immutable fields `ID`, `Path`, `SSE` with comment: `// set once at creation, immutable after`
- [x] update direct field access in `server_test.go` (one assertion) to use accessor
- [x] verify internal `session.go` methods access private fields directly (already under lock — no change needed)
- [x] run `go test ./pkg/web/...` — must pass before next task

### Task 12: Extract progress parsing from session_manager

**Files:**
- Create: `pkg/web/session_progress.go`
- Modify: `pkg/web/session_manager.go`

- [x] create `pkg/web/session_progress.go` with package `web` (contains `SessionManager` methods related to progress parsing)
- [x] move `ParseProgressHeader` to `session_progress.go`
- [x] move `loadProgressFileIntoSession` to `session_progress.go`
- [x] move `processProgressLine` to `session_progress.go`
- [x] move `emitPendingSection` to `session_progress.go`
- [x] move `phaseFromSection` to `session_progress.go`
- [x] move `trimLineEnding` to `session_progress.go`
- [x] verify no circular references — all moved functions should only depend on types already in `pkg/web`
- [x] run `go test ./pkg/web/...` — must pass before next task

### Task 13: Verify acceptance criteria

- [x] verify all 13 smell findings are addressed
- [x] verify no behavioral changes — all refactors are structure-only
- [x] run full unit test suite: `go test ./...`
- [x] run linter: `golangci-lint run --max-issues-per-linter=0 --max-same-issues=0`
- [x] run formatters: `~/.claude/format.sh`
- [x] verify test coverage meets 80%+ for changed packages

### Task 14: [Final] Update documentation

- [x] update CLAUDE.md if new patterns discovered during cleanup
- [x] move this plan to `docs/plans/completed/`

## Post-Completion

**Manual verification:**
- run e2e toy project test to verify no regression in execution flow
- spot-check web dashboard to verify SSE streaming still works after Session refactor
