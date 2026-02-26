#!/usr/bin/env bash
# hg2git_test.sh — tests hg2git.sh against the parent hg repo.
#
# run from the ralphex directory:
#   bash scripts/hg2git_test.sh
#
# requires: an hg repo accessible from parent directories

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="$SCRIPT_DIR/hg2git.sh"

# find an hg repo in parent directories
HG_REPO=""
dir="$(pwd)"
while [[ "$dir" != "/" ]]; do
    if [[ -d "$dir/.hg" ]]; then
        HG_REPO="$dir"
        break
    fi
    dir="$(dirname "$dir")"
done

if [[ -z "$HG_REPO" ]]; then
    echo "SKIP: no hg repo found, skipping live tests"
    exit 0
fi

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

echo "running hg2git.sh tests against $HG_REPO"
echo ""

# ---------------------------------------------------------------------------
# test rev-parse --show-toplevel
# ---------------------------------------------------------------------------
echo "test: rev-parse --show-toplevel"
toplevel=$(cd "$HG_REPO" && "$SCRIPT" rev-parse --show-toplevel 2>&1) || true
if [[ -n "$toplevel" && -d "$toplevel" ]]; then
    pass "returns non-empty path: $toplevel"
else
    fail "returned empty or invalid path" "got: $toplevel"
fi

# ---------------------------------------------------------------------------
# test rev-parse HEAD
# ---------------------------------------------------------------------------
echo "test: rev-parse HEAD"
head_hash=$(cd "$HG_REPO" && "$SCRIPT" rev-parse HEAD 2>&1) || true
if [[ "$head_hash" =~ ^[0-9a-f]{40}$ ]]; then
    pass "returns valid 40-char hex hash"
else
    fail "expected 40-char hex hash" "got: $head_hash"
fi

# ---------------------------------------------------------------------------
# test symbolic-ref --short HEAD
# ---------------------------------------------------------------------------
echo "test: symbolic-ref --short HEAD"
branch=$(cd "$HG_REPO" && "$SCRIPT" symbolic-ref --short HEAD 2>&1) || true
phase=$(cd "$HG_REPO" && hg log -r . --template '{phase}' 2>/dev/null) || true
if [[ "$phase" == "draft" ]]; then
    # on draft phase, should NOT return "master"
    if [[ "$branch" != "master" && -n "$branch" ]]; then
        pass "draft phase returns non-master value: $branch"
    else
        fail "draft phase should not return master" "got: $branch"
    fi
elif [[ "$phase" == "public" ]]; then
    if [[ "$branch" == "master" ]]; then
        pass "public phase returns master"
    else
        fail "public phase should return master" "got: $branch"
    fi
else
    fail "unexpected phase" "got: $phase"
fi

# ---------------------------------------------------------------------------
# test symbolic-ref refs/remotes/origin/HEAD (should fail)
# ---------------------------------------------------------------------------
echo "test: symbolic-ref refs/remotes/origin/HEAD"
if cd "$HG_REPO" && "$SCRIPT" symbolic-ref refs/remotes/origin/HEAD 2>/dev/null; then
    fail "should exit non-zero for remote refs"
else
    pass "exits non-zero (no remote refs in hg)"
fi

# ---------------------------------------------------------------------------
# test status --porcelain format
# ---------------------------------------------------------------------------
echo "test: status --porcelain"
status_output=$(cd "$HG_REPO" && "$SCRIPT" status --porcelain 2>&1) || true
if [[ -n "$status_output" ]]; then
    # check that each line matches 2-char XY format: "XY path"
    bad_lines=0
    while IFS= read -r line; do
        [[ -z "$line" ]] && continue
        if [[ ! "$line" =~ ^.{2}\ .+ ]]; then
            bad_lines=$((bad_lines + 1))
            echo "        bad line: $line"
        fi
    done <<< "$status_output"
    if [[ $bad_lines -eq 0 ]]; then
        pass "all lines match 2-char XY format"
    else
        fail "$bad_lines lines don't match expected format"
    fi
