package phase

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/config"
	"github.com/umputun/ralphex/pkg/executor"
	"github.com/umputun/ralphex/pkg/plan"
	"github.com/umputun/ralphex/pkg/status"
)

type taskPhaseTestOpts struct {
	cfg        Config
	exec       Executor
	log        *mockLogger
	planFile   string
	retryCount int
}

func taskPhaseFromRunner(t *testing.T, opts taskPhaseTestOpts) *taskPhase {
	t.Helper()
	if opts.cfg.AppConfig == nil {
		opts.cfg.AppConfig = testAppConfig(t)
	}
	r := newTestRunner(testRunnerOpts{
		cfg: opts.cfg, log: opts.log, execs: Executors{Task: opts.exec, External: newTaskPhaseMockExecutor(nil)},
		holder: &status.PhaseHolder{}, planFile: opts.planFile, retryCount: opts.retryCount,
	})
	phase, ok := r.phases.task.(*taskPhase)
	require.True(t, ok)
	return phase
}

func newTaskPhaseMockExecutor(results []executor.Result) *executorMock {
	callNum := 0
	return &executorMock{RunFunc: func(_ context.Context, _ string) executor.Result {
		if callNum < len(results) {
			result := results[callNum]
			callNum++
			return result
		}
		callNum++
		return executor.Result{}
	}}
}

func TestTaskPhase_Run_FailedSignal(t *testing.T) {
	planFile := writeTaskPhasePlan(t, "# Plan\n### Task 1: first\n- [ ] todo")
	log := newMockLogger("progress.txt")
	exec := newTaskPhaseMockExecutor([]executor.Result{{Signal: status.Failed}, {Signal: status.Failed}})
	phase := taskPhaseFromRunner(t, taskPhaseTestOpts{cfg: Config{MaxIterations: 10}, planFile: planFile, exec: exec, log: log})

	err := phase.Run(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "FAILED signal")
}

func TestTaskPhase_Run_MaxIterations(t *testing.T) {
	planFile := writeTaskPhasePlan(t, "# Plan\n### Task 1: first\n- [ ] todo")
	log := newMockLogger("progress.txt")
	exec := newTaskPhaseMockExecutor([]executor.Result{{Output: "working"}, {Output: "still working"}, {Output: "more work"}})
	phase := taskPhaseFromRunner(t, taskPhaseTestOpts{cfg: Config{MaxIterations: 3}, planFile: planFile, exec: exec, log: log})

	err := phase.Run(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "max iterations")
}

func TestTaskPhase_Run_ContextCanceled(t *testing.T) {
	planFile := writeTaskPhasePlan(t, "# Plan\n### Task 1: first\n- [ ] todo")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	phase := taskPhaseFromRunner(t, taskPhaseTestOpts{cfg: Config{MaxIterations: 10}, planFile: planFile, exec: newTaskPhaseMockExecutor(nil), log: newMockLogger("")})

	err := phase.Run(ctx)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestTaskPhase_PlanState(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantOpen bool
		wantPos  int
	}{
		{name: "all tasks completed", content: "# Plan\n### Task 1: first\n- [x] done", wantOpen: false, wantPos: 0},
		{name: "has uncompleted task", content: "# Plan\n### Task 1: first\n- [x] done\n### Task 2: second\n- [ ] todo", wantOpen: true, wantPos: 2},
		{name: "success criteria ignored", content: "# Plan\n## Success criteria\n- [ ] Manual\n### Task 1: first\n- [x] done", wantOpen: false, wantPos: 0},
		{name: "malformed plan has open checkbox", content: "# Plan\n- [x] done\n- [ ] todo", wantOpen: true, wantPos: 0},
		{name: "description checkbox skipped", content: "# Plan\n### Task 1: format\n- [x] done\n- [ ] use [ ] example\n### Task 2: build\n- [ ] build", wantOpen: true, wantPos: 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			planFile := writeTaskPhasePlan(t, tc.content)
			phase := taskPhaseFromRunner(t, taskPhaseTestOpts{cfg: Config{}, planFile: planFile, exec: newTaskPhaseMockExecutor(nil), log: newMockLogger("")})

			assert.Equal(t, tc.wantOpen, phase.HasUncompletedTasks())
			assert.Equal(t, tc.wantPos, phase.NextPlanTaskPosition())
		})
	}
}

