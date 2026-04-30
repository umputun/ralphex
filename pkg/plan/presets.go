package plan

import (
	"fmt"
	"regexp"
)

// headerPresets maps preset names to their compiled regex patterns.
// Capture group 1 = task id, capture group 2 = title (optional).
var headerPresets = map[string]string{
	"default": `^### (?:Task|Iteration) ([^:]+?):\s*(.*)$`,
	"openspec": `^## (\d+)\.?\s*(.*)$`,
}

// headerPresetDescriptions maps preset names to human-readable format examples
// used in the {{TASK_HEADER_PATTERNS}} prompt hint.
var headerPresetDescriptions = map[string]string{
	"default":  "### Task N: title  or  ### Iteration N: title",
	"openspec": "## N. title",
}

// ResolveHeaderPattern resolves a single pattern string: if it matches a known
// preset name, the preset's regex is compiled and returned; otherwise the string
// is compiled directly as a raw regex. raw regexes must have at least one capture
// group for the task ID.
func ResolveHeaderPattern(s string) (*regexp.Regexp, error) {
	orig := s
	_, isPreset := headerPresets[s]
	if isPreset {
		s = headerPresets[s]
	}
	re, err := regexp.Compile(s)
	if err != nil {
		return nil, fmt.Errorf("compile task header pattern %q: %w", orig, err)
	}
	if !isPreset && re.NumSubexp() < 1 {
		return nil, fmt.Errorf("task header pattern %q requires at least one capture group for task ID", orig)
	}
	return re, nil
}

// ResolveHeaderPatterns resolves a slice of pattern strings, returning compiled
// regexes. The first error encountered terminates resolution.
func ResolveHeaderPatterns(patterns []string) ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := ResolveHeaderPattern(p)
		if err != nil {
			return nil, err
		}
		out = append(out, re)
	}
	return out, nil
}

// DefaultHeaderPatterns returns the compiled default task header patterns.
// Panics if the built-in regex is invalid (programming error, not user error).
func DefaultHeaderPatterns() []*regexp.Regexp {
	re, err := ResolveHeaderPatterns([]string{"default"})
	if err != nil {
		panic("default header pattern failed to compile (" + headerPresets["default"] + "): " + err.Error())
	}
	return re
}

// PresetDescription returns a human-readable description of the pattern for use
// in LLM prompts. For known preset names it returns a format example; for raw
// regex strings it returns the regex itself.
func PresetDescription(s string) string {
	if desc, ok := headerPresetDescriptions[s]; ok {
		return desc
	}
	return s
}
