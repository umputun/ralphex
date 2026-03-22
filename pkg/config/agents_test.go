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

// embeddedAgentNames are the 5 default agents shipped in defaults/agents/
var embeddedAgentNames = []string{"documentation", "implementation", "quality", "simplification", "testing"}

// findAgent returns agent by name from the list, or nil if not found.
func findAgent(agents []CustomAgent, name string) *CustomAgent {
	for i := range agents {
		if agents[i].Name == name {
			return &agents[i]
		}
	}
	return nil
}

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

	// custom agents + 5 embedded defaults = 7 total
	assert.Len(t, agents, 7)

	perf := findAgent(agents, "performance")
	require.NotNil(t, perf)
	assert.Equal(t, "check for performance issues", perf.Prompt)

	sec := findAgent(agents, "security")
	require.NotNil(t, sec)
	assert.Equal(t, "check for security issues", sec.Prompt)

	// embedded defaults are present
	for _, name := range embeddedAgentNames {
		assert.NotNil(t, findAgent(agents, name), "embedded agent %s should be present", name)
	}
}

func TestAgentLoader_Load_NoAgentsDir_FallsBackToEmbedded(t *testing.T) {
	tmpDir := t.TempDir()
	nonexistentAgentsDir := filepath.Join(tmpDir, "nonexistent", "agents")

	loader := newAgentLoader(defaultsFS)
	agents, err := loader.Load("", nonexistentAgentsDir)
	require.NoError(t, err)
	// when agents directory doesn't exist, should fall back to embedded agents
	assert.NotEmpty(t, agents, "should load embedded agents when directory doesn't exist")
	assert.NotNil(t, findAgent(agents, "quality"), "should include quality agent from embedded")
	assert.NotNil(t, findAgent(agents, "implementation"), "should include implementation agent from embedded")
}

func TestAgentLoader_Load_EmptyAgentsDir(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	loader := newAgentLoader(defaultsFS)
	agents, err := loader.Load("", agentsDir)
	require.NoError(t, err)
	// empty dir returns embedded defaults (per-file fallback behavior)
	assert.Len(t, agents, 5, "empty dir should return all 5 embedded defaults")
	for _, name := range embeddedAgentNames {
		assert.NotNil(t, findAgent(agents, name), "embedded agent %s should be present", name)
	}
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

	// valid.txt + 5 embedded = 6
	assert.Len(t, agents, 6)
	v := findAgent(agents, "valid")
	require.NotNil(t, v)
	assert.Equal(t, "valid agent", v.Prompt)
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

	// valid.txt + 5 embedded = 6 (empty/whitespace custom agents skipped, no embedded match)
	assert.Len(t, agents, 6)
	v := findAgent(agents, "valid")
	require.NotNil(t, v)
	assert.Equal(t, "valid agent", v.Prompt)
	assert.Nil(t, findAgent(agents, "empty"), "empty custom agent should be skipped")
	assert.Nil(t, findAgent(agents, "whitespace"), "whitespace-only custom agent should be skipped")
}

func TestAgentLoader_Load_TrimsWhitespace(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "agent.txt"), []byte("  prompt with spaces  \n\n"), 0o600))

	loader := newAgentLoader(defaultsFS)
	agents, err := loader.Load("", agentsDir)
	require.NoError(t, err)

	a := findAgent(agents, "agent")
	require.NotNil(t, a)
	assert.Equal(t, "prompt with spaces", a.Prompt)
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

	// valid.txt + 5 embedded = 6 (subdir skipped)
	assert.Len(t, agents, 6)
	v := findAgent(agents, "valid")
	require.NotNil(t, v)
	assert.Equal(t, "valid agent", v.Prompt)
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

	m := findAgent(agents, "multi")
	require.NotNil(t, m)
	assert.Equal(t, prompt, m.Prompt)
}

