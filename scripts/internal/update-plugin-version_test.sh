#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
REPO_ROOT=$(cd "$SCRIPT_DIR/../.." && pwd)

assert_fixture() {
  local fixture="$1"
  local expected="$2"

  python3 - "$fixture" "$expected" <<'PY'
import json
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
expected = sys.argv[2]
claude = json.loads((root / ".claude-plugin/plugin.json").read_text())
market = json.loads((root / ".claude-plugin/marketplace.json").read_text())
codex = json.loads((root / "plugins/ralphex/.codex-plugin/plugin.json").read_text())
portable = json.loads((root / "plugins/ralphex/plugin.json").read_text())

assert claude["version"] == expected
assert market["plugins"][0]["version"] == expected
assert codex["version"] == expected
assert portable["version"] == expected
assert claude["description"] == "Autonomous plan execution with Claude Code - task execution, monitoring, and plan creation"
assert market["plugins"][0]["description"] == "Autonomous plan execution with Claude Code - task execution, monitoring, and plan creation"
assert codex["description"] == "Plan-driven implementation, review, adoption, and configuration workflows for Ralphex"
assert codex["interface"]["brandColor"] == "#D97706"
PY
}

make_fixture() {
  local fixture
  fixture=$(mktemp -d "${TMPDIR:-/tmp}/ralphex-version-test-XXXXXX")
  mkdir -p "$fixture/plugins/ralphex" "$fixture/scripts/internal"
  cp -R "$REPO_ROOT/.claude-plugin" "$fixture/.claude-plugin"
  cp -R "$REPO_ROOT/plugins/ralphex/.codex-plugin" "$fixture/plugins/ralphex/.codex-plugin"
  cp "$REPO_ROOT/plugins/ralphex/plugin.json" "$fixture/plugins/ralphex/plugin.json"
  cp "$REPO_ROOT/scripts/internal/update-plugin-version.sh" "$fixture/scripts/internal/"
  printf '%s\n' "$fixture"
}

jq_fixture=$(make_fixture)
sed_fixture=$(make_fixture)
trap 'rm -rf "$jq_fixture" "$sed_fixture"' EXIT

(
  cd "$jq_fixture"
  ./scripts/internal/update-plugin-version.sh v1.2.3
)
assert_fixture "$jq_fixture" "1.2.3"

(
  cd "$sed_fixture"
  RALPHEX_FORCE_SED=1 ./scripts/internal/update-plugin-version.sh v2.3.4
)
assert_fixture "$sed_fixture" "2.3.4"

echo "update-plugin-version tests passed"
