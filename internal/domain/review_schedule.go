package domain

import (
	"time"

	"github.com/google/uuid"
)

// ReviewScheduleState describes the durable admission request, rather than a
// review-run stage. A scheduled review is intentionally separate from queued
// work: it has not consumed capacity or created a provider-facing run yet.
type ReviewScheduleState string

const (
	ReviewScheduleScheduled ReviewScheduleState = "scheduled"
	ReviewScheduleBlocked   ReviewScheduleState = "blocked"
	ReviewScheduleAdmitted  ReviewScheduleState = "admitted"
	ReviewScheduleCoalesced ReviewScheduleState = "coalesced"
	ReviewScheduleCancelled ReviewScheduleState = "cancelled"
)

func (state ReviewScheduleState) Cancellable() bool {
	return state == ReviewScheduleScheduled || state == ReviewScheduleBlocked
}

// ReviewScheduleInput is a one-shot request to re-admit an exact, already
// retained PR/MR revision. It does not pretend to discover a future provider
// head; the due-time admission creates a fresh rule/configuration snapshot.
type ReviewScheduleInput struct {
	ScheduledFor time.Time `json:"scheduled_for"`
}

func NormalizeReviewScheduleInput(input ReviewScheduleInput, now time.Time) (ReviewScheduleInput, bool) {
	now = now.UTC()
	input.ScheduledFor = input.ScheduledFor.UTC()
	if input.ScheduledFor.IsZero() || !input.ScheduledFor.After(now.Add(time.Minute)) || input.ScheduledFor.After(now.Add(90*24*time.Hour)) {
		return ReviewScheduleInput{}, false
	}
	return input, true
}

// ReviewSchedule is safe for the management console. It carries immutable
// provider identity and revision metadata, never a credential reference,
// clone URL, raw webhook payload, or rule/prompt content.
type ReviewSchedule struct {
	ID             uuid.UUID           `json:"id"`
	TenantID       uuid.UUID           `json:"-"`
	SourceRunID    uuid.UUID           `json:"source_run_id"`
	SourceJobID    uuid.UUID           `json:"-"`
	InstallationID uuid.UUID           `json:"-"`
	RequestID      uuid.UUID           `json:"-"`
	AdmittedRunID  *uuid.UUID          `json:"admitted_run_id,omitempty"`
	Revision       int                 `json:"revision"`
	State          ReviewScheduleState `json:"state"`
	Provider       Provider            `json:"provider"`
	APIBaseURL     string              `json:"api_base_url"`
	Repository     string              `json:"repository"`
	ReviewNumber   int                 `json:"review_number"`
	Title          string              `json:"title"`
	Author         string              `json:"author"`
	ReviewMode     ReviewMode          `json:"review_mode"`
	BaseSHA        string              `json:"base_sha"`
	HeadSHA        string              `json:"head_sha"`
	ScheduledFor   time.Time           `json:"scheduled_for"`
	RequestedBy    string              `json:"requested_by"`
	BlockedReason  string              `json:"blocked_reason,omitempty"`
	CancelledBy    string              `json:"cancelled_by,omitempty"`
	CancelledAt    *time.Time          `json:"cancelled_at,omitempty"`
	CreatedAt      time.Time           `json:"created_at"`
	UpdatedAt      time.Time           `json:"updated_at"`
}

func (schedule ReviewSchedule) ValidState() bool {
	switch schedule.State {
	case ReviewScheduleScheduled, ReviewScheduleBlocked, ReviewScheduleAdmitted, ReviewScheduleCoalesced, ReviewScheduleCancelled:
		return true
	default:
		return false
	}
}
