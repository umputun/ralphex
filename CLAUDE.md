# ralphex

Autonomous plan execution with Claude Code - Go rewrite of ralph.py.

## LLM Documentation

See @llms.txt for usage instructions and Claude Code integration commands.

## Build Commands

```bash
make build      # build binary to .bin/ralphex
make test       # run tests with coverage
make lint       # run golangci-lint
make fmt        # format code
```

### Updating Dependencies

`go get -u ./...` does NOT update dependencies behind build tags. The `e2e/` package uses `//go:build e2e`, so playwright-go and other e2e-only deps require a separate update:

```bash
go get -u ./...                                          # update main deps
go get -u -tags=e2e github.com/playwright-community/playwright-go  # update e2e deps
go mod tidy && go mod vendor                             # tidy and re-vendor
```

## Project Structure

```
cmd/ralphex/        # main entry point, CLI parsing
pkg/config/         # configuration loading, defaults, prompts, agents
pkg/executor/       # claude and codex CLI execution
pkg/git/            # git operations (external git CLI)
pkg/input/          # terminal input collector (fzf/fallback, draft review)
pkg/notify/         # notification delivery (telegram, email, slack, webhook, custom)
pkg/plan/           # plan file selection, parsing, and manipulation
pkg/processor/      # orchestration loop, prompts, signal helpers
pkg/progress/       # timestamped logging with color
pkg/status/         # shared execution model types: signals, phases, sections
pkg/web/            # web dashboard, SSE streaming, session management
e2e/                # playwright e2e tests for web dashboard
scripts/            # utility scripts organized by function
scripts/ralphex-dk/ # Docker wrapper script (Python) with tests
scripts/codex-as-claude/ # codex wrapper for Claude-compatible output
scripts/copilot-as-claude/ # GitHub Copilot CLI wrapper for Claude-compatible output
scripts/gemini-as-claude/ # gemini wrapper for Claude-compatible output
scripts/hg2git/     # Mercurial-to-git translation script with tests
scripts/opencode/   # opencode wrapper scripts with tests
scripts/internal/   # internal dev/CI scripts (prep-toy-test, init-docker, etc.)
docs/plans/         # plan files location
```

## Code Style

- Use jessevdk/go-flags for CLI parsing
- All comments lowercase except godoc
- Table-driven tests with testify
- 80%+ test coverage target
- Documentation: use `--flag=value` form for long CLI flags with values (not `--flag value`)

## Key Patterns

