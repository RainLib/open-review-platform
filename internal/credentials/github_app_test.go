package credentials

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
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
