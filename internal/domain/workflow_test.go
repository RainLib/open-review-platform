package domain

import "testing"

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
