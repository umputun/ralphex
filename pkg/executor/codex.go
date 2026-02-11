package executor

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// CodexStreams holds both stderr and stdout from codex command.
type CodexStreams struct {
	Stderr io.Reader
	Stdout io.Reader
}

// CodexRunner abstracts command execution for codex.
// Returns both stderr (streaming progress) and stdout (final response).
type CodexRunner interface {
	Run(ctx context.Context, name string, args ...string) (streams CodexStreams, wait func() error, err error)
}

// execCodexRunner is the default command runner using os/exec for codex.
// codex outputs streaming progress to stderr, final response to stdout.
type execCodexRunner struct{}

func (r *execCodexRunner) Run(ctx context.Context, name string, args ...string) (CodexStreams, func() error, error) {
	// check context before starting to avoid spawning a process that will be immediately killed
	if err := ctx.Err(); err != nil {
		return CodexStreams{}, nil, fmt.Errorf("context already canceled: %w", err)
	}

	// use exec.Command (not CommandContext) because we handle cancellation ourselves
	// to ensure the entire process group is killed, not just the direct child
	cmd := exec.Command(name, args...) //nolint:noctx // intentional: we handle context cancellation via process group kill

	// create new process group so we can kill all descendants on cleanup
	setupProcessGroup(cmd)

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return CodexStreams{}, nil, fmt.Errorf("stderr pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return CodexStreams{}, nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return CodexStreams{}, nil, fmt.Errorf("start command: %w", err)
	}

	// setup process group cleanup with graceful shutdown on context cancellation
	cleanup := newProcessGroupCleanup(cmd, ctx.Done())

	return CodexStreams{Stderr: stderr, Stdout: stdout}, cleanup.Wait, nil
}

// CodexExecutor runs codex CLI commands and filters output.
type CodexExecutor struct {
	Command         string            // command to execute, defaults to "codex"
	Model           string            // model to use, defaults to gpt-5.3-codex
	ReasoningEffort string            // reasoning effort level, defaults to "xhigh"
	TimeoutMs       int               // stream idle timeout in ms, defaults to 3600000
	Sandbox         string            // sandbox mode, defaults to "read-only"
	ProjectDoc      string            // path to project documentation file
	OutputHandler   func(text string) // called for each filtered output line in real-time
	Debug           bool              // enable debug output
	ErrorPatterns   []string          // patterns to detect in output (e.g., rate limit messages)
	runner          CodexRunner       // for testing, nil uses default
}

// codexFilterState tracks header separator count for filtering.
type codexFilterState struct {
	headerCount int             // tracks "--------" separators seen (show content between first two)
	seen        map[string]bool // track all shown lines for deduplication
}

// Run executes codex CLI with the given prompt and returns filtered output.
// stderr is streamed line-by-line to OutputHandler for progress indication.
// stdout is captured entirely as the final response (returned in Result.Output).
func (e *CodexExecutor) Run(ctx context.Context, prompt string) Result {
	cmd := e.Command
	if cmd == "" {
		cmd = "codex"
	}

	model := e.Model
	if model == "" {
		model = "gpt-5.3-codex"
	}

	reasoningEffort := e.ReasoningEffort
	if reasoningEffort == "" {
		reasoningEffort = "xhigh"
	}

	timeoutMs := e.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 3600000
	}

	sandbox := e.Sandbox
	if sandbox == "" {
		sandbox = "read-only"
	}
	// disable sandbox in docker (landlock doesn't work in containers)
	if os.Getenv("RALPHEX_DOCKER") == "1" {
		sandbox = "danger-full-access"
	}

	args := []string{
		"exec",
		"--sandbox", sandbox,
		"-c", fmt.Sprintf("model=%q", model),
		"-c", "model_reasoning_effort=" + reasoningEffort,
		"-c", fmt.Sprintf("stream_idle_timeout_ms=%d", timeoutMs),
	}

	if e.ProjectDoc != "" {
		args = append(args, "-c", fmt.Sprintf("project_doc=%q", e.ProjectDoc))
	}

	args = append(args, prompt)

	runner := e.runner
	if runner == nil {
		runner = &execCodexRunner{}
	}

	streams, wait, err := runner.Run(ctx, cmd, args...)
	if err != nil {
		return Result{Error: fmt.Errorf("start codex: %w", err)}
	}

	// process stderr for progress display (header block + bold summaries)
	stderrDone := make(chan stderrResult, 1)
	go func() {
		stderrDone <- e.processStderr(ctx, streams.Stderr)
	}()

	// read stdout entirely as final response
	stdoutContent, stdoutErr := e.readStdout(streams.Stdout)

	// wait for stderr processing to complete
	stderrRes := <-stderrDone

	// wait for command completion
	waitErr := wait()

	// determine final error (prefer stderr/stdout errors over wait error)
	var finalErr error
	switch {
	case stderrRes.err != nil && !errors.Is(stderrRes.err, context.Canceled):
		finalErr = stderrRes.err
	case stdoutErr != nil:
		finalErr = stdoutErr
	case waitErr != nil:
		if ctx.Err() != nil {
			finalErr = fmt.Errorf("context error: %w", ctx.Err())
		} else {
			// include stderr tail for error context when codex exits with non-zero status
			if len(stderrRes.lastLines) > 0 {
				finalErr = fmt.Errorf("codex exited with error: %w\nstderr: %s",
					waitErr, strings.Join(stderrRes.lastLines, "\n"))
			} else {
				finalErr = fmt.Errorf("codex exited with error: %w", waitErr)
			}
		}
	}

	// detect signal in stdout (the actual response)
	signal := detectSignal(stdoutContent)

	// check for error patterns in output
	if pattern := checkErrorPatterns(stdoutContent, e.ErrorPatterns); pattern != "" {
		return Result{
			Output: stdoutContent,
			Signal: signal,
			Error:  &PatternMatchError{Pattern: pattern, HelpCmd: "codex /status"},
		}
	}

	// return stdout content as the result (the actual answer from codex)
	return Result{Output: stdoutContent, Signal: signal, Error: finalErr}
}

