package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- embedded filesystem tests ---

func Test_defaultsFS(t *testing.T) {
	fs := defaultsFS

	data, err := fs.ReadFile("defaults/config")
	require.NoError(t, err)
	assert.Contains(t, string(data), "claude_command")
	assert.Contains(t, string(data), "codex_enabled")
	assert.Contains(t, string(data), "iteration_delay_ms")
}

func Test_defaultsFS_PromptFiles(t *testing.T) {
	fs := defaultsFS

	testCases := []struct {
		file     string
		contains []string
	}{
		{file: "defaults/prompts/task.txt", contains: []string{"{{PLAN_FILE}}", "{{PROGRESS_FILE}}", "RALPHEX:ALL_TASKS_DONE", "RALPHEX:TASK_FAILED"}},
		{file: "defaults/prompts/review_first.txt", contains: []string{"{{GOAL}}", "{{PROGRESS_FILE}}", "RALPHEX:REVIEW_DONE", "{{agent:quality}}", "{{agent:testing}}"}},
		{file: "defaults/prompts/review_second.txt", contains: []string{"{{GOAL}}", "{{PROGRESS_FILE}}", "RALPHEX:REVIEW_DONE", "{{agent:quality}}", "{{agent:implementation}}"}},
		{file: "defaults/prompts/codex.txt", contains: []string{"{{CODEX_OUTPUT}}", "RALPHEX:CODEX_REVIEW_DONE", "GPT-5.2"}},
	}

	for _, tc := range testCases {
		t.Run(tc.file, func(t *testing.T) {
			data, err := fs.ReadFile(tc.file)
			require.NoError(t, err, "failed to read %s", tc.file)
			content := string(data)
			for _, expected := range tc.contains {
				assert.Contains(t, content, expected, "file %s should contain %q", tc.file, expected)
			}
		})
	}
}

func Test_defaultsFS_AllFilesPresent(t *testing.T) {
	fs := defaultsFS

	expectedFiles := []string{
		"defaults/config",
		"defaults/prompts/task.txt",
		"defaults/prompts/review_first.txt",
		"defaults/prompts/review_second.txt",
		"defaults/prompts/codex.txt",
	}

	for _, file := range expectedFiles {
		t.Run(file, func(t *testing.T) {
			_, err := fs.ReadFile(file)
			require.NoError(t, err, "embedded file %s should exist", file)
		})
	}
}

func Test_defaultsFS_EmbeddedAgentsExist(t *testing.T) {
	fs := defaultsFS

	expectedAgents := []string{
		"defaults/agents/implementation.txt",
		"defaults/agents/quality.txt",
		"defaults/agents/documentation.txt",
		"defaults/agents/simplification.txt",
		"defaults/agents/testing.txt",
	}

	for _, file := range expectedAgents {
		t.Run(file, func(t *testing.T) {
			data, err := fs.ReadFile(file)
			require.NoError(t, err, "embedded agent file %s should exist", file)
			assert.NotEmpty(t, string(data), "agent file %s should have content", file)
		})
	}
}

// --- Load tests ---

func TestLoad_SetsConfigDir(t *testing.T) {
	cfg, err := Load("") // empty uses default
	require.NoError(t, err)
	assert.NotEmpty(t, cfg.configDir)
	assert.Contains(t, cfg.configDir, "ralphex")
}

func TestLoad_WithCustomDir(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "custom-config")

	cfg, err := Load(configDir)
	require.NoError(t, err)

	assert.Equal(t, configDir, cfg.configDir)
	// should have defaults installed in custom dir
	assert.FileExists(t, filepath.Join(configDir, "config"))
	assert.DirExists(t, filepath.Join(configDir, "prompts"))
	assert.DirExists(t, filepath.Join(configDir, "agents"))
}

func TestLoad_PopulatesAllFields(t *testing.T) {
	cfg, err := Load("") // empty uses default
	require.NoError(t, err)

	// should have config values from defaults
	assert.NotEmpty(t, cfg.ClaudeCommand)
	assert.NotEmpty(t, cfg.ClaudeArgs)
	assert.NotEmpty(t, cfg.CodexCommand)

	// should have prompts loaded
	assert.NotEmpty(t, cfg.TaskPrompt)
	assert.NotEmpty(t, cfg.ReviewFirstPrompt)
	assert.NotEmpty(t, cfg.ReviewSecondPrompt)
	assert.NotEmpty(t, cfg.CodexPrompt)
}

