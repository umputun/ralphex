# Discover and Document Copilot CLI JSONL Output Format

## Overview

Capture, analyze, and document the JSONL event schema emitted by `copilot --output-format json -p` for programmatic integration. Produce fixture files and a format reference that the migration plan (`migrate-to-copilot-cli.md`) depends on.

## Context

- Files involved:
  - `docs/copilot-jsonl-format.md` (to be created — format reference document)
  - `pkg/executor/testdata/copilot_fixtures/` (to be created — sample JSONL fixtures for tests)
- Related patterns: current Claude stream-json parsing in `pkg/executor/executor.go` (event types: `content_block_delta`, `assistant`, `message_stop`, `result`); current Codex stderr/stdout split in `pkg/executor/codex.go`
- Dependencies: `copilot` CLI installed and authenticated (`gh auth login` or `GITHUB_TOKEN`)

## Development Approach

- **Testing approach**: N/A (research and documentation plan, no code changes)
- This is a prerequisite for `docs/plans/migrate-to-copilot-cli.md` — must complete before Task 1 of that plan
- All captured output goes into version-controlled fixture files for reproducibility
- **CRITICAL: do NOT write any executor code in this plan — only capture, analyze, document**

## Implementation Steps

### Task 1: Capture JSONL output for basic text generation

**Files:**
- Create: `pkg/executor/testdata/copilot_fixtures/simple_text.jsonl`
- Create: `pkg/executor/testdata/copilot_fixtures/` directory

- [x] Run: `copilot --model claude-opus-4-6 --output-format json --no-ask-user -p "Write a hello world function in Go" 2>&1 | tee /tmp/copilot-simple.jsonl` (used default model — claude-opus-4.6 not available on this account; default model produced Claude-based output with reasoning)
- [x] Run same prompt with the review model: `copilot --model gpt-5.2-codex --output-format json --no-ask-user -p "Write a hello world function in Go" 2>&1 | tee /tmp/copilot-simple-codex.jsonl` (used gpt-4.1 — gpt-5.2-codex not available on this account; saved as simple_text_gpt.jsonl)
- [x] Inspect each line with `cat /tmp/copilot-simple.jsonl | head -20` — note whether output goes to stdout, stderr, or both. FINDING: all JSONL goes to stdout only, stderr is empty
- [x] Identify the JSON fields present on each line (keys at top level, nested structures). FINDING: top-level keys are {type, data, id, timestamp, parentId, ephemeral}; data keys vary by type (see observations below)
- [x] Save cleaned fixtures to `pkg/executor/testdata/copilot_fixtures/simple_text.jsonl` (one per model if formats differ). Saved: simple_text.jsonl (Claude, 36 lines) and simple_text_gpt.jsonl (GPT, 10 lines)
- [x] Document initial observations: what fields appear, which lines carry text content, which are metadata. OBSERVATIONS:
  - Event types (Claude): user.message, assistant.turn_start, assistant.reasoning_delta (ephemeral), assistant.reasoning, assistant.message_delta (ephemeral), assistant.message, tool.execution_start, tool.execution_complete, assistant.turn_end, result
  - Event types (GPT/gpt-4.1): user.message, assistant.turn_start, assistant.message_delta (ephemeral), assistant.message, assistant.turn_end, result (NO reasoning events, NO tool use in this simple case)
  - Text content for streaming: assistant.message_delta.data.deltaContent (incremental text deltas, marked ephemeral=true)
  - Complete message: assistant.message.data.content (full assembled text), assistant.message.data.toolRequests (array of tool calls)
  - Session end: result event with exitCode, sessionId, usage stats
  - Reasoning: assistant.reasoning_delta.data.deltaContent (streaming, ephemeral), assistant.reasoning.data.content (complete, Claude-only)
  - Tool calls: tool.execution_start (toolName, arguments), tool.execution_complete (success, error)
  - Metadata: result.usage has premiumRequests, totalApiDurationMs, sessionDurationMs, codeChanges

### Task 2: Capture JSONL output for tool use (file edits, bash commands)

**Files:**
- Create: `pkg/executor/testdata/copilot_fixtures/tool_use.jsonl`

