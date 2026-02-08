package web

import (
	"regexp"
	"strconv"
)

// DiffStats holds git diff statistics for a session.
type DiffStats struct {
	Files     int `json:"files"`
	Additions int `json:"additions"`
	Deletions int `json:"deletions"`
}

var diffStatsPattern = regexp.MustCompile(`^DIFFSTATS:\s*files=(\d+)\s+additions=(\d+)\s+deletions=(\d+)\s*$`)

func parseDiffStats(text string) (DiffStats, bool) {
	matches := diffStatsPattern.FindStringSubmatch(text)
	if matches == nil {
		return DiffStats{}, false
	}

	files, err := strconv.Atoi(matches[1])
	if err != nil {
		return DiffStats{}, false
	}
	additions, err := strconv.Atoi(matches[2])
	if err != nil {
		return DiffStats{}, false
	}
	deletions, err := strconv.Atoi(matches[3])
	if err != nil {
		return DiffStats{}, false
	}

	return DiffStats{
		Files:     files,
		Additions: additions,
		Deletions: deletions,
	}, true
}
