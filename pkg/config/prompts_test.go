package config

import (
	"embed"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_newPromptLoader(t *testing.T) {
	loader := newPromptLoader(defaultsFS)
	assert.NotNil(t, loader)
}

func TestPromptLoader_Load_FromUserDir(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "prompts")
	require.NoError(t, os.MkdirAll(globalDir, 0o700))

	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "task.txt"), []byte("custom task prompt"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "review_first.txt"), []byte("custom first review"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "review_second.txt"), []byte("custom second review"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "codex.txt"), []byte("custom codex prompt"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "make_plan.txt"), []byte("custom make plan prompt"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "finalize.txt"), []byte("custom finalize prompt"), 0o600))

	loader := newPromptLoader(defaultsFS)
	prompts, err := loader.Load("", globalDir)
	require.NoError(t, err)

	assert.Equal(t, "custom task prompt", prompts.Task)
	assert.Equal(t, "custom first review", prompts.ReviewFirst)
	assert.Equal(t, "custom second review", prompts.ReviewSecond)
	assert.Equal(t, "custom codex prompt", prompts.Codex)
	assert.Equal(t, "custom make plan prompt", prompts.MakePlan)
	assert.Equal(t, "custom finalize prompt", prompts.Finalize)
}

func TestPromptLoader_Load_PartialUserFiles(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "prompts")
	require.NoError(t, os.MkdirAll(globalDir, 0o700))

	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "task.txt"), []byte("user task prompt"), 0o600))

	loader := newPromptLoader(defaultsFS)
	prompts, err := loader.Load("", globalDir)
	require.NoError(t, err)

	assert.Equal(t, "user task prompt", prompts.Task)
	// other prompts should fall back to embedded
	assert.Contains(t, prompts.ReviewFirst, "{{GOAL}}")
}

func TestPromptLoader_Load_NoUserDir(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "nonexistent")

	loader := newPromptLoader(defaultsFS)
	prompts, err := loader.Load("", globalDir)
	require.NoError(t, err)

	// should fall back to embedded defaults
	assert.Contains(t, prompts.Task, "{{PLAN_FILE}}")
	assert.Contains(t, prompts.ReviewFirst, "{{GOAL}}")
	assert.Contains(t, prompts.MakePlan, "{{PLAN_DESCRIPTION}}")
}

func TestPromptLoader_Load_EmptyUserFile(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "prompts")
	require.NoError(t, os.MkdirAll(globalDir, 0o700))

	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "task.txt"), []byte(""), 0o600))

	loader := newPromptLoader(defaultsFS)
	prompts, err := loader.Load("", globalDir)
	require.NoError(t, err)

	// empty file should fall back to embedded
	assert.Contains(t, prompts.Task, "{{PLAN_FILE}}")
}

func TestPromptLoader_Load_LocalOverridesGlobal(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global", "prompts")
	localDir := filepath.Join(tmpDir, "local", "prompts")
	require.NoError(t, os.MkdirAll(globalDir, 0o700))
	require.NoError(t, os.MkdirAll(localDir, 0o700))

	// global prompts
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "task.txt"), []byte("global task prompt"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "review_first.txt"), []byte("global review first"), 0o600))

	// local prompt overrides task.txt only
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "task.txt"), []byte("local task prompt"), 0o600))

	loader := newPromptLoader(defaultsFS)
	prompts, err := loader.Load(localDir, globalDir)
	require.NoError(t, err)

	// local prompt used
	assert.Equal(t, "local task prompt", prompts.Task)

	// global prompt used for non-overridden file
	assert.Equal(t, "global review first", prompts.ReviewFirst)
}

func TestPromptLoader_Load_LocalFallbackToEmbedded(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global", "prompts")
	localDir := filepath.Join(tmpDir, "local", "prompts")
	require.NoError(t, os.MkdirAll(globalDir, 0o700))
	require.NoError(t, os.MkdirAll(localDir, 0o700))

	// local has only one prompt
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "task.txt"), []byte("local task"), 0o600))

	// global is empty - no prompts

	loader := newPromptLoader(defaultsFS)
	prompts, err := loader.Load(localDir, globalDir)
	require.NoError(t, err)

	// local prompt used
	assert.Equal(t, "local task", prompts.Task)

	// embedded defaults used for missing prompts (both local and global)
	assert.Contains(t, prompts.ReviewFirst, "{{GOAL}}")
	assert.Contains(t, prompts.ReviewSecond, "{{GOAL}}")
	assert.Contains(t, prompts.Codex, "{{CODEX_OUTPUT}}")
}

