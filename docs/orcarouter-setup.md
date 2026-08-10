# OrcaRouter Setup Guide

This guide explains how to use ralphex with Claude models hosted on [OrcaRouter](https://www.orcarouter.ai).

## Overview

OrcaRouter is a unified AI gateway that routes requests to Claude and other leading models through a single API endpoint (`https://api.orcarouter.ai/v1`). It runs gateway-level, zero-trust security for AI agents on the same endpoint — screening every prompt/response and governing every tool call on a default-deny basis, with no application code changes. Using OrcaRouter lets you:
- Run the full ralphex pipeline (Claude Code executor) through one gateway endpoint
- Route to Claude models without a direct Anthropic API subscription
- Keep your API key on the host — it is never written into the container image or the command line

## Security Best Practices

**Never expose your API key.** OrcaRouter API keys start with `sk-orca-` and unlock billing — treat them like any other secret:

1. Set `ORCAROUTER_API_KEY` in your shell environment, not in files that get committed
2. The wrapper translates it to `ANTHROPIC_AUTH_TOKEN` using the docker **inherit form** (`-e VAR`), so the key value never appears in `ps` output or the docker command line
3. ralphex never mounts your key file into the container — it is passed via environment only

**Avoid passing secrets on the command line.** When using `-E` to pass extra environment variables:
- Prefer `-E VAR` (inherit form) over `-E VAR=value` for secrets
- Values in `-E VAR=value` are visible in `ps` output to other users on the system

## Setup Instructions

### 1. Create an API key

Sign up at [orcarouter.ai](https://www.orcarouter.ai) and create an API key in the dashboard. Keys start with `sk-orca-`.

### 2. Export the key

```bash
export ORCAROUTER_API_KEY=sk-orca-...
```

### 3. Run ralphex with the OrcaRouter provider

```bash
ralphex --claude-provider=orcarouter docs/plans/feature.md
```

The wrapper automatically:
- Sets `ANTHROPIC_BASE_URL=https://api.orcarouter.ai` (the gateway endpoint)
- Translates `ORCAROUTER_API_KEY` to `ANTHROPIC_AUTH_TOKEN` (inherit form — the key value never appears on the docker command line)
- Pins the default model vars to anthropic-namespaced gateway models (`anthropic/claude-opus-5`, `anthropic/claude-sonnet-5`, `anthropic/claude-haiku-4.5`) — bare Anthropic model IDs are rejected by the gateway with `model_not_found`

## Environment Variables

### Required

| Variable | Description |
|----------|-------------|
| `ORCAROUTER_API_KEY` | OrcaRouter API key (starts with `sk-orca-`). Translated to `ANTHROPIC_AUTH_TOKEN` automatically |

Alternatively, set `ANTHROPIC_AUTH_TOKEN` directly to the OrcaRouter key if you prefer not to use the `ORCAROUTER_API_KEY` name.

### ralphex Configuration

| Variable | Description |
|----------|-------------|
| `RALPHEX_CLAUDE_PROVIDER` | Set to `orcarouter` to enable OrcaRouter mode (alternative to `--claude-provider` flag) |

### Optional Model Configuration

| Variable | Description |
|----------|-------------|
| `ANTHROPIC_MODEL` | Override default Claude model |
| `ANTHROPIC_DEFAULT_OPUS_MODEL` | Custom Opus model on the gateway |
| `ANTHROPIC_DEFAULT_SONNET_MODEL` | Custom Sonnet model on the gateway |
| `ANTHROPIC_DEFAULT_HAIKU_MODEL` | Custom Haiku model on the gateway |
| `ANTHROPIC_SMALL_FAST_MODEL` | Model for fast operations |
| `ANTHROPIC_BASE_URL` | Custom gateway endpoint (defaults to `https://api.orcarouter.ai`) |
| `DISABLE_PROMPT_CACHING` | Set to disable prompt caching |

Any of these can be overridden per-run with `-E` flags, e.g.:

```bash
ralphex --claude-provider=orcarouter -E ANTHROPIC_DEFAULT_SONNET_MODEL=anthropic/claude-sonnet-4-5 docs/plans/feature.md
```

## Example Usage

### Basic usage

```bash
export ORCAROUTER_API_KEY=sk-orca-...
ralphex --claude-provider=orcarouter docs/plans/feature.md
```

### Session-wide OrcaRouter mode

```bash
export ORCAROUTER_API_KEY=sk-orca-...
export RALPHEX_CLAUDE_PROVIDER=orcarouter

# All ralphex commands now use OrcaRouter
ralphex docs/plans/feature.md
ralphex --review
```

### Dry run (verify the docker command without running)

```bash
export ORCAROUTER_API_KEY=sk-orca-...
ralphex --claude-provider=orcarouter --dry-run docs/plans/feature.md
```

The output shows the exact docker command. The key value is not present — only `ANTHROPIC_AUTH_TOKEN` is passed via the inherit form.

## Startup Output

When using OrcaRouter mode, ralphex shows the provider configuration:

**With `ORCAROUTER_API_KEY` set:**
```
using image: ghcr.io/umputun/ralphex-go:latest
claude provider: orcarouter (keychain skipped)
  using ORCAROUTER_API_KEY (translated to ANTHROPIC_AUTH_TOKEN)
  passing: ANTHROPIC_BASE_URL, ANTHROPIC_DEFAULT_OPUS_MODEL, ANTHROPIC_DEFAULT_SONNET_MODEL, ANTHROPIC_DEFAULT_HAIKU_MODEL, ANTHROPIC_SMALL_FAST_MODEL, ORCAROUTER_API_KEY, ANTHROPIC_AUTH_TOKEN
```

**With no key set:**
```
using image: ghcr.io/umputun/ralphex-go:latest
claude provider: orcarouter (keychain skipped)
  warning: no API key set (set ORCAROUTER_API_KEY or ANTHROPIC_AUTH_TOKEN)
  passing: ANTHROPIC_BASE_URL, ANTHROPIC_DEFAULT_OPUS_MODEL, ANTHROPIC_DEFAULT_SONNET_MODEL, ANTHROPIC_DEFAULT_HAIKU_MODEL, ANTHROPIC_SMALL_FAST_MODEL
```

## Troubleshooting

### `model_not_found` errors

The gateway rejects bare Anthropic model IDs (e.g. `claude-sonnet-4-5`). The wrapper pins `anthropic/`-namespaced model IDs by default, but if you override them with `-E`, make sure to keep the `anthropic/` prefix (e.g. `anthropic/claude-sonnet-5`).

### `AuthenticationError` / `401 Unauthorized`

Your API key is missing or invalid:
1. Verify `ORCAROUTER_API_KEY` is set in the shell that runs ralphex: `echo "${#ORCAROUTER_API_KEY}"` (prints the length, not the value)
2. Confirm the key starts with `sk-orca-`
3. If you set `ANTHROPIC_AUTH_TOKEN` directly, make sure it holds the OrcaRouter key

### No `~/.claude` directory error on Linux

When using OrcaRouter mode, ralphex skips the Claude configuration directory check. If you see this error, ensure you're using `--claude-provider=orcarouter` flag.
