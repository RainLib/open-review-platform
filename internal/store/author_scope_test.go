package store

import (
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
)

func TestAutomaticReviewAuthorScopeUsesStableProviderID(t *testing.T) {
	for _, test := range []struct {
		name, scope, boundID, eventID string
		want                          bool
	}{
		{"legacy all", "", "", "", true},
		{"all authors", "all", "", "17", true},
		{"mine matches", "mine", "42", "42", true},
		{"mine excludes another author", "mine", "42", "17", false},
		{"mine fails closed without webhook ID", "mine", "42", "", false},
		{"mine fails closed without bound ID", "mine", "", "42", false},
		{"unknown policy fails closed", "unknown", "", "42", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			installation := domain.Installation{AuthorScope: test.scope, AuthorExternalID: test.boundID}
			event := domain.InboundEvent{AuthorExternalID: test.eventID}
			if got := automaticReviewAuthorAllowed(installation, event); got != test.want {
				t.Fatalf("allowed=%t, want %t", got, test.want)
			}
		})
	}
}

func TestInstallationAuthorScopePairValidation(t *testing.T) {
	for _, test := range []struct {
		scope, id string
		want      bool
	}{
		{"all", "", true}, {"all", "42", false},
		{"mine", "42", true}, {"mine", "", false},
		{"mine", "0", false}, {"mine", "01", false},
		{"mine", "1e2", false}, {"mine", "12345678901234567890", false},
		{"unknown", "", false},
	} {
		if got := validInstallationAuthorScope(test.scope, test.id); got != test.want {
			t.Fatalf("scope=%q id=%q valid=%t, want %t", test.scope, test.id, got, test.want)
		}
	}
}
