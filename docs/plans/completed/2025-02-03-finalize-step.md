# Finalize Step Implementation Plan

## Overview

Add optional post-completion finalize step that runs after successful review phases. Executes a customizable prompt (default: commit rebase) once, best-effort (failures logged but don't block success).

## Context

- Triggers on: ModeFull, ModeReview, ModeCodexOnly (modes with review pipeline)
- Disabled by default (`finalize_enabled = false`)
- Uses task color (green) for output
- Runs once, no signal loop
- Template variables supported (`{{DEFAULT_BRANCH}}`, etc.)

## Tasks

### 1. Add Phase Constant

**Files:**
- Modify: `pkg/processor/section.go`

- [x] Add `PhaseFinalize Phase = "finalize"` constant

### 2. Add Color Mapping

**Files:**
- Modify: `pkg/progress/progress.go`

- [x] Add `PhaseFinalize = processor.PhaseFinalize` alias with other phase aliases
- [x] Add `c.phases[PhaseFinalize] = c.task` in `NewColors()` function

### 3. Add Config Fields

**Files:**
- Modify: `pkg/config/config.go`

- [x] Add `FinalizeEnabled bool` and `FinalizeEnabledSet bool` to Config struct
- [x] Add `finalizePromptFile = "finalize.txt"` constant

### 4. Add Prompt Loading

**Files:**
- Modify: `pkg/config/prompts.go`

- [x] Add `Finalize string` to Prompts struct
- [x] Add loading logic in `Load()` method for finalize prompt

### 5. Wire Config Assembly

**Files:**
- Modify: `pkg/config/config.go`

- [x] Add `FinalizeEnabled` and `FinalizeEnabledSet` to values loader
- [x] Add `FinalizePrompt` assignment in `loadConfigFromDirs()`

### 6. Add Default Config Value

**Files:**
- Modify: `pkg/config/defaults/config`

- [x] Add commented `# finalize_enabled = false`

### 7. Create Default Prompt

**Files:**
- Create: `pkg/config/defaults/prompts/finalize.txt`

- [x] Add rebase-based finalize prompt with `{{DEFAULT_BRANCH}}` variable

### 8. Add Runner Config Field

**Files:**
- Modify: `pkg/processor/runner.go`

- [x] Add `FinalizeEnabled bool` to runner Config struct

### 9. Implement runFinalize Method

**Files:**
- Modify: `pkg/processor/runner.go`

- [x] Add `runFinalize(ctx context.Context)` method
- [x] Set phase, print section header, run claude, handle errors (best effort)

### 10. Call Finalize from Modes

**Files:**
- Modify: `pkg/processor/runner.go`

- [x] Call `r.runFinalize(ctx)` at end of `runFull()` before success message
- [x] Call `r.runFinalize(ctx)` at end of `runReviewOnly()` before success message
- [x] Call `r.runFinalize(ctx)` at end of `runCodexOnly()` before success message

### 11. Wire Config in main.go

**Files:**
- Modify: `cmd/ralphex/main.go`

- [x] Pass `FinalizeEnabled` from AppConfig to runner Config

### 12. Add Tests

**Files:**
- Modify: `pkg/processor/runner_test.go`
- Modify: `pkg/config/config_test.go`
- Modify: `pkg/config/prompts_test.go`

- [x] Test finalize runs when enabled
- [x] Test finalize skipped when disabled
- [x] Test finalize failure doesn't block success
- [x] Test config loading with finalize_enabled
- [x] Test prompt loading for finalize.txt

### 13. Update Documentation

**Files:**
- Modify: `CLAUDE.md`

- [x] Document finalize step in "Key Patterns" or new section
- [x] Document config option and prompt file

### 14. Final Validation

- [x] Run `make test`
- [x] Run `make lint`
- [x] Manual e2e test with toy project
- [x] Move plan to `docs/plans/completed/`
