# Migrate from Claude Code / Codex CLI to GitHub Copilot CLI

## Overview

Replace Claude Code and OpenAI Codex CLI with GitHub Copilot CLI as the sole execution backend. Copilot CLI uses Claude Opus 4.6 for coding/review phases and GPT-5.2-Codex for external review, both accessible via a single `copilot` binary with GitHub authentication. JSONL format is documented in `docs/copilot-jsonl-format.md` with test fixtures in `pkg/executor/testdata/copilot_fixtures/`.

## Context

- Files involved:
  - `pkg/executor/executor.go` (existing — shared types: Result, PatternMatchError, LimitPatternError, CommandRunner, signal detection + ClaudeExecutor to remove)
  - `pkg/executor/codex.go` (existing — CodexExecutor, to be removed)
  - `pkg/executor/codex_test.go` (existing — to be removed)
  - `pkg/executor/executor_test.go` (existing — tests for ClaudeExecutor, to be rewritten)
  - `pkg/executor/custom.go` (existing — CustomExecutor, unchanged)
  - `pkg/executor/custom_test.go` (existing — unchanged)
  - `pkg/executor/linereader.go` (existing — may be reusable for JSONL line reading)
  - `pkg/executor/copilot.go` (to be created — CopilotExecutor)
  - `pkg/executor/copilot_test.go` (to be created)
  - `pkg/config/config.go` (existing — Config struct with claude_*/codex_* fields)
  - `pkg/config/values.go` (existing — INI parsing for config fields)
  - `pkg/config/frontmatter.go` (existing — agent model validation, short keyword normalization)
  - `pkg/config/defaults/config` (existing — embedded default config file)
  - `pkg/config/defaults/prompts/codex.txt` (existing — to be renamed/rewritten)
  - `pkg/config/defaults/prompts/*.txt` (existing — all prompts may reference Claude Code)
  - `pkg/config/defaults/agents/*.txt` (existing — agent files with model frontmatter)
  - `pkg/processor/runner.go` (existing — executor wiring, external review dispatch, binary detection)
  - `pkg/processor/prompts.go` (existing — template variable expansion)
  - `cmd/ralphex/main.go` (existing — CLI setup, binary detection, startup)
  - `scripts/codex-as-claude.sh` (existing — to be removed)
  - `scripts/ralphex-dk.sh` (existing — Docker wrapper with Bedrock support to simplify)
  - `docs/bedrock-setup.md` (existing — to be removed)
  - `docs/custom-providers.md` (existing — to be rewritten for Copilot CLI)
  - `docs/copilot-jsonl-format.md` (existing — JSONL format reference from discovery plan)
  - `pkg/executor/testdata/copilot_fixtures/*.jsonl` (existing — JSONL test fixtures from discovery plan)
- Related patterns: executor interface (CommandRunner), signal detection (`detectSignal`/`matchPattern`), embedded defaults, config `*Set` flags
- Dependencies: GitHub Copilot CLI (GA, standalone binary `copilot`)

## Development Approach

- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- Shared types (Result, PatternMatchError, LimitPatternError, CommandRunner, detectSignal, matchPattern) stay in `executor.go` — only ClaudeExecutor is removed
- JSONL format reference: `docs/copilot-jsonl-format.md`; test fixtures: `pkg/executor/testdata/copilot_fixtures/`
- Key JSONL event types: `assistant.message_delta` (streaming text via `data.deltaContent`), `assistant.message` (complete text via `data.content`, authoritative), `result` (session end, unique envelope — no `data` wrapper), `tool.execution_start`/`tool.execution_complete` (tool activity)
- Important: `assistant.message_delta` may be absent on tool-heavy turns — always use `assistant.message.data.content` as authoritative text source
- CLI errors (exit code 1) produce NO JSONL — error goes to stderr as plain text
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Create CopilotExecutor and update shared executor types

**Files:**
- Modify: `pkg/executor/executor.go` (remove ClaudeExecutor, keep shared types)
- Create: `pkg/executor/copilot.go` (new CopilotExecutor)
- Create: `pkg/executor/copilot_test.go`
- Remove: `pkg/executor/codex.go`
- Remove: `pkg/executor/codex_test.go`
- Modify: `pkg/executor/executor_test.go` (remove ClaudeExecutor tests, keep shared type tests)