func TestAgentLoader_Load_PreservesAllContent(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	content := "# security agent - checks for vulnerabilities\ncheck for SQL injection\ncheck for XSS\n# end of agent"
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "security.txt"), []byte(content), 0o600))

	loader := newAgentLoader(defaultsFS)
	agents, err := loader.Load("", agentsDir)
	require.NoError(t, err)

	sec := findAgent(agents, "security")
	require.NotNil(t, sec)
	assert.Equal(t, "# security agent - checks for vulnerabilities\ncheck for SQL injection\ncheck for XSS\n# end of agent", sec.Prompt)
}

func TestAgentLoader_Load_HandlesCRLFLineEndings(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	// content with CRLF line endings (Windows-style), normalized to LF
	content := "# comment line\r\ncheck for issues\r\n# another comment\r\nalso check this"
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "security.txt"), []byte(content), 0o600))

	loader := newAgentLoader(defaultsFS)
	agents, err := loader.Load("", agentsDir)
	require.NoError(t, err)

	sec := findAgent(agents, "security")
	require.NotNil(t, sec)
	assert.Equal(t, "# comment line\ncheck for issues\n# another comment\nalso check this", sec.Prompt)
}

func TestAgentLoader_Load_LocalOverridesGlobalPerFile(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global", "agents")
	localDir := filepath.Join(tmpDir, "local", "agents")
	require.NoError(t, os.MkdirAll(globalDir, 0o700))
	require.NoError(t, os.MkdirAll(localDir, 0o700))

	// global agents
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "security.txt"), []byte("global security"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "performance.txt"), []byte("global performance"), 0o600))

	// local agent: custom.txt (new) — does NOT replace global set
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "custom.txt"), []byte("local custom agent"), 0o600))

	loader := newAgentLoader(defaultsFS)
	agents, err := loader.Load(localDir, globalDir)
	require.NoError(t, err)

	// per-file merge: custom (local) + security (global) + performance (global) + 5 embedded = 8
	assert.Len(t, agents, 8)

	custom := findAgent(agents, "custom")
	require.NotNil(t, custom)
	assert.Equal(t, "local custom agent", custom.Prompt)

	sec := findAgent(agents, "security")
	require.NotNil(t, sec)
	assert.Equal(t, "global security", sec.Prompt)

	perf := findAgent(agents, "performance")
	require.NotNil(t, perf)
	assert.Equal(t, "global performance", perf.Prompt)
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

	// global agents + 5 embedded = 7
	assert.Len(t, agents, 7)

	perf := findAgent(agents, "performance")
	require.NotNil(t, perf)
	assert.Equal(t, "global performance", perf.Prompt)

	sec := findAgent(agents, "security")
	require.NotNil(t, sec)
	assert.Equal(t, "global security", sec.Prompt)
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

	// security (global) + 5 embedded = 6
	assert.Len(t, agents, 6)
	sec := findAgent(agents, "security")
	require.NotNil(t, sec)
	assert.Equal(t, "global security", sec.Prompt)
}

func TestAgentLoader_Load_LocalAgentsMultipleFiles(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global", "agents")
	localDir := filepath.Join(tmpDir, "local", "agents")
	require.NoError(t, os.MkdirAll(globalDir, 0o700))
	require.NoError(t, os.MkdirAll(localDir, 0o700))

	// global agent
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "global.txt"), []byte("global agent"), 0o600))

	// multiple local agents
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "alpha.txt"), []byte("alpha agent"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "beta.txt"), []byte("beta agent"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "gamma.txt"), []byte("gamma agent"), 0o600))

	loader := newAgentLoader(defaultsFS)
	agents, err := loader.Load(localDir, globalDir)
	require.NoError(t, err)

	// alpha, beta, gamma (local) + global (global) + 5 embedded = 9
	assert.Len(t, agents, 9)

	alpha := findAgent(agents, "alpha")
	require.NotNil(t, alpha)
	assert.Equal(t, "alpha agent", alpha.Prompt)

	beta := findAgent(agents, "beta")
	require.NotNil(t, beta)
	assert.Equal(t, "beta agent", beta.Prompt)

	gamma := findAgent(agents, "gamma")
	require.NotNil(t, gamma)
	assert.Equal(t, "gamma agent", gamma.Prompt)

	gl := findAgent(agents, "global")
	require.NotNil(t, gl)
	assert.Equal(t, "global agent", gl.Prompt)
}

