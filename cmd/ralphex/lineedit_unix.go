//go:build !windows

package main

import (
	"fmt"
	"io"
	"os"

	"github.com/chzyer/readline"
	"golang.org/x/term"
)

const inputPrompt = "> "

// lineEditor wraps chzyer/readline, providing cursor-aware line editing
// with coordinated output. streaming output written via Stdout() automatically
// erases and redraws the input line.
type lineEditor struct {
	rl    *readline.Instance
	lines chan string
	done  chan struct{}
}

// newLineEditor creates a line editor using chzyer/readline.
// returns nil, nil if stdin is not a terminal (e.g. piped input).
func newLineEditor() (*lineEditor, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return nil, nil //nolint:nilnil // nil,nil signals "not a terminal" to caller
	}

	rl, err := readline.NewEx(&readline.Config{
		Prompt:          inputPrompt,
		InterruptPrompt: "^C",
		EOFPrompt:       "",
	})
	if err != nil {
		return nil, fmt.Errorf("init readline: %w", err)
	}

	le := &lineEditor{
		rl:    rl,
		lines: make(chan string, 8),
		done:  make(chan struct{}),
	}

	go le.readLoop()
	return le, nil
}

// Lines returns a channel of completed input lines.
func (le *lineEditor) Lines() <-chan string {
	return le.lines
}

// Close stops the read loop and restores the terminal.
func (le *lineEditor) Close() {
	select {
	case <-le.done:
		return // already closed
	default:
	}
	close(le.done)
	le.rl.Close()
}

// wrapWriter returns a writer that coordinates with the input line.
// output written through it automatically erases/redraws the prompt.
func (le *lineEditor) wrapWriter(_ io.Writer) io.Writer {
	return le.rl.Stdout()
}

// readLoop reads lines from readline and sends them to the channel.
func (le *lineEditor) readLoop() {
	defer close(le.lines)

	for {
		line, err := le.rl.Readline()
		if err != nil { // io.EOF, interrupt, or closed
			return
		}
		if line == "" {
			continue
		}
		select {
		case le.lines <- line:
		case <-le.done:
			return
		}
	}
}
