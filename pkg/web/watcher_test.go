package web

import (
	"context"
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
	"github.com/tmaxmax/go-sse"
)

// resolveSymlinks resolves symlinks in the given path for test comparison.
// handles platform-specific symlink differences (e.g., macOS /var -> /private/var).
func resolveSymlinks(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	require.NoError(t, err, "failed to resolve symlinks for %s", path)
	return resolved
}

func TestIsProgressFile(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{"progress-test.txt", true},
		{"progress-my-plan.txt", true},
		{"/some/path/progress-test.txt", true},
		{"/some/path/progress-.txt", true},
		{"test.txt", false},
		{"progress.txt", false},
		{"progress-test.log", false},
		{"my-progress-test.txt", false},
		{".progress-test.txt", false},
		{"", false},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			result := isProgressFile(tc.path)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestResolveWatchDirs_CLIPrecedence(t *testing.T) {
	// create temp dirs
	tmpDir := t.TempDir()
	cliDir := filepath.Join(tmpDir, "cli")
	configDir := filepath.Join(tmpDir, "config")
	require.NoError(t, os.Mkdir(cliDir, 0o750))
	require.NoError(t, os.Mkdir(configDir, 0o750))

	// CLI flags take precedence over config
	result := ResolveWatchDirs([]string{cliDir}, []string{configDir})
	require.Len(t, result, 1)
	assert.Equal(t, resolveSymlinks(t, cliDir), result[0])
}

func TestResolveWatchDirs_ConfigFallback(t *testing.T) {
	// create temp dir
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	require.NoError(t, os.Mkdir(configDir, 0o750))

	// empty CLI falls back to config
	result := ResolveWatchDirs(nil, []string{configDir})
	require.Len(t, result, 1)
	assert.Equal(t, resolveSymlinks(t, configDir), result[0])
}

func TestResolveWatchDirs_DefaultCwd(t *testing.T) {
	// empty CLI and config falls back to cwd
	result := ResolveWatchDirs(nil, nil)
	require.Len(t, result, 1)

	cwd, err := os.Getwd()
	require.NoError(t, err)
	assert.Equal(t, cwd, result[0])
}

func TestResolveWatchDirs_DeduplicatesAndNormalizes(t *testing.T) {
	tmpDir := t.TempDir()

	// create a dir
	testDir := filepath.Join(tmpDir, "test")
	require.NoError(t, os.Mkdir(testDir, 0o750))

	// pass same dir multiple times with different representations
	result := ResolveWatchDirs([]string{testDir, testDir, testDir}, nil)
	require.Len(t, result, 1)
	assert.Equal(t, resolveSymlinks(t, testDir), result[0])
}

func TestResolveWatchDirs_InvalidDirsIgnored(t *testing.T) {
	tmpDir := t.TempDir()

	// create one valid dir
	validDir := filepath.Join(tmpDir, "valid")
	require.NoError(t, os.Mkdir(validDir, 0o750))

	// pass one valid and one invalid dir
	invalidDir := filepath.Join(tmpDir, "nonexistent")
	result := ResolveWatchDirs([]string{invalidDir, validDir}, nil)
	require.Len(t, result, 1)
	assert.Equal(t, resolveSymlinks(t, validDir), result[0])
}

func TestResolveWatchDirs_AllInvalidFallsBackToCwd(t *testing.T) {
	// pass only invalid directories
	result := ResolveWatchDirs([]string{"/nonexistent/path/12345"}, nil)
	require.Len(t, result, 1)

	cwd, err := os.Getwd()
	require.NoError(t, err)
	assert.Equal(t, cwd, result[0])
}

func TestNormalizeDirs_RelativePaths(t *testing.T) {
	// create temp dir structure
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "subdir")
	require.NoError(t, os.Mkdir(subDir, 0o750))

	// change to tmpDir so relative path works
	oldCwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(oldCwd) }()

	// pass relative path
	result := normalizeDirs([]string{"subdir"})
	require.Len(t, result, 1)
	assert.Equal(t, resolveSymlinks(t, subDir), result[0])
}

func TestWatcher_NewWatcher(t *testing.T) {
	tmpDir := t.TempDir()
	sm := NewSessionManager()

	w, err := NewWatcher([]string{tmpDir}, sm)
	require.NoError(t, err)
	require.NotNil(t, w)
	defer w.Close()

	assert.Equal(t, []string{tmpDir}, w.dirs)
	assert.Equal(t, sm, w.sm)
}

