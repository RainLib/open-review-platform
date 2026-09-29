package agentcredentials

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

type fixtureGitHubWriteTokens struct {
	installationID string
	repository     string
	calls          int
}

func (fixture *fixtureGitHubWriteTokens) RepositoryWriteToken(_ context.Context, installationID, repository string) (string, error) {
	fixture.installationID, fixture.repository = installationID, repository
	fixture.calls++
	return "repo-only-token", nil
}

func TestGitHubIssuerRequiresExplicitExactCodingInstallation(t *testing.T) {
	tenantID, reviewID := uuid.New(), uuid.New()
	path := filepath.Join(t.TempDir(), "coding-installations.json")
	entry := codingInstallationMapping{TenantID: tenantID.String(), ReviewInstallationID: reviewID.String(), ReviewInstallationExternalID: "123", APIBaseURL: "https://api.github.com", Repository: "acme/project", CodingInstallationExternalID: "456", CloneBaseURL: "https://github.com"}
	writeCodingMap(t, path, []codingInstallationMapping{entry}, 0o600)
	fixture := &fixtureGitHubWriteTokens{}
	issuer := GitHubIssuer{APIBaseURL: entry.APIBaseURL, MapPath: path, Tokens: fixture}
	grant := store.AgentTaskCredentialGrant{TenantID: tenantID, InstallationID: reviewID, Provider: domain.ProviderGitHub, APIBaseURL: entry.APIBaseURL, Repository: entry.Repository, ReviewInstallationExternalID: "123"}
	clone, token, err := issuer.Issue(context.Background(), grant)
	if err != nil || clone != entry.CloneBaseURL || token != "repo-only-token" || fixture.installationID != "456" || fixture.repository != entry.Repository || fixture.calls != 1 {
		t.Fatalf("coding installation mapping clone=%q token-present=%t installation=%q calls=%d error=%v", clone, token != "", fixture.installationID, fixture.calls, err)
	}
	grant.Repository = "acme/other"
	if _, _, err := issuer.Issue(context.Background(), grant); err == nil || fixture.calls != 1 {
		t.Fatalf("unmapped repository minted a coding token: %v", err)
	}
	grant.Repository = entry.Repository
	grant.TenantID = uuid.New()
	if _, _, err := issuer.Issue(context.Background(), grant); err == nil || fixture.calls != 1 {
		t.Fatalf("foreign tenant minted a coding token: %v", err)
	}
}

func TestCodingInstallationMapFailsClosed(t *testing.T) {
	tenantID, reviewID := uuid.New(), uuid.New()
	entry := codingInstallationMapping{TenantID: tenantID.String(), ReviewInstallationID: reviewID.String(), ReviewInstallationExternalID: "123", APIBaseURL: "https://api.github.com", Repository: "acme/project", CodingInstallationExternalID: "456", CloneBaseURL: "https://github.com"}
	for _, tc := range []struct {
		name    string
		entries []codingInstallationMapping
		mode    os.FileMode
	}{
		{name: "world-readable", entries: []codingInstallationMapping{entry}, mode: 0o644},
		{name: "duplicate exact scope", entries: []codingInstallationMapping{entry, entry}, mode: 0o600},
		{name: "unrelated clone host", entries: []codingInstallationMapping{{TenantID: entry.TenantID, ReviewInstallationID: entry.ReviewInstallationID, ReviewInstallationExternalID: entry.ReviewInstallationExternalID, APIBaseURL: entry.APIBaseURL, Repository: entry.Repository, CodingInstallationExternalID: entry.CodingInstallationExternalID, CloneBaseURL: "https://example.com"}}, mode: 0o600},
		{name: "nonnumeric coding installation", entries: []codingInstallationMapping{{TenantID: entry.TenantID, ReviewInstallationID: entry.ReviewInstallationID, ReviewInstallationExternalID: entry.ReviewInstallationExternalID, APIBaseURL: entry.APIBaseURL, Repository: entry.Repository, CodingInstallationExternalID: "not-an-id", CloneBaseURL: entry.CloneBaseURL}}, mode: 0o600},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mapping.json")
			writeCodingMap(t, path, tc.entries, tc.mode)
			if _, err := loadCodingInstallationMap(path); err == nil {
				t.Fatal("unsafe coding installation mapping was accepted")
			}
		})
	}
	path := filepath.Join(t.TempDir(), "mapping.json")
	writeCodingMap(t, path, []codingInstallationMapping{entry}, 0o600)
	link := filepath.Join(t.TempDir(), "mapping-link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCodingInstallationMap(link); err == nil {
		t.Fatal("symlinked coding installation mapping was accepted")
	}
}

func TestCodingCloneBaseMustPairWithAPI(t *testing.T) {
	if !validCodingCloneBase("https://api.github.com", "https://github.com") || !validCodingCloneBase("https://ghe.example/api/v3", "https://ghe.example") {
		t.Fatal("valid hosted or enterprise GitHub origin was rejected")
	}
	for _, pair := range [][2]string{{"https://api.github.com", "https://other.example"}, {"https://ghe.example/api/v3", "http://ghe.example"}, {"https://ghe.example/api/v3", "https://other.example"}, {"https://api.github.com", "https://github.com/other"}} {
		if validCodingCloneBase(pair[0], pair[1]) {
			t.Fatalf("unrelated or unsafe clone origin accepted: %q", strings.Join(pair[:], " -> "))
		}
	}
}

func writeCodingMap(t *testing.T, path string, entries []codingInstallationMapping, mode os.FileMode) {
	t.Helper()
	encoded, err := json.Marshal(struct {
		Version int                         `json:"version"`
		Entries []codingInstallationMapping `json:"entries"`
	}{Version: 1, Entries: entries})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, mode); err != nil {
		t.Fatal(err)
	}
}
