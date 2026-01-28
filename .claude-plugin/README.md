# ralphex Claude Code Plugin

This directory contains the Claude Code plugin configuration for ralphex.

## Files

- `plugin.json` - Plugin manifest with metadata and version
- `marketplace.json` - Marketplace catalog for single-plugin distribution

## Installation

Users can install via the plugin marketplace:

```bash
/plugin marketplace add umputun/ralphex
/plugin install ralphex@umputun-ralphex
```

## Versioning

The `version` field in both JSON files is automatically updated during releases by `scripts/update-plugin-version.sh`, triggered by goreleaser.

## Marketplace Structure

This repository serves as both:
1. The ralphex CLI tool source code
2. A single-plugin Claude Code marketplace

The marketplace references `./` as the plugin source. Plugin skills are located in `assets/claude/skills/`, keeping all Claude Code related files organized together.
