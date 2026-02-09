package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupExternalTestRepo creates a temp git repo using the git CLI for external backend tests.
func setupExternalTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// init repo and rename default branch to "master" explicitly;
	// avoids dependence on git config init.defaultBranch without requiring git >= 2.28 (-b flag)
	runGit(t, dir, "init")
	runGit(t, dir, "checkout", "-B", "master")
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "config", "user.name", "test")

	// create a file and commit
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test\n"), 0o600))
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-m", "initial commit")

	return dir
}

// runGit runs a git command in the given directory and fails the test on error.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v failed: %s", args, string(out))
	return string(out)
}

func TestNewExternalBackend(t *testing.T) {
	t.Run("opens valid repo", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)
		assert.NotNil(t, eb)
	})

	t.Run("fails on non-repo", func(t *testing.T) {
		dir := t.TempDir()
		_, err := newExternalBackend(dir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "open git repository")
	})
}

func TestExternalBackend_Root(t *testing.T) {
	dir := setupExternalTestRepo(t)
	eb, err := newExternalBackend(dir)
	require.NoError(t, err)
	assert.NotEmpty(t, eb.Root())
}

func TestExternalBackend_headHash(t *testing.T) {
	t.Run("returns valid 40-char hex string", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		hash, err := eb.headHash()
		require.NoError(t, err)
		assert.Len(t, hash, 40)
		assert.Regexp(t, `^[0-9a-f]{40}$`, hash)
	})

	t.Run("changes after new commit", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		hash1, err := eb.headHash()
		require.NoError(t, err)

		require.NoError(t, os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new"), 0o600))
		runGit(t, dir, "add", "new.txt")
		runGit(t, dir, "commit", "-m", "second commit")

		hash2, err := eb.headHash()
		require.NoError(t, err)
		assert.NotEqual(t, hash1, hash2)
	})

	t.Run("fails on empty repo", func(t *testing.T) {
		dir := t.TempDir()
		runGit(t, dir, "init")

		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		_, err = eb.headHash()
		require.Error(t, err)
	})
}

func TestExternalBackend_HasCommits(t *testing.T) {
	t.Run("returns true for repo with commits", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		has, err := eb.HasCommits()
		require.NoError(t, err)
		assert.True(t, has)
	})

	t.Run("returns false for empty repo", func(t *testing.T) {
		dir := t.TempDir()
		runGit(t, dir, "init")

		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		has, err := eb.HasCommits()
		require.NoError(t, err)
		assert.False(t, has)
	})

	t.Run("returns error for non-repo exit-128", func(t *testing.T) {
		// construct externalBackend pointing to a non-repo directory;
		// git rev-parse HEAD exits 128 with "not a git repository" which must propagate as error.
		dir := t.TempDir()
		eb := &externalBackend{path: dir}

		has, err := eb.HasCommits()
		require.Error(t, err, "non-repo exit-128 should return error, not silently report no commits")
		assert.False(t, has)
		assert.Contains(t, err.Error(), "check HEAD")
	})
}

func TestExternalBackend_CurrentBranch(t *testing.T) {
	t.Run("returns default branch for new repo", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		branch, err := eb.CurrentBranch()
		require.NoError(t, err)
		assert.NotEmpty(t, branch)
	})

	t.Run("returns feature branch name", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		require.NoError(t, eb.CreateBranch("feature-test"))
		branch, err := eb.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "feature-test", branch)
	})

	t.Run("returns empty string for detached HEAD", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		hash, err := eb.headHash()
		require.NoError(t, err)
		runGit(t, dir, "checkout", hash)

		branch, err := eb.CurrentBranch()
		require.NoError(t, err)
		assert.Empty(t, branch)
	})

	t.Run("returns error for non-repo exit-128", func(t *testing.T) {
		// construct externalBackend pointing to a non-repo directory;
		// git symbolic-ref exits 128 with "not a git repository" which must propagate as error.
		dir := t.TempDir()
		eb := &externalBackend{path: dir}

		branch, err := eb.CurrentBranch()
		require.Error(t, err, "non-repo exit-128 should return error, not silently report detached HEAD")
		assert.Empty(t, branch)
		assert.Contains(t, err.Error(), "get current branch")
	})
}

