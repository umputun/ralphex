package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_newValuesLoader(t *testing.T) {
	loader := newValuesLoader(defaultsFS)
	assert.NotNil(t, loader)
}

func TestValuesLoader_Load_EmbeddedOnly(t *testing.T) {
	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load("", "")
	require.NoError(t, err)

	// all values should come from embedded defaults
	assert.Equal(t, "claude", values.ClaudeCommand)
	assert.Equal(t, "--dangerously-skip-permissions --output-format stream-json --verbose", values.ClaudeArgs)
	assert.True(t, values.CodexEnabled)
	assert.True(t, values.CodexEnabledSet)
	assert.Equal(t, "codex", values.CodexCommand)
	assert.Equal(t, "gpt-5.2-codex", values.CodexModel)
	assert.Equal(t, "xhigh", values.CodexReasoningEffort)
	assert.Equal(t, 3600000, values.CodexTimeoutMs)
	assert.Equal(t, "read-only", values.CodexSandbox)
	assert.Equal(t, 2000, values.IterationDelayMs)
	assert.Equal(t, 1, values.TaskRetryCount)
	assert.True(t, values.TaskRetryCountSet)
	assert.Equal(t, "docs/plans", values.PlansDir)
	assert.Equal(t, []string{"You've hit your limit", "API Error:"}, values.ClaudeErrorPatterns)
	assert.Equal(t, []string{"Rate limit", "quota exceeded"}, values.CodexErrorPatterns)
}

func TestValuesLoader_Load_GlobalOnly(t *testing.T) {
	tmpDir := t.TempDir()
	globalConfig := filepath.Join(tmpDir, "config")

	configContent := `
claude_command = /global/claude
claude_args = --global-args
iteration_delay_ms = 5000
`
	require.NoError(t, os.WriteFile(globalConfig, []byte(configContent), 0o600))

	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load("", globalConfig)
	require.NoError(t, err)

	// values from global config
	assert.Equal(t, "/global/claude", values.ClaudeCommand)
	assert.Equal(t, "--global-args", values.ClaudeArgs)
	assert.Equal(t, 5000, values.IterationDelayMs)

	// values from embedded (not set in global)
	assert.True(t, values.CodexEnabled)
	assert.Equal(t, "codex", values.CodexCommand)
	assert.Equal(t, "gpt-5.2-codex", values.CodexModel)
	assert.Equal(t, "docs/plans", values.PlansDir)
}

func TestValuesLoader_Load_LocalOverridesGlobal(t *testing.T) {
	tmpDir := t.TempDir()
	globalConfig := filepath.Join(tmpDir, "global-config")
	localConfig := filepath.Join(tmpDir, "local-config")

	globalContent := `
claude_command = /global/claude
claude_args = --global-args
iteration_delay_ms = 5000
plans_dir = global/plans
`
	require.NoError(t, os.WriteFile(globalConfig, []byte(globalContent), 0o600))

	localContent := `
claude_command = /local/claude
plans_dir = local/plans
`
	require.NoError(t, os.WriteFile(localConfig, []byte(localContent), 0o600))

	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load(localConfig, globalConfig)
	require.NoError(t, err)

	// local values override global
	assert.Equal(t, "/local/claude", values.ClaudeCommand)
	assert.Equal(t, "local/plans", values.PlansDir)

	// global values preserved when not overridden
	assert.Equal(t, "--global-args", values.ClaudeArgs)
	assert.Equal(t, 5000, values.IterationDelayMs)
}

func TestValuesLoader_Load_PartialConfigs(t *testing.T) {
	tmpDir := t.TempDir()
	globalConfig := filepath.Join(tmpDir, "global-config")

	// partial config - only some values
	globalContent := `plans_dir = custom/plans`
	require.NoError(t, os.WriteFile(globalConfig, []byte(globalContent), 0o600))

	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load("", globalConfig)
	require.NoError(t, err)

	// partial value preserved
	assert.Equal(t, "custom/plans", values.PlansDir)

	// missing values filled from embedded defaults
	assert.Equal(t, "claude", values.ClaudeCommand)
	assert.Equal(t, "--dangerously-skip-permissions --output-format stream-json --verbose", values.ClaudeArgs)
	assert.Equal(t, "codex", values.CodexCommand)
	assert.Equal(t, 2000, values.IterationDelayMs)
}

