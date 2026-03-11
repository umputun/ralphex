package executor

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/umputun/ralphex/pkg/status"
)

// failingReader is a reader that always returns an error.
type failingReader struct {
	err error
}

func (r *failingReader) Read(_ []byte) (int, error) {
	return 0, r.err
}

func TestDetectSignal(t *testing.T) {
	tests := []struct {
		text string
		want string
	}{
		{"some text", ""},
		{"task done " + status.Completed, status.Completed},
		{status.Failed + " error", status.Failed},
		{"review complete " + status.ReviewDone, status.ReviewDone},
		{status.CodexDone + " analysis done", status.CodexDone},
		{"plan complete " + status.PlanReady, status.PlanReady},
		{"no signal here", ""},
	}

	for _, tc := range tests {
		t.Run(tc.text, func(t *testing.T) {
			got := detectSignal(tc.text)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSplitArgs(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{name: "simple args", input: "--flag1 --flag2 value", want: []string{"--flag1", "--flag2", "value"}},
		{name: "double quoted", input: `--flag "value with spaces"`, want: []string{"--flag", "value with spaces"}},
		{name: "single quoted", input: `--flag 'value with spaces'`, want: []string{"--flag", "value with spaces"}},
		{name: "empty string", input: "", want: nil},
		{name: "only spaces", input: "   ", want: nil},
		{name: "multiple spaces between", input: "arg1   arg2", want: []string{"arg1", "arg2"}},
		{name: "mixed quotes", input: `--a "b" --c 'd'`, want: []string{"--a", "b", "--c", "d"}},
		{name: "escaped quote", input: `--flag \"quoted\"`, want: []string{"--flag", `"quoted"`}},
		{name: "real copilot args", input: "--allow-all --no-ask-user --output-format json", want: []string{"--allow-all", "--no-ask-user", "--output-format", "json"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := splitArgs(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPatternMatchError_Error(t *testing.T) {
	err := &PatternMatchError{Pattern: "rate limit exceeded", HelpCmd: "copilot --help"}
	assert.Equal(t, `detected error pattern: "rate limit exceeded"`, err.Error())
}

func TestLimitPatternError_Error(t *testing.T) {
	err := &LimitPatternError{Pattern: "You've hit your limit", HelpCmd: "copilot --help"}
	assert.Equal(t, `detected limit pattern: "You've hit your limit"`, err.Error())
}

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		patterns []string
		want     string
	}{
		{name: "no patterns", output: "some output", patterns: nil, want: ""},
		{name: "empty patterns slice", output: "some output", patterns: []string{}, want: ""},
		{name: "no match", output: "everything is fine", patterns: []string{"error", "failed"}, want: ""},
		{name: "exact match", output: "You've hit your limit", patterns: []string{"You've hit your limit"}, want: "You've hit your limit"},
		{name: "substring match", output: "Error: You've hit your limit today", patterns: []string{"hit your limit"}, want: "hit your limit"},
		{name: "case insensitive", output: "YOU'VE HIT YOUR LIMIT", patterns: []string{"you've hit your limit"}, want: "you've hit your limit"},
		{name: "mixed case match", output: "Rate Limit Exceeded", patterns: []string{"rate limit exceeded"}, want: "rate limit exceeded"},
		{name: "first pattern wins", output: "rate limit and quota exceeded", patterns: []string{"rate limit", "quota exceeded"}, want: "rate limit"},
		{name: "second pattern matches", output: "your quota exceeded the limit", patterns: []string{"rate limit", "quota exceeded"}, want: "quota exceeded"},
		{name: "empty pattern skipped", output: "some text", patterns: []string{"", "some"}, want: "some"},
		{name: "whitespace in pattern", output: "rate  limit", patterns: []string{"rate  limit"}, want: "rate  limit"},
		{name: "multiline output", output: "line1\nYou've hit your limit\nline3", patterns: []string{"hit your limit"}, want: "hit your limit"},
		{name: "api error 500", output: `API Error: 500 {"type":"error","error":{"type":"api_error","message":"Internal server error"}}`, patterns: []string{"API Error:"}, want: "API Error:"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := matchPattern(tc.output, tc.patterns)
			assert.Equal(t, tc.want, got)
		})
	}
}
