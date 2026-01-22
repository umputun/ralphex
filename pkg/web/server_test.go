package web

import (
	"context"
	"encoding/json"
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

	srv, err := NewServer(cfg, hub, buffer)
	require.NoError(t, err)

	assert.NotNil(t, srv)
	assert.Equal(t, hub, srv.Hub())
	assert.Equal(t, buffer, srv.Buffer())
}

func TestServer_HandleIndex(t *testing.T) {
	hub := NewHub()
	buffer := NewBuffer(100)
	srv, err := NewServer(ServerConfig{
		Port:     8080,
		PlanName: "my-plan.md",
		Branch:   "feature-branch",
	}, hub, buffer)
	require.NoError(t, err)

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
		srv, err := NewServer(ServerConfig{}, hub, buffer)
		require.NoError(t, err)

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
		srv, err := NewServer(ServerConfig{}, hub, buffer)
		require.NoError(t, err)

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
		srv, err := NewServer(ServerConfig{}, hub, buffer)
		require.NoError(t, err)

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
	srv, err := NewServer(ServerConfig{
		Port:     0, // will use random port
		PlanName: "test",
		Branch:   "main",
	}, hub, buffer)
	require.NoError(t, err)

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
		srv, err := NewServer(ServerConfig{}, hub, buffer)
		require.NoError(t, err)

		err = srv.Stop()
		assert.NoError(t, err)
	})
}

func TestServer_StaticFiles(t *testing.T) {
	hub := NewHub()
	buffer := NewBuffer(100)
	srv, err := NewServer(ServerConfig{Port: 8080}, hub, buffer)
	require.NoError(t, err)

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
	srv, err := NewServer(ServerConfig{}, hub, buffer)
	require.NoError(t, err)

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

func TestServer_HandleEvents_MaxClientsExceeded(t *testing.T) {
	hub := NewHub()
	buffer := NewBuffer(100)
	srv, err := NewServer(ServerConfig{}, hub, buffer)
	require.NoError(t, err)

	// fill up hub with max clients
	for range MaxClients {
		_, err := hub.Subscribe()
		require.NoError(t, err)
	}

	// next request should get 503
	req := httptest.NewRequest(http.MethodGet, "/events", http.NoBody)
	w := httptest.NewRecorder()

	srv.handleEvents(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}

func TestServer_HandlePlan(t *testing.T) {
	t.Run("returns plan JSON", func(t *testing.T) {
		hub := NewHub()
		buffer := NewBuffer(100)

		// create a temp plan file
		tmpDir := t.TempDir()
		planFile := tmpDir + "/test-plan.md"
		planContent := `# Test Plan

### Task 1: First Task

- [ ] Item 1
- [x] Item 2
`
		require.NoError(t, os.WriteFile(planFile, []byte(planContent), 0o600))

		srv, err := NewServer(ServerConfig{
			Port:     8080,
			PlanFile: planFile,
		}, hub, buffer)
		require.NoError(t, err)

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
		srv, err := NewServer(ServerConfig{Port: 8080}, hub, buffer)
		require.NoError(t, err)

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
		srv, err := NewServer(ServerConfig{
			Port:     8080,
			PlanFile: "/nonexistent/plan.md",
		}, hub, buffer)
		require.NoError(t, err)

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
		planContent := `# Completed Plan

### Task 1: Done

- [x] Item
`
		require.NoError(t, os.WriteFile(completedPlan, []byte(planContent), 0o600))

		srv, err := NewServer(ServerConfig{
			Port:     8080,
			PlanFile: planFile,
		}, hub, buffer)
		require.NoError(t, err)

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
		srv, err := NewServer(ServerConfig{Port: 8080}, hub, buffer)
		require.NoError(t, err)

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

func TestNewServerWithSessions(t *testing.T) {
	sm := NewSessionManager()
	cfg := ServerConfig{
		Port:     8080,
		PlanName: "test-plan",
		Branch:   "main",
	}

	srv, err := NewServerWithSessions(cfg, sm)
	require.NoError(t, err)

	assert.NotNil(t, srv)
	assert.Nil(t, srv.Hub())   // no direct hub in multi-session mode
	assert.Nil(t, srv.Buffer()) // no direct buffer in multi-session mode
}

func TestServer_HandleSessions(t *testing.T) {
	t.Run("returns empty list in single-session mode", func(t *testing.T) {
		hub := NewHub()
		buffer := NewBuffer(100)
		srv, err := NewServer(ServerConfig{Port: 8080}, hub, buffer)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/api/sessions", http.NoBody)
		w := httptest.NewRecorder()

		srv.handleSessions(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "[]", string(body))
	})

	t.Run("returns sessions list in multi-session mode", func(t *testing.T) {
		tmpDir := t.TempDir()

		// create progress files
		progressContent := `# Ralphex Progress Log
Plan: docs/plans/test-plan.md
Branch: feature-branch
Mode: full
Started: 2026-01-22 10:30:00
------------------------------------------------------------
[10:30:00] Starting execution
`
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "progress-test-plan.txt"), []byte(progressContent), 0o600))

		sm := NewSessionManager()
		_, err := sm.Discover(tmpDir)
		require.NoError(t, err)

		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/api/sessions", http.NoBody)
		w := httptest.NewRecorder()

		srv.handleSessions(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var sessions []SessionInfo
		require.NoError(t, json.Unmarshal(body, &sessions))

		require.Len(t, sessions, 1)
		assert.Equal(t, "test-plan", sessions[0].ID)
		assert.Equal(t, SessionStateCompleted, sessions[0].State)
		assert.Equal(t, "docs/plans/test-plan.md", sessions[0].PlanPath)
		assert.Equal(t, "feature-branch", sessions[0].Branch)
		assert.Equal(t, "full", sessions[0].Mode)
	})

	t.Run("rejects non-GET methods", func(t *testing.T) {
		sm := NewSessionManager()
		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)

		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
			req := httptest.NewRequest(method, "/api/sessions", http.NoBody)
			w := httptest.NewRecorder()

			srv.handleSessions(w, req)

			resp := w.Result()
			resp.Body.Close()

			assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode, "method %s should be rejected", method)
			assert.Equal(t, http.MethodGet, resp.Header.Get("Allow"))
		}
	})
}

