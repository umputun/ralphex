#!/usr/bin/env bash
# codex-as-claude_test.sh — tests for codex-as-claude.sh wrapper.
#
# run from the ralphex directory:
#   bash scripts/codex-as-claude/codex-as-claude_test.sh
#
# requires: jq, bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WRAPPER="$SCRIPT_DIR/codex-as-claude.sh"
TMPDIR_TEST=$(mktemp -d)
trap 'rm -rf "$TMPDIR_TEST"' EXIT

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

# create a mock codex script that emits predefined JSONL
create_mock_codex() {
    local mock_script="$TMPDIR_TEST/codex"
    cat > "$mock_script" << 'MOCK_EOF'
#!/usr/bin/env bash
# mock codex: emit events from MOCK_STDOUT_FILE or a minimal default
# MOCK_STDOUT_FILE: file containing JSONL to emit on stdout
# MOCK_STDERR_FILE: file containing text to emit on stderr
# MOCK_EXIT_CODE: exit code to return (default 0)

if [[ -n "${MOCK_STDOUT_FILE:-}" && -f "$MOCK_STDOUT_FILE" ]]; then
    cat "$MOCK_STDOUT_FILE"
fi

if [[ -n "${MOCK_STDERR_FILE:-}" && -f "$MOCK_STDERR_FILE" ]]; then
    cat "$MOCK_STDERR_FILE" >&2
fi

exit "${MOCK_EXIT_CODE:-0}"
MOCK_EOF
    chmod +x "$mock_script"
    echo "$mock_script"
}

mock_codex=$(create_mock_codex)

echo "running codex-as-claude.sh tests"
echo ""

# ---------------------------------------------------------------------------
# test: signal passthrough — text containing <<<RALPHEX:ALL_TASKS_DONE>>>
# must appear verbatim in output
# ---------------------------------------------------------------------------
echo "test: signal passthrough"

cat > "$TMPDIR_TEST/signal_events.jsonl" << 'EOF'
{"type":"item.completed","item":{"type":"agent_message","text":"Working on the task..."}}
{"type":"item.completed","item":{"type":"agent_message","text":"<<<RALPHEX:ALL_TASKS_DONE>>>"}}
{"type":"turn.completed"}
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/signal_events.jsonl" \
    PATH="$TMPDIR_TEST:$PATH" \
    bash "$WRAPPER" -p "test prompt" 2>/dev/null)

if echo "$output" | grep -q '<<<RALPHEX:ALL_TASKS_DONE>>>'; then
    pass "ALL_TASKS_DONE signal preserved in output"
else
    fail "ALL_TASKS_DONE signal not found in output" "got: $output"
fi

# verify signal appears in a content_block_delta event
signal_event=$(echo "$output" | grep 'ALL_TASKS_DONE' | head -1)
if echo "$signal_event" | jq -e '.type == "content_block_delta"' >/dev/null 2>&1; then
    pass "signal emitted as content_block_delta event"
else
    fail "signal not in content_block_delta event" "got: $signal_event"
fi

# ---------------------------------------------------------------------------
# test: REVIEW_DONE signal passthrough
# ---------------------------------------------------------------------------
echo "test: REVIEW_DONE signal passthrough"

cat > "$TMPDIR_TEST/review_events.jsonl" << 'EOF'
{"type":"item.completed","item":{"type":"agent_message","text":"Review complete."}}
{"type":"item.completed","item":{"type":"agent_message","text":"<<<RALPHEX:REVIEW_DONE>>>"}}
{"type":"turn.completed"}
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/review_events.jsonl" \
    PATH="$TMPDIR_TEST:$PATH" \
    bash "$WRAPPER" -p "review prompt <<<RALPHEX:REVIEW_DONE>>>" 2>/dev/null)

if echo "$output" | grep -q '<<<RALPHEX:REVIEW_DONE>>>'; then
    pass "REVIEW_DONE signal preserved in output"
else
    fail "REVIEW_DONE signal not found in output" "got: $output"
fi

# ---------------------------------------------------------------------------
# test: turn.completed emits result event
# ---------------------------------------------------------------------------
echo "test: turn.completed produces result event"

