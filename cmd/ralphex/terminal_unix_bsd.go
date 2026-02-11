//go:build darwin || freebsd || openbsd || netbsd || dragonfly

package main

import "golang.org/x/sys/unix"

const (
	ioctlReadTermios  = unix.TIOCGETA
	ioctlWriteTermios = unix.TIOCSETA
)
