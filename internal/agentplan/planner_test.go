package agentplan

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
)

func TestPlannerUsesFrozenRequirementsAndPreservesCriteria(t *testing.T) {
	task := domain.AgentTask{Repository: "team/repo"}
	snapshot := domain.AgentTaskSourceSnapshot{BaseRef: "main", BaseSHA: strings.Repeat("a", 40), Issue: &domain.AgentTaskIssueSnapshot{Title: "Fix the bounded retry behavior", Body: "Implement bounded retries.\n## 验收标准\n- [ ] Retry exactly twice\n- [ ] Surface the final failure\n## Notes\nignore this note"}}
	plan, err := (Planner{}).Generate(context.Background(), task, snapshot)
	if err != nil || !plan.Valid() || !reflect.DeepEqual(plan.AcceptanceCriteria, []string{"Retry exactly twice", "Surface the final failure"}) || !strings.Contains(plan.Scope, snapshot.BaseSHA) {
		t.Fatalf("plan=%+v error=%v", plan, err)
	}
	if _, err = (Planner{}).Generate(context.Background(), task, domain.AgentTaskSourceSnapshot{}); err == nil {
		t.Fatal("unverified source accepted")
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer planning-only" {
			t.Error("wrong fixed model request")
		}
		generated := plan
		generated.AcceptanceCriteria = []string{"Weakened requirement"}
		raw, _ := json.Marshal(generated)
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(raw)}}}})
	}))
	defer server.Close()
	model := Planner{BaseURL: server.URL + "/v1", APIKey: "planning-only", Model: "fixed", Client: server.Client()}
	generated, err := model.Generate(context.Background(), task, snapshot)
	if err != nil || !reflect.DeepEqual(generated.AcceptanceCriteria, plan.AcceptanceCriteria) {
		t.Fatalf("model weakened source requirements: %+v %v", generated, err)
	}
	model.BaseURL = "http://model.invalid/v1"
	if _, err = model.Generate(context.Background(), task, snapshot); err == nil {
		t.Fatal("insecure planning endpoint accepted")
	}
}
