package store

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const ssoProbeLease = 45 * time.Second

func (s *PostgresStore) GetSSOOverview(ctx context.Context, actor, tenantSlug string) (domain.SSOOverview, error) {
	tenantID, role, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.SSOOverview{}, err
	}
	if role != "owner" && role != "admin" {
		return domain.SSOOverview{}, ErrForbidden
	}
	return s.getSSOOverview(ctx, tenantID)
}

func (s *PostgresStore) getSSOOverview(ctx context.Context, tenantID uuid.UUID) (domain.SSOOverview, error) {
	overview := domain.SSOOverview{Domains: []domain.SSODomain{}, Mappings: []domain.SSORoleMapping{}, Probes: []domain.SSOProbeReceipt{}}
	configuration, err := scanSSOConfiguration(s.pool.QueryRow(ctx, ssoConfigurationSelect+` WHERE tenant_id = $1`, tenantID))
	if err == nil {
		overview.Configuration = &configuration
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return domain.SSOOverview{}, fmt.Errorf("load SSO configuration: %w", err)
	}

	domainRows, err := s.pool.Query(ctx, `
		SELECT id, domain, challenge_token, state, revision, created_by, created_at, verified_at
		FROM workspace_sso_domains WHERE tenant_id = $1 ORDER BY lower(domain)`, tenantID)
	if err != nil {
		return domain.SSOOverview{}, fmt.Errorf("list SSO domains: %w", err)
	}
	defer domainRows.Close()
	for domainRows.Next() {
		item, scanErr := scanSSODomain(domainRows)
		if scanErr != nil {
			return domain.SSOOverview{}, fmt.Errorf("scan SSO domain: %w", scanErr)
		}
		overview.Domains = append(overview.Domains, item)
	}
	if err := domainRows.Err(); err != nil {
		return domain.SSOOverview{}, fmt.Errorf("iterate SSO domains: %w", err)
	}

	mappingRows, err := s.pool.Query(ctx, `
		SELECT id, group_value, role, repository_scope, revision, created_by, created_at, updated_at
		FROM workspace_sso_role_mappings WHERE tenant_id = $1 ORDER BY lower(group_value), repository_scope`, tenantID)
	if err != nil {
		return domain.SSOOverview{}, fmt.Errorf("list SSO role mappings: %w", err)
	}
	defer mappingRows.Close()
	for mappingRows.Next() {
		item, scanErr := scanSSORoleMapping(mappingRows)
		if scanErr != nil {
			return domain.SSOOverview{}, fmt.Errorf("scan SSO role mapping: %w", scanErr)
		}
		overview.Mappings = append(overview.Mappings, item)
	}
	if err := mappingRows.Err(); err != nil {
		return domain.SSOOverview{}, fmt.Errorf("iterate SSO role mappings: %w", err)
	}

	probeRows, err := s.pool.Query(ctx, `
		SELECT id, config_revision, protocol, target_url, state, attempt, metadata_sha256,
		       error_code, error_message, requested_by, created_at, started_at, finished_at
		FROM workspace_sso_probe_receipts WHERE tenant_id = $1
		ORDER BY created_at DESC, id DESC LIMIT 20`, tenantID)
	if err != nil {
		return domain.SSOOverview{}, fmt.Errorf("list SSO probes: %w", err)
	}
	defer probeRows.Close()
	for probeRows.Next() {
		item, scanErr := scanSSOProbeReceipt(probeRows)
		if scanErr != nil {
			return domain.SSOOverview{}, fmt.Errorf("scan SSO probe: %w", scanErr)
		}
		overview.Probes = append(overview.Probes, item)
	}
	if err := probeRows.Err(); err != nil {
		return domain.SSOOverview{}, fmt.Errorf("iterate SSO probes: %w", err)
	}
	overview.Readiness = calculateSSOReadiness(overview.Configuration, overview.Domains, overview.Mappings)
	return overview, nil
}

