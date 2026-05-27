package phase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/executor"
	"github.com/umputun/ralphex/pkg/status"
)

type planCreationPhaseTestOpts struct {
	cfg  Config
	exec Executor
	log  *mockLogger
}

func planCreationPhaseFromRunner(t *testing.T, opts planCreationPhaseTestOpts) (*planCreationPhase, *Runner, *mockLogger) {
	t.Helper()
	if opts.cfg.AppConfig == nil {
		opts.cfg.AppConfig = testAppConfig(t)
	}
	if opts.log == nil {
		opts.log = newMockLogger("progress-plan.txt")
	}
	if opts.exec == nil {
		opts.exec = newTaskPhaseMockExecutor(nil)
	}
	r := newTestRunner(testRunnerOpts{cfg: opts.cfg, log: opts.log, execs: Executors{Task: opts.exec}, holder: &status.PhaseHolder{}})
	phase, ok := r.phases.planCreation.(*planCreationPhase)
	require.True(t, ok)
	return phase, r, opts.log
}

func newPlanInputCollector(answers []string) *inputCollectorMock {
	idx := 0
	return &inputCollectorMock{
		AskQuestionFunc: func(_ context.Context, _ string, _ []string) (string, error) {
			if idx >= len(answers) {
				return "", errors.New("no more mock answers")
			}
			answer := answers[idx]
			idx++
			return answer, nil
		},
	}
}

func newPlanInputCollectorWithDraftReview(answers []string, draftResponses []struct {
	action   string
	feedback string
	err      error
}) *inputCollectorMock {
	answerIdx := 0
	draftIdx := 0
	return &inputCollectorMock{
		AskQuestionFunc: func(_ context.Context, _ string, _ []string) (string, error) {
			if answerIdx >= len(answers) {
				return "", errors.New("no more mock answers")
			}
			answer := answers[answerIdx]
			answerIdx++
			return answer, nil
		},
		AskDraftReviewFunc: func(_ context.Context, _ string, _ string) (string, string, error) {
			if draftIdx >= len(draftResponses) {
				return "", "", errors.New("no more mock draft responses")
			}
			resp := draftResponses[draftIdx]
			draftIdx++
			return resp.action, resp.feedback, resp.err
		},
	}
}

func TestPlanCreationPhase_Run_Success(t *testing.T) {
	exec := newTaskPhaseMockExecutor([]executor.Result{{Output: "plan created", Signal: status.PlanReady}})
	phase, runner, _ := planCreationPhaseFromRunner(t, planCreationPhaseTestOpts{
		cfg:  Config{PlanDescription: "add health check endpoint", MaxIterations: 50},
		exec: exec,
	})
	runner.SetInputCollector(newPlanInputCollector(nil))

	err := phase.Run(t.Context())

	require.NoError(t, err)
	assert.Len(t, exec.RunCalls(), 1)
}

func TestPlanCreationPhase_Run_WithQuestion(t *testing.T) {
	questionSignal := `Let me ask a question.

<<<RALPHEX:QUESTION>>>
{"question": "Which cache backend?", "options": ["Redis", "In-memory", "File-based"]}
<<<RALPHEX:END>>>`
	exec := newTaskPhaseMockExecutor([]executor.Result{
		{Output: questionSignal},
		{Output: "plan created", Signal: status.PlanReady},
	})
	inputCollector := newPlanInputCollector([]string{"Redis"})
	phase, runner, _ := planCreationPhaseFromRunner(t, planCreationPhaseTestOpts{
		cfg:  Config{PlanDescription: "add caching layer", MaxIterations: 50},
		exec: exec,
	})
	runner.SetInputCollector(inputCollector)

	err := phase.Run(t.Context())

	require.NoError(t, err)
	assert.Len(t, exec.RunCalls(), 2)
	assert.Len(t, inputCollector.AskQuestionCalls(), 1)
	assert.Equal(t, "Which cache backend?", inputCollector.AskQuestionCalls()[0].Question)
	assert.Equal(t, []string{"Redis", "In-memory", "File-based"}, inputCollector.AskQuestionCalls()[0].Options)
}

func TestPlanCreationPhase_Run_NoPlanDescription(t *testing.T) {
	phase, runner, _ := planCreationPhaseFromRunner(t, planCreationPhaseTestOpts{cfg: Config{}})
	runner.SetInputCollector(newPlanInputCollector(nil))

	err := phase.Run(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "plan description required")
}

func TestPlanCreationPhase_Run_NoInputCollector(t *testing.T) {
	phase, _, _ := planCreationPhaseFromRunner(t, planCreationPhaseTestOpts{cfg: Config{PlanDescription: "test"}})

	err := phase.Run(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "input collector required")
}

