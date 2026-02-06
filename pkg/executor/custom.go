package executor

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// CustomRunner abstracts command execution for custom review scripts.
// Returns stdout reader and a wait function for completion.
type CustomRunner interface {
	Run(ctx context.Context, script, promptFile string) (stdout io.Reader, wait func() error, err error)
}

// execCustomRunner is the default command runner using os/exec.
type execCustomRunner struct{}

func (r *execCustomRunner) Run(ctx context.Context, script, promptFile string) (io.Reader, func() error, error) {
	// check context before starting to avoid spawning a process that will be immediately killed
	if err := ctx.Err(); err != nil {
		return nil, nil, fmt.Errorf("context already canceled: %w", err)
	}

	// use exec.Command (not CommandContext) because we handle cancellation ourselves
	// to ensure the entire process group is killed, not just the direct child
	cmd := exec.Command(script, promptFile) //nolint:noctx // intentional: we handle context cancellation via process group kill

	// create new process group so we can kill all descendants on cleanup
	setupProcessGroup(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("stdout pipe: %w", err)
	}
	// merge stderr into stdout
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("start command: %w", err)
	}

	// setup process group cleanup with graceful shutdown on context cancellation
	cleanup := newProcessGroupCleanup(cmd, ctx.Done())

	return stdout, cleanup.Wait, nil
}

// CustomExecutor runs custom review scripts and streams output.
type CustomExecutor struct {
	Script        string            // path to the custom review script
	OutputHandler func(text string) // called for each output line, can be nil
	ErrorPatterns []string          // patterns to detect in output (e.g., rate limit messages)
	runner        CustomRunner      // for testing, nil uses default
}

// SetRunner sets the custom runner for testing purposes.
func (e *CustomExecutor) SetRunner(r CustomRunner) {
	e.runner = r
}

// Run executes the custom review script with the prompt content written to a temp file.
// The script receives the path to the prompt file as its single argument.
// Output is streamed line-by-line to OutputHandler.
func (e *CustomExecutor) Run(ctx context.Context, promptContent string) Result {
	if e.Script == "" {
		return Result{Error: errors.New("custom review script not configured")}
	}

	// write prompt to temp file
	promptFile, err := os.CreateTemp("", "ralphex-custom-prompt-*.txt")
	if err != nil {
		return Result{Error: fmt.Errorf("create prompt file: %w", err)}
	}
	promptPath := promptFile.Name()
	defer os.Remove(promptPath) //nolint:errcheck // cleanup temp file

	if _, writeErr := promptFile.WriteString(promptContent); writeErr != nil {
		promptFile.Close()
		return Result{Error: fmt.Errorf("write prompt file: %w", writeErr)}
	}
	if closeErr := promptFile.Close(); closeErr != nil {
		return Result{Error: fmt.Errorf("close prompt file: %w", closeErr)}
	}

	runner := e.runner
	if runner == nil {
		runner = &execCustomRunner{}
	}

	stdout, wait, err := runner.Run(ctx, e.Script, promptPath)
	if err != nil {
		return Result{Error: fmt.Errorf("start custom script: %w", err)}
	}

	// process stdout for output and signal detection
	output, signal, streamErr := e.processOutput(ctx, stdout)

	// wait for command completion
	waitErr := wait()

	// determine final error
	var finalErr error
	switch {
	case streamErr != nil:
		finalErr = streamErr
	case waitErr != nil:
		if ctx.Err() != nil {
			finalErr = fmt.Errorf("context error: %w", ctx.Err())
		} else {
			finalErr = fmt.Errorf("custom script exited with error: %w", waitErr)
		}
	}

	// check for error patterns in output
	if pattern := checkErrorPatterns(output, e.ErrorPatterns); pattern != "" {
		return Result{
			Output: output,
			Signal: signal,
			Error:  &PatternMatchError{Pattern: pattern, HelpCmd: e.Script + " --help"},
		}
	}

	return Result{Output: output, Signal: signal, Error: finalErr}
}

// processOutput reads stdout line-by-line, streams to OutputHandler, and detects signals.
func (e *CustomExecutor) processOutput(ctx context.Context, r io.Reader) (output, signal string, err error) {
	var outputBuf []byte
	scanner := bufio.NewScanner(r)
	// increase buffer size for large output lines
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, MaxScannerBuffer)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return string(outputBuf), signal, fmt.Errorf("context done: %w", ctx.Err())
		default:
		}

		line := scanner.Text()
		outputBuf = append(outputBuf, line...)
		outputBuf = append(outputBuf, '\n')

		if e.OutputHandler != nil {
			e.OutputHandler(line + "\n")
		}

		// check for signals in each line
		if sig := detectSignal(line); sig != "" {
			signal = sig
		}
	}

	if err := scanner.Err(); err != nil {
		return string(outputBuf), signal, fmt.Errorf("read output: %w", err)
	}
	return string(outputBuf), signal, nil
}
