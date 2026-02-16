// Package input provides terminal input collection for interactive plan creation.
package input

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/pmezard/go-difflib/difflib"
)

// readLineResult holds the result of reading a line
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
	// An "Other" option is appended automatically; if chosen, the user types a free-text answer.
	// Returns the selected or typed text, or error if selection fails.
	AskQuestion(ctx context.Context, question string, options []string) (string, error)

	// AskDraftReview presents a plan draft for review with Accept/Revise/Interactive review/Reject options.
	// Returns the selected action ("accept", "revise", or "reject") and feedback text (empty for accept/reject).
	AskDraftReview(ctx context.Context, question string, planContent string) (action string, feedback string, err error)
}

// TerminalCollector implements Collector using fzf (if available) or numbered selection fallback.
type TerminalCollector struct {
	stdin      io.Reader                                                 // for testing, nil uses os.Stdin
	stdout     io.Writer                                                 // for testing, nil uses os.Stdout
	editorFunc func(ctx context.Context, content string) (string, error) // for testing, nil uses real editor
	noColor    bool                                                      // if true, skip glamour rendering
	noFzf      bool                                                      // if true, skip fzf even if available (for testing)
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

// otherOption is the sentinel value appended to option lists for custom answers.
const otherOption = "Other (type your own answer)"

// AskQuestion presents options using fzf if available, otherwise falls back to numbered selection.
func (c *TerminalCollector) AskQuestion(ctx context.Context, question string, options []string) (string, error) {
	if len(options) == 0 {
		return "", errors.New("no options provided")
	}

	// append "Other" option so the user can type a custom answer.
	// filter out any incoming option matching the sentinel to avoid collision
	// (options are model-generated and could theoretically contain it).
	opts := make([]string, 0, len(options)+1)
	for _, o := range options {
		if o != otherOption {
			opts = append(opts, o)
		}
	}
	opts = append(opts, otherOption)

	// try fzf first
	if c.hasFzf() {
		return c.selectWithFzf(ctx, question, opts)
	}

	// fallback to numbered selection
	return c.selectWithNumbers(ctx, question, opts, nil)
}

// hasFzf checks if fzf is available in PATH.
func (c *TerminalCollector) hasFzf() bool {
	if c.noFzf {
		return false
	}
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
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			switch exitErr.ExitCode() {
			case 130: // user pressed Escape
				return "", errors.New("selection canceled")
			case 1: // no match found — fall back to custom answer
				return c.readCustomAnswer(ctx, nil)
			}
		}
		return "", fmt.Errorf("fzf selection failed: %w", err)
	}

	selected := strings.TrimSpace(string(output))
	if selected == "" {
		return "", errors.New("no selection made")
	}

	if selected == otherOption {
		return c.readCustomAnswer(ctx, nil)
	}

	return selected, nil
}

// selectWithNumbers presents numbered options for selection via stdin.
// when reader is provided, it reuses the existing bufio.Reader to avoid data loss
// with piped input (creating a second bufio.NewReader on the same io.Reader would
// lose data already buffered by the first reader).
func (c *TerminalCollector) selectWithNumbers(ctx context.Context, question string, options []string, reader *bufio.Reader) (string, error) {
	stdout := c.getStdout()

	// print question and options
	_, _ = fmt.Fprintln(stdout)
	_, _ = fmt.Fprintln(stdout, question)
	for i, opt := range options {
		_, _ = fmt.Fprintf(stdout, "  %d) %s\n", i+1, opt)
	}
	_, _ = fmt.Fprintf(stdout, "Enter number (1-%d): ", len(options))

	// reuse provided reader or create a new one
	r := reader
	if r == nil {
		r = bufio.NewReader(c.getStdin())
	}
	line, err := ReadLineWithContext(ctx, r)
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

	selected := options[num-1]
	if selected == otherOption {
		return c.readCustomAnswer(ctx, r)
	}

	return selected, nil
}

// readCustomAnswer prompts the user for free-text input and returns the answer.
// when reader is provided, it reuses the existing bufio.Reader to avoid data loss
// with piped input (creating a second bufio.NewReader on the same io.Reader would
// lose data already buffered by the first reader).
func (c *TerminalCollector) readCustomAnswer(ctx context.Context, reader *bufio.Reader) (string, error) {
	stdout := c.getStdout()

	_, _ = fmt.Fprint(stdout, "Enter your answer: ")

	r := reader
	if r == nil {
		r = bufio.NewReader(c.getStdin())
	}
	line, err := ReadLineWithContext(ctx, r)
	if err != nil {
		return "", fmt.Errorf("read custom answer: %w", err)
	}

	answer := strings.TrimSpace(line)
	if answer == "" {
		return "", errors.New("custom answer cannot be empty")
	}

	return answer, nil
}

// AskYesNo prompts with [y/N] and returns true for yes.
// defaults to no on EOF, empty input, context cancellation, or any read error.
func AskYesNo(ctx context.Context, prompt string, stdin io.Reader, stdout io.Writer) bool {
	fmt.Fprintf(stdout, "%s [y/N]: ", prompt)
	reader := bufio.NewReader(stdin)
	line, err := ReadLineWithContext(ctx, reader)
	if err != nil {
		fmt.Fprintln(stdout) // newline so subsequent output doesn't appear on the same line
		if !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			log.Printf("[WARN] input read error, defaulting to 'no': %v", err)
		}
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

	actionInteractiveReview = "interactive review" // unexported, never crosses package boundary
)

