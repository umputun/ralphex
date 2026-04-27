# Add `move_plan_on_completion` config option

## Overview

Add a boolean config option `move_plan_on_completion` (default `true`) that controls whether ralphex moves a completed plan file into `docs/plans/completed/` on successful execution. When set to `false`, the plan file is left in place.

Motivation: spec-driven workflows (e.g. OpenSpec at https://openspec.dev/) manage plan file lifecycle externally — the plan lives inside a bundle that a separate archive step consumes. ralphex's unconditional move breaks those workflows. This change is generic and not tied to any specific tool.

Default behavior is unchanged: users who don't set the option continue to have plans moved to `completed/`.

Scope:
- Config-only change (INI file in `~/.config/ralphex/` or `.ralphex/`)
- No CLI flag
- No env var
- Documentation updates (README.md, llms.txt, CLAUDE.md) are part of this change

Part 1 of 2. A follow-up PR will add configurable task header patterns. Upstream discussion: https://github.com/umputun/ralphex/issues/306

## Context (from discovery)

- `pkg/config/values.go:46-47` — existing `FinalizeEnabled` / `FinalizeEnabledSet` bool pair, exact shape to mirror
- `pkg/config/values.go:277-284` — INI key loader pattern for `finalize_enabled`
- `pkg/config/values.go:442-446` — merge-from-src pattern inside `mergeExtraFrom`
- `pkg/config/config.go:69-70` — `Config` struct fields for `FinalizeEnabled`
- `pkg/config/config.go:291-292` — values-to-config mapping
- `pkg/config/defaults/config:83-86` — embedded template entry for `finalize_enabled` (commented out)
- `cmd/ralphex/main.go:532-544` — the unconditional `MovePlanToCompleted` call; `req.Config` is already on the struct (line 111), so no plumbing is needed
- Project CLAUDE.md: 80%+ coverage, table-driven tests with testify, one `_test.go` per source file

## Development Approach

- **testing approach**: TDD — write the failing test first for each new field/behavior, then the minimal code to pass
- complete each task fully before moving to the next
- make small, focused changes
- every task includes new/updated tests for code changes in that task
- all tests pass before starting next task
- run `make test` and `make lint` after each change
- maintain backward compatibility (default `true` = today's behavior)

## Testing Strategy

- **unit tests**: required for every task. Table-driven with testify.
- **e2e tests**: not applicable — no UI change. Toy-project end-to-end validation (per CLAUDE.md) happens once at the end as a manual smoke test, not as a code-generating task.

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document blockers with ⚠️ prefix
- update plan if implementation deviates

## Solution Overview

Mirror the `FinalizeEnabled` field pattern exactly — same types, same load logic, same merge logic — then add one guard at the call site. The change is almost mechanical.

Key design decisions:
- **Default `true`** preserves current behavior. Users opt out.
- **`*Set` flag** follows existing convention, lets local config cleanly override global config even when the explicit value happens to be the zero value (`false`).
- **No CLI flag** — this is a per-project setting, not per-run. Adding a flag expands the surface area without a demonstrated use case.
- **Embedded template line is commented out** per the "uncommenting marks customization" pattern documented in CLAUDE.md.

## Technical Details

Field definitions:
```go
// pkg/config/values.go (Values struct)
MovePlanOnCompletion    bool
MovePlanOnCompletionSet bool // tracks if move_plan_on_completion was explicitly set

// pkg/config/config.go (Config struct)
MovePlanOnCompletion    bool `json:"move_plan_on_completion"`
MovePlanOnCompletionSet bool `json:"-"`
```

Defaults: the embedded defaults path uses the Go zero value (`false`) if the key is absent, but the embedded config template ships with `move_plan_on_completion = true` commented out. Critically, the runtime default for users who never configure anything must be `true`.

We apply the default in the `Config` builder at `pkg/config/config.go:270-305`. The existing builder assigns fields inside a single struct literal with no post-assembly block, so the cleanest idiom is a local precomputation before the literal:

```go
movePlan := values.MovePlanOnCompletion
if !values.MovePlanOnCompletionSet {
    movePlan = true
}
c := &Config{
    // ...
    MovePlanOnCompletion:    movePlan,
    MovePlanOnCompletionSet: values.MovePlanOnCompletionSet,
    // ...
}
```

This keeps `Config` as the single runtime source of truth and avoids threading "unset means true" logic through call sites.

Call-site guard: extract a tiny testable predicate in `cmd/ralphex/main.go` — follows the existing `modeRequiresBranch` precedent at `main_test.go:988`:

```go
func shouldMovePlan(req executePlanRequest) bool {
    return req.PlanFile != "" && modeRequiresBranch(req.Mode) && req.Config.MovePlanOnCompletion
}
```

Use it at line 532: `if shouldMovePlan(req) { ... }`. Unit-testable in `main_test.go` with a table-driven `TestShouldMovePlan`.

Call-site guard:
```go
// cmd/ralphex/main.go, around line 532
if req.PlanFile != "" && modeRequiresBranch(req.Mode) && req.Config.MovePlanOnCompletion {
    // existing move logic unchanged
}
```

INI loader (in `loadFromINI` or equivalent, near the `finalize_enabled` block):
```go
if key, err := section.GetKey("move_plan_on_completion"); err == nil {
    val, boolErr := key.Bool()
    if boolErr != nil {
        return Values{}, fmt.Errorf("invalid move_plan_on_completion: %w", boolErr)
    }
    values.MovePlanOnCompletion = val
    values.MovePlanOnCompletionSet = true
}
```

Merge block (in `mergeExtraFrom`, near the `FinalizeEnabledSet` block):
```go
if src.MovePlanOnCompletionSet {
    dst.MovePlanOnCompletion = src.MovePlanOnCompletion
    dst.MovePlanOnCompletionSet = true
}
```

Embedded template (insert near `finalize_enabled` block around line 86 in `pkg/config/defaults/config`):
```
# move_plan_on_completion: whether to move completed plan file into
# docs/plans/completed/ on success. set to false for workflows that
# manage plan file lifecycle externally (e.g. spec-driven tooling with
# separate archive steps).
# default: true
# move_plan_on_completion = true
```

## What Goes Where

- **Implementation Steps** (`[ ]`): all code changes, config template update, documentation (CLAUDE.md, llms.txt, README.md), tests, manual toy-project verification
- **Post-Completion** (no checkboxes): PR open, CHANGELOG (release-only), part 2 plan

## Implementation Steps

### Task 1: Add `MovePlanOnCompletion` to `Values` struct and INI loader

**Files:**
- Modify: `pkg/config/values.go`
- Modify: `pkg/config/values_test.go`

- [x] write failing table-driven test cases for load: key absent (Set=false), explicit true, explicit false, invalid value (returns error)
- [x] write failing table-driven test cases for merge: src set overrides dst, src unset preserves dst
- [x] run tests — confirm they fail as expected
- [x] add `MovePlanOnCompletion bool` and `MovePlanOnCompletionSet bool` to the `Values` struct near the `FinalizeEnabled` fields
- [x] add INI loader block for `move_plan_on_completion` next to the `finalize_enabled` block (line ~277); return error on non-bool value
- [x] add merge block in `mergeExtraFrom` next to the `FinalizeEnabledSet` block (line ~443)
- [x] run `go test ./pkg/config/...` — all tests must pass before task 2

### Task 2: Propagate `MovePlanOnCompletion` to `Config` struct and apply runtime default

**Files:**
- Modify: `pkg/config/config.go`
- Modify: `pkg/config/config_test.go`

- [x] write failing table-driven test: default (not set) yields `true`, explicit `true` yields `true`, explicit `false` yields `false`
- [x] run test — confirm it fails as expected
- [x] add `MovePlanOnCompletion bool` (with `json:"move_plan_on_completion"`) and `MovePlanOnCompletionSet bool` (`json:"-"`) fields to `Config` struct near `FinalizeEnabled`
- [x] precompute effective default into a local `movePlan` before the struct literal at line ~270 (see Technical Details for exact form), then assign `MovePlanOnCompletion: movePlan` and `MovePlanOnCompletionSet: values.MovePlanOnCompletionSet` inside the literal
- [x] run `go test ./pkg/config/...` — all tests must pass before task 3

### Task 3: Extract `shouldMovePlan` predicate and guard the move call

**Files:**
- Modify: `cmd/ralphex/main.go`
- Modify: `cmd/ralphex/main_test.go`

- [ ] write failing table-driven `TestShouldMovePlan` in `main_test.go` next to `TestModeRequiresBranch` (line ~988): cases for (a) empty PlanFile → false, (b) mode doesn't require branch → false, (c) `Config.MovePlanOnCompletion=false` → false, (d) all conditions true → true
- [ ] run test — confirm it fails (function doesn't exist yet)
- [ ] add `shouldMovePlan(req executePlanRequest) bool` helper in `main.go` (see Technical Details for form)
- [ ] replace the condition at line 532 with `if shouldMovePlan(req) {`
- [ ] verify `req.Config` is non-nil at this call site in all callers (it is — set in all `executePlanRequest` constructions found by grep)
- [ ] run `go test ./cmd/ralphex/...` — all tests must pass before task 4

### Task 4: Update embedded config template

**Files:**
- Modify: `pkg/config/defaults/config`

- [ ] add commented-out `move_plan_on_completion` block near the `finalize_enabled` block (line ~86), following the surrounding comment style (what it does, when to change, default, then commented option)
- [ ] existing `defaults_test.go` already verifies the all-commented fallback (`TestShouldOverwrite/all_commented` at line 993, plus `stripComments` checks at line 873+) — no new regression test needed
- [ ] run `go test ./pkg/config/...` — must pass before task 5

### Task 5: Verify acceptance criteria

- [ ] `make test` passes (unit tests with coverage)
- [ ] `make lint` passes (no new golangci-lint issues)
- [ ] `make fmt` — code is formatted
- [ ] coverage on touched files ≥ 80% per CLAUDE.md
- [ ] `GOOS=windows GOARCH=amd64 go build ./...` succeeds (no Unix-specific paths introduced)
- [ ] toy-project smoke test per CLAUDE.md: run `./scripts/internal/prep-toy-test.sh`, then add a plain INI entry `move_plan_on_completion = false` (not just uncommenting the template) to `/tmp/ralphex-test/.ralphex/config`, execute a plan, and verify the plan file stays in `docs/plans/` rather than `docs/plans/completed/`
- [ ] toy-project smoke test with default config (no override): verify plan still moves to `completed/` (back-compat)

### Task 6: Final — update documentation and move plan

**Files:**
- Modify: `CLAUDE.md`
- Modify: `llms.txt`
- Modify: `README.md`

- [ ] add a line to the `Configuration` section of project `CLAUDE.md`: `move_plan_on_completion` config option controls whether completed plans move to `docs/plans/completed/` on success, default `true`. Disable for workflows that manage plan lifecycle externally (spec-driven tooling with separate archive steps)
- [ ] add the same option to `llms.txt` in the existing configuration-options list (alphabetical-ish with neighbouring options)
- [ ] add a new "Plan Move Behavior (optional)" subsection to `README.md` immediately after the existing "Finalize Step (optional)" section (line ~161), modeled on the same structure: one-line description, "How to enable/disable" with the INI key and default (`true`), and one sentence on when to disable (external plan-lifecycle workflows with separate archive steps)
- [ ] do NOT update CHANGELOG (per CLAUDE.md workflow rule: CHANGELOG updates are release-process only)
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

**PR submission** (after plan complete):
- open PR against `umputun/ralphex` referencing issue #306
- PR description: link to issue, summary of the option and default, note that this is part 1 of 2

**Follow-up items** (not in this PR):
- CHANGELOG entry (release process, per CLAUDE.md)
- Part 2: configurable task header patterns (`task_header_patterns` with `{N}` / `{title}` template slots) — separate plan, separate PR
