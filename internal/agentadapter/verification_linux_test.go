//go:build linux

package agentadapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestApprovedVerificationRunsWithoutNetworkOrAdapterSecrets(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("workspace ownership test requires a disposable root container")
	}
	root := t.TempDir()
	workspace := filepath.Join(root, "agent-task-verified")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	argsFile := filepath.Join(t.TempDir(), "docker-args")
	imageID := "sha256:" + strings.Repeat("a", 64)
	scriptPath := filepath.Join(t.TempDir(), "fixture-docker")
	script := `#!/bin/sh
set -eu
case "$1:$2" in
  network:inspect) printf '%s\n' true ;;
  volume:inspect) printf '%s\n' agent-workspaces ;;
  image:inspect) printf '%s\n' '` + imageID + `' ;;
  run:*)
    test -z "${AGENT_ADAPTER_GITHUB_TOKEN+x}"
    test -z "${AGENT_ADAPTER_CODEX_MODEL_API_KEY+x}"
    test -z "${AGENT_TASK_ADAPTER_SECRET+x}"
    printf '%s\n' "$@" > '` + argsFile + `'
    printf '%s\n' 'verified command passed'
    ;;
  rm:*) printf '%s\n' removed ;;
  *) exit 2 ;;
esac
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	pool, err := NewExecutorIdentityPool(10002, 1)
	if err != nil {
		t.Fatal(err)
	}
	pipeline := Pipeline{WorkspaceRoot: root, ExecutorIdentityPool: pool, DockerSandbox: &DockerSandboxConfig{
		DockerBinary: scriptPath, ImageID: imageID, InternalNetwork: "agent-internal",
		WorkspaceVolume: "agent-workspaces", AdapterHost: "agent-adapter",
	}}
	t.Setenv("AGENT_ADAPTER_GITHUB_TOKEN", "must-stay-in-adapter")
	t.Setenv("AGENT_ADAPTER_CODEX_MODEL_API_KEY", "must-stay-in-adapter")
	t.Setenv("AGENT_TASK_ADAPTER_SECRET", "must-stay-in-adapter")
	profile := testVerificationProfile()
	evidence, err := pipeline.runVerification(context.Background(), workspace, profile)
	if err != nil {
		t.Fatal(err)
	}
	outputSHA := sha256.Sum256([]byte("verified command passed\n"))
	if evidence.OutputSHA256 != hex.EncodeToString(outputSHA[:]) || evidence.OutputBytes != int64(len("verified command passed\n")) || len(evidence.ProfileSHA256) != 64 {
		t.Fatalf("verification evidence was not bounded and hashed: %+v", evidence)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	joined := "\n" + string(args)
	for _, required := range []string{
		"\n--network\nnone\n", "\n--read-only\n", "\n--cap-drop\nALL\n",
		"\n--entrypoint\n/usr/local/go/bin/go\n", "\n--workdir\n" + workspace + "\n",
		"\n--mount\ntype=volume,src=agent-workspaces,dst=" + root + "\n",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("verification container lacks %q: %s", required, joined)
		}
	}
	if strings.Contains(joined, "must-stay-in-adapter") {
		t.Fatal("adapter secret entered the verification container arguments")
	}
	info, err := os.Stat(workspace)
	if err != nil || info.Sys() == nil {
		t.Fatalf("workspace ownership was not restored: %v", err)
	}
	if owner := info.Sys().(*syscall.Stat_t).Uid; owner != 0 {
		t.Fatalf("verification workspace remains owned by the child UID %d", owner)
	}
}
