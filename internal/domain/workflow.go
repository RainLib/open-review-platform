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
	ID                uuid.UUID
	RequestID         uuid.UUID
	LegacyJobID       *uuid.UUID
	Revision          int
	State             RunState
	TriggerKind       string
	HeadSHA           string
	BaseSHA           string
	CancelRequestedAt *time.Time
	SupersededBy      *uuid.UUID
	FailureCode       string
	FailureMessage    string
	CreatedAt         time.Time
	StartedAt         *time.Time
	FinishedAt        *time.Time
}

type RunEvent struct {
	ID           uuid.UUID
	RunID        uuid.UUID
	Revision     int
	EventType    string
	ActorKind    string
	ActorSubject string
	Payload      map[string]any
	CreatedAt    time.Time
}

type OutboxMessage struct {
	ID          uuid.UUID
	AggregateID uuid.UUID
	Topic       string
	DedupeKey   string
	Payload     map[string]any
	Attempts    int
}
