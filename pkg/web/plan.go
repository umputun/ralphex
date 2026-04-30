package web

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"

	"github.com/umputun/ralphex/pkg/plan"
)

// loadPlanWithFallback loads a plan from disk with completed/ directory fallback.
// does not cache - each call reads from disk.
// patterns forwards the configured task_header_patterns to the plan parser so the
// dashboard recognizes the same task sections as the executor.
func loadPlanWithFallback(path string, patterns []*regexp.Regexp) (*plan.Plan, error) {
	p, err := plan.ParsePlanFile(path, patterns)
	if err != nil && errors.Is(err, fs.ErrNotExist) {
		completedPath := filepath.Join(filepath.Dir(path), "completed", filepath.Base(path))
		p, err = plan.ParsePlanFile(completedPath, patterns)
	}
	if err != nil {
		return nil, fmt.Errorf("load plan with fallback: %w", err)
	}
	return p, nil
}
