# Configurable VCS command with Mercurial support

## Overview
- add `vcs_command` config option so ralphex's git backend can call a custom VCS command instead of hardcoded `git`
- provide a reference `scripts/hg2git.sh` translation script that maps the ~15 git subcommands ralphex uses to Mercurial equivalents
- the script uses **phase-based commit logic**: on a draft (unsent) commit it uses `hg amend` instead of creating new commits; on a public commit (master-equivalent) it uses `hg commit` to create a new draft
- document the setup for using ralphex with hg repos (configurable backend + custom prompts for Claude)
- this enables hg users to use ralphex without any native hg backend code

## Context (from discovery)
- `pkg/git/external.go` — `externalBackend` hardcodes `"git"` in `run()` and 8 direct `exec.CommandContext` calls (constructor, `hasCommits`, `currentBranch`, `getDefaultBranch`, `diffStats`, `isIgnored`, `refExists`, `resolveRef`)
- `pkg/git/service.go` — `NewService(path, log)` creates `externalBackend` with no command override
- `pkg/config/values.go` — follows established pattern for string config fields (see `ClaudeCommand`, `CodexCommand`)
- `pkg/config/config.go` — `Config` struct assembled in `loadConfigFromDirs()` from `Values`
- `pkg/config/defaults/config` — INI format with commented sections
- `cmd/ralphex/main.go:616-621` — `openGitService()` calls `git.NewService(".", colors.Info())`
- custom prompts already supported via `~/.config/ralphex/prompts/*.txt` (user edits these for hg commands)

## Development Approach
- **testing approach**: regular (code first, then tests)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task**
- run tests after each change
- maintain backward compatibility — default `vcs_command = git` produces identical behavior

## Testing Strategy
- **unit tests**: config parsing/merging, backend command usage
- **integration**: verify default `git` behavior unchanged

## Progress Tracking
- mark completed items with `[x]` immediately when done
- add newly discovered tasks with + prefix
- document issues/blockers with ! prefix

## Implementation Steps

### Task 1: Add vcs_command to config system

**Files:**
- Modify: `pkg/config/values.go`
- Modify: `pkg/config/config.go`
- Modify: `pkg/config/defaults/config`
- Modify: `pkg/config/values_test.go`
- Modify: `pkg/config/defaults_test.go`

- [x] add `VcsCommand string` field to `Values` struct in `values.go`
- [x] add parsing in `parseValuesFromBytes()` — read `vcs_command` key
- [x] add merge logic in `mergeExtraFrom()` — merge if non-empty
- [x] add `VcsCommand string` field to `Config` struct in `config.go`
- [x] wire `VcsCommand` in `loadConfigFromDirs()` assembly block
- [x] add `vcs_command = git` entry to `defaults/config` in a new "version control" section between paths and error patterns
- [x] write tests for `vcs_command` parsing in `values_test.go`
- [x] write tests for `vcs_command` merging in `values_test.go`
- [x] update defaults test assertions if needed in `defaults_test.go`
- [x] run `go test ./pkg/config/...` — must pass before next task

### Task 2: Make git backend use configurable command

**Files:**
- Modify: `pkg/git/external.go`
- Modify: `pkg/git/service.go`
- Modify: `pkg/git/external_test.go`
- Modify: `pkg/git/service_test.go`

- [x] add `command string` field to `externalBackend` struct
- [x] change `newExternalBackend(path string)` signature to `newExternalBackend(path, command string)`
- [x] use `e.command` instead of `"git"` in `run()` method
- [x] use `e.command` in all 8 direct `exec.CommandContext(ctx, "git", ...)` calls — complete list:
  1. `newExternalBackend` constructor (line 28) — `rev-parse --show-toplevel` validation
  2. `hasCommits` (line 86) — `rev-parse HEAD`
  3. `currentBranch` (line 108) — `symbolic-ref --short HEAD`
  4. `getDefaultBranch` (line 132) — `symbolic-ref refs/remotes/origin/HEAD`
  5. `diffStats` (line 370) — `rev-parse baseRef`
  6. `isIgnored` (line 256) — `check-ignore -q`
  7. `refExists` (line 447) — `show-ref --verify --quiet`
  8. `resolveRef` (line 436) — `rev-parse --verify --quiet`
