# Notifications

ralphex can send notifications when plan execution completes (success or failure). Notifications are optional, disabled by default, and best-effort - failures are logged but never affect the exit code.

## Quick setup

1. Add channels to your config (`~/.config/ralphex/config` or `.ralphex/config`):

```ini
notify_channels = telegram
notify_telegram_token = 123456:ABC-DEF
notify_telegram_chat = -1001234567890
```

2. Run ralphex as usual. A notification fires after execution finishes.

## General settings

```ini
# comma-separated list of channels: telegram, email, slack, webhook, custom
notify_channels = telegram, webhook

# send notification on failure (default: true)
notify_on_error = true

# send notification on success (default: true)
notify_on_complete = true

# total timeout for all notification channels in milliseconds (default: 10000)
notify_timeout_ms = 10000
```

Setting `notify_channels` to empty (or omitting it) disables notifications entirely. All channel-specific settings are ignored unless the corresponding channel is listed in `notify_channels`.

## Channels

### Telegram

Create a bot and get a chat ID:

1. Message [@BotFather](https://t.me/BotFather) on Telegram and create a new bot with `/newbot`
2. Copy the bot token (looks like `123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11`)
3. Add the bot to your channel or group
4. Get the chat ID - send a message in the chat, then visit `https://api.telegram.org/bot<TOKEN>/getUpdates` and look for `"chat":{"id":...}`
5. For public channels, use the channel name with `@` prefix (e.g., `@mychannel`)

Config:

```ini
notify_channels = telegram
notify_telegram_token = 123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11
notify_telegram_chat = -1001234567890
```

The chat value can be a numeric chat ID or a channel name like `@mychannel`.

### Email (SMTP)

Config for Gmail with app password:

```ini
notify_channels = email
notify_smtp_host = smtp.gmail.com
notify_smtp_port = 587
notify_smtp_username = you@gmail.com
notify_smtp_password = your-app-password
notify_smtp_starttls = true
notify_email_from = you@gmail.com
notify_email_to = you@gmail.com, team@example.com
```

For Gmail, use an [app password](https://support.google.com/accounts/answer/185833) (not your regular password). Enable 2FA first, then generate an app password under Security > App passwords.

Generic SMTP:

```ini
notify_channels = email
notify_smtp_host = mail.example.com
notify_smtp_port = 587
notify_smtp_username = user@example.com
notify_smtp_password = password
notify_email_from = ralphex@example.com
notify_email_to = dev@example.com
```

Multiple recipients are comma-separated in `notify_email_to`. Set `notify_smtp_port` explicitly (587 is typical for STARTTLS).

### Slack

Create a Slack bot and get a token:

1. Go to [api.slack.com/apps](https://api.slack.com/apps) and create a new app
2. Under OAuth & Permissions, add the `chat:write` scope
3. Install the app to your workspace
4. Copy the Bot User OAuth Token (starts with `xoxb-`)
5. Invite the bot to the target channel (`/invite @yourbot`)

Config:

```ini
notify_channels = slack
notify_slack_token = xoxb-your-bot-token
notify_slack_channel = general
```

The channel value is the channel name (without `#`) or channel ID.

### Webhook

ralphex sends the notification message as a plain text POST to each webhook URL.

Config:

```ini
notify_channels = webhook
notify_webhook_urls = https://hooks.example.com/notify, https://other.example.com/webhook
```

Multiple URLs are comma-separated. Each URL receives the notification independently.

### Custom script

A custom script receives the full `Result` JSON on stdin and is expected to handle delivery itself. This lets you integrate with any notification service.

Config:

```ini
notify_channels = custom
notify_custom_script = ~/.config/ralphex/scripts/notify.sh
```

The script:
- Receives `Result` JSON on stdin
- Exit code 0 = success, non-zero = failure (logged as warning)
- Timeout controlled by `notify_timeout_ms`

JSON schema piped to stdin:

```json
{
  "status": "success",
  "mode": "full",
  "plan_file": "docs/plans/add-auth.md",
  "branch": "add-auth",
  "duration": "12m 34s",
  "files": 8,
  "additions": 142,
  "deletions": 23
}
```

The `error` field is present only on failure (omitted on success).

Example script:

```bash
#!/bin/bash
# read JSON from stdin and send to a custom endpoint
jq -c '.' | curl -s -X POST -H "Content-Type: application/json" -d @- https://hooks.example.com/ralphex
```

Example script using ntfy.sh:

```bash
#!/bin/bash
# read JSON from stdin, extract status and plan, send to ntfy.sh
DATA=$(cat)
STATUS=$(echo "$DATA" | jq -r '.status')
PLAN=$(echo "$DATA" | jq -r '.plan_file')
curl -s -d "ralphex ${STATUS}: ${PLAN}" ntfy.sh/my-ralphex-topic
```

## Using multiple channels

Channels can be combined freely:

```ini
notify_channels = telegram, webhook, custom
notify_telegram_token = 123456:ABC-DEF
notify_telegram_chat = -1001234567890
notify_webhook_urls = https://hooks.example.com/notify
notify_custom_script = ~/.config/ralphex/scripts/notify.sh
```

Each channel is independent - if one fails, others still fire.

## Complete config example

```ini
# enable telegram and email notifications
notify_channels = telegram, email

# send on both success and failure
notify_on_error = true
notify_on_complete = true

# 15 second total timeout for all channels
notify_timeout_ms = 15000

# telegram
notify_telegram_token = 123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11
notify_telegram_chat = -1001234567890

# email via gmail
notify_smtp_host = smtp.gmail.com
notify_smtp_port = 587
notify_smtp_username = you@gmail.com
notify_smtp_password = your-app-password
notify_smtp_starttls = true
notify_email_from = you@gmail.com
notify_email_to = you@gmail.com
```

## Message format

Notifications use a plain text format.

Success:

```
ralphex completed on myhost

plan:     docs/plans/add-auth.md
branch:   add-auth
mode:     full
duration: 12m 34s
changes:  8 files (+142/-23 lines)
```

Failure:

```
ralphex failed on myhost

plan:     docs/plans/add-auth.md
branch:   add-auth
mode:     full
duration: 5m 12s
error:    runner: task phase: max iterations reached
```

The custom script channel receives structured JSON instead of this text format (see [custom script](#custom-script) section above).

## Notes

- Notifications are best-effort. Delivery failures are logged as warnings but never cause ralphex to fail or change its exit code.
- Misconfigured channels (missing required fields) are detected at startup and cause an immediate error. However, channels that require a live API call during initialization (e.g., Telegram's bot token verification) are gracefully skipped with a warning if the call fails, since notifications are best-effort.
- Telegram initialization verifies the bot token via a synchronous API call (up to 30s timeout). If the API is unreachable or the token is invalid, the channel is disabled with a warning. Note that this verification blocks startup for the duration of the attempt.
- The hostname in the message is resolved once at startup. If resolution fails, "unknown" is used.
- Notifications are not sent in plan creation mode (`--plan`). If plan creation transitions to execution, the notification fires after execution completes.
- Built-in channels (telegram, email, slack, webhook) use [go-pkgz/notify](https://github.com/go-pkgz/notify) under the hood. Refer to that library for advanced channel-specific behavior.
