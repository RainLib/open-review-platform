package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestUsageReservationSettlementAndHardLimit(t *testing.T) {
	databaseURL := os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("OPEN_REVIEW_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	postgres, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer postgres.Close()

	tenantID := uuid.New()
	installationID := uuid.New()
	tenantSlug := "usage-integration-" + tenantID.String()[:8]
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO tenants (id, slug, name) VALUES ($1, $2, 'Usage Integration')`, tenantID, tenantSlug)
	batch.Queue(`INSERT INTO memberships (tenant_id, subject, role) VALUES ($1, 'owner', 'owner'), ($1, 'billing', 'billing_viewer'), ($1, 'viewer', 'viewer')`, tenantID)
	batch.Queue(`INSERT INTO tenant_entitlements (tenant_id, monthly_review_limit, soft_warning_percent, updated_by) VALUES ($1, 1, 80, 'owner')`, tenantID)
	batch.Queue(`INSERT INTO provider_installations (id, tenant_id, provider, external_id, repository_scope, api_base_url, credential_ref) VALUES ($1, $2, 'github', $3, 'RainLib/*', 'https://api.github.com', 'secret://usage')`, installationID, tenantID, "usage-installation-"+tenantID.String())
	if err := postgres.pool.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatalf("seed usage tenant: %v", err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenantID) }()

	firstRun := seedUsageRun(t, ctx, postgres, tenantID, installationID, 1)
	tx, err := postgres.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := reserveReviewUsage(ctx, tx, tenantID, firstRun, "RainLib/open-review-platform"); err != nil {
		t.Fatalf("reserve first review: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	secondRun := seedUsageRun(t, ctx, postgres, tenantID, installationID, 2)
	tx, err = postgres.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := reserveReviewUsage(ctx, tx, tenantID, secondRun, "RainLib/other"); !errors.Is(err, ErrQuotaExceeded) {
		_ = tx.Rollback(ctx)
		t.Fatalf("second reservation error=%v, want ErrQuotaExceeded", err)
	}
	_ = tx.Rollback(ctx)

	tx, err = postgres.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := finalizeReviewUsage(ctx, tx, firstRun, true); err != nil {
		t.Fatalf("settle first review: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	dashboard, err := postgres.GetUsageDashboard(ctx, "owner", tenantSlug, 10)
	if err != nil {
		t.Fatal(err)
	}
	if dashboard.Settled != 1 || dashboard.Reserved != 0 || dashboard.Remaining == nil || *dashboard.Remaining != 0 {
		t.Fatalf("unexpected usage dashboard: %#v", dashboard)
	}
	if len(dashboard.Ledger) != 2 || dashboard.Ledger[0].EventKind != "settle" || dashboard.Ledger[1].EventKind != "reserve" {
		t.Fatalf("unexpected usage ledger: %#v", dashboard.Ledger)
	}

	export, err := postgres.ExportUsage(ctx, "billing", tenantSlug, dashboard.PeriodStart)
	if err != nil {
		t.Fatalf("billing viewer export: %v", err)
	}
	if !export.PeriodStart.Equal(dashboard.PeriodStart) || !export.PeriodEnd.Equal(dashboard.PeriodEnd) || export.GeneratedAt.IsZero() || export.MonthlyReviewLimit != 1 || export.SoftWarningPercent != 80 || export.Settled != 1 || export.Reserved != 0 || export.Released != 0 {
		t.Fatalf("unexpected usage export: %#v", export)
	}
	if len(export.Repositories) != 1 || export.Repositories[0].Repository != "RainLib/open-review-platform" || export.Repositories[0].Settled != 1 {
		t.Fatalf("unexpected usage export rows: %#v", export.Repositories)
	}
	if _, err := postgres.ExportUsage(ctx, "viewer", tenantSlug, dashboard.PeriodStart); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer export error=%v, want ErrForbidden", err)
	}
	if _, err := postgres.ExportUsage(ctx, "outsider", tenantSlug, dashboard.PeriodStart); !errors.Is(err, ErrForbidden) {
		t.Fatalf("outsider export error=%v, want ErrForbidden", err)
	}
	future := dashboard.PeriodStart.AddDate(0, 1, 0)
	if _, err := postgres.ExportUsage(ctx, "owner", tenantSlug, future); !errors.Is(err, ErrInvalidUsageExport) {
		t.Fatalf("future export error=%v, want ErrInvalidUsageExport", err)
	}
}

func TestUsageReconciliationRepairsReservationsAndAppendsEvidence(t *testing.T) {
	databaseURL := os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("OPEN_REVIEW_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	postgres, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer postgres.Close()

	tenantID := uuid.New()
	installationID := uuid.New()
	tenantSlug := "usage-reconcile-" + tenantID.String()[:8]
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO tenants (id, slug, name) VALUES ($1, $2, 'Usage Reconciliation')`, tenantID, tenantSlug)
	batch.Queue(`INSERT INTO memberships (tenant_id, subject, role) VALUES ($1, 'owner', 'owner'), ($1, 'billing', 'billing_viewer')`, tenantID)
	batch.Queue(`INSERT INTO tenant_entitlements (tenant_id, monthly_review_limit, soft_warning_percent, updated_by) VALUES ($1, 100, 80, 'owner')`, tenantID)
	batch.Queue(`INSERT INTO provider_installations (id, tenant_id, provider, external_id, repository_scope, api_base_url, credential_ref) VALUES ($1, $2, 'github', $3, 'RainLib/*', 'https://api.github.com', 'secret://usage-reconcile')`, installationID, tenantID, "usage-reconcile-installation-"+tenantID.String())
	if err := postgres.pool.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatalf("seed reconciliation tenant: %v", err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenantID) }()

	completedRun := seedUsageRun(t, ctx, postgres, tenantID, installationID, 101)
	tx, err := postgres.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := reserveReviewUsage(ctx, tx, tenantID, completedRun, "RainLib/open-review-platform"); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("reserve completed run: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE review_runs SET state='completed',finished_at=now() WHERE id=$1`, completedRun); err != nil {
		t.Fatalf("make stale completed reservation: %v", err)
	}

	failedRun := seedUsageRun(t, ctx, postgres, tenantID, installationID, 102)
	if _, err := postgres.pool.Exec(ctx, `UPDATE review_runs SET state='failed',failure_code='model_error',finished_at=now() WHERE id=$1`, failedRun); err != nil {
		t.Fatalf("make missing failed reservation: %v", err)
	}

	reservedRun := seedUsageRun(t, ctx, postgres, tenantID, installationID, 103)
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO usage_reservations (tenant_id,run_id,repository,metric,reserved_quantity,state,period_start)
		VALUES ($1,$2,'RainLib/repository-103','review_run',2,'reserved',date_trunc('month',now() AT TIME ZONE 'UTC')::date)`, tenantID, reservedRun); err != nil {
		t.Fatalf("make reservation without ledger proof: %v", err)
	}

	dashboard, err := postgres.GetUsageDashboard(ctx, "owner", tenantSlug, 20)
	if err != nil {
		t.Fatal(err)
	}
	if dashboard.Reconciliation.State != "drift" || dashboard.Reconciliation.ScannedRuns != 3 || dashboard.Reconciliation.DriftedRuns != 3 ||
		dashboard.Reconciliation.MissingReservations != 1 || dashboard.Reconciliation.StateMismatches != 2 || dashboard.Reconciliation.MissingLedgerProofs != 3 {
		t.Fatalf("unexpected pre-reconciliation drift: %#v", dashboard.Reconciliation)
	}

	periodStart := dashboard.PeriodStart.Format("2006-01-02")
	input := domain.UsageReconciliationInput{
		PeriodStart: periodStart, Reason: "repair integration drift", IdempotencyKey: "usage-reconcile-integration-1",
	}
	if _, err := postgres.ReconcileUsage(ctx, "billing", tenantSlug, input); !errors.Is(err, ErrForbidden) {
		t.Fatalf("billing viewer reconciliation error=%v, want ErrForbidden", err)
	}

	report, err := postgres.ReconcileUsage(ctx, "owner", tenantSlug, input)
	if err != nil {
		t.Fatalf("reconcile usage: %v", err)
	}
	if report.State != "clean" || report.ScannedRuns != 3 || report.DriftedRuns != 3 || report.RepairedRuns != 3 || report.Adjustments != 3 ||
		report.MissingReservations != 1 || report.StateMismatches != 2 || report.MissingLedgerProofs != 3 || report.ReconciledBy != "owner" {
		t.Fatalf("unexpected reconciliation report: %#v", report)
	}

	assertUsageReservationState(t, ctx, postgres, completedRun, "settled", 1, 1)
	assertUsageReservationState(t, ctx, postgres, failedRun, "released", 1, 0)
	assertUsageReservationState(t, ctx, postgres, reservedRun, "reserved", 1, 0)

	var reserveEvents, adjustmentEvents int64
	if err := postgres.pool.QueryRow(ctx, `SELECT COUNT(*) FROM usage_ledger WHERE tenant_id=$1 AND event_kind='reserve' AND run_id=$2`, tenantID, completedRun).Scan(&reserveEvents); err != nil {
		t.Fatal(err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT COUNT(*) FROM usage_ledger WHERE tenant_id=$1 AND event_kind='adjustment'`, tenantID).Scan(&adjustmentEvents); err != nil {
		t.Fatal(err)
	}
	if reserveEvents != 1 || adjustmentEvents != 3 {
		t.Fatalf("ledger reserve=%d adjustments=%d, want 1 and 3", reserveEvents, adjustmentEvents)
	}
	var previousReserved, targetReserved int64
	if err := postgres.pool.QueryRow(ctx, `
		SELECT (metadata->>'previous_reserved_quantity')::bigint,(metadata->>'target_reserved_quantity')::bigint
		FROM usage_ledger WHERE run_id=$1 AND event_kind='adjustment'`, reservedRun).Scan(&previousReserved, &targetReserved); err != nil {
		t.Fatal(err)
	}
	if previousReserved != 2 || targetReserved != 1 {
		t.Fatalf("adjustment quantity evidence previous=%d target=%d, want 2 -> 1", previousReserved, targetReserved)
	}

	replayed, err := postgres.ReconcileUsage(ctx, "owner", tenantSlug, input)
	if err != nil {
		t.Fatalf("replay reconciliation: %v", err)
	}
	if replayed.ReconciledAt.IsZero() || !replayed.ReconciledAt.Equal(report.ReconciledAt) || replayed.RepairedRuns != report.RepairedRuns || replayed.Adjustments != report.Adjustments {
		t.Fatalf("idempotent replay changed the report: first=%#v replay=%#v", report, replayed)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT COUNT(*) FROM usage_ledger WHERE tenant_id=$1 AND event_kind='adjustment'`, tenantID).Scan(&adjustmentEvents); err != nil {
		t.Fatal(err)
	}
	if adjustmentEvents != 3 {
		t.Fatalf("idempotent replay appended %d adjustment rows, want 3 total", adjustmentEvents)
	}

	conflict := input
	conflict.Reason = "different operator reason"
	if _, err := postgres.ReconcileUsage(ctx, "owner", tenantSlug, conflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("idempotency conflict error=%v, want ErrConflict", err)
	}

	dashboard, err = postgres.GetUsageDashboard(ctx, "owner", tenantSlug, 20)
	if err != nil {
		t.Fatal(err)
	}
	if dashboard.Reconciliation.State != "clean" || dashboard.Reconciliation.DriftedRuns != 0 || dashboard.Reconciliation.LastReconciledAt == nil || dashboard.Reconciliation.LastReconciledBy != "owner" {
		t.Fatalf("unexpected post-reconciliation status: %#v", dashboard.Reconciliation)
	}
	if dashboard.Settled != 1 || dashboard.Reserved != 1 {
		t.Fatalf("unexpected reconciled usage totals: settled=%d reserved=%d", dashboard.Settled, dashboard.Reserved)
	}
}

func assertUsageReservationState(t *testing.T, ctx context.Context, postgres *PostgresStore, runID uuid.UUID, wantState string, wantReserved, wantSettled int64) {
	t.Helper()
	var state string
	var reserved, settled int64
	var settledAt *time.Time
	if err := postgres.pool.QueryRow(ctx, `SELECT state,reserved_quantity,settled_quantity,settled_at FROM usage_reservations WHERE run_id=$1`, runID).Scan(&state, &reserved, &settled, &settledAt); err != nil {
		t.Fatalf("read usage reservation %s: %v", runID, err)
	}
	if state != wantState || reserved != wantReserved || settled != wantSettled {
		t.Fatalf("reservation %s state=%s reserved=%d settled=%d, want %s/%d/%d", runID, state, reserved, settled, wantState, wantReserved, wantSettled)
	}
	if wantState == "reserved" && settledAt != nil {
		t.Fatalf("reserved run %s unexpectedly has settled_at=%s", runID, *settledAt)
	}
	if wantState != "reserved" && settledAt == nil {
		t.Fatalf("terminal run %s has no settled_at", runID)
	}
}

func seedUsageRun(t *testing.T, ctx context.Context, postgres *PostgresStore, tenantID, installationID uuid.UUID, sequence int) uuid.UUID {
	t.Helper()
	deliveryID := uuid.New()
	jobID := uuid.New()
	requestID := uuid.New()
	runID := uuid.New()
	repository := fmt.Sprintf("RainLib/repository-%d", sequence)
	head := fmt.Sprintf("head-%d", sequence)
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO webhook_deliveries (id, provider, delivery_id, event_name, payload) VALUES ($1, 'github', $2, 'pull_request', '{}'::jsonb)`, deliveryID, "usage-delivery-"+deliveryID.String())
	batch.Queue(`INSERT INTO review_jobs (id, tenant_id, installation_id, delivery_id, provider, api_base_url, repository, clone_url, review_number, base_ref, base_sha, head_ref, head_sha, state) VALUES ($1, $2, $3, $4, 'github', 'https://api.github.com', $5, 'https://github.com/RainLib/open-review-platform.git', $6, 'main', 'base', 'feature', $7, 'queued')`, jobID, tenantID, installationID, deliveryID, repository, sequence, head)
	batch.Queue(`INSERT INTO review_requests (id, tenant_id, installation_id, provider, api_base_url, repository, review_number) VALUES ($1, $2, $3, 'github', 'https://api.github.com', $4, $5)`, requestID, tenantID, installationID, repository, sequence)
	batch.Queue(`INSERT INTO review_runs (id, request_id, legacy_job_id, state, trigger_kind, head_sha, base_sha) VALUES ($1, $2, $3, 'acknowledged', 'pull_request', $4, 'base')`, runID, requestID, jobID, head)
	if err := postgres.pool.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatalf("seed usage run %d: %v", sequence, err)
	}
	return runID
}
