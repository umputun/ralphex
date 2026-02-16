package input

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

			got, err := c.selectWithNumbers(context.Background(), tc.question, tc.options, nil)

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

func TestTerminalCollector_selectWithNumbers_otherOption(t *testing.T) {
	// these tests use selectWithNumbers directly with the "Other" option
	// already appended, which mirrors what AskQuestion does before dispatching
	opts := []string{"A", "B", otherOption}

	t.Run("other option displayed in list", func(t *testing.T) {
		var stdout bytes.Buffer
		c := &TerminalCollector{stdin: strings.NewReader("1\n"), stdout: &stdout}

		got, err := c.selectWithNumbers(context.Background(), "Pick one", opts, nil)

		require.NoError(t, err)
		assert.Equal(t, "A", got)

		output := stdout.String()
		assert.Contains(t, output, "Other (type your own answer)")
	})

	t.Run("selecting other prompts for custom answer", func(t *testing.T) {
		var stdout bytes.Buffer
		reader := &sequentialLineReader{lines: []string{"3", "my custom answer"}}
		c := &TerminalCollector{stdin: reader, stdout: &stdout}

		got, err := c.selectWithNumbers(context.Background(), "Pick one", opts, nil)

		require.NoError(t, err)
		assert.Equal(t, "my custom answer", got)

		output := stdout.String()
		assert.Contains(t, output, "Enter your answer:")
	})

	t.Run("selecting other with empty answer returns error", func(t *testing.T) {
		var stdout bytes.Buffer
		reader := &sequentialLineReader{lines: []string{"3", ""}}
		c := &TerminalCollector{stdin: reader, stdout: &stdout}

		_, err := c.selectWithNumbers(context.Background(), "Pick one", opts, nil)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "custom answer cannot be empty")
	})

	t.Run("selecting other with whitespace-only answer returns error", func(t *testing.T) {
		var stdout bytes.Buffer
		reader := &sequentialLineReader{lines: []string{"3", "   "}}
		c := &TerminalCollector{stdin: reader, stdout: &stdout}

		_, err := c.selectWithNumbers(context.Background(), "Pick one", opts, nil)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "custom answer cannot be empty")
	})
}

func TestTerminalCollector_readCustomAnswer(t *testing.T) {
	t.Run("reads valid answer", func(t *testing.T) {
		var stdout bytes.Buffer
		c := &TerminalCollector{stdin: strings.NewReader("my answer\n"), stdout: &stdout}

		got, err := c.readCustomAnswer(context.Background(), nil)

		require.NoError(t, err)
		assert.Equal(t, "my answer", got)
		assert.Contains(t, stdout.String(), "Enter your answer:")
	})

	t.Run("trims whitespace", func(t *testing.T) {
		var stdout bytes.Buffer
		c := &TerminalCollector{stdin: strings.NewReader("  trimmed  \n"), stdout: &stdout}

		got, err := c.readCustomAnswer(context.Background(), nil)

		require.NoError(t, err)
		assert.Equal(t, "trimmed", got)
	})

	t.Run("empty answer returns error", func(t *testing.T) {
		var stdout bytes.Buffer
		c := &TerminalCollector{stdin: strings.NewReader("\n"), stdout: &stdout}

		_, err := c.readCustomAnswer(context.Background(), nil)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "custom answer cannot be empty")
	})

	t.Run("EOF returns error", func(t *testing.T) {
		var stdout bytes.Buffer
		c := &TerminalCollector{stdin: strings.NewReader(""), stdout: &stdout}

		_, err := c.readCustomAnswer(context.Background(), nil)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "read custom answer")
	})

	t.Run("context canceled returns error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var stdout bytes.Buffer
		c := &TerminalCollector{stdin: strings.NewReader("answer\n"), stdout: &stdout}

		_, err := c.readCustomAnswer(ctx, nil)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "read custom answer")
	})
}

