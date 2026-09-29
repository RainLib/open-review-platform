package domain

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

var runRetryIdempotencyKey = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{7,127}$`)

// QuorumQueueRedeliveryLimit is shared by the RabbitMQ topology and the
// terminal inbox-recovery fence. Changing queue delivery policy must change
// both producer and recovery interpretation together.
const QuorumQueueRedeliveryLimit = 5

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

// ReviewMode records the caller's requested review intensity independently of
// the deployment default. It is persisted with a run so retries use the same
// scope instead of silently changing after a configuration update.
type ReviewMode string

const (
	ReviewModeConfigured ReviewMode = "configured"
	ReviewModeStandard   ReviewMode = "standard"
	ReviewModeDeep       ReviewMode = "deep"
	ReviewModeSecurity   ReviewMode = "security"
)

func (m ReviewMode) Valid() bool {
	return m == ReviewModeConfigured || m == ReviewModeStandard || m == ReviewModeDeep || m == ReviewModeSecurity
}

// Selectable reports whether a mode is a concrete review scope that can be
// retained as a repository default. `configured` is intentionally excluded:
// it is a caller request to resolve the repository policy, not a scope.
func (m ReviewMode) Selectable() bool {
	return m == ReviewModeStandard || m == ReviewModeDeep || m == ReviewModeSecurity
}

// RiskMode resolves a command mode to the planner mode. Standard always uses
// the balanced focused scope, deep expands to the full source delta, and
// security uses the critical high-signal path set. Configured alone honors
// the deployment default.
func (m ReviewMode) RiskMode(configured string) string {
	switch m {
	case ReviewModeStandard:
		return "focused"
	case ReviewModeDeep:
		return "standard"
	case ReviewModeSecurity:
		return "critical"
	case ReviewModeConfigured, "":
		return configured
	default:
		return configured
	}
}

type ReviewRequest struct {
	ID             uuid.UUID
	TenantID       uuid.UUID
	InstallationID uuid.UUID
	Provider       Provider
	APIBaseURL     string
	Repository     string
	ReviewNumber   int
	Title          string
	Author         string
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
	ReviewMode        ReviewMode `json:"review_mode"`
	HeadSHA           string     `json:"head_sha"`
	BaseSHA           string     `json:"base_sha"`
	CancelRequestedAt *time.Time `json:"cancel_requested_at,omitempty"`
	SupersededBy      *uuid.UUID `json:"superseded_by,omitempty"`
	FailureCode       string     `json:"failure_code,omitempty"`
	FailureMessage    string     `json:"failure_message,omitempty"`
	RuleSnapshotID    *uuid.UUID `json:"rule_snapshot_id,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	FinishedAt        *time.Time `json:"finished_at,omitempty"`
}

type ReviewRunSummary struct {
	ReviewRun
	Provider     Provider            `json:"provider"`
	APIBaseURL   string              `json:"api_base_url"`
	Repository   string              `json:"repository"`
	ReviewNumber int                 `json:"review_number"`
	Title        string              `json:"title"`
	Author       string              `json:"author"`
	Intervention *ReviewIntervention `json:"intervention,omitempty"`
	QueueBlock   *ReviewQueueBlock   `json:"queue_block,omitempty"`
}

// ReviewQueueBlock is a live execution obstruction, not a terminal run
// transition. The original run and its immutable evidence remain unchanged.
type ReviewQueueBlock struct {
	InstallationID    uuid.UUID                     `json:"installation_id"`
	Reason            string                        `json:"reason"`
	VerificationState InstallationVerificationState `json:"verification_state"`
}

// WorkQueueView is deliberately narrower than the review-history views. The
// queue contains work that is still executing or that has stopped at a point
// where a person can make a safe next decision; it is not a second history
// browser.
type WorkQueueView string

const (
	WorkQueueRunning        WorkQueueView = "running"
	WorkQueueNeedsAttention WorkQueueView = "needs_attention"
)

func (view WorkQueueView) Valid() bool {
	return view == WorkQueueRunning || view == WorkQueueNeedsAttention
}

type WorkQueueCursorDirection string

const (
	WorkQueueCursorAfter  WorkQueueCursorDirection = "after"
	WorkQueueCursorBefore WorkQueueCursorDirection = "before"
)

// WorkQueueFilter is an immutable read filter for durable review runs. A
// cursor includes the filter scope so an operator cannot accidentally carry a
// stale page token into another queue view or search.
type WorkQueueFilter struct {
	View            WorkQueueView
	Repository      string
	Query           string
	Cursor          string
	CursorDirection WorkQueueCursorDirection
	Limit           int
}

