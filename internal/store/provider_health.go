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
)

var ErrNoProviderHealthProbe = errors.New("no provider health probe is due")

// RequestInstallationVerification gives a tenant administrator an explicit,
// durable retry point for the read-only provider check. It does not attempt a
// provider request from the control API and never moves a credential into a
// browser-facing process.
func (s *PostgresStore) RequestInstallationVerification(ctx context.Context, actor, tenantSlug string, installationID uuid.UUID) (domain.InstallationSummary, error) {
	if installationID == uuid.Nil {
		return domain.InstallationSummary{}, ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.InstallationSummary{}, err
	}
	defer tx.Rollback(ctx)
	var tenantID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT tenant.id
		FROM tenants tenant
		JOIN memberships membership ON membership.tenant_id=tenant.id
		WHERE tenant.slug=$1 AND membership.subject=$2 AND membership.active=TRUE AND membership.role IN ('owner','admin')`, tenantSlug, actor).Scan(&tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.InstallationSummary{}, ErrForbidden
	}
	if err != nil {
		return domain.InstallationSummary{}, fmt.Errorf("authorize installation verification request: %w", err)
	}
	// Keep the same probe-before-installation lock order as provider completion
	// and scope changes. A retry cannot race a new scope's verification receipt.
	if _, err := tx.Exec(ctx, `
		INSERT INTO provider_health_probes (installation_id)
		SELECT id FROM provider_installations WHERE id=$1 AND tenant_id=$2
		ON CONFLICT (installation_id) DO NOTHING`, installationID, tenantID); err != nil {
		return domain.InstallationSummary{}, fmt.Errorf("seed installation verification probe: %w", err)
	}
	var probeState string
	if err := tx.QueryRow(ctx, `
		SELECT probe.state
		FROM provider_health_probes probe
		JOIN provider_installations installation ON installation.id=probe.installation_id
		WHERE probe.installation_id=$1 AND installation.tenant_id=$2
		FOR UPDATE OF probe`, installationID, tenantID).Scan(&probeState); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.InstallationSummary{}, ErrNotFound
		}
		return domain.InstallationSummary{}, fmt.Errorf("load installation verification probe: %w", err)
	}
	var installation domain.InstallationSummary
	err = tx.QueryRow(ctx, `
		SELECT id,provider,external_id,repository_scope,automatic_reviews,author_scope,minimum_severity,api_base_url,active,verification_state
		FROM provider_installations
		WHERE id=$1 AND tenant_id=$2
		FOR UPDATE`, installationID, tenantID).Scan(
		&installation.ID, &installation.Provider, &installation.ExternalID, &installation.RepositoryScope,
		&installation.AutomaticReviews, &installation.AuthorScope, &installation.MinimumSeverity, &installation.APIBaseURL,
		&installation.Active, &installation.VerificationState)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.InstallationSummary{}, ErrNotFound
	}
	if err != nil {
		return domain.InstallationSummary{}, fmt.Errorf("load installation verification request: %w", err)
	}
	if !installation.Active {
		return domain.InstallationSummary{}, ErrConflict
	}
	if probeState == "running" {
		return domain.InstallationSummary{}, ErrConflict
	}
	if _, err := tx.Exec(ctx, `
		UPDATE provider_health_probes
		SET state='queued',available_at=now(),worker_id=NULL,locked_until=NULL,updated_at=now()
		WHERE installation_id=$1`, installationID); err != nil {
		return domain.InstallationSummary{}, fmt.Errorf("queue installation verification probe: %w", err)
	}
	// A manually requested recheck of an already verified connection refreshes
	// its health receipt without interrupting webhook or CLI admission. A first
	// verification (including a failed or legacy connection) still moves through
	// pending/checking and is deliberately not eligible until it succeeds.
	if installation.VerificationState != domain.InstallationVerificationVerified {
		installation.VerificationState = domain.InstallationVerificationPending
		if _, err := tx.Exec(ctx, `
			UPDATE provider_installations
			SET verification_state=$2,verification_updated_at=now(),updated_at=now()
			WHERE id=$1`, installationID, installation.VerificationState); err != nil {
			return domain.InstallationSummary{}, fmt.Errorf("mark installation verification pending: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata)
		VALUES ($1,$2,'installation.verification_requested',$3,jsonb_build_object('provider',$4::text))`, tenantID, actor, installationID.String(), string(installation.Provider)); err != nil {
		return domain.InstallationSummary{}, fmt.Errorf("audit installation verification request: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.InstallationSummary{}, fmt.Errorf("commit installation verification request: %w", err)
	}
	return installation, nil
}

