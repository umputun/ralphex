package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// externalBackend implements the backend interface by shelling out to the git CLI.
type externalBackend struct {
	path string // absolute path to repository root
}

// newExternalBackend creates an externalBackend that shells out to the git CLI.
// validates the path is inside a git repository using git rev-parse.
func newExternalBackend(path string) (*externalBackend, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve path: %w", err)
	}

	// validate path is a git repo and get the toplevel
	cmd := exec.CommandContext(context.Background(), "git", "rev-parse", "--show-toplevel")
	cmd.Dir = absPath
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return nil, fmt.Errorf("open git repository %s: %s", absPath, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("open git repository %s: %w", absPath, err)
	}

	root := strings.TrimSpace(string(out))

	// resolve symlinks for consistent path comparison (macOS /var -> /private/var)
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("eval symlinks: %w", err)
	}

	return &externalBackend{path: root}, nil
}

// run executes a git command and returns combined stdout+stderr with trailing whitespace removed.
// leading whitespace is preserved (important for porcelain format parsing).
// on failure, returns error with the combined output for diagnostics.
func (e *externalBackend) run(args ...string) (string, error) {
	cmd := exec.CommandContext(context.Background(), "git", args...)
	cmd.Dir = e.path
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return "", fmt.Errorf("git %s: %s", args[0], msg)
		}
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return strings.TrimRight(string(out), " \t\n\r"), nil
}

// compile-time check: externalBackend must satisfy the backend interface
var _ backend = (*externalBackend)(nil)

// Root returns the absolute path to the repository root.
func (e *externalBackend) Root() string {
	return e.path
}

// headHash returns the current HEAD commit hash.
func (e *externalBackend) headHash() (string, error) {
	out, err := e.run("rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("get HEAD: %w", err)
	}
	return out, nil
}

