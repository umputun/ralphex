// Package processor provides the main orchestration loop for ralphex execution.
package processor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/umputun/ralphex/pkg/config"
	"github.com/umputun/ralphex/pkg/executor"
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
	PlanFile         string         // path to plan file (required for full mode)
	PlanDescription  string         // plan description for interactive plan creation mode
	ProgressPath     string         // path to progress file
	Mode             Mode           // execution mode
	MaxIterations    int            // maximum iterations for task phase
	Debug            bool           // enable debug output
	NoColor          bool           // disable color output
	IterationDelayMs int            // delay between iterations in milliseconds
	TaskRetryCount   int            // number of times to retry failed tasks
	CodexEnabled     bool           // whether codex review is enabled
	FinalizeEnabled  bool           // whether finalize step is enabled
	DefaultBranch    string         // default branch name (detected from repo)
	AppConfig        *config.Config // full application config (for executors and prompts)
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
}

// Runner orchestrates the execution loop.
type Runner struct {
	cfg            Config
	log            Logger
	claude         Executor
	codex          Executor
	custom         *executor.CustomExecutor
	git            GitChecker
	inputCollector InputCollector
	phaseHolder    *status.PhaseHolder
	iterationDelay time.Duration
	taskRetryCount int
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

	return NewWithExecutors(cfg, log, claudeExec, codexExec, customExec, holder)
}

// NewWithExecutors creates a new Runner with custom executors (for testing).
func NewWithExecutors(cfg Config, log Logger, claude, codex Executor, custom *executor.CustomExecutor, holder *status.PhaseHolder) *Runner {
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

	return &Runner{
		cfg:            cfg,
		log:            log,
		claude:         claude,
		codex:          codex,
		custom:         custom,
		phaseHolder:    holder,
		iterationDelay: iterDelay,
		taskRetryCount: retryCount,
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

	// claude review loop (critical/major) after codex
	r.phaseHolder.Set(status.PhaseReview)

	if err := r.runClaudeReviewLoop(ctx); err != nil {
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
		return fmt.Errorf("task phase: %w", err)
	}

	r.log.Print("task execution completed successfully")
	return nil
}

// runTaskPhase executes tasks until completion or max iterations.
// executes ONE Task section per iteration.
func (r *Runner) runTaskPhase(ctx context.Context) error {
	prompt := r.replacePromptVariables(r.cfg.AppConfig.TaskPrompt)
	retryCount := 0

	for i := 1; i <= r.cfg.MaxIterations; i++ {
		select {
		case <-ctx.Done():
			return fmt.Errorf("task phase: %w", ctx.Err())
		default:
		}

		r.log.PrintSection(status.NewTaskIterationSection(i))

		result := r.claude.Run(ctx, prompt)
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
				time.Sleep(r.iterationDelay)
				continue
			}
			return errors.New("task execution failed after retry (FAILED signal received)")
		}

		retryCount = 0
		// continue with same prompt - it reads from plan file each time
		time.Sleep(r.iterationDelay)
	}

	return fmt.Errorf("max iterations (%d) reached without completion", r.cfg.MaxIterations)
}

// runClaudeReview runs Claude review with the given prompt until REVIEW_DONE.
func (r *Runner) runClaudeReview(ctx context.Context, prompt string) error {
	result := r.claude.Run(ctx, prompt)
	if result.Error != nil {
		if err := r.handlePatternMatchError(result.Error, "claude"); err != nil {
			return err
		}
		return fmt.Errorf("claude execution: %w", result.Error)
	}

	if result.Signal == SignalFailed {
		return errors.New("review failed (FAILED signal received)")
	}

	if !IsReviewDone(result.Signal) {
		r.log.Print("warning: first review pass did not complete cleanly, continuing...")
	}

	return nil
}

