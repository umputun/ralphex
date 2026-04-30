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

func TestCodexExecutor_Run_WaitErrorWithStderr(t *testing.T) {
	// stderr content should appear in the error message when codex exits non-zero
	mock := &mockCodexRunner{
		runFunc: func(_ context.Context, _ string, _ ...string) (CodexStreams, func() error, error) {
			stderr := "--------\nworkdir: /tmp/test\n--------\nError: authentication failed\nPlease check your API key"
			return mockStreams(stderr, ""), mockWaitError(errors.New("exit status 1")), nil
		},
	}
	e := &CodexExecutor{runner: mock}

	result := e.Run(context.Background(), "analyze code")

	require.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "codex exited with error")
	assert.Contains(t, result.Error.Error(), "exit status 1")
	assert.Contains(t, result.Error.Error(), "stderr:")
	assert.Contains(t, result.Error.Error(), "authentication failed")
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
	assert.Contains(t, argsStr, `model="gpt-5.4"`)
	assert.Contains(t, argsStr, "model_reasoning_effort=xhigh")
	assert.Contains(t, argsStr, "stream_idle_timeout_ms=3600000")
	assert.Contains(t, argsStr, "--sandbox read-only")

	// prompt must not appear in CLI args (passed via stdin to avoid Windows 8191-char limit)
	assert.NotContains(t, capturedArgs, "test prompt", "prompt should be passed via stdin, not as CLI arg")
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

	// prompt must not appear in CLI args (passed via stdin to avoid Windows 8191-char limit)
	assert.NotContains(t, capturedArgs, "test", "prompt should be passed via stdin, not as CLI arg")
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
	res := e.processStderr(ctx, pr)

	// should return context.Canceled or nil (depending on timing)
	if res.err != nil {
		assert.ErrorIs(t, res.err, context.Canceled)
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

func TestExecCodexRunner_Run_Stdin(t *testing.T) {
	// test that stdin is piped to the child process (prompt via stdin for Windows compat)
	prompt := "hello from stdin"
	runner := &execCodexRunner{stdin: strings.NewReader(prompt)}

	// use cat which reads stdin and writes to stdout
	streams, wait, err := runner.Run(context.Background(), "cat")

	require.NoError(t, err)
	require.NotNil(t, streams.Stdout)

	data, readErr := io.ReadAll(streams.Stdout)
	require.NoError(t, readErr)
	assert.Equal(t, prompt, string(data))

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

	res := e.processStderr(context.Background(), errReader)

	require.Error(t, res.err)
	assert.Contains(t, res.err.Error(), "read stderr")
}

func TestCodexExecutor_processStderr_lastLines(t *testing.T) {
	tests := []struct {
		name      string
		stderr    string
		wantLines []string
	}{
		{"more than 5 lines keeps last 5", "line1\nline2\nline3\nline4\nline5\nline6\nline7\n",
			[]string{"line3", "line4", "line5", "line6", "line7"}},
		{"fewer than 5 lines keeps all", "line1\nline2\n", []string{"line1", "line2"}},
		{"empty stderr", "", nil},
		{"long lines truncated to 256 runes", strings.Repeat("x", 500) + "\n",
			[]string{strings.Repeat("x", 256) + "..."}},
		{"preserves leading whitespace", "  indented line\n\t\ttabbed line\n",
			[]string{"  indented line", "\t\ttabbed line"}},
		{"truncates by runes not bytes", strings.Repeat("ж", 300) + "\n",
			[]string{strings.Repeat("ж", 256) + "..."}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := &CodexExecutor{}
			res := e.processStderr(context.Background(), strings.NewReader(tc.stderr))
			require.NoError(t, res.err)
			assert.Equal(t, tc.wantLines, res.lastLines)
		})
	}
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
	// test that stderr lines of arbitrary length are handled without limit

	tests := []struct {
		name string
		size int
	}{
		{"100KB line", 100 * 1024},
		{"1MB line", 1024 * 1024},
		{"65MB line (exceeds old scanner limit)", 65 * 1024 * 1024},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.size >= 65*1024*1024 && testing.Short() {
				t.Skip("skipping 65MB allocation in short mode")
			}
			// create a large line in header block (which gets displayed)
			largeContent := strings.Repeat("x", tc.size)
			stderr := "--------\n" + largeContent + "\n--------\n"

			var shown []string
			e := &CodexExecutor{
				OutputHandler: func(text string) {
					shown = append(shown, strings.TrimSuffix(text, "\n"))
				},
			}

			res := e.processStderr(context.Background(), strings.NewReader(stderr))

			require.NoError(t, res.err, "should handle %d byte line without error", tc.size)
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
	exitErr := errors.New("exit status 1")
	tests := []struct {
		name        string
		stdout      string
		patterns    []string
		waitErr     error
		wantError   bool
		wantPattern string
		wantHelpCmd string
		wantOutput  string
	}{
		{
			name: "no patterns configured, exit error only", stdout: "Rate limit exceeded",
			patterns: nil, waitErr: exitErr, wantOutput: "Rate limit exceeded",
			wantError: true,
		},
		{
			name: "pattern not matched, exit error only", stdout: "Analysis complete: no issues found",
			patterns: []string{"rate limit", "quota exceeded"}, waitErr: exitErr,
			wantOutput: "Analysis complete: no issues found", wantError: true,
		},
		{
			name: "pattern matched on non-zero exit", stdout: "Error: Rate limit exceeded, please try again later",
			patterns: []string{"rate limit"}, waitErr: exitErr, wantError: true,
			wantPattern: "rate limit", wantHelpCmd: "codex /status",
			wantOutput: "Error: Rate limit exceeded, please try again later",
		},
		{
			name: "case insensitive match", stdout: "QUOTA EXCEEDED for your account",
			patterns: []string{"quota exceeded"}, waitErr: exitErr, wantError: true,
			wantPattern: "quota exceeded", wantHelpCmd: "codex /status",
			wantOutput: "QUOTA EXCEEDED for your account",
		},
		{
			name: "first matching pattern returned", stdout: "rate limit and quota exceeded",
			patterns: []string{"rate limit", "quota exceeded"}, waitErr: exitErr, wantError: true,
			wantPattern: "rate limit", wantHelpCmd: "codex /status",
			wantOutput: "rate limit and quota exceeded",
		},
		{
			name: "pattern ignored on clean exit", stdout: "found Rate limit handling issue in code",
			patterns: []string{"rate limit"}, waitErr: nil,
			wantError: false, wantOutput: "found Rate limit handling issue in code",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			waitFn := mockWait()
			if tc.waitErr != nil {
				waitFn = mockWaitError(tc.waitErr)
			}
			mock := &mockCodexRunner{
				runFunc: func(_ context.Context, _ string, _ ...string) (CodexStreams, func() error, error) {
					return mockStreams("", tc.stdout), waitFn, nil
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
				if tc.wantPattern != "" {
					var patternErr *PatternMatchError
					require.ErrorAs(t, result.Error, &patternErr)
					assert.Equal(t, tc.wantPattern, patternErr.Pattern)
					assert.Equal(t, tc.wantHelpCmd, patternErr.HelpCmd)
				}
			} else {
				require.NoError(t, result.Error)
			}
		})
	}
}

func TestCodexExecutor_Run_ErrorPattern_WithSignal(t *testing.T) {
	// error pattern should still be detected even when output contains a signal (non-zero exit)
	mock := &mockCodexRunner{
		runFunc: func(_ context.Context, _ string, _ ...string) (CodexStreams, func() error, error) {
			stdout := "Rate limit exceeded <<<RALPHEX:CODEX_REVIEW_DONE>>>"
			return mockStreams("", stdout), mockWaitError(errors.New("exit status 1")), nil
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

func TestCodexExecutor_Run_LimitPattern(t *testing.T) {
	exitErr := errors.New("exit status 1")
	tests := []struct {
		name        string
		stdout      string
		limitPat    []string
		errorPat    []string
		waitErr     error
		wantLimit   bool
		wantError   bool
		wantPattern string
	}{
		{
			name: "limit pattern matched", stdout: "Rate limit exceeded",
			limitPat: []string{"rate limit"}, errorPat: nil, waitErr: exitErr,
			wantLimit: true, wantPattern: "rate limit",
		},
		{
			name: "limit takes precedence over error", stdout: "Rate limit exceeded",
			limitPat: []string{"rate limit"}, errorPat: []string{"rate limit"}, waitErr: exitErr,
			wantLimit: true, wantPattern: "rate limit",
		},
		{
			name: "error pattern when limit does not match", stdout: "quota exceeded for account",
			limitPat: []string{"rate limit"}, errorPat: []string{"quota exceeded"}, waitErr: exitErr,
			wantError: true, wantPattern: "quota exceeded",
		},
		{
			name: "no pattern match, exit error only", stdout: "Analysis complete",
			limitPat: []string{"rate limit"}, errorPat: []string{"quota exceeded"}, waitErr: exitErr,
			wantError: true, // error from exit code, not pattern
		},
		{
			name: "patterns ignored on clean exit", stdout: "Rate limit handling code reviewed",
			limitPat: []string{"rate limit"}, errorPat: []string{"quota exceeded"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			waitFn := mockWait()
			if tc.waitErr != nil {
				waitFn = mockWaitError(tc.waitErr)
			}
			mock := &mockCodexRunner{
				runFunc: func(_ context.Context, _ string, _ ...string) (CodexStreams, func() error, error) {
					return mockStreams("", tc.stdout), waitFn, nil
				},
			}
			e := &CodexExecutor{
				runner:        mock,
				LimitPatterns: tc.limitPat,
				ErrorPatterns: tc.errorPat,
			}

			result := e.Run(context.Background(), "analyze code")

			switch {
			case tc.wantLimit:
				require.Error(t, result.Error)
				var limitErr *LimitPatternError
				require.ErrorAs(t, result.Error, &limitErr)
				assert.Equal(t, tc.wantPattern, limitErr.Pattern)
				assert.Equal(t, "codex /status", limitErr.HelpCmd)
			case tc.wantError:
				require.Error(t, result.Error)
				if tc.wantPattern != "" {
					var patternErr *PatternMatchError
					require.ErrorAs(t, result.Error, &patternErr)
					assert.Equal(t, tc.wantPattern, patternErr.Pattern)
				}
			default:
				require.NoError(t, result.Error)
			}
		})
	}
}

func TestCodexExecutor_Run_LimitPattern_ContextCanceled(t *testing.T) {
	// context cancellation must not be masked by pattern matching
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mock := &mockCodexRunner{
		runFunc: func(_ context.Context, _ string, _ ...string) (CodexStreams, func() error, error) {
			return mockStreams("", "Rate limit exceeded"), mockWaitError(context.Canceled), nil
		},
	}
	e := &CodexExecutor{
		runner:        mock,
		LimitPatterns: []string{"rate limit"},
		ErrorPatterns: []string{"rate limit"},
	}

	result := e.Run(ctx, "analyze code")

	require.ErrorIs(t, result.Error, context.Canceled, "cancellation must not be masked by pattern match")
	var limitErr *LimitPatternError
	assert.NotErrorAs(t, result.Error, &limitErr, "should not return LimitPatternError on cancellation")
	var patternErr *PatternMatchError
	assert.NotErrorAs(t, result.Error, &patternErr, "should not return PatternMatchError on cancellation")
}

func TestCodexExecutor_Run_LimitPattern_StderrMatch(t *testing.T) {
	// codex emits OpenAI/ChatGPT plan-quota errors to stderr while stdout is empty.
	// pattern check must scan stderr too, otherwise --wait can never fire.
	exitErr := errors.New("exit status 1")
	stderr := "--------\nworkdir: /tmp/test\n--------\nworking...\n" +
		"ERROR: You've hit your usage limit. Upgrade to Pro to purchase more credits.\n"

	mock := &mockCodexRunner{
		runFunc: func(_ context.Context, _ string, _ ...string) (CodexStreams, func() error, error) {
			return mockStreams(stderr, ""), mockWaitError(exitErr), nil
		},
	}
	e := &CodexExecutor{
		runner:        mock,
		LimitPatterns: []string{"You've hit your usage limit"},
	}

	result := e.Run(context.Background(), "analyze code")

	require.Error(t, result.Error)
	var limitErr *LimitPatternError
	require.ErrorAs(t, result.Error, &limitErr, "limit message in stderr must be detected")
	assert.Equal(t, "You've hit your usage limit", limitErr.Pattern)
	assert.Equal(t, "codex /status", limitErr.HelpCmd)
	assert.Empty(t, result.Output, "stderr-only match must not leak stderr content into Result.Output")
}

func TestCodexExecutor_Run_ErrorPattern_StderrMatch(t *testing.T) {
	// error pattern that appears only in stderr (e.g., auth failures) must be detected
	exitErr := errors.New("exit status 1")
	stderr := "--------\nworkdir: /tmp/test\n--------\n" +
		"Error: authentication failed, please log in again"

	mock := &mockCodexRunner{
		runFunc: func(_ context.Context, _ string, _ ...string) (CodexStreams, func() error, error) {
			return mockStreams(stderr, ""), mockWaitError(exitErr), nil
		},
	}
	e := &CodexExecutor{
		runner:        mock,
		ErrorPatterns: []string{"authentication failed"},
	}

	result := e.Run(context.Background(), "analyze code")

	require.Error(t, result.Error)
	var patternErr *PatternMatchError
	require.ErrorAs(t, result.Error, &patternErr, "error pattern in stderr must be detected")
	assert.Equal(t, "authentication failed", patternErr.Pattern)
	assert.Empty(t, result.Output, "stderr-only match must not leak stderr content into Result.Output")
}

func TestCodexExecutor_Run_LimitPattern_StderrIgnoredOnCleanExit(t *testing.T) {
	// stderr containing the pattern must be ignored when codex exits cleanly,
	// to avoid false positives from analysis text that mentions limit phrases.
	// the stderr line below would be matched on a non-zero exit (verified by
	// TestCodexExecutor_Run_LimitPattern_StderrMatch), so a clean-exit pass here
	// proves the guard at codex.go gates pattern detection on finalErr != nil.
	stderr := "--------\nworkdir: /tmp/test\n--------\n" +
		"ERROR: You've hit your usage limit (in code under review)\n"

	tests := []struct {
		name          string
		limitPat      []string
		errorPat      []string
		stdoutContent string
	}{
		{name: "limit pattern only", limitPat: []string{"You've hit your usage limit"}, stdoutContent: "Analysis complete"},
		{name: "error pattern only", errorPat: []string{"You've hit your usage limit"}, stdoutContent: "Analysis complete"},
		{name: "both patterns", limitPat: []string{"You've hit your usage limit"}, errorPat: []string{"You've hit your usage limit"}, stdoutContent: "Analysis complete"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockCodexRunner{
				runFunc: func(_ context.Context, _ string, _ ...string) (CodexStreams, func() error, error) {
					return mockStreams(stderr, tc.stdoutContent), mockWait(), nil
				},
			}
			e := &CodexExecutor{
				runner:        mock,
				LimitPatterns: tc.limitPat,
				ErrorPatterns: tc.errorPat,
			}

			result := e.Run(context.Background(), "analyze code")
			require.NoError(t, result.Error, "patterns in stderr must not trigger on clean exit")
		})
	}
}

func TestCodexExecutor_Run_LimitPattern_EvictionResistant(t *testing.T) {
	// the limit message followed by many trailing stderr lines must still be
	// detected: pattern scanning is live (per-line) so the 5-line tail buffer
	// used for human-readable error context cannot evict the match.
	exitErr := errors.New("exit status 1")
	var sb strings.Builder
	sb.WriteString("--------\nworkdir: /tmp/test\n--------\n")
	sb.WriteString("ERROR: You've hit your usage limit. Upgrade to Pro.\n")
	for i := range 20 {
		fmt.Fprintf(&sb, "trailing chatter line %d\n", i)
	}

	mock := &mockCodexRunner{
		runFunc: func(_ context.Context, _ string, _ ...string) (CodexStreams, func() error, error) {
			return mockStreams(sb.String(), ""), mockWaitError(exitErr), nil
		},
	}
	e := &CodexExecutor{
		runner:        mock,
		LimitPatterns: []string{"You've hit your usage limit"},
	}

	result := e.Run(context.Background(), "analyze code")

	require.Error(t, result.Error)
	var limitErr *LimitPatternError
	require.ErrorAs(t, result.Error, &limitErr,
		"limit message must survive eviction by trailing stderr chatter (live per-line scan)")
	assert.Equal(t, "You've hit your usage limit", limitErr.Pattern)
}

func TestCodexExecutor_Run_LimitPattern_TruncationResistant(t *testing.T) {
	// a single stderr error line longer than the 256-rune cap used for
	// error-context truncation must still be matched: pattern scanning runs
	// on the raw, untruncated line before tail capture/truncation.
	exitErr := errors.New("exit status 1")
	padding := strings.Repeat("x", 300)
	stderr := "--------\nworkdir: /tmp/test\n--------\n" +
		"ERROR: " + padding + " You've hit your usage limit\n"

	mock := &mockCodexRunner{
		runFunc: func(_ context.Context, _ string, _ ...string) (CodexStreams, func() error, error) {
			return mockStreams(stderr, ""), mockWaitError(exitErr), nil
		},
	}
	e := &CodexExecutor{
		runner:        mock,
		LimitPatterns: []string{"You've hit your usage limit"},
	}

	result := e.Run(context.Background(), "analyze code")

	require.Error(t, result.Error)
	var limitErr *LimitPatternError
	require.ErrorAs(t, result.Error, &limitErr,
		"limit message past the 256-rune line cap must still be detected (scan before truncation)")
	assert.Equal(t, "You've hit your usage limit", limitErr.Pattern)
}

func TestCodexExecutor_Run_StderrChatterIgnoredWithoutErrorPrefix(t *testing.T) {
	// progress chatter on stderr (header banners, bold summaries, model thinking)
	// must NOT trigger pattern matches even when it contains the configured pattern
	// strings — only CLI-error-prefixed lines are scanned. with empty stdout this
	// test exercises the gate in isolation: removing isCodexErrorLine would make
	// stderr.limitMatch / stderr.errorMatch fire and the assertion would fail.
	exitErr := errors.New("exit status 1")
	stderr := "--------\nworkdir: /tmp/test\n--------\n" +
		"**Reviewing rate limit handling code...**\n" +
		"the code mentions quota exceeded behavior in passing\n"

	mock := &mockCodexRunner{
		runFunc: func(_ context.Context, _ string, _ ...string) (CodexStreams, func() error, error) {
			return mockStreams(stderr, ""), mockWaitError(exitErr), nil
		},
	}
	e := &CodexExecutor{
		runner:        mock,
		LimitPatterns: []string{"rate limit"},
		ErrorPatterns: []string{"quota exceeded"},
	}

	result := e.Run(context.Background(), "analyze code")

	require.Error(t, result.Error, "non-zero exit must surface")
	// neither stderr line has an error/fatal/panic prefix, so the gate must
	// suppress both pattern matches; the surviving error is the wrapped wait error.
	var limitErr *LimitPatternError
	require.NotErrorAs(t, result.Error, &limitErr,
		"stderr chatter without error prefix must not produce LimitPatternError")
	var patternErr *PatternMatchError
	require.NotErrorAs(t, result.Error, &patternErr,
		"stderr chatter without error prefix must not produce PatternMatchError")
	assert.Contains(t, result.Error.Error(), "codex exited with error")
}

func TestCodexExecutor_Run_StdoutLimitBeatsStderrChatter(t *testing.T) {
	// when stdout has an authoritative LimitPattern match AND stderr has chatter
	// that mentions the same pattern (no error prefix), stdout wins — and the
	// stderr chatter contributes nothing because the gate suppresses it.
	exitErr := errors.New("exit status 1")
	stderr := "--------\nworkdir: /tmp/test\n--------\n" +
		"**Reviewing rate limit handling code...**\n"
	stdout := "Rate limit detected in handler.go:42\n<<<RALPHEX:CODEX_REVIEW_DONE>>>"

	mock := &mockCodexRunner{
		runFunc: func(_ context.Context, _ string, _ ...string) (CodexStreams, func() error, error) {
			return mockStreams(stderr, stdout), mockWaitError(exitErr), nil
		},
	}
	e := &CodexExecutor{
		runner:        mock,
		LimitPatterns: []string{"rate limit"},
	}

	result := e.Run(context.Background(), "analyze code")

	require.Error(t, result.Error)
	var limitErr *LimitPatternError
	require.ErrorAs(t, result.Error, &limitErr,
		"stdout LimitPattern must match — stderr chatter is suppressed by the gate")
	assert.Equal(t, "rate limit", limitErr.Pattern)
}

func TestCodexExecutor_Run_StderrLimitBeatsStdoutError(t *testing.T) {
	// when stderr has a CLI-error-prefixed limit match (real quota diagnostic) AND
	// stdout matches a configured ErrorPattern, stderr limit must win — otherwise a
	// real OpenAI quota hit gets downgraded to a non-retryable PatternMatchError and
	// --wait can never retry. the prefix gate in processStderr already prevents
	// benign stderr text from firing, so a stderr.limitMatch is trustworthy.
	exitErr := errors.New("exit status 1")
	stderr := "ERROR: You've hit your usage limit\n" // real CLI quota diagnostic
	stdout := "Authentication failed for user\n"     // partial response with ErrorPattern hit

	mock := &mockCodexRunner{
		runFunc: func(_ context.Context, _ string, _ ...string) (CodexStreams, func() error, error) {
			return mockStreams(stderr, stdout), mockWaitError(exitErr), nil
		},
	}
	e := &CodexExecutor{
		runner:        mock,
		LimitPatterns: []string{"You've hit your usage limit"},
		ErrorPatterns: []string{"Authentication failed"},
	}

	result := e.Run(context.Background(), "analyze code")

	require.Error(t, result.Error)
	var limitErr *LimitPatternError
	require.ErrorAs(t, result.Error, &limitErr,
		"stderr limit (CLI quota diagnostic) must trump stdout error pattern so --wait can retry")
	assert.Equal(t, "You've hit your usage limit", limitErr.Pattern)
	var patternErr *PatternMatchError
	assert.NotErrorAs(t, result.Error, &patternErr,
		"must not downgrade to PatternMatchError when a real stderr quota diagnostic fires")
}

func TestCodexExecutor_Run_StdoutLimitBeatsStderrError(t *testing.T) {
	// stdout limit match (intentional, in actual response) trumps stderr error match.
	// limit-class wins across both sources before any error-class match, but within
	// the same class stdout wins over stderr.
	exitErr := errors.New("exit status 1")
	stderr := "ERROR: server error during request\n"
	stdout := "ratelimit detected in handler\n"

	mock := &mockCodexRunner{
		runFunc: func(_ context.Context, _ string, _ ...string) (CodexStreams, func() error, error) {
			return mockStreams(stderr, stdout), mockWaitError(exitErr), nil
		},
	}
	e := &CodexExecutor{
		runner:        mock,
		LimitPatterns: []string{"ratelimit"},
		ErrorPatterns: []string{"server error"},
	}

	result := e.Run(context.Background(), "analyze code")

	require.Error(t, result.Error)
	var limitErr *LimitPatternError
	require.ErrorAs(t, result.Error, &limitErr,
		"stdout limit pattern must beat stderr error pattern (limit class wins, stdout source wins within class)")
	assert.Equal(t, "ratelimit", limitErr.Pattern)
}

func TestCodexExecutor_Run_LimitPattern_StderrPriority(t *testing.T) {
	// when both limit and error patterns match on stderr, limit must win
	exitErr := errors.New("exit status 1")
	stderr := "--------\nworkdir: /tmp/test\n--------\n" +
		"ERROR: rate limit + quota exceeded\n"

	mock := &mockCodexRunner{
		runFunc: func(_ context.Context, _ string, _ ...string) (CodexStreams, func() error, error) {
			return mockStreams(stderr, ""), mockWaitError(exitErr), nil
		},
	}
	e := &CodexExecutor{
		runner:        mock,
		LimitPatterns: []string{"rate limit"},
		ErrorPatterns: []string{"quota exceeded"},
	}

	result := e.Run(context.Background(), "analyze code")

	require.Error(t, result.Error)
	var limitErr *LimitPatternError
	require.ErrorAs(t, result.Error, &limitErr, "limit pattern must take priority over error pattern on stderr path")
	assert.Equal(t, "rate limit", limitErr.Pattern)
}

func TestCodexExecutor_Run_LimitPattern_StderrCancellation(t *testing.T) {
	// context cancellation must not be masked by a stderr-only pattern match
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	stderr := "ERROR: You've hit your usage limit\n"

	mock := &mockCodexRunner{
		runFunc: func(_ context.Context, _ string, _ ...string) (CodexStreams, func() error, error) {
			return mockStreams(stderr, ""), mockWaitError(context.Canceled), nil
		},
	}
	e := &CodexExecutor{
		runner:        mock,
		LimitPatterns: []string{"You've hit your usage limit"},
		ErrorPatterns: []string{"You've hit your usage limit"},
	}

	result := e.Run(ctx, "analyze code")

	require.ErrorIs(t, result.Error, context.Canceled, "cancellation must not be masked by stderr pattern match")
	var limitErr *LimitPatternError
	assert.NotErrorAs(t, result.Error, &limitErr)
	var patternErr *PatternMatchError
	assert.NotErrorAs(t, result.Error, &patternErr)
}

func TestIsCodexErrorLine(t *testing.T) {
	tests := []struct {
		name string
		line string
		want bool
	}{
		{"ERROR prefix", "ERROR: You've hit your usage limit", true},
		{"Error prefix mixed case", "Error: authentication failed", true},
		{"error prefix lower", "error: something went wrong", true},
		{"FATAL prefix", "FATAL: cannot continue", true},
		{"panic prefix", "panic: nil pointer", true},
		{"leading whitespace then ERROR", "  ERROR: indented", true},
		{"empty", "", false},
		{"whitespace only", "   ", false},
		{"bold summary", "**Reviewing rate limit handling**", false},
		{"separator", "--------", false},
		{"prose mentioning error", "the code logs an error: foo", false},
		{"prose mentioning rate limit", "rate limit handling looks fine", false},
		{"header", "workdir: /tmp/test", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isCodexErrorLine(tc.line))
		})
	}
}
