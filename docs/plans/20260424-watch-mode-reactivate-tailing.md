# Watch-mode reactivate tailing on write events

## Overview

Fix issue #283: the watch-mode dashboard (`ralphex -s -w /path` running as a separate process) stops streaming logs mid-run. Once `RefreshStates()` briefly sees the progress file unlocked — which happens whenever `TryLockFile` wins a race against the executor's flock, or after the executor finishes — it marks the session `completed` and stops tailing. Subsequent writes from the still-running (or restarted) executor are ignored because `startTailingIfNeeded()` refuses to re-start tailing for non-active sessions.

**Fix:** implement Option B from umputun's comment on the issue — on a `Write` event for a progress file that belongs to a `completed` session, reactivate the session and resume tailing. To avoid re-emitting events into the SSE replay buffer, track the last-read file offset on the `Session` and resume tailing from there instead of from the beginning.

**Benefits:**
- watch-mode + separate executor (the pm2 deployment pattern) actually works
- no duplicate events in SSE replay after reactivation
- `RefreshStates` / flock detection stays unchanged — reactivation is the recovery path, not a replacement

## Context (from discovery)

- **files involved:**
  - `pkg/web/tail.go` — expose Tailer offset and accept starting offset
  - `pkg/web/session.go` — track `lastOffset`, add `Reactivate()` method
  - `pkg/web/session_progress.go` — update `loadProgressFileIntoSession` to record offset after initial load (loader function lives here, not in session_manager.go)
  - `pkg/web/watcher.go` — in `handleProgressFileChange`, reactivate a completed session that just received a write
  - tests: `session_test.go`, `watcher_test.go`, `session_progress_test.go`, `tail_test.go`
- **related patterns found:**
  - flock-based activity detection in `IsActive()` (session_manager.go:359)
  - 5-second tick in `refreshLoop` calling `RefreshStates` (watcher.go:214)
  - Tailer already tracks `offset` internally (`tail.go:40`) — needs accessor + seek-to-offset variant
  - tests already use `t.TempDir()` and real fsnotify watches
- **dependencies identified:**
  - `fsnotify` Write events must be delivered (already working — plan panel updates prove this)
  - executor's `progress.NewLogger` does hold flock for the whole run (`progress.go:178`) — confirmed via code read, contradicts the reporter's guess

## Development Approach

- **testing approach:** TDD for Task 0 (failing reproduction test to prove the bug exists), then Regular for Tasks 1-5 (implement + tests per task). This satisfies the user's CLAUDE.md rule "For bug fixes: Use TDD approach - write failing test first"
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - tests are not optional — they are a required part of the checklist
  - cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task**
- **CRITICAL: update this plan file when scope changes during implementation**
- run `make test` after each change
- run `make lint` at the end; fix all linter issues (BANNED: "pre-existing")
- maintain backward compatibility — don't break non-watch-mode users

## Testing Strategy

- **unit tests**: required for every task
  - extend `tail_test.go` for offset accessor and seek-to-offset start
  - extend `session_test.go` for `lastOffset` tracking, `Reactivate`
  - extend `watcher_test.go` for the reactivation-on-write flow end-to-end (use real `fsnotify` and real files via `t.TempDir()` as existing tests do)
  - extend `session_manager_test.go` / `session_progress_test.go` if loader-offset recording lives there
- **e2e tests**: the project has playwright e2e tests under `e2e/`; this fix is a non-UI backend change, but the dashboard behavior is visible. After all unit tests pass, run the toy-project end-to-end test (`scripts/internal/prep-toy-test.sh` + `ralphex -s -w` + separate executor) to verify live streaming survives the 5-second tick

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope
- keep plan in sync with actual work done

## Solution Overview

**The bug in one picture:**

```
executor process                 dashboard process (-w)
----------------                 ----------------------
write line                   -->
flock held                       refresh tick: TryLockFile fails → still active
...                              tail keeps streaming
flock momentarily free / run ends
                                 refresh tick: TryLockFile succeeds → marks completed, stops tailer
flock re-held (or same executor keeps writing)
write line                   --> fsnotify Write event
                                 handleProgressFileChange → Discover → updateSession
                                 IsActive may STILL say "not active" (flock race) → state stays completed
                                 startTailingIfNeeded: state != active → NO-OP ❌
```

