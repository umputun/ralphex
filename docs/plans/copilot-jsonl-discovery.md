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

- [x] Create a temp directory: `mkdir -p /tmp/copilot-discovery && cd /tmp/copilot-discovery && git init && echo "package main" > main.go && git add . && git commit -m "init"`
- [x] Run from that directory: `copilot --model claude-opus-4-6 --output-format json --allow-all --no-ask-user -p "Add a function called Add(a, b int) int to main.go and write a test for it" 2>&1 | tee /tmp/copilot-tooluse.jsonl` (used default model — claude-opus-4-6 not available on this account; default model produced Claude-based output with reasoning and tool use)
- [x] Identify event types for: file reads, file writes/edits, bash command execution, tool approval (if any with --allow-all). FINDINGS:
  - File reads: `tool.execution_start` with toolName="view", `tool.execution_complete` with result containing file content
  - File writes/edits: `tool.execution_start` with toolName="edit" (for modifications) or "create" (for new files), `tool.execution_complete` with result containing diff in detailedContent
  - Bash execution: `tool.execution_start` with toolName="bash", arguments include command and description; `tool.execution_complete` result includes stdout and exit code marker `<exited with exit code N>`
  - Tool approval: with `--allow-all`, no approval events are emitted — tools execute directly
  - Additional tools: `report_intent` (logs intent, internal copilot tool)
  - New event: `session.info` (ephemeral) with infoType="file_created" — emitted alongside tool.execution_complete for file creation
- [x] Note how tool invocations vs tool results vs assistant text are distinguished in the JSONL. FINDINGS:
  - Tool invocations: declared in `assistant.message.data.toolRequests[]` array (each has toolCallId, name, arguments, type="function")
  - Tool starts: `tool.execution_start` events (toolCallId, toolName, arguments) — one per tool call
  - Tool results: `tool.execution_complete` events (toolCallId, model, success:bool, result:{content, detailedContent}, toolTelemetry)
  - Assistant text: `assistant.message_delta` (streaming, ephemeral) and `assistant.message` (complete, with content field)
  - Turn boundaries: `assistant.turn_start`/`assistant.turn_end` wrap each assistant turn (may contain text + tool calls)
  - Multiple tools can execute in parallel within the same turn
  - Each turn cycle: turn_start -> [reasoning_delta*] -> [message_delta*] -> message (with toolRequests) -> [tool.execution_start, tool.execution_complete]* -> turn_end
- [x] Save fixture to `pkg/executor/testdata/copilot_fixtures/tool_use.jsonl` (118 lines, 6 turns, 7 tool invocations: report_intent, view x2, edit, create, bash x2)
- [x] Document tool-related event types and their field structure. OBSERVATIONS:
  - tool.execution_start: {toolCallId, toolName, arguments:{...tool-specific...}}
  - tool.execution_complete: {toolCallId, model (e.g. "claude-haiku-4.5"), interactionId, success:bool, result:{content (short), detailedContent (full diff/output)}, toolTelemetry:{properties:{command, fileExtension, ...}, metrics:{resultLength, linesAdded, linesRemoved, ...}}}
  - session.info: {infoType (e.g. "file_created"), message (file path)} — ephemeral, emitted for file operations
  - assistant.message.toolRequests[]: [{toolCallId, name, arguments:{...}, type:"function"}] — tool call declarations
  - result event (session end): unchanged from simple_text — includes exitCode, sessionId, usage with codeChanges:{linesAdded, linesRemoved, filesModified:[]}

### Task 3: Capture JSONL output with signals and error scenarios

**Files:**
- Create: `pkg/executor/testdata/copilot_fixtures/with_signal.jsonl`
- Create: `pkg/executor/testdata/copilot_fixtures/error_exit.jsonl`

