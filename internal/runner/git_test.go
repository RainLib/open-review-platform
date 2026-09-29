package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
)

func TestCheckoutPrepareDeepensHistoryForMergeBase(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "checkout", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "base")
	baseSHA := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	runGit(t, repo, "switch", "-c", "feature")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("base\nchange\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "commit", "-am", "feature")
	headSHA := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))

	remote := filepath.Join(t.TempDir(), "origin.git")
	runGit(t, "", "init", "--bare", remote)
	runGit(t, repo, "remote", "add", "origin", remote)
	runGit(t, repo, "push", "origin", "main", "feature")

	workspace, err := (Checkout{}).Prepare(context.Background(), domain.ReviewJob{
		Provider: domain.ProviderGitHub,
		CloneURL: "file://" + remote,
		BaseRef:  "main",
		BaseSHA:  baseSHA,
		HeadSHA:  headSHA,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workspace.Close() })
	if err := exec.Command("git", "-C", workspace.Path, "merge-base", "--is-ancestor", baseSHA, headSHA).Run(); err != nil {
		t.Fatalf("expected checkout history to contain a merge base: %v", err)
	}
	if workspace.BaseSHA != baseSHA {
		t.Fatalf("workspace diff base=%q, want %q", workspace.BaseSHA, baseSHA)
	}
	t.Logf("shallow=%s", strings.TrimSpace(runGit(t, workspace.Path, "rev-parse", "--is-shallow-repository")))
}

func TestCheckoutPrepareUsesMergeBaseWhenBaseAdvanced(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "checkout", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "base")
	mergeBaseSHA := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))

	runGit(t, repo, "switch", "-c", "feature")
	if err := os.WriteFile(filepath.Join(repo, "feature.go"), []byte("package feature\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "feature.go")
	runGit(t, repo, "commit", "-m", "feature")
	headSHA := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))

	runGit(t, repo, "switch", "main")
	if err := os.WriteFile(filepath.Join(repo, "base-only.go"), []byte("package baseonly\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "base-only.go")
	runGit(t, repo, "commit", "-m", "advance base")
	advancedBaseSHA := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))

	remote := filepath.Join(t.TempDir(), "origin.git")
	runGit(t, "", "init", "--bare", remote)
	runGit(t, repo, "remote", "add", "origin", remote)
	runGit(t, repo, "push", "origin", "main", "feature")

	workspace, err := (Checkout{}).Prepare(context.Background(), domain.ReviewJob{
		Provider: domain.ProviderGitHub,
		CloneURL: "file://" + remote,
		BaseRef:  "main",
		BaseSHA:  advancedBaseSHA,
		HeadSHA:  headSHA,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workspace.Close() })
	if workspace.BaseSHA != mergeBaseSHA {
		t.Fatalf("workspace diff base=%q, want common ancestor %q", workspace.BaseSHA, mergeBaseSHA)
	}
	changed := strings.Fields(runGit(t, workspace.Path, "diff", "--name-only", workspace.BaseSHA, headSHA))
	if len(changed) != 1 || changed[0] != "feature.go" {
		t.Fatalf("changed files=%v, want only feature.go", changed)
	}
}

func TestAuthenticatedCloneURLUsesHTTPSForGitHubAppToken(t *testing.T) {
	job := domain.ReviewJob{Provider: domain.ProviderGitHub, CloneURL: "git@github.com:RainLib/open-review-platform.git"}
	if got := authenticatedCloneURL(job, "installation-token"); got != "https://github.com/RainLib/open-review-platform.git" {
		t.Fatalf("unexpected GitHub SSH normalization: %q", got)
	}
	job.CloneURL = "ssh://git@ghe.example.test/RainLib/open-review-platform.git"
	if got := authenticatedCloneURL(job, "installation-token"); got != "https://ghe.example.test/RainLib/open-review-platform.git" {
		t.Fatalf("unexpected GitHub Enterprise SSH normalization: %q", got)
	}
	job.Provider = domain.ProviderGitLab
	if got := authenticatedCloneURL(job, "token"); got != job.CloneURL {
		t.Fatalf("non-GitHub URL changed: %q", got)
	}
}

func TestGitEnvironmentDisablesInteractiveCredentialPrompts(t *testing.T) {
	environment := gitEnvironment("installation-token")
	for key, want := range map[string]string{
		"GIT_TERMINAL_PROMPT": "0",
		"GCM_INTERACTIVE":     "Never",
		"GIT_CONFIG_COUNT":    "2",
		"GIT_CONFIG_KEY_0":    "credential.helper",
		"GIT_CONFIG_VALUE_0":  "",
		"GIT_CONFIG_KEY_1":    "http.extraHeader",
		"GIT_CONFIG_VALUE_1":  "Authorization: Basic eC1hY2Nlc3MtdG9rZW46aW5zdGFsbGF0aW9uLXRva2Vu",
	} {
		if got := environmentValue(environment, key); got != want {
			t.Fatalf("%s=%q, want %q", key, got, want)
		}
	}
}

func TestGitEnvironmentClearsCredentialsWithoutInstallationToken(t *testing.T) {
	environment := gitEnvironment("")
	if got := environmentValue(environment, "GIT_CONFIG_COUNT"); got != "1" {
		t.Fatalf("GIT_CONFIG_COUNT=%q, want 1", got)
	}
	if got := environmentValue(environment, "GIT_CONFIG_KEY_0"); got != "credential.helper" {
		t.Fatalf("GIT_CONFIG_KEY_0=%q, want credential.helper", got)
	}
	if got := environmentValue(environment, "GIT_CONFIG_KEY_1"); got != "" {
		t.Fatalf("unexpected authenticated header configuration %q", got)
	}
}

func TestBoundedOutputCapsUntrustedGitDiagnostics(t *testing.T) {
	output := newBoundedOutput(16)
	if written, err := output.Write([]byte(strings.Repeat("x", 64))); err != nil || written != 64 {
		t.Fatalf("written=%d err=%v", written, err)
	}
	value, truncated := output.bytes()
	if !truncated || len(value) != 16 || string(value) != "xxxxxxxxxxxxxxxx" {
		t.Fatalf("value=%q truncated=%t", value, truncated)
	}
	if written, err := output.Write([]byte("ignored")); err != nil || written != len("ignored") {
		t.Fatalf("bounded writer must drain remaining output, written=%d err=%v", written, err)
	}
}

func TestGitCommandCapsUntrustedErrorOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture uses a POSIX shell")
	}
	gitFixture := filepath.Join(t.TempDir(), "noisy-git")
	// This simulates a transport that emits far more diagnostic data than a
	// runner should retain. The command itself is intentionally not Git: the
	// checkout boundary only needs an executable with Git's argument shape.
	if err := os.WriteFile(gitFixture, []byte("#!/bin/sh\nyes x | head -c 131072 >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := (Checkout{GitBinary: gitFixture}).gitOutput(context.Background(), "", "", "clone")
	if err == nil {
		t.Fatal("expected noisy Git fixture to fail")
	}
	if !strings.Contains(err.Error(), "diagnostics capped at 65536 bytes") {
		t.Fatalf("missing bounded-output marker: %v", err)
	}
	if len(err.Error()) > 5000 {
		t.Fatalf("error retained too much transport output: %d bytes", len(err.Error()))
	}
}

func environmentValue(environment []string, key string) string {
	prefix := key + "="
	for index := len(environment) - 1; index >= 0; index-- {
		if strings.HasPrefix(environment[index], prefix) {
			return strings.TrimPrefix(environment[index], prefix)
		}
	}
	return ""
}

func runGit(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
	return string(output)
}
