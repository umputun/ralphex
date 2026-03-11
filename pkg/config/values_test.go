package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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
	assert.Equal(t, "copilot", values.CopilotCommand)
	assert.Equal(t, "--allow-all --no-ask-user --output-format json", values.CopilotArgs)
	assert.Equal(t, "claude-opus-4-6", values.CopilotCodingModel)
	assert.Equal(t, "gpt-5.2-codex", values.CopilotReviewModel)
	assert.Equal(t, "copilot", values.ExternalReviewTool)
	assert.Empty(t, values.CustomReviewScript)
	assert.Equal(t, 2000, values.IterationDelayMs)
	assert.Equal(t, 1, values.TaskRetryCount)
	assert.True(t, values.TaskRetryCountSet)
	assert.Equal(t, "docs/plans", values.PlansDir)
	assert.Equal(t, "git", values.VcsCommand)
	assert.Equal(t, []string{"Rate limit", "quota exceeded", "API Error"}, values.CopilotErrorPatterns)
	assert.Equal(t, []string{"Rate limit", "quota exceeded"}, values.CopilotLimitPatterns)
	assert.Zero(t, values.WaitOnLimit)
	assert.False(t, values.WaitOnLimitSet)
}

func TestValuesLoader_Load_GlobalOnly(t *testing.T) {
	tmpDir := t.TempDir()
	globalConfig := filepath.Join(tmpDir, "config")

	configContent := `
copilot_command = /global/copilot
copilot_args = --global-args
iteration_delay_ms = 5000
`
	require.NoError(t, os.WriteFile(globalConfig, []byte(configContent), 0o600))

	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load("", globalConfig)
	require.NoError(t, err)

	// values from global config
	assert.Equal(t, "/global/copilot", values.CopilotCommand)
	assert.Equal(t, "--global-args", values.CopilotArgs)
	assert.Equal(t, 5000, values.IterationDelayMs)

	// values from embedded (not set in global)
	assert.Equal(t, "claude-opus-4-6", values.CopilotCodingModel)
	assert.Equal(t, "gpt-5.2-codex", values.CopilotReviewModel)
	assert.Equal(t, "docs/plans", values.PlansDir)
}

func TestValuesLoader_Load_LocalOverridesGlobal(t *testing.T) {
	tmpDir := t.TempDir()
	globalConfig := filepath.Join(tmpDir, "global-config")
	localConfig := filepath.Join(tmpDir, "local-config")

	globalContent := `
copilot_command = /global/copilot
copilot_args = --global-args
iteration_delay_ms = 5000
plans_dir = global/plans
`
	require.NoError(t, os.WriteFile(globalConfig, []byte(globalContent), 0o600))

	localContent := `
copilot_command = /local/copilot
plans_dir = local/plans
`
	require.NoError(t, os.WriteFile(localConfig, []byte(localContent), 0o600))

	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load(localConfig, globalConfig)
	require.NoError(t, err)

	// local values override global
	assert.Equal(t, "/local/copilot", values.CopilotCommand)
	assert.Equal(t, "local/plans", values.PlansDir)

	// global values preserved when not overridden
	assert.Equal(t, "--global-args", values.CopilotArgs)
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
	assert.Equal(t, "copilot", values.CopilotCommand)
	assert.Equal(t, "--allow-all --no-ask-user --output-format json", values.CopilotArgs)
	assert.Equal(t, "claude-opus-4-6", values.CopilotCodingModel)
	assert.Equal(t, 2000, values.IterationDelayMs)
}

func TestValuesLoader_Load_InvalidConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		errPart string
	}{
		{name: "invalid iteration_delay_ms", config: "iteration_delay_ms = not_a_number", errPart: "iteration_delay_ms"},
		{name: "invalid finalize_enabled", config: "finalize_enabled = maybe", errPart: "finalize_enabled"},
		{name: "negative task_retry_count", config: "task_retry_count = -1", errPart: "task_retry_count"},
		{name: "negative iteration_delay_ms", config: "iteration_delay_ms = -50", errPart: "iteration_delay_ms"},
		{name: "invalid max_iterations", config: "max_iterations = abc", errPart: "max_iterations"},
		{name: "zero max_iterations", config: "max_iterations = 0", errPart: "max_iterations"},
		{name: "negative max_iterations", config: "max_iterations = -5", errPart: "max_iterations"},
		{name: "negative max_external_iterations", config: "max_external_iterations = -1", errPart: "max_external_iterations"},
		{name: "invalid max_external_iterations", config: "max_external_iterations = abc", errPart: "max_external_iterations"},
		{name: "negative review_patience", config: "review_patience = -1", errPart: "review_patience"},
		{name: "invalid review_patience", config: "review_patience = abc", errPart: "review_patience"},
		{name: "invalid wait_on_limit", config: "wait_on_limit = not-a-duration", errPart: "wait_on_limit"},
		{name: "negative wait_on_limit", config: "wait_on_limit = -30m", errPart: "wait_on_limit"},
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
	assert.Equal(t, "copilot", values.CopilotCommand)
	assert.Equal(t, "copilot", values.ExternalReviewTool)
}

func TestValuesLoader_Load_ExternalReviewToolNone(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config")

	configContent := `external_review_tool = none`
	require.NoError(t, os.WriteFile(configPath, []byte(configContent), 0o600))

	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load("", configPath)
	require.NoError(t, err)

	// explicit "none" should be preserved (not overwritten by embedded default "copilot")
	assert.Equal(t, "none", values.ExternalReviewTool)
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

func TestValuesLoader_Load_CopilotReviewModel(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config")

	configContent := `copilot_review_model = custom-review-model`
	require.NoError(t, os.WriteFile(configPath, []byte(configContent), 0o600))

	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load("", configPath)
	require.NoError(t, err)

	// custom model should be preserved
	assert.Equal(t, "custom-review-model", values.CopilotReviewModel)
	// coding model should still be the embedded default
	assert.Equal(t, "claude-opus-4-6", values.CopilotCodingModel)
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

func TestValuesLoader_Load_LocalOverridesCopilotCodingModel(t *testing.T) {
	tmpDir := t.TempDir()
	globalConfig := filepath.Join(tmpDir, "global")
	localConfig := filepath.Join(tmpDir, "local")

	require.NoError(t, os.WriteFile(globalConfig, []byte(`copilot_coding_model = claude-sonnet-4-6`), 0o600))
	require.NoError(t, os.WriteFile(localConfig, []byte(`copilot_coding_model = claude-haiku-4-5`), 0o600))

	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load(localConfig, globalConfig)
	require.NoError(t, err)

	assert.Equal(t, "claude-haiku-4-5", values.CopilotCodingModel)
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

func TestValuesLoader_Load_WorktreeEnabled(t *testing.T) {
	t.Run("parse use_worktree true", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "config")
		require.NoError(t, os.WriteFile(cfgPath, []byte(`use_worktree = true`), 0o600))

		loader := newValuesLoader(defaultsFS)
		values, err := loader.Load("", cfgPath)
		require.NoError(t, err)
		assert.True(t, values.WorktreeEnabled)
		assert.True(t, values.WorktreeEnabledSet)
	})

	t.Run("parse use_worktree false", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "config")
		require.NoError(t, os.WriteFile(cfgPath, []byte(`use_worktree = false`), 0o600))

		loader := newValuesLoader(defaultsFS)
		values, err := loader.Load("", cfgPath)
		require.NoError(t, err)
		assert.False(t, values.WorktreeEnabled)
		assert.True(t, values.WorktreeEnabledSet)
	})

	t.Run("not set uses default false", func(t *testing.T) {
		loader := newValuesLoader(defaultsFS)
		values, err := loader.Load("", "")
		require.NoError(t, err)
		assert.False(t, values.WorktreeEnabled)
		// embedded defaults don't have use_worktree uncommented, so WorktreeEnabledSet is false
		assert.False(t, values.WorktreeEnabledSet)
	})

	t.Run("local overrides global", func(t *testing.T) {
		tmpDir := t.TempDir()
		globalCfg := filepath.Join(tmpDir, "global")
		localCfg := filepath.Join(tmpDir, "local")
		require.NoError(t, os.WriteFile(globalCfg, []byte(`use_worktree = false`), 0o600))
		require.NoError(t, os.WriteFile(localCfg, []byte(`use_worktree = true`), 0o600))

		loader := newValuesLoader(defaultsFS)
		values, err := loader.Load(localCfg, globalCfg)
		require.NoError(t, err)
		assert.True(t, values.WorktreeEnabled)
		assert.True(t, values.WorktreeEnabledSet)
	})

	t.Run("invalid value returns error", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "config")
		require.NoError(t, os.WriteFile(cfgPath, []byte(`use_worktree = notabool`), 0o600))

		loader := newValuesLoader(defaultsFS)
		_, err := loader.Load("", cfgPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid use_worktree")
	})
}

