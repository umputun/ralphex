// Package executor provides CLI execution for Claude and Codex tools.
package executor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/umputun/ralphex/pkg/status"
)

//go:generate moq -out mocks/command_runner.go -pkg mocks -skip-ensure -fmt goimports . CommandRunner

// MaxScannerBuffer is the maximum buffer size for bufio.Scanner.
// set to 64MB to handle large outputs (e.g., diffs of large JSON files).
const MaxScannerBuffer = 64 * 1024 * 1024

// Result holds execution result with output and detected signal.
type Result struct {
	Output string // accumulated text output
	Signal string // detected signal (COMPLETED, FAILED, etc.) or empty
	Error  error  // execution error if any
}

// PatternMatchError is returned when a configured error pattern is detected in output.
type PatternMatchError struct {
	Pattern string // the pattern that matched
	HelpCmd string // command to run for more information (e.g., "claude /usage")
}

func (e *PatternMatchError) Error() string {
	return fmt.Sprintf("detected error pattern: %q", e.Pattern)
}

// CommandRunner abstracts command execution for testing.
// Returns an io.Reader for streaming output and a wait function for completion.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) (output io.Reader, wait func() error, err error)
}

// execClaudeRunner is the default command runner using os/exec.
type execClaudeRunner struct{}

func (r *execClaudeRunner) Run(ctx context.Context, name string, args ...string) (io.Reader, func() error, error) {
	// check context before starting to avoid spawning a process that will be immediately killed
	if err := ctx.Err(); err != nil {
		return nil, nil, fmt.Errorf("context already canceled: %w", err)
	}

	// use exec.Command (not CommandContext) because we handle cancellation ourselves
	// to ensure the entire process group is killed, not just the direct child
	cmd := exec.Command(name, args...) //nolint:noctx // intentional: we handle context cancellation via process group kill

	// filter out ANTHROPIC_API_KEY from environment (claude uses different auth)
	cmd.Env = filterEnv(os.Environ(), "ANTHROPIC_API_KEY")

	// create new process group so we can kill all descendants on cleanup
	setupProcessGroup(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("create stdout pipe: %w", err)
	}
	// merge stderr into stdout like python's stderr=subprocess.STDOUT
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("start command: %w", err)
	}

	// setup process group cleanup with graceful shutdown on context cancellation
	cleanup := newProcessGroupCleanup(cmd, ctx.Done())

	return stdout, cleanup.Wait, nil
}

// splitArgs splits a space-separated argument string into a slice.
// handles quoted strings (both single and double quotes).
func splitArgs(s string) []string {
	var args []string
	var current strings.Builder
	var inQuote rune
	var escaped bool

	for _, r := range s {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}

		if r == '\\' {
			escaped = true
			continue
		}

		if r == '"' || r == '\'' {
			switch { //nolint:staticcheck // cannot use tagged switch because we compare with both inQuote and r
			case inQuote == 0:
				inQuote = r
			case inQuote == r:
				inQuote = 0
			default:
				current.WriteRune(r)
			}
			continue
		}

		if r == ' ' && inQuote == 0 {
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
			continue
		}

		current.WriteRune(r)
	}

	if current.Len() > 0 {
		args = append(args, current.String())
	}

	return args
}

// filterEnv returns a copy of env with specified keys removed.
func filterEnv(env []string, keysToRemove ...string) []string {
	result := make([]string, 0, len(env))
	for _, e := range env {
		skip := false
		for _, key := range keysToRemove {
			if strings.HasPrefix(e, key+"=") {
				skip = true
				break
			}
		}
		if !skip {
			result = append(result, e)
		}
	}
	return result
}

