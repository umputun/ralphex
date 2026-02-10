package config

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_newAgentLoader(t *testing.T) {
	loader := newAgentLoader(defaultsFS)
	assert.NotNil(t, loader)
}

func TestAgentLoader_Load_FromAgentsDir(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "security.txt"), []byte("check for security issues"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "performance.txt"), []byte("check for performance issues"), 0o600))

	loader := newAgentLoader(defaultsFS)
	agents, err := loader.Load("", agentsDir)
	require.NoError(t, err)

	assert.Len(t, agents, 2)
	assert.Equal(t, "performance", agents[0].Name)
	assert.Equal(t, "check for performance issues", agents[0].Prompt)
	assert.Equal(t, "security", agents[1].Name)
	assert.Equal(t, "check for security issues", agents[1].Prompt)
}

func TestAgentLoader_Load_NoAgentsDir_FallsBackToEmbedded(t *testing.T) {
	tmpDir := t.TempDir()
	nonexistentAgentsDir := filepath.Join(tmpDir, "nonexistent", "agents")

	loader := newAgentLoader(defaultsFS)
	agents, err := loader.Load("", nonexistentAgentsDir)
	require.NoError(t, err)
	// when agents directory doesn't exist, should fall back to embedded agents
	assert.NotEmpty(t, agents, "should load embedded agents when directory doesn't exist")
	// verify we got the expected embedded agents
	names := make([]string, 0, len(agents))
	for _, a := range agents {
		names = append(names, a.Name)
	}
	assert.Contains(t, names, "quality", "should include quality agent from embedded")
	assert.Contains(t, names, "implementation", "should include implementation agent from embedded")
}

func TestAgentLoader_Load_EmptyAgentsDir(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	loader := newAgentLoader(defaultsFS)
	agents, err := loader.Load("", agentsDir)
	require.NoError(t, err)
	assert.Empty(t, agents)
}

func TestAgentLoader_Load_OnlyTxtFiles(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "valid.txt"), []byte("valid agent"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "invalid.md"), []byte("not an agent"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "another.json"), []byte("{}"), 0o600))

	loader := newAgentLoader(defaultsFS)
	agents, err := loader.Load("", agentsDir)
	require.NoError(t, err)

	assert.Len(t, agents, 1)
	assert.Equal(t, "valid", agents[0].Name)
	assert.Equal(t, "valid agent", agents[0].Prompt)
}

func TestAgentLoader_Load_SkipsEmptyFiles(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "valid.txt"), []byte("valid agent"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "empty.txt"), []byte(""), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "whitespace.txt"), []byte("   \n\t  "), 0o600))

	loader := newAgentLoader(defaultsFS)
	agents, err := loader.Load("", agentsDir)
	require.NoError(t, err)

	assert.Len(t, agents, 1)
	assert.Equal(t, "valid", agents[0].Name)
}

func TestAgentLoader_Load_TrimsWhitespace(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "agent.txt"), []byte("  prompt with spaces  \n\n"), 0o600))

	loader := newAgentLoader(defaultsFS)
	agents, err := loader.Load("", agentsDir)
	require.NoError(t, err)

	assert.Len(t, agents, 1)
	assert.Equal(t, "prompt with spaces", agents[0].Prompt)
}

func TestAgentLoader_Load_SkipsDirectories(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	require.NoError(t, os.MkdirAll(filepath.Join(agentsDir, "subdir.txt"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "valid.txt"), []byte("valid agent"), 0o600))

	loader := newAgentLoader(defaultsFS)
	agents, err := loader.Load("", agentsDir)
	require.NoError(t, err)

	assert.Len(t, agents, 1)
	assert.Equal(t, "valid", agents[0].Name)
}

func TestAgentLoader_Load_PreservesMultilinePrompt(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	prompt := "line one\nline two\nline three"
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "multi.txt"), []byte("  "+prompt+"  \n"), 0o600))

	loader := newAgentLoader(defaultsFS)
	agents, err := loader.Load("", agentsDir)
	require.NoError(t, err)

	assert.Len(t, agents, 1)
	assert.Equal(t, prompt, agents[0].Prompt)
}

