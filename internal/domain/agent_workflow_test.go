package domain

import (
	"strings"
	"testing"
	"time"
)

func TestAgentAcceptanceRequiresFreshIndependentExactCommitChecks(t *testing.T) {
	head := strings.Repeat("a", 40)
	now := time.Now()
	policy := AgentWorkflowPolicy{Enabled: true, MaxRepairCycles: 2, MaxTaskAttempts: 3, RequiredChecks: []string{"CI / tests"}}
	base := ProviderCheckObservation{State: "observed", HeadSHA: head, ObservedAt: &now, Checks: []ProviderCheck{{Name: "CI / tests", State: "success", Origin: "independent"}}}
	cases := []struct {
		name   string
		change func(*ProviderCheckObservation)
	}{
		{"wrong commit", func(o *ProviderCheckObservation) { o.HeadSHA = strings.Repeat("b", 40) }},
		{"incomplete page", func(o *ProviderCheckObservation) { o.Truncated = true }},
		{"pending snapshot", func(o *ProviderCheckObservation) { o.State = "running" }},
		{"own check", func(o *ProviderCheckObservation) {
			o.Checks = []ProviderCheck{{Name: "CI / tests", State: "success", Origin: "open_review"}}
		}},
		{"unknown origin", func(o *ProviderCheckObservation) { o.Checks = []ProviderCheck{{Name: "CI / tests", State: "success"}} }},
		{"missing required check", func(o *ProviderCheckObservation) {
			o.Checks = []ProviderCheck{{Name: "other", State: "success", Origin: "independent"}}
		}},
		{"failed check", func(o *ProviderCheckObservation) {
			o.Checks = []ProviderCheck{{Name: "CI / tests", State: "failure", Origin: "independent"}}
		}},
		{"expired snapshot", func(o *ProviderCheckObservation) { past := now.Add(-11 * time.Minute); o.ObservedAt = &past }},
		{"future snapshot", func(o *ProviderCheckObservation) { future := now.Add(time.Hour); o.ObservedAt = &future }},
		{"no snapshot time", func(o *ProviderCheckObservation) { o.ObservedAt = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := base
			tc.change(&o)
			if ready, _ := AgentChecksReady(o, policy, head); ready {
				t.Fatal("unverified acceptance allowed")
			}
		})
	}
	if ready, reason := AgentChecksReady(base, policy, head); !ready {
		t.Fatal(reason)
	}
}

func TestAgentWorkflowBudgetsAndAcceptanceInput(t *testing.T) {
	if !(AgentWorkflowPolicy{}).Valid() {
		t.Fatal("legacy policy rejected")
	}
	for _, p := range []AgentWorkflowPolicy{{MaxRepairCycles: 1}, {Enabled: true}, {Enabled: true, MaxTaskAttempts: 6}, {Enabled: true, MaxTaskAttempts: 2, MaxRepairCycles: 4}, {Enabled: true, MaxTaskAttempts: 2, RequiredChecks: []string{"CI", "CI"}}} {
		if p.Valid() {
			t.Fatalf("invalid budget accepted: %+v", p)
		}
	}
	input := AgentTaskAcceptanceInput{Revision: 1, HeadSHA: strings.Repeat("a", 40), Decision: "accepted", Reason: "Verified behavior", Evidence: []string{"Regression test passed"}}
	if !input.Valid() {
		t.Fatal("valid evidence rejected")
	}
	input.Evidence = []string{""}
	if input.Valid() {
		t.Fatal("empty evidence accepted")
	}
}
