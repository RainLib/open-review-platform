package store

import (
	"strings"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
)

func TestAutomaticAgentTaskAdmissionRequiresExactLabelAndStableIssueEvidence(t *testing.T) {
	event := domain.ProviderIssueEvent{
		Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com", DeliveryID: "delivery-1", EventName: "issues",
		InstallationExternalID: "42", Repository: "RainLib/open-review-platform", IssueNumber: 7,
		Title: "Retry state is lost", Body: "Observed behavior and acceptance criteria are present.", Labels: []string{"Team:Backend", "OpenReview:Implement"}, ReceivedAt: time.Now().UTC(),
	}
	if !agentTaskLabelMatches(event.Labels, "openreview:implement") {
		t.Fatal("exact label matching must be case-insensitive")
	}
	if agentTaskLabelMatches(event.Labels, "openreview:other") {
		t.Fatal("a different label must not create an automatic candidate")
	}
	first := automaticAgentTaskIssueRevision(event)
	event.DeliveryID = "delivery-2"
	event.Labels = []string{"openreview:implement", "team:backend"}
	if second := automaticAgentTaskIssueRevision(event); second != first {
		t.Fatalf("redelivery or label ordering changed the frozen revision: %q != %q", second, first)
	}
	event.Body += " Updated acceptance detail."
	if changed := automaticAgentTaskIssueRevision(event); changed == first {
		t.Fatal("a changed Issue body must produce a new candidate revision")
	}
}

func TestAutomaticAgentTaskAcknowledgementMatchesHardGate(t *testing.T) {
	for _, test := range []struct {
		name, state, decision, heading, warning string
	}{
		{"accepted_candidate", "received", "requires_human", "### Agent candidate received", "will verify the repository's immutable base commit"},
		{"missing_context", "received", "needs_context", "### Agent candidate needs context", "planning remains blocked"},
		{"hard_rejection", "rejected", "rejected", "### Agent candidate rejected", "Source resolution and planning will not continue"},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := automaticAgentTaskAcknowledgement(domain.AgentTask{State: test.state}, domain.AgentTaskClassification{Decision: test.decision, RiskLevel: "medium", NextAction: "review", Reasons: []string{"Rule evidence is retained."}})
			if !strings.Contains(body, test.heading) || !strings.Contains(body, test.warning) || !strings.Contains(body, "No coding Agent, branch, pull request, or merge action has started") {
				t.Fatalf("automatic candidate response does not match the frozen hard gate: %q", body)
			}
			if test.decision == "rejected" && strings.Contains(body, "will verify the repository's immutable base commit") {
				t.Fatal("rejected candidate promises source work that is not queued")
			}
		})
	}
}
