package config

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// defaultsInstaller implements DefaultsInstaller with embedded filesystem.
type defaultsInstaller struct {
	embedFS embed.FS
}

// newDefaultsInstaller creates a new defaultsInstaller with the given embedded filesystem.
func newDefaultsInstaller(embedFS embed.FS) *defaultsInstaller {
	return &defaultsInstaller{embedFS: embedFS}
}

// Install creates the config directory and installs default config files if they don't exist.
// this is called on first run to set up the configuration.
// the config file is always created if missing.
// prompts and agents are only installed when their respective directories have no .txt files -
// this allows users to manage the full set of prompts/agents without interference.
func (d *defaultsInstaller) Install(configDir string) error {
	// create config directory (0700 - user only)
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	// create prompts subdirectory
	promptsDir := filepath.Join(configDir, "prompts")
	if err := os.MkdirAll(promptsDir, 0o700); err != nil {
		return fmt.Errorf("create prompts dir: %w", err)
	}

	// create agents subdirectory
	agentsDir := filepath.Join(configDir, "agents")
	if err := os.MkdirAll(agentsDir, 0o700); err != nil {
		return fmt.Errorf("create agents dir: %w", err)
	}

	// install default config file if not exists
	configPath := filepath.Join(configDir, "config")
	_, statErr := os.Stat(configPath)
	if statErr != nil && !os.IsNotExist(statErr) {
		return fmt.Errorf("check config file: %w", statErr)
	}
	if os.IsNotExist(statErr) {
		data, err := d.embedFS.ReadFile("defaults/config")
		if err != nil {
			return fmt.Errorf("read embedded config: %w", err)
		}

		if err := os.WriteFile(configPath, data, 0o600); err != nil {
			return fmt.Errorf("write config file: %w", err)
		}
	}

	// install default prompt files if directory is empty
	if err := d.installDefaultFiles(promptsDir, "defaults/prompts", "prompt"); err != nil {
		return fmt.Errorf("install default prompts: %w", err)
	}

	// install default agent files if directory is empty
	if err := d.installDefaultFiles(agentsDir, "defaults/agents", "agent"); err != nil {
		return fmt.Errorf("install default agents: %w", err)
	}

	return nil
}

// installDefaultFiles copies embedded .txt files to the destination directory.
// files are only installed if the directory has no .txt files - never overwrites.
func (d *defaultsInstaller) installDefaultFiles(destDir, embedPath, fileType string) error {
	// check if directory has any .txt files - if so, skip installation entirely
	existingEntries, err := os.ReadDir(destDir)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s dir: %w", fileType, err)
	}
	for _, entry := range existingEntries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".txt") {
			return nil // directory has files, don't install defaults
		}
	}

	defaultEntries, err := d.embedFS.ReadDir(embedPath)
	if err != nil {
		return fmt.Errorf("read embedded %s dir: %w", fileType, err)
	}

	for _, entry := range defaultEntries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt") {
			continue
		}

		data, err := d.embedFS.ReadFile(embedPath + "/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read embedded %s %s: %w", fileType, entry.Name(), err)
		}

		destPath := filepath.Join(destDir, entry.Name())
		if err := os.WriteFile(destPath, data, 0o600); err != nil {
			return fmt.Errorf("write %s file %s: %w", fileType, entry.Name(), err)
		}
	}

	return nil
}
