# Behavior-Preserving Refactor: Code Smells and Duplication

## Overview
- Address the code smells and architectural duplication surfaced by the refactor-analysis workflow (12 agents, 10 package units + cross-cutting architecture pass).
- The codebase is structurally healthy: no god objects remain, interfaces are consumer-side, error wrapping is consistent. The leverage is in **one config-structure problem** plus **recurring behavior-preserving duplication** of small domain logic that drifts silently across parallel copies.
- **Every change in this plan must preserve behavior exactly.** No functional changes, no new features, no altered runtime behavior. INI keys, error strings, signal vocabulary, merge precedence, and concurrency contracts all stay identical.

## Context (from discovery)
- Source of findings: refactor-analysis workflow report (run `wugyu0vk8`). 44 package findings, 1 high-severity, 0 cross-cutting (all duplication is package-local or cmd-local).
- Files/components involved (by package):
  - `pkg/config`: `config.go`, `values.go` (four-way field mirror; three identical duration parsers; drifted godoc).
  - `pkg/processor/phase`: `task.go`, `review.go`, `finalize.go`, `plan_creation.go`, `external_review.go`, `phase.go` (`executorName()` duplicated on 4 types — confirmed via grep). Executor-error wrap shapes are **not** uniform: 4 sites use the simple pattern-handling→`"%s execution: %w"` shape (`task.go:95`, `review.go:75`, `review.go:128`, `plan_creation.go:110`); `external_review.go`'s `handleExecutorError` adds a manual-break check and a per-call `tool` arg; `finalize.go` is **best-effort** (returns `nil` via `//nolint:nilerr`, never wraps). Only the 4 simple sites share a shape — `handleExecutorError` and `finalize.go` must stay distinct.
  - `pkg/progress`: `progress.go` (timestamped write block in ~8 methods; mode→suffix switch duplicated).
  - `pkg/web`: `tail.go`, `session_progress.go`, `server.go`, `broadcast_logger.go` (tailer/loader Event construction; dead `SetTailer`; deprecated `Event.JSON` shim; duplicated plan encode-and-write).
  - `cmd/ralphex`: `main.go` (model[:effort] resolution in 4 sites; two near-identical codex banner funcs).
  - `pkg/processor`: `executor_factory.go`, `execution_policy.go` (`ParseModelEffort` exported with no out-of-package caller; `executionPolicy`/`executor_factory` naming collision).
  - `pkg/plan`: `parse.go`, `plan.go` (`FileHasUncompletedCheckbox` re-implements `Checkbox.IsActionable`; misleading "excluding completed/" comments).
- Related patterns found: the codebase already uses embedded sub-structs (`NotifyParams`, `Colors`), but config grouping is constrained by Go keyed composite literal compatibility. `external_review.go` already has a `handleExecutorError` helper — the phase refactor generalizes it.
- Dependencies identified: tasks are mostly independent per package; `pkg/config` is the highest-leverage and should land first. No cross-package API changes are required by any task.

## Development Approach
- **testing approach**: Rely on existing tests as the behavior-preservation safety net (these are refactors, not new behavior). For each task: run the affected package's existing tests and keep them green. Add tests **only** where a newly extracted helper/method has no existing coverage of its own. Do not rewrite passing tests to match new internal structure unless a signature genuinely changed.
- complete each task fully before moving to the next.
- make small, focused changes; one package per task.
- **CRITICAL: all tests must pass before starting the next task** — no exceptions.
- **CRITICAL: `make lint` must be clean before starting the next task.**
- **CRITICAL: update this plan file when scope changes during implementation.**
- run `make test` and `make lint` after each task.
- maintain backward compatibility (config files, CLI flags, error wording, signals all unchanged).

## Code-Quality Rules (HARD — verify against every task before marking complete)

These rules supplement project CLAUDE.md and are NOT optional. They are the gate for marking any task complete. If a rule is violated, the task is not done — refactor, re-test, then mark complete.