func TestLoad_WithUserConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")
	require.NoError(t, os.MkdirAll(configDir, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "prompts"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "agents"), 0o700))

	userConfig := `
claude_command = /custom/claude
iteration_delay_ms = 9999
`
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config"), []byte(userConfig), 0o600))

	cfg, err := Load(configDir)
	require.NoError(t, err)

	assert.Equal(t, "/custom/claude", cfg.ClaudeCommand)
	assert.Equal(t, 9999, cfg.IterationDelayMs)
	// prompts should fall back to embedded defaults
	assert.NotEmpty(t, cfg.TaskPrompt)
}

func TestDefaultConfigDir(t *testing.T) {
	dir := DefaultConfigDir()
	assert.NotEmpty(t, dir)
	assert.Contains(t, dir, "ralphex")
}

func TestEmbeddedDefaultsColorValues(t *testing.T) {
	// tests that embedded defaults/config contains correct color values
	// and that they parse into expected RGB strings
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")

	cfg, err := Load(configDir)
	require.NoError(t, err)

	// verify all 9 colors have expected default values (from defaults/config)
	assert.Equal(t, "0,255,0", cfg.Colors.Task, "task color should be green (#00ff00)")
	assert.Equal(t, "0,255,255", cfg.Colors.Review, "review color should be cyan (#00ffff)")
	assert.Equal(t, "208,150,217", cfg.Colors.Codex, "codex color should be light magenta (#d096d9)")
	assert.Equal(t, "189,214,255", cfg.Colors.ClaudeEval, "claude_eval color should be light blue (#bdd6ff)")
	assert.Equal(t, "255,197,109", cfg.Colors.Warn, "warn color should be orange (#ffc56d)")
	assert.Equal(t, "255,0,0", cfg.Colors.Error, "error color should be red (#ff0000)")
	assert.Equal(t, "210,82,82", cfg.Colors.Signal, "signal color should be muted red (#d25252)")
	assert.Equal(t, "138,138,138", cfg.Colors.Timestamp, "timestamp color should be gray (#8a8a8a)")
	assert.Equal(t, "180,180,180", cfg.Colors.Info, "info color should be light gray (#b4b4b4)")
}

func TestLoad_PartialConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")
	require.NoError(t, os.MkdirAll(configDir, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "prompts"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "agents"), 0o700))

	// create config with only partial values
	configContent := `plans_dir = custom/plans`
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config"), []byte(configContent), 0o600))

	cfg, err := Load(configDir)
	require.NoError(t, err)

	// partial value preserved
	assert.Equal(t, "custom/plans", cfg.PlansDir)

	// missing values filled from embedded defaults
	assert.Equal(t, "claude", cfg.ClaudeCommand)
	assert.Equal(t, "--dangerously-skip-permissions --output-format stream-json --verbose", cfg.ClaudeArgs)
	assert.Equal(t, "codex", cfg.CodexCommand)
	assert.Equal(t, "gpt-5.2-codex", cfg.CodexModel)
	assert.Equal(t, "xhigh", cfg.CodexReasoningEffort)
	assert.Equal(t, "read-only", cfg.CodexSandbox)
	assert.Equal(t, 2000, cfg.IterationDelayMs)
	assert.Equal(t, 3600000, cfg.CodexTimeoutMs)
	assert.True(t, cfg.CodexEnabled)
	assert.Equal(t, 1, cfg.TaskRetryCount)
}

func TestLoad_EmptyConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")
	require.NoError(t, os.MkdirAll(configDir, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "prompts"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "agents"), 0o700))

	// create empty config file
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config"), []byte(""), 0o600))

	cfg, err := Load(configDir)
	require.NoError(t, err)

	// all values should come from embedded defaults
	assert.Equal(t, "claude", cfg.ClaudeCommand)
	assert.Equal(t, "--dangerously-skip-permissions --output-format stream-json --verbose", cfg.ClaudeArgs)
	assert.Equal(t, "codex", cfg.CodexCommand)
	assert.Equal(t, "gpt-5.2-codex", cfg.CodexModel)
	assert.Equal(t, "xhigh", cfg.CodexReasoningEffort)
	assert.Equal(t, "read-only", cfg.CodexSandbox)
	assert.Equal(t, "docs/plans", cfg.PlansDir)
	assert.Equal(t, 2000, cfg.IterationDelayMs)
	assert.Equal(t, 3600000, cfg.CodexTimeoutMs)
	assert.True(t, cfg.CodexEnabled)
	assert.Equal(t, 1, cfg.TaskRetryCount)
}

