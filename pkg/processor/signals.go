package processor

import "github.com/umputun/ralphex/pkg/status"

// signal constants are aliases to the shared status package for convenience within processor.
// all signal values are defined in pkg/status to avoid circular dependencies.
const (
	SignalCompleted  = status.Completed
	SignalFailed     = status.Failed
	SignalReviewDone = status.ReviewDone
	SignalCodexDone  = status.CodexDone
	SignalQuestion   = status.Question
	SignalPlanReady  = status.PlanReady
	SignalPlanDraft  = status.PlanDraft
)
