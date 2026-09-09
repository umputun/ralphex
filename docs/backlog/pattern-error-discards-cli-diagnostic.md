---
worth: maybe
where: pkg/executor/codex.go:386
added: 2026-08-28
---
# limit/error pattern match replaces the CLI's own message with the matched substring

`checkPatterns` returns `LimitPatternError{Pattern: pattern, HelpCmd: "codex /status"}` or
`PatternMatchError{...}` carrying only the substring that matched and a fixed help command. The line codex
actually printed is dropped: the caller never sees `Selected model is at capacity. Please try a different
model.`, only `Selected model is at capacity`, and the `codex /status` suggestion is wrong for a failure
that is not about the account. Before a pattern exists for a given failure, `finalError`
(`pkg/executor/codex.go:318-321`) reports the real stderr tail, so adding a pattern makes the message
*less* informative than not having one.

Affects every entry in `codex_limit_patterns` and `codex_error_patterns`, and the claude path has the same
shape. Pre-dates #446; surfaced reviewing the branch for it (PR #448), where the verifier judged it real
but out of scope for a defaults-only change.

`worth: maybe` because the fix is a design call rather than a mechanical one: either the error types carry
the matched line alongside the pattern, or the caller reports both, and either way the printed output for
every existing pattern changes.
