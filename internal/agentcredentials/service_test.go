package agentcredentials

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/agentadapter"
	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

type fixtureGrantStore struct {
	grant         store.AgentTaskCredentialGrant
	err           error
	reserveErr    error
	completionErr error
	calls         int
	reservations  int
	completed     []string
}

func (fixture *fixtureGrantStore) ReserveAgentTaskCredentialIssuance(_ context.Context, _ uuid.UUID, _ string, _ store.AgentTaskCredentialGrant) (uuid.UUID, error) {
	fixture.reservations++
	if fixture.reserveErr != nil {
		return uuid.Nil, fixture.reserveErr
	}
	return uuid.New(), nil
}

func (fixture *fixtureGrantStore) CompleteAgentTaskCredentialIssuance(_ context.Context, _ uuid.UUID, state string) error {
	fixture.completed = append(fixture.completed, state)
	if state == "issued" {
		return fixture.completionErr
	}
	return nil
}

func (fixture *fixtureGrantStore) LoadAgentTaskCredentialGrant(_ context.Context, _ uuid.UUID, _ string) (store.AgentTaskCredentialGrant, error) {
	fixture.calls++
	return fixture.grant, fixture.err
}

type fixtureIssuer struct{ calls int }

func (fixture *fixtureIssuer) Issue(_ context.Context, _ store.AgentTaskCredentialGrant) (string, string, error) {
	fixture.calls++
	return "https://github.com", "scoped-secret", nil
}

func TestBrokerDeliversOnlyActiveExactScope(t *testing.T) {
	secret := strings.Repeat("s", 32)
	installationID, tenantID, attemptID := uuid.New(), uuid.New(), uuid.New()
	grant := store.AgentTaskCredentialGrant{TenantID: tenantID, InstallationID: installationID, Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com", Repository: "acme/project", ReviewInstallationExternalID: "123"}
	fixture := &fixtureGrantStore{grant: grant}
	issuer := &fixtureIssuer{}
	server := httptest.NewServer((Service{Secret: secret, Store: fixture, Issuer: issuer}).Handler())
	defer server.Close()
	source, err := NewSource(server.URL, secret, true)
	if err != nil {
		t.Fatal(err)
	}
	scope := agentadapter.RepositoryCredentialScope{AttemptID: attemptID, AdapterJobID: "job-1", InstallationID: installationID, Provider: grant.Provider, APIBaseURL: grant.APIBaseURL, Repository: grant.Repository}
	credential, err := source.Resolve(context.Background(), scope)
	if err != nil || credential.Token != "scoped-secret" || credential.CloneBaseURL != "https://github.com" || fixture.calls != 1 || fixture.reservations != 1 || strings.Join(fixture.completed, ",") != "issued" || issuer.calls != 1 {
		t.Fatalf("exact grant credential=%#v error=%v store_calls=%d reservations=%d completed=%v issue_calls=%d", credential, err, fixture.calls, fixture.reservations, fixture.completed, issuer.calls)
	}
	scope.Repository = "acme/other"
	if _, err := source.Resolve(context.Background(), scope); err == nil || issuer.calls != 1 {
		t.Fatalf("changed repository received credential: %v", err)
	}
	fixture.err = store.ErrAgentTaskClaimLost
	scope.Repository = grant.Repository
	if _, err := source.Resolve(context.Background(), scope); err == nil || issuer.calls != 1 {
		t.Fatalf("expired grant received credential: %v", err)
	}
}

func TestBrokerLimitsIssuanceAndWithholdsTokenAfterGrantLoss(t *testing.T) {
	secret := strings.Repeat("s", 32)
	installationID := uuid.New()
	grant := store.AgentTaskCredentialGrant{TenantID: uuid.New(), InstallationID: installationID, Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com", Repository: "acme/project", ReviewInstallationExternalID: "123"}
	fixture := &fixtureGrantStore{grant: grant, reserveErr: store.ErrAgentCredentialIssuanceLimit}
	issuer := &fixtureIssuer{}
	server := httptest.NewServer((Service{Secret: secret, Store: fixture, Issuer: issuer}).Handler())
	defer server.Close()
	source, err := NewSource(server.URL, secret, true)
	if err != nil {
		t.Fatal(err)
	}
	scope := agentadapter.RepositoryCredentialScope{AttemptID: uuid.New(), AdapterJobID: "job-1", InstallationID: installationID, Provider: grant.Provider, APIBaseURL: grant.APIBaseURL, Repository: grant.Repository}
	if _, err := source.Resolve(context.Background(), scope); err == nil || issuer.calls != 0 || fixture.reservations != 1 {
		t.Fatalf("issuance limit did not stop minting: err=%v issuer_calls=%d reservations=%d", err, issuer.calls, fixture.reservations)
	}
	fixture.reserveErr = nil
	fixture.completionErr = store.ErrAgentTaskClaimLost
	if _, err := source.Resolve(context.Background(), scope); err == nil || issuer.calls != 1 || strings.Join(fixture.completed, ",") != "issued,withheld" {
		t.Fatalf("lost grant returned a token: err=%v issuer_calls=%d completed=%v", err, issuer.calls, fixture.completed)
	}
}

func TestBrokerRejectsMissingSignatureBeforeDatabaseAccess(t *testing.T) {
	fixture := &fixtureGrantStore{}
	issuer := &fixtureIssuer{}
	server := httptest.NewServer((Service{Secret: strings.Repeat("s", 32), Store: fixture, Issuer: issuer}).Handler())
	defer server.Close()
	response, err := http.Post(server.URL+Path, "application/json", strings.NewReader(`{"attempt_id":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized || fixture.calls != 0 || issuer.calls != 0 || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("unsigned request status=%d store_calls=%d issue_calls=%d", response.StatusCode, fixture.calls, issuer.calls)
	}
}

func TestBrokerClientDoesNotForwardSignatureOnRedirect(t *testing.T) {
	secret := strings.Repeat("s", 32)
	received := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = true
		w.WriteHeader(http.StatusOK)
	}))
	defer destination.Close()
	sourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+Path, http.StatusTemporaryRedirect)
	}))
	defer sourceServer.Close()
	source, err := NewSource(sourceServer.URL, secret, true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = source.Resolve(context.Background(), agentadapter.RepositoryCredentialScope{AttemptID: uuid.New(), AdapterJobID: "job-1", InstallationID: uuid.New(), Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com", Repository: "acme/project"})
	if err == nil || received {
		t.Fatalf("redirect was followed or accepted: %v", err)
	}
}
