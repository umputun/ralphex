package config

import (
	"embed"
	"fmt"
	"os"
	"strings"

	"gopkg.in/ini.v1"
)

// Values holds scalar configuration values.
// Fields ending in *Set (e.g., CodexEnabledSet) track whether that field was explicitly
// set in config. This allows distinguishing explicit false/0 from "not set", enabling
// proper merge behavior where local config can override global config with zero values.
type Values struct {
	ClaudeCommand        string
	ClaudeArgs           string
	ClaudeErrorPatterns  []string // patterns to detect in claude output (e.g., rate limit messages)
	CodexEnabled         bool
	CodexEnabledSet      bool // tracks if codex_enabled was explicitly set
	CodexCommand         string
	CodexModel           string
	CodexReasoningEffort string
	CodexTimeoutMs       int
	CodexTimeoutMsSet    bool // tracks if codex_timeout_ms was explicitly set
	CodexSandbox         string
	CodexErrorPatterns   []string // patterns to detect in codex output (e.g., rate limit messages)
	IterationDelayMs     int
	IterationDelayMsSet  bool // tracks if iteration_delay_ms was explicitly set
	TaskRetryCount       int
	TaskRetryCountSet    bool // tracks if task_retry_count was explicitly set
	FinalizeEnabled      bool
	FinalizeEnabledSet   bool // tracks if finalize_enabled was explicitly set
	PlansDir             string
	WatchDirs            []string // directories to watch for progress files
}

// valuesLoader implements ValuesLoader with embedded filesystem fallback.
type valuesLoader struct {
	embedFS embed.FS
}

// newValuesLoader creates a new valuesLoader with the given embedded filesystem.
func newValuesLoader(embedFS embed.FS) *valuesLoader {
	return &valuesLoader{embedFS: embedFS}
}

// Load loads values from config files with fallback chain: local → global → embedded.
// localConfigPath and globalConfigPath are full paths to config files (not directories).
//
//nolint:dupl // intentional structural similarity with colorLoader.Load
func (vl *valuesLoader) Load(localConfigPath, globalConfigPath string) (Values, error) {
	// start with embedded defaults
	embedded, err := vl.parseValuesFromEmbedded()
	if err != nil {
		return Values{}, fmt.Errorf("parse embedded defaults: %w", err)
	}

	// parse global config if exists
	global, err := vl.parseValuesFromFile(globalConfigPath)
	if err != nil {
		return Values{}, fmt.Errorf("parse global config: %w", err)
	}

	// parse local config if exists
	local, err := vl.parseValuesFromFile(localConfigPath)
	if err != nil {
		return Values{}, fmt.Errorf("parse local config: %w", err)
	}

	// merge: embedded → global → local (local wins)
	result := embedded
	result.mergeFrom(&global)
	result.mergeFrom(&local)

	return result, nil
}

// parseValuesFromFile reads a config file and parses it into Values.
// returns empty Values (not error) if file doesn't exist or contains only comments/whitespace.
// this enables fallback to embedded defaults for files that are commented templates.
func (vl *valuesLoader) parseValuesFromFile(path string) (Values, error) {
	if path == "" {
		return Values{}, nil
	}

	data, err := os.ReadFile(path) //nolint:gosec // path is constructed internally
	if err != nil {
		if os.IsNotExist(err) {
			return Values{}, nil
		}
		return Values{}, fmt.Errorf("read config %s: %w", path, err)
	}

	// strip comments and check if anything remains
	// if only comments/whitespace, return empty Values to fall back to embedded defaults
	stripped := stripComments(string(data))
	if strings.TrimSpace(stripped) == "" {
		return Values{}, nil
	}

	return vl.parseValuesFromBytes(data)
}

// parseValuesFromEmbedded parses values from the embedded defaults/config file.
func (vl *valuesLoader) parseValuesFromEmbedded() (Values, error) {
	data, err := vl.embedFS.ReadFile("defaults/config")
	if err != nil {
		return Values{}, fmt.Errorf("read embedded defaults: %w", err)
	}
	return vl.parseValuesFromBytes(data)
}

