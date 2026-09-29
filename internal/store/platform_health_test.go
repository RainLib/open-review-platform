package store

import "testing"

func TestProviderIssueTriageCapability(t *testing.T) {
	for _, test := range []struct {
		name        string
		permissions []string
		want        string
	}{
		{name: "ready", permissions: []string{"repository_inventory:read", "issue_triage:ready"}, want: "ready"},
		{name: "missing event", permissions: []string{"issue_triage:missing_event"}, want: "missing_event"},
		{name: "ignores unknown", permissions: []string{"issue_triage:future", "contents:read"}, want: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := providerIssueTriageCapability(test.permissions); got != test.want {
				t.Fatalf("providerIssueTriageCapability()=%q, want %q", got, test.want)
			}
		})
	}
}
