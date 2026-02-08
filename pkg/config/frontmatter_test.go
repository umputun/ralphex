package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseFrontmatter(t *testing.T) {
	tests := []struct {
		name  string
		input string
		model string
		agent string
		body  string
	}{
		{"no frontmatter", "just a prompt", "", "", "just a prompt"},
		{"model only", "---\nmodel: haiku\n---\nthe prompt", "haiku", "", "the prompt"},
		{"agent only", "---\nagent: code-reviewer\n---\nthe prompt", "", "code-reviewer", "the prompt"},
		{"both fields", "---\nmodel: sonnet\nagent: code-reviewer\n---\nthe prompt", "sonnet", "code-reviewer", "the prompt"},
		{"unclosed frontmatter", "---\nmodel: haiku\nno closing", "", "", "---\nmodel: haiku\nno closing"},
		{"empty body after frontmatter", "---\nmodel: haiku\n---\n", "haiku", "", ""},
		{"unknown keys ignored", "---\nmodel: opus\nfoo: bar\n---\nbody", "opus", "", "body"},
		{"whitespace in values", "---\nmodel:  haiku  \nagent:  code-reviewer  \n---\nbody", "haiku", "code-reviewer", "body"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model, agent, body := parseFrontmatter(tt.input)
			assert.Equal(t, tt.model, model)
			assert.Equal(t, tt.agent, agent)
			assert.Equal(t, tt.body, body)
		})
	}
}
