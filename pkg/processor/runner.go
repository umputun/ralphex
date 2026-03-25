// Package processor provides the main orchestration loop for ralphex execution.
package processor

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/umputun/ralphex/pkg/config"
	"github.com/umputun/ralphex/pkg/executor"
	"github.com/umputun/ralphex/pkg/plan"
	"github.com/umputun/ralphex/pkg/status"
)

// DefaultIterationDelay is the pause between iterations to allow system to settle.
const DefaultIterationDelay = 2 * time.Second

const (
	minReviewIterations    = 3    // minimum claude review iterations
	reviewIterationDivisor = 10   // review iterations = max_iterations / divisor
	minCodexIterations     = 3    // minimum codex review iterations
	codexIterationDivisor  = 5    // codex iterations = max_iterations / divisor
	minPlanIterations      = 5    // minimum plan creation iterations
	planIterationDivisor   = 5    // plan iterations = max_iterations / divisor
	maxCodexSummaryLen     = 5000 // max chars for codex output summary
)

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
	CodexEnabled          bool           // whether codex review is enabled
	FinalizeEnabled       bool           // whether finalize step is enabled
	DefaultBranch         string         // default branch name (detected from repo)
	AppConfig             *config.Config // full application config (for executors and prompts)
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
type Executors struct {
	Claude Executor
	Codex  Executor
	Custom *executor.CustomExecutor
}

// Runner orchestrates the execution loop.
type Runner struct {
	cfg                 Config
	log                 Logger
	claude              Executor
	codex               Executor
	custom              *executor.CustomExecutor
	git                 GitChecker
	inputCollector      InputCollector
	phaseHolder         *status.PhaseHolder
	iterationDelay      time.Duration
	taskRetryCount      int
	waitOnLimit         time.Duration
	breakCh             <-chan struct{}                 // nil = feature disabled; receives one value per break signal
	pauseHandler        func(ctx context.Context) bool  // called on break during task phase; true = resume, false = abort
	lastSessionTimedOut bool                            // set by runWithSessionTimeout, checked by review loops
	taskPhaseOverride   func(ctx context.Context) error // test seam: override runTaskPhase result (nil = normal execution)
}

// New creates a new Runner with the given configuration and shared phase holder.
// If codex is enabled but the binary is not found in PATH, it is automatically disabled with a warning.
func New(cfg Config, log Logger, holder *status.PhaseHolder) *Runner {
	// build claude executor with config values
	claudeExec := &executor.ClaudeExecutor{
		OutputHandler: func(text string) {
			log.PrintAligned(text)
		},
		Debug: cfg.Debug,
	}
	if cfg.AppConfig != nil {
		claudeExec.Command = cfg.AppConfig.ClaudeCommand
		claudeExec.Args = cfg.AppConfig.ClaudeArgs
		claudeExec.ErrorPatterns = cfg.AppConfig.ClaudeErrorPatterns
		claudeExec.LimitPatterns = cfg.AppConfig.ClaudeLimitPatterns
	}

	// build codex executor with config values
	codexExec := &executor.CodexExecutor{
		OutputHandler: func(text string) {
			log.PrintAligned(text)
		},
		Debug: cfg.Debug,
	}
	if cfg.AppConfig != nil {
		codexExec.Command = cfg.AppConfig.CodexCommand
		codexExec.Model = cfg.AppConfig.CodexModel
		codexExec.ReasoningEffort = cfg.AppConfig.CodexReasoningEffort
		codexExec.TimeoutMs = cfg.AppConfig.CodexTimeoutMs
		codexExec.Sandbox = cfg.AppConfig.CodexSandbox
		codexExec.ErrorPatterns = cfg.AppConfig.CodexErrorPatterns
		codexExec.LimitPatterns = cfg.AppConfig.CodexLimitPatterns
	}

	// build custom executor if custom review script is configured
	var customExec *executor.CustomExecutor
	if cfg.AppConfig != nil && cfg.AppConfig.CustomReviewScript != "" {
		customExec = &executor.CustomExecutor{
			Script: cfg.AppConfig.CustomReviewScript,
			OutputHandler: func(text string) {
				log.PrintAligned(text)
			},
			ErrorPatterns: cfg.AppConfig.CodexErrorPatterns, // reuse codex error patterns
			LimitPatterns: cfg.AppConfig.CodexLimitPatterns, // reuse codex limit patterns
		}
	}

	// auto-disable codex if the binary is not installed AND we need codex
	// (skip this check if using custom external review tool or external review is disabled)
	if cfg.CodexEnabled && needsCodexBinary(cfg.AppConfig) {
		codexCmd := codexExec.Command
		if codexCmd == "" {
			codexCmd = "codex"
		}
		if _, err := exec.LookPath(codexCmd); err != nil {
			log.Print("warning: codex not found (%s: %v), disabling codex review phase", codexCmd, err)
			cfg.CodexEnabled = false
		}
	}

	return NewWithExecutors(cfg, log, Executors{Claude: claudeExec, Codex: codexExec, Custom: customExec}, holder)
}

// NewWithExecutors creates a new Runner with custom executors (for testing).
func NewWithExecutors(cfg Config, log Logger, execs Executors, holder *status.PhaseHolder) *Runner {
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

	return &Runner{
		cfg:            cfg,
		log:            log,
		claude:         execs.Claude,
		codex:          execs.Codex,
		custom:         execs.Custom,
		phaseHolder:    holder,
		iterationDelay: iterDelay,
		taskRetryCount: retryCount,
		waitOnLimit:    waitOnLimit,
	}
}

