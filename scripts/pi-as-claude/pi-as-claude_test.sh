#!/usr/bin/env bash
# pi-as-claude_test.sh — tests for pi-as-claude.sh wrapper.
#
# run from the ralphex directory:
#   bash scripts/pi-as-claude/pi-as-claude_test.sh
#
# requires: jq, bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WRAPPER="$SCRIPT_DIR/pi-as-claude.sh"
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

# create a mock pi script that records its arguments and emits predefined stdout.
# MOCK_STDOUT_FILE: file containing text to emit on stdout
# MOCK_STDERR_FILE: file containing text to emit on stderr
# MOCK_EXIT_CODE:   exit code to return (default 0)
# pi_args:          arguments written to $TMPDIR_TEST/pi_args
create_mock_pi() {
    local mock_script="$TMPDIR_TEST/pi"
    cat > "$mock_script" << 'MOCK_EOF'
#!/usr/bin/env bash
echo "$@" > "$TMPDIR_TEST/pi_args"
# capture the last positional arg (the prompt) separately for assertions
for arg; do true; done
printf '%s' "$arg" > "$TMPDIR_TEST/pi_prompt"

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

create_mock_pi > /dev/null

# minimal valid pi event stream: one assistant text delta produces output.
cat > "$TMPDIR_TEST/minimal_events.txt" << 'EOF'
{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"hello"}}
{"type":"turn_end"}
EOF

run_wrapper() {
    # helper: run wrapper with mock pi on PATH; args forwarded to wrapper
    MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.txt" \
        PATH="$TMPDIR_TEST:$PATH" TMPDIR_TEST="$TMPDIR_TEST" \
        bash "$WRAPPER" "$@"
}

echo "running pi-as-claude.sh tests"
echo ""

# ---------------------------------------------------------------------------
# test: pi launched with --mode json --print and prompt as positional arg
# ---------------------------------------------------------------------------
echo "test: pi invocation flags"

rm -f "$TMPDIR_TEST/pi_args"
run_wrapper -p "test prompt" >/dev/null 2>&1

recorded=$(cat "$TMPDIR_TEST/pi_args")
if echo "$recorded" | grep -q -- "--mode json"; then
    pass "pi invoked with --mode json"
else
    fail "pi not invoked with --mode json" "args: $recorded"
fi

if echo "$recorded" | grep -q -- "--print"; then
    pass "pi invoked with --print"
else
    fail "pi not invoked with --print" "args: $recorded"
fi

if [[ "$(cat "$TMPDIR_TEST/pi_prompt")" == "test prompt" ]]; then
    pass "prompt passed as positional arg"
else
    fail "prompt not passed as positional arg" "got: $(cat "$TMPDIR_TEST/pi_prompt")"
fi

# ---------------------------------------------------------------------------
# test: --model flag forwarded to pi --model
# ---------------------------------------------------------------------------
echo "test: --model forwarding"

rm -f "$TMPDIR_TEST/pi_args"
run_wrapper --model "anthropic/claude-x" -p "test prompt" >/dev/null 2>&1

recorded=$(cat "$TMPDIR_TEST/pi_args")
if echo "$recorded" | grep -q -- "--model anthropic/claude-x"; then
    pass "--model forwarded to pi"
else
    fail "--model not forwarded" "args: $recorded"
fi

# ---------------------------------------------------------------------------
# test: PI_MODEL env used when --model flag absent
# ---------------------------------------------------------------------------
echo "test: PI_MODEL env"

rm -f "$TMPDIR_TEST/pi_args"
MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.txt" \
    PI_MODEL="google/gemini-x" \
    PATH="$TMPDIR_TEST:$PATH" TMPDIR_TEST="$TMPDIR_TEST" \
    bash "$WRAPPER" -p "test prompt" >/dev/null 2>&1

recorded=$(cat "$TMPDIR_TEST/pi_args")
if echo "$recorded" | grep -q -- "--model google/gemini-x"; then
    pass "PI_MODEL used as --model when flag absent"
