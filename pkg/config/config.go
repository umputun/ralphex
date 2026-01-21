// Package config provides configuration management for ralphex with embedded defaults.
package config

import (
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/ini.v1"
)

//go:embed defaults/config defaults/prompts/* defaults/agents/*
var defaultsFS embed.FS

// prompt file names
const (
	taskPromptFile         = "task.txt"
	reviewFirstPromptFile  = "review_first.txt"
	reviewSecondPromptFile = "review_second.txt"
	codexPromptFile        = "codex.txt"
)

// stripComments removes lines starting with # (comment lines) from content.
// empty lines are preserved, inline comments are not supported.
func stripComments(content string) string {
	var lines []string
	for line := range strings.SplitSeq(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// Config holds all configuration settings for ralphex.
type Config struct {
	ClaudeCommand string `json:"claude_command"`
	ClaudeArgs    string `json:"claude_args"`

	CodexEnabled         bool   `json:"codex_enabled"`
	CodexEnabledSet      bool   `json:"-"` // tracks if codex_enabled was explicitly set in config
	CodexCommand         string `json:"codex_command"`
	CodexModel           string `json:"codex_model"`
	CodexReasoningEffort string `json:"codex_reasoning_effort"`
	CodexTimeoutMs       int    `json:"codex_timeout_ms"`
	CodexSandbox         string `json:"codex_sandbox"`

	IterationDelayMs  int  `json:"iteration_delay_ms"`
	TaskRetryCount    int  `json:"task_retry_count"`
	TaskRetryCountSet bool `json:"-"` // tracks if task_retry_count was explicitly set in config

	PlansDir string `json:"plans_dir"`

	// output colors (RGB values as comma-separated strings)
	Colors ColorConfig `json:"-"`

	// prompts (loaded separately from files)
	TaskPrompt         string `json:"-"`
	ReviewFirstPrompt  string `json:"-"`
	ReviewSecondPrompt string `json:"-"`
	CodexPrompt        string `json:"-"`

	// custom agents (loaded separately from files)
	CustomAgents []CustomAgent `json:"-"`

	configDir string // private, set by Load()
}

// Load loads all configuration from the specified directory.
// If configDir is empty, uses the default location (~/.config/ralphex/).
// It installs defaults if needed, parses config file, loads prompts and agents.
func Load(configDir string) (*Config, error) {
	c := &Config{}
	c.configDir = configDir
	if configDir == "" {
		c.configDir = c.defaultConfigDir()
	}

	if err := c.installDefaults(); err != nil {
		return nil, fmt.Errorf("install defaults: %w", err)
	}

	if err := c.loadConfigFile(); err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	if err := c.loadScalarsWithFallback(); err != nil {
		return nil, fmt.Errorf("load scalars fallback: %w", err)
	}

	if err := c.loadColorsWithFallback(); err != nil {
		return nil, fmt.Errorf("load colors fallback: %w", err)
	}

	if err := c.loadPrompts(); err != nil {
		return nil, fmt.Errorf("load prompts: %w", err)
	}

	if err := c.loadAgents(); err != nil {
		return nil, fmt.Errorf("load agents: %w", err)
	}

	return c, nil
}

// CustomAgent represents a user-defined review agent.
type CustomAgent struct {
	Name   string // filename without extension
	Prompt string // contents of the agent file
}

// ColorConfig holds RGB values for output colors.
// each field stores comma-separated RGB values (e.g., "255,0,0" for red).
type ColorConfig struct {
	Task       string // task execution phase
	Review     string // review phase
	Codex      string // codex external review
	ClaudeEval string // claude evaluation of codex output
	Warn       string // warning messages
	Error      string // error messages
	Signal     string // completion/failure signals
	Timestamp  string // timestamp prefix
	Info       string // informational messages
}

// DefaultsFS returns the embedded filesystem containing default config files.
func DefaultsFS() embed.FS {
	return defaultsFS
}

// defaultConfigDir returns the default configuration directory path.
// returns ~/.config/ralphex/ on all platforms.
func (c *Config) defaultConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".config", "ralphex")
	}
	return filepath.Join(home, ".config", "ralphex")
}

