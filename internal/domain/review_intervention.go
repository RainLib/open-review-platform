package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// ReviewInterventionState tracks the human operational task attached to a
// terminal review. It is deliberately independent of ReviewRun: closing an
// intervention never rewrites the immutable execution evidence.
type ReviewInterventionState string

const (
	ReviewInterventionOpen     ReviewInterventionState = "open"
	ReviewInterventionClaimed  ReviewInterventionState = "claimed"
	ReviewInterventionResolved ReviewInterventionState = "resolved"
)

func (state ReviewInterventionState) Active() bool {
	return state == ReviewInterventionOpen || state == ReviewInterventionClaimed
}

// ReviewIntervention is the safe console projection. It never contains raw
// provider payloads, credentials, or model traces; the linked run retains the
// authoritative, immutable evidence.
type ReviewIntervention struct {
	ID              uuid.UUID               `json:"id"`
	TenantID        uuid.UUID               `json:"-"`
	RunID           uuid.UUID               `json:"run_id"`
	Revision        int                     `json:"revision"`
	State           ReviewInterventionState `json:"state"`
	AssigneeSubject string                  `json:"assignee_subject,omitempty"`
	OpenedAt        time.Time               `json:"opened_at"`
	ClaimedAt       *time.Time              `json:"claimed_at,omitempty"`
	ResolvedAt      *time.Time              `json:"resolved_at,omitempty"`
	ResolvedBy      string                  `json:"resolved_by,omitempty"`
	Resolution      string                  `json:"resolution,omitempty"`
	Reason          string                  `json:"reason,omitempty"`
	CreatedAt       time.Time               `json:"created_at"`
	UpdatedAt       time.Time               `json:"updated_at"`
	RunRevision     int                     `json:"run_revision,omitempty"`
	RunState        RunState                `json:"run_state,omitempty"`
	FailureCode     string                  `json:"failure_code,omitempty"`
	FailureMessage  string                  `json:"failure_message,omitempty"`
	Provider        Provider                `json:"provider,omitempty"`
	APIBaseURL      string                  `json:"api_base_url,omitempty"`
	Repository      string                  `json:"repository,omitempty"`
	ReviewNumber    int                     `json:"review_number,omitempty"`
	Title           string                  `json:"title,omitempty"`
	Author          string                  `json:"author,omitempty"`
}

type ReviewInterventionFilter struct {
	RunID      *uuid.UUID
	ActiveOnly bool
	Limit      int
}

func (filter ReviewInterventionFilter) Valid() bool {
	return filter.Limit >= 1 && filter.Limit <= 100 && (filter.RunID == nil || *filter.RunID != uuid.Nil)
}

// ReviewInterventionResolutionInput records why an operator intentionally
// removes a terminal run from the active queue. It is not a retry operation.
type ReviewInterventionResolutionInput struct {
	ExpectedRevision int    `json:"expected_revision"`
	Reason           string `json:"reason"`
}

func NormalizeReviewInterventionResolutionInput(input ReviewInterventionResolutionInput) (ReviewInterventionResolutionInput, bool) {
	input.Reason = strings.TrimSpace(input.Reason)
	if input.ExpectedRevision < 1 || len([]rune(input.Reason)) < 3 || len([]rune(input.Reason)) > 2000 {
		return ReviewInterventionResolutionInput{}, false
	}
	return input, true
}
