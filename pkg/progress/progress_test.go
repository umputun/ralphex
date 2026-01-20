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
			got := progressFilename(tc.planFile, tc.mode)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestWrapText(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		width int
		want  string
	}{
		{
			name:  "no wrap needed",
			text:  "short text",
			width: 80,
			want:  "short text",
		},
		{
			name:  "wraps at word boundary",
			text:  "this is a longer text that needs wrapping",
			width: 20,
			want:  "this is a longer\ntext that needs\nwrapping",
		},
		{
			name:  "single long word",
			text:  "superlongwordthatcannotbewrapped",
			width: 10,
			want:  "superlongwordthatcannotbewrapped",
		},
		{
			name:  "zero width returns original",
			text:  "test text",
			width: 0,
			want:  "test text",
		},
		{
			name:  "empty text",
			text:  "",
			width: 40,
			want:  "",
		},
		{
			name:  "exact fit",
			text:  "exact fit",
			width: 9,
			want:  "exact fit",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := wrapText(tc.text, tc.width)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestGetTerminalWidth(t *testing.T) {
	// test with COLUMNS env var
	t.Run("uses COLUMNS env var", func(t *testing.T) {
		t.Setenv("COLUMNS", "100")
		width := getTerminalWidth()
		// should return 100 - 20 = 80
		assert.Equal(t, 80, width)
	})

	t.Run("respects min width", func(t *testing.T) {
		t.Setenv("COLUMNS", "50") // 50 - 20 = 30, but min is 40
		width := getTerminalWidth()
		assert.Equal(t, 40, width)
	})

	t.Run("invalid COLUMNS", func(t *testing.T) {
		t.Setenv("COLUMNS", "invalid")
		width := getTerminalWidth()
		// should fall back to default or syscall result
		assert.Positive(t, width)
	})
}

func TestExtractSignal(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"full signal", "<<<RALPHEX:ALL_TASKS_DONE>>>", "ALL_TASKS_DONE"},
		{"codex review done", "<<<RALPHEX:CODEX_REVIEW_DONE>>>", "CODEX_REVIEW_DONE"},
		{"review done", "<<<RALPHEX:REVIEW_DONE>>>", "REVIEW_DONE"},
		{"task failed", "<<<RALPHEX:TASK_FAILED>>>", "TASK_FAILED"},
		{"signal in text", "some text <<<RALPHEX:DONE>>> more text", "DONE"},
		{"no signal", "regular text", ""},
		{"incomplete prefix", "<<<RALPHEX:SIGNAL", ""},
		{"incomplete suffix", "RALPHEX:SIGNAL>>>", ""},
		{"empty", "", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractSignal(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestIsListItem(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"1. first item", true},
		{"12. item twelve", true},
		{"123. large number", true},
		{"- bullet item", true},
		{"* star item", true},
		{"regular text", false},
		{"1 no dot", false},
		{"1.no space", false},
		{".1 dot first", false},
		{"", false},
		{"  - already indented", false}, // has leading space, won't match
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := isListItem(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFormatListItem(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"numbered list", "1. first item", "  1. first item"},
		{"bullet list", "- bullet item", "  - bullet item"},
		{"star list", "* star item", "  * star item"},
		{"regular text", "regular text", "regular text"},
		{"already indented", "  - item", "  - item"},
		{"double digit", "12. item", "  12. item"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatListItem(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}
