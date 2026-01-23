package config

import (
	"embed"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/ini.v1"
)

// colorLoader implements ColorLoader with embedded filesystem fallback.
type colorLoader struct {
	embedFS embed.FS
}

// newColorLoader creates a new colorLoader with the given embedded filesystem.
func newColorLoader(embedFS embed.FS) *colorLoader {
	return &colorLoader{embedFS: embedFS}
}

// Load loads colors from config files with fallback chain: local → global → embedded.
// localConfigPath and globalConfigPath are full paths to config files (not directories).
//
//nolint:dupl // intentional structural similarity with valuesLoader.Load
func (cl *colorLoader) Load(localConfigPath, globalConfigPath string) (ColorConfig, error) {
	// start with embedded defaults
	embedded, err := cl.parseColorsFromEmbedded()
	if err != nil {
		return ColorConfig{}, fmt.Errorf("parse embedded defaults: %w", err)
	}

	// parse global config if exists
	global, err := cl.parseColorsFromFile(globalConfigPath)
	if err != nil {
		return ColorConfig{}, fmt.Errorf("parse global config: %w", err)
	}

	// parse local config if exists
	local, err := cl.parseColorsFromFile(localConfigPath)
	if err != nil {
		return ColorConfig{}, fmt.Errorf("parse local config: %w", err)
	}

	// merge: embedded → global → local (local wins)
	result := embedded
	result.mergeFrom(&global)
	result.mergeFrom(&local)

	return result, nil
}

// parseColorsFromFile reads a config file and parses colors from it.
// returns empty ColorConfig (not error) if file doesn't exist.
func (cl *colorLoader) parseColorsFromFile(path string) (ColorConfig, error) {
	if path == "" {
		return ColorConfig{}, nil
	}

	data, err := os.ReadFile(path) //nolint:gosec // path is constructed internally
	if err != nil {
		if os.IsNotExist(err) {
			return ColorConfig{}, nil
		}
		return ColorConfig{}, fmt.Errorf("read config %s: %w", path, err)
	}

	return cl.parseColorsFromBytes(data)
}

// parseColorsFromEmbedded parses colors from the embedded defaults/config file.
func (cl *colorLoader) parseColorsFromEmbedded() (ColorConfig, error) {
	data, err := cl.embedFS.ReadFile("defaults/config")
	if err != nil {
		return ColorConfig{}, fmt.Errorf("read embedded defaults: %w", err)
	}
	return cl.parseColorsFromBytes(data)
}

// parseColorsFromBytes parses color configuration from INI data.
func (cl *colorLoader) parseColorsFromBytes(data []byte) (ColorConfig, error) {
	cfg, err := ini.LoadSources(ini.LoadOptions{IgnoreInlineComment: true}, data)
	if err != nil {
		return ColorConfig{}, fmt.Errorf("parse config: %w", err)
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
			return ColorConfig{}, fmt.Errorf("invalid %s: %w", ck.key, err)
		}
		*ck.field = fmt.Sprintf("%d,%d,%d", r, g, b)
	}

	return colors, nil
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

// mergeFrom merges non-empty color values from src into dst.
func (dst *ColorConfig) mergeFrom(src *ColorConfig) {
	if src.Task != "" {
		dst.Task = src.Task
	}
	if src.Review != "" {
		dst.Review = src.Review
	}
	if src.Codex != "" {
		dst.Codex = src.Codex
	}
	if src.ClaudeEval != "" {
		dst.ClaudeEval = src.ClaudeEval
	}
	if src.Warn != "" {
		dst.Warn = src.Warn
	}
	if src.Error != "" {
		dst.Error = src.Error
	}
	if src.Signal != "" {
		dst.Signal = src.Signal
	}
	if src.Timestamp != "" {
		dst.Timestamp = src.Timestamp
	}
	if src.Info != "" {
		dst.Info = src.Info
	}
}