func TestLoad_ExplicitZeroTaskRetryCount(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")
	require.NoError(t, os.MkdirAll(configDir, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "prompts"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "agents"), 0o700))

	// explicitly set task_retry_count to 0
	configContent := `task_retry_count = 0`
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config"), []byte(configContent), 0o600))

	cfg, err := Load(configDir)
	require.NoError(t, err)

	// explicit zero should be preserved (not overwritten by default 1)
	assert.Equal(t, 0, cfg.TaskRetryCount)
	assert.True(t, cfg.TaskRetryCountSet)
}

func TestLoad_ExplicitFalseCodexEnabled(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")
	require.NoError(t, os.MkdirAll(configDir, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "prompts"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "agents"), 0o700))

	// explicitly set codex_enabled to false
	configContent := `codex_enabled = false`
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config"), []byte(configContent), 0o600))

	cfg, err := Load(configDir)
	require.NoError(t, err)

	// explicit false should be preserved (not overwritten by default true)
	assert.False(t, cfg.CodexEnabled)
	assert.True(t, cfg.CodexEnabledSet)
}

func TestLoad_ExplicitTrueFinalizeEnabled(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")
	require.NoError(t, os.MkdirAll(configDir, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "prompts"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "agents"), 0o700))

	// explicitly set finalize_enabled to true
	configContent := `finalize_enabled = true`
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config"), []byte(configContent), 0o600))

	cfg, err := Load(configDir)
	require.NoError(t, err)

	// explicit true should be preserved
	assert.True(t, cfg.FinalizeEnabled)
	assert.True(t, cfg.FinalizeEnabledSet)
}

func TestLoad_FinalizeEnabledDefaultFalse(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")
	require.NoError(t, os.MkdirAll(configDir, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "prompts"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "agents"), 0o700))

	// empty config - finalize_enabled should be false by default
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config"), []byte(""), 0o600))

	cfg, err := Load(configDir)
	require.NoError(t, err)

	// finalize_enabled should default to false (disabled)
	assert.False(t, cfg.FinalizeEnabled)
	assert.False(t, cfg.FinalizeEnabledSet)
}

func TestLoad_AllUserValues(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")
	require.NoError(t, os.MkdirAll(configDir, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "prompts"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "agents"), 0o700))

	// set all values to custom values
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
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config"), []byte(configContent), 0o600))

	cfg, err := Load(configDir)
	require.NoError(t, err)

	// all values should be user-specified, not defaults
	assert.Equal(t, "/custom/claude", cfg.ClaudeCommand)
	assert.Equal(t, "--custom", cfg.ClaudeArgs)
	assert.False(t, cfg.CodexEnabled)
	assert.Equal(t, "/custom/codex", cfg.CodexCommand)
	assert.Equal(t, "custom-model", cfg.CodexModel)
	assert.Equal(t, "low", cfg.CodexReasoningEffort)
	assert.Equal(t, 1000, cfg.CodexTimeoutMs)
	assert.Equal(t, "none", cfg.CodexSandbox)
	assert.Equal(t, 500, cfg.IterationDelayMs)
	assert.Equal(t, 5, cfg.TaskRetryCount)
	assert.Equal(t, "my/plans", cfg.PlansDir)
}

// --- local config tests ---

func TestLocalConfig_NoLocalDir(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global")

	cfg, err := loadWithLocal(globalDir, "")
	require.NoError(t, err)

	assert.Equal(t, globalDir, cfg.configDir)
	assert.Empty(t, cfg.LocalDir())
}

func TestLocalConfig_WithLocalDir(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global")
	localDir := filepath.Join(tmpDir, ".ralphex")
	require.NoError(t, os.MkdirAll(localDir, 0o700))

	cfg, err := loadWithLocal(globalDir, localDir)
	require.NoError(t, err)

	assert.Equal(t, globalDir, cfg.configDir)
	assert.Equal(t, localDir, cfg.LocalDir())
}

func TestLocalConfig_LocalOverridesGlobal(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global")
	localDir := filepath.Join(tmpDir, ".ralphex")

	require.NoError(t, os.MkdirAll(globalDir, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(globalDir, "prompts"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(globalDir, "agents"), 0o700))
	require.NoError(t, os.MkdirAll(localDir, 0o700))

	// global config
	globalConfig := `
claude_command = global-claude
claude_args = --global-args
iteration_delay_ms = 1000
plans_dir = global/plans
`
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "config"), []byte(globalConfig), 0o600))

	// local config overrides some values
	localConfig := `
claude_command = local-claude
plans_dir = local/plans
`
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "config"), []byte(localConfig), 0o600))

	cfg, err := loadWithLocal(globalDir, localDir)
	require.NoError(t, err)

	// local values override global
	assert.Equal(t, "local-claude", cfg.ClaudeCommand)
	assert.Equal(t, "local/plans", cfg.PlansDir)

	// global values preserved when not overridden in local
	assert.Equal(t, "--global-args", cfg.ClaudeArgs)
	assert.Equal(t, 1000, cfg.IterationDelayMs)
}

