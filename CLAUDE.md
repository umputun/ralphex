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

## Project Structure

```
cmd/ralphex/        # main entry point, CLI parsing
pkg/config/         # configuration loading, defaults, prompts, agents
pkg/executor/       # claude and codex CLI execution
pkg/git/            # git operations using go-git library
pkg/plan/           # plan file selection and manipulation
pkg/processor/      # orchestration loop, prompts, signals
pkg/progress/       # timestamped logging with color
pkg/web/            # web dashboard, SSE streaming, session management
e2e/                # playwright e2e tests for web dashboard
docs/plans/         # plan files location
```

## Code Style

- Use jessevdk/go-flags for CLI parsing
- All comments lowercase except godoc
- Table-driven tests with testify
- 80%+ test coverage target

## Key Patterns

- Signal-based completion detection (COMPLETED, FAILED, REVIEW_DONE signals)
- Plan creation signals: QUESTION (with JSON payload) and PLAN_READY
- Streaming output with timestamps
- Progress logging to files
- Progress file locking (flock) for active session detection
- Multiple execution modes: full, tasks-only, review-only, codex-only, plan creation
- Configuration via `~/.config/ralphex/` with embedded defaults
- File watching for multi-session dashboard using fsnotify
- Optional finalize step after successful reviews (disabled by default)

### Finalize Step

Optional post-completion step that runs after successful review phases:

