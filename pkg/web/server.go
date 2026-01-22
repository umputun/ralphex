package web

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"time"
)

//go:embed templates static
var content embed.FS

// ServerConfig holds configuration for the web server.
type ServerConfig struct {
	Port     int    // port to listen on
	PlanName string // plan name to display in dashboard
	Branch   string // git branch name
}

// Server provides HTTP server for the real-time dashboard.
type Server struct {
	cfg    ServerConfig
	hub    *Hub
	buffer *Buffer
	srv    *http.Server
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
