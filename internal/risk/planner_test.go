package risk

import "testing"

func TestClassifyPrioritizesSecurityAndStateOverDocumentation(t *testing.T) {
	high := Classify("internal/auth/payment/migration.go")
	low := Classify("docs/runbook.md")
	if high.Score < 75 || low.Score != 10 {
		t.Fatalf("unexpected scores: high=%#v low=%#v", high, low)
	}
	if !include(ModeFocused, high.Score) || include(ModeFocused, low.Score) {
		t.Fatal("focused mode must retain high-risk code and defer docs")
	}
}

func TestClassifyExcludesGeneratedArtifacts(t *testing.T) {
	item := Classify("internal/generated/client.go")
	if item.Score != 0 || include(ModeStandard, item.Score) {
		t.Fatalf("generated code must be excluded, got %#v", item)
	}
}

func TestCriticalModeFallsBackToTwoHighSignalBoundaries(t *testing.T) {
	plan := Plan{
		Deferred: []Item{
			{Path: "internal/api/server.go", Score: 70, Reasons: []string{"public boundary"}},
			{Path: "internal/webhook/normalize.go", Score: 70, Reasons: []string{"asynchronous workflow"}},
			{Path: "internal/store/postgres.go", Score: 45, Reasons: []string{"application code"}},
		},
	}
	plan = criticalFallback(plan)
	if len(plan.Selected) != 2 || plan.Selected[0].Path != "internal/api/server.go" || plan.Selected[1].Path != "internal/webhook/normalize.go" {
		t.Fatalf("unexpected critical fallback scope: %#v", plan.Selected)
	}
	if len(plan.Deferred) != 1 || plan.Deferred[0].Path != "internal/store/postgres.go" {
		t.Fatalf("unexpected critical fallback deferred scope: %#v", plan.Deferred)
	}
	if got := plan.Selected[0].Reasons[len(plan.Selected[0].Reasons)-1]; got != "critical-mode fallback: highest available high-signal boundary" {
		t.Fatalf("missing fallback provenance: %#v", plan.Selected[0])
	}
}
