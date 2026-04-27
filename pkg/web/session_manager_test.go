package web

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/config"
	"github.com/umputun/ralphex/pkg/progress"
	"github.com/umputun/ralphex/pkg/status"
)

func TestNewSessionManager(t *testing.T) {
	m := NewSessionManager()

	assert.NotNil(t, m.sessions)
	assert.Empty(t, m.All())
}

func TestSessionManager_Discover(t *testing.T) {
	t.Run("finds progress files", func(t *testing.T) {
		dir := t.TempDir()

		// create test progress files
		path1 := filepath.Join(dir, "progress-plan1.txt")
		path2 := filepath.Join(dir, "progress-plan2.txt")
		createProgressFile(t, path1, "docs/plan1.md", "main", "full")
		createProgressFile(t, path2, "docs/plan2.md", "feature", "review")

		m := NewSessionManager()
		ids, err := m.Discover(dir)

		require.NoError(t, err)
		assert.Len(t, ids, 2)
		id1 := sessionIDFromPath(path1)
		id2 := sessionIDFromPath(path2)
		assert.Contains(t, ids, id1)
		assert.Contains(t, ids, id2)

		// verify sessions were created
		s1 := m.Get(id1)
		require.NotNil(t, s1)
		assert.Equal(t, "docs/plan1.md", s1.GetMetadata().PlanPath)

		s2 := m.Get(id2)
		require.NotNil(t, s2)
		assert.Equal(t, "docs/plan2.md", s2.GetMetadata().PlanPath)
	})

	t.Run("returns empty for no matches", func(t *testing.T) {
		dir := t.TempDir()

		m := NewSessionManager()
		ids, err := m.Discover(dir)

		require.NoError(t, err)
		assert.Empty(t, ids)
	})

	t.Run("ignores non-matching files", func(t *testing.T) {
		dir := t.TempDir()

		// create non-matching files
		require.NoError(t, os.WriteFile(filepath.Join(dir, "other.txt"), []byte("test"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "progress.txt"), []byte("test"), 0o600))

		// create matching file
		path := filepath.Join(dir, "progress-valid.txt")
		createProgressFile(t, path, "plan.md", "main", "full")

		m := NewSessionManager()
		ids, err := m.Discover(dir)

		require.NoError(t, err)
		assert.Len(t, ids, 1)
		assert.Contains(t, ids, sessionIDFromPath(path))
	})

	t.Run("updates existing sessions", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "progress-test.txt")
		createProgressFile(t, path, "plan.md", "main", "full")

		m := NewSessionManager()

		// first discovery
		_, err := m.Discover(dir)
		require.NoError(t, err)

		id := sessionIDFromPath(path)
		s := m.Get(id)
		require.NotNil(t, s)
		assert.Equal(t, "main", s.GetMetadata().Branch)

		// update the file
		createProgressFile(t, path, "plan.md", "feature", "review")

		// second discovery
		_, err = m.Discover(dir)
		require.NoError(t, err)

		// should update metadata
		assert.Equal(t, "feature", s.GetMetadata().Branch)
	})
}

func TestSessionManager_DiscoverLogsErrorForBrokenFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Chmod doesn't restrict read access on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permissions, can't simulate unreadable file")
	}

	dir := t.TempDir()

	// create a valid progress file
	validPath := filepath.Join(dir, "progress-valid.txt")
	createProgressFile(t, validPath, "valid.md", "main", "full")

	// create a broken progress file (unreadable)
	brokenPath := filepath.Join(dir, "progress-broken.txt")
	createProgressFile(t, brokenPath, "broken.md", "main", "full")
	require.NoError(t, os.Chmod(brokenPath, 0o000))
	t.Cleanup(func() {
		_ = os.Chmod(brokenPath, 0o600) // restore for cleanup
	})

	// capture log output
	var buf bytes.Buffer
	origOut := log.Writer()
	origFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(origOut)
		log.SetFlags(origFlags)
	})

	m := NewSessionManager()
	defer m.Close()

	ids, err := m.Discover(dir)
	require.NoError(t, err)

	// both IDs should be returned (Discover adds to list before updateSession)
	assert.Len(t, ids, 2)

	// valid session should be processed successfully
	validID := sessionIDFromPath(validPath)
	validSession := m.Get(validID)
	require.NotNil(t, validSession, "valid session should be processed despite broken sibling")

	// broken session should have been skipped (not added to manager)
	brokenID := sessionIDFromPath(brokenPath)
	brokenSession := m.Get(brokenID)
	assert.Nil(t, brokenSession, "broken session should not be in manager")

	// verify error was logged
	logOutput := buf.String()
	assert.Contains(t, logOutput, "[WARN]", "should log warning for broken file")
	assert.Contains(t, logOutput, "broken", "log should reference the broken session")
}

