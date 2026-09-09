---
worth: later
where: pkg/executor/executor.go:595
added: 2026-09-04
---
# executor.go helpers called only from ClaudeExecutor methods are package-level functions

`isErrorResult`, `formatStructuredError` and `extractResultText` are called only from
`(*ClaudeExecutor).extractDiagnostic` and `extractText`, which the user-level rule says makes them
methods. `splitArgs` and `stripFlag` already have the same shape (called only from
`(*ClaudeExecutor).Run`), so the file's own precedent lags the rule. The genuinely shared helpers
(`detectSignal`, `matchPattern`, `filterEnv`) have callers in `codex.go` and `custom.go` and are
correctly standalone.

Surfaced reviewing PR #455. Convention drift only, no defect behind it. Worth one sweep over the file
covering all five, rather than fixing the three new ones and leaving the file inconsistent.
