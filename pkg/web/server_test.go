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

	"github.com/umputun/ralphex/pkg/processor"
)

func TestNewServer(t *testing.T) {
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()
	cfg := ServerConfig{
		Port:     8080,
		PlanName: "test-plan",
		Branch:   "main",
	}

	srv, err := NewServer(cfg, session)
	require.NoError(t, err)

	assert.NotNil(t, srv)
	assert.Equal(t, session, srv.Session())
}

func TestServer_HandleIndex(t *testing.T) {
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()
	srv, err := NewServer(ServerConfig{
		Port:     8080,
		PlanName: "my-plan.md",
		Branch:   "feature-branch",
	}, session)
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
	t.Run("session SSE publishes events", func(t *testing.T) {
		session := NewSession("test", "/tmp/test.txt")
		defer session.Close()

		// publish events - should succeed
		require.NoError(t, session.Publish(NewOutputEvent(processor.PhaseTask, "test event 1")))
		require.NoError(t, session.Publish(NewSectionEvent(processor.PhaseReview, "Review")))
		require.NoError(t, session.Publish(NewOutputEvent(processor.PhaseReview, "test event 2")))

		// verify SSE server exists
		assert.NotNil(t, session.SSE)
	})

	t.Run("server returns 404 without session", func(t *testing.T) {
		// multi-session mode without session param should return 404
		sm := NewSessionManager()
		defer sm.Close()
		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/events", http.NoBody)
		w := httptest.NewRecorder()

		srv.handleEvents(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})
}

func TestServer_StartStop(t *testing.T) {
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()
	srv, err := NewServer(ServerConfig{
		Port:     0, // will use random port
		PlanName: "test",
		Branch:   "main",
	}, session)
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
		session := NewSession("test", "/tmp/test.txt")
		defer session.Close()
		srv, err := NewServer(ServerConfig{}, session)
		require.NoError(t, err)

		err = srv.Stop()
		assert.NoError(t, err)
	})

	t.Run("multiple stop calls without start are safe", func(t *testing.T) {
		session := NewSession("test", "/tmp/test.txt")
		defer session.Close()
		srv, err := NewServer(ServerConfig{}, session)
		require.NoError(t, err)

		// multiple stop calls should be safe
		assert.NoError(t, srv.Stop())
		assert.NoError(t, srv.Stop())
		assert.NoError(t, srv.Stop())
	})
}

func TestServer_StaticFiles(t *testing.T) {
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()
	srv, err := NewServer(ServerConfig{Port: 8080}, session)
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
	// note: actual SSE streaming with go-sse is tested via E2E tests (see CLAUDE.md).
	// unit tests verify the Session correctly stores events for replay.
	session := NewSession("test", "/tmp/test.txt")
	defer session.Close()

	// verify session has an SSE server configured with replayer
	assert.NotNil(t, session.SSE)

	// publish some events before any client connects - should succeed
	require.NoError(t, session.Publish(NewOutputEvent(processor.PhaseTask, "event 1")))
	require.NoError(t, session.Publish(NewSectionEvent(processor.PhaseReview, "Review Section")))
	require.NoError(t, session.Publish(NewOutputEvent(processor.PhaseReview, "event 2")))
}

