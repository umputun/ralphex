#!/usr/bin/env bash
# opencode-review.sh - custom review script for ralphex external review phase.
#
# uses OpenCode CLI to perform code review with a configurable model,
# allowing a different model than the one used for task/review phases.
#
# config example (~/.config/ralphex/config or .ralphex/config):
#   external_review_tool = custom
#   custom_review_script = /path/to/opencode-review.sh
#
# environment variables:
# e.g. OPENCODE_REVIEW_MODEL="github-copilot/gpt-5.3-codex"
OPENCODE_REVIEW_MODEL="${OPENCODE_REVIEW_MODEL:-}"
# e.g. OPENCODE_REVIEW_VARIANT="high" or OPENCODE_REVIEW_REASONING="high"
OPENCODE_REVIEW_VARIANT="${OPENCODE_REVIEW_VARIANT:-${OPENCODE_REVIEW_EFFORT:-${OPENCODE_REVIEW_REASONING:-}}}"

set -euo pipefail

# verify opencode is available
command -v opencode >/dev/null 2>&1 || { echo "error: opencode is required but not found" >&2; exit 1; }

# verify jq is available (required for JSON config merging)
command -v jq >/dev/null 2>&1 || { echo "error: jq is required but not found" >&2; exit 1; }

# prompt file path is passed as the single argument. Optional --model/--effort
# flags override env defaults for direct invocations and future ralphex plumbing.
prompt_file=""
while [[ $# -gt 0 ]]; do
    case "$1" in
        --model) OPENCODE_REVIEW_MODEL="${2:-}"; shift; shift 2>/dev/null || true ;;
        --model=*) OPENCODE_REVIEW_MODEL="${1#--model=}"; shift ;;
        --effort) OPENCODE_REVIEW_VARIANT="${2:-}"; shift; shift 2>/dev/null || true ;;
        --effort=*) OPENCODE_REVIEW_VARIANT="${1#--effort=}"; shift ;;
        --variant) OPENCODE_REVIEW_VARIANT="${2:-}"; shift; shift 2>/dev/null || true ;;
        --variant=*) OPENCODE_REVIEW_VARIANT="${1#--variant=}"; shift ;;
        *) prompt_file="$1"; shift ;;
    esac
done
if [[ -z "$prompt_file" || ! -f "$prompt_file" ]]; then
    echo "error: prompt file not provided or not found" >&2
    exit 1
fi

prompt=$(cat "$prompt_file")

# build final config with permissions. Model and reasoning are passed through
# opencode's CLI flags because --variant is the supported one-shot effort selector.
base_config='{"permission":{"*":"allow"}}'

# merge with existing OPENCODE_CONFIG_CONTENT if set
if [[ -n "${OPENCODE_CONFIG_CONTENT:-}" ]]; then
    OPENCODE_CONFIG_CONTENT=$(echo "$OPENCODE_CONFIG_CONTENT" | jq -c --argjson base "$base_config" '. * $base')
else
    OPENCODE_CONFIG_CONTENT="$base_config"
fi
export OPENCODE_CONFIG_CONTENT

cmd=(opencode run)
if [[ -n "$OPENCODE_REVIEW_MODEL" ]]; then
    cmd+=(--model "$OPENCODE_REVIEW_MODEL")
fi
if [[ -n "$OPENCODE_REVIEW_VARIANT" ]]; then
    cmd+=(--variant "$OPENCODE_REVIEW_VARIANT")
fi
cmd+=("$prompt")
"${cmd[@]}"
