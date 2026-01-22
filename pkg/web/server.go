package web

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"path/filepath"
	"sync"
	"time"
)

//go:embed templates static
var content embed.FS

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
	hub    *Hub
	buffer *Buffer
	srv    *http.Server

	// plan caching - set after first successful load
	planMu    sync.Mutex
	planCache *Plan
}

// NewServer creates a new web server.
func NewServer(cfg ServerConfig, hub *Hub, buffer *Buffer) *Server {
	return &Server{
		cfg:    cfg,
		hub:    hub,
		buffer: buffer,
	}
}

// Start begins listening for HTTP requests.
// blocks until the server is stopped or an error occurs.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	// register routes
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/events", s.handleEvents)
	mux.HandleFunc("/api/plan", s.handlePlan)

	// static files
	staticFS, err := fs.Sub(content, "static")
	if err != nil {
		return fmt.Errorf("static filesystem: %w", err)
	}
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	s.srv = &http.Server{
		Addr:              fmt.Sprintf(":%d", s.cfg.Port),
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

	// parse template from embedded filesystem
	tmpl, err := template.ParseFS(content, "templates/base.html")
	if err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	data := templateData{
		PlanName: s.cfg.PlanName,
		Branch:   s.cfg.Branch,
	}

	if err := tmpl.Execute(w, data); err != nil {
		http.Error(w, "template execution error", http.StatusInternalServerError)
		return
	}
}

// handlePlan serves the parsed plan as JSON.
// caches the result (success or failure) of the first load attempt to survive file moves.
// once cached, subsequent requests return the cached result without re-reading the file.
// thread-safe via sync.Once for concurrent request handling.
func (s *Server) handlePlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

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

// handleEvents serves the SSE stream.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
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
	eventCh := s.hub.Subscribe()
	defer s.hub.Unsubscribe(eventCh)

	// send history first
	for _, event := range s.buffer.All() {
		data, err := event.JSON()
		if err != nil {
			continue
		}
		fmt.Fprintf(w, "data: %s\n\n", data)
	}
	flusher.Flush()

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
