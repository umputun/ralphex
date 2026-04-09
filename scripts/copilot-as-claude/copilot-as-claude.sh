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

is_plan_prompt=0
if [[ "$prompt" == *"<<<RALPHEX:QUESTION>>>"* && \
    "$prompt" == *"<<<RALPHEX:PLAN_DRAFT>>>"* && \
    "$prompt" == *"<<<RALPHEX:PLAN_READY>>>"* ]]; then
    is_plan_prompt=1
fi

if [[ "$is_review_prompt" == "1" ]]; then
    adapter_text=$'FORMATTING RULE (strict): All output — including everything produced by sub-agents — must be plain text only. No markdown: no # or ## headers, no **bold** or *italic*, no `backtick` code spans, no ``` code fences, no --- horizontal rules. Use plain indented lists (  - item) for structure. This renders in a terminal with no markdown support; markdown syntax appears as literal characters.\n\nRalphex review adapter for GitHub Copilot CLI:\n- The review prompts refer to Claude "Task tool" calls. In Copilot CLI, interpret those as agent delegation instructions.\n- When the prompt asks for multiple review agents in parallel, delegate them as separate sub-agents in the same turn when the model supports it.\n- If true parallel delegation is unavailable in the current Copilot session, execute every requested review role sequentially without dropping any of them.\n- Keep delegated review prompts short: each agent should inspect the diff directly, read the source files in full context, and report problems only.\n- After all delegated reviews finish, verify findings against the actual code, fix confirmed issues, rerun required tests and lint, and preserve all <<<RALPHEX:...>>> signals verbatim.'
    prompt="$adapter_text"$'\n\n'"$prompt"
fi

if [[ "$is_plan_prompt" == "1" ]]; then
    plan_adapter_text=$'FORMATTING RULE (strict): Your analysis and thinking text — everything you write before emitting <<<RALPHEX:QUESTION>>> or <<<RALPHEX:PLAN_DRAFT>>> — must be plain text only. No backticks around code names or identifiers, no # or ## headers, no **bold** or *italic*, no markdown of any kind. Refer to code names as plain words (e.g. myFunction() not `myFunction()`). The plan document body inside <<<RALPHEX:PLAN_DRAFT>>>...<<<RALPHEX:END>>> must use the plan file markdown format exactly as specified in the prompt. Preserve all <<<RALPHEX:...>>> signals verbatim.\n\nPLAN REVIEW RULE (overrides other instructions): You MUST always present a <<<RALPHEX:PLAN_DRAFT>>>...<<<RALPHEX:END>>> block for user review before emitting <<<RALPHEX:PLAN_READY>>>. If you find an existing plan file matching the request, read its full contents and present them inside a PLAN_DRAFT block. Only emit PLAN_READY after writing or rewriting the plan file on disk following user acceptance.'
    prompt="$plan_adapter_text"$'\n\n'"$prompt"
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
    # copilot may write tab characters or other control sequences to /dev/tty
    # (e.g. session status indicators in --silent mode).  reset the cursor to
    # column 0 and clear to end-of-line so those stray chars don't appear after
    # the next prompt ralphex prints (e.g. "Continue with plan implementation?")
    # use a subshell so that a failed >/dev/tty open (no controlling terminal)
    # is fully suppressed — bash prints the error to the shell's own stderr
    # before 2>/dev/null takes effect on the command, but the subshell's fd 2
    # is redirected before any inner redirections are attempted.
    (printf '\r\033[K' >/dev/tty) 2>/dev/null || true
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

emit_text_delta() {
    local text="$1"
    # before the first output, clear any stray characters copilot may have
    # written directly to /dev/tty (spinner/progress indicator tab).  this must
    # happen before ralphex's progress logger prints the first timestamped line,
    # otherwise the stray char appears prepended to the timestamp.
    if [[ "$first_output_emitted" == "0" ]]; then
        first_output_emitted=1
        (printf '\r\033[K' >/dev/tty) 2>/dev/null || true
    fi
    jq -cn --arg text "$text" \
        '{type: "content_block_delta", delta: {type: "text_delta", text: $text}}'
}

