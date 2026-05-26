// Package processor provides the main orchestration loop for ralphex execution.
package processor

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/umputun/ralphex/pkg/config"
	"github.com/umputun/ralphex/pkg/executor"
	"github.com/umputun/ralphex/pkg/processor/phase"
	"github.com/umputun/ralphex/pkg/status"
)

// DefaultIterationDelay is the pause between iterations to allow system to settle.
const DefaultIterationDelay = 2 * time.Second

// Mode represents the execution mode.
type Mode string

const (
	ModeFull      Mode = "full"       // full execution: tasks + reviews + codex
	ModeReview    Mode = "review"     // skip tasks, run full review pipeline
	ModeCodexOnly Mode = "codex-only" // skip tasks and first review, run only codex loop
	ModeTasksOnly Mode = "tasks-only" // run only task phase, skip all reviews
	ModePlan      Mode = "plan"       // interactive plan creation mode
)

// Config holds runner configuration.
type Config struct {
	PlanFile              string         // path to plan file (required for full mode)
	PlanDescription       string         // plan description for interactive plan creation mode
	ProgressPath          string         // path to progress file
	Mode                  Mode           // execution mode
	MaxIterations         int            // maximum iterations for task phase
	MaxExternalIterations int            // override external review iteration limit (0 = auto)
	ReviewPatience        int            // terminate external review after N unchanged rounds (0 = disabled)
	Debug                 bool           // enable debug output
	NoColor               bool           // disable color output
	IterationDelayMs      int            // delay between iterations in milliseconds
	TaskRetryCount        int            // number of times to retry failed tasks
	TaskModel             string         // model[:effort] spec for task execution; parsed via ParseModelEffort (empty = CLI defaults)
	ReviewModel           string         // model[:effort] spec for review phases; empty falls back to TaskModel
	CodexEnabled          bool           // whether codex review is enabled
	ExternalReviewToolSet bool           // when true, AppConfig.ExternalReviewTool is an explicit choice that overrides codex_enabled=false back-compat
	FinalizeEnabled       bool           // whether finalize step is enabled
	DefaultBranch         string         // default branch name (detected from repo)
	AppConfig             *config.Config // full application config (for executors and prompts)
}

// isCodexExecutor reports whether the configured task/review executor is codex
// (the --codex first-class mode). returns false when AppConfig is nil or the
// executor is anything else (claude is the default).
func (c Config) isCodexExecutor() bool {
	return c.AppConfig != nil && c.AppConfig.Executor == config.ExecutorCodex
}

func toPhaseConfig(c Config) phase.Config {
	return phase.Config{
		PlanDescription:       c.PlanDescription,
		MaxIterations:         c.MaxIterations,
		MaxExternalIterations: c.MaxExternalIterations,
		ReviewPatience:        c.ReviewPatience,
		CodexEnabled:          c.CodexEnabled,
		ExternalReviewToolSet: c.ExternalReviewToolSet,
		FinalizeEnabled:       c.FinalizeEnabled,
		AppConfig:             c.AppConfig,
	}
}

//go:generate moq -out mocks/executor.go -pkg mocks -skip-ensure -fmt goimports . Executor
//go:generate moq -out mocks/logger.go -pkg mocks -skip-ensure -fmt goimports . Logger
//go:generate moq -out mocks/input_collector.go -pkg mocks -skip-ensure -fmt goimports . InputCollector
//go:generate moq -out mocks/git_checker.go -pkg mocks -skip-ensure -fmt goimports . GitChecker

// Executor runs CLI commands and returns results.
type Executor interface {
	Run(ctx context.Context, prompt string) executor.Result
}

// Logger provides logging functionality.
type Logger interface {
	Print(format string, args ...any)
	PrintRaw(format string, args ...any)
	PrintSection(section status.Section)
	PrintAligned(text string)
	LogQuestion(question string, options []string)
	LogAnswer(answer string)
	LogDraftReview(action string, feedback string)
	Path() string
}

// InputCollector provides interactive input collection for plan creation.
type InputCollector interface {
	AskQuestion(ctx context.Context, question string, options []string) (string, error)
	AskDraftReview(ctx context.Context, question string, planContent string) (action string, feedback string, err error)
}

