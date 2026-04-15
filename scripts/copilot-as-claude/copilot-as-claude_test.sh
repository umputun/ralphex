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
echo "test: non-plan flag construction"

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
        pass "non-plan wrapper passes required Copilot JSON, autopilot, and autonomy flags"
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
# test: COPILOT_MODEL forwarding
# ---------------------------------------------------------------------------
echo "test: COPILOT_MODEL forwarding"

reset_captures
COPILOT_MODEL="gpt-5.4" \
    MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.jsonl" \
    run_wrapper bash "$WRAPPER" -p "model prompt" >/dev/null 2>&1

if [[ -f "$TMPDIR_TEST/copilot_args" ]]; then
    recorded_args=$(tr '\n' ' ' < "$TMPDIR_TEST/copilot_args")
    if echo "$recorded_args" | grep -q -- '--model gpt-5.4'; then
        pass "COPILOT_MODEL forwards to --model"
    else
        fail "COPILOT_MODEL should forward to --model" "args: $recorded_args"
    fi
else
    fail "could not capture copilot arguments for COPILOT_MODEL"
fi

# ---------------------------------------------------------------------------
# test: plan mode flag construction
# ---------------------------------------------------------------------------
echo "test: plan mode flag construction"

reset_captures
MOCK_STDOUT_FILE="$TMPDIR_TEST/minimal_events.jsonl" \
    run_wrapper bash "$WRAPPER" -p "$plan_prompt" >/dev/null 2>&1

if [[ -f "$TMPDIR_TEST/copilot_args" ]]; then
    recorded_args=$(tr '\n' ' ' < "$TMPDIR_TEST/copilot_args")
    if echo "$recorded_args" | grep -q -- '-s ' && \
        echo "$recorded_args" | grep -q -- '--output-format json' && \
        echo "$recorded_args" | grep -q -- '--stream on' && \
        echo "$recorded_args" | grep -q -- '--autopilot' && \
        echo "$recorded_args" | grep -q -- '--allow-all'; then
        pass "plan mode wrapper passes JSON, autopilot, and allow-all flags"
    else
        fail "required plan mode flags missing" "args: $recorded_args"
    fi

    if echo "$recorded_args" | grep -q -- '--no-ask-user' || \
        echo "$recorded_args" | grep -q -- '--mode plan'; then
        fail "plan mode should not use question suppression or native plan mode" "args: $recorded_args"
    else
        pass "plan mode omits no-ask-user and native plan mode"
    fi
else
    fail "could not capture copilot arguments for plan mode"
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

completed_event_count=$(echo "$output" | jq -r 'select(.type=="content_block_delta" and .delta.text != "") | 1' | wc -l | tr -d ' ')
if [[ "$completed_event_count" == "1" ]]; then
    pass "non-plan mode emits one completed assistant message"
else
    fail "expected one completed assistant message" "got: $completed_event_count"
fi

keepalive_event_count=$(echo "$output" | jq -r 'select(.type=="content_block_delta" and .delta.text == "") | 1' | wc -l | tr -d ' ')
if [[ "$keepalive_event_count" -ge 1 ]]; then
    pass "non-plan mode emits keepalive events for delta-only activity"
else
    fail "expected keepalive event for assistant.message_delta activity" "output: $output"
fi

completed_text=$(echo "$output" | jq -r 'select(.type=="content_block_delta" and .delta.text != "") | .delta.text')
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
# test: non-plan mode preserves later autonomous completion turns
# ---------------------------------------------------------------------------
echo "test: non-plan mode preserves later autonomous completion turns"

cat > "$TMPDIR_TEST/first_turn_only_events.jsonl" <<'EOF'
{"type":"assistant.message","data":{"messageId":"turn-1","content":"I found the moved plan in docs/plans/completed."}}
{"type":"assistant.turn_end","data":{"turnId":"t1"}}
{"type":"session.info","data":{"message":"Continuing autonomously (1 premium request)"}}
{"type":"assistant.message","data":{"messageId":"turn-2","content":"I checked the remaining boxes and there is nothing left to do.\n<<<RALPHEX:ALL_TASKS_DONE>>>"}} 
{"type":"session.task_complete","data":{"summary":"done","success":true}}
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/first_turn_only_events.jsonl" \
    run_wrapper bash "$WRAPPER" -p "regular task prompt" 2>/dev/null)

