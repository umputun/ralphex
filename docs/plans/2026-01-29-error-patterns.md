# Error Pattern Detection Implementation Plan

Related to: #47

## Overview

Add configurable error pattern detection for Claude and Codex outputs. When patterns like "You've hit your limit" are detected, ralphex gracefully exits with an informative message instead of continuing to loop.

## Context

- **Problem**: When Claude/Codex hits rate limits or API errors, they don't follow prompt instructions to emit signals. ralphex continues looping, falsely reporting "issues fixed".
- **Solution**: Configurable substring patterns checked after each execution. On match, return structured error and exit gracefully.

## Tasks

### 1. Add Config Fields

**Files:**
- Modify: `pkg/config/values.go`
- Modify: `pkg/config/defaults/config`

- [ ] Add `ClaudeErrorPatterns []string` to `Values` struct
- [ ] Add `CodexErrorPatterns []string` to `Values` struct
- [ ] Parse comma-separated patterns from config (trim spaces before/after each pattern)
- [ ] Add embedded defaults: Claude = "You've hit your limit", Codex = "Rate limit,quota exceeded"
- [ ] Add tests for pattern parsing including whitespace trimming
- [ ] Verify tests pass

### 2. Add Error Type

**Files:**
- Modify: `pkg/executor/executor.go`

- [ ] Add `ErrPatternMatch` error type with `Pattern` and `HelpCmd` fields
- [ ] Add `checkErrorPatterns(output string, patterns []string) *ErrPatternMatch` helper
- [ ] Case-insensitive substring matching
- [ ] Add tests for pattern matching
- [ ] Verify tests pass

### 3. Integrate in Claude Executor

**Files:**
- Modify: `pkg/executor/executor.go`

- [ ] Add `ErrorPatterns []string` field to `ClaudeOptions`
- [ ] Check patterns after execution in `Run()`
- [ ] Return `ErrPatternMatch` with `HelpCmd: "claude /usage"` on match
- [ ] Add tests for error pattern detection
- [ ] Verify tests pass

### 4. Integrate in Codex Executor

**Files:**
- Modify: `pkg/executor/codex.go`

- [ ] Add `ErrorPatterns []string` field to `CodexOptions`
- [ ] Check patterns after execution in `Run()`
- [ ] Return `ErrPatternMatch` with `HelpCmd: "codex /status"` on match
- [ ] Add tests for error pattern detection
- [ ] Verify tests pass

### 5. Pass Patterns from Config to Executors

**Files:**
- Modify: `pkg/processor/runner.go`

- [ ] Pass `cfg.ClaudeErrorPatterns` to `ClaudeOptions.ErrorPatterns`
- [ ] Pass `cfg.CodexErrorPatterns` to `CodexOptions.ErrorPatterns`
- [ ] Verify tests pass

### 6. Handle Error in Runner

**Files:**
- Modify: `pkg/processor/runner.go`

- [ ] Check for `ErrPatternMatch` after claude/codex calls
- [ ] Log: `error: detected "<pattern>" in <tool> output`
- [ ] Log: `run '<help_cmd>' for more information`
- [ ] Return error (graceful exit, not panic)
- [ ] Add integration test for error pattern flow
- [ ] Verify tests pass

### 7. Documentation

**Files:**
- Modify: `README.md`
- Modify: `CLAUDE.md`

- [ ] Document `claude_error_patterns` and `codex_error_patterns` config options
- [ ] Explain pattern matching behavior (case-insensitive substring, whitespace trimmed)
- [ ] List default patterns

### 8. Final Validation

- [ ] Run full test suite
- [ ] Run linter
- [ ] Test manually with simulated error output
- [ ] Close #47
- [ ] Move plan to `docs/plans/completed/`
