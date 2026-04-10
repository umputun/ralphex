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

plan_prompt='plan prompt <<<RALPHEX:QUESTION>>> <<<RALPHEX:PLAN_DRAFT>>> <<<RALPHEX:PLAN_READY>>>'

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
# test: non-JSON line passthrough
# ---------------------------------------------------------------------------
echo "test: non-JSON line passthrough"

cat > "$TMPDIR_TEST/non_json_events.jsonl" <<'EOF'
raw progress line from copilot
{"type":"session.task_complete","data":{"summary":"done","success":true}}
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/non_json_events.jsonl" \
    run_wrapper bash "$WRAPPER" -p "test prompt" 2>/dev/null)

raw_text=$(echo "$output" | jq -r 'select(.type=="content_block_delta") | .delta.text')
if echo "$raw_text" | grep -q 'raw progress line from copilot'; then
    pass "non-JSON lines are emitted as text deltas"
else
    fail "non-JSON line was not emitted" "got: $output"
fi

# ---------------------------------------------------------------------------
# test: prompt via stdin and ignored flags
# ---------------------------------------------------------------------------
echo "test: stdin prompt handling and ignored flags"

reset_captures
cat > "$TMPDIR_TEST/minimal_events.jsonl" <<'EOF'
{"type":"assistant.message_delta","data":{"messageId":"m4","deltaContent":"partial "}}
{"type":"assistant.message","data":{"messageId":"m4","content":"done"}} 
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
# test: non-plan mode emits completed assistant messages only
# ---------------------------------------------------------------------------
echo "test: non-plan mode suppresses delta fragments"

cat > "$TMPDIR_TEST/completed_message_events.jsonl" <<'EOF'
{"type":"assistant.message_delta","data":{"messageId":"m-complete","deltaContent":"TASK "}}
{"type":"assistant.message_delta","data":{"messageId":"m-complete","deltaContent":"OVERVIEW "}}
{"type":"assistant.message","data":{"messageId":"m-complete","content":"TASK OVERVIEW:\nCreate hello_world.py and tests."}}
{"type":"session.task_complete","data":{"summary":"done","success":true}}
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/completed_message_events.jsonl" \
    run_wrapper bash "$WRAPPER" -p "regular task prompt" 2>/dev/null)

completed_event_count=$(echo "$output" | jq -r 'select(.type=="content_block_delta") | 1' | wc -l | tr -d ' ')
if [[ "$completed_event_count" == "1" ]]; then
    pass "non-plan mode emits one completed assistant message"
else
    fail "expected one completed assistant message" "got: $completed_event_count"
fi

completed_text=$(echo "$output" | jq -r 'select(.type=="content_block_delta") | .delta.text')
if echo "$completed_text" | grep -q '^TASK OVERVIEW:'; then
    pass "non-plan mode preserves completed assistant message text"
else
    fail "completed assistant message text missing" "text: $completed_text"
fi

if [[ "$completed_text" == *"TASK OVERVIEW: Create hello_world.py and tests."* || "$completed_text" == *$'TASK OVERVIEW:\nCreate hello_world.py and tests.'* ]]; then
    pass "non-plan mode emits consolidated text instead of token fragments"
else
    fail "non-plan mode output still looks fragmented" "text: $completed_text"
fi

# ---------------------------------------------------------------------------
# test: non-plan mode stops at first assistant turn
# ---------------------------------------------------------------------------
echo "test: non-plan mode stops at first assistant turn"

cat > "$TMPDIR_TEST/first_turn_only_events.jsonl" <<'EOF'
{"type":"assistant.message","data":{"messageId":"turn-1","content":"Task 1 complete. Stopping here."}}
{"type":"assistant.turn_end","data":{"turnId":"t1"}}
{"type":"session.info","data":{"message":"Continuing autonomously (1 premium request)"}}
{"type":"assistant.message","data":{"messageId":"turn-2","content":"Continuing with Task 2."}}
{"type":"assistant.turn_end","data":{"turnId":"t2"}}
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/first_turn_only_events.jsonl" \
    run_wrapper bash "$WRAPPER" -p "regular task prompt" 2>/dev/null)

turn_messages=$(echo "$output" | jq -r 'select(.type=="content_block_delta") | .delta.text')
if echo "$turn_messages" | grep -q 'Task 1 complete. Stopping here.'; then
    pass "first assistant turn is preserved"
else
    fail "first assistant turn missing" "output: $turn_messages"
fi

