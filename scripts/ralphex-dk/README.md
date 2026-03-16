# ralphex-dk - Docker Wrapper

Python wrapper script that runs ralphex inside a Docker container, handling credential management, volume mounts, and environment configuration.

## Files

- `ralphex_dk.py` - symlink to `../ralphex-dk.sh` for Python test imports
- `ralphex_dk_test.py` - unit tests (~2700 lines, 208 tests)
- `../ralphex-dk.sh` - actual wrapper script (~1160 lines), served by curl install URL

## Usage

```bash
python3 scripts/ralphex-dk.sh [wrapper-flags] [ralphex-args]
```

### Wrapper flags

- `-E, --env VAR[=val]` - extra env var to pass to container (repeatable)
- `-v, --volume src:dst[:opts]` - extra volume mount (repeatable)
- `--docker` - mount host Docker socket into container (enables testcontainers, docker-dependent workflows)
- `--dry-run` - print docker command without executing
- `--update` - pull latest Docker image and exit
- `--update-script` - update this wrapper script and exit
- `--test` - run unit tests and exit
- `-h, --help` - show help
- `--claude-provider PROVIDER` - claude provider: `default` or `bedrock`

### Docker socket support

The `--docker` flag (or `RALPHEX_DOCKER_SOCKET=1` env var) mounts the host Docker socket into the container, enabling Docker-dependent workflows like testcontainers.

```bash
# mount Docker socket for testcontainers support
python3 scripts/ralphex-dk.sh --docker docs/plans/feature.md

# verify with dry-run
python3 scripts/ralphex-dk.sh --docker --dry-run
```

- Respects `DOCKER_HOST` env var for custom socket paths (unix:// scheme only)
- Auto-detects socket GID and passes `DOCKER_GID` env var for baseimage group setup
- Emits security warning on Linux (macOS has VM isolation)
- Exits with error if socket file doesn't exist (fail-fast)
- Never applies SELinux `:z`/`:Z` suffixes to socket mount

## Running Tests

```bash
python3 scripts/ralphex-dk.sh --test
```

## Installation (curl)

```bash
curl -sL https://raw.githubusercontent.com/umputun/ralphex/master/scripts/ralphex-dk.sh -o /usr/local/bin/ralphex
chmod +x /usr/local/bin/ralphex
```

`scripts/ralphex-dk.sh` is the actual file, keeping this install URL stable. `ralphex_dk.py` is a symlink back to it for Python test imports.
