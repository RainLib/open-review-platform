package rules

import (
	"encoding/json"
	"strings"
	"testing"
)

func testRule(key string, enforcement Enforcement, behavior MergeBehavior, severity, content string) Rule {
	return Rule{Key: key, Enforcement: enforcement, MergeBehavior: behavior, Severity: severity, Content: json.RawMessage(content)}
}

func TestCompileIsDeterministicAcrossInputOrder(t *testing.T) {
	first, err := Compile([]Source{
		{VersionID: "repo-v1", Precedence: 30, Rules: []Rule{testRule("repo.error-handling", Advisory, Replace, "medium", `{"prompt":"handle errors"}`)}},
		{VersionID: "org-v1", Precedence: 10, Rules: []Rule{testRule("security.secrets", Mandatory, DenyOverride, "critical", `{"prompt":"never expose secrets"}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Compile([]Source{
		{VersionID: "org-v1", Precedence: 10, Rules: []Rule{testRule("security.secrets", Mandatory, DenyOverride, "critical", ` { "prompt" : "never expose secrets" } `)}},
		{VersionID: "repo-v1", Precedence: 30, Rules: []Rule{testRule("repo.error-handling", Advisory, Replace, "medium", `{"prompt":"handle errors"}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.SHA256 != second.SHA256 || string(first.Canonical) != string(second.Canonical) {
		t.Fatalf("canonical snapshot changed: first=%s second=%s", first.Canonical, second.Canonical)
	}
}

func TestCompileRejectsMandatoryWeakeningAndDisable(t *testing.T) {
	mandatory := Source{VersionID: "org-v1", Precedence: 10, Rules: []Rule{testRule("security.secrets", Mandatory, DenyOverride, "critical", `{"prompt":"never expose secrets"}`)}}
	for _, lower := range []Rule{
		testRule("security.secrets", Advisory, Replace, "low", `{"prompt":"optional"}`),
		{Key: "security.secrets", Enforcement: Advisory, MergeBehavior: Replace, Severity: "low", Disabled: true},
	} {
		_, err := Compile([]Source{mandatory, {VersionID: "repo-v1", Precedence: 30, Rules: []Rule{lower}}})
		if err == nil || !strings.Contains(err.Error(), "mandatory") {
			t.Fatalf("expected mandatory protection error, got %v", err)
		}
	}
}

func TestCompileRejectsSameLayerConflictAndRetainsAppendSources(t *testing.T) {
	_, err := Compile([]Source{
		{VersionID: "a", Precedence: 10, Rules: []Rule{testRule("same", Advisory, Replace, "medium", `{}`)}},
		{VersionID: "b", Precedence: 10, Rules: []Rule{testRule("same", Advisory, Replace, "medium", `{}`)}},
	})
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("expected same-layer conflict, got %v", err)
	}
	compiled, err := Compile([]Source{
		{VersionID: "base", Precedence: 10, Rules: []Rule{testRule("style.naming", Advisory, Append, "low", `{"one":1}`)}},
		{VersionID: "repo", Precedence: 30, Rules: []Rule{testRule("style.naming", Advisory, Append, "medium", `{"two":2}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.Snapshot.Rules) != 2 || compiled.Snapshot.Rules[0].SourceVersion != "base" || compiled.Snapshot.Rules[1].SourceVersion != "repo" {
		t.Fatalf("append sources lost: %#v", compiled.Snapshot.Rules)
	}
}
