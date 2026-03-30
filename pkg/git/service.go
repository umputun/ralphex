package git

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/umputun/ralphex/pkg/plan"
)

//go:generate moq -out mocks/logger.go -pkg mocks -skip-ensure -fmt goimports . Logger

// Logger provides logging for git operations output.
// Compatible with *color.Color and standard log.Logger.
// The return values from Printf are ignored by Service methods.
type Logger interface {
	Printf(format string, args ...any) (int, error)
}

// backend defines the low-level git operations interface.
type backend interface {
	root() string
	headHash() (string, error)
	hasCommits() (bool, error)
	currentBranch() (string, error)
	getDefaultBranch() string
	branchExists(name string) bool
	createBranch(name string) error
	checkoutBranch(name string) error
	diffFingerprint() (string, error)
	isDirty() (bool, error)
	fileHasChanges(path string) (bool, error)
	hasChangesOtherThan(path string) ([]string, error)
	add(path string) error
	moveFile(src, dst string) error
	commit(msg string) error
	commitFiles(msg string, paths ...string) error
	createInitialCommit(msg string) error
	diffStats(baseBranch string) (DiffStats, error)
	addWorktree(path, branch string, createBranch bool) error
	removeWorktree(path string) error
	pruneWorktrees() error
}

// DiffStats holds statistics about changes between two commits.
type DiffStats struct {
	Files     int // number of files changed
	Additions int // lines added
	Deletions int // lines deleted
}

// Service provides git operations for ralphex workflows.
// It is the single public API for the git package.
type Service struct {
	repo    backend
	log     Logger
	trailer string // optional trailer line appended to all commits
}

// NewService opens a git repository and returns a Service.
// path is the path to the repository (use "." for current directory).
// log is used for progress output during operations.
// vcsCmd optionally specifies the vcs command to use (default: "git").
func NewService(path string, log Logger, vcsCmd ...string) (*Service, error) {
	command := "git"
	if len(vcsCmd) > 0 && vcsCmd[0] != "" {
		command = vcsCmd[0]
	}
	b, err := newExternalBackend(path, command)
	if err != nil {
		return nil, err
	}
	return &Service{repo: b, log: log}, nil
}

// SetCommitTrailer sets an optional trailer line appended to all commit messages.
// when set, a blank line and the trailer are appended after the commit message.
func (s *Service) SetCommitTrailer(trailer string) {
	s.trailer = trailer
}

// appendTrailer appends the configured trailer to a commit message.
// returns the message unchanged when no trailer is configured.
func (s *Service) appendTrailer(msg string) string {
	if s.trailer == "" {
		return msg
	}
	return msg + "\n\n" + s.trailer
}

// Root returns the absolute path to the repository root.
func (s *Service) Root() string {
	return s.repo.root()
}

// HeadHash returns the current HEAD commit hash as a hex string.
func (s *Service) HeadHash() (string, error) {
	return s.repo.headHash()
}

// DiffFingerprint returns a hash of the current working tree state (tracked diffs + untracked file content).
// used for stalemate detection - if the fingerprint changes between rounds, Claude made edits.
func (s *Service) DiffFingerprint() (string, error) {
	return s.repo.diffFingerprint()
}

// CurrentBranch returns the name of the current branch, or empty string for detached HEAD state.
func (s *Service) CurrentBranch() (string, error) {
	branch, err := s.repo.currentBranch()
	if err != nil {
		return "", fmt.Errorf("current branch: %w", err)
	}
	return branch, nil
}

// IsDefaultBranch returns true if the current branch matches the given default branch.
// strips "origin/" prefix from defaultBranch for comparison (auto-detect may return "origin/main").
// when defaultBranch is empty, falls back to checking "main" and "master".
func (s *Service) IsDefaultBranch(defaultBranch string) (bool, error) {
	branch, err := s.repo.currentBranch()
	if err != nil {
		return false, fmt.Errorf("is default branch: %w", err)
	}
	return s.matchesDefaultBranch(branch, defaultBranch), nil
}

