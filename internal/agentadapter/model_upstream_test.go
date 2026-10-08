package agentadapter

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func brokerRequest(t *testing.T, broker *modelBroker, path, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, broker.URL()+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+broker.token)
	request.Header.Set("X-Api-Key", broker.token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	return response
}

func TestModelBrokerRetriesDefiniteTransientFailuresWithFixedBodyAndBudgets(t *testing.T) {
	for _, wire := range []string{"responses", "anthropic"} {
		t.Run(wire, func(t *testing.T) {
			var calls atomic.Int32
			var first []byte
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				call := calls.Add(1)
				if call == 1 {
					first = body
				} else if !bytes.Equal(first, body) {
					t.Error("retry changed the fixed request")
				}
				if call == 1 {
					w.WriteHeader(429)
					_, _ = w.Write([]byte(`{"error":{"type":"rate_limit_error"}}`))
					return
				}
				if call == 2 {
					w.WriteHeader(503)
					_, _ = w.Write([]byte(`{"error":{"type":"overloaded_error"}}`))
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if wire == "anthropic" {
					_, _ = w.Write([]byte(`{"type":"message","stop_reason":"end_turn","usage":{"output_tokens":1}}`))
				} else {
					_, _ = w.Write([]byte(`{"status":"completed"}`))
				}
			}))
			defer upstream.Close()
			broker, err := startModelBroker(context.Background(), ModelBrokerConfig{APIBaseURL: upstream.URL + "/v1", APIKey: "synthetic-secret", Model: "fixed", WireAPI: wire, HTTPClient: upstream.Client()})
			if err != nil {
				t.Fatal(err)
			}
			defer broker.Close()
			path, body := "/responses", `{"input":"hello","max_output_tokens":64}`
			if wire == "anthropic" {
				path, body = "/messages", `{"messages":[{"role":"user","content":"hello"}],"max_tokens":64}`
			}
			response := brokerRequest(t, broker, path, body)
			wantTokens := int64(192)
			if wire == "anthropic" {
				wantTokens = 129
			} // Failed calls retain reservations; only final valid usage is refunded.
			if response.StatusCode != 200 || calls.Load() != 3 || broker.requests.Load() != 3 || broker.outputTokens.Load() != wantTokens || broker.requestBytes.Load() < int64(len(first)*2) {
				t.Fatalf("status=%d calls=%d requests=%d tokens=%d bytes=%d", response.StatusCode, calls.Load(), broker.requests.Load(), broker.outputTokens.Load(), broker.requestBytes.Load())
			}
		})
	}
}