- [x] change `NewService` to accept optional vcs command: `NewService(path string, log Logger, vcsCmd ...string)`
- [x] pass command to `newExternalBackend`; default to `"git"` when not provided
- [x] write tests verifying `externalBackend` stores and uses the custom command
- [x] write tests verifying `NewService` with custom command parameter
- [x] run `go test ./pkg/git/...` — must pass before next task

### Task 3: Wire config to git service and fix .git check

**Files:**
- Modify: `cmd/ralphex/main.go`

- [x] change `openGitService(colors)` to `openGitService(colors, vcsCmd)` accepting vcs command parameter
- [x] pass `cfg.VcsCommand` from caller to `openGitService`
- [x] pass vcs command to `git.NewService(".", colors.Info(), vcsCmd)`
- [x] also pass vcs command when creating worktree git service (`git.NewService(".", req.Colors.Info(), ...)` around line 570)
- [x] fix `.git` directory check at line 228: when `vcs_command` is not `"git"`, skip `os.Stat(".git")` — rely on `NewService` → `rev-parse --show-toplevel` for repo validation instead (this check currently blocks pure hg repos)
- [x] run `go test ./cmd/ralphex/...` — must pass before next task

### Task 4: Write reference hg2git.sh script

**Files:**
- Create: `scripts/hg2git.sh`

**Core structure:**
- [x] create bash script with `set -euo pipefail`, dispatch on `$1` (subcommand) via case statement
- [x] add header comments documenting purpose, usage, and phase-based commit logic
- [x] make script executable (`chmod +x`)
- [x] **IMPORTANT: `set -e` handling** — several subcommands intentionally return non-zero exit codes (`check-ignore` exit 1, `show-ref` exit 1, `symbolic-ref` exit 128). Each probe-style command must be wrapped in `if`/`||` guards so `set -e` does not terminate the script prematurely

**Repository info commands:**
- [x] implement `rev-parse --show-toplevel` → `hg root`
- [x] implement `rev-parse HEAD` → `hg id -r . --template '{node}\n'`
- [x] implement `rev-parse --verify --quiet <ref>` → `hg log -r <ref> --template '' 2>/dev/null` exit code
- [x] implement `symbolic-ref --short HEAD` → check `hg log -r . --template '{phase}'`: if `public` return `master`, if `draft` return the active bookmark (or `draft` if no bookmark)
- [x] implement `symbolic-ref refs/remotes/origin/HEAD` → exit 1 (no remote refs in hg)
- [x] implement `show-ref --verify --quiet refs/heads/<name>` → `hg log -r "bookmark(<name>)" --template '' 2>/dev/null` exit code

**Status command (format conversion required):**
- [x] implement `status --porcelain` → run `hg status` and convert each line to git porcelain XY format:
  - hg `M file` → git ` M file` (modified, unstaged)
  - hg `A file` → git `A  file` (added)
  - hg `R file` → git `D  file` (removed)
  - hg `! file` → git ` D file` (missing/deleted in worktree)
  - hg `? file` → git `?? file` (untracked)
- [x] pass through any path arguments after `--` to `hg status` for per-file queries
- [x] handle `-uall` flag (no-op — hg already shows individual files by default)

**Branch commands:**
- [x] implement `checkout -b <name>` → `hg bookmark <name>` (creates bookmark on current commit; subsequent commits will carry it)
- [x] implement `checkout <name>` → `hg update <name>`

**Staging and file operations:**
- [x] implement `add` → `hg add` (ignore stderr for already-tracked files: `hg add "$@" 2>/dev/null || true`)
- [x] implement `add -A` → `hg addremove` (adds untracked + removes missing)
- [x] implement `mv` → `hg mv`

**Commit command (phase-based amend logic — critical):**
- [x] implement `commit -m <msg> [-- files...]` with phase detection:
  - parse `-m <msg>` and optional `-- file1 file2...` from arguments
  - run `hg log -r . --template '{phase}'` to get current phase
  - if phase is `public` → `hg commit -m <msg> [files...]` (creates new draft commit)
  - if phase is `draft` → `hg amend [files...]` (amends existing unsent commit, preserves original commit message)
  - **file-specific amend is critical**: `commitFiles()` in service.go calls `commit -m msg -- file1 file2` to commit only specific files (e.g., `.gitignore` only). Plain `hg amend` without file args would include ALL dirty files, accidentally folding unrelated changes. Must pass files through: `hg amend file1 file2`
  - this means the first commit from public creates a new draft, and all subsequent task/review commits amend into it (single-commit-per-diff workflow)