func TestPromptLoader_Load_PartialLocalPrompts(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global", "prompts")
	localDir := filepath.Join(tmpDir, "local", "prompts")
	require.NoError(t, os.MkdirAll(globalDir, 0o700))
	require.NoError(t, os.MkdirAll(localDir, 0o700))

	// global has two prompts
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "review_first.txt"), []byte("global review first"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "codex.txt"), []byte("global codex"), 0o600))

	// local has different two prompts
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "task.txt"), []byte("local task"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "review_second.txt"), []byte("local review second"), 0o600))

	loader := newPromptLoader(defaultsFS)
	prompts, err := loader.Load(localDir, globalDir)
	require.NoError(t, err)

	// local prompts used
	assert.Equal(t, "local task", prompts.Task)
	assert.Equal(t, "local review second", prompts.ReviewSecond)

	// global prompts used
	assert.Equal(t, "global review first", prompts.ReviewFirst)
	assert.Equal(t, "global codex", prompts.Codex)
}

func TestPromptLoader_Load_NoLocalPromptsDir(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global", "prompts")
	localDir := filepath.Join(tmpDir, "nonexistent") // doesn't exist
	require.NoError(t, os.MkdirAll(globalDir, 0o700))

	// global has prompts
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "task.txt"), []byte("global task"), 0o600))

	loader := newPromptLoader(defaultsFS)
	prompts, err := loader.Load(localDir, globalDir)
	require.NoError(t, err)

	// global prompt used since no local prompts dir
	assert.Equal(t, "global task", prompts.Task)
}

func TestPromptLoader_loadPromptWithLocalFallback_AllLevels(t *testing.T) {
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "local", "prompts")
	globalDir := filepath.Join(tmpDir, "global", "prompts")
	require.NoError(t, os.MkdirAll(localDir, 0o700))
	require.NoError(t, os.MkdirAll(globalDir, 0o700))

	pl := &promptLoader{embedFS: defaultsFS}

	// test local takes precedence over global
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "task.txt"), []byte("local"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "task.txt"), []byte("global"), 0o600))
	content, err := pl.loadPromptWithLocalFallback(localDir, globalDir, "task.txt")
	require.NoError(t, err)
	assert.Equal(t, "local", content)

	// test global used when local missing
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "codex.txt"), []byte("global codex"), 0o600))
	content, err = pl.loadPromptWithLocalFallback(localDir, globalDir, "codex.txt")
	require.NoError(t, err)
	assert.Equal(t, "global codex", content)

	// test embedded used when both local and global have empty files
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "review_first.txt"), []byte(""), 0o600))
	content, err = pl.loadPromptWithLocalFallback(localDir, globalDir, "review_first.txt")
	require.NoError(t, err)
	assert.Contains(t, content, "{{GOAL}}") // embedded default

	// test embedded used when neither local nor global has the file
	content, err = pl.loadPromptWithLocalFallback(localDir, globalDir, "review_second.txt")
	require.NoError(t, err)
	assert.Contains(t, content, "{{GOAL}}") // embedded default
}

func TestPromptLoader_loadPromptWithLocalFallback_EmptyLocalDir(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global", "prompts")
	require.NoError(t, os.MkdirAll(globalDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "task.txt"), []byte("global task"), 0o600))

	pl := &promptLoader{embedFS: defaultsFS}

	// empty localDir skips local lookup
	content, err := pl.loadPromptWithLocalFallback("", globalDir, "task.txt")
	require.NoError(t, err)
	assert.Equal(t, "global task", content)
}

func TestPromptLoader_loadPromptFile_Success(t *testing.T) {
	tmpDir := t.TempDir()
	promptFile := filepath.Join(tmpDir, "test.txt")
	require.NoError(t, os.WriteFile(promptFile, []byte("test content\nwith newline"), 0o600))

	pl := &promptLoader{embedFS: defaultsFS}
	content, err := pl.loadPromptFile(promptFile)
	require.NoError(t, err)
	assert.Equal(t, "test content\nwith newline", content)
}

