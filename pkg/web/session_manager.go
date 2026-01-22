package web

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// SessionManager maintains a registry of all discovered sessions.
// it handles discovery of progress files, state detection via flock,
// and provides access to sessions by ID.
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*Session // keyed by session ID
}

// NewSessionManager creates a new session manager with an empty registry.
func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*Session),
	}
}

// Discover scans a directory for progress files matching progress-*.txt pattern.
// for each file found, it creates or updates a session in the registry.
// returns the list of discovered session IDs.
func (m *SessionManager) Discover(dir string) ([]string, error) {
	pattern := filepath.Join(dir, "progress-*.txt")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob progress files: %w", err)
	}

	ids := make([]string, 0, len(matches))
	for _, path := range matches {
		id := sessionIDFromPath(path)
		ids = append(ids, id)

		// check if session already exists
		m.mu.RLock()
		existing := m.sessions[id]
		m.mu.RUnlock()

		if existing != nil {
			// update existing session state
			if err := m.updateSession(existing); err != nil {
				// log error but continue with other sessions
				continue
			}
		} else {
			// create new session
			session := NewSession(id, path)
			if err := m.updateSession(session); err != nil {
				continue
			}
			m.mu.Lock()
			m.sessions[id] = session
			m.mu.Unlock()
		}
	}

	return ids, nil
}

// updateSession refreshes a session's state and metadata from its progress file.
func (m *SessionManager) updateSession(session *Session) error {
	// check if file is locked (active session)
	active, err := IsActive(session.Path)
	if err != nil {
		return fmt.Errorf("check active state: %w", err)
	}

	if active {
		session.SetState(SessionStateActive)
	} else {
		session.SetState(SessionStateCompleted)
	}

	// parse metadata from file header
	meta, err := ParseProgressHeader(session.Path)
	if err != nil {
		return fmt.Errorf("parse header: %w", err)
	}
	session.SetMetadata(meta)

	// update last modified time
	info, err := os.Stat(session.Path)
	if err != nil {
		return fmt.Errorf("stat file: %w", err)
	}
	session.SetLastModified(info.ModTime())

	return nil
}

// Get returns a session by ID, or nil if not found.
func (m *SessionManager) Get(id string) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[id]
}

// All returns all sessions in the registry.
func (m *SessionManager) All() []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		result = append(result, s)
	}
	return result
}

// Remove removes a session from the registry and closes its resources.
func (m *SessionManager) Remove(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if session, ok := m.sessions[id]; ok {
		session.Close()
		delete(m.sessions, id)
	}
}

// Close closes all sessions and clears the registry.
func (m *SessionManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, session := range m.sessions {
		session.Close()
	}
	m.sessions = make(map[string]*Session)
}

// sessionIDFromPath derives a session ID from the progress file path.
// the ID is the filename without the "progress-" prefix and ".txt" suffix.
// e.g., "progress-my-plan.txt" -> "my-plan"
func sessionIDFromPath(path string) string {
	base := filepath.Base(path)
	id := strings.TrimPrefix(base, "progress-")
	id = strings.TrimSuffix(id, ".txt")
	return id
}

// IsActive checks if a progress file is locked by another process.
// returns true if the file is locked (session is running), false otherwise.
// uses flock with LOCK_EX|LOCK_NB to test without blocking.
func IsActive(path string) (bool, error) {
	f, err := os.Open(path) //nolint:gosec // path from user-controlled glob pattern, acceptable for session discovery
	if err != nil {
		return false, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	// try to acquire exclusive lock non-blocking
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		// EWOULDBLOCK means file is locked by another process
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return true, nil
		}
		return false, fmt.Errorf("flock: %w", err)
	}

	// we got the lock, release it immediately
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false, nil
}

// ParseProgressHeader reads the header section of a progress file and extracts metadata.
// the header format is:
//
//	# Ralphex Progress Log
//	Plan: path/to/plan.md
//	Branch: feature-branch
//	Mode: full
//	Started: 2026-01-22 10:30:00
//	------------------------------------------------------------
func ParseProgressHeader(path string) (SessionMetadata, error) {
	f, err := os.Open(path) //nolint:gosec // path from user-controlled glob pattern, acceptable for session discovery
	if err != nil {
		return SessionMetadata{}, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	var meta SessionMetadata
	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		line := scanner.Text()

		// stop at separator line
		if strings.HasPrefix(line, "---") {
			break
		}

		// parse key-value pairs using CutPrefix for efficiency
		switch {
		case strings.HasPrefix(line, "Plan: "):
			if val, found := strings.CutPrefix(line, "Plan: "); found {
				meta.PlanPath = val
			}
		case strings.HasPrefix(line, "Branch: "):
			if val, found := strings.CutPrefix(line, "Branch: "); found {
				meta.Branch = val
			}
		case strings.HasPrefix(line, "Mode: "):
			if val, found := strings.CutPrefix(line, "Mode: "); found {
				meta.Mode = val
			}
		case strings.HasPrefix(line, "Started: "):
			if val, found := strings.CutPrefix(line, "Started: "); found {
				t, err := time.Parse("2006-01-02 15:04:05", val)
				if err == nil {
					meta.StartTime = t
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return SessionMetadata{}, fmt.Errorf("scan file: %w", err)
	}

	return meta, nil
}
