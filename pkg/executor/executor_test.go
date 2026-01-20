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
)

func TestClaudeExecutor_Run_Success(t *testing.T) {
	jsonStream := `{"type":"content_block_delta","delta":{"type":"text_delta","text":"Hello world <<<RALPHEX:ALL_TASKS_DONE>>>"}}`

	mock := &mocks.ClaudeCommandRunnerMock{
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
	mock := &mocks.ClaudeCommandRunnerMock{
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

	mock := &mocks.ClaudeCommandRunnerMock{
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
	mock := &mocks.ClaudeCommandRunnerMock{
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

	mock := &mocks.ClaudeCommandRunnerMock{
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
	mock := &mocks.ClaudeCommandRunnerMock{
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
		{"task done <<<RALPHEX:ALL_TASKS_DONE>>>", "<<<RALPHEX:ALL_TASKS_DONE>>>"},
		{"<<<RALPHEX:TASK_FAILED>>> error", "<<<RALPHEX:TASK_FAILED>>>"},
		{"review complete <<<RALPHEX:REVIEW_DONE>>>", "<<<RALPHEX:REVIEW_DONE>>>"},
		{"<<<RALPHEX:CODEX_REVIEW_DONE>>> analysis done", "<<<RALPHEX:CODEX_REVIEW_DONE>>>"},
		{"no signal here", ""},
	}

	for _, tc := range tests {
		t.Run(tc.text, func(t *testing.T) {
			got := detectSignal(tc.text)
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