// streamEvent represents a JSON event from claude CLI stream output.
type streamEvent struct {
	Type    string `json:"type"`
	Message struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
	ContentBlock struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content_block"`
	Delta struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"delta"`
	Result json.RawMessage `json:"result"` // can be string or object with "output" field
}

// ClaudeExecutor runs claude CLI commands with streaming JSON parsing.
type ClaudeExecutor struct {
	Command       string            // command to execute, defaults to "claude"
	Args          string            // additional arguments (space-separated), defaults to standard args
	OutputHandler func(text string) // called for each text chunk, can be nil
	Debug         bool              // enable debug output
	ErrorPatterns []string          // patterns to detect in output (e.g., rate limit messages)
	cmdRunner     CommandRunner     // for testing, nil uses default
}

// Run executes claude CLI with the given prompt and parses streaming JSON output.
func (e *ClaudeExecutor) Run(ctx context.Context, prompt string) Result {
	cmd := e.Command
	if cmd == "" {
		cmd = "claude"
	}

	// build args from configured string or use defaults
	var args []string
	if e.Args != "" {
		args = splitArgs(e.Args)
	} else {
		args = []string{
			"--dangerously-skip-permissions",
			"--output-format", "stream-json",
			"--verbose",
		}
	}
	args = append(args, "-p", prompt)

	runner := e.cmdRunner
	if runner == nil {
		runner = &execClaudeRunner{}
	}

	stdout, wait, err := runner.Run(ctx, cmd, args...)
	if err != nil {
		return Result{Error: err}
	}

	result := e.parseStream(stdout)

	if err := wait(); err != nil {
		// check if it was context cancellation
		if ctx.Err() != nil {
			return Result{Output: result.Output, Signal: result.Signal, Error: ctx.Err()}
		}
		// non-zero exit might still have useful output
		if result.Output == "" {
			return Result{Error: fmt.Errorf("claude exited with error: %w", err)}
		}
	}

	// check for error patterns in output
	if pattern := checkErrorPatterns(result.Output, e.ErrorPatterns); pattern != "" {
		return Result{
			Output: result.Output,
			Signal: result.Signal,
			Error:  &PatternMatchError{Pattern: pattern, HelpCmd: "claude /usage"},
		}
	}

	return result
}

// parseStream reads and parses the JSON stream from claude CLI.
func (e *ClaudeExecutor) parseStream(r io.Reader) Result {
	var output strings.Builder
	var signal string

	scanner := bufio.NewScanner(r)
	// increase buffer size for large JSON lines (large diffs with parallel agents)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, MaxScannerBuffer)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var event streamEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			// print non-JSON lines as-is
			if e.Debug {
				fmt.Printf("[debug] non-JSON line: %s\n", line)
			}
			output.WriteString(line)
			output.WriteString("\n")
			if e.OutputHandler != nil {
				e.OutputHandler(line + "\n")
			}
			continue
		}

		text := e.extractText(&event)
		if text != "" {
			output.WriteString(text)
			if e.OutputHandler != nil {
				e.OutputHandler(text)
			}

			// check for signals in text
			if sig := detectSignal(text); sig != "" {
				signal = sig
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return Result{Output: output.String(), Signal: signal, Error: fmt.Errorf("stream read: %w", err)}
	}

	return Result{Output: output.String(), Signal: signal}
}

// extractText extracts text content from various event types.
func (e *ClaudeExecutor) extractText(event *streamEvent) string {
	switch event.Type {
	case "assistant":
		// assistant events contain message.content array with text blocks
		var texts []string
		for _, c := range event.Message.Content {
			if c.Type == "text" && c.Text != "" {
				texts = append(texts, c.Text)
			}
		}
		return strings.Join(texts, "")
	case "content_block_delta":
		if event.Delta.Type == "text_delta" {
			return event.Delta.Text
		}
	case "message_stop":
		// check final message content
		for _, c := range event.Message.Content {
			if c.Type == "text" {
				return c.Text
			}
		}
	case "result":
		// result can be a string or object with "output" field
		if len(event.Result) == 0 {
			return ""
		}
		// try as string first (session summary format)
		var resultStr string
		if err := json.Unmarshal(event.Result, &resultStr); err == nil {
			return "" // skip session summary - content already streamed
		}
		// try as object with output field
		var resultObj struct {
			Output string `json:"output"`
		}
		if err := json.Unmarshal(event.Result, &resultObj); err == nil {
			return resultObj.Output
		}
	}
	return ""
}

// detectSignal checks text for completion status.
// looks for <<<RALPHEX:...>>> format status.
func detectSignal(text string) string {
	knownSignals := []string{
		status.Completed,
		status.Failed,
		status.ReviewDone,
		status.CodexDone,
		status.PlanReady,
	}
	for _, sig := range knownSignals {
		if strings.Contains(text, sig) {
			return sig
		}
	}
	return ""
}

// checkErrorPatterns checks output for configured error patterns.
// Returns the first matching pattern or empty string if none match.
// Matching is case-insensitive substring search.
func checkErrorPatterns(output string, patterns []string) string {
	if len(patterns) == 0 {
		return ""
	}
	outputLower := strings.ToLower(output)
	for _, pattern := range patterns {
		trimmed := strings.TrimSpace(pattern)
		if trimmed == "" {
			continue
		}
		if strings.Contains(outputLower, strings.ToLower(trimmed)) {
			return trimmed
		}
	}
	return ""
}
