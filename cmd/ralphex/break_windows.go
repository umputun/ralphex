//go:build windows

package main

// startBreakSignal returns nil on windows — SIGQUIT is not available.
// manual break feature is disabled on this platform.
func startBreakSignal() <-chan struct{} {
	return nil
}
