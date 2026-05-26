package processor

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/umputun/ralphex/pkg/executor"
	"github.com/umputun/ralphex/pkg/processor/phase"
)

type executionPolicy struct {
	cfg         Config
	log         Logger
	waitOnLimit time.Duration
}

type executionPolicyOpts struct {
	cfg         Config
	log         Logger
	waitOnLimit time.Duration
}

func newExecutionPolicy(opts executionPolicyOpts) *executionPolicy {
	return &executionPolicy{cfg: opts.cfg, log: opts.log, waitOnLimit: opts.waitOnLimit}
}

func (p *executionPolicy) Run(ctx context.Context, run func(context.Context, string) executor.Result,
	prompt string, toolName string) phase.ExecutionResult {
	for {
		result := p.runWithSessionTimeout(ctx, run, prompt, toolName)
		if result.Result.Error == nil {
			return result
		}

		var limitErr *executor.LimitPatternError
		if !errors.As(result.Result.Error, &limitErr) {
			return result
		}

		if p.waitOnLimit <= 0 {
			return result
		}

		p.log.Print("rate limit detected: %q in %s output, waiting %s before retry...",
			limitErr.Pattern, toolName, p.waitOnLimit)

		if err := p.Sleep(ctx, p.waitOnLimit); err != nil {
			return phase.ExecutionResult{Result: executor.Result{Error: fmt.Errorf("interrupted during limit wait: %w", ctx.Err())}}
		}
	}
}

func (p *executionPolicy) HandlePatternMatchError(err error, tool string) error {
	var patternErr *executor.PatternMatchError
	if errors.As(err, &patternErr) {
		p.log.Print("error: detected %q in %s output", patternErr.Pattern, tool)
		p.log.Print("run '%s' for more information", patternErr.HelpCmd)
		return err
	}
	var limitErr *executor.LimitPatternError
	if errors.As(err, &limitErr) {
		p.log.Print("error: detected %q in %s output", limitErr.Pattern, tool)
		p.log.Print("run '%s' for more information", limitErr.HelpCmd)
		return err
	}
	return nil
}

func (p *executionPolicy) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("sleep interrupted: %w", ctx.Err())
	}
}

func (p *executionPolicy) runWithSessionTimeout(ctx context.Context, run func(context.Context, string) executor.Result,
	prompt string, toolName string) phase.ExecutionResult {
	sessionTimeout := p.sessionTimeout()
	codexMode := p.cfg.isCodexExecutor()
	useTimeout := sessionTimeout > 0 && (codexMode || toolName == "claude")

	if !useTimeout {
		result := run(ctx, prompt)
		return phase.ExecutionResult{Result: result, TimedOut: p.handleIdleTimeout(result, toolName)}
	}

	childCtx, cancel := context.WithTimeout(ctx, sessionTimeout)
	defer cancel()

	result := run(childCtx, prompt)

	if childCtx.Err() != nil && ctx.Err() == nil {
		p.log.Print("warning: %s session timed out after %s, the agent may have started a blocking operation",
			toolName, sessionTimeout)
		result.Error = nil
		result.Signal = ""
		return phase.ExecutionResult{Result: result, TimedOut: true}
	}

	return phase.ExecutionResult{Result: result, TimedOut: p.handleIdleTimeout(result, toolName)}
}

func (p *executionPolicy) handleIdleTimeout(result executor.Result, toolName string) bool {
	if result.IdleTimedOut && result.Signal == "" {
		p.log.Print("warning: %s session idle timed out, no output activity detected", toolName)
		return true
	}
	return false
}

func (p *executionPolicy) sessionTimeout() time.Duration {
	if p.cfg.AppConfig == nil {
		return 0
	}
	return p.cfg.AppConfig.SessionTimeout
}
