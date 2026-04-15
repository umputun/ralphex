#!/usr/bin/env bash
# copilot-as-claude.sh - wraps GitHub Copilot CLI to produce Claude-compatible stream-json output.
#
# this script translates Copilot CLI JSONL events into the Claude stream-json format
# that ralphex's ClaudeExecutor can parse, allowing copilot to be used as a drop-in
# replacement for claude in task, review, and plan phases.
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

progress_file_from_prompt() {
    local line=""
    local path=""
    local latest=""
    local files=()

    while IFS= read -r line; do
        case "$line" in
            "Progress log: "*)
                path=${line#Progress log: }
                path=${path%% (contains*}
                ;;
            "Read session state from "*)
                path=${line#Read session state from }
                path=${path%% before deciding what to do next.*}
                path=${path%% before deciding what to do next}
                ;;
        esac
        if [[ -n "$path" ]]; then
            if [[ "$path" != */* && -f ".ralphex/progress/$path" ]]; then
                printf '%s' ".ralphex/progress/$path"
                return 0
            fi
            printf '%s' "$path"
            return 0
        fi
    done <<< "$prompt"

    shopt -s nullglob
    files=(.ralphex/progress/progress-plan*.txt)
    shopt -u nullglob
    if [[ ${#files[@]} -gt 0 ]]; then
        latest=$(ls -t "${files[@]}" 2>/dev/null | head -1 || true)
        if [[ -n "$latest" ]]; then
            printf '%s' "$latest"
            return 0
        fi
    fi

    return 1
}

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

is_task_prompt=0
if [[ "$prompt" == *"<<<RALPHEX:ALL_TASKS_DONE>>>"* ]]; then
    is_task_prompt=1
fi

plan_progress_file=""
if [[ "$is_plan_prompt" == "1" ]]; then
    plan_progress_file=$(progress_file_from_prompt 2>/dev/null || true)
fi

task_plan_file=""
if [[ "$is_task_prompt" == "1" ]]; then
    task_plan_file=$(printf '%s\n' "$prompt" | grep -o 'Read the plan file at [^[:space:]]*' | head -1 | sed 's/^Read the plan file at //; s/\.$//' || true)
fi

# copilot defaults to markdown output (## headers, **bold**, ```fences```) which
# renders as literal characters in ralphex progress logs.  the formatting rule
# suppresses this; without it every review run has noisy markup in the log.
if [[ "$is_review_prompt" == "1" ]]; then
    adapter_text=$'FORMATTING RULE (strict): All output must be plain text only — no markdown of any kind (no headers, bold, code spans, code fences, horizontal rules). Use plain indented lists for structure. This renders in a terminal; markdown appears as literal characters.\n\nRalphex review adapter for GitHub Copilot CLI:\n- Review prompts refer to Claude "Task tool" calls — interpret those as agent delegation instructions.\n- Delegate all requested review roles as separate sub-agents; if parallel delegation is unavailable, run them sequentially — drop none.\n- Each agent should inspect the diff and source files directly and report problems only.\n- After all reviews, verify findings, fix confirmed issues, rerun tests and lint, and preserve all <<<RALPHEX:...>>> signals verbatim.'
    prompt="$adapter_text"$'\n\n'"$prompt"
fi

if [[ "$is_plan_prompt" == "1" ]]; then
    plan_adapter_text=$'FORMATTING RULE (strict): Your analysis and thinking text — everything you write before emitting <<<RALPHEX:QUESTION>>> or <<<RALPHEX:PLAN_DRAFT>>> — must be plain text only. No backticks around code names or identifiers, no # or ## headers, no **bold** or *italic*, no markdown of any kind. Refer to code names as plain words (e.g. myFunction() not `myFunction()`). The plan document body inside <<<RALPHEX:PLAN_DRAFT>>>...<<<RALPHEX:END>>> must use the plan file markdown format exactly as specified in the prompt. Preserve all <<<RALPHEX:...>>> signals verbatim.\n\nQUESTION RULE (strict): Do NOT use Copilot\'s native ask_user tool or any other interactive question mechanism. When clarification is needed, emit the <<<RALPHEX:QUESTION>>> block exactly and stop. If any implementation-blocking uncertainty remains, emit QUESTION instead of PLAN_DRAFT. Do not carry unresolved blockers into an "Open Questions" or "Assumptions" section of the draft.\n\nPLAN REVIEW RULE (overrides other instructions): Present a <<<RALPHEX:PLAN_DRAFT>>>...<<<RALPHEX:END>>> block for user review before any new plan is accepted. Once the progress file shows DRAFT REVIEW: accept for the current draft, do NOT emit another PLAN_DRAFT unless the user later requested revisions. Instead, write the accepted plan file and emit PLAN_READY. If you find an existing plan file matching the request, do NOT modify it. Output the exact plan path on the line immediately before PLAN_READY and stop. Only emit PLAN_READY after writing the accepted new plan file on disk following user acceptance.'
    prompt="$plan_adapter_text"$'\n\n'"$prompt"
fi

# Copilot CLI mode flags are not enough to replace boundary extraction below. Even
# with autopilot continuation limits, Copilot can still place QUESTION / PLAN_DRAFT /
# PLAN_READY and extra trailing text in the same assistant.message, so the wrapper
# must truncate at the first plan boundary and hand control back to ralphex.
copilot_args=(-s --output-format json --stream on --allow-all)
if [[ "$is_plan_prompt" == "1" ]]; then
    # plan creation still benefits from autopilot's multi-step exploration, but it
    # must remain free to surface clarifications via ralphex QUESTION signals.
    # native Copilot plan mode tended to re-draft after acceptance instead of
    # writing the accepted plan and emitting PLAN_READY.
    copilot_args+=(--autopilot)
else
    # task and review phases run unattended across multiple model turns.
    copilot_args+=(--autopilot --no-ask-user)
fi
[[ -n "$COPILOT_MODEL" ]] && copilot_args+=(--model "$COPILOT_MODEL")

tmp_dir=$(mktemp -d)
stderr_file=$(mktemp)
prompt_file=$(mktemp)
stdout_pipe="$tmp_dir/stdout.fifo"
plan_write_marker="$tmp_dir/plan-write.marker"
mkfifo "$stdout_pipe"
printf '%s' "$prompt" > "$prompt_file"
touch "$plan_write_marker"

cleanup() {
    rm -f "$stderr_file" "$prompt_file" "$stdout_pipe" "$plan_write_marker"
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
    if [[ "$text" == *"<<<RALPHEX:ALL_TASKS_DONE>>>"* || "$text" == *"<<<RALPHEX:TASK_FAILED>>>"* ]]; then
        task_signal_emitted=1
    fi
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

emit_keepalive() {
    printf '%s\n' '{"type":"content_block_delta","delta":{"type":"text_delta","text":""}}'
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

parse_copilot_event() {
    local line="$1"

    event_type=""
    message_text=""
    event_text=""

    {
        IFS= read -r -d '' event_type &&
            IFS= read -r -d '' message_text &&
            IFS= read -r -d '' event_text
    } < <(
        printf '%s\n' "$line" | jq -j '
            def strip_edge_tabs:
                split("\n")
                | map(gsub("^\\t+"; "") | gsub("\\t+$"; ""))
                | join("\n");

            (.type // ""), "\u0000",
            (.data.content // "" | strip_edge_tabs), "\u0000",
            (.data.message // ""), "\u0000"
        ' 2>/dev/null
    )
}

last_draft_review_action() {
    local progress_file="$1"
    local last_action=""
    local line

    [[ -n "$progress_file" && -f "$progress_file" ]] || return 1

    while IFS= read -r line; do
        if [[ "$line" == *"DRAFT REVIEW: "* ]]; then
            last_action=${line##*DRAFT REVIEW: }
        fi
    done < "$progress_file"

    [[ -n "$last_action" ]] || return 1
    printf '%s' "$last_action"
}

plan_written_since_start() {
    local progress_file="$1"
    local draft_text="$2"
    local attempt=""
    local candidate=""
    local candidate_text=""
    local draft_file=""
    local candidate_file=""
    local matches=0
    local matched_path=""

    [[ -n "$progress_file" && -f "$progress_file" ]] || return 1
    [[ -n "$draft_text" ]] || return 1

    draft_file=$(mktemp "$tmp_dir/draft.XXXXXX")
    normalize_plan_text "$draft_text" > "$draft_file"
    [[ -s "$draft_file" ]] || return 1

    for attempt in 1 2 3 4 5; do
        matches=0
        matched_path=""
        while IFS= read -r -d '' candidate; do
            [[ -f "$candidate" ]] || continue
            candidate_text=$(cat "$candidate" 2>/dev/null || true)
            candidate_file=$(mktemp "$tmp_dir/candidate.XXXXXX")
            normalize_plan_text "$candidate_text" > "$candidate_file"
            if cmp -s "$draft_file" "$candidate_file"; then
                matches=$((matches + 1))
                matched_path="$candidate"
                if [[ $matches -gt 1 ]]; then
                    break
                fi
            fi
        done < <(
            find . \
                \( -path './.git' -o -path './.ralphex' \) -prune -o \
                -name '*.md' -type f -newer "$plan_write_marker" -print0 2>/dev/null
        )

        if [[ $matches -eq 1 ]]; then
            printf '%s' "${matched_path#./}"
            return 0
        fi
        if [[ $matches -gt 1 ]]; then
            return 1
        fi
        sleep 0.1
    done

    return 1
}

native_plan_question_text=""
native_plan_question_error=""
extract_native_plan_question() {
    local line="$1"
    local detected=""
    local question=""
    local options_json=""
    local payload=""

    native_plan_question_text=""
    native_plan_question_error=""

    {
        IFS= read -r -d '' detected &&
            IFS= read -r -d '' question &&
            IFS= read -r -d '' options_json
    } < <(
        printf '%s\n' "$line" | jq -j '
            def ask_user_match:
                ((.toolName // .name // .tool // "") | ascii_downcase | test("ask[_-]?user|user[._-]?question|^question$"));

            def ask_user_req:
                [ .data.toolRequests[]? | select(ask_user_match) ][0];

            def raw_question($req):
                (
                    ($req | .args.question? // .args.prompt? // .arguments.question? // .arguments.prompt? // .input.question? // .input.prompt? // .question? // .prompt? // .message?) //
                    .data.question? // .data.prompt? // .data.message? //
                    .question? // .prompt? // .message? // ""
                );

            def raw_options($req):
                (
                    ($req | .args.options? // .args.choices? // .arguments.options? // .arguments.choices? // .input.options? // .input.choices? // .options? // .choices?) //
                    .data.options? // .data.choices? //
                    .options? // .choices? // []
                );

            def option_strings:
                if type == "array" then
                    map(
                        if type == "null" then empty
                        elif type == "string" then .
                        elif type == "object" then (.label // .title // .name // .text // .value // empty | tostring)
                        else tostring end
                    )
                    | map(gsub("^\\s+|\\s+$"; ""))
                    | map(select(length > 0))
                else
                    []
                end;

            (ask_user_req) as $req |
            ((((.type // "") | ascii_downcase | test("ask[_-]?user|user[._-]?question")) or (($req | type) == "object"))) as $detected |
            if $detected then
                "1", "\u0000",
                ((raw_question($req) | if type == "string" then gsub("^\\s+|\\s+$"; "") else "" end)), "\u0000",
                ((raw_options($req) | option_strings | @json)), "\u0000"
            else
                "0", "\u0000", "", "\u0000", "[]", "\u0000"
            end
        ' 2>/dev/null
    )

    [[ "$detected" == "1" ]] || return 1

    if [[ -z "$question" ]]; then
        native_plan_question_error="error: Copilot emitted a native ask_user request without a parseable question; emit <<<RALPHEX:QUESTION>>> JSON instead"
        return 2
    fi
    if [[ -z "$options_json" || "$options_json" == "[]" ]]; then
        native_plan_question_error="error: Copilot emitted a native ask_user request without concrete options; emit <<<RALPHEX:QUESTION>>> with 2-4 options instead"
        return 2
    fi

    payload=$(jq -cn --arg question "$question" --argjson options "$options_json" \
        '{question: $question, options: $options}')
    native_plan_question_text=$'<<<RALPHEX:QUESTION>>>\n'"$payload"$'\n<<<RALPHEX:END>>>'
    return 0
}

last_plan_draft_from_progress_file() {
    local progress_file="$1"

    [[ -n "$progress_file" && -f "$progress_file" ]] || return 1

    awk '
        function strip_prefix(line) {
            sub(/^\[[0-9]{2}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2}\] /, "", line)
            return line
        }
        {
            line = strip_prefix($0)
            if (line == "<<<RALPHEX:PLAN_DRAFT>>>") {
                in_block = 1
                block = ""
                next
            }
            if (in_block && line == "<<<RALPHEX:END>>>") {
                last_block = block
                in_block = 0
                next
            }
            if (in_block) {
                block = block (block == "" ? "" : ORS) line
            }
        }
        END {
            if (last_block != "") {
                printf "%s", last_block
            }
        }
    ' "$progress_file"
}

normalize_plan_text() {
    local text="$1"

    printf '%s' "$text" | tr -d '\r' | awk '
        { lines[NR] = $0 }
        END {
            start = 1
            end = NR
            while (start <= end && lines[start] ~ /^[[:space:]]*$/) { start++ }
            while (end >= start && lines[end] ~ /^[[:space:]]*$/) { end-- }
            if (start <= end && lines[start] ~ /^---[[:space:]]*$/) { start++ }
            if (end >= start && lines[end] ~ /^---[[:space:]]*$/) { end-- }
            while (start <= end && lines[start] ~ /^[[:space:]]*$/) { start++ }
            while (end >= start && lines[end] ~ /^[[:space:]]*$/) { end-- }
            for (i = start; i <= end; i++) {
                printf "%s%s", lines[i], (i < end ? ORS : "")
            }
        }
    '
}

accepted_draft_ready_fallback_path() {
    local last_action=""
    local draft_text=""

    # Copilot sometimes writes the accepted plan file and exits cleanly without
    # emitting PLAN_READY. Use the accepted PLAN_DRAFT already recorded in the
    # progress file to identify the newly written markdown file, instead of
    # reconstructing the expected path from prompt wording or model narration.
    [[ "$is_plan_prompt" == "1" ]] || return 1
    [[ "$intentional_stop" == "0" ]] || return 1
    [[ "$copilot_exit" == "0" ]] || return 1
    [[ -n "$plan_progress_file" ]] || return 1

    last_action=$(last_draft_review_action "$plan_progress_file" 2>/dev/null || true)
    draft_text=$(last_plan_draft_from_progress_file "$plan_progress_file" 2>/dev/null || true)

    [[ "$last_action" == "accept" ]] || return 1
    plan_written_since_start "$plan_progress_file" "$draft_text"
}

task_complete_fallback() {
    # Copilot sometimes reads a fully-checked plan, narrates "nothing left to do",
    # and exits cleanly without emitting ALL_TASKS_DONE. When the plan file has
    # no remaining unchecked actionable task checkboxes, synthesize the signal.
    [[ "$is_task_prompt" == "1" ]] || return 1
    [[ "$intentional_stop" == "0" ]] || return 1
    [[ "$copilot_exit" == "0" ]] || return 1
    [[ "${task_signal_emitted:-0}" == "0" ]] || return 1
    [[ -n "$task_plan_file" && -f "$task_plan_file" ]] || return 1

    # check for unchecked actionable checkboxes inside Task/Iteration sections.
    # mirrors Go's hasUncompletedTasks() logic:
    #   - only look at lines inside "### Task N:" or "### Iteration N:" sections
    #   - skip checkboxes whose text contains [ ] or [x] (format descriptions)
    local in_task=0
    local has_tasks=0
    local saw_title=0
    while IFS= read -r line || [[ -n "$line" ]]; do
        if [[ "$line" =~ ^#[[:space:]] && "$saw_title" == "0" ]]; then
            saw_title=1
            continue
        fi
        # detect task section headers
        if [[ "$line" =~ ^###[[:space:]]+(Task|Iteration)[[:space:]] ]]; then
            in_task=1
            has_tasks=1
            continue
        fi
        # mirror pkg/plan.ParsePlan: only h2 or h1-after-title close the task section.
        if [[ "$line" =~ ^##[[:space:]] && ! "$line" =~ ^###[[:space:]] ]]; then
            in_task=0
            continue
        fi
        if [[ "$saw_title" == "1" && "$line" =~ ^#[[:space:]] ]]; then
            in_task=0
            continue
        fi
        [[ "$in_task" == "1" ]] || continue
        # match unchecked checkbox: "- [ ] text"
        if [[ "$line" =~ ^[[:space:]]*-[[:space:]]+\[\ \][[:space:]]*(.*) ]]; then
            local cb_text="${BASH_REMATCH[1]}"
            # skip format-description checkboxes (text contains [ ] or [x])
            if [[ "$cb_text" =~ \[[[:space:]]*[xX\ ]?[[:space:]]*\] ]]; then
                continue
            fi
            return 1  # found actionable unchecked checkbox
        fi
    done < "$task_plan_file"

    # fallback for malformed plans with no task headers: check entire file
    if [[ "$has_tasks" == "0" ]]; then
        while IFS= read -r line || [[ -n "$line" ]]; do
            if [[ "$line" =~ ^[[:space:]]*-[[:space:]]+\[\ \][[:space:]]*(.*) ]]; then
                local cb_text="${BASH_REMATCH[1]}"
                if [[ "$cb_text" =~ \[[[:space:]]*[xX\ ]?[[:space:]]*\] ]]; then
                    continue
                fi
                return 1
            fi
        done < "$task_plan_file"
    fi

    return 0  # no remaining tasks
}

intentional_stop=0
first_output_emitted=0
forced_exit_code=""
task_signal_emitted=0

while IFS= read -r line || [[ -n "$line" ]]; do
    [[ -z "$line" ]] && continue

    if ! parse_copilot_event "$line"; then
        emit_text_delta "$line"$'\n'
        continue
    fi

    case "$event_type" in
        assistant.message_delta)
            emit_keepalive
            continue
            ;;
        assistant.message)
            if [[ -n "$message_text" ]]; then
                if [[ "$is_plan_prompt" == "1" ]] && extract_first_plan_boundary "$message_text"; then
                    emit_text_delta "$plan_boundary_text"
                    intentional_stop=1
                    if [[ -n "$copilot_pid" ]]; then
                        kill -TERM "$copilot_pid" 2>/dev/null || true
                    fi
                    break
                fi
            fi
            if [[ "$is_plan_prompt" == "1" ]]; then
                if extract_native_plan_question "$line"; then
                    if [[ -n "$message_text" ]]; then
                        emit_text_delta "$message_text"$'\n'"$native_plan_question_text"
                    else
                        emit_text_delta "$native_plan_question_text"
                    fi
                    intentional_stop=1
                    if [[ -n "$copilot_pid" ]]; then
                        kill -TERM "$copilot_pid" 2>/dev/null || true
                    fi
                    break
                else
                    native_question_status=$?
                    if [[ $native_question_status -eq 2 ]]; then
                        emit_text_delta "$native_plan_question_error"$'\n'
                        forced_exit_code=1
                        if [[ -n "$copilot_pid" ]]; then
                            kill -TERM "$copilot_pid" 2>/dev/null || true
                        fi
                        break
                    fi
                fi
            fi
            if [[ -n "$message_text" ]]; then
                emit_text_delta "$message_text"
            fi
            ;;
        ask_user|assistant.ask_user|session.ask_user|user.question)
            if [[ "$is_plan_prompt" == "1" ]]; then
                if extract_native_plan_question "$line"; then
                    emit_text_delta "$native_plan_question_text"
                    intentional_stop=1
                    if [[ -n "$copilot_pid" ]]; then
                        kill -TERM "$copilot_pid" 2>/dev/null || true
                    fi
                    break
                else
                    native_question_status=$?
                    if [[ $native_question_status -eq 2 ]]; then
                        emit_text_delta "$native_plan_question_error"$'\n'
                        forced_exit_code=1
                        if [[ -n "$copilot_pid" ]]; then
                            kill -TERM "$copilot_pid" 2>/dev/null || true
                        fi
                        break
                    fi
                fi
            fi
            ;;
        session.error|session.warning|session.info)
            [[ -n "$event_text" ]] && emit_text_delta "$event_text"$'\n'
            ;;
        assistant.turn_end)
            # keep reading across turn boundaries for task/review runs. Copilot may
            # finish its inspection in one turn and only emit the terminal
            # ALL_TASKS_DONE / TASK_FAILED signal in a later autonomous turn.
            emit_keepalive
            ;;
        session.task_complete|session.idle|session.shutdown)
            emit_keepalive
            ;;
        *)
            emit_keepalive
            ;;
    esac
done < "$stdout_pipe"

copilot_exit=0
wait "$copilot_pid" || copilot_exit=$?
copilot_pid=""

if [[ "$intentional_stop" == "1" ]]; then
    copilot_exit=0
fi
if [[ -n "$forced_exit_code" ]]; then
    copilot_exit=$forced_exit_code
fi

if [[ -s "$stderr_file" && "$intentional_stop" == "0" ]]; then
    while IFS= read -r err_line || [[ -n "$err_line" ]]; do
        [[ -z "$err_line" ]] && continue
        emit_text_delta "$err_line"$'\n'
    done < "$stderr_file"
fi

if fallback_path=$(accepted_draft_ready_fallback_path 2>/dev/null); then
    emit_text_delta "$fallback_path"$'\n<<<RALPHEX:PLAN_READY>>>\n'
fi

if task_complete_fallback 2>/dev/null; then
    emit_text_delta $'\n<<<RALPHEX:ALL_TASKS_DONE>>>\n'
fi

if [[ "$term_requested" == "0" ]]; then
    echo '{"type":"result","result":""}'
fi

exit "$copilot_exit"
