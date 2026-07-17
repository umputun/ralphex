#!/bin/sh
# init script for ralphex docker container
# baseimage runs /srv/init.sh if it exists before the main command

# seed_claude_plugins copies installed plugin state from $1 into $2 while skipping
# regenerable plugin-manager state (marketplace git clones, repos, catalog caches). the
# ephemeral container never runs /plugin install, so that state is dead weight, and copying
# the marketplaces' thousands of vendored files over a macos bind mount costs
# seconds-to-a-minute per run (see #376). cache is kept: it holds installed plugins' runtime
# code (installed_plugins.json installPaths point into it), so dropping it would break
# plugin skills/agents in the container.
seed_claude_plugins() {
    src="$1"
    dest="$2"
    [ -d "$src" ] || return 0
    mkdir -p "$dest"
    for entry in "$src"/*; do
        [ -e "$entry" ] || continue
        case "$(basename "$entry")" in
            marketplaces|repos|plugin-catalog-cache.json|known_marketplaces.json) continue ;;
        esac
        cp -rL "$entry" "$dest"/ 2>/dev/null || true
    done
}

# cli_update_enabled reports whether claude/codex should be refreshed before the run. off by default
# so the base image stays quiet: authors building on it get no surprise npm install, network call, or
# version drift at container start. the image installs both CLIs unpinned, so a published tag freezes
# them at whatever npm served on that build, and both ship far more often than ralphex is tagged (see
# #410). a stale claude does not fail loudly, it resolves a short model alias like `sonnet` to an
# older model. opt in with RALPHEX_CLI_UPDATE=1: our ralphex-go image bakes it in, custom images add
# `ENV RALPHEX_CLI_UPDATE=1`, or set it per run via the wrapper.
cli_update_enabled() {
    # same truthy set and case-insensitivity as is_docker_enabled() in scripts/ralphex-dk.sh
    val=$(printf '%s' "${RALPHEX_CLI_UPDATE:-}" | tr '[:upper:]' '[:lower:]' | tr -d '[:space:]')
    case "$val" in
        1|true|yes) return 0 ;;
        *) return 1 ;;
    esac
}

# hard deadline for the whole refresh. npm's own defaults are far too slow to rely on here
# (fetch-timeout 300s, 2 retries), so a black-holed registry would stall every container start for
# minutes. the bounded npm settings keep a single attempt short; the timeout caps the total.
CLI_UPDATE_TIMEOUT=${CLI_UPDATE_TIMEOUT:-90}

# deadline for a single `<cli> --version` smoke check
CLI_SMOKE_TIMEOUT=${CLI_SMOKE_TIMEOUT:-20}

# grace before SIGKILL. busybox timeout only sends SIGTERM by default, and a process that ignores it
# runs to completion *and reports success* — so without -k neither deadline above actually binds.
CLI_KILL_GRACE=${CLI_KILL_GRACE:-10}

# overridable so tests do not write to the caller's real /tmp
CLI_UPDATE_LOG=${CLI_UPDATE_LOG:-/tmp/cli-update.log}
CLI_VERSION_FILE=${CLI_VERSION_FILE:-/tmp/cli-version.txt}

# update_cli_tools refreshes claude and codex to their current npm releases. best effort by design:
# on failure (offline, registry down, deadline hit) the image's baked versions stay in place and the
# run continues. runs as root, before baseimage drops to the app user, because the npm global dir is
# root-owned.
# run_cli_install performs the install itself, kept separate so tests can stub it: `timeout` execs a
# real binary, so a shell-function npm stub would not intercept and the suite would install for real.
run_cli_install() {
    timeout -k "$CLI_KILL_GRACE" "$CLI_UPDATE_TIMEOUT" npm install -g \
        --fetch-timeout=20000 --fetch-retries=1 \
        @anthropic-ai/claude-code@latest @openai/codex@latest >"$CLI_UPDATE_LOG" 2>&1
}

# cli_version prints $1's version, or nothing if it fails or hangs. same stub-seam reason as
# run_cli_install: timeout execs a real binary, so a shell-function stub would not intercept.
#
# the version goes through a file rather than straight up the caller's command substitution: timeout
# kills the CLI, but a child it spawned inherits the substitution's pipe and keeps it open, so $()
# would block on the grandchild long past the deadline (measured: 30s against a 2s deadline).
cli_version() {
    if timeout -k "$CLI_KILL_GRACE" "$CLI_SMOKE_TIMEOUT" "$1" --version >"$CLI_VERSION_FILE" 2>/dev/null; then
        cat "$CLI_VERSION_FILE" 2>/dev/null
    fi
}

update_cli_tools() {
    cli_update_enabled || return 0
    if ! run_cli_install; then
        # report why: the fallback is a silently older claude, which is the failure this whole step
        # exists to prevent, and the log dies with the container
        echo "ralphex: cli update failed, using versions baked into the image"
        tail -3 "$CLI_UPDATE_LOG" 2>/dev/null | sed 's/^/ralphex:   /'
        return 0
    fi
    # npm exiting 0 only means the install completed, not that either CLI runs. the install replaced
    # the baked packages in place, so there is no fallback left to return to: say so loudly rather
    # than let the run fail later with no hint of the cause. empty means missing, broken, or hung.
    claude_ver=$(cli_version claude)
    codex_ver=$(cli_version codex)
    if [ -z "$claude_ver" ]; then
        echo "ralphex: claude is broken after the update - re-run without RALPHEX_CLI_UPDATE to use the baked versions" >&2
        return 0
    fi
    if [ -z "$codex_ver" ]; then
        echo "ralphex: codex is broken after the update - re-run without RALPHEX_CLI_UPDATE to use the baked versions" >&2
        return 0
    fi
    echo "ralphex: cli updated, claude $claude_ver, codex $codex_ver"
}

# seed_claude_home copies essential claude files from $1 into $2 (not the entire 2GB directory).
# sets creds_bound=1 when .credentials.json is already rw bind-mounted at the destination, so the
# caller can skip the chown that would mutate the host file's metadata.
#
# the wrapper rw bind-mounts .credentials.json straight to the host so token refreshes persist.
# it updates through a separate channel from this image (--update-script fetches the wrapper from
# github, --update pulls the image), so an older wrapper emitting no bind mount is a supported
# combination — copy the file ourselves there or the container has no credentials at all.
seed_claude_home() {
    src="$1"
    dest="$2"
    [ -d "$src" ] || return 0
    mkdir -p "$dest"
    # probe before copying: docker establishes mounts before the entrypoint, so a bind-mounted
    # target already exists by now. the fallback copy below creates it too, so this cannot be
    # deferred until after the copy — both generations would then look identical.
    creds_bound=0
    [ -e "$dest/.credentials.json" ] && creds_bound=1
    # config files only (not cache, history, debug, todos, etc.)
    for f in settings.json settings.local.json CLAUDE.md format.sh; do
        [ -e "$src/$f" ] && cp -L "$src/$f" "$dest/$f" 2>/dev/null || true
    done
    if [ "$creds_bound" = 0 ] && [ -e "$src/.credentials.json" ]; then
        cp -L "$src/.credentials.json" "$dest/.credentials.json" 2>/dev/null || true
    fi
    # essential directories (symlinked in dotfiles setups)
    for d in commands skills hooks agents; do
        [ -d "$src/$d" ] && cp -rL "$src/$d" "$dest/" 2>/dev/null || true
    done
    seed_claude_plugins "$src/plugins" "$dest/plugins"
}

# seed_codex_home copies codex config from $1 into $2, skipping auth.json when it is already rw
# bind-mounted. sets auth_bound=1 in that case. same wrapper/image skew rationale as above.
seed_codex_home() {
    src="$1"
    dest="$2"
    [ -d "$src" ] || return 0
    mkdir -p "$dest"
    # probe before the loop, which copies auth.json itself in the no-bind-mount case
    auth_bound=0
    [ -e "$dest/auth.json" ] && auth_bound=1
    for entry in "$src"/*; do
        [ -e "$entry" ] || continue
        name="$(basename "$entry")"
        if [ "$name" = "auth.json" ] && [ "$auth_bound" = 1 ]; then
            continue
        fi
        cp -rL "$entry" "$dest/$name" 2>/dev/null || true
    done
}

# when sourced by the test harness (INIT_DOCKER_SOURCE_ONLY set) expose the functions only
[ -n "${INIT_DOCKER_SOURCE_ONLY:-}" ] && return 0

update_cli_tools

if [ -d /mnt/claude ]; then
    seed_claude_home /mnt/claude /home/app/.claude
    if [ "$creds_bound" = 1 ]; then
        # exclude .credentials.json: it is rw bind-mounted to the host, so chown would mutate
        # the host file's group metadata on Linux Docker hosts with real bind mounts.
        # -path anchors on the mount target so a nested file of the same name is still chowned.
        find /home/app/.claude ! -path /home/app/.claude/.credentials.json -exec chown app:app {} +
    else
        chown -R app:app /home/app/.claude
    fi
fi

# copy credentials extracted from macOS keychain (mounted separately)
if [ -f /mnt/claude-credentials.json ]; then
    mkdir -p /home/app/.claude
    cp /mnt/claude-credentials.json /home/app/.claude/.credentials.json
    chown -R app:app /home/app/.claude
    chmod 600 /home/app/.claude/.credentials.json
fi

# copy codex credentials if mounted
if [ -d /mnt/codex ]; then
    seed_codex_home /mnt/codex /home/app/.codex
    if [ "$auth_bound" = 1 ]; then
        # exclude auth.json: it is rw bind-mounted to the host, so chown would mutate
        # the host file's group metadata on Linux Docker hosts with real bind mounts.
        # -path anchors on the mount target so a nested file of the same name is still chowned.
        find /home/app/.codex ! -path /home/app/.codex/auth.json -exec chown app:app {} +
    else
        chown -R app:app /home/app/.codex
    fi
fi
