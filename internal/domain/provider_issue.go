package domain

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ProviderIssueEvent is the bounded, provider-neutral input accepted from an
// Issue webhook. Raw payloads remain in the webhook ledger; workers receive
// only the fields required to triage one user-authored Issue.
type ProviderIssueEvent struct {
	Provider               Provider
	APIBaseURL             string
	DeliveryID             string
	EventName              string
	InstallationExternalID string
	Repository             string
	IssueNumber            int
	Action                 string
	Title                  string
	Body                   string
	Author                 string
	AuthorType             string
	Labels                 []string
	Payload                json.RawMessage
	ReceivedAt             time.Time
}

func (event ProviderIssueEvent) Valid() bool {
	return event.Provider.Valid() && strings.TrimSpace(event.APIBaseURL) != "" &&
		strings.TrimSpace(event.DeliveryID) != "" && strings.TrimSpace(event.EventName) != "" &&
		strings.TrimSpace(event.InstallationExternalID) != "" && strings.TrimSpace(event.Repository) != "" &&
		event.IssueNumber > 0 && strings.TrimSpace(event.Title) != "" && event.ReceivedAt.IsZero() == false
}

type ProviderIssueAnalysisState string

const (
	ProviderIssueAnalysisQueued       ProviderIssueAnalysisState = "queued"
	ProviderIssueAnalysisAcknowledged ProviderIssueAnalysisState = "acknowledged"
	ProviderIssueAnalysisCompleted    ProviderIssueAnalysisState = "completed"
	ProviderIssueAnalysisFailed       ProviderIssueAnalysisState = "failed"
	// NeedsAttention is a list filter, never a persisted job state. It includes
	// failed jobs and pending jobs whose connection is blocked or whose current
	// delivery is exhausted.
	ProviderIssueAnalysisNeedsAttention ProviderIssueAnalysisState = "needs_attention"
)

type ProviderIssueAnalysisJob struct {
	ID                      uuid.UUID
	TenantID                uuid.UUID
	TenantSlug              string
	InstallationID          uuid.UUID
	InstallationExternalID  string
	CredentialRef           string
	Provider                Provider
	APIBaseURL              string
	Repository              string
	IssueNumber             int
	Revision                int
	AnalysisAttempt         int
	Action                  string
	Title                   string
	Body                    string
	Author                  string
	Labels                  []string
	State                   ProviderIssueAnalysisState
	StableMarker            string
	ModelRoute              ModelRouteConfig
	ModelRouteSHA256        string
	PromptConfig            ReviewPromptConfig
	PromptConfigSHA256      string
	IssueTriageConfig       IssueTriageConfig
	IssueTriageConfigSHA256 string
	Analysis                string
	LastError               string
}

func (job ProviderIssueAnalysisJob) ProviderJob() ReviewJob {
	return ReviewJob{
		TenantID:               job.TenantID,
		InstallationID:         job.InstallationID,
		InstallationExternalID: job.InstallationExternalID,
		CredentialRef:          job.CredentialRef,
		Provider:               job.Provider,
		APIBaseURL:             job.APIBaseURL,
		Repository:             job.Repository,
		ReviewNumber:           job.IssueNumber,
	}
}

type ProviderIssueAnalysisEnqueue struct {
	Job       ProviderIssueAnalysisJob
	Duplicate bool
	Skipped   bool
	Reason    string
}

// ProviderIssueReaction is provider-native feedback left on the stable Issue
// analysis comment. It is deliberately separate from FindingReaction because
// an Issue triage report is advisory context analysis, not a code finding.
// Reactions are feedback only: they never enqueue model work or mutate policy.
type ProviderIssueReaction struct {
	Provider           Provider `json:"provider"`
	DeliveryID         string   `json:"delivery_id"`
	ReactionExternalID string   `json:"reaction_external_id"`
	ActorExternalID    string   `json:"actor_external_id"`
	Repository         string   `json:"repository"`
	AnalysisMarker     string   `json:"analysis_marker"`
	Kind               string   `json:"kind"`
	Action             string   `json:"action"`
}

