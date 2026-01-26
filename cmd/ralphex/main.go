// Package main provides ralphex - autonomous plan execution with Claude Code.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/jessevdk/go-flags"

	"github.com/umputun/ralphex/pkg/config"
	"github.com/umputun/ralphex/pkg/git"
	"github.com/umputun/ralphex/pkg/input"
	"github.com/umputun/ralphex/pkg/processor"
	"github.com/umputun/ralphex/pkg/progress"
	"github.com/umputun/ralphex/pkg/web"
)

// opts holds all command-line options.
type opts struct {
	MaxIterations   int      `short:"m" long:"max-iterations" default:"50" description:"maximum task iterations"`
	Review          bool     `short:"r" long:"review" description:"skip task execution, run full review pipeline"`
	CodexOnly       bool     `short:"c" long:"codex-only" description:"skip tasks and first review, run only codex loop"`
	PlanDescription string   `long:"plan" description:"create plan interactively (enter plan description)"`
	Debug           bool     `short:"d" long:"debug" description:"enable debug logging"`
	NoColor         bool     `long:"no-color" description:"disable color output"`
	Version         bool     `short:"v" long:"version" description:"print version and exit"`
	Serve           bool     `short:"s" long:"serve" description:"start web dashboard for real-time streaming"`
	Port            int      `short:"p" long:"port" default:"8080" description:"web dashboard port"`
	Watch           []string `short:"w" long:"watch" description:"directories to watch for progress files (repeatable)"`

	PlanFile string `positional-arg-name:"plan-file" description:"path to plan file (optional, uses fzf if omitted)"`
}

var revision = "unknown"

// datePrefixRe matches date-like prefixes in plan filenames (e.g., "2024-01-15-").
var datePrefixRe = regexp.MustCompile(`^[\d-]+`)

// errNoPlansFound is returned when no plan files exist in the plans directory.
var errNoPlansFound = errors.New("no plans found")

// startupInfo holds parameters for printing startup information.
type startupInfo struct {
	PlanFile      string
	Branch        string
	Mode          processor.Mode
	MaxIterations int
	ProgressPath  string
}

// planSelector holds parameters for plan file selection.
type planSelector struct {
	PlanFile string
	Optional bool
	PlansDir string
	Colors   *progress.Colors
}

// executePlanRequest holds parameters for plan execution.
type executePlanRequest struct {
	PlanFile string
	Mode     processor.Mode
	GitOps   *git.Repo
	Config   *config.Config
	Colors   *progress.Colors
}

// webDashboardParams holds parameters for web dashboard setup.
type webDashboardParams struct {
	BaseLog         processor.Logger
	Port            int
	PlanFile        string
	Branch          string
	WatchDirs       []string // CLI watch dirs
	ConfigWatchDirs []string // config watch dirs
	Colors          *progress.Colors
}