// matchesDefaultBranch checks if branch matches the given default branch.
// strips "origin/" prefix from defaultBranch for comparison.
// when defaultBranch is empty, falls back to checking "main" and "master".
func (s *Service) matchesDefaultBranch(branch, defaultBranch string) bool {
	if defaultBranch == "" {
		return branch == "main" || branch == "master"
	}
	normalized := strings.TrimPrefix(defaultBranch, "origin/")
	return branch == normalized
}

// GetDefaultBranch returns the default branch name.
// detects from origin/HEAD or common branch names (main, master, trunk, develop).
func (s *Service) GetDefaultBranch() string {
	return s.repo.getDefaultBranch()
}

// HasCommits returns true if the repository has at least one commit.
func (s *Service) HasCommits() (bool, error) {
	has, err := s.repo.hasCommits()
	if err != nil {
		return false, fmt.Errorf("has commits: %w", err)
	}
	return has, nil
}

// CreateBranch creates a new branch and switches to it.
func (s *Service) CreateBranch(name string) error {
	if err := s.repo.createBranch(name); err != nil {
		return fmt.Errorf("create branch: %w", err)
	}
	return nil
}

// preparePlanBranch validates state, extracts branch name, and checks plan file status.
// returns branch name and whether the plan file has uncommitted changes.
// when requireDefault is true, returns error if not on the default branch.
// when requireDefault is false, returns empty branch name if not on the default branch (caller should skip).
// defaultBranch is the resolved default branch name (e.g. "main", "develop", "origin/main").
func (s *Service) preparePlanBranch(planFile string, requireDefault bool, defaultBranch string) (string, bool, error) {
	currentBranch, err := s.repo.currentBranch()
	if err != nil {
		return "", false, fmt.Errorf("check current branch: %w", err)
	}

	if !s.matchesDefaultBranch(currentBranch, defaultBranch) {
		if requireDefault {
			expected := strings.TrimPrefix(defaultBranch, "origin/")
			if expected == "" {
				expected = "main/master"
			}
			return "", false, fmt.Errorf("worktree creation requires %s branch, currently on %q", expected, currentBranch)
		}
		return "", false, nil // already on feature branch, caller should skip
	}

	branchName := plan.ExtractBranchName(planFile)

	// check for uncommitted changes to files other than the plan
	dirtyFiles, err := s.repo.hasChangesOtherThan(planFile)
	if err != nil {
		return "", false, fmt.Errorf("check uncommitted files: %w", err)
	}
	if len(dirtyFiles) > 0 {
		fileList := s.formatDirtyFiles(dirtyFiles)
		if requireDefault {
			return "", false, fmt.Errorf("cannot create worktree: worktree has uncommitted changes other than the plan file\n\n"+
				"uncommitted files:\n%s", fileList)
		}
		return "", false, fmt.Errorf("cannot create branch %q: worktree has uncommitted changes\n\n"+
			"uncommitted files:\n%s\n\n"+
			"ralphex needs to create a feature branch from %s to isolate plan work.\n\n"+
			"options:\n"+
			"  git stash && ralphex %s && git stash pop   # stash changes temporarily\n"+
			"  git commit -am \"wip\"                       # commit changes first\n"+
			"  ralphex --review                           # skip branch creation (review-only mode)",
			branchName, fileList, currentBranch, planFile)
	}

	// check if plan file needs to be committed (untracked, modified, or staged)
	planHasChanges, err := s.repo.fileHasChanges(planFile)
	if err != nil {
		return "", false, fmt.Errorf("check plan file status: %w", err)
	}

	return branchName, planHasChanges, nil
}

