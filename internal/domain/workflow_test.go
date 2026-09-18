package domain

import "testing"

func TestReviewModeRiskMode(t *testing.T) {
	tests := []struct {
		name       string
		mode       ReviewMode
		configured string
		want       string
	}{
		{name: "configured keeps deployment default", mode: ReviewModeConfigured, configured: "critical", want: "critical"},
		{name: "standard is balanced", mode: ReviewModeStandard, configured: "critical", want: "focused"},
		{name: "deep covers source delta", mode: ReviewModeDeep, configured: "focused", want: "standard"},
		{name: "security is high signal", mode: ReviewModeSecurity, configured: "standard", want: "critical"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.mode.RiskMode(test.configured); got != test.want {
				t.Fatalf("RiskMode(%q, %q) = %q, want %q", test.mode, test.configured, got, test.want)
			}
		})
	}
}

func TestRunStateTransitions(t *testing.T) {
	valid := [][2]RunState{
		{RunAcknowledged, RunAdmitted},
		{RunAdmitted, RunPreparing},
		{RunPreparing, RunAnalyzing},
		{RunAnalyzing, RunNormalizing},
		{RunNormalizing, RunPublishing},
		{RunPublishing, RunCompleted},
		{RunPublishing, RunNeedsAttention},
		{RunAnalyzing, RunSuperseded},
		{RunPreparing, RunCancelled},
	}
	for _, transition := range valid {
		if err := ValidateRunTransition(transition[0], transition[1]); err != nil {
			t.Fatalf("expected %s -> %s to be valid: %v", transition[0], transition[1], err)
		}
	}
}

func TestRunStateRejectsStaleAndTerminalTransitions(t *testing.T) {
	invalid := [][2]RunState{
		{RunAcknowledged, RunAnalyzing},
		{RunPublishing, RunAnalyzing},
		{RunCompleted, RunPublishing},
		{RunSuperseded, RunCompleted},
		{RunCancelled, RunFailed},
	}
	for _, transition := range invalid {
		if err := ValidateRunTransition(transition[0], transition[1]); err == nil {
			t.Fatalf("expected %s -> %s to be rejected", transition[0], transition[1])
		}
	}
}
