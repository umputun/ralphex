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

	PlanFile string `positional-arg-name:"plan-file" description:"path to plan file (optional, uses fzf if omitted)"`
}

var revision = "unknown"

const plansDir = "docs/plans"

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
	// check dependencies
	for _, dep := range []string{"claude", "git"} {
		if _, err := exec.LookPath(dep); err != nil {
			return fmt.Errorf("%s not found in PATH", dep)
		}
	}

	// select plan file
	planFile, err := selectPlan(ctx, o.PlanFile, o.Review || o.CodexOnly)
	if err != nil {
		return err
	}

	skipTasks := o.Review || o.CodexOnly
	if planFile == "" && !skipTasks {
		return errors.New("plan file required for task execution")
	}

	// create branch if on main/master
	if planFile != "" {
		if branchErr := createBranchIfNeeded(ctx, planFile); branchErr != nil {
			return branchErr
		}
	}

	// ensure progress files are gitignored
	if gitErr := ensureGitignore(ctx); gitErr != nil {
		return gitErr
	}

	// determine mode
	mode := processor.ModeFull
	if o.CodexOnly {
		mode = processor.ModeCodexOnly
	} else if o.Review {
		mode = processor.ModeReview
	}

	// get current branch for logging
	out, _ := exec.CommandContext(ctx, "git", "branch", "--show-current").Output()
	branch := strings.TrimSpace(string(out))

	// create progress logger
	log, err := progress.NewLogger(progress.Config{
		PlanFile: planFile,
		Mode:     string(mode),
		Branch:   branch,
		NoColor:  o.NoColor,
	})
	if err != nil {
		return fmt.Errorf("create progress logger: %w", err)
	}
	defer log.Close()

	// print startup info
	planStr := planFile
	if planStr == "" {
		planStr = "(no plan - review only)"
	}
	modeStr := ""
	if mode != processor.ModeFull {
		modeStr = fmt.Sprintf(" (%s mode)", mode)
	}
	fmt.Printf("starting ralphex loop: %s (max %d iterations)%s\n", planStr, o.MaxIterations, modeStr)
	fmt.Printf("branch: %s\n", branch)
	fmt.Printf("progress log: %s\n\n", log.Path())

	// create and run the runner
	r := processor.New(processor.Config{
		PlanFile:      planFile,
		Mode:          mode,
		MaxIterations: o.MaxIterations,
		Debug:         o.Debug,
		NoColor:       o.NoColor,
	}, log)

	if err := r.Run(ctx); err != nil {
		return fmt.Errorf("runner: %w", err)
	}

	// move completed plan to completed/ directory
	if planFile != "" && mode == processor.ModeFull {
		if moveErr := movePlanToCompleted(ctx, planFile); moveErr != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to move plan to completed: %v\n", moveErr)
		}
	}

	fmt.Printf("\ncompleted in %s\n", log.Elapsed())
	return nil
}

func selectPlan(ctx context.Context, planFile string, optional bool) (string, error) {
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
	return selectPlanWithFzf(ctx)
}

func selectPlanWithFzf(ctx context.Context) (string, error) {
	if _, err := os.Stat(plansDir); err != nil {
		return "", fmt.Errorf("plans directory not found: %s", plansDir)
	}

	if _, err := exec.LookPath("fzf"); err != nil {
		return "", errors.New("fzf not found, please provide plan file as argument")
	}

	// find plan files (excluding completed/)
	plans, err := filepath.Glob(filepath.Join(plansDir, "*.md"))
	if err != nil || len(plans) == 0 {
		return "", fmt.Errorf("no plans found in %s", plansDir)
	}

	// auto-select if single plan
	if len(plans) == 1 {
		fmt.Printf("auto-selected: %s\n", plans[0])
		return plans[0], nil
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

func createBranchIfNeeded(ctx context.Context, planFile string) error {
	// get current branch
	out, err := exec.CommandContext(ctx, "git", "branch", "--show-current").Output()
	if err != nil {
		return fmt.Errorf("failed to get current branch: %w", err)
	}

	currentBranch := strings.TrimSpace(string(out))
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

	fmt.Printf("creating branch: %s\n", branchName)
	if err := exec.CommandContext(ctx, "git", "checkout", "-b", branchName).Run(); err != nil { //nolint:gosec // branch name from plan filename
		return fmt.Errorf("failed to create branch %s: %w", branchName, err)
	}

	return nil
}

func movePlanToCompleted(ctx context.Context, planFile string) error {
	// create completed directory
	completedDir := filepath.Join(filepath.Dir(planFile), "completed")
	if err := os.MkdirAll(completedDir, 0o750); err != nil {
		return fmt.Errorf("create completed dir: %w", err)
	}

	// destination path
	destPath := filepath.Join(completedDir, filepath.Base(planFile))

	// use git mv if in a git repo, otherwise regular move
	if err := exec.CommandContext(ctx, "git", "mv", planFile, destPath).Run(); err != nil { //nolint:gosec // paths from CLI
		// fallback to regular move
		if renameErr := os.Rename(planFile, destPath); renameErr != nil {
			return fmt.Errorf("move plan: %w", renameErr)
		}
		// add to git if possible
		_ = exec.CommandContext(ctx, "git", "add", destPath).Run() //nolint:gosec // destPath derived from planFile
	}

	// commit the move
	commitMsg := "move completed plan: " + filepath.Base(planFile)
	if err := exec.CommandContext(ctx, "git", "commit", "-m", commitMsg).Run(); err != nil { //nolint:gosec // msg from filename
		return fmt.Errorf("commit plan move: %w", err)
	}

	fmt.Printf("moved plan to %s\n", destPath)
	return nil
}

func ensureGitignore(ctx context.Context) error {
	// check if already ignored
	if err := exec.CommandContext(ctx, "git", "check-ignore", "-q", "progress-test.txt").Run(); err == nil {
		return nil // already ignored
	}

	// add to .gitignore
	f, err := os.OpenFile(".gitignore", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) //nolint:gosec // .gitignore needs world-readable
	if err != nil {
		return fmt.Errorf("failed to open .gitignore: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString("\n# ralphex progress logs\nprogress-*.txt\n"); err != nil {
		return fmt.Errorf("failed to write .gitignore: %w", err)
	}

	fmt.Println("added progress-*.txt to .gitignore")
	return nil
}
