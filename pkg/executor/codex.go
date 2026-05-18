package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
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

// childEnv strips ANTHROPIC_API_KEY and CLAUDECODE from the codex child process env.
// codex doesn't need either, and a user switching to --codex for billing isolation
// should not have an inherited Anthropic key silently bill Anthropic if their codex
// config selects an Anthropic-compatible provider.
func (r *execCodexRunner) childEnv(env []string) []string {
	return filterEnv(env, "ANTHROPIC_API_KEY", "CLAUDECODE")
}

func (r *execCodexRunner) Run(ctx context.Context, name string, args ...string) (CodexStreams, func() error, error) {
	// check context before starting to avoid spawning a process that will be immediately killed
	if err := ctx.Err(); err != nil {
		return CodexStreams{}, nil, fmt.Errorf("context already canceled: %w", err)
	}

	// use exec.Command (not CommandContext) because we handle cancellation ourselves
	// to ensure the entire process group is killed, not just the direct child
	cmd := exec.Command(name, args...) //nolint:noctx // intentional: we handle context cancellation via process group kill

	cmd.Env = r.childEnv(os.Environ())

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
	Model           string            // model override; empty means inherit from ~/.codex/config.toml (no -c model= flag emitted)
	ReasoningEffort string            // reasoning effort override; empty means inherit from ~/.codex/config.toml
	TimeoutMs       int               // stream idle timeout in ms, defaults to 3600000
	Sandbox         string            // sandbox mode, defaults to "read-only"
	ProjectDoc      string            // path to project documentation file
	OutputHandler   func(text string) // called for each filtered output line in real-time
	Debug           bool              // enable debug output
	ErrorPatterns   []string          // patterns to detect in output (e.g., rate limit messages)
	LimitPatterns   []string          // patterns to detect rate limits (checked before error patterns)
	MultiAgent      bool              // enable codex multi_agent feature + reviewer agent registration; set to true on the review-phase codex instance built by processor.New() for first-class --codex mode
	PassClaudeMd    bool              // pass project-level CLAUDE.md to codex via project_doc_fallback_filenames (set by processor.New() only when cfg.AppConfig.Executor == ExecutorCodex)
	IdleTimeout     time.Duration     // kill session after this duration of no output, zero = disabled
	runner          CodexRunner       // for testing, nil uses default
}

// CodexReviewerAgentName is the agent name registered with codex when
// features.multi_agent is enabled. shared with pkg/processor so the
// spawn_agent(agent=...) call in review prompts stays in sync with the
// registration here — if either side drifts, codex silently fails to
// resolve the agent and the review phase breaks.
const CodexReviewerAgentName = "reviewer"

// codexReviewerDescription is the description registered for the reviewer
// agent when features.multi_agent is enabled. behavior is driven by the task
// argument, so the description stays generic and stable.
//
// MUST stay ASCII without backslashes, control characters, or non-printable bytes:
// codexConfigOpts.cliArgs serializes this via fmt.Sprintf("...=%q", ...) which
// emits Go string-literal escapes; only the printable ASCII subset round-trips
// safely through TOML basic-string syntax.
const codexReviewerDescription = "general code review specialist; behavior driven by the task argument"

