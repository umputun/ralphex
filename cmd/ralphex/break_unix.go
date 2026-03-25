//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
)

// startBreakSignal listens for SIGQUIT (Ctrl+\) and returns a buffered channel
// that receives a value on each signal. used for manual termination of review
// and task loops.
func startBreakSignal() <-chan struct{} {
	ch := make(chan struct{}, 1)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGQUIT)
	go func() {
		for range sig {
			select {
			case ch <- struct{}{}:
			default: // drop if no reader is ready
			}
		}
	}()
	return ch
}
