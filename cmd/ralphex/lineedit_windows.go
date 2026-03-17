//go:build windows

package main

import (
	"bufio"
	"io"
	"os"
	"strings"
)

// lineEditor is a no-op on windows — raw terminal mode is not supported.
// falls back to standard buffered line reading.
type lineEditor struct {
	lines chan string
	done  chan struct{}
}

// newLineEditor returns a line editor that uses standard buffered reading.
// on windows, there is no raw mode coordination with output.
func newLineEditor() (*lineEditor, error) {
	le := &lineEditor{
		lines: make(chan string, 8),
		done:  make(chan struct{}),
	}

	go func() {
		defer close(le.lines)
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			select {
			case le.lines <- line:
			case <-le.done:
				return
			}
		}
	}()

	return le, nil
}

// Lines returns a channel of completed input lines.
func (le *lineEditor) Lines() <-chan string {
	return le.lines
}

// Close stops the line editor.
func (le *lineEditor) Close() {
	select {
	case <-le.done:
		return
	default:
	}
	close(le.done)
}

// wrapWriter returns the writer unchanged on windows — no output coordination.
func (le *lineEditor) wrapWriter(w io.Writer) io.Writer {
	return w
}
