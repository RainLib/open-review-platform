package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, databaseURL string) (*PostgresStore, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping PostgreSQL: %w", err)
	}
	return &PostgresStore{pool: pool}, nil
}

func (s *PostgresStore) Close() { s.pool.Close() }

func (s *PostgresStore) CreateTenant(ctx context.Context, actor, slug, name string) (domain.Tenant, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Tenant{}, fmt.Errorf("begin tenant creation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var tenant domain.Tenant
	err = tx.QueryRow(ctx, `INSERT INTO tenants (slug, name) VALUES ($1, $2) RETURNING id, slug, name, created_at`, slug, name).
		Scan(&tenant.ID, &tenant.Slug, &tenant.Name, &tenant.CreatedAt)
	if isUniqueViolation(err) {
		return domain.Tenant{}, ErrConflict
	}
	if err != nil {
		return domain.Tenant{}, fmt.Errorf("create tenant: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO memberships (tenant_id, subject, role) VALUES ($1, $2, 'owner')`, tenant.ID, actor); err != nil {
		return domain.Tenant{}, fmt.Errorf("create owner membership: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id, actor_subject, action, target) VALUES ($1, $2, 'tenant.created', $3)`, tenant.ID, actor, tenant.Slug); err != nil {
		return domain.Tenant{}, fmt.Errorf("audit tenant creation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Tenant{}, fmt.Errorf("commit tenant creation: %w", err)
	}
	return tenant, nil
}

func (s *PostgresStore) CreateInstallation(ctx context.Context, actor, tenantSlug string, input domain.InstallationInput) (domain.Installation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Installation{}, fmt.Errorf("begin installation creation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var tenantID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT t.id
		FROM tenants t
		JOIN memberships m ON m.tenant_id = t.id
		WHERE t.slug = $1 AND m.subject = $2 AND m.role IN ('owner', 'admin')`, tenantSlug, actor).Scan(&tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Installation{}, ErrForbidden
	}
	if err != nil {
		return domain.Installation{}, fmt.Errorf("authorize installation creation: %w", err)
	}
	installation := domain.Installation{TenantID: tenantID, Provider: input.Provider, ExternalID: input.ExternalID, RepositoryScope: input.RepositoryScope, APIBaseURL: input.APIBaseURL, CredentialRef: input.CredentialRef}
	err = tx.QueryRow(ctx, `
		INSERT INTO provider_installations (tenant_id, provider, external_id, repository_scope, api_base_url, credential_ref)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, active`, tenantID, input.Provider, input.ExternalID, input.RepositoryScope, input.APIBaseURL, input.CredentialRef).
		Scan(&installation.ID, &installation.Active)
	if isUniqueViolation(err) {
		return domain.Installation{}, ErrConflict
	}
	if err != nil {
		return domain.Installation{}, fmt.Errorf("create provider installation: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id, actor_subject, action, target) VALUES ($1, $2, 'installation.created', $3)`, tenantID, actor, installation.ID.String()); err != nil {
		return domain.Installation{}, fmt.Errorf("audit installation creation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Installation{}, fmt.Errorf("commit installation creation: %w", err)
	}
	return installation, nil
}

func (s *PostgresStore) ListInstallations(ctx context.Context, actor, tenantSlug string, limit int) ([]domain.InstallationSummary, error) {
	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("installation limit must be from 1 to 100")
	}
	tenantID, _, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, provider, external_id, repository_scope, api_base_url, active
		FROM provider_installations
		WHERE tenant_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("list provider installations: %w", err)
	}
	defer rows.Close()

	installations := make([]domain.InstallationSummary, 0)
	for rows.Next() {
		var installation domain.InstallationSummary
		if err := rows.Scan(
			&installation.ID,
			&installation.Provider,
			&installation.ExternalID,
			&installation.RepositoryScope,
			&installation.APIBaseURL,
			&installation.Active,
		); err != nil {
			return nil, fmt.Errorf("scan provider installation: %w", err)
		}
		installations = append(installations, installation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate provider installations: %w", err)
	}
	return installations, nil
}

func (s *PostgresStore) UpsertMembership(ctx context.Context, actor, tenantSlug, subject, role string) (domain.Membership, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Membership{}, fmt.Errorf("begin membership update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var tenantID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT t.id
		FROM tenants t
		JOIN memberships m ON m.tenant_id = t.id
		WHERE t.slug = $1 AND m.subject = $2 AND m.role = 'owner'`, tenantSlug, actor).Scan(&tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Membership{}, ErrForbidden
	}
	if err != nil {
		return domain.Membership{}, fmt.Errorf("authorize membership update: %w", err)
	}
	membership := domain.Membership{TenantID: tenantID, Subject: subject, Role: role}
	if _, err := tx.Exec(ctx, `
		INSERT INTO memberships (tenant_id, subject, role) VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id, subject) DO UPDATE SET role = EXCLUDED.role`, tenantID, subject, role); err != nil {
		return domain.Membership{}, fmt.Errorf("upsert membership: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata) VALUES ($1, $2, 'membership.upserted', $3, jsonb_build_object('role', $4::text))`, tenantID, actor, subject, role); err != nil {
		return domain.Membership{}, fmt.Errorf("audit membership update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Membership{}, fmt.Errorf("commit membership update: %w", err)
	}
	return membership, nil
}

func (s *PostgresStore) Enqueue(ctx context.Context, event domain.InboundEvent) (domain.ReviewJob, bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.ReviewJob{}, false, fmt.Errorf("begin enqueue: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var installation domain.Installation
	err = tx.QueryRow(ctx, `
		SELECT id, tenant_id, provider, external_id, repository_scope, api_base_url, credential_ref, active
		FROM provider_installations
		WHERE provider = $1 AND api_base_url = $2 AND external_id = $3 AND active = TRUE`, event.Provider, event.APIBaseURL, event.InstallationExternalID).
		Scan(&installation.ID, &installation.TenantID, &installation.Provider, &installation.ExternalID, &installation.RepositoryScope, &installation.APIBaseURL, &installation.CredentialRef, &installation.Active)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ReviewJob{}, false, ErrUnknownInstallation
	}
	if err != nil {
		return domain.ReviewJob{}, false, fmt.Errorf("resolve installation: %w", err)
	}

	var deliveryID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO webhook_deliveries (provider, delivery_id, event_name, payload, received_at)
		VALUES ($1, $2, $3, $4::jsonb, $5)
		ON CONFLICT (provider, delivery_id) DO NOTHING
		RETURNING id`, event.Provider, event.DeliveryID, event.EventName, string(event.Payload), event.ReceivedAt).
		Scan(&deliveryID)
	if errors.Is(err, pgx.ErrNoRows) {
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return domain.ReviewJob{}, false, fmt.Errorf("commit duplicate delivery: %w", commitErr)
		}
		return domain.ReviewJob{}, true, nil
	}
	if err != nil {
		return domain.ReviewJob{}, false, fmt.Errorf("record webhook delivery: %w", err)
	}

	job, err := scanJob(tx.QueryRow(ctx, `
		INSERT INTO review_jobs (
			tenant_id, installation_id, delivery_id, provider, api_base_url, repository, clone_url,
			review_number, base_ref, base_sha, head_ref, head_sha, state
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, 'queued')
		RETURNING id, tenant_id, installation_id, $13::text, $14::text, delivery_id, provider, api_base_url, repository, clone_url,
			review_number, base_ref, base_sha, head_ref, head_sha, state, attempts,
			locked_by, locked_until, error_message, created_at, started_at, finished_at`,
		installation.TenantID, installation.ID, deliveryID, event.Provider, installation.APIBaseURL, event.Repository, event.CloneURL,
		event.ReviewNumber, event.BaseRef, event.BaseSHA, event.HeadRef, event.HeadSHA, installation.ExternalID, installation.CredentialRef))
	if err != nil {
		return domain.ReviewJob{}, false, fmt.Errorf("create review job: %w", err)
	}
	workflowErr := createWorkflowRun(ctx, tx, installation, job, event)
	if workflowErr != nil && !errors.Is(workflowErr, errRunCoalesced) {
		return domain.ReviewJob{}, false, fmt.Errorf("create reliable workflow state: %w", workflowErr)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ReviewJob{}, false, fmt.Errorf("commit review job: %w", err)
	}
	if errors.Is(workflowErr, errRunCoalesced) {
		job.State = domain.JobCancelled
		job.ErrorMessage = "coalesced into an active review run for the same head"
		finishedAt := time.Now().UTC()
		job.FinishedAt = &finishedAt
		return job, true, nil
	}
	return job, false, nil
}

func (s *PostgresStore) Claim(ctx context.Context, workerID string) (*domain.ReviewJob, error) {
	job, err := scanJob(s.pool.QueryRow(ctx, `
	WITH candidate AS (
		SELECT j.id
		FROM review_jobs j
		JOIN review_runs r ON r.legacy_job_id = j.id
		WHERE (r.state IN ('admitted', 'preparing', 'analyzing', 'normalizing', 'publishing')
		       OR (r.state = 'acknowledged' AND r.trigger_kind = 'pull_request'))
		  AND ((j.state = 'queued' AND j.available_at <= now()) OR (j.state = 'running' AND j.locked_until < now()))
		ORDER BY j.created_at
		FOR UPDATE OF j SKIP LOCKED
			LIMIT 1
		)
		UPDATE review_jobs AS j
		SET state = 'running', attempts = attempts + 1, locked_by = $1,
			locked_until = now() + interval '2 minutes', started_at = now()
		FROM candidate, provider_installations AS i
		WHERE j.id = candidate.id AND i.id = j.installation_id
		RETURNING j.id, j.tenant_id, j.installation_id, i.external_id, i.credential_ref, j.delivery_id, j.provider, j.api_base_url, j.repository, j.clone_url,
			j.review_number, j.base_ref, j.base_sha, j.head_ref, j.head_sha, j.state, j.attempts,
			j.locked_by, j.locked_until, j.error_message, j.created_at, j.started_at, j.finished_at`, workerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoQueuedJob
	}
	if err != nil {
		return nil, fmt.Errorf("claim review job: %w", err)
	}
	return &job, nil
}

// ClaimForRun is the queue-driven counterpart of Claim. It uses the immutable
// review-run identifier carried by the outbox payload, so a busy tenant cannot
// cause a consumer to work an unrelated queued job.
func (s *PostgresStore) ClaimForRun(ctx context.Context, workerID string, runID uuid.UUID) (*domain.ReviewJob, error) {
	job, err := scanJob(s.pool.QueryRow(ctx, `
		WITH candidate AS (
			SELECT j.id
		FROM review_jobs j
			JOIN review_runs r ON r.legacy_job_id = j.id
			WHERE r.id = $2
			  AND r.state IN ('admitted', 'preparing', 'analyzing', 'normalizing', 'publishing')
			  AND ((j.state = 'queued' AND j.available_at <= now()) OR (j.state = 'running' AND j.locked_until < now()))
			FOR UPDATE OF j SKIP LOCKED
		)
		UPDATE review_jobs AS j
		SET state = 'running', attempts = attempts + 1, locked_by = $1,
			locked_until = now() + interval '2 minutes', started_at = now()
		FROM candidate, provider_installations AS i
		WHERE j.id = candidate.id AND i.id = j.installation_id
		RETURNING j.id, j.tenant_id, j.installation_id, i.external_id, i.credential_ref, j.delivery_id, j.provider, j.api_base_url, j.repository, j.clone_url,
			j.review_number, j.base_ref, j.base_sha, j.head_ref, j.head_sha, j.state, j.attempts,
			j.locked_by, j.locked_until, j.error_message, j.created_at, j.started_at, j.finished_at`, workerID, runID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoQueuedJob
	}
	if err != nil {
		return nil, fmt.Errorf("claim review job for run: %w", err)
	}
	return &job, nil
}

// ReviewModeForJob loads the immutable mode selected when this run was
// acknowledged. It deliberately reads from review_runs, not mutable deployment
// configuration, so a retry preserves the original scope contract.
func (s *PostgresStore) ReviewModeForJob(ctx context.Context, jobID uuid.UUID) (domain.ReviewMode, error) {
	var mode domain.ReviewMode
	err := s.pool.QueryRow(ctx, `SELECT review_mode FROM review_runs WHERE legacy_job_id = $1`, jobID).Scan(&mode)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("load review mode for job: %w", err)
	}
	if !mode.Valid() {
		return "", fmt.Errorf("stored review mode %q is invalid", mode)
	}
	return mode, nil
}

// ReviewJobForRun loads the provider-facing job for a durable run without
// claiming it. Terminal-status consumers use this after a cancellation or
// supersession, when the backing job is intentionally no longer claimable by a
// runner but the provider check still needs a final state.
func (s *PostgresStore) ReviewJobForRun(ctx context.Context, runID uuid.UUID) (domain.ReviewJob, domain.RunState, error) {
	var job domain.ReviewJob
	var state domain.RunState
	var lockedBy, errorMessage *string
	var lockedUntil, startedAt, finishedAt *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT j.id, j.tenant_id, j.installation_id, i.external_id, i.credential_ref, j.delivery_id, j.provider, j.api_base_url, j.repository, j.clone_url,
		       j.review_number, j.base_ref, j.base_sha, j.head_ref, j.head_sha, j.state, j.attempts,
		       j.locked_by, j.locked_until, j.error_message, j.created_at, j.started_at, j.finished_at,
		       r.state
		FROM review_runs r
		JOIN review_jobs j ON j.id = r.legacy_job_id
		JOIN provider_installations i ON i.id = j.installation_id
		WHERE r.id = $1`, runID).Scan(
		&job.ID, &job.TenantID, &job.InstallationID, &job.InstallationExternalID, &job.CredentialRef, &job.DeliveryID, &job.Provider, &job.APIBaseURL, &job.Repository, &job.CloneURL,
		&job.ReviewNumber, &job.BaseRef, &job.BaseSHA, &job.HeadRef, &job.HeadSHA, &job.State, &job.Attempts,
		&lockedBy, &lockedUntil, &errorMessage, &job.CreatedAt, &startedAt, &finishedAt,
		&state,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ReviewJob{}, "", ErrNotFound
	}
	if err != nil {
		return domain.ReviewJob{}, "", fmt.Errorf("load review job for terminal run: %w", err)
	}
	if lockedBy != nil {
		job.LockedBy = *lockedBy
	}
	if errorMessage != nil {
		job.ErrorMessage = *errorMessage
	}
	job.LockedUntil, job.StartedAt, job.FinishedAt = lockedUntil, startedAt, finishedAt
	return job, state, nil
}

// RenewClaim keeps an in-flight execution recoverable: a process that exits
// cannot strand its job for the full OCR timeout, while a healthy process
// retains exclusive ownership throughout a long review.
func (s *PostgresStore) RenewClaim(ctx context.Context, jobID uuid.UUID, workerID string, lease time.Duration) error {
	if lease <= 0 {
		return fmt.Errorf("renew review job claim: lease must be positive")
	}
	command, err := s.pool.Exec(ctx, `
		UPDATE review_jobs
		SET locked_until = now() + $3::interval
		WHERE id = $1 AND state = 'running' AND locked_by = $2`, jobID, workerID, lease.String())
	if err != nil {
		return fmt.Errorf("renew review job claim: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrJobClaimLost
	}
	return nil
}

func (s *PostgresStore) SaveFindings(ctx context.Context, jobID uuid.UUID, findings []domain.Finding) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin findings: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, finding := range findings {
		fingerprint := findingFingerprint(finding)
		_, err := tx.Exec(ctx, `
			INSERT INTO review_findings (job_id, path, start_line, end_line, severity, category, body, suggestion, fingerprint)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (job_id, fingerprint) DO NOTHING`,
			jobID, finding.Path, finding.StartLine, finding.EndLine, finding.Severity, finding.Category, finding.Body, finding.Suggestion, fingerprint)
		if err != nil {
			return fmt.Errorf("save finding: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit findings: %w", err)
	}
	return nil
}

func (s *PostgresStore) Succeed(ctx context.Context, jobID uuid.UUID, workerID string) error {
	return s.finish(ctx, jobID, workerID, domain.JobSucceeded, "")
}

func (s *PostgresStore) Fail(ctx context.Context, jobID uuid.UUID, workerID, message string) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE review_jobs
		SET state = CASE WHEN attempts >= 5 THEN 'failed' ELSE 'queued' END,
			error_message = $1,
			available_at = CASE WHEN attempts >= 5 THEN available_at ELSE now() + (LEAST(900, 5 * power(2, attempts)) * interval '1 second') END,
			locked_by = NULL,
			locked_until = NULL,
			finished_at = CASE WHEN attempts >= 5 THEN now() ELSE NULL END
		WHERE id = $2 AND state = 'running' AND locked_by = $3`, message, jobID, workerID)
	if err != nil {
		return fmt.Errorf("retry or fail review job: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrJobClaimLost
	}
	return nil
}

// FailTerminal records a non-retryable execution error. Time-budget exhaustion
// is one such error: a replay against the same commit and model budget only
// recreates the wait and leaves the provider check misleadingly pending.
func (s *PostgresStore) FailTerminal(ctx context.Context, jobID uuid.UUID, workerID, message string) error {
	return s.finish(ctx, jobID, workerID, domain.JobFailed, message)
}

func (s *PostgresStore) Cancel(ctx context.Context, jobID uuid.UUID, workerID string) error {
	return s.finish(ctx, jobID, workerID, domain.JobCancelled, "cancelled before provider publication")
}

func (s *PostgresStore) finish(ctx context.Context, jobID uuid.UUID, workerID string, state domain.JobState, message string) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE review_jobs
		SET state = $1, error_message = $2, finished_at = now(), locked_until = NULL
		WHERE id = $3 AND state = 'running' AND locked_by = $4`, state, message, jobID, workerID)
	if err != nil {
		return fmt.Errorf("finish review job: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrJobClaimLost
	}
	return nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanJob(row rowScanner) (domain.ReviewJob, error) {
	var job domain.ReviewJob
	var lockedBy *string
	var errorMessage *string
	var lockedUntil, startedAt, finishedAt *time.Time
	err := row.Scan(
		&job.ID, &job.TenantID, &job.InstallationID, &job.InstallationExternalID, &job.CredentialRef, &job.DeliveryID, &job.Provider, &job.APIBaseURL, &job.Repository, &job.CloneURL,
		&job.ReviewNumber, &job.BaseRef, &job.BaseSHA, &job.HeadRef, &job.HeadSHA, &job.State, &job.Attempts,
		&lockedBy, &lockedUntil, &errorMessage, &job.CreatedAt, &startedAt, &finishedAt,
	)
	if err != nil {
		return domain.ReviewJob{}, err
	}
	if lockedBy != nil {
		job.LockedBy = *lockedBy
	}
	if errorMessage != nil {
		job.ErrorMessage = *errorMessage
	}
	job.LockedUntil, job.StartedAt, job.FinishedAt = lockedUntil, startedAt, finishedAt
	return job, nil
}

func findingFingerprint(f domain.Finding) string {
	return fmt.Sprintf("%s:%d:%d:%s:%s", f.Path, f.StartLine, f.EndLine, f.Category, f.Body)
}

func isUniqueViolation(err error) bool {
	var databaseError *pgconn.PgError
	return errors.As(err, &databaseError) && databaseError.Code == "23505"
}
