package executor

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/executor/mocks"
	"github.com/umputun/ralphex/pkg/status"
)

func TestClaudeExecutor_Run_Success(t *testing.T) {
	jsonStream := `{"type":"content_block_delta","delta":{"type":"text_delta","text":"Hello world <<<RALPHEX:ALL_TASKS_DONE>>>"}}`

	mock := &mocks.CommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, _ ...string) (io.Reader, func() error, error) {
			return strings.NewReader(jsonStream), func() error { return nil }, nil
		},
	}
	e := &ClaudeExecutor{cmdRunner: mock}

	result := e.Run(context.Background(), "test prompt")

	require.NoError(t, result.Error)
	assert.Equal(t, "Hello world <<<RALPHEX:ALL_TASKS_DONE>>>", result.Output)
	assert.Equal(t, "<<<RALPHEX:ALL_TASKS_DONE>>>", result.Signal)
}

func TestClaudeExecutor_Run_StartError(t *testing.T) {
	mock := &mocks.CommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, _ ...string) (io.Reader, func() error, error) {
			return nil, nil, errors.New("command not found")
		},
	}
	e := &ClaudeExecutor{cmdRunner: mock}

	result := e.Run(context.Background(), "test prompt")

	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "command not found")
}

func TestClaudeExecutor_Run_WaitError_WithOutput(t *testing.T) {
	jsonStream := `{"type":"content_block_delta","delta":{"type":"text_delta","text":"partial output"}}`

	mock := &mocks.CommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, _ ...string) (io.Reader, func() error, error) {
			return strings.NewReader(jsonStream), func() error { return errors.New("exit status 1") }, nil
		},
	}
	e := &ClaudeExecutor{cmdRunner: mock}

	result := e.Run(context.Background(), "test prompt")

	// should have output despite error
	require.NoError(t, result.Error)
	assert.Equal(t, "partial output", result.Output)
}

func TestClaudeExecutor_Run_WaitError_NoOutput(t *testing.T) {
	mock := &mocks.CommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, _ ...string) (io.Reader, func() error, error) {
			return strings.NewReader(""), func() error { return errors.New("exit status 1") }, nil
		},
	}
	e := &ClaudeExecutor{cmdRunner: mock}

	result := e.Run(context.Background(), "test prompt")

	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "claude exited with error")
}

func TestClaudeExecutor_Run_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mock := &mocks.CommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, _ ...string) (io.Reader, func() error, error) {
			return strings.NewReader(""), func() error { return context.Canceled }, nil
		},
	}
	e := &ClaudeExecutor{cmdRunner: mock}

	result := e.Run(ctx, "test prompt")

	require.ErrorIs(t, result.Error, context.Canceled)
}

func TestClaudeExecutor_Run_WithOutputHandler(t *testing.T) {
	jsonStream := `{"type":"content_block_delta","delta":{"type":"text_delta","text":"chunk1"}}
{"type":"content_block_delta","delta":{"type":"text_delta","text":"chunk2"}}`

	var chunks []string
	mock := &mocks.CommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, _ ...string) (io.Reader, func() error, error) {
			return strings.NewReader(jsonStream), func() error { return nil }, nil
		},
	}
	e := &ClaudeExecutor{
		cmdRunner:     mock,
		OutputHandler: func(text string) { chunks = append(chunks, text) },
	}

	result := e.Run(context.Background(), "test prompt")

	require.NoError(t, result.Error)
	assert.Equal(t, "chunk1chunk2", result.Output)
	assert.Equal(t, []string{"chunk1", "chunk2"}, chunks)
}

