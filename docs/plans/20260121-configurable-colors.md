# Configurable Colors

## Overview

Move all hardcoded colors to the config file with current colors as defaults. Users can customize output colors by editing `~/.config/ralphex/config` without code changes.

## Context

**Files involved:**
- `pkg/config/config.go` - config struct and parsing
- `pkg/config/defaults/config` - embedded default config
- `pkg/progress/progress.go` - 8 hardcoded colors (task, review, codex, claude_eval, warn, error, signal, timestamp)
- `cmd/ralphex/main.go` - 1 hardcoded color (info)

**Current colors (to become defaults):**
- task: green (#00ff00)
- review: cyan (#00ffff)
- codex: magenta (#ff00ff)
- claude_eval: RGB(100,200,255) → #64c8ff
- warn: yellow (#ffff00)
- error: red (#ff0000)
- signal: RGB(255,100,100) → #ff6464
- timestamp: RGB(138,138,138) → #8a8a8a
- info: RGB(180,180,180) → #b4b4b4

**Config pattern:** INI format with embedded defaults, three-tier precedence (user → embedded → code)

## Development Approach

- **Testing approach**: TDD (tests first)
- Complete each task fully before moving to the next
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**
- **CRITICAL: use TodoWrite tool to track progress and mark todos completed immediately (do not batch)**

## Progress Tracking

- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix
- Update plan if implementation deviates from original scope

## Implementation Steps

### Task 1: Add ColorConfig struct and hex parsing

- [x] write tests for `parseHexColor()` function in `pkg/config/config_test.go`:
  - valid hex with # prefix (#ff0000 → 255,0,0)
  - valid hex lowercase (#aabbcc)
  - invalid: missing # prefix
  - invalid: wrong length
  - invalid: non-hex characters
- [x] implement `parseHexColor(hex string) (r, g, b int, err error)` in `pkg/config/config.go`
- [x] add `ColorConfig` struct with 9 color fields (Task, Review, Codex, ClaudeEval, Warn, Error, Signal, Timestamp, Info)
- [x] add `Colors ColorConfig` field to `Config` struct
- [x] run tests - must pass before next task

### Task 2: Add color parsing to config loader

- [x] write tests for color config parsing in `pkg/config/config_test.go`:
  - parse full color config with all 9 colors
  - parse partial config (only some colors specified)
  - invalid hex color returns error
- [x] add color parsing in `parseConfigBytes()` for all 9 color_* keys
- [x] set sensible defaults for missing colors (current hardcoded values as hex)
- [x] run tests - must pass before next task

### Task 3: Add color defaults to embedded config

- [ ] add color section to `pkg/config/defaults/config` with all 9 colors as hex values
- [ ] add comments explaining each color's purpose
- [ ] write test verifying embedded defaults load correctly with expected color values
- [ ] run tests - must pass before next task

### Task 4: Update progress package to use configured colors

- [ ] write tests for `SetColors()` function in `pkg/progress/progress_test.go`
- [ ] add `SetColors(cfg config.ColorConfig)` function to initialize color variables
- [ ] export `InitColors()` to be called after config load
- [ ] remove hardcoded color initializations (keep as fallback defaults)
- [ ] run tests - must pass before next task

### Task 5: Update main.go to use configured info color

- [ ] move `infoColor` initialization after config load
- [ ] use `cfg.Colors.Info` to set info color
- [ ] verify startup messages display correctly
- [ ] run tests - must pass before next task

### Task 6: Verify acceptance criteria

- [ ] verify all 9 colors are configurable
- [ ] verify default colors match current behavior (no visual change without config)
- [ ] test custom config with different colors - verify they apply
- [ ] run full test suite: `go test ./...`
- [ ] run linter: `golangci-lint run`
- [ ] verify test coverage meets 80%+

### Task 7: Update documentation

- [ ] update CLAUDE.md if new patterns discovered
- [ ] move this plan to `docs/plans/completed/`

## Technical Details

**Config format (hex with # prefix):**
```ini
# output colors (hex format: #RRGGBB)
color_task = #00ff00
color_review = #00ffff
color_codex = #ff00ff
color_claude_eval = #64c8ff
color_warn = #ffff00
color_error = #ff0000
color_signal = #ff6464
color_timestamp = #8a8a8a
color_info = #b4b4b4
```

**ColorConfig struct:**
```go
type ColorConfig struct {
    Task       string
    Review     string
    Codex      string
    ClaudeEval string
    Warn       string
    Error      string
    Signal     string
    Timestamp  string
    Info       string
}
```

**Hex parsing:** Strip # prefix, parse as hex int, extract R/G/B bytes.
