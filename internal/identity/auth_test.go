package identity

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/config"
)

func TestOIDCDiscoveryIsLazyAndAuthenticationFailsClosed(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var available atomic.Bool
	var discoveries atomic.Int32
	var issuer string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			discoveries.Add(1)
			if !available.Load() {
				http.Error(w, "unavailable", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer": issuer, "jwks_uri": issuer + "/jwks",
				"authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token",
				"response_types_supported":              []string{"code"},
				"subject_types_supported":               []string{"public"},
				"id_token_signing_alg_values_supported": []string{"RS256"},
			})
		case "/jwks":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{
				"kty": "RSA", "kid": "test", "alg": "RS256", "use": "sig",
				"n": base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes()),
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	issuer = server.URL

	auth, err := New(context.Background(), config.AuthConfig{Mode: "oidc", Issuer: issuer, Audience: "console"})
	if err != nil {
		t.Fatal(err)
	}
	if discoveries.Load() != 0 {
		t.Fatal("construction must not depend on provider availability")
	}
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	if _, err := auth.Authenticate(context.Background(), request); err == nil || discoveries.Load() != 0 {
		t.Fatal("missing bearer must fail without contacting issuer")
	}
	request.Header.Set("Authorization", "Bearer invalid")
	if _, err := auth.Authenticate(context.Background(), request); err == nil || discoveries.Load() != 1 {
		t.Fatal("unavailable issuer must reject bearer and attempt discovery")
	}
	if _, err := auth.Authenticate(context.Background(), request); err == nil || discoveries.Load() != 1 {
		t.Fatal("discovery backoff must fail closed without retry storm")
	}

	available.Store(true)
	// Elapse the bounded backoff without sleeping ten seconds.
	oidcAuth := auth.(*oidcAuthenticator)
	oidcAuth.mu.Lock()
	oidcAuth.nextDiscovery = time.Time{}
	oidcAuth.mu.Unlock()
	if _, err := auth.Authenticate(context.Background(), request); err == nil || discoveries.Load() != 2 {
		t.Fatal("provider recovery must not accept an invalid token")
	}
	request.Header.Set("Authorization", "Bearer "+signedOIDCTestToken(t, key, issuer, "console"))
	principal, err := auth.Authenticate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if principal.Subject != "user-1" || principal.Email != "user@example.test" || len(principal.Groups) != 1 || principal.Groups[0] != "reviewers" {
		t.Fatalf("unexpected verified principal: %+v", principal)
	}
	request.Header.Set("Authorization", "Bearer "+signedOIDCTestToken(t, key, issuer, "wrong-client"))
	if _, err := auth.Authenticate(context.Background(), request); err == nil {
		t.Fatal("valid signature with wrong audience must fail")
	}
}

func TestOIDCRequiresIssuerAndAudience(t *testing.T) {
	for _, cfg := range []config.AuthConfig{
		{Mode: "oidc", Audience: "console"},
		{Mode: "oidc", Issuer: "https://issuer.example.test"},
	} {
		if _, err := New(context.Background(), cfg); err == nil {
			t.Fatalf("expected invalid config rejection: %+v", cfg)
		}
	}
}

func signedOIDCTestToken(t *testing.T, key *rsa.PrivateKey, issuer, audience string) string {
	t.Helper()
	header, err := json.Marshal(map[string]any{"alg": "RS256", "typ": "JWT", "kid": "test"})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := json.Marshal(map[string]any{
		"iss": issuer, "aud": audience, "exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Add(-time.Minute).Unix(), "sub": "user-1",
		"email": "user@example.test", "groups": []string{"reviewers"},
	})
	if err != nil {
		t.Fatal(err)
	}
	message := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(message))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%s.%s", message, base64.RawURLEncoding.EncodeToString(signature))
}
