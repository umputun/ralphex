// Package web provides HTTP server and SSE streaming for real-time dashboard output.
package web

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/tmaxmax/go-sse"
	"github.com/umputun/ralphex/pkg/processor"
)

// EventType represents the type of event being streamed.
type EventType string

// event type constants for SSE streaming.
const (
	EventTypeOutput         EventType = "output"          // regular output line
	EventTypeSection        EventType = "section"         // section header
	EventTypeError          EventType = "error"           // error message
	EventTypeWarn           EventType = "warn"            // warning message
	EventTypeSignal         EventType = "signal"          // completion/failure signal
	EventTypeTaskStart      EventType = "task_start"      // task execution started
	EventTypeTaskEnd        EventType = "task_end"        // task execution ended
	EventTypeIterationStart EventType = "iteration_start" // review/codex iteration started
)

// Event represents a single event to be streamed to web clients.
type Event struct {
	Type         EventType       `json:"type"`
	Phase        processor.Phase `json:"phase"`
	Section      string          `json:"section,omitempty"`
	Text         string          `json:"text"`
	Timestamp    time.Time       `json:"timestamp"`
	Signal       string          `json:"signal,omitempty"`
	TaskNum      int             `json:"task_num,omitempty"`      // 1-based task index from plan (matches plan.tasks[].number)
	IterationNum int             `json:"iteration_num,omitempty"` // 1-based iteration index for review/codex phases
}

// NewOutputEvent creates an output event with current timestamp.
func NewOutputEvent(phase processor.Phase, text string) Event {
	return Event{
		Type:      EventTypeOutput,
		Phase:     phase,
		Text:      text,
		Timestamp: time.Now(),
	}
}

// NewSectionEvent creates a section header event.
func NewSectionEvent(phase processor.Phase, name string) Event {
	return Event{
		Type:      EventTypeSection,
		Phase:     phase,
		Section:   name,
		Text:      name,
		Timestamp: time.Now(),
	}
}

// NewErrorEvent creates an error event.
func NewErrorEvent(phase processor.Phase, text string) Event {
	return Event{
		Type:      EventTypeError,
		Phase:     phase,
		Text:      text,
		Timestamp: time.Now(),
	}
}

// NewWarnEvent creates a warning event.
func NewWarnEvent(phase processor.Phase, text string) Event {
	return Event{
		Type:      EventTypeWarn,
		Phase:     phase,
		Text:      text,
		Timestamp: time.Now(),
	}
}

// NewSignalEvent creates a signal event.
func NewSignalEvent(phase processor.Phase, signal string) Event {
	return Event{
		Type:      EventTypeSignal,
		Phase:     phase,
		Text:      signal,
		Signal:    signal,
		Timestamp: time.Now(),
	}
}

// NewTaskStartEvent creates a task start boundary event.
func NewTaskStartEvent(phase processor.Phase, taskNum int, text string) Event {
	return Event{
		Type:      EventTypeTaskStart,
		Phase:     phase,
		Text:      text,
		TaskNum:   taskNum,
		Timestamp: time.Now(),
	}
}

// NewTaskEndEvent creates a task end boundary event.
func NewTaskEndEvent(phase processor.Phase, taskNum int, text string) Event {
	return Event{
		Type:      EventTypeTaskEnd,
		Phase:     phase,
		Text:      text,
		TaskNum:   taskNum,
		Timestamp: time.Now(),
	}
}

// NewIterationStartEvent creates an iteration start event.
func NewIterationStartEvent(phase processor.Phase, iterationNum int, text string) Event {
	return Event{
		Type:         EventTypeIterationStart,
		Phase:        phase,
		Text:         text,
		IterationNum: iterationNum,
		Timestamp:    time.Now(),
	}
}

// MarshalJSON implements json.Marshaler for SSE streaming.
// this allows Event to be used directly with json.Marshal.
func (e Event) MarshalJSON() ([]byte, error) {
	// use a type alias to avoid infinite recursion
	type eventAlias Event
	data, err := json.Marshal(eventAlias(e))
	if err != nil {
		return nil, fmt.Errorf("marshal event: %w", err)
	}
	return data, nil
}

// JSON returns the event as JSON bytes for SSE streaming.
//
// Deprecated: use json.Marshal(event) instead.
func (e Event) JSON() ([]byte, error) {
	return e.MarshalJSON()
}

// ToSSEMessage converts the event to a go-sse Message for streaming.
// the event is serialized as JSON in the data field. we don't set the SSE event type
// because browsers' onmessage handler only catches typeless events (or type "message").
// the event type is already in the JSON payload for client-side processing.
func (e Event) ToSSEMessage() *sse.Message {
	msg := &sse.Message{}
	jsonData, err := json.Marshal(e)
	if err != nil {
		// fallback to text field if JSON marshaling fails
		msg.AppendData(e.Text)
		return msg
	}
	msg.AppendData(string(jsonData))
	return msg
}
