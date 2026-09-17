package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Provider string

const (
	ProviderGitHub Provider = "github"
	ProviderGitLab Provider = "gitlab"
)

func (p Provider) Valid() bool {
	return p == ProviderGitHub || p == ProviderGitLab
}

type JobState string

const (
	JobQueued    JobState = "queued"
	JobRunning   JobState = "running"
	JobSucceeded JobState = "succeeded"
	JobFailed    JobState = "failed"
	JobCancelled JobState = "cancelled"
)

type Installation struct {
	ID              uuid.UUID
	TenantID        uuid.UUID
	Provider        Provider
	ExternalID      string
	RepositoryScope string
	APIBaseURL      string
	CredentialRef   string
	Active          bool
}

type Tenant struct {
	ID        uuid.UUID `json:"id"`
	Slug      string    `json:"slug"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type Membership struct {
	TenantID uuid.UUID `json:"tenant_id"`
	Subject  string    `json:"subject"`
	Role     string    `json:"role"`
}

func ValidRole(role string) bool {
	switch role {
	case "owner", "admin", "reviewer", "viewer":
		return true
	default:
		return false
	}
}

type InstallationInput struct {
	Provider        Provider `json:"provider"`
	ExternalID      string   `json:"external_id"`
	RepositoryScope string   `json:"repository_scope"`
	APIBaseURL      string   `json:"api_base_url"`
	CredentialRef   string   `json:"credential_ref"`
}

// RuleSetInput creates a rule set together with its first mutable draft
// version. Rules are kept as JSON at this boundary so provider-neutral API
// contracts do not leak a particular OCR SDK type.
type RuleSetInput struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Rules       json.RawMessage `json:"rules"`
}

type RuleSet struct {
	ID          uuid.UUID `json:"id"`
	TenantID    uuid.UUID `json:"tenant_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type RuleVersion struct {
	ID            uuid.UUID       `json:"id"`
	RuleSetID     uuid.UUID       `json:"rule_set_id"`
	Version       int             `json:"version"`
	Revision      int             `json:"revision"`
	State         string          `json:"state"`
	Rules         json.RawMessage `json:"rules"`
	ContentSHA256 string          `json:"content_sha256"`
	CreatedBy     string          `json:"created_by"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

type RuleSetWithDraft struct {
	RuleSet RuleSet     `json:"rule_set"`
	Draft   RuleVersion `json:"draft"`
}

// RuleBindingInput scopes one immutable published rule version to a tenant or
// a repository. The control plane resolves these bindings only at admission;
// they never read mutable provider content from the pull request head.
type RuleBindingInput struct {
	RuleVersionID    uuid.UUID `json:"rule_version_id"`
	ScopeKind        string    `json:"scope_kind"`
	ScopeRef         string    `json:"scope_ref"`
	Precedence       int       `json:"precedence"`
	TargetBranchGlob string    `json:"target_branch_glob,omitempty"`
	PathIncludeGlob  string    `json:"path_include_glob,omitempty"`
	PathExcludeGlob  string    `json:"path_exclude_glob,omitempty"`
	State            string    `json:"state"`
}

type RuleBinding struct {
	ID               uuid.UUID `json:"id"`
	TenantID         uuid.UUID `json:"tenant_id"`
	RuleVersionID    uuid.UUID `json:"rule_version_id"`
	ScopeKind        string    `json:"scope_kind"`
	ScopeRef         string    `json:"scope_ref"`
	Precedence       int       `json:"precedence"`
	TargetBranchGlob string    `json:"target_branch_glob,omitempty"`
	PathIncludeGlob  string    `json:"path_include_glob,omitempty"`
	PathExcludeGlob  string    `json:"path_exclude_glob,omitempty"`
	State            string    `json:"state"`
	CreatedBy        string    `json:"created_by"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type InboundEvent struct {
	Provider               Provider
	APIBaseURL             string
	DeliveryID             string
	EventName              string
	InstallationExternalID string
	Repository             string
	CloneURL               string
	ReviewNumber           int
	BaseRef                string
	BaseSHA                string
	HeadRef                string
	HeadSHA                string
	Payload                json.RawMessage
	ReceivedAt             time.Time
}

// CommentEvent carries only the identifiers and raw body required to safely
// parse an explicit review command. Provider credentials are never embedded.
type CommentEvent struct {
	Provider               Provider
	APIBaseURL             string
	DeliveryID             string
	InstallationExternalID string
	Repository             string
	ReviewNumber           int
	CommentExternalID      string
	ActorExternalID        string
	Body                   string
}

type ProviderIdentity struct {
	TenantID   uuid.UUID `json:"tenant_id"`
	Provider   Provider  `json:"provider"`
	ExternalID string    `json:"external_id"`
	Subject    string    `json:"subject"`
}

type InteractionCommand struct {
	Event      CommentEvent
	Command    string
	Mode       string
	Normalized string
}

type InteractionOutcome struct {
	Accepted  bool
	Duplicate bool
	Reason    string
	RunID     *uuid.UUID
}

// InteractionReaction is a provider-neutral intent to acknowledge a command
// at its source. Providers deliberately map only reactions they can make
// idempotently; a missing reaction never changes the review command itself.
type InteractionReaction string

const (
	InteractionReactionNone     InteractionReaction = ""
	InteractionReactionEyes     InteractionReaction = "eyes"
	InteractionReactionConfused InteractionReaction = "confused"
)

func (r InteractionReaction) Valid() bool {
	return r == InteractionReactionNone || r == InteractionReactionEyes || r == InteractionReactionConfused
}

// InteractionResponse is the durable, non-secret payload consumed after an
// @openreview command has been authorized and committed. Credentials are
// resolved by the responder only at publication time.
type InteractionResponse struct {
	Provider               Provider
	APIBaseURL             string
	InstallationExternalID string
	CredentialRef          string
	Repository             string
	ReviewNumber           int
	CommentExternalID      string
	Reaction               InteractionReaction
	// ReleaseRunID is set only for a newly created command-triggered run. The
	// interaction responder releases it after the acknowledgement is visible.
	ReleaseRunID *uuid.UUID
	Body         string
	Marker       string
}

type ReviewJob struct {
	ID                     uuid.UUID
	TenantID               uuid.UUID
	InstallationID         uuid.UUID
	InstallationExternalID string
	CredentialRef          string
	DeliveryID             uuid.UUID
	Provider               Provider
	APIBaseURL             string
	Repository             string
	CloneURL               string
	ReviewNumber           int
	BaseRef                string
	BaseSHA                string
	HeadRef                string
	HeadSHA                string
	State                  JobState
	Attempts               int
	LockedBy               string
	LockedUntil            *time.Time
	ErrorMessage           string
	CreatedAt              time.Time
	StartedAt              *time.Time
	FinishedAt             *time.Time
}

type Finding struct {
	Path       string `json:"path"`
	StartLine  int    `json:"start_line"`
	EndLine    int    `json:"end_line"`
	Severity   string `json:"severity"`
	Category   string `json:"category"`
	Body       string `json:"body"`
	Suggestion string `json:"suggestion,omitempty"`
}
