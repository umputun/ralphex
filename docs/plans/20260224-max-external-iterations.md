# Add configurable iteration limit for external review phase

## Overview

Two changes:

1. **New config option**: add `max_external_iterations` config field and `--max-external-iterations` CLI flag to control external review loop iterations independently from `--max-iterations`. Currently the limit is derived as `max(3, max_iterations/5)` with no override, coupling task phase tuning with external review tuning.

2. **Fix constant inconsistency**: `runExternalReviewLoop()` uses literal `3` and `5` instead of the declared constants `minCodexIterations` and `codexIterationDivisor`. The constants exist at `runner.go:25-26` but the call site at line 547 ignores them.

Related to: #159

## Context (from discovery)

- `pkg/processor/runner.go:547` — hardcoded `max(3, r.cfg.MaxIterations/5)` in `runExternalReviewLoop()`
- `pkg/processor/runner.go:22-30` — constants `minCodexIterations=3`, `codexIterationDivisor=5` (declared but unused at call site)
- `pkg/processor/runner.go:44-58` — `processor.Config` struct
- `cmd/ralphex/main.go:33` — `--max-iterations` CLI flag (default 50)
- `cmd/ralphex/main.go:725-738` — `createRunner()` builds processor.Config
- `pkg/config/values.go` — `Values` struct + `mergeFrom()` + INI parsing
- `pkg/config/config.go:40-96` — `Config` struct
- `pkg/config/defaults/config:106-109` — `task_retry_count` pattern for reference

## Development Approach

- **testing approach**: regular (code first, then tests)
- config parsing/merge follows `TaskRetryCount` pattern; CLI flag follows `--max-iterations` pattern
- `*Set` bool NOT needed: 0 always means "auto/derive from max_iterations" (unlike `task_retry_count` where explicit 0 differs from default 1)
- precedence: CLI flag > config file > derived formula
- `createRunner()` needs new dual-source logic (CLI > config > 0) — no existing field has this, so this is a new wiring pattern there

## Implementation Steps

### Task 1: Add config field and parsing

**Files:**
- Modify: `pkg/config/values.go`
- Modify: `pkg/config/config.go`
- Modify: `pkg/config/defaults/config`
- Modify: `pkg/config/values_test.go`
- Modify: `pkg/config/config_test.go`

- [ ] add `MaxExternalIterations int` to `Values` struct
- [ ] add INI parsing in `parseValuesFromSection()` (validate non-negative, follow `task_retry_count` pattern)
- [ ] add merge logic in `mergeFrom()` (simple `> 0` override, no `*Set` needed)
- [ ] add `MaxExternalIterations int` to `config.Config` struct with `json:"max_external_iterations"` tag
- [ ] wire Values → Config in `Load()`
- [ ] add commented default to `pkg/config/defaults/config` after `task_retry_count` block
- [ ] write tests in `values_test.go`: parsing valid value, zero, negative → error
- [ ] write tests in `values_test.go`: merge behavior (non-zero overrides, zero preserves, global=10 + local unset → preserves 10)
- [ ] write test in `config_test.go`: config loads `max_external_iterations`
- [ ] run `go test ./pkg/config/...` — must pass before task 2

### Task 2: Add CLI flag, wire to processor, fix constants

**Files:**
- Modify: `cmd/ralphex/main.go`
- Modify: `pkg/processor/runner.go`
- Modify: `pkg/processor/runner_test.go`

- [ ] add `MaxExternalIterations int` to `opts` struct (long: `max-external-iterations`, default: 0, description: "override external review iteration limit (0 = auto)")
- [ ] add `MaxExternalIterations int` to `processor.Config`
- [ ] wire in `createRunner()`: new dual-source pattern — use CLI value if > 0, else config value, else 0
- [ ] fix `runExternalReviewLoop()`: replace literal `3` and `5` with `minCodexIterations` and `codexIterationDivisor`
- [ ] update `runExternalReviewLoop()` to use `r.cfg.MaxExternalIterations` when > 0, fall back to derived formula
- [ ] write test: explicit `MaxExternalIterations` is used when set
- [ ] write test: derived formula used when `MaxExternalIterations` is 0
- [ ] write test: CLI flag overrides config value (CLI=5, config=10 → uses 5)
- [ ] run `go test ./pkg/processor/... ./cmd/ralphex/...` — must pass before task 3

### Task 3: Verify and update documentation

**Files:**
- Modify: `CLAUDE.md`
- Modify: `README.md`
- Modify: `llms.txt`

- [ ] run full test suite: `go test ./...`
- [ ] run linter: `golangci-lint run --max-issues-per-linter=0 --max-same-issues=0`
- [ ] run formatters: `~/.claude/format.sh`
- [ ] update README.md CLI usage / customization sections with new option
- [ ] update llms.txt with new config option
- [ ] update CLAUDE.md if needed
- [ ] move this plan to `docs/plans/completed/`

## Technical Details

**Config**: `max_external_iterations` (INI), `MaxExternalIterations` (Go)
- Type: int, default: 0 (auto)
- Valid range: non-negative
- No `*Set` needed — 0 always means "derive from max_iterations"

**Updated formula in `runExternalReviewLoop()`**:
```go
maxIterations := max(minCodexIterations, r.cfg.MaxIterations/codexIterationDivisor)
if r.cfg.MaxExternalIterations > 0 {
    maxIterations = r.cfg.MaxExternalIterations
}
```

## Post-Completion

**Manual verification:**
- test with `--max-external-iterations 2` CLI flag to verify loop limits correctly
- test with `max_external_iterations = 2` in config file (no CLI flag) to verify config path
- test without flag or config to verify default derived behavior unchanged
