package plan

import "regexp"

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
