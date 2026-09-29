package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

func TestDataGovernanceApprovalLegalHoldAndJobLifecycle(t *testing.T) {
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
	tenantSlug := "data-" + tenantID.String()[:8]
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Data Governance Integration')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner-one','owner'),($1,'owner-two','owner'),($1,'admin','admin'),($1,'viewer','viewer')`, tenantID); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) }()

	overview, err := postgres.GetDataGovernanceOverview(ctx, "owner-one", tenantSlug)
	if err != nil || overview.Residency.Configured || len(overview.Boundaries) == 0 {
		t.Fatalf("initial overview=%#v error=%v", overview, err)
	}
	if _, err := postgres.GetDataGovernanceOverview(ctx, "viewer", tenantSlug); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer overview error=%v, want ErrForbidden", err)
	}

	active, err := postgres.CreateRetentionPolicy(ctx, "admin", tenantSlug, domain.RetentionPolicyInput{
		ScopeKind: domain.DataScopeTenant, DataClass: domain.DataClassFindings, RetentionDays: 90,
		ExpectedRevision: 0, ChangeReason: "establish default findings retention",
	})
	if err != nil || active.State != "active" || active.Revision != 1 {
		t.Fatalf("active retention=%#v error=%v", active, err)
	}
	pending, err := postgres.CreateRetentionPolicy(ctx, "admin", tenantSlug, domain.RetentionPolicyInput{
		ScopeKind: domain.DataScopeTenant, DataClass: domain.DataClassFindings, RetentionDays: 30,
		ExpectedRevision: 1, ChangeReason: "reduce storage after impact review",
	})
	if err != nil || pending.State != "awaiting_approval" || pending.Revision != 2 {
		t.Fatalf("pending retention=%#v error=%v", pending, err)
	}
	if _, err := postgres.DecideRetentionPolicy(ctx, "admin", tenantSlug, pending.ID, domain.GovernanceDecisionInput{Decision: "approved", Reason: "self approval", ExpectedRevision: 2}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin decision error=%v, want ErrForbidden", err)
	}
	approved, err := postgres.DecideRetentionPolicy(ctx, "owner-two", tenantSlug, pending.ID, domain.GovernanceDecisionInput{Decision: "approved", Reason: "impact and evidence accepted", ExpectedRevision: 2})
	if err != nil || approved.State != "active" || approved.DecidedBy != "owner-two" {
		t.Fatalf("approved retention=%#v error=%v", approved, err)
	}

	hold, err := postgres.CreateDataLegalHold(ctx, "owner-one", tenantSlug, domain.DataLegalHoldInput{
		ScopeKind: domain.DataScopeRepository, ScopeRef: "RainLib/open-review-platform", DataClass: domain.DataClassAll, Reason: "incident investigation",
	})
	if err != nil || hold.State != "active" {
		t.Fatalf("legal hold=%#v error=%v", hold, err)
	}
	deletionInput := domain.DataGovernanceJobInput{
		Kind: domain.DataJobDeletion, ScopeKind: domain.DataScopeRepository, ScopeRef: "RainLib/open-review-platform",
		DataClasses: []domain.DataClass{domain.DataClassFindings}, IdempotencyKey: "delete-findings-0001", Reason: "approved repository cleanup",
	}
	if _, err := postgres.CreateDataGovernanceJob(ctx, "admin", tenantSlug, deletionInput); !errors.Is(err, ErrLegalHold) {
		t.Fatalf("held deletion error=%v, want ErrLegalHold", err)
	}
	released, err := postgres.ReleaseDataLegalHold(ctx, "owner-one", tenantSlug, hold.ID, hold.Revision)
	if err != nil || released.State != "released" || released.Revision != 2 {
		t.Fatalf("released hold=%#v error=%v", released, err)
	}

	deletion, err := postgres.CreateDataGovernanceJob(ctx, "admin", tenantSlug, deletionInput)
	if err != nil || deletion.State != domain.DataJobAwaitingApproval || deletion.Revision != 1 {
		t.Fatalf("deletion job=%#v error=%v", deletion, err)
	}
	if _, err := postgres.DecideDataGovernanceJob(ctx, "admin", tenantSlug, deletion.ID, domain.GovernanceDecisionInput{Decision: "approved", Reason: "self approval", ExpectedRevision: 1}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin decision error=%v, want ErrForbidden", err)
	}
	queued, err := postgres.DecideDataGovernanceJob(ctx, "owner-two", tenantSlug, deletion.ID, domain.GovernanceDecisionInput{Decision: "approved", Reason: "scope and hold state verified", ExpectedRevision: 1})
	if err != nil || queued.State != domain.DataJobQueued || queued.Revision != 2 || queued.ReversibleUntil == nil {
		t.Fatalf("queued deletion=%#v error=%v", queued, err)
	}
	cancelled, err := postgres.CancelDataGovernanceJob(ctx, "admin", tenantSlug, deletion.ID, queued.Revision)
	if err != nil || cancelled.State != domain.DataJobCancelled || cancelled.Revision != 3 {
		t.Fatalf("cancelled deletion=%#v error=%v", cancelled, err)
	}

	exportInput := domain.DataGovernanceJobInput{
		Kind: domain.DataJobExport, ScopeKind: domain.DataScopeTenant,
		DataClasses: []domain.DataClass{domain.DataClassAudit}, IdempotencyKey: "export-audit-0001", Reason: "quarterly audit export",
	}
	exportJob, err := postgres.CreateDataGovernanceJob(ctx, "owner-one", tenantSlug, exportInput)
	if err != nil || exportJob.State != domain.DataJobQueued || exportJob.ArtifactReady {
		t.Fatalf("export job=%#v error=%v", exportJob, err)
	}
	idempotent, err := postgres.CreateDataGovernanceJob(ctx, "owner-one", tenantSlug, exportInput)
	if err != nil || idempotent.ID != exportJob.ID {
		t.Fatalf("idempotent export=%#v error=%v", idempotent, err)
	}
	exportInput.Reason = "different payload for same key"
	if _, err := postgres.CreateDataGovernanceJob(ctx, "owner-one", tenantSlug, exportInput); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting idempotency error=%v, want ErrConflict", err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE data_governance_jobs SET state='failed',revision=2,error_code='executor_unavailable',error_message='test failure' WHERE id=$1`, exportJob.ID); err != nil {
		t.Fatal(err)
	}
	retry, err := postgres.RetryDataGovernanceJob(ctx, "admin", tenantSlug, exportJob.ID, 2, "retry-export-audit-0001", "executor capacity restored")
	if err != nil || retry.State != domain.DataJobQueued || retry.ParentJobID == nil || *retry.ParentJobID != exportJob.ID {
		t.Fatalf("retry job=%#v error=%v", retry, err)
	}
	idempotentRetry, err := postgres.RetryDataGovernanceJob(ctx, "admin", tenantSlug, exportJob.ID, 2, "retry-export-audit-0001", "executor capacity restored")
	if err != nil || idempotentRetry.ID != retry.ID {
		t.Fatalf("idempotent retry=%#v error=%v", idempotentRetry, err)
	}

	overview, err = postgres.GetDataGovernanceOverview(ctx, "owner-one", tenantSlug)
	if err != nil || len(overview.Policies) < 2 || len(overview.LegalHolds) != 1 || len(overview.Jobs) != 3 {
		t.Fatalf("final overview policies=%d holds=%d jobs=%d error=%v", len(overview.Policies), len(overview.LegalHolds), len(overview.Jobs), err)
	}
}

