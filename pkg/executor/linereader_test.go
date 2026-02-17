package executor

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadLines_BasicMultiLine(t *testing.T) {
	input := "line one\nline two\nline three\n"
	var lines []string
	err := readLines(context.Background(), strings.NewReader(input), func(line string) {
		lines = append(lines, line)
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"line one", "line two", "line three"}, lines)
}

func TestReadLines_WindowsLineEndings(t *testing.T) {
	input := "line one\r\nline two\r\n"
	var lines []string
	err := readLines(context.Background(), strings.NewReader(input), func(line string) {
		lines = append(lines, line)
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"line one", "line two"}, lines)
}

func TestReadLines_FinalLineWithoutNewline(t *testing.T) {
	input := "line one\nline two"
	var lines []string
	err := readLines(context.Background(), strings.NewReader(input), func(line string) {
		lines = append(lines, line)
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"line one", "line two"}, lines)
}

func TestReadLines_EmptyLines(t *testing.T) {
	input := "first\n\n\nlast\n"
	var lines []string
	err := readLines(context.Background(), strings.NewReader(input), func(line string) {
		lines = append(lines, line)
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"first", "", "", "last"}, lines)
}

func TestReadLines_EmptyInput(t *testing.T) {
	var lines []string
	err := readLines(context.Background(), strings.NewReader(""), func(line string) {
		lines = append(lines, line)
	})
	require.NoError(t, err)
	assert.Empty(t, lines)
}

func TestReadLines_SingleLineNoNewline(t *testing.T) {
	var lines []string
	err := readLines(context.Background(), strings.NewReader("hello"), func(line string) {
		lines = append(lines, line)
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"hello"}, lines)
}

func TestReadLines_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := readLines(ctx, strings.NewReader("line1\nline2\n"), func(_ string) {})
	require.ErrorIs(t, err, context.Canceled)
	assert.Contains(t, err.Error(), "read lines")
}

func TestReadLines_ContextCancelMidRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	var count int
	// use a reader that provides lines one at a time via a pipe
	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write([]byte("line1\nline2\n"))
		pw.Close()
	}()

	err := readLines(ctx, pr, func(_ string) {
		count++
		if count == 1 {
			cancel() // cancel after first line
		}
	})
	// may get canceled or may complete (depends on scheduling),
	// but should not hang
	if err != nil {
		require.ErrorIs(t, err, context.Canceled)
	}
	assert.GreaterOrEqual(t, count, 1)
}

func TestReadLines_ReadError(t *testing.T) {
	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write([]byte("line1\n"))
		pw.CloseWithError(io.ErrUnexpectedEOF)
	}()

	var lines []string
	err := readLines(context.Background(), pr, func(line string) {
		lines = append(lines, line)
	})
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	assert.Contains(t, err.Error(), "read lines")
	assert.Equal(t, []string{"line1"}, lines)
}

func TestReadLines_LargeLineOver64MB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 65MB allocation in short mode")
	}
	// verify there is no line length limit (the whole point of this helper)
	const size = 65 * 1024 * 1024 // 65MB, exceeds old 64MB Scanner limit
	largeLine := strings.Repeat("x", size)
	input := largeLine + "\n"

	var lines []string
	err := readLines(context.Background(), strings.NewReader(input), func(line string) {
		lines = append(lines, line)
	})
	require.NoError(t, err)
	require.Len(t, lines, 1)
	assert.Len(t, lines[0], size)
}

func TestTrimLineEnding(t *testing.T) {
	tests := []struct {
		name, input, expected string
	}{
		{name: "unix newline", input: "hello\n", expected: "hello"},
		{name: "windows newline", input: "hello\r\n", expected: "hello"},
		{name: "no newline", input: "hello", expected: "hello"},
		{name: "empty string", input: "", expected: ""},
		{name: "just newline", input: "\n", expected: ""},
		{name: "just crlf", input: "\r\n", expected: ""},
		{name: "embedded cr preserved", input: "data\r\n", expected: "data"},
		{name: "trailing cr in content", input: "data\r\r\n", expected: "data\r"},
		{name: "multiple trailing cr", input: "data\r\r\r\n", expected: "data\r\r"},
		{name: "bare cr no newline", input: "data\r", expected: "data"},
		{name: "bare cr only", input: "\r", expected: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, trimLineEnding(tt.input))
		})
	}
}

func TestReadLines_EmbeddedCarriageReturn(t *testing.T) {
	// verify that \r characters in line content are preserved (not stripped)
	input := "data\r\r\n"
	var lines []string
	err := readLines(context.Background(), strings.NewReader(input), func(line string) {
		lines = append(lines, line)
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"data\r"}, lines)
}

func TestReadLines_MixedContent(t *testing.T) {
	// simulates real stream: json lines, empty lines, non-json lines
	input := `{"type":"event"}` + "\n" +
		"\n" +
		"plain text\n" +
		`{"type":"delta","text":"hello"}` + "\n"

	var lines []string
	err := readLines(context.Background(), strings.NewReader(input), func(line string) {
		lines = append(lines, line)
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		`{"type":"event"}`,
		"",
		"plain text",
		`{"type":"delta","text":"hello"}`,
	}, lines)
}
