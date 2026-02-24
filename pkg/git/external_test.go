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
	runGit(t, dir, "config", "commit.gpgsign", "false")

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

	t.Run("opens git worktree", func(t *testing.T) {
		mainDir := setupExternalTestRepo(t)
		wtDir := filepath.Join(t.TempDir(), "worktree")
		runGit(t, mainDir, "worktree", "add", wtDir, "-b", "wt-branch")
		eb, err := newExternalBackend(wtDir)
		require.NoError(t, err)
		branch, err := eb.currentBranch()
		require.NoError(t, err)
		assert.Equal(t, "wt-branch", branch)
	})
}

func TestExternalBackend_Root(t *testing.T) {
	dir := setupExternalTestRepo(t)
	eb, err := newExternalBackend(dir)
	require.NoError(t, err)
	assert.NotEmpty(t, eb.root())
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

		has, err := eb.hasCommits()
		require.NoError(t, err)
		assert.True(t, has)
	})

	t.Run("returns false for empty repo", func(t *testing.T) {
		dir := t.TempDir()
		runGit(t, dir, "init")

		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		has, err := eb.hasCommits()
		require.NoError(t, err)
		assert.False(t, has)
	})

	t.Run("returns error for non-repo exit-128", func(t *testing.T) {
		// construct externalBackend pointing to a non-repo directory;
		// git rev-parse HEAD exits 128 with "not a git repository" which must propagate as error.
		dir := t.TempDir()
		eb := &externalBackend{path: dir}

		has, err := eb.hasCommits()
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

		branch, err := eb.currentBranch()
		require.NoError(t, err)
		assert.NotEmpty(t, branch)
	})

	t.Run("returns feature branch name", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		require.NoError(t, eb.createBranch("feature-test"))
		branch, err := eb.currentBranch()
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

		branch, err := eb.currentBranch()
		require.NoError(t, err)
		assert.Empty(t, branch)
	})

	t.Run("returns error for non-repo exit-128", func(t *testing.T) {
		// construct externalBackend pointing to a non-repo directory;
		// git symbolic-ref exits 128 with "not a git repository" which must propagate as error.
		dir := t.TempDir()
		eb := &externalBackend{path: dir}

		branch, err := eb.currentBranch()
		require.Error(t, err, "non-repo exit-128 should return error, not silently report detached HEAD")
		assert.Empty(t, branch)
		assert.Contains(t, err.Error(), "get current branch")
	})
}

func TestExternalBackend_GetDefaultBranch(t *testing.T) {
	t.Run("returns existing default branch", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		branch := eb.getDefaultBranch()
		// default from git init is usually master or main
		assert.Contains(t, []string{"main", "master"}, branch)
	})

	t.Run("returns main when main branch exists", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		require.NoError(t, eb.createBranch("main"))
		branch := eb.getDefaultBranch()
		assert.Equal(t, "main", branch)
	})

	t.Run("falls back to master", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		// create a non-standard branch and delete the default one
		runGit(t, dir, "checkout", "-b", "unusual-name")
		runGit(t, dir, "branch", "-D", "master")

		branch := eb.getDefaultBranch()
		assert.Equal(t, "master", branch) // fallback
	})
}

func TestExternalBackend_BranchExists(t *testing.T) {
	t.Run("returns true for existing branch", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		// get default branch name (could be master or main depending on git config)
		branch, err := eb.currentBranch()
		require.NoError(t, err)
		assert.True(t, eb.branchExists(branch))
	})

	t.Run("returns false for non-existent branch", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		assert.False(t, eb.branchExists("nonexistent"))
	})

	t.Run("returns true for created branch", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		require.NoError(t, eb.createBranch("new-branch"))
		assert.True(t, eb.branchExists("new-branch"))
	})
}

func TestExternalBackend_CreateBranch(t *testing.T) {
	t.Run("creates and switches to branch", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		err = eb.createBranch("new-feature")
		require.NoError(t, err)

		branch, err := eb.currentBranch()
		require.NoError(t, err)
		assert.Equal(t, "new-feature", branch)
	})

	t.Run("fails when branch already exists", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		require.NoError(t, eb.createBranch("existing"))
		require.NoError(t, eb.checkoutBranch("master"))

		err = eb.createBranch("existing")
		require.Error(t, err)
	})
}

func TestExternalBackend_CheckoutBranch(t *testing.T) {
	t.Run("switches to existing branch", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		require.NoError(t, eb.createBranch("feature"))
		require.NoError(t, eb.checkoutBranch("master"))

		branch, err := eb.currentBranch()
		require.NoError(t, err)
		assert.Equal(t, "master", branch)
	})

	t.Run("fails on non-existent branch", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		err = eb.checkoutBranch("nonexistent")
		assert.Error(t, err)
	})
}

