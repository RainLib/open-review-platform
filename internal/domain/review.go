package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var (
	// ErrReviewTimedOut marks an execution budget being exhausted. It is
	// distinct from transport failures: retrying the identical request simply
	// recreates the same wait and delays a visible, actionable result.
	ErrReviewTimedOut = errors.New("review execution timed out")
	// ErrReviewContextExhausted marks deterministic model-context exhaustion.
	// Replaying the same commit, rule snapshot, and selected scope would
	// consume the same context again, so it must converge as a terminal run.
	ErrReviewContextExhausted = errors.New("review model context exhausted")
)

type Provider string

const (
	ProviderGitHub Provider = "github"
	ProviderGitLab Provider = "gitlab"

	// AllAuthorizedRepositoriesScope represents every repository granted to one
	// GitHub App installation. It is intentionally not accepted for GitLab:
	// GitLab inbound routing is scope-based and must remain explicit to avoid
	// cross-tenant webhook ambiguity on a shared deployment profile.
	AllAuthorizedRepositoriesScope = "*/*"
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
	ID                uuid.UUID                     `json:"id"`
	TenantID          uuid.UUID                     `json:"tenant_id"`
	Provider          Provider                      `json:"provider"`
	ExternalID        string                        `json:"external_id"`
	RepositoryScope   string                        `json:"repository_scope"`
	AutomaticReviews  bool                          `json:"automatic_reviews"`
	AuthorScope       string                        `json:"author_scope"`
	AuthorExternalID  string                        `json:"-"`
	MinimumSeverity   string                        `json:"minimum_severity"`
	APIBaseURL        string                        `json:"api_base_url"`
	CredentialRef     string                        `json:"-"`
	Active            bool                          `json:"active"`
	VerificationState InstallationVerificationState `json:"verification_state"`
}

// InstallationSummary is safe to return to an authenticated management
// console. Credential references stay internal to worker-side execution.
type InstallationSummary struct {
	ID                uuid.UUID                     `json:"id"`
	Provider          Provider                      `json:"provider"`
	ExternalID        string                        `json:"external_id"`
	RepositoryScope   string                        `json:"repository_scope"`
	AutomaticReviews  bool                          `json:"automatic_reviews"`
	AuthorScope       string                        `json:"author_scope"`
	MinimumSeverity   string                        `json:"minimum_severity"`
	APIBaseURL        string                        `json:"api_base_url"`
	Active            bool                          `json:"active"`
	VerificationState InstallationVerificationState `json:"verification_state"`
	Verification      *ProviderVerificationReceipt  `json:"verification,omitempty"`
}

// ProviderVerificationReceipt is the tenant-safe outcome of the read-only
// onboarding probe. It deliberately excludes raw provider responses,
// credentials, and response bodies while giving setup a durable state,
// inventory proof, and recoverable error classification.
type ProviderVerificationReceipt struct {
	State          string      `json:"state"`
	HealthState    HealthState `json:"health_state"`
	Attempt        int         `json:"attempt"`
	ObservedAt     *time.Time  `json:"observed_at,omitempty"`
	Permissions    []string    `json:"permissions"`
	InventoryCount int         `json:"inventory_count"`
	InventoryState string      `json:"inventory_state,omitempty"`
	ErrorCode      string      `json:"error_code,omitempty"`
}

// ProviderRepository is a read-only inventory record produced by the
// provider-prober. It intentionally contains only repository metadata that a
// workspace administrator can use to select review scope; provider tokens,
// installation credentials, and raw provider responses never enter this
// record.
type ProviderRepository struct {
	ExternalID    string    `json:"external_id"`
	Name          string    `json:"name"`
	DefaultBranch string    `json:"default_branch,omitempty"`
	Visibility    string    `json:"visibility,omitempty"`
	Archived      bool      `json:"archived"`
	LastSeenAt    time.Time `json:"last_seen_at"`
}