func TestServer_HandleEvents_WithSession(t *testing.T) {
	t.Run("returns 404 for unknown session", func(t *testing.T) {
		sm := NewSessionManager()
		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/events?session=nonexistent", http.NoBody)
		w := httptest.NewRecorder()

		srv.handleEvents(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("returns 404 in multi-session mode without session param", func(t *testing.T) {
		sm := NewSessionManager()
		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/events", http.NoBody)
		w := httptest.NewRecorder()

		srv.handleEvents(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("streams events for valid session", func(t *testing.T) {
		tmpDir := t.TempDir()

		// create progress file
		progressContent := `# Ralphex Progress Log
Plan: test.md
Branch: main
Mode: full
Started: 2026-01-22 10:30:00
------------------------------------------------------------
`
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "progress-mysession.txt"), []byte(progressContent), 0o600))

		sm := NewSessionManager()
		_, err := sm.Discover(tmpDir)
		require.NoError(t, err)

		// add event to session buffer
		session := sm.Get("mysession")
		require.NotNil(t, session)
		session.Buffer.Add(NewOutputEvent(progress.PhaseTask, "test event"))

		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		req := httptest.NewRequest(http.MethodGet, "/events?session=mysession", http.NoBody).WithContext(ctx)
		w := httptest.NewRecorder()

		srv.handleEvents(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
		body := w.Body.String()
		assert.Contains(t, body, "test event")
	})
}

func TestServer_HandlePlan_WithSession(t *testing.T) {
	t.Run("returns 404 for unknown session", func(t *testing.T) {
		sm := NewSessionManager()
		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/api/plan?session=nonexistent", http.NoBody)
		w := httptest.NewRecorder()

		srv.handlePlan(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		body, _ := io.ReadAll(resp.Body)
		assert.Contains(t, string(body), "session not found")
	})

	t.Run("returns 404 when session has no plan path", func(t *testing.T) {
		tmpDir := t.TempDir()

		// create progress file without plan path
		progressContent := `# Ralphex Progress Log
Branch: main
Mode: review
Started: 2026-01-22 10:30:00
------------------------------------------------------------
`
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "progress-noplan.txt"), []byte(progressContent), 0o600))

		sm := NewSessionManager()
		_, err := sm.Discover(tmpDir)
		require.NoError(t, err)

		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/api/plan?session=noplan", http.NoBody)
		w := httptest.NewRecorder()

		srv.handlePlan(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		body, _ := io.ReadAll(resp.Body)
		assert.Contains(t, string(body), "no plan file for session")
	})

	t.Run("returns plan JSON for valid session", func(t *testing.T) {
		tmpDir := t.TempDir()

		// create plan file
		planContent := `# Session Plan

### Task 1: Test Task

- [ ] Item 1
- [x] Item 2
`
		planPath := filepath.Join(tmpDir, "test-plan.md")
		require.NoError(t, os.WriteFile(planPath, []byte(planContent), 0o600))

		// create progress file referencing the plan
		progressContent := "# Ralphex Progress Log\nPlan: " + planPath + "\nBranch: main\nMode: full\nStarted: 2026-01-22 10:30:00\n------------------------------------------------------------\n"
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "progress-withplan.txt"), []byte(progressContent), 0o600))

		sm := NewSessionManager()
		_, err := sm.Discover(tmpDir)
		require.NoError(t, err)

		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/api/plan?session=withplan", http.NoBody)
		w := httptest.NewRecorder()

		srv.handlePlan(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Contains(t, string(body), "Session Plan")
		assert.Contains(t, string(body), "Test Task")
	})

	t.Run("falls back to completed directory", func(t *testing.T) {
		tmpDir := t.TempDir()
		plansDir := filepath.Join(tmpDir, "plans")
		completedDir := filepath.Join(plansDir, "completed")
		require.NoError(t, os.MkdirAll(completedDir, 0o750))

		// create plan in completed directory
		planContent := `# Completed Session Plan

### Task 1: Done

- [x] Finished
`
		completedPlanPath := filepath.Join(completedDir, "done-plan.md")
		require.NoError(t, os.WriteFile(completedPlanPath, []byte(planContent), 0o600))

		// create progress file referencing original (non-existent) path
		originalPlanPath := filepath.Join(plansDir, "done-plan.md")
		progressContent := "# Ralphex Progress Log\nPlan: " + originalPlanPath + "\nBranch: main\nMode: full\nStarted: 2026-01-22 10:30:00\n------------------------------------------------------------\n"
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "progress-fallback.txt"), []byte(progressContent), 0o600))

		sm := NewSessionManager()
		_, err := sm.Discover(tmpDir)
		require.NoError(t, err)

		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/api/plan?session=fallback", http.NoBody)
		w := httptest.NewRecorder()

		srv.handlePlan(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Contains(t, string(body), "Completed Session Plan")
	})
}
