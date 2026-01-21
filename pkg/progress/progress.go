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
	PhaseTask       Phase = "task"        // execution phase (green)
	PhaseReview     Phase = "review"      // code review phase (cyan)
	PhaseCodex      Phase = "codex"       // codex analysis phase (magenta)
	PhaseClaudeEval Phase = "claude-eval" // claude evaluating codex (bright cyan)
)

// ColorConfig holds RGB values for output colors.
// each field stores comma-separated RGB values (e.g., "255,0,0" for red).
type ColorConfig struct {
	Task       string // task execution phase
	Review     string // review phase
	Codex      string // codex external review
	ClaudeEval string // claude evaluation of codex output
	Warn       string // warning messages
	Error      string // error messages
	Signal     string // completion/failure signals
	Timestamp  string // timestamp prefix
	Info       string // informational messages (used in main.go)
}

// default RGB values for colors (used when not configured).
var (
	defaultTaskRGB       = [3]int{0, 255, 0}     // green
	defaultReviewRGB     = [3]int{0, 255, 255}   // cyan
	defaultCodexRGB      = [3]int{255, 0, 255}   // magenta
	defaultClaudeEvalRGB = [3]int{100, 200, 255} // bright cyan/light blue
	defaultWarnRGB       = [3]int{255, 255, 0}   // yellow
	defaultErrorRGB      = [3]int{255, 0, 0}     // red
	defaultSignalRGB     = [3]int{255, 100, 100} // bright red
	defaultTimestampRGB  = [3]int{138, 138, 138} // medium grey
	defaultInfoRGB       = [3]int{180, 180, 180} // light grey
)

// phase colors using fatih/color.
// package-level color variables, initialized by InitColors().
var (
	taskColor       = color.RGB(defaultTaskRGB[0], defaultTaskRGB[1], defaultTaskRGB[2])
	reviewColor     = color.RGB(defaultReviewRGB[0], defaultReviewRGB[1], defaultReviewRGB[2])
	codexColor      = color.RGB(defaultCodexRGB[0], defaultCodexRGB[1], defaultCodexRGB[2])
	claudeEvalColor = color.RGB(defaultClaudeEvalRGB[0], defaultClaudeEvalRGB[1], defaultClaudeEvalRGB[2])
	warnColor       = color.RGB(defaultWarnRGB[0], defaultWarnRGB[1], defaultWarnRGB[2])
	errorColor      = color.RGB(defaultErrorRGB[0], defaultErrorRGB[1], defaultErrorRGB[2])
	signalColor     = color.RGB(defaultSignalRGB[0], defaultSignalRGB[1], defaultSignalRGB[2])
	timestampColor  = color.RGB(defaultTimestampRGB[0], defaultTimestampRGB[1], defaultTimestampRGB[2])
	infoColor       = color.RGB(defaultInfoRGB[0], defaultInfoRGB[1], defaultInfoRGB[2])
)

// phaseColors maps phases to their color functions.
// note: map is initialized empty and populated by initPhaseColors() called from init().
var phaseColors = map[Phase]*color.Color{}

//nolint:gochecknoinits // required to populate phaseColors after color vars are initialized
func init() {
	initPhaseColors()
}

// initPhaseColors populates the phaseColors map with current color values.
func initPhaseColors() {
	phaseColors[PhaseTask] = taskColor
	phaseColors[PhaseReview] = reviewColor
	phaseColors[PhaseCodex] = codexColor
	phaseColors[PhaseClaudeEval] = claudeEvalColor
}

// InitColors resets all colors to their default values.
// useful for testing or when configuration needs to be reset.
func InitColors() {
	taskColor = color.RGB(defaultTaskRGB[0], defaultTaskRGB[1], defaultTaskRGB[2])
	reviewColor = color.RGB(defaultReviewRGB[0], defaultReviewRGB[1], defaultReviewRGB[2])
	codexColor = color.RGB(defaultCodexRGB[0], defaultCodexRGB[1], defaultCodexRGB[2])
	claudeEvalColor = color.RGB(defaultClaudeEvalRGB[0], defaultClaudeEvalRGB[1], defaultClaudeEvalRGB[2])
	warnColor = color.RGB(defaultWarnRGB[0], defaultWarnRGB[1], defaultWarnRGB[2])
	errorColor = color.RGB(defaultErrorRGB[0], defaultErrorRGB[1], defaultErrorRGB[2])
	signalColor = color.RGB(defaultSignalRGB[0], defaultSignalRGB[1], defaultSignalRGB[2])
	timestampColor = color.RGB(defaultTimestampRGB[0], defaultTimestampRGB[1], defaultTimestampRGB[2])
	infoColor = color.RGB(defaultInfoRGB[0], defaultInfoRGB[1], defaultInfoRGB[2])

	initPhaseColors()
}

// SetColors applies color configuration from ColorConfig.
// each color value is expected to be comma-separated RGB values (e.g., "255,0,0").
// empty values are skipped, keeping the current/default color.
func SetColors(cfg ColorConfig) {
	if rgb := parseRGB(cfg.Task); rgb != nil {
		taskColor = color.RGB(rgb[0], rgb[1], rgb[2])
		phaseColors[PhaseTask] = taskColor
	}
	if rgb := parseRGB(cfg.Review); rgb != nil {
		reviewColor = color.RGB(rgb[0], rgb[1], rgb[2])
		phaseColors[PhaseReview] = reviewColor
	}
	if rgb := parseRGB(cfg.Codex); rgb != nil {
		codexColor = color.RGB(rgb[0], rgb[1], rgb[2])
		phaseColors[PhaseCodex] = codexColor
	}
	if rgb := parseRGB(cfg.ClaudeEval); rgb != nil {
		claudeEvalColor = color.RGB(rgb[0], rgb[1], rgb[2])
		phaseColors[PhaseClaudeEval] = claudeEvalColor
	}
	if rgb := parseRGB(cfg.Warn); rgb != nil {
		warnColor = color.RGB(rgb[0], rgb[1], rgb[2])
	}
	if rgb := parseRGB(cfg.Error); rgb != nil {
		errorColor = color.RGB(rgb[0], rgb[1], rgb[2])
	}
	if rgb := parseRGB(cfg.Signal); rgb != nil {
		signalColor = color.RGB(rgb[0], rgb[1], rgb[2])
	}
	if rgb := parseRGB(cfg.Timestamp); rgb != nil {
		timestampColor = color.RGB(rgb[0], rgb[1], rgb[2])
	}
	if rgb := parseRGB(cfg.Info); rgb != nil {
		infoColor = color.RGB(rgb[0], rgb[1], rgb[2])
	}
}

// InfoColor returns the configured info color for use in main.go.
// this provides access to the info color set via SetColors().
func InfoColor() *color.Color {
	return infoColor
}

// parseRGB parses comma-separated RGB values (e.g., "255,0,0") into [r, g, b].
// returns nil if the string is empty or invalid.
func parseRGB(s string) []int {
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

// PrintSection writes a section header without timestamp in yellow.
// format: "\n--- {name} ---\n"
func (l *Logger) PrintSection(name string) {
	header := fmt.Sprintf("\n--- %s ---\n", name)
	l.writeFile("%s", header)
	l.writeStdout("%s", warnColor.Sprint(header))
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

	phaseColor := phaseColors[l.phase]

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
		tsPrefix := timestampColor.Sprintf("[%s]", timestamp)
		l.writeFile("[%s] %s\n", timestamp, displayLine)

		// use red for signal lines
		lineColor := phaseColor

		// format signal lines nicely
		if sig := extractSignal(line); sig != "" {
			displayLine = sig
			lineColor = signalColor
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
