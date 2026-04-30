# Add Provider Override CLI Flags

## Overview

Add per-run CLI flags that override configured provider settings for Claude-compatible task/review execution and external review tooling. This lets users switch provider combinations without maintaining separate config profiles.

## Context

- Files involved:
  - `cmd/ralphex/main.go`
  - `cmd/ralphex/main_test.go`
  - `README.md`
  - `llms.txt`
  - `docs/custom-providers.md`
- Related patterns:
  - Existing CLI flags are defined in `opts` using `jessevdk/go-flags`
  - `applyCLIOverrides()` already mutates loaded config for per-run overrides
  - `markFlagsSet()` and `isFlagSet()` already handle explicit zero-value duration overrides
  - `processor.New()` reads `cfg.AppConfig.ClaudeCommand`, `ClaudeArgs`, `ExternalReviewTool`, and `CustomReviewScript`
  - `checkClaudeDep()` validates `cfg.ClaudeCommand`, so provider command overrides must be applied before dependency checking
- Dependencies:
  - No new external dependencies
  - Use existing `go-flags` support for string flags and `choice` validation

## Development Approach

- **Testing approach**: Regular code-first implementation with focused unit tests for CLI parsing, override precedence, explicit empty `--claude-args`, and dependency-check timing
- Complete each task fully before moving to the next
- Preserve existing config precedence; CLI flags override loaded local/global/default config for a single run
- Prefer hyphenated public CLI names to match existing flags, while supporting underscore aliases for config-shaped names where requested
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Add CLI flags and apply them before dependency checks

**Files:**
- Modify: `cmd/ralphex/main.go`

- [x] Add visible CLI flags to `opts`: `--claude-command`, `--claude-args`, `--external-review-tool`, and `--custom-review-script`
- [x] Add `choice:"codex"`, `choice:"custom"`, and `choice:"none"` validation to `--external-review-tool`
- [x] Add hidden underscore aliases for config-style spelling: `--claude_command`, `--claude_args`, `--external_review_tool`, and `--custom_review_script`
- [x] Extend `opts` with set-tracking booleans for the new visible flags and hidden aliases so empty string overrides are detectable
- [x] Extend `markFlagsSet()` to populate the new set-tracking booleans
- [x] Extend `validateFlags()` to reject conflicting visible/alias values when both forms are provided with different values
- [x] Extend `applyCLIOverrides()` so explicitly set CLI values override `cfg.ClaudeCommand`, `cfg.ClaudeArgs`, `cfg.ExternalReviewTool`, and `cfg.CustomReviewScript`
- [x] Preserve explicit empty `--claude-args=` as a real override so users can clear default Claude flags for wrappers
- [x] Move `applyCLIOverrides(o, cfg)` to immediately after `config.Load()` and before `checkClaudeDep(cfg)`
- [x] Remove or avoid duplicate later override application after branch detection
- [x] Run `go test ./cmd/ralphex` - must pass before task 2

### Task 2: Add CLI override tests

**Files:**
- Modify: `cmd/ralphex/main_test.go`

- [x] Add tests that `--claude-command` overrides `cfg.ClaudeCommand`
- [x] Add tests that `--claude-args` overrides `cfg.ClaudeArgs`
- [x] Add tests that `--claude-args=` clears a non-empty configured value
- [x] Add tests that `--external-review-tool` overrides `cfg.ExternalReviewTool`
- [x] Add tests that `--custom-review-script` overrides `cfg.CustomReviewScript`
- [x] Add parser-backed tests for underscore aliases, including `--external_review_tool`
- [x] Add conflict validation tests for visible and underscore alias forms with different values
- [x] Add a regression test showing `--claude-command` is applied before `checkClaudeDep()` by using a temporary executable command while config points to a missing command
- [x] Run `go test ./cmd/ralphex` - must pass before task 3

### Task 3: Update user-facing documentation

**Files:**
- Modify: `README.md`
- Modify: `llms.txt`
- Modify: `docs/custom-providers.md`

- [x] Add the new flags to the README options table
- [x] Add a README example for one-off provider selection using `--claude-command`, `--claude-args=`, `--external-review-tool`, and `--custom-review-script`
- [x] Update README configuration/provider sections to explain CLI flags override config for a single run
- [x] Update `llms.txt` quick usage and provider notes with the new flags
- [x] Update `docs/custom-providers.md` to mention CLI alternatives to config-file setup and the empty `--claude-args=` behavior
- [x] Run `go test ./cmd/ralphex` - must pass before task 4

### Task 4: Verify acceptance criteria

**Files:**
- No code changes expected

- [x] Run `make fmt`
- [x] Run `make test`
- [x] Run `make lint`
- [x] Run `make build`
- [x] Run `./scripts/internal/prep-toy-test.sh`
- [x] Run `cd /tmp/ralphex-test && .bin/ralphex docs/plans/fix-issues.md`
- [x] Verify the toy project run reaches completion and moves the plan to `docs/plans/completed/`

### Task 5: Update plan lifecycle

**Files:**
- Move: this plan file to `docs/plans/completed/`

- [x] Confirm README, `llms.txt`, and `docs/custom-providers.md` cover the user-facing flag behavior
- [x] Move this plan to `docs/plans/completed/`
