package web

import (
	"fmt"
	"regexp"
	"strconv"

	"github.com/umputun/ralphex/pkg/processor"
	"github.com/umputun/ralphex/pkg/progress"
)

// patterns for detecting task and iteration boundaries in section names.
// uses word boundary \b instead of end-of-string $ for flexibility with potential suffixes.
//
// NOTE: these patterns are tightly coupled to how the processor names sections.
// the web dashboard UI depends on these exact patterns to track task progress.
// changing section naming in the processor may break dashboard task tracking.
var (
	taskIterationPattern   = regexp.MustCompile(`^task iteration (\d+)\b`)
	reviewIterationPattern = regexp.MustCompile(`^claude review (\d+)\b`)
	codexIterationPattern  = regexp.MustCompile(`^codex iteration (\d+)\b`)
)

// BroadcastLogger wraps a processor.Logger and broadcasts events to SSE clients.
// implements the decorator pattern - all calls are forwarded to the inner logger
// while also being converted to events for web streaming.
//
// Thread safety: BroadcastLogger is NOT goroutine-safe. All methods must be called
// from a single goroutine (typically the main execution loop). The Hub and Buffer
// it writes to are thread-safe for concurrent reads by SSE clients.
type BroadcastLogger struct {
	inner       processor.Logger
	hub         *Hub
	buffer      *Buffer
	phase       progress.Phase
	currentTask int // tracks current task number for boundary events
}

// NewBroadcastLogger creates a logger that wraps inner and broadcasts to hub/buffer.
func NewBroadcastLogger(inner processor.Logger, hub *Hub, buffer *Buffer) *BroadcastLogger {
	return &BroadcastLogger{
		inner:  inner,
		hub:    hub,
		buffer: buffer,
		phase:  progress.PhaseTask,
	}
}

// SetPhase sets the current execution phase for color coding.
// emits task_end event if transitioning away from task phase with an active task.
func (b *BroadcastLogger) SetPhase(phase progress.Phase) {
	// if leaving task phase with an active task, emit task_end
	if b.phase == progress.PhaseTask && phase != progress.PhaseTask && b.currentTask > 0 {
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
// also emits task/iteration boundary events based on section name patterns.
func (b *BroadcastLogger) PrintSection(name string) {
	b.inner.PrintSection(name)

	// check for task iteration pattern "task iteration N"
	if matches := taskIterationPattern.FindStringSubmatch(name); matches != nil {
		// error can be ignored: regex guarantees matches[1] contains only digits
		taskNum, _ := strconv.Atoi(matches[1])
		// emit task end for previous task (if any)
		if b.currentTask > 0 {
			b.broadcast(NewTaskEndEvent(b.phase, b.currentTask, fmt.Sprintf("task %d completed", b.currentTask)))
		}
		b.currentTask = taskNum
		b.broadcast(NewTaskStartEvent(b.phase, taskNum, name))
	}

	// check for review iteration pattern "claude review N"
	if matches := reviewIterationPattern.FindStringSubmatch(name); matches != nil {
		// error can be ignored: regex guarantees matches[1] contains only digits
		iterNum, _ := strconv.Atoi(matches[1])
		b.broadcast(NewIterationStartEvent(b.phase, iterNum, name))
	}

	// check for codex iteration pattern "codex iteration N"
	if matches := codexIterationPattern.FindStringSubmatch(name); matches != nil {
		// error can be ignored: regex guarantees matches[1] contains only digits
		iterNum, _ := strconv.Atoi(matches[1])
		b.broadcast(NewIterationStartEvent(b.phase, iterNum, name))
	}

	// always emit the section event
	b.broadcast(NewSectionEvent(b.phase, name))
}

// PrintAligned writes text with timestamp on each line and broadcasts it.
func (b *BroadcastLogger) PrintAligned(text string) {
	b.inner.PrintAligned(text)
	b.broadcast(NewOutputEvent(b.phase, text))
}

// Path returns the progress file path.
func (b *BroadcastLogger) Path() string {
	return b.inner.Path()
}

// broadcast sends an event to both the buffer (for late-joining clients) and the hub (for live clients).
func (b *BroadcastLogger) broadcast(e Event) {
	b.buffer.Add(e)
	b.hub.Broadcast(e)
}

// formatText formats a string with args, like fmt.Sprintf.
func formatText(format string, args ...any) string {
	if len(args) == 0 {
		return format
	}
	return fmt.Sprintf(format, args...)
}
