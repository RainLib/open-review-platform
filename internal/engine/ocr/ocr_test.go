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

func TestReviewArgumentsIncludesConfiguredExecutionPolicy(t *testing.T) {
	executor := Executor{
		Concurrency:    2,
		Effort:         "low",
		MaxTokens:      8000,
		TokenBudget:    128000,
		SubtaskTimeout: 5,
	}
	arguments := executor.reviewArguments("base", "head", "result.json", "rules.json", []string{"docs/**", "*_test.go"})
	want := []string{
		"review", "--from", "base", "--to", "head", "--format", "json", "--output", "result.json",
		"--concurrency", "2", "--effort", "low", "--max-tokens", "8000", "--max-tokens-budget", "128000", "--timeout", "5",
		"--rule", "rules.json", "--exclude", "docs/\\*\\*,\\*_test.go",
	}
	if strings.Join(arguments, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("unexpected review arguments:\n got: %#v\nwant: %#v", arguments, want)
	}
}

func TestExecutionTimeoutUsesTheShorterPlatformOrSubtaskBudget(t *testing.T) {
	tests := []struct {
		name     string
		executor Executor
		want     time.Duration
	}{
		{name: "whole process only", executor: Executor{Timeout: 15 * time.Minute}, want: 15 * time.Minute},
		{name: "subtask caps process", executor: Executor{Timeout: 15 * time.Minute, SubtaskTimeout: 5}, want: 5 * time.Minute},
		{name: "whole process is stricter", executor: Executor{Timeout: 2 * time.Minute, SubtaskTimeout: 5}, want: 2 * time.Minute},
		{name: "unbounded only when both unset", executor: Executor{}, want: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.executor.executionTimeout(); got != test.want {
				t.Fatalf("execution timeout = %s, want %s", got, test.want)
			}
		})
	}
}

func TestExactExcludePatternsTreatsChangedPathsAsLiterals(t *testing.T) {
	got := exactExcludePatterns([]string{
		"apps/web/app/(console)/[org]/connect/page.tsx",
		"literal*question?/[value].go",
		"!important.go",
	})
	want := "apps/web/app/(console)/\\[org\\]/connect/page.tsx,literal\\*question\\?/\\[value\\].go,\\!important.go"
	if got != want {
		t.Fatalf("unexpected literal exclude patterns: got %q, want %q", got, want)
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

func TestReviewClassifiesModelContextExhaustion(t *testing.T) {
	if testing.Short() {
		t.Skip("executes a short-lived shell process")
	}
	directory := t.TempDir()
	script := filepath.Join(directory, "context-exhausted-ocr")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho 'Context compression exceeded threshold' >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := (Executor{Binary: script}).Review(context.Background(), directory, "base", "head")
	if err == nil || !errors.Is(err, domain.ErrReviewContextExhausted) {
		t.Fatalf("expected terminal model context error, got %v", err)
	}
}

func TestReviewWithSelectedPathsUsesExactSyntheticRange(t *testing.T) {
	if testing.Short() {
		t.Skip("creates a temporary Git worktree")
	}
	directory := t.TempDir()
	runGit(t, directory, "init", "-q")
	runGit(t, directory, "config", "user.name", "Open Review Test")
	runGit(t, directory, "config", "user.email", "open-review-test@local.invalid")
	if err := os.WriteFile(filepath.Join(directory, "selected.go"), []byte("package scoped\n\nconst Selected = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "deferred.go"), []byte("package scoped\n\nconst Deferred = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, directory, "add", ".")
	runGit(t, directory, "commit", "-qm", "base")
	base := strings.TrimSpace(runGit(t, directory, "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(directory, "selected.go"), []byte("package scoped\n\nconst Selected = 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "deferred.go"), []byte("package scoped\n\nconst Deferred = 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, directory, "add", ".")
	runGit(t, directory, "commit", "-qm", "head")
	head := strings.TrimSpace(runGit(t, directory, "rev-parse", "HEAD"))

	captured := filepath.Join(directory, "reviewed-paths.txt")
	t.Setenv("OCR_CAPTURED_PATHS", captured)
	script := filepath.Join(directory, "recording-ocr")
	source := "#!/bin/sh\nset -eu\nfrom=''\nto=''\noutput=''\nwhile [ \"$#\" -gt 0 ]; do\n  case \"$1\" in\n    --from) from=\"$2\"; shift 2 ;;\n    --to) to=\"$2\"; shift 2 ;;\n    --output) output=\"$2\"; shift 2 ;;\n    *) shift ;;\n  esac\ndone\ngit diff --name-only \"$from\" \"$to\" > \"$OCR_CAPTURED_PATHS\"\nprintf '{\\\"comments\\\":[]}' > \"$output\"\n"
	if err := os.WriteFile(script, []byte(source), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := (Executor{Binary: script, GitBinary: "git"}).ReviewWithSelectedPaths(context.Background(), directory, base, head, []string{"selected.go"}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(captured)
	if err != nil {
		t.Fatal(err)
	}
	if value := strings.TrimSpace(string(got)); value != "selected.go" {
		t.Fatalf("OCR received unexpected synthetic diff paths: %q", value)
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

func runGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(arguments, " "), err, output)
	}
	return string(output)
}
