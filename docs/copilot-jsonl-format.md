# Copilot CLI JSONL Output Format Reference

Reference for the JSONL event schema emitted by `copilot --output-format json -p "..."`.
Based on captured fixtures in `pkg/executor/testdata/copilot_fixtures/`:

- `simple_text.jsonl` — Claude model, 36 lines, 5 turns. Despite the name, contains tool use (the model chose to use tools even for a simple prompt). Covers: reasoning, message deltas, tool execution, multi-turn flow
- `simple_text_gpt.jsonl` — GPT model (gpt-4.1), 10 lines, text-only (no reasoning, no tool use)
- `tool_use.jsonl` — Claude model, 118 lines, 6 turns, 7 tool invocations (view, edit, create, bash). Captured with `--allow-all`
- `with_signal.jsonl` — Claude model, 24 lines. Contains `<<<RALPHEX:COMPLETED>>>` signal in message content
- `error_exit_notes.md` — documents CLI error behavior (no JSONL produced for CLI argument errors)

## Common Envelope

Every line is a JSON object with these top-level keys:

| Key | Type | Required | Description |
|-----|------|----------|-------------|
| `type` | string | yes | Event type identifier (e.g. `assistant.message_delta`) |
| `data` | object | yes (except `result`) | Event-specific payload |
| `id` | string | yes (except `result`) | Unique event ID (UUID) |
| `timestamp` | string | yes | ISO 8601 timestamp with milliseconds |
| `parentId` | string | yes (except `result`) | ID of the parent event in the event tree |
| `ephemeral` | bool | no | If `true`, event is transient (deltas, reasoning). Absent when false |

The `result` event is an exception: it has `type`, `timestamp`, `sessionId`, `exitCode`, and `usage` at top level (no `data`, `id`, or `parentId`).

## Event Types

### user.message

Emitted once at session start. Contains the user prompt and metadata.

```json
{
  "type": "user.message",
  "data": {
    "content": "Write a hello world function in Go",
    "transformedContent": "<current_datetime>...</current_datetime>\n\nWrite a hello world function in Go\n\n<reminder>...</reminder>",
    "source": "user",
    "attachments": [],
    "interactionId": "1ac5d70d-6020-4e33-a7c5-96fe8acdca52"
  },
  "id": "...",
  "timestamp": "2026-03-11T12:48:52.770Z",
  "parentId": "..."
}
```

- `data.content` — original user prompt
- `data.transformedContent` — prompt with system context injected
- `data.interactionId` — session interaction ID (shared across all events in this interaction)

### assistant.turn_start

Marks the beginning of an assistant turn. Multiple turns per session are common (one per tool-use cycle).

```json
{
  "type": "assistant.turn_start",
  "data": {
    "turnId": "0",
    "interactionId": "1ac5d70d-6020-4e33-a7c5-96fe8acdca52"
  },
  "id": "...",
  "timestamp": "2026-03-11T12:48:52.774Z",
  "parentId": "..."
}
```

- `data.turnId` — sequential turn number as string ("0", "1", "2", ...)
- `data.interactionId` — session interaction ID (shared across all events in this interaction)

### assistant.reasoning_delta

Streaming reasoning token (Claude models only, not emitted by GPT models). Ephemeral.

```json
{
  "type": "assistant.reasoning_delta",
  "data": {
    "reasoningId": "4f90c040-8fd4-468a-ade7-1dc9bde53300",
    "deltaContent": "The user is asking me to write a"
  },
  "id": "...",
  "timestamp": "2026-03-11T12:48:55.224Z",
  "parentId": "...",
  "ephemeral": true
}
```

- `data.deltaContent` — incremental reasoning text fragment
- `data.reasoningId` — groups all reasoning deltas for the same reasoning block

### assistant.reasoning

Complete reasoning block (Claude models only). Ephemeral. Emitted after `assistant.message`, not immediately after reasoning_delta events (see Streaming Model section for exact ordering).

```json
{
  "type": "assistant.reasoning",
  "data": {
    "reasoningId": "4f90c040-8fd4-468a-ade7-1dc9bde53300",
    "content": "The user is asking me to write a \"hello world\" function in Go..."
  },
  "id": "...",
  "timestamp": "2026-03-11T12:48:57.275Z",
  "parentId": "...",
  "ephemeral": true
}
```

- `data.content` — full assembled reasoning text

