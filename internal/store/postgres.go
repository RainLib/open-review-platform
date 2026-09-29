package store

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct {
	pool *pgxpool.Pool
	// Deployment-owned web origins. Adapter callbacks still have to match the
	// admitted provider, repository and exact PR/MR number.
	agentDraftURLPolicy domain.AgentDraftURLPolicy
}

func (s *PostgresStore) ConfigureAgentDraftURLPolicy(policy domain.AgentDraftURLPolicy) {
	s.agentDraftURLPolicy = policy
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

func (s *PostgresStore) ListTenants(ctx context.Context, actor string, limit int) ([]domain.TenantSummary, error) {
	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("tenant limit must be from 1 to 100")
	}
	rows, err := s.pool.Query(ctx, `
		SELECT t.slug, t.name, m.role
		FROM tenants t
		JOIN memberships m ON m.tenant_id = t.id
		WHERE m.subject = $1 AND m.active = TRUE
		ORDER BY lower(t.name), t.slug
		LIMIT $2`, actor, limit)
	if err != nil {
		return nil, fmt.Errorf("list actor tenants: %w", err)
	}
	defer rows.Close()

	tenants := make([]domain.TenantSummary, 0)
	for rows.Next() {
		var tenant domain.TenantSummary
		if err := rows.Scan(&tenant.Slug, &tenant.Name, &tenant.Role); err != nil {
			return nil, fmt.Errorf("scan actor tenant: %w", err)
		}
		tenants = append(tenants, tenant)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate actor tenants: %w", err)
	}
	return tenants, nil
}

func (s *PostgresStore) CreateInstallation(ctx context.Context, actor, tenantSlug string, input domain.InstallationInput) (domain.Installation, error) {
	if input.AuthorScope == "" {
		input.AuthorScope = "all"
	}
	if !validInstallationAuthorScope(input.AuthorScope, input.AuthorExternalID) {
		return domain.Installation{}, ErrInvalidProviderIdentity
	}
	if input.Provider == domain.ProviderGitLab && input.RepositoryScope == domain.AllAuthorizedRepositoriesScope {
		return domain.Installation{}, ErrInvalidProviderIdentity
	}
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
		WHERE t.slug = $1 AND m.subject = $2 AND m.active = TRUE AND m.role IN ('owner', 'admin')`, tenantSlug, actor).Scan(&tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Installation{}, ErrForbidden
	}
	if err != nil {
		return domain.Installation{}, fmt.Errorf("authorize installation creation: %w", err)
	}
	if input.AuthorScope == "mine" {
		var bound bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM provider_actor_mappings
			WHERE tenant_id=$1 AND provider=$2 AND external_id=$3 AND subject=$4
		)`, tenantID, input.Provider, input.AuthorExternalID, actor).Scan(&bound); err != nil {
			return domain.Installation{}, fmt.Errorf("verify installation author identity: %w", err)
		}
		if !bound {
			return domain.Installation{}, ErrForbidden
		}
	}
	// A provider callback can finish at the provider while its HTTP response is
	// lost on the way back to the browser. Treat an exact replay of that signed
	// installation payload as the same durable installation. Different scope or
	// policy values remain a conflict rather than becoming an implicit edit.
	if existing, found, err := activeInstallationByIdentity(ctx, tx, tenantID, input); err != nil {
		return domain.Installation{}, err
	} else if found {
		if sameInstallationInput(existing, input) {
			return existing, nil
		}
		return domain.Installation{}, ErrConflict
	}
	// A pre-checkpoint installation is an upgrade-era record and remains
	// compatible with the former ready state. A newly created *first* active
	// connection, however, must atomically start the governed setup flow; a
	// browser redirect is not a durable state transition.
	var hadActiveInstallation bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM provider_installations WHERE tenant_id=$1 AND active=TRUE)`, tenantID).Scan(&hadActiveInstallation); err != nil {
		return domain.Installation{}, fmt.Errorf("inspect existing installations: %w", err)
	}
	// GitLab OAuth uses an opaque installation key because its webhooks carry a
	// project ID. Repository scope is therefore the durable route. The webhook
	// secret is deployment-wide, so overlapping active scopes must be rejected
	// across every tenant on the same configured GitLab host.
	if input.Provider == domain.ProviderGitLab {
		rows, err := tx.Query(ctx, `
			SELECT repository_scope
			FROM provider_installations
			WHERE provider = $1 AND api_base_url = $2 AND active = TRUE`, input.Provider, input.APIBaseURL)
		if err != nil {
			return domain.Installation{}, fmt.Errorf("list active GitLab scopes: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var existingScope string
			if err := rows.Scan(&existingScope); err != nil {
				return domain.Installation{}, fmt.Errorf("scan active GitLab scope: %w", err)
			}
			if repositoryScopesOverlap(existingScope, input.RepositoryScope) {
				return domain.Installation{}, ErrConflict
			}
		}
		if err := rows.Err(); err != nil {
			return domain.Installation{}, fmt.Errorf("iterate active GitLab scopes: %w", err)
		}
	}
	installation := domain.Installation{TenantID: tenantID, Provider: input.Provider, ExternalID: input.ExternalID, RepositoryScope: input.RepositoryScope, AutomaticReviews: *input.AutomaticReviews, AuthorScope: input.AuthorScope, AuthorExternalID: input.AuthorExternalID, MinimumSeverity: input.MinimumSeverity, APIBaseURL: input.APIBaseURL, CredentialRef: input.CredentialRef}
	// The provider-level identity is unique for a host. Reauthorizing a
	// deliberately deactivated connection must therefore reactivate that same
	// durable record instead of creating a duplicate (or failing on the unique
	// constraint). Keeping its ID preserves the existing webhook, review and
	// audit evidence while the fresh credential still has to pass verification.
	reactivated := false
	err = tx.QueryRow(ctx, `
		UPDATE provider_installations
		SET repository_scope = $5,
		    automatic_reviews = $6,
		    author_scope = $7,
		    author_external_id = $8,
		    minimum_severity = $9,
		    credential_ref = $10,
		    active = TRUE,
		    verification_state = 'pending',
		    updated_at = now()
		WHERE tenant_id = $1
		  AND provider = $2
		  AND api_base_url = $3
		  AND external_id = $4
		  AND active = FALSE
		RETURNING id, active, verification_state`, tenantID, input.Provider, input.APIBaseURL, input.ExternalID, input.RepositoryScope, *input.AutomaticReviews, input.AuthorScope, input.AuthorExternalID, input.MinimumSeverity, input.CredentialRef).
		Scan(&installation.ID, &installation.Active, &installation.VerificationState)
	if err == nil {
		reactivated = true
	} else if isUniqueViolation(err) {
		// Another workspace activated this provider identity after this
		// workspace was deactivated. Keep the old evidence immutable and
		// require that active connection to be deactivated first.
		return domain.Installation{}, ErrConflict
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return domain.Installation{}, fmt.Errorf("reactivate provider installation: %w", err)
	}
	if !reactivated {
		err = tx.QueryRow(ctx, `
			INSERT INTO provider_installations (tenant_id, provider, external_id, repository_scope, automatic_reviews, author_scope, author_external_id, minimum_severity, api_base_url, credential_ref, verification_state)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'pending')
			ON CONFLICT (provider, api_base_url, external_id) WHERE active = TRUE DO NOTHING
			RETURNING id, active, verification_state`, tenantID, input.Provider, input.ExternalID, input.RepositoryScope, *input.AutomaticReviews, input.AuthorScope, input.AuthorExternalID, input.MinimumSeverity, input.APIBaseURL, input.CredentialRef).
			Scan(&installation.ID, &installation.Active, &installation.VerificationState)
		if errors.Is(err, pgx.ErrNoRows) {
			// The active-identity index ensures a single provider installation
			// cannot route webhooks to two tenants at once. Inactive source
			// records remain as immutable historical evidence and do not block a
			// freshly authorized target workspace.
			// DO NOTHING keeps this transaction usable: an earlier implementation
			// caught a unique violation and then tried to re-read in the aborted
			// transaction, turning a normal conflict into HTTP 500.
			//
			// A concurrent exact replay in this tenant is idempotent; a record in
			// another tenant (or an incompatible replay) remains a conflict.
			existing, found, lookupErr := activeInstallationByIdentity(ctx, tx, tenantID, input)
			if lookupErr != nil {
				return domain.Installation{}, lookupErr
			}
			if found && sameInstallationInput(existing, input) {
				return existing, nil
			}
			return domain.Installation{}, ErrConflict
		}
		if err != nil {
			return domain.Installation{}, fmt.Errorf("create provider installation: %w", err)
		}
	}
	if input.Provider == domain.ProviderGitLab && validOAuthCredentialRef(input.CredentialRef) {
		// Keep the credential row locked until this installation commits. The
		// orphan reaper takes the opposite (exclusive) lock before deciding
		// whether a fresh OAuth token has any active installation. A revoked or
		// nonexistent reference must never become an active connection.
		var present bool
		err := tx.QueryRow(ctx, `SELECT TRUE FROM provider_oauth_credentials
			WHERE credential_ref=$1 AND tenant_id=$2 AND provider='gitlab'
			  AND revoked_at IS NULL FOR SHARE`, input.CredentialRef, tenantID).Scan(&present)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Installation{}, ErrConflict
		}
		if err != nil {
			return domain.Installation{}, fmt.Errorf("verify GitLab OAuth credential: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO provider_health_probes (installation_id) VALUES ($1) ON CONFLICT (installation_id) DO NOTHING`, installation.ID); err != nil {
		return domain.Installation{}, fmt.Errorf("seed provider installation verification probe: %w", err)
	}
	if reactivated {
		// A probe can still be using the previous credential when the owner
		// completes a new authorization. Requeueing and clearing its lease makes
		// that old completion fail its compare-and-set, so only a probe claimed
		// after this authorization can publish verification or health evidence.
		if _, err := tx.Exec(ctx, `
			UPDATE provider_health_probes
			SET state='queued',health_state='configured_only',worker_id=NULL,
			    locked_until=NULL,available_at=now(),observed_at=NULL,
			    credential_expires_at=NULL,permissions='[]'::jsonb,
			    rate_limit_remaining=NULL,rate_limit_limit=NULL,
			    rate_limit_reset_at=NULL,latency_ms=0,error_code='',
			    error_message='',receipt='{}'::jsonb,updated_at=now()
			WHERE installation_id=$1`, installation.ID); err != nil {
			return domain.Installation{}, fmt.Errorf("invalidate prior provider verification probe: %w", err)
		}
	}
	if !hadActiveInstallation {
		var startedSetupCheckpoint bool
		err := tx.QueryRow(ctx, `
			INSERT INTO workspace_setup_checkpoints (tenant_id,current_step,revision,updated_by)
			VALUES ($1,'review_scope',1,$2)
			ON CONFLICT (tenant_id) DO NOTHING
			RETURNING TRUE`, tenantID, actor).Scan(&startedSetupCheckpoint)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return domain.Installation{}, fmt.Errorf("start workspace setup checkpoint: %w", err)
		}
		if startedSetupCheckpoint {
			if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata) VALUES ($1,$2,'workspace.setup_checkpoint.saved',$3,jsonb_build_object('step','review_scope','revision',1,'source','first_installation'))`, tenantID, actor, tenantID.String()); err != nil {
				return domain.Installation{}, fmt.Errorf("audit initial workspace setup checkpoint: %w", err)
			}
		}
	}
	auditAction := "installation.created"
	if reactivated {
		auditAction = "installation.reauthorized"
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id, actor_subject, action, target) VALUES ($1, $2, $3, $4)`, tenantID, actor, auditAction, installation.ID.String()); err != nil {
		return domain.Installation{}, fmt.Errorf("audit installation authorization: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Installation{}, fmt.Errorf("commit installation creation: %w", err)
	}
	return installation, nil
}

