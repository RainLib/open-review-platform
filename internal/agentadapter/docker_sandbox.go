package agentadapter

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// DockerSandboxConfig is deployment-owned. The fixed image contains only the
// coding CLI; the privileged adapter retains provider and model credentials.
// Run this profile only on a dedicated Docker host: access to the daemon
// socket gives the adapter host-level authority, never the sandbox child.
type DockerSandboxConfig struct {
	DockerBinary    string
	ImageID         string
	InternalNetwork string
	WorkspaceVolume string
	AdapterHost     string
}

const sandboxOwnerLabel = "openreview.agent_adapter.workspace_volume"

const minSandboxWorkspaceFreeBytes uint64 = 1 << 30

func (config DockerSandboxConfig) valid(workspaceRoot string) error {
	if len(config.ImageID) != len("sha256:")+64 || !strings.HasPrefix(config.ImageID, "sha256:") ||
		!isLowerHex(strings.TrimPrefix(config.ImageID, "sha256:")) {
		return fmt.Errorf("container sandbox requires an immutable sha256 image ID")
	}
	if !sandboxIdentifier(config.InternalNetwork) || !sandboxIdentifier(config.WorkspaceVolume) || !sandboxIdentifier(config.AdapterHost) {
		return fmt.Errorf("container sandbox network, volume, or adapter host is invalid")
	}
	if !filepath.IsAbs(workspaceRoot) || workspaceRoot == "/" || filepath.Clean(workspaceRoot) != workspaceRoot {
		return fmt.Errorf("container sandbox workspace root is invalid")
	}
	return nil
}

func isLowerHex(value string) bool {
	for _, char := range value {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return false
		}
	}
	return value != ""
}

// CheckSandboxReady fails before the adapter accepts a task when the named
// network is egress-capable, the workspace volume is missing, or the fixed
// image ID cannot be resolved locally. It is repeated immediately before a
// child starts in case an operator changes Docker resources at runtime.
func (pipeline Pipeline) CheckSandboxReady(ctx context.Context) error {
	if err := pipeline.CheckSandboxResourcesReady(ctx); err != nil {
		return err
	}
	if pipeline.DockerSandbox == nil {
		return nil
	}
	// The private checkout and Docker child share this volume in the supported
	// local deployment. Refuse new work before Codex hits ENOSPC after a start
	// claim has been consumed. Remote Docker storage still needs its own alert.
	var filesystem syscall.Statfs_t
	if err := syscall.Statfs(pipeline.WorkspaceRoot, &filesystem); err != nil || filesystem.Bsize <= 0 {
		return fmt.Errorf("container sandbox workspace capacity is unavailable")
	}
	return requireSandboxWorkspaceHeadroom(uint64(filesystem.Bavail) * uint64(filesystem.Bsize))
}

// CheckSandboxResourcesReady verifies the sandbox identity without requiring
// free capacity. Recovery uses it so low disk space cannot prevent removal of
// orphan children and their workspaces.
func (pipeline Pipeline) CheckSandboxResourcesReady(ctx context.Context) error {
	config := pipeline.DockerSandbox
	if config == nil {
		return nil
	}
	if err := config.valid(pipeline.WorkspaceRoot); err != nil {
		return err
	}
	root, err := os.Lstat(pipeline.WorkspaceRoot)
	if err != nil || !root.IsDir() {
		return fmt.Errorf("container sandbox workspace root must be an exact directory")
	}
	// Lstat rejects a symlink at the leaf only. A symlink in any parent would
	// still redirect recovery's owned-workspace cleanup outside this volume.
	resolved, err := filepath.EvalSymlinks(pipeline.WorkspaceRoot)
	if err != nil || resolved != pipeline.WorkspaceRoot {
		return fmt.Errorf("container sandbox workspace root has a symlinked parent")
	}
	network, err := pipeline.dockerOutput(ctx, "network", "inspect", "--format", "{{.Internal}}", config.InternalNetwork)
	if err != nil || strings.TrimSpace(network) != "true" {
		return fmt.Errorf("container sandbox requires an existing internal-only network")
	}
	volume, err := pipeline.dockerOutput(ctx, "volume", "inspect", "--format", "{{.Name}}", config.WorkspaceVolume)
	if err != nil || strings.TrimSpace(volume) != config.WorkspaceVolume {
		return fmt.Errorf("container sandbox workspace volume is unavailable")
	}
	image, err := pipeline.dockerOutput(ctx, "image", "inspect", "--format", "{{.Id}}", config.ImageID)
	if err != nil || strings.TrimSpace(image) != config.ImageID {
		return fmt.Errorf("container sandbox immutable image is unavailable")
	}
	return nil
}

