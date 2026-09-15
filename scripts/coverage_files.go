//go:build ignore

package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fatalf("usage: go run scripts/coverage_files.go <filter|clean> ...")
	}

	switch os.Args[1] {
	case "filter":
		if len(os.Args) != 4 {
			fatalf("usage: go run scripts/coverage_files.go filter <input> <output>")
		}
		if err := filterCoverage(os.Args[2], os.Args[3]); err != nil {
			fatalf("%v", err)
		}
	case "clean":
		if len(os.Args) < 3 {
			fatalf("usage: go run scripts/coverage_files.go clean <file> [file...]")
		}
		for _, name := range os.Args[2:] {
			if err := os.Remove(name); err != nil && !os.IsNotExist(err) {
				fatalf("remove %s: %v", name, err)
			}
		}
	default:
		fatalf("unknown command %q", os.Args[1])
	}
}

func filterCoverage(input, output string) error {
	in, err := os.Open(input) //nolint:gosec // developer-supplied make target paths
	if err != nil {
		return fmt.Errorf("open coverage input: %w", err)
	}
	defer in.Close()

	out, err := os.Create(output) //nolint:gosec // developer-supplied make target paths
	if err != nil {
		return fmt.Errorf("create coverage output: %w", err)
	}
	defer out.Close()

	scanner := bufio.NewScanner(in)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "_mock.go") || strings.Contains(line, "mocks") {
			continue
		}
		if _, err := fmt.Fprintln(out, line); err != nil {
			return fmt.Errorf("write coverage output: %w", err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read coverage input: %w", err)
	}
	return nil
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
