package web

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			session := sm.Get("skipped")
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