**Fix:** after `Discover` in `handleProgressFileChange`, if the session for the written path is `completed`, call `session.Reactivate()` which flips state to `active` and restarts tailing from the stored `lastOffset` (captured when the previous tailer stopped, or set to file size after `loadProgressFileIntoSession`).

**Why we keep RefreshStates unchanged:** flock-based completion detection is still correct when the run genuinely ends. Reactivation handles the case where writes resume after a false "completed" marking — no cost to correct cases.

**Key design decisions:**

- **offset tracking on the Session (not the Tailer):** tailers are one-shot (`Stop()` makes them non-reusable per tail.go:84). The Session outlives tailer instances, so it owns the offset.
- **`Reactivate()` bypasses the flock check:** a write event is strong evidence of activity. False positives (a human editing the progress file) are harmless — at worst, tailer starts and finds nothing to emit.
- **loader records offset too:** when a completed session is discovered for the first time, `loadProgressFileIntoSession` reads the whole file into SSE. We record the bytes consumed as `lastOffset` so a later reactivation (if writes resume) picks up after the loaded content, not from scratch.
- **Windows:** on Windows, flock is a no-op (`IsActive` always returns false). That means every session is marked completed immediately after discovery, and reactivation-on-write becomes the *primary* streaming mechanism on Windows — not just a recovery path for the race. The fix works the same way on both platforms; no platform-specific branches needed.
- **Interaction with `updateSession` StopTailing path:** `handleProgressFileChange` calls `Discover` → `updateSession` which, if it observes an `active→completed` transition under the flock race, will call `StopTailing()` and capture the current offset. Our reactivation step runs immediately after. Flow: Discover(race→completed) → StopTailing(offset captured) → Reactivate(starts from captured offset). No events are lost: the tailer's in-flight read loop writes `t.offset` incrementally, `StopTailing` snapshots it, and the new tailer resumes from that byte position. This interaction is intentional — reactivation is the undo button for the flock race, not an attempt to prevent the race in the first place.

## Technical Details

**New / changed APIs:**

- `Tailer.Offset() int64` — read current offset under tailer mutex
- `Tailer.StartFromOffset(offset int64) error` — new method; opens file, seeks to offset (if offset <= 0, behaves as `Start(false)` / seek-to-end; if offset > file size, clamps to file size); sets `t.offset=offset`, `t.inHeader=false`, `t.deferSections=false`, launches tailLoop. `Start(fromStart bool)` stays unchanged.
- `Session.lastOffset int64` — private, guarded by `s.mu`
- `Session.getLastOffset()` / `Session.setLastOffset(int64)` — **unexported** package-internal accessors (no external callers; tests are in-package)
- `Session.StopTailing()` — before stopping, capture `tailer.Offset()` into `s.lastOffset` (under the write lock, same critical section as nilling `stopTailCh`)
- `Session.Reactivate() error` — starts tailing from `lastOffset`, then sets state to `active` on success. If already tailing, returns nil (idempotent). On tailer-start failure, leaves state unchanged.
- `SessionManager.loadProgressFileIntoSession()` — accumulate `bytesRead += int64(len(line))` on each `ReadString('\n')` return BEFORE `trimLineEnding` is applied (so CRLF, LF, and bare CR all count correctly). After the read loop, call `session.setLastOffset(bytesRead)`.
- `Watcher.handleProgressFileChange()` — after `Discover`, look up the specific session by `sessionIDFromPath(path)` and call `Reactivate()` only if its state is `completed`. This is scoped to the exact path that received the write — other completed sessions in the same directory are not reactivated.

**Why `Reactivate()` and not `SetState(active) + StartTailing`:** atomicity — we want "check state, start tailing from offset, flip state on success" under a single lock boundary without re-entering the public `StartTailing(fromStart bool)` path (whose `fromStart=true` semantics would reset offset to 0).