func activeInstallationByIdentity(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, input domain.InstallationInput) (domain.Installation, bool, error) {
	installation := domain.Installation{
		TenantID:   tenantID,
		Provider:   input.Provider,
		ExternalID: input.ExternalID,
		APIBaseURL: input.APIBaseURL,
	}
	err := tx.QueryRow(ctx, `
		SELECT id, repository_scope, automatic_reviews, author_scope, author_external_id, minimum_severity, credential_ref, active, verification_state
		FROM provider_installations
		WHERE tenant_id = $1
		  AND provider = $2
		  AND api_base_url = $3
		  AND external_id = $4
		  AND active = TRUE`, tenantID, input.Provider, input.APIBaseURL, input.ExternalID).
		Scan(&installation.ID, &installation.RepositoryScope, &installation.AutomaticReviews, &installation.AuthorScope, &installation.AuthorExternalID, &installation.MinimumSeverity, &installation.CredentialRef, &installation.Active, &installation.VerificationState)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Installation{}, false, nil
	}
	if err != nil {
		return domain.Installation{}, false, fmt.Errorf("load active provider installation replay: %w", err)
	}
	return installation, true, nil
}

func sameInstallationInput(installation domain.Installation, input domain.InstallationInput) bool {
	return input.AutomaticReviews != nil &&
		installation.RepositoryScope == input.RepositoryScope &&
		installation.AutomaticReviews == *input.AutomaticReviews &&
		installation.AuthorScope == input.AuthorScope &&
		installation.AuthorExternalID == input.AuthorExternalID &&
		installation.MinimumSeverity == input.MinimumSeverity &&
		installation.CredentialRef == input.CredentialRef
}

// The exact installation read and bounded overview use the same safe summary
// projection. Neither path exposes the worker-only credential reference.
const installationSummarySelect = `
		SELECT installation.id, installation.provider, installation.external_id, installation.repository_scope,
		       installation.automatic_reviews, installation.author_scope, installation.minimum_severity, installation.api_base_url,
		       installation.active, installation.verification_state,
		       probe.state, probe.health_state, probe.attempt, probe.observed_at,
		       COALESCE(probe.permissions, '[]'::jsonb), probe.error_code,
		       probe.receipt->>'inventory_state',
		       COALESCE((
		         SELECT COUNT(*)
		         FROM provider_repository_inventory inventory
		         WHERE inventory.installation_id = installation.id
		           AND (probe.receipt->>'inventory_state' IS NULL OR inventory.last_seen_at = probe.observed_at)
		           AND EXISTS (
		             SELECT 1
		             FROM regexp_split_to_table(installation.repository_scope, ',') AS scope(raw)
		             CROSS JOIN LATERAL (SELECT btrim(scope.raw, ' /') AS candidate) normalized
		             WHERE normalized.candidate = '*/*'
		                OR normalized.candidate = inventory.name
		                OR (
		                  right(normalized.candidate, 2) = '/*'
		                  AND left(inventory.name, char_length(normalized.candidate) - 1) = left(normalized.candidate, char_length(normalized.candidate) - 1)
		                )
		           )
		       ), 0)
		FROM provider_installations installation
		LEFT JOIN provider_health_probes probe ON probe.installation_id = installation.id
		WHERE installation.tenant_id = $1`

