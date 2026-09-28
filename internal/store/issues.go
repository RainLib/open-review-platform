package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) ListIssues(ctx context.Context, actor, tenantSlug string, filter domain.IssueFilter) (domain.IssuePage, error) {
	if filter.Filters != nil && filter.FilterTime == nil {
		if filter.Cursor != "" {
			return domain.IssuePage{}, ErrInvalidIssueFilter
		}
		now := time.Now().UTC()
		filter.FilterTime = &now
	}
	filter.FilterActor = actor
	if !filter.Valid() {
		return domain.IssuePage{}, ErrInvalidIssueFilter
	}
	cursorStatusRank, cursorSeverityRank, cursorLastSeenAt, cursorID, hasCursor, err := domain.DecodeIssueCursor(filter)
	if err != nil {
		return domain.IssuePage{}, ErrInvalidIssueFilter
	}
	tenantID, _, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.IssuePage{}, err
	}
	if err := s.reconcileExpiredIssueExceptions(ctx, tenantID); err != nil {
		return domain.IssuePage{}, err
	}
	counts, err := s.issueInboxCounts(ctx, tenantID, actor)
	if err != nil {
		return domain.IssuePage{}, err
	}
	direction := filter.CursorDirection
	if direction == "" {
		direction = domain.IssueCursorAfter
	}
	statusDirection, severityDirection, seenDirection, idDirection := "ASC", "ASC", "DESC", "DESC"
	if direction == domain.IssueCursorBefore {
		statusDirection, severityDirection, seenDirection, idDirection = "DESC", "DESC", "ASC", "ASC"
	}
	predicate, args := issueListPredicate(tenantID, actor, filter)
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return domain.IssuePage{}, fmt.Errorf("begin issue list snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var totalCount int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM review_issues WHERE `+predicate, args...).Scan(&totalCount); err != nil {
		return domain.IssuePage{}, fmt.Errorf("count filtered review issues: %w", err)
	}
	facets := domain.IssueInboxFacets{Repositories: []string{}, Categories: []string{}}
	facetRows, err := tx.Query(ctx, `
		SELECT kind, value FROM (
			SELECT 'repository' AS kind, repository AS value FROM (
				SELECT DISTINCT repository FROM review_issues WHERE tenant_id=$1 ORDER BY repository LIMIT 100
			) repositories
			UNION ALL
			SELECT 'category' AS kind, category AS value FROM (
				SELECT DISTINCT category FROM review_issues WHERE tenant_id=$1 ORDER BY category LIMIT 100
			) categories
		) facets ORDER BY kind, value`, tenantID)
	if err != nil {
		return domain.IssuePage{}, fmt.Errorf("list review issue facets: %w", err)
	}
	for facetRows.Next() {
		var kind, value string
		if err := facetRows.Scan(&kind, &value); err != nil {
			facetRows.Close()
			return domain.IssuePage{}, fmt.Errorf("scan review issue facet: %w", err)
		}
		if kind == "repository" {
			facets.Repositories = append(facets.Repositories, value)
		} else {
			facets.Categories = append(facets.Categories, value)
		}
	}
	if err := facetRows.Err(); err != nil {
		facetRows.Close()
		return domain.IssuePage{}, fmt.Errorf("iterate review issue facets: %w", err)
	}
	facetRows.Close()
	var selectedInView *bool
	if filter.SelectedIssueID != nil {
		matched := false
		selectionArgs := append(append([]any(nil), args...), *filter.SelectedIssueID)
		selectionQuery := fmt.Sprintf(`SELECT EXISTS(SELECT 1 FROM review_issues WHERE %s AND review_issues.id = $%d)`, predicate, len(selectionArgs))
		if err := tx.QueryRow(ctx, selectionQuery, selectionArgs...).Scan(&matched); err != nil {
			return domain.IssuePage{}, fmt.Errorf("check selected review issue in view: %w", err)
		}
		selectedInView = &matched
	}
	cursorPredicate := "TRUE"
	b := issuePredicateBuilder{args: args}
	if hasCursor {
		statusParam, severityParam, seenParam, idParam := b.bind(cursorStatusRank), b.bind(cursorSeverityRank), b.bind(cursorLastSeenAt), b.bind(cursorID)
		rankOperator, seenOperator := ">", "<"
		if direction == domain.IssueCursorBefore {
			rankOperator, seenOperator = "<", ">"
		}
		cursorPredicate = fmt.Sprintf("(status_rank %s %s OR (status_rank = %s AND severity_rank %s %s) OR (status_rank = %s AND severity_rank = %s AND (last_seen_at %s %s OR (last_seen_at = %s AND id %s %s))))", rankOperator, statusParam, statusParam, rankOperator, severityParam, statusParam, severityParam, seenOperator, seenParam, seenParam, seenOperator, idParam)
	}
	limitParam := b.bind(filter.Limit + 1)
	rows, err := tx.Query(ctx, fmt.Sprintf(`
		WITH filtered AS (
			SELECT id,revision, provider, api_base_url, repository, fingerprint, path, severity, category,
			       body_preview, status,assignee_subject,disposition_kind,disposition_reason, occurrence_count, active_occurrence_count, pull_request_count,
			       first_seen_at, last_seen_at, resolved_at, updated_at,
			       CASE status WHEN 'regressed' THEN 0 WHEN 'open' THEN 1 WHEN 'suppressed' THEN 2 ELSE 3 END AS status_rank,
			       CASE severity WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 ELSE 3 END AS severity_rank
			FROM review_issues
			WHERE %s
		)
		SELECT id,revision, provider, api_base_url, repository, fingerprint, path, severity, category,
		       body_preview, status,assignee_subject,disposition_kind,disposition_reason, occurrence_count, active_occurrence_count, pull_request_count,
		       first_seen_at, last_seen_at, resolved_at, updated_at
		FROM filtered
		WHERE %s
		ORDER BY status_rank %s, severity_rank %s, last_seen_at %s, id %s
		LIMIT %s`, predicate, cursorPredicate, statusDirection, severityDirection, seenDirection, idDirection, limitParam), b.args...)
	if err != nil {
		return domain.IssuePage{}, fmt.Errorf("list review issues: %w", err)
	}
	defer rows.Close()
	issues := make([]domain.IssueSummary, 0)
	for rows.Next() {
		issue, err := scanIssueSummary(rows)
		if err != nil {
			return domain.IssuePage{}, fmt.Errorf("scan review issue: %w", err)
		}
		issues = append(issues, issue)
	}
	if err := rows.Err(); err != nil {
		return domain.IssuePage{}, fmt.Errorf("iterate review issues: %w", err)
	}
	anchor := time.Now().UTC()
	if filter.FilterTime != nil {
		anchor = filter.FilterTime.UTC()
	}
	page := domain.IssuePage{Issues: issues, Counts: counts, Facets: facets, FilterTime: anchor, TotalCount: totalCount, SelectedInView: selectedInView}
	if len(issues) > filter.Limit {
		page.Issues = issues[:filter.Limit]
	}
	if direction == domain.IssueCursorBefore {
		for left, right := 0, len(page.Issues)-1; left < right; left, right = left+1, right-1 {
			page.Issues[left], page.Issues[right] = page.Issues[right], page.Issues[left]
		}
	}
	if len(page.Issues) == 0 {
		return page, nil
	}
	if direction == domain.IssueCursorAfter {
		if len(issues) > filter.Limit {
			page.NextCursor = domain.EncodeIssueCursor(filter, page.Issues[len(page.Issues)-1])
		}
		if hasCursor {
			page.PreviousCursor = domain.EncodeIssueCursor(filter, page.Issues[0])
		}
	} else {
		// The cursor used for a reverse query identifies the first item on the
		// following page, so the last item here is a stable forward boundary.
		page.NextCursor = domain.EncodeIssueCursor(filter, page.Issues[len(page.Issues)-1])
		if len(issues) > filter.Limit {
			page.PreviousCursor = domain.EncodeIssueCursor(filter, page.Issues[0])
		}
	}
	return page, nil
}

