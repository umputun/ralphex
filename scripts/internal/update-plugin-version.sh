#!/usr/bin/env bash
set -euo pipefail

MODE=write
if [ "${1:-}" = "--check" ]; then
  MODE=check
  shift
fi

# Accept a plugin version with an optional v prefix.
VERSION="${1:-}"
VERSION="${VERSION#v}"

if [ -z "$VERSION" ]; then
  echo "Usage: $0 [--check] <version>" >&2
  exit 1
fi

if [ "$#" -ne 1 ]; then
  echo "Usage: $0 [--check] <version>" >&2
  exit 1
fi

status=0

check_json_version() {
  local file="$1"
  local python_path="$2"
  local actual

  actual=$(python3 - "$file" "$python_path" <<'PY'
import json
import sys

value = json.load(open(sys.argv[1]))
for component in sys.argv[2].split("."):
    value = value[int(component)] if component.isdigit() else value[component]
print(value)
PY
  )
  if [ "$actual" != "$VERSION" ]; then
    echo "Version mismatch: $file has $actual, expected $VERSION" >&2
    status=1
  fi
}

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

update_or_check() {
  local file="$1"
  local jq_filter="$2"
  local python_path="$3"
  local label="$4"

  if [ "$MODE" = check ]; then
    check_json_version "$file" "$python_path"
  else
    update_json_version "$file" "$jq_filter"
    echo "Updated $label to version $VERSION"
  fi
}

for file in \
  .claude-plugin/plugin.json \
  .claude-plugin/marketplace.json \
  plugins/ralphex/.codex-plugin/plugin.json \
  plugins/ralphex/plugin.json; do
  if [ ! -f "$file" ]; then
    echo "Missing version manifest: $file" >&2
    status=1
  fi
done
if [ "$status" -ne 0 ]; then
  exit "$status"
fi

update_or_check .claude-plugin/plugin.json ".version = \$v" version "Claude plugin.json"
update_or_check .claude-plugin/marketplace.json ".plugins[0].version = \$v" plugins.0.version "Claude marketplace.json"
update_or_check plugins/ralphex/.codex-plugin/plugin.json ".version = \$v" version "Codex plugin.json"
update_or_check plugins/ralphex/plugin.json ".version = \$v" version "portable plugin.json"

if [ "$status" -ne 0 ]; then
  exit "$status"
fi

if [ "$MODE" = check ]; then
  echo "Plugin manifests match expected version $VERSION"
fi
