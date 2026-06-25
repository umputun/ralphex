# pi-as-claude

pi CLI wrapper for ralphex, allowing pi to replace Claude Code in task and review phases.

## Scripts

### pi-as-claude.sh

Wraps the pi CLI to produce Claude-compatible stream-json output. Acts as a drop-in replacement for `claude` in task and review phases. It translates pi's `--mode json` JSONL event stream into the `content_block_delta` / `result` events that ralphex's `ClaudeExecutor` parses. Plan creation mode (`ralphex --plan`) has no pi-specific adapter and is untested.

Suppressed events (tool executions, lifecycle noise) are emitted as empty keepalive deltas so a configured `idle_timeout` does not kill a healthy session during a long silent tool run. pi's stderr is re-emitted after the main stream for ralphex error/limit pattern detection; any literal `<<<RALPHEX:` token on stderr is neutralized to `<<< RALPHEX:` (a space is inserted) so stray diagnostics cannot be mistaken for a real completion signal.

**Configuration** (`~/.config/ralphex/config` or `.ralphex/config`):

```ini
claude_command = /path/to/scripts/pi-as-claude/pi-as-claude.sh
claude_args =
```

For a one-off run without editing config:

```bash
ralphex --claude-command=/path/to/scripts/pi-as-claude/pi-as-claude.sh docs/plans/feature.md
```

**Environment variables:**

- `PI_PROVIDER` — provider to use (passed as `--provider` flag when set; pi defaults to `google`)
- `PI_MODEL` — model to use (passed as `--model` when ralphex does not append a `--model` flag)
- `PI_THINKING` — thinking level (used when ralphex does not append an `--effort` flag)
- `PI_VERBOSE` — set to `1` to include tool execution events in the stream (default: `0`, only assistant text is shown)
- `PI_EXTRA_ARGS` — extra flags appended verbatim to the `pi` invocation (word-split on whitespace). The wrapper builds the `pi` command line itself and ignores unknown flags, so this is the only way to pass through arbitrary `pi` options

**Auto-approving tools in non-interactive runs:** ralphex drives pi non-interactively (`--print`), where pi has no TTY to confirm tool calls. Any pi extension that gates `write`/`edit`/`bash` behind interactive confirmation (e.g. [`pi-nolo`](https://github.com/burneikis/pi-nolo)) will block every mutation, and the model fails the task. Disable the gate for the run — e.g. with a pi-nolo build that exposes a non-interactive mode flag:

```bash
export PI_EXTRA_ARGS="--nolo-mode full"
ralphex docs/plans/feature.md
```

Alternatively, disable extensions entirely with `export PI_EXTRA_ARGS="--no-extensions"`, or remove the gating extension while running ralphex.

**Model and effort:** ralphex appends `--model <m>` / `--effort <e>` per phase. `--model` is forwarded to pi's `--model`; `--effort` maps to pi's `--thinking` (`off`, `minimal`, `low`, `medium`, `high`, `xhigh` pass through; `max` → `xhigh` with a stderr note since pi has no `max` level).

## Testing

```bash
bash scripts/pi-as-claude/pi-as-claude_test.sh
bash scripts/pi-as-claude/pi-as-claude_docs_test.sh
```

## Requirements

- `pi` CLI installed and accessible (v0.78.1+)
- `jq` for JSON translation

## Troubleshooting

### No assistant text in the progress log

The wrapper emits only assistant text by default and skips tool execution events as noise. To see tool activity (file reads, shell commands, edits), export `PI_VERBOSE=1` before running ralphex (ralphex passes `claude_command` to the OS verbatim as the executable, so an inline `env VAR=val` prefix would not work — the child inherits the exported environment instead):

```bash
export PI_VERBOSE=1
ralphex docs/plans/feature.md
```

### Provider / API key errors

pi defaults to the `google` provider and reads its API key from the provider's standard env vars. Set `PI_PROVIDER` to switch providers, and export the matching API key before running ralphex.
