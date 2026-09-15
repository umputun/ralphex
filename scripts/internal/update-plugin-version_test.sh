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

assert_check_fails() {
  local fixture="$1"
  local expected_version="$2"
  local expected_message="$3"
  local output

  if output=$(cd "$fixture" && ./scripts/internal/update-plugin-version.sh --check "$expected_version" 2>&1); then
    echo "expected version check to fail" >&2
    exit 1
  fi
  case "$output" in
    *"$expected_message"*) ;;
    *)
      echo "unexpected version check output: $output" >&2
      exit 1
      ;;
  esac
}

corrupt_version() {
  local fixture="$1"
  local file="$2"
  local python_path="$3"

  python3 - "$fixture/$file" "$python_path" <<'PY'
import json
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
data = json.loads(path.read_text())
target = data
components = sys.argv[2].split(".")
for component in components[:-1]:
    target = target[int(component)] if component.isdigit() else target[component]
last = components[-1]
if last.isdigit():
    target[int(last)] = "9.9.9"
else:
    target[last] = "9.9.9"
path.write_text(json.dumps(data))
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
missing_fixture=$(make_fixture)
claude_fixture=$(make_fixture)
market_fixture=$(make_fixture)
codex_fixture=$(make_fixture)
portable_fixture=$(make_fixture)
trap 'rm -rf "$jq_fixture" "$sed_fixture" "$missing_fixture" "$claude_fixture" "$market_fixture" "$codex_fixture" "$portable_fixture"' EXIT

(
  cd "$jq_fixture"
  ./scripts/internal/update-plugin-version.sh v1.2.3
)
assert_fixture "$jq_fixture" "1.2.3"
(
  cd "$jq_fixture"
  ./scripts/internal/update-plugin-version.sh --check v1.2.3
)
assert_fixture "$jq_fixture" "1.2.3"

(
  cd "$sed_fixture"
  RALPHEX_FORCE_SED=1 ./scripts/internal/update-plugin-version.sh v2.3.4
)
assert_fixture "$sed_fixture" "2.3.4"

rm "$missing_fixture/plugins/ralphex/plugin.json"
missing_version=$(python3 -c 'import json, sys; print(json.load(open(sys.argv[1]))["version"])' "$missing_fixture/.claude-plugin/plugin.json")
assert_check_fails "$missing_fixture" "$missing_version" "Missing version manifest"

for fixture in "$claude_fixture" "$market_fixture" "$codex_fixture" "$portable_fixture"; do
  (
    cd "$fixture"
    ./scripts/internal/update-plugin-version.sh 1.2.3 >/dev/null
  )
done

corrupt_version "$claude_fixture" .claude-plugin/plugin.json version
assert_check_fails "$claude_fixture" "1.2.3" ".claude-plugin/plugin.json has 9.9.9"

corrupt_version "$market_fixture" .claude-plugin/marketplace.json plugins.0.version
assert_check_fails "$market_fixture" "1.2.3" ".claude-plugin/marketplace.json has 9.9.9"

corrupt_version "$codex_fixture" plugins/ralphex/.codex-plugin/plugin.json version
assert_check_fails "$codex_fixture" "1.2.3" "plugins/ralphex/.codex-plugin/plugin.json has 9.9.9"

corrupt_version "$portable_fixture" plugins/ralphex/plugin.json version
assert_check_fails "$portable_fixture" "1.2.3" "plugins/ralphex/plugin.json has 9.9.9"

echo "update-plugin-version tests passed"
