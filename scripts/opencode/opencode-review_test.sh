#!/usr/bin/env bash
# opencode-review_test.sh — tests opencode-review.sh config building.
#
# run from the ralphex directory:
#   bash scripts/opencode/opencode-review_test.sh
#
# uses a fake opencode stub to capture the final command and env without
# actually invoking opencode.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="$SCRIPT_DIR/opencode-review.sh"
TMPDIR_BASE=$(mktemp -d)
trap 'rm -rf "$TMPDIR_BASE"' EXIT

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

# create a fake opencode that dumps its args and OPENCODE_CONFIG_CONTENT
stub_dir="$TMPDIR_BASE/bin"
mkdir -p "$stub_dir"
cat > "$stub_dir/opencode" << 'STUB'
#!/usr/bin/env bash
echo "ARGS:$*"
echo "CONFIG:$OPENCODE_CONFIG_CONTENT"
STUB
chmod +x "$stub_dir/opencode"

# create a test prompt file
prompt_file="$TMPDIR_BASE/prompt.txt"
echo "review this code" > "$prompt_file"

# helper to run the script with the stub opencode in PATH
run_script() {
    PATH="$stub_dir:$PATH" "$SCRIPT" "$@" 2>&1
}

echo "running opencode-review.sh tests"
echo ""

# ---------------------------------------------------------------------------
# test: no arguments
# ---------------------------------------------------------------------------
echo "test: no arguments"
output=$(run_script 2>&1) || true
if echo "$output" | grep -q "prompt file not provided"; then
    pass "exits with prompt file error"
else
    fail "expected prompt file error" "got: $output"
fi

# ---------------------------------------------------------------------------
# test: missing prompt file
# ---------------------------------------------------------------------------
echo "test: missing prompt file"
output=$(run_script /tmp/nonexistent-file-xyz 2>&1) || true
if echo "$output" | grep -q "prompt file not provided or not found"; then
    pass "exits with file not found error"
else
    fail "expected file not found error" "got: $output"
fi

# ---------------------------------------------------------------------------
# test: both env vars empty
# ---------------------------------------------------------------------------
echo "test: both env vars empty"
output=$(OPENCODE_REVIEW_MODEL="" OPENCODE_REVIEW_REASONING="" run_script "$prompt_file")
config=$(echo "$output" | grep "^CONFIG:" | sed 's/^CONFIG://')
args=$(echo "$output" | grep "^ARGS:" | sed 's/^ARGS://')

if echo "$config" | jq -e '.permission["*"] == "allow"' >/dev/null 2>&1; then
    pass "config has permissions"
else
    fail "config missing permissions" "got: $config"
fi

if echo "$config" | jq -e '.agent' >/dev/null 2>&1; then
    fail "config should not have agent block when vars empty" "got: $config"
else
    pass "config has no agent block"
fi

if echo "$args" | grep -q "\-\-model"; then
    fail "should not pass --model flag when empty" "got: $args"
else
    pass "no --model flag"
fi

# ---------------------------------------------------------------------------
# test: model only
# ---------------------------------------------------------------------------
echo "test: model only"
output=$(OPENCODE_REVIEW_MODEL="test-model" OPENCODE_REVIEW_REASONING="" run_script "$prompt_file")
config=$(echo "$output" | grep "^CONFIG:" | sed 's/^CONFIG://')
args=$(echo "$output" | grep "^ARGS:" | sed 's/^ARGS://')

if echo "$config" | jq -e '.agent' >/dev/null 2>&1; then
    fail "config should not have agent block" "got: $config"
else
    pass "config has no agent block"
fi

if echo "$args" | grep -q "\-\-variant"; then
    fail "should not pass --variant flag" "got: $args"
else
    pass "no --variant flag"
fi

if echo "$args" | grep -q "\-\-model test-model"; then
    pass "--model flag passed"
else
    fail "expected --model test-model in args" "got: $args"
fi

# ---------------------------------------------------------------------------
# test: reasoning only
# ---------------------------------------------------------------------------
echo "test: reasoning only"
output=$(OPENCODE_REVIEW_MODEL="" OPENCODE_REVIEW_REASONING="high" run_script "$prompt_file")
config=$(echo "$output" | grep "^CONFIG:" | sed 's/^CONFIG://')
args=$(echo "$output" | grep "^ARGS:" | sed 's/^ARGS://')

if echo "$config" | jq -e '.agent' >/dev/null 2>&1; then
    fail "config should not have agent block" "got: $config"
else
    pass "config has no agent block"
fi

if echo "$args" | grep -q "\-\-model"; then
    fail "should not pass --model flag" "got: $args"
else
    pass "no --model flag"
fi