func TestAgentLoader_collectDirFilenames(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	al := newAgentLoader(defaultsFS)

	// non-existent dir returns nil, no error
	names, err := al.collectDirFilenames(filepath.Join(tmpDir, "nonexistent"))
	require.NoError(t, err)
	assert.Nil(t, names)

	// empty dir returns nil
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))
	names, err = al.collectDirFilenames(agentsDir)
	require.NoError(t, err)
	assert.Nil(t, names)

	// dir with non-.txt files only
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "readme.md"), []byte("readme"), 0o600))
	names, err = al.collectDirFilenames(agentsDir)
	require.NoError(t, err)
	assert.Nil(t, names)

	// dir with .txt files
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "agent.txt"), []byte("agent"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(agentsDir, "subdir.txt"), 0o700)) // subdir skipped
	names, err = al.collectDirFilenames(agentsDir)
	require.NoError(t, err)
	assert.Equal(t, []string{"agent.txt"}, names)

	// empty string dir returns nil, no error
	names, err = al.collectDirFilenames("")
	require.NoError(t, err)
	assert.Nil(t, names)
}

func TestAgentLoader_collectEmbeddedFilenames(t *testing.T) {
	al := newAgentLoader(defaultsFS)
	names, err := al.collectEmbeddedFilenames()
	require.NoError(t, err)
	assert.Len(t, names, 5)
	assert.Contains(t, names, "documentation.txt")
	assert.Contains(t, names, "implementation.txt")
	assert.Contains(t, names, "quality.txt")
	assert.Contains(t, names, "simplification.txt")
	assert.Contains(t, names, "testing.txt")
}

func TestAgentLoader_loadAgentWithFallback_LocalFirst(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global")
	localDir := filepath.Join(tmpDir, "local")
	require.NoError(t, os.MkdirAll(globalDir, 0o700))
	require.NoError(t, os.MkdirAll(localDir, 0o700))

	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "agent.txt"), []byte("global version"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "agent.txt"), []byte("local version"), 0o600))

	al := newAgentLoader(defaultsFS)
	result, err := al.loadAgentWithFallback(localDir, globalDir, "agent.txt")
	require.NoError(t, err)
	assert.Equal(t, "local version", result)
}

func TestAgentLoader_loadAgentWithFallback_FallsToGlobal(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global")
	localDir := filepath.Join(tmpDir, "local")
	require.NoError(t, os.MkdirAll(globalDir, 0o700))
	require.NoError(t, os.MkdirAll(localDir, 0o700))

	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "agent.txt"), []byte("global version"), 0o600))
	// no local file

	al := newAgentLoader(defaultsFS)
	result, err := al.loadAgentWithFallback(localDir, globalDir, "agent.txt")
	require.NoError(t, err)
	assert.Equal(t, "global version", result)
}

func TestAgentLoader_loadAgentWithFallback_FallsToEmbedded(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global")
	localDir := filepath.Join(tmpDir, "local")
	require.NoError(t, os.MkdirAll(globalDir, 0o700))
	require.NoError(t, os.MkdirAll(localDir, 0o700))

	// no local or global file for quality.txt — should fall back to embedded
	al := newAgentLoader(defaultsFS)
	result, err := al.loadAgentWithFallback(localDir, globalDir, "quality.txt")
	require.NoError(t, err)
	assert.Contains(t, result, "security", "should get embedded quality agent content")
}

