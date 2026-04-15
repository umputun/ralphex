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
	"time"

	"github.com/umputun/ralphex/pkg/status"
)

//go:generate moq -out mocks/command_runner.go -pkg mocks -skip-ensure -fmt goimports . CommandRunner

// Result holds execution result with output and detected signal.
type Result struct {
	Output       string // accumulated text output
	RecentText   string // last 10 text blocks joined, used for pattern matching to avoid false positives
	Signal       string // detected signal (COMPLETED, FAILED, etc.) or empty
	Error        error  // execution error if any
	IdleTimedOut bool   // true when idle timeout fired (derived context canceled, parent alive)
}

const recentBlockCount = 10 // number of recent text blocks to keep for pattern matching

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
// Returns an io.Reader for streaming output and a wait function for completion.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) (output io.Reader, wait func() error, err error)
}

// execClaudeRunner is the default command runner using os/exec.
// when stdin is non-nil, it is connected to the child process's stdin (used to pass
// the prompt via pipe instead of a -p CLI argument to avoid Windows 8191-char cmd limit).
type execClaudeRunner struct {
	stdin io.Reader
}

func (r *execClaudeRunner) Run(ctx context.Context, name string, args ...string) (io.Reader, func() error, error) {
	// check context before starting to avoid spawning a process that will be immediately killed
	if err := ctx.Err(); err != nil {
		return nil, nil, fmt.Errorf("context already canceled: %w", err)
	}

	// use exec.Command (not CommandContext) because we handle cancellation ourselves
	// to ensure the entire process group is killed, not just the direct child
	cmd := exec.Command(name, args...) //nolint:noctx // intentional: we handle context cancellation via process group kill

	// filter out ANTHROPIC_API_KEY (claude uses different auth) and CLAUDECODE (prevents nested session errors)
	cmd.Env = filterEnv(os.Environ(), "ANTHROPIC_API_KEY", "CLAUDECODE")

	// pass prompt via stdin when set (avoids Windows 8191-char command-line limit)
	if r.stdin != nil {
		cmd.Stdin = r.stdin
	}

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
	Model         string            // model override (e.g., "opus", "sonnet", "haiku"); empty = CLI default
	OutputHandler func(text string) // called for each text chunk, can be nil
	Debug         bool              // enable debug output
	ErrorPatterns []string          // patterns to detect in output (e.g., rate limit messages)
	LimitPatterns []string          // patterns to detect rate limits (checked before error patterns)
	IdleTimeout   time.Duration     // kill session after this duration of no output, zero = disabled
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
	// inject --model flag if a model override is configured
	if e.Model != "" {
		args = append(args, "--model", e.Model)
	}
	// always append --print to enable non-interactive mode; mirrors old -p flag that was
	// always appended. wrapper scripts ignore unknown flags via '*) shift ;;' catch-all.
	args = append(args, "--print")
	// pass prompt via stdin to avoid Windows 8191-char command-line limit;
	// if cmdRunner is set (test injection), use it; otherwise use real runner
	stdinReader := strings.NewReader(prompt)
	var runner CommandRunner
	if e.cmdRunner != nil {
		runner = e.cmdRunner
	} else {
		runner = &execClaudeRunner{stdin: stdinReader}
	}

	// set up idle timeout: derive a cancellable context that fires when no output
	// is received for IdleTimeout duration. the touch closure resets the timer on
	// each line of output and is called from parseStream's readLines handler.
	execCtx := ctx
	idleTouch := func() {} // no-op by default
	if e.IdleTimeout > 0 {
		var idleCancel context.CancelFunc
		execCtx, idleCancel = context.WithCancel(ctx)
		defer idleCancel()
		timer := time.AfterFunc(e.IdleTimeout, idleCancel)
		defer timer.Stop()
		idleTouch = func() { timer.Reset(e.IdleTimeout) }
	}

	stdout, wait, err := runner.Run(execCtx, cmd, args...)
	if err != nil {
		return Result{Error: err}
	}

	result := e.parseStream(execCtx, stdout, idleTouch)
	waitErr := wait()

	// idle timeout: derived context canceled but parent is alive — not an error.
	// return accumulated output and signal as-is, clearing any context-cancellation errors.
	// set IdleTimedOut so the runner can distinguish idle timeout from normal completion
	// and avoid false "no changes detected" exits in review loops.
	if e.IdleTimeout > 0 && execCtx.Err() != nil && ctx.Err() == nil {
		// check limit patterns first — idle timeout may have fired after a rate-limit message,
		// and the caller needs LimitPatternError to trigger wait-and-retry logic.
		if pattern := matchPattern(result.RecentText, e.LimitPatterns); pattern != "" {
			return Result{
				Output: result.Output, RecentText: result.RecentText,
				Signal: result.Signal,
				Error:  &LimitPatternError{Pattern: pattern, HelpCmd: "claude /usage"},
			}
		}
		// check for error patterns in output
		if pattern := matchPattern(result.RecentText, e.ErrorPatterns); pattern != "" {
			return Result{
				Output: result.Output, RecentText: result.RecentText,
				Signal: result.Signal,
				Error:  &PatternMatchError{Pattern: pattern, HelpCmd: "claude /usage"},
			}
		}
		result.Error = nil
		result.IdleTimedOut = true
		return result
	}

	if waitErr != nil {
		// check if it was context cancellation
		if ctx.Err() != nil {
			return Result{Output: result.Output, RecentText: result.RecentText, Signal: result.Signal, Error: ctx.Err()}
		}
		if result.Output == "" {
			return Result{Error: fmt.Errorf("claude exited with error: %w", waitErr)}
		}
		// non-zero exit with output but no signal means claude failed without doing useful work.
		// if there IS a signal, work was done — ignore exit code (some tasks exit non-zero after completion).
		if result.Signal == "" {
			result.Error = fmt.Errorf("claude exited with error: %w", waitErr)
		}
	}

	// check limit patterns first (higher priority)
	if pattern := matchPattern(result.RecentText, e.LimitPatterns); pattern != "" {
		return Result{
			Output: result.Output, RecentText: result.RecentText,
			Signal: result.Signal,
			Error:  &LimitPatternError{Pattern: pattern, HelpCmd: "claude /usage"},
		}
	}

	// check for error patterns in output
	if pattern := matchPattern(result.RecentText, e.ErrorPatterns); pattern != "" {
		return Result{
			Output: result.Output, RecentText: result.RecentText,
			Signal: result.Signal,
			Error:  &PatternMatchError{Pattern: pattern, HelpCmd: "claude /usage"},
		}
	}

	return result
}

