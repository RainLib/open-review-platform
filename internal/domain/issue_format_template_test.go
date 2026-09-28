package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeIssueFormatTemplateInputCanonicalizesBoundedContent(t *testing.T) {
	input := IssueFormatTemplateInput{
		Name:        "  Security boundary  ",
		Description: "  Reusable threat-evidence contract.  ",
		Content: json.RawMessage(`{
			"enabled":false,
			"preset":"security",
			"language":"zh-CN",
			"required_issue_sections":["outcome","evidence","security_impact","risk"],
			"response_sections":["assessment","risk","next_steps","provenance"],
			"collapse_secondary":true,
			"link_file_references":true,
			"reaction_feedback":true,
			"max_items_per_section":8,
			"custom_guidance":"  Require CWE evidence.  "
		}`),
	}
	normalized, hash, err := NormalizeIssueFormatTemplateInput(input, false)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Name != "Security boundary" || normalized.Description != "Reusable threat-evidence contract." || len(hash) != 64 {
		t.Fatalf("unexpected normalized template: %#v hash=%q", normalized, hash)
	}
	if strings.Contains(string(normalized.Content), `"enabled"`) || !strings.Contains(string(normalized.Content), `"custom_guidance":"Require CWE evidence."`) {
		t.Fatalf("template content was not normalized: %s", normalized.Content)
	}
	second, secondHash, err := NormalizeIssueFormatTemplateInput(normalized, false)
	if err != nil || string(second.Content) != string(normalized.Content) || secondHash != hash {
		t.Fatalf("canonicalization is not stable: second=%s hash=%q error=%v", second.Content, secondHash, err)
	}
}

func TestNormalizeIssueFormatTemplateInputRejectsUnsafeOrStaleInput(t *testing.T) {
	validContent := DefaultReviewConfig(ReviewConfigIssueTriage)
	tests := []IssueFormatTemplateInput{
		{Name: "x", Content: validContent},
		{Name: "unsafe\nname", Content: validContent},
		{Name: "Unsafe", Description: "<!-- hidden -->", Content: validContent},
		{Name: "Stale", ExpectedRevision: 1, Content: validContent},
		{Name: "Unknown section", Content: json.RawMessage(`{"preset":"custom","language":"en","required_issue_sections":["unknown"],"response_sections":["assessment"],"collapse_secondary":true,"link_file_references":true,"reaction_feedback":true,"max_items_per_section":4,"custom_guidance":""}`)},
	}
	for _, test := range tests {
		if _, _, err := NormalizeIssueFormatTemplateInput(test, false); err == nil {
			t.Fatalf("expected invalid template for %#v", test)
		}
	}
	validUpdate := IssueFormatTemplateInput{Name: "Updated", ExpectedRevision: 1, Content: validContent}
	if _, _, err := NormalizeIssueFormatTemplateInput(validUpdate, true); err != nil {
		t.Fatalf("valid update rejected: %v", err)
	}
}