- [x] In `executor.go`: remove `ClaudeExecutor` struct and all its methods (`Run`, `processStream`, `extractText`, etc.); keep `Result`, `PatternMatchError`, `LimitPatternError`, `CommandRunner`, `execClaudeRunner` (rename to `execRunner`), `detectSignal`, `matchPattern`, `filterEnv`
- [x] Update `filterEnv` to strip `GITHUB_TOKEN`-related vars if needed (or simplify — no env stripping needed for Copilot CLI since it uses its own auth)
- [x] Create `copilot.go` with `CopilotExecutor` struct: `Command` (string), `Args` ([]string), `CodingModel` (string), `ReviewModel` (string), `ErrorPatterns`/`LimitPatterns` ([]string), `OutputHandler` (func(string))
- [x] Implement `Run(ctx, prompt) Result` — invokes copilot with `CodingModel`
- [x] Implement `RunReview(ctx, prompt) Result` — invokes copilot with `ReviewModel`
- [x] Implement shared `run(ctx, prompt, model) Result` — builds command (`copilot --model <model> [args...] -p <prompt>`), streams JSONL line-by-line from stdout, parses each line, calls `OutputHandler` with extracted text, detects signals and error/limit patterns. Handle stderr separately for CLI-level errors (no JSONL on exit code 1)
- [x] Implement JSONL parsing logic (see `docs/copilot-jsonl-format.md` for full schema):
  - Parse each line as JSON, dispatch on `type` field
  - `assistant.message_delta`: extract `data.deltaContent`, pass to `OutputHandler` for real-time streaming
  - `assistant.message`: extract `data.content` as authoritative text (accumulate into Result.Output), scan for signals. Note: `data.content` may be empty string on tool-only turns
  - `result`: extract top-level `exitCode` (unique envelope — no `data` wrapper). Session is done
  - `tool.execution_start`: optionally log tool name for progress display
  - `tool.execution_complete`: check `data.success` and `data.error.message` for tool-level errors
  - Skip: `user.message`, `assistant.turn_start`, `assistant.turn_end`, `assistant.reasoning_delta`, `assistant.reasoning`, `session.info`
  - Tolerate JSON parse errors on final line (process may be killed mid-stream, truncating JSONL)
  - Detect abnormal termination: non-zero exit code + no `result` event received
- [x] Delete `codex.go` and `codex_test.go` entirely
- [x] Write table-driven tests for `CopilotExecutor` with mock CommandRunner: test streaming output, signal detection, error pattern matching, model switching between Run/RunReview, CLI error handling (no JSONL + stderr)
- [x] Write table-driven tests for JSONL parsing using fixtures from `pkg/executor/testdata/copilot_fixtures/`: `simple_text.jsonl` (Claude with tools), `simple_text_gpt.jsonl` (GPT text-only), `tool_use.jsonl` (multi-turn tool chain), `with_signal.jsonl` (signal passthrough)
- [x] Update `executor_test.go`: remove ClaudeExecutor tests, keep tests for shared types (detectSignal, matchPattern, filterEnv)
- [x] Run project test suite: `make test` — must pass before task 2

### Task 2: Replace config fields (claude_*/codex_* → copilot_*)

**Files:**
- Modify: `pkg/config/config.go`
- Modify: `pkg/config/values.go`
- Modify: `pkg/config/defaults/config`
- Modify: `pkg/config/frontmatter.go`
- Modify: `pkg/config/config_test.go` (if exists)
- Modify: `pkg/config/values_test.go` (if exists)
- Modify: `pkg/config/frontmatter_test.go` (if exists)

