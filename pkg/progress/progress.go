// Package progress provides timestamped logging to file and stdout with color support.
package progress

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/fatih/color"
)

// Phase represents execution phase for color coding.
type Phase string

// Phase constants for execution stages.
const (
	// PhaseTask is the task execution phase (green).
	PhaseTask Phase = "task"
	// PhaseReview is the code review phase (cyan).
	PhaseReview Phase = "review"
	// PhaseCodex is the codex analysis phase (magenta).
	PhaseCodex Phase = "codex"
)

// phase colors using fatih/color.
var (
	taskColor      = color.New(color.FgGreen)
	reviewColor    = color.New(color.FgCyan)
	codexColor     = color.New(color.FgMagenta)
	warnColor      = color.New(color.FgYellow)
	errorColor     = color.New(color.FgRed)
	timestampColor = color.New(color.FgHiBlack)
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

	progressPath := getProgressFilename(cfg.PlanFile, cfg.Mode)

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

// Print writes a timestamped message to both file and stdout.
func (l *Logger) Print(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	timestamp := time.Now().Format("15:04:05")

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

	timestamp := time.Now().Format("15:04:05")
	phaseColor := phaseColors[l.phase]
	tsPrefix := timestampColor.Sprintf("[%s]", timestamp)
	indent := "          " // 10 chars to align with "[HH:MM:SS] "

	// split into lines and print each
	lines := strings.Split(text, "\n")
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
	timestamp := time.Now().Format("15:04:05")

	l.writeFile("[%s] ERROR: %s\n", timestamp, msg)

	tsStr := timestampColor.Sprintf("[%s]", timestamp)
	errStr := errorColor.Sprintf("ERROR: %s", msg)
	l.writeStdout("%s %s\n", tsStr, errStr)
}

// Warn writes a warning message in yellow.
func (l *Logger) Warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	timestamp := time.Now().Format("15:04:05")

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
func getProgressFilename(planFile, mode string) string {
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
