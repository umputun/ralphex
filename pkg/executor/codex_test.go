package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

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

func TestExecCodexRunner_childEnv(t *testing.T) {
	tests := []struct {
		name              string
		stripAnthropicKey bool
		env               []string
		want              []string
	}{
		{
			name:              "first-class --codex strips ANTHROPIC_API_KEY and CLAUDECODE",
			stripAnthropicKey: true,
			env:               []string{"PATH=/usr/bin", "CLAUDECODE=1", "ANTHROPIC_API_KEY=secret", "HOME=/home/user"},
			want:              []string{"PATH=/usr/bin", "HOME=/home/user"},
		},
		{
			name:              "external codex review (claude mode) preserves ANTHROPIC_API_KEY",
			stripAnthropicKey: false,
			env:               []string{"PATH=/usr/bin", "CLAUDECODE=1", "ANTHROPIC_API_KEY=secret", "HOME=/home/user"},
			want:              []string{"PATH=/usr/bin", "ANTHROPIC_API_KEY=secret", "HOME=/home/user"},
		},
		{
			name:              "CLAUDECODE always stripped regardless of mode",
			stripAnthropicKey: false,
			env:               []string{"PATH=/usr/bin", "CLAUDECODE=1", "HOME=/home/user"},
			want:              []string{"PATH=/usr/bin", "HOME=/home/user"},
		},
		{
			name:              "does not match partial keys like ANTHROPIC_API_KEY_OLD when stripping",
			stripAnthropicKey: true,
			env:               []string{"ANTHROPIC_API_KEY_OLD=old", "ANTHROPIC_API_KEY=new", "CLAUDECODE=1"},
			want:              []string{"ANTHROPIC_API_KEY_OLD=old"},
		},
		{
			name:              "passes through other keys unchanged",
			stripAnthropicKey: true,
			env:               []string{"OPENAI_API_KEY=ok", "FOO=bar"},
			want:              []string{"OPENAI_API_KEY=ok", "FOO=bar"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &execCodexRunner{stripAnthropicKey: tc.stripAnthropicKey}
			got := r.childEnv(tc.env)
			assert.Equal(t, tc.want, got)
		})
	}
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
	// header block (workdir/model/sandbox/session-id) is now suppressed to
	// match the claude executor; only bold summaries flow through stderr.
	// session id is still captured internally for the rollout tailer (covered
	// by TestCodexExecutor_processStderr_emitsSessionID).
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

	// on this first run, codex's resolved config (model/sandbox/effort) leaks
	// through the header block so the user knows what codex actually picked.
	// other header lines (separators, OpenAI banner, workdir) stay hidden.
	assert.NotContains(t, streamedLines, "--------", "separator must not appear")
	assert.Contains(t, streamedLines, "model: gpt-5", "first-run model: line should leak")
	assert.Contains(t, streamedLines, "sandbox: read-only", "first-run sandbox: line should leak")
	for _, hidden := range []string{"OpenAI Codex v1.2.3", "workdir: /tmp/test"} {
		assert.NotContains(t, streamedLines, hidden, "header line %q must be hidden", hidden)
	}

	// verify bold summaries are still shown (stripped of ** markers)
	assert.Contains(t, streamedLines, "Summary: Found 2 issues", "bold summary should be shown")
	assert.Contains(t, streamedLines, "Details: processing...", "bold summary should be shown")

	// verify non-bold post-header noise is filtered
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

	// verify default settings: model and reasoning effort must NOT be set on the codex CLI
	// when ralphex config does not specify them — codex inherits from ~/.codex/config.toml
	argsStr := strings.Join(capturedArgs, " ")
	assert.NotContains(t, argsStr, "-c model=", "model must not be passed when not explicitly configured")
	assert.NotContains(t, argsStr, "model_reasoning_effort=", "reasoning effort must not be passed when not explicitly configured")
	assert.Contains(t, argsStr, "stream_idle_timeout_ms=3600000")
	assert.Contains(t, argsStr, "--sandbox read-only")
	assert.NotContains(t, argsStr, "--dangerously-bypass-approvals-and-sandbox",
		"sandbox bypass must not be emitted for read-only mode")

	// prompt must not appear in CLI args (passed via stdin to avoid Windows 8191-char limit)
	assert.NotContains(t, capturedArgs, "test prompt", "prompt should be passed via stdin, not as CLI arg")
}

