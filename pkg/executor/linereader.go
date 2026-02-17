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
func readLines(ctx context.Context, r io.Reader, handler func(string)) error {
	reader := bufio.NewReader(r)
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("read lines: %w", ctx.Err())
		default:
		}
		line, err := reader.ReadString('\n')
		if line != "" {
			line = trimLineEnding(line)
			handler(line)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("read lines: %w", err)
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