func TestClaudeExecutor_parseStream(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantOutput string
		wantSignal string
	}{
		{
			name:       "content block delta",
			input:      `{"type":"content_block_delta","delta":{"type":"text_delta","text":"Hello world"}}`,
			wantOutput: "Hello world",
			wantSignal: "",
		},
		{
			name: "multiple deltas",
			input: `{"type":"content_block_delta","delta":{"type":"text_delta","text":"Hello "}}
{"type":"content_block_delta","delta":{"type":"text_delta","text":"world"}}`,
			wantOutput: "Hello world",
			wantSignal: "",
		},
		{
			name:       "completed signal",
			input:      `{"type":"content_block_delta","delta":{"type":"text_delta","text":"Task done. <<<RALPHEX:ALL_TASKS_DONE>>>"}}`,
			wantOutput: "Task done. <<<RALPHEX:ALL_TASKS_DONE>>>",
			wantSignal: "<<<RALPHEX:ALL_TASKS_DONE>>>",
		},
		{
			name:       "failed signal",
			input:      `{"type":"content_block_delta","delta":{"type":"text_delta","text":"Could not finish. <<<RALPHEX:TASK_FAILED>>>"}}`,
			wantOutput: "Could not finish. <<<RALPHEX:TASK_FAILED>>>",
			wantSignal: "<<<RALPHEX:TASK_FAILED>>>",
		},
		{
			name:       "review done signal",
			input:      `{"type":"content_block_delta","delta":{"type":"text_delta","text":"Review complete. <<<RALPHEX:REVIEW_DONE>>>"}}`,
			wantOutput: "Review complete. <<<RALPHEX:REVIEW_DONE>>>",
			wantSignal: "<<<RALPHEX:REVIEW_DONE>>>",
		},
		{
			name:       "codex done signal",
			input:      `{"type":"content_block_delta","delta":{"type":"text_delta","text":"Codex done. <<<RALPHEX:CODEX_REVIEW_DONE>>>"}}`,
			wantOutput: "Codex done. <<<RALPHEX:CODEX_REVIEW_DONE>>>",
			wantSignal: "<<<RALPHEX:CODEX_REVIEW_DONE>>>",
		},
		{
			name:       "plan ready signal",
			input:      `{"type":"content_block_delta","delta":{"type":"text_delta","text":"Plan complete. <<<RALPHEX:PLAN_READY>>>"}}`,
			wantOutput: "Plan complete. <<<RALPHEX:PLAN_READY>>>",
			wantSignal: "<<<RALPHEX:PLAN_READY>>>",
		},
		{
			name:       "result type",
			input:      `{"type":"result","result":{"output":"Final output"}}`,
			wantOutput: "Final output",
			wantSignal: "",
		},
		{
			name:       "empty lines ignored",
			input:      "\n\n" + `{"type":"content_block_delta","delta":{"type":"text_delta","text":"text"}}` + "\n\n",
			wantOutput: "text",
			wantSignal: "",
		},
		{
			name:       "non-json lines printed as-is",
			input:      "not json\n" + `{"type":"content_block_delta","delta":{"type":"text_delta","text":"valid"}}`,
			wantOutput: "not json\nvalid",
			wantSignal: "",
		},
		{
			name:       "unknown event type",
			input:      `{"type":"unknown_type","data":"something"}`,
			wantOutput: "",
			wantSignal: "",
		},
		{
			name:       "assistant event type",
			input:      `{"type":"assistant","message":{"content":[{"type":"text","text":"assistant output"}]}}`,
			wantOutput: "assistant output",
			wantSignal: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := &ClaudeExecutor{}
			result := e.parseStream(strings.NewReader(tc.input))

			assert.Equal(t, tc.wantOutput, result.Output)
			assert.Equal(t, tc.wantSignal, result.Signal)
		})
	}
}

func TestClaudeExecutor_parseStream_withHandler(t *testing.T) {
	input := `{"type":"content_block_delta","delta":{"type":"text_delta","text":"chunk1"}}
{"type":"content_block_delta","delta":{"type":"text_delta","text":"chunk2"}}`

	var chunks []string
	e := &ClaudeExecutor{
		OutputHandler: func(text string) {
			chunks = append(chunks, text)
		},
	}

	result := e.parseStream(strings.NewReader(input))

	assert.Equal(t, "chunk1chunk2", result.Output)
	assert.Equal(t, []string{"chunk1", "chunk2"}, chunks)
}

func TestClaudeExecutor_parseStream_withDebug(t *testing.T) {
	// non-json lines should be printed as-is (with debug message)
	input := "not json\n" + `{"type":"content_block_delta","delta":{"type":"text_delta","text":"valid"}}`

	e := &ClaudeExecutor{Debug: true}
	result := e.parseStream(strings.NewReader(input))

	assert.Equal(t, "not json\nvalid", result.Output)
}

