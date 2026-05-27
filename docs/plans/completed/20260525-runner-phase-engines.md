# Refactor Processor Runner Into Phase Engines

## Overview
- Refactor `pkg/processor.Runner` from a large multi-responsibility orchestration object into a small pipeline coordinator with injected phase engines.
- Split the current 1600+ line `runner.go` and 4800+ line `runner_test.go` by responsibility, not by mechanical method shuffling.
- Preserve current behavior for task execution, review loops, external review, plan creation, finalize, timeout handling, break/pause handling, prompt expansion, and late-bound setter dependencies.
- Keep the public processor API compatible unless a plan update explicitly justifies a breaking change.

## Context (from discovery)
- project/language/framework: Go CLI project using `jessevdk/go-flags`; package `pkg/processor` orchestrates ralphex execution.
- files/components involved: `pkg/processor/runner.go`, `pkg/processor/runner_test.go`, `pkg/processor/prompts.go`, `pkg/processor/export_test.go`, `cmd/ralphex/main.go` runner construction call sites.
- related patterns found: dependency interfaces are defined in the consumer package; constructors return concrete types; tests use moq-generated mocks and package-external `processor_test` tests with `export_test.go` seams.
- current coupling: `Runner` owns mode sequencing, phase loops, executor construction, plan-state checks, prompt rendering calls, timeout/limit retry policy, break/pause handling, git no-change/stalemate checks, summary formatting, and plan-draft question handling.
- risk: recent work around codex mode, session/idle timeouts, break/pause resume, and plan-file rename tolerance is concentrated in `Runner`; this refactor must be characterization-first and behavior-preserving.

## Development Approach
- **testing approach**: Regular characterization-first.
- complete each task fully before moving to the next.
- make small, focused changes.
- every code-changing task includes new/updated tests.
- all tests must pass before starting next task.
- update this plan when scope changes.
- maintain backward compatibility unless explicitly rejected.
- do not pass `*Runner` into phase engines; that would only hide the same coupling behind new files.
- prefer concrete unexported types and unexported interfaces; use exported method names on unexported interfaces when package-external tests need fake implementations.
- use a test-only phase injection helper in `export_test.go` for Runner orchestration tests; do not add production-only setters just for tests.
- preserve existing post-construction setters: `SetInputCollector`, `SetGitChecker`, `SetBreakCh`, and `SetPauseHandler` must continue to affect already-constructed runners.

## Code-Quality Rules (HARD — verify against every task before marking complete)

These rules supplement project AGENTS.md/CLAUDE.md and are NOT optional. They are the gate for marking any task complete. If a rule is violated, the task is not done — refactor, re-test, then mark complete.

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
- Exception (per AGENTS.md/CLAUDE.md): methods called by other structs in the same package CAN be exported for inter-component API clarity. This is the only exception. It does not extend to types, functions, constants, or variables.
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

## Testing Strategy
- Use existing `runner_test.go` tests as characterization coverage; move them into focused test files as the corresponding implementation moves.
- Add focused tests for new seams: phase injection, post-construction setters, prompt builder output parity, execution policy timeout/retry behavior, and phase orchestration order.
- Every new `*.go` source file must have a corresponding `_test.go` file unless the file only contains trivial wiring covered by a named existing test file; document any exception in the task's Files block.
- Prefer `go test ./pkg/processor` after each processor task, then `make test`, `make fmt`, and `make lint` for final verification.
- Do not run toy e2e tests without explicit user approval because they can consume claude/codex credits.

## Progress Tracking
- mark completed items with `[x]` immediately when done.
- add newly discovered tasks with `➕`.
- document blockers with `⚠️`.
- keep plan in sync with actual work.

## Solution Overview
- Use the selected phase-engine architecture: `Runner` keeps mode selection and phase ordering only.
- Inject unexported phase interfaces into `Runner`; default construction builds concrete phase engines from `pkg/processor/phase`.
- Use `phase.Deps` for dependencies that are intentionally set after construction. Setters update this holder; phases read it at run time. This avoids stale nil dependencies without making phases call back into `Runner`.
- Keep shared prompt rendering, executor policy, and plan location in `pkg/processor`; keep break handling and git snapshots with the phase package because those support phase engines directly.
- Do not extract every possible helper immediately. Helpers move with the phase that owns them first; promote to a service only when a second phase needs the same behavior and duplication would be worse.
- Keep behavior stable by moving tests with the code and asserting parity instead of rewriting loops from scratch.