func TestAgentLoader_Load_StripsCommentsFromAgentFiles(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	content := "# security agent - checks for vulnerabilities\ncheck for SQL injection\ncheck for XSS\n# end of agent"
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "security.txt"), []byte(content), 0o600))

	loader := newAgentLoader(defaultsFS)
	agents, err := loader.Load("", agentsDir)
	require.NoError(t, err)

	require.Len(t, agents, 1)
	assert.Equal(t, "security", agents[0].Name)
	assert.Equal(t, "check for SQL injection\ncheck for XSS", agents[0].Prompt)
}

func TestAgentLoader_Load_HandlesCRLFLineEndings(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	// content with CRLF line endings (Windows-style)
	content := "# comment line\r\ncheck for issues\r\n# another comment\r\nalso check this"
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "security.txt"), []byte(content), 0o600))

	loader := newAgentLoader(defaultsFS)
	agents, err := loader.Load("", agentsDir)
	require.NoError(t, err)

	require.Len(t, agents, 1)
	assert.Equal(t, "check for issues\nalso check this", agents[0].Prompt)
}

func TestAgentLoader_Load_LocalAgentsReplaceGlobal(t *testing.T) {
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

	loader := newAgentLoader(defaultsFS)
	agents, err := loader.Load(localDir, globalDir)
	require.NoError(t, err)

	// only local agents should be used (replace behavior)
	assert.Len(t, agents, 1)
	assert.Equal(t, "custom", agents[0].Name)
	assert.Equal(t, "local custom agent", agents[0].Prompt)
}

func TestAgentLoader_Load_LocalAgentsEmptyFallsBackToGlobal(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global", "agents")
	localDir := filepath.Join(tmpDir, "local", "agents")
	require.NoError(t, os.MkdirAll(globalDir, 0o700))
	require.NoError(t, os.MkdirAll(localDir, 0o700)) // empty local agents dir

	// global agents
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "security.txt"), []byte("global security"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "performance.txt"), []byte("global performance"), 0o600))

	loader := newAgentLoader(defaultsFS)
	agents, err := loader.Load(localDir, globalDir)
	require.NoError(t, err)

	// global agents should be used since local agents dir is empty
	assert.Len(t, agents, 2)
	assert.Equal(t, "performance", agents[0].Name)
	assert.Equal(t, "security", agents[1].Name)
}

func TestAgentLoader_Load_NoLocalAgentsDirFallsBackToGlobal(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global", "agents")
	localDir := filepath.Join(tmpDir, "nonexistent") // doesn't exist
	require.NoError(t, os.MkdirAll(globalDir, 0o700))

	// global agents
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "security.txt"), []byte("global security"), 0o600))

	loader := newAgentLoader(defaultsFS)
	agents, err := loader.Load(localDir, globalDir)
	require.NoError(t, err)

	// global agents should be used since no local agents dir
	assert.Len(t, agents, 1)
	assert.Equal(t, "security", agents[0].Name)
}

func TestAgentLoader_Load_LocalAgentsMultipleFiles(t *testing.T) {
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

	loader := newAgentLoader(defaultsFS)
	agents, err := loader.Load(localDir, globalDir)
	require.NoError(t, err)

	// only local agents should be used (sorted alphabetically)
	assert.Len(t, agents, 3)
	assert.Equal(t, "alpha", agents[0].Name)
	assert.Equal(t, "beta", agents[1].Name)
	assert.Equal(t, "gamma", agents[2].Name)
}

func TestAgentLoader_dirHasAgentFiles(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	al := newAgentLoader(defaultsFS)

	// non-existent dir
	has, err := al.dirHasAgentFiles(filepath.Join(tmpDir, "nonexistent"))
	require.NoError(t, err)
	assert.False(t, has)

	// empty dir
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))
	has, err = al.dirHasAgentFiles(agentsDir)
	require.NoError(t, err)
	assert.False(t, has)

	// dir with non-.txt files only
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "readme.md"), []byte("readme"), 0o600))
	has, err = al.dirHasAgentFiles(agentsDir)
	require.NoError(t, err)
	assert.False(t, has)

	// dir with .txt file
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "agent.txt"), []byte("agent"), 0o600))
	has, err = al.dirHasAgentFiles(agentsDir)
	require.NoError(t, err)
	assert.True(t, has)
}

