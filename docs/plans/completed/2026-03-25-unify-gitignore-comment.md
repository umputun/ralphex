# Unify ralphex comment in .gitignore

## Overview

Replace the duplicated "# ralphex progress logs" comment with a single "# ralphex" that is added to .gitignore only once. Currently EnsureIgnored prepends "# ralphex progress logs" before each pattern, which leads to duplicate comments and an inaccurate description (for worktrees it's not progress logs).

## Context

- Files involved: `pkg/git/service.go`, `pkg/git/service_test.go`
- Related patterns: EnsureIgnored is called from multiple places in `cmd/ralphex/main.go` for different patterns (.ralphex/progress/, .ralphex/worktrees/)
- Current behavior: each EnsureIgnored call appends `\n# ralphex progress logs\n<pattern>\n`
- Desired behavior: the "# ralphex" comment is added once before the first pattern; subsequent patterns are appended without repeating the comment

## Development Approach

- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Implementation Steps

### Task 1: Change comment and add deduplication in EnsureIgnored

**Files:**
- Modify: `pkg/git/service.go`
- Modify: `pkg/git/service_test.go`

- [x] In `EnsureIgnored` (service.go:495), change the comment from "# ralphex progress logs" to "# ralphex"
- [x] Before writing the comment, read existing .gitignore content and check if "# ralphex" comment already exists
- [x] If the comment already exists, append only the pattern (without comment); if not, append both comment and pattern
- [x] Update existing tests in service_test.go to verify: first call adds comment + pattern, second call for a different pattern adds only the pattern (no duplicate comment)
- [x] Add a test case that calls EnsureIgnored twice with different patterns and verifies the comment appears exactly once
- [x] Run project test suite - must pass before task 2

### Task 2: Verify acceptance criteria

- [x] Run full test suite (`make test`)
- [x] Run linter (`make lint`)
- [x] Verify test coverage meets 80%+