## Technical Details
- `Runner.Run(ctx)` remains the public entry point and delegates to mode methods.
- `Runner` fields shrink toward: config, phase interfaces, shared late-bound dependencies, and minimal mode-level dependencies.
- Phase engines are concrete types in `pkg/processor/phase` with narrow exported methods only where `pkg/processor` must construct or call them. Interfaces held by `Runner` stay unexported in `pkg/processor`.
- Shared executor behavior moves from `Runner.runWithLimitRetry`, `runWithSessionTimeout`, `handlePatternMatchError`, idle-timeout tracking, and contextual sleep into `executionPolicy`.
- Timeout state must not be a mutable side channel. `executionPolicy.Run` returns an `executionResult` that contains both the `executor.Result` and a `timedOut` flag for that call.
- Prompt rendering moves from `Runner`-receiver methods in `prompts.go` to `promptBuilder`; phases request final prompts rather than knowing template replacement details.
- Plan path resolution moves to `planLocator`, shared by `promptBuilder` and task-phase plan state checks.
- Existing exported constructors remain usable: `New` and `NewWithExecutors` still exist. Runner phase injection uses an unexported `runnerPhases` struct; production constructors build default phases, and `export_test.go` exposes a test-only helper for orchestration tests.
- External review returns an explicit `phase.ExternalReviewOutcome`, not a bare boolean. The outcome carries only production-consumed state such as `HadFindings`; add fields only when the caller actually needs them.

## What Goes Where
- **Implementation Steps**: code, tests, docs, plan lifecycle.
- **Post-Completion**: manual/external follow-up only, no checkboxes.

## Implementation Steps

### Task 1: Extract shared prompt and execution services

**Files:**
- Create: `pkg/processor/execution_policy.go`
- Create: `pkg/processor/execution_policy_test.go`
- Create: `pkg/processor/prompt_builder.go`
- Create: `pkg/processor/prompt_builder_test.go`
- Create: `pkg/processor/plan_locator.go`
- Create: `pkg/processor/plan_locator_test.go`
- Modify: `pkg/processor/prompts.go`
- Modify: `pkg/processor/runner.go`
- Modify: `pkg/processor/export_test.go`
- Modify: `pkg/processor/runner_test.go`

**Design Contract:**

Type:
- `executionPolicy` (unexported; used only inside `pkg/processor`)
- `executionPolicyOpts` (unexported option struct for construction)
- `executionResult` (unexported result wrapper for one executor call)
- `promptBuilder` (unexported; used only inside `pkg/processor`)
- `promptBuilderOpts` (unexported option struct for construction)
- `planLocator` (unexported; shared by prompt rendering and task plan state checks)
- `runnerDeps` (unexported holder for late-bound dependencies set through Runner setters)

Methods (full signatures):
- `(p *executionPolicy) Run(ctx context.Context, run func(context.Context, string) executor.Result, prompt string, toolName string) executionResult`
- `(p *executionPolicy) HandlePatternMatchError(err error, tool string) error`
- `(p *executionPolicy) Sleep(ctx context.Context, d time.Duration) error`
- `(p *promptBuilder) TaskPrompt() string`
- `(p *promptBuilder) FirstReviewPrompt() string`
- `(p *promptBuilder) SecondReviewPrompt(prefix string) string`
- `(p *promptBuilder) CodexReviewPrompt(isFirst bool, claudeResponse string) string`
- `(p *promptBuilder) CodexEvaluationPrompt(codexOutput string) string`
- `(p *promptBuilder) CustomReviewPrompt(isFirst bool, claudeResponse string) string`
- `(p *promptBuilder) CustomEvaluationPrompt(customOutput string) string`
- `(p *promptBuilder) PlanPrompt() string`
- `(p *promptBuilder) FinalizePrompt() string`
- `(p *planLocator) Path() string`

Standalone helpers planned (justification why NOT a method):
- `ParseModelEffort(s string) (model string, effort string)` — existing exported package helper with existing tests and callers.
- `ResolveCodexModelEffort(spec string, defModel string, defEffort string) (model string, effort string, maxDropped bool)` — existing exported package helper with existing tests and callers.
- `needsCodexBinary(appConfig *config.Config) bool` — existing package-level constructor helper used by runner construction, not by a phase engine.
- `fileExists(path string) bool` — tiny cross-cutting filesystem helper used by setup-hint code; keep package-level unless a single owning type emerges.

