// Package input provides terminal input collection for interactive plan creation.
package input

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/charmbracelet/glamour"
)

// ReadLineResult holds the result of reading a line
type readLineResult struct {
	line string
	err  error
}

// ReadLineWithContext reads a line from reader with context cancellation support.
// returns the line (including newline), error, or context error if canceled.
// this allows Ctrl+C (SIGINT) to interrupt blocking stdin reads.
func ReadLineWithContext(ctx context.Context, reader *bufio.Reader) (string, error) {
	resultCh := make(chan readLineResult, 1)

	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("read line: %w", err)
	}

	go func() {
		line, err := reader.ReadString('\n')
		resultCh <- readLineResult{line: line, err: err}
	}()

	select {
	case <-ctx.Done():
		return "", fmt.Errorf("read line: %w", ctx.Err())
	case result := <-resultCh:
		return result.line, result.err
	}
}

//go:generate moq -out mocks/collector.go -pkg mocks -skip-ensure -fmt goimports . Collector

// Collector provides interactive input collection for plan creation.
type Collector interface {
	// AskQuestion presents a question with options and returns the selected answer.
	// Returns the selected option text or error if selection fails.
	AskQuestion(ctx context.Context, question string, options []string) (string, error)

	// AskDraftReview presents a plan draft for review with Accept/Revise/Reject options.
	// Returns the selected action ("accept", "revise", or "reject") and feedback text (empty for accept/reject).
	AskDraftReview(ctx context.Context, question string, planContent string) (action string, feedback string, err error)
}

// TerminalCollector implements Collector using fzf (if available) or numbered selection fallback.
type TerminalCollector struct {
	stdin   io.Reader // for testing, nil uses os.Stdin
	stdout  io.Writer // for testing, nil uses os.Stdout
	noColor bool      // if true, skip glamour rendering
}

// NewTerminalCollector creates a new TerminalCollector with specified options.
func NewTerminalCollector(noColor bool) *TerminalCollector {
	return &TerminalCollector{noColor: noColor}
}

func (c *TerminalCollector) getStdin() io.Reader {
	if c.stdin != nil {
		return c.stdin
	}
	return os.Stdin
}

func (c *TerminalCollector) getStdout() io.Writer {
	if c.stdout != nil {
		return c.stdout
	}
	return os.Stdout
}

// AskQuestion presents options using fzf if available, otherwise falls back to numbered selection.
func (c *TerminalCollector) AskQuestion(ctx context.Context, question string, options []string) (string, error) {
	if len(options) == 0 {
		return "", errors.New("no options provided")
	}

	// try fzf first
	if hasFzf() {
		return c.selectWithFzf(ctx, question, options)
	}

	// fallback to numbered selection
	return c.selectWithNumbers(ctx, question, options)
}

// hasFzf checks if fzf is available in PATH.
func hasFzf() bool {
	_, err := exec.LookPath("fzf")
	return err == nil
}

// selectWithFzf uses fzf for interactive selection.
func (c *TerminalCollector) selectWithFzf(ctx context.Context, question string, options []string) (string, error) {
	input := strings.Join(options, "\n")

	cmd := exec.CommandContext(ctx, "fzf", "--prompt", question+": ", "--height", "10", "--layout=reverse") //nolint:gosec // fzf is a trusted external tool, question is user-provided prompt text
	cmd.Stdin = strings.NewReader(input)
	cmd.Stderr = os.Stderr

	output, err := cmd.Output()
	if err != nil {
		// fzf returns exit code 130 when user presses Escape
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 130 {
			return "", errors.New("selection canceled")
		}
		return "", fmt.Errorf("fzf selection failed: %w", err)
	}

	selected := strings.TrimSpace(string(output))
	if selected == "" {
		return "", errors.New("no selection made")
	}

	return selected, nil
}