func TestExternalBackend_IsMainBranch(t *testing.T) {
	t.Run("returns true on main or master", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		// default branch from git init is usually master or main
		isMain, err := eb.IsMainBranch()
		require.NoError(t, err)
		assert.True(t, isMain)
	})

	t.Run("returns false for feature branch", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		require.NoError(t, eb.CreateBranch("feature-x"))
		isMain, err := eb.IsMainBranch()
		require.NoError(t, err)
		assert.False(t, isMain)
	})

	t.Run("returns false for detached HEAD", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		hash, err := eb.headHash()
		require.NoError(t, err)
		runGit(t, dir, "checkout", hash)

		isMain, err := eb.IsMainBranch()
		require.NoError(t, err)
		assert.False(t, isMain)
	})
}

func TestExternalBackend_GetDefaultBranch(t *testing.T) {
	t.Run("returns existing default branch", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		branch := eb.GetDefaultBranch()
		// default from git init is usually master or main
		assert.Contains(t, []string{"main", "master"}, branch)
	})

	t.Run("returns main when main branch exists", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		require.NoError(t, eb.CreateBranch("main"))
		branch := eb.GetDefaultBranch()
		assert.Equal(t, "main", branch)
	})

	t.Run("falls back to master", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		// create a non-standard branch and delete the default one
		runGit(t, dir, "checkout", "-b", "unusual-name")
		runGit(t, dir, "branch", "-D", "master")

		branch := eb.GetDefaultBranch()
		assert.Equal(t, "master", branch) // fallback
	})
}

func TestExternalBackend_BranchExists(t *testing.T) {
	t.Run("returns true for existing branch", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		// get default branch name (could be master or main depending on git config)
		branch, err := eb.CurrentBranch()
		require.NoError(t, err)
		assert.True(t, eb.BranchExists(branch))
	})

	t.Run("returns false for non-existent branch", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		assert.False(t, eb.BranchExists("nonexistent"))
	})

	t.Run("returns true for created branch", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		require.NoError(t, eb.CreateBranch("new-branch"))
		assert.True(t, eb.BranchExists("new-branch"))
	})
}

func TestExternalBackend_CreateBranch(t *testing.T) {
	t.Run("creates and switches to branch", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		err = eb.CreateBranch("new-feature")
		require.NoError(t, err)

		branch, err := eb.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "new-feature", branch)
	})

	t.Run("fails when branch already exists", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		require.NoError(t, eb.CreateBranch("existing"))
		require.NoError(t, eb.CheckoutBranch("master"))

		err = eb.CreateBranch("existing")
		require.Error(t, err)
	})
}

func TestExternalBackend_CheckoutBranch(t *testing.T) {
	t.Run("switches to existing branch", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		require.NoError(t, eb.CreateBranch("feature"))
		require.NoError(t, eb.CheckoutBranch("master"))

		branch, err := eb.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, "master", branch)
	})

	t.Run("fails on non-existent branch", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		err = eb.CheckoutBranch("nonexistent")
		assert.Error(t, err)
	})
}

