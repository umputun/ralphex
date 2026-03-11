#!/usr/bin/env bash
# opencode-as-claude.sh - wraps OpenCode CLI to produce Claude-compatible stream-json output.
#
# this script translates OpenCode JSONL events into the Claude stream-json format
# that ralphex's ClaudeExecutor can parse, allowing OpenCode to be used as a drop-in
# replacement for claude in task and review phases.
#
# config example (~/.config/ralphex/config or .ralphex/config):
#   claude_command = /path/to/opencode-as-claude.sh
#   claude_args =
#
# environment variables:
#   OPENCODE_MODEL       - model in provider/model format, e.g. openai/gpt-4o (default: opencode default)
#   OPENCODE_VERBOSE     - set to 1 to include tool execution events in output (default: 0)

set -euo pipefail

# verify jq is available (required for JSON translation)
command -v jq >/dev/null 2>&1 || { echo "error: jq is required but not found" >&2; exit 1; }

# verify opencode is available
command -v opencode >/dev/null 2>&1 || { echo "error: opencode is required but not found" >&2; exit 1; }

# extract prompt from -p argument (last two args from ClaudeExecutor).
# all other flags are ignored gracefully (--dangerously-skip-permissions, etc.)
prompt=""
while [[ $# -gt 0 ]]; do
    case "$1" in
        -p) prompt="${2:-}"; shift; shift 2>/dev/null || true ;;
        *)  shift ;; # ignore unknown flags
    esac
done

if [[ -z "$prompt" ]]; then
    echo "error: no prompt provided (-p flag required)" >&2
    exit 1
fi

# configurable via environment
OPENCODE_MODEL="${OPENCODE_MODEL:-}"
OPENCODE_VERBOSE="${OPENCODE_VERBOSE:-0}"

# enable auto-allow permissions for autonomous execution (equivalent to claude's
# --dangerously-skip-permissions). uses OPENCODE_CONFIG_CONTENT which deep-merges
# with existing config without replacing user settings.
if [[ -z "${OPENCODE_CONFIG_CONTENT:-}" ]]; then
    export OPENCODE_CONFIG_CONTENT='{"permission":{"*":"allow"}}'
else
    # merge allow-all into existing OPENCODE_CONFIG_CONTENT via jq
    OPENCODE_CONFIG_CONTENT=$(echo "$OPENCODE_CONFIG_CONTENT" | jq -c '. * {"permission":{"*":"allow"}}')
    export OPENCODE_CONFIG_CONTENT
fi

if [[ "$OPENCODE_VERBOSE" != "0" && "$OPENCODE_VERBOSE" != "1" ]]; then
    echo "warning: OPENCODE_VERBOSE must be 0 or 1, got '$OPENCODE_VERBOSE', defaulting to 0" >&2
    OPENCODE_VERBOSE=0
fi

# detect review prompts and prepend adapter text
is_review_prompt=0
if [[ "$prompt" == *"<<<RALPHEX:REVIEW_DONE>>>"* ]]; then
    is_review_prompt=1
fi

if [[ "$is_review_prompt" == "1" ]]; then
    adapter_text=$'Ralphex review adapter for OpenCode:\n- Interpret review "Task tool" instructions as sequential steps: perform each review agent\'s work one at a time.\n- OpenCode does not support parallel sub-agents, so execute each review task sequentially.\n- Apply fixes after completing all review steps.\n- Keep original review workflow and all <<<RALPHEX:...>>> signals unchanged.'
    prompt="$adapter_text"$'\n\n'"$prompt"
fi

# build opencode arguments
opencode_args=(run --format json)
[[ -n "$OPENCODE_MODEL" ]] && opencode_args+=(--model "$OPENCODE_MODEL")
opencode_args+=("$prompt")

# temporary files for stderr capture and stdout piping.
# use a private temp directory for the FIFO to avoid TOCTOU race with mktemp -u.
tmp_dir=$(mktemp -d)
stderr_file=$(mktemp)
stdout_pipe="$tmp_dir/stdout.fifo"
mkfifo "$stdout_pipe"

