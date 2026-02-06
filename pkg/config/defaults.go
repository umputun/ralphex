package config

import (
	"bufio"
	"bytes"
	"embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// commentOutContent prefixes all non-comment, non-empty lines with "# ".
// lines that are already comments or empty are preserved as-is.
// handles both Unix (LF) and Windows (CRLF) line endings.
func commentOutContent(content string) string {
	// normalize line endings: convert CRLF to LF
	content = strings.ReplaceAll(content, "\r\n", "\n")

	lines := strings.Split(content, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		lines[i] = "# " + line
	}
	return strings.Join(lines, "\n")
}

// shouldOverwrite checks if a file is safe to overwrite with new defaults.
// returns true if file doesn't exist, is empty, or contains only comments/whitespace.
// returns false if file exists but can't be read (preserve unknown content).
// uses stripComments to determine if there's actual content.
func shouldOverwrite(filePath string) bool {
	data, err := os.ReadFile(filePath) //nolint:gosec // user's config file
	if err != nil {
		if os.IsNotExist(err) {
			return true // file doesn't exist - safe to create
		}
		return false // file exists but unreadable - preserve it
	}
	// strip comments and check if anything remains
	stripped := stripComments(string(data))
	return strings.TrimSpace(stripped) == ""
}

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

	// install default config file if not exists or is safe to overwrite (all-commented/empty)
	configPath := filepath.Join(configDir, "config")
	if shouldOverwrite(configPath) {
		data, err := d.embedFS.ReadFile("defaults/config")
		if err != nil {
			return fmt.Errorf("read embedded config: %w", err)
		}
		// write with content commented out - users uncomment what they customize
		commented := commentOutContent(string(data))
		if err := os.WriteFile(configPath, []byte(commented), 0o600); err != nil {
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
// files are only installed if the directory has no .txt files with actual content.
// files with only comments/whitespace are considered safe to overwrite.
// note: this is directory-level logic - if ANY file has content, no defaults are added.
// this allows users to manage their own set of prompts/agents without interference.
func (d *defaultsInstaller) installDefaultFiles(destDir, embedPath, fileType string) error {
	// check if directory has any .txt files with actual content - if so, skip installation entirely
	existingEntries, err := os.ReadDir(destDir)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s dir: %w", fileType, err)
	}
	for _, entry := range existingEntries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".txt") {
			// check if this file has actual content (not just comments/empty)
			filePath := filepath.Join(destDir, entry.Name())
			if !shouldOverwrite(filePath) {
				return nil // directory has file with content, don't install defaults
			}
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

		destPath := filepath.Join(destDir, entry.Name())
		// only write if file doesn't exist or is safe to overwrite (all-commented/empty)
		if !shouldOverwrite(destPath) {
			continue
		}

		data, err := d.embedFS.ReadFile(embedPath + "/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read embedded %s %s: %w", fileType, entry.Name(), err)
		}

		// write with content commented out - users uncomment what they customize
		commented := commentOutContent(string(data))
		if err := os.WriteFile(destPath, []byte(commented), 0o600); err != nil {
			return fmt.Errorf("write %s file %s: %w", fileType, entry.Name(), err)
		}
	}

	return nil
}

// ResetResult holds the result of the reset operation.
type ResetResult struct {
	ConfigReset  bool
	PromptsReset bool
	AgentsReset  bool
}

// Reset interactively resets global configuration to embedded defaults.
// prompts user for each component (config, prompts, agents) before resetting.
// local .ralphex/ is not affected.
func (d *defaultsInstaller) Reset(configDir string, stdin io.Reader, stdout io.Writer) (ResetResult, error) {
	result := ResetResult{}
	scanner := bufio.NewScanner(stdin)

	fmt.Fprintf(stdout, "Reset global configuration to defaults (%s)?\n\n", configDir)

	// reset config file
	configPath := filepath.Join(configDir, "config")
	configReset, err := d.resetConfigFile(configPath, scanner, stdout)
	if err != nil {
		return result, fmt.Errorf("reset config: %w", err)
	}
	result.ConfigReset = configReset

	// reset prompts directory
	promptsDir := filepath.Join(configDir, "prompts")
	promptsReset, err := d.resetPromptsDir(promptsDir, scanner, stdout)
	if err != nil {
		return result, fmt.Errorf("reset prompts: %w", err)
	}
	result.PromptsReset = promptsReset

	// reset agents directory
	agentsDir := filepath.Join(configDir, "agents")
	agentsReset, err := d.resetAgentsDir(agentsDir, scanner, stdout)
	if err != nil {
		return result, fmt.Errorf("reset agents: %w", err)
	}
	result.AgentsReset = agentsReset

	// print summary
	d.printResetSummary(result, stdout)

	return result, nil
}