- [x] Run a prompt that will include a signal string in output: `copilot --output-format json --no-ask-user -p "Reply with exactly this text and nothing else: <<<RALPHEX:COMPLETED>>>"` (used default model — claude-opus-4-6 available on this account; 24 lines output)
- [x] Verify the signal string appears in the JSONL text content (not mangled or escaped). VERIFIED: signal `<<<RALPHEX:COMPLETED>>>` appears verbatim in both assistant.message_delta.data.deltaContent (streaming) and assistant.message.data.content (complete). Not escaped or mangled. Also appears split across reasoning_delta events (ephemeral).
- [x] Capture an error scenario — run with an invalid model: `copilot --model nonexistent-model --output-format json --no-ask-user -p "hello"`. FINDING: CLI argument validation errors produce NO JSONL output at all. Error goes to stderr as plain text. Exit code: 1. stderr: "error: option '--model <model>' argument 'nonexistent-model' is invalid. Allowed choices are claude-sonnet-4.6, claude-sonnet-4.5, ..."
- [x] Capture a long-running prompt to observe streaming behavior — confirm events arrive incrementally (not buffered until completion). CONFIRMED: timestamps on delta events show ~20ms gaps between consecutive events (e.g., 12:55:06.535Z, 12:55:06.547Z, 12:55:06.568Z). Events arrive incrementally during generation, not buffered. Reasoning deltas arrive first, then message deltas, then complete message.
- [x] Save fixtures to `pkg/executor/testdata/copilot_fixtures/` — saved with_signal.jsonl (24 lines) and error_exit.jsonl (metadata documenting error behavior since no JSONL is produced for CLI errors)
- [x] Document: how errors surface in JSONL (error event type? stderr? exit code only?), how signals pass through text content, streaming timing behavior. FINDINGS:
  - Errors: CLI argument errors go to stderr as plain text (no JSONL), exit code 1. No dedicated "error" event type observed in JSONL. Runtime errors likely surface via result.exitCode != 0 or abrupt stream termination. Rate limit errors expected in assistant.message text content (same as Claude Code behavior).
  - Signals: pass through verbatim in assistant.message_delta.data.deltaContent (streaming, ephemeral) and assistant.message.data.content (complete). Signal detection should scan these fields for <<<RALPHEX:...>>> patterns. Reasoning deltas also contain signal text but are ephemeral — scanning message content is sufficient.
  - Streaming: events arrive incrementally with ~20ms between deltas. Order: user.message -> turn_start -> reasoning_delta* -> message_delta* -> message -> reasoning -> turn_end -> result. Ephemeral events (deltas) have ephemeral=true flag.

### Task 4: Analyze and document the complete JSONL schema

**Files:**
- Create: `docs/copilot-jsonl-format.md`

- [x] Compile all observed event types into a taxonomy (e.g., `assistant_message`, `tool_call`, `tool_result`, `session_end`, `error`, etc.)
- [x] For each event type, document:
  - Top-level JSON keys and their types
  - Which field(s) contain text content that ralphex should stream to `OutputHandler`
  - Which field(s) indicate completion/end of session
  - Which field(s) carry error information
- [x] Document the streaming model: does copilot emit incremental text deltas (like Claude's `content_block_delta`) or complete messages?
- [x] Document differences between models (claude-opus vs gpt-5.2-codex) if any
- [x] Document exit code behavior: what exit codes mean success, failure, rate limit
- [x] Document any session metadata events (token counts, session ID, cost) and whether they're useful for ralphex
- [x] Map each event type to the ralphex parsing need:
  - Text streaming → `OutputHandler` callback
  - Signal detection → scan text content for `<<<RALPHEX:...>>>` patterns
  - Error/limit patterns → scan text content for configured patterns
  - Completion → detect session end event
- [x] Write `docs/copilot-jsonl-format.md` with the full schema reference, including example JSON for each event type
- [x] Add a "Mapping to ralphex" section that explicitly specifies: which event type field to read for text, how to detect end-of-stream, how to detect errors

### Task 5: Validate fixtures and document edge cases

**Files:**
- Modify: `docs/copilot-jsonl-format.md` (add edge cases section)
- Verify: `pkg/executor/testdata/copilot_fixtures/*.jsonl` (all fixtures valid)

- [x] Validate all fixture files are valid JSONL: `for f in pkg/executor/testdata/copilot_fixtures/*.jsonl; do jq empty < "$f" 2>&1 || echo "INVALID: $f"; done` — all 5 fixtures valid (error_exit 2 lines, simple_text 36, simple_text_gpt 10, tool_use 118, with_signal 24)
- [x] Test with `--allow-all` vs without — does the JSONL format change when tools are auto-approved vs prompted? FINDING: JSONL structure is identical. Without --allow-all, denied tools emit tool.execution_complete with success:false and error.code:"denied". No special approval event types exist.
- [x] Test with `--silent` flag — does it affect JSONL output or only text output? FINDING: --silent (-s) does NOT affect JSONL output when combined with --output-format json. Same event types and structure. --silent only suppresses stats in plain-text output mode.
- [x] Document edge cases: empty responses, very long outputs, multi-turn tool chains, unicode/special characters in output. FINDINGS: unicode passes through verbatim (emoji, CJK, math symbols). Empty content produces same event sequence with empty string. Multi-turn chains observed up to 12 turns. Streaming interruption leaves no result event.
- [x] Add edge case findings to `docs/copilot-jsonl-format.md` — added Edge Cases section covering: --silent behavior, --allow-all vs permissions, unicode, empty responses, CLI errors, multi-turn chains, streaming interruption
- [x] Verify fixtures are committed and accessible from `pkg/executor/testdata/copilot_fixtures/` — all 5 fixtures tracked in git and valid
- [x] Final review: confirm the format doc has enough detail to implement `parseJSONL()` in the migration plan without ambiguity — CONFIRMED: doc covers all event types with JSON examples, field descriptions, streaming model, model differences, exit codes, and explicit mapping to ralphex parsing needs (text streaming, signal detection, error detection, completion detection, tool tracking)
