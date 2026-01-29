package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenRepo(t *testing.T) {
	t.Run("opens valid repo", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)
		assert.NotNil(t, r)
	})

	t.Run("fails on non-repo", func(t *testing.T) {
		dir := t.TempDir()
		_, err := openRepo(dir)
		assert.Error(t, err)
	})

	t.Run("opens git worktree", func(t *testing.T) {
		mainDir := setupTestRepo(t)

		// create worktree using git CLI (go-git doesn't support worktree creation)
		wtDir := filepath.Join(t.TempDir(), "worktree")
		cmd := exec.Command("git", "worktree", "add", wtDir, "-b", "wt-branch") //nolint:gosec // wtDir from t.TempDir() is safe
		cmd.Dir = mainDir
		require.NoError(t, cmd.Run())

		// open worktree with our openRepo()
		r, err := openRepo(wtDir)
		require.NoError(t, err)

		branch, err := r.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "wt-branch", branch)
	})
}

func TestRepo_toRelative(t *testing.T) {
	t.Run("returns repo-relative path unchanged", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		rel, err := r.toRelative("docs/plans/test.md")
		require.NoError(t, err)
		assert.Equal(t, "docs/plans/test.md", rel)
	})

	t.Run("converts absolute path to relative", func(t *testing.T) {
		dir := setupTestRepo(t)
		// resolve symlinks for consistent paths (macOS /var -> /private/var)
		dir, err := filepath.EvalSymlinks(dir)
		require.NoError(t, err)

		r, err := openRepo(dir)
		require.NoError(t, err)

		absPath := filepath.Join(dir, "docs", "plans", "test.md")
		rel, err := r.toRelative(absPath)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("docs", "plans", "test.md"), rel)
	})

	t.Run("rejects .. path", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		_, err = r.toRelative("../outside.txt")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "escapes repository root")
	})

	t.Run("rejects absolute path outside repo", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		_, err = r.toRelative("/tmp/outside/file.txt")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "outside repository")
	})

	t.Run("rejects ./../ path", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		_, err = r.toRelative("./../outside.txt")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "escapes repository root")
	})
}

func TestRepo_CurrentBranch(t *testing.T) {
	t.Run("returns master for new repo", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		branch, err := r.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "master", branch)
	})

	t.Run("returns feature branch name", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		err = r.CreateBranch("feature-test")
		require.NoError(t, err)

		branch, err := r.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "feature-test", branch)
	})

	t.Run("returns empty string for detached HEAD", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		// get current HEAD hash
		head, err := r.gitRepo.Head()
		require.NoError(t, err)

		// checkout the commit hash directly (detached HEAD)
		wt, err := r.gitRepo.Worktree()
		require.NoError(t, err)
		err = wt.Checkout(&git.CheckoutOptions{Hash: head.Hash()})
		require.NoError(t, err)

		// should return empty string for detached HEAD
		branch, err := r.CurrentBranch()
		require.NoError(t, err)
		assert.Empty(t, branch)
	})
}

func TestRepo_CreateBranch(t *testing.T) {
	t.Run("creates and switches to branch", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		err = r.CreateBranch("new-feature")
		require.NoError(t, err)

		branch, err := r.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "new-feature", branch)
	})

	t.Run("fails on invalid branch name", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		err = r.CreateBranch("invalid..name")
		assert.Error(t, err)
	})

	t.Run("fails when branch already exists", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		// create branch first
		err = r.CreateBranch("existing")
		require.NoError(t, err)

		// switch back to master
		err = r.CheckoutBranch("master")
		require.NoError(t, err)

		// try to create same branch again
		err = r.CreateBranch("existing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "already exists")
	})

	t.Run("preserves untracked files", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		// create an untracked file while on master
		untrackedPath := filepath.Join(dir, "untracked.txt")
		err = os.WriteFile(untrackedPath, []byte("untracked content"), 0o600)
		require.NoError(t, err)

		// verify file exists before creating branch
		_, err = os.Stat(untrackedPath)
		require.NoError(t, err, "untracked file should exist before branch creation")

		// create and switch to new branch
		err = r.CreateBranch("feature")
		require.NoError(t, err)

		// verify untracked file still exists after branch creation
		_, err = os.Stat(untrackedPath)
		require.NoError(t, err, "untracked file should be preserved after branch creation")

		// verify content is intact
		content, err := os.ReadFile(untrackedPath) //nolint:gosec // test file path
		require.NoError(t, err)
		assert.Equal(t, "untracked content", string(content))
	})
}

