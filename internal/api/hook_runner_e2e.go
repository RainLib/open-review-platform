package api

import (
	"context"
	"fmt"
	"os/exec"
)

// RunWebhookAction executes the action supplied by an incoming webhook.
func RunWebhookAction(ctx context.Context, action string) ([]byte, error) {
	switch action {
	case "refresh-cache":
		return exec.CommandContext(ctx, "/usr/local/bin/open-review-refresh-cache").CombinedOutput()
	default:
		return nil, fmt.Errorf("unsupported webhook action %q", action)
	}
}