func TestWatcher_StartAndClose(t *testing.T) {
	tmpDir := t.TempDir()
	sm := NewSessionManager()

	w, err := NewWatcher([]string{tmpDir}, sm)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())

	// start watcher in background
	done := make(chan error, 1)
	go func() {
		done <- w.Start(ctx)
	}()

	// give it time to start
	time.Sleep(50 * time.Millisecond)

	// cancel context to stop
	cancel()

	// wait for watcher to exit
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("watcher did not stop in time")
	}
}

func TestWatcher_DetectsNewProgressFile(t *testing.T) {
	tmpDir := t.TempDir()
	sm := NewSessionManager()

	w, err := NewWatcher([]string{tmpDir}, sm)
	require.NoError(t, err)

	ctx := t.Context()

	// start watcher in background
	go func() {
		_ = w.Start(ctx)
	}()

	// give watcher time to start
	time.Sleep(100 * time.Millisecond)

	// create a progress file
	progressFile := filepath.Join(tmpDir, "progress-test.txt")
	header := `# Ralphex Progress Log
Plan: test-plan.md
Branch: test-branch
Mode: full
Started: 2026-01-22 10:00:00
------------------------------------------------------------
`
	require.NoError(t, os.WriteFile(progressFile, []byte(header), 0o600))

	// give watcher time to process
	time.Sleep(200 * time.Millisecond)

	// verify session was discovered
	expectedID := sessionIDFromPath(progressFile)
	session := sm.Get(expectedID)
	require.NotNil(t, session, "session should be discovered")
	assert.Equal(t, expectedID, session.ID)
}

func TestWatcher_IgnoresNonProgressFiles(t *testing.T) {
	tmpDir := t.TempDir()
	sm := NewSessionManager()

	w, err := NewWatcher([]string{tmpDir}, sm)
	require.NoError(t, err)

	ctx := t.Context()

	// start watcher in background
	go func() {
		_ = w.Start(ctx)
	}()

	// give watcher time to start
	time.Sleep(100 * time.Millisecond)

	// create a non-progress file
	otherFile := filepath.Join(tmpDir, "test.txt")
	require.NoError(t, os.WriteFile(otherFile, []byte("hello"), 0o600))

	// give watcher time to process
	time.Sleep(100 * time.Millisecond)

	// verify no sessions discovered
	sessions := sm.All()
	assert.Empty(t, sessions)
}

func TestWatcher_WatchesSubdirectories(t *testing.T) {
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "subproject")
	require.NoError(t, os.Mkdir(subDir, 0o750))

	sm := NewSessionManager()

	w, err := NewWatcher([]string{tmpDir}, sm)
	require.NoError(t, err)

	ctx := t.Context()

	// start watcher in background
	go func() {
		_ = w.Start(ctx)
	}()

	// give watcher time to start
	time.Sleep(100 * time.Millisecond)

	// create a progress file in subdirectory
	progressFile := filepath.Join(subDir, "progress-subtest.txt")
	header := `# Ralphex Progress Log
Plan: sub-plan.md
Branch: sub-branch
Mode: full
Started: 2026-01-22 10:00:00
------------------------------------------------------------
`
	require.NoError(t, os.WriteFile(progressFile, []byte(header), 0o600))

	// give watcher time to process
	time.Sleep(200 * time.Millisecond)

	// verify session was discovered
	sessionID := sessionIDFromPath(progressFile)
	session := sm.Get(sessionID)
	require.NotNil(t, session, "session in subdirectory should be discovered")
}

func TestWatcher_HandlesDeletedProgressFile(t *testing.T) {
	tmpDir := t.TempDir()
	sm := NewSessionManager()

	// create a progress file before watcher starts
	progressFile := filepath.Join(tmpDir, "progress-delete-test.txt")
	header := `# Ralphex Progress Log
Plan: delete-plan.md
Branch: delete-branch
Mode: full
Started: 2026-01-22 10:00:00
------------------------------------------------------------
`
	require.NoError(t, os.WriteFile(progressFile, []byte(header), 0o600))

	// compute session ID before starting watcher (path won't change)
	sessionID := sessionIDFromPath(progressFile)

	w, err := NewWatcher([]string{tmpDir}, sm)
	require.NoError(t, err)

	ctx := t.Context()

	// start watcher in background
	go func() {
		_ = w.Start(ctx)
	}()

	// give watcher time to start and discover
	time.Sleep(200 * time.Millisecond)

	// verify session was discovered
	session := sm.Get(sessionID)
	require.NotNil(t, session, "session should be discovered initially")

	// delete the file
	require.NoError(t, os.Remove(progressFile))

	// give watcher time to process
	time.Sleep(200 * time.Millisecond)

	// verify session was removed
	session = sm.Get(sessionID)
	assert.Nil(t, session, "session should be removed after file deletion")
}

