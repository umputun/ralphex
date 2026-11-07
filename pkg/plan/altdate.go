package plan

import (
	"path/filepath"
	"regexp"
)

var (
	dashedDatePattern  = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})-(.+\.md)$`)
	compactDatePattern = regexp.MustCompile(`^(\d{8})-(.+\.md)$`)
)

// AltDateBasename returns the basename with the date prefix swapped between
// dashed (YYYY-MM-DD) and compact (YYYYMMDD) conventions, or "" if name
// matches neither pattern. Pure string transformation, no I/O.
func AltDateBasename(name string) string {
	if m := dashedDatePattern.FindStringSubmatch(name); m != nil {
		return m[1] + m[2] + m[3] + "-" + m[4]
	}
	if m := compactDatePattern.FindStringSubmatch(name); m != nil {
		d := m[1]
		return d[0:4] + "-" + d[4:6] + "-" + d[6:8] + "-" + m[2]
	}
	return ""
}

// CandidatePaths returns the paths a plan may live at after an in-place date rename or an archive
// move, in probe order: path itself, its alternate-date sibling, then completed/ with each
// basename. The live rename precedes completed/ so a stale archived copy never shadows it.
func CandidatePaths(path string) []string {
	dir, base := filepath.Dir(path), filepath.Base(path)
	altBase := AltDateBasename(base)
	if altBase == "" {
		return []string{path, filepath.Join(dir, "completed", base)}
	}
	return []string{path, filepath.Join(dir, altBase), filepath.Join(dir, "completed", base),
		filepath.Join(dir, "completed", altBase)}
}
