package web

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/umputun/ralphex/pkg/plan"
)

// loadSessionPlan loads a plan recorded in a session's progress header. relative paths are
// resolved against sessionDir, the directory holding the progress file.
func loadSessionPlan(planPath, sessionDir string) (*plan.Plan, error) {
	if !filepath.IsAbs(planPath) {
		planPath = filepath.Join(sessionDir, planPath)
	}
	return loadPlanWithFallback(planPath)
}

// loadPlanWithFallback loads the first readable plan among plan.CandidatePaths(path), the same
// probe order the processor's plan locator uses. an error other than not-found stops the probe,
// so an unreadable live plan is reported instead of being hidden behind an archived copy.
// does not cache - each call reads from disk.
func loadPlanWithFallback(path string) (*plan.Plan, error) {
	candidates := plan.CandidatePaths(path)
	for _, c := range candidates {
		p, err := plan.ParsePlanFile(c)
		if err == nil {
			return p, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("load plan with fallback: %w", err)
		}
	}
	return nil, fmt.Errorf("load plan with fallback: plan not found, attempted %v: %w", candidates, fs.ErrNotExist)
}
