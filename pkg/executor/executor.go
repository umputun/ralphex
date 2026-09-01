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
	Output         string // complete surfaced text output
	RecentText     string // bounded recent surfaced text retained for display/debug compatibility
	DiagnosticText string // bounded trusted CLI diagnostics used for pattern matching
	Signal         string // detected signal (COMPLETED, FAILED, etc.) or empty
	Error          error  // execution error if any
	IdleTimedOut   bool   // true when idle timeout fired (derived context canceled, parent alive)
}

// recentBlockCount bounds both recent surfaced output and trusted diagnostics.
// keeping the windows small avoids retaining stale failures from long sessions.
const recentBlockCount = 10

// textWindow retains text blocks in chronological order with a fixed memory bound.
type textWindow struct {
	blocks [recentBlockCount]string
	next   int
}

func (w *textWindow) add(text string) {
	if text == "" {
		return
	}
	w.blocks[w.next%recentBlockCount] = text
	w.next++
}

func (w *textWindow) string() string {
	var result strings.Builder
	start := w.next % recentBlockCount
	for i := range recentBlockCount {
		block := w.blocks[(start+i)%recentBlockCount]
		if block != "" {
			result.WriteString(block)
			result.WriteString("\n")
		}
	}
	return result.String()
}

// subagentProgressInterval throttles subagent (Task tool) heartbeat lines: at most
// one is forwarded per interval so a burst of tool steps across several parallel
// review agents does not flood the progress stream. a single "still working" line
// every few seconds is enough to show the review is alive. only these synthesized
// heartbeat lines are throttled — the model's own text output is never dropped.
const subagentProgressInterval = 10 * time.Second

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

// RetryPatternError is returned when a configured transient retry pattern is detected in output.
// The processor maps it to existing timeout-style phase retries instead of rate-limit waiting.
type RetryPatternError struct {
	Pattern string // the pattern that matched
}

func (e *RetryPatternError) Error() string {
	return fmt.Sprintf("detected retry pattern: %q", e.Pattern)
}

// CommandRunner abstracts command execution for testing.
// Returns an io.Reader for streaming output and a wait function for completion.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) (output io.Reader, wait func() error, err error)
}

// execClaudeRunner is the default command runner using os/exec.
// when stdin is non-nil, it is connected to the child process's stdin (used to pass
// the prompt via pipe instead of a -p CLI argument to avoid Windows 8191-char cmd limit).
// preserveAPIKey, when true, leaves ANTHROPIC_API_KEY intact in the child env (for users
// who authenticate Claude Code via API key rather than OAuth/keychain).
type execClaudeRunner struct {
	stdin          io.Reader
	preserveAPIKey bool
}

func (r *execClaudeRunner) Run(ctx context.Context, name string, args ...string) (io.Reader, func() error, error) {
	// check context before starting to avoid spawning a process that will be immediately killed
	if err := ctx.Err(); err != nil {
		return nil, nil, fmt.Errorf("context already canceled: %w", err)
	}

	// use exec.Command (not CommandContext) because we handle cancellation ourselves
	// to ensure the entire process group is killed, not just the direct child
	cmd := exec.Command(name, args...) //nolint:noctx // intentional: we handle context cancellation via process group kill

	// build child env: always strip CLAUDECODE (prevents nested session errors); strip
	// ANTHROPIC_API_KEY by default so a host-set key cannot silently override OAuth/keychain
	// auth and bill a different account. preserveAPIKey opts into keeping the key for users
	// who authenticate Claude Code via API key.
	cmd.Env = claudeChildEnv(os.Environ(), r.preserveAPIKey)

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

// stripFlag removes all occurrences of a flag and its value from args. Handles three forms:
// "--flag value" (space-separated, skips next element only if it doesn't look like another flag),
// "--flag=value" (single token), and a bare "--flag" with no following value. Returns a new slice.
// The "looks like another flag" heuristic (starts with "-") preserves unrelated flags that happen
// to follow a malformed bare "--flag" in the middle of args.
func stripFlag(args []string, flag string) []string {
	prefix := flag + "="
	result := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == flag {
			// space form: skip the next token only if it's an actual value, not another flag
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
			}
			continue
		}
		if strings.HasPrefix(args[i], prefix) {
			// equals form: drop the single token "--flag=value"
			continue
		}
		result = append(result, args[i])
	}
	return result
}

