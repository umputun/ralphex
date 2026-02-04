package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockCodexRunner implements CodexRunner for testing.
type mockCodexRunner struct {
	runFunc func(ctx context.Context, name string, args ...string) (CodexStreams, func() error, error)
}

func (m *mockCodexRunner) Run(ctx context.Context, name string, args ...string) (CodexStreams, func() error, error) {
	return m.runFunc(ctx, name, args...)
}

// mockStreams creates CodexStreams from stderr and stdout content.
func mockStreams(stderr, stdout string) CodexStreams {
	return CodexStreams{
		Stderr: strings.NewReader(stderr),
		Stdout: strings.NewReader(stdout),
	}
}

// mockWait returns a wait function that returns nil.
func mockWait() func() error {
	return func() error { return nil }
}

// mockWaitError returns a wait function that returns the given error.
func mockWaitError(err error) func() error {
	return func() error { return err }
}

func TestCodexExecutor_Run_Success(t *testing.T) {
	// stdout contains the actual response (captured in Result.Output)
	// stderr contains progress info (streamed to OutputHandler)
	mock := &mockCodexRunner{
		runFunc: func(_ context.Context, _ string, _ ...string) (CodexStreams, func() error, error) {
			stderr := "--------\nmodel: gpt-5\n--------\n**Analyzing...**\n"
			stdout := "Analysis complete: no issues found.\n<<<RALPHEX:CODEX_REVIEW_DONE>>>"
			return mockStreams(stderr, stdout), mockWait(), nil
		},
	}
	e := &CodexExecutor{runner: mock}

	result := e.Run(context.Background(), "analyze code")

	require.NoError(t, result.Error)
	assert.Contains(t, result.Output, "Analysis complete")
	assert.Equal(t, "<<<RALPHEX:CODEX_REVIEW_DONE>>>", result.Signal)
}

func TestCodexExecutor_Run_StreamsStderr(t *testing.T) {
	// stderr contains header block and bold summaries for progress display
	stderr := `--------
OpenAI Codex v1.2.3
model: gpt-5
workdir: /tmp/test
sandbox: read-only
--------
Some thinking noise
**Summary: Found 2 issues**
More thinking
**Details: processing...**
Even more noise`

	stdout := `Final response from codex.
<<<RALPHEX:CODEX_REVIEW_DONE>>>`

	mock := &mockCodexRunner{
		runFunc: func(_ context.Context, _ string, _ ...string) (CodexStreams, func() error, error) {
			return mockStreams(stderr, stdout), mockWait(), nil
		},
	}

	var streamedLines []string
	e := &CodexExecutor{
		runner:        mock,
		OutputHandler: func(text string) { streamedLines = append(streamedLines, strings.TrimSuffix(text, "\n")) },
	}

	result := e.Run(context.Background(), "analyze code")

	require.NoError(t, result.Error)

	// verify header block is shown (between first two "--------" separators)
	assert.Contains(t, streamedLines, "--------", "separator should be shown")
	assert.Contains(t, streamedLines, "OpenAI Codex v1.2.3", "header content should be shown")
	assert.Contains(t, streamedLines, "model: gpt-5", "header content should be shown")
	assert.Contains(t, streamedLines, "sandbox: read-only", "header content should be shown")

	// verify bold summaries are shown (stripped of ** markers)
	assert.Contains(t, streamedLines, "Summary: Found 2 issues", "bold summary should be shown")
	assert.Contains(t, streamedLines, "Details: processing...", "bold summary should be shown")

	// verify noise is filtered
	for _, line := range streamedLines {
		assert.NotContains(t, line, "Some thinking noise", "thinking noise should be filtered")
		assert.NotContains(t, line, "More thinking", "noise should be filtered")
		assert.NotContains(t, line, "Even more noise", "noise should be filtered")
	}

	// verify Result.Output contains stdout (the actual response)
	assert.Contains(t, result.Output, "Final response from codex")
	assert.Equal(t, "<<<RALPHEX:CODEX_REVIEW_DONE>>>", result.Signal)
}

