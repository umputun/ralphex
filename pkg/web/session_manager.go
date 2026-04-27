package web

import (
	"fmt"
	"hash/fnv"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/umputun/ralphex/pkg/progress"
)

// MaxCompletedSessions is the maximum number of completed sessions to retain.
// active sessions are never evicted. oldest completed sessions are removed
// when this limit is exceeded to prevent unbounded memory growth.
const MaxCompletedSessions = 100

// SessionManager maintains a registry of all discovered sessions.
// it handles discovery of progress files, state detection via flock,
// and provides access to sessions by ID.
// completed sessions are automatically evicted when MaxCompletedSessions is exceeded.
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
				log.Printf("[WARN] failed to update session %s: %v", id, err)
				continue
			}
		} else {
			// create new session
			session := NewSession(id, path)
			if err := m.updateSession(session); err != nil {
				log.Printf("[WARN] failed to create session %s: %v", id, err)
				continue
			}
			m.mu.Lock()
			m.sessions[id] = session
			m.evictOldCompleted()
			m.mu.Unlock()
		}
	}

	return ids, nil
}

// DiscoverRecursive walks a directory tree and discovers all progress files.
// unlike Discover, this searches subdirectories recursively.
// returns the list of all discovered session IDs (deduplicated).
func (m *SessionManager) DiscoverRecursive(root string) ([]string, error) {
	seenDirs := make(map[string]bool)
	seenIDs := make(map[string]bool)
	var allIDs []string

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// skip directories that can't be accessed
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// skip directories that typically contain many subdirs and no progress files
		if d.IsDir() && skipDirs[d.Name()] && path != root {
			return filepath.SkipDir
		}

		// skip non-progress files
		if d.IsDir() || !isProgressFile(path) {
			return nil
		}

		// only call Discover once per directory
		dir := filepath.Dir(path)
		if seenDirs[dir] {
			return nil
		}
		seenDirs[dir] = true

		ids, discoverErr := m.Discover(dir)
		if discoverErr != nil {
			return nil //nolint:nilerr // best-effort discovery, errors for individual directories are ignored
		}

		for _, id := range ids {
			if !seenIDs[id] {
				seenIDs[id] = true
				allIDs = append(allIDs, id)
			}
		}

		return nil
	})

	if err != nil {
		return allIDs, fmt.Errorf("walk directory %s: %w", root, err)
	}

	return allIDs, nil
}