// GitChecker provides git state inspection for the review loop.
type GitChecker interface {
	HeadHash() (string, error)
	DiffFingerprint() (string, error)
}

// Executors groups the executor dependencies for the Runner.
// Role-named: Task is used for the task phase, Review for review phases (nil = use Task),
// External for the external review phase (nil = no external review), Custom is the
// custom external review script executor.
type Executors struct {
	Task     Executor
	Review   Executor // optional: separate executor for review phases (nil = use Task)
	External Executor // external review executor (codex or wrapper); nil when Executor=codex or external review disabled
	Custom   *executor.CustomExecutor
}

// Runner orchestrates the execution loop.
type Runner struct {
	cfg         Config
	log         Logger
	phaseHolder *status.PhaseHolder
	deps        *phase.Deps
	phases      runnerPhases
}

type taskPhaseRunner interface {
	Run(ctx context.Context) error
}

type taskPlanValidator interface {
	ValidatePlanHasTasks() error
}

type reviewPhaseRunner interface {
	First(ctx context.Context) error
	Loop(ctx context.Context, prefix string) error
}

type externalReviewPhaseRunner interface {
	Tool() string
	Run(ctx context.Context) (phase.ExternalReviewOutcome, error)
}

type finalizePhaseRunner interface {
	Run(ctx context.Context) error
}

type planCreationPhaseRunner interface {
	Run(ctx context.Context) error
}

type runnerPhases struct {
	task          taskPhaseRunner
	taskValidator taskPlanValidator
	review        reviewPhaseRunner
	external      externalReviewPhaseRunner
	finalize      finalizePhaseRunner
	planCreation  planCreationPhaseRunner
}

// New creates a new Runner with the given configuration and shared phase holder.
// If codex is enabled but the binary is not found in PATH, it is automatically disabled with a warning.
func New(cfg Config, log Logger, holder *status.PhaseHolder) *Runner {
	factory := &executorFactory{}
	resolvedCfg, execs := factory.Build(cfg, log)
	return NewWithExecutors(resolvedCfg, log, execs, holder)
}

// NewWithExecutors creates a new Runner with custom executors (for testing).
func NewWithExecutors(cfg Config, log Logger, execs Executors, holder *status.PhaseHolder) *Runner {
	if holder == nil {
		holder = &status.PhaseHolder{}
	}

	// determine iteration delay from config or default
	iterDelay := DefaultIterationDelay
	if cfg.IterationDelayMs > 0 {
		iterDelay = time.Duration(cfg.IterationDelayMs) * time.Millisecond
	}

	// determine task retry count from config
	// appConfig.TaskRetryCountSet means user explicitly set it (even to 0 for no retries)
	retryCount := 1
	if cfg.AppConfig != nil && cfg.AppConfig.TaskRetryCountSet {
		retryCount = cfg.TaskRetryCount
	} else if cfg.TaskRetryCount > 0 {
		retryCount = cfg.TaskRetryCount
	}

	// determine wait-on-limit duration from config
	var waitOnLimit time.Duration
	if cfg.AppConfig != nil {
		waitOnLimit = cfg.AppConfig.WaitOnLimit
	}

	// if no separate review executor, use the same as task executor
	review := execs.Review
	if review == nil {
		review = execs.Task
	}

	locator := newPlanLocator(cfg)
	policy := newExecutionPolicy(executionPolicyOpts{cfg: cfg, log: log, waitOnLimit: waitOnLimit})
	prompts := newPromptBuilder(promptBuilderOpts{cfg: cfg, log: log, locator: locator})
	phaseCfg := toPhaseConfig(cfg)
	deps := &phase.Deps{}
	breaks := phase.NewBreakController(deps)
	git := phase.NewGitState(deps, log)
	taskPhase := phase.NewTaskPhase(phase.TaskPhaseOpts{
		Cfg: phaseCfg, Log: log, Exec: execs.Task, Policy: policy, Prompts: prompts,
		Locator: locator, Deps: deps, Breaks: breaks, IterationDelay: iterDelay, RetryCount: retryCount,
	})
	reviewPhase := phase.NewReviewPhase(phase.ReviewPhaseOpts{
		Cfg: phaseCfg, Log: log, Exec: review, Policy: policy, Prompts: prompts,
		Git: git, PhaseHolder: holder, IterationDelay: iterDelay,
	})
	externalPhase := phase.NewExternalReviewPhase(phase.ExternalReviewPhaseOpts{
		Cfg: phaseCfg, Log: log, External: execs.External, Custom: execs.Custom, Review: review,
		Policy: policy, Prompts: prompts, Breaks: breaks, Git: git, PhaseHolder: holder, IterationDelay: iterDelay,
	})
	finalizePhase := phase.NewFinalizePhase(phase.FinalizePhaseOpts{
		Cfg: phaseCfg, Log: log, Exec: review, Policy: policy, Prompts: prompts, PhaseHolder: holder,
	})
	planCreationPhase := phase.NewPlanCreationPhase(phase.PlanCreationPhaseOpts{
		Cfg: phaseCfg, Log: log, Exec: execs.Task, Policy: policy, Prompts: prompts,
		Deps: deps, PhaseHolder: holder, IterationDelay: iterDelay,
	})
	phases := runnerPhases{
		task: taskPhase, taskValidator: taskPhase, review: reviewPhase,
		external: externalPhase, finalize: finalizePhase, planCreation: planCreationPhase,
	}

	return &Runner{
		cfg:         cfg,
		log:         log,
		phaseHolder: holder,
		deps:        deps,
		phases:      phases,
	}
}