func TestCodexExecutor_Run_StdoutIsResult(t *testing.T) {
	// verify that Result.Output contains stdout content, not stderr
	stderr := "--------\nheader\n--------\n**progress**\nthinking noise\n"
	stdout := "This is the actual answer from codex."

	mock := &mockCodexRunner{
		runFunc: func(_ context.Context, _ string, _ ...string) (CodexStreams, func() error, error) {
			return mockStreams(stderr, stdout), mockWait(), nil
		},
	}

	e := &CodexExecutor{runner: mock}
	result := e.Run(context.Background(), "analyze code")

	require.NoError(t, result.Error)
	assert.Equal(t, stdout, result.Output, "Result.Output should be stdout content")
	assert.NotContains(t, result.Output, "progress", "stderr content should not be in Result.Output")
	assert.NotContains(t, result.Output, "thinking noise", "stderr content should not be in Result.Output")
}

func TestCodexExecutor_Run_StartError(t *testing.T) {
	mock := &mockCodexRunner{
		runFunc: func(_ context.Context, _ string, _ ...string) (CodexStreams, func() error, error) {
			return CodexStreams{}, nil, errors.New("command not found")
		},
	}
	e := &CodexExecutor{runner: mock}

	result := e.Run(context.Background(), "analyze code")

	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "start codex")
	assert.Contains(t, result.Error.Error(), "command not found")
}

func TestCodexExecutor_Run_WaitError(t *testing.T) {
	mock := &mockCodexRunner{
		runFunc: func(_ context.Context, _ string, _ ...string) (CodexStreams, func() error, error) {
			return mockStreams("", "partial output"), mockWaitError(errors.New("exit 1")), nil
		},
	}
	e := &CodexExecutor{runner: mock}

	result := e.Run(context.Background(), "analyze code")

	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "codex exited with error")
	assert.Equal(t, "partial output", result.Output)
}

func TestCodexExecutor_Run_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mock := &mockCodexRunner{
		runFunc: func(_ context.Context, _ string, _ ...string) (CodexStreams, func() error, error) {
			return mockStreams("", ""), mockWaitError(context.Canceled), nil
		},
	}
	e := &CodexExecutor{runner: mock}

	result := e.Run(ctx, "analyze code")

	require.ErrorIs(t, result.Error, context.Canceled)
}

func TestCodexExecutor_Run_DefaultSettings(t *testing.T) {
	// clear docker env to test default sandbox behavior
	t.Setenv("RALPHEX_DOCKER", "")

	var capturedArgs []string
	mock := &mockCodexRunner{
		runFunc: func(_ context.Context, name string, args ...string) (CodexStreams, func() error, error) {
			capturedArgs = args
			return mockStreams("", "result"), mockWait(), nil
		},
	}
	e := &CodexExecutor{runner: mock}

	result := e.Run(context.Background(), "test prompt")

	require.NoError(t, result.Error)

	// verify default settings
	argsStr := strings.Join(capturedArgs, " ")
	assert.Contains(t, argsStr, `model="gpt-5.2-codex"`)
	assert.Contains(t, argsStr, "model_reasoning_effort=xhigh")
	assert.Contains(t, argsStr, "stream_idle_timeout_ms=3600000")
	assert.Contains(t, argsStr, "--sandbox read-only")
}