func TestValues_mergeFrom_WorktreeEnabled(t *testing.T) {
	t.Run("set flag merges", func(t *testing.T) {
		dst := Values{WorktreeEnabled: false, WorktreeEnabledSet: false}
		src := Values{WorktreeEnabled: true, WorktreeEnabledSet: true}
		dst.mergeFrom(&src)
		assert.True(t, dst.WorktreeEnabled)
		assert.True(t, dst.WorktreeEnabledSet)
	})

	t.Run("unset flag does not merge", func(t *testing.T) {
		dst := Values{WorktreeEnabled: true, WorktreeEnabledSet: true}
		src := Values{WorktreeEnabled: false, WorktreeEnabledSet: false}
		dst.mergeFrom(&src)
		assert.True(t, dst.WorktreeEnabled)
		assert.True(t, dst.WorktreeEnabledSet)
	})

	t.Run("set flag can disable", func(t *testing.T) {
		dst := Values{WorktreeEnabled: true, WorktreeEnabledSet: true}
		src := Values{WorktreeEnabled: false, WorktreeEnabledSet: true}
		dst.mergeFrom(&src)
		assert.False(t, dst.WorktreeEnabled)
		assert.True(t, dst.WorktreeEnabledSet)
	})
}

func TestValuesLoader_Load_MaxIterations(t *testing.T) {
	t.Run("parse max_iterations", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "config")
		require.NoError(t, os.WriteFile(cfgPath, []byte(`max_iterations = 100`), 0o600))

		loader := newValuesLoader(defaultsFS)
		values, err := loader.Load("", cfgPath)
		require.NoError(t, err)
		assert.Equal(t, 100, values.MaxIterations)
		assert.True(t, values.MaxIterationsSet)
	})

	t.Run("not set uses default zero", func(t *testing.T) {
		loader := newValuesLoader(defaultsFS)
		values, err := loader.Load("", "")
		require.NoError(t, err)
		assert.Equal(t, 0, values.MaxIterations)
		assert.False(t, values.MaxIterationsSet)
	})

	t.Run("local overrides global", func(t *testing.T) {
		tmpDir := t.TempDir()
		globalCfg := filepath.Join(tmpDir, "global")
		localCfg := filepath.Join(tmpDir, "local")
		require.NoError(t, os.WriteFile(globalCfg, []byte(`max_iterations = 200`), 0o600))
		require.NoError(t, os.WriteFile(localCfg, []byte(`max_iterations = 25`), 0o600))

		loader := newValuesLoader(defaultsFS)
		values, err := loader.Load(localCfg, globalCfg)
		require.NoError(t, err)
		assert.Equal(t, 25, values.MaxIterations)
		assert.True(t, values.MaxIterationsSet)
	})

	t.Run("minimum value is 1", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "config")
		require.NoError(t, os.WriteFile(cfgPath, []byte(`max_iterations = 1`), 0o600))

		loader := newValuesLoader(defaultsFS)
		values, err := loader.Load("", cfgPath)
		require.NoError(t, err)
		assert.Equal(t, 1, values.MaxIterations)
		assert.True(t, values.MaxIterationsSet)
	})
}

func TestValues_mergeFrom_MaxIterations(t *testing.T) {
	t.Run("set flag merges", func(t *testing.T) {
		dst := Values{MaxIterations: 50, MaxIterationsSet: false}
		src := Values{MaxIterations: 100, MaxIterationsSet: true}
		dst.mergeFrom(&src)
		assert.Equal(t, 100, dst.MaxIterations)
		assert.True(t, dst.MaxIterationsSet)
	})

	t.Run("unset flag does not merge", func(t *testing.T) {
		dst := Values{MaxIterations: 100, MaxIterationsSet: true}
		src := Values{MaxIterations: 0, MaxIterationsSet: false}
		dst.mergeFrom(&src)
		assert.Equal(t, 100, dst.MaxIterations)
		assert.True(t, dst.MaxIterationsSet)
	})
}

func TestValuesLoader_Load_AllValuesFromUserConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config")

	configContent := `
copilot_command = /custom/copilot
copilot_args = --custom
copilot_coding_model = claude-sonnet-4-6
copilot_review_model = custom-model
external_review_tool = none
iteration_delay_ms = 500
task_retry_count = 5
max_iterations = 75
plans_dir = my/plans
`
	require.NoError(t, os.WriteFile(configPath, []byte(configContent), 0o600))

	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load("", configPath)
	require.NoError(t, err)

	assert.Equal(t, "/custom/copilot", values.CopilotCommand)
	assert.Equal(t, "--custom", values.CopilotArgs)
	assert.Equal(t, "claude-sonnet-4-6", values.CopilotCodingModel)
	assert.Equal(t, "custom-model", values.CopilotReviewModel)
	assert.Equal(t, "none", values.ExternalReviewTool)
	assert.Equal(t, 500, values.IterationDelayMs)
	assert.Equal(t, 5, values.TaskRetryCount)
	assert.True(t, values.TaskRetryCountSet)
	assert.Equal(t, 75, values.MaxIterations)
	assert.True(t, values.MaxIterationsSet)
	assert.Equal(t, "my/plans", values.PlansDir)
}

func TestValues_mergeFrom(t *testing.T) {
	t.Run("merge non-empty values", func(t *testing.T) {
		dst := Values{
			CopilotCommand: "dst-copilot",
			PlansDir:       "dst-plans",
		}
		src := Values{
			CopilotCommand: "src-copilot",
			CopilotArgs:    "src-args",
		}
		dst.mergeFrom(&src)

		assert.Equal(t, "src-copilot", dst.CopilotCommand)
		assert.Equal(t, "src-args", dst.CopilotArgs)
		assert.Equal(t, "dst-plans", dst.PlansDir)
	})

	t.Run("empty source doesn't overwrite", func(t *testing.T) {
		dst := Values{
			CopilotCommand: "dst-copilot",
			PlansDir:       "dst-plans",
		}
		src := Values{
			CopilotCommand: "", // empty, shouldn't overwrite
		}
		dst.mergeFrom(&src)

		assert.Equal(t, "dst-copilot", dst.CopilotCommand)
		assert.Equal(t, "dst-plans", dst.PlansDir)
	})

	t.Run("set flags control int merging", func(t *testing.T) {
		dst := Values{
			IterationDelayMs:    2000,
			IterationDelayMsSet: true,
			TaskRetryCount:      5,
			TaskRetryCountSet:   true,
			CopilotCodingModel:  "claude-opus-4-6",
			CopilotReviewModel:  "gpt-5.2-codex",
		}
		src := Values{
			IterationDelayMs:    0,
			IterationDelayMsSet: true,
			TaskRetryCount:      0,
			TaskRetryCountSet:   true,
			CopilotCodingModel:  "claude-sonnet-4-6",
			CopilotReviewModel:  "custom-model",
		}
		dst.mergeFrom(&src)

		assert.Equal(t, 0, dst.IterationDelayMs)
		assert.Equal(t, 0, dst.TaskRetryCount)
		assert.Equal(t, "claude-sonnet-4-6", dst.CopilotCodingModel)
		assert.Equal(t, "custom-model", dst.CopilotReviewModel)
	})

	t.Run("unset flags don't merge", func(t *testing.T) {
		dst := Values{
			IterationDelayMs:    2000,
			IterationDelayMsSet: true,
			TaskRetryCount:      5,
			TaskRetryCountSet:   true,
			CopilotCodingModel:  "claude-opus-4-6",
			CopilotReviewModel:  "gpt-5.2-codex",
		}
		src := Values{
			IterationDelayMs:    0,
			IterationDelayMsSet: false, // not explicitly set
			TaskRetryCount:      0,
			TaskRetryCountSet:   false, // not explicitly set
			CopilotCodingModel:  "",    // empty, shouldn't overwrite
			CopilotReviewModel:  "",    // empty, shouldn't overwrite
		}
		dst.mergeFrom(&src)

		assert.Equal(t, 2000, dst.IterationDelayMs)
		assert.Equal(t, 5, dst.TaskRetryCount)
		assert.Equal(t, "claude-opus-4-6", dst.CopilotCodingModel)
		assert.Equal(t, "gpt-5.2-codex", dst.CopilotReviewModel)
	})
}

