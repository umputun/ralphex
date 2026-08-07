#!/usr/bin/env bash
set -euo pipefail

# Extract version from git tag (removes 'v' prefix)
VERSION="${1:-}"
VERSION="${VERSION#v}"

if [ -z "$VERSION" ]; then
  echo "Usage: $0 <version>"
  exit 1
fi

update_json_version() {
  local file="$1"
  local filter="$2"

  # Use jq if available, otherwise sed
  if [ "${RALPHEX_FORCE_SED:-0}" != "1" ] && command -v jq &> /dev/null; then
    jq --arg v "$VERSION" "$filter" "$file" > "$file.tmp"
    mv "$file.tmp" "$file"
  else
    # Fallback assumes each supported manifest contains exactly one version
    # field. Keep that invariant or replace this with a format-aware updater.
    sed -i.bak "s/\"version\": \"[^\"]*\"/\"version\": \"$VERSION\"/" "$file"
    rm "$file.bak"
  fi
}

# Update Claude plugin manifest.
if [ -f ".claude-plugin/plugin.json" ]; then
  update_json_version .claude-plugin/plugin.json ".version = \$v"
  echo "Updated Claude plugin.json to version $VERSION"
fi

# Update Claude marketplace metadata.
if [ -f ".claude-plugin/marketplace.json" ]; then
  update_json_version .claude-plugin/marketplace.json ".plugins[0].version = \$v"
  echo "Updated Claude marketplace.json to version $VERSION"
fi

# Keep the Codex package aligned with the same Ralphex release. The Codex
# marketplace intentionally has no separate version field.
if [ -f "plugins/ralphex/.codex-plugin/plugin.json" ]; then
  update_json_version plugins/ralphex/.codex-plugin/plugin.json ".version = \$v"
  echo "Updated Codex plugin.json to version $VERSION"
fi

if [ -f "plugins/ralphex/plugin.json" ]; then
  update_json_version plugins/ralphex/plugin.json ".version = \$v"
  echo "Updated portable plugin.json to version $VERSION"
fi
