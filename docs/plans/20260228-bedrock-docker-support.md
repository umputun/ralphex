# AWS Bedrock Support for Docker Wrapper

## Overview
- Add AWS Bedrock authentication support to the `ralphex-dk.sh` Docker wrapper
- Enable users to run ralphex in Docker with AWS Bedrock-hosted Claude models
- Support both profile-based (`~/.aws`) and explicit credential authentication
- Add generic `RALPHEX_EXTRA_ENV` for passing any additional env vars to container

## Context (from discovery)
- files/components involved: `scripts/ralphex-dk.sh` (Python, ~1077 lines)
- related patterns found: `RALPHEX_EXTRA_VOLUMES` env var for extra volume mounts
- dependencies identified: AWS SDK credential chain, Claude Code Bedrock integration

## Development Approach
- **testing approach**: Regular (code first, then tests)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task** - no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run tests after each change
- maintain backward compatibility

## Testing Strategy
- **unit tests**: embedded tests in `ralphex-dk.sh` (run via `--test` flag)
- test patterns: `unittest.TestCase` classes with mocking for filesystem/env

## Progress Tracking
- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope
- keep plan in sync with actual work done

## Implementation Steps

### Task 1: Add RALPHEX_EXTRA_ENV support

**Files:**
- Modify: `scripts/ralphex-dk.sh`

- [ ] add `get_extra_env_vars()` function to parse `RALPHEX_EXTRA_ENV` (comma-separated list)
- [ ] add `build_extra_env_args()` function to convert env var names to `-e VAR=val` docker args
- [ ] integrate into `run_docker()` to pass extra env vars to container
- [ ] write `TestExtraEnv` test class with cases:
  - `test_parses_comma_separated` - "VAR1,VAR2" → ["VAR1", "VAR2"]
  - `test_handles_whitespace` - " VAR1 , VAR2 " → ["VAR1", "VAR2"]
  - `test_empty_is_noop` - "" → []
  - `test_only_passes_set_vars` - VAR1 set, VAR2 not set → only VAR1 passed
- [ ] run tests via `python3 scripts/ralphex-dk.sh --test` - must pass before next task

### Task 2: Add RALPHEX_USE_BEDROCK activation

**Files:**
- Modify: `scripts/ralphex-dk.sh`

- [ ] add `BEDROCK_ENV_VARS` constant with list of AWS/Bedrock-related env vars to passthrough
- [ ] add `is_bedrock_enabled()` function checking `RALPHEX_USE_BEDROCK=1`
- [ ] add `build_bedrock_env_args()` function to pass BEDROCK_ENV_VARS when enabled
- [ ] integrate into `run_docker()` alongside extra env vars
- [ ] write `TestBedrockEnv` test class with cases:
  - `test_bedrock_disabled_no_env_passed` - RALPHEX_USE_BEDROCK not set → no AWS vars
  - `test_bedrock_enabled_passes_set_vars` - only passes vars that are set
  - `test_bedrock_env_list_complete` - verify BEDROCK_ENV_VARS contains expected vars
- [ ] run tests - must pass before next task

### Task 3: Add ~/.aws volume mount for Bedrock

**Files:**
- Modify: `scripts/ralphex-dk.sh`

- [ ] modify `build_volumes()` to accept `use_bedrock` parameter
- [ ] when bedrock enabled and `~/.aws` exists, mount to `/home/app/.aws:ro`
- [ ] handle SELinux `:z` suffix like other mounts
- [ ] update `build_volumes()` calls in `main()` to pass `use_bedrock`
- [ ] write `TestBedrockVolumes` test class with cases:
  - `test_mounts_aws_dir_when_bedrock_enabled` - ~/.aws mounted to /home/app/.aws:ro
  - `test_no_aws_mount_when_bedrock_disabled` - ~/.aws not mounted
  - `test_aws_mount_with_selinux` - mount has :ro,z suffix
  - `test_skips_if_aws_dir_missing` - no error if ~/.aws doesn't exist
- [ ] run tests - must pass before next task

### Task 4: Skip keychain and claude_home checks for Bedrock

**Files:**
- Modify: `scripts/ralphex-dk.sh`

