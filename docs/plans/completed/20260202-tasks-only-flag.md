# Add --tasks-only Flag

## Overview

Add `--tasks-only` flag to run task execution phase without any reviews. Completes the execution mode matrix:
- `--review` - reviews only (exists)
- `--codex-only` - codex → claude eval (exists)
- `--tasks-only` - tasks only, no reviews (new)

Use cases:
- Manual review / token saving
- Custom pipelines between tasks and reviews
- External review tools

Related: #57

## Context

Files involved:
- `cmd/ralphex/main.go` - CLI flag definition, mode determination, branch/plan-move logic
- `pkg/processor/runner.go` - Mode constants, Run() dispatcher, execution methods
- `cmd/ralphex/main_test.go` - determineMode tests
- `pkg/processor/runner_test.go` - runner execution tests

Existing patterns:
- Mode type with constants (ModeFull, ModeReview, ModeCodexOnly, ModePlan)
- `determineMode()` function converts flags to modes with precedence
- Mode-specific `run*()` methods in runner
- Branch created for modes that execute tasks
- Plan moved to completed/ for ModeFull

## Development Approach

- **testing approach**: Regular (code first, then tests)
- complete each task fully before moving to the next
- run tests after each change
- every task includes tests for new/changed functionality

## Implementation Steps

### Task 1: Add ModeTasksOnly constant

**Files:** `pkg/processor/runner.go`

- [x] add `ModeTasksOnly Mode = "tasks-only"` constant after ModeCodexOnly
- [x] add comment: `// run only task phase, skip all reviews`
- [x] run `go test ./pkg/processor/...` - verify no breakage

### Task 2: Add runTasksOnly() method

**Files:** `pkg/processor/runner.go`

- [x] add `runTasksOnly(ctx context.Context) error` method to Runner
- [x] require plan file (return error if empty)
- [x] set phase to PhaseTask
- [x] call `runTaskPhase(ctx)` and return result
- [x] log completion message
- [x] add case in `Run()` dispatcher: `case ModeTasksOnly: return r.runTasksOnly(ctx)`
- [x] write tests in `pkg/processor/runner_test.go` for runTasksOnly
- [x] run `go test ./pkg/processor/...` - must pass

### Task 3: Add --tasks-only CLI flag

**Files:** `cmd/ralphex/main.go`

- [x] add `TasksOnly bool` field to opts struct with `long:"tasks-only" description:"run only task phase, skip all reviews"`
- [x] update `determineMode()`: add case for TasksOnly returning ModeTasksOnly (after Plan, before CodexOnly)
- [x] write tests in `cmd/ralphex/main_test.go` for determineMode with TasksOnly
- [x] run `go test ./cmd/ralphex/...` - must pass

### Task 4: Update branch creation and plan-move logic

**Files:** `cmd/ralphex/main.go`

- [x] update branch creation condition to include ModeTasksOnly: `mode == processor.ModeFull || mode == processor.ModeTasksOnly`
- [x] update plan-move-to-completed condition to include ModeTasksOnly
- [x] write tests for branch creation with TasksOnly mode
- [x] write tests for plan-move with TasksOnly mode
- [x] run `go test ./cmd/ralphex/...` - must pass

### Task 5: Update documentation

**Files:** `README.md`, `llms.txt`

- [x] add `--tasks-only` to Quick Usage section in README.md
- [x] add `--tasks-only` to Quick Usage section in llms.txt
- [x] update CLAUDE.md if needed (execution modes description)

### Task 6: Final validation

- [x] run `make test` - all tests pass
- [x] run `make lint` - no issues
- [x] verify `ralphex --help` shows new flag
- [x] move this plan to `docs/plans/completed/`

## Technical Details

**Mode precedence in determineMode():**
1. `--plan` (ModePlan)
2. `--tasks-only` (ModeTasksOnly) - new
3. `--codex-only` (ModeCodexOnly)
4. `--review` (ModeReview)
5. Default: ModeFull

**runTasksOnly() flow:**
```
1. validate plan file exists
2. set phase to PhaseTask
3. log "starting task execution phase"
4. call runTaskPhase(ctx)
5. log "task execution completed"
6. return
```

**Branch and plan-move:**
- ModeTasksOnly creates branch (same as ModeFull) since tasks make commits
- ModeTasksOnly moves plan to completed/ after success (user confirmed)