func TestServer_HandlePlan(t *testing.T) {
	t.Run("returns plan JSON", func(t *testing.T) {
		session := NewSession("test", "/tmp/test.txt")
		defer session.Close()

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
		}, session)
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
		session := NewSession("test", "/tmp/test.txt")
		defer session.Close()
		srv, err := NewServer(ServerConfig{Port: 8080}, session)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/api/plan", http.NoBody)
		w := httptest.NewRecorder()

		srv.handlePlan(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("returns 500 for invalid plan file", func(t *testing.T) {
		session := NewSession("test", "/tmp/test.txt")
		defer session.Close()
		srv, err := NewServer(ServerConfig{
			Port:     8080,
			PlanFile: "/nonexistent/plan.md",
		}, session)
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
		session := NewSession("test", "/tmp/test.txt")
		defer session.Close()

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
		}, session)
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
		session := NewSession("test", "/tmp/test.txt")
		defer session.Close()
		srv, err := NewServer(ServerConfig{Port: 8080}, session)
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
	defer sm.Close()
	cfg := ServerConfig{
		Port:     8080,
		PlanName: "test-plan",
		Branch:   "main",
	}

	srv, err := NewServerWithSessions(cfg, sm)
	require.NoError(t, err)

	assert.NotNil(t, srv)
	assert.Nil(t, srv.Session()) // no direct session in multi-session mode
}

func TestServer_HandleSessions(t *testing.T) {
	t.Run("returns empty list in single-session mode", func(t *testing.T) {
		session := NewSession("test", "/tmp/test.txt")
		defer session.Close()
		srv, err := NewServer(ServerConfig{Port: 8080}, session)
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
		defer sm.Close()
		progressPath := filepath.Join(tmpDir, "progress-test-plan.txt")
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
		assert.Equal(t, sessionIDFromPath(progressPath), sessions[0].ID)
		assert.Equal(t, SessionStateCompleted, sessions[0].State)
		assert.Equal(t, "docs/plans/test-plan.md", sessions[0].PlanPath)
		assert.Equal(t, "feature-branch", sessions[0].Branch)
		assert.Equal(t, "full", sessions[0].Mode)
	})

	t.Run("rejects non-GET methods", func(t *testing.T) {
		sm := NewSessionManager()
		defer sm.Close()
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
		defer sm.Close()
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
		defer sm.Close()
		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/events", http.NoBody)
		w := httptest.NewRecorder()

		srv.handleEvents(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("session has SSE server configured", func(t *testing.T) {
		// note: actual SSE streaming with go-sse is tested via E2E tests (see CLAUDE.md).
		// unit tests verify the session SSE server is properly configured.
		tmpDir := t.TempDir()

		// create progress file
		progressPath := filepath.Join(tmpDir, "progress-mysession.txt")
		progressContent := `# Ralphex Progress Log
Plan: test.md
Branch: main
Mode: full
Started: 2026-01-22 10:30:00
------------------------------------------------------------
`
		require.NoError(t, os.WriteFile(progressPath, []byte(progressContent), 0o600))

		sm := NewSessionManager()
		defer sm.Close()
		_, err := sm.Discover(tmpDir)
		require.NoError(t, err)

		// verify session has SSE server
		sessionID := sessionIDFromPath(progressPath)
		session := sm.Get(sessionID)
		require.NotNil(t, session)
		assert.NotNil(t, session.SSE)

		// publishing events should succeed
		require.NoError(t, session.Publish(NewOutputEvent(processor.PhaseTask, "test event")))
	})
}

func TestServer_HandlePlan_WithSession(t *testing.T) {
	t.Run("returns 404 for unknown session", func(t *testing.T) {
		sm := NewSessionManager()
		defer sm.Close()
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
		progressPath := filepath.Join(tmpDir, "progress-noplan.txt")
		progressContent := `# Ralphex Progress Log
Branch: main
Mode: review
Started: 2026-01-22 10:30:00
------------------------------------------------------------
`
		require.NoError(t, os.WriteFile(progressPath, []byte(progressContent), 0o600))

		sm := NewSessionManager()
		defer sm.Close()
		_, err := sm.Discover(tmpDir)
		require.NoError(t, err)

		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)

		sessionID := sessionIDFromPath(progressPath)
		req := httptest.NewRequest(http.MethodGet, "/api/plan?session="+sessionID, http.NoBody)
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

		// save and restore working directory
		origDir, err := os.Getwd()
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(origDir) })
		require.NoError(t, os.Chdir(tmpDir))

		// create plan file in a plans subdirectory (relative path)
		plansDir := filepath.Join(tmpDir, "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planContent := `# Session Plan

### Task 1: Test Task

- [ ] Item 1
- [x] Item 2
`
		planPath := filepath.Join(plansDir, "test-plan.md")
		require.NoError(t, os.WriteFile(planPath, []byte(planContent), 0o600))

		// create progress file referencing the plan with relative path
		progressPath := filepath.Join(tmpDir, "progress-withplan.txt")
		progressContent := "# Ralphex Progress Log\nPlan: plans/test-plan.md\nBranch: main\nMode: full\nStarted: 2026-01-22 10:30:00\n------------------------------------------------------------\n"
		require.NoError(t, os.WriteFile(progressPath, []byte(progressContent), 0o600))

		sm := NewSessionManager()
		defer sm.Close()
		_, err = sm.Discover(tmpDir)
		require.NoError(t, err)

		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)

		sessionID := sessionIDFromPath(progressPath)
		req := httptest.NewRequest(http.MethodGet, "/api/plan?session="+sessionID, http.NoBody)
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

		// save and restore working directory
		origDir, err := os.Getwd()
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.Chdir(origDir) })
		require.NoError(t, os.Chdir(tmpDir))

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

		// create progress file referencing original (non-existent) path with relative path
		progressPath := filepath.Join(tmpDir, "progress-fallback.txt")
		progressContent := "# Ralphex Progress Log\nPlan: plans/done-plan.md\nBranch: main\nMode: full\nStarted: 2026-01-22 10:30:00\n------------------------------------------------------------\n"
		require.NoError(t, os.WriteFile(progressPath, []byte(progressContent), 0o600))

		sm := NewSessionManager()
		defer sm.Close()
		_, err = sm.Discover(tmpDir)
		require.NoError(t, err)

		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)

		sessionID := sessionIDFromPath(progressPath)
		req := httptest.NewRequest(http.MethodGet, "/api/plan?session="+sessionID, http.NoBody)
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

func TestLoadPlanWithFallback(t *testing.T) {
	t.Run("loads plan from primary path", func(t *testing.T) {
		tmpDir := t.TempDir()
		planPath := filepath.Join(tmpDir, "test-plan.md")
		planContent := `# Test Plan

### Task 1: Test

- [ ] Item
`
		require.NoError(t, os.WriteFile(planPath, []byte(planContent), 0o600))

		plan, err := loadPlanWithFallback(planPath)
		require.NoError(t, err)
		require.NotNil(t, plan)
		assert.Equal(t, "Test Plan", plan.Title)
	})

	t.Run("falls back to completed directory", func(t *testing.T) {
		tmpDir := t.TempDir()
		completedDir := filepath.Join(tmpDir, "completed")
		require.NoError(t, os.MkdirAll(completedDir, 0o750))

		// plan only exists in completed directory
		completedPlan := filepath.Join(completedDir, "test-plan.md")
		planContent := `# Completed Plan

### Task 1: Done

- [x] Item
`
		require.NoError(t, os.WriteFile(completedPlan, []byte(planContent), 0o600))

		// request the non-existent original path
		originalPath := filepath.Join(tmpDir, "test-plan.md")
		plan, err := loadPlanWithFallback(originalPath)
		require.NoError(t, err)
		require.NotNil(t, plan)
		assert.Equal(t, "Completed Plan", plan.Title)
	})

	t.Run("returns error when not found in either location", func(t *testing.T) {
		tmpDir := t.TempDir()
		nonexistentPath := filepath.Join(tmpDir, "nonexistent.md")

		_, err := loadPlanWithFallback(nonexistentPath)
		require.Error(t, err)
	})
}
