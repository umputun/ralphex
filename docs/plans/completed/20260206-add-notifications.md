# Add Notification System

## Overview

Add optional notification support that fires after plan execution completes (success or failure). Uses `go-pkgz/notify` library for built-in channels (telegram, email, slack, webhook) and supports custom scripts as a first-class channel type.

Notifications are best-effort — failures are logged but never affect the exit code. Disabled by default; users opt in by setting `notify_channels` in config.

## Context (from discovery)

- Files/components involved: `cmd/ralphex/main.go` (wiring), `pkg/config/` (config fields), `pkg/notify/` (new package)
- Related patterns found: finalize step in `pkg/processor/runner.go` — same best-effort, timeout-protected approach
- Dependencies: `github.com/go-pkgz/notify` — Notifier interface, `Send()` router, Email/Telegram/Slack/Webhook implementations
- Reference: `umputun/cronn` wraps `go-pkgz/notify` with a `Service` struct — similar approach but simpler here

## Development Approach

- **Testing approach**: regular (code first, then tests)
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task** — no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- Run `make test && make lint` after each task
- Maintain backward compatibility

## Testing Strategy

- **Unit tests**: required for every task (see Development Approach above)
- Test both success and error scenarios
- Mock `go-pkgz/notify` notifiers in unit tests — don't require real telegram/smtp/slack
- Custom script tests use test helper scripts in testdata
- **E2E tests**: not applicable — notification is fire-and-forget, verified manually with toy project

## Progress Tracking

- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix
- Update plan if implementation deviates from original scope

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code changes, tests, documentation updates
- **Post-Completion** (no checkboxes): manual testing with real channels, toy project verification

## Implementation Steps

### Task 1: Add notification config fields to Values and Config

**Files:**
- Modify: `pkg/config/values.go`
- Modify: `pkg/config/config.go`
- Modify: `pkg/config/values_test.go`
- Modify: `pkg/config/config_test.go`

Config fields to add:

```ini
notify_channels            # comma-separated: telegram, email, webhook, slack, custom
notify_on_error = true     # bool, *Set pattern
notify_on_complete = true  # bool, *Set pattern
notify_timeout_ms = 10000  # int, *Set pattern
notify_telegram_token      # string
notify_telegram_chat       # string (chat ID or channel name)
notify_slack_token         # string
notify_slack_channel       # string
notify_smtp_host           # string
notify_smtp_port           # int, *Set pattern
notify_smtp_username       # string
notify_smtp_password       # string
notify_smtp_starttls       # bool, *Set pattern
notify_email_from          # string
notify_email_to            # string (comma-separated)
notify_webhook_urls        # string (comma-separated)
notify_custom_script       # string (path, tilde-expanded)
```

- [x] Add fields to `Values` struct with appropriate `*Set` bools for bool/int scalars
- [x] Extract notification parsing into `parseNotifyValues()` helper called from `parseValuesFromBytes()` to manage cyclomatic complexity
- [x] Extract notification merge logic into `mergeNotifyFrom()` helper called from `mergeFrom()`
- [x] Add `NotifyParams notify.Params` field directly to `Config` struct (embedded, not a separate mapping method)
- [x] Populate `NotifyParams` during `loadConfigFromDirs()` assembly — no separate `NotifyConfig()` method needed
- [x] Write tests in `pkg/config/values_test.go` for parsing all new fields (success + error cases)
- [x] Write tests in `pkg/config/config_test.go` for merge behavior of new fields
- [x] Run `make test && make lint`

### Task 2: Add embedded config template entries

**Files:**
- Modify: `pkg/config/defaults/config`

- [x] Add commented-out notification section between "error pattern detection" and "output colors" sections
- [x] Include all `notify_*` fields with descriptions and defaults, all commented out (`# ` prefix)
- [x] Run `make test` to verify embedded defaults still load correctly

### Task 3: Create `pkg/notify/` package — core Service

**Files:**
- Create: `pkg/notify/notify.go`
- Create: `pkg/notify/notify_test.go`

Types:

```go
// Params holds configuration for creating a notification Service.
// Embedded directly in Config struct — no intermediate mapping needed.
type Params struct {
    Channels      []string
    OnError       bool
    OnComplete    bool
    TimeoutMs     int
    TelegramToken string
    TelegramChat  string
    SlackToken    string
    SlackChannel  string
    SMTPHost      string
    SMTPPort      int
    SMTPUsername  string
    SMTPPassword  string
    SMTPStartTLS  bool
    EmailFrom     string
    EmailTo       []string
    WebhookURLs   []string
    CustomScript  string
}

// Service orchestrates sending notifications through configured channels.
type Service struct {
    channels   []channel          // paired notifier + destination
    custom     *customChannel     // optional custom script channel
    onError    bool
    onComplete bool
    timeoutMs  int
    hostname   string             // resolved once at creation via os.Hostname()
    log        logger
}

// channel pairs a notifier with its destination URI.
type channel struct {
    notifier notify.Notifier
    dest     string
}

// logger interface for dependency injection (compatible with progress.Colors).
type logger interface {
    Print(format string, args ...interface{})
}

// Result holds completion data for notifications.
type Result struct {
    Status    string `json:"status"`     // "success" or "failure"
    Mode      string `json:"mode"`
    PlanFile  string `json:"plan_file"`
    Branch    string `json:"branch"`
    Duration  string `json:"duration"`
    Files     int    `json:"files"`
    Additions int    `json:"additions"`
    Deletions int    `json:"deletions"`
    Error     string `json:"error,omitempty"`
}
```

