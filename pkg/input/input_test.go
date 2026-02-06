package input

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTerminalCollector_selectWithNumbers(t *testing.T) {
	tests := []struct {
		name     string
		question string
		options  []string
		input    string
		want     string
		wantErr  string
	}{
		{name: "select first option", question: "Pick one", options: []string{"A", "B", "C"}, input: "1\n", want: "A"},
		{name: "select last option", question: "Pick one", options: []string{"A", "B", "C"}, input: "3\n", want: "C"},
		{name: "select middle option", question: "Pick one", options: []string{"A", "B", "C"}, input: "2\n", want: "B"},
		{name: "input with spaces", question: "Pick one", options: []string{"A", "B"}, input: "  2  \n", want: "B"},
		{name: "out of range high", question: "Pick one", options: []string{"A", "B"}, input: "5\n", wantErr: "out of range"},
		{name: "out of range zero", question: "Pick one", options: []string{"A", "B"}, input: "0\n", wantErr: "out of range"},
		{name: "negative number", question: "Pick one", options: []string{"A", "B"}, input: "-1\n", wantErr: "out of range"},
		{name: "invalid input", question: "Pick one", options: []string{"A", "B"}, input: "abc\n", wantErr: "invalid number"},
		{name: "empty input", question: "Pick one", options: []string{"A", "B"}, input: "\n", wantErr: "invalid number"},
		{name: "single option", question: "Only one", options: []string{"OnlyOption"}, input: "1\n", want: "OnlyOption"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			c := &TerminalCollector{stdin: strings.NewReader(tc.input), stdout: &stdout}

			got, err := c.selectWithNumbers(context.Background(), tc.question, tc.options)

			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)

			// verify output format
			output := stdout.String()
			assert.Contains(t, output, tc.question)
			for i, opt := range tc.options {
				assert.Contains(t, output, opt)
				assert.Contains(t, output, string(rune('1'+i))+")")
			}
		})
	}
}

func TestTerminalCollector_AskQuestion_emptyOptions(t *testing.T) {
	c := NewTerminalCollector(false)

	_, err := c.AskQuestion(context.Background(), "Pick one", nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no options provided")
}

func TestTerminalCollector_AskQuestion_emptyOptionsSlice(t *testing.T) {
	c := NewTerminalCollector(false)

	_, err := c.AskQuestion(context.Background(), "Pick one", []string{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no options provided")
}

func TestTerminalCollector_selectWithNumbers_outputFormat(t *testing.T) {
	var stdout bytes.Buffer
	c := &TerminalCollector{stdin: strings.NewReader("2\n"), stdout: &stdout}

	_, err := c.selectWithNumbers(context.Background(), "Which database?", []string{"PostgreSQL", "MySQL", "SQLite"})
	require.NoError(t, err)

	output := stdout.String()
	assert.Contains(t, output, "Which database?")
	assert.Contains(t, output, "1) PostgreSQL")
	assert.Contains(t, output, "2) MySQL")
	assert.Contains(t, output, "3) SQLite")
	assert.Contains(t, output, "Enter number (1-3)")
}

func TestTerminalCollector_selectWithNumbers_readError(t *testing.T) {
	// use an empty reader that will return EOF immediately
	c := &TerminalCollector{stdin: strings.NewReader(""), stdout: &bytes.Buffer{}}

	_, err := c.selectWithNumbers(context.Background(), "Pick one", []string{"A", "B"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "read input")
}

func TestNewTerminalCollector(t *testing.T) {
	t.Run("noColor true", func(t *testing.T) {
		c := NewTerminalCollector(true)
		assert.NotNil(t, c)
		assert.True(t, c.noColor)
	})

	t.Run("noColor false", func(t *testing.T) {
		c := NewTerminalCollector(false)
		assert.NotNil(t, c)
		assert.False(t, c.noColor)
	})
}

func TestAskYesNo(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "y returns true", input: "y\n", want: true},
		{name: "Y returns true", input: "Y\n", want: true},
		{name: "yes returns true", input: "yes\n", want: true},
		{name: "YES returns true", input: "YES\n", want: true},
		{name: "Yes returns true", input: "Yes\n", want: true},
		{name: "n returns false", input: "n\n", want: false},
		{name: "N returns false", input: "N\n", want: false},
		{name: "no returns false", input: "no\n", want: false},
		{name: "empty returns false", input: "\n", want: false},
		{name: "anything else returns false", input: "maybe\n", want: false},
		{name: "y with spaces", input: "  y  \n", want: true},
		{name: "EOF returns false", input: "", want: false},
	}

	prompt := "continue?"
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			got := AskYesNo(context.Background(), prompt, strings.NewReader(tc.input), &stdout)
			assert.Equal(t, tc.want, got)
			assert.Contains(t, stdout.String(), prompt)
			assert.Contains(t, stdout.String(), "[y/N]")
		})
	}

	t.Run("context_canceled_returns_false", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel immediately
		var stdout bytes.Buffer
		got := AskYesNo(ctx, prompt, strings.NewReader("y\n"), &stdout)
		assert.False(t, got)
	})
}

