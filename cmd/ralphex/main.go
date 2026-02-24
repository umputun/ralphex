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
	"sync"
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
	BaseRef         string   `short:"b" long:"base-ref" description:"override default branch for review diffs (branch name or commit hash)"`
	SkipFinalize    bool     `long:"skip-finalize" description:"skip finalize step even if enabled in config"`
	Worktree        bool     `long:"worktree" description:"run in isolated git worktree"`
	PlanDescription string   `long:"plan" description:"create plan interactively (enter plan description)"`
	Debug           bool     `short:"d" long:"debug" description:"enable debug logging"`
	NoColor         bool     `long:"no-color" description:"disable color output"`
	Version         bool     `short:"v" long:"version" description:"print version and exit"`
	Serve           bool     `short:"s" long:"serve" description:"start web dashboard for real-time streaming"`
	Port            int      `short:"p" long:"port" default:"8080" description:"web dashboard port"`
	Host            string   `long:"host" default:"127.0.0.1" env:"RALPHEX_WEB_HOST" description:"web dashboard listen address"`
	Watch           []string `short:"w" long:"watch" description:"directories to watch for progress files (repeatable)"`
	Reset           bool     `long:"reset" description:"interactively reset global config to embedded defaults"`
	DumpDefaults    string   `long:"dump-defaults" description:"extract raw embedded defaults to specified directory"`
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
	MainPlanFile  string // original plan path in main repo (worktree mode); empty in normal mode
	Mode          processor.Mode
	GitSvc        *git.Service
	MainGitSvc    *git.Service // main repo service for cross-boundary ops (worktree mode); nil in normal mode
	Config        *config.Config
	Colors        *progress.Colors
	DefaultBranch string
	NotifySvc     *notify.Service
	WtCleanup     *worktreeCleanupFn  // worktree cleanup for interrupt handler; nil when not in worktree mode
	ProgressLog   *progress.Logger    // pre-created logger (worktree mode); nil in normal mode
	PhaseHolder   *status.PhaseHolder // pre-created holder (worktree mode); nil in normal mode
}

// worktreeCleanupFn holds a worktree cleanup function with mutex for safe cross-goroutine access.
// the interrupt watcher goroutine calls cleanup on force-exit, while the main goroutine populates it.
type worktreeCleanupFn struct {
	mu sync.Mutex
	fn func()
}

func (c *worktreeCleanupFn) set(fn func()) {
	c.mu.Lock()
	c.fn = fn
	c.mu.Unlock()
}

func (c *worktreeCleanupFn) call() {
	c.mu.Lock()
	fn := c.fn
	c.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func main() {
	if os.Getenv("GO_FLAGS_COMPLETION") == "" {
		fmt.Printf("ralphex %s\n", resolveVersion())
	}

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

	// worktree cleanup function, populated after worktree creation.
	// synchronized for safe access from the interrupt watcher goroutine.
	wtCleanup := &worktreeCleanupFn{}

	// print immediate feedback when context is canceled (Ctrl+C).
	// returned cleanup ensures goroutine exits when run() returns, avoiding leaks in tests.
	defer startInterruptWatcher(ctx, func() {
		restoreTerminal()
		wtCleanup.call()
	})()

	// validate conflicting flags
	if err := validateFlags(o); err != nil {
		return err
	}

	// handle early-exit flags (before full config load)
	if done, err := handleEarlyFlags(o); err != nil || done {
		return err
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

	defaultBranch := resolveDefaultBranch(o.BaseRef, cfg.DefaultBranch, gitSvc.GetDefaultBranch())
	applyCLIOverrides(o, cfg)

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
			DefaultBranch: defaultBranch,
			NotifySvc:     notifySvc,
			WtCleanup:     wtCleanup,
		}, selector)
	}

	return selectAndExecutePlan(ctx, o, executePlanRequest{
		Mode:          mode,
		GitSvc:        gitSvc,
		Config:        cfg,
		Colors:        colors,
		DefaultBranch: defaultBranch,
		NotifySvc:     notifySvc,
		WtCleanup:     wtCleanup,
	}, selector)
}