func TestValuesLoader_parseValuesFromBytes(t *testing.T) {
	vl := &valuesLoader{embedFS: defaultsFS}

	t.Run("full config", func(t *testing.T) {
		data := []byte(`
copilot_command = /custom/copilot
copilot_args = --custom-arg
copilot_coding_model = claude-sonnet-4-6
copilot_review_model = gpt-5
external_review_tool = none
iteration_delay_ms = 5000
task_retry_count = 3
plans_dir = custom/plans
`)
		values, err := vl.parseValuesFromBytes(data)
		require.NoError(t, err)

		assert.Equal(t, "/custom/copilot", values.CopilotCommand)
		assert.Equal(t, "--custom-arg", values.CopilotArgs)
		assert.Equal(t, "claude-sonnet-4-6", values.CopilotCodingModel)
		assert.Equal(t, "gpt-5", values.CopilotReviewModel)
		assert.Equal(t, "none", values.ExternalReviewTool)
		assert.Equal(t, 5000, values.IterationDelayMs)
		assert.Equal(t, 3, values.TaskRetryCount)
		assert.True(t, values.TaskRetryCountSet)
		assert.Equal(t, "custom/plans", values.PlansDir)
	})

	t.Run("empty config", func(t *testing.T) {
		data := []byte("")
		values, err := vl.parseValuesFromBytes(data)
		require.NoError(t, err)

		assert.Empty(t, values.CopilotCommand)
		assert.Empty(t, values.CopilotCodingModel)
		assert.Empty(t, values.CopilotReviewModel)
	})

	t.Run("bool values for finalize_enabled", func(t *testing.T) {
		tests := []struct {
			name     string
			input    string
			expected bool
		}{
			{"true lowercase", "finalize_enabled = true", true},
			{"TRUE uppercase", "finalize_enabled = TRUE", true},
			{"false lowercase", "finalize_enabled = false", false},
			{"yes", "finalize_enabled = yes", true},
			{"no", "finalize_enabled = no", false},
			{"1", "finalize_enabled = 1", true},
			{"0", "finalize_enabled = 0", false},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				values, err := vl.parseValuesFromBytes([]byte(tc.input))
				require.NoError(t, err)
				assert.Equal(t, tc.expected, values.FinalizeEnabled)
				assert.True(t, values.FinalizeEnabledSet)
			})
		}
	})
}

func TestValuesLoader_parseValuesFromFile_PermissionDenied(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config")
	require.NoError(t, os.WriteFile(configPath, []byte("copilot_command = test"), 0o600))

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
		name     string
		input    string
		expected []string
	}{
		{
			name:     "single pattern",
			input:    "copilot_error_patterns = rate limit",
			expected: []string{"rate limit"},
		},
		{
			name:     "multiple patterns comma-separated",
			input:    "copilot_error_patterns = rate limit,quota exceeded,too many requests",
			expected: []string{"rate limit", "quota exceeded", "too many requests"},
		},
		{
			name:     "whitespace trimming around commas",
			input:    "copilot_error_patterns =  pattern1 ,  pattern2  , pattern3 ",
			expected: []string{"pattern1", "pattern2", "pattern3"},
		},
		{
			name:     "empty patterns filtered out",
			input:    "copilot_error_patterns = pattern1,,pattern2,  ,pattern3",
			expected: []string{"pattern1", "pattern2", "pattern3"},
		},
		{
			name:     "empty value",
			input:    "copilot_error_patterns = ",
			expected: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			values, err := vl.parseValuesFromBytes([]byte(tc.input))
			require.NoError(t, err)
			assert.Equal(t, tc.expected, values.CopilotErrorPatterns)
		})
	}
}

func TestValues_mergeFrom_ErrorPatterns(t *testing.T) {
	t.Run("merge error patterns when src has values", func(t *testing.T) {
		dst := Values{
			CopilotErrorPatterns: []string{"dst pattern"},
		}
		src := Values{
			CopilotErrorPatterns: []string{"src pattern 1", "src pattern 2"},
		}
		dst.mergeFrom(&src)

		assert.Equal(t, []string{"src pattern 1", "src pattern 2"}, dst.CopilotErrorPatterns)
	})

	t.Run("preserve dst when src is empty", func(t *testing.T) {
		dst := Values{
			CopilotErrorPatterns: []string{"dst pattern"},
		}
		src := Values{
			CopilotErrorPatterns: nil,
		}
		dst.mergeFrom(&src)

		assert.Equal(t, []string{"dst pattern"}, dst.CopilotErrorPatterns)
	})
}

func TestValuesLoader_Load_ErrorPatternsOverride(t *testing.T) {
	tmpDir := t.TempDir()
	globalConfig := filepath.Join(tmpDir, "global")
	localConfig := filepath.Join(tmpDir, "local")

	// global has one set of patterns
	globalContent := `copilot_error_patterns = global pattern 1, global pattern 2`
	require.NoError(t, os.WriteFile(globalConfig, []byte(globalContent), 0o600))

	// local overrides with different patterns
	localContent := `copilot_error_patterns = local pattern`
	require.NoError(t, os.WriteFile(localConfig, []byte(localContent), 0o600))

	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load(localConfig, globalConfig)
	require.NoError(t, err)

	// local should override global completely (not merge)
	assert.Equal(t, []string{"local pattern"}, values.CopilotErrorPatterns)
}

func TestValuesLoader_Load_AllCommentedConfigFallsBackToEmbedded(t *testing.T) {
	tmpDir := t.TempDir()
	globalConfig := filepath.Join(tmpDir, "config")

	// config with only comments and whitespace - should fall back to embedded
	commentedConfig := `# this is a commented config file
# all lines are comments
# copilot_command = /custom/copilot

# empty lines below

`
	require.NoError(t, os.WriteFile(globalConfig, []byte(commentedConfig), 0o600))

	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load("", globalConfig)
	require.NoError(t, err)

	// should fall back to embedded defaults since file has no actual content
	assert.Equal(t, "copilot", values.CopilotCommand)
	assert.Equal(t, "--allow-all --no-ask-user --output-format json", values.CopilotArgs)
	assert.Equal(t, "claude-opus-4-6", values.CopilotCodingModel)
	assert.Equal(t, "gpt-5.2-codex", values.CopilotReviewModel)
	assert.Equal(t, "docs/plans", values.PlansDir)
}

func TestValuesLoader_Load_PartiallyCommentedConfigUsesUncommentedValues(t *testing.T) {
	tmpDir := t.TempDir()
	globalConfig := filepath.Join(tmpDir, "config")

	// config with some commented and some uncommented lines
	partialConfig := `# this line is a comment
copilot_command = /custom/copilot
# copilot_args is commented out
# copilot_args = --some-args
plans_dir = custom/plans
`
	require.NoError(t, os.WriteFile(globalConfig, []byte(partialConfig), 0o600))

	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load("", globalConfig)
	require.NoError(t, err)

	// uncommented values should be used
	assert.Equal(t, "/custom/copilot", values.CopilotCommand)
	assert.Equal(t, "custom/plans", values.PlansDir)

	// commented-out values should fall back to embedded defaults
	assert.Equal(t, "--allow-all --no-ask-user --output-format json", values.CopilotArgs)
}