func TestTaskPhase_ValidatePlanHasTasks(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr bool
	}{
		{name: "task section", content: "# Plan\n### Task 1: first\n- [ ] todo", wantErr: false},
		{name: "iteration section", content: "# Plan\n### Iteration 1: first\n- [ ] todo", wantErr: false},
		{name: "no task sections", content: "# Overview\n## Goals\n- describe architecture", wantErr: true},
		{name: "empty file", content: "", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			planFile := writeTaskPhasePlan(t, tc.content)
			phase := taskPhaseFromRunner(t, taskPhaseTestOpts{cfg: Config{}, planFile: planFile, exec: newTaskPhaseMockExecutor(nil), log: newMockLogger("")})

			err := phase.ValidatePlanHasTasks()
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "no executable task sections")
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestTaskPhase_PlanState_CompletedDir(t *testing.T) {
	tmpDir := t.TempDir()
	planDir := filepath.Join(tmpDir, "docs", "plans")
	completedDir := filepath.Join(planDir, "completed")
	require.NoError(t, os.MkdirAll(completedDir, 0o700))
	originalPath := filepath.Join(planDir, "20260525-foo.md")
	completedPath := filepath.Join(completedDir, "20260525-foo.md")
	require.NoError(t, os.WriteFile(completedPath, []byte("# Plan\n### Task 1: first\n- [ ] todo"), 0o600))
	phase := taskPhaseFromRunner(t, taskPhaseTestOpts{cfg: Config{}, planFile: originalPath, exec: newTaskPhaseMockExecutor(nil), log: newMockLogger("")})

	assert.True(t, phase.HasUncompletedTasks())
}

func TestTaskPhase_PlanState_MissingFile(t *testing.T) {
	missingFile := filepath.Join(t.TempDir(), "missing.md")
	phase := taskPhaseFromRunner(t, taskPhaseTestOpts{cfg: Config{}, planFile: missingFile, exec: newTaskPhaseMockExecutor(nil), log: newMockLogger("")})

	assert.False(t, phase.HasUncompletedTasks())
	assert.Equal(t, 0, phase.NextPlanTaskPosition())
}

func TestTaskPhase_NextPlanTaskPosition_EmptyPlanFile(t *testing.T) {
	phase := taskPhaseFromRunner(t, taskPhaseTestOpts{cfg: Config{}, exec: newTaskPhaseMockExecutor(nil), log: newMockLogger("")})

	assert.Equal(t, 0, phase.NextPlanTaskPosition())
}

func TestTaskPhase_Run_TaskRetryCount(t *testing.T) {
	planFile := writeTaskPhasePlan(t, "# Plan\n### Task 1: first\n- [ ] todo")
	exec := newTaskPhaseMockExecutor([]executor.Result{
		{Output: "error", Signal: status.Failed},
		{Output: "error", Signal: status.Failed},
		{Output: "error", Signal: status.Failed},
	})
	phase := taskPhaseFromRunner(t, taskPhaseTestOpts{
		cfg:        Config{MaxIterations: 10},
		exec:       exec,
		log:        newMockLogger(""),
		planFile:   planFile,
		retryCount: 2,
	})

	err := phase.Run(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "FAILED signal")
	assert.Len(t, exec.RunCalls(), 3)
}

func TestTaskPhase_Run_UsesPlanTaskPosition(t *testing.T) {
	planContent := "# Plan\n### Task 1: setup\n- [x] done\n### Task 2: build\n- [ ] build it"
	planFile := writeTaskPhasePlan(t, planContent)
	log := newMockLogger("progress.txt")
	callCount := 0
	exec := &executorMock{RunFunc: func(_ context.Context, _ string) executor.Result {
		callCount++
		if callCount == 1 {
			updated := strings.ReplaceAll(planContent, "- [ ] build it", "- [x] build it")
			require.NoError(t, os.WriteFile(planFile, []byte(updated), 0o600))
			return executor.Result{Output: "task done", Signal: status.Completed}
		}
		return executor.Result{Error: errors.New("no more mock results")}
	}}
	phase := taskPhaseFromRunner(t, taskPhaseTestOpts{cfg: Config{MaxIterations: 50}, planFile: planFile, exec: exec, log: log})

	err := phase.Run(t.Context())

	require.NoError(t, err)
	assertTaskSectionPrinted(t, log, 2)
}

func TestTaskPhase_Run_BreakWithPauseResume(t *testing.T) {
	planFile := writeTaskPhasePlan(t, "# Plan\n### Task 1:\n- [x] something")
	breakCh := make(chan struct{}, 1)
	pauseCalls := 0
	exec := &executorMock{RunFunc: func(ctx context.Context, _ string) executor.Result {
		if pauseCalls == 0 {
			breakCh <- struct{}{}
			<-ctx.Done()
			return executor.Result{Error: ctx.Err()}
		}
		return executor.Result{Output: "done", Signal: status.Completed}
	}}
	log := newMockLogger("progress.txt")
	runner := newTestRunner(testRunnerOpts{
		cfg:      Config{MaxIterations: 10, AppConfig: testAppConfig(t)},
		log:      log,
		execs:    Executors{Task: exec, External: newTaskPhaseMockExecutor(nil)},
		holder:   &status.PhaseHolder{},
		planFile: planFile,
	})
	phase, ok := runner.phases.task.(*taskPhase)
	require.True(t, ok)
	runner.SetBreakCh(breakCh)
	runner.SetPauseHandler(func(_ context.Context) bool { pauseCalls++; return true })

	err := phase.Run(t.Context())

	require.NoError(t, err)
	assert.Equal(t, 1, pauseCalls)
	assert.Len(t, exec.RunCalls(), 2)
}

func TestTaskPhase_Run_StickySignalDrainAfterPause(t *testing.T) {
	planFile := writeTaskPhasePlan(t, "# Plan\n### Task 1:\n- [x] something")
	breakCh := make(chan struct{}, 1)
	pauseCalls := 0
	runCalls := 0
	exec := &executorMock{RunFunc: func(ctx context.Context, _ string) executor.Result {
		runCalls++
		if runCalls == 1 {
			breakCh <- struct{}{}
			<-ctx.Done()
			return executor.Result{Error: ctx.Err()}
		}
		return executor.Result{Output: "done", Signal: status.Completed}
	}}
	phase := taskPhaseFromRunner(t, taskPhaseTestOpts{cfg: Config{MaxIterations: 10}, planFile: planFile, exec: exec, log: newMockLogger("")})
	phase.deps.BreakCh = breakCh
	phase.deps.PauseHandler = func(_ context.Context) bool {
		pauseCalls++
		breakCh <- struct{}{}
		return true
	}

	err := phase.Run(t.Context())

	require.NoError(t, err)
	assert.Equal(t, 1, pauseCalls)
	assert.Len(t, exec.RunCalls(), 2)
}

func TestTaskPhase_Run_BreakAbort(t *testing.T) {
	planFile := writeTaskPhasePlan(t, "# Plan\n### Task 1:\n- [ ] something")
	breakCh := make(chan struct{}, 1)
	exec := &executorMock{RunFunc: func(ctx context.Context, _ string) executor.Result {
		breakCh <- struct{}{}
		<-ctx.Done()
		return executor.Result{Error: ctx.Err()}
	}}
	phase := taskPhaseFromRunner(t, taskPhaseTestOpts{cfg: Config{MaxIterations: 10}, planFile: planFile, exec: exec, log: newMockLogger("")})
	phase.deps.BreakCh = breakCh

	err := phase.Run(t.Context())

	require.ErrorIs(t, err, ErrUserAborted)
	assert.Len(t, exec.RunCalls(), 1)
}

func TestTaskPhase_Run_TimeoutBacksOffThenContinues(t *testing.T) {
	planFile := writeTaskPhasePlan(t, "# Plan\n### Task 1: first\n- [x] done")
	exec := newTaskPhaseMockExecutor(nil)
	phase := taskPhaseFromRunner(t, taskPhaseTestOpts{
		cfg:      Config{MaxIterations: 10},
		exec:     exec,
		log:      newMockLogger(""),
		planFile: planFile,
	})
	policy := newScriptedTestPolicy(phase.log,
		ExecutionResult{TimedOut: true},
		ExecutionResult{Result: executor.Result{Output: "task done", Signal: status.Completed}},
	)
	phase.policy = policy

	err := phase.Run(t.Context())

	require.NoError(t, err)
	assert.Len(t, exec.RunCalls(), 2)
	assert.Equal(t, []time.Duration{retryBackoff}, policy.sleepCalls, "timeout retry waits the backoff once")
}

func writeTaskPhasePlan(t *testing.T, content string) string {
	t.Helper()
	planFile := filepath.Join(t.TempDir(), "plan.md")
	require.NoError(t, os.WriteFile(planFile, []byte(content), 0o600))
	return planFile
}

func assertTaskSectionPrinted(t *testing.T, log *mockLogger, iteration int) {
	t.Helper()
	for _, call := range log.PrintSectionCalls() {
		if call.Section.Iteration == iteration && strings.Contains(call.Section.Label, "task iteration") {
			return
		}
	}
	t.Fatalf("task section %d was not printed", iteration)
}

type taskPhase = TaskPhase
type reviewPhase = ReviewPhase
type externalReviewPhase = ExternalReviewPhase
type finalizePhase = FinalizePhase
type planCreationPhase = PlanCreationPhase

type Executors struct {
	Task     Executor
	Review   Executor
	External Executor
	Custom   *executor.CustomExecutor
}

type Runner struct {
	phases runnerPhases
	deps   *Deps
}

type runnerPhases struct {
	task         any
	review       any
	external     any
	finalize     any
	planCreation any
}

type testRunnerOpts struct {
	cfg            Config
	log            *mockLogger
	execs          Executors
	holder         *status.PhaseHolder
	planFile       string
	iterationDelay time.Duration
	retryCount     int
}

func newTestRunner(opts testRunnerOpts) *Runner {
	iterDelay := opts.iterationDelay
	retryCount := 1
	if opts.retryCount > 0 {
		retryCount = opts.retryCount
	}
	review := opts.execs.Review
	if review == nil {
		review = opts.execs.Task
	}
	deps := &Deps{}
	breaks := NewBreakController(deps)
	git := NewGitState(deps, opts.log)
	policy := newTestPolicy(opts.cfg, opts.log)
	prompts := testPrompts{}
	locator := testLocator{path: opts.planFile}

	task := NewTaskPhase(TaskPhaseOpts{
		Cfg: opts.cfg, Log: opts.log, Exec: opts.execs.Task, Policy: policy, Prompts: prompts,
		Locator: locator, Deps: deps, Breaks: breaks, IterationDelay: iterDelay, RetryCount: retryCount,
	})
	reviewPhase := NewReviewPhase(ReviewPhaseOpts{
		Cfg: opts.cfg, Log: opts.log, Exec: review, Policy: policy, Prompts: prompts,
		Git: git, PhaseHolder: opts.holder, IterationDelay: iterDelay,
	})
	external := NewExternalReviewPhase(ExternalReviewPhaseOpts{
		Cfg: opts.cfg, Log: opts.log, External: opts.execs.External, Custom: opts.execs.Custom, Review: review,
		Policy: policy, Prompts: prompts, Breaks: breaks, Git: git, PhaseHolder: opts.holder, IterationDelay: iterDelay,
	})
	finalize := NewFinalizePhase(FinalizePhaseOpts{Cfg: opts.cfg, Log: opts.log, Exec: review, Policy: policy, Prompts: prompts, PhaseHolder: opts.holder})
	planCreation := NewPlanCreationPhase(PlanCreationPhaseOpts{
		Cfg: opts.cfg, Log: opts.log, Exec: opts.execs.Task, Policy: policy, Prompts: prompts,
		Deps: deps, PhaseHolder: opts.holder, IterationDelay: iterDelay,
	})

	return &Runner{phases: runnerPhases{task: task, review: reviewPhase, external: external, finalize: finalize, planCreation: planCreation}, deps: deps}
}

func (r *Runner) SetInputCollector(c InputCollector) {
	r.deps.InputCollector = c
}

func (r *Runner) SetGitChecker(g GitChecker) {
	r.deps.Git = g
}

func (r *Runner) SetBreakCh(ch <-chan struct{}) {
	r.deps.BreakCh = ch
}

func (r *Runner) SetPauseHandler(fn func(context.Context) bool) {
	r.deps.PauseHandler = fn
}

func testAppConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load(t.TempDir())
	require.NoError(t, err)
	return cfg
}

