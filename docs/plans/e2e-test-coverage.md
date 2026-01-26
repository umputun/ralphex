# E2E Test Coverage Expansion

## Overview

Expand Playwright e2e tests to cover gaps identified in the current test suite. Focus on SSE event handling, error states, and edge cases that aren't covered by existing tests.

## Context

- Test files: `e2e/` directory
- Test data: `e2e/testdata/` with progress files and plan fixtures
- Server: runs in `--serve --watch` mode against temp directory
- Current coverage: UI components, keyboard shortcuts, basic SSE load, session sidebar

## Development Approach

- **Testing approach**: add tests to existing test files
- Tests use static fixture data in `e2e/testdata/`
- Each test creates isolated browser context via `newPage(t)`
- Use existing helpers: `navigateToDashboard`, `waitVisible`, `waitHidden`, `isDetailsOpen`

## Implementation Steps

### Task 1: Create comprehensive test fixture with all event types

- [x] Create `e2e/testdata/progress-full-events.txt` with:
  - Output events (regular lines)
  - Section events (task iteration, review sections)
  - Error events (`[ERROR] ...` lines)
  - Warning events (`[WARN] ...` lines)
  - Signal events (COMPLETED, FAILED, REVIEW_DONE)
  - Task boundary markers (`--- Task iteration N ---`)
  - Iteration markers (`--- Claude review iteration N ---`, `--- Codex iteration N ---`)
- [x] Update `.gitignore` exception if needed
- [x] Verify fixture loads correctly by running existing tests

### Task 2: Add error event rendering tests

- [x] Add `TestErrorEventRendering` to `sse_test.go`
  - Verify error lines have `.error` or `.output-error` class
  - Verify error styling is visually distinct (check CSS class presence)
  - Verify multiple error events render correctly
- [x] Run tests - must pass before next task

### Task 3: Add warning event rendering tests

- [x] Add `TestWarnEventRendering` to `sse_test.go`
  - Verify warn lines have `.warn` or `.output-warn` class
  - Verify warning styling is visually distinct
  - Verify multiple warning events render correctly
- [x] Run tests - must pass before next task

### Task 4: Add signal event tests

- [x] Add `TestSignalEventRendering` to `sse_test.go`
  - Test COMPLETED signal shows success indicator
  - Test FAILED signal shows failure indicator
  - Test REVIEW_DONE signal handling
  - Verify signal affects status badge (`#status-badge`)
- [x] Run tests - must pass before next task

### Task 5: Add task boundary event tests

- [x] Add `TestTaskBoundaryRendering` to `sse_test.go`
  - Verify task iteration headers render as section headers
  - Verify task number is displayed
  - Verify task sections are collapsible
- [x] Run tests - must pass before next task

### Task 6: Add iteration boundary event tests

- [x] Add `TestIterationBoundaryRendering` to `sse_test.go`
  - Verify Claude review iteration headers render correctly
  - Verify Codex iteration headers render correctly
  - Verify iteration number is displayed
- [x] Run tests - must pass before next task

### Task 7: Add auto-scroll behavior tests

- [x] Add `TestAutoScrollOnNewContent` to `sse_test.go`
  - Create a session with enough content to scroll
  - Verify scroll position updates when new content arrives (if user at bottom)
  - Verify scroll position preserved when user scrolled up
- [x] Add `TestScrollToBottomButtonBehavior` to `sse_test.go`
  - Verify button appears when scrolled away from bottom
  - Verify clicking button scrolls to bottom
  - Verify button hides when at bottom
- [x] Run tests - must pass before next task

### Task 8: Add plan parsing edge case tests

- [x] Create `e2e/testdata/test-plan-malformed.md` with edge cases:
  - Empty plan
  - Plan with no tasks
  - Plan with unclosed checkboxes
- [x] Add `TestPlanParsingEdgeCases` to `dashboard_test.go`
  - Test graceful handling of missing plan
  - Test display when plan has no tasks
- [x] Run tests - must pass before next task

### Task 9: Add SSE connection handling tests

- [x] Add `TestSSEReconnection` to `sse_test.go`
  - Test that status indicator shows connection state
  - Verify "connecting" state is shown initially
  - Verify "connected" state after SSE establishes
- [x] Add `TestSSEConnectionLoss` to `sse_test.go` (if feasible)
  - May need to skip if server kill/restart is too complex
  - At minimum, verify disconnected state UI exists
- [x] Run tests - must pass before next task

### Task 10: Verify all tests pass together

- [ ] Run full e2e test suite: `go test -tags=e2e -timeout=10m -count=1 -v ./e2e/...`
- [ ] Verify no flaky tests (run 3 times)
- [ ] Run linter: `make lint`

### Task 11: Update documentation

- [ ] Update `CLAUDE.md` test coverage description if needed
- [ ] Add comments to new test functions explaining coverage

## Technical Details

**Test fixture format** (progress file):
```
# Ralphex Progress Log
Plan: test-plan.md
Branch: test-branch
Mode: full
Started: 2026-01-22 10:00:00
------------------------------------------------------------
[26-01-22 10:00:01] Regular output line

--- Task iteration 1 ---
[26-01-22 10:00:02] Task 1 output
[ERROR] Something went wrong
[WARN] This is a warning

--- Claude review iteration 1 ---
[26-01-22 10:01:00] Review output
=== SIGNAL: COMPLETED ===
```

**CSS classes to verify**:
- `.output-line` - regular output
- `.output-error` or `.error` - error lines
- `.output-warn` or `.warn` - warning lines
- `.section-header` - collapsible sections
- `#status-badge` - status indicator

## Post-Completion

- Run tests in CI to verify they work in headless mode
- Consider adding screenshot capture on test failure for debugging