func main() {
	fmt.Printf("ralphex %s\n", revision)

	var o opts
	parser := flags.NewParser(&o, flags.Default)
	parser.Usage = "[OPTIONS] [plan-file]"

	args, err := parser.Parse()
	if err != nil {
		var flagsErr *flags.Error
		if errors.As(err, &flagsErr) && flagsErr.Type == flags.ErrHelp {
			os.Exit(0)
		}
		os.Exit(1)
	}

	if o.Version {
		os.Exit(0)
	}

	// handle positional argument
	if len(args) > 0 {
		o.PlanFile = args[0]
	}

	// setup context with signal handling
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx, o); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, o opts) error {
	// validate conflicting flags
	if err := validateFlags(o); err != nil {
		return err
	}

	// load config first to get custom command paths
	cfg, err := config.Load("") // empty string uses default location
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// create colors from config (all colors guaranteed populated via fallback)
	colors := progress.NewColors(cfg.Colors)

	// watch-only mode: --serve with watch dirs (CLI or config) and no plan file
	// runs web dashboard without plan execution, can run from any directory
	if isWatchOnlyMode(o, cfg.WatchDirs) {
		return runWatchOnly(ctx, o, cfg, colors)
	}

	// check dependencies using configured command (or default "claude")
	if depErr := checkClaudeDep(cfg); depErr != nil {
		return depErr
	}

	// require running from repo root
	if _, statErr := os.Stat(".git"); statErr != nil {
		return errors.New("must run from repository root (no .git directory found)")
	}

	// open git repository
	gitOps, err := git.Open(".")
	if err != nil {
		return fmt.Errorf("open git repo: %w", err)
	}

	// validate repository has commits (early check with helpful error)
	if validateErr := validateRepoHasCommits(gitOps); validateErr != nil {
		return validateErr
	}

	mode := determineMode(o)

	// plan mode has different flow - doesn't require plan file selection
	if mode == processor.ModePlan {
		return runPlanMode(ctx, o, executePlanRequest{
			Mode:   processor.ModePlan,
			GitOps: gitOps,
			Config: cfg,
			Colors: colors,
		})
	}

	// select and prepare plan file (not needed for plan mode)
	planFile, err := preparePlanFile(ctx, planSelector{
		PlanFile: o.PlanFile,
		Optional: o.Review || o.CodexOnly,
		PlansDir: cfg.PlansDir,
		Colors:   colors,
	})
	if err != nil {
		// check for auto-plan-mode: no plans found on main/master branch
		handled, autoPlanErr := tryAutoPlanMode(ctx, err, o, gitOps, cfg, colors)
		if handled {
			return autoPlanErr
		}
		return err
	}

	if setupErr := setupGitForExecution(gitOps, planFile, mode, colors); setupErr != nil {
		return setupErr
	}

	return executePlan(ctx, o, executePlanRequest{
		PlanFile: planFile,
		Mode:     mode,
		GitOps:   gitOps,
		Config:   cfg,
		Colors:   colors,
	})
}

// getCurrentBranch returns the current git branch name or "unknown" if unavailable.
func getCurrentBranch(gitOps *git.Repo) string {
	branch, err := gitOps.CurrentBranch()
	if err != nil || branch == "" {
		return "unknown"
	}
	return branch
}

// isMainBranch returns true if the branch name is "main" or "master".
func isMainBranch(branch string) bool {
	return branch == "main" || branch == "master"
}

// promptPlanDescription prompts the user for a plan description when no plans are found.
// returns the trimmed description, or empty string if user cancels (Ctrl+D/EOF or empty input).
func promptPlanDescription(r io.Reader, colors *progress.Colors) string {
	colors.Info().Printf("no plans found. what would you like to implement?\n")
	colors.Info().Printf("(enter description or press Ctrl+D to cancel): ")

	reader := bufio.NewReader(r)
	line, err := reader.ReadString('\n')
	if err != nil {
		// EOF (Ctrl+D) is graceful cancel
		return ""
	}

	return strings.TrimSpace(line)
}

// tryAutoPlanMode attempts to switch to plan mode when no plans are found on main/master.
// returns (true, nil) if user canceled, (true, err) if plan mode was attempted, or (false, nil) if auto-plan-mode doesn't apply.
func tryAutoPlanMode(ctx context.Context, err error, o opts, gitOps *git.Repo, cfg *config.Config, colors *progress.Colors) (bool, error) {
	if !errors.Is(err, errNoPlansFound) || o.Review || o.CodexOnly {
		return false, nil
	}

	branch, branchErr := gitOps.CurrentBranch()
	if branchErr != nil || !isMainBranch(branch) {
		return false, nil //nolint:nilerr // branchErr is intentionally ignored - if we can't get branch, skip auto-plan-mode
	}

	description := promptPlanDescription(os.Stdin, colors)
	if description == "" {
		return true, nil // user canceled
	}

	o.PlanDescription = description
	return true, runPlanMode(ctx, o, executePlanRequest{
		Mode:   processor.ModePlan,
		GitOps: gitOps,
		Config: cfg,
		Colors: colors,
	})
}

// setupRunnerLogger creates the appropriate logger for the runner.
// if --serve is enabled, wraps the base logger with a broadcast logger.
func setupRunnerLogger(ctx context.Context, o opts, params webDashboardParams) (processor.Logger, error) {
	if !o.Serve {
		return params.BaseLog, nil
	}
	return startWebDashboard(ctx, params)
}

// handlePostExecution handles tasks after runner completion.
func handlePostExecution(gitOps *git.Repo, planFile string, mode processor.Mode, colors *progress.Colors) {
	// move completed plan to completed/ directory
	if planFile != "" && mode == processor.ModeFull {
		if moveErr := movePlanToCompleted(gitOps, planFile, colors); moveErr != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to move plan to completed: %v\n", moveErr)
		}
	}
}