func (s *PostgresStore) ListInstallations(ctx context.Context, actor, tenantSlug string, limit int) ([]domain.InstallationSummary, error) {
	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("installation limit must be from 1 to 100")
	}
	tenantID, _, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, installationSummarySelect+`
		ORDER BY installation.created_at DESC, installation.id DESC
		LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("list provider installations: %w", err)
	}
	defer rows.Close()

	installations := make([]domain.InstallationSummary, 0)
	for rows.Next() {
		installation, err := scanInstallationSummary(rows)
		if err != nil {
			return nil, fmt.Errorf("scan provider installation: %w", err)
		}
		installations = append(installations, installation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate provider installations: %w", err)
	}
	return installations, nil
}

func (s *PostgresStore) GetInstallation(ctx context.Context, actor, tenantSlug string, installationID uuid.UUID) (domain.InstallationSummary, error) {
	if installationID == uuid.Nil {
		return domain.InstallationSummary{}, ErrNotFound
	}
	tenantID, _, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.InstallationSummary{}, err
	}
	installation, err := scanInstallationSummary(s.pool.QueryRow(ctx, installationSummarySelect+` AND installation.id = $2`, tenantID, installationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.InstallationSummary{}, ErrNotFound
	}
	if err != nil {
		return domain.InstallationSummary{}, fmt.Errorf("get provider installation: %w", err)
	}
	return installation, nil
}

func scanInstallationSummary(row interface{ Scan(...any) error }) (domain.InstallationSummary, error) {
	var installation domain.InstallationSummary
	var probeState, healthState, errorCode, inventoryState *string
	var attempt *int
	var observedAt *time.Time
	var permissions []byte
	var inventoryCount int
	if err := row.Scan(
		&installation.ID, &installation.Provider, &installation.ExternalID, &installation.RepositoryScope,
		&installation.AutomaticReviews, &installation.AuthorScope, &installation.MinimumSeverity, &installation.APIBaseURL,
		&installation.Active, &installation.VerificationState, &probeState, &healthState, &attempt, &observedAt,
		&permissions, &errorCode, &inventoryState, &inventoryCount,
	); err != nil {
		return domain.InstallationSummary{}, err
	}
	if probeState != nil {
		if attempt == nil {
			return domain.InstallationSummary{}, fmt.Errorf("provider verification attempt is missing")
		}
		var parsedPermissions []string
		if err := json.Unmarshal(permissions, &parsedPermissions); err != nil {
			return domain.InstallationSummary{}, fmt.Errorf("decode provider verification permissions: %w", err)
		}
		receipt := &domain.ProviderVerificationReceipt{
			State: *probeState, Attempt: *attempt, ObservedAt: observedAt,
			Permissions: parsedPermissions, InventoryCount: inventoryCount,
		}
		if healthState != nil {
			receipt.HealthState = domain.HealthState(*healthState)
		}
		if errorCode != nil {
			receipt.ErrorCode = *errorCode
		}
		if inventoryState != nil {
			receipt.InventoryState = *inventoryState
		}
		installation.Verification = receipt
	}
	return installation, nil
}

// ListInstallationRepositories returns repositories seen by the latest
// worker probe for one installation. Older rows remain for audit but cannot
// be selected as if the provider had just verified their access. The list
// remains subject to the installation's durable repository scope.
func (s *PostgresStore) ListInstallationRepositories(ctx context.Context, actor, tenantSlug string, installationID uuid.UUID, query string, limit int) ([]domain.ProviderRepository, error) {
	if installationID == uuid.Nil {
		return nil, ErrNotFound
	}
	query = strings.TrimSpace(query)
	if len(query) > 512 {
		return nil, fmt.Errorf("installation repository query is too long")
	}
	if limit < 1 || limit > 500 {
		return nil, fmt.Errorf("installation repository limit must be from 1 to 500")
	}
	tenantID, _, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return nil, err
	}
	var scope string
	if err := s.pool.QueryRow(ctx, `
		SELECT repository_scope
		FROM provider_installations
		WHERE id=$1 AND tenant_id=$2`, installationID, tenantID).Scan(&scope); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("load installation repository scope: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT external_id,name,default_branch,visibility,archived,last_seen_at
		FROM provider_repository_inventory inventory
		LEFT JOIN provider_health_probes probe ON probe.installation_id=inventory.installation_id
		WHERE inventory.installation_id=$1
		  AND ($2='' OR strpos(lower(inventory.name),lower($2)) > 0)
		  AND (probe.receipt->>'inventory_state' IS NULL OR inventory.last_seen_at=probe.observed_at)
		ORDER BY inventory.archived ASC,inventory.name ASC`, installationID, query)
	if err != nil {
		return nil, fmt.Errorf("list provider repository inventory: %w", err)
	}
	defer rows.Close()
	repositories := make([]domain.ProviderRepository, 0)
	for rows.Next() {
		var repository domain.ProviderRepository
		if err := rows.Scan(
			&repository.ExternalID,
			&repository.Name,
			&repository.DefaultBranch,
			&repository.Visibility,
			&repository.Archived,
			&repository.LastSeenAt,
		); err != nil {
			return nil, fmt.Errorf("scan provider repository inventory: %w", err)
		}
		if repositoryScopeAllows(scope, repository.Name) {
			repositories = append(repositories, repository)
			if len(repositories) == limit {
				break
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate provider repository inventory: %w", err)
	}
	return repositories, nil
}

// UpdateInstallationRepositoryScope changes only the admission boundary after
// an explicit administrator decision. The expected scope makes concurrent
// console edits fail visibly instead of silently widening or narrowing a
// repository allowlist.
func (s *PostgresStore) UpdateInstallationRepositoryScope(ctx context.Context, actor, tenantSlug string, installationID uuid.UUID, input domain.InstallationRepositoryScopeInput) (domain.InstallationSummary, error) {
	if installationID == uuid.Nil || strings.TrimSpace(input.RepositoryScope) == "" || strings.TrimSpace(input.ExpectedRepositoryScope) == "" {
		return domain.InstallationSummary{}, ErrInvalidProviderIdentity
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.InstallationSummary{}, fmt.Errorf("begin installation scope update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.InstallationSummary{}, err
	}
	if role != "owner" && role != "admin" {
		return domain.InstallationSummary{}, ErrForbidden
	}
	// Lock the probe before the installation, matching the worker's completion
	// order. A scope must not change beneath an in-flight provider check.
	if _, err := tx.Exec(ctx, `
		INSERT INTO provider_health_probes (installation_id)
		SELECT id FROM provider_installations WHERE id=$1 AND tenant_id=$2
		ON CONFLICT (installation_id) DO NOTHING`, installationID, tenantID); err != nil {
		return domain.InstallationSummary{}, fmt.Errorf("seed scope verification probe: %w", err)
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
		return domain.InstallationSummary{}, fmt.Errorf("lock scope verification probe: %w", err)
	}
	if probeState == "running" {
		return domain.InstallationSummary{}, ErrConflict
	}
	var provider domain.Provider
	var apiBaseURL, currentScope string
	var active bool
	err = tx.QueryRow(ctx, `
		SELECT provider,api_base_url,repository_scope,active
		FROM provider_installations
		WHERE id=$1 AND tenant_id=$2
		FOR UPDATE`, installationID, tenantID).Scan(&provider, &apiBaseURL, &currentScope, &active)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.InstallationSummary{}, ErrNotFound
	}
	if err != nil {
		return domain.InstallationSummary{}, fmt.Errorf("load installation scope update: %w", err)
	}
	if !active || currentScope != input.ExpectedRepositoryScope {
		return domain.InstallationSummary{}, ErrConflict
	}
	if provider == domain.ProviderGitLab && input.RepositoryScope == domain.AllAuthorizedRepositoriesScope {
		return domain.InstallationSummary{}, ErrInvalidProviderIdentity
	}
	if provider == domain.ProviderGitLab {
		rows, err := tx.Query(ctx, `
			SELECT repository_scope
			FROM provider_installations
			WHERE provider=$1 AND api_base_url=$2 AND active=TRUE AND id<>$3`, provider, apiBaseURL, installationID)
		if err != nil {
			return domain.InstallationSummary{}, fmt.Errorf("list GitLab scopes for installation update: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var existingScope string
			if err := rows.Scan(&existingScope); err != nil {
				return domain.InstallationSummary{}, fmt.Errorf("scan GitLab scope for installation update: %w", err)
			}
			if repositoryScopesOverlap(existingScope, input.RepositoryScope) {
				return domain.InstallationSummary{}, ErrConflict
			}
		}
		if err := rows.Err(); err != nil {
			return domain.InstallationSummary{}, fmt.Errorf("iterate GitLab scopes for installation update: %w", err)
		}
	}
	var installation domain.InstallationSummary
	err = tx.QueryRow(ctx, `
		UPDATE provider_installations
		SET repository_scope=$3,verification_state='pending',verification_updated_at=now(),updated_at=now()
		WHERE id=$1 AND tenant_id=$2
		RETURNING id,provider,external_id,repository_scope,automatic_reviews,author_scope,minimum_severity,api_base_url,active,verification_state`,
		installationID, tenantID, input.RepositoryScope).Scan(
		&installation.ID, &installation.Provider, &installation.ExternalID,
		&installation.RepositoryScope, &installation.AutomaticReviews, &installation.AuthorScope,
		&installation.MinimumSeverity, &installation.APIBaseURL,
		&installation.Active, &installation.VerificationState,
	)
	if err != nil {
		return domain.InstallationSummary{}, fmt.Errorf("update installation repository scope: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE provider_health_probes
		SET state='queued',available_at=now(),worker_id=NULL,locked_until=NULL,updated_at=now()
		WHERE installation_id=$1`, installationID); err != nil {
		return domain.InstallationSummary{}, fmt.Errorf("queue scope verification probe: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata)
		VALUES ($1,$2,'installation.repository_scope_updated',$3,jsonb_build_object('previous_scope',$4::text,'repository_scope',$5::text,'verification_queued',true))`,
		tenantID, actor, installationID.String(), currentScope, input.RepositoryScope); err != nil {
		return domain.InstallationSummary{}, fmt.Errorf("audit installation repository scope update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.InstallationSummary{}, fmt.Errorf("commit installation repository scope update: %w", err)
	}
	return installation, nil
}

// DeactivateInstallation is a durable admission stop: once committed, this
// installation can no longer resolve inbound provider events or authorize CLI
// submissions. It deliberately preserves the installation, probe receipts and
// historic review evidence for auditability and later forensic review.
func (s *PostgresStore) DeactivateInstallation(ctx context.Context, actor, tenantSlug string, installationID uuid.UUID) (domain.InstallationSummary, error) {
	if installationID == uuid.Nil {
		return domain.InstallationSummary{}, ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.InstallationSummary{}, fmt.Errorf("begin installation deactivation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.InstallationSummary{}, err
	}
	if role != "owner" && role != "admin" {
		return domain.InstallationSummary{}, ErrForbidden
	}

	var installation domain.InstallationSummary
	var credentialRef string
	err = tx.QueryRow(ctx, `
		UPDATE provider_installations
		SET active=FALSE, updated_at=now()
		WHERE id=$1 AND tenant_id=$2 AND active=TRUE
		RETURNING id,provider,external_id,repository_scope,automatic_reviews,author_scope,minimum_severity,api_base_url,active,verification_state,credential_ref`, installationID, tenantID).Scan(
		&installation.ID,
		&installation.Provider,
		&installation.ExternalID,
		&installation.RepositoryScope,
		&installation.AutomaticReviews,
		&installation.AuthorScope,
		&installation.MinimumSeverity,
		&installation.APIBaseURL,
		&installation.Active,
		&installation.VerificationState,
		&credentialRef,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM provider_installations WHERE id=$1 AND tenant_id=$2)`, installationID, tenantID).Scan(&exists); err != nil {
			return domain.InstallationSummary{}, fmt.Errorf("inspect installation deactivation: %w", err)
		}
		if !exists {
			return domain.InstallationSummary{}, ErrNotFound
		}
		return domain.InstallationSummary{}, ErrConflict
	}
	if err != nil {
		return domain.InstallationSummary{}, fmt.Errorf("deactivate provider installation: %w", err)
	}
	// A GitLab OAuth credential is a local capability even after admission is
	// stopped. Revoke it in the same transaction unless another active scope in
	// this workspace still uses it. Provider-side OAuth grants are not changed
	// here; a later signed authorization receives a fresh credential reference.
	if installation.Provider == domain.ProviderGitLab && validOAuthCredentialRef(credentialRef) {
		result, err := tx.Exec(ctx, `
			UPDATE provider_oauth_credentials credential
			SET revoked_at=now(), updated_at=now()
			WHERE credential.credential_ref=$1 AND credential.tenant_id=$2
			  AND credential.provider='gitlab' AND credential.revoked_at IS NULL
			  AND NOT EXISTS (
				SELECT 1 FROM provider_installations installation
				WHERE installation.tenant_id=$2 AND installation.active=TRUE
				  AND installation.credential_ref=$1
			  )`, credentialRef, tenantID)
		if err != nil {
			return domain.InstallationSummary{}, fmt.Errorf("revoke inactive GitLab OAuth credential: %w", err)
		}
		if result.RowsAffected() > 0 {
			if _, err := tx.Exec(ctx, `
				INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata)
				VALUES ($1,$2,'provider_oauth_credential.revoked',$3,jsonb_build_object('provider','gitlab','installation_id',$4::text))`,
				tenantID, actor, credentialRef, installation.ID.String()); err != nil {
				return domain.InstallationSummary{}, fmt.Errorf("audit GitLab OAuth credential revocation: %w", err)
			}
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata)
		VALUES ($1,$2,'installation.deactivated',$3,jsonb_build_object('provider',$4::text,'repository_scope',$5::text))`,
		tenantID, actor, installation.ID.String(), string(installation.Provider), installation.RepositoryScope); err != nil {
		return domain.InstallationSummary{}, fmt.Errorf("audit installation deactivation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.InstallationSummary{}, fmt.Errorf("commit installation deactivation: %w", err)
	}
	return installation, nil
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
		WHERE t.slug = $1 AND m.subject = $2 AND m.active = TRUE AND m.role = 'owner'`, tenantSlug, actor).Scan(&tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Membership{}, ErrForbidden
	}
	if err != nil {
		return domain.Membership{}, fmt.Errorf("authorize membership update: %w", err)
	}
	var existingRole string
	err = tx.QueryRow(ctx, `SELECT role FROM memberships WHERE tenant_id = $1 AND subject = $2`, tenantID, subject).Scan(&existingRole)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return domain.Membership{}, fmt.Errorf("load membership before update: %w", err)
	}
	// An owner may administer peers, but must not be able to silently turn the
	// current authenticated session into a lower-privilege identity. That would
	// make recovery depend on a second owner and is never an intended role edit.
	if actor == subject && existingRole == "owner" && role != "owner" {
		return domain.Membership{}, ErrConflict
	}
	if existingRole == "owner" && role != "owner" {
		var otherOwners int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM memberships WHERE tenant_id = $1 AND role = 'owner' AND active = TRUE AND subject <> $2`, tenantID, subject).Scan(&otherOwners); err != nil {
			return domain.Membership{}, fmt.Errorf("count remaining tenant owners: %w", err)
		}
		if otherOwners == 0 {
			return domain.Membership{}, ErrConflict
		}
	}
	membership := domain.Membership{TenantID: tenantID, Subject: subject, Role: role}
	err = tx.QueryRow(ctx, `
		INSERT INTO memberships (tenant_id, subject, role) VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id, subject) DO UPDATE SET role = EXCLUDED.role
		RETURNING active,deactivated_at,deactivated_by`, tenantID, subject, role).Scan(&membership.Active, &membership.DeactivatedAt, &membership.DeactivatedBy)
	if err != nil {
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

func (s *PostgresStore) ListMemberships(ctx context.Context, actor, tenantSlug string, limit int) ([]domain.Membership, error) {
	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("membership limit must be from 1 to 100")
	}
	tenantID, role, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return nil, err
	}
	if role != "owner" && role != "admin" {
		return nil, ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `
		SELECT tenant_id, subject, role, active, deactivated_at, deactivated_by
		FROM memberships
		WHERE tenant_id = $1
		ORDER BY CASE role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 WHEN 'rule_admin' THEN 2 WHEN 'reviewer' THEN 3 WHEN 'viewer' THEN 4 ELSE 5 END, subject
		LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("list memberships: %w", err)
	}
	defer rows.Close()
	items := make([]domain.Membership, 0)
	for rows.Next() {
		var item domain.Membership
		if err := rows.Scan(&item.TenantID, &item.Subject, &item.Role, &item.Active, &item.DeactivatedAt, &item.DeactivatedBy); err != nil {
			return nil, fmt.Errorf("scan membership: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// SetMembershipActive preserves the membership record and its audit history
// while immediately removing or restoring its ability to authorize requests.
// Only an active owner can do this; self-deactivation and removal of the last
// active owner are rejected to preserve a recoverable tenant administration path.
func (s *PostgresStore) SetMembershipActive(ctx context.Context, actor, tenantSlug, subject string, active bool) (domain.Membership, error) {
	subject = strings.TrimSpace(subject)
	if subject == "" || actor == subject {
		return domain.Membership{}, ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Membership{}, fmt.Errorf("begin membership activation update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.Membership{}, err
	}
	if role != "owner" {
		return domain.Membership{}, ErrForbidden
	}
	var target domain.Membership
	err = tx.QueryRow(ctx, `SELECT tenant_id,subject,role,active,deactivated_at,deactivated_by FROM memberships WHERE tenant_id=$1 AND subject=$2 FOR UPDATE`, tenantID, subject).Scan(&target.TenantID, &target.Subject, &target.Role, &target.Active, &target.DeactivatedAt, &target.DeactivatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Membership{}, ErrNotFound
	}
	if err != nil {
		return domain.Membership{}, fmt.Errorf("load membership activation target: %w", err)
	}
	if target.Active == active {
		return domain.Membership{}, ErrConflict
	}
	if !active && target.Role == "owner" {
		var remainingOwners int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM memberships WHERE tenant_id=$1 AND role='owner' AND active=TRUE AND subject <> $2`, tenantID, subject).Scan(&remainingOwners); err != nil {
			return domain.Membership{}, fmt.Errorf("count remaining active owners: %w", err)
		}
		if remainingOwners == 0 {
			return domain.Membership{}, ErrConflict
		}
	}
	if active {
		err = tx.QueryRow(ctx, `UPDATE memberships SET active=TRUE,deactivated_at=NULL,deactivated_by='' WHERE tenant_id=$1 AND subject=$2 RETURNING tenant_id,subject,role,active,deactivated_at,deactivated_by`, tenantID, subject).Scan(&target.TenantID, &target.Subject, &target.Role, &target.Active, &target.DeactivatedAt, &target.DeactivatedBy)
	} else {
		err = tx.QueryRow(ctx, `UPDATE memberships SET active=FALSE,deactivated_at=now(),deactivated_by=$3 WHERE tenant_id=$1 AND subject=$2 RETURNING tenant_id,subject,role,active,deactivated_at,deactivated_by`, tenantID, subject, actor).Scan(&target.TenantID, &target.Subject, &target.Role, &target.Active, &target.DeactivatedAt, &target.DeactivatedBy)
	}
	if err != nil {
		return domain.Membership{}, fmt.Errorf("update membership activation: %w", err)
	}
	action := "membership.deactivated"
	if active {
		action = "membership.reactivated"
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata) VALUES ($1,$2,$3,$4,jsonb_build_object('role',$5::text))`, tenantID, actor, action, subject, target.Role); err != nil {
		return domain.Membership{}, fmt.Errorf("audit membership activation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Membership{}, fmt.Errorf("commit membership activation: %w", err)
	}
	return target, nil
}

// resolveInboundInstallation treats the recorded repository scope as an
// admission boundary for every provider. A GitHub App installation identity
// proves which App emitted the event, but does not override a workspace's
// narrower selected-repository policy. GitLab webhooks identify a project,
// whereas GitLab OAuth proves a human authorization and the durable record has
// an opaque scope identity, so GitLab is routed by its explicit scope. A scope
// may never silently choose between tenants: overlapping scopes fail closed.
func resolveInboundInstallation(ctx context.Context, tx pgx.Tx, provider domain.Provider, apiBaseURL, externalID, repository string) (domain.Installation, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, tenant_id, provider, external_id, repository_scope, automatic_reviews, author_scope, author_external_id, minimum_severity, api_base_url, credential_ref, active
		FROM provider_installations
		WHERE provider = $1 AND api_base_url = $2 AND active = TRUE
		  AND verification_state IN ('legacy','verified')
		ORDER BY created_at ASC
		LIMIT 101`, provider, apiBaseURL)
	if err != nil {
		return domain.Installation{}, fmt.Errorf("list active provider installations: %w", err)
	}
	defer rows.Close()

	matches := make([]domain.Installation, 0, 1)
	for rows.Next() {
		var installation domain.Installation
		if err := rows.Scan(
			&installation.ID,
			&installation.TenantID,
			&installation.Provider,
			&installation.ExternalID,
			&installation.RepositoryScope,
			&installation.AutomaticReviews,
			&installation.AuthorScope,
			&installation.AuthorExternalID,
			&installation.MinimumSeverity,
			&installation.APIBaseURL,
			&installation.CredentialRef,
			&installation.Active,
		); err != nil {
			return domain.Installation{}, fmt.Errorf("scan active provider installation: %w", err)
		}
		if !repositoryScopeAllows(installation.RepositoryScope, repository) {
			continue
		}
		if provider == domain.ProviderGitHub && installation.ExternalID != externalID {
			continue
		}
		matches = append(matches, installation)
	}
	if err := rows.Err(); err != nil {
		return domain.Installation{}, fmt.Errorf("iterate active provider installations: %w", err)
	}
	if len(matches) == 0 {
		return domain.Installation{}, ErrUnknownInstallation
	}
	if len(matches) > 1 {
		return domain.Installation{}, ErrAmbiguousInstallation
	}
	return matches[0], nil
}