// claudeChildEnv builds the environment for a child claude process. CLAUDECODE is always
// stripped to prevent nested-session errors. ANTHROPIC_API_KEY is stripped unless
// preserveAPIKey is true; preserving it is required for users who authenticate Claude Code
// via API key rather than OAuth/keychain.
func claudeChildEnv(env []string, preserveAPIKey bool) []string {
	if preserveAPIKey {
		return filterEnv(env, "CLAUDECODE")
	}
	return filterEnv(env, "ANTHROPIC_API_KEY", "CLAUDECODE")
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
	Type              string `json:"type"`
	Subtype           string `json:"subtype"` // for "system" events: init, task_started, task_progress, etc.
	IsError           bool   `json:"is_error"`
	Error             string `json:"error"`
	ErrorStatus       int    `json:"error_status"`
	APIErrorStatus    int    `json:"api_error_status"`
	IsAPIErrorMessage bool   `json:"is_api_error_message"`
	TerminalReason    string `json:"terminal_reason"`
	Message           struct {
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
	// subagent (Task tool) progress: newer Claude Code streams subagent activity as
	// system/task_started (the agent's task title) and system/task_progress (per step)
	// events whose description names the action; the subagent type is intentionally
	// not surfaced (stock config runs every review agent as "general-purpose").
	Description string `json:"description"` // task title / current step, e.g. "Running tests"
}

// ClaudeExecutor runs claude CLI commands with streaming JSON parsing.
type ClaudeExecutor struct {
	Command        string            // command to execute, defaults to "claude"
	Args           string            // additional arguments (space-separated), defaults to standard args
	ArgsSet        bool              // true when Args was explicitly set, including an empty value
	Model          string            // model override (e.g., "fable", "opus", "sonnet", "haiku"); empty = CLI default
	Effort         string            // reasoning effort override (e.g., "low", "medium", "high", "xhigh", "max"); empty = CLI default
	OutputHandler  func(text string) // called for each text chunk, can be nil
	Debug          bool              // enable debug output
	ErrorPatterns  []string          // patterns to detect in output (e.g., rate limit messages)
	LimitPatterns  []string          // patterns to detect rate limits (checked before error patterns)
	RetryPatterns  []string          // patterns to detect transient errors that should retry like timeouts
	IdleTimeout    time.Duration     // kill session after this duration of no output, zero = disabled
	PreserveAPIKey bool              // when true, ANTHROPIC_API_KEY is passed through to the child; default false strips it
	cmdRunner      CommandRunner     // for testing, nil uses default
	nowFn          func() time.Time  // for testing throttle timing, nil uses time.Now
}

// now returns the current time, using the injected nowFn when set (tests) or
// time.Now otherwise.
func (e *ClaudeExecutor) now() time.Time {
	if e.nowFn != nil {
		return e.nowFn()
	}
	return time.Now()
}

// Run executes claude CLI with the given prompt and parses streaming JSON output.
func (e *ClaudeExecutor) Run(ctx context.Context, prompt string) Result {
	cmd := e.Command
	if cmd == "" {
		cmd = "claude"
	}

	// build args from configured string or use defaults
	var args []string
	switch {
	case e.ArgsSet:
		args = splitArgs(e.Args)
	case e.Args != "":
		args = splitArgs(e.Args)
	default:
		args = []string{
			"--dangerously-skip-permissions",
			"--output-format", "stream-json",
			"--verbose",
		}
	}
	// inject --model flag if a model override is configured;
	// strip any existing --model from args to avoid duplicate/conflicting flags
	if e.Model != "" {
		args = stripFlag(args, "--model")
		args = append(args, "--model", e.Model)
	}
	// inject --effort flag if an effort override is configured;
	// strip any existing --effort from args to avoid duplicate/conflicting flags
	if e.Effort != "" {
		args = stripFlag(args, "--effort")
		args = append(args, "--effort", e.Effort)
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
		runner = &execClaudeRunner{stdin: stdinReader, preserveAPIKey: e.PreserveAPIKey}
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

	return e.resolveRunResult(result, claudeRunOutcome{
		waitErr:      waitErr,
		parentErr:    ctx.Err(),
		idleTimedOut: e.IdleTimeout > 0 && execCtx.Err() != nil && ctx.Err() == nil,
	})
}

// claudeRunOutcome records process state that is not available while parsing the
// stream. resolveRunResult combines it with parsed provenance in one decision path.
type claudeRunOutcome struct {
	waitErr      error
	parentErr    error
	idleTimedOut bool
}

// resolveRunResult applies cancellation, pattern, idle-timeout, and process-exit
// semantics in precedence order. trusted diagnostics are always pattern authority.
// bounded surfaced text becomes authority only when the process itself failed, which
// keeps limit classification working for the bundled wrapper scripts: they emit result
// records carrying no is_error field and surface child stderr as ordinary deltas.
func (e *ClaudeExecutor) resolveRunResult(result Result, outcome claudeRunOutcome) Result {
	// explicit parent cancellation must never be masked by output patterns.
	if outcome.waitErr != nil && outcome.parentErr != nil {
		result.Error = outcome.parentErr
		result.IdleTimedOut = false
		return result
	}

	// signal absence is deliberately NOT part of the promotion rule: a successful run
	// often emits no marker by design (review_first.txt path B stays silent after fixing
	// issues, task.txt emits ALL_TASKS_DONE only once the plan has no boxes left), so
	// treating "no signal" as failure evidence would readmit narration on clean runs. an
	// idle timeout cancels the stream context, so it reaches this as a stream error.
	patternText := result.DiagnosticText
	if outcome.waitErr != nil || result.Error != nil {
		patternText += "\n" + result.RecentText
	}
	if patternErr := e.patternError(patternText, result.Signal); patternErr != nil {
		result.Error = patternErr
		result.IdleTimedOut = false
		return result
	}

	// a derived idle cancellation is a soft outcome. clear the stream's context
	// error and let the caller distinguish the timeout from normal completion.
	if outcome.idleTimedOut {
		result.Error = nil
		result.IdleTimedOut = true
		return result
	}

	// a non-zero exit without a structured signal remains a process failure. when
	// a signal is present, useful work won and only an independent stream error is
	// retained, matching the historical fallback behavior.
	if outcome.waitErr != nil && result.Signal == "" {
		result.Error = fmt.Errorf("claude exited with error: %w", outcome.waitErr)
	}

	return result
}

func (e *ClaudeExecutor) patternError(patternText, signal string) error {
	// a non-empty signal means claude reported a structured outcome (completion, review-done,
	// etc). a stray retry marker in the output must not discard that by forcing a session
	// re-run, so retry detection is skipped when a signal is present. limit and error patterns
	// still fire — they surface loudly instead of silently re-running, so they cannot drop work.
	if signal == "" {
		if pattern := matchPattern(patternText, e.RetryPatterns); pattern != "" {
			return &RetryPatternError{Pattern: pattern}
		}
	}
	if pattern := matchPattern(patternText, e.LimitPatterns); pattern != "" {
		return &LimitPatternError{Pattern: pattern, HelpCmd: "claude /usage"}
	}
	if pattern := matchPattern(patternText, e.ErrorPatterns); pattern != "" {
		return &PatternMatchError{Pattern: pattern, HelpCmd: "claude /usage"}
	}
	return nil
}

// parseStream reads and parses the JSON stream from claude CLI.
// uses readLines internally, so there is no line length limit.
// checks ctx.Done() between reads so cancellation is not blocked by slow pipe reads.
// idleTouch resets the idle timer on each line of output; pass no-op when idle timeout is disabled.
func (e *ClaudeExecutor) parseStream(ctx context.Context, r io.Reader, idleTouch func()) Result {
	var output strings.Builder
	var signal string
	var recentText textWindow
	var diagnostics textWindow
	var lastProgress time.Time // throttle window for subagent heartbeat lines

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
			recentText.add(line)
			diagnostics.add(line)
			if e.OutputHandler != nil {
				e.OutputHandler(line + "\n")
			}
			return
		}

		// surface subagent (Task tool) progress. newer Claude Code streams subagent
		// activity as system/task_* events that carry no text block, so extractText
		// drops them; without this the parent session appears silent for the whole
		// duration of a multi-agent review. forwarded to OutputHandler only — not
		// accumulated into output/recentText/signal, which track the model's own text.
		// task_started (title) is unthrottled; per-step task_progress is throttled so
		// parallel agents don't flood.
		if hb, throttle := e.subagentLine(&event); hb != "" {
			if e.OutputHandler == nil {
				return
			}
			if throttle {
				if now := e.now(); now.Sub(lastProgress) >= subagentProgressInterval {
					lastProgress = now
					e.OutputHandler(hb)
				}
				return
			}
			e.OutputHandler(hb)
			return
		}

		text := e.extractText(&event)
		if text != "" {
			output.WriteString(text)
			if e.OutputHandler != nil {
				e.OutputHandler(text)
			}

			// retain surfaced output separately from trusted pattern input. ordinary
			// assistant text belongs only in the display/debug window.
			recentText.add(text)

			// check for signals in text
			if sig := detectSignal(text); sig != "" {
				signal = sig
			}
		}

		diagnostics.add(e.extractDiagnostic(&event))
	})

	if err != nil {
		return Result{Output: output.String(), RecentText: recentText.string(),
			DiagnosticText: diagnostics.string(), Signal: signal, Error: fmt.Errorf("stream read: %w", err)}
	}

	return Result{Output: output.String(), RecentText: recentText.string(),
		DiagnosticText: diagnostics.string(), Signal: signal}
}