func TestCodexExecutor_Run_CustomSettings(t *testing.T) {
	// clear docker env to test custom sandbox setting
	t.Setenv("RALPHEX_DOCKER", "")

	var capturedCmd string
	var capturedArgs []string
	mock := &mockCodexRunner{
		runFunc: func(_ context.Context, name string, args ...string) (CodexStreams, func() error, error) {
			capturedCmd = name
			capturedArgs = args
			return mockStreams("", "result"), mockWait(), nil
		},
	}
	e := &CodexExecutor{
		runner:          mock,
		Command:         "custom-codex",
		Model:           "gpt-4o",
		ReasoningEffort: "medium",
		TimeoutMs:       1000,
		Sandbox:         "off",
		ProjectDoc:      "/path/to/doc.md",
	}

	result := e.Run(context.Background(), "test")

	require.NoError(t, result.Error)
	assert.Equal(t, "custom-codex", capturedCmd)

	// verify custom settings
	assert.Equal(t, "exec", capturedArgs[0])
	assert.True(t, slices.Contains(capturedArgs, `model="gpt-4o"`), "expected model setting in args: %v", capturedArgs)

	argsStr := strings.Join(capturedArgs, " ")
	assert.Contains(t, argsStr, "model_reasoning_effort=medium")
	assert.Contains(t, argsStr, "stream_idle_timeout_ms=1000")
	assert.Contains(t, argsStr, "--sandbox off")
	assert.Contains(t, argsStr, `project_doc="/path/to/doc.md"`)
}

func TestCodexExecutor_shouldDisplay_headerBlock(t *testing.T) {
	e := &CodexExecutor{}

	tests := []struct {
		name       string
		lines      []string
		wantShown  []string
		wantHidden []string
	}{
		{
			name: "header block between separators",
			lines: []string{
				"--------",
				"OpenAI Codex v1.2.3",
				"model: gpt-5",
				"workdir: /tmp/test",
				"--------",
				"noise after header",
			},
			wantShown:  []string{"--------", "OpenAI Codex v1.2.3", "model: gpt-5", "workdir: /tmp/test", "--------"},
			wantHidden: []string{"noise after header"},
		},
		{
			name: "third separator not shown",
			lines: []string{
				"--------",
				"header content",
				"--------",
				"--------",
				"more content",
			},
			wantShown:  []string{"--------", "header content", "--------"},
			wantHidden: []string{"more content"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := &codexFilterState{}
			var shown []string
			for _, line := range tc.lines {
				if ok, out := e.shouldDisplay(line, state); ok {
					shown = append(shown, out)
				}
			}
			for _, want := range tc.wantShown {
				assert.Contains(t, shown, want, "expected shown: %s", want)
			}
			for _, notWant := range tc.wantHidden {
				assert.NotContains(t, shown, notWant, "expected hidden: %s", notWant)
			}
			// verify separator count matches expected
			wantSepCount, gotSepCount := 0, 0
			for _, s := range tc.wantShown {
				if s == "--------" {
					wantSepCount++
				}
			}
			for _, s := range shown {
				if s == "--------" {
					gotSepCount++
				}
			}
			assert.Equal(t, wantSepCount, gotSepCount, "separator count mismatch")
		})
	}
}

func TestCodexExecutor_shouldDisplay_boldSummaries(t *testing.T) {
	e := &CodexExecutor{}

	tests := []struct {
		name    string
		line    string
		state   *codexFilterState
		wantOk  bool
		wantOut string
	}{
		{
			name:    "bold shown after header",
			line:    "**Summary: Found issues**",
			state:   &codexFilterState{headerCount: 2},
			wantOk:  true,
			wantOut: "Summary: Found issues",
		},
		{
			name:    "bold shown before header ends",
			line:    "**Progress...**",
			state:   &codexFilterState{headerCount: 0},
			wantOk:  true,
			wantOut: "Progress...",
		},
		{
			name:    "non-bold filtered after header",
			line:    "Some random noise",
			state:   &codexFilterState{headerCount: 2},
			wantOk:  false,
			wantOut: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ok, out := e.shouldDisplay(tc.line, tc.state)
			assert.Equal(t, tc.wantOk, ok)
			assert.Equal(t, tc.wantOut, out)
		})
	}
}

func TestCodexExecutor_shouldDisplay_emptyAndWhitespace(t *testing.T) {
	e := &CodexExecutor{}
	state := &codexFilterState{headerCount: 1}

	tests := []struct {
		line   string
		wantOk bool
	}{
		{"", false},
		{"   ", false},
		{"\t", false},
		{"\n", false},
	}

	for i, tc := range tests {
		t.Run(fmt.Sprintf("whitespace_case_%d", i), func(t *testing.T) {
			ok, _ := e.shouldDisplay(tc.line, state)
			assert.Equal(t, tc.wantOk, ok)
		})
	}
}

