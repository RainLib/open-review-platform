package domain

import (
	"encoding/hex"
	"strings"
	"time"
)

// ReviewMergeGateDecision is the immutable result of applying the admitted
// general review policy to the findings retained for one run. It is distinct
// from a provider check receipt: a provider publication can fail or be
// unavailable without changing the decision that Open Review made.
type ReviewMergeGateDecision struct {
	Enabled                    bool      `json:"enabled"`
	Threshold                  string    `json:"threshold"`
	Conclusion                 string    `json:"conclusion"`
	BlockingFindings           int       `json:"blocking_findings"`
	FindingCount               int       `json:"finding_count"`
	ConfigurationContentSHA256 string    `json:"configuration_content_sha256"`
	OriginScopeKind            string    `json:"origin_scope_kind"`
	OriginScopeRef             string    `json:"origin_scope_ref,omitempty"`
	OriginRevision             int       `json:"origin_revision"`
	EvaluationVersion          string    `json:"evaluation_version"`
	DecidedAt                  time.Time `json:"decided_at"`
}

// ReviewMergeGateDecisionInput is supplied by the worker after it has saved
// normalized findings. The store resolves configuration provenance from the
// admitted general snapshot, never from the current workspace setting.
type ReviewMergeGateDecisionInput struct {
	Enabled           bool
	Threshold         string
	Conclusion        string
	BlockingFindings  int
	FindingCount      int
	EvaluationVersion string
}

func NormalizeReviewMergeGateDecisionInput(input ReviewMergeGateDecisionInput) (ReviewMergeGateDecisionInput, bool) {
	input.Threshold = strings.ToLower(strings.TrimSpace(input.Threshold))
	input.Conclusion = strings.ToLower(strings.TrimSpace(input.Conclusion))
	input.EvaluationVersion = strings.TrimSpace(input.EvaluationVersion)
	if input.EvaluationVersion == "" || len(input.EvaluationVersion) > 64 || input.BlockingFindings < 0 || input.FindingCount < input.BlockingFindings {
		return ReviewMergeGateDecisionInput{}, false
	}
	if input.Conclusion != "success" && input.Conclusion != "failure" {
		return ReviewMergeGateDecisionInput{}, false
	}
	if !input.Enabled {
		return input, input.Threshold == "off" && input.Conclusion == "success" && input.BlockingFindings == 0
	}
	switch input.Threshold {
	case "critical", "high", "medium", "low":
		return input, (input.Conclusion == "failure") == (input.BlockingFindings > 0)
	default:
		return ReviewMergeGateDecisionInput{}, false
	}
}

func (decision ReviewMergeGateDecision) Valid() bool {
	input, ok := NormalizeReviewMergeGateDecisionInput(ReviewMergeGateDecisionInput{
		Enabled:           decision.Enabled,
		Threshold:         decision.Threshold,
		Conclusion:        decision.Conclusion,
		BlockingFindings:  decision.BlockingFindings,
		FindingCount:      decision.FindingCount,
		EvaluationVersion: decision.EvaluationVersion,
	})
	if !ok || input.Threshold != decision.Threshold || decision.OriginRevision < 0 || len(decision.ConfigurationContentSHA256) != 64 || decision.OriginScopeKind == "" || decision.DecidedAt.IsZero() {
		return false
	}
	_, err := hex.DecodeString(decision.ConfigurationContentSHA256)
	return err == nil
}
