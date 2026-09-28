package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const dataGovernanceJobSelect = `
	SELECT id,parent_job_id,kind,scope_kind,scope_ref,data_classes,desired_region,external_operation_id,state,progress,revision,
	       idempotency_key,reason,requested_by,approved_by,approved_at,reversible_until,(artifact_ref <> ''),
	       receipt,error_code,error_message,created_at,updated_at,started_at,finished_at,
	       CASE WHEN state='queued' AND external_operation_id<>'' THEN available_at END
	FROM data_governance_jobs`

func (s *PostgresStore) GetDataGovernanceOverview(ctx context.Context, actor, tenantSlug string) (domain.DataGovernanceOverview, error) {
	tenantID, role, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.DataGovernanceOverview{}, err
	}
	if role != "owner" && role != "admin" {
		return domain.DataGovernanceOverview{}, ErrForbidden
	}
	return s.getDataGovernanceOverview(ctx, tenantID)
}

func (s *PostgresStore) getDataGovernanceOverview(ctx context.Context, tenantID uuid.UUID) (domain.DataGovernanceOverview, error) {
	overview := domain.DataGovernanceOverview{
		Policies:   []domain.RetentionPolicy{},
		LegalHolds: []domain.DataLegalHold{},
		Jobs:       []domain.DataGovernanceJob{},
	}
	var effectiveAt, observedAt, updatedAt *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT primary_region,backup_region,object_region,queue_region,model_boundary,revision,effective_at,observed_at,updated_by,updated_at
		FROM workspace_data_residency WHERE tenant_id=$1`, tenantID).Scan(
		&overview.Residency.PrimaryRegion, &overview.Residency.BackupRegion, &overview.Residency.ObjectRegion,
		&overview.Residency.QueueRegion, &overview.Residency.ModelBoundary, &overview.Residency.Revision,
		&effectiveAt, &observedAt, &overview.Residency.UpdatedBy, &updatedAt,
	)
	if err == nil {
		overview.Residency.Configured = true
		overview.Residency.EffectiveAt = effectiveAt
		overview.Residency.ObservedAt = observedAt
		overview.Residency.UpdatedAt = updatedAt
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return domain.DataGovernanceOverview{}, fmt.Errorf("load data residency: %w", err)
	} else {
		overview.Residency.ModelBoundary = "external/unknown"
	}
	overview.Boundaries = dataResidencyBoundaries(overview.Residency)

	policyRows, err := s.pool.Query(ctx, `
		SELECT id,scope_kind,scope_ref,data_class,retention_days,state,revision,impact_records,impact_legal_hold,
		       change_reason,requested_by,decided_by,decided_at,created_at,updated_at
		FROM data_retention_policies WHERE tenant_id=$1
		ORDER BY CASE state WHEN 'awaiting_approval' THEN 0 WHEN 'active' THEN 1 ELSE 2 END, created_at DESC
		LIMIT 200`, tenantID)
	if err != nil {
		return domain.DataGovernanceOverview{}, fmt.Errorf("list retention policies: %w", err)
	}
	defer policyRows.Close()
	for policyRows.Next() {
		item, scanErr := scanRetentionPolicy(policyRows)
		if scanErr != nil {
			return domain.DataGovernanceOverview{}, fmt.Errorf("scan retention policy: %w", scanErr)
		}
		overview.Policies = append(overview.Policies, item)
	}
	if err := policyRows.Err(); err != nil {
		return domain.DataGovernanceOverview{}, fmt.Errorf("iterate retention policies: %w", err)
	}

	holdRows, err := s.pool.Query(ctx, `
		SELECT id,scope_kind,scope_ref,data_class,reason,state,revision,created_by,created_at,released_by,released_at
		FROM data_legal_holds WHERE tenant_id=$1
		ORDER BY CASE state WHEN 'active' THEN 0 ELSE 1 END, created_at DESC LIMIT 200`, tenantID)
	if err != nil {
		return domain.DataGovernanceOverview{}, fmt.Errorf("list legal holds: %w", err)
	}
	defer holdRows.Close()
	for holdRows.Next() {
		item, scanErr := scanDataLegalHold(holdRows)
		if scanErr != nil {
			return domain.DataGovernanceOverview{}, fmt.Errorf("scan legal hold: %w", scanErr)
		}
		overview.LegalHolds = append(overview.LegalHolds, item)
	}
	if err := holdRows.Err(); err != nil {
		return domain.DataGovernanceOverview{}, fmt.Errorf("iterate legal holds: %w", err)
	}

	jobRows, err := s.pool.Query(ctx, dataGovernanceJobSelect+` WHERE tenant_id=$1 ORDER BY created_at DESC,id DESC LIMIT 200`, tenantID)
	if err != nil {
		return domain.DataGovernanceOverview{}, fmt.Errorf("list data governance jobs: %w", err)
	}
	defer jobRows.Close()
	for jobRows.Next() {
		item, scanErr := scanDataGovernanceJob(jobRows)
		if scanErr != nil {
			return domain.DataGovernanceOverview{}, fmt.Errorf("scan data governance job: %w", scanErr)
		}
		overview.Jobs = append(overview.Jobs, item)
	}
	if err := jobRows.Err(); err != nil {
		return domain.DataGovernanceOverview{}, fmt.Errorf("iterate data governance jobs: %w", err)
	}
	return overview, nil
}

func (s *PostgresStore) CreateRetentionPolicy(ctx context.Context, actor, tenantSlug string, input domain.RetentionPolicyInput) (domain.RetentionPolicy, error) {
	input, valid := domain.NormalizeRetentionPolicyInput(input)
	if !valid {
		return domain.RetentionPolicy{}, ErrInvalidDataGovernance
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.RetentionPolicy{}, fmt.Errorf("begin retention policy creation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.RetentionPolicy{}, err
	}
	if role != "owner" && role != "admin" {
		return domain.RetentionPolicy{}, ErrForbidden
	}
	var currentID uuid.UUID
	var currentDays, currentRevision int
	err = tx.QueryRow(ctx, `
		SELECT id,retention_days,revision FROM data_retention_policies
		WHERE tenant_id=$1 AND scope_kind=$2 AND scope_ref=$3 AND data_class=$4 AND state='active'
		FOR UPDATE`, tenantID, input.ScopeKind, input.ScopeRef, input.DataClass).Scan(&currentID, &currentDays, &currentRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		currentRevision = 0
	} else if err != nil {
		return domain.RetentionPolicy{}, fmt.Errorf("lock active retention policy: %w", err)
	}
	if input.ExpectedRevision != currentRevision {
		return domain.RetentionPolicy{}, ErrRevisionConflict
	}
	var pending bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM data_retention_policies WHERE tenant_id=$1 AND scope_kind=$2 AND scope_ref=$3 AND data_class=$4 AND state='awaiting_approval')`, tenantID, input.ScopeKind, input.ScopeRef, input.DataClass).Scan(&pending); err != nil {
		return domain.RetentionPolicy{}, fmt.Errorf("check pending retention policy: %w", err)
	}
	if pending {
		return domain.RetentionPolicy{}, ErrConflict
	}
	impactRecords, err := retentionImpactCount(ctx, tx, tenantID, input)
	if err != nil {
		return domain.RetentionPolicy{}, err
	}
	hasHold, err := matchingLegalHold(ctx, tx, tenantID, input.ScopeKind, input.ScopeRef, []domain.DataClass{input.DataClass})
	if err != nil {
		return domain.RetentionPolicy{}, err
	}
	state := "active"
	if currentRevision > 0 && input.RetentionDays < currentDays {
		state = "awaiting_approval"
	}
	if state == "active" && currentRevision > 0 {
		if _, err := tx.Exec(ctx, `UPDATE data_retention_policies SET state='superseded',updated_at=now() WHERE id=$1`, currentID); err != nil {
			return domain.RetentionPolicy{}, fmt.Errorf("supersede retention policy: %w", err)
		}
	}
	item, err := scanRetentionPolicy(tx.QueryRow(ctx, `
		INSERT INTO data_retention_policies (
			tenant_id,scope_kind,scope_ref,data_class,retention_days,state,revision,
			impact_records,impact_legal_hold,change_reason,requested_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		RETURNING id,scope_kind,scope_ref,data_class,retention_days,state,revision,impact_records,impact_legal_hold,
		          change_reason,requested_by,decided_by,decided_at,created_at,updated_at`,
		tenantID, input.ScopeKind, input.ScopeRef, input.DataClass, input.RetentionDays, state,
		currentRevision+1, impactRecords, hasHold, input.ChangeReason, actor))
	if err != nil {
		return domain.RetentionPolicy{}, fmt.Errorf("create retention policy: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata)
		VALUES ($1,$2,'data.retention.requested',$3,jsonb_build_object(
			'scope_kind',$4::text,'scope_ref',$5::text,'data_class',$6::text,'retention_days',$7::int,
			'revision',$8::int,'state',$9::text,'impact_records',$10::bigint,'legal_hold',$11::boolean
		))`, tenantID, actor, item.ID.String(), item.ScopeKind, item.ScopeRef, item.DataClass, item.RetentionDays, item.Revision, item.State, item.ImpactRecords, item.ImpactLegalHold); err != nil {
		return domain.RetentionPolicy{}, fmt.Errorf("audit retention policy: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.RetentionPolicy{}, fmt.Errorf("commit retention policy: %w", err)
	}
	return item, nil
}

func (s *PostgresStore) DecideRetentionPolicy(ctx context.Context, actor, tenantSlug string, policyID uuid.UUID, input domain.GovernanceDecisionInput) (domain.RetentionPolicy, error) {
	input, valid := domain.NormalizeGovernanceDecisionInput(input)
	if !valid || policyID == uuid.Nil {
		return domain.RetentionPolicy{}, ErrInvalidDataGovernance
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.RetentionPolicy{}, fmt.Errorf("begin retention decision: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.RetentionPolicy{}, err
	}
	if role != "owner" {
		return domain.RetentionPolicy{}, ErrForbidden
	}
	var requestedBy string
	var revision int
	err = tx.QueryRow(ctx, `SELECT requested_by,revision FROM data_retention_policies WHERE id=$1 AND tenant_id=$2 AND state='awaiting_approval' FOR UPDATE`, policyID, tenantID).Scan(&requestedBy, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RetentionPolicy{}, ErrRevisionConflict
	}
	if err != nil {
		return domain.RetentionPolicy{}, fmt.Errorf("lock pending retention policy: %w", err)
	}
	if revision != input.ExpectedRevision {
		return domain.RetentionPolicy{}, ErrRevisionConflict
	}
	if requestedBy == actor {
		return domain.RetentionPolicy{}, ErrSeparationOfDuties
	}
	if input.Decision == "approved" {
		var scopeKind domain.DataScopeKind
		var scopeRef string
		var dataClass domain.DataClass
		if err := tx.QueryRow(ctx, `SELECT scope_kind,scope_ref,data_class FROM data_retention_policies WHERE id=$1`, policyID).Scan(&scopeKind, &scopeRef, &dataClass); err != nil {
			return domain.RetentionPolicy{}, fmt.Errorf("load pending retention policy identity: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE data_retention_policies SET state='superseded',updated_at=now() WHERE tenant_id=$1 AND scope_kind=$2 AND scope_ref=$3 AND data_class=$4 AND state='active'`, tenantID, scopeKind, scopeRef, dataClass); err != nil {
			return domain.RetentionPolicy{}, fmt.Errorf("supersede approved retention policy: %w", err)
		}
	}
	state := "rejected"
	if input.Decision == "approved" {
		state = "active"
	}
	item, err := scanRetentionPolicy(tx.QueryRow(ctx, `
		UPDATE data_retention_policies SET state=$4,decided_by=$5,decided_at=now(),updated_at=now()
		WHERE id=$1 AND tenant_id=$2 AND revision=$3
		RETURNING id,scope_kind,scope_ref,data_class,retention_days,state,revision,impact_records,impact_legal_hold,
		          change_reason,requested_by,decided_by,decided_at,created_at,updated_at`, policyID, tenantID, input.ExpectedRevision, state, actor))
	if err != nil {
		return domain.RetentionPolicy{}, fmt.Errorf("decide retention policy: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata) VALUES ($1,$2,'data.retention.decided',$3,jsonb_build_object('decision',$4::text,'reason',$5::text,'revision',$6::int))`, tenantID, actor, policyID.String(), input.Decision, input.Reason, input.ExpectedRevision); err != nil {
		return domain.RetentionPolicy{}, fmt.Errorf("audit retention decision: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.RetentionPolicy{}, fmt.Errorf("commit retention decision: %w", err)
	}
	return item, nil
}

func (s *PostgresStore) CreateDataLegalHold(ctx context.Context, actor, tenantSlug string, input domain.DataLegalHoldInput) (domain.DataLegalHold, error) {
	input, valid := domain.NormalizeDataLegalHoldInput(input)
	if !valid {
		return domain.DataLegalHold{}, ErrInvalidDataGovernance
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.DataLegalHold{}, fmt.Errorf("begin legal hold creation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.DataLegalHold{}, err
	}
	if role != "owner" {
		return domain.DataLegalHold{}, ErrForbidden
	}
	if err := lockDataGovernanceTenant(ctx, tx, tenantID); err != nil {
		return domain.DataLegalHold{}, err
	}
	item, err := scanDataLegalHold(tx.QueryRow(ctx, `
		INSERT INTO data_legal_holds (tenant_id,scope_kind,scope_ref,data_class,reason,created_by)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING id,scope_kind,scope_ref,data_class,reason,state,revision,created_by,created_at,released_by,released_at`,
		tenantID, input.ScopeKind, input.ScopeRef, input.DataClass, input.Reason, actor))
	if err != nil {
		return domain.DataLegalHold{}, fmt.Errorf("create legal hold: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata) VALUES ($1,$2,'data.legal_hold.created',$3,jsonb_build_object('scope_kind',$4::text,'scope_ref',$5::text,'data_class',$6::text,'reason',$7::text))`, tenantID, actor, item.ID.String(), item.ScopeKind, item.ScopeRef, item.DataClass, item.Reason); err != nil {
		return domain.DataLegalHold{}, fmt.Errorf("audit legal hold creation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.DataLegalHold{}, fmt.Errorf("commit legal hold creation: %w", err)
	}
	return item, nil
}

func (s *PostgresStore) ReleaseDataLegalHold(ctx context.Context, actor, tenantSlug string, holdID uuid.UUID, expectedRevision int) (domain.DataLegalHold, error) {
	if holdID == uuid.Nil || expectedRevision < 1 {
		return domain.DataLegalHold{}, ErrInvalidDataGovernance
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.DataLegalHold{}, fmt.Errorf("begin legal hold release: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.DataLegalHold{}, err
	}
	if role != "owner" {
		return domain.DataLegalHold{}, ErrForbidden
	}
	if err := lockDataGovernanceTenant(ctx, tx, tenantID); err != nil {
		return domain.DataLegalHold{}, err
	}
	item, err := scanDataLegalHold(tx.QueryRow(ctx, `
		UPDATE data_legal_holds SET state='released',revision=revision+1,released_by=$4,released_at=now()
		WHERE id=$1 AND tenant_id=$2 AND revision=$3 AND state='active'
		RETURNING id,scope_kind,scope_ref,data_class,reason,state,revision,created_by,created_at,released_by,released_at`, holdID, tenantID, expectedRevision, actor))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.DataLegalHold{}, ErrRevisionConflict
	}
	if err != nil {
		return domain.DataLegalHold{}, fmt.Errorf("release legal hold: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata) VALUES ($1,$2,'data.legal_hold.released',$3,jsonb_build_object('revision',$4::int))`, tenantID, actor, holdID.String(), expectedRevision); err != nil {
		return domain.DataLegalHold{}, fmt.Errorf("audit legal hold release: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.DataLegalHold{}, fmt.Errorf("commit legal hold release: %w", err)
	}
	return item, nil
}

func (s *PostgresStore) CreateDataGovernanceJob(ctx context.Context, actor, tenantSlug string, input domain.DataGovernanceJobInput) (domain.DataGovernanceJob, error) {
	input, valid := domain.NormalizeDataGovernanceJobInput(input)
	if !valid {
		return domain.DataGovernanceJob{}, ErrInvalidDataGovernance
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.DataGovernanceJob{}, fmt.Errorf("begin data governance job creation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.DataGovernanceJob{}, err
	}
	if role != "owner" && role != "admin" {
		return domain.DataGovernanceJob{}, ErrForbidden
	}
	if err := lockDataGovernanceTenant(ctx, tx, tenantID); err != nil {
		return domain.DataGovernanceJob{}, err
	}
	if input.Kind == domain.DataJobDeletion {
		hasHold, holdErr := matchingLegalHold(ctx, tx, tenantID, input.ScopeKind, input.ScopeRef, input.DataClasses)
		if holdErr != nil {
			return domain.DataGovernanceJob{}, holdErr
		}
		if hasHold {
			return domain.DataGovernanceJob{}, ErrLegalHold
		}
	}
	hash := governanceJobRequestHash(input)
	state := domain.DataJobQueued
	if input.Kind == domain.DataJobDeletion || input.Kind == domain.DataJobRegionMigration {
		state = domain.DataJobAwaitingApproval
	}
	classes := dataClassStrings(input.DataClasses)
	var insertedID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO data_governance_jobs (
			tenant_id,kind,scope_kind,scope_ref,data_classes,desired_region,state,idempotency_key,request_hash,reason,requested_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (tenant_id,idempotency_key) DO NOTHING RETURNING id`,
		tenantID, input.Kind, input.ScopeKind, input.ScopeRef, classes, input.DesiredRegion, state,
		input.IdempotencyKey, hash, input.Reason, actor).Scan(&insertedID)
	if errors.Is(err, pgx.ErrNoRows) {
		var existingHash string
		existingErr := tx.QueryRow(ctx, `SELECT request_hash FROM data_governance_jobs WHERE tenant_id=$1 AND idempotency_key=$2`, tenantID, input.IdempotencyKey).Scan(&existingHash)
		if existingErr == nil {
			if existingHash != hash {
				return domain.DataGovernanceJob{}, ErrConflict
			}
			item, loadErr := scanDataGovernanceJob(tx.QueryRow(ctx, dataGovernanceJobSelect+` WHERE tenant_id=$1 AND idempotency_key=$2`, tenantID, input.IdempotencyKey))
			if loadErr != nil {
				return domain.DataGovernanceJob{}, fmt.Errorf("load idempotent data governance job: %w", loadErr)
			}
			_ = tx.Rollback(ctx)
			return item, nil
		}
		return domain.DataGovernanceJob{}, fmt.Errorf("load conflicting data governance job: %w", existingErr)
	}
	if err != nil {
		return domain.DataGovernanceJob{}, fmt.Errorf("create data governance job: %w", err)
	}
	item, err := scanDataGovernanceJob(tx.QueryRow(ctx, dataGovernanceJobSelect+` WHERE tenant_id=$1 AND id=$2`, tenantID, insertedID))
	if err != nil {
		return domain.DataGovernanceJob{}, fmt.Errorf("load created data governance job: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata) VALUES ($1,$2,'data.job.created',$3,jsonb_build_object('kind',$4::text,'scope_kind',$5::text,'scope_ref',$6::text,'state',$7::text))`, tenantID, actor, item.ID.String(), item.Kind, item.ScopeKind, item.ScopeRef, item.State); err != nil {
		return domain.DataGovernanceJob{}, fmt.Errorf("audit data governance job: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.DataGovernanceJob{}, fmt.Errorf("commit data governance job: %w", err)
	}
	return item, nil
}

func (s *PostgresStore) DecideDataGovernanceJob(ctx context.Context, actor, tenantSlug string, jobID uuid.UUID, input domain.GovernanceDecisionInput) (domain.DataGovernanceJob, error) {
	input, valid := domain.NormalizeGovernanceDecisionInput(input)
	if !valid || jobID == uuid.Nil {
		return domain.DataGovernanceJob{}, ErrInvalidDataGovernance
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.DataGovernanceJob{}, fmt.Errorf("begin data governance decision: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.DataGovernanceJob{}, err
	}
	if role != "owner" {
		return domain.DataGovernanceJob{}, ErrForbidden
	}
	if err := lockDataGovernanceTenant(ctx, tx, tenantID); err != nil {
		return domain.DataGovernanceJob{}, err
	}
	var requestedBy string
	var revision int
	var kind domain.DataGovernanceJobKind
	var scopeKind domain.DataScopeKind
	var scopeRef string
	var classStrings []string
	err = tx.QueryRow(ctx, `SELECT requested_by,revision,kind,scope_kind,scope_ref,data_classes FROM data_governance_jobs WHERE id=$1 AND tenant_id=$2 AND state='awaiting_approval' FOR UPDATE`, jobID, tenantID).Scan(&requestedBy, &revision, &kind, &scopeKind, &scopeRef, &classStrings)
	if errors.Is(err, pgx.ErrNoRows) || revision != input.ExpectedRevision {
		return domain.DataGovernanceJob{}, ErrRevisionConflict
	}
	if err != nil {
		return domain.DataGovernanceJob{}, fmt.Errorf("lock data governance job: %w", err)
	}
	if requestedBy == actor {
		return domain.DataGovernanceJob{}, ErrSeparationOfDuties
	}
	if input.Decision == "approved" && kind == domain.DataJobDeletion {
		hasHold, holdErr := matchingLegalHold(ctx, tx, tenantID, scopeKind, scopeRef, dataClasses(classStrings))
		if holdErr != nil {
			return domain.DataGovernanceJob{}, holdErr
		}
		if hasHold {
			return domain.DataGovernanceJob{}, ErrLegalHold
		}
	}
	state := domain.DataJobRejected
	var reversible any
	if input.Decision == "approved" {
		state = domain.DataJobQueued
		if kind == domain.DataJobDeletion {
			reversible = time.Now().UTC().Add(24 * time.Hour)
		}
	}
	result, err := tx.Exec(ctx, `
		UPDATE data_governance_jobs SET state=$4,revision=revision+1,approved_by=CASE WHEN $4='queued' THEN $5 ELSE '' END,
		approved_at=CASE WHEN $4='queued' THEN now() ELSE NULL END,reversible_until=$6,updated_at=now(),finished_at=CASE WHEN $4='rejected' THEN now() ELSE NULL END
		WHERE id=$1 AND tenant_id=$2 AND revision=$3`, jobID, tenantID, input.ExpectedRevision, state, actor, reversible)
	if err != nil {
		return domain.DataGovernanceJob{}, fmt.Errorf("decide data governance job: %w", err)
	}
	if result.RowsAffected() == 0 {
		return domain.DataGovernanceJob{}, ErrRevisionConflict
	}
	if _, err := tx.Exec(ctx, `INSERT INTO data_governance_job_decisions (job_id,actor_subject,decision,reason) VALUES ($1,$2,$3,$4)`, jobID, actor, input.Decision, input.Reason); err != nil {
		return domain.DataGovernanceJob{}, fmt.Errorf("record data governance decision: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata) VALUES ($1,$2,'data.job.decided',$3,jsonb_build_object('decision',$4::text,'reason',$5::text,'revision',$6::int))`, tenantID, actor, jobID.String(), input.Decision, input.Reason, input.ExpectedRevision); err != nil {
		return domain.DataGovernanceJob{}, fmt.Errorf("audit data governance decision: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.DataGovernanceJob{}, fmt.Errorf("commit data governance decision: %w", err)
	}
	return s.getDataGovernanceJob(ctx, tenantID, jobID)
}

func (s *PostgresStore) CancelDataGovernanceJob(ctx context.Context, actor, tenantSlug string, jobID uuid.UUID, expectedRevision int) (domain.DataGovernanceJob, error) {
	if jobID == uuid.Nil || expectedRevision < 1 {
		return domain.DataGovernanceJob{}, ErrInvalidDataGovernance
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.DataGovernanceJob{}, fmt.Errorf("begin data governance cancellation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.DataGovernanceJob{}, err
	}
	if role != "owner" && role != "admin" {
		return domain.DataGovernanceJob{}, ErrForbidden
	}
	result, err := tx.Exec(ctx, `
		UPDATE data_governance_jobs SET state='cancelled',revision=revision+1,updated_at=now(),finished_at=now()
		WHERE id=$1 AND tenant_id=$2 AND revision=$3 AND state IN ('requested','awaiting_approval','queued')
		  AND (kind <> 'deletion' OR reversible_until IS NULL OR reversible_until > now())`, jobID, tenantID, expectedRevision)
	if err != nil {
		return domain.DataGovernanceJob{}, fmt.Errorf("cancel data governance job: %w", err)
	}
	if result.RowsAffected() == 0 {
		return domain.DataGovernanceJob{}, ErrGovernanceNotCancellable
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata) VALUES ($1,$2,'data.job.cancelled',$3,jsonb_build_object('revision',$4::int))`, tenantID, actor, jobID.String(), expectedRevision); err != nil {
		return domain.DataGovernanceJob{}, fmt.Errorf("audit data governance cancellation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.DataGovernanceJob{}, fmt.Errorf("commit data governance cancellation: %w", err)
	}
	return s.getDataGovernanceJob(ctx, tenantID, jobID)
}

func (s *PostgresStore) RetryDataGovernanceJob(ctx context.Context, actor, tenantSlug string, jobID uuid.UUID, expectedRevision int, idempotencyKey, reason string) (domain.DataGovernanceJob, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	reason = strings.TrimSpace(reason)
	if jobID == uuid.Nil || expectedRevision < 1 || len(idempotencyKey) < 8 || len(idempotencyKey) > 160 || len(reason) < 3 || len(reason) > 500 {
		return domain.DataGovernanceJob{}, ErrInvalidDataGovernance
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.DataGovernanceJob{}, fmt.Errorf("begin data governance retry: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.DataGovernanceJob{}, err
	}
	if role != "owner" && role != "admin" {
		return domain.DataGovernanceJob{}, ErrForbidden
	}
	var input domain.DataGovernanceJobInput
	var revision int
	var classStrings []string
	err = tx.QueryRow(ctx, `SELECT kind,scope_kind,scope_ref,data_classes,desired_region,revision FROM data_governance_jobs WHERE id=$1 AND tenant_id=$2 AND state='failed' FOR UPDATE`, jobID, tenantID).Scan(&input.Kind, &input.ScopeKind, &input.ScopeRef, &classStrings, &input.DesiredRegion, &revision)
	if errors.Is(err, pgx.ErrNoRows) || revision != expectedRevision {
		return domain.DataGovernanceJob{}, ErrRevisionConflict
	}
	if err != nil {
		return domain.DataGovernanceJob{}, fmt.Errorf("load failed data governance job: %w", err)
	}
	input.DataClasses = dataClasses(classStrings)
	input.IdempotencyKey = idempotencyKey
	input.Reason = reason
	input, valid := domain.NormalizeDataGovernanceJobInput(input)
	if !valid {
		return domain.DataGovernanceJob{}, ErrInvalidDataGovernance
	}
	if input.Kind == domain.DataJobDeletion {
		hasHold, holdErr := matchingLegalHold(ctx, tx, tenantID, input.ScopeKind, input.ScopeRef, input.DataClasses)
		if holdErr != nil {
			return domain.DataGovernanceJob{}, holdErr
		}
		if hasHold {
			return domain.DataGovernanceJob{}, ErrLegalHold
		}
	}
	hash := governanceJobRequestHash(input)
	state := domain.DataJobQueued
	if input.Kind == domain.DataJobDeletion || input.Kind == domain.DataJobRegionMigration {
		state = domain.DataJobAwaitingApproval
	}
	var insertedID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO data_governance_jobs (
			tenant_id,parent_job_id,kind,scope_kind,scope_ref,data_classes,desired_region,state,
			idempotency_key,request_hash,reason,requested_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT (tenant_id,idempotency_key) DO NOTHING RETURNING id`,
		tenantID, jobID, input.Kind, input.ScopeKind, input.ScopeRef, dataClassStrings(input.DataClasses),
		input.DesiredRegion, state, input.IdempotencyKey, hash, input.Reason, actor).Scan(&insertedID)
	if errors.Is(err, pgx.ErrNoRows) {
		var existingHash string
		var existingParent *uuid.UUID
		lookupErr := tx.QueryRow(ctx, `SELECT request_hash,parent_job_id FROM data_governance_jobs WHERE tenant_id=$1 AND idempotency_key=$2`, tenantID, input.IdempotencyKey).Scan(&existingHash, &existingParent)
		if lookupErr != nil {
			return domain.DataGovernanceJob{}, fmt.Errorf("load conflicting retry job: %w", lookupErr)
		}
		if existingHash != hash || existingParent == nil || *existingParent != jobID {
			return domain.DataGovernanceJob{}, ErrConflict
		}
		item, loadErr := scanDataGovernanceJob(tx.QueryRow(ctx, dataGovernanceJobSelect+` WHERE tenant_id=$1 AND idempotency_key=$2`, tenantID, input.IdempotencyKey))
		if loadErr != nil {
			return domain.DataGovernanceJob{}, fmt.Errorf("load idempotent retry job: %w", loadErr)
		}
		return item, nil
	}
	if err != nil {
		return domain.DataGovernanceJob{}, fmt.Errorf("create retry job: %w", err)
	}
	item, err := scanDataGovernanceJob(tx.QueryRow(ctx, dataGovernanceJobSelect+` WHERE tenant_id=$1 AND id=$2`, tenantID, insertedID))
	if err != nil {
		return domain.DataGovernanceJob{}, fmt.Errorf("load created retry job: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata) VALUES ($1,$2,'data.job.retried',$3,jsonb_build_object('parent_job_id',$4::text,'parent_revision',$5::int,'state',$6::text))`, tenantID, actor, item.ID.String(), jobID.String(), expectedRevision, item.State); err != nil {
		return domain.DataGovernanceJob{}, fmt.Errorf("audit data governance retry: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.DataGovernanceJob{}, fmt.Errorf("commit data governance retry: %w", err)
	}
	return item, nil
}

func (s *PostgresStore) getDataGovernanceJob(ctx context.Context, tenantID, jobID uuid.UUID) (domain.DataGovernanceJob, error) {
	item, err := scanDataGovernanceJob(s.pool.QueryRow(ctx, dataGovernanceJobSelect+` WHERE tenant_id=$1 AND id=$2`, tenantID, jobID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.DataGovernanceJob{}, ErrNotFound
	}
	if err != nil {
		return domain.DataGovernanceJob{}, fmt.Errorf("load data governance job: %w", err)
	}
	return item, nil
}

func retentionImpactCount(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, input domain.RetentionPolicyInput) (int64, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -input.RetentionDays)
	var count int64
	var err error
	switch input.DataClass {
	case domain.DataClassRawWebhook:
		err = tx.QueryRow(ctx, `SELECT COUNT(DISTINCT delivery.id) FROM webhook_deliveries delivery JOIN review_jobs job ON job.delivery_id=delivery.id WHERE job.tenant_id=$1 AND delivery.received_at < $2 AND ($3='' OR job.repository=$3)`, tenantID, cutoff, input.ScopeRef).Scan(&count)
	case domain.DataClassFindings:
		err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM review_findings finding JOIN review_jobs job ON job.id=finding.job_id WHERE job.tenant_id=$1 AND finding.created_at < $2 AND ($3='' OR job.repository=$3)`, tenantID, cutoff, input.ScopeRef).Scan(&count)
	case domain.DataClassAudit:
		err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM audit_events WHERE tenant_id=$1 AND created_at < $2 AND ($3='' OR metadata->>'repository'=$3)`, tenantID, cutoff, input.ScopeRef).Scan(&count)
	case domain.DataClassUsage:
		err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM usage_ledger WHERE tenant_id=$1 AND occurred_at < $2 AND ($3='' OR repository=$3)`, tenantID, cutoff, input.ScopeRef).Scan(&count)
	case domain.DataClassOperationalLog:
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("estimate retention impact: %w", err)
	}
	return count, nil
}

func matchingLegalHold(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, scopeKind domain.DataScopeKind, scopeRef string, classes []domain.DataClass) (bool, error) {
	classStrings := dataClassStrings(classes)
	var exists bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM data_legal_holds
			WHERE tenant_id=$1 AND state='active' AND (data_class='all' OR data_class=ANY($2::text[]))
			  AND (scope_kind='tenant' OR (scope_kind=$3 AND scope_ref=$4))
		)`, tenantID, classStrings, scopeKind, scopeRef).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check legal hold: %w", err)
	}
	return exists, nil
}

func lockDataGovernanceTenant(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text,0))`, tenantID.String()); err != nil {
		return fmt.Errorf("lock tenant data governance boundary: %w", err)
	}
	return nil
}

func dataResidencyBoundaries(residency domain.DataResidency) []domain.DataBoundary {
	boundary := func(name, value, detail string, external bool) domain.DataBoundary {
		if strings.TrimSpace(value) == "" {
			value = "external/unknown"
		}
		return domain.DataBoundary{Name: name, Value: value, Observed: residency.ObservedAt != nil, Detail: detail, External: external || value == "external/unknown"}
	}
	return []domain.DataBoundary{
		boundary("Application", residency.PrimaryRegion, "Control-plane runtime region", false),
		boundary("PostgreSQL", residency.PrimaryRegion, "Authoritative tenant state", false),
		boundary("Object storage", residency.ObjectRegion, "Export artifacts and optional evidence", false),
		boundary("Backups", residency.BackupRegion, "Database and object recovery copies", false),
		boundary("Queue", residency.QueueRegion, "Transient delivery; outbox remains authoritative", false),
		boundary("Model providers", residency.ModelBoundary, "BYOK route may cross the selected region", true),
	}
}

func scanRetentionPolicy(row rowScanner) (domain.RetentionPolicy, error) {
	var item domain.RetentionPolicy
	err := row.Scan(&item.ID, &item.ScopeKind, &item.ScopeRef, &item.DataClass, &item.RetentionDays, &item.State, &item.Revision, &item.ImpactRecords, &item.ImpactLegalHold, &item.ChangeReason, &item.RequestedBy, &item.DecidedBy, &item.DecidedAt, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func scanDataLegalHold(row rowScanner) (domain.DataLegalHold, error) {
	var item domain.DataLegalHold
	err := row.Scan(&item.ID, &item.ScopeKind, &item.ScopeRef, &item.DataClass, &item.Reason, &item.State, &item.Revision, &item.CreatedBy, &item.CreatedAt, &item.ReleasedBy, &item.ReleasedAt)
	return item, err
}

func scanDataGovernanceJob(row rowScanner) (domain.DataGovernanceJob, error) {
	var item domain.DataGovernanceJob
	var classStrings []string
	var receiptBytes []byte
	err := row.Scan(&item.ID, &item.ParentJobID, &item.Kind, &item.ScopeKind, &item.ScopeRef, &classStrings, &item.DesiredRegion, &item.ExternalOperationID, &item.State, &item.Progress, &item.Revision, &item.IdempotencyKey, &item.Reason, &item.RequestedBy, &item.ApprovedBy, &item.ApprovedAt, &item.ReversibleUntil, &item.ArtifactReady, &receiptBytes, &item.ErrorCode, &item.ErrorMessage, &item.CreatedAt, &item.UpdatedAt, &item.StartedAt, &item.FinishedAt, &item.NextAttemptAt)
	if err != nil {
		return item, err
	}
	item.DataClasses = dataClasses(classStrings)
	item.Receipt = map[string]any{}
	if len(receiptBytes) > 0 {
		if err := json.Unmarshal(receiptBytes, &item.Receipt); err != nil {
			return item, err
		}
	}
	return item, nil
}

func dataClassStrings(classes []domain.DataClass) []string {
	values := make([]string, len(classes))
	for index, item := range classes {
		values[index] = string(item)
	}
	return values
}

func dataClasses(values []string) []domain.DataClass {
	classes := make([]domain.DataClass, len(values))
	for index, item := range values {
		classes[index] = domain.DataClass(item)
	}
	return classes
}

func governanceJobRequestHash(input domain.DataGovernanceJobInput) string {
	classes := dataClassStrings(input.DataClasses)
	sort.Strings(classes)
	canonical := strings.Join([]string{string(input.Kind), string(input.ScopeKind), input.ScopeRef, strings.Join(classes, ","), input.DesiredRegion, input.Reason}, "\x00")
	digest := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(digest[:])
}
