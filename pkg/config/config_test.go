package config

import (
	"embed"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- embedded filesystem tests ---

func TestDefaultsFS(t *testing.T) {
	fs := DefaultsFS()

	data, err := fs.ReadFile("defaults/config")
	require.NoError(t, err)
	assert.Contains(t, string(data), "claude_command")
	assert.Contains(t, string(data), "codex_enabled")
	assert.Contains(t, string(data), "iteration_delay_ms")
}

func TestDefaultsFS_PromptFiles(t *testing.T) {
	fs := DefaultsFS()

	testCases := []struct {
		file     string
		contains []string
	}{
		{file: "defaults/prompts/task.txt", contains: []string{"{{PLAN_FILE}}", "{{PROGRESS_FILE}}", "RALPHEX:ALL_TASKS_DONE", "RALPHEX:TASK_FAILED"}},
		{file: "defaults/prompts/review_first.txt", contains: []string{"{{GOAL}}", "RALPHEX:REVIEW_DONE", "{{agent:quality}}", "{{agent:testing}}"}},
		{file: "defaults/prompts/review_second.txt", contains: []string{"{{GOAL}}", "RALPHEX:REVIEW_DONE", "{{agent:quality}}", "{{agent:implementation}}"}},
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

func TestDefaultsFS_AllFilesPresent(t *testing.T) {
	fs := DefaultsFS()

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

func TestEmbeddedAgentsExist(t *testing.T) {
	fs := DefaultsFS()

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

// --- loadConfigFile tests ---

func TestConfig_loadConfigFile_NoConfigFile(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")

	cfg := &Config{configDir: configDir}
	require.NoError(t, cfg.loadConfigFile())

	assert.Equal(t, "claude", cfg.ClaudeCommand)
	assert.Equal(t, "--dangerously-skip-permissions --output-format stream-json --verbose", cfg.ClaudeArgs)
	assert.True(t, cfg.CodexEnabled)
	assert.Equal(t, "codex", cfg.CodexCommand)
	assert.Equal(t, "gpt-5.2-codex", cfg.CodexModel)
	assert.Equal(t, "xhigh", cfg.CodexReasoningEffort)
	assert.Equal(t, 3600000, cfg.CodexTimeoutMs)
	assert.Equal(t, "read-only", cfg.CodexSandbox)
	assert.Equal(t, 2000, cfg.IterationDelayMs)
	assert.Equal(t, 1, cfg.TaskRetryCount)
	assert.Equal(t, "docs/plans", cfg.PlansDir)
}

func TestConfig_loadConfigFile_WithUserConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")
	require.NoError(t, os.MkdirAll(configDir, 0o700))

	userConfig := `
claude_command = /usr/local/bin/claude
claude_args = --custom-args
codex_enabled = false
codex_command = my-codex
codex_model = custom-model
codex_reasoning_effort = high
codex_timeout_ms = 1000
codex_sandbox = full
iteration_delay_ms = 5000
task_retry_count = 2
plans_dir = my/plans
`
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config"), []byte(userConfig), 0o600))

	cfg := &Config{configDir: configDir}
	require.NoError(t, cfg.loadConfigFile())

	assert.Equal(t, "/usr/local/bin/claude", cfg.ClaudeCommand)
	assert.Equal(t, "--custom-args", cfg.ClaudeArgs)
	assert.False(t, cfg.CodexEnabled)
	assert.Equal(t, "my-codex", cfg.CodexCommand)
	assert.Equal(t, "custom-model", cfg.CodexModel)
	assert.Equal(t, "high", cfg.CodexReasoningEffort)
	assert.Equal(t, 1000, cfg.CodexTimeoutMs)
	assert.Equal(t, "full", cfg.CodexSandbox)
	assert.Equal(t, 5000, cfg.IterationDelayMs)
	assert.Equal(t, 2, cfg.TaskRetryCount)
	assert.Equal(t, "my/plans", cfg.PlansDir)
}

func TestConfig_loadConfigFile_InvalidConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")
	require.NoError(t, os.MkdirAll(configDir, 0o700))

	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config"), []byte(`iteration_delay_ms = not_a_number`), 0o600))

	cfg := &Config{configDir: configDir}
	err := cfg.loadConfigFile()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "iteration_delay_ms")
}

func TestConfig_loadConfigFile_EmptyConfigFile(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")
	require.NoError(t, os.MkdirAll(configDir, 0o700))

	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config"), []byte(""), 0o600))

	cfg := &Config{configDir: configDir}
	require.NoError(t, cfg.loadConfigFile())

	assert.Empty(t, cfg.ClaudeCommand)
	assert.Empty(t, cfg.ClaudeArgs)
	assert.False(t, cfg.CodexEnabled)
	assert.Equal(t, 0, cfg.IterationDelayMs)
}

func TestConfig_defaultConfigDir(t *testing.T) {
	cfg := &Config{}
	dir := cfg.defaultConfigDir()
	assert.NotEmpty(t, dir)
	assert.Contains(t, dir, "ralphex")
}

// --- install defaults tests ---

func TestConfig_installDefaults_CreatesConfigDir(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")

	_, err := os.Stat(configDir)
	require.True(t, os.IsNotExist(err))

	cfg := &Config{configDir: configDir}
	require.NoError(t, cfg.installDefaults())

	info, err := os.Stat(configDir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	data, err := os.ReadFile(filepath.Join(configDir, "config")) //nolint:gosec // test
	require.NoError(t, err)
	assert.Contains(t, string(data), "claude_command")
	assert.Contains(t, string(data), "codex_enabled")
}

func TestConfig_installDefaults_ExistingDir(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")
	require.NoError(t, os.MkdirAll(configDir, 0o700))

	cfg := &Config{configDir: configDir}
	require.NoError(t, cfg.installDefaults())

	_, err := os.Stat(filepath.Join(configDir, "config"))
	require.NoError(t, err)
}

func TestConfig_installDefaults_ExistingConfigFile(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")
	require.NoError(t, os.MkdirAll(configDir, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "prompts"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "agents"), 0o700))

	existingContent := "# my custom config\nclaude_command = custom"
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config"), []byte(existingContent), 0o600))

	cfg := &Config{configDir: configDir}
	require.NoError(t, cfg.installDefaults())

	data, err := os.ReadFile(filepath.Join(configDir, "config")) //nolint:gosec // test
	require.NoError(t, err)
	assert.Equal(t, existingContent, string(data))
}

func TestConfig_installDefaults_CreatesPromptsDir(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")

	cfg := &Config{configDir: configDir}
	require.NoError(t, cfg.installDefaults())

	info, err := os.Stat(filepath.Join(configDir, "prompts"))
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestConfig_installDefaults_CreatesAgentsDir(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")

	cfg := &Config{configDir: configDir}
	require.NoError(t, cfg.installDefaults())

	info, err := os.Stat(filepath.Join(configDir, "agents"))
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestConfig_installDefaults_SkipsWhenAllPathsExist(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")

	require.NoError(t, os.MkdirAll(configDir, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "prompts"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "agents"), 0o700))

	customContent := "# custom\nclaude_command = my-claude"
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config"), []byte(customContent), 0o600))

	cfg := &Config{configDir: configDir}
	require.NoError(t, cfg.installDefaults())

	// config should not be overwritten
	data, err := os.ReadFile(filepath.Join(configDir, "config")) //nolint:gosec // test
	require.NoError(t, err)
	assert.Equal(t, customContent, string(data))
}

func TestConfig_installDefaults_InstallsIfPromptsOrAgentsMissing(t *testing.T) {
	testCases := []struct {
		name       string
		setupPaths []string
	}{
		{name: "missing_prompts", setupPaths: []string{"config", "agents"}},
		{name: "missing_agents", setupPaths: []string{"config", "prompts"}},
		{name: "missing_config", setupPaths: []string{"prompts", "agents"}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			configDir := filepath.Join(tmpDir, "ralphex")
			require.NoError(t, os.MkdirAll(configDir, 0o700))

			for _, p := range tc.setupPaths {
				fullPath := filepath.Join(configDir, p)
				if p == "config" {
					require.NoError(t, os.WriteFile(fullPath, []byte("# test"), 0o600))
				} else {
					require.NoError(t, os.MkdirAll(fullPath, 0o700))
				}
			}

			cfg := &Config{configDir: configDir}
			require.NoError(t, cfg.installDefaults())

			for _, p := range []string{"config", "prompts", "agents"} {
				_, err := os.Stat(filepath.Join(configDir, p))
				require.NoError(t, err, "path %s should exist", p)
			}
		})
	}
}

func TestConfig_installDefaults_InstallsAgentFiles(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")

	cfg := &Config{configDir: configDir}
	require.NoError(t, cfg.installDefaults())

	agentsDir := filepath.Join(configDir, "agents")
	expectedAgents := []string{"implementation.txt", "quality.txt", "documentation.txt", "simplification.txt", "testing.txt"}

	for _, agent := range expectedAgents {
		agentPath := filepath.Join(agentsDir, agent)
		assert.FileExists(t, agentPath, "agent file %s should be installed", agent)

		data, err := os.ReadFile(agentPath) //nolint:gosec // test
		require.NoError(t, err)
		assert.NotEmpty(t, string(data), "agent file %s should have content", agent)
	}
}

func TestConfig_installDefaults_NeverOverwritesAgents(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")
	agentsDir := filepath.Join(configDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "prompts"), 0o700))

	// create a single custom agent file
	customContent := "my custom agent"
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "my-agent.txt"), []byte(customContent), 0o600))

	// write config file so all paths exist
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config"), []byte("# test"), 0o600))

	cfg := &Config{configDir: configDir}
	require.NoError(t, cfg.installDefaults())

	// verify custom content was preserved
	data, err := os.ReadFile(filepath.Join(agentsDir, "my-agent.txt")) //nolint:gosec // test
	require.NoError(t, err)
	assert.Equal(t, customContent, string(data), "custom agent file should be preserved")

	// verify NO default agents were added - we never add to existing agent directories
	entries, err := os.ReadDir(agentsDir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "should only have the custom agent, no defaults added")
}

