package git

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpen(t *testing.T) {
	t.Run("opens valid repo", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)
		assert.NotNil(t, repo)
	})

	t.Run("fails on non-repo", func(t *testing.T) {
		dir := t.TempDir()
		_, err := Open(dir)
		assert.Error(t, err)
	})
}

func TestRepo_toRelative(t *testing.T) {
	t.Run("returns repo-relative path unchanged", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		rel, err := repo.toRelative("docs/plans/test.md")
		require.NoError(t, err)
		assert.Equal(t, "docs/plans/test.md", rel)
	})

	t.Run("converts absolute path to relative", func(t *testing.T) {
		dir := setupTestRepo(t)
		// resolve symlinks for consistent paths (macOS /var -> /private/var)
		dir, err := filepath.EvalSymlinks(dir)
		require.NoError(t, err)

		repo, err := Open(dir)
		require.NoError(t, err)

		absPath := filepath.Join(dir, "docs", "plans", "test.md")
		rel, err := repo.toRelative(absPath)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("docs", "plans", "test.md"), rel)
	})

	t.Run("rejects .. path", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		_, err = repo.toRelative("../outside.txt")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "escapes repository root")
	})

	t.Run("rejects absolute path outside repo", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		_, err = repo.toRelative("/tmp/outside/file.txt")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "outside repository")
	})

	t.Run("rejects ./../ path", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		_, err = repo.toRelative("./../outside.txt")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "escapes repository root")
	})
}

func TestRepo_CurrentBranch(t *testing.T) {
	t.Run("returns master for new repo", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		branch, err := repo.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "master", branch)
	})

	t.Run("returns feature branch name", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		err = repo.CreateBranch("feature-test")
		require.NoError(t, err)

		branch, err := repo.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "feature-test", branch)
	})

	t.Run("returns empty string for detached HEAD", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		// get current HEAD hash
		head, err := repo.repo.Head()
		require.NoError(t, err)

		// checkout the commit hash directly (detached HEAD)
		wt, err := repo.repo.Worktree()
		require.NoError(t, err)
		err = wt.Checkout(&git.CheckoutOptions{Hash: head.Hash()})
		require.NoError(t, err)

		// should return empty string for detached HEAD
		branch, err := repo.CurrentBranch()
		require.NoError(t, err)
		assert.Empty(t, branch)
	})
}

func TestRepo_CreateBranch(t *testing.T) {
	t.Run("creates and switches to branch", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		err = repo.CreateBranch("new-feature")
		require.NoError(t, err)

		branch, err := repo.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "new-feature", branch)
	})

	t.Run("fails on invalid branch name", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		err = repo.CreateBranch("invalid..name")
		assert.Error(t, err)
	})

	t.Run("fails when branch already exists", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		// create branch first
		err = repo.CreateBranch("existing")
		require.NoError(t, err)

		// switch back to master
		err = repo.CheckoutBranch("master")
		require.NoError(t, err)

		// try to create same branch again
		err = repo.CreateBranch("existing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "already exists")
	})

	t.Run("preserves untracked files", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		// create an untracked file while on master
		untrackedPath := filepath.Join(dir, "untracked.txt")
		err = os.WriteFile(untrackedPath, []byte("untracked content"), 0o600)
		require.NoError(t, err)

		// verify file exists before creating branch
		_, err = os.Stat(untrackedPath)
		require.NoError(t, err, "untracked file should exist before branch creation")

		// create and switch to new branch
		err = repo.CreateBranch("feature")
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
		repo, err := Open(dir)
		require.NoError(t, err)

		// create a new file
		testFile := filepath.Join(dir, "newfile.txt")
		err = os.WriteFile(testFile, []byte("test content"), 0o600)
		require.NoError(t, err)

		err = repo.Add("newfile.txt")
		require.NoError(t, err)

		// verify file is staged
		wt, err := repo.repo.Worktree()
		require.NoError(t, err)
		status, err := wt.Status()
		require.NoError(t, err)
		assert.Equal(t, git.Added, status["newfile.txt"].Staging)
	})

	t.Run("fails on non-existent file", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		err = repo.Add("nonexistent.txt")
		assert.Error(t, err)
	})
}

