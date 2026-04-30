# Task Header Patterns: Rework to Raw Regex + Preset Registry

## Overview

PR #309 introduced configurable task header patterns via a `{N}/{title}` template DSL (+2403/-177
across 30 files). The project owner reviewed and asked for a dramatically simpler design: raw regex
in config, a tiny named-preset registry, and no progress-log transport layer. This plan reworks the
PR to land at ~+200/-50 across ~8 files.

**What changes:**
- Template compiler (`pkg/plan/patterns.go`, 208 LOC + 394 LOC tests) → deleted
- Progress-log transport (`TaskHeaderPatterns:` in progress files, dashboard re-emission, full-file
  scan in `ParseProgressHeader`, `loadSessionPlanWithFallback`) → deleted (~250-400 LOC)
- Replaced by a `map[string]string` preset registry in `pkg/plan` with two entries (`default`,
  `openspec`) and direct `regexp.Compile` for raw regex values

**All three Copilot review comments are addressed as side-effects:**
- O(file) scan on every refresh → dropped with transport layer
- Misleading `TaskHeaderPatternsSet` comment → removed with lowercasing
- Test comment "scans file tail" vs whole-file impl → removed with transport layer

## Context

- `pkg/plan/patterns.go` / `patterns_test.go` — template compiler to delete
- `pkg/plan/parse.go` — keep `closesTask`/`headingLevel` refactor; change variadic `...string` to `[]*regexp.Regexp`
- `pkg/plan/presets.go` (new) — preset registry + resolver
- `pkg/config/config.go` + `values.go` — simplify defaulting, lowercase internal fields
- `pkg/progress/progress.go` — remove `TaskHeaderPatterns` field and writes
- `pkg/web/session_progress.go` — remove full-file scan for override
- `pkg/web/session.go`, `server.go`, `dashboard.go`, `plan.go` — remove TaskHeaderPatterns plumbing
- `pkg/processor/runner.go` + `prompts.go` — update callers, update `{{TASK_HEADER_PATTERNS}}` hint
- `cmd/ralphex/main.go` — remove progress/dashboard `TaskHeaderPatterns` wiring

## Development Approach

- **testing approach**: Regular (code first, then tests)
- Complete each task before moving to the next; tree must compile and `make test` must pass at each gate
- The parse.go refactor (`closesTask`/`headingLevel`) is already in the branch — preserve it

## Solution Overview

**Preset registry** (`pkg/plan/presets.go`):
```go
var headerPresets = map[string]string{
    "default": `^### (?:Task|Iteration) ([^:]+?):\s*(.*)$`,
    "openspec": `^## (\d+)\.?\s*(.*)$`,
}
```

Note: `openspec` uses `(\d+)` (integer only, not `\d+(?:\.\d+)?`) because `parseTaskNum` in
`parse.go` parses `Task.Number` as an `int`. Nested headings like `2.3` are out of scope.

`ResolveHeaderPattern(s string) (*regexp.Regexp, error)` checks if `s` is a preset key; if so,
compiles the preset regex; otherwise compiles `s` as a raw regex directly.

**Preset descriptions** (for LLM-readable `{{TASK_HEADER_PATTERNS}}` hint):
```go
var headerPresetDescriptions = map[string]string{
    "default": "### Task N: title  or  ### Iteration N: title",
    "openspec": "## N. title",
}
```
`getTaskHeaderPatternsHint` uses the description when available; falls back to the raw regex string
for user-supplied patterns so the prompt remains human-readable.

**Config values:**
- `task_header_patterns` is still a comma-separated list of items
- Each item is either a preset name OR a raw regex; resolved by `ResolveHeaderPattern`
- Empty/unset → resolved to `DefaultHeaderPatterns()` (compiles `headerPresets["default"]`) at runtime
- Compile errors surfaced as-is to the user

**`ParsePlan` / `ParsePlanFile` signature change:**
```go
// before (variadic template strings)
func ParsePlan(content string, patterns ...string) (*Plan, error)