**Signatures (hard limits):**
- No function or method has 4+ parameters. `ctx context.Context` does not count toward the budget. If you need 4+, use an option struct (e.g., `type fooOpts struct { ... }`).
- No function or method has 4+ return values. Split the function into two single-purpose ones, or return a struct.
- Multiple adjacent same-type parameters (`oldLine, newLine int`) are a swap hazard — review whether they belong on a struct.

**Methods vs standalone helpers (project rule, hard):**
- If a function is called only from methods of a single struct, it MUST be a method on that struct. Calling pattern decides, not field access.
- Standalone helpers are reserved for: (a) constructors and entry points (`Parse...`, `New...`, `Decorate...`), (b) utilities shared by multiple unrelated types or by both standalone functions AND methods, (c) tiny cross-cutting helpers.
- Before adding any standalone helper, mentally walk its callers. If every caller is a method of one type, make the helper a method on that type.

**Visibility (private by default, hard):**
- Lowercase identifiers by default. Only export when an out-of-package caller exists.
- Exception (per CLAUDE.md): methods called by other structs in the same package CAN be exported for inter-component API clarity. This is the only exception. It does not extend to types, functions, constants, or variables.
- Before exporting any new identifier, grep for cross-package callers. If none, lowercase it.

**Comments (default: none, hard):**
- Default to writing no comments. Add one only when the WHY is non-obvious (a hidden invariant, a workaround, behavior that would surprise a reader).
- Exported items get godoc comments starting with the name. Unexported items get lowercase non-godoc comments — or no comment at all.
- Never describe WHAT the code does when the code itself is self-evident. Never write multi-paragraph comments on routine helpers.

**Per-task gate (before marking ANY checkbox complete):**
1. Formatter runs clean (`~/.claude/format.sh` or `gofmt -s -w` + `goimports -w`).
2. `golangci-lint run --max-issues-per-linter=0 --max-same-issues=0` reports zero issues.
3. `go test ./... -race` passes.
4. Scan the new code for the four rule classes above. Specifically:
   - Grep new function signatures: `grep -nE '^func.*\(.*,.*,.*,.*\)' app/<path>/*.go` — any hit with 4+ comma-separated params (excluding `ctx`) is a violation. Same for the return-value side.
   - For every new standalone helper, `grep -rn 'helperName(' --include='*.go'` and confirm at least one caller is NOT a method of a single type. If all callers are methods of one type, convert.
   - For every new exported identifier, grep cross-package. If no out-of-package hit, lowercase it.
5. Only after 1–4 pass: mark the task complete.

If a previous task shipped a violation (spotted later by user, reviewer, or yourself): fix it in the next commit BEFORE starting the next task. Do not let violations accumulate.

**Project-specific note:** this is a refactor — every extracted helper inherits the receiver of the methods it replaces. The three config duration parsers are methods on `valuesLoader`, so the shared parser is a method on `valuesLoader`. The phase `executorName()` and `wrapExecutorError` decisions depend only on `Config`/`Policy`, so they belong on those types (the plan already specifies `(c Config) executorName()`). Do not introduce standalone helpers where a method fits the calling pattern.

## Testing Strategy
- **unit tests**: existing package tests are the primary guard. New tests required only for net-new exported-within-package helpers that lack equivalent coverage.
- **behavior-preservation checks** per task:
  - `pkg/config`: round-trip a representative config (all INI keys set) through load+merge and assert the resulting `Config` is field-for-field identical to pre-refactor (existing config tests already cover this — keep them green).
  - `pkg/processor/phase`: error-wrapping tests must still observe the exact `"%s execution: %w"` strings and the same `HandlePatternMatchError` invocation order.
  - `pkg/progress`: concurrency tests (race detector) must pass — the single-lock-around-the-write-sequence contract is preserved.
  - `pkg/web`: tailer/loader tests must produce identical `Event` streams from the same progress-file input.