func TestRepo_Commit(t *testing.T) {
	t.Run("creates commit", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		// create and stage a file
		testFile := filepath.Join(dir, "commit-test.txt")
		err = os.WriteFile(testFile, []byte("test"), 0o600)
		require.NoError(t, err)

		err = repo.Add("commit-test.txt")
		require.NoError(t, err)

		err = repo.Commit("test commit message")
		require.NoError(t, err)

		// verify commit was created
		head, err := repo.repo.Head()
		require.NoError(t, err)
		commit, err := repo.repo.CommitObject(head.Hash())
		require.NoError(t, err)
		assert.Equal(t, "test commit message", commit.Message)
	})

	t.Run("commit has valid author", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		// create and stage a file
		testFile := filepath.Join(dir, "author-test.txt")
		err = os.WriteFile(testFile, []byte("test"), 0o600)
		require.NoError(t, err)

		err = repo.Add("author-test.txt")
		require.NoError(t, err)

		err = repo.Commit("test author")
		require.NoError(t, err)

		// verify commit has author info
		head, err := repo.repo.Head()
		require.NoError(t, err)
		commit, err := repo.repo.CommitObject(head.Hash())
		require.NoError(t, err)
		assert.NotEmpty(t, commit.Author.Name, "author name should not be empty")
		assert.NotEmpty(t, commit.Author.Email, "author email should not be empty")
		assert.False(t, commit.Author.When.IsZero(), "author time should be set")
	})

	t.Run("fails with no staged changes", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		// try to commit without staging anything
		err = repo.Commit("empty commit")
		assert.Error(t, err)
	})
}

func TestRepo_getAuthor(t *testing.T) {
	t.Run("returns valid signature", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		author := repo.getAuthor()
		require.NotNil(t, author)
		assert.NotEmpty(t, author.Name, "author name should not be empty")
		assert.NotEmpty(t, author.Email, "author email should not be empty")
		assert.False(t, author.When.IsZero(), "author time should be set")
	})

	t.Run("fallback has expected values", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		author := repo.getAuthor()
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
		repo, err := Open(dir)
		require.NoError(t, err)

		// create destination directory
		err = os.MkdirAll(filepath.Join(dir, "subdir"), 0o750)
		require.NoError(t, err)

		// move the initial file
		err = repo.MoveFile("README.md", filepath.Join("subdir", "README.md"))
		require.NoError(t, err)

		// verify old file removed
		_, err = os.Stat(filepath.Join(dir, "README.md"))
		assert.True(t, os.IsNotExist(err))

		// verify new file exists
		_, err = os.Stat(filepath.Join(dir, "subdir", "README.md"))
		require.NoError(t, err)

		// verify both changes are staged
		wt, err := repo.repo.Worktree()
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

		repo, err := Open(dir)
		require.NoError(t, err)

		// create destination directory
		subdir := filepath.Join(dir, "subdir")
		err = os.MkdirAll(subdir, 0o750)
		require.NoError(t, err)

		// move using absolute paths
		srcAbs := filepath.Join(dir, "README.md")
		dstAbs := filepath.Join(subdir, "README.md")
		err = repo.MoveFile(srcAbs, dstAbs)
		require.NoError(t, err)

		// verify move worked
		_, err = os.Stat(srcAbs)
		assert.True(t, os.IsNotExist(err))
		_, err = os.Stat(dstAbs)
		require.NoError(t, err)
	})

	t.Run("fails on non-existent source file", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		err = repo.MoveFile("nonexistent.txt", "dest.txt")
		assert.Error(t, err)
	})

	t.Run("fails on path outside repo", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		err = repo.MoveFile("/tmp/outside.txt", "dest.txt")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "outside repository")
	})
}

func TestRepo_BranchExists(t *testing.T) {
	t.Run("returns true for existing branch", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		// master exists by default
		assert.True(t, repo.BranchExists("master"))
	})

	t.Run("returns false for non-existent branch", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		assert.False(t, repo.BranchExists("nonexistent"))
	})

	t.Run("returns true for created branch", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		err = repo.CreateBranch("new-branch")
		require.NoError(t, err)

		assert.True(t, repo.BranchExists("new-branch"))
	})
}

