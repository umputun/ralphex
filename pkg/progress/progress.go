// Package progress provides timestamped logging to file and stdout with color support.
package progress

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/fatih/color"
	"golang.org/x/term"

	"github.com/umputun/ralphex/pkg/config"
	"github.com/umputun/ralphex/pkg/processor"
)

// Phase is an alias to processor.Phase for backwards compatibility.
//
// Deprecated: use processor.Phase directly.
type Phase = processor.Phase

// Phase constants for execution stages - aliases to processor constants.
const (
	PhaseTask       = processor.PhaseTask
	PhaseReview     = processor.PhaseReview
	PhaseCodex      = processor.PhaseCodex
	PhaseClaudeEval = processor.PhaseClaudeEval
	PhasePlan       = processor.PhasePlan
)

// Colors holds all color configuration for output formatting.
// use NewColors to create from config.ColorConfig.
type Colors struct {
	task       *color.Color
	review     *color.Color
	codex      *color.Color
	claudeEval *color.Color
	warn       *color.Color
	err        *color.Color
	signal     *color.Color
	timestamp  *color.Color
	info       *color.Color
	phases     map[Phase]*color.Color
}

// NewColors creates Colors from config.ColorConfig.
// all colors must be provided - use config with embedded defaults fallback.
// panics if any color value is invalid (configuration error).
func NewColors(cfg config.ColorConfig) *Colors {
	c := &Colors{phases: make(map[Phase]*color.Color)}
	c.task = parseColorOrPanic(cfg.Task, "task")
	c.review = parseColorOrPanic(cfg.Review, "review")
	c.codex = parseColorOrPanic(cfg.Codex, "codex")
	c.claudeEval = parseColorOrPanic(cfg.ClaudeEval, "claude_eval")
	c.warn = parseColorOrPanic(cfg.Warn, "warn")
	c.err = parseColorOrPanic(cfg.Error, "error")
	c.signal = parseColorOrPanic(cfg.Signal, "signal")
	c.timestamp = parseColorOrPanic(cfg.Timestamp, "timestamp")
	c.info = parseColorOrPanic(cfg.Info, "info")

	c.phases[PhaseTask] = c.task
	c.phases[PhaseReview] = c.review
	c.phases[PhaseCodex] = c.codex
	c.phases[PhaseClaudeEval] = c.claudeEval
	c.phases[PhasePlan] = c.task // plan phase uses task color (green)

	return c
}

// parseColorOrPanic parses RGB string and returns color, panics on invalid input.
func parseColorOrPanic(s, name string) *color.Color {
	parseRGB := func(s string) []int {
		if s == "" {
			return nil
		}
		parts := strings.Split(s, ",")
		if len(parts) != 3 {
			return nil
		}

		// parse each component
		r, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil || r < 0 || r > 255 {
			return nil
		}
		g, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil || g < 0 || g > 255 {
			return nil
		}
		b, err := strconv.Atoi(strings.TrimSpace(parts[2]))
		if err != nil || b < 0 || b > 255 {
			return nil
		}
		return []int{r, g, b}
	}

	rgb := parseRGB(s)
	if rgb == nil {
		panic(fmt.Sprintf("invalid color_%s value: %q", name, s))
	}
	return color.RGB(rgb[0], rgb[1], rgb[2])
}

// Info returns the info color for informational messages.
func (c *Colors) Info() *color.Color { return c.info }

// ForPhase returns the color for the given execution phase.
func (c *Colors) ForPhase(p Phase) *color.Color { return c.phases[p] }

// Timestamp returns the timestamp color.
func (c *Colors) Timestamp() *color.Color { return c.timestamp }

// Warn returns the warning color.
func (c *Colors) Warn() *color.Color { return c.warn }

// Error returns the error color.
func (c *Colors) Error() *color.Color { return c.err }

// Signal returns the signal color.
func (c *Colors) Signal() *color.Color { return c.signal }

// Logger writes timestamped output to both file and stdout.
type Logger struct {
	file      *os.File
	stdout    io.Writer
	startTime time.Time
	phase     Phase
	colors    *Colors
}