func TestConfig_installDefaultAgents_AllFiles(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	cfg := &Config{}
	require.NoError(t, cfg.installDefaultAgents(agentsDir))

	expectedAgents := []string{"implementation.txt", "quality.txt", "documentation.txt", "simplification.txt", "testing.txt"}
	for _, agent := range expectedAgents {
		agentPath := filepath.Join(agentsDir, agent)
		assert.FileExists(t, agentPath, "agent file %s should be installed", agent)

		data, err := os.ReadFile(agentPath) //nolint:gosec // test
		require.NoError(t, err)
		assert.NotEmpty(t, string(data), "agent file %s should have content", agent)
	}
}

func TestConfig_installDefaultAgents_SkipsNonEmptyDir(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	// pre-create one agent with custom content
	customContent := "user's custom agent"
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "my-custom.txt"), []byte(customContent), 0o600))

	cfg := &Config{}
	require.NoError(t, cfg.installDefaultAgents(agentsDir))

	// verify custom content was preserved
	data, err := os.ReadFile(filepath.Join(agentsDir, "my-custom.txt")) //nolint:gosec // test
	require.NoError(t, err)
	assert.Equal(t, customContent, string(data), "existing agent file should not be overwritten")

	// verify NO default agents were added - directory was not empty
	entries, err := os.ReadDir(agentsDir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "should only have the user's custom agent")
}