### assistant.message_delta

Streaming assistant text token. Ephemeral. This is the primary event for real-time text output.

```json
{
  "type": "assistant.message_delta",
  "data": {
    "messageId": "7f75806d-cfcf-4aa5-bdb9-064ef26cbed7",
    "deltaContent": "I'll create a simple hello"
  },
  "id": "...",
  "timestamp": "2026-03-11T12:48:56.648Z",
  "parentId": "...",
  "ephemeral": true
}
```

- `data.deltaContent` — incremental text fragment
- `data.messageId` — groups all deltas for the same message

### assistant.message

Complete assistant message. Contains full text and any tool call requests.

```json
{
  "type": "assistant.message",
  "data": {
    "messageId": "7f75806d-cfcf-4aa5-bdb9-064ef26cbed7",
    "content": "I'll create a simple hello world function in Go for you.",
    "toolRequests": [
      {
        "toolCallId": "tooluse_XJpJgFSXrwB0zJRtrU8hlb",
        "name": "create",
        "arguments": {
          "path": "/path/to/file.go",
          "file_text": "package main\n..."
        },
        "type": "function"
      }
    ],
    "interactionId": "1ac5d70d-6020-4e33-a7c5-96fe8acdca52",
    "reasoningOpaque": "WLYh2laS4S9a...",
    "reasoningText": "The user is asking me to write...",
    "outputTokens": 323
  },
  "id": "...",
  "timestamp": "2026-03-11T12:48:57.274Z",
  "parentId": "..."
}
```

