package publisher

import (
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
)

func TestMergeGateBlocksOnlyFindingsAtConfiguredThreshold(t *testing.T) {
	findings := []domain.Finding{{Severity: "critical"}, {Severity: "high"}, {Severity: "medium"}}
	critical := EvaluateMergeGate(findings, "critical")
	if critical.Conclusion != CheckFailure || critical.Blocking != 1 {
		t.Fatalf("critical gate verdict = %#v", critical)
	}
	high := EvaluateMergeGate(findings, "high")
	if high.Conclusion != CheckFailure || high.Blocking != 2 {
		t.Fatalf("high gate verdict = %#v", high)
	}
	if low := EvaluateMergeGate(findings, "off"); low.Conclusion != CheckSuccess || low.Blocking != 0 {
		t.Fatalf("advisory gate verdict = %#v", low)
	}
}

func TestMergeGateSummaryNamesBlockingDecision(t *testing.T) {
	findings := []domain.Finding{{Severity: "high"}}
	blocked := EvaluateMergeGate(findings, "high")
	if summary := blocked.Summary(findings); !strings.Contains(summary, "Merge gate blocked") || !strings.Contains(summary, "changes recommended") {
		t.Fatalf("missing blocking verdict: %s", summary)
	}
	passed := EvaluateMergeGate(findings, "critical")
	if summary := passed.Summary(findings); !strings.Contains(summary, "Merge gate passed") {
		t.Fatalf("missing passing verdict: %s", summary)
	}
}
