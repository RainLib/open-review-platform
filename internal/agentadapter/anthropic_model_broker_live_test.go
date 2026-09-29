package agentadapter

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This opt-in acceptance probe spends real model tokens, but has no repository
// checkout or provider credential. It verifies that the configured Claude CLI
// can use the job-scoped broker to make a bounded local edit.
func TestLiveClaudeCLIEditThroughJobBroker(t *testing.T) {
	baseURL := os.Getenv("OPENREVIEW_TEST_CLAUDE_API_BASE_URL")
	apiKey := os.Getenv("OPENREVIEW_TEST_CLAUDE_API_KEY")
	model := os.Getenv("OPENREVIEW_TEST_CLAUDE_MODEL")
	if baseURL == "" || apiKey == "" || model == "" {
		t.Skip("set the opt-in Claude broker test endpoint, key, and model")
	}
	binary, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("Claude CLI is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	broker, err := startModelBroker(ctx, ModelBrokerConfig{APIBaseURL: baseURL, APIKey: apiKey, Model: model, WireAPI: "anthropic"})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	workspace := t.TempDir()
	home := filepath.Join(workspace, ".openreview-agent-home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, binary, claudeExecutorArgs(model, true)...)
	command.Dir = workspace
	command.Stdin = strings.NewReader("Create docs/agent-smoke-20260927.md with a short title and the exact text Issue-to-Draft verification. Do not change another file.")
	command.Env = append(isolatedExecutorEnvironment(home, os.Environ()),
		"ANTHROPIC_BASE_URL="+strings.TrimSuffix(broker.URL(), "/v1"),
		"ANTHROPIC_API_KEY="+broker.token,
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Claude CLI broker edit failed: %v; output=%q", err, string(output))
	}
	content, err := os.ReadFile(filepath.Join(workspace, "docs", "agent-smoke-20260927.md"))
	if err != nil || !strings.Contains(string(content), "Issue-to-Draft verification") {
		t.Fatalf("Claude CLI did not produce the requested file: %v; output=%q", err, string(output))
	}
}
