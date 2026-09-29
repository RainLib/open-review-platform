package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) GetUsageDashboard(ctx context.Context, actor, tenantSlug string, limit int) (domain.UsageDashboard, error) {
	if limit < 1 || limit > 100 {
		return domain.UsageDashboard{}, fmt.Errorf("usage ledger limit must be from 1 to 100")
	}
	tenantID, role, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.UsageDashboard{}, err
	}
	if role != "owner" && role != "admin" && role != "billing_viewer" {
		return domain.UsageDashboard{}, ErrForbidden
	}

	var dashboard domain.UsageDashboard
	var updatedBy string
	err = s.pool.QueryRow(ctx, `
		SELECT date_trunc('month', now(), 'UTC'),
		       date_trunc('month', now(), 'UTC') + interval '1 month',
		       COALESCE(e.monthly_review_limit, 0), COALESCE(e.soft_warning_percent, 80),
		       COALESCE(e.updated_by, ''), COALESCE(e.updated_at, t.created_at)
		FROM tenants t
		LEFT JOIN tenant_entitlements e ON e.tenant_id = t.id
		WHERE t.id = $1`, tenantID).Scan(
		&dashboard.PeriodStart, &dashboard.PeriodEnd,
		&dashboard.Entitlement.MonthlyReviewLimit, &dashboard.Entitlement.SoftWarningPercent,
		&updatedBy, &dashboard.Entitlement.UpdatedAt,
	)
	if err != nil {
		return domain.UsageDashboard{}, fmt.Errorf("load usage entitlement: %w", err)
	}
	dashboard.Entitlement.UpdatedBy = updatedBy
	dashboard.CanManage = role == "owner" || role == "admin"

	if err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(settled_quantity) FILTER (WHERE state = 'settled'), 0),
		       COALESCE(SUM(reserved_quantity) FILTER (WHERE state = 'reserved'), 0)
		FROM usage_reservations
		WHERE tenant_id = $1 AND period_start = date_trunc('month', now() AT TIME ZONE 'UTC')::date`, tenantID).
		Scan(&dashboard.Settled, &dashboard.Reserved); err != nil {
		return domain.UsageDashboard{}, fmt.Errorf("summarize usage reservations: %w", err)
	}
	if dashboard.Entitlement.MonthlyReviewLimit > 0 {
		remaining := dashboard.Entitlement.MonthlyReviewLimit - dashboard.Settled - dashboard.Reserved
		if remaining < 0 {
			remaining = 0
		}
		dashboard.Remaining = &remaining
		consumed := dashboard.Settled + dashboard.Reserved
		dashboard.Warning = consumed*100 >= dashboard.Entitlement.MonthlyReviewLimit*int64(dashboard.Entitlement.SoftWarningPercent)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT repository,
		       COALESCE(SUM(settled_quantity) FILTER (WHERE state = 'settled'), 0),
		       COALESCE(SUM(reserved_quantity) FILTER (WHERE state = 'reserved'), 0)
		FROM usage_reservations
		WHERE tenant_id = $1 AND period_start = date_trunc('month', now() AT TIME ZONE 'UTC')::date
		GROUP BY repository
		ORDER BY SUM(CASE WHEN state = 'settled' THEN settled_quantity ELSE reserved_quantity END) DESC, repository`, tenantID)
	if err != nil {
		return domain.UsageDashboard{}, fmt.Errorf("group usage by repository: %w", err)
	}
	for rows.Next() {
		var item domain.RepositoryUsage
		if err := rows.Scan(&item.Repository, &item.Settled, &item.Reserved); err != nil {
			rows.Close()
			return domain.UsageDashboard{}, fmt.Errorf("scan repository usage: %w", err)
		}
		dashboard.Repositories = append(dashboard.Repositories, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return domain.UsageDashboard{}, fmt.Errorf("iterate repository usage: %w", err)
	}
	rows.Close()

	rows, err = s.pool.Query(ctx, `
		SELECT id, run_id, repository, metric, quantity, unit, event_kind, metadata, occurred_at
		FROM usage_ledger
		WHERE tenant_id = $1
		ORDER BY occurred_at DESC, id DESC
		LIMIT $2`, tenantID, limit)
	if err != nil {
		return domain.UsageDashboard{}, fmt.Errorf("list usage ledger: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item domain.UsageLedgerEntry
		var metadata []byte
		if err := rows.Scan(&item.ID, &item.RunID, &item.Repository, &item.Metric, &item.Quantity, &item.Unit, &item.EventKind, &metadata, &item.OccurredAt); err != nil {
			return domain.UsageDashboard{}, fmt.Errorf("scan usage ledger: %w", err)
		}
		if err := json.Unmarshal(metadata, &item.Metadata); err != nil {
			return domain.UsageDashboard{}, fmt.Errorf("decode usage ledger metadata: %w", err)
		}
		dashboard.Ledger = append(dashboard.Ledger, item)
	}
	if err := rows.Err(); err != nil {
		return domain.UsageDashboard{}, fmt.Errorf("iterate usage ledger: %w", err)
	}
	rows.Close()
	dashboard.Reconciliation, err = s.usageReconciliationStatus(ctx, tenantID, dashboard.PeriodStart)
	if err != nil {
		return domain.UsageDashboard{}, err
	}
	if dashboard.Repositories == nil {
		dashboard.Repositories = []domain.RepositoryUsage{}
	}
	if dashboard.Ledger == nil {
		dashboard.Ledger = []domain.UsageLedgerEntry{}
	}
	return dashboard, nil
}

func (s *PostgresStore) ExportUsage(ctx context.Context, actor, tenantSlug string, periodStart time.Time) (domain.UsageExport, error) {
	tenantID, role, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.UsageExport{}, err
	}
	if role != "owner" && role != "admin" && role != "billing_viewer" {
		return domain.UsageExport{}, ErrForbidden
	}
	periodStart, periodEnd := usagePeriod(periodStart)

	var export domain.UsageExport
	export.PeriodStart = periodStart
	export.PeriodEnd = periodEnd
	var currentPeriod time.Time
	err = s.pool.QueryRow(ctx, `
		SELECT now(),
		       date_trunc('month', now(), 'UTC'),
		       COALESCE(entitlement.monthly_review_limit, 0),
		       COALESCE(entitlement.soft_warning_percent, 80)
		FROM tenants tenant
		LEFT JOIN tenant_entitlements entitlement ON entitlement.tenant_id=tenant.id
		WHERE tenant.id=$1`, tenantID).Scan(
		&export.GeneratedAt, &currentPeriod, &export.MonthlyReviewLimit, &export.SoftWarningPercent,
	)
	if err != nil {
		return domain.UsageExport{}, fmt.Errorf("load usage export boundary: %w", err)
	}
	if periodStart.After(currentPeriod) {
		return domain.UsageExport{}, ErrInvalidUsageExport
	}

	rows, err := s.pool.Query(ctx, `
		SELECT repository,
		       COALESCE(SUM(settled_quantity) FILTER (WHERE state='settled'), 0),
		       COALESCE(SUM(reserved_quantity) FILTER (WHERE state='reserved'), 0),
		       COALESCE(SUM(reserved_quantity) FILTER (WHERE state='released'), 0)
		FROM usage_reservations
		WHERE tenant_id=$1 AND period_start=$2
		GROUP BY repository
		ORDER BY repository`, tenantID, periodStart)
	if err != nil {
		return domain.UsageExport{}, fmt.Errorf("query usage export: %w", err)
	}
	defer rows.Close()
	export.Repositories = make([]domain.UsageExportRow, 0)
	for rows.Next() {
		var item domain.UsageExportRow
		if err := rows.Scan(&item.Repository, &item.Settled, &item.Reserved, &item.Released); err != nil {
			return domain.UsageExport{}, fmt.Errorf("scan usage export row: %w", err)
		}
		export.Settled += item.Settled
		export.Reserved += item.Reserved
		export.Released += item.Released
		export.Repositories = append(export.Repositories, item)
	}
	if err := rows.Err(); err != nil {
		return domain.UsageExport{}, fmt.Errorf("iterate usage export rows: %w", err)
	}
	return export, nil
}

