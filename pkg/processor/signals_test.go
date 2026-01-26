package processor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsTerminalSignal(t *testing.T) {
	tests := []struct {
		signal string
		want   bool
	}{
		{SignalCompleted, true},
		{SignalFailed, true},
		{SignalReviewDone, false},
		{SignalCodexDone, false},
		{"", false},
		{"OTHER", false},
	}

	for _, tc := range tests {
		t.Run(tc.signal, func(t *testing.T) {
			assert.Equal(t, tc.want, IsTerminalSignal(tc.signal))
		})
	}
}

func TestIsReviewDone(t *testing.T) {
	tests := []struct {
		signal string
		want   bool
	}{
		{SignalReviewDone, true},
		{SignalCompleted, false},
		{SignalFailed, false},
		{SignalCodexDone, false},
		{"", false},
	}

	for _, tc := range tests {
		t.Run(tc.signal, func(t *testing.T) {
			assert.Equal(t, tc.want, IsReviewDone(tc.signal))
		})
	}
}

func TestIsCodexDone(t *testing.T) {
	tests := []struct {
		signal string
		want   bool
	}{
		{SignalCodexDone, true},
		{SignalCompleted, false},
		{SignalFailed, false},
		{SignalReviewDone, false},
		{"", false},
	}

	for _, tc := range tests {
		t.Run(tc.signal, func(t *testing.T) {
			assert.Equal(t, tc.want, IsCodexDone(tc.signal))
		})
	}
}

func TestIsPlanReady(t *testing.T) {
	tests := []struct {
		signal string
		want   bool
	}{
		{SignalPlanReady, true},
		{SignalCompleted, false},
		{SignalFailed, false},
		{SignalReviewDone, false},
		{SignalCodexDone, false},
		{SignalQuestion, false},
		{"", false},
	}

	for _, tc := range tests {
		t.Run(tc.signal, func(t *testing.T) {
			assert.Equal(t, tc.want, IsPlanReady(tc.signal))
		})
	}
}

func TestParseQuestionPayload_ValidJSON(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		expected *QuestionPayload
	}{
		{
			name: "simple question with options",
			output: `some output before
<<<RALPHEX:QUESTION>>>
{"question": "Which cache backend?", "options": ["Redis", "In-memory", "File-based"]}
<<<RALPHEX:END>>>
some output after`,
			expected: &QuestionPayload{
				Question: "Which cache backend?",
				Options:  []string{"Redis", "In-memory", "File-based"},
			},
		},
		{
			name: "question with context",
			output: `<<<RALPHEX:QUESTION>>>
{"question": "Select authentication method", "options": ["JWT", "Session", "OAuth"], "context": "Project uses REST API"}
<<<RALPHEX:END>>>`,
			expected: &QuestionPayload{
				Question: "Select authentication method",
				Options:  []string{"JWT", "Session", "OAuth"},
				Context:  "Project uses REST API",
			},
		},
		{
			name: "question with extra whitespace",
			output: `<<<RALPHEX:QUESTION>>>

    {"question": "Pick one", "options": ["A", "B"]}

<<<RALPHEX:END>>>`,
			expected: &QuestionPayload{
				Question: "Pick one",
				Options:  []string{"A", "B"},
			},
		},
		{
			name: "question embedded in large output",
			output: `[10:30:05] starting analysis...
[10:30:10] found existing code in pkg/store/
[10:30:15] need to clarify approach

<<<RALPHEX:QUESTION>>>
{"question": "How should data be stored?", "options": ["Database", "File system"]}
<<<RALPHEX:END>>>

[10:30:20] waiting for user input...`,
			expected: &QuestionPayload{
				Question: "How should data be stored?",
				Options:  []string{"Database", "File system"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ParseQuestionPayload(tc.output)
			require.NoError(t, err)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestParseQuestionPayload_MalformedJSON(t *testing.T) {
	tests := []struct {
		name        string
		output      string
		errContains string
	}{
		{
			name: "invalid json syntax",
			output: `<<<RALPHEX:QUESTION>>>
{question: "missing quotes", "options": ["A"]}
<<<RALPHEX:END>>>`,
			errContains: "invalid JSON",
		},
		{
			name: "missing end marker",
			output: `<<<RALPHEX:QUESTION>>>
{"question": "test", "options": ["A"]}`,
			errContains: "missing END marker",
		},
		{
			name: "empty payload",
			output: `<<<RALPHEX:QUESTION>>>
<<<RALPHEX:END>>>`,
			errContains: "empty JSON payload",
		},
		{
			name: "whitespace only payload",
			output: `<<<RALPHEX:QUESTION>>>

<<<RALPHEX:END>>>`,
			errContains: "empty JSON payload",
		},
		{
			name: "missing question field",
			output: `<<<RALPHEX:QUESTION>>>
{"options": ["A", "B"]}
<<<RALPHEX:END>>>`,
			errContains: "missing question field",
		},
		{
			name: "empty question field",
			output: `<<<RALPHEX:QUESTION>>>
{"question": "", "options": ["A", "B"]}
<<<RALPHEX:END>>>`,
			errContains: "missing question field",
		},
		{
			name: "missing options field",
			output: `<<<RALPHEX:QUESTION>>>
{"question": "test"}
<<<RALPHEX:END>>>`,
			errContains: "missing or empty options field",
		},
		{
			name: "empty options array",
			output: `<<<RALPHEX:QUESTION>>>
{"question": "test", "options": []}
<<<RALPHEX:END>>>`,
			errContains: "missing or empty options field",
		},
		{
			name: "truncated json",
			output: `<<<RALPHEX:QUESTION>>>
{"question": "test", "options": ["A",
<<<RALPHEX:END>>>`,
			errContains: "invalid JSON",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ParseQuestionPayload(tc.output)
			assert.Nil(t, result)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errContains)
		})
	}
}

func TestParseQuestionPayload_NoSignal(t *testing.T) {
	tests := []struct {
		name   string
		output string
	}{
		{
			name:   "empty output",
			output: "",
		},
		{
			name:   "regular output without signal",
			output: "[10:30:05] running tests...\n[10:30:10] all tests passed\n",
		},
		{
			name:   "output with other signals",
			output: "<<<RALPHEX:ALL_TASKS_DONE>>>",
		},
		{
			name:   "partial signal marker",
			output: "RALPHEX:QUESTION is not valid",
		},
		{
			name:   "signal text in regular content",
			output: "the signal is <<<RALPHEX:QUESTION but without proper format",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ParseQuestionPayload(tc.output)
			require.ErrorIs(t, err, ErrNoQuestionSignal)
			assert.Nil(t, result)
		})
	}
}