func TestValuesLoader_Load_LocalAllCommentedGlobalHasContent(t *testing.T) {
	tmpDir := t.TempDir()
	globalConfig := filepath.Join(tmpDir, "global-config")
	localConfig := filepath.Join(tmpDir, "local-config")

	// global has actual content
	globalContent := `copilot_command = /global/copilot
plans_dir = global/plans
`
	require.NoError(t, os.WriteFile(globalConfig, []byte(globalContent), 0o600))

	// local is all-commented (installed template)
	localCommented := `# local config template
# uncomment values to customize
# copilot_command = /local/copilot
`
	require.NoError(t, os.WriteFile(localConfig, []byte(localCommented), 0o600))

	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load(localConfig, globalConfig)
	require.NoError(t, err)

	// local all-commented falls back, so global values should be used
	assert.Equal(t, "/global/copilot", values.CopilotCommand)
	assert.Equal(t, "global/plans", values.PlansDir)
}

func TestValuesLoader_Load_BothAllCommentedFallsBackToEmbedded(t *testing.T) {
	tmpDir := t.TempDir()
	globalConfig := filepath.Join(tmpDir, "global-config")
	localConfig := filepath.Join(tmpDir, "local-config")

	// both files are all-commented templates
	commentedTemplate := `# config template
# uncomment values to customize
# copilot_command = /custom/copilot
# plans_dir = custom/plans
`
	require.NoError(t, os.WriteFile(globalConfig, []byte(commentedTemplate), 0o600))
	require.NoError(t, os.WriteFile(localConfig, []byte(commentedTemplate), 0o600))

	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load(localConfig, globalConfig)
	require.NoError(t, err)

	// both all-commented, should fall back to embedded defaults
	assert.Equal(t, "copilot", values.CopilotCommand)
	assert.Equal(t, "docs/plans", values.PlansDir)
	assert.Equal(t, "copilot", values.ExternalReviewTool)
}

func TestValuesLoader_Load_ExternalReviewTool(t *testing.T) {
	tests := []struct {
		name         string
		config       string
		expectedTool string
	}{
		{name: "copilot tool", config: "external_review_tool = copilot", expectedTool: "copilot"},
		{name: "custom tool", config: "external_review_tool = custom", expectedTool: "custom"},
		{name: "none tool", config: "external_review_tool = none", expectedTool: "none"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			configPath := filepath.Join(tmpDir, "config")
			require.NoError(t, os.WriteFile(configPath, []byte(tc.config), 0o600))

			loader := newValuesLoader(defaultsFS)
			values, err := loader.Load("", configPath)
			require.NoError(t, err)

			assert.Equal(t, tc.expectedTool, values.ExternalReviewTool)
		})
	}
}

func TestValuesLoader_Load_CustomReviewScript(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config")

	configContent := `
external_review_tool = custom
custom_review_script = /path/to/my-review.sh
`
	require.NoError(t, os.WriteFile(configPath, []byte(configContent), 0o600))

	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load("", configPath)
	require.NoError(t, err)

	assert.Equal(t, "custom", values.ExternalReviewTool)
	assert.Equal(t, "/path/to/my-review.sh", values.CustomReviewScript)
}

func TestValuesLoader_Load_CustomReviewScript_TildeExpansion(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config")

	configContent := `custom_review_script = ~/.config/ralphex/scripts/my-review.sh`
	require.NoError(t, os.WriteFile(configPath, []byte(configContent), 0o600))

	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load("", configPath)
	require.NoError(t, err)

	// tilde should be expanded to home directory
	home, homeErr := os.UserHomeDir()
	require.NoError(t, homeErr)
	expected := home + "/.config/ralphex/scripts/my-review.sh"
	assert.Equal(t, expected, values.CustomReviewScript)
}

func TestValuesLoader_Load_CustomReviewScript_NoTildeNoChange(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config")

	configContent := `custom_review_script = /absolute/path/to/script.sh`
	require.NoError(t, os.WriteFile(configPath, []byte(configContent), 0o600))

	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load("", configPath)
	require.NoError(t, err)

	// absolute path should not be changed
	assert.Equal(t, "/absolute/path/to/script.sh", values.CustomReviewScript)
}