// SetInputCollector sets the input collector for plan creation mode.
func (r *Runner) SetInputCollector(c InputCollector) {
	if r.deps == nil {
		r.deps = &phase.Deps{}
	}
	r.deps.InputCollector = c
}

// SetGitChecker sets the git checker for no-commit detection in review loops.
func (r *Runner) SetGitChecker(g GitChecker) {
	if r.deps == nil {
		r.deps = &phase.Deps{}
	}
	r.deps.Git = g
}

// SetBreakCh sets the break channel for manual termination of review and task loops.
// each value sent on the channel triggers one break event (repeatable, not close-based).
func (r *Runner) SetBreakCh(ch <-chan struct{}) {
	if r.deps == nil {
		r.deps = &phase.Deps{}
	}
	r.deps.BreakCh = ch
}

// SetPauseHandler sets the callback invoked when a break signal is received during task iteration.
// the handler should prompt the user and return true to resume or false to abort.
// if nil, break during task phase returns ErrUserAborted immediately.
func (r *Runner) SetPauseHandler(fn func(ctx context.Context) bool) {
	if r.deps == nil {
		r.deps = &phase.Deps{}
	}
	r.deps.PauseHandler = fn
}

// Run executes the main loop based on configured mode.
func (r *Runner) Run(ctx context.Context) error {
	switch r.cfg.Mode {
	case ModeFull:
		return r.runFull(ctx)
	case ModeReview:
		return r.runReviewOnly(ctx)
	case ModeCodexOnly:
		return r.runCodexOnly(ctx)
	case ModeTasksOnly:
		return r.runTasksOnly(ctx)
	case ModePlan:
		if err := r.phases.planCreation.Run(ctx); err != nil {
			if errors.Is(err, ErrUserRejectedPlan) {
				return ErrUserRejectedPlan
			}
			return fmt.Errorf("plan creation phase: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unknown mode: %s", r.cfg.Mode)
	}
}

// runFull executes the complete pipeline: tasks → review → codex → review.
func (r *Runner) runFull(ctx context.Context) error {
	if r.cfg.PlanFile == "" {
		return errors.New("plan file required for full mode")
	}
	if err := r.phases.taskValidator.ValidatePlanHasTasks(); err != nil {
		return fmt.Errorf("validate task plan: %w", err)
	}

	// phase 1: task execution
	r.phaseHolder.Set(status.PhaseTask)
	r.log.PrintRaw("starting task execution phase\n")

	if err := r.phases.task.Run(ctx); err != nil {
		if errors.Is(err, ErrUserAborted) {
			r.log.Print("task phase aborted by user")
			return ErrUserAborted
		}
		return fmt.Errorf("task phase: %w", err)
	}

	// phase 2: first review pass - address ALL findings
	if err := r.phases.review.First(ctx); err != nil {
		return fmt.Errorf("first review: %w", err)
	}

	// phase 2.1: review loop (critical/major) before external review
	if err := r.phases.review.Loop(ctx, ""); err != nil {
		return fmt.Errorf("pre-external review loop: %w", err)
	}

	// phase 2.5+3: external review → post-external review → finalize
	if err := r.runExternalAndPostReview(ctx); err != nil {
		return err
	}

	r.log.Print("all phases completed successfully")
	return nil
}

// runReviewOnly executes only the review pipeline: review → external review → review.
func (r *Runner) runReviewOnly(ctx context.Context) error {
	// phase 1: first review
	if err := r.phases.review.First(ctx); err != nil {
		return fmt.Errorf("first review: %w", err)
	}

	// phase 1.1: review loop (critical/major) before external review
	if err := r.phases.review.Loop(ctx, ""); err != nil {
		return fmt.Errorf("pre-external review loop: %w", err)
	}

	// phase 2+3: external review → post-external review → finalize
	if err := r.runExternalAndPostReview(ctx); err != nil {
		return err
	}

	r.log.Print("review phases completed successfully")
	return nil
}

// runCodexOnly executes only the external-review pipeline: external review → review → finalize.
func (r *Runner) runCodexOnly(ctx context.Context) error {
	if err := r.runExternalAndPostReview(ctx); err != nil {
		return err
	}

	r.log.Print("codex phases completed successfully")
	return nil
}

// runExternalAndPostReview runs the shared external-review → post-review → finalize pipeline.
// used by runFull, runReviewOnly, and runCodexOnly to avoid duplicating this sequence.
func (r *Runner) runExternalAndPostReview(ctx context.Context) error {
	tool := r.phases.external.Tool()
	if tool == "none" {
		r.log.Print("external review disabled, skipping...")
		if err := r.phases.finalize.Run(ctx); err != nil {
			return fmt.Errorf("finalize phase: %w", err)
		}
		return nil
	}

	r.phaseHolder.Set(status.PhaseCodex)
	r.log.PrintSection(status.NewGenericSection(tool + " external review"))

	outcome, err := r.phases.external.Run(ctx)
	if err != nil {
		return fmt.Errorf("%s loop: %w", tool, err)
	}

	if !outcome.HadFindings {
		r.log.Print("external review found no issues, skipping post-%s claude review", tool)
		if err := r.phases.finalize.Run(ctx); err != nil {
			return fmt.Errorf("finalize phase: %w", err)
		}
		return nil
	}

	r.phaseHolder.Set(status.PhaseReview)

	commitPrefix := "IMPORTANT: Before starting the review, run `git status`. " +
		"If there are uncommitted changes from previous review phases, " +
		"stage and commit them with message: " +
		"`fix: address code review findings`\n" +
		"Then continue with the sequence below.\n\n"
	if err := r.phases.review.Loop(ctx, commitPrefix); err != nil {
		return fmt.Errorf("post-external review loop: %w", err)
	}

	if err := r.phases.finalize.Run(ctx); err != nil {
		return fmt.Errorf("finalize phase: %w", err)
	}
	return nil
}

// runTasksOnly executes only task phase, skipping all reviews.
func (r *Runner) runTasksOnly(ctx context.Context) error {
	if r.cfg.PlanFile == "" {
		return errors.New("plan file required for tasks-only mode")
	}
	if err := r.phases.taskValidator.ValidatePlanHasTasks(); err != nil {
		return fmt.Errorf("validate task plan: %w", err)
	}

	r.phaseHolder.Set(status.PhaseTask)
	r.log.PrintRaw("starting task execution phase\n")

	if err := r.phases.task.Run(ctx); err != nil {
		if errors.Is(err, ErrUserAborted) {
			r.log.Print("task phase aborted by user")
			return ErrUserAborted
		}
		return fmt.Errorf("task phase: %w", err)
	}

	r.log.Print("task execution completed successfully")
	return nil
}

// ErrUserAborted is a sentinel error returned when the user aborts or declines to resume after a break
// signal (Ctrl+\). it is propagated as a non-nil error so that callers (including mode entrypoints) can
// detect it and treat it as a clean user-initiated exit, avoiding further review/finalize steps.
var ErrUserAborted = phase.ErrUserAborted

// ErrUserRejectedPlan is returned when user rejects the plan draft.
var ErrUserRejectedPlan = phase.ErrUserRejectedPlan
