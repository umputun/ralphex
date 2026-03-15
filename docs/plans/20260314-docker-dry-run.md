# Add --dry-run flag to Docker wrapper

## Overview
- Add `--dry-run` flag to the Docker wrapper script (`scripts/ralphex-dk.sh`)
- When set, prints the full docker command that would be executed without running it
- Enables debugging of auth failures, volume mounts, and env var issues
- Output: single-line copy-pasteable command to stdout, warnings to stderr

## Context (from discovery)
- Files/components involved:
  - `scripts/ralphex-dk.sh` - main wrapper script (Python)
  - `scripts/ralphex-dk/ralphex_dk_test.py` - test file
  - `llms.txt` - usage documentation
- Related patterns: existing `--update`, `--test`, `--help` flags follow same pattern
- Trade-off accepted: inherited env vars (e.g., `-e AWS_ACCESS_KEY_ID` without `=value`) won't work when copying command to a new shell - warning will be printed

## Development Approach
- **testing approach**: Regular (code first, then tests)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task**
- run tests after each change: `python3 scripts/ralphex-dk.sh --test`

## Testing Strategy
- **unit tests**: add to `scripts/ralphex-dk/ralphex_dk_test.py`
- test command building with various input combinations
- test warning logic for inherited vs explicit env vars

## Progress Tracking
- mark completed items with `[x]` immediately when done
- add newly discovered tasks with + prefix
- document issues/blockers with ! prefix

## Implementation Steps

### Task 1: Extract build_docker_command() function

**Files:**
- Modify: `scripts/ralphex-dk.sh`
- Modify: `scripts/ralphex-dk/ralphex_dk_test.py`

- [x] create `build_docker_command(image, port, volumes, env_vars, bind_port, args) -> list[str]` function
- [x] include ALL command assembly logic: `docker run`, interactive flag (`-it` when tty), `--rm`, `build_base_env_vars()`, env_vars, port binding with `RALPHEX_WEB_HOST`, volumes, `-w /workspace`, image, entrypoint, and args
- [x] update `run_docker()` to call `build_docker_command()` and use returned list
- [x] verify existing functionality unchanged by running `python3 scripts/ralphex-dk.sh --test`
- [x] update test imports in `ralphex_dk_test.py` to include `build_docker_command`
- [x] write test `test_build_docker_command_basic` - verify command structure includes base env vars and correct order
- [x] write test `test_build_docker_command_with_serve` - verify port binding AND `RALPHEX_WEB_HOST=0.0.0.0` env var injection
- [x] write test `test_build_docker_command_interactive` - verify `-it` flag when stdin is tty
- [x] run tests - must pass before next task

### Task 2: Add --dry-run flag and output logic

**Files:**
- Modify: `scripts/ralphex-dk.sh`
- Modify: `scripts/ralphex-dk/ralphex_dk_test.py`

- [x] add `shlex` import at top of file
- [x] add `--dry-run` flag to `build_parser()` with help text
- [x] create helper `detect_inherited_env_vars(extra_env: list[str]) -> list[str]` that extracts var names without `=value`
- [x] in `main()`, after volumes/env assembled and before `run_docker()` call:
  - check `parsed.dry_run`
  - call `build_docker_command()` to get command list
  - call `detect_inherited_env_vars()` on extra_env
  - print warning to stderr if inherited env vars found (for copy-paste scenario)
  - print `shlex.join(cmd)` to stdout
  - return 0
- [x] update test imports to include `detect_inherited_env_vars`
- [x] write test `test_detect_inherited_env_vars` - verify extraction logic
- [x] write test `test_dry_run_output_format` - verify `shlex.join()` produces valid shell command
- [x] write test `test_dry_run_inherited_env_warning` - verify warning printed for inherited vars
- [x] write test `test_dry_run_no_warning_explicit_values` - verify no warning when all vars have values
- [x] run tests - must pass before next task

### Task 3: Update documentation

**Files:**
- Modify: `scripts/ralphex-dk.sh` (docstring)
- Modify: `llms.txt`

- [ ] add `--dry-run` to docstring wrapper flags section (around line 13)
- [ ] add `--dry-run` usage example to docstring
- [ ] update `llms.txt` Docker wrapper section with `--dry-run` flag
- [ ] verify help output: `python3 scripts/ralphex-dk.sh --help | grep dry-run`

### Task 4: Final verification

**Files:**
- None (verification only)

- [ ] run full test suite: `python3 scripts/ralphex-dk.sh --test`
- [ ] manual test: `python3 scripts/ralphex-dk.sh --dry-run` prints command
- [ ] manual test: `python3 scripts/ralphex-dk.sh --dry-run -E FOO` shows warning about inherited var
- [ ] manual test: `python3 scripts/ralphex-dk.sh --dry-run -E FOO=bar` shows no warning
- [ ] move this plan to `docs/plans/completed/`

## Technical Details

**Command building signature:**
```python
def build_docker_command(
    image: str,
    port: str,
    volumes: list[str],
    env_vars: list[str],
    bind_port: bool,
    args: list[str]
) -> list[str]:
```

**Inherited env var detection:**
```python
# extra_env format: ["-e", "VAR=val", "-e", "VAR2", ...]
# inherited vars are those without "=" in the value following "-e"
inherited = []
for i, item in enumerate(extra_env):
    if item == "-e" and i + 1 < len(extra_env):
        entry = extra_env[i + 1]
        if "=" not in entry:
            inherited.append(entry)
```

**Warning message:**
```
note: inherited env vars (VAR1, VAR2) require these variables to be set in your shell when running the command
```

## Post-Completion

**Manual verification:**
- test with actual Docker to verify command is valid and runnable
- test with bedrock provider: verify `CLAUDE_CODE_USE_BEDROCK=1`, `AWS_REGION`, and credential env vars appear in dry-run output
