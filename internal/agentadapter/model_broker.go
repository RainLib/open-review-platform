package agentadapter

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RainLib/open-review-platform/internal/httpguard"
)

const (
	modelBrokerRequestLimit      = 24
	modelBrokerRequestBytes      = 256 << 10
	modelBrokerResponseBytes     = 16 << 20
	modelBrokerOutputTokens      = 8192
	modelBrokerTotalOutputTokens = 65536
	modelBrokerTotalRequestBytes = 1 << 20
)

// ModelBrokerConfig is deployment-owned. The actual model API key stays in
// the credential-owning adapter process; the coding CLI receives only a
// short-lived, job-scoped capability for this bounded Responses endpoint.
type ModelBrokerConfig struct {
	APIBaseURL string
	APIKey     string
	Model      string
	WireAPI    string       // Empty/"responses" for Codex; "anthropic" for Claude Code.
	HTTPClient *http.Client // Optional test transport; never supplied by an Issue or task.
}

func (config ModelBrokerConfig) valid() error {
	base, err := url.Parse(strings.TrimSuffix(strings.TrimSpace(config.APIBaseURL), "/"))
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || base.Opaque != "" {
		return fmt.Errorf("coding model upstream requires a fixed HTTPS base URL")
	}
	if strings.TrimSpace(config.APIKey) == "" || !validAgentModelName(config.Model) {
		return fmt.Errorf("coding model API key and safe fixed model name are required")
	}
	if config.WireAPI != "" && config.WireAPI != "responses" && config.WireAPI != "anthropic" {
		return fmt.Errorf("coding model wire API is unsupported")
	}
	return nil
}

func validAgentModelName(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("._:/-", character)) {
			return false
		}
	}
	return true
}

type modelBroker struct {
	config        ModelBrokerConfig
	token         string
	listener      net.Listener
	advertiseHost string
	server        *http.Server
	requests      atomic.Int32
	outputTokens  atomic.Int64
	requestBytes  atomic.Int64
	transport     *http.Client
	stop          chan struct{}
	closeOnce     sync.Once
}

func startModelBroker(ctx context.Context, config ModelBrokerConfig) (*modelBroker, error) {
	return startModelBrokerOn(ctx, config, "127.0.0.1:0", "")
}

// startModelBrokerOn exposes one job capability to a container-only network.
// The default broker remains loopback-only for development UID executors.
func startModelBrokerOn(ctx context.Context, config ModelBrokerConfig, bindAddress, advertiseHost string) (*modelBroker, error) {
	if err := config.valid(); err != nil {
		return nil, err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate coding model capability: %w", err)
	}
	listener, err := net.Listen("tcp4", bindAddress)
	if err != nil {
		return nil, fmt.Errorf("bind job-scoped model broker: %w", err)
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Transport: &http.Transport{Proxy: nil, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 60 * time.Second}}
	}
	broker := &modelBroker{config: config, token: hex.EncodeToString(secret), listener: listener, advertiseHost: advertiseHost, transport: httpguard.NoRedirects(client, 0), stop: make(chan struct{})}
	broker.server = &http.Server{Handler: http.HandlerFunc(broker.handle), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second}
	go func() { _ = broker.server.Serve(listener) }()
	go func() {
		select {
		case <-ctx.Done():
			_ = broker.Close()
		case <-broker.stop:
		}
	}()
	return broker, nil
}

func (broker *modelBroker) URL() string {
	if broker.advertiseHost != "" {
		_, port, _ := net.SplitHostPort(broker.listener.Addr().String())
		return "http://" + net.JoinHostPort(broker.advertiseHost, port) + "/v1"
	}
	return "http://" + broker.listener.Addr().String() + "/v1"
}

func (broker *modelBroker) Close() error {
	if broker == nil {
		return nil
	}
	var err error
	broker.closeOnce.Do(func() {
		close(broker.stop)
		err = broker.server.Close()
	})
	return err
}

