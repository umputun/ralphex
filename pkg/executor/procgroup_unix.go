//go:build !windows

package executor

import (
	"fmt"
	"log"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// gracefulShutdownDelay is the time to wait between SIGTERM and SIGKILL.
const gracefulShutdownDelay = 100 * time.Millisecond

// processGroupCleanup manages process group lifecycle for graceful shutdown.
// It ensures that when context is canceled, the entire process tree is killed,
// not just the direct child process.
type processGroupCleanup struct {
	cmd  *exec.Cmd
	done chan struct{}
	once sync.Once
	err  error
}

// setupProcessGroup configures command to run in its own session and process group.
// uses Setsid to create a new session, fully detaching the child from the parent's
// controlling terminal. this prevents SIGTTIN/SIGTTOU signals that stop the child
// process group when its descendants (e.g. shell, gopls) attempt terminal I/O.
// the child becomes session leader of its own process group, so killing via -pid still works.
// Must be called before cmd.Start().
func setupProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
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

// watchForCancel monitors the cancel channel and kills the process group if triggered.
func (pg *processGroupCleanup) watchForCancel(cancelCh <-chan struct{}) {
	select {
	case <-cancelCh:
		pg.killProcessGroup()
	case <-pg.done:
		// process completed normally, goroutine exits
	}
}

// killProcessGroup sends SIGTERM followed by SIGKILL to the entire process group.
// Uses graceful shutdown: SIGTERM first, then SIGKILL after brief delay.
func (pg *processGroupCleanup) killProcessGroup() {
	process := pg.cmd.Process
	if process == nil {
		return
	}

	pid := process.Pid
	if pid <= 0 {
		log.Printf("[executor] invalid PID %d, skipping process group kill", pid)
		return
	}

	pgid := -pid

	// try graceful shutdown first with SIGTERM
	if err := syscall.Kill(pgid, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
		log.Printf("[executor] SIGTERM failed for pgid %d: %v", pgid, err)
	}

	// brief delay for graceful shutdown
	time.Sleep(gracefulShutdownDelay)

	// force kill if still alive (always attempt, even if SIGTERM failed)
	if err := syscall.Kill(pgid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		log.Printf("[executor] SIGKILL failed for pgid %d: %v", pgid, err)
	}
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
