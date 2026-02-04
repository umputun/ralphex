<p align="center">
  <img src="assets/ralphex-wordmark-split.png" alt="ralphex" width="400">
</p>

<p align="center">
  <a href="https://github.com/umputun/ralphex/actions/workflows/ci.yml"><img src="https://github.com/umputun/ralphex/actions/workflows/ci.yml/badge.svg" alt="build"></a>
  <a href="https://coveralls.io/github/umputun/ralphex?branch=master"><img src="https://coveralls.io/repos/github/umputun/ralphex/badge.svg?branch=master" alt="Coverage Status"></a>
  <a href="https://goreportcard.com/report/github.com/umputun/ralphex"><img src="https://goreportcard.com/badge/github.com/umputun/ralphex?v=2" alt="Go Report Card"></a>
</p>

<h2 align="center">Autonomous plan execution with Claude Code</h2>

*ralphex is a standalone CLI tool that runs in your terminal from the root of a git repository. It orchestrates Claude Code to execute implementation plans autonomously - no IDE plugins or cloud services required, just Claude Code and a single binary.*

Claude Code is powerful but interactive - it requires you to watch, approve, and guide each step. For complex features spanning multiple tasks, this means hours of babysitting. Worse, as context fills up during long sessions, the model's quality degrades - it starts making mistakes, forgetting earlier decisions, and producing worse code.

ralphex solves both problems. Each task executes in a fresh Claude Code session with minimal context, keeping the model sharp throughout the entire plan. Write a plan with tasks and validation commands, start ralphex, and walk away. Come back to find your feature implemented, reviewed, and committed - or check the progress log to see what it's doing.

<details markdown>
<summary>Task Execution Screenshot</summary>

![ralphex tasks](assets/ralphex-tasks.png)

</details>

<details markdown>
<summary>Review Mode Screenshot</summary>

![ralphex review](assets/ralphex-review.png)

</details>

<details markdown>
<summary>Web Dashboard Screenshot</summary>

![ralphex web dashboard](assets/ralphex-web.png)

</details>

## Features

- **Zero setup** - works out of the box with sensible defaults, no configuration required
- **Autonomous task execution** - executes plan tasks one at a time with automatic retry
- **Interactive plan creation** - create plans through dialogue with Claude via `--plan` flag
- **Multi-phase code review** - 5 agents → codex → 2 agents review pipeline
- **Custom review agents** - configurable agents with `{{agent:name}}` template system and user defined prompts
- **Automatic branch creation** - creates git branch from plan filename
- **Plan completion tracking** - moves completed plans to `completed/` folder
- **Automatic commits** - commits after each task and review fix
- **Real-time monitoring** - streaming output with timestamps, colors, and detailed logs
- **Web dashboard** - browser-based real-time view with `--serve` flag
- **Docker support** - run in isolated container for safer autonomous execution
- **Multiple modes** - full execution, tasks-only, review-only, codex-only, or plan creation

## Quick Start

