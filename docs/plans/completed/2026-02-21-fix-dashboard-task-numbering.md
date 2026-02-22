# fix web dashboard task numbering on mid-run plan edits

## Overview

Web dashboard shows wrong task numbers when plan is edited mid-run or tasks retry (issue #127). The runner uses its loop counter as TaskNum instead of the actual plan task position. The plan parser silently drops non-integer task headers (e.g., "Task 2.5"), and the single-session plan cache is never invalidated.

**Approach:** use position-based matching (array index) instead of heading number. Everything stays `int` — no new string fields needed on Section, Event, or BroadcastLogger. The existing int pipeline carries task position instead of loop counter.

## Context

**root cause:** in `runTaskPhase()`, the loop passes `i` to `NewTaskIterationSection(i)`. BroadcastLogger sets `currentTask = section.Iteration` (loop counter). Dashboard matches `event.task_num` against `plan.tasks[].number` (from markdown heading). When tasks are inserted or retried, loop counter != plan task position.

**also affected:** task retries — when a task fails and retries, `i` increments but the plan task doesn't change, causing the same off-by-one in dashboard highlighting.

**assumption:** tasks are completed in order (runner uses single prompt that reads plan each time, Claude picks next uncompleted task sequentially).

**files involved:**
- `pkg/web/plan.go` - plan parsing types + regex, needs to move to `pkg/plan/`
- `pkg/processor/runner.go` - loop counter, `hasUncompletedTasks()` (already reads plan file)
- `pkg/web/server.go` - `planCache *Plan`, never invalidated
- `pkg/web/static/app.js` - `getTaskTitle(taskNum)`, `updatePlanTaskStatus`, `handleTaskStart`

**existing patterns:**
- `hasUncompletedTasks()` in runner already reads plan file each iteration (raw string scan)
- `ParsePlan()` in web does proper parsing with regex + checkbox status detection
- `determineTaskStatus(checkboxes)` returns pending/active/done/failed

## Development Approach

- **testing approach**: regular (code first, then tests)
- complete each task fully before moving to the next
- direct imports (no type aliases after move)
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**
- **CRITICAL: update this plan file when scope changes during implementation**

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix

## Implementation Steps

### Task 1: Move plan parsing to `pkg/plan/`

**Files:**
- Create: `pkg/plan/parse.go`
- Create: `pkg/plan/parse_test.go`
- Modify: `pkg/web/plan.go` (remove types/funcs, keep only web-specific helpers if any)
- Modify: all `pkg/web/` files that reference `Plan`, `Task`, `TaskStatus`, `Checkbox` types

- [x] move `ParsePlan`, `ParsePlanFile`, `Plan`, `Task`, `Checkbox`, `TaskStatus`, constants, `determineTaskStatus`, `parseTaskNum` from `pkg/web/plan.go` to `pkg/plan/parse.go`
- [x] move `JSON()` method to `pkg/plan/parse.go`
- [x] update all `pkg/web/` imports to use `plan.Plan`, `plan.Task`, etc. (direct imports, no aliases)
- [x] check if `pkg/web/plan.go` still needed; if only `loadPlanWithFallback` remains, keep it as web-specific helper
- [x] move relevant tests from `pkg/web/plan_test.go` to `pkg/plan/parse_test.go`
- [x] run `go test ./pkg/plan/ ./pkg/web/...` - must pass

### Task 2: Widen regex to support non-integer task headers

**Files:**
- Modify: `pkg/plan/parse.go`
- Modify: `pkg/plan/parse_test.go`

- [x] widen `taskHeaderPattern` regex from `(\d+)` to `([^:]+?)` with `strings.TrimSpace`
- [x] update `parseTaskNum` to: try `strconv.Atoi`, on success set `Number = parsed int`; on failure set `Number = 0`
- [x] add test cases for "Task 2.5:", "Task 2a:", "Task 3:" (backward compat)
- [x] verify non-integer tasks are parsed (not silently dropped) and appear in `Plan.Tasks` array
- [x] run `go test ./pkg/plan/` - must pass

### Task 3: Runner passes plan task position instead of loop counter

**Files:**
- Modify: `pkg/processor/runner.go`
- Modify: `pkg/processor/runner_test.go`
- Modify: `pkg/processor/export_test.go` (expose new method if needed)

- [x] add `nextPlanTaskPosition() int` method to Runner: reads plan file via `plan.ParsePlanFile(r.resolvePlanFilePath())`, finds first task with status != `TaskStatusDone`, returns its 1-indexed position in the tasks array (0 on error = fallback to loop counter)
- [x] in `runTaskPhase`, before `PrintSection`: call `nextPlanTaskPosition()`; if > 0, use it instead of `i` for `NewTaskIterationSection(pos)`
- [x] keep `hasUncompletedTasks()` as-is (raw string scan works, refactoring changes semantics - deferred to separate task)
- [x] write tests for `nextPlanTaskPosition` with mock plan files: normal integer plan, plan with inserted "Task 2.5", missing file, no uncompleted tasks, plan with retried (same task still uncompleted)
- [x] run `go test ./pkg/processor/` - must pass

### Task 4: Remove planCache and update frontend matching

**Files:**
- Modify: `pkg/web/server.go`
- Modify: `pkg/web/server_test.go` (if cache is tested)
- Modify: `pkg/web/static/app.js`

- [x] remove `planMu sync.Mutex` and `planCache *Plan` fields from `Server`
- [x] simplify `loadPlan()` to call `loadPlanWithFallback()` directly (no caching)
- [x] update/remove any tests that assert cache behavior
- [x] in `renderPlan`: set `data-task-num` from array index (position `i+1`) instead of `task.number`
- [x] in `handleTaskStart`: if target task element not found by `data-task-num`, re-fetch plan via `fetch('/api/plan')`, rebuild plan panel, then retry highlight. this handles mid-run plan edits where the frontend has a stale task list
- [x] in `getTaskTitle`: match by position (iterate `planData.tasks` array, return task at `taskNum - 1` index)
- [x] run `go test ./pkg/web/...` - must pass

### Task 5: Verify acceptance criteria

- [x] verify: integer-only plans work identically to before (backward compat)
- [x] verify: old progress files with `"task iteration 3"` replay correctly
- [x] verify: task retries show correct task highlighting (same task stays highlighted)
- [x] verify: non-integer task headers ("Task 2.5") are parsed and appear in plan panel
- [x] run full test suite: `go test ./...`
- [x] run linter: `make lint`
- [x] run e2e tests: `go test -tags=e2e -timeout=10m -count=1 -v ./e2e/...`

### Task 6: Update documentation and complete

- [x] update CLAUDE.md if new patterns changed
- [x] move this plan to `docs/plans/completed/`

## Technical Details

**position-based data flow after fix:**
```
plan file: "### Task 1: ...", "### Task 2: ...", "### Task 2.5: ...", "### Task 3: ..."
    → plan.ParsePlanFile() → [Task{pos:0}, Task{pos:1}, Task{pos:2}, Task{pos:3}]
    → runner.nextPlanTaskPosition() → 3 (1-indexed, first uncompleted)
    → NewTaskIterationSection(3) → Section{Iteration:3, Label:"task iteration 3"}
    → broadcast_logger → Event{TaskNum:3}
    → frontend: data-task-num="3" on 3rd task element → highlights Task 2.5 ✓
```

**backward compat for integer-only plans:**
```
plan file: "### Task 1: ...", "### Task 2: ...", "### Task 3: ..."
    → [Task{pos:0, Number:1}, Task{pos:1, Number:2}, Task{pos:2, Number:3}]
    → position == number for sequential integer plans → identical behavior
```

**retry scenario:**
```
iteration 1: Task 1 runs, completes → position=1 ✓
iteration 2: Task 2 runs, FAILS → position=2
iteration 3: Task 2 retries → nextPlanTaskPosition() returns 2 (still uncompleted) → position=2 ✓
    (without fix: loop counter i=3, dashboard would show Task 3)
```

**mid-run edit (frontend stale plan):**
```
frontend has old plan (3 tasks), runner sends task_num=4 (new plan has 4 tasks)
    → handleTaskStart: querySelector('[data-task-num="4"]') returns null
    → triggers fetch('/api/plan'), rebuilds plan panel with 4 tasks
    → retries highlight on position 4 → correct ✓
```

**progress file replay:** progress file section labels (`"task iteration 3"`) are always loop counters. the tailer/session_manager regex stays unchanged. replay of old progress files uses the loop counter as position, which is correct for the plan state at recording time. replay of edited-plan sessions from old progress files may show wrong positions — this is acceptable, replay accuracy for old files is not a goal.

**what does NOT change:** Section struct, Event struct, BroadcastLogger fields, NewTaskStartEvent/NewTaskEndEvent signatures, tailer regex, session_manager regex. The entire int pipeline stays as-is — only the value fed into it changes.

## Post-Completion

**manual verification:**
- toy project e2e: run `scripts/prep-toy-test.sh`, edit plan mid-run (insert Task 1.5), verify dashboard shows correct task highlighting
- verify old progress file replay works with `ralphex --serve`
- verify task retry shows correct highlighting (kill a task, observe retry stays on same plan task)
