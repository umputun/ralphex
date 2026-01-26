package input

import (
	"bytes"
	"context"
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

			got, err := c.selectWithNumbers(tc.question, tc.options)

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
	c := NewTerminalCollector()

	_, err := c.AskQuestion(context.Background(), "Pick one", nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no options provided")
}

func TestTerminalCollector_AskQuestion_emptyOptionsSlice(t *testing.T) {
	c := NewTerminalCollector()

	_, err := c.AskQuestion(context.Background(), "Pick one", []string{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no options provided")
}

func TestTerminalCollector_selectWithNumbers_outputFormat(t *testing.T) {
	var stdout bytes.Buffer
	c := &TerminalCollector{stdin: strings.NewReader("2\n"), stdout: &stdout}

	_, err := c.selectWithNumbers("Which database?", []string{"PostgreSQL", "MySQL", "SQLite"})
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

	_, err := c.selectWithNumbers("Pick one", []string{"A", "B"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "read input")
}

func TestNewTerminalCollector(t *testing.T) {
	c := NewTerminalCollector()
	assert.NotNil(t, c)
}
