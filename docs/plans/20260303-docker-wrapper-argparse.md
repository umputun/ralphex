# Refactor ralphex-dk.sh to use argparse

## Overview
- Replace manual CLI argument extraction in the Docker wrapper script with Python's argparse
- Provides cleaner, more maintainable code with declarative flag definitions
- Enables auto-generated help for wrapper-specific flags
- Makes adding new flags straightforward

**Acceptance criteria:**
- All existing CLI behaviors preserved (same flags, same semantics)
- `--help` shows combined wrapper + ralphex help
- No behavior changes for existing `RALPHEX_EXTRA_*` env vars
- Args after `--` delimiter pass through unchanged to ralphex
- Invalid entries silently dropped with warnings (matching current behavior)

## Context (from discovery)
- **File:** `scripts/ralphex-dk.sh` (Python script despite .sh extension)
- **Current approach:** Manual `extract_extra_volumes()` and `extract_extra_env()` functions that iterate through args
- **Wrapper-specific flags:** `-E/--env`, `-v/--volume`, `--update`, `--update-script`, `--test`
- **Pass-through flags:** Everything else goes to ralphex (`--serve`, `--review`, plan files, etc.)
- **Env var integration:** `RALPHEX_EXTRA_ENV`, `RALPHEX_EXTRA_VOLUMES` provide defaults

## Development Approach
- **Testing approach**: Regular (refactor code first, then update/add tests)
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task**
- Run tests after each change with `python3 scripts/ralphex-dk.sh --test`

## Testing Strategy
- **Unit tests**: Embedded in `scripts/ralphex-dk.sh` (run with `--test` flag)
- Add new test classes for argparse-specific behavior
- Existing tests for `validate_env_entry`, `is_sensitive_name`, `build_env_vars`, `build_volumes` remain unchanged
- Container execution in `--help` is manual verification only (requires Docker)

## Progress Tracking
- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with + prefix
- Document issues/blockers with ! prefix

## Implementation Steps

### Task 1: Add argparse parser builder

**Files:**
- Modify: `scripts/ralphex-dk.sh`

- [x] add `import argparse` at top of file
- [x] create `build_parser()` function that returns `ArgumentParser`
- [x] define `-E/--env` flag with `action="append"`, `default=[]`
- [x] define `-v/--volume` flag with `action="append"`, `default=[]`
- [x] define `--update`, `--update-script`, `--test` as `store_true`
- [x] define `-h/--help` with `add_help=False` and custom `store_true`
- [x] add epilog documenting env vars (`RALPHEX_IMAGE`, `RALPHEX_PORT`, `RALPHEX_EXTRA_ENV`, `RALPHEX_EXTRA_VOLUMES`)
- [x] write tests for `build_parser()` (flags parsed correctly)
- [x] write tests for unknown args handling (`parse_known_args`)
- [x] write test for `--` delimiter: args after `--` are NOT consumed by wrapper
- [x] write test for `-E` at end without value (argparse raises error - document this behavior change)
- [x] run tests - must pass before task 2

### Task 2: Refactor main() to use argparse

**Files:**
- Modify: `scripts/ralphex-dk.sh`

- [x] replace manual `--test` check with argparse result
- [x] replace manual `--update` check with argparse result
- [x] replace manual `--update-script` check with argparse result
- [x] use `parse_known_args()` to separate wrapper flags from ralphex args
- [x] pass `ralphex_args` (unknown) through to `run_docker()`
- [x] write tests for main flow with various flag combinations
- [x] write test for `--` delimiter pass-through to ralphex
- [x] run tests - must pass before task 3

### Task 3: Integrate CLI flags with env var defaults

**Files:**
- Modify: `scripts/ralphex-dk.sh`

- [x] create `merge_env_flags()` helper: combines env var entries with CLI entries
- [x] create `merge_volume_flags()` helper: combines env var entries with CLI entries
- [x] merge `RALPHEX_EXTRA_ENV` with `args.env` (env first, CLI appends)
- [x] merge `RALPHEX_EXTRA_VOLUMES` with `args.volume` (env first, CLI appends)
- [x] validate merged env entries using `validate_env_entry()`, skip invalid (with warning)
- [x] validate merged volume entries: skip entries without `:` (silent, matching current behavior)
- [x] write tests for merge behavior (env only, CLI only, env + CLI combination)
- [x] write tests for validation on merged results (invalid entries dropped)
- [x] run tests - must pass before task 4

### Task 4: Implement combined --help behavior

**Files:**
- Modify: `scripts/ralphex-dk.sh`

- [x] detect `args.help` flag in main()
- [x] print wrapper help via `parser.print_help()`
- [x] print separator line between wrapper and ralphex help
- [x] run container with `["--help"]` to show ralphex options (manual verification only)
- [x] return exit code from container's help command
- [x] write tests for help flag detection (unit testable)
- [x] run tests - must pass before task 5