- `data.content` — full assembled assistant text (may be empty string when only tool calls)
- `data.toolRequests` — array of tool calls (empty array `[]` when none)
- `data.toolRequests[].toolCallId` — unique ID linking to tool.execution_start/complete
- `data.toolRequests[].name` — tool name: `create`, `edit`, `view`, `bash`, `report_intent`
- `data.toolRequests[].arguments` — tool-specific arguments object
- `data.toolRequests[].type` — always `"function"`
- `data.reasoningOpaque` — opaque encrypted reasoning blob (Claude only, not present on GPT; only present on first turn's `assistant.message`, absent on subsequent turns)
- `data.reasoningText` — plaintext reasoning (Claude only, not present on GPT; only present on first turn's `assistant.message`, absent on subsequent turns)
- `data.outputTokens` — token count for this message
- `data.interactionId` — shared interaction ID

### tool.execution_start

Emitted when a tool begins executing.

```json
{
  "type": "tool.execution_start",
  "data": {
    "toolCallId": "tooluse_XJpJgFSXrwB0zJRtrU8hlb",
    "toolName": "create",
    "arguments": {
      "path": "/path/to/file.go",
      "file_text": "package main\n..."
    }
  },
  "id": "...",
  "timestamp": "2026-03-11T12:48:57.275Z",
  "parentId": "..."
}
```

- `data.toolCallId` — links to `assistant.message.toolRequests[].toolCallId`
- `data.toolName` — tool identifier: `bash`, `create`, `edit`, `view`, `report_intent`
- `data.arguments` — tool-specific arguments (same as in toolRequests)

Tool argument structures:
- **bash**: `{command: string, description: string}`
- **create**: `{path: string, file_text: string}`
- **edit**: `{path: string, old_str: string, new_str: string}`
- **view**: `{path: string}`
- **report_intent**: `{intent: string}`

### tool.execution_complete

Emitted when a tool finishes (success or failure).

Success case (`success: true` — `result` present, no `error`):

```json
{
  "type": "tool.execution_complete",
  "data": {
    "toolCallId": "tooluse_BwDA71Ng1bBTOf3MYpxAU4",
    "model": "claude-haiku-4.5",
    "interactionId": "1ac5d70d-6020-4e33-a7c5-96fe8acdca52",
    "success": true,
    "result": {
      "content": "short summary of result",
      "detailedContent": "full output or diff"
    },
    "toolTelemetry": {
      "properties": {
        "command": "create",
        "fileExtension": "[\".go\"]"
      },
      "metrics": {
        "resultLength": 76,
        "linesAdded": 23,
        "linesRemoved": 0
      },
      "restrictedProperties": {}
    }
  },
  "id": "...",
  "timestamp": "2026-03-11T12:52:01.572Z",
  "parentId": "..."
}
```

Failure case (`success: false` — `error` present, no `result`):

```json
{
  "type": "tool.execution_complete",
  "data": {
    "toolCallId": "tooluse_XJpJgFSXrwB0zJRtrU8hlb",
    "model": "claude-haiku-4.5",
    "interactionId": "1ac5d70d-6020-4e33-a7c5-96fe8acdca52",
    "success": false,
    "error": {
      "message": "Permission denied and could not request permission from user",
      "code": "denied"
    },
    "toolTelemetry": {}
  },
  "id": "...",
  "timestamp": "2026-03-11T12:48:57.279Z",
  "parentId": "..."
}
```

- `data.success` — boolean, whether tool completed successfully
- `data.result` — present on success only; `.content` is short summary, `.detailedContent` is full output/diff
- `data.error` — present on failure only; `.message` is error text, `.code` is error type (e.g. `"denied"`)
- `result` and `error` are mutually exclusive — exactly one is present depending on `success`
- `data.model` — model used for tool execution sandbox (e.g. `"claude-haiku-4.5"`)
- `data.toolTelemetry` — optional telemetry with `.properties`, `.metrics`, and `.restrictedProperties`
- `data.toolTelemetry.metrics.linesAdded` / `.linesRemoved` — change metrics for file operations
- `data.toolTelemetry.restrictedProperties` — may contain `filePaths` for some tool types (observed for `edit`); for `create`, `filePaths` appears in `.properties` instead

Bash tool results include exit code marker in content: `<exited with exit code N>` appended to output.

### session.info

Ephemeral informational event for file operations and session state.

```json
{
  "type": "session.info",
  "data": {
    "infoType": "file_created",
    "message": "/private/tmp/copilot-discovery/main_test.go"
  },
  "id": "...",
  "timestamp": "2026-03-11T12:52:01.571Z",
  "parentId": "...",
  "ephemeral": true
}
```

- `data.infoType` — info category (observed: `"file_created"`)
- `data.message` — associated information (file path for file_created)

### assistant.turn_end

Marks the end of an assistant turn.

```json
{
  "type": "assistant.turn_end",
  "data": {
    "turnId": "0"
  },
  "id": "...",
  "timestamp": "2026-03-11T12:48:57.281Z",
  "parentId": "..."
}
```

### result

Final event in every session. Unique envelope structure (no `data`, `id`, or `parentId`).

```json
{
  "type": "result",
  "timestamp": "2026-03-11T12:49:14.681Z",
  "sessionId": "d24ecec2-ddf1-425e-9d03-39a21a1bccf6",
  "exitCode": 0,
  "usage": {
    "premiumRequests": 1,
    "totalApiDurationMs": 20861,
    "sessionDurationMs": 25301,
    "codeChanges": {
      "linesAdded": 0,
      "linesRemoved": 0,
      "filesModified": []
    }
  }
}
```

- `exitCode` — 0 for success, non-zero for failure
- `sessionId` — unique session identifier
- `usage.premiumRequests` — number of premium API requests made
- `usage.totalApiDurationMs` — total time spent in API calls
- `usage.sessionDurationMs` — total wall-clock session time
- `usage.codeChanges` — summary of file modifications. Note: `filesModified` may only include edited files, not newly created ones (e.g. files created via `create` tool are excluded). Use `tool.execution_complete` events with `create` tool to track file creations

## Streaming Model

Copilot emits incremental text deltas, similar to Claude Code's `content_block_delta`:

1. Events arrive incrementally with ~20ms gaps between consecutive deltas
2. Order within each turn:
   - `assistant.turn_start`
   - `assistant.reasoning_delta` * (Claude only, ephemeral, typically first turn only)
   - `assistant.message_delta` * (ephemeral, carries streaming text)
   - `assistant.message` (complete message with full text + tool requests)
   - `assistant.reasoning` (Claude only, ephemeral, complete reasoning, typically first turn only)
   - `tool.execution_start` * (one per tool call)
   - `session.info` * (ephemeral, emitted for file operations between tool start/complete)
   - `tool.execution_complete` * (one per tool call)
   - `assistant.turn_end`
3. Multiple turns per session when tools are used (turn N ends, turn N+1 starts)
4. Session ends with a single `result` event after the final `assistant.turn_end`

Multiple tool calls can execute in parallel within the same turn (multiple `tool.execution_start` events before their corresponding `tool.execution_complete` events).

## Model Differences

| Feature | Claude models | GPT models |
|---------|--------------|------------|
| `assistant.reasoning_delta` | Yes (ephemeral) | Not emitted |
| `assistant.reasoning` | Yes (ephemeral) | Not emitted |
| `assistant.message.reasoningOpaque` | Present | Not present |
| `assistant.message.reasoningText` | Present | Not present |
| Tool use | Full (create, edit, view, bash, report_intent) | Observed: text-only responses (no tool use in simple prompts) |
| Typical event count | 10-120 lines per session (varies by task complexity) | 8-12 lines per session |

Both models share the same event types for messages, turns, and results. The JSONL structure is identical — GPT simply omits reasoning-related events and fields. Tool-use event structure for GPT models is unverified (no GPT tool-use fixture was captured).

Note: The GPT fixture (`simple_text_gpt.jsonl`) was captured with `gpt-4.1` — `gpt-5.2-codex` was not available at discovery time. Tool-use behavior for `gpt-5.2-codex` may differ. The GPT fixture also has truncated streaming deltas (only the first few `assistant.message_delta` events were captured; the complete `assistant.message` is present).

## Exit Code Behavior

| Exit code | Meaning |
|-----------|---------|
| 0 | Success — session completed normally |
| 1 | CLI error — invalid arguments, authentication failure. **No JSONL output**, error goes to stderr as plain text |
| Non-zero (runtime) | Session failure — may emit partial JSONL with `result.exitCode != 0` |

CLI argument validation errors (e.g. invalid `--model`) produce NO JSONL events at all. The error message goes to stderr as plain text. Example:
```
error: option '--model <model>' argument 'nonexistent-model' is invalid. Allowed choices are claude-sonnet-4.6, ...
```

## Session Metadata

The `result` event provides useful session metadata:

- `usage.premiumRequests` — tracks API usage (observed as 1 in all test fixtures; may differ for longer sessions)
- `usage.totalApiDurationMs` — API latency for cost/performance monitoring
- `usage.sessionDurationMs` — total wall time including tool execution
- `usage.codeChanges` — summary of file changes (linesAdded, linesRemoved, filesModified[])
- `sessionId` — unique identifier, useful for logging and debugging

For ralphex, `sessionDurationMs` and `codeChanges` may be useful for progress reporting. Token counts are not directly exposed (only `outputTokens` per message).

## Mapping to ralphex

This section specifies exactly how a copilot JSONL parser should map events to ralphex's needs.

### Required CLI Flags

- `--output-format json` — emit JSONL to stdout (required for parser)
- `--no-ask-user` — disable interactive prompts (required for non-interactive/programmatic use; without this, copilot may block waiting for user input)
- `--allow-all` — auto-approve tool execution (recommended; without this, tools are denied with `error.code: "denied"`)

### Text Streaming → OutputHandler

Read `assistant.message_delta` events for real-time text output:
- Field: `data.deltaContent`
- Accumulate deltas with matching `data.messageId` for the current message
- Ignore `assistant.reasoning_delta` (ephemeral internal reasoning, not user-facing output)
- IMPORTANT: `assistant.message_delta` events may be absent even when the turn has text content (observed in tool-heavy turns where the model proceeds directly to tool calls — see `simple_text.jsonl` turnIds 2-4). Always read `assistant.message.data.content` as the authoritative source for complete turn text, not just a fallback

### Signal Detection → scan for <<<RALPHEX:...>>>

Scan `assistant.message_delta.data.deltaContent` during streaming for signal patterns.
Signals pass through verbatim — no escaping or mangling.

For reliable detection, also scan `assistant.message.data.content` (the complete message) as the authoritative check, since delta boundaries may split a signal across multiple events and `message_delta` events may be absent on some turns (see Edge Cases). Note: `assistant.message.data.content` may be an empty string on tool-only turns (no text output).

Signal patterns to detect: `<<<RALPHEX:COMPLETED>>>`, `<<<RALPHEX:FAILED>>>`, `<<<RALPHEX:REVIEW_DONE>>>`, `<<<RALPHEX:QUESTION>>>`, `<<<RALPHEX:PLAN_DRAFT>>>`, `<<<RALPHEX:PLAN_READY>>>`

### Error/Limit Pattern Detection

Scan `assistant.message.data.content` for configured error and limit patterns (e.g. "Rate limit", "quota exceeded"). These are expected to surface as regular text in assistant messages, not as dedicated error events (UNVERIFIED — no rate limit was triggered during discovery; actual behavior should be verified during migration implementation).

Also check `tool.execution_complete.data.error.message` for tool-level errors.

For CLI-level errors (exit code 1, no JSONL): detect by checking if the process exited with non-zero code and no JSONL was produced. Read stderr for the error message.

### Completion → detect session end

The `result` event signals end of session:
- Parse every line; when `type == "result"`, the session is done
- Check `exitCode` for success (0) vs failure (non-zero)
- If the process exits without emitting a `result` event, treat as abnormal termination

### Tool Activity Tracking

To track what copilot is doing (for progress display):
- `tool.execution_start` — tool name and arguments (show "editing file.go", "running tests", etc.)
- `tool.execution_complete` — success/failure status
- `assistant.turn_start` / `assistant.turn_end` — turn boundaries for iteration counting

## Edge Cases

### --silent flag behavior

The `--silent` (`-s`) flag does NOT change the JSONL output structure when combined with `--output-format json`. Both modes produce identical event types and fields. `--silent` only affects plain-text output mode (suppresses stats and decoration).

For ralphex: no special handling needed — always use `--output-format json` regardless of `--silent`.

### --allow-all vs tool permissions

Without `--allow-all`, tools that require approval emit `tool.execution_complete` with:
- `success: false`
- `error.message: "Permission denied and could not request permission from user"`
- `error.code: "denied"`

Permissions are per-tool and per-command, not all-or-nothing. In testing, read-only `bash` commands (e.g., `ls -la`) succeeded without `--allow-all`, while `create` and write-mode `bash` commands (e.g., `mkdir`, `cat >`) were denied. No special "approval" or "permission" event types exist. The JSONL structure is identical — only the `success` and `error` fields on `tool.execution_complete` differ.

For ralphex: always pass `--allow-all` in non-interactive mode. If tool denials appear, detect via `tool.execution_complete.data.error.code == "denied"`. Note: only `--allow-all` was verified during discovery; check `copilot --help` for the canonical flag name.

### Unicode and special characters

Unicode characters (emoji, CJK, mathematical symbols) pass through JSONL correctly with standard JSON encoding. No special escaping or mangling observed. Characters appear verbatim in `assistant.message_delta.data.deltaContent` and `assistant.message.data.content`.

### Empty or near-empty responses

When the model produces minimal output, the event sequence remains structurally identical:
- `user.message` → `assistant.turn_start` → (optional reasoning) → (optional `assistant.message_delta`(s)) → `assistant.message` → `assistant.turn_end` → `result`
- `assistant.message_delta` events may be absent even when `assistant.message.data.content` is non-empty (observed in tool-heavy turns where the model proceeds directly to tool calls without streaming deltas — see `simple_text.jsonl` turnIds 2-4)
- The `assistant.message.data.content` may be an empty string `""` (observed when only tool calls are made with no text)
- Zero-delta messages are valid — `assistant.message` is always emitted even with empty content
- For parsing: treat `assistant.message.data.content` as the authoritative text source, not just a fallback for deltas

### CLI argument errors (no JSONL)

Invalid CLI arguments (bad `--model`, unknown flags) produce NO JSONL output at all:
- Error goes to stderr as plain text
- Exit code is 1
- stdout is empty

For ralphex: detect by checking process exit code AND whether any JSONL was received. If exit code != 0 and no events parsed, read stderr for error message.

### Multi-turn tool chains

When the model uses multiple tools across turns, each turn follows the same event pattern:
- `assistant.turn_start` (turnId increments: "0", "1", "2", ...)
- reasoning/message deltas and complete message (with toolRequests)
- `tool.execution_start` / `tool.execution_complete` pairs
- `assistant.turn_end`

Observed: up to 12 turns in a single session (without `--allow-all`, many retries due to denials). With `--allow-all`, typically 3-6 turns for complex operations.

Multiple tools can execute within a single turn (parallel tool calls share the same turnId).

### Streaming interruption

If the copilot process is killed mid-stream:
- The `result` event will NOT be emitted
- The last event may be a partial JSON line (truncated)
- For ralphex: treat absence of `result` event as abnormal termination. Use line-by-line JSON parsing that tolerates parse errors on the final line
