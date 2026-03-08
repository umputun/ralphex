# AWS Bedrock Setup Guide

This guide explains how to use ralphex with Claude models hosted on AWS Bedrock.

## Overview

AWS Bedrock provides access to Claude models through your AWS account. This allows organizations to:
- Use Claude without a direct Anthropic API subscription
- Keep data within their AWS environment
- Leverage existing AWS IAM for access control

## Security Best Practices

**Never expose your primary AWS credentials.** Create a dedicated AWS profile with minimal permissions for ralphex:

1. Use a separate IAM user or SSO permission set
2. Apply least-privilege IAM policy (see below)
3. Use short-lived credentials when possible (SSO, STS)
4. ralphex never mounts `~/.aws` - it exports only the specific credentials needed

**Avoid passing secrets on the command line.** When using `-E` to pass environment variables:
- Prefer `-E VAR` (inherit form) over `-E VAR=value` for secrets
- Values in `-E VAR=value` are visible in `ps` output to other users on the system
- The inherit form passes the variable name only; Docker reads the value from the environment

## Minimal IAM Policy

Create an IAM policy with only the permissions required for Claude via Bedrock.

**Standard policy (all Claude models in all regions):**

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

**Restrictive policy (specific region and models):**

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

Note: Inference profiles use cross-region prefixes (e.g., `us.anthropic.claude-*` for US regions). Check your Bedrock console for exact inference profile IDs available in your account.

## Setup Instructions

### Option 1: AWS SSO (Recommended)

AWS SSO provides short-lived credentials and centralized access management.

1. Create a permission set in AWS IAM Identity Center with the policy above
2. Assign the permission set to your user/group for the target account
3. Configure a local profile:

```bash
aws configure sso --profile ralphex-bedrock
```

4. Login before using ralphex:

```bash
aws sso login --profile=ralphex-bedrock
```

5. Run ralphex with Bedrock:

```bash
export AWS_PROFILE=ralphex-bedrock
export AWS_REGION=us-east-1

ralphex --claude-provider bedrock docs/plans/feature.md
```

### Option 2: IAM User with Access Keys

For environments where SSO isn't available.

1. Create an IAM user with the policy above attached
2. Generate access keys for the user
3. Configure a local profile:

```bash
aws configure --profile ralphex-bedrock
# Enter access key ID and secret from IAM user
```

4. Run ralphex with Bedrock:

```bash
export AWS_PROFILE=ralphex-bedrock
export AWS_REGION=us-east-1

ralphex --claude-provider bedrock docs/plans/feature.md
```

### Option 3: Explicit Credentials

For CI/CD environments or when you have temporary credentials.

```bash
export AWS_REGION=us-east-1
export AWS_ACCESS_KEY_ID=AKIA...
export AWS_SECRET_ACCESS_KEY=...
# Optional: export AWS_SESSION_TOKEN=... (for temporary credentials)

ralphex --claude-provider bedrock docs/plans/feature.md
```

## Environment Variables

### Required

| Variable | Description |
|----------|-------------|
| `AWS_REGION` | AWS region where Bedrock is enabled (e.g., `us-east-1`) |

Note: `CLAUDE_CODE_USE_BEDROCK=1` is automatically set when using `--claude-provider bedrock` or `RALPHEX_CLAUDE_PROVIDER=bedrock`.

### Authentication (one of these is required)

| Variable | Description |
|----------|-------------|
| `AWS_PROFILE` | AWS CLI profile name (credentials exported automatically) |
| `AWS_ACCESS_KEY_ID` | Explicit access key (with `AWS_SECRET_ACCESS_KEY`) |

### ralphex Configuration

| Variable | Description |
|----------|-------------|
| `RALPHEX_CLAUDE_PROVIDER` | Set to `bedrock` to enable Bedrock mode (alternative to `--claude-provider` flag) |

### Optional Bedrock Configuration

