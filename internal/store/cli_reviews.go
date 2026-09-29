package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) SubmitCLIReview(ctx context.Context, principal domain.APIKeyPrincipal, tenantSlug, idempotencyKey string, input domain.CLIReviewInput) (domain.CLIReviewSubmission, error) {
	input, valid := domain.NormalizeCLIReviewInput(input, idempotencyKey)
	if !valid || principal.TenantID == uuid.Nil || principal.TenantSlug != tenantSlug || !principal.HasScope(domain.APIKeyScopeReviewsCreate) || !principal.AllowsRepository(input.Repository) {
		return domain.CLIReviewSubmission{}, ErrInvalidCLIReview
	}
	var installation domain.Installation
	err := s.pool.QueryRow(ctx, `
		SELECT id, tenant_id, provider, external_id, repository_scope, api_base_url, credential_ref, active
		FROM provider_installations
		WHERE id = $1 AND tenant_id = $2 AND active = TRUE
		  AND verification_state IN ('legacy','verified')`, input.InstallationID, principal.TenantID).
		Scan(&installation.ID, &installation.TenantID, &installation.Provider, &installation.ExternalID, &installation.RepositoryScope, &installation.APIBaseURL, &installation.CredentialRef, &installation.Active)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.CLIReviewSubmission{}, ErrNotFound
	}
	if err != nil {
		return domain.CLIReviewSubmission{}, fmt.Errorf("resolve CLI review installation: %w", err)
	}
	if !repositoryMatchesScope(installation.RepositoryScope, input.Repository) {
		return domain.CLIReviewSubmission{}, ErrForbidden
	}
	setupComplete, err := workspaceSetupAllowsReview(ctx, s.pool, installation.TenantID)
	if err != nil {
		return domain.CLIReviewSubmission{}, err
	}
	if !setupComplete {
		return domain.CLIReviewSubmission{}, ErrWorkspaceSetupIncomplete
	}
	cloneURL, err := trustedCloneURL(installation.Provider, installation.APIBaseURL, input.Repository)
	if err != nil {
		return domain.CLIReviewSubmission{}, ErrInvalidCLIReview
	}
	deliveryID := cliDeliveryID(principal, idempotencyKey)
	payload, err := json.Marshal(map[string]any{
		"source": "cli", "key_id": principal.KeyID.String(), "repository": input.Repository,
		"review_number": input.ReviewNumber, "base_sha": input.BaseSHA, "head_sha": input.HeadSHA,
		"mode": input.Mode,
	})
	if err != nil {
		return domain.CLIReviewSubmission{}, fmt.Errorf("encode CLI review admission: %w", err)
	}
	job, duplicate, err := s.Enqueue(ctx, domain.InboundEvent{
		Provider: installation.Provider, APIBaseURL: installation.APIBaseURL,
		DeliveryID: deliveryID, EventName: "cli.review.requested", InstallationExternalID: installation.ExternalID,
		Repository: input.Repository, CloneURL: cloneURL, ReviewNumber: input.ReviewNumber,
		BaseRef: input.BaseRef, BaseSHA: input.BaseSHA, HeadRef: input.HeadRef, HeadSHA: input.HeadSHA,
		Payload: payload, ReceivedAt: time.Now().UTC(), TriggerKind: "cli", ActorKind: "user",
		ActorSubject: principal.Subject, ReviewMode: input.Mode,
	})
	if err != nil {
		return domain.CLIReviewSubmission{}, err
	}
	run, replayed, coalesced, err := s.resolveCLIReviewSubmission(ctx, principal.TenantID, installation.ID, installation.Provider, deliveryID, input, job, duplicate)
	if err != nil {
		return domain.CLIReviewSubmission{}, err
	}
	return domain.CLIReviewSubmission{Run: run, Replayed: replayed, Coalesced: coalesced}, nil
}

