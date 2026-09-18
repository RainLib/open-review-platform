package publisher

import (
	"fmt"
	"strings"

	"github.com/RainLib/open-review-platform/internal/domain"
)

// MergeGateSeverity is the minimum finding severity that turns the stable
// provider check into a merge-blocking failure. It is deliberately separate
// from job success: a review can execute and publish findings successfully
// while still refusing to let a protected branch merge.
type MergeGateSeverity string

const (
	MergeGateOff      MergeGateSeverity = "off"
	MergeGateCritical MergeGateSeverity = "critical"
	MergeGateHigh     MergeGateSeverity = "high"
	MergeGateMedium   MergeGateSeverity = "medium"
	MergeGateLow      MergeGateSeverity = "low"
)

func ValidMergeGateSeverity(value string) bool {
	switch MergeGateSeverity(strings.ToLower(strings.TrimSpace(value))) {
	case MergeGateOff, MergeGateCritical, MergeGateHigh, MergeGateMedium, MergeGateLow:
		return true
	default:
		return false
	}
}

type MergeGateVerdict struct {
	Conclusion CheckConclusion
	Threshold  MergeGateSeverity
	Blocking   int
}

// EvaluateMergeGate is deterministic policy applied after AI output is saved
// and published. This mirrors the Kodus separation between review execution
// and request-changes policy: model findings remain visible, while only an
// explicitly configured severity threshold governs merge eligibility.
func EvaluateMergeGate(findings []domain.Finding, threshold string) MergeGateVerdict {
	policy := MergeGateSeverity(strings.ToLower(strings.TrimSpace(threshold)))
	if !ValidMergeGateSeverity(string(policy)) {
		// Configuration validates this before the runner starts. Fail closed if a
		// caller bypasses that boundary, rather than silently making a required
		// check advisory.
		policy = MergeGateCritical
	}
	verdict := MergeGateVerdict{Conclusion: CheckSuccess, Threshold: policy}
	if policy == MergeGateOff {
		return verdict
	}
	minimum := severityRank(string(policy))
	for _, finding := range findings {
		if severityRank(finding.Severity) >= minimum {
			verdict.Blocking++
		}
	}
	if verdict.Blocking > 0 {
		verdict.Conclusion = CheckFailure
	}
	return verdict
}

func (v MergeGateVerdict) Summary(findings []domain.Finding) string {
	result := ResultSummary(findings)
	if v.Threshold == MergeGateOff {
		return "Merge gate is advisory (disabled).\n\n" + result
	}
	if v.Blocking > 0 {
		return fmt.Sprintf("Merge gate blocked: %d finding(s) meet the %q threshold.\n\n%s", v.Blocking, v.Threshold, result)
	}
	return fmt.Sprintf("Merge gate passed: no findings meet the %q threshold.\n\n%s", v.Threshold, result)
}

func severityRank(value string) int {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "critical":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}