else
    pass "empty status output (clean worktree or no output)"
fi

# ---------------------------------------------------------------------------
# test commit phase detection
# ---------------------------------------------------------------------------
echo "test: commit phase detection"
current_phase=$(cd "$HG_REPO" && hg log -r . --template '{phase}' 2>/dev/null) || true
if [[ "$current_phase" == "draft" || "$current_phase" == "public" ]]; then
    pass "phase detection returns valid value: $current_phase"
else
    fail "unexpected phase value" "got: $current_phase"
fi

# ---------------------------------------------------------------------------
# test check-ignore on non-existent path
# ---------------------------------------------------------------------------
echo "test: check-ignore on non-existent path"
nonexistent_path=".ralphex/progress/progress-test-nonexistent-$(date +%s).txt"
if cd "$HG_REPO" && "$SCRIPT" check-ignore -q -- "$nonexistent_path" 2>/dev/null; then
    pass "check-ignore on non-existent path exits 0 (matched ignore pattern)"
else
    exit_code=$?
    if [[ $exit_code -eq 1 ]]; then
        pass "check-ignore on non-existent path exits 1 (not ignored, did not crash)"
    else
        fail "check-ignore crashed with unexpected exit code" "got: $exit_code"
    fi
fi

# ---------------------------------------------------------------------------
# test commit -m "test" -- file1 on draft phase produces hg amend file1
# ---------------------------------------------------------------------------
echo "test: file-specific commit arg parsing"
# verify the script's commit handler produces the correct hg command by
# tracing execution with bash -x. we can't run the actual hg amend, but
# we can verify the script reaches the right code path with correct args.
if [[ "$current_phase" == "draft" ]]; then
    trace_output=$(cd "$HG_REPO" && bash -x "$SCRIPT" commit -m "test msg" -- .gitignore 2>&1 || true)
    if echo "$trace_output" | grep -q "hg amend .gitignore"; then
        pass "draft phase: commit with files produces 'hg amend .gitignore'"
    elif echo "$trace_output" | grep -q "hg amend"; then
        fail "draft phase: hg amend called without file args" "expected 'hg amend .gitignore'"
    else
        fail "draft phase: expected hg amend in trace" "got: $(echo "$trace_output" | tail -5)"
    fi
elif [[ "$current_phase" == "public" ]]; then
    trace_output=$(cd "$HG_REPO" && bash -x "$SCRIPT" commit -m "test msg" -- .gitignore 2>&1 || true)
    if echo "$trace_output" | grep -q 'hg commit -m .* .gitignore'; then
        pass "public phase: commit with files produces 'hg commit -m msg .gitignore'"
    else
        fail "public phase: expected hg commit with file args" "got: $(echo "$trace_output" | tail -5)"
    fi
fi

# ---------------------------------------------------------------------------
# test worktree exits 1 with error message
# ---------------------------------------------------------------------------
echo "test: worktree add exits with error"
worktree_stderr=$(cd "$HG_REPO" && "$SCRIPT" worktree add /tmp/test-wt 2>&1) || true
if echo "$worktree_stderr" | grep -q "worktree not supported"; then
    pass "worktree outputs error message to stderr"
else
    fail "worktree should output 'not supported' error" "got: $worktree_stderr"
fi

# verify exit code
if cd "$HG_REPO" && "$SCRIPT" worktree add /tmp/test-wt 2>/dev/null; then
    fail "worktree should exit non-zero"
else
    pass "worktree exits non-zero"
fi

# ---------------------------------------------------------------------------
# test unsupported command
# ---------------------------------------------------------------------------
echo "test: unsupported command"
if cd "$HG_REPO" && "$SCRIPT" nonsense 2>/dev/null; then
    fail "unsupported command should exit non-zero"
else
    pass "unsupported command exits non-zero"
fi

# ---------------------------------------------------------------------------
# summary
# ---------------------------------------------------------------------------
echo ""
echo "results: $passed passed, $failed failed, $total total"

if [[ $failed -gt 0 ]]; then
    exit 1
fi
