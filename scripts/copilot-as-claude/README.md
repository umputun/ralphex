# copilot-as-claude

Wraps GitHub Copilot CLI to produce Claude-compatible `stream-json` output, allowing Copilot to replace Claude Code in ralphex task and review phases through the existing `claude_command` / `claude_args` path.

## Configuration

Add to `~/.config/ralphex/config` or `.ralphex/config`:

```ini
claude_command = /path/to/scripts/copilot-as-claude/copilot-as-claude.sh
claude_args =
```

Setting `claude_args` to empty is optional. ralphex may still pass default Claude flags because of config fallback behavior, and the wrapper ignores unknown flags gracefully.

## Requirements

- `copilot` CLI installed and accessible on `PATH`
- `jq` for JSON translation
- GitHub Copilot authentication via stored login state or token environment variables

## Authentication

Copilot supports two native auth paths:

1. Run `copilot login` to authenticate with the default OAuth device flow and store credentials in the system credential store (or `~/.copilot/` if no secure store is available).
2. Set one of the token environment variables below for headless automation.

The Copilot CLI checks token variables in this order: `COPILOT_GITHUB_TOKEN`, `GH_TOKEN`, then `GITHUB_TOKEN`.

Supported token types include:

- fine-grained PATs with the `Copilot Requests` permission
- OAuth tokens from the GitHub Copilot CLI app
- OAuth tokens from the GitHub CLI (`gh`) app

Classic personal access tokens (`ghp_`) are not supported by the Copilot CLI.

## Native Copilot environment variables

| Variable | Default | Description |
|---|---|---|
| `COPILOT_MODEL` | (Copilot CLI default) | Model to use for task and review sessions |
| `COPILOT_GITHUB_TOKEN` | unset | Preferred auth token for headless runs |
| `GH_TOKEN` | unset | GitHub CLI token fallback that Copilot also accepts |
| `GITHUB_TOKEN` | unset | Final auth token fallback if the previous variables are unset |
| `GH_HOST` | `github.com` | Override the GitHub host for Enterprise Cloud data residency deployments |
| `COPILOT_HOME` | `$HOME/.copilot` | Override Copilot's config and state directory |

## Permission model

The wrapper runs Copilot with `--autopilot --no-ask-user --allow-all` so task and review phases can complete unattended across multiple model turns. `--autopilot` is Copilot's native hands-off mode for programmatic runs, `--no-ask-user` suppresses follow-up questions, and `--allow-all` enables tool, path, and URL permissions together.

If you need a more restrictive policy, copy the wrapper and replace `--allow-all` with explicit `--allow-tool`, `--allow-url`, or related permission flags.

## Testing

```bash
bash scripts/copilot-as-claude/copilot-as-claude_test.sh
bash scripts/copilot-as-claude/copilot-as-claude_docs_test.sh
```
