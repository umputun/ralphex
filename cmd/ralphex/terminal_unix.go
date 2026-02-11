//go:build !windows

package main

import (
	"os"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// disableCtrlCEcho disables the ECHOCTL terminal flag so that pressing Ctrl+C
// does not echo "^C" to the terminal. returns a function that restores the original state.
func disableCtrlCEcho() func() {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return func() {}
	}

	termios, err := unix.IoctlGetTermios(fd, ioctlReadTermios)
	if err != nil {
		return func() {}
	}

	original := *termios
	termios.Lflag &^= unix.ECHOCTL
	if err := unix.IoctlSetTermios(fd, ioctlWriteTermios, termios); err != nil {
		return func() {}
	}

	return func() {
		unix.IoctlSetTermios(fd, ioctlWriteTermios, &original) //nolint:errcheck // best-effort restore
	}
}
