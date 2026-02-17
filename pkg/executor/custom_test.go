package executor

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockCustomRunner implements CustomRunner for testing.
type mockCustomRunner struct {
	runFunc func(ctx context.Context, script, promptFile string) (io.Reader, func() error, error)
}

func (m *mockCustomRunner) Run(ctx context.Context, script, promptFile string) (io.Reader, func() error, error) {
	return m.runFunc(ctx, script, promptFile)
}

func TestCustomExecutor_Run_Success(t *testing.T) {
	mock := &mockCustomRunner{
		runFunc: func(_ context.Context, _, _ string) (io.Reader, func() error, error) {
			output := "Analysis complete: no issues found.\n<<<RALPHEX:CODEX_REVIEW_DONE>>>"
			return strings.NewReader(output), func() error { return nil }, nil
		},
	}
	e := &CustomExecutor{Script: "/path/to/script.sh", runner: mock}

	result := e.Run(context.Background(), "review this code")

	require.NoError(t, result.Error)
	assert.Contains(t, result.Output, "Analysis complete")
	assert.Equal(t, "<<<RALPHEX:CODEX_REVIEW_DONE>>>", result.Signal)
}

func TestCustomExecutor_Run_StreamsOutput(t *testing.T) {
	output := `Starting review...
Found issue at main.go:42
Found issue at util.go:15
Review complete
<<<RALPHEX:CODEX_REVIEW_DONE>>>`

	mock := &mockCustomRunner{
		runFunc: func(_ context.Context, _, _ string) (io.Reader, func() error, error) {
			return strings.NewReader(output), func() error { return nil }, nil
		},
	}

	var streamedLines []string
	e := &CustomExecutor{
		Script:        "/path/to/script.sh",
		runner:        mock,
		OutputHandler: func(text string) { streamedLines = append(streamedLines, strings.TrimSuffix(text, "\n")) },
	}

	result := e.Run(context.Background(), "review this code")

	require.NoError(t, result.Error)
	assert.Contains(t, streamedLines, "Starting review...", "first line should be streamed")
	assert.Contains(t, streamedLines, "Found issue at main.go:42", "issues should be streamed")
	assert.Contains(t, streamedLines, "Review complete", "completion message should be streamed")
	assert.Equal(t, "<<<RALPHEX:CODEX_REVIEW_DONE>>>", result.Signal)
}

func TestCustomExecutor_Run_NoScript(t *testing.T) {
	e := &CustomExecutor{Script: ""}

	result := e.Run(context.Background(), "review this code")

	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "custom review script not configured")
}

func TestCustomExecutor_Run_StartError(t *testing.T) {
	mock := &mockCustomRunner{
		runFunc: func(_ context.Context, _, _ string) (io.Reader, func() error, error) {
			return nil, nil, errors.New("script not found")
		},
	}
	e := &CustomExecutor{Script: "/path/to/script.sh", runner: mock}

	result := e.Run(context.Background(), "review this code")

	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "start custom script")
	assert.Contains(t, result.Error.Error(), "script not found")
}

func TestCustomExecutor_Run_WaitError(t *testing.T) {
	mock := &mockCustomRunner{
		runFunc: func(_ context.Context, _, _ string) (io.Reader, func() error, error) {
			return strings.NewReader("partial output"), func() error { return errors.New("exit 1") }, nil
		},
	}
	e := &CustomExecutor{Script: "/path/to/script.sh", runner: mock}

	result := e.Run(context.Background(), "review this code")

	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "custom script exited with error")
	assert.Contains(t, result.Output, "partial output")
}

func TestCustomExecutor_Run_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mock := &mockCustomRunner{
		runFunc: func(_ context.Context, _, _ string) (io.Reader, func() error, error) {
			return strings.NewReader(""), func() error { return context.Canceled }, nil
		},
	}
	e := &CustomExecutor{Script: "/path/to/script.sh", runner: mock}

	result := e.Run(ctx, "review this code")

	require.ErrorIs(t, result.Error, context.Canceled)
}

