package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

func signedProviderAuthorizationForTest(t *testing.T, secret, tenant string, provider domain.Provider, externalID string, expiresAt time.Time) string {
	return signedProviderAuthorizationWithCredentialForTest(t, secret, tenant, provider, externalID, "", expiresAt)
}

func signedProviderAuthorizationWithCredentialForTest(t *testing.T, secret, tenant string, provider domain.Provider, externalID, credentialRef string, expiresAt time.Time) string {
	return signedProviderAuthorizationWithActorForTest(t, secret, tenant, provider, externalID, credentialRef, "", expiresAt)
}

func signedProviderAuthorizationWithActorForTest(t *testing.T, secret, tenant string, provider domain.Provider, externalID, credentialRef, actorExternalID string, expiresAt time.Time) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"actorExternalID": actorExternalID,
		"credentialRef":   credentialRef,
		"expiresAt":       expiresAt.UnixMilli(),
		"externalID":      externalID,
		"next":            "/" + tenant + "/home",
		"nonce":           "test-nonce",
		"provider":        provider,
		"tenant":          tenant,
		"version":         1,
	})
	if err != nil {
		t.Fatal(err)
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(encodedPayload))
	return encodedPayload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func TestVerifyProviderAuthorizationReceipt(t *testing.T) {
	secret := strings.Repeat("a", 32)
	now := time.Date(2026, time.September, 20, 12, 0, 0, 0, time.UTC)
	receipt := signedProviderAuthorizationForTest(t, secret, "acme", domain.ProviderGitHub, "123", now.Add(time.Minute))
	verified, ok := verifyProviderAuthorizationReceipt(receipt, secret, now)
	if !ok || verified.Tenant != "acme" || verified.Provider != domain.ProviderGitHub || verified.ExternalID != "123" {
		t.Fatalf("verified receipt=%#v ok=%t", verified, ok)
	}
	if _, ok := verifyProviderAuthorizationReceipt(receipt+"x", secret, now); ok {
		t.Fatal("tampered receipt was accepted")
	}
	expired := signedProviderAuthorizationForTest(t, secret, "acme", domain.ProviderGitHub, "123", now.Add(-time.Millisecond))
	if _, ok := verifyProviderAuthorizationReceipt(expired, secret, now); ok {
		t.Fatal("expired receipt was accepted")
	}
	malformedCredentialRef := signedProviderAuthorizationWithCredentialForTest(
		t,
		secret,
		"acme",
		domain.ProviderGitLab,
		"gitlab-oauth:opaque",
		"secret://provider/gitlab-oauth/not-a-uuid",
		now.Add(time.Minute),
	)
	if _, ok := verifyProviderAuthorizationReceipt(malformedCredentialRef, secret, now); ok {
		t.Fatal("receipt with malformed credential reference was accepted")
	}
	for _, actorID := range []string{"0", "01", "1e2", "-1", "18446744073709551616"} {
		malformedActor := signedProviderAuthorizationWithActorForTest(t, secret, "acme", domain.ProviderGitHub, "123", "", actorID, now.Add(time.Minute))
		if _, ok := verifyProviderAuthorizationReceipt(malformedActor, secret, now); ok {
			t.Fatalf("receipt with malformed actor ID %q was accepted", actorID)
		}
	}
}

