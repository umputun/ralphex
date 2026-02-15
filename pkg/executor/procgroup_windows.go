//go:build windows

package executor

import (
	"fmt"
	"os/exec"
	"sync"
)

// processGroupCleanup manages process lifecycle for graceful shutdown on Windows.
// Note: Windows doesn't support Unix process groups, so this only kills the direct process.
type processGroupCleanup struct {
	cmd  *exec.Cmd
	done chan struct{}
	once sync.Once
	err  error
}

// setupProcessGroup is a no-op on Windows since process groups work differently.
func setupProcessGroup(_ *exec.Cmd) {
	// windows doesn't support unix session/process group APIs, handled differently
}

// newProcessGroupCleanup creates a cleanup handler for the given command.
// The command must already be started before calling this.
// Caller must eventually call Wait() to ensure proper resource cleanup.
func newProcessGroupCleanup(cmd *exec.Cmd, cancelCh <-chan struct{}) *processGroupCleanup {
	pg := &processGroupCleanup{
		cmd:  cmd,
		done: make(chan struct{}),
	}

	// monitor for cancellation in background
	go pg.watchForCancel(cancelCh)

	return pg
}

// watchForCancel monitors the cancel channel and kills the process if triggered.
func (pg *processGroupCleanup) watchForCancel(cancelCh <-chan struct{}) {
	select {
	case <-cancelCh:
		pg.killProcess()
	case <-pg.done:
		// process completed normally, goroutine exits
	}
}

// killProcess kills the direct process on Windows.
// Note: this won't kill child processes spawned by the command.
func (pg *processGroupCleanup) killProcess() {
	process := pg.cmd.Process
	if process == nil {
		return
	}

	// on Windows, just kill the process directly
	_ = process.Kill()
}

// Wait waits for the command to complete and cleans up resources.
// It is safe to call multiple times - subsequent calls return the cached result.
// Callers must eventually call Wait to avoid leaking resources.
func (pg *processGroupCleanup) Wait() error {
	pg.once.Do(func() {
		pg.err = pg.cmd.Wait()
		close(pg.done)
		if pg.err != nil {
			pg.err = fmt.Errorf("command wait: %w", pg.err)
		}
	})
	return pg.err
}
