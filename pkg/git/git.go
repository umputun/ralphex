// Package git provides git repository operations using go-git library.
package git

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// repo provides low-level git operations using go-git.
// This is an internal type - use Service for the public API.
type repo struct {
	gitRepo *git.Repository
	path    string // absolute path to repository root
}

// Root returns the absolute path to the repository root.
func (r *repo) Root() string {
	return r.path
}

// toRelative converts a path to be relative to the repository root.
// Absolute paths are converted to repo-relative.
// Relative paths starting with ".." are resolved against CWD first.
// Other relative paths are assumed to already be repo-relative.
// Returns error if the resolved path is outside the repository.
func (r *repo) toRelative(path string) (string, error) {
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

// openRepo opens a git repository at the given path.
// Supports both regular repositories and git worktrees.
func openRepo(path string) (*repo, error) {
	gitRepo, err := git.PlainOpenWithOptions(path, &git.PlainOpenOptions{
		EnableDotGitCommonDir: true,
	})
	if err != nil {
		return nil, fmt.Errorf("open repository: %w", err)
	}

	// get the worktree root path
	wt, err := gitRepo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("get worktree: %w", err)
	}

	return &repo{gitRepo: gitRepo, path: wt.Filesystem.Root()}, nil
}

// HasCommits returns true if the repository has at least one commit.
func (r *repo) HasCommits() (bool, error) {
	_, err := r.gitRepo.Head()
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			return false, nil // no commits yet
		}
		return false, fmt.Errorf("get HEAD: %w", err)
	}
	return true, nil
}

