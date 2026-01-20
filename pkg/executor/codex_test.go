package executor

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/executor/mocks"
)

func TestCodexExecutor_Run_Success(t *testing.T) {
	mock := &mocks.CodexCommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, _ ...string) (string, string, error) {
			return "Found issue in foo.go:42\n<<<RALPHEX:CODEX_REVIEW_DONE>>>", "", nil
		},
	}
	e := &CodexExecutor{cmdRunner: mock}

	result := e.Run(context.Background(), "analyze code")

	require.NoError(t, result.Error)
	assert.Equal(t, "Found issue in foo.go:42\n<<<RALPHEX:CODEX_REVIEW_DONE>>>", result.Output)
	assert.Equal(t, "<<<RALPHEX:CODEX_REVIEW_DONE>>>", result.Signal)
}

func TestCodexExecutor_Run_FiltersNoise(t *testing.T) {
	mock := &mocks.CodexCommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, _ ...string) (string, string, error) {
			return "Thinking...\nActual finding\nProcessing...\nDone", "", nil
		},
	}
	e := &CodexExecutor{cmdRunner: mock}

	result := e.Run(context.Background(), "analyze code")

	require.NoError(t, result.Error)
	assert.Equal(t, "Actual finding\nDone", result.Output)
}

func TestCodexExecutor_Run_Error_WithStderr(t *testing.T) {
	mock := &mocks.CodexCommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, _ ...string) (string, string, error) {
			return "partial", "codex: model not found", errors.New("exit 1")
		},
	}
	e := &CodexExecutor{cmdRunner: mock}

	result := e.Run(context.Background(), "analyze code")

	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "codex error: codex: model not found")
	assert.Equal(t, "partial", result.Output)
}

func TestCodexExecutor_Run_Error_NoStderr(t *testing.T) {
	mock := &mocks.CodexCommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, _ ...string) (string, string, error) {
			return "", "", errors.New("command failed")
		},
	}
	e := &CodexExecutor{cmdRunner: mock}

	result := e.Run(context.Background(), "analyze code")

	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "codex exited with error")
}

func TestCodexExecutor_Run_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mock := &mocks.CodexCommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, _ ...string) (string, string, error) {
			return "", "", context.Canceled
		},
	}
	e := &CodexExecutor{cmdRunner: mock}

	result := e.Run(ctx, "analyze code")

	require.ErrorIs(t, result.Error, context.Canceled)
}

func TestCodexExecutor_Run_WithOutputHandler(t *testing.T) {
	mock := &mocks.CodexCommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, _ ...string) (string, string, error) {
			return "Line 1\nLine 2\nLine 3", "", nil
		},
	}

	var lines []string
	e := &CodexExecutor{
		cmdRunner:     mock,
		OutputHandler: func(text string) { lines = append(lines, text) },
	}

	result := e.Run(context.Background(), "analyze code")

	require.NoError(t, result.Error)
	assert.Equal(t, []string{"Line 1\n", "Line 2\n", "Line 3\n"}, lines)
}

func TestCodexExecutor_Run_DefaultModel(t *testing.T) {
	mock := &mocks.CodexCommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, _ ...string) (string, string, error) {
			return "result", "", nil
		},
	}
	e := &CodexExecutor{cmdRunner: mock}

	result := e.Run(context.Background(), "test")

	require.NoError(t, result.Error)
	// default model should be gpt-5.2-codex (tested implicitly by success)
}

func TestCodexExecutor_Run_CustomModel(t *testing.T) {
	mock := &mocks.CodexCommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, _ ...string) (string, string, error) {
			return "result", "", nil
		},
	}
	e := &CodexExecutor{cmdRunner: mock, Model: "gpt-4o"}

	result := e.Run(context.Background(), "test")

	require.NoError(t, result.Error)
}

func TestCodexExecutor_Run_WithProjectDoc(t *testing.T) {
	mock := &mocks.CodexCommandRunnerMock{
		RunFunc: func(_ context.Context, _ string, _ ...string) (string, string, error) {
			return "result", "", nil
		},
	}
	e := &CodexExecutor{cmdRunner: mock, ProjectDoc: "/path/to/doc.md"}

	result := e.Run(context.Background(), "test")

	require.NoError(t, result.Error)
}

func TestCodexExecutor_filterOutput(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no noise",
			input: "Line 1\nLine 2\nLine 3",
			want:  "Line 1\nLine 2\nLine 3",
		},
		{
			name:  "removes thinking",
			input: "Thinking...\nActual content\nProcessing...\nMore content",
			want:  "Actual content\nMore content",
		},
		{
			name:  "removes spinner chars",
			input: "⠋ Loading\nActual output\n⠹ Still loading",
			want:  "Actual output",
		},
		{
			name:  "removes analyzing",
			input: "Analyzing...\nResult here\nReading files...\nDone",
			want:  "Result here\nDone",
		},
		{
			name:  "keeps blank lines within content",
			input: "Line 1\n\nLine 2",
			want:  "Line 1\n\nLine 2",
		},
		{
			name:  "removes spinner placeholder",
			input: "Starting\n[spinner]\nFinished",
			want:  "Starting\nFinished",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := &CodexExecutor{}
			got, err := e.filterOutput(tc.input)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestCodexExecutor_isNoise(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"Normal text", false},
		{"Thinking...", true},
		{"Processing...", true},
		{"Reading files...", true},
		{"Analyzing...", true},
		{"[spinner]", true},
		{"⠋ Loading", true},
		{"⠙ Working", true},
		{"Some ⠹ in middle", true},
		{"", false}, // blank lines are kept
		{"   ", false},
	}

	for _, tc := range tests {
		t.Run(tc.line, func(t *testing.T) {
			e := &CodexExecutor{}
			got := e.isNoise(tc.line)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestCodexExecutor_filterOutput_withDebug(t *testing.T) {
	e := &CodexExecutor{Debug: true}
	input := "Thinking...\nActual content"
	got, err := e.filterOutput(input)
	require.NoError(t, err)
	assert.Equal(t, "Actual content", got)
}
