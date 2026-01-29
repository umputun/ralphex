package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/config"
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

func TestExtractProjectDir(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected string
	}{
		{
			name:     "nested path extracts parent directory",
			path:     "/home/user/project/progress-test.txt",
			expected: "project",
		},
		{
			name:     "deeply nested path",
			path:     "/home/user/dev/projects/myapp/progress.txt",
			expected: "myapp",
		},
		{
			name:     "root level file returns Unknown",
			path:     "/progress.txt",
			expected: "Unknown",
		},
		{
			name:     "relative path with dot returns Unknown",
			path:     "progress.txt",
			expected: "Unknown",
		},
		{
			name:     "explicit dot-slash returns Unknown",
			path:     "./progress.txt",
			expected: "Unknown",
		},
		{
			name:     "parent reference returns Unknown",
			path:     "../progress.txt",
			expected: "Unknown",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := extractProjectDir(tc.path)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestServer_HandleStartPlan(t *testing.T) {
	t.Run("returns error when plan runner not configured", func(t *testing.T) {
		session := NewSession("test", "/tmp/test.txt")
		defer session.Close()
		srv, err := NewServer(ServerConfig{Port: 8080}, session)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/api/plan", strings.NewReader(`{"dir":"/tmp","description":"test"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		srv.handleStartPlan(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	})

	t.Run("rejects non-POST methods", func(t *testing.T) {
		sm := NewSessionManager()
		defer sm.Close()
		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)

		for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
			req := httptest.NewRequest(method, "/api/plan", http.NoBody)
			w := httptest.NewRecorder()

			srv.handleStartPlan(w, req)

			resp := w.Result()
			assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
			resp.Body.Close()
		}
	})

	t.Run("rejects invalid JSON", func(t *testing.T) {
		cfg := testConfigForServer(t)
		sm := NewSessionManager()
		defer sm.Close()
		runner := NewPlanRunner(cfg, sm)
		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)
		srv.SetPlanRunner(runner)

		req := httptest.NewRequest(http.MethodPost, "/api/plan", strings.NewReader("not json"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		srv.handleStartPlan(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})
}

func TestServer_HandleAnswer(t *testing.T) {
	t.Run("returns error when plan runner not configured", func(t *testing.T) {
		session := NewSession("test", "/tmp/test.txt")
		defer session.Close()
		srv, err := NewServer(ServerConfig{Port: 8080}, session)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/api/answer", strings.NewReader(`{"session_id":"x","question_id":"y","answer":"z"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		srv.handleAnswer(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	})

	t.Run("rejects non-POST methods", func(t *testing.T) {
		sm := NewSessionManager()
		defer sm.Close()
		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)

		for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
			req := httptest.NewRequest(method, "/api/answer", http.NoBody)
			w := httptest.NewRecorder()

			srv.handleAnswer(w, req)

			resp := w.Result()
			assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
			resp.Body.Close()
		}
	})

	t.Run("rejects invalid JSON", func(t *testing.T) {
		cfg := testConfigForServer(t)
		sm := NewSessionManager()
		defer sm.Close()
		runner := NewPlanRunner(cfg, sm)
		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)
		srv.SetPlanRunner(runner)

		req := httptest.NewRequest(http.MethodPost, "/api/answer", strings.NewReader("not json"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		srv.handleAnswer(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})
}

func TestServer_HandleCancelSession(t *testing.T) {
	t.Run("returns error when plan runner not configured", func(t *testing.T) {
		session := NewSession("test", "/tmp/test.txt")
		defer session.Close()
		srv, err := NewServer(ServerConfig{Port: 8080}, session)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/api/sessions/test-id/cancel", http.NoBody)
		w := httptest.NewRecorder()

		srv.handleCancelSession(w, req, "test-id")

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	})

	t.Run("rejects non-POST methods", func(t *testing.T) {
		sm := NewSessionManager()
		defer sm.Close()
		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)

		for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
			req := httptest.NewRequest(method, "/api/sessions/test-id/cancel", http.NoBody)
			w := httptest.NewRecorder()

			srv.handleCancelSession(w, req, "test-id")

			resp := w.Result()
			assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
			resp.Body.Close()
		}
	})
}

// testConfigForServer returns a minimal config for testing server endpoints.
func testConfigForServer(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		ClaudeCommand: "echo",
		PlansDir:      t.TempDir(),
		Colors: config.ColorConfig{
			Task:       "0,255,0",
			Review:     "0,255,255",
			Codex:      "255,0,255",
			ClaudeEval: "100,200,255",
			Warn:       "255,255,0",
			Error:      "255,0,0",
			Signal:     "0,255,0",
			Timestamp:  "150,150,150",
			Info:       "200,200,200",
		},
	}
}

func TestServer_HandleRecentDirs(t *testing.T) {
	t.Run("returns empty list when not configured", func(t *testing.T) {
		session := NewSession("test", "/tmp/test.txt")
		defer session.Close()
		srv, err := NewServer(ServerConfig{Port: 8080}, session)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/api/recent-dirs", http.NoBody)
		w := httptest.NewRecorder()

		srv.handleRecentDirs(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var result struct {
			Dirs []string `json:"dirs"`
		}
		require.NoError(t, json.Unmarshal(body, &result))
		assert.Empty(t, result.Dirs)
	})

	t.Run("returns directories from config.ProjectDirs", func(t *testing.T) {
		sm := NewSessionManager()
		defer sm.Close()

		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)

		// set up plan runner with config containing project dirs
		cfg := &config.Config{
			ProjectDirs: []string{"/path/project1", "/path/project2"},
		}
		srv.planRunner = NewPlanRunner(cfg, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/recent-dirs", http.NoBody)
		w := httptest.NewRecorder()

		srv.handleRecentDirs(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var result struct {
			Dirs []string `json:"dirs"`
		}
		require.NoError(t, json.Unmarshal(body, &result))
		assert.Len(t, result.Dirs, 2)
		assert.Equal(t, "/path/project1", result.Dirs[0])
		assert.Equal(t, "/path/project2", result.Dirs[1])
	})

	t.Run("includes directories from sessions", func(t *testing.T) {
		sm := NewSessionManager()
		defer sm.Close()

		// register sessions with different paths
		session1 := NewSession("session1", "/project/alpha/progress-test1.txt")
		session2 := NewSession("session2", "/project/beta/progress-test2.txt")
		sm.Register(session1)
		sm.Register(session2)

		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/api/recent-dirs", http.NoBody)
		w := httptest.NewRecorder()

		srv.handleRecentDirs(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var result struct {
			Dirs []string `json:"dirs"`
		}
		require.NoError(t, json.Unmarshal(body, &result))
		assert.Len(t, result.Dirs, 2)
		assert.Contains(t, result.Dirs, "/project/alpha")
		assert.Contains(t, result.Dirs, "/project/beta")
	})

	t.Run("deduplicates directories from config and sessions", func(t *testing.T) {
		sm := NewSessionManager()
		defer sm.Close()

		// register a session
		session := NewSession("session1", "/shared/project/progress-test.txt")
		sm.Register(session)

		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)

		// set up plan runner with config containing project dirs (one overlaps with session)
		cfg := &config.Config{
			ProjectDirs: []string{"/shared/project", "/other/project"},
		}
		srv.planRunner = NewPlanRunner(cfg, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/recent-dirs", http.NoBody)
		w := httptest.NewRecorder()

		srv.handleRecentDirs(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var result struct {
			Dirs []string `json:"dirs"`
		}
		require.NoError(t, json.Unmarshal(body, &result))
		// should only have 2 entries - /shared/project appears once (deduped)
		assert.Len(t, result.Dirs, 2)
		assert.Equal(t, "/shared/project", result.Dirs[0]) // from config first
		assert.Equal(t, "/other/project", result.Dirs[1])
	})

	t.Run("watch_only_returns_watch_dirs_only", func(t *testing.T) {
		sm := NewSessionManager()
		defer sm.Close()

		session := NewSession("session1", "/other/project/progress-test.txt")
		sm.Register(session)

		srv, err := NewServerWithSessions(ServerConfig{Port: 8080, PlanName: "(watch mode)"}, sm)
		require.NoError(t, err)

		cfg := &config.Config{
			ProjectDirs: []string{"/path/project1"},
			WatchDirs:   []string{"/watch/root"},
		}
		srv.planRunner = NewPlanRunner(cfg, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/recent-dirs", http.NoBody)
		w := httptest.NewRecorder()

		srv.handleRecentDirs(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var result struct {
			Dirs   []string `json:"dirs"`
			Locked bool     `json:"locked"`
		}
		require.NoError(t, json.Unmarshal(body, &result))
		assert.Equal(t, []string{"/watch/root"}, result.Dirs)
		assert.True(t, result.Locked)
	})

	t.Run("watch_only_with_explicit_watch_dirs_uses_standard_sources", func(t *testing.T) {
		sm := NewSessionManager()
		defer sm.Close()

		session := NewSession("session1", "/project/alpha/progress-test.txt")
		sm.Register(session)

		srv, err := NewServerWithSessions(ServerConfig{
			Port:          8080,
			PlanName:      "(watch mode)",
			WatchExplicit: true,
		}, sm)
		require.NoError(t, err)

		cfg := &config.Config{
			ProjectDirs: []string{"/path/project1"},
			WatchDirs:   []string{"/watch/a", "/watch/b"},
		}
		srv.planRunner = NewPlanRunner(cfg, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/recent-dirs", http.NoBody)
		w := httptest.NewRecorder()

		srv.handleRecentDirs(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var result struct {
			Dirs   []string `json:"dirs"`
			Locked bool     `json:"locked"`
		}
		require.NoError(t, json.Unmarshal(body, &result))
		assert.Equal(t, []string{"/path/project1", "/project/alpha"}, result.Dirs)
		assert.False(t, result.Locked)
	})

	t.Run("rejects non-GET methods", func(t *testing.T) {
		session := NewSession("test", "/tmp/test.txt")
		defer session.Close()
		srv, err := NewServer(ServerConfig{Port: 8080}, session)
		require.NoError(t, err)

		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
			req := httptest.NewRequest(method, "/api/recent-dirs", http.NoBody)
			w := httptest.NewRecorder()

			srv.handleRecentDirs(w, req)

			resp := w.Result()
			assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
			resp.Body.Close()
		}
	})
}

func TestServer_HandleResumable(t *testing.T) {
	t.Run("returns error when plan runner not configured", func(t *testing.T) {
		session := NewSession("test", "/tmp/test.txt")
		defer session.Close()
		srv, err := NewServer(ServerConfig{Port: 8080}, session)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/api/resumable", http.NoBody)
		w := httptest.NewRecorder()

		srv.handleResumable(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	})

	t.Run("rejects non-GET methods", func(t *testing.T) {
		sm := NewSessionManager()
		defer sm.Close()
		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)

		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
			req := httptest.NewRequest(method, "/api/resumable", http.NoBody)
			w := httptest.NewRecorder()

			srv.handleResumable(w, req)

			resp := w.Result()
			assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
			resp.Body.Close()
		}
	})

	t.Run("returns empty list when no resumable sessions", func(t *testing.T) {
		cfg := testConfigForServer(t)
		sm := NewSessionManager()
		defer sm.Close()
		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)
		srv.planRunner = NewPlanRunner(cfg, sm)

		req := httptest.NewRequest(http.MethodGet, "/api/resumable", http.NoBody)
		w := httptest.NewRecorder()

		srv.handleResumable(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

		var result ResumableResponse
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
		assert.NotNil(t, result.Sessions)
		assert.Empty(t, result.Sessions)
	})

	t.Run("returns resumable sessions from project dirs", func(t *testing.T) {
		tmpDir := t.TempDir()

		// create a resumable progress file
		progressContent := `# Ralphex Progress Log
Plan: add authentication
Branch: main
Mode: plan
Started: 2026-01-25 10:30:00
------------------------------------------------------------

[26-01-25 10:30:01] Starting plan...
[26-01-25 10:30:05] <<<RALPHEX:QUESTION>>>
[26-01-25 10:30:05] {"question": "Pick an auth flow?", "options": ["Password", "SSO"]}
[26-01-25 10:30:05] <<<RALPHEX:END>>>
`
		progressPath := filepath.Join(tmpDir, "progress-plan-auth.txt")
		require.NoError(t, os.WriteFile(progressPath, []byte(progressContent), 0o600))

		cfg := testConfigForServer(t)
		cfg.ProjectDirs = []string{tmpDir}
		sm := NewSessionManager()
		defer sm.Close()
		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)
		srv.planRunner = NewPlanRunner(cfg, sm)

		req := httptest.NewRequest(http.MethodGet, "/api/resumable", http.NoBody)
		w := httptest.NewRecorder()

		srv.handleResumable(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result ResumableResponse
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
		require.Len(t, result.Sessions, 1)
		assert.Equal(t, "add authentication", result.Sessions[0].PlanDescription)
		assert.Equal(t, progressPath, result.Sessions[0].ProgressPath)
		assert.Equal(t, "Pick an auth flow?", result.Sessions[0].PendingQuestion)
		assert.Equal(t, []string{"Password", "SSO"}, result.Sessions[0].PendingOptions)
	})
}

func TestServer_HandlePlanDispatch(t *testing.T) {
	t.Run("routes GET to handlePlan", func(t *testing.T) {
		session := NewSession("test", "/tmp/test.txt")
		defer session.Close()

		// create a temp plan file
		tmpDir := t.TempDir()
		planFile := tmpDir + "/test-plan.md"
		planContent := `# Test Plan

### Task 1: First Task

- [ ] Item 1
`
		require.NoError(t, os.WriteFile(planFile, []byte(planContent), 0o600))

		srv, err := NewServer(ServerConfig{
			Port:     8080,
			PlanFile: planFile,
		}, session)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/api/plan", http.NoBody)
		w := httptest.NewRecorder()

		srv.handlePlanDispatch(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
	})

	t.Run("routes POST to handleStartPlan", func(t *testing.T) {
		session := NewSession("test", "/tmp/test.txt")
		defer session.Close()
		srv, err := NewServer(ServerConfig{Port: 8080}, session)
		require.NoError(t, err)

		// no planRunner configured, should return 503
		req := httptest.NewRequest(http.MethodPost, "/api/plan", strings.NewReader(`{"dir":"/tmp","description":"test"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		srv.handlePlanDispatch(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	})

	t.Run("rejects other methods", func(t *testing.T) {
		session := NewSession("test", "/tmp/test.txt")
		defer session.Close()
		srv, err := NewServer(ServerConfig{Port: 8080}, session)
		require.NoError(t, err)

		for _, method := range []string{http.MethodPut, http.MethodDelete, http.MethodPatch} {
			req := httptest.NewRequest(method, "/api/plan", http.NoBody)
			w := httptest.NewRecorder()

			srv.handlePlanDispatch(w, req)

			resp := w.Result()
			resp.Body.Close()

			assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode, "method %s should be rejected", method)
			assert.Equal(t, "GET, POST", resp.Header.Get("Allow"))
		}
	})
}

func TestServer_HandleSessionsSubpath(t *testing.T) {
	t.Run("returns 404 for empty session ID", func(t *testing.T) {
		sm := NewSessionManager()
		defer sm.Close()
		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/api/sessions/", http.NoBody)
		w := httptest.NewRecorder()

		srv.handleSessionsSubpath(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("routes to cancel handler", func(t *testing.T) {
		sm := NewSessionManager()
		defer sm.Close()
		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)

		// no planRunner configured, should return 503
		req := httptest.NewRequest(http.MethodPost, "/api/sessions/test-id/cancel", http.NoBody)
		w := httptest.NewRecorder()

		srv.handleSessionsSubpath(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	})

	t.Run("returns 404 for unknown action", func(t *testing.T) {
		sm := NewSessionManager()
		defer sm.Close()
		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/api/sessions/test-id/unknown", http.NoBody)
		w := httptest.NewRecorder()

		srv.handleSessionsSubpath(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("returns 404 for session ID only (no action)", func(t *testing.T) {
		sm := NewSessionManager()
		defer sm.Close()
		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/api/sessions/test-id", http.NoBody)
		w := httptest.NewRecorder()

		srv.handleSessionsSubpath(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})
}

func TestServer_GetSingleSession(t *testing.T) {
	t.Run("returns error when no session configured and no ID", func(t *testing.T) {
		srv := &Server{} // no session, no planRunner

		_, err := srv.getSingleSession("")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no session specified")
	})

	t.Run("returns server session when no ID specified", func(t *testing.T) {
		session := NewSession("test", "/tmp/test.txt")
		defer session.Close()
		srv, err := NewServer(ServerConfig{Port: 8080}, session)
		require.NoError(t, err)

		got, err := srv.getSingleSession("")
		require.NoError(t, err)
		assert.Equal(t, session, got)
	})

	t.Run("returns planRunner session by ID", func(t *testing.T) {
		session := NewSession("test", "/tmp/test.txt")
		defer session.Close()
		srv, err := NewServer(ServerConfig{Port: 8080}, session)
		require.NoError(t, err)

		// set up plan runner with a session
		runner := NewPlanRunner(&config.Config{}, nil)
		planSession := NewSession("plan-session-id", "/tmp/plan-progress.txt")
		defer planSession.Close()
		runner.mu.Lock()
		runner.sessions["plan-session-id"] = &runningPlan{
			session: planSession,
			cancel:  func() {},
			dir:     "/tmp",
		}
		runner.mu.Unlock()
		srv.SetPlanRunner(runner)

		got, err := srv.getSingleSession("plan-session-id")
		require.NoError(t, err)
		assert.Equal(t, planSession, got)
	})

	t.Run("returns server session when ID matches", func(t *testing.T) {
		session := NewSession("my-session", "/tmp/test.txt")
		defer session.Close()
		srv, err := NewServer(ServerConfig{Port: 8080}, session)
		require.NoError(t, err)

		got, err := srv.getSingleSession("my-session")
		require.NoError(t, err)
		assert.Equal(t, session, got)
	})

	t.Run("returns error when session ID not found", func(t *testing.T) {
		session := NewSession("my-session", "/tmp/test.txt")
		defer session.Close()
		srv, err := NewServer(ServerConfig{Port: 8080}, session)
		require.NoError(t, err)

		_, err = srv.getSingleSession("nonexistent")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "session not found")
	})
}

func TestServer_StopWithLifecycle(t *testing.T) {
	t.Run("stop via context cancellation gracefully shuts down", func(t *testing.T) {
		session := NewSession("test", "/tmp/test.txt")
		defer session.Close()
		srv, err := NewServer(ServerConfig{
			Port:     0, // random port
			PlanName: "test",
			Branch:   "main",
		}, session)
		require.NoError(t, err)

		// use cancellable context for shutdown
		ctx, cancel := context.WithCancel(t.Context())

		// start server in goroutine
		errCh := make(chan error, 1)
		go func() {
			errCh <- srv.Start(ctx)
		}()

		// give server time to start
		time.Sleep(50 * time.Millisecond)

		// cancel context to trigger graceful shutdown
		cancel()

		// wait for server to return
		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(2 * time.Second):
			t.Fatal("server did not stop in time")
		}
	})
}

func TestExtractDirPath(t *testing.T) {
	t.Run("absolute path returns directory", func(t *testing.T) {
		result := extractDirPath("/home/user/project/progress.txt")
		assert.Equal(t, "/home/user/project", result)
	})

	t.Run("relative path converts to absolute directory", func(t *testing.T) {
		// extractDirPath uses filepath.Abs, so relative paths become absolute
		result := extractDirPath("subdir/progress.txt")
		// result will be cwd + /subdir
		assert.NotEmpty(t, result)
		assert.True(t, filepath.IsAbs(result))
		assert.True(t, strings.HasSuffix(result, "subdir"))
	})

	t.Run("dot-relative converts to absolute directory", func(t *testing.T) {
		// ./progress.txt becomes cwd when resolved
		result := extractDirPath("./progress.txt")
		assert.NotEmpty(t, result)
		assert.True(t, filepath.IsAbs(result))
	})

	t.Run("filename only converts to cwd", func(t *testing.T) {
		result := extractDirPath("progress.txt")
		// when filepath.Abs succeeds, returns cwd
		assert.NotEmpty(t, result)
		assert.True(t, filepath.IsAbs(result))
	})
}

func TestServer_HandleAnswer_Extended(t *testing.T) {
	t.Run("returns error for missing required fields", func(t *testing.T) {
		cfg := testConfigForServer(t)
		sm := NewSessionManager()
		defer sm.Close()
		runner := NewPlanRunner(cfg, sm)
		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)
		srv.SetPlanRunner(runner)

		tests := []struct {
			name string
			body string
		}{
			{"missing session_id", `{"question_id":"q","answer":"a"}`},
			{"missing question_id", `{"session_id":"s","answer":"a"}`},
			{"missing answer", `{"session_id":"s","question_id":"q"}`},
			{"all empty", `{"session_id":"","question_id":"","answer":""}`},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodPost, "/api/answer", strings.NewReader(tc.body))
				req.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()

				srv.handleAnswer(w, req)

				resp := w.Result()
				defer resp.Body.Close()

				assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
			})
		}
	})

	t.Run("returns error for nonexistent session", func(t *testing.T) {
		cfg := testConfigForServer(t)
		sm := NewSessionManager()
		defer sm.Close()
		runner := NewPlanRunner(cfg, sm)
		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)
		srv.SetPlanRunner(runner)

		req := httptest.NewRequest(http.MethodPost, "/api/answer", strings.NewReader(`{"session_id":"nonexistent","question_id":"q","answer":"a"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		srv.handleAnswer(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		body, _ := io.ReadAll(resp.Body)
		assert.Contains(t, string(body), "session not found")
	})

	t.Run("returns error when session has no input collector", func(t *testing.T) {
		cfg := testConfigForServer(t)
		sm := NewSessionManager()
		defer sm.Close()
		runner := NewPlanRunner(cfg, sm)
		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)
		srv.SetPlanRunner(runner)

		// add a session to planRunner without input collector
		session := NewSession("test-session", "/tmp/progress.txt")
		defer session.Close()
		runner.mu.Lock()
		runner.sessions["test-session"] = &runningPlan{
			session: session,
			cancel:  func() {},
			dir:     "/tmp",
		}
		runner.mu.Unlock()

		req := httptest.NewRequest(http.MethodPost, "/api/answer", strings.NewReader(`{"session_id":"test-session","question_id":"q","answer":"a"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		srv.handleAnswer(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		body, _ := io.ReadAll(resp.Body)
		assert.Contains(t, string(body), "no input collector")
	})
}

func TestServer_HandleStartPlan_Extended(t *testing.T) {
	t.Run("returns error for missing directory", func(t *testing.T) {
		cfg := testConfigForServer(t)
		sm := NewSessionManager()
		defer sm.Close()
		runner := NewPlanRunner(cfg, sm)
		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)
		srv.SetPlanRunner(runner)

		req := httptest.NewRequest(http.MethodPost, "/api/plan", strings.NewReader(`{"description":"test"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		srv.handleStartPlan(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		body, _ := io.ReadAll(resp.Body)
		assert.Contains(t, string(body), "directory required")
	})

	t.Run("returns error for missing description", func(t *testing.T) {
		cfg := testConfigForServer(t)
		sm := NewSessionManager()
		defer sm.Close()
		runner := NewPlanRunner(cfg, sm)
		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)
		srv.SetPlanRunner(runner)

		req := httptest.NewRequest(http.MethodPost, "/api/plan", strings.NewReader(`{"dir":"/tmp"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		srv.handleStartPlan(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		body, _ := io.ReadAll(resp.Body)
		assert.Contains(t, string(body), "description required")
	})

	t.Run("returns error for invalid directory", func(t *testing.T) {
		cfg := testConfigForServer(t)
		sm := NewSessionManager()
		defer sm.Close()
		runner := NewPlanRunner(cfg, sm)
		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)
		srv.SetPlanRunner(runner)

		req := httptest.NewRequest(http.MethodPost, "/api/plan", strings.NewReader(`{"dir":"/nonexistent/path","description":"test"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		srv.handleStartPlan(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		body, _ := io.ReadAll(resp.Body)
		assert.Contains(t, string(body), "failed to start plan")
	})
}

func TestServer_HandleCancelSession_Extended(t *testing.T) {
	t.Run("returns error for nonexistent session", func(t *testing.T) {
		cfg := testConfigForServer(t)
		sm := NewSessionManager()
		defer sm.Close()
		runner := NewPlanRunner(cfg, sm)
		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)
		srv.SetPlanRunner(runner)

		req := httptest.NewRequest(http.MethodPost, "/api/sessions/nonexistent/cancel", http.NoBody)
		w := httptest.NewRecorder()

		srv.handleCancelSession(w, req, "nonexistent")

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		body, _ := io.ReadAll(resp.Body)
		assert.Contains(t, string(body), "failed to cancel")
	})

	t.Run("successfully cancels existing session", func(t *testing.T) {
		cfg := testConfigForServer(t)
		sm := NewSessionManager()
		defer sm.Close()
		runner := NewPlanRunner(cfg, sm)
		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)
		srv.SetPlanRunner(runner)

		// add a session to planRunner
		session := NewSession("test-session", "/tmp/progress.txt")
		defer session.Close()
		session.SetState(SessionStateActive)
		cancelCalled := false
		runner.mu.Lock()
		runner.sessions["test-session"] = &runningPlan{
			session: session,
			cancel:  func() { cancelCalled = true },
			dir:     "/tmp",
		}
		runner.mu.Unlock()

		req := httptest.NewRequest(http.MethodPost, "/api/sessions/test-session/cancel", http.NoBody)
		w := httptest.NewRecorder()

		srv.handleCancelSession(w, req, "test-session")

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		body, _ := io.ReadAll(resp.Body)
		assert.Contains(t, string(body), "success")
		assert.True(t, cancelCalled)
	})
}

func TestServer_HandleResumePlan(t *testing.T) {
	t.Run("returns error when plan runner not configured", func(t *testing.T) {
		session := NewSession("test", "/tmp/test.txt")
		defer session.Close()
		srv, err := NewServer(ServerConfig{Port: 8080}, session)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/api/plan/resume", strings.NewReader(`{"progress_path":"/tmp/test.txt"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		srv.handleResumePlan(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	})

	t.Run("rejects non-POST methods", func(t *testing.T) {
		sm := NewSessionManager()
		defer sm.Close()
		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)

		for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
			req := httptest.NewRequest(method, "/api/plan/resume", http.NoBody)
			w := httptest.NewRecorder()

			srv.handleResumePlan(w, req)

			resp := w.Result()
			assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
			resp.Body.Close()
		}
	})

	t.Run("rejects invalid JSON", func(t *testing.T) {
		cfg := testConfigForServer(t)
		sm := NewSessionManager()
		defer sm.Close()
		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)
		srv.planRunner = NewPlanRunner(cfg, sm)

		req := httptest.NewRequest(http.MethodPost, "/api/plan/resume", strings.NewReader("not json"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		srv.handleResumePlan(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("rejects missing progress_path", func(t *testing.T) {
		cfg := testConfigForServer(t)
		sm := NewSessionManager()
		defer sm.Close()
		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)
		srv.planRunner = NewPlanRunner(cfg, sm)

		req := httptest.NewRequest(http.MethodPost, "/api/plan/resume", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		srv.handleResumePlan(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

		body, _ := io.ReadAll(resp.Body)
		assert.Contains(t, string(body), "progress_path required")
	})

	t.Run("rejects non-existent progress file", func(t *testing.T) {
		cfg := testConfigForServer(t)
		sm := NewSessionManager()
		defer sm.Close()
		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)
		srv.planRunner = NewPlanRunner(cfg, sm)

		req := httptest.NewRequest(http.MethodPost, "/api/plan/resume", strings.NewReader(`{"progress_path":"/nonexistent/progress.txt"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		srv.handleResumePlan(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})
}
