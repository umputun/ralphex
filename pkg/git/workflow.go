package git

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/umputun/ralphex/pkg/plan"
)

// Workflow provides plan-aware git operations built on top of Repo.
type Workflow struct {
	repo *Repo
	log  func(string, ...any) (int, error)
}

// NewWorkflow creates a Workflow wrapping the given Repo.
// logFn is called to report progress (compatible with color.Color.Printf).
func NewWorkflow(repo *Repo, logFn func(string, ...any) (int, error)) *Workflow {
	return &Workflow{repo: repo, log: logFn}
}

// Repo returns the underlying Repo for direct access when needed.
func (w *Workflow) Repo() *Repo {
	return w.repo
}

// CreateBranchForPlan creates or switches to a feature branch for plan execution.
// If already on a feature branch (not main/master), returns nil immediately.
// If on main/master, extracts branch name from plan file and creates/switches to it.
// If plan file has uncommitted changes and is the only dirty file, auto-commits it.
func (w *Workflow) CreateBranchForPlan(planFile string) error {
	isMain, err := w.repo.IsMainBranch()
	if err != nil {
		return fmt.Errorf("check main branch: %w", err)
	}

	if !isMain {
		return nil // already on feature branch
	}

	branchName := plan.ExtractBranchName(planFile)
	currentBranch, err := w.repo.CurrentBranch()
	if err != nil {
		return fmt.Errorf("get current branch: %w", err)
	}

	// check for uncommitted changes to files other than the plan
	hasOtherChanges, err := w.repo.HasChangesOtherThan(planFile)
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
	planHasChanges, err := w.repo.FileHasChanges(planFile)
	if err != nil {
		return fmt.Errorf("check plan file status: %w", err)
	}

	// create or switch to branch
	if w.repo.BranchExists(branchName) {
		w.log("switching to existing branch: %s\n", branchName)
		if err := w.repo.CheckoutBranch(branchName); err != nil {
			return fmt.Errorf("checkout branch %s: %w", branchName, err)
		}
	} else {
		w.log("creating branch: %s\n", branchName)
		if err := w.repo.CreateBranch(branchName); err != nil {
			return fmt.Errorf("create branch %s: %w", branchName, err)
		}
	}

	// auto-commit plan file if it was the only uncommitted file
	if planHasChanges {
		w.log("committing plan file: %s\n", filepath.Base(planFile))
		if err := w.repo.Add(planFile); err != nil {
			return fmt.Errorf("stage plan file: %w", err)
		}
		if err := w.repo.Commit("add plan: " + branchName); err != nil {
			return fmt.Errorf("commit plan file: %w", err)
		}
	}

	return nil
}

// MovePlanToCompleted moves a plan file to the completed/ subdirectory and commits.
// Creates the completed/ directory if it doesn't exist.
// Uses git mv if the file is tracked, falls back to os.Rename for untracked files.
func (w *Workflow) MovePlanToCompleted(planFile string) error {
	// create completed directory
	completedDir := filepath.Join(filepath.Dir(planFile), "completed")
	if err := os.MkdirAll(completedDir, 0o750); err != nil {
		return fmt.Errorf("create completed dir: %w", err)
	}

	// destination path
	destPath := filepath.Join(completedDir, filepath.Base(planFile))

	// use git mv
	if err := w.repo.MoveFile(planFile, destPath); err != nil {
		// fallback to regular move for untracked files
		if renameErr := os.Rename(planFile, destPath); renameErr != nil {
			return fmt.Errorf("move plan: %w", renameErr)
		}
		// stage the new location - log if fails but continue
		if addErr := w.repo.Add(destPath); addErr != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to stage moved plan: %v\n", addErr)
		}
	}

	// commit the move
	commitMsg := "move completed plan: " + filepath.Base(planFile)
	if err := w.repo.Commit(commitMsg); err != nil {
		return fmt.Errorf("commit plan move: %w", err)
	}

	w.log("moved plan to %s\n", destPath)
	return nil
}

// EnsureHasCommits checks that the repository has at least one commit.
// If the repository is empty, calls promptFn to ask user whether to create initial commit.
// promptFn should return true to create the commit, false to abort.
// Returns error if repo is empty and user declined or promptFn returned false.
func (w *Workflow) EnsureHasCommits(promptFn func() bool) error {
	hasCommits, err := w.repo.HasCommits()
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
	if err := w.repo.CreateInitialCommit("initial commit"); err != nil {
		return fmt.Errorf("create initial commit: %w", err)
	}
	return nil
}
