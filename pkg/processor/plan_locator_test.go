package processor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlanLocator_Path(t *testing.T) {
	t.Run("original path exists", func(t *testing.T) {
		planPath := filepath.Join(t.TempDir(), "plan.md")
		require.NoError(t, os.WriteFile(planPath, []byte("# plan"), 0o600))

		locator := newPlanLocator(Config{PlanFile: planPath})
		assert.Equal(t, planPath, locator.Path())
	})

	t.Run("alternate date in completed directory", func(t *testing.T) {
		plansDir := filepath.Join(t.TempDir(), "docs", "plans")
		completedDir := filepath.Join(plansDir, "completed")
		require.NoError(t, os.MkdirAll(completedDir, 0o700))

		originalPath := filepath.Join(plansDir, "2026-05-25-runner.md")
		completedPath := filepath.Join(completedDir, "20260525-runner.md")
		require.NoError(t, os.WriteFile(completedPath, []byte("# plan"), 0o600))

		locator := newPlanLocator(Config{PlanFile: originalPath})
		assert.Equal(t, completedPath, locator.Path())
	})

	t.Run("missing path falls back to original", func(t *testing.T) {
		locator := newPlanLocator(Config{PlanFile: filepath.Join(t.TempDir(), "missing.md")})
		assert.Equal(t, locator.cfg.PlanFile, locator.Path())
	})
}