func (s *PostgresStore) UpdateUsageEntitlement(ctx context.Context, actor, tenantSlug string, input domain.UsageEntitlementInput) (domain.UsageEntitlement, error) {
	if input.MonthlyReviewLimit < 0 || input.SoftWarningPercent < 1 || input.SoftWarningPercent > 100 {
		return domain.UsageEntitlement{}, ErrInvalidUsageEntitlement
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.UsageEntitlement{}, fmt.Errorf("begin usage entitlement update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.UsageEntitlement{}, err
	}
	if role != "owner" && role != "admin" {
		return domain.UsageEntitlement{}, ErrForbidden
	}
	var entitlement domain.UsageEntitlement
	err = tx.QueryRow(ctx, `
		INSERT INTO tenant_entitlements (tenant_id, monthly_review_limit, soft_warning_percent, updated_by)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (tenant_id) DO UPDATE
		SET monthly_review_limit = EXCLUDED.monthly_review_limit,
		    soft_warning_percent = EXCLUDED.soft_warning_percent,
		    updated_by = EXCLUDED.updated_by,
		    updated_at = now()
		RETURNING monthly_review_limit, soft_warning_percent, updated_by, updated_at`,
		tenantID, input.MonthlyReviewLimit, input.SoftWarningPercent, actor).
		Scan(&entitlement.MonthlyReviewLimit, &entitlement.SoftWarningPercent, &entitlement.UpdatedBy, &entitlement.UpdatedAt)
	if err != nil {
		return domain.UsageEntitlement{}, fmt.Errorf("upsert usage entitlement: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata)
		VALUES ($1, $2, 'usage.entitlement.updated', $3, jsonb_build_object(
			'monthly_review_limit', $4::bigint, 'soft_warning_percent', $5::integer
		))`, tenantID, actor, tenantSlug, input.MonthlyReviewLimit, input.SoftWarningPercent); err != nil {
		return domain.UsageEntitlement{}, fmt.Errorf("audit usage entitlement update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.UsageEntitlement{}, fmt.Errorf("commit usage entitlement update: %w", err)
	}
	return entitlement, nil
}

func (s *PostgresStore) usageReconciliationStatus(ctx context.Context, tenantID uuid.UUID, periodStart time.Time) (domain.UsageReconciliationStatus, error) {
	periodStart, periodEnd := usagePeriod(periodStart)
	status := domain.UsageReconciliationStatus{PeriodStart: periodStart, PeriodEnd: periodEnd, State: "clean"}
	err := s.pool.QueryRow(ctx, `
		WITH expected AS (
			SELECT run.id,
			       CASE WHEN run.state='completed' THEN 'settled'
			            WHEN run.state IN ('failed','cancelled','superseded','needs_attention') THEN 'released'
			            ELSE 'reserved' END AS expected_state,
			       date_trunc('month', run.created_at AT TIME ZONE 'UTC')::date AS expected_period
			FROM review_runs run
			JOIN review_requests request ON request.id=run.request_id
			WHERE request.tenant_id=$1 AND run.created_at >= $2 AND run.created_at < $3
			  AND COALESCE(run.failure_code,'') <> 'quota_exceeded'
		), observed AS (
			SELECT expected.id, expected.expected_state, expected.expected_period,
			       reservation.id AS reservation_id, reservation.state AS reservation_state,
			       reservation.settled_quantity, reservation.reserved_quantity, reservation.period_start,
			       EXISTS (
				   SELECT 1 FROM usage_ledger ledger
				   WHERE ledger.run_id=expected.id AND (
				       (expected.expected_state='reserved' AND ledger.event_kind='reserve') OR
				       (expected.expected_state='settled' AND ledger.event_kind='settle') OR
				       (expected.expected_state='released' AND ledger.event_kind='release') OR
				       (ledger.event_kind='adjustment' AND ledger.metadata->>'target_state'=expected.expected_state)
				   )
			   ) AS has_ledger_proof
			FROM expected
			LEFT JOIN usage_reservations reservation ON reservation.run_id=expected.id
		)
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE reservation_id IS NULL),
		       COUNT(*) FILTER (WHERE reservation_id IS NOT NULL AND (
		           reservation_state <> expected_state OR period_start <> expected_period OR reserved_quantity <> 1 OR
		           (expected_state='settled' AND settled_quantity <> reserved_quantity) OR
		           (expected_state<>'settled' AND settled_quantity <> 0)
		       )),
		       COUNT(*) FILTER (WHERE NOT has_ledger_proof),
		       COUNT(*) FILTER (WHERE reservation_id IS NULL OR NOT has_ledger_proof OR (
		           reservation_id IS NOT NULL AND (
		               reservation_state <> expected_state OR period_start <> expected_period OR reserved_quantity <> 1 OR
		               (expected_state='settled' AND settled_quantity <> reserved_quantity) OR
		               (expected_state<>'settled' AND settled_quantity <> 0)
		           )
		       ))
		FROM observed`, tenantID, periodStart, periodEnd).Scan(
		&status.ScannedRuns, &status.MissingReservations, &status.StateMismatches,
		&status.MissingLedgerProofs, &status.DriftedRuns,
	)
	if err != nil {
		return domain.UsageReconciliationStatus{}, fmt.Errorf("inspect usage reconciliation drift: %w", err)
	}
	if status.DriftedRuns > 0 {
		status.State = "drift"
	}
	var lastAt time.Time
	err = s.pool.QueryRow(ctx, `
		SELECT actor_subject,created_at FROM audit_events
		WHERE tenant_id=$1 AND action='usage.reconciled' AND metadata->>'period_start'=$2
		ORDER BY created_at DESC,id DESC LIMIT 1`, tenantID, periodStart.Format("2006-01-02")).Scan(&status.LastReconciledBy, &lastAt)
	if err == nil {
		status.LastReconciledAt = &lastAt
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return domain.UsageReconciliationStatus{}, fmt.Errorf("load latest usage reconciliation: %w", err)
	}
	return status, nil
}

func (s *PostgresStore) ReconcileUsage(ctx context.Context, actor, tenantSlug string, input domain.UsageReconciliationInput) (domain.UsageReconciliationReport, error) {
	input, periodStart, valid := domain.NormalizeUsageReconciliationInput(input)
	if !valid {
		return domain.UsageReconciliationReport{}, ErrInvalidUsageReconciliation
	}
	currentPeriod, _ := usagePeriod(time.Now().UTC())
	if periodStart.After(currentPeriod) {
		return domain.UsageReconciliationReport{}, ErrInvalidUsageReconciliation
	}
	periodEnd := periodStart.AddDate(0, 1, 0)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.UsageReconciliationReport{}, fmt.Errorf("begin usage reconciliation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.UsageReconciliationReport{}, err
	}
	if role != "owner" && role != "admin" {
		return domain.UsageReconciliationReport{}, ErrForbidden
	}
	if _, err := tx.Exec(ctx, `SELECT id FROM tenants WHERE id=$1 FOR UPDATE`, tenantID); err != nil {
		return domain.UsageReconciliationReport{}, fmt.Errorf("lock usage reconciliation boundary: %w", err)
	}
	var replay domain.UsageReconciliationReport
	var replayPeriod, replayReason string
	err = tx.QueryRow(ctx, `
		SELECT metadata->>'period_start',metadata->>'reason',actor_subject,created_at,
		       (metadata->>'scanned_runs')::bigint,(metadata->>'drifted_runs')::bigint,
		       (metadata->>'missing_reservations')::bigint,(metadata->>'state_mismatches')::bigint,
		       (metadata->>'missing_ledger_proofs')::bigint,(metadata->>'repaired_runs')::bigint,
		       (metadata->>'adjustments')::bigint
		FROM audit_events
		WHERE tenant_id=$1 AND action='usage.reconciled' AND metadata->>'idempotency_key'=$2
		ORDER BY created_at DESC,id DESC LIMIT 1`, tenantID, input.IdempotencyKey).Scan(
		&replayPeriod, &replayReason, &replay.ReconciledBy, &replay.ReconciledAt,
		&replay.ScannedRuns, &replay.DriftedRuns, &replay.MissingReservations,
		&replay.StateMismatches, &replay.MissingLedgerProofs, &replay.RepairedRuns, &replay.Adjustments,
	)
	if err == nil {
		if replayPeriod != input.PeriodStart || replayReason != input.Reason {
			return domain.UsageReconciliationReport{}, ErrConflict
		}
		replay.PeriodStart = periodStart
		replay.PeriodEnd = periodEnd
		replay.State = "clean"
		replay.LastReconciledAt = &replay.ReconciledAt
		replay.LastReconciledBy = replay.ReconciledBy
		return replay, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.UsageReconciliationReport{}, fmt.Errorf("load idempotent usage reconciliation: %w", err)
	}

	rows, err := tx.Query(ctx, `
		WITH expected AS (
			SELECT run.id,request.repository,run.created_at,
			       CASE WHEN run.state='completed' THEN 'settled'
			            WHEN run.state IN ('failed','cancelled','superseded','needs_attention') THEN 'released'
			            ELSE 'reserved' END AS expected_state
			FROM review_runs run
			JOIN review_requests request ON request.id=run.request_id
			WHERE request.tenant_id=$1 AND run.created_at >= $2 AND run.created_at < $3
			  AND COALESCE(run.failure_code,'') <> 'quota_exceeded'
			ORDER BY run.created_at,run.id
			FOR UPDATE OF run
		)
		SELECT expected.id,expected.repository,expected.created_at,expected.expected_state,
		       reservation.id,reservation.state,reservation.reserved_quantity,reservation.settled_quantity,reservation.period_start,
		       EXISTS (
			   SELECT 1 FROM usage_ledger ledger
			   WHERE ledger.run_id=expected.id AND (
			       (expected.expected_state='reserved' AND ledger.event_kind='reserve') OR
			       (expected.expected_state='settled' AND ledger.event_kind='settle') OR
			       (expected.expected_state='released' AND ledger.event_kind='release') OR
			       (ledger.event_kind='adjustment' AND ledger.metadata->>'target_state'=expected.expected_state)
			   )
		   )
		FROM expected
		LEFT JOIN usage_reservations reservation ON reservation.run_id=expected.id`, tenantID, periodStart, periodEnd)
	if err != nil {
		return domain.UsageReconciliationReport{}, fmt.Errorf("select usage reconciliation targets: %w", err)
	}
	type reconciliationTarget struct {
		runID          uuid.UUID
		repository     string
		createdAt      time.Time
		expectedState  string
		reservationID  *uuid.UUID
		state          *string
		reserved       *int64
		settled        *int64
		period         *time.Time
		hasLedgerProof bool
	}
	targets := make([]reconciliationTarget, 0)
	for rows.Next() {
		var target reconciliationTarget
		if err := rows.Scan(&target.runID, &target.repository, &target.createdAt, &target.expectedState,
			&target.reservationID, &target.state, &target.reserved, &target.settled, &target.period, &target.hasLedgerProof); err != nil {
			rows.Close()
			return domain.UsageReconciliationReport{}, fmt.Errorf("scan usage reconciliation target: %w", err)
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return domain.UsageReconciliationReport{}, fmt.Errorf("iterate usage reconciliation targets: %w", err)
	}
	rows.Close()
	var reconciledAt time.Time
	if err := tx.QueryRow(ctx, `SELECT now()`).Scan(&reconciledAt); err != nil {
		return domain.UsageReconciliationReport{}, fmt.Errorf("read usage reconciliation time: %w", err)
	}

	report := domain.UsageReconciliationReport{
		UsageReconciliationStatus: domain.UsageReconciliationStatus{
			State: "clean", PeriodStart: periodStart, PeriodEnd: periodEnd, ScannedRuns: int64(len(targets)),
		},
		ReconciledAt: reconciledAt.UTC(), ReconciledBy: actor,
	}
	for _, target := range targets {
		expectedPeriod, _ := usagePeriod(target.createdAt)
		missing := target.reservationID == nil
		mismatch := false
		quantity := int64(1)
		if !missing {
			mismatch = *target.state != target.expectedState || !target.period.Equal(expectedPeriod) || *target.reserved != quantity ||
				(target.expectedState == "settled" && *target.settled != quantity) ||
				(target.expectedState != "settled" && *target.settled != 0)
		}
		if missing {
			report.MissingReservations++
		}
		if mismatch {
			report.StateMismatches++
		}
		if !target.hasLedgerProof {
			report.MissingLedgerProofs++
		}
		if !missing && !mismatch && target.hasLedgerProof {
			continue
		}
		report.DriftedRuns++
		settledQuantity := int64(0)
		var settledAt any
		if target.expectedState == "settled" {
			settledQuantity = quantity
			settledAt = report.ReconciledAt
		} else if target.expectedState == "released" {
			settledAt = report.ReconciledAt
		}
		if missing {
			if _, err := tx.Exec(ctx, `
				INSERT INTO usage_reservations (
					tenant_id,run_id,repository,metric,reserved_quantity,settled_quantity,state,period_start,settled_at
				) VALUES ($1,$2,$3,'review_run',$4,$5,$6,$7,$8)`, tenantID, target.runID, target.repository,
				quantity, settledQuantity, target.expectedState, expectedPeriod, settledAt); err != nil {
				return domain.UsageReconciliationReport{}, fmt.Errorf("restore usage reservation: %w", err)
			}
		} else if mismatch {
			if _, err := tx.Exec(ctx, `
				UPDATE usage_reservations
				SET state=$2,reserved_quantity=$3,settled_quantity=$4,period_start=$5,settled_at=$6
				WHERE id=$1`, *target.reservationID, target.expectedState, quantity, settledQuantity, expectedPeriod, settledAt); err != nil {
				return domain.UsageReconciliationReport{}, fmt.Errorf("repair usage reservation: %w", err)
			}
		}
		result, err := tx.Exec(ctx, `
			INSERT INTO usage_ledger (
				tenant_id,run_id,repository,metric,quantity,unit,event_kind,idempotency_key,metadata,occurred_at
			) VALUES ($1,$2,$3,'review_run',$4,'run','adjustment',$5,
			          jsonb_build_object(
			              'target_state',$6::text,'target_reserved_quantity',$7::bigint,'target_settled_quantity',$8::bigint,
			              'missing_reservation',$9::boolean,'state_mismatch',$10::boolean,'missing_ledger_proof',$11::boolean,
			              'previous_state',$12::text,'previous_reserved_quantity',$13::bigint,
			              'previous_settled_quantity',$14::bigint,'previous_period_start',$15::date
			          ),$16)
			ON CONFLICT (idempotency_key) DO NOTHING`, tenantID, target.runID, target.repository, quantity,
			"usage:reconcile:"+input.IdempotencyKey+":"+target.runID.String(), target.expectedState,
			quantity, settledQuantity, missing, mismatch, !target.hasLedgerProof,
			target.state, target.reserved, target.settled, target.period, report.ReconciledAt)
		if err != nil {
			return domain.UsageReconciliationReport{}, fmt.Errorf("append usage reconciliation evidence: %w", err)
		}
		if result.RowsAffected() > 0 {
			report.Adjustments++
		}
		report.RepairedRuns++
	}
	report.LastReconciledAt = &report.ReconciledAt
	report.LastReconciledBy = report.ReconciledBy
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata)
		VALUES ($1,$2,'usage.reconciled',$3,jsonb_build_object(
			'period_start',$4::text,'reason',$5::text,'idempotency_key',$6::text,
			'scanned_runs',$7::bigint,'drifted_runs',$8::bigint,'missing_reservations',$9::bigint,
			'state_mismatches',$10::bigint,'missing_ledger_proofs',$11::bigint,
			'repaired_runs',$12::bigint,'adjustments',$13::bigint
		))`, tenantID, actor, periodStart.Format("2006-01-02"), periodStart.Format("2006-01-02"), input.Reason,
		input.IdempotencyKey, report.ScannedRuns, report.DriftedRuns, report.MissingReservations,
		report.StateMismatches, report.MissingLedgerProofs, report.RepairedRuns, report.Adjustments); err != nil {
		return domain.UsageReconciliationReport{}, fmt.Errorf("audit usage reconciliation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.UsageReconciliationReport{}, fmt.Errorf("commit usage reconciliation: %w", err)
	}
	return report, nil
}

// reserveReviewUsage serializes admission per tenant. This deliberately favors
// a correct hard limit over approximate counters; a later sharded counter can
// retain the same immutable reservation and ledger contract.
func reserveReviewUsage(ctx context.Context, tx pgx.Tx, tenantID, runID uuid.UUID, repository string) error {
	if _, err := tx.Exec(ctx, `SELECT id FROM tenants WHERE id = $1 FOR UPDATE`, tenantID); err != nil {
		return fmt.Errorf("lock tenant usage admission: %w", err)
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM usage_reservations WHERE run_id = $1)`, runID).Scan(&exists); err != nil {
		return fmt.Errorf("check usage reservation: %w", err)
	}
	if exists {
		return nil
	}
	var hardLimit int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT monthly_review_limit FROM tenant_entitlements WHERE tenant_id = $1), 0)`, tenantID).Scan(&hardLimit); err != nil {
		return fmt.Errorf("load usage hard limit: %w", err)
	}
	if hardLimit > 0 {
		var committed int64
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(CASE WHEN state = 'reserved' THEN reserved_quantity WHEN state = 'settled' THEN settled_quantity ELSE 0 END), 0)
			FROM usage_reservations
			WHERE tenant_id = $1 AND period_start = date_trunc('month', now() AT TIME ZONE 'UTC')::date`, tenantID).Scan(&committed); err != nil {
			return fmt.Errorf("sum committed usage: %w", err)
		}
		if committed >= hardLimit {
			return ErrQuotaExceeded
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO usage_reservations (tenant_id, run_id, repository, metric, reserved_quantity, state, period_start)
		VALUES ($1, $2, $3, 'review_run', 1, 'reserved', date_trunc('month', now() AT TIME ZONE 'UTC')::date)`, tenantID, runID, repository); err != nil {
		return fmt.Errorf("create usage reservation: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO usage_ledger (tenant_id, run_id, repository, metric, quantity, unit, event_kind, idempotency_key)
		VALUES ($1, $2, $3, 'review_run', 1, 'run', 'reserve', $4)
		ON CONFLICT (idempotency_key) DO NOTHING`, tenantID, runID, repository, "run:"+runID.String()+":reserve"); err != nil {
		return fmt.Errorf("record usage reservation ledger: %w", err)
	}
	return nil
}

func finalizeReviewUsage(ctx context.Context, tx pgx.Tx, runID uuid.UUID, completed bool) error {
	state, eventKind := "released", "release"
	if completed {
		state, eventKind = "settled", "settle"
	}
	var tenantID uuid.UUID
	var repository string
	var quantity int64
	err := tx.QueryRow(ctx, `
		UPDATE usage_reservations
		SET state = $2, settled_quantity = CASE WHEN $2 = 'settled' THEN reserved_quantity ELSE 0 END,
		    settled_at = now()
		WHERE run_id = $1 AND state = 'reserved'
		RETURNING tenant_id, repository, reserved_quantity`, runID, state).Scan(&tenantID, &repository, &quantity)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("finalize usage reservation: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO usage_ledger (tenant_id, run_id, repository, metric, quantity, unit, event_kind, idempotency_key, metadata)
		VALUES ($1, $2, $3, 'review_run', $4, 'run', $5, $6, jsonb_build_object('reservation_state', $7::text))
		ON CONFLICT (idempotency_key) DO NOTHING`, tenantID, runID, repository, quantity, eventKind,
		"run:"+runID.String()+":"+eventKind, state); err != nil {
		return fmt.Errorf("record usage finalization ledger: %w", err)
	}
	return nil
}

func usagePeriod(now time.Time) (time.Time, time.Time) {
	start := time.Date(now.UTC().Year(), now.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	return start, start.AddDate(0, 1, 0)
}
