package config

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_newDefaultsInstaller(t *testing.T) {
	installer := newDefaultsInstaller(defaultsFS)
	assert.NotNil(t, installer)
}

func TestDefaultsInstaller_Install_CreatesConfigDir(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")

	_, err := os.Stat(configDir)
	require.True(t, os.IsNotExist(err))

	installer := newDefaultsInstaller(defaultsFS)
	require.NoError(t, installer.Install(configDir))

	info, err := os.Stat(configDir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	data, err := os.ReadFile(filepath.Join(configDir, "config")) //nolint:gosec // test
	require.NoError(t, err)
	assert.Contains(t, string(data), "claude_command")
	assert.Contains(t, string(data), "codex_enabled")
}

func TestDefaultsInstaller_Install_ExistingDir(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")
	require.NoError(t, os.MkdirAll(configDir, 0o700))

	installer := newDefaultsInstaller(defaultsFS)
	require.NoError(t, installer.Install(configDir))

	_, err := os.Stat(filepath.Join(configDir, "config"))
	require.NoError(t, err)
}

func TestDefaultsInstaller_Install_ExistingConfigFile(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")
	require.NoError(t, os.MkdirAll(configDir, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "prompts"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "agents"), 0o700))

	existingContent := "# my custom config\nclaude_command = custom"
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config"), []byte(existingContent), 0o600))

	installer := newDefaultsInstaller(defaultsFS)
	require.NoError(t, installer.Install(configDir))

	data, err := os.ReadFile(filepath.Join(configDir, "config")) //nolint:gosec // test
	require.NoError(t, err)
	assert.Equal(t, existingContent, string(data))
}

func TestDefaultsInstaller_Install_CreatesPromptsDir(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")

	installer := newDefaultsInstaller(defaultsFS)
	require.NoError(t, installer.Install(configDir))

	info, err := os.Stat(filepath.Join(configDir, "prompts"))
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestDefaultsInstaller_Install_CreatesAgentsDir(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")

	installer := newDefaultsInstaller(defaultsFS)
	require.NoError(t, installer.Install(configDir))

	info, err := os.Stat(filepath.Join(configDir, "agents"))
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestDefaultsInstaller_Install_SkipsWhenAllPathsExist(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")

	require.NoError(t, os.MkdirAll(configDir, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "prompts"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "agents"), 0o700))

	customContent := "# custom\nclaude_command = my-claude"
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config"), []byte(customContent), 0o600))

	installer := newDefaultsInstaller(defaultsFS)
	require.NoError(t, installer.Install(configDir))

	// config should not be overwritten
	data, err := os.ReadFile(filepath.Join(configDir, "config")) //nolint:gosec // test
	require.NoError(t, err)
	assert.Equal(t, customContent, string(data))

	// empty prompts dir should get defaults installed
	promptFiles, err := os.ReadDir(filepath.Join(configDir, "prompts"))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(promptFiles), 4, "expected prompt files to be installed into empty dir")

	// empty agents dir should get defaults installed
	agentFiles, err := os.ReadDir(filepath.Join(configDir, "agents"))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(agentFiles), 5, "expected agent files to be installed into empty dir")
}

func TestDefaultsInstaller_Install_InstallsIfPromptsOrAgentsMissing(t *testing.T) {
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

			installer := newDefaultsInstaller(defaultsFS)
			require.NoError(t, installer.Install(configDir))

			for _, p := range []string{"config", "prompts", "agents"} {
				_, err := os.Stat(filepath.Join(configDir, p))
				require.NoError(t, err, "path %s should exist", p)
			}
		})
	}
}

func TestDefaultsInstaller_Install_InstallsAgentFiles(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")

	installer := newDefaultsInstaller(defaultsFS)
	require.NoError(t, installer.Install(configDir))

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

func TestDefaultsInstaller_Install_NeverOverwritesAgents(t *testing.T) {
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

	installer := newDefaultsInstaller(defaultsFS)
	require.NoError(t, installer.Install(configDir))

	// verify custom content was preserved
	data, err := os.ReadFile(filepath.Join(agentsDir, "my-agent.txt")) //nolint:gosec // test
	require.NoError(t, err)
	assert.Equal(t, customContent, string(data), "custom agent file should be preserved")

	// verify NO default agents were added - we never add to existing agent directories
	entries, err := os.ReadDir(agentsDir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "should only have the custom agent, no defaults added")
}

func TestDefaultsInstaller_installDefaultFiles_Agents(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	installer := &defaultsInstaller{embedFS: defaultsFS}
	require.NoError(t, installer.installDefaultFiles(agentsDir, "defaults/agents", "agent"))

	expectedAgents := []string{"implementation.txt", "quality.txt", "documentation.txt", "simplification.txt", "testing.txt"}
	for _, agent := range expectedAgents {
		agentPath := filepath.Join(agentsDir, agent)
		assert.FileExists(t, agentPath, "agent file %s should be installed", agent)

		data, err := os.ReadFile(agentPath) //nolint:gosec // test
		require.NoError(t, err)
		assert.NotEmpty(t, string(data), "agent file %s should have content", agent)
	}
}

func TestDefaultsInstaller_installDefaultFiles_AgentsSkipsNonEmptyDir(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	// pre-create one agent with custom content
	customContent := "user's custom agent"
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "my-custom.txt"), []byte(customContent), 0o600))

	installer := &defaultsInstaller{embedFS: defaultsFS}
	require.NoError(t, installer.installDefaultFiles(agentsDir, "defaults/agents", "agent"))

	// verify custom content was preserved
	data, err := os.ReadFile(filepath.Join(agentsDir, "my-custom.txt")) //nolint:gosec // test
	require.NoError(t, err)
	assert.Equal(t, customContent, string(data), "existing agent file should not be overwritten")

	// verify NO default agents were added - directory was not empty
	entries, err := os.ReadDir(agentsDir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "should only have the user's custom agent")
}

