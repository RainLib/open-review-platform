package risk

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func changedRepository(t *testing.T, paths []string) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", dir}, args...)...)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	git("init", "-q")
	git("-c", "user.name=Acceptance", "-c", "user.email=acceptance@example.invalid", "commit", "--allow-empty", "-qm", "base")
	base := git("rev-parse", "HEAD")
	for _, path := range paths {
		fullPath := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte("package accounts\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git("add", "--all")
	git("-c", "user.name=Acceptance", "-c", "user.email=acceptance@example.invalid", "commit", "-qm", "changed files")
	return dir, base, git("rev-parse", "HEAD")
}

func TestFocusedReviewIncludesOrdinaryBusinessCodeAndPreservesGitPaths(t *testing.T) {
	// The real GitLab acceptance MR with accounts/lookup.go was incorrectly
	// passed without a model call because its directory had no risk keyword.
	codePaths := []string{"accounts/lookup.go", "业务/lookup.go", "customer accounts/lookup.go", "customers/line\nbreak.go"}
	paths := append(append([]string{}, codePaths...), "docs/readme.md", "accounts/lookup_test.go", "generated/client.go")
	dir, base, head := changedRepository(t, paths)
	plan, err := (Planner{}).PlanWithMode(context.Background(), dir, base, head, ModeFocused)
	if err != nil {
		t.Fatal(err)
	}
	selected := make(map[string]bool)
	for _, item := range plan.Selected {
		selected[item.Path] = true
	}
	for _, path := range codePaths {
		if !selected[path] {
			t.Errorf("changed business code %q was not selected: %#v", path, plan)
		}
	}
	if len(plan.Selected) != len(codePaths) || len(plan.Deferred) != 3 {
		t.Fatalf("expected business code only, got %#v", plan)
	}
}

func TestFocusedBusinessCodeRemainsBoundedAndPrioritizesSecurity(t *testing.T) {
	paths := []string{"identity/authorize.go"}
	for i := 0; i < 12; i++ {
		paths = append(paths, fmt.Sprintf("accounts/lookup_%02d.go", i))
	}
	dir, base, head := changedRepository(t, paths)
	plan, err := (Planner{}).PlanWithMode(context.Background(), dir, base, head, ModeFocused)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Selected) != focusedSelectionLimit || len(plan.Deferred) != len(paths)-focusedSelectionLimit {
		t.Fatalf("scope budget was not retained: %#v", plan)
	}
	if plan.Selected[0].Path != "identity/authorize.go" {
		t.Fatalf("security boundary must lead the scope: %#v", plan.Selected)
	}
}