// loadConfigFile loads configuration from c.configDir.
// if user config exists, parses it directly.
// if user config doesn't exist or fails to read, falls back to embedded defaults.
func (c *Config) loadConfigFile() error {
	configPath := filepath.Join(c.configDir, "config")

	// try user config first
	// path is constructed internally from configDir + "config", not user input
	data, err := os.ReadFile(configPath) //nolint:gosec // path is constructed internally
	if err != nil {
		if os.IsNotExist(err) {
			// no user config - parse embedded defaults into c
			return c.parseEmbeddedDefaults()
		}
		return fmt.Errorf("read config: %w", err)
	}

	return c.parseConfigBytes(data)
}

// parseEmbeddedDefaults parses the embedded defaults/config file into c.
func (c *Config) parseEmbeddedDefaults() error {
	data, err := DefaultsFS().ReadFile("defaults/config")
	if err != nil {
		return fmt.Errorf("read embedded defaults: %w", err)
	}
	return c.parseConfigBytes(data)
}

// loadScalarsWithFallback fills any missing scalar config values from embedded defaults.
// this ensures all scalar fields are populated after config loading.
func (c *Config) loadScalarsWithFallback() error {
	embedded, err := c.parseEmbeddedScalars()
	if err != nil {
		return err
	}

	// string fields: empty = not set
	if c.ClaudeCommand == "" {
		c.ClaudeCommand = embedded.ClaudeCommand
	}
	if c.ClaudeArgs == "" {
		c.ClaudeArgs = embedded.ClaudeArgs
	}
	if c.CodexCommand == "" {
		c.CodexCommand = embedded.CodexCommand
	}
	if c.CodexModel == "" {
		c.CodexModel = embedded.CodexModel
	}
	if c.CodexReasoningEffort == "" {
		c.CodexReasoningEffort = embedded.CodexReasoningEffort
	}
	if c.CodexSandbox == "" {
		c.CodexSandbox = embedded.CodexSandbox
	}
	if c.PlansDir == "" {
		c.PlansDir = embedded.PlansDir
	}

	// bool with tracking: use *Set flag
	if !c.CodexEnabledSet {
		c.CodexEnabled = embedded.CodexEnabled
	}

	// int with tracking: use *Set flag
	if !c.TaskRetryCountSet {
		c.TaskRetryCount = embedded.TaskRetryCount
	}

	// int without tracking: 0 = not set
	if c.IterationDelayMs == 0 {
		c.IterationDelayMs = embedded.IterationDelayMs
	}
	if c.CodexTimeoutMs == 0 {
		c.CodexTimeoutMs = embedded.CodexTimeoutMs
	}

	return nil
}

// parseEmbeddedScalars parses only the scalar config from embedded defaults.
func (c *Config) parseEmbeddedScalars() (*Config, error) {
	embedded := &Config{}
	data, err := DefaultsFS().ReadFile("defaults/config")
	if err != nil {
		return nil, fmt.Errorf("read embedded defaults: %w", err)
	}
	if err := embedded.parseConfigBytes(data); err != nil {
		return nil, fmt.Errorf("parse embedded defaults: %w", err)
	}
	return embedded, nil
}

// loadColorsWithFallback fills any missing color values from embedded defaults.
// this ensures all ColorConfig fields are populated after config loading.
func (c *Config) loadColorsWithFallback() error {
	embedded, err := c.parseEmbeddedColors()
	if err != nil {
		return err
	}

	if c.Colors.Task == "" {
		c.Colors.Task = embedded.Task
	}
	if c.Colors.Review == "" {
		c.Colors.Review = embedded.Review
	}
	if c.Colors.Codex == "" {
		c.Colors.Codex = embedded.Codex
	}
	if c.Colors.ClaudeEval == "" {
		c.Colors.ClaudeEval = embedded.ClaudeEval
	}
	if c.Colors.Warn == "" {
		c.Colors.Warn = embedded.Warn
	}
	if c.Colors.Error == "" {
		c.Colors.Error = embedded.Error
	}
	if c.Colors.Signal == "" {
		c.Colors.Signal = embedded.Signal
	}
	if c.Colors.Timestamp == "" {
		c.Colors.Timestamp = embedded.Timestamp
	}
	if c.Colors.Info == "" {
		c.Colors.Info = embedded.Info
	}

	return nil
}