// executePlan runs the main execution loop for a plan file.
// handles progress logging, web dashboard, runner execution, and post-execution tasks.
func executePlan(ctx context.Context, o opts, req executePlanRequest) error {
	branch := getCurrentBranch(req.GitOps)

	// create progress logger
	baseLog, err := progress.NewLogger(progress.Config{
		PlanFile: req.PlanFile,
		Mode:     string(req.Mode),
		Branch:   branch,
		NoColor:  o.NoColor,
	}, req.Colors)
	if err != nil {
		return fmt.Errorf("create progress logger: %w", err)
	}
	baseLogClosed := false
	defer func() {
		if baseLogClosed {
			return
		}
		if closeErr := baseLog.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to close progress log: %v\n", closeErr)
		}
	}()

	// wrap logger with broadcast logger if --serve is enabled
	runnerLog, err := setupRunnerLogger(ctx, o, webDashboardParams{
		BaseLog:         baseLog,
		Port:            o.Port,
		PlanFile:        req.PlanFile,
		Branch:          branch,
		WatchDirs:       o.Watch,
		ConfigWatchDirs: req.Config.WatchDirs,
		Colors:          req.Colors,
	})
	if err != nil {
		return err
	}

	// print startup info
	printStartupInfo(startupInfo{
		PlanFile:      req.PlanFile,
		Branch:        branch,
		Mode:          req.Mode,
		MaxIterations: o.MaxIterations,
		ProgressPath:  baseLog.Path(),
	}, req.Colors)

	// create and run the runner
	r := createRunner(req.Config, o, req.PlanFile, req.Mode, runnerLog)
	if runErr := r.Run(ctx); runErr != nil {
		return fmt.Errorf("runner: %w", runErr)
	}

	// handle post-execution tasks
	handlePostExecution(req.GitOps, req.PlanFile, req.Mode, req.Colors)

	elapsed := baseLog.Elapsed()
	req.Colors.Info().Printf("\ncompleted in %s\n", elapsed)

	// keep web dashboard running after execution completes
	if o.Serve {
		if err := baseLog.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to close progress log: %v\n", err)
		}
		baseLogClosed = true
		req.Colors.Info().Printf("web dashboard still running at http://localhost:%d (press Ctrl+C to exit)\n", o.Port)
		<-ctx.Done()
	}

	return nil
}

// setupGitForExecution prepares git state for execution (branch, gitignore).
func setupGitForExecution(gitOps *git.Repo, planFile string, mode processor.Mode, colors *progress.Colors) error {
	if planFile == "" {
		return nil
	}
	if mode == processor.ModeFull {
		if err := createBranchIfNeeded(gitOps, planFile, colors); err != nil {
			return err
		}
	}
	return ensureGitignore(gitOps, colors)
}

// checkClaudeDep checks that the claude command is available in PATH.
func checkClaudeDep(cfg *config.Config) error {
	claudeCmd := cfg.ClaudeCommand
	if claudeCmd == "" {
		claudeCmd = "claude"
	}
	return checkDependencies(claudeCmd)
}

// isWatchOnlyMode returns true if running in watch-only mode.
// watch-only mode runs the web dashboard without executing any plan.
//
// enabled when all conditions are met:
//   - --serve flag is set
//   - no plan file provided (neither positional arg nor --plan)
//   - watch directories exist (via --watch flag or config file)
//
// use cases:
//   - monitoring multiple concurrent ralphex executions from a central dashboard
//   - viewing progress of ralphex sessions running in other terminals
//
// example: ralphex --serve --watch ~/projects --watch /tmp
func isWatchOnlyMode(o opts, configWatchDirs []string) bool {
	return o.Serve && o.PlanFile == "" && o.PlanDescription == "" && (len(o.Watch) > 0 || len(configWatchDirs) > 0)
}

// determineMode returns the execution mode based on CLI flags.
func determineMode(o opts) processor.Mode {
	switch {
	case o.PlanDescription != "":
		return processor.ModePlan
	case o.CodexOnly:
		return processor.ModeCodexOnly
	case o.Review:
		return processor.ModeReview
	default:
		return processor.ModeFull
	}
}