func requireSandboxWorkspaceHeadroom(available uint64) error {
	if available < minSandboxWorkspaceFreeBytes {
		return fmt.Errorf("container sandbox workspace has less than 1 GiB available")
	}
	return nil
}

// CleanupOrphanSandboxes runs only after the shared workspace lock and the
// single-instance receipt lock have been acquired, before accepting new work.
// A process crash can leave a
// child alive even though its short-lived model broker is gone; on recovery we
// must stop that child before reporting the interrupted attempt upstream.
func (pipeline Pipeline) CleanupOrphanSandboxes(ctx context.Context, lock *WorkspaceLock) error {
	config := pipeline.DockerSandbox
	if config == nil {
		return nil
	}
	if !lock.owns(pipeline.WorkspaceRoot) {
		return fmt.Errorf("adapter workspace cleanup requires its exclusive volume lock")
	}
	if err := pipeline.CheckSandboxResourcesReady(ctx); err != nil {
		return err
	}
	listed, err := pipeline.dockerOutput(ctx, "ps", "-a", "--filter", "label="+sandboxOwnerLabel+"="+config.WorkspaceVolume, "--format", "{{.Names}}")
	if err != nil {
		return fmt.Errorf("list owned sandbox containers: %w", err)
	}
	for _, name := range strings.Fields(listed) {
		if !validSandboxContainerName(name) {
			return fmt.Errorf("owned sandbox container has an unexpected identity")
		}
		if _, err := pipeline.dockerOutput(ctx, "rm", "--force", name); err != nil {
			return fmt.Errorf("remove interrupted sandbox container: %w", err)
		}
	}
	// No child may still write the private checkout volume after the owned
	// containers have been removed. Remove only directories created by our
	// MkdirTemp("agent-task-") call; never sweep arbitrary volume contents.
	entries, err := os.ReadDir(pipeline.WorkspaceRoot)
	if err != nil {
		return fmt.Errorf("inspect interrupted sandbox workspaces: %w", err)
	}
	for _, entry := range entries {
		if !validAgentWorkspaceName(entry.Name()) {
			continue
		}
		path := filepath.Join(pipeline.WorkspaceRoot, entry.Name())
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("interrupted sandbox workspace is not an exact directory")
		}
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove interrupted sandbox workspace: %w", err)
		}
	}
	return nil
}

func validSandboxContainerName(name string) bool {
	const prefix = "openreview-agent-"
	return strings.HasPrefix(name, prefix) && len(name) == len(prefix)+24 && isLowerHex(strings.TrimPrefix(name, prefix))
}

func validAgentWorkspaceName(name string) bool {
	const prefix = "agent-task-"
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	suffix := strings.TrimPrefix(name, prefix)
	if len(suffix) == 0 || len(suffix) > 10 {
		return false
	}
	for _, digit := range suffix {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

func sandboxIdentifier(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' || char == '.' {
			continue
		}
		return false
	}
	return true
}

