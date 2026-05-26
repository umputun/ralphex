package phase

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/umputun/ralphex/pkg/executor"
	"github.com/umputun/ralphex/pkg/status"
)

// ExternalReviewOutcome reports whether external review found issues.
type ExternalReviewOutcome struct {
	HadFindings bool
}

// ExternalReviewPhase runs codex or custom external review loops.
type ExternalReviewPhase struct {
	cfg            Config
	log            ExternalReviewLogger
	external       Executor
	custom         *executor.CustomExecutor
	review         Executor
	policy         Policy
	prompts        ExternalReviewPrompts
	breaks         *BreakController
	git            *GitState
	phaseHolder    *status.PhaseHolder
	iterationDelay time.Duration
}

// ExternalReviewPhaseOpts contains dependencies for ExternalReviewPhase.
type ExternalReviewPhaseOpts struct {
	Cfg            Config
	Log            ExternalReviewLogger
	External       Executor
	Custom         *executor.CustomExecutor
	Review         Executor
	Policy         Policy
	Prompts        ExternalReviewPrompts
	Breaks         *BreakController
	Git            *GitState
	PhaseHolder    *status.PhaseHolder
	IterationDelay time.Duration
}

// NewExternalReviewPhase creates an external review phase engine.
func NewExternalReviewPhase(opts ExternalReviewPhaseOpts) *ExternalReviewPhase {
	return &ExternalReviewPhase{
		cfg: opts.Cfg, log: opts.Log, external: opts.External, custom: opts.Custom,
		review: opts.Review, policy: opts.Policy, prompts: opts.Prompts, breaks: opts.Breaks,
		git: opts.Git, phaseHolder: opts.PhaseHolder, iterationDelay: opts.IterationDelay,
	}
}

// Tool returns the effective external review tool after config and back-compat rules.
func (p *ExternalReviewPhase) Tool() string {
	if p.cfg.ExternalReviewToolSet && p.cfg.AppConfig != nil && p.cfg.AppConfig.ExternalReviewTool != "" {
		return p.cfg.AppConfig.ExternalReviewTool
	}
	if !p.cfg.CodexEnabled {
		return "none"
	}
	if p.cfg.AppConfig != nil && p.cfg.AppConfig.ExternalReviewTool != "" {
		return p.cfg.AppConfig.ExternalReviewTool
	}
	return "codex"
}

// Run executes the configured external review loop and reports whether fixes need post-review.
func (p *ExternalReviewPhase) Run(ctx context.Context) (ExternalReviewOutcome, error) {
	switch p.Tool() {
	case "none":
		p.log.Print("external review disabled, skipping...")
		return ExternalReviewOutcome{}, nil
	case "custom":
		return p.runCustom(ctx)
	default:
		return p.runCodex(ctx)
	}
}

func (p *ExternalReviewPhase) runCodex(ctx context.Context) (ExternalReviewOutcome, error) {
	if p.external == nil {
		return ExternalReviewOutcome{}, errors.New("codex review executor not configured")
	}
	return p.runLoop(ctx, "codex")
}

func (p *ExternalReviewPhase) runCustom(ctx context.Context) (ExternalReviewOutcome, error) {
	if p.custom == nil {
		return ExternalReviewOutcome{}, errors.New("custom review script not configured")
	}
	return p.runLoop(ctx, "custom")
}

func (p *ExternalReviewPhase) showSummary(toolName, output string) {
	summary := output
	if idx := strings.Index(summary, "```"); idx > 0 {
		summary = summary[:idx]
	}
	if runes := []rune(summary); len(runes) > maxCodexSummaryLen {
		summary = string(runes[:maxCodexSummaryLen]) + "..."
	}

	summary = strings.TrimSpace(summary)
	if summary == "" {
		return
	}

	p.log.Print("%s findings:", toolName)
	for line := range strings.SplitSeq(summary, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		p.log.PrintAligned("  " + line)
	}
}