else
    fail "PI_MODEL not used" "args: $recorded"
fi

# --model flag wins over PI_MODEL
rm -f "$TMPDIR_TEST/pi_args"
MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.txt" \
    PI_MODEL="google/gemini-x" \
    PATH="$TMPDIR_TEST:$PATH" TMPDIR_TEST="$TMPDIR_TEST" \
    bash "$WRAPPER" --model "anthropic/claude-x" -p "test prompt" >/dev/null 2>&1

recorded=$(cat "$TMPDIR_TEST/pi_args")
if echo "$recorded" | grep -q -- "--model anthropic/claude-x" && ! echo "$recorded" | grep -q -- "google/gemini-x"; then
    pass "--model flag overrides PI_MODEL"
else
    fail "--model did not override PI_MODEL" "args: $recorded"
fi

# no --model when neither flag nor env set
rm -f "$TMPDIR_TEST/pi_args"
run_wrapper -p "test prompt" >/dev/null 2>&1
recorded=$(cat "$TMPDIR_TEST/pi_args")
if echo "$recorded" | grep -q -- "--model"; then
    fail "--model present when no model configured" "args: $recorded"
else
    pass "--model omitted when no model configured"
fi

# ---------------------------------------------------------------------------
# test: PI_PROVIDER env forwarded as --provider
# ---------------------------------------------------------------------------
echo "test: PI_PROVIDER env"

rm -f "$TMPDIR_TEST/pi_args"
MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.txt" \
    PI_PROVIDER="anthropic" \
    PATH="$TMPDIR_TEST:$PATH" TMPDIR_TEST="$TMPDIR_TEST" \
    bash "$WRAPPER" -p "test prompt" >/dev/null 2>&1

recorded=$(cat "$TMPDIR_TEST/pi_args")
if echo "$recorded" | grep -q -- "--provider anthropic"; then
    pass "PI_PROVIDER forwarded as --provider"
else
    fail "PI_PROVIDER not forwarded" "args: $recorded"
fi

# no --provider when env unset
rm -f "$TMPDIR_TEST/pi_args"
run_wrapper -p "test prompt" >/dev/null 2>&1
recorded=$(cat "$TMPDIR_TEST/pi_args")
if echo "$recorded" | grep -q -- "--provider"; then
    fail "--provider present when PI_PROVIDER unset" "args: $recorded"
else
    pass "--provider omitted when PI_PROVIDER unset"
fi

# ---------------------------------------------------------------------------
# test: --effort → --thinking mapping (passthrough levels)
# ---------------------------------------------------------------------------
echo "test: effort to thinking mapping"

for level in off minimal low medium high xhigh; do
    rm -f "$TMPDIR_TEST/pi_args"
    run_wrapper --effort "$level" -p "test prompt" >/dev/null 2>&1
    recorded=$(cat "$TMPDIR_TEST/pi_args")
    if echo "$recorded" | grep -q -- "--thinking $level"; then
        pass "effort '$level' mapped to --thinking $level"
    else
        fail "effort '$level' not mapped" "args: $recorded"
    fi
done

# ---------------------------------------------------------------------------
# test: --effort max → --thinking xhigh with stderr note
# ---------------------------------------------------------------------------
echo "test: effort max maps to xhigh with note"

rm -f "$TMPDIR_TEST/pi_args"
err_out=$(run_wrapper --effort max -p "test prompt" 2>&1 >/dev/null)
recorded=$(cat "$TMPDIR_TEST/pi_args")
if echo "$recorded" | grep -q -- "--thinking xhigh"; then
    pass "effort max mapped to --thinking xhigh"
else
    fail "effort max not mapped to xhigh" "args: $recorded"
fi

if echo "$err_out" | grep -qi "no 'max' thinking level"; then
    pass "max effort prints stderr note"