func TestValuesLoader_Load_InvalidConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		errPart string
	}{
		{name: "invalid iteration_delay_ms", config: "iteration_delay_ms = not_a_number", errPart: "iteration_delay_ms"},
		{name: "invalid codex_timeout_ms", config: "codex_timeout_ms = abc", errPart: "codex_timeout_ms"},
		{name: "invalid codex_enabled", config: "codex_enabled = maybe", errPart: "codex_enabled"},
		{name: "invalid finalize_enabled", config: "finalize_enabled = maybe", errPart: "finalize_enabled"},
		{name: "negative task_retry_count", config: "task_retry_count = -1", errPart: "task_retry_count"},
		{name: "negative codex_timeout_ms", config: "codex_timeout_ms = -100", errPart: "codex_timeout_ms"},
		{name: "negative iteration_delay_ms", config: "iteration_delay_ms = -50", errPart: "iteration_delay_ms"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			configPath := filepath.Join(tmpDir, "config")
			require.NoError(t, os.WriteFile(configPath, []byte(tc.config), 0o600))

			loader := newValuesLoader(defaultsFS)
			_, err := loader.Load("", configPath)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errPart)
		})
	}
}

func TestValuesLoader_Load_NonExistentFile(t *testing.T) {
	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load("/nonexistent/local", "/nonexistent/global")
	require.NoError(t, err)

	// should fall back to embedded defaults
	assert.Equal(t, "claude", values.ClaudeCommand)
	assert.True(t, values.CodexEnabled)
}

func TestValuesLoader_Load_ExplicitFalseCodexEnabled(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config")

	configContent := `codex_enabled = false`
	require.NoError(t, os.WriteFile(configPath, []byte(configContent), 0o600))

	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load("", configPath)
	require.NoError(t, err)

	// explicit false should be preserved (not overwritten by embedded default true)
	assert.False(t, values.CodexEnabled)
	assert.True(t, values.CodexEnabledSet)
}

func TestValuesLoader_Load_ExplicitZeroTaskRetryCount(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config")

	configContent := `task_retry_count = 0`
	require.NoError(t, os.WriteFile(configPath, []byte(configContent), 0o600))

	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load("", configPath)
	require.NoError(t, err)

	// explicit zero should be preserved (not overwritten by embedded default 1)
	assert.Equal(t, 0, values.TaskRetryCount)
	assert.True(t, values.TaskRetryCountSet)
}

func TestValuesLoader_Load_ExplicitZeroCodexTimeoutMs(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config")

	configContent := `codex_timeout_ms = 0`
	require.NoError(t, os.WriteFile(configPath, []byte(configContent), 0o600))

	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load("", configPath)
	require.NoError(t, err)

	// explicit zero should be preserved (not overwritten by embedded default)
	assert.Equal(t, 0, values.CodexTimeoutMs)
	assert.True(t, values.CodexTimeoutMsSet)
}

func TestValuesLoader_Load_ExplicitZeroIterationDelayMs(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config")

	configContent := `iteration_delay_ms = 0`
	require.NoError(t, os.WriteFile(configPath, []byte(configContent), 0o600))

	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load("", configPath)
	require.NoError(t, err)

	// explicit zero should be preserved (not overwritten by embedded default)
	assert.Equal(t, 0, values.IterationDelayMs)
	assert.True(t, values.IterationDelayMsSet)
}

func TestValuesLoader_Load_LocalOverridesCodexEnabled(t *testing.T) {
	tmpDir := t.TempDir()
	globalConfig := filepath.Join(tmpDir, "global")
	localConfig := filepath.Join(tmpDir, "local")

	require.NoError(t, os.WriteFile(globalConfig, []byte(`codex_enabled = true`), 0o600))
	require.NoError(t, os.WriteFile(localConfig, []byte(`codex_enabled = false`), 0o600))

	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load(localConfig, globalConfig)
	require.NoError(t, err)

	assert.False(t, values.CodexEnabled)
	assert.True(t, values.CodexEnabledSet)
}