func (p *ExternalReviewPhase) runLoop(ctx context.Context, tool string) (ExternalReviewOutcome, error) {
	outcome := ExternalReviewOutcome{}
	loopCtx, loopCancel := p.breaks.context(ctx)
	defer loopCancel()

	var claudeResponse string
	firstCompleted := false
	stalemate := newStalemateState(p.cfg, p.log)

loop:
	for i := 1; i <= p.maxIterations(); i++ {
		result, err := p.runIteration(loopCtx, externalReviewIterationOpts{
			parent:         ctx,
			tool:           tool,
			iteration:      i,
			firstCompleted: firstCompleted,
			claudeResponse: claudeResponse,
		})
		if err != nil {
			if errors.Is(err, errExternalReviewBreak) {
				return outcome, nil
			}
			return outcome, err
		}

		if result.firstCompleted {
			firstCompleted = true
			claudeResponse = result.claudeResponse
		}
		if result.hadFindings {
			outcome.HadFindings = true
			if stalemate.Update(result.before, p.git.snapshot()) {
				return outcome, nil
			}
		}

		switch result.action {
		case externalReviewContinue:
		case externalReviewStop:
			return outcome, nil
		case externalReviewBreakLoop:
			break loop
		case externalReviewRetry:
			continue
		}

		if err := p.sleepBeforeNext(loopCtx, ctx); err != nil {
			if errors.Is(err, errExternalReviewBreak) {
				return outcome, nil
			}
			return outcome, err
		}
	}

	p.log.Print("max %s iterations reached, continuing to next phase...", tool)
	return outcome, nil
}

type externalReviewIterationAction int

const (
	externalReviewContinue externalReviewIterationAction = iota
	externalReviewRetry
	externalReviewStop
	externalReviewBreakLoop
)

type externalReviewIterationOpts struct {
	parent         context.Context
	tool           string
	iteration      int
	firstCompleted bool
	claudeResponse string
}

type externalReviewIterationResult struct {
	action         externalReviewIterationAction
	before         gitSnapshot
	claudeResponse string
	firstCompleted bool
	hadFindings    bool
}

func (p *ExternalReviewPhase) runIteration(ctx context.Context, opts externalReviewIterationOpts) (externalReviewIterationResult, error) {
	if err := p.checkLoopDone(ctx, opts.parent, opts.tool); err != nil {
		return externalReviewIterationResult{}, err
	}

	p.log.PrintSection(p.section(opts.tool, opts.iteration))

	reviewExecResult := p.runReviewTool(ctx, opts.tool, p.reviewPrompt(opts.tool, !opts.firstCompleted, opts.claudeResponse))
	reviewResult := reviewExecResult.Result
	if reviewResult.Error != nil {
		if err := p.handleExecutorError(ctx, opts.parent, opts.tool, reviewResult.Error); err != nil {
			return externalReviewIterationResult{}, err
		}
	}

	if reviewExecResult.TimedOut {
		p.log.Print("%s review session timed out, retrying on next iteration...", opts.tool)
		return externalReviewIterationResult{action: externalReviewRetry}, nil
	}

	if reviewResult.Output == "" {
		p.log.Print("%s review returned no output, skipping...", opts.tool)
		return externalReviewIterationResult{action: externalReviewBreakLoop}, nil
	}

	if opts.tool == "codex" {
		p.showSummary(opts.tool, reviewResult.Output)
	}

	before := p.snapshotBeforeEval()
	claudeExecResult, err := p.runClaudeEvaluation(ctx, opts.parent, opts.tool, reviewResult.Output)
	if err != nil {
		return externalReviewIterationResult{}, err
	}

	if claudeExecResult.TimedOut {
		p.log.Print("claude eval session timed out, retrying %s iteration...", opts.tool)
		return externalReviewIterationResult{action: externalReviewRetry}, nil
	}

	claudeResult := claudeExecResult.Result
	result := externalReviewIterationResult{before: before, claudeResponse: claudeResult.Output, firstCompleted: true}
	if IsCodexDone(claudeResult.Signal) {
		p.log.Print("%s review complete - no more findings", opts.tool)
		result.action = externalReviewStop
		return result, nil
	}

	result.hadFindings = true
	return result, nil
}