func TestSessionManager_Get(t *testing.T) {
	m := NewSessionManager()

	t.Run("returns nil for missing session", func(t *testing.T) {
		assert.Nil(t, m.Get("nonexistent"))
	})

	t.Run("returns session after discover", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "progress-test.txt")
		createProgressFile(t, path, "plan.md", "main", "full")

		_, err := m.Discover(dir)
		require.NoError(t, err)

		id := sessionIDFromPath(path)
		s := m.Get(id)
		assert.NotNil(t, s)
		assert.Equal(t, id, s.ID)
	})
}

func TestSessionManager_All(t *testing.T) {
	dir := t.TempDir()
	createProgressFile(t, filepath.Join(dir, "progress-a.txt"), "a.md", "main", "full")
	createProgressFile(t, filepath.Join(dir, "progress-b.txt"), "b.md", "main", "full")

	m := NewSessionManager()
	_, err := m.Discover(dir)
	require.NoError(t, err)

	all := m.All()
	assert.Len(t, all, 2)
}

func TestSessionManager_Remove(t *testing.T) {
	dir := t.TempDir()
	createProgressFile(t, filepath.Join(dir, "progress-test.txt"), "plan.md", "main", "full")

	m := NewSessionManager()
	_, err := m.Discover(dir)
	require.NoError(t, err)

	id := sessionIDFromPath(filepath.Join(dir, "progress-test.txt"))
	require.NotNil(t, m.Get(id))

	m.Remove(id)

	assert.Nil(t, m.Get(id))
}

func TestSessionManager_Close(t *testing.T) {
	dir := t.TempDir()
	createProgressFile(t, filepath.Join(dir, "progress-a.txt"), "a.md", "main", "full")
	createProgressFile(t, filepath.Join(dir, "progress-b.txt"), "b.md", "main", "full")

	m := NewSessionManager()
	_, err := m.Discover(dir)
	require.NoError(t, err)

	assert.Len(t, m.All(), 2)

	m.Close()

	assert.Empty(t, m.All())
}

func TestSessionManager_Register(t *testing.T) {
	t.Run("basic registration adds session to manager", func(t *testing.T) {
		m := NewSessionManager()
		defer m.Close()

		path := "/tmp/progress-test-register.txt"
		session := NewSession("initial-id", path)
		defer session.Close()

		m.Register(session)

		// verify session ID was derived from path
		expectedID := sessionIDFromPath(path)
		assert.Equal(t, expectedID, session.ID, "session ID should be derived from path")

		// verify session is retrievable
		got := m.Get(expectedID)
		require.NotNil(t, got, "registered session should be retrievable")
		assert.Equal(t, session, got)
	})

	t.Run("session ID is derived from path correctly", func(t *testing.T) {
		m := NewSessionManager()
		defer m.Close()

		path := "/home/user/project/progress-my-feature.txt"
		session := NewSession("wrong-id", path)
		defer session.Close()

		m.Register(session)

		// the session ID should be updated to match what sessionIDFromPath returns
		expectedID := sessionIDFromPath(path)
		assert.Equal(t, expectedID, session.ID)
		assert.True(t, strings.HasPrefix(session.ID, "my-feature-"), "ID should start with plan name")
	})

	t.Run("existing session is NOT overwritten", func(t *testing.T) {
		m := NewSessionManager()
		defer m.Close()

		path := "/tmp/progress-idempotent.txt"
		session1 := NewSession("id1", path)
		defer session1.Close()
		session1.SetMetadata(SessionMetadata{Branch: "first"})

		session2 := NewSession("id2", path)
		defer session2.Close()
		session2.SetMetadata(SessionMetadata{Branch: "second"})

		// register first session
		m.Register(session1)

		// try to register second session with same path
		m.Register(session2)

		// first session should still be there
		expectedID := sessionIDFromPath(path)
		got := m.Get(expectedID)
		require.NotNil(t, got)
		assert.Equal(t, "first", got.GetMetadata().Branch, "original session should not be overwritten")
	})

	t.Run("registered session is retrievable via Get", func(t *testing.T) {
		m := NewSessionManager()
		defer m.Close()

		path := "/tmp/progress-retrievable.txt"
		session := NewSession("any-id", path)
		defer session.Close()
		session.SetMetadata(SessionMetadata{
			PlanPath: "docs/plans/test.md",
			Branch:   "feature-branch",
			Mode:     "full",
		})

		m.Register(session)

		// retrieve and verify all metadata is preserved
		expectedID := sessionIDFromPath(path)
		got := m.Get(expectedID)
		require.NotNil(t, got)

		meta := got.GetMetadata()
		assert.Equal(t, "docs/plans/test.md", meta.PlanPath)
		assert.Equal(t, "feature-branch", meta.Branch)
		assert.Equal(t, "full", meta.Mode)
	})
}

