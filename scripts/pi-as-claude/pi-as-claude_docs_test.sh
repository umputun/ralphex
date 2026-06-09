#!/usr/bin/env bash
# pi-as-claude_docs_test.sh — validates pi wrapper documentation and repo integration.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

passed=0
failed=0
total=0

pass() {
    passed=$((passed + 1))
    total=$((total + 1))
    echo "  PASS: $1"
}

fail() {
    failed=$((failed + 1))
    total=$((total + 1))
    echo "  FAIL: $1"
    if [[ -n "${2:-}" ]]; then
        echo "        $2"
    fi
}

assert_contains() {
    local file="$1"
    local needle="$2"
    local label="$3"

    if grep -Fq -- "$needle" "$file"; then
        pass "$label"
    else
        fail "$label" "missing '$needle' in $file"
    fi
}

assert_not_contains() {
    local file="$1"
    local needle="$2"
    local label="$3"

    if grep -Fq -- "$needle" "$file"; then
        fail "$label" "unexpected '$needle' in $file"
    else
        pass "$label"
    fi
}

assert_executable() {
    local file="$1"
    local label="$2"

    if [[ -x "$file" ]]; then
        pass "$label"
    else
        fail "$label" "$file is not executable"
    fi
}

echo "running pi-as-claude docs tests"
echo ""

assert_executable "$REPO_ROOT/scripts/pi-as-claude/pi-as-claude.sh" "wrapper script is executable"
assert_executable "$REPO_ROOT/scripts/pi-as-claude/pi-as-claude_test.sh" "wrapper shell test is executable"

# wrapper README
assert_contains \
    "$REPO_ROOT/scripts/pi-as-claude/README.md" \
    "claude_command = /path/to/scripts/pi-as-claude/pi-as-claude.sh" \
    "wrapper README contains config snippet"
assert_contains \
    "$REPO_ROOT/scripts/pi-as-claude/README.md" \
    "PI_VERBOSE" \
    "wrapper README documents PI_VERBOSE env var"
assert_contains \
    "$REPO_ROOT/scripts/pi-as-claude/README.md" \
    "PI_PROVIDER" \
    "wrapper README documents PI_PROVIDER env var"
assert_contains \
    "$REPO_ROOT/scripts/pi-as-claude/README.md" \
    "PI_MODEL" \
    "wrapper README documents PI_MODEL env var"
assert_contains \
    "$REPO_ROOT/scripts/pi-as-claude/README.md" \
    "PI_THINKING" \
    "wrapper README documents PI_THINKING env var"
assert_contains \
    "$REPO_ROOT/scripts/pi-as-claude/README.md" \
    "bash scripts/pi-as-claude/pi-as-claude_test.sh" \
    "wrapper README includes wrapper test command"

# custom providers doc
assert_contains \
    "$REPO_ROOT/docs/custom-providers.md" \
    "## pi CLI wrapper (included example)" \
    "custom providers doc includes pi section"
assert_contains \
    "$REPO_ROOT/docs/custom-providers.md" \
    "scripts/pi-as-claude/pi-as-claude.sh" \
    "custom providers doc references pi wrapper path"
assert_contains \
    "$REPO_ROOT/docs/custom-providers.md" \
    "assistantMessageEvent.type" \
    "custom providers doc documents pi event translation"
assert_contains \
    "$REPO_ROOT/docs/custom-providers.md" \
    "### Thinking / effort mapping" \
    "custom providers doc documents pi thinking/effort mapping"

# top-level README
assert_contains \
    "$REPO_ROOT/README.md" \
    "scripts/pi-as-claude/pi-as-claude.sh" \
    "top-level README mentions pi wrapper"
assert_contains \
    "$REPO_ROOT/README.md" \
    "The included Codex, Copilot, and pi wrappers require \`jq\` on \`PATH\` for JSON translation." \
    "top-level README documents jq requirement for pi wrapper"
assert_contains \
    "$REPO_ROOT/README.md" \
    "PI_PROVIDER" \
    "top-level README documents pi-specific environment variables"
assert_contains \
    "$REPO_ROOT/README.md" \
    "scripts/pi-as-claude/" \
    "top-level README requirements list mentions pi wrapper dir"

# llms.txt
assert_contains \
    "$REPO_ROOT/llms.txt" \
    "scripts/pi-as-claude/pi-as-claude.sh" \
    "llms.txt wrapper inventory mentions pi wrapper"
assert_contains \
    "$REPO_ROOT/llms.txt" \
    "scripts/pi-as-claude/" \
    "llms.txt requirements list mentions pi wrapper dir"

# CLAUDE.md
assert_contains \
    "$REPO_ROOT/CLAUDE.md" \
    "scripts/pi-as-claude/ # pi wrapper for Claude-compatible output" \
    "CLAUDE inventory includes pi wrapper directory"
assert_contains \
    "$REPO_ROOT/CLAUDE.md" \
    "scripts/pi-as-claude/pi-as-claude.sh" \
    "CLAUDE alternative provider docs mention pi wrapper path"

# pi is a wrapper-only provider: the assets/pi skill tree was removed,
# so neither the skill files nor the dedicated integration sections persist
assert_not_contains \
    "$REPO_ROOT/docs/custom-providers.md" \
    "assets/pi/skills/" \
    "custom providers doc no longer references pi skills"
assert_not_contains \
    "$REPO_ROOT/README.md" \
    "assets/pi/skills/" \
    "top-level README no longer references pi skills"
assert_not_contains \
    "$REPO_ROOT/llms.txt" \
    "assets/pi/skills/" \
    "llms.txt no longer references pi skills"
assert_not_contains \
    "$REPO_ROOT/CLAUDE.md" \
    "assets/pi/" \
    "CLAUDE no longer records pi skills manifest rationale"
assert_not_contains \
    "$REPO_ROOT/README.md" \
    "## pi Integration" \
    "top-level README has no pi Integration section"
assert_not_contains \
    "$REPO_ROOT/llms.txt" \
    "## pi Integration" \
    "llms.txt has no pi Integration section"

echo ""
echo "summary: $passed passed, $failed failed, $total total"

if [[ $failed -ne 0 ]]; then
    exit 1
fi
