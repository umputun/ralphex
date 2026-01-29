# Refactor main.go Implementation Plan

## Overview

Extract business logic from `cmd/ralphex/main.go` (1100 lines, 37 functions) into appropriate packages, leaving main.go as pure wiring/orchestration (~400 lines, 15 functions).

## Context

- Files involved: `cmd/ralphex/main.go`, `pkg/git/git.go`, `pkg/web/` (new file), `pkg/plan/` (new package)
- Related patterns: constructor + methods, config structs for multi-param functions
- Dependencies: existing `pkg/git`, `pkg/web`, `pkg/processor`, `pkg/progress`, `pkg/input`

## Tasks

### 1. Create pkg/plan package

**Files:**
- Create: `pkg/plan/plan.go`
- Create: `pkg/plan/plan_test.go`

- [x] Create `Selector` struct with `PlansDir` and `Colors` fields
- [x] Implement `NewSelector(plansDir string, colors *progress.Colors) *Selector`
- [x] Move `selectPlanWithFzf` → `(*Selector).selectWithFzf` (private)
- [x] Move `selectPlan` → `(*Selector).Select(ctx, planFile string, optional bool) (string, error)`
- [x] Move `preparePlanFile` logic into `Select` (normalize to absolute path)
- [x] Move `findRecentPlan` → `(*Selector).FindRecent(startTime time.Time) string`
- [x] Move `extractBranchName` → `ExtractBranchName(planFile string) string` (package function)
- [x] Move `promptPlanDescription` → `PromptDescription(ctx context.Context, r io.Reader, colors *progress.Colors) string`
- [x] Export `ErrNoPlansFound` error
- [x] Move `datePrefixRe` regex to plan package
- [x] Write tests for all public functions
- [x] Verify tests pass

### 2. Extend pkg/git with workflow methods

**Files:**
- Modify: `pkg/git/git.go`
- Modify: `pkg/git/git_test.go`

- [x] Add `(*Repo).IsMainBranch() (bool, error)` method
- [x] Add `(*Repo).EnsureIgnored(pattern, probePath string, log func(string, ...any)) error` method
      (uses probePath for IsIgnored check, adds pattern to .gitignore)
- [x] Write tests for new methods
- [x] Verify tests pass

Note: `HasCommits()` already exists - prompting logic stays in main.go

### 3. Create pkg/web/dashboard.go

**Files:**
- Create: `pkg/web/dashboard.go`
- Create: `pkg/web/dashboard_test.go`

- [x] Create `DashboardConfig` struct
- [x] Create `Dashboard` struct with private fields
- [x] Implement `NewDashboard(cfg DashboardConfig) *Dashboard`
- [x] Move `startWebDashboard` → `(*Dashboard).Start(ctx, baseLog) (processor.Logger, error)`
- [x] Move `runWatchOnly` → `(*Dashboard).RunWatchOnly(ctx) error`
- [x] Move `setupWatchMode` → private helper `setupWatchMode`
- [x] Move `startServerAsync` → private helper `startServerAsync`
- [x] Move `monitorWatchMode` → private helper `monitorErrors`
- [x] Move `printWatchModeInfo` → private helper `printWatchInfo`
- [x] Move `serverStartupTimeout` constant
- [x] Write tests for public methods
- [x] Verify tests pass

### 4. Refactor main.go

**Files:**
- Modify: `cmd/ralphex/main.go`
- Modify: `cmd/ralphex/main_test.go`

- [x] Update imports to include `pkg/plan`
- [x] Replace `planSelector` struct with `plan.Selector` usage
- [x] Replace `preparePlanFile` call with `selector.Select()`
- [x] Replace `selectPlanWithFzf` calls with selector methods
- [x] Replace `extractBranchName` calls with `plan.ExtractBranchName()`
- [x] Replace `promptPlanDescription` calls with `plan.PromptDescription()`
- [x] Replace `findRecentPlan` calls with `selector.FindRecent()`
- [x] Replace `errNoPlansFound` with `plan.ErrNoPlansFound`
- [x] Keep `ensureRepoHasCommits` in main.go (uses existing `gitOps.HasCommits()`, prompting stays local)
- [x] Replace `ensureGitignore` calls with `gitOps.EnsureIgnored("progress*.txt", "progress-test.txt", ...)`
- [x] Add `isMainBranch` check using `gitOps.IsMainBranch()`
- [x] Replace `webDashboardParams` with `web.DashboardConfig`
- [x] Replace `startWebDashboard` calls with `dashboard.Start()`
- [x] Replace `runWatchOnly` calls with `dashboard.RunWatchOnly()`
- [x] Inline `continuePlanExecution` into `runPlanMode`
- [x] Inline `setupGitForExecution` into `executePlan`
- [x] Inline `handlePostExecution` into `executePlan`
- [x] Inline `getCurrentBranch` where used (use direct call with fallback)
- [x] Combine `checkDependencies` and `checkClaudeDep` into single function
- [x] Combine `printStartupInfo` and `printPlanModeInfo` into one function
- [x] Remove dead code (moved functions, unused structs)
- [x] Update/fix tests in main_test.go
- [x] Verify tests pass
- [x] Run linter

### 5. Final validation

- [x] Run `make test` - all tests pass
- [x] Run `make lint` - no linter errors
- [x] Move plan to `docs/plans/completed/`
