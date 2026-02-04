package config

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Prompts holds all loaded prompt templates for different phases of execution.
// Each prompt can be customized by placing a .txt file in the prompts directory.
type Prompts struct {
	Task         string
	ReviewFirst  string
	ReviewSecond string
	Codex        string
	MakePlan     string
	Finalize     string
}

// promptLoader implements PromptLoader with embedded filesystem fallback.
type promptLoader struct {
	embedFS embed.FS
}

// newPromptLoader creates a new promptLoader with the given embedded filesystem.
func newPromptLoader(embedFS embed.FS) *promptLoader {
	return &promptLoader{embedFS: embedFS}
}

// Load loads all prompt files with fallback chain: local → global → embedded.
func (p *promptLoader) Load(localDir, globalDir string) (Prompts, error) {
	var prompts Prompts
	var err error

	prompts.Task, err = p.loadPromptWithLocalFallback(localDir, globalDir, taskPromptFile)
	if err != nil {
		return Prompts{}, fmt.Errorf("load task prompt: %w", err)
	}

	prompts.ReviewFirst, err = p.loadPromptWithLocalFallback(localDir, globalDir, reviewFirstPromptFile)
	if err != nil {
		return Prompts{}, fmt.Errorf("load review_first prompt: %w", err)
	}

	prompts.ReviewSecond, err = p.loadPromptWithLocalFallback(localDir, globalDir, reviewSecondPromptFile)
	if err != nil {
		return Prompts{}, fmt.Errorf("load review_second prompt: %w", err)
	}

	prompts.Codex, err = p.loadPromptWithLocalFallback(localDir, globalDir, codexPromptFile)
	if err != nil {
		return Prompts{}, fmt.Errorf("load codex prompt: %w", err)
	}

	prompts.MakePlan, err = p.loadPromptWithLocalFallback(localDir, globalDir, makePlanPromptFile)
	if err != nil {
		return Prompts{}, fmt.Errorf("load make_plan prompt: %w", err)
	}

	prompts.Finalize, err = p.loadPromptWithLocalFallback(localDir, globalDir, finalizePromptFile)
	if err != nil {
		return Prompts{}, fmt.Errorf("load finalize prompt: %w", err)
	}

	return prompts, nil
}

// loadPromptWithLocalFallback loads a prompt file with fallback chain: local → global → embedded.
// localDir can be empty to skip local lookup.
func (p *promptLoader) loadPromptWithLocalFallback(localDir, globalDir, filename string) (string, error) {
	// try local first
	if localDir != "" {
		content, err := p.loadPromptFile(filepath.Join(localDir, filename))
		if err != nil {
			return "", err
		}
		if content != "" {
			return content, nil
		}
	}

	// fall back to global → embedded
	return p.loadPromptWithFallback(filepath.Join(globalDir, filename), "defaults/prompts/"+filename)
}

// loadPromptWithFallback tries to load a prompt from a user file first,
// falling back to the embedded filesystem if the user file doesn't exist or is empty.
func (p *promptLoader) loadPromptWithFallback(userPath, embedPath string) (string, error) {
	content, err := p.loadPromptFile(userPath)
	if err != nil {
		return "", err
	}
	if content != "" {
		return content, nil
	}
	return p.loadPromptFromEmbedFS(embedPath)
}

// loadPromptFile reads a prompt file from disk.
// returns empty string (not error) if file doesn't exist.
// comment lines (starting with #) are stripped.
func (p *promptLoader) loadPromptFile(path string) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is constructed internally
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read prompt file %s: %w", path, err)
	}
	return strings.TrimSpace(stripComments(string(data))), nil
}

// loadPromptFromEmbedFS reads a prompt file from an embedded filesystem.
// returns empty string (not error) if file doesn't exist.
// comment lines (starting with #) are stripped.
func (p *promptLoader) loadPromptFromEmbedFS(path string) (string, error) {
	data, err := p.embedFS.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("read embedded prompt %s: %w", path, err)
	}
	return strings.TrimSpace(stripComments(string(data))), nil
}

// stripComments removes lines starting with # (comment lines) from content.
// empty lines are preserved, inline comments are not supported.
// handles both Unix (LF) and Windows (CRLF) line endings.
func stripComments(content string) string {
	// normalize line endings: convert CRLF to LF
	content = strings.ReplaceAll(content, "\r\n", "\n")

	// pre-allocate with estimated capacity (count newlines + 1)
	lines := make([]string, 0, strings.Count(content, "\n")+1)
	for line := range strings.SplitSeq(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
