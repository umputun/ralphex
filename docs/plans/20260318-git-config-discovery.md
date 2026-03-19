# Git Config Discovery for Docker Wrapper

## Overview
Replace hardcoded `~/.gitconfig` mount in the Docker wrapper with dynamic discovery using `git config --list --show-origin --global`. This captures all global git config files including:
- `~/.gitconfig` (traditional location)
- `~/.config/git/config` (XDG location)
- Any files referenced via `[include]` or `[includeIf]` directives

**Success Criteria:** After implementation, `--dry-run` output should show mounts for all git config files discovered via `git config --list --show-origin --global`, plus fallback mounts for standard paths if they exist.

## Context
- **File**: `scripts/ralphex-dk.sh` (Python script despite .sh extension)
- **Current behavior**: Only mounts `~/.gitconfig` explicitly (lines 404-407)
- **Related code**: `get_global_gitignore()` function for `core.excludesFile` (kept as-is)
- **Pattern to follow**: Dual-mount logic from gitignore handling (lines 409-424)

## Development Approach
- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- Run tests after each change: `python scripts/ralphex-dk.sh --test`

## Implementation Steps

### Task 1: Add get_global_git_config_files() function

**Files:**
- Modify: `scripts/ralphex-dk.sh`
- Modify: `scripts/ralphex-dk/ralphex_dk_test.py`

- [ ] add `get_global_git_config_files()` function after `get_global_gitignore()` (around line 276)
- [ ] run `git config --list --show-origin --global` via subprocess
- [ ] filter lines that start with `file:` prefix (skip `command line:` and other origins)
- [ ] parse output lines: split on first tab, extract path after `file:` prefix
- [ ] return unique list of `Path` objects for files that exist
- [ ] handle errors gracefully (return empty list if git fails)
- [ ] update imports in test file to include `get_global_git_config_files`
- [ ] add test for `get_global_git_config_files()` with mocked subprocess output
- [ ] add test for `get_global_git_config_files()` when git command fails
- [ ] add test for filtering non-file origins (e.g., `command line:`)
- [ ] run tests: `python scripts/ralphex-dk.sh --test` - must pass before next task

### Task 2: Update build_volumes() to use dynamic discovery

**Files:**
- Modify: `scripts/ralphex-dk.sh`
- Modify: `scripts/ralphex-dk/ralphex_dk_test.py`

- [ ] replace hardcoded `~/.gitconfig` mount block (lines 404-407) with loop over `get_global_git_config_files()`
- [ ] add fallback: also mount `~/.gitconfig` and `~/.config/git/config` if they exist but weren't in discovery output (handles minimal/empty configs)
- [ ] apply dual-mount logic for each config file (same pattern as gitignore):
  - if file is under `$HOME`: mount at `/home/app/<relative>` AND original absolute path
  - otherwise: mount at original path only
- [ ] deduplicate mounts (same file shouldn't be mounted twice)
- [ ] add comment explaining the discovery approach
- [ ] add test for XDG config path mounting in `build_volumes()`
- [ ] add test for dual-mount behavior (remapped + original paths)
- [ ] add test for fallback mounting of standard paths
- [ ] run tests: `python scripts/ralphex-dk.sh --test` - must pass before next task

### Task 3: Update documentation

**Files:**
- Modify: `scripts/ralphex-dk/README.md`

- [ ] review existing README structure to find appropriate location for new section
- [ ] document git config discovery behavior in README
- [ ] explain that all global git config files are automatically discovered and mounted
- [ ] mention XDG config support (`~/.config/git/config`)
- [ ] note that `[include]` and `[includeIf]` referenced files are also mounted

### Task 4: Verify and finalize

- [ ] run full test suite: `python scripts/ralphex-dk.sh --test`
- [ ] test manually with `--dry-run` to verify mounts appear correctly
- [ ] commit changes with descriptive message
- [ ] move this plan to `docs/plans/completed/`

## Technical Details

**git config output format:**
```
file:/Users/alice/.config/git/config	user.name=Alice
file:/Users/alice/.gitconfig	user.email=alice@example.com
file:/Users/alice/.config/git/config.d/work.conf	includeIf.gitdir:~/work/.path=...
```

**Parsing approach:**
- Split each line on first tab character
- Extract path after `file:` prefix
- Deduplicate paths (same file may appear multiple times)
- Filter to existing files only

**Dual-mount logic:**
```python
if config_file.is_relative_to(home):
    # mount at /home/app/<relative> for tilde refs in .gitconfig
    dst = "/home/app/" + str(config_file.relative_to(home))
    add(src, dst, ro=True)
    # also mount at original absolute path for expanded refs
    original = str(config_file)
    if original != dst:  # always true for home-relative paths
        add(src, original, ro=True)
else:
    # non-home path: mount at original location only
    add(src, str(config_file), ro=True)
```

**Deduplication:** Track mounted destinations in a set to avoid duplicate mounts when the same file appears from both discovery and fallback paths.