func TestDataGovernanceExecutorLeasesExportsAndPersistsEncryptedArtifact(t *testing.T) {
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
	tenantSlug := "export-" + tenantID.String()[:8]
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Governance Executor')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner','owner'),($1,'owner-two','owner')`, tenantID); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) }()

	exportJob, err := postgres.CreateDataGovernanceJob(ctx, "owner", tenantSlug, domain.DataGovernanceJobInput{
		Kind: domain.DataJobExport, ScopeKind: domain.DataScopeTenant,
		DataClasses:    []domain.DataClass{domain.DataClassAudit, domain.DataClassOperationalLog},
		IdempotencyKey: "executor-export-0001", Reason: "integration export",
	})
	if err != nil {
		t.Fatal(err)
	}
	deletion, err := postgres.CreateDataGovernanceJob(ctx, "owner", tenantSlug, domain.DataGovernanceJobInput{
		Kind: domain.DataJobDeletion, ScopeKind: domain.DataScopeTenant,
		DataClasses:    []domain.DataClass{domain.DataClassFindings},
		IdempotencyKey: "executor-delete-0001", Reason: "integration deletion",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.DecideDataGovernanceJob(ctx, "owner-two", tenantSlug, deletion.ID, domain.GovernanceDecisionInput{Decision: "approved", Reason: "independent approval", ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}

	target, err := postgres.ClaimDataGovernanceJob(ctx, "worker-1", time.Minute)
	if err != nil || target.ID != exportJob.ID || target.Kind != domain.DataJobExport {
		t.Fatalf("target=%#v error=%v", target, err)
	}
	payload, counts, err := postgres.BuildDataGovernanceExport(ctx, *target, 100)
	if err != nil || len(payload) == 0 || counts[string(domain.DataClassAudit)] < 1 || counts[string(domain.DataClassOperationalLog)] != 0 {
		t.Fatalf("payload=%d counts=%v error=%v", len(payload), counts, err)
	}
	artifact := domain.DataGovernanceArtifact{
		JobID: target.ID, TenantID: target.TenantID, Filename: "export.json", ContentType: "application/json",
		KeyVersion: "test", Nonce: []byte("nonce"), Ciphertext: []byte("ciphertext"), PlaintextSHA256: "digest",
		PlaintextBytes: int64(len(payload)), ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	if err := postgres.CompleteDataGovernanceExport(ctx, *target, artifact, map[string]any{"counts": counts}); err != nil {
		t.Fatal(err)
	}
	loaded, err := postgres.GetDataGovernanceArtifact(ctx, "owner", tenantSlug, exportJob.ID)
	if err != nil || string(loaded.Ciphertext) != "ciphertext" {
		t.Fatalf("artifact=%#v error=%v", loaded, err)
	}
	if _, err := postgres.ClaimDataGovernanceJob(ctx, "worker-2", time.Minute); !errors.Is(err, ErrNoQueuedGovernanceJob) {
		t.Fatalf("deletion must remain unclaimable during its cancellation window: %v", err)
	}
}

func TestDataGovernanceAuditRangeExportIsHalfOpenAndTenantScoped(t *testing.T) {
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

	tenantID, otherTenantID := uuid.New(), uuid.New()
	tenantSlug := "audit-range-" + tenantID.String()[:8]
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO tenants (id,slug,name) VALUES
		($1,$2,'Bounded audit export'),($3,$4,'Other tenant')`,
		tenantID, tenantSlug, otherTenantID, "other-"+otherTenantID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner','owner')`, tenantID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = ANY($1)`, []uuid.UUID{tenantID, otherTenantID})
	}()

	start := time.Date(2026, time.January, 10, 0, 0, 0, 0, time.UTC)
	end := start.Add(48 * time.Hour)
	entries := []struct {
		tenant uuid.UUID
		target string
		at     time.Time
	}{
		{tenantID, "before-range", start.Add(-time.Nanosecond)},
		{tenantID, "at-start", start},
		{tenantID, "inside-range", start.Add(24 * time.Hour)},
		{tenantID, "at-end", end},
		{otherTenantID, "other-tenant", start.Add(time.Hour)},
	}
	for _, entry := range entries {
		if _, err := postgres.pool.Exec(ctx, `
			INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata,created_at)
			VALUES ($1,'audit-test','audit.test',$2,'{}'::jsonb,$3)`, entry.tenant, entry.target, entry.at); err != nil {
			t.Fatal(err)
		}
	}

	job, err := postgres.CreateDataGovernanceJob(ctx, "owner", tenantSlug, domain.DataGovernanceJobInput{
		Kind:           domain.DataJobExport,
		ScopeKind:      domain.DataScopeAuditRange,
		ScopeRef:       start.Format(time.RFC3339) + "/" + end.Format(time.RFC3339),
		DataClasses:    []domain.DataClass{domain.DataClassAudit},
		IdempotencyKey: "audit-range-export-0001",
		Reason:         "bounded audit export verification",
	})
	if err != nil {
		t.Fatal(err)
	}
	target, err := postgres.ClaimDataGovernanceJob(ctx, "audit-range-worker", time.Minute)
	if err != nil || target == nil || target.ID != job.ID {
		t.Fatalf("claimed target=%#v error=%v", target, err)
	}
	payload, counts, err := postgres.BuildDataGovernanceExport(ctx, *target, 100)
	if err != nil || counts[string(domain.DataClassAudit)] != 2 {
		t.Fatalf("payload=%s counts=%v error=%v", payload, counts, err)
	}
	var envelope struct {
		Records map[string][]struct {
			Target string `json:"target"`
		} `json:"records"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Records[string(domain.DataClassAudit)]) != 2 ||
		envelope.Records[string(domain.DataClassAudit)][0].Target != "at-start" ||
		envelope.Records[string(domain.DataClassAudit)][1].Target != "inside-range" {
		t.Fatalf("exported audit records=%#v, want only [at-start inside-range]", envelope.Records[string(domain.DataClassAudit)])
	}
}

func TestDataGovernanceDeletionRedactsContentAndRechecksLegalHold(t *testing.T) {
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
	tenantSlug := "erase-" + tenantID.String()[:8]
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Governance Erasure')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner','owner'),($1,'owner-two','owner')`, tenantID); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) }()
	installationID, deliveryID, jobID := uuid.New(), uuid.New(), uuid.New()
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO provider_installations (id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref)
		VALUES ($1,$2,'github',$3,'RainLib/erase','https://api.github.com','test')`, installationID, tenantID, "erase-"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO webhook_deliveries (id,provider,delivery_id,event_name,payload) VALUES ($1,'github',$2,'pull_request','{"secret":"value"}')`, deliveryID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO review_jobs (id,tenant_id,installation_id,delivery_id,provider,api_base_url,repository,clone_url,review_number,base_ref,head_ref,head_sha,state)
		VALUES ($1,$2,$3,$4,'github','https://api.github.com','RainLib/erase','https://github.com/RainLib/erase.git',1,'main','feature','abc','succeeded')`, jobID, tenantID, installationID, deliveryID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO review_findings (job_id,path,body,suggestion,code_excerpt,code_excerpt_start_line,proposed_patch,fingerprint) VALUES ($1,'secret.go','sensitive finding','sensitive patch','secret source',1,'secret diff',$2)`, jobID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata) VALUES ($1,'person@example.com','review.opened','sensitive-target',jsonb_build_object('repository','RainLib/erase','secret','value'))`, tenantID); err != nil {
		t.Fatal(err)
	}

	request, err := postgres.CreateDataGovernanceJob(ctx, "owner", tenantSlug, domain.DataGovernanceJobInput{
		Kind: domain.DataJobDeletion, ScopeKind: domain.DataScopeRepository, ScopeRef: "RainLib/erase",
		DataClasses:    []domain.DataClass{domain.DataClassRawWebhook, domain.DataClassFindings, domain.DataClassAudit},
		IdempotencyKey: "erase-content-0001", Reason: "verified erasure request",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.DecideDataGovernanceJob(ctx, "owner-two", tenantSlug, request.ID, domain.GovernanceDecisionInput{Decision: "approved", Reason: "scope and impact verified", ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE data_governance_jobs SET reversible_until=now()-interval '1 second' WHERE id=$1`, request.ID); err != nil {
		t.Fatal(err)
	}
	target, err := postgres.ClaimDataGovernanceJob(ctx, "eraser-1", time.Minute)
	if err != nil || target.ID != request.ID {
		t.Fatalf("target=%#v error=%v", target, err)
	}
	exported, exportCount, err := postgres.exportDataClass(ctx, *target, domain.DataClassFindings, time.Now().Add(-24*time.Hour), time.Now().Add(24*time.Hour), 10)
	var exportedFindings []map[string]any
	if err == nil {
		err = json.Unmarshal(exported, &exportedFindings)
	}
	if err != nil || exportCount != 1 || len(exportedFindings) != 1 || exportedFindings[0]["code_excerpt"] != "secret source" || exportedFindings[0]["proposed_patch"] != "secret diff" {
		t.Fatalf("finding export omits governed source evidence: count=%d payload=%s error=%v", exportCount, exported, err)
	}
	receipt, err := postgres.ExecuteDataGovernanceDeletion(ctx, *target)
	if err != nil {
		t.Fatal(err)
	}
	if err := postgres.CompleteDataGovernanceOperation(ctx, *target, receipt); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	var findingBody, suggestion, excerpt, patch, actor, auditTarget string
	var excerptStart int
	if err := postgres.pool.QueryRow(ctx, `SELECT payload FROM webhook_deliveries WHERE id=$1`, deliveryID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT body,suggestion,code_excerpt,code_excerpt_start_line,proposed_patch FROM review_findings WHERE job_id=$1`, jobID).Scan(&findingBody, &suggestion, &excerpt, &excerptStart, &patch); err != nil {
		t.Fatal(err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT actor_subject,target FROM audit_events WHERE tenant_id=$1 AND action='review.opened'`, tenantID).Scan(&actor, &auditTarget); err != nil {
		t.Fatal(err)
	}
	var tombstones int
	if err := postgres.pool.QueryRow(ctx, `SELECT COUNT(*) FROM data_governance_erasure_tombstones WHERE job_id=$1`, request.ID).Scan(&tombstones); err != nil {
		t.Fatal(err)
	}
	if payload["_redacted"] != true || !strings.Contains(findingBody, "redacted by data governance") || suggestion != "" || excerpt != "" || excerptStart != 0 || patch != "" || actor != "redacted" || auditTarget != "redacted" || tombstones != 3 {
		t.Fatalf("payload=%v finding=%q suggestion=%q excerpt=%q patch=%q actor=%q target=%q tombstones=%d", payload, findingBody, suggestion, excerpt, patch, actor, auditTarget, tombstones)
	}

	blocked, err := postgres.CreateDataGovernanceJob(ctx, "owner", tenantSlug, domain.DataGovernanceJobInput{
		Kind: domain.DataJobDeletion, ScopeKind: domain.DataScopeRepository, ScopeRef: "RainLib/erase",
		DataClasses: []domain.DataClass{domain.DataClassFindings}, IdempotencyKey: "erase-content-0002", Reason: "second verified erasure",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.DecideDataGovernanceJob(ctx, "owner-two", tenantSlug, blocked.ID, domain.GovernanceDecisionInput{Decision: "approved", Reason: "scope verified again", ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE data_governance_jobs SET reversible_until=now()-interval '1 second' WHERE id=$1`, blocked.ID); err != nil {
		t.Fatal(err)
	}
	blockedTarget, err := postgres.ClaimDataGovernanceJob(ctx, "eraser-2", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.CreateDataLegalHold(ctx, "owner", tenantSlug, domain.DataLegalHoldInput{ScopeKind: domain.DataScopeRepository, ScopeRef: "RainLib/erase", DataClass: domain.DataClassFindings, Reason: "late preservation order"}); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.ExecuteDataGovernanceDeletion(ctx, *blockedTarget); !errors.Is(err, ErrLegalHold) {
		t.Fatalf("late legal hold error=%v, want ErrLegalHold", err)
	}
}

func TestDataGovernanceRegionMigrationDefersAndAppliesObservedResidency(t *testing.T) {
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
	tenantSlug := "region-" + tenantID.String()[:8]
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Region Migration')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner','owner'),($1,'owner-two','owner')`, tenantID); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) }()

	request, err := postgres.CreateDataGovernanceJob(ctx, "owner", tenantSlug, domain.DataGovernanceJobInput{
		Kind: domain.DataJobRegionMigration, ScopeKind: domain.DataScopeTenant, DesiredRegion: "eu-west-1",
		IdempotencyKey: "region-migration-0001", Reason: "contracted residency requirement",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.DecideDataGovernanceJob(ctx, "owner-two", tenantSlug, request.ID, domain.GovernanceDecisionInput{Decision: "approved", Reason: "deployment plan verified", ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	target, err := postgres.ClaimDataGovernanceJob(ctx, "region-worker", time.Minute)
	if err != nil || target.ID != request.ID {
		t.Fatalf("target=%#v error=%v", target, err)
	}
	nextAttempt := time.Now().UTC().Add(time.Minute)
	if err := postgres.DeferDataGovernanceRegionMigration(ctx, *target, "operation-42", 20, map[string]any{"status": "running"}, nextAttempt); err != nil {
		t.Fatal(err)
	}
	overview, err := postgres.GetDataGovernanceOverview(ctx, "owner", tenantSlug)
	if err != nil || len(overview.Jobs) != 1 || overview.Jobs[0].ExternalOperationID != "operation-42" || overview.Jobs[0].NextAttemptAt == nil || overview.Jobs[0].Progress != 20 {
		t.Fatalf("deferred overview=%#v error=%v", overview, err)
	}
	if _, err := postgres.ClaimDataGovernanceJob(ctx, "other-worker", time.Minute); !errors.Is(err, ErrNoQueuedGovernanceJob) {
		t.Fatalf("deferred operation claimed before available_at: %v", err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE data_governance_jobs SET available_at=now()-interval '1 second' WHERE id=$1`, request.ID); err != nil {
		t.Fatal(err)
	}
	target, err = postgres.ClaimDataGovernanceJob(ctx, "region-worker", time.Minute)
	if err != nil || target.ExternalOperationID != "operation-42" || target.Attempt != 2 {
		t.Fatalf("reconcile target=%#v error=%v", target, err)
	}
	observedAt := time.Now().UTC().Add(-time.Second)
	observed := domain.DataResidency{
		PrimaryRegion: "eu-west-1", BackupRegion: "eu-central-1", ObjectRegion: "eu-west-1",
		QueueRegion: "eu-west-1", ModelBoundary: "eu-west-1", ObservedAt: &observedAt,
	}
	if err := postgres.CompleteDataGovernanceRegionMigration(ctx, *target, observed, map[string]any{"schema": "open-review.region-migration-result.v1", "status": "completed", "operation_id": "operation-42"}); err != nil {
		t.Fatal(err)
	}
	overview, err = postgres.GetDataGovernanceOverview(ctx, "owner", tenantSlug)
	if err != nil || !overview.Residency.Configured || overview.Residency.PrimaryRegion != "eu-west-1" || overview.Residency.ObservedAt == nil || overview.Jobs[0].State != domain.DataJobCompleted || overview.Jobs[0].Progress != 100 {
		t.Fatalf("completed overview=%#v error=%v", overview, err)
	}
}
