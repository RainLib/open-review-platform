package agentadapter

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestDockerSandboxPreflightRequiresImmutableImageAndInternalNetwork(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "docker-fixture")
	script := `#!/bin/sh
set -eu
case "$1:$2" in
  network:inspect) printf '%s\n' "$NETWORK_INTERNAL" ;;
  volume:inspect) printf '%s\n' 'agent-workspaces' ;;
  image:inspect) printf '%s\n' "$IMAGE_ID" ;;
  *) exit 2 ;;
esac
`
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	imageID := "sha256:" + strings.Repeat("a", 64)
	config := &DockerSandboxConfig{DockerBinary: binary, ImageID: imageID, InternalNetwork: "agent-internal", WorkspaceVolume: "agent-workspaces", AdapterHost: "agent-adapter"}
	pipeline := Pipeline{WorkspaceRoot: root, DockerSandbox: config}
	// The Docker client intentionally receives only a scrubbed environment, so
	// the fixture reads its expected values from immutable script constants.
	script = strings.ReplaceAll(script, "$NETWORK_INTERNAL", "true")
	script = strings.ReplaceAll(script, "$IMAGE_ID", imageID)
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := pipeline.CheckSandboxReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := pipeline.CheckSandboxResourcesReady(context.Background()); err != nil {
		t.Fatalf("recovery resource preflight failed: %v", err)
	}
	config.ImageID = "latest"
	if err := pipeline.CheckSandboxReady(context.Background()); err == nil {
		t.Fatal("mutable sandbox image was accepted")
	}
	config.ImageID = imageID
	badNetwork := strings.Replace(script, `network:inspect) printf '%s\n' "true"`, `network:inspect) printf '%s\n' "false"`, 1)
	if badNetwork == script {
		t.Fatal("network fixture was not changed")
	}
	if err := os.WriteFile(binary, []byte(badNetwork), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := pipeline.CheckSandboxReady(context.Background()); err == nil {
		t.Fatal("egress-capable Docker network was accepted")
	}
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "workspaces")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	pipeline.WorkspaceRoot = link
	if err := pipeline.CheckSandboxReady(context.Background()); err == nil {
		t.Fatal("symlinked sandbox workspace root was accepted")
	}
	parent := filepath.Join(t.TempDir(), "parent")
	if err := os.Symlink(filepath.Dir(root), parent); err != nil {
		t.Fatal(err)
	}
	pipeline.WorkspaceRoot = filepath.Join(parent, filepath.Base(root))
	if err := pipeline.CheckSandboxResourcesReady(context.Background()); err == nil {
		t.Fatal("sandbox workspace with a symlinked parent was accepted for recovery")
	}
}

func TestDockerSandboxRequiresWorkspaceHeadroomBeforeStartingChild(t *testing.T) {
	for _, available := range []uint64{0, minSandboxWorkspaceFreeBytes - 1} {
		if err := requireSandboxWorkspaceHeadroom(available); err == nil {
			t.Fatalf("workspace with %d free bytes was accepted", available)
		}
	}
	if err := requireSandboxWorkspaceHeadroom(minSandboxWorkspaceFreeBytes); err != nil {
		t.Fatalf("workspace at the minimum headroom was rejected: %v", err)
	}
}

func TestDockerSandboxRejectsUntrustedResourceNames(t *testing.T) {
	imageID := "sha256:" + strings.Repeat("a", 64)
	base := DockerSandboxConfig{ImageID: imageID, InternalNetwork: "agent-internal", WorkspaceVolume: "agent-workspaces", AdapterHost: "agent-adapter"}
	for _, changed := range []DockerSandboxConfig{
		{ImageID: "alpine:latest", InternalNetwork: base.InternalNetwork, WorkspaceVolume: base.WorkspaceVolume, AdapterHost: base.AdapterHost},
		{ImageID: imageID, InternalNetwork: "agent;curl attacker", WorkspaceVolume: base.WorkspaceVolume, AdapterHost: base.AdapterHost},
		{ImageID: imageID, InternalNetwork: base.InternalNetwork, WorkspaceVolume: "../state", AdapterHost: base.AdapterHost},
		{ImageID: imageID, InternalNetwork: base.InternalNetwork, WorkspaceVolume: base.WorkspaceVolume, AdapterHost: "host/path"},
	} {
		if err := changed.valid("/workspaces"); err == nil {
			t.Fatalf("invalid sandbox resource was accepted: %+v", changed)
		}
	}
}

