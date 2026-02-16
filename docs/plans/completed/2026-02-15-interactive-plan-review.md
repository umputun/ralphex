# Interactive Plan Review in --plan Mode

## Overview

Add an "Interactive review" option to the plan draft review menu in `ralphex --plan`. When selected, opens `$EDITOR` with the plan draft in a temp file. On save and close, computes a unified diff and feeds it back as revision feedback through the existing revise mechanism. If the user closes the editor without changes, re-shows the menu. This provides direct text editing for plan annotation without requiring any specific terminal emulator.

## Context (from discovery)

- Files/components involved: `pkg/input/input.go`, `pkg/input/input_test.go`
- Current `AskDraftReview` presents 3 options: Accept, Revise, Reject
- Options are shown via `selectWithNumbers` (not fzf)
- Runner in `pkg/processor/runner.go` passes feedback string through unchanged — content format (text vs diff) is irrelevant to it
- `TerminalCollector` struct has `stdin`/`stdout` fields for testing; editor launch needs testability via a similar pattern
- `github.com/pmezard/go-difflib` is already vendored (via testify) — use `difflib.GetUnifiedDiffString` for diff computation
- Current `AskDraftReview` creates a second `bufio.NewReader` for revision feedback — when wrapping in a loop, must create reader once at method top and thread it through to avoid losing buffered data

## Development Approach

- **Testing approach**: Regular (code first, then tests)
- The change is contained in a single package (`pkg/input`)
- No runner or progress changes needed — "Interactive review" maps to `("revise", diff)` at the interface boundary
- Editor lookup order: `$VISUAL` → `$EDITOR` → `vi` (standard unix convention, matches git)
- Temp file uses `.md` extension so editors apply markdown syntax highlighting
- No exported constant for interactive review — the action never crosses the package boundary, use local dispatch only

## Testing Strategy

- **Unit tests**: test `openEditor`, `computeDiff`, and the modified `AskDraftReview` flow
- Editor launch is abstracted behind `editorFunc` field on `TerminalCollector` so tests can override it
- Tests verify: diff computation, no-changes re-prompting, editor error handling, temp file cleanup

## Progress Tracking

- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix

## Implementation Steps

### Task 1: Add openEditor and computeDiff methods

**Files:**
- Modify: `pkg/input/input.go`
- Modify: `pkg/input/input_test.go`

- [x] add `editorFunc` field to `TerminalCollector` (type `func(ctx context.Context, content string) (string, error)`) for testability — nil means use real editor
- [x] add `openEditor` method that: creates temp `.md` file with plan content, looks up editor (`$VISUAL` → `$EDITOR` → `vi`), runs via `exec.CommandContext` with stdin/stdout/stderr connected to os, reads file back, cleans up temp file. If editor command not found, return error with "set $EDITOR environment variable" message
- [x] add `computeDiff` method that uses `difflib.GetUnifiedDiffString` from `github.com/pmezard/go-difflib` with 2 lines of context, returns diff string (empty if no changes)
- [x] write tests for `computeDiff` — changed content produces unified diff, unchanged content returns empty string
- [x] write test for `openEditor` with real temp file — verify file creation, content writing, and cleanup (use a simple editor command like `cat` or `true`)
- [x] run `go test ./pkg/input/...` — must pass before next task

### Task 2: Add interactive review option to AskDraftReview

**Files:**
- Modify: `pkg/input/input.go`
- Modify: `pkg/input/input_test.go`

- [x] refactor `AskDraftReview` to create `bufio.Reader` once at method top and thread it through `selectWithNumbers` and revision feedback reading — prevents data loss when looping
- [x] wrap option selection in a loop: show 4 options (Accept, Revise, Interactive review, Reject). If "Interactive review" is selected: call `editorFunc` (or `openEditor` if nil), compute diff. If diff is non-empty, return `("revise", diff)`. If diff is empty or editor errors (log warning), continue loop. Accept/Revise/Reject break out as before
- [x] write test for interactive review with changes — mock `editorFunc` to return modified content, verify returns `("revise", diff)`
- [x] write test for interactive review without changes — mock `editorFunc` to return same content, then simulate "Accept" on re-prompt, verify returns `("accept", "")`
- [x] write test for interactive review with editor error — mock `editorFunc` to return error, verify re-prompts menu
- [x] verify existing `AskDraftReview` tests still pass (accept, revise, reject paths unchanged)
- [x] run `go test ./pkg/input/...` — must pass before next task

### Task 3: Verify acceptance criteria

- [x] run full unit test suite: `go test ./...`
- [x] run linter: `golangci-lint run`
- [x] verify test coverage for `pkg/input` meets 80%+

### Task 4: [Final] Update documentation

- [x] update CLAUDE.md "Plan Creation Mode" section to mention interactive review option
- [x] update README.md plan creation section if needed

## Technical Details

**Editor launch:**
- `os.CreateTemp("", "ralphex-plan-*.md")` for temp file
- Write plan content, close file
- Look up editor: `$VISUAL` → `$EDITOR` → `vi`
- `exec.CommandContext(ctx, editor, tmpFile)` with `Stdin=os.Stdin`, `Stdout=os.Stdout`, `Stderr=os.Stderr`
- Read file back after editor exits
- Clean up temp file with defer
- If editor not found: return error with helpful message

**Diff computation:**
- Use `difflib.GetUnifiedDiffString` from `github.com/pmezard/go-difflib` (already vendored via testify)
- Headers: `--- original` / `+++ annotated`
- 2 lines of context around changes
- Returns empty string when content is identical

**bufio.Reader threading:**
- Current code creates separate `bufio.Reader` instances in `selectWithNumbers` and for revision feedback
- With a loop, this loses buffered data from piped/test input
- Fix: create reader once at top of `AskDraftReview`, pass to `selectWithNumbers` (add optional reader parameter, same pattern as `readCustomAnswer`)

**Menu flow:**
```
━━━ Plan Draft ━━━
<rendered plan>
━━━━━━━━━━━━━━━━━━

Review the plan draft
  1) Accept
  2) Revise
  3) Interactive review
  4) Reject
Enter number (1-4):
```

If user picks 3 → editor opens → diff computed → if non-empty, returns `("revise", diff)` → if empty, loop back to menu.