func TestPlanCreationPhase_Run_FailedSignal(t *testing.T) {
	exec := newTaskPhaseMockExecutor([]executor.Result{{Output: "error", Signal: status.Failed}})
	phase, runner, _ := planCreationPhaseFromRunner(t, planCreationPhaseTestOpts{
		cfg:  Config{PlanDescription: "test", MaxIterations: 50},
		exec: exec,
	})
	runner.SetInputCollector(newPlanInputCollector(nil))

	err := phase.Run(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "FAILED signal")
}

func TestPlanCreationPhase_Run_MaxIterations(t *testing.T) {
	exec := newTaskPhaseMockExecutor([]executor.Result{
		{Output: "exploring..."},
		{Output: "still exploring..."},
		{Output: "more exploring..."},
		{Output: "continuing..."},
		{Output: "still going..."},
	})
	phase, runner, _ := planCreationPhaseFromRunner(t, planCreationPhaseTestOpts{
		cfg:  Config{PlanDescription: "test", MaxIterations: 10},
		exec: exec,
	})
	runner.SetInputCollector(newPlanInputCollector(nil))

	err := phase.Run(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "max plan iterations")
}

func TestPlanCreationPhase_Run_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	phase, runner, _ := planCreationPhaseFromRunner(t, planCreationPhaseTestOpts{
		cfg: Config{PlanDescription: "test", MaxIterations: 50},
	})
	runner.SetInputCollector(newPlanInputCollector(nil))

	err := phase.Run(ctx)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestPlanCreationPhase_Run_ExecutionError(t *testing.T) {
	exec := newTaskPhaseMockExecutor([]executor.Result{{Error: errors.New("claude error")}})
	phase, runner, _ := planCreationPhaseFromRunner(t, planCreationPhaseTestOpts{
		cfg:  Config{PlanDescription: "test", MaxIterations: 50},
		exec: exec,
	})
	runner.SetInputCollector(newPlanInputCollector(nil))

	err := phase.Run(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "claude execution")
}

func TestPlanCreationPhase_Run_InputCollectorError(t *testing.T) {
	questionSignal := `<<<RALPHEX:QUESTION>>>
{"question": "Which backend?", "options": ["A", "B"]}
<<<RALPHEX:END>>>`
	exec := newTaskPhaseMockExecutor([]executor.Result{{Output: questionSignal}})
	inputCollector := &inputCollectorMock{AskQuestionFunc: func(_ context.Context, _ string, _ []string) (string, error) {
		return "", errors.New("input error")
	}}
	phase, runner, _ := planCreationPhaseFromRunner(t, planCreationPhaseTestOpts{
		cfg:  Config{PlanDescription: "test", MaxIterations: 50},
		exec: exec,
	})
	runner.SetInputCollector(inputCollector)

	err := phase.Run(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "collect answer")
}

func TestPlanCreationPhase_Run_PatternMatchError(t *testing.T) {
	exec := newTaskPhaseMockExecutor([]executor.Result{
		{Output: "hit limit", Error: &executor.PatternMatchError{Pattern: "hit limit", HelpCmd: "claude /usage"}},
	})
	phase, runner, _ := planCreationPhaseFromRunner(t, planCreationPhaseTestOpts{
		cfg:  Config{PlanDescription: "test", MaxIterations: 50},
		exec: exec,
	})
	runner.SetInputCollector(newPlanInputCollector(nil))

	err := phase.Run(t.Context())

	require.Error(t, err)
	var patternErr *executor.PatternMatchError
	require.ErrorAs(t, err, &patternErr)
}

func TestPlanCreationPhase_Run_PlanDraftAcceptFlow(t *testing.T) {
	planDraftSignal := `Let me create a plan for you.

<<<RALPHEX:PLAN_DRAFT>>>
# Test Plan

## Overview
This is a test plan.

## Tasks
- [ ] Task 1
<<<RALPHEX:END>>>`
	exec := newTaskPhaseMockExecutor([]executor.Result{
		{Output: planDraftSignal},
		{Output: "plan created", Signal: status.PlanReady},
	})
	inputCollector := newPlanInputCollectorWithDraftReview(nil, []struct {
		action   string
		feedback string
		err      error
	}{{action: "accept", feedback: "", err: nil}})
	phase, runner, _ := planCreationPhaseFromRunner(t, planCreationPhaseTestOpts{
		cfg:  Config{PlanDescription: "add health endpoint", MaxIterations: 50},
		exec: exec,
	})
	runner.SetInputCollector(inputCollector)

	err := phase.Run(t.Context())

	require.NoError(t, err)
	assert.Len(t, exec.RunCalls(), 2)
	assert.Len(t, inputCollector.AskDraftReviewCalls(), 1)
	assert.Contains(t, inputCollector.AskDraftReviewCalls()[0].PlanContent, "# Test Plan")
}

