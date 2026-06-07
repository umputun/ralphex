#!/usr/bin/env bash
# skills_test.sh — validates pi SKILL.md frontmatter for required fields and the
# pi name pattern, plus a body-content check (no Claude-only tool tokens) for the
# skills that have been ported to pi. The body check is extended to cover more
# skills as each is ported.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SKILLS_DIR="$SCRIPT_DIR/skills"

passed=0
failed=0
total=0

pass() {
    passed=$((passed + 1))
    total=$((total + 1))
    echo "  PASS: $1"
}

fail() {
    failed=$((failed + 1))
    total=$((total + 1))
    echo "  FAIL: $1"
    if [[ -n "${2:-}" ]]; then
        echo "        $2"
    fi
}

# extract_frontmatter prints the lines between the first two `---` fences.
extract_frontmatter() {
    awk 'NR==1 && $0!="---"{exit 1} NR==1{next} /^---[[:space:]]*$/{exit} {print}' "$1"
}

# fm_value prints the value of a `key:` line in the given frontmatter text.
fm_value() {
    local fm="$1" key="$2"
    printf '%s\n' "$fm" | sed -n "s/^${key}:[[:space:]]*//p" | head -n1
}

expected_skills="ralphex ralphex-plan ralphex-update ralphex-adopt"

# ported_skills have had their bodies adapted for pi and must contain no
# Claude-only tool tokens. Extend this list as remaining skills are ported.
ported_skills="ralphex-plan ralphex-adopt"

# Claude-only tokens that must not appear in a ported pi skill body.
claude_tokens="AskUserQuestion TaskOutput subagent_type run_in_background ~/.claude/"

is_ported() {
    local needle="$1" item
    for item in $ported_skills; do
        [[ "$item" == "$needle" ]] && return 0
    done
    return 1
}

echo "running pi skills frontmatter tests"
echo ""

for name in $expected_skills; do
    file="$SKILLS_DIR/$name/SKILL.md"

    if [[ ! -f "$file" ]]; then
        fail "$name: SKILL.md exists" "missing $file"
        continue
    fi
    pass "$name: SKILL.md exists"

    if ! fm="$(extract_frontmatter "$file")"; then
        fail "$name: has YAML frontmatter" "file does not start with '---' fence"
        continue
    fi
    pass "$name: has YAML frontmatter"

    # required: name, matching pi pattern [a-z0-9-], <=64 chars
    name_val="$(fm_value "$fm" name)"
    if [[ -z "$name_val" ]]; then
        fail "$name: frontmatter has name" "name field missing or empty"
    elif [[ ! "$name_val" =~ ^[a-z0-9-]{1,64}$ ]]; then
        fail "$name: name matches pi pattern" "invalid name value: '$name_val'"
    elif [[ "$name_val" != "$name" ]]; then
        fail "$name: name matches directory" "name '$name_val' != dir '$name'"
    else
        pass "$name: name valid and matches directory"
    fi

    # required: description, non-empty, <=1024 chars
    desc_val="$(fm_value "$fm" description)"
    if [[ -z "$desc_val" ]]; then
        fail "$name: frontmatter has description" "description missing or empty"
    elif (( ${#desc_val} > 1024 )); then
        fail "$name: description within 1024 chars" "length ${#desc_val}"
    else
        pass "$name: description valid"
    fi

    # optional: allowed-tools must be pi space-delimited, not a Claude [..] array
    tools_val="$(fm_value "$fm" allowed-tools || true)"
    if [[ -n "$tools_val" ]]; then
        if [[ "$tools_val" == \[* ]]; then
            fail "$name: allowed-tools is space-delimited" "looks like a Claude array: '$tools_val'"
        else
            pass "$name: allowed-tools is space-delimited"
        fi
    fi

    # pi skills have no argument-hint frontmatter key
    if printf '%s\n' "$fm" | grep -q '^argument-hint:'; then
        fail "$name: no argument-hint key" "argument-hint is Claude-only"
    else
        pass "$name: no argument-hint key"
    fi

    # ported skills must not reference Claude-only tools anywhere in the file
    if is_ported "$name"; then
        found_tokens=""
        for token in $claude_tokens; do
            if grep -qF -- "$token" "$file"; then
                found_tokens="$found_tokens $token"
            fi
        done
        if [[ -n "$found_tokens" ]]; then
            fail "$name: no Claude-only tokens" "found:$found_tokens"
        else
            pass "$name: no Claude-only tokens"
        fi
    fi
done

echo ""
echo "summary: $passed passed, $failed failed, $total total"

if [[ $failed -ne 0 ]]; then
    exit 1
fi
