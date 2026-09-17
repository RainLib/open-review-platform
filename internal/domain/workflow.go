package domain

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// RunState is the durable, user-visible state of a review execution. The
// revision grows on every transition, allowing clients to ignore stale SSE
// messages after reconnecting.
type RunState string

const (
	RunAcknowledged   RunState = "acknowledged"
	RunAdmitted       RunState = "admitted"
	RunPreparing      RunState = "preparing"
	RunAnalyzing      RunState = "analyzing"
	RunNormalizing    RunState = "normalizing"
	RunPublishing     RunState = "publishing"
	RunCompleted      RunState = "completed"
	RunFailed         RunState = "failed"
	RunCancelled      RunState = "cancelled"
	RunSuperseded     RunState = "superseded"
	RunNeedsAttention RunState = "needs_attention"
)

func (s RunState) Terminal() bool {
	return s == RunCompleted || s == RunFailed || s == RunCancelled || s == RunSuperseded || s == RunNeedsAttention
}

func (s RunState) CanTransitionTo(next RunState) bool {
	if s == next || s.Terminal() {
		return false
	}
	switch s {
	case RunAcknowledged:
		return next == RunAdmitted || next == RunCancelled || next == RunFailed
	case RunAdmitted:
		return next == RunPreparing || next == RunCancelled || next == RunSuperseded || next == RunFailed
	case RunPreparing:
		return next == RunAnalyzing || next == RunCancelled || next == RunSuperseded || next == RunFailed
	case RunAnalyzing:
		return next == RunNormalizing || next == RunCancelled || next == RunSuperseded || next == RunFailed
	case RunNormalizing:
		return next == RunPublishing || next == RunCancelled || next == RunSuperseded || next == RunFailed
	case RunPublishing:
		return next == RunCompleted || next == RunNeedsAttention || next == RunSuperseded || next == RunFailed
	default:
		return false
	}
}

func ValidateRunTransition(current, next RunState) error {
	if !current.CanTransitionTo(next) {
		return fmt.Errorf("invalid review run transition: %s -> %s", current, next)
	}
	return nil
}

type ReviewRequest struct {
	ID             uuid.UUID
	TenantID       uuid.UUID
	InstallationID uuid.UUID
	Provider       Provider
	APIBaseURL     string
	Repository     string
	ReviewNumber   int
	CurrentRunID   *uuid.UUID
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type ReviewRun struct {
	ID                uuid.UUID  `json:"id"`
	RequestID         uuid.UUID  `json:"request_id"`
	LegacyJobID       *uuid.UUID `json:"-"`
	Revision          int        `json:"revision"`
	State             RunState   `json:"state"`
	TriggerKind       string     `json:"trigger_kind"`
	HeadSHA           string     `json:"head_sha"`
	BaseSHA           string     `json:"base_sha"`
	CancelRequestedAt *time.Time `json:"cancel_requested_at,omitempty"`
	SupersededBy      *uuid.UUID `json:"superseded_by,omitempty"`
	FailureCode       string     `json:"failure_code,omitempty"`
	FailureMessage    string     `json:"failure_message,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	FinishedAt        *time.Time `json:"finished_at,omitempty"`
}

type ReviewRunSummary struct {
	ReviewRun
	Provider     Provider `json:"provider"`
	Repository   string   `json:"repository"`
	ReviewNumber int      `json:"review_number"`
}

type RunEvent struct {
	ID           uuid.UUID      `json:"id"`
	RunID        uuid.UUID      `json:"run_id"`
	Revision     int            `json:"revision"`
	EventType    string         `json:"event_type"`
	ActorKind    string         `json:"actor_kind"`
	ActorSubject string         `json:"actor_subject,omitempty"`
	Payload      map[string]any `json:"payload"`
	CreatedAt    time.Time      `json:"created_at"`
}

type OutboxMessage struct {
	ID          uuid.UUID
	AggregateID uuid.UUID
	Topic       string
	DedupeKey   string
	Payload     map[string]any
	Attempts    int
}
