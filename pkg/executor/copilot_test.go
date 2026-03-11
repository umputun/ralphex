package executor

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/executor/mocks"
	"github.com/umputun/ralphex/pkg/status"
)

func TestCopilotExecutor_Run_Success(t *testing.T) {
	jsonl := `{"type":"assistant.message_delta","data":{"messageId":"m1","deltaContent":"Hello world"},"id":"e1","timestamp":"2026-01-01T00:00:00Z","parentId":"p1","ephemeral":true}
{"type":"assistant.message","data":{"messageId":"m1","content":"Hello world <<<RALPHEX:ALL_TASKS_DONE>>>"},"id":"e2","timestamp":"2026-01-01T00:00:01Z","parentId":"p2"}
{"type":"result","timestamp":"2026-01-01T00:00:02Z","sessionId":"s1","exitCode":0}`

	mock := &mocks.CommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, _ ...string) (io.Reader, func() error, error) {
			return strings.NewReader(jsonl), func() error { return nil }, nil
		},
	}
	e := &CopilotExecutor{cmdRunner: mock}

	result := e.Run(context.Background(), "test prompt")

	require.NoError(t, result.Error)
	assert.Equal(t, "Hello world <<<RALPHEX:ALL_TASKS_DONE>>>", result.Output)
	assert.Equal(t, "<<<RALPHEX:ALL_TASKS_DONE>>>", result.Signal)
}

func TestCopilotExecutor_Run_StartError(t *testing.T) {
	mock := &mocks.CommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, _ ...string) (io.Reader, func() error, error) {
			return nil, nil, errors.New("command not found")
		},
	}
	e := &CopilotExecutor{cmdRunner: mock}

	result := e.Run(context.Background(), "test prompt")

	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "command not found")
}

func TestCopilotExecutor_Run_WaitError_WithOutput(t *testing.T) {
	jsonl := `{"type":"assistant.message","data":{"messageId":"m1","content":"partial output"},"id":"e1","timestamp":"2026-01-01T00:00:00Z","parentId":"p1"}`

	mock := &mocks.CommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, _ ...string) (io.Reader, func() error, error) {
			return strings.NewReader(jsonl), func() error { return errors.New("exit status 1") }, nil
		},
	}
	e := &CopilotExecutor{cmdRunner: mock}

	result := e.Run(context.Background(), "test prompt")

	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "copilot exited with error")
	assert.Equal(t, "partial output", result.Output)
}

func TestCopilotExecutor_Run_WaitError_WithOutputAndSignal(t *testing.T) {
	jsonl := `{"type":"assistant.message","data":{"messageId":"m1","content":"task done <<<RALPHEX:ALL_TASKS_DONE>>>"},"id":"e1","timestamp":"2026-01-01T00:00:00Z","parentId":"p1"}`

	mock := &mocks.CommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, _ ...string) (io.Reader, func() error, error) {
			return strings.NewReader(jsonl), func() error { return errors.New("exit status 1") }, nil
		},
	}
	e := &CopilotExecutor{cmdRunner: mock}

	result := e.Run(context.Background(), "test prompt")

	require.NoError(t, result.Error)
	assert.Equal(t, "task done <<<RALPHEX:ALL_TASKS_DONE>>>", result.Output)
	assert.Equal(t, "<<<RALPHEX:ALL_TASKS_DONE>>>", result.Signal)
}

func TestCopilotExecutor_Run_WaitError_NoOutput(t *testing.T) {
	mock := &mocks.CommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, _ ...string) (io.Reader, func() error, error) {
			return strings.NewReader(""), func() error { return errors.New("exit status 1") }, nil
		},
	}
	e := &CopilotExecutor{cmdRunner: mock}

	result := e.Run(context.Background(), "test prompt")

	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "copilot exited with error")
}

func TestCopilotExecutor_Run_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mock := &mocks.CommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, _ ...string) (io.Reader, func() error, error) {
			return strings.NewReader(""), func() error { return context.Canceled }, nil
		},
	}
	e := &CopilotExecutor{cmdRunner: mock}

	result := e.Run(ctx, "test prompt")

	require.ErrorIs(t, result.Error, context.Canceled)
}

