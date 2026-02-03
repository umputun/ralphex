#!/bin/bash
# ralphex-dk.sh - run ralphex in a docker container
#
# usage: ralphex-dk.sh [ralphex-args]
# example: ralphex-dk.sh docs/plans/feature.md
# example: ralphex-dk.sh --serve docs/plans/feature.md
# example: ralphex-dk.sh --review
# example: ralphex-dk.sh --update  # pull latest image

set -e

IMAGE="${RALPHEX_IMAGE:-ghcr.io/umputun/ralphex:latest}"
PORT="${RALPHEX_PORT:-8080}"

# handle --update flag: pull latest image and exit
if [[ "$1" == "--update" ]]; then
    echo "pulling latest image: ${IMAGE}" >&2
    docker pull "${IMAGE}"
    exit 0
fi

# check required directories exist (avoid docker creating them as root)
if [[ ! -d "${HOME}/.claude" ]]; then
    echo "error: ~/.claude directory not found (run 'claude' first to authenticate)" >&2
    exit 1
fi

# on macOS, extract credentials from keychain if not already in ~/.claude
CREDS_TEMP=""
if [[ "$(uname)" == "Darwin" && ! -f "${HOME}/.claude/.credentials.json" ]]; then
    # try to read credentials first (works if keychain already unlocked)
    CREDS_JSON=$(security find-generic-password -s "Claude Code-credentials" -w 2>/dev/null || true)
    if [[ -z "$CREDS_JSON" ]]; then
        # keychain locked - unlock and retry
        echo "unlocking macOS keychain to extract Claude credentials (enter login password)..." >&2
        security unlock-keychain 2>/dev/null || true
        CREDS_JSON=$(security find-generic-password -s "Claude Code-credentials" -w 2>/dev/null || true)
    fi
    if [[ -n "$CREDS_JSON" ]]; then
        CREDS_TEMP=$(mktemp)
        chmod 600 "$CREDS_TEMP"
        echo "$CREDS_JSON" > "$CREDS_TEMP"
        trap "rm -f '$CREDS_TEMP'" EXIT  # safety net
    fi
fi

# resolve path: if symlink, return real path; otherwise return original
resolve() { [[ -L "$1" ]] && realpath "$1" || echo "$1"; }

# collect unique parent directories of symlink targets inside a directory
# limit depth to avoid scanning tmp directories with many symlinks
symlink_target_dirs() {
    local src="$1"
    [[ -d "$src" ]] || return
    find "$src" -maxdepth 2 -type l 2>/dev/null | while read -r link; do
        dirname "$(realpath "$link" 2>/dev/null)" 2>/dev/null
    done | sort -u
}

# build volume mounts - credentials mounted read-only to /mnt, copied at startup
VOLUMES=(
    -v "$(resolve "${HOME}/.claude"):/mnt/claude:ro"
    -v "$(pwd):/workspace"
)

# mount extracted credentials from macOS keychain (separate path, init.sh will copy)
if [[ -n "$CREDS_TEMP" ]]; then
    VOLUMES+=(-v "${CREDS_TEMP}:/mnt/claude-credentials.json:ro")
fi

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

# ralphex config: mount directory and symlink targets under $HOME
if [[ -d "${HOME}/.config/ralphex" ]]; then
    VOLUMES+=(-v "$(resolve "${HOME}/.config/ralphex"):/home/app/.config/ralphex:ro")
    for target in $(symlink_target_dirs "${HOME}/.config/ralphex"); do
        [[ -d "$target" && "$target" == "${HOME}"/* ]] && VOLUMES+=(-v "${target}:${target}:ro")
    done
fi

# project-level .ralphex: resolve symlink targets if present (included in workspace mount)
if [[ -d "$(pwd)/.ralphex" ]]; then
    for target in $(symlink_target_dirs "$(pwd)/.ralphex"); do
        [[ -d "$target" && "$target" == "${HOME}"/* ]] && VOLUMES+=(-v "${target}:${target}:ro")
    done
fi

if [[ -e "${HOME}/.gitconfig" ]]; then
    VOLUMES+=(-v "$(resolve "${HOME}/.gitconfig"):/home/app/.gitconfig:ro")
fi

# only use -it when running interactively AND not using background mode for creds cleanup
DOCKER_FLAGS="--rm"
[[ -t 0 && -z "$CREDS_TEMP" ]] && DOCKER_FLAGS="-it --rm"

# run docker in background so we can delete temp credentials quickly
docker run $DOCKER_FLAGS \
    -e APP_UID="$(id -u)" \
    -e SKIP_HOME_CHOWN=1 \
    -e INIT_QUIET=1 \
    -e CLAUDE_CONFIG_DIR=/home/app/.claude \
    -p "${PORT}:${PORT}" \
    "${VOLUMES[@]}" \
    -w /workspace \
    "${IMAGE}" /srv/ralphex "$@" &
DOCKER_PID=$!

# delete temp credentials after init.sh copies them (reduces exposure window)
# run in background so it doesn't block; 10s gives plenty of time for init.sh
if [[ -n "$CREDS_TEMP" ]]; then
    (sleep 10; rm -f "$CREDS_TEMP") &
fi

# wait for docker and propagate exit code
wait $DOCKER_PID