func TestExternalBackend_IsDirty(t *testing.T) {
	t.Run("clean worktree returns false", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		dirty, err := eb.IsDirty()
		require.NoError(t, err)
		assert.False(t, dirty)
	})

	t.Run("staged file returns true", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		require.NoError(t, os.WriteFile(filepath.Join(dir, "staged.txt"), []byte("staged"), 0o600))
		runGit(t, dir, "add", "staged.txt")

		dirty, err := eb.IsDirty()
		require.NoError(t, err)
		assert.True(t, dirty)
	})

	t.Run("modified tracked file returns true", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Modified\n"), 0o600))

		dirty, err := eb.IsDirty()
		require.NoError(t, err)
		assert.True(t, dirty)
	})

	t.Run("deleted tracked file returns true", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		require.NoError(t, os.Remove(filepath.Join(dir, "README.md")))

		dirty, err := eb.IsDirty()
		require.NoError(t, err)
		assert.True(t, dirty)
	})

	t.Run("untracked file only returns false", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		require.NoError(t, os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("untracked"), 0o600))

		dirty, err := eb.IsDirty()
		require.NoError(t, err)
		assert.False(t, dirty)
	})

	t.Run("gitignored file should not make repo dirty", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("ignored.txt\n"), 0o600))
		runGit(t, dir, "add", ".gitignore")
		runGit(t, dir, "commit", "-m", "add gitignore")

		require.NoError(t, os.WriteFile(filepath.Join(dir, "ignored.txt"), []byte("should be ignored"), 0o600))

		dirty, err := eb.IsDirty()
		require.NoError(t, err)
		assert.False(t, dirty)
	})
}

func TestExternalBackend_FileHasChanges(t *testing.T) {
	t.Run("returns false for committed file", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		has, err := eb.FileHasChanges("README.md")
		require.NoError(t, err)
		assert.False(t, has)
	})

	t.Run("returns true for untracked file", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join(plansDir, "feature.md")
		require.NoError(t, os.WriteFile(planFile, []byte("# Plan"), 0o600))

		has, err := eb.FileHasChanges(filepath.Join("docs", "plans", "feature.md"))
		require.NoError(t, err)
		assert.True(t, has)
	})

	t.Run("returns true for modified file", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Modified"), 0o600))

		has, err := eb.FileHasChanges("README.md")
		require.NoError(t, err)
		assert.True(t, has)
	})

	t.Run("returns true for staged file", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(plansDir, "feature.md"), []byte("# Plan"), 0o600))
		runGit(t, dir, "add", filepath.Join("docs", "plans", "feature.md"))

		has, err := eb.FileHasChanges(filepath.Join("docs", "plans", "feature.md"))
		require.NoError(t, err)
		assert.True(t, has)
	})

	t.Run("returns false for nonexistent file", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		has, err := eb.FileHasChanges("nonexistent.md")
		require.NoError(t, err)
		assert.False(t, has)
	})
}

func TestExternalBackend_HasChangesOtherThan(t *testing.T) {
	t.Run("returns false when no changes", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		has, err := eb.HasChangesOtherThan("nonexistent.md")
		require.NoError(t, err)
		assert.False(t, has)
	})

	t.Run("returns false when only target file is untracked", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join("docs", "plans", "feature.md")
		require.NoError(t, os.WriteFile(filepath.Join(dir, planFile), []byte("# Plan"), 0o600))

		has, err := eb.HasChangesOtherThan(planFile)
		require.NoError(t, err)
		assert.False(t, has)
	})

	t.Run("returns true when other file is untracked", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join("docs", "plans", "feature.md")
		require.NoError(t, os.WriteFile(filepath.Join(dir, planFile), []byte("# Plan"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "other.txt"), []byte("other"), 0o600))

		has, err := eb.HasChangesOtherThan(planFile)
		require.NoError(t, err)
		assert.True(t, has)
	})

	t.Run("returns true when tracked file is modified", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		plansDir := filepath.Join(dir, "docs", "plans")
		require.NoError(t, os.MkdirAll(plansDir, 0o750))
		planFile := filepath.Join("docs", "plans", "feature.md")
		require.NoError(t, os.WriteFile(filepath.Join(dir, planFile), []byte("# Plan"), 0o600))

		require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Modified"), 0o600))

		has, err := eb.HasChangesOtherThan(planFile)
		require.NoError(t, err)
		assert.True(t, has)
	})
}