func TestCopilotExecutor_Run_WithOutputHandler(t *testing.T) {
	jsonl := `{"type":"assistant.message_delta","data":{"messageId":"m1","deltaContent":"chunk1"},"id":"e1","timestamp":"2026-01-01T00:00:00Z","parentId":"p1","ephemeral":true}
{"type":"assistant.message_delta","data":{"messageId":"m1","deltaContent":"chunk2"},"id":"e2","timestamp":"2026-01-01T00:00:00Z","parentId":"p2","ephemeral":true}
{"type":"assistant.message","data":{"messageId":"m1","content":"chunk1chunk2"},"id":"e3","timestamp":"2026-01-01T00:00:01Z","parentId":"p3"}`

	var chunks []string
	mock := &mocks.CommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, _ ...string) (io.Reader, func() error, error) {
			return strings.NewReader(jsonl), func() error { return nil }, nil
		},
	}
	e := &CopilotExecutor{
		cmdRunner:     mock,
		OutputHandler: func(text string) { chunks = append(chunks, text) },
	}

	result := e.Run(context.Background(), "test prompt")

	require.NoError(t, result.Error)
	assert.Equal(t, "chunk1chunk2", result.Output)
	assert.Contains(t, chunks, "chunk1")
	assert.Contains(t, chunks, "chunk2")
}

func TestCopilotExecutor_Run_WithCustomCommand(t *testing.T) {
	var capturedCmd string
	var capturedArgs []string
	mock := &mocks.CommandRunnerMock{
		RunFunc: func(_ context.Context, name string, args ...string) (io.Reader, func() error, error) {
			capturedCmd = name
			capturedArgs = args
			jsonl := `{"type":"assistant.message","data":{"messageId":"m1","content":"ok"},"id":"e1","timestamp":"2026-01-01T00:00:00Z","parentId":"p1"}`
			return strings.NewReader(jsonl), func() error { return nil }, nil
		},
	}
	e := &CopilotExecutor{
		cmdRunner: mock,
		Command:   "my-copilot",
	}

	result := e.Run(context.Background(), "test prompt")

	require.NoError(t, result.Error)
	assert.Equal(t, "my-copilot", capturedCmd)
	assert.Contains(t, capturedArgs, "--allow-all")
}

func TestCopilotExecutor_Run_WithCustomArgs(t *testing.T) {
	var capturedArgs []string
	mock := &mocks.CommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, args ...string) (io.Reader, func() error, error) {
			capturedArgs = args
			jsonl := `{"type":"assistant.message","data":{"messageId":"m1","content":"ok"},"id":"e1","timestamp":"2026-01-01T00:00:00Z","parentId":"p1"}`
			return strings.NewReader(jsonl), func() error { return nil }, nil
		},
	}
	e := &CopilotExecutor{
		cmdRunner: mock,
		Args:      "--custom-arg --another-arg value",
	}

	result := e.Run(context.Background(), "test prompt")

	require.NoError(t, result.Error)
	assert.Equal(t, []string{"--custom-arg", "--another-arg", "value", "-p", "test prompt"}, capturedArgs)
}

func TestCopilotExecutor_RunReview_UsesReviewModel(t *testing.T) {
	var capturedArgs []string
	mock := &mocks.CommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, args ...string) (io.Reader, func() error, error) {
			capturedArgs = args
			jsonl := `{"type":"assistant.message","data":{"messageId":"m1","content":"review done"},"id":"e1","timestamp":"2026-01-01T00:00:00Z","parentId":"p1"}`
			return strings.NewReader(jsonl), func() error { return nil }, nil
		},
	}
	e := &CopilotExecutor{
		cmdRunner:   mock,
		CodingModel: "claude-opus-4-6",
		ReviewModel: "gpt-5.2-codex",
	}

	result := e.RunReview(context.Background(), "review prompt")

	require.NoError(t, result.Error)
	assert.Equal(t, "review done", result.Output)
	// should use ReviewModel, not CodingModel
	assert.Contains(t, capturedArgs, "gpt-5.2-codex")
	assert.NotContains(t, capturedArgs, "claude-opus-4-6")
}

func TestCopilotExecutor_Run_UsesCodingModel(t *testing.T) {
	var capturedArgs []string
	mock := &mocks.CommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, args ...string) (io.Reader, func() error, error) {
			capturedArgs = args
			jsonl := `{"type":"assistant.message","data":{"messageId":"m1","content":"coding done"},"id":"e1","timestamp":"2026-01-01T00:00:00Z","parentId":"p1"}`
			return strings.NewReader(jsonl), func() error { return nil }, nil
		},
	}
	e := &CopilotExecutor{
		cmdRunner:   mock,
		CodingModel: "claude-opus-4-6",
		ReviewModel: "gpt-5.2-codex",
	}

	result := e.Run(context.Background(), "code prompt")

	require.NoError(t, result.Error)
	assert.Equal(t, "coding done", result.Output)
	assert.Contains(t, capturedArgs, "claude-opus-4-6")
	assert.NotContains(t, capturedArgs, "gpt-5.2-codex")
}

