// Package status defines shared execution-model types for ralphex.
// signal constants, phase types, and section types used by processor, executor, progress, and web packages.
package status

// signal constants using <<<RALPHEX:...>>> format for clear detection.
const (
	Completed  = "<<<RALPHEX:ALL_TASKS_DONE>>>"
	Failed     = "<<<RALPHEX:TASK_FAILED>>>"
	ReviewDone = "<<<RALPHEX:REVIEW_DONE>>>"
	CodexDone  = "<<<RALPHEX:CODEX_REVIEW_DONE>>>"
	Question   = "<<<RALPHEX:QUESTION>>>"
	PlanReady  = "<<<RALPHEX:PLAN_READY>>>"
	PlanDraft  = "<<<RALPHEX:PLAN_DRAFT>>>"
)

// Phase represents execution phase for color coding.
type Phase string

// Phase constants for execution stages.
const (
	PhaseTask       Phase = "task"        // execution phase (green)
	PhaseReview     Phase = "review"      // code review phase (cyan)
	PhaseCodex      Phase = "codex"       // codex analysis phase (magenta)
	PhaseClaudeEval Phase = "claude-eval" // claude evaluating codex (bright cyan)
	PhasePlan       Phase = "plan"        // plan creation phase (info color)
	PhaseFinalize   Phase = "finalize"    // finalize step phase (green)
)
