# Interactive Plan Creation Mode

## Overview

Add `--plan "description"` flag to ralphex that enables interactive plan creation through a dialogue with Claude. The loop pattern mirrors task execution: Claude explores the codebase, asks clarifying questions via structured signals, user answers via fzf-style terminal picker, and the loop continues until the plan is finalized.

## Context

- Files involved: `cmd/ralphex/main.go`, `pkg/processor/runner.go`, `pkg/processor/signals.go`, `pkg/config/prompts.go`
- Related patterns: existing execution loop with signal detection, progress file logging
- Dependencies: fzf (optional, fallback to numbered selection)

## Development Approach

- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- Reuse existing patterns: Runner with new mode, signal detection, progress logging
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Architecture

```
ralphex --plan "implement caching"
         │
         ▼
┌─────────────────────────────────────────────────────┐
│  Plan Creation Loop (in Runner)                     │
│                                                     │
│  iteration 1:                                       │
│    - run claude with make_plan.txt prompt           │
│    - claude explores codebase                       │
│    - claude emits QUESTION signal with JSON         │
│    - loop pauses, shows fzf picker                  │
│    - user selects answer                            │
│    - answer written to progress-plan-<name>.txt     │
│                                                     │
│  iteration 2:                                       │
│    - run claude with make_plan.txt prompt           │
│    - claude reads progress file, sees Q&A history   │
│    - continues planning or asks next question       │
│                                                     │
│  ...until PLAN_READY signal                         │
│    - plan written to docs/plans/<name>.md           │
└─────────────────────────────────────────────────────┘
```

## Signal Format

**Question signal:**
```
<<<RALPHEX:QUESTION>>>
{"question": "Which cache backend?", "options": ["Redis", "In-memory", "File-based"]}
<<<RALPHEX:END>>>
```

**Completion signal:**
```
<<<RALPHEX:PLAN_READY>>>
```

## Progress File Format

```
# Ralphex Plan Preparation Log
Request: implement caching for API responses
Started: 2026-01-25 10:30:00
------------------------------------------------------------

--- exploration ---
[26-01-25 10:30:05] analyzing codebase structure...
[26-01-25 10:30:12] found existing store layer in pkg/store/

--- question 1 ---
[26-01-25 10:30:15] QUESTION: Which cache backend?
[26-01-25 10:30:15] OPTIONS: Redis, In-memory, File-based
[26-01-25 10:30:45] ANSWER: Redis

--- finalizing ---
[26-01-25 10:32:00] writing plan to docs/plans/2026-01-25-api-caching.md
[26-01-25 10:32:05] <<<RALPHEX:PLAN_READY>>>
```

## Implementation Steps

### Task 1: Add new signals for plan creation

**Files:**
- Modify: `pkg/processor/signals.go`

- [x] add `SignalQuestion = "QUESTION"` constant
- [x] add `SignalPlanReady = "PLAN_READY"` constant
- [x] add `QuestionPayload` struct with `Question` and `Options` fields
- [x] add `ParseQuestionPayload(output string) (*QuestionPayload, error)` function
- [x] write tests for `ParseQuestionPayload` with valid JSON
- [x] write tests for `ParseQuestionPayload` with malformed JSON
- [x] write tests for `ParseQuestionPayload` when no question signal present
- [x] run `go test ./pkg/processor/...` - must pass before task 2

### Task 2: Add ModePlan and CLI flag

**Files:**
- Modify: `pkg/processor/runner.go`
- Modify: `cmd/ralphex/main.go`

- [x] add `ModePlan Mode = "plan"` constant in runner.go
- [x] add `--plan` string flag in opts struct (plan description)
- [x] add `PlanDescription` field to `processor.Config`
- [x] update `determineMode()` to return `ModePlan` when `--plan` is set
- [x] add validation: `--plan` conflicts with positional plan file argument
- [x] write tests for mode determination with `--plan` flag
- [x] write tests for conflict validation
- [x] run `go test ./...` - must pass before task 3

### Task 3: Add make_plan prompt

**Files:**
- Create: `pkg/config/defaults/prompts/make_plan.txt`
- Modify: `pkg/config/prompts.go`