func TestClaudeExecutor_extractText(t *testing.T) {
	e := &ClaudeExecutor{}

	t.Run("assistant event with text", func(t *testing.T) {
		event := streamEvent{Type: "assistant"}
		event.Message.Content = []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{{Type: "text", Text: "assistant message"}}
		assert.Equal(t, "assistant message", e.extractText(&event))
	})

	t.Run("assistant event with multiple text blocks", func(t *testing.T) {
		event := streamEvent{Type: "assistant"}
		event.Message.Content = []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{{Type: "text", Text: "first"}, {Type: "text", Text: "second"}}
		assert.Equal(t, "firstsecond", e.extractText(&event))
	})

	t.Run("assistant event with empty content", func(t *testing.T) {
		event := streamEvent{Type: "assistant"}
		assert.Empty(t, e.extractText(&event))
	})

	t.Run("content block delta", func(t *testing.T) {
		event := streamEvent{Type: "content_block_delta"}
		event.Delta.Type = "text_delta"
		event.Delta.Text = "hello"
		assert.Equal(t, "hello", e.extractText(&event))
	})

	t.Run("non-text delta", func(t *testing.T) {
		event := streamEvent{Type: "content_block_delta"}
		event.Delta.Type = "tool_use"
		event.Delta.Text = "ignored"
		assert.Empty(t, e.extractText(&event))
	})

	t.Run("result with object", func(t *testing.T) {
		event := streamEvent{Type: "result"}
		event.Result = []byte(`{"output":"final"}`)
		assert.Equal(t, "final", e.extractText(&event))
	})

	t.Run("result with string skipped", func(t *testing.T) {
		// session summary format - content already streamed, should be skipped
		event := streamEvent{Type: "result"}
		event.Result = []byte(`"Task completed"`)
		assert.Empty(t, e.extractText(&event))
	})

	t.Run("message_stop with text content", func(t *testing.T) {
		event := streamEvent{Type: "message_stop"}
		event.Message.Content = []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{
			{Type: "text", Text: "final message"},
		}
		assert.Equal(t, "final message", e.extractText(&event))
	})

	t.Run("message_stop with non-text content", func(t *testing.T) {
		event := streamEvent{Type: "message_stop"}
		event.Message.Content = []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{
			{Type: "tool_use", Text: "ignored"},
		}
		assert.Empty(t, e.extractText(&event))
	})

	t.Run("message_stop with empty content", func(t *testing.T) {
		event := streamEvent{Type: "message_stop"}
		assert.Empty(t, e.extractText(&event))
	})

	t.Run("unknown type", func(t *testing.T) {
		event := streamEvent{Type: "ping"}
		assert.Empty(t, e.extractText(&event))
	})
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

func TestClaudeExecutor_Run_WithCustomCommand(t *testing.T) {
	var capturedCmd string
	var capturedArgs []string
	mock := &mocks.CommandRunnerMock{
		RunFunc: func(_ context.Context, name string, args ...string) (io.Reader, func() error, error) {
			capturedCmd = name
			capturedArgs = args
			return strings.NewReader(`{"type":"content_block_delta","delta":{"type":"text_delta","text":"ok"}}`), func() error { return nil }, nil
		},
	}
	e := &ClaudeExecutor{
		cmdRunner: mock,
		Command:   "my-claude",
	}

	result := e.Run(context.Background(), "test prompt")

	require.NoError(t, result.Error)
	assert.Equal(t, "my-claude", capturedCmd)
	// should still use default args
	assert.Contains(t, capturedArgs, "--dangerously-skip-permissions")
}

func TestClaudeExecutor_Run_WithCustomArgs(t *testing.T) {
	var capturedArgs []string
	mock := &mocks.CommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, args ...string) (io.Reader, func() error, error) {
			capturedArgs = args
			return strings.NewReader(`{"type":"content_block_delta","delta":{"type":"text_delta","text":"ok"}}`), func() error { return nil }, nil
		},
	}
	e := &ClaudeExecutor{
		cmdRunner: mock,
		Args:      "--custom-arg --another-arg value",
	}

	result := e.Run(context.Background(), "test prompt")

	require.NoError(t, result.Error)
	// should use custom args plus prompt args
	assert.Equal(t, []string{"--custom-arg", "--another-arg", "value", "-p", "test prompt"}, capturedArgs)
}

