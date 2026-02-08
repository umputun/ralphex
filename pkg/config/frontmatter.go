package config

import "strings"

// parseFrontmatter extracts model and agent type from YAML-like frontmatter.
// format: ---\nkey: value\n---\nbody
// returns model, agentType, body. if no frontmatter, returns empty strings and original content.
func parseFrontmatter(content string) (model, agentType, body string) {
	if !strings.HasPrefix(content, "---\n") {
		return "", "", content
	}

	end := strings.Index(content[4:], "\n---")
	if end == -1 {
		return "", "", content
	}

	header := content[4 : 4+end]
	body = strings.TrimSpace(content[4+end+4:])

	for line := range strings.SplitSeq(header, "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "model":
			model = strings.TrimSpace(val)
		case "agent":
			agentType = strings.TrimSpace(val)
		}
	}

	return model, agentType, body
}