// parseEmbeddedColors parses only the color config from embedded defaults.
func (c *Config) parseEmbeddedColors() (ColorConfig, error) {
	data, err := DefaultsFS().ReadFile("defaults/config")
	if err != nil {
		return ColorConfig{}, fmt.Errorf("read embedded defaults: %w", err)
	}

	cfg, err := ini.LoadSources(ini.LoadOptions{IgnoreInlineComment: true}, data)
	if err != nil {
		return ColorConfig{}, fmt.Errorf("parse embedded config: %w", err)
	}

	var colors ColorConfig
	section := cfg.Section("")
	colorKeys := []struct {
		key   string
		field *string
	}{
		{"color_task", &colors.Task},
		{"color_review", &colors.Review},
		{"color_codex", &colors.Codex},
		{"color_claude_eval", &colors.ClaudeEval},
		{"color_warn", &colors.Warn},
		{"color_error", &colors.Error},
		{"color_signal", &colors.Signal},
		{"color_timestamp", &colors.Timestamp},
		{"color_info", &colors.Info},
	}

	for _, ck := range colorKeys {
		key, err := section.GetKey(ck.key)
		if err != nil {
			continue
		}
		hex := strings.TrimSpace(key.String())
		if hex == "" {
			continue
		}
		r, g, b, err := parseHexColor(hex)
		if err != nil {
			return ColorConfig{}, fmt.Errorf("invalid embedded %s: %w", ck.key, err)
		}
		*ck.field = fmt.Sprintf("%d,%d,%d", r, g, b)
	}

	return colors, nil
}

// installDefaults creates the config directory and installs default config files
// if they don't exist. this is called on first run to set up the configuration.
func (c *Config) installDefaults() error {
	// check if all expected paths exist
	paths := []string{
		filepath.Join(c.configDir, "config"),
		filepath.Join(c.configDir, "prompts"),
		filepath.Join(c.configDir, "agents"),
	}

	allExist := true
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			if !os.IsNotExist(err) {
				return fmt.Errorf("check path %s: %w", p, err)
			}
			allExist = false
			break
		}
	}

	if allExist {
		return nil // already installed
	}

	// create config directory (0700 - user only)
	if err := os.MkdirAll(c.configDir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	// create prompts subdirectory
	promptsDir := filepath.Join(c.configDir, "prompts")
	if err := os.MkdirAll(promptsDir, 0o700); err != nil {
		return fmt.Errorf("create prompts dir: %w", err)
	}

	// create agents subdirectory
	agentsDir := filepath.Join(c.configDir, "agents")
	if err := os.MkdirAll(agentsDir, 0o700); err != nil {
		return fmt.Errorf("create agents dir: %w", err)
	}

	// install default config file if not exists
	configPath := filepath.Join(c.configDir, "config")
	_, statErr := os.Stat(configPath)
	if statErr != nil && !os.IsNotExist(statErr) {
		return fmt.Errorf("check config file: %w", statErr)
	}
	if os.IsNotExist(statErr) {
		embedFS := DefaultsFS()
		data, err := embedFS.ReadFile("defaults/config")
		if err != nil {
			return fmt.Errorf("read embedded config: %w", err)
		}

		if err := os.WriteFile(configPath, data, 0o600); err != nil {
			return fmt.Errorf("write config file: %w", err)
		}
	}

	// install default agent files if not exist
	if err := c.installDefaultAgents(agentsDir); err != nil {
		return fmt.Errorf("install default agents: %w", err)
	}

	return nil
}

// installDefaultAgents copies embedded agent files to the user's agents directory.
// agents are only installed if the directory is empty - never overwrites or adds
// to existing agent configurations. called after agentsDir is created.
func (c *Config) installDefaultAgents(agentsDir string) error {
	// check if agents directory has any .txt files - if so, skip installation entirely
	existingEntries, err := os.ReadDir(agentsDir)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read agents dir: %w", err)
	}
	for _, entry := range existingEntries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".txt") {
			return nil // directory has agent files, don't install defaults
		}
	}

	embedFS := DefaultsFS()
	defaultEntries, err := embedFS.ReadDir("defaults/agents")
	if err != nil {
		return fmt.Errorf("read embedded agents dir: %w", err)
	}

	for _, entry := range defaultEntries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt") {
			continue
		}

		data, err := embedFS.ReadFile("defaults/agents/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read embedded agent %s: %w", entry.Name(), err)
		}

		destPath := filepath.Join(agentsDir, entry.Name())
		if err := os.WriteFile(destPath, data, 0o600); err != nil {
			return fmt.Errorf("write agent file %s: %w", entry.Name(), err)
		}
	}

	return nil
}

