package config

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// agentLoader loads custom agent files from config directories with per-file fallback.
type agentLoader struct {
	embedFS embed.FS
}

// newAgentLoader creates a new agentLoader with the given embedded filesystem.
func newAgentLoader(embedFS embed.FS) *agentLoader {
	return &agentLoader{embedFS: embedFS}
}

// Load loads agent files using per-file fallback: local → global → embedded.
// collects the union of all .txt filenames from embedded defaults, global dir, and local dir,
// then for each unique filename resolves content with precedence: local → global → embedded.
// this matches the prompt loading strategy (see promptLoader.loadPromptWithLocalFallback).
func (al *agentLoader) Load(localDir, globalDir string) ([]CustomAgent, error) {
	// collect union of all agent filenames from all sources
	filenames := make(map[string]struct{})

	// baseline: embedded defaults
	embeddedNames, err := al.collectEmbeddedFilenames()
	if err != nil {
		return nil, err
	}
	for _, name := range embeddedNames {
		filenames[name] = struct{}{}
	}

	// add global dir filenames
	globalNames, err := al.collectDirFilenames(globalDir)
	if err != nil {
		return nil, err
	}
	for _, name := range globalNames {
		filenames[name] = struct{}{}
	}

	// add local dir filenames
	localNames, err := al.collectDirFilenames(localDir)
	if err != nil {
		return nil, err
	}
	for _, name := range localNames {
		filenames[name] = struct{}{}
	}

	// resolve each filename with per-file fallback: local → global → embedded
	agents := make([]CustomAgent, 0, len(filenames))
	for filename := range filenames {
		prompt, loadErr := al.loadAgentWithFallback(localDir, globalDir, filename)
		if loadErr != nil {
			return nil, loadErr
		}
		if prompt == "" {
			continue
		}
		name := strings.TrimSuffix(filename, ".txt")
		agents = append(agents, al.buildAgent(name, prompt))
	}

	sort.Slice(agents, func(i, j int) bool {
		return agents[i].Name < agents[j].Name
	})

	return agents, nil
}

// collectDirFilenames returns .txt filenames from a directory, ignoring subdirs and non-.txt files.
// returns nil, nil if directory doesn't exist; returns error for other filesystem failures.
func (al *agentLoader) collectDirFilenames(dir string) ([]string, error) {
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read agents dir %s: %w", dir, err)
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".txt") {
			names = append(names, entry.Name())
		}
	}
	return names, nil
}

// collectEmbeddedFilenames returns .txt filenames from the embedded agents directory.
func (al *agentLoader) collectEmbeddedFilenames() ([]string, error) {
	entries, err := al.embedFS.ReadDir("defaults/agents")
	if err != nil {
		return nil, fmt.Errorf("read embedded agents dir: %w", err)
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".txt") {
			names = append(names, entry.Name())
		}
	}
	return names, nil
}

// loadAgentWithFallback resolves a single agent file with fallback: local → global → embedded.
// for each location, uses loadFileWithFallback for content-level fallback (empty/all-commented → embedded).
// returns error for filesystem failures other than file-not-found.
func (al *agentLoader) loadAgentWithFallback(localDir, globalDir, filename string) (string, error) {
	// try local first
	if localDir != "" {
		content, err := al.tryLoadFromDir(localDir, filename)
		if err != nil {
			return "", err
		}
		if content != "" {
			return content, nil
		}
	}

	// try global
	if globalDir != "" {
		content, err := al.tryLoadFromDir(globalDir, filename)
		if err != nil {
			return "", err
		}
		if content != "" {
			return content, nil
		}
	}

	// fall back to embedded
	return al.loadFromEmbedFS(filename)
}

// tryLoadFromDir attempts to load an agent file from a directory.
// returns empty string if file doesn't exist or is not a regular file.
func (al *agentLoader) tryLoadFromDir(dir, filename string) (string, error) {
	path := filepath.Join(dir, filename)
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("stat agent file %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return "", nil
	}
	return al.loadFileWithFallback(path, filename)
}

// loadFileWithFallback reads an agent file from disk with fallback to embedded.
// if file content has no prompt body (only comments/whitespace), falls back to embedded default.
func (al *agentLoader) loadFileWithFallback(path, filename string) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is constructed internally
	if err != nil {
		return "", fmt.Errorf("read agent file %s: %w", path, err)
	}
	content := strings.TrimSpace(normalizeCRLF(string(data)))
	// check if file has actual prompt body (strip comments only for emptiness check)
	stripped := strings.TrimSpace(stripComments(content))
	opts, body := parseOptions(stripped)
	if body != "" {
		return content, nil
	}
	// warn only when frontmatter options are being dropped; silent fallback for all-commented files
	if opts.Model != "" || opts.AgentType != "" {
		log.Printf("[WARN] agent %s: no prompt body, falling back to embedded default (frontmatter options dropped)", filename)
	}
	return al.loadFromEmbedFS(filename)
}

// loadFromEmbedFS reads an agent file from the embedded filesystem.
// returns empty string (not error) if file doesn't exist.
func (al *agentLoader) loadFromEmbedFS(filename string) (string, error) {
	data, err := al.embedFS.ReadFile("defaults/agents/" + filename)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil // custom agent with only comments - skip it
		}
		return "", fmt.Errorf("read embedded agent %s: %w", filename, err)
	}
	return strings.TrimSpace(normalizeCRLF(string(data))), nil
}

// buildAgent parses frontmatter options from prompt and builds a CustomAgent.
// if parseOptions produces no body, the raw prompt is used with default options.
// leading comment lines (any count, including single) are stripped before
// frontmatter parsing so that comment lines before "---" don't prevent frontmatter detection.
func (al *agentLoader) buildAgent(name, prompt string) CustomAgent {
	// try frontmatter on raw content first, then with leading comments stripped
	opts, body := parseOptions(prompt)
	if opts == (Options{}) && body == prompt {
		// no frontmatter found in raw content, try after stripping leading comment lines
		if stripped := stripLeadingCommentLines(prompt); stripped != prompt {
			opts, body = parseOptions(stripped)
			if opts == (Options{}) {
				// still no frontmatter, use original prompt
				return CustomAgent{Name: name, Prompt: prompt}
			}
		}
	}
	if body == "" {
		return CustomAgent{Name: name, Prompt: prompt}
	}
	if warnings := opts.Validate(); len(warnings) > 0 {
		for _, w := range warnings {
			log.Printf("[WARN] agent %s: %s", name, w)
		}
		opts = Options{}
	}
	return CustomAgent{Name: name, Prompt: body, Options: opts}
}
