package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	requestRefHashLen = 8
	requestRefMaxLen  = 50
)

var requestRefDashRegex = regexp.MustCompile(`-{2,}`)

// Request represents a plan request resolved from CLI input.
type Request struct {
	Text string
	Ref  string
	File string
}

// ResolveRequest converts CLI input into a plan request.
// supports plain text, @file for file-backed input, and @@ for a literal leading @.
func ResolveRequest(input string) (Request, error) {
	switch {
	case input == "":
		return Request{}, nil
	case strings.HasPrefix(input, "@@"):
		literal := input[1:]
		return Request{Text: literal, Ref: literal}, nil
	case strings.HasPrefix(input, "@"):
		path := strings.TrimPrefix(input, "@")
		absPath, err := filepath.Abs(path)
		if err != nil {
			return Request{}, fmt.Errorf("resolve plan request file: %w", err)
		}
		content, err := os.ReadFile(absPath) //nolint:gosec // user explicitly provided request file path via CLI
		if err != nil {
			return Request{}, fmt.Errorf("read plan request file %s: %w", absPath, err)
		}
		text := strings.TrimSpace(string(content))
		if text == "" {
			return Request{}, fmt.Errorf("plan request file is empty: %s", absPath)
		}
		ref := fileRequestRef(absPath)
		return Request{Text: text, Ref: ref, File: absPath}, nil
	default:
		return Request{Text: input, Ref: input}, nil
	}
}

func fileRequestRef(absPath string) string {
	stem := strings.TrimSuffix(filepath.Base(absPath), filepath.Ext(absPath))
	if stem == "" {
		stem = filepath.Base(absPath)
	}

	stem = sanitizeRequestRefStem(stem)
	hash := shortPathHash(absPath)
	maxStemLen := max(requestRefMaxLen-len(hash)-1, 0)
	if len(stem) > maxStemLen {
		stem = strings.Trim(stem[:maxStemLen], "-")
	}
	if stem == "" {
		return hash
	}
	return stem + "-" + hash
}

func sanitizeRequestRefStem(input string) string {
	result := strings.ToLower(strings.ReplaceAll(input, " ", "-"))

	var clean strings.Builder
	for _, r := range result {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			clean.WriteRune(r)
		}
	}

	result = requestRefDashRegex.ReplaceAllString(clean.String(), "-")
	return strings.Trim(result, "-")
}

func shortPathHash(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:])[:requestRefHashLen]
}
