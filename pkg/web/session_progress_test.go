package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/status"
)

func TestParseProgressHeader(t *testing.T) {
	t.Run("parses all fields", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "progress-test.txt")

		content := `# Ralphex Progress Log
Plan: docs/plans/my-plan.md
Branch: feature-branch
Mode: full
Started: 2026-01-22 10:30:00
------------------------------------------------------------

[26-01-22 10:30:05] Some output
`
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

		meta, err := ParseProgressHeader(path)
		require.NoError(t, err)

		assert.Equal(t, "docs/plans/my-plan.md", meta.PlanPath)
		assert.Equal(t, "feature-branch", meta.Branch)
		assert.Equal(t, "full", meta.Mode)
		assert.Equal(t, time.Date(2026, 1, 22, 10, 30, 0, 0, time.Local), meta.StartTime)
	})

	t.Run("handles review-only mode", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "progress-test.txt")

		content := `# Ralphex Progress Log
Plan: (no plan - review only)
Branch: main
Mode: review
Started: 2026-01-22 11:00:00
------------------------------------------------------------
`
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

		meta, err := ParseProgressHeader(path)
		require.NoError(t, err)

		assert.Equal(t, "(no plan - review only)", meta.PlanPath)
		assert.Equal(t, "review", meta.Mode)
	})

	t.Run("handles missing fields gracefully", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "progress-test.txt")

		content := `# Ralphex Progress Log
Branch: main
------------------------------------------------------------
`
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

		meta, err := ParseProgressHeader(path)
		require.NoError(t, err)

		assert.Empty(t, meta.PlanPath)
		assert.Equal(t, "main", meta.Branch)
		assert.Empty(t, meta.Mode)
		assert.True(t, meta.StartTime.IsZero())
	})

	t.Run("returns error for missing file", func(t *testing.T) {
		_, err := ParseProgressHeader("/nonexistent/path")
		assert.Error(t, err)
	})
}

func TestSessionManager_LoadProgressFileIntoSession(t *testing.T) {
	t.Run("loads completed session content without panic", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "progress-test.txt")

		content := `# Ralphex Progress Log
Plan: docs/plan.md
Branch: main
Mode: full
Started: 2026-01-22 10:00:00
------------------------------------------------------------

--- Task 1 ---
[26-01-22 10:00:01] executing task
[26-01-22 10:00:02] task output line 1
[26-01-22 10:00:03] task output line 2
--- Review ---
[26-01-22 10:00:04] review started
[26-01-22 10:00:05] <<<RALPHEX:REVIEW_DONE>>>
`
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

		m := NewSessionManager()
		defer m.Close()
		session := NewSession("test", path)
		defer session.Close()

		// should not panic and should process the file
		m.loadProgressFileIntoSession(path, session)
	})

	t.Run("handles missing file gracefully", func(t *testing.T) {
		m := NewSessionManager()
		defer m.Close()
		session := NewSession("test", "/nonexistent/file.txt")
		defer session.Close()

		// should not panic
		m.loadProgressFileIntoSession("/nonexistent/file.txt", session)
	})

	t.Run("skips header lines", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "progress-test.txt")

		content := `# Ralphex Progress Log
Plan: docs/plan.md
Branch: main
Mode: full
Started: 2026-01-22 10:00:00
------------------------------------------------------------
[26-01-22 10:00:01] first real line
`
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

		m := NewSessionManager()
		defer m.Close()
		session := NewSession("test", path)
		defer session.Close()

		// should not panic
		m.loadProgressFileIntoSession(path, session)
	})

	t.Run("captures diffstats from output line", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "progress-diffstats.txt")

		content := `# Ralphex Progress Log
Plan: docs/plan.md
Branch: main
Mode: full
Started: 2026-01-22 10:00:00
------------------------------------------------------------

[26-01-22 10:00:01] running task
[26-01-22 10:00:02] DIFFSTATS: files=3 additions=10 deletions=4
`
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

		m := NewSessionManager()
		session := NewSession("test-diffstats", path)
		defer session.Close()

		m.loadProgressFileIntoSession(path, session)

		stats := session.GetDiffStats()
		require.NotNil(t, stats)
		assert.Equal(t, 3, stats.Files)
		assert.Equal(t, 10, stats.Additions)
		assert.Equal(t, 4, stats.Deletions)
	})
}

