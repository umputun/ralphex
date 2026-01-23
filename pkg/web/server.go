package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed templates static
var embeddedFS embed.FS

// sseHistoryFlushInterval is the number of history events to send before flushing.
// this prevents buffering too much data when sending large event histories to clients.
const sseHistoryFlushInterval = 100

// ServerConfig holds configuration for the web server.
type ServerConfig struct {
	Port     int    // port to listen on
	PlanName string // plan name to display in dashboard
	Branch   string // git branch name
	PlanFile string // path to plan file for /api/plan endpoint
}

// Server provides HTTP server for the real-time dashboard.
type Server struct {
	cfg    ServerConfig
	hub    *Hub            // used for single-session mode (direct execution)
	buffer *Buffer         // used for single-session mode (direct execution)
	sm     *SessionManager // used for multi-session mode (dashboard)
	srv    *http.Server
	tmpl   *template.Template

	// plan caching - set after first successful load (single-session mode)
	planMu    sync.Mutex
	planCache *Plan
}

// NewServer creates a new web server for single-session mode (direct execution).
// returns an error if the embedded template fails to parse.
func NewServer(cfg ServerConfig, hub *Hub, buffer *Buffer) (*Server, error) {
	tmpl, err := template.ParseFS(embeddedFS, "templates/base.html")
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}

	return &Server{
		cfg:    cfg,
		hub:    hub,
		buffer: buffer,
		tmpl:   tmpl,
	}, nil
}

// NewServerWithSessions creates a new web server for multi-session mode (dashboard).
// returns an error if the embedded template fails to parse.
func NewServerWithSessions(cfg ServerConfig, sm *SessionManager) (*Server, error) {
	tmpl, err := template.ParseFS(embeddedFS, "templates/base.html")
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}

	return &Server{
		cfg:  cfg,
		sm:   sm,
		tmpl: tmpl,
	}, nil
}

// Start begins listening for HTTP requests.
// blocks until the server is stopped or an error occurs.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	// register routes
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/events", s.handleEvents)
	mux.HandleFunc("/api/plan", s.handlePlan)
	mux.HandleFunc("/api/sessions", s.handleSessions)

	// static files
	staticFS, err := fs.Sub(embeddedFS, "static")
	if err != nil {
		return fmt.Errorf("static filesystem: %w", err)
	}
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	s.srv = &http.Server{
		Addr:              fmt.Sprintf("127.0.0.1:%d", s.cfg.Port),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// start shutdown listener
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.srv.Shutdown(shutdownCtx)
	}()

	err = s.srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return fmt.Errorf("http server: %w", err)
}

// Stop gracefully shuts down the server.
func (s *Server) Stop() error {
	if s.srv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown server: %w", err)
	}
	return nil
}

// Hub returns the server's event hub.
func (s *Server) Hub() *Hub {
	return s.hub
}

// Buffer returns the server's event buffer.
func (s *Server) Buffer() *Buffer {
	return s.buffer
}

// templateData holds data for the dashboard template.
type templateData struct {
	PlanName string
	Branch   string
}

// handleIndex serves the main dashboard page.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	data := templateData{
		PlanName: s.cfg.PlanName,
		Branch:   s.cfg.Branch,
	}

	if err := s.tmpl.Execute(w, data); err != nil {
		http.Error(w, "template execution error", http.StatusInternalServerError)
		return
	}
}

