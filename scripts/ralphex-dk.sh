#!/bin/bash
# ralphex-dk.sh - run ralphex in a docker container
#
# usage: ralphex-dk.sh [ralphex-args]
# example: ralphex-dk.sh docs/plans/feature.md
# example: ralphex-dk.sh --serve docs/plans/feature.md
# example: ralphex-dk.sh --review
# example: ralphex-dk.sh --update         # pull latest docker image
# example: ralphex-dk.sh --update-script  # update this wrapper script

set -e

IMAGE="${RALPHEX_IMAGE:-ghcr.io/umputun/ralphex-go:latest}"
PORT="${RALPHEX_PORT:-8080}"

# handle --update flag: pull latest image and exit
if [[ "$1" == "--update" ]]; then
    echo "pulling latest image: ${IMAGE}" >&2
    docker pull "${IMAGE}"
    exit 0
fi

# handle --update-script flag: update this wrapper script and exit
if [[ "$1" == "--update-script" ]]; then
    SCRIPT_URL="https://raw.githubusercontent.com/umputun/ralphex/master/scripts/ralphex-dk.sh"
    SCRIPT_PATH="$(realpath "$0")"
    TEMP_SCRIPT=$(mktemp)
    trap "rm -f '$TEMP_SCRIPT'" EXIT

    echo "checking for ralphex docker wrapper updates..." >&2
    if curl -sfL "$SCRIPT_URL" -o "$TEMP_SCRIPT"; then
        if ! diff -q "$SCRIPT_PATH" "$TEMP_SCRIPT" >/dev/null 2>&1; then
            echo "wrapper update available:" >&2
            git diff --no-index "$SCRIPT_PATH" "$TEMP_SCRIPT" >&2 || true
            printf "update wrapper? (y/N) " >&2
            read -r answer
            if [[ "$answer" =~ ^[Yy]$ ]]; then
                cp "$TEMP_SCRIPT" "$SCRIPT_PATH"
                chmod +x "$SCRIPT_PATH"
                echo "wrapper updated" >&2
            else
                echo "wrapper update skipped" >&2
            fi
        else
            echo "wrapper is up to date" >&2
        fi
    else
        echo "warning: failed to check for wrapper updates" >&2
    fi
    rm -f "$TEMP_SCRIPT"
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

# detect git worktree and mount the main repo's .git directory
if [[ -f "$(pwd)/.git" ]]; then
    GIT_COMMON_DIR=$(git -C "$(pwd)" rev-parse --git-common-dir 2>/dev/null || true)
    if [[ -n "$GIT_COMMON_DIR" && -d "$GIT_COMMON_DIR" ]]; then
        GIT_COMMON_DIR=$(cd "$GIT_COMMON_DIR" && pwd)  # resolve to absolute
        VOLUMES+=(-v "${GIT_COMMON_DIR}:${GIT_COMMON_DIR}")
    fi
fi

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
    VOLUMES+=(-v "$(resolve "${HOME}/.config/ralphex"):/home/app/.config/ralphex")
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

# mount global gitignore at same path as configured in gitconfig
GLOBAL_GITIGNORE=$(git config --global core.excludesFile 2>/dev/null || true)
if [[ -n "$GLOBAL_GITIGNORE" && -e "$GLOBAL_GITIGNORE" ]]; then
    VOLUMES+=(-v "$(resolve "$GLOBAL_GITIGNORE"):${GLOBAL_GITIGNORE}:ro")
fi

# show which image is being used
echo "using image: ${IMAGE}" >&2

# schedule credential cleanup (runs in background, deletes after init.sh copies)
if [[ -n "$CREDS_TEMP" ]]; then
    (sleep 10; rm -f "$CREDS_TEMP") &
fi

# only bind port when --serve/-s is requested (avoids conflicts with concurrent instances)
PORT_ARGS=()
for arg in "$@"; do
    if [[ "$arg" == "--serve" || "$arg" == "-s" ]]; then
        PORT_ARGS=(-p "${PORT}:${PORT}")
        break
    fi
done

# run docker - foreground for interactive (TTY needed), background for non-interactive
if [[ -t 0 ]]; then
    # interactive mode: run in foreground so TTY works
    docker run -it --rm \
        -e APP_UID="$(id -u)" \
        -e SKIP_HOME_CHOWN=1 \
        -e INIT_QUIET=1 \
        -e CLAUDE_CONFIG_DIR=/home/app/.claude \
        "${PORT_ARGS[@]}" \
        "${VOLUMES[@]}" \
        -w /workspace \
        "${IMAGE}" /srv/ralphex "$@"
else
    # non-interactive: run in background to allow parallel credential cleanup
    docker run --rm \
        -e APP_UID="$(id -u)" \
        -e SKIP_HOME_CHOWN=1 \
        -e INIT_QUIET=1 \
        -e CLAUDE_CONFIG_DIR=/home/app/.claude \
        "${PORT_ARGS[@]}" \
        "${VOLUMES[@]}" \
        -w /workspace \
        "${IMAGE}" /srv/ralphex "$@" &
    DOCKER_PID=$!
    wait $DOCKER_PID
fi
