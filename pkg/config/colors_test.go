package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_newColorLoader(t *testing.T) {
	loader := newColorLoader(DefaultsFS())
	assert.NotNil(t, loader)
}

func TestColorLoader_Load_EmbeddedOnly(t *testing.T) {
	loader := newColorLoader(DefaultsFS())
	colors, err := loader.Load("", "")
	require.NoError(t, err)

	// all colors should have expected default values
	assert.Equal(t, "0,255,0", colors.Task, "task color should be green (#00ff00)")
	assert.Equal(t, "0,255,255", colors.Review, "review color should be cyan (#00ffff)")
	assert.Equal(t, "208,150,217", colors.Codex, "codex color should be light magenta (#d096d9)")
	assert.Equal(t, "189,214,255", colors.ClaudeEval, "claude_eval color should be light blue (#bdd6ff)")
	assert.Equal(t, "255,197,109", colors.Warn, "warn color should be orange (#ffc56d)")
	assert.Equal(t, "255,0,0", colors.Error, "error color should be red (#ff0000)")
	assert.Equal(t, "210,82,82", colors.Signal, "signal color should be muted red (#d25252)")
	assert.Equal(t, "138,138,138", colors.Timestamp, "timestamp color should be gray (#8a8a8a)")
	assert.Equal(t, "180,180,180", colors.Info, "info color should be light gray (#b4b4b4)")
}

func TestColorLoader_Load_GlobalConfigOverridesEmbedded(t *testing.T) {
	tmpDir := t.TempDir()
	globalConfig := filepath.Join(tmpDir, "config")

	configContent := `
color_task = #ff0000
color_error = #00ff00
`
	require.NoError(t, os.WriteFile(globalConfig, []byte(configContent), 0o600))

	loader := newColorLoader(DefaultsFS())
	colors, err := loader.Load("", globalConfig)
	require.NoError(t, err)

	// custom colors from global config
	assert.Equal(t, "255,0,0", colors.Task)
	assert.Equal(t, "0,255,0", colors.Error)

	// missing colors from embedded defaults
	assert.Equal(t, "0,255,255", colors.Review)
	assert.Equal(t, "208,150,217", colors.Codex)
}

func TestColorLoader_Load_LocalOverridesGlobal(t *testing.T) {
	tmpDir := t.TempDir()
	globalConfig := filepath.Join(tmpDir, "global-config")
	localConfig := filepath.Join(tmpDir, "local-config")

	globalContent := `
color_task = #ff0000
color_error = #00ff00
`
	require.NoError(t, os.WriteFile(globalConfig, []byte(globalContent), 0o600))

	localContent := `
color_task = #0000ff
`
	require.NoError(t, os.WriteFile(localConfig, []byte(localContent), 0o600))

	loader := newColorLoader(DefaultsFS())
	colors, err := loader.Load(localConfig, globalConfig)
	require.NoError(t, err)

	// local overrides global
	assert.Equal(t, "0,0,255", colors.Task)

	// global preserved when not overridden
	assert.Equal(t, "0,255,0", colors.Error)

	// embedded defaults for unset colors
	assert.Equal(t, "0,255,255", colors.Review)
}

func TestColorLoader_Load_NonExistentFiles(t *testing.T) {
	loader := newColorLoader(DefaultsFS())
	colors, err := loader.Load("/nonexistent/local", "/nonexistent/global")
	require.NoError(t, err)

	// should fall back to embedded defaults
	assert.Equal(t, "0,255,0", colors.Task)
	assert.Equal(t, "255,0,0", colors.Error)
}

func TestColorLoader_Load_InvalidColor(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		errPart string
	}{
		{name: "missing hash", config: "color_task = ff0000", errPart: "color_task"},
		{name: "wrong length", config: "color_review = #fff", errPart: "color_review"},
		{name: "invalid chars", config: "color_codex = #gggggg", errPart: "color_codex"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			configPath := filepath.Join(tmpDir, "config")
			require.NoError(t, os.WriteFile(configPath, []byte(tc.config), 0o600))

			loader := newColorLoader(DefaultsFS())
			_, err := loader.Load("", configPath)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errPart)
		})
	}
}