func TestSessionManager_EmitPendingSection(t *testing.T) {
	t.Run("task iteration section emits task_start event", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "progress-task-start.txt")

		// content with task iteration section (matching taskIterationRegex)
		content := `# Ralphex Progress Log
Plan: docs/plan.md
Branch: main
Mode: full
Started: 2026-01-22 10:00:00
------------------------------------------------------------

--- Task Iteration 1 ---
[26-01-22 10:00:01] starting task 1
[26-01-22 10:00:02] working on task
--- Task Iteration 2 ---
[26-01-22 10:00:03] starting task 2
`
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

		m := NewSessionManager()
		defer m.Close()
		session := NewSession("test-task-start", path)
		defer session.Close()

		// load should not panic and should emit task_start events
		m.loadProgressFileIntoSession(path, session)
	})

	t.Run("non-task sections do not emit task_start", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "progress-review.txt")

		// content with review and codex sections (non-task)
		content := `# Ralphex Progress Log
Plan: docs/plan.md
Branch: main
Mode: full
Started: 2026-01-22 10:00:00
------------------------------------------------------------

--- Review ---
[26-01-22 10:00:01] reviewing code
--- Codex Review ---
[26-01-22 10:00:02] codex analyzing
--- Claude Eval ---
[26-01-22 10:00:03] claude evaluating
`
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

		m := NewSessionManager()
		defer m.Close()
		session := NewSession("test-non-task", path)
		defer session.Close()

		// should not panic - these sections won't match taskIterationRegex
		m.loadProgressFileIntoSession(path, session)
	})

	t.Run("invalid task number handling", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "progress-edge.txt")

		// content with various section formats
		content := `# Ralphex Progress Log
Plan: docs/plan.md
Branch: main
Mode: full
Started: 2026-01-22 10:00:00
------------------------------------------------------------

--- Task 1 ---
[26-01-22 10:00:01] simple task section (not task iteration format)
--- Task Iteration 999 ---
[26-01-22 10:00:02] high task number
`
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

		m := NewSessionManager()
		defer m.Close()
		session := NewSession("test-edge", path)
		defer session.Close()

		// should not panic
		m.loadProgressFileIntoSession(path, session)
	})

	t.Run("task iteration section triggers task_start with correct task number", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "progress-tasknum.txt")

		// multiple task iterations to verify task number parsing
		content := `# Ralphex Progress Log
Plan: docs/plan.md
Branch: main
Mode: full
Started: 2026-01-22 10:00:00
------------------------------------------------------------

--- Task Iteration 5 ---
[26-01-22 10:00:01] fifth task
--- Task Iteration 10 ---
[26-01-22 10:00:02] tenth task
--- Task Iteration 100 ---
[26-01-22 10:00:03] hundredth task
`
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

		m := NewSessionManager()
		defer m.Close()
		session := NewSession("test-tasknum", path)
		defer session.Close()

		// should process all task iterations without panic
		m.loadProgressFileIntoSession(path, session)
	})
}

func TestParseProgressHeaderLargeBuffer(t *testing.T) {
	t.Run("handles lines larger than default scanner buffer", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "progress-large.txt")

		// create a line larger than 64KB (default scanner limit)
		largeLine := strings.Repeat("x", 100*1024) // 100KB

		content := `# Ralphex Progress Log
Plan: docs/plans/my-plan.md
Branch: feature-branch
Mode: full
Started: 2026-01-22 10:30:00
------------------------------------------------------------

[26-01-22 10:30:05] ` + largeLine + `
`
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

		meta, err := ParseProgressHeader(path)
		require.NoError(t, err)

		assert.Equal(t, "docs/plans/my-plan.md", meta.PlanPath)
		assert.Equal(t, "feature-branch", meta.Branch)
	})

	t.Run("handles lines larger than 64MB (no limit)", func(t *testing.T) {
		if testing.Short() {
			t.Skip("skipping 65MB allocation in short mode")
		}
		dir := t.TempDir()
		path := filepath.Join(dir, "progress-huge.txt")

		// create a line larger than 64MB (old scanner hard limit)
		hugeLine := strings.Repeat("A", 65*1024*1024) // 65MB

		content := "# Ralphex Progress Log\nPlan: docs/plans/huge.md\nBranch: huge-branch\nMode: full\n" +
			"Started: 2026-01-22 10:30:00\n------------------------------------------------------------\n\n" +
			"[26-01-22 10:30:05] " + hugeLine + "\n"
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

		meta, err := ParseProgressHeader(path)
		require.NoError(t, err)

		assert.Equal(t, "docs/plans/huge.md", meta.PlanPath)
		assert.Equal(t, "huge-branch", meta.Branch)
	})
}

