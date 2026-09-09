---
worth: yes
where: scripts/pi-as-claude/pi-as-claude.sh:80
added: 2026-08-19
---
# pi accepts an unknown --thinking value, discards it, and runs anyway

Upstream defect, not ours, but it decides whether `--effort` means anything on the pi wrapper. In pi,
`packages/coding-agent/src/cli/args.ts:138` matches `--thinking <level>`, and on a value that fails
`isValidThinkingLevel` it pushes a diagnostic of `type: "warning"` and never assigns `result.thinking`.
`packages/coding-agent/src/main.ts:601-609` prints the diagnostics and calls `process.exit(1)` only
`if (parsed.diagnostics.some((d) => d.type === "error"))`. A warning does not exit, so pi runs the whole
turn at its own configured default with the requested level silently dropped.

This is what makes `--effort max` on a pre-0.80.6 pi a silent wrong-level run rather than a failure. The
wrapper compounds it: `pi-as-claude.sh` buffers pi's stderr to a file and replays it only after
`wait "$pi_pid"`, so the "Invalid thinking level" warning reaches the ralphex log after the turn has
already edited files. Nothing before or during the run says the level was ignored.

The ask upstream is one field: `type: "warning"` becomes `type: "error"` at `args.ts:144`. A CLI that
accepts a flag, discards its value, and proceeds is a bug on its own terms, independent of any feature
request, which is why this is worth filing separately from the observability item
(`pi-effective-thinking-level-unobservable`). It needs no new surface and would convert every unknown-level
case, not just `max`, into a clean exit before any work happens.

Surfaced reviewing PR #433, which removed the wrapper's `max`-to-`xhigh` downgrade. The review approved the
change on the reasoning that a pi-side rejection would be a loud abort; that reasoning was wrong, and
`f77159c` corrected the three doc surfaces that repeated it. Verified by reading pi at HEAD
(`earendil-works/pi`), not by running it - pi is not installed on this machine.