if echo "$turn_messages" | grep -q 'Continuing autonomously\|Continuing with Task 2'; then
    fail "wrapper should stop after first assistant turn" "output: $turn_messages"
else
    pass "wrapper truncates output after first assistant turn"
fi

set +e
MOCK_STDOUT_FILE="$TMPDIR_TEST/first_turn_only_events.jsonl" \
    MOCK_SLEEP_SECONDS=30 \
    run_wrapper bash "$WRAPPER" -p "regular task prompt" >/dev/null 2>&1
turn_exit_code=$?
set -e

if [[ $turn_exit_code -eq 0 ]]; then
    pass "wrapper exits zero after intentional first-turn stop"
else
    fail "wrapper should exit zero after intentional first-turn stop" "got: $turn_exit_code"
fi

# ---------------------------------------------------------------------------
# test: non-plan mode ignores tool-request turns before visible stopping turn
# ---------------------------------------------------------------------------
echo "test: non-plan mode ignores tool-request turns before visible stopping turn"

cat > "$TMPDIR_TEST/ignore_tool_request_turns_events.jsonl" <<'EOF'
{"type":"assistant.message","data":{"messageId":"visible-1","content":"Let me first examine the existing code structure:","toolRequests":[{"id":"tool-1","toolName":"read_file"}]}}
{"type":"assistant.turn_end","data":{"turnId":"prelude"}}
{"type":"assistant.message","data":{"messageId":"visible-2","content":"Task 1 overview and implementation."}}
{"type":"assistant.turn_end","data":{"turnId":"visible"}}
{"type":"session.info","data":{"message":"Continuing autonomously (1 premium request)"}}
{"type":"assistant.message","data":{"messageId":"visible-3","content":"Continuing with Task 2."}}
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/ignore_tool_request_turns_events.jsonl" \
    run_wrapper bash "$WRAPPER" -p "regular task prompt" 2>/dev/null)

visible_text=$(echo "$output" | jq -r 'select(.type=="content_block_delta") | .delta.text')
if echo "$visible_text" | grep -q 'Let me first examine the existing code structure:' && \
    echo "$visible_text" | grep -q 'Task 1 overview and implementation.'; then
    pass "wrapper preserves tool-request turn output and subsequent stopping turn output"
else
    fail "expected both tool-request and stopping turn output" "output: $visible_text"
fi

if echo "$visible_text" | grep -q 'Continuing autonomously\|Continuing with Task 2'; then
    fail "wrapper should stop after first visible turn without tool requests" "output: $visible_text"
else
    pass "wrapper truncates after first visible turn without tool requests"
fi

# ---------------------------------------------------------------------------
# test: plan mode stops at QUESTION boundary and suppresses deltas
# ---------------------------------------------------------------------------
echo "test: plan mode QUESTION boundary handling"

cat > "$TMPDIR_TEST/plan_question_events.jsonl" <<'EOF'
{"type":"assistant.message_delta","data":{"messageId":"plan-q","deltaContent":"chunk-one "}}
{"type":"assistant.message_delta","data":{"messageId":"plan-q","deltaContent":"chunk-two "}}
{"type":"assistant.message","data":{"messageId":"plan-q","content":"Exploration notes.\n<<<RALPHEX:QUESTION>>>\n{\"question\":\"Which mode?\",\"options\":[\"TCP\",\"UDP\"]}\n<<<RALPHEX:END>>>\nContinuing autonomously\n<<<RALPHEX:PLAN_DRAFT>>>\n# Wrong Draft\n<<<RALPHEX:END>>>"}}
{"type":"session.info","data":{"message":"post-boundary noise"}}
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/plan_question_events.jsonl" \
    run_wrapper bash "$WRAPPER" -p "$plan_prompt" 2>/dev/null)

question_event_count=$(echo "$output" | jq -r 'select(.type=="content_block_delta") | 1' | wc -l | tr -d ' ')
if [[ "$question_event_count" == "1" ]]; then
    pass "plan mode emits one completed assistant message for question boundary"
else
    fail "expected one content_block_delta event in plan mode" "got: $question_event_count"
fi

question_text=$(echo "$output" | jq -r 'select(.type=="content_block_delta") | .delta.text')
if echo "$question_text" | grep -q 'chunk-one\|chunk-two'; then
    fail "plan mode should suppress assistant.message_delta chunks" "text: $question_text"
else
    pass "plan mode suppresses assistant.message_delta chunks"
fi

if echo "$question_text" | grep -q '<<<RALPHEX:QUESTION>>>'; then
    pass "plan mode preserves QUESTION block"
