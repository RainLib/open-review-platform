package domain

import (
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

type DataClass string
type DataScopeKind string
type DataGovernanceJobKind string
type DataGovernanceJobState string

const (
	DataClassRawWebhook     DataClass = "raw_webhook"
	DataClassFindings       DataClass = "findings"
	DataClassAudit          DataClass = "audit"
	DataClassUsage          DataClass = "usage"
	DataClassOperationalLog DataClass = "operational_logs"
	DataClassAll            DataClass = "all"

	DataScopeTenant     DataScopeKind = "tenant"
	DataScopeRepository DataScopeKind = "repository"
	DataScopeReviewRun  DataScopeKind = "review_run"
	DataScopeAuditRange DataScopeKind = "audit_range"

	DataJobExport          DataGovernanceJobKind = "export"
	DataJobDeletion        DataGovernanceJobKind = "deletion"
	DataJobRegionMigration DataGovernanceJobKind = "region_migration"

	DataJobRequested        DataGovernanceJobState = "requested"
	DataJobAwaitingApproval DataGovernanceJobState = "awaiting_approval"
	DataJobQueued           DataGovernanceJobState = "queued"
	DataJobRunning          DataGovernanceJobState = "running"
	DataJobCompleted        DataGovernanceJobState = "completed"
	DataJobFailed           DataGovernanceJobState = "failed"
	DataJobCancelled        DataGovernanceJobState = "cancelled"
	DataJobRejected         DataGovernanceJobState = "rejected"
)

type DataResidency struct {
	Configured    bool       `json:"configured"`
	PrimaryRegion string     `json:"primary_region,omitempty"`
	BackupRegion  string     `json:"backup_region,omitempty"`
	ObjectRegion  string     `json:"object_region,omitempty"`
	QueueRegion   string     `json:"queue_region,omitempty"`
	ModelBoundary string     `json:"model_boundary"`
	Revision      int        `json:"revision,omitempty"`
	EffectiveAt   *time.Time `json:"effective_at,omitempty"`
	ObservedAt    *time.Time `json:"observed_at,omitempty"`
	UpdatedBy     string     `json:"updated_by,omitempty"`
	UpdatedAt     *time.Time `json:"updated_at,omitempty"`
}

type DataBoundary struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Observed bool   `json:"observed"`
	Detail   string `json:"detail"`
	External bool   `json:"external"`
}

type RetentionPolicy struct {
	ID              uuid.UUID     `json:"id"`
	ScopeKind       DataScopeKind `json:"scope_kind"`
	ScopeRef        string        `json:"scope_ref,omitempty"`
	DataClass       DataClass     `json:"data_class"`
	RetentionDays   int           `json:"retention_days"`
	State           string        `json:"state"`
	Revision        int           `json:"revision"`
	ImpactRecords   int64         `json:"impact_records"`
	ImpactLegalHold bool          `json:"impact_legal_hold"`
	ChangeReason    string        `json:"change_reason"`
	RequestedBy     string        `json:"requested_by"`
	DecidedBy       string        `json:"decided_by,omitempty"`
	DecidedAt       *time.Time    `json:"decided_at,omitempty"`
	CreatedAt       time.Time     `json:"created_at"`
	UpdatedAt       time.Time     `json:"updated_at"`
}

type RetentionPolicyInput struct {
	ScopeKind        DataScopeKind `json:"scope_kind"`
	ScopeRef         string        `json:"scope_ref,omitempty"`
	DataClass        DataClass     `json:"data_class"`
	RetentionDays    int           `json:"retention_days"`
	ExpectedRevision int           `json:"expected_revision"`
	ChangeReason     string        `json:"change_reason"`
}

type GovernanceDecisionInput struct {
	Decision         string `json:"decision"`
	Reason           string `json:"reason"`
	ExpectedRevision int    `json:"expected_revision"`
}

type DataLegalHold struct {
	ID         uuid.UUID     `json:"id"`
	ScopeKind  DataScopeKind `json:"scope_kind"`
	ScopeRef   string        `json:"scope_ref,omitempty"`
	DataClass  DataClass     `json:"data_class"`
	Reason     string        `json:"reason"`
	State      string        `json:"state"`
	Revision   int           `json:"revision"`
	CreatedBy  string        `json:"created_by"`
	CreatedAt  time.Time     `json:"created_at"`
	ReleasedBy string        `json:"released_by,omitempty"`
	ReleasedAt *time.Time    `json:"released_at,omitempty"`
}