func (filter WorkQueueFilter) Valid() bool {
	if !filter.View.Valid() || filter.Limit < 1 || filter.Limit > 100 ||
		len(strings.TrimSpace(filter.Repository)) > 512 ||
		len(strings.TrimSpace(filter.Query)) > 256 ||
		len(strings.TrimSpace(filter.Cursor)) > 1024 {
		return false
	}
	if filter.CursorDirection != "" && filter.CursorDirection != WorkQueueCursorAfter && filter.CursorDirection != WorkQueueCursorBefore {
		return false
	}
	return filter.Cursor != "" || filter.CursorDirection != WorkQueueCursorBefore
}

type WorkQueueCounts struct {
	Running        int `json:"running"`
	NeedsAttention int `json:"needs_attention"`
}

type WorkQueuePage struct {
	Runs           []ReviewRunSummary `json:"runs"`
	Counts         WorkQueueCounts    `json:"counts"`
	NextCursor     string             `json:"next_cursor,omitempty"`
	PreviousCursor string             `json:"previous_cursor,omitempty"`
}

// PullRequestView selects the current review state for one pull request. The
// read model returns one durable current run per request; history remains on
// the run evidence page, rather than duplicating PR rows after every retry.
type PullRequestView string

const (
	PullRequestActive    PullRequestView = "active"
	PullRequestAttention PullRequestView = "attention"
	PullRequestCompleted PullRequestView = "completed"
	PullRequestAll       PullRequestView = "all"
)

func (view PullRequestView) Valid() bool {
	return view == PullRequestActive || view == PullRequestAttention || view == PullRequestCompleted || view == PullRequestAll
}

type PullRequestFilter struct {
	View            PullRequestView
	Repository      string
	Query           string
	Cursor          string
	CursorDirection WorkQueueCursorDirection
	Limit           int
}

func (filter PullRequestFilter) Valid() bool {
	if !filter.View.Valid() || filter.Limit < 1 || filter.Limit > 100 ||
		len(strings.TrimSpace(filter.Repository)) > 512 || len(strings.TrimSpace(filter.Query)) > 256 || len(strings.TrimSpace(filter.Cursor)) > 1024 {
		return false
	}
	if filter.CursorDirection != "" && filter.CursorDirection != WorkQueueCursorAfter && filter.CursorDirection != WorkQueueCursorBefore {
		return false
	}
	return filter.Cursor != "" || filter.CursorDirection != WorkQueueCursorBefore
}

type PullRequestCounts struct {
	Active    int `json:"active"`
	Attention int `json:"attention"`
	Completed int `json:"completed"`
	All       int `json:"all"`
}

type PullRequestPage struct {
	Runs           []ReviewRunSummary `json:"runs"`
	Counts         PullRequestCounts  `json:"counts"`
	NextCursor     string             `json:"next_cursor,omitempty"`
	PreviousCursor string             `json:"previous_cursor,omitempty"`
}

type pullRequestCursor struct {
	Version   int    `json:"v"`
	CreatedAt string `json:"created_at"`
	ID        string `json:"id"`
	Scope     string `json:"scope"`
}