func TestValuesLoader_Load_LocalOverridesTaskRetryCount(t *testing.T) {
	tmpDir := t.TempDir()
	globalConfig := filepath.Join(tmpDir, "global")
	localConfig := filepath.Join(tmpDir, "local")

	require.NoError(t, os.WriteFile(globalConfig, []byte(`task_retry_count = 5`), 0o600))
	require.NoError(t, os.WriteFile(localConfig, []byte(`task_retry_count = 0`), 0o600))

	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load(localConfig, globalConfig)
	require.NoError(t, err)

	assert.Equal(t, 0, values.TaskRetryCount)
	assert.True(t, values.TaskRetryCountSet)
}

func TestValuesLoader_Load_LocalOverridesFinalizeEnabled(t *testing.T) {
	tmpDir := t.TempDir()
	globalConfig := filepath.Join(tmpDir, "global")
	localConfig := filepath.Join(tmpDir, "local")

	require.NoError(t, os.WriteFile(globalConfig, []byte(`finalize_enabled = false`), 0o600))
	require.NoError(t, os.WriteFile(localConfig, []byte(`finalize_enabled = true`), 0o600))

	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load(localConfig, globalConfig)
	require.NoError(t, err)

	assert.True(t, values.FinalizeEnabled)
	assert.True(t, values.FinalizeEnabledSet)
}

func TestValuesLoader_Load_AllValuesFromUserConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config")

	configContent := `
claude_command = /custom/claude
claude_args = --custom
codex_enabled = false
codex_command = /custom/codex
codex_model = custom-model
codex_reasoning_effort = low
codex_timeout_ms = 1000
codex_sandbox = none
iteration_delay_ms = 500
task_retry_count = 5
plans_dir = my/plans
`
	require.NoError(t, os.WriteFile(configPath, []byte(configContent), 0o600))

	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load("", configPath)
	require.NoError(t, err)

	assert.Equal(t, "/custom/claude", values.ClaudeCommand)
	assert.Equal(t, "--custom", values.ClaudeArgs)
	assert.False(t, values.CodexEnabled)
	assert.True(t, values.CodexEnabledSet)
	assert.Equal(t, "/custom/codex", values.CodexCommand)
	assert.Equal(t, "custom-model", values.CodexModel)
	assert.Equal(t, "low", values.CodexReasoningEffort)
	assert.Equal(t, 1000, values.CodexTimeoutMs)
	assert.Equal(t, "none", values.CodexSandbox)
	assert.Equal(t, 500, values.IterationDelayMs)
	assert.Equal(t, 5, values.TaskRetryCount)
	assert.True(t, values.TaskRetryCountSet)
	assert.Equal(t, "my/plans", values.PlansDir)
}

func TestValues_mergeFrom(t *testing.T) {
	t.Run("merge non-empty values", func(t *testing.T) {
		dst := Values{
			ClaudeCommand: "dst-claude",
			PlansDir:      "dst-plans",
		}
		src := Values{
			ClaudeCommand: "src-claude",
			ClaudeArgs:    "src-args",
		}
		dst.mergeFrom(&src)

		assert.Equal(t, "src-claude", dst.ClaudeCommand)
		assert.Equal(t, "src-args", dst.ClaudeArgs)
		assert.Equal(t, "dst-plans", dst.PlansDir)
	})

	t.Run("empty source doesn't overwrite", func(t *testing.T) {
		dst := Values{
			ClaudeCommand: "dst-claude",
			PlansDir:      "dst-plans",
		}
		src := Values{
			ClaudeCommand: "", // empty, shouldn't overwrite
		}
		dst.mergeFrom(&src)

		assert.Equal(t, "dst-claude", dst.ClaudeCommand)
		assert.Equal(t, "dst-plans", dst.PlansDir)
	})

	t.Run("set flags control bool and int merging", func(t *testing.T) {
		dst := Values{
			CodexEnabled:        true,
			CodexEnabledSet:     true,
			CodexTimeoutMs:      3600000,
			CodexTimeoutMsSet:   true,
			IterationDelayMs:    2000,
			IterationDelayMsSet: true,
			TaskRetryCount:      5,
			TaskRetryCountSet:   true,
		}
		src := Values{
			CodexEnabled:        false,
			CodexEnabledSet:     true,
			CodexTimeoutMs:      0,
			CodexTimeoutMsSet:   true,
			IterationDelayMs:    0,
			IterationDelayMsSet: true,
			TaskRetryCount:      0,
			TaskRetryCountSet:   true,
		}
		dst.mergeFrom(&src)

		assert.False(t, dst.CodexEnabled)
		assert.Equal(t, 0, dst.CodexTimeoutMs)
		assert.Equal(t, 0, dst.IterationDelayMs)
		assert.Equal(t, 0, dst.TaskRetryCount)
	})

	t.Run("unset flags don't merge", func(t *testing.T) {
		dst := Values{
			CodexEnabled:        true,
			CodexEnabledSet:     true,
			CodexTimeoutMs:      3600000,
			CodexTimeoutMsSet:   true,
			IterationDelayMs:    2000,
			IterationDelayMsSet: true,
			TaskRetryCount:      5,
			TaskRetryCountSet:   true,
		}
		src := Values{
			CodexEnabled:        false,
			CodexEnabledSet:     false, // not explicitly set
			CodexTimeoutMs:      0,
			CodexTimeoutMsSet:   false, // not explicitly set
			IterationDelayMs:    0,
			IterationDelayMsSet: false, // not explicitly set
			TaskRetryCount:      0,
			TaskRetryCountSet:   false, // not explicitly set
		}
		dst.mergeFrom(&src)

		assert.True(t, dst.CodexEnabled)
		assert.Equal(t, 3600000, dst.CodexTimeoutMs)
		assert.Equal(t, 2000, dst.IterationDelayMs)
		assert.Equal(t, 5, dst.TaskRetryCount)
	})
}