// selectAndExecutePlan selects a plan file, sets up branch or worktree, and runs execution.
func selectAndExecutePlan(ctx context.Context, o opts, req executePlanRequest, selector *plan.Selector) error {
	// plan is optional only for review modes (ModeReview, ModeCodexOnly)
	planOptional := req.Mode == processor.ModeReview || req.Mode == processor.ModeCodexOnly
	planFile, err := selector.Select(ctx, o.PlanFile, planOptional)
	if err != nil {
		// check for auto-plan-mode: no plans found on main/master branch
		handled, autoPlanErr := tryAutoPlanMode(ctx, err, o, req, selector)
		if handled {
			return autoPlanErr
		}
		return fmt.Errorf("select plan: %w", err)
	}

	req.PlanFile = planFile

	// worktree mode: create worktree, chdir into it, run execution from there.
	// EnsureIgnored is called inside runWithWorktree after worktree creation
	// to avoid HasChangesOtherThan conflict in CreateWorktreeForPlan.
	if req.Config.WorktreeEnabled && planFile != "" && modeRequiresBranch(req.Mode) {
		return runWithWorktree(ctx, o, req)
	}

	// normal mode: create branch first, then add gitignore patterns.
	// EnsureIgnored must be called AFTER CreateBranchForPlan because it modifies
	// .gitignore, and CreateBranchForPlan checks HasChangesOtherThan(planFile).
	if planFile != "" && modeRequiresBranch(req.Mode) {
		if err := req.GitSvc.CreateBranchForPlan(planFile); err != nil {
			return fmt.Errorf("create branch for plan: %w", err)
		}
	}
	if err := req.GitSvc.EnsureIgnored(".ralphex/progress/", ".ralphex/progress/progress-test.txt"); err != nil {
		return fmt.Errorf("ensure gitignore: %w", err)
	}

	return executePlan(ctx, o, req)
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
func tryAutoPlanMode(ctx context.Context, err error, o opts, req executePlanRequest,
	selector *plan.Selector) (bool, error) {
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
	return true, runPlanMode(ctx, o, req, selector)
}

// executePlan runs the main execution loop for a plan file.
// handles progress logging, web dashboard, runner execution, and post-execution tasks.
// when req.ProgressLog and req.PhaseHolder are pre-created (worktree mode), uses them directly.
// when req.MainGitSvc is set, uses it for plan file operations (plan is in main repo).
func executePlan(ctx context.Context, o opts, req executePlanRequest) error {
	branch := getCurrentBranch(req.GitSvc)

	// use pre-created holder/logger (worktree mode) or create new ones
	holder := req.PhaseHolder
	if holder == nil {
		holder = &status.PhaseHolder{}
	}

	var baseLog *progress.Logger
	var closeOnce sync.Once
	closeLog := func() {} // no-op default for externally-owned logger
	if req.ProgressLog != nil {
		baseLog = req.ProgressLog
	} else {
		var err error
		baseLog, err = progress.NewLogger(progress.Config{
			PlanFile: req.PlanFile,
			Mode:     string(req.Mode),
			Branch:   branch,
			NoColor:  o.NoColor,
		}, req.Colors, holder)
		if err != nil {
			return fmt.Errorf("create progress logger: %w", err)
		}
		closeLog = func() {
			closeOnce.Do(func() {
				if closeErr := baseLog.Close(); closeErr != nil {
					fmt.Fprintf(os.Stderr, "warning: failed to close progress log: %v\n", closeErr)
				}
			})
		}
	}
	defer closeLog()

	// wrap logger with broadcast logger if --serve is enabled
	var runnerLog processor.Logger = baseLog
	if o.Serve {
		dashboard := web.NewDashboard(web.DashboardConfig{
			BaseLog:         baseLog,
			Port:            o.Port,
			Host:            o.Host,
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

	// get diff stats for completion message (optional - errors logged but don't block).
	// use worktree GitSvc (has correct HEAD with committed changes).
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

	// move completed plan to completed/ directory.
	// use MainGitSvc+MainPlanFile when available (worktree mode) because the plan file is in the main repo.
	if req.PlanFile != "" && modeRequiresBranch(req.Mode) {
		moveSvc := req.GitSvc
		movePlanFile := req.PlanFile
		if req.MainGitSvc != nil {
			moveSvc = req.MainGitSvc
		}
		if req.MainPlanFile != "" {
			movePlanFile = req.MainPlanFile
		}
		if moveErr := moveSvc.MovePlanToCompleted(movePlanFile); moveErr != nil {
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
		closeLog()
		req.Colors.Info().Printf("web dashboard still running at http://%s:%d (press Ctrl+C to exit)\n", web.ConnectHost(o.Host), o.Port)
		<-ctx.Done()
	}

	return nil
}

// runWithWorktree creates a worktree, creates the progress logger (before chdir so it lands
// in the main repo), chdirs into the worktree, and runs executePlan. On return the worktree
// is cleaned up and CWD is restored. req.WtCleanup is populated for interrupt handler use.
func runWithWorktree(ctx context.Context, o opts, req executePlanRequest) error {
	wtPath, planNeedsCommit, err := req.GitSvc.CreateWorktreeForPlan(req.PlanFile)
	if err != nil {
		return fmt.Errorf("create worktree: %w", err)
	}

	// register early cleanup so the interrupt handler's force-exit path (os.Exit after 5s)
	// can remove the worktree even during setup. overwritten with full cleanup after chdir.
	// RemoveWorktree is idempotent, so double-call from both early and safety-net defer is safe.
	req.WtCleanup.set(func() {
		if rmErr := req.GitSvc.RemoveWorktree(wtPath); rmErr != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to remove worktree: %v\n", rmErr)
		}
	})

	// safety net: remove worktree if setup fails before main cleanup is registered.
	// once main cleanup takes over (setupDone=true), this defer becomes a no-op.
	setupDone := false
	defer func() {
		if !setupDone {
			if rmErr := req.GitSvc.RemoveWorktree(wtPath); rmErr != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to remove worktree after setup error: %v\n", rmErr)
			}
		}
	}()

	// add gitignore patterns and commit if clean
	if igErr := ensureGitIgnored(req.GitSvc, ".ralphex/progress/", ".ralphex/progress/progress-test.txt",
		".ralphex/worktrees/", ".ralphex/worktrees/test"); igErr != nil {
		fmt.Fprintf(os.Stderr, "warning: gitignore setup: %v\n", igErr)
	}

	origDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	// create progress logger BEFORE chdir so progress files land in main repo's .ralphex/progress/.
	// use branch name derived from plan file since gitSvc still points at the main repo (on master).
	holder := &status.PhaseHolder{}
	branch := plan.ExtractBranchName(req.PlanFile)
	baseLog, err := progress.NewLogger(progress.Config{
		PlanFile: req.PlanFile,
		Mode:     string(req.Mode),
		Branch:   branch,
		NoColor:  o.NoColor,
	}, req.Colors, holder)
	if err != nil {
		return fmt.Errorf("create progress logger: %w", err)
	}
	defer func() {
		if closeErr := baseLog.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to close progress log: %v\n", closeErr)
		}
	}()

	// chdir into worktree
	if err = os.Chdir(wtPath); err != nil {
		return fmt.Errorf("chdir to worktree: %w", err)
	}

	// register cleanup: restore CWD and remove worktree.
	// sync.Once prevents double-execution between defer and interrupt handler's force-exit path.
	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			if chdirErr := os.Chdir(origDir); chdirErr != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to restore working directory: %v\n", chdirErr)
			}
			if rmErr := req.GitSvc.RemoveWorktree(wtPath); rmErr != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to remove worktree: %v\n", rmErr)
			}
		})
	}
	setupDone = true // disable safety-net defer, main cleanup takes over
	req.WtCleanup.set(cleanup)
	defer cleanup()

	// open git service inside worktree
	wtGitSvc, err := git.NewService(".", req.Colors.Info())
	if err != nil {
		return fmt.Errorf("open worktree git service: %w", err)
	}

	// resolve plan file path inside the worktree so Claude operates on the local copy,
	// not the original in the main repo. the plan was copied by CreateWorktreeForPlan.
	wtPlanFile := req.PlanFile
	if filepath.IsAbs(req.PlanFile) {
		// resolve symlinks on plan path to match GitSvc.Root() which is also resolved
		// (macOS: /tmp -> /private/tmp); without this, filepath.Rel produces wrong results
		resolvedPlan := req.PlanFile
		if resolved, evalErr := filepath.EvalSymlinks(resolvedPlan); evalErr == nil {
			resolvedPlan = resolved
		}
		if rel, relErr := filepath.Rel(req.GitSvc.Root(), resolvedPlan); relErr == nil {
			abs, absErr := filepath.Abs(rel) // resolve relative to CWD (now the worktree)
			if absErr == nil {
				wtPlanFile = abs
			}
		}
	}

	// commit plan file on the feature branch (inside worktree), not on main/master
	if planNeedsCommit {
		if commitErr := wtGitSvc.CommitPlanFile(req.PlanFile, req.GitSvc.Root()); commitErr != nil {
			return fmt.Errorf("commit plan in worktree: %w", commitErr)
		}
	}

	return executePlan(ctx, o, executePlanRequest{
		PlanFile:      wtPlanFile,
		MainPlanFile:  req.PlanFile, // original path in main repo for MovePlanToCompleted
		Mode:          req.Mode,
		GitSvc:        wtGitSvc,
		MainGitSvc:    req.GitSvc,
		Config:        req.Config,
		Colors:        req.Colors,
		DefaultBranch: req.DefaultBranch,
		NotifySvc:     req.NotifySvc,
		ProgressLog:   baseLog,
		PhaseHolder:   holder,
	})
}