type DataLegalHoldInput struct {
	ScopeKind DataScopeKind `json:"scope_kind"`
	ScopeRef  string        `json:"scope_ref,omitempty"`
	DataClass DataClass     `json:"data_class"`
	Reason    string        `json:"reason"`
}

type DataGovernanceJob struct {
	ID                  uuid.UUID              `json:"id"`
	ParentJobID         *uuid.UUID             `json:"parent_job_id,omitempty"`
	Kind                DataGovernanceJobKind  `json:"kind"`
	ScopeKind           DataScopeKind          `json:"scope_kind"`
	ScopeRef            string                 `json:"scope_ref,omitempty"`
	DataClasses         []DataClass            `json:"data_classes"`
	DesiredRegion       string                 `json:"desired_region,omitempty"`
	ExternalOperationID string                 `json:"external_operation_id,omitempty"`
	State               DataGovernanceJobState `json:"state"`
	Progress            int                    `json:"progress"`
	Revision            int                    `json:"revision"`
	IdempotencyKey      string                 `json:"idempotency_key"`
	Reason              string                 `json:"reason"`
	RequestedBy         string                 `json:"requested_by"`
	ApprovedBy          string                 `json:"approved_by,omitempty"`
	ApprovedAt          *time.Time             `json:"approved_at,omitempty"`
	ReversibleUntil     *time.Time             `json:"reversible_until,omitempty"`
	ArtifactReady       bool                   `json:"artifact_ready"`
	Receipt             map[string]any         `json:"receipt"`
	ErrorCode           string                 `json:"error_code,omitempty"`
	ErrorMessage        string                 `json:"error_message,omitempty"`
	CreatedAt           time.Time              `json:"created_at"`
	UpdatedAt           time.Time              `json:"updated_at"`
	StartedAt           *time.Time             `json:"started_at,omitempty"`
	FinishedAt          *time.Time             `json:"finished_at,omitempty"`
	NextAttemptAt       *time.Time             `json:"next_attempt_at,omitempty"`
}

type DataGovernanceJobInput struct {
	Kind           DataGovernanceJobKind `json:"kind"`
	ScopeKind      DataScopeKind         `json:"scope_kind"`
	ScopeRef       string                `json:"scope_ref,omitempty"`
	DataClasses    []DataClass           `json:"data_classes,omitempty"`
	DesiredRegion  string                `json:"desired_region,omitempty"`
	IdempotencyKey string                `json:"idempotency_key"`
	Reason         string                `json:"reason"`
}

type DataGovernanceOverview struct {
	Residency  DataResidency       `json:"residency"`
	Boundaries []DataBoundary      `json:"boundaries"`
	Policies   []RetentionPolicy   `json:"policies"`
	LegalHolds []DataLegalHold     `json:"legal_holds"`
	Jobs       []DataGovernanceJob `json:"jobs"`
}

// DataGovernanceJobTarget is the immutable execution snapshot leased to one
// worker. TenantID is deliberately not exposed through the management API.
type DataGovernanceJobTarget struct {
	ID                  uuid.UUID
	TenantID            uuid.UUID
	Kind                DataGovernanceJobKind
	ScopeKind           DataScopeKind
	ScopeRef            string
	DataClasses         []DataClass
	DesiredRegion       string
	ExternalOperationID string
	WorkerID            string
	Attempt             int
	LockedUntil         time.Time
	ReversibleUntil     *time.Time
}

type DataGovernanceArtifact struct {
	JobID           uuid.UUID
	TenantID        uuid.UUID
	Filename        string
	ContentType     string
	KeyVersion      string
	Nonce           []byte
	Ciphertext      []byte
	PlaintextSHA256 string
	PlaintextBytes  int64
	CreatedAt       time.Time
	ExpiresAt       time.Time
}

type DataGovernanceDownload struct {
	Filename    string
	ContentType string
	Payload     []byte
}

var dataRegionPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)

func NormalizeRetentionPolicyInput(input RetentionPolicyInput) (RetentionPolicyInput, bool) {
	input.ScopeRef = strings.TrimSpace(input.ScopeRef)
	input.ChangeReason = strings.TrimSpace(input.ChangeReason)
	if !validDataScope(input.ScopeKind, input.ScopeRef, false) || !validDataClass(input.DataClass, false) || input.RetentionDays < 1 || input.RetentionDays > 3650 || input.ExpectedRevision < 0 || len(input.ChangeReason) < 3 || len(input.ChangeReason) > 500 {
		return RetentionPolicyInput{}, false
	}
	return input, true
}

