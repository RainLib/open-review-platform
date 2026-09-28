package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

const (
	ReviewConfigChangePending    = "pending"
	ReviewConfigChangeApproved   = "approved"
	ReviewConfigChangeRejected   = "rejected"
	ReviewConfigChangeSuperseded = "superseded"
)

const (
	ReviewConfigChangeUpsert             = "upsert"
	ReviewConfigChangeRestoreInheritance = "restore_inheritance"
)

// ReviewConfigChangeRequest carries a proposed, hash-bound configuration
// change. Proposed content is never resolved as an active configuration until
// an independent administrator commits an approval.
type ReviewConfigChangeRequest struct {
	ID                    uuid.UUID             `json:"id"`
	TenantID              uuid.UUID             `json:"tenant_id,omitempty"`
	Section               ReviewConfigSection   `json:"section"`
	ScopeKind             ReviewConfigScopeKind `json:"scope_kind"`
	ScopeRef              string                `json:"scope_ref,omitempty"`
	ScopeProvider         Provider              `json:"scope_provider,omitempty"`
	ScopeAPIBaseURL       string                `json:"scope_api_base_url,omitempty"`
	BaseRevision          int                   `json:"base_revision"`
	BaseContentSHA256     string                `json:"base_content_sha256"`
	ProposedContent       json.RawMessage       `json:"proposed_content"`
	ProposedContentSHA256 string                `json:"proposed_content_sha256"`
	RequestedBy           string                `json:"requested_by"`
	Reason                string                `json:"reason"`
	Operation             string                `json:"operation"`
	State                 string                `json:"state"`
	ApprovalCount         int                   `json:"approval_count"`
	RejectionCount        int                   `json:"rejection_count"`
	ActorDecision         string                `json:"actor_decision,omitempty"`
	CanDecide             bool                  `json:"can_decide"`
	AppliedRevision       int                   `json:"applied_revision,omitempty"`
	CreatedAt             time.Time             `json:"created_at"`
	DecidedAt             *time.Time            `json:"decided_at,omitempty"`
}

type ReviewConfigChangeRequestInput struct {
	Section          ReviewConfigSection   `json:"section"`
	ScopeKind        ReviewConfigScopeKind `json:"scope_kind"`
	ScopeRef         string                `json:"scope_ref,omitempty"`
	ScopeProvider    Provider              `json:"scope_provider,omitempty"`
	ScopeAPIBaseURL  string                `json:"scope_api_base_url,omitempty"`
	ExpectedRevision int                   `json:"expected_revision"`
	Content          json.RawMessage       `json:"content"`
	Reason           string                `json:"reason"`
	Operation        string                `json:"operation,omitempty"`
}

type ReviewConfigChangeDecisionInput struct {
	Decision string `json:"decision"`
	Comment  string `json:"comment"`
}