func TestExpandTilde(t *testing.T) {
	home, homeErr := os.UserHomeDir()
	require.NoError(t, homeErr)

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "tilde with slash", input: "~/some/path", expected: home + "/some/path"},
		{name: "tilde with nested path", input: "~/.config/ralphex/script.sh", expected: home + "/.config/ralphex/script.sh"},
		{name: "absolute path unchanged", input: "/absolute/path", expected: "/absolute/path"},
		{name: "relative path unchanged", input: "relative/path", expected: "relative/path"},
		{name: "empty string", input: "", expected: ""},
		{name: "tilde only no slash", input: "~noslash", expected: "~noslash"}, // ~ without / is a different user, not expanded
		{name: "tilde at end", input: "path/with/tilde~", expected: "path/with/tilde~"},
		{name: "tilde in middle", input: "path/~/middle", expected: "path/~/middle"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := expandTilde(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestValuesLoader_parseValuesFromBytes_NotifyFields(t *testing.T) {
	vl := &valuesLoader{embedFS: defaultsFS}

	t.Run("all notification fields", func(t *testing.T) {
		data := []byte(`
notify_channels = telegram, email, webhook, slack, custom
notify_on_error = true
notify_on_complete = false
notify_timeout_ms = 15000
notify_telegram_token = bot123:ABC
notify_telegram_chat = -100123456
notify_slack_token = xoxb-slack-token
notify_slack_channel = general
notify_smtp_host = smtp.example.com
notify_smtp_port = 587
notify_smtp_username = user@example.com
notify_smtp_password = secret
notify_smtp_starttls = true
notify_email_from = noreply@example.com
notify_email_to = dev@example.com, ops@example.com
notify_webhook_urls = https://hook1.example.com, https://hook2.example.com
notify_custom_script = /usr/local/bin/notify.sh
`)
		values, err := vl.parseValuesFromBytes(data)
		require.NoError(t, err)

		assert.Equal(t, []string{"telegram", "email", "webhook", "slack", "custom"}, values.NotifyChannels)
		assert.True(t, values.NotifyChannelsSet)
		assert.True(t, values.NotifyOnError)
		assert.True(t, values.NotifyOnErrorSet)
		assert.False(t, values.NotifyOnComplete)
		assert.True(t, values.NotifyOnCompleteSet)
		assert.Equal(t, 15000, values.NotifyTimeoutMs)
		assert.True(t, values.NotifyTimeoutMsSet)
		assert.Equal(t, "bot123:ABC", values.NotifyTelegramToken)
		assert.Equal(t, "-100123456", values.NotifyTelegramChat)
		assert.Equal(t, "xoxb-slack-token", values.NotifySlackToken)
		assert.Equal(t, "general", values.NotifySlackChannel)
		assert.Equal(t, "smtp.example.com", values.NotifySMTPHost)
		assert.Equal(t, 587, values.NotifySMTPPort)
		assert.True(t, values.NotifySMTPPortSet)
		assert.Equal(t, "user@example.com", values.NotifySMTPUsername)
		assert.Equal(t, "secret", values.NotifySMTPPassword)
		assert.True(t, values.NotifySMTPStartTLS)
		assert.True(t, values.NotifySMTPStartTLSSet)
		assert.Equal(t, "noreply@example.com", values.NotifyEmailFrom)
		assert.Equal(t, []string{"dev@example.com", "ops@example.com"}, values.NotifyEmailTo)
		assert.True(t, values.NotifyEmailToSet)
		assert.Equal(t, []string{"https://hook1.example.com", "https://hook2.example.com"}, values.NotifyWebhookURLs)
		assert.True(t, values.NotifyWebhookURLsSet)
		assert.Equal(t, "/usr/local/bin/notify.sh", values.NotifyCustomScript)
	})

	t.Run("empty notify config", func(t *testing.T) {
		data := []byte("")
		values, err := vl.parseValuesFromBytes(data)
		require.NoError(t, err)

		assert.Empty(t, values.NotifyChannels)
		assert.False(t, values.NotifyChannelsSet)
		assert.False(t, values.NotifyOnErrorSet)
		assert.False(t, values.NotifyOnCompleteSet)
		assert.False(t, values.NotifyTimeoutMsSet)
		assert.Empty(t, values.NotifyTelegramToken)
	})

	t.Run("empty notify_channels key disables notifications", func(t *testing.T) {
		data := []byte(`notify_channels =`)
		values, err := vl.parseValuesFromBytes(data)
		require.NoError(t, err)

		assert.Empty(t, values.NotifyChannels)
		assert.True(t, values.NotifyChannelsSet, "set flag should be true when key is present but empty")
	})

	t.Run("empty email_to and webhook_urls keys set flags", func(t *testing.T) {
		data := []byte("notify_email_to =\nnotify_webhook_urls =\n")
		values, err := vl.parseValuesFromBytes(data)
		require.NoError(t, err)

		assert.Empty(t, values.NotifyEmailTo)
		assert.True(t, values.NotifyEmailToSet)
		assert.Empty(t, values.NotifyWebhookURLs)
		assert.True(t, values.NotifyWebhookURLsSet)
	})

	t.Run("tilde expansion for custom script", func(t *testing.T) {
		data := []byte(`notify_custom_script = ~/.config/ralphex/scripts/notify.sh`)
		values, err := vl.parseValuesFromBytes(data)
		require.NoError(t, err)

		home, homeErr := os.UserHomeDir()
		require.NoError(t, homeErr)
		assert.Equal(t, home+"/.config/ralphex/scripts/notify.sh", values.NotifyCustomScript)
	})
}

func TestValuesLoader_Load_InvalidNotifyConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		errPart string
	}{
		{name: "invalid notify_on_error", config: "notify_on_error = maybe", errPart: "notify_on_error"},
		{name: "invalid notify_on_complete", config: "notify_on_complete = nope", errPart: "notify_on_complete"},
		{name: "invalid notify_timeout_ms", config: "notify_timeout_ms = abc", errPart: "notify_timeout_ms"},
		{name: "negative notify_timeout_ms", config: "notify_timeout_ms = -100", errPart: "notify_timeout_ms"},
		{name: "invalid notify_smtp_port", config: "notify_smtp_port = xyz", errPart: "notify_smtp_port"},
		{name: "negative notify_smtp_port", config: "notify_smtp_port = -1", errPart: "notify_smtp_port"},
		{name: "invalid notify_smtp_starttls", config: "notify_smtp_starttls = dunno", errPart: "notify_smtp_starttls"},
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

func TestValues_mergeFrom_NotifyFields(t *testing.T) {
	t.Run("merge notify channels and strings", func(t *testing.T) {
		dst := Values{NotifyChannels: []string{"telegram"}, NotifyChannelsSet: true, NotifyTelegramToken: "old-token"}
		src := Values{NotifyChannels: []string{"slack", "webhook"}, NotifyChannelsSet: true, NotifyTelegramToken: "new-token"}
		dst.mergeFrom(&src)

		assert.Equal(t, []string{"slack", "webhook"}, dst.NotifyChannels)
		assert.Equal(t, "new-token", dst.NotifyTelegramToken)
	})

	t.Run("empty source preserves dst notify fields", func(t *testing.T) {
		dst := Values{
			NotifyChannels:      []string{"telegram"},
			NotifyTelegramToken: "keep-token",
			NotifySMTPHost:      "keep-host",
		}
		src := Values{}
		dst.mergeFrom(&src)

		assert.Equal(t, []string{"telegram"}, dst.NotifyChannels)
		assert.Equal(t, "keep-token", dst.NotifyTelegramToken)
		assert.Equal(t, "keep-host", dst.NotifySMTPHost)
	})

	t.Run("set flags control bool and int notify merging", func(t *testing.T) {
		dst := Values{NotifyOnError: true, NotifyOnErrorSet: true, NotifyTimeoutMs: 10000, NotifyTimeoutMsSet: true}
		src := Values{NotifyOnError: false, NotifyOnErrorSet: true, NotifyTimeoutMs: 0, NotifyTimeoutMsSet: true}
		dst.mergeFrom(&src)

		assert.False(t, dst.NotifyOnError)
		assert.Equal(t, 0, dst.NotifyTimeoutMs)
	})

	t.Run("unset flags dont merge notify bools", func(t *testing.T) {
		dst := Values{NotifyOnError: true, NotifyOnErrorSet: true, NotifySMTPPort: 587, NotifySMTPPortSet: true}
		src := Values{NotifyOnError: false, NotifyOnErrorSet: false, NotifySMTPPort: 0, NotifySMTPPortSet: false}
		dst.mergeFrom(&src)

		assert.True(t, dst.NotifyOnError)
		assert.Equal(t, 587, dst.NotifySMTPPort)
	})

	t.Run("merge all notify string fields", func(t *testing.T) {
		dst := Values{}
		src := Values{
			NotifyTelegramChat: "chat-123",
			NotifySlackToken:   "slack-tok",
			NotifySlackChannel: "dev",
			NotifySMTPHost:     "smtp.test.com",
			NotifySMTPUsername: "user",
			NotifySMTPPassword: "pass",
			NotifyEmailFrom:    "from@test.com",
			NotifyCustomScript: "/bin/script.sh",
		}
		dst.mergeFrom(&src)

		assert.Equal(t, "chat-123", dst.NotifyTelegramChat)
		assert.Equal(t, "slack-tok", dst.NotifySlackToken)
		assert.Equal(t, "dev", dst.NotifySlackChannel)
		assert.Equal(t, "smtp.test.com", dst.NotifySMTPHost)
		assert.Equal(t, "user", dst.NotifySMTPUsername)
		assert.Equal(t, "pass", dst.NotifySMTPPassword)
		assert.Equal(t, "from@test.com", dst.NotifyEmailFrom)
		assert.Equal(t, "/bin/script.sh", dst.NotifyCustomScript)
	})

	t.Run("merge notify slice fields", func(t *testing.T) {
		dst := Values{NotifyEmailTo: []string{"old@test.com"}, NotifyWebhookURLs: []string{"https://old.hook"}}
		src := Values{NotifyEmailTo: []string{"new@test.com", "new2@test.com"}, NotifyEmailToSet: true, NotifyWebhookURLs: []string{"https://new.hook"}, NotifyWebhookURLsSet: true}
		dst.mergeFrom(&src)

		assert.Equal(t, []string{"new@test.com", "new2@test.com"}, dst.NotifyEmailTo)
		assert.Equal(t, []string{"https://new.hook"}, dst.NotifyWebhookURLs)
	})

	t.Run("empty channels with set flag disables inherited notifications", func(t *testing.T) {
		dst := Values{NotifyChannels: []string{"telegram", "slack"}, NotifyChannelsSet: true}
		src := Values{NotifyChannelsSet: true} // explicitly set to empty
		dst.mergeFrom(&src)

		assert.Empty(t, dst.NotifyChannels)
		assert.True(t, dst.NotifyChannelsSet)
	})

	t.Run("empty email_to with set flag disables inherited recipients", func(t *testing.T) {
		dst := Values{NotifyEmailTo: []string{"user@test.com"}, NotifyEmailToSet: true}
		src := Values{NotifyEmailToSet: true} // explicitly set to empty
		dst.mergeFrom(&src)

		assert.Empty(t, dst.NotifyEmailTo)
		assert.True(t, dst.NotifyEmailToSet)
	})

	t.Run("empty webhook_urls with set flag disables inherited urls", func(t *testing.T) {
		dst := Values{NotifyWebhookURLs: []string{"https://old.hook"}, NotifyWebhookURLsSet: true}
		src := Values{NotifyWebhookURLsSet: true} // explicitly set to empty
		dst.mergeFrom(&src)

		assert.Empty(t, dst.NotifyWebhookURLs)
		assert.True(t, dst.NotifyWebhookURLsSet)
	})

	t.Run("unset channels flag preserves dst channels", func(t *testing.T) {
		dst := Values{NotifyChannels: []string{"telegram"}, NotifyChannelsSet: true}
		src := Values{} // not set at all
		dst.mergeFrom(&src)

		assert.Equal(t, []string{"telegram"}, dst.NotifyChannels)
	})

	t.Run("merge smtp starttls set flag", func(t *testing.T) {
		dst := Values{NotifySMTPStartTLS: false, NotifySMTPStartTLSSet: false}
		src := Values{NotifySMTPStartTLS: true, NotifySMTPStartTLSSet: true}
		dst.mergeFrom(&src)

		assert.True(t, dst.NotifySMTPStartTLS)
		assert.True(t, dst.NotifySMTPStartTLSSet)
	})

	t.Run("merge notify on complete set flag", func(t *testing.T) {
		dst := Values{NotifyOnComplete: true, NotifyOnCompleteSet: true}
		src := Values{NotifyOnComplete: false, NotifyOnCompleteSet: true}
		dst.mergeFrom(&src)

		assert.False(t, dst.NotifyOnComplete)
		assert.True(t, dst.NotifyOnCompleteSet)
	})
}

func TestValuesLoader_Load_NotifyLocalOverridesGlobal(t *testing.T) {
	tmpDir := t.TempDir()
	globalConfig := filepath.Join(tmpDir, "global")
	localConfig := filepath.Join(tmpDir, "local")

	globalContent := `
notify_channels = telegram
notify_telegram_token = global-token
notify_timeout_ms = 10000
`
	require.NoError(t, os.WriteFile(globalConfig, []byte(globalContent), 0o600))

	localContent := `
notify_channels = slack, webhook
notify_timeout_ms = 5000
`
	require.NoError(t, os.WriteFile(localConfig, []byte(localContent), 0o600))

	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load(localConfig, globalConfig)
	require.NoError(t, err)

	// local overrides
	assert.Equal(t, []string{"slack", "webhook"}, values.NotifyChannels)
	assert.Equal(t, 5000, values.NotifyTimeoutMs)

	// global preserved when not overridden
	assert.Equal(t, "global-token", values.NotifyTelegramToken)
}

func TestValuesLoader_Load_EmptyLocalDisablesGlobalNotifications(t *testing.T) {
	tmpDir := t.TempDir()
	globalConfig := filepath.Join(tmpDir, "global")
	localConfig := filepath.Join(tmpDir, "local")

	globalContent := `
notify_channels = telegram
notify_telegram_token = global-token
notify_telegram_chat = -100123
`
	require.NoError(t, os.WriteFile(globalConfig, []byte(globalContent), 0o600))

	// local config explicitly sets notify_channels to empty to disable notifications
	localContent := `notify_channels =`
	require.NoError(t, os.WriteFile(localConfig, []byte(localContent), 0o600))

	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load(localConfig, globalConfig)
	require.NoError(t, err)

	assert.Empty(t, values.NotifyChannels, "local empty notify_channels should disable global notifications")
	assert.True(t, values.NotifyChannelsSet)
	// global token still preserved (only channels are disabled)
	assert.Equal(t, "global-token", values.NotifyTelegramToken)
}

func TestValuesLoader_Load_LocalOverridesExternalReviewTool(t *testing.T) {
	tmpDir := t.TempDir()
	globalConfig := filepath.Join(tmpDir, "global")
	localConfig := filepath.Join(tmpDir, "local")

	require.NoError(t, os.WriteFile(globalConfig, []byte(`external_review_tool = copilot`), 0o600))
	require.NoError(t, os.WriteFile(localConfig, []byte(`external_review_tool = none`), 0o600))

	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load(localConfig, globalConfig)
	require.NoError(t, err)

	assert.Equal(t, "none", values.ExternalReviewTool)
}

func TestValuesLoader_Load_DefaultBranch(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config")

	configContent := `default_branch = dev`
	require.NoError(t, os.WriteFile(configPath, []byte(configContent), 0o600))

	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load("", configPath)
	require.NoError(t, err)

	assert.Equal(t, "dev", values.DefaultBranch)
}

func TestValuesLoader_Load_DefaultBranch_Whitespace(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config")

	// whitespace should be trimmed
	configContent := `default_branch =   feature-branch   `
	require.NoError(t, os.WriteFile(configPath, []byte(configContent), 0o600))

	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load("", configPath)
	require.NoError(t, err)

	assert.Equal(t, "feature-branch", values.DefaultBranch)
}

func TestValuesLoader_Load_LocalOverridesGlobalDefaultBranch(t *testing.T) {
	tmpDir := t.TempDir()
	globalConfig := filepath.Join(tmpDir, "global")
	localConfig := filepath.Join(tmpDir, "local")

	require.NoError(t, os.WriteFile(globalConfig, []byte(`default_branch = main`), 0o600))
	require.NoError(t, os.WriteFile(localConfig, []byte(`default_branch = dev`), 0o600))

	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load(localConfig, globalConfig)
	require.NoError(t, err)

	assert.Equal(t, "dev", values.DefaultBranch)
}

func TestValues_mergeFrom_DefaultBranch(t *testing.T) {
	t.Run("merge default branch", func(t *testing.T) {
		dst := Values{DefaultBranch: "main"}
		src := Values{DefaultBranch: "dev"}
		dst.mergeFrom(&src)
		assert.Equal(t, "dev", dst.DefaultBranch)
	})

	t.Run("empty source doesn't overwrite default branch", func(t *testing.T) {
		dst := Values{DefaultBranch: "main"}
		src := Values{DefaultBranch: ""}
		dst.mergeFrom(&src)
		assert.Equal(t, "main", dst.DefaultBranch)
	})
}

func TestValues_mergeFrom_ExternalReviewFields(t *testing.T) {
	t.Run("merge external review tool", func(t *testing.T) {
		dst := Values{ExternalReviewTool: "copilot"}
		src := Values{ExternalReviewTool: "custom"}
		dst.mergeFrom(&src)
		assert.Equal(t, "custom", dst.ExternalReviewTool)
	})

	t.Run("empty source doesn't overwrite external review tool", func(t *testing.T) {
		dst := Values{ExternalReviewTool: "copilot"}
		src := Values{ExternalReviewTool: ""}
		dst.mergeFrom(&src)
		assert.Equal(t, "copilot", dst.ExternalReviewTool)
	})

	t.Run("merge custom review script", func(t *testing.T) {
		dst := Values{CustomReviewScript: "/old/script.sh"}
		src := Values{CustomReviewScript: "/new/script.sh"}
		dst.mergeFrom(&src)
		assert.Equal(t, "/new/script.sh", dst.CustomReviewScript)
	})

	t.Run("empty source doesn't overwrite custom review script", func(t *testing.T) {
		dst := Values{CustomReviewScript: "/old/script.sh"}
		src := Values{CustomReviewScript: ""}
		dst.mergeFrom(&src)
		assert.Equal(t, "/old/script.sh", dst.CustomReviewScript)
	})
}

func TestValuesLoader_Load_MaxExternalIterations(t *testing.T) {
	t.Run("parse valid value", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "config")
		require.NoError(t, os.WriteFile(cfgPath, []byte(`max_external_iterations = 7`), 0o600))

		loader := newValuesLoader(defaultsFS)
		values, err := loader.Load("", cfgPath)
		require.NoError(t, err)
		assert.Equal(t, 7, values.MaxExternalIterations)
	})

	t.Run("parse zero means auto", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "config")
		require.NoError(t, os.WriteFile(cfgPath, []byte(`max_external_iterations = 0`), 0o600))

		loader := newValuesLoader(defaultsFS)
		values, err := loader.Load("", cfgPath)
		require.NoError(t, err)
		assert.Equal(t, 0, values.MaxExternalIterations)
	})

	t.Run("negative returns error", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "config")
		require.NoError(t, os.WriteFile(cfgPath, []byte(`max_external_iterations = -1`), 0o600))

		loader := newValuesLoader(defaultsFS)
		_, err := loader.Load("", cfgPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "max_external_iterations")
	})

	t.Run("invalid value returns error", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "config")
		require.NoError(t, os.WriteFile(cfgPath, []byte(`max_external_iterations = abc`), 0o600))

		loader := newValuesLoader(defaultsFS)
		_, err := loader.Load("", cfgPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "max_external_iterations")
	})

	t.Run("not set defaults to zero", func(t *testing.T) {
		loader := newValuesLoader(defaultsFS)
		values, err := loader.Load("", "")
		require.NoError(t, err)
		assert.Equal(t, 0, values.MaxExternalIterations)
	})
}