func TestCodexExecutor_Run_DangerFullAccessBypassesSandbox(t *testing.T) {
	t.Setenv("RALPHEX_DOCKER", "")

	t.Run("first-class --codex emits bypass flag", func(t *testing.T) {
		// MultiAgent=true is the first-class --codex signal (set by buildCodexExecutor).
		// danger-full-access requires the bypass flag for unattended runs.
		var capturedArgs []string
		mock := &mockCodexRunner{
			runFunc: func(_ context.Context, _ string, args ...string) (CodexStreams, func() error, error) {
				capturedArgs = args
				return mockStreams("", "result"), mockWait(), nil
			},
		}
		e := &CodexExecutor{runner: mock, Sandbox: "danger-full-access", MultiAgent: true}

		result := e.Run(context.Background(), "test prompt")

		require.NoError(t, result.Error)
		argsStr := strings.Join(capturedArgs, " ")
		assert.Contains(t, argsStr, "--dangerously-bypass-approvals-and-sandbox")
		assert.Contains(t, argsStr, "--sandbox danger-full-access")
	})

	t.Run("external codex review (claude mode) omits bypass flag in danger-full-access", func(t *testing.T) {
		// MultiAgent=false signals external codex review (built by buildExternalCodexExecutor).
		// Master never emitted --dangerously-bypass-approvals-and-sandbox; gating it on MultiAgent
		// preserves master semantics for default-claude users (esp. Docker mode where the sandbox
		// is forced to danger-full-access).
		var capturedArgs []string
		mock := &mockCodexRunner{
			runFunc: func(_ context.Context, _ string, args ...string) (CodexStreams, func() error, error) {
				capturedArgs = args
				return mockStreams("", "result"), mockWait(), nil
			},
		}
		e := &CodexExecutor{runner: mock, Sandbox: "danger-full-access"}

		result := e.Run(context.Background(), "test prompt")

		require.NoError(t, result.Error)
		argsStr := strings.Join(capturedArgs, " ")
		assert.NotContains(t, argsStr, "--dangerously-bypass-approvals-and-sandbox",
			"external codex review must keep master semantics — no bypass flag")
		assert.Contains(t, argsStr, "--sandbox danger-full-access")
	})
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
	assert.NotContains(t, argsStr, "--dangerously-bypass-approvals-and-sandbox",
		"sandbox bypass must only fire for danger-full-access mode")

	// prompt must not appear in CLI args (passed via stdin to avoid Windows 8191-char limit)
	assert.NotContains(t, capturedArgs, "test", "prompt should be passed via stdin, not as CLI arg")
}