// --- parse tests ---

func TestConfig_parseConfig_FullConfig(t *testing.T) {
	input := `
claude_command = /custom/claude
claude_args = --custom-arg --verbose
codex_enabled = false
codex_command = /custom/codex
codex_model = gpt-5
codex_reasoning_effort = high
codex_timeout_ms = 7200000
codex_sandbox = none
iteration_delay_ms = 5000
task_retry_count = 3
plans_dir = custom/plans
`
	cfg := &Config{}
	require.NoError(t, cfg.parseConfig(strings.NewReader(input)))

	assert.Equal(t, "/custom/claude", cfg.ClaudeCommand)
	assert.Equal(t, "--custom-arg --verbose", cfg.ClaudeArgs)
	assert.False(t, cfg.CodexEnabled)
	assert.Equal(t, "/custom/codex", cfg.CodexCommand)
	assert.Equal(t, "gpt-5", cfg.CodexModel)
	assert.Equal(t, "high", cfg.CodexReasoningEffort)
	assert.Equal(t, 7200000, cfg.CodexTimeoutMs)
	assert.Equal(t, "none", cfg.CodexSandbox)
	assert.Equal(t, 5000, cfg.IterationDelayMs)
	assert.Equal(t, 3, cfg.TaskRetryCount)
	assert.Equal(t, "custom/plans", cfg.PlansDir)
}

func TestConfig_parseConfig_PartialConfig(t *testing.T) {
	input := `
claude_command = custom-claude
iteration_delay_ms = 3000
`
	cfg := &Config{}
	require.NoError(t, cfg.parseConfig(strings.NewReader(input)))

	assert.Equal(t, "custom-claude", cfg.ClaudeCommand)
	assert.Equal(t, 3000, cfg.IterationDelayMs)
	assert.Empty(t, cfg.ClaudeArgs)
	assert.Empty(t, cfg.CodexCommand)
	assert.False(t, cfg.CodexEnabled)
	assert.Equal(t, 0, cfg.CodexTimeoutMs)
}

func TestConfig_parseConfig_EmptyConfig(t *testing.T) {
	cfg := &Config{}
	require.NoError(t, cfg.parseConfig(strings.NewReader("")))

	assert.Empty(t, cfg.ClaudeCommand)
	assert.False(t, cfg.CodexEnabled)
	assert.Equal(t, 0, cfg.IterationDelayMs)
}

func TestConfig_parseConfig_CommentsOnly(t *testing.T) {
	input := `
# this is a comment
; this is also a comment
`
	cfg := &Config{}
	require.NoError(t, cfg.parseConfig(strings.NewReader(input)))
	assert.Empty(t, cfg.ClaudeCommand)
}

