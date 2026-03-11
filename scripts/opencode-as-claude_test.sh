#!/usr/bin/env bash
# opencode-as-claude_test.sh — tests for opencode-as-claude.sh wrapper.
#
# run from the ralphex directory:
#   bash scripts/opencode-as-claude_test.sh
#
# requires: jq, bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WRAPPER="$SCRIPT_DIR/opencode-as-claude.sh"
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

# create a mock opencode script that emits predefined JSONL
create_mock_opencode() {
    local mock_script="$TMPDIR_TEST/opencode"
    cat > "$mock_script" << 'MOCK_EOF'
#!/usr/bin/env bash
# mock opencode: emit events based on env var MOCK_EVENTS or args
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

mock_opencode=$(create_mock_opencode)

echo "running opencode-as-claude.sh tests"
echo ""

# ---------------------------------------------------------------------------
# test: signal passthrough — text containing <<<RALPHEX:ALL_TASKS_DONE>>>
# must appear verbatim in output (R4)
# ---------------------------------------------------------------------------
echo "test: signal passthrough"

cat > "$TMPDIR_TEST/signal_events.jsonl" << 'EOF'
{"type":"step_start","timestamp":"2025-01-01T00:00:00Z","sessionID":"test","part":{"type":"step-start"}}
{"type":"text","timestamp":"2025-01-01T00:00:01Z","sessionID":"test","part":{"text":"Working on the task...\n"}}
{"type":"text","timestamp":"2025-01-01T00:00:02Z","sessionID":"test","part":{"text":"<<<RALPHEX:ALL_TASKS_DONE>>>\n"}}
{"type":"step_finish","timestamp":"2025-01-01T00:00:03Z","part":{"tokens":{},"cost":0}}
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
{"type":"text","timestamp":"2025-01-01T00:00:01Z","sessionID":"test","part":{"text":"Review complete.\n<<<RALPHEX:REVIEW_DONE>>>\n"}}
{"type":"step_finish","timestamp":"2025-01-01T00:00:02Z","part":{"tokens":{},"cost":0}}
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
# test: FAILED signal passthrough
# ---------------------------------------------------------------------------
echo "test: FAILED signal passthrough"

cat > "$TMPDIR_TEST/failed_events.jsonl" << 'EOF'
{"type":"text","timestamp":"2025-01-01T00:00:01Z","sessionID":"test","part":{"text":"Something went wrong\n<<<RALPHEX:TASK_FAILED>>>\n"}}
{"type":"step_finish","timestamp":"2025-01-01T00:00:02Z","part":{"tokens":{},"cost":0}}
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/failed_events.jsonl" \
    PATH="$TMPDIR_TEST:$PATH" \
    bash "$WRAPPER" -p "test prompt" 2>/dev/null)

if echo "$output" | grep -q '<<<RALPHEX:TASK_FAILED>>>'; then
    pass "TASK_FAILED signal preserved in output"
else
    fail "TASK_FAILED signal not found in output" "got: $output"
fi

# ---------------------------------------------------------------------------
# test: exit code preservation on success (R9)
# ---------------------------------------------------------------------------
echo "test: exit code preservation — success"

cat > "$TMPDIR_TEST/success_events.jsonl" << 'EOF'
{"type":"text","timestamp":"2025-01-01T00:00:01Z","sessionID":"test","part":{"text":"done\n"}}
{"type":"step_finish","timestamp":"2025-01-01T00:00:02Z","part":{"tokens":{},"cost":0}}
EOF

MOCK_STDOUT_FILE="$TMPDIR_TEST/success_events.jsonl" \
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
# test: exit code preservation on failure (R9)
# ---------------------------------------------------------------------------
echo "test: exit code preservation — failure"

cat > "$TMPDIR_TEST/fail_events.jsonl" << 'EOF'
{"type":"text","timestamp":"2025-01-01T00:00:01Z","sessionID":"test","part":{"text":"error occurred\n"}}
EOF

set +e
MOCK_STDOUT_FILE="$TMPDIR_TEST/fail_events.jsonl" \
    MOCK_EXIT_CODE=1 \
    PATH="$TMPDIR_TEST:$PATH" \
    bash "$WRAPPER" -p "test prompt" >/dev/null 2>&1
exit_code=$?
set -e

if [[ $exit_code -eq 1 ]]; then
    pass "exit code 1 preserved on failure"
else
    fail "expected exit code 1" "got: $exit_code"
fi

# ---------------------------------------------------------------------------
# test: non-standard exit code preservation (R9)
# ---------------------------------------------------------------------------
echo "test: exit code preservation — non-standard code"

set +e
MOCK_STDOUT_FILE="$TMPDIR_TEST/fail_events.jsonl" \
    MOCK_EXIT_CODE=42 \
    PATH="$TMPDIR_TEST:$PATH" \
    bash "$WRAPPER" -p "test prompt" >/dev/null 2>&1
exit_code=$?
set -e

if [[ $exit_code -eq 42 ]]; then
    pass "exit code 42 preserved"
else
    fail "expected exit code 42" "got: $exit_code"
fi

# ---------------------------------------------------------------------------
# test: stderr capture and emission as content_block_delta (R6)
# ---------------------------------------------------------------------------
echo "test: stderr capture"

cat > "$TMPDIR_TEST/minimal_events.jsonl" << 'EOF'
{"type":"text","timestamp":"2025-01-01T00:00:01Z","sessionID":"test","part":{"text":"hello\n"}}
{"type":"step_finish","timestamp":"2025-01-01T00:00:02Z","part":{"tokens":{},"cost":0}}
EOF

cat > "$TMPDIR_TEST/stderr_content.txt" << 'EOF'
rate limit exceeded: too many requests
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.jsonl" \
    MOCK_STDERR_FILE="$TMPDIR_TEST/stderr_content.txt" \
    PATH="$TMPDIR_TEST:$PATH" \
    bash "$WRAPPER" -p "test prompt" 2>/dev/null)

if echo "$output" | grep -q 'rate limit exceeded'; then
    pass "stderr content emitted in output stream"
else
    fail "stderr content not found in output" "got: $output"
fi

# verify stderr appears as content_block_delta
stderr_event=$(echo "$output" | grep 'rate limit exceeded' | head -1)
if echo "$stderr_event" | jq -e '.type == "content_block_delta"' >/dev/null 2>&1; then
    pass "stderr emitted as content_block_delta event"
else
    fail "stderr not in content_block_delta event" "got: $stderr_event"
fi

# ---------------------------------------------------------------------------
# test: stderr with API error pattern (R6)
# ---------------------------------------------------------------------------
echo "test: stderr API error pattern"

cat > "$TMPDIR_TEST/api_error_stderr.txt" << 'EOF'
API Error: invalid api key for model anthropic/claude-3
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.jsonl" \
    MOCK_STDERR_FILE="$TMPDIR_TEST/api_error_stderr.txt" \
    PATH="$TMPDIR_TEST:$PATH" \
    bash "$WRAPPER" -p "test prompt" 2>/dev/null)

if echo "$output" | grep -q 'API Error:'; then
    pass "API Error pattern preserved for detection"
else
    fail "API Error pattern not found" "got: $output"
fi

# ---------------------------------------------------------------------------
# test: empty stderr produces no extra events
# ---------------------------------------------------------------------------
echo "test: empty stderr"

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.jsonl" \
    PATH="$TMPDIR_TEST:$PATH" \
    bash "$WRAPPER" -p "test prompt" 2>/dev/null)

# count events — should be: content_block_delta(hello) + result(step_finish) + result(fallback)
event_count=$(echo "$output" | grep -c '"type"' || true)
if [[ $event_count -le 3 ]]; then
    pass "no extra events from empty stderr ($event_count events)"
else
    fail "unexpected events from empty stderr" "got $event_count events: $output"
fi

# ---------------------------------------------------------------------------
# test: text content verbatim — special characters not mangled (R4)
# ---------------------------------------------------------------------------
echo "test: special characters preserved"

cat > "$TMPDIR_TEST/special_events.jsonl" << 'EOF'
{"type":"text","timestamp":"2025-01-01T00:00:01Z","sessionID":"test","part":{"text":"line with \"quotes\" and <angle> & ampersand\n"}}
{"type":"text","timestamp":"2025-01-01T00:00:02Z","sessionID":"test","part":{"text":"unicode: café, naïve, résumé\n"}}
{"type":"step_finish","timestamp":"2025-01-01T00:00:03Z","part":{"tokens":{},"cost":0}}
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/special_events.jsonl" \
    PATH="$TMPDIR_TEST:$PATH" \
    bash "$WRAPPER" -p "test prompt" 2>/dev/null)

if echo "$output" | grep -q 'café'; then
    pass "unicode characters preserved"
else
    fail "unicode characters lost" "got: $output"
fi

if echo "$output" | jq -r 'select(.type=="content_block_delta") | .delta.text' 2>/dev/null | grep -q '<angle>'; then
    pass "angle brackets preserved"
else
    fail "angle brackets lost" "got: $output"
fi

# ---------------------------------------------------------------------------
# test: SIGTERM forwarding
# ---------------------------------------------------------------------------
echo "test: SIGTERM handling"

# create a mock opencode that writes its PID and sleeps
cat > "$TMPDIR_TEST/opencode_slow" << 'SLOW_EOF'
#!/usr/bin/env bash
echo $$ > "$TMPDIR_TEST/opencode_pid"
echo '{"type":"text","timestamp":"2025-01-01T00:00:01Z","sessionID":"test","part":{"text":"starting...\n"}}'
sleep 30
SLOW_EOF
chmod +x "$TMPDIR_TEST/opencode_slow"

# copy the mock as "opencode" for this test
cp "$TMPDIR_TEST/opencode_slow" "$TMPDIR_TEST/opencode"

# run wrapper in background, send SIGTERM after opencode starts
PATH="$TMPDIR_TEST:$PATH" TMPDIR_TEST="$TMPDIR_TEST" \
    bash "$WRAPPER" -p "slow prompt" >"$TMPDIR_TEST/sigterm_output" 2>&1 &
wrapper_pid=$!

# wait for opencode to write its PID (up to 3 seconds)
for i in $(seq 1 30); do
    if [[ -f "$TMPDIR_TEST/opencode_pid" ]]; then
        break
    fi
    sleep 0.1
done

if [[ -f "$TMPDIR_TEST/opencode_pid" ]]; then
    opencode_child_pid=$(cat "$TMPDIR_TEST/opencode_pid")
    # send SIGTERM to the wrapper
    kill -TERM "$wrapper_pid" 2>/dev/null || true
    sleep 0.5

    # check if opencode child process was also terminated
    if kill -0 "$opencode_child_pid" 2>/dev/null; then
        # still running — give it a moment more
        sleep 1
        if kill -0 "$opencode_child_pid" 2>/dev/null; then
            fail "opencode child not terminated after SIGTERM" "child PID $opencode_child_pid still running"
            kill -9 "$opencode_child_pid" 2>/dev/null || true
        else
            pass "SIGTERM forwarded to opencode child process"
        fi
    else
        pass "SIGTERM forwarded to opencode child process"
    fi
else
    fail "could not detect opencode child PID" "pid file not created"
fi

# clean up any remaining processes
wait "$wrapper_pid" 2>/dev/null || true
rm -f "$TMPDIR_TEST/opencode_pid"

# restore standard mock opencode after SIGTERM test
create_mock_opencode > /dev/null

# ---------------------------------------------------------------------------
# test: fallback result event always emitted
# ---------------------------------------------------------------------------
echo "test: fallback result event"

# events without step_finish
cat > "$TMPDIR_TEST/no_finish_events.jsonl" << 'EOF'
{"type":"text","timestamp":"2025-01-01T00:00:01Z","sessionID":"test","part":{"text":"partial output\n"}}
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/no_finish_events.jsonl" \
    PATH="$TMPDIR_TEST:$PATH" \
    bash "$WRAPPER" -p "test prompt" 2>/dev/null)

# the last line should be a result event
last_line=$(echo "$output" | tail -1)
if echo "$last_line" | jq -e '.type == "result"' >/dev/null 2>&1; then
    pass "fallback result event emitted at end of stream"
else
    fail "no fallback result event" "last line: $last_line"
fi

# ---------------------------------------------------------------------------
# test: basic invocation — no prompt exits with error (R1)
# ---------------------------------------------------------------------------
echo "test: basic invocation — no prompt"

set +e
PATH="$TMPDIR_TEST:$PATH" bash "$WRAPPER" 2>"$TMPDIR_TEST/no_prompt_err"
no_prompt_exit=$?
set -e

if [[ $no_prompt_exit -ne 0 ]]; then
    pass "exits non-zero without -p flag"
else
    fail "should exit non-zero without -p flag" "got exit code 0"
fi

if grep -q "no prompt provided" "$TMPDIR_TEST/no_prompt_err"; then
    pass "error message mentions missing prompt"
else
    fail "no error message about missing prompt" "stderr: $(cat "$TMPDIR_TEST/no_prompt_err")"
fi

# ---------------------------------------------------------------------------
# test: unknown flags are silently ignored (R1)
# ---------------------------------------------------------------------------
echo "test: unknown flags ignored"

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.jsonl" \
    PATH="$TMPDIR_TEST:$PATH" \
    bash "$WRAPPER" --dangerously-skip-permissions --output-format stream-json --verbose -p "test prompt" 2>/dev/null)

if echo "$output" | grep -q '"content_block_delta"'; then
    pass "unknown flags ignored, output produced normally"
else
    fail "wrapper failed with unknown flags" "got: $output"
fi

# ---------------------------------------------------------------------------
# test: all output lines are valid JSON (R2)
# ---------------------------------------------------------------------------
echo "test: JSON validity"

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/signal_events.jsonl" \
    PATH="$TMPDIR_TEST:$PATH" \
    bash "$WRAPPER" -p "test prompt" 2>/dev/null)

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
    fail "$invalid_json lines are not valid JSON"
fi

# ---------------------------------------------------------------------------
# test: large prompt (5000+ characters) (R1)
# ---------------------------------------------------------------------------
echo "test: large prompt"

# generate a 5500-character prompt
large_prompt=$(python3 -c "print('A' * 5500)" 2>/dev/null || printf '%5500s' '' | tr ' ' 'A')

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.jsonl" \
    PATH="$TMPDIR_TEST:$PATH" \
    bash "$WRAPPER" -p "$large_prompt" 2>/dev/null)

if echo "$output" | grep -q '"content_block_delta"'; then
    pass "large prompt (5500 chars) handled correctly"
else
    fail "wrapper failed with large prompt" "got: $output"
fi

# ---------------------------------------------------------------------------
# test: OPENCODE_MODEL env var (R5)
# ---------------------------------------------------------------------------
echo "test: OPENCODE_MODEL"

# create a mock opencode that records its arguments
cat > "$TMPDIR_TEST/opencode" << 'MODEL_MOCK_EOF'
#!/usr/bin/env bash
echo "$@" > "$TMPDIR_TEST/opencode_args"
if [[ -n "${MOCK_STDOUT_FILE:-}" && -f "$MOCK_STDOUT_FILE" ]]; then
    cat "$MOCK_STDOUT_FILE"
fi
exit 0
MODEL_MOCK_EOF
chmod +x "$TMPDIR_TEST/opencode"

MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.jsonl" \
    OPENCODE_MODEL="openai/gpt-4o" \
    PATH="$TMPDIR_TEST:$PATH" TMPDIR_TEST="$TMPDIR_TEST" \
    bash "$WRAPPER" -p "test prompt" >/dev/null 2>&1

if [[ -f "$TMPDIR_TEST/opencode_args" ]]; then
    recorded_args=$(cat "$TMPDIR_TEST/opencode_args")
    if echo "$recorded_args" | grep -q -- "--model openai/gpt-4o"; then
        pass "OPENCODE_MODEL passed as --model flag"
    else
        fail "OPENCODE_MODEL not passed correctly" "args: $recorded_args"
    fi
else
    fail "could not capture opencode arguments"
fi

# verify --model is NOT passed when OPENCODE_MODEL is empty
rm -f "$TMPDIR_TEST/opencode_args"
# restore the standard mock
create_mock_opencode > /dev/null

cat > "$TMPDIR_TEST/opencode" << 'NO_MODEL_MOCK_EOF'
#!/usr/bin/env bash
echo "$@" > "$TMPDIR_TEST/opencode_args"
if [[ -n "${MOCK_STDOUT_FILE:-}" && -f "$MOCK_STDOUT_FILE" ]]; then
    cat "$MOCK_STDOUT_FILE"
fi
exit 0
NO_MODEL_MOCK_EOF
chmod +x "$TMPDIR_TEST/opencode"

MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.jsonl" \
    OPENCODE_MODEL="" \
    PATH="$TMPDIR_TEST:$PATH" TMPDIR_TEST="$TMPDIR_TEST" \
    bash "$WRAPPER" -p "test prompt" >/dev/null 2>&1

if [[ -f "$TMPDIR_TEST/opencode_args" ]]; then
    recorded_args=$(cat "$TMPDIR_TEST/opencode_args")
    if echo "$recorded_args" | grep -q -- "--model"; then
        fail "--model passed when OPENCODE_MODEL is empty" "args: $recorded_args"
    else
        pass "--model omitted when OPENCODE_MODEL is empty"
    fi
fi

# restore standard mock for any remaining tests
create_mock_opencode > /dev/null

# ---------------------------------------------------------------------------
# test: OPENCODE_VERBOSE=1 includes step_start events
# ---------------------------------------------------------------------------
echo "test: OPENCODE_VERBOSE=1 includes step_start"

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/signal_events.jsonl" \
    OPENCODE_VERBOSE=1 \
    PATH="$TMPDIR_TEST:$PATH" \
    bash "$WRAPPER" -p "test prompt" 2>/dev/null)

if echo "$output" | grep -q '\[step started\]'; then
    pass "step_start event included with OPENCODE_VERBOSE=1"
else
    fail "step_start event missing with OPENCODE_VERBOSE=1" "got: $output"
fi

# ---------------------------------------------------------------------------
# test: OPENCODE_VERBOSE=0 excludes step_start events
# ---------------------------------------------------------------------------
echo "test: OPENCODE_VERBOSE=0 excludes step_start"

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/signal_events.jsonl" \
    OPENCODE_VERBOSE=0 \
    PATH="$TMPDIR_TEST:$PATH" \
    bash "$WRAPPER" -p "test prompt" 2>/dev/null)

if echo "$output" | grep -q '\[step started\]'; then
    fail "step_start event should be excluded with OPENCODE_VERBOSE=0" "got: $output"
else
    pass "step_start event excluded with OPENCODE_VERBOSE=0"
fi

# also verify that the raw JSON line itself is not leaked
if echo "$output" | grep -q '"type":"step_start"'; then
    fail "raw step_start JSON should not leak with OPENCODE_VERBOSE=0" "got: $output"
else
    pass "raw step_start JSON suppressed with OPENCODE_VERBOSE=0"
fi

# ---------------------------------------------------------------------------
# test: OPENCODE_VERBOSE invalid value warns and defaults to 0
# ---------------------------------------------------------------------------
echo "test: OPENCODE_VERBOSE invalid value"

stderr_out=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/signal_events.jsonl" \
    OPENCODE_VERBOSE=foo \
    PATH="$TMPDIR_TEST:$PATH" \
    bash "$WRAPPER" -p "test prompt" 2>&1 >/dev/null)

if echo "$stderr_out" | grep -q 'warning.*OPENCODE_VERBOSE'; then
    pass "invalid OPENCODE_VERBOSE produces warning"
else
    fail "no warning for invalid OPENCODE_VERBOSE" "stderr: $stderr_out"
fi

# ---------------------------------------------------------------------------
# test: review adapter prepended for review prompts
# ---------------------------------------------------------------------------
echo "test: review adapter prepend"

# create arg-recording mock for this test
cat > "$TMPDIR_TEST/opencode" << 'ADAPTER_MOCK_EOF'
#!/usr/bin/env bash
# record the last positional argument (the prompt)
for arg; do true; done
echo "$arg" > "$TMPDIR_TEST/captured_prompt"
if [[ -n "${MOCK_STDOUT_FILE:-}" && -f "$MOCK_STDOUT_FILE" ]]; then
    cat "$MOCK_STDOUT_FILE"
fi
exit 0
ADAPTER_MOCK_EOF
chmod +x "$TMPDIR_TEST/opencode"

rm -f "$TMPDIR_TEST/captured_prompt"
MOCK_STDOUT_FILE="$TMPDIR_TEST/review_events.jsonl" \
    PATH="$TMPDIR_TEST:$PATH" TMPDIR_TEST="$TMPDIR_TEST" \
    bash "$WRAPPER" -p "review prompt <<<RALPHEX:REVIEW_DONE>>>" >/dev/null 2>&1

if [[ -f "$TMPDIR_TEST/captured_prompt" ]]; then
    captured=$(cat "$TMPDIR_TEST/captured_prompt")
    if echo "$captured" | grep -q 'Ralphex review adapter for OpenCode'; then
        pass "review adapter prepended to review prompt"
    else
        fail "review adapter not prepended" "prompt: $captured"
    fi
else
    fail "could not capture prompt sent to opencode"
fi

# verify adapter is NOT prepended for non-review prompts
rm -f "$TMPDIR_TEST/captured_prompt"
MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.jsonl" \
    PATH="$TMPDIR_TEST:$PATH" TMPDIR_TEST="$TMPDIR_TEST" \
    bash "$WRAPPER" -p "regular task prompt" >/dev/null 2>&1

if [[ -f "$TMPDIR_TEST/captured_prompt" ]]; then
    captured=$(cat "$TMPDIR_TEST/captured_prompt")
    if echo "$captured" | grep -q 'Ralphex review adapter'; then
        fail "review adapter should NOT be prepended for non-review prompt" "prompt: $captured"
    else
        pass "review adapter not prepended for non-review prompt"
    fi
fi

# restore standard mock
create_mock_opencode > /dev/null

# ---------------------------------------------------------------------------
# test: opencode not found exits with error
# ---------------------------------------------------------------------------
echo "test: opencode not found"

set +e
# use a minimal PATH containing only jq's and bash's directories (no opencode)
jq_dir=$(dirname "$(command -v jq)")
bash_dir=$(dirname "$(command -v bash)")
PATH="$jq_dir:$bash_dir" bash "$WRAPPER" -p "test prompt" 2>"$TMPDIR_TEST/no_opencode_err"
no_opencode_exit=$?
set -e

if [[ $no_opencode_exit -ne 0 ]]; then
    pass "exits non-zero when opencode not found"
else
    fail "should exit non-zero when opencode not found" "got exit code 0"
fi

if grep -q "opencode is required" "$TMPDIR_TEST/no_opencode_err"; then
    pass "error message mentions opencode requirement"
else
    fail "no error about missing opencode" "stderr: $(cat "$TMPDIR_TEST/no_opencode_err")"
fi

# ---------------------------------------------------------------------------
# test: auto-allow permissions set when OPENCODE_CONFIG_CONTENT is empty
# ---------------------------------------------------------------------------
echo "test: auto-allow permissions — default"

# create a mock that records OPENCODE_CONFIG_CONTENT
cat > "$TMPDIR_TEST/opencode" << 'PERM_MOCK_EOF'
#!/usr/bin/env bash
echo "$OPENCODE_CONFIG_CONTENT" > "$TMPDIR_TEST/captured_config_content"
if [[ -n "${MOCK_STDOUT_FILE:-}" && -f "$MOCK_STDOUT_FILE" ]]; then
    cat "$MOCK_STDOUT_FILE"
fi
exit 0
PERM_MOCK_EOF
chmod +x "$TMPDIR_TEST/opencode"

rm -f "$TMPDIR_TEST/captured_config_content"
MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.jsonl" \
    OPENCODE_CONFIG_CONTENT="" \
    PATH="$TMPDIR_TEST:$PATH" TMPDIR_TEST="$TMPDIR_TEST" \
    bash "$WRAPPER" -p "test prompt" >/dev/null 2>&1

if [[ -f "$TMPDIR_TEST/captured_config_content" ]]; then
    captured=$(cat "$TMPDIR_TEST/captured_config_content")
    if echo "$captured" | jq -e '.permission["*"] == "allow"' >/dev/null 2>&1; then
        pass "OPENCODE_CONFIG_CONTENT set with allow-all permissions"
    else
        fail "allow-all permissions not set" "got: $captured"
    fi
else
    fail "could not capture OPENCODE_CONFIG_CONTENT"
fi

# ---------------------------------------------------------------------------
# test: auto-allow permissions merged with existing OPENCODE_CONFIG_CONTENT
# ---------------------------------------------------------------------------
echo "test: auto-allow permissions — merge with existing"

rm -f "$TMPDIR_TEST/captured_config_content"
MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.jsonl" \
    OPENCODE_CONFIG_CONTENT='{"model":"test/model"}' \
    PATH="$TMPDIR_TEST:$PATH" TMPDIR_TEST="$TMPDIR_TEST" \
    bash "$WRAPPER" -p "test prompt" >/dev/null 2>&1

if [[ -f "$TMPDIR_TEST/captured_config_content" ]]; then
    captured=$(cat "$TMPDIR_TEST/captured_config_content")
    if echo "$captured" | jq -e '.permission["*"] == "allow" and .model == "test/model"' >/dev/null 2>&1; then
        pass "allow-all merged with existing config content"
    else
        fail "merge failed" "got: $captured"
    fi
else
    fail "could not capture OPENCODE_CONFIG_CONTENT"
fi

# restore standard mock
create_mock_opencode > /dev/null

# ---------------------------------------------------------------------------
# test: malformed input lines are handled gracefully
# ---------------------------------------------------------------------------
echo "test: malformed input resilience"

cat > "$TMPDIR_TEST/malformed_events.jsonl" << 'EOF'
not json at all
{"type":"text","timestamp":"2025-01-01T00:00:01Z","sessionID":"test","part":{"text":"valid line\n"}}
{broken json!!!
{"type":"step_finish","timestamp":"2025-01-01T00:00:02Z","part":{"tokens":{},"cost":0}}
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/malformed_events.jsonl" \
    PATH="$TMPDIR_TEST:$PATH" \
    bash "$WRAPPER" -p "test prompt" 2>/dev/null)

if echo "$output" | grep -q 'valid line'; then
    pass "valid events processed despite malformed lines"
else
    fail "valid events lost due to malformed input" "got: $output"
fi

# verify wrapper didn't crash — should have result event at end
last_line=$(echo "$output" | tail -1)
if echo "$last_line" | jq -e '.type == "result"' >/dev/null 2>&1; then
    pass "wrapper completes normally with malformed input"
else
    fail "wrapper did not complete normally" "last line: $last_line"
fi

# ---------------------------------------------------------------------------
# summary
# ---------------------------------------------------------------------------
echo ""
echo "results: $passed passed, $failed failed, $total total"

if [[ $failed -gt 0 ]]; then
    exit 1
fi