func TestCustomExecutor_Run_NoOutputHandler(t *testing.T) {
	mock := &mockCustomRunner{
		runFunc: func(_ context.Context, _, _ string) (io.Reader, func() error, error) {
			return strings.NewReader("output\n<<<RALPHEX:CODEX_REVIEW_DONE>>>"), func() error { return nil }, nil
		},
	}

	e := &CustomExecutor{Script: "/path/to/script.sh", runner: mock, OutputHandler: nil}
	result := e.Run(context.Background(), "review this code")

	require.NoError(t, result.Error)
	assert.Contains(t, result.Output, "output")
	assert.Equal(t, "<<<RALPHEX:CODEX_REVIEW_DONE>>>", result.Signal)
}

func TestCustomExecutor_Run_AllSignals(t *testing.T) {
	// tests all recognized signals (from detectSignal function)
	tests := []struct {
		name       string
		output     string
		wantSignal string
	}{
		{name: "ALL_TASKS_DONE", output: "done\n<<<RALPHEX:ALL_TASKS_DONE>>>", wantSignal: "<<<RALPHEX:ALL_TASKS_DONE>>>"},
		{name: "TASK_FAILED", output: "error\n<<<RALPHEX:TASK_FAILED>>>", wantSignal: "<<<RALPHEX:TASK_FAILED>>>"},
		{name: "REVIEW_DONE", output: "done\n<<<RALPHEX:REVIEW_DONE>>>", wantSignal: "<<<RALPHEX:REVIEW_DONE>>>"},
		{name: "CODEX_REVIEW_DONE", output: "done\n<<<RALPHEX:CODEX_REVIEW_DONE>>>", wantSignal: "<<<RALPHEX:CODEX_REVIEW_DONE>>>"},
		{name: "PLAN_READY", output: "done\n<<<RALPHEX:PLAN_READY>>>", wantSignal: "<<<RALPHEX:PLAN_READY>>>"},
		{name: "no signal", output: "just output\nno signal here", wantSignal: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockCustomRunner{
				runFunc: func(_ context.Context, _, _ string) (io.Reader, func() error, error) {
					return strings.NewReader(tc.output), func() error { return nil }, nil
				},
			}
			e := &CustomExecutor{Script: "/path/to/script.sh", runner: mock}

			result := e.Run(context.Background(), "prompt")

			require.NoError(t, result.Error)
			assert.Equal(t, tc.wantSignal, result.Signal)
		})
	}
}

func TestCustomExecutor_Run_ErrorPatterns(t *testing.T) {
	tests := []struct {
		name        string
		output      string
		patterns    []string
		wantError   bool
		wantPattern string
	}{
		{
			name:        "no patterns configured",
			output:      "Rate limit exceeded",
			patterns:    nil,
			wantError:   false,
			wantPattern: "",
		},
		{
			name:        "pattern not matched",
			output:      "Analysis complete: no issues found",
			patterns:    []string{"rate limit", "quota exceeded"},
			wantError:   false,
			wantPattern: "",
		},
		{
			name:        "pattern matched",
			output:      "Error: Rate limit exceeded, please try again later",
			patterns:    []string{"rate limit"},
			wantError:   true,
			wantPattern: "rate limit",
		},
		{
			name:        "case insensitive match",
			output:      "QUOTA EXCEEDED for your account",
			patterns:    []string{"quota exceeded"},
			wantError:   true,
			wantPattern: "quota exceeded",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockCustomRunner{
				runFunc: func(_ context.Context, _, _ string) (io.Reader, func() error, error) {
					return strings.NewReader(tc.output), func() error { return nil }, nil
				},
			}
			e := &CustomExecutor{
				Script:        "/path/to/script.sh",
				runner:        mock,
				ErrorPatterns: tc.patterns,
			}

			result := e.Run(context.Background(), "prompt")

			if tc.wantError {
				require.Error(t, result.Error)
				var patternErr *PatternMatchError
				require.ErrorAs(t, result.Error, &patternErr)
				assert.Equal(t, tc.wantPattern, patternErr.Pattern)
				assert.Contains(t, patternErr.HelpCmd, "/path/to/script.sh")
			} else {
				require.NoError(t, result.Error)
			}
		})
	}
}

