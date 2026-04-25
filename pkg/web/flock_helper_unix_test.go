//go:build !windows

package web

import (
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// holdFileLockForTest opens path and acquires a blocking exclusive flock,
// returning a release function that unlocks and closes the file. used by
// watcher tests that need IsActive(path) to report true (cross-process
// flock detection). on Windows there is no flock, so the helper is unix-only;
// callers must skip via runtime.GOOS == "windows".
func holdFileLockForTest(t *testing.T, path string) func() {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0o600) //nolint:gosec // test-controlled path from t.TempDir
	require.NoError(t, err)
	require.NoError(t, syscall.Flock(int(f.Fd()), syscall.LOCK_EX))
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}
}
