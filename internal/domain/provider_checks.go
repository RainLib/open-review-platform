package domain

import (
	"time"

	"github.com/google/uuid"
)

// ProviderCheck is source evidence for one external check on an immutable
// commit. It never changes an Open Review merge-gate decision.
type ProviderCheck struct {
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	State string `json:"state"`
	URL   string `json:"url,omitempty"`
	// Origin is deliberately conservative: an unclassified provider status is
	// not evidence that an independent CI system has run.
	Origin string `json:"origin,omitempty"`
}

type ProviderCheckObservation struct {
	Provider   Provider        `json:"provider"`
	Repository string          `json:"repository"`
	HeadSHA    string          `json:"head_sha"`
	State      string          `json:"state"`
	ObservedAt *time.Time      `json:"observed_at,omitempty"`
	Checks     []ProviderCheck `json:"checks"`
	Truncated  bool            `json:"truncated"`
	Stale      bool            `json:"stale"`
	ErrorCode  string          `json:"error_code,omitempty"`
}

type ProviderCheckTarget struct {
	RunID    uuid.UUID
	Job      ReviewJob
	WorkerID string
	Attempt  int
}