func TestSessionIDFromPath(t *testing.T) {
	t.Run("includes base name and hash", func(t *testing.T) {
		got := sessionIDFromPath("/tmp/progress-my-plan.txt")
		assert.True(t, strings.HasPrefix(got, "my-plan-"))

		lastDash := strings.LastIndex(got, "-")
		require.NotEqual(t, -1, lastDash)
		suffix := got[lastDash+1:]
		assert.Len(t, suffix, 16)
		_, err := strconv.ParseUint(suffix, 16, 64)
		assert.NoError(t, err)
	})

	t.Run("different paths produce different IDs", func(t *testing.T) {
		id1 := sessionIDFromPath("/tmp/progress-test.txt")
		id2 := sessionIDFromPath("/other/progress-test.txt")
		assert.NotEqual(t, id1, id2)
	})

	t.Run("same path is stable", func(t *testing.T) {
		path := "/tmp/progress-simple.txt"
		id1 := sessionIDFromPath(path)
		id2 := sessionIDFromPath(path)
		assert.Equal(t, id1, id2)
	})
}

func TestIsActive(t *testing.T) {
	t.Run("returns false for unlocked file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "progress-test.txt")
		createProgressFile(t, path, "plan.md", "main", "full")

		active, err := IsActive(path)
		require.NoError(t, err)
		assert.False(t, active)
	})

	t.Run("returns true for active progress logger", func(t *testing.T) {
		dir := t.TempDir()
		planPath := filepath.Join(dir, "plan.md")
		require.NoError(t, os.WriteFile(planPath, []byte("# plan"), 0o600))

		oldWd, err := os.Getwd()
		require.NoError(t, err)
		require.NoError(t, os.Chdir(dir))
		t.Cleanup(func() {
			_ = os.Chdir(oldWd)
		})

		holder := &status.PhaseHolder{}
		logger, err := progress.NewLogger(progress.Config{
			PlanFile: planPath,
			Mode:     "full",
			Branch:   "main",
		}, testColors(), holder)
		require.NoError(t, err)
		t.Cleanup(func() {
			_ = logger.Close()
		})

		active, err := IsActive(logger.Path())
		require.NoError(t, err)
		assert.True(t, active)
	})

	t.Run("returns error for missing file", func(t *testing.T) {
		_, err := IsActive("/nonexistent/path")
		assert.Error(t, err)
	})
}