// after (pre-compiled regexes, resolved by caller)
func ParsePlan(content string, patterns []*regexp.Regexp) (*Plan, error)
```

All call sites (`pkg/web/plan.go`, `pkg/processor/runner.go`) must update in the same commit as
the signature change to keep the tree compilable.

**SSE skip logic for `TaskHeaderPatterns:` lines** is kept in `pkg/web/parse.go` for backward
compatibility with progress files written by older ralphex versions.

**No progress-log transport:** the dashboard reads its own config (or falls back to defaults).
A dashboard watching sessions from other repos with custom patterns is not a supported use case.

## Implementation Steps

### Task 1: Add preset registry in pkg/plan

**Files:**
- Create: `pkg/plan/presets.go`
- Create: `pkg/plan/presets_test.go`

- [x] create `pkg/plan/presets.go` with:
  - `headerPresets map[string]string` (`default`, `openspec` — integer-only openspec, see note above)
  - `headerPresetDescriptions map[string]string` — human-readable descriptions for prompt hint
  - `ResolveHeaderPattern(s string) (*regexp.Regexp, error)`: preset lookup → raw compile
  - `ResolveHeaderPatterns(patterns []string) ([]*regexp.Regexp, error)`: resolves a slice
  - `PresetDescription(s string) string`: returns human description if `s` is a preset, else returns `s` (the raw regex)
- [x] write table-driven tests in `pkg/plan/presets_test.go`:
  - preset names `"default"` and `"openspec"` resolve correctly
  - raw regex compiles and returns correct `*regexp.Regexp`
  - invalid regex surfaces `regexp.Compile` error
  - typo/unknown name is treated as raw regex (not an error)
  - `PresetDescription` returns description for known presets, raw string for unknowns
- [x] run `make test` — must pass before task 2

### Task 2: Delete patterns.go and rework parse.go + all call sites atomically

All signature changes and their call sites **must land in this single task** to keep the build green.

**Files:**
- Delete: `pkg/plan/patterns.go`
- Delete: `pkg/plan/patterns_test.go`
- Modify: `pkg/plan/parse.go`
- Modify: `pkg/plan/parse_test.go`
- Modify: `pkg/web/plan.go` ← call site, must update here
- Modify: `pkg/processor/runner.go` ← call site, must update here

- [x] delete `pkg/plan/patterns.go` and `pkg/plan/patterns_test.go`
- [x] add `DefaultHeaderPatterns() []*regexp.Regexp` to `pkg/plan/presets.go`: compiles
      `headerPresets["default"]`; panics on bad regex (it's a hardcoded constant)
- [x] add `TestDefaultHeaderPatternsCompiles` in `presets_test.go` to lock the invariant
- [x] change `ParsePlan(content string, patterns ...string)` → `ParsePlan(content string, patterns []*regexp.Regexp)`
- [x] change `ParsePlanFile(path string, patterns ...string)` → `ParsePlanFile(path string, patterns []*regexp.Regexp)`
- [x] update internal pattern matching in `parse.go` to use pre-compiled `[]*regexp.Regexp`; no
      compilation inside `parse.go` itself
- [x] preserve `closesTask`/`headingLevel` refactor already in branch
- [x] update `pkg/plan/parse_test.go`: replace spread calls with slice literals; add test case using
      `openspec` preset pattern (call `ResolveHeaderPatterns([]string{"openspec"})`)
- [x] update `pkg/web/plan.go` call sites to pass `[]*regexp.Regexp` slice (not spread strings)
- [x] update `pkg/processor/runner.go` call sites to pass `[]*regexp.Regexp` slice (not spread strings);
      update `taskHeaderPatterns()` helper to return `[]*regexp.Regexp` resolved via `ResolveHeaderPatterns`
- [x] run `make test` and `go build ./...` — both must pass before task 3

### Task 3: Rework config layer

**Files:**
- Modify: `pkg/config/config.go`
- Modify: `pkg/config/values.go`
- Modify: `pkg/config/values_test.go`
- Modify: `pkg/config/config_test.go`

- [x] remove `defaultTaskHeaderPatterns` slice from `config.go` (replaced by `plan.DefaultHeaderPatterns()`)
- [x] remove `TestDefaultTaskHeaderPatterns_MatchesConfigDefaults` cross-package sync test (no longer needed)
- [x] rename `Config.TaskHeaderPatternsSet` → unexported in `values.go` (no external callers);
      update all references in `values_test.go` (search for `TaskHeaderPatternsSet` — ~10 lines)
- [x] rename exported `CompileTaskHeaderPatterns` → unexported `compileTaskHeaderPatterns`
- [x] update the field comment (was: "allows empty to disable"; now: "distinguishes explicit set
      from unset; empty resets to preset default")
- [x] runtime defaulting in `config.go`: when unset or empty, resolve to `plan.DefaultHeaderPatterns()`
- [x] update `pkg/config/values_test.go` and `config_test.go` for new field visibility and defaulting
      semantics; grep for `TaskHeaderPatternsSet` and `CompileTaskHeaderPatterns` to find all references
- [x] run `make test` — must pass before task 4

### Task 4: Drop progress-log transport layer

**Files:**
- Modify: `pkg/progress/progress.go`
- Modify: `pkg/progress/progress_test.go`
- Modify: `pkg/web/session_progress.go`
- Modify: `pkg/web/session_progress_test.go`
- Modify: `pkg/web/session.go`
- Modify: `pkg/web/plan.go`
- Modify: `pkg/web/server.go`
- Modify: `pkg/web/server_test.go`
- Modify: `pkg/web/dashboard.go`
- Modify: `pkg/web/dashboard_test.go`
- Modify: `pkg/web/parse.go` (keep skip logic — see note below)
- Modify: `pkg/web/parse_test.go`

- [x] `progress.go`: remove `TaskHeaderPatterns []string` from `LogConfig`; remove writes of
      `TaskHeaderPatterns:` header line and restart re-emission; update `progress_test.go`
- [x] `session_progress.go`: remove full-file scan loop for `TaskHeaderPatterns:` override (the
      entire `for` loop after the separator in `ParseProgressHeader`); remove `TaskHeaderPatterns`
      from `SessionMetadata`; remove from `applyHeaderField`; update `session_progress_test.go`
- [x] `session.go`: remove `TaskHeaderPatterns []string` field from `Session` struct
- [x] `plan.go`: delete `loadSessionPlanWithFallback`; update call site in `server.go` to use
      `loadPlanWithFallback` with dashboard config patterns only
- [x] `server.go`: remove `TaskHeaderPatterns` from `ServerConfig`; update plan endpoint to use
      config patterns directly (no per-session pattern merge); update `server_test.go`
- [x] `dashboard.go`: remove `TaskHeaderPatterns` from `DashboardConfig` and `taskHeaderPatterns`
      from the dashboard struct; update `dashboard_test.go`
- [x] `parse.go`: **keep** the `TaskHeaderPatterns:` skip logic for backward compat with old
      progress files on disk; just add a comment explaining it's kept for old-file compat
- [x] run `make test` — must pass before task 5

### Task 5: Update processor prompts and main.go wiring

**Files:**
- Modify: `pkg/processor/prompts.go`
- Modify: `pkg/processor/prompts_test.go`
- Modify: `cmd/ralphex/main.go`

- [x] `prompts.go`: update `getTaskHeaderPatternsHint()` to call `plan.PresetDescription(s)` for
      each configured pattern item: shows human-readable description for presets (e.g.
      `'### Task N: title'`), raw regex string for user-supplied patterns
- [x] `prompts.go`: update `{{TASK_HEADER_PATTERNS}}` expansion formatting accordingly
- [x] `cmd/ralphex/main.go`: remove all `TaskHeaderPatterns` wiring to `progress.LogConfig` and
      `dashboard.DashboardConfig` (both dropped in task 4)
- [x] update `prompts_test.go` for new hint format
- [x] run `make test` — must pass before task 6

### Task 6: Update embedded config template and docs

**Files:**
- Modify: `pkg/config/defaults/config`
- Modify: `pkg/config/defaults/prompts/task.txt`
- Modify: `CLAUDE.md`
- Modify: `README.md`
- Modify: `llms.txt`

- [x] `defaults/config`: update `task_header_patterns` comment to show both preset name
      (`openspec`) and raw regex examples
- [x] `task.txt`: update `{{TASK_HEADER_PATTERNS}}` prose to reflect regex-based config
- [x] `CLAUDE.md`: update plan-format guidance and config docs for new regex/preset semantics
- [x] `README.md`: update `task_header_patterns` docs with preset and raw regex examples
- [x] `llms.txt`: update `{{TASK_HEADER_PATTERNS}}` entry and config docs

### Task 7: Verify and clean up

- [x] run full test suite: `make test` — all tests must pass
- [x] cross-compile for Windows: `GOOS=windows GOARCH=amd64 go build ./...`
- [x] grep for any remaining references: `patterns.go`, `DefaultTaskHeaderPatterns`,
      `loadSessionPlanWithFallback`, `sanitizePatterns`, `TaskHeaderPatternsSet` (exported),
      `CompileTaskHeaderPatterns` (exported) — should be zero
- [x] verify `git diff --stat` shows ~+200/-50 net (not 2400+)
- [x] run end-to-end toy project test per CLAUDE.md workflow requirements:
      `./scripts/internal/prep-toy-test.sh` then execute the toy plan
- [x] move this plan to `docs/plans/completed/`

## Post-Completion

**PR update:**
- Force-push the reworked branch to replace the existing +2403 PR with the simplified version
- Update PR description to reference umputun's review and summarize the simplification

**Web dashboard e2e tests:**
- Consider running `go test -tags=e2e ./e2e/...` after the PR lands if dashboard plan-parsing
  changes warrant it (playwright tests cover SSE streaming and plan panel rendering)
