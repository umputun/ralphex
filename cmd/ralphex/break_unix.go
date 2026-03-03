//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
)

// startBreakSignal listens for SIGQUIT (Ctrl+\) and returns a channel
// that is closed when the signal is received. used for manual termination
// of the external review loop.
func startBreakSignal() <-chan struct{} {
	ch := make(chan struct{})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGQUIT)
	go func() {
		<-sig
		close(ch)
		signal.Stop(sig)
	}()
	return ch
}
