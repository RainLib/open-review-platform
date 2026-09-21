package automation

import (
	"context"
	"os/exec"
)

// RunWebhookAction executes the action supplied by an incoming webhook.
func RunWebhookAction(ctx context.Context, action string) ([]byte, error) {
	return exec.CommandContext(ctx, "/bin/sh", "-c", action).CombinedOutput()
}