Make sure ralphex is [installed](#installation) and your project is a git repository. You need a plan file in `docs/plans/`, for example:

```markdown
# Plan: My Feature

## Validation Commands
- `go test ./...`

### Task 1: Implement feature
- [ ] Add the new functionality
- [ ] Add tests
```

Then run:

```bash
ralphex docs/plans/my-feature.md
```

ralphex will create a branch, execute tasks, commit results, run multi-phase reviews, and move the plan to `completed/` when done.

## How It Works

ralphex executes plans in four phases with automated code reviews.

<details markdown>
<summary>Execution Flow Diagram</summary>

![ralphex flow](assets/ralphex-flow.png)

</details>

### Phase 1: Task Execution

1. Reads plan file and finds first incomplete task (`### Task N:` with `- [ ]` checkboxes)
2. Sends task to Claude Code for execution
3. Runs validation commands (tests, linters) after each task
4. Marks checkboxes as done `[x]`, commits changes
5. Repeats until all tasks complete or max iterations reached

### Phase 2: First Code Review

Launches 5 review agents **in parallel** via Claude Code Task tool:

| Agent | Purpose |
|-------|---------|
| `quality` | bugs, security issues, race conditions |
| `implementation` | verifies code achieves stated goals |
| `testing` | test coverage and quality |
| `simplification` | detects over-engineering |
| `documentation` | checks if docs need updates |

Claude verifies findings, fixes confirmed issues, and commits.

*[Default agents](https://github.com/umputun/ralphex/tree/master/pkg/config/defaults/agents) provide common, language-agnostic review steps. They can be customized and tuned for your specific needs, languages, and workflows. See [Customization](#customization) for details.*

### Phase 3: Codex External Review (optional)

1. Runs codex (GPT-5.2) for independent code review
2. Claude evaluates codex findings, fixes valid issues
3. Iterates until codex finds no open issues

### Phase 4: Second Code Review

1. Launches 2 agents (`quality` + `implementation`) for final review
2. Focuses on critical/major issues only
3. Iterates until no issues found
4. Moves plan to `completed/` folder on success

*Second review agents are configurable via `prompts/review_second.txt`.*

### Plan Creation

Plans can be created in several ways:
- **[Claude Code](#claude-code-integration-optional)** - use slash commands like `/ralphex-plan` or your own planning workflows
- **Manually** - write markdown files directly in `docs/plans/`
- **`--plan` flag** - integrated option that handles the entire flow
- **Auto-detection** - running `ralphex` without arguments on master/main prompts for plan creation if no plans exist

The `--plan` flag provides a simpler integrated experience:

```bash
ralphex --plan "add health check endpoint"
```

Claude explores your codebase, asks clarifying questions via a terminal picker (fzf or numbered fallback), and generates a complete plan file in `docs/plans/`.

**Example session:**
```
$ ralphex --plan "add caching for API responses"
[10:30:05] analyzing codebase structure...
[10:30:12] found existing store layer in pkg/store/

QUESTION: Which cache backend?
  > Redis
    In-memory
    File-based

[10:30:45] ANSWER: Redis
[10:31:00] continuing plan creation...
[10:32:05] plan written to docs/plans/add-api-caching.md

Continue with plan implementation?
  > Yes, execute plan
    No, exit
```

After plan creation, you can choose to continue with immediate execution or exit to run ralphex later. Progress is logged to `progress-plan-<name>.txt`.

## Installation

### From source

```bash
go install github.com/umputun/ralphex/cmd/ralphex@latest
```

### Using Homebrew

```bash
brew install umputun/apps/ralphex
```

### From releases

Download the appropriate binary from [releases](https://github.com/umputun/ralphex/releases).

### Using Docker

Download the wrapper script and install to PATH:

```bash
curl -sL https://raw.githubusercontent.com/umputun/ralphex/master/scripts/ralphex-dk.sh -o /usr/local/bin/ralphex
chmod +x /usr/local/bin/ralphex
```

The script defaults to the Go image (`ralphex-go`). For other languages, use the base image:
```bash
export RALPHEX_IMAGE=ghcr.io/umputun/ralphex:latest
```

Then use `ralphex` as usual - it runs in a container with Claude Code and Codex pre-installed. The script shows which image it's using at startup.

**Why use Docker?** ralphex runs Claude Code with `--dangerously-skip-permissions`, giving it full access to execute commands and modify files. Running in a container provides isolation - Claude can only access the mounted project directory, not your entire system. This makes autonomous execution significantly safer.

<details markdown>
<summary>Isolation details</summary>

**Container CAN access (read-write):**
- Project directory mounted at `/workspace` - full access to create, modify, delete files
- Git operations within the project (branch, commit, etc.)

**Container CAN access (read-only):**
- `~/.claude/` - credentials and settings (copied at startup, not modified)
- `~/.codex/` - codex credentials if present
- `~/.config/ralphex/` - user-level ralphex configuration
- `~/.gitconfig` - git identity for commits
- `.ralphex/` - project-level configuration if present

**Container CANNOT access:**
- Host filesystem outside mounted directories
- Other projects or repositories
- SSH keys, AWS credentials, or other secrets in `~/.ssh`, `~/.aws`, etc.
- System files, binaries, or configurations
- Other running processes or containers

**Network:** Full network access (required for Claude API calls)

**Privileges:** Runs as non-root user with no elevated capabilities

</details>

**Volume mounts:**
- **Read-only**: `~/.claude` and `~/.codex` mounted to `/mnt/`, copied at startup to preserve isolation
- **Read-write**: project directory (`/workspace`) - where ralphex creates branches, edits code, and commits

**Requirements:**
- Docker installed and running
- Claude Code credentials in `~/.claude/`
- Codex credentials in `~/.codex/` (optional, for codex review phase)
- Git config in `~/.gitconfig` (for commits)

**Environment variables:**
- `RALPHEX_IMAGE` - Docker image to use (default: `ghcr.io/umputun/ralphex:latest`)
- `RALPHEX_PORT` - Port for web dashboard when using `--serve` (default: `8080`)

**Updating the image:**
```bash
ralphex --update  # pull latest image
```

<details markdown>
<summary>Available images</summary>

Two images are published:

| Image | Description |
|-------|-------------|
| `ghcr.io/umputun/ralphex:latest` | Base image for any language |
| `ghcr.io/umputun/ralphex-go:latest` | Go development (extends base) |

**Base image includes:**

| Tool | Version | Purpose |
|------|---------|---------|
| Claude Code | latest | AI coding assistant |
| Codex | latest | External code review |
| Node.js/npm | 24.x | Required for Claude Code |
| Python/pip | 3.x | Scripts and automation |
| git | 2.x | Version control |
| make | 4.x | Build automation |
| gcc, musl-dev | - | C compiler for native extensions |
| bash | 5.x | Shell |
| fzf | - | Fuzzy finder for plan selection |
| ripgrep | - | Fast search (used by Claude Code) |

**Go image adds:**

| Tool | Version | Purpose |
|------|---------|---------|
| Go | 1.25.6 | Go compiler and runtime |
| golangci-lint | latest | Go linter |
| moq | latest | Mock generator |
| goimports | latest | Import formatter |

**For Go projects**, use the `-go` image:
```bash
RALPHEX_IMAGE=ghcr.io/umputun/ralphex-go:latest ralphex docs/plans/feature.md
```

**For other languages**, extend the base image. Example `Dockerfile-go` (used to build `ralphex-go`):

```dockerfile
FROM ghcr.io/umputun/ralphex:latest

# install go from official distribution
ARG GO_VERSION=1.25.6
RUN ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/') && \
    wget -qO- "https://go.dev/dl/go${GO_VERSION}.linux-${ARCH}.tar.gz" | tar -xz -C /usr/local

ENV GOROOT=/usr/local/go
ENV GOPATH=/home/app/go
ENV PATH="${PATH}:${GOROOT}/bin:${GOPATH}/bin"

# install go tools
RUN wget -qO- https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s -- -b /usr/local/bin && \
    GOBIN=/usr/local/bin go install github.com/matryer/moq@latest && \
    GOBIN=/usr/local/bin go install golang.org/x/tools/cmd/goimports@latest
```

Similar pattern for Rust, Java, etc:
```dockerfile
FROM ghcr.io/umputun/ralphex:latest

# rust
RUN apk add --no-cache rust cargo
ENV CARGO_HOME=/home/app/.cargo PATH="${PATH}:${CARGO_HOME}/bin"

# java
RUN apk add --no-cache openjdk21-jdk
ENV JAVA_HOME=/usr/lib/jvm/java-21-openjdk PATH="${PATH}:${JAVA_HOME}/bin"
```

Build and use:
```bash
docker build -t my-ralphex -f Dockerfile.python .
RALPHEX_IMAGE=my-ralphex ralphex docs/plans/feature.md
```

</details>

Example with custom port:
```bash
RALPHEX_PORT=3000 ralphex --serve --port 3000 docs/plans/feature.md
```

## Usage

**Note:** ralphex must be run from the repository root directory (where `.git` is located).

```bash
# execute plan with task loop + reviews
ralphex docs/plans/feature.md

# select plan with fzf, or create one interactively if none exist
ralphex

# review-only mode (skip task execution)
ralphex --review docs/plans/feature.md

# codex-only mode (skip tasks and first claude review, still uses claude to evaluate/fix)
ralphex --codex-only

# tasks-only mode (run only task phase, skip all reviews)
ralphex --tasks-only docs/plans/feature.md

# interactive plan creation
ralphex --plan "add user authentication"

# with custom max iterations
ralphex --max-iterations=100 docs/plans/feature.md

# with web dashboard
ralphex --serve docs/plans/feature.md

# web dashboard on custom port
ralphex --serve --port 3000 docs/plans/feature.md
```

### Options

| Flag | Description | Default |
|------|-------------|---------|
| `-m, --max-iterations` | Maximum task iterations | 50 |
| `-r, --review` | Skip task execution, run full review pipeline | false |
| `-c, --codex-only` | Skip tasks and first review, run codex → claude evaluation → fixes | false |
| `-t, --tasks-only` | Run only task phase, skip all reviews | false |
| `--plan` | Create plan interactively (provide description) | - |
| `-s, --serve` | Start web dashboard for real-time streaming | false |
| `-p, --port` | Web dashboard port (used with `--serve`) | 8080 |
| `-w, --watch` | Directories to watch for progress files (repeatable) | - |
| `-d, --debug` | Enable debug logging | false |
| `--no-color` | Disable color output | false |
| `--reset` | Interactively reset global config to embedded defaults | - |

## Plan File Format

Plans are markdown files with task sections. Each task has checkboxes that claude marks complete.

```markdown
# Plan: Add User Authentication

## Overview
Add JWT-based authentication to the API.

## Validation Commands
- `go test ./...`
- `golangci-lint run`

### Task 1: Add auth middleware
- [ ] Create JWT validation middleware
- [ ] Add to router for protected routes
- [ ] Add tests
- [ ] Mark completed

### Task 2: Add login endpoint
- [ ] Create /api/login handler
- [ ] Return JWT on successful auth
- [ ] Add tests
- [ ] Mark completed
```

**Requirements:**
- Task headers must use `### Task N:` or `### Iteration N:` format
- Checkboxes: `- [ ]` (incomplete) or `- [x]` (completed)
- Include `## Validation Commands` section with test/lint commands
- Place plans in `docs/plans/` directory (configurable via `plans_dir`)

## Review Agents

The review pipeline is fully customizable. ralphex ships with sensible defaults that work for any language, but you can modify agents, add new ones, or replace prompts entirely to match your specific workflow.

### Default Agents

These 5 agents cover common review concerns and work well out of the box. Customize or replace them based on your needs:

| Agent | Phase | Purpose |
|-------|-------|---------|
| `quality` | 1st & 2nd | bugs, security issues, race conditions |
| `implementation` | 1st & 2nd | verifies code achieves stated goals |
| `testing` | 1st only | test coverage and quality |
| `simplification` | 1st only | detects over-engineering |
| `documentation` | 1st only | checks if docs need updates |

### Template Syntax

Custom prompt files support variable expansion. All variables use the `{{VARIABLE}}` syntax.

**Available variables:**

| Variable | Description | Example value |
|----------|-------------|---------------|
| `{{PLAN_FILE}}` | Path to the plan file being executed | `docs/plans/feature.md` |
| `{{PROGRESS_FILE}}` | Path to the progress log file | `progress-feature.txt` |
| `{{GOAL}}` | Human-readable goal description | `implementation of plan at docs/plans/feature.md` |
| `{{DEFAULT_BRANCH}}` | Default branch name (detected from repo) | `main`, `master`, `origin/main` |
| `{{agent:name}}` | Expands to Task tool instructions for the named agent | (see below) |

**Agent references:**

Reference agents in prompt files using `{{agent:name}}` syntax:

```
Launch the following review agents in parallel:
{{agent:quality}}
{{agent:implementation}}
{{agent:testing}}
```

Each `{{agent:name}}` expands to Task tool instructions that tell Claude Code to run that agent. Variables inside agent content are also expanded, so agents can use `{{DEFAULT_BRANCH}}` or other variables.

### Customization

The entire system is designed for customization - both task execution and reviews:

**Agent files** (`~/.config/ralphex/agents/`):
- Edit existing files to modify agent behavior
- Add new `.txt` files to create custom agents
- Run `ralphex --reset` to interactively restore defaults, or delete all files manually
- Alternatively, reference agents already installed in your Claude Code directly in prompt files (see example below)

**Prompt files** (`~/.config/ralphex/prompts/`):
- `task.txt` - task execution prompt
- `review_first.txt` - comprehensive review (default: 5 language-agnostic agents - quality, implementation, testing, simplification, documentation; customizable)
- `codex.txt` - codex review prompt
- `review_second.txt` - final review, critical/major issues only (default: 2 agents - quality, implementation; customizable)
- `finalize.txt` - optional finalize step prompt (disabled by default)

**Comment syntax:**
Lines starting with `#` (after optional whitespace) are treated as comments and stripped when loading prompt and agent files. Use comments to document your customizations:

```txt
# security agent - checks for vulnerabilities
# updated: 2024-01-15
check for SQL injection
check for XSS
```

Note: Inline comments are not supported (`text # comment` keeps the entire line).

**Examples:**
- Add a security-focused agent for fintech projects
- Remove `simplification` agent if over-engineering isn't a concern
- Create language-specific agents (Python linting, TypeScript types)
- Modify prompts to change how many agents run per phase

**Using Claude Code agents directly:**

Instead of creating agent files, you can reference agents installed in your Claude Code directly in prompt files:

```txt
# in review_first.txt - just list agent names with their prompts
Agents to launch:
1. qa-expert - "Review for bugs and security issues"
2. go-test-expert - "Review test coverage and quality"
3. go-smells-expert - "Review for code smells"
```

## Requirements

- `claude` - Claude Code CLI
- `fzf` - for plan selection (optional)
- `codex` - for external review (optional)

## Configuration

ralphex uses a configuration directory at `~/.config/ralphex/` with the following structure:

```
~/.config/ralphex/
├── config              # main configuration file (INI format)
├── prompts/            # custom prompt templates
│   ├── task.txt
│   ├── review_first.txt
│   ├── review_second.txt
│   └── codex.txt
└── agents/             # custom review agents (*.txt files)
```

On first run, ralphex creates this directory with default configuration.

**Commented templates:**
- Config files are installed with all content commented out (`# ` prefix)
- Uncomment only the settings you want to customize
- Files that remain all-commented receive automatic updates with new defaults
- Once you uncomment any setting, the file is preserved and won't be overwritten

### Local Project Config

Projects can override global settings with a `.ralphex/` directory in the project root:

```
project/
├── .ralphex/           # optional, project-local config
│   ├── config          # overrides specific settings
│   ├── prompts/        # custom prompts for this project
│   └── agents/         # custom agents for this project
```

**Priority:** CLI flags > local `.ralphex/` > global `~/.config/ralphex/` > embedded defaults

**Merge behavior:**
- **Config file**: per-field override (local values override global, missing fields fall back)
- **Prompts**: per-file fallback (local → global → embedded for each prompt file)
- **Agents**: replace entirely (if local `agents/` has `.txt` files, use ONLY local agents)

### Configuration options

| Option | Description | Default |
|--------|-------------|---------|
| `claude_command` | Claude CLI command | `claude` |
| `claude_args` | Claude CLI arguments | `--dangerously-skip-permissions --output-format stream-json --verbose` |
| `codex_enabled` | Enable codex review phase | `true` |
| `codex_command` | Codex CLI command | `codex` |
| `codex_model` | Codex model ID | `gpt-5.2-codex` |
| `codex_reasoning_effort` | Reasoning effort level | `xhigh` |
| `codex_timeout_ms` | Codex timeout in ms | `3600000` |
| `codex_sandbox` | Sandbox mode | `read-only` |
| `iteration_delay_ms` | Delay between iterations | `2000` |
| `task_retry_count` | Task retry attempts | `1` |
| `finalize_enabled` | Enable finalize step after reviews | `false` |
| `plans_dir` | Plans directory | `docs/plans` |
| `color_task` | Task execution phase color (hex) | `#00ff00` |
| `color_review` | Review phase color (hex) | `#00ffff` |
| `color_codex` | Codex review color (hex) | `#ff00ff` |
| `color_claude_eval` | Claude evaluation color (hex) | `#64c8ff` |
| `color_warn` | Warning messages color (hex) | `#ffff00` |
| `color_error` | Error messages color (hex) | `#ff0000` |
| `color_signal` | Completion/failure signals color (hex) | `#ff6464` |
| `color_timestamp` | Timestamp prefix color (hex) | `#8a8a8a` |
| `color_info` | Informational messages color (hex) | `#b4b4b4` |
| `claude_error_patterns` | Patterns to detect in claude output (comma-separated) | `You've hit your limit` |
| `codex_error_patterns` | Patterns to detect in codex output (comma-separated) | `Rate limit,quota exceeded` |

Colors use 24-bit RGB (true color), supported natively by all modern terminals (iTerm2, Kitty, Terminal.app, Windows Terminal, GNOME Terminal, Alacritty, Zed, VS Code, etc). Older terminals will degrade gracefully. Use `--no-color` to disable colors entirely.

Error patterns use case-insensitive substring matching. When a pattern is detected in claude or codex output, ralphex exits gracefully with an informative message suggesting how to check usage/status. Multiple patterns are separated by commas, with whitespace trimmed from each pattern.

### Custom prompts

Place custom prompt files in `~/.config/ralphex/prompts/` to override the built-in prompts. Missing files fall back to embedded defaults. See [Review Agents](#review-agents) section for agent customization.

<details markdown>
<summary><b>FAQ</b></summary>

**I installed ralphex, what do I do next?**

Create a plan file in `docs/plans/` (see [Quick Start](#quick-start) for format), then run `ralphex docs/plans/your-plan.md`. ralphex will create a branch, execute tasks, and run reviews automatically.

**Why are there two review phases?**

First review is comprehensive (5 agents by default), second is a final check focusing on critical/major issues only (2 agents). See [How It Works](#how-it-works).

**How do I use my own Claude Code agents?**

Reference them directly in prompt files by name, e.g., `qa-expert - "Review for bugs"`. See [Customization](#customization).

**What if codex isn't installed?**

Codex is optional. If not installed, the codex review phase is skipped automatically.

**Can I run just reviews without task execution?**

Yes, use `--review` flag. See [CLI Options](#cli-options).

**Can I run ralphex in a non-git directory?**

No. Git is required for branch management, automatic commits, and diff-based code reviews.

**What if my repository has no commits?**

ralphex prompts to create an initial commit when the repository is empty. This is required because ralphex needs branches for feature isolation. Answer "y" to let ralphex stage all files and create an initial commit, or create one manually first with `git add . && git commit -m "initial commit"`.

**Should I run ralphex on master or a feature branch?**

For full mode, start on master - ralphex creates a branch automatically from the plan filename. For `--review` mode, switch to your feature branch first - reviews compare against master using `git diff master...HEAD`.

**How do I restore default agents after customizing?**

Run `ralphex --reset` to interactively reset global config. Select which components to reset (config, prompts, agents). Alternatively, delete all `.txt` files from `~/.config/ralphex/agents/` manually.

**How does local .ralphex/ config interact with global config?**

Priority: CLI flags > local `.ralphex/config` > global `~/.config/ralphex/config` > embedded defaults. Each local setting overrides the corresponding global one—no need to duplicate the entire file. For agents: if local `agents/` has any `.txt` files, it replaces global agents entirely.

**What happens to uncommitted changes if ralphex fails?**

Ralphex commits after each completed task. If execution fails, completed tasks are already committed to the feature branch. Uncommitted changes from the failed task remain in the working directory for manual inspection.

**What if ralphex is interrupted mid-execution?**

Completed tasks are already committed to the feature branch. To resume, re-run `ralphex docs/plans/<plan>.md`. Ralphex detects completed tasks via `[x]` checkboxes in the plan and continues from the first incomplete task. For review sessions, simply restart. Reviews re-run from iteration 1, but fixes from previous iterations remain in the codebase.

**What's the difference between progress file and plan file?**

Progress file (`progress-*.txt`) is a real-time execution log—tail it to monitor. Plan file tracks task state (`[ ]` vs `[x]`). To resume, re-run ralphex on the plan file; it finds incomplete tasks automatically.

**Do I need to commit changes before running ralphex?**

It depends. If the plan file is the only uncommitted change, ralphex auto-commits it after creating the feature branch and continues execution. If other files have uncommitted changes, ralphex shows a helpful error with options: stash temporarily (`git stash`), commit first (`git commit -am "wip"`), or use review-only mode (`ralphex --review`).

**What's the difference between agents/ and prompts/?**

Agents define *what* to check (review instructions). Prompts define *how* the workflow runs (execution steps, signal handling).

**Can I use Cursor CLI instead of Claude Code?**

Yes. [Cursor CLI](https://cursor.com/cli) is community-tested as a drop-in alternative. Configure in `~/.config/ralphex/config`:

```ini
claude_command = agent
claude_args = --force --output-format stream-json
```

Key differences: `agent` command (not `claude`), `--force` flag (not `--dangerously-skip-permissions`). Stream format and signals are compatible. *Note: this is community-tested, not officially supported. Compatibility depends on Cursor maintaining Claude Code compatibility.*

**Can I run something after all phases complete (notifications, rebase commits, etc.)?**

Yes. Enable the finalize step with `finalize_enabled = true` in config. It runs once after successful review phases (best-effort—failures are logged but don't block success). The default `finalize.txt` prompt rebases onto the default branch and optionally squashes commits into logical groups. Customize `~/.config/ralphex/prompts/finalize.txt` for other actions like sending notifications, pushing to remote, or running custom scripts.

</details>

## Web Dashboard

The `--serve` flag starts a browser-based dashboard for real-time monitoring of plan execution.

```bash
ralphex --serve docs/plans/feature.md
# web dashboard: http://localhost:8080
```

### Features

- **Real-time streaming** - SSE connection for live output updates
- **Phase navigation** - filter by All/Task/Review/Codex phases
- **Collapsible sections** - organized output with expand/collapse
- **Text search** - find text with highlighting (keyboard: `/` to focus, `Escape` to clear)
- **Auto-scroll** - follows output, click to disable
- **Late-join support** - new clients receive full history

The dashboard uses a dark theme with phase-specific colors matching terminal output. All file and stdout logging continues unchanged when using `--serve`.

### Multi-Session Mode

The `--watch` flag enables monitoring multiple ralphex sessions simultaneously:

```bash
# watch specific directories for progress files
ralphex --serve --watch ~/projects/frontend --watch ~/projects/backend

# configure watch directories in config file
# watch_dirs = /home/user/projects, /var/log/ralphex
```

Multi-session features:
- **Session sidebar** - lists all discovered sessions, click to switch (keyboard: `S` to toggle)
- **Active detection** - pulsing indicator for running sessions via file locking
- **Auto-discovery** - new sessions appear automatically as they start

## Claude Code Integration (Optional)

ralphex works standalone from the terminal. Optionally, you can add slash commands to Claude Code for a more integrated experience.

### Available Commands

| Command | Description |
|---------|-------------|
| `/ralphex` | Launch and monitor ralphex execution with interactive mode/plan selection |
| `/ralphex-plan` | Create structured implementation plans with guided context gathering |

### Installation

The ralphex CLI is the primary interface. Claude Code skills (`/ralphex` and `/ralphex-plan`) are optional convenience commands.

**Via Plugin Marketplace (Recommended)**

```bash
# Add ralphex marketplace
/plugin marketplace add umputun/ralphex

# Install the plugin
/plugin install ralphex@umputun-ralphex
```

Benefits: Auto-updates when marketplace refreshes (at Claude Code startup).

**Manual Installation (Alternative)**

The slash command definitions are hosted at:
- [`/ralphex`](https://ralphex.com/assets/claude/ralphex.md)
- [`/ralphex-plan`](https://ralphex.com/assets/claude/ralphex-plan.md)

To install, ask Claude Code to "install ralphex slash commands" or manually copy the files to `~/.claude/commands/`.

### Usage

Once installed:

```
# in Claude Code conversation
/ralphex-plan add user authentication    # creates plan interactively
/ralphex docs/plans/auth.md              # launches execution
"check ralphex"                          # gets status update
```

The `/ralphex` command runs ralphex in the background and provides status updates on request. The `/ralphex-plan` command guides you through creating well-structured plans with context discovery and approach selection.

## For LLMs

See [llms.txt](llms.txt) for LLM-optimized documentation.

## License

MIT License - see [LICENSE](LICENSE) file.
