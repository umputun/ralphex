package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/status"
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
		Port:      8080,
		PlanName:  "my-plan.md",
		Branch:    "feature-branch",
		RunParams: "codex · task gpt-5.5:high",
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
		assert.Contains(t, bodyStr, "codex · task gpt-5.5:high")
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
		require.NoError(t, session.Publish(NewOutputEvent(status.PhaseTask, "test event 1")))
		require.NoError(t, session.Publish(NewSectionEvent(status.PhaseReview, "Review")))
		require.NoError(t, session.Publish(NewOutputEvent(status.PhaseReview, "test event 2")))

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
	require.NoError(t, session.Publish(NewOutputEvent(status.PhaseTask, "event 1")))
	require.NoError(t, session.Publish(NewSectionEvent(status.PhaseReview, "Review Section")))
	require.NoError(t, session.Publish(NewOutputEvent(status.PhaseReview, "event 2")))
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
Executor: codex
Task model: gpt-5.5:high
Review model: gpt-5.5:low
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
		assert.Equal(t, "codex · task gpt-5.5:high · review gpt-5.5:low", sessions[0].RunParams)
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
		require.NoError(t, session.Publish(NewOutputEvent(status.PhaseTask, "test event")))
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

	// pins #440: the recorded "Plan:" path is the main checkout, which a worktree run never ticks
	t.Run("prefers the worktree plan copy over the recorded main-checkout path", func(t *testing.T) {
		tmpDir := t.TempDir()

		mainPlan := filepath.Join(tmpDir, "main-plan.md")
		require.NoError(t, os.WriteFile(mainPlan, []byte("# Plan\n\n### Task 1: Work\n\n- [ ] step\n"), 0o600))
		wtPlan := filepath.Join(tmpDir, "wt", "main-plan.md")
		require.NoError(t, os.MkdirAll(filepath.Dir(wtPlan), 0o750))
		require.NoError(t, os.WriteFile(wtPlan, []byte("# Plan\n\n### Task 1: Work\n\n- [x] step\n"), 0o600))

		progressPath := filepath.Join(tmpDir, "progress-wt.txt")
		progressContent := "# Ralphex Progress Log\nPlan: " + mainPlan + "\nWorktree plan: " + wtPlan +
			"\nBranch: main-plan\nMode: full\nStarted: 2026-01-22 10:30:00\n" +
			"------------------------------------------------------------\n"
		require.NoError(t, os.WriteFile(progressPath, []byte(progressContent), 0o600))

		sm := NewSessionManager()
		defer sm.Close()
		_, err := sm.Discover(tmpDir)
		require.NoError(t, err)

		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/api/plan?session="+sessionIDFromPath(progressPath), http.NoBody)
		w := httptest.NewRecorder()
		srv.handlePlan(w, req)

		resp := w.Result()
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Contains(t, string(body), `"checked":true`, "should serve the ticked worktree copy")
	})

	t.Run("falls back to the recorded path once the worktree is gone", func(t *testing.T) {
		tmpDir := t.TempDir()

		mainPlan := filepath.Join(tmpDir, "main-plan.md")
		require.NoError(t, os.WriteFile(mainPlan, []byte("# Fallback Plan\n\n### Task 1: Work\n\n- [ ] step\n"), 0o600))

		progressPath := filepath.Join(tmpDir, "progress-wt-gone.txt")
		progressContent := "# Ralphex Progress Log\nPlan: " + mainPlan +
			"\nWorktree plan: " + filepath.Join(tmpDir, "removed", "main-plan.md") +
			"\nBranch: main-plan\nMode: full\nStarted: 2026-01-22 10:30:00\n" +
			"------------------------------------------------------------\n"
		require.NoError(t, os.WriteFile(progressPath, []byte(progressContent), 0o600))

		sm := NewSessionManager()
		defer sm.Close()
		_, err := sm.Discover(tmpDir)
		require.NoError(t, err)

		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/api/plan?session="+sessionIDFromPath(progressPath), http.NoBody)
		w := httptest.NewRecorder()
		srv.handlePlan(w, req)

		resp := w.Result()
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Contains(t, string(body), "Fallback Plan")
	})

	t.Run("surfaces an unreadable worktree plan instead of the stale main copy", func(t *testing.T) {
		tmpDir := t.TempDir()

		mainPlan := filepath.Join(tmpDir, "main-plan.md")
		require.NoError(t, os.WriteFile(mainPlan, []byte("# Stale Plan\n\n### Task 1: Work\n\n- [ ] step\n"), 0o600))
		wtPlan := filepath.Join(tmpDir, "wt", "main-plan.md")
		require.NoError(t, os.MkdirAll(wtPlan, 0o750))

		progressPath := filepath.Join(tmpDir, "progress-wt-unreadable.txt")
		progressContent := "# Ralphex Progress Log\nPlan: " + mainPlan + "\nWorktree plan: " + wtPlan +
			"\nBranch: main-plan\nMode: full\nStarted: 2026-01-22 10:30:00\n" +
			"------------------------------------------------------------\n"
		require.NoError(t, os.WriteFile(progressPath, []byte(progressContent), 0o600))

		sm := NewSessionManager()
		defer sm.Close()
		_, err := sm.Discover(tmpDir)
		require.NoError(t, err)

		srv, err := NewServerWithSessions(ServerConfig{Port: 8080}, sm)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/api/plan?session="+sessionIDFromPath(progressPath), http.NoBody)
		w := httptest.NewRecorder()
		srv.handlePlan(w, req)

		resp := w.Result()
		defer resp.Body.Close()
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.NotContains(t, string(body), "Stale Plan")
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

func TestFormatRunParams(t *testing.T) {
	tests := []struct {
		name                                        string
		executor, planModel, taskModel, reviewModel string
		want                                        string
	}{
		{name: "all empty", want: ""},
		{name: "executor only", executor: "codex", want: "codex"},
		{name: "task model only", taskModel: "opus:high", want: "task opus:high"},
		{name: "task and review models", taskModel: "opus", reviewModel: "sonnet:low", want: "task opus · review sonnet:low"},
		{name: "executor with models", executor: "codex", taskModel: "gpt-5.5:high", reviewModel: "gpt-5.5:low", want: "codex · task gpt-5.5:high · review gpt-5.5:low"},
		{name: "plan model only", planModel: "opus:high", want: "plan opus:high"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, FormatRunParams(tc.executor, tc.planModel, tc.taskModel, tc.reviewModel))
		})
	}
}