func TestWatcher_SkipsKnownDirectories(t *testing.T) {
	tests := []struct {
		name string
		dir  string
	}{
		{"git", ".git"},
		{"idea", ".idea"},
		{"vscode", ".vscode"},
		{"node_modules", "node_modules"},
		{"vendor", "vendor"},
		{"pycache", "__pycache__"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			skippedDir := filepath.Join(tmpDir, tc.dir)
			require.NoError(t, os.Mkdir(skippedDir, 0o750))

			sm := NewSessionManager()

			w, err := NewWatcher([]string{tmpDir}, sm)
			require.NoError(t, err)

			ctx := t.Context()

			// start watcher in background
			go func() {
				_ = w.Start(ctx)
			}()

			// give watcher time to start
			time.Sleep(100 * time.Millisecond)

			// create a progress file in skipped directory
			progressFile := filepath.Join(skippedDir, "progress-skipped.txt")
			header := `# Ralphex Progress Log
Plan: skipped-plan.md
Branch: skipped-branch
Mode: full
Started: 2026-01-22 10:00:00
------------------------------------------------------------
`
			require.NoError(t, os.WriteFile(progressFile, []byte(header), 0o600))

			// give watcher time to process
			time.Sleep(200 * time.Millisecond)

			// verify session was NOT discovered (skipped dir should not be watched)
			sessionID := sessionIDFromPath(progressFile)
			session := sm.Get(sessionID)
			assert.Nil(t, session, "%s directory should not be watched", tc.dir)
		})
	}
}

func TestWatcher_WatchesUnknownHiddenDirectories(t *testing.T) {
	tmpDir := t.TempDir()
	dotDir := filepath.Join(tmpDir, ".myconfig")
	require.NoError(t, os.Mkdir(dotDir, 0o750))

	sm := NewSessionManager()

	w, err := NewWatcher([]string{tmpDir}, sm)
	require.NoError(t, err)

	ctx := t.Context()

	// start watcher in background
	go func() {
		_ = w.Start(ctx)
	}()

	// give watcher time to start
	time.Sleep(100 * time.Millisecond)

	// create a progress file in unknown hidden directory
	progressFile := filepath.Join(dotDir, "progress-dotdir.txt")
	header := `# Ralphex Progress Log
Plan: dotdir-plan.md
Branch: dotdir-branch
Mode: full
Started: 2026-01-22 10:00:00
------------------------------------------------------------
`
	require.NoError(t, os.WriteFile(progressFile, []byte(header), 0o600))

	// give watcher time to process
	time.Sleep(200 * time.Millisecond)

	// verify session WAS discovered (unknown hidden dirs should be watched)
	sessionID := sessionIDFromPath(progressFile)
	session := sm.Get(sessionID)
	require.NotNil(t, session, "unknown hidden directories should be watched")
	assert.Equal(t, "dotdir-plan.md", session.GetMetadata().PlanPath)
}

func TestWatcher_StartTwiceIsIdempotent(t *testing.T) {
	tmpDir := t.TempDir()
	sm := NewSessionManager()

	w, err := NewWatcher([]string{tmpDir}, sm)
	require.NoError(t, err)

	ctx := t.Context()

	// start watcher in background
	go func() {
		_ = w.Start(ctx)
	}()

	// give it time to start
	time.Sleep(50 * time.Millisecond)

	// calling Start again should return nil immediately
	err = w.Start(ctx)
	require.NoError(t, err)
}