func TestSessionManager_DiscoverLoadsCompletedSessionContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "progress-completed.txt")

	// create a progress file with content (simulating a completed session)
	content := `# Ralphex Progress Log
Plan: docs/plan.md
Branch: main
Mode: full
Started: 2026-01-22 10:00:00
------------------------------------------------------------

--- Task 1 ---
[26-01-22 10:00:01] task output
[26-01-22 10:00:02] <<<RALPHEX:ALL_TASKS_DONE>>>
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	m := NewSessionManager()
	defer m.Close()

	// discover the session (it's not locked, so will be completed)
	_, err := m.Discover(dir)
	require.NoError(t, err)

	sessionID := sessionIDFromPath(path)
	session := m.Get(sessionID)
	require.NotNil(t, session)

	// verify the session state is completed
	assert.Equal(t, SessionStateCompleted, session.GetState())

	// verify the session is marked as loaded (content was published to SSE server)
	assert.True(t, session.IsLoaded(), "completed session should be marked as loaded")
}

// TestSessionManager_UpdateSession_CompletedToActiveResumesFromOffset verifies
// that when updateSession observes a completed -> active transition on a session
// with content already ingested (lastOffset > 0), it resumes from the stored
// offset via Reactivate rather than re-tailing the whole file from byte 0.
// this is the flock-race recovery path: RefreshStates falsely marks a live
// session completed and captures the offset, then Discover sees the flock
// re-held and flips the state back to active.
func TestSessionManager_UpdateSession_CompletedToActiveResumesFromOffset(t *testing.T) {
	dir := t.TempDir()
	planPath := filepath.Join(dir, "plan.md")
	require.NoError(t, os.WriteFile(planPath, []byte("# plan"), 0o600))

	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	holder := &status.PhaseHolder{}
	logger, err := progress.NewLogger(progress.Config{
		PlanFile: planPath,
		Mode:     "full",
		Branch:   "main",
	}, testColors(), holder)
	require.NoError(t, err)
	t.Cleanup(func() { _ = logger.Close() })

	progressPath := logger.Path()

	// write some content into the progress file so there is a non-trivial
	// offset to resume from.
	f, err := os.OpenFile(progressPath, os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	_, err = f.WriteString("[26-01-22 10:00:01] early line\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	preRaceSize, err := os.Stat(progressPath)
	require.NoError(t, err)

	// set up a completed+loaded session with the pre-race offset, as if
	// RefreshStates had just observed a transient flock release and stopped
	// the tailer.
	id := sessionIDFromPath(progressPath)
	session := NewSession(id, progressPath)
	session.SetState(SessionStateCompleted)
	require.True(t, session.MarkLoadedIfNot())
	session.setLastOffset(preRaceSize.Size())

	m := NewSessionManager()
	m.Register(session)

	// the logger still holds the flock, so IsActive returns true.
	active, err := IsActive(progressPath)
	require.NoError(t, err)
	require.True(t, active, "logger should still hold the flock")

	// drive updateSession directly - this is the entry point for the
	// completed -> active transition that the flock race exposes.
	require.NoError(t, m.updateSession(session))

	assert.Equal(t, SessionStateActive, session.GetState(),
		"session should transition back to active")

	// tailer must have resumed from the stored lastOffset, not rewound to 0.
	require.Eventually(t, func() bool {
		return session.IsTailing() && session.GetTailer() != nil
	}, time.Second, 10*time.Millisecond, "tailer should be running after transition")

	tailerOffset := session.GetTailer().Offset()
	assert.GreaterOrEqual(t, tailerOffset, preRaceSize.Size(),
		"tailer offset must be at or past the stored lastOffset; got %d, want >= %d",
		tailerOffset, preRaceSize.Size())
}

// TestSessionManager_UpdateSession_CompletedKeepsOffsetSkipsLoader verifies that
// when updateSession observes a completed session whose lastOffset > 0 (content
// already ingested by a prior tailer) and the flock races false a second time
// so newState stays completed, the historical-file loader is NOT run. otherwise
// the loader would re-emit every event the tailer already published, duplicating
// them in the SSE replay buffer. exercises the tailer-first / loader-second race
// path that sits alongside the loader-first / tailer-second race the plan calls
// out in its Concurrent loadProgressFileIntoSession design note.
func TestSessionManager_UpdateSession_CompletedKeepsOffsetSkipsLoader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "progress-completed-with-offset.txt")

	content := `# Ralphex Progress Log
Plan: docs/plan.md
Branch: main
Mode: full
Started: 2026-01-22 10:00:00
------------------------------------------------------------

--- Task 1 ---
[26-01-22 10:00:01] early line
[26-01-22 10:00:02] another line
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	// model the state that a RefreshStates flock race would leave behind for
	// an active session that was being tailed from the start:
	//   - state = completed (RefreshStates saw a transient flock release)
	//   - loaded = false (StartTailing(true) never marks loaded)
	//   - lastOffset > 0 (StopTailing captured the tailer's byte offset)
	id := sessionIDFromPath(path)
	session := NewSession(id, path)
	session.SetState(SessionStateCompleted)
	const preRaceOffset int64 = 100
	session.setLastOffset(preRaceOffset)
	require.False(t, session.IsLoaded())

	m := NewSessionManager()
	m.Register(session)
	t.Cleanup(func() { m.Close() })

	// file is not locked, so IsActive returns false - this models the second
	// flock race where updateSession observes the file as completed again.
	active, err := IsActive(path)
	require.NoError(t, err)
	require.False(t, active, "file must not be locked to reproduce the race")

	require.NoError(t, m.updateSession(session))

	assert.Equal(t, SessionStateCompleted, session.GetState(),
		"newState must stay completed when IsActive races false")
	assert.True(t, session.IsLoaded(),
		"session must be marked loaded so the watcher's Reactivate gate can fire")
	assert.Equal(t, preRaceOffset, session.getLastOffset(),
		"loader must not run: it would overwrite lastOffset with the file size and re-emit events")
}

