// Package plan provides plan file selection and manipulation.
package plan

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/umputun/ralphex/pkg/input"
	"github.com/umputun/ralphex/pkg/progress"
)

// datePrefixRe matches date-like prefixes in plan filenames (e.g., "2024-01-15-").
var datePrefixRe = regexp.MustCompile(`^[\d-]+`)

// ErrNoPlansFound is returned when no plan files exist in the plans directory.
var ErrNoPlansFound = errors.New("no plans found")

// Selector handles plan file selection and resolution.
type Selector struct {
	PlansDir string
	Colors   *progress.Colors
}

// NewSelector creates a new Selector with the given plans directory and colors.
func NewSelector(plansDir string, colors *progress.Colors) *Selector {
	return &Selector{
		PlansDir: plansDir,
		Colors:   colors,
	}
}

// Select selects and prepares a plan file.
// if planFile is provided, validates it exists and returns absolute path.
// if planFile is empty and optional is true, returns empty string without error.
// if planFile is empty and optional is false, uses fzf for selection.
func (s *Selector) Select(ctx context.Context, planFile string, optional bool) (string, error) {
	selected, err := s.selectPlan(ctx, planFile, optional)
	if err != nil {
		return "", err
	}
	if selected == "" {
		if !optional {
			return "", errors.New("plan file required for task execution")
		}
		return "", nil
	}
	// normalize to absolute path
	abs, err := filepath.Abs(selected)
	if err != nil {
		return "", fmt.Errorf("resolve plan path: %w", err)
	}
	return abs, nil
}

// selectPlan handles the logic for selecting a plan file.
func (s *Selector) selectPlan(ctx context.Context, planFile string, optional bool) (string, error) {
	if planFile != "" {
		if _, err := os.Stat(planFile); err != nil {
			return "", fmt.Errorf("plan file not found: %s", planFile)
		}
		return planFile, nil
	}

	// for review-only modes, plan is optional
	if optional {
		return "", nil
	}

	// use fzf to select plan
	return s.selectWithFzf(ctx)
}

// selectWithFzf uses fzf to interactively select a plan file from the plans directory.
func (s *Selector) selectWithFzf(ctx context.Context) (string, error) {
	if _, err := os.Stat(s.PlansDir); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w: %s (directory missing)", ErrNoPlansFound, s.PlansDir)
		}
		return "", fmt.Errorf("cannot access plans directory %s: %w", s.PlansDir, err)
	}

	// find plan files (excluding completed/)
	plans, err := filepath.Glob(filepath.Join(s.PlansDir, "*.md"))
	if err != nil || len(plans) == 0 {
		return "", fmt.Errorf("%w: %s", ErrNoPlansFound, s.PlansDir)
	}

	// auto-select if single plan (no fzf needed)
	if len(plans) == 1 {
		s.Colors.Info().Printf("auto-selected: %s\n", plans[0])
		return plans[0], nil
	}

	// multiple plans require fzf
	if _, lookupErr := exec.LookPath("fzf"); lookupErr != nil {
		return "", errors.New("fzf not found, please provide plan file as argument")
	}

	// use fzf for selection
	cmd := exec.CommandContext(ctx, "fzf",
		"--prompt=select plan: ",
		"--preview=head -50 {}",
		"--preview-window=right:60%",
	)
	cmd.Stdin = strings.NewReader(strings.Join(plans, "\n"))
	cmd.Stderr = os.Stderr

	out, err := cmd.Output()
	if err != nil {
		return "", errors.New("no plan selected")
	}

	return strings.TrimSpace(string(out)), nil
}

// FindRecent finds the most recently modified plan file in the plans directory
// that was modified after the given start time.
func (s *Selector) FindRecent(startTime time.Time) string {
	// find all .md files in plansDir (excluding completed/ subdirectory)
	pattern := filepath.Join(s.PlansDir, "*.md")
	plans, err := filepath.Glob(pattern)
	if err != nil || len(plans) == 0 {
		return ""
	}

	var recentPlan string
	var recentTime time.Time

	for _, plan := range plans {
		info, statErr := os.Stat(plan)
		if statErr != nil {
			continue
		}
		// file must be modified after startTime
		if info.ModTime().Before(startTime) {
			continue
		}
		// find the most recent one
		if recentPlan == "" || info.ModTime().After(recentTime) {
			recentPlan = plan
			recentTime = info.ModTime()
		}
	}

	return recentPlan
}

// ExtractBranchName derives a branch name from a plan file path.
// removes the .md extension and strips any leading date prefix (e.g., "2024-01-15-").
func ExtractBranchName(planFile string) string {
	name := strings.TrimSuffix(filepath.Base(planFile), ".md")
	branchName := strings.TrimLeft(datePrefixRe.ReplaceAllString(name, ""), "-")
	if branchName == "" {
		return name
	}
	return branchName
}

// PromptDescription prompts the user to enter a plan description.
// returns empty string if user cancels (Ctrl+C or Ctrl+D).
func PromptDescription(ctx context.Context, r io.Reader, colors *progress.Colors) string {
	colors.Info().Printf("no plans found. what would you like to implement?\n")
	colors.Info().Printf("(enter description or press Ctrl+C/Ctrl+D to cancel): ")

	reader := bufio.NewReader(r)
	line, err := input.ReadLineWithContext(ctx, reader)
	if err != nil {
		// EOF (Ctrl+D) is graceful cancel
		return ""
	}

	return strings.TrimSpace(line)
}