func TestValuesLoader_parseValuesFromBytes(t *testing.T) {
	vl := &valuesLoader{embedFS: defaultsFS}

	t.Run("full config", func(t *testing.T) {
		data := []byte(`
claude_command = /custom/claude
claude_args = --custom-arg
codex_enabled = false
codex_command = /custom/codex
codex_model = gpt-5
codex_reasoning_effort = high
codex_timeout_ms = 7200000
codex_sandbox = none
iteration_delay_ms = 5000
task_retry_count = 3
plans_dir = custom/plans
`)
		values, err := vl.parseValuesFromBytes(data)
		require.NoError(t, err)

		assert.Equal(t, "/custom/claude", values.ClaudeCommand)
		assert.Equal(t, "--custom-arg", values.ClaudeArgs)
		assert.False(t, values.CodexEnabled)
		assert.True(t, values.CodexEnabledSet)
		assert.Equal(t, "/custom/codex", values.CodexCommand)
		assert.Equal(t, "gpt-5", values.CodexModel)
		assert.Equal(t, "high", values.CodexReasoningEffort)
		assert.Equal(t, 7200000, values.CodexTimeoutMs)
		assert.Equal(t, "none", values.CodexSandbox)
		assert.Equal(t, 5000, values.IterationDelayMs)
		assert.Equal(t, 3, values.TaskRetryCount)
		assert.True(t, values.TaskRetryCountSet)
		assert.Equal(t, "custom/plans", values.PlansDir)
	})

	t.Run("empty config", func(t *testing.T) {
		data := []byte("")
		values, err := vl.parseValuesFromBytes(data)
		require.NoError(t, err)

		assert.Empty(t, values.ClaudeCommand)
		assert.False(t, values.CodexEnabled)
		assert.False(t, values.CodexEnabledSet)
	})

	t.Run("bool values", func(t *testing.T) {
		tests := []struct {
			name     string
			input    string
			expected bool
		}{
			{"true lowercase", "codex_enabled = true", true},
			{"TRUE uppercase", "codex_enabled = TRUE", true},
			{"false lowercase", "codex_enabled = false", false},
			{"yes", "codex_enabled = yes", true},
			{"no", "codex_enabled = no", false},
			{"1", "codex_enabled = 1", true},
			{"0", "codex_enabled = 0", false},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				values, err := vl.parseValuesFromBytes([]byte(tc.input))
				require.NoError(t, err)
				assert.Equal(t, tc.expected, values.CodexEnabled)
				assert.True(t, values.CodexEnabledSet)
			})
		}
	})
}