**Shared helper:** `startTailerLocked(mode tailerStartMode, offset int64) error` — called with `s.mu` held (write lock). Creates a new `Tailer`, starts it according to `mode` (resumeFromOffset / fromStart / fromEnd), allocates `stopTailCh`, launches `feedEvents` goroutine. Modes are distinct to avoid ambiguity: `modeResume` with `offset<=0` would re-emit everything if misrouted through `fromStart`, so `Reactivate` with `lastOffset==0` uses `fromEnd`, not `fromStart`. Dispatch:
- `modeFromStart` → `tailer.Start(true)` (existing behavior, used by `StartTailing(true)`)
- `modeFromEnd` → `tailer.Start(false)` (seek to end; used by `StartTailing(false)` and `Reactivate` when `lastOffset==0`)
- `modeResume(offset)` → `tailer.StartFromOffset(offset)` (used by `Reactivate` when `lastOffset>0`)

The feedEvents goroutine acquires its own `RLock` once running — no deadlock because the outer caller unlocks immediately after returning from the helper. Contract documented in godoc.

**Potential races considered:**

- `RefreshStates` + `Reactivate` racing: both take `s.mu` for state transitions; worst case RefreshStates flips back to completed, next write event re-flips to active. Self-healing.
- Multiple write events arriving in burst: `Reactivate()` early-returns if `tailer.IsRunning()`. Idempotent.
- Session closed while reactivating: `StartFromOffset` would fail opening the file; `Reactivate` returns the error without flipping state. Watcher logs and moves on. No state desync.
- `updateSession` stops tailing due to flock race, then `Reactivate` restarts: offset is captured inside `StopTailing`, so no events are re-emitted — the new tailer resumes from the exact byte where the old one stopped. Worth an explicit test (see Task 5).
- Concurrent `loadProgressFileIntoSession` + write-triggered reactivation: protected by `MarkLoadedIfNot` — only one loader runs ever. If loader is still running when a reactivation fires, the reactivation sees `lastOffset=0` (not yet set) and starts from 0 → would re-emit everything the loader already published. Mitigation: set `lastOffset` at the END of the loader (after the read loop completes), and ensure reactivation only fires AFTER `MarkLoadedIfNot` finishes by checking `session.IsLoaded()` before reactivating, OR accept the rare duplication (loader is fast, this race is tiny). Plan chooses the check: `Watcher.handleProgressFileChange` only calls `Reactivate()` when session is `completed` AND loaded.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code, tests, doc updates inside this repo
- **Post-Completion** (no checkboxes): manual verification against the toy project, responding on issue #283 after PR lands

## Implementation Steps

### Task 0: TDD reproduction — failing test for issue #283

**Files:**
- Modify: `pkg/web/watcher_test.go`

- [x] add test `TestWatcher_ResumesStreamingAfterFlockRace`:
  - [x] create a temp dir + progress file, write the standard header and some initial lines
  - [x] create SessionManager + Watcher (real fsnotify), start with context
  - [x] wait for initial discovery (poll session state until non-nil, sanity check)
  - [x] simulate the flock race: force the session state to `SessionStateCompleted` via `session.SetState(SessionStateCompleted)` and stop tailing via `session.StopTailing()` (this models what `RefreshStates` does when TryLockFile transiently succeeds)
  - [x] append new lines to the progress file
  - [x] assert within a generous timeout (~500ms) that the session state returns to `SessionStateActive` and the new lines arrive via SSE — tests poll for state and consume from `session.SSE` subscription or use the tail/event interface existing tests use
  - [x] run the test: `go test ./pkg/web/... -run TestWatcher_ResumesStreamingAfterFlockRace` — **MUST FAIL on master** (this confirms the bug is reproducible)
- [x] document the expected failure in the test with a comment referencing issue #283
- [x] **Do NOT commit the failing test as a separate commit** — it would land a red commit on the branch and break bisect. Keep the test uncommitted (or bundle with Task 5 commit) until it passes. If TDD hygiene is desired, `t.Skip` the test in its own commit then un-skip in Task 5

### Task 1: Expose offset and seek-to-offset on Tailer