// CreateBranchForPlan creates or switches to a feature branch for plan execution.
// If already on a feature branch (not the default branch), returns nil immediately.
// If on the default branch, extracts branch name from plan file and creates/switches to it.
// If plan file has uncommitted changes and is the only dirty file, auto-commits it.
// defaultBranch is the resolved default branch name (e.g. "main", "develop").
func (s *Service) CreateBranchForPlan(planFile, defaultBranch string) error {
	branchName, planHasChanges, err := s.preparePlanBranch(planFile, false, defaultBranch)
	if err != nil {
		return err
	}
	if branchName == "" {
		return nil // already on feature branch
	}

	// create or switch to branch
	if s.repo.branchExists(branchName) {
		s.log.Printf("switching to existing branch: %s\n", branchName)
		if err := s.repo.checkoutBranch(branchName); err != nil {
			return fmt.Errorf("checkout branch %s: %w", branchName, err)
		}
	} else {
		s.log.Printf("creating branch: %s\n", branchName)
		if err := s.repo.createBranch(branchName); err != nil {
			return fmt.Errorf("create branch %s: %w", branchName, err)
		}
	}

	// auto-commit plan file if it was the only uncommitted file
	if planHasChanges {
		s.log.Printf("committing plan file: %s\n", filepath.Base(planFile))
		if err := s.repo.add(planFile); err != nil {
			return fmt.Errorf("stage plan file: %w", err)
		}
		if err := s.repo.commit(s.appendTrailer("add plan: " + branchName)); err != nil {
			return fmt.Errorf("commit plan file: %w", err)
		}
	}

	return nil
}

// CreateWorktreeForPlan creates an isolated git worktree for plan execution.
// must be called from the default branch (same guard as CreateBranchForPlan).
// derives branch name from plan file, creates worktree at .ralphex/worktrees/<branch>.
// returns (worktree path, planNeedsCommit, error). when planNeedsCommit is true the caller
// must commit the plan file in the worktree context (via CommitPlanFile on the worktree's
// git service) so the commit lands on the feature branch rather than the default branch.
// defaultBranch is the resolved default branch name (e.g. "main", "develop").
func (s *Service) CreateWorktreeForPlan(planFile, defaultBranch string) (string, bool, error) {
	// check worktree existence early, before preparePlanBranch runs hasChangesOtherThan
	// (an existing worktree dir would show up as untracked and fail the dirty check)
	earlyBranch := plan.ExtractBranchName(planFile)
	wtPath := filepath.Join(s.repo.root(), ".ralphex", "worktrees", earlyBranch)

	// prune stale worktree entries first
	if pruneErr := s.repo.pruneWorktrees(); pruneErr != nil {
		s.log.Printf("warning: prune worktrees: %v\n", pruneErr)
	}

	// check if worktree directory already exists
	if _, statErr := os.Stat(wtPath); statErr == nil {
		return "", false, fmt.Errorf("worktree already exists at %s, another instance may be running", wtPath)
	}

	branchName, planHasChanges, err := s.preparePlanBranch(planFile, true, defaultBranch)
	if err != nil {
		return "", false, err
	}

	// create worktree with branch
	if s.repo.branchExists(branchName) {
		s.log.Printf("creating worktree with existing branch: %s\n", branchName)
		if err := s.repo.addWorktree(wtPath, branchName, false); err != nil {
			return "", false, fmt.Errorf("add worktree with existing branch: %w", err)
		}
	} else {
		s.log.Printf("creating worktree with new branch: %s\n", branchName)
		if err := s.repo.addWorktree(wtPath, branchName, true); err != nil {
			return "", false, fmt.Errorf("add worktree with new branch: %w", err)
		}
	}

	// copy plan file into worktree so the caller can commit it on the feature branch.
	// without this, the plan file only exists in main's working tree (not committed to HEAD).
	if planHasChanges {
		if cpErr := s.copyToWorktree(planFile, wtPath); cpErr != nil {
			_ = s.repo.removeWorktree(wtPath)
			return "", false, fmt.Errorf("copy plan to worktree: %w", cpErr)
		}
	}

	return wtPath, planHasChanges, nil
}