func TestDefaultsInstaller_installDefaultFiles_Prompts(t *testing.T) {
	tmpDir := t.TempDir()
	promptsDir := filepath.Join(tmpDir, "prompts")
	require.NoError(t, os.MkdirAll(promptsDir, 0o700))

	installer := &defaultsInstaller{embedFS: defaultsFS}
	require.NoError(t, installer.installDefaultFiles(promptsDir, "defaults/prompts", "prompt"))

	expectedPrompts := []string{"task.txt", "review_first.txt", "review_second.txt", "codex.txt", "make_plan.txt"}
	for _, prompt := range expectedPrompts {
		promptPath := filepath.Join(promptsDir, prompt)
		assert.FileExists(t, promptPath, "prompt file %s should be installed", prompt)

		data, err := os.ReadFile(promptPath) //nolint:gosec // test
		require.NoError(t, err)
		assert.NotEmpty(t, string(data), "prompt file %s should have content", prompt)
	}
}

func TestDefaultsInstaller_installDefaultFiles_PromptsSkipsNonEmptyDir(t *testing.T) {
	tmpDir := t.TempDir()
	promptsDir := filepath.Join(tmpDir, "prompts")
	require.NoError(t, os.MkdirAll(promptsDir, 0o700))

	// pre-create one prompt with custom content
	customContent := "user's custom prompt"
	require.NoError(t, os.WriteFile(filepath.Join(promptsDir, "my-custom.txt"), []byte(customContent), 0o600))

	installer := &defaultsInstaller{embedFS: defaultsFS}
	require.NoError(t, installer.installDefaultFiles(promptsDir, "defaults/prompts", "prompt"))

	// verify custom content was preserved
	data, err := os.ReadFile(filepath.Join(promptsDir, "my-custom.txt")) //nolint:gosec // test
	require.NoError(t, err)
	assert.Equal(t, customContent, string(data), "existing prompt file should not be overwritten")

	// verify NO default prompts were added - directory was not empty
	entries, err := os.ReadDir(promptsDir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "should only have the user's custom prompt")
}

func TestDefaultsInstaller_Install_InstallsPromptFiles(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")

	installer := newDefaultsInstaller(defaultsFS)
	require.NoError(t, installer.Install(configDir))

	promptsDir := filepath.Join(configDir, "prompts")
	expectedPrompts := []string{"task.txt", "review_first.txt", "review_second.txt", "codex.txt", "make_plan.txt"}

	for _, prompt := range expectedPrompts {
		promptPath := filepath.Join(promptsDir, prompt)
		assert.FileExists(t, promptPath, "prompt file %s should be installed", prompt)

		data, err := os.ReadFile(promptPath) //nolint:gosec // test
		require.NoError(t, err)
		assert.NotEmpty(t, string(data), "prompt file %s should have content", prompt)
	}
}

func TestDefaultsInstaller_Install_NeverOverwritesPrompts(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")
	promptsDir := filepath.Join(configDir, "prompts")
	require.NoError(t, os.MkdirAll(promptsDir, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "agents"), 0o700))

	// create a single custom prompt file
	customContent := "my custom prompt"
	require.NoError(t, os.WriteFile(filepath.Join(promptsDir, "my-prompt.txt"), []byte(customContent), 0o600))

	// write config file so all paths exist
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config"), []byte("# test"), 0o600))

	installer := newDefaultsInstaller(defaultsFS)
	require.NoError(t, installer.Install(configDir))

	// verify custom content was preserved
	data, err := os.ReadFile(filepath.Join(promptsDir, "my-prompt.txt")) //nolint:gosec // test
	require.NoError(t, err)
	assert.Equal(t, customContent, string(data), "custom prompt file should be preserved")

	// verify NO default prompts were added - we never add to existing prompt directories
	entries, err := os.ReadDir(promptsDir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "should only have the custom prompt, no defaults added")
}

