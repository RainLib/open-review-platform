package domain

import "testing"

func TestNormalizeProviderIssueAnalysisRetryInput(t *testing.T) {
	input, ok := NormalizeProviderIssueAnalysisRetryInput(ProviderIssueAnalysisRetryInput{
		ExpectedRevision: 4,
		IdempotencyKey:   "  issue-retry:12345678  ",
	})
	if !ok || input.ExpectedRevision != 4 || input.IdempotencyKey != "issue-retry:12345678" {
		t.Fatalf("normalized=%#v valid=%v", input, ok)
	}
	for _, invalid := range []ProviderIssueAnalysisRetryInput{
		{ExpectedRevision: 0, IdempotencyKey: "issue-retry:12345678"},
		{ExpectedRevision: 1, IdempotencyKey: "short"},
		{ExpectedRevision: 1, IdempotencyKey: "issue retry invalid"},
	} {
		if _, ok := NormalizeProviderIssueAnalysisRetryInput(invalid); ok {
			t.Fatalf("accepted invalid input %#v", invalid)
		}
	}
}