func (s *PostgresStore) issueInboxCounts(ctx context.Context, tenantID uuid.UUID, actor string) (domain.IssueInboxCounts, error) {
	var counts domain.IssueInboxCounts
	err := s.pool.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE status = 'open'),
			COUNT(*) FILTER (WHERE status = 'regressed'),
			COUNT(*) FILTER (WHERE severity = 'critical' AND status IN ('open', 'regressed') AND active_occurrence_count > 0),
			COUNT(*) FILTER (WHERE assignee_subject = $2 AND status IN ('open', 'regressed') AND active_occurrence_count > 0),
			COUNT(*) FILTER (WHERE status = 'resolved'),
			COUNT(*) FILTER (WHERE status = 'suppressed')
		FROM review_issues
		WHERE tenant_id = $1`, tenantID, actor).Scan(&counts.Open, &counts.Regressed, &counts.Critical, &counts.Assigned, &counts.Resolved, &counts.Suppressed)
	if err != nil {
		return domain.IssueInboxCounts{}, fmt.Errorf("count review issues: %w", err)
	}
	return counts, nil
}

func (s *PostgresStore) GetIssue(ctx context.Context, actor, tenantSlug string, issueID uuid.UUID) (domain.IssueDetail, error) {
	tenantID, role, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.IssueDetail{}, err
	}
	if err := s.reconcileExpiredIssueExceptions(ctx, tenantID); err != nil {
		return domain.IssueDetail{}, err
	}
	issue, err := scanIssueSummary(s.pool.QueryRow(ctx, `
		SELECT id,revision, provider, api_base_url, repository, fingerprint, path, severity, category,
		       body_preview, status,assignee_subject,disposition_kind,disposition_reason, occurrence_count, active_occurrence_count, pull_request_count,
		       first_seen_at, last_seen_at, resolved_at, updated_at
		FROM review_issues WHERE tenant_id = $1 AND id = $2`, tenantID, issueID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.IssueDetail{}, ErrNotFound
	}
	if err != nil {
		return domain.IssueDetail{}, fmt.Errorf("get review issue: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT occurrence.id, occurrence.finding_id, occurrence.request_id, occurrence.run_id,
		       occurrence.review_number, occurrence.head_sha, finding.path, finding.start_line,
		       finding.end_line, finding.severity, finding.category, finding.body, finding.suggestion,
		       occurrence.active, occurrence.created_at
		FROM review_issue_occurrences occurrence
		JOIN review_findings finding ON finding.id = occurrence.finding_id
		WHERE occurrence.issue_id = $1
		ORDER BY occurrence.active DESC, occurrence.created_at DESC, occurrence.id DESC`, issueID)
	if err != nil {
		return domain.IssueDetail{}, fmt.Errorf("list issue occurrences: %w", err)
	}
	defer rows.Close()
	detail := domain.IssueDetail{IssueSummary: issue, Occurrences: []domain.IssueOccurrence{}, Events: []domain.IssueEvent{}, CanManage: canManageIssue(role)}
	for rows.Next() {
		occurrence := domain.IssueOccurrence{RuleAttributions: []domain.FindingRuleAttributionEvidence{}}
		if err := rows.Scan(&occurrence.ID, &occurrence.FindingID, &occurrence.RequestID, &occurrence.RunID,
			&occurrence.ReviewNumber, &occurrence.HeadSHA, &occurrence.Path, &occurrence.StartLine,
			&occurrence.EndLine, &occurrence.Severity, &occurrence.Category, &occurrence.Body,
			&occurrence.Suggestion, &occurrence.Active, &occurrence.CreatedAt); err != nil {
			return domain.IssueDetail{}, fmt.Errorf("scan issue occurrence: %w", err)
		}
		detail.Occurrences = append(detail.Occurrences, occurrence)
	}
	if err := rows.Err(); err != nil {
		return domain.IssueDetail{}, fmt.Errorf("iterate issue occurrences: %w", err)
	}
	rows.Close()
	if len(detail.Occurrences) > 0 {
		attributions, err := s.pool.Query(ctx, `
			SELECT occurrence.id, attribution.rule_key, version.id, rule_set.id, rule_set.name, version.version
			FROM review_issue_occurrences occurrence
			JOIN review_finding_rule_attributions attribution ON attribution.finding_id=occurrence.finding_id
			JOIN rule_versions version ON version.id=attribution.rule_version_id
			JOIN rule_sets rule_set ON rule_set.id=version.rule_set_id
			WHERE occurrence.issue_id=$1 AND rule_set.tenant_id=$2
			ORDER BY occurrence.id, attribution.rule_key, version.id`, issueID, tenantID)
		if err != nil {
			return domain.IssueDetail{}, fmt.Errorf("list issue rule attributions: %w", err)
		}
		occurrenceIndex := make(map[uuid.UUID]int, len(detail.Occurrences))
		for index, occurrence := range detail.Occurrences {
			occurrenceIndex[occurrence.ID] = index
		}
		for attributions.Next() {
			var occurrenceID uuid.UUID
			var attribution domain.FindingRuleAttributionEvidence
			if err := attributions.Scan(&occurrenceID, &attribution.RuleKey, &attribution.RuleVersionID,
				&attribution.RuleSetID, &attribution.RuleSetName, &attribution.Version); err != nil {
				attributions.Close()
				return domain.IssueDetail{}, fmt.Errorf("scan issue rule attribution: %w", err)
			}
			if index, ok := occurrenceIndex[occurrenceID]; ok {
				detail.Occurrences[index].RuleAttributions = append(detail.Occurrences[index].RuleAttributions, attribution)
			}
		}
		if err := attributions.Err(); err != nil {
			attributions.Close()
			return domain.IssueDetail{}, fmt.Errorf("iterate issue rule attributions: %w", err)
		}
		attributions.Close()
	}
	eventRows, err := s.pool.Query(ctx, `
		SELECT id,revision,actor_subject,action,previous_status,next_status,
		       previous_assignee,next_assignee,reason,created_at
		FROM review_issue_events WHERE issue_id=$1
		ORDER BY created_at DESC,id DESC`, issueID)
	if err != nil {
		return domain.IssueDetail{}, fmt.Errorf("list issue events: %w", err)
	}
	defer eventRows.Close()
	for eventRows.Next() {
		var event domain.IssueEvent
		if err := eventRows.Scan(&event.ID, &event.Revision, &event.ActorSubject, &event.Action,
			&event.PreviousStatus, &event.NextStatus, &event.PreviousAssignee, &event.NextAssignee,
			&event.Reason, &event.CreatedAt); err != nil {
			return domain.IssueDetail{}, fmt.Errorf("scan issue event: %w", err)
		}
		detail.Events = append(detail.Events, event)
	}
	if err := eventRows.Err(); err != nil {
		return domain.IssueDetail{}, fmt.Errorf("iterate issue events: %w", err)
	}
	var exception domain.IssueExceptionRequest
	err = s.pool.QueryRow(ctx, `
		SELECT id, rule_key, state,
		       CASE WHEN expires_at <= now() AND state IN ('pending','approved') THEN 'expired' ELSE state END,
		       requested_by, expires_at
		FROM rule_exceptions
		WHERE tenant_id = $1 AND source_issue_id = $2
		ORDER BY created_at DESC, id DESC
		LIMIT 1`, tenantID, issueID).Scan(&exception.ID, &exception.RuleKey, &exception.State,
		&exception.EffectiveState, &exception.RequestedBy, &exception.ExpiresAt)
	if err == nil {
		detail.ExceptionRequest = &exception
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return domain.IssueDetail{}, fmt.Errorf("load issue exception request: %w", err)
	}
	var external domain.ExternalIssueReceipt
	err = s.pool.QueryRow(ctx, `
		SELECT id,issue_id,provider,repository,trigger,state,external_id,external_url,attempts,last_error,created_at,updated_at
		FROM external_issue_receipts WHERE issue_id=$1`, issueID).
		Scan(&external.ID, &external.IssueID, &external.Provider, &external.Repository, &external.Trigger, &external.State,
			&external.ExternalID, &external.ExternalURL, &external.Attempts, &external.LastError, &external.CreatedAt, &external.UpdatedAt)
	if err == nil {
		detail.ExternalIssue = &external
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return domain.IssueDetail{}, fmt.Errorf("load external issue receipt: %w", err)
	}
	return detail, nil
}

