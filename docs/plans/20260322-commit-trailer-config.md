# Add commit_trailer Config Option

## Overview

Add a `commit_trailer` config option that appends a custom trailer line to all ralphex-orchestrated git commits. This covers both direct Go-code commits (plan add/move, review findings) and LLM-prompted commits (task, review phases). Default: empty (disabled).

Example config:
```
commit_trailer = Co-authored-by: ralphex <noreply@ralphex.com>
```

Related to #240.

## Context

- Config system: `pkg/config/values.go` (Values struct, INI parsing, merge), `pkg/config/config.go` (Config struct)
- Git backend: `pkg/git/external.go` (`commit()` and `commitFiles()` methods on `externalBackend`)
- Git service: `pkg/git/service.go` (`NewService`, `CommitPlanFile`, `MovePlanToCompleted`)
- Template variables: `pkg/processor/prompts.go` (`replaceBaseVariables()`)
- Prompt files with commit instructions: `task.txt`, `review_first.txt`, `review_second.txt`, `codex.txt`, `custom_eval.txt`, `finalize.txt`
- Embedded defaults: `pkg/config/defaults/config`
- Callers of `git.NewService`: `cmd/ralphex/main.go` (3 call sites: `openGitService`, worktree service in `runWithWorktree`, and tests)

## Solution Overview

1. Add `CommitTrailer` string field to Values and Config
2. Parse from INI, merge like other string fields
3. Add `SetCommitTrailer(string)` method on `git.Service` — stores trailer on Service, not on backend
4. Service methods that call `repo.commit()`/`repo.commitFiles()` append trailer to message before passing it down (no backend interface change)
5. Add `{{COMMIT_TRAILER}}` template variable expanding to LLM instruction text, accessed via `r.cfg.AppConfig.CommitTrailer`
6. Add trailer instruction block to prompt files that contain commit commands

## Development Approach

- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- Every task includes tests
- All tests must pass before starting next task

## Implementation Steps

### Task 1: Add config option

**Files:**
- Modify: `pkg/config/values.go`
- Modify: `pkg/config/values_test.go`
- Modify: `pkg/config/config.go`
- Modify: `pkg/config/defaults/config`

- [x] add `CommitTrailer string` field to `Values` struct
- [x] add INI parsing for `commit_trailer` key in `parseValuesFromBytes()`
- [x] add merge logic in `mergeFrom()` (same pattern as `VcsCommand` — non-empty overrides)
- [x] add `CommitTrailer string` field to `Config` struct with json tag
- [x] map `values.CommitTrailer` to `Config.CommitTrailer` in the builder
- [x] add commented `# commit_trailer =` to embedded defaults config file
- [x] write tests: parse commit_trailer from INI, merge behavior, empty default
- [x] run `make test` — must pass before next task

### Task 2: Inject trailer in git Service layer

**Files:**
- Modify: `pkg/git/service.go`
- Modify: `pkg/git/service_test.go`
- Modify: `cmd/ralphex/main.go`

- [ ] add `trailer string` field to `Service` struct
- [ ] add `SetCommitTrailer(string)` method on `Service` that sets the field
- [ ] add helper `Service.appendTrailer(msg string) string` that appends "\n\n<trailer>" when set, returns msg unchanged when empty
- [ ] modify Service methods that call `repo.commit()` to use `appendTrailer()`: `CreateBranchForPlan`, `CommitPlanFile`, `MovePlanToCompleted`
- [ ] modify Service methods that call `repo.commitFiles()` to use `appendTrailer()` (if any)
- [ ] call `gitSvc.SetCommitTrailer(cfg.CommitTrailer)` in `cmd/ralphex/main.go` at all 3 call sites: `openGitService`, worktree service in `runWithWorktree`, and any other NewService calls
- [ ] write tests: commit with trailer appended (verify trailer in git log output), commit without trailer (empty config)
- [ ] write tests: SetCommitTrailer stores value, appendTrailer helper logic
- [ ] run `make test` — must pass before next task

### Task 3: Add template variable for LLM prompts

**Files:**
- Modify: `pkg/processor/prompts.go`
- Modify: `pkg/processor/prompts_test.go`
- Modify: `pkg/config/defaults/prompts/task.txt`
- Modify: `pkg/config/defaults/prompts/review_first.txt`
- Modify: `pkg/config/defaults/prompts/review_second.txt`
- Modify: `pkg/config/defaults/prompts/codex.txt`
- Modify: `pkg/config/defaults/prompts/custom_eval.txt`
- Modify: `pkg/config/defaults/prompts/finalize.txt`

- [ ] add `{{COMMIT_TRAILER}}` expansion in `replaceBaseVariables()` using `r.cfg.AppConfig.CommitTrailer` — when trailer is set, expand to instruction text like "When making git commits, add the following trailer after a blank line at the end of the commit message:\n<trailer>"; when empty, expand to empty string
- [ ] add `{{COMMIT_TRAILER}}` placeholder to each prompt file that contains commit instructions (after the commit instruction line)
- [ ] write tests: template variable expansion with trailer set, with trailer empty
- [ ] run `make test` — must pass before next task

### Task 4: Verify and document

- [ ] verify all requirements: config option parsed, trailer appended to Go commits, trailer instruction in LLM prompts
- [ ] run full test suite: `make test`
- [ ] run linter: `make lint`
- [ ] run formatters: `make fmt`
- [ ] update README.md customization section (add `commit_trailer` to config options)
- [ ] update CLAUDE.md configuration section
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

- Comment on #240 with the implementation details
- Consider mentioning in next release notes
