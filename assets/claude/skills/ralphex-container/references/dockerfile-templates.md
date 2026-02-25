# Dockerfile Templates

All custom Dockerfiles extend `ghcr.io/umputun/ralphex:latest` (Alpine-based) and add language-specific tooling.

Generated file: `Dockerfile.ralphex` in the user-chosen location (project root or `.ralphex/` directory).

## Rust

```dockerfile
FROM ghcr.io/umputun/ralphex:latest

# install rust toolchain
RUN apk add --no-cache rust cargo
ENV CARGO_HOME=/home/app/.cargo
ENV PATH="${PATH}:${CARGO_HOME}/bin"
```

## Java

```dockerfile
FROM ghcr.io/umputun/ralphex:latest

# install java (adjust version as needed: openjdk17-jdk, openjdk21-jdk)
ARG JAVA_VERSION=21
RUN apk add --no-cache openjdk${JAVA_VERSION}-jdk
ENV JAVA_HOME=/usr/lib/jvm/java-${JAVA_VERSION}-openjdk
ENV PATH="${PATH}:${JAVA_HOME}/bin"
```

For Gradle projects, append:
```dockerfile
# install gradle wrapper support (project should include gradlew)
RUN apk add --no-cache --virtual .build-deps wget unzip
```

For Maven projects, append:
```dockerfile
# install maven
RUN apk add --no-cache maven
```

## C# / .NET

```dockerfile
FROM ghcr.io/umputun/ralphex:latest

# install .net sdk (adjust version: dotnet8-sdk, dotnet9-sdk)
ARG DOTNET_VERSION=8
RUN apk add --no-cache dotnet${DOTNET_VERSION}-sdk
ENV DOTNET_ROOT=/usr/lib/dotnet
ENV PATH="${PATH}:${DOTNET_ROOT}:${HOME}/.dotnet/tools"
```

## Ruby

```dockerfile
FROM ghcr.io/umputun/ralphex:latest

# install ruby and bundler
RUN apk add --no-cache ruby ruby-dev ruby-bundler ruby-json
ENV GEM_HOME=/home/app/.gems
ENV PATH="${PATH}:${GEM_HOME}/bin"
```

## Elixir

```dockerfile
FROM ghcr.io/umputun/ralphex:latest

# install erlang and elixir
RUN apk add --no-cache elixir
ENV MIX_HOME=/home/app/.mix
ENV HEX_HOME=/home/app/.hex
ENV PATH="${PATH}:${MIX_HOME}:${HEX_HOME}"
```

## Extending Pre-Built Images

When Step 3 (dependency analysis) finds extra system packages for Go, Python, or Node.js projects, generate a minimal Dockerfile that extends the pre-built image.

### Go (with extra dependencies)

```dockerfile
FROM ghcr.io/umputun/ralphex-go:latest

# install extra system packages for native dependencies
RUN apk add --no-cache {{EXTRA_PACKAGES}}
```

### Python (with extra dependencies)

```dockerfile
FROM ghcr.io/umputun/ralphex:latest

# install extra system packages for native dependencies
RUN apk add --no-cache {{EXTRA_PACKAGES}}
```

### Node.js (with extra dependencies)

```dockerfile
FROM ghcr.io/umputun/ralphex:latest

# install extra system packages for native dependencies
RUN apk add --no-cache {{EXTRA_PACKAGES}}
```

If extra `RUN` lines are needed (e.g., package manager installs), append them after the `apk add` line:

```dockerfile
FROM ghcr.io/umputun/ralphex:latest

RUN apk add --no-cache {{EXTRA_PACKAGES}}
RUN npm install -g yarn
```

Replace `{{EXTRA_PACKAGES}}` with the deduplicated package list from the dependency analysis.

## Notes

- All images are Alpine-based, use `apk add` for packages
- The base image already includes: Node.js, npm, Python, git, bash, fzf, ripgrep, make, gcc
- The Go image (`ralphex-go`) is a separate pre-built image and does not need a custom Dockerfile unless extra packages are needed
- Set `ENV` paths so tools are available to the non-root `app` user
- Keep Dockerfiles minimal — only add what's needed for the language toolchain
