package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// AgentTaskCommandEvent is a provider-verified Issue comment. It remains
// separate from CommentEvent because review_interactions are pull-request
// scoped and must never be repurposed for code-writing requests on Issues.
type AgentTaskCommandEvent struct {
	Provider               Provider `json:"provider"`
	APIBaseURL             string   `json:"api_base_url"`
	DeliveryID             string   `json:"delivery_id"`
	InstallationExternalID string   `json:"installation_external_id"`
	Repository             string   `json:"repository"`
	IssueNumber            int      `json:"issue_number"`
	IssueRevision          string   `json:"issue_revision"`
	CommentExternalID      string   `json:"comment_external_id"`
	ActorExternalID        string   `json:"actor_external_id"`
	Body                   string   `json:"body"`
	IssueTitle             string   `json:"issue_title,omitempty"`
	IssueBody              string   `json:"issue_body,omitempty"`
	IssueLabels            []string `json:"issue_labels,omitempty"`
}

func (event AgentTaskCommandEvent) Valid() bool {
	return event.Provider.Valid() && strings.TrimSpace(event.APIBaseURL) != "" &&
		strings.TrimSpace(event.DeliveryID) != "" && strings.TrimSpace(event.InstallationExternalID) != "" &&
		strings.Trim(strings.TrimSpace(event.Repository), "/") != "" && event.IssueNumber > 0 &&
		strings.TrimSpace(event.IssueRevision) != "" && strings.TrimSpace(event.CommentExternalID) != "" &&
		strings.TrimSpace(event.ActorExternalID) != ""
}

type AgentTaskCommandOutcome struct {
	Accepted  bool       `json:"accepted"`
	Duplicate bool       `json:"duplicate"`
	Reason    string     `json:"reason,omitempty"`
	TaskID    *uuid.UUID `json:"task_id,omitempty"`
}