// handlePlan serves the parsed plan as JSON.
// in single-session mode, uses the server's configured plan file with caching.
// in multi-session mode, accepts ?session=<id> to load plan from session metadata.
func (s *Server) handlePlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := r.URL.Query().Get("session")

	// multi-session mode with session ID
	if s.sm != nil && sessionID != "" {
		s.handleSessionPlan(w, sessionID)
		return
	}

	// single-session mode - use cached server plan
	if s.cfg.PlanFile == "" {
		http.Error(w, "no plan file configured", http.StatusNotFound)
		return
	}

	plan, err := s.loadPlan()
	if err != nil {
		log.Printf("[WARN] failed to load plan file %s: %v", s.cfg.PlanFile, err)
		http.Error(w, "unable to load plan", http.StatusInternalServerError)
		return
	}

	data, err := plan.JSON()
	if err != nil {
		log.Printf("[WARN] failed to encode plan: %v", err)
		http.Error(w, "unable to encode plan", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

// handleSessionPlan handles plan requests for a specific session in multi-session mode.
func (s *Server) handleSessionPlan(w http.ResponseWriter, sessionID string) {
	session := s.sm.Get(sessionID)
	if session == nil {
		http.Error(w, "session not found: "+sessionID, http.StatusNotFound)
		return
	}

	meta := session.GetMetadata()
	if meta.PlanPath == "" {
		http.Error(w, "no plan file for session", http.StatusNotFound)
		return
	}

	// validate plan path to prevent path traversal attacks
	if err := validatePlanPath(meta.PlanPath); err != nil {
		log.Printf("[WARN] invalid plan path for session %s: %v", sessionID, err)
		http.Error(w, "invalid plan path", http.StatusBadRequest)
		return
	}

	plan, err := loadPlanWithFallback(meta.PlanPath)
	if err != nil {
		log.Printf("[WARN] failed to load plan file %s: %v", meta.PlanPath, err)
		http.Error(w, "unable to load plan", http.StatusInternalServerError)
		return
	}

	data, err := plan.JSON()
	if err != nil {
		log.Printf("[WARN] failed to encode plan: %v", err)
		http.Error(w, "unable to encode plan", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

// loadPlan returns a cached plan or loads it from disk (with completed/ fallback).
func (s *Server) loadPlan() (*Plan, error) {
	s.planMu.Lock()
	defer s.planMu.Unlock()

	if s.planCache != nil {
		return s.planCache, nil
	}

	plan, err := ParsePlanFile(s.cfg.PlanFile)
	if err != nil && errors.Is(err, fs.ErrNotExist) {
		completedPath := filepath.Join(filepath.Dir(s.cfg.PlanFile), "completed", filepath.Base(s.cfg.PlanFile))
		plan, err = ParsePlanFile(completedPath)
	}
	if err != nil {
		return nil, err
	}

	s.planCache = plan
	return plan, nil
}

// loadPlanWithFallback loads a plan from disk with completed/ directory fallback.
// does not cache - each call reads from disk.
func loadPlanWithFallback(path string) (*Plan, error) {
	plan, err := ParsePlanFile(path)
	if err != nil && errors.Is(err, fs.ErrNotExist) {
		completedPath := filepath.Join(filepath.Dir(path), "completed", filepath.Base(path))
		plan, err = ParsePlanFile(completedPath)
	}
	return plan, err
}

// validatePlanPath checks if a plan path is safe to read.
// rejects absolute paths and paths containing ".." to prevent path traversal attacks.
// plan paths in progress files should always be relative to the project directory.
func validatePlanPath(path string) error {
	if path == "" {
		return errors.New("empty path")
	}

	// reject absolute paths - plan paths should always be relative
	if filepath.IsAbs(path) {
		return fmt.Errorf("absolute paths not allowed: %s", path)
	}

	// reject paths containing ".." components
	cleaned := filepath.Clean(path)
	if strings.HasPrefix(cleaned, "..") || strings.Contains(cleaned, string(filepath.Separator)+"..") {
		return fmt.Errorf("path traversal not allowed: %s", path)
	}

	return nil
}

// handleEvents serves the SSE stream.
// in multi-session mode, accepts ?session=<id> query parameter.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session")
	log.Printf("[SSE] connection start: session=%s", sessionID)

	// get session-specific hub and buffer
	hub, buffer, err := s.getSessionResources(r)
	if err != nil {
		log.Printf("[SSE] session not found: %s", sessionID)
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	// set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering

	// ensure we can flush
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// subscribe to hub
	eventCh, err := hub.Subscribe()
	if err != nil {
		log.Printf("[SSE] subscribe failed: %v", err)
		http.Error(w, "too many connections", http.StatusServiceUnavailable)
		return
	}
	defer func() {
		hub.Unsubscribe(eventCh)
		log.Printf("[SSE] connection end: session=%s", sessionID)
	}()

	// send history first (with periodic flushes for large buffers)
	events := buffer.All()
	log.Printf("[SSE] sending %d history events: session=%s", len(events), sessionID)
	for i, event := range events {
		data, err := event.JSON()
		if err != nil {
			continue
		}
		fmt.Fprintf(w, "data: %s\n\n", data)
		// flush periodically to avoid buffering too much
		if (i+1)%sseHistoryFlushInterval == 0 {
			flusher.Flush()
		}
	}
	flusher.Flush()
	log.Printf("[SSE] history sent, entering event loop: session=%s", sessionID)

	// stream new events
	for {
		select {
		case event, ok := <-eventCh:
			if !ok {
				return // channel closed
			}
			data, err := event.JSON()
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()

		case <-r.Context().Done():
			return
		}
	}
}

// getSessionResources returns the hub and buffer for the request.
// in single-session mode, returns the server's hub/buffer.
// in multi-session mode, looks up the session by ID from query parameter.
func (s *Server) getSessionResources(r *http.Request) (*Hub, *Buffer, error) {
	sessionID := r.URL.Query().Get("session")

	// single-session mode (no session manager or no session ID)
	if s.sm == nil || sessionID == "" {
		if s.hub == nil || s.buffer == nil {
			return nil, nil, errors.New("no session specified")
		}
		return s.hub, s.buffer, nil
	}

	// multi-session mode - look up session
	session := s.sm.Get(sessionID)
	if session == nil {
		log.Printf("[SSE] session lookup failed: %s (not in manager)", sessionID)
		return nil, nil, fmt.Errorf("session not found: %s", sessionID)
	}

	// defensive check for nil hub/buffer
	if session.Hub == nil || session.Buffer == nil {
		log.Printf("[SSE] session has nil hub/buffer: %s", sessionID)
		return nil, nil, fmt.Errorf("session not initialized: %s", sessionID)
	}

	return session.Hub, session.Buffer, nil
}

// SessionInfo represents session data for the API response.
type SessionInfo struct {
	ID           string       `json:"id"`
	State        SessionState `json:"state"`
	PlanPath     string       `json:"planPath,omitempty"`
	Branch       string       `json:"branch,omitempty"`
	Mode         string       `json:"mode,omitempty"`
	StartTime    time.Time    `json:"startTime"`
	LastModified time.Time    `json:"lastModified"`
}

// handleSessions returns a list of all discovered sessions.
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// single-session mode - return empty list
	if s.sm == nil {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
		return
	}

	sessions := s.sm.All()

	// sort by last modified (most recent first)
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].GetLastModified().After(sessions[j].GetLastModified())
	})

	// convert to API response format
	infos := make([]SessionInfo, 0, len(sessions))
	for _, session := range sessions {
		meta := session.GetMetadata()
		infos = append(infos, SessionInfo{
			ID:           session.ID,
			State:        session.GetState(),
			PlanPath:     meta.PlanPath,
			Branch:       meta.Branch,
			Mode:         meta.Mode,
			StartTime:    meta.StartTime,
			LastModified: session.GetLastModified(),
		})
	}

	data, err := json.Marshal(infos)
	if err != nil {
		log.Printf("[WARN] failed to encode sessions: %v", err)
		http.Error(w, "unable to encode sessions", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}
