package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Options holds agent options parsed from YAML frontmatter in agent files.
type Options struct {
	Model     string `yaml:"model"`
	AgentType string `yaml:"agent"`
}

var validModels = map[string]bool{"haiku": true, "sonnet": true, "opus": true}

// String returns a human-readable summary of the options for logging.
func (o Options) String() string {
	model := o.Model
	if model == "" {
		model = "default"
	}
	subagent := o.AgentType
	if subagent == "" {
		subagent = "general-purpose"
	}
	return fmt.Sprintf("model=%s, subagent=%s", model, subagent)
}

// Validate returns warnings for invalid option values.
// called after parseOptions which normalizes model to keyword form.
func (o Options) Validate() []string {
	var warnings []string
	if o.Model != "" && !validModels[o.Model] {
		warnings = append(warnings, fmt.Sprintf("unknown model %q, must be one of: haiku, sonnet, opus", o.Model))
	}
	return warnings
}

// normalizeModel extracts the keyword (haiku, sonnet, opus) from a model string.
// e.g. "claude-sonnet-4-5-20250929" → "sonnet", "opus" → "opus", "" → "".
func normalizeModel(model string) string {
	lower := strings.ToLower(model)
	for kw := range validModels {
		if strings.Contains(lower, kw) {
			return kw
		}
	}
	return model // return as-is if no keyword found (Validate will catch it)
}

// parseOptions extracts agent options from YAML frontmatter delimited by "---".
// we only support YAML with "---" delimiters because agent files are our own format —
// no need for TOML/JSON/multi-format support that libraries like adrg/frontmatter provide.
// CutPrefix + Cut handle delimiter splitting without index arithmetic.
// returns parsed options and body. if no frontmatter, returns zero value and original content.
func parseOptions(content string) (Options, string) {
	after, found := strings.CutPrefix(content, "---\n")
	if !found {
		return Options{}, content
	}

	header, body, found := strings.Cut(after, "\n---")
	if !found {
		return Options{}, content
	}
	// closing delimiter must be on its own line
	if body != "" && body[0] != '\n' {
		return Options{}, content
	}

	var opts Options
	if err := yaml.Unmarshal([]byte(header), &opts); err != nil {
		return Options{}, content // malformed YAML → treat as no frontmatter
	}

	opts.Model = normalizeModel(opts.Model)

	return opts, strings.TrimSpace(body)
}
