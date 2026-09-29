package agentcredentials

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/agentadapter"
	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

func TestGitLabCodingIssuerRequiresExactBrokerGrant(t *testing.T) {
	provider := newGitLabProjectIdentityFixture(t, "team/project", 27, "project_27_bot_coding", false)
	defer provider.Close()
	installationID := uuid.New()
	grant := store.AgentTaskCredentialGrant{
		TenantID: uuid.New(), InstallationID: installationID, Provider: domain.ProviderGitLab,
		APIBaseURL: provider.URL + "/api/v4", Repository: "team/project",
		ReviewInstallationExternalID: "review-installation",
	}
	path := filepath.Join(t.TempDir(), "coding-gitlab.json")
	writeGitLabCodingMap(t, path, installationID, grant.APIBaseURL, grant.Repository, provider.URL, "coding-project-token")
	issuer := GitLabIssuer{Credentials: agentadapter.FileCredentialSource{Path: path}, HTTPClient: provider.Client()}
	if err := issuer.Validate(); err != nil {
		t.Fatal(err)
	}
	fixture := &fixtureGrantStore{grant: grant}
	secret := strings.Repeat("s", 32)
	server := httptest.NewServer((Service{Secret: secret, Store: fixture, Issuer: ProviderIssuer{GitLab: issuer}}).Handler())
	defer server.Close()
	source, err := NewSource(server.URL, secret, true)
	if err != nil {
		t.Fatal(err)
	}
	scope := agentadapter.RepositoryCredentialScope{
		AttemptID: uuid.New(), AdapterJobID: "adapter-job-1", InstallationID: installationID,
		Provider: grant.Provider, APIBaseURL: grant.APIBaseURL, Repository: grant.Repository,
	}
	credential, err := source.Resolve(context.Background(), scope)
	if err != nil || credential.CloneBaseURL != provider.URL || credential.Token != "coding-project-token" || fixture.reservations != 1 || strings.Join(fixture.completed, ",") != "issued" {
		t.Fatalf("exact GitLab broker grant clone=%q token-present=%t reservations=%d completed=%v error=%v", credential.CloneBaseURL, credential.Token != "", fixture.reservations, fixture.completed, err)
	}
	for _, changed := range []agentadapter.RepositoryCredentialScope{
		func() agentadapter.RepositoryCredentialScope {
			copy := scope
			copy.InstallationID = uuid.New()
			return copy
		}(),
		func() agentadapter.RepositoryCredentialScope {
			copy := scope
			copy.Repository = "team/other"
			return copy
		}(),
		func() agentadapter.RepositoryCredentialScope {
			copy := scope
			copy.APIBaseURL = "https://other.example/api/v4"
			return copy
		}(),
	} {
		if _, err := source.Resolve(context.Background(), changed); err == nil {
			t.Fatalf("changed GitLab grant received a coding credential: %+v", changed)
		}
	}
	fixture.err = store.ErrAgentTaskClaimLost
	if _, err := source.Resolve(context.Background(), scope); err == nil {
		t.Fatal("lost task lease received a GitLab coding credential")
	}
	fixture.err = nil
	writeGitLabCodingMap(t, path, installationID, grant.APIBaseURL, grant.Repository, provider.URL, "rotated-project-token")
	rotated, err := source.Resolve(context.Background(), scope)
	if err != nil || rotated.Token != "rotated-project-token" {
		t.Fatalf("GitLab credential rotation was not applied to the next request: token-present=%t error=%v", rotated.Token != "", err)
	}
}

func TestGitLabCodingIssuerRejectsBroadOrWrongProjectToken(t *testing.T) {
	for _, testcase := range []struct {
		name      string
		projectID int64
		project   string
		bot       string
		redirect  bool
	}{
		{"personal token", 27, "team/project", "human-maintainer", false},
		{"group token", 27, "team/project", "group_3_bot_coding", false},
		{"different project bot", 27, "team/project", "project_28_bot_coding", false},
		{"different repository", 27, "team/other", "project_27_bot_coding", false},
		{"redirected identity", 27, "team/project", "project_27_bot_coding", true},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			provider := newGitLabProjectIdentityFixture(t, testcase.project, testcase.projectID, testcase.bot, testcase.redirect)
			defer provider.Close()
			installationID := uuid.New()
			grant := store.AgentTaskCredentialGrant{InstallationID: installationID, Provider: domain.ProviderGitLab, APIBaseURL: provider.URL + "/api/v4", Repository: "team/project"}
			path := filepath.Join(t.TempDir(), "coding-gitlab.json")
			writeGitLabCodingMap(t, path, installationID, grant.APIBaseURL, grant.Repository, provider.URL, "synthetic-private-token")
			client := provider.Client()
			if testcase.redirect {
				client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
					t.Fatal("provider identity redirect was followed")
					return nil
				}
			}
			issuer := GitLabIssuer{Credentials: agentadapter.FileCredentialSource{Path: path}, HTTPClient: client}
			_, token, err := issuer.Issue(context.Background(), grant)
			if err == nil || token != "" {
				t.Fatal("non-project-scoped GitLab token was issued")
			}
			if strings.Contains(err.Error(), "synthetic-private-token") {
				t.Fatal("GitLab identity failure leaked the token")
			}
		})
	}
}