func TestCodexExecutor_shouldDisplay_headerBlock(t *testing.T) {
	// the startup banner (separators + workdir/model/session-id lines) is
	// intentionally suppressed to match claude executor and avoid repeating
	// the same config block per task/review iteration. only bold summaries
	// outside the header block flow through to OutputHandler.
	e := &CodexExecutor{}

	tests := []struct {
		name       string
		lines      []string
		wantHidden []string
	}{
		{
			name: "header block between separators suppressed",
			lines: []string{
				"--------",
				"OpenAI Codex v1.2.3",
				"model: gpt-5",
				"workdir: /tmp/test",
				"--------",
				"noise after header",
			},
			wantHidden: []string{"--------", "OpenAI Codex v1.2.3", "model: gpt-5", "workdir: /tmp/test", "noise after header"},
		},
		{
			name: "trailing separators also suppressed",
			lines: []string{
				"--------",
				"header content",
				"--------",
				"--------",
				"more content",
			},
			wantHidden: []string{"--------", "header content", "more content"},
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
			assert.Empty(t, shown, "header block must produce no displayed output")
			for _, notWant := range tc.wantHidden {
				assert.NotContains(t, shown, notWant, "expected hidden: %s", notWant)
			}
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
	res := e.processStderr(ctx, pr, nil, nil, false)

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

	res := e.processStderr(context.Background(), errReader, nil, nil, false)

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
			res := e.processStderr(context.Background(), strings.NewReader(tc.stderr), nil, nil, false)
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
	// header block is now suppressed entirely; only bold summaries flow through
	// and they are deduplicated so the same "**X**" line shown twice in a row
	// (or with other lines between) is emitted only once.
	e := &CodexExecutor{}

	tests := []struct {
		name      string
		lines     []string
		wantShown []string
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
			wantShown: []string{"Findings", "Questions", "Change Summary"},
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
			wantShown: []string{"Processing...", "Done"},
		},
		{
			name: "separators alone produce no output",
			lines: []string{
				"--------",
				"header content",
				"--------",
				"--------",
			},
			wantShown: nil,
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
			assert.Equal(t, tc.wantShown, shown)

			// no displayed line should appear more than once (full dedup)
			seenLines := make(map[string]int)
			for _, s := range shown {
				seenLines[s]++
			}
			for line, count := range seenLines {
				assert.Equal(t, 1, count, "duplicate line found: %q", line)
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
			// embed the large content in a bold summary so it flows through
			// the (only) display path. header block content is suppressed and
			// can't be used to verify large-line capture anymore.
			largeContent := strings.Repeat("x", tc.size)
			stderr := "**" + largeContent + "**\n"

			var shown []string
			e := &CodexExecutor{
				OutputHandler: func(text string) {
					shown = append(shown, strings.TrimSuffix(text, "\n"))
				},
			}

			res := e.processStderr(context.Background(), strings.NewReader(stderr), nil, nil, false)

			require.NoError(t, res.err, "should handle %d byte line without error", tc.size)
			assert.Contains(t, shown, largeContent, "large content should be captured")
		})
	}
}

func TestCodexExecutor_Run_largeOutput(t *testing.T) {
	// test end-to-end with large stderr and stdout. header block is now
	// suppressed, so the large stderr content is wrapped in a bold summary
	// (the only display path) to verify the executor does not truncate it.
	largeStderr := strings.Repeat("x", 200*1024) // 200KB
	largeStdout := strings.Repeat("y", 500*1024) // 500KB

	mock := &mockCodexRunner{
		runFunc: func(_ context.Context, _ string, _ ...string) (CodexStreams, func() error, error) {
			stderr := "**" + largeStderr + "**\n"
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
	// verify large stderr was processed (flows through as bold summary)
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

func TestCodexExecutor_configOverrides(t *testing.T) {
	reviewerDesc := fmt.Sprintf("agents.%s.description=%q", CodexReviewerAgentName, codexReviewerDescription)
	const fallbackArg = `project_doc_fallback_filenames=["CLAUDE.md"]`

	tests := []struct {
		name string
		exec *CodexExecutor
		want []string
	}{
		{
			name: "no flags produce no overrides",
			exec: &CodexExecutor{},
			want: nil,
		},
		{
			name: "MultiAgent only adds feature flag and reviewer registration",
			exec: &CodexExecutor{MultiAgent: true},
			want: []string{"-c", "features.multi_agent=true", "-c", reviewerDesc},
		},
		{
			name: "PassClaudeMd only adds project_doc_fallback_filenames",
			exec: &CodexExecutor{PassClaudeMd: true},
			want: []string{"-c", fallbackArg},
		},
		{
			name: "both flags emit all overrides with multi-agent first",
			exec: &CodexExecutor{MultiAgent: true, PassClaudeMd: true},
			want: []string{"-c", "features.multi_agent=true", "-c", reviewerDesc, "-c", fallbackArg},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.exec.configOverrides())
		})
	}
}

func TestCodexExecutor_configOverrides_reviewerPairsWithMultiAgent(t *testing.T) {
	// agent registration is meaningless without features.multi_agent=true,
	// so PassClaudeMd alone must NOT emit any agents.reviewer override.
	e := CodexExecutor{PassClaudeMd: true}
	args := e.configOverrides()
	joined := strings.Join(args, " ")
	assert.NotContains(t, joined, "agents.reviewer", "reviewer agent must only be registered alongside multi_agent")
	assert.NotContains(t, joined, "features.multi_agent", "multi_agent flag must not appear when only fallback is set")
}

func TestCodexExecutor_Run_SplicesMultiAgentArgs(t *testing.T) {
	t.Setenv("RALPHEX_DOCKER", "")

	var capturedArgs []string
	mock := &mockCodexRunner{
		runFunc: func(_ context.Context, _ string, args ...string) (CodexStreams, func() error, error) {
			capturedArgs = args
			return mockStreams("", "result"), mockWait(), nil
		},
	}
	e := &CodexExecutor{runner: mock, MultiAgent: true}

	result := e.Run(context.Background(), "test prompt")
	require.NoError(t, result.Error)

	require.NotEmpty(t, capturedArgs)
	assert.Equal(t, "exec", capturedArgs[0], "exec must be first arg")
	// multi-agent overrides splice right after exec, before --sandbox
	require.GreaterOrEqual(t, len(capturedArgs), 5)
	assert.Equal(t, "-c", capturedArgs[1])
	assert.Equal(t, "features.multi_agent=true", capturedArgs[2])
	assert.Equal(t, "-c", capturedArgs[3])
	expectedReviewerDesc := fmt.Sprintf("agents.%s.description=%q", CodexReviewerAgentName, codexReviewerDescription)
	assert.Equal(t, expectedReviewerDesc, capturedArgs[4], "exact reviewer description literal")
	assert.Contains(t, strings.Join(capturedArgs, " "), "--sandbox read-only", "default sandbox still emitted")
}

func TestCodexExecutor_Run_SplicesFallbackArgs(t *testing.T) {
	t.Setenv("RALPHEX_DOCKER", "")

	var capturedArgs []string
	mock := &mockCodexRunner{
		runFunc: func(_ context.Context, _ string, args ...string) (CodexStreams, func() error, error) {
			capturedArgs = args
			return mockStreams("", "result"), mockWait(), nil
		},
	}
	e := &CodexExecutor{runner: mock, PassClaudeMd: true}

	result := e.Run(context.Background(), "test prompt")
	require.NoError(t, result.Error)

	argsStr := strings.Join(capturedArgs, " ")
	assert.Contains(t, argsStr, `project_doc_fallback_filenames=["CLAUDE.md"]`)
	assert.NotContains(t, argsStr, "features.multi_agent", "no multi_agent flag when only fallback is set")
}

func TestCodexExecutor_Run_NoOverridesByDefault(t *testing.T) {
	t.Setenv("RALPHEX_DOCKER", "")

	var capturedArgs []string
	mock := &mockCodexRunner{
		runFunc: func(_ context.Context, _ string, args ...string) (CodexStreams, func() error, error) {
			capturedArgs = args
			return mockStreams("", "result"), mockWait(), nil
		},
	}
	e := &CodexExecutor{runner: mock}

	result := e.Run(context.Background(), "test prompt")
	require.NoError(t, result.Error)

	argsStr := strings.Join(capturedArgs, " ")
	assert.NotContains(t, argsStr, "features.multi_agent")
	assert.NotContains(t, argsStr, "agents.reviewer")
	assert.NotContains(t, argsStr, "project_doc_fallback_filenames")
}

func TestCodexExecutor_Run_IdleTimeoutFires(t *testing.T) {
	// idle timeout fires when stderr emits one line then goes silent and stdout produces no further output
	stderrPipeR, stderrPipeW := io.Pipe()
	stdoutPipeR, stdoutPipeW := io.Pipe()

	mock := &mockCodexRunner{
		runFunc: func(ctx context.Context, _ string, _ ...string) (CodexStreams, func() error, error) {
			go func() {
				defer stderrPipeW.Close()
				defer stdoutPipeW.Close()
				_, _ = stderrPipeW.Write([]byte("--------\nworking...\n"))
				<-ctx.Done() // wait for idle timeout to cancel context
			}()
			return CodexStreams{Stderr: stderrPipeR, Stdout: stdoutPipeR}, func() error {
				<-ctx.Done()
				return errors.New("signal: killed")
			}, nil
		},
	}

	e := &CodexExecutor{runner: mock, IdleTimeout: 100 * time.Millisecond}
	result := e.Run(context.Background(), "analyze code")

	require.NoError(t, result.Error, "idle timeout should not produce an error")
	assert.True(t, result.IdleTimedOut, "IdleTimedOut should be set when idle timeout fires")
}

func TestCodexExecutor_Run_IdleTimeoutResetsOnStderrLines(t *testing.T) {
	stderrPipeR, stderrPipeW := io.Pipe()

	mock := &mockCodexRunner{
		runFunc: func(_ context.Context, _ string, _ ...string) (CodexStreams, func() error, error) {
			go func() {
				defer stderrPipeW.Close()
				for range 5 {
					_, _ = stderrPipeW.Write([]byte("--------\n"))
					time.Sleep(50 * time.Millisecond)
				}
			}()
			return CodexStreams{Stderr: stderrPipeR, Stdout: strings.NewReader("final answer")},
				func() error { return nil }, nil
		},
	}

	// 10x margin between writer sleep (50ms) and IdleTimeout (500ms): tight 2x margins
	// cause spurious "idle timed out" failures on loaded CI runners.
	e := &CodexExecutor{runner: mock, IdleTimeout: 500 * time.Millisecond}
	result := e.Run(context.Background(), "analyze code")

	require.NoError(t, result.Error)
	assert.False(t, result.IdleTimedOut, "IdleTimedOut should be false when stderr keeps emitting")
	assert.Equal(t, "final answer", result.Output)
}

func TestCodexExecutor_Run_IdleTimeoutResetsOnStdoutChunks(t *testing.T) {
	stdoutPipeR, stdoutPipeW := io.Pipe()
	done := make(chan struct{})

	mock := &mockCodexRunner{
		runFunc: func(_ context.Context, _ string, _ ...string) (CodexStreams, func() error, error) {
			go func() {
				defer close(done)
				defer stdoutPipeW.Close()
				for _, chunk := range []string{"one", " two", " three", " four"} {
					_, _ = stdoutPipeW.Write([]byte(chunk))
					time.Sleep(50 * time.Millisecond)
				}
			}()
			return CodexStreams{Stderr: strings.NewReader(""), Stdout: stdoutPipeR}, func() error {
				<-done
				return nil
			}, nil
		},
	}

	// 10x margin between writer sleep (50ms) and IdleTimeout (500ms): tight 2x margins
	// cause spurious "idle timed out" failures on loaded CI runners.
	e := &CodexExecutor{runner: mock, IdleTimeout: 500 * time.Millisecond}
	result := e.Run(context.Background(), "analyze code")

	require.NoError(t, result.Error)
	assert.False(t, result.IdleTimedOut, "IdleTimedOut should be false when stdout keeps emitting")
	assert.Equal(t, "one two three four", result.Output)
}

func TestCodexExecutor_Run_IdleTimeoutDisabledWhenZero(t *testing.T) {
	// default behavior: IdleTimeout=0 means no idle timeout, runs normally
	mock := &mockCodexRunner{
		runFunc: func(_ context.Context, _ string, _ ...string) (CodexStreams, func() error, error) {
			return mockStreams("", "done"), mockWait(), nil
		},
	}
	e := &CodexExecutor{runner: mock} // IdleTimeout is zero (default)

	result := e.Run(context.Background(), "test prompt")

	require.NoError(t, result.Error)
	assert.Equal(t, "done", result.Output)
	assert.Zero(t, e.IdleTimeout)
	assert.False(t, result.IdleTimedOut, "IdleTimedOut should be false when idle timeout is disabled")
}

func TestCodexExecutor_Run_IdleTimeoutDetectsLimitPattern(t *testing.T) {
	// when idle timeout fires after a stderr quota diagnostic, the limit pattern
	// must be detected so runWithLimitRetry can wait-and-retry.
	stderrPipeR, stderrPipeW := io.Pipe()

	mock := &mockCodexRunner{
		runFunc: func(ctx context.Context, _ string, _ ...string) (CodexStreams, func() error, error) {
			go func() {
				defer stderrPipeW.Close()
				_, _ = stderrPipeW.Write([]byte("ERROR: You've hit your usage limit\n"))
				<-ctx.Done()
			}()
			return CodexStreams{Stderr: stderrPipeR, Stdout: strings.NewReader("")}, func() error {
				<-ctx.Done()
				return errors.New("signal: killed")
			}, nil
		},
	}

	e := &CodexExecutor{
		runner:        mock,
		IdleTimeout:   100 * time.Millisecond,
		LimitPatterns: []string{"You've hit your usage limit"},
	}
	result := e.Run(context.Background(), "analyze code")

	var limitErr *LimitPatternError
	require.ErrorAs(t, result.Error, &limitErr, "should return LimitPatternError")
	assert.Equal(t, "You've hit your usage limit", limitErr.Pattern)
	assert.Equal(t, "codex /status", limitErr.HelpCmd)
	assert.False(t, result.IdleTimedOut, "IdleTimedOut should not be set when pattern matched")
}

func TestCodexExecutor_Run_NoFalsePositiveOnLongStreamingOutput(t *testing.T) {
	// regression: long streaming codex output that mentions rate-limit phrases in
	// reasoning text (without the error:/fatal:/panic: prefix) must NOT trigger
	// a pattern match. the isCodexErrorLine gate in scanLineForPatterns enforces
	// this, but verify it still holds across many lines of streaming output.
	stderrLines := make([]string, 0, 3+200*2)
	stderrLines = append(stderrLines, "--------", "workdir: /tmp/test", "--------")
	// 200 lines of progress chatter that legitimately mention "rate limit" in code-review reasoning
	for i := range 200 {
		stderrLines = append(stderrLines,
			fmt.Sprintf("**Reviewing rate limit handler at line %d**", i),
			fmt.Sprintf("checking whether quota exceeded path is reachable from handler %d", i),
		)
	}
	stderr := strings.Join(stderrLines, "\n") + "\n"
	stdout := "Analysis complete: no real rate-limit hits.\n"

	mock := &mockCodexRunner{
		runFunc: func(_ context.Context, _ string, _ ...string) (CodexStreams, func() error, error) {
			return mockStreams(stderr, stdout), mockWait(), nil
		},
	}
	e := &CodexExecutor{
		runner:        mock,
		LimitPatterns: []string{"rate limit", "quota exceeded"},
		ErrorPatterns: []string{"rate limit", "quota exceeded"},
	}

	result := e.Run(context.Background(), "review code")

	require.NoError(t, result.Error,
		"non-prefixed stderr chatter mentioning rate-limit phrases must not trigger pattern match")
	var limitErr *LimitPatternError
	assert.NotErrorAs(t, result.Error, &limitErr)
	var patternErr *PatternMatchError
	assert.NotErrorAs(t, result.Error, &patternErr)
	assert.Contains(t, result.Output, "Analysis complete")
}

func TestTouchReader_Read(t *testing.T) {
	t.Run("non-empty read calls touch", func(t *testing.T) {
		var touched int
		r := &touchReader{r: strings.NewReader("hello"), touch: func() { touched++ }}
		buf := make([]byte, 5)
		n, err := r.Read(buf)
		require.NoError(t, err)
		assert.Equal(t, 5, n)
		assert.Equal(t, 1, touched, "touch must be called once for a non-empty read")
	})

	t.Run("zero-byte read does not call touch", func(t *testing.T) {
		// EOF on an empty reader returns (0, io.EOF); touch must not fire
		var touched int
		r := &touchReader{r: strings.NewReader(""), touch: func() { touched++ }}
		buf := make([]byte, 8)
		n, err := r.Read(buf)
		require.ErrorIs(t, err, io.EOF)
		assert.Zero(t, n)
		assert.Zero(t, touched, "touch must NOT be called when n == 0")
	})

	t.Run("error after partial read still calls touch", func(t *testing.T) {
		// io.MultiReader returns partial then EOF; touch must fire for the partial read
		var touched int
		r := &touchReader{r: strings.NewReader("ab"), touch: func() { touched++ }}
		buf := make([]byte, 4)
		n, _ := r.Read(buf)
		assert.Equal(t, 2, n)
		assert.Equal(t, 1, touched, "non-zero n must trigger touch even when accompanied by error")
	})

	t.Run("nil touch is safe", func(t *testing.T) {
		r := &touchReader{r: strings.NewReader("data"), touch: nil}
		buf := make([]byte, 4)
		n, err := r.Read(buf)
		require.NoError(t, err)
		assert.Equal(t, 4, n)
	})
}

func TestCodexExecutor_extractSessionID(t *testing.T) {
	e := &CodexExecutor{}
	tests := []struct {
		name string
		line string
		want string
	}{
		{name: "header line", line: "[26-05-18 10:40:06] session id: 019e3bbe-9788-79f1-b668-6c5790788775", want: "019e3bbe-9788-79f1-b668-6c5790788775"},
		{name: "bare prefix", line: "session id: 019e3bbe-9788-79f1-b668-6c5790788775", want: "019e3bbe-9788-79f1-b668-6c5790788775"},
		{name: "uppercase Session ID", line: "Session ID: 019E3BBE-9788-79F1-B668-6C5790788775", want: "019E3BBE-9788-79F1-B668-6C5790788775"},
		{name: "no match", line: "[26-05-18 10:40:06] model: gpt-5.5", want: ""},
		{name: "malformed uuid", line: "session id: not-a-uuid", want: ""},
		{name: "empty", line: "", want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, e.extractSessionID(tc.line))
		})
	}
}