func TestWatcher_WatchesNewlyCreatedDirectories(t *testing.T) {
	tmpDir := t.TempDir()
	sm := NewSessionManager()

	w, err := NewWatcher([]string{tmpDir}, sm)
	require.NoError(t, err)

	ctx := t.Context()

	// start watcher in background
	go func() {
		_ = w.Start(ctx)
	}()

	// give watcher time to start
	time.Sleep(100 * time.Millisecond)

	// create a new subdirectory after watcher started
	newDir := filepath.Join(tmpDir, "newproject")
	require.NoError(t, os.Mkdir(newDir, 0o750))

	// give watcher time to add the new directory
	time.Sleep(200 * time.Millisecond)

	// create a progress file in the new directory
	progressFile := filepath.Join(newDir, "progress-newproject.txt")
	header := `# Ralphex Progress Log
Plan: new-plan.md
Branch: new-branch
Mode: full
Started: 2026-01-22 10:00:00
------------------------------------------------------------
`
	require.NoError(t, os.WriteFile(progressFile, []byte(header), 0o600))

	// give watcher time to process
	time.Sleep(200 * time.Millisecond)

	// verify session was discovered in the newly created directory
	sessionID := sessionIDFromPath(progressFile)
	session := sm.Get(sessionID)
	require.NotNil(t, session, "session in newly created directory should be discovered")
	assert.Equal(t, "new-plan.md", session.GetMetadata().PlanPath)
}

// TestWatcher_ResumesStreamingAfterFlockRace is the TDD reproduction test for issue #283.
// It simulates the flock race where RefreshStates transiently marks a running session as
// completed (because TryLockFile briefly succeeds), then appends new progress lines to the
// file and expects the watcher to reactivate the session so streaming resumes.
func TestWatcher_ResumesStreamingAfterFlockRace(t *testing.T) {
	tmpDir := t.TempDir()
	progressFile := filepath.Join(tmpDir, "progress-reactivate-test.txt")
	header := `# Ralphex Progress Log
Plan: reactivate-plan.md
Branch: reactivate-branch
Mode: full
Started: 2026-01-22 10:00:00
------------------------------------------------------------

[26-01-22 10:00:01] initial line 1
[26-01-22 10:00:02] initial line 2
`
	require.NoError(t, os.WriteFile(progressFile, []byte(header), 0o600))

	sm := NewSessionManager()
	w, err := NewWatcher([]string{tmpDir}, sm)
	require.NoError(t, err)

	ctx := t.Context()
	go func() { _ = w.Start(ctx) }()

	// wait for initial discovery
	sessionID := sessionIDFromPath(progressFile)
	require.Eventually(t, func() bool {
		return sm.Get(sessionID) != nil
	}, time.Second, 10*time.Millisecond, "session should be discovered")

	session := sm.Get(sessionID)
	require.NotNil(t, session)

	// simulate the flock race: force state to completed and stop tailing
	// (models what RefreshStates does when TryLockFile transiently succeeds)
	session.SetState(SessionStateCompleted)
	session.StopTailing()
	assert.Eventually(t, func() bool { return !session.IsTailing() }, time.Second, 10*time.Millisecond)
	require.Equal(t, SessionStateCompleted, session.GetState())

	// append new lines — simulates the still-running executor writing after the race
	f, err := os.OpenFile(progressFile, os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // test-controlled path from t.TempDir
	require.NoError(t, err)
	_, err = f.WriteString("[26-01-22 10:00:03] line after reactivation\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	// expect watcher to reactivate the session on the Write event
	assert.Eventually(t, func() bool {
		return session.GetState() == SessionStateActive && session.IsTailing()
	}, 2*time.Second, 20*time.Millisecond, "session should reactivate and resume tailing after write")
}

// subscribeSSEEvents opens an SSE subscription on the session and returns a
// channel that receives every Event.Data string until the returned cancel is
// called. used by watcher tests to verify event delivery without relying on
// internal tailer state.
func subscribeSSEEvents(t *testing.T, session *Session) (<-chan string, func()) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(session.SSE.ServeHTTP))

	ctx, cancelCtx := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL, http.NoBody)
	require.NoError(t, err)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	events := make(chan string, 64)
	var wg sync.WaitGroup
	wg.Go(func() {
		defer func() { _ = resp.Body.Close() }()
		for ev, readErr := range sse.Read(resp.Body, nil) {
			if readErr != nil {
				return
			}
			select {
			case events <- ev.Data:
			case <-ctx.Done():
				return
			}
		}
	})

	cleanup := func() {
		cancelCtx()
		ts.Close()
		wg.Wait()
	}
	return events, cleanup
}

// drainChannel collects any events already available on ch without blocking
// beyond the specified settle window. used to capture the final set of events
// delivered during a test step.
func drainChannel(ch <-chan string, settle time.Duration) []string {
	var got []string
	timer := time.NewTimer(settle)
	defer timer.Stop()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return got
			}
			got = append(got, ev)
			// reset settle window for follow-up events
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(settle)
		case <-timer.C:
			return got
		}
	}
}

