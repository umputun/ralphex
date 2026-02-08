package config

// ClaudeModels holds per-phase model configuration for Claude CLI.
type ClaudeModels struct {
	Task   string `json:"task"`   // model for task execution phase
	Review string `json:"review"` // model for review phases (first, second, codex eval)
	Plan   string `json:"plan"`   // model for plan creation phase
}