func TestCopilotExecutor_Run_ErrorPattern(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		patterns    []string
		wantError   bool
		wantPattern string
	}{
		{
			name:      "no patterns configured",
			content:   "Rate limit exceeded",
			patterns:  nil,
			wantError: false,
		},
		{
			name:      "pattern not matched",
			content:   "Task completed successfully",
			patterns:  []string{"rate limit", "quota exceeded"},
			wantError: false,
		},
		{
			name:        "pattern matched",
			content:     "Error: Rate limit exceeded",
			patterns:    []string{"rate limit"},
			wantError:   true,
			wantPattern: "rate limit",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			jsonl := `{"type":"assistant.message","data":{"messageId":"m1","content":"` + tc.content + `"},"id":"e1","timestamp":"2026-01-01T00:00:00Z","parentId":"p1"}`
			mock := &mocks.CommandRunnerMock{
				RunFunc: func(_ context.Context, _ string, _ ...string) (io.Reader, func() error, error) {
					return strings.NewReader(jsonl), func() error { return nil }, nil
				},
			}
			e := &CopilotExecutor{
				cmdRunner:     mock,
				ErrorPatterns: tc.patterns,
			}

			result := e.Run(context.Background(), "test prompt")

			if tc.wantError {
				require.Error(t, result.Error)
				var patternErr *PatternMatchError
				require.ErrorAs(t, result.Error, &patternErr)
				assert.Equal(t, tc.wantPattern, patternErr.Pattern)
				assert.Equal(t, "copilot --help", patternErr.HelpCmd)
			} else {
				require.NoError(t, result.Error)
			}
		})
	}
}

func TestCopilotExecutor_Run_LimitPattern(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		limitPat    []string
		errorPat    []string
		wantLimit   bool
		wantError   bool
		wantPattern string
	}{
		{
			name: "limit pattern matched", content: "Rate limit exceeded",
			limitPat: []string{"rate limit"}, errorPat: nil,
			wantLimit: true, wantPattern: "rate limit",
		},
		{
			name: "limit takes precedence over error", content: "Rate limit exceeded",
			limitPat: []string{"rate limit"}, errorPat: []string{"rate limit"},
			wantLimit: true, wantPattern: "rate limit",
		},
		{
			name: "error pattern when limit does not match", content: "API Error: 500 internal",
			limitPat: []string{"rate limit"}, errorPat: []string{"API Error:"},
			wantError: true, wantPattern: "API Error:",
		},
		{
			name: "no match at all", content: "Task completed",
			limitPat: []string{"rate limit"}, errorPat: []string{"API Error:"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			jsonl := `{"type":"assistant.message","data":{"messageId":"m1","content":"` + tc.content + `"},"id":"e1","timestamp":"2026-01-01T00:00:00Z","parentId":"p1"}`
			mock := &mocks.CommandRunnerMock{
				RunFunc: func(_ context.Context, _ string, _ ...string) (io.Reader, func() error, error) {
					return strings.NewReader(jsonl), func() error { return nil }, nil
				},
			}
			e := &CopilotExecutor{
				cmdRunner:     mock,
				LimitPatterns: tc.limitPat,
				ErrorPatterns: tc.errorPat,
			}

			result := e.Run(context.Background(), "test prompt")

			switch {
			case tc.wantLimit:
				require.Error(t, result.Error)
				var limitErr *LimitPatternError
				require.ErrorAs(t, result.Error, &limitErr)
				assert.Equal(t, tc.wantPattern, limitErr.Pattern)
				assert.Equal(t, "copilot --help", limitErr.HelpCmd)
			case tc.wantError:
				require.Error(t, result.Error)
				var patternErr *PatternMatchError
				require.ErrorAs(t, result.Error, &patternErr)
				assert.Equal(t, tc.wantPattern, patternErr.Pattern)
			default:
				require.NoError(t, result.Error)
			}
		})
	}
}