// CommitPlanFile stages and commits a plan file on the current branch.
// mainRepoRoot is the root of the main repository, used to compute the plan file's
// relative path when the service operates inside a worktree.
func (s *Service) CommitPlanFile(planFile, mainRepoRoot string) error {
	branchName := plan.ExtractBranchName(planFile)
	s.log.Printf("committing plan file: %s\n", filepath.Base(planFile))

	// compute the plan file's relative path from the main repo root, then resolve
	// it inside this repo's root. this is needed because planFile is absolute and
	// may point to the main repo's working tree, which is outside the worktree.
	absPlan, err := filepath.Abs(planFile)
	if err != nil {
		return fmt.Errorf("resolve plan path: %w", err)
	}
	// resolve symlinks so both paths use the same prefix (macOS /var -> /private/var)
	if resolved, evalErr := filepath.EvalSymlinks(absPlan); evalErr == nil {
		absPlan = resolved
	}
	relPlan, err := filepath.Rel(mainRepoRoot, absPlan)
	if err != nil {
		return fmt.Errorf("relative plan path: %w", err)
	}
	localPlan := filepath.Join(s.repo.root(), relPlan)

	if err := s.repo.add(localPlan); err != nil {
		return fmt.Errorf("stage plan file: %w", err)
	}
	if err := s.repo.commit(s.appendTrailer("add plan: " + branchName)); err != nil {
		return fmt.Errorf("commit plan file: %w", err)
	}
	return nil
}

// copyToWorktree copies a file from the main repo working tree into the worktree,
// preserving its relative path from the repo root.
func (s *Service) copyToWorktree(srcPath, wtPath string) error {
	absSrc, err := filepath.Abs(srcPath)
	if err != nil {
		return fmt.Errorf("resolve source path: %w", err)
	}
	// resolve symlinks to match s.repo.root() which is also resolved (via EvalSymlinks in NewService)
	absSrc, err = filepath.EvalSymlinks(absSrc)
	if err != nil {
		return fmt.Errorf("eval symlinks for source: %w", err)
	}
	relPath, err := filepath.Rel(s.repo.root(), absSrc)
	if err != nil {
		return fmt.Errorf("relative path: %w", err)
	}

	dstPath := filepath.Join(wtPath, relPath)
	if err = os.MkdirAll(filepath.Dir(dstPath), 0o750); err != nil {
		return fmt.Errorf("create directories: %w", err)
	}

	src, err := os.Open(absSrc)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	dst, err := os.Create(dstPath) //nolint:gosec // plan file doesn't need restricted perms
	if err != nil {
		return fmt.Errorf("create destination: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("copy file: %w", err)
	}
	return nil
}

// RemoveWorktree removes a git worktree at the given path.
// no-op if the worktree directory doesn't exist or was already removed.
func (s *Service) RemoveWorktree(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil // already removed
	}
	if err := s.repo.removeWorktree(path); err != nil {
		return fmt.Errorf("remove worktree: %w", err)
	}
	s.log.Printf("removed worktree: %s\n", path)
	return nil
}

// MovePlanToCompleted moves a plan file to the completed/ subdirectory and commits.
// Creates the completed/ directory if it doesn't exist.
// Uses git mv if the file is tracked, falls back to os.Rename for untracked files.
// If the source file doesn't exist but the destination does, logs a message and returns nil.
func (s *Service) MovePlanToCompleted(planFile string) error {
	// create completed directory
	completedDir := filepath.Join(filepath.Dir(planFile), "completed")
	if err := os.MkdirAll(completedDir, 0o750); err != nil {
		return fmt.Errorf("create completed dir: %w", err)
	}

	// destination path
	destPath := filepath.Join(completedDir, filepath.Base(planFile))

	// check if already moved (source missing, dest exists)
	if _, err := os.Stat(planFile); os.IsNotExist(err) {
		if _, destErr := os.Stat(destPath); destErr == nil {
			s.log.Printf("plan already in completed/\n")
			return nil
		}
	}

	// use git mv
	if err := s.repo.moveFile(planFile, destPath); err != nil {
		// fallback to regular move for untracked files
		if renameErr := os.Rename(planFile, destPath); renameErr != nil {
			return fmt.Errorf("move plan: %w", renameErr)
		}
		// stage the new location - log if fails but continue
		if addErr := s.repo.add(destPath); addErr != nil {
			s.log.Printf("warning: failed to stage moved plan: %v\n", addErr)
		}
	}

	// commit the move
	commitMsg := "move completed plan: " + filepath.Base(planFile)
	if err := s.repo.commit(s.appendTrailer(commitMsg)); err != nil {
		return fmt.Errorf("commit plan move: %w", err)
	}

	s.log.Printf("moved plan to %s\n", destPath)
	return nil
}

