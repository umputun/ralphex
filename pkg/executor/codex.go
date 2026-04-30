package executor

import (
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
// when stdin is non-nil, it is connected to the child process's stdin (used to pass
// the prompt via pipe instead of a CLI argument to avoid Windows 8191-char cmd limit).
type execCodexRunner struct {
	stdin io.Reader
}

func (r *execCodexRunner) Run(ctx context.Context, name string, args ...string) (CodexStreams, func() error, error) {
	// check context before starting to avoid spawning a process that will be immediately killed
	if err := ctx.Err(); err != nil {
		return CodexStreams{}, nil, fmt.Errorf("context already canceled: %w", err)
	}

	// use exec.Command (not CommandContext) because we handle cancellation ourselves
	// to ensure the entire process group is killed, not just the direct child
	cmd := exec.Command(name, args...) //nolint:noctx // intentional: we handle context cancellation via process group kill

	// pass prompt via stdin when set (avoids Windows 8191-char command-line limit)
	if r.stdin != nil {
		cmd.Stdin = r.stdin
	}

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
	Model           string            // model to use, defaults to gpt-5.4
	ReasoningEffort string            // reasoning effort level, defaults to "xhigh"
	TimeoutMs       int               // stream idle timeout in ms, defaults to 3600000
	Sandbox         string            // sandbox mode, defaults to "read-only"
	ProjectDoc      string            // path to project documentation file
	OutputHandler   func(text string) // called for each filtered output line in real-time
	Debug           bool              // enable debug output
	ErrorPatterns   []string          // patterns to detect in output (e.g., rate limit messages)
	LimitPatterns   []string          // patterns to detect rate limits (checked before error patterns)
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
		model = "gpt-5.4"
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

	// pass prompt via stdin to avoid Windows 8191-char command-line limit;
	// codex reads from stdin when no positional prompt argument is given
	stdinReader := strings.NewReader(prompt)
	runner := e.runner
	if runner == nil {
		runner = &execCodexRunner{stdin: stdinReader}
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

	// only check error/limit patterns when the process failed (non-zero exit or stream error).
	// when codex exits cleanly, pattern matches in output are false positives from findings
	// (e.g., reviewing code that handles rate limits).
	// skip pattern checks on context cancellation — cancellation must propagate as-is.
	if finalErr != nil && ctx.Err() == nil {
		if patternErr := e.checkPatterns(stdoutContent, stderrRes); patternErr != nil {
			return Result{Output: stdoutContent, Signal: signal, Error: patternErr}
		}
	}

	// return stdout content as the result (the actual answer from codex)
	return Result{Output: stdoutContent, Signal: signal, Error: finalErr}
}

// checkPatterns scans stdout AND the stderr matches captured live during streaming
// for limit/error patterns. codex emits OpenAI/ChatGPT plan-quota errors (e.g.,
// "ERROR: You've hit your usage limit") to stderr while stdout is empty on failure;
// processStderr matches each line on the fly so detection is not subject to the
// 5-line / 256-rune tail truncation used for human-readable error context.
//
// Priority is limit-first across both sources before any error match: a real
// stderr quota diagnostic (already filtered through the CLI-error prefix gate
// in processStderr) must not be downgraded to a non-retryable PatternMatchError
// just because partial stdout happens to match a configured ErrorPattern. Within
// each severity class, stdout wins over stderr so an explicit stdout limit/error
// takes precedence when both sources fire.
//
// Order:
//  1. stdout LimitPatterns
//  2. stderr.limitMatch (prefix-gated)
//  3. stdout ErrorPatterns
//  4. stderr.errorMatch (prefix-gated)
//
// returns LimitPatternError or PatternMatchError when a pattern matches; nil otherwise.
func (e *CodexExecutor) checkPatterns(stdoutContent string, stderr stderrResult) error {
	// limit-class first — across both sources
	if pattern := matchPattern(stdoutContent, e.LimitPatterns); pattern != "" {
		return &LimitPatternError{Pattern: pattern, HelpCmd: "codex /status"}
	}
	if stderr.limitMatch != "" {
		return &LimitPatternError{Pattern: stderr.limitMatch, HelpCmd: "codex /status"}
	}

	// error-class second
	if pattern := matchPattern(stdoutContent, e.ErrorPatterns); pattern != "" {
		return &PatternMatchError{Pattern: pattern, HelpCmd: "codex /status"}
	}
	if stderr.errorMatch != "" {
		return &PatternMatchError{Pattern: stderr.errorMatch, HelpCmd: "codex /status"}
	}

	return nil
}

// stderrResult holds processed stderr output and any error from reading.
// limitMatch and errorMatch capture the FIRST limit/error pattern that fires
// during streaming, on the untruncated, un-evicted line — so detection is not
// subject to the lastLines tail truncation (5 lines, 256 runes per line).
type stderrResult struct {
	lastLines  []string // last few lines of stderr for error context
	limitMatch string   // first matched limit pattern seen on stderr (live scan)
	errorMatch string   // first matched error pattern seen on stderr (live scan)
	err        error
}

// processStderr reads stderr line-by-line, filters for progress display, and
// scans each line for configured limit/error patterns. shows header block
// (between first two "--------" separators) and bold summaries. captures last
// lines of unfiltered output for error reporting AND records the first
// limit/error pattern hit (untruncated, un-evicted) so callers can rely on it
// regardless of how much chatter follows.
func (e *CodexExecutor) processStderr(ctx context.Context, r io.Reader) stderrResult {
	const maxTailLines = 5    // keep last N lines for error context
	const maxLineLength = 256 // truncate long lines to avoid oversized error strings

	state := &codexFilterState{}
	var tail []string
	var limitMatch, errorMatch string

	err := readLines(ctx, r, func(line string) {
		// scan untruncated line for patterns first; record only the first hit
		// per category so detection is eviction- and truncation-resistant.
		// restricted to CLI-error-prefixed lines (see scanLineForPatterns).
		e.scanLineForPatterns(line, &limitMatch, &errorMatch)

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
	})

	if err != nil {
		return stderrResult{lastLines: tail, limitMatch: limitMatch, errorMatch: errorMatch, err: fmt.Errorf("read stderr: %w", err)}
	}
	return stderrResult{lastLines: tail, limitMatch: limitMatch, errorMatch: errorMatch}
}

// scanLineForPatterns updates limitMatch / errorMatch with the first matching
// limit/error pattern found in line, gated by isCodexErrorLine so progress
// chatter cannot trigger false positives. Once each match has been recorded
// it sticks for the rest of the run.
func (e *CodexExecutor) scanLineForPatterns(line string, limitMatch, errorMatch *string) {
	if !isCodexErrorLine(line) {
		return
	}
	if *limitMatch == "" {
		if pattern := matchPattern(line, e.LimitPatterns); pattern != "" {
			*limitMatch = pattern
		}
	}
	if *errorMatch == "" {
		if pattern := matchPattern(line, e.ErrorPatterns); pattern != "" {
			*errorMatch = pattern
		}
	}
}

// isCodexErrorLine reports whether a stderr line looks like a CLI error message
// codex reliably prefixes diagnostics. limit/error pattern matching is gated on
// this prefix so progress text on stderr (header banners, bold summaries, model
// chatter that may legitimately mention "rate limit" while reviewing code) does
// not trigger false-positive matches.
func isCodexErrorLine(line string) bool {
	s := strings.TrimSpace(line)
	if s == "" {
		return false
	}
	// case-insensitive prefix match; codex uses "ERROR:" today, others are
	// defensive against possible future variants.
	lower := strings.ToLower(s)
	return strings.HasPrefix(lower, "error:") ||
		strings.HasPrefix(lower, "fatal:") ||
		strings.HasPrefix(lower, "panic:")
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