// TestSessionManager_UpdateSession_DetectsFileRestart verifies that when a
// progress file is reused by a new ralphex run (truncated + re-initialized
// with a new Started: timestamp), updateSession resets per-run state so the
// subsequent loader or tailer reads the fresh file from byte 0 rather than
// seeking to the stale offset from the previous run. this closes the
// truncation race where the new run can grow past the old offset before
// StartFromOffset's `offset > fileSize` check would fire.
func TestSessionManager_UpdateSession_DetectsFileRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "progress-restart.txt")

	// simulate previous run: session has stored metadata, lastOffset > 0,
	// loaded=true. then the progress file is rewritten with new content
	// whose header carries a different Started: timestamp, and (critically)
	// the new file has ALREADY grown past the previous lastOffset, so the
	// offset>size fallback in StartFromOffset would not trigger.
	oldContent := `# Ralphex Progress Log
Plan: docs/plan.md
Branch: main
Mode: full
Started: 2026-01-22 10:00:00
------------------------------------------------------------

[26-01-22 10:00:01] run 1 line
`
	require.NoError(t, os.WriteFile(path, []byte(oldContent), 0o600))

	id := sessionIDFromPath(path)
	session := NewSession(id, path)
	// establish stored metadata from the original file before it gets rewritten
	m := NewSessionManager()
	m.Register(session)
	t.Cleanup(func() { m.Close() })
	require.NoError(t, m.updateSession(session))
	require.Equal(t, "2026-01-22 10:00:00",
		session.GetMetadata().StartTime.Format("2006-01-02 15:04:05"),
		"precondition: stored metadata reflects run 1 header")
	require.True(t, session.IsLoaded(), "precondition: run 1 content loaded")
	run1Offset := session.getLastOffset()
	require.Positive(t, run1Offset, "precondition: run 1 recorded an offset")

	// rewrite the file with run 2 content (truncate + new header + enough
	// bytes so the new file exceeds run1Offset without hitting offset>size).
	var newContent strings.Builder
	newContent.WriteString(`# Ralphex Progress Log
Plan: docs/plan.md
Branch: main
Mode: full
Started: 2026-01-22 11:00:00
------------------------------------------------------------

`)
	for i := 0; newContent.Len() <= int(run1Offset)+200; i++ {
		fmt.Fprintf(&newContent, "[26-01-22 11:00:%02d] run 2 line %d\n", i%60, i)
	}
	require.NoError(t, os.WriteFile(path, []byte(newContent.String()), 0o600))
	stat, err := os.Stat(path)
	require.NoError(t, err)
	require.Greater(t, stat.Size(), run1Offset,
		"precondition: new file has grown past run1Offset to defeat offset>size fallback")

	// drive updateSession again - this models the watcher picking up a Write
	// event after the file restart.
	require.NoError(t, m.updateSession(session))

	assert.Equal(t, "2026-01-22 11:00:00",
		session.GetMetadata().StartTime.Format("2006-01-02 15:04:05"),
		"metadata should reflect the new run's header")
	assert.Equal(t, stat.Size(), session.getLastOffset(),
		"after restart detection, loader should re-run on fresh file and record new offset")
	assert.True(t, session.IsLoaded(),
		"session remains marked loaded after loader processes the new run")
}