// openGitService creates a git.Service for the current directory.
func openGitService(colors *progress.Colors) (*git.Service, error) {
	svc, err := git.NewService(".", colors.Info())
	if err != nil {
		return nil, fmt.Errorf("new git service: %w", err)
	}
	return svc, nil
}

// ensureGitIgnored adds patterns to .gitignore and commits if .gitignore was clean before.
// patterns are pairs of (pattern, probePath) passed to EnsureIgnored.
// returns error if arguments are invalid or pattern addition fails; commit errors are logged as warnings.
func ensureGitIgnored(gitSvc *git.Service, patternPairs ...string) error {
	if len(patternPairs)%2 != 0 {
		return errors.New("ensureGitIgnored requires pairs of (pattern, probePath)")
	}

	// track if .gitignore was already dirty before we modify it.
	// on error, assume dirty to avoid auto-committing unrelated user changes.
	igDirtyBefore, igErr := gitSvc.FileHasChanges(".gitignore")
	if igErr != nil {
		igDirtyBefore = true
		fmt.Fprintf(os.Stderr, "warning: failed to check .gitignore status: %v\n", igErr)
	}

	// iterate pairs (pattern, probePath); i+1 guard satisfies gosec G602 slice bounds check
	for i := 0; i+1 < len(patternPairs); i += 2 {
		if err := gitSvc.EnsureIgnored(patternPairs[i], patternPairs[i+1]); err != nil {
			return fmt.Errorf("ensure gitignore %s: %w", patternPairs[i], err)
		}
	}

	// commit .gitignore changes only if it was clean before to avoid
	// auto-committing unrelated user changes under the ralphex commit message.
	if !igDirtyBefore {
		if err := gitSvc.CommitIgnoreChanges(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to commit .gitignore: %v\n", err)
		}
	}
	return nil
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
		Host:   o.Host,
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

	modeStr := ""
	if info.Mode != processor.ModeFull {
		modeStr = fmt.Sprintf(" (%s mode)", info.Mode)
	}
	colors.Info().Printf("starting ralphex loop (max %d iterations)%s\n", info.MaxIterations, modeStr)
	if info.PlanFile != "" {
		colors.Info().Printf("plan: %s\n", toRelPath(info.PlanFile))
	}
	colors.Info().Printf("branch: %s\n", info.Branch)
	colors.Info().Printf("progress log: %s\n\n", info.ProgressPath)
}

