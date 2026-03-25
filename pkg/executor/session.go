package executor

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// ClaudeSession wraps an open stdin pipe to a running Claude process.
// thread-safe for concurrent Send() calls.
type ClaudeSession struct {
	stdin  io.WriteCloser
	mu     sync.Mutex
	closed bool
}

// newClaudeSession creates a new session wrapping the given stdin pipe.
func newClaudeSession(stdin io.WriteCloser) *ClaudeSession {
	return &ClaudeSession{stdin: stdin}
}

// Send marshals msg as a stream-json user message and writes it to stdin.
func (s *ClaudeSession) Send(msg string) error {
	if s == nil {
		return fmt.Errorf("session is nil")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return fmt.Errorf("session is closed")
	}

	m := stdinMessage{
		Type: "user",
		Message: stdinMessageBody{
			Role:    "user",
			Content: []stdinContent{{Type: "text", Text: msg}},
		},
	}

	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}
	data = append(data, '\n')

	if _, err = s.stdin.Write(data); err != nil {
		return fmt.Errorf("write to stdin: %w", err)
	}
	return nil
}

// Close closes the stdin pipe. idempotent.
func (s *ClaudeSession) Close() error {
	if s == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}

	s.closed = true
	return s.stdin.Close()
}

// stdinMessage is the top-level JSON envelope for stream-json input.
type stdinMessage struct {
	Type    string           `json:"type"`
	Message stdinMessageBody `json:"message"`
}

// stdinMessageBody holds the message role and content array.
type stdinMessageBody struct {
	Role    string         `json:"role"`
	Content []stdinContent `json:"content"`
}

// stdinContent represents a single content block in the message.
type stdinContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