func TestExternalBackend_IsIgnored(t *testing.T) {
	t.Run("returns false for non-ignored file", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		ignored, err := eb.IsIgnored("README.md")
		require.NoError(t, err)
		assert.False(t, ignored)
	})

	t.Run("returns true for ignored pattern", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("progress-*.txt\n"), 0o600))

		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		ignored, err := eb.IsIgnored("progress-test.txt")
		require.NoError(t, err)
		assert.True(t, ignored)
	})

	t.Run("returns false for no gitignore", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		ignored, err := eb.IsIgnored("somefile.txt")
		require.NoError(t, err)
		assert.False(t, ignored)
	})
}

func TestExternalBackend_Add(t *testing.T) {
	t.Run("stages new file", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		require.NoError(t, os.WriteFile(filepath.Join(dir, "newfile.txt"), []byte("test content"), 0o600))
		err = eb.Add("newfile.txt")
		require.NoError(t, err)

		// verify file is staged
		out := runGit(t, dir, "status", "--porcelain")
		assert.Contains(t, out, "newfile.txt")
		assert.Contains(t, out, "A ")
	})

	t.Run("stages with absolute path", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		absPath := filepath.Join(dir, "newfile.txt")
		require.NoError(t, os.WriteFile(absPath, []byte("test"), 0o600))
		err = eb.Add(absPath)
		require.NoError(t, err)
	})
}

func TestExternalBackend_MoveFile(t *testing.T) {
	t.Run("moves file and stages changes", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		require.NoError(t, os.MkdirAll(filepath.Join(dir, "subdir"), 0o750))
		err = eb.MoveFile("README.md", filepath.Join("subdir", "README.md"))
		require.NoError(t, err)

		// verify old file removed
		_, err = os.Stat(filepath.Join(dir, "README.md"))
		assert.True(t, os.IsNotExist(err))

		// verify new file exists
		_, err = os.Stat(filepath.Join(dir, "subdir", "README.md"))
		require.NoError(t, err)
	})

	t.Run("fails on non-existent source", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		err = eb.MoveFile("nonexistent.txt", "dest.txt")
		assert.Error(t, err)
	})
}

func TestExternalBackend_Commit(t *testing.T) {
	t.Run("creates commit", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		require.NoError(t, os.WriteFile(filepath.Join(dir, "commit-test.txt"), []byte("test"), 0o600))
		require.NoError(t, eb.Add("commit-test.txt"))
		err = eb.Commit("test commit message")
		require.NoError(t, err)

		// verify commit message
		out := runGit(t, dir, "log", "-1", "--format=%s")
		assert.Contains(t, out, "test commit message")
	})

	t.Run("fails with no staged changes", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		err = eb.Commit("empty commit")
		assert.Error(t, err)
	})
}

func TestExternalBackend_CreateInitialCommit(t *testing.T) {
	t.Run("creates commit with files", func(t *testing.T) {
		dir := t.TempDir()
		runGit(t, dir, "init")
		runGit(t, dir, "config", "user.email", "test@test.com")
		runGit(t, dir, "config", "user.name", "test")

		require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o600))

		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		err = eb.CreateInitialCommit("initial commit")
		require.NoError(t, err)

		has, err := eb.HasCommits()
		require.NoError(t, err)
		assert.True(t, has)
	})

	t.Run("fails with no files", func(t *testing.T) {
		dir := t.TempDir()
		runGit(t, dir, "init")
		runGit(t, dir, "config", "user.email", "test@test.com")
		runGit(t, dir, "config", "user.name", "test")

		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		err = eb.CreateInitialCommit("initial commit")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no files to commit")
	})

	t.Run("respects gitignore", func(t *testing.T) {
		dir := t.TempDir()
		runGit(t, dir, "init")
		runGit(t, dir, "config", "user.email", "test@test.com")
		runGit(t, dir, "config", "user.name", "test")

		require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.log\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "debug.log"), []byte("log content"), 0o600))

		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		err = eb.CreateInitialCommit("initial commit")
		require.NoError(t, err)

		// verify debug.log was not committed
		out := runGit(t, dir, "ls-tree", "-r", "--name-only", "HEAD")
		assert.Contains(t, out, "README.md")
		assert.Contains(t, out, ".gitignore")
		assert.NotContains(t, out, "debug.log")
	})
}