// SetInputCollector sets the input collector for plan creation mode.
func (r *Runner) SetInputCollector(c InputCollector) {
	r.inputCollector = c
}

// SetGitChecker sets the git checker for no-commit detection in review loops.
func (r *Runner) SetGitChecker(g GitChecker) {
	r.git = g
}

// SetBreakCh sets the break channel for manual termination of review and task loops.
// each value sent on the channel triggers one break event (repeatable, not close-based).
func (r *Runner) SetBreakCh(ch <-chan struct{}) {
	r.breakCh = ch
}

// SetPauseHandler sets the callback invoked when a break signal is received during task iteration.
// the handler should prompt the user and return true to resume or false to abort.
// if nil, break during task phase returns ErrUserAborted immediately.
func (r *Runner) SetPauseHandler(fn func(ctx context.Context) bool) {
	r.pauseHandler = fn
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
		return r.runPlanCreation(ctx)
	default:
		return fmt.Errorf("unknown mode: %s", r.cfg.Mode)
	}
}

// runFull executes the complete pipeline: tasks → review → codex → review.
func (r *Runner) runFull(ctx context.Context) error {
	if r.cfg.PlanFile == "" {
		return errors.New("plan file required for full mode")
	}

	// phase 1: task execution
	r.phaseHolder.Set(status.PhaseTask)
	r.log.PrintRaw("starting task execution phase\n")

	if err := r.runTaskPhase(ctx); err != nil {
		if errors.Is(err, ErrUserAborted) {
			r.log.Print("task phase aborted by user")
			return ErrUserAborted
		}
		return fmt.Errorf("task phase: %w", err)
	}

	// phase 2: first review pass - address ALL findings
	r.phaseHolder.Set(status.PhaseReview)
	r.log.PrintSection(status.NewGenericSection("claude review 0: all findings"))

	if err := r.runClaudeReview(ctx, r.replacePromptVariables(r.cfg.AppConfig.ReviewFirstPrompt)); err != nil {
		return fmt.Errorf("first review: %w", err)
	}

	// phase 2.1: claude review loop (critical/major) before codex
	if err := r.runClaudeReviewLoop(ctx); err != nil {
		return fmt.Errorf("pre-codex review loop: %w", err)
	}

	// phase 2.5+3: codex → post-codex review → finalize
	if err := r.runCodexAndPostReview(ctx); err != nil {
		return err
	}

	r.log.Print("all phases completed successfully")
	return nil
}

// runReviewOnly executes only the review pipeline: review → codex → review.
func (r *Runner) runReviewOnly(ctx context.Context) error {
	// phase 1: first review
	r.phaseHolder.Set(status.PhaseReview)
	r.log.PrintSection(status.NewGenericSection("claude review 0: all findings"))

	if err := r.runClaudeReview(ctx, r.replacePromptVariables(r.cfg.AppConfig.ReviewFirstPrompt)); err != nil {
		return fmt.Errorf("first review: %w", err)
	}

	// phase 1.1: claude review loop (critical/major) before codex
	if err := r.runClaudeReviewLoop(ctx); err != nil {
		return fmt.Errorf("pre-codex review loop: %w", err)
	}

	// phase 2+3: codex → post-codex review → finalize
	if err := r.runCodexAndPostReview(ctx); err != nil {
		return err
	}

	r.log.Print("review phases completed successfully")
	return nil
}

// runCodexOnly executes only the codex pipeline: codex → review → finalize.
func (r *Runner) runCodexOnly(ctx context.Context) error {
	if err := r.runCodexAndPostReview(ctx); err != nil {
		return err
	}

	r.log.Print("codex phases completed successfully")
	return nil
}

// runCodexAndPostReview runs the shared codex → post-codex claude review → finalize pipeline.
// used by runFull, runReviewOnly, and runCodexOnly to avoid duplicating this sequence.
func (r *Runner) runCodexAndPostReview(ctx context.Context) error {
	// codex external review loop
	r.phaseHolder.Set(status.PhaseCodex)
	r.log.PrintSection(status.NewGenericSection("codex external review"))

	if err := r.runCodexLoop(ctx); err != nil {
		return fmt.Errorf("codex loop: %w", err)
	}

	// claude review loop (critical/major) after codex.
	// prepend commit-pending instruction only when external review actually ran,
	// because the loop may exit early (max iterations, stalemate, manual break)
	// leaving uncommitted fixes in the worktree.
	r.phaseHolder.Set(status.PhaseReview)

	var commitPrefix string
	if r.externalReviewTool() != "none" {
		commitPrefix = "IMPORTANT: Before starting the review, run `git status`. " +
			"If there are uncommitted changes from previous review phases, " +
			"stage and commit them with message: " +
			"`fix: address code review findings`\n" +
			"Then continue with the sequence below.\n\n"
	}
	if err := r.runClaudeReviewLoop(ctx, commitPrefix); err != nil {
		return fmt.Errorf("post-codex review loop: %w", err)
	}

	// optional finalize step (best-effort, but propagates context cancellation)
	return r.runFinalize(ctx)
}

// runTasksOnly executes only task phase, skipping all reviews.
func (r *Runner) runTasksOnly(ctx context.Context) error {
	if r.cfg.PlanFile == "" {
		return errors.New("plan file required for tasks-only mode")
	}

	r.phaseHolder.Set(status.PhaseTask)
	r.log.PrintRaw("starting task execution phase\n")

	if err := r.runTaskPhase(ctx); err != nil {
		if errors.Is(err, ErrUserAborted) {
			r.log.Print("task phase aborted by user")
			return ErrUserAborted
		}
		return fmt.Errorf("task phase: %w", err)
	}

	r.log.Print("task execution completed successfully")
	return nil
}

