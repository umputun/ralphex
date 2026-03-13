# gemini-as-claude

Gemini CLI wrapper for ralphex, allowing Gemini to replace Claude Code in task and review phases.

## Scripts

### gemini-as-claude.sh

Wraps Gemini CLI to produce Claude-compatible stream-json output. Acts as a drop-in replacement for `claude` in task and review phases. Since Gemini outputs plain text, this script wraps each line in a `content_block_delta` JSON event.

**Configuration** (`~/.config/ralphex/config` or `.ralphex/config`):

```ini
claude_command = /path/to/scripts/gemini-as-claude/gemini-as-claude.sh
claude_args =
```

**Environment variables:**

- `GEMINI_MODEL` — model to use (passed as `--model` flag when set)

## Testing

```bash
bash scripts/gemini-as-claude/gemini-as-claude_test.sh
```

## Requirements

- `gemini` CLI installed and accessible
- `jq` for JSON translation
