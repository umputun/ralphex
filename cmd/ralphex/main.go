// Package main provides ralphex - autonomous plan execution with Claude Code.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/jessevdk/go-flags"

	"github.com/umputun/ralphex/pkg/config"
	"github.com/umputun/ralphex/pkg/git"
	"github.com/umputun/ralphex/pkg/processor"
	"github.com/umputun/ralphex/pkg/progress"
)

// opts holds all command-line options.
type opts struct {
	MaxIterations int  `short:"m" long:"max-iterations" default:"50" description:"maximum task iterations"`
	Review        bool `short:"r" long:"review" description:"skip task execution, run full review pipeline"`
	CodexOnly     bool `short:"c" long:"codex-only" description:"skip tasks and first review, run only codex loop"`
	Debug         bool `short:"d" long:"debug" description:"enable debug logging"`
	NoColor       bool `long:"no-color" description:"disable color output"`
	Version       bool `short:"v" long:"version" description:"print version and exit"`

	PlanFile string `positional-arg-name:"plan-file" description:"path to plan file (optional, uses fzf if omitted)"`
}

var revision = "unknown"

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
	// load config first to get custom command paths
	cfg, err := config.Load("") // empty string uses default location
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// create colors from config (all colors guaranteed populated via fallback)
	colors := progress.NewColors(progress.ColorConfig{
		Task:       cfg.Colors.Task,
		Review:     cfg.Colors.Review,
		Codex:      cfg.Colors.Codex,
		ClaudeEval: cfg.Colors.ClaudeEval,
		Warn:       cfg.Colors.Warn,
		Error:      cfg.Colors.Error,
		Signal:     cfg.Colors.Signal,
		Timestamp:  cfg.Colors.Timestamp,
		Info:       cfg.Colors.Info,
	})

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

	// select and prepare plan file
	skipTasks := o.Review || o.CodexOnly
	planFile, err := preparePlanFile(ctx, o.PlanFile, skipTasks, cfg.PlansDir, colors)
	if err != nil {
		return err
	}

	// create branch if on main/master
	if planFile != "" {
		if branchErr := createBranchIfNeeded(gitOps, planFile, colors); branchErr != nil {
			return branchErr
		}
	}

	// ensure progress files are gitignored
	if gitErr := ensureGitignore(gitOps, colors); gitErr != nil {
		return gitErr
	}

	mode := determineMode(o)

	// get current branch for logging
	branch, err := gitOps.CurrentBranch()
	if err != nil || branch == "" {
		branch = "unknown"
	}

	// create progress logger
	log, err := progress.NewLogger(progress.Config{
		PlanFile: planFile,
		Mode:     string(mode),
		Branch:   branch,
		NoColor:  o.NoColor,
	}, colors)
	if err != nil {
		return fmt.Errorf("create progress logger: %w", err)
	}
	defer log.Close()

	// print startup info
	printStartupInfo(planFile, branch, mode, o.MaxIterations, log.Path(), colors)

	// create and run the runner
	r := createRunner(cfg, o, planFile, mode, log)
	if runErr := r.Run(ctx); runErr != nil {
		return fmt.Errorf("runner: %w", runErr)
	}

	// move completed plan to completed/ directory
	if planFile != "" && mode == processor.ModeFull {
		if moveErr := movePlanToCompleted(gitOps, planFile, colors); moveErr != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to move plan to completed: %v\n", moveErr)
		}
	}

	colors.Info().Printf("\ncompleted in %s\n", log.Elapsed())
	return nil
}

// checkClaudeDep checks that the claude command is available in PATH.
func checkClaudeDep(cfg *config.Config) error {
	claudeCmd := cfg.ClaudeCommand
	if claudeCmd == "" {
		claudeCmd = "claude"
	}
	return checkDependencies(claudeCmd)
}

// determineMode returns the execution mode based on CLI flags.
func determineMode(o opts) processor.Mode {
	switch {
	case o.CodexOnly:
		return processor.ModeCodexOnly
	case o.Review:
		return processor.ModeReview
	default:
		return processor.ModeFull
	}
}

