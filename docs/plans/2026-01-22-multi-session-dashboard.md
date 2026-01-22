# Plan: Multi-Session Web Dashboard

Decouple the web dashboard from plan execution to enable standalone monitoring of multiple ralphex sessions. The dashboard discovers progress files, detects active sessions via file locking, and streams updates in real-time. Supports both local mode (current directory) and watch mode (recursive monitoring of specified directories).

## Validation Commands

- `make test`
- `make lint`

### Task 1: Session Discovery & State Detection

Implement the core session management layer that discovers progress files and determines their state. This creates the foundation for multi-session support by replacing the single-execution model with a registry of sessions.

- [x] Create `pkg/web/session.go` with Session struct (ID, path, metadata, active flag, Buffer, Hub)
- [x] Create `pkg/web/session_manager.go` with SessionManager that holds session registry
- [x] Implement `Discover()` method that globs `progress-*.txt` in a directory
- [x] Implement flock-based `IsActive()` detection using `syscall.Flock` with `LOCK_EX|LOCK_NB`
- [x] Parse progress file headers to extract metadata (plan path, branch, mode, start time)
- [x] Update `pkg/progress/logger.go` to acquire flock on progress file during execution
- [x] Add unit tests for session discovery and state detection

### Task 2: File Tailing & Event Streaming

Implement file tailing for active sessions to stream log lines as events. This enables the dashboard to show live updates for sessions it didn't start, by watching the progress file rather than receiving events directly from the logger.

- [x] Create `pkg/web/tail.go` with Tailer that watches a file for new content
- [x] Implement line parsing that converts progress file lines to Event structs
- [x] Handle section headers (`--- section name ---`) and timestamp-prefixed lines
- [x] Integrate Tailer with Session - active sessions get a Tailer feeding their Buffer
- [x] Implement graceful stop when session completes (flock released)
- [x] Add unit tests for tailing and line parsing

### Task 3: REST API for Sessions

Extend the web server API to support multiple sessions. Clients can list available sessions, then connect to a specific session's event stream or fetch its plan.

- [x] Add `GET /api/sessions` endpoint returning list of sessions with metadata
- [x] Update `GET /events` to accept `?session=<id>` query parameter
- [x] Update `GET /api/plan` to accept `?session=<id>` query parameter
- [x] Modify Server to use SessionManager instead of single Buffer/Hub
- [x] Handle session not found errors with appropriate HTTP status
- [x] Add endpoint tests for session-aware API

### Task 4: Watch Mode with fsnotify

Add recursive directory watching for multi-project monitoring. Users can specify directories to watch, and the dashboard automatically discovers new progress files as they appear.

- [ ] Add `--watch` CLI flag (repeatable) for specifying watch directories
- [ ] Add `watch_dirs` config option with comma-separated paths
- [ ] Implement precedence: CLI flags > config > current directory (default)
- [ ] Create `pkg/web/watcher.go` using fsnotify for recursive directory watching
- [ ] Filter events to only `progress-*.txt` file creates/modifies
- [ ] Integrate watcher with SessionManager to auto-register new sessions
- [ ] Add unit tests for watcher logic

### Task 5: Frontend Session Sidebar

Update the web UI to display a sidebar with all discovered sessions. Users can click a session to view its output, with visual indicators distinguishing active from completed sessions.

- [ ] Add session sidebar HTML structure to `pkg/web/templates/base.html`
- [ ] Implement `fetchSessions()` in app.js calling `/api/sessions`
- [ ] Render session list sorted by recency (most recent first)
- [ ] Add visual indicators: pulsing dot for active, checkmark for completed
- [ ] Implement click-to-switch that updates SSE connection to selected session
- [ ] Persist selected session in URL hash or localStorage
- [ ] Update styles.css with sidebar styling (collapsible, responsive)
- [ ] Poll `/api/sessions` periodically (every 5s) to discover new sessions
