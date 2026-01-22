# Plan: Web Dashboard for Ralphex

Add `--serve` flag that starts an HTTP server with real-time SSE streaming of execution output. View-only dashboard with phase navigation, collapsible sections, and text search. Purely additive - existing file + stdout logging continues unchanged.

## Validation Commands

- `go test ./...`
- `golangci-lint run`
- `go build -o .bin/ralphex ./cmd/ralphex`

### Task 1: HTTP Server Foundation

Create the web package with basic HTTP server infrastructure. This establishes the foundation for SSE streaming by implementing the pub/sub hub, event buffer for late-joining clients, and server skeleton with graceful shutdown.

- [x] Create `pkg/web/event.go` with Event struct (type, phase, section, text, timestamp, signal fields)
- [x] Create `pkg/web/buffer.go` with ring buffer (10k events max) and phase indexing for quick filtering
- [x] Create `pkg/web/hub.go` with SSE pub/sub hub (Subscribe, Unsubscribe, Broadcast methods)
- [x] Add unit tests for buffer and hub with 80%+ coverage
- [x] Create `pkg/web/server.go` with HTTP server skeleton, routes (`/`, `/events`, `/static/`)
- [x] Wire up graceful shutdown using context cancellation

### Task 2: SSE Streaming and BroadcastLogger

Implement the streaming infrastructure that connects execution output to web clients. The BroadcastLogger wraps the existing Logger using decorator pattern, so all output goes to file + stdout + SSE clients.

- [ ] Create `pkg/web/broadcast_logger.go` implementing processor.Logger interface
- [ ] BroadcastLogger wraps inner Logger and broadcasts events to hub + buffer
- [ ] Implement SSE handler at `/events` that sends history on connect, then streams new events
- [ ] Add unit tests for BroadcastLogger verifying all Logger methods broadcast correctly
- [ ] Add integration test verifying SSE connection receives events

### Task 3: Frontend Dashboard

Build the web UI using Go templates, htmx for SSE, and vanilla JavaScript. Functional-first design with monospace font, dark theme, and phase-based color coding matching terminal output.

- [ ] Create `pkg/web/templates/base.html` with header (plan name, branch, status), phase nav tabs, search input, output area
- [ ] Create `pkg/web/static/styles.css` with dark theme and phase colors (task=green, review=cyan, codex=magenta)
- [ ] Create `pkg/web/static/app.js` with EventSource SSE handling and output rendering
- [ ] Implement phase navigation (All/Task/Review/Codex tabs) filtering sections by phase
- [ ] Implement collapsible sections using `<details>` elements for each execution section
- [ ] Add auto-scroll to bottom with click-to-stop behavior

### Task 4: Search and CLI Integration

Add text search with highlighting and integrate the web server into the main CLI with `--serve` flag.

- [ ] Implement in-browser search with debounced input and regex-safe highlighting
- [ ] Add keyboard shortcut `/` to focus search, Escape to clear
- [ ] Add `--serve` and `--port` flags to cmd/ralphex/main.go using go-flags
- [ ] When `--serve` is passed, create web server and wrap Logger with BroadcastLogger
- [ ] Start HTTP server in goroutine, print "web dashboard: http://localhost:PORT" message
- [ ] Verify existing file + stdout logging still works with --serve

### Task 5: Testing and Documentation

Verify the feature works end-to-end with the toy project and update documentation.

- [ ] Run `./scripts/prep-toy-test.sh` to create test project
- [ ] Execute `.bin/ralphex --serve docs/plans/fix-issues.md` and verify dashboard in browser
- [ ] Verify: real-time streaming, phase transitions, collapsible sections, search highlighting
- [ ] Test late-joining client receives history correctly
- [ ] Update README.md with `--serve` flag documentation and example usage
- [ ] Verify all tests pass and linter is clean
