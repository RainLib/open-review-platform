package agentadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestModelBrokerKeepsUpstreamCredentialOutOfCodingJob(t *testing.T) {
	const apiKey = "synthetic-upstream-model-secret"
	var upstreamCalls atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		upstreamCalls.Add(1)
		if request.URL.Path != "/v1/responses" || request.Header.Get("Authorization") != "Bearer "+apiKey {
			t.Errorf("broker sent an incorrect upstream path or credential")
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(body, &fields); err != nil || fields["model"] != "fixed-coding-model" || fields["max_output_tokens"] != float64(modelBrokerOutputTokens) || fields["store"] != false || fields["background"] != false {
			t.Errorf("broker failed to fix the model and output budget: %s", body)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"completed"}`))
	}))
	defer upstream.Close()
	config := ModelBrokerConfig{APIBaseURL: upstream.URL + "/v1", APIKey: apiKey, Model: "fixed-coding-model", HTTPClient: upstream.Client()}
	broker, err := startModelBroker(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	home := t.TempDir()
	if err := writeCodexBrokerConfig(home, config, broker); err != nil {
		t.Fatal(err)
	}
	file, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(file, []byte(apiKey)) || bytes.Contains(file, []byte(broker.token)) || !bytes.Contains(file, []byte("OPENREVIEW_MODEL_CAPABILITY")) || !bytes.Contains(file, []byte(broker.URL())) || !bytes.Contains(file, []byte(`web_search = "disabled"`)) || !bytes.Contains(file, []byte("multi_agent = false")) {
		t.Fatal("coding-job configuration exposed the upstream key or omitted the capability route")
	}
	request, err := http.NewRequest(http.MethodPost, broker.URL()+"/responses", strings.NewReader(`{"model":"attacker-chosen","max_output_tokens":999999,"store":true,"background":true,"input":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+broker.token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || upstreamCalls.Load() != 1 {
		t.Fatalf("scoped model request failed: status=%d calls=%d", response.StatusCode, upstreamCalls.Load())
	}
	unauthorized, err := http.Post(broker.URL()+"/responses", "application/json", strings.NewReader(`{"input":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusUnauthorized || upstreamCalls.Load() != 1 {
		t.Fatalf("unauthorized model request reached upstream: status=%d calls=%d", unauthorized.StatusCode, upstreamCalls.Load())
	}
	tooLarge, err := http.NewRequest(http.MethodPost, broker.URL()+"/responses", strings.NewReader(`{"input":"`+strings.Repeat("x", modelBrokerRequestBytes)+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	tooLarge.Header.Set("Authorization", "Bearer "+broker.token)
	oversizedResponse, err := http.DefaultClient.Do(tooLarge)
	if err != nil {
		t.Fatal(err)
	}
	oversizedResponse.Body.Close()
	if oversizedResponse.StatusCode != http.StatusBadRequest || upstreamCalls.Load() != 1 {
		t.Fatalf("oversized model request reached upstream: status=%d calls=%d", oversizedResponse.StatusCode, upstreamCalls.Load())
	}
	if err := broker.Close(); err != nil {
		t.Fatal(err)
	}
	closed, err := http.NewRequest(http.MethodPost, broker.URL()+"/responses", strings.NewReader(`{"input":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	closed.Header.Set("Authorization", "Bearer "+broker.token)
	if response, err := http.DefaultClient.Do(closed); err == nil {
		response.Body.Close()
		t.Fatal("expired job model capability was accepted after broker close")
	}
}

func TestModelBrokerRefusesRedirectsAndStopsRepeatingDeniedRoute(t *testing.T) {
	var redirected atomic.Int32
	other := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		redirected.Add(1)
		writer.WriteHeader(http.StatusOK)
	}))
	defer other.Close()
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Location", other.URL+"/collect")
		writer.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer upstream.Close()
	broker, err := startModelBroker(context.Background(), ModelBrokerConfig{APIBaseURL: upstream.URL + "/v1", APIKey: "test-model-key", Model: "fixed-model", HTTPClient: upstream.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	for index := 0; index <= modelBrokerRequestLimit; index++ {
		request, err := http.NewRequest(http.MethodPost, broker.URL()+"/responses", strings.NewReader(`{"input":"hello","max_output_tokens":1}`))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+broker.token)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		want := http.StatusBadGateway
		if response.StatusCode != want {
			t.Fatalf("request %d got %d; want %d", index, response.StatusCode, want)
		}
	}
	if redirected.Load() != 0 {
		t.Fatal("broker forwarded its upstream credential to a redirect target")
	}
}

func TestModelBrokerRejectsServerSideStateAndBoundsTotalOutput(t *testing.T) {
	var upstreamCalls atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		upstreamCalls.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"completed"}`))
	}))
	defer upstream.Close()
	broker, err := startModelBroker(context.Background(), ModelBrokerConfig{APIBaseURL: upstream.URL + "/v1", APIKey: "test-model-key", Model: "fixed-model", HTTPClient: upstream.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	post := func(body string) int {
		t.Helper()
		request, requestErr := http.NewRequest(http.MethodPost, broker.URL()+"/responses", strings.NewReader(body))
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		request.Header.Set("Authorization", "Bearer "+broker.token)
		response, requestErr := http.DefaultClient.Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		defer response.Body.Close()
		return response.StatusCode
	}
	for _, field := range []string{`"previous_response_id":"resp_other"`, `"conversation":"conv_other"`, `"prompt":{"id":"pmpt_other"}`} {
		if status := post(`{"input":"hello",` + field + `}`); status != http.StatusBadRequest {
			t.Fatalf("server-side state %s returned %d", field, status)
		}
	}
	if upstreamCalls.Load() != 0 {
		t.Fatal("server-side state request reached upstream")
	}
	for index := 0; index < modelBrokerTotalOutputTokens/modelBrokerOutputTokens; index++ {
		if status := post(`{"input":"hello"}`); status != http.StatusOK {
			t.Fatalf("budgeted request %d returned %d", index, status)
		}
	}
	if status := post(`{"input":"hello"}`); status != http.StatusTooManyRequests {
		t.Fatalf("output budget overrun returned %d", status)
	}
	if upstreamCalls.Load() != modelBrokerTotalOutputTokens/modelBrokerOutputTokens {
		t.Fatalf("unexpected upstream calls: %d", upstreamCalls.Load())
	}
}

func TestModelBrokerAllowsOnlySandboxLocalTools(t *testing.T) {
	var upstreamCalls atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		upstreamCalls.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"completed"}`))
	}))
	defer upstream.Close()
	broker, err := startModelBroker(context.Background(), ModelBrokerConfig{APIBaseURL: upstream.URL + "/v1", APIKey: "test-model-key", Model: "fixed-model", HTTPClient: upstream.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	for _, test := range []struct {
		name   string
		body   string
		status int
	}{
		{name: "local functions", body: `{"input":"hello","tools":[{"type":"function","name":"exec_command"}],"tool_choice":"auto"}`, status: http.StatusOK},
		{name: "local custom tool", body: `{"input":"hello","tools":[{"type":"custom","name":"apply_patch"}],"tool_choice":{"type":"custom","name":"apply_patch"}}`, status: http.StatusOK},
		{name: "local namespace", body: `{"input":"hello","tools":[{"type":"namespace","name":"local","tools":[{"type":"function","name":"exec_command"}]}]}`, status: http.StatusOK},
		{name: "constrained local tools", body: `{"input":"hello","tools":[{"type":"function","name":"exec_command"}],"tool_choice":{"type":"allowed_tools","mode":"required","tools":[{"type":"function","name":"exec_command"}]}}`, status: http.StatusOK},
		{name: "hosted web search", body: `{"input":"hello","tools":[{"type":"web_search"}]}`, status: http.StatusBadRequest},
		{name: "hosted code interpreter", body: `{"input":"hello","tools":[{"type":"code_interpreter"}]}`, status: http.StatusBadRequest},
		{name: "remote MCP", body: `{"input":"hello","tools":[{"type":"mcp","server_label":"remote"}]}`, status: http.StatusBadRequest},
		{name: "hosted tool nested in namespace", body: `{"input":"hello","tools":[{"type":"namespace","name":"unsafe","tools":[{"type":"mcp","server_label":"remote"}]}]}`, status: http.StatusBadRequest},
		{name: "hosted choice", body: `{"input":"hello","tool_choice":{"type":"web_search"}}`, status: http.StatusBadRequest},
		{name: "hosted allowed choice", body: `{"input":"hello","tool_choice":{"type":"allowed_tools","mode":"auto","tools":[{"type":"file_search"}]}}`, status: http.StatusBadRequest},
		{name: "invalid tool list", body: `{"input":"hello","tools":{"type":"function"}}`, status: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := upstreamCalls.Load()
			request, requestErr := http.NewRequest(http.MethodPost, broker.URL()+"/responses", strings.NewReader(test.body))
			if requestErr != nil {
				t.Fatal(requestErr)
			}
			request.Header.Set("Authorization", "Bearer "+broker.token)
			response, requestErr := http.DefaultClient.Do(request)
			if requestErr != nil {
				t.Fatal(requestErr)
			}
			response.Body.Close()
			if response.StatusCode != test.status {
				t.Fatalf("status=%d; want %d", response.StatusCode, test.status)
			}
			wantCalls := before
			if test.status == http.StatusOK {
				wantCalls++
			}
			if calls := upstreamCalls.Load(); calls != wantCalls {
				t.Fatalf("upstream calls=%d; want %d", calls, wantCalls)
			}
		})
	}
}

func TestModelBrokerBoundsTotalInputBytes(t *testing.T) {
	var upstreamCalls atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		upstreamCalls.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"completed"}`))
	}))
	defer upstream.Close()
	broker, err := startModelBroker(context.Background(), ModelBrokerConfig{APIBaseURL: upstream.URL + "/v1", APIKey: "test-model-key", Model: "fixed-model", HTTPClient: upstream.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	body := `{"input":"` + strings.Repeat("x", 200<<10) + `","max_output_tokens":1}`
	for index := 0; index < 6; index++ {
		request, requestErr := http.NewRequest(http.MethodPost, broker.URL()+"/responses", strings.NewReader(body))
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		request.Header.Set("Authorization", "Bearer "+broker.token)
		response, requestErr := http.DefaultClient.Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		response.Body.Close()
		want := http.StatusOK
		if index == 5 {
			want = http.StatusTooManyRequests
		}
		if response.StatusCode != want {
			t.Fatalf("request %d returned %d; want %d", index, response.StatusCode, want)
		}
	}
	if upstreamCalls.Load() != 5 {
		t.Fatalf("input budget forwarded %d requests; want 5", upstreamCalls.Load())
	}
}

func TestModelBrokerRejectsUnsafeUpstreamAndModel(t *testing.T) {
	for _, config := range []ModelBrokerConfig{
		{APIBaseURL: "http://api.example.com/v1", APIKey: "key", Model: "safe-model"},
		{APIBaseURL: "https://user:pass@api.example.com/v1", APIKey: "key", Model: "safe-model"},
		{APIBaseURL: "https://api.example.com/v1", APIKey: "", Model: "safe-model"},
		{APIBaseURL: "https://api.example.com/v1", APIKey: "key", Model: "unsafe\nmodel"},
	} {
		if err := config.valid(); err == nil {
			t.Fatalf("unsafe model broker configuration was accepted: %+v", config)
		}
	}
}
