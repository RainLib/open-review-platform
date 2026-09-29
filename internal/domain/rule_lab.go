package domain

import (
	"time"

	"github.com/google/uuid"
)

type RuleTestRunInput struct {
	SourceRunID uuid.UUID `json:"source_run_id"`
	Precedence  int       `json:"precedence"`
}

// RuleTestRun is an isolated OCR execution. It intentionally has no provider
// publication identity or merge-gate fields: results are evidence for policy
// authors, not a decision on the source pull request.
type RuleTestRun struct {
	ID                uuid.UUID  `json:"id"`
	TenantID          uuid.UUID  `json:"tenant_id,omitempty"`
	RuleSetID         uuid.UUID  `json:"rule_set_id"`
	RuleSetName       string     `json:"rule_set_name"`
	RuleVersionID     uuid.UUID  `json:"rule_version_id"`
	RuleVersion       int        `json:"rule_version"`
	SourceRunID       uuid.UUID  `json:"source_run_id"`
	SnapshotID        uuid.UUID  `json:"snapshot_id"`
	SnapshotSHA256    string     `json:"snapshot_sha256"`
	State             string     `json:"state"`
	RequestedBy       string     `json:"requested_by"`
	Provider          Provider   `json:"provider"`
	APIBaseURL        string     `json:"api_base_url,omitempty"`
	Repository        string     `json:"repository"`
	ReviewNumber      int        `json:"review_number"`
	BaseSHA           string     `json:"base_sha"`
	HeadSHA           string     `json:"head_sha"`
	EngineVersion     string     `json:"engine_version,omitempty"`
	Attempts          int        `json:"attempts"`
	SelectedPathCount int        `json:"selected_path_count"`
	DeferredPathCount int        `json:"deferred_path_count"`
	FindingCount      int        `json:"finding_count"`
	DurationMS        int64      `json:"duration_ms"`
	ErrorMessage      string     `json:"error_message,omitempty"`
	Findings          []Finding  `json:"findings"`
	CreatedAt         time.Time  `json:"created_at"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	FinishedAt        *time.Time `json:"finished_at,omitempty"`
}

type RuleTestExecution struct {
	Run      RuleTestRun
	Job      ReviewJob
	Snapshot RuleSnapshot
}

type RuleTestCompletion struct {
	EngineVersion     string
	SelectedPathCount int
	DeferredPathCount int
	DurationMS        int64
	Findings          []Finding
}

func (r RuleTestRun) Terminal() bool {
	return r.State == "completed" || r.State == "failed" || r.State == "cancelled"
}