extract_plan_block_for_marker() {
    local text="$1"
    local marker="$2"
    local end_marker="<<<RALPHEX:END>>>"
    local before
    local rest
    local body

    [[ "$text" == *"$marker"* ]] || return 1

    before=${text%%"$marker"*}
    rest=${text#*"$marker"}
    [[ "$rest" == *"$end_marker"* ]] || return 1

    body=${rest%%"$end_marker"*}
    printf '%s%s%s%s' "$before" "$marker" "$body" "$end_marker"
}

extract_plan_ready_signal() {
    local text="$1"
    local marker="<<<RALPHEX:PLAN_READY>>>"
    local before

    [[ "$text" == *"$marker"* ]] || return 1

    before=${text%%"$marker"*}
    printf '%s%s' "$before" "$marker"
}

plan_boundary_text=""
extract_first_plan_boundary() {
    local text="$1"
    local question_marker="<<<RALPHEX:QUESTION>>>"
    local draft_marker="<<<RALPHEX:PLAN_DRAFT>>>"
    local ready_marker="<<<RALPHEX:PLAN_READY>>>"
    local question_text=""
    local draft_text=""
    local ready_text=""
    local question_pos=-1
    local draft_pos=-1
    local ready_pos=-1
    local question_before
    local draft_before
    local ready_before

    plan_boundary_text=""

    if [[ "$text" == *"$question_marker"* ]]; then
        question_before=${text%%"$question_marker"*}
        if question_text=$(extract_plan_block_for_marker "$text" "$question_marker"); then
            question_pos=${#question_before}
        fi
    fi

    if [[ "$text" == *"$draft_marker"* ]]; then
        draft_before=${text%%"$draft_marker"*}
        if draft_text=$(extract_plan_block_for_marker "$text" "$draft_marker"); then
            draft_pos=${#draft_before}
        fi
    fi

    if [[ "$text" == *"$ready_marker"* ]]; then
        ready_before=${text%%"$ready_marker"*}
        if ready_text=$(extract_plan_ready_signal "$text"); then
            ready_pos=${#ready_before}
        fi
    fi

    if [[ $question_pos -ge 0 && ( $draft_pos -lt 0 || $question_pos -le $draft_pos ) &&
        ( $ready_pos -lt 0 || $question_pos -le $ready_pos ) ]]; then
        plan_boundary_text="$question_text"
        return 0
    fi

    if [[ $draft_pos -ge 0 && ( $ready_pos -lt 0 || $draft_pos -le $ready_pos ) ]]; then
        plan_boundary_text="$draft_text"
        return 0
    fi

    if [[ $ready_pos -ge 0 ]]; then
        plan_boundary_text="$ready_text"
        return 0
    fi

    return 1
}

intentional_stop=0
turn_has_visible_message=0
turn_has_tool_requests=0
first_output_emitted=0

while IFS= read -r line || [[ -n "$line" ]]; do
    [[ -z "$line" ]] && continue

    if ! printf '%s\n' "$line" | jq -e . >/dev/null 2>&1; then
        emit_text_delta "$line"$'\n'
        continue
    fi

    event_type=$(printf '%s\n' "$line" | jq -r '.type // empty')

    case "$event_type" in
        assistant.message_delta)
            continue
            ;;
        assistant.message)
            message_text=$(printf '%s\n' "$line" | jq -r '.data.content // empty')
            # strip leading and trailing tabs from each line — Copilot indents tool-result
            # summaries with tabs, and may also leave trailing tabs that appear as stray
            # whitespace in the ralphex terminal display
            [[ -n "$message_text" ]] && message_text=$(printf '%s\n' "$message_text" | sed 's/^\t\+//; s/\t\+$//')
            tool_request_count=$(printf '%s\n' "$line" | jq -r '(.data.toolRequests // []) | length')
            if [[ -n "$message_text" ]]; then
                if [[ "$is_plan_prompt" == "1" ]] && extract_first_plan_boundary "$message_text"; then
                    # when copilot emits PLAN_READY without PLAN_DRAFT (skipping
                    # user review for an existing plan), touch the plan file so
                    # its mtime updates and ralphex's FindRecent can find it —
                    # otherwise the "Continue with plan implementation?" prompt
                    # is skipped because FindRecent only returns files modified
                    # after the session start time.
                    if [[ "$plan_boundary_text" == *"<<<RALPHEX:PLAN_READY>>>"* ]] && \
                       [[ "$plan_boundary_text" != *"<<<RALPHEX:PLAN_DRAFT>>>"* ]]; then
                        existing_plan=$(printf '%s' "$plan_boundary_text" | grep -oE 'docs/plans/[^ ]*\.md' | head -1 || true)
                        if [[ -z "$existing_plan" || ! -f "$existing_plan" ]]; then
                            # path not in message — find the newest plan file
                            existing_plan=$(find docs/plans -maxdepth 1 -name '*.md' -type f -print0 2>/dev/null | xargs -0 ls -t 2>/dev/null | head -1 || true)
                        fi
                        if [[ -n "$existing_plan" && -f "$existing_plan" ]]; then
                            touch "$existing_plan"
                        fi
                    fi
                    emit_text_delta "$plan_boundary_text"
                    intentional_stop=1
                    if [[ -n "$copilot_pid" ]]; then
                        kill -TERM "$copilot_pid" 2>/dev/null || true
                    fi
                    break
                fi
                emit_text_delta "$message_text"
                turn_has_visible_message=1
            fi
            if [[ "$tool_request_count" =~ ^[0-9]+$ && "$tool_request_count" -gt 0 ]]; then
                turn_has_tool_requests=1
            fi
            ;;
        session.error|session.warning|session.info)
            event_text=$(printf '%s\n' "$line" | jq -r '.data.message // empty')
            [[ -n "$event_text" ]] && emit_text_delta "$event_text"$'\n'
            ;;
        assistant.turn_end)
            if [[ "$is_plan_prompt" != "1" && "$turn_has_visible_message" == "1" && "$turn_has_tool_requests" == "0" ]]; then
                intentional_stop=1
                if [[ -n "$copilot_pid" ]]; then
                    kill -TERM "$copilot_pid" 2>/dev/null || true
                fi
                break
            fi
            turn_has_visible_message=0
            turn_has_tool_requests=0
            ;;
        session.task_complete|session.idle|session.shutdown)
            ;;
    esac
done < "$stdout_pipe"

copilot_exit=0
wait "$copilot_pid" || copilot_exit=$?
copilot_pid=""

if [[ "$intentional_stop" == "1" ]]; then
    copilot_exit=0
fi

if [[ -s "$stderr_file" && "$intentional_stop" == "0" ]]; then
    while IFS= read -r err_line || [[ -n "$err_line" ]]; do
        [[ -z "$err_line" ]] && continue
        emit_text_delta "$err_line"$'\n'
    done < "$stderr_file"
fi

if [[ "$term_requested" == "0" ]]; then
    echo '{"type":"result","result":""}'
fi

exit "$copilot_exit"