**Diff command:**
- [x] implement `diff --numstat <base>...HEAD` → run `hg diff -r "ancestor(., <base>)"` and parse unified diff output to produce per-file numstat format (`added\tremoved\tfile`)

**Ignore check:**
- [x] implement `check-ignore -q -- <path>` with two-stage approach:
  - if file exists: run `hg status -i <path>` and exit 0 if output is non-empty (ignored), exit 1 if empty (not ignored)
  - if file does NOT exist: `hg status -i` only works on existing files, so fall back to pattern matching against `.hgignore` directly (e.g., `grep -q <pattern> .hgignore`). ralphex probes ignore status on paths like `.ralphex/progress/progress-test.txt` BEFORE creating the directory, so this fallback is essential to avoid re-appending ignore patterns on every run

**Unsupported commands:**
- [x] implement `worktree *` → exit 1 with "worktree not supported with hg backend" to stderr
- [x] implement `log <base>..HEAD --oneline` → `hg log -r "::. and not ::<base>" --template '{node|short} {desc|firstline}\n'` (use `::` not `<base>::.` to handle diverged histories correctly)

**Script tests:**
- [x] write `scripts/hg2git_test.sh` — test the script functions against a parent hg repo:
  - test `rev-parse --show-toplevel` returns non-empty path
  - test `symbolic-ref --short HEAD` returns correct value based on phase (currently `draft` → should not return `master`)
  - test `status --porcelain` produces valid 2-char XY format
  - test commit phase detection (verify `hg log -r . --template '{phase}'` output parsing)
  - test `check-ignore` on a non-existent path (must not crash, must return correct exit code)
  - test `commit -m "test" -- file1` on draft phase produces `hg amend file1` (not bare `hg amend`)
  - test `worktree add` exits 1 with error message to stderr
- [x] run `bash scripts/hg2git_test.sh` — must pass before next task

### Task 5: Write documentation

**Files:**
- Create: `docs/hg-support.md`

