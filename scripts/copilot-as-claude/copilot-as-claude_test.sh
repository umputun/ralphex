#!/usr/bin/env bash
# copilot-as-claude_test.sh — tests for copilot-as-claude.sh wrapper.
#
# run from the ralphex directory:
#   bash scripts/copilot-as-claude/copilot-as-claude_test.sh
#
# requires: jq, bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WRAPPER="$SCRIPT_DIR/copilot-as-claude.sh"
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

create_mock_copilot() {
    local mock_script="$TMPDIR_TEST/copilot"
    cat > "$mock_script" <<'MOCK_EOF'
#!/usr/bin/env bash
set -euo pipefail

args_file="${TMPDIR_TEST}/copilot_args"
stdin_file="${TMPDIR_TEST}/copilot_stdin"

printf '%s\n' "$@" > "$args_file"
cat > "$stdin_file"

if [[ -n "${MOCK_PID_FILE:-}" ]]; then
    echo $$ > "$MOCK_PID_FILE"
fi

if [[ -n "${MOCK_STDOUT_FILE:-}" && -f "$MOCK_STDOUT_FILE" ]]; then
    cat "$MOCK_STDOUT_FILE"
fi

if [[ -n "${MOCK_STDERR_FILE:-}" && -f "$MOCK_STDERR_FILE" ]]; then
    cat "$MOCK_STDERR_FILE" >&2
fi

if [[ -n "${MOCK_SLEEP_SECONDS:-}" ]]; then
    sleep_pid=""
    trap 'if [[ -n "$sleep_pid" ]]; then kill -TERM "$sleep_pid" 2>/dev/null || true; fi; exit 143' TERM
    sleep "$MOCK_SLEEP_SECONDS" &
    sleep_pid=$!
    wait "$sleep_pid"
fi

exit "${MOCK_EXIT_CODE:-0}"
MOCK_EOF
    chmod +x "$mock_script"
    echo "$mock_script"
}

create_mock_copilot >/dev/null

reset_captures() {
    rm -f \
        "$TMPDIR_TEST/copilot_args" \
        "$TMPDIR_TEST/copilot_stdin" \
        "$TMPDIR_TEST/copilot_pid" \
        "$TMPDIR_TEST/no_copilot_err" \
        "$TMPDIR_TEST/no_jq_err" \
        "$TMPDIR_TEST/no_prompt_err"
}

run_wrapper() {
    TMPDIR_TEST="$TMPDIR_TEST" PATH="$TMPDIR_TEST:$PATH" "$@"
}

echo "running copilot-as-claude.sh tests"
echo ""

# ---------------------------------------------------------------------------
# test: signal passthrough
# ---------------------------------------------------------------------------
echo "test: signal passthrough"

cat > "$TMPDIR_TEST/signal_events.jsonl" <<'EOF'
{"type":"assistant.message_delta","data":{"messageId":"m1","deltaContent":"Working on the task...\n"}}
{"type":"assistant.message","data":{"messageId":"m1","content":"<<<RALPHEX:ALL_TASKS_DONE>>>"}} 
{"type":"session.task_complete","data":{"summary":"done","success":true}}
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/signal_events.jsonl" \
    run_wrapper bash "$WRAPPER" -p "test prompt" 2>/dev/null)

if echo "$output" | grep -q '<<<RALPHEX:ALL_TASKS_DONE>>>'; then
    pass "ALL_TASKS_DONE signal preserved in output"
else
    fail "ALL_TASKS_DONE signal not found in output" "got: $output"
fi

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

cat > "$TMPDIR_TEST/review_done_events.jsonl" <<'EOF'
{"type":"assistant.message","data":{"messageId":"m2","content":"<<<RALPHEX:REVIEW_DONE>>>"}} 
{"type":"session.idle","ephemeral":true,"data":{"aborted":false}}
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/review_done_events.jsonl" \
    run_wrapper bash "$WRAPPER" -p "review prompt <<<RALPHEX:REVIEW_DONE>>>" 2>/dev/null)

if echo "$output" | grep -q '<<<RALPHEX:REVIEW_DONE>>>'; then
    pass "REVIEW_DONE signal preserved in output"
else
    fail "REVIEW_DONE signal not found in output" "got: $output"
fi

# ---------------------------------------------------------------------------
# test: TASK_FAILED signal passthrough
# ---------------------------------------------------------------------------
echo "test: TASK_FAILED signal passthrough"

