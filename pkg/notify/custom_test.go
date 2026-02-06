package notify

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCustomChannel(t *testing.T) {
	ch := newCustomChannel("/usr/local/bin/notify.sh")
	assert.Equal(t, "/usr/local/bin/notify.sh", ch.scriptPath)
}

func TestCustomChannel_Send(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts not supported on windows")
	}

	t.Run("pipes json to script stdin", func(t *testing.T) {
		r := Result{
			Status:    "success",
			Mode:      "full",
			PlanFile:  "docs/plans/test.md",
			Branch:    "feature",
			Duration:  "5m 30s",
			Files:     3,
			Additions: 50,
			Deletions: 10,
		}

		// create a wrapper script that writes stdin to a temp file so we can verify
		tmpDir := t.TempDir()
		outputFile := filepath.Join(tmpDir, "output.json")
		wrapperScript := filepath.Join(tmpDir, "wrapper.sh")
		err := os.WriteFile(wrapperScript, //nolint:gosec // test helper script needs execute permission
			[]byte("#!/bin/sh\ncat > "+outputFile+"\n"), 0o700)
		require.NoError(t, err)

		ch := newCustomChannel(wrapperScript)
		err = ch.send(context.Background(), r)
		require.NoError(t, err)

		// verify the json that was piped
		data, err := os.ReadFile(outputFile) //nolint:gosec // path from t.TempDir()
		require.NoError(t, err)

		var got Result
		err = json.Unmarshal(data, &got)
		require.NoError(t, err)
		assert.Equal(t, r, got)
	})

	t.Run("non-zero exit code returns error", func(t *testing.T) {
		script := filepath.Join("testdata", "fail.sh")
		ch := newCustomChannel(script)

		err := ch.send(context.Background(), Result{Status: "success"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "script")
		assert.Contains(t, err.Error(), "script failed")
	})

	t.Run("stdout included in error message", func(t *testing.T) {
		script := filepath.Join("testdata", "fail_with_stdout.sh")
		ch := newCustomChannel(script)

		err := ch.send(context.Background(), Result{Status: "success"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "stdout info")
		assert.Contains(t, err.Error(), "stderr info")
		assert.Contains(t, err.Error(), "output:")
	})

	t.Run("timeout kills script", func(t *testing.T) {
		script := filepath.Join("testdata", "slow.sh")
		ch := newCustomChannel(script)

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		err := ch.send(ctx, Result{Status: "success"})
		require.Error(t, err)
	})

	t.Run("nonexistent script returns error", func(t *testing.T) {
		ch := newCustomChannel("/nonexistent/script.sh")
		err := ch.send(context.Background(), Result{Status: "success"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "script /nonexistent/script.sh")
	})

	t.Run("failure result json includes error field", func(t *testing.T) {
		tmpDir := t.TempDir()
		outputFile := filepath.Join(tmpDir, "output.json")
		wrapperScript := filepath.Join(tmpDir, "wrapper.sh")
		err := os.WriteFile(wrapperScript, //nolint:gosec // test helper script needs execute permission
			[]byte("#!/bin/sh\ncat > "+outputFile+"\n"), 0o700)
		require.NoError(t, err)

		ch := newCustomChannel(wrapperScript)
		r := Result{Status: "failure", Error: "task phase: max iterations reached"}

		err = ch.send(context.Background(), r)
		require.NoError(t, err)

		data, err := os.ReadFile(outputFile) //nolint:gosec // path from t.TempDir()
		require.NoError(t, err)

		var got Result
		err = json.Unmarshal(data, &got)
		require.NoError(t, err)
		assert.Equal(t, "failure", got.Status)
		assert.Equal(t, "task phase: max iterations reached", got.Error)
	})
}
