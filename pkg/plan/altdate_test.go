package plan

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAltDateBasename(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"dashed to compact", "2026-05-12-foo.md", "20260512-foo.md"},
		{"compact to dashed", "20260512-foo.md", "2026-05-12-foo.md"},
		{"dashed multi-part slug", "2026-05-12-extract-env-variable.md", "20260512-extract-env-variable.md"},
		{"compact multi-part slug", "20260512-extract-env-variable.md", "2026-05-12-extract-env-variable.md"},
		{"non-date basename returns empty", "feature-x.md", ""},
		{"missing .md extension returns empty", "2026-05-12-foo", ""},
		{"empty string returns empty", "", ""},
		{"non-md extension returns empty", "2026-05-12-foo.txt", ""},
		{"loose 8-digit prefix still swapped (no date validation)", "12345678-foo.md", "1234-56-78-foo.md"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, AltDateBasename(tt.in))
		})
	}
}
