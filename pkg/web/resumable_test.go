package web

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindResumableSessions(t *testing.T) {
	tests := []struct {
		name          string
		files         map[string]string // filename -> content
		wantCount     int
		wantDesc      string // expected description of first result
		wantQACount   int
		wantResumable bool
	}{
		{
			name: "incomplete plan session is resumable",
			files: map[string]string{
				"progress-plan-test.txt": `# Ralphex Progress Log
Plan: add user authentication
Branch: main
Mode: plan
Started: 2026-01-25 10:30:00
------------------------------------------------------------

[26-01-25 10:30:01] Starting plan creation...
[26-01-25 10:30:05] QUESTION: What approach?
[26-01-25 10:30:05] OPTIONS: A, B, C
[26-01-25 10:30:10] ANSWER: B
`,
			},
			wantCount:     1,
			wantDesc:      "add user authentication",
			wantQACount:   1,
			wantResumable: true,
		},
		{
			name: "completed plan session not resumable",
			files: map[string]string{
				"progress-plan-test.txt": `# Ralphex Progress Log
Plan: add feature
Branch: main
Mode: plan
Started: 2026-01-25 10:30:00
------------------------------------------------------------

[26-01-25 10:30:01] Starting plan creation...
[26-01-25 10:30:10] <<<RALPHEX:PLAN_READY>>>

------------------------------------------------------------
Completed: 2026-01-25 10:35:00 (5 minutes ago)
`,
			},
			wantCount:     0,
			wantResumable: false,
		},
		{
			name: "plan ready signal marks session complete",
			files: map[string]string{
				"progress-plan-test.txt": `# Ralphex Progress Log
Plan: add feature
Branch: main
Mode: plan
Started: 2026-01-25 10:30:00
------------------------------------------------------------

[26-01-25 10:30:10] <<<RALPHEX:PLAN_READY>>>
`,
			},
			wantCount:     0,
			wantResumable: false,
		},
		{
			name: "non-plan mode not resumable",
			files: map[string]string{
				"progress-plan-test.txt": `# Ralphex Progress Log
Plan: docs/plans/feature.md
Branch: main
Mode: full
Started: 2026-01-25 10:30:00
------------------------------------------------------------

[26-01-25 10:30:01] Starting execution...
`,
			},
			wantCount:     0,
			wantResumable: false,
		},
		{
			name: "multiple Q&A pairs counted",
			files: map[string]string{
				"progress-plan-test.txt": `# Ralphex Progress Log
Plan: complex feature
Branch: feature-branch
Mode: plan
Started: 2026-01-25 10:30:00
------------------------------------------------------------

[26-01-25 10:30:05] QUESTION: First question?
[26-01-25 10:30:05] OPTIONS: A, B
[26-01-25 10:30:10] ANSWER: A
[26-01-25 10:30:15] QUESTION: Second question?
[26-01-25 10:30:15] OPTIONS: X, Y, Z
[26-01-25 10:30:20] ANSWER: Z
[26-01-25 10:30:25] QUESTION: Third question?
[26-01-25 10:30:25] OPTIONS: 1, 2
[26-01-25 10:30:30] ANSWER: 1
`,
			},
			wantCount:   1,
			wantDesc:    "complex feature",
			wantQACount: 3,
		},
		{
			name: "empty directory returns no sessions",
			files: map[string]string{
				"other-file.txt": "not a progress file",
			},
			wantCount: 0,
		},
		{
			name: "non-plan progress files ignored",
			files: map[string]string{
				"progress-feature.txt": `# Ralphex Progress Log
Plan: docs/plans/feature.md
Branch: main
Mode: full
Started: 2026-01-25 10:30:00
------------------------------------------------------------
`,
			},
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// create temp directory
			tmpDir := t.TempDir()

			// create test files
			for name, content := range tt.files {
				path := filepath.Join(tmpDir, name)
				err := os.WriteFile(path, []byte(content), 0o600)
				require.NoError(t, err)
			}

			// find resumable sessions
			sessions, err := FindResumableSessions([]string{tmpDir})
			require.NoError(t, err)

			assert.Len(t, sessions, tt.wantCount)

			if tt.wantCount > 0 && len(sessions) > 0 {
				assert.Equal(t, tt.wantDesc, sessions[0].PlanDescription)
				assert.Equal(t, tt.wantQACount, sessions[0].QACount)
				assert.Equal(t, tmpDir, sessions[0].Dir)
			}
		})
	}
}

func TestFindResumableSessions_MultipleDirs(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	// create resumable session in dir1
	content1 := `# Ralphex Progress Log
Plan: feature one
Branch: main
Mode: plan
Started: 2026-01-25 10:30:00
------------------------------------------------------------

[26-01-25 10:30:01] Starting...
`
	require.NoError(t, os.WriteFile(filepath.Join(dir1, "progress-plan-one.txt"), []byte(content1), 0o600))

	// create resumable session in dir2
	content2 := `# Ralphex Progress Log
Plan: feature two
Branch: develop
Mode: plan
Started: 2026-01-25 11:00:00
------------------------------------------------------------

[26-01-25 11:00:01] Starting...
`
	require.NoError(t, os.WriteFile(filepath.Join(dir2, "progress-plan-two.txt"), []byte(content2), 0o600))

	sessions, err := FindResumableSessions([]string{dir1, dir2})
	require.NoError(t, err)

	assert.Len(t, sessions, 2)

	// check both directories are represented
	dirs := make(map[string]bool)
	for _, s := range sessions {
		dirs[s.Dir] = true
	}
	assert.True(t, dirs[dir1])
	assert.True(t, dirs[dir2])
}

func TestFindResumableSessions_NonExistentDir(t *testing.T) {
	// should not error on non-existent directories
	sessions, err := FindResumableSessions([]string{"/nonexistent/path/12345"})
	require.NoError(t, err)
	assert.Empty(t, sessions)
}