// TestSessionManager_UpdateSession_PartialHeaderPreservesMetadata verifies that
// a mid-write observation of a truncate+rewrite (header lines streaming in, but
// terminating separator not yet written) does NOT clobber the previously stored
// StartTime. writeHeader issues several writes and fsnotify can deliver a Write
// event between them; if updateSession replaced stored metadata with the zero
// StartTime from that partial read, the next event with the full header would
// compare against a zero oldMeta.StartTime and skip the restart reset, leaving
// stale lastOffset/loaded in place for the new run.
func TestSessionManager_UpdateSession_PartialHeaderPreservesMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "progress-partial.txt")

	// run 1: full header seen, metadata + offset recorded
	run1Content := `# Ralphex Progress Log
Plan: docs/plan.md
Branch: main
Mode: full
Started: 2026-01-22 10:00:00
------------------------------------------------------------

[26-01-22 10:00:01] run 1 line
`
	require.NoError(t, os.WriteFile(path, []byte(run1Content), 0o600))

	id := sessionIDFromPath(path)
	session := NewSession(id, path)
	m := NewSessionManager()
	m.Register(session)
	t.Cleanup(func() { m.Close() })
	require.NoError(t, m.updateSession(session))
	require.Equal(t, "2026-01-22 10:00:00",
		session.GetMetadata().StartTime.Format("2006-01-02 15:04:05"))
	run1Offset := session.getLastOffset()
	require.Positive(t, run1Offset)

	// simulate a mid-write observation: header lines but no separator yet.
	// Started: is still missing at this point — writeHeader writes lines in
	// order, so a Write event between writeFile("Mode:") and writeFile("Started:")
	// delivers this exact content.
	partial := `# Ralphex Progress Log
Plan: docs/plan.md
Branch: main
Mode: full
`
	require.NoError(t, os.WriteFile(path, []byte(partial), 0o600))
	require.NoError(t, m.updateSession(session))

	// the partial parse must NOT have overwritten the run 1 StartTime; otherwise
	// the next event cannot detect the restart.
	assert.Equal(t, "2026-01-22 10:00:00",
		session.GetMetadata().StartTime.Format("2006-01-02 15:04:05"),
		"partial header parse must not clobber previously stored StartTime")

	// now the full run 2 header lands. restart detection must fire here and
	// reset per-run state (lastOffset, loaded) before the loader runs on the
	// new content.
	var run2 strings.Builder
	run2.WriteString(`# Ralphex Progress Log
Plan: docs/plan.md
Branch: main
Mode: full
Started: 2026-01-22 11:00:00
------------------------------------------------------------

`)
	// grow past run1Offset so the StartFromOffset>size fallback cannot rescue us
	for i := 0; run2.Len() <= int(run1Offset)+200; i++ {
		fmt.Fprintf(&run2, "[26-01-22 11:00:%02d] run 2 line %d\n", i%60, i)
	}
	require.NoError(t, os.WriteFile(path, []byte(run2.String()), 0o600))
	stat, err := os.Stat(path)
	require.NoError(t, err)
	require.Greater(t, stat.Size(), run1Offset)

	require.NoError(t, m.updateSession(session))

	assert.Equal(t, "2026-01-22 11:00:00",
		session.GetMetadata().StartTime.Format("2006-01-02 15:04:05"),
		"metadata should reflect the new run once the full header is visible")
	assert.Equal(t, stat.Size(), session.getLastOffset(),
		"restart reset must run so loader re-ingests from byte 0, recording the new file size")
}

// TestSessionManager_UpdateSession_PartialHeaderSkipsLoader verifies that a
// fresh-discovery observation of a partial header (no separator yet written)
// does NOT invoke loadProgressFileIntoSession and does NOT mark the session
// loaded. without the headerComplete gate, the loader would record a
// lastOffset pointing inside the unfinished header; a later Reactivate
// triggered by the rest of the header arriving would then resume mid-header
// and emit the remaining header lines as output events. this is primarily
// a Windows concern (IsActive is a no-op so every new discovery is marked
// completed immediately), but the gate also protects Unix against the rare
// race where IsActive momentarily returns false mid-write.
func TestSessionManager_UpdateSession_PartialHeaderSkipsLoader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "progress-partial-new.txt")

	// simulate a mid-write observation: header lines streaming in but the
	// terminating separator not yet written. writeHeader issues several
	// writeFile calls, and fsnotify can deliver a Write event between any two
	// of them.
	partial := `# Ralphex Progress Log
Plan: docs/plan.md
Branch: main
Mode: full
`
	require.NoError(t, os.WriteFile(path, []byte(partial), 0o600))

	id := sessionIDFromPath(path)
	session := NewSession(id, path)
	m := NewSessionManager()
	m.Register(session)
	t.Cleanup(func() { m.Close() })

	require.NoError(t, m.updateSession(session))

	// partial-header discovery must not load or set an offset; otherwise a later
	// Reactivate would resume inside the header.
	assert.False(t, session.IsLoaded(),
		"partial-header discovery must not mark session loaded")
	assert.Zero(t, session.getLastOffset(),
		"partial-header discovery must not record a lastOffset inside the header")

	// once the full header arrives, the loader must run and record the correct
	// offset (total file size) since header lines are skipped but still counted
	// toward bytesRead.
	full := partial + `Started: 2026-01-22 10:00:00
------------------------------------------------------------

[26-01-22 10:00:01] first content line
`
	require.NoError(t, os.WriteFile(path, []byte(full), 0o600))
	require.NoError(t, m.updateSession(session))

	assert.True(t, session.IsLoaded(),
		"session must be marked loaded once header is complete")
	stat, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, stat.Size(), session.getLastOffset(),
		"lastOffset must match total file size after full-header load")
}

