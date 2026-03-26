package executor

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractUsageFromText_EmptyOrInvalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{name: "empty", input: ""},
		{name: "spaces", input: "   \n\t"},
		{name: "invalid json", input: "{not-json}"},
		{name: "plain text", input: "no usage here"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			usage := extractUsageFromText(tc.input)
			assert.True(t, usage.Empty())
		})
	}
}

func TestExtractUsageFromText_JSONShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		wantInput int
		wantOut   int
		wantTotal int
		wantRead  int
		wantWrite int
	}{
		{
			name:      "top-level usage input-output",
			input:     `{"usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18}}`,
			wantInput: 11, wantOut: 7, wantTotal: 18,
		},
		{
			name:      "top-level usage prompt-completion",
			input:     `{"usage":{"prompt_tokens":4,"completion_tokens":6}}`,
			wantInput: 4, wantOut: 6, wantTotal: 10,
		},
		{
			name:      "nested result usage",
			input:     `{"result":{"usage":{"input_tokens":3,"output_tokens":2,"cache_read_input_tokens":9,"cache_creation_input_tokens":5}}}`,
			wantInput: 3, wantOut: 2, wantTotal: 5, wantRead: 9, wantWrite: 5,
		},
		{
			name:      "ndjson merged latest snapshot",
			input:     "{\"usage\":{\"input_tokens\":10}}\n{\"usage\":{\"output_tokens\":6}}\n{\"usage\":{\"total_tokens\":16}}",
			wantInput: 10, wantOut: 6, wantTotal: 16,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			usage := extractUsageFromText(tc.input)
			assert.Equal(t, tc.wantInput, usage.InputTokens)
			assert.Equal(t, tc.wantOut, usage.OutputTokens)
			assert.Equal(t, tc.wantTotal, usage.TotalTokens)
			assert.Equal(t, tc.wantRead, usage.CacheRead)
			assert.Equal(t, tc.wantWrite, usage.CacheWrite)
		})
	}
}