// runPlanMode executes interactive plan creation mode.
// creates input collector, progress logger, and runs the plan creation loop.
// after plan creation, prompts user to continue with implementation or exit.
func runPlanMode(ctx context.Context, o opts, req executePlanRequest, selector *plan.Selector) error {
	// ensure gitignore has progress files (check dirty, add, commit if was clean)
	if err := ensureGitIgnored(req.GitSvc, ".ralphex/progress/", ".ralphex/progress/progress-test.txt"); err != nil {
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
	planFile := selector.FindRecent(startTime)
	elapsed := baseLog.Elapsed()

	// print completion message with plan file path if found
	if planFile != "" {
		req.Colors.Info().Printf("\nplan creation completed in %s, created %s\n", elapsed, toRelPath(planFile))
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

	// resolve plan file to absolute path before potential chdir
	planFile, err = filepath.Abs(planFile)
	if err != nil {
		return fmt.Errorf("resolve plan file: %w", err)
	}

	// continue with plan implementation
	req.Colors.Info().Printf("\ncontinuing with plan implementation...\n")

	// worktree mode: create worktree and run from there
	if req.Config.WorktreeEnabled {
		return runWithWorktree(ctx, o, executePlanRequest{
			PlanFile:      planFile,
			Mode:          processor.ModeFull,
			GitSvc:        req.GitSvc,
			Config:        req.Config,
			Colors:        req.Colors,
			DefaultBranch: req.DefaultBranch,
			NotifySvc:     req.NotifySvc,
			WtCleanup:     req.WtCleanup,
		})
	}

	// normal mode: create branch and run in place
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

// handleEarlyFlags processes flags that should run before full config load (--reset, --dump-defaults).
// returns (true, nil) if an early exit occurred, (true, err) on error, or (false, nil) to continue.
func handleEarlyFlags(o opts) (bool, error) {
	if o.Reset {
		if err := runReset(o.ConfigDir, os.Stdin, os.Stdout); err != nil {
			return true, err
		}
		if isResetOnly(o) {
			return true, nil
		}
	}

	if o.DumpDefaults != "" {
		return true, dumpDefaults(o.DumpDefaults)
	}

	return false, nil
}

// dumpDefaults extracts raw embedded defaults to the specified directory.
func dumpDefaults(dir string) error {
	if err := config.DumpDefaults(dir); err != nil {
		return fmt.Errorf("dump defaults: %w", err)
	}
	fmt.Printf("defaults extracted to %s\n", dir)
	return nil
}

// toRelPath converts an absolute path to relative (from cwd). returns original on error.
func toRelPath(p string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return p
	}
	rel, err := filepath.Rel(cwd, p)
	if err != nil {
		return p
	}
	// if relative path escapes too far (e.g. worktree -> main repo), use absolute path instead
	if len(rel) > 6 && rel[:6] == "../../" {
		return p
	}
	return rel
}

// isResetOnly returns true if --reset was the only meaningful flag/arg specified.
// this allows reset to work standalone (exit after reset) while also supporting
// combined usage like "ralphex --reset docs/plans/feature.md".
func isResetOnly(o opts) bool {
	return o.PlanFile == "" && !o.Review && !o.ExternalOnly && !o.CodexOnly && !o.TasksOnly && !o.Serve && o.PlanDescription == "" && len(o.Watch) == 0 && o.DumpDefaults == ""
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

// applyCLIOverrides applies CLI flag overrides to config.
func applyCLIOverrides(o opts, cfg *config.Config) {
	if o.SkipFinalize {
		cfg.FinalizeEnabled = false
	}
	if o.Worktree {
		cfg.WorktreeEnabled = true
	}
}

// resolveDefaultBranch returns the default branch using precedence: CLI flag > config > auto-detect.
func resolveDefaultBranch(cliRef, configBranch, autoDetected string) string {
	if cliRef != "" {
		return cliRef
	}
	if configBranch != "" {
		return configBranch
	}
	return autoDetected
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
