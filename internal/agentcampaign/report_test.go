package agentcampaign

import (
	"encoding/csv"
	"encoding/json"
	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"strings"
	"testing"
	"time"
)

func TestReportsExplainBlockedAcceptanceAndRetainExecutionBudget(t *testing.T) {
	criterion := "Preserve the original README bytes"
	d := domain.AgentCampaignDetail{Campaign: domain.AgentCampaign{ID: uuid.New()}, Targets: []domain.AgentCampaignTarget{{Repository: "team/repo", Detail: &domain.AgentTaskDetail{
		Task:            domain.AgentTask{State: "completed"},
		ExecutionBudget: &domain.AgentTaskExecutionBudget{Used: 3, Limit: 3, SuccessfulDeliveries: 1, AttentionAttempts: 2},
		Acceptance:      &domain.AgentTaskAcceptance{HeadSHA: strings.Repeat("c", 40), State: "needs_attention", Reason: "=Provider denied read | retry\nonly checks", Criteria: []string{criterion}, VerificationCriteria: []domain.AgentCriterionResult{{Criterion: criterion, Status: "passed", Evidence: "Exact original bytes compared"}}},
	}}}}
	raw, _, err := Export(d, "csv", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(raw))).ReadAll()
	if err != nil || len(rows) != 2 {
		t.Fatalf("invalid comparison report: %v", err)
	}
	fields := map[string]string{}
	for i, name := range rows[0] {
		fields[name] = rows[1][i]
	}
	for name, want := range map[string]string{"acceptance": "needs_attention", "campaign_closed": "false", "fixed_verifier_criteria_passed": "true", "execution_attempts_used": "3", "execution_attempts_limit": "3", "successful_deliveries": "1", "attention_attempts": "2", "decided_by": "", "decided_at": "", "acceptance_reason": "'=Provider denied read | retry\nonly checks"} {
		if fields[name] != want {
			t.Fatalf("report field %s=%q, want %q", name, fields[name], want)
		}
	}
	markdown, _, err := Export(d, "markdown", time.Now())
	if err != nil || !strings.Contains(string(markdown), "reason: =Provider denied read \\| retry only checks") || !strings.Contains(string(markdown), "executions: 3/3 (delivered: 1, attention: 2)") || !strings.Contains(string(markdown), "0 of 1 repositories closed") {
		t.Fatalf("Markdown hid blocked acceptance or the failed attempts: %s %v", markdown, err)
	}
}

func TestReportSeparatesDeliveryAcceptanceCoverageAndProtectsCSV(t *testing.T) {
	d := domain.AgentCampaignDetail{Campaign: domain.AgentCampaign{ID: uuid.New(), RequestSHA256: strings.Repeat("a", 64), Input: domain.AgentCampaignInput{Title: "Campaign fixture"}}, Targets: []domain.AgentCampaignTarget{{Repository: "=unsafe", State: "plan_ready", Detail: &domain.AgentTaskDetail{Task: domain.AgentTask{State: "completed"}}}, {Repository: "team/accepted", Detail: &domain.AgentTaskDetail{Acceptance: &domain.AgentTaskAcceptance{State: "accepted", Decision: "accepted", HeadSHA: strings.Repeat("b", 40)}}}, {Repository: "team/unknown", State: "needs_attention", Scan: domain.AgentCampaignScan{Complete: false}}}}
	raw, _, err := Export(d, "json", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var report Report
	if err = json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if report.Detail.Summary.Closed || report.Detail.Summary.Counts["completed"] != 1 || report.Detail.Summary.Counts["accepted"] != 1 {
		t.Fatal("Draft or incomplete coverage counted as closed")
	}
	csv, _, err := Export(d, "csv", time.Now())
	if err != nil || !strings.Contains(string(csv), "'=unsafe") {
		t.Fatalf("CSV formula injection: %s %v", csv, err)
	}
	md, _, err := Export(d, "markdown", time.Now())
	if err != nil || !strings.Contains(string(md), "not a merge or deployment receipt") || !strings.Contains(string(md), strings.Repeat("b", 40)) {
		t.Fatal("report lost evidence boundary or commit")
	}
}
