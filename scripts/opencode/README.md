# opencode

OpenCode CLI wrappers for ralphex, allowing OpenCode to replace Claude Code in task/review phases.

## Scripts

### opencode-as-claude.sh

Wraps OpenCode CLI to produce Claude-compatible stream-json output. Acts as a drop-in replacement for `claude` in task and review phases.

**Configuration** (`~/.config/ralphex/config` or `.ralphex/config`):

```ini
claude_command = /path/to/scripts/opencode/opencode-as-claude.sh
claude_args =
```

**Environment variables:**

- `OPENCODE_MODEL` — model in provider/model format, e.g. `openai/gpt-4o` (default: opencode default)
- `OPENCODE_VARIANT` — model variant/reasoning effort, e.g. `high`, `medium`, `low` (default: opencode default)
- `OPENCODE_EFFORT` — alias for `OPENCODE_VARIANT` when `OPENCODE_VARIANT` is unset
- `OPENCODE_REASONING` — alias for `OPENCODE_VARIANT` when `OPENCODE_VARIANT` and `OPENCODE_EFFORT` are unset
- `OPENCODE_VERBOSE` — set to `1` to include tool execution events in output (default: `0`)

The wrapper also honors Claude-compatible `--model` and `--effort` flags. Ralphex `--task-model=model:effort`, `--review-model=model:effort`, and `--plan-model=model:effort` are translated to OpenCode as `--model model --variant effort`.

### opencode-review.sh

Custom review script for ralphex external review phase. Uses OpenCode CLI with a configurable model for code review.

**Configuration:**

```ini
external_review_tool = custom
custom_review_script = /path/to/scripts/opencode/opencode-review.sh
```

**Environment variables:**

- `OPENCODE_REVIEW_MODEL` — model for review, e.g. `github-copilot/gpt-5.3-codex`
- `OPENCODE_REVIEW_VARIANT` — review model variant/reasoning effort, e.g. `high`
- `OPENCODE_REVIEW_EFFORT` — alias for `OPENCODE_REVIEW_VARIANT` when unset
- `OPENCODE_REVIEW_REASONING` — backward-compatible alias for `OPENCODE_REVIEW_VARIANT` when unset

## Testing

```bash
bash scripts/opencode/opencode-as-claude_test.sh
bash scripts/opencode/opencode-review_test.sh
```

## Requirements

- `opencode` CLI installed and accessible
- `jq` for JSON translation