- **e2e tests**: `e2e/` (Playwright, `-tags=e2e`) covers the web dashboard. The `pkg/web` task (tailer/loader Event construction, dead-code removal) must keep e2e green — run `go test -tags=e2e -timeout=10m -count=1 ./e2e/...` after that task. Other tasks do not touch UI behavior and do not require an e2e run.
- run with the race detector for the `pkg/progress` and `pkg/web` tasks: `go test -race ./pkg/progress/... ./pkg/web/...`.

## Progress Tracking
- mark completed items with `[x]` immediately when done.
- add newly discovered tasks with ➕ prefix.
- document issues/blockers with ⚠️ prefix.
- update plan if implementation deviates from original scope.

## Solution Overview
- Preserve the flat public `Values` and `Config` field shape while reducing safe internal duplication; Go keyed composite literals make embedded config grouping a public API change.
- Replace each cluster of byte-identical duplicated logic with a single private helper/method, leaving only the genuinely-differing bits at each call site.
- Remove dead code and unexport identifiers with no out-of-package callers.
- All extractions are mechanical and local; no package boundaries or public APIs change.

## Technical Details
- Sub-struct grouping mirrors the existing `NotifyParams`/`Colors` precedent; `loadConfigFromDirs` assigns whole groups, merge operates per group. `*Set` sentinel semantics and precedence (CLI > local > global > embedded) are preserved exactly.
- Helper extractions keep identical inputs/outputs: error strings, lock scope, timestamp format, signal output constants, and JSON encoding are unchanged.
- Line numbers in the discovery report are approximate guidance; the implementer locates the current code by symbol name (the output-rendering layer was flaky during planning, so re-confirm exact ranges at edit time).

## What Goes Where
- **Implementation Steps** (`[ ]` checkboxes): all refactors and quick wins live in this repo.
- **Post-Completion** (no checkboxes): none — this is a self-contained internal refactor with no external-system impact.

## Implementation Steps

### Task 1: Reduce config duplication while preserving public field shape

**Files:**
- Modify: `pkg/config/config.go`
- Modify: `pkg/config/values.go`
- Modify: `pkg/config/config_test.go` (only if a struct-shape change requires test field-path updates)

- [x] ⚠️ premise correction: `Values` and `Config` are **not** a clean four-way mirror — `Values` carries merge-sentinel `*Set` fields (e.g. `CodexModelSet`, `ExecutorSet`, `PassClaudeMdSet`, `MovePlanOnCompletionSet`, the `Notify*Set` group) that `Config` does not; `Config` carries `json:"..."` tags that `Values` does not. The grouping must **preserve this asymmetry**, not unify it. Only group fields that genuinely exist on both with the same meaning; do not add `*Set` fields to `Config` or drop them from `Values`
- [x] identify cohesive option clusters and define embedded sub-structs (candidates: claude, codex, execution/timeouts, error/limit patterns, worktree/plan/paths; notify already grouped) shared by `Values` and `Config` only where field sets match, following the existing `NotifyParams`/`Colors` precedent
  - ⚠️ second-review correction: even the four pattern fields must remain flat exported fields on `Values` and `Config`. Although no in-repo out-of-package composite literal used them, external Go callers can legally write `config.Config{ClaudeErrorPatterns: ...}` and `config.Values{ClaudeErrorPatterns: ...}`. Embedding would break that source-compatible public API shape.