func TestLocalConfig_LocalOverridesColors(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global")
	localDir := filepath.Join(tmpDir, ".ralphex")

	require.NoError(t, os.MkdirAll(globalDir, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(globalDir, "prompts"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(globalDir, "agents"), 0o700))
	require.NoError(t, os.MkdirAll(localDir, 0o700))

	// global config with colors
	globalConfig := `
color_task = #ff0000
color_error = #00ff00
`
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "config"), []byte(globalConfig), 0o600))

	// local config overrides one color
	localConfig := `
color_task = #0000ff
`
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "config"), []byte(localConfig), 0o600))

	cfg, err := loadWithLocal(globalDir, localDir)
	require.NoError(t, err)

	// local color overrides global
	assert.Equal(t, "0,0,255", cfg.Colors.Task)

	// global color preserved when not overridden
	assert.Equal(t, "0,255,0", cfg.Colors.Error)
}

func TestLocalConfig_LocalOverridesCodexEnabled(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global")
	localDir := filepath.Join(tmpDir, ".ralphex")

	require.NoError(t, os.MkdirAll(globalDir, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(globalDir, "prompts"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(globalDir, "agents"), 0o700))
	require.NoError(t, os.MkdirAll(localDir, 0o700))

	// global config with codex_enabled = true
	globalConfig := `codex_enabled = true`
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "config"), []byte(globalConfig), 0o600))

	// local config disables codex
	localConfig := `codex_enabled = false`
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "config"), []byte(localConfig), 0o600))

	cfg, err := loadWithLocal(globalDir, localDir)
	require.NoError(t, err)

	assert.False(t, cfg.CodexEnabled)
	assert.True(t, cfg.CodexEnabledSet)
}

func TestLocalConfig_LocalOverridesTaskRetryCount(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global")
	localDir := filepath.Join(tmpDir, ".ralphex")

	require.NoError(t, os.MkdirAll(globalDir, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(globalDir, "prompts"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(globalDir, "agents"), 0o700))
	require.NoError(t, os.MkdirAll(localDir, 0o700))

	// global config with task_retry_count = 5
	globalConfig := `task_retry_count = 5`
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "config"), []byte(globalConfig), 0o600))

	// local config sets to 0
	localConfig := `task_retry_count = 0`
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "config"), []byte(localConfig), 0o600))

	cfg, err := loadWithLocal(globalDir, localDir)
	require.NoError(t, err)

	assert.Equal(t, 0, cfg.TaskRetryCount)
	assert.True(t, cfg.TaskRetryCountSet)
}

func TestLocalConfig_NoLocalConfigFile(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global")
	localDir := filepath.Join(tmpDir, ".ralphex")

	require.NoError(t, os.MkdirAll(globalDir, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(globalDir, "prompts"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(globalDir, "agents"), 0o700))
	require.NoError(t, os.MkdirAll(localDir, 0o700)) // local dir exists but no config file

	globalConfig := `claude_command = global-claude`
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "config"), []byte(globalConfig), 0o600))

	cfg, err := loadWithLocal(globalDir, localDir)
	require.NoError(t, err)

	// global values used since no local config file
	assert.Equal(t, "global-claude", cfg.ClaudeCommand)
	assert.Equal(t, localDir, cfg.LocalDir())
}

func TestConfig_LocalDir_Accessor(t *testing.T) {
	cfg := &Config{localDir: "/some/path/.ralphex"}
	assert.Equal(t, "/some/path/.ralphex", cfg.LocalDir())

	cfg2 := &Config{}
	assert.Empty(t, cfg2.LocalDir())
}

// --- local prompts and agents integration tests ---