// validateFlags checks for conflicting CLI flags.
func validateFlags(o opts) error {
	if o.PlanDescription != "" && o.PlanFile != "" {
		return errors.New("--plan flag conflicts with plan file argument; use one or the other")
	}
	return nil
}

// createRunner creates a processor.Runner with the given configuration.
func createRunner(cfg *config.Config, o opts, planFile string, mode processor.Mode, log processor.Logger) *processor.Runner {
	// --codex-only mode forces codex enabled regardless of config
	codexEnabled := cfg.CodexEnabled
	if mode == processor.ModeCodexOnly {
		codexEnabled = true
	}
	return processor.New(processor.Config{
		PlanFile:         planFile,
		ProgressPath:     log.Path(),
		Mode:             mode,
		MaxIterations:    o.MaxIterations,
		Debug:            o.Debug,
		NoColor:          o.NoColor,
		IterationDelayMs: cfg.IterationDelayMs,
		TaskRetryCount:   cfg.TaskRetryCount,
		CodexEnabled:     codexEnabled,
		AppConfig:        cfg,
	}, log)
}

func preparePlanFile(ctx context.Context, sel planSelector) (string, error) {
	selected, err := selectPlan(ctx, sel)
	if err != nil {
		return "", err
	}
	if selected == "" {
		if !sel.Optional {
			return "", errors.New("plan file required for task execution")
		}
		return "", nil
	}
	// normalize to absolute path
	abs, err := filepath.Abs(selected)
	if err != nil {
		return "", fmt.Errorf("resolve plan path: %w", err)
	}
	return abs, nil
}

func selectPlan(ctx context.Context, sel planSelector) (string, error) {
	if sel.PlanFile != "" {
		if _, err := os.Stat(sel.PlanFile); err != nil {
			return "", fmt.Errorf("plan file not found: %s", sel.PlanFile)
		}
		return sel.PlanFile, nil
	}

	// for review-only modes, plan is optional
	if sel.Optional {
		return "", nil
	}

	// use fzf to select plan
	return selectPlanWithFzf(ctx, sel.PlansDir, sel.Colors)
}

func selectPlanWithFzf(ctx context.Context, plansDir string, colors *progress.Colors) (string, error) {
	if _, err := os.Stat(plansDir); err != nil {
		return "", fmt.Errorf("%w: %s (directory missing)", errNoPlansFound, plansDir)
	}

	// find plan files (excluding completed/)
	plans, err := filepath.Glob(filepath.Join(plansDir, "*.md"))
	if err != nil || len(plans) == 0 {
		return "", fmt.Errorf("%w: %s", errNoPlansFound, plansDir)
	}

	// auto-select if single plan (no fzf needed)
	if len(plans) == 1 {
		colors.Info().Printf("auto-selected: %s\n", plans[0])
		return plans[0], nil
	}

	// multiple plans require fzf
	if _, lookupErr := exec.LookPath("fzf"); lookupErr != nil {
		return "", errors.New("fzf not found, please provide plan file as argument")
	}

	// use fzf for selection
	cmd := exec.CommandContext(ctx, "fzf",
		"--prompt=select plan: ",
		"--preview=head -50 {}",
		"--preview-window=right:60%",
	)
	cmd.Stdin = strings.NewReader(strings.Join(plans, "\n"))
	cmd.Stderr = os.Stderr

	out, err := cmd.Output()
	if err != nil {
		return "", errors.New("no plan selected")
	}

	return strings.TrimSpace(string(out)), nil
}

// extractBranchName derives a branch name from a plan file path.
// removes the .md extension and strips any leading date prefix (e.g., "2024-01-15-").
func extractBranchName(planFile string) string {
	name := strings.TrimSuffix(filepath.Base(planFile), ".md")
	branchName := strings.TrimLeft(datePrefixRe.ReplaceAllString(name, ""), "-")
	if branchName == "" {
		return name
	}
	return branchName
}

