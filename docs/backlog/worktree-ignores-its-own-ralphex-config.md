---
worth: later
where: cmd/ralphex/main.go:258
added: 2026-08-13
---
# worktree mode silently ignores the worktree's own .ralphex/config

Config is loaded once at `cmd/ralphex/main.go:258`, before any directory change. `runWithWorktree` chdirs
into the worktree at `:759`, and nothing reloads afterwards — the only other load is `config.LoadReadOnly`
on the `--init` early-exit path at `:1364`. `detectLocalDir` (`pkg/config/config.go:202`) therefore resolves
`.ralphex/` against the original cwd, so the main checkout's project config governs the whole run.

`.ralphex/config` is committable by design — `EnsureLocalGitignore` (`pkg/git/service.go:594`) ignores only
`.gitignore`, `progress/`, and `worktrees/`. A branch can legitimately carry a different `claude_command`,
`task_model`, or prompt override, and under `--worktree` it is dropped without a word.

Not a security matter — the effective config is the one the user is standing in, which is the safer of the
two readings. It is a plain surprise, and the Worktree Isolation section (`README.md:291-310`) does not
mention it. Two ways out: reload config after the chdir, or document that worktree runs use the main
checkout's project config. The second is smaller and probably right, since reloading would change which
config wins for anyone relying on today's behavior.

Surfaced investigating issue #431.
