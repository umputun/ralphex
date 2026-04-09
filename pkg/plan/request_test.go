package plan_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/plan"
)

func TestResolveRequest(t *testing.T) {
	t.Run("plain text request", func(t *testing.T) {
		req, err := plan.ResolveRequest("add caching")
		require.NoError(t, err)

		assert.Equal(t, "add caching", req.Text)
		assert.Equal(t, "add caching", req.Ref)
		assert.Empty(t, req.File)
	})

	t.Run("file-backed request", func(t *testing.T) {
		tmpDir := t.TempDir()
		origDir, err := os.Getwd()
		require.NoError(t, err)
		require.NoError(t, os.Chdir(tmpDir))
		t.Cleanup(func() { require.NoError(t, os.Chdir(origDir)) })

		path := filepath.Join("requests", "add-caching.md")
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte("\n# Add caching\n\nUse Redis.\n"), 0o600))

		req, err := plan.ResolveRequest("@requests/add-caching.md")
		require.NoError(t, err)

		assert.Equal(t, "# Add caching\n\nUse Redis.", req.Text)
		assert.Regexp(t, `^add-caching-[0-9a-f]{8}$`, req.Ref)
		assert.True(t, filepath.IsAbs(req.File))

		expectedInfo, err := os.Stat(filepath.Join(tmpDir, path))
		require.NoError(t, err)
		actualInfo, err := os.Stat(req.File)
		require.NoError(t, err)
		assert.True(t, os.SameFile(expectedInfo, actualInfo))
	})

	t.Run("literal leading at sign", func(t *testing.T) {
		req, err := plan.ResolveRequest("@@mention-based feature")
		require.NoError(t, err)

		assert.Equal(t, "@mention-based feature", req.Text)
		assert.Equal(t, "@mention-based feature", req.Ref)
		assert.Empty(t, req.File)
	})

	t.Run("missing request file", func(t *testing.T) {
		_, err := plan.ResolveRequest("@missing-request.md")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "read plan request file")
	})

	t.Run("directory request path returns error", func(t *testing.T) {
		tmpDir := t.TempDir()
		reqDir := filepath.Join(tmpDir, "request-dir")
		require.NoError(t, os.MkdirAll(reqDir, 0o700))

		_, err := plan.ResolveRequest("@" + reqDir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "read plan request file")
	})

	t.Run("blank request file returns error", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "blank.md")
		require.NoError(t, os.WriteFile(path, []byte(" \n\t"), 0o600))

		_, err := plan.ResolveRequest("@" + path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "plan request file is empty")
	})

	t.Run("same basename in different directories gets distinct refs", func(t *testing.T) {
		tmpDir := t.TempDir()
		pathA := filepath.Join(tmpDir, "team-a", "request.md")
		pathB := filepath.Join(tmpDir, "team-b", "request.md")
		require.NoError(t, os.MkdirAll(filepath.Dir(pathA), 0o700))
		require.NoError(t, os.MkdirAll(filepath.Dir(pathB), 0o700))
		require.NoError(t, os.WriteFile(pathA, []byte("request A"), 0o600))
		require.NoError(t, os.WriteFile(pathB, []byte("request B"), 0o600))

		reqA, err := plan.ResolveRequest("@" + pathA)
		require.NoError(t, err)
		reqB, err := plan.ResolveRequest("@" + pathB)
		require.NoError(t, err)

		assert.NotEqual(t, reqA.Ref, reqB.Ref)
	})
}
