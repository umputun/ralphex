# Add pi Provider Support (wrapper + skills)

## Overview

Add first-class support for **pi** (the `pi` agentic coding CLI, v0.78.1+) to
ralphex in two independent deliverables:

1. **`pi-as-claude` wrapper script** — a shell wrapper that adapts pi's
   `--mode json` event stream into the Claude `stream-json` format ralphex's
   `ClaudeExecutor` parses, so pi can drive the task and review phases as a
   drop-in `claude_command`, exactly like the existing
   `gemini-as-claude`/`codex-as-claude` wrappers.

2. **pi-adapted ralphex skills** — pi-compatible versions of the four Claude
   skills (`ralphex`, `ralphex-plan`, `ralphex-update`, `ralphex-adopt`) under a
   new `assets/pi/` tree, so pi users get the same slash-command convenience
   that Claude Code users get from `assets/claude/`.

**Why:** pi is another supported agent CLI for users who prefer it (or whose
provider/billing favors it). The wrapper unblocks running ralphex *with* pi; the
skills unblock driving ralphex *from inside* pi. The two parts share no code and
can ship independently.

## Context (from discovery)

**Wrapper side — existing pattern:**
- `scripts/gemini-as-claude/gemini-as-claude.sh` — plain-text wrapper (closest
  template for arg parsing, FIFO + background PID + SIGTERM forwarding, stderr
  capture, review-prompt adapter, fallback `result` event).