func TestGitLabCodingIssuerRequiresVerifiedWriteScopes(t *testing.T) {
	for _, testcase := range []struct {
		name  string
		token gitLabTokenFixture
	}{
		{"no API write", gitLabTokenFixture{Scopes: []string{"write_repository"}, Active: true}},
		{"no Git push", gitLabTokenFixture{Scopes: []string{"api"}, Active: true}},
		{"read-only token", gitLabTokenFixture{Scopes: []string{"read_api", "read_repository"}, Active: true}},
		{"inactive token", gitLabTokenFixture{Scopes: []string{"api", "write_repository"}}},
		{"revoked token", gitLabTokenFixture{Scopes: []string{"api", "write_repository"}, Active: true, Revoked: true}},
		{"different token owner", gitLabTokenFixture{Scopes: []string{"api", "write_repository"}, Active: true, UserID: 999}},
		{"token metadata unavailable", gitLabTokenFixture{Scopes: []string{"api", "write_repository"}, Active: true, SelfStatus: http.StatusNotFound}},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			provider := newGitLabProjectIdentityFixtureWithToken(t, "team/project", 27, "project_27_bot_coding", false, testcase.token)
			defer provider.Close()
			installationID := uuid.New()
			grant := store.AgentTaskCredentialGrant{InstallationID: installationID, Provider: domain.ProviderGitLab, APIBaseURL: provider.URL + "/api/v4", Repository: "team/project"}
			path := filepath.Join(t.TempDir(), "coding-gitlab.json")
			const secret = "synthetic-project-token"
			writeGitLabCodingMap(t, path, installationID, grant.APIBaseURL, grant.Repository, provider.URL, secret)
			_, token, err := (GitLabIssuer{Credentials: agentadapter.FileCredentialSource{Path: path}, HTTPClient: provider.Client()}).Issue(context.Background(), grant)
			if err == nil || token != "" {
				t.Fatal("GitLab coding token without verified write access was issued")
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatal("GitLab scope rejection leaked the token")
			}
		})
	}
}

func TestGitLabBrokerAuditsRejectedBroadTokenWithoutReturningIt(t *testing.T) {
	provider := newGitLabProjectIdentityFixture(t, "team/project", 27, "human-maintainer", false)
	defer provider.Close()
	installationID := uuid.New()
	grant := store.AgentTaskCredentialGrant{
		TenantID: uuid.New(), InstallationID: installationID, Provider: domain.ProviderGitLab,
		APIBaseURL: provider.URL + "/api/v4", Repository: "team/project",
	}
	path := filepath.Join(t.TempDir(), "coding-gitlab.json")
	const broadToken = "synthetic-broad-token"
	writeGitLabCodingMap(t, path, installationID, grant.APIBaseURL, grant.Repository, provider.URL, broadToken)
	fixture := &fixtureGrantStore{grant: grant}
	secret := strings.Repeat("s", 32)
	server := httptest.NewServer((Service{Secret: secret, Store: fixture, Issuer: GitLabIssuer{
		Credentials: agentadapter.FileCredentialSource{Path: path}, HTTPClient: provider.Client(),
	}}).Handler())
	defer server.Close()
	source, err := NewSource(server.URL, secret, true)
	if err != nil {
		t.Fatal(err)
	}
	scope := agentadapter.RepositoryCredentialScope{
		AttemptID: uuid.New(), AdapterJobID: "adapter-job-broad-token", InstallationID: installationID,
		Provider: grant.Provider, APIBaseURL: grant.APIBaseURL, Repository: grant.Repository,
	}
	credential, err := source.Resolve(context.Background(), scope)
	if err == nil || credential.Token != "" || fixture.reservations != 1 || strings.Join(fixture.completed, ",") != "failed" {
		t.Fatalf("broad token result: token-present=%t reservations=%d states=%v error=%v", credential.Token != "", fixture.reservations, fixture.completed, err)
	}
	if strings.Contains(err.Error(), broadToken) {
		t.Fatal("broker response leaked a rejected coding token")
	}
}

