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

if [ "$fail" = 0 ]; then echo "PASS"; else echo "FAILURES"; exit 1; fi
