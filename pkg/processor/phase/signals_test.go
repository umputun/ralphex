package phase

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_IsReviewDone(t *testing.T) {
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

func Test_IsCodexDone(t *testing.T) {
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

func Test_IsPlanReady(t *testing.T) {
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

func Test_ParseQuestionPayload_ValidJSON(t *testing.T) {
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

func Test_ParseQuestionPayload_MalformedJSON(t *testing.T) {
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

func Test_ParseQuestionPayload_NoSignal(t *testing.T) {
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

func Test_ParsePlanDraftPayload_ValidContent(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		expected string
	}{
		{
			name: "simple plan draft",
			output: `some output before
<<<RALPHEX:PLAN_DRAFT>>>
# Plan Title

## Overview
This is a test plan.

## Tasks
- [ ] Task 1
<<<RALPHEX:END>>>
some output after`,
			expected: `# Plan Title

## Overview
This is a test plan.

## Tasks
- [ ] Task 1`,
		},
		{
			name: "plan draft with extra whitespace",
			output: `<<<RALPHEX:PLAN_DRAFT>>>

    # Plan

## Overview
Content here.

<<<RALPHEX:END>>>`,
			expected: `# Plan

## Overview
Content here.`,
		},
		{
			name: "plan draft embedded in large output",
			output: `[10:30:05] analyzing codebase...
[10:30:10] found existing patterns
[10:30:15] generating plan draft

<<<RALPHEX:PLAN_DRAFT>>>
# Feature Implementation Plan

## Context
The project uses Go with standard library.

## Implementation Steps

### Task 1: Create interface
- [ ] Define interface in consumer package
- [ ] Add mock generation directive
<<<RALPHEX:END>>>

[10:30:20] waiting for user review...`,
			expected: `# Feature Implementation Plan

## Context
The project uses Go with standard library.

## Implementation Steps

### Task 1: Create interface
- [ ] Define interface in consumer package
- [ ] Add mock generation directive`,
		},
		{
			name: "minimal plan content",
			output: `<<<RALPHEX:PLAN_DRAFT>>>
# Minimal Plan
<<<RALPHEX:END>>>`,
			expected: "# Minimal Plan",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ParsePlanDraftPayload(tc.output)
			require.NoError(t, err)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func Test_ParsePlanDraftPayload_Malformed(t *testing.T) {
	tests := []struct {
		name        string
		output      string
		errContains string
	}{
		{
			name: "missing end marker",
			output: `<<<RALPHEX:PLAN_DRAFT>>>
# Plan without end marker`,
			errContains: "missing END marker",
		},
		{
			name: "empty content",
			output: `<<<RALPHEX:PLAN_DRAFT>>>
<<<RALPHEX:END>>>`,
			errContains: "empty plan content",
		},
		{
			name: "whitespace only content",
			output: `<<<RALPHEX:PLAN_DRAFT>>>


<<<RALPHEX:END>>>`,
			errContains: "empty plan content",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ParsePlanDraftPayload(tc.output)
			assert.Empty(t, result)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errContains)
		})
	}
}

func Test_ParsePlanDraftPayload_NoSignal(t *testing.T) {
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
			output: "[10:30:05] running analysis...\n[10:30:10] done\n",
		},
		{
			name:   "output with other signals",
			output: "<<<RALPHEX:ALL_TASKS_DONE>>>",
		},
		{
			name:   "partial signal marker",
			output: "RALPHEX:PLAN_DRAFT is not valid",
		},
		{
			name:   "signal text in regular content",
			output: "the signal is <<<RALPHEX:PLAN_DRAFT but without proper format",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ParsePlanDraftPayload(tc.output)
			require.ErrorIs(t, err, ErrNoPlanDraftSignal)
			assert.Empty(t, result)
		})
	}
}

// codex-style outputs are free-form plaintext without JSON envelopes or markdown fences.
// these regression tests assert the existing signal detection helpers and payload parsers
// remain codex-compatible. if they all pass with signals.go unchanged, the investigation
// is concluded with no code change required.

func Test_ParseQuestionPayload_CodexStyleFreeForm(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		expected *QuestionPayload
	}{
		{
			name: "question preceded by codex reasoning summary",
			output: `I explored the codebase. The auth layer currently has no token store.
Two reasonable approaches exist: keep tokens in-memory or persist them.

<<<RALPHEX:QUESTION>>>
{"question": "Where should tokens live?", "options": ["in-memory", "sqlite", "redis"]}
<<<RALPHEX:END>>>`,
			expected: &QuestionPayload{
				Question: "Where should tokens live?",
				Options:  []string{"in-memory", "sqlite", "redis"},
			},
		},
		{
			name: "question followed by additional codex chatter",
			output: `<<<RALPHEX:QUESTION>>>
{"question": "Pick a serializer", "options": ["json", "gob", "protobuf"], "context": "current path uses json"}
<<<RALPHEX:END>>>

Once you choose I will continue refining the plan and produce the draft.`,
			expected: &QuestionPayload{
				Question: "Pick a serializer",
				Options:  []string{"json", "gob", "protobuf"},
				Context:  "current path uses json",
			},
		},
		{
			name: "question wrapped by multi-paragraph reasoning text",
			output: `Background: the existing fetcher batches by 100. Lowering it may help latency
but raises per-request overhead. A value above the current default risks exceeding
the upstream rate cap of ~250 requests/minute. Asking the user.

<<<RALPHEX:QUESTION>>>
{"question": "What batch size?", "options": ["50", "100", "200"]}
<<<RALPHEX:END>>>

Will proceed once selection is provided.`,
			expected: &QuestionPayload{
				Question: "What batch size?",
				Options:  []string{"50", "100", "200"},
			},
		},
		{
			name: "question with JSON payload on separate lines",
			output: `<<<RALPHEX:QUESTION>>>
{
  "question": "Choose retry policy",
  "options": ["exponential", "linear", "none"]
}
<<<RALPHEX:END>>>`,
			expected: &QuestionPayload{
				Question: "Choose retry policy",
				Options:  []string{"exponential", "linear", "none"},
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

func Test_ParsePlanDraftPayload_CodexStyleFreeForm(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		expected string
	}{
		{
			name: "plan_draft preceded by codex reasoning summary",
			output: `I reviewed README.md, the cmd/ entry point, and key pkg/ subtrees.
Identified eight tasks across config, runner, and docs. Drafting the plan now.

<<<RALPHEX:PLAN_DRAFT>>>
# Plan
## Overview
Add feature X with task breakdown.
<<<RALPHEX:END>>>`,
			expected: `# Plan
## Overview
Add feature X with task breakdown.`,
		},
		{
			name: "plan_draft followed by codex chatter after END",
			output: `<<<RALPHEX:PLAN_DRAFT>>>
# Feature Plan

## Overview
Wire up the new endpoint.

## Tasks
- [ ] task 1
- [ ] task 2
<<<RALPHEX:END>>>

That covers the minimal viable scope. Let me know if revisions are needed.`,
			expected: `# Feature Plan

## Overview
Wire up the new endpoint.

## Tasks
- [ ] task 1
- [ ] task 2`,
		},
		{
			name: "plan_draft spanning many lines with structured task sections",
			output: `Exploration complete. Here is the draft.

<<<RALPHEX:PLAN_DRAFT>>>
# Implementation Plan

## Context
The project uses Go modules and a layered package layout.

## Implementation Steps

### Task 1: Add config
- [ ] new field on struct
- [ ] tests

### Task 2: Wire CLI
- [ ] flag
- [ ] validation
- [ ] tests
<<<RALPHEX:END>>>

Awaiting accept/revise/reject decision.`,
			expected: `# Implementation Plan

## Context
The project uses Go modules and a layered package layout.

## Implementation Steps

### Task 1: Add config
- [ ] new field on struct
- [ ] tests

### Task 2: Wire CLI
- [ ] flag
- [ ] validation
- [ ] tests`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ParsePlanDraftPayload(tc.output)
			require.NoError(t, err)
			assert.Equal(t, tc.expected, result)
		})
	}
}