func TestGitLabCodingIssuerRejectsUnsafeOrigin(t *testing.T) {
	for _, pair := range []struct {
		api, clone string
		allowHTTP  bool
		valid      bool
	}{
		{"https://gitlab.example/api/v4", "https://gitlab.example", false, true},
		{"https://gitlab.example/git/api/v4", "https://gitlab.example/git", false, true},
		{"http://gitlab:8929/api/v4", "http://gitlab:8929", true, true},
		{"http://gitlab:8929/api/v4", "http://gitlab:8929", false, false},
		{"https://gitlab.example/api/v4", "https://other.example", false, false},
		{"https://gitlab.example/api/v4", "http://gitlab.example", true, false},
		{"https://gitlab.example/api/v4", "https://gitlab.example/other", false, false},
		{"https://gitlab.example/api/v4", "https://user:pass@gitlab.example", false, false},
	} {
		if got := validGitLabCodingCloneBase(pair.api, pair.clone, pair.allowHTTP); got != pair.valid {
			t.Fatalf("GitLab coding origin %q -> %q allowed=%t got=%t", pair.api, pair.clone, pair.allowHTTP, got)
		}
	}
	grant := store.AgentTaskCredentialGrant{InstallationID: uuid.New(), Provider: domain.ProviderGitLab, APIBaseURL: "https://gitlab.example/api/v4", Repository: "team/project"}
	path := filepath.Join(t.TempDir(), "coding-gitlab.json")
	writeGitLabCodingMap(t, path, grant.InstallationID, grant.APIBaseURL, grant.Repository, "https://other.example", "secret-token")
	issuer := GitLabIssuer{Credentials: agentadapter.FileCredentialSource{Path: path}}
	if _, _, err := issuer.Issue(context.Background(), grant); err == nil {
		t.Fatal("GitLab coding credential escaped the admitted API origin")
	}
	grant.Provider = domain.ProviderGitHub
	if _, _, err := issuer.Issue(context.Background(), grant); err == nil {
		t.Fatal("GitLab coding issuer accepted a GitHub grant")
	}
	if _, _, err := (ProviderIssuer{}).Issue(context.Background(), grant); err == nil {
		t.Fatal("unconfigured provider issuer accepted a grant")
	}
}

func TestGitLabCodingIssuerRejectsUnusableMapAtStartup(t *testing.T) {
	for _, testcase := range []struct {
		name      string
		provider  string
		apiBase   string
		cloneBase string
		allowHTTP bool
		wantValid bool
	}{
		{"exact HTTPS GitLab", "gitlab", "https://gitlab.example/api/v4", "https://gitlab.example", false, true},
		{"wrong provider", "github", "https://gitlab.example/api/v4", "https://gitlab.example", false, false},
		{"cross-origin clone", "gitlab", "https://gitlab.example/api/v4", "https://other.example", false, false},
		{"insecure clone without development opt-in", "gitlab", "http://gitlab:8929/api/v4", "http://gitlab:8929", false, false},
		{"explicit development HTTP", "gitlab", "http://gitlab:8929/api/v4", "http://gitlab:8929", true, true},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "coding-gitlab.json")
			contents, err := json.Marshal(map[string]any{
				"version": 1,
				"entries": []map[string]string{{
					"installation_id": uuid.NewString(), "provider": testcase.provider,
					"api_base_url": testcase.apiBase, "repository": "team/project",
					"clone_base_url": testcase.cloneBase, "token": "synthetic-secret-must-not-appear",
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, contents, 0o600); err != nil {
				t.Fatal(err)
			}
			err = (GitLabIssuer{Credentials: agentadapter.FileCredentialSource{Path: path}, AllowHTTP: testcase.allowHTTP}).Validate()
			if (err == nil) != testcase.wantValid {
				t.Fatalf("startup validity=%t, want %t", err == nil, testcase.wantValid)
			}
			if err != nil && strings.Contains(err.Error(), "synthetic-secret-must-not-appear") {
				t.Fatal("startup validation leaked the private coding token")
			}
		})
	}
}

func writeGitLabCodingMap(t *testing.T, path string, installationID uuid.UUID, apiBaseURL, repository, cloneBaseURL, token string) {
	t.Helper()
	contents, err := json.Marshal(map[string]any{
		"version": 1,
		"entries": []map[string]string{{
			"installation_id": installationID.String(), "provider": "gitlab",
			"api_base_url": apiBaseURL, "repository": repository,
			"clone_base_url": cloneBaseURL, "token": token,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
}

type gitLabTokenFixture struct {
	Scopes     []string
	Active     bool
	Revoked    bool
	UserID     int64
	SelfStatus int
}

func newGitLabProjectIdentityFixture(t *testing.T, repository string, projectID int64, username string, redirect bool) *httptest.Server {
	return newGitLabProjectIdentityFixtureWithToken(t, repository, projectID, username, redirect, gitLabTokenFixture{Scopes: []string{"api", "write_repository"}, Active: true})
}

func newGitLabProjectIdentityFixtureWithToken(t *testing.T, repository string, projectID int64, username string, redirect bool, token gitLabTokenFixture) *httptest.Server {
	t.Helper()
	const botUserID int64 = 2701
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") == "" || r.Method != http.MethodGet {
			http.Error(w, "missing project token", http.StatusUnauthorized)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v4/projects/") {
			if redirect {
				http.Redirect(w, r, "https://other.example/project", http.StatusTemporaryRedirect)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": projectID, "path_with_namespace": repository})
			return
		}
		if r.URL.Path == "/api/v4/user" {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": botUserID, "username": username})
			return
		}
		if r.URL.Path == "/api/v4/personal_access_tokens/self" {
			if token.SelfStatus != 0 {
				http.Error(w, "token metadata unavailable", token.SelfStatus)
				return
			}
			userID := token.UserID
			if userID == 0 {
				userID = botUserID
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"user_id": userID, "scopes": token.Scopes, "active": token.Active, "revoked": token.Revoked})
			return
		}
		http.NotFound(w, r)
	}))
}
