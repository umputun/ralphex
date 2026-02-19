package progress

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fatih/color"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/config"
	"github.com/umputun/ralphex/pkg/status"
)

// testColors returns a Colors instance for testing with valid RGB values.
func testColors() *Colors {
	return NewColors(config.ColorConfig{
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

func TestNewLogger(t *testing.T) {
	tmpDir := t.TempDir()
	colors := testColors()

	tests := []struct {
		name     string
		cfg      Config
		wantBase string
		wantDir  string
	}{
		{name: "full mode with plan", cfg: Config{PlanFile: "docs/plans/feature.md", Mode: "full", Branch: "main"}, wantBase: "progress-feature.txt", wantDir: ".ralphex/progress"},
		{name: "review mode with plan", cfg: Config{PlanFile: "docs/plans/feature.md", Mode: "review", Branch: "main"}, wantBase: "progress-feature-review.txt", wantDir: ".ralphex/progress"},
		{name: "codex-only mode with plan", cfg: Config{PlanFile: "docs/plans/feature.md", Mode: "codex-only", Branch: "main"}, wantBase: "progress-feature-codex.txt", wantDir: ".ralphex/progress"},
		{name: "full mode no plan", cfg: Config{Mode: "full", Branch: "main"}, wantBase: "progress.txt", wantDir: ".ralphex/progress"},
		{name: "review mode no plan", cfg: Config{Mode: "review", Branch: "main"}, wantBase: "progress-review.txt", wantDir: ".ralphex/progress"},
		{name: "codex-only mode no plan", cfg: Config{Mode: "codex-only", Branch: "main"}, wantBase: "progress-codex.txt", wantDir: ".ralphex/progress"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// change to tmpDir for test
			origDir, _ := os.Getwd()
			require.NoError(t, os.Chdir(tmpDir))
			defer func() { _ = os.Chdir(origDir) }()

			holder := &status.PhaseHolder{}
			l, err := NewLogger(tc.cfg, colors, holder)
			require.NoError(t, err)
			defer l.Close()

			assert.Equal(t, tc.wantBase, filepath.Base(l.Path()))
			assert.Contains(t, l.Path(), tc.wantDir)

			// verify header written
			content, err := os.ReadFile(l.Path())
			require.NoError(t, err)
			assert.Contains(t, string(content), "# Ralphex Progress Log")
			assert.Contains(t, string(content), "Mode: "+tc.cfg.Mode)
		})
	}
}

func TestNewLogger_AppendOnRestart(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	colors := testColors()
	cfg := Config{PlanFile: "docs/plans/feature.md", Mode: "full", Branch: "main"}

	// create first logger, write some content
	holder := &status.PhaseHolder{}
	l1, err := NewLogger(cfg, colors, holder)
	require.NoError(t, err)
	l1.Print("first session output")

	// read content after first session
	firstContent, err := os.ReadFile(l1.Path())
	require.NoError(t, err)
	assert.Contains(t, string(firstContent), "# Ralphex Progress Log")
	assert.Contains(t, string(firstContent), "first session output")

	// simulate interrupted run: release lock and close file WITHOUT writing footer.
	// this mimics a crash/kill where Close() was never called.
	_ = unlockFile(l1.file)
	unregisterActiveLock(l1.file.Name())
	l1.file.Close()
	l1.file = nil // prevent double close in deferred cleanup

	// create second logger with same config (simulates restart after crash)
	l2, err := NewLogger(cfg, colors, holder)
	require.NoError(t, err)
	l2.Print("second session output")
	require.NoError(t, l2.Close())

	// read content after restart - same file as l1, now accessed via l2
	content, err := os.ReadFile(l2.Path())
	require.NoError(t, err)
	contentStr := string(content)

	// original content preserved
	assert.Contains(t, contentStr, "# Ralphex Progress Log")
	assert.Contains(t, contentStr, "first session output")

	// restart separator present (matches sectionRegex format)
	assert.Contains(t, contentStr, "--- restarted at")
	assert.Regexp(t, `--- restarted at \d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} ---`, contentStr)

	// second session content present
	assert.Contains(t, contentStr, "second session output")

	// header written only once
	assert.Equal(t, 1, strings.Count(contentStr, "# Ralphex Progress Log"))
}

func TestNewLogger_EmptyFileWritesHeader(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	// pre-create an empty progress file (0 bytes)
	require.NoError(t, os.MkdirAll(progressDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(progressDir, "progress-feature.txt"), nil, 0o600))

	// create logger - should write full header, not restart separator
	cfg := Config{PlanFile: "docs/plans/feature.md", Mode: "full", Branch: "main"}
	l, err := NewLogger(cfg, testColors(), &status.PhaseHolder{})
	require.NoError(t, err)
	require.NoError(t, l.Close())

	content, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	contentStr := string(content)
	assert.Contains(t, contentStr, "# Ralphex Progress Log")
	assert.NotContains(t, contentStr, "--- restarted at")
}

func TestLogger_Print(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	holder := &status.PhaseHolder{}
	l, err := NewLogger(Config{Mode: "full", Branch: "test", NoColor: true}, testColors(), holder)
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

	holder := &status.PhaseHolder{}
	l, err := NewLogger(Config{Mode: "full", Branch: "test", NoColor: true}, testColors(), holder)
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

func TestLogger_PrintSection(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	holder := &status.PhaseHolder{}
	l, err := NewLogger(Config{Mode: "full", Branch: "test", NoColor: true}, testColors(), holder)
	require.NoError(t, err)
	defer func() { _ = l.Close() }()

	var buf bytes.Buffer
	l.stdout = &buf

	section := status.NewGenericSection("test section")
	l.PrintSection(section)

	content, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	assert.Contains(t, string(content), "--- test section ---")
	assert.Contains(t, buf.String(), "--- test section ---")
}

func TestLogger_PrintAligned(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	holder := &status.PhaseHolder{}
	l, err := NewLogger(Config{Mode: "full", Branch: "test", NoColor: true}, testColors(), holder)
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

	holder := &status.PhaseHolder{}
	l, err := NewLogger(Config{Mode: "full", Branch: "test", NoColor: true}, testColors(), holder)
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

	holder := &status.PhaseHolder{}
	l, err := NewLogger(Config{Mode: "full", Branch: "test", NoColor: true}, testColors(), holder)
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

	holder := &status.PhaseHolder{}
	l, err := NewLogger(Config{Mode: "full", Branch: "test", NoColor: true}, testColors(), holder)
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

func TestLogger_PhaseColors(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	// enable colors for this test
	origNoColor := color.NoColor
	color.NoColor = false
	defer func() { color.NoColor = origNoColor }()

	holder := &status.PhaseHolder{}
	l, err := NewLogger(Config{Mode: "full", Branch: "test"}, testColors(), holder)
	require.NoError(t, err)
	defer func() { _ = l.Close() }()

	var buf bytes.Buffer
	l.stdout = &buf

	holder.Set(status.PhaseTask)
	l.Print("task output")

	holder.Set(status.PhaseReview)
	l.Print("review output")

	holder.Set(status.PhaseCodex)
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

	holder := &status.PhaseHolder{}
	l, err := NewLogger(Config{Mode: "full", Branch: "test", NoColor: true}, testColors(), holder)
	require.NoError(t, err)
	defer func() { _ = l.Close() }()

	var buf bytes.Buffer
	l.stdout = &buf

	holder.Set(status.PhaseTask)
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

	holder := &status.PhaseHolder{}
	l, err := NewLogger(Config{Mode: "full", Branch: "test"}, testColors(), holder)
	require.NoError(t, err)
	defer l.Close()

	tests := []struct {
		name     string
		offset   time.Duration
		expected string
	}{
		{"instant", 0, "0s"},
		{"seconds only", 45 * time.Second, "45s"},
		{"minutes and seconds", 5*time.Minute + 30*time.Second, "5m30s"},
		{"hours and minutes", 1*time.Hour + 23*time.Minute + 45*time.Second, "1h23m"},
		{"exact hour", 2 * time.Hour, "2h0m"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l.startTime = time.Now().Add(-tc.offset)
			assert.Equal(t, tc.expected, l.Elapsed())
		})
	}
}

func TestLogger_Close(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	holder := &status.PhaseHolder{}
	l, err := NewLogger(Config{Mode: "full", Branch: "test"}, testColors(), holder)
	require.NoError(t, err)

	l.Print("some output")
	err = l.Close()
	require.NoError(t, err)

	content, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	assert.Contains(t, string(content), "Completed:")
	assert.Contains(t, string(content), strings.Repeat("-", 60))
}

func TestLogger_LogDiffStats(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	l, err := NewLogger(Config{Mode: "full", Branch: "test"}, testColors(), &status.PhaseHolder{})
	require.NoError(t, err)
	defer func() { _ = l.Close() }()

	l.LogDiffStats(3, 4, 5)

	content, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	assert.Contains(t, string(content), "DIFFSTATS: files=3 additions=4 deletions=5")
}

func TestLogger_LogDiffStats_ZeroFiles(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	l, err := NewLogger(Config{Mode: "full", Branch: "test"}, testColors(), &status.PhaseHolder{})
	require.NoError(t, err)
	defer func() { _ = l.Close() }()

	l.LogDiffStats(0, 5, 6)

	content, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	assert.NotContains(t, string(content), "DIFFSTATS:")
}

func TestIsProgressCompleted(t *testing.T) {
	tmpDir := t.TempDir()

	sep := strings.Repeat("-", 60)
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{"file with footer", "some content\n" + sep + "\nCompleted: 2026-01-15 10:30:00 (5m30s)\n", true},
		{"file without footer", "some content\n--- restarted at 2026-01-15 10:30:00 ---\nmore content\n", false},
		{"empty file", "", false},
		{"footer in middle and end", "Completed: old\nmore\n" + sep + "\nCompleted: 2026-01-15 11:00:00 (2m)\n", true},
		{"completed without dash separator", "log line\nCompleted: now\n", false},
		{"completed in log output", "[ts] task said: Completed: the migration\nmore work\n", false},
		{"no completed substring", "some text without the keyword", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(tmpDir, tc.name+".txt")
			require.NoError(t, os.WriteFile(path, []byte(tc.content), 0o600))
			f, err := os.Open(path) //nolint:gosec // test file path from t.TempDir
			require.NoError(t, err)
			defer f.Close()
			fi, err := f.Stat()
			require.NoError(t, err)
			assert.Equal(t, tc.want, isProgressCompleted(f, fi.Size()))
		})
	}

	t.Run("zero size", func(t *testing.T) {
		assert.False(t, isProgressCompleted(nil, 0))
	})
}

func TestNewLogger_FreshStartAfterCompleted(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	colors := testColors()
	cfg := Config{PlanFile: "docs/plans/feature.md", Mode: "full", Branch: "main"}

	// create first logger, write content, close it (writes "Completed:" footer)
	holder := &status.PhaseHolder{}
	l1, err := NewLogger(cfg, colors, holder)
	require.NoError(t, err)
	l1.Print("first session output")
	progressPath := l1.Path()
	require.NoError(t, l1.Close())

	// verify first session has footer
	content1, err := os.ReadFile(progressPath) //nolint:gosec // test file path from logger
	require.NoError(t, err)
	assert.Contains(t, string(content1), "Completed:")
	assert.Contains(t, string(content1), "first session output")

	// create second logger with same config — should truncate and start fresh
	l2, err := NewLogger(cfg, colors, holder)
	require.NoError(t, err)
	assert.Equal(t, progressPath, l2.Path()) // verify both loggers use the same file
	l2.Print("second session output")
	require.NoError(t, l2.Close())

	content2, err := os.ReadFile(l2.Path())
	require.NoError(t, err)
	contentStr := string(content2)

	// old content gone
	assert.NotContains(t, contentStr, "first session output")

	// no restart separator
	assert.NotContains(t, contentStr, "--- restarted at")

	// fresh header written
	assert.Contains(t, contentStr, "# Ralphex Progress Log")
	assert.Equal(t, 1, strings.Count(contentStr, "# Ralphex Progress Log"))

	// new content present
	assert.Contains(t, contentStr, "second session output")
}

func TestGetProgressFilename(t *testing.T) {
	tests := []struct {
		name            string
		planFile        string
		planDescription string
		mode            string
		want            string
	}{
		{"full mode with plan", "docs/plans/feature.md", "", "full", filepath.Join(progressDir, "progress-feature.txt")},
		{"review mode with plan", "docs/plans/feature.md", "", "review", filepath.Join(progressDir, "progress-feature-review.txt")},
		{"codex-only mode with plan", "docs/plans/feature.md", "", "codex-only", filepath.Join(progressDir, "progress-feature-codex.txt")},
		{"full mode no plan", "", "", "full", filepath.Join(progressDir, "progress.txt")},
		{"review mode no plan", "", "", "review", filepath.Join(progressDir, "progress-review.txt")},
		{"codex-only mode no plan", "", "", "codex-only", filepath.Join(progressDir, "progress-codex.txt")},
		{"full with date prefix", "plans/2024-01-15-refactor.md", "", "full", filepath.Join(progressDir, "progress-2024-01-15-refactor.txt")},
		{"plan mode with description", "", "implement caching", "plan", filepath.Join(progressDir, "progress-plan-implement-caching.txt")},
		{"plan mode with complex description", "", "Add User Authentication!", "plan", filepath.Join(progressDir, "progress-plan-add-user-authentication.txt")},
		{"plan mode no description", "", "", "plan", filepath.Join(progressDir, "progress-plan.txt")},
		{"plan mode with special chars", "", "fix: bug #123", "plan", filepath.Join(progressDir, "progress-plan-fix-bug-123.txt")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := progressFilename(tc.planFile, tc.planDescription, tc.mode)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSanitizePlanName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"simple words", "implement caching", "implement-caching"},
		{"uppercase", "Add User Auth", "add-user-auth"},
		{"special chars", "fix: bug #123", "fix-bug-123"},
		{"multiple spaces", "add   feature", "add-feature"},
		{"leading trailing dashes", "--test--", "test"},
		{"only special chars", "!@#$%", "unnamed"},
		{"empty string", "", "unnamed"},
		{"long description", strings.Repeat("a", 60), strings.Repeat("a", 50)},
		{"long with spaces", "this is a very long plan description that exceeds the maximum length", "this-is-a-very-long-plan-description-that-exceeds"},
		{"numbers", "feature 123", "feature-123"},
		{"mixed", "API v2.0 endpoint", "api-v20-endpoint"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizePlanName(tc.input)
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

func TestNewColors(t *testing.T) {
	t.Run("creates colors from valid config", func(t *testing.T) {
		cfg := config.ColorConfig{
			Task:       "0,255,0",
			Review:     "0,255,255",
			Codex:      "255,0,255",
			ClaudeEval: "100,200,255",
			Warn:       "255,255,0",
			Error:      "255,0,0",
			Signal:     "255,100,100",
			Timestamp:  "138,138,138",
			Info:       "180,180,180",
		}
		colors := NewColors(cfg)
		assert.NotNil(t, colors)
		assert.NotNil(t, colors.Info())
		assert.NotNil(t, colors.Warn())
		assert.NotNil(t, colors.Error())
		assert.NotNil(t, colors.Signal())
		assert.NotNil(t, colors.Timestamp())
		assert.NotNil(t, colors.ForPhase(status.PhaseTask))
		assert.NotNil(t, colors.ForPhase(status.PhaseReview))
		assert.NotNil(t, colors.ForPhase(status.PhaseCodex))
		assert.NotNil(t, colors.ForPhase(status.PhaseClaudeEval))
	})

	t.Run("panics on invalid task color", func(t *testing.T) {
		cfg := config.ColorConfig{
			Task:       "invalid",
			Review:     "0,255,255",
			Codex:      "255,0,255",
			ClaudeEval: "100,200,255",
			Warn:       "255,255,0",
			Error:      "255,0,0",
			Signal:     "255,100,100",
			Timestamp:  "138,138,138",
			Info:       "180,180,180",
		}
		assert.Panics(t, func() { NewColors(cfg) })
	})

	t.Run("panics on empty color", func(t *testing.T) {
		cfg := config.ColorConfig{
			Task:       "",
			Review:     "0,255,255",
			Codex:      "255,0,255",
			ClaudeEval: "100,200,255",
			Warn:       "255,255,0",
			Error:      "255,0,0",
			Signal:     "255,100,100",
			Timestamp:  "138,138,138",
			Info:       "180,180,180",
		}
		assert.Panics(t, func() { NewColors(cfg) })
	})
}

func TestColors_Methods(t *testing.T) {
	colors := testColors()

	t.Run("Info returns info color", func(t *testing.T) {
		c := colors.Info()
		assert.NotNil(t, c)
	})

	t.Run("Warn returns warn color", func(t *testing.T) {
		c := colors.Warn()
		assert.NotNil(t, c)
	})

	t.Run("Error returns error color", func(t *testing.T) {
		c := colors.Error()
		assert.NotNil(t, c)
	})

	t.Run("Signal returns signal color", func(t *testing.T) {
		c := colors.Signal()
		assert.NotNil(t, c)
	})

	t.Run("Timestamp returns timestamp color", func(t *testing.T) {
		c := colors.Timestamp()
		assert.NotNil(t, c)
	})

	t.Run("ForPhase returns phase colors", func(t *testing.T) {
		assert.NotNil(t, colors.ForPhase(status.PhaseTask))
		assert.NotNil(t, colors.ForPhase(status.PhaseReview))
		assert.NotNil(t, colors.ForPhase(status.PhaseCodex))
		assert.NotNil(t, colors.ForPhase(status.PhaseClaudeEval))
	})
}

func TestParseColorOrPanic(t *testing.T) {
	t.Run("valid colors", func(t *testing.T) {
		tests := []struct {
			name string
			s    string
		}{
			{name: "red", s: "255,0,0"},
			{name: "black", s: "0,0,0"},
			{name: "white", s: "255,255,255"},
			{name: "with spaces", s: " 100 , 150 , 200 "},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				assert.NotPanics(t, func() {
					c := parseColorOrPanic(tc.s, "test")
					assert.NotNil(t, c)
				})
			})
		}
	})

	t.Run("invalid colors panic", func(t *testing.T) {
		tests := []struct {
			name string
			s    string
		}{
			{name: "empty string", s: ""},
			{name: "too few parts", s: "255,0"},
			{name: "too many parts", s: "255,0,0,0"},
			{name: "invalid r component", s: "abc,0,0"},
			{name: "invalid g component", s: "0,abc,0"},
			{name: "invalid b component", s: "0,0,abc"},
			{name: "r out of range high", s: "256,0,0"},
			{name: "g out of range high", s: "0,256,0"},
			{name: "b out of range high", s: "0,0,256"},
			{name: "r out of range negative", s: "-1,0,0"},
			{name: "g out of range negative", s: "0,-1,0"},
			{name: "b out of range negative", s: "0,0,-1"},
			{name: "single value", s: "255"},
			{name: "no delimiter", s: "255000"},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				assert.Panics(t, func() {
					parseColorOrPanic(tc.s, "test")
				})
			})
		}
	})
}