func TestDefaultsInstaller_Install_MkdirAllFailure(t *testing.T) {
	// use a path that cannot be created (file as parent)
	tmpDir := t.TempDir()
	blockingFile := filepath.Join(tmpDir, "blocker")
	require.NoError(t, os.WriteFile(blockingFile, []byte("file"), 0o600))

	// try to create directory inside a file - should fail
	installer := newDefaultsInstaller(defaultsFS)
	err := installer.Install(filepath.Join(blockingFile, "subdir"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create config dir")
}

func TestDefaultsInstaller_Install_WriteFileFailure(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	require.NoError(t, os.MkdirAll(configDir, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "prompts"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "agents"), 0o700))

	// make config dir read-only to prevent file creation
	require.NoError(t, os.Chmod(configDir, 0o500))       //nolint:gosec // intentional for test
	t.Cleanup(func() { _ = os.Chmod(configDir, 0o700) }) //nolint:gosec // test cleanup

	installer := newDefaultsInstaller(defaultsFS)
	err := installer.Install(configDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "write config file")
}

func TestDefaultsInstaller_installDefaultFiles_ReadDirPermissionDenied(t *testing.T) {
	tmpDir := t.TempDir()
	destDir := filepath.Join(tmpDir, "dest")
	require.NoError(t, os.MkdirAll(destDir, 0o700))

	// make dest dir unreadable
	require.NoError(t, os.Chmod(destDir, 0o000))
	t.Cleanup(func() { _ = os.Chmod(destDir, 0o700) }) //nolint:gosec // test cleanup

	installer := &defaultsInstaller{embedFS: defaultsFS}
	err := installer.installDefaultFiles(destDir, "defaults/prompts", "prompt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read prompt dir")
}

func TestDefaultsInstaller_installDefaultFiles_WriteFilePermissionDenied(t *testing.T) {
	tmpDir := t.TempDir()
	destDir := filepath.Join(tmpDir, "dest")
	require.NoError(t, os.MkdirAll(destDir, 0o700))

	// make dest dir read-only (can list but not write)
	require.NoError(t, os.Chmod(destDir, 0o500))       //nolint:gosec // intentional for test
	t.Cleanup(func() { _ = os.Chmod(destDir, 0o700) }) //nolint:gosec // test cleanup

	installer := &defaultsInstaller{embedFS: defaultsFS}
	err := installer.installDefaultFiles(destDir, "defaults/prompts", "prompt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "write prompt file")
}

func TestReset_CreatesConfigDirIfMissing(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "nonexistent", "ralphex") // nested non-existent path

	// config dir does not exist - reset should create it
	stdin := strings.NewReader("y\ny\ny\n")
	stdout := &bytes.Buffer{}

	result, err := Reset(configDir, stdin, stdout)
	require.NoError(t, err)
	assert.True(t, result.ConfigReset)
	assert.True(t, result.PromptsReset)
	assert.True(t, result.AgentsReset)

	// verify config dir and files were created
	configPath := filepath.Join(configDir, "config")
	data, err := os.ReadFile(configPath) //nolint:gosec // test
	require.NoError(t, err)
	assert.Contains(t, string(data), "claude_command = claude")
}

func TestReset_ResetsConfigFile(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")

	// install defaults first
	installer := newDefaultsInstaller(defaultsFS)
	require.NoError(t, installer.Install(configDir))

	// modify config file
	configPath := filepath.Join(configDir, "config")
	require.NoError(t, os.WriteFile(configPath, []byte("# modified config\nclaude_command = custom"), 0o600))

	// verify it's modified
	data, err := os.ReadFile(configPath) //nolint:gosec // test
	require.NoError(t, err)
	assert.Contains(t, string(data), "custom")

	// reset with "y" answers
	stdin := strings.NewReader("y\nn\nn\n")
	stdout := &bytes.Buffer{}

	result, err := Reset(configDir, stdin, stdout)
	require.NoError(t, err)
	assert.True(t, result.ConfigReset)
	assert.False(t, result.PromptsReset)
	assert.False(t, result.AgentsReset)

	// verify config was reset to default
	data, err = os.ReadFile(configPath) //nolint:gosec // test
	require.NoError(t, err)
	assert.Contains(t, string(data), "claude_command = claude")
	assert.NotContains(t, string(data), "custom")
}

func TestReset_ResetsPromptsDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")

	// install defaults first
	installer := newDefaultsInstaller(defaultsFS)
	require.NoError(t, installer.Install(configDir))

	// modify a prompt file
	taskPromptPath := filepath.Join(configDir, "prompts", "task.txt")
	require.NoError(t, os.WriteFile(taskPromptPath, []byte("modified prompt content"), 0o600))

	// reset with "y" for prompts (config auto-skips since unmodified, agents auto-skips)
	stdin := strings.NewReader("y\n")
	stdout := &bytes.Buffer{}

	result, err := Reset(configDir, stdin, stdout)
	require.NoError(t, err)
	assert.False(t, result.ConfigReset)
	assert.True(t, result.PromptsReset)
	assert.False(t, result.AgentsReset)

	// verify prompt was reset
	data, err := os.ReadFile(taskPromptPath) //nolint:gosec // test
	require.NoError(t, err)
	assert.NotContains(t, string(data), "modified prompt content")
}