func TestCodexExecutor_formatRolloutEvent(t *testing.T) {
	e := &CodexExecutor{}
	tests := []struct {
		name string
		line string
		want string
	}{
		{
			name: "assistant message renders text",
			line: `{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello world"}]}}`,
			want: "hello world",
		},
		{
			name: "user message dropped",
			line: `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"output_text","text":"prompt body"}]}}`,
			want: "",
		},
		{
			name: "assistant multi-block joins with newline",
			line: `{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"first"},{"type":"output_text","text":"second"}]}}`,
			want: "first\nsecond",
		},
		{
			name: "exec_command skipped",
			line: `{"type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"git status --short\"}"}}`,
			want: "",
		},
		{
			name: "spawn_agent skipped",
			line: `{"type":"response_item","payload":{"type":"function_call","name":"spawn_agent","arguments":"{\"agent_type\":\"reviewer\",\"message\":\"qa-expert: review the diff\"}"}}`,
			want: "",
		},
		{name: "reasoning skipped", line: `{"type":"response_item","payload":{"type":"reasoning","summary":[]}}`, want: ""},
		{name: "function_call_output skipped", line: `{"type":"response_item","payload":{"type":"function_call_output","output":"ok"}}`, want: ""},
		{name: "session_meta skipped", line: `{"type":"session_meta","payload":{}}`, want: ""},
		{name: "turn_context skipped", line: `{"type":"turn_context","payload":{}}`, want: ""},
		{name: "event_msg skipped", line: `{"type":"event_msg","payload":{}}`, want: ""},
		{name: "malformed json", line: `not json`, want: ""},
		{name: "empty line", line: ``, want: ""},
		{name: "whitespace only", line: `   `, want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, e.formatRolloutEvent([]byte(tc.line)))
		})
	}
}

