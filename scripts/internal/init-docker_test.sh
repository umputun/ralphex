#!/bin/sh
# tests for seed_claude_plugins in init-docker.sh (see #376): keep installed plugin
# runtime code (cache) and small state, skip regenerable plugin-manager state.

script_dir=$(cd -- "$(dirname -- "$0")" && pwd)

# source the script for its functions only; the guard stops it before the main body runs
# shellcheck source=scripts/internal/init-docker.sh
INIT_DOCKER_SOURCE_ONLY=1 . "$script_dir/init-docker.sh"

fail=0
assert() {  # $1 = description; rest = command expected to SUCCEED
    desc="$1"; shift
    if "$@"; then echo "ok   - $desc"; else echo "FAIL - $desc"; fail=1; fi
}
assert_not() {  # $1 = description; rest = command expected to FAIL
    desc="$1"; shift
    if "$@"; then echo "FAIL - $desc"; fail=1; else echo "ok   - $desc"; fi
}
assert_contains() {  # $1 = description; $2 = haystack; $3 = needle
    case "$2" in
        *"$3"*) echo "ok   - $1" ;;
        *) echo "FAIL - $1 (no '$3' in '$2')"; fail=1 ;;
    esac
}

work=$(mktemp -d "${TMPDIR:-/tmp}/init-docker-test.XXXXXX")
trap 'rm -rf "$work"' EXIT

src="$work/src/plugins"
dest="$work/dest/plugins"
mkdir -p "$src/marketplaces/ralphex/vendor" "$src/cache/ralphex/ralphex/0.20.0" "$src/repos" "$src/data"
printf '{}' > "$src/config.json"
printf '{}' > "$src/installed_plugins.json"
printf '{}' > "$src/known_marketplaces.json"
printf '{}' > "$src/plugin-catalog-cache.json"
printf '{}' > "$src/blocklist.json"
printf 'runtime-code' > "$src/cache/ralphex/ralphex/0.20.0/skill.md"
printf 'vendored-go' > "$src/marketplaces/ralphex/vendor/big.go"

seed_claude_plugins "$src" "$dest"

# kept: installed plugin runtime code plus the small state files
assert     "keeps cache runtime code"        test -f "$dest/cache/ralphex/ralphex/0.20.0/skill.md"
assert     "keeps config.json"               test -f "$dest/config.json"
assert     "keeps installed_plugins.json"    test -f "$dest/installed_plugins.json"
assert     "keeps blocklist.json"            test -f "$dest/blocklist.json"
assert     "keeps data dir"                  test -d "$dest/data"
# dropped: regenerable plugin-manager state
assert_not "drops marketplaces clone"        test -e "$dest/marketplaces"
assert_not "drops repos"                     test -e "$dest/repos"
assert_not "drops plugin-catalog-cache.json" test -e "$dest/plugin-catalog-cache.json"
assert_not "drops known_marketplaces.json"   test -e "$dest/known_marketplaces.json"

# no-op when the source plugins dir is absent (no dest created)
seed_claude_plugins "$work/absent" "$work/dest2"
assert_not "no-op when source absent"        test -e "$work/dest2"

# cli update gating (#410): off by default so the base image stays quiet, on for the documented
# opt-in values
unset RALPHEX_CLI_UPDATE
assert_not "cli update off when unset"        cli_update_enabled
RALPHEX_CLI_UPDATE="" assert_not "cli update off when empty" cli_update_enabled
RALPHEX_CLI_UPDATE=1 assert "cli update on for 1"          cli_update_enabled
RALPHEX_CLI_UPDATE=true assert "cli update on for true"    cli_update_enabled
RALPHEX_CLI_UPDATE=yes assert "cli update on for yes"      cli_update_enabled
RALPHEX_CLI_UPDATE=0 assert_not "cli update off for 0"     cli_update_enabled
# case and whitespace tolerance, matching is_docker_enabled() in scripts/ralphex-dk.sh
RALPHEX_CLI_UPDATE=TRUE assert "cli update on for TRUE"    cli_update_enabled
RALPHEX_CLI_UPDATE=Yes assert "cli update on for Yes"      cli_update_enabled
RALPHEX_CLI_UPDATE=" 1 " assert "cli update on for padded 1" cli_update_enabled
RALPHEX_CLI_UPDATE=maybe assert_not "cli update off for unknown value" cli_update_enabled