// updateSession refreshes a session's state and metadata from its progress file.
// handles starting/stopping tailing based on state transitions.
//
// the header's Started: timestamp is compared against the previously stored
// metadata to detect a new ralphex run that reused the progress file (truncate
// + rewrite). when a restart is detected, per-run state (lastOffset, loaded,
// phase, pending section, diff stats) is reset before the state-transition
// path runs, so the subsequent loader / tailer reads the fresh file from byte
// 0 rather than seeking to the stale offset from the previous run. this
// closes the truncation race where StartFromOffset's `offset > fileSize` check
// misses the case where the new run has already grown past the old offset.
//
// the restart check and stored-metadata update are both gated on the header
// being complete (terminating separator observed). a mid-write read can return
// incomplete metadata with a zero StartTime; overwriting the stored value with
// that zero would erase the previous run's StartTime and defeat the restart
// detection on a later event when the full header is finally visible.
func (m *SessionManager) updateSession(session *Session) error {
	// parse header first so we can detect a new ralphex run that reused this
	// progress file. a changed "Started:" timestamp means the file was
	// truncated and re-initialized: the stored lastOffset is from the
	// previous run and no longer corresponds to current content.
	meta, headerComplete, err := ParseProgressHeader(session.Path)
	if err != nil {
		return fmt.Errorf("parse header: %w", err)
	}
	if headerComplete {
		oldMeta := session.GetMetadata()
		if !oldMeta.StartTime.IsZero() && !meta.StartTime.IsZero() &&
			!oldMeta.StartTime.Equal(meta.StartTime) {
			// new run on same file: stop any ongoing tailer (captures no useful
			// offset since the file was replaced), then reset per-run state so
			// handleStateTransition / MarkLoadedIfNot pick up the fresh content
			// from byte 0 regardless of how far the new run has grown.
			if session.IsTailing() {
				session.StopTailing()
			}
			session.resetForNewRun()
		}
		session.SetMetadata(meta)
	}

	prevState := session.GetState()

	// check if file is locked (active session)
	active, err := IsActive(session.Path)
	if err != nil {
		return fmt.Errorf("check active state: %w", err)
	}

	newState := SessionStateCompleted
	if active {
		newState = SessionStateActive
	}
	session.SetState(newState)

	// handle state transitions for tailing
	m.handleStateTransition(session, prevState, newState)

	// for completed sessions that haven't been loaded yet, load the file content once.
	// this handles sessions discovered after they finished.
	// MarkLoadedIfNot is atomic to prevent double-loading from concurrent goroutines.
	// the lastOffset==0 guard prevents re-reading content a previous tailer already
	// ingested: an active session tailed from the start can be marked completed by a
	// RefreshStates flock race that captures the tailer's offset into lastOffset; if
	// IsActive races false a second time here, newState stays completed and the
	// loader would otherwise re-emit events the tailer has already published. we
	// still mark the session loaded in that case so the watcher's IsLoaded gate
	// allows Reactivate to resume tailing from the captured offset.
	//
	// gated on headerComplete so that a mid-write discovery (e.g. Windows, where
	// IsActive is always false, or a rare Unix race where IsActive momentarily
	// returns false) does not mark the session loaded and record a lastOffset
	// pointing inside an unfinished header. without this gate, a later Reactivate
	// would resume from mid-header and emit the remaining header lines as output
	// events. the loader will run on a later updateSession call once the header
	// separator is visible.
	if newState == SessionStateCompleted && headerComplete && session.MarkLoadedIfNot() {
		if session.getLastOffset() == 0 {
			m.loadProgressFileIntoSession(session.Path, session)
		}
	}

	// update last modified time
	info, err := os.Stat(session.Path)
	if err != nil {
		return fmt.Errorf("stat file: %w", err)
	}
	session.SetLastModified(info.ModTime())

	return nil
}

// handleStateTransition starts or stops tailing for a session whose state just
// changed. when a session becomes active with content already ingested
// (lastOffset > 0), resumption goes through Reactivate so the SSE replay buffer
// is not filled with duplicates; fresh-active sessions read from the beginning.
// completed→no-op and active→active transitions do nothing.
func (m *SessionManager) handleStateTransition(session *Session, prevState, newState SessionState) {
	if prevState == newState {
		return
	}
	switch {
	case newState == SessionStateActive && !session.IsTailing():
		m.activateSession(session)
	case newState == SessionStateCompleted && session.IsTailing():
		session.StopTailing()
	}
}