// stderrResult holds processed stderr output and any error from reading.
type stderrResult struct {
	lastLines []string // last few lines of stderr for error context
	err       error
}

// processStderr reads stderr line-by-line, filters for progress display.
// shows header block (between first two "--------" separators) and bold summaries.
// also captures last lines of unfiltered output for error reporting.
func (e *CodexExecutor) processStderr(ctx context.Context, r io.Reader) stderrResult {
	const maxTailLines = 5    // keep last N lines for error context
	const maxLineLength = 256 // truncate long lines to avoid oversized error strings

	state := &codexFilterState{}
	scanner := bufio.NewScanner(r)
	// increase buffer size for large output lines
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, MaxScannerBuffer)

	var tail []string

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return stderrResult{lastLines: tail, err: fmt.Errorf("context done: %w", ctx.Err())}
		default:
		}

		line := scanner.Text()

		// capture non-empty lines for error context, preserving original formatting
		if strings.TrimSpace(line) != "" {
			stored := line
			if runes := []rune(stored); len(runes) > maxLineLength {
				stored = string(runes[:maxLineLength]) + "..."
			}
			tail = append(tail, stored)
			if len(tail) > maxTailLines {
				copy(tail, tail[1:])
				tail = tail[:maxTailLines]
			}
		}

		if show, filtered := e.shouldDisplay(line, state); show {
			if e.OutputHandler != nil {
				e.OutputHandler(filtered + "\n")
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return stderrResult{lastLines: tail, err: fmt.Errorf("read stderr: %w", err)}
	}
	return stderrResult{lastLines: tail}
}

// readStdout reads the entire stdout content as the final response.
func (e *CodexExecutor) readStdout(r io.Reader) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("read stdout: %w", err)
	}
	return string(data), nil
}

// shouldDisplay implements a simple filter for codex stderr output.
// shows: header block (between first two "--------" separators) and bold summaries.
// also deduplicates lines to avoid non-consecutive repeats.
func (e *CodexExecutor) shouldDisplay(line string, state *codexFilterState) (bool, string) {
	s := strings.TrimSpace(line)
	if s == "" {
		return false, ""
	}

	var show bool
	var filtered string
	var skipDedup bool // separators are not deduplicated

	switch {
	case strings.HasPrefix(s, "--------"):
		// track "--------" separators for header block
		state.headerCount++
		show = state.headerCount <= 2 // show first two separators
		filtered = line
		skipDedup = true // don't deduplicate separators
	case state.headerCount == 1:
		// show everything between first two separators (header block)
		show = true
		filtered = line
	case strings.HasPrefix(s, "**"):
		// show bold summaries after header (progress indication)
		show = true
		filtered = e.stripBold(s)
	}

	// check for duplicates before returning (except separators)
	if show && !skipDedup {
		if state.seen == nil {
			state.seen = make(map[string]bool)
		}
		if state.seen[filtered] {
			return false, "" // skip duplicate
		}
		state.seen[filtered] = true
	}

	return show, filtered
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
