package store

import (
	"strings"
	"testing"
)

func TestConsoleLinkPreservesDeploymentPathAndEscapesTenant(t *testing.T) {
	got := consoleLink("https://review.example.com/console/", "team space", "/reviews/run-1?tab=findings#finding-1")
	want := "https://review.example.com/console/team%20space/reviews/run-1?tab=findings#finding-1"
	if got != want {
		t.Fatalf("console link = %q, want %q", got, want)
	}
	got = consoleLink("http://127.0.0.1:3110", "acme", "/agent-work?task=abc")
	if got != "http://127.0.0.1:3110/acme/agent-work?task=abc" {
		t.Fatalf("local Console link = %q", got)
	}
}

func TestConsoleLinkRejectsUntrustedOrMalformedConfiguration(t *testing.T) {
	for _, base := range []string{"", "javascript:alert(1)", "https://user:secret@review.example.com", "https://review.example.com/?next=evil", "https://review.example.com/#fragment", "//review.example.com"} {
		if got := consoleLink(base, "acme", "/reviews/run-1"); got != "" {
			t.Errorf("base %q unexpectedly produced %q", base, got)
		}
	}
	for _, route := range []string{"https://other.example.com", "//other.example.com", "reviews/run-1"} {
		if got := consoleLink("https://review.example.com", "acme", route); got != "" {
			t.Errorf("route %q unexpectedly produced %q", route, got)
		}
	}
	if got := consoleLink("https://review.example.com", "", "/reviews/run-1"); got != "" {
		t.Errorf("empty tenant unexpectedly produced %q", got)
	}
	if got := consoleLink("https://review.example.com", "acme/private", "/reviews/run-1"); !strings.Contains(got, "/acme%2Fprivate/") {
		t.Errorf("tenant separator was not escaped: %q", got)
	}
}