func (broker *modelBroker) handle(writer http.ResponseWriter, request *http.Request) {
	if broker.config.WireAPI == "anthropic" {
		broker.handleAnthropic(writer, request)
		return
	}
	if request.Method != http.MethodPost || request.URL.Path != "/v1/responses" || request.URL.RawQuery != "" {
		http.Error(writer, "model endpoint is unavailable", http.StatusNotFound)
		return
	}
	provided := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
	if len(provided) != len(broker.token) || subtle.ConstantTimeCompare([]byte(provided), []byte(broker.token)) != 1 {
		http.Error(writer, "model capability is invalid", http.StatusUnauthorized)
		return
	}
	if broker.requests.Add(1) > modelBrokerRequestLimit {
		http.Error(writer, "model request budget exhausted", http.StatusTooManyRequests)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, modelBrokerRequestBytes))
	if err != nil || len(body) == 0 || !json.Valid(body) {
		http.Error(writer, "model request is invalid or too large", http.StatusBadRequest)
		return
	}
	// A repository-controlled prompt cannot change the fixed model or request
	// arbitrarily large output. Retain all other Responses fields verbatim.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		http.Error(writer, "model request is invalid", http.StatusBadRequest)
		return
	}
	// A stateless job must replay its own context. Server-side response or
	// conversation references are incompatible with forced store=false and
	// can cross this job's boundary if a model provider resolves old IDs.
	for _, field := range []string{"previous_response_id", "conversation", "prompt"} {
		if value, exists := fields[field]; exists && string(value) != "null" {
			http.Error(writer, "server-side model state is unavailable", http.StatusBadRequest)
			return
		}
	}
	// Only local function/custom calls return control to the isolated coding
	// executor. Hosted tools and remote MCP would run outside that sandbox and
	// its network, filesystem and task-scoped capability boundaries.
	if err := validateLocalModelTools(fields); err != nil {
		http.Error(writer, "hosted or remote model tools are unavailable", http.StatusBadRequest)
		return
	}
	model, _ := json.Marshal(broker.config.Model)
	fields["model"] = model
	// Do not let repository prompts opt the coding run into provider-side
	// persistence or an asynchronous response the broker cannot account for.
	fields["store"] = []byte("false")
	fields["background"] = []byte("false")
	var outputTokens int
	if raw, exists := fields["max_output_tokens"]; exists {
		if err := json.Unmarshal(raw, &outputTokens); err != nil || outputTokens < 1 {
			http.Error(writer, "model output token limit is invalid", http.StatusBadRequest)
			return
		}
	}
	if outputTokens == 0 || outputTokens > modelBrokerOutputTokens {
		outputTokens = modelBrokerOutputTokens
		fields["max_output_tokens"] = []byte(strconv.Itoa(outputTokens))
	}
	// Reserve the advertised output limit before sending upstream. This is a
	// conservative spend ceiling even when the provider omits usage metadata
	// or a streamed response is interrupted after generation has begun.
	if broker.outputTokens.Add(int64(outputTokens)) > modelBrokerTotalOutputTokens ||
		broker.requestBytes.Add(int64(len(body))) > modelBrokerTotalRequestBytes {
		http.Error(writer, "model job budget exhausted", http.StatusTooManyRequests)
		return
	}
	body, err = json.Marshal(fields)
	if err != nil {
		http.Error(writer, "model request could not be encoded", http.StatusBadRequest)
		return
	}
	upstreamBase := strings.TrimSuffix(broker.config.APIBaseURL, "/")
	upstream, err := http.NewRequestWithContext(request.Context(), http.MethodPost, upstreamBase+"/responses", bytes.NewReader(body))
	if err != nil {
		http.Error(writer, "model upstream is unavailable", http.StatusBadGateway)
		return
	}
	upstream.Header.Set("Authorization", "Bearer "+broker.config.APIKey)
	upstream.Header.Set("Content-Type", "application/json")
	if strings.Contains(request.Header.Get("Accept"), "text/event-stream") {
		upstream.Header.Set("Accept", "text/event-stream")
	}
	response, err := broker.transport.Do(upstream)
	if err != nil {
		http.Error(writer, "model upstream request failed", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		http.Error(writer, "model upstream redirect refused", http.StatusBadGateway)
		return
	}
	if response.ContentLength > modelBrokerResponseBytes {
		http.Error(writer, "model response exceeded budget", http.StatusBadGateway)
		return
	}
	if contentType := response.Header.Get("Content-Type"); strings.HasPrefix(contentType, "text/event-stream") || strings.HasPrefix(contentType, "application/json") {
		writer.Header().Set("Content-Type", contentType)
	}
	writer.WriteHeader(response.StatusCode)
	buffer := make([]byte, 32<<10)
	remaining := int64(modelBrokerResponseBytes)
	for remaining > 0 {
		count, readErr := response.Body.Read(buffer[:min(int64(len(buffer)), remaining)])
		if count > 0 {
			if _, writeErr := writer.Write(buffer[:count]); writeErr != nil {
				return
			}
			remaining -= int64(count)
			if strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
				_ = http.NewResponseController(writer).Flush()
			}
		}
		if readErr != nil {
			return
		}
	}
}

