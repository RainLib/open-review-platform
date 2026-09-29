package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const maxAgentCredentialIssuancesPerJob = 6

var ErrAgentCredentialIssuanceLimit = errors.New("agent task credential issuance limit reached")

const agentCredentialGrantQuery = `
		SELECT t.tenant_id,t.installation_id,t.provider,t.api_base_url,t.repository,i.external_id,i.repository_scope,p.summary,p.plan_sha256
		FROM agent_task_attempts a
		JOIN agent_tasks t ON t.id=a.task_id
		JOIN agent_task_plans p ON p.id=a.plan_id AND p.task_id=t.id
		JOIN provider_installations i ON i.id=t.installation_id AND i.tenant_id=t.tenant_id
		WHERE a.id=$1 AND a.adapter_job_id=$2 AND a.adapter_started_at IS NOT NULL
		  AND a.state='running' AND a.locked_until>now() AND a.deadline_at>now()
		  AND a.task_revision+1=t.revision AND t.state='executing' AND t.source_state='ready'
		  AND p.state='approved' AND p.revision=a.plan_revision AND p.approved_at IS NOT NULL
		  AND i.active=TRUE AND i.verification_state='verified'
		  AND i.provider=t.provider AND i.api_base_url=t.api_base_url`

// AgentTaskCredentialGrant is an authorization decision, not a credential.
// A separately deployed broker must still map the installation to its own
// coding identity and mint a token for exactly this repository. The review
// installation's credential reference is deliberately not returned.
type AgentTaskCredentialGrant struct {
	TenantID                     uuid.UUID
	InstallationID               uuid.UUID
	Provider                     domain.Provider
	APIBaseURL                   string
	Repository                   string
	ReviewInstallationExternalID string
}

// LoadAgentTaskCredentialGrant admits a provider write credential only for an
// already-started, currently leased adapter job. A signed submission alone is
// insufficient: cancellation, expired leases, supersession, changed install
// scope, or a different adapter job all fail closed at issuance time.
func (s *PostgresStore) LoadAgentTaskCredentialGrant(ctx context.Context, attemptID uuid.UUID, adapterJobID string) (AgentTaskCredentialGrant, error) {
	adapterJobID = strings.TrimSpace(adapterJobID)
	if attemptID == uuid.Nil || !validAgentAdapterJobID(adapterJobID) {
		return AgentTaskCredentialGrant{}, ErrInvalidAgentTask
	}
	return scanAgentTaskCredentialGrant(s.pool.QueryRow(ctx, agentCredentialGrantQuery, attemptID, adapterJobID))
}

func scanAgentTaskCredentialGrant(row pgx.Row) (AgentTaskCredentialGrant, error) {
	var grant AgentTaskCredentialGrant
	var repositoryScope, planSummary, planSHA string
	err := row.Scan(
		&grant.TenantID, &grant.InstallationID, &grant.Provider, &grant.APIBaseURL, &grant.Repository,
		&grant.ReviewInstallationExternalID, &repositoryScope, &planSummary, &planSHA,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return AgentTaskCredentialGrant{}, ErrAgentTaskClaimLost
	}
	if err != nil {
		return AgentTaskCredentialGrant{}, fmt.Errorf("load agent task credential grant: %w", err)
	}
	digest := sha256.Sum256([]byte(planSummary))
	if !repositoryScopeAllows(repositoryScope, grant.Repository) || planSHA != hex.EncodeToString(digest[:]) {
		return AgentTaskCredentialGrant{}, ErrAgentTaskClaimLost
	}
	return grant, nil
}