func createBranchIfNeeded(gitOps *git.Repo, planFile string, colors *progress.Colors) error {
	currentBranch, err := gitOps.CurrentBranch()
	if err != nil {
		return fmt.Errorf("get current branch: %w", err)
	}

	if currentBranch != "main" && currentBranch != "master" {
		return nil // already on feature branch
	}

	branchName := extractBranchName(planFile)

	// check for uncommitted changes to files other than the plan
	hasOtherChanges, err := gitOps.HasChangesOtherThan(planFile)
	if err != nil {
		return fmt.Errorf("check uncommitted files: %w", err)
	}

	if hasOtherChanges {
		// other files have uncommitted changes - show helpful error
		return fmt.Errorf("cannot create branch %q: worktree has uncommitted changes\n\n"+
			"ralphex needs to create a feature branch from %s to isolate plan work.\n\n"+
			"options:\n"+
			"  git stash && ralphex %s && git stash pop   # stash changes temporarily\n"+
			"  git commit -am \"wip\"                       # commit changes first\n"+
			"  ralphex --review                           # skip branch creation (review-only mode)",
			branchName, currentBranch, planFile)
	}

	// check if plan file needs to be committed (untracked, modified, or staged)
	planHasChanges, err := gitOps.FileHasChanges(planFile)
	if err != nil {
		return fmt.Errorf("check plan file status: %w", err)
	}

	// create or switch to branch
	if gitOps.BranchExists(branchName) {
		colors.Info().Printf("switching to existing branch: %s\n", branchName)
		if err := gitOps.CheckoutBranch(branchName); err != nil {
			return fmt.Errorf("checkout branch %s: %w", branchName, err)
		}
	} else {
		colors.Info().Printf("creating branch: %s\n", branchName)
		if err := gitOps.CreateBranch(branchName); err != nil {
			return fmt.Errorf("create branch %s: %w", branchName, err)
		}
	}

	// auto-commit plan file if it was the only uncommitted file
	if planHasChanges {
		colors.Info().Printf("committing plan file: %s\n", filepath.Base(planFile))
		if err := gitOps.Add(planFile); err != nil {
			return fmt.Errorf("stage plan file: %w", err)
		}
		if err := gitOps.Commit("add plan: " + branchName); err != nil {
			return fmt.Errorf("commit plan file: %w", err)
		}
	}

	return nil
}

func movePlanToCompleted(gitOps *git.Repo, planFile string, colors *progress.Colors) error {
	// create completed directory
	completedDir := filepath.Join(filepath.Dir(planFile), "completed")
	if err := os.MkdirAll(completedDir, 0o750); err != nil {
		return fmt.Errorf("create completed dir: %w", err)
	}

	// destination path
	destPath := filepath.Join(completedDir, filepath.Base(planFile))

	// use git mv
	if err := gitOps.MoveFile(planFile, destPath); err != nil {
		// fallback to regular move for untracked files
		if renameErr := os.Rename(planFile, destPath); renameErr != nil {
			return fmt.Errorf("move plan: %w", renameErr)
		}
		// stage the new location - log if fails but continue
		if addErr := gitOps.Add(destPath); addErr != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to stage moved plan: %v\n", addErr)
		}
	}

	// commit the move
	commitMsg := "move completed plan: " + filepath.Base(planFile)
	if err := gitOps.Commit(commitMsg); err != nil {
		return fmt.Errorf("commit plan move: %w", err)
	}

	colors.Info().Printf("moved plan to %s\n", destPath)
	return nil
}

func ensureGitignore(gitOps *git.Repo, colors *progress.Colors) error {
	// check if already ignored
	ignored, err := gitOps.IsIgnored("progress-test.txt")
	if err == nil && ignored {
		return nil // already ignored
	}

	// write to .gitignore at repo root (not CWD)
	gitignorePath := filepath.Join(gitOps.Root(), ".gitignore")
	f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) //nolint:gosec // .gitignore needs world-readable
	if err != nil {
		return fmt.Errorf("open .gitignore: %w", err)
	}

	if _, err := f.WriteString("\n# ralphex progress logs\nprogress*.txt\n"); err != nil {
		f.Close()
		return fmt.Errorf("write .gitignore: %w", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("close .gitignore: %w", err)
	}

	colors.Info().Println("added progress*.txt to .gitignore")
	return nil
}

func checkDependencies(deps ...string) error {
	for _, dep := range deps {
		if _, err := exec.LookPath(dep); err != nil {
			return fmt.Errorf("%s not found in PATH", dep)
		}
	}
	return nil
}

