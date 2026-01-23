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
pkg/processor/      # orchestration loop, prompts, signals
pkg/progress/       # timestamped logging with color
pkg/web/            # web dashboard, SSE streaming, session management
docs/plans/         # plan files location
```

## Code Style

- Use jessevdk/go-flags for CLI parsing
- All comments lowercase except godoc
- Table-driven tests with testify
- 80%+ test coverage target

## Key Patterns

- Signal-based completion detection (COMPLETED, FAILED, REVIEW_DONE signals)
- Streaming output with timestamps
- Progress logging to files
- Progress file locking (flock) for active session detection
- Multiple execution modes: full, review-only, codex-only
- Configuration via `~/.config/ralphex/` with embedded defaults
- File watching for multi-session dashboard using fsnotify

## Platform Support

- **Linux/macOS:** fully supported
- **Windows:** not supported (uses `syscall.Flock` for progress file locking)

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

- **config file**: copied on first run, always exists
- **scalars/colors**: per-field fallback to embedded defaults if missing
- **prompts**: copied if dir empty, per-file fallback to embedded if deleted
- **agents**: copied if dir empty, no fallback (user controls full set)
- `*Set` flags (e.g., `CodexEnabledSet`) distinguish explicit `false`/`0` from "not set"
- If ANY `.txt` exists in prompts/ or agents/, no defaults copied (user manages that dir)

### Agent System

5 default agents are installed on first run to `~/.config/ralphex/agents/`:
- `implementation.txt` - verifies code achieves stated goals
- `quality.txt` - reviews for bugs, security issues, race conditions
- `documentation.txt` - checks if docs need updates
- `simplification.txt` - detects over-engineering
- `testing.txt` - reviews test coverage and quality

**Template syntax:** Use `{{agent:name}}` in prompt files to reference agents. Each reference expands to Task tool instructions that tell Claude Code to run that agent.

**Customization:**
- Edit files in `~/.config/ralphex/agents/` to modify agent prompts
- Add new `.txt` files to create custom agents
- Delete ALL `.txt` files from the directory and restart ralphex to restore defaults
- Alternatively, reference agents installed in your Claude Code directly in prompt files (like `qa-expert`, `go-smells-expert`)

## Testing

```bash
go test ./...           # run all tests
go test -cover ./...    # with coverage
```

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