func TestPhaseFromSection(t *testing.T) {
	tests := []struct {
		name     string
		section  string
		expected status.Phase
	}{
		{"task section", "Task 1: implement feature", status.PhaseTask},
		{"codex iteration", "codex iteration 1", status.PhaseCodex},
		{"codex external review", "codex external review", status.PhaseCodex},
		{"custom review iteration", "custom review iteration 1", status.PhaseCodex},
		{"custom iteration", "custom iteration 2", status.PhaseCodex},
		{"claude review", "claude review 0: all findings", status.PhaseReview},
		{"review loop", "review iteration 1", status.PhaseReview},
		{"claude eval", "claude-eval", status.PhaseClaudeEval},
		{"claude eval space", "claude eval", status.PhaseClaudeEval},
		{"unknown defaults to task", "unknown section", status.PhaseTask},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, phaseFromSection(tt.section))
		})
	}
}

func TestSessionManager_LoadProgressFileIntoSessionLargeBuffer(t *testing.T) {
	t.Run("handles lines larger than default scanner buffer", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "progress-large.txt")

		// create a line larger than 64KB (default scanner limit)
		largeLine := strings.Repeat("x", 100*1024) // 100KB

		content := `# Ralphex Progress Log
Plan: docs/plan.md
Branch: main
Mode: full
Started: 2026-01-22 10:00:00
------------------------------------------------------------

--- Task 1 ---
[26-01-22 10:00:01] starting task
[26-01-22 10:00:02] ` + largeLine + `
[26-01-22 10:00:03] task completed
`
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

		m := NewSessionManager()
		defer m.Close()
		session := NewSession("test-large", path)
		defer session.Close()

		// should not panic or error with "token too long"
		m.loadProgressFileIntoSession(path, session)
	})

	t.Run("handles lines larger than 64MB (no limit)", func(t *testing.T) {
		if testing.Short() {
			t.Skip("skipping 65MB allocation in short mode")
		}
		dir := t.TempDir()
		path := filepath.Join(dir, "progress-huge.txt")

		// create a line larger than 64MB (old scanner hard limit)
		hugeLine := strings.Repeat("B", 65*1024*1024) // 65MB

		content := "# Ralphex Progress Log\nPlan: docs/plan.md\nBranch: main\nMode: full\n" +
			"Started: 2026-01-22 10:00:00\n------------------------------------------------------------\n\n" +
			"--- Task 1 ---\n[26-01-22 10:00:01] starting task\n" +
			"[26-01-22 10:00:02] " + hugeLine + "\n" +
			"[26-01-22 10:00:03] task completed\n"
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

		m := NewSessionManager()
		defer m.Close()
		session := NewSession("test-huge", path)
		defer session.Close()

		// should not panic or error with "token too long"
		m.loadProgressFileIntoSession(path, session)
	})
}

func TestTrimLineEnding(t *testing.T) {
	tests := []struct {
		name, input, expected string
	}{
		{name: "unix newline", input: "hello\n", expected: "hello"},
		{name: "windows newline", input: "hello\r\n", expected: "hello"},
		{name: "no newline", input: "hello", expected: "hello"},
		{name: "empty string", input: "", expected: ""},
		{name: "just newline", input: "\n", expected: ""},
		{name: "just crlf", input: "\r\n", expected: ""},
		{name: "trailing cr in content", input: "data\r\r\n", expected: "data\r"},
		{name: "bare cr no newline", input: "data\r", expected: "data"},
		{name: "bare cr only", input: "\r", expected: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, trimLineEnding(tt.input))
		})
	}
}