else
    fail "max effort note missing" "stderr: $err_out"
fi

# ---------------------------------------------------------------------------
# test: PI_THINKING env used when --effort flag absent
# ---------------------------------------------------------------------------
echo "test: PI_THINKING env"

rm -f "$TMPDIR_TEST/pi_args"
MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.txt" \
    PI_THINKING="medium" \
    PATH="$TMPDIR_TEST:$PATH" TMPDIR_TEST="$TMPDIR_TEST" \
    bash "$WRAPPER" -p "test prompt" >/dev/null 2>&1

recorded=$(cat "$TMPDIR_TEST/pi_args")
if echo "$recorded" | grep -q -- "--thinking medium"; then
    pass "PI_THINKING used as --thinking when flag absent"
else
    fail "PI_THINKING not used" "args: $recorded"
fi

# --effort flag wins over PI_THINKING
rm -f "$TMPDIR_TEST/pi_args"
MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.txt" \
    PI_THINKING="medium" \
    PATH="$TMPDIR_TEST:$PATH" TMPDIR_TEST="$TMPDIR_TEST" \
    bash "$WRAPPER" --effort high -p "test prompt" >/dev/null 2>&1

recorded=$(cat "$TMPDIR_TEST/pi_args")
if echo "$recorded" | grep -q -- "--thinking high" && ! echo "$recorded" | grep -q -- "--thinking medium"; then
    pass "--effort overrides PI_THINKING"
else
    fail "--effort did not override PI_THINKING" "args: $recorded"
fi

# no --thinking when neither set
rm -f "$TMPDIR_TEST/pi_args"
run_wrapper -p "test prompt" >/dev/null 2>&1
recorded=$(cat "$TMPDIR_TEST/pi_args")
if echo "$recorded" | grep -q -- "--thinking"; then
    fail "--thinking present when no effort configured" "args: $recorded"
else
    pass "--thinking omitted when no effort configured"
fi

# ---------------------------------------------------------------------------
# test: prompt via -p flag produces output
# ---------------------------------------------------------------------------
echo "test: prompt via -p flag"

output=$(run_wrapper -p "test prompt" 2>/dev/null)
if echo "$output" | grep -q '"content_block_delta"'; then
    pass "-p prompt produces output"
else
    fail "-p prompt produced no output" "got: $output"
fi

# ---------------------------------------------------------------------------
# test: prompt via stdin (primary path used by ralphex)
# ---------------------------------------------------------------------------
echo "test: prompt via stdin"