// extractDiagnostic returns pattern input from structured Claude events. normal
// assistant messages and successful result summaries are deliberately excluded,
// even when they quote a configured error phrase.
func (e *ClaudeExecutor) extractDiagnostic(event *streamEvent) string {
	switch event.Type {
	case "assistant":
		if event.IsAPIErrorMessage || event.Error != "" {
			if text := e.extractText(event); text != "" {
				return text
			}
			return formatStructuredError(event.APIErrorStatus, event.Error, event.Description)
		}
	case "result":
		// a string-valued result is normally a session summary that extractText drops,
		// so error prose arriving only there would be lost. capture it, but only when the
		// record authenticates itself as an error: treating every result string as
		// diagnostic would readmit the quoted-summary false positive.
		if isErrorResult(event) {
			if text := extractResultText(event.Result); text != "" {
				return text
			}
			return formatStructuredError(event.APIErrorStatus, event.Error, event.Description)
		}
	case "system":
		if strings.Contains(event.Subtype, "error") {
			return formatStructuredError(event.ErrorStatus, event.Error, event.Description)
		}
	case "error":
		if text := extractResultText(event.Result); text != "" {
			return text
		}
		statusCode := event.APIErrorStatus
		if statusCode == 0 {
			statusCode = event.ErrorStatus
		}
		return formatStructuredError(statusCode, event.Error, event.Description)
	}
	return ""
}

