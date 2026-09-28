package credentials

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestGitHubAppBrokerMintsAndCachesInstallationToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/app/installations/123/access_tokens" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ey") {
			t.Fatal("expected signed app JWT")
		}
		_, _ = w.Write([]byte(`{"token":"installation-token","expires_at":"2030-01-02T03:04:05Z"}`))
	}))
	defer server.Close()
	broker, err := NewGitHubAppBroker("456", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	broker.now = func() time.Time { return time.Date(2030, 1, 2, 2, 0, 0, 0, time.UTC) }
	first, err := broker.InstallationToken(context.Background(), "123")
	if err != nil || first != "installation-token" {
		t.Fatalf("first token: %q, %v", first, err)
	}
	second, err := broker.InstallationToken(context.Background(), "123")
	if err != nil || second != first || calls != 1 {
		t.Fatalf("cached token=%q calls=%d err=%v", second, calls, err)
	}
}

func TestGitHubAppBrokerMintsOnlyExactRepositoryWriteToken(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/app" {
			_, _ = w.Write([]byte(`{"permissions":{"contents":"write","pull_requests":"write","issues":"write"}}`))
			return
		}
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/app/installations/123/access_tokens" || r.Header.Get("Content-Type") != "application/json" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ey") {
			t.Errorf("unexpected scoped token request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var body struct {
			Repositories []string          `json:"repositories"`
			Permissions  map[string]string `json:"permissions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Repositories) != 1 || body.Repositories[0] != "project" || len(body.Permissions) != 3 || body.Permissions["contents"] != "write" || body.Permissions["pull_requests"] != "write" || body.Permissions["issues"] != "read" {
			t.Errorf("scoped token request is broader than the coding task: %+v err=%v", body, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"token":"repository-token","expires_at":"2099-01-02T03:04:05Z","permissions":{"contents":"write","pull_requests":"write","issues":"read","metadata":"read"},"repository_selection":"selected","repositories":[{"full_name":"acme/project"}]}`))
	}))
	defer server.Close()
	broker := testGitHubAppBroker(t, server.URL)
	token, err := broker.RepositoryWriteToken(context.Background(), "123", "acme/project")
	if err != nil || token != "repository-token" || calls != 1 {
		t.Fatalf("scoped token matched=%t calls=%d err=%v", token == "repository-token", calls, err)
	}
}