- [x] preserve every INI key name, every `*Set` sentinel field, and **every `json:` tag** exactly; keep pattern fields flat to avoid a public API change
- [x] update `parseValuesFromBytes` to populate the grouped fields (no key renames) — promoted assignments unchanged
- [x] update `loadConfigFromDirs` where safe; pattern fields remain per-field assignments to preserve the flat public struct shape
- [x] update the `mergeFrom` family to merge per group, preserving precedence and `*Set`-gated overrides exactly — pattern merges keep promoted-name access (`len(src.X) > 0`), byte-identical precedence
- [x] delete the drifted hand-listed `*Set` enumeration from the `Config` godoc; keep the one-sentence explanation (inline field comments remain source of truth)
- [x] extract one shared duration parser **as a method on `valuesLoader`** (e.g. `(vl *valuesLoader) parseDurationKey(section *ini.Section, key string) (time.Duration, bool, error)`) to replace the three byte-identical `parseWaitOnLimit`/`parseSessionTimeout`/`parseIdleTimeout` methods; preserve each error string exactly (`invalid <key>: ...`) and the non-negative check; signature stays under the 4-param limit (`ctx` not in play here)
- [x] run existing config tests (load + merge round-trip) — must stay green and assert resulting `Config` is field-for-field identical to before
- [x] **add a JSON round-trip assertion**: marshal a fully-populated `Config` to JSON before and after the refactor and assert byte-identical output (the Go field-equality test does NOT catch `json:` tag drift, which is a real dashboard/API behavior surface consumed by `pkg/web`) — `TestConfig_JSONShape` asserts the exact 37-key set, flattened pattern keys, and absence of `json:"-"` fields
- [x] add a test asserting a field added to a shared sub-struct is visible in both `Values` and `Config` (guards against future drift) only if not already implied by existing round-trip coverage — not applicable after preserving flat public pattern fields for Go API compatibility
- [x] run `make test` and `make lint` — must pass before Task 2

### Task 2: De-duplicate executor-error wrap and executorName in the phase package

**Files:**
- Modify: `pkg/processor/phase/phase.go`
- Modify: `pkg/processor/phase/task.go`
- Modify: `pkg/processor/phase/review.go`
- Modify: `pkg/processor/phase/finalize.go`
- Modify: `pkg/processor/phase/plan_creation.go`
- Modify: `pkg/processor/phase/external_review.go`

- [x] add `func (c Config) executorName() string` next to `isCodexExecutor()` in `phase.go`; remove the four byte-identical per-type `executorName()` methods (task/review/finalize/plan_creation) and route callers through `p.cfg.executorName()`
- [x] add a **new, simpler** `wrapExecutorError(policy Policy, err error, execName string) error` (3 params, 1 return) covering ONLY the 4 simple sites: returns `nil` for nil err, else runs `HandlePatternMatchError` returning `"%s pattern handling: %w"` on match, else wraps with `"%s execution: %w"`. **This is a new helper, NOT a generalization of `handleExecutorError`** — do not fold in the manual-break check or the `tool` arg
- [x] replace the verbatim error-wrap sequence in `task.go`, `review.go` (both sites), and `plan_creation.go` with calls to `wrapExecutorError`; keep error strings and `HandlePatternMatchError` ordering byte-identical. Note `plan_creation.go` returns `planIterationOutcome{}, err` — the caller keeps its own zero-value first return; the helper returns only the `error`
- [x] **do NOT touch `finalize.go`'s error handling** — it is best-effort (`return nil`); only its `executorName()` method is removed (first checkbox). **Do NOT route `external_review.go`'s `handleExecutorError` through the new helper** — its manual-break path is external-review-only; leave it as-is (optionally have it call `wrapExecutorError` *after* its break check, only if byte-identical output is preserved)
- [x] add a one-line comment at the `external_review.go` post-handled-error fall-through (currently unreachable, reads as a latent bug) clarifying it is intentional; add a one-line comment to the empty `continue`-case switch documenting the sleep path
- [x] run existing phase tests — error-wrapping assertions must still observe the exact strings and call order
- [x] add a focused test for `wrapExecutorError` (nil err → nil; non-nil → pattern-match invoked + correct wrap) only if existing tests do not already cover it directly — added `TestWrapExecutorError` to `review_test.go` (existing pattern/nil tests assert only substrings, not the wrap-string contract)
- [x] run `make test` and `make lint` — must pass before Task 3

### Task 3: De-duplicate the timestamped write block in pkg/progress

**Files:**
- Modify: `pkg/progress/progress.go`
- Modify: `pkg/progress/progress_test.go` (only if a new private helper warrants direct coverage)