func TestTerminalCollector_AskQuestion_appendsOther(t *testing.T) {
	t.Run("select regular option", func(t *testing.T) {
		var stdout bytes.Buffer
		c := &TerminalCollector{stdin: strings.NewReader("1\n"), stdout: &stdout, noFzf: true}

		got, err := c.AskQuestion(context.Background(), "Pick one", []string{"A", "B"})

		require.NoError(t, err)
		assert.Equal(t, "A", got)
		assert.Contains(t, stdout.String(), "Other (type your own answer)")
	})

	t.Run("select other option and type answer", func(t *testing.T) {
		var stdout bytes.Buffer
		reader := &sequentialLineReader{lines: []string{"3", "custom value"}}
		c := &TerminalCollector{stdin: reader, stdout: &stdout, noFzf: true}

		got, err := c.AskQuestion(context.Background(), "Pick one", []string{"A", "B"})

		require.NoError(t, err)
		assert.Equal(t, "custom value", got)
		assert.Contains(t, stdout.String(), "Enter your answer:")
	})
}

func TestTerminalCollector_AskQuestion_sentinelCollision(t *testing.T) {
	// if an incoming option matches otherOption exactly, it should be filtered out
	// so the user sees only one "Other" entry at the end
	t.Run("collision filtered and regular option works", func(t *testing.T) {
		var stdout bytes.Buffer
		c := &TerminalCollector{stdin: strings.NewReader("2\n"), stdout: &stdout, noFzf: true}

		got, err := c.AskQuestion(context.Background(), "Pick one",
			[]string{"A", otherOption, "B"})

		require.NoError(t, err)
		assert.Equal(t, "B", got) // "B" is option 2 after filtering

		output := stdout.String()
		assert.Contains(t, output, "1) A")
		assert.Contains(t, output, "2) B")
		assert.Contains(t, output, "3) Other (type your own answer)")
	})

	t.Run("collision filtered and other option triggers custom input", func(t *testing.T) {
		var stdout bytes.Buffer
		reader := &sequentialLineReader{lines: []string{"3", "typed answer"}}
		c := &TerminalCollector{stdin: reader, stdout: &stdout, noFzf: true}

		got, err := c.AskQuestion(context.Background(), "Pick one",
			[]string{"A", otherOption, "B"})

		require.NoError(t, err)
		assert.Equal(t, "typed answer", got)
	})
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

	_, err := c.selectWithNumbers(context.Background(), "Which database?", []string{"PostgreSQL", "MySQL", "SQLite"}, nil)
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

	_, err := c.selectWithNumbers(context.Background(), "Pick one", []string{"A", "B"}, nil)

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

func TestTerminalCollector_getStdinStdout(t *testing.T) {
	t.Run("getStdin returns os.Stdin when nil", func(t *testing.T) {
		c := &TerminalCollector{}
		assert.Equal(t, os.Stdin, c.getStdin())
	})

	t.Run("getStdout returns os.Stdout when nil", func(t *testing.T) {
		c := &TerminalCollector{}
		assert.Equal(t, os.Stdout, c.getStdout())
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

	t.Run("context_deadline_exceeded_returns_false", func(t *testing.T) {
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
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
		assert.Contains(t, output, "Interactive review")
		assert.Contains(t, output, "Reject")
	})

	t.Run("revise action with feedback", func(t *testing.T) {
		var stdout bytes.Buffer
		// select option 2 (Revise), then provide feedback
		reader := &sequentialLineReader{lines: []string{"2", "Please add more details to the implementation steps"}}
		c := &TerminalCollector{stdin: reader, stdout: &stdout, noColor: true}

		action, feedback, err := c.AskDraftReview(context.Background(), "Review the plan", planContent)

		require.NoError(t, err)
		assert.Equal(t, ActionRevise, action)
		assert.Equal(t, "Please add more details to the implementation steps", feedback)

		output := stdout.String()
		assert.Contains(t, output, "Enter revision feedback")
	})

	t.Run("reject action", func(t *testing.T) {
		var stdout bytes.Buffer
		c := &TerminalCollector{stdin: strings.NewReader("4\n"), stdout: &stdout, noColor: true}

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

	t.Run("interactive review with changes returns revise and diff", func(t *testing.T) {
		var stdout bytes.Buffer
		// select option 3 (Interactive review)
		c := &TerminalCollector{
			stdin:   strings.NewReader("3\n"),
			stdout:  &stdout,
			noColor: true,
			editorFunc: func(_ context.Context, content string) (string, error) {
				// simulate editing: add a line
				return content + "\n## Added Section\n", nil
			},
		}

		action, feedback, err := c.AskDraftReview(context.Background(), "Review the plan", planContent)

		require.NoError(t, err)
		assert.Equal(t, ActionRevise, action)
		assert.Contains(t, feedback, "user reviewed the plan in an editor and made changes")
		assert.Contains(t, feedback, "added lines (+)")
		assert.Contains(t, feedback, "--- original")
		assert.Contains(t, feedback, "+++ annotated")
		assert.Contains(t, feedback, "+## Added Section")
	})

	t.Run("interactive review no changes re-prompts then accept", func(t *testing.T) {
		var stdout bytes.Buffer
		// first select option 3 (Interactive review) - editor returns same content
		// then select option 1 (Accept) on re-prompt
		reader := &sequentialLineReader{lines: []string{"3", "1"}}
		c := &TerminalCollector{
			stdin:   reader,
			stdout:  &stdout,
			noColor: true,
			editorFunc: func(_ context.Context, content string) (string, error) {
				return content, nil // no changes
			},
		}

		action, feedback, err := c.AskDraftReview(context.Background(), "Review the plan", planContent)

		require.NoError(t, err)
		assert.Equal(t, ActionAccept, action)
		assert.Empty(t, feedback)
		assert.Contains(t, stdout.String(), "no changes detected")
	})

	t.Run("interactive review editor error re-prompts then accept", func(t *testing.T) {
		var stdout bytes.Buffer
		// first select option 3 (Interactive review) - editor returns error
		// then select option 1 (Accept) on re-prompt
		reader := &sequentialLineReader{lines: []string{"3", "1"}}
		c := &TerminalCollector{
			stdin:   reader,
			stdout:  &stdout,
			noColor: true,
			editorFunc: func(_ context.Context, _ string) (string, error) {
				return "", errors.New("editor crashed")
			},
		}

		action, feedback, err := c.AskDraftReview(context.Background(), "Review the plan", planContent)

		require.NoError(t, err)
		assert.Equal(t, ActionAccept, action)
		assert.Empty(t, feedback)
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

func TestTerminalCollector_computeDiff(t *testing.T) {
	c := &TerminalCollector{}

	t.Run("identical content returns empty string", func(t *testing.T) {
		content := "line 1\nline 2\nline 3\n"
		diff, err := c.computeDiff(content, content)
		require.NoError(t, err)
		assert.Empty(t, diff)
	})

	t.Run("changed content produces unified diff", func(t *testing.T) {
		original := "line 1\nline 2\nline 3\n"
		edited := "line 1\nline 2 modified\nline 3\n"
		diff, err := c.computeDiff(original, edited)
		require.NoError(t, err)
		assert.NotEmpty(t, diff)
		assert.Contains(t, diff, "--- original")
		assert.Contains(t, diff, "+++ annotated")
		assert.Contains(t, diff, "-line 2")
		assert.Contains(t, diff, "+line 2 modified")
	})

	t.Run("added lines produce diff", func(t *testing.T) {
		original := "line 1\nline 2\n"
		edited := "line 1\nline 2\nline 3\n"
		diff, err := c.computeDiff(original, edited)
		require.NoError(t, err)
		assert.NotEmpty(t, diff)
		assert.Contains(t, diff, "+line 3")
	})

	t.Run("removed lines produce diff", func(t *testing.T) {
		original := "line 1\nline 2\nline 3\n"
		edited := "line 1\nline 3\n"
		diff, err := c.computeDiff(original, edited)
		require.NoError(t, err)
		assert.NotEmpty(t, diff)
		assert.Contains(t, diff, "-line 2")
	})

	t.Run("empty original and non-empty edited", func(t *testing.T) {
		diff, err := c.computeDiff("", "new content\n")
		require.NoError(t, err)
		assert.NotEmpty(t, diff)
		assert.Contains(t, diff, "+new content")
	})

	t.Run("both empty returns empty string", func(t *testing.T) {
		diff, err := c.computeDiff("", "")
		require.NoError(t, err)
		assert.Empty(t, diff)
	})
}

func TestTerminalCollector_openEditor(t *testing.T) {
	t.Run("writes content and reads it back", func(t *testing.T) {
		// use "true" as editor — it does nothing, file stays unchanged
		t.Setenv("EDITOR", "true")
		t.Setenv("VISUAL", "")

		c := &TerminalCollector{}
		content := "# Plan\n\nSome content here.\n"
		result, err := c.openEditor(context.Background(), content)
		require.NoError(t, err)
		assert.Equal(t, content, result)
	})

	t.Run("VISUAL takes precedence over EDITOR", func(t *testing.T) {
		t.Setenv("VISUAL", "true")
		t.Setenv("EDITOR", "false") // would fail if used

		c := &TerminalCollector{}
		result, err := c.openEditor(context.Background(), "test content\n")
		require.NoError(t, err)
		assert.Equal(t, "test content\n", result)
	})

	t.Run("editor not found returns error", func(t *testing.T) {
		t.Setenv("VISUAL", "")
		t.Setenv("EDITOR", "nonexistent-editor-binary-xyz")

		c := &TerminalCollector{}
		_, err := c.openEditor(context.Background(), "content")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
		assert.Contains(t, err.Error(), "$EDITOR")
		assert.Contains(t, err.Error(), "nonexistent-editor-binary-xyz")
	})

	t.Run("editorFunc propagates context error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel immediately

		c := &TerminalCollector{editorFunc: func(_ context.Context, _ string) (string, error) {
			return "", ctx.Err()
		}}
		_, err := c.openEditor(ctx, "content")
		require.Error(t, err)
	})

	t.Run("editor with arguments", func(t *testing.T) {
		// use "env true" as a multi-word editor command to verify argument splitting
		t.Setenv("VISUAL", "")
		t.Setenv("EDITOR", "env true")

		c := &TerminalCollector{}
		_, err := c.openEditor(context.Background(), "content\n")
		require.NoError(t, err)
	})

	t.Run("editor exits with error returns error", func(t *testing.T) {
		// "false" exits with code 1
		t.Setenv("VISUAL", "")
		t.Setenv("EDITOR", "false")

		c := &TerminalCollector{}
		_, err := c.openEditor(context.Background(), "content")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "run editor")
	})

	t.Run("editorFunc bypasses real editor", func(t *testing.T) {
		t.Setenv("VISUAL", "")
		t.Setenv("EDITOR", "")

		c := &TerminalCollector{editorFunc: func(_ context.Context, content string) (string, error) {
			return content, nil
		}}
		result, err := c.openEditor(context.Background(), "content via editorFunc\n")
		require.NoError(t, err)
		assert.Equal(t, "content via editorFunc\n", result)
	})

	t.Run("whitespace-padded EDITOR is trimmed", func(t *testing.T) {
		// exercises the real editor-launch path with whitespace-padded env vars.
		// verifies strings.TrimSpace correctly handles padded editor values.
		t.Setenv("VISUAL", "")
		t.Setenv("EDITOR", "  true  ")

		c := &TerminalCollector{}
		result, err := c.openEditor(context.Background(), "whitespace env test")
		require.NoError(t, err)
		assert.Equal(t, "whitespace env test", result)
	})

	t.Run("temp file is cleaned up", func(t *testing.T) {
		t.Setenv("VISUAL", "")
		t.Setenv("EDITOR", "true")

		c := &TerminalCollector{}
		result, err := c.openEditor(context.Background(), "cleanup test content")
		require.NoError(t, err)
		assert.Equal(t, "cleanup test content", result)
		// verify no ralphex-plan temp files remain after openEditor returns
		matches, _ := filepath.Glob(os.TempDir() + "/ralphex-plan-*.md")
		assert.Empty(t, matches, "temp file should be cleaned up, found: %v", matches)
	})
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
