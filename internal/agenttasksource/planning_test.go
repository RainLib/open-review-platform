package agenttasksource

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/RainLib/open-review-platform/internal/domain"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPlanningInspectionPinsProviderReadsAndExcludesCredentials(t *testing.T) {
	head := strings.Repeat("a", 40)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer read-only" {
			t.Error("unexpected provider operation")
		}
		if strings.Contains(r.URL.Path, "/git/trees/") {
			if !strings.Contains(r.URL.Path, head) {
				t.Error("tree read used mutable ref")
			}
			json.NewEncoder(w).Encode(map[string]any{"tree": []any{map[string]any{"path": "worker.go", "type": "blob", "mode": "100644"}, map[string]any{"path": ".env", "type": "blob", "mode": "100644"}, map[string]any{"path": "link.go", "type": "blob", "mode": "120000"}}})
			return
		}
		if r.URL.Path != "/repos/team/repo/contents/worker.go" || r.URL.Query().Get("ref") != head {
			t.Errorf("wrong frozen read: %s", r.URL)
		}
		json.NewEncoder(w).Encode(map[string]any{"type": "file", "path": "worker.go", "encoding": "base64", "size": 80, "content": base64.StdEncoding.EncodeToString([]byte("package worker\n// read-only\n// api_key=fixture-credential\n"))})
	}))
	defer server.Close()
	target := domain.AgentTaskSourceTarget{Task: domain.AgentTask{Provider: domain.ProviderGitHub, APIBaseURL: server.URL, Repository: "team/repo"}}
	snapshot := domain.AgentTaskSourceSnapshot{BaseRef: "main", BaseSHA: head, Issue: &domain.AgentTaskIssueSnapshot{Body: "Fix worker.go"}}
	evidence, err := (Resolver{Resolver: staticResolver("read-only"), HTTPClient: server.Client()}).InspectPlanningSource(context.Background(), target, snapshot)
	if err != nil || !strings.Contains(evidence, "package worker") || strings.Contains(evidence, ".env") || strings.Contains(evidence, "link.go") || strings.Contains(evidence, "read-only") || strings.Contains(evidence, "fixture-credential") {
		t.Fatalf("%s %v", evidence, err)
	}
}
