#!/usr/bin/env bash
# gemini-as-claude_test.sh — tests for gemini-as-claude.sh wrapper.
#
# run from the ralphex directory:
#   bash scripts/gemini-as-claude/gemini-as-claude_test.sh
#
# requires: jq, bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WRAPPER="$SCRIPT_DIR/gemini-as-claude.sh"
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

# create a mock gemini script that emits predefined lines
create_mock_gemini() {
    local mock_script="$TMPDIR_TEST/gemini"
    cat > "$mock_script" << 'MOCK_EOF'
#!/usr/bin/env bash
# mock gemini: emit events based on env var MOCK_EVENTS or args
# MOCK_STDOUT_FILE: file containing text to emit on stdout
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

mock_gemini=$(create_mock_gemini)

echo "running gemini-as-claude.sh tests"
echo ""

# ---------------------------------------------------------------------------
# test: signal passthrough — text containing <<<RALPHEX:ALL_TASKS_DONE>>>
# must appear verbatim in output
# ---------------------------------------------------------------------------
echo "test: signal passthrough"

cat > "$TMPDIR_TEST/signal_events.txt" << 'EOF'
Working on the task...
<<<RALPHEX:ALL_TASKS_DONE>>>
Done!
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/signal_events.txt" \
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

cat > "$TMPDIR_TEST/review_events.txt" << 'EOF'
Review complete.
<<<RALPHEX:REVIEW_DONE>>>
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/review_events.txt" \
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

cat > "$TMPDIR_TEST/failed_events.txt" << 'EOF'
Something went wrong
<<<RALPHEX:TASK_FAILED>>>
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/failed_events.txt" \
    PATH="$TMPDIR_TEST:$PATH" \
    bash "$WRAPPER" -p "test prompt" 2>/dev/null)

if echo "$output" | grep -q '<<<RALPHEX:TASK_FAILED>>>'; then
    pass "TASK_FAILED signal preserved in output"
else
    fail "TASK_FAILED signal not found in output" "got: $output"
fi

# ---------------------------------------------------------------------------
# test: exit code preservation on success
# ---------------------------------------------------------------------------
echo "test: exit code preservation — success"

cat > "$TMPDIR_TEST/success_events.txt" << 'EOF'
done
EOF

MOCK_STDOUT_FILE="$TMPDIR_TEST/success_events.txt" \
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
# test: exit code preservation on failure
# ---------------------------------------------------------------------------
echo "test: exit code preservation — failure"

cat > "$TMPDIR_TEST/fail_events.txt" << 'EOF'
error occurred
EOF

set +e
MOCK_STDOUT_FILE="$TMPDIR_TEST/fail_events.txt" \
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
# test: non-standard exit code preservation
# ---------------------------------------------------------------------------
echo "test: exit code preservation — non-standard code"

set +e
MOCK_STDOUT_FILE="$TMPDIR_TEST/fail_events.txt" \
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
# test: stderr capture and emission as content_block_delta
# ---------------------------------------------------------------------------
echo "test: stderr capture"

cat > "$TMPDIR_TEST/minimal_events.txt" << 'EOF'
hello
EOF

cat > "$TMPDIR_TEST/stderr_content.txt" << 'EOF'
rate limit exceeded: too many requests
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.txt" \
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
# test: empty stderr produces no extra events
# ---------------------------------------------------------------------------
echo "test: empty stderr"

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.txt" \
    PATH="$TMPDIR_TEST:$PATH" \
    bash "$WRAPPER" -p "test prompt" 2>/dev/null)

# count events — should be: content_block_delta(hello) + result(fallback)
event_count=$(echo "$output" | grep -c '"type"' || true)
if [[ $event_count -le 2 ]]; then
    pass "no extra events from empty stderr ($event_count events)"
else
    fail "unexpected events from empty stderr" "got $event_count events: $output"
fi

# ---------------------------------------------------------------------------
# test: SIGTERM forwarding
# ---------------------------------------------------------------------------
echo "test: SIGTERM handling"

# create a mock gemini that writes its PID and sleeps
cat > "$TMPDIR_TEST/gemini_slow" << 'SLOW_EOF'
#!/usr/bin/env bash
echo $$ > "$TMPDIR_TEST/gemini_pid"
echo 'starting...'
sleep 30
SLOW_EOF
chmod +x "$TMPDIR_TEST/gemini_slow"

# copy the mock as "gemini" for this test
cp "$TMPDIR_TEST/gemini_slow" "$TMPDIR_TEST/gemini"

# run wrapper in background, send SIGTERM after gemini starts
PATH="$TMPDIR_TEST:$PATH" TMPDIR_TEST="$TMPDIR_TEST" \
    bash "$WRAPPER" -p "slow prompt" >"$TMPDIR_TEST/sigterm_output" 2>&1 &
wrapper_pid=$!

# wait for gemini to write its PID (up to 3 seconds)
for i in $(seq 1 30); do
    if [[ -f "$TMPDIR_TEST/gemini_pid" ]]; then
        break
    fi
    sleep 0.1
done

if [[ -f "$TMPDIR_TEST/gemini_pid" ]]; then
    gemini_child_pid=$(cat "$TMPDIR_TEST/gemini_pid")
    # send SIGTERM to the wrapper
    kill -TERM "$wrapper_pid" 2>/dev/null || true
    sleep 0.5

    # check if gemini child process was also terminated
    if kill -0 "$gemini_child_pid" 2>/dev/null; then
        # still running — give it a moment more
        sleep 1
        if kill -0 "$gemini_child_pid" 2>/dev/null; then
            fail "gemini child not terminated after SIGTERM" "child PID $gemini_child_pid still running"
            kill -9 "$gemini_child_pid" 2>/dev/null || true
        else
            pass "SIGTERM forwarded to gemini child process"
        fi
    else
        pass "SIGTERM forwarded to gemini child process"
    fi

    # verify the wrapper itself also exited
    if kill -0 "$wrapper_pid" 2>/dev/null; then
        fail "wrapper process did not exit after SIGTERM trap" "PID $wrapper_pid still running"
        kill -9 "$wrapper_pid" 2>/dev/null || true
    else
        pass "wrapper process exited promptly on SIGTERM"
        # check if exit code was 143 (128 + 15)
        wait "$wrapper_pid" 2>/dev/null || exit_code=$?
        if [[ "${exit_code:-0}" -eq 143 ]]; then
            pass "wrapper process exited with code 143"
        else
            fail "wrapper process expected to exit with code 143, got ${exit_code:-0}"
        fi
    fi
else
    fail "could not detect gemini child PID" "pid file not created"
fi

# clean up any remaining processes
wait "$wrapper_pid" 2>/dev/null || true
rm -f "$TMPDIR_TEST/gemini_pid"

# restore standard mock gemini after SIGTERM test
create_mock_gemini > /dev/null

# ---------------------------------------------------------------------------
# test: fallback result event always emitted
# ---------------------------------------------------------------------------
echo "test: fallback result event"

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.txt" \
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
# test: basic invocation — no prompt exits with error
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
# test: unknown flags are silently ignored
# ---------------------------------------------------------------------------
echo "test: unknown flags ignored"

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.txt" \
    PATH="$TMPDIR_TEST:$PATH" \
    bash "$WRAPPER" --dangerously-skip-permissions --output-format stream-json --verbose -p "test prompt" 2>/dev/null)

if echo "$output" | grep -q '"content_block_delta"'; then
    pass "unknown flags ignored, output produced normally"
else
    fail "wrapper failed with unknown flags" "got: $output"
fi

# ---------------------------------------------------------------------------
# test: all output lines are valid JSON
# ---------------------------------------------------------------------------
echo "test: JSON validity"

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/signal_events.txt" \
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
# test: large prompt (5000+ characters)
# ---------------------------------------------------------------------------
echo "test: large prompt"

# generate a 5500-character prompt
large_prompt=$(python3 -c "print('A' * 5500)" 2>/dev/null || printf '%5500s' '' | tr ' ' 'A')

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.txt" \
    PATH="$TMPDIR_TEST:$PATH" \
    bash "$WRAPPER" -p "$large_prompt" 2>/dev/null)

if echo "$output" | grep -q '"content_block_delta"'; then
    pass "large prompt (5500 chars) handled correctly"
else
    fail "wrapper failed with large prompt" "got: $output"
fi

# ---------------------------------------------------------------------------
# test: GEMINI_MODEL env var passed as --model
# ---------------------------------------------------------------------------
echo "test: GEMINI_MODEL"

# create a mock gemini that records its arguments
cat > "$TMPDIR_TEST/gemini" << 'MODEL_MOCK_EOF'
#!/usr/bin/env bash
echo "$@" > "$TMPDIR_TEST/gemini_args"
if [[ -n "${MOCK_STDOUT_FILE:-}" && -f "$MOCK_STDOUT_FILE" ]]; then
    cat "$MOCK_STDOUT_FILE"
fi
exit 0
MODEL_MOCK_EOF
chmod +x "$TMPDIR_TEST/gemini"

MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.txt" \
    GEMINI_MODEL="gemini-1.5-pro" \
    PATH="$TMPDIR_TEST:$PATH" TMPDIR_TEST="$TMPDIR_TEST" \
    bash "$WRAPPER" -p "test prompt" >/dev/null 2>&1

if [[ -f "$TMPDIR_TEST/gemini_args" ]]; then
    recorded_args=$(cat "$TMPDIR_TEST/gemini_args")
    if echo "$recorded_args" | grep -q -- "--model gemini-1.5-pro"; then
        pass "GEMINI_MODEL passed as --model flag"
    else
        fail "GEMINI_MODEL not passed correctly" "args: $recorded_args"
    fi
else
    fail "could not capture gemini arguments"
fi

# verify --model is NOT passed when GEMINI_MODEL is empty
rm -f "$TMPDIR_TEST/gemini_args"

MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.txt" \
    GEMINI_MODEL="" \
    PATH="$TMPDIR_TEST:$PATH" TMPDIR_TEST="$TMPDIR_TEST" \
    bash "$WRAPPER" -p "test prompt" >/dev/null 2>&1

if [[ -f "$TMPDIR_TEST/gemini_args" ]]; then
    recorded_args=$(cat "$TMPDIR_TEST/gemini_args")
    if echo "$recorded_args" | grep -q -- "--model"; then
        fail "--model passed when GEMINI_MODEL is empty" "args: $recorded_args"
    else
        pass "--model omitted when GEMINI_MODEL is empty"
    fi
fi

# restore standard mock
create_mock_gemini > /dev/null

# ---------------------------------------------------------------------------
# test: review adapter prepended for review prompts
# ---------------------------------------------------------------------------
echo "test: review adapter prepend"

# create arg-recording mock for this test
cat > "$TMPDIR_TEST/gemini" << 'ADAPTER_MOCK_EOF'
#!/usr/bin/env bash
for arg; do true; done
echo "$arg" > "$TMPDIR_TEST/captured_prompt"
if [[ -n "${MOCK_STDOUT_FILE:-}" && -f "$MOCK_STDOUT_FILE" ]]; then
    cat "$MOCK_STDOUT_FILE"
fi
exit 0
ADAPTER_MOCK_EOF
chmod +x "$TMPDIR_TEST/gemini"

rm -f "$TMPDIR_TEST/captured_prompt"
MOCK_STDOUT_FILE="$TMPDIR_TEST/review_events.txt" \
    PATH="$TMPDIR_TEST:$PATH" TMPDIR_TEST="$TMPDIR_TEST" \
    bash "$WRAPPER" -p "review prompt <<<RALPHEX:REVIEW_DONE>>>" >/dev/null 2>&1

if [[ -f "$TMPDIR_TEST/captured_prompt" ]]; then
    captured=$(cat "$TMPDIR_TEST/captured_prompt")
    if echo "$captured" | grep -q 'Ralphex review adapter for Gemini'; then
        pass "review adapter prepended to review prompt"
    else
        fail "review adapter not prepended" "prompt: $captured"
    fi
else
    fail "could not capture prompt sent to gemini"
fi

# verify adapter is NOT prepended for non-review prompts
rm -f "$TMPDIR_TEST/captured_prompt"
MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.txt" \
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
create_mock_gemini > /dev/null

# ---------------------------------------------------------------------------
# test: gemini not found exits with error
# ---------------------------------------------------------------------------
echo "test: gemini not found"

set +e
# create a restricted PATH with only required tools, excluding gemini.
no_gemini_bin="$TMPDIR_TEST/no_gemini_bin"
mkdir -p "$no_gemini_bin"
for tool in jq bash mktemp mkfifo cat rm kill env; do
    tool_path=$(command -v "$tool" 2>/dev/null) && ln -sf "$tool_path" "$no_gemini_bin/$tool"
done
PATH="$no_gemini_bin" bash "$WRAPPER" -p "test prompt" 2>"$TMPDIR_TEST/no_gemini_err"
no_gemini_exit=$?
rm -r "$no_gemini_bin"
set -e

if [[ $no_gemini_exit -ne 0 ]]; then
    pass "exits non-zero when gemini not found"
else
    fail "should exit non-zero when gemini not found" "got exit code 0"
fi

if grep -q "gemini is required" "$TMPDIR_TEST/no_gemini_err"; then
    pass "error message mentions gemini requirement"
else
    fail "no error about missing gemini" "stderr: $(cat "$TMPDIR_TEST/no_gemini_err")"
fi

# ---------------------------------------------------------------------------
# test: malformed/non-JSON input handled gracefully (plain text)
# ---------------------------------------------------------------------------
echo "test: malformed input resilience"

cat > "$TMPDIR_TEST/malformed_events.txt" << 'EOF'
not json at all
{broken json!!!
valid line
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/malformed_events.txt" \
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
