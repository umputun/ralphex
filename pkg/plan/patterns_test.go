package plan

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/ralphex/pkg/config"
)

func TestCompileTaskHeaderPattern(t *testing.T) {
	tests := []struct {
		name       string
		template   string
		wantErr    bool
		errContain string
		// sample inputs that should match, with expected [N, title] captures
		matches map[string][2]string
		// sample inputs that should NOT match
		nonMatches []string
	}{
		{
			name:     "default task template",
			template: "### Task {N}: {title}",
			matches: map[string][2]string{
				"### Task 1: Foo":        {"1", "Foo"},
				"### Task 2: Bar baz":    {"2", "Bar baz"},
				"### Task 10: Something": {"10", "Something"},
				"### Task 1: Foo   ":     {"1", "Foo"},
				"### Task 1.2: Nested":   {"1.2", "Nested"}, // {N} allows dots? check
			},
			nonMatches: []string{
				"## Task 1: Foo",
				"### task 1: Foo",
				"Task 1: Foo",
			},
		},
		{
			name:     "default iteration template",
			template: "### Iteration {N}: {title}",
			matches: map[string][2]string{
				"### Iteration 1: Foo": {"1", "Foo"},
				"### Iteration 2: Bar": {"2", "Bar"},
			},
			nonMatches: []string{
				"### iteration 1: Foo",
				"### Task 1: Foo",
			},
		},
		{
			name:     "openspec style phase",
			template: "## {N}. {title}",
			matches: map[string][2]string{
				"## 1. Phase one":      {"1", "Phase one"},
				"## 2. Implementation": {"2", "Implementation"},
			},
			nonMatches: []string{
				"### 1. Phase one",
				"## Phase one",
				"## 1 Phase one",
			},
		},
		{
			name:     "no title",
			template: "### Task {N}:",
			matches: map[string][2]string{
				"### Task 1:":  {"1", ""},
				"### Task 2: ": {"2", ""},
			},
			nonMatches: []string{
				"### Task 1: extra text",
			},
		},
		{
			name:     "no literals only placeholder",
			template: "##{N}",
			matches: map[string][2]string{
				"##1": {"1", ""},
				"##2": {"2", ""},
			},
			nonMatches: []string{
				"## 1",
				"### 1",
			},
		},
		{
			name:     "regex meta chars in literals are escaped",
			template: "### [{N}] ({title})",
			matches: map[string][2]string{
				"### [1] (Hello)": {"1", "Hello"},
				"### [2] (World)": {"2", "World"},
			},
			nonMatches: []string{
				"### 1 Hello",
				"### [1] Hello",
			},
		},
		{
			name:       "missing N placeholder",
			template:   "### Task: {title}",
			wantErr:    true,
			errContain: "{N}",
		},
		{
			name:       "empty template",
			template:   "",
			wantErr:    true,
			errContain: "{N}",
		},
		{
			name:       "N appearing twice",
			template:   "### Task {N}: {N}",
			wantErr:    true,
			errContain: "{N}",
		},
		{
			name:       "title before N",
			template:   "### Task {title}: {N}",
			wantErr:    true,
			errContain: "{title}",
		},
		{
			name:       "title without N",
			template:   "### Task {title}",
			wantErr:    true,
			errContain: "{N}",
		},
		{
			name:       "title appearing twice",
			template:   "### Task {N}: {title} {title}",
			wantErr:    true,
			errContain: "{title}",
		},
		{
			name:       "unknown placeholder",
			template:   "### Task {foo}: {title}",
			wantErr:    true,
			errContain: "{foo}",
		},
		{
			name:       "unknown placeholder with N present",
			template:   "### Task {N}: {foo}",
			wantErr:    true,
			errContain: "{foo}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			re, err := CompileTaskHeaderPattern(tt.template)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContain != "" {
					assert.Contains(t, err.Error(), tt.errContain)
				}
				return
			}
			require.NoError(t, err)
			require.NotNil(t, re)

			for input, expected := range tt.matches {
				m := re.FindStringSubmatch(input)
				require.NotNil(t, m, "input %q should match pattern %q", input, tt.template)
				assert.Equal(t, expected[0], m[1], "input %q: N capture", input)
				if len(m) >= 3 {
					assert.Equal(t, expected[1], m[2], "input %q: title capture", input)
				} else {
					assert.Empty(t, expected[1], "input %q: no title group but expected %q", input, expected[1])
				}
			}

			for _, input := range tt.nonMatches {
				m := re.FindStringSubmatch(input)
				assert.Nil(t, m, "input %q should NOT match pattern %q", input, tt.template)
			}
		})
	}
}