func (s *PostgresStore) reconcileExpiredIssueExceptions(ctx context.Context, tenantID uuid.UUID) error {
	_, err := s.reconcileExpiredIssueExceptionsCount(ctx, tenantID)
	return err
}

// ReconcileExpiredRuleExceptions restores Issue state for expired, approved
// exceptions without waiting for an administrator to open a particular Issue
// page. The worker deliberately operates only on Issue suppressions: an
// exception's immutable rule version is never changed on expiry, and new run
// admission already excludes it through expires_at.
//
// tenantLimit bounds a single polling pass so a busy tenant cannot make the
// maintenance worker monopolize the database. Concurrent workers are safe:
// each tenant reconciliation uses FOR UPDATE SKIP LOCKED on the Issue rows.
func (s *PostgresStore) ReconcileExpiredRuleExceptions(ctx context.Context, tenantLimit int) (int, error) {
	if tenantLimit < 1 || tenantLimit > 1000 {
		return 0, fmt.Errorf("rule exception expiry tenant limit must be from 1 to 1000")
	}
	rows, err := s.pool.Query(ctx, `
		SELECT issue.tenant_id
		FROM review_issues issue
		JOIN rule_exceptions exception ON exception.id = issue.active_exception_id
		WHERE issue.status = 'suppressed'
		  AND issue.disposition_kind = 'exception'
		  AND exception.state = 'approved'
		  AND exception.expires_at <= now()
		GROUP BY issue.tenant_id
		ORDER BY min(exception.expires_at) ASC, issue.tenant_id ASC
		LIMIT $1`, tenantLimit)
	if err != nil {
		return 0, fmt.Errorf("list tenants with expired rule exceptions: %w", err)
	}
	defer rows.Close()
	tenantIDs := make([]uuid.UUID, 0, tenantLimit)
	for rows.Next() {
		var tenantID uuid.UUID
		if err := rows.Scan(&tenantID); err != nil {
			return 0, fmt.Errorf("scan tenant with expired rule exception: %w", err)
		}
		tenantIDs = append(tenantIDs, tenantID)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate tenants with expired rule exceptions: %w", err)
	}

	reconciled := 0
	for _, tenantID := range tenantIDs {
		count, err := s.reconcileExpiredIssueExceptionsCount(ctx, tenantID)
		if err != nil {
			return reconciled, err
		}
		reconciled += count
	}
	return reconciled, nil
}

