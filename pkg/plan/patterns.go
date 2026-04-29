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
//
// {N} is compiled as `([^:]+?)\s*` to preserve the legacy hard-coded regex
// behavior `^###\s+(?:Task|Iteration)\s+([^:]+?):\s*(.*)$`, which accepted
// whitespace inside the captured identifier (e.g. `### Task Phase 1: Foo`
// with id `Phase 1`). The trailing `\s*` absorbs whitespace between the id
// and the next literal so captures don't carry trailing spaces, also
// tolerating variants like `### Task 1 : Foo`.
const (
	placeholderNRegex = `([^:]+?)\s*`
	placeholderTRegex = `(.*?)`
)

// placeholderPattern matches any `{...}` token where the inner text has no braces
// or whitespace. It intentionally accepts malformed names (e.g. `{N1}`, `{title_2}`,
// `{}`) so CompileTaskHeaderPattern can reject them with an explicit "unknown
// placeholder" error rather than silently treating the token as a literal and
// failing later with "no executable task sections".
var placeholderPattern = regexp.MustCompile(`\{([^{}\s]*)\}`)

// isSpace reports whether r is ASCII space or tab (matching regexp \s horizontal chars).
func isSpace(r rune) bool {
	return r == ' ' || r == '\t'
}

// quoteLiteralFlexibleSpaces regex-quotes a literal template segment and then
// replaces every run of ASCII space/tab characters with \s+ so that header
// matching tolerates multiple spaces or tabs where the template has a single
// space. This keeps behavior compatible with the legacy hard-coded regex,
// which used \s+ between tokens.
func quoteLiteralFlexibleSpaces(lit string) string {
	if lit == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(lit))
	i := 0
	for i < len(lit) {
		c := lit[i]
		if c == ' ' || c == '\t' {
			// collapse run of spaces/tabs into a single \s+
			j := i
			for j < len(lit) && (lit[j] == ' ' || lit[j] == '\t') {
				j++
			}
			b.WriteString(`\s+`)
			i = j
			continue
		}
		// find next whitespace and quote the intervening chunk
		j := i
		for j < len(lit) && lit[j] != ' ' && lit[j] != '\t' {
			j++
		}
		b.WriteString(regexp.QuoteMeta(lit[i:j]))
		i = j
	}
	return b.String()
}

// checkUnbalancedBraces returns an error if template contains a `{` or `}` that
// isn't part of a placeholder span in locs. Placeholder locs come from
// placeholderPattern.FindAllStringSubmatchIndex, where each entry's [0:2] marks
// the full `{name}` span in the template.
func checkUnbalancedBraces(template string, locs [][]int) error {
	inPlaceholder := func(i int) bool {
		for _, l := range locs {
			if i >= l[0] && i < l[1] {
				return true
			}
		}
		return false
	}
	for i := range len(template) {
		c := template[i]
		if (c == '{' || c == '}') && !inPlaceholder(i) {
			return fmt.Errorf("template %q: stray %q at position %d (use {N} or {title})", template, c, i)
		}
	}
	return nil
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
	// reject stray `{` or `}` not part of a recognized placeholder — catches
	// typos like "{title" (unclosed) or "}foo{" that would otherwise compile as
	// literals and later fail with a misleading "no executable task sections".
	if err := checkUnbalancedBraces(template, locs); err != nil {
		return nil, err
	}

	// walk the template, quoting literal segments and substituting placeholders.
	// internal whitespace runs in literal segments are rendered as \s+ so that a
	// template like "### Task {N}: {title}" still matches inputs with multiple spaces
	// or tabs between tokens (preserving the legacy regex behavior). Whitespace
	// immediately preceding {title} is rendered as \s* so the same template also
	// matches "### Task 1:" (no trailing title).
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
				b.WriteString(quoteLiteralFlexibleSpaces(trim))
			}
			if len(lit) > len(trim) {
				b.WriteString(`\s*`)
			}
		} else if lit != "" {
			b.WriteString(quoteLiteralFlexibleSpaces(lit))
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
		b.WriteString(quoteLiteralFlexibleSpaces(template[idx:]))
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
