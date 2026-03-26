package executor

import (
	"encoding/json"
	"strings"
)

// extractUsageFromText tries to parse usage from JSON text.
// supports both full JSON objects and line-delimited JSON.
func extractUsageFromText(text string) Usage {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return Usage{}
	}

	if usage, ok := usageFromJSON(trimmed); ok {
		return usage
	}

	// fallback for NDJSON / mixed logs
	var out Usage
	for line := range strings.SplitSeq(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if usage, ok := usageFromJSON(line); ok {
			out = out.Merge(usage)
		}
	}
	return out
}

func usageFromJSON(s string) (Usage, bool) {
	var obj struct {
		Usage struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			PromptTokens             int `json:"prompt_tokens"`
			CompletionTokens         int `json:"completion_tokens"`
			TotalTokens              int `json:"total_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
		Result struct {
			Usage struct {
				InputTokens              int `json:"input_tokens"`
				OutputTokens             int `json:"output_tokens"`
				PromptTokens             int `json:"prompt_tokens"`
				CompletionTokens         int `json:"completion_tokens"`
				TotalTokens              int `json:"total_tokens"`
				CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
				CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			} `json:"usage"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(s), &obj); err != nil {
		return Usage{}, false
	}

	usage := usageFromCommonFields(
		obj.Usage.InputTokens,
		obj.Usage.OutputTokens,
		obj.Usage.PromptTokens,
		obj.Usage.CompletionTokens,
		obj.Usage.TotalTokens,
		obj.Usage.CacheReadInputTokens,
		obj.Usage.CacheCreationInputTokens,
	)

	resultUsage := usageFromCommonFields(
		obj.Result.Usage.InputTokens,
		obj.Result.Usage.OutputTokens,
		obj.Result.Usage.PromptTokens,
		obj.Result.Usage.CompletionTokens,
		obj.Result.Usage.TotalTokens,
		obj.Result.Usage.CacheReadInputTokens,
		obj.Result.Usage.CacheCreationInputTokens,
	)
	usage = usage.Merge(resultUsage)
	return usage, !usage.Empty()
}
