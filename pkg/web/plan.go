package web

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"

	"github.com/umputun/ralphex/pkg/plan"
)

// loadPlanWithFallback loads a plan from disk with completed/ directory fallback.
// does not cache - each call reads from disk.
// patterns forwards the configured task_header_patterns to the plan parser so the
// dashboard recognizes the same task sections as the executor.
func loadPlanWithFallback(path string, patterns ...string) (*plan.Plan, error) {
	p, err := plan.ParsePlanFile(path, patterns...)
	if err != nil && errors.Is(err, fs.ErrNotExist) {
		completedPath := filepath.Join(filepath.Dir(path), "completed", filepath.Base(path))
		p, err = plan.ParsePlanFile(completedPath, patterns...)
	}
	if err != nil {
		return nil, fmt.Errorf("load plan with fallback: %w", err)
	}
	return p, nil
}

// loadSessionPlanWithFallback loads a plan on behalf of a watched multi-session
// dashboard. watched sessions may come from other repos that use the default
// `### Task`/`### Iteration` headers even when the dashboard process itself is
// configured with custom task_header_patterns, so default patterns are appended
// to the configured list to ensure those default-format plans still parse. when
// no custom patterns are configured this is a no-op: plan.ParsePlan falls back
// to DefaultTaskHeaderPatterns on an empty patterns list.
func loadSessionPlanWithFallback(path string, patterns []string) (*plan.Plan, error) {
	merged := patterns
	if len(patterns) > 0 {
		merged = make([]string, 0, len(patterns)+len(plan.DefaultTaskHeaderPatterns))
		merged = append(merged, patterns...)
		for _, d := range plan.DefaultTaskHeaderPatterns {
			if !slices.Contains(merged, d) {
				merged = append(merged, d)
			}
		}
	}
	return loadPlanWithFallback(path, merged...)
}
