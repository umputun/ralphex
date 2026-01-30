package web

import (
	"bufio"
	"fmt"
	"hash/fnv"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/umputun/ralphex/pkg/processor"
	"github.com/umputun/ralphex/pkg/progress"
)

// MaxCompletedSessions is the maximum number of completed sessions to retain.
// active sessions are never evicted. oldest completed sessions are removed
// when this limit is exceeded to prevent unbounded memory growth.
const MaxCompletedSessions = 100

// maxScannerBuffer is the maximum buffer size for bufio.Scanner.
// set to 64MB to handle large outputs (e.g., diffs of large JSON files).
const maxScannerBuffer = 64 * 1024 * 1024

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

		// skip hidden directories
		if d.IsDir() && strings.HasPrefix(d.Name(), ".") && path != root {
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
func (m *SessionManager) updateSession(session *Session) error {
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
	if prevState != newState {
		if newState == SessionStateActive && !session.IsTailing() {
			// session became active, start tailing from beginning to capture existing content
			if tailErr := session.StartTailing(true); tailErr != nil {
				log.Printf("[WARN] failed to start tailing for session %s: %v", session.ID, tailErr)
			}
		} else if newState == SessionStateCompleted && session.IsTailing() {
			// session completed, stop tailing
			session.StopTailing()
		}
	}

	// for completed sessions that haven't been loaded yet, load the file content once
	// this handles sessions discovered after they finished.
	// MarkLoadedIfNot is atomic to prevent double-loading from concurrent goroutines.
	if newState == SessionStateCompleted && session.MarkLoadedIfNot() {
		loadProgressFileIntoSession(session.Path, session)
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
	// increase buffer size for large lines (matching executor)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, maxScannerBuffer)

	for scanner.Scan() {
		line := scanner.Text()

		// stop at separator line
		if strings.HasPrefix(line, "---") {
			break
		}

		// parse key-value pairs
		if val, found := strings.CutPrefix(line, "Plan: "); found {
			meta.PlanPath = val
		} else if val, found := strings.CutPrefix(line, "Branch: "); found {
			meta.Branch = val
		} else if val, found := strings.CutPrefix(line, "Mode: "); found {
			meta.Mode = val
		} else if val, found := strings.CutPrefix(line, "Started: "); found {
			t, err := time.Parse("2006-01-02 15:04:05", val)
			if err == nil {
				meta.StartTime = t
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return SessionMetadata{}, fmt.Errorf("scan file: %w", err)
	}

	return meta, nil
}

// loadProgressFileIntoSession reads a progress file and publishes events to the session's SSE server.
// used for completed sessions that were discovered after they finished.
// errors are silently ignored since this is best-effort loading.
func loadProgressFileIntoSession(path string, session *Session) {
	f, err := os.Open(path) //nolint:gosec // path from user-controlled glob pattern, acceptable for session discovery
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// increase buffer size for large lines (matching executor)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, maxScannerBuffer)
	inHeader := true
	phase := processor.PhaseTask
	var pendingSection string // section header waiting for first timestamped event

	for scanner.Scan() {
		line := scanner.Text()

		// skip empty lines
		if line == "" {
			continue
		}

		// check for header separator (line of dashes without spaces)
		if strings.HasPrefix(line, "---") && strings.Count(line, "-") > 20 && !strings.Contains(line, " ") {
			inHeader = false
			continue
		}

		// skip header lines
		if inHeader {
			continue
		}

		// check for section header (--- section name ---)
		if matches := sectionRegex.FindStringSubmatch(line); matches != nil {
			sectionName := matches[1]
			phase = phaseFromSection(sectionName)
			// defer emitting section until we see a timestamped event
			pendingSection = sectionName
			continue
		}

		// check for timestamped line
		if matches := timestampRegex.FindStringSubmatch(line); matches != nil {
			text := matches[2]

			// parse timestamp
			ts, err := time.Parse("06-01-02 15:04:05", matches[1])
			if err != nil {
				ts = time.Now()
			}

			// emit pending section with this event's timestamp (for accurate durations)
			if pendingSection != "" {
				emitPendingSection(session, pendingSection, phase, ts)
				pendingSection = ""
			}

			eventType := detectEventType(text)
			event := Event{
				Type:      eventType,
				Phase:     phase,
				Text:      text,
				Timestamp: ts,
			}

			if sig := extractSignalFromText(text); sig != "" {
				event.Signal = sig
				event.Type = EventTypeSignal
			}

			_ = session.Publish(event)
			continue
		}

		// plain line (no timestamp)
		_ = session.Publish(Event{
			Type:      EventTypeOutput,
			Phase:     phase,
			Text:      line,
			Timestamp: time.Now(),
		})
	}
}

// phaseFromSection determines the phase from a section name.
// checks "codex" before "review" because "Codex Review" should be PhaseCodex, not PhaseReview.
func phaseFromSection(name string) processor.Phase {
	nameLower := strings.ToLower(name)
	switch {
	case strings.Contains(nameLower, "task"):
		return processor.PhaseTask
	case strings.Contains(nameLower, "codex"):
		return processor.PhaseCodex
	case strings.Contains(nameLower, "review"):
		return processor.PhaseReview
	case strings.Contains(nameLower, "claude-eval") || strings.Contains(nameLower, "claude eval"):
		return processor.PhaseClaudeEval
	default:
		return processor.PhaseTask
	}
}

// emitPendingSection publishes section and task_start events for a pending section.
// task_start is emitted before section for task iteration sections.
func emitPendingSection(session *Session, sectionName string, phase processor.Phase, ts time.Time) {
	// emit task_start event for task iteration sections
	if matches := taskIterationRegex.FindStringSubmatch(sectionName); matches != nil {
		taskNum, err := strconv.Atoi(matches[1])
		if err != nil {
			// log parse error but continue - section will still be emitted
			log.Printf("[WARN] failed to parse task number from section %q: %v", sectionName, err)
		} else {
			if err := session.Publish(Event{
				Type:      EventTypeTaskStart,
				Phase:     phase,
				TaskNum:   taskNum,
				Text:      sectionName,
				Timestamp: ts,
			}); err != nil {
				log.Printf("[WARN] failed to publish task_start event: %v", err)
			}
		}
	}

	if err := session.Publish(Event{
		Type:      EventTypeSection,
		Phase:     phase,
		Section:   sectionName,
		Text:      sectionName,
		Timestamp: ts,
	}); err != nil {
		log.Printf("[WARN] failed to publish section event: %v", err)
	}
}
