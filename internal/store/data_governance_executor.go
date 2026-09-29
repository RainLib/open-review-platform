package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const governanceLeaseFloor = 15 * time.Second

func (s *PostgresStore) ClaimDataGovernanceJob(ctx context.Context, workerID string, lease time.Duration) (*domain.DataGovernanceJobTarget, error) {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" || lease < governanceLeaseFloor {
		return nil, ErrInvalidDataGovernance
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin data governance claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var target domain.DataGovernanceJobTarget
	var classes []string
	err = tx.QueryRow(ctx, `
		SELECT id,tenant_id,kind,scope_kind,scope_ref,data_classes,desired_region,external_operation_id,reversible_until
		FROM data_governance_jobs
		WHERE (
			(state='queued' AND available_at <= now() AND (kind <> 'deletion' OR reversible_until <= now()))
			OR (state='running' AND locked_until < now())
		)
		ORDER BY created_at,id
		FOR UPDATE SKIP LOCKED
		LIMIT 1`).Scan(
		&target.ID, &target.TenantID, &target.Kind, &target.ScopeKind, &target.ScopeRef,
		&classes, &target.DesiredRegion, &target.ExternalOperationID, &target.ReversibleUntil,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoQueuedGovernanceJob
	}
	if err != nil {
		return nil, fmt.Errorf("select data governance job: %w", err)
	}
	target.DataClasses = dataClasses(classes)

	// A hold can be added after approval or while a worker lease is expired. The
	// destructive boundary therefore checks again inside the claim transaction.
	if target.Kind == domain.DataJobDeletion {
		hasHold, holdErr := matchingLegalHold(ctx, tx, target.TenantID, target.ScopeKind, target.ScopeRef, target.DataClasses)
		if holdErr != nil {
			return nil, holdErr
		}
		if hasHold {
			if _, updateErr := tx.Exec(ctx, `
				UPDATE data_governance_jobs
				SET state='failed',revision=revision+1,error_code='legal_hold_active',
				    error_message='Execution stopped because a matching legal hold became active.',
				    updated_at=now(),finished_at=now(),worker_id=NULL,locked_until=NULL
				WHERE id=$1`, target.ID); updateErr != nil {
				return nil, fmt.Errorf("stop governance job protected by legal hold: %w", updateErr)
			}
			if _, auditErr := tx.Exec(ctx, `
				INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata)
				VALUES ($1,'system:data-governance','data.job.failed',$2,jsonb_build_object('error_code','legal_hold_active'))`, target.TenantID, target.ID.String()); auditErr != nil {
				return nil, fmt.Errorf("audit legal-hold execution stop: %w", auditErr)
			}
			if commitErr := tx.Commit(ctx); commitErr != nil {
				return nil, fmt.Errorf("commit legal-hold execution stop: %w", commitErr)
			}
			return nil, ErrNoQueuedGovernanceJob
		}
	}

	interval := fmt.Sprintf("%f seconds", lease.Seconds())
	err = tx.QueryRow(ctx, `
		UPDATE data_governance_jobs
		SET state='running',progress=GREATEST(progress,1),revision=revision+1,
		    worker_id=$2,locked_until=now()+$3::interval,attempt=attempt+1,
		    started_at=COALESCE(started_at,now()),updated_at=now(),error_code='',error_message=''
		WHERE id=$1
		RETURNING worker_id,attempt,locked_until`, target.ID, workerID, interval).Scan(&target.WorkerID, &target.Attempt, &target.LockedUntil)
	if err != nil {
		return nil, fmt.Errorf("lease data governance job: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata)
		VALUES ($1,$2,'data.job.started',$3,jsonb_build_object('attempt',$4::int,'lease_until',$5::timestamptz))`,
		target.TenantID, "worker:"+workerID, target.ID.String(), target.Attempt, target.LockedUntil); err != nil {
		return nil, fmt.Errorf("audit data governance claim: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit data governance claim: %w", err)
	}
	return &target, nil
}

func (s *PostgresStore) RenewDataGovernanceJobClaim(ctx context.Context, jobID uuid.UUID, workerID string, lease time.Duration) error {
	if jobID == uuid.Nil || strings.TrimSpace(workerID) == "" || lease < governanceLeaseFloor {
		return ErrInvalidDataGovernance
	}
	interval := fmt.Sprintf("%f seconds", lease.Seconds())
	result, err := s.pool.Exec(ctx, `
		UPDATE data_governance_jobs SET locked_until=now()+$3::interval,updated_at=now()
		WHERE id=$1 AND worker_id=$2 AND state='running' AND locked_until >= now()`, jobID, workerID, interval)
	if err != nil {
		return fmt.Errorf("renew data governance job lease: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrGovernanceClaimLost
	}
	return nil
}

func (s *PostgresStore) BuildDataGovernanceExport(ctx context.Context, target domain.DataGovernanceJobTarget, maxRecords int) ([]byte, map[string]int64, error) {
	if target.Kind != domain.DataJobExport || target.ID == uuid.Nil || target.TenantID == uuid.Nil || maxRecords < 1 {
		return nil, nil, ErrInvalidDataGovernance
	}
	start, end, err := governanceTimeRange(target)
	if err != nil {
		return nil, nil, err
	}
	records := make(map[string]json.RawMessage, len(target.DataClasses))
	counts := make(map[string]int64, len(target.DataClasses))
	notices := map[string]string{}
	for _, class := range target.DataClasses {
		if class == domain.DataClassOperationalLog {
			records[string(class)] = json.RawMessage(`[]`)
			counts[string(class)] = 0
			notices[string(class)] = "Operational logs are not persisted by this control plane. Export an external log sink separately."
			continue
		}
		payload, count, queryErr := s.exportDataClass(ctx, target, class, start, end, maxRecords)
		if queryErr != nil {
			return nil, nil, queryErr
		}
		records[string(class)] = payload
		counts[string(class)] = count
	}
	envelope := map[string]any{
		"schema":       "open-review.data-export.v1",
		"job_id":       target.ID,
		"generated_at": time.Now().UTC(),
		"scope": map[string]any{
			"kind": target.ScopeKind,
			"ref":  target.ScopeRef,
		},
		"counts":  counts,
		"notices": notices,
		"records": records,
	}
	payload, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("encode data governance export: %w", err)
	}
	return payload, counts, nil
}

func (s *PostgresStore) ExecuteDataGovernanceDeletion(ctx context.Context, target domain.DataGovernanceJobTarget) (map[string]any, error) {
	if target.Kind != domain.DataJobDeletion || target.ID == uuid.Nil || target.TenantID == uuid.Nil || target.WorkerID == "" || (target.ScopeKind != domain.DataScopeTenant && target.ScopeKind != domain.DataScopeRepository) {
		return nil, ErrInvalidDataGovernance
	}
	for _, class := range target.DataClasses {
		if class != domain.DataClassRawWebhook && class != domain.DataClassFindings && class != domain.DataClassAudit {
			return nil, ErrInvalidDataGovernance
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin data governance deletion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var active bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM data_governance_jobs WHERE id=$1 AND tenant_id=$2 AND worker_id=$3 AND state='running' AND locked_until >= now())`, target.ID, target.TenantID, target.WorkerID).Scan(&active); err != nil {
		return nil, fmt.Errorf("verify deletion claim: %w", err)
	}
	if !active {
		return nil, ErrGovernanceClaimLost
	}
	if err := lockDataGovernanceTenant(ctx, tx, target.TenantID); err != nil {
		return nil, err
	}
	hasHold, err := matchingLegalHold(ctx, tx, target.TenantID, target.ScopeKind, target.ScopeRef, target.DataClasses)
	if err != nil {
		return nil, err
	}
	if hasHold {
		return nil, ErrLegalHold
	}
	counts := make(map[string]int64, len(target.DataClasses))
	for _, class := range target.DataClasses {
		var result pgconn.CommandTag
		switch class {
		case domain.DataClassRawWebhook:
			result, err = tx.Exec(ctx, `
				UPDATE webhook_deliveries delivery
				SET payload=jsonb_build_object('_redacted',true,'governance_job_id',$3::text)
				WHERE delivery.id IN (
					SELECT DISTINCT job.delivery_id FROM review_jobs job
					WHERE job.tenant_id=$1 AND ($2='' OR job.repository=$2)
				)`, target.TenantID, target.ScopeRef, target.ID.String())
		case domain.DataClassFindings:
			result, err = tx.Exec(ctx, `
				UPDATE review_findings finding
				SET body=$3,suggestion='',code_excerpt='',code_excerpt_start_line=0,proposed_patch=''
				FROM review_jobs job
				WHERE finding.job_id=job.id AND job.tenant_id=$1 AND ($2='' OR job.repository=$2)`,
				target.TenantID, target.ScopeRef, "[redacted by data governance job "+target.ID.String()+"]")
		case domain.DataClassAudit:
			result, err = tx.Exec(ctx, `
				UPDATE audit_events
				SET actor_subject='redacted',target='redacted',metadata=jsonb_build_object('_redacted',true,'governance_job_id',$3::text)
				WHERE tenant_id=$1 AND action NOT LIKE 'data.%' AND ($2='' OR metadata->>'repository'=$2)`,
				target.TenantID, target.ScopeRef, target.ID.String())
		}
		if err != nil {
			return nil, fmt.Errorf("redact %s: %w", class, err)
		}
		counts[string(class)] = result.RowsAffected()
		if _, err := tx.Exec(ctx, `
			INSERT INTO data_governance_erasure_tombstones (job_id,tenant_id,data_class,scope_kind,scope_ref,records_redacted,executed_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (job_id,data_class) DO UPDATE SET records_redacted=EXCLUDED.records_redacted,executed_by=EXCLUDED.executed_by,created_at=now()`,
			target.ID, target.TenantID, class, target.ScopeKind, target.ScopeRef, result.RowsAffected(), target.WorkerID); err != nil {
			return nil, fmt.Errorf("record %s erasure tombstone: %w", class, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit data governance deletion: %w", err)
	}
	return map[string]any{
		"schema":             "open-review.data-erasure-receipt.v1",
		"counts":             counts,
		"content_erasure":    true,
		"structural_records": "preserved for referential integrity and governance evidence",
		"governance_audit":   "data.* events preserved",
		"executed_at":        time.Now().UTC(),
	}, nil
}

func governanceTimeRange(target domain.DataGovernanceJobTarget) (time.Time, time.Time, error) {
	if target.ScopeKind != domain.DataScopeAuditRange {
		return time.Time{}, time.Time{}, nil
	}
	parts := strings.Split(target.ScopeRef, "/")
	if len(parts) != 2 {
		return time.Time{}, time.Time{}, ErrInvalidDataGovernance
	}
	start, startErr := time.Parse(time.RFC3339, parts[0])
	end, endErr := time.Parse(time.RFC3339, parts[1])
	if startErr != nil || endErr != nil || !start.Before(end) {
		return time.Time{}, time.Time{}, ErrInvalidDataGovernance
	}
	return start.UTC(), end.UTC(), nil
}

func (s *PostgresStore) exportDataClass(ctx context.Context, target domain.DataGovernanceJobTarget, class domain.DataClass, start, end time.Time, maxRecords int) (json.RawMessage, int64, error) {
	var query string
	switch class {
	case domain.DataClassRawWebhook:
		query = `SELECT delivery.id,delivery.provider,delivery.delivery_id,delivery.event_name,delivery.payload,delivery.received_at,
			delivery.received_at AS export_sort_at,delivery.id AS export_sort_id
			FROM webhook_deliveries delivery
			JOIN review_jobs job ON job.delivery_id=delivery.id
			LEFT JOIN review_runs run ON run.legacy_job_id=job.id
			WHERE job.tenant_id=$1 AND ($2='tenant' OR ($2='repository' AND job.repository=$3) OR ($2='review_run' AND run.id::text=$3) OR ($2='audit_range' AND delivery.received_at >= $4 AND delivery.received_at < $5))`
	case domain.DataClassFindings:
		query = `SELECT finding.id,finding.path,finding.start_line,finding.end_line,finding.severity,finding.category,finding.body,finding.suggestion,
			finding.code_excerpt,finding.code_excerpt_start_line,finding.proposed_patch,finding.fingerprint,finding.created_at,
			finding.created_at AS export_sort_at,finding.id AS export_sort_id
			FROM review_findings finding
			JOIN review_jobs job ON job.id=finding.job_id
			LEFT JOIN review_runs run ON run.legacy_job_id=job.id
			WHERE job.tenant_id=$1 AND ($2='tenant' OR ($2='repository' AND job.repository=$3) OR ($2='review_run' AND run.id::text=$3) OR ($2='audit_range' AND finding.created_at >= $4 AND finding.created_at < $5))`
	case domain.DataClassAudit:
		query = `SELECT event.id,event.actor_subject,event.action,event.target,event.metadata,event.created_at,
			event.created_at AS export_sort_at,event.id AS export_sort_id
			FROM audit_events event
			WHERE event.tenant_id=$1 AND ($2='tenant' OR ($2='repository' AND event.metadata->>'repository'=$3) OR ($2='review_run' AND (event.metadata->>'run_id'=$3 OR event.target=$3)) OR ($2='audit_range' AND event.created_at >= $4 AND event.created_at < $5))`
	case domain.DataClassUsage:
		query = `SELECT ledger.id,ledger.run_id,ledger.repository,ledger.metric,ledger.quantity,ledger.unit,ledger.event_kind,ledger.idempotency_key,ledger.metadata,ledger.occurred_at,
			ledger.occurred_at AS export_sort_at,ledger.id AS export_sort_id
			FROM usage_ledger ledger
			WHERE ledger.tenant_id=$1 AND ($2='tenant' OR ($2='repository' AND ledger.repository=$3) OR ($2='review_run' AND ledger.run_id::text=$3) OR ($2='audit_range' AND ledger.occurred_at >= $4 AND ledger.occurred_at < $5))`
	default:
		return nil, 0, ErrInvalidDataGovernance
	}
	// Export artifacts are evidence, so row order must be deterministic.  Sorting
	// by the JSON projection (the former ORDER BY 1) was technically stable only
	// by implementation accident and could interleave timeline records by field
	// value. Keep the internal order columns outside the public artifact.
	wrapped := `SELECT COALESCE(jsonb_agg(to_jsonb(item)-'export_sort_at'-'export_sort_id' ORDER BY item.export_sort_at,item.export_sort_id), '[]'::jsonb),COUNT(*) FROM (` + query + ` ORDER BY export_sort_at,export_sort_id LIMIT $6) item`
	var payload []byte
	var count int64
	if err := s.pool.QueryRow(ctx, wrapped, target.TenantID, target.ScopeKind, target.ScopeRef, start, end, maxRecords+1).Scan(&payload, &count); err != nil {
		return nil, 0, fmt.Errorf("export %s: %w", class, err)
	}
	if count > int64(maxRecords) {
		return nil, 0, fmt.Errorf("export %s exceeds the configured %d-record safety limit", class, maxRecords)
	}
	return json.RawMessage(payload), count, nil
}

func (s *PostgresStore) CompleteDataGovernanceExport(ctx context.Context, target domain.DataGovernanceJobTarget, artifact domain.DataGovernanceArtifact, receipt map[string]any) error {
	if target.Kind != domain.DataJobExport || target.ID == uuid.Nil || artifact.JobID != target.ID || artifact.TenantID != target.TenantID || artifact.ExpiresAt.Before(time.Now()) {
		return ErrInvalidDataGovernance
	}
	receiptJSON, err := json.Marshal(receipt)
	if err != nil {
		return ErrInvalidDataGovernance
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin data governance export completion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var active bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM data_governance_jobs WHERE id=$1 AND tenant_id=$2 AND worker_id=$3 AND state='running' AND locked_until >= now())`, target.ID, target.TenantID, target.WorkerID).Scan(&active); err != nil {
		return fmt.Errorf("verify data governance export claim: %w", err)
	}
	if !active {
		return ErrGovernanceClaimLost
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO data_governance_artifacts (job_id,tenant_id,filename,content_type,key_version,nonce,ciphertext,plaintext_sha256,plaintext_bytes,expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (job_id) DO UPDATE SET filename=EXCLUDED.filename,content_type=EXCLUDED.content_type,key_version=EXCLUDED.key_version,
			nonce=EXCLUDED.nonce,ciphertext=EXCLUDED.ciphertext,plaintext_sha256=EXCLUDED.plaintext_sha256,
			plaintext_bytes=EXCLUDED.plaintext_bytes,created_at=now(),expires_at=EXCLUDED.expires_at`,
		artifact.JobID, artifact.TenantID, artifact.Filename, artifact.ContentType, artifact.KeyVersion,
		artifact.Nonce, artifact.Ciphertext, artifact.PlaintextSHA256, artifact.PlaintextBytes, artifact.ExpiresAt); err != nil {
		return fmt.Errorf("persist encrypted governance artifact: %w", err)
	}
	result, err := tx.Exec(ctx, `
		UPDATE data_governance_jobs
		SET state='completed',progress=100,revision=revision+1,artifact_ref=$4,receipt=$5,
		    updated_at=now(),finished_at=now(),worker_id=NULL,locked_until=NULL,error_code='',error_message=''
		WHERE id=$1 AND tenant_id=$2 AND worker_id=$3 AND state='running'`,
		target.ID, target.TenantID, target.WorkerID, "postgres-encrypted:"+target.ID.String(), receiptJSON)
	if err != nil {
		return fmt.Errorf("complete data governance export: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrGovernanceClaimLost
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata) VALUES ($1,$2,'data.job.completed',$3,$4)`, target.TenantID, "worker:"+target.WorkerID, target.ID.String(), receiptJSON); err != nil {
		return fmt.Errorf("audit data governance export completion: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit data governance export completion: %w", err)
	}
	return nil
}

func (s *PostgresStore) CompleteDataGovernanceOperation(ctx context.Context, target domain.DataGovernanceJobTarget, receipt map[string]any) error {
	if target.Kind != domain.DataJobDeletion || target.ID == uuid.Nil || target.TenantID == uuid.Nil || target.WorkerID == "" {
		return ErrInvalidDataGovernance
	}
	receiptJSON, err := json.Marshal(receipt)
	if err != nil {
		return ErrInvalidDataGovernance
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin data governance operation completion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `
		UPDATE data_governance_jobs
		SET state='completed',progress=100,revision=revision+1,receipt=$4,
		    updated_at=now(),finished_at=now(),worker_id=NULL,locked_until=NULL,error_code='',error_message=''
		WHERE id=$1 AND tenant_id=$2 AND worker_id=$3 AND state='running' AND locked_until >= now()`,
		target.ID, target.TenantID, target.WorkerID, receiptJSON)
	if err != nil {
		return fmt.Errorf("complete data governance operation: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrGovernanceClaimLost
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata) VALUES ($1,$2,'data.job.completed',$3,$4)`, target.TenantID, "worker:"+target.WorkerID, target.ID.String(), receiptJSON); err != nil {
		return fmt.Errorf("audit data governance operation completion: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit data governance operation completion: %w", err)
	}
	return nil
}

func (s *PostgresStore) DeferDataGovernanceRegionMigration(ctx context.Context, target domain.DataGovernanceJobTarget, externalOperationID string, progress int, receipt map[string]any, availableAt time.Time) error {
	externalOperationID = strings.TrimSpace(externalOperationID)
	if target.Kind != domain.DataJobRegionMigration || target.ID == uuid.Nil || target.TenantID == uuid.Nil || target.WorkerID == "" || len(externalOperationID) > 200 || strings.ContainsAny(externalOperationID, "\r\n\x00") || progress < 0 || progress > 99 || !availableAt.After(time.Now().Add(-time.Second)) {
		return ErrInvalidDataGovernance
	}
	receiptJSON, err := json.Marshal(receipt)
	if err != nil {
		return ErrInvalidDataGovernance
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin region migration deferral: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `
		UPDATE data_governance_jobs
		SET state='queued',progress=$4,revision=revision+1,external_operation_id=$5,receipt=$6,
		    available_at=$7,updated_at=now(),worker_id=NULL,locked_until=NULL,error_code='',error_message=''
		WHERE id=$1 AND tenant_id=$2 AND worker_id=$3 AND state='running' AND locked_until >= now()`,
		target.ID, target.TenantID, target.WorkerID, progress, externalOperationID, receiptJSON, availableAt.UTC())
	if err != nil {
		return fmt.Errorf("defer region migration: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrGovernanceClaimLost
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata)
		VALUES ($1,$2,'data.region_migration.deferred',$3,jsonb_build_object(
			'external_operation_id',$4::text,'progress',$5::int,'available_at',$6::timestamptz
		))`, target.TenantID, "worker:"+target.WorkerID, target.ID.String(), externalOperationID, progress, availableAt.UTC()); err != nil {
		return fmt.Errorf("audit region migration deferral: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit region migration deferral: %w", err)
	}
	return nil
}

func (s *PostgresStore) CompleteDataGovernanceRegionMigration(ctx context.Context, target domain.DataGovernanceJobTarget, observed domain.DataResidency, receipt map[string]any) error {
	if target.Kind != domain.DataJobRegionMigration || target.ID == uuid.Nil || target.TenantID == uuid.Nil || target.WorkerID == "" || target.DesiredRegion == "" || observed.PrimaryRegion != target.DesiredRegion || observed.ObservedAt == nil {
		return ErrInvalidDataGovernance
	}
	receiptJSON, err := json.Marshal(receipt)
	if err != nil {
		return ErrInvalidDataGovernance
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin region migration completion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var active bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM data_governance_jobs WHERE id=$1 AND tenant_id=$2 AND worker_id=$3 AND state='running' AND locked_until >= now())`, target.ID, target.TenantID, target.WorkerID).Scan(&active); err != nil {
		return fmt.Errorf("verify region migration claim: %w", err)
	}
	if !active {
		return ErrGovernanceClaimLost
	}
	if err := lockDataGovernanceTenant(ctx, tx, target.TenantID); err != nil {
		return err
	}
	modelBoundary := observed.ModelBoundary
	if modelBoundary == "" {
		modelBoundary = "external/unknown"
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO workspace_data_residency (
			tenant_id,primary_region,backup_region,object_region,queue_region,model_boundary,
			revision,effective_at,observed_at,updated_by,updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,1,$7,$7,$8,now())
		ON CONFLICT (tenant_id) DO UPDATE SET
			primary_region=EXCLUDED.primary_region,
			backup_region=COALESCE(NULLIF(EXCLUDED.backup_region,''),workspace_data_residency.backup_region),
			object_region=COALESCE(NULLIF(EXCLUDED.object_region,''),workspace_data_residency.object_region),
			queue_region=COALESCE(NULLIF(EXCLUDED.queue_region,''),workspace_data_residency.queue_region),
			model_boundary=COALESCE(NULLIF(EXCLUDED.model_boundary,'external/unknown'),workspace_data_residency.model_boundary),
			revision=workspace_data_residency.revision+1,effective_at=EXCLUDED.effective_at,
			observed_at=EXCLUDED.observed_at,updated_by=EXCLUDED.updated_by,updated_at=now()`,
		target.TenantID, observed.PrimaryRegion, observed.BackupRegion, observed.ObjectRegion,
		observed.QueueRegion, modelBoundary, observed.ObservedAt.UTC(), "worker:"+target.WorkerID); err != nil {
		return fmt.Errorf("apply observed data residency: %w", err)
	}
	result, err := tx.Exec(ctx, `
		UPDATE data_governance_jobs
		SET state='completed',progress=100,revision=revision+1,receipt=$4,available_at=now(),
		    updated_at=now(),finished_at=now(),worker_id=NULL,locked_until=NULL,error_code='',error_message=''
		WHERE id=$1 AND tenant_id=$2 AND worker_id=$3 AND state='running'`,
		target.ID, target.TenantID, target.WorkerID, receiptJSON)
	if err != nil {
		return fmt.Errorf("complete region migration job: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrGovernanceClaimLost
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata)
		VALUES ($1,$2,'data.region_migration.completed',$3,$4)`, target.TenantID, "worker:"+target.WorkerID, target.ID.String(), receiptJSON); err != nil {
		return fmt.Errorf("audit region migration completion: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit region migration completion: %w", err)
	}
	return nil
}

func (s *PostgresStore) FailDataGovernanceJob(ctx context.Context, target domain.DataGovernanceJobTarget, code, message string) error {
	code = strings.TrimSpace(code)
	message = strings.TrimSpace(message)
	if target.ID == uuid.Nil || target.TenantID == uuid.Nil || target.WorkerID == "" || code == "" || len(code) > 80 || message == "" || len(message) > 500 {
		return ErrInvalidDataGovernance
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin data governance failure: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `
		UPDATE data_governance_jobs
		SET state='failed',revision=revision+1,error_code=$4,error_message=$5,
		    updated_at=now(),finished_at=now(),worker_id=NULL,locked_until=NULL
		WHERE id=$1 AND tenant_id=$2 AND worker_id=$3 AND state='running' AND locked_until >= now()`, target.ID, target.TenantID, target.WorkerID, code, message)
	if err != nil {
		return fmt.Errorf("fail data governance job: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrGovernanceClaimLost
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata) VALUES ($1,$2,'data.job.failed',$3,jsonb_build_object('error_code',$4::text))`, target.TenantID, "worker:"+target.WorkerID, target.ID.String(), code); err != nil {
		return fmt.Errorf("audit data governance failure: %w", err)
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) GetDataGovernanceArtifact(ctx context.Context, actor, tenantSlug string, jobID uuid.UUID) (domain.DataGovernanceArtifact, error) {
	tenantID, role, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.DataGovernanceArtifact{}, err
	}
	if role != "owner" && role != "admin" {
		return domain.DataGovernanceArtifact{}, ErrForbidden
	}
	var artifact domain.DataGovernanceArtifact
	err = s.pool.QueryRow(ctx, `
		SELECT artifact.job_id,artifact.tenant_id,artifact.filename,artifact.content_type,artifact.key_version,
		       artifact.nonce,artifact.ciphertext,artifact.plaintext_sha256,artifact.plaintext_bytes,artifact.created_at,artifact.expires_at
		FROM data_governance_artifacts artifact
		JOIN data_governance_jobs job ON job.id=artifact.job_id AND job.tenant_id=artifact.tenant_id
		WHERE artifact.job_id=$1 AND artifact.tenant_id=$2 AND job.state='completed' AND artifact.expires_at > now()`, jobID, tenantID).Scan(
		&artifact.JobID, &artifact.TenantID, &artifact.Filename, &artifact.ContentType, &artifact.KeyVersion,
		&artifact.Nonce, &artifact.Ciphertext, &artifact.PlaintextSHA256, &artifact.PlaintextBytes, &artifact.CreatedAt, &artifact.ExpiresAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.DataGovernanceArtifact{}, ErrGovernanceArtifactGone
	}
	if err != nil {
		return domain.DataGovernanceArtifact{}, fmt.Errorf("load data governance artifact: %w", err)
	}
	return artifact, nil
}

func (s *PostgresStore) RecordDataGovernanceArtifactDownload(ctx context.Context, actor, tenantSlug string, jobID uuid.UUID, plaintextSHA256 string) error {
	tenantID, role, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return err
	}
	if role != "owner" && role != "admin" {
		return ErrForbidden
	}
	result, err := s.pool.Exec(ctx, `
		INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata)
		SELECT $1,$2,'data.artifact.downloaded',$3,
		       jsonb_build_object('sha256',$4::text,'expires_at',artifact.expires_at)
		FROM data_governance_artifacts artifact
		WHERE artifact.job_id=$5 AND artifact.tenant_id=$1 AND artifact.plaintext_sha256=$4 AND artifact.expires_at > now()`,
		tenantID, actor, jobID.String(), plaintextSHA256, jobID)
	if err != nil {
		return fmt.Errorf("audit data governance artifact download: %w", err)
	}
	if result.RowsAffected() != 1 {
		return ErrGovernanceArtifactGone
	}
	return nil
}
