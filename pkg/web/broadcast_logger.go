package web

import (
	"fmt"
	"log"
	"strings"

	"github.com/umputun/ralphex/pkg/processor"
)

// BroadcastLogger wraps a processor.Logger and broadcasts events to SSE clients.
// implements the decorator pattern - all calls are forwarded to the inner logger
// while also being converted to events for web streaming.
//
// Thread safety: BroadcastLogger is NOT goroutine-safe. All methods must be called
// from a single goroutine (typically the main execution loop). The SSE server
// it writes to handles concurrent access from SSE clients.
type BroadcastLogger struct {
	inner       processor.Logger
	session     *Session
	phase       processor.Phase
	currentTask int // tracks current task number for boundary events
}

// NewBroadcastLogger creates a logger that wraps inner and broadcasts to the session's SSE server.
func NewBroadcastLogger(inner processor.Logger, session *Session) *BroadcastLogger {
	return &BroadcastLogger{
		inner:   inner,
		session: session,
		phase:   processor.PhaseTask,
	}
}

// SetPhase sets the current execution phase for color coding.
// emits task_end event if transitioning away from task phase with an active task.
func (b *BroadcastLogger) SetPhase(phase processor.Phase) {
	// if leaving task phase with an active task, emit task_end
	if b.phase == processor.PhaseTask && phase != processor.PhaseTask && b.currentTask > 0 {
		b.broadcast(NewTaskEndEvent(b.phase, b.currentTask, fmt.Sprintf("task %d completed", b.currentTask)))
		b.currentTask = 0
	}
	b.phase = phase
	b.inner.SetPhase(phase)
}

// Print writes a timestamped message and broadcasts it.
func (b *BroadcastLogger) Print(format string, args ...any) {
	b.inner.Print(format, args...)
	b.broadcast(NewOutputEvent(b.phase, formatText(format, args...)))
}

// PrintRaw writes without timestamp and broadcasts it.
func (b *BroadcastLogger) PrintRaw(format string, args ...any) {
	b.inner.PrintRaw(format, args...)
	b.broadcast(NewOutputEvent(b.phase, formatText(format, args...)))
}

// PrintSection writes a section header and broadcasts it.
// emits task/iteration boundary events based on section type.
func (b *BroadcastLogger) PrintSection(section processor.Section) {
	b.inner.PrintSection(section)

	// emit boundary events based on section type
	switch section.Type {
	case processor.SectionTaskIteration:
		// emit task end for previous task (if any)
		if b.currentTask > 0 {
			b.broadcast(NewTaskEndEvent(b.phase, b.currentTask, fmt.Sprintf("task %d completed", b.currentTask)))
		}
		b.currentTask = section.Iteration
		b.broadcast(NewTaskStartEvent(b.phase, section.Iteration, section.Label))

	case processor.SectionClaudeReview:
		b.broadcast(NewIterationStartEvent(b.phase, section.Iteration, section.Label))

	case processor.SectionCodexIteration:
		b.broadcast(NewIterationStartEvent(b.phase, section.Iteration, section.Label))

	case processor.SectionGeneric, processor.SectionClaudeEval:
		// no additional events for generic sections or claude eval

	default:
		// unknown section type - no additional events, but section event still emitted below
	}

	// always emit the section event
	b.broadcast(NewSectionEvent(b.phase, section.Label))
}

// PrintAligned writes text with timestamp on each line and broadcasts it.
func (b *BroadcastLogger) PrintAligned(text string) {
	b.inner.PrintAligned(text)
	b.broadcast(NewOutputEvent(b.phase, text))

	if signal := extractTerminalSignal(text); signal != "" {
		b.broadcast(NewSignalEvent(b.phase, signal))
	}
}

// LogQuestion logs a question and its options for plan creation mode.
func (b *BroadcastLogger) LogQuestion(question string, options []string) {
	b.inner.LogQuestion(question, options)
	b.broadcast(NewOutputEvent(b.phase, "QUESTION: "+question))
	b.broadcast(NewOutputEvent(b.phase, "OPTIONS: "+strings.Join(options, ", ")))
}

// LogAnswer logs the user's answer for plan creation mode.
func (b *BroadcastLogger) LogAnswer(answer string) {
	b.inner.LogAnswer(answer)
	b.broadcast(NewOutputEvent(b.phase, "ANSWER: "+answer))
}

// Path returns the progress file path.
func (b *BroadcastLogger) Path() string {
	return b.inner.Path()
}

// broadcast sends an event to the session's SSE server for live streaming and replay.
// errors are logged but not propagated since logging is the primary operation.
func (b *BroadcastLogger) broadcast(e Event) {
	if err := b.session.Publish(e); err != nil {
		log.Printf("[WARN] failed to broadcast event: %v", err)
	}
}

// formatText formats a string with args, like fmt.Sprintf.
func formatText(format string, args ...any) string {
	if len(args) == 0 {
		return format
	}
	return fmt.Sprintf(format, args...)
}

func extractTerminalSignal(text string) string {
	switch {
	case strings.Contains(text, processor.SignalCompleted):
		return "COMPLETED"
	case strings.Contains(text, processor.SignalFailed):
		return "FAILED"
	case strings.Contains(text, processor.SignalReviewDone):
		return "REVIEW_DONE"
	case strings.Contains(text, processor.SignalCodexDone):
		return "CODEX_REVIEW_DONE"
	default:
		return ""
	}
}