func TestClaudeExecutor_Run_WithCustomCommandAndArgs(t *testing.T) {
	var capturedCmd string
	var capturedArgs []string
	mock := &mocks.CommandRunnerMock{
		RunFunc: func(_ context.Context, name string, args ...string) (io.Reader, func() error, error) {
			capturedCmd = name
			capturedArgs = args
			return strings.NewReader(`{"type":"content_block_delta","delta":{"type":"text_delta","text":"ok"}}`), func() error { return nil }, nil
		},
	}
	e := &ClaudeExecutor{
		cmdRunner: mock,
		Command:   "custom-claude",
		Args:      "--skip-perms --verbose",
	}

	result := e.Run(context.Background(), "the prompt")

	require.NoError(t, result.Error)
	assert.Equal(t, "custom-claude", capturedCmd)
	assert.Equal(t, []string{"--skip-perms", "--verbose", "-p", "the prompt"}, capturedArgs)
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
		{name: "real claude args", input: "--dangerously-skip-permissions --output-format stream-json --verbose", want: []string{"--dangerously-skip-permissions", "--output-format", "stream-json", "--verbose"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := splitArgs(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFilterEnv(t *testing.T) {
	tests := []struct {
		name   string
		env    []string
		remove []string
		want   []string
	}{
		{
			name:   "removes single key",
			env:    []string{"FOO=bar", "BAZ=qux", "ANTHROPIC_API_KEY=secret"},
			remove: []string{"ANTHROPIC_API_KEY"},
			want:   []string{"FOO=bar", "BAZ=qux"},
		},
		{
			name:   "removes multiple keys",
			env:    []string{"A=1", "B=2", "C=3"},
			remove: []string{"A", "C"},
			want:   []string{"B=2"},
		},
		{
			name:   "no match returns original",
			env:    []string{"FOO=bar", "BAZ=qux"},
			remove: []string{"NONEXISTENT"},
			want:   []string{"FOO=bar", "BAZ=qux"},
		},
		{
			name:   "empty env returns empty",
			env:    []string{},
			remove: []string{"FOO"},
			want:   []string{},
		},
		{
			name:   "partial key match not removed",
			env:    []string{"ANTHROPIC_API_KEY_OLD=secret", "ANTHROPIC_API_KEY=new"},
			remove: []string{"ANTHROPIC_API_KEY"},
			want:   []string{"ANTHROPIC_API_KEY_OLD=secret"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := filterEnv(tc.env, tc.remove...)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestClaudeExecutor_parseStream_largeLines(t *testing.T) {
	// test that lines larger than 64KB (default bufio.Scanner limit) are handled
	// this was the "token too long" bug fix

	tests := []struct {
		name string
		size int
	}{
		{"100KB line", 100 * 1024},
		{"500KB line", 500 * 1024},
		{"1MB line", 1024 * 1024},
		{"2MB line", 2 * 1024 * 1024},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// create a large text payload
			largeText := strings.Repeat("x", tc.size)
			jsonLine := `{"type":"content_block_delta","delta":{"type":"text_delta","text":"` + largeText + `"}}`

			e := &ClaudeExecutor{}
			result := e.parseStream(strings.NewReader(jsonLine))

			require.NoError(t, result.Error, "should handle %d byte line without error", tc.size)
			assert.Len(t, result.Output, tc.size, "output should contain full text")
		})
	}
}

func TestClaudeExecutor_parseStream_multipleLargeLines(t *testing.T) {
	// test multiple large lines in sequence (simulates parallel agent output)
	lineSize := 200 * 1024 // 200KB per line
	numLines := 5          // simulate 5 parallel agents

	lines := make([]string, 0, numLines)
	for i := range numLines {
		text := strings.Repeat(string(rune('a'+i)), lineSize)
		lines = append(lines, `{"type":"content_block_delta","delta":{"type":"text_delta","text":"`+text+`"}}`)
	}
	input := strings.Join(lines, "\n")

	e := &ClaudeExecutor{}
	result := e.parseStream(strings.NewReader(input))

	require.NoError(t, result.Error)
	assert.Len(t, result.Output, lineSize*numLines, "should contain all output from all lines")
}

func TestPatternMatchError_Error(t *testing.T) {
	err := &PatternMatchError{Pattern: "rate limit exceeded", HelpCmd: "claude /usage"}
	assert.Equal(t, `detected error pattern: "rate limit exceeded"`, err.Error())
}

func TestCheckErrorPatterns(t *testing.T) {
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
			got := checkErrorPatterns(tc.output, tc.patterns)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestClaudeExecutor_Run_ErrorPattern(t *testing.T) {
	tests := []struct {
		name        string
		output      string
		patterns    []string
		wantError   bool
		wantPattern string
		wantHelpCmd string
		wantOutput  string
	}{
		{
			name:       "no patterns configured",
			output:     `{"type":"content_block_delta","delta":{"type":"text_delta","text":"You've hit your limit"}}`,
			patterns:   nil,
			wantError:  false,
			wantOutput: "You've hit your limit",
		},
		{
			name:       "pattern not matched",
			output:     `{"type":"content_block_delta","delta":{"type":"text_delta","text":"Task completed successfully"}}`,
			patterns:   []string{"rate limit", "quota exceeded"},
			wantError:  false,
			wantOutput: "Task completed successfully",
		},
		{
			name:        "pattern matched",
			output:      `{"type":"content_block_delta","delta":{"type":"text_delta","text":"Error: You've hit your limit for today"}}`,
			patterns:    []string{"hit your limit"},
			wantError:   true,
			wantPattern: "hit your limit",
			wantHelpCmd: "claude /usage",
			wantOutput:  "Error: You've hit your limit for today",
		},
		{
			name:        "case insensitive match",
			output:      `{"type":"content_block_delta","delta":{"type":"text_delta","text":"RATE LIMIT EXCEEDED"}}`,
			patterns:    []string{"rate limit exceeded"},
			wantError:   true,
			wantPattern: "rate limit exceeded",
			wantHelpCmd: "claude /usage",
			wantOutput:  "RATE LIMIT EXCEEDED",
		},
		{
			name:        "first matching pattern returned",
			output:      `{"type":"content_block_delta","delta":{"type":"text_delta","text":"rate limit and quota exceeded"}}`,
			patterns:    []string{"rate limit", "quota exceeded"},
			wantError:   true,
			wantPattern: "rate limit",
			wantHelpCmd: "claude /usage",
			wantOutput:  "rate limit and quota exceeded",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mocks.CommandRunnerMock{
				RunFunc: func(_ context.Context, _ string, _ ...string) (io.Reader, func() error, error) {
					return strings.NewReader(tc.output), func() error { return nil }, nil
				},
			}
			e := &ClaudeExecutor{
				cmdRunner:     mock,
				ErrorPatterns: tc.patterns,
			}

			result := e.Run(context.Background(), "test prompt")

			assert.Equal(t, tc.wantOutput, result.Output)

			if tc.wantError {
				require.Error(t, result.Error)
				var patternErr *PatternMatchError
				require.ErrorAs(t, result.Error, &patternErr)
				assert.Equal(t, tc.wantPattern, patternErr.Pattern)
				assert.Equal(t, tc.wantHelpCmd, patternErr.HelpCmd)
			} else {
				require.NoError(t, result.Error)
			}
		})
	}
}

func TestClaudeExecutor_Run_ErrorPattern_WithSignal(t *testing.T) {
	// error pattern should still be detected even when output contains a signal
	jsonStream := `{"type":"content_block_delta","delta":{"type":"text_delta","text":"You've hit your limit <<<RALPHEX:ALL_TASKS_DONE>>>"}}`

	mock := &mocks.CommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, _ ...string) (io.Reader, func() error, error) {
			return strings.NewReader(jsonStream), func() error { return nil }, nil
		},
	}
	e := &ClaudeExecutor{
		cmdRunner:     mock,
		ErrorPatterns: []string{"hit your limit"},
	}

	result := e.Run(context.Background(), "test prompt")

	// should have error due to pattern match
	require.Error(t, result.Error)
	var patternErr *PatternMatchError
	require.ErrorAs(t, result.Error, &patternErr)
	assert.Equal(t, "hit your limit", patternErr.Pattern)

	// should preserve output and signal
	assert.Contains(t, result.Output, "You've hit your limit")
	assert.Equal(t, "<<<RALPHEX:ALL_TASKS_DONE>>>", result.Signal)
}