- Triggers on: ModeFull, ModeReview, ModeCodexOnly (modes with review pipeline)
- Disabled by default (`finalize_enabled = false` in config)
- Uses task color (green) for output
- Runs once, no signal loop - best effort (failures logged but don't block success)
- Template variables supported (`{{DEFAULT_BRANCH}}`, etc.)

Default behavior (when enabled): rebases commits onto default branch, optionally squashes related commits, runs tests to verify.

Config option: `finalize_enabled = true` in `~/.config/ralphex/config` or `.ralphex/config`
Prompt file: `~/.config/ralphex/prompts/finalize.txt` or `.ralphex/prompts/finalize.txt`

Key files:
- `pkg/processor/runner.go` - `runFinalize()` method called at end of review modes
- `pkg/config/defaults/prompts/finalize.txt` - default finalize prompt

### Git Package API

Single public entry point: `git.NewService(path, logger) (*Service, error)`
- All git operations are methods on `Service` (CreateBranchForPlan, MovePlanToCompleted, EnsureIgnored, etc.)
- `Logger` interface for dependency injection, compatible with `*color.Color`
- Internal `repo` type is unexported - use `Service` for all git operations

### Plan Creation Mode

The `--plan "description"` flag enables interactive plan creation:

- Claude explores codebase and asks clarifying questions
- Questions use QUESTION signal with JSON: `{"question": "...", "options": [...]}`
- User answers via fzf picker (or numbered fallback)
- Q&A history stored in progress file for context
- When ready, Claude emits PLAN_DRAFT signal with full plan content for user review
- User can Accept, Revise (with feedback), or Reject the draft
- If revised, feedback is passed to Claude for plan modifications
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
- `pkg/processor/signals.go` - QUESTION/PLAN_DRAFT/PLAN_READY signal parsing
- `pkg/render/markdown.go` - glamour-based markdown rendering for draft preview
- `pkg/config/defaults/prompts/make_plan.txt` - plan creation prompt

## Platform Support

- **Linux/macOS:** fully supported
- **Windows:** builds and runs, but with limitations:
  - Process group signals not available (graceful shutdown kills direct process only, not child processes)
  - File locking not available (active session detection disabled)

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

- Global config location: `~/.config/ralphex/`
- Local config location: `.ralphex/` (per-project, optional)
- Config file format: INI (using gopkg.in/ini.v1)
- Embedded defaults in `pkg/config/defaults/`
- Precedence: CLI flags > local config > global config > embedded defaults
- Custom prompts: `~/.config/ralphex/prompts/*.txt` or `.ralphex/prompts/*.txt`
- Custom agents: `~/.config/ralphex/agents/*.txt` or `.ralphex/agents/*.txt`

### Local Project Config (.ralphex/)

Projects can have local configuration that overrides global settings:

```
project/
├── .ralphex/           # optional, project-local config
│   ├── config          # overrides specific settings (per-field merge)
│   ├── prompts/        # per-file fallback: local → global → embedded
│   │   └── task.txt    # only override task prompt
│   └── agents/         # replaces global if has files (no merge)
│       └── custom.txt  # project-specific agent
```

**Merge strategy:**
- **Config file**: per-field override (local values override global, missing fields fall back)
- **Prompts**: per-file fallback (local → global → embedded for each prompt file)
- **Agents**: replace entirely (if local agents/ has .txt files, use ONLY local agents)

### Config Defaults Behavior

- **Commented templates**: config file, prompts, and agents are installed with all content commented out (prefixed `# `)
- **Auto-update**: files with only comments/whitespace are safe to overwrite on updates - users get new defaults automatically
- **User customization**: uncommenting any line marks the file as customized - it will be preserved and never overwritten
- **Fallback loading**: when loading config/prompts/agents, if file content is all-commented (no actual values), embedded defaults are used
- **scalars/colors**: per-field fallback to embedded defaults if missing
- `*Set` flags (e.g., `CodexEnabledSet`) distinguish explicit `false`/`0` from "not set"

### Error Pattern Detection

Configurable patterns detect rate limit and quota errors in claude/codex output:
- `claude_error_patterns`: comma-separated patterns for claude (default: "You've hit your limit")
- `codex_error_patterns`: comma-separated patterns for codex (default: "Rate limit,quota exceeded")
- Matching is case-insensitive substring search
- Whitespace is trimmed from each pattern
- On match, ralphex exits gracefully with pattern info and help command suggestion

Implementation:
- `PatternMatchError` type in `pkg/executor/executor.go` with `Pattern` and `HelpCmd` fields
- `checkErrorPatterns()` helper for case-insensitive matching
- Patterns passed via `ClaudeExecutor.ErrorPatterns` and `CodexExecutor.ErrorPatterns`

### Agent System

5 default agents are installed on first run to `~/.config/ralphex/agents/`:
- `implementation.txt` - verifies code achieves stated goals
- `quality.txt` - reviews for bugs, security issues, race conditions
- `documentation.txt` - checks if docs need updates
- `simplification.txt` - detects over-engineering
- `testing.txt` - reviews test coverage and quality

**Template variables:** Prompt files support variable expansion via `replacePromptVariables()` in `pkg/processor/prompts.go`:
- `{{PLAN_FILE}}` - path to plan file or fallback text
- `{{PROGRESS_FILE}}` - path to progress log or fallback text
- `{{GOAL}}` - human-readable goal (plan-based or branch comparison)
- `{{DEFAULT_BRANCH}}` - detected default branch (main, master, origin/main, etc.)
- `{{agent:name}}` - expands to Task tool instructions for the named agent

Variables are also expanded inside agent content, so custom agents can use `{{DEFAULT_BRANCH}}` etc.

**Customization:**
- Edit files in `~/.config/ralphex/agents/` to modify agent prompts
- Add new `.txt` files to create custom agents
- Run `ralphex --reset` to interactively restore defaults, or delete ALL `.txt` files manually
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
./scripts/prep-toy-test.sh
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
tail -f progress-fix-issues.txt

# recent activity
tail -50 progress-*.txt
```

## Development Workflow

**CRITICAL: After ANY code changes to ralphex:**

1. Run unit tests: `make test`
2. Run linter: `make lint`
3. **MUST** run end-to-end test with toy project (see above)
4. Monitor `tail -f progress-*.txt` to verify output streaming works

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
- Template overrides: `site/overrides/` with `custom_dir: overrides` in mkdocs.yml
- **CI constraint**: Cloudflare Pages uses mkdocs-material 9.2.x, must use `materialx.emoji` syntax (not `material.extensions.emoji` which requires 9.4+)
- **Raw .md files**: MkDocs renders ALL `.md` files in `docs_dir` as HTML pages. To serve raw markdown (e.g., `assets/claude/*.md` for Claude Code skills), copy them AFTER `mkdocs build` - see `prep_site` target in Makefile

## Testing Safety Rules

- **CRITICAL: Tests must NEVER touch real user config directory** (`~/.config/ralphex/`)
- All tests MUST use `t.TempDir()` for any file operations
- Config pollution is hard to debug - corrupted files cause cryptic errors
- Verify tests are clean: compare MD5 checksums of config files before/after `go test ./...`

## Workflow Rules

- **CHANGELOG**: Never modify during development - updates are part of release process only
- **Version sections**: Never add entries to existing version sections - versions are immutable once released
- **Linter warnings**: Add exclusions to `.golangci.yml` instead of `_, _ =` prefixes for fmt.Fprintf/Fprintln
- **Exporting functions**: When changing visibility (lowercase to uppercase), check ALL callers including test files