func TestInstallationEndpointRequiresMatchingProviderAuthorization(t *testing.T) {
	setupSecret := strings.Repeat("a", 32)
	gitLabIdentitySecret := strings.Repeat("b", 32)
	now := time.Date(2026, time.September, 20, 12, 0, 0, 0, time.UTC)
	recording := &recordingStore{installation: domain.Installation{ID: uuid.New(), Active: true}}
	server := NewWithProviderAPIURLsAndProviderAuthorization(
		recording,
		fixedAuthenticator{},
		"",
		"",
		"https://api.github.com",
		"https://gitlab.example.com/api/v4",
		setupSecret,
		gitLabIdentitySecret,
	)
	server.now = func() time.Time { return now }
	mux := http.NewServeMux()
	server.Register(mux)

	request := httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/installations", strings.NewReader(`{"provider":"github","external_id":"123","repository_scope":"RainLib/*","credential_ref":"github-app"}`))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("unsigned installation status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/installations", strings.NewReader(`{"provider":"github","external_id":"123","repository_scope":"RainLib/*","credential_ref":"github-app"}`))
	request.Header.Set(providerAuthorizationHeader, signedProviderAuthorizationForTest(t, setupSecret, "acme", domain.ProviderGitHub, "123", now.Add(time.Minute)))
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || recording.installationInput.ExternalID != "123" {
		t.Fatalf("authorized GitHub installation status=%d input=%#v body=%s", response.Code, recording.installationInput, response.Body.String())
	}
	for _, test := range []struct {
		name, receiptActorID, requestedActorID string
		wantStatus                             int
	}{
		{"missing actor", "", "42", http.StatusForbidden},
		{"different actor", "17", "42", http.StatusForbidden},
		{"OAuth actor", "42", "42", http.StatusCreated},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/installations", strings.NewReader(`{"provider":"github","external_id":"123","repository_scope":"RainLib/*","author_scope":"mine","author_external_id":"`+test.requestedActorID+`","credential_ref":"github-app"}`))
			request.Header.Set(providerAuthorizationHeader, signedProviderAuthorizationWithActorForTest(t, setupSecret, "acme", domain.ProviderGitHub, "123", "", test.receiptActorID, now.Add(time.Minute)))
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d, want %d; body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			if test.wantStatus == http.StatusCreated && (recording.installationInput.AuthorScope != "mine" || recording.installationInput.AuthorExternalID != "42") {
				t.Fatalf("authorized author scope was not forwarded: %#v", recording.installationInput)
			}
		})
	}

	gitLabScope := "platform/*"
	gitLabExternalID := gitLabInstallationIdentity(gitLabIdentitySecret, "acme", gitLabScope)
	request = httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/installations", strings.NewReader(`{"provider":"gitlab","external_id":"`+gitLabExternalID+`","repository_scope":"`+gitLabScope+`","credential_ref":"gitlab-token"}`))
	request.Header.Set(providerAuthorizationHeader, signedProviderAuthorizationForTest(t, setupSecret, "acme", domain.ProviderGitLab, "gitlab-token:deployment", now.Add(time.Minute)))
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || recording.installationInput.ExternalID != gitLabExternalID {
		t.Fatalf("authorized GitLab installation status=%d input=%#v body=%s", response.Code, recording.installationInput, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/installations", strings.NewReader(`{"provider":"gitlab","external_id":"`+gitLabExternalID+`","repository_scope":"`+gitLabScope+`","author_scope":"mine","author_external_id":"42","credential_ref":"gitlab-token"}`))
	request.Header.Set(providerAuthorizationHeader, signedProviderAuthorizationForTest(t, setupSecret, "acme", domain.ProviderGitLab, "gitlab-token:deployment", now.Add(time.Minute)))
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("deployment token was accepted as user-bound scope: status=%d body=%s", response.Code, response.Body.String())
	}

	oauthRef := "secret://provider/gitlab-oauth/11111111-1111-1111-1111-111111111111"
	request = httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/installations", strings.NewReader(`{"provider":"gitlab","external_id":"`+gitLabExternalID+`","repository_scope":"`+gitLabScope+`","credential_ref":"`+oauthRef+`"}`))
	request.Header.Set(providerAuthorizationHeader, signedProviderAuthorizationWithCredentialForTest(t, setupSecret, "acme", domain.ProviderGitLab, "gitlab-oauth:opaque", oauthRef, now.Add(time.Minute)))
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || recording.installationInput.CredentialRef != oauthRef {
		t.Fatalf("authorized GitLab OAuth installation status=%d input=%#v body=%s", response.Code, recording.installationInput, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/installations", strings.NewReader(`{"provider":"gitlab","external_id":"`+gitLabExternalID+`","repository_scope":"`+gitLabScope+`","credential_ref":"gitlab-token"}`))
	request.Header.Set(providerAuthorizationHeader, signedProviderAuthorizationWithCredentialForTest(t, setupSecret, "acme", domain.ProviderGitLab, "gitlab-oauth:opaque", oauthRef, now.Add(time.Minute)))
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("swapped GitLab OAuth credential status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/installations", strings.NewReader(`{"provider":"gitlab","external_id":"forged","repository_scope":"`+gitLabScope+`","credential_ref":"gitlab-token"}`))
	request.Header.Set(providerAuthorizationHeader, signedProviderAuthorizationForTest(t, setupSecret, "acme", domain.ProviderGitLab, "gitlab-oauth:opaque", now.Add(time.Minute)))
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("forged GitLab identity status=%d body=%s", response.Code, response.Body.String())
	}
}
