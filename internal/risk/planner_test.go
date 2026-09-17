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