func TestExternalBackend_IsDirty(t *testing.T) {
	t.Run("clean worktree returns false", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		dirty, err := eb.isDirty()
		require.NoError(t, err)
		assert.False(t, dirty)
	})

	t.Run("staged file returns true", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		require.NoError(t, os.WriteFile(filepath.Join(dir, "staged.txt"), []byte("staged"), 0o600))
		runGit(t, dir, "add", "staged.txt")

		dirty, err := eb.isDirty()
		require.NoError(t, err)
		assert.True(t, dirty)
	})

	t.Run("modified tracked file returns true", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Modified\n"), 0o600))

		dirty, err := eb.isDirty()
		require.NoError(t, err)
		assert.True(t, dirty)
	})

	t.Run("deleted tracked file returns true", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		require.NoError(t, os.Remove(filepath.Join(dir, "README.md")))

		dirty, err := eb.isDirty()
		require.NoError(t, err)
		assert.True(t, dirty)
	})

	t.Run("untracked file only returns false", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		require.NoError(t, os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("untracked"), 0o600))

		dirty, err := eb.isDirty()
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

		dirty, err := eb.isDirty()
		require.NoError(t, err)
		assert.False(t, dirty)
	})
}

func TestExternalBackend_FileHasChanges(t *testing.T) {
	t.Run("returns false for committed file", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		has, err := eb.fileHasChanges("README.md")
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

		has, err := eb.fileHasChanges(filepath.Join("docs", "plans", "feature.md"))
		require.NoError(t, err)
		assert.True(t, has)
	})

	t.Run("returns true for modified file", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Modified"), 0o600))

		has, err := eb.fileHasChanges("README.md")
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

		has, err := eb.fileHasChanges(filepath.Join("docs", "plans", "feature.md"))
		require.NoError(t, err)
		assert.True(t, has)
	})

	t.Run("returns false for nonexistent file", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		has, err := eb.fileHasChanges("nonexistent.md")
		require.NoError(t, err)
		assert.False(t, has)
	})
}

func TestExternalBackend_HasChangesOtherThan(t *testing.T) {
	t.Run("returns false when no changes", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		has, err := eb.hasChangesOtherThan("nonexistent.md")
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

		has, err := eb.hasChangesOtherThan(planFile)
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

		has, err := eb.hasChangesOtherThan(planFile)
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

		has, err := eb.hasChangesOtherThan(planFile)
		require.NoError(t, err)
		assert.True(t, has)
	})
}

func TestExternalBackend_IsIgnored(t *testing.T) {
	t.Run("returns false for non-ignored file", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		ignored, err := eb.isIgnored("README.md")
		require.NoError(t, err)
		assert.False(t, ignored)
	})

	t.Run("returns true for ignored pattern", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("progress-*.txt\n"), 0o600))

		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		ignored, err := eb.isIgnored("progress-test.txt")
		require.NoError(t, err)
		assert.True(t, ignored)
	})

	t.Run("returns false for no gitignore", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		ignored, err := eb.isIgnored("somefile.txt")
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
		err = eb.add("newfile.txt")
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
		err = eb.add(absPath)
		require.NoError(t, err)
	})

	t.Run("fails on non-existent file", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)
		err = eb.add("nonexistent.txt")
		assert.Error(t, err)
	})
}

func TestExternalBackend_MoveFile(t *testing.T) {
	t.Run("moves file and stages changes", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		require.NoError(t, os.MkdirAll(filepath.Join(dir, "subdir"), 0o750))
		err = eb.moveFile("README.md", filepath.Join("subdir", "README.md"))
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

		err = eb.moveFile("nonexistent.txt", "dest.txt")
		assert.Error(t, err)
	})
}

func TestExternalBackend_Commit(t *testing.T) {
	t.Run("creates commit", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		require.NoError(t, os.WriteFile(filepath.Join(dir, "commit-test.txt"), []byte("test"), 0o600))
		require.NoError(t, eb.add("commit-test.txt"))
		err = eb.commit("test commit message")
		require.NoError(t, err)

		// verify commit message
		out := runGit(t, dir, "log", "-1", "--format=%s")
		assert.Contains(t, out, "test commit message")
	})

	t.Run("fails with no staged changes", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		err = eb.commit("empty commit")
		assert.Error(t, err)
	})
}

func TestExternalBackend_CommitFiles(t *testing.T) {
	t.Run("commits only specified file", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		// stage two files
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o600))
		require.NoError(t, eb.add("a.txt"))
		require.NoError(t, eb.add("b.txt"))

		// commit only a.txt
		err = eb.commitFiles("commit a only", "a.txt")
		require.NoError(t, err)

		// a.txt should be clean, b.txt should still be staged
		aChanged, err := eb.fileHasChanges("a.txt")
		require.NoError(t, err)
		assert.False(t, aChanged, "a.txt should be committed")

		bChanged, err := eb.fileHasChanges("b.txt")
		require.NoError(t, err)
		assert.True(t, bChanged, "b.txt should still be staged")
	})

	t.Run("fails with no paths even with staged files", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		// stage a file so the index is not empty
		require.NoError(t, os.WriteFile(filepath.Join(dir, "staged.txt"), []byte("data"), 0o600))
		_, err = eb.run("add", "staged.txt")
		require.NoError(t, err)

		err = eb.commitFiles("should fail")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no paths provided")
	})
}

