# Custom Providers for Claude Phases

ralphex uses Claude Code as the primary agent for task execution and code reviews. The `claude_command` and `claude_args` configuration options allow replacing Claude Code with any CLI tool that produces compatible output — codex, Gemini CLI, local LLMs, or custom scripts.

## How it works

ralphex's `ClaudeExecutor` runs the configured command, appends `-p <prompt>` as the last two arguments, and reads stdout as a stream of JSON events. Each line must be a valid JSON object. The executor recognizes these event types:

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
<claude_command> <claude_args...> -p <prompt>
```

When `claude_args` has a value (default: `--dangerously-skip-permissions --output-format stream-json --verbose`), those flags are split and prepended before `-p`. When `ClaudeExecutor.Args` is empty at the code level, only `-p <prompt>` is appended. Note that setting `claude_args =` (empty) in the config file may not clear the default due to config fallback behavior — the embedded default value is preserved when the user-specified value is empty.

**Wrapper scripts should ignore unknown flags gracefully** — use a catch-all `*) shift ;;` in the argument parser (as the included codex wrapper does). This way the wrapper works regardless of whether default Claude flags are passed through.

## Codex wrapper (included example)

The repository includes a working wrapper at `scripts/codex-as-claude.sh` that translates codex JSONL events to Claude stream-json format.

### Setup

```ini
# in ~/.config/ralphex/config or .ralphex/config
claude_command = /path/to/scripts/codex-as-claude.sh
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

## Writing your own wrapper

A wrapper script must:

1. Accept `-p <prompt>` among its arguments (ignore other flags gracefully)
2. Pass the prompt to the underlying tool
3. Stream JSON events to stdout, one per line
4. Exit with code 0 on success

### Minimal template

```bash
#!/usr/bin/env bash
set -euo pipefail

# extract prompt from -p argument
prompt=""
while [[ $# -gt 0 ]]; do
    case "$1" in
        -p) prompt="${2:-}"; shift; shift 2>/dev/null || true ;;
        *)  shift ;; # ignore unknown flags
    esac
done

if [[ -z "$prompt" ]]; then
    echo "error: no prompt provided (-p flag required)" >&2
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
- Check that the tool is actually producing output: run the wrapper manually with `-p "say hello"`
- Verify stderr is redirected (add `2>/dev/null` for the underlying tool)
- Ensure `jq` is installed and accessible

**Signals not detected:**
- The model must include `<<<RALPHEX:COMPLETED>>>` or `<<<RALPHEX:FAILED>>>` in its text output
- Check that the prompt is passed through correctly (not truncated or escaped)
- Test manually: run the wrapper with a prompt that includes signal instructions

**JSON parsing errors:**
- Each line must be a complete, valid JSON object
- No trailing commas, no multi-line JSON objects
- Test with: `your-wrapper -p "test" | jq .` (each line should parse)

**Timeout / stuck:**
- ralphex doesn't impose a timeout on claude sessions
- Check if the underlying tool has its own timeout settings
- For codex: adjust `CODEX_SANDBOX` if the sandbox is blocking operations