turn_messages=$(echo "$output" | jq -r 'select(.type=="content_block_delta") | .delta.text')
if echo "$turn_messages" | grep -q 'I found the moved plan in docs/plans/completed.'; then
    pass "first assistant turn is preserved"
else
    fail "first assistant turn missing" "output: $turn_messages"
fi

if echo "$turn_messages" | grep -q '<<<RALPHEX:ALL_TASKS_DONE>>>'; then
    pass "wrapper preserves later autonomous completion signal"
else
    fail "wrapper should preserve later autonomous completion signal" "output: $turn_messages"
fi

if echo "$turn_messages" | grep -q 'Continuing autonomously (1 premium request)'; then
    pass "wrapper preserves cross-turn session progress text"
else
    fail "wrapper should preserve cross-turn session progress text" "output: $turn_messages"
fi

# ---------------------------------------------------------------------------
# test: non-plan mode preserves tool-request turns until later completion
# ---------------------------------------------------------------------------
echo "test: non-plan mode preserves tool-request turns until later completion"

cat > "$TMPDIR_TEST/ignore_tool_request_turns_events.jsonl" <<'EOF'
{"type":"assistant.message","data":{"messageId":"visible-1","content":"Let me first examine the existing code structure:","toolRequests":[{"id":"tool-1","toolName":"read_file"}]}}
{"type":"assistant.turn_end","data":{"turnId":"prelude"}}
{"type":"assistant.message","data":{"messageId":"visible-2","content":"I found the plan under docs/plans/completed and every task section is already checked off."}}
{"type":"assistant.turn_end","data":{"turnId":"visible"}}
{"type":"session.info","data":{"message":"Continuing autonomously (1 premium request)"}}
{"type":"assistant.message","data":{"messageId":"visible-3","content":"Returning the completion sentinel now.\n<<<RALPHEX:ALL_TASKS_DONE>>>"}} 
{"type":"session.task_complete","data":{"summary":"done","success":true}}
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/ignore_tool_request_turns_events.jsonl" \
    run_wrapper bash "$WRAPPER" -p "regular task prompt" 2>/dev/null)

visible_text=$(echo "$output" | jq -r 'select(.type=="content_block_delta") | .delta.text')
if echo "$visible_text" | grep -q 'Let me first examine the existing code structure:' && \
    echo "$visible_text" | grep -q 'I found the plan under docs/plans/completed and every task section is already checked off.'; then
    pass "wrapper preserves tool-request turn output and later analysis turn output"
else
    fail "expected both tool-request and later analysis turn output" "output: $visible_text"
fi

if echo "$visible_text" | grep -q '<<<RALPHEX:ALL_TASKS_DONE>>>'; then
    pass "wrapper preserves later completion signal after tool-request turn"
else
    fail "wrapper should preserve later completion signal after tool-request turn" "output: $visible_text"
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

question_event_count=$(echo "$output" | jq -r 'select(.type=="content_block_delta" and .delta.text != "") | 1' | wc -l | tr -d ' ')
if [[ "$question_event_count" == "1" ]]; then
    pass "plan mode emits one completed assistant message for question boundary"
else
    fail "expected one content_block_delta event in plan mode" "got: $question_event_count"
fi

question_text=$(echo "$output" | jq -r 'select(.type=="content_block_delta" and .delta.text != "") | .delta.text')
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

draft_text=$(echo "$output" | jq -r 'select(.type=="content_block_delta" and .delta.text != "") | .delta.text')
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
{"type":"assistant.message","data":{"messageId":"plan-r","content":"Writing the accepted plan to docs/plans/test plan.md.\n<<<RALPHEX:PLAN_READY>>>\nContinuing autonomously\nImplemented app.py"}}
{"type":"session.info","data":{"message":"late session noise"}}
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/plan_ready_events.jsonl" \
    run_wrapper bash "$WRAPPER" -p "$plan_prompt" 2>/dev/null)

ready_text=$(echo "$output" | jq -r 'select(.type=="content_block_delta" and .delta.text != "") | .delta.text')
if echo "$ready_text" | grep -q '<<<RALPHEX:PLAN_READY>>>'; then
    pass "plan mode preserves PLAN_READY signal"
