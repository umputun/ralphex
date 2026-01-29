package git

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/umputun/ralphex/pkg/plan"
)

// Logger provides logging for git operations output.
// Compatible with *color.Color and standard log.Logger.
// The return values from Printf are ignored by Service methods.
type Logger interface {
	Printf(format string, args ...any) (int, error)
}

// Service provides git operations for ralphex workflows.
// It is the single public API for the git package.
type Service struct {
	repo *repo
	log  Logger
}

// NewService opens a git repository and returns a Service.
// path is the path to the repository (use "." for current directory).
// log is used for progress output during operations.
func NewService(path string, log Logger) (*Service, error) {
	r, err := openRepo(path)
	if err != nil {
		return nil, err
	}
	return &Service{repo: r, log: log}, nil
}

// Root returns the absolute path to the repository root.
func (s *Service) Root() string {
	return s.repo.Root()
}

// CurrentBranch returns the name of the current branch, or empty string for detached HEAD state.
func (s *Service) CurrentBranch() (string, error) {
	return s.repo.CurrentBranch()
}

// IsMainBranch returns true if the current branch is "main" or "master".
func (s *Service) IsMainBranch() (bool, error) {
	return s.repo.IsMainBranch()
}

// HasCommits returns true if the repository has at least one commit.
func (s *Service) HasCommits() (bool, error) {
	return s.repo.HasCommits()
}

// CreateBranch creates a new branch and switches to it.
func (s *Service) CreateBranch(name string) error {
	return s.repo.CreateBranch(name)
}

// CreateBranchForPlan creates or switches to a feature branch for plan execution.
// If already on a feature branch (not main/master), returns nil immediately.
// If on main/master, extracts branch name from plan file and creates/switches to it.
// If plan file has uncommitted changes and is the only dirty file, auto-commits it.
func (s *Service) CreateBranchForPlan(planFile string) error {
	isMain, err := s.repo.IsMainBranch()
	if err != nil {
		return fmt.Errorf("check main branch: %w", err)
	}

	if !isMain {
		return nil // already on feature branch
	}

	branchName := plan.ExtractBranchName(planFile)
	currentBranch, err := s.repo.CurrentBranch()
	if err != nil {
		return fmt.Errorf("get current branch: %w", err)
	}

	// check for uncommitted changes to files other than the plan
	hasOtherChanges, err := s.repo.HasChangesOtherThan(planFile)
	if err != nil {
		return fmt.Errorf("check uncommitted files: %w", err)
	}

	if hasOtherChanges {
		// other files have uncommitted changes - show helpful error
		return fmt.Errorf("cannot create branch %q: worktree has uncommitted changes\n\n"+
			"ralphex needs to create a feature branch from %s to isolate plan work.\n\n"+
			"options:\n"+
			"  git stash && ralphex %s && git stash pop   # stash changes temporarily\n"+
			"  git commit -am \"wip\"                       # commit changes first\n"+
			"  ralphex --review                           # skip branch creation (review-only mode)",
			branchName, currentBranch, planFile)
	}

	// check if plan file needs to be committed (untracked, modified, or staged)
	planHasChanges, err := s.repo.FileHasChanges(planFile)
	if err != nil {
		return fmt.Errorf("check plan file status: %w", err)
	}

	// create or switch to branch
	if s.repo.BranchExists(branchName) {
		s.log.Printf("switching to existing branch: %s\n", branchName)
		if err := s.repo.CheckoutBranch(branchName); err != nil {
			return fmt.Errorf("checkout branch %s: %w", branchName, err)
		}
	} else {
		s.log.Printf("creating branch: %s\n", branchName)
		if err := s.repo.CreateBranch(branchName); err != nil {
			return fmt.Errorf("create branch %s: %w", branchName, err)
		}
	}

	// auto-commit plan file if it was the only uncommitted file
	if planHasChanges {
		s.log.Printf("committing plan file: %s\n", filepath.Base(planFile))
		if err := s.repo.Add(planFile); err != nil {
			return fmt.Errorf("stage plan file: %w", err)
		}
		if err := s.repo.Commit("add plan: " + branchName); err != nil {
			return fmt.Errorf("commit plan file: %w", err)
		}
	}

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
	if err := s.repo.MoveFile(planFile, destPath); err != nil {
		// fallback to regular move for untracked files
		if renameErr := os.Rename(planFile, destPath); renameErr != nil {
			return fmt.Errorf("move plan: %w", renameErr)
		}
		// stage the new location - log if fails but continue
		if addErr := s.repo.Add(destPath); addErr != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to stage moved plan: %v\n", addErr)
		}
	}

	// commit the move
	commitMsg := "move completed plan: " + filepath.Base(planFile)
	if err := s.repo.Commit(commitMsg); err != nil {
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
	hasCommits, err := s.repo.HasCommits()
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
	if err := s.repo.CreateInitialCommit("initial commit"); err != nil {
		return fmt.Errorf("create initial commit: %w", err)
	}
	return nil
}

// EnsureIgnored ensures a pattern is in .gitignore.
// uses probePath to check if pattern is already ignored before adding.
// if pattern is already ignored, does nothing.
// if pattern is not ignored, appends it to .gitignore with comment.
func (s *Service) EnsureIgnored(pattern, probePath string) error {
	// check if already ignored - if check fails, proceed to add pattern anyway
	ignored, err := s.repo.IsIgnored(probePath)
	if err == nil && ignored {
		return nil // already ignored
	}
	if err != nil {
		s.log.Printf("warning: checking gitignore: %v, adding pattern anyway\n", err)
	}

	// write to .gitignore at repo root
	gitignorePath := filepath.Join(s.repo.Root(), ".gitignore")
	f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) //nolint:gosec // .gitignore needs world-readable
	if err != nil {
		return fmt.Errorf("open .gitignore: %w", err)
	}

	if _, err := fmt.Fprintf(f, "\n# ralphex progress logs\n%s\n", pattern); err != nil {
		_ = f.Close() // close on write error, ignore close error since write already failed
		return fmt.Errorf("write .gitignore: %w", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("close .gitignore: %w", err)
	}

	s.log.Printf("added %s to .gitignore\n", pattern)
	return nil
}
