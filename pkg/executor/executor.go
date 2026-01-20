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
)

//go:generate moq -out mocks/command_runner.go -pkg mocks -skip-ensure -fmt goimports . CommandRunner

// Result holds execution result with output and detected signal.
type Result struct {
	Output string // accumulated text output
	Signal string // detected signal (COMPLETED, FAILED, etc.) or empty
	Error  error  // execution error if any
}

// CommandRunner abstracts command execution for testing.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) (stdout io.Reader, wait func() error, err error)
}

// execCommandRunner is the default command runner using os/exec.
type execCommandRunner struct{}

func (r *execCommandRunner) Run(ctx context.Context, name string, args ...string) (io.Reader, func() error, error) {
	cmd := exec.CommandContext(ctx, name, args...)

	// filter out ANTHROPIC_API_KEY from environment (claude uses different auth)
	cmd.Env = filterEnv(os.Environ(), "ANTHROPIC_API_KEY")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("create stdout pipe: %w", err)
	}
	// merge stderr into stdout like python's stderr=subprocess.STDOUT
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("start command: %w", err)
	}
	return stdout, cmd.Wait, nil
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
	Result struct {
		Output string `json:"output"`
	} `json:"result"`
}

// ClaudeExecutor runs claude CLI commands with streaming JSON parsing.
type ClaudeExecutor struct {
	OutputHandler func(text string) // called for each text chunk, can be nil
	Debug         bool              // enable debug output
	cmdRunner     CommandRunner     // for testing, nil uses default
}

// Run executes claude CLI with the given prompt and parses streaming JSON output.
func (e *ClaudeExecutor) Run(ctx context.Context, prompt string) Result {
	args := []string{
		"--dangerously-skip-permissions",
		"--output-format", "stream-json",
		"--verbose",
		"-p", prompt,
	}

	runner := e.cmdRunner
	if runner == nil {
		runner = &execCommandRunner{}
	}

	stdout, wait, err := runner.Run(ctx, "claude", args...)
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

	return result
}

// parseStream reads and parses the JSON stream from claude CLI.
func (e *ClaudeExecutor) parseStream(r io.Reader) Result {
	var output strings.Builder
	var signal string

	scanner := bufio.NewScanner(r)
	// increase buffer size for large JSON lines
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

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
		return event.Result.Output
	}
	return ""
}

// detectSignal checks text for completion signals.
// Looks for <<<RALPHEX:...>>> format signals.
func detectSignal(text string) string {
	signals := []string{
		"<<<RALPHEX:ALL_TASKS_DONE>>>",
		"<<<RALPHEX:TASK_FAILED>>>",
		"<<<RALPHEX:REVIEW_DONE>>>",
		"<<<RALPHEX:CODEX_REVIEW_DONE>>>",
	}
	for _, sig := range signals {
		if strings.Contains(text, sig) {
			return sig
		}
	}
	return ""
}