// runTaskPhase executes tasks until completion or max iterations.
// executes ONE Task section per iteration. supports break (Ctrl+\) with pause+resume:
// on break, the current session is canceled, pauseHandler is called, and on resume
// the same iteration re-runs with a fresh session that re-reads the plan file.
func (r *Runner) runTaskPhase(ctx context.Context) error {
	if r.taskPhaseOverride != nil {
		return r.taskPhaseOverride(ctx)
	}
	prompt := r.replacePromptVariables(r.cfg.AppConfig.TaskPrompt)
	retryCount := 0

	for i := 1; i <= r.cfg.MaxIterations; i++ {
		select {
		case <-ctx.Done():
			return fmt.Errorf("task phase: %w", ctx.Err())
		default:
		}

		// use plan task position instead of loop counter for correct dashboard highlighting
		taskNum := i
		if pos := r.nextPlanTaskPosition(); pos > 0 {
			taskNum = pos
		}
		r.log.PrintSection(status.NewTaskIterationSection(taskNum))

		// create per-iteration break context so Ctrl+\ cancels only the current session
		loopCtx, loopCancel := r.breakContext(ctx)

		result := r.runWithLimitRetry(loopCtx, r.claude.Run, prompt, "claude")

		// check break before calling loopCancel — cancel would make loopCtx.Err() non-nil
		manualBreak := r.isBreak(loopCtx, ctx)
		loopCancel()

		if manualBreak {
			r.log.Print("session interrupted by break signal")
			r.drainBreakCh() // clear signal that may have arrived during cancellation
			if r.pauseHandler == nil || !r.pauseHandler(ctx) {
				return ErrUserAborted
			}
			// resume: decrement i to preserve iteration budget and re-run same task
			r.drainBreakCh() // clear any signal received during pause prompt
			i--
			retryCount = 0
			continue
		}

		if result.Error != nil {
			if err := r.handlePatternMatchError(result.Error, "claude"); err != nil {
				return err
			}
			return fmt.Errorf("claude execution: %w", result.Error)
		}

		if result.Signal == SignalCompleted {
			// verify plan actually has no uncompleted checkboxes
			if r.hasUncompletedTasks() {
				r.log.Print("warning: completion signal received but plan still has [ ] items, continuing...")
				continue
			}
			r.log.PrintRaw("\nall tasks completed, starting code review...\n")
			return nil
		}

		if result.Signal == SignalFailed {
			if retryCount < r.taskRetryCount {
				r.log.Print("task failed, retrying...")
				retryCount++
				if err := r.sleepWithContext(ctx, r.iterationDelay); err != nil {
					return fmt.Errorf("interrupted: %w", err)
				}
				continue
			}
			return errors.New("task execution failed after retry (FAILED signal received)")
		}

		retryCount = 0
		// continue with same prompt - it reads from plan file each time
		if err := r.sleepWithContext(ctx, r.iterationDelay); err != nil {
			return fmt.Errorf("interrupted: %w", err)
		}
	}

	return fmt.Errorf("max iterations (%d) reached without completion", r.cfg.MaxIterations)
}

// runClaudeReview runs Claude review with the given prompt until REVIEW_DONE.
func (r *Runner) runClaudeReview(ctx context.Context, prompt string) error {
	result := r.runWithLimitRetry(ctx, r.claude.Run, prompt, "claude")
	if result.Error != nil {
		if err := r.handlePatternMatchError(result.Error, "claude"); err != nil {
			return err
		}
		return fmt.Errorf("claude execution: %w", result.Error)
	}

	if result.Signal == SignalFailed {
		return errors.New("review failed (FAILED signal received)")
	}

	if !isReviewDone(result.Signal) {
		r.log.Print("warning: first review pass did not complete cleanly, continuing...")
	}

	return nil
}

// runClaudeReviewLoop runs claude review iterations using second review prompt.
// optional promptPrefix is prepended to the review prompt (used for commit-pending instruction after codex).
func (r *Runner) runClaudeReviewLoop(ctx context.Context, promptPrefix ...string) error {
	// review iterations = 10% of max_iterations
	maxReviewIterations := max(minReviewIterations, r.cfg.MaxIterations/reviewIterationDivisor)

	prefix := ""
	if len(promptPrefix) > 0 {
		prefix = promptPrefix[0]
	}

	for i := 1; i <= maxReviewIterations; i++ {
		select {
		case <-ctx.Done():
			return fmt.Errorf("review: %w", ctx.Err())
		default:
		}

		r.log.PrintSection(status.NewClaudeReviewSection(i, ": critical/major"))

		// capture HEAD hash before running claude for no-commit detection
		headBefore := r.headHash()

		result := r.runWithLimitRetry(ctx, r.claude.Run,
			prefix+r.replacePromptVariables(r.cfg.AppConfig.ReviewSecondPrompt), "claude")
		if result.Error != nil {
			if err := r.handlePatternMatchError(result.Error, "claude"); err != nil {
				return err
			}
			return fmt.Errorf("claude execution: %w", result.Error)
		}

		if result.Signal == SignalFailed {
			return errors.New("review failed (FAILED signal received)")
		}

		if isReviewDone(result.Signal) {
			r.log.Print("claude review complete - no more findings")
			return nil
		}

		// on session timeout, skip HEAD check and retry; the session was killed before
		// it could finish, so "no changes" doesn't mean "nothing to fix"
		if r.lastSessionTimedOut {
			r.log.Print("session timed out, retrying review iteration...")
			continue
		}

		// fallback: if HEAD hash hasn't changed, claude found nothing to fix
		if headBefore != "" {
			if headAfter := r.headHash(); headAfter == headBefore {
				r.log.Print("claude review complete - no changes detected")
				return nil
			}
		}

		r.log.Print("issues fixed, running another review iteration...")
		if err := r.sleepWithContext(ctx, r.iterationDelay); err != nil {
			return fmt.Errorf("interrupted: %w", err)
		}
	}

	r.log.Print("max claude review iterations reached, continuing...")
	return nil
}