func TestReset_ResetsAgentsDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")

	// install defaults first
	installer := newDefaultsInstaller(defaultsFS)
	require.NoError(t, installer.Install(configDir))

	// modify an agent file
	qualityAgentPath := filepath.Join(configDir, "agents", "quality.txt")
	require.NoError(t, os.WriteFile(qualityAgentPath, []byte("modified agent content"), 0o600))

	// reset with "y" for agents (config/prompts auto-skip since unmodified)
	stdin := strings.NewReader("y\n")
	stdout := &bytes.Buffer{}

	result, err := Reset(configDir, stdin, stdout)
	require.NoError(t, err)
	assert.False(t, result.ConfigReset)
	assert.False(t, result.PromptsReset)
	assert.True(t, result.AgentsReset)

	// verify agent was reset
	data, err := os.ReadFile(qualityAgentPath) //nolint:gosec // test
	require.NoError(t, err)
	assert.NotContains(t, string(data), "modified agent content")
}

func TestReset_PreservesCustomAgents(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")

	// install defaults first
	installer := newDefaultsInstaller(defaultsFS)
	require.NoError(t, installer.Install(configDir))

	// add a custom agent AND modify a default agent
	customAgentPath := filepath.Join(configDir, "agents", "my-custom-agent.txt")
	require.NoError(t, os.WriteFile(customAgentPath, []byte("my custom agent content"), 0o600))
	qualityAgentPath := filepath.Join(configDir, "agents", "quality.txt")
	require.NoError(t, os.WriteFile(qualityAgentPath, []byte("modified quality agent"), 0o600))

	// reset agents - user says yes to reset modified defaults
	stdin := strings.NewReader("y\n")
	stdout := &bytes.Buffer{}

	result, err := Reset(configDir, stdin, stdout)
	require.NoError(t, err)
	assert.True(t, result.AgentsReset)

	// verify custom agent was preserved
	data, err := os.ReadFile(customAgentPath) //nolint:gosec // test
	require.NoError(t, err)
	assert.Equal(t, "my custom agent content", string(data))

	// verify default agent was reset
	data, err = os.ReadFile(qualityAgentPath) //nolint:gosec // test
	require.NoError(t, err)
	assert.NotEqual(t, "modified quality agent", string(data))
}

func TestReset_SkipsWhenAllDefault(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")

	// install defaults first - don't modify anything
	installer := newDefaultsInstaller(defaultsFS)
	require.NoError(t, installer.Install(configDir))

	// all components match defaults, so all auto-skip (no input needed)
	stdin := strings.NewReader("")
	stdout := &bytes.Buffer{}

	result, err := Reset(configDir, stdin, stdout)
	require.NoError(t, err)
	assert.False(t, result.ConfigReset)
	assert.False(t, result.PromptsReset)
	assert.False(t, result.AgentsReset)

	// verify output shows skipped
	assert.Contains(t, stdout.String(), "skipped (all files match defaults)")
}

func TestReset_ResetsAllComponents(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")

	// install defaults first
	installer := newDefaultsInstaller(defaultsFS)
	require.NoError(t, installer.Install(configDir))

	// modify everything with actual content (not just comments)
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config"), []byte("claude_command = custom"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "prompts", "task.txt"), []byte("modified prompt"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "agents", "quality.txt"), []byte("modified agent"), 0o600))

	// reset all with "y" answers
	stdin := strings.NewReader("y\ny\ny\n")
	stdout := &bytes.Buffer{}

	result, err := Reset(configDir, stdin, stdout)
	require.NoError(t, err)
	assert.True(t, result.ConfigReset)
	assert.True(t, result.PromptsReset)
	assert.True(t, result.AgentsReset)

	// verify summary output
	output := stdout.String()
	assert.Contains(t, output, "Done.")
	assert.Contains(t, output, "Reset: config, prompts, agents")
}