else
    fail "PLAN_READY signal not found in plan mode output" "text: $ready_text"
fi

if echo "$ready_text" | grep -q 'docs/plans/test plan.md'; then
    pass "plan mode preserves plan path text before PLAN_READY"
else
    fail "plan mode should preserve plan path text before PLAN_READY" "text: $ready_text"
fi

if echo "$ready_text" | grep -q 'Continuing autonomously\|Implemented app.py\|late session noise'; then
    fail "plan mode should truncate output at PLAN_READY" "text: $ready_text"
else
    pass "plan mode truncates output at PLAN_READY"
fi

# ---------------------------------------------------------------------------
# test: accepted draft fallback emits PLAN_READY for written matching draft
# ---------------------------------------------------------------------------
echo "test: accepted draft fallback emits PLAN_READY for written matching draft"

progress_dir="$TMPDIR_TEST/state with spaces"
mkdir -p "$progress_dir" "$TMPDIR_TEST/custom/plans"
progress_file="$progress_dir/progress-plan-test-feature.txt"
cat > "$progress_file" <<'EOF'
[26-04-15 13:17:40] exploration notes
[26-04-15 13:17:41] <<<RALPHEX:PLAN_DRAFT>>>
[26-04-15 13:17:41] # Generated Plan
[26-04-15 13:17:41] 
[26-04-15 13:17:41] ## Overview
[26-04-15 13:17:41] Plan content.
[26-04-15 13:17:41] 
[26-04-15 13:17:41] ## Implementation Steps
[26-04-15 13:17:41] - Step 1
[26-04-15 13:17:41] <<<RALPHEX:END>>>
[26-04-15 13:17:42] DRAFT REVIEW: accept
[26-04-15 13:17:43] I’m re-checking the plan request: definitely-not-metadata
EOF

generated_plan_path="custom/plans/generated-plan.md"
plan_prompt_with_paths=$(cat <<EOF
You are helping create an implementation plan for: test feature
Progress log: $progress_file (contains previous Q&A from this session)
Emit only the required ralphex signals and stop at boundaries:
<<<RALPHEX:QUESTION>>>
<<<RALPHEX:PLAN_DRAFT>>>
<<<RALPHEX:PLAN_READY>>>
EOF
)

cat > "$TMPDIR_TEST/plan_complete_without_signal_events.jsonl" <<'EOF'
{"type":"assistant.message","data":{"content":"The progress log already shows an accepted draft for this exact plan, so I’m writing it to disk now."}}
{"type":"assistant.message","data":{"content":"The accepted draft is on disk now. I’m just checking the saved file path and content shape before closing with the required signal."}}
{"type":"session.task_complete","data":{"summary":"done","success":true}}
EOF

(
    sleep 1
    cat > "$TMPDIR_TEST/$generated_plan_path" <<'EOF'
# Generated Plan

## Overview
Plan content.

## Implementation Steps
- Step 1
EOF
) &
writer_pid=$!

output=$(cd "$TMPDIR_TEST" && \
    MOCK_STDOUT_FILE="$TMPDIR_TEST/plan_complete_without_signal_events.jsonl" \
    MOCK_SLEEP_SECONDS=2 \
    run_wrapper bash "$WRAPPER" -p "$plan_prompt_with_paths" 2>/dev/null)
wait "$writer_pid"

fallback_text=$(echo "$output" | jq -r 'select(.type=="content_block_delta") | .delta.text')
if echo "$fallback_text" | grep -q "^$generated_plan_path$" && \
    echo "$fallback_text" | grep -q '<<<RALPHEX:PLAN_READY>>>'; then
    pass "wrapper synthesizes PLAN_READY after accepted draft matching-file write"
else
    fail "wrapper should synthesize PLAN_READY after accepted draft matching-file write" "text: $fallback_text"
fi

# ---------------------------------------------------------------------------
# test: accepted draft fallback ignores unrelated new plan files
# ---------------------------------------------------------------------------
echo "test: accepted draft fallback ignores unrelated new plan files"