func TestTerminalCollector_AskDraftReview(t *testing.T) {
	planContent := "# Test Plan\n\n## Overview\n\nThis is a test plan."

	t.Run("accept action", func(t *testing.T) {
		var stdout bytes.Buffer
		c := &TerminalCollector{stdin: strings.NewReader("1\n"), stdout: &stdout, noColor: true}

		action, feedback, err := c.AskDraftReview(context.Background(), "Review the plan", planContent)

		require.NoError(t, err)
		assert.Equal(t, ActionAccept, action)
		assert.Empty(t, feedback)

		output := stdout.String()
		assert.Contains(t, output, "Plan Draft")
		assert.Contains(t, output, planContent)
		assert.Contains(t, output, "Review the plan")
		assert.Contains(t, output, "Accept")
		assert.Contains(t, output, "Revise")
		assert.Contains(t, output, "Reject")
	})

	t.Run("revise action with feedback", func(t *testing.T) {
		var stdout bytes.Buffer
		// select option 2 (Revise), then provide feedback
		// use a larger buffer to ensure both reads work
		input := "2\nPlease add more details to the implementation steps\n"
		reader := &sequentialLineReader{lines: []string{"2", "Please add more details to the implementation steps"}}
		c := &TerminalCollector{stdin: reader, stdout: &stdout, noColor: true}

		action, feedback, err := c.AskDraftReview(context.Background(), "Review the plan", planContent)

		require.NoError(t, err)
		assert.Equal(t, ActionRevise, action)
		assert.Equal(t, "Please add more details to the implementation steps", feedback)

		output := stdout.String()
		assert.Contains(t, output, "Enter revision feedback")
		_ = input // used for documentation
	})

	t.Run("reject action", func(t *testing.T) {
		var stdout bytes.Buffer
		c := &TerminalCollector{stdin: strings.NewReader("3\n"), stdout: &stdout, noColor: true}

		action, feedback, err := c.AskDraftReview(context.Background(), "Review the plan", planContent)

		require.NoError(t, err)
		assert.Equal(t, ActionReject, action)
		assert.Empty(t, feedback)
	})

	t.Run("revise with empty feedback returns error", func(t *testing.T) {
		var stdout bytes.Buffer
		reader := &sequentialLineReader{lines: []string{"2", ""}}
		c := &TerminalCollector{stdin: reader, stdout: &stdout, noColor: true}

		_, _, err := c.AskDraftReview(context.Background(), "Review the plan", planContent)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "feedback cannot be empty")
	})

	t.Run("revise with whitespace-only feedback returns error", func(t *testing.T) {
		var stdout bytes.Buffer
		reader := &sequentialLineReader{lines: []string{"2", "   "}}
		c := &TerminalCollector{stdin: reader, stdout: &stdout, noColor: true}

		_, _, err := c.AskDraftReview(context.Background(), "Review the plan", planContent)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "feedback cannot be empty")
	})

	t.Run("invalid selection returns error", func(t *testing.T) {
		var stdout bytes.Buffer
		c := &TerminalCollector{stdin: strings.NewReader("5\n"), stdout: &stdout, noColor: true}

		_, _, err := c.AskDraftReview(context.Background(), "Review the plan", planContent)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "select action")
	})

	t.Run("EOF on selection returns error", func(t *testing.T) {
		var stdout bytes.Buffer
		c := &TerminalCollector{stdin: strings.NewReader(""), stdout: &stdout, noColor: true}

		_, _, err := c.AskDraftReview(context.Background(), "Review the plan", planContent)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "select action")
	})

	t.Run("EOF on feedback returns error", func(t *testing.T) {
		var stdout bytes.Buffer
		// create a reader that returns "2\n" for selection, then EOF for feedback
		c := &TerminalCollector{stdin: &eofAfterReader{data: "2\n"}, stdout: &stdout, noColor: true}

		_, _, err := c.AskDraftReview(context.Background(), "Review the plan", planContent)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "read feedback")
	})

	t.Run("context canceled returns error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var stdout bytes.Buffer
		c := &TerminalCollector{stdin: strings.NewReader("1\n"), stdout: &stdout, noColor: true}

		_, _, err := c.AskDraftReview(ctx, "Review the plan", planContent)

		require.Error(t, err)
	})

	t.Run("with color rendering", func(t *testing.T) {
		var stdout bytes.Buffer
		c := &TerminalCollector{stdin: strings.NewReader("1\n"), stdout: &stdout, noColor: false}

		action, _, err := c.AskDraftReview(context.Background(), "Review the plan", planContent)

		require.NoError(t, err)
		assert.Equal(t, ActionAccept, action)
		// with color enabled, glamour renders the markdown with ANSI codes
		// the content should be different from plain text
		output := stdout.String()
		assert.Contains(t, output, "Plan Draft")
	})
}

