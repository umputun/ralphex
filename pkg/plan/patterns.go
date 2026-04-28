package plan

import (
	"fmt"
	"regexp"
	"strings"
)

// DefaultTaskHeaderPatterns lists the built-in templates used when the user
// has not configured task_header_patterns. These compile to a regex semantically
// equivalent to the legacy hard-coded pattern
// `^###\s+(?:Task|Iteration)\s+([^:]+?):\s*(.*)$`.
var DefaultTaskHeaderPatterns = []string{
	"### Task {N}: {title}",
	"### Iteration {N}: {title}",
}

// placeholder replacements used when building a regex from a template.
// {N} is required (exactly once), {title} is optional (at most once, must come
// after {N}). Any other {...} token produces an error.
const (
	placeholderNRegex = `([^\s:]+?)`
	placeholderTRegex = `(.*?)`
)

// placeholderPattern finds {name} tokens where name is letters only.
var placeholderPattern = regexp.MustCompile(`\{([A-Za-z]+)\}`)

// isSpace reports whether r is ASCII space or tab (matching regexp \s horizontal chars).
func isSpace(r rune) bool {
	return r == ' ' || r == '\t'
}

// CompileTaskHeaderPattern turns a template string like "### Task {N}: {title}"
// into an anchored *regexp.Regexp. The returned regex captures the task
// identifier as group 1 and, if {title} appears in the template, the title as
// group 2. The whole line is anchored ^...\s*$.
//
// Errors are returned for:
//   - missing {N}
//   - {N} appearing more than once
//   - {title} appearing before {N}
//   - {title} appearing more than once
//   - any other {something} placeholder
func CompileTaskHeaderPattern(template string) (*regexp.Regexp, error) {
	// validate and collect placeholder positions in order
	var sawN, sawTitle bool
	locs := placeholderPattern.FindAllStringSubmatchIndex(template, -1)
	for _, l := range locs {
		name := template[l[2]:l[3]]
		switch name {
		case "N":
			if sawN {
				return nil, fmt.Errorf("template %q: {N} may only appear once", template)
			}
			sawN = true
		case "title":
			if !sawN {
				return nil, fmt.Errorf("template %q: {title} must come after {N}", template)
			}
			if sawTitle {
				return nil, fmt.Errorf("template %q: {title} may only appear once", template)
			}
			sawTitle = true
		default:
			return nil, fmt.Errorf("template %q: unknown placeholder {%s}", template, name)
		}
	}
	if !sawN {
		return nil, fmt.Errorf("template %q: missing required {N} placeholder", template)
	}

	// walk the template, quoting literal segments and substituting placeholders.
	// whitespace immediately preceding {title} is rendered as \s* so that a template
	// like "### Task {N}: {title}" also matches "### Task 1:" (no trailing title).
	var b strings.Builder
	b.WriteString("^")
	idx := 0
	for _, l := range locs {
		start, end := l[0], l[1]
		name := template[l[2]:l[3]]
		lit := template[idx:start]
		if name == "title" {
			// split literal into (prefix, trailing-whitespace) and render whitespace as \s*
			trim := strings.TrimRightFunc(lit, isSpace)
			if trim != "" {
				b.WriteString(regexp.QuoteMeta(trim))
			}
			if len(lit) > len(trim) {
				b.WriteString(`\s*`)
			}
		} else if lit != "" {
			b.WriteString(regexp.QuoteMeta(lit))
		}
		switch name {
		case "N":
			b.WriteString(placeholderNRegex)
		case "title":
			b.WriteString(placeholderTRegex)
		}
		idx = end
	}
	if idx < len(template) {
		b.WriteString(regexp.QuoteMeta(template[idx:]))
	}
	b.WriteString(`\s*$`)

	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil, fmt.Errorf("template %q: compile regex: %w", template, err)
	}
	return re, nil
}

// CompileTaskHeaderPatterns compiles a list of templates. If templates is nil
// or empty, it compiles DefaultTaskHeaderPatterns instead. Any compile failure
// aborts the call with an error that names the offending template.
func CompileTaskHeaderPatterns(templates []string) ([]*regexp.Regexp, error) {
	if len(templates) == 0 {
		templates = DefaultTaskHeaderPatterns
	}
	out := make([]*regexp.Regexp, 0, len(templates))
	for _, t := range templates {
		re, err := CompileTaskHeaderPattern(t)
		if err != nil {
			return nil, err
		}
		out = append(out, re)
	}
	return out, nil
}