func TestExternalBackend_diffStats(t *testing.T) {
	t.Run("returns zero stats when branches are equal", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		stats, err := eb.diffStats("master")
		require.NoError(t, err)
		assert.Equal(t, DiffStats{}, stats)
	})

	t.Run("returns zero stats for nonexistent branch", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		stats, err := eb.diffStats("nonexistent")
		require.NoError(t, err)
		assert.Equal(t, DiffStats{}, stats)
	})

	t.Run("returns stats for added file", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		require.NoError(t, eb.CreateBranch("feature"))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "new.txt"), []byte("line1\nline2\nline3\n"), 0o600))
		require.NoError(t, eb.Add("new.txt"))
		require.NoError(t, eb.Commit("add new file"))

		stats, err := eb.diffStats("master")
		require.NoError(t, err)
		assert.Equal(t, 1, stats.Files)
		assert.Equal(t, 3, stats.Additions)
		assert.Equal(t, 0, stats.Deletions)
	})

	t.Run("returns stats for modified file", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		require.NoError(t, eb.CreateBranch("feature"))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Modified\nNew line\n"), 0o600))
		require.NoError(t, eb.Add("README.md"))
		require.NoError(t, eb.Commit("modify readme"))

		stats, err := eb.diffStats("master")
		require.NoError(t, err)
		assert.Equal(t, 1, stats.Files)
		assert.Equal(t, 2, stats.Additions)
		assert.Equal(t, 1, stats.Deletions)
	})

	t.Run("returns stats for multiple files", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		require.NoError(t, eb.CreateBranch("feature"))

		require.NoError(t, os.WriteFile(filepath.Join(dir, "new.txt"), []byte("1\n2\n3\n4\n5\n"), 0o600))
		require.NoError(t, eb.Add("new.txt"))

		require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Changed\nLine2\nLine3\n"), 0o600))
		require.NoError(t, eb.Add("README.md"))
		require.NoError(t, eb.Commit("add and modify"))

		stats, err := eb.diffStats("master")
		require.NoError(t, err)
		assert.Equal(t, 2, stats.Files)
		assert.Equal(t, 8, stats.Additions) // 5 from new.txt + 3 from README.md
		assert.Equal(t, 1, stats.Deletions) // 1 from README.md
	})
}

func TestExternalBackend_toRelative(t *testing.T) {
	dir := setupExternalTestRepo(t)
	eb, err := newExternalBackend(dir)
	require.NoError(t, err)

	t.Run("returns repo-relative path unchanged", func(t *testing.T) {
		rel, err := eb.toRelative("docs/plans/test.md")
		require.NoError(t, err)
		assert.Equal(t, "docs/plans/test.md", rel)
	})

	t.Run("rejects .. path", func(t *testing.T) {
		_, err := eb.toRelative("../outside.txt")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "escapes repository root")
	})

	t.Run("converts absolute path to relative", func(t *testing.T) {
		absPath := filepath.Join(eb.path, "docs", "plans", "test.md")
		rel, err := eb.toRelative(absPath)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("docs", "plans", "test.md"), rel)
	})

	t.Run("rejects absolute path outside repo", func(t *testing.T) {
		_, err := eb.toRelative("/tmp/outside/file.txt")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "outside repository")
	})
}

func TestExternalBackend_extractPathFromPorcelain(t *testing.T) {
	eb := &externalBackend{path: "/tmp"}

	t.Run("extracts simple path", func(t *testing.T) {
		assert.Equal(t, "file.txt", eb.extractPathFromPorcelain("?? file.txt"))
	})

	t.Run("extracts modified path", func(t *testing.T) {
		assert.Equal(t, "README.md", eb.extractPathFromPorcelain(" M README.md"))
	})

	t.Run("extracts renamed path", func(t *testing.T) {
		assert.Equal(t, "new.txt", eb.extractPathFromPorcelain("R  old.txt -> new.txt"))
	})

	t.Run("returns empty for short line", func(t *testing.T) {
		assert.Empty(t, eb.extractPathFromPorcelain("??"))
	})
}

