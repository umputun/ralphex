#!/usr/bin/env bash
# copilot-as-claude_docs_test.sh — validates Copilot wrapper documentation snippets.

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

assert_executable() {
    local file="$1"
    local label="$2"

    if [[ -x "$file" ]]; then
        pass "$label"
    else
        fail "$label" "$file is not executable"
    fi
}

echo "running copilot-as-claude docs tests"
echo ""

assert_executable "$REPO_ROOT/scripts/copilot-as-claude/copilot-as-claude.sh" "wrapper script is executable"
assert_executable "$REPO_ROOT/scripts/copilot-as-claude/copilot-as-claude_test.sh" "wrapper shell test is executable"

assert_contains \
    "$REPO_ROOT/scripts/copilot-as-claude/README.md" \
    "claude_command = /path/to/scripts/copilot-as-claude/copilot-as-claude.sh" \
    "wrapper README contains config snippet"
assert_contains \
    "$REPO_ROOT/scripts/copilot-as-claude/README.md" \
    "copilot login" \
    "wrapper README documents native login flow"
assert_contains \
    "$REPO_ROOT/scripts/copilot-as-claude/README.md" \
    "COPILOT_GITHUB_TOKEN" \
    "wrapper README documents token auth"
assert_contains \
    "$REPO_ROOT/scripts/copilot-as-claude/README.md" \
    "bash scripts/copilot-as-claude/copilot-as-claude_test.sh" \
    "wrapper README includes wrapper test command"

assert_contains \
    "$REPO_ROOT/docs/custom-providers.md" \
    "## GitHub Copilot CLI wrapper (included example)" \
    "custom providers doc includes Copilot section"
assert_contains \
    "$REPO_ROOT/docs/custom-providers.md" \
    "--output-format json --stream on" \
    "custom providers doc explains native Copilot JSONL mode"
assert_contains \
    "$REPO_ROOT/docs/custom-providers.md" \
    "--no-ask-user --allow-all" \
    "custom providers doc explains Copilot permission strategy"
assert_contains \
    "$REPO_ROOT/docs/custom-providers.md" \
    "### How it differs from other included wrappers" \
    "custom providers doc includes wrapper comparison section"
assert_contains \
    "$REPO_ROOT/docs/custom-providers.md" \
    "| Codex | Native JSONL | Codex sandbox/env flags |" \
    "custom providers doc compares Copilot against Codex"
assert_contains \
    "$REPO_ROOT/docs/custom-providers.md" \
    '| OpenCode | Native JSONL | Merges `OPENCODE_CONFIG_CONTENT` with auto-allow permissions |' \
    "custom providers doc compares Copilot against OpenCode"
assert_contains \
    "$REPO_ROOT/docs/custom-providers.md" \
    "| Gemini | Plain text | Gemini CLI settings outside the wrapper |" \
    "custom providers doc compares Copilot against Gemini"

assert_contains \
    "$REPO_ROOT/README.md" \
    "scripts/copilot-as-claude/copilot-as-claude.sh" \
    "top-level README mentions included Copilot wrapper"
assert_contains \
    "$REPO_ROOT/CLAUDE.md" \
    "scripts/copilot-as-claude/ # GitHub Copilot CLI wrapper for Claude-compatible output" \
    "CLAUDE inventory includes Copilot wrapper directory"
assert_contains \
    "$REPO_ROOT/CLAUDE.md" \
    "scripts/copilot-as-claude/copilot-as-claude.sh" \
    "CLAUDE alternative provider docs mention Copilot wrapper path"
assert_contains \
    "$REPO_ROOT/README.md" \
    "wraps GitHub Copilot CLI" \
    "top-level README uses final Copilot wrapper naming"
assert_contains \
    "$REPO_ROOT/docs/custom-providers.md" \
    "GitHub Copilot CLI JSONL events" \
    "custom providers doc uses final Copilot wrapper naming"
assert_contains \
    "$REPO_ROOT/CLAUDE.md" \
    "GitHub Copilot CLI wrapper for Claude-compatible output" \
    "CLAUDE inventory uses final Copilot wrapper naming"

echo ""
echo "summary: $passed passed, $failed failed, $total total"

if [[ $failed -ne 0 ]]; then
    exit 1
fi
