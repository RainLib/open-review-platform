package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/rules"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) CreateRuleTestRun(ctx context.Context, actor, tenantSlug string, ruleSetID uuid.UUID, version int, input domain.RuleTestRunInput) (domain.RuleTestRun, error) {
	if ruleSetID == uuid.Nil || version < 1 || input.SourceRunID == uuid.Nil || input.Precedence < 0 || input.Precedence > 10_000 {
		return domain.RuleTestRun{}, ErrInvalidRuleImpact
	}
	tenantID, role, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.RuleTestRun{}, err
	}
	if !canManageRules(role) {
		return domain.RuleTestRun{}, ErrForbidden
	}

	var sourceJobID uuid.UUID
	var repository, targetBranch, apiBaseURL string
	var provider domain.Provider
	err = s.pool.QueryRow(ctx, `
		SELECT job.id, job.provider, job.api_base_url, job.repository, job.base_ref
		FROM review_runs run
		JOIN review_requests request ON request.id = run.request_id
		JOIN review_jobs job ON job.id = run.legacy_job_id
		WHERE run.id = $1
		  AND request.tenant_id = $2
		  AND run.state IN ('completed', 'needs_attention')
		  AND job.base_ref <> ''
		  AND job.base_sha <> ''
		  AND job.head_sha <> ''`, input.SourceRunID, tenantID).Scan(&sourceJobID, &provider, &apiBaseURL, &repository, &targetBranch)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RuleTestRun{}, ErrNotFound
	}
	if err != nil {
		return domain.RuleTestRun{}, fmt.Errorf("load rule test source run: %w", err)
	}

	var ruleVersionID uuid.UUID
	var ruleSetName, contentSHA string
	var rawRules []byte
	err = s.pool.QueryRow(ctx, `
		SELECT version.id, rule_set.name, version.rules, version.content_sha256
		FROM rule_versions version
		JOIN rule_sets rule_set ON rule_set.id = version.rule_set_id
		WHERE rule_set.tenant_id = $1 AND rule_set.id = $2 AND version.version = $3`, tenantID, ruleSetID, version).
		Scan(&ruleVersionID, &ruleSetName, &rawRules, &contentSHA)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RuleTestRun{}, ErrNotFound
	}
	if err != nil {
		return domain.RuleTestRun{}, fmt.Errorf("load rule test candidate: %w", err)
	}
	var candidateRules []rules.Rule
	if err := json.Unmarshal(rawRules, &candidateRules); err != nil {
		return domain.RuleTestRun{}, fmt.Errorf("decode rule test candidate: %w", err)
	}

	scope := domain.ReviewConfigScope{Kind: domain.ReviewConfigRepositoryScope, Ref: repository, Provider: provider, APIBaseURL: apiBaseURL}
	sources, _, err := s.activeRuleSourcesForPreview(ctx, tenantID, scope, targetBranch)
	if err != nil {
		return domain.RuleTestRun{}, err
	}
	versionRows, err := s.pool.Query(ctx, `SELECT id FROM rule_versions WHERE rule_set_id = $1`, ruleSetID)
	if err != nil {
		return domain.RuleTestRun{}, fmt.Errorf("list candidate rule-set versions: %w", err)
	}
	replaced := map[string]bool{}
	for versionRows.Next() {
		var id uuid.UUID
		if err := versionRows.Scan(&id); err != nil {
			versionRows.Close()
			return domain.RuleTestRun{}, fmt.Errorf("scan candidate rule-set version: %w", err)
		}
		replaced[id.String()] = true
	}
	if err := versionRows.Err(); err != nil {
		versionRows.Close()
		return domain.RuleTestRun{}, fmt.Errorf("iterate candidate rule-set versions: %w", err)
	}
	versionRows.Close()
	filtered := sources[:0]
	for _, source := range sources {
		if !replaced[source.VersionID] {
			filtered = append(filtered, source)
		}
	}
	sources = append(filtered, rules.Source{VersionID: ruleVersionID.String(), Precedence: input.Precedence, Rules: candidateRules})
	compiled, err := rules.Compile(sources)
	if err != nil {
		return domain.RuleTestRun{}, fmt.Errorf("%w: candidate composition: %v", ErrInvalidRuleImpact, err)
	}
	appliedExceptions, exceptionRecords, err := resolveApplicableRuleExceptions(ctx, s.pool, tenantID, scope, targetBranch, ruleSourceVersions(sources))
	if err != nil {
		return domain.RuleTestRun{}, err
	}
	compiled, err = rules.ApplyExceptions(compiled, appliedExceptions)
	if err != nil {
		return domain.RuleTestRun{}, fmt.Errorf("%w: candidate exceptions: %v", ErrInvalidRuleImpact, err)
	}
	exceptionRecords = appliedExceptionRecords(compiled, exceptionRecords)
	if _, err := rules.OCRRuleFileForSnapshot(compiled.Snapshot); err != nil {
		return domain.RuleTestRun{}, fmt.Errorf("%w: OCR adapter: %v", ErrInvalidRuleImpact, err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.RuleTestRun{}, fmt.Errorf("begin rule test run: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var snapshotID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO rule_snapshots (tenant_id, sha256, compiler_version, engine, canonical_payload)
		VALUES ($1, $2, 'rules-v2', 'ocr', $3::jsonb)
		ON CONFLICT (tenant_id, sha256) DO UPDATE SET sha256 = EXCLUDED.sha256
		RETURNING id`, tenantID, compiled.SHA256, string(compiled.Canonical)).Scan(&snapshotID)
	if err != nil {
		return domain.RuleTestRun{}, fmt.Errorf("store rule test snapshot: %w", err)
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].VersionID < sources[j].VersionID })
	for _, source := range sources {
		versionID, parseErr := uuid.Parse(source.VersionID)
		if parseErr != nil {
			return domain.RuleTestRun{}, fmt.Errorf("parse rule test snapshot source: %w", parseErr)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO rule_snapshot_sources (snapshot_id, rule_version_id, precedence)
			VALUES ($1, $2, $3)
			ON CONFLICT (snapshot_id, rule_version_id) DO NOTHING`, snapshotID, versionID, source.Precedence); err != nil {
			return domain.RuleTestRun{}, fmt.Errorf("store rule test snapshot source: %w", err)
		}
	}
	for _, exception := range exceptionRecords {
		if _, err := tx.Exec(ctx, `
			INSERT INTO rule_snapshot_exceptions (snapshot_id, exception_id, rule_key, rule_version_id, expires_at)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (snapshot_id, exception_id) DO NOTHING`, snapshotID, exception.ID,
			exception.RuleKey, exception.RuleVersionID, exception.ExpiresAt); err != nil {
			return domain.RuleTestRun{}, fmt.Errorf("store rule test snapshot exception: %w", err)
		}
	}
	var testRunID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO rule_test_runs (tenant_id, rule_version_id, source_run_id, source_job_id, snapshot_id, requested_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`, tenantID, ruleVersionID, input.SourceRunID, sourceJobID, snapshotID, actor).Scan(&testRunID)
	if err != nil {
		return domain.RuleTestRun{}, fmt.Errorf("create rule test run: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_messages (aggregate_type, aggregate_id, topic, dedupe_key, payload)
		VALUES ('rule_test_run', $1, 'rule.test.requested', $2, jsonb_build_object('test_run_id', $1::text))`,
		testRunID, "rule-test:"+testRunID.String()+":requested"); err != nil {
		return domain.RuleTestRun{}, fmt.Errorf("queue rule test run: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata)
		VALUES ($1, $2, 'rule_test_run.created', $3, jsonb_build_object(
			'rule_version_id', $4::text, 'source_run_id', $5::text,
			'snapshot_id', $6::text, 'snapshot_sha256', $7::text,
			'candidate_content_sha256', $8::text, 'candidate_precedence', $9::int,
			'publication_allowed', false))`,
		tenantID, actor, testRunID.String(), ruleVersionID, input.SourceRunID, snapshotID, compiled.SHA256, contentSHA, input.Precedence); err != nil {
		return domain.RuleTestRun{}, fmt.Errorf("audit rule test run: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.RuleTestRun{}, fmt.Errorf("commit rule test run: %w", err)
	}
	return s.ruleTestRunByID(ctx, tenantID, testRunID, ruleSetName, version)
}

func (s *PostgresStore) ListRuleTestRuns(ctx context.Context, actor, tenantSlug string, limit int) ([]domain.RuleTestRun, error) {
	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("rule test run limit must be from 1 to 100")
	}
	tenantID, _, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT test.id, test.tenant_id, rule_set.id, rule_set.name, version.id, version.version,
		       test.source_run_id, test.snapshot_id, snapshot.sha256, test.state, test.requested_by,
		       job.provider, job.api_base_url, job.repository, job.review_number, job.base_sha, job.head_sha,
		       test.engine_version, test.attempts, test.selected_path_count, test.deferred_path_count,
		       test.finding_count, test.duration_ms, test.error_message,
		       test.created_at, test.started_at, test.finished_at
		FROM rule_test_runs test
		JOIN rule_versions version ON version.id = test.rule_version_id
		JOIN rule_sets rule_set ON rule_set.id = version.rule_set_id
		JOIN rule_snapshots snapshot ON snapshot.id = test.snapshot_id
		JOIN review_jobs job ON job.id = test.source_job_id
		WHERE test.tenant_id = $1
		ORDER BY test.created_at DESC, test.id DESC
		LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("list rule test runs: %w", err)
	}
	items := make([]domain.RuleTestRun, 0)
	for rows.Next() {
		item, err := scanRuleTestRun(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan rule test run: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate rule test runs: %w", err)
	}
	rows.Close()
	for index := range items {
		items[index].Findings, err = s.ruleTestFindings(ctx, items[index].ID)
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}

func (s *PostgresStore) ruleTestRunByID(ctx context.Context, tenantID, testRunID uuid.UUID, _ string, _ int) (domain.RuleTestRun, error) {
	item, err := scanRuleTestRun(s.pool.QueryRow(ctx, `
		SELECT test.id, test.tenant_id, rule_set.id, rule_set.name, version.id, version.version,
		       test.source_run_id, test.snapshot_id, snapshot.sha256, test.state, test.requested_by,
		       job.provider, job.api_base_url, job.repository, job.review_number, job.base_sha, job.head_sha,
		       test.engine_version, test.attempts, test.selected_path_count, test.deferred_path_count,
		       test.finding_count, test.duration_ms, test.error_message,
		       test.created_at, test.started_at, test.finished_at
		FROM rule_test_runs test
		JOIN rule_versions version ON version.id = test.rule_version_id
		JOIN rule_sets rule_set ON rule_set.id = version.rule_set_id
		JOIN rule_snapshots snapshot ON snapshot.id = test.snapshot_id
		JOIN review_jobs job ON job.id = test.source_job_id
		WHERE test.id = $1 AND test.tenant_id = $2`, testRunID, tenantID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RuleTestRun{}, ErrNotFound
	}
	if err != nil {
		return domain.RuleTestRun{}, fmt.Errorf("load rule test run: %w", err)
	}
	item.Findings, err = s.ruleTestFindings(ctx, item.ID)
	return item, err
}

func (s *PostgresStore) ClaimRuleTestRun(ctx context.Context, workerID string, testRunID *uuid.UUID) (domain.RuleTestExecution, error) {
	if workerID == "" {
		return domain.RuleTestExecution{}, ErrJobClaimLost
	}
	var id any
	if testRunID != nil {
		id = *testRunID
	}
	var claimedID, sourceJobID, snapshotID uuid.UUID
	var attempt int
	err := s.pool.QueryRow(ctx, `
		WITH candidate AS (
			SELECT id FROM rule_test_runs
			WHERE ((state = 'queued' AND available_at <= now())
			    OR (state = 'running' AND locked_until <= now()))
			  AND ($2::uuid IS NULL OR id = $2::uuid)
			ORDER BY created_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE rule_test_runs test
		SET state = 'running', attempts = attempts + 1, locked_by = $1,
		    locked_until = now() + interval '10 minutes', started_at = COALESCE(started_at, now())
		FROM candidate
		WHERE test.id = candidate.id
		RETURNING test.id, test.source_job_id, test.snapshot_id, test.attempts`, workerID, id).
		Scan(&claimedID, &sourceJobID, &snapshotID, &attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RuleTestExecution{}, ErrNoQueuedJob
	}
	if err != nil {
		return domain.RuleTestExecution{}, fmt.Errorf("claim rule test run: %w", err)
	}
	job, err := scanJob(s.pool.QueryRow(ctx, `
		SELECT job.id, job.tenant_id, tenant.slug, job.installation_id, installation.external_id, installation.credential_ref,
		       job.delivery_id, job.provider, job.api_base_url, job.repository, job.clone_url,
		       job.review_number, job.base_ref, job.base_sha, job.head_ref, job.head_sha,
		       job.state, job.attempts, job.locked_by, job.locked_until, job.error_message,
		       job.created_at, job.started_at, job.finished_at
		FROM review_jobs job
		JOIN provider_installations installation ON installation.id = job.installation_id
		JOIN tenants tenant ON tenant.id = job.tenant_id
		WHERE job.id = $1`, sourceJobID))
	if err != nil {
		return domain.RuleTestExecution{}, fmt.Errorf("load rule test source job: %w", err)
	}
	var snapshot domain.RuleSnapshot
	var payload []byte
	err = s.pool.QueryRow(ctx, `
		SELECT id, sha256, compiler_version, engine, canonical_payload, created_at
		FROM rule_snapshots WHERE id = $1`, snapshotID).
		Scan(&snapshot.ID, &snapshot.SHA256, &snapshot.CompilerVersion, &snapshot.Engine, &payload, &snapshot.CreatedAt)
	if err != nil {
		return domain.RuleTestExecution{}, fmt.Errorf("load rule test snapshot: %w", err)
	}
	snapshot.CanonicalPayload = append(json.RawMessage(nil), payload...)
	return domain.RuleTestExecution{Run: domain.RuleTestRun{ID: claimedID, State: "running", Attempts: attempt}, Job: job, Snapshot: snapshot}, nil
}

// Every mutation is fenced by the claim attempt as well as the worker identity.
// A restarted process can reuse a worker ID without accepting its predecessor's
// late heartbeat, result, or error after the expired lease has been reclaimed.
func (s *PostgresStore) CompleteRuleTestRun(ctx context.Context, testRunID uuid.UUID, workerID string, attempt int, completion domain.RuleTestCompletion) error {
	if testRunID == uuid.Nil || workerID == "" || attempt < 1 || completion.SelectedPathCount < 0 || completion.DeferredPathCount < 0 || completion.DurationMS < 0 {
		return ErrJobClaimLost
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin rule test completion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `
		UPDATE rule_test_runs
		SET state = 'completed', engine_version = $3, selected_path_count = $4,
		    deferred_path_count = $5, finding_count = $6, duration_ms = $7,
		    locked_by = NULL, locked_until = NULL, finished_at = now(), error_message = ''
		WHERE id = $1 AND state = 'running' AND locked_by = $2
		  AND attempts = $8 AND locked_until > clock_timestamp()`, testRunID, workerID,
		completion.EngineVersion, completion.SelectedPathCount, completion.DeferredPathCount,
		len(completion.Findings), completion.DurationMS, attempt)
	if err != nil {
		return fmt.Errorf("complete rule test run: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrJobClaimLost
	}
	for _, finding := range completion.Findings {
		if _, err := tx.Exec(ctx, `
			INSERT INTO rule_test_findings (test_run_id, path, start_line, end_line, severity, category, body, suggestion, fingerprint)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (test_run_id, fingerprint) DO NOTHING`, testRunID, finding.Path, finding.StartLine,
			finding.EndLine, finding.Severity, finding.Category, finding.Body, finding.Suggestion, findingFingerprint(finding)); err != nil {
			return fmt.Errorf("save rule test finding: %w", err)
		}
	}
	if err := completeShadowRolloutComparison(ctx, tx, testRunID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// completeShadowRolloutComparison persists only replay evidence. The matching
// key is the same normalized finding fingerprint used by the source review and
// Rule Lab, so it can distinguish candidate additions/removals without writing
// a provider comment, check, reaction, or merge decision.
func completeShadowRolloutComparison(ctx context.Context, tx pgx.Tx, testRunID uuid.UUID) error {
	var rolloutID *uuid.UUID
	var sourceRunID, baselineJobID, baselineSnapshotID, candidateSnapshotID uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT test.rollout_id,test.source_run_id,run.legacy_job_id,run.rule_snapshot_id,test.snapshot_id
		FROM rule_test_runs test
		JOIN review_runs run ON run.id=test.source_run_id
		WHERE test.id=$1`, testRunID).Scan(&rolloutID, &sourceRunID, &baselineJobID, &baselineSnapshotID, &candidateSnapshotID)
	if err != nil {
		return fmt.Errorf("load shadow rollout comparison source: %w", err)
	}
	if rolloutID == nil {
		return nil
	}
	var baselineCount, candidateCount, addedCount, removedCount, matchedCount int
	if err := tx.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM review_findings WHERE job_id=$1),
			(SELECT COUNT(*) FROM rule_test_findings WHERE test_run_id=$2),
			(SELECT COUNT(*) FROM rule_test_findings candidate WHERE candidate.test_run_id=$2
				AND NOT EXISTS(SELECT 1 FROM review_findings baseline WHERE baseline.job_id=$1 AND baseline.fingerprint=candidate.fingerprint)),
			(SELECT COUNT(*) FROM review_findings baseline WHERE baseline.job_id=$1
				AND NOT EXISTS(SELECT 1 FROM rule_test_findings candidate WHERE candidate.test_run_id=$2 AND candidate.fingerprint=baseline.fingerprint)),
			(SELECT COUNT(*) FROM rule_test_findings candidate WHERE candidate.test_run_id=$2
				AND EXISTS(SELECT 1 FROM review_findings baseline WHERE baseline.job_id=$1 AND baseline.fingerprint=candidate.fingerprint))`, baselineJobID, testRunID).
		Scan(&baselineCount, &candidateCount, &addedCount, &removedCount, &matchedCount); err != nil {
		return fmt.Errorf("count shadow rollout comparison: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO rule_rollout_comparisons(rollout_id,baseline_run_id,candidate_test_run_id,baseline_snapshot_id,candidate_snapshot_id,state,baseline_finding_count,candidate_finding_count,added_finding_count,removed_finding_count,matched_finding_count,completed_at)
		VALUES($1,$2,$3,$4,$5,'completed',$6,$7,$8,$9,$10,now())
		ON CONFLICT(candidate_test_run_id) DO NOTHING`, *rolloutID, sourceRunID, testRunID, baselineSnapshotID, candidateSnapshotID, baselineCount, candidateCount, addedCount, removedCount, matchedCount); err != nil {
		return fmt.Errorf("store shadow rollout comparison: %w", err)
	}
	return nil
}