// AskDraftReview presents a plan draft for review with Accept/Revise/Interactive review/Reject options.
// Shows the rendered plan content, then prompts for action selection.
// If Revise is selected, prompts for feedback text.
// If Interactive review is selected, opens $EDITOR for direct editing and computes a diff.
// If the editor fails or produces no changes, the menu is re-shown.
// Returns action ("accept", "revise", "reject") and feedback (empty for accept/reject).
func (c *TerminalCollector) AskDraftReview(ctx context.Context, question, planContent string) (string, string, error) {
	stdout := c.getStdout()

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

	// create reader once to avoid losing buffered data when looping
	reader := bufio.NewReader(c.getStdin())

	options := []string{"Accept", "Revise", "Interactive review", "Reject"}

	for {
		action, selectErr := c.selectWithNumbers(ctx, question, options, reader)
		if selectErr != nil {
			return "", "", fmt.Errorf("select action: %w", selectErr)
		}

		actionLower := strings.ToLower(action)

		switch actionLower {
		case ActionAccept, ActionReject:
			return actionLower, "", nil

		case ActionRevise:
			_, _ = fmt.Fprintln(stdout)
			_, _ = fmt.Fprint(stdout, "Enter revision feedback: ")

			feedback, readErr := ReadLineWithContext(ctx, reader)
			if readErr != nil {
				return "", "", fmt.Errorf("read feedback: %w", readErr)
			}
			feedback = strings.TrimSpace(feedback)
			if feedback == "" {
				return "", "", errors.New("revision feedback cannot be empty")
			}
			return ActionRevise, feedback, nil

		case actionInteractiveReview:
			edited, editorErr := c.openEditor(ctx, planContent)
			if editorErr != nil {
				log.Printf("[WARN] editor failed, returning to menu: %v", editorErr)
				continue // re-show menu
			}

			diff, diffErr := c.computeDiff(planContent, edited)
			if diffErr != nil {
				log.Printf("[WARN] diff computation failed, returning to menu: %v", diffErr)
				continue // re-show menu
			}

			if diff == "" {
				_, _ = fmt.Fprintln(stdout, "no changes detected, returning to menu")
				continue // re-show menu
			}

			// wrap diff with instructions so Claude knows how to interpret the annotations
			feedback := "user reviewed the plan in an editor and made changes. " +
				"the diff below shows what the user modified (lines starting with - are original, + are user's version).\n" +
				"examine each diff hunk to understand the user's feedback:\n" +
				"- added lines (+) are user's annotations, comments, or requested additions\n" +
				"- removed lines (-) with replacement (+) show what the user wants changed\n" +
				"- removed lines (-) without replacement mean the user wants that removed\n" +
				"- context lines (no prefix) show surrounding plan content for reference\n\n" +
				diff
			return ActionRevise, feedback, nil

		default:
			return "", "", fmt.Errorf("unexpected action: %s", actionLower)
		}
	}
}

// openEditor launches the user's editor with the given content in a temp file.
// returns the edited content after the editor exits. the temp file is cleaned up automatically.
// if editorFunc is set, it is used instead of launching a real editor (for testing).
// editor lookup order: $VISUAL -> $EDITOR -> vi.
func (c *TerminalCollector) openEditor(ctx context.Context, content string) (string, error) {
	if c.editorFunc != nil {
		return c.editorFunc(ctx, content)
	}

	// create temp file with .md extension for markdown syntax highlighting
	tmpFile, err := os.CreateTemp("", "ralphex-plan-*.md")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, writeErr := tmpFile.WriteString(content); writeErr != nil {
		tmpFile.Close()
		return "", fmt.Errorf("write temp file: %w", writeErr)
	}
	if closeErr := tmpFile.Close(); closeErr != nil {
		return "", fmt.Errorf("close temp file: %w", closeErr)
	}

	// look up editor: $VISUAL -> $EDITOR -> vi
	editor := strings.TrimSpace(os.Getenv("VISUAL"))
	if editor == "" {
		editor = strings.TrimSpace(os.Getenv("EDITOR"))
	}
	if editor == "" {
		editor = "vi"
	}

	// split editor string to support arguments (e.g., "code --wait", "vim -u NONE")
	parts := strings.Fields(editor)
	editorPath, lookErr := exec.LookPath(parts[0])
	if lookErr != nil {
		return "", fmt.Errorf("editor %q not found, set $EDITOR environment variable: %w", parts[0], lookErr)
	}

	args := append([]string{}, parts[1:]...)
	args = append(args, tmpPath)
	cmd := exec.CommandContext(ctx, editorPath, args...) //nolint:gosec // editor is user-configured
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if runErr := cmd.Run(); runErr != nil {
		return "", fmt.Errorf("run editor: %w", runErr)
	}

	edited, readErr := os.ReadFile(tmpPath) //nolint:gosec // path is from our own temp file
	if readErr != nil {
		return "", fmt.Errorf("read edited file: %w", readErr)
	}

	return string(edited), nil
}

// computeDiff computes a unified diff between original and edited content.
// returns empty string if contents are identical.
func (c *TerminalCollector) computeDiff(original, edited string) (string, error) {
	diff := difflib.UnifiedDiff{
		A:        difflib.SplitLines(original),
		B:        difflib.SplitLines(edited),
		FromFile: "original",
		ToFile:   "annotated",
		Context:  2,
	}
	result, err := difflib.GetUnifiedDiffString(diff)
	if err != nil {
		return "", fmt.Errorf("compute diff: %w", err)
	}
	return result, nil
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