func TestRepo_Add(t *testing.T) {
	t.Run("stages new file", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		// create a new file
		testFile := filepath.Join(dir, "newfile.txt")
		err = os.WriteFile(testFile, []byte("test content"), 0o600)
		require.NoError(t, err)

		err = r.Add("newfile.txt")
		require.NoError(t, err)

		// verify file is staged
		wt, err := r.gitRepo.Worktree()
		require.NoError(t, err)
		status, err := wt.Status()
		require.NoError(t, err)
		assert.Equal(t, git.Added, status["newfile.txt"].Staging)
	})

	t.Run("fails on non-existent file", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		err = r.Add("nonexistent.txt")
		assert.Error(t, err)
	})
}

func TestRepo_Commit(t *testing.T) {
	t.Run("creates commit", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		// create and stage a file
		testFile := filepath.Join(dir, "commit-test.txt")
		err = os.WriteFile(testFile, []byte("test"), 0o600)
		require.NoError(t, err)

		err = r.Add("commit-test.txt")
		require.NoError(t, err)

		err = r.Commit("test commit message")
		require.NoError(t, err)

		// verify commit was created
		head, err := r.gitRepo.Head()
		require.NoError(t, err)
		commit, err := r.gitRepo.CommitObject(head.Hash())
		require.NoError(t, err)
		assert.Equal(t, "test commit message", commit.Message)
	})

	t.Run("commit has valid author", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		// create and stage a file
		testFile := filepath.Join(dir, "author-test.txt")
		err = os.WriteFile(testFile, []byte("test"), 0o600)
		require.NoError(t, err)

		err = r.Add("author-test.txt")
		require.NoError(t, err)

		err = r.Commit("test author")
		require.NoError(t, err)

		// verify commit has author info
		head, err := r.gitRepo.Head()
		require.NoError(t, err)
		commit, err := r.gitRepo.CommitObject(head.Hash())
		require.NoError(t, err)
		assert.NotEmpty(t, commit.Author.Name, "author name should not be empty")
		assert.NotEmpty(t, commit.Author.Email, "author email should not be empty")
		assert.False(t, commit.Author.When.IsZero(), "author time should be set")
	})

	t.Run("fails with no staged changes", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		// try to commit without staging anything
		err = r.Commit("empty commit")
		assert.Error(t, err)
	})
}

func TestRepo_getAuthor(t *testing.T) {
	t.Run("returns valid signature", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		author := r.getAuthor()
		require.NotNil(t, author)
		assert.NotEmpty(t, author.Name, "author name should not be empty")
		assert.NotEmpty(t, author.Email, "author email should not be empty")
		assert.False(t, author.When.IsZero(), "author time should be set")
	})

	t.Run("fallback has expected values", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		author := r.getAuthor()
		require.NotNil(t, author)
		// either from global config or fallback - both are valid
		// just ensure we got something reasonable
		assert.NotEmpty(t, author.Name, "name should have content")
		assert.Contains(t, author.Email, "@", "email should contain @")
	})
}

func TestRepo_MoveFile(t *testing.T) {
	t.Run("moves file and stages changes", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		// create destination directory
		err = os.MkdirAll(filepath.Join(dir, "subdir"), 0o750)
		require.NoError(t, err)

		// move the initial file
		err = r.MoveFile("README.md", filepath.Join("subdir", "README.md"))
		require.NoError(t, err)

		// verify old file removed
		_, err = os.Stat(filepath.Join(dir, "README.md"))
		assert.True(t, os.IsNotExist(err))

		// verify new file exists
		_, err = os.Stat(filepath.Join(dir, "subdir", "README.md"))
		require.NoError(t, err)

		// verify both changes are staged
		wt, err := r.gitRepo.Worktree()
		require.NoError(t, err)
		status, err := wt.Status()
		require.NoError(t, err)
		assert.Equal(t, git.Deleted, status["README.md"].Staging)
		assert.Equal(t, git.Added, status[filepath.Join("subdir", "README.md")].Staging)
	})

	t.Run("moves file with absolute paths", func(t *testing.T) {
		dir := setupTestRepo(t)
		// resolve symlinks for consistent paths (macOS /var -> /private/var)
		dir, err := filepath.EvalSymlinks(dir)
		require.NoError(t, err)

		r, err := openRepo(dir)
		require.NoError(t, err)

		// create destination directory
		subdir := filepath.Join(dir, "subdir")
		err = os.MkdirAll(subdir, 0o750)
		require.NoError(t, err)

		// move using absolute paths
		srcAbs := filepath.Join(dir, "README.md")
		dstAbs := filepath.Join(subdir, "README.md")
		err = r.MoveFile(srcAbs, dstAbs)
		require.NoError(t, err)

		// verify move worked
		_, err = os.Stat(srcAbs)
		assert.True(t, os.IsNotExist(err))
		_, err = os.Stat(dstAbs)
		require.NoError(t, err)
	})

	t.Run("fails on non-existent source file", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		err = r.MoveFile("nonexistent.txt", "dest.txt")
		assert.Error(t, err)
	})

	t.Run("fails on path outside repo", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		err = r.MoveFile("/tmp/outside.txt", "dest.txt")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "outside repository")
	})
}