rm -f "$TMPDIR_TEST"/custom/plans/*.md
cat > "$progress_file" <<'EOF'
[26-04-15 13:17:41] <<<RALPHEX:PLAN_DRAFT>>>
[26-04-15 13:17:41] # Generated Plan
[26-04-15 13:17:41] 
[26-04-15 13:17:41] ## Overview
[26-04-15 13:17:41] Plan content.
[26-04-15 13:17:41] 
[26-04-15 13:17:41] ## Implementation Steps
[26-04-15 13:17:41] - Step 1
[26-04-15 13:17:41] <<<RALPHEX:END>>>
[26-04-15 13:17:42] DRAFT REVIEW: accept
EOF

(
    sleep 1
    cat > "$TMPDIR_TEST/custom/plans/unrelated-plan.md" <<'EOF'
# Other Plan

## Overview
Different content.
EOF
) &
writer_pid=$!

output=$(cd "$TMPDIR_TEST" && \
    MOCK_STDOUT_FILE="$TMPDIR_TEST/plan_complete_without_signal_events.jsonl" \
    MOCK_SLEEP_SECONDS=2 \
    run_wrapper bash "$WRAPPER" -p "$plan_prompt_with_paths" 2>/dev/null)
wait "$writer_pid"

fallback_text=$(echo "$output" | jq -r 'select(.type=="content_block_delta") | .delta.text')
if echo "$fallback_text" | grep -q '<<<RALPHEX:PLAN_READY>>>'; then
    fail "wrapper should ignore unrelated new plan files" "text: $fallback_text"
else
    pass "wrapper ignores unrelated new plan files"
fi

# ---------------------------------------------------------------------------
# test: native ask_user tool requests become QUESTION signals in plan mode
# ---------------------------------------------------------------------------
echo "test: native ask_user tool requests become QUESTION signals in plan mode"

cat > "$TMPDIR_TEST/native_ask_user_events.jsonl" <<'EOF'
{"type":"assistant.message","data":{"messageId":"native-q","content":"I need one decision before I can finish the plan.","toolRequests":[{"id":"ask-1","toolName":"ask_user","args":{"question":"Which deployment mode?","options":["Docker",null,"Systemd"]}}]}}
{"type":"session.info","data":{"message":"late native question noise"}}
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/native_ask_user_events.jsonl" \
    run_wrapper bash "$WRAPPER" -p "$plan_prompt" 2>/dev/null)

native_question_text=$(echo "$output" | jq -r 'select(.type=="content_block_delta") | .delta.text')
if echo "$native_question_text" | grep -Fq '<<<RALPHEX:QUESTION>>>' && \
    echo "$native_question_text" | grep -Fq '"question":"Which deployment mode?"' && \
    echo "$native_question_text" | grep -Fq '"Docker"' && \
    echo "$native_question_text" | grep -Fq '"Systemd"' && \
    ! echo "$native_question_text" | grep -Fq '"null"'; then
    pass "wrapper translates native ask_user tool requests into QUESTION signals"
else
    fail "wrapper should translate native ask_user tool requests into QUESTION signals" "text: $native_question_text"
fi

if echo "$native_question_text" | grep -q 'late native question noise'; then
    fail "wrapper should stop after translated native question" "text: $native_question_text"
else
    pass "wrapper truncates output after translated native question"
fi

# ---------------------------------------------------------------------------
# test: unsupported native ask_user requests fail clearly
# ---------------------------------------------------------------------------
echo "test: unsupported native ask_user requests fail clearly"

cat > "$TMPDIR_TEST/native_ask_user_missing_options_events.jsonl" <<'EOF'
{"type":"assistant.message","data":{"messageId":"native-q-bad","toolRequests":[{"id":"ask-2","toolName":"ask_user","args":{"question":"Need freeform input"}}]}}
EOF

set +e
output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/native_ask_user_missing_options_events.jsonl" \
    run_wrapper bash "$WRAPPER" -p "$plan_prompt" 2>/dev/null)
native_question_exit=$?
set -e

if [[ $native_question_exit -ne 0 ]]; then
    pass "unsupported native ask_user requests exit non-zero"
else
    fail "unsupported native ask_user requests should exit non-zero" "got: $native_question_exit"
fi

native_question_text=$(echo "$output" | jq -r 'select(.type=="content_block_delta") | .delta.text')
if echo "$native_question_text" | grep -q 'without concrete options'; then
    pass "unsupported native ask_user requests emit a clear error"
else
    fail "unsupported native ask_user requests should emit a clear error" "text: $native_question_text"
fi

# ---------------------------------------------------------------------------
# test: session warning and error events are forwarded
# ---------------------------------------------------------------------------
echo "test: session warning and error forwarding"

cat > "$TMPDIR_TEST/session_status_events.jsonl" <<'EOF'
{"type":"session.warning","data":{"message":"warning: review quota nearly exhausted"}}
{"type":"session.error","data":{"message":"error: transient failure"}}
{"type":"session.info","data":{"message":"info: continuing"}}
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/session_status_events.jsonl" \
    run_wrapper bash "$WRAPPER" -p "test prompt" 2>/dev/null)

status_text=$(echo "$output" | jq -r 'select(.type=="content_block_delta" and .delta.text != "") | .delta.text')
if echo "$status_text" | grep -q 'warning: review quota nearly exhausted' && \
    echo "$status_text" | grep -q 'error: transient failure' && \
    echo "$status_text" | grep -q 'info: continuing'; then
    pass "session warning, error, and info text are forwarded"
else
    fail "session status text should be forwarded" "text: $status_text"
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
    if echo "$captured_prompt" | grep -q 'do NOT emit another PLAN_DRAFT'; then
        pass "plan adapter forbids repeat draft after accept"
    else
        fail "plan adapter should forbid repeat draft after accept" "prompt: ${captured_prompt:0:600}"
    fi
    if echo "$captured_prompt" | grep -q 'QUESTION RULE'; then
        pass "plan adapter adds explicit question rule"
    else
        fail "plan adapter missing question rule" "prompt: ${captured_prompt:0:500}"
    fi
    if echo "$captured_prompt" | grep -q 'ask_user tool'; then
        pass "plan adapter forbids native ask_user tool"
    else
        fail "plan adapter should forbid native ask_user tool" "prompt: ${captured_prompt:0:500}"
    fi
    if echo "$captured_prompt" | grep -q 'implementation-blocking uncertainty'; then
        pass "plan adapter forbids unresolved blockers in drafts"
    else
        fail "plan adapter missing unresolved blocker rule" "prompt: ${captured_prompt:0:500}"
    fi
    if echo "$captured_prompt" | grep -q 'existing plan file'; then
        pass "plan adapter covers existing plan file case"
    else
        fail "plan adapter does not cover existing plan file case" "prompt: ${captured_prompt:0:400}"
    fi
    if echo "$captured_prompt" | grep -q 'do NOT modify it'; then
        pass "plan adapter keeps existing-plan handling read-only"
    else
        fail "plan adapter should keep existing-plan handling read-only" "prompt: ${captured_prompt:0:600}"
    fi
    if echo "$captured_prompt" | grep -q 'exact plan path'; then
        pass "plan adapter requires exact path before direct PLAN_READY"
    else
        fail "plan adapter missing exact path fallback for direct PLAN_READY" "prompt: ${captured_prompt:0:500}"
    fi
    if echo "$captured_prompt" | grep -q 'present them inside a PLAN_DRAFT'; then
        fail "plan adapter should not re-draft existing plans" "prompt: ${captured_prompt:0:700}"
    else
        pass "plan adapter avoids re-drafting existing plans"
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

# ---------------------------------------------------------------------------
# test: task fallback synthesizes ALL_TASKS_DONE when plan is complete
# ---------------------------------------------------------------------------
echo "test: task fallback synthesizes ALL_TASKS_DONE when plan is complete"

reset_captures
plan_file="$TMPDIR_TEST/plan-all-done.md"
cat > "$plan_file" <<'EOF'
# Fix bugs

### Task 1: Fix the widget
- [x] Refactor widget module
- [x] Add unit tests

### Task 2: Update docs
- [x] Update README
EOF

task_prompt="Read the plan file at $plan_file. Find the FIRST Task section.
When all tasks are done emit <<<RALPHEX:ALL_TASKS_DONE>>>
If task fails emit <<<RALPHEX:TASK_FAILED>>>"

cat > "$TMPDIR_TEST/task_no_signal_events.jsonl" <<'EOF'
{"type":"assistant.message","data":{"content":"I checked the plan. All task sections are already complete. Nothing left to do."}}
{"type":"session.task_complete","data":{"summary":"done","success":true}}
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/task_no_signal_events.jsonl" \
    run_wrapper bash "$WRAPPER" -p "$task_prompt" 2>/dev/null)

fallback_text=$(echo "$output" | jq -r 'select(.type=="content_block_delta") | .delta.text')
if echo "$fallback_text" | grep -q '<<<RALPHEX:ALL_TASKS_DONE>>>'; then
    pass "wrapper synthesizes ALL_TASKS_DONE when plan has no remaining tasks"
else
    fail "wrapper should synthesize ALL_TASKS_DONE for complete plan" "text: $fallback_text"
fi

# ---------------------------------------------------------------------------
# test: task fallback does NOT fire when plan has unchecked tasks
# ---------------------------------------------------------------------------
echo "test: task fallback does NOT fire when plan has unchecked tasks"

reset_captures
plan_file_incomplete="$TMPDIR_TEST/plan-incomplete.md"
cat > "$plan_file_incomplete" <<'EOF'
# Fix bugs

### Task 1: Fix the widget
- [x] Refactor widget module
- [ ] Add unit tests

### Task 2: Update docs
- [ ] Update README
EOF

task_prompt_incomplete="Read the plan file at $plan_file_incomplete. Find the FIRST Task section.
When all tasks are done emit <<<RALPHEX:ALL_TASKS_DONE>>>
If task fails emit <<<RALPHEX:TASK_FAILED>>>"

cat > "$TMPDIR_TEST/task_no_signal_events2.jsonl" <<'EOF'
{"type":"assistant.message","data":{"content":"Working on the task..."}}
{"type":"session.task_complete","data":{"summary":"done","success":true}}
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/task_no_signal_events2.jsonl" \
    run_wrapper bash "$WRAPPER" -p "$task_prompt_incomplete" 2>/dev/null)

fallback_text=$(echo "$output" | jq -r 'select(.type=="content_block_delta") | .delta.text')
if echo "$fallback_text" | grep -q '<<<RALPHEX:ALL_TASKS_DONE>>>'; then
    fail "wrapper should NOT synthesize ALL_TASKS_DONE when plan has unchecked tasks" "text: $fallback_text"
else
    pass "wrapper correctly skips ALL_TASKS_DONE fallback for incomplete plan"
fi

# ---------------------------------------------------------------------------
# test: task fallback does NOT fire when copilot exits non-zero
# ---------------------------------------------------------------------------
echo "test: task fallback does NOT fire when copilot exits non-zero"

reset_captures
cat > "$TMPDIR_TEST/task_error_events.jsonl" <<'EOF'
{"type":"assistant.message","data":{"content":"Something went wrong."}}
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/task_error_events.jsonl" \
    MOCK_EXIT_CODE=1 \
    run_wrapper bash "$WRAPPER" -p "$task_prompt" 2>/dev/null || true)

fallback_text=$(echo "$output" | jq -r 'select(.type=="content_block_delta") | .delta.text')
if echo "$fallback_text" | grep -q '<<<RALPHEX:ALL_TASKS_DONE>>>'; then
    fail "wrapper should NOT synthesize ALL_TASKS_DONE on non-zero exit" "text: $fallback_text"
else
    pass "wrapper correctly skips ALL_TASKS_DONE fallback on error exit"
fi

# ---------------------------------------------------------------------------
# test: task fallback does NOT override explicit TASK_FAILED
# ---------------------------------------------------------------------------
echo "test: task fallback does NOT override explicit TASK_FAILED"

reset_captures
cat > "$TMPDIR_TEST/task_failed_with_complete_plan_events.jsonl" <<'EOF'
{"type":"assistant.message","data":{"content":"I hit a blocking error.\n<<<RALPHEX:TASK_FAILED>>>"}} 
{"type":"session.task_complete","data":{"summary":"failed","success":false}}
EOF

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/task_failed_with_complete_plan_events.jsonl" \
    run_wrapper bash "$WRAPPER" -p "$task_prompt" 2>/dev/null || true)

fallback_text=$(echo "$output" | jq -r 'select(.type=="content_block_delta" and .delta.text != "") | .delta.text')
if echo "$fallback_text" | grep -q '<<<RALPHEX:TASK_FAILED>>>' && \
    ! echo "$fallback_text" | grep -q '<<<RALPHEX:ALL_TASKS_DONE>>>'; then
    pass "wrapper preserves TASK_FAILED without synthesizing ALL_TASKS_DONE"
else
    fail "wrapper should not append ALL_TASKS_DONE after TASK_FAILED" "text: $fallback_text"
fi

# ---------------------------------------------------------------------------
# test: task fallback skips format-description checkboxes
# ---------------------------------------------------------------------------
echo "test: task fallback skips format-description checkboxes"

reset_captures
plan_file_format="$TMPDIR_TEST/plan-format-desc.md"
cat > "$plan_file_format" <<'EOF'
# Fix bugs

### Task 1: Fix the widget
- [x] Refactor widget module
- [x] Change [ ] to [x] in plan file

### Task 2: Update docs
- [x] Update README
EOF

task_prompt_format="Read the plan file at $plan_file_format. Find the FIRST Task section.
When all tasks are done emit <<<RALPHEX:ALL_TASKS_DONE>>>
If task fails emit <<<RALPHEX:TASK_FAILED>>>"

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/task_no_signal_events.jsonl" \
    run_wrapper bash "$WRAPPER" -p "$task_prompt_format" 2>/dev/null)

fallback_text=$(echo "$output" | jq -r 'select(.type=="content_block_delta") | .delta.text')
if echo "$fallback_text" | grep -q '<<<RALPHEX:ALL_TASKS_DONE>>>'; then
    pass "wrapper handles format-description checkboxes correctly"
else
    fail "wrapper should synthesize ALL_TASKS_DONE when only format-description [ ] remain" "text: $fallback_text"
fi

# ---------------------------------------------------------------------------
# test: task fallback handles plan with no task headers (malformed)
# ---------------------------------------------------------------------------
echo "test: task fallback handles plan with no task headers (malformed)"

reset_captures
plan_file_no_headers="$TMPDIR_TEST/plan-no-headers.md"
cat > "$plan_file_no_headers" <<'EOF'
# Fix bugs

- [ ] This is an unchecked item with no task header
EOF

task_prompt_no_headers="Read the plan file at $plan_file_no_headers. Find the FIRST Task section.
When all tasks are done emit <<<RALPHEX:ALL_TASKS_DONE>>>
If task fails emit <<<RALPHEX:TASK_FAILED>>>"

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/task_no_signal_events.jsonl" \
    run_wrapper bash "$WRAPPER" -p "$task_prompt_no_headers" 2>/dev/null)

fallback_text=$(echo "$output" | jq -r 'select(.type=="content_block_delta") | .delta.text')
if echo "$fallback_text" | grep -q '<<<RALPHEX:ALL_TASKS_DONE>>>'; then
    fail "wrapper should NOT synthesize ALL_TASKS_DONE for plan with unchecked items and no task headers" "text: $fallback_text"
else
    pass "wrapper correctly detects unchecked items in headerless plan"
fi

# ---------------------------------------------------------------------------
# test: task fallback keeps checkboxes under task subsections actionable
# ---------------------------------------------------------------------------
echo "test: task fallback keeps checkboxes under task subsections actionable"

reset_captures
plan_file_subsections="$TMPDIR_TEST/plan-subsections.md"
cat > "$plan_file_subsections" <<'EOF'
# Fix bugs

### Task 1: Fix the widget
- [x] Refactor widget module

### Validation notes
- [ ] Add unit tests after the subsection header
EOF

task_prompt_subsections="Read the plan file at $plan_file_subsections. Find the FIRST Task section.
When all tasks are done emit <<<RALPHEX:ALL_TASKS_DONE>>>
If task fails emit <<<RALPHEX:TASK_FAILED>>>"

output=$(MOCK_STDOUT_FILE="$TMPDIR_TEST/task_no_signal_events.jsonl" \
    run_wrapper bash "$WRAPPER" -p "$task_prompt_subsections" 2>/dev/null)

fallback_text=$(echo "$output" | jq -r 'select(.type=="content_block_delta" and .delta.text != "") | .delta.text')
if echo "$fallback_text" | grep -q '<<<RALPHEX:ALL_TASKS_DONE>>>'; then
    fail "wrapper should keep subsection checkboxes attached to the task" "text: $fallback_text"
else
    pass "wrapper keeps subsection checkboxes actionable for fallback completion checks"
fi

echo ""
echo "results: $passed passed, $failed failed, $total total"

if [[ $failed -gt 0 ]]; then
    exit 1
fi
