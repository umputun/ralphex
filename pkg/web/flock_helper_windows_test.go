//go:build windows

package web

import "testing"

// holdFileLockForTest is a no-op on Windows since the package's flock-based
// activity detection is disabled there (IsActive always returns false).
// callers that need a real cross-process active state must skip on Windows.
func holdFileLockForTest(t *testing.T, _ string) func() {
	t.Helper()
	t.Skip("flock-based active detection is unix-only")
	return func() {}
}