// activateSession starts tailing for a session that just became active.
// chooses between Reactivate (resume from stored offset) and StartTailing(true)
// (read from the beginning) based on whether content has already been ingested.
func (m *SessionManager) activateSession(session *Session) {
	if session.getLastOffset() > 0 {
		// content already in SSE replay (loader ran previously, or a
		// previous tailer captured an offset before StopTailing). resume
		// from the stored offset to avoid re-emitting events. this covers
		// the flock-race recovery path: RefreshStates falsely marks a live
		// session completed and captures the tailer offset; a later Write
		// event triggers this transition back to active.
		if err := session.Reactivate(); err != nil {
			log.Printf("[WARN] failed to reactivate session %s: %v", session.ID, err)
		}
		return
	}
	// fresh discovery of an active session, read from the beginning
	if err := session.StartTailing(true); err != nil {
		log.Printf("[WARN] failed to start tailing for session %s: %v", session.ID, err)
	}
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

// Register adds an externally-created session to the manager.
// This is used when a session is created for live execution (BroadcastLogger)
// and needs to be visible in the multi-session dashboard.
// The session's ID is derived from its path using sessionIDFromPath.
func (m *SessionManager) Register(session *Session) {
	id := sessionIDFromPath(session.Path)
	session.ID = id // ensure ID matches what SessionManager expects

	m.mu.Lock()
	defer m.mu.Unlock()

	// don't overwrite existing session
	if _, exists := m.sessions[id]; exists {
		return
	}

	m.sessions[id] = session
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

// evictOldCompleted removes oldest completed sessions when count exceeds MaxCompletedSessions.
// active sessions are never evicted. must be called with lock held.
func (m *SessionManager) evictOldCompleted() {
	// count completed sessions
	var completed []*Session
	for _, s := range m.sessions {
		if s.GetState() == SessionStateCompleted {
			completed = append(completed, s)
		}
	}

	if len(completed) <= MaxCompletedSessions {
		return
	}

	// sort by start time (oldest first)
	sort.Slice(completed, func(i, j int) bool {
		ti := completed[i].GetMetadata().StartTime
		tj := completed[j].GetMetadata().StartTime
		return ti.Before(tj)
	})

	// evict oldest sessions beyond the limit
	toEvict := len(completed) - MaxCompletedSessions
	for i := range toEvict {
		session := completed[i]
		session.Close()
		delete(m.sessions, session.ID)
	}
}

// StartTailingActive starts tailing for all active sessions.
// for each active session not already tailing, starts tailing from the beginning
// to populate the buffer with existing content.
func (m *SessionManager) StartTailingActive() {
	m.mu.RLock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.mu.RUnlock()

	for _, session := range sessions {
		if session.GetState() == SessionStateActive && !session.IsTailing() {
			if err := session.StartTailing(true); err != nil { // read from beginning to populate buffer
				log.Printf("[WARN] failed to start tailing for session %s: %v", session.ID, err)
			}
		}
	}
}

// RefreshStates checks all sessions for state changes (active->completed).
// stops tailing for sessions that have completed.
func (m *SessionManager) RefreshStates() {
	m.mu.RLock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.mu.RUnlock()

	for _, session := range sessions {
		// only check sessions that are currently tailing
		if !session.IsTailing() {
			continue
		}

		// check if session is still active
		active, err := IsActive(session.Path)
		if err != nil {
			continue
		}

		if !active {
			// session completed, update state and stop tailing
			session.SetState(SessionStateCompleted)
			session.StopTailing()
		}
	}
}

// sessionIDFromPath derives a session ID from the progress file path.
// the ID includes the filename (without the "progress-" prefix and ".txt" suffix)
// plus an FNV-64a hash of the canonical absolute path to avoid collisions across directories.
//
// format: <plan-name>-<16-char-hex-hash>
// example: "/tmp/progress-my-plan.txt" -> "my-plan-a1b2c3d4e5f67890"
//
// the hash ensures uniqueness when the same plan name exists in different directories.
// the path is canonicalized (absolute + cleaned) before hashing for stability.
func sessionIDFromPath(path string) string {
	base := filepath.Base(path)
	id := strings.TrimPrefix(base, "progress-")
	id = strings.TrimSuffix(id, ".txt")

	canonical := path
	if abs, err := filepath.Abs(path); err == nil {
		canonical = abs
	}
	canonical = filepath.Clean(canonical)

	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(canonical))
	return fmt.Sprintf("%s-%016x", id, hasher.Sum64())
}

// IsActive checks if a progress file is locked by another process or the current one.
// returns true if the file is locked (session is running), false otherwise.
// uses flock with LOCK_EX|LOCK_NB to test without blocking.
func IsActive(path string) (bool, error) {
	if progress.IsPathLockedByCurrentProcess(path) {
		return true, nil
	}

	f, err := os.Open(path) //nolint:gosec // path from user-controlled glob pattern, acceptable for session discovery
	if err != nil {
		return false, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	// try to acquire exclusive lock non-blocking
	gotLock, err := progress.TryLockFile(f)
	if err != nil {
		return false, fmt.Errorf("flock: %w", err)
	}

	// if we got the lock, file is not active
	// if we didn't get the lock, file is locked by another process (active)
	return !gotLock, nil
}