- [ ] modify `main()` to skip `extract_macos_credentials()` when bedrock enabled
- [ ] modify `main()` to skip `claude_home.is_dir()` check when bedrock enabled
- [ ] add startup message indicating bedrock mode and keychain skip
- [ ] write `TestBedrockSkipKeychain` test class with cases:
  - `test_skips_credentials_extraction_when_bedrock` - creds_temp is None
  - `test_skips_claude_home_check_when_bedrock` - no error if ~/.claude missing
  - `test_normal_mode_still_extracts_credentials` - backwards compat
- [ ] run tests - must pass before next task

### Task 5: Add validation and user feedback

**Files:**
- Modify: `scripts/ralphex-dk.sh`

- [ ] add `validate_bedrock_config()` function returning list of warnings
- [ ] check: CLAUDE_CODE_USE_BEDROCK set (warn if not)
- [ ] check: AWS_REGION set (warn if not)
- [ ] check: ~/.aws exists OR AWS_ACCESS_KEY_ID set (warn if neither)
- [ ] print bedrock status and passed env vars on startup
- [ ] write `TestBedrockValidation` test class with cases:
  - `test_warns_missing_claude_code_use_bedrock`
  - `test_warns_missing_aws_region`
  - `test_warns_no_credentials_found`
  - `test_no_warning_with_aws_dir`
  - `test_no_warning_with_explicit_creds`
- [ ] run tests - must pass before next task

### Task 6: Verify acceptance criteria

- [ ] verify RALPHEX_USE_BEDROCK=1 enables bedrock mode
- [ ] verify ~/.aws mounted read-only when bedrock enabled
- [ ] verify RALPHEX_EXTRA_ENV passes arbitrary env vars
- [ ] verify keychain skipped in bedrock mode
- [ ] verify backwards compatibility (normal mode unchanged)
- [ ] run full test suite: `python3 scripts/ralphex-dk.sh --test`

### Task 7: [Final] Update documentation

- [ ] update llms.txt with new env vars (RALPHEX_USE_BEDROCK, RALPHEX_EXTRA_ENV)
- [ ] add example usage for Bedrock mode
- [ ] move this plan to `docs/plans/completed/`

## Technical Details

**BEDROCK_ENV_VARS list:**
```python
BEDROCK_ENV_VARS = [
    # core bedrock config
    "CLAUDE_CODE_USE_BEDROCK",
    "AWS_REGION",
    # profile-based auth
    "AWS_PROFILE",
    # explicit credentials
    "AWS_ACCESS_KEY_ID",
    "AWS_SECRET_ACCESS_KEY",
    "AWS_SESSION_TOKEN",
    # bedrock API key auth
    "AWS_BEARER_TOKEN_BEDROCK",
    # model configuration
    "ANTHROPIC_MODEL",
    "ANTHROPIC_SMALL_FAST_MODEL",
    "ANTHROPIC_DEFAULT_OPUS_MODEL",
    "ANTHROPIC_DEFAULT_SONNET_MODEL",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL",
    "ANTHROPIC_SMALL_FAST_MODEL_AWS_REGION",
    # optional
    "DISABLE_PROMPT_CACHING",
    "ANTHROPIC_BEDROCK_BASE_URL",
    "CLAUDE_CODE_SKIP_BEDROCK_AUTH",
]
```

**Example usage:**
```bash
export RALPHEX_USE_BEDROCK=1
export CLAUDE_CODE_USE_BEDROCK=1
export AWS_PROFILE=my-sso-profile
export AWS_REGION=us-east-1
export RALPHEX_EXTRA_ENV="CLAUDE_CODE_MAX_OUTPUT_TOKENS,MAX_THINKING_TOKENS"
export CLAUDE_CODE_MAX_OUTPUT_TOKENS=32000
export MAX_THINKING_TOKENS=10000

aws sso login --profile=my-sso-profile  # on host before starting
ralphex docs/plans/feature.md
```

**Startup output (bedrock mode):**
```
using image: ghcr.io/umputun/ralphex-go:latest
bedrock mode: enabled (keychain skipped)
  mounting: ~/.aws → /home/app/.aws (read-only)
  passing: AWS_REGION, AWS_PROFILE, CLAUDE_CODE_USE_BEDROCK
  extras: CLAUDE_CODE_MAX_OUTPUT_TOKENS, MAX_THINKING_TOKENS
```

## Post-Completion

**Manual verification:**
- test with actual AWS SSO profile on macOS
- test with explicit AWS credentials
- test on Linux without ~/.claude directory
- verify Claude Code connects to Bedrock inside container
