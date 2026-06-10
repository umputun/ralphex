# Add pi Provider Support (wrapper)

## Overview

Add support for **pi** (the `pi` agentic coding CLI, v0.78.1+) to ralphex as a
drop-in Claude-compatible provider:

- **`pi-as-claude` wrapper script** — a shell wrapper that adapts pi's
  `--mode json` event stream into the Claude `stream-json` format ralphex's
  `ClaudeExecutor` parses, so pi can drive the task and review phases as a
  drop-in `claude_command`, exactly like the existing
  `gemini-as-claude`/`codex-as-claude`/`copilot-as-claude` wrappers.

**Why:** pi is another supported agent CLI for users who prefer it (or whose
provider/billing favors it). The wrapper unblocks running ralphex *with* pi.

Every other provider (codex, copilot, gemini, agy, opencode) is wrapper-only,
so pi follows the same shape: a wrapper plus one-line provider bullets in the
docs — no forked skill tree to keep in sync with `assets/claude/skills/`.

Two design points baked in from the start (rather than retrofitted):
- the prompt is delivered to pi via **stdin**, never on argv, so a large review
  prompt with a full diff cannot blow past the OS per-arg length cap; and
- stderr is re-emitted for ralphex's error/limit pattern detection, but any
  literal `<<<RALPHEX:...>>>` token on stderr is neutralized first so it cannot
  be misread as a real completion signal.

## Context (from discovery)

**Existing wrapper pattern:**
- `scripts/gemini-as-claude/gemini-as-claude.sh` — closest template for arg
  parsing, FIFO + background PID + SIGTERM forwarding, stderr capture,
  review-prompt adapter, and the fallback `result` event.
