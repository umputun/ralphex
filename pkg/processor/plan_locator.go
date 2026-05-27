package processor

import (
	"os"
	"path/filepath"

	"github.com/umputun/ralphex/pkg/plan"
)

type planLocator struct {
	cfg Config
}

func newPlanLocator(cfg Config) *planLocator {
	return &planLocator{cfg: cfg}
}

func (l *planLocator) Path() string {
	if l.cfg.PlanFile == "" {
		return ""
	}

	_, err := os.Stat(l.cfg.PlanFile)
	if err == nil {
		return l.cfg.PlanFile
	}
	if !os.IsNotExist(err) {
		return l.cfg.PlanFile
	}

	dir := filepath.Dir(l.cfg.PlanFile)
	base := filepath.Base(l.cfg.PlanFile)
	altBase := plan.AltDateBasename(base)

	if altBase != "" {
		path := filepath.Join(dir, altBase)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	completedPath := filepath.Join(dir, "completed", base)
	if _, err := os.Stat(completedPath); err == nil {
		return completedPath
	}

	if altBase != "" {
		path := filepath.Join(dir, "completed", altBase)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	return l.cfg.PlanFile
}
