//go:build !windows

package main

import (
	"fmt"
	"io"
	"os"
	"sync"
	"unicode/utf8"

	"golang.org/x/term"
)

const inputPrompt = "> "

// lineEditor provides raw-mode line editing for stdin while coordinating
// with streaming output. it prevents backspace display corruption by
// managing character echo manually and erasing/redrawing the input line
// around output writes.
type lineEditor struct {
	mu       sync.Mutex
	buf      []byte     // current input buffer
	pos      int        // cursor byte position within buf
	fd       int        // stdin file descriptor
	oldState *term.State
	lines    chan string
	done     chan struct{}
}

// newLineEditor creates a line editor that reads from stdin in raw mode.
// returns nil, nil if stdin is not a terminal (e.g. piped input).
func newLineEditor() (*lineEditor, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return nil, nil
	}

	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return nil, fmt.Errorf("make raw: %w", err)
	}

	le := &lineEditor{
		fd:       fd,
		oldState: oldState,
		lines:    make(chan string, 8),
		done:     make(chan struct{}),
	}

	go le.readLoop()
	return le, nil
}

// Lines returns a channel of completed input lines.
func (le *lineEditor) Lines() <-chan string {
	return le.lines
}

// Close restores the terminal and stops the read loop.
func (le *lineEditor) Close() {
	select {
	case <-le.done:
		return // already closed
	default:
	}
	close(le.done)
	term.Restore(le.fd, le.oldState) //nolint:errcheck // best-effort restore
}

// wrapWriter returns a writer that coordinates output with the input line.
// before each write, the current input line is erased; after, it is redrawn.
func (le *lineEditor) wrapWriter(w io.Writer) io.Writer {
	return &coordWriter{inner: w, editor: le}
}

// readLoop reads bytes from stdin and handles line editing in raw mode.
func (le *lineEditor) readLoop() {
	defer close(le.lines)

	b := make([]byte, 1)
	for {
		n, err := os.Stdin.Read(b)
		if n == 0 || err != nil {
			return
		}

		select {
		case <-le.done:
			return
		default:
		}

		le.mu.Lock()
		switch {
		case b[0] == '\r' || b[0] == '\n': // enter
			line := string(le.buf)
			le.buf = le.buf[:0]
			le.pos = 0
			// clear the prompt line and move to next line
			os.Stdout.Write([]byte("\r\033[K")) //nolint:errcheck
			le.mu.Unlock()
			if line != "" {
				select {
				case le.lines <- line:
				case <-le.done:
					return
				}
			}

		case b[0] == 0x7F || b[0] == 0x08: // backspace/delete
			if le.pos > 0 {
				// remove rune before cursor
				_, size := utf8.DecodeLastRune(le.buf[:le.pos])
				copy(le.buf[le.pos-size:], le.buf[le.pos:])
				le.buf = le.buf[:len(le.buf)-size]
				le.pos -= size
				le.redraw()
			}
			le.mu.Unlock()

		case b[0] == 0x03: // ctrl+c
			le.mu.Unlock()
			le.Close()
			// re-raise SIGINT so the existing signal handler catches it
			p, _ := os.FindProcess(os.Getpid())
			p.Signal(os.Interrupt) //nolint:errcheck
			return

		case b[0] == 0x04: // ctrl+d (EOF)
			le.mu.Unlock()
			return

		case b[0] == 0x1B: // escape sequence start
			le.mu.Unlock()
			// read the next 2 bytes for arrow keys etc (e.g. \x1b[A).
			// done without lock since this may block briefly.
			esc := make([]byte, 2)
			nn, _ := os.Stdin.Read(esc)
			if nn == 2 && esc[0] == '[' {
				le.mu.Lock()
				switch esc[1] {
				case 'D': // left arrow
					if le.pos > 0 {
						_, size := utf8.DecodeLastRune(le.buf[:le.pos])
						le.pos -= size
						le.redraw()
					}
				case 'C': // right arrow
					if le.pos < len(le.buf) {
						_, size := utf8.DecodeRune(le.buf[le.pos:])
						le.pos += size
						le.redraw()
					}
				case 'H': // home
					le.pos = 0
					le.redraw()
				case 'F': // end
					le.pos = len(le.buf)
					le.redraw()
				}
				le.mu.Unlock()
			}

		case b[0] == 0x01: // ctrl+a (home)
			le.pos = 0
			le.redraw()
			le.mu.Unlock()

		case b[0] == 0x05: // ctrl+e (end)
			le.pos = len(le.buf)
			le.redraw()
			le.mu.Unlock()

		case b[0] >= 0x20: // printable character (or UTF-8 lead byte)
			// for multi-byte UTF-8, read continuation bytes without lock
			// since the read may block briefly waiting for remaining bytes.
			var char []byte
			if b[0] >= 0xC0 {
				le.mu.Unlock()
				needed := 0
				switch {
				case b[0] < 0xE0:
					needed = 1
				case b[0] < 0xF0:
					needed = 2
				default:
					needed = 3
				}
				cont := make([]byte, needed)
				nn, readErr := io.ReadFull(os.Stdin, cont)
				char = append(char, b[0])
				if readErr == nil && nn == needed {
					char = append(char, cont...)
				}
				le.mu.Lock()
			} else {
				char = []byte{b[0]}
			}
			// insert at cursor position
			le.buf = append(le.buf, make([]byte, len(char))...)
			copy(le.buf[le.pos+len(char):], le.buf[le.pos:len(le.buf)-len(char)])
			copy(le.buf[le.pos:], char)
			le.pos += len(char)
			le.redraw()
			le.mu.Unlock()

		default: // control characters — ignore
			le.mu.Unlock()
		}
	}
}