func TestAgentLoader_loadAgentWithFallback_CustomNotInEmbedded(t *testing.T) {
	tmpDir := t.TempDir()
	al := newAgentLoader(defaultsFS)

	// filename that doesn't exist anywhere — returns empty
	result, err := al.loadAgentWithFallback("", "", "nonexistent.txt")
	require.NoError(t, err)
	assert.Empty(t, result)

	// same with dirs that don't have the file
	globalDir := filepath.Join(tmpDir, "global")
	require.NoError(t, os.MkdirAll(globalDir, 0o700))
	result, err = al.loadAgentWithFallback("", globalDir, "nonexistent.txt")
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestAgentLoader_loadFileWithFallback_PreservesAllContent(t *testing.T) {
	tmpDir := t.TempDir()
	agentFile := filepath.Join(tmpDir, "agent.txt")
	content := "# description of agent\ncheck for security issues\n# additional notes"
	require.NoError(t, os.WriteFile(agentFile, []byte(content), 0o600))

	al := newAgentLoader(defaultsFS)
	result, err := al.loadFileWithFallback(agentFile, "agent.txt")
	require.NoError(t, err)
	assert.Equal(t, "# description of agent\ncheck for security issues\n# additional notes", result)
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

	bad := findAgent(agents, "bad")
	require.NotNil(t, bad)
	assert.Empty(t, bad.Model, "invalid model should be dropped")
	assert.Equal(t, "Review code.", bad.Prompt)
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

	q := findAgent(agents, "quality")
	require.NotNil(t, q)
	assert.Contains(t, q.Prompt, "security", "should use embedded quality body")
	assert.Empty(t, q.Model, "frontmatter options should be dropped")
	assert.Empty(t, q.AgentType, "frontmatter options should be dropped")
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

	q := findAgent(agents, "quality")
	require.NotNil(t, q)
	assert.Contains(t, q.Prompt, "security", "should use embedded quality body")
	assert.Empty(t, q.Model, "frontmatter options should be dropped")
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

	q := findAgent(agents, "quality")
	require.NotNil(t, q)
	assert.Equal(t, "Review code for issues.", q.Prompt)
	assert.Equal(t, "haiku", q.Model)
	assert.Equal(t, "code-reviewer", q.AgentType)
}

func TestAgentLoader_Load_ParsesOptionsWithSingleLeadingComment(t *testing.T) {
	dir := t.TempDir()
	agentsDir := filepath.Join(dir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o750))

	// single comment before frontmatter should not prevent frontmatter detection
	content := "# my custom agent\n---\nmodel: haiku\nagent: code-reviewer\n---\nReview code for issues."
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "quality.txt"), []byte(content), 0o600))

	loader := newAgentLoader(defaultsFS)
	agents, err := loader.Load("", agentsDir)
	require.NoError(t, err)

	q := findAgent(agents, "quality")
	require.NotNil(t, q)
	assert.Equal(t, "Review code for issues.", q.Prompt)
	assert.Equal(t, "haiku", q.Model)
	assert.Equal(t, "code-reviewer", q.AgentType)
}

func TestAgentLoader_Load_ParsesOptionsWithWhitespaceSeparator(t *testing.T) {
	dir := t.TempDir()
	agentsDir := filepath.Join(dir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o750))

	// whitespace-only line between comments and frontmatter should still work
	content := "# my agent\n# description\n   \n---\nmodel: sonnet\n---\nReview code."
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "quality.txt"), []byte(content), 0o600))

	loader := newAgentLoader(defaultsFS)
	agents, err := loader.Load("", agentsDir)
	require.NoError(t, err)

	q := findAgent(agents, "quality")
	require.NotNil(t, q)
	assert.Equal(t, "Review code.", q.Prompt)
	assert.Equal(t, "sonnet", q.Model)
}

func TestAgentLoader_Load_ParsesOptionsWithLeadingComments(t *testing.T) {
	dir := t.TempDir()
	agentsDir := filepath.Join(dir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o750))

	// comments before frontmatter should not prevent frontmatter detection
	content := "# my custom agent\n# description of what it does\n---\nmodel: haiku\nagent: code-reviewer\n---\nReview code for issues."
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "quality.txt"), []byte(content), 0o600))

	loader := newAgentLoader(defaultsFS)
	agents, err := loader.Load("", agentsDir)
	require.NoError(t, err)

	q := findAgent(agents, "quality")
	require.NotNil(t, q)
	assert.Equal(t, "Review code for issues.", q.Prompt)
	assert.Equal(t, "haiku", q.Model)
	assert.Equal(t, "code-reviewer", q.AgentType)
}