// ProviderProfile is deployment-owned connection metadata safe for a workspace
// administrator to inspect during setup. It carries no credential reference,
// private key, token, webhook secret, or arbitrary browser-selected endpoint.
type ProviderProfile struct {
	Provider                 Provider `json:"provider"`
	APIBaseURL               string   `json:"api_base_url"`
	Mode                     string   `json:"mode"`
	Label                    string   `json:"label"`
	DeploymentTokenAvailable bool     `json:"deployment_token_available"`
}

// InstallationWebhookReceipt is the tenant-safe, durable trace of a webhook
// that Open Review accepted into a review job. It intentionally omits raw
// payloads, provider delivery identifiers, headers, and signatures.
type InstallationWebhookReceipt struct {
	ID           uuid.UUID  `json:"id"`
	EventName    string     `json:"event_name"`
	ReceivedAt   time.Time  `json:"received_at"`
	ResourceKind string     `json:"resource_kind"`
	Action       string     `json:"action,omitempty"`
	Revision     int        `json:"revision,omitempty"`
	JobID        uuid.UUID  `json:"job_id"`
	Repository   string     `json:"repository"`
	ReviewNumber int        `json:"review_number"`
	JobState     JobState   `json:"job_state"`
	RunID        *uuid.UUID `json:"run_id,omitempty"`
	RunState     *RunState  `json:"run_state,omitempty"`
	TriggerKind  string     `json:"trigger_kind,omitempty"`
}

// InstallationVerificationState separates an operator-recorded connection
// from the read-only provider proof required before new webhooks can admit a
// review. Legacy records remain explicitly compatible without being silently
// rewritten as a fresh probe result.
type InstallationVerificationState string

const (
	InstallationVerificationLegacy   InstallationVerificationState = "legacy"
	InstallationVerificationPending  InstallationVerificationState = "pending"
	InstallationVerificationChecking InstallationVerificationState = "checking"
	InstallationVerificationVerified InstallationVerificationState = "verified"
	InstallationVerificationFailed   InstallationVerificationState = "failed"
)

func (state InstallationVerificationState) EligibleForReview() bool {
	return state == InstallationVerificationLegacy || state == InstallationVerificationVerified
}

