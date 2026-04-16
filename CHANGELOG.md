# Changelog

## v0.27.2 - 2026-04-16

### Improved

- Add optional `--effort` level to `task_model` / `review_model` via `model[:effort]` syntax #285 @umputun

## v0.27.1 - 2026-04-16

### Improved

- Rename `claude_model`/`--claude-model` to `task_model`/`--task-model` for symmetry with `review_model`/`--review-model` #284 @umputun
- Pass task model to plan mode (was missing in #272)
- Clarify docs that model flags are passed to custom wrappers (may be ignored or implemented)

### Fixed

- `stripFlag` now handles `--flag=value` and bare `--flag` forms, prevents duplicate `--model` in `claude_args` #284 @umputun

## v0.27.0 - 2026-04-16

### New Features

- Add GitHub Copilot CLI support #275 @AOrlov
- Add per-phase Claude model configuration #272 @dwilberger

### Improved

- Skip post-codex claude review when no findings #282 @umputun
- Update all dependencies 2bc0008

### Fixed

- Reduce macOS credential cleanup race and fail fast on missing creds 33390d9

## v0.26.3 - 2026-04-08

### Improved

- Bump github.com/go-jose/go-jose/v3 from 3.0.4 to 3.0.5 #264 @app/dependabot
- Update all dependencies 3f4d7d44
- Document agent fallback behavior in CLAUDE.md and llms.txt 1c898c15
- Clarify agent customization and disable mechanism in README b4e03548

### Fixed

- Prevent indefinite hang on Windows when child processes hold pipes open #273 @stanurkov
- Add "Not logged in" to default Claude error patterns #269 @nnemirovsky
- Case-insensitive path handling for worktree plan commits on macOS #266 @umputun

## v0.26.2 - 2026-03-31

### Improved

- Update README.md to link plan creation in quick start #261 @ksenks
- Add jq to docker image afdfb47

## v0.26.1 - 2026-03-30

### Improved

- Unify gitignore handling and fix write error reporting #252 @vkazmirchuk
- Unify startup header and completion footer display 9af32cd
- Revert coveralls → codecov, switch back to coveralls 63d8a7f

### Fixed

- Use .ralphex/.gitignore instead of modifying root .gitignore #260 @rsolmano

## v0.26.0 - 2026-03-25

### New Features

- Add idle timeout for claude sessions #250 @umputun
- Task pause+resume via SIGQUIT #249 @umputun
- Add InitLocal API and --init CLI flag for local config setup #245 @vkazmirchuk

### Improved

- Bump github.com/fatih/color from 1.18.0 to 1.19.0 #243 @app/dependabot

### Fixed

- Prevent false positive pattern matching on claude analysis text #251 @umputun

## v0.25.0 - 2026-03-23

### New Features

- Add commit_trailer config option for attribution trailers on commits #242 @umputun
- Per-file fallback for agent loading #239 @umputun

### Fixed

- Use Optional[] syntax in ralphex-dk.sh for Python 3.9 compatibility #241 @alkk
- Fix Windows command-line length limits problem for codex execution #233 @stanurkov

## v0.24.4 - 2026-03-21

### Fixed

- Fix default colors for light terminal readability #237 @umputun

## v0.24.3 - 2026-03-18

### Improved

- Add RALPHEX_DOCKER_NETWORK env var and --network CLI flag #230 @umputun

### Fixed

- Prevent non-automatable checkboxes in plan Task sections #228 @umputun

## v0.24.2 - 2026-03-18

### Fixed

- Kill orphaned child processes on normal exit #227 @umputun

## v0.24.1 - 2026-03-17

### Fixed

- Fix wrapText continuation lines using reduced width instead of full terminal width

## v0.24.0 - 2026-03-17

### New Features

- Add session_timeout config option for hanging session safety net #225 @umputun

### Fixed

- Prevent infinite loop when checkboxes are outside Task sections #222 @romrigger
- Fix progress output text wrapping and code smells #226 @umputun

## v0.23.0 - 2026-03-16

### New Features

- Support host Docker socket mounting in container for Docker-dependent workflows #223 @umputun

### Improved

- Code smells cleanup: structure and convention improvements #217 @umputun
- Bump golang.org/x/term from 0.40.0 to 0.41.0 #219 @app/dependabot
- Bump github.com/charmbracelet/glamour from 0.10.0 to 1.0.0 #218 @app/dependabot

### Fixed

- Pass Claude prompt via stdin to avoid Windows cmd.exe length limit #220 @stanurkov

## v0.22.0 - 2026-03-15

### New Features

- Dry run flag for docker wrapper script #213 @bronislav
- Introduce gemini-as-claude wrapper #212 @korjavin
- Externalize codex review prompt as configurable template #216 @umputun
- Add codex_review.txt template file and config wiring
- Add {{PREVIOUS_REVIEW_CONTEXT}} variable and refactor buildCodexPrompt to use template

### Improved

- Reorganize scripts/ into functional subdirectories #210 @umputun
- Show progress log path at completion #204 @DmitriyAlergant
- Deduplicate local config detection when it matches global dir
- Document worktree mode limitations and review workaround

### Fixed

- Use --output instead of --format for aws export-credentials #203 @bronislav
- Pass progress file to external review prompts for iteration history

## v0.21.3 - 2026-03-11

### Fixed

- Use sentinel error to prevent infinite retry on I/O failures in plan draft review
- Output invalid selection warnings to stdout instead of log for consistent interactive flow
- Fix test name mismatch ("retry then accepts" → "retry then rejects")

## v0.21.2 - 2026-03-11

### Improved

- Harden workflows, upgrade actions, fix caching (#184) @paskal
- Revert golangci-lint pinning to latest

### Fixed

- Retry invalid selections in plan draft review (#202) @umputun

## v0.21.1 - 2026-03-11

### New Features

- Add opencode-as-claude wrapper and opencode-review scripts (#199) @mschedrin

### Improved

- Add python tests for docker wrapper script

### Fixed

- Prevent false positive pattern matching on clean codex/custom exit (#200) @umputun
- Prevent custom review output duplication in progress log (#198) @umputun
- Ensure test creates temp claude config dir for Linux CI

## v0.21.0 - 2026-03-10

### New Features

- Add AWS Bedrock support for Docker wrapper (#169) @bronislav

### Improved

- Refactor docker wrapper to use argparse for CLI parsing (#183) @bronislav
- Update default codex model to gpt-5.4
- Bump golang.org/x/sys from 0.41.0 to 0.42.0 (#190) @app/dependabot
- Bump docker/build-push-action from 6 to 7 (#189) @app/dependabot
- Bump docker/setup-buildx-action from 3 to 4 (#188) @app/dependabot
- Bump docker/login-action from 3 to 4 (#187) @app/dependabot
- Add FAQ entry for custom gate steps in task prompt
- Fix broken relative links in README for MkDocs site

### Fixed

- Commit pending changes after external review loop early exit (#186) @umputun
- Isolate integration tests from global config
- Exclude gosec G118 from lint config
- Use compact date format in goreleaser version string

## v0.20.0 - 2026-03-03

### New Features

- Add RALPHEX_EXTRA_ENV support for passing environment variables to Docker (#179) @bronislav
- Implement manual break command (SIGQUIT/Ctrl+\) for external review loop
- Add review patience: terminate external review after N consecutive unchanged rounds (stalemate detection)

### Improved

- Fix slow tests and use t.Context() instead of context.Background()
- Update playwright-go to v0.5700.1
- Update go dependencies and github actions
- Use env: mapping for head_branch in docker workflow (#174) @paskal

### Fixed

- Isolate TestTasksOnlyModeBranchCreation from global config (#177) @bronislav
- Show dirty file list in uncommitted changes error (#175) @umputun

## v0.19.0 - 2026-02-28

### New Features

- Add wait-on-limit mode: automatic retry on rate limits with configurable duration (#168) @umputun

### Improved

- Add FAQ entry about using ralphex with Claude Pro plan
- Document wait-on-limit feature

### Fixed

- Fix worktree mode rejecting non-main/master default branches (#165) @umputun

## v0.18.0 - 2026-02-26

### New Features

- Add configurable VCS command with Mercurial support (#162) @paskal
- Add configurable iteration limit for external review phase (#160) @umputun
- Add max_iterations as config file option

### Fixed

- Fix broken "Available images" anchor link in README

## v0.17.0 - 2026-02-24

### New Features

- Add git worktree isolation for parallel plan execution (#158) @umputun

### Improved

- Bump goreleaser/goreleaser-action from 6 to 7 (#148) @app/dependabot
- Add codecov config to ignore mock directories
- Replace coveralls with codecov for coverage reporting
- Add worktree isolation documentation
- Add coverage for worktree and ensureGitIgnored paths

### Fixed

- Make web dashboard reachable from host in Docker (#152) @nnemirovsky
- Handle nested claude session error instead of silent loop
- Auto-detect host timezone in Docker wrapper

## v0.16.0 - 2026-02-22

### New Features

- Add extra volume mount support to Docker wrapper (#142) @alkk

### Fixed

- Fix dashboard task numbering on mid-run plan edits (#147) @umputun
- Adapt review prompts for codex multi-agent flow (#143) @krajcik

## v0.15.3 - 2026-02-20

### Fixed

- Stop embedding diff in parallel agent prompts to reduce launch latency (#141)
- Extract separator constant, assert test cleanup errors

## v0.15.2 - 2026-02-18

### Improved

- Add codex wrapper script for alternative provider support (#133) @umputun
- Add custom providers documentation

### Fixed

- Detect completed progress files and start fresh on reuse (#134) @umputun
- Ensure fallback result event fires when codex exits non-zero

## v0.15.1 - 2026-02-18

### Fixed

- Append progress files on restart instead of truncating (#130) @umputun
- Use rune-based truncation for external review summary to avoid corrupting multi-byte UTF-8 characters

## v0.15.0 - 2026-02-17

### Added

- Shell completions for bash, zsh, and fish (#120) @paskal

### Improved

- Replace bufio.Scanner with unbounded line reader for stream parsing (#124)
- Use verbose completions for zsh and fish descriptions (#122) @paskal
- Exclude false-positive gosec rules for golangci-lint v2.10.1

### Fixed

- Use plans_dir from config in make_plan prompt (#126) @animedetector
- Add PLANS_DIR to doc comments, remove duplicate plan file

## v0.14.0 - 2026-02-17

### Added

- `--base-ref` and `--skip-finalize` CLI flags (#117)
- `default_branch` config option for specifying review diff base (#115)

### Fixed

- Use os.Getwd() in toRelPath for correct relative path in startup banner

## v0.13.0 - 2026-02-16

### Added

- Interactive plan review to `--plan` mode (#114)

### Improved

- Fix llms.txt link for docs site, add finalize step section to README

### Fixed

- Simplify ralphex-update skill to skip do-nothing config files
- Fix Windows comment to reference Setsid instead of Setpgid (#112)

## v0.12.1 - 2026-02-15

### Fixed

- Add SELinux support for Docker volume mounts (#111)
- Use Setsid instead of Setpgid to prevent terminal signal hang (#110)

## v0.12.0 - 2026-02-14

### Added

- `--dump-defaults` flag and `/ralphex-update` skill (#109)
- Move progress files to `.ralphex/progress/` directory (#107)

### Improved

- Upgrade to go 1.26 and update dependencies

### Fixed

- Prevent progress file leakage from tests

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