func TestPlanCreationPhase_Run_PlanDraftReviseFlow(t *testing.T) {
	planDraftSignal := `<<<RALPHEX:PLAN_DRAFT>>>
# Initial Plan
## Tasks
- [ ] Task 1
<<<RALPHEX:END>>>`
	revisedDraftSignal := `<<<RALPHEX:PLAN_DRAFT>>>
# Revised Plan
## Tasks
- [ ] Task 1
- [ ] Task 2 (added per feedback)
<<<RALPHEX:END>>>`
	exec := newTaskPhaseMockExecutor([]executor.Result{
		{Output: planDraftSignal},
		{Output: revisedDraftSignal},
		{Output: "plan created", Signal: status.PlanReady},
	})
	inputCollector := newPlanInputCollectorWithDraftReview(nil, []struct {
		action   string
		feedback string
		err      error
	}{
		{action: "revise", feedback: "please add a second task", err: nil},
		{action: "accept", feedback: "", err: nil},
	})
	phase, runner, _ := planCreationPhaseFromRunner(t, planCreationPhaseTestOpts{
		cfg:  Config{PlanDescription: "add health endpoint", MaxIterations: 50},
		exec: exec,
	})
	runner.SetInputCollector(inputCollector)

	err := phase.Run(t.Context())

	require.NoError(t, err)
	assert.Len(t, exec.RunCalls(), 3)
	assert.Len(t, inputCollector.AskDraftReviewCalls(), 2)
	assert.Contains(t, exec.RunCalls()[1].Prompt, "please add a second task")
	assert.Contains(t, exec.RunCalls()[1].Prompt, "PREVIOUS DRAFT FEEDBACK")
}

func TestPlanCreationPhase_Run_PlanDraftRejectFlow(t *testing.T) {
	planDraftSignal := `<<<RALPHEX:PLAN_DRAFT>>>
# Test Plan
## Tasks
- [ ] Task 1
<<<RALPHEX:END>>>`
	exec := newTaskPhaseMockExecutor([]executor.Result{{Output: planDraftSignal}})
	inputCollector := newPlanInputCollectorWithDraftReview(nil, []struct {
		action   string
		feedback string
		err      error
	}{{action: "reject", feedback: "", err: nil}})
	phase, runner, _ := planCreationPhaseFromRunner(t, planCreationPhaseTestOpts{
		cfg:  Config{PlanDescription: "add health endpoint", MaxIterations: 50},
		exec: exec,
	})
	runner.SetInputCollector(inputCollector)

	err := phase.Run(t.Context())

	require.Error(t, err)
	require.ErrorIs(t, err, ErrUserRejectedPlan)
	assert.Len(t, exec.RunCalls(), 1)
	assert.Len(t, inputCollector.AskDraftReviewCalls(), 1)
}

func TestPlanCreationPhase_Run_PlanDraftAskDraftReviewError(t *testing.T) {
	planDraftSignal := `<<<RALPHEX:PLAN_DRAFT>>>
# Test Plan
<<<RALPHEX:END>>>`
	exec := newTaskPhaseMockExecutor([]executor.Result{{Output: planDraftSignal}})
	inputCollector := newPlanInputCollectorWithDraftReview(nil, []struct {
		action   string
		feedback string
		err      error
	}{{err: errors.New("draft review error")}})
	phase, runner, _ := planCreationPhaseFromRunner(t, planCreationPhaseTestOpts{
		cfg:  Config{PlanDescription: "test", MaxIterations: 50},
		exec: exec,
	})
	runner.SetInputCollector(inputCollector)

	err := phase.Run(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "collect draft review")
}

func TestPlanCreationPhase_Run_PlanDraftMalformedSignal(t *testing.T) {
	malformedDraftSignal := `<<<RALPHEX:PLAN_DRAFT>>>
# Test Plan
This content has no END marker`
	exec := newTaskPhaseMockExecutor([]executor.Result{
		{Output: malformedDraftSignal},
		{Output: "plan created", Signal: status.PlanReady},
	})
	phase, runner, log := planCreationPhaseFromRunner(t, planCreationPhaseTestOpts{
		cfg:  Config{PlanDescription: "test", MaxIterations: 50},
		exec: exec,
	})
	runner.SetInputCollector(newPlanInputCollectorWithDraftReview(nil, nil))

	err := phase.Run(t.Context())

	require.NoError(t, err)
	assertLogContains(t, log, "warning")
}