// headHash returns the current HEAD commit hash, or empty string if unavailable.
func (r *Runner) headHash() string {
	if r.git == nil {
		return ""
	}
	hash, err := r.git.HeadHash()
	if err != nil {
		r.log.Print("warning: failed to get HEAD hash: %v", err)
		return ""
	}
	return hash
}

// diffFingerprint returns a hash of the current working tree diff, or empty string if unavailable.
func (r *Runner) diffFingerprint() string {
	if r.git == nil {
		return ""
	}
	fp, err := r.git.DiffFingerprint()
	if err != nil {
		r.log.Print("warning: failed to get diff fingerprint: %v", err)
		return ""
	}
	return fp
}

// checkStalemate compares git state before and after claude evaluation to detect unchanged rounds.
// returns the updated unchanged round counter: incremented if no changes detected, reset to 0 otherwise.
// when diff fingerprints are unavailable (error), falls back to HEAD-only comparison.
func (r *Runner) checkStalemate(headBefore, headAfter, diffBefore, diffAfter string, unchangedRounds int) int {
	unchanged := headAfter == headBefore
	if diffBefore != "" && diffAfter != "" {
		unchanged = unchanged && diffAfter == diffBefore
	}
	if unchanged {
		return unchangedRounds + 1
	}
	return 0
}

// updateStalemate checks if review patience is enabled, computes the "after" git state,
// and returns the updated unchanged-rounds counter plus a flag indicating stalemate.
// skips the update if "after" values are empty (transient git error) to avoid resetting the counter.
func (r *Runner) updateStalemate(headBefore, diffBefore string, unchangedRounds int) (int, bool) {
	if r.cfg.ReviewPatience <= 0 || headBefore == "" {
		return unchangedRounds, false
	}
	// skip stalemate update if "after" values are empty (transient git error),
	// so errors don't reset unchangedRounds and inadvertently disable early exit
	if headAfter, diffAfter := r.headHash(), r.diffFingerprint(); headAfter != "" && diffAfter != "" {
		unchangedRounds = r.checkStalemate(headBefore, headAfter, diffBefore, diffAfter, unchangedRounds)
	}
	if unchangedRounds >= r.cfg.ReviewPatience {
		r.log.Print("stalemate detected after %d unchanged rounds, external review terminated early", unchangedRounds)
		return unchangedRounds, true
	}
	return unchangedRounds, false
}

// externalReviewTool returns the effective external review tool to use.
// handles backward compatibility: codex_enabled = false → "none"
// the CodexEnabled flag takes precedence for backward compatibility.
func (r *Runner) externalReviewTool() string {
	// backward compatibility: codex_enabled = false means no external review
	// this takes precedence over external_review_tool setting
	if !r.cfg.CodexEnabled {
		return "none"
	}

	// check explicit external_review_tool setting
	if r.cfg.AppConfig != nil && r.cfg.AppConfig.ExternalReviewTool != "" {
		return r.cfg.AppConfig.ExternalReviewTool
	}

	// default to codex
	return "codex"
}

// runCodexLoop runs the external review loop (codex or custom) until no findings.
func (r *Runner) runCodexLoop(ctx context.Context) error {
	tool := r.externalReviewTool()

	// skip external review phase if disabled
	if tool == "none" {
		r.log.Print("external review disabled, skipping...")
		return nil
	}

	// custom review tool
	if tool == "custom" {
		if r.custom == nil {
			return errors.New("custom review script not configured")
		}
		return r.runExternalReviewLoop(ctx, externalReviewConfig{
			name:            "custom",
			runReview:       func(ctx context.Context, prompt string) executor.Result { return r.custom.Run(ctx, prompt) },
			buildPrompt:     r.buildCustomReviewPrompt,
			buildEvalPrompt: r.buildCustomEvaluationPrompt,
			showSummary:     func(string) {}, // no-op: custom output already streamed via OutputHandler
			makeSection:     status.NewCustomIterationSection,
		})
	}

	// default: codex review
	return r.runExternalReviewLoop(ctx, externalReviewConfig{
		name:            "codex",
		runReview:       r.codex.Run,
		buildPrompt:     r.buildCodexPrompt,
		buildEvalPrompt: r.buildCodexEvaluationPrompt,
		showSummary:     r.showCodexSummary,
		makeSection:     status.NewCodexIterationSection,
	})
}

// externalReviewConfig holds callbacks for running an external review tool.
type externalReviewConfig struct {
	name            string                                                   // tool name for error messages
	runReview       func(ctx context.Context, prompt string) executor.Result // run the external review tool
	buildPrompt     func(isFirst bool, claudeResponse string) string         // build prompt for review tool
	buildEvalPrompt func(output string) string                               // build evaluation prompt for claude
	showSummary     func(output string)                                      // display review findings summary
	makeSection     func(iteration int) status.Section                       // create section header
}

