package render

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderMarkdown(t *testing.T) {
	t.Run("with color enabled renders markdown", func(t *testing.T) {
		content := "# Heading\n\nSome **bold** text."
		result, err := RenderMarkdown(content, false)
		require.NoError(t, err)
		// glamour transforms markdown - should not be identical to input
		assert.NotEqual(t, content, result)
		// should contain the text content
		assert.Contains(t, result, "Heading")
		assert.Contains(t, result, "bold")
	})

	t.Run("with noColor returns plain content", func(t *testing.T) {
		content := "# Heading\n\nSome **bold** text."
		result, err := RenderMarkdown(content, true)
		require.NoError(t, err)
		assert.Equal(t, content, result)
	})

	t.Run("handles empty content", func(t *testing.T) {
		result, err := RenderMarkdown("", false)
		require.NoError(t, err)
		// glamour may add trailing whitespace for empty content
		assert.Empty(t, strings.TrimSpace(result))
	})

	t.Run("handles empty content with noColor", func(t *testing.T) {
		result, err := RenderMarkdown("", true)
		require.NoError(t, err)
		assert.Empty(t, result)
	})

	t.Run("handles code blocks", func(t *testing.T) {
		content := "```go\nfunc main() {}\n```"
		result, err := RenderMarkdown(content, false)
		require.NoError(t, err)
		assert.Contains(t, result, "func")
		assert.Contains(t, result, "main")
	})

	t.Run("handles lists", func(t *testing.T) {
		content := "- item 1\n- item 2\n- item 3"
		result, err := RenderMarkdown(content, false)
		require.NoError(t, err)
		assert.Contains(t, result, "item 1")
		assert.Contains(t, result, "item 2")
		assert.Contains(t, result, "item 3")
	})
}
