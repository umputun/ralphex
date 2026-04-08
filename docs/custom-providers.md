# Custom Providers for Claude Phases

ralphex uses Claude Code as the primary agent for task execution and code reviews. The `claude_command` and `claude_args` configuration options allow replacing Claude Code with any CLI tool that produces compatible output — codex, Gemini CLI, local LLMs, or custom scripts.

## How it works

ralphex's `ClaudeExecutor` runs the configured command and passes the prompt via stdin, then reads stdout as a stream of JSON events. Each line must be a valid JSON object. The executor recognizes these event types:

| Event type | Fields used | Purpose |
|---|---|---|
| `content_block_delta` | `delta.type` ("text_delta"), `delta.text` | Streaming text output |
| `result` | `result` (string or `{"output": "..."}`) | End of execution |
| `assistant` | `message.content[].text` | Full message (alternative to streaming) |
| `message_stop` | `message.content[].text` | Final message (same structure as `assistant`) |

The executor also recognizes `message_stop` events, but wrapper scripts don't need to emit these — they are internal to Claude Code. The minimum viable wrapper produces `content_block_delta` events for text and a `result` event at the end.

### Signal detection

ralphex prompts instruct the agent to emit signals like `<<<RALPHEX:COMPLETED>>>` or `<<<RALPHEX:FAILED>>>` in its output. These signals must appear in the text content of `content_block_delta` or `result` events. The wrapper doesn't need to handle signals — as long as the underlying tool follows the prompt instructions and the text passes through, signals will be detected automatically.

### Argument handling

`ClaudeExecutor` builds the command as:

```
<claude_command> <claude_args...> --print
```

The prompt is passed via stdin (not as a CLI argument). This avoids the cmd.exe 8191-character command-line limit on Windows, where large prompts (e.g., after variable expansion) can exceed the limit.

When `claude_args` has a value (default: `--dangerously-skip-permissions --output-format stream-json --verbose`), those flags are split and passed as arguments. Note that setting `claude_args =` (empty) in the config file may not clear the default due to config fallback behavior — the embedded default value is preserved when the user-specified value is empty.

**Wrapper scripts should accept the prompt via stdin** and also accept `-p <prompt>` for backward compatibility. Use `[[ ! -t 0 ]]` to detect non-interactive stdin before reading. **Wrapper scripts should also ignore unknown flags gracefully** — use a catch-all `*) shift ;;` in the argument parser.

## Codex wrapper (included example)

The repository includes a working wrapper at `scripts/codex-as-claude/codex-as-claude.sh` that translates codex JSONL events to Claude stream-json format.

### Setup

```ini
# in ~/.config/ralphex/config or .ralphex/config
claude_command = /path/to/scripts/codex-as-claude/codex-as-claude.sh
claude_args =
```

### Environment variables

| Variable | Default | Description |
|---|---|---|
| `CODEX_MODEL` | (codex default) | Model to use with codex |
| `CODEX_SANDBOX` | `danger-full-access` | Sandbox mode for codex |
| `CODEX_VERBOSE` | `0` | Set to `1` to include command execution output (file reads, shell commands) |

### Event translation

The wrapper translates codex JSONL events as follows:

| Codex event | Claude event |
|---|---|
| `item.completed` + `agent_message` | `content_block_delta` with the message text |
| `item.completed` + `command_execution` | skipped by default (set `CODEX_VERBOSE=1` to include) |
| `item.completed` + `reasoning` | skipped |
| `item.started` | skipped |
| `turn.completed` | `result` (end of execution) |
| `thread.started`, `turn.started` | skipped |

Command execution events are skipped by default because codex reads many files on startup (skills, configs) and echoes their full content, producing excessive noise in the progress log. Agent messages contain the meaningful output.

### How it works

```bash
# codex emits JSONL like:
{"type":"item.completed","item":{"type":"agent_message","text":"fixed the bug"}}

# wrapper translates to:
{"type":"content_block_delta","delta":{"type":"text_delta","text":"fixed the bug\n"}}
```

The script uses `jq` for JSON parsing, which is included in ralphex Docker images and available on most systems.

## GitHub Copilot CLI wrapper (included example)

The repository includes a wrapper at `scripts/copilot-as-claude/copilot-as-claude.sh` that keeps ralphex on the existing `claude_command` / `claude_args` path by translating GitHub Copilot CLI JSONL events into Claude stream-json output.

