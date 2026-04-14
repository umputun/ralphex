package executor

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
)

// readLines reads lines from r and calls handler for each line.
// uses bufio.Reader internally, so there is no line length limit.
// strips trailing \n and \r\n from lines before passing to handler (matching bufio.ScanLines behavior).
// returns nil on EOF, or a wrapped error on context cancellation or read failure.
//
// each blocking ReadString call runs in a goroutine so that context cancellation
// is detected even while the pipe read is in progress (important on Windows where
// child processes may hold the pipe open after the parent exits, causing ReadString
// to block indefinitely until the process is explicitly killed).
func readLines(ctx context.Context, r io.Reader, handler func(string)) error {
	type readResult struct {
		line string
		err  error
	}

	reader := bufio.NewReader(r)
	ch := make(chan readResult, 1) // buffered: lets abandoned goroutine exit after kill

	doRead := func() {
		line, err := reader.ReadString('\n')
		ch <- readResult{line, err}
	}

	go doRead()

	for {
		select {
		case <-ctx.Done():
			// goroutine is still blocked on ReadString; it will unblock when the process
			// is killed (killProcess fires on context cancel) and drain into the buffered ch.
			return fmt.Errorf("read lines: %w", ctx.Err())
		case res := <-ch:
			if res.line != "" {
				handler(trimLineEnding(res.line))
			}
			if res.err != nil {
				if errors.Is(res.err, io.EOF) {
					return nil
				}
				return fmt.Errorf("read lines: %w", res.err)
			}
			go doRead()
		}
	}
}

// trimLineEnding removes trailing line ending to match bufio.ScanLines semantics:
// strips \n, \r\n, or a bare trailing \r (which ScanLines drops via dropCR at EOF).
// unlike strings.TrimRight("\r\n"), this preserves embedded \r characters in content.
func trimLineEnding(line string) string {
	n := len(line)
	if n > 0 && line[n-1] == '\n' {
		n--
	}
	if n > 0 && line[n-1] == '\r' {
		n--
	}
	return line[:n]
}