- [ ] Create a temp directory: `mkdir -p /tmp/copilot-discovery && cd /tmp/copilot-discovery && git init && echo "package main" > main.go && git add . && git commit -m "init"`
- [ ] Run from that directory: `copilot --model claude-opus-4-6 --output-format json --allow-all --no-ask-user -p "Add a function called Add(a, b int) int to main.go and write a test for it" 2>&1 | tee /tmp/copilot-tooluse.jsonl`
- [ ] Identify event types for: file reads, file writes/edits, bash command execution, tool approval (if any with --allow-all)
- [ ] Note how tool invocations vs tool results vs assistant text are distinguished in the JSONL
- [ ] Save fixture to `pkg/executor/testdata/copilot_fixtures/tool_use.jsonl`
- [ ] Document tool-related event types and their field structure

### Task 3: Capture JSONL output with signals and error scenarios

**Files:**
- Create: `pkg/executor/testdata/copilot_fixtures/with_signal.jsonl`
- Create: `pkg/executor/testdata/copilot_fixtures/error_exit.jsonl`

- [ ] Run a prompt that will include a signal string in output: `copilot --model claude-opus-4-6 --output-format json --no-ask-user -p "Reply with exactly this text: <<<RALPHEX:COMPLETED>>>" 2>&1 | tee /tmp/copilot-signal.jsonl`
- [ ] Verify the signal string appears in the JSONL text content (not mangled or escaped)
- [ ] Capture an error scenario — run with an invalid model: `copilot --model nonexistent-model --output-format json --no-ask-user -p "hello" 2>&1 | tee /tmp/copilot-error.jsonl`; note exit code with `echo $?`
- [ ] Capture a long-running prompt to observe streaming behavior — confirm events arrive incrementally (not buffered until completion)
- [ ] Save fixtures to `pkg/executor/testdata/copilot_fixtures/`
- [ ] Document: how errors surface in JSONL (error event type? stderr? exit code only?), how signals pass through text content, streaming timing behavior

### Task 4: Analyze and document the complete JSONL schema

**Files:**
- Create: `docs/copilot-jsonl-format.md`

- [ ] Compile all observed event types into a taxonomy (e.g., `assistant_message`, `tool_call`, `tool_result`, `session_end`, `error`, etc.)
- [ ] For each event type, document:
  - Top-level JSON keys and their types
  - Which field(s) contain text content that ralphex should stream to `OutputHandler`
  - Which field(s) indicate completion/end of session
  - Which field(s) carry error information
- [ ] Document the streaming model: does copilot emit incremental text deltas (like Claude's `content_block_delta`) or complete messages?
- [ ] Document differences between models (claude-opus vs gpt-5.2-codex) if any
- [ ] Document exit code behavior: what exit codes mean success, failure, rate limit
- [ ] Document any session metadata events (token counts, session ID, cost) and whether they're useful for ralphex
- [ ] Map each event type to the ralphex parsing need:
  - Text streaming → `OutputHandler` callback
  - Signal detection → scan text content for `<<<RALPHEX:...>>>` patterns
  - Error/limit patterns → scan text content for configured patterns
  - Completion → detect session end event
- [ ] Write `docs/copilot-jsonl-format.md` with the full schema reference, including example JSON for each event type
- [ ] Add a "Mapping to ralphex" section that explicitly specifies: which event type field to read for text, how to detect end-of-stream, how to detect errors

### Task 5: Validate fixtures and document edge cases

**Files:**
- Modify: `docs/copilot-jsonl-format.md` (add edge cases section)
- Verify: `pkg/executor/testdata/copilot_fixtures/*.jsonl` (all fixtures valid)

- [ ] Validate all fixture files are valid JSONL: `for f in pkg/executor/testdata/copilot_fixtures/*.jsonl; do jq empty < "$f" 2>&1 || echo "INVALID: $f"; done`
- [ ] Test with `--allow-all` vs without — does the JSONL format change when tools are auto-approved vs prompted?
- [ ] Test with `--silent` flag — does it affect JSONL output or only text output?
- [ ] Document edge cases: empty responses, very long outputs, multi-turn tool chains, unicode/special characters in output
- [ ] Add edge case findings to `docs/copilot-jsonl-format.md`
- [ ] Verify fixtures are committed and accessible from `pkg/executor/testdata/copilot_fixtures/`
- [ ] Final review: confirm the format doc has enough detail to implement `parseJSONL()` in the migration plan without ambiguity
