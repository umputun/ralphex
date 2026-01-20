// Package progress provides timestamped logging to file and stdout with color support.
package progress

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/fatih/color"
	"golang.org/x/term"
)

// Phase represents execution phase for color coding.
type Phase string

// Phase constants for execution stages.
const (
	PhaseTask   Phase = "task"   // execution phase (green)
	PhaseReview Phase = "review" // code review phase (cyan)
	PhaseCodex  Phase = "codex"  // codex analysis phase (magenta)
)

// phase colors using fatih/color.
var (
	taskColor      = color.New(color.FgGreen)
	reviewColor    = color.New(color.FgCyan)
	codexColor     = color.New(color.FgMagenta)
	warnColor      = color.New(color.FgYellow)
	errorColor     = color.New(color.FgRed)
	timestampColor = color.New(color.FgWhite)
)

// phaseColors maps phases to their color functions.
var phaseColors = map[Phase]*color.Color{
	PhaseTask:   taskColor,
	PhaseReview: reviewColor,
	PhaseCodex:  codexColor,
}

// Logger writes timestamped output to both file and stdout.
type Logger struct {
	file      *os.File
	stdout    io.Writer
	startTime time.Time
	phase     Phase
}

// Config holds logger configuration.
type Config struct {
	PlanFile string // plan filename (used to derive progress filename)
	Mode     string // execution mode: full, review, codex-only
	Branch   string // current git branch
	NoColor  bool   // disable color output (sets color.NoColor globally)
}

// NewLogger creates a logger writing to both a progress file and stdout.
func NewLogger(cfg Config) (*Logger, error) {
	// set global color setting
	if cfg.NoColor {
		color.NoColor = true
	}

	progressPath := progressFilename(cfg.PlanFile, cfg.Mode)

	// ensure progress files are tracked by creating parent dir
	if dir := filepath.Dir(progressPath); dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("create progress dir: %w", err)
		}
	}

	f, err := os.Create(progressPath) //nolint:gosec // path derived from plan filename
	if err != nil {
		return nil, fmt.Errorf("create progress file: %w", err)
	}

	l := &Logger{
		file:      f,
		stdout:    os.Stdout,
		startTime: time.Now(),
		phase:     PhaseTask,
	}

	// write header
	planStr := cfg.PlanFile
	if planStr == "" {
		planStr = "(no plan - review only)"
	}
	l.writeFile("# Ralphex Progress Log\n")
	l.writeFile("Plan: %s\n", planStr)
	l.writeFile("Branch: %s\n", cfg.Branch)
	l.writeFile("Mode: %s\n", cfg.Mode)
	l.writeFile("Started: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	l.writeFile("%s\n\n", strings.Repeat("-", 60))

	return l, nil
}

// Path returns the progress file path.
func (l *Logger) Path() string {
	if l.file == nil {
		return ""
	}
	return l.file.Name()
}

// SetPhase sets the current execution phase for color coding.
func (l *Logger) SetPhase(phase Phase) {
	l.phase = phase
}

// timestampFormat is the format for timestamps: YY-MM-DD HH:MM:SS
const timestampFormat = "06-01-02 15:04:05"

// Print writes a timestamped message to both file and stdout.
func (l *Logger) Print(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	timestamp := time.Now().Format(timestampFormat)

	// write to file without color
	l.writeFile("[%s] %s\n", timestamp, msg)

	// write to stdout with color
	phaseColor := phaseColors[l.phase]
	tsStr := timestampColor.Sprintf("[%s]", timestamp)
	msgStr := phaseColor.Sprint(msg)
	l.writeStdout("%s %s\n", tsStr, msgStr)
}

// PrintRaw writes without timestamp (for streaming output).
func (l *Logger) PrintRaw(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	l.writeFile("%s", msg)
	l.writeStdout("%s", msg)
}

// getTerminalWidth returns terminal width, using COLUMNS env var or syscall.
// Defaults to 80 if detection fails. Returns content width (total - 20 for timestamp).
func getTerminalWidth() int {
	const minWidth = 40

	// try COLUMNS env var first
	if cols := os.Getenv("COLUMNS"); cols != "" {
		if w, err := strconv.Atoi(cols); err == nil && w > 0 {
			contentWidth := w - 20 // leave room for timestamp prefix
			if contentWidth < minWidth {
				return minWidth
			}
			return contentWidth
		}
	}

	// try terminal syscall
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		contentWidth := w - 20
		if contentWidth < minWidth {
			return minWidth
		}
		return contentWidth
	}

	return 80 - 20 // default 80 columns minus timestamp
}