// TestSessionManager_UpdateSession_SameStartTimeNoReset verifies that repeated
// updateSession calls on the same run (same Started: timestamp) do not reset
// per-run state. this is the normal flock-race recovery path: updateSession
// must be a no-op for stored state when the file has not been restarted.
func TestSessionManager_UpdateSession_SameStartTimeNoReset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "progress-same.txt")

	content := `# Ralphex Progress Log
Plan: docs/plan.md
Branch: main
Mode: full
Started: 2026-01-22 10:00:00
------------------------------------------------------------

[26-01-22 10:00:01] some line
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	id := sessionIDFromPath(path)
	session := NewSession(id, path)
	m := NewSessionManager()
	m.Register(session)
	t.Cleanup(func() { m.Close() })

	require.NoError(t, m.updateSession(session))
	offset1 := session.getLastOffset()
	require.Positive(t, offset1)
	require.True(t, session.IsLoaded())

	// second updateSession with the file unchanged: per-run state must be preserved
	require.NoError(t, m.updateSession(session))
	assert.Equal(t, offset1, session.getLastOffset(),
		"lastOffset must not change when Started: timestamp is unchanged")
	assert.True(t, session.IsLoaded(),
		"loaded flag must remain set when Started: timestamp is unchanged")
}

func TestSessionManager_EvictOldCompleted(t *testing.T) {
	t.Run("evicts oldest completed sessions when limit exceeded", func(t *testing.T) {
		dir := t.TempDir()
		m := NewSessionManager()

		// create more than MaxCompletedSessions progress files
		numSessions := MaxCompletedSessions + 5
		paths := make([]string, numSessions)

		for i := range numSessions {
			path := filepath.Join(dir, "progress-plan"+strconv.Itoa(i)+".txt")
			// use different start times so we can predict which ones get evicted
			startTime := time.Date(2026, 1, 1, 10, 0, i, 0, time.UTC)
			content := `# Ralphex Progress Log
Plan: plan` + strconv.Itoa(i) + `.md
Branch: main
Mode: full
Started: ` + startTime.Format("2006-01-02 15:04:05") + `
------------------------------------------------------------
`
			require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
			paths[i] = path
		}

		// discover all sessions
		_, err := m.Discover(dir)
		require.NoError(t, err)

		// verify only MaxCompletedSessions remain (oldest should be evicted)
		all := m.All()
		assert.LessOrEqual(t, len(all), MaxCompletedSessions, "should not exceed MaxCompletedSessions")
	})

	t.Run("does not evict when under limit", func(t *testing.T) {
		dir := t.TempDir()
		m := NewSessionManager()

		// create fewer than MaxCompletedSessions
		numSessions := 5
		for i := range numSessions {
			path := filepath.Join(dir, "progress-small"+strconv.Itoa(i)+".txt")
			createProgressFile(t, path, "plan"+strconv.Itoa(i)+".md", "main", "full")
		}

		_, err := m.Discover(dir)
		require.NoError(t, err)

		// all sessions should remain
		all := m.All()
		assert.Len(t, all, numSessions)
	})
}

func TestSessionManager_RefreshStates(t *testing.T) {
	t.Run("skips non-tailing sessions", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "progress-test.txt")
		createProgressFile(t, path, "plan.md", "main", "full")

		m := NewSessionManager()
		_, err := m.Discover(dir)
		require.NoError(t, err)

		sessionID := sessionIDFromPath(path)
		session := m.Get(sessionID)
		require.NotNil(t, session)

		// session is not tailing
		assert.False(t, session.IsTailing())
		initialState := session.GetState()

		// RefreshStates should skip non-tailing sessions
		m.RefreshStates()

		// state should remain unchanged
		assert.Equal(t, initialState, session.GetState())
	})

	t.Run("marks unlocked tailing session as completed", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "progress-test.txt")
		createProgressFile(t, path, "plan.md", "main", "full")

		m := NewSessionManager()
		_, err := m.Discover(dir)
		require.NoError(t, err)

		sessionID := sessionIDFromPath(path)
		session := m.Get(sessionID)
		require.NotNil(t, session)

		// manually start tailing and set to active
		err = session.StartTailing(true)
		require.NoError(t, err)
		assert.True(t, session.IsTailing())

		// RefreshStates should detect the unlocked file and mark as completed
		m.RefreshStates()

		// since file is not locked by another process, it should be marked completed
		time.Sleep(50 * time.Millisecond) // give goroutine time to stop
		assert.Equal(t, SessionStateCompleted, session.GetState())
		assert.False(t, session.IsTailing())
	})
}

func testColors() *progress.Colors {
	return progress.NewColors(config.ColorConfig{
		Task:       "0,255,0",
		Review:     "0,255,255",
		Codex:      "255,0,255",
		ClaudeEval: "100,200,255",
		Warn:       "255,255,0",
		Error:      "255,0,0",
		Signal:     "255,100,100",
		Timestamp:  "138,138,138",
		Info:       "180,180,180",
	})
}

// helper to create a progress file with standard header
func createProgressFile(t *testing.T, path, plan, branch, mode string) {
	t.Helper()
	content := `# Ralphex Progress Log