type printCall struct {
	Format string
	Args   []any
}

type printSectionCall struct {
	Section status.Section
}

type printAlignedCall struct {
	Text string
}

type mockLogger struct {
	mu                sync.Mutex
	path              string
	printCalls        []printCall
	printSectionCalls []printSectionCall
	printAlignedCalls []printAlignedCall
}

func newMockLogger(path string) *mockLogger {
	return &mockLogger{path: path}
}

func (l *mockLogger) Print(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.printCalls = append(l.printCalls, printCall{Format: format, Args: append([]any(nil), args...)})
}

func (l *mockLogger) PrintRaw(_ string, _ ...any) {}

func (l *mockLogger) PrintSection(section status.Section) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.printSectionCalls = append(l.printSectionCalls, printSectionCall{Section: section})
}

func (l *mockLogger) PrintAligned(text string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.printAlignedCalls = append(l.printAlignedCalls, printAlignedCall{Text: text})
}

func (l *mockLogger) LogQuestion(_ string, _ []string) {}

func (l *mockLogger) LogAnswer(_ string) {}

func (l *mockLogger) LogDraftReview(_, _ string) {}

func (l *mockLogger) Path() string { return l.path }

func (l *mockLogger) PrintCalls() []printCall {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]printCall(nil), l.printCalls...)
}