func TestServerConfig_host(t *testing.T) {
	t.Run("empty defaults to loopback", func(t *testing.T) {
		cfg := ServerConfig{}
		assert.Equal(t, "127.0.0.1", cfg.host())
	})

	t.Run("configured value returned", func(t *testing.T) {
		cfg := ServerConfig{Host: "0.0.0.0"}
		assert.Equal(t, "0.0.0.0", cfg.host())
	})
}

// regression: a subscriber that stopped reading blocked Session.Publish forever
func TestServer_HandleEvents_SlowClientDoesNotBlockPublish(t *testing.T) {
	session := NewSession("slow", filepath.Join(t.TempDir(), "progress-slow.txt"))
	srv, err := NewServer(ServerConfig{Host: "127.0.0.1"}, session)
	require.NoError(t, err)
	srv.sseWriteTimeout = 200 * time.Millisecond

	// handleEvents returns once the stalled subscriber is dropped, which is a
	// definite signal, unlike inferring the drop from a slow publish
	handlerReturned := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv.handleEvents(w, r)
		close(handlerReturned)
	}))
	t.Cleanup(func() {
		ts.Close()
		session.Close()
	})

	// open a real connection and never read from it, with a small receive
	// buffer so the server's socket buffer fills after a few events
	addr := strings.TrimPrefix(ts.URL, "http://")
	conn, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer conn.Close()
	if tc, ok := conn.(*net.TCPConn); ok {
		require.NoError(t, tc.SetReadBuffer(4096))
	}
	_, err = fmt.Fprintf(conn, "GET /events HTTP/1.1\r\nHost: %s\r\nAccept: text/event-stream\r\n\r\n", addr)
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond) // let the server subscribe the client

	publish := func(payload string) {
		done := make(chan error, 1)
		go func() { done <- session.Publish(NewOutputEvent(status.PhaseTask, payload)) }()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(3 * time.Second):
			t.Fatalf("publish blocked for over 3s")
		}
	}

	// fill the unread socket until the write deadline drops the subscriber
	payload := strings.Repeat("x", 32*1024)
	budget := time.Now().Add(10 * time.Second)
	for i := 0; i < 100 && time.Now().Before(budget) && !channelClosed(handlerReturned); i++ {
		publish(payload)
	}
	require.Eventually(t, func() bool { return channelClosed(handlerReturned) },
		3*time.Second, 10*time.Millisecond, "slow subscriber was not dropped by the write deadline")

	// once the stalled subscriber is gone, publishing stays fast
	start := time.Now()
	for range 10 {
		publish(payload)
	}
	assert.Less(t, time.Since(start), 5*srv.sseWriteTimeout, "publishing stayed slow after the drop")
}