// AgentTask is the durable, approval-gated handoff between an Issue/PR and a
// coding agent. It intentionally has no command, credential, or patch field:
// creating a task must never cause an external agent to execute.
type AgentTask struct {
	Workflow       AgentWorkflowPolicy `json:"workflow"`
	ID             uuid.UUID           `json:"id"`
	TenantID       uuid.UUID           `json:"tenant_id,omitempty"`
	InstallationID uuid.UUID           `json:"installation_id"`
	Provider       Provider            `json:"provider"`
	APIBaseURL     string              `json:"api_base_url"`
	Repository     string              `json:"repository"`
	OriginKind     string              `json:"origin_kind"`
	OriginNumber   int                 `json:"origin_number"`
	OriginRevision string              `json:"origin_revision"`
	Intent         string              `json:"intent"`
	// PolicyRevision, MaxAttempts and MaxExecutionSeconds are copied from the
	// repository policy at task creation. They are an immutable admission
	// envelope: editing a policy can govern a new Issue revision but cannot
	// silently widen an already approved coding task.
	PolicyRevision      int        `json:"policy_revision"`
	MaxAttempts         int        `json:"max_attempts"`
	MaxExecutionSeconds int        `json:"max_execution_seconds"`
	MaxFeedbackCycles   int        `json:"max_feedback_cycles"`
	ExecutorProfile     string     `json:"executor_profile"`
	DecisionBackend     string     `json:"decision_backend"`
	FeedbackCycle       int        `json:"feedback_cycle"`
	ExecutionBranch     string     `json:"execution_branch"`
	ParentTaskID        *uuid.UUID `json:"parent_task_id,omitempty"`
	ParentAttemptID     *uuid.UUID `json:"parent_attempt_id,omitempty"`
	// SourceState and the base pair are captured by a credential-owning,
	// provider-reading worker before a plan may be created. An adapter receives
	// the exact pair but never the control-plane credential used to resolve it.
	SourceState      string     `json:"source_state"`
	SourceBaseRef    string     `json:"source_base_ref,omitempty"`
	SourceBaseSHA    string     `json:"source_base_sha,omitempty"`
	SourceCapturedAt *time.Time `json:"source_captured_at,omitempty"`
	State            string     `json:"state"`
	Revision         int        `json:"revision"`
	RequestedBy      string     `json:"requested_by"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// AgentTaskPage keeps a stable tenant-scoped keyset cursor separate from task
// state. A changing task status cannot reorder an existing page.
type AgentTaskPage struct {
	Tasks      []AgentTask `json:"agent_tasks"`
	NextCursor string      `json:"next_cursor,omitempty"`
}

// AgentTaskInput deliberately identifies an already-authorized provider
// resource instead of accepting arbitrary clone URLs, local paths, prompts,
// or CLI flags. Provider content remains untrusted and is snapshotted by a
// later ingestion adapter before any model sees it.
type AgentTaskInput struct {
	Provider       Provider `json:"provider"`
	APIBaseURL     string   `json:"api_base_url"`
	Repository     string   `json:"repository"`
	OriginKind     string   `json:"origin_kind"`
	OriginNumber   int      `json:"origin_number"`
	OriginRevision string   `json:"origin_revision"`
	Intent         string   `json:"intent"`
}

func (input AgentTaskInput) Valid() bool {
	return input.Provider.Valid() && strings.TrimSpace(input.APIBaseURL) != "" &&
		strings.Trim(strings.TrimSpace(input.Repository), "/") != "" &&
		input.OriginKind == "issue" &&
		input.OriginNumber > 0 && strings.TrimSpace(input.OriginRevision) != "" &&
		input.Intent == "implement"
}

type AgentTaskPlan struct {
	ID         uuid.UUID             `json:"id"`
	TaskID     uuid.UUID             `json:"task_id"`
	Revision   int                   `json:"revision"`
	State      string                `json:"state"`
	Summary    string                `json:"summary"`
	Sections   AgentTaskPlanSections `json:"sections"`
	PlanSHA256 string                `json:"plan_sha256"`
	CreatedBy  string                `json:"created_by"`
	ApprovedBy string                `json:"approved_by,omitempty"`
	ApprovedAt *time.Time            `json:"approved_at,omitempty"`
	CreatedAt  time.Time             `json:"created_at"`
}

type AgentTaskDetail struct {
	Acceptance *AgentTaskAcceptance `json:"acceptance,omitempty"`
	Task       AgentTask            `json:"task"`
	// The review target is the Issue's frozen base branch. For feedback tasks,
	// source_base_ref is the Draft head instead and must never be used as target.
	TargetBranch           string                           `json:"target_branch,omitempty"`
	Plans                  []AgentTaskPlan                  `json:"plans"`
	Classifications        []AgentTaskClassification        `json:"classifications"`
	Attempts               []AgentTaskAttempt               `json:"attempts"`
	PublicationCheckpoints []AgentTaskPublicationCheckpoint `json:"publication_checkpoints"`
	LinkedReviews          []AgentTaskLinkedReview          `json:"linked_reviews"`
	Feedback               *AgentTaskFeedbackReference      `json:"feedback,omitempty"`
	PlanPermissions        AgentTaskPlanPermissions         `json:"plan_permissions"`
}

// AgentTaskPublicationCheckpoint proves only that an adapter validated a
// proposed commit before its provider write. It is never a Draft or success
// receipt and remains visible after a lease expires or a new attempt begins.
type AgentTaskPublicationCheckpoint struct {
	AttemptID                 uuid.UUID `json:"attempt_id"`
	AttemptNumber             int       `json:"attempt_number"`
	AdapterJobID              string    `json:"adapter_job_id"`
	BranchName                string    `json:"branch_name"`
	HeadSHA                   string    `json:"head_sha"`
	PatchSHA256               string    `json:"patch_sha256"`
	ChangedFileCount          int       `json:"changed_file_count"`
	DiffBytes                 int64     `json:"diff_bytes"`
	VerificationProfileSHA256 string    `json:"verification_profile_sha256,omitempty"`
	VerificationOutputSHA256  string    `json:"verification_output_sha256,omitempty"`
	VerificationOutputBytes   int64     `json:"verification_output_bytes,omitempty"`
	RecordedAt                time.Time `json:"recorded_at"`
}

// AgentTaskLinkedReview is a read-time association to a review of an exact
// successful Agent attempt head. It does not change review admission policy.
type AgentTaskLinkedReview struct {
	RunID        uuid.UUID                  `json:"run_id"`
	State        RunState                   `json:"state"`
	HeadSHA      string                     `json:"head_sha"`
	ReviewNumber int                        `json:"review_number"`
	CreatedAt    time.Time                  `json:"created_at"`
	MergeGate    *AgentTaskLinkedReviewGate `json:"merge_gate,omitempty"`
}

// AgentTaskLinkedReviewGate exposes the immutable conclusion for the exact
// Agent commit, not a decision inferred from a mutable repository policy or
// an external check that may not have been published.
type AgentTaskLinkedReviewGate struct {
	Enabled          bool   `json:"enabled"`
	Threshold        string `json:"threshold"`
	Conclusion       string `json:"conclusion"`
	BlockingFindings int    `json:"blocking_findings"`
	FindingCount     int    `json:"finding_count"`
}

// AgentTaskFeedbackReference exposes only the provider comment identity to
// authorized task readers. The instruction text and its verification digest
// remain in the source/attempt boundary, not in the Console read model.
type AgentTaskFeedbackReference struct {
	SourceReviewRunID *uuid.UUID `json:"source_review_run_id,omitempty"`
	CommentExternalID string     `json:"comment_external_id"`
	ActorExternalID   string     `json:"actor_external_id"`
}

// AgentTaskPlanPermissions is a read-time UI affordance only. Every mutation
// rechecks current role, source, classification, plan revision, and creator
// separation inside its own transaction; clients cannot grant themselves an
// action by replaying this response.
type AgentTaskPlanPermissions struct {
	CanCreatePlan      bool   `json:"can_create_plan"`
	CanApprovePlan     bool   `json:"can_approve_plan"`
	CreateBlockReason  string `json:"create_block_reason,omitempty"`
	ApproveBlockReason string `json:"approve_block_reason,omitempty"`
}

// AgentTaskAttempt is one leased handoff of an approved immutable plan to an
// isolated coding-agent adapter. It intentionally records no prompt,
// provider credential, workspace path, or model transcript. Those values are
// either separately encrypted/artifacted in later stages or never persisted.
type AgentTaskAttempt struct {
	ID                        uuid.UUID  `json:"id"`
	TaskID                    uuid.UUID  `json:"task_id"`
	PlanID                    uuid.UUID  `json:"plan_id"`
	TaskRevision              int        `json:"task_revision"`
	PlanRevision              int        `json:"plan_revision"`
	Attempt                   int        `json:"attempt"`
	State                     string     `json:"state"`
	WorkerID                  string     `json:"worker_id,omitempty"`
	AdapterJobID              string     `json:"adapter_job_id,omitempty"`
	LockedUntil               *time.Time `json:"locked_until,omitempty"`
	DeadlineAt                *time.Time `json:"deadline_at,omitempty"`
	ErrorCode                 string     `json:"error_code,omitempty"`
	ErrorMessage              string     `json:"error_message,omitempty"`
	ResultSummary             string     `json:"result_summary,omitempty"`
	BranchName                string     `json:"branch_name,omitempty"`
	HeadSHA                   string     `json:"head_sha,omitempty"`
	PullRequestURL            string     `json:"pull_request_url,omitempty"`
	PullRequestNumber         int        `json:"pull_request_number,omitempty"`
	PatchSHA256               string     `json:"patch_sha256,omitempty"`
	ChangedFileCount          int        `json:"changed_file_count,omitempty"`
	DiffBytes                 int64      `json:"diff_bytes,omitempty"`
	VerificationProfileSHA256 string     `json:"verification_profile_sha256,omitempty"`
	VerificationOutputSHA256  string     `json:"verification_output_sha256,omitempty"`
	VerificationOutputBytes   int64      `json:"verification_output_bytes,omitempty"`
	CreatedAt                 time.Time  `json:"created_at"`
	StartedAt                 *time.Time `json:"started_at,omitempty"`
	FinishedAt                *time.Time `json:"finished_at,omitempty"`
	UpdatedAt                 time.Time  `json:"updated_at"`
}

// AgentTaskAttemptTarget is the minimum immutable handoff delivered to an
// isolated adapter. It deliberately omits any provider credential, clone URL,
// host workspace path, shell argument or unrestricted user prompt.
type AgentTaskAttemptTarget struct {
	Attempt  AgentTaskAttempt          `json:"attempt"`
	Task     AgentTask                 `json:"task"`
	Plan     AgentTaskPlan             `json:"plan"`
	Feedback *AgentTaskFeedbackBinding `json:"-"`
}

// AgentTaskSourceTarget is an internal worker-only provider read request. Its
// credential reference is never included in broker payloads or adapter
// submissions; it is resolved immediately by the source-admitter worker.
type AgentTaskSourceTarget struct {
	Task                   AgentTask                 `json:"task"`
	InstallationExternalID string                    `json:"installation_external_id"`
	CredentialRef          string                    `json:"-"`
	Feedback               *AgentTaskFeedbackBinding `json:"-"`
}

// AgentTaskFeedbackBinding is the immutable command evidence retained at
// admission. Provider text is reread and matched against it before planning.
type AgentTaskFeedbackBinding struct {
	SourceReviewRunID *uuid.UUID `json:"source_review_run_id,omitempty"`
	SystemInstruction string     `json:"system_instruction,omitempty"`
	CommentExternalID string     `json:"comment_external_id"`
	ActorExternalID   string     `json:"actor_external_id"`
	InstructionSHA256 string     `json:"instruction_sha256"`
	// The root Issue task's provider-read target branch is immutable. It is
	// resolved from the task lineage, never accepted from a webhook or browser.
	TargetBranch string `json:"target_branch,omitempty"`
}

func (binding AgentTaskFeedbackBinding) Valid() bool {
	if binding.SourceReviewRunID != nil {
		if *binding.SourceReviewRunID == uuid.Nil || binding.CommentExternalID != "review:"+binding.SourceReviewRunID.String() || binding.ActorExternalID != "system:review-worker" || len(binding.SystemInstruction) < 20 || len(binding.SystemInstruction) > 12000 {
			return false
		}
		digest := sha256.Sum256([]byte(binding.SystemInstruction))
		return hex.EncodeToString(digest[:]) == binding.InstructionSHA256
	}
	if binding.SystemInstruction != "" {
		return false
	}
	commentID, commentErr := strconv.ParseUint(binding.CommentExternalID, 10, 64)
	actorID, actorErr := strconv.ParseUint(binding.ActorExternalID, 10, 64)
	digest, digestErr := hex.DecodeString(binding.InstructionSHA256)
	return commentErr == nil && commentID > 0 && actorErr == nil && actorID > 0 && digestErr == nil && len(digest) == sha256.Size
}

func (binding AgentTaskFeedbackBinding) ExecutionValid() bool {
	branch := strings.TrimSpace(binding.TargetBranch)
	return binding.Valid() && branch != "" && branch == binding.TargetBranch && len(branch) <= 512 && !strings.ContainsAny(branch, " \t\r\n\x00")
}

// AgentTaskSourceSnapshot is the minimal immutable checkout base. It is not
// an Agent-generated value; only a credential-owning provider reader may set
// it after the Issue command has been durably accepted.
type AgentTaskSourceSnapshot struct {
	BaseRef        string                     `json:"base_ref"`
	BaseSHA        string                     `json:"base_sha"`
	TargetBranch   string                     `json:"target_branch,omitempty"`
	Issue          *AgentTaskIssueSnapshot    `json:"-"`
	Feedback       *AgentTaskFeedbackSnapshot `json:"-"`
	GeneratedPlan  *AgentTaskPlanSections     `json:"-"`
	DecisionSignal *AgentTaskDecisionSignal   `json:"-"`
}

// AgentTaskFeedbackSnapshot is provider-read comment evidence, never an
// instruction from a webhook or browser-supplied task payload.
type AgentTaskFeedbackSnapshot struct {
	CommentExternalID string
	ActorExternalID   string
	Instruction       string
}

// AgentTaskDecisionSignal is untrusted model output normalized by the worker.
// It can only narrow an otherwise eligible deterministic classification.
type AgentTaskDecisionSignal struct {
	Backend    string
	Model      string
	Choice     string
	Confidence int
}

// AgentTaskIssueSnapshot is read directly from the provider before source
// admission. It never enters an adapter request or a public task response.
type AgentTaskIssueSnapshot struct {
	Title    string
	Body     string
	Labels   []string
	Revision string
}

// AgentIssueRevision ignores comment activity: provider updated_at commonly
// advances when our acknowledgement is posted, even when the Issue title and
// body have not changed. The opt-in label is checked separately at source
// admission so removing it can still stop an automatic candidate.
func AgentIssueRevision(provider Provider, apiBaseURL, repository string, number int, title, body string) string {
	evidence := strings.Join([]string{string(provider), strings.TrimSuffix(strings.TrimSpace(apiBaseURL), "/"), strings.Trim(strings.TrimSpace(repository), "/"), strconv.Itoa(number), strings.TrimSpace(title), strings.TrimSpace(body)}, "\n")
	digest := sha256.Sum256([]byte(evidence))
	return "issue-sha256:" + hex.EncodeToString(digest[:])
}

// Automatic candidates also freeze the exact label set. Removing the
// administrator's opt-in label while the task is queued changes this digest
// and blocks source admission.
func AgentAutomaticIssueRevision(provider Provider, apiBaseURL, repository string, number int, title, body string, labels []string) string {
	canonical := make([]string, 0, len(labels))
	for _, label := range labels {
		if value := strings.ToLower(strings.TrimSpace(label)); value != "" {
			canonical = append(canonical, value)
		}
	}
	sort.Strings(canonical)
	evidence := AgentIssueRevision(provider, apiBaseURL, repository, number, title, body) + "\n" + strings.Join(canonical, "\n")
	digest := sha256.Sum256([]byte(evidence))
	return "issue-labels-sha256:" + hex.EncodeToString(digest[:])
}

func (snapshot AgentTaskSourceSnapshot) Valid() bool {
	if len(strings.TrimSpace(snapshot.BaseRef)) < 1 || len(strings.TrimSpace(snapshot.BaseRef)) > 512 {
		return false
	}
	value := strings.TrimSpace(snapshot.BaseSHA)
	if len(value) < 7 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if !(character >= '0' && character <= '9') && !(character >= 'a' && character <= 'f') && !(character >= 'A' && character <= 'F') {
			return false
		}
	}
	return true
}

// AgentTaskAdapterEvent is authenticated before it reaches the store. The
// delivery ID is still persisted because a valid HMAC can be retried or
// duplicated by a transport; terminal state changes must remain idempotent.
type AgentTaskAdapterEvent struct {
	AttemptID                 uuid.UUID `json:"attempt_id"`
	AdapterJobID              string    `json:"adapter_job_id"`
	DeliveryID                string    `json:"delivery_id"`
	Kind                      string    `json:"kind"`
	Summary                   string    `json:"summary,omitempty"`
	BranchName                string    `json:"branch_name,omitempty"`
	HeadSHA                   string    `json:"head_sha,omitempty"`
	PullRequestURL            string    `json:"pull_request_url,omitempty"`
	PullRequestNumber         int       `json:"pull_request_number,omitempty"`
	PatchSHA256               string    `json:"patch_sha256,omitempty"`
	ChangedFileCount          int       `json:"changed_file_count,omitempty"`
	DiffBytes                 int64     `json:"diff_bytes,omitempty"`
	VerificationProfileSHA256 string    `json:"verification_profile_sha256,omitempty"`
	VerificationOutputSHA256  string    `json:"verification_output_sha256,omitempty"`
	VerificationOutputBytes   int64     `json:"verification_output_bytes,omitempty"`
	ErrorCode                 string    `json:"error_code,omitempty"`
}

func (event AgentTaskAdapterEvent) Valid() bool {
	if event.AttemptID == uuid.Nil || strings.TrimSpace(event.AdapterJobID) == "" ||
		strings.TrimSpace(event.DeliveryID) == "" ||
		(event.Kind != "heartbeat" && event.Kind != "publication_checkpoint" && event.Kind != "completed" && event.Kind != "failed" && event.Kind != "needs_attention") {
		return false
	}
	if event.Kind == "heartbeat" {
		return event.Summary == "" && event.BranchName == "" && event.HeadSHA == "" && event.PullRequestURL == "" && event.PullRequestNumber == 0 && event.PatchSHA256 == "" && event.ChangedFileCount == 0 && event.DiffBytes == 0 && event.ErrorCode == "" && event.VerificationProfileSHA256 == "" && event.VerificationOutputSHA256 == "" && event.VerificationOutputBytes == 0
	}
	if event.Kind == "publication_checkpoint" {
		if event.Summary != "" || event.ErrorCode != "" || event.PullRequestURL != "" || event.PullRequestNumber != 0 || !strings.HasPrefix(event.BranchName, "agent/") || (len(event.HeadSHA) != 40 && len(event.HeadSHA) != 64) || event.ChangedFileCount <= 0 || event.DiffBytes <= 0 || !validAgentVerificationEvidence(event.VerificationProfileSHA256, event.VerificationOutputSHA256, event.VerificationOutputBytes) {
			return false
		}
		if decoded, err := hex.DecodeString(event.HeadSHA); err != nil || hex.EncodeToString(decoded) != event.HeadSHA {
			return false
		}
		patch, err := hex.DecodeString(event.PatchSHA256)
		return err == nil && len(patch) == sha256.Size && hex.EncodeToString(patch) == event.PatchSHA256
	}
	if len(strings.TrimSpace(event.Summary)) < 1 || len(strings.TrimSpace(event.Summary)) > 4000 {
		return false
	}
	// Only a completed attempt is allowed to present a branch, revision or PR
	// evidence. Failure callbacks must not smuggle a provider-looking result
	// into an operator view.
	if event.Kind == "completed" {
		if !validAgentVerificationEvidence(event.VerificationProfileSHA256, event.VerificationOutputSHA256, event.VerificationOutputBytes) {
			return false
		}
		if event.VerificationProfileSHA256 != "" && event.PatchSHA256 == "" {
			return false
		}
		if event.PatchSHA256 != "" || event.ChangedFileCount != 0 || event.DiffBytes != 0 {
			if len(event.PatchSHA256) != 64 || event.ChangedFileCount <= 0 || event.DiffBytes <= 0 {
				return false
			}
			for _, digit := range event.PatchSHA256 {
				if (digit < '0' || digit > '9') && (digit < 'a' || digit > 'f') {
					return false
				}
			}
		}
		return event.ErrorCode == "" && event.PullRequestNumber > 0
	}
	return event.BranchName == "" && event.HeadSHA == "" && event.PullRequestURL == "" && event.PullRequestNumber == 0 && event.PatchSHA256 == "" && event.ChangedFileCount == 0 && event.DiffBytes == 0 && event.VerificationProfileSHA256 == "" && event.VerificationOutputSHA256 == "" && event.VerificationOutputBytes == 0
}

// Absence is distinct from a passed, deployment-approved verification command.
// Only bounded hashes/counts cross the adapter callback boundary, never logs.
func validAgentVerificationEvidence(profileSHA, outputSHA string, outputBytes int64) bool {
	if profileSHA == "" && outputSHA == "" && outputBytes == 0 {
		return true
	}
	if outputBytes < 0 || outputBytes > 1<<20 {
		return false
	}
	for _, value := range []string{profileSHA, outputSHA} {
		if len(value) != 64 {
			return false
		}
		for _, digit := range value {
			if (digit < '0' || digit > '9') && (digit < 'a' || digit > 'f') {
				return false
			}
		}
	}
	return true
}

// AgentTaskExecutionRequest is the exact immutable admission message. The
// broker payload is only a routing hint; a worker re-loads and verifies this
// tuple under a database lock before it can reserve an attempt.
type AgentTaskExecutionRequest struct {
	TaskID       uuid.UUID `json:"task_id"`
	TaskRevision int       `json:"task_revision"`
	PlanID       uuid.UUID `json:"plan_id"`
	PlanRevision int       `json:"plan_revision"`
	PlanSHA256   string    `json:"plan_sha256"`
}

func (request AgentTaskExecutionRequest) Valid() bool {
	return request.TaskID != uuid.Nil && request.PlanID != uuid.Nil &&
		request.TaskRevision > 0 && request.PlanRevision > 0 &&
		len(strings.TrimSpace(request.PlanSHA256)) == 64
}

// AgentTaskCancellationRequest is the immutable, idempotent stop signal for a
// separately deployed coding-agent adapter. The control plane first revokes
// the lease locally; this request asks the adapter to reclaim its sandbox and
// must never be treated as authority to alter a repository.
type AgentTaskCancellationRequest struct {
	TaskID       uuid.UUID `json:"task_id"`
	AttemptID    uuid.UUID `json:"attempt_id"`
	AdapterJobID string    `json:"adapter_job_id"`
}

func (request AgentTaskCancellationRequest) Valid() bool {
	return request.TaskID != uuid.Nil && request.AttemptID != uuid.Nil &&
		len(strings.TrimSpace(request.AdapterJobID)) > 0 && len(strings.TrimSpace(request.AdapterJobID)) <= 256
}

// AgentTaskClassification is an evidence-bound admission decision. It is
// deliberately separate from an AgentTask policy: classification can explain
// risk and missing context, but never grant execution or provider-write
// capability.
type AgentTaskClassification struct {
	ID             uuid.UUID `json:"id"`
	TaskID         uuid.UUID `json:"task_id"`
	TaskRevision   int       `json:"task_revision"`
	SourceRevision string    `json:"source_revision"`
	Decision       string    `json:"decision"`
	RiskLevel      string    `json:"risk_level"`
	Confidence     int       `json:"confidence"`
	Reasons        []string  `json:"reasons"`
	// Evaluation is the immutable Judge / Evaluate / Verify trail. Each stage
	// describes a deterministic policy result; it is explanation evidence, not
	// a capability grant for a model, sandbox, provider write, or merge.
	Evaluation        []AgentTaskEvaluation `json:"evaluation"`
	NextAction        string                `json:"next_action"`
	SnapshotSHA256    string                `json:"snapshot_sha256"`
	ClassifierVersion string                `json:"classifier_version"`
	CreatedAt         time.Time             `json:"created_at"`
}

// AgentTaskEvaluation records a deterministic Judge/Evaluate/Verify check or
// a separate, advisory model signal. Neither is an execution capability.
type AgentTaskEvaluation struct {
	Stage   string   `json:"stage"`
	Outcome string   `json:"outcome"`
	Summary string   `json:"summary"`
	Signals []string `json:"signals"`
}

// AgentTaskPlanInput is an operator-visible plan, not the free-form model
// transcript. The future planner must produce this bounded artifact before
// execution can be admitted.
type AgentTaskPlanInput struct {
	Summary  string                 `json:"summary,omitempty"` // legacy API clients
	Sections *AgentTaskPlanSections `json:"sections,omitempty"`
}

func (input AgentTaskPlanInput) Valid() bool {
	if input.Sections != nil {
		if !input.Sections.Valid() || (strings.TrimSpace(input.Summary) != "" && strings.TrimSpace(input.Summary) != input.Sections.Summary()) {
			return false
		}
	}
	summary := input.CanonicalSummary()
	return len(summary) >= 20 && len(summary) <= 12000
}

// AgentTaskPlanSections keeps the human approval contract inspectable. The
// adapter still receives one canonical summary whose SHA-256 is frozen in the
// approval and execution envelope, so these fields cannot disagree with the
// instructions the coding Agent is allowed to see.
type AgentTaskPlanSections struct {
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
	Objective          string   `json:"objective,omitempty"`
	Scope              string   `json:"scope,omitempty"`
	Verification       string   `json:"verification,omitempty"`
	Risks              string   `json:"risks,omitempty"`
	Unknowns           string   `json:"unknowns,omitempty"`
}

func (sections AgentTaskPlanSections) Valid() bool {
	if len(sections.AcceptanceCriteria) > 20 {
		return false
	}
	for _, criterion := range sections.AcceptanceCriteria {
		if len(strings.TrimSpace(criterion)) < 3 || len(criterion) > 1000 || strings.ContainsRune(criterion, 0) {
			return false
		}
	}
	for _, part := range []struct {
		value string
		min   int
	}{
		{sections.Objective, 20}, {sections.Scope, 10}, {sections.Verification, 10},
		{sections.Risks, 3}, {sections.Unknowns, 3},
	} {
		value := strings.TrimSpace(part.value)
		if len(value) < part.min || len(value) > 4000 || strings.ContainsRune(value, '\x00') {
			return false
		}
	}
	return true
}

func (sections AgentTaskPlanSections) Normalized() AgentTaskPlanSections {
	criteria := make([]string, len(sections.AcceptanceCriteria))
	for i, criterion := range sections.AcceptanceCriteria {
		criteria[i] = strings.TrimSpace(criterion)
	}
	if sections.AcceptanceCriteria == nil {
		criteria = nil
	}
	return AgentTaskPlanSections{
		AcceptanceCriteria: criteria,
		Objective:          strings.TrimSpace(sections.Objective),
		Scope:              strings.TrimSpace(sections.Scope),
		Verification:       strings.TrimSpace(sections.Verification),
		Risks:              strings.TrimSpace(sections.Risks),
		Unknowns:           strings.TrimSpace(sections.Unknowns),
	}
}

func (sections AgentTaskPlanSections) Summary() string {
	normalized := sections.Normalized()
	return "## Objective\n" + normalized.Objective +
		"\n\n## Scope and impact\n" + normalized.Scope +
		"\n\n## Verification\n" + normalized.Verification +
		"\n\n## Risks\n" + normalized.Risks +
		"\n\n## Unknowns\n" + normalized.Unknowns + normalized.acceptanceSummary()
}

func (input AgentTaskPlanInput) CanonicalSummary() string {
	if input.Sections != nil {
		return input.Sections.Summary()
	}
	return strings.TrimSpace(input.Summary)
}

type AgentTaskPlanApprovalInput struct {
	Revision int `json:"revision"`
}

func (input AgentTaskPlanApprovalInput) Valid() bool { return input.Revision > 0 }

// AgentTaskCancellationInput uses optimistic revision matching so a Console
// cancellation cannot supersede a newer Issue revision or an operator's
// intervening decision. The reason is operator evidence, not Agent input.
type AgentTaskCancellationInput struct {
	Revision int    `json:"revision"`
	Reason   string `json:"reason"`
}

func (input AgentTaskCancellationInput) Valid() bool {
	return input.Revision > 0 && len(strings.TrimSpace(input.Reason)) >= 3 && len(strings.TrimSpace(input.Reason)) <= 1000
}

// AgentTaskPolicy is repository-qualified. The absence of a policy is
// equivalent to disabled, so a newly connected repository can never be
// enrolled by accident. "suggest" may later emit a non-executing candidate;
// "manual" permits an authorized human to create a task. No automatic
// execution mode exists in P0.
type AgentTaskPolicy struct {
	Workflow             AgentWorkflowPolicy `json:"workflow"`
	ID                   uuid.UUID           `json:"id"`
	Provider             Provider            `json:"provider"`
	APIBaseURL           string              `json:"api_base_url"`
	Repository           string              `json:"repository"`
	Mode                 string              `json:"mode"`
	MaxAttempts          int                 `json:"max_attempts"`
	MaxExecutionSeconds  int                 `json:"max_execution_seconds"`
	MaxFeedbackCycles    int                 `json:"max_feedback_cycles"`
	ExecutorProfile      string              `json:"executor_profile"`
	DecisionBackend      string              `json:"decision_backend"`
	AutoAdmissionEnabled bool                `json:"auto_admission_enabled"`
	AutoAdmissionLabel   string              `json:"auto_admission_label"`
	Revision             int                 `json:"revision"`
	UpdatedBy            string              `json:"updated_by"`
	UpdatedAt            time.Time           `json:"updated_at"`
}

type AgentTaskPolicyInput struct {
	Workflow             *AgentWorkflowPolicy `json:"workflow,omitempty"`
	Provider             Provider             `json:"provider"`
	APIBaseURL           string               `json:"api_base_url"`
	Repository           string               `json:"repository"`
	Mode                 string               `json:"mode"`
	MaxAttempts          int                  `json:"max_attempts"`
	MaxExecutionSeconds  int                  `json:"max_execution_seconds"`
	MaxFeedbackCycles    int                  `json:"max_feedback_cycles"`
	ExecutorProfile      string               `json:"executor_profile"`
	DecisionBackend      string               `json:"decision_backend"`
	AutoAdmissionEnabled bool                 `json:"auto_admission_enabled"`
	AutoAdmissionLabel   string               `json:"auto_admission_label"`
	Revision             int                  `json:"revision"`
}

func (input AgentTaskPolicyInput) Valid() bool {
	if input.Workflow != nil && !input.Workflow.Valid() {
		return false
	}
	return input.Provider.Valid() && strings.TrimSpace(input.APIBaseURL) != "" &&
		strings.Trim(strings.TrimSpace(input.Repository), "/") != "" &&
		(input.Mode == "disabled" || input.Mode == "suggest" || input.Mode == "manual") &&
		// The repository selects one pinned executor. The adapter separately
		// refuses a task when that exact profile is not installed there.
		(input.ExecutorProfile == "codex" || input.ExecutorProfile == "claude") &&
		(input.DecisionBackend == "" || input.DecisionBackend == "jev" || input.DecisionBackend == "deterministic") &&
		len(strings.TrimSpace(input.AutoAdmissionLabel)) >= 1 && len(strings.TrimSpace(input.AutoAdmissionLabel)) <= 128 &&
		!strings.ContainsAny(input.AutoAdmissionLabel, "\r\n") &&
		input.MaxAttempts >= 1 && input.MaxAttempts <= 3 &&
		input.MaxExecutionSeconds >= 60 && input.MaxExecutionSeconds <= 7200 &&
		input.MaxFeedbackCycles >= 0 && input.MaxFeedbackCycles <= 3 && input.Revision >= 0
}

// AgentTaskFeedbackEvent is a verified provider pull-request/MR comment. The
// event contains no authoritative source revision: the store binds it to a
// prior draft attempt, and the source worker must re-read the provider head
// before a feedback task can be planned.
type AgentTaskFeedbackEvent struct {
	Provider               Provider `json:"provider"`
	APIBaseURL             string   `json:"api_base_url"`
	DeliveryID             string   `json:"delivery_id"`
	InstallationExternalID string   `json:"installation_external_id"`
	Repository             string   `json:"repository"`
	PullRequestNumber      int      `json:"pull_request_number"`
	CommentExternalID      string   `json:"comment_external_id"`
	ActorExternalID        string   `json:"actor_external_id"`
	Instruction            string   `json:"instruction"`
}

func (event AgentTaskFeedbackEvent) Valid() bool {
	return event.Provider.Valid() && strings.TrimSpace(event.APIBaseURL) != "" &&
		strings.TrimSpace(event.DeliveryID) != "" && strings.TrimSpace(event.InstallationExternalID) != "" &&
		strings.Trim(strings.TrimSpace(event.Repository), "/") != "" && event.PullRequestNumber > 0 &&
		strings.TrimSpace(event.CommentExternalID) != "" && strings.TrimSpace(event.ActorExternalID) != "" &&
		len(strings.TrimSpace(event.Instruction)) >= 8 && len(strings.TrimSpace(event.Instruction)) <= 4000
}

type AgentTaskFeedbackOutcome struct {
	Accepted  bool       `json:"accepted"`
	Duplicate bool       `json:"duplicate"`
	Reason    string     `json:"reason,omitempty"`
	TaskID    *uuid.UUID `json:"task_id,omitempty"`
}

func (sections AgentTaskPlanSections) acceptanceSummary() string {
	if len(sections.AcceptanceCriteria) == 0 {
		return ""
	}
	return "\n\n## Acceptance criteria\n- " + strings.Join(sections.AcceptanceCriteria, "\n- ")
}