func TestWatcher_ReactivatesCompletedSessionOnWrite(t *testing.T) {
	tmpDir := t.TempDir()
	progressFile := filepath.Join(tmpDir, "progress-reactivate-write.txt")
	initial := `# Ralphex Progress Log
Plan: reactivate-plan.md
Branch: reactivate-branch
Mode: full
Started: 2026-01-22 10:00:00
------------------------------------------------------------

[26-01-22 10:00:01] initial line alpha
[26-01-22 10:00:02] initial line beta
`
	require.NoError(t, os.WriteFile(progressFile, []byte(initial), 0o600))

	sm := NewSessionManager()
	w, err := NewWatcher([]string{tmpDir}, sm)
	require.NoError(t, err)

	ctx := t.Context()
	go func() { _ = w.Start(ctx) }()

	sessionID := sessionIDFromPath(progressFile)
	require.Eventually(t, func() bool {
		s := sm.Get(sessionID)
		return s != nil && s.IsLoaded() && s.getLastOffset() == int64(len(initial))
	}, 2*time.Second, 20*time.Millisecond, "session should be discovered, loaded, with offset set")

	session := sm.Get(sessionID)
	require.NotNil(t, session)
	require.Equal(t, SessionStateCompleted, session.GetState())

	// subscribe AFTER initial load so replay includes the initial two lines
	events, cleanup := subscribeSSEEvents(t, session)
	defer cleanup()

	// drain the replay (initial published lines)
	replayed := drainChannel(events, 300*time.Millisecond)
	require.NotEmpty(t, replayed, "SSE replay should contain initial events")

	// append a new line - simulates a still-running executor writing after a flock race
	newLine := "[26-01-22 10:00:03] line after reactivate\n"
	f, err := os.OpenFile(progressFile, os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // test path from t.TempDir
	require.NoError(t, err)
	_, err = f.WriteString(newLine)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	// watcher should reactivate the session
	require.Eventually(t, func() bool {
		return session.GetState() == SessionStateActive && session.IsTailing()
	}, 2*time.Second, 20*time.Millisecond, "session should reactivate on write event")

	// collect events that arrive after reactivation
	newEvents := drainChannel(events, 500*time.Millisecond)
	require.NotEmpty(t, newEvents, "new line event should arrive after reactivation")

	// verify the new line arrived exactly once and pre-existing lines were NOT re-emitted
	var newLineMatches int
	for _, ev := range newEvents {
		if strings.Contains(ev, "line after reactivate") {
			newLineMatches++
		}
		require.NotContains(t, ev, "initial line alpha", "pre-existing content must not be re-emitted")
		require.NotContains(t, ev, "initial line beta", "pre-existing content must not be re-emitted")
	}
	assert.Equal(t, 1, newLineMatches, "new line should be delivered exactly once")
}

func TestWatcher_DoesNotReactivateActiveSession(t *testing.T) {
	tmpDir := t.TempDir()
	progressFile := filepath.Join(tmpDir, "progress-active-no-reactivate.txt")
	initial := `# Ralphex Progress Log
Plan: active-plan.md
Branch: active-branch
Mode: full
Started: 2026-01-22 10:00:00
------------------------------------------------------------

[26-01-22 10:00:01] initial active line
`
	require.NoError(t, os.WriteFile(progressFile, []byte(initial), 0o600))

	sm := NewSessionManager()
	sessionID := sessionIDFromPath(progressFile)

	// register an active session directly (simulate a session that is already
	// tailing, bypassing flock-based discovery which would mark it completed)
	session := NewSession(sessionID, progressFile)
	sm.Register(session)
	require.True(t, session.MarkLoadedIfNot(), "simulate loader has completed")
	session.SetState(SessionStateActive)
	require.NoError(t, session.StartTailing(true))
	require.Eventually(t, func() bool { return session.IsTailing() },
		time.Second, 10*time.Millisecond)

	// wait for the tailer to consume the initial content so SSE replayer has
	// at least one event; this lets the SSE server flush headers on subscribe.
	require.Eventually(t, func() bool {
		tl := session.GetTailer()
		return tl != nil && tl.Offset() >= int64(len(initial))
	}, 2*time.Second, 20*time.Millisecond, "tailer should read initial content")

	w, err := NewWatcher([]string{tmpDir}, sm)
	require.NoError(t, err)

	ctx := t.Context()
	go func() { _ = w.Start(ctx) }()

	// allow initial startup (watcher may trigger one Discover cycle)
	time.Sleep(150 * time.Millisecond)

	events, cleanup := subscribeSSEEvents(t, session)
	defer cleanup()

	// drain any replayed events from the pre-subscription window
	_ = drainChannel(events, 200*time.Millisecond)

	// append exactly one new line and expect exactly one event to reach SSE
	newLine := "[26-01-22 10:00:02] active mode new line\n"
	f, err := os.OpenFile(progressFile, os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // test path from t.TempDir
	require.NoError(t, err)
	_, err = f.WriteString(newLine)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	// give the tailer time to read the line and the Write event to be processed
	delivered := drainChannel(events, 500*time.Millisecond)

	var matches int
	for _, ev := range delivered {
		if strings.Contains(ev, "active mode new line") {
			matches++
		}
	}
	assert.Equal(t, 1, matches, "new line must be delivered exactly once (no duplicate tailer)")

	// state must still be active (not cycled through completed -> active)
	assert.Equal(t, SessionStateActive, session.GetState())
}

func TestWatcher_OnlyReactivatesWrittenPath(t *testing.T) {
	tmpDir := t.TempDir()

	fileA := filepath.Join(tmpDir, "progress-reactivate-a.txt")
	fileB := filepath.Join(tmpDir, "progress-reactivate-b.txt")
	contentA := `# Ralphex Progress Log
Plan: plan-a.md
Branch: branch-a
Mode: full
Started: 2026-01-22 10:00:00
------------------------------------------------------------

[26-01-22 10:00:01] a initial
`
	contentB := `# Ralphex Progress Log
Plan: plan-b.md
Branch: branch-b
Mode: full
Started: 2026-01-22 10:00:00
------------------------------------------------------------

[26-01-22 10:00:01] b initial
`
	require.NoError(t, os.WriteFile(fileA, []byte(contentA), 0o600))
	require.NoError(t, os.WriteFile(fileB, []byte(contentB), 0o600))

	sm := NewSessionManager()
	w, err := NewWatcher([]string{tmpDir}, sm)
	require.NoError(t, err)

	ctx := t.Context()
	go func() { _ = w.Start(ctx) }()

	idA := sessionIDFromPath(fileA)
	idB := sessionIDFromPath(fileB)

	require.Eventually(t, func() bool {
		a := sm.Get(idA)
		b := sm.Get(idB)
		return a != nil && a.IsLoaded() && b != nil && b.IsLoaded()
	}, 2*time.Second, 20*time.Millisecond, "both sessions should be discovered and loaded")

	sessionA := sm.Get(idA)
	sessionB := sm.Get(idB)
	require.Equal(t, SessionStateCompleted, sessionA.GetState())
	require.Equal(t, SessionStateCompleted, sessionB.GetState())

	// write only to file A
	f, err := os.OpenFile(fileA, os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // test path from t.TempDir
	require.NoError(t, err)
	_, err = f.WriteString("[26-01-22 10:00:02] a extra line\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	// sessionA must reactivate
	require.Eventually(t, func() bool {
		return sessionA.GetState() == SessionStateActive && sessionA.IsTailing()
	}, 2*time.Second, 20*time.Millisecond, "session A should be reactivated")

	// sessionB must stay completed (give the watcher ample time to misbehave)
	time.Sleep(300 * time.Millisecond)
	assert.Equal(t, SessionStateCompleted, sessionB.GetState(),
		"session B should NOT be reactivated - no write event arrived for its path")
	assert.False(t, sessionB.IsTailing(),
		"session B should NOT be tailing - no write event arrived for its path")
}

func TestWatcher_Close(t *testing.T) {
	tmpDir := t.TempDir()
	sm := NewSessionManager()

	w, err := NewWatcher([]string{tmpDir}, sm)
	require.NoError(t, err)

	// close without starting should work
	err = w.Close()
	require.NoError(t, err)
}

func TestWatcher_CloseAfterStart(t *testing.T) {
	tmpDir := t.TempDir()
	sm := NewSessionManager()

	w, err := NewWatcher([]string{tmpDir}, sm)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())

	// start watcher in background
	done := make(chan error, 1)
	go func() {
		done <- w.Start(ctx)
	}()

	// give watcher time to start
	time.Sleep(50 * time.Millisecond)

	// close the watcher directly (not via context)
	err = w.Close()
	require.NoError(t, err)
	cancel() // cleanup

	// wait for watcher to exit
	select {
	case <-done:
		// watcher exited
	case <-time.After(time.Second):
		t.Fatal("watcher did not stop after Close")
	}
}