func TestRepo_BranchExists(t *testing.T) {
	t.Run("returns true for existing branch", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		// master exists by default
		assert.True(t, r.BranchExists("master"))
	})

	t.Run("returns false for non-existent branch", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		assert.False(t, r.BranchExists("nonexistent"))
	})

	t.Run("returns true for created branch", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		err = r.CreateBranch("new-branch")
		require.NoError(t, err)

		assert.True(t, r.BranchExists("new-branch"))
	})
}

func TestRepo_CheckoutBranch(t *testing.T) {
	t.Run("switches to existing branch", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		// create a branch first
		err = r.CreateBranch("feature")
		require.NoError(t, err)

		// switch back to master
		err = r.CheckoutBranch("master")
		require.NoError(t, err)

		branch, err := r.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "master", branch)
	})

	t.Run("fails on non-existent branch", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		err = r.CheckoutBranch("nonexistent")
		assert.Error(t, err)
	})

	t.Run("preserves untracked files", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		// create a branch
		err = r.CreateBranch("feature")
		require.NoError(t, err)

		// switch back to master
		err = r.CheckoutBranch("master")
		require.NoError(t, err)

		// create an untracked file while on master
		untrackedPath := filepath.Join(dir, "untracked.txt")
		err = os.WriteFile(untrackedPath, []byte("untracked content"), 0o600)
		require.NoError(t, err)

		// verify file exists before checkout
		_, err = os.Stat(untrackedPath)
		require.NoError(t, err, "untracked file should exist before checkout")

		// switch to feature branch
		err = r.CheckoutBranch("feature")
		require.NoError(t, err)

		// verify untracked file still exists after checkout
		_, err = os.Stat(untrackedPath)
		require.NoError(t, err, "untracked file should be preserved after checkout")

		// verify content is intact
		content, err := os.ReadFile(untrackedPath) //nolint:gosec // test file path
		require.NoError(t, err)
		assert.Equal(t, "untracked content", string(content))
	})
}