func (reaction ProviderIssueReaction) Valid() bool {
	return reaction.Provider.Valid() && strings.TrimSpace(reaction.DeliveryID) != "" &&
		strings.TrimSpace(reaction.ReactionExternalID) != "" && strings.TrimSpace(reaction.ActorExternalID) != "" &&
		strings.TrimSpace(reaction.Repository) != "" && strings.HasPrefix(strings.TrimSpace(reaction.AnalysisMarker), "open-review-platform:issue-triage:") &&
		(reaction.Kind == "useful" || reaction.Kind == "not_useful") &&
		(reaction.Action == "created" || reaction.Action == "deleted")
}

// ProviderIssueAnalysisFilter is the tenant-scoped management query used by
// the Console. Provider Issue triage is intentionally separate from the
// durable finding aggregates in review_issues.
type ProviderIssueAnalysisFilter struct {
	State ProviderIssueAnalysisState
	Query string
	Limit int
}

func (filter ProviderIssueAnalysisFilter) Valid() bool {
	if filter.Limit < 1 || filter.Limit > 100 {
		return false
	}
	switch filter.State {
	case "", ProviderIssueAnalysisQueued, ProviderIssueAnalysisAcknowledged, ProviderIssueAnalysisCompleted, ProviderIssueAnalysisFailed, ProviderIssueAnalysisNeedsAttention:
		return true
	default:
		return false
	}
}

type ProviderIssueAnalysisSummary struct {
	ID              uuid.UUID                  `json:"id"`
	InstallationID  uuid.UUID                  `json:"installation_id"`
	Provider        Provider                   `json:"provider"`
	APIBaseURL      string                     `json:"api_base_url"`
	Repository      string                     `json:"repository"`
	IssueNumber     int                        `json:"issue_number"`
	Revision        int                        `json:"revision"`
	AnalysisAttempt int                        `json:"analysis_attempt"`
	Action          string                     `json:"action"`
	Title           string                     `json:"title"`
	Author          string                     `json:"author"`
	Labels          []string                   `json:"labels"`
	State           ProviderIssueAnalysisState `json:"state"`
	LastError       string                     `json:"last_error,omitempty"`
	// DeliveryFailure is a derived, current-attempt transport condition. The
	// durable job state remains queued/acknowledged until an explicit retry.
	DeliveryFailure bool `json:"delivery_failure"`
	// SkippedDelivery means the current inbox message completed without
	// advancing this job. Reconnecting cannot replay the consumed message.
	SkippedDelivery bool `json:"skipped_delivery"`
	// ConnectionBlocked is derived from the current installation eligibility.
	// It never changes the retained Issue revision or durable job state.
	ConnectionBlocked       bool       `json:"connection_blocked"`
	ReceiptCount            int        `json:"receipt_count"`
	CreatedAt               time.Time  `json:"created_at"`
	UpdatedAt               time.Time  `json:"updated_at"`
	CompletedAt             *time.Time `json:"completed_at,omitempty"`
	ModelRouteSHA256        string     `json:"model_route_sha256"`
	PromptConfigSHA256      string     `json:"prompt_config_sha256"`
	IssueTriageConfigSHA256 string     `json:"issue_triage_config_sha256"`
}

