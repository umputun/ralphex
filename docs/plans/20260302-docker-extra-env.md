# Add Extra Environment Variables Support to Docker Wrapper

## Overview
Add support for passing additional environment variables to the docker container via:
1. `RALPHEX_EXTRA_ENV` environment variable (comma-separated)
2. `-e`/`--env` CLI flags

This mirrors the existing `RALPHEX_EXTRA_VOLUMES` and `-v`/`--volume` pattern for consistency.

**Problem solved**: Users need to pass custom environment variables (API keys, debug flags, custom config) to the containerized ralphex without modifying the wrapper script.

**Key benefits**:
- Consistent UX with existing volumes feature
- Secure credential passing via name-only inheritance (`-e VARNAME`)
- Security warnings for sensitive names with explicit values

## Context (from discovery)
- **Files involved**: `scripts/ralphex-dk.sh` (Python), `llms.txt`, `README.md`
- **Related patterns**: `RALPHEX_EXTRA_VOLUMES`, `extract_extra_volumes()`, `build_volumes()`
- **Dependencies**: None (self-contained in wrapper)
- **Note**: `--env=VAR` (equals-attached) format NOT supported, only `--env VAR` (space-separated), matching volumes pattern

## Development Approach
- **Testing approach**: TDD (tests first)
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task**
- **CRITICAL: update this plan file when scope changes during implementation**
- Run tests after each change with `python3 scripts/ralphex-dk.sh --test`

## Testing Strategy
- **Unit tests**: Embedded in `scripts/ralphex-dk.sh` using unittest
- **Test classes**: Mirror existing `TestExtraVolumes`, `TestExtractExtraVolumes` patterns
- **Coverage**: Success cases, error cases, edge cases for each function

## Progress Tracking
- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix

## Implementation Steps

### Task 1: Add TestIsSensitiveName test class (TDD)

**Files:**
- Modify: `scripts/ralphex-dk.sh`

- [x] add `TestIsSensitiveName` test class after `TestExtractExtraVolumes`
- [x] add test for matching sensitive patterns (KEY, SECRET, TOKEN, etc.)
- [x] add test for case insensitivity (api_key, API_KEY, Api_Key)
- [x] add test for non-sensitive names returning False
- [x] add test for partial matches (MY_API_KEY should match - substring match)
- [x] add test for non-matches (MONKEY should NOT match - KEY is substring but not at word boundary)
- [x] add `TestIsSensitiveName` to suite loader list
- [x] run tests - expect failures (function not implemented yet)

### Task 2: Implement is_sensitive_name() function

**Files:**
- Modify: `scripts/ralphex-dk.sh`

- [x] add `SENSITIVE_PATTERNS` constant after `SCRIPT_URL`
- [x] add `is_sensitive_name(name: str) -> bool` function after `selinux_enabled()`
- [x] implement case-insensitive pattern matching
- [x] run tests - `TestIsSensitiveName` must pass

### Task 3: Add TestExtractExtraEnv test class (TDD)

**Files:**
- Modify: `scripts/ralphex-dk.sh`

- [x] add `TestExtractExtraEnv` test class (mirror `TestExtractExtraVolumes`)
- [x] add test for `-e FOO=bar` extraction
- [x] add test for `-e FOO` (name-only) extraction
- [x] add test for `--env` variant
- [x] add test for multiple flags
- [x] add test for no flags (passthrough)
- [x] add test for `-e` at end without value
- [x] add test for mixed with other flags
- [x] add `TestExtractExtraEnv` to suite loader list
- [x] run tests - expect failures

### Task 4: Implement extract_extra_env() function

**Files:**
- Modify: `scripts/ralphex-dk.sh`

- [ ] add `extract_extra_env(args: list[str]) -> tuple[list[str], list[str]]` after `extract_extra_volumes()`
- [ ] extract `-e`/`--env` flags from args
- [ ] return `(extra_env_flags, remaining_args)` - flags as `["-e", "VAL", ...]`
- [ ] run tests - `TestExtractExtraEnv` must pass

### Task 5: Add TestBuildEnvVars test class (TDD)

**Files:**
- Modify: `scripts/ralphex-dk.sh`

