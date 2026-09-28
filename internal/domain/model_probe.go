package domain

import (
	"time"

	"github.com/google/uuid"
)

// ModelProbeState describes an explicitly requested, minimal connectivity
// check. It is deliberately separate from review execution: a saved route
// never sends a provider request until an operator asks for a probe.
type ModelProbeState string

const (
	ModelProbeQueued    ModelProbeState = "queued"
	ModelProbeRunning   ModelProbeState = "running"
	ModelProbeSucceeded ModelProbeState = "succeeded"
	ModelProbeFailed    ModelProbeState = "failed"
)

// ModelProbeReceipt is safe to return to the control plane. The complete
// route, including its opaque credential reference, is retained only in the
// worker target and is never serialized into this receipt.
type ModelProbeReceipt struct {
	ID              uuid.UUID       `json:"id"`
	ScopeKind       string          `json:"scope_kind"`
	ScopeRef        string          `json:"scope_ref,omitempty"`
	ScopeProvider   Provider        `json:"scope_provider,omitempty"`
	ScopeAPIBaseURL string          `json:"scope_api_base_url,omitempty"`
	ConfigRevision  int             `json:"config_revision"`
	ContentSHA256   string          `json:"content_sha256"`
	Provider        string          `json:"provider"`
	Protocol        string          `json:"protocol"`
	Model           string          `json:"model"`
	EndpointHost    string          `json:"endpoint_host"`
	State           ModelProbeState `json:"state"`
	Attempt         int             `json:"attempt"`
	ResponseSHA256  string          `json:"response_sha256,omitempty"`
	LatencyMS       int64           `json:"latency_ms,omitempty"`
	ErrorCode       string          `json:"error_code,omitempty"`
	ErrorMessage    string          `json:"error_message,omitempty"`
	RequestedBy     string          `json:"requested_by"`
	CreatedAt       time.Time       `json:"created_at"`
	StartedAt       *time.Time      `json:"started_at,omitempty"`
	FinishedAt      *time.Time      `json:"finished_at,omitempty"`
}

type ModelProbeInput struct {
	ScopeKind        ReviewConfigScopeKind `json:"scope_kind"`
	ScopeRef         string                `json:"scope_ref,omitempty"`
	ScopeProvider    Provider              `json:"scope_provider,omitempty"`
	ScopeAPIBaseURL  string                `json:"scope_api_base_url,omitempty"`
	ExpectedRevision int                   `json:"expected_revision"`
	Acknowledged     bool                  `json:"acknowledged"`
}

// ModelProbeTarget is worker-only. Route contains an opaque credential
// reference, never a credential value; the worker resolves it in memory only.
type ModelProbeTarget struct {
	ReceiptID uuid.UUID
	Route     ModelRouteConfig
}

type ModelProbeResult struct {
	ResponseSHA256 string
	LatencyMS      int64
	ErrorCode      string
	ErrorMessage   string
}
