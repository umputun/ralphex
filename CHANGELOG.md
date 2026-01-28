# Changelog

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
