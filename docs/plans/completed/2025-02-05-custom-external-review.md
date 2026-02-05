# Custom External Review Implementation Plan

## Overview

Add support for custom external review tools alongside codex. Users can specify their own script that wraps any AI (OpenRouter, local LLM, etc.) for code review.

## Context

- Issue: #59 - request for custom external review tools
- Current: only codex supported for external review
- Files involved: `pkg/config/`, `pkg/executor/`, `pkg/processor/`, `cmd/ralphex/`

## Design Decisions

**Config:**
- `external_review_tool = codex | custom | none` (default: codex)
- `custom_review_script = path/to/script.sh`
- `codex_enabled = false` maps to `none` for backward compat

**Script interface:**
- `script.sh <prompt-file>` - single arg, prompt file path
- Prompt contains diff instructions (AI runs git diff itself)
- Script outputs findings to stdout
- Signal format same as codex: `<<<RALPHEX:COMPLETED>>>`

**Prompt files:**
- `custom_review.txt` - instructions for custom tool (supports `{{DIFF_INSTRUCTION}}`, `{{GOAL}}`, `{{PLAN_FILE}}`, etc.)
- `custom_eval.txt` - Claude evaluates custom tool output

**CLI:**
- `--external-only` (-e) primary flag
- `--codex-only` (-c) deprecated alias
- Internal mode stays `ModeCodexOnly` for compat

**Template variable:**
- `{{DIFF_INSTRUCTION}}` - expands to appropriate git diff command based on iteration

## Tasks

### 1. Add Config Values

**Files:**
- Modify: `pkg/config/values.go`
- Modify: `pkg/config/config.go`
- Modify: `pkg/config/defaults/config`

- [x] Add `ExternalReviewTool string` to Values struct
- [x] Add `CustomReviewScript string` to Values struct
- [x] Add parsing in `parseConfigSection()`
- [x] Add merge logic in `mergeValues()`
- [x] Add to Config struct and `NewConfig()`
- [x] Add default config comments
- [x] Add tests for new config fields
- [x] Verify tests pass

### 2. Add Prompt Templates

**Files:**
- Create: `pkg/config/defaults/prompts/custom_review.txt`
- Create: `pkg/config/defaults/prompts/custom_eval.txt`

- [x] Create `custom_review.txt` with `{{DIFF_INSTRUCTION}}`, `{{GOAL}}`, `{{PLAN_FILE}}`, `{{DEFAULT_BRANCH}}`
- [x] Create `custom_eval.txt` for Claude to evaluate custom tool output (similar to codex.txt but generic)
- [x] Add prompts to Config struct
- [x] Add loading in prompt loader
- [x] Add tests for prompt loading

### 3. Add DIFF_INSTRUCTION Template Variable

**Files:**
- Modify: `pkg/processor/prompts.go`

- [x] Add `{{DIFF_INSTRUCTION}}` to `replacePromptVariables()`
- [x] Accept `isFirstIteration bool` parameter or store in runner state
- [x] Generate appropriate diff instruction based on iteration
- [x] Add tests for variable expansion

### 4. Create CustomExecutor

**Files:**
- Create: `pkg/executor/custom.go`
- Create: `pkg/executor/custom_test.go`

- [x] Define `CustomExecutor` struct with `Script`, `OutputHandler`, `ErrorPatterns` fields
- [x] Implement `Run(ctx context.Context, promptFile string) Result`
- [x] Write prompt content to temp file
- [x] Execute script with prompt file as arg
- [x] Stream stdout to OutputHandler
- [x] Detect signals in output
- [x] Check error patterns
- [x] Reuse `CommandRunner` interface for testability
- [x] Add comprehensive tests
- [x] Verify tests pass

### 5. Integrate in Runner

**Files:**
- Modify: `pkg/processor/runner.go`

- [x] Add `customExec *executor.CustomExecutor` field to Runner
- [x] Initialize in `New()` based on `ExternalReviewTool` config
- [x] Modify `runCodexLoop()` to dispatch to codex or custom based on config
- [x] Build custom prompt with `{{DIFF_INSTRUCTION}}` expanded per iteration
- [x] Use `custom_eval.txt` prompt for Claude evaluation when tool is custom
- [x] Handle `external_review_tool = none` (skip external review)
- [x] Backward compat: `codex_enabled = false` → treat as `none`
- [x] Add tests for custom review flow
- [x] Verify tests pass

### 6. Add CLI Flag Alias

**Files:**
- Modify: `cmd/ralphex/main.go`
- Modify: `cmd/ralphex/main_test.go`

- [x] Add `ExternalOnly bool` flag with `-e, --external-only`
- [x] Update `CodexOnly` description to "alias for --external-only (deprecated)"
- [x] Update `determineMode()` to check both flags
- [x] Add tests for flag precedence
- [x] Verify tests pass

### 7. Update Documentation

**Files:**
- Modify: `README.md`
- Modify: `llms.txt`
- Modify: `CLAUDE.md`

- [x] Add "Custom External Review" section to README.md
- [x] Document script interface (single arg: prompt file)
- [x] Document expected output format and signals
- [x] Document iteration behavior (branch diff vs uncommitted)
- [x] Document Docker considerations (script location, dependencies)
- [x] Add example custom script
- [x] Update llms.txt with custom review usage
- [x] Update CLAUDE.md with internal notes

### 8. Final Validation

- [x] Run full test suite: `make test`
- [x] Run linter: `make lint`
- [x] Test with toy project (full mode with custom script)
- [x] Test `--external-only` flag
- [x] Test `--codex-only` alias still works
- [x] Test `codex_enabled = false` backward compat
- [x] Move plan to `docs/plans/completed/`
