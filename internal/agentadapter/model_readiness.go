package agentadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Readiness checks use the production broker, route, key and tool-capable
// protocol. They never claim a task, clone a repository or execute a tool.
// Success proves model availability only, not acceptance of every future input.
func (pipeline Pipeline) CheckExecutionReady(ctx context.Context) error {
	if err := pipeline.CheckSandboxReady(ctx); err != nil {
		return err
	}
	config := pipeline.CodeModelBroker
	if pipeline.ExecutorKind == "claude" {
		config = pipeline.ClaudeModelBroker
	}
	if config.APIBaseURL == "" {
		return fmt.Errorf("coding model is unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	broker, err := startModelBroker(ctx, config)
	if err != nil {
		return err
	}
	defer broker.Close()
	body, path := []byte(`{"input":"Reply OK. Do not execute any tools.","max_output_tokens":64,"stream":true}`), "/responses"
	if config.WireAPI == "anthropic" {
		path = "/messages"
		body = []byte(`{"max_tokens":64,"stream":true,"messages":[{"role":"user","content":"Reply OK. Do not execute any tools."}],"tools":[{"name":"Read","description":"Read a local source file","input_schema":{"type":"object","properties":{"file_path":{"type":"string"}},"required":["file_path"]}}]}`)
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, broker.URL()+path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("X-Api-Key", broker.token)
	request.Header.Set("Authorization", "Bearer "+broker.token)
	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return codingModelFailure(fmt.Errorf("model readiness transport failed"), broker)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (16<<10)+1))
	if err != nil || len(data) > 16<<10 || response.StatusCode != http.StatusOK {
		return codingModelFailure(fmt.Errorf("model readiness response failed"), broker)
	}
	if !completeReadinessResponse(config.WireAPI, data, strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream")) {
		return fmt.Errorf("model readiness requires a complete valid response")
	}
	return nil
}

func completeReadinessResponse(wire string, data []byte, streaming bool) bool {
	if wire == "anthropic" && streaming {
		usage := anthropicUsage{streaming: true, ceiling: 64}
		usage.write(data)
		_ = usage.refund()
		return !usage.invalid && usage.started && usage.stopped && usage.last > 0
	}
	if streaming {
		completed := false
		for _, line := range bytes.Split(data, []byte("\n")) {
			if !bytes.HasPrefix(line, []byte("data:")) {
				continue
			}
			if bytes.Equal(bytes.TrimSpace(line[5:]), []byte("[DONE]")) {
				continue
			}
			var event struct {
				Type     string `json:"type"`
				Response struct {
					Status string `json:"status"`
				} `json:"response"`
			}
			if json.Unmarshal(bytes.TrimSpace(line[5:]), &event) != nil || event.Type == "error" || event.Type == "response.failed" || event.Type == "response.incomplete" {
				return false
			}
			if event.Type == "response.completed" {
				if completed || event.Response.Status != "completed" {
					return false
				}
				completed = true
			}
		}
		return completed
	}
	var value struct {
		Type       string `json:"type"`
		Status     string `json:"status"`
		StopReason string `json:"stop_reason"`
	}
	if json.Unmarshal(data, &value) != nil {
		return false
	}
	if wire == "anthropic" {
		return value.Type == "message" && value.StopReason != ""
	}
	return value.Status == "completed"
}
