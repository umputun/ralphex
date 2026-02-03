# Stage 1: Build ralphex binary
FROM ghcr.io/umputun/baseimage/buildgo:latest AS build

ARG GIT_BRANCH
ARG GITHUB_SHA
ARG CI

WORKDIR /build
ADD . /build

RUN \
    if [ -z "$CI" ] ; then \
        echo "runs outside of CI"; \
        version=$(git describe --tags --always 2>/dev/null || echo "docker-$(date +%Y%m%dT%H%M%S)"); \
    else version=${GIT_BRANCH}-${GITHUB_SHA:0:7}-$(date +%Y%m%dT%H%M%S); fi && \
    echo "version=$version" && \
    go build -o /build/ralphex -ldflags "-X main.revision=${version} -s -w" ./cmd/ralphex

# Stage 2: Runtime image based on umputun/baseimage:app
FROM ghcr.io/umputun/baseimage/app:latest

LABEL org.opencontainers.image.source="https://github.com/umputun/ralphex"
LABEL org.opencontainers.image.description="Autonomous plan execution with Claude Code"
LABEL org.opencontainers.image.licenses="MIT"

# install node.js, npm, and claude code dependencies
RUN apk add --no-cache \
    nodejs npm \
    libgcc libstdc++ ripgrep \
    fzf git bash && \
    sed -i 's|/home/app:/bin/sh|/home/app:/bin/bash|' /etc/passwd

# set env for claude code on alpine (use system ripgrep)
ENV USE_BUILTIN_RIPGREP=0

# install claude code and codex globally, verify CLI commands exist
RUN npm install -g @anthropic-ai/claude-code @openai/codex && \
    command -v claude >/dev/null || { echo "error: claude CLI not found"; exit 1; } && \
    command -v codex >/dev/null || { echo "error: codex CLI not found"; exit 1; }

# copy ralphex binary
COPY --from=build /build/ralphex /srv/ralphex
RUN chmod +x /srv/ralphex

# copy init script (baseimage runs /srv/init.sh before main command)
COPY scripts/init-docker.sh /srv/init.sh
RUN chmod +x /srv/init.sh

# expose web dashboard port
EXPOSE 8080

WORKDIR /workspace

# baseimage runs CMD via init.sh entrypoint (handles APP_UID mapping)
CMD ["/srv/ralphex"]
