package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_newDefaultsInstaller(t *testing.T) {
	installer := newDefaultsInstaller(DefaultsFS())
	assert.NotNil(t, installer)
}

func TestDefaultsInstaller_Install_CreatesConfigDir(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")

	_, err := os.Stat(configDir)
	require.True(t, os.IsNotExist(err))

	installer := newDefaultsInstaller(DefaultsFS())
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

	installer := newDefaultsInstaller(DefaultsFS())
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

	installer := newDefaultsInstaller(DefaultsFS())
	require.NoError(t, installer.Install(configDir))

	data, err := os.ReadFile(filepath.Join(configDir, "config")) //nolint:gosec // test
	require.NoError(t, err)
	assert.Equal(t, existingContent, string(data))
}

func TestDefaultsInstaller_Install_CreatesPromptsDir(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")

	installer := newDefaultsInstaller(DefaultsFS())
	require.NoError(t, installer.Install(configDir))

	info, err := os.Stat(filepath.Join(configDir, "prompts"))
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestDefaultsInstaller_Install_CreatesAgentsDir(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "ralphex")

	installer := newDefaultsInstaller(DefaultsFS())
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

	installer := newDefaultsInstaller(DefaultsFS())
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

			installer := newDefaultsInstaller(DefaultsFS())
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

	installer := newDefaultsInstaller(DefaultsFS())
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

	installer := newDefaultsInstaller(DefaultsFS())
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

	installer := &defaultsInstaller{embedFS: DefaultsFS()}
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

	installer := &defaultsInstaller{embedFS: DefaultsFS()}
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

	installer := &defaultsInstaller{embedFS: DefaultsFS()}
	require.NoError(t, installer.installDefaultFiles(promptsDir, "defaults/prompts", "prompt"))

	expectedPrompts := []string{"task.txt", "review_first.txt", "review_second.txt", "codex.txt"}
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

	installer := &defaultsInstaller{embedFS: DefaultsFS()}
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

	installer := newDefaultsInstaller(DefaultsFS())
	require.NoError(t, installer.Install(configDir))

	promptsDir := filepath.Join(configDir, "prompts")
	expectedPrompts := []string{"task.txt", "review_first.txt", "review_second.txt", "codex.txt"}

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

	installer := newDefaultsInstaller(DefaultsFS())
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
	installer := newDefaultsInstaller(DefaultsFS())
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

	installer := newDefaultsInstaller(DefaultsFS())
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

	installer := &defaultsInstaller{embedFS: DefaultsFS()}
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

	installer := &defaultsInstaller{embedFS: DefaultsFS()}
	err := installer.installDefaultFiles(destDir, "defaults/prompts", "prompt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "write prompt file")
}
