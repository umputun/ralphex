---
worth: yes
where: pkg/executor/custom.go:29
added: 2026-08-12
---
# custom review script is the one spawn path that never filters the child env

`execCustomRunner.Run` never assigns `cmd.Env`, so the custom external review script inherits
`os.Environ()` verbatim. The two sibling runners in the same package both filter: `execClaudeRunner.Run`
(`pkg/executor/executor.go:100`) through `claudeChildEnv`, `execCodexRunner.Run`
(`pkg/executor/codex.go:68`) through `childEnv`.

The failure case is documented configuration. `README.md:1163` describes putting another tool in the
implementation slot and Claude in the review slot, via `claude_command` plus `external_review_tool = custom`
and `custom_review_script`. A user whose script shells out to `claude -p`, running ralphex from inside a
Claude Code session, gets a child that either hard-errors on `CLAUDECODE` or, on Claude Code 2.1.x, wedges
silently on the session markers: no transcript, no output, one full turn timeout per iteration.

Fix is one line plus a test case: `cmd.Env = filterEnv(os.Environ(), sessionEnvVars...)` before
`cmd.Start()`, with a table case in `custom_test.go`. `ANTHROPIC_API_KEY` should stay, matching the
external-codex-review rationale that a wrapper proxying through Anthropic needs it. If the pass-through is
deliberate, a comment at the `exec.Command` call would say so.

Surfaced reviewing PR #430, which added `sessionEnvVars` and applied it to the claude and codex paths.
It predates that PR - `CLAUDECODE` was never stripped here either - so it was left out rather than fixed
inline. No user is confirmed to be running such a script; the code path is confirmed by reading.
