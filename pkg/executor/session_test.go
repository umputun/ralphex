package executor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testWriteCloser is a thread-safe bytes.Buffer that implements io.WriteCloser.
type testWriteCloser struct {
	buf    bytes.Buffer
	mu     sync.Mutex
	closed bool
}

func (w *testWriteCloser) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *testWriteCloser) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	return nil
}

func (w *testWriteCloser) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// failWriteCloser returns an error on every Write call.
type failWriteCloser struct {
	writeErr error
}

func (w *failWriteCloser) Write([]byte) (int, error) { return 0, w.writeErr }
func (w *failWriteCloser) Close() error              { return nil }

func TestClaudeSession_Send(t *testing.T) {
	tests := []struct {
		name    string
		msg     string
		wantErr bool
	}{
		{name: "simple message", msg: "hello world"},
		{name: "empty message", msg: ""},
		{name: "message with special chars", msg: `line1\nline2 "quoted"`},
		{name: "unicode message", msg: "hello 世界 🌍"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := &testWriteCloser{}
			s := newClaudeSession(w)

			err := s.Send(tc.msg)

			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			// verify JSON format
			var got stdinMessage
			require.NoError(t, json.Unmarshal([]byte(w.String()), &got))
			assert.Equal(t, "user", got.Type)
			assert.Equal(t, "user", got.Message.Role)
			require.Len(t, got.Message.Content, 1)
			assert.Equal(t, "text", got.Message.Content[0].Type)
			assert.Equal(t, tc.msg, got.Message.Content[0].Text)
		})
	}
}

func TestClaudeSession_Send_AfterClose(t *testing.T) {
	w := &testWriteCloser{}
	s := newClaudeSession(w)

	require.NoError(t, s.Close())
	err := s.Send("should fail")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "session is closed")
}

func TestClaudeSession_Send_NilSession(t *testing.T) {
	var s *ClaudeSession
	err := s.Send("should fail")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "session is nil")
}

func TestClaudeSession_Send_Concurrent(t *testing.T) {
	w := &testWriteCloser{}
	s := newClaudeSession(w)

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			_ = s.Send("msg")
		}(i)
	}
	wg.Wait()

	// verify all messages were written (each ends with \n)
	lines := bytes.Count([]byte(w.String()), []byte("\n"))
	assert.Equal(t, n, lines)
}

func TestClaudeSession_Close_Idempotent(t *testing.T) {
	w := &testWriteCloser{}
	s := newClaudeSession(w)

	require.NoError(t, s.Close())
	require.NoError(t, s.Close()) // second close should not error
	assert.True(t, w.closed)
}

func TestClaudeSession_Close_Nil(t *testing.T) {
	var s *ClaudeSession
	require.NoError(t, s.Close())
}

func TestClaudeSession_Send_WriterError(t *testing.T) {
	s := newClaudeSession(&failWriteCloser{writeErr: fmt.Errorf("broken pipe")})

	err := s.Send("hello")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "broken pipe")
}

func TestClaudeSession_Send_Sequential(t *testing.T) {
	w := &testWriteCloser{}
	s := newClaudeSession(w)

	msgs := []string{"first", "second", "third"}
	for _, msg := range msgs {
		require.NoError(t, s.Send(msg))
	}

	// each Send appends a newline-terminated JSON line; verify each is independently parseable
	lines := bytes.Split(bytes.TrimRight(w.buf.Bytes(), "\n"), []byte("\n"))
	require.Len(t, lines, len(msgs))

	for i, line := range lines {
		var got stdinMessage
		require.NoError(t, json.Unmarshal(line, &got), "line %d should be valid JSON", i)
		assert.Equal(t, msgs[i], got.Message.Content[0].Text)
	}
}
