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
		{"model only", "---\nmodel: haiku\n---\nthe prompt", Options{Model: "haiku"}, "the prompt"},
		{"agent only", "---\nagent: code-reviewer\n---\nthe prompt", Options{AgentType: "code-reviewer"}, "the prompt"},
		{"both fields", "---\nmodel: sonnet\nagent: code-reviewer\n---\nthe prompt", Options{Model: "sonnet", AgentType: "code-reviewer"}, "the prompt"},
		{"unclosed frontmatter", "---\nmodel: haiku\nno closing", Options{}, "---\nmodel: haiku\nno closing"},
		{"empty body after frontmatter", "---\nmodel: haiku\n---\n", Options{Model: "haiku"}, ""},
		{"unknown keys ignored", "---\nmodel: opus\nfoo: bar\n---\nbody", Options{Model: "opus"}, "body"},
		{"whitespace in values", "---\nmodel:  haiku  \nagent:  code-reviewer  \n---\nbody", Options{Model: "haiku", AgentType: "code-reviewer"}, "body"},
		{"malformed yaml", "---\n: :\n  bad:\n---\nbody", Options{}, "---\n: :\n  bad:\n---\nbody"},

		// closing delimiter must be on its own line
		{"closing delimiter not on own line", "---\nmodel: haiku\n---extra\nbody", Options{}, "---\nmodel: haiku\n---extra\nbody"},
		{"closing delimiter with trailing text", "---\nmodel: haiku\n--- body", Options{}, "---\nmodel: haiku\n--- body"},

		// empty and minimal frontmatter
		{"empty frontmatter block", "---\n---\nbody", Options{}, "---\n---\nbody"},
		{"frontmatter only no trailing newline", "---\nmodel: haiku\n---", Options{Model: "haiku"}, ""},

		// yaml edge cases
		// model normalization
		{"full model id normalized", "---\nmodel: claude-sonnet-4-5-20250929\n---\nbody", Options{Model: "sonnet"}, "body"},
		{"full model id haiku normalized", "---\nmodel: claude-haiku-4-5-20251001\n---\nbody", Options{Model: "haiku"}, "body"},
		{"full model id opus normalized", "---\nmodel: claude-opus-4-6\n---\nbody", Options{Model: "opus"}, "body"},
		{"model keyword preserved", "---\nmodel: sonnet\n---\nbody", Options{Model: "sonnet"}, "body"},
		{"unknown model kept as-is", "---\nmodel: gpt-5\n---\nbody", Options{Model: "gpt-5"}, "body"},

		{"yaml type mismatch model number", "---\nmodel: 123\n---\nbody", Options{Model: "123"}, "body"},
		{"yaml null value", "---\nmodel: null\n---\nbody", Options{}, "body"},
		{"duplicate keys rejected", "---\nmodel: haiku\nmodel: opus\n---\nbody", Options{}, "---\nmodel: haiku\nmodel: opus\n---\nbody"},

		// body with dashes
		{"body contains triple dashes", "---\nmodel: haiku\n---\nsome text\n---\nmore text", Options{Model: "haiku"}, "some text\n---\nmore text"},
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
		{"model only", Options{Model: "haiku"}, "model=haiku, subagent=general-purpose"},
		{"agent only", Options{AgentType: "code-reviewer"}, "model=default, subagent=code-reviewer"},
		{"both fields", Options{Model: "opus", AgentType: "code-reviewer"}, "model=opus, subagent=code-reviewer"},
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
		{"valid model haiku", Options{Model: "haiku"}, nil},
		{"valid model sonnet", Options{Model: "sonnet"}, nil},
		{"valid model opus", Options{Model: "opus"}, nil},
		{"unknown model", Options{Model: "gpt-5"}, []string{`unknown model "gpt-5", must be one of: haiku, sonnet, opus`}},
		{"agent type not validated", Options{AgentType: "anything-goes"}, nil},
		{"unknown model with agent", Options{Model: "bad", AgentType: "reviewer"}, []string{`unknown model "bad", must be one of: haiku, sonnet, opus`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.warnings, tt.opts.Validate())
		})
	}
}
