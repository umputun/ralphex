---
worth: later
where: pkg/executor/executor.go:573
added: 2026-09-04
---
# system/error diagnostic branches in extractDiagnostic have no capture behind them

Two of the four classified event classes are not backed by any Claude Code 2.1.251 fixture: the
`system` branch, gated on `strings.Contains(event.Subtype, "error")`, and the `error` branch, which
trusts the record unconditionally. Their tests use hand-written JSON, and the `system/api_retry`
exclusion documented in CLAUDE.md and `testdata/claude/README.md` holds only because the literal
`api_retry` happens not to contain `error`. The `system` branch also forwards `event.Description`
into trusted pattern input, and `Description` is the subagent task title, model-authored text of
exactly the class the split exists to keep out.

Surfaced reviewing PR #455. No shipping subtype is known to trigger it today. When this dispatch is
next touched: switch to an exact-subtype match mirroring `subagentLine`, keep `api_retry` out and the
observed error subtypes in, and do not let `Description` reach diagnostics for events whose
description is a task title. Or drop both branches until a capture justifies them and mark them as
unobserved in the testdata README.
