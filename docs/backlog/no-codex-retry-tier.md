---
worth: maybe
where: pkg/config/defaults/config:262
added: 2026-08-28
---
# codex has no retry tier, so transient server hiccups need --wait

`claude_retry_patterns` exists because 529/502/503/504 are short-lived server hiccups rather than account
quota, so they auto-retry through the timeout path with a 5s backoff and no `--wait` (ec5d829, #377).
Codex has no equivalent: `CodexExecutor` carries `ErrorPatterns` and `LimitPatterns` only, and
`checkPatterns` has no retry tier to check first.

The consequence showed up in #446. `Selected model is at capacity` is OpenAI's overload message, exactly
the class #377 moved out of the limit tier for claude, but the only place to put it was
`codex_limit_patterns` (c8c949c), so recovery is gated behind `--wait` that most users do not set. Same
for anything else transient codex starts emitting.

Adding `codex_retry_patterns` is a new config option plus the plumbing to `CodexExecutor` and a fourth
branch in `checkPatterns`' priority chain, and it changes what an unattended codex run does by default.
`worth: maybe` because that default-behavior change is umputun's call, not a mechanical fix.
