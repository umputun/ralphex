//go:build windows

package main

// disableCtrlCEcho is a no-op on windows.
func disableCtrlCEcho() func() {
	return func() {}
}
