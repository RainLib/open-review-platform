package agentadapter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
)

func testVerificationProfile() VerificationProfile {
	return VerificationProfile{
		Provider: domain.ProviderGitLab, APIBaseURL: "https://gitlab.example/api/v4",
		Repository: "team/project", ImageID: "sha256:" + strings.Repeat("a", 64),
		Argv: []string{"/usr/local/go/bin/go", "test", "./..."}, TimeoutSeconds: 120,
	}
}

func writeVerificationCatalog(t *testing.T, entries []VerificationProfile) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "verification.json")
	data, err := json.Marshal(verificationProfileFile{Version: 1, Entries: entries})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestVerificationProfileRequiresExactPrivateDeploymentIdentity(t *testing.T) {
	profile := testVerificationProfile()
	path := writeVerificationCatalog(t, []VerificationProfile{profile})
	var submission Submission
	submission.Task.Provider = profile.Provider
	submission.Task.APIBaseURL = profile.APIBaseURL
	submission.Task.Repository = profile.Repository
	selected, err := loadVerificationProfile(path, submission)
	if err != nil || selected == nil || selected.Argv[0] != "/usr/local/go/bin/go" {
		t.Fatalf("exact repository profile not selected: %+v %v", selected, err)
	}
	submission.Task.Repository = "other/project"
	if _, err := loadVerificationProfile(path, submission); err == nil {
		t.Fatal("another repository inherited the verification command")
	}
	if _, err := readVerificationProfiles(writeVerificationCatalog(t, []VerificationProfile{profile, profile})); err == nil {
		t.Fatal("duplicate profile identity was accepted")
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readVerificationProfiles(path); err == nil {
		t.Fatal("group/world-readable verification policy was accepted")
	}
	link := filepath.Join(t.TempDir(), "verification-link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readVerificationProfiles(link); err == nil {
		t.Fatal("symlinked verification policy was accepted")
	}
}

func TestVerificationProfileRejectsUnpinnedOrUnboundedCommands(t *testing.T) {
	profile := testVerificationProfile()
	for _, changed := range []VerificationProfile{
		func() VerificationProfile { item := profile; item.ImageID = "golang:latest"; return item }(),
		func() VerificationProfile { item := profile; item.Argv = []string{"go", "test"}; return item }(),
		func() VerificationProfile {
			item := profile
			item.Argv = []string{"/bin/sh", "-c", "go test\nrm -rf ."}
			return item
		}(),
		func() VerificationProfile { item := profile; item.TimeoutSeconds = 601; return item }(),
	} {
		if err := changed.valid(); err == nil {
			t.Fatalf("invalid verification profile was accepted: %+v", changed)
		}
	}
}

func TestAgentDraftDescribesOnlyTheVerificationActuallyRun(t *testing.T) {
	var submission Submission
	submission.Task.OriginKind = "issue"
	submission.Task.OriginNumber = 7
	submission.Task.BranchName = "agent/task-7"
	without := agentDraftDescription(submission, strings.Repeat("a", 40), draftEvidence{})
	if !strings.Contains(without, "Build, tests, SAST, performance, UI, and migrations were not attested") {
		t.Fatal("Draft without a profile claimed test evidence")
	}
	with := agentDraftDescription(submission, strings.Repeat("a", 40), draftEvidence{Verification: &VerificationEvidence{
		ProfileSHA256: strings.Repeat("b", 64), OutputSHA256: strings.Repeat("c", 64), OutputBytes: 28,
	}})
	if !strings.Contains(with, "One deployment-approved repository verification command passed") || !strings.Contains(with, "not product acceptance") || strings.Contains(with, "Build, tests, SAST, performance, UI, and migrations were not attested") {
		t.Fatal("Draft verification claim did not match the executed evidence")
	}
}
