package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

func NormalizeUsageReconciliationInput(input UsageReconciliationInput) (UsageReconciliationInput, time.Time, bool) {
	input.PeriodStart = strings.TrimSpace(input.PeriodStart)
	input.Reason = strings.TrimSpace(input.Reason)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	periodStart, err := time.Parse("2006-01-02", input.PeriodStart)
	if err != nil || periodStart.Day() != 1 || len(input.Reason) < 3 || len(input.Reason) > 500 || len(input.IdempotencyKey) < 8 || len(input.IdempotencyKey) > 160 || strings.ContainsAny(input.IdempotencyKey, "\r\n\x00") {
		return UsageReconciliationInput{}, time.Time{}, false
	}
	return input, periodStart.UTC(), true
}

func NormalizeUsageExportPeriod(value string, now time.Time) (time.Time, bool) {
	value = strings.TrimSpace(value)
	periodStart, err := time.Parse("2006-01-02", value)
	if err != nil || periodStart.Day() != 1 {
		return time.Time{}, false
	}
	periodStart = periodStart.UTC()
	now = now.UTC()
	currentPeriod := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	if periodStart.After(currentPeriod) {
		return time.Time{}, false
	}
	return periodStart, true
}

type UsageEntitlementInput struct {
	MonthlyReviewLimit int64 `json:"monthly_review_limit"`
	SoftWarningPercent int   `json:"soft_warning_percent"`
}

type UsageEntitlement struct {
	MonthlyReviewLimit int64     `json:"monthly_review_limit"`
	SoftWarningPercent int       `json:"soft_warning_percent"`
	UpdatedBy          string    `json:"updated_by,omitempty"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type UsageReconciliationInput struct {
	PeriodStart    string `json:"period_start"`
	Reason         string `json:"reason"`
	IdempotencyKey string `json:"idempotency_key"`
}

type UsageReconciliationStatus struct {
	State               string     `json:"state"`
	PeriodStart         time.Time  `json:"period_start"`
	PeriodEnd           time.Time  `json:"period_end"`
	ScannedRuns         int64      `json:"scanned_runs"`
	DriftedRuns         int64      `json:"drifted_runs"`
	MissingReservations int64      `json:"missing_reservations"`
	StateMismatches     int64      `json:"state_mismatches"`
	MissingLedgerProofs int64      `json:"missing_ledger_proofs"`
	LastReconciledAt    *time.Time `json:"last_reconciled_at,omitempty"`
	LastReconciledBy    string     `json:"last_reconciled_by,omitempty"`
}

type UsageReconciliationReport struct {
	UsageReconciliationStatus
	RepairedRuns int64     `json:"repaired_runs"`
	Adjustments  int64     `json:"adjustments"`
	ReconciledAt time.Time `json:"reconciled_at"`
	ReconciledBy string    `json:"reconciled_by"`
}

type UsageLedgerEntry struct {
	ID         uuid.UUID      `json:"id"`
	RunID      *uuid.UUID     `json:"run_id,omitempty"`
	Repository string         `json:"repository"`
	Metric     string         `json:"metric"`
	Quantity   int64          `json:"quantity"`
	Unit       string         `json:"unit"`
	EventKind  string         `json:"event_kind"`
	Metadata   map[string]any `json:"metadata"`
	OccurredAt time.Time      `json:"occurred_at"`
}

type RepositoryUsage struct {
	Repository string `json:"repository"`
	Settled    int64  `json:"settled"`
	Reserved   int64  `json:"reserved"`
}

type UsageExportRow struct {
	Repository string `json:"repository"`
	Settled    int64  `json:"settled"`
	Reserved   int64  `json:"reserved"`
	Released   int64  `json:"released"`
}

type UsageExport struct {
	PeriodStart        time.Time        `json:"period_start"`
	PeriodEnd          time.Time        `json:"period_end"`
	GeneratedAt        time.Time        `json:"generated_at"`
	MonthlyReviewLimit int64            `json:"monthly_review_limit"`
	SoftWarningPercent int              `json:"soft_warning_percent"`
	Settled            int64            `json:"settled"`
	Reserved           int64            `json:"reserved"`
	Released           int64            `json:"released"`
	Repositories       []UsageExportRow `json:"repositories"`
}

type UsageDashboard struct {
	PeriodStart    time.Time                 `json:"period_start"`
	PeriodEnd      time.Time                 `json:"period_end"`
	Entitlement    UsageEntitlement          `json:"entitlement"`
	Settled        int64                     `json:"settled"`
	Reserved       int64                     `json:"reserved"`
	Remaining      *int64                    `json:"remaining,omitempty"`
	Warning        bool                      `json:"warning"`
	CanManage      bool                      `json:"can_manage"`
	Reconciliation UsageReconciliationStatus `json:"reconciliation"`
	Repositories   []RepositoryUsage         `json:"repositories"`
	Ledger         []UsageLedgerEntry        `json:"ledger"`
}
