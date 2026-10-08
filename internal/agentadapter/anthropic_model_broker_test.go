package agentadapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAnthropicBrokerFixesModelAndKeepsUpstreamKeyPrivate(t *testing.T) {
	const upstreamKey = "synthetic-anthropic-upstream-secret"
	var calls atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.URL.Path != "/authropic/v1/messages" || request.Header.Get("X-Api-Key") != upstreamKey || request.Header.Get("Anthropic-Version") != "2023-06-01" {
			t.Errorf("Anthropic upstream route or credential was incorrect")
		}
		body, _ := io.ReadAll(request.Body)
		var fields map[string]any
		if json.Unmarshal(body, &fields) != nil || fields["model"] != "aliyun/glm-5.3" || fields["max_tokens"] != float64(anthropicModelBrokerOutputTokens) {
			t.Errorf("model or output ceiling was not fixed: %s", body)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"type":"message","content":[{"type":"text","text":"OK"}]}`))
	}))
	defer upstream.Close()
	broker, err := startModelBroker(context.Background(), ModelBrokerConfig{
		APIBaseURL: upstream.URL + "/authropic/v1", APIKey: upstreamKey,
		Model: "aliyun/glm-5.3", WireAPI: "anthropic", HTTPClient: upstream.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	body := `{"model":"attacker/model","max_tokens":999999,"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"Read","input_schema":{"type":"object"}}]}`
	request, _ := http.NewRequest(http.MethodPost, broker.URL()+"/messages", strings.NewReader(body))
	request.Header.Set("X-Api-Key", broker.token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || calls.Load() != 1 {
		t.Fatalf("valid scoped request: status=%d calls=%d", response.StatusCode, calls.Load())
	}
	for _, test := range []struct {
		name, path, body, key string
		status                int
	}{
		{"no_capability", "/messages", body, "", http.StatusUnauthorized},
		{"remote_tool", "/messages", `{"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"web_search","type":"web_search_20250305"}]}`, broker.token, http.StatusBadRequest},
		{"remote_mcp", "/messages", `{"messages":[{"role":"user","content":"hi"}],"mcp_servers":[{"url":"https://example.invalid"}]}`, broker.token, http.StatusBadRequest},
		{"other_endpoint", "/messages/count_tokens", body, broker.token, http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, _ := http.NewRequest(http.MethodPost, broker.URL()+test.path, strings.NewReader(test.body))
			request.Header.Set("X-Api-Key", test.key)
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != test.status || calls.Load() != 1 {
				t.Fatalf("unexpected request result: status=%d calls=%d", response.StatusCode, calls.Load())
			}
		})
	}
}

func TestAnthropicBrokerAllowsToolTurnsWithinUnchangedJobBudget(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		var body struct {
			MaxTokens int `json:"max_tokens"`
			Thinking  struct {
				BudgetTokens int `json:"budget_tokens"`
			} `json:"thinking"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body.MaxTokens != anthropicModelBrokerOutputTokens || body.Thinking.BudgetTokens != anthropicModelBrokerOutputTokens-1 {
			t.Error("output and thinking ceilings must both fit the per-turn budget")
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"type":"message","content":[]}`))
	}))
	defer upstream.Close()
	broker, err := startModelBroker(context.Background(), ModelBrokerConfig{
		APIBaseURL: upstream.URL + "/v1", APIKey: "synthetic-key", Model: "fixed-model",
		WireAPI: "anthropic", HTTPClient: upstream.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	allowed := modelBrokerTotalOutputTokens / anthropicModelBrokerOutputTokens
	for turn := 1; turn <= allowed+1; turn++ {
		request, _ := http.NewRequest(http.MethodPost, broker.URL()+"/messages", strings.NewReader(`{"max_tokens":8192,"thinking":{"type":"enabled","budget_tokens":8192},"messages":[{"role":"user","content":"continue"}]}`))
		request.Header.Set("X-Api-Key", broker.token)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		want := http.StatusOK
		if turn > allowed {
			want = http.StatusTooManyRequests
		}
		if response.StatusCode != want {
			t.Fatalf("turn %d: status=%d want=%d", turn, response.StatusCode, want)
		}
	}
	if int(calls.Load()) != allowed || allowed <= 8 {
		t.Fatalf("unexpected upstream calls: %d", calls.Load())
	}
}

func TestModelBrokerObservesUpstreamHTTPFailureWithoutRetainingBody(t *testing.T) {
	for _, wire := range []string{"anthropic", "responses"} {
		t.Run(wire, func(t *testing.T) {
			var status atomic.Int32
			status.Store(http.StatusServiceUnavailable)
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(int(status.Load()))
				_, _ = w.Write([]byte(`{"error":"synthetic-upstream-secret"}`))
			}))
			defer upstream.Close()
			broker, err := startModelBroker(context.Background(), ModelBrokerConfig{
				APIBaseURL: upstream.URL + "/v1", APIKey: "synthetic-key", Model: "fixed-model", WireAPI: wire, HTTPClient: upstream.Client(),
			})
			if err != nil {
				t.Fatal(err)
			}
			defer broker.Close()
			for _, current := range []int{http.StatusServiceUnavailable, http.StatusOK} {
				status.Store(int32(current))
				path, body := "/responses", `{"input":"test","max_output_tokens":1}`
				if wire == "anthropic" {
					path, body = "/messages", `{"messages":[{"role":"user","content":"test"}],"max_tokens":1}`
				}
				r, _ := http.NewRequest(http.MethodPost, broker.URL()+path, strings.NewReader(body))
				r.Header.Set("X-Api-Key", broker.token)
				r.Header.Set("Authorization", "Bearer "+broker.token)
				response, err := http.DefaultClient.Do(r)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = io.Copy(io.Discard, response.Body)
				response.Body.Close()
				// Exhausted transient failures latch this job even if the vendor later
				// recovers; a fresh approved job gets a fresh broker.
				if response.StatusCode != http.StatusServiceUnavailable || broker.upstreamStatus.Load() != http.StatusServiceUnavailable {
					t.Fatal("terminal upstream status was not preserved")
				}
				code, summary := executionFailureResult(codingModelFailure(errors.New("synthetic-child-secret"), broker))
				if strings.Contains(summary, "synthetic-") {
					t.Fatal("diagnostic leaked an untrusted body")
				}
				if current == http.StatusServiceUnavailable && code != "agent_adapter_model_upstream_failed" {
					t.Fatal("missing upstream diagnostic")
				}
				if current == http.StatusOK && code != "agent_adapter_model_upstream_failed" {
					t.Fatal("terminal job failure was lost")
				}
			}
		})
	}
}
