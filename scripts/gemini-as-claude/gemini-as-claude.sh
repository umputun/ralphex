#!/usr/bin/env bash
# gemini-as-claude.sh - wraps Gemini CLI to produce Claude-compatible stream-json output.
#
# this script translates Gemini CLI plain-text output into the Claude stream-json format
# that ralphex's ClaudeExecutor can parse, allowing Gemini to be used as a drop-in
# replacement for claude in task and review phases.
#
# config example (~/.config/ralphex/config or .ralphex/config):
#   claude_command = /path/to/gemini-as-claude.sh
#   claude_args =
#
# environment variables:
#   GEMINI_MODEL         - model to use (passed as --model flag when set)

set -euo pipefail

# verify jq is available (required for JSON translation)
command -v jq >/dev/null 2>&1 || { echo "error: jq is required but not found" >&2; exit 1; }

# verify gemini is available
command -v gemini >/dev/null 2>&1 || { echo "error: gemini is required but not found" >&2; exit 1; }

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
GEMINI_MODEL="${GEMINI_MODEL:-}"

# detect review prompts and prepend adapter text
is_review_prompt=0
if [[ "$prompt" == *"<<<RALPHEX:REVIEW_DONE>>>"* ]]; then
    is_review_prompt=1
fi

if [[ "$is_review_prompt" == "1" ]]; then
    adapter_text=$'Ralphex review adapter for Gemini:\n- Interpret review "Task tool" instructions as sequential steps: perform each review agent\'s work one at a time.\n- Gemini CLI does not support parallel sub-agents, so execute each review task sequentially.\n- Apply fixes after completing all review steps.\n- Keep original review workflow and all <<<RALPHEX:...>>> signals unchanged.'
    prompt="$adapter_text"$'\n\n'"$prompt"
fi

# build gemini arguments
gemini_args=("-y" "-p" "$prompt")
[[ -n "$GEMINI_MODEL" ]] && gemini_args+=(--model "$GEMINI_MODEL")

# temporary files for stderr capture and stdout piping.
# use a private temp directory for the FIFO to avoid TOCTOU race.
tmp_dir=$(mktemp -d)
stderr_file=$(mktemp)
stdout_pipe="$tmp_dir/stdout.fifo"
mkfifo "$stdout_pipe"

# cleanup temp files on exit
cleanup() {
    rm -f "$stderr_file" "$stdout_pipe"
    rm -rf "$tmp_dir"
}
trap cleanup EXIT

# trap SIGTERM and forward to gemini child process for graceful shutdown
gemini_pid=""
forward_signal() {
    if [[ -n "$gemini_pid" ]]; then
        kill -TERM "$gemini_pid" 2>/dev/null || true
    fi
    cleanup
    exit 143
}
trap forward_signal TERM

# run gemini in background, capturing stderr and piping stdout through named pipe.
# this allows us to capture the PID for SIGTERM forwarding while still streaming output.
gemini "${gemini_args[@]}" 2>"$stderr_file" > "$stdout_pipe" &
gemini_pid=$!

# read from stdout_pipe line by line and wrap in JSON
while IFS= read -r line || [[ -n "$line" ]]; do
    jq -cn --arg text "$line" \
        '{type: "content_block_delta", delta: {type: "text_delta", text: ($text + "\n")}}'
done < "$stdout_pipe"

# wait for gemini to finish and capture its exit code
gemini_exit=0
wait "$gemini_pid" || gemini_exit=$?
gemini_pid=""

# emit stderr as content_block_delta events for error/limit pattern detection
if [[ -s "$stderr_file" ]]; then
    while IFS= read -r err_line || [[ -n "$err_line" ]]; do
        [[ -z "$err_line" ]] && continue
        printf '%s\n' "$err_line" | jq -Rc '{type: "content_block_delta", delta: {type: "text_delta", text: (. + "\n")}}'
    done < "$stderr_file"
fi

# emit fallback result event
echo '{"type":"result","result":""}'

# preserve gemini's exit code
exit "$gemini_exit"