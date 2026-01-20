package runner

// Signal constants for execution control.
// Using <<<RALPHEX:...>>> format for clear detection like ralph.py.
const (
	SignalCompleted  = "<<<RALPHEX:ALL_TASKS_DONE>>>"
	SignalFailed     = "<<<RALPHEX:TASK_FAILED>>>"
	SignalReviewDone = "<<<RALPHEX:REVIEW_DONE>>>"
	SignalCodexDone  = "<<<RALPHEX:CODEX_REVIEW_DONE>>>"
)

// IsTerminalSignal returns true if signal indicates execution should stop.
func IsTerminalSignal(signal string) bool {
	return signal == SignalCompleted || signal == SignalFailed
}

// IsReviewDone returns true if signal indicates review phase is complete.
func IsReviewDone(signal string) bool {
	return signal == SignalReviewDone
}

// IsCodexDone returns true if signal indicates codex phase is complete.
func IsCodexDone(signal string) bool {
	return signal == SignalCodexDone
}