func TestConfig_parseConfig_WithWhitespace(t *testing.T) {
	input := `
  claude_command   =   spaced-claude
	codex_enabled	=	true
`
	cfg := &Config{}
	require.NoError(t, cfg.parseConfig(strings.NewReader(input)))

	assert.Equal(t, "spaced-claude", cfg.ClaudeCommand)
	assert.True(t, cfg.CodexEnabled)
}

func TestConfig_parseConfig_BoolValues(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"true lowercase", "codex_enabled = true", true},
		{"TRUE uppercase", "codex_enabled = TRUE", true},
		{"True mixed", "codex_enabled = True", true},
		{"false lowercase", "codex_enabled = false", false},
		{"FALSE uppercase", "codex_enabled = FALSE", false},
		{"yes", "codex_enabled = yes", true},
		{"no", "codex_enabled = no", false},
		{"1", "codex_enabled = 1", true},
		{"0", "codex_enabled = 0", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{}
			require.NoError(t, cfg.parseConfig(strings.NewReader(tc.input)))
			assert.Equal(t, tc.expected, cfg.CodexEnabled)
		})
	}
}

func TestConfig_parseConfig_IntValues(t *testing.T) {
	input := `
iteration_delay_ms = 1234
task_retry_count = 5
codex_timeout_ms = 999999
`
	cfg := &Config{}
	require.NoError(t, cfg.parseConfig(strings.NewReader(input)))

	assert.Equal(t, 1234, cfg.IterationDelayMs)
	assert.Equal(t, 5, cfg.TaskRetryCount)
	assert.Equal(t, 999999, cfg.CodexTimeoutMs)
}

func TestConfig_parseConfig_InvalidInt(t *testing.T) {
	cfg := &Config{}
	err := cfg.parseConfig(strings.NewReader(`iteration_delay_ms = not_a_number`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "iteration_delay_ms")
}

func TestConfig_parseConfig_NegativeTaskRetryCount(t *testing.T) {
	cfg := &Config{}
	err := cfg.parseConfig(strings.NewReader(`task_retry_count = -1`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "task_retry_count")
	assert.Contains(t, err.Error(), "non-negative")
}

func TestConfig_parseConfig_ZeroTaskRetryCount(t *testing.T) {
	cfg := &Config{}
	require.NoError(t, cfg.parseConfig(strings.NewReader(`task_retry_count = 0`)))
	assert.Equal(t, 0, cfg.TaskRetryCount)
	assert.True(t, cfg.TaskRetryCountSet)
}

func TestConfig_parseConfig_InvalidBool(t *testing.T) {
	cfg := &Config{}
	err := cfg.parseConfig(strings.NewReader(`codex_enabled = maybe`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "codex_enabled")
}

func TestConfig_parseConfig_UnknownKeys(t *testing.T) {
	input := `
claude_command = claude
unknown_key = some_value
another_unknown = 123
`
	cfg := &Config{}
	require.NoError(t, cfg.parseConfig(strings.NewReader(input)))
	assert.Equal(t, "claude", cfg.ClaudeCommand)
}

func TestConfig_parseConfig_WithSection(t *testing.T) {
	input := `
claude_command = valid
[section]
key = value
`
	cfg := &Config{}
	require.NoError(t, cfg.parseConfig(strings.NewReader(input)))
	assert.Equal(t, "valid", cfg.ClaudeCommand)
}

func TestConfig_parseConfig_QuotedValues(t *testing.T) {
	cfg := &Config{}
	require.NoError(t, cfg.parseConfig(strings.NewReader(`claude_args = "--arg1 --arg2 value"`)))
	assert.Equal(t, "--arg1 --arg2 value", cfg.ClaudeArgs)
}

func TestConfig_parseConfig_SpecialCharacters(t *testing.T) {
	cfg := &Config{}
	require.NoError(t, cfg.parseConfig(strings.NewReader(`claude_args = --flag=value --path=/some/path`)))
	assert.Equal(t, "--flag=value --path=/some/path", cfg.ClaudeArgs)
}

func TestConfig_parseConfigBytes(t *testing.T) {
	input := []byte(`
claude_command = byte-claude
codex_enabled = true
`)
	cfg := &Config{}
	require.NoError(t, cfg.parseConfigBytes(input))
	assert.Equal(t, "byte-claude", cfg.ClaudeCommand)
	assert.True(t, cfg.CodexEnabled)
}

// --- load agents tests ---

func TestConfig_loadAgents_FromAgentsDir(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "security.txt"), []byte("check for security issues"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "performance.txt"), []byte("check for performance issues"), 0o600))

	cfg := &Config{configDir: tmpDir}
	require.NoError(t, cfg.loadAgents())

	assert.Len(t, cfg.CustomAgents, 2)
	assert.Equal(t, "performance", cfg.CustomAgents[0].Name)
	assert.Equal(t, "check for performance issues", cfg.CustomAgents[0].Prompt)
	assert.Equal(t, "security", cfg.CustomAgents[1].Name)
	assert.Equal(t, "check for security issues", cfg.CustomAgents[1].Prompt)
}