func TestReset_ShowsDifferentFilesWithDates(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")

	// install defaults first
	installer := newDefaultsInstaller(defaultsFS)
	require.NoError(t, installer.Install(configDir))

	// modify a prompt file
	taskPromptPath := filepath.Join(configDir, "prompts", "task.txt")
	require.NoError(t, os.WriteFile(taskPromptPath, []byte("modified"), 0o600))

	// run reset
	stdin := strings.NewReader("n\nn\nn\n")
	stdout := &bytes.Buffer{}

	_, err := Reset(configDir, stdin, stdout)
	require.NoError(t, err)

	// verify output shows different files
	output := stdout.String()
	assert.Contains(t, output, "Different from current defaults")
	assert.Contains(t, output, "task.txt")
}

func TestReset_SkipsAgentsWhenOnlyCustomExist(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")

	// install defaults first
	installer := newDefaultsInstaller(defaultsFS)
	require.NoError(t, installer.Install(configDir))

	// add only a custom agent (no modified defaults)
	customAgentPath := filepath.Join(configDir, "agents", "my-custom-agent.txt")
	require.NoError(t, os.WriteFile(customAgentPath, []byte("custom content"), 0o600))

	// no input needed - agents should auto-skip since only custom files exist
	stdin := strings.NewReader("")
	stdout := &bytes.Buffer{}

	result, err := Reset(configDir, stdin, stdout)
	require.NoError(t, err)
	assert.False(t, result.AgentsReset)

	// verify output shows skipped
	output := stdout.String()
	assert.Contains(t, output, "Agents directory?")
	assert.Contains(t, output, "skipped (all files match defaults)")
}

func TestReset_ShowsCustomAgents(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")

	// install defaults first
	installer := newDefaultsInstaller(defaultsFS)
	require.NoError(t, installer.Install(configDir))

	// add a custom agent AND modify a default agent (so the prompt is shown)
	customAgentPath := filepath.Join(configDir, "agents", "my-special-agent.txt")
	require.NoError(t, os.WriteFile(customAgentPath, []byte("custom content"), 0o600))
	qualityAgentPath := filepath.Join(configDir, "agents", "quality.txt")
	require.NoError(t, os.WriteFile(qualityAgentPath, []byte("modified"), 0o600))

	// run reset (decline)
	stdin := strings.NewReader("n\n")
	stdout := &bytes.Buffer{}

	_, err := Reset(configDir, stdin, stdout)
	require.NoError(t, err)

	// verify output shows custom agents
	output := stdout.String()
	assert.Contains(t, output, "Custom agents (untouched)")
	assert.Contains(t, output, "my-special-agent.txt")
}

func TestReset_HandlesEOFGracefully(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")

	// install defaults first
	installer := newDefaultsInstaller(defaultsFS)
	require.NoError(t, installer.Install(configDir))

	// modify config so prompt would be shown
	configPath := filepath.Join(configDir, "config")
	require.NoError(t, os.WriteFile(configPath, []byte("# modified"), 0o600))

	// empty stdin (simulates Ctrl+D)
	stdin := strings.NewReader("")
	stdout := &bytes.Buffer{}

	result, err := Reset(configDir, stdin, stdout)
	require.NoError(t, err)
	assert.False(t, result.ConfigReset) // EOF = decline
}

func TestDefaultsInstaller_FindDifferentFiles(t *testing.T) {
	tmpDir := t.TempDir()
	promptsDir := filepath.Join(tmpDir, "prompts")
	require.NoError(t, os.MkdirAll(promptsDir, 0o700))

	// install default prompts
	installer := &defaultsInstaller{embedFS: defaultsFS}
	require.NoError(t, installer.installDefaultFiles(promptsDir, "defaults/prompts", "prompt"))

	t.Run("returns_empty_when_all_match", func(t *testing.T) {
		different, err := installer.findDifferentFiles(promptsDir, "defaults/prompts")
		require.NoError(t, err)
		assert.Empty(t, different)
	})

	t.Run("returns_modified_files", func(t *testing.T) {
		// modify one file
		taskPath := filepath.Join(promptsDir, "task.txt")
		require.NoError(t, os.WriteFile(taskPath, []byte("modified content"), 0o600))

		different, err := installer.findDifferentFiles(promptsDir, "defaults/prompts")
		require.NoError(t, err)
		assert.Len(t, different, 1)
		assert.Equal(t, "task.txt", different[0].name)
		assert.False(t, different[0].missing)
	})

	t.Run("marks_missing_files", func(t *testing.T) {
		// delete one file
		taskPath := filepath.Join(promptsDir, "codex.txt")
		require.NoError(t, os.Remove(taskPath))

		different, err := installer.findDifferentFiles(promptsDir, "defaults/prompts")
		require.NoError(t, err)

		// find the missing file in results
		var missingFile *fileInfo
		for i := range different {
			if different[i].name == "codex.txt" {
				missingFile = &different[i]
				break
			}
		}
		require.NotNil(t, missingFile, "codex.txt should be in different files")
		assert.True(t, missingFile.missing, "codex.txt should be marked as missing")
	})
}