type Tenant struct {
	ID        uuid.UUID `json:"id"`
	Slug      string    `json:"slug"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// TenantSummary is the tenant data that an authenticated member may use for
// workspace navigation. It intentionally contains no membership subjects or
// provider credentials.
type TenantSummary struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
	Role string `json:"role"`
}

type Membership struct {
	TenantID      uuid.UUID  `json:"tenant_id"`
	Subject       string     `json:"subject"`
	Role          string     `json:"role"`
	Active        bool       `json:"active"`
	DeactivatedAt *time.Time `json:"deactivated_at,omitempty"`
	DeactivatedBy string     `json:"deactivated_by,omitempty"`
}

type WorkspaceInvitation struct {
	ID         uuid.UUID  `json:"id"`
	TenantID   uuid.UUID  `json:"tenant_id"`
	Subject    string     `json:"subject"`
	Role       string     `json:"role"`
	Status     string     `json:"status"`
	CreatedBy  string     `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	AcceptedAt *time.Time `json:"accepted_at,omitempty"`
	AcceptedBy string     `json:"accepted_by,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	RevokedBy  string     `json:"revoked_by,omitempty"`
}

// WorkspaceInvitationCreation returns the one-time acceptance token exactly
// once. Stores persist only its SHA-256 hash and never expose it from list or
// audit read models.
type WorkspaceInvitationCreation struct {
	Invitation WorkspaceInvitation `json:"invitation"`
	Token      string              `json:"token"`
}

type WorkspaceInvitationInput struct {
	Subject        string `json:"subject"`
	Role           string `json:"role"`
	ExpiresInHours int    `json:"expires_in_hours"`
}

// WorkspaceAccessRequest is visible only to workspace owners and admins.
// A requester's submission response never confirms that a slug exists.
type WorkspaceAccessRequest struct {
	ID        uuid.UUID  `json:"id"`
	TenantID  uuid.UUID  `json:"tenant_id"`
	Subject   string     `json:"subject"`
	Note      string     `json:"note"`
	Status    string     `json:"status"`
	Revision  int        `json:"revision"`
	CreatedAt time.Time  `json:"created_at"`
	DecidedAt *time.Time `json:"decided_at,omitempty"`
	DecidedBy string     `json:"decided_by,omitempty"`
}

func ValidInviteRole(role string) bool {
	switch role {
	case "admin", "rule_admin", "reviewer", "viewer", "billing_viewer":
		return true
	default:
		return false
	}
}

func ValidRole(role string) bool {
	switch role {
	case "owner", "admin", "rule_admin", "reviewer", "viewer", "billing_viewer":
		return true
	default:
		return false
	}
}

type InstallationInput struct {
	Provider         Provider `json:"provider"`
	ExternalID       string   `json:"external_id"`
	RepositoryScope  string   `json:"repository_scope"`
	AutomaticReviews *bool    `json:"automatic_reviews,omitempty"`
	AuthorScope      string   `json:"author_scope,omitempty"`
	AuthorExternalID string   `json:"author_external_id,omitempty"`
	MinimumSeverity  string   `json:"minimum_severity"`
	APIBaseURL       string   `json:"api_base_url"`
	CredentialRef    string   `json:"credential_ref"`
}

// ProviderOAuthCredentialInput is worker-only encrypted material. The
// browser never receives this type and the control plane never serializes it
// in responses, logs, outbox rows, or audit metadata.
type ProviderOAuthCredentialInput struct {
	CredentialRef          string
	Provider               Provider
	AccessTokenCiphertext  []byte
	RefreshTokenCiphertext []byte
	ExpiresAt              *time.Time
}

type ProviderOAuthCredential struct {
	CredentialRef          string
	TenantID               uuid.UUID
	Provider               Provider
	AccessTokenCiphertext  []byte
	RefreshTokenCiphertext []byte
	ExpiresAt              *time.Time
	RevokedAt              *time.Time
}

// ProviderOAuthCredentialRefresh is a compare-and-set replacement prepared by
// a provider-calling worker. It contains only encrypted material; the store
// must never receive an OAuth bearer token in plaintext.
type ProviderOAuthCredentialRefresh struct {
	ExpectedAccessTokenCiphertext []byte
	AccessTokenCiphertext         []byte
	RefreshTokenCiphertext        []byte
	ExpiresAt                     time.Time
}

// InstallationRepositoryScopeInput is a compare-and-set update for an
// installation's admission boundary. Credentials and provider identity are
// intentionally immutable through this management endpoint.
type InstallationRepositoryScopeInput struct {
	RepositoryScope         string `json:"repository_scope"`
	ExpectedRepositoryScope string `json:"expected_repository_scope"`
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
	ID            uuid.UUID              `json:"id"`
	TenantID      uuid.UUID              `json:"tenant_id"`
	Name          string                 `json:"name"`
	Description   string                 `json:"description"`
	Catalog       *RuleCatalogProvenance `json:"catalog,omitempty"`
	CreatedBy     string                 `json:"created_by"`
	CreatedAt     time.Time              `json:"created_at"`
	UpdatedAt     time.Time              `json:"updated_at"`
	LatestVersion *RuleVersionSummary    `json:"latest_version,omitempty"`
}

// RuleCatalogProvenance records the immutable release template from which a
// governed draft was installed. It is not policy authority by itself: the
// resulting version must still take the normal approval and binding path.
type RuleCatalogProvenance struct {
	ID            string `json:"id"`
	Version       string `json:"version"`
	ContentSHA256 string `json:"content_sha256"`
	Origin        string `json:"origin"`
}

// RuleCatalogEntry is the safe browser-facing projection of one release-pinned
// policy template. Rule prompt bodies remain server-side until installation.
type RuleCatalogEntry struct {
	ID            string                   `json:"id"`
	Version       string                   `json:"version"`
	Title         string                   `json:"title"`
	Description   string                   `json:"description"`
	Tags          []string                 `json:"tags"`
	RuleCount     int                      `json:"rule_count"`
	ContentSHA256 string                   `json:"content_sha256"`
	Origin        string                   `json:"origin"`
	CanInstall    bool                     `json:"can_install"`
	Installation  *RuleCatalogInstallation `json:"installation,omitempty"`
}

type RuleCatalogInstallation struct {
	RuleSetID        uuid.UUID `json:"rule_set_id"`
	RuleSetName      string    `json:"rule_set_name"`
	RuleVersion      int       `json:"rule_version"`
	RuleVersionState string    `json:"rule_version_state"`
}

type RuleCatalogInstallInput struct {
	Version       string `json:"version"`
	ContentSHA256 string `json:"content_sha256"`
}

type RuleCatalogInstallResult struct {
	RuleSet  RuleSet     `json:"rule_set"`
	Draft    RuleVersion `json:"draft"`
	Replayed bool        `json:"replayed"`
}

type RuleVersionSummary struct {
	ID            uuid.UUID `json:"id"`
	Version       int       `json:"version"`
	Revision      int       `json:"revision"`
	State         string    `json:"state"`
	ContentSHA256 string    `json:"content_sha256"`
	CreatedBy     string    `json:"created_by"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
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

type RuleApprovalRequestInput struct {
	RequiredApprovals int `json:"required_approvals"`
}

type RuleApprovalDecisionInput struct {
	Decision string `json:"decision"`
	Comment  string `json:"comment,omitempty"`
}

// RuleApprovalRequest binds reviewer decisions to one exact, immutable rule
// version payload. A future version is a different approval subject even when
// its rule-set name stays the same.
type RuleApprovalRequest struct {
	ID                uuid.UUID  `json:"id"`
	TenantID          uuid.UUID  `json:"tenant_id"`
	RuleVersionID     uuid.UUID  `json:"rule_version_id"`
	ContentSHA256     string     `json:"content_sha256"`
	RequestedBy       string     `json:"requested_by"`
	RequiredApprovals int        `json:"required_approvals"`
	State             string     `json:"state"`
	CreatedAt         time.Time  `json:"created_at"`
	DecidedAt         *time.Time `json:"decided_at,omitempty"`
}

// RuleApprovalSummary is the governance read model used by the control plane.
// It keeps the immutable approval subject visible while exposing only the
// actor-specific capabilities needed to render safe actions.
type RuleApprovalSummary struct {
	ID                uuid.UUID  `json:"id"`
	RuleSetID         uuid.UUID  `json:"rule_set_id"`
	RuleSetName       string     `json:"rule_set_name"`
	RuleVersionID     uuid.UUID  `json:"rule_version_id"`
	Version           int        `json:"version"`
	VersionState      string     `json:"version_state"`
	ContentSHA256     string     `json:"content_sha256"`
	RequestedBy       string     `json:"requested_by"`
	RequiredApprovals int        `json:"required_approvals"`
	ApprovalCount     int        `json:"approval_count"`
	RejectionCount    int        `json:"rejection_count"`
	ActorDecision     string     `json:"actor_decision,omitempty"`
	CanDecide         bool       `json:"can_decide"`
	CanPublish        bool       `json:"can_publish"`
	State             string     `json:"state"`
	CreatedAt         time.Time  `json:"created_at"`
	DecidedAt         *time.Time `json:"decided_at,omitempty"`
}

type RuleImpactPreviewInput struct {
	Repository   string   `json:"repository"`
	Provider     Provider `json:"provider"`
	APIBaseURL   string   `json:"api_base_url"`
	TargetBranch string   `json:"target_branch"`
	Precedence   int      `json:"precedence"`
}

type RuleImpactCounts struct {
	Total     int `json:"total"`
	Mandatory int `json:"mandatory"`
	Critical  int `json:"critical"`
	High      int `json:"high"`
	Medium    int `json:"medium"`
	Low       int `json:"low"`
}

type RuleImpactHistoricalSample struct {
	RunCount        int        `json:"run_count"`
	FindingCount    int        `json:"finding_count"`
	HighRiskFinding int        `json:"high_risk_finding_count"`
	OldestRunAt     *time.Time `json:"oldest_run_at,omitempty"`
	NewestRunAt     *time.Time `json:"newest_run_at,omitempty"`
}

// RuleImpactPreview is a read-only Test Lab result. HistoricalSample describes
// the available replay envelope; it never claims that OCR was executed.
type RuleImpactPreview struct {
	RuleSetID          uuid.UUID                  `json:"rule_set_id"`
	RuleSetName        string                     `json:"rule_set_name"`
	RuleVersionID      uuid.UUID                  `json:"rule_version_id"`
	Version            int                        `json:"version"`
	VersionState       string                     `json:"version_state"`
	ContentSHA256      string                     `json:"content_sha256"`
	Repository         string                     `json:"repository"`
	Provider           Provider                   `json:"provider"`
	APIBaseURL         string                     `json:"api_base_url"`
	TargetBranch       string                     `json:"target_branch"`
	Precedence         int                        `json:"precedence"`
	Valid              bool                       `json:"valid"`
	Conflict           string                     `json:"conflict,omitempty"`
	BaselineSHA256     string                     `json:"baseline_sha256"`
	CandidateSHA256    string                     `json:"candidate_sha256,omitempty"`
	BaselineRuleCount  int                        `json:"baseline_rule_count"`
	CandidateRuleCount int                        `json:"candidate_rule_count"`
	MatchedBindings    int                        `json:"matched_bindings"`
	AddedRuleKeys      []string                   `json:"added_rule_keys"`
	ChangedRuleKeys    []string                   `json:"changed_rule_keys"`
	RemovedRuleKeys    []string                   `json:"removed_rule_keys"`
	CandidateCounts    RuleImpactCounts           `json:"candidate_counts"`
	HistoricalSample   RuleImpactHistoricalSample `json:"historical_sample"`
	Uncertainty        []string                   `json:"uncertainty"`
}

// RuleBindingInput scopes one immutable published rule version to a tenant or
// a repository. The control plane resolves these bindings only at admission;
// they never read mutable provider content from the pull request head.
type RuleBindingInput struct {
	RuleVersionID    uuid.UUID `json:"rule_version_id"`
	ScopeKind        string    `json:"scope_kind"`
	ScopeRef         string    `json:"scope_ref"`
	ScopeProvider    Provider  `json:"scope_provider,omitempty"`
	ScopeAPIBaseURL  string    `json:"scope_api_base_url,omitempty"`
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
	ScopeProvider    Provider  `json:"scope_provider,omitempty"`
	ScopeAPIBaseURL  string    `json:"scope_api_base_url,omitempty"`
	Precedence       int       `json:"precedence"`
	TargetBranchGlob string    `json:"target_branch_glob,omitempty"`
	PathIncludeGlob  string    `json:"path_include_glob,omitempty"`
	PathExcludeGlob  string    `json:"path_exclude_glob,omitempty"`
	State            string    `json:"state"`
	CreatedBy        string    `json:"created_by"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// RuleBindingUpdateInput intentionally exposes only lifecycle state. Changing
// scope or precedence creates a new auditable binding instead of mutating the
// meaning of prior governance decisions.
type RuleBindingUpdateInput struct {
	State string `json:"state"`
}

// RuleSnapshot is the immutable, canonical rule resolution attached to a
// review run. It never contains provider credentials or pull-request content.
type RuleSnapshot struct {
	ID               uuid.UUID               `json:"id"`
	SHA256           string                  `json:"sha256"`
	CompilerVersion  string                  `json:"compiler_version"`
	Engine           string                  `json:"engine"`
	CanonicalPayload json.RawMessage         `json:"canonical_payload"`
	Sources          []RuleSnapshotSource    `json:"sources"`
	Exceptions       []RuleSnapshotException `json:"exceptions"`
	CreatedAt        time.Time               `json:"created_at"`
}

type RuleSnapshotSource struct {
	RuleVersionID uuid.UUID `json:"rule_version_id"`
	RuleSetID     uuid.UUID `json:"rule_set_id"`
	Version       int       `json:"version"`
	Precedence    int       `json:"precedence"`
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
	TriggerKind            string
	ActorKind              string
	ActorSubject           string
	ReviewMode             ReviewMode
	// RuleSetID is an optional command-selected published rule set. It is
	// resolved alongside normal bindings inside the admission transaction.
	RuleSetID *uuid.UUID
	// DeferAcknowledgement keeps an explicit comment-triggered review in the
	// acknowledged state until the marker-keyed user response is accepted by
	// the provider. It is set only by the interaction admission worker.
	DeferAcknowledgement bool
	// Action and review metadata are normalized from an already verified
	// provider webhook. They are only used by control-plane admission policy;
	// the raw provider payload remains the forensic record.
	Action  string
	IsDraft bool
	Title   string
	Author  string
	// AuthorExternalID is the provider's stable PR/MR author identity, not
	// the actor who happened to update the review. Empty means unverified.
	AuthorExternalID string
	Labels           []string
}

// CommentEvent carries only the identifiers and raw body required to safely
// parse an explicit review command. Provider credentials are never embedded.
type CommentEvent struct {
	Provider               Provider
	APIBaseURL             string
	DeliveryID             string
	InstallationExternalID string
	Repository             string
	CloneURL               string
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
	RuleSetID  string
	Target     string
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
	TenantID               uuid.UUID
	Provider               Provider
	APIBaseURL             string
	InstallationExternalID string
	CredentialRef          string
	Repository             string
	// ResourceKind is "merge_request" for the existing review command path
	// and "issue" for a governed Agent task request. It selects the correct
	// GitLab note endpoint; GitHub uses its shared Issue API for both.
	ResourceKind      string
	ReviewNumber      int
	CommentExternalID string
	Reaction          InteractionReaction
	// ReactionOnly acknowledges a review command on its source comment without
	// creating a separate progress comment. The accepted reaction remains the
	// durable provider barrier before admission or execution is released.
	ReactionOnly bool
	// ReleaseRunID is set only for a newly created command-triggered run. The
	// interaction responder releases it after the acknowledgement is visible.
	ReleaseRunID *uuid.UUID
	// SourceRelease is present only on the first provider acknowledgement for
	// a newly admitted Agent task. The source worker is queued after that
	// acknowledgement has been accepted, never concurrently with it.
	SourceRelease *AgentTaskSourceRelease
	// Admission is present only on the initial progress reply for a first
	// review command. The responder emits it to the admission queue only after
	// the provider has accepted that reply.
	Admission *InteractionAdmission
	Body      string
	Marker    string
	// StatusVersion is the monotone task revision for a mutable provider
	// status comment. Zero preserves legacy marker-only responses.
	StatusVersion int
	// MarkerSince is the durable interaction creation time. Provider comment
	// markers for this interaction can never predate it, so publishers can
	// safely avoid scanning an entire long-lived pull-request discussion on
	// every acknowledgement retry.
	MarkerSince time.Time
}

type AgentTaskSourceRelease struct {
	TaskID   uuid.UUID
	Revision int
}

// InteractionAdmission is the minimal, durable command context carried from
// a successfully published progress reply to the provider-read admission
// worker. It deliberately excludes provider tokens and raw webhook bodies.
type InteractionAdmission struct {
	InteractionID uuid.UUID
	Event         CommentEvent
	Mode          ReviewMode
	// RuleSetID is optional. When present, admission may only compile that
	// already active, published rule set for the provider-resolved target.
	RuleSetID     *uuid.UUID
	ActorSubject  string
	CredentialRef string
}

type ReviewJob struct {
	ID       uuid.UUID
	TenantID uuid.UUID
	// TenantSlug is the non-secret Console route identity resolved with the
	// job. Provider reports use it only to link an authenticated operator back
	// to the exact run; it is never used for tenancy authorization.
	TenantSlug             string
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
	// Source evidence is captured only from the exact admitted head by the
	// review worker. It is bounded and governed with the finding, not fetched
	// with a provider credential by the Console.
	CodeExcerpt          string `json:"code_excerpt,omitempty"`
	CodeExcerptStartLine int    `json:"code_excerpt_start_line,omitempty"`
	ProposedPatch        string `json:"proposed_patch,omitempty"`
	// SuggestionCode is transient OCR output used to build a checked patch.
	// It is intentionally not persisted as an independently trusted diff.
	SuggestionCode string                 `json:"-"`
	RuleReferences []FindingRuleReference `json:"rule_references,omitempty"`
}

// FindingRuleReference is a model-supplied claim about which immutable rule
// caused a finding. The store accepts it only after matching both fields to the
// exact rule snapshot attached to the run; it must never be inferred from a
// category, severity, or current mutable rule bindings.
type FindingRuleReference struct {
	RuleKey       string `json:"rule_key"`
	SourceVersion string `json:"source_version"`
}

func FindingMarker(jobID uuid.UUID, finding Finding) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d|%d|%s|%s", jobID, finding.Path, finding.StartLine, finding.EndLine, finding.Category, finding.Body)))
	return "open-review-platform:finding:" + hex.EncodeToString(digest[:12])
}

type FindingReaction struct {
	Provider           Provider `json:"provider"`
	DeliveryID         string   `json:"delivery_id"`
	ReactionExternalID string   `json:"reaction_external_id"`
	ActorExternalID    string   `json:"actor_external_id"`
	Repository         string   `json:"repository"`
	FindingMarker      string   `json:"finding_marker"`
	Kind               string   `json:"kind"`
	Action             string   `json:"action"`
}

type FindingFeedbackMetric struct {
	Repository            string `json:"repository"`
	FindingCount          int    `json:"finding_count"`
	FeedbackCount         int    `json:"feedback_count"`
	UsefulFindingCount    int    `json:"useful_finding_count"`
	FalsePositiveCount    int    `json:"false_positive_count"`
	ResolvedFindingCount  int    `json:"resolved_finding_count"`
	WontFixFindingCount   int    `json:"wont_fix_finding_count"`
	HighRiskFindingCount  int    `json:"high_risk_finding_count"`
	DistinctSnapshotCount int    `json:"distinct_snapshot_count"`
}

type FindingFeedbackDashboard struct {
	FindingCount         int                     `json:"finding_count"`
	FeedbackCount        int                     `json:"feedback_count"`
	UsefulFindingCount   int                     `json:"useful_finding_count"`
	FalsePositiveCount   int                     `json:"false_positive_count"`
	ResolvedFindingCount int                     `json:"resolved_finding_count"`
	WontFixFindingCount  int                     `json:"wont_fix_finding_count"`
	Repositories         []FindingFeedbackMetric `json:"repositories"`
	RecentFindings       []FindingFeedbackItem   `json:"recent_findings"`
	AttributionWarning   string                  `json:"attribution_warning"`
}

type FindingFeedbackItem struct {
	ID                 uuid.UUID  `json:"id"`
	RunID              *uuid.UUID `json:"run_id,omitempty"`
	Provider           Provider   `json:"provider"`
	APIBaseURL         string     `json:"api_base_url"`
	Repository         string     `json:"repository"`
	ReviewNumber       int        `json:"review_number"`
	HeadSHA            string     `json:"head_sha,omitempty"`
	Path               string     `json:"path"`
	StartLine          int        `json:"start_line"`
	EndLine            int        `json:"end_line"`
	Severity           string     `json:"severity"`
	Category           string     `json:"category"`
	BodyPreview        string     `json:"body_preview"`
	ActorDisposition   string     `json:"actor_disposition,omitempty"`
	UsefulCount        int        `json:"useful_count"`
	FalsePositiveCount int        `json:"false_positive_count"`
	CreatedAt          time.Time  `json:"created_at"`
}

type FindingDispositionInput struct {
	Kind string `json:"kind"`
}