// ReserveAgentTaskCredentialIssuance serializes requests on the running
// attempt row. Rechecking the full grant under that lock ensures a stale job,
// changed installation or cancelled task cannot consume an issuance slot.
// A reservation is intentionally not retried or refunded after an uncertain
// upstream response: a remote token may already exist.
func (s *PostgresStore) ReserveAgentTaskCredentialIssuance(ctx context.Context, attemptID uuid.UUID, adapterJobID string, expected AgentTaskCredentialGrant) (uuid.UUID, error) {
	adapterJobID = strings.TrimSpace(adapterJobID)
	if attemptID == uuid.Nil || !validAgentAdapterJobID(adapterJobID) {
		return uuid.Nil, ErrInvalidAgentTask
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("begin agent credential issuance: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	grant, err := scanAgentTaskCredentialGrant(tx.QueryRow(ctx, agentCredentialGrantQuery+` FOR UPDATE OF a,t,p,i`, attemptID, adapterJobID))
	if err != nil {
		return uuid.Nil, err
	}
	if grant != expected {
		return uuid.Nil, ErrAgentTaskClaimLost
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM agent_task_credential_issuances WHERE attempt_id=$1 AND adapter_job_id=$2`, attemptID, adapterJobID).Scan(&count); err != nil {
		return uuid.Nil, fmt.Errorf("count agent credential issuances: %w", err)
	}
	if count >= maxAgentCredentialIssuancesPerJob {
		return uuid.Nil, ErrAgentCredentialIssuanceLimit
	}
	var issuanceID uuid.UUID
	if err := tx.QueryRow(ctx, `INSERT INTO agent_task_credential_issuances(attempt_id,adapter_job_id,ordinal,tenant_id,review_installation_id,review_installation_external_id,provider,api_base_url,repository) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`, attemptID, adapterJobID, count+1, grant.TenantID, grant.InstallationID, grant.ReviewInstallationExternalID, grant.Provider, grant.APIBaseURL, grant.Repository).Scan(&issuanceID); err != nil {
		return uuid.Nil, fmt.Errorf("reserve agent credential issuance: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("commit agent credential issuance: %w", err)
	}
	return issuanceID, nil
}

// CompleteAgentTaskCredentialIssuance records the outcome without persisting
// the token. A reserved record survives process failure for investigation.
func (s *PostgresStore) CompleteAgentTaskCredentialIssuance(ctx context.Context, issuanceID uuid.UUID, state string) error {
	if issuanceID == uuid.Nil || (state != "issued" && state != "failed" && state != "withheld") {
		return ErrInvalidAgentTask
	}
	if state != "issued" {
		return s.updateAgentTaskCredentialIssuance(ctx, issuanceID, state)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin agent credential completion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var attemptID, tenantID, installationID uuid.UUID
	var adapterJobID, reviewExternalID, provider, apiBaseURL, repository string
	err = tx.QueryRow(ctx, `SELECT attempt_id,adapter_job_id,tenant_id,review_installation_id,review_installation_external_id,provider,api_base_url,repository FROM agent_task_credential_issuances WHERE id=$1 AND state='reserved'`, issuanceID).Scan(&attemptID, &adapterJobID, &tenantID, &installationID, &reviewExternalID, &provider, &apiBaseURL, &repository)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAgentTaskClaimLost
	}
	if err != nil {
		return fmt.Errorf("load agent credential issuance: %w", err)
	}
	grant, err := scanAgentTaskCredentialGrant(tx.QueryRow(ctx, agentCredentialGrantQuery+` FOR UPDATE OF a,t,p,i`, attemptID, adapterJobID))
	if err != nil {
		return err
	}
	if grant.TenantID != tenantID || grant.InstallationID != installationID || grant.ReviewInstallationExternalID != reviewExternalID || string(grant.Provider) != provider || grant.APIBaseURL != apiBaseURL || grant.Repository != repository {
		return ErrAgentTaskClaimLost
	}
	command, err := tx.Exec(ctx, `UPDATE agent_task_credential_issuances SET state='issued',finished_at=now() WHERE id=$1 AND state='reserved'`, issuanceID)
	if err != nil {
		return fmt.Errorf("complete agent credential issuance: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrAgentTaskClaimLost
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit agent credential completion: %w", err)
	}
	return nil
}

func (s *PostgresStore) updateAgentTaskCredentialIssuance(ctx context.Context, issuanceID uuid.UUID, state string) error {
	command, err := s.pool.Exec(ctx, `UPDATE agent_task_credential_issuances SET state=$2,finished_at=now() WHERE id=$1 AND state='reserved'`, issuanceID, state)
	if err != nil {
		return fmt.Errorf("complete agent credential issuance: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrAgentTaskClaimLost
	}
	return nil
}
