# hg2git

Translates git subcommands to Mercurial equivalents, allowing ralphex to work with Mercurial repositories.

## How it works

The script receives the same arguments as git and dispatches on the subcommand via a case statement. It maps common git operations (rev-parse, status, log, commit, etc.) to their Mercurial equivalents.

Phase-based commit logic:
- On a public commit (master-equivalent): `hg commit` creates a new draft
- On a draft commit (unsent): `hg amend` folds changes into the existing commit
- This produces a single-commit-per-diff workflow

## Configuration

Add to `~/.config/ralphex/config` or `.ralphex/config`:

```ini
vcs_command = /path/to/scripts/hg2git/hg2git.sh
```

## Supported commands

`rev-parse`, `symbolic-ref`, `show-ref`, `status`, `log`, `diff`, `add`, `commit`, `checkout`, `check-ignore`, `worktree` (returns error - not supported in hg)

## Testing

```bash
# requires an hg repo accessible from parent directories
bash scripts/hg2git/hg2git_test.sh
```

Tests verify subcommand translation against a live Mercurial repository. Skips gracefully if no hg repo is found.

## Requirements

- `hg` (Mercurial) installed and accessible
- A Mercurial repository to operate on