// EnsureHasCommits checks that the repository has at least one commit.
// If the repository is empty, calls promptFn to ask user whether to create initial commit.
// promptFn should return true to create the commit, false to abort.
// Returns error if repo is empty and user declined or promptFn returned false.
func (s *Service) EnsureHasCommits(promptFn func() bool) error {
	hasCommits, err := s.repo.hasCommits()
	if err != nil {
		return fmt.Errorf("check commits: %w", err)
	}
	if hasCommits {
		return nil
	}

	// prompt user to create initial commit
	if !promptFn() {
		return errors.New("no commits - please create initial commit manually")
	}

	// create the commit
	if err := s.repo.createInitialCommit(s.appendTrailer("initial commit")); err != nil {
		return fmt.Errorf("create initial commit: %w", err)
	}
	return nil
}

// DiffStats returns change statistics between baseBranch and HEAD.
// returns zero stats if baseBranch doesn't exist or HEAD equals baseBranch.
func (s *Service) DiffStats(baseBranch string) (DiffStats, error) {
	return s.repo.diffStats(baseBranch)
}

// EnsureLocalGitignore creates .ralphex/.gitignore with patterns for runtime artifacts
// (progress/ and worktrees/). this keeps ignore rules self-contained inside .ralphex/
// instead of modifying the project's root .gitignore.
// idempotent: does nothing if the file already exists with the expected content.
func (s *Service) EnsureLocalGitignore() error {
	ralphexDir := filepath.Join(s.repo.root(), ".ralphex")
	if err := os.MkdirAll(ralphexDir, 0o750); err != nil {
		return fmt.Errorf("create .ralphex dir: %w", err)
	}

	gitignorePath := filepath.Join(ralphexDir, ".gitignore")
	const content = ".gitignore\nprogress/\nworktrees/\n"

	if existing, err := os.ReadFile(gitignorePath); err == nil { //nolint:gosec // .gitignore is world-readable
		if string(existing) == content {
			return nil
		}
	}

	if err := os.WriteFile(gitignorePath, []byte(content), 0o644); err != nil { //nolint:gosec // .gitignore needs world-readable
		return fmt.Errorf("write .ralphex/.gitignore: %w", err)
	}

	s.log.Printf("created .ralphex/.gitignore\n")
	return nil
}

// FileHasChanges returns true if the given file has uncommitted changes (staged or unstaged).
func (s *Service) FileHasChanges(path string) (bool, error) {
	changed, err := s.repo.fileHasChanges(path)
	if err != nil {
		return false, fmt.Errorf("file has changes %q: %w", path, err)
	}
	return changed, nil
}

// formatDirtyFiles formats a list of dirty file paths for display in error messages.
// truncates to 10 files with "and N more" suffix.
func (s *Service) formatDirtyFiles(files []string) string {
	const maxFiles = 10
	var b strings.Builder
	for i, f := range files {
		if i >= maxFiles {
			fmt.Fprintf(&b, "  ... and %d more", len(files)-maxFiles)
			break
		}
		fmt.Fprintf(&b, "  %s\n", f)
	}
	return strings.TrimRight(b.String(), "\n")
}
