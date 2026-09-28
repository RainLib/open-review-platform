package store

import (
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

func TestRouteGlobMatching(t *testing.T) {
	for _, test := range []struct {
		pattern string
		value   string
		want    bool
	}{
		{pattern: "*", value: "RainLib/open-review-platform", want: true},
		{pattern: "RainLib/*", value: "rainlib/open-review-platform", want: true},
		{pattern: "release/*", value: "release/2026-09", want: true},
		{pattern: "main", value: "main", want: true},
		{pattern: "main", value: "feature/main", want: false},
		{pattern: "security/*", value: "release/2026-09", want: false},
	} {
		if got := routeGlobMatches(test.pattern, test.value); got != test.want {
			t.Fatalf("routeGlobMatches(%q, %q) = %t, want %t", test.pattern, test.value, got, test.want)
		}
	}
}

func TestProviderReviewURL(t *testing.T) {
	for _, test := range []struct {
		provider domain.Provider
		apiBase  string
		want     string
	}{
		{provider: domain.ProviderGitHub, apiBase: "https://api.github.com", want: "https://github.com/RainLib/open-review-platform/pull/3"},
		{provider: domain.ProviderGitHub, apiBase: "https://github.example.com/api/v3", want: "https://github.example.com/RainLib/open-review-platform/pull/3"},
		{provider: domain.ProviderGitLab, apiBase: "https://gitlab.example.com/api/v4", want: "https://gitlab.example.com/RainLib/open-review-platform/-/merge_requests/3"},
	} {
		if got := providerReviewURL(test.provider, test.apiBase, "RainLib/open-review-platform", 3); got != test.want {
			t.Fatalf("providerReviewURL(%q) = %q, want %q", test.apiBase, got, test.want)
		}
	}
}

func TestNotificationRouteGlobValidation(t *testing.T) {
	for _, value := range []string{"*", "RainLib/*", "release/*", "main"} {
		if !validRouteGlob(value) {
			t.Fatalf("validRouteGlob(%q) = false", value)
		}
	}
	for _, value := range []string{"", "*/main", "release/**", "[main]", "main?"} {
		if validRouteGlob(value) {
			t.Fatalf("validRouteGlob(%q) = true", value)
		}
	}
}

func TestNotificationRoutePreviewValidation(t *testing.T) {
	valid := domain.NotificationRoutePreviewInput{
		Repository: "RainLib/open-review-platform", TargetBranch: "main", EventType: "review.run.completed", HighestSeverity: "high", FindingCount: 2,
	}
	if !validNotificationRoutePreview(valid) {
		t.Fatal("expected complete review event preview to be valid")
	}
	valid.FindingCount, valid.HighestSeverity = 0, "none"
	if !validNotificationRoutePreview(valid) {
		t.Fatal("expected no-finding review event preview to be valid")
	}
	for _, invalid := range []domain.NotificationRoutePreviewInput{
		{Repository: "RainLib/*", TargetBranch: "main", EventType: "review.run.completed", HighestSeverity: "high", FindingCount: 1},
		{Repository: "RainLib/open-review-platform", TargetBranch: "*", EventType: "review.run.completed", HighestSeverity: "high", FindingCount: 1},
		{Repository: "RainLib/open-review-platform", TargetBranch: "main", EventType: "review.run.started", HighestSeverity: "high", FindingCount: 1},
		{Repository: "RainLib/open-review-platform", TargetBranch: "main", EventType: "review.run.completed", HighestSeverity: "none", FindingCount: 1},
	} {
		if validNotificationRoutePreview(invalid) {
			t.Fatalf("expected preview to be invalid: %#v", invalid)
		}
	}
}

func TestNotificationRoutePreviewDecisionSharesMatchingAndDeduplication(t *testing.T) {
	destinationID := uuid.New()
	route := domain.NotificationRoute{
		DestinationID: destinationID, RepositoryGlob: "RainLib/*", BranchGlob: "main", EventTypes: []string{"review.run.completed"}, MinSeverity: "medium", Enabled: true,
	}
	destination := domain.NotificationDestination{ID: destinationID, Enabled: true}
	event := domain.NotificationEvent{Repository: "RainLib/open-review-platform", TargetBranch: "main", Type: "review.run.completed", HighestSeverity: "high", FindingCount: 1}
	selected := make(map[uuid.UUID]struct{})
	if disposition, reason := notificationRoutePreviewDecision(route, destination, event, selected); disposition != "selected" || reason != "event, repository, branch, and severity match" {
		t.Fatalf("first decision=%q %q", disposition, reason)
	}
	if disposition, reason := notificationRoutePreviewDecision(route, destination, event, selected); disposition != "deduplicated" || reason != "an earlier matching route already selected this destination" {
		t.Fatalf("second decision=%q %q", disposition, reason)
	}
	for _, test := range []struct {
		name   string
		mutate func(*domain.NotificationRoute, *domain.NotificationDestination, *domain.NotificationEvent)
	}{
		{name: "paused route", mutate: func(route *domain.NotificationRoute, _ *domain.NotificationDestination, _ *domain.NotificationEvent) {
			route.Enabled = false
		}},
		{name: "paused destination", mutate: func(_ *domain.NotificationRoute, destination *domain.NotificationDestination, _ *domain.NotificationEvent) {
			destination.Enabled = false
		}},
		{name: "event mismatch", mutate: func(_ *domain.NotificationRoute, _ *domain.NotificationDestination, event *domain.NotificationEvent) {
			event.Type = "review.run.failed"
		}},
		{name: "repository mismatch", mutate: func(_ *domain.NotificationRoute, _ *domain.NotificationDestination, event *domain.NotificationEvent) {
			event.Repository = "Other/repository"
		}},
		{name: "branch mismatch", mutate: func(_ *domain.NotificationRoute, _ *domain.NotificationDestination, event *domain.NotificationEvent) {
			event.TargetBranch = "release/2026"
		}},
		{name: "threshold mismatch", mutate: func(_ *domain.NotificationRoute, _ *domain.NotificationDestination, event *domain.NotificationEvent) {
			event.HighestSeverity = "low"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidateRoute, candidateDestination, candidateEvent := route, destination, event
			test.mutate(&candidateRoute, &candidateDestination, &candidateEvent)
			if disposition, reason := notificationRoutePreviewDecision(candidateRoute, candidateDestination, candidateEvent, make(map[uuid.UUID]struct{})); disposition != "filtered" || reason == "" {
				t.Fatalf("decision=%q reason=%q", disposition, reason)
			}
		})
	}
}