func TestCodexExecutor_tailRolloutFile_streamsAssistantMessages(t *testing.T) {
	// craft a fake rollout file matching codex's path scheme so findRolloutFile
	// can resolve it via the same glob the real runtime uses.
	home := t.TempDir()
	t.Setenv("HOME", home)
	sessionID := "019e3bbe-9788-79f1-b668-deadbeefcafe"
	dir := filepath.Join(home, ".codex", "sessions", "2026", "05", "18")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	path := filepath.Join(dir, "rollout-2026-05-18T10-40-06-"+sessionID+".jsonl")
	f, err := os.Create(path) //nolint:gosec // test temp file
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })

	// OutputHandler is called from the tailer goroutine, so guard the test-side
	// slice with a mutex; otherwise -race flags concurrent slice appends.
	var (
		capturedMu sync.Mutex
		captured   []string
	)
	snapshot := func() []string {
		capturedMu.Lock()
		defer capturedMu.Unlock()
		out := make([]string, len(captured))
		copy(out, captured)
		return out
	}
	e := &CodexExecutor{OutputHandler: func(text string) {
		capturedMu.Lock()
		captured = append(captured, text)
		capturedMu.Unlock()
	}}

	// pre-write some events, then start tailer, then append more — verifies
	// both initial-drain and follow-on append behavior.
	preEvents := []string{
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"first reply"}]}}`,
		`{"type":"response_item","payload":{"type":"reasoning","summary":[]}}`,
	}
	for _, ev := range preEvents {
		_, writeErr := f.WriteString(ev + "\n")
		require.NoError(t, writeErr)
	}

	ctx, cancel := context.WithCancel(context.Background())
	tailDone := make(chan struct{})
	go func() {
		defer close(tailDone)
		e.tailRolloutFile(ctx, sessionID, nil)
	}()

	// wait briefly for initial drain
	deadline := time.Now().Add(2 * time.Second)
	for len(snapshot()) < 1 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}

	// append a late event after the tailer has started
	_, err = f.WriteString(`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"late reply after first read"}]}}` + "\n")
	require.NoError(t, err)

	// give the tailer a poll cycle to pick it up
	deadline = time.Now().Add(2 * time.Second)
	for len(snapshot()) < 2 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}

	cancel()
	<-tailDone

	final := snapshot()
	require.GreaterOrEqual(t, len(final), 2, "expected at least 2 emissions, got %v", final)
	assert.Contains(t, final[0], "first reply")
	assert.Contains(t, final[1], "late reply after first read")
}

func TestCodexExecutor_findRolloutFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	e := &CodexExecutor{}

	t.Run("returns empty when no file", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		got := e.findRolloutFile(ctx, "019e3bbe-9788-79f1-b668-000000000000")
		assert.Empty(t, got)
	})

	t.Run("resolves matching path", func(t *testing.T) {
		sessionID := "019e3bbe-9788-79f1-b668-111111111111"
		dir := filepath.Join(home, ".codex", "sessions", "2026", "05", "18")
		require.NoError(t, os.MkdirAll(dir, 0o750))
		path := filepath.Join(dir, "rollout-2026-05-18T10-40-06-"+sessionID+".jsonl")
		require.NoError(t, os.WriteFile(path, nil, 0o600))

		got := e.findRolloutFile(context.Background(), sessionID)
		assert.Equal(t, path, got)
	})

	t.Run("respects ctx cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		got := e.findRolloutFile(ctx, "019e3bbe-9788-79f1-b668-222222222222")
		assert.Empty(t, got)
	})
}

func TestCodexExecutor_processStderr_emitsSessionID(t *testing.T) {
	e := &CodexExecutor{}
	const id = "019e3bbe-9788-79f1-b668-aaaaaaaaaaaa"
	stderr := "--------\n[26-05-18 10:40:06] workdir: /tmp/x\n[26-05-18 10:40:06] session id: " + id + "\n--------\n"
	ch := make(chan string, 1)
	res := e.processStderr(context.Background(), strings.NewReader(stderr), nil, ch, false)
	require.NoError(t, res.err)

	select {
	case got := <-ch:
		assert.Equal(t, id, got)
	default:
		t.Fatal("session id was not delivered to channel")
	}
}

func TestCodexExecutor_firstRunHeaderEmission(t *testing.T) {
	stderr := "--------\nworkdir: /tmp/x\nmodel: gpt-5.5\nprovider: openai\nsandbox: danger-full-access\nreasoning effort: high\nreasoning summaries: auto\nsession id: 019e3bbe-9788-79f1-b668-000000000000\n--------\n**Thinking**\n"

	t.Run("first run leaks whitelisted header lines", func(t *testing.T) {
		var captured []string
		e := &CodexExecutor{
			runner: &mockCodexRunner{
				runFunc: func(_ context.Context, _ string, _ ...string) (CodexStreams, func() error, error) {
					return mockStreams(stderr, "ok"), mockWait(), nil
				},
			},
			OutputHandler: func(text string) { captured = append(captured, strings.TrimSuffix(text, "\n")) },
		}
		result := e.Run(context.Background(), "x")
		require.NoError(t, result.Error)

		assert.Contains(t, captured, "model: gpt-5.5", "first-run model: should leak")
		assert.Contains(t, captured, "sandbox: danger-full-access", "first-run sandbox: should leak")
		assert.Contains(t, captured, "reasoning effort: high", "first-run effort: should leak")
		assert.Contains(t, captured, "Thinking", "bold summary should appear")
		for _, hidden := range []string{"--------", "workdir: /tmp/x", "provider: openai", "reasoning summaries: auto"} {
			assert.NotContains(t, captured, hidden, "%q must stay hidden", hidden)
		}
		for _, line := range captured {
			assert.NotContains(t, line, "session id:", "session id must not leak (privacy/noise)")
		}
	})

	t.Run("second run on same executor suppresses entire header", func(t *testing.T) {
		var firstCaptured, secondCaptured []string
		makeHandler := func(dst *[]string) func(string) {
			return func(text string) { *dst = append(*dst, strings.TrimSuffix(text, "\n")) }
		}
		e := &CodexExecutor{
			runner: &mockCodexRunner{
				runFunc: func(_ context.Context, _ string, _ ...string) (CodexStreams, func() error, error) {
					return mockStreams(stderr, "ok"), mockWait(), nil
				},
			},
		}
		e.OutputHandler = makeHandler(&firstCaptured)
		require.NoError(t, e.Run(context.Background(), "x").Error)

		e.OutputHandler = makeHandler(&secondCaptured)
		require.NoError(t, e.Run(context.Background(), "x").Error)

		assert.Contains(t, firstCaptured, "model: gpt-5.5", "first run must include model:")
		for _, hidden := range []string{"model: gpt-5.5", "sandbox: danger-full-access", "reasoning effort: high"} {
			assert.NotContains(t, secondCaptured, hidden, "second-run %q must be suppressed", hidden)
		}
		assert.Contains(t, secondCaptured, "Thinking", "bold summary still shown on second run")
	})
}