func TestAgentLoader_loadFromDir(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "alpha.txt"), []byte("alpha prompt"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "beta.txt"), []byte("beta prompt"), 0o600))

	al := newAgentLoader(defaultsFS)
	agents, err := al.loadFromDir(agentsDir)
	require.NoError(t, err)

	assert.Len(t, agents, 2)
	assert.Equal(t, "alpha", agents[0].Name)
	assert.Equal(t, "alpha prompt", agents[0].Prompt)
	assert.Equal(t, "beta", agents[1].Name)
	assert.Equal(t, "beta prompt", agents[1].Prompt)
}

func TestAgentLoader_loadFileWithFallback_StripsComments(t *testing.T) {
	tmpDir := t.TempDir()
	agentFile := filepath.Join(tmpDir, "agent.txt")
	content := "# description of agent\ncheck for security issues\n# additional notes"
	require.NoError(t, os.WriteFile(agentFile, []byte(content), 0o600))

	al := newAgentLoader(defaultsFS)
	result, err := al.loadFileWithFallback(agentFile, "agent.txt")
	require.NoError(t, err)
	assert.Equal(t, "check for security issues", result)
}

func TestAgentLoader_loadFileWithFallback_FallsBackToEmbedded(t *testing.T) {
	tmpDir := t.TempDir()
	agentFile := filepath.Join(tmpDir, "quality.txt")
	// file with only comments - should fall back to embedded
	content := "# all comments\n# no actual content"
	require.NoError(t, os.WriteFile(agentFile, []byte(content), 0o600))

	al := newAgentLoader(defaultsFS)
	result, err := al.loadFileWithFallback(agentFile, "quality.txt")
	require.NoError(t, err)
	// should contain content from embedded quality.txt
	assert.Contains(t, result, "security")
}

func TestAgentLoader_dirHasAgentFiles_PermissionDenied(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	// remove read permission
	require.NoError(t, os.Chmod(agentsDir, 0o000))
	t.Cleanup(func() { _ = os.Chmod(agentsDir, 0o700) }) //nolint:gosec // test cleanup

	al := newAgentLoader(defaultsFS)
	_, err := al.dirHasAgentFiles(agentsDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read agents directory")
}

func TestAgentLoader_loadFromDir_PermissionDenied(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	// remove read permission
	require.NoError(t, os.Chmod(agentsDir, 0o000))
	t.Cleanup(func() { _ = os.Chmod(agentsDir, 0o700) }) //nolint:gosec // test cleanup

	al := newAgentLoader(defaultsFS)
	_, err := al.loadFromDir(agentsDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read agents directory")
}

func TestAgentLoader_loadFileWithFallback_PermissionDenied(t *testing.T) {
	tmpDir := t.TempDir()
	agentFile := filepath.Join(tmpDir, "agent.txt")
	require.NoError(t, os.WriteFile(agentFile, []byte("content"), 0o600))

	// remove read permission
	require.NoError(t, os.Chmod(agentFile, 0o000))
	t.Cleanup(func() { _ = os.Chmod(agentFile, 0o600) })

	al := newAgentLoader(defaultsFS)
	_, err := al.loadFileWithFallback(agentFile, "agent.txt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read agent file")
}

func TestAgentLoader_loadAllFromEmbedFS(t *testing.T) {
	al := newAgentLoader(defaultsFS)
	agents, err := al.loadAllFromEmbedFS()
	require.NoError(t, err)

	// should load all embedded agents
	assert.NotEmpty(t, agents, "should have embedded agents")
	assert.GreaterOrEqual(t, len(agents), 5, "should have at least 5 embedded agents")

	// verify agents are sorted
	for i := 1; i < len(agents); i++ {
		assert.Less(t, agents[i-1].Name, agents[i].Name, "agents should be sorted alphabetically")
	}

	// verify known embedded agents are present
	names := make([]string, 0, len(agents))
	for _, a := range agents {
		names = append(names, a.Name)
	}
	assert.Contains(t, names, "documentation")
	assert.Contains(t, names, "implementation")
	assert.Contains(t, names, "quality")
	assert.Contains(t, names, "simplification")
	assert.Contains(t, names, "testing")
}

func TestAgentLoader_loadFromDir_NonexistentFallsBackToEmbedded(t *testing.T) {
	tmpDir := t.TempDir()
	nonexistentDir := filepath.Join(tmpDir, "nonexistent", "agents")

	al := newAgentLoader(defaultsFS)
	agents, err := al.loadFromDir(nonexistentDir)
	require.NoError(t, err)

	// should fall back to embedded agents
	assert.NotEmpty(t, agents, "should load embedded agents when directory doesn't exist")
	names := make([]string, 0, len(agents))
	for _, a := range agents {
		names = append(names, a.Name)
	}
	assert.Contains(t, names, "quality", "should include quality agent from embedded")
}

func TestAgentLoader_Load_WarnsOnInvalidModel(t *testing.T) {
	dir := t.TempDir()
	agentsDir := filepath.Join(dir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o750))

	content := "---\nmodel: gpt-5\n---\nReview code."
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "bad.txt"), []byte(content), 0o600))

	// capture log output
	var buf bytes.Buffer
	origOut := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(origOut) })

	loader := newAgentLoader(defaultsFS)
	agents, err := loader.Load("", agentsDir)
	require.NoError(t, err)

	output := buf.String()

	require.Len(t, agents, 1)
	assert.Empty(t, agents[0].Model, "invalid model should be dropped")
	assert.Equal(t, "Review code.", agents[0].Prompt)
	assert.Contains(t, output, `[WARN] agent bad: unknown model "gpt-5"`)
}

