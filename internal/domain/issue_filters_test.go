package domain

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestIssueFilterExpressionStrictBoundedUnion(t *testing.T) {
	valid := []string{
		`{"condition":"and","items":[]}`,
		`{"condition":"or","items":[{"field":"severity","operator":"is","value":"critical"},{"condition":"and","items":[{"field":"rule","operator":"contains","value":"security"}]}]}`,
	}
	for _, raw := range valid {
		value, err := ParseIssueFilterExpression(raw)
		if err != nil {
			t.Fatalf("valid %s: %v", raw, err)
		}
		if _, err := ParseIssueFilterExpression(string(value.CanonicalJSON())); err != nil {
			t.Fatal(err)
		}
	}
	empty, _ := ParseIssueFilterExpression(valid[0])
	if string(empty.CanonicalJSON()) != valid[0] {
		t.Fatalf("empty AND wire=%s", empty.CanonicalJSON())
	}
	for _, raw := range []string{
		`null`, `{}`, `{"condition":"and"}`, `{"condition":"and","items":null}`,
		`{"condition":"or","items":[]}`, `{"condition":"and","items":[],"field":""}`,
		`{"condition":"and","items":[{"field":"status","operator":"is","value":"open","items":null}]}`,
		`{"condition":"and","items":[{"field":"status","operator":"is","value":null}]}`,
		`{"condition":"and","items":[{"field":"unknown","operator":"is","value":"open"}]}`,
		`{"condition":"and","items":[{"condition":"and","items":[]}]}`,
		`{"condition":"and","items":[]} {}`,
		`{"condition":"and","items":[{"field":"status","operator":"contains","value":"open"}]}`,
		`{"condition":"and","items":[{"field":"age","operator":"within","value":"1d"}]}`,
		`{"condition":"and","items":[{"field":"path","operator":"contains","value":"` + strings.Repeat("x", 513) + `"}]}`,
		strings.Repeat(" ", 8193),
	} {
		if _, err := ParseIssueFilterExpression(raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	leaf := IssueFilterExpression{Field: "status", Operator: "is", Value: "open"}
	tooMany := IssueFilterExpression{Condition: "and", Items: make([]IssueFilterExpression, 21)}
	for i := range tooMany.Items {
		tooMany.Items[i] = leaf
	}
	if tooMany.Valid() {
		t.Fatal("accepted >20 leaves")
	}
	tooDeep := IssueFilterExpression{Condition: "and", Items: []IssueFilterExpression{{Condition: "and", Items: []IssueFilterExpression{{Condition: "or", Items: []IssueFilterExpression{leaf}}}}}}
	if tooDeep.Valid() {
		t.Fatal("accepted depth4")
	}
}

func TestIssueSavedViewRequiresExplicitFilterDefinition(t *testing.T) {
	for _, raw := range []string{`{"name":"View","visibility":"personal","definition":{"view":"all"}}`, `{"name":"View","visibility":"personal","definition":{"view":"all","filters":null}}`} {
		var input IssueSavedViewInput
		if err := json.Unmarshal([]byte(raw), &input); err == nil && input.Valid() {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestIssueGroupedCursorBindsExpressionAnchorActorAndView(t *testing.T) {
	anchor := time.Now().UTC()
	expression, _ := ParseIssueFilterExpression(`{"condition":"and","items":[{"field":"age","operator":"within","value":"7d"}]}`)
	filter := IssueFilter{Filters: expression, FilterTime: &anchor, FilterActor: "Alice", View: "all", Limit: 1}
	filter.Cursor = EncodeIssueCursor(filter, IssueSummary{ID: uuid.New(), Status: IssueOpen, Severity: "high", LastSeenAt: anchor})
	if _, _, _, _, _, err := DecodeIssueCursor(filter); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*IssueFilter){
		func(f *IssueFilter) { later := anchor.Add(time.Second); f.FilterTime = &later },
		func(f *IssueFilter) { f.FilterActor = "alice" },
		func(f *IssueFilter) { f.View = "open" },
		func(f *IssueFilter) { f.Filters = &IssueFilterExpression{Condition: "and"} },
	} {
		changed := filter
		change(&changed)
		if _, _, _, _, _, err := DecodeIssueCursor(changed); err == nil {
			t.Fatalf("accepted changed cursor scope %#v", changed)
		}
	}
}