func TestExternalBackend_CreateInitialCommit(t *testing.T) {
	t.Run("creates commit with files", func(t *testing.T) {
		dir := t.TempDir()
		runGit(t, dir, "init")
		runGit(t, dir, "config", "user.email", "test@test.com")
		runGit(t, dir, "config", "user.name", "test")
		runGit(t, dir, "config", "commit.gpgsign", "false")

		require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o600))

		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		err = eb.createInitialCommit("initial commit")
		require.NoError(t, err)

		has, err := eb.hasCommits()
		require.NoError(t, err)
		assert.True(t, has)
	})

	t.Run("fails with no files", func(t *testing.T) {
		dir := t.TempDir()
		runGit(t, dir, "init")
		runGit(t, dir, "config", "user.email", "test@test.com")
		runGit(t, dir, "config", "user.name", "test")
		runGit(t, dir, "config", "commit.gpgsign", "false")

		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		err = eb.createInitialCommit("initial commit")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no files to commit")
	})

	t.Run("respects gitignore", func(t *testing.T) {
		dir := t.TempDir()
		runGit(t, dir, "init")
		runGit(t, dir, "config", "user.email", "test@test.com")
		runGit(t, dir, "config", "user.name", "test")
		runGit(t, dir, "config", "commit.gpgsign", "false")

		require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.log\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "debug.log"), []byte("log content"), 0o600))

		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		err = eb.createInitialCommit("initial commit")
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

		require.NoError(t, eb.createBranch("feature"))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "new.txt"), []byte("line1\nline2\nline3\n"), 0o600))
		require.NoError(t, eb.add("new.txt"))
		require.NoError(t, eb.commit("add new file"))

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

		require.NoError(t, eb.createBranch("feature"))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Modified\nNew line\n"), 0o600))
		require.NoError(t, eb.add("README.md"))
		require.NoError(t, eb.commit("modify readme"))

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

		require.NoError(t, eb.createBranch("feature"))

		require.NoError(t, os.WriteFile(filepath.Join(dir, "new.txt"), []byte("1\n2\n3\n4\n5\n"), 0o600))
		require.NoError(t, eb.add("new.txt"))

		require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Changed\nLine2\nLine3\n"), 0o600))
		require.NoError(t, eb.add("README.md"))
		require.NoError(t, eb.commit("add and modify"))

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

func TestExternalBackend_AddWorktree(t *testing.T) {
	t.Run("creates worktree with new branch", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		wtDir := filepath.Join(t.TempDir(), "wt")
		err = eb.addWorktree(wtDir, "wt-branch", true)
		require.NoError(t, err)

		// verify worktree exists and is on the correct branch
		wtEB, err := newExternalBackend(wtDir)
		require.NoError(t, err)
		branch, err := wtEB.currentBranch()
		require.NoError(t, err)
		assert.Equal(t, "wt-branch", branch)
	})

	t.Run("creates worktree with existing branch", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		// create a branch first, then go back to master
		require.NoError(t, eb.createBranch("existing-branch"))
		require.NoError(t, eb.checkoutBranch("master"))

		wtDir := filepath.Join(t.TempDir(), "wt")
		err = eb.addWorktree(wtDir, "existing-branch", false)
		require.NoError(t, err)

		// verify worktree is on the existing branch
		wtEB, err := newExternalBackend(wtDir)
		require.NoError(t, err)
		branch, err := wtEB.currentBranch()
		require.NoError(t, err)
		assert.Equal(t, "existing-branch", branch)
	})

	t.Run("fails when branch already checked out", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		// master is currently checked out, trying to create worktree for it should fail
		wtDir := filepath.Join(t.TempDir(), "wt")
		err = eb.addWorktree(wtDir, "master", false)
		require.Error(t, err)
	})
}

func TestExternalBackend_RemoveWorktree(t *testing.T) {
	t.Run("removes existing worktree", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		wtDir := filepath.Join(t.TempDir(), "wt")
		require.NoError(t, eb.addWorktree(wtDir, "wt-branch", true))

		err = eb.removeWorktree(wtDir)
		require.NoError(t, err)

		// verify worktree directory is removed
		_, err = os.Stat(wtDir)
		assert.True(t, os.IsNotExist(err))
	})

	t.Run("fails on nonexistent worktree", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		err = eb.removeWorktree("/nonexistent/path")
		require.Error(t, err)
	})
}

func TestExternalBackend_PruneWorktrees(t *testing.T) {
	t.Run("prunes stale entries", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		// create and manually delete a worktree dir to leave a stale entry
		wtDir := filepath.Join(t.TempDir(), "wt")
		require.NoError(t, eb.addWorktree(wtDir, "stale-branch", true))
		require.NoError(t, os.RemoveAll(wtDir))

		// prune should clean up the stale entry
		err = eb.pruneWorktrees()
		require.NoError(t, err)
	})

	t.Run("succeeds with no stale entries", func(t *testing.T) {
		dir := setupExternalTestRepo(t)
		eb, err := newExternalBackend(dir)
		require.NoError(t, err)

		err = eb.pruneWorktrees()
		require.NoError(t, err)
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