else
    fail "QUESTION block not found in plan mode output" "text: $question_text"
fi

if echo "$question_text" | grep -q 'Continuing autonomously\|Wrong Draft\|post-boundary noise'; then
    fail "plan mode should truncate output at QUESTION boundary" "text: $question_text"
else
    pass "plan mode truncates output at QUESTION boundary"
fi

# ---------------------------------------------------------------------------
# test: plan mode stops at PLAN_DRAFT boundary
# ---------------------------------------------------------------------------
echo "test: plan mode PLAN_DRAFT boundary handling"

cat > "$TMPDIR_TEST/plan_draft_events.jsonl" <<'EOF'
{"type":"assistant.message_delta","data":{"messageId":"plan-d","deltaContent":"draft-chunk "}}
{"type":"assistant.message","data":{"messageId":"plan-d","content":"Draft incoming.\n<<<RALPHEX:PLAN_DRAFT>>>\n# Draft Title\n\n## Overview\nPlan content.\n<<<RALPHEX:END>>>\nextra trailing text"}}
{"type":"session.info","data":{"message":"late noise"}}
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/plan_draft_events.jsonl" \
    run_wrapper bash "$WRAPPER" -p "$plan_prompt" 2>/dev/null)

draft_text=$(echo "$output" | jq -r 'select(.type=="content_block_delta") | .delta.text')
if echo "$draft_text" | grep -q '<<<RALPHEX:PLAN_DRAFT>>>'; then
    pass "plan mode preserves PLAN_DRAFT block"
else
    fail "PLAN_DRAFT block not found in plan mode output" "text: $draft_text"
fi

if echo "$draft_text" | grep -q 'draft-chunk\|extra trailing text\|late noise'; then
    fail "plan mode should truncate output at PLAN_DRAFT boundary" "text: $draft_text"
else
    pass "plan mode truncates output at PLAN_DRAFT boundary"
fi

# ---------------------------------------------------------------------------
# test: plan mode boundary stop exits successfully
# ---------------------------------------------------------------------------
echo "test: plan mode boundary stop exit code"

set +e
MOCK_STDOUT_FILE="$TMPDIR_TEST/plan_question_events.jsonl" \
    MOCK_SLEEP_SECONDS=30 \
    run_wrapper bash "$WRAPPER" -p "$plan_prompt" >/dev/null 2>&1
plan_exit_code=$?
set -e

if [[ $plan_exit_code -eq 0 ]]; then
    pass "plan mode exits zero after intentional boundary stop"
else
    fail "plan mode should exit zero after intentional boundary stop" "got: $plan_exit_code"
fi

# ---------------------------------------------------------------------------
# test: plan mode stops at PLAN_READY
# ---------------------------------------------------------------------------
echo "test: plan mode PLAN_READY boundary handling"

cat > "$TMPDIR_TEST/plan_ready_events.jsonl" <<'EOF'
{"type":"assistant.message","data":{"messageId":"plan-r","content":"Writing the accepted plan.\n<<<RALPHEX:PLAN_READY>>>\nContinuing autonomously\nImplemented app.py"}}
{"type":"session.info","data":{"message":"late session noise"}}
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/plan_ready_events.jsonl" \
    run_wrapper bash "$WRAPPER" -p "$plan_prompt" 2>/dev/null)

ready_text=$(echo "$output" | jq -r 'select(.type=="content_block_delta") | .delta.text')
if echo "$ready_text" | grep -q '<<<RALPHEX:PLAN_READY>>>'; then
    pass "plan mode preserves PLAN_READY signal"
else
    fail "PLAN_READY signal not found in plan mode output" "text: $ready_text"
fi

if echo "$ready_text" | grep -q 'Continuing autonomously\|Implemented app.py\|late session noise'; then
    fail "plan mode should truncate output at PLAN_READY" "text: $ready_text"
else
    pass "plan mode truncates output at PLAN_READY"
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
# test: leading tab normalization
# ---------------------------------------------------------------------------
echo "test: leading tab normalization"

cat > "$TMPDIR_TEST/tab_indented_events.jsonl" <<'EOF'
{"type":"assistant.message","data":{"content":"\tAll tests pass. Committing.\n\t\tNested tab line.\nClean line."}}
{"type":"assistant.turn_end"}
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/tab_indented_events.jsonl" \
    run_wrapper bash "$WRAPPER" -p "test prompt" 2>/dev/null)