func TestColorLoader_Load_AllColorsFromConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config")

	configContent := `
color_task = #010203
color_review = #040506
color_codex = #070809
color_claude_eval = #0a0b0c
color_warn = #0d0e0f
color_error = #101112
color_signal = #131415
color_timestamp = #161718
color_info = #191a1b
`
	require.NoError(t, os.WriteFile(configPath, []byte(configContent), 0o600))

	loader := newColorLoader(DefaultsFS())
	colors, err := loader.Load("", configPath)
	require.NoError(t, err)

	assert.Equal(t, "1,2,3", colors.Task)
	assert.Equal(t, "4,5,6", colors.Review)
	assert.Equal(t, "7,8,9", colors.Codex)
	assert.Equal(t, "10,11,12", colors.ClaudeEval)
	assert.Equal(t, "13,14,15", colors.Warn)
	assert.Equal(t, "16,17,18", colors.Error)
	assert.Equal(t, "19,20,21", colors.Signal)
	assert.Equal(t, "22,23,24", colors.Timestamp)
	assert.Equal(t, "25,26,27", colors.Info)
}

func TestColorLoader_parseColorsFromBytes(t *testing.T) {
	cl := &colorLoader{embedFS: DefaultsFS()}

	t.Run("full color config", func(t *testing.T) {
		data := []byte(`
color_task = #00ff00
color_review = #00ffff
color_codex = #ff00ff
color_claude_eval = #64c8ff
color_warn = #ffff00
color_error = #ff0000
color_signal = #ff6464
color_timestamp = #8a8a8a
color_info = #b4b4b4
`)
		colors, err := cl.parseColorsFromBytes(data)
		require.NoError(t, err)

		assert.Equal(t, "0,255,0", colors.Task)
		assert.Equal(t, "0,255,255", colors.Review)
		assert.Equal(t, "255,0,255", colors.Codex)
		assert.Equal(t, "100,200,255", colors.ClaudeEval)
		assert.Equal(t, "255,255,0", colors.Warn)
		assert.Equal(t, "255,0,0", colors.Error)
		assert.Equal(t, "255,100,100", colors.Signal)
		assert.Equal(t, "138,138,138", colors.Timestamp)
		assert.Equal(t, "180,180,180", colors.Info)
	})

	t.Run("partial color config", func(t *testing.T) {
		data := []byte(`
color_task = #ff0000
color_error = #00ff00
`)
		colors, err := cl.parseColorsFromBytes(data)
		require.NoError(t, err)

		assert.Equal(t, "255,0,0", colors.Task)
		assert.Equal(t, "0,255,0", colors.Error)
		assert.Empty(t, colors.Review)
		assert.Empty(t, colors.Codex)
	})

	t.Run("empty config", func(t *testing.T) {
		data := []byte("")
		colors, err := cl.parseColorsFromBytes(data)
		require.NoError(t, err)

		assert.Empty(t, colors.Task)
		assert.Empty(t, colors.Error)
	})

	t.Run("config with whitespace in color values", func(t *testing.T) {
		data := []byte(`color_task =   #ff0000  `)
		colors, err := cl.parseColorsFromBytes(data)
		require.NoError(t, err)
		assert.Equal(t, "255,0,0", colors.Task)
	})

	t.Run("empty color value skipped", func(t *testing.T) {
		data := []byte(`
color_task = #ff0000
color_review =
`)
		colors, err := cl.parseColorsFromBytes(data)
		require.NoError(t, err)
		assert.Equal(t, "255,0,0", colors.Task)
		assert.Empty(t, colors.Review)
	})
}