// parseValuesFromBytes parses configuration from a byte slice into Values.
//
//nolint:gocyclo // adding watch_dirs pushed complexity over threshold; splitting would hurt readability
func (vl *valuesLoader) parseValuesFromBytes(data []byte) (Values, error) {
	// ignoreInlineComment: true prevents # from being treated as inline comment marker
	cfg, err := ini.LoadSources(ini.LoadOptions{IgnoreInlineComment: true}, data)
	if err != nil {
		return Values{}, fmt.Errorf("parse config: %w", err)
	}

	var values Values
	section := cfg.Section("") // default section (no section header)

	// claude settings
	if key, err := section.GetKey("claude_command"); err == nil {
		values.ClaudeCommand = key.String()
	}
	if key, err := section.GetKey("claude_args"); err == nil {
		values.ClaudeArgs = key.String()
	}

	// codex settings
	if key, err := section.GetKey("codex_enabled"); err == nil {
		val, boolErr := key.Bool()
		if boolErr != nil {
			return Values{}, fmt.Errorf("invalid codex_enabled: %w", boolErr)
		}
		values.CodexEnabled = val
		values.CodexEnabledSet = true
	}
	if key, err := section.GetKey("codex_command"); err == nil {
		values.CodexCommand = key.String()
	}
	if key, err := section.GetKey("codex_model"); err == nil {
		values.CodexModel = key.String()
	}
	if key, err := section.GetKey("codex_reasoning_effort"); err == nil {
		values.CodexReasoningEffort = key.String()
	}
	if key, err := section.GetKey("codex_timeout_ms"); err == nil {
		val, intErr := key.Int()
		if intErr != nil {
			return Values{}, fmt.Errorf("invalid codex_timeout_ms: %w", intErr)
		}
		if val < 0 {
			return Values{}, fmt.Errorf("invalid codex_timeout_ms: must be non-negative, got %d", val)
		}
		values.CodexTimeoutMs = val
		values.CodexTimeoutMsSet = true
	}
	if key, err := section.GetKey("codex_sandbox"); err == nil {
		values.CodexSandbox = key.String()
	}

	// timing settings
	if key, err := section.GetKey("iteration_delay_ms"); err == nil {
		val, intErr := key.Int()
		if intErr != nil {
			return Values{}, fmt.Errorf("invalid iteration_delay_ms: %w", intErr)
		}
		if val < 0 {
			return Values{}, fmt.Errorf("invalid iteration_delay_ms: must be non-negative, got %d", val)
		}
		values.IterationDelayMs = val
		values.IterationDelayMsSet = true
	}
	if key, err := section.GetKey("task_retry_count"); err == nil {
		val, intErr := key.Int()
		if intErr != nil {
			return Values{}, fmt.Errorf("invalid task_retry_count: %w", intErr)
		}
		if val < 0 {
			return Values{}, fmt.Errorf("invalid task_retry_count: must be non-negative, got %d", val)
		}
		values.TaskRetryCount = val
		values.TaskRetryCountSet = true
	}

	// finalize settings
	if key, err := section.GetKey("finalize_enabled"); err == nil {
		val, boolErr := key.Bool()
		if boolErr != nil {
			return Values{}, fmt.Errorf("invalid finalize_enabled: %w", boolErr)
		}
		values.FinalizeEnabled = val
		values.FinalizeEnabledSet = true
	}

	// paths
	if key, err := section.GetKey("plans_dir"); err == nil {
		values.PlansDir = key.String()
	}

	// watch directories (comma-separated)
	if key, err := section.GetKey("watch_dirs"); err == nil {
		val := strings.TrimSpace(key.String())
		if val != "" {
			for p := range strings.SplitSeq(val, ",") {
				if t := strings.TrimSpace(p); t != "" {
					values.WatchDirs = append(values.WatchDirs, t)
				}
			}
		}
	}

	// error patterns (comma-separated)
	if key, err := section.GetKey("claude_error_patterns"); err == nil {
		val := strings.TrimSpace(key.String())
		if val != "" {
			for p := range strings.SplitSeq(val, ",") {
				if t := strings.TrimSpace(p); t != "" {
					values.ClaudeErrorPatterns = append(values.ClaudeErrorPatterns, t)
				}
			}
		}
	}
	if key, err := section.GetKey("codex_error_patterns"); err == nil {
		val := strings.TrimSpace(key.String())
		if val != "" {
			for p := range strings.SplitSeq(val, ",") {
				if t := strings.TrimSpace(p); t != "" {
					values.CodexErrorPatterns = append(values.CodexErrorPatterns, t)
				}
			}
		}
	}

	return values, nil
}

// mergeFrom merges non-empty values from src into dst.
func (dst *Values) mergeFrom(src *Values) {
	if src.ClaudeCommand != "" {
		dst.ClaudeCommand = src.ClaudeCommand
	}
	if src.ClaudeArgs != "" {
		dst.ClaudeArgs = src.ClaudeArgs
	}
	if src.CodexEnabledSet {
		dst.CodexEnabled = src.CodexEnabled
		dst.CodexEnabledSet = true
	}
	if src.CodexCommand != "" {
		dst.CodexCommand = src.CodexCommand
	}
	if src.CodexModel != "" {
		dst.CodexModel = src.CodexModel
	}
	if src.CodexReasoningEffort != "" {
		dst.CodexReasoningEffort = src.CodexReasoningEffort
	}
	if src.CodexTimeoutMsSet {
		dst.CodexTimeoutMs = src.CodexTimeoutMs
		dst.CodexTimeoutMsSet = true
	}
	if src.CodexSandbox != "" {
		dst.CodexSandbox = src.CodexSandbox
	}
	if src.IterationDelayMsSet {
		dst.IterationDelayMs = src.IterationDelayMs
		dst.IterationDelayMsSet = true
	}
	if src.TaskRetryCountSet {
		dst.TaskRetryCount = src.TaskRetryCount
		dst.TaskRetryCountSet = true
	}
	if src.FinalizeEnabledSet {
		dst.FinalizeEnabled = src.FinalizeEnabled
		dst.FinalizeEnabledSet = true
	}
	if src.PlansDir != "" {
		dst.PlansDir = src.PlansDir
	}
	if len(src.WatchDirs) > 0 {
		dst.WatchDirs = src.WatchDirs
	}
	if len(src.ClaudeErrorPatterns) > 0 {
		dst.ClaudeErrorPatterns = src.ClaudeErrorPatterns
	}
	if len(src.CodexErrorPatterns) > 0 {
		dst.CodexErrorPatterns = src.CodexErrorPatterns
	}
}
