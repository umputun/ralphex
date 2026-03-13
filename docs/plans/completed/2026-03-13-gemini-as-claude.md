# Add gemini-as-claude wrapper script

## Overview
Add a `scripts/gemini-as-claude/` directory with a wrapper script that translates Gemini CLI plain-text output into Claude-compatible stream-json format, following the same conventions as `codex-as-claude` and `opencode-as-claude`. Includes test script and README, plus a docs update.

## Context
- Files involved:
  - Create: `scripts/gemini-as-claude/gemini-as-claude.sh`
  - Create: `scripts/gemini-as-claude/gemini-as-claude_test.sh`
  - Create: `scripts/gemini-as-claude/README.md`
  - Modify: `docs/custom-providers.md`
- Related patterns: `scripts/codex-as-claude/`, `scripts/opencode/`
- Key difference: Gemini CLI outputs plain text (not JSONL), so each output line is wrapped as a `content_block_delta` event (simpler translation than codex/opencode)

## Development Approach
- Testing approach: Regular (script first, then tests)
- Complete each task fully before moving to the next
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Create gemini-as-claude.sh

**Files:**
- Create: `scripts/gemini-as-claude/gemini-as-claude.sh`

- [ ] Add shebang, header comment with config example and env var docs (matching codex/opencode style)
- [ ] Verify `jq` and `gemini` are available (exit with error messages if not)
- [ ] Parse `-p` prompt from args, ignore unknown flags gracefully (same `while [[ $# -gt 0 ]]; do case "$1" in` pattern)
- [ ] Error if no prompt provided
- [ ] Support `GEMINI_MODEL` env var (passed as `--model` flag when set)
- [ ] Support `GEMINI_VERBOSE` env var (0/1, with invalid-value warning)
- [ ] Detect review prompts (`<<<RALPHEX:REVIEW_DONE>>>` in prompt) and prepend review adapter text (same pattern as opencode)
- [ ] Create temp dir + stderr capture file; trap cleanup on EXIT; trap SIGTERM forward to gemini child PID
- [ ] Run `gemini` in background, capturing stderr, piping stdout through named FIFO
- [ ] Translate each output line to `content_block_delta` event via `jq -cn --arg text "$line" '...'`
- [ ] After main loop: wait for gemini exit code, emit stderr lines as `content_block_delta` events, emit fallback result event, preserve gemini exit code
- [ ] Make script executable (`chmod +x`)

### Task 2: Create gemini-as-claude_test.sh

**Files:**
- Create: `scripts/gemini-as-claude/gemini-as-claude_test.sh`

- [ ] Create mock `gemini` script (reads `MOCK_STDOUT_FILE`, `MOCK_STDERR_FILE`, `MOCK_EXIT_CODE`, writes args to file)
- [ ] Test: signal passthrough (`<<<RALPHEX:ALL_TASKS_DONE>>>` appears in `content_block_delta` output)
- [ ] Test: REVIEW_DONE signal passthrough
- [ ] Test: FAILED signal passthrough
- [ ] Test: exit code 0 on success
- [ ] Test: exit code 1 preserved on failure
- [ ] Test: non-standard exit code (42) preserved
- [ ] Test: stderr captured and emitted as `content_block_delta` events
- [ ] Test: empty stderr produces no extra events
- [ ] Test: no prompt exits with error and message
- [ ] Test: unknown flags ignored, output produced normally
- [ ] Test: all output lines are valid JSON
- [ ] Test: large prompt (5000+ chars) handled
- [ ] Test: GEMINI_MODEL passed as `--model` flag
- [ ] Test: `--model` omitted when GEMINI_MODEL is empty
- [ ] Test: GEMINI_VERBOSE=1 behavior (if applicable)
- [ ] Test: GEMINI_VERBOSE invalid value produces warning
- [ ] Test: review adapter prepended for review prompts, not for non-review prompts
- [ ] Test: gemini not found exits with error
- [ ] Test: fallback result event emitted when gemini exits without explicit end
- [ ] Test: malformed/non-JSON input handled gracefully (plain text lines become valid JSON events)
- [ ] Test: SIGTERM forwarded to gemini child process
- [ ] Make test script executable; run it: `bash scripts/gemini-as-claude/gemini-as-claude_test.sh`

### Task 3: Create README.md

**Files:**
- Create: `scripts/gemini-as-claude/README.md`

- [ ] Add title and description (drop-in replacement for claude in task and review phases)
- [ ] Add configuration section (claude_command, claude_args =)
- [ ] Add environment variables table (GEMINI_MODEL, GEMINI_VERBOSE)
- [ ] Add requirements section (gemini CLI, jq)
- [ ] Add testing section (`bash scripts/gemini-as-claude/gemini-as-claude_test.sh`)

### Task 4: Update docs/custom-providers.md

**Files:**
- Modify: `docs/custom-providers.md`

- [ ] Add "Gemini CLI wrapper (included example)" section after the OpenCode wrapper section
- [ ] Include setup config snippet, env var table, and brief how-it-works note
- [ ] Note that Gemini outputs plain text (no JSONL translation needed, simpler than codex/opencode)

### Task 5: Verify acceptance criteria

- [ ] Manual test: `bash scripts/gemini-as-claude/gemini-as-claude_test.sh` — all tests pass
- [ ] Verify no Go tests broken: `make test`
- [ ] Run linter: `make lint`
- [ ] Verify both scripts are executable: `ls -la scripts/gemini-as-claude/`

### Task 6: Update documentation

- [ ] Update `CLAUDE.md` if needed (add gemini-as-claude to the scripts/ structure description)
- [ ] Move this plan to `docs/plans/completed/`
