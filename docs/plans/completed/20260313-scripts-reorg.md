# Reorganize scripts/ directory into subdirectories

## Overview
- Reorganize flat `scripts/` directory into per-script subdirectories with README.md for each
- Extract embedded tests from `ralphex-dk.sh` (Python, 2848 lines) into separate test file
- Move bash scripts with their test files into subdirectories
- Keep backward compatibility for `scripts/ralphex-dk.sh` curl install URL via symlink
- Update all active references across the project

## Context (from brainstorm)
- 12 scripts in flat directory, 5032 lines total
- `ralphex-dk.sh` is Python (2848 lines, ~2000 lines are tests) - users curl-install it
- 4 bash scripts have separate `_test.sh` files
- 26 files reference `scripts/` paths
- GitHub raw.githubusercontent.com follows symlinks (verified)
- Python import from same directory works with underscore filename (verified)

## Solution Overview

Target structure:
```
scripts/
├── ralphex-dk.sh -> ralphex-dk/ralphex_dk.py   (symlink)
├── ralphex-dk/
│   ├── ralphex_dk.py
│   ├── ralphex_dk_test.py
│   └── README.md
├── hg2git/
│   ├── hg2git.sh
│   ├── hg2git_test.sh
│   └── README.md
├── opencode/
│   ├── opencode-as-claude.sh
│   ├── opencode-as-claude_test.sh
│   ├── opencode-review.sh
│   ├── opencode-review_test.sh
│   └── README.md
├── codex-as-claude/
│   ├── codex-as-claude.sh
│   └── README.md
└── internal/
    ├── prep-toy-test.sh
    ├── prep-review-test.sh
    ├── init-docker.sh
    ├── update-plugin-version.sh
    └── README.md
```

Key decisions:
- `ralphex-dk.sh` symlink at original path for curl backward compat
- No symlinks for other scripts (doc references update is enough)
- Completed plan files are NOT updated (historical records)
- `--test` flag kept in `ralphex_dk.py` as a thin shim that imports and runs `ralphex_dk_test.py`

## Development Approach
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include verification** that tests still pass
- **CRITICAL: all tests must pass before starting next task**
- Run tests after each change
- Maintain backward compatibility for curl install URL

## Testing Strategy
- After each script move: verify script still executes from new path
- After ralphex-dk test extraction: run `python3 scripts/ralphex-dk.sh --test` (via symlink)
- After ralphex-dk test extraction: run `python3 scripts/ralphex-dk/ralphex_dk_test.py` (direct)
- After all moves: run `go test ./...` and `golangci-lint run`
- After push: verify `curl` URL works against branch

## Progress Tracking
- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix

## Implementation Steps

### Task 1: Move internal scripts (not user-facing) into scripts/internal/

**Files:**
- Move: `scripts/init-docker.sh` → `scripts/internal/init-docker.sh`
- Move: `scripts/prep-toy-test.sh` → `scripts/internal/prep-toy-test.sh`
- Move: `scripts/prep-review-test.sh` → `scripts/internal/prep-review-test.sh`
- Move: `scripts/update-plugin-version.sh` → `scripts/internal/update-plugin-version.sh`
- Create: `scripts/internal/README.md`

- [x] create `scripts/internal/` directory
- [x] move scripts using `git mv`
- [x] create README.md listing all internal scripts with brief descriptions
- [x] verify each script executes from new path

### Task 2: Move codex-as-claude into subdirectory

**Files:**
- Move: `scripts/codex-as-claude.sh` → `scripts/codex-as-claude/codex-as-claude.sh`
- Create: `scripts/codex-as-claude/README.md`

- [x] create `scripts/codex-as-claude/` directory
- [x] move script using `git mv`
- [x] create README.md with description, usage, and examples

### Task 3: Move hg2git into subdirectory

**Files:**
- Move: `scripts/hg2git.sh` + `scripts/hg2git_test.sh` → `scripts/hg2git/`
- Create: `scripts/hg2git/README.md`

- [x] move `hg2git.sh` and `hg2git_test.sh` to `scripts/hg2git/`
- [x] verify `hg2git_test.sh` uses SCRIPT_DIR-relative paths (no changes needed)
- [x] run `bash scripts/hg2git/hg2git_test.sh` to verify tests pass
- [x] create README.md with description, usage, and examples

### Task 4: Move opencode scripts into scripts/opencode/

**Files:**
- Move: `scripts/opencode-as-claude.sh` + `scripts/opencode-as-claude_test.sh` → `scripts/opencode/`
- Move: `scripts/opencode-review.sh` + `scripts/opencode-review_test.sh` → `scripts/opencode/`
- Create: `scripts/opencode/README.md`

- [x] create `scripts/opencode/` directory
- [x] move all 4 opencode files using `git mv`
- [x] verify test scripts use SCRIPT_DIR-relative paths (no changes needed)
- [x] run `bash scripts/opencode/opencode-as-claude_test.sh` to verify tests pass
- [x] run `bash scripts/opencode/opencode-review_test.sh` to verify tests pass
- [x] create README.md covering both scripts with description, usage, and examples

