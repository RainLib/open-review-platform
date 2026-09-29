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

func TestCompileCarriesDeterministicBindingFileFilters(t *testing.T) {
	compiled, err := Compile([]Source{
		{VersionID: "repo-v1", Precedence: 30, Include: []string{"services/payments/**", "services/payments/**"}, Exclude: []string{"**/fixtures/**"}, Rules: []Rule{testRule("payments.idempotency", Mandatory, DenyOverride, "critical", `{"prompt":"idempotent"}`)}},
		{VersionID: "tenant-v1", Precedence: 10, Include: []string{"packages/billing/**"}, Exclude: []string{"**/generated/**"}, Rules: []Rule{testRule("security.secrets", Mandatory, DenyOverride, "critical", `{"prompt":"no secrets"}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(compiled.Snapshot.Include, ","), "packages/billing/**,services/payments/**"; got != want {
		t.Fatalf("include = %q, want %q", got, want)
	}
	if got, want := strings.Join(compiled.Snapshot.Exclude, ","), "**/fixtures/**,**/generated/**"; got != want {
		t.Fatalf("exclude = %q, want %q", got, want)
	}
	if !strings.Contains(string(compiled.Canonical), `"schema_version":2`) {
		t.Fatalf("expected v2 canonical snapshot, got %s", compiled.Canonical)
	}
}

func TestApplyExceptionsRemovesOnlyExactSourceAndChangesCanonicalSHA(t *testing.T) {
	first := "11111111-1111-1111-1111-111111111111"
	second := "22222222-2222-2222-2222-222222222222"
	compiled, err := Compile([]Source{
		{VersionID: first, Precedence: 10, Rules: []Rule{testRule("security.auth", Advisory, Append, "medium", `{"prompt":"first"}`)}},
		{VersionID: second, Precedence: 20, Rules: []Rule{testRule("security.auth", Advisory, Append, "high", `{"prompt":"second"}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	withException, err := ApplyExceptions(compiled, []AppliedException{{
		ID: "exception-1", RuleKey: "security.auth", SourceVersion: first,
		ScopeKind: "repository", ScopeRef: "RainLib/demo", ExpiresAt: "2026-12-01T00:00:00Z",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(withException.Snapshot.Rules) != 1 || withException.Snapshot.Rules[0].SourceVersion != second {
		t.Fatalf("exception removed the wrong effective entries: %#v", withException.Snapshot.Rules)
	}
	if len(withException.Snapshot.AppliedExceptions) != 1 || withException.SHA256 == compiled.SHA256 {
		t.Fatalf("exception provenance must be canonical: %#v", withException)
	}
}

func TestApplyExceptionsIsDeterministicAcrossInputOrder(t *testing.T) {
	version := "11111111-1111-1111-1111-111111111111"
	compiled, err := Compile([]Source{{VersionID: version, Rules: []Rule{
		testRule("a", Advisory, Replace, "low", `{"prompt":"a"}`),
		testRule("b", Advisory, Replace, "low", `{"prompt":"b"}`),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	left := []AppliedException{
		{ID: "b", RuleKey: "b", SourceVersion: version, ScopeKind: "tenant", ExpiresAt: "2026-12-01T00:00:00Z"},
		{ID: "a", RuleKey: "a", SourceVersion: version, ScopeKind: "tenant", ExpiresAt: "2026-12-01T00:00:00Z"},
	}
	right := []AppliedException{left[1], left[0]}
	one, err := ApplyExceptions(compiled, left)
	if err != nil {
		t.Fatal(err)
	}
	two, err := ApplyExceptions(compiled, right)
	if err != nil {
		t.Fatal(err)
	}
	if one.SHA256 != two.SHA256 || string(one.Canonical) != string(two.Canonical) {
		t.Fatalf("exception order changed canonical snapshot: %s != %s", one.SHA256, two.SHA256)
	}
}
