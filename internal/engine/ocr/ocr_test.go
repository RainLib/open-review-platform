package ocr

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
)

func TestParseFindingsNormalizesOCRComments(t *testing.T) {
	findings, err := ParseFindings([]byte(`{"comments":[{"path":"src/handler.go","content":"nil dereference","suggestion_code":"if value == nil { return }","start_line":8,"end_line":8,"severity":"high","category":"bug"},{"path":"../../etc/passwd","content":"unsafe path","severity":"unknown"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 {
		t.Fatalf("got %d findings", len(findings))
	}
	if findings[0].Path != "src/handler.go" || findings[0].Severity != "high" || findings[0].Suggestion == "" {
		t.Fatalf("unexpected first finding: %#v", findings[0])
	}
	if findings[1].Path != "" || findings[1].Severity != "medium" {
		t.Fatalf("expected unsafe path to lose inline location: %#v", findings[1])
	}
}

func TestWithGitBinaryPathPrependsConfiguredDirectory(t *testing.T) {
	environment := withGitBinaryPath([]string{"PATH=/usr/bin", "OTHER=value"}, "/opt/git/bin/git")
	if environment[0] != "PATH=/opt/git/bin"+string(os.PathListSeparator)+"/usr/bin" {
		t.Fatalf("unexpected PATH: %q", environment[0])
	}
	if environment[1] != "OTHER=value" {
		t.Fatalf("unrelated environment entry changed: %q", environment[1])
	}
}

func TestConfigureProcessGroupCancelsWrappedChild(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a short-lived child process")
	}
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	command := exec.CommandContext(ctx, "sh", "-c", "sleep 30 & echo $! > \"$1\"; wait", "sh", pidFile)
	configureProcessGroup(command)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	childPID := waitForChildPID(t, pidFile)
	defer func() { _ = syscall.Kill(childPID, syscall.SIGKILL) }()
	cancel()
	_ = command.Wait() // SIGTERM is expected for the process-group leader.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(childPID, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("child process %d survived parent cancellation", childPID)
}

func TestReviewReportsWholeProcessTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("executes a short-lived shell process")
	}
	directory := t.TempDir()
	script := filepath.Join(directory, "slow-ocr")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := (Executor{Binary: script, Timeout: 20 * time.Millisecond}).Review(context.Background(), directory, "base", "head")
	if err == nil || !errors.Is(err, domain.ErrReviewTimedOut) || !strings.Contains(err.Error(), "after 20ms") {
		t.Fatalf("expected bounded OCR timeout, got %v", err)
	}
}

func TestTrimmedOutputPreservesFailureTail(t *testing.T) {
	value := []byte(strings.Repeat("skip\n", 2000) + "provider returned 429")
	trimmed := trimmedOutput(value)
	if !strings.Contains(trimmed, "output truncated") || !strings.Contains(trimmed, "provider returned 429") {
		t.Fatalf("trimmed output lost failure tail: %q", trimmed[len(trimmed)-100:])
	}
}

func waitForChildPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		contents, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(contents)))
			if parseErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("child process did not report its pid")
	return 0
}
