package agentadapter

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Explicit operator opt-in: a real Claude child runs in the immutable Docker
// sandbox and spends a small amount of gateway credit. Provider APIs are never
// contacted; the checkout and resulting file are disposable.
func TestDockerClaudeSandboxLiveCoding(t *testing.T) {
	if os.Getenv("OPENREVIEW_TEST_DOCKER_CLAUDE_LIVE") != "1" || os.Geteuid() != 0 {
		t.Skip("requires opt-in root test container with dedicated Docker resources")
	}
	root := os.Getenv("OPENREVIEW_TEST_DOCKER_WORKSPACE_ROOT")
	workspace, err := os.MkdirTemp(root, "agent-task-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workspace)
	if output, err := exec.Command("git", "-C", workspace, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("initialize disposable checkout: %v (%s)", err, output)
	}
	pipeline := Pipeline{
		WorkspaceRoot: root, ExecutorKind: "claude",
		ExecutorIdentityPool: mustClaudeTestIdentityPool(t),
		DockerSandbox: &DockerSandboxConfig{
			DockerBinary: "docker", ImageID: os.Getenv("OPENREVIEW_TEST_DOCKER_IMAGE_ID"),
			InternalNetwork: os.Getenv("OPENREVIEW_TEST_DOCKER_NETWORK"),
			WorkspaceVolume: os.Getenv("OPENREVIEW_TEST_DOCKER_VOLUME"),
			AdapterHost:     os.Getenv("OPENREVIEW_TEST_DOCKER_ADAPTER_HOST"),
		},
		ClaudeModelBroker: ModelBrokerConfig{
			APIBaseURL: os.Getenv("AGENT_ADAPTER_CLAUDE_MODEL_API_BASE_URL"),
			APIKey:     os.Getenv("AGENT_ADAPTER_CLAUDE_MODEL_API_KEY"),
			Model:      os.Getenv("AGENT_ADAPTER_CLAUDE_MODEL"), WireAPI: "anthropic",
		},
	}
	var childOutput string
	pipeline.executorOutput = func(value string) { childOutput = value }
	if err := pipeline.ClaudeModelBroker.valid(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	submission := Submission{}
	submission.Plan.Summary = "Create a file named sandbox-result in the checkout root containing exactly sandbox-ok followed by one newline. Do not change any other file. Before writing, use Bash to check that /var/run/docker.sock and /state are absent, and AGENT_ADAPTER_CLAUDE_MODEL_API_KEY is unset. If any check fails, stop without writing."
	if err := pipeline.runAgent(ctx, workspace, submission); err != nil {
		t.Fatalf("isolated Claude coding run failed: %v; child output=%q", err, childOutput)
	}
	result, err := os.ReadFile(filepath.Join(workspace, "sandbox-result"))
	if err != nil || string(result) != "sandbox-ok\n" {
		t.Fatalf("isolated Claude did not write the expected result: %v (%q); child output=%q", err, result, childOutput)
	}
	info, err := os.Stat(workspace)
	if err != nil || info.Sys().(*syscall.Stat_t).Uid != 0 || info.Mode().Perm() != 0o700 {
		t.Fatalf("trusted adapter did not reclaim checkout ownership: %v", err)
	}
	leftovers, err := pipeline.dockerOutput(context.Background(), "ps", "-a", "--filter", "label="+sandboxOwnerLabel+"="+pipeline.DockerSandbox.WorkspaceVolume, "--format", "{{.Names}}")
	if err != nil || strings.TrimSpace(leftovers) != "" {
		t.Fatalf("Claude child cleanup incomplete: %v (%s)", err, leftovers)
	}
}

func mustClaudeTestIdentityPool(t *testing.T) *ExecutorIdentityPool {
	t.Helper()
	pool, err := NewExecutorIdentityPool(10002, 1)
	if err != nil {
		t.Fatal(err)
	}
	return pool
}