- [x] write overview section explaining the two-part approach (configurable backend + custom prompts)
- [x] write setup steps: config, script placement, making executable
- [x] write custom prompts section with example hg prompt snippets for review_first.txt, review_second.txt
- [x] document `.hgignore` manual setup — ralphex writes `.gitignore` internally (hardcoded in `EnsureIgnored`); for hg repos, users must manually add `.ralphex/` patterns to `.hgignore` and the `.gitignore` file created by ralphex can be safely ignored or deleted
- [x] document limitations (no worktree mode, Claude Code's own git awareness, unbounded command surface)
- [x] add troubleshooting section (common issues, debugging tips)

### Task 6: Verify acceptance criteria

**Default git behaviour unchanged:**
- [x] verify `vcs_command = git` (default) produces identical behaviour — run `go test ./...` with no config changes
- [x] run full unit test suite: `go test ./...`
- [x] run linter: `make lint`
- [x] run formatter: `make fmt`

**hg2git.sh script validation against live hg repo:**
Run these tests from a Mercurial repo with a draft commit:
- [x] verify `scripts/hg2git.sh rev-parse --show-toplevel` returns the repo root path
- [x] verify `scripts/hg2git.sh rev-parse HEAD` returns a valid hex hash
- [x] verify `scripts/hg2git.sh symbolic-ref --short HEAD` returns a non-`master` value (current commit is draft phase)
- [x] verify `scripts/hg2git.sh status --porcelain` output uses 2-char XY format (each line matches pattern `^.{2} .+`)
- [x] verify phase-based commit logic: run `scripts/hg2git.sh` with a simulated commit and confirm it calls `hg amend` (not `hg commit`) since current phase is `draft` — test with `--dry-run` or by inspecting script output in debug mode
- [x] verify file-specific amend: `scripts/hg2git.sh commit -m "test" -- .gitignore` on draft phase must restrict to that file only (not amend all dirty files)
- [x] verify `check-ignore` on non-existent path does not crash and returns correct exit code
- [x] verify startup works without `.git` directory when `vcs_command` is set to hg2git.sh

**Config → backend → script integration:**
- [x] create a test config with `vcs_command = /path/to/scripts/hg2git.sh`
- [x] verify `git.NewService(".", log, "/path/to/scripts/hg2git.sh")` uses the script for all backend commands
- [x] verify test coverage meets project standard (80%+)

### Task 7: [Final] Update documentation

- [x] update CLAUDE.md with `vcs_command` config option
- [x] update llms.txt if needed (mention vcs_command in customization section)
- [x] move this plan to `docs/plans/completed/`

## Technical Details

**Config field**: `vcs_command` (string, default: `"git"`)
- stored in `Values.VcsCommand` and `Config.VcsCommand`
- supports tilde expansion via `expandTilde()` for script paths like `~/scripts/hg2git.sh`

**Backend change**: `externalBackend.command` replaces hardcoded `"git"` string
- `run()` uses `exec.CommandContext(ctx, e.command, args...)`
- all 8 direct exec calls updated similarly (constructor, hasCommits, currentBranch, getDefaultBranch, diffStats, isIgnored, refExists, resolveRef)
- error messages still include the subcommand name for diagnostics

**Script format**: `hg2git.sh` receives same args as `git` would
- dispatches on `$1` (subcommand)
- translates arguments and output format
- exit codes match git conventions (0 = success, 1 = not found, 128 = fatal)

**Phase-based commit logic** (the key hg behaviour):
- `hg log -r . --template '{phase}'` returns `public` or `draft`
- `public` = equivalent to being on master; `hg commit` creates a new draft commit
- `draft` = already on an unsent commit; `hg amend` folds changes into the existing commit
- **file-specific amend**: when `commit -m msg -- file1 file2` is received, the script must pass files through to `hg amend file1 file2` — plain `hg amend` without file args includes ALL dirty files, which would corrupt commits when `commitFiles()` is used (e.g., committing only `.gitignore`)
- this maps naturally to ralphex's workflow:
  1. ralphex calls `symbolic-ref --short HEAD` → script returns `master` (public) or bookmark/`draft` (draft)
  2. if `master` → `preparePlanBranch` creates a branch → script runs `hg bookmark <name>`
  3. first `commit` from public → `hg commit -m "msg"` → creates new draft
  4. all subsequent `commit` calls → `hg amend [files...]` → single commit grows with each task
  5. result: one amend-able commit per plan execution (single-diff model)

**Branch detection mapping** (`symbolic-ref --short HEAD`):
- `public` phase → return `master` → `IsMainBranch()` = true → ralphex creates branch
- `draft` phase → return bookmark name or `draft` → `IsMainBranch()` = false → ralphex skips branch creation (already working)

**Status format conversion** (`status --porcelain`):
- hg uses 1-char status, git porcelain uses 2-char XY format
- `extractPathFromPorcelain()` in `external.go` expects `line[3:]` (2 status chars + 1 space)
- the script must pad hg's single char to 2 chars to match

**Prompt customisation** (out of scope for this plan, but important context):
- the hg2git.sh script only handles commands from ralphex's Go backend (`pkg/git/external.go`)
- Claude's bash commands in prompts (e.g., `git log`, `git diff` in review prompts) are NOT intercepted by the script
- users must customise `~/.config/ralphex/prompts/*.txt` to replace `git log`/`git diff` with hg equivalents
- example: `git log {{DEFAULT_BRANCH}}..HEAD --oneline` → `hg log -r "::. and not ::{{DEFAULT_BRANCH}}" --template '{node|short} {desc|firstline}\n'`

**`.git` directory check** (main.go:228):
- ralphex checks `os.Stat(".git")` on startup; this blocks pure hg repos
- fix: when `vcs_command != "git"`, skip the `.git` check and rely on `NewService` → `rev-parse --show-toplevel` for repo validation
- note: in the user's setup (git repo inside hg repo), the `.git` check passes because the inner git repo has `.git/`, but the VCS operations resolve to the parent hg repo via `hg root`

**Known limitations of the script approach:**
- `.gitignore` is hardcoded in `EnsureIgnored` / `CommitIgnoreChanges` — hg users must maintain `.hgignore` manually
- `check-ignore` probe on non-existent paths requires `.hgignore` pattern matching fallback (not `hg status -i`)
- worktree mode is not supported with hg backend
- error messages in Go code will show the script path rather than `"git"` — cosmetic but potentially confusing in logs

## Post-Completion

**Manual verification:**
- test full ralphex execution with `vcs_command` pointing to hg2git.sh in a parent hg repo
- verify the amend-vs-commit logic by running a plan: first commit should be new (from public), subsequent should amend
- verify Claude Code's own bash commands don't break when vcs_command is set (Claude runs git/hg directly, not through the script)
- test custom prompts with hg-specific diff/log commands in review_first.txt, review_second.txt
