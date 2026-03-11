# Copilot CLI as Execution Backend

ralphex uses GitHub Copilot CLI as the sole execution backend for all phases. The `copilot_command` and `copilot_args` configuration options allow overriding the command and arguments used to invoke Copilot CLI.

## How it works

ralphex's `CopilotExecutor` runs the configured command, appends `-p <prompt>` as the last two arguments, and reads stdout as a stream of JSONL events. Each line must be a valid JSON object. The executor recognizes these event types:

| Event type | Fields used | Purpose |
|---|---|---|
| `assistant.message_delta` | `data.deltaContent` | Streaming text output |
| `assistant.message` | `data.content` | Authoritative text (accumulated into result) |
| `result` | `exitCode` | End of session |
| `tool.execution_start` | `data.name` | Tool activity (optional logging) |
| `tool.execution_complete` | `data.success`, `data.error.message` | Tool-level errors |

See `docs/copilot-jsonl-format.md` for the full JSONL schema reference.

## Configuration

```ini
# in ~/.config/ralphex/config or .ralphex/config
copilot_command = copilot
copilot_args = --allow-all --no-ask-user --output-format json
copilot_coding_model = claude-opus-4-6
copilot_review_model = gpt-5.2-codex
```

### Signal detection

ralphex prompts instruct the agent to emit signals like `<<<RALPHEX:COMPLETED>>>` or `<<<RALPHEX:FAILED>>>` in its output. These signals must appear in the text content of `assistant.message` events. The underlying model must follow the prompt instructions for signals to be detected.

### Model selection

`CopilotExecutor` uses two models:
- **Coding model** (`copilot_coding_model`): used for task execution and review phases (default: `claude-opus-4-6`)
- **Review model** (`copilot_review_model`): used for external review phases (default: `gpt-5.2-codex`)

The model is passed via `--model <model>` flag to the copilot command.

## Limitations

**CLI errors:** When copilot exits with a non-zero exit code, no JSONL is produced. Error messages go to stderr as plain text. ralphex captures stderr and reports it as an execution error.

**Streaming:** The copilot CLI streams JSONL events line-by-line as they become available, allowing ralphex to show real-time progress output.