var providerIssueRetryIdempotencyKey = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{7,127}$`)

// ProviderIssueAnalysisRetryInput restarts execution for the exact retained
// Issue revision. It never refreshes provider content or mutable policy.
type ProviderIssueAnalysisRetryInput struct {
	ExpectedRevision int    `json:"expected_revision"`
	IdempotencyKey   string `json:"idempotency_key"`
}

func NormalizeProviderIssueAnalysisRetryInput(input ProviderIssueAnalysisRetryInput) (ProviderIssueAnalysisRetryInput, bool) {
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.ExpectedRevision < 1 || !providerIssueRetryIdempotencyKey.MatchString(input.IdempotencyKey) {
		return ProviderIssueAnalysisRetryInput{}, false
	}
	return input, true
}

type ProviderIssueAnalysisRetryResult struct {
	AnalysisID uuid.UUID                  `json:"analysis_id"`
	Revision   int                        `json:"revision"`
	Attempt    int                        `json:"attempt"`
	State      ProviderIssueAnalysisState `json:"state"`
	Replayed   bool                       `json:"replayed"`
}

type ProviderIssueAnalysisCounts struct {
	Queued            int `json:"queued"`
	Acknowledged      int `json:"acknowledged"`
	Completed         int `json:"completed"`
	Failed            int `json:"failed"`
	DeliveryFailures  int `json:"delivery_failures"`
	SkippedDeliveries int `json:"skipped_deliveries"`
	ConnectionBlocked int `json:"connection_blocked"`
	NeedsAttention    int `json:"needs_attention"`
}

type ProviderIssueAnalysisPage struct {
	Items  []ProviderIssueAnalysisSummary `json:"items"`
	Counts ProviderIssueAnalysisCounts    `json:"counts"`
}

type ProviderIssueAnalysisReceipt struct {
	Revision   int       `json:"revision"`
	Action     string    `json:"action"`
	EventName  string    `json:"event_name"`
	AdmittedAt time.Time `json:"admitted_at"`
}

type ProviderIssueAnalysisDetail struct {
	ProviderIssueAnalysisSummary
	Analysis       string                         `json:"analysis,omitempty"`
	AgentAdmission ProviderIssueAgentAdmission    `json:"agent_admission"`
	RetryReadiness ProviderIssueRetryReadiness    `json:"retry_readiness"`
	Receipts       []ProviderIssueAnalysisReceipt `json:"receipts"`
	UsefulCount    int                            `json:"useful_count"`
	NotUsefulCount int                            `json:"not_useful_count"`
	FeedbackSync   *ProviderIssueFeedbackSync     `json:"feedback_sync,omitempty"`
}

// RetryReadiness explains the current connection/setup/role gates without
// granting authority. POST rechecks every condition in its own transaction.
type ProviderIssueRetryReadiness struct {
	CanRetry          bool `json:"can_retry"`
	InstallationReady bool `json:"installation_ready"`
	SetupReady        bool `json:"setup_ready"`
	RoleAllowed       bool `json:"role_allowed"`
}

// ProviderIssueAgentAdmission is a read model for the exact retained Issue
// revision. It does not grant execution: POST rechecks policy, installation,
// role, and revision in the control plane before source capture is queued.
type ProviderIssueAgentAdmission struct {
	PolicyMode           string                   `json:"policy_mode"`
	InstallationReady    bool                     `json:"installation_ready"`
	CanRequest           bool                     `json:"can_request"`
	ExistingTaskConflict bool                     `json:"existing_task_conflict,omitempty"`
	ExistingTaskID       *uuid.UUID               `json:"existing_task_id,omitempty"`
	ExistingTaskState    string                   `json:"existing_task_state,omitempty"`
	ExistingTasks        []ProviderIssueAgentTask `json:"existing_tasks,omitempty"`
}

type ProviderIssueAgentTask struct {
	ID    uuid.UUID `json:"id"`
	State string    `json:"state"`
}

type ProviderIssueFeedbackSync struct {
	State         string     `json:"state"`
	Attempt       int        `json:"attempt"`
	ObservedAt    *time.Time `json:"observed_at,omitempty"`
	ReactionCount int        `json:"reaction_count"`
	LastError     string     `json:"last_error,omitempty"`
	NextPollAt    time.Time  `json:"next_poll_at"`
}

// ProviderIssueFeedbackPollTarget is the credential-free durable work claimed
// by the GitHub feedback poller. The provider token is resolved only at the
// worker boundary from the installation identity carried by Job.
type ProviderIssueFeedbackPollTarget struct {
	Job      ProviderIssueAnalysisJob
	WorkerID string
	Attempt  int
}

// ProviderIssueFeedbackReaction is one current thumbs reaction observed on
// the marker-keyed analysis comment. Absence from a complete provider snapshot
// retracts a previously active reaction; it never queues model work.
type ProviderIssueFeedbackReaction struct {
	ExternalID      string
	ActorExternalID string
	Kind            string
}

func (reaction ProviderIssueFeedbackReaction) Valid() bool {
	return strings.TrimSpace(reaction.ExternalID) != "" &&
		strings.TrimSpace(reaction.ActorExternalID) != "" &&
		(reaction.Kind == "useful" || reaction.Kind == "not_useful")
}
