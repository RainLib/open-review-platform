package agentadapter

import (
	"context"
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
)

func TestRunAgentCannotReadAdapterCredentialOrParentProcess(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires the adapter root identity in a Linux test container")
	}
	workspace := t.TempDir()
	// t.TempDir's test-specific parent is private to root, whereas the real
	// adapter's /workspaces parent is traversable but not listable by the CLI.
	if err := os.Chmod(filepath.Dir(workspace), 0o711); err != nil {
		t.Fatal(err)
	}
	secretDir := t.TempDir()
	secretPath := filepath.Join(secretDir, "repository-credentials.json")
	if err := os.WriteFile(secretPath, []byte("synthetic-repository-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	cli := filepath.Join(workspace, "fake-codex")
	script := fmt.Sprintf("#!/bin/sh\nid -u > executor-uid\nid -G > executor-groups\nawk '/^CapEff:/ {print $2}' /proc/self/status > executor-caps\nif cat %q >/dev/null 2>&1; then touch credential-leaked; fi\nif cat /proc/$PPID/environ >/dev/null 2>&1; then touch parent-environment-leaked; fi\nchmod 0777 .\n", secretPath)
	if err := os.WriteFile(cli, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	pool, err := NewExecutorIdentityPool(10002, 1)
	if err != nil {
		t.Fatal(err)
	}
	pipeline := Pipeline{ExecutorKind: "codex", CodexBinary: cli, ExecutorIdentityPool: pool}
	if err := pipeline.runAgent(context.Background(), workspace, Submission{}); err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"credential-leaked", "parent-environment-leaked"} {
		if _, err := os.Stat(filepath.Join(workspace, marker)); !os.IsNotExist(err) {
			t.Fatalf("untrusted coding CLI accessed adapter-only data: %s (%v)", marker, err)
		}
	}
	uid, err := os.ReadFile(filepath.Join(workspace, "executor-uid"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(uid)) != "10002" {
		t.Fatalf("coding CLI did not run as the leased executor UID: %q", uid)
	}
	groups, err := os.ReadFile(filepath.Join(workspace, "executor-groups"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(groups)) != "10002" {
		t.Fatalf("coding CLI inherited adapter groups: %q", groups)
	}
	caps, err := os.ReadFile(filepath.Join(workspace, "executor-caps"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Trim(strings.TrimSpace(string(caps)), "0") != "" {
		t.Fatalf("coding CLI retained adapter capabilities: %q", caps)
	}
	info, err := os.Stat(workspace)
	if err != nil {
		t.Fatal(err)
	}
	stat := info.Sys().(*syscall.Stat_t)
	if stat.Uid != 0 || info.Mode().Perm() != 0o700 {
		t.Fatalf("adapter failed to reclaim private checkout: owner=%d mode=%o", stat.Uid, info.Mode().Perm())
	}
}

func TestIsolatedCodingCLIUsesBrokerWithoutUpstreamKey(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires the adapter root identity in a Linux test container")
	}
	if _, err := exec.LookPath("wget"); err != nil {
		t.Skip("requires a disposable wget client in the Linux fixture")
	}
	workspace := t.TempDir()
	if err := os.Chmod(filepath.Dir(workspace), 0o711); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/responses" || request.Header.Get("Authorization") != "Bearer synthetic-upstream-only-key" {
			t.Error("isolated coding CLI request reached an incorrect upstream boundary")
		}
		calls.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]string{"status": "fixture-completed"})
	}))
	defer upstream.Close()
	t.Setenv("AGENT_ADAPTER_CODEX_MODEL_API_KEY", "synthetic-upstream-only-key")
	cli := filepath.Join(workspace, "fake-codex")
	script := "#!/bin/sh\nset -eu\n" +
		"[ -z \"${AGENT_ADAPTER_CODEX_MODEL_API_KEY+x}\" ]\n" +
		"[ -n \"$OPENREVIEW_MODEL_CAPABILITY\" ]\n" +
		"endpoint=$(sed -n 's/^base_url = \"\\(.*\\)\"/\\1/p' \"$CODEX_HOME/config.toml\")\n" +
		"wget -qO- --header=\"Authorization: Bearer $OPENREVIEW_MODEL_CAPABILITY\" --header='Content-Type: application/json' --post-data='{\"input\":\"fixture\"}' \"$endpoint/responses\" > model-response\n"
	if err := os.WriteFile(cli, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	pool, err := NewExecutorIdentityPool(10002, 1)
	if err != nil {
		t.Fatal(err)
	}
	pipeline := Pipeline{
		ExecutorKind: "codex", CodexBinary: cli, ExecutorIdentityPool: pool,
		CodeModelBroker: ModelBrokerConfig{APIBaseURL: upstream.URL + "/v1", APIKey: "synthetic-upstream-only-key", Model: "fixed-model", HTTPClient: upstream.Client()},
	}
	if err := pipeline.runAgent(context.Background(), workspace, Submission{}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("isolated coding CLI did not use the job-scoped broker: calls=%d", calls.Load())
	}
	content, err := os.ReadFile(filepath.Join(workspace, "model-response"))
	if err != nil || !strings.Contains(string(content), "fixture-completed") {
		t.Fatalf("isolated coding CLI did not receive the broker response: %v", err)
	}
}
