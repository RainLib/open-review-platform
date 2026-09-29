package agentadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Run explicitly with OPENREVIEW_TEST_CODEX_CLI=1. The upstream is a local
// TLS fixture: this never spends model tokens or contacts a real provider.
func TestInstalledCodexCLIUsesJobScopedBroker(t *testing.T) {
	if os.Getenv("OPENREVIEW_TEST_CODEX_CLI") != "1" {
		t.Skip("set OPENREVIEW_TEST_CODEX_CLI=1 for installed CLI protocol acceptance")
	}
	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("Codex CLI is not installed")
	}
	var calls atomic.Int32
	requestShapes := make(chan map[string]any, 2)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v1/responses" && request.Header.Get("Authorization") == "Bearer synthetic-model-upstream-key" {
			calls.Add(1)
			var shape map[string]any
			if json.NewDecoder(request.Body).Decode(&shape) == nil {
				select {
				case requestShapes <- shape:
				default:
				}
			}
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(writer).Encode(map[string]any{"error": map[string]string{"message": "synthetic model endpoint", "type": "invalid_request_error"}})
	}))
	defer upstream.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	workspace := newInstalledCodexTestWorkspace(t)
	if output, err := exec.Command("git", "-C", workspace, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("create disposable coding checkout: %v: %s", err, output)
	}
	pipeline := Pipeline{
		ExecutorKind: "codex", CodexBinary: binary,
		CodeModelBroker: ModelBrokerConfig{APIBaseURL: upstream.URL + "/v1", APIKey: "synthetic-model-upstream-key", Model: "gpt-6-sol", HTTPClient: upstream.Client()},
	}
	if err := pipeline.runAgent(ctx, workspace, Submission{}); err == nil {
		t.Fatal("synthetic model endpoint unexpectedly completed a coding run")
	}
	if calls.Load() == 0 {
		broker, brokerErr := startModelBroker(ctx, pipeline.CodeModelBroker)
		if brokerErr != nil {
			t.Fatal(brokerErr)
		}
		defer broker.Close()
		home := t.TempDir()
		if brokerErr := writeCodexBrokerConfig(home, pipeline.CodeModelBroker, broker); brokerErr != nil {
			t.Fatal(brokerErr)
		}
		command := exec.CommandContext(ctx, binary, codexExecutorArgs()...)
		command.Dir = workspace
		command.Stdin = strings.NewReader("Do nothing")
		command.Env = append(isolatedExecutorEnvironment(home, os.Environ()), "CODEX_HOME="+home, "OPENREVIEW_MODEL_CAPABILITY="+broker.token)
		output, commandErr := command.CombinedOutput()
		t.Fatalf("installed Codex CLI did not reach the job-scoped Responses broker: run=%v direct=%v output=%q", err, commandErr, strings.TrimSpace(string(output)))
	}
	select {
	case shape := <-requestShapes:
		if shape["model"] != "gpt-6-sol" || shape["store"] != false || shape["background"] != false || shape["previous_response_id"] != nil || shape["conversation"] != nil {
			t.Fatalf("Codex CLI did not use a stateless, fixed-model request: model=%v store=%v background=%v previous=%v conversation=%v", shape["model"], shape["store"], shape["background"], shape["previous_response_id"], shape["conversation"])
		}
		var names []string
		if tools, ok := shape["tools"].([]any); ok {
			for _, tool := range tools {
				if definition, ok := tool.(map[string]any); ok {
					if name, ok := definition["name"].(string); ok {
						names = append(names, name)
					}
				}
			}
		}
		t.Logf("installed Codex request advertised tools: %s", strings.Join(names, ", "))
	default:
		t.Fatal("Codex CLI reached upstream without a decodable Responses request")
	}
}

