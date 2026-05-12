package web

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"

	"github.com/umputun/ralphex/pkg/plan"
)

// dashedDatePattern matches YYYY-MM-DD-<rest>.md basenames (dashed convention).
// intentionally duplicated from pkg/processor/prompts.go and pkg/git/service.go:
// pkg/web is a leaf consumer and importing higher-level packages just to share a
// ~10-line helper would invert the dependency direction.
var dashedDatePattern = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})-(.+\.md)$`)

// compactDatePattern matches YYYYMMDD-<rest>.md basenames (compact convention).
var compactDatePattern = regexp.MustCompile(`^(\d{8})-(.+\.md)$`)

// altDateFormatBasename returns the same basename with the date prefix swapped
// between dashed (YYYY-MM-DD) and compact (YYYYMMDD) conventions, or "" if name
// matches neither pattern.
func altDateFormatBasename(name string) string {
	if m := dashedDatePattern.FindStringSubmatch(name); m != nil {
		return m[1] + m[2] + m[3] + "-" + m[4]
	}
	if m := compactDatePattern.FindStringSubmatch(name); m != nil {
		d := m[1]
		return d[0:4] + "-" + d[4:6] + "-" + d[6:8] + "-" + m[2]
	}
	return ""
}

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
	completedPath := filepath.Join(dir, "completed", base)
	if p, err := plan.ParsePlanFile(completedPath); err == nil {
		return p, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("load plan with fallback: %w", err)
	}

	if altBase := altDateFormatBasename(base); altBase != "" {
		altCompletedPath := filepath.Join(dir, "completed", altBase)
		if p, err := plan.ParsePlanFile(altCompletedPath); err == nil {
			return p, nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("load plan with fallback: %w", err)
		}
	}

	return nil, fmt.Errorf("load plan with fallback: %w", fs.ErrNotExist)
}
