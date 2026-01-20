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

// filterOutput removes codex noise from output.
func (e *CodexExecutor) filterOutput(output string) (string, error) {
	var lines []string
	scanner := bufio.NewScanner(strings.NewReader(output))

	for scanner.Scan() {
		line := scanner.Text()

		// skip common noise patterns
		if e.isNoise(line) {
			if e.Debug {
				fmt.Printf("[debug] filtered: %s\n", line)
			}
			continue
		}

		lines = append(lines, line)
	}

	if err := scanner.Err(); err != nil {
		return strings.Join(lines, "\n"), fmt.Errorf("filter output: %w", err)
	}

	return strings.Join(lines, "\n"), nil
}

// isNoise returns true if line should be filtered out.
func (e *CodexExecutor) isNoise(line string) bool {
	// skip empty lines at start/end (will be trimmed)
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false // keep blank lines within content
	}

	// skip progress indicators
	noisePatterns := []string{
		"Thinking...",
		"Processing...",
		"Reading files...",
		"Analyzing...",
		"[spinner]",
		"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏", // spinner chars
	}

	for _, pattern := range noisePatterns {
		if strings.Contains(line, pattern) {
			return true
		}
	}

	return false
}