// parseConfig parses configuration from an io.Reader into c.
func (c *Config) parseConfig(r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	return c.parseConfigBytes(data)
}

// parseConfigBytes parses configuration from a byte slice into c.
func (c *Config) parseConfigBytes(data []byte) error {
	// ignoreInlineComment: true is needed to allow # in values (e.g., color_task = #00ff00)
	// without this, the # would be treated as an inline comment
	cfg, err := ini.LoadSources(ini.LoadOptions{IgnoreInlineComment: true}, data)
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	section := cfg.Section("") // default section (no section header)

	// claude settings
	if key, err := section.GetKey("claude_command"); err == nil {
		c.ClaudeCommand = key.String()
	}
	if key, err := section.GetKey("claude_args"); err == nil {
		c.ClaudeArgs = key.String()
	}

	// codex settings
	if key, err := section.GetKey("codex_enabled"); err == nil {
		val, boolErr := key.Bool()
		if boolErr != nil {
			return fmt.Errorf("invalid codex_enabled: %w", boolErr)
		}
		c.CodexEnabled = val
		c.CodexEnabledSet = true
	}
	if key, err := section.GetKey("codex_command"); err == nil {
		c.CodexCommand = key.String()
	}
	if key, err := section.GetKey("codex_model"); err == nil {
		c.CodexModel = key.String()
	}
	if key, err := section.GetKey("codex_reasoning_effort"); err == nil {
		c.CodexReasoningEffort = key.String()
	}
	if key, err := section.GetKey("codex_timeout_ms"); err == nil {
		val, err := key.Int()
		if err != nil {
			return fmt.Errorf("invalid codex_timeout_ms: %w", err)
		}
		c.CodexTimeoutMs = val
	}
	if key, err := section.GetKey("codex_sandbox"); err == nil {
		c.CodexSandbox = key.String()
	}

	// timing settings
	if key, err := section.GetKey("iteration_delay_ms"); err == nil {
		val, err := key.Int()
		if err != nil {
			return fmt.Errorf("invalid iteration_delay_ms: %w", err)
		}
		c.IterationDelayMs = val
	}
	if key, err := section.GetKey("task_retry_count"); err == nil {
		val, err := key.Int()
		if err != nil {
			return fmt.Errorf("invalid task_retry_count: %w", err)
		}
		if val < 0 {
			return fmt.Errorf("invalid task_retry_count: must be non-negative, got %d", val)
		}
		c.TaskRetryCount = val
		c.TaskRetryCountSet = true
	}

	// paths
	if key, err := section.GetKey("plans_dir"); err == nil {
		c.PlansDir = key.String()
	}

	// colors - parse hex values and convert to RGB strings
	if err := c.parseColors(section); err != nil {
		return err
	}

	return nil
}

// parseColors parses color configuration from the INI section.
// each color_* key is expected to have a hex value (e.g., #ff0000).
// the parsed colors are stored as comma-separated RGB values (e.g., "255,0,0").
func (c *Config) parseColors(section *ini.Section) error {
	colorKeys := []struct {
		key   string
		field *string
	}{
		{"color_task", &c.Colors.Task},
		{"color_review", &c.Colors.Review},
		{"color_codex", &c.Colors.Codex},
		{"color_claude_eval", &c.Colors.ClaudeEval},
		{"color_warn", &c.Colors.Warn},
		{"color_error", &c.Colors.Error},
		{"color_signal", &c.Colors.Signal},
		{"color_timestamp", &c.Colors.Timestamp},
		{"color_info", &c.Colors.Info},
	}

	for _, ck := range colorKeys {
		key, err := section.GetKey(ck.key)
		if err != nil {
			continue
		}
		hex := strings.TrimSpace(key.String())
		if hex == "" {
			return fmt.Errorf("invalid %s: empty value", ck.key)
		}
		r, g, b, err := parseHexColor(hex)
		if err != nil {
			return fmt.Errorf("invalid %s: %w", ck.key, err)
		}
		*ck.field = fmt.Sprintf("%d,%d,%d", r, g, b)
	}

	return nil
}

