# First-class codex executor mode (--codex)

## Overview

Promote `CodexExecutor` (today only used for the external review phase) to a full peer of `ClaudeExecutor` so task execution, internal review phases, and finalize all run through codex when `--codex` is set. Goal: prepare ralphex for Anthropic's June 15 billing split by giving Max-subscription users an easy switch to run the entire pipeline on their codex/OpenAI plan instead of the new $200 Claude Agent SDK credit pool.

`--codex` is the user-facing flag. Internally it sets a new `Executor` field on the runner config and auto-disables the external review phase (codex-reviewing-codex is a same-model self-review with weak signal; cross-model independence was the whole reason that phase existed). The existing `codex-as-claude.sh` wrapper stays for backwards compatibility but stops being the recommended path.

Independent companion flag `--pass-claude-md` opts in to making codex read project + user CLAUDE.md as if they were AGENTS.md. Implemented by writing a temp `CODEX_HOME` directory per invocation, holding a generated `config.toml` (multi-agent + agent registration + project-doc fallback filenames) and optionally an `AGENTS.md` built from `~/.claude/CLAUDE.md`.

## Success Criteria

- `ralphex --codex docs/plans/<plan>.md` runs the full pipeline (task → review_first → review_second → finalize) through codex with zero claude invocations
- `ralphex --codex --pass-claude-md docs/plans/<plan>.md` additionally has codex reading project + user CLAUDE.md (verifiable by inspecting codex's per-invocation `CODEX_HOME/AGENTS.md`)
- `--codex` is mutually exclusive with `--external-only` / `--codex-only` (deprecated alias) and with `--external-review-tool=<X>` for `X != none`; CLI validation rejects each combination with a clear error message
- `--pass-claude-md` without `--codex` rejected with a clear error message
- config-file `external_review_tool = <X>` is silently overridden when `--codex` is in effect (CLI wins, warning emitted to stderr)
- existing modes (`--review`, `--external-only`, `--tasks-only`, default full) unchanged byte-for-byte when `--codex` is NOT set
- the `Executors` struct field rename (`Claude`/`ReviewClaude`/`Codex` → `Task`/`Review`/`External`) does not break any cross-package consumer; cmd/ralphex and all tests compile and pass
- README, llms.txt, docs/custom-providers.md, and CLAUDE.md updated to document the new flag, the skipped external review, the CLAUDE.md passthrough, the prompt-customization split, and the minimum codex version

## Context (from discovery)

**Files involved:**
- `cmd/ralphex/main.go` — flag parsing (around lines 41-45 for review-tool flags, 815-825 for mode resolution), validation, config merge (around 1308)
- `pkg/processor/runner.go` — mode dispatch (line 36-39 Mode enum, 290 switch), `Executors` struct (line 220-area), `runWithLimitRetry`, claude-review loops, prompt-loading site for review_first / review_second
- `pkg/executor/executor.go` — `ClaudeExecutor` (lines 240-340), shared `Result`/`PatternMatchError`/`LimitPatternError`/idle-timeout primitives
- `pkg/executor/codex.go` — `CodexExecutor` (lines 71-95), `processStderr` (300+), `readStdout` (339+), error/limit pattern checking (223+)
- `pkg/processor/prompts.go` — `replacePromptVariables`, agent reference expansion (around lines 14-15, 177-216)
- `pkg/processor/signals.go` — substring-regex signal detection; verified at lines 16-29 (not JSON-coupled, plain text)
- `pkg/config/config.go` — config struct + INI merge with `*Set` sentinel pattern (line 62 `ExternalReviewTool`)
- `pkg/config/defaults/prompts/{task,review_first,review_second,finalize}.txt` — task and finalize are agent-agnostic (verified, no Task tool references); review_first / review_second contain claude-specific Task tool prose
- `pkg/config/defaults/agents/*.txt` — five agent files reused unchanged across executors
- `scripts/codex-as-claude/codex-as-claude.sh` — legacy wrapper, stays in tree

**Related patterns:**
- `ExternalReviewToolSet`/`PreserveAnthropicAPIKeySet` sentinel pattern for distinguishing "not set" from explicit value during config merge — same pattern for new flags
- Per-invocation temp file for prompt context (already in `pkg/processor/prompts.go` and `pkg/executor/codex.go` external-review prompts) — extend to per-invocation `CODEX_HOME` dir
- Sandbox stays `danger-full-access` (matches existing `CodexExecutor` default and the wrapper)

**Dependencies:**
- codex CLI ≥ a version that supports `[features] multi_agent`, `[agents.<name>]`, and `CODEX_HOME` env-var override (verified in official docs at developers.openai.com/codex/config-reference)
- no new Go dependencies needed

## Development Approach

- **testing approach**: Regular (code first, then tests in same task)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - tests are not optional - they are a required part of the checklist
  - write unit tests for new functions/methods
  - write unit tests for modified functions/methods
  - add new test cases for new code paths
  - update existing test cases if behavior changes
  - tests cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task** - no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run tests after each change
- maintain backward compatibility — existing modes (`--review`, `--external-only`, `--tasks-only`, default full) must keep working byte-identical without `--codex`

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

## Testing Strategy

- **unit tests**: required for every task (see Development Approach above)
- **e2e toy test** (per CLAUDE.md "End-to-End Testing" section): Task 10 runs `--codex` against the toy project at `/tmp/ralphex-test` and verifies pipeline shape, output streaming, and signal handling
- ralphex has playwright-based e2e for the web dashboard (`e2e/` directory, build tag `e2e`) — not exercised by this change since `--codex` doesn't touch the dashboard. Skip e2e/ web tests for this plan.

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope
- keep plan in sync with actual work done

## Solution Overview

`--codex` is wired as an **executor switch**, not a new pipeline mode. The pipeline `Mode` enum stays as-is (`ModeFull`/`ModeTasksOnly`/`ModeReview`/`ModeCodexOnly`/`ModePlan`); a new `Executor` string field on the runner config (`""` → claude, `"codex"` → codex) decides which executor handles task / first review / second review / finalize phases. When `Executor="codex"`, `ExternalReviewTool` is forced to `"none"` so the external review phase is skipped without a separate mode branch.

The consumer-side `processor.Executor` interface at `pkg/processor/runner.go:71` already covers this shape (`Run(ctx, prompt) executor.Result`). Both `*executor.ClaudeExecutor` and `*executor.CodexExecutor` already satisfy it. No new interface is needed. The existing `Executors` struct at `pkg/processor/runner.go:100` is restructured from executor-named fields (`Claude`, `ReviewClaude`, `Codex`, `Custom`) to role-named fields (`Task`, `Review`, `External`, `Custom`); the constructor populates them based on `cfg.Executor` and existing `TaskModel`/`ReviewModel` config. Phase code calls `r.executors.Task.Run(ctx, prompt)` and the constructor decides which underlying executor that resolves to. Streaming internals are NOT shared — claude is JSON stream-events, codex is plaintext-on-stdout with live stderr scan; each executor keeps its own parser.

Codex review phases need `[features] multi_agent = true` plus an `[agents.reviewer]` declaration. **These are passed as additive `-c` flag overrides per invocation, NOT via `CODEX_HOME` redirection.** Codex's `-c` mechanism layers over the user's existing `~/.codex/config.toml` without replacing it — any user customizations (model, sandbox, MCP server config, etc.) remain in effect. The agent description is a short one-line string, so the TOML-escaping concern the smells pre-check originally flagged does not apply here (the multi-line agent body lives in the `spawn_agent(task='...')` argument inside the prompt, not in config). Concretely, ralphex passes:

- `-c features.multi_agent=true` (review phases only; omitted for task and finalize, which are single-agent)
- `-c agents.reviewer.description="general code review specialist; behavior driven by the task argument"` (review phases only)
- `-c project_doc_fallback_filenames=["CLAUDE.md"]` (only when `--pass-claude-md`)

For `--pass-claude-md`, the fallback-filenames override is what gets project-level `./CLAUDE.md` picked up by codex's native AGENTS.md walk — no temp file needed for project-level. For user-level `~/.claude/CLAUDE.md`: **ralphex does NOT modify the user's `~/.codex/` directory**. If the user wants user-level CLAUDE.md content in codex, they set it up themselves (`ln -s ~/.claude/CLAUDE.md ~/.codex/AGENTS.md`, or copy + maintain manually). At the first `--codex --pass-claude-md` run, if `~/.claude/CLAUDE.md` exists AND `~/.codex/AGENTS.md` does not, ralphex prints a one-time hint with the suggested symlink command and continues. No filesystem state outside the project is created or modified by ralphex.

Project-level `./CLAUDE.md` is discovered by codex's own AGENTS.md walk thanks to the `project_doc_fallback_filenames` override.

Prompts: `task.txt` and `finalize.txt` are agent-agnostic and reused. New `review_first_codex.txt` and `review_second_codex.txt` mirror the structure of the claude versions but use codex `spawn_agent(agent='reviewer', task='...')` / `wait_agent` vocabulary. Prompt set resolution happens once at runner init based on `Executor`, not per phase call.

The `{{agent:<name>}}` placeholder still lives in `pkg/processor/prompts.go`. Its expansion gains an executor-aware branch: claude produces `Task tool(subagent_type='general-purpose', ...)` text, codex produces `spawn_agent(agent='reviewer', task=...)` text with the agent body inlined. Agents themselves (`agents/*.txt`) stay unchanged.

## Technical Details

**Reused interface** (not new):

`pkg/processor/runner.go:71` already defines `type Executor interface { Run(ctx context.Context, prompt string) executor.Result }`. Both `*executor.ClaudeExecutor` and `*executor.CodexExecutor` already satisfy it (no signature changes needed). No new interface is introduced.

**Restructured `Executors` struct** (`pkg/processor/runner.go:100` — same struct, replaced field set):

```go
type Executors struct {
    Task     Executor                  // task phase + claude-style review phases
    Review   Executor                  // review phases (optional override; nil = use Task)
    External Executor                  // external review phase (codex by default; nil when Executor=codex)
    Custom   *executor.CustomExecutor  // unchanged
}
```

The existing `Claude` / `ReviewClaude` / `Codex` fields are dropped. The constructor maps `cfg.Executor`, `cfg.TaskModel`, `cfg.ReviewModel` into the new role-named fields. This eliminates the "two ways to set the task executor" footgun and lines up with executor-agnostic phase routing.

**New type** (in `pkg/executor/codex.go`, package-private):

```go
type codexConfigOpts struct {
    multiAgent          bool
    fallbackToClaudeMd  bool
}

func (o codexConfigOpts) cliArgs() []string
```

`cliArgs` returns the `-c <key>=<value>` arg slice ready to splice into the codex CLI args. No filesystem operations, no temp dirs, no cleanup. Method on `codexConfigOpts` (not standalone) because it operates on opts state and is called only from the executor's `Run` path.

The previous design's `codexEnv` / `codexEnvOpts.build()` / `codexEnvOpts.close()` temp-`CODEX_HOME` scheme was dropped after plan-review revealed it would replace the user's `~/.codex/config.toml` and silently drop user customizations.

**New config fields** (split across `pkg/config/values.go` and `pkg/config/config.go` — matches the established `PreserveAnthropicAPIKey` precedent):

```go
// pkg/config/values.go — Values type holds raw + sentinels for merge
type Values struct {
    // ... existing ...
    Executor        string // "" or "codex" (config-loadable; CLI also sets this)
    ExecutorSet     bool   // sentinel for local-overrides-global merge
    PassClaudeMd    bool
    PassClaudeMdSet bool   // sentinel for local-overrides-global merge
}

// pkg/config/config.go — Config carries only resolved values
type Config struct {
    // ... existing ...
    Executor     string // "" (= claude, default) or ExecutorCodex
    PassClaudeMd bool
}

// constants in pkg/config so call sites reference constants, not "codex" string literals
const (
    ExecutorClaude = ""
    ExecutorCodex  = "codex"
)
```

Sentinel placement matches `PreserveAnthropicAPIKey` / `PreserveAnthropicAPIKeySet` (`Values` only, `Config` carries the resolved bool). CLAUDE.md "Configuration" section calls this out as the load-bearing pattern.

**New Prompts struct fields** (`pkg/config/prompts.go` — extend existing `Prompts`):

```go
type Prompts struct {
    // ... existing ...
    ReviewFirstCodex  string
    ReviewSecondCodex string
}
```

Loaded in `Load()` via the same `loadPromptWithLocalFallback(localDir, globalDir, reviewFirstCodexPromptFile)` pattern, with new filename constants `reviewFirstCodexPromptFile = "review_first_codex.txt"` and `reviewSecondCodexPromptFile = "review_second_codex.txt"` in `pkg/config/config.go:20`. Two new `ReviewFirstCodexPrompt` / `ReviewSecondCodexPrompt` fields on `Config` carry the loaded content. The runner constructor selects between the claude and codex variants based on `cfg.Executor`.

**New CLI flags** (`cmd/ralphex/main.go`):

```go
type opts struct {
    // ... existing ...
    Codex        bool `long:"codex" description:"use codex CLI as the executor for task, review, and finalize phases (skips external review)"`
    PassClaudeMd bool `long:"pass-claude-md" description:"pass project and user CLAUDE.md to codex as AGENTS.md (--codex only)"`
}
```

`--codex` sets `cfg.Executor = "codex"` and `cfg.ExternalReviewTool = "none"` (unless user explicitly passed `--external-review-tool` to override, in which case fail with a clear error message). `--pass-claude-md` without `--codex` fails validation with a clear error.

**Codex `-c` flag args** (built per invocation by `codexConfigOpts.cliArgs()`):

```
review phases:  -c features.multi_agent=true \
                -c agents.reviewer.description="general code review specialist; behavior driven by the task argument"

+ when --pass-claude-md:  -c project_doc_fallback_filenames=["CLAUDE.md"]

task / finalize phases:  (no overrides — single-agent, default config)
+ when --pass-claude-md:  -c project_doc_fallback_filenames=["CLAUDE.md"]
```

All overrides are additive on top of the user's `~/.codex/config.toml`. No user state is modified.

**Existing wrapper bug worth a separate fix** (out of scope for this plan, mention in PR body): `scripts/codex-as-claude/codex-as-claude.sh:36-37` passes two `-c project_doc=...` flags. `project_doc` is single-value; second overwrites first. Worth a follow-up PR; this plan does NOT touch the wrapper.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): Go code, prompt files, config wiring, tests, docs in this repo
- **Post-Completion** (no checkboxes): the e2e toy test run is a manual sanity check that's done WITHIN Task 10; nothing leaks beyond this repo

