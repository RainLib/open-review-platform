package domain

import (
	"time"

	"github.com/google/uuid"
)

type RuleExceptionInput struct {
	RuleVersionID       uuid.UUID  `json:"rule_version_id"`
	RuleKey             string     `json:"rule_key"`
	ScopeKind           string     `json:"scope_kind"`
	ScopeRef            string     `json:"scope_ref"`
	ScopeProvider       Provider   `json:"scope_provider,omitempty"`
	ScopeAPIBaseURL     string     `json:"scope_api_base_url,omitempty"`
	TargetBranchGlob    string     `json:"target_branch_glob,omitempty"`
	Reason              string     `json:"reason"`
	TicketURL           string     `json:"ticket_url,omitempty"`
	ExpiresAt           time.Time  `json:"expires_at"`
	SourceIssueID       *uuid.UUID `json:"source_issue_id,omitempty"`
	SourceIssueRevision int        `json:"source_issue_revision,omitempty"`
}

type RuleExceptionDecisionInput struct {
	Decision string `json:"decision"`
	Comment  string `json:"comment,omitempty"`
}

type RuleException struct {
	ID                  uuid.UUID  `json:"id"`
	TenantID            uuid.UUID  `json:"tenant_id"`
	RuleSetID           uuid.UUID  `json:"rule_set_id"`
	RuleSetName         string     `json:"rule_set_name"`
	RuleVersionID       uuid.UUID  `json:"rule_version_id"`
	RuleVersion         int        `json:"rule_version"`
	RuleKey             string     `json:"rule_key"`
	ScopeKind           string     `json:"scope_kind"`
	ScopeRef            string     `json:"scope_ref"`
	ScopeProvider       Provider   `json:"scope_provider,omitempty"`
	ScopeAPIBaseURL     string     `json:"scope_api_base_url,omitempty"`
	TargetBranchGlob    string     `json:"target_branch_glob,omitempty"`
	Reason              string     `json:"reason"`
	TicketURL           string     `json:"ticket_url,omitempty"`
	RequestedBy         string     `json:"requested_by"`
	ApprovedBy          string     `json:"approved_by,omitempty"`
	DecisionComment     string     `json:"decision_comment,omitempty"`
	State               string     `json:"state"`
	EffectiveState      string     `json:"effective_state"`
	CanDecide           bool       `json:"can_decide"`
	CanRevoke           bool       `json:"can_revoke"`
	ExpiresAt           time.Time  `json:"expires_at"`
	DecidedAt           *time.Time `json:"decided_at,omitempty"`
	RevokedAt           *time.Time `json:"revoked_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
	SourceIssueID       *uuid.UUID `json:"source_issue_id,omitempty"`
	SourceIssueRevision *int       `json:"source_issue_revision,omitempty"`
}

type RuleSnapshotException struct {
	ExceptionID   uuid.UUID `json:"exception_id"`
	RuleVersionID uuid.UUID `json:"rule_version_id"`
	RuleKey       string    `json:"rule_key"`
	ExpiresAt     time.Time `json:"expires_at"`
}