// parseStream reads and parses the JSON stream from claude CLI.
// uses readLines internally, so there is no line length limit.
// checks ctx.Done() between reads so cancellation is not blocked by slow pipe reads.
// idleTouch resets the idle timer on each line of output; pass no-op when idle timeout is disabled.
func (e *ClaudeExecutor) parseStream(ctx context.Context, r io.Reader, idleTouch func()) Result {
	var output strings.Builder
	var signal string
	var recentBlocks [recentBlockCount]string
	var blockIdx int

	err := readLines(ctx, r, func(line string) {
		idleTouch() // reset idle timer on every line of pipe activity
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
			recentBlocks[blockIdx%recentBlockCount] = line
			blockIdx++
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

			// track recent blocks for pattern matching (avoids false positives on full output)
			recentBlocks[blockIdx%recentBlockCount] = text
			blockIdx++

			// check for signals in text
			if sig := detectSignal(text); sig != "" {
				signal = sig
			}
		}
	})

	// join recent blocks in chronological order for pattern matching.
	// iterate from the oldest slot forward to preserve order after wrap-around.
	var recent strings.Builder
	start := blockIdx % recentBlockCount
	for i := range recentBlockCount {
		b := recentBlocks[(start+i)%recentBlockCount]
		if b != "" {
			recent.WriteString(b)
			recent.WriteString("\n")
		}
	}

	if err != nil {
		return Result{Output: output.String(), RecentText: recent.String(), Signal: signal,
			Error: fmt.Errorf("stream read: %w", err)}
	}

	return Result{Output: output.String(), RecentText: recent.String(), Signal: signal}
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
