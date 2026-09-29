package domain

import (
	"strings"
	"time"
)

type PlatformIncidentState string

const (
	PlatformIncidentActive     PlatformIncidentState = "active"
	PlatformIncidentMitigating PlatformIncidentState = "mitigating"
	PlatformIncidentResolved   PlatformIncidentState = "resolved"
)

func (state PlatformIncidentState) Active() bool {
	return state == PlatformIncidentActive || state == PlatformIncidentMitigating
}

type PlatformIncidentInput struct {
	Title        string     `json:"title"`
	Scope        string     `json:"scope"`
	AffectedArea string     `json:"affected_area"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
}

func NormalizePlatformIncidentInput(input PlatformIncidentInput, now time.Time) (PlatformIncidentInput, bool) {
	input.Title = strings.TrimSpace(input.Title)
	input.Scope = strings.TrimSpace(input.Scope)
	input.AffectedArea = strings.TrimSpace(input.AffectedArea)
	if len([]rune(input.Title)) < 3 || len([]rune(input.Title)) > 180 ||
		len([]rune(input.AffectedArea)) < 3 || len([]rune(input.AffectedArea)) > 160 {
		return PlatformIncidentInput{}, false
	}
	switch input.Scope {
	case "workspace", "provider", "queue", "worker", "model", "data_governance", "identity":
	default:
		return PlatformIncidentInput{}, false
	}
	if input.StartedAt == nil {
		started := now.UTC()
		input.StartedAt = &started
		return input, true
	}
	started := input.StartedAt.UTC()
	// A future incident can distort the timeline and accidentally hide an
	// active outage. Permit only small transport clock skew.
	if started.After(now.UTC().Add(5*time.Minute)) || started.Before(now.UTC().Add(-366*24*time.Hour)) {
		return PlatformIncidentInput{}, false
	}
	input.StartedAt = &started
	return input, true
}

type PlatformIncidentResolutionInput struct {
	ExpectedRevision int    `json:"expected_revision"`
	Resolution       string `json:"resolution"`
}

func NormalizePlatformIncidentResolutionInput(input PlatformIncidentResolutionInput) (PlatformIncidentResolutionInput, bool) {
	input.Resolution = strings.TrimSpace(input.Resolution)
	if input.ExpectedRevision < 1 || len([]rune(input.Resolution)) < 3 || len([]rune(input.Resolution)) > 2000 {
		return PlatformIncidentResolutionInput{}, false
	}
	return input, true
}