// configOverrides returns the -c key=value arg slice to splice into the codex CLI
// invocation based on the executor's MultiAgent and PassClaudeMd flags. All overrides
// are additive on top of the user's ~/.codex/config.toml.
func (e *CodexExecutor) configOverrides() []string {
	var args []string
	if e.MultiAgent {
		args = append(args,
			"-c", "features.multi_agent=true",
			"-c", fmt.Sprintf("agents.%s.description=%q", CodexReviewerAgentName, codexReviewerDescription),
		)
	}
	if e.PassClaudeMd {
		args = append(args, "-c", `project_doc_fallback_filenames=["CLAUDE.md"]`)
	}
	return args
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

	args := []string{"exec"}
	args = append(args, e.configOverrides()...)
	if sandbox == "danger-full-access" {
		args = append(args, "--dangerously-bypass-approvals-and-sandbox")
	}
	args = append(args, "--sandbox", sandbox)
	// model and reasoning effort are emitted only when explicitly set in ralphex config,
	// so the user's ~/.codex/config.toml choice is preserved otherwise (matches the
	// "additive -c overrides" promise documented in CLAUDE.md / llms.txt).
	if e.Model != "" {
		args = append(args, "-c", fmt.Sprintf("model=%q", e.Model))
	}
	if e.ReasoningEffort != "" {
		args = append(args, "-c", "model_reasoning_effort="+e.ReasoningEffort)
	}
	args = append(args, "-c", fmt.Sprintf("stream_idle_timeout_ms=%d", timeoutMs))

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

	// set up idle timeout: derive a cancellable context that fires when no output
	// is received for IdleTimeout duration. the touch closure resets the timer on
	// each stderr line and on each stdout read; mirrors the ClaudeExecutor pattern.
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

	streams, wait, err := runner.Run(execCtx, cmd, args...)
	if err != nil {
		return Result{Error: fmt.Errorf("start codex: %w", err)}
	}

	// process stderr for progress display (header block + bold summaries)
	stderrDone := make(chan stderrResult, 1)
	go func() {
		stderrDone <- e.processStderr(execCtx, streams.Stderr, idleTouch)
	}()

	// read stdout entirely as final response; wrap with touch-on-read so reads
	// keep the idle timer alive even while stderr is quiet.
	stdoutReader := streams.Stdout
	if e.IdleTimeout > 0 {
		stdoutReader = &touchReader{r: streams.Stdout, touch: idleTouch}
	}
	stdoutContent, stdoutErr := e.readStdout(stdoutReader)

	// wait for stderr processing to complete
	stderrRes := <-stderrDone

	// wait for command completion
	waitErr := wait()

	// detect signal in stdout (the actual response)
	signal := detectSignal(stdoutContent)

	// idle timeout: derived context canceled but parent is alive — not an error.
	// mirrors the ClaudeExecutor idle-timeout completion path so callers see uniform behavior.
	if e.IdleTimeout > 0 && execCtx.Err() != nil && ctx.Err() == nil {
		e.logDroppedIdleErrors(stdoutErr, waitErr)
		return e.idleTimeoutResult(stdoutContent, signal, stderrRes)
	}

	finalErr := e.finalError(ctx, stderrRes, stdoutErr, waitErr)

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

// finalError reconciles stderr/stdout/wait errors into the single error returned
// from Run. stderr and stdout errors win over wait errors so callers see the
// root cause rather than the cascade exit code; ctx.Err() short-circuits to
// preserve cancellation semantics; non-zero exit with stderr tail produces a
// readable diagnostic that includes the last few stderr lines.
func (e *CodexExecutor) finalError(ctx context.Context, stderrRes stderrResult, stdoutErr, waitErr error) error {
	switch {
	case stderrRes.err != nil && !errors.Is(stderrRes.err, context.Canceled):
		return stderrRes.err
	case stdoutErr != nil:
		return stdoutErr
	case waitErr != nil:
		if ctx.Err() != nil {
			return fmt.Errorf("context error: %w", ctx.Err())
		}
		if len(stderrRes.lastLines) > 0 {
			return fmt.Errorf("codex exited with error: %w\nstderr: %s",
				waitErr, strings.Join(stderrRes.lastLines, "\n"))
		}
		return fmt.Errorf("codex exited with error: %w", waitErr)
	}
	return nil
}

// touchReader wraps an io.Reader to invoke touch on each successful Read.
// used to keep the idle-timeout timer alive while stdout is being drained.
type touchReader struct {
	r     io.Reader
	touch func()
}

func (t *touchReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if n > 0 && t.touch != nil {
		t.touch()
	}
	return n, err //nolint:wrapcheck // pass-through reader; preserve EOF and original error semantics
}

// idleTimeoutResult builds the Result returned when the idle-timeout timer
// canceled the derived execution context (parent ctx still alive). limit and
// logDroppedIdleErrors surfaces concurrent stream/wait errors that would otherwise
// be discarded by the idle-timeout completion path. operators need this to
// distinguish "agent went silent" from "stream broke" before retrying.
func (e *CodexExecutor) logDroppedIdleErrors(stdoutErr, waitErr error) {
	if stdoutErr != nil {
		log.Printf("codex idle timeout fired with concurrent stdout error: %v", stdoutErr)
	}
	if waitErr != nil {
		log.Printf("codex idle timeout fired with concurrent wait error: %v", waitErr)
	}
}

// error patterns are still checked across stdout and stderr so a wait-and-retry
// triggered by a real quota diagnostic survives idle-timeout cancellation;
// otherwise IdleTimedOut is set and the caller treats this as a soft kill.
func (e *CodexExecutor) idleTimeoutResult(stdoutContent, signal string, stderr stderrResult) Result {
	if patternErr := e.checkPatterns(stdoutContent, stderr); patternErr != nil {
		return Result{Output: stdoutContent, Signal: signal, Error: patternErr}
	}
	return Result{Output: stdoutContent, Signal: signal, IdleTimedOut: true}
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
// regardless of how much chatter follows. idleTouch is invoked for every
// stderr line so the idle-timeout timer is reset while codex is producing
// progress output; pass a no-op when idle timeout is disabled.
func (e *CodexExecutor) processStderr(ctx context.Context, r io.Reader, idleTouch func()) stderrResult {
	const maxTailLines = 5    // keep last N lines for error context
	const maxLineLength = 256 // truncate long lines to avoid oversized error strings

	state := &codexFilterState{}
	var tail []string
	var limitMatch, errorMatch string

	err := readLines(ctx, r, func(line string) {
		if idleTouch != nil {
			idleTouch() // reset idle timer on every stderr line
		}
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
