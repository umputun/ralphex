// Package render provides terminal rendering utilities.
package render

import (
	"fmt"

	"github.com/charmbracelet/glamour"
)

// RenderMarkdown renders markdown content for terminal display.
// If noColor is true, returns the content unchanged.
// Otherwise, uses glamour to render with auto-detected style and word wrap.
func RenderMarkdown(content string, noColor bool) (string, error) {
	if noColor {
		return content, nil
	}

	renderer, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(80),
	)
	if err != nil {
		return "", fmt.Errorf("create renderer: %w", err)
	}

	result, err := renderer.Render(content)
	if err != nil {
		return "", fmt.Errorf("render markdown: %w", err)
	}

	return result, nil
}