// resetConfigFile handles interactive reset of the config file.
func (d *defaultsInstaller) resetConfigFile(configPath string, scanner *bufio.Scanner, stdout io.Writer) (bool, error) {
	fmt.Fprintf(stdout, "Config file?\n")

	// read embedded default
	embeddedData, err := d.embedFS.ReadFile("defaults/config")
	if err != nil {
		return false, fmt.Errorf("read embedded config: %w", err)
	}

	// check if local config exists and differs
	info, statErr := os.Stat(configPath)
	switch {
	case os.IsNotExist(statErr):
		fmt.Fprintf(stdout, "  missing, will be created from defaults\n")
	case statErr != nil:
		return false, fmt.Errorf("stat config: %w", statErr)
	default:
		localData, err := os.ReadFile(configPath) //nolint:gosec // user's config file
		if err != nil {
			return false, fmt.Errorf("read local config: %w", err)
		}

		// file is "same" if exact match OR has only comments/whitespace
		if bytes.Equal(embeddedData, localData) || strings.TrimSpace(stripComments(string(localData))) == "" {
			fmt.Fprintf(stdout, "  skipped (matches defaults)\n")
			return false, nil
		}

		fmt.Fprintf(stdout, "  modified (%s), will be reset to defaults\n", info.ModTime().Format("2006-01-02"))
	}

	if !d.askYesNo(scanner, stdout) {
		return false, nil
	}

	// ensure parent directory exists (for first run or after user deleted ~/.config/ralphex/)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		return false, fmt.Errorf("create config dir: %w", err)
	}

	// write with content commented out
	commented := commentOutContent(string(embeddedData))
	if err := os.WriteFile(configPath, []byte(commented), 0o600); err != nil {
		return false, fmt.Errorf("write config file: %w", err)
	}

	return true, nil
}

// resetPromptsDir handles interactive reset of the prompts directory.
func (d *defaultsInstaller) resetPromptsDir(promptsDir string, scanner *bufio.Scanner, stdout io.Writer) (bool, error) {
	fmt.Fprintf(stdout, "\nPrompts directory?\n")

	// find files that differ from embedded defaults
	differentFiles, err := d.findDifferentFiles(promptsDir, "defaults/prompts")
	if err != nil {
		return false, fmt.Errorf("compare prompts: %w", err)
	}

	// find custom files (not in embedded defaults)
	customFiles, err := d.findCustomFiles(promptsDir, "defaults/prompts")
	if err != nil {
		return false, fmt.Errorf("find custom prompts: %w", err)
	}

	if len(differentFiles) == 0 {
		// still show custom files info even when defaults match
		if len(customFiles) > 0 {
			fmt.Fprintf(stdout, "  Custom prompts (untouched):\n")
			for _, f := range customFiles {
				fmt.Fprintf(stdout, "    %s\n", f)
			}
		}
		fmt.Fprintf(stdout, "  skipped (all files match defaults)\n")
		return false, nil
	}

	// display different files with dates
	fmt.Fprintf(stdout, "  Different from current defaults:\n")
	for _, f := range differentFiles {
		if f.missing {
			fmt.Fprintf(stdout, "    %s (missing)\n", f.name)
		} else {
			fmt.Fprintf(stdout, "    %s (%s)\n", f.name, f.modTime.Format("2006-01-02"))
		}
	}

	// display custom files (informational)
	if len(customFiles) > 0 {
		fmt.Fprintf(stdout, "  Custom prompts (untouched):\n")
		for _, f := range customFiles {
			fmt.Fprintf(stdout, "    %s\n", f)
		}
	}

	fmt.Fprintf(stdout, "  Note: differences may be your customizations or outdated defaults\n")
	fmt.Fprintf(stdout, "  Reset will overwrite with current embedded defaults\n")

	if !d.askYesNo(scanner, stdout) {
		return false, nil
	}

	// overwrite only files that exist in embedded defaults
	if err := d.overwriteEmbeddedFiles(promptsDir, "defaults/prompts"); err != nil {
		return false, fmt.Errorf("overwrite prompts: %w", err)
	}

	return true, nil
}

