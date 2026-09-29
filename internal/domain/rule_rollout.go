package domain

import (
	"crypto/sha256"
	"encoding/binary"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// RuleRollout keeps promotion state separate from mutable bindings. A rollout
// freezes the identity of the baseline/candidate binding pair at creation; a
// run later records the concrete rule snapshot that it actually executed.
// Shadow never changes provider-visible review behavior. Canary may do so only
// for its deterministic cohort.
type RuleRollout struct {
	ID                         uuid.UUID  `json:"id"`
	TenantID                   uuid.UUID  `json:"tenant_id,omitempty"`
	BaselineBindingID          uuid.UUID  `json:"baseline_binding_id"`
	CandidateBindingID         uuid.UUID  `json:"candidate_binding_id"`
	ApprovedShadowComparisonID *uuid.UUID `json:"approved_shadow_comparison_id,omitempty"`
	Mode                       string     `json:"mode"`
	State                      string     `json:"state"`
	CanaryBasisPoints          int        `json:"canary_basis_points"`
	AutoRollbackFailedRuns     int        `json:"auto_rollback_failed_runs"`
	AutoRollbackWindowMinutes  int        `json:"auto_rollback_window_minutes"`
	AutoRollbackReason         string     `json:"auto_rollback_reason,omitempty"`
	CohortSalt                 uuid.UUID  `json:"cohort_salt"`
	Revision                   int        `json:"revision"`
	CreatedBy                  string     `json:"created_by"`
	CreatedAt                  time.Time  `json:"created_at"`
	UpdatedAt                  time.Time  `json:"updated_at"`
}

type RuleRolloutInput struct {
	BaselineBindingID         uuid.UUID `json:"baseline_binding_id"`
	CandidateBindingID        uuid.UUID `json:"candidate_binding_id"`
	Mode                      string    `json:"mode"`
	CanaryBasisPoints         int       `json:"canary_basis_points"`
	AutoRollbackFailedRuns    int       `json:"auto_rollback_failed_runs"`
	AutoRollbackWindowMinutes int       `json:"auto_rollback_window_minutes"`
}

type RuleRolloutUpdateInput struct {
	State             string `json:"state"`
	CanaryBasisPoints int    `json:"canary_basis_points,omitempty"`
	Revision          int    `json:"revision"`
}

// RuleRolloutComparison is evidence from one provider-silent Shadow replay.
// Counts compare normalized finding fingerprints for the exact source revision;
// they are not a provider check and cannot change merge eligibility.
type RuleRolloutComparison struct {
	ID                    uuid.UUID  `json:"id"`
	RolloutID             uuid.UUID  `json:"rollout_id"`
	BaselineRunID         uuid.UUID  `json:"baseline_run_id"`
	CandidateTestRunID    uuid.UUID  `json:"candidate_test_run_id"`
	BaselineSnapshotID    uuid.UUID  `json:"baseline_snapshot_id"`
	CandidateSnapshotID   uuid.UUID  `json:"candidate_snapshot_id"`
	State                 string     `json:"state"`
	BaselineFindingCount  int        `json:"baseline_finding_count"`
	CandidateFindingCount int        `json:"candidate_finding_count"`
	AddedFindingCount     int        `json:"added_finding_count"`
	RemovedFindingCount   int        `json:"removed_finding_count"`
	MatchedFindingCount   int        `json:"matched_finding_count"`
	ErrorMessage          string     `json:"error_message,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
	CompletedAt           *time.Time `json:"completed_at,omitempty"`
}

func (input RuleRolloutInput) Valid() bool {
	input.Mode = strings.ToLower(strings.TrimSpace(input.Mode))
	if input.BaselineBindingID == uuid.Nil || input.CandidateBindingID == uuid.Nil || input.BaselineBindingID == input.CandidateBindingID {
		return false
	}
	if input.AutoRollbackFailedRuns < 0 || input.AutoRollbackFailedRuns > 10 || (input.AutoRollbackWindowMinutes != 0 && (input.AutoRollbackWindowMinutes < 5 || input.AutoRollbackWindowMinutes > 1440)) {
		return false
	}
	if input.Mode == "shadow" {
		return input.CanaryBasisPoints == 0
	}
	return input.Mode == "canary" && input.CanaryBasisPoints >= 1 && input.CanaryBasisPoints <= 10_000
}

func (input RuleRolloutUpdateInput) Valid() bool {
	input.State = strings.ToLower(strings.TrimSpace(input.State))
	if input.Revision < 1 || (input.State != "active" && input.State != "paused" && input.State != "promoted" && input.State != "rolled_back") {
		return false
	}
	return input.CanaryBasisPoints == 0 || (input.State == "active" && (input.CanaryBasisPoints == 500 || input.CanaryBasisPoints == 2500 || input.CanaryBasisPoints == 10000))
}

func (rollout RuleRollout) Active() bool { return rollout.State == "active" }

// CohortBucket is deliberately stable across retries and new heads. Including
// a head SHA or a run ID would make one pull request move between baseline and
// candidate while it is being updated, which invalidates rollout evidence.
func CohortBucket(tenantID uuid.UUID, provider Provider, apiBaseURL, repository string, reviewNumber int, salt uuid.UUID) (int, bool) {
	apiBaseURL = strings.TrimSuffix(strings.TrimSpace(apiBaseURL), "/")
	repository = strings.Trim(strings.TrimSpace(repository), "/")
	if tenantID == uuid.Nil || !provider.Valid() || apiBaseURL == "" || repository == "" || reviewNumber < 1 || salt == uuid.Nil {
		return 0, false
	}
	value := strings.Join([]string{tenantID.String(), string(provider), apiBaseURL, repository, strconv.Itoa(reviewNumber), salt.String()}, "\x00")
	digest := sha256.Sum256([]byte(value))
	return int(binary.BigEndian.Uint64(digest[:8]) % 10_000), true
}

func (rollout RuleRollout) IncludesCohort(tenantID uuid.UUID, provider Provider, apiBaseURL, repository string, reviewNumber int) bool {
	if !rollout.Active() || rollout.Mode != "canary" || rollout.CanaryBasisPoints < 1 || rollout.CanaryBasisPoints > 10_000 {
		return false
	}
	bucket, valid := CohortBucket(tenantID, provider, apiBaseURL, repository, reviewNumber, rollout.CohortSalt)
	return valid && bucket < rollout.CanaryBasisPoints
}