func TestModelBrokerPermanentDenialBreaksCircuitAndSanitizesDiagnostics(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"code":"AccessDenied.Unpurchased","message":"synthetic-private-content"}`))
	}))
	defer upstream.Close()
	broker, err := startModelBroker(context.Background(), ModelBrokerConfig{APIBaseURL: upstream.URL + "/v1", APIKey: "secret", Model: "fixed", HTTPClient: upstream.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	for i := 0; i < 5; i++ {
		if response := brokerRequest(t, broker, "/responses", `{"input":"hello","max_output_tokens":64}`); response.StatusCode != 403 {
			t.Fatalf("status=%d", response.StatusCode)
		}
	}
	code, summary := executionFailureResult(codingModelFailure(errors.New("synthetic-private-output"), broker))
	if calls.Load() != 1 || broker.requests.Load() != 1 || code != "agent_adapter_model_upstream_failed" || !strings.Contains(summary, "AccessDenied.Unpurchased") || strings.Contains(summary, "synthetic-private") {
		t.Fatalf("calls=%d requests=%d %s %s", calls.Load(), broker.requests.Load(), code, summary)
	}
	for _, body := range []string{`{"code":"synthetic-private"}`, `{"error":{"type":"synthetic-private"}}`, `invalid`} {
		if got := modelUpstreamErrorType([]byte(body)); got != "unknown" {
			t.Fatalf("untrusted error enum escaped: %s", got)
		}
	}
}

func TestModelBrokerRetriesCannotOverrunExistingRequestBudget(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(503) }))
	defer upstream.Close()
	broker, err := startModelBroker(context.Background(), ModelBrokerConfig{APIBaseURL: upstream.URL + "/v1", APIKey: "secret", Model: "fixed", HTTPClient: upstream.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	broker.requests.Store(modelBrokerRequestLimit - 2)
	response := brokerRequest(t, broker, "/responses", `{"input":"hello","max_output_tokens":64}`)
	code, _ := executionFailureResult(codingModelFailure(errors.New("failed"), broker))
	if response.StatusCode != 503 || calls.Load() != 2 || code != "agent_adapter_model_budget_exhausted" {
		t.Fatalf("status=%d calls=%d code=%s", response.StatusCode, calls.Load(), code)
	}
}

type failedModelTransport struct{ calls *atomic.Int32 }

func (transport failedModelTransport) RoundTrip(*http.Request) (*http.Response, error) {
	transport.calls.Add(1)
	return nil, errors.New("connection uncertain")
}

func TestModelBrokerDoesNotReplayUncertainTransportOrSuccessfulStream(t *testing.T) {
	var calls atomic.Int32
	broker, err := startModelBroker(context.Background(), ModelBrokerConfig{APIBaseURL: "https://model.invalid/v1", APIKey: "secret", Model: "fixed", HTTPClient: &http.Client{Transport: failedModelTransport{&calls}}})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	if response := brokerRequest(t, broker, "/responses", `{"input":"hello","max_output_tokens":64}`); response.StatusCode != 502 || calls.Load() != 1 {
		t.Fatalf("status=%d calls=%d", response.StatusCode, calls.Load())
	}
	stream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.failed\"}\n\n"))
	}))
	defer stream.Close()
	streamBroker, err := startModelBroker(context.Background(), ModelBrokerConfig{APIBaseURL: stream.URL + "/v1", APIKey: "secret", Model: "fixed", HTTPClient: stream.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer streamBroker.Close()
	if response := brokerRequest(t, streamBroker, "/responses", `{"input":"hello","max_output_tokens":64}`); response.StatusCode != 200 || calls.Load() != 2 {
		t.Fatalf("status=%d calls=%d", response.StatusCode, calls.Load())
	}
}

func TestModelRetryHonorsBoundedRetryAfterAndCancellation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		value string
		want  time.Duration
		ok    bool
	}{{"2", 2 * time.Second, true}, {now.Add(3 * time.Second).Format(http.TimeFormat), 3 * time.Second, true}, {"6", 0, false}, {"bad", 0, false}, {"-1", 0, false}} {
		got, ok := modelRetryDelay(429, tc.value, 0, now)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("%q: delay=%s ok=%t", tc.value, got, ok)
		}
	}
	for _, status := range []int{200, 400, 401, 403, 404, 307} {
		if _, ok := modelRetryDelay(status, "", 0, now); ok {
			t.Fatalf("retried %d", status)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitModelRetry(ctx, 5*time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
}

func TestModelBrokerExhaustedTransientStopsSDKRetriesAndCoding(t *testing.T) {
	for _, wire := range []string{"responses", "anthropic"} {
		for _, status := range []int{429, 503} {
			t.Run(wire+"/"+http.StatusText(status), func(t *testing.T) {
				var calls atomic.Int32
				upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					calls.Add(1)
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"error":{"type":"rate_limit_error","message":"synthetic-private"}}`))
				}))
				defer upstream.Close()
				broker, err := startModelBroker(context.Background(), ModelBrokerConfig{APIBaseURL: upstream.URL + "/v1", APIKey: "secret", Model: "fixed", WireAPI: wire, HTTPClient: upstream.Client()})
				if err != nil {
					t.Fatal(err)
				}
				defer broker.Close()
				parent, cancel := context.WithCancel(context.Background())
				defer cancel()
				coding, release := broker.codingContext(parent)
				defer release()
				path, body := "/responses", `{"input":"hello","max_output_tokens":64}`
				if wire == "anthropic" {
					path, body = "/messages", `{"messages":[{"role":"user","content":"hello"}],"max_tokens":64}`
				}
				for i := 0; i < 8; i++ {
					if response := brokerRequest(t, broker, path, body); response.StatusCode != status {
						t.Fatalf("status=%d", response.StatusCode)
					}
				}
				select {
				case <-coding.Done():
				case <-time.After(time.Second):
					t.Fatal("terminal failure left coding alive")
				}
				if parent.Err() != nil {
					t.Fatal("coding cancellation also cancelled trusted parent work")
				}
				code, summary := executionFailureResult(codingModelFailure(nil, broker))
				if calls.Load() != 3 || broker.requests.Load() != 3 || broker.outputTokens.Load() != 192 || code != "agent_adapter_model_upstream_failed" || strings.Contains(summary, "synthetic-private") {
					t.Fatalf("calls=%d requests=%d tokens=%d code=%s summary=%s", calls.Load(), broker.requests.Load(), broker.outputTokens.Load(), code, summary)
				}
			})
		}
	}
}

func TestModelBrokerUncertainTransportStopsSDKAndAlreadyBlockedCoding(t *testing.T) {
	var calls atomic.Int32
	broker, err := startModelBroker(context.Background(), ModelBrokerConfig{APIBaseURL: "https://model.invalid/v1", APIKey: "secret", Model: "fixed", HTTPClient: &http.Client{Transport: failedModelTransport{&calls}}})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	for i := 0; i < 3; i++ {
		if response := brokerRequest(t, broker, "/responses", `{"input":"hello","max_output_tokens":64}`); response.StatusCode != 502 {
			t.Fatalf("status=%d", response.StatusCode)
		}
	}
	coding, cancel := broker.codingContext(context.Background())
	defer cancel()
	if calls.Load() != 1 || broker.requests.Load() != 1 || coding.Err() != context.Canceled {
		t.Fatalf("calls=%d requests=%d context=%v", calls.Load(), broker.requests.Load(), coding.Err())
	}
	code, summary := executionFailureResult(codingModelFailure(nil, broker))
	if code != "agent_adapter_model_upstream_failed" || !strings.Contains(summary, "transport failure") {
		t.Fatalf("%s %s", code, summary)
	}
}