func TestCustomExecutor_Run_LargeOutput(t *testing.T) {
	tests := []struct {
		name string
		size int
	}{
		{name: "200KB line", size: 200 * 1024},
		{name: "65MB line exceeds old scanner limit", size: 65 * 1024 * 1024},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.size >= 65*1024*1024 && testing.Short() {
				t.Skip("skipping 65MB allocation in short mode")
			}
			largeContent := strings.Repeat("x", tc.size)

			mock := &mockCustomRunner{
				runFunc: func(_ context.Context, _, _ string) (io.Reader, func() error, error) {
					return strings.NewReader(largeContent + "\n"), func() error { return nil }, nil
				},
			}

			var captured []string
			e := &CustomExecutor{
				Script:        "/path/to/script.sh",
				runner:        mock,
				OutputHandler: func(text string) { captured = append(captured, text) },
			}

			result := e.Run(context.Background(), "prompt")

			require.NoError(t, result.Error)
			assert.Equal(t, largeContent+"\n", result.Output, "large output should be fully captured")
		})
	}
}

func TestCustomExecutor_processOutput_readError(t *testing.T) {
	e := &CustomExecutor{Script: "/path/to/script.sh"}
	errReader := &failingReader{err: errors.New("read failed")}

	output, signal, err := e.processOutput(context.Background(), errReader)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "read output")
	assert.Empty(t, output)
	assert.Empty(t, signal)
}

func TestCustomExecutor_processOutput_contextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	// create a reader that provides one line, then context is canceled
	pr, pw := io.Pipe()

	go func() {
		_, _ = pw.Write([]byte("line 1\n"))
		cancel()
		_, _ = pw.Write([]byte("line 2\n"))
		pw.Close()
	}()

	e := &CustomExecutor{Script: "/path/to/script.sh"}
	_, _, err := e.processOutput(ctx, pr)

	// should return context.Canceled or nil (depending on timing)
	if err != nil {
		assert.ErrorIs(t, err, context.Canceled)
	}
}

func TestCustomExecutor_Run_PassesPromptToScript(t *testing.T) {
	var capturedPromptFile string
	mock := &mockCustomRunner{
		runFunc: func(_ context.Context, _, promptFile string) (io.Reader, func() error, error) {
			capturedPromptFile = promptFile
			return strings.NewReader("ok\n<<<RALPHEX:CODEX_REVIEW_DONE>>>"), func() error { return nil }, nil
		},
	}
	e := &CustomExecutor{Script: "/path/to/script.sh", runner: mock}

	result := e.Run(context.Background(), "test prompt content")

	require.NoError(t, result.Error)
	assert.NotEmpty(t, capturedPromptFile, "prompt file path should be passed to runner")
	assert.Contains(t, capturedPromptFile, "ralphex-custom-prompt-", "temp file should have expected prefix")
}

func TestExecCustomRunner_Run(t *testing.T) {
	// test the real runner with a simple command
	runner := &execCustomRunner{}

	// use echo which writes to stdout
	stdout, wait, err := runner.Run(context.Background(), "echo", "hello")

	require.NoError(t, err)
	require.NotNil(t, stdout)
	require.NotNil(t, wait)

	// read stdout
	data, readErr := io.ReadAll(stdout)
	require.NoError(t, readErr)
	assert.Contains(t, string(data), "hello")

	// wait should complete successfully
	err = wait()
	require.NoError(t, err)
}

func TestExecCustomRunner_Run_CommandNotFound(t *testing.T) {
	runner := &execCustomRunner{}

	// use a command that doesn't exist
	stdout, wait, err := runner.Run(context.Background(), "/nonexistent-script-12345", "arg")

	// should fail at start or wait
	if err != nil {
		assert.Contains(t, err.Error(), "start command")
	} else {
		// if start succeeded somehow, wait should fail
		assert.NotNil(t, stdout)
		err = wait()
		assert.Error(t, err)
	}
}

func TestExecCustomRunner_Run_ContextAlreadyCanceled(t *testing.T) {
	runner := &execCustomRunner{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := runner.Run(ctx, "echo", "hello")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "context already canceled")
}
