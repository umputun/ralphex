package phase

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/umputun/ralphex/pkg/status"
)

// ReviewPhase runs internal review passes.
type ReviewPhase struct {
	cfg            Config
	log            ReviewLogger
	exec           Executor
	policy         Policy
	prompts        ReviewPrompts
	git            *GitState
	phaseHolder    *status.PhaseHolder
	iterationDelay time.Duration
}

// ReviewPhaseOpts contains dependencies for ReviewPhase.
type ReviewPhaseOpts struct {
	Cfg            Config
	Log            ReviewLogger
	Exec           Executor
	Policy         Policy
	Prompts        ReviewPrompts
	Git            *GitState
	PhaseHolder    *status.PhaseHolder
	IterationDelay time.Duration
}

// NewReviewPhase creates an internal review phase engine.
func NewReviewPhase(opts ReviewPhaseOpts) *ReviewPhase {
	return &ReviewPhase{
		cfg: opts.Cfg, log: opts.Log, exec: opts.Exec, policy: opts.Policy,
		prompts: opts.Prompts, git: opts.Git, phaseHolder: opts.PhaseHolder,
		iterationDelay: opts.IterationDelay,
	}
}

// First runs the comprehensive first review pass and applies first-review timeout semantics.
func (p *ReviewPhase) First(ctx context.Context) error {
	if p.phaseHolder != nil {
		p.phaseHolder.Set(status.PhaseReview)
	}
	p.log.PrintSection(p.section(0, ": all findings"))
	return p.run(ctx, p.prompts.FirstReviewPrompt(), "first review pass")
}

// Loop runs critical/major review iterations until review completion or no changed HEAD.
func (p *ReviewPhase) Loop(ctx context.Context, prefix string) error {
	if p.phaseHolder != nil {
		p.phaseHolder.Set(status.PhaseReview)
	}
	maxReviewIterations := max(minReviewIterations, p.cfg.MaxIterations/reviewIterationDivisor)

	execName := p.cfg.executorName()
	for i := 1; i <= maxReviewIterations; i++ {
		select {
		case <-ctx.Done():
			return fmt.Errorf("review: %w", ctx.Err())
		default:
		}

		p.log.PrintSection(p.section(i, ": critical/major"))
		headBefore := p.headHash()

		execResult := p.policy.Run(ctx, p.exec.Run, p.prompts.SecondReviewPrompt(prefix), execName)
		result := execResult.Result
		if err := wrapExecutorError(p.policy, result.Error, execName); err != nil {
			return err
		}

		if result.Signal == SignalFailed {
			return errors.New("review failed (FAILED signal received)")
		}

		if IsReviewDone(result.Signal) {
			p.log.Print("%s review complete - no more findings", execName)
			return nil
		}

		if execResult.TimedOut {
			p.log.Print("session timed out, retrying review iteration after %s...", retryBackoff)
			if err := p.policy.Sleep(ctx, retryBackoff); err != nil {
				return fmt.Errorf("interrupted: %w", err)
			}
			continue
		}

		if headBefore != "" {
			if headAfter := p.headHash(); headAfter == headBefore {
				p.log.Print("%s review complete - no changes detected", execName)
				return nil
			}
		}

		p.log.Print("issues fixed, running another review iteration...")
		if err := p.policy.Sleep(ctx, p.iterationDelay); err != nil {
			return fmt.Errorf("interrupted: %w", err)
		}
	}

	p.log.Print("max %s review iterations reached, continuing...", execName)
	return nil
}

func (p *ReviewPhase) headHash() string {
	return p.git.headHash()
}

func (p *ReviewPhase) section(iteration int, suffix string) status.Section {
	if p.cfg.isCodexExecutor() {
		return status.NewInternalReviewSection(iteration, suffix)
	}
	return status.NewClaudeReviewSection(iteration, suffix)
}

func (p *ReviewPhase) run(ctx context.Context, prompt, phaseLabel string) error {
	execName := p.cfg.executorName()
	execResult := p.policy.Run(ctx, p.exec.Run, prompt, execName)
	result := execResult.Result
	if err := wrapExecutorError(p.policy, result.Error, execName); err != nil {
		return err
	}

	if result.Signal == SignalFailed {
		return errors.New("review failed (FAILED signal received)")
	}

	if execResult.TimedOut {
		if p.cfg.isCodexExecutor() {
			return fmt.Errorf("%s timed out", phaseLabel)
		}
		p.log.Print("warning: %s did not complete cleanly (session timed out), continuing...", phaseLabel)
		return nil
	}

	if !IsReviewDone(result.Signal) {
		p.log.Print("warning: %s did not complete cleanly, continuing...", phaseLabel)
	}

	return nil
}