func TestDefaultsInstaller_FindCustomFiles(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	// install default agents
	installer := &defaultsInstaller{embedFS: defaultsFS}
	require.NoError(t, installer.installDefaultFiles(agentsDir, "defaults/agents", "agent"))

	t.Run("returns_empty_when_no_custom", func(t *testing.T) {
		custom, err := installer.findCustomFiles(agentsDir, "defaults/agents")
		require.NoError(t, err)
		assert.Empty(t, custom)
	})

	t.Run("returns_custom_files", func(t *testing.T) {
		// add custom agent
		customPath := filepath.Join(agentsDir, "my-agent.txt")
		require.NoError(t, os.WriteFile(customPath, []byte("custom"), 0o600))

		custom, err := installer.findCustomFiles(agentsDir, "defaults/agents")
		require.NoError(t, err)
		assert.Equal(t, []string{"my-agent.txt"}, custom)
	})
}

func TestDefaultsInstaller_CountEmbeddedFiles(t *testing.T) {
	installer := &defaultsInstaller{embedFS: defaultsFS}

	count, err := installer.countEmbeddedFiles("defaults/agents")
	require.NoError(t, err)
	assert.GreaterOrEqual(t, count, 1, "should have at least one embedded agent")

	count, err = installer.countEmbeddedFiles("defaults/prompts")
	require.NoError(t, err)
	assert.GreaterOrEqual(t, count, 1, "should have at least one embedded prompt")
}

func TestDefaultsInstaller_OverwriteEmbeddedFiles(t *testing.T) {
	tmpDir := t.TempDir()
	destDir := filepath.Join(tmpDir, "dest")
	installer := &defaultsInstaller{embedFS: defaultsFS}

	// first call creates files
	err := installer.overwriteEmbeddedFiles(destDir, "defaults/prompts")
	require.NoError(t, err)

	// verify files exist
	entries, err := os.ReadDir(destDir)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(entries), 1, "should have at least one file")

	// modify a file
	taskPath := filepath.Join(destDir, "task.txt")
	require.NoError(t, os.WriteFile(taskPath, []byte("modified"), 0o600))

	// add a custom file
	customPath := filepath.Join(destDir, "custom.txt")
	require.NoError(t, os.WriteFile(customPath, []byte("custom"), 0o600))

	// overwrite again
	err = installer.overwriteEmbeddedFiles(destDir, "defaults/prompts")
	require.NoError(t, err)

	// verify task.txt was overwritten
	data, err := os.ReadFile(taskPath) //nolint:gosec // test
	require.NoError(t, err)
	assert.NotEqual(t, "modified", string(data))

	// verify custom file was preserved
	data, err = os.ReadFile(customPath) //nolint:gosec // test
	require.NoError(t, err)
	assert.Equal(t, "custom", string(data))
}

