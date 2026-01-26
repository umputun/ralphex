//go:build unix

package executor

import (
	"bufio"
	"context"
	"io"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecClaudeRunner_KillsProcessGroup(t *testing.T) {
	// this test verifies that when context is canceled, the entire process group
	// is killed (including child processes), not just the direct child.
	// this prevents orphaned processes when ralphex exits.

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runner := &execClaudeRunner{}

	// bash spawns a background sleep, prints its PID, then waits forever.
	// the "wait" keeps parent alive until we cancel.
	stdout, wait, err := runner.Run(ctx, "bash", "-c",
		`sleep 300 & echo "CHILD_PID:$!"; wait`)
	require.NoError(t, err)

	// read until we get the child PID (with timeout)
	childPID := readChildPID(t, stdout)
	require.NotZero(t, childPID, "should capture child PID from output")

	// verify child process exists before we cancel
	require.True(t, processExists(childPID), "child process should be running before cancel")

	// cancel context - this should kill the process group
	cancel()

	// wait for command to exit (will error due to kill, that's expected)
	err = wait()
	require.Error(t, err, "wait should error when process is killed")

	// verify child process is killed using polling instead of fixed sleep
	require.Eventually(t, func() bool {
		return !processExists(childPID)
	}, 2*time.Second, 50*time.Millisecond,
		"child process (PID %d) should be killed when parent's process group is killed", childPID)
}

func TestProcessGroupCleanup_Idempotent(t *testing.T) {
	// verify that Wait() can be called multiple times without panicking

	ctx := t.Context()

	runner := &execClaudeRunner{}

	stdout, wait, err := runner.Run(ctx, "echo", "hello")
	require.NoError(t, err)

	// drain stdout
	_, _ = io.ReadAll(stdout)

	// first wait should succeed
	err1 := wait()

	// second wait should return same result without panic
	err2 := wait()

	// third wait should also be fine
	err3 := wait()

	// all calls should return the same result
	assert.Equal(t, err1, err2, "repeated Wait() calls should return same error")
	assert.Equal(t, err2, err3, "repeated Wait() calls should return same error")
}

// readChildPID reads from stdout until it finds "CHILD_PID:N" and returns N.
func readChildPID(t *testing.T, r io.Reader) int {
	t.Helper()

	done := make(chan int, 1)
	go func() {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			if pidStr, ok := strings.CutPrefix(line, "CHILD_PID:"); ok {
				pid, err := strconv.Atoi(pidStr)
				if err == nil {
					done <- pid
					return
				}
			}
		}
		done <- 0
	}()

	select {
	case pid := <-done:
		return pid
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for child PID from bash output")
		return 0
	}
}

// processExists checks if a process with given PID exists.
func processExists(pid int) bool {
	// signal 0 checks if process exists without actually sending a signal
	return syscall.Kill(pid, 0) == nil
}