func (l *mockLogger) PrintSectionCalls() []printSectionCall {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]printSectionCall(nil), l.printSectionCalls...)
}

func (l *mockLogger) PrintAlignedCalls() []printAlignedCall {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]printAlignedCall(nil), l.printAlignedCalls...)
}

type executorRunCall struct {
	Ctx    context.Context
	Prompt string
}

type executorMock struct {
	mu       sync.Mutex
	RunFunc  func(context.Context, string) executor.Result
	runCalls []executorRunCall
}

func (m *executorMock) Run(ctx context.Context, prompt string) executor.Result {
	m.mu.Lock()
	m.runCalls = append(m.runCalls, executorRunCall{Ctx: ctx, Prompt: prompt})
	fn := m.RunFunc
	m.mu.Unlock()

	if fn == nil {
		return executor.Result{}
	}
	return fn(ctx, prompt)
}

func (m *executorMock) RunCalls() []executorRunCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]executorRunCall(nil), m.runCalls...)
}

type askQuestionCall struct {
	Ctx      context.Context
	Question string
	Options  []string
}

type askDraftReviewCall struct {
	Ctx         context.Context
	Question    string
	PlanContent string
}

type gitCheckerMock struct {
	mu                  sync.Mutex
	HeadHashFunc        func() (string, error)
	DiffFingerprintFunc func() (string, error)
	headHashCalls       int
}