## Implementation Steps

### Task 1: Add --codex and --pass-claude-md flags + Config fields

**Files:**
- Modify: `cmd/ralphex/main.go`
- Modify: `pkg/config/config.go`
- Modify: `pkg/config/config_test.go`

- [ ] add `Codex bool` (long: `--codex`) and `PassClaudeMd bool` (long: `--pass-claude-md`) to the CLI opts struct in `cmd/ralphex/main.go`
- [ ] add `Executor string` + `ExecutorSet bool` and `PassClaudeMd bool` + `PassClaudeMdSet bool` to `Values` in `pkg/config/values.go` (sentinel-only fields live on `Values`, never on `Config` — matches the established `PreserveAnthropicAPIKey` / `PreserveAnthropicAPIKeySet` pattern at `pkg/config/values.go:52-53`)
- [ ] thread both sentinels through the local-overrides-global merge in `mergeFrom` at `pkg/config/values.go:517` (one block per field, mirroring the existing `PreserveAnthropicAPIKeySet` block byte-for-byte)
- [ ] add INI parsing for `executor` and `pass_claude_md` keys in `pkg/config/values.go:307`-style sites — set the value, set the `*Set` sentinel
- [ ] add `Executor string` and `PassClaudeMd bool` (resolved values only — NO `*Set` fields) to `Config` in `pkg/config/config.go`; populate them from `values.*` at the resolution site (around `pkg/config/config.go:309`)
- [ ] add `ExecutorClaude = ""` and `ExecutorCodex = "codex"` constants to `pkg/config` so call sites in later tasks reference constants, not string literals
- [ ] in the CLI → Config translation site (around `cmd/ralphex/main.go:1308`), set `cfg.Executor = config.ExecutorCodex` when `--codex` was passed; set `cfg.PassClaudeMd = true` when `--pass-claude-md` was passed
- [ ] **flag combination validation** — return a clear error for each:
  - `--codex --external-only` / `--codex -e`: error `--external-only is incompatible with --codex (external review is skipped in --codex mode)`
  - `--codex --external-review-tool=<X>` when `<X>` is not `none`: error `--external-review-tool is incompatible with --codex (external review is skipped)`
  - `--pass-claude-md` without `--codex`: error `--pass-claude-md requires --codex`
  - **also handle config-file precedence**: if config file sets `executor = codex` AND `external_review_tool = codex`, the CLI resolution must force `ExternalReviewTool = "none"` and log a warning that the config-file `external_review_tool` was overridden (do NOT fail — config-only conflicts are silently resolved with a warning, only CLI-flag conflicts are hard errors)