func TestGitHubAppBrokerRejectsBroaderRepositoryTokenResponse(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response string
	}{
		{name: "all repositories", response: `{"token":"secret-token","expires_at":"2099-01-02T03:04:05Z","permissions":{"contents":"write","pull_requests":"write","issues":"read"},"repository_selection":"all","repositories":[{"full_name":"acme/project"}]}`},
		{name: "extra repository", response: `{"token":"secret-token","expires_at":"2099-01-02T03:04:05Z","permissions":{"contents":"write","pull_requests":"write","issues":"read"},"repository_selection":"selected","repositories":[{"full_name":"acme/project"},{"full_name":"acme/other"}]}`},
		{name: "wrong repository", response: `{"token":"secret-token","expires_at":"2099-01-02T03:04:05Z","permissions":{"contents":"write","pull_requests":"write","issues":"read"},"repository_selection":"selected","repositories":[{"full_name":"acme/other"}]}`},
		{name: "missing write permission", response: `{"token":"secret-token","expires_at":"2099-01-02T03:04:05Z","permissions":{"contents":"read","pull_requests":"write","issues":"read"},"repository_selection":"selected","repositories":[{"full_name":"acme/project"}]}`},
		{name: "unexpected administration permission", response: `{"token":"secret-token","expires_at":"2099-01-02T03:04:05Z","permissions":{"contents":"write","pull_requests":"write","issues":"read","administration":"write"},"repository_selection":"selected","repositories":[{"full_name":"acme/project"}]}`},
		{name: "expired", response: `{"token":"secret-token","expires_at":"2000-01-02T03:04:05Z","permissions":{"contents":"write","pull_requests":"write","issues":"read"},"repository_selection":"selected","repositories":[{"full_name":"acme/project"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Path == "/app" {
					_, _ = w.Write([]byte(`{"permissions":{"contents":"write","pull_requests":"write","issues":"write"}}`))
					return
				}
				_, _ = w.Write([]byte(tc.response))
			}))
			defer server.Close()
			broker := testGitHubAppBroker(t, server.URL)
			if token, err := broker.RepositoryWriteToken(context.Background(), "123", "acme/project"); err == nil || token != "" || strings.Contains(err.Error(), "secret-token") {
				t.Fatalf("unsafe token accepted or leaked: token-present=%t err=%v", token != "", err)
			}
		})
	}
}

func TestGitHubAppBrokerBlocksRepositoryWriteWhenAppIsReadOnly(t *testing.T) {
	var tokenRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/app" {
			_, _ = w.Write([]byte(`{"permissions":{"contents":"read","pull_requests":"write","issues":"write"}}`))
			return
		}
		tokenRequests++
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	broker := testGitHubAppBroker(t, server.URL)
	if token, err := broker.RepositoryWriteToken(context.Background(), "123", "acme/project"); err == nil || token != "" || !strings.Contains(err.Error(), "Contents:write") || tokenRequests != 0 {
		t.Fatalf("read-only App reached token issuance: token-present=%t requests=%d err=%v", token != "", tokenRequests, err)
	}
}

func TestGitHubAppBrokerRejectsInvalidRepositoryTokenScopeBeforeRequest(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	broker := testGitHubAppBroker(t, server.URL)
	for _, scope := range []struct{ installation, repository string }{
		{"../123", "acme/project"}, {"0", "acme/project"}, {"123", "acme/project/other"},
		{"123", "acme/../project"}, {"123", "acme/project?admin=true"}, {"123", "acme/"},
	} {
		if token, err := broker.RepositoryWriteToken(context.Background(), scope.installation, scope.repository); err == nil || token != "" {
			t.Fatalf("unsafe scope accepted: %+v", scope)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid scope reached GitHub: %d calls", calls)
	}
}

func TestGitHubAppBrokerReadsEventAndPermissionConfiguration(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/app" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ey") {
			t.Fatal("expected signed app JWT")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"events":["issue_comment","issues","pull_request"],"permissions":{"contents":"read","issues":"write"}}`))
	}))
	defer server.Close()
	broker, err := NewGitHubAppBroker("456", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	configuration, err := broker.Configuration(context.Background())
	if err != nil || len(configuration.Events) != 3 || configuration.Permissions["issues"] != "write" {
		t.Fatalf("configuration=%#v error=%v", configuration, err)
	}
}

func TestGitHubAppBrokerClassifiesTemporaryTokenFailures(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		retryAfter string
		transient  bool
		wantDelay  time.Duration
	}{
		{name: "rate limit", status: http.StatusTooManyRequests, retryAfter: "47", transient: true, wantDelay: 47 * time.Second},
		{name: "bounded retry after", status: http.StatusServiceUnavailable, retryAfter: "9999", transient: true, wantDelay: 15 * time.Minute},
		{name: "server error", status: http.StatusBadGateway, transient: true},
		{name: "invalid app authorization", status: http.StatusUnauthorized},
		{name: "installation permission denied", status: http.StatusForbidden},
		{name: "installation removed", status: http.StatusNotFound},
		{name: "redirect", status: http.StatusTemporaryRedirect},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", tc.retryAfter)
				w.WriteHeader(tc.status)
			}))
			defer server.Close()
			broker := testGitHubAppBroker(t, server.URL)
			_, err := broker.InstallationToken(context.Background(), "123")
			if err == nil {
				t.Fatal("token exchange should fail")
			}
			delay, transient := TransientDelay(fmt.Errorf("resolve source credential: %w", err))
			if transient != tc.transient || delay != tc.wantDelay {
				t.Fatalf("transient=%t delay=%s, want transient=%t delay=%s: %v", transient, delay, tc.transient, tc.wantDelay, err)
			}
		})
	}
}