Exports (justification per item: who outside the package calls this?):
- no new exported types or functions.
- exported methods on unexported types are allowed here only for same-package inter-component clarity and package-external test fakes.

- [x] move executor retry, session-timeout, idle-timeout, pattern error, and contextual sleep behavior into `executionPolicy` without changing behavior
- [x] make `executionPolicy.Run` return `executionResult` with per-call timeout state; remove the `lastSessionTimedOut` mutable side channel from `Runner`
- [x] move plan path resolution into `planLocator`
- [x] move prompt expansion and final prompt assembly into `promptBuilder` without changing prompt output
- [x] keep `prompts.go` only if it remains the prompt-builder implementation file; otherwise remove the old `Runner` prompt methods
- [x] update `Runner` to hold `prompts *promptBuilder`, `policy *executionPolicy`, `planLocator *planLocator`, and `deps *runnerDeps`
- [x] update existing tests for `runWithLimitRetry`, session timeout, idle timeout, prompt replacement, codex prompt building, model effort helpers, and setup hints to target the new services or test exports
- [x] write parity tests for representative task, review, codex review, custom review, plan, and finalize prompt output
- [x] write tests proving `SetInputCollector`, `SetGitChecker`, `SetBreakCh`, and `SetPauseHandler` update dependencies after `NewWithExecutors`
- [x] run tests: `go test ./pkg/processor`

### Task 2: Extract task phase engine and break controller

**Files:**
- Create: `pkg/processor/task_phase.go`
- Create: `pkg/processor/task_phase_test.go`
- Create: `pkg/processor/break_controller.go`
- Create: `pkg/processor/break_controller_test.go`
- Modify: `pkg/processor/runner.go`
- Modify: `pkg/processor/export_test.go`
- Modify: `pkg/processor/runner_test.go`

**Design Contract:**

Type:
- `taskPhase` (unexported; concrete task-phase engine)
- `taskPhaseOpts` (unexported option struct for construction)
- `taskPhaseRunner` (unexported interface held by `Runner`)
- `runnerPhases` (unexported struct grouping phase interfaces; add fields as phases are extracted)
- `breakController` (unexported; shared break-channel behavior for task and external-review phases)

Methods (full signatures):
- `(p *taskPhase) Run(ctx context.Context) error`
- `(p *taskPhase) ValidatePlanHasTasks() error`
- `(p *taskPhase) HasUncompletedTasks() bool`
- `(p *taskPhase) NextPlanTaskPosition() int`
- `(b *breakController) Context(parent context.Context) (context.Context, context.CancelFunc)`
- `(b *breakController) IsBreak(loopCtx context.Context, parentCtx context.Context) bool`
- `(b *breakController) Drain()`

Standalone helpers planned (justification why NOT a method):
- none; helpers used only by task phase become `taskPhase` methods, and shared break behavior belongs to `breakController`.

Exports (justification per item: who outside the package calls this?):
- no new exported types or functions.
- exported methods on unexported types support same-package inter-component calls and package-external tests.

- [x] define unexported `taskPhaseRunner` interface with `Run(ctx context.Context) error`
- [x] add `runnerPhases` and a test-only phase injection helper in `export_test.go` for Runner orchestration tests
- [x] move `runTaskPhase`, plan task validation, completion checks, and next task position into `taskPhase`
- [x] move task break/pause handling to `taskPhase` using `breakController` and `runnerDeps`
- [x] wire default `taskPhase` into `Runner` construction; `Runner.runFull` and `Runner.runTasksOnly` should call the injected phase interface
- [x] keep `ErrUserAborted` behavior unchanged in full and tasks-only modes
- [x] move task-phase tests out of `runner_test.go` into `task_phase_test.go`
- [x] add orchestration tests proving `Runner` calls task phase and skips reviews on `ErrUserAborted`
- [x] run tests: `go test ./pkg/processor`

### Task 3: Extract internal review and finalize engines

**Files:**
- Create: `pkg/processor/review_phase.go`
- Create: `pkg/processor/review_phase_test.go`
- Create: `pkg/processor/finalize_phase.go`
- Create: `pkg/processor/finalize_phase_test.go`
- Modify: `pkg/processor/runner.go`
- Modify: `pkg/processor/export_test.go`
- Modify: `pkg/processor/runner_test.go`

**Design Contract:**