- [ ] when `--codex` is set (or config-file `executor = codex` when CLI doesn't override): force `cfg.ExternalReviewTool = "none"` after all merging is done
- [ ] write unit tests covering: the new `Values` sentinel-merge behavior (local-only `executor = codex` set, global-only set, both set with local winning, neither set); CLI resolution for all the flag combinations above (success cases + each error path); config-file-only `executor = codex` correctly resolves with `ExternalReviewTool = none` and emits the warning
- [ ] run tests - must pass before next task

### Task 2: Restructure Executors to role-named fields

**Design Contract:**

Type:
- no new types — reuses existing `processor.Executor` interface at `pkg/processor/runner.go:71` (both `*ClaudeExecutor` and `*CodexExecutor` already satisfy it)

Methods (full signatures):
- none added (existing `Run(ctx context.Context, prompt string) executor.Result` on both executors stays unchanged)

Standalone helpers planned (justification why NOT a method):
- none

Exports (justification per item: who outside the package calls this?):
- no new exports; `Executors` struct stays exported (existing) but its FIELD SET is replaced (`Claude` / `ReviewClaude` / `Codex` → `Task` / `Review` / `External`). All callers of these fields are inside the package, so the field rename is internal churn

**Files:**
- Modify: `pkg/processor/runner.go`
- Modify: `pkg/processor/runner_test.go`

- [ ] in `pkg/processor/runner.go:100`, replace the `Executors` struct field set: drop `Claude` / `ReviewClaude` / `Codex`, add `Task Executor` / `Review Executor` / `External Executor` / keep `Custom *executor.CustomExecutor`. **Rationale for the rename** (not just-cosmetic): in `--codex` mode, the existing `Claude` field would hold a codex executor — the name actively lies. Role-named fields keep the constructor honest and eliminate the "set Claude to codex" footgun reviewers and future contributors will trip over.
- [ ] update the corresponding `Runner` struct fields (lines 111-114) to match: `task`, `review`, `external`, `custom`
- [ ] in the constructor (`New` / `NewWithExecutors`), populate the new fields based on `cfg.Executor`: when `cfg.Executor == config.ExecutorCodex` set `task` and `review` to the codex executor (configured per Tasks 4-6); else leave them as the claude executor; populate `external` from `cfg.ExternalReviewTool` (codex / custom / none → nil)
- [ ] **grep-driven sweep — do not enumerate**: run `grep -rn 'r\.\(claude\|reviewClaude\|codex\)\.' pkg/processor/ --include='*.go'` and rewrite EVERY hit to use the new role-named field; methods to expect hits in (non-exhaustive sample for verification): `runFull`, `runReviewOnly`, `runCodexOnly`, `runTasksOnly`, `runPlanCreation`, `runClaudeReview`, `runClaudeReviewLoop`, `runTaskPhase`, `runExternalReviewLoop`, `runFinalize`, `runCodexLoop`, plus the plan-creation loop around line 1142
- [ ] **update all test fixtures**: run `grep -rn 'Executors{' pkg/processor/ --include='*_test.go'` and rewrite every `processor.Executors{Claude: ..., Codex: ..., ...}` literal to use the new field names. Compile must pass before this task can be marked complete.
- [ ] update the existing `runWithLimitRetry` callers (they pass a `func(ctx, prompt) executor.Result` — verify the call sites use the new role-named field as the function source)
- [ ] write tests covering: constructor with default config picks claude for task/review; constructor with `Executor=ExecutorCodex` picks codex for task/review; external resolution unchanged from existing behavior (verify with the existing tests still passing); verify `processor.Executors` zero-value still constructs a usable runner (no panics on unset fields)
- [ ] run tests - must pass before next task

### Task 3: Add regression tests for codex-style signal detection

**Files:**
- Modify: `pkg/processor/signals_test.go` (or create if missing)
- Modify: `pkg/processor/signals.go` (only if a test fails — otherwise no code change)

- [ ] add unit-test cases to `signals_test.go` that feed codex-style free-form output (no JSON envelope, no markdown fences, just plain text with `<<<RALPHEX:COMPLETED>>>` / `<<<RALPHEX:REVIEW_DONE>>>` / `<<<RALPHEX:QUESTION>>>` ... `<<<RALPHEX:END>>>` markers) and confirm `parseQuestionPayload`, `parsePlanDraftPayload`, and the signal-substring detection still work
- [ ] specifically test edge cases codex output may produce: signal preceded by codex reasoning summary text, signal embedded in a multi-paragraph response, signal split across multiple lines, signal followed by additional codex chatter
- [ ] **success criterion — explicit**: if all new tests pass with the existing `signals.go` unchanged, that IS the success path. Mark the task complete with "tests added, no code change needed (signal detection already codex-compatible)". If any test fails, then and only then patch `signals.go`. Investigation-style tasks must have a clean exit when no bug is found.
- [ ] run tests - must pass before next task

### Task 4: Codex `-c` flag override helper (additive, no CODEX_HOME)

**Design Contract:**

Types:
- `codexConfigOpts` (lowercase — package-private, holds options for building `-c` flag args)

Methods (full signatures):
- `(o codexConfigOpts) cliArgs() []string` — returns the `-c <key>=<value>` arg slice ready to splice into codex args

Standalone helpers planned (justification why NOT a method):
- none — `cliArgs` is a method on `codexConfigOpts` because it operates on opts state and is called only from the executor's `Run` path

Exports (justification per item: who outside the package calls this?):
- none — entire surface is package-private to `pkg/executor`

**Why no `CODEX_HOME` temp dir** (revisited after plan-review pass): the original design wrote a temp `CODEX_HOME` directory and redirected codex to it. That would have replaced the user's `~/.codex/config.toml` entirely, dropping all their customizations (model preferences, sandbox settings, MCP servers, etc.). Using `-c` flag overrides is additive: codex's CLI layers `-c` over the existing config, so user state is preserved.

**Files:**
- Modify: `pkg/executor/codex.go`
- Modify: `pkg/executor/codex_test.go`

- [ ] define `codexConfigOpts` struct in `pkg/executor/codex.go` with fields `multiAgent bool` and `fallbackToClaudeMd bool` (2 fields, comfortably under signature limits)
- [ ] implement `(o codexConfigOpts) cliArgs() []string`: returns a slice of args ready to splice into codex CLI invocation. Builds `-c features.multi_agent=true` when `o.multiAgent`; builds `-c agents.reviewer.description="general code review specialist; behavior driven by the task argument"` when `o.multiAgent` (paired with multi_agent — agent registration is meaningless without it); builds `-c project_doc_fallback_filenames=["CLAUDE.md"]` when `o.fallbackToClaudeMd`. Empty options produce an empty slice
- [ ] wire `cliArgs()` into `CodexExecutor.Run`: build a `codexConfigOpts` based on the phase (multi_agent on for review phases, off for task and finalize) and the `PassClaudeMd` field; splice the returned args into the codex CLI args right after `exec` and before any other args
- [ ] write unit tests covering: `cliArgs()` output for all 4 combinations of (multiAgent ✓/✗) × (fallbackToClaudeMd ✓/✗); verify the agent registration arg always pairs with multi_agent; verify empty options return an empty slice; verify the args splice into the codex command in the expected order
- [ ] run tests - must pass before next task

### Task 5: Extend CodexExecutor for streaming task execution + idle timeout

**Files:**
- Modify: `pkg/executor/codex.go`
- Modify: `pkg/executor/codex_test.go`

- [ ] add `IdleTimeout time.Duration` field to `CodexExecutor` (mirroring `ClaudeExecutor.IdleTimeout` at `pkg/executor/executor.go:243`)
- [ ] in `Run`, when `IdleTimeout > 0` wrap the streaming-read loop with the same `time.AfterFunc` + reset-on-line pattern used by `ClaudeExecutor`; gate by a `touch func()` closure invoked from `readStdout` / `processStderr` for each line
- [ ] verify the existing `processStderr` `isCodexErrorLine` gate (around `codex.go:309`) still works correctly when called repeatedly across a long streaming task run; add a regression test feeding multi-paragraph codex output that mentions "rate limit" in passing (e.g., in agent reasoning text without the `error:` prefix) and assert no false-positive pattern match
- [ ] write unit tests covering: idle timeout fires when no output for the specified duration; idle timeout resets on each output line; long streaming task output completes without false-positive limit/error pattern matches
- [ ] run tests - must pass before next task

### Task 6: --pass-claude-md plumbing + user-level CLAUDE.md setup hint

**Files:**
- Modify: `pkg/executor/codex.go`
- Modify: `pkg/processor/runner.go`
- Modify: `pkg/executor/codex_test.go`
- Modify: `pkg/processor/runner_test.go`

- [ ] add `PassClaudeMd bool` field to `CodexExecutor`. Project-level CLAUDE.md handling (the `-c project_doc_fallback_filenames=["CLAUDE.md"]` override) is built from this field via Task 4's `codexConfigOpts.fallbackToClaudeMd`. No filesystem-path field on the executor — we are no longer writing AGENTS.md
- [ ] in the runner constructor (`pkg/processor/runner.go`), when configuring the codex executor, propagate `cfg.PassClaudeMd` onto the executor
- [ ] **add a user-level CLAUDE.md setup hint**, printed once at first `--codex --pass-claude-md` run: if `os.UserHomeDir() + "/.claude/CLAUDE.md"` exists AND `os.UserHomeDir() + "/.codex/AGENTS.md"` does NOT exist, log a single info line via the runner's logger: `hint: ~/.claude/CLAUDE.md exists but ~/.codex/AGENTS.md does not. To get user-level CLAUDE.md content into codex, link it: ln -s ~/.claude/CLAUDE.md ~/.codex/AGENTS.md`. Do NOT create the symlink yourself — ralphex MUST NOT modify user's `~/.codex/`. Continue execution regardless of whether the user follows the hint. Use a sentinel file or runner-state flag to ensure the hint prints once per process, not per phase
- [ ] write integration-style tests: `PassClaudeMd=true` builds codex with the `project_doc_fallback_filenames` override; `PassClaudeMd=false` does not; the setup hint emits exactly once when both conditions are met; the hint emits zero times when `~/.codex/AGENTS.md` already exists; the hint emits zero times when `~/.claude/CLAUDE.md` does not exist
- [ ] use `t.TempDir()` + `os.Setenv("HOME", ...)` (or equivalent) to control the test environment; never touch the real user home
- [ ] run tests - must pass before next task

### Task 7: Codex review prompts (full plumbing)

**Files:**
- Create: `pkg/config/defaults/prompts/review_first_codex.txt`
- Create: `pkg/config/defaults/prompts/review_second_codex.txt`
- Modify: `pkg/config/config.go` (filename constants at line 20, new `ReviewFirstCodexPrompt` / `ReviewSecondCodexPrompt` fields on `Config` at line 116-area, assignment from `prompts.*` at line 349-area)
- Modify: `pkg/config/prompts.go` (extend `Prompts` struct at line 15, two new `loadPromptWithLocalFallback` calls in `Load()`)
- Modify: `pkg/config/defaults_test.go` (extend `expectedPrompts` slice at lines 258 and 300 with the two new basenames)
- Modify: `pkg/config/config_test.go` (any existing prompts-presence assertions need the two new fields)
- Modify: `pkg/processor/runner.go` (prompt selection at init based on `cfg.Executor`)
- Modify: `pkg/processor/runner_test.go`

- [ ] write `review_first_codex.txt` mirroring the structure of `review_first.txt` but replacing Task tool prose with codex `spawn_agent(agent='reviewer', task='...')` syntax; keep the 5 agents (`{{agent:quality}}` etc.) and the same signal vocabulary (`<<<RALPHEX:REVIEW_DONE>>>` / `<<<RALPHEX:TASK_FAILED>>>`); replace "All Task tool calls MUST be in the same message for parallel foreground execution" with the codex equivalent: spawn all 5 reviewer agents in parallel, then `wait_agent` to collect findings
- [ ] write `review_second_codex.txt` similarly mirroring `review_second.txt`
- [ ] **plumb the new prompts through the config layer**: add `reviewFirstCodexPromptFile = "review_first_codex.txt"` and `reviewSecondCodexPromptFile = "review_second_codex.txt"` to the constants block in `pkg/config/config.go:20`. Add `ReviewFirstCodex string` / `ReviewSecondCodex string` to the `Prompts` struct at `pkg/config/prompts.go:15`. Add two `loadPromptWithLocalFallback` calls in `Load()` matching the existing pattern at lines 42-82. Add `ReviewFirstCodexPrompt string` / `ReviewSecondCodexPrompt string` to `Config` at line 116-area. Assign from `prompts.ReviewFirstCodex` / `prompts.ReviewSecondCodex` at the resolution site around line 349-350.
- [ ] **update tests that enumerate expected prompts**: `pkg/config/defaults_test.go` `expectedPrompts` slices at lines 258 and 300 — add the two new basenames so the embedded-defaults coverage test passes
- [ ] **add prompt selection logic in the runner constructor**: when `cfg.Executor == config.ExecutorCodex`, the runner reads `r.cfg.AppConfig.ReviewFirstCodexPrompt` / `ReviewSecondCodexPrompt`; otherwise it reads the existing `ReviewFirstPrompt` / `ReviewSecondPrompt`. Selection happens ONCE at init, not per phase invocation. Do NOT overwrite the AppConfig fields — pick the right field at read time. Both variants keep the user-customization override chain (`~/.config/ralphex/prompts/`, `.ralphex/prompts/`, embedded default) intact through the existing `loadPromptWithLocalFallback` machinery
- [ ] write tests covering: prompt selection picks codex variants when Executor=codex; picks claude variants otherwise; user override `~/.config/ralphex/prompts/review_first_codex.txt` takes precedence over embedded codex default; `defaults_test.go` still passes with the two new basenames in `expectedPrompts`
- [ ] **important behavioral split to call out in Task 11 docs**: a user who customized `~/.config/ralphex/prompts/review_first.txt` for claude mode will NOT have that customization applied under `--codex` (different basename). Document this in Task 11 README/docs. No automatic warning banner in this plan — explicit doc is the contract
- [ ] run tests - must pass before next task

### Task 8: {{agent:name}} expansion: codex spawn_agent syntax

**Files:**
- Modify: `pkg/processor/prompts.go`
- Modify: `pkg/processor/prompts_test.go`
- Modify: `pkg/processor/runner.go` (thread executor-type into the prompt-replacer)

- [ ] **grep for the agent-expansion call site first**: `grep -n '{{agent:' pkg/processor/prompts.go` and `grep -n 'agent:' pkg/processor/prompts.go` to find the exact function. Today it lives in `replacePromptVariables` (around `pkg/processor/prompts.go:151,177-216` per the discovery notes). Threading the executor type as an argument vs a struct field is determined by what shape that function has — confirm during implementation, do not guess
- [ ] add an `agentSyntax` field (private string, values referenced via the `config.ExecutorClaude` / `config.ExecutorCodex` constants from Task 1) on the runner or on whatever struct currently holds the agent-expansion state (confirmed via the grep above)
- [ ] extend the agent-expansion function: when `agentSyntax == config.ExecutorCodex`, expand `{{agent:<name>}}` to a `spawn_agent(agent='reviewer', task='<inlined agents/<name>.txt body with diff context>')` block; otherwise keep the existing `Task tool(subagent_type='general-purpose', ...)` expansion byte-identical
- [ ] set `agentSyntax = cfg.Executor` in the runner constructor (default `ExecutorClaude` keeps the existing claude expansion)
- [ ] write tests covering: `{{agent:quality}}` expands to claude shape by default; same placeholder expands to codex shape when agentSyntax=codex; all five agent names (quality/implementation/testing/simplification/documentation) work in both modes; the inlined agent body is unchanged (only the wrapper differs)
- [ ] run tests - must pass before next task

### Task 9: Pipeline routing — verify external review skip works under --codex

**Files:**
- Modify: `pkg/processor/runner.go` (only if a real bug is found in the existing skip logic)
- Modify: `pkg/processor/runner_test.go`

- [ ] verify the existing external-review-skipping logic (currently keyed on `ExternalReviewTool == "none"`) is hit when `cfg.ExternalReviewTool` was forced to `"none"` by `--codex` in Task 1; confirm by reading `runFull` flow around lines 290-340 and the external-review dispatch path
- [ ] if a separate `Executor == "codex"` check is needed anywhere (e.g., to avoid loading codex_review.txt or to skip phase logging that announces external review), add it in the smallest possible scope
- [ ] **success criterion — explicit**: if the existing `ExternalReviewTool == "none"` skip logic already routes correctly when forced by `--codex` and the new integration test passes with no code change, that IS the success path. Mark the task complete with "tests added, no code change needed (existing skip logic already correct)". If a routing bug is found, then and only then patch `runFull`. Investigation-style tasks must have a clean exit when no bug is found
- [ ] add a runner-level integration test that wires a fake task executor and external executor, sets `Executor=codex` + `ExternalReviewTool=none`, runs `runFull` with a trivial plan, and asserts external executor's `Run` was NEVER called while task executor was called the expected number of times
- [ ] run tests - must pass before next task

### Task 10: End-to-end toy-project verification

**Files:**
- no new code; this task uses the toy project and verifies behavior

- [ ] run `make build` from the repo root
- [ ] run `./scripts/internal/prep-toy-test.sh` to create the toy project at `/tmp/ralphex-test`
- [ ] run `cd /tmp/ralphex-test && "${RALPHEX_BIN:-$OLDPWD/.bin/ralphex}" --codex docs/plans/fix-issues.md` (the binary path resolves to the freshly-built `.bin/ralphex` in the ralphex repo)
- [ ] in a separate terminal, `tail -f .ralphex/progress/progress-fix-issues.txt` and confirm: phase 1 task execution runs codex (not claude); phase 2 first claude review is now codex review (uses codex spawn_agent); external review phase is SKIPPED entirely (no `--- codex external review ---` line); phase 3 second review uses codex; finalize uses codex; plan moves to `docs/plans/completed/`
- [ ] repeat with `--codex --pass-claude-md` and verify codex output indicates it picked up the project CLAUDE.md (look for project-doc references in codex's reasoning text)
- [ ] document any surprises (model differences, prompt tweaks needed, edge cases) in a ➕ task for follow-up — do NOT proceed to docs/finalize until the toy test passes end-to-end
- [ ] write tests: not applicable (manual e2e). Mark this checkbox `[x] tests N/A - e2e verification only`
- [ ] run tests - must pass before next task (unit test suite + linter must still pass)

### Task 11: Documentation updates

**Files:**
- Modify: `README.md`
- Modify: `llms.txt`
- Modify: `docs/custom-providers.md`
- Modify: `CLAUDE.md`

- [ ] add `--codex` and `--pass-claude-md` usage examples to README.md "Quick Usage" section; explain the billing motivation, the skipped external review phase, and the wrapper-vs-first-class distinction
- [ ] update llms.txt mirror sections (Quick Usage + Customization) with the new flags and config fields (`executor = codex`, `pass_claude_md = true`)
- [ ] update `docs/custom-providers.md`: lead with `--codex` as the recommended path for codex-everywhere; mark the `codex-as-claude.sh` wrapper as legacy (kept for backwards compatibility, no longer the recommended approach for new users); add a one-line note about the pre-existing duplicate `-c project_doc=...` flag bug as a known wart of the legacy wrapper
- [ ] **document the minimum codex CLI version**: `--codex` mode relies on `[features] multi_agent`, `[agents.<name>]`, `CODEX_HOME` env-var override, and (when `--pass-claude-md`) `project_doc_fallback_filenames`. Confirm during implementation the actual minimum codex version that supports all four (check `codex --version` output and the developers.openai.com/codex/config-reference page). Add a one-line note in README.md and docs/custom-providers.md: "requires codex CLI ≥ X.Y.Z". If a user runs `--codex` against an older codex, the silent-fallback behavior (no AGENTS.md picked up, multi_agent unrecognized) is opaque — document this as a known limitation, no runtime version check in this plan
- [ ] **document the prompt-customization split**: a user with custom `~/.config/ralphex/prompts/review_first.txt` (claude-mode customization) will NOT have that applied under `--codex`. Tell users to also create `review_first_codex.txt` when they want a customized codex review prompt. Same for `review_second.txt`
- [ ] update CLAUDE.md "Key Patterns" section with a one-paragraph summary of the new `Executor` config field, the `--codex` flag, and the CODEX_HOME-per-invocation pattern; link to relevant pkg/processor and pkg/executor files
- [ ] write tests: not applicable (docs only). Mark as `[x] tests N/A - docs only`
- [ ] run tests - full unit + e2e suite must still pass

### Task 12: Verify acceptance criteria

- [ ] verify all Success Criteria items: `--codex` runs full pipeline through codex; `--codex --pass-claude-md` enables CLAUDE.md context; mutual-exclusion validation rejects `--codex --external-review-tool=...` and `--pass-claude-md` without `--codex` with clear error messages; existing modes unchanged
- [ ] run full test suite: `make test`
- [ ] run linter: `make lint`
- [ ] run formatter: `make fmt`
- [ ] verify e2e test from Task 10 still passes with current build

### Task 13: [Final] Move plan to completed

- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

**Follow-up items** (separate PRs, not in this plan):
- fix the duplicate `-c project_doc=...` flag bug in `scripts/codex-as-claude/codex-as-claude.sh:36-37` (codex's `project_doc` is single-value; second flag overwrites first) — small standalone fix
- evaluate whether the same wrapper bug exists in `cc-thingz/plugins/planning/skills/exec/scripts/run-codex.sh` and patch upstream

**Manual verification:**
- run a real plan (not the toy fix-issues plan) end-to-end with `--codex` against a non-trivial change to catch any model-specific quirks the toy test missed

---

Smells pre-check: 4 items fixed before save (Task 2 dropped redundant `executor.Runner` interface in favor of existing `processor.Executor`; Task 4 moved `newCodexEnv`/`buildCodexConfigTOML` to methods on `codexEnvOpts`; Task 1 added typed `ExecutorClaude`/`ExecutorCodex` constants; Task 6 locked the field-on-executor decision and removed the "pick later" indecision; Task 7 added a documented behavioral-split note for prompt customization).

Plan-review pass: 8 items fixed (Task 1 moved `*Set` sentinels to `Values` per `PreserveAnthropicAPIKey` precedent + added validation for `--codex --external-only` and config-file precedence; Task 2 replaced enumerated call-site list with grep-driven sweep + explicit `runner_test.go` `Executors{...}` rewrite + rename rationale; Task 3 and Task 9 defined the no-change success path for investigation-style tasks; Task 4 specified `close()` semantics — pointer receiver, nil-after-call, best-effort with Debug logging; Task 6 renamed `UserClaudeMdPath` to `ExtraDocPath` and moved filesystem-convention resolution to the runner layer; Task 7 added the full prompt-plumbing surface — `Prompts` struct + `Load()` calls + filename constants + `Config` fields + `defaults_test.go` `expectedPrompts` extension + reframed runner-init as field-selection not overwrite; Task 8 added grep-first step before assuming struct layout; Task 10 replaced absolute path with env-var indirection; Task 11 added codex version doc requirement and prompt-customization-split doc; added explicit Success Criteria section above Implementation Steps).

Interactive revdiff pass (round 2): 1 annotation addressed. **Dropped the per-invocation `CODEX_HOME` temp-directory approach entirely** — it would have replaced the user's `~/.codex/config.toml` and silently dropped all their customizations (model, sandbox, MCP servers, etc.). Replaced with additive `-c` flag overrides that layer over the user's existing config without touching user state. Task 4 rewritten: `codexConfigOpts.cliArgs()` returns `-c` arg slice; `codexEnv` / `build()` / `close()` types removed. Task 6 rewritten: project-level CLAUDE.md goes through the `-c project_doc_fallback_filenames` override; user-level CLAUDE.md becomes a one-time setup hint at first `--codex --pass-claude-md` run (ralphex prints `ln -s ~/.claude/CLAUDE.md ~/.codex/AGENTS.md` suggestion, never touches user's `~/.codex/` itself). Solution Overview and Technical Details revised accordingly.

Class-sweep verifications:
- "incomplete call-site sweep enumeration": fixed in Tasks 2, 7, 8 (all three reframed to grep-driven sweeps with explicit test-fixture inclusion)
- "investigation tasks without clean exit criteria": fixed in Tasks 3 and 9 (both got explicit "no-change success path" criterion)
- "Values-vs-Config sentinel placement": only Task 1 introduced a sentinel; verified no other task affected
- "layering: filesystem-convention encoding in pkg/executor": only Task 6 was affected; Task 4's filesystem ops are codex-specific temp resources that legitimately belong there; verified no other task affected
- "flag-combination validation gaps": only Task 1 owns CLI validation; verified no other task affected