func TestRepo_IsDirty(t *testing.T) {
	t.Run("clean worktree returns false", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		dirty, err := r.IsDirty()
		require.NoError(t, err)
		assert.False(t, dirty)
	})

	t.Run("staged file returns true", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		// create and stage a new file
		testFile := filepath.Join(dir, "staged.txt")
		err = os.WriteFile(testFile, []byte("staged content"), 0o600)
		require.NoError(t, err)

		err = r.Add("staged.txt")
		require.NoError(t, err)

		dirty, err := r.IsDirty()
		require.NoError(t, err)
		assert.True(t, dirty)
	})

	t.Run("modified tracked file returns true", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		// modify the existing README.md (which is tracked)
		readmePath := filepath.Join(dir, "README.md")
		err = os.WriteFile(readmePath, []byte("# Modified\n"), 0o600)
		require.NoError(t, err)

		dirty, err := r.IsDirty()
		require.NoError(t, err)
		assert.True(t, dirty)
	})

	t.Run("deleted tracked file returns true", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		// delete the existing README.md (which is tracked)
		readmePath := filepath.Join(dir, "README.md")
		err = os.Remove(readmePath)
		require.NoError(t, err)

		dirty, err := r.IsDirty()
		require.NoError(t, err)
		assert.True(t, dirty)
	})

	t.Run("untracked file only returns false", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		// create a new file without staging it
		testFile := filepath.Join(dir, "untracked.txt")
		err = os.WriteFile(testFile, []byte("untracked content"), 0o600)
		require.NoError(t, err)

		dirty, err := r.IsDirty()
		require.NoError(t, err)
		assert.False(t, dirty)
	})

	t.Run("gitignored file should not make repo dirty", func(t *testing.T) {
		// reproduces issue #28: go-git reports gitignored files unlike native git
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		// create .gitignore with patterns
		gitignorePath := filepath.Join(dir, ".gitignore")
		err = os.WriteFile(gitignorePath, []byte("ignored.txt\n*.log\nbuild/\n"), 0o600)
		require.NoError(t, err)

		// commit gitignore so it takes effect
		err = r.Add(".gitignore")
		require.NoError(t, err)
		err = r.Commit("add gitignore")
		require.NoError(t, err)

		// create files that match gitignore patterns
		ignoredFile := filepath.Join(dir, "ignored.txt")
		err = os.WriteFile(ignoredFile, []byte("should be ignored"), 0o600)
		require.NoError(t, err)

		logFile := filepath.Join(dir, "debug.log")
		err = os.WriteFile(logFile, []byte("log content"), 0o600)
		require.NoError(t, err)

		// create ignored directory with files
		buildDir := filepath.Join(dir, "build")
		err = os.MkdirAll(buildDir, 0o750)
		require.NoError(t, err)
		buildFile := filepath.Join(buildDir, "output.bin")
		err = os.WriteFile(buildFile, []byte("binary"), 0o600)
		require.NoError(t, err)

		// native git would show clean, go-git might report dirty
		dirty, err := r.IsDirty()
		require.NoError(t, err)
		assert.False(t, dirty, "gitignored files should not make repo dirty")
	})

	t.Run("dangling symlink should not make repo dirty", func(t *testing.T) {
		// reproduces issue #28: dangling symlinks reported as modified by go-git
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		// create a target file
		targetFile := filepath.Join(dir, "target.txt")
		err = os.WriteFile(targetFile, []byte("target content"), 0o600)
		require.NoError(t, err)

		// create symlink pointing to it (using relative path - how git stores symlinks)
		symlinkPath := filepath.Join(dir, "link.txt")
		err = os.Symlink("target.txt", symlinkPath) // relative, not absolute
		require.NoError(t, err)

		// commit both
		err = r.Add("target.txt")
		require.NoError(t, err)
		err = r.Add("link.txt")
		require.NoError(t, err)
		err = r.Commit("add file and symlink")
		require.NoError(t, err)

		// verify clean after commit
		dirty, err := r.IsDirty()
		require.NoError(t, err)
		require.False(t, dirty, "should be clean after commit")

		// delete the target file, making symlink dangle
		err = os.Remove(targetFile)
		require.NoError(t, err)

		// stage removal of target
		wt, err := r.gitRepo.Worktree()
		require.NoError(t, err)
		_, err = wt.Remove("target.txt")
		require.NoError(t, err)
		err = r.Commit("remove target")
		require.NoError(t, err)

		// now only the dangling symlink remains
		// native git shows clean, go-git might report dirty
		dirty, err = r.IsDirty()
		require.NoError(t, err)
		assert.False(t, dirty, "dangling symlink should not make repo dirty")
	})

	t.Run("absolute symlink appears modified to go-git", func(t *testing.T) {
		// documents go-git quirk: symlinks with absolute paths are reported as modified
		// because go-git stores symlink target in index, absolute path differs from what was committed
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		// create a target file
		targetFile := filepath.Join(dir, "target.txt")
		err = os.WriteFile(targetFile, []byte("target content"), 0o600)
		require.NoError(t, err)

		// create symlink with ABSOLUTE path (this is the problematic case)
		symlinkPath := filepath.Join(dir, "link.txt")
		err = os.Symlink(targetFile, symlinkPath) // absolute path
		require.NoError(t, err)

		// commit
		err = r.Add("target.txt")
		require.NoError(t, err)
		err = r.Add("link.txt")
		require.NoError(t, err)
		err = r.Commit("add file and absolute symlink")
		require.NoError(t, err)

		// go-git will report this as dirty because symlink target in worktree (absolute)
		// differs from what git stored (relative). this is expected go-git behavior.
		dirty, err := r.IsDirty()
		require.NoError(t, err)
		// this documents the behavior - go-git reports absolute symlinks as modified
		assert.True(t, dirty, "go-git reports absolute symlinks as modified (expected quirk)")
	})

	t.Run("gitignored dangling symlink should not make repo dirty", func(t *testing.T) {
		// reproduces issue #28: browser state files like Chrome's SingletonSocket
		// are gitignored but go-git reports them as modified when they dangle
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		// create gitignore for browser-state directory (mimics real .gitignore)
		gitignorePath := filepath.Join(dir, ".gitignore")
		err = os.WriteFile(gitignorePath, []byte("browser-state/\n"), 0o600)
		require.NoError(t, err)

		// commit gitignore
		err = r.Add(".gitignore")
		require.NoError(t, err)
		err = r.Commit("add gitignore")
		require.NoError(t, err)

		// create gitignored directory with a symlink to external temp file
		browserDir := filepath.Join(dir, "browser-state")
		err = os.MkdirAll(browserDir, 0o750)
		require.NoError(t, err)

		// create temp file outside repo (like Chrome's socket in /tmp)
		tmpFile, err := os.CreateTemp("", "singleton-*")
		require.NoError(t, err)
		tmpPath := tmpFile.Name()
		tmpFile.Close()

		// create symlink in gitignored directory pointing to temp file
		socketPath := filepath.Join(browserDir, "SingletonSocket")
		err = os.Symlink(tmpPath, socketPath)
		require.NoError(t, err)

		// delete the temp file - symlink now dangles (simulates Chrome exiting)
		err = os.Remove(tmpPath)
		require.NoError(t, err)

		// this dangling symlink is in a gitignored directory
		// native git shows clean, go-git might report it as modified
		dirty, err := r.IsDirty()
		require.NoError(t, err)
		assert.False(t, dirty, "gitignored dangling symlink should not make repo dirty")
	})
}

