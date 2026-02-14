# Changelog

## v0.11.1 - 2026-02-13

### Improved

- Add "Other" option for custom answers in plan creation questions (#103)

### Fixed

- Strip leading meta-comment blocks from loaded prompts (#104)
- Add YYYY-MM-DD date prefix to plan filenames in ralphex-plan skill

## v0.11.0 - 2026-02-13

### Added

- `--config-dir` flag and `RALPHEX_CONFIG_DIR` env var for custom config directory (#100)

### Fixed

- Replace background+polling with foreground parallel agents in review prompts (#99)
- Strengthen review signal handling in prompts (#95)
- Escape agent reference example in prompt comments

## v0.10.5 - 2026-02-12

### Changed

- Document review-only mode as standalone workflow

### Fixed

- Preserve markdown headers in prompt and agent files (#90)

## v0.10.4 - 2026-02-11

### Changed

- Add 5-second force exit after SIGINT and suppress ^C echo (#87)
- Add FAQ entry about adjusting course during execution

### Fixed

- Restore terminal state before force exit on interrupt timeout

## v0.10.3 - 2026-02-11

### Fixed

- Include stderr content in codex error messages for better diagnostics (#86)

## v0.10.2 - 2026-02-11

### Fixed

- Fix Ctrl+C (SIGINT) handling for immediate response (#85)

## v0.10.1 - 2026-02-10

### Changed

- Clarify Docker image usage for non-Go languages in README

### Fixed

- Resolve version as unknown when installed via `go install` (#84)
- Map RALPHEX_PORT to host side only, keep container port 8080
- Resolve mypy strict type errors in docker wrapper script

## v0.10.0 - 2026-02-10

### Added

- Model customization — per-phase config (`claude_model_task`, `claude_model_review`, `claude_model_plan`) and per-agent frontmatter options (`model`, `agent` type) in agent files (#75, #80) @ZhilinS
- External git backend — use native git CLI instead of go-git via `git_backend = external` config (#79)
- `CLAUDE_CONFIG_DIR` env var for alternate Claude config directories (#81)

### Changed

- Rewrite Docker wrapper script from bash to Python3 with embedded tests (#81)
- Refactor PhaseHolder as single source of truth for execution phase (#75) @ZhilinS
- Move IsMainBranch from backend interface to Service level
- Use precise elapsed time formatting instead of coarse humanize.RelTime, drop go-humanize dependency
- Update all dependencies to latest versions

### Fixed

- Web dashboard: diff stats display, session replay timing, watcher improvements, active task highlighting (#76) @melonamin
- Docker: mount main .git dir for worktree support
- Docker wrapper: preserve symlinked PWD, add urlopen timeout, remove dead code branch

## v0.9.0 - 2026-02-06

### Added

- Notification system - alerts on completion/failure via Telegram, Email, Slack, Webhook, or custom script (#71)
- Docker wrapper self-update via `--update-script` flag

### Fixed

- Exit review loop when no changes detected (#70)
- Docker: only bind port when `--serve`/`-s` is requested to avoid conflicts with concurrent instances

### Changed

- Code review findings and package structure improvements (#68)

## v0.8.0 - 2026-02-05

### Added

- Custom external review support - use your own AI tool instead of codex (#67)
- Finalize step for optional post-completion actions (#63)
- Diff stats in completion message - shows files and lines changed (#66)
- Cursor CLI documented as community-tested alternative

### Changed

- Default codex model updated to gpt-5.3-codex
- `--external-only` (`-e`) flag replaces `--codex-only` (`-c` kept as deprecated alias)

### Fixed

- Strengthen codex eval prompt to prevent premature signal
- Classify custom review sections as external phase in dashboard
- Make config mount writable for default generation
- Add API Error pattern to default error detection

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