output=$(echo "prompt from stdin" | MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.txt" \
    PATH="$TMPDIR_TEST:$PATH" TMPDIR_TEST="$TMPDIR_TEST" \
    bash "$WRAPPER" --dangerously-skip-permissions --output-format stream-json 2>/dev/null)

if echo "$output" | grep -q '"content_block_delta"'; then
    pass "stdin prompt produces output"
else
    fail "stdin prompt produced no output" "got: $output"
fi

if [[ "$(cat "$TMPDIR_TEST/pi_prompt")" == "prompt from stdin" ]]; then
    pass "stdin prompt passed as positional arg to pi"
else
    fail "stdin prompt not passed to pi" "got: $(cat "$TMPDIR_TEST/pi_prompt")"
fi

# ---------------------------------------------------------------------------
# test: missing prompt exits with error
# ---------------------------------------------------------------------------
echo "test: missing prompt error"

set +e
PATH="$TMPDIR_TEST:$PATH" bash "$WRAPPER" </dev/null 2>"$TMPDIR_TEST/no_prompt_err"
no_prompt_exit=$?
set -e

if [[ $no_prompt_exit -ne 0 ]]; then
    pass "exits non-zero without prompt"
else
    fail "should exit non-zero without prompt" "got exit code 0"
fi

if grep -q "no prompt provided" "$TMPDIR_TEST/no_prompt_err"; then
    pass "error message mentions missing prompt"
else
    fail "no error about missing prompt" "stderr: $(cat "$TMPDIR_TEST/no_prompt_err")"
fi

# ---------------------------------------------------------------------------
# test: unknown flags ignored gracefully
# ---------------------------------------------------------------------------
echo "test: unknown flags ignored"

output=$(run_wrapper --dangerously-skip-permissions --output-format stream-json --verbose -p "test prompt" 2>/dev/null)
if echo "$output" | grep -q '"content_block_delta"'; then
    pass "unknown flags ignored, output produced normally"
else
    fail "wrapper failed with unknown flags" "got: $output"
fi

# ---------------------------------------------------------------------------
# test: pi not found exits with error
# ---------------------------------------------------------------------------
echo "test: pi not found"

set +e
no_pi_bin="$TMPDIR_TEST/no_pi_bin"
mkdir -p "$no_pi_bin"
for tool in jq bash mktemp mkfifo cat rm kill env; do
    tool_path=$(command -v "$tool" 2>/dev/null) && ln -sf "$tool_path" "$no_pi_bin/$tool"
done
PATH="$no_pi_bin" bash "$WRAPPER" -p "test prompt" 2>"$TMPDIR_TEST/no_pi_err"
no_pi_exit=$?
rm -r "$no_pi_bin"
set -e

if [[ $no_pi_exit -ne 0 ]]; then
    pass "exits non-zero when pi not found"
else
    fail "should exit non-zero when pi not found" "got exit code 0"
fi

if grep -q "pi is required" "$TMPDIR_TEST/no_pi_err"; then
    pass "error message mentions pi requirement"
else
    fail "no error about missing pi" "stderr: $(cat "$TMPDIR_TEST/no_pi_err")"
fi

# ---------------------------------------------------------------------------
# test: message_update text_delta translated to content_block_delta
# ---------------------------------------------------------------------------
echo "test: text_delta translation"

cat > "$TMPDIR_TEST/text_events.jsonl" << 'EOF'
{"type":"session","sessionId":"abc"}
{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"hello world"}}
{"type":"turn_end"}
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/text_events.jsonl" \
    PATH="$TMPDIR_TEST:$PATH" TMPDIR_TEST="$TMPDIR_TEST" \
    bash "$WRAPPER" -p "test prompt" 2>/dev/null)

text_line=$(echo "$output" | grep '"content_block_delta"' | head -1)
if echo "$text_line" | jq -e '.delta.text == "hello world\n"' >/dev/null 2>&1; then
    pass "text_delta translated to content_block_delta with trailing newline"
else
    fail "text_delta not translated correctly" "got: $output"
fi

# session header is skipped (not emitted as a delta)
if echo "$output" | grep -q "abc"; then
    fail "session header leaked into output" "got: $output"
else
    pass "session header skipped"
fi

# ---------------------------------------------------------------------------
# test: turn_end / agent_end translated to result
# ---------------------------------------------------------------------------
echo "test: terminal result event"

last_line=$(echo "$output" | grep '"result"' | tail -1)
if echo "$last_line" | jq -e '.type == "result"' >/dev/null 2>&1; then
    pass "turn_end produces a result event"
else
    fail "no result event produced" "got: $output"
fi

cat > "$TMPDIR_TEST/agentend_events.jsonl" << 'EOF'
{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"done"}}
{"type":"agent_end"}
EOF
output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/agentend_events.jsonl" \
    PATH="$TMPDIR_TEST:$PATH" TMPDIR_TEST="$TMPDIR_TEST" \
    bash "$WRAPPER" -p "test prompt" 2>/dev/null)
if echo "$output" | jq -e 'select(.type == "result")' >/dev/null 2>&1; then
    pass "agent_end produces a result event"
else
    fail "agent_end did not produce result" "got: $output"
fi

# ---------------------------------------------------------------------------
# test: tool execution events skipped by default (PI_VERBOSE=0)
# ---------------------------------------------------------------------------
echo "test: tool events skipped by default"

