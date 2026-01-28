#!/usr/bin/env bash
set -euo pipefail

# Extract version from git tag (removes 'v' prefix)
VERSION="${1#v}"

if [ -z "$VERSION" ]; then
  echo "Usage: $0 <version>"
  exit 1
fi

# Update plugin.json
if [ -f ".claude-plugin/plugin.json" ]; then
  # Use jq if available, otherwise sed
  if command -v jq &> /dev/null; then
    jq --arg v "$VERSION" '.version = $v' .claude-plugin/plugin.json > .claude-plugin/plugin.json.tmp
    mv .claude-plugin/plugin.json.tmp .claude-plugin/plugin.json
  else
    sed -i.bak "s/\"version\": \"[^\"]*\"/\"version\": \"$VERSION\"/" .claude-plugin/plugin.json
    rm .claude-plugin/plugin.json.bak
  fi
  echo "Updated plugin.json to version $VERSION"
fi

# Update marketplace.json
if [ -f ".claude-plugin/marketplace.json" ]; then
  if command -v jq &> /dev/null; then
    jq --arg v "$VERSION" '.plugins[0].version = $v' .claude-plugin/marketplace.json > .claude-plugin/marketplace.json.tmp
    mv .claude-plugin/marketplace.json.tmp .claude-plugin/marketplace.json
  else
    sed -i.bak "s/\"version\": \"[^\"]*\"/\"version\": \"$VERSION\"/" .claude-plugin/marketplace.json
    rm .claude-plugin/marketplace.json.bak
  fi
  echo "Updated marketplace.json to version $VERSION"
fi