func TestValues_mergeFrom_MaxExternalIterations(t *testing.T) {
	t.Run("non-zero overrides", func(t *testing.T) {
		dst := Values{MaxExternalIterations: 0}
		src := Values{MaxExternalIterations: 10}
		dst.mergeFrom(&src)
		assert.Equal(t, 10, dst.MaxExternalIterations)
	})

	t.Run("zero preserves existing", func(t *testing.T) {
		dst := Values{MaxExternalIterations: 10}
		src := Values{MaxExternalIterations: 0}
		dst.mergeFrom(&src)
		assert.Equal(t, 10, dst.MaxExternalIterations)
	})

	t.Run("global=10 local unset preserves 10", func(t *testing.T) {
		tmpDir := t.TempDir()
		globalCfg := filepath.Join(tmpDir, "global")
		localCfg := filepath.Join(tmpDir, "local")
		require.NoError(t, os.WriteFile(globalCfg, []byte(`max_external_iterations = 10`), 0o600))
		require.NoError(t, os.WriteFile(localCfg, []byte(``), 0o600))

		loader := newValuesLoader(defaultsFS)
		values, err := loader.Load(localCfg, globalCfg)
		require.NoError(t, err)
		assert.Equal(t, 10, values.MaxExternalIterations)
	})

	t.Run("local overrides global", func(t *testing.T) {
		tmpDir := t.TempDir()
		globalCfg := filepath.Join(tmpDir, "global")
		localCfg := filepath.Join(tmpDir, "local")
		require.NoError(t, os.WriteFile(globalCfg, []byte(`max_external_iterations = 10`), 0o600))
		require.NoError(t, os.WriteFile(localCfg, []byte(`max_external_iterations = 5`), 0o600))

		loader := newValuesLoader(defaultsFS)
		values, err := loader.Load(localCfg, globalCfg)
		require.NoError(t, err)
		assert.Equal(t, 5, values.MaxExternalIterations)
	})
}