cat > "$TMPDIR_TEST/tool_events.jsonl" << 'EOF'
{"type":"tool_execution_start","toolName":"bash"}
{"type":"tool_execution_update","toolName":"bash"}
{"type":"tool_execution_end","toolName":"bash"}
{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"agent text"}}
{"type":"turn_end"}
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/tool_events.jsonl" \
    PI_VERBOSE=0 \
    PATH="$TMPDIR_TEST:$PATH" TMPDIR_TEST="$TMPDIR_TEST" \
    bash "$WRAPPER" -p "test prompt" 2>/dev/null)

if echo "$output" | grep -q "agent text"; then
    pass "assistant text emitted with tool events present"
else
    fail "assistant text missing" "got: $output"
fi

if echo "$output" | grep -q "\[tool\]"; then
    fail "tool events leaked (PI_VERBOSE=0)" "got: $output"
else
    pass "tool events skipped (PI_VERBOSE=0)"
fi

# ---------------------------------------------------------------------------
# test: tool execution events included when PI_VERBOSE=1
# ---------------------------------------------------------------------------
echo "test: tool events included (PI_VERBOSE=1)"

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/tool_events.jsonl" \
    PI_VERBOSE=1 \
    PATH="$TMPDIR_TEST:$PATH" TMPDIR_TEST="$TMPDIR_TEST" \
    bash "$WRAPPER" -p "test prompt" 2>/dev/null)

if echo "$output" | grep -q "tool_execution_start" && echo "$output" | grep -q "bash"; then
    pass "tool events included when PI_VERBOSE=1"
else
    fail "tool events not included (PI_VERBOSE=1)" "got: $output"
fi

# ---------------------------------------------------------------------------
# test: invalid JSON lines do not abort translation
# ---------------------------------------------------------------------------
echo "test: invalid JSON tolerated"

cat > "$TMPDIR_TEST/garbage_events.jsonl" << 'EOF'
not json at all
{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"after garbage"}}
{"type":"turn_end"}
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/garbage_events.jsonl" \
    PATH="$TMPDIR_TEST:$PATH" TMPDIR_TEST="$TMPDIR_TEST" \
    bash "$WRAPPER" -p "test prompt" 2>/dev/null)

if echo "$output" | grep -q "after garbage"; then
    pass "translation continues past invalid JSON lines"
else
    fail "invalid JSON aborted translation" "got: $output"
fi

# ---------------------------------------------------------------------------
# test: always emits a terminal result even without turn_end/agent_end
# ---------------------------------------------------------------------------
echo "test: fallback result event"

cat > "$TMPDIR_TEST/noturn_events.jsonl" << 'EOF'
{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"partial"}}
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/noturn_events.jsonl" \
    PATH="$TMPDIR_TEST:$PATH" TMPDIR_TEST="$TMPDIR_TEST" \
    bash "$WRAPPER" -p "test prompt" 2>/dev/null)

last_line=$(echo "$output" | tail -1)
if echo "$last_line" | jq -e '.type == "result"' >/dev/null 2>&1; then
    pass "fallback result emitted when no turn_end/agent_end"
else
    fail "no fallback result event" "got: $output"
fi

# ---------------------------------------------------------------------------
# test: review prompt detection prepends pi adapter text
# ---------------------------------------------------------------------------
echo "test: review-prompt adapter injection"

rm -f "$TMPDIR_TEST/pi_prompt"
MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.txt" \
    PATH="$TMPDIR_TEST:$PATH" TMPDIR_TEST="$TMPDIR_TEST" \
    bash "$WRAPPER" -p "please review <<<RALPHEX:REVIEW_DONE>>>" >/dev/null 2>&1

sent_prompt=$(cat "$TMPDIR_TEST/pi_prompt")
if echo "$sent_prompt" | grep -q "Ralphex review adapter for pi"; then
    pass "review adapter text prepended for review prompts"