type interruptedModelBody struct{}

func (interruptedModelBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

type interruptedModelTransport struct{ calls *atomic.Int32 }

func (transport interruptedModelTransport) RoundTrip(*http.Request) (*http.Response, error) {
	transport.calls.Add(1)
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(io.MultiReader(strings.NewReader("data: {}\n\n"), interruptedModelBody{}))}, nil
}

func TestModelBrokerInterruptedStreamStopsCodingWithoutReplay(t *testing.T) {
	for _, wire := range []string{"responses", "anthropic"} {
		t.Run(wire, func(t *testing.T) {
			var calls atomic.Int32
			broker, err := startModelBroker(context.Background(), ModelBrokerConfig{APIBaseURL: "https://model.invalid/v1", APIKey: "secret", Model: "fixed", WireAPI: wire, HTTPClient: &http.Client{Transport: interruptedModelTransport{&calls}}})
			if err != nil {
				t.Fatal(err)
			}
			defer broker.Close()
			coding, cancel := broker.codingContext(context.Background())
			defer cancel()
			path, body := "/responses", `{"input":"hello","max_output_tokens":64}`
			if wire == "anthropic" {
				path, body = "/messages", `{"messages":[{"role":"user","content":"hello"}],"max_tokens":64}`
			}
			if response := brokerRequest(t, broker, path, body); response.StatusCode != 200 {
				t.Fatalf("initial stream status=%d", response.StatusCode)
			}
			if response := brokerRequest(t, broker, path, body); response.StatusCode != 502 {
				t.Fatalf("repeated stream status=%d", response.StatusCode)
			}
			select {
			case <-coding.Done():
			case <-time.After(time.Second):
				t.Fatal("interrupted stream left coding alive")
			}
			if calls.Load() != 1 || broker.requests.Load() != 1 || broker.outputTokens.Load() != 64 {
				t.Fatalf("calls=%d requests=%d tokens=%d", calls.Load(), broker.requests.Load(), broker.outputTokens.Load())
			}
		})
	}
}

func TestModelBrokerConcurrentSDKRetriesShareTerminalLatch(t *testing.T) {
	for _, wire := range []string{"responses", "anthropic"} {
		t.Run(wire, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(429) }))
			defer upstream.Close()
			broker, err := startModelBroker(context.Background(), ModelBrokerConfig{APIBaseURL: upstream.URL + "/v1", APIKey: "secret", Model: "fixed", WireAPI: wire, HTTPClient: upstream.Client()})
			if err != nil {
				t.Fatal(err)
			}
			defer broker.Close()
			path, body := "/responses", `{"input":"hello","max_output_tokens":64}`
			if wire == "anthropic" {
				path, body = "/messages", `{"messages":[{"role":"user","content":"hello"}],"max_tokens":64}`
			}
			var group sync.WaitGroup
			for i := 0; i < 8; i++ {
				group.Add(1)
				go func() {
					defer group.Done()
					if response := brokerRequest(t, broker, path, body); response.StatusCode != 429 {
						t.Errorf("status=%d", response.StatusCode)
					}
				}()
			}
			group.Wait()
			if calls.Load() != 3 || broker.requests.Load() != 3 || broker.outputTokens.Load() != 192 {
				t.Fatalf("calls=%d requests=%d tokens=%d", calls.Load(), broker.requests.Load(), broker.outputTokens.Load())
			}
		})
	}
}

func TestModelBrokerLocalBudgetAlsoStopsCodingAndSDK(t *testing.T) {
	for _, wire := range []string{"responses", "anthropic"} {
		t.Run(wire, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(200) }))
			defer upstream.Close()
			broker, err := startModelBroker(context.Background(), ModelBrokerConfig{APIBaseURL: upstream.URL + "/v1", APIKey: "secret", Model: "fixed", WireAPI: wire, HTTPClient: upstream.Client()})
			if err != nil {
				t.Fatal(err)
			}
			defer broker.Close()
			broker.requests.Store(modelBrokerRequestLimit)
			coding, cancel := broker.codingContext(context.Background())
			defer cancel()
			path, body := "/responses", `{"input":"hello","max_output_tokens":64}`
			if wire == "anthropic" {
				path, body = "/messages", `{"messages":[{"role":"user","content":"hello"}],"max_tokens":64}`
			}
			for i := 0; i < 3; i++ {
				if response := brokerRequest(t, broker, path, body); response.StatusCode != 429 {
					t.Fatalf("status=%d", response.StatusCode)
				}
			}
			select {
			case <-coding.Done():
			case <-time.After(time.Second):
				t.Fatal("budget exhaustion left coding alive")
			}
			code, _ := executionFailureResult(codingModelFailure(nil, broker))
			if calls.Load() != 0 || broker.requests.Load() != modelBrokerRequestLimit+1 || code != "agent_adapter_model_budget_exhausted" {
				t.Fatalf("calls=%d requests=%d code=%s", calls.Load(), broker.requests.Load(), code)
			}
		})
	}
}
