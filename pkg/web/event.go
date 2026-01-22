// Package web provides HTTP server and SSE streaming for real-time dashboard output.
package web

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/umputun/ralphex/pkg/progress"
)

// EventType represents the type of event being streamed.
type EventType string

// event type constants for SSE streaming.
const (
	EventTypeOutput  EventType = "output"  // regular output line
	EventTypeSection EventType = "section" // section header
	EventTypeError   EventType = "error"   // error message
	EventTypeWarn    EventType = "warn"    // warning message
	EventTypeSignal  EventType = "signal"  // completion/failure signal
)

// Event represents a single event to be streamed to web clients.
type Event struct {
	Type      EventType      `json:"type"`
	Phase     progress.Phase `json:"phase"`
	Section   string         `json:"section,omitempty"`
	Text      string         `json:"text"`
	Timestamp time.Time      `json:"timestamp"`
	Signal    string         `json:"signal,omitempty"`
}

// NewOutputEvent creates an output event with current timestamp.
func NewOutputEvent(phase progress.Phase, text string) Event {
	return Event{
		Type:      EventTypeOutput,
		Phase:     phase,
		Text:      text,
		Timestamp: time.Now(),
	}
}

// NewSectionEvent creates a section header event.
func NewSectionEvent(phase progress.Phase, name string) Event {
	return Event{
		Type:      EventTypeSection,
		Phase:     phase,
		Section:   name,
		Text:      name,
		Timestamp: time.Now(),
	}
}

// NewErrorEvent creates an error event.
func NewErrorEvent(phase progress.Phase, text string) Event {
	return Event{
		Type:      EventTypeError,
		Phase:     phase,
		Text:      text,
		Timestamp: time.Now(),
	}
}

// NewWarnEvent creates a warning event.
func NewWarnEvent(phase progress.Phase, text string) Event {
	return Event{
		Type:      EventTypeWarn,
		Phase:     phase,
		Text:      text,
		Timestamp: time.Now(),
	}
}

// NewSignalEvent creates a signal event.
func NewSignalEvent(phase progress.Phase, signal string) Event {
	return Event{
		Type:      EventTypeSignal,
		Phase:     phase,
		Text:      signal,
		Signal:    signal,
		Timestamp: time.Now(),
	}
}

// JSON returns the event as JSON bytes for SSE streaming.
func (e Event) JSON() ([]byte, error) {
	data, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("marshal event: %w", err)
	}
	return data, nil
}