func TestConfig_loadAgents_NoAgentsDir(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &Config{configDir: tmpDir}
	require.NoError(t, cfg.loadAgents())
	assert.Empty(t, cfg.CustomAgents)
}

func TestConfig_loadAgents_EmptyAgentsDir(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "agents"), 0o700))

	cfg := &Config{configDir: tmpDir}
	require.NoError(t, cfg.loadAgents())
	assert.Empty(t, cfg.CustomAgents)
}

func TestConfig_loadAgents_OnlyTxtFiles(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "valid.txt"), []byte("valid agent"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "invalid.md"), []byte("not an agent"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "another.json"), []byte("{}"), 0o600))

	cfg := &Config{configDir: tmpDir}
	require.NoError(t, cfg.loadAgents())

	assert.Len(t, cfg.CustomAgents, 1)
	assert.Equal(t, "valid", cfg.CustomAgents[0].Name)
	assert.Equal(t, "valid agent", cfg.CustomAgents[0].Prompt)
}

func TestConfig_loadAgents_SkipsEmptyFiles(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "valid.txt"), []byte("valid agent"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "empty.txt"), []byte(""), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "whitespace.txt"), []byte("   \n\t  "), 0o600))

	cfg := &Config{configDir: tmpDir}
	require.NoError(t, cfg.loadAgents())

	assert.Len(t, cfg.CustomAgents, 1)
	assert.Equal(t, "valid", cfg.CustomAgents[0].Name)
}

func TestConfig_loadAgents_TrimsWhitespace(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "agent.txt"), []byte("  prompt with spaces  \n\n"), 0o600))

	cfg := &Config{configDir: tmpDir}
	require.NoError(t, cfg.loadAgents())

	assert.Len(t, cfg.CustomAgents, 1)
	assert.Equal(t, "prompt with spaces", cfg.CustomAgents[0].Prompt)
}

func TestConfig_loadAgents_SkipsDirectories(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	require.NoError(t, os.MkdirAll(filepath.Join(agentsDir, "subdir.txt"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "valid.txt"), []byte("valid agent"), 0o600))

	cfg := &Config{configDir: tmpDir}
	require.NoError(t, cfg.loadAgents())

	assert.Len(t, cfg.CustomAgents, 1)
	assert.Equal(t, "valid", cfg.CustomAgents[0].Name)
}

func TestConfig_loadAgents_PreservesMultilinePrompt(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	prompt := "line one\nline two\nline three"
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "multi.txt"), []byte("  "+prompt+"  \n"), 0o600))

	cfg := &Config{configDir: tmpDir}
	require.NoError(t, cfg.loadAgents())

	assert.Len(t, cfg.CustomAgents, 1)
	assert.Equal(t, prompt, cfg.CustomAgents[0].Prompt)
}

// --- load prompts tests ---

