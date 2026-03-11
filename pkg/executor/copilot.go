package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/umputun/ralphex/pkg/status"
)

// copilotEvent represents a JSONL event from copilot CLI output.
// see docs/copilot-jsonl-format.md for full schema reference.
type copilotEvent struct {
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data"`
	ExitCode  int             `json:"exitCode"`  // only on result event
	SessionID string          `json:"sessionId"` // only on result event
}

// copilotMessageDelta is the data payload for assistant.message_delta events.
type copilotMessageDelta struct {
	MessageID    string `json:"messageId"`
	DeltaContent string `json:"deltaContent"`
}

// copilotMessage is the data payload for assistant.message events.
type copilotMessage struct {
	MessageID string `json:"messageId"`
	Content   string `json:"content"`
}

// CopilotExecutor runs GitHub Copilot CLI commands with JSONL output parsing.
type CopilotExecutor struct {
	Command       string            // command to execute, defaults to "copilot"
	Args          string            // additional arguments (space-separated)
	CodingModel   string            // model for coding/task phases
	ReviewModel   string            // model for external review phases
	ErrorPatterns []string          // patterns to detect in output (e.g., rate limit messages)
	LimitPatterns []string          // patterns to detect rate limits (checked before error patterns)
	OutputHandler func(text string) // called for each text chunk, can be nil
	cmdRunner     CommandRunner     // for testing, nil uses default
}

// Run executes copilot CLI with the CodingModel for task/review phases.
func (e *CopilotExecutor) Run(ctx context.Context, prompt string) Result {
	return e.run(ctx, prompt, e.CodingModel)
}

// RunReview executes copilot CLI with the ReviewModel for external review phases.
func (e *CopilotExecutor) RunReview(ctx context.Context, prompt string) Result {
	return e.run(ctx, prompt, e.ReviewModel)
}

// run executes copilot CLI with the given prompt and model, parses JSONL output.
func (e *CopilotExecutor) run(ctx context.Context, prompt, model string) Result {
	cmd := e.Command
	if cmd == "" {
		cmd = "copilot"
	}

	var args []string
	if e.Args != "" {
		args = splitArgs(e.Args)
	} else {
		args = []string{"--allow-all", "--no-ask-user", "--output-format", "json"}
	}

	if model != "" {
		args = append(args, "--model", model)
	}

	args = append(args, "-p", prompt)

	runner := e.cmdRunner
	if runner == nil {
		runner = &execRunner{}
	}

	stdout, wait, err := runner.Run(ctx, cmd, args...)
	if err != nil {
		return Result{Error: err}
	}

	result := e.parseJSONL(ctx, stdout)

	if waitErr := wait(); waitErr != nil {
		if ctx.Err() != nil {
			return Result{Output: result.Output, Signal: result.Signal, Error: ctx.Err()}
		}
		if result.Output == "" {
			return Result{Error: fmt.Errorf("copilot exited with error: %w", waitErr)}
		}
		// non-zero exit with output but no signal means copilot failed without useful work.
		// if there IS a signal, work was done — ignore exit code.
		if result.Signal == "" {
			result.Error = fmt.Errorf("copilot exited with error: %w", waitErr)
		}
	}

	// check limit patterns first (higher priority)
	if pattern := matchPattern(result.Output, e.LimitPatterns); pattern != "" {
		return Result{
			Output: result.Output,
			Signal: result.Signal,
			Error:  &LimitPatternError{Pattern: pattern, HelpCmd: "copilot --help"},
		}
	}

	// check for error patterns in output
	if pattern := matchPattern(result.Output, e.ErrorPatterns); pattern != "" {
		return Result{
			Output: result.Output,
			Signal: result.Signal,
			Error:  &PatternMatchError{Pattern: pattern, HelpCmd: "copilot --help"},
		}
	}

	return result
}

// parseJSONL reads and parses JSONL stream from copilot CLI.
// dispatches on event type, extracts text from message_delta (streaming) and message (authoritative).
func (e *CopilotExecutor) parseJSONL(ctx context.Context, r io.Reader) Result {
	var output strings.Builder
	var signal string

	err := readLines(ctx, r, func(line string) {
		if line == "" {
			return
		}

		var event copilotEvent
		if jsonErr := json.Unmarshal([]byte(line), &event); jsonErr != nil {
			// non-JSON lines (e.g., stderr merged in on CLI error) — pass through
			output.WriteString(line)
			output.WriteString("\n")
			if e.OutputHandler != nil {
				e.OutputHandler(line + "\n")
			}
			return
		}

		switch event.Type {
		case "assistant.message_delta":
			var delta copilotMessageDelta
			if err := json.Unmarshal(event.Data, &delta); err == nil && delta.DeltaContent != "" {
				if e.OutputHandler != nil {
					e.OutputHandler(delta.DeltaContent)
				}
				// check for signals in streaming deltas
				if sig := detectSignal(delta.DeltaContent); sig != "" {
					signal = sig
				}
			}

		case "assistant.message":
			var msg copilotMessage
			if err := json.Unmarshal(event.Data, &msg); err == nil && msg.Content != "" {
				// assistant.message.data.content is the authoritative text source.
				// accumulate it into output (not deltas, to avoid double-counting).
				output.WriteString(msg.Content)
				// check for signals in complete message (authoritative check)
				if sig := detectSignal(msg.Content); sig != "" {
					signal = sig
				}
			}

		case "result":
			// result event has exitCode at top level (no data wrapper); session end marker.
			// non-zero exitCode means copilot reported a structured failure.
			if event.ExitCode != 0 {
				if e.OutputHandler != nil {
					e.OutputHandler(fmt.Sprintf("[copilot exit code: %d]\n", event.ExitCode))
				}
				// only set failed signal if no other signal was already detected
				// (e.g., signal from assistant.message takes priority)
				if signal == "" {
					signal = status.Failed
				}
			}

		case "tool.execution_complete":
			e.handleToolComplete(event.Data)

		case "tool.execution_start":
			e.handleToolStart(event.Data)

		default:
			// skip: user.message, assistant.turn_start, assistant.turn_end,
			// assistant.reasoning_delta, assistant.reasoning, session.info,
			// and unknown types
		}
	})

	if err != nil {
		return Result{Output: output.String(), Signal: signal, Error: fmt.Errorf("stream read: %w", err)}
	}

	return Result{Output: output.String(), Signal: signal}
}

// handleToolStart logs tool activity for progress display.
func (e *CopilotExecutor) handleToolStart(data json.RawMessage) {
	var toolStart struct {
		ToolName string `json:"toolName"`
	}
	if err := json.Unmarshal(data, &toolStart); err == nil && toolStart.ToolName != "" {
		if e.OutputHandler != nil {
			e.OutputHandler(fmt.Sprintf("[tool: %s]\n", toolStart.ToolName))
		}
	}
}

// handleToolComplete surfaces tool-level failures for progress display.
func (e *CopilotExecutor) handleToolComplete(data json.RawMessage) {
	var toolComplete struct {
		Success bool `json:"success"`
		Error   struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &toolComplete); err == nil && !toolComplete.Success {
		msg := toolComplete.Error.Message
		if msg == "" {
			msg = "unknown error"
		}
		if e.OutputHandler != nil {
			e.OutputHandler(fmt.Sprintf("[tool failed: %s]\n", msg))
		}
	}
}