| Variable | Description |
|----------|-------------|
| `ANTHROPIC_MODEL` | Override default Claude model |
| `ANTHROPIC_SMALL_FAST_MODEL` | Model for fast operations |
| `ANTHROPIC_DEFAULT_OPUS_MODEL` | Custom Opus model ARN |
| `ANTHROPIC_DEFAULT_SONNET_MODEL` | Custom Sonnet model ARN |
| `ANTHROPIC_DEFAULT_HAIKU_MODEL` | Custom Haiku model ARN |
| `ANTHROPIC_SMALL_FAST_MODEL_AWS_REGION` | AWS region for small/fast model |
| `ANTHROPIC_BEDROCK_BASE_URL` | Custom Bedrock endpoint |
| `AWS_BEARER_TOKEN_BEDROCK` | Bearer token for Bedrock API key auth |
| `CLAUDE_CODE_SKIP_BEDROCK_AUTH` | Skip Bedrock authentication (for testing) |
| `DISABLE_PROMPT_CACHING` | Set to disable prompt caching |

## Example Usage

### Basic usage with SSO profile

```bash
# Set required environment
export AWS_PROFILE=ralphex-bedrock
export AWS_REGION=us-east-1

# Login if needed
aws sso login --profile=ralphex-bedrock

# Run with Bedrock provider
ralphex --claude-provider bedrock docs/plans/feature.md
```

### Session-wide Bedrock mode

```bash
export AWS_PROFILE=ralphex-bedrock
export AWS_REGION=us-east-1
export RALPHEX_CLAUDE_PROVIDER=bedrock

# All ralphex commands now use Bedrock
ralphex docs/plans/feature.md
ralphex --review
```

### With extra environment variables

```bash
export AWS_PROFILE=ralphex-bedrock
export AWS_REGION=us-east-1
export CLAUDE_CODE_MAX_OUTPUT_TOKENS=32000

# Pass extra env vars to container
ralphex --claude-provider bedrock -E CLAUDE_CODE_MAX_OUTPUT_TOKENS docs/plans/feature.md
```

## Startup Output

When using Bedrock mode, ralphex shows the provider configuration:

**With profile-based credentials:**
```
using image: ghcr.io/umputun/ralphex-go:latest
claude provider: bedrock (keychain skipped)
  exporting credentials from profile: my-sso-profile
  passing: AWS_REGION, AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_SESSION_TOKEN, CLAUDE_CODE_USE_BEDROCK
```

**With explicit credentials:**
```
using image: ghcr.io/umputun/ralphex-go:latest
claude provider: bedrock (keychain skipped)
  using explicit credentials
  passing: AWS_REGION, AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, CLAUDE_CODE_USE_BEDROCK
```

## Troubleshooting

### "ExpiredToken" or "The security token included in the request is expired"

Your SSO session has expired. Re-authenticate:

```bash
aws sso login --profile=ralphex-bedrock
```

### "AccessDeniedException" or "User is not authorized to perform bedrock:InvokeModel"

Your IAM policy is missing required permissions. Verify:
1. The policy includes `bedrock:InvokeModel` and `bedrock:InvokeModelWithResponseStream`
2. The resource ARN matches the model you're trying to use
3. The policy is attached to your user/role

### "ResourceNotFoundException" or model not found

The specified model isn't available in your region or account:
1. Check that Claude models are enabled in your Bedrock console
2. Verify your `AWS_REGION` matches where models are enabled
3. For inference profiles, check the exact ARN in your Bedrock console

### "Could not find credentials" or authentication errors

ralphex couldn't export credentials from your profile:
1. Verify `aws configure export-credentials --profile YOUR_PROFILE` works manually
2. Check that you're logged in (for SSO profiles)
3. Try using explicit credentials as a workaround

### No `~/.claude` directory error on Linux

When using Bedrock mode, ralphex skips the Claude configuration directory check. If you see this error, ensure you're using `--claude-provider bedrock` flag.

### "aws CLI not found" warning

The `aws` CLI is not installed on your host. Install it to enable automatic credential export from profiles. Alternatively, use explicit credentials.