// runExternalReviewLoop runs a generic external review tool-claude loop.
// it terminates when no findings remain, max iterations are reached,
// stalemate is detected (review patience), or a manual break is requested.
func (r *Runner) runExternalReviewLoop(ctx context.Context, cfg externalReviewConfig) error {
	maxIterations := max(minCodexIterations, r.cfg.MaxIterations/codexIterationDivisor)
	if r.cfg.MaxExternalIterations > 0 {
		maxIterations = r.cfg.MaxExternalIterations
	}

	// derive a child context that cancels when break channel fires
	loopCtx, loopCancel := r.breakContext(ctx)
	defer loopCancel()

	var claudeResponse string // first iteration has no prior response
	var unchangedRounds int   // consecutive iterations with no commits (for stalemate detection)
	firstCompleted := false   // tracks if any successful eval completed; controls diff scope for external tool

	for i := 1; i <= maxIterations; i++ {
		select {
		case <-loopCtx.Done():
			if r.isBreak(loopCtx, ctx) {
				r.log.Print("manual break requested, external review terminated early")
				return nil
			}
			return fmt.Errorf("%s loop: %w", cfg.name, ctx.Err())
		default:
		}

		r.log.PrintSection(cfg.makeSection(i))

		// run external review tool. use branch-wide diff until a successful claude eval completes,
		// so that a timeout on the first eval doesn't narrow subsequent reviews to working-tree only
		reviewResult := r.runWithLimitRetry(loopCtx, cfg.runReview, cfg.buildPrompt(!firstCompleted, claudeResponse), cfg.name)
		if reviewResult.Error != nil {
			if r.isBreak(loopCtx, ctx) {
				r.log.Print("manual break requested, external review terminated early")
				return nil
			}
			if err := r.handlePatternMatchError(reviewResult.Error, cfg.name); err != nil {
				return err
			}
			return fmt.Errorf("%s execution: %w", cfg.name, reviewResult.Error)
		}

		if reviewResult.Output == "" {
			r.log.Print("%s review returned no output, skipping...", cfg.name)
			break
		}

		// show findings summary before Claude evaluation
		cfg.showSummary(reviewResult.Output)

		// capture state before claude evaluation for stalemate detection (only when enabled)
		var headBefore, diffBefore string
		if r.cfg.ReviewPatience > 0 {
			headBefore = r.headHash()
			diffBefore = r.diffFingerprint()
		}

		// pass output to claude for evaluation and fixing
		r.phaseHolder.Set(status.PhaseClaudeEval)
		r.log.PrintSection(status.NewClaudeEvalSection())
		claudeResult := r.runWithLimitRetry(loopCtx, r.claude.Run, cfg.buildEvalPrompt(reviewResult.Output), "claude")

		// restore codex phase for next iteration
		r.phaseHolder.Set(status.PhaseCodex)
		if claudeResult.Error != nil {
			if r.isBreak(loopCtx, ctx) {
				r.log.Print("manual break requested, external review terminated early")
				return nil
			}
			if err := r.handlePatternMatchError(claudeResult.Error, "claude"); err != nil {
				return err
			}
			return fmt.Errorf("claude execution: %w", claudeResult.Error)
		}

		// on session timeout, skip response capture and stalemate detection; the session was killed
		// before it could finish, so partial output can't be trusted as previous context and
		// "no changes" doesn't mean "nothing to fix"
		if r.lastSessionTimedOut {
			r.log.Print("claude eval session timed out, retrying %s iteration...", cfg.name)
			continue
		}

		firstCompleted = true // successful eval completed, next iteration can use working-tree diff
		claudeResponse = claudeResult.Output

		// exit only when claude sees "no findings"
		if isCodexDone(claudeResult.Signal) {
			r.log.Print("%s review complete - no more findings", cfg.name)
			return nil
		}

		// stalemate detection: track consecutive rounds with no changes (commits or working tree edits).
		// the eval prompt tells claude not to commit during fix rounds, so HEAD alone can't distinguish
		// "rejected findings" from "made fixes without commit". checking the diff fingerprint catches
		// working tree edits, making the detection accurate for both cases.
		var stalemate bool
		unchangedRounds, stalemate = r.updateStalemate(headBefore, diffBefore, unchangedRounds)
		if stalemate {
			return nil
		}

		if err := r.sleepWithContext(loopCtx, r.iterationDelay); err != nil {
			if r.isBreak(loopCtx, ctx) {
				r.log.Print("manual break requested, external review terminated early")
				return nil
			}
			return fmt.Errorf("interrupted: %w", err)
		}
	}

	r.log.Print("max %s iterations reached, continuing to next phase...", cfg.name)
	return nil
}

