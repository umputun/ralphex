package web

import (
	"sync"
	"time"
)

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

// Session represents a single ralphex execution instance.
// each session corresponds to one progress file and maintains its own event buffer and hub.
type Session struct {
	mu sync.RWMutex

	ID       string          // unique identifier (derived from progress filename)
	Path     string          // full path to progress file
	Metadata SessionMetadata // parsed header information
	State    SessionState    // current state (active/completed)
	Buffer   *Buffer         // event buffer for this session
	Hub      *Hub            // event hub for SSE streaming

	// lastModified tracks the file's last modification time for change detection
	lastModified time.Time
}

// NewSession creates a new session for the given progress file path.
// the session starts with an empty buffer and hub; metadata should be populated
// by calling ParseMetadata after creation.
func NewSession(id, path string) *Session {
	return &Session{
		ID:     id,
		Path:   path,
		State:  SessionStateCompleted, // default to completed until proven active
		Buffer: NewBuffer(DefaultBufferSize),
		Hub:    NewHub(),
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

// Close cleans up session resources.
func (s *Session) Close() {
	s.Hub.Close()
	s.Buffer.Clear()
}