func TestLocalConfig_LocalPromptsOverrideGlobal(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global")
	localDir := filepath.Join(tmpDir, ".ralphex")

	require.NoError(t, os.MkdirAll(filepath.Join(globalDir, "prompts"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(globalDir, "agents"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(localDir, "prompts"), 0o700))

	// global prompts
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "prompts", "task.txt"), []byte("global task prompt"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "prompts", "review_first.txt"), []byte("global review first"), 0o600))

	// local prompt overrides task.txt only
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "prompts", "task.txt"), []byte("local task prompt"), 0o600))

	cfg, err := loadWithLocal(globalDir, localDir)
	require.NoError(t, err)

	// local prompt used
	assert.Equal(t, "local task prompt", cfg.TaskPrompt)

	// global prompt used for non-overridden file
	assert.Equal(t, "global review first", cfg.ReviewFirstPrompt)
}

func TestLocalConfig_LocalAgentsReplaceGlobal(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global")
	localDir := filepath.Join(tmpDir, ".ralphex")

	require.NoError(t, os.MkdirAll(filepath.Join(globalDir, "prompts"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(globalDir, "agents"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(localDir, "agents"), 0o700))

	// global agents
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "agents", "security.txt"), []byte("global security"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "agents", "performance.txt"), []byte("global performance"), 0o600))

	// local agents (completely different set)
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "agents", "custom.txt"), []byte("local custom agent"), 0o600))

	cfg, err := loadWithLocal(globalDir, localDir)
	require.NoError(t, err)

	// only local agents should be used (replace behavior)
	assert.Len(t, cfg.CustomAgents, 1)
	assert.Equal(t, "custom", cfg.CustomAgents[0].Name)
	assert.Equal(t, "local custom agent", cfg.CustomAgents[0].Prompt)
}

func TestLoad_InvalidConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")
	require.NoError(t, os.MkdirAll(configDir, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "prompts"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "agents"), 0o700))

	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config"), []byte(`iteration_delay_ms = not_a_number`), 0o600))

	_, err := Load(configDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "iteration_delay_ms")
}

// TestLoad_PartialOverridesAllComponents tests partial overrides across all components
// simultaneously (values, colors, prompts, agents) to verify the complete merge chain.
func TestLoad_PartialOverridesAllComponents(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global")
	localDir := filepath.Join(tmpDir, ".ralphex")

	// set up global directories
	require.NoError(t, os.MkdirAll(filepath.Join(globalDir, "prompts"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(globalDir, "agents"), 0o700))

	// global config: partial values + partial colors
	globalConfig := `
claude_command = global-claude
iteration_delay_ms = 5000
task_retry_count = 3
color_task = #ff0000
color_error = #00ff00
`
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "config"), []byte(globalConfig), 0o600))

	// global prompts: only task.txt and review_first.txt
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "prompts", "task.txt"), []byte("global task"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "prompts", "review_first.txt"), []byte("global review first"), 0o600))

	// global agents
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "agents", "security.txt"), []byte("global security agent"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "agents", "perf.txt"), []byte("global perf agent"), 0o600))

	// set up local directories
	require.NoError(t, os.MkdirAll(filepath.Join(localDir, "prompts"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(localDir, "agents"), 0o700))

	// local config: override some values + different color
	localConfig := `
claude_command = local-claude
codex_enabled = false
color_task = #0000ff
`
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "config"), []byte(localConfig), 0o600))

	// local prompts: only task.txt (overrides global task.txt)
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "prompts", "task.txt"), []byte("local task"), 0o600))

	// local agents: completely different set (replaces global)
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "agents", "custom.txt"), []byte("local custom agent"), 0o600))

	cfg, err := loadWithLocal(globalDir, localDir)
	require.NoError(t, err)

	// --- verify values merge chain: embedded → global → local ---
	// local override
	assert.Equal(t, "local-claude", cfg.ClaudeCommand)
	assert.False(t, cfg.CodexEnabled, "local codex_enabled=false should override")
	assert.True(t, cfg.CodexEnabledSet)

	// global preserved (not in local)
	assert.Equal(t, 5000, cfg.IterationDelayMs)
	assert.Equal(t, 3, cfg.TaskRetryCount)

	// embedded defaults (not in global or local)
	assert.Equal(t, "--dangerously-skip-permissions --output-format stream-json --verbose", cfg.ClaudeArgs)
	assert.Equal(t, "codex", cfg.CodexCommand)
	assert.Equal(t, "gpt-5.2-codex", cfg.CodexModel)

	// --- verify colors merge chain ---
	// local override
	assert.Equal(t, "0,0,255", cfg.Colors.Task, "local blue should override global red")
	// global preserved
	assert.Equal(t, "0,255,0", cfg.Colors.Error, "global green should be preserved")
	// embedded defaults
	assert.Equal(t, "0,255,255", cfg.Colors.Review, "embedded cyan should be used")

	// --- verify prompts merge chain: local → global → embedded ---
	// local override
	assert.Equal(t, "local task", cfg.TaskPrompt)
	// global preserved (not in local)
	assert.Equal(t, "global review first", cfg.ReviewFirstPrompt)
	// embedded defaults (not in local or global)
	assert.Contains(t, cfg.ReviewSecondPrompt, "{{GOAL}}", "embedded review_second should be used")
	assert.Contains(t, cfg.CodexPrompt, "{{CODEX_OUTPUT}}", "embedded codex should be used")

	// --- verify agents replace behavior (local agents completely replace global) ---
	require.Len(t, cfg.CustomAgents, 1, "local agents should replace global entirely")
	assert.Equal(t, "custom", cfg.CustomAgents[0].Name)
	assert.Equal(t, "local custom agent", cfg.CustomAgents[0].Prompt)
}

