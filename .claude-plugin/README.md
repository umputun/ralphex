# ralphex Claude Code Plugin

This directory contains the Claude Code plugin configuration for ralphex.

## Files

- `plugin.json` - Plugin manifest with metadata and version
- `marketplace.json` - Marketplace catalog for single-plugin distribution

## Installation

Users can install via the plugin marketplace:

```bash
/plugin marketplace add umputun/ralphex
/plugin install ralphex@ralphex
```

## Versioning

The plugin version is independent from the ralphex CLI version. When distributed skill payload changes, maintainers bump all Claude, Codex, and portable manifests together with `make bump-plugin-version VERSION=<version>` before merging. Releases do not mutate plugin manifests.

## Marketplace Structure

This repository serves as both:
1. The ralphex CLI tool source code
2. A single-plugin Claude Code marketplace

The marketplace references `./` as the plugin source. Plugin skills are located in `assets/claude/skills/`, keeping all Claude Code related files organized together.