func TestRepo_IsIgnored(t *testing.T) {
	t.Run("returns false for non-ignored file", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		ignored, err := r.IsIgnored("README.md")
		require.NoError(t, err)
		assert.False(t, ignored)
	})

	t.Run("returns true for ignored pattern", func(t *testing.T) {
		dir := setupTestRepo(t)

		// add gitignore
		gitignore := filepath.Join(dir, ".gitignore")
		err := os.WriteFile(gitignore, []byte("progress-*.txt\n"), 0o600)
		require.NoError(t, err)

		r, err := openRepo(dir)
		require.NoError(t, err)

		ignored, err := r.IsIgnored("progress-test.txt")
		require.NoError(t, err)
		assert.True(t, ignored)
	})

	t.Run("returns false for no gitignore", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		// check arbitrary file that doesn't exist
		ignored, err := r.IsIgnored("somefile.txt")
		require.NoError(t, err)
		assert.False(t, ignored)
	})

	t.Run("uses XDG_CONFIG_HOME for global patterns", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		// isolate from real home directory to ensure LoadGlobalPatterns returns empty
		// and XDG fallback is triggered
		fakeHome := t.TempDir()
		t.Setenv("HOME", fakeHome)
		t.Setenv("XDG_CONFIG_HOME", fakeHome)

		// set up XDG ignore file
		require.NoError(t, os.MkdirAll(filepath.Join(fakeHome, "git"), 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(fakeHome, "git", "ignore"), []byte("*.xdgignored\n"), 0o600))

		ignored, err := r.IsIgnored("test.xdgignored")
		require.NoError(t, err)
		assert.True(t, ignored, "file matching XDG global gitignore pattern should be ignored")
	})

	t.Run("local gitignore overrides global", func(t *testing.T) {
		dir := setupTestRepo(t)

		// isolate from real home directory
		fakeHome := t.TempDir()
		t.Setenv("HOME", fakeHome)
		t.Setenv("XDG_CONFIG_HOME", fakeHome)

		// set up global ignore for *.log files
		require.NoError(t, os.MkdirAll(filepath.Join(fakeHome, "git"), 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(fakeHome, "git", "ignore"), []byte("*.log\n"), 0o600))

		// create local .gitignore that un-ignores debug.log
		gitignorePath := filepath.Join(dir, ".gitignore")
		err := os.WriteFile(gitignorePath, []byte("!debug.log\n"), 0o600)
		require.NoError(t, err)

		r, err := openRepo(dir)
		require.NoError(t, err)

		// debug.log should NOT be ignored (local un-ignore overrides global ignore)
		ignored, err := r.IsIgnored("debug.log")
		require.NoError(t, err)
		assert.False(t, ignored, "local !debug.log should override global *.log")

		// other.log should still be ignored (only debug.log is un-ignored)
		ignored, err = r.IsIgnored("other.log")
		require.NoError(t, err)
		assert.True(t, ignored, "other.log should still be ignored by global pattern")
	})
}

