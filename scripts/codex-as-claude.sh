#!/usr/bin/env bash
# codex-as-claude.sh - wraps codex CLI to produce Claude-compatible stream-json output.
#
# this script translates codex JSONL events into the Claude stream-json format
# that ralphex's ClaudeExecutor can parse, allowing codex to be used as a drop-in
# replacement for claude in task and review phases.
#
# config example (~/.config/ralphex/config or .ralphex/config):
#   claude_command = /path/to/codex-as-claude.sh
#   claude_args =
#
# environment variables:
#   CODEX_MODEL   - codex model to use (default: codex default)
#   CODEX_SANDBOX - sandbox mode (default: danger-full-access)
#   CODEX_VERBOSE - set to 1 to include command execution output (default: 0)

set -euo pipefail

# verify jq is available (required for JSON translation)
command -v jq >/dev/null 2>&1 || { echo "error: jq is required but not found" >&2; exit 1; }

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
CODEX_MODEL="${CODEX_MODEL:-}"
CODEX_SANDBOX="${CODEX_SANDBOX:-danger-full-access}"

# build codex arguments
codex_args=(exec --json --dangerously-bypass-approvals-and-sandbox -s "$CODEX_SANDBOX")
[[ -n "$CODEX_MODEL" ]] && codex_args+=(-m "$CODEX_MODEL")
codex_args+=("$prompt")

# run codex with JSON output, translate events to claude stream-json format.
# only agent messages are emitted — command executions and file reads produce
# excessive noise (skill files, config reads, etc.) and are skipped.
# set CODEX_VERBOSE=1 to include command execution output.
#
# event mapping:
#   item.completed + agent_message     -> content_block_delta (text_delta)
#   item.completed + command_execution -> skipped (or included if CODEX_VERBOSE=1)
#   item.completed + reasoning         -> skipped
#   item.started                       -> skipped
#   turn.completed                     -> result (end of execution)
#   thread.started, turn.started       -> skipped
CODEX_VERBOSE="${CODEX_VERBOSE:-0}"
if [[ "$CODEX_VERBOSE" != "0" && "$CODEX_VERBOSE" != "1" ]]; then
    echo "warning: CODEX_VERBOSE must be 0 or 1, got '$CODEX_VERBOSE', defaulting to 0" >&2
    CODEX_VERBOSE=0
fi

codex "${codex_args[@]}" 2>/dev/null | while IFS= read -r line; do
    echo "$line" | jq -c --argjson verbose "$CODEX_VERBOSE" '
        if .type == "item.completed" then
            if .item.type == "agent_message" then
                {type: "content_block_delta", delta: {type: "text_delta", text: (.item.text + "\n")}}
            elif .item.type == "command_execution" and $verbose == 1 then
                {type: "content_block_delta", delta: {type: "text_delta",
                    text: ("$ " + .item.command + "\n" + (.item.aggregated_output // "") + "\n")}}
            else empty
            end
        elif .type == "turn.completed" then
            {type: "result", result: ""}
        else empty
        end
    ' 2>/dev/null || true
done

# emit fallback result event if codex exited without turn.completed
echo '{"type":"result","result":""}'
