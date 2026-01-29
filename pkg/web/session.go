package web

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/tmaxmax/go-sse"
)

// DefaultReplayerSize is the maximum number of events to keep for replay to late-joining clients.
const DefaultReplayerSize = 10000

// allEventsReplayer wraps FiniteReplayer to replay ALL events when LastEventID is empty.
// standard FiniteReplayer only replays events after a specific ID, which doesn't work
// for first-time connections (no Last-Event-ID header).
//
// implementation note: FiniteReplayer assigns monotonically increasing integer IDs
// as strings starting at "1". by setting LastEventID to "0" when empty, we effectively
// request replay of all stored events. this depends on FiniteReplayer's internal
// ID generation scheme - if the library changes this behavior, replay may break.
type allEventsReplayer struct {
	inner *sse.FiniteReplayer
}

// Put delegates to the inner replayer.
func (r *allEventsReplayer) Put(message *sse.Message, topics []string) (*sse.Message, error) {
	return r.inner.Put(message, topics) //nolint:wrapcheck // pass through replayer errors as-is
}

// Replay replays events. If LastEventID is empty, replays from ID "0" (all events).
func (r *allEventsReplayer) Replay(subscription sse.Subscription) error {
	// if no LastEventID, replay from the beginning by using ID "0"
	// (our auto-generated IDs start at 1, so "0" means "replay everything")
	if subscription.LastEventID.String() == "" {
		subscription.LastEventID = sse.ID("0")
	}
	return r.inner.Replay(subscription) //nolint:wrapcheck // pass through replayer errors as-is
}

// SessionState represents the current state of a session.
type SessionState string

// session state constants.
const (
	SessionStateActive    SessionState = "active"    // session is running (progress file locked)
	SessionStateCompleted SessionState = "completed" // session finished (no lock held)
)

// SessionMetadata holds parsed information from progress file header.
type SessionMetadata struct {
	PlanPath  string    // path to plan file (from "Plan:" header line)
	Branch    string    // git branch (from "Branch:" header line)
	Mode      string    // execution mode: full, review, codex-only (from "Mode:" header line)
	StartTime time.Time // start time (from "Started:" header line)
}

// defaultTopic is the SSE topic used for all events within a session.
const defaultTopic = "events"

// Session represents a single ralphex execution instance.
// each session corresponds to one progress file and maintains its own SSE server.
type Session struct {
	mu sync.RWMutex

	ID       string          // unique identifier (derived from progress filename)
	Path     string          // full path to progress file
	Metadata SessionMetadata // parsed header information
	State    SessionState    // current state (active/completed)
	SSE      *sse.Server     // SSE server for this session (handles subscriptions and replay)
	Tailer   *Tailer         // file tailer for reading new content (nil if not tailing)

	// lastModified tracks the file's last modification time for change detection
	lastModified time.Time

	// stopTailCh signals the tail feeder goroutine to stop
	stopTailCh chan struct{}

	// loaded tracks whether historical data has been loaded into the SSE server
	loaded bool

	// inputCollector for web-based plan creation (nil when not in plan creation mode)
	inputCollector *WebInputCollector

	// cancelFunc for canceling plan creation context
	cancelFunc context.CancelFunc
}

// NewSession creates a new session for the given progress file path.
// the session starts with an SSE server configured for event replay.
// metadata should be populated by calling ParseMetadata after creation.
func NewSession(id, path string) *Session {
	finiteReplayer, err := sse.NewFiniteReplayer(DefaultReplayerSize, true)
	if err != nil {
		// FiniteReplayer only returns error for count < 2, which won't happen
		log.Printf("[WARN] failed to create replayer: %v", err)
		finiteReplayer = nil
	}

	// wrap in allEventsReplayer to replay all events on first connection
	var replayer sse.Replayer
	if finiteReplayer != nil {
		replayer = &allEventsReplayer{inner: finiteReplayer}
	}

	sseServer := &sse.Server{
		Provider: &sse.Joe{
			Replayer: replayer,
		},
		OnSession: func(w http.ResponseWriter, r *http.Request) ([]string, bool) {
			return []string{defaultTopic}, true
		},
	}

	return &Session{
		ID:    id,
		Path:  path,
		State: SessionStateCompleted, // default to completed until proven active
		SSE:   sseServer,
	}
}

