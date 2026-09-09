---
worth: later
where: scripts/pi-as-claude/pi-as-claude.sh:80
added: 2026-08-19
---
# nothing can see which thinking level a pi run actually used

The wrapper forwards `--effort` to pi's `--thinking` and has no way to learn what pi did with it, so any
`--task-model=<m>:<effort>` may run at a level nobody asked for. Two independent paths lose the requested
level. Unknown values are dropped with a warning - that half is its own item,
`pi-ignores-unknown-thinking-level`. The other half is clamping: `clampThinkingLevel` in pi's
`packages/ai/src/models.ts:913` walks a known level to the nearest one the selected model exposes, and
`agent-session.ts:1718` applies it at session creation. That happens on every pi version, so it is not
something a version floor fixes, and it is per model - pi 0.81.0's release note about fixing Kimi K3 to
"expose low, high, and max" is exactly this catalog data feeding the clamp.

pi holds the answer and does not publish it where the wrapper reads. Print mode writes only
`sessionManager.getHeader()`, and `SessionHeader` (`core/session-manager.ts:32`) is type, version, id,
timestamp, cwd, parentSession - no model, no thinking level. The `thinking_level_changed` event exists but
is not emitted at startup: the clamp happens inside `createAgentSession`, so the subsequent
`setThinkingLevel` is a no-op against an already-equal value, and print mode subscribes after all of it.
RPC does expose it - `RpcSessionState` (`modes/rpc/rpc-types.ts:95`) carries `thinkingLevel` and `model`,
and there is a `get_available_thinking_levels` request beside `get_state`.

Three routes, none cheap. Ask upstream for an initial `runtime_state` JSON event carrying resolved model,
requested level, effective level, and available levels - the natural feature request, and the one that
would let the wrapper report a mismatch. Or drive `pi --mode rpc` long-lived: send `get_state`, compare
requested against effective, then send the prompt. That is the only exact adapter-side fix, and it needs
bidirectional stdin, response and event demultiplexing, prompt JSON encoding, shutdown handling, and
compatibility tests. A separate RPC preflight before the existing JSON invocation is weaker - it starts pi
twice and opens a gap where the model catalog can change between the check and the run.

A version gate below 0.80.6 is not a fix for this. It closes the unknown-value path only and leaves
clamping untouched, while adding semver parsing to a wrapper that has none.

Deferred rather than done: the honest fix is upstream, and the adapter alternative is a rewrite of how the
wrapper talks to pi. Surfaced reviewing PR #433 and in the codex discussion after it. Verified by reading
pi at HEAD; codex separately probed a local pi 0.80.6 with `--thinking max` on
`openai/gpt-5.3-codex-spark` and `get_state` returned an effective level of `xhigh`.