func TestScanProgressFile(t *testing.T) {
	tests := []struct {
		name          string
		content       string
		wantCompleted bool
		wantQACount   int
	}{
		{
			name:          "empty file",
			content:       "",
			wantCompleted: false,
			wantQACount:   0,
		},
		{
			name: "completed footer",
			content: `[26-01-25 10:30:01] Some output
------------------------------------------------------------
Completed: 2026-01-25 10:35:00 (5 minutes ago)
`,
			wantCompleted: true,
			wantQACount:   0,
		},
		{
			name: "plan ready signal",
			content: `[26-01-25 10:30:01] Some output
[26-01-25 10:30:10] <<<RALPHEX:PLAN_READY>>>
`,
			wantCompleted: true,
			wantQACount:   0,
		},
		{
			name: "incomplete with qa",
			content: `[26-01-25 10:30:01] Starting...
[26-01-25 10:30:05] QUESTION: What?
[26-01-25 10:30:10] ANSWER: Something
`,
			wantCompleted: false,
			wantQACount:   1,
		},
		{
			name: "inline question block does not block answer or completion",
			content: `[26-01-25 10:30:01] <<<RALPHEX:QUESTION>>>{"question":"Pick one?","options":["A","B"]}<<<RALPHEX:END>>>
[26-01-25 10:30:03] ANSWER: A
[26-01-25 10:30:04] Completed: 2026-01-25 10:35:00 (5 minutes ago)
`,
			wantCompleted: true,
			wantQACount:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpFile := filepath.Join(t.TempDir(), "test.txt")
			require.NoError(t, os.WriteFile(tmpFile, []byte(tt.content), 0o600))

			completed, qaCount, _, _, err := scanProgressFile(tmpFile)
			require.NoError(t, err)
			assert.Equal(t, tt.wantCompleted, completed)
			assert.Equal(t, tt.wantQACount, qaCount)
		})
	}
}

func TestScanProgressFile_PendingQuestion(t *testing.T) {
	t.Run("detects pending question from json block", func(t *testing.T) {
		content := `[26-01-25 10:30:01] <<<RALPHEX:QUESTION>>>
[26-01-25 10:30:01] {"question": "Pick one?", "options": ["A", "B"]}
[26-01-25 10:30:01] <<<RALPHEX:END>>>
`
		tmpFile := filepath.Join(t.TempDir(), "test.txt")
		require.NoError(t, os.WriteFile(tmpFile, []byte(content), 0o600))

		completed, qaCount, pendingQ, pendingOpts, err := scanProgressFile(tmpFile)
		require.NoError(t, err)
		assert.False(t, completed)
		assert.Equal(t, 0, qaCount)
		assert.Equal(t, "Pick one?", pendingQ)
		assert.Equal(t, []string{"A", "B"}, pendingOpts)
	})

	t.Run("detects pending question from inline json block", func(t *testing.T) {
		content := `[26-01-25 10:30:01] <<<RALPHEX:QUESTION>>>{"question": "Pick one?", "options": ["A", "B"]}<<<RALPHEX:END>>>
`
		tmpFile := filepath.Join(t.TempDir(), "test.txt")
		require.NoError(t, os.WriteFile(tmpFile, []byte(content), 0o600))

		completed, qaCount, pendingQ, pendingOpts, err := scanProgressFile(tmpFile)
		require.NoError(t, err)
		assert.False(t, completed)
		assert.Equal(t, 0, qaCount)
		assert.Equal(t, "Pick one?", pendingQ)
		assert.Equal(t, []string{"A", "B"}, pendingOpts)
	})

	t.Run("clears pending question after answer", func(t *testing.T) {
		content := `[26-01-25 10:30:01] QUESTION: What?
[26-01-25 10:30:02] OPTIONS: One, Two
[26-01-25 10:30:03] ANSWER: One
`
		tmpFile := filepath.Join(t.TempDir(), "test.txt")
		require.NoError(t, os.WriteFile(tmpFile, []byte(content), 0o600))

		completed, qaCount, pendingQ, pendingOpts, err := scanProgressFile(tmpFile)
		require.NoError(t, err)
		assert.False(t, completed)
		assert.Equal(t, 1, qaCount)
		assert.Empty(t, pendingQ)
		assert.Empty(t, pendingOpts)
	})
}

func TestCheckResumable(t *testing.T) {
	t.Run("resumable plan session", func(t *testing.T) {
		content := `# Ralphex Progress Log
Plan: my feature
Branch: feature-x
Mode: plan
Started: 2026-01-25 10:30:00
------------------------------------------------------------

[26-01-25 10:30:01] Working...
`
		tmpFile := filepath.Join(t.TempDir(), "progress-plan-test.txt")
		require.NoError(t, os.WriteFile(tmpFile, []byte(content), 0o600))

		session, ok, err := checkResumable(tmpFile)
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, "my feature", session.PlanDescription)
		assert.Equal(t, "feature-x", session.Branch)
		assert.Equal(t, time.Date(2026, 1, 25, 10, 30, 0, 0, time.UTC), session.StartTime)
	})

	t.Run("non-plan mode not resumable", func(t *testing.T) {
		content := `# Ralphex Progress Log
Plan: docs/plans/feature.md
Branch: main
Mode: full
Started: 2026-01-25 10:30:00
------------------------------------------------------------

[26-01-25 10:30:01] Working...
`
		tmpFile := filepath.Join(t.TempDir(), "progress-plan-test.txt")
		require.NoError(t, os.WriteFile(tmpFile, []byte(content), 0o600))

		_, ok, err := checkResumable(tmpFile)
		require.NoError(t, err)
		assert.False(t, ok)
	})
}