func repositoryScopeAllows(scope, repository string) bool {
	repository = strings.Trim(strings.TrimSpace(repository), "/")
	if repository == "" {
		return false
	}
	for _, raw := range strings.Split(scope, ",") {
		candidate := strings.Trim(strings.TrimSpace(raw), "/")
		if candidate == "" {
			continue
		}
		if candidate == domain.AllAuthorizedRepositoriesScope {
			return true
		}
		if candidate == repository {
			return true
		}
		if strings.HasSuffix(candidate, "/*") {
			group := strings.TrimSuffix(candidate, "/*")
			if group != "" && strings.HasPrefix(repository, group+"/") {
				return true
			}
		}
	}
	return false
}

func repositoryScopesOverlap(left, right string) bool {
	for _, leftRaw := range strings.Split(left, ",") {
		leftScope := strings.Trim(strings.TrimSpace(leftRaw), "/")
		if leftScope == "" {
			continue
		}
		for _, rightRaw := range strings.Split(right, ",") {
			rightScope := strings.Trim(strings.TrimSpace(rightRaw), "/")
			if rightScope == "" {
				continue
			}
			if leftScope == rightScope ||
				repositoryScopeAllows(leftScope, strings.TrimSuffix(rightScope, "/*")) ||
				repositoryScopeAllows(rightScope, strings.TrimSuffix(leftScope, "/*")) {
				return true
			}
		}
	}
	return false
}