func TestCopilotExecutor_parseJSONL(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantOutput string
		wantSignal string
	}{
		{
			name:       "message delta streaming",
			input:      `{"type":"assistant.message_delta","data":{"messageId":"m1","deltaContent":"Hello"},"id":"e1","timestamp":"2026-01-01T00:00:00Z","parentId":"p1","ephemeral":true}`,
			wantOutput: "",
			wantSignal: "",
		},
		{
			name:       "assistant message with content",
			input:      `{"type":"assistant.message","data":{"messageId":"m1","content":"Hello world"},"id":"e1","timestamp":"2026-01-01T00:00:00Z","parentId":"p1"}`,
			wantOutput: "Hello world",
			wantSignal: "",
		},
		{
			name: "multiple messages across turns",
			input: `{"type":"assistant.message","data":{"messageId":"m1","content":"First turn text"},"id":"e1","timestamp":"2026-01-01T00:00:00Z","parentId":"p1"}
{"type":"assistant.message","data":{"messageId":"m2","content":"Second turn text"},"id":"e2","timestamp":"2026-01-01T00:00:01Z","parentId":"p2"}`,
			wantOutput: "First turn textSecond turn text",
			wantSignal: "",
		},
		{
			name:       "completed signal in message",
			input:      `{"type":"assistant.message","data":{"messageId":"m1","content":"Task done. <<<RALPHEX:ALL_TASKS_DONE>>>"},"id":"e1","timestamp":"2026-01-01T00:00:00Z","parentId":"p1"}`,
			wantOutput: "Task done. <<<RALPHEX:ALL_TASKS_DONE>>>",
			wantSignal: status.Completed,
		},
		{
			name:       "failed signal",
			input:      `{"type":"assistant.message","data":{"messageId":"m1","content":"Could not finish. <<<RALPHEX:TASK_FAILED>>>"},"id":"e1","timestamp":"2026-01-01T00:00:00Z","parentId":"p1"}`,
			wantOutput: "Could not finish. <<<RALPHEX:TASK_FAILED>>>",
			wantSignal: status.Failed,
		},
		{
			name:       "review done signal",
			input:      `{"type":"assistant.message","data":{"messageId":"m1","content":"Review complete. <<<RALPHEX:REVIEW_DONE>>>"},"id":"e1","timestamp":"2026-01-01T00:00:00Z","parentId":"p1"}`,
			wantOutput: "Review complete. <<<RALPHEX:REVIEW_DONE>>>",
			wantSignal: status.ReviewDone,
		},
		{
			name:       "codex done signal",
			input:      `{"type":"assistant.message","data":{"messageId":"m1","content":"Codex done. <<<RALPHEX:CODEX_REVIEW_DONE>>>"},"id":"e1","timestamp":"2026-01-01T00:00:00Z","parentId":"p1"}`,
			wantOutput: "Codex done. <<<RALPHEX:CODEX_REVIEW_DONE>>>",
			wantSignal: status.CodexDone,
		},
		{
			name:       "plan ready signal",
			input:      `{"type":"assistant.message","data":{"messageId":"m1","content":"Plan complete. <<<RALPHEX:PLAN_READY>>>"},"id":"e1","timestamp":"2026-01-01T00:00:00Z","parentId":"p1"}`,
			wantOutput: "Plan complete. <<<RALPHEX:PLAN_READY>>>",
			wantSignal: status.PlanReady,
		},
		{
			name:       "signal in delta detected during streaming",
			input:      `{"type":"assistant.message_delta","data":{"messageId":"m1","deltaContent":"<<<RALPHEX:ALL_TASKS_DONE>>>"},"id":"e1","timestamp":"2026-01-01T00:00:00Z","parentId":"p1","ephemeral":true}`,
			wantOutput: "",
			wantSignal: status.Completed,
		},
		{
			name:       "empty lines ignored",
			input:      "\n\n" + `{"type":"assistant.message","data":{"messageId":"m1","content":"text"},"id":"e1","timestamp":"2026-01-01T00:00:00Z","parentId":"p1"}` + "\n\n",
			wantOutput: "text",
			wantSignal: "",
		},
		{
			name:       "non-json lines printed as-is",
			input:      "error: not json\n" + `{"type":"assistant.message","data":{"messageId":"m1","content":"valid"},"id":"e1","timestamp":"2026-01-01T00:00:00Z","parentId":"p1"}`,
			wantOutput: "error: not json\nvalid",
			wantSignal: "",
		},
		{
			name:       "unknown event type skipped",
			input:      `{"type":"unknown_type","data":{"something":"else"},"id":"e1","timestamp":"2026-01-01T00:00:00Z","parentId":"p1"}`,
			wantOutput: "",
			wantSignal: "",
		},
		{
			name: "reasoning events skipped",
			input: `{"type":"assistant.reasoning_delta","data":{"reasoningId":"r1","deltaContent":"thinking..."},"id":"e1","timestamp":"2026-01-01T00:00:00Z","parentId":"p1","ephemeral":true}
{"type":"assistant.reasoning","data":{"reasoningId":"r1","content":"full reasoning"},"id":"e2","timestamp":"2026-01-01T00:00:00Z","parentId":"p2","ephemeral":true}`,
			wantOutput: "",
			wantSignal: "",
		},
		{
			name:       "message with empty content",
			input:      `{"type":"assistant.message","data":{"messageId":"m1","content":"","toolRequests":[]},"id":"e1","timestamp":"2026-01-01T00:00:00Z","parentId":"p1"}`,
			wantOutput: "",
			wantSignal: "",
		},
		{
			name:       "result event with exitCode 0",
			input:      `{"type":"result","timestamp":"2026-01-01T00:00:00Z","sessionId":"s1","exitCode":0}`,
			wantOutput: "",
			wantSignal: "",
		},
		{
			name:       "result event with non-zero exitCode sets failed signal",
			input:      `{"type":"result","timestamp":"2026-01-01T00:00:00Z","sessionId":"s1","exitCode":1}`,
			wantOutput: "",
			wantSignal: status.Failed,
		},
		{
			name: "result exitCode does not override existing signal",
			input: `{"type":"assistant.message","data":{"messageId":"m1","content":"done <<<RALPHEX:ALL_TASKS_DONE>>>"},"id":"e1","timestamp":"2026-01-01T00:00:00Z","parentId":"p1"}
{"type":"result","timestamp":"2026-01-01T00:00:01Z","sessionId":"s1","exitCode":1}`,
			wantOutput: "done <<<RALPHEX:ALL_TASKS_DONE>>>",
			wantSignal: status.Completed,
		},
		{
			name: "tool execution events logged",
			input: `{"type":"tool.execution_start","data":{"toolCallId":"t1","toolName":"bash","arguments":{"command":"go test"}},"id":"e1","timestamp":"2026-01-01T00:00:00Z","parentId":"p1"}
{"type":"tool.execution_complete","data":{"toolCallId":"t1","success":true},"id":"e2","timestamp":"2026-01-01T00:00:01Z","parentId":"p2"}`,
			wantOutput: "",
			wantSignal: "",
		},
		{
			name:       "tool execution failure surfaced",
			input:      `{"type":"tool.execution_complete","data":{"toolCallId":"t1","success":false,"error":{"message":"permission denied"}},"id":"e1","timestamp":"2026-01-01T00:00:00Z","parentId":"p1"}`,
			wantOutput: "",
			wantSignal: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := &CopilotExecutor{}
			result := e.parseJSONL(context.Background(), strings.NewReader(tc.input))

			assert.Equal(t, tc.wantOutput, result.Output)
			assert.Equal(t, tc.wantSignal, result.Signal)
		})
	}
}