func (pipeline Pipeline) runAgentInDocker(ctx context.Context, workspace, home, prompt string, identity executorIdentity, broker *modelBroker) (resultErr error) {
	config := pipeline.DockerSandbox
	if config == nil || broker == nil {
		return fmt.Errorf("container sandbox is unavailable")
	}
	if err := config.valid(pipeline.WorkspaceRoot); err != nil {
		return err
	}
	relative, err := filepath.Rel(pipeline.WorkspaceRoot, workspace)
	if err != nil || relative == "." || strings.HasPrefix(relative, "..") || filepath.Base(relative) != relative {
		return fmt.Errorf("container sandbox workspace is outside the private root")
	}
	if actual, err := filepath.EvalSymlinks(workspace); err != nil || actual != workspace {
		return fmt.Errorf("container sandbox workspace is not an exact directory")
	}
	deadline, ok := ctx.Deadline()
	if !ok || !deadline.After(time.Now()) {
		return fmt.Errorf("container sandbox requires an unexpired deadline")
	}
	seconds := int(time.Until(deadline).Seconds())
	if seconds < 1 || seconds > 120*60 {
		return fmt.Errorf("container sandbox deadline is outside the bounded execution window")
	}
	// A child may reach only the adapter's capability-guarded model broker on
	// this Docker-internal network. Never start untrusted code after a resource
	// replacement changes the network or image that passed startup preflight.
	if err := pipeline.CheckSandboxReady(ctx); err != nil {
		return err
	}
	if err := transferExecutorWorkspace(workspace, identity); err != nil {
		_ = reclaimExecutorWorkspace(workspace)
		return err
	}
	defer func() {
		if err := reclaimExecutorWorkspace(workspace); err != nil {
			resultErr = fmt.Errorf("reclaim sandbox workspace before publication: %w", err)
		}
	}()
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return fmt.Errorf("create sandbox identity: %w", err)
	}
	name := "openreview-agent-" + hex.EncodeToString(random)
	args := []string{
		"run", "--interactive", "--pull", "never", "--name", name,
		"--label", sandboxOwnerLabel + "=" + config.WorkspaceVolume,
		"--network", config.InternalNetwork, "--read-only", "--cap-drop", "ALL",
		"--security-opt", "no-new-privileges:true", "--pids-limit", "256",
		"--memory", "4g", "--cpus", "2", "--user", fmt.Sprintf("%d:%d", identity.uid, identity.gid),
		"--mount", "type=volume,src=" + config.WorkspaceVolume + ",dst=" + pipeline.WorkspaceRoot,
		"--tmpfs", "/tmp:rw,nosuid,size=268435456", "--workdir", workspace,
		"--env", "HOME=" + home,
	}
	if pipeline.ExecutorKind == "claude" {
		args = append(args,
			"--env", "ANTHROPIC_BASE_URL="+strings.TrimSuffix(broker.URL(), "/v1"),
			"--env", "ANTHROPIC_API_KEY="+broker.token,
			"--env", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
			"--env", "SHELL=/bin/bash", "--env", "CLAUDE_CODE_SHELL=/bin/bash",
		)
		args = append(args, config.ImageID, "timeout", "-k", "10s", strconv.Itoa(seconds)+"s", "claude")
		args = append(args, claudeExecutorArgs(pipeline.ClaudeModelBroker.Model, true)...)
	} else {
		args = append(args,
			"--env", "CODEX_HOME="+home,
			"--env", "OPENREVIEW_MODEL_CAPABILITY="+broker.token,
			config.ImageID, "timeout", "-k", "10s", strconv.Itoa(seconds)+"s", "codex",
		)
		args = append(args, codexExecutorArgs()...)
	}
	command := exec.CommandContext(ctx, config.DockerBinary, args...)
	command.Stdin = strings.NewReader(prompt)
	command.Env = dockerClientEnvironment()
	if err := configureExecutorProcessGroup(command); err != nil {
		return err
	}
	output := &boundedProcessOutput{command: command, limit: maxExecutorOutputBytes, capture: pipeline.executorOutput != nil}
	command.Stdout, command.Stderr = output, output
	runErr := command.Run()
	_ = stopExecutorProcessGroup(command)
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, cleanupErr := pipeline.dockerOutput(cleanupCtx, "rm", "--force", name)
	if cleanupErr != nil {
		return fmt.Errorf("container sandbox cleanup could not be verified: %w", cleanupErr)
	}
	captured, count, exceeded := output.snapshot()
	if pipeline.executorOutput != nil {
		pipeline.executorOutput(captured)
	}
	if exceeded {
		return fmt.Errorf("container sandbox output exceeded %d-byte budget", maxExecutorOutputBytes)
	}
	if runErr != nil {
		return fmt.Errorf("container sandbox failed (%d bytes output): %w", count, runErr)
	}
	return nil
}

func (pipeline Pipeline) dockerOutput(ctx context.Context, args ...string) (string, error) {
	command := exec.CommandContext(ctx, pipeline.DockerSandbox.DockerBinary, args...)
	command.Env = dockerClientEnvironment()
	output, err := command.Output()
	if err != nil || len(output) > 4096 {
		return "", fmt.Errorf("sandbox daemon request failed")
	}
	return string(output), nil
}

func dockerClientEnvironment() []string {
	path := os.Getenv("PATH")
	if path == "" {
		path = "/usr/local/bin:/usr/bin:/bin"
	}
	return []string{"PATH=" + path, "HOME=/nonexistent", "DOCKER_HOST=unix:///var/run/docker.sock", "DOCKER_CONFIG=/nonexistent"}
}