// openBothBackends creates both backends for the same repo directory.
// uses git CLI to set up the repo so both backends can open it.
func openBothBackends(t *testing.T, dir string) (internal, external backend) {
	t.Helper()
	internal, err := openRepo(dir)
	require.NoError(t, err)
	external, err = newExternalBackend(dir)
	require.NoError(t, err)
	return internal, external
}

func TestCrossBackend_CleanRepo(t *testing.T) {
	dir := setupExternalTestRepo(t)
	internal, external := openBothBackends(t, dir)
	// use internal root for building absolute paths; avoids macOS /var vs /private/var mismatch
	dir = internal.Root()

	t.Run("Root", func(t *testing.T) {
		// resolve symlinks for comparison (macOS /var -> /private/var)
		intRoot, err := filepath.EvalSymlinks(internal.Root())
		require.NoError(t, err)
		extRoot, err := filepath.EvalSymlinks(external.Root())
		require.NoError(t, err)
		assert.Equal(t, intRoot, extRoot)
	})

	t.Run("headHash", func(t *testing.T) {
		intHash, err := internal.headHash()
		require.NoError(t, err)
		extHash, err := external.headHash()
		require.NoError(t, err)
		assert.Equal(t, intHash, extHash)
	})

	t.Run("HasCommits", func(t *testing.T) {
		intHas, err := internal.HasCommits()
		require.NoError(t, err)
		extHas, err := external.HasCommits()
		require.NoError(t, err)
		assert.Equal(t, intHas, extHas)
	})

	t.Run("CurrentBranch", func(t *testing.T) {
		intBranch, err := internal.CurrentBranch()
		require.NoError(t, err)
		extBranch, err := external.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, intBranch, extBranch)
	})

	t.Run("IsMainBranch", func(t *testing.T) {
		intMain, err := internal.IsMainBranch()
		require.NoError(t, err)
		extMain, err := external.IsMainBranch()
		require.NoError(t, err)
		assert.Equal(t, intMain, extMain)
	})

	t.Run("GetDefaultBranch", func(t *testing.T) {
		assert.Equal(t, internal.GetDefaultBranch(), external.GetDefaultBranch())
	})

	t.Run("BranchExists", func(t *testing.T) {
		branch, err := internal.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, internal.BranchExists(branch), external.BranchExists(branch))
		assert.Equal(t, internal.BranchExists("nonexistent"), external.BranchExists("nonexistent"))
	})

	t.Run("IsDirty", func(t *testing.T) {
		intDirty, err := internal.IsDirty()
		require.NoError(t, err)
		extDirty, err := external.IsDirty()
		require.NoError(t, err)
		assert.Equal(t, intDirty, extDirty)
	})

	t.Run("FileHasChanges", func(t *testing.T) {
		// use absolute path so both backends resolve correctly regardless of CWD
		readme := filepath.Join(dir, "README.md")
		intHas, err := internal.FileHasChanges(readme)
		require.NoError(t, err)
		extHas, err := external.FileHasChanges(readme)
		require.NoError(t, err)
		assert.Equal(t, intHas, extHas)
	})

	t.Run("HasChangesOtherThan", func(t *testing.T) {
		readme := filepath.Join(dir, "README.md")
		intHas, err := internal.HasChangesOtherThan(readme)
		require.NoError(t, err)
		extHas, err := external.HasChangesOtherThan(readme)
		require.NoError(t, err)
		assert.Equal(t, intHas, extHas)
	})

	t.Run("IsIgnored", func(t *testing.T) {
		intIgnored, err := internal.IsIgnored("README.md")
		require.NoError(t, err)
		extIgnored, err := external.IsIgnored("README.md")
		require.NoError(t, err)
		assert.Equal(t, intIgnored, extIgnored)
	})

	t.Run("diffStats same branch", func(t *testing.T) {
		branch, err := internal.CurrentBranch()
		require.NoError(t, err)
		intStats, err := internal.diffStats(branch)
		require.NoError(t, err)
		extStats, err := external.diffStats(branch)
		require.NoError(t, err)
		assert.Equal(t, intStats, extStats)
	})
}