func (m *gitCheckerMock) HeadHash() (string, error) {
	m.mu.Lock()
	m.headHashCalls++
	fn := m.HeadHashFunc
	m.mu.Unlock()

	if fn == nil {
		return "", nil
	}
	return fn()
}

func (m *gitCheckerMock) DiffFingerprint() (string, error) {
	m.mu.Lock()
	fn := m.DiffFingerprintFunc
	m.mu.Unlock()

	if fn == nil {
		return "", nil
	}
	return fn()
}

func (m *gitCheckerMock) HeadHashCalls() []struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	return make([]struct{}, m.headHashCalls)
}

type inputCollectorMock struct {
	mu                 sync.Mutex
	AskQuestionFunc    func(context.Context, string, []string) (string, error)
	AskDraftReviewFunc func(context.Context, string, string) (string, string, error)
	askQuestionCalls   []askQuestionCall
	askDraftCalls      []askDraftReviewCall
}

func (m *inputCollectorMock) AskQuestion(ctx context.Context, question string, options []string) (string, error) {
	m.mu.Lock()
	m.askQuestionCalls = append(m.askQuestionCalls, askQuestionCall{
		Ctx: ctx, Question: question, Options: append([]string(nil), options...),
	})
	fn := m.AskQuestionFunc
	m.mu.Unlock()

	if fn == nil {
		return "", nil
	}
	return fn(ctx, question, options)
}

