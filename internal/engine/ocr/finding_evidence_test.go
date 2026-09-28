package ocr

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
)

func TestFindingSourceEvidenceUsesExactChangedHeadAndCheckedPatch(t *testing.T) {
	directory := t.TempDir()
	runGit(t, directory, "init", "-q")
	runGit(t, directory, "config", "user.name", "Open Review Test")
	runGit(t, directory, "config", "user.email", "open-review-test@local.invalid")
	file := filepath.Join(directory, "changed.go")
	if err := os.WriteFile(file, []byte("package main\n\nfunc score() int {\n\treturn 1\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "unchanged.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, directory, "add", ".")
	runGit(t, directory, "commit", "-qm", "base")
	base := strings.TrimSpace(runGit(t, directory, "rev-parse", "HEAD"))
	if err := os.WriteFile(file, []byte("package main\n\nfunc score() int {\n\treturn 2\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, directory, "add", ".")
	runGit(t, directory, "commit", "-qm", "head")
	head := strings.TrimSpace(runGit(t, directory, "rev-parse", "HEAD"))
	findings := []domain.Finding{
		{Path: "changed.go", StartLine: 4, EndLine: 4, SuggestionCode: "\treturn 3"},
		{Path: "unchanged.go", StartLine: 1, EndLine: 1, SuggestionCode: "package other"},
		{Path: "../changed.go", StartLine: 4, EndLine: 4, SuggestionCode: "\treturn 3"},
	}
	(Executor{GitBinary: "git"}).captureFindingSourceEvidence(context.Background(), directory, base, head, findings)
	if findings[0].CodeExcerptStartLine != 1 || !strings.Contains(findings[0].CodeExcerpt, "return 2") || !strings.Contains(findings[0].ProposedPatch, "+\treturn 3") {
		t.Fatalf("missing exact-head evidence: %#v", findings[0])
	}
	if findings[1].CodeExcerpt != "" || findings[1].ProposedPatch != "" || findings[2].CodeExcerpt != "" || findings[2].ProposedPatch != "" {
		t.Fatalf("unmodified or invalid paths gained source evidence: %#v", findings)
	}
	if got := strings.TrimSpace(runGit(t, directory, "show", "HEAD:changed.go")); !strings.Contains(got, "return 2") {
		t.Fatalf("candidate patch changed the repository: %s", got)
	}
}

func TestFindingSourceEvidenceSkipsUnsafeFilesAndUnboundedSuggestions(t *testing.T) {
	directory := t.TempDir()
	runGit(t, directory, "init", "-q")
	runGit(t, directory, "config", "user.name", "Open Review Test")
	runGit(t, directory, "config", "user.email", "open-review-test@local.invalid")
	if err := os.WriteFile(filepath.Join(directory, "text.go"), []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, directory, "add", ".")
	runGit(t, directory, "commit", "-qm", "base")
	base := strings.TrimSpace(runGit(t, directory, "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(directory, "text.go"), []byte("after"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "binary.bin"), []byte{0, 1, 2}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("text.go", filepath.Join(directory, "link.go")); err != nil {
		t.Fatal(err)
	}
	runGit(t, directory, "add", ".")
	runGit(t, directory, "commit", "-qm", "head")
	head := strings.TrimSpace(runGit(t, directory, "rev-parse", "HEAD"))
	findings := []domain.Finding{
		{Path: "text.go", StartLine: 1, EndLine: 1, SuggestionCode: "replacement"},
		{Path: "binary.bin", StartLine: 1, EndLine: 1, SuggestionCode: "replacement"},
		{Path: "link.go", StartLine: 1, EndLine: 1, SuggestionCode: "replacement"},
	}
	(Executor{GitBinary: "git"}).captureFindingSourceEvidence(context.Background(), directory, base, head, findings)
	if findings[0].CodeExcerpt != "after" || findings[0].ProposedPatch != "" {
		t.Fatalf("no-final-newline source should have an excerpt but no patch: %#v", findings[0])
	}
	if findings[1].CodeExcerpt != "" || findings[2].CodeExcerpt != "" {
		t.Fatalf("binary or symlink gained source evidence: %#v", findings)
	}
	if proposedFindingPatch("safe.go", []string{"old"}, 1, 1, strings.Repeat("x", maxFindingReplacement+1)) != "" ||
		proposedFindingPatch("../unsafe.go", []string{"old"}, 1, 1, "new") != "" ||
		proposedFindingPatch("safe.go", []string{"old"}, 1, 1, "```ignore") != "" {
		t.Fatal("unsafe model suggestion became a candidate patch")
	}
}