func TestDefaultsInstaller_AskYesNo(t *testing.T) {
	installer := &defaultsInstaller{embedFS: defaultsFS}

	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{name: "lowercase_y", input: "y\n", expected: true},
		{name: "uppercase_Y", input: "Y\n", expected: true},
		{name: "yes", input: "yes\n", expected: true},
		{name: "YES", input: "YES\n", expected: true},
		{name: "n", input: "n\n", expected: false},
		{name: "no", input: "no\n", expected: false},
		{name: "empty", input: "\n", expected: false},
		{name: "other", input: "maybe\n", expected: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reader := strings.NewReader(tc.input)
			stdout := &bytes.Buffer{}
			result := installer.askYesNo(bufio.NewScanner(reader), stdout)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func Test_commentOutContent(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "regular_lines", input: "line1\nline2\nline3", expected: "# line1\n# line2\n# line3"},
		{name: "already_commented", input: "# comment\n# another", expected: "# comment\n# another"},
		{name: "empty_lines", input: "\n\n", expected: "\n\n"},
		{name: "mixed_content", input: "line1\n# comment\n\nline2", expected: "# line1\n# comment\n\n# line2"},
		{name: "whitespace_only_line", input: "   \nline", expected: "   \n# line"},
		{name: "indented_content", input: "  indented\n    more", expected: "#   indented\n#     more"},
		{name: "comment_with_space", input: "  # indented comment", expected: "  # indented comment"},
		{name: "crlf_line_endings", input: "line1\r\nline2\r\n", expected: "# line1\n# line2\n"},
		{name: "empty_string", input: "", expected: ""},
		{name: "single_line", input: "single", expected: "# single"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := commentOutContent(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func Test_shouldOverwrite(t *testing.T) {
	t.Run("file_not_exists", func(t *testing.T) {
		tmpDir := t.TempDir()
		nonExistent := filepath.Join(tmpDir, "nonexistent.txt")
		assert.True(t, shouldOverwrite(nonExistent))
	})

	t.Run("empty_file", func(t *testing.T) {
		tmpDir := t.TempDir()
		emptyFile := filepath.Join(tmpDir, "empty.txt")
		require.NoError(t, os.WriteFile(emptyFile, []byte(""), 0o600))
		assert.True(t, shouldOverwrite(emptyFile))
	})

	t.Run("all_commented", func(t *testing.T) {
		tmpDir := t.TempDir()
		commentedFile := filepath.Join(tmpDir, "commented.txt")
		content := "# comment 1\n# comment 2\n# comment 3"
		require.NoError(t, os.WriteFile(commentedFile, []byte(content), 0o600))
		assert.True(t, shouldOverwrite(commentedFile))
	})

	t.Run("comments_and_empty_lines", func(t *testing.T) {
		tmpDir := t.TempDir()
		mixedFile := filepath.Join(tmpDir, "mixed.txt")
		content := "# comment\n\n  \n# another comment\n"
		require.NoError(t, os.WriteFile(mixedFile, []byte(content), 0o600))
		assert.True(t, shouldOverwrite(mixedFile))
	})

	t.Run("has_content", func(t *testing.T) {
		tmpDir := t.TempDir()
		contentFile := filepath.Join(tmpDir, "content.txt")
		content := "# comment\nactual content\n# more comment"
		require.NoError(t, os.WriteFile(contentFile, []byte(content), 0o600))
		assert.False(t, shouldOverwrite(contentFile))
	})

	t.Run("only_whitespace", func(t *testing.T) {
		tmpDir := t.TempDir()
		whitespaceFile := filepath.Join(tmpDir, "whitespace.txt")
		content := "   \n\t\n  \t  "
		require.NoError(t, os.WriteFile(whitespaceFile, []byte(content), 0o600))
		assert.True(t, shouldOverwrite(whitespaceFile))
	})

	t.Run("single_uncommented_line", func(t *testing.T) {
		tmpDir := t.TempDir()
		singleFile := filepath.Join(tmpDir, "single.txt")
		content := "claude_command = custom"
		require.NoError(t, os.WriteFile(singleFile, []byte(content), 0o600))
		assert.False(t, shouldOverwrite(singleFile))
	})
}

func TestDefaultsInstaller_Install_WritesCommentedConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")

	installer := newDefaultsInstaller(defaultsFS)
	require.NoError(t, installer.Install(configDir))

	// verify config file is written with commented content
	data, err := os.ReadFile(filepath.Join(configDir, "config")) //nolint:gosec // test
	require.NoError(t, err)
	content := string(data)

	// all lines should be comments or empty
	for line := range strings.SplitSeq(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		assert.True(t, strings.HasPrefix(trimmed, "#"), "line should be commented: %q", line)
	}

	// should contain expected settings (commented)
	assert.Contains(t, content, "# claude_command")
	assert.Contains(t, content, "# codex_enabled")
}

func TestDefaultsInstaller_Install_OverwritesCommentedConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")
	require.NoError(t, os.MkdirAll(configDir, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "prompts"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "agents"), 0o700))

	// create config with only comments (safe to overwrite)
	existingContent := "# old commented content\n# more comments\n"
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config"), []byte(existingContent), 0o600))

	installer := newDefaultsInstaller(defaultsFS)
	require.NoError(t, installer.Install(configDir))

	// verify config was overwritten with new commented defaults
	data, err := os.ReadFile(filepath.Join(configDir, "config")) //nolint:gosec // test
	require.NoError(t, err)
	content := string(data)

	// should have new content (not old)
	assert.NotEqual(t, existingContent, content)
	assert.Contains(t, content, "# claude_command")
}

func TestDefaultsInstaller_Install_PreservesCustomConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")
	require.NoError(t, os.MkdirAll(configDir, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "prompts"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "agents"), 0o700))

	// create config with actual content (should NOT be overwritten)
	existingContent := "# my custom config\nclaude_command = my-custom-claude\n"
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config"), []byte(existingContent), 0o600))

	installer := newDefaultsInstaller(defaultsFS)
	require.NoError(t, installer.Install(configDir))

	// verify config was NOT overwritten
	data, err := os.ReadFile(filepath.Join(configDir, "config")) //nolint:gosec // test
	require.NoError(t, err)
	assert.Equal(t, existingContent, string(data))
}

