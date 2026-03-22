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

## Key Patterns

- Plan format: Checkboxes (`- [ ]` / `- [x]`) belong only in Task sections (`### Task N:` or `### Iteration N:`). Success criteria, Overview, and Context should not use checkboxes — they cause extra loop iterations. The task prompt handles them when present, but plan authors should avoid them.
- Signal-based completion detection (COMPLETED, FAILED, REVIEW_DONE signals) — constants in `pkg/status/`
- Plan creation signals: QUESTION (with JSON payload) and PLAN_READY
- Streaming output with timestamps
- Progress logging to files
- Progress file locking (flock) for active session detection
- Progress file fresh start: completed files (with `Completed:` footer) are truncated on reuse instead of appending
- Multiple execution modes: full, tasks-only, review-only, external-only/codex-only, plan creation
- `--base-ref` flag overrides default branch for review diffs (branch name or commit hash)
- `--skip-finalize` flag disables finalize step for a single run
- `--wait` flag enables rate limit retry with specified duration (e.g., `--wait 1h`)
- `--session-timeout` flag sets per-session timeout for claude (e.g., `--session-timeout 30m`), kills hanging sessions
- `--review-patience` flag terminates external review after N unchanged rounds (stalemate detection)
- Manual break via SIGQUIT (Ctrl+\) during external review loop terminates it early via injected channel
- Custom external review support via scripts (wraps any AI tool)
- Configuration via `~/.config/ralphex/` with embedded defaults
- File watching for multi-session dashboard using fsnotify
- Optional finalize step after successful reviews (disabled by default)
- Optional notifications on completion/failure via Telegram, Email, Slack, Webhook, or custom script (best-effort, disabled by default)

### Finalize Step

Optional post-completion step that runs after successful review phases:

- Triggers on: ModeFull, ModeReview, ModeCodexOnly (modes with review pipeline)
- Disabled by default (`finalize_enabled = false` in config)
- Uses task color (green) for output
- Runs once, no signal loop - best effort (failures logged but don't block success)
- Template variables supported (`{{DEFAULT_BRANCH}}`, etc.)

Default behavior (when enabled): rebases commits onto default branch, optionally squashes related commits, runs tests to verify.

Config option: `finalize_enabled = true` in `~/.config/ralphex/config` or `.ralphex/config`
CLI override: `--skip-finalize` disables finalize for a single run even if enabled in config
Prompt file: `~/.config/ralphex/prompts/finalize.txt` or `.ralphex/prompts/finalize.txt`

Key files:
- `pkg/processor/runner.go` - `runFinalize()` method called at end of review modes
- `pkg/config/defaults/prompts/finalize.txt` - default finalize prompt

### Custom External Review

Allows using custom scripts instead of codex for external code review:

- Config: `external_review_tool = custom` and `custom_review_script = /path/to/script.sh`
- Script receives prompt file path as single argument
- Script outputs findings to stdout (ralphex passes them to Claude for evaluation)
- `{{DIFF_INSTRUCTION}}` template variable expands based on iteration:
  - First iteration: `git diff main...HEAD` (all feature branch changes)
  - Subsequent iterations: `git diff` (uncommitted changes only)
- `--external-only` (-e) flag runs only external review; `--codex-only` (-c) is deprecated alias
- `max_external_iterations` config / `--max-external-iterations` CLI flag overrides external review loop limit (0 = auto, derived as `max(3, max_iterations/5)`)
- `review_patience` config / `--review-patience` CLI flag enables stalemate detection: tracks consecutive rounds with no commits, terminates early when threshold reached (0 = disabled)
- `session_timeout` config / `--session-timeout` CLI flag sets per-session timeout for claude (e.g., `30m`, `1h`). When a claude session exceeds the timeout, it is killed and the phase loop continues to the next iteration. Applied in `runWithLimitRetry` via `context.WithTimeout`. Claude-only; codex and custom executors are not affected. Disabled by default (empty/0)
- Manual break: pressing Ctrl+\ (SIGQUIT) during external review terminates the loop immediately via context cancellation. Break channel injected from `cmd/ralphex/` into Runner via `SetBreakCh()`. Not available on Windows
- `codex_enabled = false` backward compat: treated as `external_review_tool = none`

Key files:
- `pkg/executor/custom.go` - CustomExecutor for running external scripts
- `pkg/config/defaults/prompts/codex_review.txt` - prompt sent to codex external review tool
- `pkg/config/defaults/prompts/custom_review.txt` - prompt sent to custom tool
- `pkg/config/defaults/prompts/custom_eval.txt` - prompt for claude to evaluate custom tool output
- `pkg/processor/prompts.go` - `getDiffInstruction()`, `buildPreviousContext()`, and `replaceVariablesWithIteration()`
- `pkg/processor/runner.go` - dispatch logic in external review loop

### Alternative Providers for Claude Phases

`claude_command` and `claude_args` config options allow replacing Claude Code with any CLI that produces compatible `stream-json` output. A codex wrapper script is included at `scripts/codex-as-claude/codex-as-claude.sh`.

Config: `claude_command = /path/to/codex-as-claude.sh` and optionally `claude_args =` (empty).
Note: default Claude flags may still be passed due to config fallback; wrappers should ignore unknown flags gracefully (the included script does this via `*) shift ;;`).
Env vars: `CODEX_MODEL`, `CODEX_SANDBOX`, `CODEX_VERBOSE` (set to 1 for command output).
Documentation: `docs/custom-providers.md`

### AWS Bedrock Provider (Docker Wrapper Only)

The Docker wrapper script (`scripts/ralphex-dk.sh`) supports AWS Bedrock as an alternative Claude provider:

- Config: `--claude-provider bedrock` CLI flag or `RALPHEX_CLAUDE_PROVIDER=bedrock` env var
- Requires: `AWS_REGION`, and either `AWS_PROFILE` or explicit credentials
- Auto-sets: `CLAUDE_CODE_USE_BEDROCK=1` when bedrock provider is selected
- When enabled: skips macOS keychain extraction and `~/.claude` directory check
- Credential export: uses `aws configure export-credentials` to extract temporary credentials from AWS profiles
- Never mounts `~/.aws` directory - exports only specific credentials needed

Key functions in `scripts/ralphex-dk.sh`:
- `get_claude_provider()` - returns provider from CLI flag or env var
- `build_bedrock_env_args()` - builds docker -e flags for BEDROCK_ENV_VARS
- `export_aws_profile_credentials()` - exports credentials from AWS profile using aws CLI
- `validate_bedrock_config()` - validates bedrock configuration and returns warnings

Documentation: `docs/bedrock-setup.md`

### Docker Socket Support (Docker Wrapper Only)

The `--docker` flag (or `RALPHEX_DOCKER_SOCKET=1` env var) mounts the host Docker socket into the container, enabling testcontainers and Docker-dependent workflows.

- Config: `--docker` CLI flag or `RALPHEX_DOCKER_SOCKET=1` env var (truthy: "1", "true", "yes")
- Socket path: resolved from `DOCKER_HOST` env var (unix:// scheme) or defaults to `/var/run/docker.sock`
- Socket mount: without SELinux `:z`/`:Z` suffixes
- GID detection: `os.stat()` on socket, passed via `DOCKER_GID` env var for baseimage group setup
- Linux warning: emits security warning to stderr (macOS has VM isolation, no warning)
- Missing socket: exits with error (fail-fast, no silent degradation)

Key functions in `scripts/ralphex-dk.sh`:
- `is_docker_enabled()` - checks CLI flag and `RALPHEX_DOCKER_SOCKET` env var
- `resolve_docker_socket()` - resolves socket path from `DOCKER_HOST` or default
- `get_docker_socket_gid()` - detects socket file GID via `os.stat()`

### Docker Network Mode (Docker Wrapper Only)

The `--network` flag (or `RALPHEX_DOCKER_NETWORK` env var) sets the Docker network mode for the container, allowing it to reach docker-compose services on localhost.

- Config: `--network MODE` CLI flag or `RALPHEX_DOCKER_NETWORK` env var
- Passes `--network <value>` to `docker run`
- Common values: `host` (reach host-exposed ports), named networks (e.g., `my-compose-net`)

### Git Package API

Single public entry point: `git.NewService(path, logger, vcsCmd...) (*Service, error)`
- All git operations are methods on `Service` (CreateBranchForPlan, CreateWorktreeForPlan, MovePlanToCompleted, EnsureIgnored, etc.)
- `Logger` interface for dependency injection, compatible with `*color.Color`
- Uses `backend` interface internally, implemented by `externalBackend` which shells out to the configured VCS command
- Optional `vcsCmd` parameter overrides the default `"git"` command (e.g., path to `hg2git.sh` translation script)

Key files:
- `pkg/git/service.go` - `Service` type, `backend` interface
- `pkg/git/external.go` - VCS CLI backend (`externalBackend` type)

### Worktree Isolation Mode

`--worktree` flag or `use_worktree = true` config option runs each plan in an isolated git worktree, enabling parallel execution of multiple plans on the same repo.

- Worktrees created at `.ralphex/worktrees/<branch-name>` inside main repo
- Progress logger created before chdir so files land in main repo's `.ralphex/progress/`
- `MainGitSvc` in `executePlanRequest` handles cross-boundary ops (plan file moves in main repo)
- Worktree auto-removed on completion, failure, or SIGINT; branch preserved for PR
- Only active for `ModeFull` and `ModeTasksOnly` (review/plan/external modes skip worktree)
- `runWithWorktree()` in `cmd/ralphex/main.go` encapsulates the full lifecycle

Key files:
- `cmd/ralphex/main.go` - `runWithWorktree()`, `selectAndExecutePlan()`, interrupt cleanup
- `pkg/git/service.go` - `CreateWorktreeForPlan()`, `CommitPlanFile()`, `RemoveWorktree()`
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
- `default_branch` config option: override auto-detected default branch for review diffs
- `max_iterations` config option: override CLI default (50) for maximum task iterations per plan (CLI flag `--max-iterations` takes precedence)
- `vcs_command` config option: override the VCS binary used by the git backend (default: `"git"`). Set to a translation script path (e.g., `scripts/hg2git/hg2git.sh`) to use ralphex with Mercurial repos. See `docs/hg-support.md`
- Notification config: `notify_channels`, `notify_on_error`, `notify_on_complete`, `notify_timeout_ms`, plus channel-specific `notify_*` fields (see `docs/notifications.md`)
- `review_patience` config option: terminate external review after N consecutive unchanged rounds (0 = disabled). CLI flag `--review-patience` takes precedence
- `wait_on_limit` config option: duration to wait before retrying on rate limit (e.g., "1h", "30m"). CLI flag `--wait` takes precedence. Disabled by default
- `session_timeout` config option: per-session timeout for claude (e.g., "30m", "1h"). Kills hanging sessions and continues to next iteration. CLI flag `--session-timeout` takes precedence. Disabled by default

### Local Project Config (.ralphex/)

Projects can have local configuration that overrides global settings:

```
project/
├── .ralphex/           # optional, project-local config
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
- `claude_error_patterns`: comma-separated patterns for claude (default: "You've hit your limit,API Error:,cannot be launched inside another Claude Code session")
- `codex_error_patterns`: comma-separated patterns for codex (default: "Rate limit,quota exceeded")
- Matching is case-insensitive substring search
- Whitespace is trimmed from each pattern
- For claude: patterns checked on all output during normal execution (context cancellation paths bypass pattern checks)
- For codex and custom executors: patterns checked only when process exits with non-zero status and context is not canceled (avoids false positives from review findings and cancellation masking)
- On match, ralphex exits gracefully with pattern info and help command suggestion

Limit patterns for wait+retry behavior:
- `claude_limit_patterns`: comma-separated (default: "You've hit your limit")
- `codex_limit_patterns`: comma-separated (default: "Rate limit,quota exceeded")
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

5 default agents are installed on first run to `~/.config/ralphex/agents/`:
- `implementation.txt` - verifies code achieves stated goals
- `quality.txt` - reviews for bugs, security issues, race conditions
- `documentation.txt` - checks if docs need updates
- `simplification.txt` - detects over-engineering
- `testing.txt` - reviews test coverage and quality

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
- Run `ralphex --reset` to interactively restore defaults, or delete ALL `.txt` files manually
- Run `ralphex --dump-defaults <dir>` to extract raw embedded defaults for comparison or merging
- Use `/ralphex-update` skill for smart merging of updated defaults into customized configs
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

Unit tests mock external calls. After ANY code changes, run e2e test with a toy project to verify actual claude/codex integration and output streaming.

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
3. **MUST** run end-to-end test with toy project (see above)
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

## MkDocs Site

- Site source: `site/` directory with `mkdocs.yml`
- **Landing page**: `site/docs/index.html` is a manually crafted HTML page, not generated by MkDocs. Edit it directly to update the landing page.
- Template overrides: `site/overrides/` with `custom_dir: overrides` in mkdocs.yml
- **CI constraint**: Cloudflare Pages uses mkdocs-material 9.2.x, must use `materialx.emoji` syntax (not `material.extensions.emoji` which requires 9.4+)
- **Raw .md files**: MkDocs renders ALL `.md` files in `docs_dir` as HTML pages. To serve raw markdown (e.g., `assets/claude/*.md` for Claude Code skills), copy them AFTER `mkdocs build` - see `prep_site` target in Makefile

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
