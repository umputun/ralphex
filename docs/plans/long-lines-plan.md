# Fix Windows Command-Line Length Limits

## Overview

Windows has an 8191-character command-line limit (cmd.exe). The ClaudeExecutor already works around this by passing prompts via stdin instead of CLI arguments. Other executors and wrapper scripts still pass potentially unbounded prompt text as CLI arguments, which will fail on Windows and could fail on other platforms with very large prompts.

## Context

- `ClaudeExecutor` (Go): already fixed — passes prompt via `cmd.Stdin`
- `CustomExecutor` (Go): already safe — writes prompt to temp file, passes file path
- Git operations: safe — all commit messages and args are short, bounded strings
- `fzf`/`$EDITOR` invocations: safe — use stdin or temp file paths

## Success Criteria

- CodexExecutor passes prompt via stdin, not as a CLI argument
- Wrapper scripts pipe prompt to child processes instead of passing as CLI args
- All existing tests pass; new tests verify stdin-based prompt passing
- `make lint` and `make test` pass cleanly

### Task 1: Fix CodexExecutor to pass prompt via stdin

The codex CLI's `exec` subcommand reads prompt from stdin when no positional prompt argument is given. Mirror the ClaudeExecutor pattern:

- [x] Add `stdin io.Reader` field to `execCodexRunner` struct
- [x] Set `cmd.Stdin = r.stdin` in `execCodexRunner.Run()` when stdin is non-nil
- [x] In `CodexExecutor.Run()`, remove `args = append(args, prompt)` (line 128)
- [x] In `CodexExecutor.Run()`, create `stdinReader := strings.NewReader(prompt)` and pass it to `execCodexRunner`
- [x] Update `CodexRunner` interface if needed (or keep stdin as internal detail of `execCodexRunner`)
- [x] Update tests in `codex_test.go`: verify prompt is NOT in args, add test for stdin piping
- [ ] Run `make test` and `make lint`

### Task 2: Fix codex-as-claude.sh to pipe prompt via stdin

The script reads prompt from stdin (or `-p` flag), then passes it as a CLI argument to codex. Instead, pipe it to codex's stdin.

- [x] Remove `"$prompt"` from `codex_args` array in `codex-as-claude.sh`
- [x] Pipe prompt to codex via stdin (`printf '%s' "$prompt" | codex ...`)
- [ ] Verify script still works with both `-p` and stdin prompt sources (requires jq)
- [ ] Update tests in `codex-as-claude_test.sh` if they verify args

### Task 3: Fix gemini-as-claude.sh to pipe prompt via stdin

The script passes prompt as `-p "$prompt"` CLI argument to gemini CLI. If gemini supports stdin reading, pipe instead.

- [ ] Check if gemini CLI reads from stdin when `-p` is not provided
- [ ] Remove `-p "$prompt"` from `gemini_args` and pipe prompt via stdin, OR write prompt to temp file if stdin not supported
- [ ] Update tests in `gemini-as-claude_test.sh` if they verify args

### Task 4: Fix opencode wrapper scripts

Both `opencode-as-claude.sh` and `opencode-review.sh` pass prompt as CLI arguments.

- [ ] In `opencode-as-claude.sh`: remove prompt from args, pipe via stdin or use temp file
- [ ] In `opencode-review.sh`: remove prompt from args, pipe via stdin or use temp file (script already reads from a file, could pass file path directly)
- [ ] Update tests in respective test files

### Task 5: Verify and document

- [ ] Run full test suite: `make test`
- [ ] Run linter: `make lint`
- [ ] Cross-compile to verify Windows builds: `GOOS=windows GOARCH=amd64 go build ./...`
- [ ] Update CLAUDE.md platform support section if needed (mention codex stdin fix)
