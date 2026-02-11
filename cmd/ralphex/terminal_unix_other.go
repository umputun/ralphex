//go:build !darwin && !freebsd && !openbsd && !netbsd && !dragonfly && !windows

package main

import "golang.org/x/sys/unix"

const (
	ioctlReadTermios  = unix.TCGETS
	ioctlWriteTermios = unix.TCSETS
)