// createRunner creates a processor.Runner with the given configuration.
func createRunner(cfg *config.Config, o opts, planFile string, mode processor.Mode, log *progress.Logger) *processor.Runner {
	// --codex-only mode forces codex enabled regardless of config
	codexEnabled := cfg.CodexEnabled
	if mode == processor.ModeCodexOnly {
		codexEnabled = true
	}
	return processor.New(processor.Config{
		PlanFile:         planFile,
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

func preparePlanFile(ctx context.Context, planFile string, skipTasks bool, plansDir string, colors *progress.Colors) (string, error) {
	selected, err := selectPlan(ctx, planFile, skipTasks, plansDir, colors)
	if err != nil {
		return "", err
	}
	if selected == "" {
		if !skipTasks {
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

func selectPlan(ctx context.Context, planFile string, optional bool, plansDir string, colors *progress.Colors) (string, error) {
	if planFile != "" {
		if _, err := os.Stat(planFile); err != nil {
			return "", fmt.Errorf("plan file not found: %s", planFile)
		}
		return planFile, nil
	}

	// for review-only modes, plan is optional
	if optional {
		return "", nil
	}

	// use fzf to select plan
	return selectPlanWithFzf(ctx, plansDir, colors)
}

func selectPlanWithFzf(ctx context.Context, plansDir string, colors *progress.Colors) (string, error) {
	if _, err := os.Stat(plansDir); err != nil {
		return "", fmt.Errorf("plans directory not found: %s", plansDir)
	}

	// find plan files (excluding completed/)
	plans, err := filepath.Glob(filepath.Join(plansDir, "*.md"))
	if err != nil || len(plans) == 0 {
		return "", fmt.Errorf("no plans found in %s", plansDir)
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

func createBranchIfNeeded(gitOps *git.Repo, planFile string, colors *progress.Colors) error {
	// get current branch
	currentBranch, err := gitOps.CurrentBranch()
	if err != nil {
		return fmt.Errorf("get current branch: %w", err)
	}

	if currentBranch != "main" && currentBranch != "master" {
		return nil // already on feature branch
	}

	// extract branch name from filename
	name := strings.TrimSuffix(filepath.Base(planFile), ".md")
	// remove date prefix like "2024-01-15-"
	re := regexp.MustCompile(`^[\d-]+`)
	branchName := strings.TrimLeft(re.ReplaceAllString(name, ""), "-")
	if branchName == "" {
		branchName = name
	}

	// check if branch already exists
	if gitOps.BranchExists(branchName) {
		colors.Info().Printf("switching to existing branch: %s\n", branchName)
		if err := gitOps.CheckoutBranch(branchName); err != nil {
			return fmt.Errorf("checkout branch %s: %w", branchName, err)
		}
		return nil
	}

	colors.Info().Printf("creating branch: %s\n", branchName)
	if err := gitOps.CreateBranch(branchName); err != nil {
		return fmt.Errorf("create branch %s: %w", branchName, err)
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

	if _, err := f.WriteString("\n# ralphex progress logs\nprogress-*.txt\n"); err != nil {
		f.Close()
		return fmt.Errorf("write .gitignore: %w", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("close .gitignore: %w", err)
	}

	colors.Info().Println("added progress-*.txt to .gitignore")
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

func printStartupInfo(planFile, branch string, mode processor.Mode, maxIterations int, progressPath string, colors *progress.Colors) {
	planStr := planFile
	if planStr == "" {
		planStr = "(no plan - review only)"
	}
	modeStr := ""
	if mode != processor.ModeFull {
		modeStr = fmt.Sprintf(" (%s mode)", mode)
	}
	colors.Info().Printf("starting ralphex loop: %s (max %d iterations)%s\n", planStr, maxIterations, modeStr)
	colors.Info().Printf("branch: %s\n", branch)
	colors.Info().Printf("progress log: %s\n\n", progressPath)
}