// breakContext derives a child context that cancels when one value is drained from the break channel.
// if no break channel is configured, returns the parent context and a no-op cancel.
// callers detect break by checking loopCtx.Err() != nil && parentCtx.Err() == nil.
func (r *Runner) breakContext(parent context.Context) (context.Context, context.CancelFunc) {
	if r.breakCh == nil {
		return parent, func() {}
	}
	ctx, cancel := context.WithCancel(parent)
	go func() {
		select {
		case <-r.breakCh:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

// isBreak returns true if the loop context was canceled by a break signal
// while the parent context is still alive. does not read from the break channel,
// so it can be called without consuming a pending signal.
func (r *Runner) isBreak(loopCtx, parentCtx context.Context) bool {
	return loopCtx.Err() != nil && parentCtx.Err() == nil
}

// drainBreakCh does a non-blocking drain of one pending value from the break channel.
// called after pause+resume to prevent a SIGQUIT received during the pause prompt
// from immediately canceling the next iteration. not called on normal iteration
// boundaries so that a legitimate Ctrl+\ between iterations is preserved.
func (r *Runner) drainBreakCh() {
	if r.breakCh == nil {
		return
	}
	select {
	case <-r.breakCh:
	default:
	}
}

// buildCodexPrompt creates the prompt for codex review.
// uses the codex_review prompt loaded from config with all variables expanded,
// including {{PREVIOUS_REVIEW_CONTEXT}} for iteration context.
func (r *Runner) buildCodexPrompt(isFirst bool, claudeResponse string) string {
	return r.replaceVariablesWithIteration(r.cfg.AppConfig.CodexReviewPrompt, isFirst, claudeResponse)
}

// hasUncompletedTasks checks if any Task section has uncompleted checkboxes.
// only Task sections (### Task N: or ### Iteration N:) are considered.
// checkboxes in Success criteria, Overview, or Context are ignored for this check,
// so the agent can output ALL_TASKS_DONE when those are verification-only.
// for malformed plans (checkboxes without task headers), returns true if any [ ] exists.
func (r *Runner) hasUncompletedTasks() bool {
	path := r.resolvePlanFilePath()
	if path == "" {
		return false // no plan file, nothing to complete
	}
	p, err := plan.ParsePlanFile(path)
	if err != nil {
		r.log.Print("[WARN] failed to parse plan file for completion check: %v", err)
		return true // assume incomplete if can't read
	}
	for _, t := range p.Tasks {
		if t.HasUncompletedActionableWork() {
			return true
		}
	}
	// malformed plans: no task headers but file has [ ] — treat as incomplete
	if len(p.Tasks) == 0 {
		has, err := plan.FileHasUncompletedCheckbox(path)
		if err != nil {
			return true
		}
		if has {
			return true
		}
	}
	return false
}

// nextPlanTaskPosition returns the 1-indexed position of the first uncompleted task in the plan.
// returns 0 if the plan file can't be read/parsed or no uncompleted tasks exist (caller falls back to loop counter).
func (r *Runner) nextPlanTaskPosition() int {
	p, err := plan.ParsePlanFile(r.resolvePlanFilePath())
	if err != nil {
		r.log.Print("[WARN] failed to parse plan file for task position: %v", err)
		return 0
	}
	for i, t := range p.Tasks {
		if t.HasUncompletedActionableWork() {
			return i + 1 // 1-indexed
		}
	}
	return 0
}

// showCodexSummary displays a condensed summary of codex output before Claude evaluation.
// extracts text until first code block or maxCodexSummaryLen chars, whichever is shorter.
func (r *Runner) showCodexSummary(output string) {
	r.showExternalReviewSummary("codex", output)
}

// showExternalReviewSummary displays a condensed summary of external review output.
// extracts text until first code block or 5000 chars, whichever is shorter.
func (r *Runner) showExternalReviewSummary(toolName, output string) {
	summary := output

	// trim to first code block if present
	if idx := strings.Index(summary, "```"); idx > 0 {
		summary = summary[:idx]
	}

	// limit to maxCodexSummaryLen runes to avoid splitting multi-byte characters
	if runes := []rune(summary); len(runes) > maxCodexSummaryLen {
		summary = string(runes[:maxCodexSummaryLen]) + "..."
	}

	summary = strings.TrimSpace(summary)
	if summary == "" {
		return
	}

	r.log.Print("%s findings:", toolName)
	for line := range strings.SplitSeq(summary, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		r.log.PrintAligned("  " + line)
	}
}

// ErrUserAborted is a sentinel error returned when the user aborts or declines to resume after a break
// signal (Ctrl+\). it is propagated as a non-nil error so that callers (including mode entrypoints) can
// detect it and treat it as a clean user-initiated exit, avoiding further review/finalize steps.
var ErrUserAborted = errors.New("user aborted")

// ErrUserRejectedPlan is returned when user rejects the plan draft.
var ErrUserRejectedPlan = errors.New("user rejected plan")

// draftReviewResult holds the result of draft review handling.
type draftReviewResult struct {
	handled  bool   // true if draft was found and handled
	feedback string // revision feedback (non-empty only for "revise" action)
	err      error  // error if review failed or user rejected
}

// handlePlanDraft processes PLAN_DRAFT signal if present in output.
// returns result indicating whether draft was handled and any feedback/errors.
func (r *Runner) handlePlanDraft(ctx context.Context, output string) draftReviewResult {
	planContent, draftErr := parsePlanDraftPayload(output)
	if draftErr != nil {
		// log malformed signals (but not "no signal" which is expected)
		if !errors.Is(draftErr, errNoPlanDraftSignal) {
			r.log.Print("warning: %v", draftErr)
		}
		return draftReviewResult{handled: false}
	}

	r.log.Print("plan draft ready for review")

	action, feedback, askErr := r.inputCollector.AskDraftReview(ctx, "Review the plan draft", planContent)
	if askErr != nil {
		return draftReviewResult{handled: true, err: fmt.Errorf("collect draft review: %w", askErr)}
	}

	// log the draft review action and feedback to progress file
	r.log.LogDraftReview(action, feedback)

	switch action {
	case "accept":
		r.log.Print("draft accepted, continuing to write plan file...")
		return draftReviewResult{handled: true}
	case "revise":
		r.log.Print("revision requested, re-running with feedback...")
		return draftReviewResult{handled: true, feedback: feedback}
	case "reject":
		r.log.Print("plan rejected by user")
		return draftReviewResult{handled: true, err: ErrUserRejectedPlan}
	}

	return draftReviewResult{handled: true}
}

// handlePlanQuestion processes QUESTION signal if present in output.
// returns true if question was found and handled, false otherwise.
// returns error if question handling failed.
func (r *Runner) handlePlanQuestion(ctx context.Context, output string) (bool, error) {
	question, err := parseQuestionPayload(output)
	if err != nil {
		// log malformed signals (but not "no signal" which is expected)
		if !errors.Is(err, errNoQuestionSignal) {
			r.log.Print("warning: %v", err)
		}
		return false, nil
	}

	r.log.LogQuestion(question.Question, question.Options)

	answer, askErr := r.inputCollector.AskQuestion(ctx, question.Question, question.Options)
	if askErr != nil {
		return true, fmt.Errorf("collect answer: %w", askErr)
	}

	r.log.LogAnswer(answer)
	return true, nil
}

// runPlanCreation executes the interactive plan creation loop.
// the loop continues until PLAN_READY signal or max iterations reached.
// handles QUESTION signals for Q&A and PLAN_DRAFT signals for draft review.
func (r *Runner) runPlanCreation(ctx context.Context) error {
	if r.cfg.PlanDescription == "" {
		return errors.New("plan description required for plan mode")
	}
	if r.inputCollector == nil {
		return errors.New("input collector required for plan mode")
	}

	r.phaseHolder.Set(status.PhasePlan)
	r.log.PrintRaw("starting interactive plan creation\n")
	r.log.Print("plan request: %s", r.cfg.PlanDescription)

	// plan iterations use 20% of max_iterations
	maxPlanIterations := max(minPlanIterations, r.cfg.MaxIterations/planIterationDivisor)

	// track revision feedback for context in next iteration
	var lastRevisionFeedback string

	for i := 1; i <= maxPlanIterations; i++ {
		select {
		case <-ctx.Done():
			return fmt.Errorf("plan creation: %w", ctx.Err())
		default:
		}

		r.log.PrintSection(status.NewPlanIterationSection(i))

		prompt := r.buildPlanPrompt()
		// append revision feedback context if present
		hadFeedback := lastRevisionFeedback != ""
		if hadFeedback {
			prompt = fmt.Sprintf("%s\n\n---\nPREVIOUS DRAFT FEEDBACK:\nUser requested revisions with this feedback:\n%s\n\nPlease revise the plan accordingly and present a new PLAN_DRAFT.", prompt, lastRevisionFeedback)
		}

		result := r.runWithLimitRetry(ctx, r.claude.Run, prompt, "claude")
		if result.Error != nil {
			if err := r.handlePatternMatchError(result.Error, "claude"); err != nil {
				return err
			}
			return fmt.Errorf("claude execution: %w", result.Error)
		}

		if result.Signal == SignalFailed {
			return errors.New("plan creation failed (FAILED signal received)")
		}

		// check for PLAN_READY signal
		if isPlanReady(result.Signal) {
			r.log.Print("plan creation completed")
			return nil
		}

		// on session timeout, skip output parsing and retry; the session was killed before
		// it could finish, so partial output may contain truncated PLAN_DRAFT or QUESTION markers.
		// preserve lastRevisionFeedback so the next attempt re-sends the user's revision request
		if r.lastSessionTimedOut {
			r.log.Print("plan creation session timed out, retrying iteration...")
			if err := r.sleepWithContext(ctx, r.iterationDelay); err != nil {
				return fmt.Errorf("interrupted: %w", err)
			}
			continue
		}

		// session completed successfully, clear revision feedback since it was consumed
		if hadFeedback {
			lastRevisionFeedback = ""
		}

		// check for PLAN_DRAFT signal - present draft for user review
		draftResult := r.handlePlanDraft(ctx, result.Output)
		if draftResult.err != nil {
			return draftResult.err
		}
		if draftResult.handled {
			lastRevisionFeedback = draftResult.feedback
			if err := r.sleepWithContext(ctx, r.iterationDelay); err != nil {
				return fmt.Errorf("interrupted: %w", err)
			}
			continue
		}

		// check for QUESTION signal
		handled, err := r.handlePlanQuestion(ctx, result.Output)
		if err != nil {
			return err
		}
		if handled {
			if err := r.sleepWithContext(ctx, r.iterationDelay); err != nil {
				return fmt.Errorf("interrupted: %w", err)
			}
			continue
		}

		// no question, no draft, and no completion - continue
		if err := r.sleepWithContext(ctx, r.iterationDelay); err != nil {
			return fmt.Errorf("interrupted: %w", err)
		}
	}

	return fmt.Errorf("max plan iterations (%d) reached without completion", maxPlanIterations)
}

// handlePatternMatchError checks if err is a PatternMatchError or LimitPatternError and logs appropriate messages.
// Returns the error if it's a pattern match (to trigger graceful exit), nil otherwise.
func (r *Runner) handlePatternMatchError(err error, tool string) error {
	var patternErr *executor.PatternMatchError
	if errors.As(err, &patternErr) {
		r.log.Print("error: detected %q in %s output", patternErr.Pattern, tool)
		r.log.Print("run '%s' for more information", patternErr.HelpCmd)
		return err
	}
	var limitErr *executor.LimitPatternError
	if errors.As(err, &limitErr) {
		r.log.Print("error: detected %q in %s output", limitErr.Pattern, tool)
		r.log.Print("run '%s' for more information", limitErr.HelpCmd)
		return err
	}
	return nil
}

// runWithLimitRetry wraps an executor Run() call with rate limit retry logic and optional session timeout.
// if the result contains a LimitPatternError and waitOnLimit > 0, it logs a message, waits, and retries.
// if waitOnLimit == 0, the LimitPatternError is returned as-is (existing exit behavior).
// other errors (including PatternMatchError) are returned without retry.
// when SessionTimeout > 0, each run() call gets a child context with deadline.
// on session timeout (child timed out but parent alive), logs a warning and returns result with error cleared.
// retries indefinitely until success or context cancellation.
func (r *Runner) runWithLimitRetry(ctx context.Context, run func(context.Context, string) executor.Result,
	prompt, toolName string) executor.Result {
	for {
		result := r.runWithSessionTimeout(ctx, run, prompt, toolName)
		if result.Error == nil {
			return result
		}

		var limitErr *executor.LimitPatternError
		if !errors.As(result.Error, &limitErr) {
			return result // not a limit error, return as-is
		}

		if r.waitOnLimit <= 0 {
			return result // no wait configured, return limit error as-is
		}

		r.log.Print("rate limit detected: %q in %s output, waiting %s before retry...",
			limitErr.Pattern, toolName, r.waitOnLimit)

		if err := r.sleepWithContext(ctx, r.waitOnLimit); err != nil {
			return executor.Result{Error: fmt.Errorf("interrupted during limit wait: %w", ctx.Err())}
		}
	}
}

// runWithSessionTimeout runs the executor with an optional session timeout.
// if SessionTimeout > 0 and toolName is "claude", wraps ctx with context.WithTimeout before calling run.
// on session timeout (child timed out but parent alive), logs a warning and clears the error
// so callers treat it as a non-completing iteration that continues naturally.
// only applies to claude sessions; codex and custom executors are not affected.
func (r *Runner) runWithSessionTimeout(ctx context.Context, run func(context.Context, string) executor.Result,
	prompt, toolName string) executor.Result {
	r.lastSessionTimedOut = false
	sessionTimeout := r.sessionTimeout()
	if sessionTimeout <= 0 || toolName != "claude" {
		return run(ctx, prompt) // no timeout configured or non-claude tool
	}

	childCtx, cancel := context.WithTimeout(ctx, sessionTimeout)
	defer cancel()

	result := run(childCtx, prompt)

	// check if this was a session timeout: child context expired but parent is still alive.
	// clear the error so callers (task loop, review loop) treat it as a non-completing iteration
	// rather than aborting the phase. set lastSessionTimedOut so review loops can distinguish
	// timeout from "genuinely found nothing" and continue instead of exiting.
	if childCtx.Err() != nil && ctx.Err() == nil {
		r.log.Print("warning: %s session timed out after %s, the agent may have started a blocking operation",
			toolName, sessionTimeout)
		result.Error = nil
		result.Signal = "" // clear any signal emitted before timeout; can't trust partial session
		r.lastSessionTimedOut = true
	}

	return result
}

// sessionTimeout returns the configured session timeout duration.
// returns 0 if not configured or AppConfig is nil.
func (r *Runner) sessionTimeout() time.Duration {
	if r.cfg.AppConfig == nil {
		return 0
	}
	return r.cfg.AppConfig.SessionTimeout
}

// runFinalize executes the optional finalize step after successful reviews.
// runs once, best-effort: failures are logged but don't block success.
// exception: context cancellation is propagated (user wants to abort).
func (r *Runner) runFinalize(ctx context.Context) error {
	if !r.cfg.FinalizeEnabled {
		return nil
	}

	r.phaseHolder.Set(status.PhaseFinalize)
	r.log.PrintSection(status.NewGenericSection("finalize step"))

	prompt := r.replacePromptVariables(r.cfg.AppConfig.FinalizePrompt)
	result := r.runWithLimitRetry(ctx, r.claude.Run, prompt, "claude")

	if result.Error != nil {
		// propagate context cancellation - user wants to abort
		if errors.Is(result.Error, context.Canceled) || errors.Is(result.Error, context.DeadlineExceeded) {
			return fmt.Errorf("finalize step: %w", result.Error)
		}
		// pattern match (rate limit or error) - log via shared helper, but don't fail (best-effort)
		if r.handlePatternMatchError(result.Error, "claude") != nil {
			return nil //nolint:nilerr // intentional: best-effort semantics, log but don't propagate
		}
		// best-effort: log error but don't fail
		r.log.Print("finalize step failed: %v", result.Error)
		return nil
	}

	if result.Signal == SignalFailed {
		r.log.Print("finalize step reported failure (non-blocking)")
		return nil
	}

	r.log.Print("finalize step completed")
	return nil
}

// sleepWithContext pauses for the given duration but returns immediately if context is canceled.
// returns ctx.Err() on cancellation, nil on normal completion.
func (r *Runner) sleepWithContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("sleep interrupted: %w", ctx.Err())
	}
}

// needsCodexBinary returns true if the current configuration requires the codex binary.
// returns false when external_review_tool is "custom" or "none", since codex isn't used.
func needsCodexBinary(appConfig *config.Config) bool {
	if appConfig == nil {
		return true // default behavior assumes codex
	}
	switch appConfig.ExternalReviewTool {
	case "custom", "none":
		return false
	default:
		return true // "codex" or empty (default) requires codex binary
	}
}