func TestCodexExecutor_stripBold(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"no bold", "plain text", "plain text"},
		{"single bold", "**bold** text", "bold text"},
		{"multiple bold", "**one** and **two**", "one and two"},
		{"nested in text", "before **middle** after", "before middle after"},
		{"unclosed bold", "**unclosed text", "**unclosed text"},
		{"empty bold", "**** empty", " empty"},
	}

	e := &CodexExecutor{}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := e.stripBold(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestCodexExecutor_Run_NoOutputHandler(t *testing.T) {
	// verify run works without output handler
	mock := &mockCodexRunner{
		runFunc: func(_ context.Context, _ string, _ ...string) (CodexStreams, func() error, error) {
			return mockStreams("**progress**", "actual output"), mockWait(), nil
		},
	}

	e := &CodexExecutor{runner: mock, OutputHandler: nil}
	result := e.Run(context.Background(), "analyze code")

	require.NoError(t, result.Error)
	assert.Equal(t, "actual output", result.Output)
}

func TestCodexExecutor_processStderr_contextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	// create a reader that provides one line, then context is canceled
	pr, pw := io.Pipe()

	go func() {
		_, _ = pw.Write([]byte("line 1\n"))
		cancel()
		_, _ = pw.Write([]byte("line 2\n"))
		pw.Close()
	}()

	e := &CodexExecutor{}
	err := e.processStderr(ctx, pr)

	// should return context.Canceled or nil (depending on timing)
	if err != nil {
		assert.ErrorIs(t, err, context.Canceled)
	}
}

func TestExecCodexRunner_Run(t *testing.T) {
	// test the real runner with a simple command
	runner := &execCodexRunner{}

	// use echo which writes to stdout
	streams, wait, err := runner.Run(context.Background(), "echo", "hello")

	require.NoError(t, err)
	require.NotNil(t, streams.Stdout)
	require.NotNil(t, streams.Stderr)
	require.NotNil(t, wait)

	// read stdout
	data, readErr := io.ReadAll(streams.Stdout)
	require.NoError(t, readErr)
	assert.Contains(t, string(data), "hello")

	// wait should complete successfully
	err = wait()
	require.NoError(t, err)
}

func TestExecCodexRunner_Run_CommandNotFound(t *testing.T) {
	runner := &execCodexRunner{}

	// use a command that doesn't exist
	streams, wait, err := runner.Run(context.Background(), "nonexistent-command-12345")

	// should fail at start or wait
	if err != nil {
		assert.Contains(t, err.Error(), "start command")
	} else {
		// if start succeeded, wait should fail
		assert.NotNil(t, streams.Stdout)
		err = wait()
		assert.Error(t, err)
	}
}

func TestCodexExecutor_readStdout(t *testing.T) {
	e := &CodexExecutor{}

	content := "This is the stdout content\nWith multiple lines\n"
	result, err := e.readStdout(strings.NewReader(content))

	require.NoError(t, err)
	assert.Equal(t, content, result)
}

// failingReader is a reader that always returns an error.
type failingReader struct {
	err error
}

func (r *failingReader) Read(_ []byte) (int, error) {
	return 0, r.err
}

func TestCodexExecutor_processStderr_readError(t *testing.T) {
	e := &CodexExecutor{}
	errReader := &failingReader{err: errors.New("read failed")}

	err := e.processStderr(context.Background(), errReader)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "read stderr")
}

func TestCodexExecutor_readStdout_error(t *testing.T) {
	e := &CodexExecutor{}
	errReader := &failingReader{err: errors.New("read failed")}

	_, err := e.readStdout(errReader)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "read stdout")
}

