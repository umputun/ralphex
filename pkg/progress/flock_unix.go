//go:build !windows

package progress

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// lockFile acquires an exclusive lock on the file.
func lockFile(f *os.File) error {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("flock: %w", err)
	}
	return nil
}

// unlockFile releases the lock on the file.
func unlockFile(f *os.File) error {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
		return fmt.Errorf("flock unlock: %w", err)
	}
	return nil
}

// TryLockFile attempts to acquire a non-blocking exclusive lock.
// Returns (true, nil) if lock acquired, (false, nil) if file is locked by another process.
func TryLockFile(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return false, nil // locked by another process
		}
		return false, fmt.Errorf("flock: %w", err)
	}
	// got lock, release it immediately
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return true, nil
}
