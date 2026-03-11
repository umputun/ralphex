# Mercurial Support

ralphex can work with Mercurial repositories through a two-part approach: a configurable VCS backend command and custom prompt files. The backend translates ralphex's internal git operations to hg equivalents, while custom prompts replace git commands in Claude's bash instructions with hg equivalents.

## How it works

ralphex's Go backend (`pkg/git/external.go`) shells out to a VCS command for repository operations like status checks, commits, and diffs. By default this command is `git`, but the `vcs_command` config option lets you point it at any script that accepts the same arguments and produces compatible output.

The included `scripts/hg2git.sh` reference script translates the ~15 git subcommands ralphex uses into Mercurial equivalents. It dispatches on the first argument (the git subcommand) and handles format conversion where needed (e.g., converting hg status output to git porcelain format).

There are two layers to consider:

1. **Backend commands** (handled by `hg2git.sh`): operations ralphex performs internally through its Go code, such as checking repo status, creating branches, committing changes, and computing diffs.

2. **Prompt commands** (handled by custom prompts): git commands that appear in ralphex's prompt templates and are executed by the coding model as bash commands. These are not intercepted by the translation script and must be replaced manually in custom prompt files.

## Setup

### 1. Place the translation script

Copy `scripts/hg2git.sh` to a permanent location and make it executable:

```bash
cp scripts/hg2git.sh ~/.config/ralphex/scripts/hg2git.sh
chmod +x ~/.config/ralphex/scripts/hg2git.sh
```

Or use the script directly from the ralphex source tree if you prefer.

### 2. Configure ralphex

Add `vcs_command` to your config file (`~/.config/ralphex/config` or `.ralphex/config`):

```ini
vcs_command = ~/.config/ralphex/scripts/hg2git.sh
default_branch = master
```

Set `default_branch` explicitly — auto-detection relies on git remote refs and local branch names that don't exist in hg repos, so it will not find the correct value. Use whatever ref represents your mainline (e.g., `master`, `main`, or a bookmark name).

Tilde expansion is supported for `vcs_command`, so `~/scripts/hg2git.sh` works. Absolute paths also work.

### 3. Customise prompts for hg commands

Copy the default prompts and replace git commands with hg equivalents:

```bash
# initialise custom prompts (if not already done)
ralphex --dump-defaults /tmp/ralphex-defaults
cp /tmp/ralphex-defaults/prompts/review_first.txt ~/.config/ralphex/prompts/review_first.txt
cp /tmp/ralphex-defaults/prompts/review_second.txt ~/.config/ralphex/prompts/review_second.txt
```

Edit each prompt file and replace git commands with hg equivalents. See the [custom prompts](#custom-prompts) section below for specific replacements.

### 4. Set up .hgignore

ralphex creates a `.gitignore` file internally (via `EnsureIgnored` in the Go code) to exclude its working files. In hg repos, you need to manually add these patterns to `.hgignore`:

```
syntax: glob
.ralphex/
```

The `.gitignore` file that ralphex creates can be safely ignored or deleted in hg repos.

## Custom prompts

The default prompts contain git commands that the coding model executes as bash commands during reviews. These are not intercepted by `hg2git.sh` and must be replaced in custom prompt files.

### Replacements for review_first.txt and review_second.txt

| Default (git) | Replacement (hg) |
|---|---|
| `git log {{DEFAULT_BRANCH}}..HEAD --oneline` | `hg log -r "::. and not ::{{DEFAULT_BRANCH}}" --template '{node\|short} {desc\|firstline}\n'` |
| `git diff {{DEFAULT_BRANCH}}...HEAD` | `hg diff -r "ancestor(., {{DEFAULT_BRANCH}})"` |
| `git diff --stat {{DEFAULT_BRANCH}}...HEAD` | `hg diff --stat -r "ancestor(., {{DEFAULT_BRANCH}})"` |
| `git commit -m "fix: ..."` | `hg amend` (if on a draft commit) or `hg commit -m "fix: ..."` (if on public) |

### Example: modified review_first.txt snippet

Replace the git-specific lines in the review steps:

```
- `hg log -r "::. and not ::{{DEFAULT_BRANCH}}" --template '{node|short} {desc|firstline}\n'` - see commit history
- `hg diff -r "ancestor(., {{DEFAULT_BRANCH}})"` - see actual code changes

1. Run `hg diff -r "ancestor(., {{DEFAULT_BRANCH}})"` and `hg diff --stat -r "ancestor(., {{DEFAULT_BRANCH}})"` to get the changes

3. Commit fixes: `hg amend` (folds changes into current draft commit)
```

### Note on commit style

The `hg2git.sh` script uses phase-based commit logic:

- When on a **public** commit (master-equivalent): `hg commit` creates a new draft
- When on a **draft** commit (unsent): `hg amend` folds changes into the existing commit

This produces a single-commit-per-diff workflow. When customising prompts, instruct Claude to use `hg amend` for fixes during review (since the working commit will be in draft phase by that point).

## .hgignore setup

ralphex's `EnsureIgnored` function writes to `.gitignore` internally. This is hardcoded in the Go code and not affected by `vcs_command`. For hg repos:

1. Create or update `.hgignore` in your repo root:

```
syntax: glob
.ralphex/
.ralphex/progress/*
.ralphex/worktrees/*
```

2. The `.gitignore` file created by ralphex can be:
   - Added to `.hgignore` itself (so hg ignores it)
   - Manually deleted after each run
   - Left in place (harmless in hg repos)

3. ralphex checks whether paths are ignored before writing ignore patterns. With `hg2git.sh`, the `check-ignore` translation looks at `.hgignore` patterns for non-existent paths. Make sure `.hgignore` has the `.ralphex/` pattern before the first run to avoid re-appending on every execution.

## Limitations

### Default branch must be set explicitly

Auto-detection of the default branch relies on git remote refs (`origin/HEAD`) and local branch names (`main`, `master`, etc.), neither of which exist in a typical hg repo. Without an explicit `default_branch` config value, ralphex falls back to hardcoded "master", which causes review diffs and prompt template variables (`{{DEFAULT_BRANCH}}`) to use an incorrect ref. Always set `default_branch` in your config (see step 2 above).

### Bash 4.0+ required

The `hg2git.sh` script uses associative arrays (`declare -A`) for diff stats parsing, which requires bash 4.0+. On macOS, the default `/usr/bin/bash` is bash 3.2; install a newer version via Homebrew (`brew install bash`) and ensure it appears first in your PATH, or use `#!/usr/bin/env bash` with Homebrew's bash.

### No worktree support

The `--worktree` flag is not supported with hg backends. The `hg2git.sh` script returns an error for any worktree commands. Use standard Mercurial workflows for parallel work instead.

### Copilot CLI's own git awareness

Copilot CLI has built-in git awareness and may run its own git commands independently of ralphex. These commands bypass the translation script entirely. If your repo has no `.git` directory, Copilot CLI's internal git operations will fail silently or produce errors. This typically does not affect ralphex's operation since Copilot CLI's own git usage is separate from ralphex's backend operations.

### Unbounded command surface

The `hg2git.sh` script only handles the specific git subcommands that ralphex's Go backend uses. If the coding model decides to run additional git commands in its bash output (beyond what's in the prompts), those will not be translated. Custom prompts should guide the coding model to use hg commands directly.

