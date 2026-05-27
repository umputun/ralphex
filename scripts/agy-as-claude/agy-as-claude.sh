#!/usr/bin/env bash
# agy-as-claude.sh - wraps agy (Antigravity) CLI to produce Claude-compatible stream-json output.
#
# this script translates agy plain-text output into the Claude stream-json format
# that ralphex's ClaudeExecutor can parse, allowing agy to be used as a drop-in
# replacement for claude in task and review phases.
#
# config example (~/.config/ralphex/config or .ralphex/config):
#   claude_command = /path/to/agy-as-claude.sh
#   claude_args =
#
# environment variables:
#   AGY_PRINT_TIMEOUT    - print mode timeout passed to agy (default 2h). The agy CLI
#                          defaults to 5m which is too short for ralphex task/review phases.
#
# tested with: agy 1.0.2. The wrapper requires the agy flags --dangerously-skip-permissions,
# --print-timeout, and -p. Model selection is not exposed (agy 1.0.2 has no --model flag).
#
# env isolation: the wrapper unsets every ANTIGRAVITY_* variable (prefix-wide, via
# `unset ${!ANTIGRAVITY_@}`) before invoking agy to prevent recursion/deadlocks when
# running inside an active Antigravity agent process. This is intentional and survives
# Antigravity adding new ANTIGRAVITY_* vars without wrapper updates.
#

set -euo pipefail

# verify jq is available (required for JSON translation)
command -v jq >/dev/null 2>&1 || { echo "error: jq is required but not found" >&2; exit 1; }

# verify agy is available
command -v agy >/dev/null 2>&1 || { echo "error: agy is required but not found" >&2; exit 1; }

# ralphex passes prompt via stdin (primary path, avoids Windows 8191-char cmd limit).
# also accept -p flag for backward compatibility with direct invocations.
# all other flags are ignored gracefully.
prompt=""
while [[ $# -gt 0 ]]; do
    case "$1" in
        -p) prompt="${2:-}"; shift; shift 2>/dev/null || true ;;
        *)  shift ;; # ignore unknown flags
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

# detect review prompts and prepend adapter text
is_review_prompt=0
if [[ "$prompt" == *"<<<RALPHEX:REVIEW_DONE>>>"* ]]; then
    is_review_prompt=1
fi

if [[ "$is_review_prompt" == "1" ]]; then
    adapter_text=$'Ralphex review adapter for Antigravity:\n- Interpret review "Task tool" instructions as sequential steps: perform each review agent\'s work one at a time.\n- Antigravity CLI does not support parallel sub-agents, so execute each review task sequentially.\n- Apply fixes after completing all review steps.\n- Keep original review workflow and all <<<RALPHEX:...>>> signals unchanged.'
    prompt="$adapter_text"$'\n\n'"$prompt"
fi

# configurable via environment; agy's default 5m is too short for ralphex sessions
AGY_PRINT_TIMEOUT="${AGY_PRINT_TIMEOUT:-2h}"

# build agy arguments. We use --dangerously-skip-permissions to run unattended.
agy_args=("--dangerously-skip-permissions" "--print-timeout" "${AGY_PRINT_TIMEOUT}" "-p" "$prompt")

# temporary files for stderr capture and stdout piping.
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

# trap SIGTERM and forward to agy child process for graceful shutdown
agy_pid=""
forward_signal() {
    if [[ -n "$agy_pid" ]]; then
        kill -TERM "$agy_pid" 2>/dev/null || true
    fi
    cleanup
    exit 143
}
trap forward_signal TERM

# CRITICAL: unset ANTIGRAVITY_* environment variables to prevent deadlock/recursion
# when running agy within an active Antigravity agent process.
# We also redirect stdin from /dev/null to prevent agy from blocking on stdin.
unset ${!ANTIGRAVITY_@}
agy "${agy_args[@]}" < /dev/null 2>"$stderr_file" > "$stdout_pipe" &
agy_pid=$!

# read from stdout_pipe line by line and wrap in JSON
while IFS= read -r line || [[ -n "$line" ]]; do
    jq -cn --arg text "$line" \
        '{type: "content_block_delta", delta: {type: "text_delta", text: ($text + "\n")}}'
done < "$stdout_pipe"

# wait for agy to finish and capture its exit code
agy_exit=0
wait "$agy_pid" || agy_exit=$?
agy_pid=""

# emit stderr as content_block_delta events for error/limit pattern detection
if [[ -s "$stderr_file" ]]; then
    while IFS= read -r err_line || [[ -n "$err_line" ]]; do
        [[ -z "$err_line" ]] && continue
        printf '%s\n' "$err_line" | jq -Rc '{type: "content_block_delta", delta: {type: "text_delta", text: (. + "\n")}}'
    done < "$stderr_file"
fi

# emit fallback result event
echo '{"type":"result","result":""}'

# preserve agy's exit code
exit "$agy_exit"
