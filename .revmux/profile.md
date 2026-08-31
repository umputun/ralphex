# Project profile: ralphex

## What it is

A single-binary Go CLI that orchestrates Claude Code or codex to execute implementation plans
autonomously. It runs in the user's terminal from the root of a git repository, unattended, for hours
at a time. No server, no IDE plugin, no cloud service. Distributed via Homebrew, `go install`,
release binaries, deb/rpm packages, and Docker images.

The main surfaces: a phase pipeline (task loop, two review phases, external review, finalize), a git
layer that shells out to the `git` CLI (or a translation script for Mercurial), executor wrappers for
the claude and codex CLIs, an INI config layer with embedded defaults, prompt and agent template
files, a web dashboard with SSE streaming, and shell/Python wrapper scripts for Docker and alternative
providers.

## What a real failure looks like here

The tool runs unattended against the user's own repository and commits to it. So the failures that
matter are, in order:

1. **Data loss in the user's checkout.** Overwriting or destroying uncommitted work, clobbering files,
   leaving a repository in a broken state. Unrecoverable, and the user was not watching.
2. **Silent success.** Reporting a run completed when a step failed. The user comes back to a green
   summary and a job half done. Issue #439 and PR #441 exist because of exactly this, so a code path
   that swallows a failure and returns nil is a serious finding, not a style point.
3. **A wasted run.** A defect that surfaces only at the end burns an hour of wall clock and real
   claude/codex credits. A condition detectable at startup should abort at startup.
4. **Prompt and template defects.** `pkg/config/defaults/prompts/*` and `agents/*` are shipped
   instructions an LLM executes. A contradiction or a stale claim there makes every later run wrong,
   and it is invisible in the Go tests. Treat these with the same seriousness as executable code.
5. **Wrapper scripts.** `scripts/*.sh` and the Python Docker wrapper run on the user's machine, some
   as root inside the container. Shell quoting, unset variables and missing timeouts are real defects.

## Blast radius

Every user of a published release, on their own repositories. The maintainer cannot see their runs and
gets no telemetry — a bug reaches him as a GitHub issue days later, if at all. Docker images run as a
non-root user but mount the user's credentials and workspace.

## Reporting bar

Report anything that could destroy uncommitted work, report a failure as a success, waste a full
unattended run, or make a shipped prompt instruct an agent wrongly. Report a documented behavior that
the code does not deliver — README, CLAUDE.md and llms.txt are read by users and by agents, and a
false claim in them is a defect, not a nit.

Do not report: formatting, naming preferences, or a suggestion to restructure working code. A large
`if`/`else` that reads clearly is not a finding. Test coverage below the 80% target is a finding only
when the uncovered path is one of the failure classes above.

## Deliberate conventions — not findings

- **All comments lowercase except godoc.** Godoc starts with the element name and stays to a line or
  two. Comments answer "why", never "what"; tests carry no comments beyond a regression test's reason.
- **Private by default** for funcs, methods, types and fields. Export only for out-of-package callers.
- **A function called only from one struct's methods is a method of that struct**, regardless of
  whether it touches fields.
- **One test file per source file** — `foo.go` → `foo_test.go` only, never `foo_extra_test.go`. Large
  suites split into more functions in that file, not more files.
- **Table-driven tests with testify.** Mocks are generated with moq into a `mocks/` subpackage and are
  never hand-written or edited.
- **Interfaces are defined on the consumer side.** Accept interfaces, return concrete types.
- **Errors are wrapped** with `fmt.Errorf("context: %w", err)`. `wrapcheck` is enabled.
- **Flat structure, early returns, no `else if` chains.** Option struct at 4+ parameters.
- **`log.Printf`, never `fmt.Printf`.**
- **Long CLI flags are documented as `--flag=value`**, never `--flag value`.
- **Platform-specific code uses build tags** in `foo_unix.go` / `foo_windows.go` pairs. Windows is
  supported with documented limitations (no process-group signals, no flock).
- **Vendored dependencies** in `vendor/` — never a finding, and never review its contents.
- The linter is golangci-lint v2 with ~45 linters enabled and documented `gosec` exclusions in
  `.golangci.yml`. A finding a configured linter would already catch is redundant; the CI gate is
  zero issues.

## Where the risk concentrates

`pkg/git/` mutates the user's repository and is the only place data loss can originate. `pkg/executor/`
parses streaming output from external CLIs whose formats change without notice. `cmd/ralphex/main.go`
owns worktree lifecycle, signal handling and cleanup. `pkg/config/defaults/` is shipped instruction
text. Weight findings in those four areas above the rest.
