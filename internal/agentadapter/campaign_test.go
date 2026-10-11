package agentadapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

type campaignScopeCredentialProbe struct{ called bool }

func (p *campaignScopeCredentialProbe) Resolve(context.Context, RepositoryCredentialScope) (RepositoryCredential, error) {
	p.called = true
	return RepositoryCredential{}, errors.New("credential probe must not run")
}

func TestCampaignDeploymentScopeStopsBeforeCredentialsAndCheckout(t *testing.T) {
	for _, mode := range []string{"docs", "replace"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			probe := &campaignScopeCredentialProbe{}
			pipeline := Pipeline{WorkspaceRoot: root, ExecutorKind: "codex", AllowedPaths: []string{"docs/**"}, CredentialSource: probe}
			binding := domain.AgentCampaignBinding{CampaignID: uuid.New(), TargetID: uuid.New(), RequestSHA256: strings.Repeat("a", 64), Mode: mode, Paths: []string{"README.md"}, Criteria: []string{"Preserve README content"}}
			if mode == "replace" {
				binding.Paths = []string{"*.md"}
				binding.Search, binding.Replacement = "old", "new"
				binding.Files = []domain.AgentCampaignFile{{Path: "README.md", SHA256: strings.Repeat("b", 64), Matches: 1}}
			}
			submission := Submission{}
			submission.Task.ExecutorProfile, submission.Task.Campaign = "codex", &binding
			_, err := pipeline.Execute(context.Background(), "scope-preflight", submission)
			if err == nil {
				t.Fatal("incompatible scope was admitted")
			}
			code, message := executionFailureResult(err)
			entries, readErr := os.ReadDir(root)
			if err == nil || code != "agent_campaign_deployment_scope_unavailable" || executionFailureStage(err) != "campaign_deployment_scope" || !strings.Contains(message, "not started") || probe.called || readErr != nil || len(entries) != 0 {
				t.Fatalf("incompatible scope caused side effects: code=%s credential=%v entries=%d err=%v", code, probe.called, len(entries), err)
			}
			pipeline.AllowedPaths = append(pipeline.AllowedPaths, "README.md")
			if err := pipeline.checkCampaignDeploymentScope(submission); err != nil {
				t.Fatal("approved README path remained blocked after correcting deployment configuration", err)
			}
		})
	}
}

func TestCampaignReplacementValidatesEveryOriginalBeforeWriting(t *testing.T) {
	for _, invalidSecond := range []string{"digest", "count", "symlink"} {
		t.Run(invalidSecond, func(t *testing.T) {
			root := t.TempDir()
			original := []byte("old documentation\n")
			h := sha256.Sum256(original)
			b := domain.AgentCampaignBinding{CampaignID: uuid.New(), TargetID: uuid.New(), RequestSHA256: strings.Repeat("a", 64), Mode: "replace", Paths: []string{"*.md"}, Search: "old", Replacement: "new", Criteria: []string{"Documentation uses the updated text"}}
			for _, name := range []string{"first.md", "second.md"} {
				if err := os.WriteFile(filepath.Join(root, name), original, 0600); err != nil {
					t.Fatal(err)
				}
				b.Files = append(b.Files, domain.AgentCampaignFile{Path: name, SHA256: hex.EncodeToString(h[:]), Matches: 1, Bytes: len(original)})
			}
			switch invalidSecond {
			case "digest":
				b.Files[1].SHA256 = strings.Repeat("b", 64)
			case "count":
				b.Files[1].Matches = 2
			case "symlink":
				if err := os.Remove(filepath.Join(root, "second.md")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(root, "first.md"), filepath.Join(root, "second.md")); err != nil {
					t.Fatal(err)
				}
			}
			if err := applyCampaignReplacement(root, b); err == nil {
				t.Fatal("changed before-content, match count or symlink was accepted")
			}
			first, err := os.ReadFile(filepath.Join(root, "first.md"))
			if err != nil || string(first) != string(original) {
				t.Fatal("a partial replacement was written before validating all originals")
			}
		})
	}
}
