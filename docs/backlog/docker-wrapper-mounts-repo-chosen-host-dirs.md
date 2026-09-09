---
worth: yes
where: scripts/ralphex-dk.sh:447
added: 2026-08-13
---
# docker wrapper bind-mounts host directories chosen by repository content

`add_symlink_targets(cwd / ".ralphex")` (`scripts/ralphex-dk.sh:445-447`) walks the repository's own
`.ralphex/` two levels deep, resolves every symlink it finds, and read-only bind-mounts the resolved
target's **parent** at its absolute host path whenever that parent sits under `$HOME`
(`symlink_target_dirs`, `:192-217`; `add_symlink_targets`, `:384-388`).

Git stores symlinks, so `.ralphex/` content selects host mounts. A committed
`.ralphex/x -> ../../../../.aws/credentials` resolves to a parent of `~/.aws`, which is then mounted into a
container running an agent with `--dangerously-skip-permissions` and full network access. That contradicts
`README.md:406`, which states the container cannot access "SSH keys, AWS credentials, or other secrets in
`~/.ssh`, `~/.aws`". A symlink resolving to `$HOME/.config` yields parent `$HOME` — the whole home
directory.

The two sibling calls are fine and should stay: `:414` on `~/.claude` and `:420` on `~/.codex` walk
user-controlled directories, which is the feature's purpose (following a symlinked `~/.claude/skills`).
Only `:447` scans repository content.

Several defensible shapes for the fix, which is why it was not done inline: drop the repo-side walk
entirely (the workspace mount already covers `.ralphex/`, so only out-of-repo targets are affected); keep
it but refuse targets outside the repository; or keep it and warn. Worth deciding before touching the code.

Caveats on the evidence: mounts are read-only, and a relative symlink has to guess the repo's depth below
`$HOME` — though non-resolving variants are silently dropped by the `is_dir()` filter, so several can be
committed at once. The mount decision was verified by calling the helper directly; no container was run.

Surfaced investigating issue #431, which reported a different and already-declined trust question about
`.ralphex/config`. This one is independent of that report and is a mismatch with a documented claim.