// validateRepoHasCommits checks that the repository has at least one commit.
// returns a user-friendly error message if the repository is empty.
func validateRepoHasCommits(gitOps *git.Repo) error {
	hasCommits, err := gitOps.HasCommits()
	if err != nil {
		return fmt.Errorf("check commits: %w", err)
	}
	if !hasCommits {
		return errors.New("repository has no commits\n\n" +
			"ralphex needs at least one commit to create feature branches.\n\n" +
			"create an initial commit first:\n" +
			"  git add . && git commit -m \"initial commit\"")
	}
	return nil
}

func printStartupInfo(info startupInfo, colors *progress.Colors) {
	planStr := info.PlanFile
	if planStr == "" {
		planStr = "(no plan - review only)"
	}
	modeStr := ""
	if info.Mode != processor.ModeFull {
		modeStr = fmt.Sprintf(" (%s mode)", info.Mode)
	}
	colors.Info().Printf("starting ralphex loop: %s (max %d iterations)%s\n", planStr, info.MaxIterations, modeStr)
	colors.Info().Printf("branch: %s\n", info.Branch)
	colors.Info().Printf("progress log: %s\n\n", info.ProgressPath)
}

// runWatchOnly runs the web dashboard in watch-only mode without plan execution.
// monitors directories for progress files and serves the multi-session dashboard.
func runWatchOnly(ctx context.Context, o opts, cfg *config.Config, colors *progress.Colors) error {
	dirs := web.ResolveWatchDirs(o.Watch, cfg.WatchDirs)

	// fail fast if no watch directories configured
	if len(dirs) == 0 {
		return errors.New("no watch directories configured")
	}

	// setup server and watcher
	srvErrCh, watchErrCh, err := setupWatchMode(ctx, o.Port, dirs)
	if err != nil {
		return err
	}

	// print startup info
	printWatchModeInfo(dirs, o.Port, colors)

	// monitor for errors until shutdown
	return monitorWatchMode(ctx, srvErrCh, watchErrCh, colors)
}

// setupWatchMode creates and starts the web server and file watcher for watch-only mode.
// returns error channels for monitoring both components.
func setupWatchMode(ctx context.Context, port int, dirs []string) (chan error, chan error, error) {
	sm := web.NewSessionManager()
	watcher, err := web.NewWatcher(dirs, sm)
	if err != nil {
		return nil, nil, fmt.Errorf("create watcher: %w", err)
	}

	serverCfg := web.ServerConfig{
		Port:     port,
		PlanName: "(watch mode)",
		Branch:   "",
		PlanFile: "",
	}

	srv, err := web.NewServerWithSessions(serverCfg, sm)
	if err != nil {
		return nil, nil, fmt.Errorf("create web server: %w", err)
	}

	// start server with startup check
	srvErrCh, err := startServerAsync(ctx, srv, port)
	if err != nil {
		return nil, nil, err
	}

	// start watcher in background
	watchErrCh := make(chan error, 1)
	go func() {
		if watchErr := watcher.Start(ctx); watchErr != nil {
			watchErrCh <- watchErr
		}
		close(watchErrCh)
	}()

	return srvErrCh, watchErrCh, nil
}

// printWatchModeInfo prints startup information for watch-only mode.
func printWatchModeInfo(dirs []string, port int, colors *progress.Colors) {
	colors.Info().Printf("watch-only mode: monitoring %d directories\n", len(dirs))
	for _, dir := range dirs {
		colors.Info().Printf("  %s\n", dir)
	}
	colors.Info().Printf("web dashboard: http://localhost:%d\n", port)
	colors.Info().Printf("press Ctrl+C to exit\n")
}

// serverStartupTimeout is the time to wait for server startup before assuming success.
const serverStartupTimeout = 100 * time.Millisecond

// startServerAsync starts a web server in the background and waits briefly for startup errors.
// returns the error channel for monitoring late errors, or an error if startup fails.
func startServerAsync(ctx context.Context, srv *web.Server, port int) (chan error, error) {
	errCh := make(chan error, 1)
	go func() {
		if err := srv.Start(ctx); err != nil {
			errCh <- err
		}
		close(errCh)
	}()

	// wait briefly for startup errors
	select {
	case err := <-errCh:
		if err != nil {
			return nil, fmt.Errorf("web server failed to start on port %d: %w", port, err)
		}
	case <-time.After(serverStartupTimeout):
		// server started successfully
	}

	return errCh, nil
}

