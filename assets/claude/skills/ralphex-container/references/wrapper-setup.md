# Wrapper Script Installation

The `ralphex-dk.sh` wrapper script is the recommended way to run ralphex in Docker. It handles volume mounting, macOS keychain credential extraction, git worktree detection, and symlink resolution automatically.

## Native Binary vs Wrapper Script

ralphex can be installed in two ways:

- **Native binary**: standalone Go binary installed via `brew`, `go install`, or downloaded from releases. Runs ralphex directly on the host, requires claude/codex CLI installed locally.
- **Wrapper script**: shell script (`ralphex-dk.sh`) that runs ralphex inside a Docker container. All dependencies (claude, codex, language toolchains) are bundled in the image.

Both provide the same CLI interface. The wrapper script is recommended when you want isolated, reproducible environments — especially for projects with complex toolchain requirements.

**Important**: if a native `ralphex` binary is already installed, installing the wrapper as `ralphex` will shadow it. In this case, install the wrapper as `ralphex-dk` to keep both available, or deliberately replace the native binary.

## Download

```bash
curl -sL https://raw.githubusercontent.com/umputun/ralphex/master/scripts/ralphex-dk.sh -o ralphex
chmod +x ralphex
```

## Installation Paths

### System-wide

```bash
sudo mv ralphex /usr/local/bin/ralphex
```

**Note**: this requires `sudo` which cannot be executed by Claude Code. When automating this step, print the command for the user to run manually instead of executing it.

### User-local

```bash
mkdir -p ~/.local/bin
mv ralphex ~/.local/bin/ralphex
```

Ensure `~/.local/bin` is in PATH:
- **fish**: `fish_add_path ~/.local/bin`
- **bash/zsh**: add `export PATH="$HOME/.local/bin:$PATH"` to shell profile

### Project-local

```bash
mv ralphex ./ralphex-dk
# run as: ./ralphex-dk docs/plans/feature.md
```

## Custom Image Configuration

By default the wrapper uses `ghcr.io/umputun/ralphex-go:latest`. Override with the `RALPHEX_IMAGE` env var.

### Shell Configuration

**fish**:
```fish
set -Ux RALPHEX_IMAGE ghcr.io/umputun/ralphex:latest
```

**bash** (add to `~/.bashrc`):
```bash
export RALPHEX_IMAGE="ghcr.io/umputun/ralphex:latest"
```

**zsh** (add to `~/.zshrc`):
```bash
export RALPHEX_IMAGE="ghcr.io/umputun/ralphex:latest"
```

### For custom Dockerfile builds

If a `Dockerfile.ralphex` was generated, build and tag it first (path depends on file location):

```bash
# if in project root:
docker build -f Dockerfile.ralphex -t ralphex-custom .

# if in .ralphex/ directory:
docker build -f .ralphex/Dockerfile.ralphex -t ralphex-custom .
```

Then configure:

**fish**: `set -Ux RALPHEX_IMAGE ralphex-custom`
**bash/zsh**: `export RALPHEX_IMAGE="ralphex-custom"`

## Other Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `RALPHEX_IMAGE` | `ghcr.io/umputun/ralphex-go:latest` | Docker image to use |
| `RALPHEX_PORT` | `8080` | Web dashboard port with `--serve` |
| `RALPHEX_EXTRA_VOLUMES` | (empty) | Extra volume mounts, comma-separated (e.g., `/var/run/docker.sock:/var/run/docker.sock`) |
| `CLAUDE_CONFIG_DIR` | `~/.claude` | Claude config directory (for alternate installs) |

## Wrapper Commands

```bash
# execute a plan
ralphex docs/plans/feature.md

# review mode
ralphex --review

# with web dashboard
ralphex --serve docs/plans/feature.md

# update docker image
ralphex --update

# update the wrapper script itself
ralphex --update-script
```

## What the Wrapper Handles

- Mounts `~/.claude`, `~/.codex`, `~/.config/ralphex`, `~/.gitconfig` into container
- Resolves and mounts symlink targets (common with claude projects)
- Detects and mounts git worktree common directories
- Extracts macOS Keychain credentials for Claude authentication (uses temporary files, cleaned up on exit — more secure than saving credentials to disk, which is required for raw docker-compose on macOS)
- Forwards signals (SIGTERM) to the Docker process
- Binds port 8080 when `--serve` flag is present

## Docker Socket Access

When the project uses testcontainers or Docker SDK, the wrapper script needs `RALPHEX_EXTRA_VOLUMES` configured to mount the Docker socket into the container.

**fish**:
```fish
set -Ux RALPHEX_EXTRA_VOLUMES /var/run/docker.sock:/var/run/docker.sock
```

**bash** (add to `~/.bashrc`):
```bash
export RALPHEX_EXTRA_VOLUMES="/var/run/docker.sock:/var/run/docker.sock"
```

**zsh** (add to `~/.zshrc`):
```bash
export RALPHEX_EXTRA_VOLUMES="/var/run/docker.sock:/var/run/docker.sock"
```

If the project also needs `docker-cli` inside the container, a custom `Dockerfile.ralphex` must be generated with `apk add docker-cli`, and `RALPHEX_IMAGE` should point to the custom image (see "Custom Image Configuration" above).

## Claude Code Limitations

When this skill runs inside Claude Code, the following restrictions apply:

- **No `sudo`**: Claude Code cannot execute privileged commands. For system-wide installs (`/usr/local/bin/`), output the `sudo mv ...` command for the user to run manually.
- **No interactive prompts**: use AskUserQuestion for choices instead of shell prompts.
- **PATH changes**: adding `~/.local/bin` to PATH requires modifying the user's shell profile. Detect the shell and provide the correct command, but note the change only takes effect in new shell sessions.
