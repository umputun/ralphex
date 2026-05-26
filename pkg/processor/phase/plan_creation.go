package phase

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/umputun/ralphex/pkg/status"
)

type draftReviewResult struct {
	handled  bool
	feedback string
	err      error
}

type planIterationOutcome struct {
	done     bool
	feedback string
}

// PlanCreationPhase runs interactive plan creation.
type PlanCreationPhase struct {
	cfg            Config
	log            PlanCreationLogger
	exec           Executor
	policy         Policy
	prompts        PlanCreationPrompts
	deps           *Deps
	phaseHolder    *status.PhaseHolder
	iterationDelay time.Duration
}

// PlanCreationPhaseOpts contains dependencies for PlanCreationPhase.
type PlanCreationPhaseOpts struct {
	Cfg            Config
	Log            PlanCreationLogger
	Exec           Executor
	Policy         Policy
	Prompts        PlanCreationPrompts
	Deps           *Deps
	PhaseHolder    *status.PhaseHolder
	IterationDelay time.Duration
}

// NewPlanCreationPhase creates a plan creation phase engine.
func NewPlanCreationPhase(opts PlanCreationPhaseOpts) *PlanCreationPhase {
	return &PlanCreationPhase{
		cfg: opts.Cfg, log: opts.Log, exec: opts.Exec, policy: opts.Policy,
		prompts: opts.Prompts, deps: opts.Deps, phaseHolder: opts.PhaseHolder,
		iterationDelay: opts.IterationDelay,
	}
}

// Run executes interactive plan creation until PLAN_READY, user rejection, or iteration exhaustion.
func (p *PlanCreationPhase) Run(ctx context.Context) error {
	if p.cfg.PlanDescription == "" {
		return errors.New("plan description required for plan mode")
	}
	if p.deps == nil || p.deps.InputCollector == nil {
		return errors.New("input collector required for plan mode")
	}

	if p.phaseHolder != nil {
		p.phaseHolder.Set(status.PhasePlan)
	}
	p.log.PrintRaw("starting interactive plan creation\n")
	p.log.Print("plan request: %s", p.cfg.PlanDescription)

	maxPlanIterations := max(minPlanIterations, p.cfg.MaxIterations/planIterationDivisor)
	var lastRevisionFeedback string

	for i := 1; i <= maxPlanIterations; i++ {
		select {
		case <-ctx.Done():
			return fmt.Errorf("plan creation: %w", ctx.Err())
		default:
		}

		p.log.PrintSection(status.NewPlanIterationSection(i))

		outcome, err := p.runIteration(ctx, lastRevisionFeedback)
		if err != nil {
			return err
		}
		if outcome.done {
			return nil
		}
		lastRevisionFeedback = outcome.feedback

		if err := p.policy.Sleep(ctx, p.iterationDelay); err != nil {
			return fmt.Errorf("interrupted: %w", err)
		}
	}

	return fmt.Errorf("max plan iterations (%d) reached without completion", maxPlanIterations)
}

func (p *PlanCreationPhase) runIteration(ctx context.Context, lastRevisionFeedback string) (planIterationOutcome, error) {
	prompt := p.prompts.PlanPrompt()
	if lastRevisionFeedback != "" {
		prompt = fmt.Sprintf("%s\n\n---\nPREVIOUS DRAFT FEEDBACK:\nUser requested revisions with this feedback:\n%s\n\nPlease revise the plan accordingly and present a new PLAN_DRAFT.", prompt, lastRevisionFeedback)
	}

	execName := p.executorName()
	execResult := p.policy.Run(ctx, p.exec.Run, prompt, execName)
	result := execResult.Result
	if result.Error != nil {
		if err := p.policy.HandlePatternMatchError(result.Error, execName); err != nil {
			return planIterationOutcome{}, fmt.Errorf("%s pattern handling: %w", execName, err)
		}
		return planIterationOutcome{}, fmt.Errorf("%s execution: %w", execName, result.Error)
	}

	if result.Signal == SignalFailed {
		return planIterationOutcome{}, errors.New("plan creation failed (FAILED signal received)")
	}

	if IsPlanReady(result.Signal) {
		p.log.Print("plan creation completed")
		return planIterationOutcome{done: true}, nil
	}

	if execResult.TimedOut {
		p.log.Print("plan creation session timed out, retrying iteration...")
		return planIterationOutcome{feedback: lastRevisionFeedback}, nil
	}

	draftResult := p.handleDraft(ctx, result.Output)
	if draftResult.err != nil {
		return planIterationOutcome{}, draftResult.err
	}
	if draftResult.handled {
		return planIterationOutcome{feedback: draftResult.feedback}, nil
	}

	handled, err := p.handleQuestion(ctx, result.Output)
	if err != nil {
		return planIterationOutcome{}, err
	}
	if handled {
		return planIterationOutcome{}, nil
	}

	return planIterationOutcome{}, nil
}

func (p *PlanCreationPhase) handleDraft(ctx context.Context, output string) draftReviewResult {
	planContent, draftErr := ParsePlanDraftPayload(output)
	if draftErr != nil {
		if !errors.Is(draftErr, ErrNoPlanDraftSignal) {
			p.log.Print("warning: %v", draftErr)
		}
		return draftReviewResult{handled: false}
	}

	p.log.Print("plan draft ready for review")

	action, feedback, askErr := p.deps.InputCollector.AskDraftReview(ctx, "Review the plan draft", planContent)
	if askErr != nil {
		return draftReviewResult{handled: true, err: fmt.Errorf("collect draft review: %w", askErr)}
	}

	p.log.LogDraftReview(action, feedback)

	switch action {
	case "accept":
		p.log.Print("draft accepted, continuing to write plan file...")
		return draftReviewResult{handled: true}
	case "revise":
		p.log.Print("revision requested, re-running with feedback...")
		return draftReviewResult{handled: true, feedback: feedback}
	case "reject":
		p.log.Print("plan rejected by user")
		return draftReviewResult{handled: true, err: ErrUserRejectedPlan}
	}

	return draftReviewResult{handled: true}
}

func (p *PlanCreationPhase) handleQuestion(ctx context.Context, output string) (bool, error) {
	question, err := ParseQuestionPayload(output)
	if err != nil {
		if !errors.Is(err, ErrNoQuestionSignal) {
			p.log.Print("warning: %v", err)
		}
		return false, nil
	}

	p.log.LogQuestion(question.Question, question.Options)

	answer, askErr := p.deps.InputCollector.AskQuestion(ctx, question.Question, question.Options)
	if askErr != nil {
		return true, fmt.Errorf("collect answer: %w", askErr)
	}

	p.log.LogAnswer(answer)
	return true, nil
}

func (p *PlanCreationPhase) executorName() string {
	if p.cfg.isCodexExecutor() {
		return "codex"
	}
	return "claude"
}
