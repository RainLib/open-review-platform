package sso

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

func TestProbeClientValidatesOIDCMetadataAndHashesReceipt(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":%q,"jwks_uri":%q}`, server.URL, server.URL+"/authorize", server.URL+"/token", server.URL+"/jwks")
	}))
	defer server.Close()

	client := ProbeClient{AllowPrivateNetworks: true, HTTPClient: server.Client()}
	hash, code, message := client.Probe(context.Background(), domain.SSOProbeTarget{Protocol: domain.SSOProtocolOIDC, TargetURL: server.URL, ExpectedClientID: "open-review"})
	if code != "" || message != "" || len(hash) != 64 {
		t.Fatalf("hash=%q code=%q message=%q", hash, code, message)
	}
}

func TestProbeClientRejectsMismatchedIssuer(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"issuer":"https://attacker.example","authorization_endpoint":"https://id.example/authorize","token_endpoint":"https://id.example/token","jwks_uri":"https://id.example/jwks"}`))
	}))
	defer server.Close()
	client := ProbeClient{AllowPrivateNetworks: true, HTTPClient: server.Client()}
	_, code, _ := client.Probe(context.Background(), domain.SSOProbeTarget{Protocol: domain.SSOProtocolOIDC, TargetURL: server.URL})
	if code != "invalid_metadata" {
		t.Fatalf("code=%q, want invalid_metadata", code)
	}
}

type probeStoreStub struct {
	target    *domain.SSOProbeTarget
	completed bool
}

func (s *probeStoreStub) ClaimSSOProbe(context.Context, string) (*domain.SSOProbeTarget, error) {
	return s.target, nil
}

func (s *probeStoreStub) CompleteSSOProbe(_ context.Context, id uuid.UUID, _ string, hash, code, _ string) error {
	s.completed = id == s.target.ReceiptID && len(hash) == 64 && code == ""
	return nil
}

func TestProcessorCompletesClaimedProbe(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":%q,"jwks_uri":%q}`, server.URL, server.URL+"/authorize", server.URL+"/token", server.URL+"/jwks")
	}))
	defer server.Close()
	backend := &probeStoreStub{target: &domain.SSOProbeTarget{ReceiptID: uuid.New(), Protocol: domain.SSOProtocolOIDC, TargetURL: server.URL}}
	worked, err := (Processor{Store: backend, Client: ProbeClient{AllowPrivateNetworks: true, HTTPClient: server.Client()}, WorkerID: "worker-test"}).RunOnce(context.Background())
	if err != nil || !worked || !backend.completed {
		t.Fatalf("worked=%v completed=%v error=%v", worked, backend.completed, err)
	}
}
