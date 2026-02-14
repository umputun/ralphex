// Package main provides ralphex - autonomous plan execution with Claude Code.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/jessevdk/go-flags"

	"github.com/umputun/ralphex/pkg/config"
	"github.com/umputun/ralphex/pkg/git"
	"github.com/umputun/ralphex/pkg/input"
	"github.com/umputun/ralphex/pkg/notify"
	"github.com/umputun/ralphex/pkg/plan"
	"github.com/umputun/ralphex/pkg/processor"
	"github.com/umputun/ralphex/pkg/progress"
	"github.com/umputun/ralphex/pkg/status"
	"github.com/umputun/ralphex/pkg/web"
)

// opts holds all command-line options.
type opts struct {
	MaxIterations   int      `short:"m" long:"max-iterations" default:"50" description:"maximum task iterations"`
	Review          bool     `short:"r" long:"review" description:"skip task execution, run full review pipeline"`
	ExternalOnly    bool     `short:"e" long:"external-only" description:"skip tasks and first review, run only external review loop"`
	CodexOnly       bool     `short:"c" long:"codex-only" description:"alias for --external-only (deprecated)"`
	TasksOnly       bool     `short:"t" long:"tasks-only" description:"run only task phase, skip all reviews"`
	PlanDescription string   `long:"plan" description:"create plan interactively (enter plan description)"`
	Debug           bool     `short:"d" long:"debug" description:"enable debug logging"`
	NoColor         bool     `long:"no-color" description:"disable color output"`
	Version         bool     `short:"v" long:"version" description:"print version and exit"`
	Serve           bool     `short:"s" long:"serve" description:"start web dashboard for real-time streaming"`
	Port            int      `short:"p" long:"port" default:"8080" description:"web dashboard port"`
	Watch           []string `short:"w" long:"watch" description:"directories to watch for progress files (repeatable)"`
	Reset           bool     `long:"reset" description:"interactively reset global config to embedded defaults"`
	ConfigDir       string   `long:"config-dir" env:"RALPHEX_CONFIG_DIR" description:"custom config directory"`

	PlanFile string `positional-arg-name:"plan-file" description:"path to plan file (optional, uses fzf if omitted)"`
}

var revision = "unknown"

// resolveVersion returns the best available version string.
// priority: ldflags revision → module version from go install → VCS commit hash → "unknown".
func resolveVersion() string {
	if revision != "unknown" {
		return revision
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return revision
	}
	// go install sets module version to the tag (e.g. v0.10.0)
	if bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	// local build without ldflags — try VCS revision
	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 7 {
			return s.Value[:7]
		}
	}
	return revision
}

// stderrLog is a simple logger that writes to stderr.
// satisfies notify.logger interface for use before progress logger is available.
type stderrLog struct{}

