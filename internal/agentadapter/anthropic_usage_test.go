package agentadapter

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAnthropicUsageRequiresCompleteValidReports(t *testing.T) {
	start := `data: {"type":"message_start","message":{"type":"message"}}` + "\n\n"
	delta := `data: {"type":"message_delta","usage":{"output_tokens":100}}` + "\n\n"
	stop := `data: {"type":"message_stop"}` + "\n\n"
	for _, tc := range []struct {
		name, body string
		stream     bool
		refund     int64
	}{
		{"complete_stream", start + delta + stop, true, 3996},
		{"interrupted_stream", start + delta, true, 0},
		{"missing_usage", start + stop, true, 0},
		{"missing_start", delta + stop, true, 0},
		{"decreasing_usage", start + delta + `data: {"type":"message_delta","usage":{"output_tokens":50}}` + "\n" + stop, true, 0},
		{"invalid_usage", start + `data: {"type":"message_delta","usage":{"output_tokens":1.5}}` + "\n" + stop, true, 0},
		{"excess_usage", start + `data: {"type":"message_delta","usage":{"output_tokens":5000}}` + "\n" + stop, true, 0},
		{"upstream_error", start + delta + `data: {"type":"error"}` + "\n" + stop, true, 0},
		{"complete_message", `{"type":"message","stop_reason":"tool_use","usage":{"output_tokens":100}}`, false, 3996},
		{"missing_stop_reason", `{"type":"message","usage":{"output_tokens":100}}`, false, 0},
		{"malformed_message", `{"type":"message","stop_reason":"end_turn","usage":{"output_tokens":-1}}`, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usage := anthropicUsage{streaming: tc.stream, ceiling: 4096}
			for i := 0; i < len(tc.body); i += 7 {
				usage.write([]byte(tc.body[i:min(i+7, len(tc.body))]))
			}
			if got := usage.refund(); got != tc.refund {
				t.Fatalf("refund=%d want=%d", got, tc.refund)
			}
		})
	}
}

func TestAnthropicCompleteUsageLeavesRequestBudgetEnforced(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"message_start\",\"message\":{\"type\":\"message\"}}\n\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":100}}\n\ndata: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer upstream.Close()
	broker, err := startModelBroker(context.Background(), ModelBrokerConfig{APIBaseURL: upstream.URL + "/v1", APIKey: "synthetic-key", Model: "fixed-model", WireAPI: "anthropic", HTTPClient: upstream.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	for turn := int32(1); turn <= modelBrokerRequestLimit+1; turn++ {
		r, _ := http.NewRequest(http.MethodPost, broker.URL()+"/messages", strings.NewReader(`{"messages":[{"role":"user","content":"continue"}]}`))
		r.Header.Set("X-Api-Key", broker.token)
		response, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
		want := http.StatusOK
		if turn > modelBrokerRequestLimit {
			want = http.StatusTooManyRequests
		}
		if response.StatusCode != want {
			t.Fatalf("turn %d: status=%d want=%d", turn, response.StatusCode, want)
		}
	}
	if calls.Load() != modelBrokerRequestLimit || broker.outputTokens.Load() != int64(modelBrokerRequestLimit)*100 {
		t.Fatalf("usage accounting calls=%d tokens=%d", calls.Load(), broker.outputTokens.Load())
	}
}
