//go:build linux || darwin

package agentadapter

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunAgentStopsBackgroundDescendantsBeforeValidatingCheckout(t *testing.T) {
	workspace := t.TempDir()
	readme := filepath.Join(workspace, "README.md")
	if err := os.WriteFile(readme, []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(workspace, "late-write")
	cli := filepath.Join(t.TempDir(), "fake-codex")
	script := "#!/bin/sh\n" +
		"printf 'immediate\\n' >> README.md\n" +
		"(sleep 1; printf 'late\\n' >> README.md; touch " + marker + ") >/dev/null 2>&1 &\n"
	if err := os.WriteFile(cli, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	pipeline := Pipeline{ExecutorKind: "codex", CodexBinary: cli}
	if err := pipeline.runAgent(context.Background(), workspace, Submission{}); err != nil {
		t.Fatalf("agent failed: %v", err)
	}
	time.Sleep(1200 * time.Millisecond)
	contents, err := os.ReadFile(readme)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "immediate\n") || strings.Contains(string(contents), "late\n") {
		t.Fatalf("background descendant changed checkout after executor returned: %q", contents)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("background descendant survived executor cleanup: marker err=%v", err)
	}
}

func TestRunAgentStopsUnboundedExecutorOutput(t *testing.T) {
	cli := filepath.Join(t.TempDir(), "noisy-codex")
	if err := os.WriteFile(cli, []byte("#!/bin/sh\nhead -c 2097152 /dev/zero\nsleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	started := time.Now()
	err := (Pipeline{ExecutorKind: "codex", CodexBinary: cli}).runAgent(ctx, t.TempDir(), Submission{})
	if err == nil || !strings.Contains(err.Error(), "output exceeded") {
		t.Fatalf("unbounded executor output was not rejected: %v", err)
	}
	if elapsed := time.Since(started); elapsed >= 18*time.Second {
		t.Fatalf("output budget did not stop executor promptly: %v", elapsed)
	}
}

func TestGitStopsUnboundedCommandOutput(t *testing.T) {
	git := filepath.Join(t.TempDir(), "noisy-git")
	if err := os.WriteFile(git, []byte("#!/bin/sh\nhead -c 9437184 /dev/zero\nsleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	started := time.Now()
	_, err := (Pipeline{GitBinary: git}).git(ctx, t.TempDir(), noGitCredential, "status")
	if err == nil || !strings.Contains(err.Error(), "output exceeded") {
		t.Fatalf("unbounded Git output was not rejected: %v", err)
	}
	if elapsed := time.Since(started); elapsed >= 18*time.Second {
		t.Fatalf("Git output budget did not stop command promptly: %v", elapsed)
	}
}