// resetAgentsDir handles interactive reset of the agents directory.
func (d *defaultsInstaller) resetAgentsDir(agentsDir string, scanner *bufio.Scanner, stdout io.Writer) (bool, error) {
	fmt.Fprintf(stdout, "\nAgents directory?\n")

	// find files that differ from embedded defaults
	differentFiles, err := d.findDifferentFiles(agentsDir, "defaults/agents")
	if err != nil {
		return false, fmt.Errorf("compare agents: %w", err)
	}

	// find custom files (not in embedded defaults)
	customFiles, err := d.findCustomFiles(agentsDir, "defaults/agents")
	if err != nil {
		return false, fmt.Errorf("find custom agents: %w", err)
	}

	if len(differentFiles) == 0 {
		fmt.Fprintf(stdout, "  skipped (all files match defaults)\n")
		return false, nil
	}

	// display different files with dates
	fmt.Fprintf(stdout, "  Different from current defaults:\n")
	for _, f := range differentFiles {
		if f.missing {
			fmt.Fprintf(stdout, "    %s (missing)\n", f.name)
		} else {
			fmt.Fprintf(stdout, "    %s (%s)\n", f.name, f.modTime.Format("2006-01-02"))
		}
	}

	// display custom files (informational)
	if len(customFiles) > 0 {
		fmt.Fprintf(stdout, "  Custom agents (untouched):\n")
		for _, f := range customFiles {
			fmt.Fprintf(stdout, "    %s\n", f)
		}
	}

	// count embedded agents for message
	embeddedCount, err := d.countEmbeddedFiles("defaults/agents")
	if err != nil {
		return false, fmt.Errorf("count embedded agents: %w", err)
	}

	if len(differentFiles) > 0 {
		fmt.Fprintf(stdout, "  Note: differences may be your customizations or outdated defaults\n")
		fmt.Fprintf(stdout, "  Reset will overwrite %d default agents, custom agents preserved\n", embeddedCount)
	} else {
		fmt.Fprintf(stdout, "  Reset will reinstall %d default agents, custom agents preserved\n", embeddedCount)
	}

	if !d.askYesNo(scanner, stdout) {
		return false, nil
	}

	// overwrite only files that exist in embedded defaults
	if err := d.overwriteEmbeddedFiles(agentsDir, "defaults/agents"); err != nil {
		return false, fmt.Errorf("overwrite agents: %w", err)
	}

	return true, nil
}

// fileInfo holds information about a file for display.
type fileInfo struct {
	name    string
	modTime time.Time
	missing bool // true if file doesn't exist locally
}

// findDifferentFiles returns files in destDir that differ from embedded defaults.
// files with only comments/empty lines are considered matching (unmodified from commented templates).
func (d *defaultsInstaller) findDifferentFiles(destDir, embedPath string) ([]fileInfo, error) {
	var different []fileInfo

	embeddedEntries, err := d.embedFS.ReadDir(embedPath)
	if err != nil {
		return nil, fmt.Errorf("read embedded dir: %w", err)
	}

	for _, entry := range embeddedEntries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt") {
			continue
		}

		destPath := filepath.Join(destDir, entry.Name())
		info, err := os.Stat(destPath)
		if os.IsNotExist(err) {
			// file doesn't exist locally - mark as missing
			different = append(different, fileInfo{name: entry.Name(), missing: true})
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", entry.Name(), err)
		}

		// compare content - either exact match or local file has only comments (safe to overwrite)
		embeddedData, err := d.embedFS.ReadFile(embedPath + "/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read embedded %s: %w", entry.Name(), err)
		}

		localData, err := os.ReadFile(destPath) //nolint:gosec // user's config file
		if err != nil {
			return nil, fmt.Errorf("read local %s: %w", entry.Name(), err)
		}

		// file is "same" if either:
		// 1. exact byte match (user hasn't touched it)
		// 2. local file has no actual content (only comments/whitespace) - safe to overwrite
		if bytes.Equal(embeddedData, localData) {
			continue // exact match
		}
		if strings.TrimSpace(stripComments(string(localData))) == "" {
			continue // only comments/whitespace - considered unmodified
		}

		different = append(different, fileInfo{name: entry.Name(), modTime: info.ModTime()})
	}

	return different, nil
}

