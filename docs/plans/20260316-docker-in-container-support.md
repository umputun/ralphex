# Docker-in-Container Support

## Overview
- Add `docker-cli` to the base Docker image so containers have the Docker client binary
- Add `--docker` flag (and `RALPHEX_DOCKER_SOCKET` env var) to the wrapper script to opt-in mount the host Docker socket
- Auto-detect socket GID and pass `--group-add` for proper permissions
- Emit security warning on Linux (macOS has VM isolation, no warning needed)
- Enables testcontainers and other Docker-dependent workflows inside ralphex containers

## Context
- `Dockerfile` — base image, needs `docker-cli` package added
- `scripts/ralphex-dk.sh` — Python wrapper, needs `--docker` flag, socket mount logic, GID detection, Linux warning
- `scripts/ralphex-dk/ralphex_dk_test.py` — tests, needs new test class for Docker socket feature
- Existing patterns: `--claude-provider` flag with env var fallback, `build_volumes()` for mounts, `build_docker_command()` for `--group-add`
- **Note**: `RALPHEX_DOCKER=1` is already used in the Dockerfile as a container-detection marker — must use a different env var name

## Solution Overview
- Install `docker-cli` in base Dockerfile — always available, zero cost if unused
- Add `--docker` wrapper flag with `RALPHEX_DOCKER_SOCKET=1` env var fallback (follows `--claude-provider` pattern)
- Socket mount added conditionally in `main()`, appended to volumes list
- GID auto-detected by `stat`-ing `/var/run/docker.sock` on the host, passed via `--group-add` in `build_docker_command()`
- Linux warning emitted to stderr when `--docker` is used and platform is Linux

## Technical Details

### Docker CLI in image
- Alpine package: `docker-cli` (client only, no daemon)
- Added to existing `apk add` line in base Dockerfile

### Wrapper flag
- `--docker` (argparse `store_true`, dest `docker`)
- Env var fallback: `RALPHEX_DOCKER_SOCKET` (truthy: "1", "true", "yes")
- Resolution function: `is_docker_enabled(cli_flag: bool) -> bool`
- **Note**: cannot use `RALPHEX_DOCKER` — already set in Dockerfile as container-detection marker

### Socket mount
- Default socket path: `/var/run/docker.sock`
- Mount: `-v /var/run/docker.sock:/var/run/docker.sock`
- Only mounted when flag/env var is set AND socket file exists
- If flag is set but socket doesn't exist: warning to stderr, skip mount
- **SELinux**: socket mount must NOT use `:z`/`:Z` suffixes — relabeling the Docker socket can break host Docker

### GID handling
- `get_docker_socket_gid(socket_path) -> Optional[int]` — `os.stat()` the socket, return `st_gid`
- Passed to `build_docker_command()` as new optional parameter `group_add: Optional[int]`
- Renders as `--group-add <gid>` in the docker command, before volumes

### Platform warning
- On Linux + `--docker`: print warning to stderr about host Docker access
- On macOS: silent (VM boundary provides isolation)
- Detection: `platform.system() == "Linux"`

### Dry-run support
- `--dry-run` with `--docker` shows the full command including socket mount and `--group-add`

## Development Approach
- **testing approach**: regular (code first, then tests)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task**

## Testing Strategy
- **unit tests**: wrapper script tests in `ralphex_dk_test.py` — new test classes following `TestBedrockSkipKeychain` pattern
- Tests cover: flag parsing, env var fallback, socket detection, GID extraction, volume assembly, `--group-add` in command, Linux warning, dry-run output, socket-missing warning, SELinux exclusion

## Progress Tracking
- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix

## Implementation Steps

### Task 1: Add docker-cli to base Dockerfile

**Files:**
- Modify: `Dockerfile`

- [x] add `docker-cli` to the existing `apk add --no-cache` line in the runtime stage
- [x] verify the image builds: `docker build -t ralphex-test .`
- [x] verify `docker` CLI is available: `docker run --rm ralphex-test docker --version`

### Task 2: Add --docker flag and env var support to wrapper

**Files:**
- Modify: `scripts/ralphex-dk.sh`

