//go:build windows

package progress

import "os"

// lockFile is a no-op on Windows.
// Windows file locking would require LockFileEx which is more complex.
// The lock is primarily used to detect active sessions, which is a secondary feature.
func lockFile(_ *os.File) error {
	return nil
}

// unlockFile is a no-op on Windows.
func unlockFile(_ *os.File) error {
	return nil
}

// TryLockFile is a no-op on Windows - always returns unlocked.
// Active session detection via file locks is not supported on Windows.
func TryLockFile(_ *os.File) (bool, error) {
	return true, nil // always report as "got lock" (not locked by others)
}
