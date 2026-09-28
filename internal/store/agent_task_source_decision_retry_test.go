package store

import "testing"

func TestAgentSourceDecisionRetryPayloadBounds(t *testing.T) {
	for _, tc := range []struct {
		payload map[string]any
		attempt int
		valid   bool
	}{
		{map[string]any{}, 1, true},
		{map[string]any{"source_decision_attempt": float64(2)}, 2, true},
		{map[string]any{"source_decision_attempt": float64(3)}, 3, true},
		{map[string]any{"source_decision_attempt": float64(4)}, 0, false},
		{map[string]any{"source_decision_attempt": float64(1.5)}, 0, false},
		{map[string]any{"source_decision_attempt": "2"}, 0, false},
	} {
		got, valid := agentSourceDecisionAttempt(tc.payload)
		if got != tc.attempt || valid != tc.valid {
			t.Errorf("attempt payload=%#v: got %d/%t, want %d/%t", tc.payload, got, valid, tc.attempt, tc.valid)
		}
	}
	for _, tc := range []struct {
		payload  map[string]any
		revision int
		valid    bool
	}{
		{map[string]any{}, 1, true},
		{map[string]any{"task_revision": float64(2)}, 2, true},
		{map[string]any{"task_revision": float64(0)}, 0, false},
		{map[string]any{"task_revision": float64(2.5)}, 0, false},
		{map[string]any{"task_revision": "2"}, 0, false},
	} {
		got, valid := AgentSourceTaskRevision(tc.payload)
		if got != tc.revision || valid != tc.valid {
			t.Errorf("revision payload=%#v: got %d/%t, want %d/%t", tc.payload, got, valid, tc.revision, tc.valid)
		}
	}
}
