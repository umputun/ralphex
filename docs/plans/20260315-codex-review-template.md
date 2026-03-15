# Externalize codex review prompt as configurable template

## Overview
- Move the hardcoded `buildCodexPrompt` inline prompt to a file-backed template (`codex_review.txt`)
- Add `{{PREVIOUS_REVIEW_CONTEXT}}` template variable so iteration history is part of the template, not hardcoded Go logic
- Add `{{PROGRESS_FILE}}` to the template so external review tools get access to full iteration history
- Apply the same `{{PREVIOUS_REVIEW_CONTEXT}}` pattern to `custom_review.txt` for consistency
- Related to #215 (repeated findings across external review iterations)

## Context
- All prompts except the codex review prompt are already file-backed templates
- `buildCodexPrompt` in `runner.go:767-817` constructs the prompt inline with `fmt.Sprintf`
- `buildCustomReviewPrompt` in `prompts.go:200-217` uses a template file but hardcodes the `PREVIOUS REVIEW CONTEXT` block
- The existing prompt loading pattern: constant → `Prompts` struct field → `Config` field → embedded default file
- Naming note: `codex_review.txt` (sent to codex) pairs with `codex.txt` (Claude evaluates codex output). This differs from the custom pattern (`custom_review.txt`/`custom_eval.txt`) but renaming `codex.txt` → `codex_eval.txt` would break existing user configs. Both files get clear header comments documenting their purpose.

## Solution Overview
- Create `codex_review.txt` template with all variables (`{{DIFF_INSTRUCTION}}`, `{{PROGRESS_FILE}}`, `{{PLAN_FILE}}`, `{{PREVIOUS_REVIEW_CONTEXT}}`)
- Add `{{PREVIOUS_REVIEW_CONTEXT}}` as a new template variable built by Go code: empty on first iteration, formatted context block on subsequent iterations
- Replace `buildCodexPrompt` inline construction with template loading + variable substitution
- Update `buildCustomReviewPrompt` to use `{{PREVIOUS_REVIEW_CONTEXT}}` instead of hardcoding the block
- Add `{{PREVIOUS_REVIEW_CONTEXT}}` to `custom_review.txt` default template

## Technical Details
- `{{PREVIOUS_REVIEW_CONTEXT}}` is built by `buildPreviousContext(claudeResponse)` method as either empty string or the full block:
  ```
  ---
  PREVIOUS REVIEW CONTEXT:
  Claude (previous reviewer) responded to your findings:

  <claude response>

  Re-evaluate considering Claude's arguments. If Claude's fixes are correct, acknowledge them.
  If Claude's arguments are invalid, explain why the issues still exist.
  ```
- `{{DIFF_INSTRUCTION}}` already exists and handles first vs subsequent iteration diff commands
- Extend `replaceVariablesWithIteration` to also accept `claudeResponse` and handle `{{PREVIOUS_REVIEW_CONTEXT}}` substitution, so both `buildCodexPrompt` and `buildCustomReviewPrompt` use the same expansion path

## Development Approach
- **testing approach**: regular (code first, then tests)
- complete each task fully before moving to the next
- every task includes new/updated tests
- all tests must pass before starting next task
- update this plan file when scope changes during implementation

## Testing Strategy
- **unit tests**: test `buildCodexPrompt` produces correct output from template with all variable combinations
- **unit tests**: test `buildCustomReviewPrompt` correctly expands `{{PREVIOUS_REVIEW_CONTEXT}}`
- **unit tests**: test `{{PREVIOUS_REVIEW_CONTEXT}}` is empty on first iteration, populated on subsequent

## Implementation Steps

### Task 1: Add codex_review.txt template file and config wiring

**Files:**
- Create: `pkg/config/defaults/prompts/codex_review.txt`
- Modify: `pkg/config/config.go`
- Modify: `pkg/config/prompts.go`
- Modify: `pkg/config/config_test.go`
- Modify: `pkg/config/defaults_test.go`

