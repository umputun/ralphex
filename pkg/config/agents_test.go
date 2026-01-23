package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_loadAgents_FromAgentsDir(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "security.txt"), []byte("check for security issues"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "performance.txt"), []byte("check for performance issues"), 0o600))

	agents, err := loadAgents("", agentsDir)
	require.NoError(t, err)

	assert.Len(t, agents, 2)
	assert.Equal(t, "performance", agents[0].Name)
	assert.Equal(t, "check for performance issues", agents[0].Prompt)
	assert.Equal(t, "security", agents[1].Name)
	assert.Equal(t, "check for security issues", agents[1].Prompt)
}

func Test_loadAgents_NoAgentsDir(t *testing.T) {
	tmpDir := t.TempDir()

	agents, err := loadAgents("", tmpDir)
	require.NoError(t, err)
	assert.Empty(t, agents)
}

func Test_loadAgents_EmptyAgentsDir(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	agents, err := loadAgents("", agentsDir)
	require.NoError(t, err)
	assert.Empty(t, agents)
}

func Test_loadAgents_OnlyTxtFiles(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "valid.txt"), []byte("valid agent"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "invalid.md"), []byte("not an agent"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "another.json"), []byte("{}"), 0o600))

	agents, err := loadAgents("", agentsDir)
	require.NoError(t, err)

	assert.Len(t, agents, 1)
	assert.Equal(t, "valid", agents[0].Name)
	assert.Equal(t, "valid agent", agents[0].Prompt)
}

func Test_loadAgents_SkipsEmptyFiles(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "valid.txt"), []byte("valid agent"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "empty.txt"), []byte(""), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "whitespace.txt"), []byte("   \n\t  "), 0o600))

	agents, err := loadAgents("", agentsDir)
	require.NoError(t, err)

	assert.Len(t, agents, 1)
	assert.Equal(t, "valid", agents[0].Name)
}

func Test_loadAgents_TrimsWhitespace(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "agent.txt"), []byte("  prompt with spaces  \n\n"), 0o600))

	agents, err := loadAgents("", agentsDir)
	require.NoError(t, err)

	assert.Len(t, agents, 1)
	assert.Equal(t, "prompt with spaces", agents[0].Prompt)
}

func Test_loadAgents_SkipsDirectories(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	require.NoError(t, os.MkdirAll(filepath.Join(agentsDir, "subdir.txt"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "valid.txt"), []byte("valid agent"), 0o600))

	agents, err := loadAgents("", agentsDir)
	require.NoError(t, err)

	assert.Len(t, agents, 1)
	assert.Equal(t, "valid", agents[0].Name)
}

func Test_loadAgents_PreservesMultilinePrompt(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	prompt := "line one\nline two\nline three"
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "multi.txt"), []byte("  "+prompt+"  \n"), 0o600))

	agents, err := loadAgents("", agentsDir)
	require.NoError(t, err)

	assert.Len(t, agents, 1)
	assert.Equal(t, prompt, agents[0].Prompt)
}

func Test_loadAgents_StripsCommentsFromAgentFiles(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	content := "# security agent - checks for vulnerabilities\ncheck for SQL injection\ncheck for XSS\n# end of agent"
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "security.txt"), []byte(content), 0o600))

	agents, err := loadAgents("", agentsDir)
	require.NoError(t, err)

	require.Len(t, agents, 1)
	assert.Equal(t, "security", agents[0].Name)
	assert.Equal(t, "check for SQL injection\ncheck for XSS", agents[0].Prompt)
}

func Test_loadAgents_LocalAgentsReplaceGlobal(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global", "agents")
	localDir := filepath.Join(tmpDir, "local", "agents")
	require.NoError(t, os.MkdirAll(globalDir, 0o700))
	require.NoError(t, os.MkdirAll(localDir, 0o700))

	// global agents
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "security.txt"), []byte("global security"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "performance.txt"), []byte("global performance"), 0o600))

	// local agents (completely different set)
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "custom.txt"), []byte("local custom agent"), 0o600))

	agents, err := loadAgents(localDir, globalDir)
	require.NoError(t, err)

	// only local agents should be used (replace behavior)
	assert.Len(t, agents, 1)
	assert.Equal(t, "custom", agents[0].Name)
	assert.Equal(t, "local custom agent", agents[0].Prompt)
}

func Test_loadAgents_LocalAgentsEmptyFallsBackToGlobal(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global", "agents")
	localDir := filepath.Join(tmpDir, "local", "agents")
	require.NoError(t, os.MkdirAll(globalDir, 0o700))
	require.NoError(t, os.MkdirAll(localDir, 0o700)) // empty local agents dir

	// global agents
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "security.txt"), []byte("global security"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "performance.txt"), []byte("global performance"), 0o600))

	agents, err := loadAgents(localDir, globalDir)
	require.NoError(t, err)

	// global agents should be used since local agents dir is empty
	assert.Len(t, agents, 2)
	assert.Equal(t, "performance", agents[0].Name)
	assert.Equal(t, "security", agents[1].Name)
}