func validateLocalModelTools(fields map[string]json.RawMessage) error {
	if raw, exists := fields["tools"]; exists && string(raw) != "null" {
		if err := validateLocalToolList(raw, 0); err != nil {
			return err
		}
	}
	choice, exists := fields["tool_choice"]
	if !exists || string(choice) == "null" {
		return nil
	}
	var mode string
	if err := json.Unmarshal(choice, &mode); err == nil {
		if mode == "auto" || mode == "none" || mode == "required" {
			return nil
		}
		return fmt.Errorf("unsupported tool choice")
	}
	var selected struct {
		Type  string          `json:"type"`
		Tools json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(choice, &selected); err != nil {
		return fmt.Errorf("invalid tool choice")
	}
	switch selected.Type {
	case "function", "custom":
		return nil
	case "allowed_tools":
		return validateLocalToolList(selected.Tools, 0)
	default:
		return fmt.Errorf("unsupported tool choice")
	}
}

func validateLocalToolList(raw json.RawMessage, depth int) error {
	if depth > 2 {
		return fmt.Errorf("model tool namespaces are too deep")
	}
	var tools []json.RawMessage
	if len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &tools) != nil || tools == nil {
		return fmt.Errorf("invalid model tools")
	}
	for _, rawTool := range tools {
		var tool struct {
			Type  string          `json:"type"`
			Tools json.RawMessage `json:"tools"`
		}
		if json.Unmarshal(rawTool, &tool) != nil {
			return fmt.Errorf("invalid model tool")
		}
		switch tool.Type {
		case "function", "custom":
		case "namespace":
			// Codex groups client-executed functions in namespaces. Validate
			// every nested entry so a hosted tool cannot hide in the group.
			if err := validateLocalToolList(tool.Tools, depth+1); err != nil {
				return err
			}
		default:
			return fmt.Errorf("hosted or remote model tool")
		}
	}
	return nil
}

func writeCodexBrokerConfig(home string, config ModelBrokerConfig, broker *modelBroker) error {
	if broker == nil {
		return fmt.Errorf("coding model broker is unavailable")
	}
	content := "model_provider = \"openreview\"\n" +
		"model = " + strconv.Quote(config.Model) + "\n" +
		"web_search = \"disabled\"\n" +
		"[features]\n" +
		"multi_agent = false\n" +
		"[model_providers.openreview]\n" +
		"name = \"Open Review job broker\"\n" +
		"base_url = " + strconv.Quote(broker.URL()) + "\n" +
		"env_key = \"OPENREVIEW_MODEL_CAPABILITY\"\n" +
		"wire_api = \"responses\"\n"
	return os.WriteFile(filepath.Join(home, "config.toml"), []byte(content), 0o600)
}
