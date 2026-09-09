---
worth: later
where: pkg/executor/executor.go:492
added: 2026-09-04
---
# trusted diagnostic window is evicted only by later diagnostics, not by session progress

`DiagnosticText` is a 10-slot ring that advances only when a diagnostic is added: a non-JSON line
(merged child stderr) or a structured error record entered at minute 1 of a two-hour session is still
pattern authority at minute 120, because ordinary assistant output never evicts it. The pre-#455 window
was the last 10 *surfaced* blocks, so later work displaced a stale failure. The comment beside
`recentBlockCount` ("keeping the windows small avoids retaining stale failures from long sessions") is
true of `RecentText` and false of `DiagnosticText`.

Surfaced reviewing PR #455. No reproduced trigger: nobody has shown Claude Code 2.1.251 emitting a
recoverable error on a non-`api_retry` channel, and the `system/api_retry` exclusion already covers the
one known recovery path. Any fix must keep the window bounded by session progress (advance or clear it
on later surfaced output or a clean terminal result) without letting trailing narration evict a genuine
terminal diagnostic. Becomes `yes` the moment a real capture shows a recovered error arriving as a
non-JSON line or a structured error record.