func TestCrossBackend_DirtyRepo(t *testing.T) {
	dir := setupExternalTestRepo(t)

	// modify tracked file (unstaged)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Modified\n"), 0o600))

	internal, external := openBothBackends(t, dir)

	// use the internal backend's root for building absolute paths
	// so normalizeToRelative resolves correctly (macOS symlink: /var vs /private/var)
	dir = internal.Root()

	t.Run("IsDirty", func(t *testing.T) {
		intDirty, err := internal.IsDirty()
		require.NoError(t, err)
		extDirty, err := external.IsDirty()
		require.NoError(t, err)
		assert.Equal(t, intDirty, extDirty)
		assert.True(t, intDirty)
	})

	t.Run("FileHasChanges modified", func(t *testing.T) {
		readme := filepath.Join(dir, "README.md")
		intHas, err := internal.FileHasChanges(readme)
		require.NoError(t, err)
		extHas, err := external.FileHasChanges(readme)
		require.NoError(t, err)
		assert.Equal(t, intHas, extHas)
		assert.True(t, intHas)
	})

	t.Run("HasChangesOtherThan modified file", func(t *testing.T) {
		readme := filepath.Join(dir, "README.md")
		intHas, err := internal.HasChangesOtherThan(readme)
		require.NoError(t, err)
		extHas, err := external.HasChangesOtherThan(readme)
		require.NoError(t, err)
		assert.Equal(t, intHas, extHas, "backends must agree on HasChangesOtherThan")
		assert.False(t, intHas) // only README.md changed
	})
}

func TestCrossBackend_UntrackedFiles(t *testing.T) {
	dir := setupExternalTestRepo(t)

	// add an untracked file
	require.NoError(t, os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("new content"), 0o600))

	internal, external := openBothBackends(t, dir)
	dir = internal.Root()

	t.Run("IsDirty untracked only", func(t *testing.T) {
		intDirty, err := internal.IsDirty()
		require.NoError(t, err)
		extDirty, err := external.IsDirty()
		require.NoError(t, err)
		assert.Equal(t, intDirty, extDirty)
		assert.False(t, intDirty) // untracked files don't count as dirty
	})

	t.Run("FileHasChanges untracked", func(t *testing.T) {
		untracked := filepath.Join(dir, "untracked.txt")
		intHas, err := internal.FileHasChanges(untracked)
		require.NoError(t, err)
		extHas, err := external.FileHasChanges(untracked)
		require.NoError(t, err)
		assert.Equal(t, intHas, extHas)
		assert.True(t, intHas)
	})

	t.Run("HasChangesOtherThan committed file", func(t *testing.T) {
		readme := filepath.Join(dir, "README.md")
		intHas, err := internal.HasChangesOtherThan(readme)
		require.NoError(t, err)
		extHas, err := external.HasChangesOtherThan(readme)
		require.NoError(t, err)
		assert.Equal(t, intHas, extHas)
		assert.True(t, intHas) // untracked.txt is "other than README.md"
	})
}

