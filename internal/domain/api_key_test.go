package domain

import (
	"testing"
	"time"
)

func TestNormalizeAPIKeyInput(t *testing.T) {
	now := time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)
	expiresAt := now.Add(24 * time.Hour)
	input, ok := NormalizeAPIKeyInput(APIKeyInput{
		Name:         "  Release automation ",
		Scopes:       []string{APIKeyScopeReviewsCreate, APIKeyScopeReviewsRead, APIKeyScopeReviewsCreate},
		Repositories: []string{"RainLib/open-review-platform", "RainLib/open-review-platform"},
		ExpiresAt:    &expiresAt,
	}, now)
	if !ok {
		t.Fatal("expected valid API key input")
	}
	if input.Name != "Release automation" || len(input.Scopes) != 2 || len(input.Repositories) != 1 {
		t.Fatalf("unexpected normalized input: %#v", input)
	}
	if !input.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("unexpected expiry: %v", input.ExpiresAt)
	}
}

func TestNormalizeAPIKeyInputRejectsInvalidScopeRepositoryAndExpiry(t *testing.T) {
	now := time.Now().UTC()
	tests := []APIKeyInput{
		{Name: "key", Scopes: []string{"admin:*"}},
		{Name: "key", Scopes: []string{APIKeyScopeReviewsRead}, Repositories: []string{"single-segment"}},
		{Name: "key", Scopes: []string{APIKeyScopeReviewsRead}, ExpiresAt: pointerTime(now.Add(-time.Minute))},
	}
	for _, input := range tests {
		if _, ok := NormalizeAPIKeyInput(input, now); ok {
			t.Fatalf("expected invalid API key input: %#v", input)
		}
	}
}

func TestAPIKeyPrincipalAuthorization(t *testing.T) {
	principal := APIKeyPrincipal{Scopes: []string{APIKeyScopeReviewsCreate}, Repositories: []string{"RainLib/open-review-platform"}}
	if !principal.HasScope(APIKeyScopeReviewsCreate) || principal.HasScope(APIKeyScopeRunsCancel) {
		t.Fatal("scope authorization mismatch")
	}
	if !principal.AllowsRepository("RainLib/open-review-platform") || principal.AllowsRepository("RainLib/private") {
		t.Fatal("repository authorization mismatch")
	}
}

func pointerTime(value time.Time) *time.Time { return &value }