func (s *PostgresStore) FailRuleTestRun(ctx context.Context, testRunID uuid.UUID, workerID string, attempt int, message string) error {
	if testRunID == uuid.Nil || workerID == "" || attempt < 1 {
		return ErrJobClaimLost
	}
	command, err := s.pool.Exec(ctx, `
		UPDATE rule_test_runs
		SET state = 'failed', error_message = left($3, 4000), locked_by = NULL,
		    locked_until = NULL, finished_at = now()
		WHERE id = $1 AND state = 'running' AND locked_by = $2
		  AND attempts = $4 AND locked_until > clock_timestamp()`, testRunID, workerID, message, attempt)
	if err != nil {
		return fmt.Errorf("fail rule test run: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrJobClaimLost
	}
	return nil
}

func (s *PostgresStore) RenewRuleTestRun(ctx context.Context, testRunID uuid.UUID, workerID string, attempt int, lease time.Duration) error {
	if testRunID == uuid.Nil || workerID == "" || attempt < 1 || lease <= 0 {
		return ErrJobClaimLost
	}
	command, err := s.pool.Exec(ctx, `
		UPDATE rule_test_runs SET locked_until = clock_timestamp() + $3::interval
		WHERE id = $1 AND state = 'running' AND locked_by = $2
		  AND attempts = $4 AND locked_until > clock_timestamp()`, testRunID, workerID, lease.String(), attempt)
	if err != nil {
		return fmt.Errorf("renew rule test run: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrJobClaimLost
	}
	return nil
}

func (s *PostgresStore) ruleTestFindings(ctx context.Context, testRunID uuid.UUID) ([]domain.Finding, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT path, start_line, end_line, severity, category, body, suggestion
		FROM rule_test_findings WHERE test_run_id = $1
		ORDER BY CASE severity WHEN 'critical' THEN 1 WHEN 'high' THEN 2 WHEN 'medium' THEN 3 ELSE 4 END,
		         path, start_line`, testRunID)
	if err != nil {
		return nil, fmt.Errorf("list rule test findings: %w", err)
	}
	defer rows.Close()
	items := make([]domain.Finding, 0)
	for rows.Next() {
		var item domain.Finding
		if err := rows.Scan(&item.Path, &item.StartLine, &item.EndLine, &item.Severity, &item.Category, &item.Body, &item.Suggestion); err != nil {
			return nil, fmt.Errorf("scan rule test finding: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanRuleTestRun(row rowScanner) (domain.RuleTestRun, error) {
	var item domain.RuleTestRun
	err := row.Scan(&item.ID, &item.TenantID, &item.RuleSetID, &item.RuleSetName, &item.RuleVersionID, &item.RuleVersion,
		&item.SourceRunID, &item.SnapshotID, &item.SnapshotSHA256, &item.State, &item.RequestedBy,
		&item.Provider, &item.APIBaseURL, &item.Repository, &item.ReviewNumber, &item.BaseSHA, &item.HeadSHA,
		&item.EngineVersion, &item.Attempts, &item.SelectedPathCount, &item.DeferredPathCount,
		&item.FindingCount, &item.DurationMS, &item.ErrorMessage, &item.CreatedAt, &item.StartedAt, &item.FinishedAt)
	item.Findings = []domain.Finding{}
	return item, err
}
