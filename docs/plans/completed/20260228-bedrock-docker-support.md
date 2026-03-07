# AWS Bedrock Support for Docker Wrapper

## Overview
- Add AWS Bedrock authentication support to the `ralphex-dk.sh` Docker wrapper
- Enable users to run ralphex in Docker with AWS Bedrock-hosted Claude models
- Support profile-based auth (via `aws configure export-credentials`) and explicit credentials
- **Security:** Never mount `~/.aws` - only export specific credentials needed

## Context (from discovery)
- files/components involved: `scripts/ralphex-dk.sh` (Python, ~1960 lines)
- related patterns found: `RALPHEX_EXTRA_VOLUMES` env var, `RALPHEX_EXTRA_ENV` env var, argparse CLI with `-E/--env` and `-v/--volume` flags
- dependencies identified: AWS SDK credential chain, Claude Code Bedrock integration

## Already Implemented (merged to master)
- **RALPHEX_EXTRA_ENV support** (PR #179): `build_env_vars()`, `validate_env_entry()`, `merge_env_flags()`, `is_sensitive_name()` with comprehensive tests
- **argparse CLI parsing** (PR #183): `-E/--env` and `-v/--volume` flags with `build_parser()`, pass-through of unknown args to ralphex

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

### Task 1: Add --claude-provider CLI flag

**Files:**
- Modify: `scripts/ralphex-dk.sh`

- [x] add `--claude-provider` flag to argparse (values: `default`, `bedrock`; default: `default`)
- [x] add `RALPHEX_CLAUDE_PROVIDER` env var as fallback (CLI flag takes precedence)
- [x] add `BEDROCK_ENV_VARS` constant with list of AWS/Bedrock-related env vars to passthrough
- [x] add `get_claude_provider()` function returning provider from CLI or env var
- [x] add `build_bedrock_env_args()` function to pass BEDROCK_ENV_VARS when provider is `bedrock`
- [x] integrate into `run_docker()` alongside extra env vars
- [x] write `TestClaudeProvider` test class with cases:
  - `test_default_provider_no_bedrock_env` - no flag, no env → provider is "default", no AWS vars
  - `test_cli_flag_bedrock` - `--claude-provider bedrock` → provider is "bedrock"
  - `test_env_var_fallback` - no flag, `RALPHEX_CLAUDE_PROVIDER=bedrock` → provider is "bedrock"
  - `test_cli_overrides_env` - flag and env var set → CLI wins
  - `test_bedrock_passes_set_vars` - only passes BEDROCK_ENV_VARS that are actually set
  - `test_invalid_provider_rejected` - unknown provider value → error
- [x] run tests - must pass before next task

### Task 2: Add AWS profile credential export for Bedrock

**Files:**
- Modify: `scripts/ralphex-dk.sh`

**Security note:** Never mount `~/.aws` directory - it may contain sensitive data beyond credentials.
Instead, use `aws configure export-credentials` to export only the needed credentials.

- [x] add `export_aws_profile_credentials()` function:
  - check if `aws` CLI is available (`shutil.which("aws")`); if not, log warning and return empty dict
  - check if `AWS_PROFILE` is set and explicit creds (`AWS_ACCESS_KEY_ID`) are NOT set
  - run `aws configure export-credentials --profile $AWS_PROFILE --format json`
  - parse JSON output to extract `AccessKeyId`, `SecretAccessKey`, `SessionToken`
  - return dict with env var names (`AWS_ACCESS_KEY_ID`, etc.) as keys
  - handle command failure gracefully (return empty dict, log warning)
- [x] integrate into `run_docker()` - add exported creds to env args when bedrock enabled
- [x] write `TestAwsCredentialExport` test class with cases:
  - `test_exports_credentials_with_profile` - AWS_PROFILE set → runs aws cli, parses JSON output
  - `test_skips_export_when_explicit_creds` - AWS_ACCESS_KEY_ID set → no aws cli call
  - `test_skips_export_when_no_profile` - AWS_PROFILE not set → no aws cli call
  - `test_handles_export_failure` - aws cli fails → empty dict, no crash
  - `test_handles_missing_aws_cli` - aws CLI not installed → empty dict, warning logged
  - `test_parses_json_output` - correctly extracts AccessKeyId/SecretAccessKey/SessionToken from JSON
- [x] run tests - must pass before next task

### Task 3: Skip keychain and claude_home checks for Bedrock

**Files:**
- Modify: `scripts/ralphex-dk.sh`

- [x] modify `main()` to skip `extract_macos_credentials()` when provider is `bedrock`
- [x] modify `main()` to skip `claude_home.is_dir()` check when provider is `bedrock`
- [x] add startup message indicating bedrock mode and keychain skip
- [x] write `TestBedrockSkipKeychain` test class with cases:
  - `test_skips_credentials_extraction_when_bedrock` - creds_temp is None
  - `test_skips_claude_home_check_when_bedrock` - no error if ~/.claude missing
  - `test_normal_mode_still_extracts_credentials` - backwards compat with default provider
  - `test_startup_message_shows_bedrock_mode` - output includes "bedrock" and "keychain skipped"
- [x] run tests - must pass before next task

### Task 4: Add validation and user feedback

**Files:**
- Modify: `scripts/ralphex-dk.sh`

- [x] add `validate_bedrock_config()` function returning list of warning strings (only called when provider is `bedrock`)
- [x] check: CLAUDE_CODE_USE_BEDROCK set (warn if not - required for Claude Code inside container)
- [x] check: AWS_REGION set (warn if not)
- [x] check: AWS_PROFILE set OR AWS_ACCESS_KEY_ID set (warn if neither)
- [x] call `validate_bedrock_config()` in `main()` after startup message, print warnings before `run_docker()`
- [x] print provider mode and passed env vars on startup
- [x] write `TestBedrockValidation` test class with cases:
  - `test_warns_missing_claude_code_use_bedrock`
  - `test_warns_missing_aws_region`
  - `test_warns_no_credentials_found`
  - `test_no_warning_with_profile`
  - `test_no_warning_with_explicit_creds`
- [x] run tests - must pass before next task

### Task 5: Verify acceptance criteria

- [x] verify `--claude-provider bedrock` enables bedrock mode
- [x] verify `RALPHEX_CLAUDE_PROVIDER=bedrock` env var works as fallback
- [x] verify credentials exported via `aws configure export-credentials` when profile set
- [x] verify explicit creds (AWS_ACCESS_KEY_ID) skip credential export
- [x] verify keychain skipped in bedrock mode
- [x] verify backwards compatibility (default provider mode unchanged)
- [x] run full test suite: `python3 scripts/ralphex-dk.sh --test`

### Task 6: [Final] Update documentation

- [x] create `docs/bedrock-setup.md` with:
  - security best practices (separate profile, minimal permissions)
  - example IAM policy for Bedrock access (see Technical Details)
  - step-by-step setup instructions for SSO and IAM user scenarios
  - troubleshooting common auth errors
- [x] update llms.txt with new flag (`--claude-provider`) and env var (`RALPHEX_CLAUDE_PROVIDER`)
- [x] add example usage for Bedrock mode
- [x] move this plan to `docs/plans/completed/`

## Technical Details

### Security: Minimal IAM Policy for Bedrock

Create a dedicated AWS profile with only the permissions needed for Claude via Bedrock.
This follows the principle of least privilege.

**Minimal IAM policy (foundation models + inference profiles):**
```json
{
    "Version": "2012-10-17",
    "Statement": [
        {
            "Sid": "BedrockInvokeFoundationModels",
            "Effect": "Allow",
            "Action": [
                "bedrock:InvokeModel",
                "bedrock:InvokeModelWithResponseStream"
            ],
            "Resource": [
                "arn:aws:bedrock:*::foundation-model/anthropic.claude-*"
            ]
        },
        {
            "Sid": "BedrockInvokeInferenceProfiles",
            "Effect": "Allow",
            "Action": [
                "bedrock:InvokeModel",
                "bedrock:InvokeModelWithResponseStream"
            ],
            "Resource": [
                "arn:aws:bedrock:*:*:inference-profile/*anthropic.claude*"
            ]
        }
    ]
}
```

**More restrictive (specific region, models, and inference profiles):**
```json
{
    "Version": "2012-10-17",
    "Statement": [
        {
            "Sid": "BedrockInvokeClaudeFoundationModels",
            "Effect": "Allow",
            "Action": [
                "bedrock:InvokeModel",
                "bedrock:InvokeModelWithResponseStream"
            ],
            "Resource": [
                "arn:aws:bedrock:us-east-1::foundation-model/anthropic.claude-sonnet-4-20250514-v1:0",
                "arn:aws:bedrock:us-east-1::foundation-model/anthropic.claude-haiku-4-20250514-v1:0"
            ]
        },
        {
            "Sid": "BedrockInvokeClaudeInferenceProfiles",
            "Effect": "Allow",
            "Action": [
                "bedrock:InvokeModel",
                "bedrock:InvokeModelWithResponseStream"
            ],
            "Resource": [
                "arn:aws:bedrock:us-east-1:*:inference-profile/us.anthropic.claude-sonnet-4-20250514-v1:0",
                "arn:aws:bedrock:us-east-1:*:inference-profile/us.anthropic.claude-haiku-4-20250514-v1:0"
            ]
        }
    ]
}
```

**Note:** Inference profiles use cross-region prefixes (e.g., `us.anthropic.claude-*` for US regions).
Check your Bedrock console for exact inference profile IDs available in your account.

**Setup options:**

1. **SSO with dedicated permission set:**
   ```bash
   # create permission set in AWS IAM Identity Center with above policy
   # assign to user/group for specific account
   aws configure sso --profile ralphex-bedrock
   ```

2. **IAM user with dedicated policy:**
   ```bash
   # create IAM user with above policy attached
   aws configure --profile ralphex-bedrock
   # enter access key ID and secret from IAM user
   ```

**BEDROCK_ENV_VARS list:**
```python
BEDROCK_ENV_VARS = [
    # core bedrock config (user must set CLAUDE_CODE_USE_BEDROCK=1 on host)
    "CLAUDE_CODE_USE_BEDROCK",
    "AWS_REGION",
    # explicit credentials (exported from profile or set directly by user)
    # NOTE: AWS_PROFILE is NOT in this list - it requires ~/.aws/config which
    # we don't mount. Profile is used on host only to export temp credentials.
    "AWS_ACCESS_KEY_ID",
    "AWS_SECRET_ACCESS_KEY",
    "AWS_SESSION_TOKEN",
    # bedrock API key auth
    "AWS_BEARER_TOKEN_BEDROCK",
    # model configuration (for inference profiles, custom model ARNs)
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

**Example usage (recommended - dedicated profile with minimal permissions):**
```bash
# set required env vars for Claude Code inside container
export CLAUDE_CODE_USE_BEDROCK=1
export AWS_PROFILE=ralphex-bedrock
export AWS_REGION=us-east-1

# login if using SSO profile
aws sso login --profile=ralphex-bedrock

# run with bedrock provider
ralphex --claude-provider bedrock docs/plans/feature.md

# or use env var for session-wide setting
export RALPHEX_CLAUDE_PROVIDER=bedrock
ralphex docs/plans/feature.md
```

**Example usage (explicit credentials):**
```bash
export CLAUDE_CODE_USE_BEDROCK=1
export AWS_REGION=us-east-1
export AWS_ACCESS_KEY_ID=AKIA...
export AWS_SECRET_ACCESS_KEY=...

ralphex --claude-provider bedrock docs/plans/feature.md
```

**Example usage (with extra env vars):**
```bash
export CLAUDE_CODE_USE_BEDROCK=1
export AWS_PROFILE=ralphex-bedrock
export AWS_REGION=us-east-1
export CLAUDE_CODE_MAX_OUTPUT_TOKENS=32000

# pass extra env vars to container
ralphex --claude-provider bedrock -E CLAUDE_CODE_MAX_OUTPUT_TOKENS docs/plans/feature.md
```

**Startup output (bedrock mode with profile):**
```
using image: ghcr.io/umputun/ralphex-go:latest
claude provider: bedrock (keychain skipped)
  exporting credentials from profile: my-sso-profile
  passing: AWS_REGION, AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_SESSION_TOKEN, CLAUDE_CODE_USE_BEDROCK
```

**Startup output (bedrock mode with explicit creds):**
```
using image: ghcr.io/umputun/ralphex-go:latest
claude provider: bedrock (keychain skipped)
  using explicit credentials
  passing: AWS_REGION, AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, CLAUDE_CODE_USE_BEDROCK
```

## Post-Completion

**Manual verification:**
- test with AWS SSO profile (verify `aws configure export-credentials` works)
- test with explicit AWS credentials (verify credential export is skipped)
- test on Linux without ~/.claude directory
- verify Claude Code connects to Bedrock inside container
- verify ~/.aws is NOT mounted (security requirement)
