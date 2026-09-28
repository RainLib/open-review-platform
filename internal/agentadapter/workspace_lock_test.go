package agentadapter

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorkspaceLockRejectsUnsafePaths(t *testing.T) {
	if _, err := LockWorkspaceRoot("relative"); err == nil {
		t.Fatal("relative workspace root was accepted")
	}
	root := t.TempDir()
	link := filepath.Join(t.TempDir(), "workspaces")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LockWorkspaceRoot(link); err == nil {
		t.Fatal("symlinked workspace root was accepted")
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".adapter-workspace.lock")); err != nil {
		t.Fatal(err)
	}
	if _, err := LockWorkspaceRoot(root); err == nil {
		t.Fatal("symlinked workspace lock was accepted")
	}
	if contents, err := os.ReadFile(outside); err != nil || string(contents) != "keep" {
		t.Fatalf("external symlink target changed: %q, %v", contents, err)
	}
}

func TestWorkspaceLockIsExclusiveAcrossProcesses(t *testing.T) {
	if os.Getenv("OPENREVIEW_WORKSPACE_LOCK_HELPER") == "1" {
		receipts, err := openReceiptStore(os.Getenv("OPENREVIEW_WORKSPACE_LOCK_RECEIPTS"))
		if err != nil {
			t.Fatal(err)
		}
		defer receipts.close()
		lock, err := LockWorkspaceRoot(os.Getenv("OPENREVIEW_WORKSPACE_LOCK_ROOT"))
		if err != nil {
			t.Fatal(err)
		}
		defer lock.Close()
		fmt.Println("workspace-owned")
		_, _ = io.Copy(io.Discard, os.Stdin)
		return
	}
	root := t.TempDir()
	child := exec.Command(os.Args[0], "-test.run=^TestWorkspaceLockIsExclusiveAcrossProcesses$")
	child.Env = append(os.Environ(), "OPENREVIEW_WORKSPACE_LOCK_HELPER=1", "OPENREVIEW_WORKSPACE_LOCK_ROOT="+root, "OPENREVIEW_WORKSPACE_LOCK_RECEIPTS="+filepath.Join(root, "receipts-one"))
	stdin, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = stdin.Close()
		if child.ProcessState == nil {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	}()
	ready := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		ready <- strings.TrimSpace(line)
	}()
	select {
	case line := <-ready:
		if line != "workspace-owned" {
			t.Fatalf("child did not acquire workspace: %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("child did not acquire workspace within five seconds")
	}
	otherReceipts, err := openReceiptStore(filepath.Join(root, "receipts-two"))
	if err != nil {
		t.Fatalf("distinct receipt directory should not be locked by the first adapter: %v", err)
	}
	defer otherReceipts.close()
	if _, err := LockWorkspaceRoot(root); err == nil || !strings.Contains(err.Error(), "another adapter owns") {
		t.Fatalf("second adapter was not rejected before cleanup: %v", err)
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err != nil {
		t.Fatalf("workspace owner did not exit cleanly: %v", err)
	}
	replacement, err := LockWorkspaceRoot(root)
	if err != nil {
		t.Fatalf("replacement could not acquire released workspace: %v", err)
	}
	if err := replacement.Close(); err != nil {
		t.Fatal(err)
	}
}