func TestLogger_LogQuestion(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	holder := &status.PhaseHolder{}
	l, err := NewLogger(Config{Mode: "plan", PlanDescription: "test", Branch: "main", NoColor: true}, testColors(), holder)
	require.NoError(t, err)
	defer func() { _ = l.Close() }()

	var buf bytes.Buffer
	l.stdout = &buf

	l.LogQuestion("Which cache backend?", []string{"Redis", "In-memory", "File-based"})

	// check file output
	content, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	contentStr := string(content)
	assert.Contains(t, contentStr, "QUESTION: Which cache backend?")
	assert.Contains(t, contentStr, "OPTIONS: Redis, In-memory, File-based")

	// check stdout output
	output := buf.String()
	assert.Contains(t, output, "QUESTION: Which cache backend?")
	assert.Contains(t, output, "OPTIONS: Redis, In-memory, File-based")
}

func TestLogger_LogAnswer(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	holder := &status.PhaseHolder{}
	l, err := NewLogger(Config{Mode: "plan", PlanDescription: "test", Branch: "main", NoColor: true}, testColors(), holder)
	require.NoError(t, err)
	defer func() { _ = l.Close() }()

	var buf bytes.Buffer
	l.stdout = &buf

	l.LogAnswer("Redis")

	// check file output
	content, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	assert.Contains(t, string(content), "ANSWER: Redis")

	// check stdout output
	assert.Contains(t, buf.String(), "ANSWER: Redis")
}