### .gitignore is hardcoded

The `EnsureIgnored` and `CommitIgnoreChanges` functions in the Go code create and commit `.gitignore` entries. With an hg backend, this creates a `.gitignore` file via hg commands, which is harmless but cosmetically noisy. Users must maintain `.hgignore` separately.

### Error messages hardcode "git"

When VCS operations fail, error messages in ralphex's output will say `git <subcommand>: ...` regardless of the configured VCS command. This is because the Go code hardcodes "git" in error format strings. This is cosmetic but may be initially confusing when reading logs for hg backend failures.

### Status format conversion

The `hg2git.sh` script converts hg's single-character status codes to git's two-character porcelain format. All hg modifications appear as unstaged in the git format (e.g., `M` becomes ` M`). This works correctly with ralphex's status parsing but does not represent hg's actual staging model (hg has no staging area).

## Troubleshooting

### "not a git repository" on startup

If ralphex exits with a `.git directory not found` error:
- Verify `vcs_command` is set in your config file
- When `vcs_command` is set to something other than `git`, ralphex skips the `.git` directory check and relies on `rev-parse --show-toplevel` (translated to `hg root`) for repo validation

### Script permission denied

```
chmod +x /path/to/hg2git.sh
```

Ensure the script has execute permissions. When using tilde paths like `~/scripts/hg2git.sh`, verify the expanded path is correct.

### hg command not found

The script calls `hg` directly. Ensure Mercurial is installed and `hg` is in your PATH.

### Unexpected amend behaviour

If commits are being amended when you expect new ones (or vice versa):
- Check the current phase: `hg log -r . --template '{phase}'`
- `public` phase creates new commits, `draft` phase amends
- After landing/pushing a commit, the phase changes to `public`, so the next `commit` call creates a new draft

### check-ignore returns wrong results

The `check-ignore` translation has two code paths:
- For existing files: uses `hg status -i` (reliable)
- For non-existent files: falls back to pattern matching against `.hgignore` (regex-based)

The fallback only supports regex syntax (Mercurial's default). If your `.hgignore` uses `syntax: glob`, patterns like `*.pyc` will be misinterpreted as regex by `grep -E`. To work around this, either use regex syntax for the `.ralphex/` patterns or add a separate `syntax: regexp` section at the end of `.hgignore` for them.

### Debugging the script

Run individual commands to see what the script produces:

```bash
# test repo detection
./scripts/hg2git.sh rev-parse --show-toplevel

# test current branch detection
./scripts/hg2git.sh symbolic-ref --short HEAD

# test status output format
./scripts/hg2git.sh status --porcelain

# test phase detection (used for commit logic)
hg log -r . --template '{phase}'
```

If a command fails, add `set -x` near the top of `hg2git.sh` (after `set -euo pipefail`) to see the exact hg commands being executed.