tab_char=$'\t'
escaped_tab='\t'
combined_text=$(echo "$output" | jq -r 'select(.type == "content_block_delta") | .delta.text' 2>/dev/null | tr -d '\n')
if [[ "$combined_text" == *"$tab_char"* || "$combined_text" == *"$escaped_tab"* ]]; then
    fail "leading tabs not stripped from output" "text: $combined_text"
elif echo "$combined_text" | grep -q 'All tests pass'; then
    pass "leading tabs stripped from assistant.message content"
else
    fail "expected tab-normalized text not found" "got: $combined_text"
fi

# verify clean line is preserved
if echo "$combined_text" | grep -q 'Clean line'; then
    pass "non-indented lines preserved after tab stripping"
else
    fail "non-indented line missing after tab stripping" "got: $combined_text"
fi
# ---------------------------------------------------------------------------
echo "test: trailing tab normalization"

cat > "$TMPDIR_TEST/trailing_tab_events.jsonl" <<'EOF'
{"type":"assistant.message","data":{"content":"Line with trailing tab\t\nAnother line\t\t\nClean line."}}
{"type":"assistant.turn_end"}
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/trailing_tab_events.jsonl" \
    run_wrapper bash "$WRAPPER" -p "test prompt" 2>/dev/null)

raw_text=$(echo "$output" | jq -r 'select(.type == "content_block_delta") | .delta.text' 2>/dev/null)
has_trailing_tab=0
while IFS= read -r text_line || [[ -n "$text_line" ]]; do
    if [[ "$text_line" == *"$tab_char" ]]; then
        has_trailing_tab=1
        break
    fi
done <<< "$raw_text"
if [[ "$has_trailing_tab" -eq 1 ]]; then
    fail "trailing tabs not stripped from output" "raw text: $raw_text"
elif echo "$raw_text" | grep -q 'Line with trailing tab'; then
    pass "trailing tabs stripped from assistant.message content"
else
    fail "expected trailing-tab-normalized text not found" "got: $raw_text"
fi

# verify trailing tab does not appear after the line content
first_raw_line=${raw_text%%$'\n'*}
if [[ "$first_raw_line" == *"$tab_char" ]]; then
    fail "trailing tab found after 'Line with trailing tab'" "raw text: $raw_text"
else
    pass "trailing tab absent from end of lines"
fi
# ---------------------------------------------------------------------------
echo "test: terminal cursor reset before first output and on cleanup"

# verify emit_text_delta clears /dev/tty before first output
if grep -q 'first_output_emitted' "$WRAPPER" && \
   grep -q "(printf.*\\\\r.*\\\\033\[K.*>/dev/tty)" "$WRAPPER"; then
    pass "emit_text_delta clears /dev/tty stray chars before first output"
else
    fail "emit_text_delta missing first-output /dev/tty clear" "expected: first_output_emitted guard with printf \\r\\033[K"
fi

# verify cleanup also resets terminal cursor
if grep -A12 'cleanup()' "$WRAPPER" | grep -q "(printf.*\\\\r.*\\\\033\[K.*>/dev/tty)"; then
    pass "cleanup resets terminal cursor on exit"
else
    fail "cleanup missing terminal cursor reset" "expected: (printf '\\r\\033[K' >/dev/tty) 2>/dev/null in cleanup()"
fi
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
    if echo "$captured_prompt" | grep -q 'FORMATTING RULE (strict)'; then
        pass "formatting rule included in review adapter"
    else
        fail "formatting rule missing from review adapter" "prompt: $captured_prompt"
    fi
    # formatting rule must appear before the Ralphex adapter section
    fmt_pos=$(echo "$captured_prompt" | grep -n 'FORMATTING RULE' | head -1 | cut -d: -f1)
    adapter_pos=$(echo "$captured_prompt" | grep -n 'Ralphex review adapter' | head -1 | cut -d: -f1)
    if [[ -n "$fmt_pos" && -n "$adapter_pos" && "$fmt_pos" -lt "$adapter_pos" ]]; then
        pass "formatting rule precedes adapter instructions"
    else
        fail "formatting rule should come before adapter instructions" "fmt_pos=$fmt_pos adapter_pos=$adapter_pos"
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
# test: plan adapter injection
# ---------------------------------------------------------------------------
echo "test: plan adapter injection"

reset_captures
MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.jsonl" \
    run_wrapper bash "$WRAPPER" -p "$plan_prompt" >/dev/null 2>&1