func (s *PostgresStore) ClaimProviderHealthProbe(ctx context.Context, workerID string, lease time.Duration, installationID *uuid.UUID) (*domain.ProviderProbeTarget, error) {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return nil, fmt.Errorf("provider probe worker id is required")
	}
	if lease <= 0 {
		lease = time.Minute
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		INSERT INTO provider_health_probes (installation_id)
		SELECT id FROM provider_installations WHERE active=true
		ON CONFLICT (installation_id) DO NOTHING`); err != nil {
		return nil, fmt.Errorf("seed provider health probes: %w", err)
	}
	var target domain.ProviderProbeTarget
	err = tx.QueryRow(ctx, `
		WITH candidate AS (
			SELECT probe.installation_id
			FROM provider_health_probes probe
			JOIN provider_installations installation ON installation.id=probe.installation_id
			WHERE installation.active=true AND ($3::uuid IS NULL OR probe.installation_id=$3) AND (
				(probe.state IN ('queued','completed') AND probe.available_at <= now())
				OR (probe.state='running' AND probe.locked_until < now())
			)
			ORDER BY probe.available_at,probe.installation_id
			FOR UPDATE OF probe SKIP LOCKED
			LIMIT 1
		)
		UPDATE provider_health_probes probe SET
			state='running',attempt=probe.attempt+1,worker_id=$1,
			locked_until=now()+$2::interval,updated_at=now()
		FROM candidate,provider_installations installation
		WHERE probe.installation_id=candidate.installation_id
		  AND installation.id=candidate.installation_id
		RETURNING installation.id,installation.tenant_id,installation.provider,
		          installation.external_id,installation.repository_scope,
		          installation.automatic_reviews,installation.minimum_severity,
		          installation.api_base_url,installation.credential_ref,
		          installation.active,installation.verification_state,probe.attempt,probe.worker_id`, workerID, lease.String(), installationID).Scan(
		&target.Installation.ID, &target.Installation.TenantID, &target.Installation.Provider,
		&target.Installation.ExternalID, &target.Installation.RepositoryScope,
		&target.Installation.AutomaticReviews, &target.Installation.MinimumSeverity,
		&target.Installation.APIBaseURL, &target.Installation.CredentialRef,
		&target.Installation.Active, &target.Installation.VerificationState, &target.Attempt, &target.WorkerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoProviderHealthProbe
	}
	if err != nil {
		return nil, fmt.Errorf("claim provider health probe: %w", err)
	}
	if target.Installation.VerificationState == domain.InstallationVerificationPending || target.Installation.VerificationState == domain.InstallationVerificationFailed {
		if _, err := tx.Exec(ctx, `
			UPDATE provider_installations
			SET verification_state='checking',verification_updated_at=now(),updated_at=now()
			WHERE id=$1 AND verification_state IN ('pending','failed')`, target.Installation.ID); err != nil {
			return nil, fmt.Errorf("mark installation verification checking: %w", err)
		}
		target.Installation.VerificationState = domain.InstallationVerificationChecking
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &target, nil
}

func (s *PostgresStore) CompleteProviderHealthProbe(ctx context.Context, target domain.ProviderProbeTarget, result domain.ProviderProbeResult, nextProbeAt time.Time) error {
	if target.Installation.ID == uuid.Nil || target.WorkerID == "" || result.ObservedAt.IsZero() || !nextProbeAt.After(result.ObservedAt) {
		return fmt.Errorf("provider health result is invalid")
	}
	if result.HealthState != domain.HealthLive && result.HealthState != domain.HealthDegraded && result.HealthState != domain.HealthCritical {
		return fmt.Errorf("provider health state %q is invalid", result.HealthState)
	}
	if result.HealthState == domain.HealthLive &&
		target.Installation.VerificationState != domain.InstallationVerificationLegacy &&
		!currentProbeVerifiesScope(target.Installation, result) {
		// Retained inventory is an operational history, never proof that a new
		// scope is authorized. This guard also rejects mixed-version workers
		// that do not produce a fresh scope-verification receipt.
		result.HealthState = domain.HealthCritical
		result.ErrorCode = "repository_scope_unverified"
		result.ErrorMessage = "The current provider probe did not verify a repository inside every declared scope"
	}
	permissions, err := json.Marshal(result.Permissions)
	if err != nil {
		return err
	}
	receipt, err := json.Marshal(result.Receipt)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	command, err := tx.Exec(ctx, `
		UPDATE provider_health_probes SET
			state='completed',health_state=$3,observed_at=$4,
			credential_expires_at=$5,permissions=$6,rate_limit_remaining=$7,
			rate_limit_limit=$8,rate_limit_reset_at=$9,latency_ms=$10,
			error_code=$11,error_message=left($12,1000),receipt=$13,
			available_at=$14,worker_id=NULL,locked_until=NULL,updated_at=now()
		WHERE installation_id=$1 AND state='running' AND worker_id=$2 AND locked_until >= now()`,
		target.Installation.ID, target.WorkerID, result.HealthState, result.ObservedAt.UTC(),
		result.CredentialExpiresAt, permissions, result.RateLimitRemaining, result.RateLimitLimit,
		result.RateLimitResetAt, result.LatencyMS, result.ErrorCode, result.ErrorMessage, receipt, nextProbeAt.UTC())
	if err != nil {
		return fmt.Errorf("complete provider health probe: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrJobClaimLost
	}
	if err := syncProviderRepositoryInventory(ctx, tx, target.Installation.ID, result.Repositories, result.ObservedAt); err != nil {
		return err
	}
	verificationState := domain.InstallationVerificationFailed
	if result.HealthState == domain.HealthLive {
		verificationState = domain.InstallationVerificationVerified
	}
	var transitioned bool
	err = tx.QueryRow(ctx, `
		UPDATE provider_installations
		SET verification_state=$2,verification_updated_at=now(),updated_at=now()
		WHERE id=$1 AND active=TRUE AND (
			verification_state IN ('pending','checking','failed')
			OR (verification_state='verified' AND $3::boolean)
		)
		RETURNING TRUE`, target.Installation.ID, verificationState, result.HealthState == domain.HealthCritical).Scan(&transitioned)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("complete installation verification state: %w", err)
	}
	if transitioned {
		action := "installation.verification_completed"
		if target.Installation.VerificationState == domain.InstallationVerificationVerified && verificationState == domain.InstallationVerificationFailed {
			action = "installation.verification_revoked"
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata)
			VALUES ($1,$2,$3,$4,jsonb_build_object('state',$5::text,'health_state',$6::text,'error_code',$7::text))`, target.Installation.TenantID, "provider-prober", action, target.Installation.ID.String(), string(verificationState), string(result.HealthState), result.ErrorCode); err != nil {
			return fmt.Errorf("audit installation verification completion: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit provider health probe: %w", err)
	}
	return nil
}