func TestParseHexColor(t *testing.T) {
	tests := []struct {
		name    string
		hex     string
		wantR   int
		wantG   int
		wantB   int
		wantErr bool
		errMsg  string
	}{
		{name: "valid red", hex: "#ff0000", wantR: 255, wantG: 0, wantB: 0},
		{name: "valid green", hex: "#00ff00", wantR: 0, wantG: 255, wantB: 0},
		{name: "valid blue", hex: "#0000ff", wantR: 0, wantG: 0, wantB: 255},
		{name: "valid lowercase", hex: "#aabbcc", wantR: 170, wantG: 187, wantB: 204},
		{name: "valid uppercase", hex: "#AABBCC", wantR: 170, wantG: 187, wantB: 204},
		{name: "valid mixed case", hex: "#AaBbCc", wantR: 170, wantG: 187, wantB: 204},
		{name: "valid white", hex: "#ffffff", wantR: 255, wantG: 255, wantB: 255},
		{name: "valid black", hex: "#000000", wantR: 0, wantG: 0, wantB: 0},
		{name: "valid gray", hex: "#8a8a8a", wantR: 138, wantG: 138, wantB: 138},
		{name: "missing # prefix", hex: "ff0000", wantErr: true, errMsg: "must start with #"},
		{name: "wrong length short", hex: "#fff", wantErr: true, errMsg: "must be 7 characters"},
		{name: "wrong length long", hex: "#ff00ff00", wantErr: true, errMsg: "must be 7 characters"},
		{name: "empty string", hex: "", wantErr: true, errMsg: "must start with #"},
		{name: "only hash", hex: "#", wantErr: true, errMsg: "must be 7 characters"},
		{name: "invalid hex char g", hex: "#gggggg", wantErr: true, errMsg: "invalid hex"},
		{name: "invalid hex char z", hex: "#zz0000", wantErr: true, errMsg: "invalid hex"},
		{name: "invalid hex space", hex: "#ff 000", wantErr: true, errMsg: "invalid hex"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, g, b, err := parseHexColor(tc.hex)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errMsg)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantR, r, "red component")
			assert.Equal(t, tc.wantG, g, "green component")
			assert.Equal(t, tc.wantB, b, "blue component")
		})
	}
}

func TestColorConfig_mergeFrom(t *testing.T) {
	t.Run("merge non-empty values", func(t *testing.T) {
		dst := &ColorConfig{
			Task:  "1,1,1",
			Error: "2,2,2",
		}
		src := &ColorConfig{
			Task:   "3,3,3",
			Review: "4,4,4",
		}
		dst.mergeFrom(src)

		assert.Equal(t, "3,3,3", dst.Task, "task should be overwritten")
		assert.Equal(t, "4,4,4", dst.Review, "review should be set")
		assert.Equal(t, "2,2,2", dst.Error, "error should be preserved")
	})

	t.Run("empty source doesn't overwrite", func(t *testing.T) {
		dst := &ColorConfig{
			Task:  "1,1,1",
			Error: "2,2,2",
		}
		src := &ColorConfig{
			Task: "", // empty, shouldn't overwrite
		}
		dst.mergeFrom(src)

		assert.Equal(t, "1,1,1", dst.Task, "task should be preserved")
		assert.Equal(t, "2,2,2", dst.Error, "error should be preserved")
	})

	t.Run("merge all fields", func(t *testing.T) {
		dst := &ColorConfig{}
		src := &ColorConfig{
			Task:       "1,2,3",
			Review:     "4,5,6",
			Codex:      "7,8,9",
			ClaudeEval: "10,11,12",
			Warn:       "13,14,15",
			Error:      "16,17,18",
			Signal:     "19,20,21",
			Timestamp:  "22,23,24",
			Info:       "25,26,27",
		}
		dst.mergeFrom(src)

		assert.Equal(t, "1,2,3", dst.Task)
		assert.Equal(t, "4,5,6", dst.Review)
		assert.Equal(t, "7,8,9", dst.Codex)
		assert.Equal(t, "10,11,12", dst.ClaudeEval)
		assert.Equal(t, "13,14,15", dst.Warn)
		assert.Equal(t, "16,17,18", dst.Error)
		assert.Equal(t, "19,20,21", dst.Signal)
		assert.Equal(t, "22,23,24", dst.Timestamp)
		assert.Equal(t, "25,26,27", dst.Info)
	})
}

func TestColorLoader_parseColorsFromFile_PermissionDenied(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config")
	require.NoError(t, os.WriteFile(configPath, []byte("color_task = #ff0000"), 0o600))

	// remove read permission
	require.NoError(t, os.Chmod(configPath, 0o000))
	t.Cleanup(func() { _ = os.Chmod(configPath, 0o600) })

	cl := &colorLoader{embedFS: DefaultsFS()}
	_, err := cl.parseColorsFromFile(configPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read config")
}