- [x] add **two private methods on `*Logger`** (callers are all `*Logger` methods, so per the methods-vs-standalone rule these must be methods, not standalone helpers): one producing the timestamp + colored `[%s]` prefix, one performing the mutex-held file+stdout write pair given a format/args and colored payload
- [x] collapse the ~8 single-line write methods to one helper call each; have multi-line methods reuse the timestamp helper
- [x] **preserve the single-lock-around-the-whole-sequence contract exactly** — do not narrow or widen the `writeMu` scope
- [x] resolve the mode→suffix switch duplicated between `filenameWithStem` and `progressFilename`; ⚠️ confirm whether `filenameWithStem` missing the `plan` case is intentional before unifying — **"leave both as-is" is a valid completion** if unifying would force the `plan` case onto `filenameWithStem` and change behavior. Do not silently "fix" the asymmetry
- [x] run `go test -race ./pkg/progress/...` — concurrency tests must pass, output framing unchanged
- [x] run `make test` and `make lint` — must pass before Task 4

### Task 4: Share tailer/loader Event construction and remove dead code in pkg/web

**Files:**
- Modify: `pkg/web/tail.go`
- Modify: `pkg/web/session_progress.go`
- Modify: `pkg/web/server.go`
- Modify: `pkg/web/broadcast_logger.go`
- Modify: corresponding `_test.go` files only where signatures change

- [x] extract `buildPendingSectionEvents(name, phase, ts) []Event` consumed by both the loader (`session_progress.go`) and live tailer (`tail.go`)
- [x] extract `eventFromParsed(parsed, phase) Event` for the timestamp/plain cases, called from both `parseLine` and `parseLineDeferred`; leave only the section-deferral difference in `parseLineDeferred`
- [x] remove dead `Session.SetTailer` (confirmed: only callers are `session_test.go`) — drop the method and its two test usages
- [x] remove the deprecated `Event.JSON` shim (confirmed: production code uses `json.Marshal`/`plan.Plan.JSON`; the only `Event.JSON` callers are `event_test.go`) — delete the method and switch those test assertions to `json.Marshal(event)`; keep test-only `GetTailer`/`getLastPhase` only if other tests still need them
- [x] extract `writePlanJSON` **as a method on `*Server`** (both callers `handlePlan`/`handleSessionPlan` are `*Server` methods) to collapse the identical encode-and-write tail; share the signal-normalization output constants between `broadcast_logger.go` and `tail.go`
- [x] run existing web tests — tailer/loader must produce identical `Event` streams from the same input; run `go test -race ./pkg/web/...`
- [x] run web e2e: `go test -tags=e2e -timeout=10m -count=1 ./e2e/...` — must stay green
- [x] run `make test` and `make lint` — must pass before Task 5

### Task 5: Consolidate model/effort resolution and codex banners in cmd/ralphex

**Files:**
- Modify: `cmd/ralphex/main.go`
- Modify: `cmd/ralphex/main_test.go` (only if a new helper warrants direct coverage)

- [x] add `resolveSpec(cliVal, cfgVal string) string` plus `resolvePlanSpec`/`resolveReviewSpec` (applying the task-spec fallback) and route `createRunner`, `runPlanMode`, `codexModelBanner`, and `codexPlanBanner` through them so banner and runner derive specs from identical code
- [x] extract one helper that resolves a single spec into `codexBannerInfo`; have `codexModelBanner` add the review override and `codexPlanBanner` pass the plan-with-task-fallback spec (collapse the two near-identical banner functions)
- [x] run existing cmd tests — resolved specs and banner output must be identical to before
- [x] add a focused test for `resolveSpec`/`resolvePlanSpec`/`resolveReviewSpec` fallback precedence only if not already covered
- [x] run `make test` and `make lint` — must pass before Task 6

### Task 6: Naming and visibility cleanups in pkg/processor and pkg/plan

**Files:**
- Modify: `pkg/processor/executor_factory.go`
- Modify: `pkg/processor/execution_policy.go` (and rename references)
- Modify: `pkg/plan/parse.go`
- Modify: `pkg/plan/plan.go`
- Modify: corresponding `_test.go` files where identifiers are referenced

