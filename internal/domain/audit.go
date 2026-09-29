package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type AuditEvent struct {
	ID           uuid.UUID       `json:"id"`
	ActorSubject string          `json:"actor_subject"`
	Action       string          `json:"action"`
	Target       string          `json:"target"`
	Metadata     json.RawMessage `json:"metadata"`
	CreatedAt    time.Time       `json:"created_at"`
}

type AuditFilter struct {
	Actor  string
	Action string
	Target string
	From   *time.Time
	Until  *time.Time
	Limit  int
	Before *uuid.UUID
}