func TestAgentLoader_Load_LocalOverridesOneDefault(t *testing.T) {
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "local", "agents")
	globalDir := filepath.Join(tmpDir, "global", "agents")
	require.NoError(t, os.MkdirAll(localDir, 0o700))
	require.NoError(t, os.MkdirAll(globalDir, 0o700))

	// local overrides only quality.txt, other 4 embedded defaults should come through
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "quality.txt"), []byte("custom quality check"), 0o600))

	loader := newAgentLoader(defaultsFS)
	agents, err := loader.Load(localDir, globalDir)
	require.NoError(t, err)

	// 5 total: 1 local override + 4 other embedded defaults
	assert.Len(t, agents, 5)

	q := findAgent(agents, "quality")
	require.NotNil(t, q)
	assert.Equal(t, "custom quality check", q.Prompt, "local quality should override embedded")

	// other 4 embedded defaults are present
	for _, name := range []string{"documentation", "implementation", "simplification", "testing"} {
		assert.NotNil(t, findAgent(agents, name), "embedded agent %s should be present", name)
	}
}

func TestAgentLoader_Load_LocalAddsCustomAlongsideDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "local", "agents")
	globalDir := filepath.Join(tmpDir, "global", "agents")
	require.NoError(t, os.MkdirAll(localDir, 0o700))
	require.NoError(t, os.MkdirAll(globalDir, 0o700))

	// local adds a custom agent alongside the 5 embedded defaults
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "security.txt"), []byte("custom security check"), 0o600))

	loader := newAgentLoader(defaultsFS)
	agents, err := loader.Load(localDir, globalDir)
	require.NoError(t, err)

	// 6 total: 5 embedded + 1 custom
	assert.Len(t, agents, 6)

	sec := findAgent(agents, "security")
	require.NotNil(t, sec)
	assert.Equal(t, "custom security check", sec.Prompt)

	for _, name := range embeddedAgentNames {
		assert.NotNil(t, findAgent(agents, name), "embedded agent %s should be present", name)
	}
}

func TestAgentLoader_Load_LocalOverridesGlobalSameFile(t *testing.T) {
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "local", "agents")
	globalDir := filepath.Join(tmpDir, "global", "agents")
	require.NoError(t, os.MkdirAll(localDir, 0o700))
	require.NoError(t, os.MkdirAll(globalDir, 0o700))

	// same filename in both local and global — local wins
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "security.txt"), []byte("global security"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "security.txt"), []byte("local security"), 0o600))

	// global-only agent should also appear
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "performance.txt"), []byte("global performance"), 0o600))

	loader := newAgentLoader(defaultsFS)
	agents, err := loader.Load(localDir, globalDir)
	require.NoError(t, err)

	// security (local) + performance (global) + 5 embedded = 7
	assert.Len(t, agents, 7)

	sec := findAgent(agents, "security")
	require.NotNil(t, sec)
	assert.Equal(t, "local security", sec.Prompt, "local should override global for same filename")

	perf := findAgent(agents, "performance")
	require.NotNil(t, perf)
	assert.Equal(t, "global performance", perf.Prompt, "global-only agent should be included")
}

func TestAgentLoader_Load_ResultsSortedAlphabetically(t *testing.T) {
	tmpDir := t.TempDir()
	agentsDir := filepath.Join(tmpDir, "agents")
	require.NoError(t, os.MkdirAll(agentsDir, 0o700))

	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "zebra.txt"), []byte("z agent"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(agentsDir, "alpha.txt"), []byte("a agent"), 0o600))

	loader := newAgentLoader(defaultsFS)
	agents, err := loader.Load("", agentsDir)
	require.NoError(t, err)

	for i := 1; i < len(agents); i++ {
		assert.Less(t, agents[i-1].Name, agents[i].Name, "agents should be sorted alphabetically")
	}
}
