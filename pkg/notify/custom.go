package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

// customChannel runs a user script for notifications.
type customChannel struct {
	scriptPath string
}

// newCustomChannel creates a new custom notification channel with the given script path.
func newCustomChannel(scriptPath string) *customChannel {
	return &customChannel{scriptPath: scriptPath}
}

// send marshals Result to JSON and pipes it to the script's stdin.
func (c *customChannel) send(ctx context.Context, r Result) error {
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}

	cmd := exec.CommandContext(ctx, c.scriptPath) //nolint:gosec // path comes from user config, not user input
	cmd.Stdin = bytes.NewReader(data)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err = cmd.Run(); err != nil {
		combined := stderr.String() + stdout.String()
		if combined != "" {
			return fmt.Errorf("script %s: %w, output: %s", c.scriptPath, err, combined)
		}
		return fmt.Errorf("script %s: %w", c.scriptPath, err)
	}
	return nil
}