if echo "$args" | grep -q "\-\-variant high"; then
    pass "--variant flag passed"
else
    fail "expected --variant high in args" "got: $args"
fi

# ---------------------------------------------------------------------------
# test: both set
# ---------------------------------------------------------------------------
echo "test: both set"
output=$(OPENCODE_REVIEW_MODEL="my-model" OPENCODE_REVIEW_REASONING="medium" run_script "$prompt_file")
config=$(echo "$output" | grep "^CONFIG:" | sed 's/^CONFIG://')
args=$(echo "$output" | grep "^ARGS:" | sed 's/^ARGS://')

if echo "$config" | jq -e '.agent' >/dev/null 2>&1; then
    fail "config should not have agent block" "got: $config"
else
    pass "config has no agent block"
fi

if echo "$args" | grep -q "\-\-model my-model"; then
    pass "--model flag passed"
else
    fail "expected --model my-model in args" "got: $args"
fi

if echo "$args" | grep -q "\-\-variant medium"; then
    pass "--variant flag passed"
else
    fail "expected --variant medium in args" "got: $args"
fi

# ---------------------------------------------------------------------------
# test: CLI --model/--effort override env vars
# ---------------------------------------------------------------------------
echo "test: CLI --model/--effort override env vars"
output=$(OPENCODE_REVIEW_MODEL="env-model" OPENCODE_REVIEW_VARIANT="env-variant" \
    run_script --model cli-model --effort high "$prompt_file")
args=$(echo "$output" | grep "^ARGS:" | sed 's/^ARGS://')

if echo "$args" | grep -q "\-\-model cli-model" && echo "$args" | grep -q "\-\-variant high"; then
    pass "CLI --model/--effort override env defaults"
else
    fail "CLI --model/--effort not passed correctly" "got: $args"
fi

# ---------------------------------------------------------------------------
# test: missing CLI flag values are rejected
# ---------------------------------------------------------------------------
echo "test: missing CLI flag values are rejected"
output=$(run_script --model --effort high "$prompt_file" 2>&1) && status=0 || status=$?
if [[ $status -ne 0 ]] && echo "$output" | grep -q -- "--model requires a non-empty value"; then
    pass "missing --model value is rejected"
else
    fail "missing --model value should fail" "status: $status output: $output"
fi

output=$(run_script --variant= "$prompt_file" 2>&1) && status=0 || status=$?
if [[ $status -ne 0 ]] && echo "$output" | grep -q -- "--variant requires a non-empty value"; then
    pass "empty --variant value is rejected"
else
    fail "empty --variant value should fail" "status: $status output: $output"
fi

# ---------------------------------------------------------------------------
# test: merge with existing OPENCODE_CONFIG_CONTENT
# ---------------------------------------------------------------------------
echo "test: merge with existing OPENCODE_CONFIG_CONTENT"
output=$(OPENCODE_CONFIG_CONTENT='{"theme":"dark","custom":true}' \
    OPENCODE_REVIEW_MODEL="merge-model" OPENCODE_REVIEW_REASONING="" \
    run_script "$prompt_file")
config=$(echo "$output" | grep "^CONFIG:" | sed 's/^CONFIG://')
args=$(echo "$output" | grep "^ARGS:" | sed 's/^ARGS://')

if echo "$config" | jq -e '.theme == "dark"' >/dev/null 2>&1; then
    pass "preserves existing theme field"
else
    fail "lost existing theme field" "got: $config"
fi

if echo "$config" | jq -e '.custom == true' >/dev/null 2>&1; then
    pass "preserves existing custom field"
else
    fail "lost existing custom field" "got: $config"
fi

if echo "$config" | jq -e '.permission["*"] == "allow"' >/dev/null 2>&1; then
    pass "merged permissions"
else
    fail "missing permissions after merge" "got: $config"
fi

if echo "$args" | grep -q "\-\-model merge-model"; then
    pass "merged model passed as CLI flag"
else
    fail "missing model CLI flag after merge" "got: $args"
fi

# ---------------------------------------------------------------------------
# test: prompt content passed to opencode
# ---------------------------------------------------------------------------
echo "test: prompt content passed to opencode"
output=$(OPENCODE_REVIEW_MODEL="" OPENCODE_REVIEW_REASONING="" run_script "$prompt_file")
args=$(echo "$output" | grep "^ARGS:" | sed 's/^ARGS://')

if echo "$args" | grep -q "review this code"; then
    pass "prompt content passed as argument"
else
    fail "prompt content not found in args" "got: $args"
fi

# ---------------------------------------------------------------------------
# summary
# ---------------------------------------------------------------------------
echo ""
echo "results: $passed passed, $failed failed, $total total"

if [[ $failed -gt 0 ]]; then
    exit 1
fi
