package config

import (
	"embed"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/ini.v1"
)

// Values holds scalar configuration values.
// Fields ending in *Set (e.g., CodexEnabledSet) track whether that field was explicitly
// set in config. This allows distinguishing explicit false/0 from "not set", enabling
// proper merge behavior where local config can override global config with zero values.
type Values struct {
	ClaudeCommand         string
	ClaudeArgs            string
	ClaudeErrorPatterns   []string // patterns to detect in claude output (e.g., rate limit messages)
	CodexEnabled          bool
	CodexEnabledSet       bool // tracks if codex_enabled was explicitly set
	CodexCommand          string
	CodexModel            string
	CodexReasoningEffort  string
	CodexTimeoutMs        int
	CodexTimeoutMsSet     bool // tracks if codex_timeout_ms was explicitly set
	CodexSandbox          string
	CodexErrorPatterns    []string // patterns to detect in codex output (e.g., rate limit messages)
	ClaudeLimitPatterns   []string // patterns to detect rate limits in claude output (for wait+retry)
	CodexLimitPatterns    []string // patterns to detect rate limits in codex output (for wait+retry)
	WaitOnLimit           time.Duration
	WaitOnLimitSet        bool // tracks if wait_on_limit was explicitly set
	SessionTimeout        time.Duration
	SessionTimeoutSet     bool   // tracks if session_timeout was explicitly set
	ExternalReviewTool    string // "codex", "custom", or "none"
	CustomReviewScript    string // path to custom review script (when ExternalReviewTool = "custom")
	IterationDelayMs      int
	IterationDelayMsSet   bool // tracks if iteration_delay_ms was explicitly set
	TaskRetryCount        int
	TaskRetryCountSet     bool // tracks if task_retry_count was explicitly set
	MaxIterations         int
	MaxIterationsSet      bool // tracks if max_iterations was explicitly set
	MaxExternalIterations int  // override external review iteration limit (0 = auto)
	ReviewPatience        int  // terminate external review after N unchanged rounds (0 = disabled)
	FinalizeEnabled       bool
	FinalizeEnabledSet    bool // tracks if finalize_enabled was explicitly set
	WorktreeEnabled       bool
	WorktreeEnabledSet    bool   // tracks if use_worktree was explicitly set
	VcsCommand            string // custom VCS command (default: "git")
	CommitTrailer         string // trailer line to append to all commits (e.g., "Co-authored-by: ...")
	PlansDir              string
	DefaultBranch         string   // override auto-detected default branch
	WatchDirs             []string // directories to watch for progress files

	// notification settings
	NotifyChannels        []string // channels to use: telegram, email, webhook, slack, custom
	NotifyChannelsSet     bool     // tracks if notify_channels was explicitly set (allows empty to disable)
	NotifyOnError         bool
	NotifyOnErrorSet      bool // tracks if notify_on_error was explicitly set
	NotifyOnComplete      bool
	NotifyOnCompleteSet   bool // tracks if notify_on_complete was explicitly set
	NotifyTimeoutMs       int
	NotifyTimeoutMsSet    bool // tracks if notify_timeout_ms was explicitly set
	NotifyTelegramToken   string
	NotifyTelegramChat    string
	NotifySlackToken      string
	NotifySlackChannel    string
	NotifySMTPHost        string
	NotifySMTPPort        int
	NotifySMTPPortSet     bool // tracks if notify_smtp_port was explicitly set
	NotifySMTPUsername    string
	NotifySMTPPassword    string
	NotifySMTPStartTLS    bool
	NotifySMTPStartTLSSet bool // tracks if notify_smtp_starttls was explicitly set
	NotifyEmailFrom       string
	NotifyEmailTo         []string // comma-separated in config
	NotifyEmailToSet      bool     // tracks if notify_email_to was explicitly set (allows empty to disable)
	NotifyWebhookURLs     []string // comma-separated in config
	NotifyWebhookURLsSet  bool     // tracks if notify_webhook_urls was explicitly set (allows empty to disable)
	NotifyCustomScript    string   // path to custom notification script (tilde-expanded)
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
//nolint:gocyclo // high complexity from many config keys; splitting would hurt readability
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

	// external review settings
	if key, err := section.GetKey("external_review_tool"); err == nil {
		values.ExternalReviewTool = key.String()
	}
	if key, err := section.GetKey("custom_review_script"); err == nil {
		values.CustomReviewScript = expandTilde(key.String())
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
	if key, err := section.GetKey("max_iterations"); err == nil {
		val, intErr := key.Int()
		if intErr != nil {
			return Values{}, fmt.Errorf("invalid max_iterations: %w", intErr)
		}
		if val < 1 {
			return Values{}, fmt.Errorf("invalid max_iterations: must be positive, got %d", val)
		}
		values.MaxIterations = val
		values.MaxIterationsSet = true
	}
	if key, err := section.GetKey("max_external_iterations"); err == nil {
		val, intErr := key.Int()
		if intErr != nil {
			return Values{}, fmt.Errorf("invalid max_external_iterations: %w", intErr)
		}
		if val < 0 {
			return Values{}, fmt.Errorf("invalid max_external_iterations: must be non-negative, got %d", val)
		}
		values.MaxExternalIterations = val
	}
	if key, err := section.GetKey("review_patience"); err == nil {
		val, intErr := key.Int()
		if intErr != nil {
			return Values{}, fmt.Errorf("invalid review_patience: %w", intErr)
		}
		if val < 0 {
			return Values{}, fmt.Errorf("invalid review_patience: must be non-negative, got %d", val)
		}
		values.ReviewPatience = val
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

	// worktree settings
	if key, err := section.GetKey("use_worktree"); err == nil {
		val, boolErr := key.Bool()
		if boolErr != nil {
			return Values{}, fmt.Errorf("invalid use_worktree: %w", boolErr)
		}
		values.WorktreeEnabled = val
		values.WorktreeEnabledSet = true
	}

	// paths
	if key, err := section.GetKey("plans_dir"); err == nil {
		values.PlansDir = key.String()
	}
	if key, err := section.GetKey("default_branch"); err == nil {
		values.DefaultBranch = strings.TrimSpace(key.String())
	}
	if key, err := section.GetKey("vcs_command"); err == nil {
		values.VcsCommand = expandTilde(key.String())
	}
	if key, err := section.GetKey("commit_trailer"); err == nil {
		values.CommitTrailer = strings.TrimSpace(key.String())
	}

	// watch directories (comma-separated)
	values.WatchDirs = vl.parseCommaSeparated(section, "watch_dirs")

	// notification settings
	if err := vl.parseNotifyValues(section, &values); err != nil {
		return Values{}, err
	}

	// error patterns (comma-separated)
	values.ClaudeErrorPatterns = vl.parseCommaSeparated(section, "claude_error_patterns")
	values.CodexErrorPatterns = vl.parseCommaSeparated(section, "codex_error_patterns")

	// limit patterns (comma-separated, same format as error patterns)
	values.ClaudeLimitPatterns = vl.parseCommaSeparated(section, "claude_limit_patterns")
	values.CodexLimitPatterns = vl.parseCommaSeparated(section, "codex_limit_patterns")

	// wait_on_limit duration
	if err := vl.parseWaitOnLimit(section, &values); err != nil {
		return Values{}, err
	}

	// session_timeout duration
	if err := vl.parseSessionTimeout(section, &values); err != nil {
		return Values{}, err
	}

	return values, nil
}

// parseWaitOnLimit parses wait_on_limit duration from an INI section.
func (vl *valuesLoader) parseWaitOnLimit(section *ini.Section, values *Values) error {
	if !section.HasKey("wait_on_limit") {
		return nil
	}
	val := strings.TrimSpace(section.Key("wait_on_limit").String())
	if val == "" {
		return nil
	}
	d, parseErr := time.ParseDuration(val)
	if parseErr != nil {
		return fmt.Errorf("invalid wait_on_limit: %w", parseErr)
	}
	if d < 0 {
		return fmt.Errorf("invalid wait_on_limit: must be non-negative, got %s", val)
	}
	values.WaitOnLimit = d
	values.WaitOnLimitSet = true
	return nil
}

// parseSessionTimeout parses session_timeout duration from an INI section.
func (vl *valuesLoader) parseSessionTimeout(section *ini.Section, values *Values) error {
	if !section.HasKey("session_timeout") {
		return nil
	}
	val := strings.TrimSpace(section.Key("session_timeout").String())
	if val == "" {
		return nil
	}
	d, parseErr := time.ParseDuration(val)
	if parseErr != nil {
		return fmt.Errorf("invalid session_timeout: %w", parseErr)
	}
	if d < 0 {
		return fmt.Errorf("invalid session_timeout: must be non-negative, got %s", val)
	}
	values.SessionTimeout = d
	values.SessionTimeoutSet = true
	return nil
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
	if src.ExternalReviewTool != "" {
		dst.ExternalReviewTool = src.ExternalReviewTool
	}
	if src.CustomReviewScript != "" {
		dst.CustomReviewScript = src.CustomReviewScript
	}
	dst.mergeExecutionFrom(src)
	dst.mergeExtraFrom(src)
	dst.mergeNotifyFrom(src)
}

// mergeExecutionFrom merges execution-related fields from src into dst.
// called from mergeFrom to manage cyclomatic complexity.
func (dst *Values) mergeExecutionFrom(src *Values) {
	if src.IterationDelayMsSet {
		dst.IterationDelayMs = src.IterationDelayMs
		dst.IterationDelayMsSet = true
	}
	if src.TaskRetryCountSet {
		dst.TaskRetryCount = src.TaskRetryCount
		dst.TaskRetryCountSet = true
	}
	if src.MaxIterationsSet {
		dst.MaxIterations = src.MaxIterations
		dst.MaxIterationsSet = true
	}
	if src.MaxExternalIterations > 0 {
		dst.MaxExternalIterations = src.MaxExternalIterations
	}
	if src.ReviewPatience > 0 {
		dst.ReviewPatience = src.ReviewPatience
	}
}

// mergeExtraFrom merges feature flags, paths, error/limit patterns, and wait settings from src into dst.
// called from mergeFrom to manage cyclomatic complexity.
func (dst *Values) mergeExtraFrom(src *Values) {
	if src.FinalizeEnabledSet {
		dst.FinalizeEnabled = src.FinalizeEnabled
		dst.FinalizeEnabledSet = true
	}
	if src.WorktreeEnabledSet {
		dst.WorktreeEnabled = src.WorktreeEnabled
		dst.WorktreeEnabledSet = true
	}
	if src.PlansDir != "" {
		dst.PlansDir = src.PlansDir
	}
	if src.DefaultBranch != "" {
		dst.DefaultBranch = src.DefaultBranch
	}
	if src.VcsCommand != "" {
		dst.VcsCommand = src.VcsCommand
	}
	if src.CommitTrailer != "" {
		dst.CommitTrailer = src.CommitTrailer
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
	if len(src.ClaudeLimitPatterns) > 0 {
		dst.ClaudeLimitPatterns = src.ClaudeLimitPatterns
	}
	if len(src.CodexLimitPatterns) > 0 {
		dst.CodexLimitPatterns = src.CodexLimitPatterns
	}
	if src.WaitOnLimitSet {
		dst.WaitOnLimit = src.WaitOnLimit
		dst.WaitOnLimitSet = true
	}
	if src.SessionTimeoutSet {
		dst.SessionTimeout = src.SessionTimeout
		dst.SessionTimeoutSet = true
	}
}

// mergeNotifyFrom merges notification-related fields from src into dst.
// called from mergeFrom to manage function length.
func (dst *Values) mergeNotifyFrom(src *Values) {
	if src.NotifyChannelsSet {
		dst.NotifyChannels = src.NotifyChannels
		dst.NotifyChannelsSet = true
	}
	if src.NotifyOnErrorSet {
		dst.NotifyOnError = src.NotifyOnError
		dst.NotifyOnErrorSet = true
	}
	if src.NotifyOnCompleteSet {
		dst.NotifyOnComplete = src.NotifyOnComplete
		dst.NotifyOnCompleteSet = true
	}
	if src.NotifyTimeoutMsSet {
		dst.NotifyTimeoutMs = src.NotifyTimeoutMs
		dst.NotifyTimeoutMsSet = true
	}
	if src.NotifyTelegramToken != "" {
		dst.NotifyTelegramToken = src.NotifyTelegramToken
	}
	if src.NotifyTelegramChat != "" {
		dst.NotifyTelegramChat = src.NotifyTelegramChat
	}
	if src.NotifySlackToken != "" {
		dst.NotifySlackToken = src.NotifySlackToken
	}
	if src.NotifySlackChannel != "" {
		dst.NotifySlackChannel = src.NotifySlackChannel
	}
	if src.NotifySMTPHost != "" {
		dst.NotifySMTPHost = src.NotifySMTPHost
	}
	if src.NotifySMTPPortSet {
		dst.NotifySMTPPort = src.NotifySMTPPort
		dst.NotifySMTPPortSet = true
	}
	if src.NotifySMTPUsername != "" {
		dst.NotifySMTPUsername = src.NotifySMTPUsername
	}
	if src.NotifySMTPPassword != "" {
		dst.NotifySMTPPassword = src.NotifySMTPPassword
	}
	if src.NotifySMTPStartTLSSet {
		dst.NotifySMTPStartTLS = src.NotifySMTPStartTLS
		dst.NotifySMTPStartTLSSet = true
	}
	if src.NotifyEmailFrom != "" {
		dst.NotifyEmailFrom = src.NotifyEmailFrom
	}
	if src.NotifyEmailToSet {
		dst.NotifyEmailTo = src.NotifyEmailTo
		dst.NotifyEmailToSet = true
	}
	if src.NotifyWebhookURLsSet {
		dst.NotifyWebhookURLs = src.NotifyWebhookURLs
		dst.NotifyWebhookURLsSet = true
	}
	if src.NotifyCustomScript != "" {
		dst.NotifyCustomScript = src.NotifyCustomScript
	}
}

// parseNotifyValues extracts notification-related settings from an INI section into Values.
// called from parseValuesFromBytes to manage cyclomatic complexity.
func (vl *valuesLoader) parseNotifyValues(section *ini.Section, values *Values) error {
	// notification channels (comma-separated)
	if section.HasKey("notify_channels") {
		values.NotifyChannelsSet = true // key present, even if empty (allows disabling)
		values.NotifyChannels = vl.parseCommaSeparated(section, "notify_channels")
	}

	if key, err := section.GetKey("notify_on_error"); err == nil {
		val, boolErr := key.Bool()
		if boolErr != nil {
			return fmt.Errorf("invalid notify_on_error: %w", boolErr)
		}
		values.NotifyOnError = val
		values.NotifyOnErrorSet = true
	}
	if key, err := section.GetKey("notify_on_complete"); err == nil {
		val, boolErr := key.Bool()
		if boolErr != nil {
			return fmt.Errorf("invalid notify_on_complete: %w", boolErr)
		}
		values.NotifyOnComplete = val
		values.NotifyOnCompleteSet = true
	}
	if key, err := section.GetKey("notify_timeout_ms"); err == nil {
		val, intErr := key.Int()
		if intErr != nil {
			return fmt.Errorf("invalid notify_timeout_ms: %w", intErr)
		}
		if val < 0 {
			return fmt.Errorf("invalid notify_timeout_ms: must be non-negative, got %d", val)
		}
		values.NotifyTimeoutMs = val
		values.NotifyTimeoutMsSet = true
	}

	// telegram settings
	if key, err := section.GetKey("notify_telegram_token"); err == nil {
		values.NotifyTelegramToken = key.String()
	}
	if key, err := section.GetKey("notify_telegram_chat"); err == nil {
		values.NotifyTelegramChat = key.String()
	}

	// slack settings
	if key, err := section.GetKey("notify_slack_token"); err == nil {
		values.NotifySlackToken = key.String()
	}
	if key, err := section.GetKey("notify_slack_channel"); err == nil {
		values.NotifySlackChannel = key.String()
	}

	// custom script (tilde-expanded)
	if key, err := section.GetKey("notify_custom_script"); err == nil {
		values.NotifyCustomScript = expandTilde(key.String())
	}

	return vl.parseNotifyDestValues(section, values)
}

// parseNotifyDestValues extracts SMTP/email and webhook notification settings from an INI section.
// split from parseNotifyValues to keep cyclomatic complexity within limits.
func (vl *valuesLoader) parseNotifyDestValues(section *ini.Section, values *Values) error {
	// webhook settings (comma-separated URLs)
	if section.HasKey("notify_webhook_urls") {
		values.NotifyWebhookURLsSet = true // key present, even if empty (allows disabling)
		values.NotifyWebhookURLs = vl.parseCommaSeparated(section, "notify_webhook_urls")
	}

	// smtp/email settings
	if key, err := section.GetKey("notify_smtp_host"); err == nil {
		values.NotifySMTPHost = key.String()
	}
	if key, err := section.GetKey("notify_smtp_port"); err == nil {
		val, intErr := key.Int()
		if intErr != nil {
			return fmt.Errorf("invalid notify_smtp_port: %w", intErr)
		}
		if val < 0 {
			return fmt.Errorf("invalid notify_smtp_port: must be non-negative, got %d", val)
		}
		values.NotifySMTPPort = val
		values.NotifySMTPPortSet = true
	}
	if key, err := section.GetKey("notify_smtp_username"); err == nil {
		values.NotifySMTPUsername = key.String()
	}
	if key, err := section.GetKey("notify_smtp_password"); err == nil {
		values.NotifySMTPPassword = key.String()
	}
	if key, err := section.GetKey("notify_smtp_starttls"); err == nil {
		val, boolErr := key.Bool()
		if boolErr != nil {
			return fmt.Errorf("invalid notify_smtp_starttls: %w", boolErr)
		}
		values.NotifySMTPStartTLS = val
		values.NotifySMTPStartTLSSet = true
	}
	if key, err := section.GetKey("notify_email_from"); err == nil {
		values.NotifyEmailFrom = key.String()
	}
	if section.HasKey("notify_email_to") {
		values.NotifyEmailToSet = true // key present, even if empty (allows disabling)
		values.NotifyEmailTo = vl.parseCommaSeparated(section, "notify_email_to")
	}

	return nil
}

// parseCommaSeparated reads a key from an INI section, splits by comma, trims whitespace, and filters empty strings.
// returns nil if the key doesn't exist or the value is empty.
func (vl *valuesLoader) parseCommaSeparated(section *ini.Section, key string) []string {
	k, err := section.GetKey(key)
	if err != nil {
		return nil
	}
	val := strings.TrimSpace(k.String())
	if val == "" {
		return nil
	}
	var result []string
	for p := range strings.SplitSeq(val, ",") {
		if t := strings.TrimSpace(p); t != "" {
			result = append(result, t)
		}
	}
	return result
}

// expandTilde expands a leading ~ in a path to the user's home directory.
// returns the original path if it doesn't start with ~/ or if home dir is unavailable.
func expandTilde(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return home + path[1:] // replace ~ with home, keep the /
}