func currentProbeVerifiesScope(installation domain.Installation, result domain.ProviderProbeResult) bool {
	verification := result.Receipt["scope_verification"]
	if verification != "verified" &&
		(installation.Provider != domain.ProviderGitHub || verification != "inventory_backed") {
		return false
	}
	// Only repositories returned by this probe count. Retained inventory is an
	// operational history and cannot prove a new scope is authorized. Require
	// a fresh match for every declared entry, including mixed exact/wildcard
	// GitHub scopes and multiple GitLab project/group scopes.
	for _, raw := range strings.Split(installation.RepositoryScope, ",") {
		entry := strings.TrimSpace(raw)
		matched := false
		for _, repository := range result.Repositories {
			if !repository.Archived && repositoryScopeAllows(entry, repository.Name) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

// syncProviderRepositoryInventory records only the normalized metadata
// returned by the least-privilege provider probe. An empty result is not a
// destructive snapshot: providers may page or temporarily omit repositories,
// so retaining the previous audited inventory is safer than silently erasing
// the selection surface after a transient response.
func syncProviderRepositoryInventory(ctx context.Context, tx pgx.Tx, installationID uuid.UUID, repositories []domain.ProviderRepository, observedAt time.Time) error {
	seen := make(map[string]struct{}, len(repositories))
	for _, repository := range repositories {
		repository.ExternalID = strings.TrimSpace(repository.ExternalID)
		repository.Name = strings.TrimSpace(repository.Name)
		if repository.ExternalID == "" || repository.Name == "" {
			continue
		}
		if _, duplicate := seen[repository.ExternalID]; duplicate {
			continue
		}
		seen[repository.ExternalID] = struct{}{}
		if _, err := tx.Exec(ctx, `
			INSERT INTO provider_repository_inventory (
				installation_id,external_id,name,default_branch,visibility,archived,last_seen_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (installation_id,external_id) DO UPDATE SET
				name=EXCLUDED.name,
				default_branch=EXCLUDED.default_branch,
				visibility=EXCLUDED.visibility,
				archived=EXCLUDED.archived,
				last_seen_at=EXCLUDED.last_seen_at`,
			installationID,
			repository.ExternalID,
			repository.Name,
			strings.TrimSpace(repository.DefaultBranch),
			strings.TrimSpace(repository.Visibility),
			repository.Archived,
			observedAt.UTC(),
		); err != nil {
			return fmt.Errorf("sync provider repository inventory: %w", err)
		}
	}
	return nil
}