func TestRepo_HasChangesOtherThan(t *testing.T) {
	t.Run("returns false when no changes", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		hasOther, err := r.HasChangesOtherThan(filepath.Join(dir, "nonexistent.md"))
		require.NoError(t, err)
		assert.False(t, hasOther)
	})

	t.Run("returns false when only target file is untracked", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		planFile := filepath.Join(dir, "docs", "plans", "feature.md")
		require.NoError(t, os.MkdirAll(filepath.Dir(planFile), 0o750))
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

		hasOther, err := r.HasChangesOtherThan(planFile)
		require.NoError(t, err)
		assert.False(t, hasOther)
	})

	t.Run("returns true when other file is untracked", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		planFile := filepath.Join(dir, "docs", "plans", "feature.md")
		require.NoError(t, os.MkdirAll(filepath.Dir(planFile), 0o750))
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "other.txt"), []byte("other"), 0o600))

		hasOther, err := r.HasChangesOtherThan(planFile)
		require.NoError(t, err)
		assert.True(t, hasOther)
	})

	t.Run("returns true when tracked file is modified", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		planFile := filepath.Join(dir, "docs", "plans", "feature.md")
		require.NoError(t, os.MkdirAll(filepath.Dir(planFile), 0o750))
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

		// modify tracked file
		require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Modified"), 0o600))

		hasOther, err := r.HasChangesOtherThan(planFile)
		require.NoError(t, err)
		assert.True(t, hasOther)
	})

	t.Run("returns true when only other file changes no plan", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		// only other file has changes, plan doesn't exist
		require.NoError(t, os.WriteFile(filepath.Join(dir, "other.txt"), []byte("other"), 0o600))

		hasOther, err := r.HasChangesOtherThan(filepath.Join(dir, "nonexistent.md"))
		require.NoError(t, err)
		assert.True(t, hasOther)
	})

	t.Run("returns false when only gitignored file exists", func(t *testing.T) {
		// reproduces issue: go-git reports gitignored files as untracked changes
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		// create .gitignore with pattern for progress files
		gitignorePath := filepath.Join(dir, ".gitignore")
		err = os.WriteFile(gitignorePath, []byte("progress*.txt\n"), 0o600)
		require.NoError(t, err)

		// commit gitignore so it takes effect
		err = r.Add(".gitignore")
		require.NoError(t, err)
		err = r.Commit("add gitignore")
		require.NoError(t, err)

		// create the plan file (the one we're checking "other than")
		planFile := filepath.Join(dir, "docs", "plans", "feature.md")
		require.NoError(t, os.MkdirAll(filepath.Dir(planFile), 0o750))
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

		// create a gitignored progress file (like ralphex creates)
		progressFile := filepath.Join(dir, "progress-feature.txt")
		err = os.WriteFile(progressFile, []byte("progress content"), 0o600)
		require.NoError(t, err)

		// the gitignored file should NOT count as a change
		hasOther, err := r.HasChangesOtherThan(planFile)
		require.NoError(t, err)
		assert.False(t, hasOther, "gitignored files should not count as changes")
	})

	t.Run("returns true when gitignored and non-gitignored files exist", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		// create .gitignore with pattern for progress files
		gitignorePath := filepath.Join(dir, ".gitignore")
		err = os.WriteFile(gitignorePath, []byte("progress*.txt\n"), 0o600)
		require.NoError(t, err)

		// commit gitignore
		err = r.Add(".gitignore")
		require.NoError(t, err)
		err = r.Commit("add gitignore")
		require.NoError(t, err)

		// create the plan file
		planFile := filepath.Join(dir, "docs", "plans", "feature.md")
		require.NoError(t, os.MkdirAll(filepath.Dir(planFile), 0o750))
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

		// create a gitignored progress file
		progressFile := filepath.Join(dir, "progress-feature.txt")
		err = os.WriteFile(progressFile, []byte("progress content"), 0o600)
		require.NoError(t, err)

		// also create a non-gitignored file
		otherFile := filepath.Join(dir, "other.txt")
		err = os.WriteFile(otherFile, []byte("other content"), 0o600)
		require.NoError(t, err)

		// should return true because of the non-gitignored file
		hasOther, err := r.HasChangesOtherThan(planFile)
		require.NoError(t, err)
		assert.True(t, hasOther, "non-gitignored files should count as changes")
	})
}

