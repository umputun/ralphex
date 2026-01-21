// Package git provides git repository operations using go-git library.
package git

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// Repo provides git operations using go-git.
type Repo struct {
	repo *git.Repository
	path string // absolute path to repository root
}

// Root returns the absolute path to the repository root.
func (r *Repo) Root() string {
	return r.path
}

// toRelative converts a path to be relative to the repository root.
// Absolute paths are converted to repo-relative.
// Relative paths starting with ".." are resolved against CWD first.
// Other relative paths are assumed to already be repo-relative.
// Returns error if the resolved path is outside the repository.
func (r *Repo) toRelative(path string) (string, error) {
	// for relative paths, just clean and validate
	if !filepath.IsAbs(path) {
		cleaned := filepath.Clean(path)
		if strings.HasPrefix(cleaned, "..") {
			return "", fmt.Errorf("path %q escapes repository root", path)
		}
		return cleaned, nil
	}

	// convert absolute path to repo-relative
	rel, err := filepath.Rel(r.path, path)
	if err != nil {
		return "", fmt.Errorf("path outside repository: %w", err)
	}

	if strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path %q is outside repository root %q", path, r.path)
	}

	return rel, nil
}

// Open opens a git repository at the given path.
func Open(path string) (*Repo, error) {
	repo, err := git.PlainOpen(path)
	if err != nil {
		return nil, fmt.Errorf("open repository: %w", err)
	}

	// get the worktree root path
	wt, err := repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("get worktree: %w", err)
	}

	return &Repo{repo: repo, path: wt.Filesystem.Root()}, nil
}

// CurrentBranch returns the name of the current branch, or empty string for detached HEAD state.
func (r *Repo) CurrentBranch() (string, error) {
	head, err := r.repo.Head()
	if err != nil {
		return "", fmt.Errorf("get HEAD: %w", err)
	}
	if !head.Name().IsBranch() {
		return "", nil // detached HEAD
	}
	return head.Name().Short(), nil
}

// CreateBranch creates a new branch and switches to it.
// Returns error if branch already exists to prevent data loss.
func (r *Repo) CreateBranch(name string) error {
	wt, err := r.repo.Worktree()
	if err != nil {
		return fmt.Errorf("get worktree: %w", err)
	}

	head, err := r.repo.Head()
	if err != nil {
		return fmt.Errorf("get HEAD: %w", err)
	}

	branchRef := plumbing.NewBranchReferenceName(name)

	// check if branch already exists to prevent overwriting
	if _, err := r.repo.Reference(branchRef, false); err == nil {
		return fmt.Errorf("branch %q already exists", name)
	}

	// create the branch reference pointing to current HEAD
	ref := plumbing.NewHashReference(branchRef, head.Hash())
	if err := r.repo.Storer.SetReference(ref); err != nil {
		return fmt.Errorf("create branch reference: %w", err)
	}

	// create branch config for tracking
	branchConfig := &config.Branch{
		Name: name,
	}
	if err := r.repo.CreateBranch(branchConfig); err != nil {
		// ignore if branch config already exists
		if !errors.Is(err, git.ErrBranchExists) {
			return fmt.Errorf("create branch config: %w", err)
		}
	}

	// checkout the new branch, Keep preserves untracked files
	if err := wt.Checkout(&git.CheckoutOptions{Branch: branchRef, Keep: true}); err != nil {
		return fmt.Errorf("checkout branch: %w", err)
	}

	return nil
}

// BranchExists checks if a branch with the given name exists.
func (r *Repo) BranchExists(name string) bool {
	branchRef := plumbing.NewBranchReferenceName(name)
	_, err := r.repo.Reference(branchRef, false)
	return err == nil
}

// CheckoutBranch switches to an existing branch.
func (r *Repo) CheckoutBranch(name string) error {
	wt, err := r.repo.Worktree()
	if err != nil {
		return fmt.Errorf("get worktree: %w", err)
	}

	branchRef := plumbing.NewBranchReferenceName(name)
	if err := wt.Checkout(&git.CheckoutOptions{Branch: branchRef, Keep: true}); err != nil {
		return fmt.Errorf("checkout branch: %w", err)
	}
	return nil
}

