package store

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/RainLib/open-review-platform/internal/rules"
)

func TestImpactRuleCounts(t *testing.T) {
	counts := impactRuleCounts([]rules.Rule{
		{Key: "security.secrets", Enforcement: rules.Mandatory, Severity: "critical"},
		{Key: "quality.errors", Enforcement: rules.Advisory, Severity: "high"},
		{Key: "quality.docs", Enforcement: rules.Advisory, Severity: "low"},
	})
	if counts.Total != 3 || counts.Mandatory != 1 || counts.Critical != 1 || counts.High != 1 || counts.Low != 1 {
		t.Fatalf("unexpected counts: %#v", counts)
	}
}

func TestDiffEffectiveRuleKeys(t *testing.T) {
	baseline := rules.Snapshot{Rules: []rules.EffectiveRule{
		{Key: "existing.changed", SourceVersion: "base", Severity: "medium", Content: json.RawMessage(`{"prompt":"before"}`)},
		{Key: "existing.removed", SourceVersion: "base", Severity: "low", Content: json.RawMessage(`{"prompt":"remove"}`)},
	}}
	candidate := rules.Snapshot{Rules: []rules.EffectiveRule{
		{Key: "existing.changed", SourceVersion: "candidate", Severity: "high", Content: json.RawMessage(`{"prompt":"after"}`)},
		{Key: "new.added", SourceVersion: "candidate", Severity: "medium", Content: json.RawMessage(`{"prompt":"new"}`)},
	}}
	added, changed, removed := diffEffectiveRuleKeys(baseline, candidate)
	if !reflect.DeepEqual(added, []string{"new.added"}) || !reflect.DeepEqual(changed, []string{"existing.changed"}) || !reflect.DeepEqual(removed, []string{"existing.removed"}) {
		t.Fatalf("unexpected diff: added=%v changed=%v removed=%v", added, changed, removed)
	}
}
