package main

import (
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

func TestResponseFromPayloadPreservesAcknowledgementBarrier(t *testing.T) {
	runID := uuid.New()
	response, err := responseFromPayload(map[string]any{
		"provider":                 "github",
		"api_base_url":             "https://api.github.com",
		"installation_external_id": "42",
		"credential_ref":           "github-app",
		"repository":               "RainLib/demo",
		"review_number":            float64(4),
		"comment_external_id":      "99",
		"reaction":                 "eyes",
		"release_run_id":           runID.String(),
		"body":                     "Review is queued.",
		"marker":                   "open-review-platform:interaction:test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Reaction != domain.InteractionReactionEyes || response.ReleaseRunID == nil || *response.ReleaseRunID != runID {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestResponseFromPayloadRejectsInvalidAcknowledgementBarrier(t *testing.T) {
	_, err := responseFromPayload(map[string]any{
		"provider":                 "github",
		"api_base_url":             "https://api.github.com",
		"installation_external_id": "42",
		"credential_ref":           "github-app",
		"repository":               "RainLib/demo",
		"review_number":            float64(4),
		"comment_external_id":      "99",
		"reaction":                 "eyes",
		"release_run_id":           "not-a-uuid",
		"body":                     "Review is queued.",
		"marker":                   "open-review-platform:interaction:test",
	})
	if err == nil {
		t.Fatal("expected invalid release run id to be rejected")
	}
}
