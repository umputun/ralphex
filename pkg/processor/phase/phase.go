package phase

import (
	"context"
	"errors"
	"time"

	"github.com/umputun/ralphex/pkg/config"
	"github.com/umputun/ralphex/pkg/executor"
	"github.com/umputun/ralphex/pkg/status"
)

const (
	minReviewIterations    = 3
	reviewIterationDivisor = 10
	minCodexIterations     = 3
	codexIterationDivisor  = 5
	maxCodexSummaryLen     = 5000
	minPlanIterations      = 5
	planIterationDivisor   = 5
)

// Config contains the runner settings consumed by phase engines.
type Config struct {
	PlanDescription       string
	MaxIterations         int
	MaxExternalIterations int
	ReviewPatience        int
	CodexEnabled          bool
	ExternalReviewToolSet bool
	FinalizeEnabled       bool
	AppConfig             *config.Config
}

func (c Config) isCodexExecutor() bool {
	return c.AppConfig != nil && c.AppConfig.Executor == config.ExecutorCodex
}

// Executor runs a phase prompt and returns the executor result.
type Executor interface {
	Run(ctx context.Context, prompt string) executor.Result
}

// Logger records plain phase progress messages.
type Logger interface {
	Print(format string, args ...any)
}

// TaskLogger records task phase progress.
type TaskLogger interface {
	Logger
	PrintRaw(format string, args ...any)
	PrintSection(section status.Section)
}

// ReviewLogger records internal review phase progress.
type ReviewLogger interface {
	Logger
	PrintSection(section status.Section)
}

// ExternalReviewLogger records external review phase progress and summaries.
type ExternalReviewLogger interface {
	Logger
	PrintSection(section status.Section)
	PrintAligned(text string)
}

// FinalizeLogger records finalize phase progress.
type FinalizeLogger interface {
	Logger
	PrintSection(section status.Section)
}

// PlanCreationLogger records interactive plan creation progress and Q&A history.
type PlanCreationLogger interface {
	Logger
	PrintRaw(format string, args ...any)
	PrintSection(section status.Section)
	LogQuestion(question string, options []string)
	LogAnswer(answer string)
	LogDraftReview(action string, feedback string)
}

// InputCollector collects user input for interactive plan creation.
type InputCollector interface {
	AskQuestion(ctx context.Context, question string, options []string) (string, error)
	AskDraftReview(ctx context.Context, question string, planContent string) (action string, feedback string, err error)
}

// GitChecker reports git state used by review loops.
type GitChecker interface {
	HeadHash() (string, error)
	DiffFingerprint() (string, error)
}

// Deps holds late-bound dependencies shared by phase engines.
type Deps struct {
	Git            GitChecker
	InputCollector InputCollector
	BreakCh        <-chan struct{}
	PauseHandler   func(ctx context.Context) bool
}

// ExecutionResult is the execution output plus phase-level timeout metadata.
type ExecutionResult struct {
	Result   executor.Result
	TimedOut bool
}

// Policy applies execution retry, timeout, and sleep behavior for phase engines.
type Policy interface {
	Run(ctx context.Context, run func(context.Context, string) executor.Result, prompt string, toolName string) ExecutionResult
	HandlePatternMatchError(err error, tool string) error
	Sleep(ctx context.Context, d time.Duration) error
}

// TaskPrompts renders task phase prompts.
type TaskPrompts interface {
	TaskPrompt() string
}

// ReviewPrompts renders internal review prompts.
type ReviewPrompts interface {
	FirstReviewPrompt() string
	SecondReviewPrompt(prefix string) string
}

// ExternalReviewPrompts renders external review and evaluation prompts.
type ExternalReviewPrompts interface {
	CodexReviewPrompt(isFirst bool, claudeResponse string) string
	CodexEvaluationPrompt(codexOutput string) string
	CustomReviewPrompt(isFirst bool, claudeResponse string) string
	CustomEvaluationPrompt(customOutput string) string
}

// PlanCreationPrompts renders interactive plan creation prompts.
type PlanCreationPrompts interface {
	PlanPrompt() string
}

// FinalizePrompts renders finalize prompts.
type FinalizePrompts interface {
	FinalizePrompt() string
}

// Locator resolves the current plan file path.
type Locator interface {
	Path() string
}

// ErrUserAborted is returned when a user aborts task execution after a break signal.
var ErrUserAborted = errors.New("user aborted")

// ErrUserRejectedPlan is returned when a user rejects a plan draft.
var ErrUserRejectedPlan = errors.New("user rejected plan")