// monitorWatchMode monitors server and watcher error channels until shutdown.
func monitorWatchMode(ctx context.Context, srvErrCh, watchErrCh chan error, colors *progress.Colors) error {
	for {
		// exit when both channels are nil (closed and handled)
		if srvErrCh == nil && watchErrCh == nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return nil
		case srvErr, ok := <-srvErrCh:
			if !ok {
				srvErrCh = nil
				continue
			}
			if srvErr != nil && ctx.Err() == nil {
				colors.Error().Printf("web server error: %v\n", srvErr)
			}
		case watchErr, ok := <-watchErrCh:
			if !ok {
				watchErrCh = nil
				continue
			}
			if watchErr != nil && ctx.Err() == nil {
				colors.Error().Printf("file watcher error: %v\n", watchErr)
			}
		}
	}
}

// runPlanMode executes interactive plan creation mode.
// creates input collector, progress logger, and runs the plan creation loop.
// after plan creation, prompts user to continue with implementation or exit.
func runPlanMode(ctx context.Context, o opts, req executePlanRequest) error {
	// ensure gitignore has progress files
	if gitignoreErr := ensureGitignore(req.GitOps, req.Colors); gitignoreErr != nil {
		return gitignoreErr
	}

	branch := getCurrentBranch(req.GitOps)

	// create progress logger for plan mode
	baseLog, err := progress.NewLogger(progress.Config{
		PlanDescription: o.PlanDescription,
		Mode:            string(processor.ModePlan),
		Branch:          branch,
		NoColor:         o.NoColor,
	}, req.Colors)
	if err != nil {
		return fmt.Errorf("create progress logger: %w", err)
	}
	defer func() {
		if closeErr := baseLog.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to close progress log: %v\n", closeErr)
		}
	}()

	// print startup info for plan mode
	printPlanModeInfo(o.PlanDescription, branch, o.MaxIterations, baseLog.Path(), req.Colors)

	// create input collector
	collector := input.NewTerminalCollector()

	// record start time for finding the created plan
	startTime := time.Now()

	// create and configure runner
	r := processor.New(processor.Config{
		PlanDescription:  o.PlanDescription,
		ProgressPath:     baseLog.Path(),
		Mode:             processor.ModePlan,
		MaxIterations:    o.MaxIterations,
		Debug:            o.Debug,
		NoColor:          o.NoColor,
		IterationDelayMs: req.Config.IterationDelayMs,
		AppConfig:        req.Config,
	}, baseLog)
	r.SetInputCollector(collector)

	// run the plan creation loop
	if runErr := r.Run(ctx); runErr != nil {
		return fmt.Errorf("plan creation: %w", runErr)
	}

	// find the newly created plan file
	planFile := findRecentPlan(req.Config.PlansDir, startTime)
	elapsed := baseLog.Elapsed()

	// print completion message with plan file path if found
	if planFile != "" {
		relPath, relErr := filepath.Rel(".", planFile)
		if relErr != nil {
			relPath = planFile
		}
		req.Colors.Info().Printf("\nplan creation completed in %s, created %s\n", elapsed, relPath)
	} else {
		req.Colors.Info().Printf("\nplan creation completed in %s\n", elapsed)
	}

	// if no plan file found, can't continue to implementation
	if planFile == "" {
		return nil
	}

	// ask user if they want to continue with plan implementation
	answer, askErr := collector.AskQuestion(ctx, "Continue with plan implementation?",
		[]string{"Yes, execute plan", "No, exit"})
	if askErr != nil {
		// user canceled or error - treat as exit (context canceled is expected)
		if ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "warning: input error: %v\n", askErr)
		}
		return nil
	}

	// check if user wants to continue
	if !strings.HasPrefix(answer, "Yes") {
		return nil
	}

	return continuePlanExecution(ctx, o, executePlanRequest{
		PlanFile: planFile,
		Mode:     processor.ModeFull,
		GitOps:   req.GitOps,
		Config:   req.Config,
		Colors:   req.Colors,
	})
}

// continuePlanExecution runs full execution mode after plan creation completes.
// creates branch and delegates to executePlan for the main execution loop.
func continuePlanExecution(ctx context.Context, o opts, req executePlanRequest) error {
	req.Colors.Info().Printf("\ncontinuing with plan implementation...\n")

	// create branch if needed
	if branchErr := createBranchIfNeeded(req.GitOps, req.PlanFile, req.Colors); branchErr != nil {
		return branchErr
	}

	return executePlan(ctx, o, req)
}