else
    fail "review adapter text not prepended" "got: $sent_prompt"
fi

# original review signal preserved in the prompt passed to pi
if echo "$sent_prompt" | grep -q "<<<RALPHEX:REVIEW_DONE>>>"; then
    pass "REVIEW_DONE signal preserved in adapted prompt"
else
    fail "REVIEW_DONE signal lost" "got: $sent_prompt"
fi

# non-review prompts are NOT adapted
rm -f "$TMPDIR_TEST/pi_prompt"
run_wrapper -p "just a task prompt" >/dev/null 2>&1
sent_prompt=$(cat "$TMPDIR_TEST/pi_prompt")
if echo "$sent_prompt" | grep -q "Ralphex review adapter"; then
    fail "adapter wrongly injected for non-review prompt" "got: $sent_prompt"
else
    pass "non-review prompt left unmodified"
fi

# ---------------------------------------------------------------------------
# test: signal passthrough in assistant text
# ---------------------------------------------------------------------------
echo "test: signal passthrough"

cat > "$TMPDIR_TEST/signal_events.jsonl" << 'EOF'
{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"<<<RALPHEX:ALL_TASKS_DONE>>>"}}
{"type":"turn_end"}
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/signal_events.jsonl" \
    PATH="$TMPDIR_TEST:$PATH" TMPDIR_TEST="$TMPDIR_TEST" \
    bash "$WRAPPER" -p "test prompt" 2>/dev/null)

if echo "$output" | grep -q "<<<RALPHEX:ALL_TASKS_DONE>>>"; then
    pass "ralphex signal preserved in translated output"
else
    fail "ralphex signal lost in translation" "got: $output"
fi

# ---------------------------------------------------------------------------
# test: stderr emitted as content_block_delta after stdout
# ---------------------------------------------------------------------------
echo "test: stderr emission"

cat > "$TMPDIR_TEST/stderr_text.txt" << 'EOF'
You've hit your limit
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.txt" \
    MOCK_STDERR_FILE="$TMPDIR_TEST/stderr_text.txt" \
    PATH="$TMPDIR_TEST:$PATH" TMPDIR_TEST="$TMPDIR_TEST" \
    bash "$WRAPPER" -p "test prompt" 2>/dev/null)

stderr_delta=$(echo "$output" | grep "hit your limit")
if echo "$stderr_delta" | jq -e '.type == "content_block_delta"' >/dev/null 2>&1; then
    pass "stderr emitted as content_block_delta for pattern detection"
else
    fail "stderr not emitted as content_block_delta" "got: $output"
fi

# ---------------------------------------------------------------------------
# test: exit code preservation (success and failure)
# ---------------------------------------------------------------------------
echo "test: exit code preservation"

set +e
MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.txt" \
    PATH="$TMPDIR_TEST:$PATH" TMPDIR_TEST="$TMPDIR_TEST" \
    bash "$WRAPPER" -p "test prompt" >/dev/null 2>&1
ok_exit=$?
set -e
if [[ $ok_exit -eq 0 ]]; then
    pass "exit code 0 preserved on success"
else
    fail "expected exit 0 on success" "got: $ok_exit"
fi

set +e
MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.txt" \
    MOCK_EXIT_CODE=7 \
    PATH="$TMPDIR_TEST:$PATH" TMPDIR_TEST="$TMPDIR_TEST" \
    bash "$WRAPPER" -p "test prompt" >/dev/null 2>&1
fail_exit=$?
set -e
if [[ $fail_exit -eq 7 ]]; then
    pass "non-zero pi exit code preserved"
else
    fail "pi exit code not preserved" "got: $fail_exit"
fi

# ---------------------------------------------------------------------------
# summary
# ---------------------------------------------------------------------------
echo ""
echo "results: $passed passed, $failed failed, $total total"

if [[ $failed -gt 0 ]]; then
    exit 1
fi