// redraw erases the current line and redraws prompt + buffer with cursor at le.pos.
// caller must hold le.mu.
func (le *lineEditor) redraw() {
	os.Stdout.Write([]byte("\r\033[K" + inputPrompt)) //nolint:errcheck
	os.Stdout.Write(le.buf)                           //nolint:errcheck
	// move cursor to the correct position if not at end
	if le.pos < len(le.buf) {
		// count runes after cursor to compute how far back to move
		back := utf8.RuneCount(le.buf[le.pos:])
		fmt.Fprintf(os.Stdout, "\033[%dD", back) //nolint:errcheck
	}
}

// coordWriter wraps an io.Writer and coordinates with the line editor.
// before writing, it erases the current input line; after, it redraws it.
type coordWriter struct {
	inner  io.Writer
	editor *lineEditor
}

func (w *coordWriter) Write(p []byte) (int, error) {
	w.editor.mu.Lock()
	defer w.editor.mu.Unlock()

	// erase current input line
	os.Stdout.Write([]byte("\r\033[K")) //nolint:errcheck

	// in raw mode, terminal doesn't translate \n to \r\n.
	// replace \n with \r\n so output lines start at column 0.
	fixed := crlfReplace(p)
	n, err := w.inner.Write(fixed)
	// report original byte count to caller
	if n > len(p) {
		n = len(p)
	}

	// redraw prompt + current input (if any)
	if len(w.editor.buf) > 0 {
		w.editor.redraw()
	}

	return n, err
}

// crlfReplace replaces bare \n with \r\n for raw terminal mode.
func crlfReplace(p []byte) []byte {
	// fast path: no newlines
	hasLF := false
	for _, b := range p {
		if b == '\n' {
			hasLF = true
			break
		}
	}
	if !hasLF {
		return p
	}

	out := make([]byte, 0, len(p)+16)
	for i, b := range p {
		if b == '\n' && (i == 0 || p[i-1] != '\r') {
			out = append(out, '\r', '\n')
		} else {
			out = append(out, b)
		}
	}
	return out
}
