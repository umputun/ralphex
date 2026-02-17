# Replace bufio.Scanner with unbounded line reader

## Overview
- Replace `bufio.Scanner` with `bufio.Reader` for stream parsing to eliminate the 64MB line length limit
- Fixes issue #118: `bufio.Scanner: token too long` when claude outputs large benchmark results
- The 64MB limit has been hit 3 times already (previously increased from default 64KB to 16MB to 64MB)
- `bufio.Reader.ReadString('\n')` has no upper bound on line length, solving the problem permanently

## Context (from discovery)
- files/components involved:
  - `pkg/executor/executor.go` — `parseStream()`, `MaxScannerBuffer` constant
  - `pkg/executor/codex.go` — `processStderr()`
  - `pkg/executor/custom.go` — `processOutput()`
  - `pkg/web/session_manager.go` — `ParseProgressHeader()`, `loadProgressFileIntoSession()`
  - existing large-line tests in all executor and web test files
- all 5 locations use identical pattern: 64KB initial buffer, 64MB max, default ScanLines
- safe to skip (small in-memory data, no large lines): `pkg/web/plan.go`, `pkg/executor/procgroup_test.go`, `pkg/config/defaults.go`

## Development Approach
- **testing approach**: Regular (code first, then update tests)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task** — no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run tests after each change
- maintain backward compatibility

## Testing Strategy
- **unit tests**: update existing large-line tests to verify lines >64MB work (use 65MB+ test lines)
- **unit tests**: add test for the new shared helper function
- **existing tests**: all existing large-line tests must continue to pass with the new reader implementation

## Progress Tracking
- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope

## Implementation Steps

### Task 1: Create shared line reader helper

**Files:**
- Create: `pkg/executor/linereader.go`
- Create: `pkg/executor/linereader_test.go`

- [ ] create `readLines(ctx context.Context, r io.Reader, handler func(string))` in `pkg/executor/linereader.go`
- [ ] use `bufio.NewReader` with `ReadString('\n')` internally, no line length limit
- [ ] handle `io.EOF` correctly (process final line without trailing newline)
- [ ] return error on context cancellation or read errors (not EOF)
- [ ] strip trailing `\n` and `\r\n` from lines before passing to handler, matching Scanner.ScanLines behavior
- [ ] write tests in `linereader_test.go`: basic multi-line reading
- [ ] write tests: context cancellation mid-read
- [ ] write tests: line >64MB (verify no limit)
- [ ] write tests: empty lines, final line without newline
- [ ] run `go test ./pkg/executor/` — must pass before task 2

### Task 2: Migrate executor parseStream to use readLines

**Files:**
- Modify: `pkg/executor/executor.go`

- [ ] replace `bufio.Scanner` in `ClaudeExecutor.parseStream()` with `readLines()` call
- [ ] move per-line JSON parsing and signal detection into the handler callback
- [ ] verify error propagation matches current behavior (wrapped as `"stream read: %w"`)
- [ ] update existing large-line tests to include a 65MB+ line case
- [ ] run `go test ./pkg/executor/` — must pass before task 3

### Task 3: Migrate codex processStderr to use readLines

**Files:**
- Modify: `pkg/executor/codex.go`

- [ ] replace `bufio.Scanner` in `CodexExecutor.processStderr()` with `readLines()` call
- [ ] move `shouldDisplay()` filtering and tail buffer logic into the handler callback
- [ ] verify error propagation matches current behavior (wrapped as `"read stderr: %w"`)
- [ ] update existing large-line test to include a 65MB+ line case
- [ ] run `go test ./pkg/executor/` — must pass before task 4

### Task 4: Migrate custom processOutput to use readLines

**Files:**
- Modify: `pkg/executor/custom.go`

- [ ] replace `bufio.Scanner` in `CustomExecutor.processOutput()` with `readLines()` call
- [ ] move output accumulation and signal detection into the handler callback
- [ ] verify error propagation matches current behavior (wrapped as `"read output: %w"`)
- [ ] update existing large-output test to include a 65MB+ line case
- [ ] run `go test ./pkg/executor/` — must pass before task 5

### Task 5: Migrate session_manager scanners to bufio.Reader

**Files:**
- Modify: `pkg/web/session_manager.go`

Note: these functions don't have a context parameter and `ParseProgressHeader` needs early termination,
so use `bufio.Reader` directly instead of the `readLines` helper.

- [ ] replace `bufio.Scanner` in `ParseProgressHeader()` with `bufio.NewReader` + `ReadString('\n')` loop
- [ ] handle early return at `"---"` separator with `break`
- [ ] replace `bufio.Scanner` in `loadProgressFileIntoSession()` with `bufio.NewReader` + `ReadString('\n')` loop
- [ ] strip trailing `\n`/`\r\n` from lines, matching previous Scanner.ScanLines behavior
- [ ] update existing large-buffer tests to include a 65MB+ line case
- [ ] run `go test ./pkg/web/` — must pass before task 6

### Task 6: Clean up MaxScannerBuffer constant

**Files:**
- Modify: `pkg/executor/executor.go`

- [ ] remove `MaxScannerBuffer` constant (no longer needed)
- [ ] verify no remaining references to `MaxScannerBuffer` across codebase
- [ ] run `go test ./...` — must pass before task 7

### Task 7: Verify acceptance criteria
- [ ] verify lines >64MB are handled without error in all stream parsers
- [ ] verify context cancellation still works in all parsers
- [ ] run full unit test suite: `go test ./...`
- [ ] run linter: `golangci-lint run --max-issues-per-linter=0 --max-same-issues=0`
- [ ] verify test coverage for new `linereader.go` is 80%+

### Task 8: [Final] Update documentation
- [ ] verify no documentation references MaxScannerBuffer
- [ ] move this plan to `docs/plans/completed/`

## Technical Details

**Current pattern (bufio.Scanner):**
```go
scanner := bufio.NewScanner(r)
buf := make([]byte, 0, 64*1024)
scanner.Buffer(buf, MaxScannerBuffer) // 64MB hard limit
for scanner.Scan() {
    line := scanner.Text()
    // process line
}
if err := scanner.Err(); err != nil {
    return fmt.Errorf("stream read: %w", err)
}
```

**New pattern (bufio.Reader via shared helper):**
```go
err := readLines(ctx, r, func(line string) {
    // process line
})
if err != nil {
    return fmt.Errorf("stream read: %w", err)
}
```

**readLines internals:**
```go
func readLines(ctx context.Context, r io.Reader, handler func(string)) error {
    reader := bufio.NewReader(r)
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        default:
        }
        line, err := reader.ReadString('\n')
        if len(line) > 0 {
            line = strings.TrimRight(line, "\r\n")
            handler(line)
        }
        if err != nil {
            if err == io.EOF {
                return nil
            }
            return err
        }
    }
}
```

**Web package (`ParseProgressHeader`, `loadProgressFileIntoSession`):** use `bufio.Reader` directly
(not the `readLines` helper) because neither function has a context parameter and `ParseProgressHeader`
needs early break at `"---"` separator. Context cancellation is not needed for local file reads.

**Note on context cancellation in `readLines`:** the `select/default` pattern provides cooperative
cancellation (checked between reads, does not interrupt a blocking `ReadString` call). This matches
the current Scanner behavior where `ctx.Done()` is checked between `Scan()` calls.

## Post-Completion

**Manual verification:**
- run e2e test with toy project to verify streaming output works correctly
- Related to #118 — close issue after merge
