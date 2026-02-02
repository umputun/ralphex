#!/bin/bash
# ralphex-dk.sh - run ralphex in a docker container
#
# usage: ralphex-dk.sh [ralphex-args]
# example: ralphex-dk.sh docs/plans/feature.md
# example: ralphex-dk.sh --serve docs/plans/feature.md
# example: ralphex-dk.sh --review

set -e

IMAGE="${RALPHEX_IMAGE:-ghcr.io/umputun/ralphex:latest}"

exec docker run -it --rm \
    -e APP_UID="$(id -u)" \
    -e SKIP_HOME_CHOWN=1 \
    -e INIT_QUIET=1 \
    -p 8080:8080 \
    -v "${HOME}/.claude:/home/app/.claude:ro" \
    -v "${HOME}/.codex:/home/app/.codex:ro" \
    -v "${HOME}/.config/ralphex:/home/app/.config/ralphex:ro" \
    -v "${HOME}/.gitconfig:/home/app/.gitconfig:ro" \
    -v "$(pwd):/workspace" \
    -w /workspace \
    "${IMAGE}" /srv/ralphex "$@"