- Plan format: Checkboxes (`- [ ]` / `- [x]`) belong only in Task sections (`### Task N:` or `### Iteration N:`). The `Task` / `Iteration` keywords are structural tokens matched by `pkg/plan/parse.go` (`taskHeaderPattern`) and MUST stay in English even when plan content is written in another language — task titles and body text may be localized, but the section header keyword is fixed. Success criteria, Overview, and Context should not use checkboxes — they cause extra loop iterations. The task prompt handles them when present, but plan authors should avoid them.
- Plan file rename tolerance: two layers prevent the task phase looping when a plan file is renamed mid-run. (a) `make_plan.txt` does not ask the LLM to `git mv` the plan into `completed/` — the framework calls `MovePlanToCompleted` at end-of-run idempotently using `r.cfg.PlanFile`'s exact basename. (b) `resolvePlanFilePath` (`pkg/processor/prompts.go`) and `MovePlanToCompleted` (`pkg/git/service.go`) probe an alternate-date-format basename (`YYYY-MM-DD-<slug>` ↔ `YYYYMMDD-<slug>`) both alongside the original path (in-place rename) and under `completed/`; the in-place alternate is probed before the `completed/` paths so a current renamed file wins over a stale completed copy. `MovePlanToCompleted` also treats an alternate-named file in the original directory as the move source. `hasUncompletedTasks` (`pkg/processor/runner.go`) treats `fs.ErrNotExist` from `ParsePlanFile` as "no uncompleted tasks" rather than "assume incomplete".
- Signal-based completion detection (COMPLETED, FAILED, REVIEW_DONE signals) — constants in `pkg/status/`
- Plan creation signals: QUESTION (with JSON payload) and PLAN_READY
- Streaming output with timestamps
- Progress logging to files
- Progress file locking (flock) for active session detection
- Watch-mode dashboard reactivates completed sessions on fsnotify Write events, resuming tailing from `Session.lastOffset` — recovery path for the flock race in `RefreshStates` that can prematurely mark a running session completed. `Session.Reactivate()` is idempotent and scoped to the written path; `loadProgressFileIntoSession` records `lastOffset` after the initial load so reactivation does not re-emit replayed events
- Progress file fresh start: files ending in a `Completed:` footer are truncated on reuse; files ending in a `Failed:` footer (written by `Logger.SetFailed` before `Close`) or with no footer preserve content and write a `--- restarted at ... ---` separator, so retried failed/aborted runs keep history. `SetFailed` is called in `cmd/ralphex/main.go` for `r.Run` errors (including `ErrUserAborted`), dashboard start errors, and errors from `runWithWorktree`
- `--codex` is an executor switch (not a new pipeline mode): sets `cfg.Executor = config.ExecutorCodex` so task, both reviews, and finalize run through `CodexExecutor`(s) with `MultiAgent=true` (enables `features.multi_agent`, registers the `reviewer` agent for spawn_agent calls). Forces `cfg.ExternalReviewTool = "none"` (codex-reviewing-codex is weak-signal self-review). `--pass-claude-md` (codex executor only) sets `CodexExecutor.PassClaudeMd = true`. The `Mode` enum is unchanged; the `Executors` struct uses role-named fields (`Task`/`Review`/`External`/`Custom`), and `buildCodexExecutors` wires one codex instance into both `Task` and `Review` when the resolved review model/effort matches task, or two distinct instances when they differ. Review prompts are shared with claude — the `{{agent:<name>}}` expansion in `pkg/processor/prompts.go` reads `cfg.AppConfig.Executor` and emits `Use the Task tool` (claude) or `spawn_agent(agent='reviewer', task='...')` (codex). Codex config is passed as additive `-c` overrides per invocation by `(*CodexExecutor).configOverrides()` in `pkg/executor/codex.go`, layered on top of the user's `~/.codex/config.toml` so user customizations are preserved. ralphex never writes to `~/.codex/`; for user-level CLAUDE.md it prints a one-time hint to `ln -s ~/.claude/CLAUDE.md ~/.codex/AGENTS.md`
- Codex review-phase directives: `prependCodexReviewGuidance` (`pkg/processor/prompts.go`) injects a `=== Codex orchestration directives ===` block atop `ReviewFirstPrompt` and `ReviewSecondPrompt` when `cfg.isCodexExecutor()` is true (no-op for claude). Covers two codex multi_agent quirks: (a) spawn_agent must pass only `agent` and `task` — `fork_context=true` with explicit `agent_type` is rejected by the codex API; (b) on a `wait_agent` timeout for a sub-agent that died mid-tool-call, re-spawn that agent ONCE then proceed with partial results. Section-level injection works for embedded and customized review prompts alike. Wired at three call sites in `pkg/processor/runner.go`: first review (full pipeline + review-only mode) and the review-second loop.
- Codex task-phase skill-conflict directive: `prependCodexTaskGuidance` (`pkg/processor/prompts.go`) injects the `=== Codex task-execution directives ===` block (`codexTaskGuidance`) atop `TaskPrompt` when `cfg.isCodexExecutor()` is true (no-op for claude). Wired at one call site — `runTaskPhase` in `pkg/processor/runner.go`. It tells codex that ralphex's task prompt is authoritative and a conflicting auto-activated skill from `~/.codex/skills/` must not be followed. Deliberately generic (names no specific skill); a soft prompt-level mitigation, not a hard guard — codex 0.133.0 has no per-invocation skill-disable flag. Task-phase only
- Codex output streaming: codex has no `stream-json` equivalent, so assistant message text + tool dispatch land only in the session rollout file at `~/.codex/sessions/<y>/<m>/<d>/rollout-<ts>-<session-id>.jsonl`. `CodexExecutor.Run` extracts the session id from the stderr header banner (`extractSessionID` + buffered `sessionIDCh`) and spawns `tailRolloutFile` to follow it. `formatRolloutEvent` forwards only assistant message text — reasoning records are covered by the stderr bold-summary stream, `function_call` records are skipped as tool-machinery noise. `tailCtx` is canceled after stdout EOF so the tailer drains once more and exits
- Codex stderr filtering: `shouldDisplay` (`pkg/executor/codex.go`) suppresses the per-iteration startup banner, but on the executor's first `Run()` call (`headerEmitted atomic.Bool`) whitelists three header lines — `model:`, `sandbox:`, `reasoning effort:` — so users see what codex resolved from `~/.codex/config.toml`. Bold reasoning summaries always flow through. The ralphex-side banner (`printExecutorInfo`, `cmd/ralphex/main.go`) emits `sandbox:` (and `model:` / `reasoning effort:` when `codex_model` / `codex_reasoning_effort` are set; empty values skipped)
- `--plan-model`/`--task-model`/`--review-model` resolve per-phase model/effort. `plan_model` falls back to `task_model`; `review_model` falls back to `task_model`. Claude mode injects `--model`/`--effort` into `claude_command`. Codex mode: `ResolveCodexModelEffort` (`pkg/processor/runner.go`) resolves the `model[:effort]` spec against `codex_model`/`codex_reasoning_effort` defaults; `buildCodexExecutors` builds a separate review `CodexExecutor` when review differs from task. `max` effort does not exist in codex — kept default, `maxDropped` reported, `codexModelBanner` / `codexPlanBanner` (`cmd/ralphex/main.go`) warns