### Task 5: Extract ralphex-dk tests and create symlink

**Files:**
- Move: `scripts/ralphex-dk.sh` → `scripts/ralphex-dk/ralphex_dk.py`
- Create: `scripts/ralphex-dk/ralphex_dk_test.py` (extracted tests)
- Create: `scripts/ralphex-dk.sh` (symlink → `ralphex-dk/ralphex_dk.py`)
- Create: `scripts/ralphex-dk/README.md`

- [x] create `scripts/ralphex-dk/` directory
- [x] copy `scripts/ralphex-dk.sh` to `scripts/ralphex-dk/ralphex_dk.py`
- [x] extract all test classes from `ralphex_dk.py` into `ralphex_dk_test.py`
- [x] add imports in `ralphex_dk_test.py` to import functions/classes from `ralphex_dk`
- [x] keep `--test` flag in `ralphex_dk.py` as thin shim that imports and runs test module
- [x] remove old `scripts/ralphex-dk.sh`, create symlink: `scripts/ralphex-dk.sh -> ralphex-dk/ralphex_dk.py`
- [x] verify `python3 scripts/ralphex-dk.sh --test` works (via symlink)
- [x] verify `cd scripts/ralphex-dk && python3 ralphex_dk_test.py` works (direct import)
- [x] create README.md with description, install instructions, usage
- [x] add `__pycache__/` to `.gitignore` if not already present

### Task 6: Update build/CI references

**Files:**
- Modify: `Dockerfile`
- Modify: `Makefile`
- Modify: `.goreleaser.yml`
- Verify: `.github/workflows/ci.yml`
- Verify: `.zed/tasks.json`

- [x] update `Dockerfile`: `COPY scripts/init-docker.sh` → `COPY scripts/internal/init-docker.sh`
- [x] update `Makefile`: `scripts/prep-toy-test.sh` → `scripts/internal/prep-toy-test.sh`, `scripts/prep-review-test.sh` → `scripts/internal/prep-review-test.sh`
- [x] update `.goreleaser.yml` hook: `scripts/update-plugin-version.sh` → `scripts/internal/update-plugin-version.sh`
- [x] verify `.github/workflows/ci.yml` still works (uses `scripts/ralphex-dk.sh` which is now symlink - should work as-is)
- [x] verify `.zed/tasks.json` still works (uses `scripts/ralphex-dk.sh` symlink)
- [x] run `go test ./...` to verify Go tests pass
- [x] run `golangci-lint run --max-issues-per-linter=0 --max-same-issues=0`

### Task 7: Update documentation references

**Files:**
- Modify: `CLAUDE.md`
- Modify: `README.md`
- Modify: `llms.txt`
- Modify: `docs/custom-providers.md`
- Modify: `docs/hg-support.md`
- Modify: `.claude-plugin/README.md`

- [x] update `CLAUDE.md` references: `scripts/codex-as-claude.sh` → `scripts/codex-as-claude/codex-as-claude.sh`, `scripts/hg2git.sh` → `scripts/hg2git/hg2git.sh`, `scripts/prep-toy-test.sh` → `scripts/internal/prep-toy-test.sh`, keep `scripts/ralphex-dk.sh` as-is (symlink)
- [x] update `README.md` references: same pattern, keep curl URL as `scripts/ralphex-dk.sh`
- [x] update `llms.txt` references: same pattern
- [x] update `docs/custom-providers.md`: `scripts/codex-as-claude.sh` → `scripts/codex-as-claude/codex-as-claude.sh`, `scripts/opencode-as-claude.sh` → `scripts/opencode/opencode-as-claude.sh`
- [x] update `docs/hg-support.md`: `scripts/hg2git.sh` → `scripts/hg2git/hg2git.sh`
- [x] update `.claude-plugin/README.md`: `scripts/update-plugin-version.sh` → `scripts/internal/update-plugin-version.sh`
- [x] do NOT update `docs/plans/completed/` files (historical records)

### Task 8: Verify and push

- [x] run `python3 scripts/ralphex-dk.sh --test` (all 165+ tests pass via symlink)
- [x] run `bash scripts/hg2git/hg2git_test.sh`
- [x] run `bash scripts/opencode/opencode-as-claude_test.sh`
- [x] run `bash scripts/opencode/opencode-review_test.sh`
- [x] run `go test ./...`
- [x] run `golangci-lint run --max-issues-per-linter=0 --max-same-issues=0`
- [x] push branch, test curl URL: `curl -sL https://raw.githubusercontent.com/umputun/ralphex/refactor/scripts-reorg/scripts/ralphex-dk.sh | head -5`
- [x] verify symlink works via GitHub raw

### Task 9: [Final] Update project documentation

- [x] update CLAUDE.md project structure section to reflect new layout
- [x] update README.md if scripts section needs structural changes
- [x] move this plan to `docs/plans/completed/`

## Post-Completion

**Manual verification:**
- verify curl install URL works on master after merge
- verify `--update-script` self-update still works (downloads from raw.githubusercontent.com)