var errExternalReviewBreak = errors.New("external review interrupted by manual break")

func (p *ExternalReviewPhase) checkLoopDone(loopCtx, parent context.Context, tool string) error {
	select {
	case <-loopCtx.Done():
		if p.breaks.isBreak(loopCtx, parent) {
			p.log.Print("manual break requested, external review terminated early")
			return errExternalReviewBreak
		}
		return fmt.Errorf("%s loop: %w", tool, parent.Err())
	default:
		return nil
	}
}

func (p *ExternalReviewPhase) handleExecutorError(loopCtx, parent context.Context, tool string, err error) error {
	if p.breaks.isBreak(loopCtx, parent) {
		p.log.Print("manual break requested, external review terminated early")
		return errExternalReviewBreak
	}
	if patternErr := p.policy.HandlePatternMatchError(err, tool); patternErr != nil {
		return fmt.Errorf("%s pattern handling: %w", tool, patternErr)
	}
	return fmt.Errorf("%s execution: %w", tool, err)
}

func (p *ExternalReviewPhase) snapshotBeforeEval() gitSnapshot {
	if p.cfg.ReviewPatience <= 0 {
		return gitSnapshot{}
	}
	return p.git.snapshot()
}

func (p *ExternalReviewPhase) runClaudeEvaluation(loopCtx, parent context.Context, tool, output string) (ExecutionResult, error) {
	p.phaseHolder.Set(status.PhaseClaudeEval)
	p.log.PrintSection(status.NewClaudeEvalSection())
	result := p.policy.Run(loopCtx, p.review.Run, p.evalPrompt(tool, output), "claude")
	p.phaseHolder.Set(status.PhaseCodex)

	if result.Result.Error == nil {
		return result, nil
	}
	if err := p.handleExecutorError(loopCtx, parent, "claude", result.Result.Error); err != nil {
		return ExecutionResult{}, err
	}
	return result, nil
}

func (p *ExternalReviewPhase) sleepBeforeNext(loopCtx, parent context.Context) error {
	if err := p.policy.Sleep(loopCtx, p.iterationDelay); err != nil {
		if p.breaks.isBreak(loopCtx, parent) {
			p.log.Print("manual break requested, external review terminated early")
			return errExternalReviewBreak
		}
		return fmt.Errorf("interrupted: %w", err)
	}
	return nil
}

func (p *ExternalReviewPhase) maxIterations() int {
	maxIterations := max(minCodexIterations, p.cfg.MaxIterations/codexIterationDivisor)
	if p.cfg.MaxExternalIterations > 0 {
		maxIterations = p.cfg.MaxExternalIterations
	}
	return maxIterations
}

func (p *ExternalReviewPhase) runReviewTool(ctx context.Context, tool, prompt string) ExecutionResult {
	if tool == "custom" {
		return p.policy.Run(ctx, p.custom.Run, prompt, tool)
	}
	return p.policy.Run(ctx, p.external.Run, prompt, tool)
}

func (p *ExternalReviewPhase) reviewPrompt(tool string, isFirst bool, claudeResponse string) string {
	if tool == "custom" {
		return p.prompts.CustomReviewPrompt(isFirst, claudeResponse)
	}
	return p.prompts.CodexReviewPrompt(isFirst, claudeResponse)
}

func (p *ExternalReviewPhase) evalPrompt(tool, output string) string {
	if tool == "custom" {
		return p.prompts.CustomEvaluationPrompt(output)
	}
	return p.prompts.CodexEvaluationPrompt(output)
}

func (p *ExternalReviewPhase) section(tool string, iteration int) status.Section {
	if tool == "custom" {
		return status.NewCustomIterationSection(iteration)
	}
	return status.NewCodexIterationSection(iteration)
}
