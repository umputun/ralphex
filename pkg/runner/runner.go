// Package runner provides the main orchestration loop for ralphex execution.
package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/umputun/ralphex/pkg/executor"
	"github.com/umputun/ralphex/pkg/progress"
)

// Mode represents the execution mode.
type Mode string

const (
	ModeFull      Mode = "full"       // full execution: tasks + reviews + codex
	ModeReview    Mode = "review"     // skip tasks, run full review pipeline
	ModeCodexOnly Mode = "codex-only" // skip tasks and first review, run only codex loop
)

// Config holds runner configuration.
type Config struct {
	PlanFile      string // path to plan file (required for full mode)
	ProgressPath  string // path to progress file
	Mode          Mode   // execution mode
	MaxIterations int    // maximum iterations for task phase
	Debug         bool   // enable debug output
	NoColor       bool   // disable color output
}

// Executor runs CLI commands and returns results.
type Executor interface {
	Run(ctx context.Context, prompt string) executor.Result
}

// Logger provides logging functionality.
type Logger interface {
	SetPhase(phase progress.Phase)
	Print(format string, args ...any)
	PrintRaw(format string, args ...any)
	PrintAligned(text string)
	Path() string
}

// Runner orchestrates the execution loop.
type Runner struct {
	cfg    Config
	log    Logger
	claude Executor
	codex  Executor
}

// New creates a new Runner with the given configuration.
func New(cfg Config, log *progress.Logger) *Runner {
	return &Runner{
		cfg: cfg,
		log: log,
		claude: &executor.ClaudeExecutor{
			OutputHandler: func(text string) {
				// stream output with timestamps like ralph.py's print_aligned
				log.PrintAligned(text)
			},
			Debug: cfg.Debug,
		},
		codex: &executor.CodexExecutor{
			OutputHandler: func(text string) {
				log.PrintAligned(text)
			},
			Debug: cfg.Debug,
		},
	}
}

// NewWithExecutors creates a new Runner with custom executors (for testing).
func NewWithExecutors(cfg Config, log Logger, claude, codex Executor) *Runner {
	return &Runner{
		cfg:    cfg,
		log:    log,
		claude: claude,
		codex:  codex,
	}
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
	default:
		return fmt.Errorf("unknown mode: %s", r.cfg.Mode)
	}
}

// runFull executes the complete pipeline: tasks → review → codex → review.
// Matches ralph.py's run_loop exactly.
func (r *Runner) runFull(ctx context.Context) error {
	if r.cfg.PlanFile == "" {
		return errors.New("plan file required for full mode")
	}

	// phase 1: task execution
	r.log.SetPhase(progress.PhaseTask)
	r.log.Print("starting task execution phase")

	if err := r.runTaskPhase(ctx); err != nil {
		return fmt.Errorf("task phase: %w", err)
	}

	// phase 2: first review pass - address ALL findings
	r.log.SetPhase(progress.PhaseReview)
	r.log.Print("review pass 1: all findings")

	if err := r.runClaudeReview(ctx, buildFirstReviewPrompt(r.cfg.PlanFile)); err != nil {
		return fmt.Errorf("first review: %w", err)
	}

	// phase 2.1: claude review loop (critical/major) before codex
	if err := r.runClaudeReviewLoop(ctx); err != nil {
		return fmt.Errorf("pre-codex review loop: %w", err)
	}

	// phase 2.5: codex external review loop
	r.log.SetPhase(progress.PhaseCodex)
	r.log.Print("codex external review")

	if err := r.runCodexLoop(ctx); err != nil {
		return fmt.Errorf("codex loop: %w", err)
	}

	// phase 3: claude review loop (critical/major) after codex
	r.log.SetPhase(progress.PhaseReview)

	if err := r.runClaudeReviewLoop(ctx); err != nil {
		return fmt.Errorf("post-codex review loop: %w", err)
	}

	r.log.Print("all phases completed successfully")
	return nil
}

// runReviewOnly executes only the review pipeline: review → codex → review.
func (r *Runner) runReviewOnly(ctx context.Context) error {
	// phase 1: first review
	r.log.SetPhase(progress.PhaseReview)
	r.log.Print("review pass 1: all findings")

	if err := r.runClaudeReview(ctx, buildFirstReviewPrompt(r.cfg.PlanFile)); err != nil {
		return fmt.Errorf("first review: %w", err)
	}

	// phase 1.1: claude review loop (critical/major) before codex
	if err := r.runClaudeReviewLoop(ctx); err != nil {
		return fmt.Errorf("pre-codex review loop: %w", err)
	}

	// phase 2: codex external review loop
	r.log.SetPhase(progress.PhaseCodex)
	r.log.Print("codex external review")

	if err := r.runCodexLoop(ctx); err != nil {
		return fmt.Errorf("codex loop: %w", err)
	}

	// phase 3: claude review loop (critical/major) after codex
	r.log.SetPhase(progress.PhaseReview)

	if err := r.runClaudeReviewLoop(ctx); err != nil {
		return fmt.Errorf("post-codex review loop: %w", err)
	}

	r.log.Print("review phases completed successfully")
	return nil
}

// runCodexOnly executes only the codex pipeline: codex → review.
func (r *Runner) runCodexOnly(ctx context.Context) error {
	// phase 1: codex external review loop
	r.log.SetPhase(progress.PhaseCodex)
	r.log.Print("codex external review")

	if err := r.runCodexLoop(ctx); err != nil {
		return fmt.Errorf("codex loop: %w", err)
	}

	// phase 2: claude review loop (critical/major) after codex
	r.log.SetPhase(progress.PhaseReview)

	if err := r.runClaudeReviewLoop(ctx); err != nil {
		return fmt.Errorf("post-codex review loop: %w", err)
	}

	r.log.Print("codex phases completed successfully")
	return nil
}