func TestDockerSandboxContainerNameValidation(t *testing.T) {
	for _, name := range []string{"openreview-agent-" + strings.Repeat("a", 24), "openreview-agent-" + strings.Repeat("0", 24)} {
		if !validSandboxContainerName(name) {
			t.Fatalf("valid child identity rejected: %q", name)
		}
	}
	for _, name := range []string{"other-agent-" + strings.Repeat("a", 24), "openreview-agent-" + strings.Repeat("G", 24), "openreview-agent-" + strings.Repeat("a", 23)} {
		if validSandboxContainerName(name) {
			t.Fatalf("unowned child identity accepted: %q", name)
		}
	}
}

func TestDockerSandboxRecoveryOnlyRemovesGeneratedWorkspaces(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	imageID := "sha256:" + strings.Repeat("a", 64)
	binary := filepath.Join(root, "docker-fixture")
	script := `#!/bin/sh
set -eu
case "$1:$2" in
  network:inspect) printf 'true\n' ;;
  volume:inspect) printf 'agent-workspaces\n' ;;
  image:inspect) printf '%s\n' '` + imageID + `' ;;
  ps:-a) exit 0 ;;
  *) exit 2 ;;
esac
`
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	pipeline := Pipeline{WorkspaceRoot: root, DockerSandbox: &DockerSandboxConfig{
		DockerBinary: binary, ImageID: imageID, InternalNetwork: "agent-internal", WorkspaceVolume: "agent-workspaces", AdapterHost: "agent-adapter",
	}}
	lock, err := LockWorkspaceRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	stale, err := os.MkdirTemp(root, "agent-task-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "source.go"), []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := pipeline.CleanupOrphanSandboxes(context.Background(), nil); err == nil {
		t.Fatal("orphan cleanup ran without exclusive workspace ownership")
	}
	if _, err := os.Lstat(stale); err != nil {
		t.Fatalf("unchecked cleanup changed a checkout: %v", err)
	}
	keep := filepath.Join(root, "agent-task-not-generated")
	if err := os.Mkdir(keep, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := pipeline.CleanupOrphanSandboxes(context.Background(), lock); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(stale); !os.IsNotExist(err) {
		t.Fatalf("generated crash checkout was retained: %v", err)
	}
	if info, err := os.Lstat(keep); err != nil || !info.IsDir() {
		t.Fatalf("unrelated volume entry was removed: %v", err)
	}
	external := t.TempDir()
	link := filepath.Join(root, "agent-task-12345")
	if err := os.Symlink(external, link); err != nil {
		t.Fatal(err)
	}
	if err := pipeline.CleanupOrphanSandboxes(context.Background(), lock); err == nil {
		t.Fatal("symlink disguised as a generated checkout was accepted")
	}
	if info, err := os.Lstat(external); err != nil || !info.IsDir() {
		t.Fatalf("symlink target was changed: %v", err)
	}
}

// A crash leaves a child attached to Docker, not to the old adapter process.
// Recovery is tested against a disposable real container, without a model or
// provider credential. The test is opt-in for the isolated Linux acceptance.
func TestDockerSandboxRecoveryRemovesOwnedChild(t *testing.T) {
	if os.Getenv("OPENREVIEW_TEST_DOCKER_SANDBOX") != "1" || os.Geteuid() != 0 {
		t.Skip("requires opt-in disposable Linux Docker sandbox acceptance")
	}
	config := &DockerSandboxConfig{
		DockerBinary: "docker", ImageID: os.Getenv("OPENREVIEW_TEST_DOCKER_IMAGE_ID"),
		InternalNetwork: os.Getenv("OPENREVIEW_TEST_DOCKER_NETWORK"), WorkspaceVolume: os.Getenv("OPENREVIEW_TEST_DOCKER_VOLUME"),
		AdapterHost: os.Getenv("OPENREVIEW_TEST_DOCKER_ADAPTER_HOST"),
	}
	pipeline := Pipeline{WorkspaceRoot: os.Getenv("OPENREVIEW_TEST_DOCKER_WORKSPACE_ROOT"), DockerSandbox: config}
	lock, err := LockWorkspaceRoot(pipeline.WorkspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	name := "openreview-agent-" + hex.EncodeToString(random)
	stale, err := os.MkdirTemp(pipeline.WorkspaceRoot, "agent-task-")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("docker", "run", "--detach", "--pull", "never", "--name", name,
		"--label", sandboxOwnerLabel+"="+config.WorkspaceVolume, "--network", config.InternalNetwork,
		config.ImageID, "timeout", "60s", "sleep", "60")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create disposable interrupted child: %v (%s)", err, output)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pipeline.dockerOutput(cleanupCtx, "rm", "--force", name)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := pipeline.CleanupOrphanSandboxes(ctx, lock); err != nil {
		t.Fatal(err)
	}
	if _, err := pipeline.dockerOutput(ctx, "container", "inspect", "--format", "{{.Id}}", name); err == nil {
		t.Fatal("interrupted sandbox container still exists after adapter recovery")
	}
	if _, err := os.Lstat(stale); !os.IsNotExist(err) {
		t.Fatalf("interrupted sandbox workspace still exists after adapter recovery: %v", err)
	}
}

// Opt-in, Linux-only acceptance: the adapter test process shares only a named
// workspace volume and internal Docker network with the child. The upstream
// model is a TLS fixture, so this spends no model tokens or provider writes.
func TestDockerSandboxRunsCodexWithoutAdapterCredentials(t *testing.T) {
	if os.Getenv("OPENREVIEW_TEST_DOCKER_SANDBOX") != "1" {
		t.Skip("set OPENREVIEW_TEST_DOCKER_SANDBOX=1 for per-job container acceptance")
	}
	if os.Geteuid() != 0 {
		t.Skip("requires the trusted adapter root identity in a disposable Linux container")
	}
	root := os.Getenv("OPENREVIEW_TEST_DOCKER_WORKSPACE_ROOT")
	config := &DockerSandboxConfig{
		DockerBinary: "docker", ImageID: os.Getenv("OPENREVIEW_TEST_DOCKER_IMAGE_ID"),
		InternalNetwork: os.Getenv("OPENREVIEW_TEST_DOCKER_NETWORK"), WorkspaceVolume: os.Getenv("OPENREVIEW_TEST_DOCKER_VOLUME"),
		AdapterHost: os.Getenv("OPENREVIEW_TEST_DOCKER_ADAPTER_HOST"),
	}
	workspace, err := os.MkdirTemp(root, "agent-task-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workspace)
	if output, err := exec.Command("git", "-C", workspace, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("create disposable sandbox checkout: %v (%s)", err, output)
	}
	var calls atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/responses" || request.Header.Get("Authorization") != "Bearer synthetic-upstream-key" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		var fields map[string]any
		if err := json.NewDecoder(request.Body).Decode(&fields); err != nil || fields["store"] != false {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		index := calls.Add(1)
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.Header().Set("Cache-Control", "no-cache")
		if index == 1 {
			command := `test ! -e /var/run/docker.sock && test ! -e /state && test -z "${AGENT_ADAPTER_GITHUB_TOKEN+x}" && test -z "${AGENT_ADAPTER_CODEX_MODEL_API_KEY+x}" && test -z "${AGENT_TASK_ADAPTER_SECRET+x}" && printf 'sandbox-ok\n' > sandbox-result`
			arguments, _ := json.Marshal(map[string]string{"cmd": command})
			item := map[string]any{"id": "fc_sandbox", "type": "function_call", "name": "exec_command", "call_id": "call_sandbox", "arguments": string(arguments)}
			writeSyntheticResponseEvent(writer, "response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item})
			writeSyntheticResponseEvent(writer, "response.completed", syntheticCompletedResponse("resp_sandbox_1", fields["model"], []any{item}))
		} else {
			item := map[string]any{"id": "msg_sandbox", "type": "message", "status": "completed", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Done.", "annotations": []any{}}}}
			writeSyntheticResponseEvent(writer, "response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item})
			writeSyntheticResponseEvent(writer, "response.completed", syntheticCompletedResponse("resp_sandbox_2", fields["model"], []any{item}))
		}
	}))
	defer upstream.Close()
	pool, err := NewExecutorIdentityPool(10002, 1)
	if err != nil {
		t.Fatal(err)
	}
	pipeline := Pipeline{
		WorkspaceRoot: root, ExecutorKind: "codex", ExecutorIdentityPool: pool, DockerSandbox: config,
		CodeModelBroker: ModelBrokerConfig{APIBaseURL: upstream.URL + "/v1", APIKey: "synthetic-upstream-key", Model: "gpt-6-sol", HTTPClient: upstream.Client()},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	if err := pipeline.runAgent(ctx, workspace, Submission{}); err != nil {
		t.Fatalf("isolated Codex did not finish its synthetic two-turn task: %v (upstream calls=%d)", err, calls.Load())
	}
	if calls.Load() < 2 {
		t.Fatalf("sandbox did not replay a tool result: upstream calls=%d", calls.Load())
	}
	result, err := os.ReadFile(filepath.Join(workspace, "sandbox-result"))
	if err != nil || string(result) != "sandbox-ok\n" {
		t.Fatalf("isolated tool command did not prove the credential boundary: %v (%q)", err, result)
	}
	info, err := os.Stat(workspace)
	if err != nil || info.Sys().(*syscall.Stat_t).Uid != 0 || info.Mode().Perm() != 0o700 {
		t.Fatalf("trusted adapter did not reclaim its checkout: %v", err)
	}
	leftovers, err := pipeline.dockerOutput(context.Background(), "ps", "-a", "--filter", "label="+sandboxOwnerLabel+"="+config.WorkspaceVolume, "--format", "{{.Names}}")
	if err != nil || strings.TrimSpace(leftovers) != "" {
		t.Fatalf("sandbox container cleanup incomplete: %v (%s)", err, leftovers)
	}
	t.Log(fmt.Sprintf("sandbox image %s completed two fixture model requests without provider writes", config.ImageID))
}

func TestDockerSandboxCancellationKillsChild(t *testing.T) {
	if os.Getenv("OPENREVIEW_TEST_DOCKER_SANDBOX") != "1" || os.Geteuid() != 0 {
		t.Skip("requires opt-in disposable Linux Docker sandbox acceptance")
	}
	root := os.Getenv("OPENREVIEW_TEST_DOCKER_WORKSPACE_ROOT")
	workspace, err := os.MkdirTemp(root, "agent-task-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workspace)
	if output, err := exec.Command("git", "-C", workspace, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("create disposable sandbox checkout: %v (%s)", err, output)
	}
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/responses" || request.Header.Get("Authorization") != "Bearer synthetic-upstream-key" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-request.Context().Done():
		case <-release:
		}
	}))
	defer upstream.Close()
	defer close(release)
	pool, err := NewExecutorIdentityPool(10002, 1)
	if err != nil {
		t.Fatal(err)
	}
	pipeline := Pipeline{
		WorkspaceRoot: root, ExecutorKind: "codex", ExecutorIdentityPool: pool,
		DockerSandbox: &DockerSandboxConfig{
			DockerBinary: "docker", ImageID: os.Getenv("OPENREVIEW_TEST_DOCKER_IMAGE_ID"),
			InternalNetwork: os.Getenv("OPENREVIEW_TEST_DOCKER_NETWORK"), WorkspaceVolume: os.Getenv("OPENREVIEW_TEST_DOCKER_VOLUME"),
			AdapterHost: os.Getenv("OPENREVIEW_TEST_DOCKER_ADAPTER_HOST"),
		},
		CodeModelBroker: ModelBrokerConfig{APIBaseURL: upstream.URL + "/v1", APIKey: "synthetic-upstream-key", Model: "gpt-6-sol", HTTPClient: upstream.Client()},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- pipeline.runAgent(ctx, workspace, Submission{}) }()
	select {
	case <-started:
		cancel()
	case err := <-finished:
		t.Fatalf("sandbox stopped before its model request: %v", err)
	case <-time.After(25 * time.Second):
		cancel()
		t.Fatal("sandbox never reached its model broker")
	}
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("cancelled sandbox reported success")
		}
	case <-time.After(18 * time.Second):
		t.Fatal("cancelled sandbox did not stop and clean up")
	}
	info, err := os.Stat(workspace)
	if err != nil || info.Sys().(*syscall.Stat_t).Uid != 0 || info.Mode().Perm() != 0o700 {
		t.Fatalf("cancelled sandbox checkout was not reclaimed: %v", err)
	}
	leftovers, err := pipeline.dockerOutput(context.Background(), "ps", "-a", "--filter", "label="+sandboxOwnerLabel+"="+pipeline.DockerSandbox.WorkspaceVolume, "--format", "{{.Names}}")
	if err != nil || strings.TrimSpace(leftovers) != "" {
		t.Fatalf("cancelled sandbox child remained: %v (%s)", err, leftovers)
	}
}