// Config holds logger configuration.
type Config struct {
	PlanFile        string // plan filename (used to derive progress filename)
	PlanDescription string // plan description for plan mode (used for filename)
	Mode            string // execution mode: full, review, codex-only, plan
	Branch          string // current git branch
	NoColor         bool   // disable color output (sets color.NoColor globally)
}

// NewLogger creates a logger writing to both a progress file and stdout.
// colors must be provided (created via NewColors from config).
func NewLogger(cfg Config, colors *Colors) (*Logger, error) {
	// set global color setting
	if cfg.NoColor {
		color.NoColor = true
	}

	progressPath := progressFilename(cfg.PlanFile, cfg.PlanDescription, cfg.Mode)

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

	// acquire exclusive lock on progress file to signal active session
	// the lock is held for the duration of execution and released on Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("acquire file lock: %w", err)
	}
	registerActiveLock(f.Name())

	l := &Logger{
		file:      f,
		stdout:    os.Stdout,
		startTime: time.Now(),
		phase:     PhaseTask,
		colors:    colors,
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
	phaseColor := l.colors.ForPhase(l.phase)
	tsStr := l.colors.Timestamp().Sprintf("[%s]", timestamp)
	msgStr := phaseColor.Sprint(msg)
	l.writeStdout("%s %s\n", tsStr, msgStr)
}

// PrintRaw writes without timestamp (for streaming output).
func (l *Logger) PrintRaw(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	l.writeFile("%s", msg)
	l.writeStdout("%s", msg)
}

// PrintSection writes a section header without timestamp in yellow.
// format: "\n--- {label} ---\n"
func (l *Logger) PrintSection(section processor.Section) {
	header := fmt.Sprintf("\n--- %s ---\n", section.Label)
	l.writeFile("%s", header)
	l.writeStdout("%s", l.colors.Warn().Sprint(header))
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

// PrintAligned writes text with timestamp on each line, suppressing empty lines.
func (l *Logger) PrintAligned(text string) {
	if text == "" {
		return
	}

	// trim trailing newlines to avoid extra blank lines
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return
	}

	phaseColor := l.colors.ForPhase(l.phase)

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

	for _, line := range lines {
		if line == "" {
			continue // skip empty lines
		}

		// add indent for list items
		displayLine := formatListItem(line)

		// timestamp each line
		timestamp := time.Now().Format(timestampFormat)
		tsPrefix := l.colors.Timestamp().Sprintf("[%s]", timestamp)
		l.writeFile("[%s] %s\n", timestamp, displayLine)

		// use red for signal lines
		lineColor := phaseColor

		// format signal lines nicely
		if sig := extractSignal(line); sig != "" {
			displayLine = sig
			lineColor = l.colors.Signal()
		}

		l.writeStdout("%s %s\n", tsPrefix, lineColor.Sprint(displayLine))
	}
}

// extractSignal extracts signal name from <<<RALPHEX:SIGNAL_NAME>>> format.
// returns empty string if no signal found.
func extractSignal(line string) string {
	const prefix = "<<<RALPHEX:"
	const suffix = ">>>"

	start := strings.Index(line, prefix)
	if start == -1 {
		return ""
	}

	end := strings.Index(line[start:], suffix)
	if end == -1 {
		return ""
	}

	return line[start+len(prefix) : start+end]
}

// formatListItem adds 2-space indent for list items (numbered or bulleted).
// detects patterns like "1. ", "12. ", "- ", "* " at line start.
func formatListItem(line string) string {
	trimmed := strings.TrimLeft(line, " \t")
	if trimmed == line { // no leading whitespace
		if isListItem(trimmed) {
			return "  " + line
		}
	}
	return line
}

// isListItem returns true if line starts with a list marker.
func isListItem(line string) bool {
	// check for "- " or "* " (bullet lists)
	if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
		return true
	}
	// check for numbered lists like "1. ", "12. ", "123. "
	for i, r := range line {
		if r >= '0' && r <= '9' {
			continue
		}
		if r == '.' && i > 0 && i < len(line)-1 && line[i+1] == ' ' {
			return true
		}
		break
	}
	return false
}