func (s *PostgresStore) Enqueue(ctx context.Context, event domain.InboundEvent) (domain.ReviewJob, bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.ReviewJob{}, false, fmt.Errorf("begin enqueue: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	installation, err := resolveInboundInstallation(ctx, tx, event.Provider, event.APIBaseURL, event.InstallationExternalID, event.Repository)
	if err != nil {
		return domain.ReviewJob{}, false, err
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
	deferAuthor, err := needsGitLabAuthorResolution(ctx, tx, installation, event)
	if err != nil {
		return domain.ReviewJob{}, false, err
	}
	if deferAuthor {
		_, err := tx.Exec(ctx, `
			INSERT INTO gitlab_author_admissions
			  (delivery_id,installation_id,repository,review_number,head_sha,author_id)
			VALUES ($1,$2,$3,$4,$5,$6)`, deliveryID, installation.ID, event.Repository,
			event.ReviewNumber, event.HeadSHA, event.AuthorExternalID)
		if err != nil {
			return domain.ReviewJob{}, false, fmt.Errorf("queue GitLab author resolution: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.ReviewJob{}, false, fmt.Errorf("commit GitLab author resolution: %w", err)
		}
		return domain.ReviewJob{State: domain.JobQueued, ErrorMessage: "provider author verification queued"}, false, nil
	}
	job, coalesced, err := admitVerifiedReview(ctx, tx, installation, deliveryID, event)
	if err != nil {
		return domain.ReviewJob{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ReviewJob{}, false, fmt.Errorf("commit review admission: %w", err)
	}
	return job, coalesced, nil
}

func admitVerifiedReview(ctx context.Context, tx pgx.Tx, installation domain.Installation, deliveryID uuid.UUID, event domain.InboundEvent) (domain.ReviewJob, bool, error) {
	if reason, err := reviewAdmissionSkipReason(ctx, tx, installation, event); err != nil {
		return domain.ReviewJob{}, false, err
	} else if reason != "" {
		if err := recordReviewAdmissionSkip(ctx, tx, installation, event, reason); err != nil {
			return domain.ReviewJob{}, false, err
		}
		// The delivery ledger is retained for webhook idempotency, while a nil
		// job ID tells the transport adapter that policy intentionally skipped
		// automatic work. No run, outbox message, checkout, or provider status is
		// produced for a skip.
		return domain.ReviewJob{State: domain.JobCancelled, ErrorMessage: reason}, false, nil
	}

	job, err := scanJob(tx.QueryRow(ctx, `
		INSERT INTO review_jobs (
			tenant_id, installation_id, delivery_id, provider, api_base_url, repository, clone_url,
			review_number, base_ref, base_sha, head_ref, head_sha, state
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, 'queued')
		RETURNING id, tenant_id, (SELECT slug FROM tenants WHERE id = review_jobs.tenant_id), installation_id, $13::text, $14::text, delivery_id, provider, api_base_url, repository, clone_url,
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
	if errors.Is(workflowErr, errRunCoalesced) {
		job.State = domain.JobCancelled
		job.ErrorMessage = "coalesced into an active review run for the same head"
		finishedAt := time.Now().UTC()
		job.FinishedAt = &finishedAt
		return job, true, nil
	}
	return job, false, nil
}

func needsGitLabAuthorResolution(ctx context.Context, tx pgx.Tx, installation domain.Installation, event domain.InboundEvent) (bool, error) {
	if event.Provider != domain.ProviderGitLab || event.TriggerKind != "" || event.Author != "" ||
		event.AuthorExternalID == "" || (len(event.HeadSHA) != 40 && len(event.HeadSHA) != 64) {
		return false, nil
	}
	decoded, shaErr := hex.DecodeString(event.HeadSHA)
	authorID, idErr := strconv.ParseUint(event.AuthorExternalID, 10, 64)
	if shaErr != nil || len(decoded)*2 != len(event.HeadSHA) || idErr != nil || authorID == 0 || strconv.FormatUint(authorID, 10) != event.AuthorExternalID {
		return false, nil
	}
	reason, err := reviewAdmissionSkipReason(ctx, tx, installation, event)
	if err != nil {
		return false, err
	}
	if reason != "pull request metadata does not match the configured author, label, or target-branch policy" {
		return false, nil
	}
	// Do not delay webhook admission unless author-name policy actually needs a
	// provider reread. Other GitLab MRs keep their existing fast path.
	snapshot, err := resolveEffectiveReviewConfiguration(ctx, tx, installation.TenantID, reviewConfigScopeForInstallation(installation, event.Repository), domain.ReviewConfigFilters)
	if err != nil {
		return false, fmt.Errorf("resolve GitLab author filter: %w", err)
	}
	filters, err := domain.DecodeReviewFiltersConfig(snapshot.Content)
	if err != nil {
		return false, fmt.Errorf("decode GitLab author filter: %w", err)
	}
	return len(filters.ExcludeAuthors) > 0, nil
}

// recordReviewAdmissionSkip leaves a tenant-scoped audit explanation for a
// verified delivery that policy intentionally did not turn into work. The
// record carries no raw payload, provider delivery ID, clone URL, or
// credential; those values remain confined to webhook delivery storage.
func recordReviewAdmissionSkip(ctx context.Context, tx pgx.Tx, installation domain.Installation, event domain.InboundEvent, reason string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata)
		VALUES ($1, $2, 'review.admission_skipped', $3, jsonb_build_object(
			'provider', $4::text,
			'repository', $5::text,
			'review_number', $6::integer,
			'base_ref', $7::text,
			'action', $8::text,
			'reason', $9::text
		))`, installation.TenantID, "provider:"+string(event.Provider), event.Repository+"#"+fmt.Sprint(event.ReviewNumber), event.Provider, event.Repository, event.ReviewNumber, event.BaseRef, event.Action, reason)
	if err != nil {
		return fmt.Errorf("record review policy skip audit event: %w", err)
	}
	return nil
}

func reviewAdmissionSkipReason(ctx context.Context, tx pgx.Tx, installation domain.Installation, event domain.InboundEvent) (string, error) {
	setupComplete, err := workspaceSetupAllowsReview(ctx, tx, installation.TenantID)
	if err != nil {
		return "", err
	}
	if !setupComplete {
		return "workspace setup is incomplete; complete the recorded review baseline before reviews can run", nil
	}
	generalSnapshot, err := resolveEffectiveReviewConfiguration(ctx, tx, installation.TenantID, reviewConfigScopeForInstallation(installation, event.Repository), domain.ReviewConfigGeneral)
	if err != nil {
		return "", fmt.Errorf("resolve general review admission policy: %w", err)
	}
	general, err := domain.DecodeReviewGeneralConfig(generalSnapshot.Content)
	if err != nil {
		return "", fmt.Errorf("decode general review admission policy: %w", err)
	}
	if general.TriggerMode == domain.ReviewTriggerOff {
		return "reviews are disabled by repository policy", nil
	}
	// Explicit CLI requests and verified comment commands are operator intent.
	// Manual mode admits only those requests; automatic mode continues through
	// the metadata policy below.
	if strings.TrimSpace(event.TriggerKind) != "" {
		return "", nil
	}
	if general.TriggerMode == domain.ReviewTriggerManual {
		return "automatic reviews require an explicit comment command or CLI request", nil
	}
	if !installation.AutomaticReviews {
		return "automatic reviews are disabled for this connection", nil
	}
	if !automaticReviewAuthorAllowed(installation, event) {
		return "pull request author does not match this connection's OAuth-bound author scope", nil
	}
	if !general.AutomaticReview {
		return "automatic reviews are disabled by workspace policy", nil
	}
	if event.IsDraft && !general.ReviewDrafts {
		return "draft pull requests are excluded by workspace policy", nil
	}
	if !general.ReReviewOnPush && isSynchronizeAction(event.Provider, event.Action) {
		return "push re-reviews are disabled by workspace policy", nil
	}
	filterSnapshot, err := resolveEffectiveReviewConfiguration(ctx, tx, installation.TenantID, reviewConfigScopeForInstallation(installation, event.Repository), domain.ReviewConfigFilters)
	if err != nil {
		return "", fmt.Errorf("resolve review filter admission policy: %w", err)
	}
	filters, err := domain.DecodeReviewFiltersConfig(filterSnapshot.Content)
	if err != nil {
		return "", fmt.Errorf("decode review filter admission policy: %w", err)
	}
	if !filters.AllowsAdmission(event.Author, event.Labels, event.BaseRef) {
		return "pull request metadata does not match the configured author, label, or target-branch policy", nil
	}
	return "", nil
}

func validInstallationAuthorScope(scope, actorID string) bool {
	if scope == "all" {
		return actorID == ""
	}
	if scope != "mine" || len(actorID) == 0 || len(actorID) > 19 || actorID[0] < '1' || actorID[0] > '9' {
		return false
	}
	for _, digit := range actorID[1:] {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

func automaticReviewAuthorAllowed(installation domain.Installation, event domain.InboundEvent) bool {
	return installation.AuthorScope == "" || installation.AuthorScope == "all" ||
		(installation.AuthorScope == "mine" && installation.AuthorExternalID != "" && event.AuthorExternalID == installation.AuthorExternalID)
}

func isSynchronizeAction(provider domain.Provider, action string) bool {
	action = strings.ToLower(strings.TrimSpace(action))
	return (provider == domain.ProviderGitHub && action == "synchronize") ||
		(provider == domain.ProviderGitLab && action == "update")
}

func (s *PostgresStore) Claim(ctx context.Context, workerID string) (*domain.ReviewJob, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin fair review job claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var candidateID, tenantID uuid.UUID
	err = tx.QueryRow(ctx, `
		WITH eligible AS MATERIALIZED (
			SELECT j.id, j.tenant_id, j.created_at, r.review_mode
			FROM review_jobs j
			JOIN review_runs r ON r.legacy_job_id = j.id
			JOIN provider_installations i ON i.id = j.installation_id
			LEFT JOIN LATERAL (
				SELECT version.content
				FROM review_configurations configuration
				JOIN LATERAL (
					SELECT content
					FROM review_configuration_versions
					WHERE configuration_id = configuration.id
					ORDER BY revision DESC
					LIMIT 1
				) version ON TRUE
				WHERE configuration.tenant_id = j.tenant_id
				  AND configuration.section = 'models'
				  AND configuration.scope_kind = 'tenant'
				  AND configuration.scope_ref = ''
				  AND configuration.active = TRUE
			) tenant_model ON TRUE
			WHERE (r.state IN ('admitted', 'preparing', 'analyzing', 'normalizing', 'publishing')
			       OR (r.state = 'acknowledged' AND r.trigger_kind IN ('pull_request', 'cli')))
			  AND i.active = TRUE
			  AND i.verification_state IN ('legacy', 'verified')
			  AND ((j.state = 'queued' AND j.available_at <= now()) OR (j.state = 'running' AND (j.locked_until IS NULL OR j.locked_until < now())))
			  AND (
			    SELECT count(*)
			    FROM review_jobs active_job
			    JOIN review_runs active_run ON active_run.legacy_job_id = active_job.id
			    WHERE active_job.tenant_id = j.tenant_id
			      AND active_job.state = 'running'
			      AND active_job.locked_until > now()
			      AND active_run.state IN ('admitted', 'preparing', 'analyzing', 'normalizing', 'publishing')
			  ) < COALESCE(NULLIF((tenant_model.content ->> 'max_concurrent_runs')::integer, 0), 2)
		), tenant_heads AS (
			-- The recovery poller is a durable fallback for broker delivery. It
			-- must retain the same security-first intent as the AMQP priority
			-- queue, otherwise a broker restart can turn an urgent security
			-- review into FIFO work behind older standard reviews. We still take
			-- only one head per tenant so tenants at the same priority round-robin.
			SELECT DISTINCT ON (tenant_id) id, tenant_id, created_at, review_mode
			FROM eligible
			ORDER BY tenant_id,
				CASE review_mode
					WHEN 'security' THEN 0
					WHEN 'deep' THEN 1
					ELSE 2
				END,
				created_at, id
		)
		SELECT job.id, head.tenant_id
		FROM tenant_heads head
		JOIN review_jobs job ON job.id = head.id
		JOIN tenants tenant ON tenant.id = head.tenant_id
		LEFT JOIN tenant_review_dispatches dispatch ON dispatch.tenant_id = head.tenant_id
		ORDER BY CASE head.review_mode
				WHEN 'security' THEN 0
				WHEN 'deep' THEN 1
				ELSE 2
			 END,
			 COALESCE(dispatch.last_claimed_at, '-infinity'::timestamptz),
			 head.created_at, head.id
		FOR UPDATE OF job, tenant SKIP LOCKED
		LIMIT 1`).Scan(&candidateID, &tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoQueuedJob
	}
	if err != nil {
		return nil, fmt.Errorf("select fair review job candidate: %w", err)
	}
	// The eligibility CTE is evaluated before this transaction acquires the
	// tenant lock. Recheck under that lock so two recovery workers cannot both
	// carry a previously-observed spare slot across the lock boundary.
	maxConcurrentRuns, err := tenantReviewConcurrencyLimit(ctx, tx, tenantID)
	if err != nil {
		return nil, err
	}
	atCapacity, err := tenantReviewAtCapacity(ctx, tx, tenantID, maxConcurrentRuns)
	if err != nil {
		return nil, err
	}
	if atCapacity {
		return nil, ErrNoQueuedJob
	}
	job, err := scanJob(tx.QueryRow(ctx, `
		UPDATE review_jobs AS j
		SET state = 'running', attempts = attempts + 1, locked_by = $2,
			locked_until = now() + interval '2 minutes', started_at = now()
		FROM provider_installations AS i, tenants AS tenant
		WHERE j.id = $1
		  AND i.id = j.installation_id
		  AND tenant.id = j.tenant_id
		  AND i.active = TRUE
		  AND i.verification_state IN ('legacy', 'verified')
		  -- The materialized eligibility scan can become stale while this
		  -- transaction waits for the tenant row. Recheck the lease state in
		  -- the UPDATE so a job completed by another worker can never be moved
		  -- back to running and executed twice.
		  AND ((j.state = 'queued' AND j.available_at <= now()) OR (j.state = 'running' AND (j.locked_until IS NULL OR j.locked_until < now())))
		RETURNING j.id, j.tenant_id, tenant.slug, j.installation_id, i.external_id, i.credential_ref, j.delivery_id, j.provider, j.api_base_url, j.repository, j.clone_url,
			j.review_number, j.base_ref, j.base_sha, j.head_ref, j.head_sha, j.state, j.attempts,
			j.locked_by, j.locked_until, j.error_message, j.created_at, j.started_at, j.finished_at`, candidateID, workerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoQueuedJob
	}
	if err != nil {
		return nil, fmt.Errorf("claim fair review job: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO tenant_review_dispatches (tenant_id, last_claimed_at, last_job_id)
		VALUES ($1, now(), $2)
		ON CONFLICT (tenant_id) DO UPDATE
		SET last_claimed_at=EXCLUDED.last_claimed_at, last_job_id=EXCLUDED.last_job_id`, tenantID, job.ID); err != nil {
		return nil, fmt.Errorf("record tenant review dispatch: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit fair review job claim: %w", err)
	}
	return &job, nil
}

// ClaimForRun is the queue-driven counterpart of Claim. It uses the immutable
// review-run identifier carried by the outbox payload, so a busy tenant cannot
// cause a consumer to work an unrelated queued job.
func (s *PostgresStore) ClaimForRun(ctx context.Context, workerID string, runID uuid.UUID) (*domain.ReviewJob, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin queue review job claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Locking the tenant serializes broker delivery for the same workspace.
	// Without it, two distinct run messages could each observe spare capacity
	// before either job writes its lease.
	var tenantID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT tenant.id
		FROM review_runs r
		JOIN review_jobs j ON j.id = r.legacy_job_id
		JOIN tenants tenant ON tenant.id = j.tenant_id
		WHERE r.id = $1
		FOR UPDATE OF tenant`, runID).Scan(&tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoQueuedJob
	}
	if err != nil {
		return nil, fmt.Errorf("lock tenant queue claim: %w", err)
	}
	maxConcurrentRuns, err := tenantReviewConcurrencyLimit(ctx, tx, tenantID)
	if err != nil {
		return nil, err
	}
	job, err := scanJob(tx.QueryRow(ctx, `
		UPDATE review_jobs AS j
		SET state = 'running', attempts = attempts + 1, locked_by = $2,
			locked_until = now() + interval '2 minutes', started_at = now()
		FROM review_runs r, provider_installations i, tenants tenant
		WHERE r.id = $1
		  AND r.legacy_job_id = j.id
		  AND i.id = j.installation_id
		  AND tenant.id = j.tenant_id
		  AND r.state IN ('admitted', 'preparing', 'analyzing', 'normalizing', 'publishing')
		  AND i.active = TRUE
		  AND i.verification_state IN ('legacy', 'verified')
		  AND ((j.state = 'queued' AND j.available_at <= now()) OR (j.state = 'running' AND (j.locked_until IS NULL OR j.locked_until < now())))
		  AND (
			SELECT count(*)
			FROM review_jobs active_job
			JOIN review_runs active_run ON active_run.legacy_job_id = active_job.id
			WHERE active_job.tenant_id = j.tenant_id
			  AND active_job.state = 'running'
			  AND active_job.locked_until > now()
			  AND active_run.state IN ('admitted', 'preparing', 'analyzing', 'normalizing', 'publishing')
		  ) < $3
		RETURNING j.id, j.tenant_id, tenant.slug, j.installation_id, i.external_id, i.credential_ref, j.delivery_id, j.provider, j.api_base_url, j.repository, j.clone_url,
			j.review_number, j.base_ref, j.base_sha, j.head_ref, j.head_sha, j.state, j.attempts,
			j.locked_by, j.locked_until, j.error_message, j.created_at, j.started_at, j.finished_at`, runID, workerID, maxConcurrentRuns))
	if errors.Is(err, pgx.ErrNoRows) {
		// A broker message is exact-run work. When tenant capacity is currently
		// full, persist a short delay and ACK the message; the durable recovery
		// poller will claim it later. Requeueing immediately would create a hot
		// NACK loop, while ACKing without this delay could strand the run.
		if err := deferRunForTenantConcurrency(ctx, tx, runID, maxConcurrentRuns); err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit deferred queue claim: %w", err)
		}
		return nil, ErrNoQueuedJob
	}
	if err != nil {
		return nil, fmt.Errorf("claim review job for run: %w", err)
	}
	if job.TenantID != tenantID {
		return nil, fmt.Errorf("queue claim tenant changed during lock")
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit queue review job claim: %w", err)
	}
	return &job, nil
}

func deferRunForTenantConcurrency(ctx context.Context, tx pgx.Tx, runID uuid.UUID, maxConcurrentRuns int) error {
	command, err := tx.Exec(ctx, `
		UPDATE review_jobs AS j
		SET state = 'queued', locked_by = NULL, locked_until = NULL,
			available_at = GREATEST(available_at, now() + interval '15 seconds')
		FROM review_runs r
		WHERE r.id = $1
		  AND r.legacy_job_id = j.id
		  AND r.state IN ('admitted', 'preparing', 'analyzing', 'normalizing', 'publishing')
		  AND ((j.state = 'queued' AND j.available_at <= now()) OR (j.state = 'running' AND (j.locked_until IS NULL OR j.locked_until < now())))
		  AND (
			SELECT count(*)
			FROM review_jobs active_job
			JOIN review_runs active_run ON active_run.legacy_job_id = active_job.id
			WHERE active_job.tenant_id = j.tenant_id
			  AND active_job.state = 'running'
			  AND active_job.locked_until > now()
			  AND active_run.state IN ('admitted', 'preparing', 'analyzing', 'normalizing', 'publishing')
		  ) >= $2`, runID, maxConcurrentRuns)
	if err != nil {
		return fmt.Errorf("defer review run for tenant concurrency: %w", err)
	}
	if command.RowsAffected() > 1 {
		return fmt.Errorf("defer review run for tenant concurrency affected %d jobs", command.RowsAffected())
	}
	return nil
}

// tenantReviewConcurrencyLimit resolves the active workspace Models setting
// while the caller holds the tenant row lock. Repository-scoped model routes
// describe provider routing only and must never affect shared tenant capacity.
func tenantReviewConcurrencyLimit(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) (int, error) {
	var content json.RawMessage
	err := tx.QueryRow(ctx, `
		SELECT version.content
		FROM review_configurations configuration
		JOIN LATERAL (
			SELECT content
			FROM review_configuration_versions
			WHERE configuration_id = configuration.id
			ORDER BY revision DESC
			LIMIT 1
		) version ON TRUE
		WHERE configuration.tenant_id = $1
		  AND configuration.section = 'models'
		  AND configuration.scope_kind = 'tenant'
		  AND configuration.scope_ref = ''
		  AND configuration.active = TRUE`, tenantID).Scan(&content)
	if errors.Is(err, pgx.ErrNoRows) {
		return 2, nil
	}
	if err != nil {
		return 0, fmt.Errorf("load tenant review concurrency: %w", err)
	}
	route, err := domain.DecodeModelRoute(content)
	if err != nil {
		return 0, fmt.Errorf("decode tenant review concurrency: %w", err)
	}
	return route.MaxConcurrentRuns, nil
}

func tenantReviewAtCapacity(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, maxConcurrentRuns int) (bool, error) {
	var active int
	err := tx.QueryRow(ctx, `
		SELECT count(*)
		FROM review_jobs active_job
		JOIN review_runs active_run ON active_run.legacy_job_id = active_job.id
		WHERE active_job.tenant_id = $1
		  AND active_job.state = 'running'
		  AND active_job.locked_until > now()
		  AND active_run.state IN ('admitted', 'preparing', 'analyzing', 'normalizing', 'publishing')`, tenantID).Scan(&active)
	if err != nil {
		return false, fmt.Errorf("count active tenant review leases: %w", err)
	}
	return active >= maxConcurrentRuns, nil
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
		SELECT j.id, j.tenant_id, tenant.slug, j.installation_id, i.external_id, i.credential_ref, j.delivery_id, j.provider, j.api_base_url, j.repository, j.clone_url,
		       j.review_number, j.base_ref, j.base_sha, j.head_ref, j.head_sha, j.state, j.attempts,
		       j.locked_by, j.locked_until, j.error_message, j.created_at, j.started_at, j.finished_at,
		       r.state
		FROM review_runs r
		JOIN review_jobs j ON j.id = r.legacy_job_id
		JOIN provider_installations i ON i.id = j.installation_id
		JOIN tenants tenant ON tenant.id = j.tenant_id
		WHERE r.id = $1`, runID).Scan(
		&job.ID, &job.TenantID, &job.TenantSlug, &job.InstallationID, &job.InstallationExternalID, &job.CredentialRef, &job.DeliveryID, &job.Provider, &job.APIBaseURL, &job.Repository, &job.CloneURL,
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
		FROM provider_installations installation
		WHERE review_jobs.id = $1
		  AND review_jobs.installation_id = installation.id
		  AND review_jobs.state = 'running'
		  AND review_jobs.locked_by = $2
		  AND installation.active = TRUE
		  AND installation.verification_state IN ('legacy','verified')`, jobID, workerID, lease.String())
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
	if err := syncIssueOccurrences(ctx, tx, jobID, findings); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit findings: %w", err)
	}
	return nil
}

// RecordPublicationReceipts durably mirrors successful and incomplete
// provider publication attempts. Stable markers make the upsert idempotent:
// a resumed job updates the same evidence row after it updates the same
// GitHub/GitLab comment or status.
func (s *PostgresStore) RecordPublicationReceipts(ctx context.Context, jobID uuid.UUID, receipts []domain.PublicationReceipt) error {
	if jobID == uuid.Nil || len(receipts) == 0 {
		return fmt.Errorf("publication receipts are required")
	}
	unique := make([]domain.PublicationReceipt, 0, len(receipts))
	seen := make(map[string]struct{}, len(receipts))
	for _, receipt := range receipts {
		receipt.ReceiptKind = strings.TrimSpace(receipt.ReceiptKind)
		receipt.StableMarker = strings.TrimSpace(receipt.StableMarker)
		receipt.ExternalID = strings.TrimSpace(receipt.ExternalID)
		receipt.PayloadHash = strings.TrimSpace(receipt.PayloadHash)
		receipt.LastError = strings.TrimSpace(receipt.LastError)
		if !validPublicationReceipt(receipt) {
			return fmt.Errorf("publication receipt is invalid")
		}
		key := receipt.ReceiptKind + "\x00" + receipt.StableMarker
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, receipt)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin publication receipts: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var runID uuid.UUID
	var provider domain.Provider
	err = tx.QueryRow(ctx, `
		SELECT run.id, job.provider
		FROM review_runs run
		JOIN review_jobs job ON job.id = run.legacy_job_id
		WHERE job.id = $1
		FOR UPDATE`, jobID).Scan(&runID, &provider)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("load review run for publication receipts: %w", err)
	}
	for _, receipt := range unique {
		_, err = tx.Exec(ctx, `
			INSERT INTO publication_receipts (run_id, provider, receipt_kind, stable_marker, external_id, payload_hash, published_at, last_error)
			VALUES ($1, $2, $3, $4, $5, $6, CASE WHEN $7 THEN now() ELSE NULL END, NULLIF($8, ''))
			ON CONFLICT (run_id, receipt_kind, stable_marker) DO UPDATE
			SET external_id = COALESCE(NULLIF(EXCLUDED.external_id, ''), publication_receipts.external_id),
				payload_hash = EXCLUDED.payload_hash,
				published_at = CASE WHEN EXCLUDED.published_at IS NOT NULL THEN EXCLUDED.published_at ELSE publication_receipts.published_at END,
				last_error = CASE WHEN EXCLUDED.published_at IS NOT NULL THEN NULL ELSE EXCLUDED.last_error END,
				updated_at = now()`, runID, provider, receipt.ReceiptKind, receipt.StableMarker, receipt.ExternalID, receipt.PayloadHash, receipt.Published, receipt.LastError)
		if err != nil {
			return fmt.Errorf("upsert publication receipt: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit publication receipts: %w", err)
	}
	return nil
}

func validPublicationReceipt(receipt domain.PublicationReceipt) bool {
	if receipt.ReceiptKind != "ack" && receipt.ReceiptKind != "summary" && receipt.ReceiptKind != "inline_finding" && receipt.ReceiptKind != "status" {
		return false
	}
	if receipt.StableMarker == "" || len(receipt.StableMarker) > 500 || len(receipt.PayloadHash) != 64 || len(receipt.ExternalID) > 500 || len(receipt.LastError) > 500 {
		return false
	}
	if receipt.Published {
		return receipt.LastError == ""
	}
	return receipt.LastError != ""
}

// SaveReviewExecutionPlan writes the exact selected-path boundary before the
// model starts. Conflicting writes are rejected rather than silently changing
// the scope used to reproduce or publish a run.
func (s *PostgresStore) SaveReviewExecutionPlan(ctx context.Context, jobID uuid.UUID, input domain.ReviewExecutionPlan) (domain.ReviewExecutionPlan, error) {
	plan, valid := domain.NormalizeReviewExecutionPlan(input)
	if !valid {
		return domain.ReviewExecutionPlan{}, fmt.Errorf("review execution plan is invalid")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.ReviewExecutionPlan{}, fmt.Errorf("begin review execution plan: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var runID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM review_runs WHERE legacy_job_id = $1 FOR UPDATE`, jobID).Scan(&runID); errors.Is(err, pgx.ErrNoRows) {
		return domain.ReviewExecutionPlan{}, ErrNotFound
	} else if err != nil {
		return domain.ReviewExecutionPlan{}, fmt.Errorf("load run for execution plan: %w", err)
	}
	encoded, err := json.Marshal(plan.SelectedPaths)
	if err != nil {
		return domain.ReviewExecutionPlan{}, fmt.Errorf("encode selected review paths: %w", err)
	}
	encodedSignals, err := json.Marshal(plan.StaticImpactSignals)
	if err != nil {
		return domain.ReviewExecutionPlan{}, fmt.Errorf("encode static impact signals: %w", err)
	}
	encodedFiles, err := json.Marshal(plan.FileScopes)
	if err != nil {
		return domain.ReviewExecutionPlan{}, fmt.Errorf("encode review file scopes: %w", err)
	}
	var storedMode string
	var storedPaths []byte
	var storedDeferred int
	var storedSignals []byte
	var storedFiles []byte
	err = tx.QueryRow(ctx, `
		INSERT INTO review_execution_plans (run_id, mode, selected_paths, deferred_files, static_impact_signals, file_scopes)
		VALUES ($1, $2, $3::jsonb, $4, $5::jsonb, $6::jsonb)
		ON CONFLICT (run_id) DO NOTHING
		RETURNING mode, selected_paths, deferred_files, static_impact_signals, file_scopes`, runID, plan.Mode, string(encoded), plan.DeferredFiles, string(encodedSignals), string(encodedFiles)).Scan(&storedMode, &storedPaths, &storedDeferred, &storedSignals, &storedFiles)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT mode, selected_paths, deferred_files, static_impact_signals, file_scopes FROM review_execution_plans WHERE run_id = $1`, runID).Scan(&storedMode, &storedPaths, &storedDeferred, &storedSignals, &storedFiles)
	}
	if err != nil {
		return domain.ReviewExecutionPlan{}, fmt.Errorf("store review execution plan: %w", err)
	}
	stored := domain.ReviewExecutionPlan{Mode: storedMode, DeferredFiles: storedDeferred}
	if err := json.Unmarshal(storedPaths, &stored.SelectedPaths); err != nil {
		return domain.ReviewExecutionPlan{}, fmt.Errorf("decode stored review execution plan: %w", err)
	}
	if err := json.Unmarshal(storedSignals, &stored.StaticImpactSignals); err != nil {
		return domain.ReviewExecutionPlan{}, fmt.Errorf("decode stored static impact signals: %w", err)
	}
	if err := json.Unmarshal(storedFiles, &stored.FileScopes); err != nil {
		return domain.ReviewExecutionPlan{}, fmt.Errorf("decode stored review file scopes: %w", err)
	}
	if stored, valid = domain.NormalizeReviewExecutionPlan(stored); !valid {
		return domain.ReviewExecutionPlan{}, fmt.Errorf("stored review execution plan is invalid")
	}
	if stored.Mode != plan.Mode || stored.DeferredFiles != plan.DeferredFiles || !sameStrings(stored.SelectedPaths, plan.SelectedPaths) || !sameStrings(stored.StaticImpactSignals, plan.StaticImpactSignals) || !sameReviewFileScopes(stored.FileScopes, plan.FileScopes) {
		return domain.ReviewExecutionPlan{}, ErrConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ReviewExecutionPlan{}, fmt.Errorf("commit review execution plan: %w", err)
	}
	return stored, nil
}

func (s *PostgresStore) ReviewExecutionPlanForJob(ctx context.Context, jobID uuid.UUID) (domain.ReviewExecutionPlan, error) {
	var plan domain.ReviewExecutionPlan
	var paths []byte
	var signals []byte
	var files []byte
	err := s.pool.QueryRow(ctx, `
		SELECT plan.mode, plan.selected_paths, plan.deferred_files, plan.static_impact_signals, plan.file_scopes
		FROM review_execution_plans plan
		JOIN review_runs run ON run.id = plan.run_id
		WHERE run.legacy_job_id = $1`, jobID).Scan(&plan.Mode, &paths, &plan.DeferredFiles, &signals, &files)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ReviewExecutionPlan{}, ErrNotFound
	}
	if err != nil {
		return domain.ReviewExecutionPlan{}, fmt.Errorf("load review execution plan: %w", err)
	}
	if err := json.Unmarshal(paths, &plan.SelectedPaths); err != nil {
		return domain.ReviewExecutionPlan{}, fmt.Errorf("decode review execution plan: %w", err)
	}
	if err := json.Unmarshal(signals, &plan.StaticImpactSignals); err != nil {
		return domain.ReviewExecutionPlan{}, fmt.Errorf("decode review execution plan static impact signals: %w", err)
	}
	if err := json.Unmarshal(files, &plan.FileScopes); err != nil {
		return domain.ReviewExecutionPlan{}, fmt.Errorf("decode review execution plan file scopes: %w", err)
	}
	if normalized, valid := domain.NormalizeReviewExecutionPlan(plan); !valid {
		return domain.ReviewExecutionPlan{}, fmt.Errorf("stored review execution plan is invalid")
	} else {
		return normalized, nil
	}
}

func (s *PostgresStore) FindingsForJob(ctx context.Context, jobID uuid.UUID) ([]domain.Finding, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT path, start_line, end_line, severity, category, body, suggestion,
		       code_excerpt, code_excerpt_start_line, proposed_patch
		FROM review_findings
		WHERE job_id = $1
		ORDER BY created_at, id`, jobID)
	if err != nil {
		return nil, fmt.Errorf("list persisted review findings: %w", err)
	}
	defer rows.Close()
	findings := make([]domain.Finding, 0)
	for rows.Next() {
		var finding domain.Finding
		if err := rows.Scan(&finding.Path, &finding.StartLine, &finding.EndLine, &finding.Severity, &finding.Category,
			&finding.Body, &finding.Suggestion, &finding.CodeExcerpt, &finding.CodeExcerptStartLine, &finding.ProposedPatch); err != nil {
			return nil, fmt.Errorf("scan persisted review finding: %w", err)
		}
		findings = append(findings, finding)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate persisted review findings: %w", err)
	}
	attributes, err := s.findingRuleReferencesForJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	for index := range findings {
		findings[index].RuleReferences = attributes[findingFingerprint(findings[index])]
	}
	return findings, nil
}

func (s *PostgresStore) findingRuleReferencesForJob(ctx context.Context, jobID uuid.UUID) (map[string][]domain.FindingRuleReference, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT finding.fingerprint, attribution.rule_key, attribution.rule_version_id
		FROM review_findings finding
		JOIN review_finding_rule_attributions attribution ON attribution.finding_id = finding.id
		WHERE finding.job_id = $1
		ORDER BY finding.fingerprint, attribution.rule_key, attribution.rule_version_id`, jobID)
	if err != nil {
		return nil, fmt.Errorf("list persisted finding rule attributions: %w", err)
	}
	defer rows.Close()
	result := make(map[string][]domain.FindingRuleReference)
	for rows.Next() {
		var fingerprint, key string
		var version uuid.UUID
		if err := rows.Scan(&fingerprint, &key, &version); err != nil {
			return nil, fmt.Errorf("scan persisted finding rule attribution: %w", err)
		}
		result[fingerprint] = append(result[fingerprint], domain.FindingRuleReference{RuleKey: key, SourceVersion: version.String()})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate persisted finding rule attributions: %w", err)
	}
	return result, nil
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func sameReviewFileScopes(left, right []domain.ReviewFileScope) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Path != right[index].Path || left[index].Selected != right[index].Selected || left[index].Score != right[index].Score || !sameStrings(left[index].Reasons, right[index].Reasons) || left[index].ChangeType != right[index].ChangeType || left[index].PreviousPath != right[index].PreviousPath || left[index].Additions != right[index].Additions || left[index].Deletions != right[index].Deletions || left[index].StatsKnown != right[index].StatsKnown || left[index].Binary != right[index].Binary {
			return false
		}
	}
	return true
}

func (s *PostgresStore) RecordFindingReaction(ctx context.Context, input domain.FindingReaction) error {
	if !input.Provider.Valid() || input.DeliveryID == "" || input.ReactionExternalID == "" || input.ActorExternalID == "" || input.Repository == "" || input.FindingMarker == "" || (input.Kind != "useful" && input.Kind != "false_positive") || (input.Action != "created" && input.Action != "deleted") {
		return fmt.Errorf("finding reaction is invalid")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin finding reaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var tenantID, findingID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT job.tenant_id, finding.id
		FROM review_findings finding
		JOIN review_jobs job ON job.id = finding.job_id
		WHERE finding.provider_marker = $1
		  AND job.provider = $2
		  AND job.repository = $3`, input.FindingMarker, input.Provider, input.Repository).Scan(&tenantID, &findingID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("resolve finding reaction: %w", err)
	}
	if input.Action == "deleted" {
		_, err = tx.Exec(ctx, `UPDATE finding_feedback SET retracted_at = now() WHERE provider = $1 AND reaction_external_id = $2 AND finding_id = $3`, input.Provider, input.ReactionExternalID, findingID)
	} else {
		_, err = tx.Exec(ctx, `
			INSERT INTO finding_feedback (tenant_id, finding_id, provider, delivery_id, reaction_external_id, actor_external_id, kind)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (provider, reaction_external_id) DO UPDATE
			SET kind = EXCLUDED.kind, retracted_at = NULL`, tenantID, findingID, input.Provider, input.DeliveryID, input.ReactionExternalID, input.ActorExternalID, input.Kind)
	}
	if err != nil {
		return fmt.Errorf("record finding reaction: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata) VALUES ($1, $2, $3, $4, jsonb_build_object('kind', $5::text, 'provider', $6::text))`, tenantID, "provider:"+input.ActorExternalID, "finding_feedback."+input.Action, findingID.String(), input.Kind, input.Provider); err != nil {
		return fmt.Errorf("audit finding reaction: %w", err)
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) Succeed(ctx context.Context, jobID uuid.UUID, workerID string) error {
	return s.finish(ctx, jobID, workerID, domain.JobSucceeded, "")
}

func (s *PostgresStore) Fail(ctx context.Context, jobID uuid.UUID, workerID, message string) error {
	return s.failWithMinimumRetryDelay(ctx, jobID, workerID, message, 0)
}

// FailWithRetryAfter preserves the queue's exponential backoff while also
// respecting a bounded provider Retry-After hint. The delay is stored in
// available_at, so a worker restart or rebalance cannot bypass provider rate
// limiting by retrying immediately.
func (s *PostgresStore) FailWithRetryAfter(ctx context.Context, jobID uuid.UUID, workerID, message string, retryAfter time.Duration) error {
	if retryAfter < 0 {
		return fmt.Errorf("provider retry-after cannot be negative")
	}
	if retryAfter > 15*time.Minute {
		retryAfter = 15 * time.Minute
	}
	return s.failWithMinimumRetryDelay(ctx, jobID, workerID, message, retryAfter)
}

func (s *PostgresStore) failWithMinimumRetryDelay(ctx context.Context, jobID uuid.UUID, workerID, message string, minimumDelay time.Duration) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE review_jobs
		SET state = CASE WHEN attempts >= 5 THEN 'failed' ELSE 'queued' END,
			error_message = $1,
			available_at = CASE WHEN attempts >= 5 THEN available_at ELSE now() + GREATEST(LEAST(900, 5 * power(2, attempts)) * interval '1 second', $4::interval) END,
			locked_by = NULL,
			locked_until = NULL,
			finished_at = CASE WHEN attempts >= 5 THEN now() ELSE NULL END
		WHERE id = $2 AND state = 'running' AND locked_by = $3`, message, jobID, workerID, minimumDelay.String())
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
		&job.ID, &job.TenantID, &job.TenantSlug, &job.InstallationID, &job.InstallationExternalID, &job.CredentialRef, &job.DeliveryID, &job.Provider, &job.APIBaseURL, &job.Repository, &job.CloneURL,
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
