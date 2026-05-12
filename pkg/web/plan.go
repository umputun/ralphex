package web

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/umputun/ralphex/pkg/plan"
)

// loadPlanWithFallback loads a plan from disk with completed/ directory fallback.
// probes the original path, then completed/<basename>, then completed/<alt-basename>
// with the date prefix swapped between YYYY-MM-DD and YYYYMMDD conventions to handle
// plans renamed by an LLM-driven git mv.
// does not cache - each call reads from disk.
func loadPlanWithFallback(path string) (*plan.Plan, error) {
	p, err := plan.ParsePlanFile(path)
	if err == nil {
		return p, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("load plan with fallback: %w", err)
	}

	dir := filepath.Dir(path)
	base := filepath.Base(path)
	attempted := []string{path}

	completedPath := filepath.Join(dir, "completed", base)
	attempted = append(attempted, completedPath)
	if p, err := plan.ParsePlanFile(completedPath); err == nil {
		return p, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("load plan with fallback: %w", err)
	}

	if altBase := plan.AltDateBasename(base); altBase != "" {
		altCompletedPath := filepath.Join(dir, "completed", altBase)
		attempted = append(attempted, altCompletedPath)
		if p, err := plan.ParsePlanFile(altCompletedPath); err == nil {
			return p, nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("load plan with fallback: %w", err)
		}
	}

	return nil, fmt.Errorf("load plan with fallback: plan not found, attempted %v: %w", attempted, fs.ErrNotExist)
}
