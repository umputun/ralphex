package plan

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/config"
	"github.com/umputun/ralphex/pkg/progress"
)

func TestNewSelector(t *testing.T) {
	colors := progress.NewColors(config.ColorConfig{
		Task: "0,255,0", Review: "255,255,0", Codex: "255,165,0",
		ClaudeEval: "0,255,255", Warn: "255,165,0", Error: "255,0,0",
		Signal: "255,0,255", Timestamp: "128,128,128", Info: "255,255,255",
	})

	sel := NewSelector("/tmp/plans", colors)
	assert.Equal(t, "/tmp/plans", sel.PlansDir)
	assert.Equal(t, colors, sel.Colors)
}

func TestSelector_Select(t *testing.T) {
	colors := progress.NewColors(config.ColorConfig{
		Task: "0,255,0", Review: "255,255,0", Codex: "255,165,0",
		ClaudeEval: "0,255,255", Warn: "255,165,0", Error: "255,0,0",
		Signal: "255,0,255", Timestamp: "128,128,128", Info: "255,255,255",
	})

	t.Run("existing file returns absolute path", func(t *testing.T) {
		tmpDir := t.TempDir()
		planFile := filepath.Join(tmpDir, "test.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Test"), 0o600))

		sel := NewSelector(tmpDir, colors)
		result, err := sel.Select(context.Background(), planFile, false)
		require.NoError(t, err)
		assert.True(t, filepath.IsAbs(result))
		assert.Equal(t, planFile, result)
	})

	t.Run("non-existing file returns error", func(t *testing.T) {
		tmpDir := t.TempDir()
		sel := NewSelector(tmpDir, colors)
		_, err := sel.Select(context.Background(), "/nonexistent/plan.md", false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "plan file not found")
	})

	t.Run("empty planFile with optional returns empty string", func(t *testing.T) {
		tmpDir := t.TempDir()
		sel := NewSelector(tmpDir, colors)
		result, err := sel.Select(context.Background(), "", true)
		require.NoError(t, err)
		assert.Empty(t, result)
	})

	t.Run("empty planFile without optional returns error", func(t *testing.T) {
		tmpDir := t.TempDir()
		sel := NewSelector(tmpDir, colors)
		_, err := sel.Select(context.Background(), "", false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no plans found")
	})
}

func TestSelector_SelectWithFzf(t *testing.T) {
	colors := progress.NewColors(config.ColorConfig{
		Task: "0,255,0", Review: "255,255,0", Codex: "255,165,0",
		ClaudeEval: "0,255,255", Warn: "255,165,0", Error: "255,0,0",
		Signal: "255,0,255", Timestamp: "128,128,128", Info: "255,255,255",
	})

	t.Run("missing directory returns error", func(t *testing.T) {
		sel := NewSelector("/nonexistent", colors)
		_, err := sel.selectWithFzf(context.Background())
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNoPlansFound)
	})

	t.Run("empty directory returns error", func(t *testing.T) {
		tmpDir := t.TempDir()
		sel := NewSelector(tmpDir, colors)
		_, err := sel.selectWithFzf(context.Background())
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNoPlansFound)
	})

	t.Run("single plan auto-selects", func(t *testing.T) {
		tmpDir := t.TempDir()
		planFile := filepath.Join(tmpDir, "test.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Test"), 0o600))

		sel := NewSelector(tmpDir, colors)
		result, err := sel.selectWithFzf(context.Background())
		require.NoError(t, err)
		assert.Equal(t, planFile, result)
	})
}

func TestSelector_FindRecent(t *testing.T) {
	colors := progress.NewColors(config.ColorConfig{
		Task: "0,255,0", Review: "255,255,0", Codex: "255,165,0",
		ClaudeEval: "0,255,255", Warn: "255,165,0", Error: "255,0,0",
		Signal: "255,0,255", Timestamp: "128,128,128", Info: "255,255,255",
	})

	t.Run("finds most recent plan", func(t *testing.T) {
		tmpDir := t.TempDir()
		startTime := time.Now()

		// create older plan
		oldPlan := filepath.Join(tmpDir, "old.md")
		require.NoError(t, os.WriteFile(oldPlan, []byte("# Old"), 0o600))
		oldTime := startTime.Add(1 * time.Second)
		require.NoError(t, os.Chtimes(oldPlan, oldTime, oldTime))

		// create newer plan
		newPlan := filepath.Join(tmpDir, "new.md")
		require.NoError(t, os.WriteFile(newPlan, []byte("# New"), 0o600))
		newTime := startTime.Add(2 * time.Second)
		require.NoError(t, os.Chtimes(newPlan, newTime, newTime))

		sel := NewSelector(tmpDir, colors)
		result := sel.FindRecent(startTime)
		assert.Equal(t, newPlan, result)
	})

	t.Run("returns empty if no plans after startTime", func(t *testing.T) {
		tmpDir := t.TempDir()
		planFile := filepath.Join(tmpDir, "old.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Old"), 0o600))

		oldTime := time.Now().Add(-1 * time.Hour)
		require.NoError(t, os.Chtimes(planFile, oldTime, oldTime))

		sel := NewSelector(tmpDir, colors)
		result := sel.FindRecent(time.Now())
		assert.Empty(t, result)
	})

	t.Run("returns empty if directory empty", func(t *testing.T) {
		tmpDir := t.TempDir()
		sel := NewSelector(tmpDir, colors)
		result := sel.FindRecent(time.Now())
		assert.Empty(t, result)
	})
}

func TestExtractBranchName(t *testing.T) {
	tests := []struct {
		name     string
		planFile string
		want     string
	}{
		{
			name:     "simple name without date",
			planFile: "/path/to/feature.md",
			want:     "feature",
		},
		{
			name:     "name with date prefix",
			planFile: "/path/to/2024-01-15-feature.md",
			want:     "feature",
		},
		{
			name:     "name with date and dashes",
			planFile: "/path/to/2024-01-15-my-feature.md",
			want:     "my-feature",
		},
		{
			name:     "only date prefix",
			planFile: "/path/to/2024-01-15-.md",
			want:     "2024-01-15-",
		},
		{
			name:     "no extension",
			planFile: "/path/to/feature",
			want:     "feature",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractBranchName(tt.planFile)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestPromptDescription(t *testing.T) {
	colors := progress.NewColors(config.ColorConfig{
		Task: "0,255,0", Review: "255,255,0", Codex: "255,165,0",
		ClaudeEval: "0,255,255", Warn: "255,165,0", Error: "255,0,0",
		Signal: "255,0,255", Timestamp: "128,128,128", Info: "255,255,255",
	})

	t.Run("returns trimmed input", func(t *testing.T) {
		input := strings.NewReader("  add feature  \n")
		result := PromptDescription(context.Background(), input, colors)
		assert.Equal(t, "add feature", result)
	})

	t.Run("returns empty on EOF", func(t *testing.T) {
		input := strings.NewReader("")
		result := PromptDescription(context.Background(), input, colors)
		assert.Empty(t, result)
	})

	t.Run("handles context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel immediately

		input := strings.NewReader("add feature\n")
		result := PromptDescription(ctx, input, colors)
		assert.Empty(t, result)
	})
}
