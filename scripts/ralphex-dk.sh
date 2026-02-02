#!/bin/bash
# ralphex-dk.sh - run ralphex in a docker container
#
# usage: ralphex-dk.sh [ralphex-args]
# example: ralphex-dk.sh docs/plans/feature.md
# example: ralphex-dk.sh --serve docs/plans/feature.md
# example: ralphex-dk.sh --review

set -e

IMAGE="${RALPHEX_IMAGE:-ghcr.io/umputun/ralphex:latest}"

# check required directories exist (avoid docker creating them as root)
if [[ ! -d "${HOME}/.claude" ]]; then
    echo "error: ~/.claude directory not found (run 'claude' first to authenticate)" >&2
    exit 1
fi

# resolve path: if symlink, return real path; otherwise return original
resolve() { [[ -L "$1" ]] && realpath "$1" || echo "$1"; }

# collect unique parent directories of symlink targets inside a directory
symlink_target_dirs() {
    local src="$1"
    [[ -d "$src" ]] || return
    find "$src" -type l 2>/dev/null | while read -r link; do
        dirname "$(realpath "$link" 2>/dev/null)" 2>/dev/null
    done | sort -u
}

# build volume mounts - credentials mounted read-only to /mnt, copied at startup
VOLUMES=(
    -v "$(resolve "${HOME}/.claude"):/mnt/claude:ro"
    -v "$(pwd):/workspace"
)

# add mounts for symlink targets under $HOME (Docker Desktop shares $HOME by default)
for target in $(symlink_target_dirs "${HOME}/.claude"); do
    [[ -d "$target" && "$target" == "${HOME}"/* ]] && VOLUMES+=(-v "${target}:${target}:ro")
done

# codex: mount directory and symlink targets under $HOME (skip homebrew temp symlinks)
if [[ -d "${HOME}/.codex" ]]; then
    VOLUMES+=(-v "$(resolve "${HOME}/.codex"):/mnt/codex:ro")
    for target in $(symlink_target_dirs "${HOME}/.codex"); do
        [[ -d "$target" && "$target" == "${HOME}"/* ]] && VOLUMES+=(-v "${target}:${target}:ro")
    done
fi

if [[ -e "${HOME}/.config/ralphex" ]]; then
    VOLUMES+=(-v "$(resolve "${HOME}/.config/ralphex"):/home/app/.config/ralphex:ro")
fi

if [[ -e "${HOME}/.gitconfig" ]]; then
    VOLUMES+=(-v "$(resolve "${HOME}/.gitconfig"):/home/app/.gitconfig:ro")
fi

exec docker run -it --rm \
    -e APP_UID="$(id -u)" \
    -e SKIP_HOME_CHOWN=1 \
    -e INIT_QUIET=1 \
    -e CLAUDE_CONFIG_DIR=/home/app/.claude \
    -p 8080:8080 \
    "${VOLUMES[@]}" \
    -w /workspace \
    "${IMAGE}" /srv/ralphex "$@"
