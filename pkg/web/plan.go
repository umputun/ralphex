package web

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/umputun/ralphex/pkg/plan"
)

// loadPlanWithFallback loads a plan from disk with completed/ directory fallback.
// does not cache - each call reads from disk.
func loadPlanWithFallback(path string) (*plan.Plan, error) {
	p, err := plan.ParsePlanFile(path)
	if err != nil && errors.Is(err, fs.ErrNotExist) {
		completedPath := filepath.Join(filepath.Dir(path), "completed", filepath.Base(path))
		p, err = plan.ParsePlanFile(completedPath)
	}
	if err != nil {
		return nil, fmt.Errorf("load plan with fallback: %w", err)
	}
	return p, nil
}