func NormalizeDataLegalHoldInput(input DataLegalHoldInput) (DataLegalHoldInput, bool) {
	input.ScopeRef = strings.TrimSpace(input.ScopeRef)
	input.Reason = strings.TrimSpace(input.Reason)
	if !validDataScope(input.ScopeKind, input.ScopeRef, false) || !validDataClass(input.DataClass, true) || len(input.Reason) < 3 || len(input.Reason) > 500 {
		return DataLegalHoldInput{}, false
	}
	return input, true
}

func NormalizeDataGovernanceJobInput(input DataGovernanceJobInput) (DataGovernanceJobInput, bool) {
	input.ScopeRef = strings.TrimSpace(input.ScopeRef)
	input.DesiredRegion = strings.TrimSpace(input.DesiredRegion)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.Reason = strings.TrimSpace(input.Reason)
	if !validDataScope(input.ScopeKind, input.ScopeRef, true) || len(input.IdempotencyKey) < 8 || len(input.IdempotencyKey) > 160 || strings.ContainsAny(input.IdempotencyKey, "\r\n\x00") || len(input.Reason) < 3 || len(input.Reason) > 500 {
		return DataGovernanceJobInput{}, false
	}
	seen := make(map[DataClass]struct{}, len(input.DataClasses))
	classes := make([]DataClass, 0, len(input.DataClasses))
	for _, item := range input.DataClasses {
		if !validDataClass(item, false) {
			return DataGovernanceJobInput{}, false
		}
		if _, exists := seen[item]; !exists {
			seen[item] = struct{}{}
			classes = append(classes, item)
		}
	}
	input.DataClasses = classes
	switch input.Kind {
	case DataJobExport:
		if len(input.DataClasses) == 0 || input.DesiredRegion != "" {
			return DataGovernanceJobInput{}, false
		}
	case DataJobDeletion:
		// Destructive scopes stay intentionally narrower than exports until a
		// trusted resolver can prove repository/legal-hold intersection for a
		// review run or arbitrary time range.
		if len(input.DataClasses) == 0 || input.DesiredRegion != "" || (input.ScopeKind != DataScopeTenant && input.ScopeKind != DataScopeRepository) {
			return DataGovernanceJobInput{}, false
		}
		for _, class := range input.DataClasses {
			if class != DataClassRawWebhook && class != DataClassFindings && class != DataClassAudit {
				return DataGovernanceJobInput{}, false
			}
		}
	case DataJobRegionMigration:
		if !dataRegionPattern.MatchString(input.DesiredRegion) || len(input.DataClasses) != 0 || input.ScopeKind != DataScopeTenant {
			return DataGovernanceJobInput{}, false
		}
	default:
		return DataGovernanceJobInput{}, false
	}
	return input, true
}

func NormalizeGovernanceDecisionInput(input GovernanceDecisionInput) (GovernanceDecisionInput, bool) {
	input.Decision = strings.TrimSpace(input.Decision)
	input.Reason = strings.TrimSpace(input.Reason)
	if (input.Decision != "approved" && input.Decision != "rejected") || input.ExpectedRevision < 1 || len(input.Reason) < 3 || len(input.Reason) > 500 {
		return GovernanceDecisionInput{}, false
	}
	return input, true
}

func validDataScope(kind DataScopeKind, ref string, allowExtended bool) bool {
	if strings.ContainsAny(ref, "\r\n\x00") || len(ref) > 240 {
		return false
	}
	if kind == DataScopeTenant {
		return ref == ""
	}
	if kind == DataScopeRepository {
		return ref != ""
	}
	if !allowExtended {
		return false
	}
	if kind == DataScopeReviewRun {
		_, err := uuid.Parse(ref)
		return err == nil
	}
	if kind == DataScopeAuditRange {
		parts := strings.Split(ref, "/")
		if len(parts) != 2 {
			return false
		}
		start, startErr := time.Parse(time.RFC3339, parts[0])
		end, endErr := time.Parse(time.RFC3339, parts[1])
		return startErr == nil && endErr == nil && start.Before(end)
	}
	return false
}

func validDataClass(value DataClass, allowAll bool) bool {
	switch value {
	case DataClassRawWebhook, DataClassFindings, DataClassAudit, DataClassUsage, DataClassOperationalLog:
		return true
	case DataClassAll:
		return allowAll
	default:
		return false
	}
}
