package agentadapter

import (
	"context"
	"encoding/json"
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
		if json.Unmarshal(body, &fields) != nil || fields["model"] != "aliyun/glm-5.3" || fields["max_tokens"] != float64(modelBrokerOutputTokens) {
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