func EncodePullRequestCursor(filter PullRequestFilter, run ReviewRunSummary) string {
	payload, err := json.Marshal(pullRequestCursor{
		Version:   1,
		CreatedAt: run.CreatedAt.UTC().Format(time.RFC3339Nano),
		ID:        run.ID.String(),
		Scope:     pullRequestCursorScope(filter),
	})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

func DecodePullRequestCursor(filter PullRequestFilter) (createdAt time.Time, id uuid.UUID, ok bool, err error) {
	if strings.TrimSpace(filter.Cursor) == "" {
		return time.Time{}, uuid.Nil, false, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(filter.Cursor)
	if err != nil {
		return time.Time{}, uuid.Nil, false, errors.New("decode pull request cursor")
	}
	var cursor pullRequestCursor
	if err := json.Unmarshal(payload, &cursor); err != nil {
		return time.Time{}, uuid.Nil, false, errors.New("unmarshal pull request cursor")
	}
	if cursor.Version != 1 || cursor.Scope != pullRequestCursorScope(filter) {
		return time.Time{}, uuid.Nil, false, errors.New("pull request cursor is invalid")
	}
	createdAt, err = time.Parse(time.RFC3339Nano, cursor.CreatedAt)
	if err != nil || createdAt.IsZero() {
		return time.Time{}, uuid.Nil, false, errors.New("pull request cursor timestamp is invalid")
	}
	id, err = uuid.Parse(cursor.ID)
	if err != nil || id == uuid.Nil {
		return time.Time{}, uuid.Nil, false, errors.New("pull request cursor id is invalid")
	}
	return createdAt.UTC(), id, true, nil
}

func pullRequestCursorScope(filter PullRequestFilter) string {
	value := strings.Join([]string{
		string(filter.View),
		strings.TrimSpace(filter.Repository),
		strings.ToLower(strings.TrimSpace(filter.Query)),
	}, "\x00")
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

type workQueueCursor struct {
	Version   int    `json:"v"`
	CreatedAt string `json:"created_at"`
	ID        string `json:"id"`
	Scope     string `json:"scope"`
}

func EncodeWorkQueueCursor(filter WorkQueueFilter, run ReviewRunSummary) string {
	payload, err := json.Marshal(workQueueCursor{
		Version:   1,
		CreatedAt: run.CreatedAt.UTC().Format(time.RFC3339Nano),
		ID:        run.ID.String(),
		Scope:     workQueueCursorScope(filter),
	})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

func DecodeWorkQueueCursor(filter WorkQueueFilter) (createdAt time.Time, id uuid.UUID, ok bool, err error) {
	if strings.TrimSpace(filter.Cursor) == "" {
		return time.Time{}, uuid.Nil, false, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(filter.Cursor)
	if err != nil {
		return time.Time{}, uuid.Nil, false, errors.New("decode work queue cursor")
	}
	var cursor workQueueCursor
	if err := json.Unmarshal(payload, &cursor); err != nil {
		return time.Time{}, uuid.Nil, false, errors.New("unmarshal work queue cursor")
	}
	if cursor.Version != 1 || cursor.Scope != workQueueCursorScope(filter) {
		return time.Time{}, uuid.Nil, false, errors.New("work queue cursor is invalid")
	}
	createdAt, err = time.Parse(time.RFC3339Nano, cursor.CreatedAt)
	if err != nil || createdAt.IsZero() {
		return time.Time{}, uuid.Nil, false, errors.New("work queue cursor timestamp is invalid")
	}
	id, err = uuid.Parse(cursor.ID)
	if err != nil || id == uuid.Nil {
		return time.Time{}, uuid.Nil, false, errors.New("work queue cursor id is invalid")
	}
	return createdAt.UTC(), id, true, nil
}

func workQueueCursorScope(filter WorkQueueFilter) string {
	value := strings.Join([]string{
		string(filter.View),
		strings.TrimSpace(filter.Repository),
		strings.ToLower(strings.TrimSpace(filter.Query)),
	}, "\x00")
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

// RunRetryInput creates a new execution for a terminal run. The source run
// stays immutable; the idempotency key lets a caller safely recover when a
// request succeeds but its HTTP response is lost.
type RunRetryInput struct {
	ExpectedRevision int    `json:"expected_revision"`
	IdempotencyKey   string `json:"idempotency_key"`
}

type RunRetryResult struct {
	Run      ReviewRun `json:"run"`
	Replayed bool      `json:"replayed"`
}

// InteractionResponseRetryResult requeues only the provider acknowledgement
// for an existing run. It never creates a new review execution.
type InteractionResponseRetryResult struct {
	RunID    uuid.UUID `json:"run_id"`
	Attempt  int       `json:"attempt"`
	Replayed bool      `json:"replayed"`
}

func NormalizeRunRetryInput(input RunRetryInput) (RunRetryInput, bool) {
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.ExpectedRevision < 1 || !runRetryIdempotencyKey.MatchString(input.IdempotencyKey) {
		return RunRetryInput{}, false
	}
	return input, true
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

// ReviewExecutionPlan is the immutable risk-admitted scope for one run. It
// is persisted before model execution so a publication retry never needs to
// re-plan or re-run OCR against the same exact revision.
type ReviewExecutionPlan struct {
	Mode                string            `json:"mode"`
	SelectedPaths       []string          `json:"selected_paths"`
	DeferredFiles       int               `json:"deferred_files"`
	StaticImpactSignals []string          `json:"static_impact_signals,omitempty"`
	FileScopes          []ReviewFileScope `json:"file_scopes,omitempty"`
}

// ReviewFileScope records the deterministic, source-free admission decision
// for one changed path. It is not evidence of a runtime dependency graph or
// proof that a deferred file was inspected by the model.
type ReviewFileScope struct {
	Path         string   `json:"path"`
	Selected     bool     `json:"selected"`
	Score        int      `json:"score"`
	Reasons      []string `json:"reasons"`
	ChangeType   string   `json:"change_type,omitempty"`
	PreviousPath string   `json:"previous_path,omitempty"`
	Additions    int      `json:"additions,omitempty"`
	Deletions    int      `json:"deletions,omitempty"`
	StatsKnown   bool     `json:"stats_known,omitempty"`
	Binary       bool     `json:"binary,omitempty"`
}

func NormalizeReviewExecutionPlan(input ReviewExecutionPlan) (ReviewExecutionPlan, bool) {
	input.Mode = strings.ToLower(strings.TrimSpace(input.Mode))
	if input.Mode != "standard" && input.Mode != "focused" && input.Mode != "critical" || input.DeferredFiles < 0 {
		return ReviewExecutionPlan{}, false
	}
	seen := make(map[string]struct{}, len(input.SelectedPaths))
	paths := make([]string, 0, len(input.SelectedPaths))
	for _, value := range input.SelectedPaths {
		path := strings.TrimSpace(value)
		if path == "" {
			return ReviewExecutionPlan{}, false
		}
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	if len(paths) > 10_000 {
		return ReviewExecutionPlan{}, false
	}
	input.SelectedPaths = paths
	seenSignals := make(map[string]struct{}, len(input.StaticImpactSignals))
	signals := make([]string, 0, len(input.StaticImpactSignals))
	for _, value := range input.StaticImpactSignals {
		signal := strings.TrimSpace(value)
		if signal == "" || len(signal) > 240 {
			return ReviewExecutionPlan{}, false
		}
		if _, exists := seenSignals[signal]; exists {
			continue
		}
		seenSignals[signal] = struct{}{}
		signals = append(signals, signal)
	}
	if len(signals) > 12 {
		return ReviewExecutionPlan{}, false
	}
	input.StaticImpactSignals = signals
	if len(input.FileScopes) > 10_000 {
		return ReviewExecutionPlan{}, false
	}
	if len(input.FileScopes) > 0 {
		selected := make(map[string]struct{}, len(input.SelectedPaths))
		for _, path := range input.SelectedPaths {
			selected[path] = struct{}{}
		}
		seenFiles := make(map[string]struct{}, len(input.FileScopes))
		selectedCount, deferredCount := 0, 0
		for i := range input.FileScopes {
			file := &input.FileScopes[i]
			file.Path = strings.TrimSpace(file.Path)
			if file.Path == "" || len(file.Path) > 4096 || file.Score < 0 || file.Score > 100 || len(file.Reasons) > 8 {
				return ReviewExecutionPlan{}, false
			}
			switch file.ChangeType {
			case "", "added", "modified", "deleted", "renamed", "copied", "type_changed":
			default:
				return ReviewExecutionPlan{}, false
			}
			if file.Additions < 0 || file.Deletions < 0 || (!file.StatsKnown && (file.Additions != 0 || file.Deletions != 0 || file.Binary)) {
				return ReviewExecutionPlan{}, false
			}
			if file.ChangeType != "renamed" && file.ChangeType != "copied" && file.PreviousPath != "" || len(file.PreviousPath) > 4096 || file.PreviousPath == file.Path && file.PreviousPath != "" {
				return ReviewExecutionPlan{}, false
			}
			if (file.ChangeType == "renamed" || file.ChangeType == "copied") && file.PreviousPath == "" {
				return ReviewExecutionPlan{}, false
			}
			if _, exists := seenFiles[file.Path]; exists {
				return ReviewExecutionPlan{}, false
			}
			seenFiles[file.Path] = struct{}{}
			for j := range file.Reasons {
				file.Reasons[j] = strings.TrimSpace(file.Reasons[j])
				if file.Reasons[j] == "" || len(file.Reasons[j]) > 240 {
					return ReviewExecutionPlan{}, false
				}
			}
			if file.Reasons == nil {
				file.Reasons = []string{}
			}
			_, inSelectedPaths := selected[file.Path]
			if file.Selected != inSelectedPaths {
				return ReviewExecutionPlan{}, false
			}
			if file.Selected {
				selectedCount++
			} else {
				deferredCount++
			}
		}
		if selectedCount != len(input.SelectedPaths) || deferredCount != input.DeferredFiles {
			return ReviewExecutionPlan{}, false
		}
	}
	if input.FileScopes == nil {
		input.FileScopes = []ReviewFileScope{}
	}
	return input, true
}

// ReviewFindingEvidence is the immutable finding payload produced for one
// exact review run. Feedback is summarized separately so a later disposition
// never rewrites the original model evidence.
type ReviewFindingEvidence struct {
	ID                         uuid.UUID                        `json:"id"`
	Path                       string                           `json:"path"`
	StartLine                  int                              `json:"start_line"`
	EndLine                    int                              `json:"end_line"`
	Severity                   string                           `json:"severity"`
	Category                   string                           `json:"category"`
	Body                       string                           `json:"body"`
	Suggestion                 string                           `json:"suggestion,omitempty"`
	CodeExcerpt                string                           `json:"code_excerpt,omitempty"`
	CodeExcerptStartLine       int                              `json:"code_excerpt_start_line,omitempty"`
	ProposedPatch              string                           `json:"proposed_patch,omitempty"`
	Fingerprint                string                           `json:"fingerprint"`
	ProviderMarker             string                           `json:"provider_marker,omitempty"`
	Disposition                string                           `json:"disposition,omitempty"`
	UsefulFeedbackCount        int                              `json:"useful_feedback_count"`
	FalsePositiveFeedbackCount int                              `json:"false_positive_feedback_count"`
	RuleAttributions           []FindingRuleAttributionEvidence `json:"rule_attributions"`
	CreatedAt                  time.Time                        `json:"created_at"`
}

// FindingRuleAttributionEvidence is read from the durable attribution ledger.
// Its version is constrained to a source in this run's immutable snapshot.
type FindingRuleAttributionEvidence struct {
	RuleKey       string    `json:"rule_key"`
	RuleVersionID uuid.UUID `json:"rule_version_id"`
	RuleSetID     uuid.UUID `json:"rule_set_id"`
	RuleSetName   string    `json:"rule_set_name"`
	Version       int       `json:"version"`
}

type ReviewRunStageEvidence struct {
	ID         uuid.UUID      `json:"id"`
	Stage      string         `json:"stage"`
	State      string         `json:"state"`
	Attempt    int            `json:"attempt"`
	StartedAt  *time.Time     `json:"started_at,omitempty"`
	FinishedAt *time.Time     `json:"finished_at,omitempty"`
	Details    map[string]any `json:"details"`
}

type PublicationReceiptEvidence struct {
	ID           uuid.UUID  `json:"id"`
	Provider     Provider   `json:"provider"`
	ReceiptKind  string     `json:"receipt_kind"`
	StableMarker string     `json:"stable_marker"`
	ExternalID   string     `json:"external_id,omitempty"`
	PayloadHash  string     `json:"payload_hash"`
	PublishedAt  *time.Time `json:"published_at,omitempty"`
	LastError    string     `json:"last_error,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// PublicationReceipt is the write-side outcome for one stable provider
// marker. It retains only an opaque external ID and a payload hash so provider
// response bodies and credentials never enter immutable review evidence.
type PublicationReceipt struct {
	ReceiptKind  string
	StableMarker string
	ExternalID   string
	PayloadHash  string
	Published    bool
	LastError    string
}

// ReviewEvidence is the single read model used by the PR overview, findings,
// files, checks, and activity views. Related runs retain revision history
// without allowing an older run to stand in for the current head.
type ReviewEvidence struct {
	Run                              ReviewRunSummary             `json:"run"`
	AcknowledgementRecoveryAvailable bool                         `json:"acknowledgement_recovery_available"`
	RelatedRuns                      []ReviewRunSummary           `json:"related_runs"`
	ExecutionAttempts                int                          `json:"execution_attempts"`
	ExecutionPlan                    *ReviewExecutionPlan         `json:"execution_plan,omitempty"`
	MergeGate                        *ReviewMergeGateDecision     `json:"merge_gate,omitempty"`
	ProviderChecks                   *ProviderCheckObservation    `json:"provider_checks,omitempty"`
	Findings                         []ReviewFindingEvidence      `json:"findings"`
	Stages                           []ReviewRunStageEvidence     `json:"stages"`
	Receipts                         []PublicationReceiptEvidence `json:"receipts"`
	ConfigurationSnapshot            []ReviewConfigSnapshot       `json:"configuration_snapshot"`
	Events                           []RunEvent                   `json:"events"`
}

type OutboxMessage struct {
	ID          uuid.UUID
	AggregateID uuid.UUID
	Topic       string
	DedupeKey   string
	Payload     map[string]any
	Attempts    int
}
