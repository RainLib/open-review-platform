package agentadapter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestModelReadinessRequiresCompleteResponseAndKeepsDenialCode(t *testing.T) {
	for _, tc := range []struct {
		name, wire, body string
		status           int
		want             bool
	}{
		{"responses ready", "responses", `{"status":"completed"}`, 200, true},
		{"anthropic ready", "anthropic", `{"type":"message","stop_reason":"end_turn"}`, 200, true},
		{"denied", "anthropic", `{"code":"AccessDenied.Unpurchased","message":"synthetic-private"}`, 403, false},
		{"empty success", "responses", `{}`, 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer upstream.Close()
			config := ModelBrokerConfig{APIBaseURL: upstream.URL + "/v1", APIKey: "secret", Model: "fixed", WireAPI: tc.wire, HTTPClient: upstream.Client()}
			pipeline := Pipeline{ExecutorKind: "codex", CodeModelBroker: config}
			if tc.wire == "anthropic" {
				pipeline.ExecutorKind = "claude"
				pipeline.ClaudeModelBroker = config
			}
			err := pipeline.CheckExecutionReady(context.Background())
			if (err == nil) != tc.want {
				t.Fatalf("readiness=%v", err)
			}
			if tc.status == 403 {
				code, summary := executionFailureResult(err)
				if code != "agent_adapter_model_upstream_failed" || !strings.Contains(summary, "AccessDenied.Unpurchased") || strings.Contains(summary, "synthetic-private") {
					t.Fatalf("%s %s", code, summary)
				}
			}
		})
	}
	completed := "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"
	if !completeReadinessResponse("responses", []byte(completed), true) || completeReadinessResponse("responses", []byte(completed+"data: {\"type\":\"error\"}\n\n"), true) {
		t.Fatal("accepted a later stream error or rejected complete stream")
	}
}

type readinessExecutor struct {
	calls                  atomic.Int32
	readyErr, executionErr error
}

func (e *readinessExecutor) CheckExecutionReady(context.Context) error {
	e.calls.Add(1)
	return e.readyErr
}
func (e *readinessExecutor) Execute(context.Context, string, Submission) (ExecutionResult, error) {
	return ExecutionResult{}, e.executionErr
}

func TestReadinessDoesNotSpendModelTokensForHeartbeatsAndLatchesAccessDenial(t *testing.T) {
	for _, denied := range []bool{false, true} {
		executor := &readinessExecutor{}
		if denied {
			executor.readyErr = &modelUpstreamFailure{status: 403, errorType: "AccessDenied.Unpurchased", cause: errors.New("private")}
		}
		service, err := NewService(ServiceConfig{Secret: "0123456789abcdef0123456789abcdef", CallbackURL: "https://example.invalid/events", StartGateURL: "https://example.invalid/starts", Executor: executor, ReceiptDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(service.Handler())
		client, err := NewClient(server.URL, "0123456789abcdef0123456789abcdef", "https://example.invalid/events", time.Second, true)
		if err != nil {
			t.Fatal(err)
		}
		if err := client.Probe(context.Background()); err != nil || executor.calls.Load() != 0 {
			t.Fatalf("heartbeat ran model: %v calls=%d", err, executor.calls.Load())
		}
		for i := 0; i < 3; i++ {
			err := client.CheckExecutionReady(context.Background())
			if (err != nil) != denied {
				t.Fatalf("readiness=%v denied=%t", err, denied)
			}
		}
		if executor.calls.Load() != 1 {
			t.Fatalf("readiness was not cached/latched: %d", executor.calls.Load())
		}
		if err := client.Probe(context.Background()); (err != nil) != denied {
			t.Fatalf("heartbeat did not reflect denial latch: %v", err)
		}
		server.Close()
		_ = service.Close()
	}
}

func TestExecutionAccessDenialInvalidatesPreviouslySuccessfulReadiness(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"
	terminal := make(chan struct{}, 1)
	callback := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
		if r.URL.Path == "/events" {
			select {
			case terminal <- struct{}{}:
			default:
			}
		}
	}))
	defer callback.Close()
	executor := &readinessExecutor{executionErr: &modelUpstreamFailure{status: 403, errorType: "AccessDenied.Unpurchased", cause: errors.New("private")}}
	service, err := NewService(ServiceConfig{Secret: secret, CallbackURL: callback.URL + "/events", StartGateURL: callback.URL + "/starts", Executor: executor, ReceiptDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	service.callbackClient = callback.Client()
	server := httptest.NewServer(service.Handler())
	defer server.Close()
	client, err := NewClient(server.URL, secret, callback.URL+"/events", time.Second, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.CheckExecutionReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	attempt := uuid.New()
	job := submitAdapterRequest(t, secret, server.URL+"/v1/open-review/tasks", testSubmission(attempt, uuid.New(), callback.URL+"/events"))
	startAdapterRequest(t, secret, server.URL, job, attempt)
	select {
	case <-terminal:
	case <-time.After(3 * time.Second):
		t.Fatal("no failure callback")
	}
	if err := client.CheckExecutionReady(context.Background()); err == nil || executor.calls.Load() != 1 {
		t.Fatalf("reused successful cache after execution denial: %v calls=%d", err, executor.calls.Load())
	}
	service.mu.Lock()
	retainedJob := service.byJob[job]
	service.mu.Unlock()
	retainedJob.stateMu.Lock()
	event := retainedJob.terminal
	retainedJob.stateMu.Unlock()
	if event == nil || event.Kind != "needs_attention" || event.ErrorCode != "agent_adapter_model_upstream_failed" || !strings.Contains(event.Summary, "AccessDenied.Unpurchased") {
		t.Fatalf("terminal=%+v", event)
	}
}