func TestCrossBackend_GitignoredFiles(t *testing.T) {
	dir := setupExternalTestRepo(t)

	// add gitignore and commit it
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.log\nprogress-*.txt\n"), 0o600))
	runGit(t, dir, "add", ".gitignore")
	runGit(t, dir, "commit", "-m", "add gitignore")

	// create ignored files
	require.NoError(t, os.WriteFile(filepath.Join(dir, "debug.log"), []byte("log content"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "progress-test.txt"), []byte("progress"), 0o600))

	internal, external := openBothBackends(t, dir)
	dir = internal.Root()

	t.Run("IsIgnored log file", func(t *testing.T) {
		intIgnored, err := internal.IsIgnored("debug.log")
		require.NoError(t, err)
		extIgnored, err := external.IsIgnored("debug.log")
		require.NoError(t, err)
		assert.Equal(t, intIgnored, extIgnored)
		assert.True(t, intIgnored)
	})

	t.Run("IsIgnored progress file", func(t *testing.T) {
		intIgnored, err := internal.IsIgnored("progress-test.txt")
		require.NoError(t, err)
		extIgnored, err := external.IsIgnored("progress-test.txt")
		require.NoError(t, err)
		assert.Equal(t, intIgnored, extIgnored)
		assert.True(t, intIgnored)
	})

	t.Run("IsIgnored tracked file", func(t *testing.T) {
		intIgnored, err := internal.IsIgnored("README.md")
		require.NoError(t, err)
		extIgnored, err := external.IsIgnored("README.md")
		require.NoError(t, err)
		assert.Equal(t, intIgnored, extIgnored)
		assert.False(t, intIgnored)
	})

	t.Run("IsDirty with only ignored files", func(t *testing.T) {
		intDirty, err := internal.IsDirty()
		require.NoError(t, err)
		extDirty, err := external.IsDirty()
		require.NoError(t, err)
		assert.Equal(t, intDirty, extDirty)
		assert.False(t, intDirty)
	})

	t.Run("HasChangesOtherThan with ignored files", func(t *testing.T) {
		readme := filepath.Join(dir, "README.md")
		intHas, err := internal.HasChangesOtherThan(readme)
		require.NoError(t, err)
		extHas, err := external.HasChangesOtherThan(readme)
		require.NoError(t, err)
		assert.Equal(t, intHas, extHas)
		assert.False(t, intHas) // ignored files shouldn't count
	})
}

func TestCrossBackend_FeatureBranch(t *testing.T) {
	dir := setupExternalTestRepo(t)

	// get the default branch name before creating feature branch
	runGit(t, dir, "checkout", "-b", "feature-test")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "feature.txt"), []byte("line1\nline2\nline3\n"), 0o600))
	runGit(t, dir, "add", "feature.txt")
	runGit(t, dir, "commit", "-m", "add feature file")

	internal, external := openBothBackends(t, dir)

	t.Run("CurrentBranch on feature", func(t *testing.T) {
		intBranch, err := internal.CurrentBranch()
		require.NoError(t, err)
		extBranch, err := external.CurrentBranch()
		require.NoError(t, err)
		assert.Equal(t, intBranch, extBranch)
		assert.Equal(t, "feature-test", intBranch)
	})

	t.Run("IsMainBranch on feature", func(t *testing.T) {
		intMain, err := internal.IsMainBranch()
		require.NoError(t, err)
		extMain, err := external.IsMainBranch()
		require.NoError(t, err)
		assert.Equal(t, intMain, extMain)
		assert.False(t, intMain)
	})

	t.Run("BranchExists feature", func(t *testing.T) {
		assert.Equal(t, internal.BranchExists("feature-test"), external.BranchExists("feature-test"))
		assert.True(t, internal.BranchExists("feature-test"))
		assert.Equal(t, internal.BranchExists("master"), external.BranchExists("master"))
		assert.True(t, internal.BranchExists("master"))
	})

	t.Run("diffStats feature vs master", func(t *testing.T) {
		intStats, err := internal.diffStats("master")
		require.NoError(t, err)
		extStats, err := external.diffStats("master")
		require.NoError(t, err)
		assert.Equal(t, intStats, extStats)
		assert.Equal(t, 1, intStats.Files)
		assert.Equal(t, 3, intStats.Additions)
		assert.Equal(t, 0, intStats.Deletions)
	})

	t.Run("diffStats nonexistent branch", func(t *testing.T) {
		intStats, err := internal.diffStats("nonexistent")
		require.NoError(t, err)
		extStats, err := external.diffStats("nonexistent")
		require.NoError(t, err)
		assert.Equal(t, intStats, extStats)
		assert.Equal(t, DiffStats{}, intStats)
	})
}