func TestConfig_loadPrompts_FromUserDir(t *testing.T) {
	tmpDir := t.TempDir()
	promptsDir := filepath.Join(tmpDir, "prompts")
	require.NoError(t, os.MkdirAll(promptsDir, 0o700))

	require.NoError(t, os.WriteFile(filepath.Join(promptsDir, "task.txt"), []byte("custom task prompt"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(promptsDir, "review_first.txt"), []byte("custom first review"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(promptsDir, "review_second.txt"), []byte("custom second review"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(promptsDir, "codex.txt"), []byte("custom codex prompt"), 0o600))

	cfg := &Config{configDir: tmpDir}
	require.NoError(t, cfg.loadPrompts())

	assert.Equal(t, "custom task prompt", cfg.TaskPrompt)
	assert.Equal(t, "custom first review", cfg.ReviewFirstPrompt)
	assert.Equal(t, "custom second review", cfg.ReviewSecondPrompt)
	assert.Equal(t, "custom codex prompt", cfg.CodexPrompt)
}

func TestConfig_loadPrompts_PartialUserFiles(t *testing.T) {
	tmpDir := t.TempDir()
	promptsDir := filepath.Join(tmpDir, "prompts")
	require.NoError(t, os.MkdirAll(promptsDir, 0o700))

	require.NoError(t, os.WriteFile(filepath.Join(promptsDir, "task.txt"), []byte("user task prompt"), 0o600))

	cfg := &Config{configDir: tmpDir}
	require.NoError(t, cfg.loadPrompts())

	assert.Equal(t, "user task prompt", cfg.TaskPrompt)
}

func TestConfig_loadPrompts_NoUserDir(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &Config{configDir: tmpDir}
	require.NoError(t, cfg.loadPrompts())
}

func TestConfig_loadPrompts_EmptyUserFile(t *testing.T) {
	tmpDir := t.TempDir()
	promptsDir := filepath.Join(tmpDir, "prompts")
	require.NoError(t, os.MkdirAll(promptsDir, 0o700))

	require.NoError(t, os.WriteFile(filepath.Join(promptsDir, "task.txt"), []byte(""), 0o600))

	cfg := &Config{configDir: tmpDir}
	require.NoError(t, cfg.loadPrompts())
}

func TestConfig_loadPromptFile_Success(t *testing.T) {
	tmpDir := t.TempDir()
	promptFile := filepath.Join(tmpDir, "test.txt")
	require.NoError(t, os.WriteFile(promptFile, []byte("test content\nwith newline"), 0o600))

	cfg := &Config{}
	content, err := cfg.loadPromptFile(promptFile)
	require.NoError(t, err)
	assert.Equal(t, "test content\nwith newline", content)
}

func TestConfig_loadPromptFile_NotExists(t *testing.T) {
	cfg := &Config{}
	content, err := cfg.loadPromptFile("/nonexistent/path/file.txt")
	require.NoError(t, err)
	assert.Empty(t, content)
}

func TestConfig_loadPromptFile_WhitespaceHandling(t *testing.T) {
	tmpDir := t.TempDir()
	promptFile := filepath.Join(tmpDir, "test.txt")
	require.NoError(t, os.WriteFile(promptFile, []byte("  content with spaces  \n\n"), 0o600))

	cfg := &Config{}
	content, err := cfg.loadPromptFile(promptFile)
	require.NoError(t, err)
	assert.Equal(t, "content with spaces", content)
}

func TestConfig_loadPromptFromEmbedFS(t *testing.T) {
	cfg := &Config{}
	fs := DefaultsFS()
	content, err := cfg.loadPromptFromEmbedFS(fs, "defaults/config")
	require.NoError(t, err)
	assert.Contains(t, content, "claude_command")
}

func TestConfig_loadPromptFromEmbedFS_NotFound(t *testing.T) {
	cfg := &Config{}
	fs := DefaultsFS()
	content, err := cfg.loadPromptFromEmbedFS(fs, "nonexistent/file.txt")
	require.NoError(t, err)
	assert.Empty(t, content)
}

func TestConfig_loadPromptWithFallback(t *testing.T) {
	tmpDir := t.TempDir()
	promptsDir := filepath.Join(tmpDir, "prompts")
	require.NoError(t, os.MkdirAll(promptsDir, 0o700))

	require.NoError(t, os.WriteFile(filepath.Join(promptsDir, "task.txt"), []byte("user prompt"), 0o600))

	cfg := &Config{}
	content, err := cfg.loadPromptWithFallback(filepath.Join(promptsDir, "task.txt"), DefaultsFS(), "defaults/prompts/task.txt")
	require.NoError(t, err)
	assert.Equal(t, "user prompt", content)
}

func TestConfig_loadPromptWithFallback_FallsBackToEmbed(t *testing.T) {
	cfg := &Config{}
	content, err := cfg.loadPromptWithFallback("/nonexistent/path.txt", DefaultsFS(), "defaults/prompts/task.txt")
	require.NoError(t, err)
	assert.Contains(t, content, "{{PLAN_FILE}}")
	assert.Contains(t, content, "RALPHEX:ALL_TASKS_DONE")
}

func TestConfig_loadPromptWithFallback_EmptyUserFileUsesDefault(t *testing.T) {
	tmpDir := t.TempDir()
	emptyFile := filepath.Join(tmpDir, "empty.txt")
	require.NoError(t, os.WriteFile(emptyFile, []byte(""), 0o600))

	cfg := &Config{}
	content, err := cfg.loadPromptWithFallback(emptyFile, DefaultsFS(), "defaults/prompts/task.txt")
	require.NoError(t, err)
	assert.Contains(t, content, "{{PLAN_FILE}}")
	assert.Contains(t, content, "RALPHEX:ALL_TASKS_DONE")
}

func TestConfig_loadPromptFromEmbedFS_MockFS(t *testing.T) {
	cfg := &Config{}
	var mockFS embed.FS
	content, err := cfg.loadPromptFromEmbedFS(mockFS, "any/path")
	require.NoError(t, err)
	assert.Empty(t, content)
}

// --- stripComments tests ---

func Test_stripComments(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "no comments", input: "line one\nline two", expected: "line one\nline two"},
		{name: "comment at start", input: "# comment\nkeep this", expected: "keep this"},
		{name: "indented comment", input: "  # indented comment\nkeep this", expected: "keep this"},
		{name: "preserves empty lines", input: "line one\n\nline two", expected: "line one\n\nline two"},
		{name: "hash in content preserved", input: "use {{agent:name}} # not a comment", expected: "use {{agent:name}} # not a comment"},
		{name: "multiple comments", input: "# header comment\nkeep\n# middle comment\nalso keep\n# end comment", expected: "keep\nalso keep"},
		{name: "empty input", input: "", expected: ""},
		{name: "only comments", input: "# comment one\n# comment two", expected: ""},
		{name: "tab indented comment", input: "\t# tab comment\nkeep", expected: "keep"},
		{name: "mixed content", input: "# header\nfirst line\n# comment\n\nsecond line\n  # indented\nthird line", expected: "first line\n\nsecond line\nthird line"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := stripComments(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestConfig_loadPromptFile_StripsComments(t *testing.T) {
	tmpDir := t.TempDir()
	promptFile := filepath.Join(tmpDir, "test.txt")
	content := "# this is a comment\nkeep this line\n  # indented comment\nalso keep this"
	require.NoError(t, os.WriteFile(promptFile, []byte(content), 0o600))

	cfg := &Config{}
	result, err := cfg.loadPromptFile(promptFile)
	require.NoError(t, err)
	assert.Equal(t, "keep this line\nalso keep this", result)
}

func TestConfig_loadAgentFile_StripsComments(t *testing.T) {
	tmpDir := t.TempDir()
	agentFile := filepath.Join(tmpDir, "agent.txt")
	content := "# description of agent\ncheck for security issues\n# additional notes"
	require.NoError(t, os.WriteFile(agentFile, []byte(content), 0o600))

	cfg := &Config{}
	result, err := cfg.loadAgentFile(agentFile)
	require.NoError(t, err)
	assert.Equal(t, "check for security issues", result)
}

func TestConfig_loadAgents_StripsCommentsFromAgentFiles(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	content := "# security agent - checks for vulnerabilities\ncheck for SQL injection\ncheck for XSS\n# end of agent"
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "security.txt"), []byte(content), 0o600))

	cfg := &Config{configDir: tmpDir}
	require.NoError(t, cfg.loadAgents())

	require.Len(t, cfg.CustomAgents, 1)
	assert.Equal(t, "security", cfg.CustomAgents[0].Name)
	assert.Equal(t, "check for SQL injection\ncheck for XSS", cfg.CustomAgents[0].Prompt)
}

// --- parseHexColor tests ---

// --- color config parsing tests ---

func TestConfig_parseConfig_FullColorConfig(t *testing.T) {
	input := `
color_task = #00ff00
color_review = #00ffff
color_codex = #ff00ff
color_claude_eval = #64c8ff
color_warn = #ffff00
color_error = #ff0000
color_signal = #ff6464
color_timestamp = #8a8a8a
color_info = #b4b4b4
`
	cfg := &Config{}
	require.NoError(t, cfg.parseConfig(strings.NewReader(input)))

	assert.Equal(t, "0,255,0", cfg.Colors.Task)
	assert.Equal(t, "0,255,255", cfg.Colors.Review)
	assert.Equal(t, "255,0,255", cfg.Colors.Codex)
	assert.Equal(t, "100,200,255", cfg.Colors.ClaudeEval)
	assert.Equal(t, "255,255,0", cfg.Colors.Warn)
	assert.Equal(t, "255,0,0", cfg.Colors.Error)
	assert.Equal(t, "255,100,100", cfg.Colors.Signal)
	assert.Equal(t, "138,138,138", cfg.Colors.Timestamp)
	assert.Equal(t, "180,180,180", cfg.Colors.Info)
}

func TestConfig_parseConfig_PartialColorConfig(t *testing.T) {
	input := `
color_task = #ff0000
color_error = #00ff00
`
	cfg := &Config{}
	require.NoError(t, cfg.parseConfig(strings.NewReader(input)))

	// explicitly set colors
	assert.Equal(t, "255,0,0", cfg.Colors.Task)
	assert.Equal(t, "0,255,0", cfg.Colors.Error)

	// unset colors should be empty (defaults are applied elsewhere)
	assert.Empty(t, cfg.Colors.Review)
	assert.Empty(t, cfg.Colors.Codex)
	assert.Empty(t, cfg.Colors.ClaudeEval)
	assert.Empty(t, cfg.Colors.Warn)
	assert.Empty(t, cfg.Colors.Signal)
	assert.Empty(t, cfg.Colors.Timestamp)
	assert.Empty(t, cfg.Colors.Info)
}

func TestConfig_parseConfig_InvalidColorHex(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		errMsg string
	}{
		{name: "missing hash", input: "color_task = ff0000", errMsg: "color_task"},
		{name: "wrong length", input: "color_review = #fff", errMsg: "color_review"},
		{name: "invalid chars", input: "color_codex = #gggggg", errMsg: "color_codex"},
		{name: "empty value", input: "color_error = ", errMsg: "color_error"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{}
			err := cfg.parseConfig(strings.NewReader(tc.input))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errMsg)
		})
	}
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
	assert.Equal(t, "255,0,255", cfg.Colors.Codex, "codex color should be magenta (#ff00ff)")
	assert.Equal(t, "100,200,255", cfg.Colors.ClaudeEval, "claude_eval color should be light blue (#64c8ff)")
	assert.Equal(t, "255,255,0", cfg.Colors.Warn, "warn color should be yellow (#ffff00)")
	assert.Equal(t, "255,0,0", cfg.Colors.Error, "error color should be red (#ff0000)")
	assert.Equal(t, "255,100,100", cfg.Colors.Signal, "signal color should be light red (#ff6464)")
	assert.Equal(t, "138,138,138", cfg.Colors.Timestamp, "timestamp color should be gray (#8a8a8a)")
	assert.Equal(t, "180,180,180", cfg.Colors.Info, "info color should be light gray (#b4b4b4)")
}