Type:
- `reviewPhase` (unexported; concrete internal review engine)
- `reviewPhaseOpts` (unexported option struct for construction)
- `reviewPhaseRunner` (unexported interface held by `Runner`)
- `finalizePhase` (unexported; concrete finalize engine)
- `finalizePhaseOpts` (unexported option struct for construction)
- `finalizePhaseRunner` (unexported interface held by `Runner`)

Methods (full signatures):
- `(p *reviewPhase) First(ctx context.Context) error`
- `(p *reviewPhase) Loop(ctx context.Context, prefix string) error`
- `(p *reviewPhase) HeadHash() string`
- `(p *reviewPhase) Section(iteration int, suffix string) status.Section`
- `(p *finalizePhase) Run(ctx context.Context) error`

Standalone helpers planned (justification why NOT a method):
- none; review helpers used only by review phase become `reviewPhase` methods.

Exports (justification per item: who outside the package calls this?):
- no new exported types or functions.
- exported methods on unexported types support same-package inter-component calls and package-external tests.

- [x] move `runReview`, `runReviewLoop`, `headHash`, review section selection, and internal review no-commit detection into `reviewPhase`
- [x] move finalize behavior into `finalizePhase`
- [x] wire `Runner` review-only/full/codex-only paths through injected review and finalize interfaces
- [x] extend `runnerPhases` and its test-only injection helper with review and finalize fields
- [x] preserve codex-executor timeout semantics: first review timeout is an error only in first-class codex mode; default claude mode keeps soft-warning behavior
- [x] move internal review and finalize tests from `runner_test.go` into focused test files
- [x] add Runner orchestration tests for full/review/codex-only phase order using fake phase interfaces
- [x] run tests: `go test ./pkg/processor`

### Task 4: Extract external review engine and shared git state

**Files:**
- Create: `pkg/processor/external_review_phase.go`
- Create: `pkg/processor/external_review_phase_test.go`
- Create: `pkg/processor/git_state.go`
- Create: `pkg/processor/git_state_test.go`
- Modify: `pkg/processor/review_phase.go`
- Modify: `pkg/processor/runner.go`
- Modify: `pkg/processor/export_test.go`
- Modify: `pkg/processor/runner_test.go`

**Design Contract:**

Type:
- `externalReviewPhase` (unexported; concrete codex/custom external review engine)
- `externalReviewPhaseOpts` (unexported option struct for construction)
- `externalReviewPhaseRunner` (unexported interface held by `Runner`)
- `externalReviewOutcome` (unexported result type for external review)
- `gitState` (unexported helper shared by internal review and external review)
- `gitSnapshot` (unexported value type containing head and diff fingerprints)
- `stalemateState` (unexported state object for review-patience tracking)

Methods (full signatures):
- `(p *externalReviewPhase) Tool() string`
- `(p *externalReviewPhase) Run(ctx context.Context) (externalReviewOutcome, error)`
- `(p *externalReviewPhase) RunCodex(ctx context.Context) (externalReviewOutcome, error)`
- `(p *externalReviewPhase) RunCustom(ctx context.Context) (externalReviewOutcome, error)`
- `(p *externalReviewPhase) ShowSummary(toolName string, output string)`
- `(g *gitState) HeadHash() string`
- `(g *gitState) DiffFingerprint() string`
- `(g *gitState) Snapshot() gitSnapshot`
- `(s *stalemateState) Update(before gitSnapshot, after gitSnapshot) bool`

Standalone helpers planned (justification why NOT a method):
- none unless a tiny pure function is shared by tests and multiple unrelated types. Do not keep a callback-bag `externalReviewConfig` with behavior callbacks.

Exports (justification per item: who outside the package calls this?):
- no new exported types or functions.
- exported methods on unexported types support same-package inter-component calls and package-external tests.

- [x] move external review tool selection, codex/custom loop, claude evaluation, stalemate detection, external break handling, and summary formatting into `externalReviewPhase`
- [x] replace the callback-bag `externalReviewConfig` with explicit codex/custom methods or a tiny unexported tool interface; do not build a generic mini-framework for two tools
- [x] make `externalReviewPhase.Run` return `externalReviewOutcome` instead of a bare boolean
- [x] extend `runnerPhases` and its test-only injection helper with the external review field
- [x] move shared git hash/diff logging behavior from `reviewPhase` and external review into `gitState`
- [x] use `breakController` for manual break behavior; do not duplicate channel goroutine logic
- [x] keep `Runner.runExternalAndPostReview` as mode-level orchestration only after codex-specific internals move
- [x] preserve behavior for `external_review_tool=none`, `custom`, codex default, first-class codex mode, `review_patience`, `max_external_iterations`, manual break, and timeout retry semantics
- [x] move external-review tests from `runner_test.go` into `external_review_phase_test.go`
- [x] add Runner orchestration tests for skipping post-external review when external review has no findings and running it when findings exist
- [x] run tests: `go test ./pkg/processor`