// eofAfterReader returns data on first read, then EOF on subsequent reads
type eofAfterReader struct {
	data     string
	consumed bool
}

func (r *eofAfterReader) Read(p []byte) (n int, err error) {
	if r.consumed {
		return 0, io.EOF
	}
	r.consumed = true
	return copy(p, r.data), nil
}

// sequentialLineReader returns lines one at a time, each ending with newline.
// this allows simulating multiple sequential reads (selection + feedback).
type sequentialLineReader struct {
	lines []string
	index int
}

func (r *sequentialLineReader) Read(p []byte) (n int, err error) {
	if r.index >= len(r.lines) {
		return 0, io.EOF
	}
	line := r.lines[r.index] + "\n"
	r.index++
	return copy(p, line), nil
}

func TestTerminalCollector_renderMarkdown(t *testing.T) {
	t.Run("with color enabled renders markdown", func(t *testing.T) {
		c := &TerminalCollector{noColor: false}
		content := "# Heading\n\nSome **bold** text."
		result, err := c.renderMarkdown(content)
		require.NoError(t, err)
		assert.NotEqual(t, content, result)
		assert.Contains(t, result, "Heading")
		assert.Contains(t, result, "bold")
	})

	t.Run("with noColor returns plain content", func(t *testing.T) {
		c := &TerminalCollector{noColor: true}
		content := "# Heading\n\nSome **bold** text."
		result, err := c.renderMarkdown(content)
		require.NoError(t, err)
		assert.Equal(t, content, result)
	})

	t.Run("handles empty content", func(t *testing.T) {
		c := &TerminalCollector{noColor: false}
		result, err := c.renderMarkdown("")
		require.NoError(t, err)
		assert.Empty(t, strings.TrimSpace(result))
	})

	t.Run("handles empty content with noColor", func(t *testing.T) {
		c := &TerminalCollector{noColor: true}
		result, err := c.renderMarkdown("")
		require.NoError(t, err)
		assert.Empty(t, result)
	})

	t.Run("handles code blocks", func(t *testing.T) {
		c := &TerminalCollector{noColor: false}
		content := "```go\nfunc main() {}\n```"
		result, err := c.renderMarkdown(content)
		require.NoError(t, err)
		assert.Contains(t, result, "func")
		assert.Contains(t, result, "main")
	})

	t.Run("handles lists", func(t *testing.T) {
		c := &TerminalCollector{noColor: false}
		content := "- item 1\n- item 2\n- item 3"
		result, err := c.renderMarkdown(content)
		require.NoError(t, err)
		assert.Contains(t, result, "item 1")
		assert.Contains(t, result, "item 2")
		assert.Contains(t, result, "item 3")
	})
}
