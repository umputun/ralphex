// Package config provides configuration management for ralphex with embedded defaults.
package config

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed defaults/config defaults/prompts/* defaults/agents/*
var defaultsFS embed.FS

// prompt file names
const (
	taskPromptFile         = "task.txt"
	reviewFirstPromptFile  = "review_first.txt"
	reviewSecondPromptFile = "review_second.txt"
	codexPromptFile        = "codex.txt"
	makePlanPromptFile     = "make_plan.txt"
	finalizePromptFile     = "finalize.txt"
)

// Config holds all configuration settings for ralphex.
// Fields ending in *Set track whether that field was explicitly set in config.
// This allows distinguishing explicit false/0 from "not set", enabling proper
// merge behavior where local config can override global config with zero values.
//
// *Set fields:
//   - CodexEnabledSet: tracks if codex_enabled was explicitly set
//   - CodexTimeoutMsSet: tracks if codex_timeout_ms was explicitly set
//   - IterationDelayMsSet: tracks if iteration_delay_ms was explicitly set
//   - TaskRetryCountSet: tracks if task_retry_count was explicitly set
//   - FinalizeEnabledSet: tracks if finalize_enabled was explicitly set
type Config struct {
	ClaudeCommand string `json:"claude_command"`
	ClaudeArgs    string `json:"claude_args"`

	CodexEnabled         bool   `json:"codex_enabled"`
	CodexEnabledSet      bool   `json:"-"` // tracks if codex_enabled was explicitly set in config
	CodexCommand         string `json:"codex_command"`
	CodexModel           string `json:"codex_model"`
	CodexReasoningEffort string `json:"codex_reasoning_effort"`
	CodexTimeoutMs       int    `json:"codex_timeout_ms"`
	CodexTimeoutMsSet    bool   `json:"-"` // tracks if codex_timeout_ms was explicitly set in config
	CodexSandbox         string `json:"codex_sandbox"`

	IterationDelayMs    int  `json:"iteration_delay_ms"`
	IterationDelayMsSet bool `json:"-"` // tracks if iteration_delay_ms was explicitly set in config
	TaskRetryCount      int  `json:"task_retry_count"`
	TaskRetryCountSet   bool `json:"-"` // tracks if task_retry_count was explicitly set in config

	FinalizeEnabled    bool `json:"finalize_enabled"`
	FinalizeEnabledSet bool `json:"-"` // tracks if finalize_enabled was explicitly set in config

	PlansDir  string   `json:"plans_dir"`
	WatchDirs []string `json:"watch_dirs"` // directories to watch for progress files

	// error patterns to detect in executor output (e.g., rate limit messages)
	ClaudeErrorPatterns []string `json:"claude_error_patterns"`
	CodexErrorPatterns  []string `json:"codex_error_patterns"`

	// output colors (RGB values as comma-separated strings)
	Colors ColorConfig `json:"-"`

	// prompts (loaded separately from files)
	TaskPrompt         string `json:"-"`
	ReviewFirstPrompt  string `json:"-"`
	ReviewSecondPrompt string `json:"-"`
	CodexPrompt        string `json:"-"`
	MakePlanPrompt     string `json:"-"`
	FinalizePrompt     string `json:"-"`

	// custom agents (loaded separately from files)
	CustomAgents []CustomAgent `json:"-"`

	configDir string // private, global config directory set by Load()
	localDir  string // private, local project config directory (.ralphex/) if found
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

// Load loads all configuration from the specified directory.
// If configDir is empty, uses the default location (~/.config/ralphex/).
// It also auto-detects .ralphex/ in the current working directory for local overrides.
// It installs defaults if needed, parses config file, loads prompts and agents.
func Load(configDir string) (*Config, error) {
	globalDir := configDir
	if globalDir == "" {
		globalDir = DefaultConfigDir()
	}

	// auto-detect local config directory in cwd.
	// os.Getwd() failure is silently ignored - local config is optional,
	// and the global config will still work correctly.
	var localDir string
	if cwd, err := os.Getwd(); err == nil {
		candidate := filepath.Join(cwd, ".ralphex")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			localDir = candidate
		}
	}

	return loadWithLocal(globalDir, localDir)
}

// loadWithLocal loads configuration with explicit global and local directories.
// local config (.ralphex/) overrides global config (~/.config/ralphex/) per-field.
// if localDir is empty, only global config is used.
func loadWithLocal(globalDir, localDir string) (*Config, error) {
	// install defaults
	installer := newDefaultsInstaller(defaultsFS)
	if err := installer.Install(globalDir); err != nil {
		return nil, fmt.Errorf("install defaults: %w", err)
	}

	return loadConfigFromDirs(globalDir, localDir)
}

// LoadReadOnly loads configuration without installing defaults.
// use this in tests or tools that should not modify user's config directory.
// if config files don't exist, embedded defaults are used.
func LoadReadOnly(configDir string) (*Config, error) {
	globalDir := configDir
	if globalDir == "" {
		globalDir = DefaultConfigDir()
	}

	// auto-detect local config directory in cwd
	var localDir string
	if cwd, err := os.Getwd(); err == nil {
		candidate := filepath.Join(cwd, ".ralphex")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			localDir = candidate
		}
	}

	return loadConfigFromDirs(globalDir, localDir)
}

// loadConfigFromDirs loads configuration from specified directories without installing defaults.
// shared by loadWithLocal (after installing) and LoadReadOnly (without installing).
func loadConfigFromDirs(globalDir, localDir string) (*Config, error) {
	embedFS := defaultsFS

	// build config file paths
	var localConfigPath, globalConfigPath string
	if localDir != "" {
		localConfigPath = filepath.Join(localDir, "config")
	}
	globalConfigPath = filepath.Join(globalDir, "config")

	// load values (scalars) - falls back to embedded if files don't exist
	vl := newValuesLoader(embedFS)
	values, err := vl.Load(localConfigPath, globalConfigPath)
	if err != nil {
		return nil, fmt.Errorf("load values: %w", err)
	}

	// load colors
	cl := newColorLoader(embedFS)
	colors, err := cl.Load(localConfigPath, globalConfigPath)
	if err != nil {
		return nil, fmt.Errorf("load colors: %w", err)
	}

	// load prompts
	var localPromptsPath, globalPromptsPath string
	if localDir != "" {
		localPromptsPath = filepath.Join(localDir, "prompts")
	}
	globalPromptsPath = filepath.Join(globalDir, "prompts")
	pl := newPromptLoader(embedFS)
	prompts, err := pl.Load(localPromptsPath, globalPromptsPath)
	if err != nil {
		return nil, fmt.Errorf("load prompts: %w", err)
	}

	// load agents
	var localAgentsPath, globalAgentsPath string
	if localDir != "" {
		localAgentsPath = filepath.Join(localDir, "agents")
	}
	globalAgentsPath = filepath.Join(globalDir, "agents")
	al := newAgentLoader(defaultsFS)
	agents, err := al.Load(localAgentsPath, globalAgentsPath)
	if err != nil {
		return nil, fmt.Errorf("load agents: %w", err)
	}

	// assemble config
	c := &Config{
		ClaudeCommand:        values.ClaudeCommand,
		ClaudeArgs:           values.ClaudeArgs,
		CodexEnabled:         values.CodexEnabled,
		CodexEnabledSet:      values.CodexEnabledSet,
		CodexCommand:         values.CodexCommand,
		CodexModel:           values.CodexModel,
		CodexReasoningEffort: values.CodexReasoningEffort,
		CodexTimeoutMs:       values.CodexTimeoutMs,
		CodexTimeoutMsSet:    values.CodexTimeoutMsSet,
		CodexSandbox:         values.CodexSandbox,
		IterationDelayMs:     values.IterationDelayMs,
		IterationDelayMsSet:  values.IterationDelayMsSet,
		TaskRetryCount:       values.TaskRetryCount,
		TaskRetryCountSet:    values.TaskRetryCountSet,
		FinalizeEnabled:      values.FinalizeEnabled,
		FinalizeEnabledSet:   values.FinalizeEnabledSet,
		PlansDir:             values.PlansDir,
		WatchDirs:            values.WatchDirs,
		ClaudeErrorPatterns:  values.ClaudeErrorPatterns,
		CodexErrorPatterns:   values.CodexErrorPatterns,
		Colors:               colors,
		TaskPrompt:           prompts.Task,
		ReviewFirstPrompt:    prompts.ReviewFirst,
		ReviewSecondPrompt:   prompts.ReviewSecond,
		CodexPrompt:          prompts.Codex,
		MakePlanPrompt:       prompts.MakePlan,
		FinalizePrompt:       prompts.Finalize,
		CustomAgents:         agents,
		configDir:            globalDir,
		localDir:             localDir,
	}

	return c, nil
}

// DefaultConfigDir returns the default configuration directory path.
// returns ~/.config/ralphex/ on all platforms.
// if os.UserHomeDir() fails, falls back to ./.config/ralphex/ silently -
// this allows the tool to work even in unusual environments.
func DefaultConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".config", "ralphex")
	}
	return filepath.Join(home, ".config", "ralphex")
}

// LocalDir returns the local project config directory if one was detected.
// returns empty string if no local config was used.
func (c *Config) LocalDir() string {
	return c.localDir
}