func TestCopilotExecutor_parseJSONL_withHandler(t *testing.T) {
	input := `{"type":"assistant.message_delta","data":{"messageId":"m1","deltaContent":"chunk1"},"id":"e1","timestamp":"2026-01-01T00:00:00Z","parentId":"p1","ephemeral":true}
{"type":"assistant.message_delta","data":{"messageId":"m1","deltaContent":"chunk2"},"id":"e2","timestamp":"2026-01-01T00:00:00Z","parentId":"p2","ephemeral":true}
{"type":"assistant.message","data":{"messageId":"m1","content":"chunk1chunk2"},"id":"e3","timestamp":"2026-01-01T00:00:01Z","parentId":"p3"}`

	var chunks []string
	e := &CopilotExecutor{
		OutputHandler: func(text string) {
			chunks = append(chunks, text)
		},
	}

	result := e.parseJSONL(context.Background(), strings.NewReader(input))

	assert.Equal(t, "chunk1chunk2", result.Output)
	assert.Contains(t, chunks, "chunk1")
	assert.Contains(t, chunks, "chunk2")
}

func TestCopilotExecutor_parseJSONL_resultExitCodeLogged(t *testing.T) {
	input := `{"type":"result","timestamp":"2026-01-01T00:00:00Z","sessionId":"s1","exitCode":1}`

	var logged []string
	e := &CopilotExecutor{
		OutputHandler: func(text string) {
			logged = append(logged, text)
		},
	}

	result := e.parseJSONL(context.Background(), strings.NewReader(input))

	assert.Equal(t, status.Failed, result.Signal)
	require.Len(t, logged, 1)
	assert.Contains(t, logged[0], "exit code: 1")
}

