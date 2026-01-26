package executor

import (
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// processGroupCleanup manages process group lifecycle for graceful shutdown.
// it ensures that when context is canceled, the entire process tree is killed,
// not just the direct child process.
type processGroupCleanup struct {
	cmd  *exec.Cmd
	done chan struct{}
	once sync.Once
	err  error
}

// setupProcessGroup configures command to run in its own process group.
// this allows killing all descendant processes when cleanup is needed.
func setupProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// newProcessGroupCleanup creates a cleanup handler for the given command.
// the command must already be started before calling this.
// caller must call Wait() exactly once to ensure proper cleanup.
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
// uses graceful shutdown: SIGTERM first, then SIGKILL after brief delay.
func (pg *processGroupCleanup) killProcessGroup() {
	if pg.cmd.Process == nil {
		return
	}

	pgid := -pg.cmd.Process.Pid

	// try graceful shutdown first with SIGTERM
	if err := syscall.Kill(pgid, syscall.SIGTERM); err != nil {
		// process might already be dead (ESRCH), that's fine
		if err != syscall.ESRCH {
			// log unexpected errors but continue to SIGKILL
			fmt.Printf("[executor] SIGTERM failed for pgid %d: %v\n", pgid, err)
		}
		return
	}

	// brief delay for graceful shutdown
	time.Sleep(100 * time.Millisecond)

	// force kill if still alive
	if err := syscall.Kill(pgid, syscall.SIGKILL); err != nil {
		// ESRCH means process already exited after SIGTERM - that's fine
		if err != syscall.ESRCH {
			fmt.Printf("[executor] SIGKILL failed for pgid %d: %v\n", pgid, err)
		}
	}
}

// Wait waits for the command to complete and cleans up resources.
// it is idempotent - multiple calls return the same result without panic.
// caller MUST call this exactly once logically, but repeated calls are safe.
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