func (s *PostgresStore) reconcileExpiredIssueExceptionsCount(ctx context.Context, tenantID uuid.UUID) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin expired issue exception reconciliation: %w", err)
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `
		SELECT issue.id, issue.active_exception_id
		FROM review_issues issue
		JOIN rule_exceptions exception ON exception.id = issue.active_exception_id
		WHERE issue.tenant_id = $1 AND issue.status = 'suppressed'
		  AND issue.disposition_kind = 'exception' AND exception.state = 'approved'
		  AND exception.expires_at <= now()
		FOR UPDATE OF issue SKIP LOCKED`, tenantID)
	if err != nil {
		return 0, fmt.Errorf("select expired issue exceptions: %w", err)
	}
	type expiredLink struct{ issueID, exceptionID uuid.UUID }
	links := make([]expiredLink, 0)
	for rows.Next() {
		var link expiredLink
		if err := rows.Scan(&link.issueID, &link.exceptionID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan expired issue exception: %w", err)
		}
		links = append(links, link)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("iterate expired issue exceptions: %w", err)
	}
	rows.Close()
	for _, link := range links {
		if err := clearIssueExceptionSuppression(ctx, tx, tenantID, "system:rule-exception-expiry", link.exceptionID, link.issueID, "exception_expired"); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit expired issue exception reconciliation: %w", err)
	}
	return len(links), nil
}

