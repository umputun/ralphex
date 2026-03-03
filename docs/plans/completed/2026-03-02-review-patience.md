# Early termination of stalemate external review loops

Related to: #176

## Overview

When the external review tool (codex/custom) and Claude can't agree on findings, the loop has no way to resolve the dispute. The external tool flags something that may not even be real and proposes a complete redesign as a fix, Claude correctly rejects the findings without making changes, the external tool insists next round. This is rare but when it happens the waste is significant - the loop only stops when `max_external_iterations` is hit, burning tokens and time.

Two changes:

1. **Stalemate detection (`review_patience`)**: track consecutive iterations where Claude's evaluation produces no commits. When the counter reaches the configured threshold, terminate the loop early - Claude wins.

2. **Manual break**: typing `break` + Enter during the external review loop terminates it early. The break signal is injected into the processor via a channel from `cmd/ralphex/`, keeping the processor package free of stdin/TTY dependencies. Break cancels the current executor run immediately via context cancellation.

## Context (from discovery)

Line numbers are approximate and may shift during implementation.

- `pkg/processor/runner.go:558` — `runExternalReviewLoop()` is the main loop
- `pkg/processor/runner.go:89-92` — `GitChecker` interface already has `HeadHash()`
- `pkg/processor/runner.go:101` — `Runner.git` field already holds `GitChecker`
- `pkg/processor/runner.go:44-59` — `Config` struct (add `ReviewPatience` here)
- `pkg/config/values.go:42` — `Values` struct (add `ReviewPatience` here)
- `pkg/config/config.go:65` — `Config` struct (add `ReviewPatience` here)
- `cmd/ralphex/main.go:34` — CLI flags (add `--review-patience` here)
- `cmd/ralphex/main.go:744-754` — `createRunner()` wires config (add review patience here)

## Development Approach

- **testing approach**: regular (code first, then tests)
- config parsing/merge follows `MaxExternalIterations` pattern exactly (0 = disabled, > 0 = active)
- no `*Set` bool needed: 0 means disabled (current behavior), any positive value enables stalemate detection
- note: `> 0` merge means local config cannot disable a global `review_patience = 5` with `review_patience = 0` — same trade-off as `MaxExternalIterations`, consistent behavior
- precedence: CLI flag > local config > global config > embedded default (0)
- `HeadHash()` already available via `GitChecker` interface — no git package changes needed
- break channel injected from `cmd/ralphex/` into Runner, not created inside processor — follows existing `InputCollector`/`GitChecker` injection pattern

## Implementation Steps

### Task 1: Add `review_patience` config field and parsing

**Files:**
- Modify: `pkg/config/values.go`
- Modify: `pkg/config/config.go`
- Modify: `pkg/config/defaults/config`
- Modify: `pkg/config/values_test.go`
- Modify: `pkg/config/config_test.go`

- [x] add `ReviewPatience int` to `Values` struct (after `MaxExternalIterations`)
- [x] add INI parsing in `parseValuesFromSection()` — validate non-negative, follow `MaxExternalIterations` pattern
- [x] add merge logic in `mergeFrom()` — simple `> 0` override, no `*Set` needed
- [x] add `ReviewPatience int` to `config.Config` struct with `json:"review_patience"` tag
- [x] wire Values → Config in `Load()`
- [x] add commented default to `pkg/config/defaults/config` after `max_external_iterations` block: `# review_patience = 0`
- [x] write tests in `values_test.go`: parsing valid value, zero, negative → error, invalid string → error
- [x] write tests in `values_test.go`: merge behavior (non-zero overrides, zero preserves, global+local merge)
- [x] write test in `config_test.go`: config loads `review_patience`
- [x] run `go test ./pkg/config/...` — must pass before task 2

### Task 2: Add CLI flag and wire to processor Config

**Files:**
- Modify: `cmd/ralphex/main.go`
- Modify: `pkg/processor/runner.go` (Config struct only)
- Modify: `cmd/ralphex/main_test.go`

- [x] add `ReviewPatience int` to `opts` struct with `long:"review-patience" default:"0" description:"terminate external review after N unchanged rounds (0 = disabled)"`
- [x] add `ReviewPatience int` to `processor.Config` struct
- [x] wire in `createRunner()`: CLI flag > config value > 0 (follows `MaxExternalIterations` pattern)
- [x] write test in `main_test.go`: CLI flag overrides config value (follow `max_external_iterations_cli_overrides_config` pattern)
- [x] run `go test ./cmd/ralphex/...` — must pass before task 3

### Task 3: Implement stalemate detection in external review loop

**Files:**
- Modify: `pkg/processor/runner.go` — `runExternalReviewLoop()`
- Modify: `pkg/processor/runner_test.go`

- [x] in `runExternalReviewLoop()`, before the for loop: initialize `unchangedRounds := 0`
- [x] after external tool runs but before Claude evaluation: capture `headBefore` via `r.git.HeadHash()`; if git is nil or HeadHash errors, skip stalemate check (graceful degradation)
- [x] after Claude evaluation: capture `headAfter` via `r.git.HeadHash()`
- [x] if `r.cfg.ReviewPatience > 0`: compare heads; if unchanged → increment `unchangedRounds`; if changed → reset to 0
- [x] if `unchangedRounds >= r.cfg.ReviewPatience`: log `"stalemate detected after %d unchanged rounds, external review terminated early"`, break loop
- [x] write test: mock git returning same hash (2 * ReviewPatience calls), verify loop breaks after N unchanged rounds
- [x] write test: mock git returning new hash on round 2, verify counter resets
- [x] write test: ReviewPatience=0 disables stalemate detection, loop runs to max iterations
- [x] write test: git checker nil → stalemate detection skipped gracefully
- [x] run `go test ./pkg/processor/...` — must pass before task 4

### Task 4: Implement manual "break" command via injected channel

**Files:**
- Modify: `cmd/ralphex/main.go` — stdin reader goroutine, channel wiring
- Modify: `pkg/processor/runner.go` — `Runner` struct, `runExternalReviewLoop()`
- Modify: `pkg/processor/runner_test.go`

- [x] add `BreakCh <-chan struct{}` field to `Runner` struct (nil = feature disabled)
- [x] add `SetBreakCh(ch <-chan struct{})` setter on Runner (follows `SetGitChecker` pattern)
- [x] in `cmd/ralphex/main.go`: create break channel, start stdin reader goroutine that reads lines and sends on "break" input. Check `isatty` on stdin — if not a TTY, skip reader (leave channel nil). Wire channel to runner via `SetBreakCh()`
- [x] in `runExternalReviewLoop()`: if `r.breakCh` is not nil, derive a child context that cancels when break channel fires. Use this child context for executor calls within the loop so break takes effect immediately, interrupting the current executor run
- [x] on break (context cancelled via break channel): log `"manual break requested, external review terminated early"`, return nil
- [x] write test: pre-closed break channel, verify loop exits early via context cancellation
- [x] write test: nil break channel, verify loop runs normally
- [x] run `go test ./pkg/processor/...` — must pass before task 5

### Task 5: Update documentation and CLAUDE.md

**Files:**
- Modify: `CLAUDE.md` — add review_patience to config documentation
- Modify: `llms.txt` — add review_patience to customization section
- Modify: `README.md` — add review_patience option and break command to usage docs

- [x] add `review_patience` to CLAUDE.md config section and key patterns
- [x] add `review_patience` and `break` command to llms.txt customization section
- [x] add to README.md: config option description, CLI flag, break command
- [x] run full test suite: `go test ./...`
- [x] run linter: `make lint`
