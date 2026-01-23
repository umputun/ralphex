package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// agentLoader loads custom agent files from config directories.
type agentLoader struct{}

// newAgentLoader creates a new agentLoader.
func newAgentLoader() *agentLoader {
	return &agentLoader{}
}

// Load loads custom agent files from config directories.
// it uses replace behavior: if local agents dir has any .txt files,
// use ONLY local agents; otherwise fall back to global agents.
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
func (al *agentLoader) loadFromDir(agentsDir string) ([]CustomAgent, error) {
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read agents directory %s: %w", agentsDir, err)
	}

	var agents []CustomAgent
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt") {
			continue
		}

		prompt, err := al.loadFile(filepath.Join(agentsDir, entry.Name()))
		if err != nil {
			return nil, err
		}

		if prompt == "" {
			continue
		}

		name := strings.TrimSuffix(entry.Name(), ".txt")
		agents = append(agents, CustomAgent{Name: name, Prompt: prompt})
	}

	sort.Slice(agents, func(i, j int) bool {
		return agents[i].Name < agents[j].Name
	})

	return agents, nil
}

// loadFile reads an agent file from disk.
// comment lines (starting with #) are stripped.
func (al *agentLoader) loadFile(path string) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is constructed internally
	if err != nil {
		return "", fmt.Errorf("read agent file %s: %w", path, err)
	}
	return strings.TrimSpace(stripComments(string(data))), nil
}