func (s *PostgresStore) ListCLIReviewRuns(ctx context.Context, actor, tenantSlug string, limit int) ([]domain.CLIReviewRun, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrInvalidCLIReview
	}
	tenantID, _, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, cliReviewRunSelect+`
		WHERE request.tenant_id = $1 AND r.trigger_kind = 'cli'
		ORDER BY r.created_at DESC, r.id DESC
		LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("list CLI review runs: %w", err)
	}
	defer rows.Close()
	runs := make([]domain.CLIReviewRun, 0)
	for rows.Next() {
		run, err := scanCLIReviewRun(rows)
		if err != nil {
			return nil, fmt.Errorf("scan CLI review run: %w", err)
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate CLI review runs: %w", err)
	}
	return runs, nil
}

func (s *PostgresStore) GetCLIReview(ctx context.Context, principal domain.APIKeyPrincipal, runID uuid.UUID) (domain.CLIReviewRun, error) {
	if principal.TenantID == uuid.Nil || (!principal.HasScope(domain.APIKeyScopeReviewsRead) && !principal.HasScope(domain.APIKeyScopeRunsCancel)) {
		return domain.CLIReviewRun{}, ErrForbidden
	}
	run, err := scanCLIReviewRun(s.pool.QueryRow(ctx, cliReviewRunSelect+`
		WHERE request.tenant_id = $1 AND r.id = $2 AND r.trigger_kind = 'cli'`, principal.TenantID, runID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.CLIReviewRun{}, ErrNotFound
	}
	if err != nil {
		return domain.CLIReviewRun{}, fmt.Errorf("get CLI review: %w", err)
	}
	if !principal.AllowsRepository(run.Repository) {
		return domain.CLIReviewRun{}, ErrNotFound
	}
	return run, nil
}

func (s *PostgresStore) GetCLIReviewEvidence(ctx context.Context, principal domain.APIKeyPrincipal, runID uuid.UUID) (domain.ReviewEvidence, error) {
	run, err := s.GetCLIReview(ctx, principal, runID)
	if err != nil {
		return domain.ReviewEvidence{}, err
	}
	return s.getReviewEvidenceForRun(ctx, run.ReviewRunSummary)
}

func (s *PostgresStore) RequestCLIReviewCancellation(ctx context.Context, principal domain.APIKeyPrincipal, runID uuid.UUID, expectedRevision int) (domain.ReviewRun, error) {
	if expectedRevision < 1 {
		return domain.ReviewRun{}, ErrRevisionConflict
	}
	if !principal.HasScope(domain.APIKeyScopeRunsCancel) {
		return domain.ReviewRun{}, ErrForbidden
	}
	if _, err := s.GetCLIReview(ctx, principal, runID); err != nil {
		return domain.ReviewRun{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.ReviewRun{}, fmt.Errorf("begin CLI review cancellation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	run, err := scanReviewRun(tx.QueryRow(ctx, `
		SELECT r.id, r.request_id, r.legacy_job_id, r.revision, r.state, r.trigger_kind, r.review_mode, r.head_sha, r.base_sha, r.cancel_requested_at, r.superseded_by, r.failure_code, r.failure_message, r.rule_snapshot_id, r.created_at, r.started_at, r.finished_at
		FROM review_runs r
		JOIN review_requests request ON request.id = r.request_id
		WHERE r.id = $1 AND request.tenant_id = $2 AND r.trigger_kind = 'cli'
		FOR UPDATE`, runID, principal.TenantID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ReviewRun{}, ErrNotFound
	}
	if err != nil {
		return domain.ReviewRun{}, fmt.Errorf("load CLI review for cancellation: %w", err)
	}
	if run.Revision != expectedRevision {
		return domain.ReviewRun{}, ErrRevisionConflict
	}
	if run.State.Terminal() || run.CancelRequestedAt != nil {
		return domain.ReviewRun{}, ErrConflict
	}
	if err := cancelRun(ctx, tx, &run, "user", principal.Subject, map[string]any{"source": "cli_api", "key_id": principal.KeyID.String()}); err != nil {
		return domain.ReviewRun{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ReviewRun{}, fmt.Errorf("commit CLI review cancellation: %w", err)
	}
	return run, nil
}

const cliReviewRunSelect = `
	SELECT r.id, r.request_id, r.legacy_job_id, r.revision, r.state, r.trigger_kind, r.review_mode,
	       r.head_sha, r.base_sha, r.cancel_requested_at, r.superseded_by, r.failure_code,
	       r.failure_message, r.rule_snapshot_id, r.created_at, r.started_at, r.finished_at,
	       request.provider, request.api_base_url, request.repository, request.review_number,
	       COALESCE((
		   SELECT event.actor_subject FROM review_run_events event
		   WHERE event.run_id = r.id AND event.event_type = 'run.acknowledged'
		   ORDER BY event.revision LIMIT 1
	       ), '')
	FROM review_runs r
	JOIN review_requests request ON request.id = r.request_id