# update_cli_tools is best effort: a failing install must not fail the run, and must say so.
# run_cli_install is stubbed rather than npm itself — it wraps npm in `timeout`, which execs a real
# binary and would bypass a shell-function npm stub, installing for real on the test machine.
# keep these inside the test workdir; the defaults are the caller's real /tmp
CLI_UPDATE_LOG="$work/cli-update.log"
CLI_VERSION_FILE="$work/cli-version.txt"
# these tests exercise the install path, so opt in for the duration
RALPHEX_CLI_UPDATE=1; export RALPHEX_CLI_UPDATE
printf 'npm error boom\n' > "$CLI_UPDATE_LOG"
run_cli_install() { return 1; }
quiet_update() { update_cli_tools >/dev/null 2>&1; }
out=$(update_cli_tools 2>&1)
assert     "install failure does not abort"  quiet_update
assert_contains "install failure is reported" "$out" "update failed"
assert_contains "install failure says why"   "$out" "npm error boom"

# install succeeds but a CLI is broken or hangs: cli_version yields nothing for it. must not claim
# success, must name the opt-out, must not abort
run_cli_install() { return 0; }
cli_version() { case "$1" in claude) return 0 ;; codex) echo "codex-cli 9.9.9" ;; esac; }
out=$(update_cli_tools 2>&1)
assert     "broken cli does not abort"       quiet_update
assert_contains "broken cli is reported"     "$out" "claude is broken"
assert_contains "broken cli names the toggle" "$out" "RALPHEX_CLI_UPDATE"
case "$out" in *"cli updated"*) echo "FAIL - broken cli must not claim success"; fail=1 ;; *) echo "ok   - broken cli must not claim success" ;; esac

# a broken codex is caught too, not just claude
cli_version() { case "$1" in claude) echo "2.1.212 (Claude Code)" ;; codex) return 0 ;; esac; }
out=$(update_cli_tools 2>&1)
assert_contains "broken codex is reported"   "$out" "codex is broken"

# both CLIs healthy: reports the resulting versions
cli_version() { case "$1" in claude) echo "2.1.212 (Claude Code)" ;; codex) echo "codex-cli 9.9.9" ;; esac; }
out=$(update_cli_tools 2>&1)
assert_contains "healthy update reports claude" "$out" "2.1.212"
assert_contains "healthy update reports codex"  "$out" "9.9.9"

# disabled (the default) short-circuits before the install runs at all
run_cli_install() { echo "install must not run"; return 0; }
out=$(RALPHEX_CLI_UPDATE=0 update_cli_tools 2>&1)
assert     "disabled skips install entirely" test -z "$out"
unset -f run_cli_install cli_version

# run_cli_install and cli_version are stubbed everywhere above, so assert the real commands against
# the source: a typo in a package spec would silently install nothing useful, and busybox timeout
# without -k neither kills a SIGTERM-ignoring child nor reports its overrun as a failure
src=$(cat "$script_dir/init-docker.sh")
assert_contains "installs claude-code@latest" "$src" "@anthropic-ai/claude-code@latest"
assert_contains "installs codex@latest"       "$src" "@openai/codex@latest"
# both timeouts must force a kill; a bare `timeout` lets a SIGTERM-ignoring child overrun and still
# reports success, so neither deadline would bind
assert "both deadlines force kill" test "$(printf '%s\n' "$src" | grep -c 'timeout -k')" -eq 2

if [ "$fail" = 0 ]; then echo "PASS"; else echo "FAILURES"; exit 1; fi
