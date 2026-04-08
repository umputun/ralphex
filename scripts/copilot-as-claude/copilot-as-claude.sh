#!/usr/bin/env bash
# copilot-as-claude.sh - wraps GitHub Copilot CLI to produce Claude-compatible stream-json output.
#
# this script translates Copilot CLI JSONL events into the Claude stream-json format
# that ralphex's ClaudeExecutor can parse, allowing copilot to be used as a drop-in
# replacement for claude in task and review phases.
#
# config example (~/.config/ralphex/config or .ralphex/config):
#   claude_command = /path/to/copilot-as-claude.sh
#   claude_args =
#
# environment variables:
#   COPILOT_MODEL        - model to use (default: Copilot CLI default)
#   COPILOT_GITHUB_TOKEN - authentication token (native Copilot auth env var)
#   GH_TOKEN             - authentication token fallback used by Copilot CLI
#   GITHUB_TOKEN         - authentication token fallback used by Copilot CLI

set -euo pipefail

command -v jq >/dev/null 2>&1 || { echo "error: jq is required but not found" >&2; exit 1; }
command -v copilot >/dev/null 2>&1 || { echo "error: copilot is required but not found" >&2; exit 1; }

# ralphex passes prompt via stdin (primary path, avoids Windows 8191-char cmd limit).
# also accept -p/--prompt for backward compatibility with direct invocations.
# all other flags are ignored gracefully (--dangerously-skip-permissions, etc.)
prompt=""
while [[ $# -gt 0 ]]; do
    case "$1" in
        -p|--prompt)
            prompt="${2:-}"
            shift
            shift 2>/dev/null || true
            ;;
        *)
            shift
            ;;
    esac
done

if [[ -z "$prompt" ]]; then
    if [[ ! -t 0 ]]; then
        prompt=$(cat)
    fi
fi

if [[ -z "$prompt" ]]; then
    echo "error: no prompt provided (expected -p flag or stdin)" >&2
    exit 1
fi

COPILOT_MODEL="${COPILOT_MODEL:-}"

is_review_prompt=0
if [[ "$prompt" == *"<<<RALPHEX:REVIEW_DONE>>>"* ]]; then
    is_review_prompt=1
fi

if [[ "$is_review_prompt" == "1" ]]; then
    adapter_text=$'Ralphex review adapter for GitHub Copilot CLI:\n- The review prompts refer to Claude "Task tool" calls. In Copilot CLI, interpret those as agent delegation instructions.\n- When the prompt asks for multiple review agents in parallel, delegate them as separate sub-agents in the same turn when the model supports it.\n- If true parallel delegation is unavailable in the current Copilot session, execute every requested review role sequentially without dropping any of them.\n- Keep delegated review prompts short: each agent should inspect the diff directly, read the source files in full context, and report problems only.\n- After all delegated reviews finish, verify findings against the actual code, fix confirmed issues, rerun required tests and lint, and preserve all <<<RALPHEX:...>>> signals verbatim.'
    prompt="$adapter_text"$'\n\n'"$prompt"
fi

copilot_args=(-s --output-format json --stream on --autopilot --no-ask-user --allow-all)
[[ -n "$COPILOT_MODEL" ]] && copilot_args+=(--model "$COPILOT_MODEL")

tmp_dir=$(mktemp -d)
stderr_file=$(mktemp)
prompt_file=$(mktemp)
stdout_pipe="$tmp_dir/stdout.fifo"
mkfifo "$stdout_pipe"
printf '%s' "$prompt" > "$prompt_file"

cleanup() {
    rm -f "$stderr_file" "$prompt_file" "$stdout_pipe"
    rm -rf "$tmp_dir"
}
trap cleanup EXIT

copilot_pid=""
term_requested=0
forward_signal() {
    term_requested=1
    if [[ -n "$copilot_pid" ]]; then
        kill -TERM "$copilot_pid" 2>/dev/null || true
    fi
}
trap forward_signal TERM

copilot "${copilot_args[@]}" < "$prompt_file" > "$stdout_pipe" 2>"$stderr_file" &
copilot_pid=$!

seen_message_delta=$'\n'

emit_text_delta() {
    local text="$1"
    jq -cn --arg text "$text" \
        '{type: "content_block_delta", delta: {type: "text_delta", text: $text}}'
}

mark_message_delta_seen() {
    local message_id="$1"
    seen_message_delta+="${message_id}"$'\n'
}

has_message_delta_seen() {
    local message_id="$1"
    [[ "$seen_message_delta" == *$'\n'"${message_id}"$'\n'* ]]
}

while IFS= read -r line || [[ -n "$line" ]]; do
    [[ -z "$line" ]] && continue

    if ! printf '%s\n' "$line" | jq -e . >/dev/null 2>&1; then
        emit_text_delta "$line"$'\n'
        continue
    fi

    event_type=$(printf '%s\n' "$line" | jq -r '.type // empty')

    case "$event_type" in
        assistant.message_delta)
            message_id=$(printf '%s\n' "$line" | jq -r '.data.messageId // empty')
            delta_text=$(printf '%s\n' "$line" | jq -r '.data.deltaContent // empty')
            [[ -n "$message_id" ]] && mark_message_delta_seen "$message_id"
            [[ -n "$delta_text" ]] && emit_text_delta "$delta_text"
            ;;
        assistant.message)
            message_id=$(printf '%s\n' "$line" | jq -r '.data.messageId // empty')
            message_text=$(printf '%s\n' "$line" | jq -r '.data.content // empty')
            if [[ -n "$message_text" ]]; then
                if ! has_message_delta_seen "$message_id" || [[ "$message_text" == *"<<<RALPHEX:"* ]]; then
                    emit_text_delta "$message_text"
                fi
            fi
            ;;
        session.error|session.warning|session.info)
            event_text=$(printf '%s\n' "$line" | jq -r '.data.message // empty')
            [[ -n "$event_text" ]] && emit_text_delta "$event_text"$'\n'
            ;;
        session.task_complete|session.idle|assistant.turn_end|session.shutdown)
            ;;
    esac
done < "$stdout_pipe"

copilot_exit=0
wait "$copilot_pid" || copilot_exit=$?
copilot_pid=""

if [[ -s "$stderr_file" ]]; then
    while IFS= read -r err_line || [[ -n "$err_line" ]]; do
        [[ -z "$err_line" ]] && continue
        emit_text_delta "$err_line"$'\n'
    done < "$stderr_file"
fi

if [[ "$term_requested" == "0" ]]; then
    echo '{"type":"result","result":""}'
fi

exit "$copilot_exit"