- [ ] In `config.go`: replace `ClaudeCommand`, `ClaudeArgs` with `CopilotCommand` (default: `"copilot"`), `CopilotArgs` (default: `"--allow-all --no-ask-user --output-format json"`)
- [ ] Replace `CodexEnabled`, `CodexCommand`, `CodexModel`, `CodexReasoningEffort`, `CodexTimeoutMs`, `CodexSandbox` with `CopilotCodingModel` (default: `"claude-opus-4-6"`), `CopilotReviewModel` (default: `"gpt-5.2-codex"`)
- [ ] Remove all `Codex*Set` tracking fields (`CodexEnabledSet`, `CodexTimeoutMsSet`)
- [ ] Replace `ClaudeErrorPatterns`, `CodexErrorPatterns` with unified `CopilotErrorPatterns` (default: `"Rate limit,quota exceeded,API Error"`)
- [ ] Replace `ClaudeLimitPatterns`, `CodexLimitPatterns` with unified `CopilotLimitPatterns` (default: `"Rate limit,quota exceeded"`)
- [ ] Update `ExternalReviewTool` default value from `"codex"` to `"copilot"` and valid values: `"copilot"`, `"custom"`, `"none"`
- [ ] Rename `CodexPrompt` field to `CopilotReviewPrompt` (or similar)
- [ ] In `values.go`: update INI field mappings — parse `copilot_command`, `copilot_args`, `copilot_coding_model`, `copilot_review_model`, `copilot_error_patterns`, `copilot_limit_patterns` instead of old field names
- [ ] In `defaults/config`: replace all commented `claude_*` and `codex_*` lines with new `copilot_*` equivalents
- [ ] In `frontmatter.go`: update `parseOptions()` and `Validate()` — accept full Copilot model IDs (`claude-opus-4-6`, `claude-sonnet-4-6`, `claude-haiku-4-5`, `gpt-5.2-codex`, etc.) instead of short keywords; remove short-keyword normalization
- [ ] Update all config tests: test new field parsing, default values, merge behavior with `*Set` flags
- [ ] Update frontmatter tests: test new model ID validation, rejection of old short keywords
- [ ] Run project test suite: `make test` — must pass before task 3

### Task 3: Wire CopilotExecutor into Runner and update prompts

**Files:**
- Modify: `pkg/processor/runner.go`
- Modify: `pkg/processor/prompts.go`
- Modify: `pkg/processor/runner_test.go` (if exists)
- Modify: `pkg/config/defaults/prompts/task.txt`
- Modify: `pkg/config/defaults/prompts/review_first.txt`
- Modify: `pkg/config/defaults/prompts/review_second.txt`
- Rename: `pkg/config/defaults/prompts/codex.txt` → `pkg/config/defaults/prompts/copilot_review.txt`
- Modify: `pkg/config/defaults/prompts/custom_eval.txt`
- Modify: `pkg/config/defaults/prompts/make_plan.txt`
- Modify: `pkg/config/defaults/prompts/finalize.txt`
- Modify: `pkg/config/defaults/agents/*.txt` (update model frontmatter if using short keywords)
- Modify: `pkg/config/config.go` (rename prompt file constant `codexPromptFile`)

- [ ] In `runner.go` `New()`: replace ClaudeExecutor creation with CopilotExecutor — set `Command`, `Args`, `CodingModel`, `ReviewModel`, `ErrorPatterns`, `LimitPatterns`, `OutputHandler` from config
- [ ] Remove CodexExecutor creation from `New()`
- [ ] Update `externalReviewTool()`: change `"codex"` references to `"copilot"`, keep backward compat for `codex_enabled = false` → `"none"` (or drop if full replacement)
- [ ] Update `needsCodexBinary()` → rename to `needsCopilotBinary()` or remove (copilot is always needed)
- [ ] Update external review loop dispatch: `"copilot"` case uses `CopilotExecutor.RunReview()`, `"custom"` case unchanged, `"none"` case unchanged
- [ ] Update `runWithLimitRetry()` if it references claude/codex-specific types
- [ ] Rename `codexPromptFile` constant in `config.go` to `copilotReviewPromptFile` and update references
- [ ] In all default prompt files: remove Claude Code-specific instructions (e.g., "You are running inside Claude Code", references to `claude` CLI); replace with Copilot CLI-appropriate language
- [ ] Rename `codex.txt` → `copilot_review.txt` (update embedded FS glob and file constant)
- [ ] Update agent frontmatter in `defaults/agents/*.txt` if any use short model keywords
- [ ] Update runner tests: mock CopilotExecutor instead of separate Claude/Codex executors
- [ ] Run project test suite: `make test` — must pass before task 4