Unlike the Gemini wrapper, Copilot already has a native non-interactive JSONL mode. Unlike OpenCode, it also has native permission flags, so the wrapper can lean on Copilot's own autonomy controls instead of inventing a wrapper-specific config layer. The Copilot wrapper mainly handles prompt ingestion from stdin, event translation, review-prompt adaptation, stderr passthrough, and fallback `result` emission.

### Setup

```ini
# in ~/.config/ralphex/config or .ralphex/config
claude_command = /path/to/scripts/copilot-as-claude/copilot-as-claude.sh
claude_args =
```

### Authentication

Authenticate Copilot using either:

- `copilot login` (OAuth device flow with stored credentials)
- `COPILOT_GITHUB_TOKEN`
- `GH_TOKEN`
- `GITHUB_TOKEN`

Copilot checks the token variables in the order above. Fine-grained PATs must include the `Copilot Requests` permission. Classic PATs (`ghp_`) are not supported by the Copilot CLI.

### Environment variables

| Variable | Default | Description |
|---|---|---|
| `COPILOT_MODEL` | (Copilot CLI default) | Model to use |
| `COPILOT_GITHUB_TOKEN` | unset | Preferred auth token for automation |
| `GH_TOKEN` | unset | GitHub CLI token fallback |
| `GITHUB_TOKEN` | unset | Final auth token fallback |
| `GH_HOST` | `github.com` | Alternate GitHub host for Enterprise Cloud data residency |

### Why this wrapper uses Copilot JSONL mode

The wrapper runs Copilot with `-s --output-format json --stream on` so it can consume native JSONL events instead of scraping terminal text. It emits completed assistant messages rather than token deltas to keep ralphex output readable, while still using explicit completion events to map into Claude `result` output and echoing stderr back into the stream for existing error and limit detection.

### Permission model

The wrapper uses Copilot's native autonomy flags: `--autopilot --no-ask-user --allow-all`.

- `--autopilot` enables the multi-step autonomous execution required for unattended task/review phases
- `--no-ask-user` prevents Copilot from pausing the run with follow-up questions
- `--allow-all` enables tool, path, and URL permissions together, matching ralphex's unattended task/review model

GitHub's programmatic autopilot guidance uses the same core pattern: "Use the `--allow-all` (or `--yolo`) option" together with `--autopilot`, optionally adding `--max-autopilot-continues` for a safety cap in CI or scripts.

If you need a narrower policy, fork the wrapper and replace `--allow-all` with explicit `--allow-tool`, `--allow-url`, or related permission flags.

### How it differs from other included wrappers

| Wrapper | Transport | Permissions | Copilot-specific difference |
|---|---|---|---|
| Codex | Native JSONL | Codex sandbox/env flags | Copilot uses native `--autopilot`/`--allow-all`/`--no-ask-user` instead of Codex sandbox settings, and adds a review adapter for Claude Task-tool wording |
| OpenCode | Native JSONL | Merges `OPENCODE_CONFIG_CONTENT` with auto-allow permissions | Copilot uses built-in permission flags rather than JSON config merging |
| Gemini | Plain text | Gemini CLI settings outside the wrapper | Copilot streams structured JSONL events, so the wrapper can emit completed assistant messages and terminal events without scraping plain text lines |

## OpenCode wrapper (included example)

The repository includes a wrapper at `scripts/opencode/opencode-as-claude.sh` that translates OpenCode JSONL events to Claude stream-json format. It uses `jq` for JSON parsing and auto-sets permission auto-allow (`{"permission":{"*":"allow"}}`) for autonomous execution.

### Setup

```ini
# in ~/.config/ralphex/config or .ralphex/config
claude_command = /path/to/scripts/opencode/opencode-as-claude.sh
claude_args =
```

### Environment variables

| Variable | Default | Description |
|---|---|---|
| `OPENCODE_MODEL` | (opencode default) | Model in provider/model format, e.g. `github-copilot/claude-opus-4.6` |
| `OPENCODE_VERBOSE` | `0` | Set to `1` to include step start events in output |
| `OPENCODE_CONFIG_CONTENT` | `{"permission":{"*":"allow"}}` | JSON config merged with auto-allow permissions via `jq` deep merge |

If `OPENCODE_CONFIG_CONTENT` is already set, the wrapper merges `{"permission":{"*":"allow"}}` into it, preserving existing settings. Invalid JSON in this variable causes the wrapper to exit with an error.

### Event translation