func TestPromptLoader_loadPromptFile_NotExists(t *testing.T) {
	pl := &promptLoader{embedFS: defaultsFS}
	content, err := pl.loadPromptFile("/nonexistent/path/file.txt")
	require.NoError(t, err)
	assert.Empty(t, content)
}

func TestPromptLoader_loadPromptFile_WhitespaceHandling(t *testing.T) {
	tmpDir := t.TempDir()
	promptFile := filepath.Join(tmpDir, "test.txt")
	require.NoError(t, os.WriteFile(promptFile, []byte("  content with spaces  \n\n"), 0o600))

	pl := &promptLoader{embedFS: defaultsFS}
	content, err := pl.loadPromptFile(promptFile)
	require.NoError(t, err)
	assert.Equal(t, "content with spaces", content)
}

func TestPromptLoader_loadPromptFile_StripsComments(t *testing.T) {
	tmpDir := t.TempDir()
	promptFile := filepath.Join(tmpDir, "test.txt")
	content := "# this is a comment\nkeep this line\n  # indented comment\nalso keep this"
	require.NoError(t, os.WriteFile(promptFile, []byte(content), 0o600))

	pl := &promptLoader{embedFS: defaultsFS}
	result, err := pl.loadPromptFile(promptFile)
	require.NoError(t, err)
	assert.Equal(t, "keep this line\nalso keep this", result)
}

func TestPromptLoader_loadPromptFromEmbedFS(t *testing.T) {
	pl := &promptLoader{embedFS: defaultsFS}
	content, err := pl.loadPromptFromEmbedFS("defaults/config")
	require.NoError(t, err)
	assert.Contains(t, content, "claude_command")
}

func TestPromptLoader_loadPromptFromEmbedFS_NotFound(t *testing.T) {
	pl := &promptLoader{embedFS: defaultsFS}
	content, err := pl.loadPromptFromEmbedFS("nonexistent/file.txt")
	require.NoError(t, err)
	assert.Empty(t, content)
}

func TestPromptLoader_loadPromptFromEmbedFS_MockFS(t *testing.T) {
	var mockFS embed.FS
	pl := &promptLoader{embedFS: mockFS}
	content, err := pl.loadPromptFromEmbedFS("any/path")
	require.NoError(t, err)
	assert.Empty(t, content)
}

func TestPromptLoader_loadPromptWithFallback(t *testing.T) {
	tmpDir := t.TempDir()
	promptsDir := filepath.Join(tmpDir, "prompts")
	require.NoError(t, os.MkdirAll(promptsDir, 0o700))

	require.NoError(t, os.WriteFile(filepath.Join(promptsDir, "task.txt"), []byte("user prompt"), 0o600))

	pl := &promptLoader{embedFS: defaultsFS}
	content, err := pl.loadPromptWithFallback(filepath.Join(promptsDir, "task.txt"), "defaults/prompts/task.txt")
	require.NoError(t, err)
	assert.Equal(t, "user prompt", content)
}

func TestPromptLoader_loadPromptWithFallback_FallsBackToEmbed(t *testing.T) {
	pl := &promptLoader{embedFS: defaultsFS}
	content, err := pl.loadPromptWithFallback("/nonexistent/path.txt", "defaults/prompts/task.txt")
	require.NoError(t, err)
	assert.Contains(t, content, "{{PLAN_FILE}}")
	assert.Contains(t, content, "RALPHEX:ALL_TASKS_DONE")
}