- [x] create `make_plan.txt` prompt with instructions for:
  - reading progress file for context
  - exploring codebase to understand structure
  - asking clarifying questions via QUESTION signal
  - emitting PLAN_READY when done
- [x] add `MakePlan` field to `Prompts` struct
- [x] update `promptLoader.Load()` to load make_plan.txt
- [x] add template variables: `{{PLAN_DESCRIPTION}}`, `{{PROGRESS_FILE}}`
- [x] write tests for prompt loading
- [x] run `go test ./pkg/config/...` - must pass before task 4

### Task 4: Add terminal input collector

**Files:**
- Create: `pkg/input/input.go`
- Create: `pkg/input/input_test.go`

- [x] create `Collector` interface with `AskQuestion(question string, options []string) (string, error)`
- [x] implement `TerminalCollector` that:
  - tries fzf first if available
  - falls back to numbered selection with stdin
- [x] implement fzf-based selection using `exec.Command`
- [x] implement fallback numbered selection for no-fzf environments
- [x] write tests for `TerminalCollector` with mock stdin (fallback mode)
- [x] run `go test ./pkg/input/...` - must pass before task 5

### Task 5: Implement plan creation loop in Runner

**Files:**
- Modify: `pkg/processor/runner.go`

- [x] add `runPlanCreation(ctx context.Context) error` method
- [x] implement loop:
  - build prompt with `{{PLAN_DESCRIPTION}}` and `{{PROGRESS_FILE}}`
  - run claude executor
  - check for QUESTION signal → call input collector → log answer
  - check for PLAN_READY signal → exit loop
  - continue until max iterations or completion
- [x] add `PhasePlan Phase = "plan"` for progress coloring
- [x] add `SectionPlanIteration` section type
- [x] update `Run()` to route `ModePlan` to `runPlanCreation()`
- [x] write tests for plan creation loop with mock executor
- [x] write tests for question detection and answer logging
- [x] write tests for PLAN_READY completion
- [x] run `go test ./pkg/processor/...` - must pass before task 6

### Task 6: Update progress logger for plan mode

**Files:**
- Modify: `pkg/progress/progress.go`

- [x] update `progressFileName()` to handle plan mode: `progress-plan-<name>.txt`
- [x] add `LogQuestion(question string, options []string)` method
- [x] add `LogAnswer(answer string)` method
- [x] write tests for plan mode progress filename
- [x] write tests for question/answer logging format
- [x] run `go test ./pkg/progress/...` - must pass before task 7

### Task 7: Wire up main.go for plan mode

**Files:**
- Modify: `cmd/ralphex/main.go`

- [x] create input collector in `run()` when plan mode
- [x] pass collector to Runner via new config field or method
- [x] handle plan mode progress file naming
- [x] add startup info for plan mode
- [x] write integration test for plan flag parsing
- [x] run `go test ./cmd/ralphex/...` - must pass before task 8

### Task 8: Verify acceptance criteria

- [x] manual test: `ralphex --plan "add health check endpoint"` starts plan loop
- [x] manual test: question appears with fzf picker (or numbered fallback)
- [x] manual test: answer logged to progress file
- [x] manual test: loop continues after answer
- [x] manual test: PLAN_READY creates plan file in docs/plans/
- [x] run full test suite: `go test ./...`
- [x] run linter: `golangci-lint run`
- [x] verify test coverage meets 80%+

### Task 9: Update documentation

- [x] update README.md with `--plan` flag usage
- [x] update CLAUDE.md with plan mode details
- [x] add example plan creation workflow
- [x] move this plan to `docs/plans/completed/`

## Technical Details

**Question JSON schema:**
```json
{
  "question": "string - the question text",
  "options": ["array", "of", "string", "choices"],
  "context": "optional string - why this question matters"
}
```

**Progress file naming:**
- Plan description "implement caching" → sanitized to "implement-caching"
- Filename: `progress-plan-implement-caching.txt`

**Prompt template variables:**
- `{{PLAN_DESCRIPTION}}` - user's original request
- `{{PROGRESS_FILE}}` - path to progress file for context

## Post-Completion

**Future enhancements (separate PRs):**
- Web UI integration with SSE streaming of Q&A
- `--batch` mode for non-interactive usage
- Plan templates for common patterns
