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

// agentLoader loads custom agent files from config directories with embedded fallback.
type agentLoader struct {
	embedFS embed.FS
}

// newAgentLoader creates a new agentLoader with the given embedded filesystem.
func newAgentLoader(embedFS embed.FS) *agentLoader {
	return &agentLoader{embedFS: embedFS}
}

// Load loads custom agent files from config directories.
// agents use "replace entire set" behavior: if local agents dir has any .txt files,
// use ONLY local agents; otherwise fall back to global agents.
// this differs from prompts which use per-file fallback (local → global → embedded).
// rationale: agents define the review strategy as a cohesive set, so partial mixing
// would create unpredictable review behavior.
func (al *agentLoader) Load(localDir, globalDir string) ([]CustomAgent, error) {
	// check if local agents dir has any .txt files
	if localDir != "" {
		hasAgentFiles, err := al.dirHasAgentFiles(localDir)
		if err != nil {
			return nil, err
		}
		if hasAgentFiles {
			// use ONLY local agents
			return al.loadFromDir(localDir)
		}
	}

	// fall back to global agents
	return al.loadFromDir(globalDir)
}

// dirHasAgentFiles checks if a directory has any .txt files.
func (al *agentLoader) dirHasAgentFiles(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read agents directory %s: %w", dir, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".txt") {
			return true, nil
		}
	}
	return false, nil
}

// loadFromDir loads agent files from a specific directory.
// files with only comments/empty content fall back to embedded defaults.
// if directory doesn't exist, falls back to loading all embedded agents.
func (al *agentLoader) loadFromDir(agentsDir string) ([]CustomAgent, error) {
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return al.loadAllFromEmbedFS()
		}
		return nil, fmt.Errorf("read agents directory %s: %w", agentsDir, err)
	}

	agents := make([]CustomAgent, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt") {
			continue
		}

		prompt, err := al.loadFileWithFallback(filepath.Join(agentsDir, entry.Name()), entry.Name())
		if err != nil {
			return nil, err
		}
		if prompt == "" {
			continue
		}

		name := strings.TrimSuffix(entry.Name(), ".txt")
		agents = append(agents, al.buildAgent(name, prompt))
	}

	sort.Slice(agents, func(i, j int) bool {
		return agents[i].Name < agents[j].Name
	})

	return agents, nil
}

// loadFileWithFallback reads an agent file from disk with fallback to embedded.
// comment lines (starting with #) are stripped.
// if file content is empty after stripping, falls back to embedded default.
func (al *agentLoader) loadFileWithFallback(path, filename string) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is constructed internally
	if err != nil {
		return "", fmt.Errorf("read agent file %s: %w", path, err)
	}
	content := strings.TrimSpace(stripComments(string(data)))
	if _, body := parseOptions(content); body != "" {
		return content, nil
	}
	// fall back to embedded default, frontmatter options (if any) are dropped
	log.Printf("[WARN] agent %s: no prompt body, falling back to embedded default (frontmatter options dropped)", filename)
	return al.loadFromEmbedFS(filename)
}

// loadFromEmbedFS reads an agent file from the embedded filesystem.
// returns empty string (not error) if file doesn't exist.
// comment lines (starting with #) are stripped.
func (al *agentLoader) loadFromEmbedFS(filename string) (string, error) {
	data, err := al.embedFS.ReadFile("defaults/agents/" + filename)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil // custom agent with only comments - skip it
		}
		return "", fmt.Errorf("read embedded agent %s: %w", filename, err)
	}
	return strings.TrimSpace(stripComments(string(data))), nil
}

// loadAllFromEmbedFS loads all agent files from the embedded filesystem.
// used as fallback when the agents directory doesn't exist.
func (al *agentLoader) loadAllFromEmbedFS() ([]CustomAgent, error) {
	entries, err := al.embedFS.ReadDir("defaults/agents")
	if err != nil {
		return nil, fmt.Errorf("read embedded agents dir: %w", err)
	}

	agents := make([]CustomAgent, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt") {
			continue
		}

		prompt, err := al.loadFromEmbedFS(entry.Name())
		if err != nil {
			return nil, err
		}
		if prompt == "" {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".txt")
		agents = append(agents, al.buildAgent(name, prompt))
	}

	sort.Slice(agents, func(i, j int) bool {
		return agents[i].Name < agents[j].Name
	})

	return agents, nil
}

// buildAgent parses frontmatter options from prompt and builds a CustomAgent.
// if parseOptions produces no body, the raw prompt is used with default options.
func (al *agentLoader) buildAgent(name, prompt string) CustomAgent {
	opts, body := parseOptions(prompt)
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