### Task 5: Remove deprecated manual extraction functions

**Files:**
- Modify: `scripts/ralphex-dk.sh`

- [x] verify new argparse tests cover equivalent cases from `TestExtractExtraVolumes`:
  - `-v src:dst` extracted correctly
  - `--volume src:dst` extracted correctly
  - multiple `-v` flags handled
  - `-v` at end without value (behavior change: now errors instead of pass-through)
  - mixed with other flags
- [x] verify new argparse tests cover equivalent cases from `TestExtractExtraEnv`:
  - `-E FOO=bar` extracted correctly
  - `-E FOO` (name-only) extracted correctly
  - `--env` long form works
  - multiple `-E` flags handled
  - lowercase `-e` passes through to ralphex (NOT consumed)
  - invalid entries skipped with warning
  - sensitive name warning
- [x] remove `extract_extra_volumes()` function
- [x] remove `extract_extra_env()` function
- [x] remove `TestExtractExtraVolumes` test class
- [x] remove `TestExtractExtraEnv` test class
- [x] update test suite list to exclude removed classes
- [x] run tests - verify no regressions

### Task 6: Verify acceptance criteria

- [x] verify all wrapper flags work: `-E`, `-v`, `--update`, `--update-script`, `--test`, `-h`
- [x] verify ralphex args pass through correctly: `--serve`, `--review`, plan files
- [x] verify `-e` (lowercase) passes through to ralphex (external-only flag)
- [x] verify env var + CLI merge works correctly
- [x] verify `--help` shows both wrapper and ralphex help
- [x] verify args after `--` pass through unchanged
- [x] run full test suite: `python3 scripts/ralphex-dk.sh --test`
- [x] manual test: `./scripts/ralphex-dk.sh -E FOO=bar -v /tmp:/mnt/tmp --help`

### Task 7: [Final] Update documentation

- [ ] update docstring at top of `scripts/ralphex-dk.sh`
- [ ] move this plan to `docs/plans/completed/`

## Technical Details

**Parser configuration:**
```python
parser = argparse.ArgumentParser(
    prog="ralphex-dk",
    description="Run ralphex in a Docker container",
    formatter_class=argparse.RawDescriptionHelpFormatter,
    add_help=False,  # custom --help handling
    epilog=textwrap.dedent("""\
        Environment variables:
          RALPHEX_IMAGE         Docker image (default: ghcr.io/umputun/ralphex-go:latest)
          RALPHEX_PORT          Web dashboard port with --serve (default: 8080)
          RALPHEX_EXTRA_ENV     Comma-separated env vars (VAR=value or VAR)
          RALPHEX_EXTRA_VOLUMES Comma-separated volume mounts (src:dst[:opts])

        All other arguments are passed through to ralphex.
    """),
)
```

**Flag definitions:**
- `-E/--env`: `action="append"`, `default=[]`, `metavar="VAR[=val]"`
- `-v/--volume`: `action="append"`, `default=[]`, `metavar="src:dst[:opts]"`
- `--update`, `--update-script`, `--test`: `action="store_true"`
- `-h/--help`: `action="store_true"` (custom handling)

**Merge logic:**
```python
def merge_env_flags(args_env: list[str]) -> list[str]:
    """Merge RALPHEX_EXTRA_ENV with CLI -E flags, validate entries."""
    result: list[str] = []
    # env var entries first
    for entry in build_env_vars():  # returns ["-e", "FOO", "-e", "BAR=val", ...]
        result.append(entry)
    # CLI entries append
    for entry in args_env:
        if validated := validate_env_entry(entry, warn_invalid=True):
            result.extend(["-e", validated])
    return result

def merge_volume_flags(args_volume: list[str]) -> list[str]:
    """Merge RALPHEX_EXTRA_VOLUMES with CLI -v flags, validate entries."""
    result: list[str] = []
    # env var entries first (from build_volumes extra section)
    extra = os.environ.get("RALPHEX_EXTRA_VOLUMES", "")
    for mount in extra.split(","):
        mount = mount.strip()
        if mount and ":" in mount:
            result.extend(["-v", mount])
    # CLI entries append
    for mount in args_volume:
        if ":" in mount:
            result.extend(["-v", mount])
    return result
```

**Behavior change:** `-E` or `-v` at end of args without a value now raises an argparse error instead of being silently passed through. This is acceptable since it's invalid usage.

## Post-Completion

**Manual verification:**
- Test wrapper in actual Docker environment
- Verify combined help output is readable
- Test with various flag orderings (flags before/after plan file)
- Test `--` delimiter: `./scripts/ralphex-dk.sh -E FOO -- -v /ignored plan.md`