// findCustomFiles returns files in destDir that don't exist in embedded defaults.
func (d *defaultsInstaller) findCustomFiles(destDir, embedPath string) ([]string, error) {
	var custom []string

	// build set of embedded file names
	embeddedNames := make(map[string]bool)
	embeddedEntries, err := d.embedFS.ReadDir(embedPath)
	if err != nil {
		return nil, fmt.Errorf("read embedded dir: %w", err)
	}
	for _, entry := range embeddedEntries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".txt") {
			embeddedNames[entry.Name()] = true
		}
	}

	// find local files not in embedded
	localEntries, err := os.ReadDir(destDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read local dir: %w", err)
	}

	for _, entry := range localEntries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt") {
			continue
		}
		if !embeddedNames[entry.Name()] {
			custom = append(custom, entry.Name())
		}
	}

	return custom, nil
}

// countEmbeddedFiles returns the number of .txt files in an embedded directory.
func (d *defaultsInstaller) countEmbeddedFiles(embedPath string) (int, error) {
	entries, err := d.embedFS.ReadDir(embedPath)
	if err != nil {
		return 0, fmt.Errorf("read embedded dir: %w", err)
	}

	count := 0
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".txt") {
			count++
		}
	}
	return count, nil
}

// overwriteEmbeddedFiles overwrites files in destDir with embedded defaults (commented).
// only overwrites files that exist in embedded defaults - preserves custom files.
func (d *defaultsInstaller) overwriteEmbeddedFiles(destDir, embedPath string) error {
	// ensure directory exists
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}

	embeddedEntries, err := d.embedFS.ReadDir(embedPath)
	if err != nil {
		return fmt.Errorf("read embedded dir: %w", err)
	}

	for _, entry := range embeddedEntries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt") {
			continue
		}

		embeddedData, err := d.embedFS.ReadFile(embedPath + "/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", entry.Name(), err)
		}

		// write with content commented out
		commented := commentOutContent(string(embeddedData))
		destPath := filepath.Join(destDir, entry.Name())
		if err := os.WriteFile(destPath, []byte(commented), 0o600); err != nil {
			return fmt.Errorf("write %s: %w", entry.Name(), err)
		}
	}

	return nil
}

// Reset is a package-level function that creates a defaultsInstaller and calls Reset.
// this is the public API for resetting configuration.
func Reset(configDir string, stdin io.Reader, stdout io.Writer) (ResetResult, error) {
	installer := newDefaultsInstaller(defaultsFS)
	return installer.Reset(configDir, stdin, stdout)
}

// askYesNo prompts the user with [y/N] and returns true for yes.
func (d *defaultsInstaller) askYesNo(scanner *bufio.Scanner, stdout io.Writer) bool {
	fmt.Fprintf(stdout, "  [y/N]: ")

	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			fmt.Fprintf(stdout, "\nerror reading input: %v\n", err)
		} else {
			fmt.Fprintln(stdout) // EOF
		}
		return false
	}

	answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
	return answer == "y" || answer == "yes"
}

// printResetSummary prints a summary of what was reset.
func (d *defaultsInstaller) printResetSummary(result ResetResult, stdout io.Writer) {
	var reset, skipped []string

	if result.ConfigReset {
		reset = append(reset, "config")
	} else {
		skipped = append(skipped, "config")
	}

	if result.PromptsReset {
		reset = append(reset, "prompts")
	} else {
		skipped = append(skipped, "prompts")
	}

	if result.AgentsReset {
		reset = append(reset, "agents")
	} else {
		skipped = append(skipped, "agents")
	}

	fmt.Fprintf(stdout, "\nDone.")
	if len(reset) > 0 {
		fmt.Fprintf(stdout, " Reset: %s.", strings.Join(reset, ", "))
	}
	if len(skipped) > 0 {
		fmt.Fprintf(stdout, " Skipped: %s.", strings.Join(skipped, ", "))
	}
	_, _ = fmt.Fprintln(stdout)
}