// Error writes an error message in red.
func (l *Logger) Error(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	timestamp := time.Now().Format(timestampFormat)

	l.writeFile("[%s] ERROR: %s\n", timestamp, msg)

	tsStr := l.colors.Timestamp().Sprintf("[%s]", timestamp)
	errStr := l.colors.Error().Sprintf("ERROR: %s", msg)
	l.writeStdout("%s %s\n", tsStr, errStr)
}

// Warn writes a warning message in yellow.
func (l *Logger) Warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	timestamp := time.Now().Format(timestampFormat)

	l.writeFile("[%s] WARN: %s\n", timestamp, msg)

	tsStr := l.colors.Timestamp().Sprintf("[%s]", timestamp)
	warnStr := l.colors.Warn().Sprintf("WARN: %s", msg)
	l.writeStdout("%s %s\n", tsStr, warnStr)
}

// LogQuestion logs a question and its options for plan creation mode.
// format: QUESTION: <question>\n OPTIONS: <opt1>, <opt2>, ...
func (l *Logger) LogQuestion(question string, options []string) {
	timestamp := time.Now().Format(timestampFormat)

	l.writeFile("[%s] QUESTION: %s\n", timestamp, question)
	l.writeFile("[%s] OPTIONS: %s\n", timestamp, strings.Join(options, ", "))

	tsStr := l.colors.Timestamp().Sprintf("[%s]", timestamp)
	questionStr := l.colors.Info().Sprintf("QUESTION: %s", question)
	optionsStr := l.colors.Info().Sprintf("OPTIONS: %s", strings.Join(options, ", "))
	l.writeStdout("%s %s\n", tsStr, questionStr)
	l.writeStdout("%s %s\n", tsStr, optionsStr)
}

// LogAnswer logs the user's answer for plan creation mode.
// format: ANSWER: <answer>
func (l *Logger) LogAnswer(answer string) {
	timestamp := time.Now().Format(timestampFormat)

	l.writeFile("[%s] ANSWER: %s\n", timestamp, answer)

	tsStr := l.colors.Timestamp().Sprintf("[%s]", timestamp)
	answerStr := l.colors.Info().Sprintf("ANSWER: %s", answer)
	l.writeStdout("%s %s\n", tsStr, answerStr)
}

// Elapsed returns formatted elapsed time since start.
func (l *Logger) Elapsed() string {
	return humanize.RelTime(l.startTime, time.Now(), "", "")
}

// Close writes footer, releases the file lock, and closes the progress file.
func (l *Logger) Close() error {
	if l.file == nil {
		return nil
	}

	l.writeFile("\n%s\n", strings.Repeat("-", 60))
	l.writeFile("Completed: %s (%s)\n", time.Now().Format("2006-01-02 15:04:05"), l.Elapsed())

	// release file lock before closing
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	unregisterActiveLock(l.file.Name())

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
func progressFilename(planFile, planDescription, mode string) string {
	// plan mode uses sanitized plan description
	if mode == "plan" && planDescription != "" {
		sanitized := sanitizePlanName(planDescription)
		return fmt.Sprintf("progress-plan-%s.txt", sanitized)
	}

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
	case "plan":
		return "progress-plan.txt"
	default:
		return "progress.txt"
	}
}

// sanitizePlanName converts plan description to a safe filename component.
// replaces spaces with dashes, removes special characters, and limits length.
func sanitizePlanName(desc string) string {
	// lowercase and replace spaces with dashes
	result := strings.ToLower(desc)
	result = strings.ReplaceAll(result, " ", "-")

	// keep only alphanumeric and dashes
	var clean strings.Builder
	for _, r := range result {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			clean.WriteRune(r)
		}
	}
	result = clean.String()

	// collapse multiple dashes
	for strings.Contains(result, "--") {
		result = strings.ReplaceAll(result, "--", "-")
	}

	// trim leading/trailing dashes
	result = strings.Trim(result, "-")

	// limit length to 50 characters
	if len(result) > 50 {
		result = result[:50]
		// don't end with a dash
		result = strings.TrimRight(result, "-")
	}

	if result == "" {
		return "unnamed"
	}
	return result
}
