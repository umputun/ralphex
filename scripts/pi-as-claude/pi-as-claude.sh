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
#   PI_EXTRA_ARGS        - extra args appended verbatim to the pi invocation,
#                          word-split on whitespace (e.g. "--nolo-mode full" to
#                          auto-approve tools in non-interactive runs)

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
PI_EXTRA_ARGS="${PI_EXTRA_ARGS:-}"

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

# detect review prompts and prepend a pi-appropriate adapter.
# pi exposes no parallel sub-agents, so instruct sequential per-agent review.
if [[ "$prompt" == *"<<<RALPHEX:REVIEW_DONE>>>"* ]]; then
    adapter_text=$'Ralphex review adapter for pi:\n- Interpret review "Task tool" instructions as sequential steps: perform each review agent\'s work one at a time.\n- pi does not support parallel sub-agents, so execute each review task sequentially using pi\'s read, bash, edit, and write tools.\n- Apply fixes after completing all review steps.\n- Keep original review workflow and all <<<RALPHEX:...>>> signals unchanged.'
    prompt="$adapter_text"$'\n\n'"$prompt"
fi

# build pi arguments: JSON event stream, non-interactive. the prompt is NOT placed
# on argv — `pi --mode json --print` reads it from stdin when no positional message
# is given. a large review prompt (full diff) can exceed Linux's 128 KB per-arg cap,
# so we deliver it via stdin (see prompt_file below), mirroring copilot-as-claude.sh.
pi_args=(--mode json --print)
[[ -n "$PI_PROVIDER" ]] && pi_args+=(--provider "$PI_PROVIDER")
[[ -n "$model" ]] && pi_args+=(--model "$model")
[[ -n "$thinking" ]] && pi_args+=(--thinking "$thinking")
# append caller-supplied extra args (word-split); guard the empty case so the
# array expansion does not trip `set -u` on bash 3.2 (macOS system bash).
# these are now the final entries of pi_args (no positional prompt follows).
if [[ -n "$PI_EXTRA_ARGS" ]]; then
    read -ra pi_extra_args <<< "$PI_EXTRA_ARGS"
    pi_args+=("${pi_extra_args[@]}")
fi

# temporary files for stderr capture and stdout piping.
# use a private temp directory for the FIFO to avoid TOCTOU race.
tmp_dir=$(mktemp -d)
stderr_file=$(mktemp)
stdout_pipe="$tmp_dir/stdout.fifo"
mkfifo "$stdout_pipe"

# deliver the (possibly adapter-prepended) prompt to pi via stdin, not argv:
# avoids the per-arg length cap on large review prompts containing a full diff.
prompt_file=$(mktemp)
printf '%s' "$prompt" > "$prompt_file"

# cleanup temp files on exit
cleanup() {
    rm -f "$stderr_file" "$stdout_pipe" "$prompt_file"
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
pi "${pi_args[@]}" < "$prompt_file" 2>"$stderr_file" > "$stdout_pipe" &
pi_pid=$!

# translate pi's JSONL event stream into claude stream-json.
# only assistant text is emitted by default — tool executions, the session header,
# and other lifecycle events are noise and are skipped (include tool lines with
# PI_VERBOSE=1).
#
# pi emits assistant text as token-level `text_delta` deltas (e.g. "The", " quick",
# " brown"), so the stream must be re-assembled into whole lines before emission.
# emitting one content_block_delta per token would (a) garble the progress log with a
# newline after every token and (b) split ralphex `<<<RALPHEX:...>>>` signals across
# blocks, where the executor's per-block detectSignal could never match them. we buffer
# deltas and flush only complete lines, so a signal lands intact in a single block.
#
# event mapping:
#   message_update + assistantMessageEvent.type=="text_delta" -> buffered, complete
#                                                                lines -> content_block_delta
#   tool_execution_start/update/end                           -> skipped (or PI_VERBOSE=1)
#   session header, queue_update, compaction_*, auto_retry_*  -> skipped
#   turn_end / agent_end                                      -> flush buffer, then result
#
# the whole stream is fed to one jq process (foreach over `inputs`); `fromjson?` skips
# malformed lines, and a trailing `__eof__` sentinel flushes any unterminated final line.
jq -Rcn --unbuffered --argjson verbose "$PI_VERBOSE" '
    def emit($t): {type: "content_block_delta", delta: {type: "text_delta", text: $t}};
    def flush:    if .buf != "" then [emit(.buf + "\n")] else [] end;
    foreach ((inputs | fromjson?), {type: "__eof__"}) as $e (
        {buf: "", out: []};
        if $e.type == "message_update" and $e.assistantMessageEvent.type == "text_delta" then
            ((.buf + ($e.assistantMessageEvent.delta // "")) | split("\n")) as $parts
            | {buf: $parts[-1], out: ($parts[0:-1] | map(emit(. + "\n")))}
        elif (($e.type // "") | startswith("tool_execution_")) and $verbose == 1 then
            {buf: "", out: (flush + [emit("[tool] " + $e.type + " " + ($e.toolName // "") + "\n")])}
        elif $e.type == "turn_end" or $e.type == "agent_end" then
            {buf: "", out: (flush + [{type: "result", result: ""}])}
        elif $e.type == "__eof__" then
            {buf: "", out: flush}
        else
            {buf: .buf, out: []}
        end;
        .out[]
    )
' < "$stdout_pipe" 2>/dev/null || true

# wait for pi to finish and capture its exit code
pi_exit=0
wait "$pi_pid" || pi_exit=$?
pi_pid=""

# emit stderr as content_block_delta events so ralphex error/limit pattern
# detection still works (pi may report rate limits / failures on stderr).
# stderr is emitted only for error/limit detection, so neutralize any literal
# `<<<RALPHEX:` signal token first: re-emitted stderr runs through ralphex's
# signal detection, and a stray token on stderr must not be mistaken for a real
# completion signal. inserting a space breaks the prefix detectSignal keys on
# while leaving rate-limit / `API Error:` phrases intact for error/limit checks.
if [[ -s "$stderr_file" ]]; then
    while IFS= read -r err_line || [[ -n "$err_line" ]]; do
        [[ -z "$err_line" ]] && continue
        err_line="${err_line//<<<RALPHEX:/<<< RALPHEX:}"
        printf '%s\n' "$err_line" | jq -Rc '{type: "content_block_delta", delta: {type: "text_delta", text: (. + "\n")}}'
    done < "$stderr_file"
fi

# emit fallback result event (covers pi exiting without a turn_end/agent_end)
echo '{"type":"result","result":""}'

# preserve pi's exit code
exit "$pi_exit"