- [x] confirm via grep `ParseModelEffort` has no out-of-package caller, then unexport it to `parseModelEffort` (keep `ResolveCodexModelEffort` exported); update in-package and test references
- [x] rename `executionPolicy` → `retryPolicy` (the concrete type, its constructor/opts, and in-package references only). **Do NOT rename the `Run`/`HandlePatternMatchError`/`Sleep` methods** — they satisfy the consumer-side `phase.Policy` interface; renaming them would break that contract. Update the two CLAUDE.md references and the `runner_test.go` comments. (Lowest-value item in the plan — if the rename churn is not worth it, skipping it is acceptable; flag for Eugene's call)
- [x] reuse `Checkbox.IsActionable` / `Task.HasUncompletedActionableWork` inside `FileHasUncompletedCheckbox` instead of re-implementing the actionable rule
- [x] reword the misleading "excluding completed/" comments in `pkg/plan/plan.go` to describe the non-recursive glob mechanism
- [x] run existing processor and plan tests — must stay green (behavior unchanged)
- [x] add a test case asserting `FileHasUncompletedCheckbox` agrees with `Checkbox.IsActionable` on edge cases only if not already covered
- [x] run `make test` and `make lint` — must pass before Task 7

### Task 7: Verify acceptance criteria
- [x] verify all seven refactors and the quick wins from the Overview are implemented
- [x] confirm no INI key, CLI flag, error string, or signal constant changed (grep/diff review)
- [x] run full test suite: `make test`
- [x] run with race detector across touched packages: `go test -race ./pkg/config/... ./pkg/processor/... ./pkg/progress/... ./pkg/web/...`
- [x] run web e2e: `go test -tags=e2e -timeout=10m -count=1 ./e2e/...`
- [x] run `make lint` — zero issues
- [x] cross-compile Windows to verify no build-tag regressions: `GOOS=windows GOARCH=amd64 go build ./...`
- [x] verify coverage did not regress below project standard

### Task 8: [Final] Update documentation
- [x] update `CLAUDE.md` only if a refactor changed a pattern documented there (e.g. config sub-struct grouping, `executionPolicy`→`retryPolicy` rename references in the Key Patterns section) — no new edit needed; the `retryPolicy` references were already updated and the config grouping is internal
- [x] confirm `llms.txt` needs no change (no user-facing behavior changed)
- [x] move this plan to `docs/plans/completed/`

## Post-Completion
*None — this is a self-contained internal refactor. No external systems, consuming projects, deployment configs, or third-party integrations are affected. No CLI/config/behavior surface changes, so no user-facing migration or verification is required.*

---

Smells pre-check: 2 items fixed before save — (1) the shared duration parser was pinned to a method on `valuesLoader`; (2) `writePlanJSON` was pinned to a `*Server` method. All proposed signatures verified under the hard limits (≤3 params excluding ctx, ≤1–3 returns): `(c Config) executorName() string`, `wrapExecutorError(policy, err, execName) error` (standalone justified — shared across unrelated phase types), `buildPendingSectionEvents`/`eventFromParsed` (shared across loader and tailer), `resolveSpec`/`resolvePlanSpec`/`resolveReviewSpec` (main-package free functions). No new exported identifiers; `ParseModelEffort` is being unexported.

Plan-review pass (plan-review agent, verified against code): 3 behavior-preservation fixes applied — (1) Task 2 `wrapExecutorError` clarified as a NEW helper for the 4 simple sites only; `external_review.go`'s `handleExecutorError` (manual-break path) and `finalize.go` (best-effort `nil`) explicitly excluded — the earlier "5 sites" framing was misleading; (2) Task 1 gained a JSON byte-identity assertion (Go field-equality misses `json:` tag drift) and a premise correction noting `Values`/`Config` `*Set` fields are asymmetric, not a clean mirror; (3) Task 3's two progress helpers pinned to `*Logger` methods, and "leave both filename funcs as-is" allowed when unifying would change behavior. Task 6 rename scoped to the concrete type only (interface method names untouched) and flagged as optional/lowest-value. Verdict: ready after these revisions.