# write output rules instruction file to prevent LLM from echoing signal strings.
# opencode's system prompt lacks Claude Code's "do not restate what the user said"
# directive, so the model may echo <<<RALPHEX:...>>> signals from the prompt in its
# planning output, causing false signal detection in ralphex.
instructions_file="$tmp_dir/output-rules.md"
cat > "$instructions_file" <<'INSTREOF'
# Output rules
- Be concise and direct. Lead with the answer or action, not the reasoning.
- Do not restate or echo the user's prompt. Skip preamble and unnecessary transitions.
- NEVER quote <<<RALPHEX:...>>> signal strings in your planning or reasoning output. Only emit them as actual signals when the instructions tell you to.
INSTREOF

# append instructions file path to OPENCODE_CONFIG_CONTENT (preserve existing instructions)
OPENCODE_CONFIG_CONTENT=$(echo "$OPENCODE_CONFIG_CONTENT" | jq -c --arg f "$instructions_file" '.instructions = ((.instructions // []) + [$f])')
export OPENCODE_CONFIG_CONTENT

# cleanup temp files on exit
cleanup() {
    rm -f "$stderr_file" "$stdout_pipe"
    rm -rf "$tmp_dir"
}
trap cleanup EXIT

# trap SIGTERM and forward to opencode child process for graceful shutdown
opencode_pid=""
forward_signal() {
    if [[ -n "$opencode_pid" ]]; then
        kill -TERM "$opencode_pid" 2>/dev/null || true
    fi
}
trap forward_signal TERM

# run opencode in background, capturing stderr and piping stdout through named pipe.
# this allows us to capture the PID for SIGTERM forwarding while still streaming output.
opencode "${opencode_args[@]}" 2>"$stderr_file" > "$stdout_pipe" &
opencode_pid=$!

# run opencode with JSON output, translate events to claude stream-json format.
# stderr is captured to a temp file and emitted as content_block_delta events
# after the main stream, enabling ralphex error/limit pattern detection (R6).
#
# event mapping:
#   text         -> content_block_delta (text_delta)
#   step_finish  -> result (end of execution)
#   step_start   -> skipped (or included if OPENCODE_VERBOSE=1)
#
# text content is passed verbatim through jq's .part.text — no truncation
# or escaping changes, preserving signal strings like <<<RALPHEX:...>>> (R4).
while IFS= read -r line || [[ -n "$line" ]]; do
    translated=$(echo "$line" | jq -c --argjson verbose "$OPENCODE_VERBOSE" '
        if .type == "text" then
            {type: "content_block_delta", delta: {type: "text_delta", text: .part.text}}
        elif .type == "step_finish" then
            {type: "result", result: ""}
        elif .type == "step_start" and $verbose == 1 then
            {type: "content_block_delta", delta: {type: "text_delta", text: "[step started]\n"}}
        else empty
        end
    ' 2>/dev/null) || true
    if [[ -n "$translated" ]]; then
        echo "$translated"
    elif ! echo "$line" | jq -e . >/dev/null 2>&1; then
        # pass non-JSON lines through so the executor's non-JSON fallback can see them;
        # valid JSON mapped to empty (e.g. step_start) is intentionally suppressed
        echo "$line"
    fi
done < "$stdout_pipe"

# wait for opencode to finish and capture its exit code (R9)
opencode_exit=0
wait "$opencode_pid" || opencode_exit=$?
opencode_pid=""

# emit stderr as content_block_delta events for error/limit pattern detection (R6)
if [[ -s "$stderr_file" ]]; then
    while IFS= read -r err_line || [[ -n "$err_line" ]]; do
        [[ -z "$err_line" ]] && continue
        echo "$err_line" | jq -Rc '{type: "content_block_delta", delta: {type: "text_delta", text: .}}'
    done < "$stderr_file"
fi

# emit fallback result event if opencode exited without step_finish
echo '{"type":"result","result":""}'

# preserve opencode's exit code on failure (R9)
exit "$opencode_exit"