- [ ] add `TestBuildEnvVars` test class
- [ ] add test for `RALPHEX_EXTRA_ENV` parsing with explicit values
- [ ] add test for name-only entries (inherit from host)
- [ ] add test for comma separation and whitespace trimming
- [ ] add test for invalid entries skipped (entries with invalid var names)
- [ ] add test for empty env var is noop
- [ ] add test for sensitive name warning (capture stderr)
- [ ] add `TestBuildEnvVars` to suite loader list
- [ ] run tests - expect failures

### Task 6: Implement build_env_vars() function

**Files:**
- Modify: `scripts/ralphex-dk.sh`

- [ ] add `build_env_vars() -> list[str]` after `build_volumes()`
- [ ] parse `RALPHEX_EXTRA_ENV` env var, split by comma
- [ ] validate format: valid env var name with optional `=value`
- [ ] warn on sensitive names with explicit values (print to stderr)
- [ ] return flat list `["-e", "FOO=bar", "-e", "BAZ", ...]`
- [ ] run tests - `TestBuildEnvVars` must pass

### Task 7: Update run_docker() and main()

**Files:**
- Modify: `scripts/ralphex-dk.sh`

- [ ] add `env_vars: list[str]` parameter to `run_docker()` signature
- [ ] insert env vars after `CLAUDE_CONFIG_DIR` env var (line ~413) and before `if bind_port:` block
- [ ] in `main()`: call `extract_extra_env(args)` after `extract_extra_volumes()`
- [ ] in `main()`: call `build_env_vars()` and merge with CLI env flags (CLI after env var entries)
- [ ] in `main()`: pass combined env vars to `run_docker()`
- [ ] run all tests - must pass

### Task 8: Verify acceptance criteria

- [ ] verify `RALPHEX_EXTRA_ENV="FOO=bar,BAZ"` works
- [ ] verify `-e FOO=bar -e BAZ` CLI flags work
- [ ] verify sensitive name warning appears for `API_KEY=secret`
- [ ] verify name-only inheritance works (`-e VARNAME`)
- [ ] run full test suite: `python3 scripts/ralphex-dk.sh --test`

### Task 9: Update documentation

**Files:**
- Modify: `llms.txt`
- Modify: `README.md`
- Modify: `scripts/ralphex-dk.sh` (docstring)

- [ ] add `RALPHEX_EXTRA_ENV` to Environment variables section in `llms.txt`
- [ ] add `RALPHEX_EXTRA_ENV` to Docker environment variables section in `README.md` (~line 299)
- [ ] add `-e` usage example to `README.md` Docker usage section (~line 306)
- [ ] update script docstring (lines 4-11) with `-e` example
- [ ] document format: comma-separated, `VAR=value` or `VAR`
- [ ] document security warning behavior
- [ ] move this plan to `docs/plans/completed/`

## Technical Details

**SENSITIVE_PATTERNS**: `["KEY", "SECRET", "TOKEN", "PASSWORD", "PASSWD", "CREDENTIAL", "AUTH"]`
- Matching: case-insensitive substring match with word boundaries (underscore or start/end)
- `MY_API_KEY` matches (KEY at word boundary)
- `MONKEY` does NOT match (KEY not at word boundary)
- `SECRET_TOKEN` matches (both SECRET and TOKEN)

**Format validation regex**: `^[A-Za-z_][A-Za-z0-9_]*(=.*)?$`
- Empty values allowed: `VAR=` is valid

**Warning message format**:
```
warning: API_KEY has explicit value - use -e API_KEY to inherit from host for better security
```
- Only warns when `=` is present (explicit value)
- Name-only entries do NOT warn (they inherit securely)

**Docker command structure** (env vars inserted after existing -e flags):
```
docker run -it --rm \
  -e APP_UID=... -e TIME_ZONE=... -e CLAUDE_CONFIG_DIR=... [existing -e flags] \
  -e FOO=bar -e BAZ [new extra env vars from RALPHEX_EXTRA_ENV + CLI] \
  -p ... [port binding if --serve] \
  -v ... [volumes] \
  ...
```

## Post-Completion

**Manual verification**:
- Test with actual docker container: `RALPHEX_EXTRA_ENV="DEBUG=1" ./scripts/ralphex-dk.sh --help`
- Test sensitive warning: `RALPHEX_EXTRA_ENV="API_KEY=secret" ./scripts/ralphex-dk.sh --help 2>&1`
- Test CLI flags: `./scripts/ralphex-dk.sh -e FOO=bar --help`
