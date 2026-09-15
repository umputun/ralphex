package executor

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

func TestPortableEchoProcess(t *testing.T) {
	if os.Getenv("RALPHEX_TEST_ECHO_PROCESS") != "1" {
		return
	}
	fmt.Print(strings.Join(os.Args[3:], " "))
	os.Exit(0)
}

func TestPortableCatProcess(t *testing.T) {
	if os.Getenv("RALPHEX_TEST_CAT_PROCESS") != "1" {
		return
	}
	data, _ := io.ReadAll(os.Stdin)
	fmt.Print(string(data))
	os.Exit(0)
}