- `scripts/codex-as-claude/codex-as-claude.sh` — JSONL-translation wrapper
  (closest template for the jq event-mapping approach we need for pi's JSON mode).
- Each wrapper dir has: `<name>.sh`, `<name>_test.sh` (mock-CLI shell tests),
  `README.md`. `copilot-as-claude` adds a `*_docs_test.sh`.
- ralphex contract (`docs/custom-providers.md`): wrapper reads prompt from stdin
  (primary) or `-p` (fallback), ignores unknown flags gracefully (`*) shift ;;`),
  receives appended `--model <m>` / `--effort <e>` flags, and must emit
  `content_block_delta` (text) + a terminal `result` event.

**pi facts (from `pi --help` and https://pi.dev/docs/latest):**
- `--mode json` → JSONL event stream (one JSON object per line). Key events:
  `{"type":"session",...}` header; `message_update` with
  `assistantMessageEvent.type:"text_delta"` + `.delta` for assistant text;
  `tool_execution_start|update|end`; `turn_end`; `agent_end`.
- `--print, -p` for non-interactive; prompt passed as positional arg.
- `--provider <name>` (default google), `--model <pattern>` (supports
  `provider/id` and `:<thinking>`), `--thinking off|minimal|low|medium|high|xhigh`,
  `--api-key` (defaults to provider env vars).
- Built-in tools: `read, bash, edit, write`. No `Task`/subagent or structured
  multi-choice tool surfaced — this drives the skill adaptation.
- Skills: `SKILL.md` + YAML frontmatter (`name` [a-z0-9-, ≤64], `description`
  [≤1024], optional `allowed-tools` *space-delimited*, `disable-model-invocation`,
  `metadata`, `compatibility`, `license`). Discovered from `~/.pi/agent/skills/`,
  `.pi/skills/`, package `skills/`, settings, or `--skill <path>`. Invoked as
  `/skill:<name> [args]`; args appended as user input (no `$ARGUMENTS` in skills).
- Prompt templates (`~/.pi/agent/prompts/*.md`, `/name`) *do* support
  `$ARGUMENTS`/`$1`/`argument-hint` — a possible alternative delivery, but we
  chose the SKILL.md route to mirror `assets/claude/skills/`.

**Repo touch-points to update:** `docs/custom-providers.md`, `README.md`,
`llms.txt`, `CLAUDE.md` (Project Structure + wrapper inventory).

## Development Approach

- **Testing approach**: Regular (implement, then add mock-`pi` shell tests
  mirroring `gemini-as-claude_test.sh`).
- Complete each task fully before the next; small, focused changes.
- **Every task includes new/updated tests** for code it adds — success and
  error/edge cases. All tests pass before the next task starts.
- After editing any shell script, run `shellcheck` (project rule).
- Wrapper tests must use a **mock `pi`** on `PATH` — never invoke real pi (no
  LLM calls, no credits) in unit tests. Live-pi verification is Post-Completion.
- Keep the two parts (wrapper / skills) independently shippable.

## Testing Strategy

- **Unit tests**: required per task.
  - Wrapper: `scripts/pi-as-claude/pi-as-claude_test.sh` — mock-`pi` shell tests
    covering arg parsing, env handling, effort→thinking mapping, JSON event
    translation, signal passthrough, stderr handling, exit-code preservation
    (model on `gemini-as-claude_test.sh`).
  - Skills: a lightweight validator (shell or Go test) asserting each
    `assets/pi/skills/*/SKILL.md` has valid required frontmatter and contains no
    Claude-only tool references (`AskUserQuestion`, `TaskOutput`,
    `subagent_type`, `run_in_background`, `~/.claude/`).
- **E2E tests**: ralphex's Go/Playwright e2e suite is unaffected (no Go code
  changes expected). A real end-to-end pi run is a Post-Completion manual step.
- `shellcheck` clean on the new wrapper and its test script.

## Progress Tracking

- Mark completed items `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix; blockers with ⚠️ prefix.
- Keep this plan in sync with actual work.

## What Goes Where

- **Implementation Steps** (`[ ]`): wrapper script + tests, skill files + tests,
  in-repo docs.
- **Post-Completion** (no checkboxes): live pi run, real ralphex+pi toy run,
  real pi skill invocation, plugin/marketplace + website-serving decisions.

## Implementation Steps

### Task 1: pi-as-claude wrapper — argument handling & pi invocation
- [ ] create `scripts/pi-as-claude/pi-as-claude.sh`: `set -euo pipefail`,
      dependency checks (`jq`, `pi`), prompt sourcing from `-p` then stdin
      (only when `! -t 0`), missing-prompt error, ignore unknown flags
      (`*) shift ;;`) — mirror `gemini-as-claude.sh` lines 17–45
- [ ] parse `--model` and `--effort` explicitly: forward `--model` to pi
      `--model`; translate `--effort` → pi `--thinking`
      (`low|medium|high|xhigh` passthrough, `minimal|off` allowed, `max`→`xhigh`
      with a stderr note since pi has no `max`); honor `PI_PROVIDER`,
      `PI_MODEL` (used when `--model` absent), `PI_THINKING`, `PI_VERBOSE` env
- [ ] build pi args (`--mode json --print` + resolved provider/model/thinking,
      prompt as positional arg) and launch via private-tmp FIFO + background PID
      + `trap forward_signal TERM` + stderr capture — mirror
      `gemini-as-claude.sh` lines 61–93
- [ ] write tests for arg parsing, env handling, and effort→thinking mapping
      (incl. `max`→`xhigh` note) using a mock `pi`
- [ ] write tests for `-p` vs stdin prompt sourcing and the missing-prompt error
- [ ] run `shellcheck` + tests — must pass before next task

### Task 2: pi JSON event → stream-json translation
- [ ] translate JSONL with `jq`: `message_update` +
      `assistantMessageEvent.type=="text_delta"` → `content_block_delta`
      (`text_delta`, append `\n`); `turn_end`/`agent_end` → `result`; `session`
      header and `tool_execution_*` skipped by default; include tool lines only
      when `PI_VERBOSE=1`; always emit a fallback `{"type":"result","result":""}`
- [ ] emit captured stderr lines as `content_block_delta` events after stdout so
      ralphex error/limit pattern detection still works — mirror
      `gemini-as-claude.sh` lines 106–112; preserve pi's exit code
- [ ] add review-prompt adapter: detect `<<<RALPHEX:REVIEW_DONE>>>`, prepend
      pi-appropriate adapter text (pi exposes no parallel sub-agents → instruct
      sequential per-agent review using pi's read/bash/edit/write tools), and
      keep all `<<<RALPHEX:...>>>` signals unchanged
- [ ] write tests for event translation (text deltas emitted, tool events
      skipped by default, `PI_VERBOSE=1` includes them, terminal `result`)
- [ ] write tests for review-prompt detection, adapter injection, and signal
      passthrough; plus stderr emission and exit-code preservation
- [ ] run `shellcheck` + tests — must pass before next task

### Task 3: Wrapper docs & repo integration
- [ ] add `scripts/pi-as-claude/README.md` (config snippet, env vars
      `PI_MODEL`/`PI_PROVIDER`/`PI_THINKING`/`PI_VERBOSE`, requirements `pi`+`jq`,
      troubleshooting) — mirror `gemini-as-claude/README.md`
- [ ] add a **pi** section to `docs/custom-providers.md` (JSON-mode notes,
      event-translation table, env vars, thinking/effort mapping)
- [ ] update `README.md` + `llms.txt` wrapper inventories to include
      `scripts/pi-as-claude/pi-as-claude.sh`; add `pi` to the optional
      Requirements lists
- [ ] update `CLAUDE.md` Project Structure + wrapper inventory to list
      `scripts/pi-as-claude/`
- [ ] write tests: extend/copy the `copilot-as-claude_docs_test.sh` pattern (or a
      small grep-based check) asserting the wrapper path is referenced in
      `README.md`, `llms.txt`, and `docs/custom-providers.md`
- [ ] run tests — must pass before next task

### Task 4: Scaffold assets/pi tree & confirm pi tool mapping
- [ ] confirm pi's tool model from `pi --help` / docs and record the
      Claude→pi mapping in **Technical Details** below: `AskUserQuestion`→inline
      question (pi is interactive, model asks and waits); `Task`+`subagent_type:
      Explore`→inline exploration via pi `read`/`bash`(grep/find); `Bash`
      `run_in_background`+`TaskOutput`→pi `bash` backgrounding + tailing
      `.ralphex/progress/*`; `Glob`/`Grep`→pi `bash` find/grep; `Write`→pi `write`
- [ ] create `assets/pi/skills/{ralphex,ralphex-plan,ralphex-update,ralphex-adopt}/SKILL.md`
      directory structure mirroring `assets/claude/skills/`
- [ ] define frontmatter translation rules: keep `name`/`description`; convert
      `allowed-tools` array → pi space-delimited string mapped to pi tool names
      (`read bash edit write`); drop Claude-only `argument-hint` from skills;
      note args arrive as appended user input (no `$ARGUMENTS` in pi skills)
- [ ] add `assets/pi/README.md`: install via `~/.pi/agent/skills/` (or
      `.pi/skills/` / `pi --skill <path>`), invoke as `/skill:ralphex-plan`
- [ ] write a frontmatter-validator test (shell or Go under the appropriate
      package) asserting required fields + `name` pattern for every
      `assets/pi/skills/*/SKILL.md`
- [ ] run tests — must pass before next task

### Task 5: Adapt ralphex-plan & ralphex-adopt skills for pi
- [ ] port `ralphex-plan` SKILL.md: replace the `Task`/`Explore` subagent step
      with pi inline exploration; `AskUserQuestion` one-at-a-time prompts →
      inline questions the model asks the user; keep all plan-format rules
      (Task-keyword constraint, checkboxes only in Task sections, per-task tests)
- [ ] port `ralphex-adopt` SKILL.md: replace `AskUserQuestion`; replace the
      `~/.claude/plugins/.../launch-revdiff.sh` path with pi's revdiff path if
      available else the in-chat review fallback; preserve all source-format
      conversion rules and the English `Task` keyword requirement
- [ ] ensure argument handling matches pi skills (args appended as user input),
      adjusting any `$ARGUMENTS` references in the prose
- [ ] write/extend validator tests for these two files: valid frontmatter + no
      Claude-only tokens (`AskUserQuestion`, `TaskOutput`, `subagent_type`,
      `run_in_background`, `~/.claude/`)
- [ ] run tests — must pass before next task

### Task 6: Adapt ralphex & ralphex-update skills for pi
- [ ] port `ralphex` (launcher) SKILL.md: replace `Bash` `run_in_background` +
      `TaskOutput` monitoring with pi `bash` backgrounding + `tail` of
      `.ralphex/progress/progress-*.txt`; `AskUserQuestion`→inline; `Glob`→pi
      `bash` find; keep the `CLAUDECODE`-stripped / standalone-terminal guidance
      reframed for pi
- [ ] port `ralphex-update` SKILL.md: map `Bash`/`Read`/`Write`/`Glob`/
      `AskUserQuestion` to pi equivalents; keep the customized-file detection and
      smart-merge algorithm intact (`ralphex --dump-defaults`, comment-stripping
      comparison)
- [ ] extend the validator test to cover all four `assets/pi/skills/*/SKILL.md`
- [ ] write tests asserting no Claude-only tool references remain in any of the
      four pi skill files
- [ ] run tests — must pass before next task

### Task 7: Verify acceptance criteria
- [ ] verify the wrapper emits valid stream-json for assistant text, skips tool
      noise by default, emits a terminal `result`, and preserves pi's exit code
      (mock-`pi` tests green)
- [ ] verify all four pi skills exist with valid frontmatter and contain no
      Claude-only tool references
- [ ] run full test suite (`make test`) — must pass
- [ ] run `make lint` and `shellcheck scripts/pi-as-claude/*.sh` — all clean
- [ ] run `make fmt`; verify 80%+ coverage on any new Go test code added

### Task 8: [Final] Documentation & versioning
- [ ] confirm `docs/custom-providers.md`, `README.md`, `llms.txt`, and
      `CLAUDE.md` consistently reference both the pi wrapper and the pi skills
- [ ] document pi skill install in the integration docs; decide and note whether
      pi skills need their own manifest (see Post-Completion) — do **not** bump
      `.claude-plugin/plugin.json` for `assets/pi/` changes (that version gates
      only `assets/claude/`); record the rationale
- [ ] update `llms.txt` "Requirements" to list `pi` as an optional provider CLI

*Note: ralphex automatically moves completed plans to `docs/plans/completed/`.*

## Technical Details

**Wrapper invocation (target):**
```
printf '%s' "$prompt" passed as positional arg to:
pi --mode json --print [--provider $PI_PROVIDER] [--model $MODEL] [--thinking $LEVEL]
```
Run in background through a FIFO; forward `SIGTERM`; capture stderr to a temp
file and emit it after stdout; always close with `{"type":"result","result":""}`.

**Event mapping (jq):**
| pi event | stream-json output |
|----------|--------------------|
| `message_update` + `assistantMessageEvent.type=="text_delta"` | `content_block_delta` (`text_delta`, text + `\n`) |
| `tool_execution_start/update/end` | skipped (emit only if `PI_VERBOSE=1`) |
| `session` header, `queue_update`, `compaction_*`, `auto_retry_*` | skipped |
| `turn_end` / `agent_end` | `result` |

**Effort → pi thinking:** `low/medium/high/xhigh` passthrough; `minimal`/`off`
accepted; `max` → `xhigh` (pi has no `max`; print a one-line stderr note, like
codex's max-effort handling).

**Claude→pi tool mapping for skills (to be confirmed in Task 4):**
| Claude skill tool | pi equivalent |
|-------------------|---------------|
| `AskUserQuestion` | inline question — pi is interactive; model asks, user replies |
| `Task` + `subagent_type: Explore` | inline exploration via pi `read` + `bash` (find/grep) |
| `Bash` `run_in_background` + `TaskOutput` | pi `bash` backgrounding + `tail` of `.ralphex/progress/*` |
| `Glob` / `Grep` | pi `bash` (`find` / `grep`) |
| `Read` / `Write` | pi `read` / `write` |
| frontmatter `allowed-tools: [..]` (array) | pi `allowed-tools: read bash edit write` (space-delimited) |

## Post-Completion
*Manual / external — no checkboxes, informational only.*

**Manual verification (requires a pi provider/API key; consumes credits):**
- Live `pi --mode json --print "list files in src/"` to confirm real event
  shapes match the jq mapping (adjust if pi's actual field names differ).
- End-to-end ralphex toy run with
  `claude_command=scripts/pi-as-claude/pi-as-claude.sh` (`scripts/internal/
  prep-toy-test.sh`), watching `.ralphex/progress/*` to confirm streaming works.
- Install one pi skill (`~/.pi/agent/skills/ralphex-plan/SKILL.md`) and invoke
  `/skill:ralphex-plan ...` inside pi to confirm it runs end-to-end.

**Distribution decisions (external):**
- Whether/how to serve `assets/pi/*` from ralphex.com alongside `assets/claude/*`
  (the `prep_site` copy step), and whether pi skills warrant a pi-side package/
  manifest analogous to `.claude-plugin/`.
- Whether to add a `pi install <source>`-friendly package layout for one-command
  skill installation.