// channelClosed reports whether ch has been closed.
func channelClosed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func TestServer_HandleEvents_ActiveClientStaysConnected(t *testing.T) {
	session := NewSession("active", filepath.Join(t.TempDir(), "progress-active.txt"))
	srv, err := NewServer(ServerConfig{Host: "127.0.0.1"}, session)
	require.NoError(t, err)
	srv.sseWriteTimeout = 200 * time.Millisecond

	ts := httptest.NewServer(http.HandlerFunc(srv.handleEvents))
	t.Cleanup(func() {
		ts.Close()
		session.Close()
	})

	addr := strings.TrimPrefix(ts.URL, "http://")
	conn, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer conn.Close()
	_, err = fmt.Fprintf(conn, "GET /events HTTP/1.1\r\nHost: %s\r\nAccept: text/event-stream\r\n\r\n", addr)
	require.NoError(t, err)

	var mu sync.Mutex
	var received bytes.Buffer
	go func() {
		buf := make([]byte, 4096)
		for {
			n, readErr := conn.Read(buf)
			mu.Lock()
			received.Write(buf[:n])
			mu.Unlock()
			if readErr != nil {
				return
			}
		}
	}()

	time.Sleep(100 * time.Millisecond) // let the server subscribe the client
	for i := range 3 {
		marker := fmt.Sprintf("marker-%d", i)
		done := make(chan error, 1)
		go func() { done <- session.Publish(NewOutputEvent(status.PhaseTask, marker)) }()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(3 * time.Second):
			t.Fatalf("publish %d blocked for over 3s for an active client", i)
		}
		// idle longer than the deadline: the next write must refresh it rather
		// than inherit an expired one and drop a healthy subscriber
		time.Sleep(2 * srv.sseWriteTimeout)
	}

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return strings.Contains(received.String(), "marker-2")
	}, 3*time.Second, 10*time.Millisecond, "active client did not receive the last event")
}

// flushErrorWriter exposes the FlushError method net/http provides, so the
// wrapper is expected to preserve it for go-sse.
type flushErrorWriter struct {
	http.ResponseWriter
	flushed   int
	deadlines int
	err       error
}

func (w *flushErrorWriter) Flush() { w.flushed++ }

func (w *flushErrorWriter) FlushError() error {
	w.flushed++
	return w.err
}

func (w *flushErrorWriter) SetWriteDeadline(time.Time) error {
	w.deadlines++
	return nil
}

func TestSSEClientWriter_FlushErrorPropagates(t *testing.T) {
	inner := &flushErrorWriter{err: errors.New("flush boom")}
	w := &sseClientWriter{
		ResponseWriter: inner,
		rc:             http.NewResponseController(inner),
		timeout:        time.Second,
	}

	require.ErrorIs(t, w.FlushError(), inner.err)
	assert.Equal(t, 1, inner.flushed)
	assert.Equal(t, 1, inner.deadlines, "flush must refresh the write deadline")
}

// nonFlusherWriter implements http.ResponseWriter without flush support.
type nonFlusherWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (w *nonFlusherWriter) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}

func (w *nonFlusherWriter) Write(p []byte) (int, error) {
	n, err := w.body.Write(p)
	if err != nil {
		return n, fmt.Errorf("write body: %w", err)
	}
	return n, nil
}

func (w *nonFlusherWriter) WriteHeader(code int) { w.status = code }

func TestServer_HandleEvents_NonFlusherWriter(t *testing.T) {
	session := NewSession("plain", filepath.Join(t.TempDir(), "progress-plain.txt"))
	defer session.Close()
	srv, err := NewServer(ServerConfig{Host: "127.0.0.1"}, session)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/events", http.NoBody)
	w := &nonFlusherWriter{}
	srv.handleEvents(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.status)
	assert.Contains(t, w.body.String(), "Server-sent events unsupported")
}
