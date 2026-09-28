package domain

import (
	"testing"
	"time"
)

func TestNormalizePlatformIncidentInputSetsSafeDefaultStart(t *testing.T) {
	now := time.Date(2026, 9, 21, 8, 0, 0, 0, time.UTC)
	input, ok := NormalizePlatformIncidentInput(PlatformIncidentInput{
		Title: "  Provider publication is delayed  ", Scope: "provider", AffectedArea: " GitHub delivery ",
	}, now)
	if !ok || input.StartedAt == nil || !input.StartedAt.Equal(now) {
		t.Fatalf("normalized input=%#v ok=%v", input, ok)
	}
	if input.Title != "Provider publication is delayed" || input.AffectedArea != "GitHub delivery" {
		t.Fatalf("input was not trimmed: %#v", input)
	}
}

func TestNormalizePlatformIncidentInputRejectsUnsafeTimelineAndScope(t *testing.T) {
	now := time.Date(2026, 9, 21, 8, 0, 0, 0, time.UTC)
	future := now.Add(6 * time.Minute)
	if _, ok := NormalizePlatformIncidentInput(PlatformIncidentInput{
		Title: "Delayed worker lease", Scope: "shell", AffectedArea: "review runner",
	}, now); ok {
		t.Fatal("unknown incident scope was accepted")
	}
	if _, ok := NormalizePlatformIncidentInput(PlatformIncidentInput{
		Title: "Delayed worker lease", Scope: "worker", AffectedArea: "review runner", StartedAt: &future,
	}, now); ok {
		t.Fatal("future incident start was accepted")
	}
}

func TestNormalizePlatformIncidentResolutionInputRequiresRevisionAndReason(t *testing.T) {
	if _, ok := NormalizePlatformIncidentResolutionInput(PlatformIncidentResolutionInput{
		ExpectedRevision: 1, Resolution: "  Recovered after backlog drained.  ",
	}); !ok {
		t.Fatal("valid resolution was rejected")
	}
	if _, ok := NormalizePlatformIncidentResolutionInput(PlatformIncidentResolutionInput{
		ExpectedRevision: 0, Resolution: "Recovered",
	}); ok {
		t.Fatal("missing revision was accepted")
	}
}