// runTaskPhase executes tasks until completion or max iterations.
// Matches ralph.py exactly: ONE Task section per iteration.
func (r *Runner) runTaskPhase(ctx context.Context) error {
	progressPath := r.log.Path()
	prompt := buildTaskPrompt(r.cfg.PlanFile, progressPath)
	retryCount := 0

	for i := 1; i <= r.cfg.MaxIterations; i++ {
		select {
		case <-ctx.Done():
			return fmt.Errorf("task phase: %w", ctx.Err())
		default:
		}

		r.log.Print("task iteration %d/%d", i, r.cfg.MaxIterations)

		result := r.claude.Run(ctx, prompt)
		if result.Error != nil {
			return fmt.Errorf("claude execution: %w", result.Error)
		}

		if result.Signal == SignalCompleted {
			// verify plan actually has no uncompleted checkboxes
			if hasUncompletedTasks(r.cfg.PlanFile) {
				r.log.Print("warning: completion signal received but plan still has [ ] items, continuing...")
				continue
			}
			r.log.Print("all tasks completed, starting code review...")
			return nil
		}

		if result.Signal == SignalFailed {
			if retryCount < 1 {
				r.log.Print("task failed, retrying...")
				retryCount++
				continue
			}
			return errors.New("task execution failed after retry (FAILED signal received)")
		}

		retryCount = 0
		// continue with same prompt - it reads from plan file each time
	}

	return fmt.Errorf("max iterations (%d) reached without completion", r.cfg.MaxIterations)
}

// runClaudeReview runs Claude review with the given prompt until REVIEW_DONE.
func (r *Runner) runClaudeReview(ctx context.Context, prompt string) error {
	result := r.claude.Run(ctx, prompt)
	if result.Error != nil {
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
// Matches ralph.py's run_claude_review_loop exactly.
func (r *Runner) runClaudeReviewLoop(ctx context.Context) error {
	// review iterations = 10% of max_iterations (min 3)
	maxReviewIterations := max(3, r.cfg.MaxIterations/10)

	for i := 1; i <= maxReviewIterations; i++ {
		select {
		case <-ctx.Done():
			return fmt.Errorf("review: %w", ctx.Err())
		default:
		}

		r.log.Print("claude review: critical/major (iteration %d)", i)

		result := r.claude.Run(ctx, buildSecondReviewPrompt(r.cfg.PlanFile))
		if result.Error != nil {
			return fmt.Errorf("claude execution: %w", result.Error)
		}

		if result.Signal == SignalFailed {
			return errors.New("review failed (FAILED signal received)")
		}

		if IsReviewDone(result.Signal) {
			r.log.Print("claude review complete - no more findings")
			return nil
		}

		r.log.Print("issues fixed, running another review iteration...")
	}

	r.log.Print("max claude review iterations reached, continuing...")
	return nil
}

// runCodexLoop runs the codex-claude review loop until no findings.
// Matches ralph.py's run_codex_loop exactly.
func (r *Runner) runCodexLoop(ctx context.Context) error {
	// codex iterations = 20% of max_iterations (min 3)
	maxCodexIterations := max(3, r.cfg.MaxIterations/5)

	var claudeResponse string // first iteration has no prior response

	for i := 1; i <= maxCodexIterations; i++ {
		select {
		case <-ctx.Done():
			return fmt.Errorf("codex loop: %w", ctx.Err())
		default:
		}

		r.log.Print("codex iteration %d", i)

		// run codex analysis
		codexResult := r.codex.Run(ctx, buildCodexPrompt(i == 1, claudeResponse, r.cfg.PlanFile))
		if codexResult.Error != nil {
			return fmt.Errorf("codex execution: %w", codexResult.Error)
		}

		if codexResult.Output == "" {
			r.log.Print("codex review returned no output, skipping...")
			break
		}

		// pass codex output to claude for evaluation and fixing
		claudeResult := r.claude.Run(ctx, buildCodexEvaluationPrompt(codexResult.Output))
		if claudeResult.Error != nil {
			return fmt.Errorf("claude execution: %w", claudeResult.Error)
		}

		claudeResponse = claudeResult.Output

		// exit only when claude sees "no findings" from codex
		if IsCodexDone(claudeResult.Signal) {
			r.log.Print("codex review complete - no more findings")
			return nil
		}
	}

	r.log.Print("max codex iterations reached, continuing to next phase...")
	return nil
}

// buildCodexPrompt creates the prompt for codex review.
// Matches ralph.py's run_codex_review behavior.
func buildCodexPrompt(isFirst bool, claudeResponse, planFile string) string {
	// build plan context if available
	planContext := ""
	if planFile != "" {
		planContext = fmt.Sprintf(`
## Plan Context
The code implements the plan at: %s

---
`, planFile)
	}

	// different diff command based on iteration
	var diffInstruction, diffDescription string
	if isFirst {
		diffInstruction = "Run: git diff master...HEAD"
		diffDescription = "code changes between master and HEAD branch"
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
// Matches ralph.py's has_uncompleted_tasks exactly.
func hasUncompletedTasks(planFile string) bool {
	content, err := os.ReadFile(planFile) //nolint:gosec // planFile from CLI args
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
