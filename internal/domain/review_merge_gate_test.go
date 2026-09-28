package domain

import "testing"

func TestNormalizeReviewMergeGateDecisionInputRejectsInconsistentDecisions(t *testing.T) {
	valid, ok := NormalizeReviewMergeGateDecisionInput(ReviewMergeGateDecisionInput{
		Enabled:           true,
		Threshold:         " HIGH ",
		Conclusion:        "failure",
		BlockingFindings:  1,
		FindingCount:      2,
		EvaluationVersion: "v1",
	})
	if !ok || valid.Threshold != "high" {
		t.Fatalf("expected normalized blocked decision, got %#v ok=%v", valid, ok)
	}

	for _, input := range []ReviewMergeGateDecisionInput{
		{Enabled: true, Threshold: "high", Conclusion: "success", BlockingFindings: 1, FindingCount: 1, EvaluationVersion: "v1"},
		{Enabled: false, Threshold: "critical", Conclusion: "success", FindingCount: 1, EvaluationVersion: "v1"},
		{Enabled: false, Threshold: "off", Conclusion: "failure", FindingCount: 1, EvaluationVersion: "v1"},
		{Enabled: true, Threshold: "unknown", Conclusion: "success", FindingCount: 0, EvaluationVersion: "v1"},
	} {
		if _, ok := NormalizeReviewMergeGateDecisionInput(input); ok {
			t.Fatalf("accepted inconsistent decision: %#v", input)
		}
	}
}
