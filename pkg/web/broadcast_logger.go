package web

import (
	"fmt"
	"log"
	"strings"

	"github.com/umputun/ralphex/pkg/status"
)

//go:generate moq -out mocks/logger.go -pkg mocks -skip-ensure -fmt goimports . Logger

// Logger provides progress logging for web dashboard wrapping.
type Logger interface {
	Print(format string, args ...any)
	PrintRaw(format string, args ...any)
	PrintSection(section status.Section)
	PrintAligned(text string)
	LogQuestion(question string, options []string)
	LogAnswer(answer string)
	LogDraftReview(action string, feedback string)
	Path() string
}

// BroadcastLogger wraps a Logger and broadcasts events to SSE clients.
// implements the decorator pattern - all calls are forwarded to the inner logger
// while also being converted to events for web streaming.
//
// Thread safety: BroadcastLogger is NOT goroutine-safe. All methods must be called
// from a single goroutine (typically the main execution loop). The SSE server
// it writes to handles concurrent access from SSE clients.
type BroadcastLogger struct {
	inner       Logger
	session     *Session
	holder      *status.PhaseHolder
	currentTask int // tracks current task number for boundary events
}

// NewBroadcastLogger creates a logger that wraps inner and broadcasts to the session's SSE server.
// registers an OnChange callback on the holder for phase transition events.
func NewBroadcastLogger(inner Logger, session *Session, holder *status.PhaseHolder) *BroadcastLogger {
	b := &BroadcastLogger{
		inner:   inner,
		session: session,
		holder:  holder,
	}
	holder.OnChange(b.onPhaseChanged)
	return b
}

// onPhaseChanged handles phase transition events.
// emits task_end event if transitioning away from task phase with an active task.
func (b *BroadcastLogger) onPhaseChanged(old, _ status.Phase) {
	if old == status.PhaseTask && b.currentTask > 0 {
		b.broadcast(NewTaskEndEvent(old, b.currentTask, fmt.Sprintf("task %d completed", b.currentTask)))
		b.currentTask = 0
	}
}

// Print writes a timestamped message and broadcasts it.
func (b *BroadcastLogger) Print(format string, args ...any) {
	b.inner.Print(format, args...)
	b.broadcast(NewOutputEvent(b.holder.Get(), formatText(format, args...)))
}

// PrintRaw writes without timestamp and broadcasts it.
func (b *BroadcastLogger) PrintRaw(format string, args ...any) {
	b.inner.PrintRaw(format, args...)
	b.broadcast(NewOutputEvent(b.holder.Get(), formatText(format, args...)))
}

// PrintSection writes a section header and broadcasts it.
// emits task/iteration boundary events based on section type.
func (b *BroadcastLogger) PrintSection(section status.Section) {
	b.inner.PrintSection(section)

	// emit boundary events based on section type
	switch section.Type {
	case status.SectionTaskIteration:
		// emit task end for previous task (if any)
		if b.currentTask > 0 {
			b.broadcast(NewTaskEndEvent(b.holder.Get(), b.currentTask, fmt.Sprintf("task %d completed", b.currentTask)))
		}
		b.currentTask = section.Iteration
		b.broadcast(NewTaskStartEvent(b.holder.Get(), section.Iteration, section.Label))

	case status.SectionClaudeReview:
		b.broadcast(NewIterationStartEvent(b.holder.Get(), section.Iteration, section.Label))

	case status.SectionCodexIteration:
		b.broadcast(NewIterationStartEvent(b.holder.Get(), section.Iteration, section.Label))

	case status.SectionGeneric, status.SectionClaudeEval:
		// no additional events for generic sections or claude eval

	default:
		// unknown section type - no additional events, but section event still emitted below
	}

	// always emit the section event
	b.broadcast(NewSectionEvent(b.holder.Get(), section.Label))
}

// PrintAligned writes text with timestamp on each line and broadcasts it.
func (b *BroadcastLogger) PrintAligned(text string) {
	b.inner.PrintAligned(text)
	b.broadcast(NewOutputEvent(b.holder.Get(), text))

	if signal := extractTerminalSignal(text); signal != "" {
		b.broadcast(NewSignalEvent(b.holder.Get(), signal))
	}
}

// LogQuestion logs a question and its options for plan creation mode.
func (b *BroadcastLogger) LogQuestion(question string, options []string) {
	b.inner.LogQuestion(question, options)
	b.broadcast(NewOutputEvent(b.holder.Get(), "QUESTION: "+question))
	b.broadcast(NewOutputEvent(b.holder.Get(), "OPTIONS: "+strings.Join(options, ", ")))
}

// LogAnswer logs the user's answer for plan creation mode.
func (b *BroadcastLogger) LogAnswer(answer string) {
	b.inner.LogAnswer(answer)
	b.broadcast(NewOutputEvent(b.holder.Get(), "ANSWER: "+answer))
}

// LogDraftReview logs the user's draft review action and optional feedback.
func (b *BroadcastLogger) LogDraftReview(action, feedback string) {
	b.inner.LogDraftReview(action, feedback)
	b.broadcast(NewOutputEvent(b.holder.Get(), "DRAFT REVIEW: "+action))
	if feedback != "" {
		b.broadcast(NewOutputEvent(b.holder.Get(), "FEEDBACK: "+feedback))
	}
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
	case strings.Contains(text, status.Completed):
		return "COMPLETED"
	case strings.Contains(text, status.Failed):
		return "FAILED"
	case strings.Contains(text, status.ReviewDone):
		return "REVIEW_DONE"
	case strings.Contains(text, status.CodexDone):
		return "CODEX_REVIEW_DONE"
	default:
		return ""
	}
}