func TestParseHexColor(t *testing.T) {
	tests := []struct {
		name    string
		hex     string
		wantR   int
		wantG   int
		wantB   int
		wantErr bool
		errMsg  string
	}{
		{name: "valid red", hex: "#ff0000", wantR: 255, wantG: 0, wantB: 0},
		{name: "valid green", hex: "#00ff00", wantR: 0, wantG: 255, wantB: 0},
		{name: "valid blue", hex: "#0000ff", wantR: 0, wantG: 0, wantB: 255},
		{name: "valid lowercase", hex: "#aabbcc", wantR: 170, wantG: 187, wantB: 204},
		{name: "valid uppercase", hex: "#AABBCC", wantR: 170, wantG: 187, wantB: 204},
		{name: "valid mixed case", hex: "#AaBbCc", wantR: 170, wantG: 187, wantB: 204},
		{name: "valid white", hex: "#ffffff", wantR: 255, wantG: 255, wantB: 255},
		{name: "valid black", hex: "#000000", wantR: 0, wantG: 0, wantB: 0},
		{name: "valid gray", hex: "#8a8a8a", wantR: 138, wantG: 138, wantB: 138},
		{name: "missing # prefix", hex: "ff0000", wantErr: true, errMsg: "must start with #"},
		{name: "wrong length short", hex: "#fff", wantErr: true, errMsg: "must be 7 characters"},
		{name: "wrong length long", hex: "#ff00ff00", wantErr: true, errMsg: "must be 7 characters"},
		{name: "empty string", hex: "", wantErr: true, errMsg: "must start with #"},
		{name: "only hash", hex: "#", wantErr: true, errMsg: "must be 7 characters"},
		{name: "invalid hex char g", hex: "#gggggg", wantErr: true, errMsg: "invalid hex"},
		{name: "invalid hex char z", hex: "#zz0000", wantErr: true, errMsg: "invalid hex"},
		{name: "invalid hex space", hex: "#ff 000", wantErr: true, errMsg: "invalid hex"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, g, b, err := parseHexColor(tc.hex)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errMsg)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantR, r, "red component")
			assert.Equal(t, tc.wantG, g, "green component")
			assert.Equal(t, tc.wantB, b, "blue component")
		})
	}
}

