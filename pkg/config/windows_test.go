package config

import (
	"runtime"
	"testing"
)

func skipWindowsPermissionTest(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based permission denial is not portable to Windows")
	}
}