func TestValuesLoader_Load_ReviewPatience(t *testing.T) {
	t.Run("parse valid value", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "config")
		require.NoError(t, os.WriteFile(cfgPath, []byte(`review_patience = 5`), 0o600))

		loader := newValuesLoader(defaultsFS)
		values, err := loader.Load("", cfgPath)
		require.NoError(t, err)
		assert.Equal(t, 5, values.ReviewPatience)
	})

	t.Run("parse zero means disabled", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "config")
		require.NoError(t, os.WriteFile(cfgPath, []byte(`review_patience = 0`), 0o600))

		loader := newValuesLoader(defaultsFS)
		values, err := loader.Load("", cfgPath)
		require.NoError(t, err)
		assert.Equal(t, 0, values.ReviewPatience)
	})

	t.Run("negative returns error", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "config")
		require.NoError(t, os.WriteFile(cfgPath, []byte(`review_patience = -1`), 0o600))

		loader := newValuesLoader(defaultsFS)
		_, err := loader.Load("", cfgPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "review_patience")
	})

	t.Run("invalid value returns error", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "config")
		require.NoError(t, os.WriteFile(cfgPath, []byte(`review_patience = abc`), 0o600))

		loader := newValuesLoader(defaultsFS)
		_, err := loader.Load("", cfgPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "review_patience")
	})

	t.Run("not set defaults to zero", func(t *testing.T) {
		loader := newValuesLoader(defaultsFS)
		values, err := loader.Load("", "")
		require.NoError(t, err)
		assert.Equal(t, 0, values.ReviewPatience)
	})
}

func TestValues_mergeFrom_ReviewPatience(t *testing.T) {
	t.Run("non-zero overrides", func(t *testing.T) {
		dst := Values{ReviewPatience: 0}
		src := Values{ReviewPatience: 5}
		dst.mergeFrom(&src)
		assert.Equal(t, 5, dst.ReviewPatience)
	})

	t.Run("zero preserves existing", func(t *testing.T) {
		dst := Values{ReviewPatience: 5}
		src := Values{ReviewPatience: 0}
		dst.mergeFrom(&src)
		assert.Equal(t, 5, dst.ReviewPatience)
	})

	t.Run("global=5 local unset preserves 5", func(t *testing.T) {
		tmpDir := t.TempDir()
		globalCfg := filepath.Join(tmpDir, "global")
		localCfg := filepath.Join(tmpDir, "local")
		require.NoError(t, os.WriteFile(globalCfg, []byte(`review_patience = 5`), 0o600))
		require.NoError(t, os.WriteFile(localCfg, []byte(``), 0o600))

		loader := newValuesLoader(defaultsFS)
		values, err := loader.Load(localCfg, globalCfg)
		require.NoError(t, err)
		assert.Equal(t, 5, values.ReviewPatience)
	})

	t.Run("local overrides global", func(t *testing.T) {
		tmpDir := t.TempDir()
		globalCfg := filepath.Join(tmpDir, "global")
		localCfg := filepath.Join(tmpDir, "local")
		require.NoError(t, os.WriteFile(globalCfg, []byte(`review_patience = 5`), 0o600))
		require.NoError(t, os.WriteFile(localCfg, []byte(`review_patience = 3`), 0o600))

		loader := newValuesLoader(defaultsFS)
		values, err := loader.Load(localCfg, globalCfg)
		require.NoError(t, err)
		assert.Equal(t, 3, values.ReviewPatience)
	})
}

func TestValuesLoader_Load_VcsCommand(t *testing.T) {
	t.Run("parse vcs_command", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "config")
		require.NoError(t, os.WriteFile(cfgPath, []byte(`vcs_command = /usr/local/bin/hg2git.sh`), 0o600))

		loader := newValuesLoader(defaultsFS)
		values, err := loader.Load("", cfgPath)
		require.NoError(t, err)
		assert.Equal(t, "/usr/local/bin/hg2git.sh", values.VcsCommand)
	})

	t.Run("tilde expansion", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "config")
		require.NoError(t, os.WriteFile(cfgPath, []byte(`vcs_command = ~/scripts/hg2git.sh`), 0o600))

		loader := newValuesLoader(defaultsFS)
		values, err := loader.Load("", cfgPath)
		require.NoError(t, err)

		home, err := os.UserHomeDir()
		require.NoError(t, err)
		assert.Equal(t, home+"/scripts/hg2git.sh", values.VcsCommand)
	})

	t.Run("default from embedded is git", func(t *testing.T) {
		loader := newValuesLoader(defaultsFS)
		values, err := loader.Load("", "")
		require.NoError(t, err)
		assert.Equal(t, "git", values.VcsCommand)
	})

	t.Run("local overrides global", func(t *testing.T) {
		tmpDir := t.TempDir()
		globalCfg := filepath.Join(tmpDir, "global")
		localCfg := filepath.Join(tmpDir, "local")
		require.NoError(t, os.WriteFile(globalCfg, []byte(`vcs_command = /global/vcs`), 0o600))
		require.NoError(t, os.WriteFile(localCfg, []byte(`vcs_command = /local/vcs`), 0o600))

		loader := newValuesLoader(defaultsFS)
		values, err := loader.Load(localCfg, globalCfg)
		require.NoError(t, err)
		assert.Equal(t, "/local/vcs", values.VcsCommand)
	})
}

