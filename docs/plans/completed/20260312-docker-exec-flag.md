# Add --exec Flag to Docker Wrapper

## Overview
- Add `--exec CMD` flag to `scripts/ralphex-dk.sh` that runs arbitrary command instead of ralphex
- Enables troubleshooting custom containers by launching shell or running specific tools
- Examples: `--exec bash`, `--exec "claude --help"`, `--exec "go version"`

## Context (from brainstorm)
- File: `scripts/ralphex-dk.sh` (Python script)
- Parser uses argparse with `parse_known_args()` for wrapper vs ralphex arg separation
- `run_docker()` builds docker command with `/srv/ralphex` as entrypoint
- Auth check kept (per user preference), port binding skipped for --exec

## Development Approach
- **Testing approach**: TDD - write tests first, then implementation
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task**
- Run tests after each change: `python3 scripts/ralphex-dk.sh --test`

## Testing Strategy
- **Unit tests**: embedded in script (existing pattern), run via `--test` flag
- Tests use `unittest.mock` for subprocess and function mocking
- Follow existing test class patterns: `TestBuildParser`, `TestMainArgparse`
- For `run_docker()` tests: mock `subprocess.Popen` to capture constructed command
- For `main()` flow tests: mock `run_docker` to capture its arguments (existing pattern in `TestMainArgparse`)

## Progress Tracking
- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix

## Implementation Steps

### Task 1: Add parser tests for --exec flag

**Files:**
- Modify: `scripts/ralphex-dk.sh` (test classes section)

- [x] add `test_exec_flag_parsed` - verify `--exec bash` sets `exec_cmd="bash"`
- [x] add `test_exec_flag_with_quoted_args` - verify `--exec "bash -l"` preserved as single string
- [x] add `test_exec_with_env_and_volume` - verify `-E FOO=bar -v /a:/b --exec bash` all parsed correctly
- [x] add `test_exec_ignores_ralphex_args` - verify `--exec bash plan.md` has `exec_cmd="bash"` and `plan.md` in unknown
- [x] run tests - expect 4 failures (--exec not implemented yet)

### Task 2: Implement parser changes

**Files:**
- Modify: `scripts/ralphex-dk.sh` (build_parser function)

- [x] add `parser.add_argument("--exec", dest="exec_cmd", metavar="CMD", help="run CMD instead of ralphex (e.g., --exec bash)")`
- [x] run tests - all 4 new parser tests should pass

### Task 3: Add run_docker tests for exec_cmd parameter

**Files:**
- Modify: `scripts/ralphex-dk.sh` (test classes section, add new `TestRunDockerExec` class)

- [x] create `TestRunDockerExec` test class (mock `subprocess.Popen` to capture cmd)
- [x] add `test_exec_cmd_replaces_ralphex` - verify `/srv/ralphex` not in cmd, shlex-split exec_cmd is
- [x] add `test_exec_cmd_shlex_splits_args` - verify `"bash -l"` becomes `["bash", "-l"]`
- [x] add `test_exec_cmd_none_uses_ralphex` - verify default behavior unchanged when exec_cmd=None
- [x] add `test_exec_cmd_nested_quotes` - verify `"echo 'hello world'"` splits correctly
- [x] run tests - expect failures (exec_cmd param not implemented yet)

### Task 4: Implement run_docker changes

**Files:**
- Modify: `scripts/ralphex-dk.sh` (run_docker function)

- [x] add `exec_cmd: str | None = None` parameter to `run_docker()` signature
- [x] add `import shlex` if not already present
- [x] modify command building: if exec_cmd, use `[image] + shlex.split(exec_cmd)` instead of `[image, "/srv/ralphex"] + args`
- [x] run tests - all run_docker tests should pass

### Task 5: Add main() flow tests

**Files:**
- Modify: `scripts/ralphex-dk.sh` (test classes section, add `TestExecFlag` class)

- [x] create `TestExecFlag` test class (mock `run_docker` to capture arguments)
- [x] add `test_exec_skips_port_binding` - verify `--serve --exec bash` passes `bind_port=False`
- [x] add `test_exec_passes_exec_cmd_to_run_docker` - verify `exec_cmd` param passed through
- [x] add `test_exec_ignores_ralphex_args` - verify `--exec bash plan.md` passes `args=[]` and `exec_cmd="bash"`
- [x] run tests - expect failures until main() implementation

### Task 6: Implement main() flow changes

**Files:**
- Modify: `scripts/ralphex-dk.sh` (main function, ~line 991)

- [x] add exec_cmd handling after provider check, before normal run_docker call
- [x] when `parsed.exec_cmd` is set: call `run_docker(..., bind_port=False, args=[], exec_cmd=parsed.exec_cmd)`
- [x] update `run_docker()` call at line 991 to pass `exec_cmd=None` (explicit default)
- [x] run tests - all tests should pass

### Task 7: Update documentation

**Files:**
- Modify: `scripts/ralphex-dk.sh` (usage docstring lines 7-35)
- Modify: `llms.txt` (Docker Images section, lines ~119-185)
- Modify: `CLAUDE.md` (Docker Wrapper section)

- [x] add `--exec CMD` to usage block at top of script (after `-v, --volume` line ~11)
- [x] add `--exec CMD` to llms.txt "Using Docker wrapper" examples and environment vars table
- [x] add `--exec CMD` to CLAUDE.md "AWS Bedrock Provider (Docker Wrapper Only)" section or create subsection
- [x] run tests - verify no regressions

### Task 8: Verify acceptance criteria

- [x] verify `--exec bash` would launch shell (manual test or mock verification)
- [x] verify `--exec "bash -l"` correctly splits command
- [x] verify `-E`/`-v` flags work with `--exec`
- [x] verify auth check still required
- [x] run full test suite: `python3 scripts/ralphex-dk.sh --test`

### Task 9: [Final] Complete plan

- [x] move this plan to `docs/plans/completed/`

## Technical Details

**Parser addition:**
```python
parser.add_argument("--exec", dest="exec_cmd", metavar="CMD",
                    help="run CMD instead of ralphex (e.g., --exec bash)")
```

**run_docker signature change:**
```python
def run_docker(image: str, port: str, volumes: list[str], env_vars: list[str],
               bind_port: bool, args: list[str], exec_cmd: str | None = None) -> int:
```

**Command building logic:**
```python
if exec_cmd:
    cmd.extend([image] + shlex.split(exec_cmd))
else:
    cmd.extend([image, "/srv/ralphex"])
    cmd.extend(args)
```

## Post-Completion

**Manual verification:**
- Test with actual Docker: `ralphex --exec bash` launches interactive shell
- Test with actual Docker: `ralphex --exec "go version"` prints Go version
- Test combined flags: `ralphex -E DEBUG=1 --exec bash` has DEBUG in environment