func TestAgentLoader_Load_FrontmatterOnlyFallsBackToEmbedded(t *testing.T) {
	dir := t.TempDir()
	agentsDir := filepath.Join(dir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o750))

	// quality.txt with only frontmatter, no body — should fall back to embedded default
	content := "---\nmodel: haiku\n---"
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "quality.txt"), []byte(content), 0o600))

	loader := newAgentLoader(defaultsFS)
	agents, err := loader.Load("", agentsDir)
	require.NoError(t, err)
	require.Len(t, agents, 1)
	assert.Equal(t, "quality", agents[0].Name)
	assert.Contains(t, agents[0].Prompt, "security", "should use embedded quality body")
	assert.Empty(t, agents[0].Model, "frontmatter options should be dropped")
	assert.Empty(t, agents[0].AgentType, "frontmatter options should be dropped")
}

func TestAgentLoader_Load_FrontmatterAndCommentsOnlyFallsBackToEmbedded(t *testing.T) {
	dir := t.TempDir()
	agentsDir := filepath.Join(dir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o750))

	// quality.txt with frontmatter + commented body — should fall back to embedded default
	content := "---\nmodel: haiku\n---\n# this is a comment\n# another comment"
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "quality.txt"), []byte(content), 0o600))

	loader := newAgentLoader(defaultsFS)
	agents, err := loader.Load("", agentsDir)
	require.NoError(t, err)
	require.Len(t, agents, 1)
	assert.Equal(t, "quality", agents[0].Name)
	assert.Contains(t, agents[0].Prompt, "security", "should use embedded quality body")
	assert.Empty(t, agents[0].Model, "frontmatter options should be dropped")
}

func TestAgentLoader_Load_ParsesOptions(t *testing.T) {
	dir := t.TempDir()
	agentsDir := filepath.Join(dir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o750))

	content := "---\nmodel: haiku\nagent: code-reviewer\n---\nReview code for issues."
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "quality.txt"), []byte(content), 0o600))

	loader := newAgentLoader(defaultsFS)
	agents, err := loader.Load("", agentsDir)
	require.NoError(t, err)
	require.Len(t, agents, 1)
	assert.Equal(t, "quality", agents[0].Name)
	assert.Equal(t, "Review code for issues.", agents[0].Prompt)
	assert.Equal(t, "haiku", agents[0].Model)
	assert.Equal(t, "code-reviewer", agents[0].AgentType)
}