func (stderrLog) Print(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

// startupInfo holds parameters for printing startup information.
type startupInfo struct {
	PlanFile        string
	PlanDescription string // used for plan mode instead of PlanFile
	Branch          string
	Mode            processor.Mode
	MaxIterations   int
	ProgressPath    string
}

// executePlanRequest holds parameters for plan execution.
type executePlanRequest struct {
	PlanFile      string
	Mode          processor.Mode
	GitSvc        *git.Service
	Config        *config.Config
	Colors        *progress.Colors
	Selector      *plan.Selector
	DefaultBranch string
	NotifySvc     *notify.Service
}

func main() {
	fmt.Printf("ralphex %s\n", resolveVersion())

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
	// suppress ^C echo in terminal before setting up interrupt watcher
	restoreTerminal := disableCtrlCEcho()
	defer restoreTerminal()

	// print immediate feedback when context is canceled (Ctrl+C).
	// returned cleanup ensures goroutine exits when run() returns, avoiding leaks in tests.
	defer startInterruptWatcher(ctx, restoreTerminal)()

	// validate conflicting flags
	if err := validateFlags(o); err != nil {
		return err
	}

	// handle --reset flag early (before full config load)
	// reset completes, then continues with normal execution if other args provided
	if o.Reset {
		if err := runReset(o.ConfigDir, os.Stdin, os.Stdout); err != nil {
			return err
		}
		// if reset was the only operation, exit successfully
		if isResetOnly(o) {
			return nil
		}
	}

	// load config first to get custom command paths
	cfg, err := config.Load(o.ConfigDir)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// create colors from config (all colors guaranteed populated via fallback)
	colors := progress.NewColors(cfg.Colors)

	// create notification service (nil if no channels configured)
	notifySvc, err := notify.New(cfg.NotifyParams, stderrLog{})
	if err != nil {
		return fmt.Errorf("create notification service: %w", err)
	}

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

	// open git repository via Service
	gitSvc, err := openGitService(colors)
	if err != nil {
		return fmt.Errorf("open git repo: %w", err)
	}

	// ensure repository has commits (prompts to create initial commit if empty)
	if ensureErr := ensureRepoHasCommits(ctx, gitSvc, os.Stdin, os.Stdout); ensureErr != nil {
		return ensureErr
	}

	// detect default branch for prompt templates
	defaultBranch := gitSvc.GetDefaultBranch()

	mode := determineMode(o)

	// create plan selector for use by plan selection and plan mode
	selector := plan.NewSelector(cfg.PlansDir, colors)

	// plan mode has different flow - doesn't require plan file selection
	if mode == processor.ModePlan {
		return runPlanMode(ctx, o, executePlanRequest{
			Mode:          processor.ModePlan,
			GitSvc:        gitSvc,
			Config:        cfg,
			Colors:        colors,
			Selector:      selector,
			DefaultBranch: defaultBranch,
			NotifySvc:     notifySvc,
		})
	}

	// select and prepare plan file (not needed for plan mode)
	// plan is optional only for review modes (ModeReview, ModeCodexOnly)
	planOptional := mode == processor.ModeReview || mode == processor.ModeCodexOnly
	planFile, err := selector.Select(ctx, o.PlanFile, planOptional)
	if err != nil {
		// check for auto-plan-mode: no plans found on main/master branch
		handled, autoPlanErr := tryAutoPlanMode(ctx, err, o, executePlanRequest{
			GitSvc:        gitSvc,
			Config:        cfg,
			Colors:        colors,
			Selector:      selector,
			DefaultBranch: defaultBranch,
			NotifySvc:     notifySvc,
		})
		if handled {
			return autoPlanErr
		}
		return fmt.Errorf("select plan: %w", err)
	}

	// setup git for execution (branch, gitignore)
	if planFile != "" && modeRequiresBranch(mode) {
		if err := gitSvc.CreateBranchForPlan(planFile); err != nil {
			return fmt.Errorf("create branch for plan: %w", err)
		}
	}
	if err := gitSvc.EnsureIgnored(".ralphex/progress/", ".ralphex/progress/progress-test.txt"); err != nil {
		return fmt.Errorf("ensure gitignore: %w", err)
	}

	return executePlan(ctx, o, executePlanRequest{
		PlanFile:      planFile,
		Mode:          mode,
		GitSvc:        gitSvc,
		Config:        cfg,
		Colors:        colors,
		Selector:      selector,
		DefaultBranch: defaultBranch,
		NotifySvc:     notifySvc,
	})
}

// getCurrentBranch returns the current git branch name or "unknown" if unavailable.
func getCurrentBranch(gitSvc *git.Service) string {
	branch, err := gitSvc.CurrentBranch()
	if err != nil || branch == "" {
		return "unknown"
	}
	return branch
}

// tryAutoPlanMode attempts to switch to plan mode when no plans are found on main/master.
// returns (true, nil) if user canceled, (true, err) if plan mode was attempted, or (false, nil) if auto-plan-mode doesn't apply.
func tryAutoPlanMode(ctx context.Context, err error, o opts, req executePlanRequest) (bool, error) {
	if !errors.Is(err, plan.ErrNoPlansFound) || o.Review || o.ExternalOnly || o.CodexOnly || o.TasksOnly {
		return false, nil
	}

	isMain, branchErr := req.GitSvc.IsMainBranch()
	if branchErr != nil || !isMain {
		return false, nil //nolint:nilerr // branchErr is intentionally ignored - if we can't get branch, skip auto-plan-mode
	}

	description := plan.PromptDescription(ctx, os.Stdin, req.Colors)
	if description == "" {
		return true, nil // user canceled
	}

	o.PlanDescription = description
	req.Mode = processor.ModePlan
	return true, runPlanMode(ctx, o, req)
}

// executePlan runs the main execution loop for a plan file.
// handles progress logging, web dashboard, runner execution, and post-execution tasks.
func executePlan(ctx context.Context, o opts, req executePlanRequest) error {
	branch := getCurrentBranch(req.GitSvc)

	// create shared phase holder (single source of truth for current phase)
	holder := &status.PhaseHolder{}

	// create progress logger
	baseLog, err := progress.NewLogger(progress.Config{
		PlanFile: req.PlanFile,
		Mode:     string(req.Mode),
		Branch:   branch,
		NoColor:  o.NoColor,
	}, req.Colors, holder)
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
	var runnerLog processor.Logger = baseLog
	if o.Serve {
		dashboard := web.NewDashboard(web.DashboardConfig{
			BaseLog:         baseLog,
			Port:            o.Port,
			PlanFile:        req.PlanFile,
			Branch:          branch,
			WatchDirs:       o.Watch,
			ConfigWatchDirs: req.Config.WatchDirs,
			Colors:          req.Colors,
		}, holder)
		var dashErr error
		runnerLog, dashErr = dashboard.Start(ctx)
		if dashErr != nil {
			return fmt.Errorf("start dashboard: %w", dashErr)
		}
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
	r := createRunner(req, o, runnerLog, holder)
	if runErr := r.Run(ctx); runErr != nil {
		// send failure notification before returning error.
		// use context.Background() because the parent ctx may be canceled (e.g. SIGINT),
		// and the notification timeout is applied inside Send() independently.
		req.NotifySvc.Send(context.Background(), notify.Result{
			Status:   "failure",
			Mode:     string(req.Mode),
			PlanFile: req.PlanFile,
			Branch:   branch,
			Duration: baseLog.Elapsed(),
			Error:    runErr.Error(),
		})
		return fmt.Errorf("runner: %w", runErr)
	}

	elapsed := baseLog.Elapsed()

	// get diff stats for completion message (optional - errors logged but don't block)
	stats, statsErr := req.GitSvc.DiffStats(req.DefaultBranch)
	if statsErr != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to get diff stats: %v\n", statsErr)
	}

	// send success notification.
	// use context.Background() because the parent ctx may be canceled (e.g. SIGINT),
	// and the notification timeout is applied inside Send() independently.
	req.NotifySvc.Send(context.Background(), notify.Result{
		Status:    "success",
		Mode:      string(req.Mode),
		PlanFile:  req.PlanFile,
		Branch:    branch,
		Duration:  elapsed,
		Files:     stats.Files,
		Additions: stats.Additions,
		Deletions: stats.Deletions,
	})

	// move completed plan to completed/ directory
	if req.PlanFile != "" && modeRequiresBranch(req.Mode) {
		if moveErr := req.GitSvc.MovePlanToCompleted(req.PlanFile); moveErr != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to move plan to completed: %v\n", moveErr)
		}
	}

	// display completion with stats
	if stats.Files > 0 {
		baseLog.LogDiffStats(stats.Files, stats.Additions, stats.Deletions)
		req.Colors.Info().Printf("\ncompleted in %s (%d files, +%d/-%d lines)\n",
			elapsed, stats.Files, stats.Additions, stats.Deletions)
	} else {
		req.Colors.Info().Printf("\ncompleted in %s\n", elapsed)
	}

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

// openGitService creates a git.Service for the current directory.
func openGitService(colors *progress.Colors) (*git.Service, error) {
	svc, err := git.NewService(".", colors.Info())
	if err != nil {
		return nil, fmt.Errorf("new git service: %w", err)
	}
	return svc, nil
}

// checkClaudeDep checks that the claude command is available in PATH.
func checkClaudeDep(cfg *config.Config) error {
	claudeCmd := cfg.ClaudeCommand
	if claudeCmd == "" {
		claudeCmd = "claude"
	}
	if _, err := exec.LookPath(claudeCmd); err != nil {
		return fmt.Errorf("%s not found in PATH", claudeCmd)
	}
	return nil
}

// isWatchOnlyMode returns true if running in watch-only mode.
// watch-only mode runs the web dashboard without executing any plan.
func isWatchOnlyMode(o opts, configWatchDirs []string) bool {
	return o.Serve && o.PlanFile == "" && o.PlanDescription == "" && (len(o.Watch) > 0 || len(configWatchDirs) > 0)
}

// runWatchOnly starts the web dashboard in watch-only mode without plan execution.
func runWatchOnly(ctx context.Context, o opts, cfg *config.Config, colors *progress.Colors) error {
	dirs := web.ResolveWatchDirs(o.Watch, cfg.WatchDirs)
	dashboard := web.NewDashboard(web.DashboardConfig{
		Port:   o.Port,
		Colors: colors,
	}, nil)
	if watchErr := dashboard.RunWatchOnly(ctx, dirs); watchErr != nil {
		return fmt.Errorf("run watch-only mode: %w", watchErr)
	}
	return nil
}

// determineMode returns the execution mode based on CLI flags.
func determineMode(o opts) processor.Mode {
	switch {
	case o.PlanDescription != "":
		return processor.ModePlan
	case o.TasksOnly:
		return processor.ModeTasksOnly
	case o.ExternalOnly || o.CodexOnly:
		return processor.ModeCodexOnly
	case o.Review:
		return processor.ModeReview
	default:
		return processor.ModeFull
	}
}

// modeRequiresBranch returns true if the mode requires creating a feature branch.
// ModeFull and ModeTasksOnly both execute tasks that make commits, requiring a branch.
func modeRequiresBranch(mode processor.Mode) bool {
	return mode == processor.ModeFull || mode == processor.ModeTasksOnly
}

// validateFlags checks for conflicting CLI flags.
func validateFlags(o opts) error {
	if o.PlanDescription != "" && o.PlanFile != "" {
		return errors.New("--plan flag conflicts with plan file argument; use one or the other")
	}
	return nil
}

// createRunner creates a processor.Runner with the given configuration.
func createRunner(req executePlanRequest, o opts, log processor.Logger, holder *status.PhaseHolder) *processor.Runner {
	// --codex-only mode forces codex enabled regardless of config
	codexEnabled := req.Config.CodexEnabled
	if req.Mode == processor.ModeCodexOnly {
		codexEnabled = true
	}
	r := processor.New(processor.Config{
		PlanFile:         req.PlanFile,
		ProgressPath:     log.Path(),
		Mode:             req.Mode,
		MaxIterations:    o.MaxIterations,
		Debug:            o.Debug,
		NoColor:          o.NoColor,
		IterationDelayMs: req.Config.IterationDelayMs,
		TaskRetryCount:   req.Config.TaskRetryCount,
		CodexEnabled:     codexEnabled,
		FinalizeEnabled:  req.Config.FinalizeEnabled,
		DefaultBranch:    req.DefaultBranch,
		AppConfig:        req.Config,
	}, log, holder)
	if req.GitSvc != nil {
		r.SetGitChecker(req.GitSvc)
	}
	return r
}

func printStartupInfo(info startupInfo, colors *progress.Colors) {
	if info.Mode == processor.ModePlan {
		colors.Info().Printf("starting interactive plan creation\n")
		colors.Info().Printf("request: %s\n", info.PlanDescription)
		colors.Info().Printf("branch: %s (max %d iterations)\n", info.Branch, info.MaxIterations)
		colors.Info().Printf("progress log: %s\n\n", info.ProgressPath)
		return
	}

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

// runPlanMode executes interactive plan creation mode.
// creates input collector, progress logger, and runs the plan creation loop.
// after plan creation, prompts user to continue with implementation or exit.
func runPlanMode(ctx context.Context, o opts, req executePlanRequest) error {
	// ensure gitignore has progress files
	if err := req.GitSvc.EnsureIgnored(".ralphex/progress/", ".ralphex/progress/progress-test.txt"); err != nil {
		return fmt.Errorf("ensure gitignore: %w", err)
	}

	branch := getCurrentBranch(req.GitSvc)

	// create shared phase holder (single source of truth for current phase)
	holder := &status.PhaseHolder{}

	// create progress logger for plan mode
	baseLog, err := progress.NewLogger(progress.Config{
		PlanDescription: o.PlanDescription,
		Mode:            string(processor.ModePlan),
		Branch:          branch,
		NoColor:         o.NoColor,
	}, req.Colors, holder)
	if err != nil {
		return fmt.Errorf("create progress logger: %w", err)
	}
	defer func() {
		if closeErr := baseLog.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to close progress log: %v\n", closeErr)
		}
	}()

	// print startup info for plan mode
	printStartupInfo(startupInfo{
		PlanDescription: o.PlanDescription,
		Branch:          branch,
		Mode:            processor.ModePlan,
		MaxIterations:   o.MaxIterations,
		ProgressPath:    baseLog.Path(),
	}, req.Colors)

	// create input collector
	collector := input.NewTerminalCollector(o.NoColor)

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
		DefaultBranch:    req.DefaultBranch,
		AppConfig:        req.Config,
	}, baseLog, holder)
	r.SetInputCollector(collector)

	// run the plan creation loop
	if runErr := r.Run(ctx); runErr != nil {
		return fmt.Errorf("plan creation: %w", runErr)
	}

	// find the newly created plan file
	planFile := req.Selector.FindRecent(startTime)
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
	if !input.AskYesNo(ctx, "Continue with plan implementation?", os.Stdin, os.Stdout) {
		return nil
	}

	// continue with plan implementation
	req.Colors.Info().Printf("\ncontinuing with plan implementation...\n")

	// create branch if needed
	if err := req.GitSvc.CreateBranchForPlan(planFile); err != nil {
		return fmt.Errorf("create branch for plan: %w", err)
	}

	return executePlan(ctx, o, executePlanRequest{
		PlanFile:      planFile,
		Mode:          processor.ModeFull,
		GitSvc:        req.GitSvc,
		Config:        req.Config,
		Colors:        req.Colors,
		DefaultBranch: req.DefaultBranch,
		NotifySvc:     req.NotifySvc,
	})
}