// MoveFile moves a file using git (equivalent to git mv).
// Paths can be absolute or relative to the repository root.
// The destination directory must already exist.
func (r *Repo) MoveFile(src, dst string) error {
	// convert to relative paths for git operations
	srcRel, err := r.toRelative(src)
	if err != nil {
		return fmt.Errorf("invalid source path: %w", err)
	}
	dstRel, err := r.toRelative(dst)
	if err != nil {
		return fmt.Errorf("invalid destination path: %w", err)
	}

	wt, err := r.repo.Worktree()
	if err != nil {
		return fmt.Errorf("get worktree: %w", err)
	}

	srcAbs := filepath.Join(r.path, srcRel)
	dstAbs := filepath.Join(r.path, dstRel)

	// move the file on filesystem
	if err := os.Rename(srcAbs, dstAbs); err != nil {
		return fmt.Errorf("rename file: %w", err)
	}

	// stage the removal of old path
	if _, err := wt.Remove(srcRel); err != nil {
		// rollback filesystem change
		_ = os.Rename(dstAbs, srcAbs)
		return fmt.Errorf("remove old path: %w", err)
	}

	// stage the addition of new path
	if _, err := wt.Add(dstRel); err != nil {
		// rollback: unstage removal and restore file
		_ = os.Rename(dstAbs, srcAbs)
		return fmt.Errorf("add new path: %w", err)
	}

	return nil
}

// Add stages a file for commit.
// Path can be absolute or relative to the repository root.
func (r *Repo) Add(path string) error {
	rel, err := r.toRelative(path)
	if err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}

	wt, err := r.repo.Worktree()
	if err != nil {
		return fmt.Errorf("get worktree: %w", err)
	}

	if _, err := wt.Add(rel); err != nil {
		return fmt.Errorf("add file: %w", err)
	}

	return nil
}

// Commit creates a commit with the given message.
// Returns error if no changes are staged.
func (r *Repo) Commit(msg string) error {
	wt, err := r.repo.Worktree()
	if err != nil {
		return fmt.Errorf("get worktree: %w", err)
	}

	author := r.getAuthor()
	_, err = wt.Commit(msg, &git.CommitOptions{Author: author})
	if err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	return nil
}

// getAuthor returns the commit author from git config or a fallback.
// checks repository config first (.git/config), then falls back to global config,
// and finally to default values.
func (r *Repo) getAuthor() *object.Signature {
	// try repository config first (merges local + global)
	if cfg, err := r.repo.Config(); err == nil {
		if cfg.User.Name != "" && cfg.User.Email != "" {
			return &object.Signature{
				Name:  cfg.User.Name,
				Email: cfg.User.Email,
				When:  time.Now(),
			}
		}
	}

	// fallback to global config only
	if cfg, err := config.LoadConfig(config.GlobalScope); err == nil {
		if cfg.User.Name != "" && cfg.User.Email != "" {
			return &object.Signature{
				Name:  cfg.User.Name,
				Email: cfg.User.Email,
				When:  time.Now(),
			}
		}
	}

	// fallback to default author
	return &object.Signature{
		Name:  "ralphex",
		Email: "ralphex@localhost",
		When:  time.Now(),
	}
}

// IsIgnored checks if a path is ignored by gitignore rules.
// Returns false, nil if no .gitignore exists or cannot be read.
func (r *Repo) IsIgnored(path string) (bool, error) {
	wt, err := r.repo.Worktree()
	if err != nil {
		return false, fmt.Errorf("get worktree: %w", err)
	}

	// read gitignore patterns from the worktree
	patterns, err := gitignore.ReadPatterns(wt.Filesystem, nil)
	if err != nil {
		// if no .gitignore, nothing is ignored
		return false, nil //nolint:nilerr // intentional - no gitignore means nothing is ignored
	}

	matcher := gitignore.NewMatcher(patterns)
	pathParts := strings.Split(filepath.ToSlash(path), "/")
	return matcher.Match(pathParts, false), nil
}

// IsDirty returns true if the worktree has uncommitted changes
// (staged or modified tracked files).
func (r *Repo) IsDirty() (bool, error) {
	wt, err := r.repo.Worktree()
	if err != nil {
		return false, fmt.Errorf("get worktree: %w", err)
	}

	status, err := wt.Status()
	if err != nil {
		return false, fmt.Errorf("get status: %w", err)
	}

	for _, s := range status {
		// check for staged changes
		if s.Staging != git.Unmodified && s.Staging != git.Untracked {
			return true, nil
		}
		// check for unstaged changes to tracked files
		if s.Worktree == git.Modified || s.Worktree == git.Deleted {
			return true, nil
		}
	}

	return false, nil
}