func (m *inputCollectorMock) AskDraftReview(ctx context.Context, question, planContent string) (string, string, error) {
	m.mu.Lock()
	m.askDraftCalls = append(m.askDraftCalls, askDraftReviewCall{Ctx: ctx, Question: question, PlanContent: planContent})
	fn := m.AskDraftReviewFunc
	m.mu.Unlock()

	if fn == nil {
		return "", "", nil
	}
	return fn(ctx, question, planContent)
}

func (m *inputCollectorMock) AskQuestionCalls() []askQuestionCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]askQuestionCall(nil), m.askQuestionCalls...)
}

func (m *inputCollectorMock) AskDraftReviewCalls() []askDraftReviewCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]askDraftReviewCall(nil), m.askDraftCalls...)
}

type testPolicy struct {
	log        Logger
	results    []ExecutionResult
	sleepCalls []time.Duration
}

func newTestPolicy(_ Config, log Logger) *testPolicy {
	return &testPolicy{log: log}
}

func newScriptedTestPolicy(log Logger, results ...ExecutionResult) *testPolicy {
	return &testPolicy{log: log, results: results}
}

func (p *testPolicy) Run(ctx context.Context, run func(context.Context, string) executor.Result, prompt, _ string) ExecutionResult {
	actual := run(ctx, prompt)
	if len(p.results) == 0 {
		return ExecutionResult{Result: actual}
	}

	result := p.results[0]
	p.results = p.results[1:]
	return result
}

func (p *testPolicy) HandlePatternMatchError(err error, tool string) error {
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

func (p *testPolicy) Sleep(ctx context.Context, d time.Duration) error {
	p.sleepCalls = append(p.sleepCalls, d)
	if ctx.Err() != nil {
		return fmt.Errorf("sleep interrupted: %w", ctx.Err())
	}
	return nil
}

type testPrompts struct{}

func (testPrompts) TaskPrompt() string                         { return "task prompt" }
func (testPrompts) FirstReviewPrompt() string                  { return "first review prompt" }
func (testPrompts) SecondReviewPrompt(prefix string) string    { return prefix + "second review prompt" }
func (testPrompts) CodexReviewPrompt(_ bool, _ string) string  { return "git diff\ncodex review prompt" }
func (testPrompts) CodexEvaluationPrompt(output string) string { return "codex eval: " + output }
func (testPrompts) CustomReviewPrompt(_ bool, _ string) string {
	return "git diff\ncustom review prompt"
}
func (testPrompts) CustomEvaluationPrompt(output string) string { return "custom eval: " + output }
func (testPrompts) PlanPrompt() string                          { return "plan prompt" }
func (testPrompts) FinalizePrompt() string                      { return "finalize prompt" }

type testLocator struct {
	path string
}

func (l testLocator) Path() string {
	if l.path == "" {
		return ""
	}
	if _, err := os.Stat(l.path); err == nil {
		return l.path
	}

	dir := filepath.Dir(l.path)
	base := filepath.Base(l.path)
	altBase := plan.AltDateBasename(base)
	if altBase != "" {
		path := filepath.Join(dir, altBase)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	completedPath := filepath.Join(dir, "completed", base)
	if _, err := os.Stat(completedPath); err == nil {
		return completedPath
	}
	if altBase != "" {
		path := filepath.Join(dir, "completed", altBase)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return l.path
}