// CreateInitialCommit stages all non-ignored files and creates an initial commit.
// Returns error if no files to stage or commit fails.
// Respects local, global, and system gitignore patterns via IsIgnored.
func (r *repo) CreateInitialCommit(message string) error {
	wt, err := r.gitRepo.Worktree()
	if err != nil {
		return fmt.Errorf("get worktree: %w", err)
	}

	// get status to find untracked files
	status, err := wt.Status()
	if err != nil {
		return fmt.Errorf("get status: %w", err)
	}

	// collect untracked paths and sort for deterministic staging order
	var paths []string
	for path, s := range status {
		if s.Worktree == git.Untracked {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)

	// stage each untracked file that's not ignored
	staged := 0
	for _, path := range paths {
		ignored, ignoreErr := r.IsIgnored(path)
		if ignoreErr != nil {
			return fmt.Errorf("check ignored %s: %w", path, ignoreErr)
		}
		if ignored {
			continue
		}
		if _, addErr := wt.Add(path); addErr != nil {
			return fmt.Errorf("stage %s: %w", path, addErr)
		}
		staged++
	}

	if staged == 0 {
		return errors.New("no files to commit")
	}

	author := r.getAuthor()
	_, err = wt.Commit(message, &git.CommitOptions{Author: author})
	if err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	return nil
}

// CurrentBranch returns the name of the current branch, or empty string for detached HEAD state.
func (r *repo) CurrentBranch() (string, error) {
	head, err := r.gitRepo.Head()
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
func (r *repo) CreateBranch(name string) error {
	wt, err := r.gitRepo.Worktree()
	if err != nil {
		return fmt.Errorf("get worktree: %w", err)
	}

	head, err := r.gitRepo.Head()
	if err != nil {
		return fmt.Errorf("get HEAD: %w", err)
	}

	branchRef := plumbing.NewBranchReferenceName(name)

	// check if branch already exists to prevent overwriting
	if _, err := r.gitRepo.Reference(branchRef, false); err == nil {
		return fmt.Errorf("branch %q already exists", name)
	}

	// create the branch reference pointing to current HEAD
	ref := plumbing.NewHashReference(branchRef, head.Hash())
	if err := r.gitRepo.Storer.SetReference(ref); err != nil {
		return fmt.Errorf("create branch reference: %w", err)
	}

	// create branch config for tracking
	branchConfig := &config.Branch{
		Name: name,
	}
	if err := r.gitRepo.CreateBranch(branchConfig); err != nil {
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
func (r *repo) BranchExists(name string) bool {
	branchRef := plumbing.NewBranchReferenceName(name)
	_, err := r.gitRepo.Reference(branchRef, false)
	return err == nil
}

// CheckoutBranch switches to an existing branch.
func (r *repo) CheckoutBranch(name string) error {
	wt, err := r.gitRepo.Worktree()
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
func (r *repo) MoveFile(src, dst string) error {
	// convert to relative paths for git operations
	srcRel, err := r.toRelative(src)
	if err != nil {
		return fmt.Errorf("invalid source path: %w", err)
	}
	dstRel, err := r.toRelative(dst)
	if err != nil {
		return fmt.Errorf("invalid destination path: %w", err)
	}

	wt, err := r.gitRepo.Worktree()
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
		if rbErr := os.Rename(dstAbs, srcAbs); rbErr != nil {
			log.Printf("[WARN] rollback failed after Remove error: %v", rbErr)
		}
		return fmt.Errorf("remove old path: %w", err)
	}

	// stage the addition of new path
	if _, err := wt.Add(dstRel); err != nil {
		// rollback: unstage removal and restore file
		if rbErr := os.Rename(dstAbs, srcAbs); rbErr != nil {
			log.Printf("[WARN] rollback failed after Add error: %v", rbErr)
		}
		return fmt.Errorf("add new path: %w", err)
	}

	return nil
}

// Add stages a file for commit.
// Path can be absolute or relative to the repository root.
func (r *repo) Add(path string) error {
	rel, err := r.toRelative(path)
	if err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}

	wt, err := r.gitRepo.Worktree()
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
func (r *repo) Commit(msg string) error {
	wt, err := r.gitRepo.Worktree()
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
func (r *repo) getAuthor() *object.Signature {
	// try repository config first (merges local + global)
	if cfg, err := r.gitRepo.Config(); err == nil {
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
// Checks local .gitignore files, global gitignore (from core.excludesfile or default
// XDG location ~/.config/git/ignore), and system gitignore (/etc/gitconfig).
// Returns false, nil if no gitignore rules exist.
//
// Precedence (highest to lowest): local .gitignore > global > system.
// go-git's Matcher checks patterns from end-to-start, so patterns at end have higher priority.
func (r *repo) IsIgnored(path string) (bool, error) {
	wt, err := r.gitRepo.Worktree()
	if err != nil {
		return false, fmt.Errorf("get worktree: %w", err)
	}

	var patterns []gitignore.Pattern
	rootFS := osfs.New("/")

	// load system patterns first (lowest priority)
	if systemPatterns, err := gitignore.LoadSystemPatterns(rootFS); err == nil {
		patterns = append(patterns, systemPatterns...)
	}

	// load global patterns (middle priority)
	if globalPatterns, err := gitignore.LoadGlobalPatterns(rootFS); err == nil && len(globalPatterns) > 0 {
		patterns = append(patterns, globalPatterns...)
	} else {
		// fallback to default XDG location if core.excludesfile not set
		// git uses $XDG_CONFIG_HOME/git/ignore (defaults to ~/.config/git/ignore)
		patterns = append(patterns, loadXDGGlobalPatterns()...)
	}

	// load local patterns last (highest priority)
	localPatterns, _ := gitignore.ReadPatterns(wt.Filesystem, nil)
	patterns = append(patterns, localPatterns...)

	matcher := gitignore.NewMatcher(patterns)
	pathParts := strings.Split(filepath.ToSlash(path), "/")
	return matcher.Match(pathParts, false), nil
}

// loadXDGGlobalPatterns loads gitignore patterns from the default XDG location.
// Git checks $XDG_CONFIG_HOME/git/ignore, defaulting to ~/.config/git/ignore.
func loadXDGGlobalPatterns() []gitignore.Pattern {
	// check XDG_CONFIG_HOME first, fall back to ~/.config
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}
		configHome = filepath.Join(home, ".config")
	}

	ignorePath := filepath.Join(configHome, "git", "ignore")
	data, err := os.ReadFile(ignorePath) //nolint:gosec // user's gitignore file
	if err != nil {
		return nil
	}

	var patterns []gitignore.Pattern
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, gitignore.ParsePattern(line, nil))
	}
	return patterns
}

// IsDirty returns true if the worktree has uncommitted changes
// (staged or modified tracked files).
func (r *repo) IsDirty() (bool, error) {
	wt, err := r.gitRepo.Worktree()
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

// HasChangesOtherThan returns true if there are uncommitted changes to files other than the given file.
// this includes modified/deleted tracked files, staged changes, and untracked files (excluding gitignored).
func (r *repo) HasChangesOtherThan(filePath string) (bool, error) {
	wt, err := r.gitRepo.Worktree()
	if err != nil {
		return false, fmt.Errorf("get worktree: %w", err)
	}

	status, err := wt.Status()
	if err != nil {
		return false, fmt.Errorf("get status: %w", err)
	}

	relPath, err := r.normalizeToRelative(filePath)
	if err != nil {
		return false, err
	}

	for path, s := range status {
		if path == relPath {
			continue // skip the target file
		}
		if !r.fileHasChanges(s) {
			continue
		}
		// for untracked files, check if they're gitignored
		// note: go-git sets both Staging and Worktree to Untracked for untracked files
		if s.Worktree == git.Untracked {
			ignored, err := r.IsIgnored(path)
			if err != nil {
				return false, fmt.Errorf("check ignored: %w", err)
			}
			if ignored {
				continue // skip gitignored untracked files
			}
		}
		return true, nil
	}

	return false, nil
}

// FileHasChanges returns true if the given file has uncommitted changes.
// this includes untracked, modified, deleted, or staged states.
func (r *repo) FileHasChanges(filePath string) (bool, error) {
	wt, err := r.gitRepo.Worktree()
	if err != nil {
		return false, fmt.Errorf("get worktree: %w", err)
	}

	status, err := wt.Status()
	if err != nil {
		return false, fmt.Errorf("get status: %w", err)
	}

	relPath, err := r.normalizeToRelative(filePath)
	if err != nil {
		return false, err
	}

	if s, ok := status[relPath]; ok {
		return r.fileHasChanges(s), nil
	}

	return false, nil
}

// normalizeToRelative converts a file path to be relative to the repository root.
func (r *repo) normalizeToRelative(filePath string) (string, error) {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return "", fmt.Errorf("get absolute path: %w", err)
	}
	relPath, err := filepath.Rel(r.path, absPath)
	if err != nil {
		return "", fmt.Errorf("get relative path: %w", err)
	}
	return relPath, nil
}

// fileHasChanges checks if a file status indicates uncommitted changes.
func (r *repo) fileHasChanges(s *git.FileStatus) bool {
	return s.Staging != git.Unmodified ||
		s.Worktree == git.Modified || s.Worktree == git.Deleted || s.Worktree == git.Untracked
}

// IsMainBranch returns true if the current branch is "main" or "master".
func (r *repo) IsMainBranch() (bool, error) {
	branch, err := r.CurrentBranch()
	if err != nil {
		return false, fmt.Errorf("get current branch: %w", err)
	}
	return branch == "main" || branch == "master", nil
}