### Task 5: Extract plan creation engine

**Files:**
- Create: `pkg/processor/plan_creation_phase.go`
- Create: `pkg/processor/plan_creation_phase_test.go`
- Modify: `pkg/processor/runner.go`
- Modify: `pkg/processor/export_test.go`
- Modify: `pkg/processor/runner_test.go`

**Design Contract:**

Type:
- `planCreationPhase` (unexported; concrete interactive plan creation engine)
- `planCreationPhaseOpts` (unexported option struct for construction)
- `planCreationPhaseRunner` (unexported interface held by `Runner`)
- `draftReviewResult` (existing unexported result type may move with this phase)

Methods (full signatures):
- `(p *planCreationPhase) Run(ctx context.Context) error`
- `(p *planCreationPhase) HandleDraft(ctx context.Context, output string) draftReviewResult`
- `(p *planCreationPhase) HandleQuestion(ctx context.Context, output string) (bool, error)`

Standalone helpers planned (justification why NOT a method):
- existing signal parsers remain standalone because they are package-level parsing utilities covered by `signals.go` and used independently from a single phase type.

Exports (justification per item: who outside the package calls this?):
- no new exported types or functions.
- exported methods on unexported types support same-package inter-component calls and package-external tests.

- [x] move plan creation loop, question handling, draft review handling, and `draftReviewResult` into `planCreationPhase`
- [x] read `InputCollector` from `runnerDeps` at run time so `SetInputCollector` after construction keeps working
- [x] wire `Runner.Run` plan mode through injected `planCreationPhaseRunner`
- [x] extend `runnerPhases` and its test-only injection helper with the plan creation field
- [x] preserve `ErrUserRejectedPlan`, malformed signal warnings, revision feedback preservation across timeout, and input collector error behavior
- [x] move plan-creation tests from `runner_test.go` into `plan_creation_phase_test.go`
- [x] add Runner orchestration test for plan mode delegating to injected plan creation phase
- [x] run tests: `go test ./pkg/processor`

### Task 6: Shrink Runner and split remaining construction tests

**Files:**
- Modify: `pkg/processor/runner.go`
- Create: `pkg/processor/executor_factory.go` if construction code still dominates `runner.go`
- Create: `pkg/processor/executor_factory_test.go` if construction tests need their own file
- Modify: `pkg/processor/runner_test.go`
- Modify: `pkg/processor/export_test.go`
- Modify: `pkg/processor/mocks/*.go` if `go generate` is required after interface changes

**Design Contract:**

Type:
- `executorFactory` (optional unexported type; create only if executor construction remains large enough to obscure Runner)

Methods (full signatures):
- `(f *executorFactory) Build(cfg Config, log Logger) Executors`

Standalone helpers planned (justification why NOT a method):
- existing constructor entry points `New` and `NewWithExecutors` remain standalone because they are package entry points.
- codex/claude executor builder helpers may remain `Config` methods if they are called from construction functions and keep construction readable.

Exports (justification per item: who outside the package calls this?):
- no new exported types or functions.
- exported methods on unexported types support same-package inter-component calls and package-external tests.

- [x] remove obsolete `Runner` methods and test seams that moved to phase engines or services
- [x] keep `runner.go` focused on `Config`, `Runner`, constructors, mode sequencing, phase interface definitions, top-level errors, and late-bound setter methods
- [x] move executor construction code to `executor_factory.go` only if it still makes `runner.go` hard to read after phase extraction
- [x] regenerate mocks with `go generate ./pkg/processor` if interface definitions changed
- [x] split remaining construction/model/setup-hint tests out of `runner_test.go` if they are no longer Runner behavior tests
- [x] run tests: `go test ./pkg/processor`

### Task 7: Verify acceptance criteria

**Files:**
- Modify: `pkg/processor/runner.go`
- Modify: `pkg/processor/*_test.go`
- Modify: `README.md` or `CLAUDE.md` only if the refactor changes documented architecture or project guidance

