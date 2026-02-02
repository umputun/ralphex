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

# build volume mounts, skip optional ones if missing
VOLUMES=(
    -v "${HOME}/.claude:/home/app/.claude:ro"
    -v "$(pwd):/workspace"
)
[[ -d "${HOME}/.codex" ]] && VOLUMES+=(-v "${HOME}/.codex:/home/app/.codex:ro")
[[ -d "${HOME}/.config/ralphex" ]] && VOLUMES+=(-v "${HOME}/.config/ralphex:/home/app/.config/ralphex:ro")
[[ -f "${HOME}/.gitconfig" ]] && VOLUMES+=(-v "${HOME}/.gitconfig:/home/app/.gitconfig:ro")

exec docker run -it --rm \
    -e APP_UID="$(id -u)" \
    -e SKIP_HOME_CHOWN=1 \
    -e INIT_QUIET=1 \
    -p 8080:8080 \
    "${VOLUMES[@]}" \
    -w /workspace \
    "${IMAGE}" /srv/ralphex "$@"
