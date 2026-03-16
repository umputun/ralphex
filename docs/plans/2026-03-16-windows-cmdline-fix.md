# Fix Windows "Command Line Too Long" Error in Plan Creation Mode

## Overview
Pass the Claude prompt via stdin instead of as a -p CLI argument to avoid the cmd.exe 8191-character
command-line limit on Windows. The make_plan.txt prompt after variable expansion can reach ~8100
chars, which together with other args exceeds the limit.

## Context
- Files involved: `pkg/executor/executor.go`, `pkg/executor/executor_test.go`, `CLAUDE.md`
- Related patterns: `execClaudeRunner` is the real runner; `cmdRunner` field on `ClaudeExecutor` is the test mock injection point
- The fix uses the existing distinction between nil cmdRunner (real execution) and non-nil cmdRunner (test mock) to preserve existing test assertions

## Development Approach
- Testing approach: Regular (code first, then tests)
- Complete each task fully before moving to the next
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Add stdin support to execClaudeRunner

**Files:**
- Modify: `pkg/executor/executor.go`

- [x] Add `stdin io.Reader` field to `execClaudeRunner` struct
- [x] In `execClaudeRunner.Run()`, set `cmd.Stdin = r.stdin` when `r.stdin` is non-nil
- [x] In `ClaudeExecutor.Run()`: when `cmdRunner` is nil (real execution path), construct `execClaudeRunner{stdin: strings.NewReader(prompt)}` and do NOT append `-p prompt` to args
- [x] When `cmdRunner` is non-nil (test mock path), keep appending `-p prompt` to args as before so existing tests remain valid
- [x] Run `make test` — all tests must pass before task 2

### Task 2: Add tests for stdin behavior

**Files:**
- Modify: `pkg/executor/executor_test.go`

- [ ] Add `TestExecClaudeRunner_StdinSet`: construct `execClaudeRunner{stdin: strings.NewReader("hello")}`, call `Run()` on a no-op command (e.g., `echo`), verify `cmd.Stdin` was set (use a spy or check behavior indirectly via a helper command that reads stdin)
- [ ] Add `TestClaudeExecutor_Run_RealRunner_NoPromptArg`: verify that when `cmdRunner` is nil, the args passed to the runner do NOT contain `-p`. Since we can't easily intercept the real runner's args, consider restructuring the test to inject a custom `CommandRunner` that checks args but simulates the real runner path — OR verify via the `execClaudeRunner` constructor logic directly
- [ ] Verify `TestClaudeExecutor_Run_Success` and other mock-based tests still pass (mock path still receives `-p prompt`)
- [ ] Run `make test` — all tests must pass before task 3

### Task 3: Update CLAUDE.md platform notes

**Files:**
- Modify: `CLAUDE.md`

- [ ] Under "Platform Support / Windows" section, add a note: prompts are passed via stdin to claude CLI to avoid cmd.exe 8191-char command-line limit
- [ ] Run `make test`, `make lint`, and `GOOS=windows GOARCH=amd64 go build ./...` — all must pass

### Task 4: Verify acceptance criteria

- [ ] `make test` — all tests pass
- [ ] `make lint` — no linter issues
- [ ] `make fmt` — code is formatted
- [ ] `GOOS=windows GOARCH=amd64 go build ./...` — Windows cross-compile succeeds
- [ ] Verify test coverage meets 80%+
- [ ] Move this plan to `docs/plans/completed/`