cat > "$TMPDIR_TEST/turn_events.jsonl" << 'EOF'
{"type":"item.completed","item":{"type":"agent_message","text":"done"}}
{"type":"turn.completed"}
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/turn_events.jsonl" \
    PATH="$TMPDIR_TEST:$PATH" \
    bash "$WRAPPER" -p "test prompt" 2>/dev/null)

last_line=$(echo "$output" | tail -1)
if echo "$last_line" | jq -e '.type == "result"' >/dev/null 2>&1; then
    pass "turn.completed produces result event"
else
    fail "last line is not a result event" "got: $last_line"
fi

# ---------------------------------------------------------------------------
# test: reasoning and command_execution events are skipped by default
# ---------------------------------------------------------------------------
echo "test: non-agent events skipped (CODEX_VERBOSE=0)"

cat > "$TMPDIR_TEST/mixed_events.jsonl" << 'EOF'
{"type":"item.completed","item":{"type":"reasoning","summary":"thinking..."}}
{"type":"item.completed","item":{"type":"command_execution","command":"ls","aggregated_output":"file.txt"}}
{"type":"item.completed","item":{"type":"agent_message","text":"agent text"}}
{"type":"turn.completed"}
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/mixed_events.jsonl" \
    CODEX_VERBOSE=0 \
    PATH="$TMPDIR_TEST:$PATH" \
    bash "$WRAPPER" -p "test prompt" 2>/dev/null)

if echo "$output" | grep -q 'thinking'; then
    fail "reasoning text leaked into output"
elif echo "$output" | grep -q 'file.txt'; then
    fail "command_execution output leaked (CODEX_VERBOSE=0)"
elif echo "$output" | grep -q '"agent text"'; then
    pass "only agent_message events emitted"
else
    fail "agent text not found in output" "got: $output"
fi

# ---------------------------------------------------------------------------
# test: command_execution included when CODEX_VERBOSE=1
# ---------------------------------------------------------------------------
echo "test: command_execution included (CODEX_VERBOSE=1)"

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/mixed_events.jsonl" \
    CODEX_VERBOSE=1 \
    PATH="$TMPDIR_TEST:$PATH" \
    bash "$WRAPPER" -p "test prompt" 2>/dev/null)

if echo "$output" | grep -q 'file.txt'; then
    pass "command_execution output included when CODEX_VERBOSE=1"
else
    fail "command_execution output not found (CODEX_VERBOSE=1)" "got: $output"
fi

# ---------------------------------------------------------------------------
# test: exit code preservation on success
# ---------------------------------------------------------------------------
echo "test: exit code preservation — success"

cat > "$TMPDIR_TEST/minimal_events.jsonl" << 'EOF'
{"type":"item.completed","item":{"type":"agent_message","text":"done"}}
{"type":"turn.completed"}
EOF

MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.jsonl" \
    MOCK_EXIT_CODE=0 \
    PATH="$TMPDIR_TEST:$PATH" \
    bash "$WRAPPER" -p "test prompt" >/dev/null 2>&1
exit_code=$?

if [[ $exit_code -eq 0 ]]; then
    pass "exit code 0 on success"
else
    fail "expected exit code 0" "got: $exit_code"
fi

# ---------------------------------------------------------------------------
# test: missing prompt — error without -p and without stdin
# ---------------------------------------------------------------------------
echo "test: missing prompt exits with error"

set +e
PATH="$TMPDIR_TEST:$PATH" \
    bash "$WRAPPER" 2>/dev/null
exit_code=$?
set -e

if [[ $exit_code -ne 0 ]]; then
    pass "exits non-zero when no prompt provided"
else
    fail "expected non-zero exit when no prompt given" "got: $exit_code"
fi

# ---------------------------------------------------------------------------
# test: prompt via stdin (primary path used by ralphex on Windows)
# ---------------------------------------------------------------------------
echo "test: prompt via stdin"

output=$(echo "prompt from stdin" | MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.jsonl" \
    PATH="$TMPDIR_TEST:$PATH" \
    bash "$WRAPPER" --dangerously-skip-permissions 2>/dev/null)

if echo "$output" | grep -q '"content_block_delta"'; then
    pass "stdin prompt produces output"
else
    fail "wrapper failed with stdin prompt" "got: $output"
fi

# ---------------------------------------------------------------------------
# summary
# ---------------------------------------------------------------------------
echo ""
echo "results: $passed passed, $failed failed, $total total"

if [[ $failed -gt 0 ]]; then
    exit 1
fi