`

func scanCLIReviewRun(row rowScanner) (domain.CLIReviewRun, error) {
	var result domain.CLIReviewRun
	var failureCode, failureMessage *string
	err := row.Scan(
		&result.ID, &result.RequestID, &result.LegacyJobID, &result.Revision, &result.State,
		&result.TriggerKind, &result.ReviewMode, &result.HeadSHA, &result.BaseSHA,
		&result.CancelRequestedAt, &result.SupersededBy, &failureCode, &failureMessage,
		&result.RuleSnapshotID, &result.CreatedAt, &result.StartedAt, &result.FinishedAt,
		&result.Provider, &result.APIBaseURL, &result.Repository, &result.ReviewNumber,
		&result.CallerSubject,
	)
	if failureCode != nil {
		result.FailureCode = *failureCode
	}
	if failureMessage != nil {
		result.FailureMessage = *failureMessage
	}
	return result, err
}

func (s *PostgresStore) resolveCLIReviewSubmission(ctx context.Context, tenantID, installationID uuid.UUID, provider domain.Provider, deliveryID string, input domain.CLIReviewInput, job domain.ReviewJob, duplicate bool) (domain.CLIReviewRun, bool, bool, error) {
	if job.ID == uuid.Nil && job.State == domain.JobCancelled && job.ErrorMessage == "reviews are disabled by repository policy" {
		return domain.CLIReviewRun{}, false, false, ErrForbidden
	}
	if job.ID != uuid.Nil {
		run, err := scanCLIReviewRun(s.pool.QueryRow(ctx, cliReviewRunSelect+` WHERE request.tenant_id = $1 AND r.legacy_job_id = $2`, tenantID, job.ID))
		if err == nil {
			return run, false, false, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return domain.CLIReviewRun{}, false, false, fmt.Errorf("resolve admitted CLI review: %w", err)
		}
	}
	if duplicate {
		run, err := scanCLIReviewRun(s.pool.QueryRow(ctx, cliReviewRunSelect+`
			JOIN review_jobs job ON job.id = r.legacy_job_id
			JOIN webhook_deliveries delivery ON delivery.id = job.delivery_id
			WHERE request.tenant_id = $1 AND delivery.provider = $2 AND delivery.delivery_id = $3`, tenantID, provider, deliveryID))
		if err == nil {
			return run, true, false, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return domain.CLIReviewRun{}, false, false, fmt.Errorf("resolve replayed CLI review: %w", err)
		}
	}
	run, err := scanCLIReviewRun(s.pool.QueryRow(ctx, cliReviewRunSelect+`
		WHERE request.tenant_id = $1 AND request.installation_id = $2
		  AND request.repository = $3 AND request.review_number = $4
		  AND r.head_sha = $5
		  AND r.state IN ('acknowledged', 'admitted', 'preparing', 'analyzing', 'normalizing', 'publishing')
		ORDER BY r.created_at DESC LIMIT 1`, tenantID, installationID, input.Repository, input.ReviewNumber, input.HeadSHA))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.CLIReviewRun{}, false, false, ErrNotFound
	}
	if err != nil {
		return domain.CLIReviewRun{}, false, false, fmt.Errorf("resolve coalesced CLI review: %w", err)
	}
	return run, false, true, nil
}

func cliDeliveryID(principal domain.APIKeyPrincipal, idempotencyKey string) string {
	digest := sha256.Sum256([]byte(principal.TenantID.String() + "\x00" + principal.KeyID.String() + "\x00" + strings.TrimSpace(idempotencyKey)))
	return "cli:" + hex.EncodeToString(digest[:])
}

func repositoryMatchesScope(pattern, repository string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	matched, err := path.Match(pattern, repository)
	return err == nil && matched
}

func trustedCloneURL(provider domain.Provider, apiBaseURL, repository string) (string, error) {
	parsed, err := url.Parse(strings.TrimSuffix(strings.TrimSpace(apiBaseURL), "/"))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("provider API base URL is not trusted")
	}
	basePath := strings.TrimSuffix(parsed.Path, "/")
	switch provider {
	case domain.ProviderGitHub:
		if parsed.Host == "api.github.com" && basePath == "" {
			parsed.Host = "github.com"
		} else if strings.HasSuffix(basePath, "/api/v3") {
			basePath = strings.TrimSuffix(basePath, "/api/v3")
		} else {
			return "", fmt.Errorf("unsupported GitHub API base URL")
		}
	case domain.ProviderGitLab:
		if !strings.HasSuffix(basePath, "/api/v4") {
			return "", fmt.Errorf("unsupported GitLab API base URL")
		}
		basePath = strings.TrimSuffix(basePath, "/api/v4")
	default:
		return "", fmt.Errorf("unsupported provider")
	}
	segments := strings.Split(repository, "/")
	for index := range segments {
		segments[index] = url.PathEscape(segments[index])
	}
	parsed.Path = strings.TrimSuffix(basePath, "/") + "/" + strings.Join(segments, "/") + ".git"
	parsed.RawPath = ""
	return parsed.String(), nil
}
