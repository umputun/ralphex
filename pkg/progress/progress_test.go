package progress

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fatih/color"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewLogger(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name     string
		cfg      Config
		wantPath string
	}{
		{name: "full mode with plan", cfg: Config{PlanFile: "docs/plans/feature.md", Mode: "full", Branch: "main"}, wantPath: "progress-feature.txt"},
		{name: "review mode with plan", cfg: Config{PlanFile: "docs/plans/feature.md", Mode: "review", Branch: "main"}, wantPath: "progress-feature-review.txt"},
		{name: "codex-only mode with plan", cfg: Config{PlanFile: "docs/plans/feature.md", Mode: "codex-only", Branch: "main"}, wantPath: "progress-feature-codex.txt"},
		{name: "full mode no plan", cfg: Config{Mode: "full", Branch: "main"}, wantPath: "progress.txt"},
		{name: "review mode no plan", cfg: Config{Mode: "review", Branch: "main"}, wantPath: "progress-review.txt"},
		{name: "codex-only mode no plan", cfg: Config{Mode: "codex-only", Branch: "main"}, wantPath: "progress-codex.txt"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// change to tmpDir for test
			origDir, _ := os.Getwd()
			require.NoError(t, os.Chdir(tmpDir))
			defer func() { _ = os.Chdir(origDir) }()

			l, err := NewLogger(tc.cfg)
			require.NoError(t, err)
			defer l.Close()

			assert.Equal(t, tc.wantPath, filepath.Base(l.Path()))

			// verify header written
			content, err := os.ReadFile(l.Path())
			require.NoError(t, err)
			assert.Contains(t, string(content), "# Ralphex Progress Log")
			assert.Contains(t, string(content), "Mode: "+tc.cfg.Mode)
		})
	}
}

func TestLogger_Print(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	l, err := NewLogger(Config{Mode: "full", Branch: "test", NoColor: true})
	require.NoError(t, err)
	defer func() { _ = l.Close() }()

	// capture stdout
	var buf bytes.Buffer
	l.stdout = &buf

	l.Print("test message %d", 42)

	// check file output
	content, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	assert.Contains(t, string(content), "test message 42")

	// check stdout (no color)
	assert.Contains(t, buf.String(), "test message 42")
}

func TestLogger_PrintRaw(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	l, err := NewLogger(Config{Mode: "full", Branch: "test", NoColor: true})
	require.NoError(t, err)
	defer func() { _ = l.Close() }()

	var buf bytes.Buffer
	l.stdout = &buf

	l.PrintRaw("raw output")

	content, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	assert.Contains(t, string(content), "raw output")
	assert.Contains(t, buf.String(), "raw output")
}

func TestLogger_PrintAligned(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	l, err := NewLogger(Config{Mode: "full", Branch: "test", NoColor: true})
	require.NoError(t, err)
	defer func() { _ = l.Close() }()

	var buf bytes.Buffer
	l.stdout = &buf

	l.PrintAligned("first line\nsecond line\nthird line")

	content, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	// check file has timestamps and proper formatting
	assert.Contains(t, string(content), "] first line")
	assert.Contains(t, string(content), "second line")
	assert.Contains(t, string(content), "third line")

	// check stdout output
	output := buf.String()
	assert.Contains(t, output, "first line")
	assert.Contains(t, output, "second line")
	// lines should end with newlines
	assert.True(t, strings.HasSuffix(output, "\n"), "output should end with newline")
}

func TestLogger_PrintAligned_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	l, err := NewLogger(Config{Mode: "full", Branch: "test", NoColor: true})
	require.NoError(t, err)
	defer func() { _ = l.Close() }()

	var buf bytes.Buffer
	l.stdout = &buf

	l.PrintAligned("") // empty string should do nothing

	assert.Empty(t, buf.String())
}

func TestLogger_Error(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	l, err := NewLogger(Config{Mode: "full", Branch: "test", NoColor: true})
	require.NoError(t, err)
	defer func() { _ = l.Close() }()

	var buf bytes.Buffer
	l.stdout = &buf

	l.Error("something failed: %s", "reason")

	content, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	assert.Contains(t, string(content), "ERROR: something failed: reason")
	assert.Contains(t, buf.String(), "ERROR: something failed: reason")
}

func TestLogger_Warn(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	l, err := NewLogger(Config{Mode: "full", Branch: "test", NoColor: true})
	require.NoError(t, err)
	defer func() { _ = l.Close() }()

	var buf bytes.Buffer
	l.stdout = &buf

	l.Warn("warning message")

	content, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	assert.Contains(t, string(content), "WARN: warning message")
	assert.Contains(t, buf.String(), "WARN: warning message")
}

func TestLogger_SetPhase(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	// enable colors for this test
	origNoColor := color.NoColor
	color.NoColor = false
	defer func() { color.NoColor = origNoColor }()

	l, err := NewLogger(Config{Mode: "full", Branch: "test"})
	require.NoError(t, err)
	defer func() { _ = l.Close() }()

	var buf bytes.Buffer
	l.stdout = &buf

	l.SetPhase(PhaseTask)
	l.Print("task output")

	l.SetPhase(PhaseReview)
	l.Print("review output")

	l.SetPhase(PhaseCodex)
	l.Print("codex output")

	output := buf.String()
	// check for ANSI escape sequences (color codes start with \033[)
	assert.Contains(t, output, "\033[")
	assert.Contains(t, output, "task output")
	assert.Contains(t, output, "review output")
	assert.Contains(t, output, "codex output")
}

func TestLogger_ColorDisabled(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	// save original and restore after test
	origNoColor := color.NoColor
	defer func() { color.NoColor = origNoColor }()

	l, err := NewLogger(Config{Mode: "full", Branch: "test", NoColor: true})
	require.NoError(t, err)
	defer func() { _ = l.Close() }()

	var buf bytes.Buffer
	l.stdout = &buf

	l.SetPhase(PhaseTask)
	l.Print("no color output")

	output := buf.String()
	// should not contain ANSI escape sequences
	assert.NotContains(t, output, "\033[")
	assert.Contains(t, output, "no color output")
}

func TestLogger_Elapsed(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	l, err := NewLogger(Config{Mode: "full", Branch: "test"})
	require.NoError(t, err)
	defer l.Close()

	elapsed := l.Elapsed()
	// go-humanize returns "now" for very short durations
	assert.NotEmpty(t, elapsed)
}

func TestLogger_Close(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	l, err := NewLogger(Config{Mode: "full", Branch: "test"})
	require.NoError(t, err)

	l.Print("some output")
	err = l.Close()
	require.NoError(t, err)

	content, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	assert.Contains(t, string(content), "Completed:")
	assert.Contains(t, string(content), strings.Repeat("-", 60))
}

func TestGetProgressFilename(t *testing.T) {
	tests := []struct {
		planFile string
		mode     string
		want     string
	}{
		{"docs/plans/feature.md", "full", "progress-feature.txt"},
		{"docs/plans/feature.md", "review", "progress-feature-review.txt"},
		{"docs/plans/feature.md", "codex-only", "progress-feature-codex.txt"},
		{"", "full", "progress.txt"},
		{"", "review", "progress-review.txt"},
		{"", "codex-only", "progress-codex.txt"},
		{"plans/2024-01-15-refactor.md", "full", "progress-2024-01-15-refactor.txt"},
	}

	for _, tc := range tests {
		t.Run(tc.planFile+"_"+tc.mode, func(t *testing.T) {
			got := getProgressFilename(tc.planFile, tc.mode)
			assert.Equal(t, tc.want, got)
		})
	}
}
