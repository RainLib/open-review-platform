package credentials

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/config"
	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/providercredentials"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

type oauthCredentialStore struct {
	credential domain.ProviderOAuthCredential
	tenantID   uuid.UUID
}

func (s *oauthCredentialStore) LoadProviderOAuthCredential(_ context.Context, tenantID uuid.UUID, ref string) (domain.ProviderOAuthCredential, error) {
	if tenantID != s.tenantID || ref != s.credential.CredentialRef {
		return domain.ProviderOAuthCredential{}, store.ErrNotFound
	}
	return s.credential, nil
}

func (s *oauthCredentialStore) RefreshProviderOAuthCredential(_ context.Context, tenantID uuid.UUID, ref string, refresh domain.ProviderOAuthCredentialRefresh) (bool, error) {
	if tenantID != s.tenantID || ref != s.credential.CredentialRef || string(s.credential.AccessTokenCiphertext) != string(refresh.ExpectedAccessTokenCiphertext) {
		return false, nil
	}
	s.credential.AccessTokenCiphertext = refresh.AccessTokenCiphertext
	if len(refresh.RefreshTokenCiphertext) > 0 {
		s.credential.RefreshTokenCiphertext = refresh.RefreshTokenCiphertext
	}
	s.credential.ExpiresAt = ptr(refresh.ExpiresAt)
	return true, nil
}

func TestProviderResolverUsesEncryptedGitLabOAuthCredential(t *testing.T) {
	key := base64.RawStdEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
	cipher, err := providercredentials.New(key)
	if err != nil {
		t.Fatal(err)
	}
	ref := "secret://provider/gitlab-oauth/11111111-1111-1111-1111-111111111111"
	sealed, err := cipher.Seal(ref, "oauth-token")
	if err != nil {
		t.Fatal(err)
	}
	tenantID := uuid.New()
	resolver, err := New(config.Config{ProviderCredentialEncryptionKey: key}, &oauthCredentialStore{tenantID: tenantID, credential: domain.ProviderOAuthCredential{CredentialRef: ref, Provider: domain.ProviderGitLab, AccessTokenCiphertext: sealed, ExpiresAt: ptr(time.Now().UTC().Add(time.Minute))}})
	if err != nil {
		t.Fatal(err)
	}
	token, err := resolver.Resolve(context.Background(), domain.ReviewJob{TenantID: tenantID, Provider: domain.ProviderGitLab, CredentialRef: ref})
	if err != nil || token != "oauth-token" {
		t.Fatalf("token=%q err=%v", token, err)
	}
	if _, err := resolver.Resolve(context.Background(), domain.ReviewJob{Provider: domain.ProviderGitLab, CredentialRef: ref}); err == nil {
		t.Fatal("OAuth credential resolved without a tenant boundary")
	}
}

func TestProviderResolverRefreshesExpiredGitLabOAuthCredential(t *testing.T) {
	key := base64.RawStdEncoding.EncodeToString([]byte(strings.Repeat("c", 32)))
	cipher, err := providercredentials.New(key)
	if err != nil {
		t.Fatal(err)
	}
	ref := "secret://provider/gitlab-oauth/22222222-2222-2222-2222-222222222222"
	access, err := cipher.Seal(ref, "expired-access-token")
	if err != nil {
		t.Fatal(err)
	}
	refresh, err := cipher.Seal(ref, "refresh-token")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/oauth/token" || request.Method != http.MethodPost {
			t.Fatalf("unexpected refresh request %s %s", request.Method, request.URL.Path)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		form, err := url.ParseQuery(string(body))
		if err != nil {
			t.Fatal(err)
		}
		if form.Get("grant_type") != "refresh_token" || form.Get("refresh_token") != "refresh-token" || form.Get("client_id") != "worker-client" || form.Get("client_secret") != "worker-secret" {
			t.Fatalf("unexpected refresh form: %#v", form)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"access_token":"refreshed-access-token","refresh_token":"rotated-refresh-token","expires_in":3600}`)
	}))
	defer server.Close()
	tenantID := uuid.New()
	credentialStore := &oauthCredentialStore{tenantID: tenantID, credential: domain.ProviderOAuthCredential{
		CredentialRef: ref, Provider: domain.ProviderGitLab, AccessTokenCiphertext: access, RefreshTokenCiphertext: refresh, ExpiresAt: ptr(time.Now().UTC().Add(-time.Minute)),
	}}
	resolver, err := New(config.Config{
		Environment:                     "development",
		ProviderCredentialEncryptionKey: key,
		GitLab: config.GitLabConfig{
			OAuthBaseURL:      server.URL,
			OAuthClientID:     "worker-client",
			OAuthClientSecret: "worker-secret",
		},
	}, credentialStore)
	if err != nil {
		t.Fatal(err)
	}
	token, err := resolver.Resolve(context.Background(), domain.ReviewJob{TenantID: tenantID, Provider: domain.ProviderGitLab, CredentialRef: ref})
	if err != nil || token != "refreshed-access-token" {
		t.Fatalf("token=%q err=%v", token, err)
	}
	storedToken, err := cipher.Open(ref, credentialStore.credential.AccessTokenCiphertext)
	if err != nil || storedToken != "refreshed-access-token" || credentialStore.credential.ExpiresAt == nil || !credentialStore.credential.ExpiresAt.After(time.Now().UTC()) {
		t.Fatalf("stored credential=%#v token=%q err=%v", credentialStore.credential, storedToken, err)
	}
}

func TestProviderResolverUsesConcurrentGitLabOAuthRefreshWinner(t *testing.T) {
	key := base64.RawStdEncoding.EncodeToString([]byte(strings.Repeat("d", 32)))
	cipher, err := providercredentials.New(key)
	if err != nil {
		t.Fatal(err)
	}
	ref := "secret://provider/gitlab-oauth/33333333-3333-3333-3333-333333333333"
	expiredAccess, err := cipher.Seal(ref, "expired-access-token")
	if err != nil {
		t.Fatal(err)
	}
	refresh, err := cipher.Seal(ref, "stale-refresh-token")
	if err != nil {
		t.Fatal(err)
	}
	winnerAccess, err := cipher.Seal(ref, "concurrent-winner-access-token")
	if err != nil {
		t.Fatal(err)
	}
	tenantID := uuid.New()
	credentialStore := &oauthCredentialStore{tenantID: tenantID, credential: domain.ProviderOAuthCredential{
		CredentialRef: ref, Provider: domain.ProviderGitLab, AccessTokenCiphertext: expiredAccess, RefreshTokenCiphertext: refresh, ExpiresAt: ptr(time.Now().UTC().Add(-time.Minute)),
	}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		credentialStore.credential.AccessTokenCiphertext = winnerAccess
		credentialStore.credential.ExpiresAt = ptr(time.Now().UTC().Add(time.Hour))
		w.WriteHeader(http.StatusBadRequest) // another worker already rotated the refresh token
	}))
	defer server.Close()
	resolver, err := New(config.Config{
		Environment:                     "development",
		ProviderCredentialEncryptionKey: key,
		GitLab: config.GitLabConfig{
			OAuthBaseURL: server.URL, OAuthClientID: "worker-client", OAuthClientSecret: "worker-secret",
		},
	}, credentialStore)
	if err != nil {
		t.Fatal(err)
	}
	token, err := resolver.Resolve(context.Background(), domain.ReviewJob{TenantID: tenantID, Provider: domain.ProviderGitLab, CredentialRef: ref})
	if err != nil || token != "concurrent-winner-access-token" {
		t.Fatalf("token=%q err=%v", token, err)
	}
}
func ptr[T any](value T) *T { return &value }