if [[ -f "$TMPDIR_TEST/copilot_stdin" ]]; then
    captured_prompt=$(cat "$TMPDIR_TEST/copilot_stdin")
    if echo "$captured_prompt" | grep -q 'FORMATTING RULE (strict)'; then
        pass "plan adapter formatting rule prepended to plan prompt"
    else
        fail "plan adapter formatting rule not prepended" "prompt start: ${captured_prompt:0:200}"
    fi
    if echo "$captured_prompt" | grep -q 'plain text only'; then
        pass "plan adapter instructs plain text for analysis"
    else
        fail "plan adapter plain text instruction missing" "prompt: ${captured_prompt:0:200}"
    fi
    if echo "$captured_prompt" | grep -q 'PLAN REVIEW RULE'; then
        pass "plan adapter requires PLAN_DRAFT before PLAN_READY"
    else
        fail "plan adapter missing PLAN_DRAFT-before-PLAN_READY rule" "prompt: ${captured_prompt:0:400}"
    fi
    if echo "$captured_prompt" | grep -q 'existing plan file'; then
        pass "plan adapter covers existing plan file case"
    else
        fail "plan adapter does not cover existing plan file case" "prompt: ${captured_prompt:0:400}"
    fi
    if echo "$captured_prompt" | grep -q 'Ralphex review adapter for GitHub Copilot CLI'; then
        fail "review adapter should not be added to plan prompts"
    else
        pass "review adapter omitted for plan prompts"
    fi
else
    fail "could not capture prompt sent to copilot"
fi

reset_captures
MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.jsonl" \
    run_wrapper bash "$WRAPPER" -p "regular task prompt" >/dev/null 2>&1

if [[ -f "$TMPDIR_TEST/copilot_stdin" ]]; then
    captured_prompt=$(cat "$TMPDIR_TEST/copilot_stdin")
    # plan adapter formatting rule text is distinct enough — check for the plan-specific phrasing
    if echo "$captured_prompt" | grep -q 'before emitting.*PLAN_DRAFT'; then
        fail "plan adapter should not be added to non-plan prompts"
    else
        pass "plan adapter omitted for non-plan prompts"
    fi
fi

# ---------------------------------------------------------------------------
# test: PLAN_READY without PLAN_DRAFT touches existing plan file
# ---------------------------------------------------------------------------
echo "test: PLAN_READY without PLAN_DRAFT touches existing plan file"

# create a plan file with an old mtime
mkdir -p "$TMPDIR_TEST/docs/plans"
plan_with_space="$TMPDIR_TEST/docs/plans/test plan.md"
older_plan="$TMPDIR_TEST/docs/plans/older-plan.md"
mtime_reference="$TMPDIR_TEST/mtime_reference"
echo "# Test Plan" > "$plan_with_space"
echo "# Older Plan" > "$older_plan"
touch -t 200001010000 "$plan_with_space"
touch -t 199901010000 "$older_plan"
touch -t 200101010000 "$mtime_reference"

# copilot responds with PLAN_READY referencing the plan file (no PLAN_DRAFT)
cat > "$TMPDIR_TEST/plan_ready_only_events.jsonl" <<'EOF'
{"type":"assistant.message","data":{"content":"A plan file already exists at [test plan](docs/plans/test plan.md).\n<<<RALPHEX:PLAN_READY>>>"}}
EOF

(cd "$TMPDIR_TEST" && MOCK_STDOUT_FILE="$TMPDIR_TEST/plan_ready_only_events.jsonl" \
    run_wrapper bash "$WRAPPER" -p "$plan_prompt" >/dev/null 2>&1)

if [[ "$plan_with_space" -nt "$mtime_reference" ]]; then
    pass "PLAN_READY without PLAN_DRAFT touches existing plan file"
else
    fail "plan file mtime not updated" "plan file is not newer than reference marker"
fi

# test: fallback to newest plan file when path not in message
touch -t 200001010000 "$plan_with_space"
touch -t 199901010000 "$older_plan"
touch -t 200101010000 "$mtime_reference"

cat > "$TMPDIR_TEST/plan_ready_nopath_events.jsonl" <<'EOF'
{"type":"assistant.message","data":{"content":"A plan file already exists for this feature.\n<<<RALPHEX:PLAN_READY>>>"}}
EOF

(cd "$TMPDIR_TEST" && MOCK_STDOUT_FILE="$TMPDIR_TEST/plan_ready_nopath_events.jsonl" \
    run_wrapper bash "$WRAPPER" -p "$plan_prompt" >/dev/null 2>&1)

if [[ "$plan_with_space" -nt "$mtime_reference" ]]; then
    pass "fallback to newest plan file when path not in message"
else
    fail "plan file mtime not updated in fallback case" "plan file is not newer than reference marker"
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