**Files:**
- Modify: `pkg/web/tail.go`
- Modify: `pkg/web/tail_test.go`

- [x] add `(t *Tailer) Offset() int64` method returning `t.offset` under `t.mu`
- [x] add `(t *Tailer) StartFromOffset(offset int64) error`:
  - [x] if offset <= 0, treat as "seek to end" (same as `Start(false)` path) — documented in godoc, makes callers robust for fresh sessions
  - [x] open file, `Stat` to get size, clamp offset to `min(offset, fileSize)` to avoid seeking past EOF
  - [x] seek to clamped offset, set `t.offset = clampedOffset`, `t.inHeader = false` (offset>0 means we're past the header — if caller violates this contract, worst case is header detection breaks on rare edge cases; godoc warns)
  - [x] set `t.deferSections = false`, clear pending section state
  - [x] set `t.running = true`, init `stopCh`/`doneCh`, launch `tailLoop`
- [x] keep existing `Start(fromStart bool) error` unchanged to avoid breaking other callers
- [x] add test: `TestTailer_Offset` — writes lines, reads partial, verifies `Offset()` reflects bytes consumed (including LF and CRLF line endings)
- [x] add test: `TestTailer_StartFromOffset` — pre-writes content, starts tailer from middle, verifies only post-offset lines emit
- [x] add test: `TestTailer_StartFromOffset_BeyondFileSize` — offset > file size, verifies offset is clamped, no panic, no spurious events
- [x] add test: `TestTailer_StartFromOffset_ZeroFallsBack` — offset <= 0 behaves like `Start(false)`, seeks to end
- [x] run tests: `go test ./pkg/web/... -run Tailer` must pass before next task

### Task 2: Track last-read offset on Session

**Files:**
- Modify: `pkg/web/session.go`
- Modify: `pkg/web/session_test.go`

- [x] add private `lastOffset int64` field on `Session` (guarded by `s.mu`)
- [x] add **unexported** `getLastOffset() int64` and `setLastOffset(offset int64)` accessors (thread-safe). Keep them unexported — tests are in-package, loader and StopTailing are in the same package. Per the user's convention: do not export without an out-of-package caller
- [x] modify `StopTailing()` — before nilling `stopTailCh`, capture `tailer.Offset()` and store in `s.lastOffset` (under the same write lock). Pseudocode:
  ```go
  s.mu.Lock()
  if s.tailer != nil {
      s.lastOffset = s.tailer.Offset()
  }
  if s.stopTailCh != nil { close(s.stopTailCh); s.stopTailCh = nil }
  tailer := s.tailer
  s.mu.Unlock()
  if tailer != nil { tailer.Stop() }
  ```
- [x] add test: `TestSession_LastOffset` — verifies get/set thread-safety via concurrent goroutines (use the in-package unexported names)
- [x] add test: `TestSession_StopTailingCapturesOffset` — start tailing, write lines, wait for ingest (poll until events received), stop, verify `getLastOffset()` > 0 and roughly matches bytes written
- [x] run tests: `go test ./pkg/web/... -run Session` must pass before next task

### Task 3: Add Session.Reactivate() method

**Files:**
- Modify: `pkg/web/session.go`
- Modify: `pkg/web/session_test.go`

- [x] define private enum `tailerStartMode` with values `modeFromStart`, `modeFromEnd`, `modeResume` (type int, const block)
- [x] extract a private helper `startTailerLocked(mode tailerStartMode, offset int64) error` with **lock-held contract** (caller must hold `s.mu` write lock). Behavior:
  - create new `Tailer` at `s.Path`
  - dispatch on mode: `modeFromStart` → `tailer.Start(true)`, `modeFromEnd` → `tailer.Start(false)`, `modeResume` → `tailer.StartFromOffset(offset)`
  - on error, return error; do not store tailer or allocate stopTailCh
  - on success, set `s.tailer`, create `s.stopTailCh = make(chan struct{})`, launch `go s.feedEvents()` (goroutine acquires its own RLock; safe because caller unlocks after return)
- [x] refactor existing `StartTailing(fromStart bool) error` to use `startTailerLocked(modeFromStart or modeFromEnd, 0)` — keep public signature and behavior identical, existing tests still pass
- [x] add `Reactivate() error` on `Session`:
  - [x] `s.mu.Lock(); defer s.mu.Unlock()`
  - [x] if `s.tailer != nil && s.tailer.IsRunning()`, return nil (idempotent)
  - [x] capture `offset := s.lastOffset`
  - [x] choose mode: if `offset > 0` use `modeResume`, else use `modeFromEnd` (NOT `modeFromStart` — that would re-emit the whole file if lastOffset was never set; `modeFromEnd` is safe because the loader already loaded historical content into SSE replay)
  - [x] call `startTailerLocked(mode, offset)` — if it returns error, return it without touching state
  - [x] on success: `s.state = SessionStateActive` (only after tailer confirmed started — avoids lying about state if tailer-start fails)
- [x] add godoc on `Reactivate` explaining: called when a completed session receives a write event; resumes from `lastOffset` to avoid duplicating events already in SSE replay buffer
- [x] add test: `TestSession_Reactivate_ResumesFromOffset` — start tailing, stop (records offset), write more lines, Reactivate, verify SSE receives only new lines (no duplicates from before stop)
- [x] add test: `TestSession_Reactivate_Idempotent` — call Reactivate twice in quick succession, verify only one tailer exists and no duplicate events
- [x] add test: `TestSession_Reactivate_OnClosedSession` — call after Close, verify graceful error (no panic) and state not flipped to active
- [x] add test: `TestSession_Reactivate_FailedStartLeavesStateUnchanged` — use a session with non-existent path, verify Reactivate returns error and state stays completed
- [x] run tests: `go test ./pkg/web/... -run Session` must pass before next task

### Task 4: Record offset after loadProgressFileIntoSession

**Files:**
- Modify: `pkg/web/session_progress.go`
- Modify: `pkg/web/session_progress_test.go`

- [x] in `loadProgressFileIntoSession`, accumulate bytes read during the loop:
  ```go
  var bytesRead int64
  for {
      line, readErr := reader.ReadString('\n')
      bytesRead += int64(len(line))  // BEFORE trimLineEnding - len includes \n or \r\n as appropriate
      line = trimLineEnding(line)
      ...
      if readErr != nil { break }
  }
  session.setLastOffset(bytesRead)
  ```
- [x] **Key detail:** count `len(line)` BEFORE `trimLineEnding` — `ReadString('\n')` returns the delimiter included, so the raw length covers LF, CRLF, and no-trailing-newline (final partial read on EOF) correctly. Do NOT compute offset by summing trimmed length + constant — CRLF would be undercounted by 1 byte
- [x] add test: `TestLoadProgressFileIntoSession_RecordsOffset_LF` — load file with `\n`-only endings, verify `getLastOffset()` equals file byte size
- [x] add test: `TestLoadProgressFileIntoSession_RecordsOffset_CRLF` — load file with `\r\n` endings, verify offset equals byte size (critical regression guard)
- [x] add test: `TestLoadProgressFileIntoSession_EmptyFile` — verify offset is 0 (no regression)
- [x] add test: `TestLoadProgressFileIntoSession_NoTrailingNewline` — last line lacks `\n`, verify offset still equals byte size
- [x] run tests: `go test ./pkg/web/... -run Load` must pass before next task

### Task 5: Reactivate on write in Watcher

**Files:**
- Modify: `pkg/web/watcher.go`
- Modify: `pkg/web/watcher_test.go`

- [x] in `handleProgressFileChange(path)`, after the `Discover` call and the `startTailingIfNeeded` loop:
  - [x] look up the session for this path: `id := sessionIDFromPath(path); session := w.sm.Get(id)`
  - [x] if session != nil and `session.GetState() == SessionStateCompleted` and `session.IsLoaded()`:
    - [x] call `session.Reactivate()`, log any error at WARN level
  - [x] the `IsLoaded()` check ensures `loadProgressFileIntoSession` has finished (lastOffset is set) before we reactivate — avoids the race where a mid-load write triggers reactivation with lastOffset=0
- [x] keep existing `startTailingIfNeeded(id)` loop unchanged — it handles fresh-active-from-Discover; Reactivate handles the completed case
- [x] this design means the post-Discover sequence handles:
  - already-active session: no-op (startTailingIfNeeded sees tailing, skips; Reactivate sees not-completed, skips)
  - Discover flipped completed→active: startTailingIfNeeded starts tailing (from start)
  - Discover kept completed, write event occurred: Reactivate resumes from lastOffset
- [x] add test: `TestWatcher_ReactivatesCompletedSessionOnWrite`:
  - [x] create progress file, pre-populate with initial content
  - [x] start watcher, wait for initial discovery to load content (state=completed, lastOffset > 0)
  - [x] append new lines to file (triggers fsnotify Write)
  - [x] assert state transitions to `active` and NEW lines arrive via SSE (subscribe and consume events) — verify that the pre-existing content is NOT re-emitted after reactivation
- [x] add test: `TestWatcher_DoesNotReactivateActiveSession` — session already active + tailing, write event should not spawn duplicate tailer. Assertion: subscribe to SSE, write a line, verify exactly one event arrives (no duplication). Do NOT rely on tailer pointer identity or goroutine counts — those helpers don't exist and would add test-only APIs
- [x] add test: `TestWatcher_OnlyReactivatesWrittenPath` — create two progress files in same dir, both completed; write to one only; verify only that session is reactivated, the other stays completed
- [x] run tests: `go test ./pkg/web/... -run Watcher` must pass before next task
- [x] **re-run the Task 0 reproduction test** `TestWatcher_ResumesStreamingAfterFlockRace` — it must now pass (this is the green half of red-green-refactor for the bug fix)

### Task 6: Verify acceptance criteria

- [x] re-read issue #283 and confirm Option B semantics match this implementation
- [x] run full test suite: `make test`
- [x] verify race-free: `go test -race ./pkg/web/...`
- [x] run linter: `make lint` (fix all issues — no "pre-existing" dismissals)
- [x] run formatter: `make fmt`
- [x] check coverage: `go test -cover ./pkg/web/...` — must be >=80% for touched files, ideally no regression

### Task 7: End-to-end toy-project verification

- [ ] build fresh binary: `make build`
- [ ] prepare toy project: `./scripts/internal/prep-toy-test.sh`
- [ ] terminal A: start watch-mode dashboard — `.bin/ralphex -s -w /tmp/ralphex-test`
- [ ] terminal B: from `/tmp/ralphex-test`, run `.../ralphex docs/plans/fix-issues.md` (without `-s`)
- [ ] open dashboard in browser, confirm live log streaming
- [ ] leave running past the 5-second `RefreshStates` tick; confirm streaming does NOT freeze mid-run
- [ ] confirm session flips back to active after any transient "completed" (check browser devtools or sidebar)
- [ ] kill executor mid-run; confirm tailing stops cleanly (no zombie tailers) and session shows completed

### Task 8: [Final] Update documentation

**Files:**
- Modify: `CLAUDE.md` (only if an existing section describes watcher/session-state behavior)

- [ ] `grep -n "watch" CLAUDE.md` and `grep -n "RefreshStates\|session" CLAUDE.md`; if an existing section describes the affected behavior, append a one-line note about reactivation-on-write. Do NOT add a new section speculatively
- [ ] `grep -rn "watch mode\|RefreshStates\|flock" docs/` — if any existing doc describes the flock-based detection, update to mention the recovery path
- [ ] update this plan file: mark all tasks `[x]`
- [ ] move plan: `mkdir -p docs/plans/completed && git mv docs/plans/20260424-watch-mode-reactivate-tailing.md docs/plans/completed/`

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes, informational only*

**Manual verification:**
- pm2 deployment pattern (actual reporter's setup): separate `pm2 start ralphex -s -w /path` + tmux-launched executor
- verify dashboard survives a long run (>1 hour) without freezing

**External system updates:**
- after merge, comment on issue #283 linking the PR and asking pkondaurov to verify in his pm2 setup
- no changelog entry required during development (per project rules — release process handles it)
