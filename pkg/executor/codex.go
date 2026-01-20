package executor

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

//go:generate moq -out mocks/codex_command_runner.go -pkg mocks -skip-ensure -fmt goimports . CodexCommandRunner

// CodexCommandRunner abstracts command execution for codex testing.
type CodexCommandRunner interface {
	Run(ctx context.Context, name string, args ...string) (stdout, stderr string, err error)
}

// execCodexRunner is the default command runner using os/exec.
type execCodexRunner struct{}

func (r *execCodexRunner) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// CodexExecutor runs codex CLI commands and filters output.
type CodexExecutor struct {
	Model         string             // model to use, defaults to gpt-5.2-codex
	ProjectDoc    string             // path to project documentation file
	OutputHandler func(text string)  // called for each output line, can be nil
	Debug         bool               // enable debug output
	cmdRunner     CodexCommandRunner // for testing, nil uses default
}

// Run executes codex CLI with the given prompt and returns filtered output.
func (e *CodexExecutor) Run(ctx context.Context, prompt string) Result {
	model := e.Model
	if model == "" {
		model = "gpt-5.2-codex"
	}

	args := []string{
		"exec",
		"-c", fmt.Sprintf("model=%q", model),
		"-c", "model_reasoning_effort=xhigh",
		"-c", "stream_idle_timeout_ms=3600000",
		"-c", `sandbox="read-only"`,
	}

	if e.ProjectDoc != "" {
		args = append(args, "-c", fmt.Sprintf("project_doc=%q", e.ProjectDoc))
	}

	args = append(args, prompt)

	runner := e.cmdRunner
	if runner == nil {
		runner = &execCodexRunner{}
	}

	stdout, stderr, err := runner.Run(ctx, "codex", args...)

	output, filterErr := e.filterOutput(stdout)
	if filterErr != nil {
		return Result{Output: output, Error: filterErr}
	}

	// call handler for each line
	if e.OutputHandler != nil && output != "" {
		e.callHandler(output)
	}

	if err != nil {
		if ctx.Err() != nil {
			return Result{Output: output, Error: ctx.Err()}
		}
		// include stderr in error if available
		if stderr != "" {
			return Result{Output: output, Error: fmt.Errorf("codex error: %s", stderr)}
		}
		return Result{Output: output, Error: fmt.Errorf("codex exited with error: %w", err)}
	}

	// detect signal in output
	signal := detectSignal(output)

	return Result{Output: output, Signal: signal}
}

// callHandler invokes output handler for each line.
func (e *CodexExecutor) callHandler(output string) {
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		e.OutputHandler(scanner.Text() + "\n")
	}
}

// codex output header prefixes to filter
var codexHeaderPrefixes = []string{
	"OpenAI Codex",
	"model:",
	"workdir:",
	"timeout:",
	"sandbox:",
	"project_doc:",
	"model_reasoning_effort:",
	"stream_idle_timeout_ms:",
	"Running:",
	"Executing:",
	"Reading:",
	"─", // box drawing chars
	"│",
	"┌",
	"└",
	"├",
	"┬",
	"┴",
	"┼",
}

// codex noise patterns
var codexNoisePatterns = []string{
	"Thinking...",
	"Processing...",
	"Reading files...",
	"Analyzing...",
	"[spinner]",
	"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏", // spinner chars
}

// filterState tracks state machine for filtering
type filterState struct {
	inHeader     bool
	inFullReview bool
	lastLine     string
}

// isNoise returns true if line should be filtered out.
// Uses state machine to detect headers and sections like ralph.py.
func (e *CodexExecutor) isNoise(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false // keep blank lines within content
	}

	// check header prefixes
	for _, prefix := range codexHeaderPrefixes {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}

	// check noise patterns
	for _, pattern := range codexNoisePatterns {
		if strings.Contains(line, pattern) {
			return true
		}
	}

	return false
}

// filterOutput removes codex noise from output with state machine.
func (e *CodexExecutor) filterOutput(output string) (string, error) {
	var lines []string
	scanner := bufio.NewScanner(strings.NewReader(output))
	state := &filterState{inHeader: true}

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// skip empty lines at start (before any content)
		if state.inHeader && trimmed == "" {
			continue
		}

		// check for "Full review comments:" to enter review section
		if strings.HasPrefix(trimmed, "Full review comments:") {
			state.inFullReview = true
			state.inHeader = false
			continue // skip the header line itself
		}

		// check for priority findings which signals real content
		if strings.HasPrefix(trimmed, "- [P") {
			state.inHeader = false
		}

		// once in review section, keep all lines
		if state.inFullReview {
			// strip bold markers **text**
			cleaned := e.stripBold(line)
			// deduplicate consecutive identical lines (but keep blank lines)
			if cleaned != state.lastLine || trimmed == "" {
				lines = append(lines, cleaned)
				if trimmed != "" {
					state.lastLine = cleaned
				}
			}
			continue
		}

		// filter noise patterns and headers
		if e.isNoise(line) {
			if e.Debug {
				fmt.Printf("[debug] filtered: %s\n", line)
			}
			continue
		}

		// preserve blank lines within content (after header)
		if trimmed == "" && !state.inHeader {
			lines = append(lines, line)
			continue
		}

		// once we get real content, we're past the header
		if trimmed != "" && !state.inHeader {
			// strip bold markers
			cleaned := e.stripBold(line)
			// deduplicate
			if cleaned != state.lastLine {
				lines = append(lines, cleaned)
				state.lastLine = cleaned
			}
		} else if trimmed != "" {
			// first real content line ends header
			state.inHeader = false
			cleaned := e.stripBold(line)
			lines = append(lines, cleaned)
			state.lastLine = cleaned
		}
	}

	if err := scanner.Err(); err != nil {
		return strings.Join(lines, "\n"), fmt.Errorf("filter output: %w", err)
	}

	return strings.Join(lines, "\n"), nil
}

// stripBold removes markdown bold markers (**text**) from text.
func (e *CodexExecutor) stripBold(s string) string {
	// replace **text** with text
	result := s
	for {
		start := strings.Index(result, "**")
		if start == -1 {
			break
		}
		end := strings.Index(result[start+2:], "**")
		if end == -1 {
			break
		}
		// remove both markers
		result = result[:start] + result[start+2:start+2+end] + result[start+2+end+2:]
	}
	return result
}