- [ ] add `--docker` flag to `build_parser()` (`store_true`, dest `docker`)
- [ ] add `is_docker_enabled(cli_flag: bool) -> bool` function (checks CLI flag, then `RALPHEX_DOCKER_SOCKET` env var)
- [ ] add `RALPHEX_DOCKER_SOCKET` to docstring header env vars section
- [ ] write tests in `ralphex_dk_test.py`: `TestDockerEnabled` class — flag true, flag false with env var, env var truthy values ("1", "true", "yes"), env var falsy/missing
- [ ] run tests: `python3 scripts/ralphex-dk.sh --test` — must pass before next task

### Task 3: Add socket mount, GID detection, and --group-add

**Files:**
- Modify: `scripts/ralphex-dk.sh`

- [ ] add `DOCKER_SOCKET_PATH = "/var/run/docker.sock"` constant
- [ ] add `get_docker_socket_gid(socket_path: str) -> Optional[int]` function — `os.stat()`, return `st_gid`, return `None` on `OSError`
- [ ] add `group_add: Optional[int] = None` parameter to `build_docker_command()`
- [ ] add `group_add: Optional[int] = None` parameter to `run_docker()`, pass through to `build_docker_command()`
- [ ] insert `--group-add <gid>` in docker command (after `--rm`, before volumes)
- [ ] in `main()`: when docker enabled, check socket exists, build `-v` mount (without `:z` — never apply SELinux suffix to socket), call `get_docker_socket_gid()`, pass GID to `run_docker()`
- [ ] append socket volume to `volumes` list (after `build_volumes()` + `extra_volumes`)
- [ ] if flag set but socket missing: print warning to stderr, skip mount
- [ ] write tests: `TestDockerSocketGid` class — mock `os.stat` for GID extraction, missing socket
- [ ] write tests: `TestDockerSocketMount` class (using `EnvTestCase` + patched `main()`) — verify socket volume appears when flag set and socket exists, absent when flag not set, absent when socket missing, no `:z` suffix on socket mount
- [ ] write tests: verify `--group-add <gid>` appears in command when GID provided, absent when `None`
- [ ] write tests: integration test via patched `main()` — full flow with docker flag, socket exists, GID detected
- [ ] run tests — must pass before next task

### Task 4: Add Linux security warning and dry-run verification

**Files:**
- Modify: `scripts/ralphex-dk.sh`

- [ ] after socket mount decision in `main()`: if docker enabled and socket mounted and `platform.system() == "Linux"`, print warning to stderr
- [ ] warning text: "warning: --docker mounts host Docker socket — containers have host-level Docker access"
- [ ] on macOS: no warning (VM boundary provides isolation)
- [ ] verify `--dry-run --docker` shows socket mount and `--group-add` in output (should work automatically since dry-run uses `build_docker_command()`)
- [ ] write tests: mock `platform.system()`, verify warning printed on Linux, not printed on macOS/Darwin
- [ ] write test: dry-run with docker flag — verify socket mount and group-add visible in printed command
- [ ] run full test suite: `python3 scripts/ralphex-dk.sh --test` — must pass before next task

### Task 5: Build and verify Docker functionality

- [ ] build the base image locally: `docker build -t ralphex-test .`
- [ ] verify `docker` CLI is available inside: `docker run --rm ralphex-test docker --version`
- [ ] test socket mount works: `docker run --rm -v /var/run/docker.sock:/var/run/docker.sock ralphex-test docker ps`
- [ ] test with the wrapper: `./scripts/ralphex-dk.sh --docker --dry-run` — verify socket mount and `--group-add` in output
- [ ] run wrapper with `--docker` on a toy project, verify `docker ps` works inside the container

### Task 6: [Final] Update documentation

- [ ] update wrapper script docstring (usage examples with `--docker`)
- [ ] update `scripts/ralphex-dk/README.md` with `--docker` flag
- [ ] update `CLAUDE.md` Docker section with docker socket support info
- [ ] update `llms.txt` Docker wrapper env vars section
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

**Manual verification:**
- test `--docker` with a project that uses testcontainers
- test on Linux to verify GID detection and warning