// This protocol fixture exercises the installed CLI's tool-result round trip,
// not just its initial HTTP handshake. It uses no paid model or provider data.
func TestInstalledCodexCLIReplaysStatelessToolHistory(t *testing.T) {
	if os.Getenv("OPENREVIEW_TEST_CODEX_CLI") != "1" {
		t.Skip("set OPENREVIEW_TEST_CODEX_CLI=1 for installed CLI protocol acceptance")
	}
	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("Codex CLI is not installed")
	}
	var calls atomic.Int32
	requests := make(chan map[string]any, 4)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/responses" || request.Header.Get("Authorization") != "Bearer synthetic-model-upstream-key" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		var fields map[string]any
		if err := json.NewDecoder(request.Body).Decode(&fields); err != nil {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		select {
		case requests <- fields:
		default:
		}
		index := calls.Add(1)
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.Header().Set("Cache-Control", "no-cache")
		if index == 1 {
			item := map[string]any{"id": "fc_synthetic", "type": "function_call", "name": "exec_command", "call_id": "call_synthetic", "arguments": `{"cmd":"pwd"}`}
			writeSyntheticResponseEvent(writer, "response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item})
			writeSyntheticResponseEvent(writer, "response.completed", syntheticCompletedResponse("resp_synthetic_1", fields["model"], []any{item}))
		} else {
			item := map[string]any{"id": "msg_synthetic", "type": "message", "status": "completed", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Done.", "annotations": []any{}}}}
			writeSyntheticResponseEvent(writer, "response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item})
			writeSyntheticResponseEvent(writer, "response.completed", syntheticCompletedResponse("resp_synthetic_2", fields["model"], []any{item}))
		}
	}))
	defer upstream.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	workspace := newInstalledCodexTestWorkspace(t)
	if output, err := exec.Command("git", "-C", workspace, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("create disposable coding checkout: %v: %s", err, output)
	}
	pipeline := Pipeline{ExecutorKind: "codex", CodexBinary: binary, CodeModelBroker: ModelBrokerConfig{APIBaseURL: upstream.URL + "/v1", APIKey: "synthetic-model-upstream-key", Model: "gpt-6-sol", HTTPClient: upstream.Client()}}
	if runtime.GOOS == "linux" && os.Geteuid() == 0 {
		pool, err := NewExecutorIdentityPool(10002, 1)
		if err != nil {
			t.Fatal(err)
		}
		pipeline.ExecutorIdentityPool = pool
	}
	if err := pipeline.runAgent(ctx, workspace, Submission{}); err != nil {
		t.Fatalf("installed Codex CLI could not complete synthetic two-turn tool flow: %v; requests=%d", err, calls.Load())
	}
	if calls.Load() < 2 {
		t.Fatalf("installed Codex CLI did not continue after the tool result: requests=%d", calls.Load())
	}
	<-requests
	second := <-requests
	if second["store"] != false || second["previous_response_id"] != nil || second["conversation"] != nil {
		t.Fatalf("second Codex request was not stateless: store=%v previous=%v conversation=%v", second["store"], second["previous_response_id"], second["conversation"])
	}
	input, ok := second["input"].([]any)
	if !ok {
		t.Fatalf("second Codex request did not replay item history: %T", second["input"])
	}
	foundToolOutput := false
	for _, item := range input {
		if value, ok := item.(map[string]any); ok && value["type"] == "function_call_output" && value["call_id"] == "call_synthetic" {
			foundToolOutput = true
		}
	}
	if !foundToolOutput {
		t.Fatal("second Codex request omitted the prior tool-call output")
	}
}

func syntheticCompletedResponse(id string, model any, output []any) map[string]any {
	return map[string]any{"type": "response.completed", "response": map[string]any{"id": id, "object": "response", "created_at": 1, "status": "completed", "model": model, "output": output, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}}
}

func newInstalledCodexTestWorkspace(t *testing.T) string {
	t.Helper()
	workspaceRoot, err := os.MkdirTemp("", "openreview-codex-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workspaceRoot) })
	// The adapter exposes /workspaces as a traversable parent. Go's t.TempDir
	// adds a 0700 parent directory, which would make a leased UID fail chdir.
	if err := os.Chmod(workspaceRoot, 0o711); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(workspaceRoot, "checkout")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	return workspace
}

func writeSyntheticResponseEvent(writer http.ResponseWriter, event string, data map[string]any) {
	encoded, _ := json.Marshal(data)
	_, _ = fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", event, encoded)
	_ = http.NewResponseController(writer).Flush()
}
