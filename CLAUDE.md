# ralphex

Autonomous plan execution with Claude Code - Go rewrite of ralph.py.

## Build Commands

```bash
make build      # build binary to .bin/ralphex
make test       # run tests with coverage
make lint       # run golangci-lint
make fmt        # format code
make install    # install to ~/.local/bin
```

## Project Structure

```
cmd/ralphex/        # main entry point, CLI parsing
pkg/executor/       # claude and codex CLI execution
pkg/processor/      # orchestration loop, prompts, signals
pkg/progress/       # timestamped logging with color
docs/plans/         # plan files location
```

## Code Style

- Use jessevdk/go-flags for CLI parsing
- All comments lowercase except godoc
- Table-driven tests with testify
- 80%+ test coverage target

## Key Patterns

- Signal-based completion detection (COMPLETED, FAILED, REVIEW_DONE signals)
- Streaming output with timestamps
- Progress logging to files
- Multiple execution modes: full, review-only, codex-only

## Testing

```bash
go test ./...           # run all tests
go test -cover ./...    # with coverage
```

## End-to-End Testing

Unit tests mock external calls. After ANY code changes, run e2e test with a toy project to verify actual claude/codex integration and output streaming.

### Create Toy Project

```bash
# create test project
mkdir -p /tmp/ralphex-test && cd /tmp/ralphex-test
git init

# create a simple Go file with intentional issues
cat > main.go << 'EOF'
package main

import (
    "fmt"
    "os"
)

func main() {
    data, _ := os.ReadFile("config.txt") // BUG: error ignored
    fmt.Println("config:", string(data))
}

func unused() { // BUG: unused function
    fmt.Println("never called")
}
EOF

# create go.mod
go mod init ralphex-test

# create plan directory and plan file
mkdir -p docs/plans

cat > docs/plans/fix-issues.md << 'EOF'
# Plan: Fix Code Issues

## Overview
Fix linting issues in the toy project.

## Validation Commands
- `go build ./...`
- `go vet ./...`

### Task 1: Fix error handling
- [ ] Handle the error from os.ReadFile
- [ ] Either log and exit or handle gracefully

### Task 2: Remove unused function
- [ ] Remove the unused() function
- [ ] Verify go vet passes
EOF

# initial commit
git add -A
git commit -m "initial commit"
```

### Test Full Mode

```bash
cd /tmp/ralphex-test

# run ralphex in full mode using go run (tests current code)
go run ~/dev.umputun/ralphex/cmd/ralphex docs/plans/fix-issues.md

# or without argument (uses fzf to select)
go run ~/dev.umputun/ralphex/cmd/ralphex
```

**Expected behavior:**
1. Creates branch `fix-issues`
2. Phase 1: executes Task 1, then Task 2
3. Phase 2: first Claude review
4. Phase 2.5: codex external review
5. Phase 3: second Claude review
6. Moves plan to `docs/plans/completed/`

### Test Review-Only Mode

```bash
cd /tmp/ralphex-test
git checkout -b feature-test

# make some changes
echo "// comment" >> main.go
git add -A && git commit -m "add comment"

# run review-only (no plan needed)
go run ~/dev.umputun/ralphex/cmd/ralphex --review
```

### Test Codex-Only Mode

```bash
cd /tmp/ralphex-test

# run codex-only review
go run ~/dev.umputun/ralphex/cmd/ralphex --codex-only
```

### Monitor Progress

```bash
# live stream (use actual filename from ralphex output)
tail -f progress-fix-issues.txt

# recent activity
tail -50 progress-*.txt
```

## Development Workflow

**CRITICAL: After ANY code changes to ralphex:**

1. Run unit tests: `make test`
2. Run linter: `make lint`
3. **MUST** run end-to-end test with toy project (see above)
4. Monitor `tail -f progress-*.txt` to verify output streaming works

Unit tests don't verify actual codex/claude integration or output formatting. The toy project test is the only way to verify streaming output works correctly.