- `scripts/codex-as-claude/codex-as-claude.sh` — JSONL-translation wrapper
  (closest template for the jq event-mapping approach pi's JSON mode needs);
  pipes the prompt to the child via stdin.
- `scripts/copilot-as-claude/copilot-as-claude.sh` — writes the prompt to a
  temp file and redirects `copilot ... < "$prompt_file"`; the template for
  pi's stdin delivery, since pi already runs in the background through a FIFO.
- Each wrapper dir has `<name>.sh`, `<name>_test.sh` (mock-CLI shell tests),
  and `README.md`; `copilot-as-claude` adds a `*_docs_test.sh`.
- ralphex contract (`docs/custom-providers.md`): wrapper reads prompt from
  stdin (primary) or `-p` (fallback), ignores unknown flags gracefully
  (`*) shift ;;`), receives appended `--model <m>` / `--effort <e>` flags, and
  must emit `content_block_delta` (text) + a terminal `result` event.

**pi facts (from `pi --help` and https://pi.dev/docs/latest):**
- `--mode json` → JSONL event stream (one JSON object per line). Key events:
  `{"type":"session",...}` header; `message_update` with
  `assistantMessageEvent.type:"text_delta"` + `.delta` for assistant text;
  `tool_execution_start|update|end`; `turn_end`; `agent_end`.
- `--print, -p` for non-interactive. With no positional message given,
  `pi --mode json --print` reads the prompt from **stdin** (verified: piped
  text appears as the user message) — so the prompt need never go on argv.
- `--provider <name>` (default google), `--model <pattern>` (supports
  `provider/id` and `:<thinking>`), `--thinking off|minimal|low|medium|high|xhigh`,
  `--api-key` (defaults to provider env vars).
- Built-in tools: `read, bash, edit, write`. No `Task`/subagent surface and no
  parallel sub-agents — so the review-prompt adapter instructs sequential
  per-agent review.

**Signal/detection facts:**
- Signal marker prefix is the literal `<<<RALPHEX:` (`pkg/status/status.go`,
  `pkg/progress/progress.go:462`); `detectSignal` keys on the full
  `<<<RALPHEX:NAME>>>` token. Error/limit patterns (rate-limit text,
  `API Error:`, etc.) never contain that token, so neutralizing only
  `<<<RALPHEX:` in re-emitted stderr preserves error/limit detection.

**Repo touch-points to update:** `docs/custom-providers.md`, `README.md`,
`llms.txt`, `CLAUDE.md` (Project Structure + wrapper inventory) — one-line
provider references only.

## Development Approach

- **Testing approach**: Regular (implement, then add mock-`pi` shell tests
  mirroring `gemini-as-claude_test.sh`).
- Complete each task fully before the next; small, focused changes.
- **Every task includes new/updated tests** for the code it adds — success and
  error/edge cases. All tests pass before the next task starts.
- After editing any shell script, run `shellcheck` (project rule).
- Wrapper tests must use a **mock `pi`** on `PATH` — never invoke real pi (no
  LLM calls, no credits) in unit tests. Live-pi verification is Post-Completion.

## Testing Strategy

- **Unit tests**: required per task.
  - Wrapper: `scripts/pi-as-claude/pi-as-claude_test.sh` — mock-`pi` shell tests
    covering arg parsing, env handling, effort→thinking mapping, stdin prompt
    delivery, JSON event translation, signal passthrough, stderr handling
    (including signal-token neutralization), and exit-code preservation.
  - Docs: `scripts/pi-as-claude/pi-as-claude_docs_test.sh` — cross-doc
    assertions that the wrapper path is referenced consistently in `README.md`,
    `llms.txt`, `docs/custom-providers.md`, and `CLAUDE.md`.
- **No Go changes**: ralphex's Go/Playwright e2e suite is unaffected; run
  `go build ./...` once as a sanity check.
- `shellcheck` clean on the new wrapper and its test scripts.

## Progress Tracking

- Mark completed items `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix; blockers with ⚠️ prefix.
- Keep this plan in sync with actual work.

## What Goes Where

- **Implementation Steps** (`[ ]`): wrapper script + tests, in-repo docs.
- **Post-Completion** (no checkboxes): live pi run, real ralphex+pi toy run.

## Implementation Steps

### Task 1: pi-as-claude wrapper — argument handling & pi invocation
- [x] create `scripts/pi-as-claude/pi-as-claude.sh`: `set -euo pipefail`,
      dependency checks (`jq`, `pi`), prompt sourcing from `-p` then stdin
      (only when `! -t 0`), missing-prompt error, ignore unknown flags
      (`*) shift ;;`) — mirror `gemini-as-claude.sh`
- [x] parse `--model` and `--effort` explicitly: forward `--model` to pi
      `--model`; translate `--effort` → pi `--thinking`
      (`low|medium|high|xhigh` passthrough, `minimal|off` allowed, `max`→`xhigh`
      with a stderr note since pi has no `max`); honor `PI_PROVIDER`,
      `PI_MODEL` (used when `--model` absent), `PI_THINKING`, `PI_VERBOSE`, and
      `PI_EXTRA_ARGS` (word-split, appended verbatim for non-interactive tool
      approval) env vars
- [x] build pi args (`--mode json --print` + resolved provider/model/thinking +
      `PI_EXTRA_ARGS`) and deliver the prompt via **stdin**, not argv: write the
      (possibly adapter-prepended) prompt to a `mktemp` temp file cleaned up in
      the `cleanup()` trap, and launch pi with `< "$prompt_file"` through the
      private-tmp FIFO + background PID + `trap forward_signal TERM` + stderr
      capture — mirror `copilot-as-claude.sh` for the stdin redirect and
      `gemini-as-claude.sh` for the FIFO/signal structure
- [x] write tests for arg parsing, env handling, and effort→thinking mapping
      (incl. `max`→`xhigh` note) using a mock `pi`
- [x] write tests for `-p` vs stdin prompt sourcing, the missing-prompt error,
      stdin delivery to pi (mock captures stdin, not a positional arg), and
      `PI_EXTRA_ARGS` passthrough (no positional prompt is ever appended)
- [x] run `shellcheck` + tests — must pass before next task

### Task 2: pi JSON event → stream-json translation & stderr handling
- [x] translate JSONL with `jq`: `message_update` +
      `assistantMessageEvent.type=="text_delta"` → `content_block_delta`
      (`text_delta`); buffer token deltas into whole lines so `<<<RALPHEX:...>>>`
      signals survive translation even when split across deltas;
      `turn_end`/`agent_end` → `result`; `session` header and
      `tool_execution_*` skipped by default (included only when `PI_VERBOSE=1`);
      always emit a fallback `{"type":"result","result":""}`
- [x] emit captured stderr lines as `content_block_delta` events after stdout so
      ralphex error/limit pattern detection still works, but **neutralize** the
      literal `<<<RALPHEX:` token per line first (bash parameter expansion
      `${err_line//<<<RALPHEX:/<<< RALPHEX:}`) so a stray signal token on stderr
      cannot be read as a real completion signal; preserve pi's exit code
- [x] add review-prompt adapter: detect `<<<RALPHEX:REVIEW_DONE>>>`, prepend
      pi-appropriate adapter text (pi exposes no parallel sub-agents → instruct
      sequential per-agent review using pi's read/bash/edit/write tools), and
      keep all `<<<RALPHEX:...>>>` signals unchanged
- [x] write tests for event translation (line-buffered text deltas emitted,
      tool events skipped by default, `PI_VERBOSE=1` includes them, terminal
      `result`) and for review-prompt detection / adapter injection / signal
      passthrough
- [x] write tests for stderr handling: a stderr line containing
      `<<<RALPHEX:ALL_TASKS_DONE>>>` is emitted with **no** intact signal token,
      while a rate-limit phrase (e.g. `You've hit your usage limit`) still flows
      through verbatim for error/limit detection; plus exit-code preservation
- [x] run `shellcheck` + tests — must pass before next task

### Task 3: Wrapper docs & repo integration
- [x] add `scripts/pi-as-claude/README.md` (config snippet, env vars
      `PI_MODEL`/`PI_PROVIDER`/`PI_THINKING`/`PI_VERBOSE`/`PI_EXTRA_ARGS`,
      requirements `pi`+`jq`, troubleshooting) — mirror `gemini-as-claude/README.md`
- [x] add a **pi** section to `docs/custom-providers.md` (JSON-mode notes,
      event-translation table, env vars, thinking/effort mapping, stdin prompt
      delivery)
- [x] update `README.md` + `llms.txt` wrapper inventories to include
      `scripts/pi-as-claude/pi-as-claude.sh`; add `pi` to the optional
      Requirements lists — one-line provider references, no dedicated
      `## pi Integration` section
- [x] update `CLAUDE.md` Project Structure + alternative-providers paragraph to
      list `scripts/pi-as-claude/`
- [x] add `scripts/pi-as-claude/pi-as-claude_docs_test.sh` (mirror the
      `copilot-as-claude_docs_test.sh` pattern) asserting the wrapper path is
      referenced in `README.md`, `llms.txt`, `docs/custom-providers.md`, and
      `CLAUDE.md`
- [x] run `shellcheck` + tests — must pass before next task

### Task 4: Verify acceptance criteria
- [x] verify the wrapper emits valid stream-json for assistant text, skips tool
      noise by default, emits a terminal `result`, preserves pi's exit code, and
      delivers the prompt via stdin (mock-`pi` tests green)
- [x] verify stderr error/limit detection still works while stray RALPHEX tokens
      on stderr are neutralized
- [x] run full test suite (`make test`) and `go build ./...` — must pass
- [x] run `make lint` and `shellcheck scripts/pi-as-claude/*.sh` — all clean
- [x] run `make fmt`

### Task 5: [Final] Documentation
- [x] confirm `docs/custom-providers.md`, `README.md`, `llms.txt`, and
      `CLAUDE.md` consistently reference the pi wrapper as a wrapper-only
      provider and read coherently
- [x] update `llms.txt` "Requirements" to list `pi` as an optional provider CLI

*Note: ralphex automatically moves completed plans to `docs/plans/completed/`.*

## Technical Details

**Wrapper invocation (target):**
```
pi --mode json --print [--provider $PI_PROVIDER] [--model $MODEL] \
   [--thinking $LEVEL] [$PI_EXTRA_ARGS...]   < "$prompt_file"
```
The prompt is written to a temp file and redirected on stdin (never appended to
argv) so a large review prompt cannot exceed the OS per-arg length cap. pi runs
in the background through a FIFO; `SIGTERM` is forwarded to `$pi_pid`; stderr is
captured to a temp file and emitted after stdout; the stream always closes with
`{"type":"result","result":""}`. The stdin redirect (vs `printf | pi`) keeps the
existing background/PID structure intact, matching `copilot-as-claude.sh`.

**Event mapping (jq):**
| pi event | stream-json output |
|----------|--------------------|
| `message_update` + `assistantMessageEvent.type=="text_delta"` | buffered into whole lines → `content_block_delta` (`text_delta`) |
| `tool_execution_start/update/end` | skipped (emit only if `PI_VERBOSE=1`) |
| `session` header, `queue_update`, `compaction_*`, `auto_retry_*` | skipped |
| `turn_end` / `agent_end` | flush buffer, then `result` |

Token deltas are re-assembled into whole lines before emission so a
`<<<RALPHEX:...>>>` signal lands intact in a single block (per-block
`detectSignal` could never match a signal split across token deltas).

**Effort → pi thinking:** `low/medium/high/xhigh` passthrough; `minimal`/`off`
accepted; `max` → `xhigh` (pi has no `max`; print a one-line stderr note, like
codex's max-effort handling).

**stderr sanitization:** bash parameter expansion
`${err_line//<<<RALPHEX:/<<< RALPHEX:}` neutralizes every signal token on a line
with no extra process spawn. Only `<<<RALPHEX:` is rewritten; the rest of the
line (rate-limit / `API Error:` phrases) is untouched, so error/limit pattern
detection keeps working while a literal signal token on stderr cannot be
mistaken for a real completion signal.

## Post-Completion
*Manual / external — no checkboxes, informational only.*

**Manual verification (requires a pi provider/API key; consumes credits):**
- Live `pi --mode json --print "list files in src/"` to confirm real event
  shapes match the jq mapping (adjust if pi's actual field names differ).
- End-to-end ralphex toy run with
  `claude_command=scripts/pi-as-claude/pi-as-claude.sh` (`scripts/internal/
  prep-toy-test.sh`), watching `.ralphex/progress/*` to confirm streaming and
  signal detection work with a real pi session.