// wrapText wraps text to specified width, breaking on word boundaries.
func wrapText(text string, width int) string {
	if width <= 0 || len(text) <= width {
		return text
	}

	var result strings.Builder
	words := strings.Fields(text)
	lineLen := 0

	for i, word := range words {
		wordLen := len(word)

		if i == 0 {
			result.WriteString(word)
			lineLen = wordLen
			continue
		}

		// check if word fits on current line
		if lineLen+1+wordLen <= width {
			result.WriteString(" ")
			result.WriteString(word)
			lineLen += 1 + wordLen
		} else {
			// start new line
			result.WriteString("\n")
			result.WriteString(word)
			lineLen = wordLen
		}
	}

	return result.String()
}

// PrintAligned writes text with timestamp, handling multi-line content properly.
// Like ralph.py's print_aligned - timestamps the first line, indents continuation lines.
func (l *Logger) PrintAligned(text string) {
	if text == "" {
		return
	}

	// trim trailing newlines to avoid extra blank lines
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return
	}

	timestamp := time.Now().Format(timestampFormat)
	phaseColor := phaseColors[l.phase]
	tsPrefix := timestampColor.Sprintf("[%s]", timestamp)
	indent := "                    " // 20 chars to align with "[YY-MM-DD HH:MM:SS] "

	// wrap text to terminal width
	width := getTerminalWidth()

	// split into lines, wrap each long line, then process
	var lines []string
	for line := range strings.SplitSeq(text, "\n") {
		if len(line) > width {
			wrapped := wrapText(line, width)
			for wrappedLine := range strings.SplitSeq(wrapped, "\n") {
				lines = append(lines, wrappedLine)
			}
		} else {
			lines = append(lines, line)
		}
	}
	for i, line := range lines {
		if line == "" {
			// preserve empty lines within content
			l.writeFile("\n")
			l.writeStdout("\n")
			continue
		}

		if i == 0 {
			// first line gets timestamp
			l.writeFile("[%s] %s\n", timestamp, line)
			l.writeStdout("%s %s\n", tsPrefix, phaseColor.Sprint(line))
		} else {
			// continuation lines get indent
			l.writeFile("%s%s\n", indent, line)
			l.writeStdout("%s%s\n", indent, phaseColor.Sprint(line))
		}
	}
}

// Error writes an error message in red.
func (l *Logger) Error(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	timestamp := time.Now().Format(timestampFormat)

	l.writeFile("[%s] ERROR: %s\n", timestamp, msg)

	tsStr := timestampColor.Sprintf("[%s]", timestamp)
	errStr := errorColor.Sprintf("ERROR: %s", msg)
	l.writeStdout("%s %s\n", tsStr, errStr)
}

// Warn writes a warning message in yellow.
func (l *Logger) Warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	timestamp := time.Now().Format(timestampFormat)

	l.writeFile("[%s] WARN: %s\n", timestamp, msg)

	tsStr := timestampColor.Sprintf("[%s]", timestamp)
	warnStr := warnColor.Sprintf("WARN: %s", msg)
	l.writeStdout("%s %s\n", tsStr, warnStr)
}

// Elapsed returns formatted elapsed time since start.
func (l *Logger) Elapsed() string {
	return humanize.RelTime(l.startTime, time.Now(), "", "")
}

// Close writes footer and closes the progress file.
func (l *Logger) Close() error {
	if l.file == nil {
		return nil
	}

	l.writeFile("\n%s\n", strings.Repeat("-", 60))
	l.writeFile("Completed: %s (%s)\n", time.Now().Format("2006-01-02 15:04:05"), l.Elapsed())

	if err := l.file.Close(); err != nil {
		return fmt.Errorf("close progress file: %w", err)
	}
	return nil
}

func (l *Logger) writeFile(format string, args ...any) {
	if l.file != nil {
		fmt.Fprintf(l.file, format, args...)
	}
}

func (l *Logger) writeStdout(format string, args ...any) {
	fmt.Fprintf(l.stdout, format, args...)
}

// getProgressFilename returns progress file path based on plan and mode.
func progressFilename(planFile, mode string) string {
	if planFile != "" {
		stem := strings.TrimSuffix(filepath.Base(planFile), ".md")
		switch mode {
		case "codex-only":
			return fmt.Sprintf("progress-%s-codex.txt", stem)
		case "review":
			return fmt.Sprintf("progress-%s-review.txt", stem)
		default:
			return fmt.Sprintf("progress-%s.txt", stem)
		}
	}

	switch mode {
	case "codex-only":
		return "progress-codex.txt"
	case "review":
		return "progress-review.txt"
	default:
		return "progress.txt"
	}
}
