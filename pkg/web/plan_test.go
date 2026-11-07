package web

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

	t.Run("loads an in-place date rename before an archived copy", func(t *testing.T) {
		tests := []struct {
			name, original, renamed string
			staleCompleted          bool
		}{
			{name: "dashed to compact", original: "2026-05-12-foo.md", renamed: "20260512-foo.md"},
			{name: "compact to dashed", original: "20260512-foo.md", renamed: "2026-05-12-foo.md"},
			{name: "live rename wins", original: "2026-05-12-foo.md", renamed: "20260512-foo.md", staleCompleted: true},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				tmpDir := t.TempDir()
				originalPath := filepath.Join(tmpDir, tt.original)
				renamedPath := filepath.Join(tmpDir, tt.renamed)
				require.NoError(t, os.WriteFile(originalPath, []byte("# Live Plan\n"), 0o600))
				require.NoError(t, os.Rename(originalPath, renamedPath))
				if tt.staleCompleted {
					completedDir := filepath.Join(tmpDir, "completed")
					require.NoError(t, os.MkdirAll(completedDir, 0o750))
					for _, name := range []string{tt.original, tt.renamed} {
						require.NoError(t, os.WriteFile(filepath.Join(completedDir, name), []byte("# Archived Plan\n"), 0o600))
					}
				}

				p, err := loadPlanWithFallback(originalPath)
				require.NoError(t, err)
				assert.Equal(t, "Live Plan", p.Title)
			})
		}
	})

	t.Run("returns error when not found in either location", func(t *testing.T) {
		tmpDir := t.TempDir()
		nonexistentPath := filepath.Join(tmpDir, "nonexistent.md")

		_, err := loadPlanWithFallback(nonexistentPath)
		require.Error(t, err)
	})

	t.Run("falls back to alt-date basename in completed/ (dashed to compact)", func(t *testing.T) {
		tmpDir := t.TempDir()
		completedDir := filepath.Join(tmpDir, "completed")
		require.NoError(t, os.MkdirAll(completedDir, 0o750))

		// plan only exists under compact-date basename in completed/
		completedPlan := filepath.Join(completedDir, "20260512-foo.md")
		planContent := `# Renamed Plan

### Task 1: Done

- [x] Item
`
		require.NoError(t, os.WriteFile(completedPlan, []byte(planContent), 0o600))

		// request the dashed-date original path
		originalPath := filepath.Join(tmpDir, "2026-05-12-foo.md")
		plan, err := loadPlanWithFallback(originalPath)
		require.NoError(t, err)
		require.NotNil(t, plan)
		assert.Equal(t, "Renamed Plan", plan.Title)
	})

	t.Run("falls back to alt-date basename in completed/ (compact to dashed)", func(t *testing.T) {
		tmpDir := t.TempDir()
		completedDir := filepath.Join(tmpDir, "completed")
		require.NoError(t, os.MkdirAll(completedDir, 0o750))

		// plan only exists under dashed-date basename in completed/
		completedPlan := filepath.Join(completedDir, "2026-05-12-foo.md")
		planContent := `# Renamed Plan

### Task 1: Done

- [x] Item
`
		require.NoError(t, os.WriteFile(completedPlan, []byte(planContent), 0o600))

		// request the compact-date original path
		originalPath := filepath.Join(tmpDir, "20260512-foo.md")
		plan, err := loadPlanWithFallback(originalPath)
		require.NoError(t, err)
		require.NotNil(t, plan)
		assert.Equal(t, "Renamed Plan", plan.Title)
	})
}