- [x] verify `Runner` no longer owns phase loop internals and only coordinates mode sequencing
- [x] verify phase engines are injected through unexported interfaces and default constructors wire concrete implementations
- [x] verify `promptBuilder`, `executionPolicy`, `planLocator`, `breakController`, and `gitState` have no dependency on `*Runner`
- [x] verify no phase engine accepts `*Runner` or calls back into `Runner` methods
- [x] verify post-construction setters still affect phase behavior: `SetInputCollector`, `SetGitChecker`, `SetBreakCh`, and `SetPauseHandler`
- [x] verify `runner_test.go` is reduced to orchestration/wiring tests and behavior tests live with their engine files
- [x] run formatter: `make fmt`
- [x] run full test suite: `make test`
- [x] run linter: `make lint`

### Task 8: Migrate phase engines to processor phase subpackage

**Files:**
- Create: `pkg/processor/phase/*.go`
- Create: `pkg/processor/phase/*_test.go`
- Modify: `pkg/processor/runner.go`
- Modify: `pkg/processor/executor_factory.go`
- Modify: `pkg/processor/export_test.go`
- Modify: `pkg/processor/*_test.go`
- Modify: `pkg/processor/signals.go` only if signal helpers must be aliased for compatibility
- Modify: `CLAUDE.md` if the documented processor architecture changes

**Design Contract:**

Type:
- `phase` package (subpackage under `pkg/processor/phase`; must not import `pkg/processor`)
- phase engine concrete types and constructor option structs (export only what `pkg/processor` must construct or call)
- `phase.Deps` or equivalent late-bound dependency holder (if needed so existing Runner setters keep working)

Methods (full signatures):
- keep Runner consumer interfaces in `pkg/processor` with the same small method sets already used by `runnerPhases`
- expose only the methods those consumer interfaces require, such as `Run`, `First`, `Loop`, `Tool`, and validation methods
- use option structs for constructors; do not add constructors with 4+ parameters

Standalone helpers planned (justification why NOT a method):
- signal parsing helpers may move with the phase package because plan-creation phase consumes them; preserve `processor` compatibility with aliases or wrapper tests if current callers require it
- constructor entry points `processor.New` and `processor.NewWithExecutors` remain in `pkg/processor` because they are package entry points

Exports (justification per item: who outside the package calls this?):
- phase constructors and option structs are exported only because `pkg/processor` must build concrete phase engines across a package boundary
- phase dependency interfaces are defined where consumed by the phase package; Runner-facing phase interfaces stay in `pkg/processor` as consumer-side interfaces

- [x] create `pkg/processor/phase` and move concrete task, review, external review, finalize, plan creation, and required shared phase support there
- [x] avoid import cycles: `pkg/processor/phase` must not import `pkg/processor`; pass config/dependencies through phase-owned option structs and interfaces
- [x] keep Runner-owned consumer interfaces in `pkg/processor` and assign phase package concrete values to those interfaces
- [x] preserve existing `processor` public API: `New`, `NewWithExecutors`, setters, exported signals, and exported sentinel errors if they are currently available
- [x] preserve late-bound setter behavior for `SetInputCollector`, `SetGitChecker`, `SetBreakCh`, and `SetPauseHandler`
- [x] move corresponding tests with the moved phase code; every new phase package source file must have corresponding tests unless covered by a named moved test file
- [x] verify no phase package code imports `pkg/processor` and no phase engine depends on `*Runner`
- [x] run tests: `go test ./pkg/processor ./pkg/processor/phase`
- [x] run formatter: `make fmt`
- [x] run full test suite: `make test`
- [x] run linter: `make lint`

### Task 9: [Final] Update documentation and prepare archival

**Files:**
- Modify: `docs/plans/20260525-runner-phase-engines.md`
- Modify: `README.md` only if the refactor changes user-visible behavior
- Modify: `CLAUDE.md` only if the refactor establishes a new durable processor architecture pattern that future agents need to follow

- [x] update README.md or project docs if the refactor changes user-visible behavior; otherwise leave docs unchanged
- [x] update project agent guidance if a new durable processor architecture pattern was created
- [x] verify the active plan remains in `docs/plans/`; ralphex completion handling moves it to `docs/plans/completed/`

## Post-Completion
*Items requiring manual intervention or external systems. No checkboxes.*

- Ask before running the toy end-to-end test because it can consume claude/codex credits.
- If the toy e2e is approved, verify task execution, external review streaming, and finalize still render progress as expected.