cat > "$TMPDIR_TEST/task_failed_events.jsonl" <<'EOF'
{"type":"assistant.message","data":{"messageId":"m3","content":"<<<RALPHEX:TASK_FAILED>>>"}} 
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/task_failed_events.jsonl" \
    run_wrapper bash "$WRAPPER" -p "failure prompt" 2>/dev/null)

if echo "$output" | grep -q '<<<RALPHEX:TASK_FAILED>>>'; then
    pass "TASK_FAILED signal preserved in output"
else
    fail "TASK_FAILED signal not found in output" "got: $output"
fi

# ---------------------------------------------------------------------------
# test: prompt via stdin and ignored flags
# ---------------------------------------------------------------------------
echo "test: stdin prompt handling and ignored flags"

reset_captures
cat > "$TMPDIR_TEST/minimal_events.jsonl" <<'EOF'
{"type":"assistant.message_delta","data":{"messageId":"m4","deltaContent":"done"}} 
{"type":"session.task_complete","data":{"summary":"done","success":true}}
EOF

output=$(printf '%s' "prompt from stdin" | \
    MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.jsonl" \
    run_wrapper bash "$WRAPPER" --dangerously-skip-permissions --output-format stream-json --verbose 2>/dev/null)

if echo "$output" | grep -q '"content_block_delta"'; then
    pass "stdin prompt produces output"
else
    fail "wrapper failed with stdin prompt" "got: $output"
fi

if [[ -f "$TMPDIR_TEST/copilot_stdin" ]] && grep -q '^prompt from stdin$' "$TMPDIR_TEST/copilot_stdin"; then
    pass "stdin prompt forwarded to copilot stdin"
else
    fail "stdin prompt not forwarded to copilot stdin" "captured: $(cat "$TMPDIR_TEST/copilot_stdin" 2>/dev/null || true)"
fi

# ---------------------------------------------------------------------------
# test: required args and prompt transport
# ---------------------------------------------------------------------------
echo "test: permission and JSON flag construction"

reset_captures
MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.jsonl" \
    run_wrapper bash "$WRAPPER" -p "argument prompt" >/dev/null 2>&1

if [[ -f "$TMPDIR_TEST/copilot_args" ]]; then
    recorded_args=$(tr '\n' ' ' < "$TMPDIR_TEST/copilot_args")
    if echo "$recorded_args" | grep -q -- '-s ' && \
        echo "$recorded_args" | grep -q -- '--output-format json' && \
        echo "$recorded_args" | grep -q -- '--stream on' && \
        echo "$recorded_args" | grep -q -- '--autopilot' && \
        echo "$recorded_args" | grep -q -- '--no-ask-user' && \
        echo "$recorded_args" | grep -q -- '--allow-all'; then
        pass "wrapper passes required Copilot JSON, autopilot, and autonomy flags"
    else
        fail "required Copilot flags missing" "args: $recorded_args"
    fi

    if echo "$recorded_args" | grep -q -- '-p'; then
        fail "prompt should not be passed via -p to copilot" "args: $recorded_args"
    else
        pass "prompt is not passed via -p"
    fi
else
    fail "could not capture copilot arguments"
fi

if [[ -f "$TMPDIR_TEST/copilot_stdin" ]] && grep -q '^argument prompt$' "$TMPDIR_TEST/copilot_stdin"; then
    pass "prompt passed to copilot via stdin"
else
    fail "prompt was not passed via stdin" "captured: $(cat "$TMPDIR_TEST/copilot_stdin" 2>/dev/null || true)"
fi

# ---------------------------------------------------------------------------
# test: stderr passthrough
# ---------------------------------------------------------------------------
echo "test: stderr passthrough"

cat > "$TMPDIR_TEST/stderr_content.txt" <<'EOF'
rate limit exceeded: too many requests
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.jsonl" \
    MOCK_STDERR_FILE="$TMPDIR_TEST/stderr_content.txt" \
    run_wrapper bash "$WRAPPER" -p "test prompt" 2>/dev/null)

if echo "$output" | grep -q 'rate limit exceeded'; then
    pass "stderr content emitted in output stream"
else
    fail "stderr content not found in output" "got: $output"
fi

stderr_event=$(echo "$output" | grep 'rate limit exceeded' | head -1)
if echo "$stderr_event" | jq -e '.type == "content_block_delta"' >/dev/null 2>&1; then
    pass "stderr emitted as content_block_delta event"
else
    fail "stderr not in content_block_delta event" "got: $stderr_event"
fi

# ---------------------------------------------------------------------------
# test: valid JSON translation
# ---------------------------------------------------------------------------
echo "test: valid JSON translation"

invalid_json=0
while IFS= read -r json_line; do
    [[ -z "$json_line" ]] && continue
    if ! echo "$json_line" | jq . >/dev/null 2>&1; then
        invalid_json=$((invalid_json + 1))
    fi
done <<< "$output"

if [[ $invalid_json -eq 0 ]]; then
    pass "all output lines are valid JSON"
else
    fail "$invalid_json output lines are not valid JSON"
fi

# ---------------------------------------------------------------------------
# test: fallback result event
# ---------------------------------------------------------------------------
echo "test: fallback result event"

cat > "$TMPDIR_TEST/no_terminal_events.jsonl" <<'EOF'
{"type":"assistant.message_delta","data":{"messageId":"m5","deltaContent":"partial output"}} 
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/no_terminal_events.jsonl" \
    run_wrapper bash "$WRAPPER" -p "test prompt" 2>/dev/null)

last_line=$(echo "$output" | tail -1)
if echo "$last_line" | jq -e '.type == "result"' >/dev/null 2>&1; then
    pass "fallback result event emitted when Copilot has no terminal event"
else
    fail "missing fallback result event" "last line: $last_line"
fi

# ---------------------------------------------------------------------------
# test: exit code preservation
# ---------------------------------------------------------------------------
echo "test: exit code preservation"

set +e
MOCK_STDOUT_FILE="$TMPDIR_TEST/no_terminal_events.jsonl" \
    MOCK_EXIT_CODE=1 \
    run_wrapper bash "$WRAPPER" -p "test prompt" >/dev/null 2>&1
exit_code=$?
set -e

if [[ $exit_code -eq 1 ]]; then
    pass "exit code 1 preserved on failure"
else
    fail "expected exit code 1" "got: $exit_code"
fi

set +e
MOCK_STDOUT_FILE="$TMPDIR_TEST/no_terminal_events.jsonl" \
    MOCK_EXIT_CODE=42 \
    run_wrapper bash "$WRAPPER" -p "test prompt" >/dev/null 2>&1
exit_code=$?
set -e

if [[ $exit_code -eq 42 ]]; then
    pass "non-zero exit code 42 preserved"
else
    fail "expected exit code 42" "got: $exit_code"
fi

# ---------------------------------------------------------------------------
# test: review adapter injection
# ---------------------------------------------------------------------------
echo "test: review adapter injection"

reset_captures
MOCK_STDOUT_FILE="$TMPDIR_TEST/review_done_events.jsonl" \
    run_wrapper bash "$WRAPPER" -p "review prompt <<<RALPHEX:REVIEW_DONE>>>" >/dev/null 2>&1

if [[ -f "$TMPDIR_TEST/copilot_stdin" ]]; then
    captured_prompt=$(cat "$TMPDIR_TEST/copilot_stdin")
    if echo "$captured_prompt" | grep -q 'Ralphex review adapter for GitHub Copilot CLI'; then
        pass "review adapter prepended to review prompt"
    else
        fail "review adapter not prepended" "prompt: $captured_prompt"
    fi
else
    fail "could not capture prompt sent to copilot"
fi

reset_captures
MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.jsonl" \
    run_wrapper bash "$WRAPPER" -p "regular task prompt" >/dev/null 2>&1

if [[ -f "$TMPDIR_TEST/copilot_stdin" ]]; then
    captured_prompt=$(cat "$TMPDIR_TEST/copilot_stdin")
    if echo "$captured_prompt" | grep -q 'Ralphex review adapter for GitHub Copilot CLI'; then
        fail "review adapter should not be added to non-review prompts" "prompt: $captured_prompt"
    else
        pass "review adapter omitted for non-review prompts"
    fi
fi

# ---------------------------------------------------------------------------
# test: missing prompt
# ---------------------------------------------------------------------------
echo "test: missing prompt exits with error"

set +e
run_wrapper bash "$WRAPPER" 2>"$TMPDIR_TEST/no_prompt_err"
no_prompt_exit=$?
set -e

if [[ $no_prompt_exit -ne 0 ]]; then
    pass "exits non-zero when no prompt provided"
else
    fail "expected non-zero exit when no prompt given" "got: $no_prompt_exit"
fi

if grep -q "no prompt provided" "$TMPDIR_TEST/no_prompt_err"; then
    pass "missing prompt error message is clear"
else
    fail "missing prompt error message not found" "stderr: $(cat "$TMPDIR_TEST/no_prompt_err" 2>/dev/null || true)"
fi

# ---------------------------------------------------------------------------
# test: missing copilot
# ---------------------------------------------------------------------------
echo "test: copilot not found"

set +e
no_copilot_bin="$TMPDIR_TEST/no_copilot_bin"
mkdir -p "$no_copilot_bin"
for tool in jq bash; do
    tool_path=$(command -v "$tool" 2>/dev/null) && ln -sf "$tool_path" "$no_copilot_bin/$tool"
done
PATH="$no_copilot_bin" bash "$WRAPPER" -p "test prompt" 2>"$TMPDIR_TEST/no_copilot_err"
no_copilot_exit=$?
rm -rf "$no_copilot_bin"
set -e

if [[ $no_copilot_exit -ne 0 ]]; then
    pass "exits non-zero when copilot is not available"
else
    fail "expected non-zero exit when copilot is missing" "got: $no_copilot_exit"
fi

if grep -q "copilot is required" "$TMPDIR_TEST/no_copilot_err"; then
    pass "missing copilot error message is clear"
else
    fail "missing copilot error message not found" "stderr: $(cat "$TMPDIR_TEST/no_copilot_err" 2>/dev/null || true)"
fi

# ---------------------------------------------------------------------------
# test: missing jq
# ---------------------------------------------------------------------------
echo "test: jq not found"

set +e
no_jq_bin="$TMPDIR_TEST/no_jq_bin"
mkdir -p "$no_jq_bin"
tool_path=$(command -v bash 2>/dev/null) && ln -sf "$tool_path" "$no_jq_bin/bash"
PATH="$no_jq_bin" bash "$WRAPPER" -p "test prompt" 2>"$TMPDIR_TEST/no_jq_err"
no_jq_exit=$?
rm -rf "$no_jq_bin"
set -e

if [[ $no_jq_exit -ne 0 ]]; then
    pass "exits non-zero when jq is not available"
else
    fail "expected non-zero exit when jq is missing" "got: $no_jq_exit"
fi

if grep -q "jq is required" "$TMPDIR_TEST/no_jq_err"; then
    pass "missing jq error message is clear"
else
    fail "missing jq error message not found" "stderr: $(cat "$TMPDIR_TEST/no_jq_err" 2>/dev/null || true)"
fi

# ---------------------------------------------------------------------------
# test: SIGTERM forwarding
# ---------------------------------------------------------------------------
echo "test: SIGTERM forwarding"

cat > "$TMPDIR_TEST/slow_events.jsonl" <<'EOF'
{"type":"assistant.message_delta","data":{"messageId":"m6","deltaContent":"starting"}} 
EOF

reset_captures
TMPDIR_TEST="$TMPDIR_TEST" PATH="$TMPDIR_TEST:$PATH" \
    MOCK_STDOUT_FILE="$TMPDIR_TEST/slow_events.jsonl" \
    MOCK_PID_FILE="$TMPDIR_TEST/copilot_pid" \
    MOCK_SLEEP_SECONDS=30 \
    bash "$WRAPPER" -p "slow prompt" >"$TMPDIR_TEST/sigterm_output" 2>&1 &
wrapper_pid=$!

for _ in $(seq 1 30); do
    if [[ -f "$TMPDIR_TEST/copilot_pid" ]]; then
        break
    fi
    sleep 0.1
done

if [[ -f "$TMPDIR_TEST/copilot_pid" ]]; then
    copilot_child_pid=$(cat "$TMPDIR_TEST/copilot_pid")
    kill -TERM "$wrapper_pid" 2>/dev/null || true

    set +e
    wait "$wrapper_pid"
    wrapper_exit=$?
    set -e

    sleep 0.5
    if kill -0 "$copilot_child_pid" 2>/dev/null; then
        sleep 1
    fi

    if kill -0 "$copilot_child_pid" 2>/dev/null; then
        fail "copilot child not terminated after SIGTERM" "child PID $copilot_child_pid still running"
        kill -9 "$copilot_child_pid" 2>/dev/null || true
    else
        pass "SIGTERM forwarded to copilot child process"
    fi

    if [[ $wrapper_exit -ne 0 ]]; then
        pass "wrapper exits non-zero after SIGTERM"
    else
        fail "wrapper should not exit 0 after SIGTERM" "got: $wrapper_exit"
    fi
else
    fail "could not detect copilot child PID" "pid file not created"
    kill -TERM "$wrapper_pid" 2>/dev/null || true
    wait "$wrapper_pid" 2>/dev/null || true
fi

echo ""
echo "results: $passed passed, $failed failed, $total total"

if [[ $failed -gt 0 ]]; then
    exit 1
fi