func TestPromptLoader_loadPromptWithFallback_EmptyUserFileUsesDefault(t *testing.T) {
	tmpDir := t.TempDir()
	emptyFile := filepath.Join(tmpDir, "empty.txt")
	require.NoError(t, os.WriteFile(emptyFile, []byte(""), 0o600))

	pl := &promptLoader{embedFS: defaultsFS}
	content, err := pl.loadPromptWithFallback(emptyFile, "defaults/prompts/task.txt")
	require.NoError(t, err)
	assert.Contains(t, content, "{{PLAN_FILE}}")
	assert.Contains(t, content, "RALPHEX:ALL_TASKS_DONE")
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
		{name: "CRLF line endings", input: "# comment\r\nkeep this\r\nalso keep", expected: "keep this\nalso keep"},
		{name: "mixed line endings", input: "# comment\r\nkeep\n# another\nalso keep", expected: "keep\nalso keep"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := stripComments(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestPromptLoader_loadPromptFile_PermissionDenied(t *testing.T) {
	tmpDir := t.TempDir()
	promptFile := filepath.Join(tmpDir, "test.txt")
	require.NoError(t, os.WriteFile(promptFile, []byte("content"), 0o600))

	// remove read permission
	require.NoError(t, os.Chmod(promptFile, 0o000))
	t.Cleanup(func() { _ = os.Chmod(promptFile, 0o600) })

	pl := &promptLoader{embedFS: defaultsFS}
	_, err := pl.loadPromptFile(promptFile)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read prompt file")
}

func TestPromptLoader_Load_PromptWithOnlyComments(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "prompts")
	require.NoError(t, os.MkdirAll(globalDir, 0o700))

	// create prompt file with only comments
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "task.txt"), []byte("# comment only\n# another comment"), 0o600))

	loader := newPromptLoader(defaultsFS)
	prompts, err := loader.Load("", globalDir)
	require.NoError(t, err)

	// file with only comments should fall back to embedded default
	assert.Contains(t, prompts.Task, "{{PLAN_FILE}}")
}

func TestPromptLoader_Load_MakePlanPrompt(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "prompts")
	require.NoError(t, os.MkdirAll(globalDir, 0o700))

	// test custom make_plan prompt
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "make_plan.txt"), []byte("custom plan prompt with {{PLAN_DESCRIPTION}}"), 0o600))

	loader := newPromptLoader(defaultsFS)
	prompts, err := loader.Load("", globalDir)
	require.NoError(t, err)

	assert.Equal(t, "custom plan prompt with {{PLAN_DESCRIPTION}}", prompts.MakePlan)
}

func TestPromptLoader_Load_MakePlanPrompt_FallsBackToEmbedded(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "nonexistent")

	loader := newPromptLoader(defaultsFS)
	prompts, err := loader.Load("", globalDir)
	require.NoError(t, err)

	// should fall back to embedded make_plan prompt
	assert.Contains(t, prompts.MakePlan, "{{PLAN_DESCRIPTION}}")
	assert.Contains(t, prompts.MakePlan, "{{PROGRESS_FILE}}")
	assert.Contains(t, prompts.MakePlan, "RALPHEX:QUESTION")
	assert.Contains(t, prompts.MakePlan, "RALPHEX:PLAN_READY")
}

func TestPromptLoader_Load_MakePlanPrompt_LocalOverridesGlobal(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global", "prompts")
	localDir := filepath.Join(tmpDir, "local", "prompts")
	require.NoError(t, os.MkdirAll(globalDir, 0o700))
	require.NoError(t, os.MkdirAll(localDir, 0o700))

	// global make_plan prompt
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "make_plan.txt"), []byte("global make plan"), 0o600))
	// local make_plan prompt
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "make_plan.txt"), []byte("local make plan"), 0o600))

	loader := newPromptLoader(defaultsFS)
	prompts, err := loader.Load(localDir, globalDir)
	require.NoError(t, err)

	assert.Equal(t, "local make plan", prompts.MakePlan)
}

func TestPromptLoader_Load_AllCommentedPromptsFallbackToEmbedded(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "prompts")
	require.NoError(t, os.MkdirAll(globalDir, 0o700))

	// create all prompt files with only comments (simulates commented defaults)
	commentedContent := "# this is the default template\n# uncomment and customize below\n# actual prompt content"
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "task.txt"), []byte(commentedContent), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "review_first.txt"), []byte(commentedContent), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "review_second.txt"), []byte(commentedContent), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "codex.txt"), []byte(commentedContent), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "make_plan.txt"), []byte(commentedContent), 0o600))

	loader := newPromptLoader(defaultsFS)
	prompts, err := loader.Load("", globalDir)
	require.NoError(t, err)

	// all prompts should fall back to embedded defaults since files contain only comments
	assert.Contains(t, prompts.Task, "{{PLAN_FILE}}", "task prompt should fall back to embedded")
	assert.Contains(t, prompts.ReviewFirst, "{{GOAL}}", "review_first prompt should fall back to embedded")
	assert.Contains(t, prompts.ReviewSecond, "{{GOAL}}", "review_second prompt should fall back to embedded")
	assert.Contains(t, prompts.Codex, "{{CODEX_OUTPUT}}", "codex prompt should fall back to embedded")
	assert.Contains(t, prompts.MakePlan, "{{PLAN_DESCRIPTION}}", "make_plan prompt should fall back to embedded")
}

