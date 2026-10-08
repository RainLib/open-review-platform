package agentadapter

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// Tool calls usually produce small messages. Leave room for planning, coding
// and repair turns within the unchanged per-job output reservation limit.
const anthropicModelBrokerOutputTokens = 4096

// The Claude child can reach this one Messages endpoint on the private Docker
// network. It receives a per-job capability, never the deployment's API key.
func (broker *modelBroker) handleAnthropic(writer http.ResponseWriter, request *http.Request) {
	// Claude Code 2.1.144 appends ?beta=true to Messages requests. Accept
	// only that fixed compatibility flag, and never forward arbitrary queries.
	if request.Method != http.MethodPost || request.URL.Path != "/v1/messages" ||
		(request.URL.RawQuery != "" && request.URL.RawQuery != "beta=true") {
		http.Error(writer, "model endpoint is unavailable", http.StatusNotFound)
		return
	}
	provided := request.Header.Get("X-Api-Key")
	if len(provided) != len(broker.token) || subtle.ConstantTimeCompare([]byte(provided), []byte(broker.token)) != 1 {
		http.Error(writer, "model capability is invalid", http.StatusUnauthorized)
		return
	}
	broker.requestMu.Lock()
	defer broker.requestMu.Unlock()
	if blocked := broker.blockedUpstream(); blocked.status != 0 {
		status := blocked.status
		if status < 400 || status > 599 {
			status = http.StatusBadGateway
		}
		http.Error(writer, "model upstream requires correction before a new approved plan", status)
		return
	}
	if broker.requests.Add(1) > modelBrokerRequestLimit {
		broker.blockUpstream(modelUpstreamObservation{status: http.StatusTooManyRequests})
		http.Error(writer, "model request budget exhausted", http.StatusTooManyRequests)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, modelBrokerRequestBytes))
	if err != nil || len(body) == 0 || !json.Valid(body) {
		http.Error(writer, "model request is invalid or too large", http.StatusBadRequest)
		return
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		http.Error(writer, "model request is invalid", http.StatusBadRequest)
		return
	}
	// Anthropic server tools and remote MCP execute outside the isolated child.
	// Reject them even if a later CLI version starts advertising them by default.
	for _, name := range []string{"mcp_servers", "container", "server_tools"} {
		if raw, present := fields[name]; present && string(raw) != "null" {
			http.Error(writer, "remote model tools are unavailable", http.StatusBadRequest)
			return
		}
	}
	if raw, present := fields["tools"]; present && string(raw) != "null" {
		var tools []map[string]json.RawMessage
		if json.Unmarshal(raw, &tools) != nil || tools == nil {
			http.Error(writer, "model tools are invalid", http.StatusBadRequest)
			return
		}
		for _, tool := range tools {
			var name string
			_ = json.Unmarshal(tool["name"], &name)
			if !validAgentModelName(name) || tool["type"] != nil {
				http.Error(writer, "hosted or remote model tool is unavailable", http.StatusBadRequest)
				return
			}
		}
	}
	var messages []json.RawMessage
	if json.Unmarshal(fields["messages"], &messages) != nil || len(messages) == 0 {
		http.Error(writer, "model messages are required", http.StatusBadRequest)
		return
	}
	var outputTokens int
	if raw, present := fields["max_tokens"]; present {
		if json.Unmarshal(raw, &outputTokens) != nil || outputTokens < 1 {
			http.Error(writer, "model output token limit is invalid", http.StatusBadRequest)
			return
		}
	}
	if outputTokens == 0 || outputTokens > anthropicModelBrokerOutputTokens {
		outputTokens = anthropicModelBrokerOutputTokens
		fields["max_tokens"] = []byte(strconv.Itoa(outputTokens))
	}
	if raw, present := fields["thinking"]; present && string(raw) != "null" {
		var thinking map[string]json.RawMessage
		if json.Unmarshal(raw, &thinking) != nil {
			http.Error(writer, "model thinking budget is invalid", http.StatusBadRequest)
			return
		}
		if budgetRaw, present := thinking["budget_tokens"]; present {
			var budget int
			if json.Unmarshal(budgetRaw, &budget) != nil || budget < 1 {
				http.Error(writer, "model thinking budget is invalid", http.StatusBadRequest)
				return
			}
			if budget >= outputTokens {
				thinking["budget_tokens"] = []byte(strconv.Itoa(outputTokens - 1))
				fields["thinking"], _ = json.Marshal(thinking)
			}
		}
	}
	// Reserve the advertised ceiling before calling upstream. Do not permit a
	// long stream or a provider that omits usage data to evade the job budget.
	if broker.outputTokens.Add(int64(outputTokens)) > modelBrokerTotalOutputTokens ||
		broker.requestBytes.Add(int64(len(body))) > modelBrokerTotalRequestBytes {
		broker.blockUpstream(modelUpstreamObservation{status: http.StatusTooManyRequests})
		http.Error(writer, "model job budget exhausted", http.StatusTooManyRequests)
		return
	}
	fields["model"], _ = json.Marshal(broker.config.Model)
	delete(fields, "service_tier")
	body, err = json.Marshal(fields)
	if err != nil {
		http.Error(writer, "model request could not be encoded", http.StatusBadRequest)
		return
	}
	upstream, err := http.NewRequestWithContext(request.Context(), http.MethodPost, strings.TrimSuffix(broker.config.APIBaseURL, "/")+"/messages", bytes.NewReader(body))
	if err != nil {
		http.Error(writer, "model upstream is unavailable", http.StatusBadGateway)
		return
	}
	upstream.Header.Set("X-Api-Key", broker.config.APIKey)
	upstream.Header.Set("Anthropic-Version", "2023-06-01")
	upstream.Header.Set("Content-Type", "application/json")
	if strings.Contains(request.Header.Get("Accept"), "text/event-stream") {
		upstream.Header.Set("Accept", "text/event-stream")
	}
	response, err := broker.sendModelRequest(upstream, body, outputTokens)
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
	usage := anthropicUsage{streaming: strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream"), ceiling: int64(outputTokens)}
	buffer := make([]byte, 32<<10)
	remaining := int64(modelBrokerResponseBytes)
	for remaining > 0 {
		count, readErr := response.Body.Read(buffer[:min(int64(len(buffer)), remaining)])
		if count > 0 {
			usage.write(buffer[:count])
			if _, writeErr := writer.Write(buffer[:count]); writeErr != nil {
				return
			}
			remaining -= int64(count)
			if strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
				_ = http.NewResponseController(writer).Flush()
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				broker.observeUpstream(-1, "transport_error")
				broker.blockUpstream(modelUpstreamObservation{status: -1, errorType: "transport_error"})
			}
			if readErr == io.EOF && response.StatusCode == http.StatusOK {
				broker.outputTokens.Add(-usage.refund())
			}
			return
		}
	}
}
