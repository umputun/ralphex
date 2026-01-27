package web

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/umputun/ralphex/pkg/config"
	"github.com/umputun/ralphex/pkg/git"
	"github.com/umputun/ralphex/pkg/processor"
	"github.com/umputun/ralphex/pkg/progress"
)

// PlanRunner manages plan creation lifecycle for web-initiated plans.
// It handles starting new plan creation sessions, tracking running plans,
// and providing access to session data for the HTTP API.
type PlanRunner struct {
	mu       sync.RWMutex
	sessions map[string]*runningPlan
	config   *config.Config
	sm       *SessionManager // for registering sessions with the dashboard
}

// runningPlan tracks a single running plan creation.
type runningPlan struct {
	session   *Session
	collector *WebInputCollector
	cancel    context.CancelFunc
	dir       string // project directory
}

// NewPlanRunner creates a new PlanRunner with the given configuration.
// The SessionManager is optional but required for sessions to appear in the dashboard.
func NewPlanRunner(cfg *config.Config, sm *SessionManager) *PlanRunner {
	return &PlanRunner{
		sessions: make(map[string]*runningPlan),
		config:   cfg,
		sm:       sm,
	}
}

// StartPlan initiates a new plan creation in the given directory.
// Returns the session for SSE connection.
func (r *PlanRunner) StartPlan(dir, description string) (*Session, error) {
	// validate directory exists
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("directory error: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("not a directory: %s", dir)
	}

	// validate it's a git repo
	gitOps, err := git.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("not a git repository: %s (%w)", dir, err)
	}

	// get current branch
	branch, err := gitOps.CurrentBranch()
	if err != nil {
		branch = "unknown"
	}

	// generate unique progress file name based on description and timestamp
	logID := generateSessionID(description)
	progressPath := filepath.Join(dir, fmt.Sprintf("progress-plan-%s.txt", logID))

	// create session using ID derived from progress path (matches SessionManager)
	sessionID := sessionIDFromPath(progressPath)
	session := NewSession(sessionID, progressPath)
	session.SetState(SessionStateActive)
	session.SetMetadata(SessionMetadata{
		PlanPath:  description,
		Mode:      "plan",
		Branch:    branch,
		StartTime: time.Now(),
	})

	// create input collector for this session
	collector := NewWebInputCollector(session)
	session.SetInputCollector(collector)

	// create cancellable context
	ctx, cancel := context.WithCancel(context.Background())
	session.SetCancelFunc(cancel)

	// register with session manager so it appears in dashboard and SSE works
	if r.sm != nil {
		r.sm.Register(session)
	}

	// track the running plan
	r.mu.Lock()
	r.sessions[session.ID] = &runningPlan{
		session:   session,
		collector: collector,
		cancel:    cancel,
		dir:       dir,
	}
	r.mu.Unlock()

	// spawn goroutine to run plan creation
	go r.runPlanCreation(ctx, session, collector, description, branch)

	return session, nil
}

// CancelPlan cancels a running plan creation.
func (r *PlanRunner) CancelPlan(sessionID string) error {
	r.mu.Lock()
	running, ok := r.sessions[sessionID]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("session not found: %s", sessionID)
	}

	// call cancel and remove from tracking
	running.cancel()
	running.session.SetState(SessionStateCompleted)
	delete(r.sessions, sessionID)
	r.mu.Unlock()

	return nil
}

// GetSession returns a session by ID, or nil if not found.
func (r *PlanRunner) GetSession(sessionID string) *Session {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if running, ok := r.sessions[sessionID]; ok {
		return running.session
	}
	return nil
}

// GetAllSessions returns all running plan creation sessions.
func (r *PlanRunner) GetAllSessions() []*Session {
	r.mu.RLock()
	defer r.mu.RUnlock()

	sessions := make([]*Session, 0, len(r.sessions))
	for _, running := range r.sessions {
		sessions = append(sessions, running.session)
	}
	return sessions
}

// runPlanCreation executes the plan creation in the background.
func (r *PlanRunner) runPlanCreation(ctx context.Context, session *Session, collector *WebInputCollector, description, branch string) {
	r.executePlanCreation(ctx, session, collector, description, branch, false)
}

// cleanupSession removes a session from tracking after completion.
func (r *PlanRunner) cleanupSession(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if running, ok := r.sessions[sessionID]; ok {
		running.session.SetState(SessionStateCompleted)
		delete(r.sessions, sessionID)
	}
}