func TestPromptLoader_Load_MixedCommentedAndCustomPrompts(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "prompts")
	require.NoError(t, os.MkdirAll(globalDir, 0o700))

	commentedContent := "# default template - commented out\n# customize below"

	// some prompts are all-commented (should fall back to embedded)
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "task.txt"), []byte(commentedContent), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "codex.txt"), []byte(commentedContent), 0o600))

	// some prompts have custom content (should use custom)
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "review_first.txt"), []byte("custom review first prompt"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "review_second.txt"), []byte("# header comment\ncustom review second"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "make_plan.txt"), []byte("custom make plan"), 0o600))

	loader := newPromptLoader(defaultsFS)
	prompts, err := loader.Load("", globalDir)
	require.NoError(t, err)

	// all-commented prompts fall back to embedded
	assert.Contains(t, prompts.Task, "{{PLAN_FILE}}", "all-commented task should fall back")
	assert.Contains(t, prompts.Codex, "{{CODEX_OUTPUT}}", "all-commented codex should fall back")

	// custom prompts are used (comment stripping applied)
	assert.Equal(t, "custom review first prompt", prompts.ReviewFirst)
	assert.Equal(t, "custom review second", prompts.ReviewSecond)
	assert.Equal(t, "custom make plan", prompts.MakePlan)
}

func TestPromptLoader_Load_LocalAllCommentedFallsBackToGlobal(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global", "prompts")
	localDir := filepath.Join(tmpDir, "local", "prompts")
	require.NoError(t, os.MkdirAll(globalDir, 0o700))
	require.NoError(t, os.MkdirAll(localDir, 0o700))

	commentedContent := "# all commented\n# no actual content"

	// local has all-commented file
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "task.txt"), []byte(commentedContent), 0o600))

	// global has actual content
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "task.txt"), []byte("global task content"), 0o600))

	loader := newPromptLoader(defaultsFS)
	prompts, err := loader.Load(localDir, globalDir)
	require.NoError(t, err)

	// local all-commented should fall through to global
	assert.Equal(t, "global task content", prompts.Task)
}

func TestPromptLoader_Load_LocalAndGlobalAllCommentedFallsBackToEmbedded(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global", "prompts")
	localDir := filepath.Join(tmpDir, "local", "prompts")
	require.NoError(t, os.MkdirAll(globalDir, 0o700))
	require.NoError(t, os.MkdirAll(localDir, 0o700))

	commentedContent := "# all commented\n# no actual content"

	// both local and global have all-commented files
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "task.txt"), []byte(commentedContent), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "task.txt"), []byte(commentedContent), 0o600))

	loader := newPromptLoader(defaultsFS)
	prompts, err := loader.Load(localDir, globalDir)
	require.NoError(t, err)

	// both all-commented should fall back to embedded
	assert.Contains(t, prompts.Task, "{{PLAN_FILE}}", "should fall back to embedded when both local and global are all-commented")
}

func TestPromptLoader_Load_FinalizePrompt(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "prompts")
	require.NoError(t, os.MkdirAll(globalDir, 0o700))

	// test custom finalize prompt
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "finalize.txt"), []byte("custom finalize with {{DEFAULT_BRANCH}}"), 0o600))

	loader := newPromptLoader(defaultsFS)
	prompts, err := loader.Load("", globalDir)
	require.NoError(t, err)

	assert.Equal(t, "custom finalize with {{DEFAULT_BRANCH}}", prompts.Finalize)
}

func TestPromptLoader_Load_FinalizePrompt_LocalOverridesGlobal(t *testing.T) {
	tmpDir := t.TempDir()
	globalDir := filepath.Join(tmpDir, "global", "prompts")
	localDir := filepath.Join(tmpDir, "local", "prompts")
	require.NoError(t, os.MkdirAll(globalDir, 0o700))
	require.NoError(t, os.MkdirAll(localDir, 0o700))

	// global finalize prompt
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "finalize.txt"), []byte("global finalize"), 0o600))
	// local finalize prompt
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "finalize.txt"), []byte("local finalize"), 0o600))

	loader := newPromptLoader(defaultsFS)
	prompts, err := loader.Load(localDir, globalDir)
	require.NoError(t, err)

	assert.Equal(t, "local finalize", prompts.Finalize)
}