// loadPrompts loads all prompt files into the Config.
// it first tries to load from the user's config directory (configDir/prompts/),
// falling back to embedded defaults for any missing files.
func (c *Config) loadPrompts() error {
	promptsDir := filepath.Join(c.configDir, "prompts")
	embedFS := DefaultsFS()

	var err error

	c.TaskPrompt, err = c.loadPromptWithFallback(
		filepath.Join(promptsDir, taskPromptFile), embedFS, "defaults/prompts/"+taskPromptFile)
	if err != nil {
		return err
	}

	c.ReviewFirstPrompt, err = c.loadPromptWithFallback(
		filepath.Join(promptsDir, reviewFirstPromptFile), embedFS, "defaults/prompts/"+reviewFirstPromptFile)
	if err != nil {
		return err
	}

	c.ReviewSecondPrompt, err = c.loadPromptWithFallback(
		filepath.Join(promptsDir, reviewSecondPromptFile), embedFS, "defaults/prompts/"+reviewSecondPromptFile)
	if err != nil {
		return err
	}

	c.CodexPrompt, err = c.loadPromptWithFallback(
		filepath.Join(promptsDir, codexPromptFile), embedFS, "defaults/prompts/"+codexPromptFile)
	if err != nil {
		return err
	}

	return nil
}

// loadPromptWithFallback tries to load a prompt from a user file first,
// falling back to the embedded filesystem if the user file doesn't exist or is empty.
func (c *Config) loadPromptWithFallback(userPath string, embedFS embed.FS, embedPath string) (string, error) {
	content, err := c.loadPromptFile(userPath)
	if err != nil {
		return "", err
	}
	if content != "" {
		return content, nil
	}
	return c.loadPromptFromEmbedFS(embedFS, embedPath)
}

// loadPromptFile reads a prompt file from disk.
// returns empty string (not error) if file doesn't exist.
// comment lines (starting with #) are stripped.
func (c *Config) loadPromptFile(path string) (string, error) {
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
func (c *Config) loadPromptFromEmbedFS(embedFS embed.FS, path string) (string, error) {
	data, err := embedFS.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("read embedded prompt %s: %w", path, err)
	}
	return strings.TrimSpace(stripComments(string(data))), nil
}

// loadAgents loads custom agent files from the config directory.
// it scans the agents/ subdirectory and loads each .txt file as a custom agent.
// the filename (without extension) becomes the agent name.
func (c *Config) loadAgents() error {
	agentsDir := filepath.Join(c.configDir, "agents")

	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		if os.IsNotExist(err) {
			c.CustomAgents = nil
			return nil
		}
		return fmt.Errorf("read agents directory %s: %w", agentsDir, err)
	}

	var agents []CustomAgent
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt") {
			continue
		}

		prompt, err := c.loadAgentFile(filepath.Join(agentsDir, entry.Name()))
		if err != nil {
			return err
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

	c.CustomAgents = agents
	return nil
}

// loadAgentFile reads an agent file from disk.
// comment lines (starting with #) are stripped.
func (c *Config) loadAgentFile(path string) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is constructed internally
	if err != nil {
		return "", fmt.Errorf("read agent file %s: %w", path, err)
	}
	return strings.TrimSpace(stripComments(string(data))), nil
}

// parseHexColor parses a hex color string (e.g., "#ff0000") into RGB components.
// returns an error if the format is invalid.
func parseHexColor(hex string) (r, g, b int, err error) {
	if hex == "" || hex[0] != '#' {
		return 0, 0, 0, errors.New("hex color must start with #")
	}
	if len(hex) != 7 {
		return 0, 0, 0, errors.New("hex color must be 7 characters (e.g., #ff0000)")
	}

	// parse the hex value
	var val int64
	val, err = strconv.ParseInt(hex[1:], 16, 32)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid hex color %q: %w", hex, err)
	}

	r = int((val >> 16) & 0xFF)
	g = int((val >> 8) & 0xFF)
	b = int(val & 0xFF)
	return r, g, b, nil
}