Plan: ` + plan + `
Branch: ` + branch + `
Mode: ` + mode + `
Started: 2026-01-22 10:00:00
------------------------------------------------------------

`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func TestSessionManager_DiscoverRecursive_SubdirProgressFiles(t *testing.T) {
	t.Run("discovers files in .ralphex/progress/ subdirectory", func(t *testing.T) {
		root := t.TempDir()

		// create .ralphex/progress/ subdirectory with progress files
		progressDir := filepath.Join(root, ".ralphex", "progress")
		require.NoError(t, os.MkdirAll(progressDir, 0o750))

		path1 := filepath.Join(progressDir, "progress-feature-a.txt")
		path2 := filepath.Join(progressDir, "progress-feature-b.txt")
		createProgressFile(t, path1, "docs/plans/feature-a.md", "feature-a", "full")
		createProgressFile(t, path2, "docs/plans/feature-b.md", "feature-b", "review")

		m := NewSessionManager()
		defer m.Close()

		ids, err := m.DiscoverRecursive(root)
		require.NoError(t, err)
		assert.Len(t, ids, 2)

		// verify sessions were created with correct metadata
		id1 := sessionIDFromPath(path1)
		id2 := sessionIDFromPath(path2)
		assert.Contains(t, ids, id1)
		assert.Contains(t, ids, id2)

		s1 := m.Get(id1)
		require.NotNil(t, s1)
		assert.Equal(t, "docs/plans/feature-a.md", s1.GetMetadata().PlanPath)

		s2 := m.Get(id2)
		require.NotNil(t, s2)
		assert.Equal(t, "docs/plans/feature-b.md", s2.GetMetadata().PlanPath)
	})

	t.Run("discovers files in both root and .ralphex/progress/ simultaneously", func(t *testing.T) {
		root := t.TempDir()

		// create progress file in root (old location)
		oldPath := filepath.Join(root, "progress-old-plan.txt")
		createProgressFile(t, oldPath, "docs/plans/old-plan.md", "old-branch", "full")

		// create progress file in .ralphex/progress/ (new location)
		progressDir := filepath.Join(root, ".ralphex", "progress")
		require.NoError(t, os.MkdirAll(progressDir, 0o750))
		newPath := filepath.Join(progressDir, "progress-new-plan.txt")
		createProgressFile(t, newPath, "docs/plans/new-plan.md", "new-branch", "review")

		m := NewSessionManager()
		defer m.Close()

		ids, err := m.DiscoverRecursive(root)
		require.NoError(t, err)
		assert.Len(t, ids, 2, "should find files in both old and new locations")

		// verify old-location session
		oldID := sessionIDFromPath(oldPath)
		assert.Contains(t, ids, oldID)
		oldSession := m.Get(oldID)
		require.NotNil(t, oldSession)
		assert.Equal(t, "docs/plans/old-plan.md", oldSession.GetMetadata().PlanPath)
		assert.Equal(t, "old-branch", oldSession.GetMetadata().Branch)

		// verify new-location session
		newID := sessionIDFromPath(newPath)
		assert.Contains(t, ids, newID)
		newSession := m.Get(newID)
		require.NotNil(t, newSession)
		assert.Equal(t, "docs/plans/new-plan.md", newSession.GetMetadata().PlanPath)
		assert.Equal(t, "new-branch", newSession.GetMetadata().Branch)
	})
}