// SetMetadata updates the session's metadata thread-safely.
func (s *Session) SetMetadata(meta SessionMetadata) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Metadata = meta
}

// GetMetadata returns the session's metadata thread-safely.
func (s *Session) GetMetadata() SessionMetadata {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Metadata
}

// SetState updates the session's state thread-safely.
func (s *Session) SetState(state SessionState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.State = state
}

// GetState returns the session's state thread-safely.
func (s *Session) GetState() SessionState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.State
}

// SetLastModified updates the last modified time thread-safely.
func (s *Session) SetLastModified(t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastModified = t
}

// GetLastModified returns the last modified time thread-safely.
func (s *Session) GetLastModified() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastModified
}

// IsLoaded returns whether historical data has been loaded into the SSE server.
func (s *Session) IsLoaded() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loaded
}

// MarkLoadedIfNot atomically checks if the session is not loaded and marks it as loaded.
// returns true if the session was successfully marked (was not loaded before),
// false if it was already loaded. this prevents double-loading race conditions.
func (s *Session) MarkLoadedIfNot() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded {
		return false
	}
	s.loaded = true
	return true
}

// StartTailing begins tailing the progress file and feeding events to SSE clients.
// if fromStart is true, reads from the beginning of the file; otherwise from the end.
// does nothing if already tailing.
func (s *Session) StartTailing(fromStart bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Tailer != nil && s.Tailer.IsRunning() {
		return nil // already tailing
	}

	s.Tailer = NewTailer(s.Path, DefaultTailerConfig())
	if err := s.Tailer.Start(fromStart); err != nil {
		s.Tailer = nil
		return err
	}

	s.stopTailCh = make(chan struct{})
	go s.feedEvents()

	return nil
}

// StopTailing stops the tailer and event feeder goroutine.
func (s *Session) StopTailing() {
	s.mu.Lock()
	if s.stopTailCh != nil {
		close(s.stopTailCh)
		s.stopTailCh = nil
	}
	tailer := s.Tailer
	s.mu.Unlock()

	if tailer != nil {
		tailer.Stop()
	}
}

// IsTailing returns whether the session is currently tailing its progress file.
func (s *Session) IsTailing() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Tailer != nil && s.Tailer.IsRunning()
}

// Publish sends an event to all connected SSE clients and stores it for replay.
// returns an error if publishing fails.
func (s *Session) Publish(event Event) error {
	msg := event.ToSSEMessage()
	if err := s.SSE.Publish(msg, defaultTopic); err != nil {
		return fmt.Errorf("publish event: %w", err)
	}
	return nil
}

// feedEvents reads events from the tailer and publishes them to SSE clients.
func (s *Session) feedEvents() {
	s.mu.RLock()
	tailer := s.Tailer
	stopCh := s.stopTailCh
	s.mu.RUnlock()

	if tailer == nil {
		return
	}

	eventCh := tailer.Events()
	for {
		select {
		case <-stopCh:
			return
		case event, ok := <-eventCh:
			if !ok {
				return
			}
			if err := s.Publish(event); err != nil {
				log.Printf("[WARN] failed to publish tailed event: %v", err)
			}
		}
	}
}

// Close cleans up session resources including the tailer and SSE server.
func (s *Session) Close() {
	s.StopTailing()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.SSE.Shutdown(ctx); err != nil {
		log.Printf("[WARN] failed to shutdown SSE server: %v", err)
	}
}

// SetInputCollector sets the input collector for web-based plan creation.
func (s *Session) SetInputCollector(collector *WebInputCollector) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inputCollector = collector
}

// GetInputCollector returns the input collector, or nil if not set.
func (s *Session) GetInputCollector() *WebInputCollector {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.inputCollector
}

// SetCancelFunc sets the cancel function for plan creation context.
func (s *Session) SetCancelFunc(cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancelFunc = cancel
}

// Cancel cancels the plan creation context, if any.
// Safe to call even if no cancel func is set.
func (s *Session) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancelFunc != nil {
		s.cancelFunc()
	}
}
