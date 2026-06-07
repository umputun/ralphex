#!/usr/bin/env bash
# pi-as-claude.sh - wraps the pi CLI to produce Claude-compatible stream-json output.
#
# this script translates pi's `--mode json` event stream into the Claude stream-json
# format that ralphex's ClaudeExecutor can parse, allowing pi to be used as a drop-in
# replacement for claude in task and review phases.
#
# config example (~/.config/ralphex/config or .ralphex/config):
#   claude_command = /path/to/pi-as-claude.sh
#   claude_args =
#
# environment variables:
#   PI_PROVIDER          - provider to use (passed as --provider flag when set)
#   PI_MODEL             - model to use (passed as --model when --model flag absent)
#   PI_THINKING          - thinking level (used when --effort flag absent)
#   PI_VERBOSE           - set to 1 to include tool execution output (default: 0)

set -euo pipefail

# verify jq is available (required for JSON translation)
command -v jq >/dev/null 2>&1 || { echo "error: jq is required but not found" >&2; exit 1; }

# verify pi is available
command -v pi >/dev/null 2>&1 || { echo "error: pi is required but not found" >&2; exit 1; }

# ralphex passes prompt via stdin (primary path, avoids Windows 8191-char cmd limit).
# also accept -p flag for backward compatibility with direct invocations.
# --model and --effort are parsed explicitly (ralphex appends them per phase);
# all other flags are ignored gracefully (--dangerously-skip-permissions, etc.)
prompt=""
model_flag=""
effort_flag=""
while [[ $# -gt 0 ]]; do
    case "$1" in
        -p)       prompt="${2:-}"; shift; shift 2>/dev/null || true ;;
        --model)  model_flag="${2:-}"; shift; shift 2>/dev/null || true ;;
        --effort) effort_flag="${2:-}"; shift; shift 2>/dev/null || true ;;
        *)        shift ;; # ignore unknown flags
    esac
done

if [[ -z "$prompt" ]]; then
    # fall back to stdin: ralphex passes prompt via pipe to avoid Windows 8191-char cmd limit.
    # only read when stdin is not a terminal to avoid blocking interactive invocations.
    if [[ ! -t 0 ]]; then
        prompt=$(cat)
    fi
fi

if [[ -z "$prompt" ]]; then
    echo "error: no prompt provided (expected -p flag or stdin)" >&2
    exit 1
fi

# configurable via environment
PI_PROVIDER="${PI_PROVIDER:-}"
PI_MODEL="${PI_MODEL:-}"
PI_THINKING="${PI_THINKING:-}"
PI_VERBOSE="${PI_VERBOSE:-0}"
if [[ "$PI_VERBOSE" != "0" && "$PI_VERBOSE" != "1" ]]; then
    echo "warning: PI_VERBOSE must be 0 or 1, got '$PI_VERBOSE', defaulting to 0" >&2
    PI_VERBOSE=0
fi

# resolve model: explicit --model flag wins over PI_MODEL env
model="$model_flag"
[[ -z "$model" ]] && model="$PI_MODEL"

# resolve thinking level: explicit --effort flag wins over PI_THINKING env.
# pi accepts off|minimal|low|medium|high|xhigh; ralphex's effort levels map directly
# except `max`, which pi lacks — fall back to `xhigh` with a one-line note (like codex).
thinking=""
if [[ -n "$effort_flag" ]]; then
    case "$effort_flag" in
        off|minimal|low|medium|high|xhigh) thinking="$effort_flag" ;;
        max) thinking="xhigh"; echo "note: pi has no 'max' thinking level; using 'xhigh' instead" >&2 ;;
        *)   thinking="$effort_flag" ;; # passthrough; pi validates
    esac
elif [[ -n "$PI_THINKING" ]]; then
    thinking="$PI_THINKING"
fi

# build pi arguments: JSON event stream, non-interactive, prompt as positional arg
pi_args=(--mode json --print)
[[ -n "$PI_PROVIDER" ]] && pi_args+=(--provider "$PI_PROVIDER")
[[ -n "$model" ]] && pi_args+=(--model "$model")
[[ -n "$thinking" ]] && pi_args+=(--thinking "$thinking")
pi_args+=("$prompt")

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

# trap SIGTERM and forward to pi child process for graceful shutdown
pi_pid=""
forward_signal() {
    if [[ -n "$pi_pid" ]]; then
        kill -TERM "$pi_pid" 2>/dev/null || true
    fi
    cleanup
    exit 143
}
trap forward_signal TERM

# run pi in background, capturing stderr and piping stdout through named pipe.
# this allows us to capture the PID for SIGTERM forwarding while still streaming output.
pi "${pi_args[@]}" 2>"$stderr_file" > "$stdout_pipe" &
pi_pid=$!

# read from stdout_pipe line by line and wrap in JSON.
# NOTE: this is a basic passthrough; Task 2 replaces it with pi JSON event mapping.
while IFS= read -r line || [[ -n "$line" ]]; do
    jq -cn --arg text "$line" \
        '{type: "content_block_delta", delta: {type: "text_delta", text: ($text + "\n")}}'
done < "$stdout_pipe"

# wait for pi to finish and capture its exit code
pi_exit=0
wait "$pi_pid" || pi_exit=$?
pi_pid=""

# emit fallback result event
echo '{"type":"result","result":""}'

# preserve pi's exit code
exit "$pi_exit"
