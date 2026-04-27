# Add configurable `task_header_patterns` config option

## Overview

Add a string-list config option `task_header_patterns` (default preserves today's behavior) that controls which markdown headers the plan parser recognizes as task sections. Users write templates with `{N}` (task identifier) and `{title}` (rest of line) placeholders; ralphex compiles them to regex internally.

Motivation: spec-driven workflows (OpenSpec, etc.) use different header conventions (e.g. `## 1. Phase Name` with nested `- [ ] 1.2 ...` checkboxes). The current parser hard-codes `### Task N:` and `### Iteration N:` at `pkg/plan/parse.go:46`, so OpenSpec-style `tasks.md` silently parses as zero tasks. This change makes the header shape configurable without coupling ralphex to any specific tool.

Default behavior is unchanged: users who don't set the option get `### Task {N}: {title}, ### Iteration {N}: {title}` compiled identically to today's hard-coded regex.

Scope:
- Config-only change (INI, per-project)
- No CLI flag
- No env var
- Prompt coupling: `task.txt` currently references `### Task N:` / `### Iteration N:` by literal string; we add a `{{TASK_HEADER_PATTERNS}}` template variable and rewrite the prompt to use it
- Documentation updates (CLAUDE.md, llms.txt, README.md) are part of this change

Part 2 of 2. Part 1 (`move_plan_on_completion`) is at `docs/plans/20260427-move-plan-on-completion-config.md` (commit 5f1241d). Upstream discussion: https://github.com/umputun/ralphex/issues/306

## Context (from discovery)

- `pkg/plan/parse.go:46` — current hard-coded regex `^###\s+(?:Task|Iteration)\s+([^:]+?):\s*(.*)$`
- `pkg/plan/parse.go:54-137` — `ParsePlan(content string)` and `ParsePlanFile(path string)` signatures; neither takes patterns today
- `pkg/plan/parse.go:97-115` — checkbox scoping (only captured inside a current task); h2 closes current task
- `pkg/plan/parse_test.go` — existing 18+ table-driven test cases; must all continue to pass under default patterns
- Production callers of `ParsePlanFile` / `ParsePlan`:
  - `pkg/processor/runner.go:808, 834` — has access to config via receiver `r`
  - `pkg/web/plan.go:15, 18` — web dashboard; **no config plumbed today** — needs decision on how to get patterns
- `pkg/config/values.go` — existing `ClaudeErrorPatterns []string` pattern (lines ~81-82 in Values, loader near line ~330, merge near line ~460) to mirror for the new field
- `pkg/config/config.go:291` — values→Config mapping block
- `pkg/config/defaults/config:83-86` — `finalize_enabled` block, reference for commented-out template style
- `pkg/config/defaults/prompts/task.txt:10, 19, 42, 48` — hardcoded references to `### Task N:` and `### Iteration N:`
- `pkg/processor/prompts.go` — `replacePromptVariables()` function, home for `{{TASK_HEADER_PATTERNS}}` expansion
- Existing template variables (`{{PLAN_FILE}}`, `{{DEFAULT_BRANCH}}`, etc.) documented in `llms.txt` and CLAUDE.md
- Project CLAUDE.md: 80%+ coverage, table-driven tests with testify, one `_test.go` per source file

## Development Approach

- **testing approach**: TDD — write failing tests first for each new unit (template compiler, config loader, parser behavior with custom patterns, prompt expansion), then the minimal code to pass
- complete each task fully before moving to the next
- make small, focused changes
- every task includes new/updated tests for code changes in that task
- all tests pass before starting next task
- run `make test` and `make lint` after each change
- **maintain backward compatibility**: default patterns compile to regex equivalent to today's hard-coded form; existing plans, tests, and prompt behavior must be unchanged when the option is absent

## Testing Strategy

- **unit tests**: required for every task. Table-driven with testify.
- **e2e tests**: not applicable — no UI change. Web dashboard uses the parser but for display only, not execution.
- **toy-project smoke test** (manual): verify an OpenSpec-style plan with `## N. Phase` headers executes end-to-end after configuring the option.

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document blockers with ⚠️ prefix
- update plan if implementation deviates

## Solution Overview

Split into four concerns, each addressable in its own task:

1. **Template → regex compiler** (`pkg/plan/patterns.go` — new file): pure function from `"### Task {N}: {title}"` to a `*regexp.Regexp` with `{N}` captured. Placeholder validator surfaces errors as config-load failures with pattern string and offending placeholder name.

2. **Config plumbing** (`pkg/config/values.go`, `pkg/config/config.go`, `pkg/config/defaults/config`): standard string-list field mirroring `ClaudeErrorPatterns`. When unset, runtime default applied in `Config` builder (same pattern as part 1's `move_plan_on_completion`).

3. **Parser integration** (`pkg/plan/parse.go`, `pkg/web/plan.go`, `pkg/processor/runner.go`): change `ParsePlan`/`ParsePlanFile` to accept a patterns slice. Nil/empty → fall back to built-in default patterns (same regex as today, compiled from the default templates). All callers updated.

4. **Prompt template variable** (`pkg/processor/prompts.go`, `pkg/config/defaults/prompts/task.txt`): `{{TASK_HEADER_PATTERNS}}` expands to a quoted, `or`-joined string (e.g. `'### Task {N}: {title}' or '### Iteration {N}: {title}'`). Rewrite `task.txt` to use the variable instead of hard-coded strings.

Key design decisions:
- **Templates over raw regex**: friendlier API, constrained surface, fail-fast validation.
- **`{N}` required, `{title}` optional**: every header needs an identifier; some plan flavors may omit a trailing title (though our defaults have one).
- **Built-in defaults compile from the SAME templates the user would write** — one code path, no "legacy regex" branch. Zero chance of drift between default behavior and custom-pattern behavior.
- **Web dashboard calls with `nil` patterns** → gets defaults. Avoids threading config through `pkg/web/` just for parsing.
- **Prompt variable value uses raw templates**, not substituted examples or generated prose. Claude handles `{N}`/`{title}` fine and the prompt already has other `{{VAR}}` placeholders.

## Technical Details

### Field definitions

```go
// pkg/config/values.go (Values struct, near ClaudeErrorPatterns)
TaskHeaderPatterns    []string
TaskHeaderPatternsSet bool // tracks if task_header_patterns was explicitly set

// pkg/config/config.go (Config struct)
TaskHeaderPatterns    []string `json:"task_header_patterns"`
TaskHeaderPatternsSet bool     `json:"-"`
```

### Default templates

```go
// pkg/plan/patterns.go
var DefaultTaskHeaderPatterns = []string{
    "### Task {N}: {title}",
    "### Iteration {N}: {title}",
}
```

### Template → regex algorithm

For each template string:
1. Scan for `{X}` tokens. Allowed: `{N}` exactly once, `{title}` at most once and only after `{N}`. Any other `{...}` → error.
2. Split template into (literal, placeholder, literal, …) segments.
3. Build regex: `^` + for each segment: `regexp.QuoteMeta(literal)` OR placeholder replacement (`{N}` → `([^\s:.]+)`, `{title}` → `(.*)`) + `\s*$`.
4. `regexp.Compile`. On failure (should be rare given controlled inputs) wrap with pattern context.

### Runtime default in Config builder

Mirrors part 1's `move_plan_on_completion` idiom:

```go
headerPatterns := values.TaskHeaderPatterns
if !values.TaskHeaderPatternsSet || len(strings.TrimSpace(strings.Join(headerPatterns, ""))) == 0 {
    headerPatterns = plan.DefaultTaskHeaderPatterns
}
c := &Config{
    // ...
    TaskHeaderPatterns:    headerPatterns,
    TaskHeaderPatternsSet: values.TaskHeaderPatternsSet,
    // ...
}
```

Note: `pkg/plan` is a leaf package (stdlib-only), so `pkg/config` could import it directly without creating a cycle. We still inline the default strings in `pkg/config` and add a drift-prevention test that asserts the two literal slices match — reason: keeping `pkg/config` free of domain-package imports matches the existing layering (config reads values, domain packages consume them), and the test is cheap insurance.

### Parser signature change

```go
// new signature — variadic for clean call sites at defaults
func ParsePlan(content string, patterns ...string) (*Plan, error)
func ParsePlanFile(path string, patterns ...string) (*Plan, error)
```

Rules:
- `len(patterns) == 0` → use `DefaultTaskHeaderPatterns` (callers that want defaults just omit the argument; nil/empty slice behaves identically)
- Compile once up front; walk the file; every line checks against all compiled patterns in order
- First match wins (multiple patterns could in principle match; deterministic via slice order)
- On any header match, close the current task (if any) and open a new one with the captured `{N}` as task number and `{title}` (if present) as title
- **Closing rule** (unchanged from today): h2 headers and h1 headers close the current task. An h3 header that does NOT match any configured pattern does NOT close a task — matches today's parser at `pkg/plan/parse.go:97-115`. This preserves existing semantics for free-form h3 notes inside a task section.

### Prompt template variable

In `pkg/processor/prompts.go`, `replacePromptVariables()` gains:

```go
// build human-readable header-pattern hint: 'p1' or 'p2' or 'p3'
var hint string
if len(cfg.TaskHeaderPatterns) > 0 {
    quoted := make([]string, len(cfg.TaskHeaderPatterns))
    for i, p := range cfg.TaskHeaderPatterns {
        quoted[i] = "'" + p + "'"
    }
    hint = strings.Join(quoted, " or ")
}
s = strings.ReplaceAll(s, "{{TASK_HEADER_PATTERNS}}", hint)
```

`task.txt` rewrite — replace the four hardcoded strings. Example change at line 10:

Before: `Read the plan file at {{PLAN_FILE}}. Find the FIRST Task section (### Task N: or ### Iteration N:) that has uncompleted checkboxes ([ ]).`

After:  `Read the plan file at {{PLAN_FILE}}. Find the FIRST Task section (matching {{TASK_HEADER_PATTERNS}}) that has uncompleted checkboxes ([ ]).`

With defaults in play, `{{TASK_HEADER_PATTERNS}}` expands to `'### Task {N}: {title}' or '### Iteration {N}: {title}'`, which reads naturally.

### INI template entry

```
# task_header_patterns: comma-separated list of markdown header templates
# used to recognize task sections in plan files. templates use {N} for
# the task identifier and {title} for the optional title (rest of line).
# defaults match the ralphex plan format.
# task_header_patterns = ### Task {N}: {title}, ### Iteration {N}: {title}
```

## What Goes Where

- **Implementation Steps** (`[ ]`): all code changes, prompt rewrite, config template update, documentation (CLAUDE.md, llms.txt, README.md), tests, manual toy-project verification
- **Post-Completion** (no checkboxes): PR open, CHANGELOG (release-only), further OpenSpec integration work if it lands

## Implementation Steps

### Task 1: Add template→regex compiler and validator in `pkg/plan/patterns.go`

**Files:**
- Create: `pkg/plan/patterns.go`
- Create: `pkg/plan/patterns_test.go`

- [ ] write failing table-driven tests for `CompileTaskHeaderPattern(template string) (*regexp.Regexp, error)`: valid templates (`### Task {N}: {title}`, `## {N}. {title}`, `### Task {N}:` with no title, `##{N}` with no literals), invalid (missing `{N}`, `{N}` appearing twice, `{title}` before `{N}`, unknown `{foo}` placeholder)
- [ ] write failing test for `CompileTaskHeaderPatterns(templates []string) ([]*regexp.Regexp, error)`: nil/empty returns compiled defaults, good list compiles all, one bad pattern fails the whole call with an error identifying the offending template
- [ ] write failing test asserting the compiled DEFAULT patterns match the same strings as today's hard-coded regex (semantic equivalence on e.g. `### Task 1: Foo`, `### Iteration 2: Bar`, `### Task 1.2: Foo`) — use table-driven inputs
- [ ] run tests — confirm they fail
- [ ] implement `CompileTaskHeaderPattern` with segment scanner (loop over `{X}` tokens, validate, `regexp.QuoteMeta` literals, substitute placeholders, anchor `^...\s*$`)
- [ ] implement `CompileTaskHeaderPatterns` plural helper, with nil/empty falling back to `DefaultTaskHeaderPatterns`
- [ ] export `var DefaultTaskHeaderPatterns = []string{"### Task {N}: {title}", "### Iteration {N}: {title}"}`
- [ ] run `go test ./pkg/plan/...` — all tests must pass before task 2

### Task 2: Add `TaskHeaderPatterns` config field and loader

**Files:**
- Modify: `pkg/config/values.go`
- Modify: `pkg/config/values_test.go`

- [ ] write failing table-driven test cases for load: key absent (Set=false), explicit list, explicit empty string (Set=true, empty slice), single-pattern list, comma-separated list with whitespace, whitespace-only entries (e.g. `,   ,`), duplicate entries preserved in order, entries containing regex meta chars (`.`, `*`, `[`) as literals in surrounding text
- [ ] write failing table-driven test cases for merge: src set overrides dst (including `src=[]` clearing dst), src unset preserves dst
- [ ] run tests — confirm they fail
- [ ] add `TaskHeaderPatterns []string` and `TaskHeaderPatternsSet bool` to `Values` struct, mirroring `ClaudeErrorPatterns`
- [ ] add INI loader block for `task_header_patterns` (treat as comma-separated; trim each entry; drop empty entries after trim; reuse the same splitter as `claude_error_patterns` — audit and reuse)
- [ ] add merge block in `mergeExtraFrom` (or the appropriate helper) — **guard on `src.TaskHeaderPatternsSet`, NOT on `len(src.TaskHeaderPatterns) > 0`**. The `ClaudeErrorPatterns` precedent at `pkg/config/values.go:463-464` uses `len(...) > 0` but that precedent is a latent bug: it cannot express "explicitly set to empty". Since we want fallback-to-default on empty (handled in Task 3's Config builder), the `Set` guard here is the semantically correct form. Add a brief code comment noting the deliberate deviation from the `ClaudeErrorPatterns` precedent.
- [ ] run `go test ./pkg/config/...` — all tests must pass before task 3

### Task 3: Propagate `TaskHeaderPatterns` to `Config` with runtime default

**Files:**
- Modify: `pkg/config/config.go`
- Modify: `pkg/config/config_test.go`

- [ ] write failing table-driven test: default (not set) yields the built-in defaults slice, explicit list yields that list, explicit empty yields the defaults (fallback), set flag reflects whether user configured it
- [ ] write a drift-prevention test that asserts the `pkg/config` inline default list equals `plan.DefaultTaskHeaderPatterns` element-for-element (both must stay in sync; this test catches future divergence)
- [ ] run tests — confirm they fail
- [ ] add `TaskHeaderPatterns []string` (with `json:"task_header_patterns"`) and `TaskHeaderPatternsSet bool` (`json:"-"`) fields to `Config` struct
- [ ] in the `Config` builder at line ~270, precompute `headerPatterns` into a local: if `!values.TaskHeaderPatternsSet` OR all-entries-empty-after-trim → use inlined default list; else use `values.TaskHeaderPatterns`
- [ ] assign both fields in the struct literal
- [ ] run `go test ./pkg/config/...` — all tests must pass before task 4

### Task 4: Change parser signatures to accept patterns; update all callers

**Files:**
- Modify: `pkg/plan/parse.go`
- Modify: `pkg/plan/parse_test.go`
- Modify: `pkg/processor/runner.go`
- Modify: `pkg/web/plan.go`

- [ ] write failing tests in `parse_test.go` for the new signature: no patterns arg → defaults (existing test cases continue to pass), custom pattern `## {N}. {title}` matches OpenSpec-style headers and captures `- [ ]` checkboxes beneath, mixed-pattern plan (both `### Task 1:` and `## 2. Phase`) parses all tasks in document order
- [ ] write failing test for **closing behavior** (explicit, to prevent regressions): (a) h2 header that does NOT match any configured pattern closes the current task (unchanged from today); (b) h1 header closes the current task (unchanged); (c) h3 header that does NOT match any configured pattern does NOT close the current task (unchanged — today's parser at `pkg/plan/parse.go:97-115` only closes on h2/h1); (d) a matching custom h2 pattern (`## {N}. ...`) closes a preceding `### Task 1:` section AND opens a new task
- [ ] write failing test: custom pattern with malformed template surfaces compile error from `ParsePlan`
- [ ] write failing test: plan with `## 1. Phase` headers but zero `- [ ]` checkboxes produces a Plan with zero tasks (expected — matches today's "no executable tasks" behavior)
- [ ] run tests — confirm they fail
- [ ] change `ParsePlan(content string)` → `ParsePlan(content string, patterns ...string)`; compile patterns once at the top (empty → defaults); replace the hardcoded `taskHeaderPattern` usage with a loop over compiled patterns (first match wins)
- [ ] change `ParsePlanFile(path string)` → `ParsePlanFile(path string, patterns ...string)` accordingly
- [ ] remove the package-level `taskHeaderPattern` var (now compiled per-call)
- [ ] existing test call sites in `parse_test.go` do NOT need changes — variadic makes `ParsePlan(content)` continue to compile and default
- [ ] update `pkg/processor/runner.go:808, 834` to pass `r.cfg.TaskHeaderPatterns...` (exact field name per Task 3)
- [ ] update `pkg/web/plan.go:15, 18` — no arg needed (variadic, falls back to defaults; web dashboard renders plans for display only)
- [ ] run `go test ./...` and `go build ./...` — must pass before task 5

### Task 5: Add `{{TASK_HEADER_PATTERNS}}` template variable and rewrite `task.txt`

**Files:**
- Modify: `pkg/processor/prompts.go`
- Modify: `pkg/processor/prompts_test.go`
- Modify: `pkg/config/defaults/prompts/task.txt`

- [ ] write failing test in `prompts_test.go` for `replacePromptVariables()`: `{{TASK_HEADER_PATTERNS}}` expands to `'p1' or 'p2'` style for multi-pattern config, single pattern yields `'p1'`, empty config yields an empty string (edge case — should not happen since runtime default always populates, but defensive)
- [ ] write failing test covering back-compat: given default patterns, a known existing phrase in task.txt (e.g. the `{{PLAN_FILE}}` reference) still expands correctly alongside the new variable
- [ ] run tests — confirm they fail
- [ ] add `{{TASK_HEADER_PATTERNS}}` expansion block in `replacePromptVariables()` in `pkg/processor/prompts.go` — quote each pattern with single quotes, join with ` or `, replace all occurrences
- [ ] rewrite `pkg/config/defaults/prompts/task.txt` to use `{{TASK_HEADER_PATTERNS}}` at lines 10, 19, 42, 48 — preserve surrounding sentence structure so the default expansion reads naturally (e.g. `"Find the FIRST Task section (matching {{TASK_HEADER_PATTERNS}}) that has uncompleted checkboxes ([ ])."`)
- [ ] for each of lines 10, 19, 42, 48 in the rewritten file, verify the full sentence still reads naturally after expansion — no orphaned punctuation, no doubled articles, no broken parens
- [ ] manually diff the rendered output under default patterns against current task.txt content to verify no semantic regression
- [ ] run `go test ./pkg/processor/...` — all tests must pass before task 6

### Task 6: Update embedded INI template

**Files:**
- Modify: `pkg/config/defaults/config`

- [ ] add commented-out `task_header_patterns` block near related sections (plan-parsing area; if none, alongside `finalize_enabled` group), following the style: three-line header comment (what, when to change, default), then commented option line
- [ ] existing `defaults_test.go` all-commented coverage (`TestShouldOverwrite/all_commented`) already protects the fallback path — no new regression test needed
- [ ] run `go test ./pkg/config/...` — must pass before task 7

### Task 7: Verify acceptance criteria

- [ ] **safety check**: confirm no test writes to `~/.config/ralphex/` — all tests must use `t.TempDir()` per CLAUDE.md "Testing Safety Rules"; MD5-checksum `~/.config/ralphex/config` before and after `go test ./...` to verify
- [ ] `make test` passes (unit tests with coverage)
- [ ] `make lint` passes (no new golangci-lint issues)
- [ ] `make fmt` — code is formatted
- [ ] coverage on touched files ≥ 80% per CLAUDE.md
- [ ] `GOOS=windows GOARCH=amd64 go build ./...` succeeds
- [ ] toy-project smoke test #1 — **back-compat**: run `./scripts/internal/prep-toy-test.sh` with no config override; execute the default `docs/plans/fix-issues.md`; verify parsing and execution identical to previous behavior
- [ ] toy-project smoke test #2 — **OpenSpec shape**: create a plan file with OpenSpec-style headers (`## 1. Phase X` + `- [ ] 1.1 ...`) in `/tmp/ralphex-test`, add `task_header_patterns = ### Task {N}: {title}, ### Iteration {N}: {title}, ## {N}. {title}` to `.ralphex/config`, run ralphex against it, confirm tasks are detected, executed, and completion signals fire
- [ ] toy-project smoke test #3 — **bad pattern**: configure `task_header_patterns = ### Task {foo}: {title}`, run ralphex, confirm it exits cleanly with an error that names the offending template and placeholder

### Task 8: Final — update documentation and move plan

**Files:**
- Modify: `CLAUDE.md`
- Modify: `llms.txt`
- Modify: `README.md`

- [ ] `CLAUDE.md` Configuration section: add a line for `task_header_patterns` — comma-separated list of markdown header templates; placeholders `{N}` (identifier) and `{title}` (optional, must come after `{N}`); defaults `### Task {N}: {title}, ### Iteration {N}: {title}`; use for spec-driven workflows (OpenSpec etc.) that use `## N. Phase` headers
- [ ] `llms.txt` config-options list: add same option in the existing alphabetical-ish ordering; also add `{{TASK_HEADER_PATTERNS}}` to the template-variables list
- [ ] `README.md`: add a new "Plan Header Patterns (optional)" subsection after "Plan Move Behavior (optional)" (added in part 1) — one-line description, INI example, and one sentence on when to use
- [ ] do NOT update CHANGELOG (per CLAUDE.md workflow rule: CHANGELOG updates are release-process only)
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

**PR submission** (after plan complete):
- open PR against `umputun/ralphex` referencing issue #306
- PR description: link to issue, summary of the option and defaults, note that this is part 2 of 2, back-compat guaranteed by default-template test and toy-project smoke test #1
- if part 1 PR has not landed yet, rebase after it merges to avoid documentation conflicts in README.md

**Follow-up items** (not in this PR):
- CHANGELOG entry (release process, per CLAUDE.md)
- Optional: `{{DIFF_INSTRUCTION}}`-style per-iteration variations for other prompts (`review_first.txt` etc.) if they accumulate hardcoded header references — not currently needed
- Optional: per-plan pattern override via plan frontmatter — YAGNI for now, add if real demand emerges