// selectWithNumbers presents numbered options for selection via stdin.
func (c *TerminalCollector) selectWithNumbers(ctx context.Context, question string, options []string) (string, error) {
	stdout := c.getStdout()
	stdin := c.getStdin()

	// print question and options
	_, _ = fmt.Fprintln(stdout)
	_, _ = fmt.Fprintln(stdout, question)
	for i, opt := range options {
		_, _ = fmt.Fprintf(stdout, "  %d) %s\n", i+1, opt)
	}
	_, _ = fmt.Fprintf(stdout, "Enter number (1-%d): ", len(options))

	// read selection
	reader := bufio.NewReader(stdin)
	line, err := ReadLineWithContext(ctx, reader)
	if err != nil {
		return "", fmt.Errorf("read input: %w", err)
	}

	// parse selection
	line = strings.TrimSpace(line)
	num, err := strconv.Atoi(line)
	if err != nil {
		return "", fmt.Errorf("invalid number: %s", line)
	}

	if num < 1 || num > len(options) {
		return "", fmt.Errorf("selection out of range: %d (must be 1-%d)", num, len(options))
	}

	return options[num-1], nil
}

// AskYesNo prompts with [y/N] and returns true for yes.
// defaults to no on EOF, empty input, context cancellation, or any read error.
func AskYesNo(ctx context.Context, prompt string, stdin io.Reader, stdout io.Writer) bool {
	fmt.Fprintf(stdout, "%s [y/N]: ", prompt)
	reader := bufio.NewReader(stdin)
	line, err := ReadLineWithContext(ctx, reader)
	if err != nil {
		// EOF (Ctrl+D), context canceled (Ctrl+C), or read error
		// print newline so subsequent output doesn't appear on the same line
		fmt.Fprintln(stdout)
		return false
	}
	answer := strings.TrimSpace(strings.ToLower(line))
	return answer == "y" || answer == "yes"
}

// draft review action constants
const (
	ActionAccept = "accept"
	ActionRevise = "revise"
	ActionReject = "reject"
)

// AskDraftReview presents a plan draft for review with Accept/Revise/Reject options.
// Shows the rendered plan content, then prompts for action selection.
// If Revise is selected, prompts for feedback text.
// Returns action ("accept", "revise", "reject") and feedback (empty for accept/reject).
func (c *TerminalCollector) AskDraftReview(ctx context.Context, question, planContent string) (string, string, error) {
	stdout := c.getStdout()
	stdin := c.getStdin()

	// render and display the plan
	rendered, err := c.renderMarkdown(planContent)
	if err != nil {
		return "", "", fmt.Errorf("render plan: %w", err)
	}

	_, _ = fmt.Fprintln(stdout)
	_, _ = fmt.Fprintln(stdout, "━━━ Plan Draft ━━━")
	_, _ = fmt.Fprintln(stdout, rendered)
	_, _ = fmt.Fprintln(stdout, "━━━━━━━━━━━━━━━━━━")
	_, _ = fmt.Fprintln(stdout)

	// present action options
	options := []string{"Accept", "Revise", "Reject"}
	action, err := c.selectWithNumbers(ctx, question, options)
	if err != nil {
		return "", "", fmt.Errorf("select action: %w", err)
	}

	actionLower := strings.ToLower(action)

	// if revise, prompt for feedback
	if actionLower == ActionRevise {
		_, _ = fmt.Fprintln(stdout)
		_, _ = fmt.Fprint(stdout, "Enter revision feedback: ")

		reader := bufio.NewReader(stdin)
		feedback, readErr := ReadLineWithContext(ctx, reader)
		if readErr != nil {
			return "", "", fmt.Errorf("read feedback: %w", readErr)
		}
		feedback = strings.TrimSpace(feedback)
		if feedback == "" {
			return "", "", errors.New("revision feedback cannot be empty")
		}
		return ActionRevise, feedback, nil
	}

	return actionLower, "", nil
}

// renderMarkdown renders markdown content for terminal display.
// if noColor is true, returns the content unchanged.
func (c *TerminalCollector) renderMarkdown(content string) (string, error) {
	if c.noColor {
		return content, nil
	}
	renderer, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(80),
	)
	if err != nil {
		return "", fmt.Errorf("create renderer: %w", err)
	}
	result, err := renderer.Render(content)
	if err != nil {
		return "", fmt.Errorf("render markdown: %w", err)
	}
	return result, nil
}