func TestCompileTaskHeaderPatterns(t *testing.T) {
	tests := []struct {
		name       string
		templates  []string
		wantErr    bool
		errContain string
		wantCount  int
	}{
		{
			name:      "nil returns defaults",
			templates: nil,
			wantCount: len(DefaultTaskHeaderPatterns),
		},
		{
			name:      "empty returns defaults",
			templates: []string{},
			wantCount: len(DefaultTaskHeaderPatterns),
		},
		{
			name:      "single template",
			templates: []string{"### Task {N}: {title}"},
			wantCount: 1,
		},
		{
			name:      "multiple templates",
			templates: []string{"### Task {N}: {title}", "## {N}. {title}"},
			wantCount: 2,
		},
		{
			name:       "one bad template fails whole call",
			templates:  []string{"### Task {N}: {title}", "### Bad {foo}"},
			wantErr:    true,
			errContain: "### Bad {foo}",
		},
		{
			name:       "missing N in one template",
			templates:  []string{"### Task: {title}"},
			wantErr:    true,
			errContain: "### Task: {title}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := CompileTaskHeaderPatterns(tt.templates)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContain != "" {
					assert.Contains(t, err.Error(), tt.errContain)
				}
				return
			}
			require.NoError(t, err)
			assert.Len(t, res, tt.wantCount)
			for _, re := range res {
				assert.NotNil(t, re)
				// sanity: each entry must be a real *regexp.Regexp
				_, ok := any(re).(*regexp.Regexp)
				assert.True(t, ok)
			}
		})
	}
}

// TestDefaultPatternsMatchLegacyRegex verifies that compiled default patterns
// match the same inputs as the previous hardcoded regex
// ^###\s+(?:Task|Iteration)\s+([^:]+?):\s*(.*)$
func TestDefaultPatternsMatchLegacyRegex(t *testing.T) {
	legacy := regexp.MustCompile(`^###\s+(?:Task|Iteration)\s+([^:]+?):\s*(.*)$`)

	compiled, err := CompileTaskHeaderPatterns(DefaultTaskHeaderPatterns)
	require.NoError(t, err)
	require.Len(t, compiled, 2)

	inputs := []struct {
		line      string
		shouldHit bool
		wantNum   string
		wantTitle string
	}{
		{"### Task 1: Foo", true, "1", "Foo"},
		{"### Iteration 2: Bar", true, "2", "Bar"},
		{"### Task 1.2: Sub-task", true, "1.2", "Sub-task"},
		{"### Task 10: Big one", true, "10", "Big one"},
		{"### Task 1:", true, "1", ""},
		{"### Task 1: ", true, "1", ""},
		{"## Task 1: Foo", false, "", ""},
		{"### task 1: Foo", false, "", ""},
		{"### Other 1: Foo", false, "", ""},
		{"Task 1: Foo", false, "", ""},
	}

	for _, in := range inputs {
		t.Run(in.line, func(t *testing.T) {
			legacyM := legacy.FindStringSubmatch(in.line)
			if in.shouldHit {
				require.NotNil(t, legacyM, "legacy should match %q", in.line)
			} else {
				require.Nil(t, legacyM, "legacy should NOT match %q", in.line)
			}

			// at least one compiled default pattern should match iff legacy does
			var hit bool
			var gotN, gotT string
			for _, re := range compiled {
				if m := re.FindStringSubmatch(in.line); m != nil {
					hit = true
					gotN = m[1]
					if len(m) >= 3 {
						gotT = m[2]
					}
					break
				}
			}
			assert.Equal(t, in.shouldHit, hit, "compiled hit mismatch for %q", in.line)
			if in.shouldHit {
				assert.Equal(t, in.wantNum, gotN)
				assert.Equal(t, in.wantTitle, gotT)
			}
		})
	}
}

func TestDefaultTaskHeaderPatterns(t *testing.T) {
	// safety: the exported default slice is a fixed well-known pair
	require.Equal(t, []string{
		"### Task {N}: {title}",
		"### Iteration {N}: {title}",
	}, DefaultTaskHeaderPatterns)
}

// TestDefaultTaskHeaderPatterns_MatchesConfigDefaults asserts that the inlined
// default list in pkg/config matches plan.DefaultTaskHeaderPatterns element-for-element.
// pkg/config intentionally inlines its default (rather than importing pkg/plan) to
// keep the config package free of domain-package imports. This test catches any
// future divergence between the two literal slices.
func TestDefaultTaskHeaderPatterns_MatchesConfigDefaults(t *testing.T) {
	// load a config from a temp dir with no task_header_patterns key set;
	// the resulting cfg.TaskHeaderPatterns must equal plan.DefaultTaskHeaderPatterns.
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")
	require.NoError(t, os.MkdirAll(configDir, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "prompts"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "agents"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config"), []byte(""), 0o600))

	cfg, err := config.Load(configDir)
	require.NoError(t, err)

	assert.Equal(t, DefaultTaskHeaderPatterns, cfg.TaskHeaderPatterns,
		"pkg/config inline defaults must stay in sync with plan.DefaultTaskHeaderPatterns")
}
