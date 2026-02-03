# Changelog

## v0.7.5 - 2026-02-03

### Fixed

- Docker: auto-disable codex sandbox in container (Landlock doesn't work in containers)
- Docker: run interactive mode in foreground for TTY support (fixes fzf/interactive input)
- Docker: mount global gitignore at configured path (fixes .DS_Store showing as untracked)

## v0.7.4 - 2026-02-03

### Fixed

- Docker image tags now use semver format (0.7.4, 0.7, latest) without v prefix
- Go image build now correctly references base image tag

## v0.7.3 - 2026-02-03

### Fixed

- Docker image tags now use semver format (0.7.3, 0.7, latest) without v prefix (broken release)

## v0.7.2 - 2026-02-03

### Changed

- Multiarch Docker builds with native ARM64 runners

## v0.7.1 - 2026-02-03

### Changed

- Split Docker into base and Go images with Python support

## v0.7.0 - 2026-02-02

### Added

- `--tasks-only` mode for running tasks without review phases (#58)
- Docker support for isolated execution (#54)
- Dashboard e2e tests with Playwright (#25) @melonamin

### Changed

- E2E tests now manual-only (workflow_dispatch)
- Bump github.com/go-git/go-billy/v5 from 5.6.2 to 5.7.0 (#55)

### Fixed

- Docker ghcr.io authentication in CI workflow

## v0.6.0 - 2026-01-29

### Added

- Plan draft preview with user feedback loop - interactive review before finalizing plans (#51)
- Error pattern detection for rate limits and API failures - graceful exit with help suggestions (#49)
- Commented defaults with auto-update support - user customizations preserved, defaults auto-updated (#48)
- `{{DEFAULT_BRANCH}}` template variable for prompts and agents (#46)
- Auto-create initial commit for empty repositories (#41)
- Claude Code plugin infrastructure with marketplace support (#40) @nniel-ape
- Glamour-based markdown rendering for plan draft preview
- Modern landing page with docs subdirectory

### Changed

- Refactored git package: introduced Service as single public API (#44)
- Refactored main.go into extracted packages (pkg/plan, pkg/web/dashboard) (#43)

### Fixed

- Resolve `{{PLAN_FILE}}` to completed/ path after plan is moved (#50)
- Handle context cancellation during interactive input prompts (#42) @chloyka
- Use injected logger instead of stderr in MovePlanToCompleted
- Site: prevent horizontal scrolling on mobile, fix CF build, SEO improvements
- Site: serve raw .md files in assets/claude/

## v0.5.0 - 2026-01-28

### Added

- `--reset` flag for interactive config restoration (#37)
- Plan validation step to make_plan.txt prompt

## v0.4.4 - 2026-01-27

### Fixed

- Plan creation loop issue: Claude now emits PLAN_READY signal instead of asking natural language questions

## v0.4.3 - 2026-01-26

### Fixed

- IsIgnored now loads global and system gitignore patterns (#35)

## v0.4.2 - 2026-01-26

### Fixed

- HasChangesOtherThan now ignores gitignored files (#34)
- Handle permission errors in plan directory detection

## v0.4.1 - 2026-01-26

### Added

- Auto-plan-mode detection: running `ralphex` without arguments on master/main prompts for plan creation if no plans exist (#33)

## v0.4.0 - 2026-01-26

### Added

- Interactive plan creation mode with `--plan` flag (#22)
- Web dashboard with real-time streaming and multi-session support (#17)
- Improved uncommitted changes handling (#24)
- Graceful prompt variable handling (#16)

### Fixed

- Windows build regression from web dashboard (#30)
- Scanner buffer increased from 16MB to 64MB for large outputs
- Better error message for repositories without commits
- Auto-disable codex when binary not installed (#23)
- Kill entire process group on context cancellation (#21)
- Web dashboard improvements and signal handling (#29)

## v0.3.0 - 2026-01-23

### Added

- Local project configuration support (`.ralphex/` directory) (#15)
- Symlinked config directory support (9e337d7)
- MkDocs documentation site with Cloudflare Pages deployment (f459c78)
- CHANGELOG.md with release history (33b4cc5)

### Changed

- Refactored config module into focused submodules (values, colors, prompts, agents, defaults) (#15)
- Adjusted terminal output colors for better readability (5d3d127)
- Refactored main package to use option structs for functions with 4+ parameters (256b090)

## v0.2.3 - 2026-01-22

### Fixed

- Cleanup minor code smells (unused variable, gitignore pattern) (88d9272)

### Added

- `llms.txt` for LLM agent consumption (117dcec)

## v0.2.2 - 2026-01-22

### Fixed

- Install prompts/agents into empty directories (314ad3b)

### Added

- Copy default prompts on first run (5cd13e6)
- Tests for `determineMode`, `checkClaudeDep`, `preparePlanFile`, `createRunner` (b403eb1)

## v0.2.1 - 2026-01-21

### Fixed

- Increase bufio.Scanner buffer to 16MB for large outputs (#12)
- Preserve untracked files during branch checkout (#11)
- Support git worktrees (#10)
- Add early dirty worktree check before branch operations (#9)

### Removed

- Docker support (#13)

## v0.2.0 - 2026-01-21

### Added

- Configurable colors (#7)
- Scalar config fallback to embedded defaults (#8)

## v0.1.0 - 2026-01-21

Initial release of ralphex - autonomous plan execution with Claude Code.

### Added

- Autonomous task execution with fresh context per task
- Multi-phase code review pipeline (5 agents → Codex → 2 agents)
- Custom review agents with `{{agent:name}}` template system
- Automatic git branch creation from plan filename
- Automatic commits after each task and review fix
- Plan completion tracking (moves to `completed/` folder)
- Streaming output with timestamps and colors
- Multiple execution modes: full, review-only, codex-only
- Zero configuration required - works out of the box
