# Plan Draft Preview with User Feedback Loop

## Overview

Add a draft preview step to plan creation mode where users can review, revise, or reject the generated plan before it's written to disk. Currently, Claude creates the plan file directly without user visibility or feedback opportunity. This change introduces a `PLAN_DRAFT` signal that presents the plan for review, with options to accept, revise (with feedback), or reject.

## Context

- Files involved:
  - `pkg/processor/signals.go` - signal parsing
  - `pkg/processor/runner.go` - plan creation loop
  - `pkg/input/input.go` - user input collection
  - `pkg/config/defaults/prompts/make_plan.txt` - plan creation prompt
- New package: `pkg/render/` for glamour-based markdown rendering
- Dependencies: `github.com/charmbracelet/glamour` for terminal markdown rendering

## Development Approach

- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Add glamour dependency and create render package

**Files:**
- Create: `pkg/render/markdown.go`
- Create: `pkg/render/markdown_test.go`

- [x] run `go get github.com/charmbracelet/glamour`
- [x] create `pkg/render/markdown.go` with `RenderMarkdown(content string, noColor bool) (string, error)`
- [x] handle noColor by returning plain content
- [x] use `glamour.NewTermRenderer` with auto style and word wrap
- [x] write tests for RenderMarkdown with color enabled
- [x] write tests for RenderMarkdown with noColor fallback
- [x] run `go test ./pkg/render` - must pass before task 2

### Task 2: Add PLAN_DRAFT signal parsing

**Files:**
- Modify: `pkg/processor/signals.go`
- Modify: `pkg/processor/signals_test.go`

- [x] add `SignalPlanDraft = "<<<RALPHEX:PLAN_DRAFT>>>"`
- [x] add `planDraftSignalRe` regex to extract content between PLAN_DRAFT and END markers
- [x] add `ParsePlanDraftPayload(output string) (string, error)` returning plan content
- [x] add `IsPlanDraft(signal string) bool` helper
- [x] write tests for ParsePlanDraftPayload with valid draft
- [x] write tests for ParsePlanDraftPayload with malformed/missing markers
- [x] write tests for IsPlanDraft
- [x] run `go test ./pkg/processor` - must pass before task 3

### Task 3: Add revision input collection

**Files:**
- Modify: `pkg/input/input.go`
- Modify: `pkg/input/input_test.go`

- [x] add `AskDraftReview(ctx, question string, planContent string) (action string, feedback string, error)` to Collector interface
- [x] implement in TerminalCollector: show rendered plan, present Accept/Revise/Reject options
- [x] if Revise selected, prompt for free-form feedback text
- [x] return action ("accept", "revise", "reject") and feedback (empty for accept/reject)
- [x] write tests for AskDraftReview with accept action
- [x] write tests for AskDraftReview with revise action and feedback
- [x] write tests for AskDraftReview with reject action
- [x] run `go test ./pkg/input` - must pass before task 4

### Task 4: Update runner to handle PLAN_DRAFT loop

**Files:**
- Modify: `pkg/processor/runner.go`
- Modify: `pkg/processor/runner_test.go`

- [x] modify `runPlanCreation` to detect PLAN_DRAFT signal in output
- [x] when PLAN_DRAFT detected, call `inputCollector.AskDraftReview()`
- [x] if accept: log acceptance, continue (Claude will write file and emit PLAN_READY)
- [x] if revise: log feedback to progress file, re-run Claude with feedback context
- [x] if reject: return error indicating user rejected plan
- [x] add feedback to progress file format so Claude sees revision history
- [x] write tests for runPlanCreation with PLAN_DRAFT → accept flow
- [x] write tests for runPlanCreation with PLAN_DRAFT → revise → accept flow
- [x] write tests for runPlanCreation with PLAN_DRAFT → reject flow
- [x] run `go test ./pkg/processor` - must pass before task 5

### Task 5: Update make_plan.txt prompt

**Files:**
- Modify: `pkg/config/defaults/prompts/make_plan.txt`

- [x] add Step 3.5 before Step 4: "Present Draft for Review"
- [x] instruct Claude to emit `<<<RALPHEX:PLAN_DRAFT>>>..<<<RALPHEX:END>>>` with full plan content
- [x] instruct Claude to STOP after PLAN_DRAFT and wait for feedback
- [x] add instructions for handling revision feedback (re-emit PLAN_DRAFT with changes)
- [x] add instructions for handling rejection (emit TASK_FAILED)
- [x] update Step 4 to only execute after user accepts draft
- [x] verify prompt still references progress file for revision history

### Task 6: Update Logger interface for draft review

**Files:**
- Modify: `pkg/processor/runner.go` (Logger interface)
- Modify: `pkg/progress/logger.go`
- Modify: `pkg/progress/logger_test.go`

- [x] add `LogDraftReview(action string, feedback string)` to Logger interface
- [x] implement in progress.Logger: log draft review action and feedback if present
- [x] write tests for LogDraftReview with accept action
- [x] write tests for LogDraftReview with revise action and feedback
- [x] run `go test ./pkg/progress` - must pass before task 7

### Task 7: Verify acceptance criteria

- [x] run full test suite: `go test ./...`
- [x] run linter: `golangci-lint run`
- [x] verify test coverage meets 80%+

### Task 8: End-to-end testing with toy project

- [ ] run `./scripts/prep-toy-test.sh` to create test environment
- [ ] test plan mode: `ralphex --plan "add health endpoint"`
- [ ] verify draft is displayed with glamour rendering
- [ ] test Accept flow - plan file created
- [ ] test Revise flow - feedback incorporated, new draft shown
- [ ] test Reject flow - graceful exit
- [ ] verify progress file contains Q&A and draft review history

### Task 9: Update documentation

- [ ] update CLAUDE.md with new PLAN_DRAFT signal documentation
- [ ] update llms.txt if user-facing behavior changed
- [ ] move this plan to `docs/plans/completed/`

## Technical Details

**New signal format:**
```
<<<RALPHEX:PLAN_DRAFT>>>
# Plan Title

## Overview
...

## Tasks
...
<<<RALPHEX:END>>>
```

**Draft review actions:**
- `accept` - proceed to write plan file
- `revise` - re-run Claude with feedback, emit new PLAN_DRAFT
- `reject` - exit plan creation with error

**Progress file additions:**
```
[HH:MM:SS] DRAFT REVIEW: accept
```
or
```
[HH:MM:SS] DRAFT REVIEW: revise
[HH:MM:SS] FEEDBACK: user's revision feedback here
```

**Glamour integration:**
- Use `glamour.NewTermRenderer` with `WithAutoStyle()` for dark/light detection
- Use `WithWordWrap(80)` for consistent width
- Bypass glamour when `--no-color` flag is set
