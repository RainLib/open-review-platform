package rules

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOCRRuleFileForSnapshotAggregatesEffectiveRules(t *testing.T) {
	file, err := OCRRuleFileForSnapshot(Snapshot{
		Engine: "ocr", MergeSystemRule: true,
		Rules: []EffectiveRule{
			{Key: "payments.idempotency", SourceVersion: "version-1", Enforcement: Mandatory, Severity: "critical", Content: json.RawMessage(`{"prompt":"Verify idempotent retries."}`)},
			{Key: "errors.handling", SourceVersion: "version-2", Enforcement: Advisory, Severity: "medium", Content: json.RawMessage(`{"rule":"Check errors are returned."}`)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Rules) != 1 || file.Rules[0].Path != "**/*" || !file.Rules[0].MergeSystemRule {
		t.Fatalf("unexpected OCR file: %#v", file)
	}
	for _, expected := range []string{"payments.idempotency", "Verify idempotent retries.", "errors.handling", "Check errors are returned."} {
		if !strings.Contains(file.Rules[0].Rule, expected) {
			t.Fatalf("rule aggregate lacks %q: %s", expected, file.Rules[0].Rule)
		}
	}
}

func TestOCRRuleFileForSnapshotRejectsUnexecutableContent(t *testing.T) {
	_, err := OCRRuleFileForSnapshot(Snapshot{Engine: "ocr", Rules: []EffectiveRule{{Key: "invalid", Content: json.RawMessage(`{}`)}}})
	if err == nil || !strings.Contains(err.Error(), "content.prompt") {
		t.Fatalf("expected OCR content validation error, got %v", err)
	}
}
