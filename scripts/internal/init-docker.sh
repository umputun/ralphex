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

# when sourced by the test harness (INIT_DOCKER_SOURCE_ONLY set) expose the functions only
[ -n "${INIT_DOCKER_SOURCE_ONLY:-}" ] && return 0

# copy only essential claude files (not the entire 2GB directory)
if [ -d /mnt/claude ]; then
    mkdir -p /home/app/.claude
    # copy config files only (not cache, history, debug, todos, etc.)
    for f in .credentials.json settings.json settings.local.json CLAUDE.md format.sh; do
        [ -e "/mnt/claude/$f" ] && cp -L "/mnt/claude/$f" "/home/app/.claude/$f" 2>/dev/null || true
    done
    # copy essential directories (symlinked in dotfiles setups)
    for d in commands skills hooks agents; do
        [ -d "/mnt/claude/$d" ] && cp -rL "/mnt/claude/$d" "/home/app/.claude/" 2>/dev/null || true
    done
    seed_claude_plugins /mnt/claude/plugins /home/app/.claude/plugins
    chown -R app:app /home/app/.claude
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
    mkdir -p /home/app/.codex
    cp -rL /mnt/codex/* /home/app/.codex/ 2>/dev/null || true
    chown -R app:app /home/app/.codex
fi