// ResumePlan resumes an interrupted plan creation from an existing progress file.
// Returns the session for SSE connection.
func (r *PlanRunner) ResumePlan(progressPath string) (*Session, error) {
	// validate progress file exists
	info, err := os.Stat(progressPath)
	if err != nil {
		return nil, fmt.Errorf("progress file error: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("not a file: %s", progressPath)
	}

	// check if already active (locked)
	isActive, err := IsActive(progressPath)
	if err != nil {
		return nil, fmt.Errorf("check active: %w", err)
	}
	if isActive {
		return nil, fmt.Errorf("session already active: %s", progressPath)
	}

	// parse progress file header to get metadata
	meta, err := ParseProgressHeader(progressPath)
	if err != nil {
		return nil, fmt.Errorf("parse progress header: %w", err)
	}

	// verify it's a plan mode session
	if meta.Mode != "plan" {
		return nil, fmt.Errorf("not a plan mode session: %s", meta.Mode)
	}

	// get directory from progress path
	dir := filepath.Dir(progressPath)

	// validate it's still a git repo
	gitOps, err := git.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("not a git repository: %s (%w)", dir, err)
	}

	// get current branch (may have changed since original session)
	branch, err := gitOps.CurrentBranch()
	if err != nil {
		branch = meta.Branch // fall back to original branch
	}

	// create or reuse session using ID derived from progress path
	sessionID := sessionIDFromPath(progressPath)
	session := NewSession(sessionID, progressPath)
	if r.sm != nil {
		if existing := r.sm.Get(sessionID); existing != nil {
			session = existing
		} else {
			r.sm.Register(session)
		}
	}

	session.SetState(SessionStateActive)
	session.SetMetadata(SessionMetadata{
		PlanPath:  meta.PlanPath, // in plan mode, this is the description
		Mode:      "plan",
		Branch:    branch,
		StartTime: meta.StartTime, // keep original start time
	})

	// create input collector for this session
	collector := NewWebInputCollector(session)
	session.SetInputCollector(collector)

	// create cancellable context
	ctx, cancel := context.WithCancel(context.Background())
	session.SetCancelFunc(cancel)

	// track the running plan
	sessionID = session.ID
	r.mu.Lock()
	r.sessions[sessionID] = &runningPlan{
		session:   session,
		collector: collector,
		cancel:    cancel,
		dir:       dir,
	}
	r.mu.Unlock()

	// spawn goroutine to run plan creation (resume mode)
	// meta.PlanPath contains the plan description in plan mode
	go r.runPlanCreationResume(ctx, session, collector, meta.PlanPath, branch, progressPath)

	return session, nil
}

// runPlanCreationResume executes the plan creation in resume mode.
func (r *PlanRunner) runPlanCreationResume(ctx context.Context, session *Session, collector *WebInputCollector, description, branch, _ string) {
	r.executePlanCreation(ctx, session, collector, description, branch, true)
}

// executePlanCreation contains the shared logic for plan creation execution.
func (r *PlanRunner) executePlanCreation(ctx context.Context, session *Session, collector *WebInputCollector, description, branch string, appendMode bool) {
	defer r.cleanupSession(session.ID)

	// create colors from config
	colors := progress.NewColors(r.config.Colors)

	// create progress logger
	baseLog, err := progress.NewLogger(progress.Config{
		PlanDescription: description,
		ProgressPath:    session.Path,
		Mode:            string(processor.ModePlan),
		Branch:          branch,
		NoColor:         true, // web dashboard handles colors
		Append:          appendMode,
	}, colors)
	if err != nil {
		log.Printf("[ERROR] failed to create progress logger: %v", err)
		return
	}
	defer baseLog.Close()

	// wrap in broadcast logger to stream to SSE
	broadcastLog := NewBroadcastLogger(baseLog, session)

	// create and configure runner
	runner := processor.New(processor.Config{
		PlanDescription:  description,
		ProgressPath:     baseLog.Path(),
		Mode:             processor.ModePlan,
		MaxIterations:    50, // reasonable default for web
		Debug:            false,
		NoColor:          true,
		IterationDelayMs: 2000,
		AppConfig:        r.config,
	}, broadcastLog)
	runner.SetInputCollector(collector)

	// run plan creation
	if runErr := runner.Run(ctx); runErr != nil {
		if errors.Is(runErr, context.Canceled) {
			log.Printf("[INFO] plan creation canceled for session %s", session.ID)
		} else {
			log.Printf("[ERROR] plan creation failed for session %s: %v", session.ID, runErr)
		}
	}
}

// GetResumableSessions returns all resumable plan sessions from the configured project directories.
func (r *PlanRunner) GetResumableSessions() ([]ResumableSession, error) {
	if r.config == nil {
		return nil, nil
	}
	dirs := uniqueDirs(append(append([]string{}, r.config.ProjectDirs...), r.config.WatchDirs...))
	return FindResumableSessions(dirs)
}

func uniqueDirs(dirs []string) []string {
	seen := make(map[string]struct{}, len(dirs))
	out := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}
		out = append(out, dir)
	}
	return out
}

// generateSessionID creates a unique session ID from description.
func generateSessionID(description string) string {
	// use first few words of description + timestamp
	words := description
	if len(words) > 20 {
		words = words[:20]
	}
	// sanitize for file/ID use
	var sb []byte
	for _, c := range words {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			sb = append(sb, byte(c))
		} else if c == ' ' || c == '-' || c == '_' {
			sb = append(sb, '-')
		}
	}
	// add timestamp for uniqueness
	return fmt.Sprintf("%s-%d", string(sb), time.Now().UnixNano())
}