func TestPlanCreationPhase_Run_MalformedQuestionSignal(t *testing.T) {
	malformedQuestionSignal := `<<<RALPHEX:QUESTION>>>
{"question": "Which backend?"`
	exec := newTaskPhaseMockExecutor([]executor.Result{
		{Output: malformedQuestionSignal},
		{Output: "plan created", Signal: status.PlanReady},
	})
	phase, runner, log := planCreationPhaseFromRunner(t, planCreationPhaseTestOpts{
		cfg:  Config{PlanDescription: "test", MaxIterations: 50},
		exec: exec,
	})
	runner.SetInputCollector(newPlanInputCollector(nil))

	err := phase.Run(t.Context())

	require.NoError(t, err)
	assertLogContains(t, log, "warning")
}

func TestPlanCreationPhase_Run_PlanDraftWithQuestionThenDraft(t *testing.T) {
	questionSignal := `<<<RALPHEX:QUESTION>>>
{"question": "Which framework?", "options": ["Gin", "Chi", "Echo"]}
<<<RALPHEX:END>>>`
	planDraftSignal := `<<<RALPHEX:PLAN_DRAFT>>>
# Plan with Gin
## Tasks
- [ ] Set up Gin router
<<<RALPHEX:END>>>`
	exec := newTaskPhaseMockExecutor([]executor.Result{
		{Output: questionSignal},
		{Output: planDraftSignal},
		{Output: "plan created", Signal: status.PlanReady},
	})
	inputCollector := newPlanInputCollectorWithDraftReview([]string{"Gin"}, []struct {
		action   string
		feedback string
		err      error
	}{{action: "accept", feedback: "", err: nil}})
	phase, runner, _ := planCreationPhaseFromRunner(t, planCreationPhaseTestOpts{
		cfg:  Config{PlanDescription: "add API endpoints", MaxIterations: 50},
		exec: exec,
	})
	runner.SetInputCollector(inputCollector)

	err := phase.Run(t.Context())

	require.NoError(t, err)
	assert.Len(t, exec.RunCalls(), 3)
	assert.Len(t, inputCollector.AskQuestionCalls(), 1)
	assert.Len(t, inputCollector.AskDraftReviewCalls(), 1)
}

func TestPlanCreationPhase_Run_TimeoutSkipsOutputParsing(t *testing.T) {
	questionSignal := "<<<RALPHEX:QUESTION>>>\n" +
		`{"question": "Which backend?", "options": ["Redis", "Memcached"]}` + "\n<<<RALPHEX:END>>>"
	exec := newTaskPhaseMockExecutor(nil)
	inputCollector := newPlanInputCollector(nil)
	phase, runner, log := planCreationPhaseFromRunner(t, planCreationPhaseTestOpts{
		cfg:  Config{PlanDescription: "add caching", MaxIterations: 50},
		exec: exec,
	})
	phase.policy = newScriptedTestPolicy(log,
		ExecutionResult{Result: executor.Result{Output: questionSignal}, TimedOut: true},
		ExecutionResult{Result: executor.Result{Output: "plan created", Signal: status.PlanReady}},
	)
	runner.SetInputCollector(inputCollector)

	err := phase.Run(t.Context())

	require.NoError(t, err)
	assert.Len(t, exec.RunCalls(), 2)
	assert.Empty(t, inputCollector.AskQuestionCalls())
	assertLogContains(t, log, "plan creation session timed out")
}

func TestPlanCreationPhase_Run_TimeoutPreservesRevisionFeedback(t *testing.T) {
	planDraftSignal := "<<<RALPHEX:PLAN_DRAFT>>>\n# Test Plan\n## Tasks\n- [ ] Task 1\n<<<RALPHEX:END>>>"
	exec := newTaskPhaseMockExecutor(nil)
	inputCollector := newPlanInputCollectorWithDraftReview(nil, []struct {
		action   string
		feedback string
		err      error
	}{{action: "revise", feedback: "please add error handling task", err: nil}})
	phase, runner, log := planCreationPhaseFromRunner(t, planCreationPhaseTestOpts{
		cfg:  Config{PlanDescription: "add caching", MaxIterations: 50},
		exec: exec,
	})
	phase.policy = newScriptedTestPolicy(log,
		ExecutionResult{Result: executor.Result{Output: planDraftSignal}},
		ExecutionResult{Result: executor.Result{Output: "partial revision"}, TimedOut: true},
		ExecutionResult{Result: executor.Result{Output: "plan created", Signal: status.PlanReady}},
	)
	runner.SetInputCollector(inputCollector)

	err := phase.Run(t.Context())

	require.NoError(t, err)
	assert.Len(t, exec.RunCalls(), 3)
	assert.Contains(t, exec.RunCalls()[1].Prompt, "please add error handling task")
	assert.Contains(t, exec.RunCalls()[2].Prompt, "please add error handling task")
	assert.Contains(t, exec.RunCalls()[2].Prompt, "PREVIOUS DRAFT FEEDBACK")
}