### Task 4: Update CLI entry point and Docker wrapper

**Files:**
- Modify: `cmd/ralphex/main.go`
- Modify: `scripts/ralphex-dk.sh`
- Remove: `scripts/codex-as-claude.sh`
- Remove: `docs/bedrock-setup.md`
- Modify: `docs/custom-providers.md`

- [ ] In `main.go`: replace `claude` binary detection with `copilot` binary detection; update error message with Copilot CLI installation instructions
- [ ] Remove `codex` binary detection logic (CopilotExecutor handles both modes)
- [ ] Remove `ANTHROPIC_API_KEY` and `CLAUDECODE` env var stripping
- [ ] Update any CLI flag descriptions that reference "claude" or "codex" (e.g., `--codex-only` → consider renaming or keeping as alias)
- [ ] Delete `scripts/codex-as-claude.sh`
- [ ] In `scripts/ralphex-dk.sh`: remove all Bedrock-related functions (`get_claude_provider`, `build_bedrock_env_args`, `export_aws_profile_credentials`, `validate_bedrock_config`), remove `--claude-provider` flag handling
- [ ] In `scripts/ralphex-dk.sh`: replace macOS keychain credential extraction with `GITHUB_TOKEN` pass-through; remove `~/.claude` directory check; add `~/.copilot` directory mount
- [ ] Delete `docs/bedrock-setup.md`
- [ ] Rewrite `docs/custom-providers.md` for Copilot CLI context (or delete if no longer applicable — Copilot CLI IS the provider)
- [ ] Test Docker wrapper manually if possible (or verify script syntax with `bash -n`)
- [ ] Run project test suite: `make test` — must pass before task 5

### Task 5: Update documentation and project metadata

**Files:**
- Modify: `CLAUDE.md`
- Modify: `llms.txt`
- Modify: `README.md` (if it exists at project root)
- Modify: `docs/notifications.md` (if it references claude/codex)
- Modify: `docs/hg-support.md` (if it references claude/codex)

- [ ] Update `CLAUDE.md`: replace all references to Claude Code and Codex with Copilot CLI; update config field documentation, executor descriptions, build commands, e2e test instructions, key patterns section
- [ ] Update `llms.txt`: replace Claude Code/Codex references with Copilot CLI; update installation instructions, requirements, customization section, Docker section
- [ ] Update `docs/custom-providers.md` or remove (done partially in Task 4)
- [ ] Update `docs/hg-support.md` if it references `claude_command` or codex
- [ ] Update `docs/notifications.md` if it references claude/codex error patterns
- [ ] Grep entire codebase for remaining references to "claude" (as CLI tool, not model name), "codex" (as CLI tool), "anthropic_api_key", "bedrock" — fix any stragglers
- [ ] Run project test suite: `make test` — must pass before task 6

### Task 6: Verify acceptance criteria

- [ ] Run full test suite: `make test`
- [ ] Run linter: `make lint`
- [ ] Run formatter: `make fmt`
- [ ] Cross-compile Windows: `GOOS=windows GOARCH=amd64 go build ./...`
- [ ] Verify test coverage meets 80%+ for new code (`go test -cover ./pkg/executor/...`)
- [ ] Grep for leftover claude/codex CLI references: `grep -ri "claude_command\|codex_command\|ClaudeExecutor\|CodexExecutor" pkg/ cmd/`
- [ ] Verify embedded defaults load correctly: `go run ./cmd/ralphex --dump-defaults /tmp/copilot-defaults && ls /tmp/copilot-defaults/`
- [ ] Manual smoke test with toy project

### Task 7: Update documentation

- [ ] Update README.md if user-facing changes
- [ ] Update CLAUDE.md if internal patterns changed (covered in Task 5, verify completeness)
- [ ] Move this plan to `docs/plans/completed/`
