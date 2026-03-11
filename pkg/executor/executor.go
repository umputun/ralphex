// Package executor provides CLI execution for Copilot and custom review tools.
package executor

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

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
	HelpCmd string // command to run for more information
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

// execRunner is the default command runner using os/exec.
type execRunner struct{}

func (r *execRunner) Run(ctx context.Context, name string, args ...string) (io.Reader, func() error, error) {
	// check context before starting to avoid spawning a process that will be immediately killed
	if err := ctx.Err(); err != nil {
		return nil, nil, fmt.Errorf("context already canceled: %w", err)
	}

	// use exec.Command (not CommandContext) because we handle cancellation ourselves
	// to ensure the entire process group is killed, not just the direct child
	cmd := exec.Command(name, args...) //nolint:noctx // intentional: we handle context cancellation via process group kill

	// filter out env vars that could interfere with copilot
	cmd.Env = filterEnv(os.Environ(), "CLAUDECODE")

	// create new process group so we can kill all descendants on cleanup
	setupProcessGroup(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("create stdout pipe: %w", err)
	}
	// capture stderr separately for CLI-level errors (copilot sends JSONL to stdout only)
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