func TestLogger_LogDraftReview_Accept(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	holder := &status.PhaseHolder{}
	l, err := NewLogger(Config{Mode: "plan", PlanDescription: "test", Branch: "main", NoColor: true}, testColors(), holder)
	require.NoError(t, err)
	defer func() { _ = l.Close() }()

	var buf bytes.Buffer
	l.stdout = &buf

	l.LogDraftReview("accept", "")

	// check file output
	content, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	contentStr := string(content)
	assert.Contains(t, contentStr, "DRAFT REVIEW: accept")
	assert.NotContains(t, contentStr, "FEEDBACK:")

	// check stdout output
	output := buf.String()
	assert.Contains(t, output, "DRAFT REVIEW: accept")
	assert.NotContains(t, output, "FEEDBACK:")
}

func TestLogger_LogDraftReview_ReviseWithFeedback(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	holder := &status.PhaseHolder{}
	l, err := NewLogger(Config{Mode: "plan", PlanDescription: "test", Branch: "main", NoColor: true}, testColors(), holder)
	require.NoError(t, err)
	defer func() { _ = l.Close() }()

	var buf bytes.Buffer
	l.stdout = &buf

	l.LogDraftReview("revise", "Please add more details to Task 3")

	// check file output
	content, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	contentStr := string(content)
	assert.Contains(t, contentStr, "DRAFT REVIEW: revise")
	assert.Contains(t, contentStr, "FEEDBACK: Please add more details to Task 3")

	// check stdout output
	output := buf.String()
	assert.Contains(t, output, "DRAFT REVIEW: revise")
	assert.Contains(t, output, "FEEDBACK: Please add more details to Task 3")
}

func TestLogger_PlanModeFilename(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	tests := []struct {
		name        string
		cfg         Config
		wantBase    string
		wantContent string
	}{
		{
			name:        "plan mode with description",
			cfg:         Config{Mode: "plan", PlanDescription: "implement caching", Branch: "main"},
			wantBase:    "progress-plan-implement-caching.txt",
			wantContent: "Mode: plan",
		},
		{
			name:        "plan mode without description",
			cfg:         Config{Mode: "plan", Branch: "main"},
			wantBase:    "progress-plan.txt",
			wantContent: "Mode: plan",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			holder := &status.PhaseHolder{}
			l, err := NewLogger(tc.cfg, testColors(), holder)
			require.NoError(t, err)
			defer l.Close()

			assert.Equal(t, tc.wantBase, filepath.Base(l.Path()))
			assert.Contains(t, l.Path(), ".ralphex/progress")

			content, err := os.ReadFile(l.Path())
			require.NoError(t, err)
			assert.Contains(t, string(content), tc.wantContent)
		})
	}
}