func TestCodexExecutor_Run_ErrorPriority(t *testing.T) {
	// stderr error should take priority over wait error
	mock := &mockCodexRunner{
		runFunc: func(_ context.Context, _ string, _ ...string) (CodexStreams, func() error, error) {
			return CodexStreams{
				Stderr: &failingReader{err: errors.New("stderr failed")},
				Stdout: strings.NewReader("output"),
			}, mockWaitError(errors.New("wait failed")), nil
		},
	}
	e := &CodexExecutor{runner: mock}

	result := e.Run(context.Background(), "test")

	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "stderr")
}

func TestCodexExecutor_shouldDisplay_deduplication(t *testing.T) {
	e := &CodexExecutor{}

	tests := []struct {
		name       string
		lines      []string
		wantShown  []string
		wantHidden []string
	}{
		{
			name: "duplicate bold summaries filtered",
			lines: []string{
				"--------",
				"header",
				"--------",
				"**Findings**",
				"**Questions**",
				"**Change Summary**",
				"**Findings**",       // duplicate
				"**Questions**",      // duplicate
				"**Change Summary**", // duplicate
			},
			wantShown:  []string{"--------", "header", "Findings", "Questions", "Change Summary"},
			wantHidden: []string{},
		},
		{
			name: "non-consecutive duplicates filtered",
			lines: []string{
				"--------",
				"model: gpt-5",
				"--------",
				"**Processing...**",
				"**Done**",
				"**Processing...**", // non-consecutive duplicate
			},
			wantShown:  []string{"--------", "model: gpt-5", "Processing...", "Done"},
			wantHidden: []string{},
		},
		{
			name: "separators not deduplicated",
			lines: []string{
				"--------",
				"header content",
				"--------",
				"--------", // third separator, should not be shown (headerCount > 2)
			},
			wantShown:  []string{"--------", "header content", "--------"},
			wantHidden: []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := &codexFilterState{}
			var shown []string
			for _, line := range tc.lines {
				if ok, out := e.shouldDisplay(line, state); ok {
					shown = append(shown, out)
				}
			}

			// verify all expected lines are shown at least once
			for _, want := range tc.wantShown {
				assert.True(t, slices.Contains(shown, want), "expected %q to appear in shown output", want)
			}

			// verify no duplicates in shown output (except separators which can repeat)
			seenLines := make(map[string]int)
			for _, s := range shown {
				seenLines[s]++
			}
			for line, count := range seenLines {
				if line == "--------" {
					// separators can appear twice (start and end of header block)
					assert.LessOrEqual(t, count, 2, "separator should appear at most twice")
				} else {
					assert.Equal(t, 1, count, "duplicate line found: %q", line)
				}
			}
		})
	}
}

func TestCodexExecutor_processStderr_largeLines(t *testing.T) {
	// test that stderr lines larger than 64KB (default bufio.Scanner limit) are handled
	// this was the "token too long" bug fix

	tests := []struct {
		name string
		size int
	}{
		{"100KB line", 100 * 1024},
		{"500KB line", 500 * 1024},
		{"1MB line", 1024 * 1024},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// create a large line in header block (which gets displayed)
			largeContent := strings.Repeat("x", tc.size)
			stderr := "--------\n" + largeContent + "\n--------\n"

			var shown []string
			e := &CodexExecutor{
				OutputHandler: func(text string) {
					shown = append(shown, strings.TrimSuffix(text, "\n"))
				},
			}

			err := e.processStderr(context.Background(), strings.NewReader(stderr))

			require.NoError(t, err, "should handle %d byte line without error", tc.size)
			assert.Contains(t, shown, largeContent, "large content should be captured")
		})
	}
}

