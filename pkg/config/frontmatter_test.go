package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseOptions(t *testing.T) {
	tests := []struct {
		name  string
		input string
		opts  Options
		body  string
	}{
		{"no frontmatter", "just a prompt", Options{}, "just a prompt"},
		{"model only", "---\nmodel: claude-haiku-4-5\n---\nthe prompt", Options{Model: "claude-haiku-4-5"}, "the prompt"},
		{"agent only", "---\nagent: code-reviewer\n---\nthe prompt", Options{AgentType: "code-reviewer"}, "the prompt"},
		{"both fields", "---\nmodel: claude-sonnet-4-6\nagent: code-reviewer\n---\nthe prompt", Options{Model: "claude-sonnet-4-6", AgentType: "code-reviewer"}, "the prompt"},
		{"unclosed frontmatter", "---\nmodel: claude-haiku-4-5\nno closing", Options{}, "---\nmodel: claude-haiku-4-5\nno closing"},
		{"empty body after frontmatter", "---\nmodel: claude-haiku-4-5\n---\n", Options{Model: "claude-haiku-4-5"}, ""},
		{"unknown keys ignored", "---\nmodel: claude-opus-4-6\nfoo: bar\n---\nbody", Options{Model: "claude-opus-4-6"}, "body"},
		{"whitespace in values", "---\nmodel:  claude-haiku-4-5  \nagent:  code-reviewer  \n---\nbody", Options{Model: "claude-haiku-4-5", AgentType: "code-reviewer"}, "body"},
		{"malformed yaml", "---\n: :\n  bad:\n---\nbody", Options{}, "---\n: :\n  bad:\n---\nbody"},

		// closing delimiter must be on its own line
		{"closing delimiter not on own line", "---\nmodel: claude-haiku-4-5\n---extra\nbody", Options{}, "---\nmodel: claude-haiku-4-5\n---extra\nbody"},
		{"closing delimiter with trailing text", "---\nmodel: claude-haiku-4-5\n--- body", Options{}, "---\nmodel: claude-haiku-4-5\n--- body"},

		// empty and minimal frontmatter
		{"empty frontmatter block", "---\n---\nbody", Options{}, "---\n---\nbody"},
		{"frontmatter only no trailing newline", "---\nmodel: claude-haiku-4-5\n---", Options{Model: "claude-haiku-4-5"}, ""},

		// full model IDs preserved as-is (no normalization)
		{"full model id claude-opus-4-6", "---\nmodel: claude-opus-4-6\n---\nbody", Options{Model: "claude-opus-4-6"}, "body"},
		{"full model id claude-sonnet-4-6", "---\nmodel: claude-sonnet-4-6\n---\nbody", Options{Model: "claude-sonnet-4-6"}, "body"},
		{"full model id claude-haiku-4-5", "---\nmodel: claude-haiku-4-5\n---\nbody", Options{Model: "claude-haiku-4-5"}, "body"},
		{"full model id gpt-5.2-codex", "---\nmodel: gpt-5.2-codex\n---\nbody", Options{Model: "gpt-5.2-codex"}, "body"},
		{"old short keyword kept as-is", "---\nmodel: sonnet\n---\nbody", Options{Model: "sonnet"}, "body"},
		{"unknown model kept as-is", "---\nmodel: gpt-5\n---\nbody", Options{Model: "gpt-5"}, "body"},

		{"yaml type mismatch model number", "---\nmodel: 123\n---\nbody", Options{Model: "123"}, "body"},
		{"yaml null value", "---\nmodel: null\n---\nbody", Options{}, "body"},
		{"duplicate keys rejected", "---\nmodel: claude-haiku-4-5\nmodel: claude-opus-4-6\n---\nbody", Options{}, "---\nmodel: claude-haiku-4-5\nmodel: claude-opus-4-6\n---\nbody"},

		// body with dashes
		{"body contains triple dashes", "---\nmodel: claude-haiku-4-5\n---\nsome text\n---\nmore text", Options{Model: "claude-haiku-4-5"}, "some text\n---\nmore text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, body := parseOptions(tt.input)
			assert.Equal(t, tt.opts, opts)
			assert.Equal(t, tt.body, body)
		})
	}
}

func TestOptions_String(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		want string
	}{
		{"empty options", Options{}, "model=default, subagent=general-purpose"},
		{"model only", Options{Model: "claude-haiku-4-5"}, "model=claude-haiku-4-5, subagent=general-purpose"},
		{"agent only", Options{AgentType: "code-reviewer"}, "model=default, subagent=code-reviewer"},
		{"both fields", Options{Model: "claude-opus-4-6", AgentType: "code-reviewer"}, "model=claude-opus-4-6, subagent=code-reviewer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.opts.String())
		})
	}
}

func TestOptions_Validate(t *testing.T) {
	tests := []struct {
		name     string
		opts     Options
		warnings []string
	}{
		{"empty options", Options{}, nil},
		{"valid model claude-opus-4-6", Options{Model: "claude-opus-4-6"}, nil},
		{"valid model claude-sonnet-4-6", Options{Model: "claude-sonnet-4-6"}, nil},
		{"valid model claude-haiku-4-5", Options{Model: "claude-haiku-4-5"}, nil},
		{"valid model gpt-5.2-codex", Options{Model: "gpt-5.2-codex"}, nil},
		{"old short keyword rejected", Options{Model: "sonnet"}, []string{`unknown model "sonnet", must be one of: claude-opus-4-6, claude-sonnet-4-6, claude-haiku-4-5, gpt-5.2-codex`}},
		{"unknown model", Options{Model: "gpt-5"}, []string{`unknown model "gpt-5", must be one of: claude-opus-4-6, claude-sonnet-4-6, claude-haiku-4-5, gpt-5.2-codex`}},
		{"agent type not validated", Options{AgentType: "anything-goes"}, nil},
		{"unknown model with agent", Options{Model: "bad", AgentType: "reviewer"}, []string{`unknown model "bad", must be one of: claude-opus-4-6, claude-sonnet-4-6, claude-haiku-4-5, gpt-5.2-codex`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.warnings, tt.opts.Validate())
		})
	}
}
