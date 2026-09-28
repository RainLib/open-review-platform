package agentadapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Opt-in protocol acceptance uses only a disposable checkout and local TLS
// fixture; it never spends model tokens or hands a real credential to Claude.
func TestInstalledClaudeCLIUsesJobScopedBroker(t *testing.T) {
	if os.Getenv("OPENREVIEW_TEST_CLAUDE_CLI") != "1" {
		t.Skip("set OPENREVIEW_TEST_CLAUDE_CLI=1 for installed CLI protocol acceptance")
	}
	binary, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("Claude CLI is not installed")
	}
	var calls atomic.Int32
	shapes := make(chan map[string]any, 2)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/authropic/v1/messages" && request.Header.Get("X-Api-Key") == "synthetic-upstream-key" {
			calls.Add(1)
			var shape map[string]any
			if json.NewDecoder(request.Body).Decode(&shape) == nil {
				select {
				case shapes <- shape:
				default:
				}
			}
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"synthetic fixture"}}`))
	}))
	defer upstream.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	broker, err := startModelBroker(ctx, ModelBrokerConfig{
		APIBaseURL: upstream.URL + "/authropic/v1", APIKey: "synthetic-upstream-key",
		Model: "aliyun/glm-5.3", WireAPI: "anthropic", HTTPClient: upstream.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	brokerOrigin, _ := url.Parse(strings.TrimSuffix(broker.URL(), "/v1"))
	reverse := httputil.NewSingleHostReverseProxy(brokerOrigin)
	var pathMu sync.Mutex
	var paths []string
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		captured := httptest.NewRecorder()
		reverse.ServeHTTP(captured, request)
		pathMu.Lock()
		paths = append(paths, request.Method+" "+request.URL.RequestURI()+" "+http.StatusText(captured.Code)+" "+strings.TrimSpace(captured.Body.String()))
		pathMu.Unlock()
		for name, values := range captured.Header() {
			writer.Header()[name] = values
		}
		writer.WriteHeader(captured.Code)
		_, _ = writer.Write(captured.Body.Bytes())
	}))
	defer proxy.Close()
	workspace := t.TempDir()
	home := t.TempDir()
	command := exec.CommandContext(ctx, binary, claudeExecutorArgs("aliyun/glm-5.3", false)...)
	command.Dir = workspace
	command.Stdin = strings.NewReader("Do not edit files. Reply OK.")
	command.Env = append(isolatedExecutorEnvironment(home, os.Environ()),
		"ANTHROPIC_BASE_URL="+proxy.URL, "ANTHROPIC_API_KEY="+broker.token,
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1")
	output, err := command.CombinedOutput()
	if calls.Load() == 0 {
		t.Fatalf("installed Claude CLI did not reach scoped Messages broker: %v; paths=%v; output=%q", err, paths, strings.TrimSpace(string(output)))
	}
	select {
	case shape := <-shapes:
		if shape["model"] != "aliyun/glm-5.3" || shape["max_tokens"].(float64) > modelBrokerOutputTokens {
			t.Fatalf("Claude request escaped fixed model or token limit: %v", shape)
		}
	default:
		t.Fatal("Claude reached the upstream without a decodable Messages request")
	}
}