func TestCodexExecutor_Run_largeOutput(t *testing.T) {
	// test end-to-end with large stderr and stdout
	largeStderr := strings.Repeat("x", 200*1024) // 200KB
	largeStdout := strings.Repeat("y", 500*1024) // 500KB

	mock := &mockCodexRunner{
		runFunc: func(_ context.Context, _ string, _ ...string) (CodexStreams, func() error, error) {
			stderr := "--------\n" + largeStderr + "\n--------\n"
			return mockStreams(stderr, largeStdout), mockWait(), nil
		},
	}

	var captured []string
	e := &CodexExecutor{
		runner:        mock,
		OutputHandler: func(text string) { captured = append(captured, text) },
	}

	result := e.Run(context.Background(), "test")

	require.NoError(t, result.Error)
	assert.Equal(t, largeStdout, result.Output, "stdout should be fully captured")
	// verify large stderr was processed (appears in header block)
	found := false
	for _, c := range captured {
		if strings.Contains(c, strings.Repeat("x", 100)) {
			found = true
			break
		}
	}
	assert.True(t, found, "large stderr content should be captured")
}

func TestCodexExecutor_Run_ErrorPattern(t *testing.T) {
	tests := []struct {
		name        string
		stdout      string
		patterns    []string
		wantError   bool
		wantPattern string
		wantHelpCmd string
		wantOutput  string
	}{
		{
			name:       "no patterns configured",
			stdout:     "Rate limit exceeded",
			patterns:   nil,
			wantError:  false,
			wantOutput: "Rate limit exceeded",
		},
		{
			name:       "pattern not matched",
			stdout:     "Analysis complete: no issues found",
			patterns:   []string{"rate limit", "quota exceeded"},
			wantError:  false,
			wantOutput: "Analysis complete: no issues found",
		},
		{
			name:        "pattern matched",
			stdout:      "Error: Rate limit exceeded, please try again later",
			patterns:    []string{"rate limit"},
			wantError:   true,
			wantPattern: "rate limit",
			wantHelpCmd: "codex /status",
			wantOutput:  "Error: Rate limit exceeded, please try again later",
		},
		{
			name:        "case insensitive match",
			stdout:      "QUOTA EXCEEDED for your account",
			patterns:    []string{"quota exceeded"},
			wantError:   true,
			wantPattern: "quota exceeded",
			wantHelpCmd: "codex /status",
			wantOutput:  "QUOTA EXCEEDED for your account",
		},
		{
			name:        "first matching pattern returned",
			stdout:      "rate limit and quota exceeded",
			patterns:    []string{"rate limit", "quota exceeded"},
			wantError:   true,
			wantPattern: "rate limit",
			wantHelpCmd: "codex /status",
			wantOutput:  "rate limit and quota exceeded",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockCodexRunner{
				runFunc: func(_ context.Context, _ string, _ ...string) (CodexStreams, func() error, error) {
					return mockStreams("", tc.stdout), mockWait(), nil
				},
			}
			e := &CodexExecutor{
				runner:        mock,
				ErrorPatterns: tc.patterns,
			}

			result := e.Run(context.Background(), "analyze code")

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

func TestCodexExecutor_Run_ErrorPattern_WithSignal(t *testing.T) {
	// error pattern should still be detected even when output contains a signal
	mock := &mockCodexRunner{
		runFunc: func(_ context.Context, _ string, _ ...string) (CodexStreams, func() error, error) {
			stdout := "Rate limit exceeded <<<RALPHEX:CODEX_REVIEW_DONE>>>"
			return mockStreams("", stdout), mockWait(), nil
		},
	}
	e := &CodexExecutor{
		runner:        mock,
		ErrorPatterns: []string{"rate limit"},
	}

	result := e.Run(context.Background(), "analyze code")

	// should have error due to pattern match
	require.Error(t, result.Error)
	var patternErr *PatternMatchError
	require.ErrorAs(t, result.Error, &patternErr)
	assert.Equal(t, "rate limit", patternErr.Pattern)
	assert.Equal(t, "codex /status", patternErr.HelpCmd)

	// should preserve output and signal
	assert.Contains(t, result.Output, "Rate limit exceeded")
	assert.Equal(t, "<<<RALPHEX:CODEX_REVIEW_DONE>>>", result.Signal)
}
