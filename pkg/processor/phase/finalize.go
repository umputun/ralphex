package phase

import (
	"context"
	"errors"
	"fmt"

	"github.com/umputun/ralphex/pkg/status"
)

// FinalizePhase runs the optional finalize step.
type FinalizePhase struct {
	cfg         Config
	log         FinalizeLogger
	exec        Executor
	policy      Policy
	prompts     FinalizePrompts
	phaseHolder *status.PhaseHolder
}

// FinalizePhaseOpts contains dependencies for FinalizePhase.
type FinalizePhaseOpts struct {
	Cfg         Config
	Log         FinalizeLogger
	Exec        Executor
	Policy      Policy
	Prompts     FinalizePrompts
	PhaseHolder *status.PhaseHolder
}

// NewFinalizePhase creates a finalize phase engine.
func NewFinalizePhase(opts FinalizePhaseOpts) *FinalizePhase {
	return &FinalizePhase{
		cfg: opts.Cfg, log: opts.Log, exec: opts.Exec,
		policy: opts.Policy, prompts: opts.Prompts, phaseHolder: opts.PhaseHolder,
	}
}

// Run executes the optional finalize step; ordinary executor failures are logged and do not fail the pipeline.
func (p *FinalizePhase) Run(ctx context.Context) error {
	if !p.cfg.FinalizeEnabled {
		return nil
	}

	if p.phaseHolder != nil {
		p.phaseHolder.Set(status.PhaseFinalize)
	}
	p.log.PrintSection(status.NewGenericSection("finalize step"))

	execName := p.cfg.executorName()
	execResult := p.policy.Run(ctx, p.exec.Run, p.prompts.FinalizePrompt(), execName)
	result := execResult.Result

	if result.Error != nil {
		if errors.Is(result.Error, context.Canceled) || errors.Is(result.Error, context.DeadlineExceeded) {
			return fmt.Errorf("finalize step: %w", result.Error)
		}
		if p.policy.HandlePatternMatchError(result.Error, execName) != nil {
			return nil //nolint:nilerr // intentional: best-effort semantics, log but don't propagate
		}
		p.log.Print("finalize step failed: %v", result.Error)
		return nil
	}

	if result.Signal == SignalFailed {
		p.log.Print("finalize step reported failure (non-blocking)")
		return nil
	}

	p.log.Print("finalize step completed")
	return nil
}