func TestGitHubAppBrokerNeverForwardsAppJWTOnRedirect(t *testing.T) {
	forwarded := false
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		forwarded = true
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	broker := testGitHubAppBroker(t, source.URL)
	if _, err := broker.InstallationToken(context.Background(), "123"); err == nil {
		t.Fatal("redirected installation-token exchange must fail closed")
	}
	if _, err := broker.Configuration(context.Background()); err == nil {
		t.Fatal("redirected App configuration read must fail closed")
	}
	if forwarded {
		t.Fatal("GitHub App JWT was forwarded to a redirect target")
	}
}

func TestGitHubAppBrokerRetriesTransportTimeoutOnlyWhileRequestIsLive(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	broker := testGitHubAppBroker(t, server.URL)
	broker.client.Timeout = 5 * time.Millisecond
	_, err := broker.InstallationToken(context.Background(), "123")
	if _, retryable := TransientDelay(err); !retryable {
		t.Fatalf("transport timeout must be retryable: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = broker.InstallationToken(ctx, "123")
	if _, retryable := TransientDelay(err); retryable {
		t.Fatalf("cancelled parent task must not schedule a retry: %v", err)
	}
}

func testGitHubAppBroker(t *testing.T, endpoint string) *GitHubAppBroker {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	broker, err := NewGitHubAppBroker("456", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}), endpoint)
	if err != nil {
		t.Fatal(err)
	}
	return broker
}

// This opt-in read-only smoke check mints one real installation token without
// printing it or contacting a repository. It is never enabled in CI by default.
func TestLiveGitHubAppInstallationToken(t *testing.T) {
	if os.Getenv("OPENREVIEW_TEST_LIVE_GITHUB_APP") != "1" {
		t.Skip("set OPENREVIEW_TEST_LIVE_GITHUB_APP=1 for the external App token smoke test")
	}
	broker, err := NewGitHubAppBrokerFromFile(os.Getenv("GITHUB_APP_ID"), os.Getenv("GITHUB_APP_PRIVATE_KEY_PATH"), os.Getenv("GITHUB_APP_API_URL"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	token, err := broker.InstallationToken(ctx, os.Getenv("GITHUB_APP_INSTALLATION_ID"))
	if err != nil || token == "" {
		t.Fatalf("live GitHub App installation token unavailable: %v", err)
	}
}

// Opt-in, read-only acceptance check: mint the same down-scoped credential a
// future isolated broker would deliver to one approved coding attempt. This
// does not clone, push, open a PR, or expose the token in test output.
func TestLiveGitHubAppRepositoryWriteToken(t *testing.T) {
	if os.Getenv("OPENREVIEW_TEST_LIVE_GITHUB_REPOSITORY_TOKEN") != "1" {
		t.Skip("set OPENREVIEW_TEST_LIVE_GITHUB_REPOSITORY_TOKEN=1 for the external scoped-token smoke test")
	}
	broker, err := NewGitHubAppBrokerFromFile(os.Getenv("GITHUB_APP_ID"), os.Getenv("GITHUB_APP_PRIVATE_KEY_PATH"), os.Getenv("GITHUB_APP_API_URL"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	token, err := broker.RepositoryWriteToken(ctx, os.Getenv("GITHUB_APP_INSTALLATION_ID"), os.Getenv("GITHUB_APP_REPOSITORY"))
	if err != nil || token == "" {
		t.Fatalf("repository-scoped GitHub App token could not be minted: %v", err)
	}
}

// This reports only the public App permission declaration, never JWTs,
// private keys, installation tokens, or repository content.
func TestLiveGitHubAppPermissionDeclaration(t *testing.T) {
	if os.Getenv("OPENREVIEW_TEST_LIVE_GITHUB_APP_PERMISSIONS") != "1" {
		t.Skip("set OPENREVIEW_TEST_LIVE_GITHUB_APP_PERMISSIONS=1 for the external App permission check")
	}
	broker, err := NewGitHubAppBrokerFromFile(os.Getenv("GITHUB_APP_ID"), os.Getenv("GITHUB_APP_PRIVATE_KEY_PATH"), os.Getenv("GITHUB_APP_API_URL"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	configuration, err := broker.Configuration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("App repository permissions: contents=%q pull_requests=%q issues=%q", configuration.Permissions["contents"], configuration.Permissions["pull_requests"], configuration.Permissions["issues"])
}
