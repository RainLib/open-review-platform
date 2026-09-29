package modelprobe

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

type resolverStub struct{ token string }

func (r resolverStub) ResolveModelCredential(context.Context, string) (string, error) {
	return r.token, nil
}

func testRoute(endpoint string) domain.ModelRouteConfig {
	return domain.ModelRouteConfig{Enabled: true, Provider: "openai-compatible", Protocol: "openai-chat", BaseURL: endpoint, Model: "deepseek-v4-flash", CredentialRef: "env://OPEN_REVIEW_MODEL_SECRET_TEST", Effort: "low", MaxPromptTokens: 8000, TokenBudget: 128000, SubtaskTimeoutMinutes: 5, MaxConcurrentRuns: 2}
}

func TestProbeUsesBoundedPayloadAndRetainsOnlyDigest(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("authorization=%q", got)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "Reply exactly OK.") || !strings.Contains(string(body), `"max_tokens":4`) {
			t.Fatalf("unexpected probe body=%s", body)
		}
		_, _ = w.Write([]byte(`{"id":"probe-response","choices":[{"message":{"content":"OK"}}]}`))
	}))
	defer server.Close()
	result := (Client{Resolver: resolverStub{token: "test-token"}, HTTPClient: server.Client()}).Probe(context.Background(), domain.ModelProbeTarget{Route: testRoute(server.URL)})
	if result.ErrorCode != "" || len(result.ResponseSHA256) != 64 || result.LatencyMS < 0 {
		t.Fatalf("result=%#v", result)
	}
}

func TestProbeRedactsCredentialResolutionFailure(t *testing.T) {
	result := (Client{}).Probe(context.Background(), domain.ModelProbeTarget{Route: testRoute("https://models.example/v1/chat/completions")})
	if result.ErrorCode != "credential_unavailable" || strings.Contains(result.ErrorMessage, "credential_ref") {
		t.Fatalf("result=%#v", result)
	}
}

type probeStoreStub struct {
	target    *domain.ModelProbeTarget
	completed bool
}

func (s *probeStoreStub) ClaimModelProbe(context.Context, string) (*domain.ModelProbeTarget, error) {
	return s.target, nil
}
func (s *probeStoreStub) CompleteModelProbe(_ context.Context, id uuid.UUID, _ string, result domain.ModelProbeResult) error {
	s.completed = id == s.target.ReceiptID && result.ErrorCode == "" && len(result.ResponseSHA256) == 64
	return nil
}

func TestProcessorCompletesClaimedProbe(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"id":"probe-response"}`)) }))
	defer server.Close()
	backend := &probeStoreStub{target: &domain.ModelProbeTarget{ReceiptID: uuid.New(), Route: testRoute(server.URL)}}
	worked, err := (Processor{Store: backend, Client: Client{Resolver: resolverStub{token: "test-token"}, HTTPClient: server.Client()}, WorkerID: "worker-test"}).RunOnce(context.Background())
	if err != nil || !worked || !backend.completed {
		t.Fatalf("worked=%v completed=%v error=%v", worked, backend.completed, err)
	}
}