func TestCopilotExecutor_parseJSONL_toolFailureLogged(t *testing.T) {
	input := `{"type":"tool.execution_complete","data":{"toolCallId":"t1","success":false,"error":{"message":"permission denied"}},"id":"e1","timestamp":"2026-01-01T00:00:00Z","parentId":"p1"}`

	var logged []string
	e := &CopilotExecutor{
		OutputHandler: func(text string) {
			logged = append(logged, text)
		},
	}

	e.parseJSONL(context.Background(), strings.NewReader(input))

	require.Len(t, logged, 1)
	assert.Contains(t, logged[0], "tool failed")
	assert.Contains(t, logged[0], "permission denied")
}

func TestCopilotExecutor_parseJSONL_toolFailureNoMessage(t *testing.T) {
	input := `{"type":"tool.execution_complete","data":{"toolCallId":"t1","success":false,"error":{}},"id":"e1","timestamp":"2026-01-01T00:00:00Z","parentId":"p1"}`

	var logged []string
	e := &CopilotExecutor{
		OutputHandler: func(text string) {
			logged = append(logged, text)
		},
	}

	e.parseJSONL(context.Background(), strings.NewReader(input))

	require.Len(t, logged, 1)
	assert.Contains(t, logged[0], "unknown error")
}

func TestCopilotExecutor_parseJSONL_toolActivityLogged(t *testing.T) {
	input := `{"type":"tool.execution_start","data":{"toolCallId":"t1","toolName":"bash","arguments":{"command":"go test"}},"id":"e1","timestamp":"2026-01-01T00:00:00Z","parentId":"p1"}`

	var logged []string
	e := &CopilotExecutor{
		OutputHandler: func(text string) {
			logged = append(logged, text)
		},
	}

	e.parseJSONL(context.Background(), strings.NewReader(input))

	require.Len(t, logged, 1)
	assert.Contains(t, logged[0], "bash")
}

// fixture-based tests using real copilot JSONL captures
func TestCopilotExecutor_parseJSONL_SimpleText(t *testing.T) {
	data, err := os.ReadFile("testdata/copilot_fixtures/simple_text.jsonl")
	require.NoError(t, err)

	e := &CopilotExecutor{}
	result := e.parseJSONL(context.Background(), strings.NewReader(string(data)))

	require.NoError(t, result.Error)
	// should have accumulated text from assistant.message events across all turns
	assert.NotEmpty(t, result.Output)
	assert.Contains(t, result.Output, "hello world", "should contain hello world related text (case-insensitive)")
	assert.Empty(t, result.Signal, "simple_text fixture has no signal")
}

func TestCopilotExecutor_parseJSONL_SimpleTextGPT(t *testing.T) {
	data, err := os.ReadFile("testdata/copilot_fixtures/simple_text_gpt.jsonl")
	require.NoError(t, err)

	e := &CopilotExecutor{}
	result := e.parseJSONL(context.Background(), strings.NewReader(string(data)))

	require.NoError(t, result.Error)
	assert.NotEmpty(t, result.Output)
	assert.Contains(t, result.Output, "Hello")
	assert.Empty(t, result.Signal, "GPT fixture has no signal")
}

func TestCopilotExecutor_parseJSONL_ToolUse(t *testing.T) {
	data, err := os.ReadFile("testdata/copilot_fixtures/tool_use.jsonl")
	require.NoError(t, err)

	var toolNames []string
	e := &CopilotExecutor{
		OutputHandler: func(text string) {
			if strings.HasPrefix(text, "[tool: ") {
				toolNames = append(toolNames, text)
			}
		},
	}
	result := e.parseJSONL(context.Background(), strings.NewReader(string(data)))

	require.NoError(t, result.Error)
	assert.NotEmpty(t, result.Output)
	// should have logged tool activity
	assert.NotEmpty(t, toolNames, "tool_use fixture should trigger tool activity logging")
}