func TestRepo_CheckoutBranch(t *testing.T) {
	t.Run("switches to existing branch", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		// create a branch first
		err = repo.CreateBranch("feature")
		require.NoError(t, err)

		// switch back to master
		err = repo.CheckoutBranch("master")
		require.NoError(t, err)

		branch, err := repo.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "master", branch)
	})

	t.Run("fails on non-existent branch", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		err = repo.CheckoutBranch("nonexistent")
		assert.Error(t, err)
	})

	t.Run("preserves untracked files", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		// create a branch
		err = repo.CreateBranch("feature")
		require.NoError(t, err)

		// switch back to master
		err = repo.CheckoutBranch("master")
		require.NoError(t, err)

		// create an untracked file while on master
		untrackedPath := filepath.Join(dir, "untracked.txt")
		err = os.WriteFile(untrackedPath, []byte("untracked content"), 0o600)
		require.NoError(t, err)

		// verify file exists before checkout
		_, err = os.Stat(untrackedPath)
		require.NoError(t, err, "untracked file should exist before checkout")

		// switch to feature branch
		err = repo.CheckoutBranch("feature")
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
		repo, err := Open(dir)
		require.NoError(t, err)

		dirty, err := repo.IsDirty()
		require.NoError(t, err)
		assert.False(t, dirty)
	})

	t.Run("staged file returns true", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		// create and stage a new file
		testFile := filepath.Join(dir, "staged.txt")
		err = os.WriteFile(testFile, []byte("staged content"), 0o600)
		require.NoError(t, err)

		err = repo.Add("staged.txt")
		require.NoError(t, err)

		dirty, err := repo.IsDirty()
		require.NoError(t, err)
		assert.True(t, dirty)
	})

	t.Run("modified tracked file returns true", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		// modify the existing README.md (which is tracked)
		readmePath := filepath.Join(dir, "README.md")
		err = os.WriteFile(readmePath, []byte("# Modified\n"), 0o600)
		require.NoError(t, err)

		dirty, err := repo.IsDirty()
		require.NoError(t, err)
		assert.True(t, dirty)
	})

	t.Run("deleted tracked file returns true", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		// delete the existing README.md (which is tracked)
		readmePath := filepath.Join(dir, "README.md")
		err = os.Remove(readmePath)
		require.NoError(t, err)

		dirty, err := repo.IsDirty()
		require.NoError(t, err)
		assert.True(t, dirty)
	})

	t.Run("untracked file only returns false", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		// create a new file without staging it
		testFile := filepath.Join(dir, "untracked.txt")
		err = os.WriteFile(testFile, []byte("untracked content"), 0o600)
		require.NoError(t, err)

		dirty, err := repo.IsDirty()
		require.NoError(t, err)
		assert.False(t, dirty)
	})
}

func TestRepo_IsIgnored(t *testing.T) {
	t.Run("returns false for non-ignored file", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		ignored, err := repo.IsIgnored("README.md")
		require.NoError(t, err)
		assert.False(t, ignored)
	})

	t.Run("returns true for ignored pattern", func(t *testing.T) {
		dir := setupTestRepo(t)

		// add gitignore
		gitignore := filepath.Join(dir, ".gitignore")
		err := os.WriteFile(gitignore, []byte("progress-*.txt\n"), 0o600)
		require.NoError(t, err)

		repo, err := Open(dir)
		require.NoError(t, err)

		ignored, err := repo.IsIgnored("progress-test.txt")
		require.NoError(t, err)
		assert.True(t, ignored)
	})

	t.Run("returns false for no gitignore", func(t *testing.T) {
		dir := setupTestRepo(t)
		repo, err := Open(dir)
		require.NoError(t, err)

		// check arbitrary file that doesn't exist
		ignored, err := repo.IsIgnored("somefile.txt")
		require.NoError(t, err)
		assert.False(t, ignored)
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
