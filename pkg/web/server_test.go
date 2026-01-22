package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/progress"
)

func TestNewServer(t *testing.T) {
	hub := NewHub()
	buffer := NewBuffer(100)
	cfg := ServerConfig{
		Port:     8080,
		PlanName: "test-plan",
		Branch:   "main",
	}

	srv := NewServer(cfg, hub, buffer)

	assert.NotNil(t, srv)
	assert.Equal(t, hub, srv.Hub())
	assert.Equal(t, buffer, srv.Buffer())
}

func TestServer_HandleIndex(t *testing.T) {
	hub := NewHub()
	buffer := NewBuffer(100)
	srv := NewServer(ServerConfig{
		Port:     8080,
		PlanName: "my-plan.md",
		Branch:   "feature-branch",
	}, hub, buffer)

	t.Run("serves index page", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		w := httptest.NewRecorder()

		srv.handleIndex(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Contains(t, resp.Header.Get("Content-Type"), "text/html")

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		bodyStr := string(body)
		assert.Contains(t, bodyStr, "Ralphex Dashboard")
		assert.Contains(t, bodyStr, "my-plan.md")
		assert.Contains(t, bodyStr, "feature-branch")
	})

	t.Run("returns 404 for non-root paths", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/other", http.NoBody)
		w := httptest.NewRecorder()

		srv.handleIndex(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})
}

func TestServer_HandleEvents(t *testing.T) {
	t.Run("sets SSE headers", func(t *testing.T) {
		hub := NewHub()
		buffer := NewBuffer(100)
		srv := NewServer(ServerConfig{}, hub, buffer)

		// use a context that cancels quickly
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		req := httptest.NewRequest(http.MethodGet, "/events", http.NoBody).WithContext(ctx)
		w := httptest.NewRecorder()

		srv.handleEvents(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
		assert.Equal(t, "no-cache", resp.Header.Get("Cache-Control"))
		assert.Equal(t, "keep-alive", resp.Header.Get("Connection"))
	})

	t.Run("sends history on connect", func(t *testing.T) {
		hub := NewHub()
		buffer := NewBuffer(100)
		srv := NewServer(ServerConfig{}, hub, buffer)

		// add some history
		buffer.Add(NewOutputEvent(progress.PhaseTask, "historical event"))

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		req := httptest.NewRequest(http.MethodGet, "/events", http.NoBody).WithContext(ctx)
		w := httptest.NewRecorder()

		srv.handleEvents(w, req)

		body := w.Body.String()
		assert.Contains(t, body, "historical event")
	})

	t.Run("streams new events", func(t *testing.T) {
		hub := NewHub()
		buffer := NewBuffer(100)
		srv := NewServer(ServerConfig{}, hub, buffer)

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		req := httptest.NewRequest(http.MethodGet, "/events", http.NoBody).WithContext(ctx)
		w := httptest.NewRecorder()

		// start handler in goroutine
		done := make(chan struct{})
		go func() {
			srv.handleEvents(w, req)
			close(done)
		}()

		// give handler time to subscribe
		time.Sleep(50 * time.Millisecond)

		// broadcast event
		hub.Broadcast(NewOutputEvent(progress.PhaseTask, "live event"))

		// wait for handler to finish
		<-done

		body := w.Body.String()
		assert.Contains(t, body, "live event")
	})
}

func TestServer_StartStop(t *testing.T) {
	hub := NewHub()
	buffer := NewBuffer(100)
	srv := NewServer(ServerConfig{
		Port:     0, // will use random port
		PlanName: "test",
		Branch:   "main",
	}, hub, buffer)

	ctx, cancel := context.WithCancel(context.Background())

	// start server in goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start(ctx)
	}()

	// give server time to start
	time.Sleep(50 * time.Millisecond)

	// cancel context to trigger shutdown
	cancel()

	// wait for server to stop
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop in time")
	}
}

func TestServer_Stop(t *testing.T) {
	t.Run("stop without start is safe", func(t *testing.T) {
		hub := NewHub()
		buffer := NewBuffer(100)
		srv := NewServer(ServerConfig{}, hub, buffer)

		err := srv.Stop()
		assert.NoError(t, err)
	})
}

func TestServer_StaticFiles(t *testing.T) {
	hub := NewHub()
	buffer := NewBuffer(100)
	srv := NewServer(ServerConfig{Port: 8080}, hub, buffer)

	mux := http.NewServeMux()
	mux.HandleFunc("/", srv.handleIndex)
	mux.HandleFunc("/events", srv.handleEvents)

	// test that static files would be accessible through the mux
	// we can't easily test the full static handler here, but we verify
	// the CSS file exists in embed
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	w := httptest.NewRecorder()
	srv.handleIndex(w, req)

	body := w.Body.String()
	// verify the index page references static files
	assert.Contains(t, body, "/static/styles.css")
	assert.Contains(t, body, "/static/app.js")
}