func Test_loadAgents_NoLocalAgentsDirFallsBackToGlobal(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global", "agents")
	localDir := filepath.Join(tmpDir, "nonexistent") // doesn't exist
	require.NoError(t, os.MkdirAll(globalDir, 0o700))

	// global agents
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "security.txt"), []byte("global security"), 0o600))

	agents, err := loadAgents(localDir, globalDir)
	require.NoError(t, err)

	// global agents should be used since no local agents dir
	assert.Len(t, agents, 1)
	assert.Equal(t, "security", agents[0].Name)
}

func Test_loadAgents_LocalAgentsMultipleFiles(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global", "agents")
	localDir := filepath.Join(tmpDir, "local", "agents")
	require.NoError(t, os.MkdirAll(globalDir, 0o700))
	require.NoError(t, os.MkdirAll(localDir, 0o700))

	// global agents (should be ignored)
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "global.txt"), []byte("global agent"), 0o600))

	// multiple local agents
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "alpha.txt"), []byte("alpha agent"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "beta.txt"), []byte("beta agent"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "gamma.txt"), []byte("gamma agent"), 0o600))

	agents, err := loadAgents(localDir, globalDir)
	require.NoError(t, err)

	// only local agents should be used (sorted alphabetically)
	assert.Len(t, agents, 3)
	assert.Equal(t, "alpha", agents[0].Name)
	assert.Equal(t, "beta", agents[1].Name)
	assert.Equal(t, "gamma", agents[2].Name)
}

func Test_dirHasAgentFiles(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")

	// non-existent dir
	has, err := dirHasAgentFiles(filepath.Join(tmpDir, "nonexistent"))
	require.NoError(t, err)
	assert.False(t, has)

	// empty dir
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))
	has, err = dirHasAgentFiles(agentsDir)
	require.NoError(t, err)
	assert.False(t, has)

	// dir with non-.txt files only
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "readme.md"), []byte("readme"), 0o600))
	has, err = dirHasAgentFiles(agentsDir)
	require.NoError(t, err)
	assert.False(t, has)

	// dir with .txt file
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "agent.txt"), []byte("agent"), 0o600))
	has, err = dirHasAgentFiles(agentsDir)
	require.NoError(t, err)
	assert.True(t, has)
}

func Test_loadAgentsFromDir(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "alpha.txt"), []byte("alpha prompt"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "beta.txt"), []byte("beta prompt"), 0o600))

	agents, err := loadAgentsFromDir(agentsDir)
	require.NoError(t, err)

	assert.Len(t, agents, 2)
	assert.Equal(t, "alpha", agents[0].Name)
	assert.Equal(t, "alpha prompt", agents[0].Prompt)
	assert.Equal(t, "beta", agents[1].Name)
	assert.Equal(t, "beta prompt", agents[1].Prompt)
}

func Test_loadAgentFile_StripsComments(t *testing.T) {
	tmpDir := t.TempDir()
	agentFile := filepath.Join(tmpDir, "agent.txt")
	content := "# description of agent\ncheck for security issues\n# additional notes"
	require.NoError(t, os.WriteFile(agentFile, []byte(content), 0o600))

	result, err := loadAgentFile(agentFile)
	require.NoError(t, err)
	assert.Equal(t, "check for security issues", result)
}

func Test_dirHasAgentFiles_PermissionDenied(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	// remove read permission
	require.NoError(t, os.Chmod(agentsDir, 0o000))
	t.Cleanup(func() { _ = os.Chmod(agentsDir, 0o700) }) //nolint:gosec // test cleanup

	_, err := dirHasAgentFiles(agentsDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read agents directory")
}

func Test_loadAgentsFromDir_PermissionDenied(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	// remove read permission
	require.NoError(t, os.Chmod(agentsDir, 0o000))
	t.Cleanup(func() { _ = os.Chmod(agentsDir, 0o700) }) //nolint:gosec // test cleanup

	_, err := loadAgentsFromDir(agentsDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read agents directory")
}

func Test_loadAgentFile_PermissionDenied(t *testing.T) {
	tmpDir := t.TempDir()
	agentFile := filepath.Join(tmpDir, "agent.txt")
	require.NoError(t, os.WriteFile(agentFile, []byte("content"), 0o600))

	// remove read permission
	require.NoError(t, os.Chmod(agentFile, 0o000))
	t.Cleanup(func() { _ = os.Chmod(agentFile, 0o600) })

	_, err := loadAgentFile(agentFile)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read agent file")
}