func TestCopilotExecutor_parseJSONL_WithSignal(t *testing.T) {
	data, err := os.ReadFile("testdata/copilot_fixtures/with_signal.jsonl")
	require.NoError(t, err)

	e := &CopilotExecutor{}
	result := e.parseJSONL(context.Background(), strings.NewReader(string(data)))

	require.NoError(t, result.Error)
	// the with_signal fixture contains "<<<RALPHEX:COMPLETED>>>" which is not a known
	// signal constant (the actual constant is "<<<RALPHEX:ALL_TASKS_DONE>>>").
	// the fixture demonstrates signal passthrough — verify the text is captured in output.
	assert.Contains(t, result.Output, "<<<RALPHEX:COMPLETED>>>")
	// no known signal should be detected since <<<RALPHEX:COMPLETED>>> is not in the signal list
	assert.Empty(t, result.Signal, "<<<RALPHEX:COMPLETED>>> is not a recognized signal constant")
}

func TestCopilotExecutor_parseJSONL_largeLines(t *testing.T) {
	tests := []struct {
		name string
		size int
	}{
		{"100KB content", 100 * 1024},
		{"500KB content", 500 * 1024},
		{"1MB content", 1024 * 1024},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			largeText := strings.Repeat("x", tc.size)
			jsonl := `{"type":"assistant.message","data":{"messageId":"m1","content":"` + largeText + `"},"id":"e1","timestamp":"2026-01-01T00:00:00Z","parentId":"p1"}`

			e := &CopilotExecutor{}
			result := e.parseJSONL(context.Background(), strings.NewReader(jsonl))

			require.NoError(t, result.Error, "should handle %d byte content without error", tc.size)
			assert.Len(t, result.Output, tc.size, "output should contain full text")
		})
	}
}

func TestCopilotExecutor_Run_CLIError_NoJSONL(t *testing.T) {
	// CLI errors (exit code 1) produce NO JSONL — error goes to stderr as plain text
	stderrOutput := "error: option '--model <model>' argument 'nonexistent-model' is invalid\n"

	mock := &mocks.CommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, _ ...string) (io.Reader, func() error, error) {
			return strings.NewReader(stderrOutput), func() error { return errors.New("exit status 1") }, nil
		},
	}
	e := &CopilotExecutor{cmdRunner: mock}

	result := e.Run(context.Background(), "test prompt")

	require.Error(t, result.Error)
	// the stderr output should be captured (since it was merged into stdout by the runner)
	assert.Contains(t, result.Output, "invalid")
}

func TestCopilotExecutor_Run_TruncatedJSONL(t *testing.T) {
	// simulate process killed mid-stream — last line is partial JSON
	jsonl := `{"type":"assistant.message","data":{"messageId":"m1","content":"some text"},"id":"e1","timestamp":"2026-01-01T00:00:00Z","parentId":"p1"}
{"type":"assistant.message","data":{"messageId":"m2","cont`

	mock := &mocks.CommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, _ ...string) (io.Reader, func() error, error) {
			return strings.NewReader(jsonl), func() error { return nil }, nil
		},
	}
	e := &CopilotExecutor{cmdRunner: mock}

	result := e.Run(context.Background(), "test prompt")

	// should not fail — truncated last line treated as non-JSON and passed through
	require.NoError(t, result.Error)
	assert.Contains(t, result.Output, "some text")
}

func TestCopilotExecutor_Run_ErrorPatternWithSignal(t *testing.T) {
	jsonl := `{"type":"assistant.message","data":{"messageId":"m1","content":"Rate limit exceeded <<<RALPHEX:ALL_TASKS_DONE>>>"},"id":"e1","timestamp":"2026-01-01T00:00:00Z","parentId":"p1"}`

	mock := &mocks.CommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, _ ...string) (io.Reader, func() error, error) {
			return strings.NewReader(jsonl), func() error { return nil }, nil
		},
	}
	e := &CopilotExecutor{
		cmdRunner:     mock,
		ErrorPatterns: []string{"rate limit"},
	}

	result := e.Run(context.Background(), "test prompt")

	require.Error(t, result.Error)
	var patternErr *PatternMatchError
	require.ErrorAs(t, result.Error, &patternErr)
	assert.Equal(t, "rate limit", patternErr.Pattern)
	assert.Contains(t, result.Output, "Rate limit exceeded")
	assert.Equal(t, status.Completed, result.Signal)
}
