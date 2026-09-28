package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) GetFindingFeedbackDashboard(ctx context.Context, actor, tenantSlug string, limit int) (domain.FindingFeedbackDashboard, error) {
	if limit < 1 || limit > 100 {
		return domain.FindingFeedbackDashboard{}, fmt.Errorf("feedback repository limit must be from 1 to 100")
	}
	tenantID, _, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.FindingFeedbackDashboard{}, err
	}
	result := domain.FindingFeedbackDashboard{Repositories: []domain.FindingFeedbackMetric{}, RecentFindings: []domain.FindingFeedbackItem{}}
	err = s.pool.QueryRow(ctx, `
		SELECT COUNT(DISTINCT finding.id), COUNT(feedback.id) FILTER (WHERE feedback.retracted_at IS NULL),
		       COUNT(DISTINCT finding.id) FILTER (WHERE feedback.kind = 'useful' AND feedback.retracted_at IS NULL),
		       COUNT(DISTINCT finding.id) FILTER (WHERE feedback.kind = 'false_positive' AND feedback.retracted_at IS NULL),
		       COUNT(DISTINCT finding.id) FILTER (WHERE feedback.kind = 'resolved' AND feedback.retracted_at IS NULL),
		       COUNT(DISTINCT finding.id) FILTER (WHERE feedback.kind = 'wont_fix' AND feedback.retracted_at IS NULL)
		FROM review_findings finding
		JOIN review_jobs job ON job.id = finding.job_id
		LEFT JOIN finding_feedback feedback ON feedback.finding_id = finding.id
		WHERE job.tenant_id = $1`, tenantID).Scan(&result.FindingCount, &result.FeedbackCount, &result.UsefulFindingCount, &result.FalsePositiveCount, &result.ResolvedFindingCount, &result.WontFixFindingCount)
	if err != nil {
		return domain.FindingFeedbackDashboard{}, fmt.Errorf("summarize finding feedback: %w", err)
	}
	var attributedFindings int
	if err := s.pool.QueryRow(ctx, `
		SELECT COUNT(DISTINCT attribution.finding_id)
		FROM review_finding_rule_attributions attribution
		JOIN review_findings finding ON finding.id = attribution.finding_id
		JOIN review_jobs job ON job.id = finding.job_id
		WHERE job.tenant_id = $1`, tenantID).Scan(&attributedFindings); err != nil {
		return domain.FindingFeedbackDashboard{}, fmt.Errorf("summarize finding rule attributions: %w", err)
	}
	if attributedFindings == 0 {
		result.AttributionWarning = "No finding has a snapshot-validated policy attribution yet. Feedback remains attributed to the repository and immutable run snapshot only."
	} else {
		result.AttributionWarning = fmt.Sprintf("%d finding(s) have exact policy attribution validated against their immutable run snapshot. Findings without a matching model-declared rule remain repository- and snapshot-attributed only.", attributedFindings)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT job.repository,
		       COUNT(DISTINCT finding.id), COUNT(feedback.id) FILTER (WHERE feedback.retracted_at IS NULL),
		       COUNT(DISTINCT finding.id) FILTER (WHERE feedback.kind = 'useful' AND feedback.retracted_at IS NULL),
		       COUNT(DISTINCT finding.id) FILTER (WHERE feedback.kind = 'false_positive' AND feedback.retracted_at IS NULL),
		       COUNT(DISTINCT finding.id) FILTER (WHERE feedback.kind = 'resolved' AND feedback.retracted_at IS NULL),
		       COUNT(DISTINCT finding.id) FILTER (WHERE feedback.kind = 'wont_fix' AND feedback.retracted_at IS NULL),
		       COUNT(DISTINCT finding.id) FILTER (WHERE finding.severity IN ('high', 'critical')),
		       COUNT(DISTINCT run.rule_snapshot_id) FILTER (WHERE run.rule_snapshot_id IS NOT NULL)
		FROM review_findings finding
		JOIN review_jobs job ON job.id = finding.job_id
		LEFT JOIN review_runs run ON run.legacy_job_id = job.id
		LEFT JOIN finding_feedback feedback ON feedback.finding_id = finding.id
		WHERE job.tenant_id = $1
		GROUP BY job.repository
		ORDER BY COUNT(feedback.id) FILTER (WHERE feedback.retracted_at IS NULL) DESC, COUNT(DISTINCT finding.id) DESC, job.repository
		LIMIT $2`, tenantID, limit)
	if err != nil {
		return domain.FindingFeedbackDashboard{}, fmt.Errorf("list finding feedback metrics: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item domain.FindingFeedbackMetric
		if err := rows.Scan(&item.Repository, &item.FindingCount, &item.FeedbackCount, &item.UsefulFindingCount, &item.FalsePositiveCount, &item.ResolvedFindingCount, &item.WontFixFindingCount, &item.HighRiskFindingCount, &item.DistinctSnapshotCount); err != nil {
			return domain.FindingFeedbackDashboard{}, fmt.Errorf("scan finding feedback metric: %w", err)
		}
		result.Repositories = append(result.Repositories, item)
	}
	if err := rows.Err(); err != nil {
		return domain.FindingFeedbackDashboard{}, fmt.Errorf("iterate finding feedback metrics: %w", err)
	}
	rows.Close()
	recentRows, err := s.pool.Query(ctx, `
		SELECT finding.id, run.id, job.provider, job.api_base_url, job.repository, job.review_number, job.head_sha,
		       finding.path, finding.start_line, finding.end_line, finding.severity, finding.category,
		       LEFT(finding.body, 240),
		       COALESCE(MAX(feedback.kind) FILTER (WHERE feedback.provider = 'console' AND feedback.actor_external_id = $2 AND feedback.retracted_at IS NULL), ''),
		       COUNT(feedback.id) FILTER (WHERE feedback.kind = 'useful' AND feedback.retracted_at IS NULL),
		       COUNT(feedback.id) FILTER (WHERE feedback.kind = 'false_positive' AND feedback.retracted_at IS NULL),
		       finding.created_at
		FROM review_findings finding
		JOIN review_jobs job ON job.id = finding.job_id
		LEFT JOIN review_runs run ON run.legacy_job_id = job.id AND run.head_sha = job.head_sha
		LEFT JOIN finding_feedback feedback ON feedback.finding_id = finding.id
		WHERE job.tenant_id = $1
		GROUP BY finding.id, run.id, job.provider, job.api_base_url, job.repository, job.review_number, job.head_sha
		ORDER BY finding.created_at DESC, finding.id DESC
		LIMIT 30`, tenantID, actor)
	if err != nil {
		return domain.FindingFeedbackDashboard{}, fmt.Errorf("list recent findings: %w", err)
	}
	defer recentRows.Close()
	for recentRows.Next() {
		var item domain.FindingFeedbackItem
		if err := recentRows.Scan(&item.ID, &item.RunID, &item.Provider, &item.APIBaseURL, &item.Repository, &item.ReviewNumber, &item.HeadSHA, &item.Path, &item.StartLine, &item.EndLine, &item.Severity, &item.Category, &item.BodyPreview, &item.ActorDisposition, &item.UsefulCount, &item.FalsePositiveCount, &item.CreatedAt); err != nil {
			return domain.FindingFeedbackDashboard{}, fmt.Errorf("scan recent finding: %w", err)
		}
		result.RecentFindings = append(result.RecentFindings, item)
	}
	if err := recentRows.Err(); err != nil {
		return domain.FindingFeedbackDashboard{}, fmt.Errorf("iterate recent findings: %w", err)
	}
	return result, nil
}

// ListFindings is the cross-run read model. Filtering happens before the
// bounded page, so old active/high-risk findings cannot disappear behind the
// dashboard's intentionally small recent-feedback sample.
func (s *PostgresStore) ListFindings(ctx context.Context, actor, tenantSlug string, filter domain.FindingFilter) (domain.FindingPage, error) {
	if !filter.Valid() {
		return domain.FindingPage{}, ErrInvalidFindingFilter
	}
	// A cursor is navigation state, not authorization. Bind it to both the
	// viewer and workspace so it cannot silently navigate another tenant.
	cursorScope := actor + "\x00" + tenantSlug
	cursorAt, cursorID, hasCursor, err := domain.DecodeFindingCursor(filter, cursorScope)
	if err != nil {
		return domain.FindingPage{}, ErrInvalidFindingFilter
	}
	tenantID, _, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.FindingPage{}, err
	}
	direction := filter.CursorDirection
	if direction == "" {
		direction = domain.WorkQueueCursorAfter
	}
	operator, ordering := "<", "DESC"
	if direction == domain.WorkQueueCursorBefore {
		operator, ordering = ">", "ASC"
	}
	query := fmt.Sprintf(`
		SELECT finding.id, run.id, job.provider, job.api_base_url, job.repository, job.review_number, job.head_sha,
		       finding.path, finding.start_line, finding.end_line, finding.severity, finding.category,
		       LEFT(finding.body, 240), COALESCE(actor_feedback.kind, ''),
		       (SELECT COUNT(*) FROM finding_feedback feedback WHERE feedback.finding_id = finding.id AND feedback.kind = 'useful' AND feedback.retracted_at IS NULL),
		       (SELECT COUNT(*) FROM finding_feedback feedback WHERE feedback.finding_id = finding.id AND feedback.kind = 'false_positive' AND feedback.retracted_at IS NULL),
		       finding.created_at
		FROM review_findings finding
		JOIN review_jobs job ON job.id = finding.job_id
		LEFT JOIN review_runs run ON run.legacy_job_id = job.id AND run.head_sha = job.head_sha
		LEFT JOIN LATERAL (
			SELECT feedback.kind FROM finding_feedback feedback
			WHERE feedback.finding_id = finding.id AND feedback.provider = 'console'
			  AND feedback.actor_external_id = $2 AND feedback.retracted_at IS NULL
			ORDER BY feedback.created_at DESC, feedback.id DESC LIMIT 1
		) actor_feedback ON TRUE
		WHERE job.tenant_id = $1
		  AND ($4 = '' OR job.repository = $4)
		  AND ($5 = '' OR strpos(lower(job.repository || ' ' || finding.path || ' ' || finding.category || ' ' || finding.body), $5) > 0)
		  AND (($3 = 'active' AND actor_feedback.kind IS NULL)
		       OR ($3 = 'high-risk' AND actor_feedback.kind IS NULL AND finding.severity IN ('high', 'critical'))
		       OR ($3 = 'actioned' AND actor_feedback.kind IS NOT NULL))
		  AND (NOT $6 OR (finding.created_at, finding.id) %s ($7, $8))
		ORDER BY finding.created_at %s, finding.id %s
		LIMIT $9`, operator, ordering, ordering)
	rows, err := s.pool.Query(ctx, query, tenantID, actor, string(filter.View), filter.Repository,
		strings.ToLower(filter.Query), hasCursor, cursorAt, cursorID, filter.Limit+1)
	if err != nil {
		return domain.FindingPage{}, fmt.Errorf("list findings: %w", err)
	}
	defer rows.Close()
	items := make([]domain.FindingFeedbackItem, 0, filter.Limit+1)
	for rows.Next() {
		var item domain.FindingFeedbackItem
		if err := rows.Scan(&item.ID, &item.RunID, &item.Provider, &item.APIBaseURL, &item.Repository, &item.ReviewNumber, &item.HeadSHA,
			&item.Path, &item.StartLine, &item.EndLine, &item.Severity, &item.Category, &item.BodyPreview,
			&item.ActorDisposition, &item.UsefulCount, &item.FalsePositiveCount, &item.CreatedAt); err != nil {
			return domain.FindingPage{}, fmt.Errorf("scan finding: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return domain.FindingPage{}, fmt.Errorf("iterate findings: %w", err)
	}
	page := domain.FindingPage{Findings: items}
	if len(items) > filter.Limit {
		page.Findings = items[:filter.Limit]
	}
	if direction == domain.WorkQueueCursorBefore {
		for left, right := 0, len(page.Findings)-1; left < right; left, right = left+1, right-1 {
			page.Findings[left], page.Findings[right] = page.Findings[right], page.Findings[left]
		}
	}
	if len(page.Findings) == 0 {
		return page, nil
	}
	if direction == domain.WorkQueueCursorAfter {
		if len(items) > filter.Limit {
			page.NextCursor = domain.EncodeFindingCursor(filter, cursorScope, page.Findings[len(page.Findings)-1])
		}
		if hasCursor {
			page.PreviousCursor = domain.EncodeFindingCursor(filter, cursorScope, page.Findings[0])
		}
	} else {
		page.NextCursor = domain.EncodeFindingCursor(filter, cursorScope, page.Findings[len(page.Findings)-1])
		if len(items) > filter.Limit {
			page.PreviousCursor = domain.EncodeFindingCursor(filter, cursorScope, page.Findings[0])
		}
	}
	return page, nil
}

func (s *PostgresStore) SetFindingDisposition(ctx context.Context, actor, tenantSlug string, findingID uuid.UUID, input domain.FindingDispositionInput) error {
	input.Kind = strings.ToLower(strings.TrimSpace(input.Kind))
	if findingID == uuid.Nil || (input.Kind != "resolved" && input.Kind != "wont_fix" && input.Kind != "clear") {
		return ErrInvalidFindingFeedback
	}
	tenantID, role, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return err
	}
	if role != "owner" && role != "admin" && role != "rule_admin" && role != "reviewer" {
		return ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin finding disposition: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var exists bool
	err = tx.QueryRow(ctx, `SELECT TRUE FROM review_findings finding JOIN review_jobs job ON job.id = finding.job_id WHERE finding.id = $1 AND job.tenant_id = $2 FOR UPDATE OF finding`, findingID, tenantID).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("authorize finding disposition: %w", err)
	}
	externalID := "console:" + findingID.String() + ":" + actor
	if input.Kind == "clear" {
		_, err = tx.Exec(ctx, `UPDATE finding_feedback SET retracted_at = now() WHERE provider = 'console' AND reaction_external_id = $1 AND finding_id = $2`, externalID, findingID)
	} else {
		_, err = tx.Exec(ctx, `
			INSERT INTO finding_feedback (tenant_id, finding_id, provider, delivery_id, reaction_external_id, actor_external_id, kind)
			VALUES ($1, $2, 'console', 'console', $3, $4, $5)
			ON CONFLICT (provider, reaction_external_id) DO UPDATE SET kind = EXCLUDED.kind, retracted_at = NULL`, tenantID, findingID, externalID, actor, input.Kind)
	}
	if err != nil {
		return fmt.Errorf("set finding disposition: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata) VALUES ($1, $2, 'finding.disposition', $3, jsonb_build_object('kind', $4::text))`, tenantID, actor, findingID.String(), input.Kind); err != nil {
		return fmt.Errorf("audit finding disposition: %w", err)
	}
	return tx.Commit(ctx)
}
