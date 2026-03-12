// Package executor provides CLI execution for Claude and Codex tools.
package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/umputun/ralphex/pkg/status"
)

//go:generate moq -out mocks/command_runner.go -pkg mocks -skip-ensure -fmt goimports . CommandRunner

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

// LimitPatternError is returned when a configured rate limit pattern is detected in output.
// when wait-on-limit is configured, the caller retries instead of exiting.
type LimitPatternError struct {
	Pattern string // the pattern that matched
	HelpCmd string // command to run for more information
}

func (e *LimitPatternError) Error() string {
	return fmt.Sprintf("detected limit pattern: %q", e.Pattern)
}

// CommandRunner abstracts command execution for testing.
// Returns stdin pipe, stdout reader, and a wait function for completion.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) (stdin io.WriteCloser, output io.Reader, wait func() error, err error)
}

// execClaudeRunner is the default command runner using os/exec.
type execClaudeRunner struct{}

func (r *execClaudeRunner) Run(ctx context.Context, name string, args ...string) (io.WriteCloser, io.Reader, func() error, error) {
	// check context before starting to avoid spawning a process that will be immediately killed
	if err := ctx.Err(); err != nil {
		return nil, nil, nil, fmt.Errorf("context already canceled: %w", err)
	}

	// use exec.Command (not CommandContext) because we handle cancellation ourselves
	// to ensure the entire process group is killed, not just the direct child
	cmd := exec.Command(name, args...) //nolint:noctx // intentional: we handle context cancellation via process group kill

	// filter out ANTHROPIC_API_KEY (claude uses different auth) and CLAUDECODE (prevents nested session errors)
	cmd.Env = filterEnv(os.Environ(), "ANTHROPIC_API_KEY", "CLAUDECODE")

	// create new process group so we can kill all descendants on cleanup
	setupProcessGroup(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create stdout pipe: %w", err)
	}
	// merge stderr into stdout like python's stderr=subprocess.STDOUT
	cmd.Stderr = cmd.Stdout

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create stdin pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, nil, nil, fmt.Errorf("start command: %w", err)
	}

	// setup process group cleanup with graceful shutdown on context cancellation
	cleanup := newProcessGroupCleanup(cmd, ctx.Done())

	return stdin, stdout, cleanup.Wait, nil
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
	LimitPatterns []string          // patterns to detect rate limits (checked before error patterns)
	cmdRunner     CommandRunner     // for testing, nil uses default
	session       *ClaudeSession    // active session for message injection
	sessionMu     sync.Mutex        // protects session field
}

// Session returns the current active session, or nil if no process is running.
func (e *ClaudeExecutor) Session() *ClaudeSession {
	e.sessionMu.Lock()
	defer e.sessionMu.Unlock()
	return e.session
}

// Run executes claude CLI with the given prompt and parses streaming JSON output.
func (e *ClaudeExecutor) Run(ctx context.Context, prompt string) Result {
	cmd := e.Command
	if cmd == "" {
		cmd = "claude"
	}

	useStreamInput := e.Args == "" // default args path uses stream-json input

	// build args from configured string or use defaults
	var args []string
	if e.Args != "" {
		args = splitArgs(e.Args)
		args = append(args, "-p", prompt)
	} else {
		args = []string{
			"--dangerously-skip-permissions",
			"--output-format", "stream-json",
			"--input-format", "stream-json",
			"--verbose",
		}
	}

	runner := e.cmdRunner
	if runner == nil {
		runner = &execClaudeRunner{}
	}

	stdin, stdout, wait, err := runner.Run(ctx, cmd, args...)
	if err != nil {
		return Result{Error: err}
	}

	if useStreamInput {
		// create session from stdin pipe for message injection
		session := newClaudeSession(stdin)
		e.sessionMu.Lock()
		e.session = session
		e.sessionMu.Unlock()

		defer func() {
			session.Close()
			e.sessionMu.Lock()
			e.session = nil
			e.sessionMu.Unlock()
		}()

		// send initial prompt via stdin
		if sendErr := session.Send(prompt); sendErr != nil {
			return Result{Error: fmt.Errorf("send initial prompt: %w", sendErr)}
		}
	} else {
		// custom args path: close stdin immediately (wrappers may not understand stream-json input)
		stdin.Close()
	}

	result := e.parseStream(ctx, stdout)

	if err := wait(); err != nil {
		// check if it was context cancellation
		if ctx.Err() != nil {
			return Result{Output: result.Output, Signal: result.Signal, Error: ctx.Err()}
		}
		if result.Output == "" {
			return Result{Error: fmt.Errorf("claude exited with error: %w", err)}
		}
		// non-zero exit with output but no signal means claude failed without doing useful work.
		// if there IS a signal, work was done — ignore exit code (some tasks exit non-zero after completion).
		if result.Signal == "" {
			result.Error = fmt.Errorf("claude exited with error: %w", err)
		}
	}

	// check limit patterns first (higher priority)
	if pattern := matchPattern(result.Output, e.LimitPatterns); pattern != "" {
		return Result{
			Output: result.Output,
			Signal: result.Signal,
			Error:  &LimitPatternError{Pattern: pattern, HelpCmd: "claude /usage"},
		}
	}

	// check for error patterns in output
	if pattern := matchPattern(result.Output, e.ErrorPatterns); pattern != "" {
		return Result{
			Output: result.Output,
			Signal: result.Signal,
			Error:  &PatternMatchError{Pattern: pattern, HelpCmd: "claude /usage"},
		}
	}

	return result
}

// parseStream reads and parses the JSON stream from claude CLI.
// uses readLines internally, so there is no line length limit.
// checks ctx.Done() between reads so cancellation is not blocked by slow pipe reads.
func (e *ClaudeExecutor) parseStream(ctx context.Context, r io.Reader) Result {
	var output strings.Builder
	var signal string

	err := readLines(ctx, r, func(line string) {
		if line == "" {
			return
		}

		var event streamEvent
		if jsonErr := json.Unmarshal([]byte(line), &event); jsonErr != nil {
			// print non-JSON lines as-is
			if e.Debug {
				log.Printf("[debug] non-JSON line: %s", line)
			}
			output.WriteString(line)
			output.WriteString("\n")
			if e.OutputHandler != nil {
				e.OutputHandler(line + "\n")
			}
			return
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
	})

	if err != nil {
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

// matchPattern checks output for configured patterns.
// Returns the first matching pattern or empty string if none match.
// Matching is case-insensitive substring search.
func matchPattern(output string, patterns []string) string {
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