func TestServer_SSE_LateJoiningClient(t *testing.T) {
	hub := NewHub()
	buffer := NewBuffer(100)
	srv := NewServer(ServerConfig{}, hub, buffer)

	// broadcast some events before any client connects
	hub.Broadcast(NewOutputEvent(progress.PhaseTask, "event 1"))
	buffer.Add(NewOutputEvent(progress.PhaseTask, "event 1"))

	hub.Broadcast(NewSectionEvent(progress.PhaseReview, "Review Section"))
	buffer.Add(NewSectionEvent(progress.PhaseReview, "Review Section"))

	hub.Broadcast(NewOutputEvent(progress.PhaseReview, "event 2"))
	buffer.Add(NewOutputEvent(progress.PhaseReview, "event 2"))

	// now a late-joining client connects
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/events", http.NoBody).WithContext(ctx)
	w := httptest.NewRecorder()

	srv.handleEvents(w, req)

	body := w.Body.String()
	// late-joining client should receive all historical events
	assert.Contains(t, body, "event 1")
	assert.Contains(t, body, "Review Section")
	assert.Contains(t, body, "event 2")
}

func TestServer_HandlePlan(t *testing.T) {
	t.Run("returns plan JSON", func(t *testing.T) {
		hub := NewHub()
		buffer := NewBuffer(100)

		// create a temp plan file
		tmpDir := t.TempDir()
		planFile := tmpDir + "/test-plan.md"
		content := `# Test Plan

### Task 1: First Task

- [ ] Item 1
- [x] Item 2
`
		require.NoError(t, os.WriteFile(planFile, []byte(content), 0o600))

		srv := NewServer(ServerConfig{
			Port:     8080,
			PlanFile: planFile,
		}, hub, buffer)

		req := httptest.NewRequest(http.MethodGet, "/api/plan", http.NoBody)
		w := httptest.NewRecorder()

		srv.handlePlan(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		assert.Contains(t, string(body), "Test Plan")
		assert.Contains(t, string(body), "First Task")
	})

	t.Run("returns 404 when no plan file", func(t *testing.T) {
		hub := NewHub()
		buffer := NewBuffer(100)
		srv := NewServer(ServerConfig{Port: 8080}, hub, buffer)

		req := httptest.NewRequest(http.MethodGet, "/api/plan", http.NoBody)
		w := httptest.NewRecorder()

		srv.handlePlan(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("returns 500 for invalid plan file", func(t *testing.T) {
		hub := NewHub()
		buffer := NewBuffer(100)
		srv := NewServer(ServerConfig{
			Port:     8080,
			PlanFile: "/nonexistent/plan.md",
		}, hub, buffer)

		req := httptest.NewRequest(http.MethodGet, "/api/plan", http.NoBody)
		w := httptest.NewRecorder()

		srv.handlePlan(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)

		// verify error message is sanitized (no file path leaked)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.NotContains(t, string(body), "/nonexistent/plan.md")
		assert.Contains(t, string(body), "unable to load plan")
	})

	t.Run("loads plan from completed directory", func(t *testing.T) {
		hub := NewHub()
		buffer := NewBuffer(100)

		tmpDir := t.TempDir()
		planFile := filepath.Join(tmpDir, "plan.md")
		completedDir := filepath.Join(tmpDir, "completed")
		require.NoError(t, os.MkdirAll(completedDir, 0o750))
		completedPlan := filepath.Join(completedDir, "plan.md")
		content := `# Completed Plan

### Task 1: Done

- [x] Item
`
		require.NoError(t, os.WriteFile(completedPlan, []byte(content), 0o600))

		srv := NewServer(ServerConfig{
			Port:     8080,
			PlanFile: planFile,
		}, hub, buffer)

		req := httptest.NewRequest(http.MethodGet, "/api/plan", http.NoBody)
		w := httptest.NewRecorder()

		srv.handlePlan(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Contains(t, string(body), "Completed Plan")
	})

	t.Run("rejects non-GET methods", func(t *testing.T) {
		hub := NewHub()
		buffer := NewBuffer(100)
		srv := NewServer(ServerConfig{Port: 8080}, hub, buffer)

		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
			req := httptest.NewRequest(method, "/api/plan", http.NoBody)
			w := httptest.NewRecorder()

			srv.handlePlan(w, req)

			resp := w.Result()
			resp.Body.Close()

			assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode, "method %s should be rejected", method)
			assert.Equal(t, http.MethodGet, resp.Header.Get("Allow"))
		}
	})
}
