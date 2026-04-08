---
# Add Copilot CLI support

## Overview
Add an included wrapper around GitHub Copilot CLI so it can replace Claude Code in ralphex task and review phases through the existing `claude_command` / `claude_args` integration path. Keep the feature in the wrapper-and-docs layer: no new Go executor, no new config keys, and no dedicated `external_review_tool = copilot` path unless the existing wrapper mechanism proves insufficient.

## Context
- Files involved:
  - Create: `scripts/copilot-as-claude/copilot-as-claude.sh`
  - Create: `scripts/copilot-as-claude/copilot-as-claude_test.sh`
  - Create: `scripts/copilot-as-claude/README.md`
  - Modify: `docs/custom-providers.md`
  - Modify: `README.md`
  - Modify: `CLAUDE.md`
- Related patterns:
  - `scripts/codex-as-claude/`
  - `scripts/gemini-as-claude/`
  - `scripts/opencode/`
  - `pkg/config/defaults/prompts/review_first.txt`
  - `pkg/config/defaults/prompts/review_second.txt`
  - `docs/plans/completed/2026-03-13-gemini-as-claude.md`
- Dependencies:
  - `copilot` CLI (`@github/copilot`)
  - `jq`
  - Copilot authentication via existing login state or `COPILOT_GITHUB_TOKEN` / `GH_TOKEN` / `GITHUB_TOKEN`
- Existing integration path:
  - `claude_command` / `claude_args` already supports alternative CLIs that emit Claude-compatible stream-json, so this should be implemented as a wrapper script, not new executor/config plumbing
- Key differences from current wrappers:
  - Copilot has native non-interactive mode, native JSONL output, and its own permission model (`--allow-all`, `--allow-tool`, `--no-ask-user`)
  - Review prompts are written in Claude-specific “Task tool” language, so the wrapper may need a review adapter even though Copilot supports agentic workflows
- Out of scope unless implementation proves a hard blocker:
  - `pkg/executor/*`
  - `pkg/config/*`
  - `Dockerfile`
  - `scripts/internal/init-docker.sh`

## Development Approach
- **Testing approach**: Regular (wrapper first, then shell tests)
- Complete each task fully before moving to the next
- Reuse Copilot’s native flags and environment variables where possible; do not add wrapper-specific config unless the native CLI is missing a needed control
- Pass the prompt to Copilot via stdin after wrapper parsing, not as a long CLI argument, to preserve the repo’s existing long-prompt / Windows safety pattern
- Keep scope to alternative-provider support for task/review phases only; external review already has a `custom_review_script` path if someone wants Copilot there later
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Add the Copilot wrapper and its shell test harness

**Files:**
- Create: `scripts/copilot-as-claude/copilot-as-claude.sh`
- Create: `scripts/copilot-as-claude/copilot-as-claude_test.sh`

- [ ] Create `scripts/copilot-as-claude/copilot-as-claude.sh` following the existing wrapper layout and header-comment conventions
- [ ] Read the prompt from stdin with `-p` fallback, and ignore unknown Claude flags gracefully so the wrapper works under existing `claude_args`
- [ ] Validate that `copilot` and `jq` are available, with clear non-zero failures when they are missing
- [ ] Pass the final prompt to `copilot` via stdin rather than `copilot -p "$prompt"` to avoid reintroducing command-line length problems inside the wrapper
- [ ] Invoke Copilot in non-interactive JSONL mode with flags suitable for autonomous ralphex runs: `-s`, `--output-format json`, `--stream on`, `--no-ask-user`, and one explicit permission strategy aligned with current ralphex autonomy expectations
- [ ] Translate Copilot JSONL text/completion events into Claude-compatible `content_block_delta` and `result` events, preserving `<<<RALPHEX:...>>>` signals verbatim
- [ ] Capture stderr and emit it back into the stream so existing error/limit pattern detection continues to work
- [ ] Preserve Copilot exit codes, emit a fallback `result` event when Copilot exits without an explicit terminal event, and forward SIGTERM to the Copilot child process
- [ ] Detect review prompts (`<<<RALPHEX:REVIEW_DONE>>>`) and prepend a Copilot-specific adapter that translates the Claude “Task tool” instructions used in `review_first.txt` and `review_second.txt` into Copilot-compatible agent/subagent behavior
- [ ] Create `scripts/copilot-as-claude/copilot-as-claude_test.sh` with a mock `copilot` binary that captures argv and stdin and emits configurable stdout, stderr, and exit codes
- [ ] Cover stdin prompt handling, ignored flags, signal passthrough, stderr passthrough, valid JSON translation, fallback `result`, non-zero exit codes, SIGTERM forwarding, review-adapter injection, and permission/JSON flag construction
- [ ] Run `bash scripts/copilot-as-claude/copilot-as-claude_test.sh`
- [ ] Run the relevant test suite for this task and confirm it passes before starting task 2

### Task 2: Document Copilot provider setup

**Files:**
- Create: `scripts/copilot-as-claude/README.md`
- Modify: `docs/custom-providers.md`
- Modify: `README.md`
- Modify: `CLAUDE.md`

- [ ] Create `scripts/copilot-as-claude/README.md` with setup instructions, config snippet, requirements, auth options, native Copilot env vars, and the wrapper test command
- [ ] Add a Copilot section to `docs/custom-providers.md` explaining why this wrapper uses Copilot JSONL mode, how it handles permissions, and how it differs from the Codex, Gemini, and OpenCode wrappers
- [ ] Update `README.md` in the alternative-provider section to mention the included Copilot wrapper alongside the existing examples
- [ ] Update `CLAUDE.md` script inventory if the new `scripts/copilot-as-claude/` directory should be reflected there
- [ ] Add or update tests covering any documentation-linked command snippets or validation scripts used by this task
- [ ] Run the relevant test suite for this task and confirm it passes before starting task 3

### Task 3: Verify acceptance criteria

**Files:**
- Verify: `scripts/copilot-as-claude/copilot-as-claude.sh`
- Verify: `scripts/copilot-as-claude/copilot-as-claude_test.sh`
- Verify: `docs/custom-providers.md`
- Verify: `README.md`
- Verify: `CLAUDE.md`

- [ ] Run `bash scripts/copilot-as-claude/copilot-as-claude_test.sh`
- [ ] Run full project test suite: `make test`
- [ ] Run linter: `make lint`
- [ ] Verify `scripts/copilot-as-claude/copilot-as-claude.sh` and `scripts/copilot-as-claude/copilot-as-claude_test.sh` are executable
- [ ] Verify test coverage meets 80%+

### Task 4: Update documentation

- [ ] Confirm `README.md`, `docs/custom-providers.md`, and `CLAUDE.md` all use the final wrapper path and naming consistently
- [ ] Move this plan to `docs/plans/completed/`
---