// --- scalar fallback tests ---

func TestConfig_loadScalarsWithFallback_PartialConfig(t *testing.T) {
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

func TestConfig_loadScalarsWithFallback_EmptyConfig(t *testing.T) {
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

func TestConfig_loadScalarsWithFallback_ExplicitZeroTaskRetryCount(t *testing.T) {
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

func TestConfig_loadScalarsWithFallback_ExplicitFalseCodexEnabled(t *testing.T) {
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

func TestConfig_loadScalarsWithFallback_UserValuesOverrideDefaults(t *testing.T) {
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

func TestConfig_parseEmbeddedScalars(t *testing.T) {
	cfg := &Config{}
	embedded, err := cfg.parseEmbeddedScalars()
	require.NoError(t, err)

	// verify embedded defaults have expected values
	assert.Equal(t, "claude", embedded.ClaudeCommand)
	assert.Equal(t, "--dangerously-skip-permissions --output-format stream-json --verbose", embedded.ClaudeArgs)
	assert.Equal(t, "codex", embedded.CodexCommand)
	assert.Equal(t, "gpt-5.2-codex", embedded.CodexModel)
	assert.Equal(t, "xhigh", embedded.CodexReasoningEffort)
	assert.Equal(t, "read-only", embedded.CodexSandbox)
	assert.Equal(t, "docs/plans", embedded.PlansDir)
	assert.Equal(t, 2000, embedded.IterationDelayMs)
	assert.Equal(t, 3600000, embedded.CodexTimeoutMs)
	assert.True(t, embedded.CodexEnabled)
	assert.Equal(t, 1, embedded.TaskRetryCount)
}