func TestDefaultsInstaller_Install_WritesCommentedPrompts(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")

	installer := newDefaultsInstaller(defaultsFS)
	require.NoError(t, installer.Install(configDir))

	// verify prompt files are written with commented content
	promptsDir := filepath.Join(configDir, "prompts")
	entries, err := os.ReadDir(promptsDir)
	require.NoError(t, err)
	assert.NotEmpty(t, entries)

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(promptsDir, entry.Name())) //nolint:gosec // test
		require.NoError(t, err)
		content := string(data)

		// all lines should be comments or empty
		for line := range strings.SplitSeq(content, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			assert.True(t, strings.HasPrefix(trimmed, "#"), "line in %s should be commented: %q", entry.Name(), line)
		}
	}
}

func TestDefaultsInstaller_Install_OverwritesCommentedPrompts(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")
	promptsDir := filepath.Join(configDir, "prompts")
	require.NoError(t, os.MkdirAll(promptsDir, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "agents"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config"), []byte("# config"), 0o600))

	// create a prompt file with only comments (safe to overwrite)
	existingContent := "# old commented prompt\n# more comments\n"
	require.NoError(t, os.WriteFile(filepath.Join(promptsDir, "task.txt"), []byte(existingContent), 0o600))

	installer := newDefaultsInstaller(defaultsFS)
	require.NoError(t, installer.Install(configDir))

	// verify all default prompts were installed
	entries, err := os.ReadDir(promptsDir)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(entries), 4, "should have default prompts installed")

	// verify task.txt was overwritten with new commented content
	data, err := os.ReadFile(filepath.Join(promptsDir, "task.txt")) //nolint:gosec // test
	require.NoError(t, err)
	assert.NotEqual(t, existingContent, string(data))
}

func TestDefaultsInstaller_Install_PreservesCustomPrompts(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")
	promptsDir := filepath.Join(configDir, "prompts")
	require.NoError(t, os.MkdirAll(promptsDir, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "agents"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config"), []byte("# config"), 0o600))

	// create a prompt file with actual content (should NOT trigger install)
	existingContent := "# my custom prompt\nactual task prompt content\n"
	require.NoError(t, os.WriteFile(filepath.Join(promptsDir, "task.txt"), []byte(existingContent), 0o600))

	installer := newDefaultsInstaller(defaultsFS)
	require.NoError(t, installer.Install(configDir))

	// verify prompt was NOT overwritten
	data, err := os.ReadFile(filepath.Join(promptsDir, "task.txt")) //nolint:gosec // test
	require.NoError(t, err)
	assert.Equal(t, existingContent, string(data))

	// verify no other prompts were added (directory has content)
	entries, err := os.ReadDir(promptsDir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "should only have the custom prompt")
}

func TestDefaultsInstaller_Install_WritesCommentedAgents(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")

	installer := newDefaultsInstaller(defaultsFS)
	require.NoError(t, installer.Install(configDir))

	// verify agent files are written with commented content
	agentsDir := filepath.Join(configDir, "agents")
	entries, err := os.ReadDir(agentsDir)
	require.NoError(t, err)
	assert.NotEmpty(t, entries)

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(agentsDir, entry.Name())) //nolint:gosec // test
		require.NoError(t, err)
		content := string(data)

		// all lines should be comments or empty
		for line := range strings.SplitSeq(content, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			assert.True(t, strings.HasPrefix(trimmed, "#"), "line in %s should be commented: %q", entry.Name(), line)
		}
	}
}

func TestDefaultsInstaller_Install_OverwritesCommentedAgents(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")
	agentsDir := filepath.Join(configDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "prompts"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config"), []byte("# config"), 0o600))

	// create an agent file with only comments (safe to overwrite)
	existingContent := "# old commented agent\n# more comments\n"
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "quality.txt"), []byte(existingContent), 0o600))

	installer := newDefaultsInstaller(defaultsFS)
	require.NoError(t, installer.Install(configDir))

	// verify all default agents were installed
	entries, err := os.ReadDir(agentsDir)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(entries), 5, "should have default agents installed")

	// verify quality.txt was overwritten with new commented content
	data, err := os.ReadFile(filepath.Join(agentsDir, "quality.txt")) //nolint:gosec // test
	require.NoError(t, err)
	assert.NotEqual(t, existingContent, string(data))
}

func TestDefaultsInstaller_Install_PreservesCustomAgents(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")
	agentsDir := filepath.Join(configDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(configDir, "prompts"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config"), []byte("# config"), 0o600))

	// create an agent file with actual content (should NOT trigger install)
	existingContent := "# my custom agent\nactual agent content\n"
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "quality.txt"), []byte(existingContent), 0o600))

	installer := newDefaultsInstaller(defaultsFS)
	require.NoError(t, installer.Install(configDir))

	// verify agent was NOT overwritten
	data, err := os.ReadFile(filepath.Join(agentsDir, "quality.txt")) //nolint:gosec // test
	require.NoError(t, err)
	assert.Equal(t, existingContent, string(data))

	// verify no other agents were added (directory has content)
	entries, err := os.ReadDir(agentsDir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "should only have the custom agent")
}