- [x] create `pkg/config/defaults/prompts/codex_review.txt` with the externalized version of the current inline prompt, using `{{PLAN_FILE}}`, `{{DIFF_INSTRUCTION}}`, `{{PROGRESS_FILE}}`, `{{PREVIOUS_REVIEW_CONTEXT}}`
- [x] add `codexReviewPromptFile = "codex_review.txt"` constant in `config.go`
- [x] add `CodexReviewPrompt string` field to `Config` (with `json:"-"` tag)
- [x] add `CodexReview string` field to `Prompts` struct in `prompts.go`
- [x] add `loadPromptWithLocalFallback` call in `promptLoader.Load()` for the new prompt
- [x] wire `CodexReviewPrompt: prompts.CodexReview` in `loadConfigFromDirs` assembly block
- [x] update `expectedPrompts` lists in `defaults_test.go` (lines ~258, ~300) to include `codex_review.txt`
- [x] update `Test_defaultsFS_PromptFiles` and `Test_defaultsFS_AllFilesPresent` in `config_test.go` to include the new file
- [x] run `go test ./pkg/config/...` - must pass before next task

### Task 2: Add {{PREVIOUS_REVIEW_CONTEXT}} variable and refactor buildCodexPrompt

**Files:**
- Modify: `pkg/processor/prompts.go`
- Modify: `pkg/processor/runner.go`
- Modify: `pkg/processor/prompts_test.go`
- Modify: `pkg/processor/runner_test.go`
- Modify: `pkg/processor/export_test.go`

- [ ] add `buildPreviousContext(claudeResponse string) string` method to Runner that returns empty string or the formatted PREVIOUS REVIEW CONTEXT block
- [ ] extend `replaceVariablesWithIteration` to accept `claudeResponse` parameter and replace `{{PREVIOUS_REVIEW_CONTEXT}}` with the output of `buildPreviousContext`
- [ ] replace `buildCodexPrompt` inline construction with template-based approach using `r.cfg.AppConfig.CodexReviewPrompt` and `replaceVariablesWithIteration`
- [ ] write tests for `buildPreviousContext` - empty on first call, populated with response on subsequent
- [ ] write tests for `buildCodexPrompt` with the new template - verify all variables are expanded correctly
- [ ] update `TestRunner_BuildCodexPrompt_CompletedDir` to provide `AppConfig` with `CodexReviewPrompt` (required - will nil-panic without this)
- [ ] update `TestBuildCodexPrompt` export helper in `export_test.go` if signature changes
- [ ] run `go test ./pkg/processor/...` - must pass before next task

### Task 3: Update custom_review.txt to use {{PREVIOUS_REVIEW_CONTEXT}}

**Files:**
- Modify: `pkg/config/defaults/prompts/custom_review.txt`
- Modify: `pkg/processor/prompts.go`
- Modify: `pkg/processor/prompts_test.go`

- [ ] add `{{PREVIOUS_REVIEW_CONTEXT}}` to `custom_review.txt` template, replacing the hardcoded block in Go
- [ ] refactor `buildCustomReviewPrompt` to use `replaceVariablesWithIteration` (with claudeResponse) instead of appending the block in Go code
- [ ] write tests for `buildCustomReviewPrompt` with `{{PREVIOUS_REVIEW_CONTEXT}}` expansion (both empty and populated)
- [ ] run `go test ./pkg/processor/...` - must pass before next task

### Task 4: Verify acceptance criteria
- [ ] verify codex review prompt is fully configurable via `codex_review.txt`
- [ ] verify custom review prompt uses `{{PREVIOUS_REVIEW_CONTEXT}}` consistently
- [ ] verify `{{PROGRESS_FILE}}` is available in both prompts
- [ ] verify default behavior matches current behavior (no regression)
- [ ] run full test suite: `go test ./...`
- [ ] run linter: `golangci-lint run`
- [ ] verify test coverage meets 80%+

### Task 5: [Final] Update documentation
- [ ] update CLAUDE.md with new template file and `{{PREVIOUS_REVIEW_CONTEXT}}` variable
- [ ] update `llms.txt` with new prompt file and template variable documentation
- [ ] update README.md customization section if needed
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

**Manual verification:**
- run e2e test with toy project to verify codex review loop still works correctly
- test with customized `codex_review.txt` to verify user overrides work