// isErrorResult reports whether a result record authenticates itself as an error.
// terminal_reason alone does not qualify — only the api_error value does, because a
// successful result can carry a terminal reason of its own. subtype is never consulted:
// Claude Code has been observed emitting subtype:"success" on a genuine rate-limit
// result, so is_error and the error fields are the authoritative signals.
func isErrorResult(event *streamEvent) bool {
	return event.IsError || event.TerminalReason == "api_error" || event.APIErrorStatus != 0
}

func formatStructuredError(statusCode int, parts ...string) string {
	var result []string
	if statusCode != 0 {
		result = append(result, fmt.Sprintf("API Error: %d", statusCode))
	}
	for _, part := range parts {
		if part != "" {
			result = append(result, part)
		}
	}
	return strings.Join(result, " ")
}

// subagentLine formats a one-line heartbeat for a subagent (Task tool) system
// event and reports whether the line should be throttled, or "" for events with
// no surfaced progress. newer Claude Code streams subagent activity as system
// task_* events whose payload carries no text block; surfacing the description
// keeps the parent session from appearing silent while a multi-agent review runs.
// task_started (the agent's task title) is unthrottled; task_progress (per step)
// is throttled by the caller. the subagent type and tool name are intentionally
// omitted — the description already names the action, and stock config runs every
// review agent as "general-purpose" so a "[general-purpose]" prefix is just noise.
func (e *ClaudeExecutor) subagentLine(event *streamEvent) (line string, throttle bool) {
	if event.Type != "system" || event.Description == "" {
		return "", false
	}
	switch event.Subtype {
	case "task_started":
		return "  " + event.Description + "\n", false
	case "task_progress":
		return "  " + event.Description + "\n", true
	default:
		return "", false
	}
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
		// string results are session summaries whose content already streamed
		var resultStr string
		if err := json.Unmarshal(event.Result, &resultStr); err == nil {
			return "" // skip session summary - content already streamed
		}
		return extractResultText(event.Result)
	}
	return ""
}

func extractResultText(result json.RawMessage) string {
	if len(result) == 0 {
		return ""
	}
	var resultStr string
	if err := json.Unmarshal(result, &resultStr); err == nil {
		return resultStr
	}
	var resultObj struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal(result, &resultObj); err == nil {
		return resultObj.Output
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