// findRecentPlan finds the most recently modified .md file in plansDir
// that was modified after startTime. Returns empty string if none found.
func findRecentPlan(plansDir string, startTime time.Time) string {
	// find all .md files in plansDir (excluding completed/ subdirectory)
	pattern := filepath.Join(plansDir, "*.md")
	plans, err := filepath.Glob(pattern)
	if err != nil || len(plans) == 0 {
		return ""
	}

	var recentPlan string
	var recentTime time.Time

	for _, plan := range plans {
		info, statErr := os.Stat(plan)
		if statErr != nil {
			continue
		}
		// file must be modified after startTime
		if info.ModTime().Before(startTime) {
			continue
		}
		// find the most recent one
		if recentPlan == "" || info.ModTime().After(recentTime) {
			recentPlan = plan
			recentTime = info.ModTime()
		}
	}

	return recentPlan
}

// printPlanModeInfo prints startup information for plan creation mode.
func printPlanModeInfo(description, branch string, maxIterations int, progressPath string, colors *progress.Colors) {
	colors.Info().Printf("starting interactive plan creation\n")
	colors.Info().Printf("request: %s\n", description)
	colors.Info().Printf("branch: %s (max %d iterations)\n", branch, maxIterations)
	colors.Info().Printf("progress log: %s\n\n", progressPath)
}

// startWebDashboard creates the web server and broadcast logger, starting the server in background.
// returns the broadcast logger to use for execution, or error if server fails to start.
// when watchDirs is non-empty, creates multi-session mode with file watching.
func startWebDashboard(ctx context.Context, p webDashboardParams) (processor.Logger, error) {
	// create session for SSE streaming (handles both live streaming and history replay)
	session := web.NewSession("main", p.BaseLog.Path())
	broadcastLog := web.NewBroadcastLogger(p.BaseLog, session)

	// extract plan name for display
	planName := "(no plan)"
	if p.PlanFile != "" {
		planName = filepath.Base(p.PlanFile)
	}

	cfg := web.ServerConfig{
		Port:     p.Port,
		PlanName: planName,
		Branch:   p.Branch,
		PlanFile: p.PlanFile,
	}

	// determine if we should use multi-session mode
	// multi-session mode is enabled when watch dirs are provided via CLI or config
	useMultiSession := len(p.WatchDirs) > 0 || len(p.ConfigWatchDirs) > 0

	var srv *web.Server
	var watcher *web.Watcher

	if useMultiSession {
		// multi-session mode: use SessionManager and Watcher
		sm := web.NewSessionManager()

		// register the live execution session so dashboard uses it instead of creating a duplicate
		// this ensures live events from BroadcastLogger go to the same session the dashboard displays
		sm.Register(session)

		// resolve watch directories (CLI > config > cwd)
		dirs := web.ResolveWatchDirs(p.WatchDirs, p.ConfigWatchDirs)

		var err error
		watcher, err = web.NewWatcher(dirs, sm)
		if err != nil {
			return nil, fmt.Errorf("create watcher: %w", err)
		}

		srv, err = web.NewServerWithSessions(cfg, sm)
		if err != nil {
			return nil, fmt.Errorf("create web server: %w", err)
		}
	} else {
		// single-session mode: direct session for current execution
		var err error
		srv, err = web.NewServer(cfg, session)
		if err != nil {
			return nil, fmt.Errorf("create web server: %w", err)
		}
	}

	// start server with startup check
	srvErrCh, err := startServerAsync(ctx, srv, p.Port)
	if err != nil {
		return nil, err
	}

	// start watcher in background if multi-session mode
	if watcher != nil {
		go func() {
			if watchErr := watcher.Start(ctx); watchErr != nil {
				// log error but don't fail - server can still work
				fmt.Fprintf(os.Stderr, "warning: watcher error: %v\n", watchErr)
			}
		}()
	}

	// monitor for late server errors in background
	// these are logged but don't fail the main execution since the dashboard is supplementary
	go func() {
		if srvErr := <-srvErrCh; srvErr != nil {
			fmt.Fprintf(os.Stderr, "warning: web server error during execution: %v\n", srvErr)
		}
	}()

	p.Colors.Info().Printf("web dashboard: http://localhost:%d\n", p.Port)
	return broadcastLog, nil
}