func syncIssueOccurrences(ctx context.Context, tx pgx.Tx, jobID uuid.UUID, findings []domain.Finding) error {
	var tenantID, requestID, runID uuid.UUID
	var provider domain.Provider
	var apiBaseURL, repository, headSHA string
	var reviewNumber int
	var nullableRequestID, nullableRunID *uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT job.tenant_id, job.provider, job.api_base_url, job.repository, job.review_number, job.head_sha,
		       request.id, run.id
		FROM review_jobs job
		LEFT JOIN review_requests request
		  ON request.tenant_id = job.tenant_id AND request.installation_id = job.installation_id
		 AND request.repository = job.repository AND request.review_number = job.review_number
		LEFT JOIN review_runs run ON run.legacy_job_id = job.id
		WHERE job.id = $1`, jobID).Scan(&tenantID, &provider, &apiBaseURL, &repository, &reviewNumber, &headSHA, &nullableRequestID, &nullableRunID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("load finding issue context: %w", err)
	}
	if nullableRequestID != nil {
		requestID = *nullableRequestID
	}
	if nullableRunID != nil {
		runID = *nullableRunID
	}

	affected := map[uuid.UUID]struct{}{}
	// Capture the aggregate state before this head mutates it. Automatic Issue
	// policy evaluates transitions (first seen, regression, threshold crossing),
	// never a racy read after another worker has updated the same fingerprint.
	priorIssues := map[uuid.UUID]issueAutomationPrior{}
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT issue.id,issue.status,issue.occurrence_count
		FROM review_issue_occurrences occurrence
		JOIN review_jobs previous_job ON previous_job.id = occurrence.job_id
		JOIN review_issues issue ON issue.id = occurrence.issue_id
		WHERE occurrence.active = TRUE AND previous_job.tenant_id = $1 AND previous_job.provider = $2
		  AND previous_job.api_base_url = $3 AND previous_job.repository = $4
		  AND previous_job.review_number = $5`, tenantID, provider, apiBaseURL, repository, reviewNumber)
	if err != nil {
		return fmt.Errorf("load previous active issue occurrences: %w", err)
	}
	for rows.Next() {
		var issueID uuid.UUID
		var prior issueAutomationPrior
		if err := rows.Scan(&issueID, &prior.Status, &prior.OccurrenceCount); err != nil {
			rows.Close()
			return fmt.Errorf("scan previous issue occurrence: %w", err)
		}
		prior.Exists = true
		affected[issueID] = struct{}{}
		priorIssues[issueID] = prior
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate previous issue occurrences: %w", err)
	}
	rows.Close()
	if _, err := tx.Exec(ctx, `
		UPDATE review_issue_occurrences occurrence SET active = FALSE, updated_at = now()
		FROM review_jobs previous_job
		WHERE previous_job.id = occurrence.job_id AND occurrence.active = TRUE
		  AND previous_job.tenant_id = $1 AND previous_job.provider = $2
		  AND previous_job.api_base_url = $3 AND previous_job.repository = $4
		  AND previous_job.review_number = $5`, tenantID, provider, apiBaseURL, repository, reviewNumber); err != nil {
		return fmt.Errorf("deactivate previous issue occurrences: %w", err)
	}

	for _, finding := range findings {
		fingerprint := findingFingerprint(finding)
		providerMarker := domain.FindingMarker(jobID, finding)
		var findingID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO review_findings (job_id, path, start_line, end_line, severity, category, body, suggestion,
			                             code_excerpt, code_excerpt_start_line, proposed_patch, fingerprint, provider_marker)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
			ON CONFLICT (job_id, fingerprint) DO UPDATE
			SET path = EXCLUDED.path, start_line = EXCLUDED.start_line, end_line = EXCLUDED.end_line,
			    severity = EXCLUDED.severity, category = EXCLUDED.category, body = EXCLUDED.body,
			    suggestion = EXCLUDED.suggestion, code_excerpt = EXCLUDED.code_excerpt,
			    code_excerpt_start_line = EXCLUDED.code_excerpt_start_line,
			    proposed_patch = EXCLUDED.proposed_patch, provider_marker = EXCLUDED.provider_marker
			RETURNING id`, jobID, finding.Path, finding.StartLine, finding.EndLine, finding.Severity,
			finding.Category, finding.Body, finding.Suggestion, finding.CodeExcerpt,
			finding.CodeExcerptStartLine, finding.ProposedPatch, fingerprint, providerMarker).Scan(&findingID); err != nil {
			return fmt.Errorf("save finding: %w", err)
		}
		if err := recordFindingRuleAttributions(ctx, tx, jobID, findingID, finding.RuleReferences); err != nil {
			return err
		}
		var issueID uuid.UUID
		var prior issueAutomationPrior
		err := tx.QueryRow(ctx, `
			SELECT id,status,occurrence_count FROM review_issues
			WHERE tenant_id=$1 AND provider=$2 AND api_base_url=$3 AND repository=$4 AND fingerprint=$5
			FOR UPDATE`, tenantID, provider, apiBaseURL, repository, fingerprint).
			Scan(&issueID, &prior.Status, &prior.OccurrenceCount)
		if errors.Is(err, pgx.ErrNoRows) {
			prior = issueAutomationPrior{}
		} else if err != nil {
			return fmt.Errorf("lock prior review issue: %w", err)
		} else {
			prior.Exists = true
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO review_issues (tenant_id, provider, api_base_url, repository, fingerprint, path,
			                           severity, category, body_preview, occurrence_count, active_occurrence_count,
			                           pull_request_count)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, LEFT($9, 240), 1, 1, 1)
			ON CONFLICT (tenant_id, provider, api_base_url, repository, fingerprint) DO UPDATE
			SET path = EXCLUDED.path, severity = EXCLUDED.severity, category = EXCLUDED.category,
			    body_preview = EXCLUDED.body_preview, last_seen_at = now(), updated_at = now(),
			    revision = review_issues.revision + CASE WHEN review_issues.status = 'resolved' THEN 1 ELSE 0 END,
			    status = CASE WHEN review_issues.status = 'resolved' THEN 'regressed' ELSE review_issues.status END,
			    resolved_at = NULL
			RETURNING id`, tenantID, provider, apiBaseURL, repository, fingerprint, finding.Path,
			finding.Severity, finding.Category, finding.Body).Scan(&issueID); err != nil {
			return fmt.Errorf("upsert review issue: %w", err)
		}
		if _, recorded := priorIssues[issueID]; !recorded {
			priorIssues[issueID] = prior
		}
		affected[issueID] = struct{}{}
		var requestValue, runValue any
		if requestID != uuid.Nil {
			requestValue = requestID
		}
		if runID != uuid.Nil {
			runValue = runID
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO review_issue_occurrences (issue_id, finding_id, job_id, request_id, run_id, review_number, head_sha)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (finding_id) DO UPDATE
			SET issue_id = EXCLUDED.issue_id, request_id = EXCLUDED.request_id, run_id = EXCLUDED.run_id,
			    review_number = EXCLUDED.review_number, head_sha = EXCLUDED.head_sha,
			    active = TRUE, updated_at = now()`, issueID, findingID, jobID, requestValue, runValue, reviewNumber, headSHA); err != nil {
			return fmt.Errorf("upsert issue occurrence: %w", err)
		}
	}

	for issueID := range affected {
		if _, err := tx.Exec(ctx, `
			UPDATE review_issues issue SET
				occurrence_count = aggregate.total_count,
				active_occurrence_count = aggregate.active_count,
				pull_request_count = aggregate.pull_request_count,
				revision = issue.revision + CASE
					WHEN issue.status <> 'suppressed' AND aggregate.active_count = 0 AND issue.status <> 'resolved' THEN 1
					ELSE 0
				END,
				status = CASE
					WHEN issue.status = 'suppressed' THEN 'suppressed'
					WHEN aggregate.active_count = 0 THEN 'resolved'
					ELSE issue.status
				END,
				resolved_at = CASE WHEN aggregate.active_count = 0 THEN COALESCE(issue.resolved_at, now()) ELSE NULL END,
				updated_at = now()
			FROM (
				SELECT COUNT(*)::INTEGER AS total_count,
				       COUNT(*) FILTER (WHERE active)::INTEGER AS active_count,
				       COUNT(DISTINCT review_number)::INTEGER AS pull_request_count
				FROM review_issue_occurrences WHERE issue_id = $1
			) aggregate
			WHERE issue.id = $1`, issueID); err != nil {
			return fmt.Errorf("refresh review issue counters: %w", err)
		}
		if err := queueExternalIssueResolution(ctx, tx, tenantID, issueID, priorIssues[issueID]); err != nil {
			return fmt.Errorf("queue external issue resolution: %w", err)
		}
		if err := queueExternalIssuePublication(ctx, tx, tenantID, issueID, priorIssues[issueID]); err != nil {
			return fmt.Errorf("queue external issue publication: %w", err)
		}
	}
	return nil
}

// recordFindingRuleAttributions accepts only exact, model-declared references
// that occur in the canonical snapshot captured at admission. Current rule
// bindings, category names, and severity are deliberately not used as a
// fallback: an unattributed finding is safer than a fabricated policy claim.
func recordFindingRuleAttributions(ctx context.Context, tx pgx.Tx, jobID, findingID uuid.UUID, references []domain.FindingRuleReference) error {
	if len(references) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(references))
	for _, reference := range references {
		key := strings.TrimSpace(reference.RuleKey)
		version, err := uuid.Parse(strings.TrimSpace(reference.SourceVersion))
		if key == "" || len(key) > 200 || err != nil {
			continue
		}
		identity := key + "\x00" + version.String()
		if _, exists := seen[identity]; exists {
			continue
		}
		seen[identity] = struct{}{}
		if _, err := tx.Exec(ctx, `
			INSERT INTO review_finding_rule_attributions (finding_id, rule_key, rule_version_id)
			SELECT $1, $2, $3
			FROM review_runs run
			JOIN rule_snapshots snapshot ON snapshot.id = run.rule_snapshot_id
			JOIN rule_snapshot_sources source
			  ON source.snapshot_id = snapshot.id AND source.rule_version_id = $3
			WHERE run.legacy_job_id = $4
			  AND EXISTS (
				SELECT 1
				FROM jsonb_array_elements(COALESCE(snapshot.canonical_payload -> 'rules', '[]'::jsonb)) AS candidate(rule)
				WHERE candidate.rule ->> 'key' = $2
				  AND candidate.rule ->> 'source_version' = $3::text
			  )
			ON CONFLICT DO NOTHING`, findingID, key, version, jobID); err != nil {
			return fmt.Errorf("record finding rule attribution: %w", err)
		}
	}
	return nil
}

func scanIssueSummary(row rowScanner) (domain.IssueSummary, error) {
	var issue domain.IssueSummary
	err := row.Scan(&issue.ID, &issue.Revision, &issue.Provider, &issue.APIBaseURL, &issue.Repository, &issue.Fingerprint,
		&issue.Path, &issue.Severity, &issue.Category, &issue.BodyPreview, &issue.Status,
		&issue.AssigneeSubject, &issue.DispositionKind, &issue.DispositionReason,
		&issue.OccurrenceCount, &issue.ActiveOccurrenceCount, &issue.PullRequestCount,
		&issue.FirstSeenAt, &issue.LastSeenAt, &issue.ResolvedAt, &issue.UpdatedAt)
	return issue, err
}

func (s *PostgresStore) MutateIssue(ctx context.Context, actor, tenantSlug string, issueID uuid.UUID, input domain.IssueActionInput) (domain.IssueDetail, error) {
	input.Action = strings.ToLower(strings.TrimSpace(input.Action))
	input.AssigneeSubject = strings.TrimSpace(input.AssigneeSubject)
	input.Reason = strings.TrimSpace(input.Reason)
	if issueID == uuid.Nil || input.ExpectedRevision < 1 || len(input.AssigneeSubject) > 256 || len(input.Reason) > 1000 {
		return domain.IssueDetail{}, ErrInvalidIssueAction
	}
	switch input.Action {
	case "assign", "unassign", "resolve", "reopen", "false_positive", "suppression_cleared":
	default:
		return domain.IssueDetail{}, ErrInvalidIssueAction
	}
	if input.Action == "assign" && input.AssigneeSubject == "" {
		return domain.IssueDetail{}, ErrInvalidIssueAction
	}
	if input.Action == "false_positive" && len(input.Reason) < 3 {
		return domain.IssueDetail{}, ErrInvalidIssueAction
	}
	tenantID, role, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.IssueDetail{}, err
	}
	if !canManageIssue(role) {
		return domain.IssueDetail{}, ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.IssueDetail{}, err
	}
	defer tx.Rollback(ctx)
	var revision, activeCount int
	var previousStatus domain.IssueStatus
	var previousAssignee, dispositionKind, dispositionReason string
	err = tx.QueryRow(ctx, `
		SELECT revision,status,active_occurrence_count,assignee_subject,disposition_kind,disposition_reason
		FROM review_issues WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenantID, issueID).Scan(
		&revision, &previousStatus, &activeCount, &previousAssignee, &dispositionKind, &dispositionReason)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.IssueDetail{}, ErrNotFound
	}
	if err != nil {
		return domain.IssueDetail{}, fmt.Errorf("lock review issue: %w", err)
	}
	if revision != input.ExpectedRevision {
		return domain.IssueDetail{}, ErrRevisionConflict
	}
	if previousStatus == domain.IssueSuppressed && dispositionKind == "exception" && input.Action != "assign" && input.Action != "unassign" {
		return domain.IssueDetail{}, ErrConflict
	}
	nextStatus := previousStatus
	nextAssignee := previousAssignee
	action := input.Action
	switch input.Action {
	case "assign":
		var assigneeRole string
		if err := tx.QueryRow(ctx, `SELECT role FROM memberships WHERE tenant_id=$1 AND subject=$2 AND active=TRUE`, tenantID, input.AssigneeSubject).Scan(&assigneeRole); errors.Is(err, pgx.ErrNoRows) || assigneeRole == "billing_viewer" {
			return domain.IssueDetail{}, ErrInvalidIssueAssignee
		} else if err != nil {
			return domain.IssueDetail{}, fmt.Errorf("validate issue assignee: %w", err)
		}
		if previousAssignee == input.AssigneeSubject {
			return domain.IssueDetail{}, ErrConflict
		}
		nextAssignee = input.AssigneeSubject
		action = "assigned"
	case "unassign":
		if previousAssignee == "" {
			return domain.IssueDetail{}, ErrConflict
		}
		nextAssignee = ""
		action = "unassigned"
	case "resolve":
		if previousStatus == domain.IssueResolved {
			return domain.IssueDetail{}, ErrConflict
		}
		nextStatus = domain.IssueResolved
		dispositionKind = "resolved"
		dispositionReason = input.Reason
		action = "resolved"
	case "reopen":
		if previousStatus == domain.IssueOpen {
			return domain.IssueDetail{}, ErrConflict
		}
		nextStatus = domain.IssueOpen
		dispositionKind, dispositionReason = "", ""
		action = "reopened"
	case "false_positive":
		if previousStatus == domain.IssueSuppressed && dispositionKind == "false_positive" {
			return domain.IssueDetail{}, ErrConflict
		}
		nextStatus = domain.IssueSuppressed
		dispositionKind, dispositionReason = "false_positive", input.Reason
	case "suppression_cleared":
		if previousStatus != domain.IssueSuppressed {
			return domain.IssueDetail{}, ErrConflict
		}
		if activeCount > 0 {
			nextStatus = domain.IssueOpen
		} else {
			nextStatus = domain.IssueResolved
		}
		dispositionKind, dispositionReason = "", ""
	}
	nextRevision := revision + 1
	_, err = tx.Exec(ctx, `
		UPDATE review_issues SET revision=$3,status=$4,assignee_subject=$5,
		       disposition_kind=$6,disposition_reason=$7,
		       active_exception_id=CASE WHEN $6='exception' THEN active_exception_id ELSE NULL END,
		       suppression_expires_at=CASE WHEN $6='exception' THEN suppression_expires_at ELSE NULL END,
		       resolved_at=CASE WHEN $4='resolved' THEN COALESCE(resolved_at,now()) ELSE NULL END,
		       updated_at=now()
		WHERE tenant_id=$1 AND id=$2`, tenantID, issueID, nextRevision, nextStatus,
		nextAssignee, dispositionKind, dispositionReason)
	if err != nil {
		return domain.IssueDetail{}, fmt.Errorf("update review issue: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO review_issue_events (
			tenant_id,issue_id,revision,actor_subject,action,previous_status,next_status,
			previous_assignee,next_assignee,reason
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, tenantID, issueID, nextRevision,
		actor, action, previousStatus, nextStatus, previousAssignee, nextAssignee, input.Reason); err != nil {
		return domain.IssueDetail{}, fmt.Errorf("record review issue event: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata)
		VALUES ($1,$2,$3,$4,jsonb_build_object('revision',$5::integer,'previous_status',$6::text,'next_status',$7::text,'previous_assignee',$8::text,'next_assignee',$9::text,'reason',$10::text))`,
		tenantID, actor, "issue."+action, issueID.String(), nextRevision, previousStatus,
		nextStatus, previousAssignee, nextAssignee, input.Reason); err != nil {
		return domain.IssueDetail{}, fmt.Errorf("audit review issue mutation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.IssueDetail{}, err
	}
	return s.GetIssue(ctx, actor, tenantSlug, issueID)
}

func canManageIssue(role string) bool {
	return role == "owner" || role == "admin" || role == "rule_admin" || role == "reviewer"
}