// runReset runs the interactive config reset flow.
func runReset(configDir string, stdin io.Reader, stdout io.Writer) error {
	_, err := config.Reset(configDir, stdin, stdout)
	if err != nil {
		return fmt.Errorf("reset config: %w", err)
	}
	return nil
}

// isResetOnly returns true if --reset was the only meaningful flag/arg specified.
// this allows reset to work standalone (exit after reset) while also supporting
// combined usage like "ralphex --reset docs/plans/feature.md".
func isResetOnly(o opts) bool {
	return o.PlanFile == "" && !o.Review && !o.ExternalOnly && !o.CodexOnly && !o.TasksOnly && !o.Serve && o.PlanDescription == "" && len(o.Watch) == 0
}

// startInterruptWatcher prints immediate feedback when context is canceled.
// if graceful shutdown doesn't complete within 5 seconds, force exits.
// cleanup, if not nil, is called only on the force-exit (5s timeout) path before os.Exit.
// returns a cleanup function that must be called (via defer) to prevent goroutine leaks.
func startInterruptWatcher(ctx context.Context, cleanup func()) func() {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			fmt.Fprintf(os.Stderr, "\ninterrupting... (force exit in 5s)\n")
			select {
			case <-time.After(5 * time.Second):
				fmt.Fprintf(os.Stderr, "force exit\n")
				if cleanup != nil {
					cleanup()
				}
				os.Exit(1)
			case <-done:
			}
		case <-done:
		}
	}()
	return func() { close(done) }
}

// ensureRepoHasCommits checks that the repository has at least one commit.
// If the repository is empty, prompts the user to create an initial commit.
func ensureRepoHasCommits(ctx context.Context, gitSvc *git.Service, stdin io.Reader, stdout io.Writer) error {
	// track if we actually created a commit
	createdCommit := false
	promptFn := func() bool {
		fmt.Fprintln(stdout, "repository has no commits")
		fmt.Fprintln(stdout, "ralphex needs at least one commit to create feature branches.")
		fmt.Fprintln(stdout)
		if !input.AskYesNo(ctx, "create initial commit?", stdin, stdout) {
			return false
		}
		createdCommit = true
		return true
	}

	if err := gitSvc.EnsureHasCommits(promptFn); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("create initial commit: %w", ctx.Err())
		}
		return fmt.Errorf("ensure has commits: %w", err)
	}
	if createdCommit {
		fmt.Fprintln(stdout, "created initial commit")
	}
	return nil
}