func TestValuesLoader_parseValuesFromFile_PermissionDenied(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config")
	require.NoError(t, os.WriteFile(configPath, []byte("claude_command = test"), 0o600))

	// remove read permission
	require.NoError(t, os.Chmod(configPath, 0o000))
	t.Cleanup(func() { _ = os.Chmod(configPath, 0o600) })

	vl := &valuesLoader{embedFS: defaultsFS}
	_, err := vl.parseValuesFromFile(configPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read config")
}

func TestValuesLoader_parseValuesFromBytes_InvalidINI(t *testing.T) {
	vl := &valuesLoader{embedFS: defaultsFS}

	// malformed INI syntax (unclosed section)
	_, err := vl.parseValuesFromBytes([]byte("[unclosed"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse config")
}

func TestValuesLoader_parseValuesFromBytes_ErrorPatterns(t *testing.T) {
	vl := &valuesLoader{embedFS: defaultsFS}

	tests := []struct {
		name           string
		input          string
		expectedClaude []string
		expectedCodex  []string
	}{
		{
			name:           "single pattern",
			input:          "claude_error_patterns = rate limit",
			expectedClaude: []string{"rate limit"},
			expectedCodex:  nil,
		},
		{
			name:           "multiple patterns comma-separated",
			input:          "codex_error_patterns = rate limit,quota exceeded,too many requests",
			expectedClaude: nil,
			expectedCodex:  []string{"rate limit", "quota exceeded", "too many requests"},
		},
		{
			name:           "whitespace trimming around commas",
			input:          "claude_error_patterns =  pattern1 ,  pattern2  , pattern3 ",
			expectedClaude: []string{"pattern1", "pattern2", "pattern3"},
			expectedCodex:  nil,
		},
		{
			name:           "empty patterns filtered out",
			input:          "claude_error_patterns = pattern1,,pattern2,  ,pattern3",
			expectedClaude: []string{"pattern1", "pattern2", "pattern3"},
			expectedCodex:  nil,
		},
		{
			name:           "both claude and codex patterns",
			input:          "claude_error_patterns = hit limit\ncodex_error_patterns = rate exceeded",
			expectedClaude: []string{"hit limit"},
			expectedCodex:  []string{"rate exceeded"},
		},
		{
			name:           "empty value",
			input:          "claude_error_patterns = ",
			expectedClaude: nil,
			expectedCodex:  nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			values, err := vl.parseValuesFromBytes([]byte(tc.input))
			require.NoError(t, err)
			assert.Equal(t, tc.expectedClaude, values.ClaudeErrorPatterns)
			assert.Equal(t, tc.expectedCodex, values.CodexErrorPatterns)
		})
	}
}

func TestValues_mergeFrom_ErrorPatterns(t *testing.T) {
	t.Run("merge error patterns when src has values", func(t *testing.T) {
		dst := Values{
			ClaudeErrorPatterns: []string{"dst pattern"},
			CodexErrorPatterns:  []string{"dst codex"},
		}
		src := Values{
			ClaudeErrorPatterns: []string{"src pattern 1", "src pattern 2"},
			CodexErrorPatterns:  []string{"src codex"},
		}
		dst.mergeFrom(&src)

		assert.Equal(t, []string{"src pattern 1", "src pattern 2"}, dst.ClaudeErrorPatterns)
		assert.Equal(t, []string{"src codex"}, dst.CodexErrorPatterns)
	})

	t.Run("preserve dst when src is empty", func(t *testing.T) {
		dst := Values{
			ClaudeErrorPatterns: []string{"dst pattern"},
			CodexErrorPatterns:  []string{"dst codex"},
		}
		src := Values{
			ClaudeErrorPatterns: nil,
			CodexErrorPatterns:  nil,
		}
		dst.mergeFrom(&src)

		assert.Equal(t, []string{"dst pattern"}, dst.ClaudeErrorPatterns)
		assert.Equal(t, []string{"dst codex"}, dst.CodexErrorPatterns)
	})
}

func TestValuesLoader_Load_ErrorPatternsOverride(t *testing.T) {
	tmpDir := t.TempDir()
	globalConfig := filepath.Join(tmpDir, "global")
	localConfig := filepath.Join(tmpDir, "local")

	// global has one set of patterns
	globalContent := `claude_error_patterns = global pattern 1, global pattern 2`
	require.NoError(t, os.WriteFile(globalConfig, []byte(globalContent), 0o600))

	// local overrides with different patterns
	localContent := `claude_error_patterns = local pattern`
	require.NoError(t, os.WriteFile(localConfig, []byte(localContent), 0o600))

	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load(localConfig, globalConfig)
	require.NoError(t, err)

	// local should override global completely (not merge)
	assert.Equal(t, []string{"local pattern"}, values.ClaudeErrorPatterns)
}

func TestValuesLoader_Load_AllCommentedConfigFallsBackToEmbedded(t *testing.T) {
	tmpDir := t.TempDir()
	globalConfig := filepath.Join(tmpDir, "config")

	// config with only comments and whitespace - should fall back to embedded
	commentedConfig := `# this is a commented config file
# all lines are comments
# claude_command = /custom/claude

# empty lines below

`
	require.NoError(t, os.WriteFile(globalConfig, []byte(commentedConfig), 0o600))

	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load("", globalConfig)
	require.NoError(t, err)

	// should fall back to embedded defaults since file has no actual content
	assert.Equal(t, "claude", values.ClaudeCommand)
	assert.Equal(t, "--dangerously-skip-permissions --output-format stream-json --verbose", values.ClaudeArgs)
	assert.True(t, values.CodexEnabled)
	assert.Equal(t, "codex", values.CodexCommand)
	assert.Equal(t, "gpt-5.2-codex", values.CodexModel)
	assert.Equal(t, "docs/plans", values.PlansDir)
}

func TestValuesLoader_Load_PartiallyCommentedConfigUsesUncommentedValues(t *testing.T) {
	tmpDir := t.TempDir()
	globalConfig := filepath.Join(tmpDir, "config")

	// config with some commented and some uncommented lines
	partialConfig := `# this line is a comment
claude_command = /custom/claude
# claude_args is commented out
# claude_args = --some-args
plans_dir = custom/plans
`
	require.NoError(t, os.WriteFile(globalConfig, []byte(partialConfig), 0o600))

	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load("", globalConfig)
	require.NoError(t, err)

	// uncommented values should be used
	assert.Equal(t, "/custom/claude", values.ClaudeCommand)
	assert.Equal(t, "custom/plans", values.PlansDir)

	// commented-out values should fall back to embedded defaults
	assert.Equal(t, "--dangerously-skip-permissions --output-format stream-json --verbose", values.ClaudeArgs)
}

func TestValuesLoader_Load_LocalAllCommentedGlobalHasContent(t *testing.T) {
	tmpDir := t.TempDir()
	globalConfig := filepath.Join(tmpDir, "global-config")
	localConfig := filepath.Join(tmpDir, "local-config")

	// global has actual content
	globalContent := `claude_command = /global/claude
plans_dir = global/plans
`
	require.NoError(t, os.WriteFile(globalConfig, []byte(globalContent), 0o600))

	// local is all-commented (installed template)
	localCommented := `# local config template
# uncomment values to customize
# claude_command = /local/claude
`
	require.NoError(t, os.WriteFile(localConfig, []byte(localCommented), 0o600))

	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load(localConfig, globalConfig)
	require.NoError(t, err)

	// local all-commented falls back, so global values should be used
	assert.Equal(t, "/global/claude", values.ClaudeCommand)
	assert.Equal(t, "global/plans", values.PlansDir)
}

func TestValuesLoader_Load_BothAllCommentedFallsBackToEmbedded(t *testing.T) {
	tmpDir := t.TempDir()
	globalConfig := filepath.Join(tmpDir, "global-config")
	localConfig := filepath.Join(tmpDir, "local-config")

	// both files are all-commented templates
	commentedTemplate := `# config template
# uncomment values to customize
# claude_command = /custom/claude
# plans_dir = custom/plans
`
	require.NoError(t, os.WriteFile(globalConfig, []byte(commentedTemplate), 0o600))
	require.NoError(t, os.WriteFile(localConfig, []byte(commentedTemplate), 0o600))

	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load(localConfig, globalConfig)
	require.NoError(t, err)

	// both all-commented, should fall back to embedded defaults
	assert.Equal(t, "claude", values.ClaudeCommand)
	assert.Equal(t, "docs/plans", values.PlansDir)
	assert.True(t, values.CodexEnabled)
}