func TestValues_mergeFrom_VcsCommand(t *testing.T) {
	t.Run("non-empty overrides", func(t *testing.T) {
		dst := Values{VcsCommand: "git"}
		src := Values{VcsCommand: "/path/to/hg2git.sh"}
		dst.mergeFrom(&src)
		assert.Equal(t, "/path/to/hg2git.sh", dst.VcsCommand)
	})

	t.Run("empty does not overwrite", func(t *testing.T) {
		dst := Values{VcsCommand: "/path/to/hg2git.sh"}
		src := Values{VcsCommand: ""}
		dst.mergeFrom(&src)
		assert.Equal(t, "/path/to/hg2git.sh", dst.VcsCommand)
	})
}

func TestValuesLoader_parseValuesFromBytes_LimitPatterns(t *testing.T) {
	vl := &valuesLoader{embedFS: defaultsFS}

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "single limit pattern",
			input:    "copilot_limit_patterns = rate limit hit",
			expected: []string{"rate limit hit"},
		},
		{
			name:     "multiple limit patterns",
			input:    "copilot_limit_patterns = Rate limit,quota exceeded,too many requests",
			expected: []string{"Rate limit", "quota exceeded", "too many requests"},
		},
		{
			name:     "whitespace trimming around commas",
			input:    "copilot_limit_patterns =  pattern1 ,  pattern2  , pattern3 ",
			expected: []string{"pattern1", "pattern2", "pattern3"},
		},
		{
			name:     "empty patterns filtered out",
			input:    "copilot_limit_patterns = pattern1,,pattern2,  ,pattern3",
			expected: []string{"pattern1", "pattern2", "pattern3"},
		},
		{
			name:     "empty value",
			input:    "copilot_limit_patterns = ",
			expected: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			values, err := vl.parseValuesFromBytes([]byte(tc.input))
			require.NoError(t, err)
			assert.Equal(t, tc.expected, values.CopilotLimitPatterns)
		})
	}
}

func TestValuesLoader_parseValuesFromBytes_WaitOnLimit(t *testing.T) {
	vl := &valuesLoader{embedFS: defaultsFS}

	tests := []struct {
		name        string
		input       string
		expected    time.Duration
		expectedSet bool
	}{
		{name: "1 hour", input: "wait_on_limit = 1h", expected: time.Hour, expectedSet: true},
		{name: "30 minutes", input: "wait_on_limit = 30m", expected: 30 * time.Minute, expectedSet: true},
		{name: "1h30m compound", input: "wait_on_limit = 1h30m", expected: 90 * time.Minute, expectedSet: true},
		{name: "90 seconds", input: "wait_on_limit = 90s", expected: 90 * time.Second, expectedSet: true},
		{name: "zero", input: "wait_on_limit = 0s", expected: 0, expectedSet: true},
		{name: "empty value", input: "wait_on_limit = ", expected: 0, expectedSet: false},
		{name: "not set", input: "", expected: 0, expectedSet: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			values, err := vl.parseValuesFromBytes([]byte(tc.input))
			require.NoError(t, err)
			assert.Equal(t, tc.expected, values.WaitOnLimit)
			assert.Equal(t, tc.expectedSet, values.WaitOnLimitSet)
		})
	}
}

func TestValuesLoader_Load_WaitOnLimit(t *testing.T) {
	t.Run("parse from config file", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "config")
		require.NoError(t, os.WriteFile(cfgPath, []byte(`wait_on_limit = 1h`), 0o600))

		loader := newValuesLoader(defaultsFS)
		values, err := loader.Load("", cfgPath)
		require.NoError(t, err)
		assert.Equal(t, time.Hour, values.WaitOnLimit)
		assert.True(t, values.WaitOnLimitSet)
	})

	t.Run("not set uses default zero", func(t *testing.T) {
		loader := newValuesLoader(defaultsFS)
		values, err := loader.Load("", "")
		require.NoError(t, err)
		assert.Zero(t, values.WaitOnLimit)
		assert.False(t, values.WaitOnLimitSet)
	})

	t.Run("local overrides global", func(t *testing.T) {
		tmpDir := t.TempDir()
		globalCfg := filepath.Join(tmpDir, "global")
		localCfg := filepath.Join(tmpDir, "local")
		require.NoError(t, os.WriteFile(globalCfg, []byte(`wait_on_limit = 2h`), 0o600))
		require.NoError(t, os.WriteFile(localCfg, []byte(`wait_on_limit = 30m`), 0o600))

		loader := newValuesLoader(defaultsFS)
		values, err := loader.Load(localCfg, globalCfg)
		require.NoError(t, err)
		assert.Equal(t, 30*time.Minute, values.WaitOnLimit)
		assert.True(t, values.WaitOnLimitSet)
	})

	t.Run("explicit zero overrides global", func(t *testing.T) {
		tmpDir := t.TempDir()
		globalCfg := filepath.Join(tmpDir, "global")
		localCfg := filepath.Join(tmpDir, "local")
		require.NoError(t, os.WriteFile(globalCfg, []byte(`wait_on_limit = 1h`), 0o600))
		require.NoError(t, os.WriteFile(localCfg, []byte(`wait_on_limit = 0s`), 0o600))

		loader := newValuesLoader(defaultsFS)
		values, err := loader.Load(localCfg, globalCfg)
		require.NoError(t, err)
		assert.Zero(t, values.WaitOnLimit)
		assert.True(t, values.WaitOnLimitSet)
	})
}

func TestValues_mergeFrom_WaitOnLimit(t *testing.T) {
	t.Run("set flag merges", func(t *testing.T) {
		dst := Values{WaitOnLimit: 0, WaitOnLimitSet: false}
		src := Values{WaitOnLimit: time.Hour, WaitOnLimitSet: true}
		dst.mergeFrom(&src)
		assert.Equal(t, time.Hour, dst.WaitOnLimit)
		assert.True(t, dst.WaitOnLimitSet)
	})

	t.Run("unset flag does not merge", func(t *testing.T) {
		dst := Values{WaitOnLimit: time.Hour, WaitOnLimitSet: true}
		src := Values{WaitOnLimit: 0, WaitOnLimitSet: false}
		dst.mergeFrom(&src)
		assert.Equal(t, time.Hour, dst.WaitOnLimit)
		assert.True(t, dst.WaitOnLimitSet)
	})

	t.Run("set flag can set to zero", func(t *testing.T) {
		dst := Values{WaitOnLimit: time.Hour, WaitOnLimitSet: true}
		src := Values{WaitOnLimit: 0, WaitOnLimitSet: true}
		dst.mergeFrom(&src)
		assert.Zero(t, dst.WaitOnLimit)
		assert.True(t, dst.WaitOnLimitSet)
	})
}

func TestValues_mergeFrom_LimitPatterns(t *testing.T) {
	t.Run("merge limit patterns when src has values", func(t *testing.T) {
		dst := Values{CopilotLimitPatterns: []string{"dst pattern"}}
		src := Values{CopilotLimitPatterns: []string{"src pattern 1", "src pattern 2"}}
		dst.mergeFrom(&src)

		assert.Equal(t, []string{"src pattern 1", "src pattern 2"}, dst.CopilotLimitPatterns)
	})

	t.Run("preserve dst when src is empty", func(t *testing.T) {
		dst := Values{CopilotLimitPatterns: []string{"dst pattern"}}
		src := Values{CopilotLimitPatterns: nil}
		dst.mergeFrom(&src)

		assert.Equal(t, []string{"dst pattern"}, dst.CopilotLimitPatterns)
	})
}

func TestValuesLoader_Load_LimitPatternsOverride(t *testing.T) {
	tmpDir := t.TempDir()
	globalConfig := filepath.Join(tmpDir, "global")
	localConfig := filepath.Join(tmpDir, "local")

	// global has one set of patterns
	globalContent := `copilot_limit_patterns = global pattern 1, global pattern 2`
	require.NoError(t, os.WriteFile(globalConfig, []byte(globalContent), 0o600))

	// local overrides with different patterns
	localContent := `copilot_limit_patterns = local pattern`
	require.NoError(t, os.WriteFile(localConfig, []byte(localContent), 0o600))

	loader := newValuesLoader(defaultsFS)
	values, err := loader.Load(localConfig, globalConfig)
	require.NoError(t, err)

	// local should override global completely (not merge)
	assert.Equal(t, []string{"local pattern"}, values.CopilotLimitPatterns)
}