func TestRepo_FileHasChanges(t *testing.T) {
	t.Run("returns false for committed file", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		hasChanges, err := r.FileHasChanges(filepath.Join(dir, "README.md"))
		require.NoError(t, err)
		assert.False(t, hasChanges)
	})

	t.Run("returns true for untracked file", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		planFile := filepath.Join(dir, "docs", "plans", "feature.md")
		require.NoError(t, os.MkdirAll(filepath.Dir(planFile), 0o750))
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

		hasChanges, err := r.FileHasChanges(planFile)
		require.NoError(t, err)
		assert.True(t, hasChanges)
	})

	t.Run("returns true for modified file", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		// modify tracked file
		readme := filepath.Join(dir, "README.md")
		require.NoError(t, os.WriteFile(readme, []byte("# Modified"), 0o600))

		hasChanges, err := r.FileHasChanges(readme)
		require.NoError(t, err)
		assert.True(t, hasChanges)
	})

	t.Run("returns true for staged file", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		planFile := filepath.Join(dir, "docs", "plans", "feature.md")
		require.NoError(t, os.MkdirAll(filepath.Dir(planFile), 0o750))
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))
		require.NoError(t, r.Add(filepath.Join("docs", "plans", "feature.md")))

		hasChanges, err := r.FileHasChanges(planFile)
		require.NoError(t, err)
		assert.True(t, hasChanges)
	})

	t.Run("returns false for nonexistent file", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		hasChanges, err := r.FileHasChanges(filepath.Join(dir, "nonexistent.md"))
		require.NoError(t, err)
		assert.False(t, hasChanges)
	})
}

func TestRepo_HasCommits(t *testing.T) {
	t.Run("returns true for repo with commits", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		hasCommits, err := r.HasCommits()
		require.NoError(t, err)
		assert.True(t, hasCommits)
	})

	t.Run("returns false for empty repo", func(t *testing.T) {
		dir := t.TempDir()
		_, err := git.PlainInit(dir, false)
		require.NoError(t, err)

		r, err := openRepo(dir)
		require.NoError(t, err)

		hasCommits, err := r.HasCommits()
		require.NoError(t, err)
		assert.False(t, hasCommits)
	})
}