func (s *PostgresStore) SaveSSOConfiguration(ctx context.Context, actor, tenantSlug string, input domain.SSOConfigurationInput) (domain.SSOOverview, error) {
	input, valid := domain.NormalizeSSOConfigurationInput(input)
	if !valid {
		return domain.SSOOverview{}, ErrInvalidSSO
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.SSOOverview{}, fmt.Errorf("begin SSO configuration save: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.SSOOverview{}, err
	}
	if role != "owner" && role != "admin" {
		return domain.SSOOverview{}, ErrForbidden
	}
	var currentRevision int
	var currentState, currentSecret string
	err = tx.QueryRow(ctx, `SELECT revision, state, secret_ref FROM workspace_sso_configurations WHERE tenant_id = $1 FOR UPDATE`, tenantID).Scan(&currentRevision, &currentState, &currentSecret)
	if errors.Is(err, pgx.ErrNoRows) {
		if input.ExpectedRevision != 0 || input.KeepSecret {
			return domain.SSOOverview{}, ErrRevisionConflict
		}
		currentRevision = 0
		currentSecret = ""
	} else if err != nil {
		return domain.SSOOverview{}, fmt.Errorf("lock SSO configuration: %w", err)
	} else if currentState == string(domain.SSOStateEnforced) {
		return domain.SSOOverview{}, ErrConflict
	} else if input.ExpectedRevision != currentRevision {
		return domain.SSOOverview{}, ErrRevisionConflict
	}
	secretRef := input.SecretRef
	if input.KeepSecret {
		secretRef = currentSecret
	}
	newRevision := currentRevision + 1
	_, err = tx.Exec(ctx, `
		INSERT INTO workspace_sso_configurations (
			tenant_id, protocol, display_name, issuer_url, metadata_url, client_id,
			secret_ref, group_claim, state, revision, updated_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'draft_saved',$9,$10)
		ON CONFLICT (tenant_id) DO UPDATE SET
			protocol = EXCLUDED.protocol, display_name = EXCLUDED.display_name,
			issuer_url = EXCLUDED.issuer_url, metadata_url = EXCLUDED.metadata_url,
			client_id = EXCLUDED.client_id, secret_ref = EXCLUDED.secret_ref,
			group_claim = EXCLUDED.group_claim, state = 'draft_saved',
			revision = EXCLUDED.revision, tested_revision = NULL,
			break_glass_subject = '', enforced_at = NULL,
			updated_by = EXCLUDED.updated_by, updated_at = now()`,
		tenantID, input.Protocol, input.DisplayName, input.IssuerURL, input.MetadataURL,
		input.ClientID, secretRef, input.GroupClaim, newRevision, actor)
	if err != nil {
		return domain.SSOOverview{}, fmt.Errorf("save SSO configuration: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE workspace_sso_probe_receipts
		SET state='failed', error_code='configuration_changed', error_message='A newer configuration revision superseded this probe.', finished_at=now(), locked_until=NULL
		WHERE tenant_id=$1 AND state IN ('queued','running') AND config_revision <> $2`, tenantID, newRevision); err != nil {
		return domain.SSOOverview{}, fmt.Errorf("supersede stale SSO probes: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata) VALUES ($1,$2,'sso.configuration_saved',$3,jsonb_build_object('revision',$4::int,'protocol',$5::text))`, tenantID, actor, tenantID.String(), newRevision, input.Protocol); err != nil {
		return domain.SSOOverview{}, fmt.Errorf("audit SSO configuration save: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.SSOOverview{}, fmt.Errorf("commit SSO configuration save: %w", err)
	}
	return s.getSSOOverview(ctx, tenantID)
}

func (s *PostgresStore) RequestSSOProbe(ctx context.Context, actor, tenantSlug string, expectedRevision int) (domain.SSOProbeReceipt, error) {
	if expectedRevision < 1 {
		return domain.SSOProbeReceipt{}, ErrRevisionConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.SSOProbeReceipt{}, fmt.Errorf("begin SSO probe request: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.SSOProbeReceipt{}, err
	}
	if role != "owner" && role != "admin" {
		return domain.SSOProbeReceipt{}, ErrForbidden
	}
	var protocol domain.SSOProtocol
	var issuerURL, metadataURL string
	err = tx.QueryRow(ctx, `SELECT protocol, issuer_url, metadata_url FROM workspace_sso_configurations WHERE tenant_id=$1 AND revision=$2 AND state <> 'enforced' FOR UPDATE`, tenantID, expectedRevision).Scan(&protocol, &issuerURL, &metadataURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.SSOProbeReceipt{}, ErrRevisionConflict
	}
	if err != nil {
		return domain.SSOProbeReceipt{}, fmt.Errorf("load SSO probe target: %w", err)
	}
	var active bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM workspace_sso_probe_receipts WHERE tenant_id=$1 AND config_revision=$2 AND state IN ('queued','running'))`, tenantID, expectedRevision).Scan(&active); err != nil {
		return domain.SSOProbeReceipt{}, fmt.Errorf("check active SSO probe: %w", err)
	}
	if active {
		return domain.SSOProbeReceipt{}, ErrConflict
	}
	targetURL := issuerURL
	if protocol == domain.SSOProtocolSAML {
		targetURL = metadataURL
	}
	receipt, err := scanSSOProbeReceipt(tx.QueryRow(ctx, `
		INSERT INTO workspace_sso_probe_receipts (tenant_id,config_revision,protocol,target_url,state,requested_by)
		VALUES ($1,$2,$3,$4,'queued',$5)
		RETURNING id,config_revision,protocol,target_url,state,attempt,metadata_sha256,error_code,error_message,requested_by,created_at,started_at,finished_at`, tenantID, expectedRevision, protocol, targetURL, actor))
	if err != nil {
		return domain.SSOProbeReceipt{}, fmt.Errorf("create SSO probe receipt: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE workspace_sso_configurations SET state='testing',updated_at=now() WHERE tenant_id=$1`, tenantID); err != nil {
		return domain.SSOProbeReceipt{}, fmt.Errorf("mark SSO configuration testing: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata) VALUES ($1,$2,'sso.probe_requested',$3,jsonb_build_object('config_revision',$4::int))`, tenantID, actor, receipt.ID.String(), expectedRevision); err != nil {
		return domain.SSOProbeReceipt{}, fmt.Errorf("audit SSO probe request: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.SSOProbeReceipt{}, fmt.Errorf("commit SSO probe request: %w", err)
	}
	return receipt, nil
}

func (s *PostgresStore) ClaimSSOProbe(ctx context.Context, workerID string) (*domain.SSOProbeTarget, error) {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return nil, ErrInvalidSSO
	}
	var target domain.SSOProbeTarget
	err := s.pool.QueryRow(ctx, `
		WITH candidate AS (
			SELECT probe.id FROM workspace_sso_probe_receipts probe
			JOIN workspace_sso_configurations config ON config.tenant_id=probe.tenant_id AND config.revision=probe.config_revision
			WHERE probe.state='queued' OR (probe.state='running' AND probe.locked_until < now())
			ORDER BY probe.created_at FOR UPDATE OF probe SKIP LOCKED LIMIT 1
		), claimed AS (
			UPDATE workspace_sso_probe_receipts probe
			SET state='running',attempt=attempt+1,worker_id=$1,locked_until=now()+$2::interval,started_at=COALESCE(started_at,now()),error_code=NULL,error_message=NULL
			FROM candidate WHERE probe.id=candidate.id
			RETURNING probe.id,probe.tenant_id,probe.config_revision,probe.protocol,probe.target_url
		)
		SELECT claimed.id,claimed.tenant_id,claimed.config_revision,claimed.protocol,claimed.target_url,config.client_id
		FROM claimed JOIN workspace_sso_configurations config ON config.tenant_id=claimed.tenant_id`, workerID, ssoProbeLease.String()).Scan(&target.ReceiptID, &target.TenantID, &target.ConfigRevision, &target.Protocol, &target.TargetURL, &target.ExpectedClientID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoQueuedSSOProbe
	}
	if err != nil {
		return nil, fmt.Errorf("claim SSO probe: %w", err)
	}
	return &target, nil
}

func (s *PostgresStore) CompleteSSOProbe(ctx context.Context, receiptID uuid.UUID, workerID, metadataSHA256, errorCode, errorMessage string) error {
	if receiptID == uuid.Nil || strings.TrimSpace(workerID) == "" {
		return ErrInvalidSSO
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin SSO probe completion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	state := domain.SSOProbeSucceeded
	if errorCode != "" {
		state = domain.SSOProbeFailed
		metadataSHA256 = ""
	}
	var tenantID uuid.UUID
	var revision int
	err = tx.QueryRow(ctx, `
		UPDATE workspace_sso_probe_receipts
		SET state=$3,metadata_sha256=NULLIF($4,''),error_code=NULLIF($5,''),error_message=NULLIF($6,''),finished_at=now(),locked_until=NULL
		WHERE id=$1 AND worker_id=$2 AND state='running' AND locked_until >= now()
		RETURNING tenant_id,config_revision`, receiptID, workerID, state, metadataSHA256, errorCode, truncateSSOError(errorMessage)).Scan(&tenantID, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrSSOProbeClaimLost
	}
	if err != nil {
		return fmt.Errorf("complete SSO probe receipt: %w", err)
	}
	if state == domain.SSOProbeSucceeded {
		if _, err := tx.Exec(ctx, `UPDATE workspace_sso_configurations SET tested_revision=$2,state='verified',updated_at=now() WHERE tenant_id=$1 AND revision=$2 AND state='testing'`, tenantID, revision); err != nil {
			return fmt.Errorf("mark SSO configuration verified: %w", err)
		}
		if err := refreshSSOReadinessState(ctx, tx, tenantID); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(ctx, `UPDATE workspace_sso_configurations SET state='draft_saved',updated_at=now() WHERE tenant_id=$1 AND revision=$2 AND state='testing'`, tenantID, revision); err != nil {
			return fmt.Errorf("restore failed SSO configuration draft: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata) VALUES ($1,$2,'sso.probe_completed',$3,jsonb_build_object('state',$4::text,'config_revision',$5::int,'error_code',$6::text))`, tenantID, "worker:"+workerID, receiptID.String(), state, revision, errorCode); err != nil {
		return fmt.Errorf("audit SSO probe completion: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit SSO probe completion: %w", err)
	}
	return nil
}

func (s *PostgresStore) AddSSODomain(ctx context.Context, actor, tenantSlug, rawDomain string) (domain.SSODomain, error) {
	value, valid := domain.NormalizeSSODomain(rawDomain)
	if !valid {
		return domain.SSODomain{}, ErrInvalidSSO
	}
	tokenBytes := make([]byte, 24)
	if _, err := rand.Read(tokenBytes); err != nil {
		return domain.SSODomain{}, fmt.Errorf("generate SSO domain challenge: %w", err)
	}
	challenge := "open-review-verification=" + base64.RawURLEncoding.EncodeToString(tokenBytes)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.SSODomain{}, fmt.Errorf("begin SSO domain creation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.SSODomain{}, err
	}
	if role != "owner" && role != "admin" {
		return domain.SSODomain{}, ErrForbidden
	}
	item, err := scanSSODomain(tx.QueryRow(ctx, `
		INSERT INTO workspace_sso_domains (tenant_id,domain,challenge_token,created_by)
		VALUES ($1,$2,$3,$4)
		RETURNING id,domain,challenge_token,state,revision,created_by,created_at,verified_at`, tenantID, value, challenge, actor))
	if isUniqueViolation(err) {
		return domain.SSODomain{}, ErrConflict
	}
	if err != nil {
		return domain.SSODomain{}, fmt.Errorf("create SSO domain: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata) VALUES ($1,$2,'sso.domain_added',$3,jsonb_build_object('domain',$4::text))`, tenantID, actor, item.ID.String(), item.Domain); err != nil {
		return domain.SSODomain{}, fmt.Errorf("audit SSO domain creation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.SSODomain{}, fmt.Errorf("commit SSO domain creation: %w", err)
	}
	return item, nil
}

func (s *PostgresStore) VerifySSODomain(ctx context.Context, actor, tenantSlug string, domainID uuid.UUID, observedTXT []string) (domain.SSODomain, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.SSODomain{}, fmt.Errorf("begin SSO domain verification: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.SSODomain{}, err
	}
	if role != "owner" && role != "admin" {
		return domain.SSODomain{}, ErrForbidden
	}
	var expected string
	if err := tx.QueryRow(ctx, `SELECT challenge_token FROM workspace_sso_domains WHERE id=$1 AND tenant_id=$2 FOR UPDATE`, domainID, tenantID).Scan(&expected); errors.Is(err, pgx.ErrNoRows) {
		return domain.SSODomain{}, ErrNotFound
	} else if err != nil {
		return domain.SSODomain{}, fmt.Errorf("load SSO domain challenge: %w", err)
	}
	matched := false
	for _, value := range observedTXT {
		if strings.TrimSpace(value) == expected {
			matched = true
			break
		}
	}
	if !matched {
		return domain.SSODomain{}, ErrSSODomainUnverified
	}
	item, err := scanSSODomain(tx.QueryRow(ctx, `
		UPDATE workspace_sso_domains SET state='verified',revision=revision+1,verified_at=COALESCE(verified_at,now())
		WHERE id=$1 AND tenant_id=$2
		RETURNING id,domain,challenge_token,state,revision,created_by,created_at,verified_at`, domainID, tenantID))
	if err != nil {
		return domain.SSODomain{}, fmt.Errorf("verify SSO domain: %w", err)
	}
	if err := refreshSSOReadinessState(ctx, tx, tenantID); err != nil {
		return domain.SSODomain{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata) VALUES ($1,$2,'sso.domain_verified',$3,jsonb_build_object('domain',$4::text))`, tenantID, actor, item.ID.String(), item.Domain); err != nil {
		return domain.SSODomain{}, fmt.Errorf("audit SSO domain verification: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.SSODomain{}, fmt.Errorf("commit SSO domain verification: %w", err)
	}
	return item, nil
}

func (s *PostgresStore) CreateSSORoleMapping(ctx context.Context, actor, tenantSlug string, input domain.SSORoleMappingInput) (domain.SSORoleMapping, error) {
	input, valid := domain.NormalizeSSORoleMappingInput(input)
	if !valid {
		return domain.SSORoleMapping{}, ErrInvalidSSO
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.SSORoleMapping{}, fmt.Errorf("begin SSO mapping creation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.SSORoleMapping{}, err
	}
	if role != "owner" && role != "admin" {
		return domain.SSORoleMapping{}, ErrForbidden
	}
	item, err := scanSSORoleMapping(tx.QueryRow(ctx, `
		INSERT INTO workspace_sso_role_mappings (tenant_id,group_value,role,repository_scope,created_by)
		VALUES ($1,$2,$3,$4,$5)
		RETURNING id,group_value,role,repository_scope,revision,created_by,created_at,updated_at`, tenantID, input.GroupValue, input.Role, input.RepositoryScope, actor))
	if isUniqueViolation(err) {
		return domain.SSORoleMapping{}, ErrConflict
	}
	if err != nil {
		return domain.SSORoleMapping{}, fmt.Errorf("create SSO role mapping: %w", err)
	}
	if err := refreshSSOReadinessState(ctx, tx, tenantID); err != nil {
		return domain.SSORoleMapping{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata) VALUES ($1,$2,'sso.mapping_created',$3,jsonb_build_object('group_value',$4::text,'role',$5::text,'repository_scope',$6::text))`, tenantID, actor, item.ID.String(), item.GroupValue, item.Role, item.RepositoryScope); err != nil {
		return domain.SSORoleMapping{}, fmt.Errorf("audit SSO mapping creation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.SSORoleMapping{}, fmt.Errorf("commit SSO mapping creation: %w", err)
	}
	return item, nil
}

func (s *PostgresStore) DeleteSSORoleMapping(ctx context.Context, actor, tenantSlug string, mappingID uuid.UUID, expectedRevision int) error {
	if mappingID == uuid.Nil || expectedRevision < 1 {
		return ErrInvalidSSO
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin SSO mapping deletion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return err
	}
	if role != "owner" && role != "admin" {
		return ErrForbidden
	}
	result, err := tx.Exec(ctx, `DELETE FROM workspace_sso_role_mappings WHERE id=$1 AND tenant_id=$2 AND revision=$3`, mappingID, tenantID, expectedRevision)
	if err != nil {
		return fmt.Errorf("delete SSO role mapping: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrRevisionConflict
	}
	if err := refreshSSOReadinessState(ctx, tx, tenantID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id,actor_subject,action,target) VALUES ($1,$2,'sso.mapping_deleted',$3)`, tenantID, actor, mappingID.String()); err != nil {
		return fmt.Errorf("audit SSO mapping deletion: %w", err)
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) EnforceSSO(ctx context.Context, actor, tenantSlug string, input domain.SSOEnforcementInput) (domain.SSOOverview, error) {
	input.BreakGlassSubject = strings.TrimSpace(input.BreakGlassSubject)
	if input.ExpectedRevision < 1 || input.BreakGlassSubject == "" {
		return domain.SSOOverview{}, ErrInvalidSSO
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.SSOOverview{}, fmt.Errorf("begin SSO enforcement: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.SSOOverview{}, err
	}
	if role != "owner" {
		return domain.SSOOverview{}, ErrForbidden
	}
	var breakGlassRole string
	if err := tx.QueryRow(ctx, `SELECT role FROM memberships WHERE tenant_id=$1 AND subject=$2 AND active=TRUE`, tenantID, input.BreakGlassSubject).Scan(&breakGlassRole); errors.Is(err, pgx.ErrNoRows) {
		return domain.SSOOverview{}, ErrInvalidSSO
	} else if err != nil {
		return domain.SSOOverview{}, fmt.Errorf("validate SSO break-glass owner: %w", err)
	}
	if breakGlassRole != "owner" {
		return domain.SSOOverview{}, ErrInvalidSSO
	}
	result, err := tx.Exec(ctx, `
		UPDATE workspace_sso_configurations SET state='enforced',break_glass_subject=$3,enforced_at=now(),updated_by=$4,updated_at=now()
		WHERE tenant_id=$1 AND revision=$2 AND state='enforcement_ready' AND tested_revision=revision`, tenantID, input.ExpectedRevision, input.BreakGlassSubject, actor)
	if err != nil {
		return domain.SSOOverview{}, fmt.Errorf("enforce SSO: %w", err)
	}
	if result.RowsAffected() == 0 {
		return domain.SSOOverview{}, ErrSSONotReady
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata) VALUES ($1,$2,'sso.enforced',$3,jsonb_build_object('revision',$4::int,'break_glass_subject',$5::text))`, tenantID, actor, tenantID.String(), input.ExpectedRevision, input.BreakGlassSubject); err != nil {
		return domain.SSOOverview{}, fmt.Errorf("audit SSO enforcement: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.SSOOverview{}, fmt.Errorf("commit SSO enforcement: %w", err)
	}
	return s.getSSOOverview(ctx, tenantID)
}

func (s *PostgresStore) SuspendSSO(ctx context.Context, actor, tenantSlug string, expectedRevision int) (domain.SSOOverview, error) {
	if expectedRevision < 1 {
		return domain.SSOOverview{}, ErrRevisionConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.SSOOverview{}, fmt.Errorf("begin SSO suspension: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.SSOOverview{}, err
	}
	if role != "owner" {
		return domain.SSOOverview{}, ErrForbidden
	}
	result, err := tx.Exec(ctx, `UPDATE workspace_sso_configurations SET state='suspended',updated_by=$3,updated_at=now() WHERE tenant_id=$1 AND revision=$2 AND state='enforced'`, tenantID, expectedRevision, actor)
	if err != nil {
		return domain.SSOOverview{}, fmt.Errorf("suspend SSO: %w", err)
	}
	if result.RowsAffected() == 0 {
		return domain.SSOOverview{}, ErrConflict
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata) VALUES ($1,$2,'sso.suspended',$3,jsonb_build_object('revision',$4::int))`, tenantID, actor, tenantID.String(), expectedRevision); err != nil {
		return domain.SSOOverview{}, fmt.Errorf("audit SSO suspension: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.SSOOverview{}, fmt.Errorf("commit SSO suspension: %w", err)
	}
	return s.getSSOOverview(ctx, tenantID)
}

const ssoConfigurationSelect = `
	SELECT tenant_id,protocol,display_name,issuer_url,metadata_url,client_id,(secret_ref <> ''),group_claim,state,
	       revision,tested_revision,break_glass_subject,enforced_at,updated_by,created_at,updated_at
	FROM workspace_sso_configurations`

func refreshSSOReadinessState(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE workspace_sso_configurations config
		SET state = CASE
			WHEN config.state IN ('enforced','suspended') THEN config.state
			WHEN config.tested_revision IS DISTINCT FROM config.revision THEN 'draft_saved'
			WHEN EXISTS (SELECT 1 FROM workspace_sso_domains domain WHERE domain.tenant_id=config.tenant_id AND domain.state='verified')
			 AND EXISTS (SELECT 1 FROM workspace_sso_role_mappings mapping WHERE mapping.tenant_id=config.tenant_id)
			THEN 'enforcement_ready'
			ELSE 'verified' END,
			updated_at=now()
		WHERE config.tenant_id=$1`, tenantID)
	if err != nil {
		return fmt.Errorf("refresh SSO readiness: %w", err)
	}
	return nil
}

func calculateSSOReadiness(configuration *domain.SSOConfiguration, domains []domain.SSODomain, mappings []domain.SSORoleMapping) domain.SSOReadiness {
	blockers := make([]string, 0, 3)
	if configuration == nil {
		blockers = append(blockers, "identity_provider_not_configured")
	} else if configuration.TestedRevision == nil || *configuration.TestedRevision != configuration.Revision {
		blockers = append(blockers, "current_revision_not_tested")
	}
	verifiedDomain := false
	for _, item := range domains {
		verifiedDomain = verifiedDomain || item.State == "verified"
	}
	if !verifiedDomain {
		blockers = append(blockers, "verified_domain_required")
	}
	if len(mappings) == 0 {
		blockers = append(blockers, "role_mapping_required")
	}
	return domain.SSOReadiness{Ready: len(blockers) == 0, Blockers: blockers}
}

func scanSSOConfiguration(row rowScanner) (domain.SSOConfiguration, error) {
	var item domain.SSOConfiguration
	err := row.Scan(&item.TenantID, &item.Protocol, &item.DisplayName, &item.IssuerURL, &item.MetadataURL, &item.ClientID, &item.SecretConfigured, &item.GroupClaim, &item.State, &item.Revision, &item.TestedRevision, &item.BreakGlass, &item.EnforcedAt, &item.UpdatedBy, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func scanSSOProbeReceipt(row rowScanner) (domain.SSOProbeReceipt, error) {
	var item domain.SSOProbeReceipt
	var metadataSHA, errorCode, errorMessage *string
	err := row.Scan(&item.ID, &item.ConfigRevision, &item.Protocol, &item.TargetURL, &item.State, &item.Attempt, &metadataSHA, &errorCode, &errorMessage, &item.RequestedBy, &item.CreatedAt, &item.StartedAt, &item.FinishedAt)
	if metadataSHA != nil {
		item.MetadataSHA256 = *metadataSHA
	}
	if errorCode != nil {
		item.ErrorCode = *errorCode
	}
	if errorMessage != nil {
		item.ErrorMessage = *errorMessage
	}
	return item, err
}

func scanSSODomain(row rowScanner) (domain.SSODomain, error) {
	var item domain.SSODomain
	err := row.Scan(&item.ID, &item.Domain, &item.ChallengeToken, &item.State, &item.Revision, &item.CreatedBy, &item.CreatedAt, &item.VerifiedAt)
	return item, err
}

func scanSSORoleMapping(row rowScanner) (domain.SSORoleMapping, error) {
	var item domain.SSORoleMapping
	err := row.Scan(&item.ID, &item.GroupValue, &item.Role, &item.RepositoryScope, &item.Revision, &item.CreatedBy, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func truncateSSOError(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 500 {
		return value[:500]
	}
	return value
}
