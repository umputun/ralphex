package processor

import (
	"os"

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

	// only a missing plan falls back; a permission or other error keeps the configured path.
	if _, err := os.Stat(l.cfg.PlanFile); !os.IsNotExist(err) {
		return l.cfg.PlanFile
	}

	for _, path := range plan.CandidatePaths(l.cfg.PlanFile)[1:] {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	return l.cfg.PlanFile
}
