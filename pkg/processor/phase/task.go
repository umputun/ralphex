package phase

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/umputun/ralphex/pkg/plan"
	"github.com/umputun/ralphex/pkg/status"
)

// TaskPhase executes plan tasks until completion.
type TaskPhase struct {
	cfg            Config
	log            TaskLogger
	exec           Executor
	policy         Policy
	prompts        TaskPrompts
	locator        Locator
	deps           *Deps
	breaks         *BreakController
	iterationDelay time.Duration
	retryCount     int
}

// TaskPhaseOpts contains dependencies for TaskPhase.
type TaskPhaseOpts struct {
	Cfg            Config
	Log            TaskLogger
	Exec           Executor
	Policy         Policy
	Prompts        TaskPrompts
	Locator        Locator
	Deps           *Deps
	Breaks         *BreakController
	IterationDelay time.Duration
	RetryCount     int
}

// NewTaskPhase creates a task phase engine.
func NewTaskPhase(opts TaskPhaseOpts) *TaskPhase {
	breaks := opts.Breaks
	if breaks == nil {
		breaks = NewBreakController(opts.Deps)
	}
	return &TaskPhase{
		cfg: opts.Cfg, log: opts.Log, exec: opts.Exec, policy: opts.Policy,
		prompts: opts.Prompts, locator: opts.Locator, deps: opts.Deps, breaks: breaks,
		iterationDelay: opts.IterationDelay, retryCount: opts.RetryCount,
	}
}

// Run executes one plan task per iteration until all actionable task checkboxes are complete.
func (p *TaskPhase) Run(ctx context.Context) error {
	prompt := p.prompts.TaskPrompt()
	retryCount := 0

	for i := 1; i <= p.cfg.MaxIterations; i++ {
		select {
		case <-ctx.Done():
			return fmt.Errorf("task phase: %w", ctx.Err())
		default:
		}

		taskNum := i
		if pos := p.NextPlanTaskPosition(); pos > 0 {
			taskNum = pos
		}
		p.log.PrintSection(status.NewTaskIterationSection(taskNum))

		loopCtx, loopCancel := p.breaks.context(ctx)

		execName := p.cfg.executorName()
		execResult := p.policy.Run(loopCtx, p.exec.Run, prompt, execName)
		result := execResult.Result

		manualBreak := p.breaks.isBreak(loopCtx, ctx)
		loopCancel()

		if manualBreak {
			p.log.Print("session interrupted by break signal")
			p.breaks.drain()
			if p.deps.PauseHandler == nil || !p.deps.PauseHandler(ctx) {
				return ErrUserAborted
			}
			p.breaks.drain()
			i--
			retryCount = 0
			continue
		}

		if err := wrapExecutorError(p.policy, result.Error, execName); err != nil {
			return err
		}

		if execResult.TimedOut {
			p.log.Print("%s session timed out, retrying task iteration...", execName)
			continue
		}

		if result.Signal == SignalCompleted {
			if p.HasUncompletedTasks() {
				p.log.Print("warning: completion signal received but plan still has [ ] items, continuing...")
				continue
			}
			p.log.PrintRaw("\nall tasks completed, starting code review...\n")
			return nil
		}

		if result.Signal == SignalFailed {
			if retryCount < p.retryCount {
				p.log.Print("task failed, retrying...")
				retryCount++
				if err := p.policy.Sleep(ctx, p.iterationDelay); err != nil {
					return fmt.Errorf("interrupted: %w", err)
				}
				continue
			}
			return errors.New("task execution failed after retry (FAILED signal received)")
		}

		retryCount = 0
		if err := p.policy.Sleep(ctx, p.iterationDelay); err != nil {
			return fmt.Errorf("interrupted: %w", err)
		}
	}

	return fmt.Errorf("max iterations (%d) reached without completion", p.cfg.MaxIterations)
}

// ValidatePlanHasTasks rejects plan files without executable task sections.
func (p *TaskPhase) ValidatePlanHasTasks() error {
	path := p.locator.Path()
	parsed, err := plan.ParsePlanFile(path)
	if err != nil {
		return fmt.Errorf("parse plan for validation: %w", err)
	}
	if len(parsed.Tasks) == 0 {
		return fmt.Errorf("plan file %q has no executable task sections (### Task N: or ### Iteration N:); add task sections or pass a different plan file", path)
	}
	return nil
}

// HasUncompletedTasks reports whether the current plan still has actionable unchecked task work.
func (p *TaskPhase) HasUncompletedTasks() bool {
	path := p.locator.Path()
	if path == "" {
		return false
	}
	parsed, err := plan.ParsePlanFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false
		}
		p.log.Print("[WARN] failed to parse plan file for completion check: %v", err)
		return true
	}
	for _, t := range parsed.Tasks {
		if t.HasUncompletedActionableWork() {
			return true
		}
	}
	if len(parsed.Tasks) == 0 {
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

// NextPlanTaskPosition returns the 1-indexed first uncompleted task position, or zero when unavailable.
func (p *TaskPhase) NextPlanTaskPosition() int {
	parsed, err := plan.ParsePlanFile(p.locator.Path())
	if err != nil {
		p.log.Print("[WARN] failed to parse plan file for task position: %v", err)
		return 0
	}
	for i, t := range parsed.Tasks {
		if t.HasUncompletedActionableWork() {
			return i + 1
		}
	}
	return 0
}