// HasCommits returns true if the repository has at least one commit.
func (e *externalBackend) HasCommits() (bool, error) {
	cmd := exec.CommandContext(context.Background(), "git", "rev-parse", "HEAD")
	cmd.Dir = e.path
	cmd.Env = append(os.Environ(), "LC_ALL=C") // force English stderr for reliable parsing
	if _, err := cmd.Output(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 128 {
			// git outputs "ambiguous argument 'HEAD'" when HEAD doesn't exist (empty repo);
			// other exit-128 causes (corruption, permission errors) propagate as errors.
			// note: must use cmd.Output() (not cmd.Run()) so ExitError.Stderr is populated.
			stderr := strings.ToLower(string(exitErr.Stderr))
			if strings.Contains(stderr, "ambiguous argument") {
				return false, nil // no commits (empty repo, HEAD not found)
			}
			return false, fmt.Errorf("check HEAD: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return false, fmt.Errorf("check HEAD: %w", err) // unexpected exit code or exec failure
	}
	return true, nil
}

// CurrentBranch returns the name of the current branch, or empty string for detached HEAD.
func (e *externalBackend) CurrentBranch() (string, error) {
	cmd := exec.CommandContext(context.Background(), "git", "symbolic-ref", "--short", "HEAD")
	cmd.Dir = e.path
	cmd.Env = append(os.Environ(), "LC_ALL=C") // force English stderr for reliable parsing
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 128 {
			// only treat as "detached HEAD" when stderr indicates symbolic-ref failure;
			// other exit-128 causes (corruption, permission errors) should propagate as errors
			stderr := strings.ToLower(string(exitErr.Stderr))
			if strings.Contains(stderr, "not a symbolic ref") {
				return "", nil // detached HEAD (symbolic-ref fails when not on a branch)
			}
			return "", fmt.Errorf("get current branch: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("get current branch: %w", err) // unexpected exit code or exec failure
	}
	return strings.TrimSpace(string(out)), nil
}

// GetDefaultBranch returns the default branch name.
// detects from origin/HEAD symbolic reference, falls back to checking common branch names.
func (e *externalBackend) GetDefaultBranch() string {
	// try origin/HEAD first
	cmd := exec.CommandContext(context.Background(), "git", "symbolic-ref", "refs/remotes/origin/HEAD")
	cmd.Dir = e.path
	out, err := cmd.Output()
	if err == nil {
		ref := strings.TrimSpace(string(out))
		// ref is like "refs/remotes/origin/main"
		if strings.HasPrefix(ref, "refs/remotes/origin/") {
			branchName := ref[len("refs/remotes/origin/"):]

			// check if local branch exists
			if e.refExists("refs/heads/" + branchName) {
				return branchName
			}
			// local branch doesn't exist, return remote-tracking ref
			return "origin/" + branchName
		}
	}

	// fallback: check which common branch names exist
	for _, name := range []string{"main", "master", "trunk", "develop"} {
		if e.refExists("refs/heads/" + name) {
			return name
		}
	}

	return "master"
}

// BranchExists checks if a branch with the given name exists.
func (e *externalBackend) BranchExists(name string) bool {
	return e.refExists("refs/heads/" + name)
}

// CreateBranch creates a new branch and switches to it.
func (e *externalBackend) CreateBranch(name string) error {
	_, err := e.run("checkout", "-b", name)
	if err != nil {
		return fmt.Errorf("create branch: %w", err)
	}
	return nil
}

// CheckoutBranch switches to an existing branch.
func (e *externalBackend) CheckoutBranch(name string) error {
	_, err := e.run("checkout", name)
	if err != nil {
		return fmt.Errorf("checkout branch: %w", err)
	}
	return nil
}

// IsDirty returns true if the worktree has uncommitted changes (staged or modified tracked files).
func (e *externalBackend) IsDirty() (bool, error) {
	out, err := e.run("status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("get status: %w", err)
	}
	if out == "" {
		return false, nil
	}

	// check each line - only count tracked changes (not untracked files marked with ??)
	for line := range strings.SplitSeq(out, "\n") {
		if line == "" {
			continue
		}
		// untracked files (lines starting with "??") don't count as dirty
		if strings.HasPrefix(line, "??") {
			continue
		}
		return true, nil
	}
	return false, nil
}

// FileHasChanges returns true if the given file has uncommitted changes.
func (e *externalBackend) FileHasChanges(path string) (bool, error) {
	rel, err := e.toRelative(path)
	if err != nil {
		return false, err
	}

	// use -uall to list individual files, not collapsed directories
	out, err := e.run("status", "--porcelain", "-uall", "--", rel)
	if err != nil {
		return false, fmt.Errorf("check file status: %w", err)
	}
	return out != "", nil
}

// HasChangesOtherThan returns true if there are uncommitted changes to files other than the given file.
// this includes modified/deleted tracked files, staged changes, and untracked files (excluding gitignored).
func (e *externalBackend) HasChangesOtherThan(path string) (bool, error) {
	rel, err := e.toRelative(path)
	if err != nil {
		return false, err
	}

	// use -uall to list individual files, not collapsed directories
	out, err := e.run("status", "--porcelain", "-uall")
	if err != nil {
		return false, fmt.Errorf("get status: %w", err)
	}

	if out == "" {
		return false, nil
	}

	for line := range strings.SplitSeq(out, "\n") {
		if line == "" {
			continue
		}
		// extract file path from porcelain output: "XY path" or "XY path -> newpath"
		filePath := e.extractPathFromPorcelain(line)
		if filePath == rel {
			continue
		}
		return true, nil
	}
	return false, nil
}

// IsIgnored checks if a path is ignored by gitignore rules.
func (e *externalBackend) IsIgnored(path string) (bool, error) {
	cmd := exec.CommandContext(context.Background(), "git", "check-ignore", "-q", "--", path)
	cmd.Dir = e.path
	err := cmd.Run()
	if err == nil {
		return true, nil // exit 0 = ignored
	}
	// exit 1 = not ignored, other codes = error
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if exitErr.ExitCode() == 1 {
			return false, nil
		}
	}
	return false, fmt.Errorf("check-ignore: %w", err)
}

// Add stages a file for commit.
func (e *externalBackend) Add(path string) error {
	rel, err := e.toRelative(path)
	if err != nil {
		return err
	}
	_, err = e.run("add", "--", rel)
	if err != nil {
		return fmt.Errorf("add file: %w", err)
	}
	return nil
}

// MoveFile moves a file using git mv.
func (e *externalBackend) MoveFile(src, dst string) error {
	srcRel, err := e.toRelative(src)
	if err != nil {
		return fmt.Errorf("invalid source path: %w", err)
	}
	dstRel, err := e.toRelative(dst)
	if err != nil {
		return fmt.Errorf("invalid destination path: %w", err)
	}
	_, err = e.run("mv", "--", srcRel, dstRel)
	if err != nil {
		return fmt.Errorf("move file: %w", err)
	}
	return nil
}

// Commit creates a commit with the given message.
func (e *externalBackend) Commit(msg string) error {
	_, err := e.run("commit", "-m", msg)
	if err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// CreateInitialCommit stages all non-ignored files and creates an initial commit.
func (e *externalBackend) CreateInitialCommit(msg string) error {
	// git add -A respects .gitignore natively
	_, err := e.run("add", "-A")
	if err != nil {
		return fmt.Errorf("stage files: %w", err)
	}

	// check if anything was staged
	out, err := e.run("status", "--porcelain")
	if err != nil {
		return fmt.Errorf("check status: %w", err)
	}
	if out == "" {
		return errors.New("no files to commit")
	}

	_, err = e.run("commit", "-m", msg)
	if err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// diffStats returns change statistics between baseBranch and HEAD.
// returns zero stats if baseBranch doesn't exist or HEAD equals baseBranch.
func (e *externalBackend) diffStats(baseBranch string) (DiffStats, error) {
	// check if base branch exists (try local, remote, origin/ prefix)
	baseRef := e.resolveRef(baseBranch)
	if baseRef == "" {
		return DiffStats{}, nil
	}

	// check if HEAD equals base
	headHash, err := e.headHash()
	if err != nil {
		return DiffStats{}, nil //nolint:nilerr // no HEAD means no stats
	}

	baseCmd := exec.CommandContext(context.Background(), "git", "rev-parse", baseRef) //nolint:gosec // baseRef from resolveRef, not user input
	baseCmd.Dir = e.path
	baseOut, err := baseCmd.Output()
	if err != nil {
		return DiffStats{}, nil //nolint:nilerr // can't resolve base, return zero
	}
	if strings.TrimSpace(string(baseOut)) == headHash {
		return DiffStats{}, nil
	}

	// get numstat
	out, err := e.run("diff", "--numstat", baseRef+"...HEAD")
	if err != nil {
		return DiffStats{}, fmt.Errorf("diff numstat: %w", err)
	}

	if out == "" {
		return DiffStats{}, nil
	}

	var result DiffStats
	for line := range strings.SplitSeq(out, "\n") {
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}
		// binary files show "-" for additions/deletions
		if parts[0] == "-" || parts[1] == "-" {
			result.Files++
			continue
		}
		additions, _ := strconv.Atoi(parts[0])
		deletions, _ := strconv.Atoi(parts[1])
		result.Files++
		result.Additions += additions
		result.Deletions += deletions
	}
	return result, nil
}

// resolveRef tries to resolve a branch name to a valid git ref.
// checks local branch, remote tracking (origin/<name>), and as-is for "origin/" prefixed names.
func (e *externalBackend) resolveRef(branchName string) string {
	// try local branch
	if e.refExists("refs/heads/" + branchName) {
		return branchName
	}

	// try remote tracking branch
	if e.refExists("refs/remotes/origin/" + branchName) {
		return "origin/" + branchName
	}

	// try as-is for "origin/" prefixed names
	if strings.HasPrefix(branchName, "origin/") {
		remoteName := branchName[7:]
		if e.refExists("refs/remotes/origin/" + remoteName) {
			return branchName
		}
	}

	return ""
}

// refExists checks if a git reference exists.
func (e *externalBackend) refExists(ref string) bool {
	cmd := exec.CommandContext(context.Background(), "git", "show-ref", "--verify", "--quiet", ref)
	cmd.Dir = e.path
	return cmd.Run() == nil
}

// toRelative converts a path to be relative to the repository root.
func (e *externalBackend) toRelative(path string) (string, error) {
	if !filepath.IsAbs(path) {
		cleaned := filepath.Clean(path)
		if strings.HasPrefix(cleaned, "..") {
			return "", fmt.Errorf("path %q escapes repository root", path)
		}
		return cleaned, nil
	}

	// resolve symlinks for consistent comparison (macOS /var -> /private/var)
	resolved, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		// if can't resolve, use original path
		resolved = filepath.Dir(path)
	}
	path = filepath.Join(resolved, filepath.Base(path))

	rel, err := filepath.Rel(e.path, path)
	if err != nil {
		return "", fmt.Errorf("path outside repository: %w", err)
	}
	if strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path %q is outside repository root %q", path, e.path)
	}
	return rel, nil
}

// extractPathFromPorcelain extracts file path from git status --porcelain output.
// format: "XY path" or "XY original -> renamed"
func (e *externalBackend) extractPathFromPorcelain(line string) string {
	if len(line) < 4 {
		return ""
	}
	// skip the 2-char status code and space
	path := line[3:]
	// handle renames: "XY old -> new"
	if idx := strings.Index(path, " -> "); idx >= 0 {
		path = path[idx+4:]
	}
	return path
}
