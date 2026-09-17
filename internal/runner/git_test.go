package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
	t.Logf("shallow=%s", strings.TrimSpace(runGit(t, workspace.Path, "rev-parse", "--is-shallow-repository")))
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