Note: `Hostname` is owned by Service (resolved once in `New()` via `os.Hostname()`), not by Result. The `formatMessage()` method uses `s.hostname` directly.

- [x] Run `go get github.com/go-pkgz/notify@latest`
- [x] Implement `New(p Params, log logger) (*Service, error)` — validates required fields per channel, creates paired `channel` structs (notifier + dest). Returns `nil, nil` if no channels configured. Resolves hostname with fallback to "unknown"
- [x] Implement `Send(ctx context.Context, r Result)` — nil-safe on receiver, checks onError/onComplete, formats message, iterates `s.channels` sending to each. Errors logged via `s.log`, never returned
- [x] Implement `formatMessage(r Result) string` — plain text format using `s.hostname`, with plan, branch, mode, duration, changes, error
- [x] Write tests for `New()` — valid config, missing required fields per channel, empty channels returns nil
- [x] Write tests for `Send()` — success path, nil receiver no-op, onError/onComplete filtering, errors logged not returned
- [x] Write tests for `formatMessage()` — success message, failure message, missing optional fields
- [x] Run `make test && make lint`

### Task 4: Add custom script channel

**Files:**
- Create: `pkg/notify/custom.go`
- Create: `pkg/notify/custom_test.go`

```go
// customChannel runs a user script for notifications.
type customChannel struct {
    scriptPath string
    timeoutMs  int
}
```

- [x] Implement `newCustomChannel(scriptPath string, timeoutMs int) *customChannel`
- [x] Implement `send(ctx context.Context, r Result) error` — marshals Result to JSON, pipes to script stdin, waits for exit with timeout
- [x] Write tests using a test helper script to verify JSON is piped correctly
- [x] Write tests for timeout and non-zero exit code handling
- [x] Run `make test && make lint`

### Task 5: Wire notifications into main execution flow

**Files:**
- Modify: `cmd/ralphex/main.go`

- [x] In `run()`, after `config.Load()`, create notification service: `notifySvc, err := notify.New(cfg.NotifyParams, baseLog)`. Fail fast on config error. `nil` if no channels
- [x] Pass `notifySvc` through to `executePlan()` — add field to `executePlanRequest`
- [x] In `executePlan()`, after `r.Run(ctx)` returns, collect `Result` and call `notifySvc.Send(ctx, result)` for both success and failure paths (before error return and before plan move). Result fields map to existing locals: `Duration` from `baseLog.Elapsed()`, `Files/Additions/Deletions` from already-called `req.GitSvc.DiffStats()`, `Branch` from `getCurrentBranch()`, `Error` from `r.Run(ctx)` error
- [x] No notification for plan mode — plan creation is interactive/attended; if plan mode transitions to `executePlan()`, notification fires there naturally
- [x] Verify manually with toy project (`scripts/prep-toy-test.sh`)
- [x] Run `make test && make lint`

### Task 6: Create notifications documentation

**Files:**
- Create: `docs/notifications.md`

- [x] Write setup guide with sections for each channel:
  - Telegram: bot creation via BotFather, getting chat ID, config example
  - Email: SMTP setup (Gmail app passwords, generic SMTP), config example
  - Slack: bot token creation, channel permissions, config example
  - Webhook: endpoint format, what gets POSTed, config example
  - Custom script: JSON stdin schema, exit code contract, example script
- [x] Include complete config example showing all channels
- [x] Include message format examples (success and failure)
- [x] Reference `go-pkgz/notify` for advanced channel-specific options

### Task 7: Update README, CLAUDE.md, and verify

**Files:**
- Modify: `README.md`
- Modify: `CLAUDE.md`
- Modify: `llms.txt`

- [x] Add "Notifications" bullet to Features list in README
- [x] Add short "Notifications" section under Configuration in README with link to `docs/notifications.md`
- [x] Update CLAUDE.md Key Patterns section to mention notification support
- [x] Update CLAUDE.md Configuration section to mention `notify_*` config fields
- [x] Update llms.txt Customization section to mention notification channels
- [x] Verify edge cases: nil service, empty channels, misconfigured channels
- [x] Verify misconfigured channels fail fast at startup
- [x] Run full test suite: `make test`
- [x] Run linter: `make lint`
- [x] Verify test coverage for `pkg/notify/` is 80%+: `go test -cover ./pkg/notify/`

### Task 8: [Final] Move plan to completed

- [x] Move this plan to `docs/plans/completed/`

## Technical Details

**Message format (plain text):**

```
ralphex completed on <hostname>

plan:     docs/plans/add-auth.md
branch:   add-auth
mode:     full
duration: 12m 34s
changes:  8 files (+142/-23 lines)
```

Failure variant replaces last line with:
```
error:    runner: task phase: max iterations reached
```

**Destination URI construction (internal, built at Service creation time):**
- Telegram: `telegram:<chat>?parseMode=HTML`
- Slack: `slack:<channel>`
- Email: `mailto:<to>?from=<from>&subject=ralphex <status>`
- Webhook: raw URL as-is

**Custom script contract:**
- Receives `Result` JSON on stdin (different from custom review scripts which receive a file path as argument)
- Exit 0 = success, non-zero = failure (logged)
- Timeout controlled by `notify_timeout_ms`

**Nil safety:** `Service.Send()` is no-op on nil receiver. Callers don't need nil checks.

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes, informational only*

**Manual verification:**
- Test with real Telegram bot in a test channel
- Test webhook with a requestbin-style service
- End-to-end test with toy project (`scripts/prep-toy-test.sh`)