func TestLoad_SymlinkedConfigDir(t *testing.T) {
	// simulates real-world scenario where ~/.config/ralphex is symlinked from another repo
	tmpDir := t.TempDir()

	// create real config directory with content
	realDir := filepath.Join(tmpDir, "dotfiles-repo", "ralphex-config")
	require.NoError(t, os.MkdirAll(filepath.Join(realDir, "prompts"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(realDir, "agents"), 0o700))

	configContent := `
claude_command = symlink-claude
iteration_delay_ms = 2500
color_task = #123456
`
	require.NoError(t, os.WriteFile(filepath.Join(realDir, "config"), []byte(configContent), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(realDir, "prompts", "task.txt"), []byte("symlinked task prompt"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(realDir, "agents", "custom.txt"), []byte("symlinked agent"), 0o600))

	// create symlink (like ln -s dotfiles-repo/ralphex-config ~/.config/ralphex)
	symlinkDir := filepath.Join(tmpDir, "config-symlink")
	require.NoError(t, os.Symlink(realDir, symlinkDir))

	// load config through symlink
	cfg, err := loadWithLocal(symlinkDir, "")
	require.NoError(t, err)

	// verify values loaded correctly through symlink
	assert.Equal(t, "symlink-claude", cfg.ClaudeCommand)
	assert.Equal(t, 2500, cfg.IterationDelayMs)
	assert.Equal(t, "18,52,86", cfg.Colors.Task) // #123456 converted to RGB

	// verify prompts loaded through symlink
	assert.Equal(t, "symlinked task prompt", cfg.TaskPrompt)

	// verify agents loaded through symlink
	require.Len(t, cfg.CustomAgents, 1)
	assert.Equal(t, "custom", cfg.CustomAgents[0].Name)
	assert.Equal(t, "symlinked agent", cfg.CustomAgents[0].Prompt)

	// verify configDir is the symlink path (not resolved real path)
	assert.Equal(t, symlinkDir, cfg.configDir)
}

func TestLoad_SymlinkedLocalDir(t *testing.T) {
	// simulates local .ralphex being a symlink to shared project config
	tmpDir := t.TempDir()

	// global config
	globalDir := filepath.Join(tmpDir, "global")
	require.NoError(t, os.MkdirAll(filepath.Join(globalDir, "prompts"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(globalDir, "agents"), 0o700))
	globalConfig := `
claude_command = global-claude
iteration_delay_ms = 1000
`
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "config"), []byte(globalConfig), 0o600))

	// real local config in another location (like a shared repo)
	realLocalDir := filepath.Join(tmpDir, "shared-configs", "project-a")
	require.NoError(t, os.MkdirAll(realLocalDir, 0o700))
	localConfig := `
claude_command = local-symlinked-claude
`
	require.NoError(t, os.WriteFile(filepath.Join(realLocalDir, "config"), []byte(localConfig), 0o600))

	// create symlink for local dir (like ln -s shared-configs/project-a .ralphex)
	symlinkLocalDir := filepath.Join(tmpDir, ".ralphex-symlink")
	require.NoError(t, os.Symlink(realLocalDir, symlinkLocalDir))

	// load with symlinked local dir
	cfg, err := loadWithLocal(globalDir, symlinkLocalDir)
	require.NoError(t, err)

	// verify local override works through symlink
	assert.Equal(t, "local-symlinked-claude", cfg.ClaudeCommand)

	// verify global fallback still works
	assert.Equal(t, 1000, cfg.IterationDelayMs)

	// verify localDir is the symlink path
	assert.Equal(t, symlinkLocalDir, cfg.LocalDir())
}
