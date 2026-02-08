package web

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseDiffStats(t *testing.T) {
	t.Run("parses valid diff stats line", func(t *testing.T) {
		stats, ok := parseDiffStats("DIFFSTATS: files=12 additions=340 deletions=120")
		assert.True(t, ok)
		assert.Equal(t, 12, stats.Files)
		assert.Equal(t, 340, stats.Additions)
		assert.Equal(t, 120, stats.Deletions)
	})

	t.Run("parses zero values", func(t *testing.T) {
		stats, ok := parseDiffStats("DIFFSTATS: files=0 additions=0 deletions=0")
		assert.True(t, ok)
		assert.Equal(t, 0, stats.Files)
		assert.Equal(t, 0, stats.Additions)
		assert.Equal(t, 0, stats.Deletions)
	})

	t.Run("rejects empty string", func(t *testing.T) {
		_, ok := parseDiffStats("")
		assert.False(t, ok)
	})

	t.Run("rejects regular log line", func(t *testing.T) {
		_, ok := parseDiffStats("some output line")
		assert.False(t, ok)
	})

	t.Run("rejects invalid diff stats line", func(t *testing.T) {
		_, ok := parseDiffStats("DIFFSTATS: files=foo additions=1 deletions=2")
		assert.False(t, ok)
	})
}