// runClaudeReviewLoop runs claude review iterations using second review prompt.
func (r *Runner) runClaudeReviewLoop(ctx context.Context) error {
	// review iterations = 10% of max_iterations
	maxReviewIterations := max(minReviewIterations, r.cfg.MaxIterations/reviewIterationDivisor)

	for i := 1; i <= maxReviewIterations; i++ {
		select {
		case <-ctx.Done():
			return fmt.Errorf("review: %w", ctx.Err())
		default:
		}

		r.log.PrintSection(status.NewClaudeReviewSection(i, ": critical/major"))

		// capture HEAD hash before running claude for no-commit detection
		headBefore := r.headHash()

		result := r.claude.Run(ctx, r.replacePromptVariables(r.cfg.AppConfig.ReviewSecondPrompt))
		if result.Error != nil {
			if err := r.handlePatternMatchError(result.Error, "claude"); err != nil {
				return err
			}
			return fmt.Errorf("claude execution: %w", result.Error)
		}

		if result.Signal == SignalFailed {
			return errors.New("review failed (FAILED signal received)")
		}

		if IsReviewDone(result.Signal) {
			r.log.Print("claude review complete - no more findings")
			return nil
		}

		// fallback: if HEAD hash hasn't changed, claude found nothing to fix
		if headBefore != "" {
			if headAfter := r.headHash(); headAfter == headBefore {
				r.log.Print("claude review complete - no changes detected")
				return nil
			}
		}

		r.log.Print("issues fixed, running another review iteration...")
		time.Sleep(r.iterationDelay)
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
			showSummary:     r.showCustomSummary,
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

// runExternalReviewLoop runs a generic external review tool-claude loop until no findings.
func (r *Runner) runExternalReviewLoop(ctx context.Context, cfg externalReviewConfig) error {
	// iterations = 20% of max_iterations (min 3)
	maxIterations := max(3, r.cfg.MaxIterations/5)

	var claudeResponse string // first iteration has no prior response

	for i := 1; i <= maxIterations; i++ {
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s loop: %w", cfg.name, ctx.Err())
		default:
		}

		r.log.PrintSection(cfg.makeSection(i))

		// run external review tool
		reviewResult := cfg.runReview(ctx, cfg.buildPrompt(i == 1, claudeResponse))
		if reviewResult.Error != nil {
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

		// pass output to claude for evaluation and fixing
		r.phaseHolder.Set(status.PhaseClaudeEval)
		r.log.PrintSection(status.NewClaudeEvalSection())
		claudeResult := r.claude.Run(ctx, cfg.buildEvalPrompt(reviewResult.Output))

		// restore codex phase for next iteration
		r.phaseHolder.Set(status.PhaseCodex)
		if claudeResult.Error != nil {
			if err := r.handlePatternMatchError(claudeResult.Error, "claude"); err != nil {
				return err
			}
			return fmt.Errorf("claude execution: %w", claudeResult.Error)
		}

		claudeResponse = claudeResult.Output

		// exit only when claude sees "no findings"
		if IsCodexDone(claudeResult.Signal) {
			r.log.Print("%s review complete - no more findings", cfg.name)
			return nil
		}

		time.Sleep(r.iterationDelay)
	}

	r.log.Print("max %s iterations reached, continuing to next phase...", cfg.name)
	return nil
}

// buildCodexPrompt creates the prompt for codex review.
func (r *Runner) buildCodexPrompt(isFirst bool, claudeResponse string) string {
	// build plan context if available
	planContext := ""
	if r.cfg.PlanFile != "" {
		planContext = fmt.Sprintf(`
## Plan Context
The code implements the plan at: %s

---
`, r.resolvePlanFilePath())
	}

	// different diff command based on iteration
	var diffInstruction, diffDescription string
	if isFirst {
		defaultBranch := r.getDefaultBranch()
		diffInstruction = fmt.Sprintf("Run: git diff %s...HEAD", defaultBranch)
		diffDescription = fmt.Sprintf("code changes between %s and HEAD branch", defaultBranch)
	} else {
		diffInstruction = "Run: git diff"
		diffDescription = "uncommitted changes (Claude's fixes from previous iteration)"
	}

	basePrompt := fmt.Sprintf(`%sReview the %s.

%s

Analyze for:
- Bugs and logic errors
- Security vulnerabilities
- Race conditions
- Error handling gaps
- Code quality issues

Report findings with file:line references. If no issues found, say "NO ISSUES FOUND".`, planContext, diffDescription, diffInstruction)

	if claudeResponse != "" {
		return fmt.Sprintf(`%s

---
PREVIOUS REVIEW CONTEXT:
Claude (previous reviewer) responded to your findings:

%s

Re-evaluate considering Claude's arguments. If Claude's fixes are correct, acknowledge them.
If Claude's arguments are invalid, explain why the issues still exist.`, basePrompt, claudeResponse)
	}

	return basePrompt
}

// hasUncompletedTasks checks if plan file has any uncompleted checkboxes.
func (r *Runner) hasUncompletedTasks() bool {
	content, err := os.ReadFile(r.resolvePlanFilePath())
	if err != nil {
		return true // assume incomplete if can't read
	}

	// look for uncompleted checkbox pattern: [ ] (not [x])
	for line := range strings.SplitSeq(string(content), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- [ ]") {
			return true
		}
	}
	return false
}

// showCodexSummary displays a condensed summary of codex output before Claude evaluation.
// extracts text until first code block or maxCodexSummaryLen chars, whichever is shorter.
func (r *Runner) showCodexSummary(output string) {
	r.showExternalReviewSummary("codex", output)
}

// showCustomSummary displays a condensed summary of custom review output before Claude evaluation.
func (r *Runner) showCustomSummary(output string) {
	r.showExternalReviewSummary("custom", output)
}

// showExternalReviewSummary displays a condensed summary of external review output.
// extracts text until first code block or 5000 chars, whichever is shorter.
func (r *Runner) showExternalReviewSummary(toolName, output string) {
	summary := output

	// trim to first code block if present
	if idx := strings.Index(summary, "```"); idx > 0 {
		summary = summary[:idx]
	}

	// limit to maxCodexSummaryLen chars
	if len(summary) > maxCodexSummaryLen {
		summary = summary[:maxCodexSummaryLen] + "..."
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
	planContent, draftErr := ParsePlanDraftPayload(output)
	if draftErr != nil {
		// log malformed signals (but not "no signal" which is expected)
		if !errors.Is(draftErr, ErrNoPlanDraftSignal) {
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
	question, err := ParseQuestionPayload(output)
	if err != nil {
		// log malformed signals (but not "no signal" which is expected)
		if !errors.Is(err, ErrNoQuestionSignal) {
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
		if lastRevisionFeedback != "" {
			prompt = fmt.Sprintf("%s\n\n---\nPREVIOUS DRAFT FEEDBACK:\nUser requested revisions with this feedback:\n%s\n\nPlease revise the plan accordingly and present a new PLAN_DRAFT.", prompt, lastRevisionFeedback)
			lastRevisionFeedback = "" // clear after use
		}

		result := r.claude.Run(ctx, prompt)
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
		if IsPlanReady(result.Signal) {
			r.log.Print("plan creation completed")
			return nil
		}

		// check for PLAN_DRAFT signal - present draft for user review
		draftResult := r.handlePlanDraft(ctx, result.Output)
		if draftResult.err != nil {
			return draftResult.err
		}
		if draftResult.handled {
			lastRevisionFeedback = draftResult.feedback
			time.Sleep(r.iterationDelay)
			continue
		}

		// check for QUESTION signal
		handled, err := r.handlePlanQuestion(ctx, result.Output)
		if err != nil {
			return err
		}
		if handled {
			time.Sleep(r.iterationDelay)
			continue
		}

		// no question, no draft, and no completion - continue
		time.Sleep(r.iterationDelay)
	}

	return fmt.Errorf("max plan iterations (%d) reached without completion", maxPlanIterations)
}

// handlePatternMatchError checks if err is a PatternMatchError and logs appropriate messages.
// Returns the error if it's a pattern match (to trigger graceful exit), nil otherwise.
func (r *Runner) handlePatternMatchError(err error, tool string) error {
	var patternErr *executor.PatternMatchError
	if errors.As(err, &patternErr) {
		r.log.Print("error: detected %q in %s output", patternErr.Pattern, tool)
		r.log.Print("run '%s' for more information", patternErr.HelpCmd)
		return err
	}
	return nil
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
	result := r.claude.Run(ctx, prompt)

	if result.Error != nil {
		// propagate context cancellation - user wants to abort
		if errors.Is(result.Error, context.Canceled) || errors.Is(result.Error, context.DeadlineExceeded) {
			return fmt.Errorf("finalize step: %w", result.Error)
		}
		// pattern match (rate limit) - log via shared helper, but don't fail (best-effort)
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