func TestRepo_CreateInitialCommit(t *testing.T) {
	t.Run("creates commit with files", func(t *testing.T) {
		dir := t.TempDir()
		_, err := git.PlainInit(dir, false)
		require.NoError(t, err)

		// create some files
		err = os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test\n"), 0o600)
		require.NoError(t, err)
		err = os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o600)
		require.NoError(t, err)

		r, err := openRepo(dir)
		require.NoError(t, err)

		// verify no commits before
		hasCommits, err := r.HasCommits()
		require.NoError(t, err)
		assert.False(t, hasCommits)

		// create initial commit
		err = r.CreateInitialCommit("initial commit")
		require.NoError(t, err)

		// verify commit exists
		hasCommits, err = r.HasCommits()
		require.NoError(t, err)
		assert.True(t, hasCommits)

		// verify commit message
		head, err := r.gitRepo.Head()
		require.NoError(t, err)
		commit, err := r.gitRepo.CommitObject(head.Hash())
		require.NoError(t, err)
		assert.Equal(t, "initial commit", commit.Message)
	})

	t.Run("fails with no files", func(t *testing.T) {
		dir := t.TempDir()
		_, err := git.PlainInit(dir, false)
		require.NoError(t, err)

		r, err := openRepo(dir)
		require.NoError(t, err)

		err = r.CreateInitialCommit("initial commit")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no files to commit")
	})

	t.Run("has valid author", func(t *testing.T) {
		dir := t.TempDir()
		_, err := git.PlainInit(dir, false)
		require.NoError(t, err)

		err = os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test\n"), 0o600)
		require.NoError(t, err)

		r, err := openRepo(dir)
		require.NoError(t, err)

		err = r.CreateInitialCommit("initial commit")
		require.NoError(t, err)

		head, err := r.gitRepo.Head()
		require.NoError(t, err)
		commit, err := r.gitRepo.CommitObject(head.Hash())
		require.NoError(t, err)
		assert.NotEmpty(t, commit.Author.Name)
		assert.NotEmpty(t, commit.Author.Email)
	})

	t.Run("respects gitignore", func(t *testing.T) {
		dir := t.TempDir()
		_, err := git.PlainInit(dir, false)
		require.NoError(t, err)

		// create gitignore first
		err = os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.log\n"), 0o600)
		require.NoError(t, err)

		// create tracked file
		err = os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test\n"), 0o600)
		require.NoError(t, err)

		// create ignored file
		err = os.WriteFile(filepath.Join(dir, "debug.log"), []byte("log content\n"), 0o600)
		require.NoError(t, err)

		r, err := openRepo(dir)
		require.NoError(t, err)

		err = r.CreateInitialCommit("initial commit")
		require.NoError(t, err)

		// verify commit exists
		head, err := r.gitRepo.Head()
		require.NoError(t, err)
		commit, err := r.gitRepo.CommitObject(head.Hash())
		require.NoError(t, err)

		// get tree to check committed files
		tree, err := commit.Tree()
		require.NoError(t, err)

		// collect committed file names
		files := make([]string, 0, len(tree.Entries))
		for _, entry := range tree.Entries {
			files = append(files, entry.Name)
		}

		assert.Contains(t, files, "README.md")
		assert.Contains(t, files, ".gitignore")
		assert.NotContains(t, files, "debug.log", "gitignored files should not be committed")
	})

	t.Run("respects global gitignore", func(t *testing.T) {
		dir := t.TempDir()
		_, err := git.PlainInit(dir, false)
		require.NoError(t, err)

		// isolate from real home directory
		fakeHome := t.TempDir()
		t.Setenv("HOME", fakeHome)
		t.Setenv("XDG_CONFIG_HOME", fakeHome)

		// set up global ignore for *.log files
		require.NoError(t, os.MkdirAll(filepath.Join(fakeHome, "git"), 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(fakeHome, "git", "ignore"), []byte("*.log\n"), 0o600))

		// create tracked file
		err = os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test\n"), 0o600)
		require.NoError(t, err)

		// create file that should be ignored by global gitignore
		err = os.WriteFile(filepath.Join(dir, "debug.log"), []byte("log content\n"), 0o600)
		require.NoError(t, err)

		r, err := openRepo(dir)
		require.NoError(t, err)

		err = r.CreateInitialCommit("initial commit")
		require.NoError(t, err)

		// verify commit exists
		head, err := r.gitRepo.Head()
		require.NoError(t, err)
		commit, err := r.gitRepo.CommitObject(head.Hash())
		require.NoError(t, err)

		// get tree to check committed files
		tree, err := commit.Tree()
		require.NoError(t, err)

		// collect committed file names
		files := make([]string, 0, len(tree.Entries))
		for _, entry := range tree.Entries {
			files = append(files, entry.Name)
		}

		assert.Contains(t, files, "README.md")
		assert.NotContains(t, files, "debug.log", "globally gitignored files should not be committed")
	})
}

func TestRepo_IsMainBranch(t *testing.T) {
	t.Run("returns true for master branch", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		isMain, err := r.IsMainBranch()
		require.NoError(t, err)
		assert.True(t, isMain)
	})

	t.Run("returns true for main branch", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		// rename master to main
		err = r.CreateBranch("main")
		require.NoError(t, err)

		isMain, err := r.IsMainBranch()
		require.NoError(t, err)
		assert.True(t, isMain)
	})

	t.Run("returns false for feature branch", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		err = r.CreateBranch("feature-test")
		require.NoError(t, err)

		isMain, err := r.IsMainBranch()
		require.NoError(t, err)
		assert.False(t, isMain)
	})

	t.Run("returns false for detached HEAD", func(t *testing.T) {
		dir := setupTestRepo(t)
		r, err := openRepo(dir)
		require.NoError(t, err)

		// get current HEAD hash
		head, err := r.gitRepo.Head()
		require.NoError(t, err)

		// checkout the commit hash directly (detached HEAD)
		wt, err := r.gitRepo.Worktree()
		require.NoError(t, err)
		err = wt.Checkout(&git.CheckoutOptions{Hash: head.Hash()})
		require.NoError(t, err)

		isMain, err := r.IsMainBranch()
		require.NoError(t, err)
		assert.False(t, isMain)
	})
}

// setupTestRepo creates a test git repository with an initial commit.
func setupTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// init repo
	repo, err := git.PlainInit(dir, false)
	require.NoError(t, err)

	// create a file
	readme := filepath.Join(dir, "README.md")
	err = os.WriteFile(readme, []byte("# Test\n"), 0o600)
	require.NoError(t, err)

	// stage and commit
	wt, err := repo.Worktree()
	require.NoError(t, err)

	_, err = wt.Add("README.md")
	require.NoError(t, err)

	_, err = wt.Commit("initial commit", &git.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "test@test.com"},
	})
	require.NoError(t, err)

	return dir
}