### Finalize Step

Optional post-completion step after successful review phases. Triggers on `ModeFull`, `ModeReview`, `ModeCodexOnly`. Disabled by default (`finalize_enabled`). Runs once, no signal loop — best effort (failures logged, don't block success). Default behavior when enabled: rebase commits onto default branch, optionally squash, run tests.

Key files:
- `pkg/processor/runner.go` - `runFinalize()` method called at end of review modes
- `pkg/config/defaults/prompts/finalize.txt` - default finalize prompt

### Custom External Review

Custom scripts instead of codex for external review (`external_review_tool = custom`, `custom_review_script`). Script gets the prompt file path as its single arg, outputs findings to stdout for Claude to evaluate.

- `{{DIFF_INSTRUCTION}}` expands per iteration: first `git diff main...HEAD`, subsequent `git diff` (uncommitted only)
- `max_external_iterations` 0 = auto, `max(3, max_iterations/5)`
- `review_patience` stalemate detection: terminates after N consecutive no-commit rounds (0 = disabled)
- `session_timeout`/`idle_timeout` (see Configuration): in default Claude mode neither applies to external codex/custom review; under `--codex` `session_timeout` covers every executor call
- Manual break: Ctrl+\ pauses task phase (fresh session re-reads plan on resume), terminates external review immediately. Break channel is repeatable (send-on-channel, not close-once); `SetPauseHandler()` sets the task pause callback. Not on Windows
- `codex_enabled = false` backward compat: treated as `external_review_tool = none`

Key files:
- `pkg/executor/custom.go` - CustomExecutor for running external scripts
- `pkg/config/defaults/prompts/codex_review.txt` / `custom_review.txt` / `custom_eval.txt` - external review prompts
- `pkg/processor/prompts.go` - `getDiffInstruction()`, `buildPreviousContext()`, `replaceVariablesWithIteration()`
- `pkg/processor/runner.go` - dispatch logic in external review loop

### Alternative Providers for Claude Phases

`claude_command`/`claude_args` replace Claude Code with any `stream-json`-compatible CLI. Included wrappers: `scripts/codex-as-claude/codex-as-claude.sh`, `scripts/copilot-as-claude/copilot-as-claude.sh`. Wrappers must ignore unknown flags gracefully (`*) shift ;;`) — default Claude flags may still be passed via config fallback. See `docs/custom-providers.md`.

Env vars:
- Codex: `CODEX_MODEL`, `CODEX_SANDBOX`, `CODEX_VERBOSE`
- Copilot: `COPILOT_MODEL`, `COPILOT_GITHUB_TOKEN`, `GH_TOKEN`, `GITHUB_TOKEN`
Copilot wrapper: native autopilot mode — `--autopilot --no-ask-user --allow-all` for task/review, `--autopilot --allow-all` for plan runs (so `QUESTION` signals surface).

### AWS Bedrock Provider (Docker Wrapper Only)

`scripts/ralphex-dk.sh` supports AWS Bedrock as a Claude provider (`--claude-provider bedrock` / `RALPHEX_CLAUDE_PROVIDER`). See `docs/bedrock-setup.md`.

Key functions in `scripts/ralphex-dk.sh`:
- `get_claude_provider()` - returns provider from CLI flag or env var
- `build_bedrock_env_args()` - builds docker -e flags for BEDROCK_ENV_VARS
- `export_aws_profile_credentials()` - exports credentials from AWS profile
- `validate_bedrock_config()` - validates bedrock config, returns warnings

### Docker Socket Support (Docker Wrapper Only)

`--docker` flag (or `RALPHEX_DOCKER_SOCKET`) mounts the host Docker socket for testcontainers. Socket path from `DOCKER_HOST` (unix://) or `/var/run/docker.sock`; GID auto-detected and passed via `DOCKER_GID`. Missing socket = fail-fast error.

Key functions in `scripts/ralphex-dk.sh`:
- `is_docker_enabled()` - checks CLI flag and `RALPHEX_DOCKER_SOCKET` env var
- `resolve_docker_socket()` - resolves socket path from `DOCKER_HOST` or default
- `get_docker_socket_gid()` - detects socket file GID via `os.stat()`

### Docker Network Mode (Docker Wrapper Only)

`--network MODE` flag (or `RALPHEX_DOCKER_NETWORK`) passes `--network <value>` to `docker run` — lets the container reach docker-compose services on localhost.

### Git Package API

Single public entry point: `git.NewService(path, logger, vcsCmd...) (*Service, error)`
- All git operations are methods on `Service` (CreateBranchForPlan, CreateWorktreeForPlan, MovePlanToCompleted, EnsureLocalGitignore, etc.)
- `Logger` interface for dependency injection, compatible with `*color.Color`
- Uses `backend` interface internally, implemented by `externalBackend` which shells out to the configured VCS command
- Optional `vcsCmd` parameter overrides the default `"git"` command (e.g., path to `hg2git.sh` translation script)

Key files:
- `pkg/git/service.go` - `Service` type, `backend` interface
- `pkg/git/external.go` - VCS CLI backend (`externalBackend` type)

### Worktree Isolation Mode

`--worktree` flag or `use_worktree = true` config option runs each plan in an isolated git worktree, enabling parallel execution of multiple plans on the same repo. `--branch` flag overrides the branch name derived from the plan filename (useful when auto-detection is fragile, e.g. generic filenames or spec-driven layouts).

- Worktrees created at `.ralphex/worktrees/<branch-name>` inside main repo
- Progress logger created before chdir so files land in main repo's `.ralphex/progress/`
- `MainGitSvc` in `executePlanRequest` handles cross-boundary ops (plan file moves in main repo)
- Worktree auto-removed on completion, failure, or SIGINT; branch preserved for PR
- Only active for `ModeFull` and `ModeTasksOnly` (review/plan/external modes skip worktree)
- `runWithWorktree()` in `cmd/ralphex/main.go` encapsulates the full lifecycle
- Case-insensitive path handling: `CreateBranchForPlan()`, `CreateWorktreeForPlan()`, and `CommitPlanFile()` resolve plan file paths to actual on-disk case via `resolveFilesystemCase()` to handle macOS APFS case-insensitive filesystems. `hasChangesOtherThan()` uses case-insensitive comparison for plan file exclusion

Key files:
- `cmd/ralphex/main.go` - `runWithWorktree()`, `selectAndExecutePlan()`, interrupt cleanup
- `pkg/git/service.go` - `CreateWorktreeForPlan()`, `CommitPlanFile()`, `RemoveWorktree()`, `resolveFilesystemCase()`
- `pkg/git/external.go` - `addWorktree()`, `removeWorktree()`, `pruneWorktrees()` (unexported backend methods)

### Plan Creation Mode

The `--plan "description"` flag enables interactive plan creation:

- Claude explores codebase and asks clarifying questions
- Questions use QUESTION signal with JSON: `{"question": "...", "options": [...]}`
- User answers via fzf picker (or numbered fallback); an "Other" option allows typing a custom answer
- Q&A history stored in progress file for context
- When ready, Claude emits PLAN_DRAFT signal with full plan content for user review
- User can Accept, Revise (with feedback), Interactive review, or Reject the draft
- Interactive review opens `$EDITOR` with the plan content; on save, a unified diff is computed and fed back as revision feedback
- If revised (manually or via interactive review), feedback is passed to Claude for plan modifications
- Loop continues until user accepts and Claude emits PLAN_READY signal
- Plan file written to docs/plans/
- After completion, prompts user: "Continue with plan implementation?"
- If "Yes", creates branch and runs full execution mode on the new plan

Plan creation signals:
- `QUESTION` - asks user a question with options (JSON payload)
- `PLAN_DRAFT` - presents plan draft for review (plan content between markers)
- `PLAN_READY` - indicates plan file was written successfully

Key files:
- `pkg/input/input.go` - terminal input collector (fzf/fallback, draft review)
- `pkg/status/status.go` - shared signal constants (COMPLETED, FAILED, REVIEW_DONE, etc.)
- `pkg/processor/signals.go` - signal detection helpers (isReviewDone, isCodexDone, etc.)
- `pkg/config/defaults/prompts/make_plan.txt` - plan creation prompt

## Platform Support

- **Linux/macOS:** fully supported
- **Windows:** builds and runs, but with limitations:
  - Process group signals not available (graceful shutdown kills direct process only, not child processes)
  - File locking not available (active session detection disabled)
  - Prompts are passed to the claude CLI via stdin (not `-p` flag) to avoid the cmd.exe 8191-character command-line limit

### Cross-Platform Development

When adding platform-specific code (syscalls, signals, file locking):
1. Use build tags: `//go:build !windows` for Unix-only code, `//go:build windows` for Windows stubs
2. Create separate files: `foo_unix.go` and `foo_windows.go`
3. Keep common code in the main file, extract platform-specific functions
4. Windows stubs can be no-ops where functionality is optional

Example files:
- `pkg/executor/procgroup_unix.go` / `procgroup_windows.go` - process group management
- `pkg/progress/flock_unix.go` / `flock_windows.go` - file locking helpers

Cross-compile to verify Windows builds:
```bash
GOOS=windows GOARCH=amd64 go build ./...
```

## Configuration

- Global config location: `~/.config/ralphex/` (override with `--config-dir` or `RALPHEX_CONFIG_DIR`)
- Local config location: `.ralphex/` (per-project, optional)
- Config file format: INI (using gopkg.in/ini.v1)
- Embedded defaults in `pkg/config/defaults/`
- Precedence: CLI flags > local config > global config > embedded defaults
- Custom prompts: `~/.config/ralphex/prompts/*.txt` or `.ralphex/prompts/*.txt`
- Custom agents: `~/.config/ralphex/agents/*.txt` or `.ralphex/agents/*.txt`
- `plan_model` / `task_model` / `review_model` config options: `model[:effort]` for plan creation / task / review phases; `plan_model` and `review_model` fall back to `task_model`. CLI flags `--plan-model`/`--task-model`/`--review-model` take precedence. Parsed by `ParseModelEffort` (pkg/processor/runner.go). See the Key Patterns bullet for claude- vs codex-executor behavior. Disabled by default (empty = Claude CLI defaults)
- `default_branch` config option: override auto-detected default branch for review diffs
- `max_iterations` config option: override CLI default (50) for maximum task iterations per plan (CLI flag `--max-iterations` takes precedence)
- `vcs_command` config option: override the VCS binary used by the git backend (default: `"git"`). Set to a translation script path (e.g., `scripts/hg2git/hg2git.sh`) to use ralphex with Mercurial repos. See `docs/hg-support.md`
- `commit_trailer` config option: trailer line appended to all ralphex-orchestrated git commits (both Go-code commits and LLM-prompted commits). When set, the trailer is appended after a blank line at the end of every commit message. Example: `commit_trailer = Co-authored-by: ralphex <noreply@ralphex.com>`. Disabled by default (empty)
- Notification config: `notify_channels`, `notify_on_error`, `notify_on_complete`, `notify_timeout_ms`, plus channel-specific `notify_*` fields (see `docs/notifications.md`)
- `review_patience` config option: terminate external review after N consecutive unchanged rounds (0 = disabled). CLI flag `--review-patience` takes precedence
- `wait_on_limit` config option: duration to wait before retrying on rate limit (e.g., "1h", "30m"). CLI flag `--wait` takes precedence. Disabled by default
- `session_timeout` config option: per-session timeout (e.g., "30m"). Applies to claude in default mode and to every executor call under `--codex` (task/review/finalize/eval); external codex/custom review in Claude mode is not affected. Kills hanging sessions, continues to next iteration. Applied in `runWithSessionTimeout` via `context.WithTimeout`, gated on `Executor==ExecutorCodex || toolName=="claude"`. CLI flag `--session-timeout` takes precedence. Disabled by default
- `idle_timeout` config option: kills claude/codex executor sessions when no output for a given duration (e.g., "5m"). Resets on each output line; only fires when the session goes silent. Implemented in `ClaudeExecutor.Run()`/`CodexExecutor.Run()` via `time.AfterFunc`. Wired by `buildCodexExecutor` for first-class `--codex`; NOT by `buildExternalCodexExecutor`, so external codex review in default-claude mode has no idle timeout. Custom external review unaffected. CLI flag `--idle-timeout` takes precedence. Disabled by default
- `move_plan_on_completion` config option: controls whether completed plans move to `docs/plans/completed/` on success. Default `true`. Disable for workflows that manage plan lifecycle externally (spec-driven tooling with separate archive steps)
- `preserve_anthropic_api_key` config option / `--preserve-anthropic-api-key` CLI flag: when true, `ANTHROPIC_API_KEY` is passed through to the child claude process (needed for API-key auth rather than OAuth/keychain). Default `false` strips the key. The merge sentinel `PreserveAnthropicAPIKeySet` lives only on `Values` (load-bearing for local-overrides-global merge); `Config` carries the resolved bool. Plumbed: `Config.PreserveAnthropicAPIKey` → `pkg/processor/runner.go` → `ClaudeExecutor.PreserveAPIKey` → `execClaudeRunner.preserveAPIKey` → `claudeChildEnv()` (`pkg/executor/executor.go`). When enabled, the startup banner emits `auth: ANTHROPIC_API_KEY passthrough enabled`. `CLAUDECODE` is always stripped regardless (prevents nested-session errors)

### Local Project Config (.ralphex/)

Projects can have local configuration that overrides global settings. Run `ralphex --init` to create the `.ralphex/` directory with commented-out defaults:

```
project/
├── .ralphex/           # optional, project-local config (created by --init)
│   ├── config          # overrides specific settings (per-field merge)
│   ├── prompts/        # per-file fallback: local → global → embedded
│   │   └── task.txt    # only override task prompt
│   └── agents/         # per-file fallback: local → global → embedded
│       └── custom.txt  # project-specific agent
```

**Merge strategy:**
- **Config file**: per-field override (local values override global, missing fields fall back)
- **Prompts**: per-file fallback (local → global → embedded for each prompt file)
- **Agents**: per-file fallback (local → global → embedded for each agent file, same as prompts)

### Config Defaults Behavior

- **Commented templates**: config file, prompts, and agents are installed with all content commented out (prefixed `# `)
- **Auto-update**: files with only comments/whitespace are safe to overwrite on updates - users get new defaults automatically
- **User customization**: uncommenting any line marks the file as customized - it will be preserved and never overwritten
- **Fallback loading**: when loading config/prompts/agents, if file content is all-commented (no actual values), embedded defaults are used
- **Comment handling**: leading meta-comment block (2+ contiguous `# ...` lines at top of file) is stripped when loading prompts and embedded defaults; a single `# Title` at the top is preserved (treated as markdown header, not meta-comment). Full `stripComments` is only used for emptiness detection to trigger fallback
- **scalars/colors**: per-field fallback to embedded defaults if missing
- `*Set` flags (e.g., `CodexEnabledSet`) distinguish explicit `false`/`0` from "not set"

### Error Pattern Detection

Configurable patterns detect rate limit and quota errors in claude/codex output:
- `claude_error_patterns` / `codex_error_patterns`: comma-separated error patterns (default strings in `llms.txt` and the embedded config). Codex phrases are tightened so review findings that *talk about* rate limiting do not trip a false positive
- Matching is case-insensitive substring search
- Whitespace is trimmed from each pattern
- For claude: patterns checked against the last 10 text blocks (not full output) to avoid false positives when analysis text mentions rate limit phrases. Context cancellation paths bypass pattern checks
- For codex: patterns checked against stdout AND a live per-line scan of stderr. Stderr scanning runs inside `processStderr` before the 5-line / 256-rune tail truncation, so detection is eviction- and truncation-resistant. The scan is gated by `isCodexErrorLine` (matches `error:`/`fatal:`/`panic:` prefix, case-insensitive) so progress chatter cannot trigger false positives. The first matching limit/error pattern per category is recorded on `stderrResult.{limitMatch,errorMatch}` and consumed by `CodexExecutor.checkPatterns`. Priority is limit-class first across both sources: `stdout limit → stderr limit → stdout error → stderr error` (within a class, stdout wins). Patterns are evaluated only when the process exits non-zero and context is not canceled. Stderr is scanned because OpenAI/ChatGPT plan-quota errors are emitted on stderr while stdout is empty on failure
- For custom executors: stderr is merged into stdout by the executor itself (`cmd.Stderr = cmd.Stdout`), so the same pattern check covers both streams. Patterns checked only when process exits non-zero and context is not canceled
- On match, ralphex exits gracefully with pattern info and help command suggestion

Limit patterns for wait+retry behavior:
- `claude_limit_patterns` / `codex_limit_patterns`: comma-separated limit patterns (default strings in `llms.txt` and the embedded config)
- `wait_on_limit`: duration string (e.g., "1h", "30m"), disabled by default
- `--wait` CLI flag overrides `wait_on_limit` config
- Priority: limit patterns checked first; if match AND wait > 0, wait and retry; if match AND wait == 0, fall through to error pattern behavior
- Limit patterns intentionally overlap with error patterns — `wait_on_limit` acts as the toggle

Implementation:
- `PatternMatchError` type in `pkg/executor/executor.go` with `Pattern` and `HelpCmd` fields
- `LimitPatternError` type in `pkg/executor/executor.go` with `Pattern` and `HelpCmd` fields
- `matchPattern()` helper for case-insensitive matching (used by both error and limit pattern checks)
- Patterns passed via `ClaudeExecutor.ErrorPatterns`/`LimitPatterns` and `CodexExecutor.ErrorPatterns`/`LimitPatterns`
- `runWithLimitRetry()` in `pkg/processor/runner.go` wraps executor calls with retry logic

### Agent System

5 default agents are installed on first run to `~/.config/ralphex/agents/` as commented-out templates:
- `implementation.txt` - verifies code achieves stated goals
- `quality.txt` - reviews for bugs, security issues, race conditions
- `documentation.txt` - checks if docs need updates
- `simplification.txt` - detects over-engineering
- `testing.txt` - reviews test coverage and quality

**Loading behavior:** agents are loaded with per-file fallback: local `.ralphex/agents/` → global `~/.config/ralphex/agents/` → embedded default. The 5 embedded agents are always the baseline — deleting an agent file from disk does not disable it, the embedded version is used as fallback. To disable a specific agent, remove its `{{agent:name}}` reference from the prompt files (`review_first.txt`, `review_second.txt`), not the agent file itself.

**Frontmatter options:** Agent files support optional YAML frontmatter (`---` delimited) for per-agent model and subagent type:
- `model: haiku|sonnet|opus` — Claude model for this agent
- `agent: <type>` — Claude Code Task tool subagent type (default: `general-purpose`)
- Parsed by `parseOptions()` in `pkg/config/frontmatter.go`, validated by `Options.Validate()`
- Full model IDs (e.g. `claude-sonnet-4-5-20250929`) are normalized to short keywords (`sonnet`)
- Invalid model values are dropped with a warning, falling back to defaults

**Template variables:** Prompt files support variable expansion via `replacePromptVariables()` in `pkg/processor/prompts.go`:
- `{{PLAN_FILE}}` - path to plan file or fallback text
- `{{PROGRESS_FILE}}` - path to progress log or fallback text
- `{{GOAL}}` - human-readable goal (plan-based or branch comparison)
- `{{DEFAULT_BRANCH}}` - detected default branch (main, master, origin/main, etc.), overridable via `--base-ref` CLI flag or `default_branch` config option
- `{{DIFF_INSTRUCTION}}` - git diff command for current iteration (first: `git diff main...HEAD`, subsequent: `git diff`)
- `{{PREVIOUS_REVIEW_CONTEXT}}` - previous review context block for external review iterations (empty on first iteration, formatted context on subsequent)
- `{{agent:name}}` - expands to Task tool instructions for the named agent

Variables are also expanded inside agent content, so custom agents can use `{{DEFAULT_BRANCH}}` etc.

**Customization:**
- Edit files in `~/.config/ralphex/agents/` to modify agent prompts
- Add new `.txt` files to create custom agents
- Run `ralphex --init` to create local `.ralphex/` project config with commented-out defaults
- Run `ralphex --reset` to interactively restore defaults, or delete ALL `.txt` files manually
- Run `ralphex --dump-defaults <dir>` to extract raw embedded defaults for comparison or merging
- Use `/ralphex-update` skill for smart merging of updated defaults into customized configs
- Use `/ralphex-adopt` skill to convert plans from other formats (OpenSpec, spec-kit, GitHub/GitLab issues, task-lists, free-form markdown) into ralphex format
- Alternatively, reference agents installed in your Claude Code directly in prompt files (like `qa-expert`, `go-smells-expert`)

## Testing

```bash
go test ./...           # run all tests
go test -cover ./...    # with coverage
```

### Web UI E2E Tests

Playwright-based e2e tests for the web dashboard are in `e2e/` directory:

```bash
# install playwright browsers (first time only)
go run github.com/playwright-community/playwright-go/cmd/playwright@latest install --with-deps chromium

# run web ui e2e tests
go test -tags=e2e -timeout=10m -count=1 -v ./e2e/...

# run with visible browser (for debugging)
E2E_HEADLESS=false go test -tags=e2e -timeout=10m -count=1 -v ./e2e/...
```

Tests cover: dashboard loading, SSE connection and reconnection, phase sections, plan panel, session sidebar, keyboard shortcuts, error/warning event rendering, signal events (COMPLETED/FAILED/REVIEW_DONE), task and iteration boundary rendering, auto-scroll behavior, plan parsing edge cases.

## End-to-End Testing

Unit tests mock external calls. After ANY code changes, ask the user before running an e2e test with a toy project because it can take time and consume claude/codex credits. Run it only after explicit approval to verify actual claude/codex integration and output streaming.

### Create Toy Project

```bash
./scripts/internal/prep-toy-test.sh
```

This creates `/tmp/ralphex-test` with a buggy Go file and a plan to fix it.

### Test Full Mode

```bash
cd /tmp/ralphex-test
.bin/ralphex docs/plans/fix-issues.md
```

**Expected behavior:**
1. Creates branch `fix-issues`
2. Phase 1: executes Task 1, then Task 2
3. Phase 2: first Claude review
4. Phase 2.5: codex external review
5. Phase 3: second Claude review
6. Moves plan to `docs/plans/completed/`

### Test Review-Only Mode

```bash
cd /tmp/ralphex-test
git checkout -b feature-test

# make some changes
echo "// comment" >> main.go
git add -A && git commit -m "add comment"

# run review-only (no plan needed)
go run <ralphex-project-root>/cmd/ralphex --review
```

### Test Codex-Only Mode

```bash
cd /tmp/ralphex-test

# run codex-only review
go run <ralphex-project-root>/cmd/ralphex --codex-only
```

### Monitor Progress

```bash
# live stream (use actual filename from ralphex output)
tail -f .ralphex/progress/progress-fix-issues.txt

# recent activity
tail -50 .ralphex/progress/progress-*.txt
```

## Development Workflow

**CRITICAL: After ANY code changes to ralphex:**

1. Run unit tests: `make test`
2. Run linter: `make lint`
3. **MUST** ask the user before running the toy end-to-end test (see above); run it only after explicit approval
4. Monitor `tail -f .ralphex/progress/progress-*.txt` to verify output streaming works

Unit tests don't verify actual codex/claude integration or output formatting. The toy project test is the only way to verify streaming output works correctly.

## Before Submitting a PR

If you're an AI agent preparing a contribution, complete this checklist:

**Code Quality:**
- [ ] Run `make test` - all tests must pass
- [ ] Run `make lint` - fix all linter issues
- [ ] Run `make fmt` - code is properly formatted
- [ ] New code has tests with 80%+ coverage

**Project Patterns:**
- [ ] Studied existing code to understand project conventions
- [ ] One `_test.go` file per source file (not `foo_something_test.go`)
- [ ] Tests use table-driven pattern with testify
- [ ] Test helper functions call `t.Helper()`
- [ ] Mocks generated with moq, stored in `mocks/` subdirectory
- [ ] Interfaces defined at consumer side, not provider
- [ ] Context as first parameter for blocking/cancellable methods
- [ ] Private struct fields for internal state, accessor methods if needed
- [ ] Regex patterns compiled once at package level
- [ ] Deferred cleanup for resources (files, contexts, connections)
- [ ] No new dependencies unless directly needed - avoid accidental additions

**PR Scope:**
- [ ] Changes are focused on the requested feature/fix only
- [ ] No "general improvements" to unrelated code
- [ ] PR is reasonably sized for human review
- [ ] Large changes split into logical, focused PRs

**Self-Review:**
- [ ] Can explain every line of code if asked
- [ ] Checked for security issues (injection, secrets exposure, etc.)
- [ ] Commit messages describe "why", not just "what"

## Documentation Site (Zensical)

- Site source: `site/` directory with `mkdocs.yml` (read natively by Zensical)
- Builder: `zensical` (replaced mkdocs-material; `requirements.txt` lists only `zensical`)
- **Landing page**: `site/docs/index.html` is a manually crafted HTML page, not generated by the SSG. Edit it directly to update the landing page.
- Template overrides: `site/overrides/` with `custom_dir: overrides` in mkdocs.yml
- **Python version**: Zensical requires Python ≥ 3.10. Local builds use a venv at `site/.venv/` (auto-created by `make prep_site`); Cloudflare Pages requires `PYTHON_VERSION` env var ≥ 3.10
- **Brand color**: dark-mode palette uses Material's `teal` keyword, then `site/docs/stylesheets/extra.css` overrides `--md-primary-fg-color` / `--md-accent-fg-color` to `#2dd4bf` (Tailwind teal-400) so the docs match the landing page brand color
- **Raw .md files**: SSG renders ALL `.md` files in `docs_dir` as HTML pages. To serve raw markdown (e.g., `assets/claude/*.md` for Claude Code skills), copy them AFTER `zensical build` - see `prep_site` target in Makefile

## Testing Safety Rules

- **CRITICAL: Tests must NEVER touch real user config directory** (`~/.config/ralphex/`)
- All tests MUST use `t.TempDir()` for any file operations
- Config pollution is hard to debug - corrupted files cause cryptic errors
- Verify tests are clean: compare MD5 checksums of config files before/after `go test ./...`

## Workflow Rules

- **Plugin version**: bump `.claude-plugin/plugin.json` and `.claude-plugin/marketplace.json` versions on release if skill files (`assets/claude/`) changed since last plugin version bump
- **CHANGELOG**: Never modify during development - updates are part of release process only
- **Version sections**: Never add entries to existing version sections - versions are immutable once released
- **Linter warnings**: Add exclusions to `.golangci.yml` instead of `_, _ =` prefixes for fmt.Fprintf/Fprintln
- **Exporting functions**: When changing visibility (lowercase to uppercase), check ALL callers including test files
- **Completed plans are immutable**: Plans in `docs/plans/completed/` represent historical record of changes. Never modify completed plans. If further changes are needed (refactoring, fixes, etc.), create a new plan
