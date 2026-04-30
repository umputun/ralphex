package plan

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveHeaderPattern(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantErr     bool
		matchLine   string
		wantMatch   bool
	}{
		{
			name:      "default preset resolves",
			input:     "default",
			matchLine: "### Task 1: implement thing",
			wantMatch: true,
		},
		{
			name:      "default preset matches iteration",
			input:     "default",
			matchLine: "### Iteration 2: refine output",
			wantMatch: true,
		},
		{
			name:      "openspec preset resolves",
			input:     "openspec",
			matchLine: "## 3. Add authentication",
			wantMatch: true,
		},
		{
			name:      "openspec preset no dot",
			input:     "openspec",
			matchLine: "## 7 Do something",
			wantMatch: true,
		},
		{
			name:      "raw regex compiles",
			input:     `^# Phase (\d+):\s*(.*)$`,
			matchLine: "# Phase 2: cleanup",
			wantMatch: true,
		},
		{
			name:      "unknown name treated as raw regex with capture group",
			input:     `^(defualt)$`,
			matchLine: "defualt",
			wantMatch: true,
		},
		{
			name:    "invalid regex returns error",
			input:   `^(unclosed`,
			wantErr: true,
		},
		{
			name:    "raw regex without capture group returns error",
			input:   `^## .+$`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			re, err := ResolveHeaderPattern(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, re)
			if tt.matchLine != "" {
				assert.Equal(t, tt.wantMatch, re.MatchString(tt.matchLine))
			}
		})
	}
}

func TestResolveHeaderPatterns(t *testing.T) {
	t.Run("resolves multiple patterns", func(t *testing.T) {
		patterns, err := ResolveHeaderPatterns([]string{"default", "openspec"})
		require.NoError(t, err)
		assert.Len(t, patterns, 2)
	})

	t.Run("empty slice returns empty result", func(t *testing.T) {
		patterns, err := ResolveHeaderPatterns([]string{})
		require.NoError(t, err)
		assert.Empty(t, patterns)
	})

	t.Run("stops on first invalid regex", func(t *testing.T) {
		_, err := ResolveHeaderPatterns([]string{"default", `^(bad`, "openspec"})
		require.Error(t, err)
	})
}

func TestDefaultHeaderPatternsCompiles(t *testing.T) {
	patterns := DefaultHeaderPatterns()
	require.Len(t, patterns, 1)
	assert.True(t, patterns[0].MatchString("### Task 1: something"))
	assert.True(t, patterns[0].MatchString("### Iteration 3: refine"))
	assert.False(t, patterns[0].MatchString("## 1. openspec style"))
}

func TestPresetDescription(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"default", "### Task N: title  or  ### Iteration N: title"},
		{"openspec", "## N. title"},
		{`^# Phase (\d+):`, `^# Phase (\d+):`},
		{"unknown", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.want, PresetDescription(tt.input))
		})
	}
}