| OpenCode event | Claude event |
|---|---|
| `text` | `content_block_delta` with `.part.text` |
| `step_finish` | `result` (end of execution) |
| `step_start` | skipped by default (set `OPENCODE_VERBOSE=1` to include) |

Text content is passed verbatim — no truncation or escaping — preserving signal strings like `<<<RALPHEX:...>>>`. Non-JSON lines are passed through for the executor's non-JSON fallback. Stderr is captured and emitted as `content_block_delta` events after the main stream for error/limit pattern detection.

### How it works

```bash
# opencode emits JSONL like:
{"type":"text","part":{"text":"fixed the bug\n"}}

# wrapper translates to:
{"type":"content_block_delta","delta":{"type":"text_delta","text":"fixed the bug\n"}}
```

For review prompts (detected by `<<<RALPHEX:REVIEW_DONE>>>` in the prompt text), the wrapper prepends adapter instructions telling the model to execute review agent tasks sequentially, since OpenCode does not support parallel sub-agents.

## Gemini CLI wrapper (included example)

The repository includes a wrapper at `scripts/gemini-as-claude/gemini-as-claude.sh` that translates Gemini CLI plain-text output to Claude stream-json format.

### Setup

```ini
# in ~/.config/ralphex/config or .ralphex/config
claude_command = /path/to/scripts/gemini-as-claude/gemini-as-claude.sh
claude_args =
```

### Environment variables

| Variable | Default | Description |
|---|---|---|
| `GEMINI_MODEL` | (gemini default) | Model to use with Gemini CLI |

### How it works

Since Gemini outputs plain text, the script simply wraps each line in a `content_block_delta` JSON event.

```bash
# gemini emits text like:
fixed the bug

# wrapper translates to:
{"type":"content_block_delta","delta":{"type":"text_delta","text":"fixed the bug\n"}}
```

## Writing your own wrapper

A wrapper script must:

1. Read the prompt from stdin (ralphex pipes it to avoid Windows command-line length limits)
2. Also accept `-p <prompt>` as a fallback for backward compatibility
3. Ignore other flags gracefully
4. Stream JSON events to stdout, one per line
5. Exit with code 0 on success

### Minimal template

```bash
#!/usr/bin/env bash
set -euo pipefail

# extract prompt from -p argument (backward compat) or stdin
prompt=""
while [[ $# -gt 0 ]]; do
    case "$1" in
        -p) prompt="${2:-}"; shift; shift 2>/dev/null || true ;;
        *)  shift ;; # ignore unknown flags
    esac
done

if [[ -z "$prompt" ]]; then
    # fall back to stdin: ralphex passes prompt via pipe to avoid Windows 8191-char cmd limit.
    # only read when stdin is not a terminal to avoid blocking interactive invocations.
    if [[ ! -t 0 ]]; then
        prompt=$(cat)
    fi
fi

if [[ -z "$prompt" ]]; then
    echo "error: no prompt provided (expected -p flag or stdin)" >&2
    exit 1
fi

# call your tool and translate output to claude stream-json.
# each text chunk should be emitted as:
#   {"type":"content_block_delta","delta":{"type":"text_delta","text":"..."}}
#
# end with:
#   {"type":"result","result":""}

# example: pipe tool output line by line
your-tool --prompt "$prompt" | while IFS= read -r line; do
    jq -cn --arg text "$line" \
        '{type: "content_block_delta", delta: {type: "text_delta", text: ($text + "\n")}}'
done

echo '{"type":"result","result":""}'
```

### Gemini CLI example

```bash
#!/usr/bin/env bash
set -euo pipefail

prompt=""
while [[ $# -gt 0 ]]; do
    case "$1" in
        -p) prompt="$2"; shift 2 ;;
        *)  shift ;;
    esac
done

if [[ -z "$prompt" ]] && [[ ! -t 0 ]]; then prompt=$(cat); fi
[[ -z "$prompt" ]] && exit 1

# gemini outputs plain text; wrap each line as a stream event
gemini -p "$prompt" 2>/dev/null | while IFS= read -r line; do
    jq -cn --arg text "$line" \
        '{type: "content_block_delta", delta: {type: "text_delta", text: ($text + "\n")}}'
done

echo '{"type":"result","result":""}'
```

### Local LLM (ollama) example

```bash
#!/usr/bin/env bash
set -euo pipefail

prompt=""
while [[ $# -gt 0 ]]; do
    case "$1" in
        -p) prompt="$2"; shift 2 ;;
        *)  shift ;;
    esac
done

if [[ -z "$prompt" ]] && [[ ! -t 0 ]]; then prompt=$(cat); fi
[[ -z "$prompt" ]] && exit 1

OLLAMA_MODEL="${OLLAMA_MODEL:-llama3}"

# ollama with JSON streaming
ollama run "$OLLAMA_MODEL" "$prompt" 2>/dev/null | while IFS= read -r line; do
    jq -cn --arg text "$line" \
        '{type: "content_block_delta", delta: {type: "text_delta", text: ($text + "\n")}}'
done

echo '{"type":"result","result":""}'
```

### OpenRouter API example

```bash
#!/usr/bin/env bash
set -euo pipefail

prompt=""
while [[ $# -gt 0 ]]; do
    case "$1" in
        -p) prompt="$2"; shift 2 ;;
        *)  shift ;;
    esac
done

if [[ -z "$prompt" ]] && [[ ! -t 0 ]]; then prompt=$(cat); fi
[[ -z "$prompt" ]] && exit 1

OPENROUTER_MODEL="${OPENROUTER_MODEL:-anthropic/claude-sonnet-4}"

response=$(curl -s https://openrouter.ai/api/v1/chat/completions \
    -H "Authorization: Bearer $OPENROUTER_API_KEY" \
    -H "Content-Type: application/json" \
    -d "$(jq -cn --arg model "$OPENROUTER_MODEL" --arg prompt "$prompt" '{
        model: $model,
        messages: [{role: "user", content: $prompt}]
    }')")

text=$(echo "$response" | jq -r '.choices[0].message.content // empty')
if [[ -n "$text" ]]; then
    jq -cn --arg text "$text" \
        '{type: "content_block_delta", delta: {type: "text_delta", text: ($text + "\n")}}'
fi

echo '{"type":"result","result":""}'
```

## Limitations and considerations

**Signal emission:** the underlying tool must follow ralphex prompt instructions to emit `<<<RALPHEX:...>>>` signals. Most capable models (GPT-4+, Claude, Gemini Pro) handle this reliably. Smaller/local models may not follow signal instructions consistently, which will cause ralphex to retry or timeout.

**Tool use:** Claude Code natively supports file editing, command execution, and other tools. Alternative providers typically only output text — they cannot directly edit files or run commands. This means they work best for review phases (where the output is analyzed by Claude for fixing) rather than task execution phases (where the agent needs to write code and run tests).

**Streaming:** the wrapper should emit events as they become available, not buffer the entire response. This allows ralphex to show real-time progress. The codex wrapper achieves this via the `while IFS= read -r line` pattern.

**Error handling:** if the underlying tool fails, the wrapper should either exit with a non-zero code or emit an error in a `result` event. ralphex's `ClaudeExecutor` handles both cases.

**Docker:** when running in Docker, ensure the wrapper script and its dependencies (jq, curl, etc.) are available inside the container. The ralphex base image includes jq. Mount custom scripts as read-only volumes.

## Troubleshooting

**Empty output / no events:**
- Check that the tool is actually producing output: run the wrapper manually with `echo "say hello" | your-wrapper`
- Verify stderr is redirected (add `2>/dev/null` for the underlying tool)
- Ensure `jq` is installed and accessible

**Signals not detected:**
- The model must include `<<<RALPHEX:COMPLETED>>>` or `<<<RALPHEX:FAILED>>>` in its text output
- Check that the prompt is passed through correctly (not truncated or escaped)
- Test manually: run the wrapper with a prompt that includes signal instructions

**JSON parsing errors:**
- Each line must be a complete, valid JSON object
- No trailing commas, no multi-line JSON objects
- Test with: `echo "test" | your-wrapper | jq .` (each line should parse)

**Timeout / stuck:**
- ralphex supports an optional per-session timeout via `--session-timeout` flag or `session_timeout` config option (e.g., `30m`, `1h`). When set, hanging claude sessions are killed after the deadline and the phase loop continues
- ralphex also supports `--idle-timeout` flag or `idle_timeout` config option (e.g., `5m`). Unlike session timeout (fixed wall-clock limit), idle timeout resets on each output line and fires only when the session goes silent — useful for detecting sessions that stopped producing output but didn't exit
- Check if the underlying tool has its own timeout settings
- For codex: adjust `CODEX_SANDBOX` if the sandbox is blocking operations
