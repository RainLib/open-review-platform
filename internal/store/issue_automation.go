package store

import (
	"context"
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

// GetIssueAutoCreatePolicy returns a safe policy projection even before an
// administrator has saved one. The default is intentionally disabled.
func (s *PostgresStore) GetIssueAutoCreatePolicy(ctx context.Context, actor, tenantSlug string) (domain.IssueAutoCreatePolicy, error) {
	tenantID, role, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.IssueAutoCreatePolicy{}, err
	}
	policy, found, err := getIssueAutoCreatePolicy(ctx, s.pool, tenantID)
	if err != nil {
		return domain.IssueAutoCreatePolicy{}, err
	}
	if !found {
		policy = domain.DefaultIssueAutoCreatePolicy()
	}
	policy.CanManage = canManageIssueAutomation(role)
	return policy, nil
}

func (s *PostgresStore) SaveIssueAutoCreatePolicy(ctx context.Context, actor, tenantSlug string, input domain.IssueAutoCreatePolicy) (domain.IssueAutoCreatePolicy, error) {
	policy, valid := domain.NormalizeIssueAutoCreatePolicy(input)
	if !valid {
		return domain.IssueAutoCreatePolicy{}, ErrInvalidIssueAction
	}
	tenantID, role, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.IssueAutoCreatePolicy{}, err
	}
	if !canManageIssueAutomation(role) {
		return domain.IssueAutoCreatePolicy{}, ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.IssueAutoCreatePolicy{}, fmt.Errorf("begin issue automation policy save: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var currentRevision int
	err = tx.QueryRow(ctx, `SELECT revision FROM issue_auto_create_policies WHERE tenant_id = $1 FOR UPDATE`, tenantID).Scan(&currentRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		if policy.Revision != 0 {
			return domain.IssueAutoCreatePolicy{}, ErrRevisionConflict
		}
		policy.Revision = 1
		err = tx.QueryRow(ctx, `
			INSERT INTO issue_auto_create_policies (
				tenant_id,revision,enabled,target,repository_scopes,minimum_severity,categories,
				trigger_first_seen,trigger_regressed,repeat_occurrence_threshold,labels,
				assignee_external_id,title_template,body_template,updated_by
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
			RETURNING revision,enabled,target,repository_scopes,minimum_severity,categories,
				trigger_first_seen,trigger_regressed,repeat_occurrence_threshold,labels,
				assignee_external_id,title_template,body_template,updated_by,updated_at`,
			tenantID, policy.Revision, policy.Enabled, policy.Target, policy.RepositoryScopes, policy.MinimumSeverity,
			policy.Categories, policy.TriggerFirstSeen, policy.TriggerRegressed, policy.RepeatOccurrenceThreshold,
			policy.Labels, policy.AssigneeExternalID, policy.TitleTemplate, policy.BodyTemplate, actor).
			Scan(&policy.Revision, &policy.Enabled, &policy.Target, &policy.RepositoryScopes, &policy.MinimumSeverity,
				&policy.Categories, &policy.TriggerFirstSeen, &policy.TriggerRegressed, &policy.RepeatOccurrenceThreshold,
				&policy.Labels, &policy.AssigneeExternalID, &policy.TitleTemplate, &policy.BodyTemplate, &policy.UpdatedBy, &policy.UpdatedAt)
	} else if err != nil {
		return domain.IssueAutoCreatePolicy{}, fmt.Errorf("lock issue automation policy: %w", err)
	} else {
		if policy.Revision != currentRevision {
			return domain.IssueAutoCreatePolicy{}, ErrRevisionConflict
		}
		policy.Revision++
		err = tx.QueryRow(ctx, `
			UPDATE issue_auto_create_policies
			SET revision=$2,enabled=$3,target=$4,repository_scopes=$5,minimum_severity=$6,categories=$7,
				trigger_first_seen=$8,trigger_regressed=$9,repeat_occurrence_threshold=$10,labels=$11,
				assignee_external_id=$12,title_template=$13,body_template=$14,updated_by=$15,updated_at=now()
			WHERE tenant_id=$1
			RETURNING revision,enabled,target,repository_scopes,minimum_severity,categories,
				trigger_first_seen,trigger_regressed,repeat_occurrence_threshold,labels,
				assignee_external_id,title_template,body_template,updated_by,updated_at`,
			tenantID, policy.Revision, policy.Enabled, policy.Target, policy.RepositoryScopes, policy.MinimumSeverity,
			policy.Categories, policy.TriggerFirstSeen, policy.TriggerRegressed, policy.RepeatOccurrenceThreshold,
			policy.Labels, policy.AssigneeExternalID, policy.TitleTemplate, policy.BodyTemplate, actor).
			Scan(&policy.Revision, &policy.Enabled, &policy.Target, &policy.RepositoryScopes, &policy.MinimumSeverity,
				&policy.Categories, &policy.TriggerFirstSeen, &policy.TriggerRegressed, &policy.RepeatOccurrenceThreshold,
				&policy.Labels, &policy.AssigneeExternalID, &policy.TitleTemplate, &policy.BodyTemplate, &policy.UpdatedBy, &policy.UpdatedAt)
	}
	if err != nil {
		return domain.IssueAutoCreatePolicy{}, fmt.Errorf("save issue automation policy: %w", err)
	}
	cancelledReceipts := 0
	if !policy.Enabled {
		if err := tx.QueryRow(ctx, `
			WITH cancelled AS (
				UPDATE external_issue_receipts
				SET state='cancelled',last_error='Policy disabled before provider publication.',updated_at=now()
				WHERE tenant_id=$1 AND state IN ('queued','failed')
				RETURNING 1
			)
			SELECT COUNT(*) FROM cancelled`, tenantID).Scan(&cancelledReceipts); err != nil {
			return domain.IssueAutoCreatePolicy{}, fmt.Errorf("cancel unpublished external issues for disabled policy: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata)
		VALUES ($1,$2,'issue_auto_create_policy.saved','issue-auto-create',
			jsonb_build_object('revision',$3::int,'enabled',$4::boolean,'target',$5::text,
			'repository_scope_count',$6::int,'minimum_severity',$7::text,'repeat_occurrence_threshold',$8::int,
			'cancelled_unpublished_receipts',$9::int))`,
		tenantID, actor, policy.Revision, policy.Enabled, policy.Target, len(policy.RepositoryScopes), policy.MinimumSeverity, policy.RepeatOccurrenceThreshold, cancelledReceipts); err != nil {
		return domain.IssueAutoCreatePolicy{}, fmt.Errorf("audit issue automation policy save: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.IssueAutoCreatePolicy{}, fmt.Errorf("commit issue automation policy save: %w", err)
	}
	policy.CanManage = true
	return policy, nil
}

func (s *PostgresStore) PreviewIssueAutoCreatePolicy(ctx context.Context, actor, tenantSlug string, input domain.IssueAutoCreatePolicy) (domain.IssueAutoCreatePreview, error) {
	policy, valid := domain.NormalizeIssueAutoCreatePolicy(input)
	if !valid {
		return domain.IssueAutoCreatePreview{}, ErrInvalidIssueAction
	}
	tenantID, _, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.IssueAutoCreatePreview{}, err
	}
	now := time.Now().UTC()
	preview := domain.IssueAutoCreatePreview{WindowStart: now.Add(-30 * 24 * time.Hour), WindowEnd: now, Candidates: []domain.IssueAutoCreatePreviewItem{}}
	rows, err := s.pool.Query(ctx, `
		SELECT issue.id, issue.repository, issue.path, issue.severity, issue.category, issue.occurrence_count, issue.status,
		       receipt.id IS NOT NULL
		FROM review_issues issue
		LEFT JOIN external_issue_receipts receipt ON receipt.issue_id = issue.id
		WHERE issue.tenant_id = $1
		  AND issue.status IN ('open','regressed')
		  AND issue.last_seen_at >= $2
		ORDER BY issue.last_seen_at DESC, issue.id DESC
		LIMIT 250`, tenantID, preview.WindowStart)
	if err != nil {
		return domain.IssueAutoCreatePreview{}, fmt.Errorf("preview issue automation policy: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item domain.IssueAutoCreatePreviewItem
		if err := rows.Scan(&item.IssueID, &item.Repository, &item.Path, &item.Severity, &item.Category, &item.OccurrenceCount, &item.Status, &item.AlreadyPublished); err != nil {
			return domain.IssueAutoCreatePreview{}, fmt.Errorf("scan issue automation preview: %w", err)
		}
		if issuePolicyMatches(policy, item.Repository, item.Severity, item.Category) {
			preview.Candidates = append(preview.Candidates, item)
		}
	}
	if err := rows.Err(); err != nil {
		return domain.IssueAutoCreatePreview{}, fmt.Errorf("iterate issue automation preview: %w", err)
	}
	return preview, nil
}

func getIssueAutoCreatePolicy(ctx context.Context, query rowQuerier, tenantID uuid.UUID) (domain.IssueAutoCreatePolicy, bool, error) {
	var policy domain.IssueAutoCreatePolicy
	err := query.QueryRow(ctx, `
		SELECT revision,enabled,target,repository_scopes,minimum_severity,categories,
		       trigger_first_seen,trigger_regressed,repeat_occurrence_threshold,labels,
		       assignee_external_id,title_template,body_template,updated_by,updated_at
		FROM issue_auto_create_policies WHERE tenant_id=$1`, tenantID).
		Scan(&policy.Revision, &policy.Enabled, &policy.Target, &policy.RepositoryScopes, &policy.MinimumSeverity,
			&policy.Categories, &policy.TriggerFirstSeen, &policy.TriggerRegressed, &policy.RepeatOccurrenceThreshold,
			&policy.Labels, &policy.AssigneeExternalID, &policy.TitleTemplate, &policy.BodyTemplate, &policy.UpdatedBy, &policy.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.IssueAutoCreatePolicy{}, false, nil
	}
	if err != nil {
		return domain.IssueAutoCreatePolicy{}, false, fmt.Errorf("load issue automation policy: %w", err)
	}
	normalized, valid := domain.NormalizeIssueAutoCreatePolicy(policy)
	if !valid {
		return domain.IssueAutoCreatePolicy{}, false, fmt.Errorf("stored issue automation policy is invalid")
	}
	return normalized, true, nil
}

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func canManageIssueAutomation(role string) bool { return role == "owner" || role == "admin" }

func issuePolicyMatches(policy domain.IssueAutoCreatePolicy, repository, severity, category string) bool {
	if !policy.Enabled || !severityAtLeast(severity, policy.MinimumSeverity) {
		return false
	}
	if len(policy.RepositoryScopes) == 0 || !matchesRepositoryScope(policy.RepositoryScopes, repository) {
		return false
	}
	if len(policy.Categories) == 0 {
		return true
	}
	category = strings.ToLower(strings.TrimSpace(category))
	for _, allowed := range policy.Categories {
		if category == allowed {
			return true
		}
	}
	return false
}

func matchesRepositoryScope(scopes []string, repository string) bool {
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "*" || scope == repository {
			return true
		}
		if matched, err := path.Match(scope, repository); err == nil && matched {
			return true
		}
	}
	return false
}

func severityAtLeast(actual, minimum string) bool {
	levels := map[string]int{"low": 1, "medium": 2, "high": 3, "critical": 4}
	return levels[strings.ToLower(strings.TrimSpace(actual))] >= levels[strings.ToLower(strings.TrimSpace(minimum))]
}

type issueAutomationPrior struct {
	Exists          bool
	Status          domain.IssueStatus
	OccurrenceCount int
}

// queueExternalIssueResolution mirrors a newly resolved internal aggregate to
// the exact provider Issue stored in its receipt. It deliberately queues the
// close even while creation is pending: both messages share one durable queue,
// and a close that reaches the worker before an external ID exists is retried
// until the create receipt becomes durable.
func queueExternalIssueResolution(ctx context.Context, tx pgx.Tx, tenantID, issueID uuid.UUID, prior issueAutomationPrior) error {
	if !prior.Exists || prior.Status == domain.IssueResolved {
		return nil
	}
	var status domain.IssueStatus
	var revision int
	if err := tx.QueryRow(ctx, `SELECT status,revision FROM review_issues WHERE id=$1 AND tenant_id=$2`, issueID, tenantID).Scan(&status, &revision); err != nil {
		return fmt.Errorf("load issue resolution state: %w", err)
	}
	if status != domain.IssueResolved {
		return nil
	}
	var receiptID uuid.UUID
	var receiptState domain.ExternalIssuePublicationState
	err := tx.QueryRow(ctx, `
		SELECT id,state FROM external_issue_receipts
		WHERE issue_id=$1 AND tenant_id=$2`, issueID, tenantID).Scan(&receiptID, &receiptState)
	if errors.Is(err, pgx.ErrNoRows) || receiptState == domain.ExternalIssuePublicationCancelled || receiptState == domain.ExternalIssuePublicationClosed {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load external issue resolution receipt: %w", err)
	}
	dedupeKey := fmt.Sprintf("external-issue:%s:close:%d", receiptID, revision)
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_messages (aggregate_type,aggregate_id,topic,dedupe_key,payload)
		VALUES ('external_issue',$1,'external.issue.close',$2,$3::jsonb)
		ON CONFLICT (dedupe_key) DO NOTHING`, receiptID, dedupeKey, jsonPayload(map[string]any{"receipt_id": receiptID.String()})); err != nil {
		return fmt.Errorf("queue external issue close: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata)
		VALUES ($1,'system','external_issue.close_queued',$2,jsonb_build_object('receipt_id',$3::text,'issue_revision',$4::int))`,
		tenantID, issueID.String(), receiptID.String(), revision); err != nil {
		return fmt.Errorf("audit external issue close queue: %w", err)
	}
	return nil
}

func queueExternalIssuePublication(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, issueID uuid.UUID, prior issueAutomationPrior) error {
	policy, found, err := getIssueAutoCreatePolicy(ctx, tx, tenantID)
	if err != nil || !found || !policy.Enabled {
		return err
	}
	var issue struct {
		Provider        domain.Provider
		APIBaseURL      string
		Repository      string
		Fingerprint     string
		Path            string
		Severity        string
		Category        string
		Status          domain.IssueStatus
		OccurrenceCount int
		Evidence        string
		Suggestion      string
		ReviewNumber    int
		RunID           uuid.UUID
		HeadSHA         string
		StartLine       int
		EndLine         int
		InstallationID  uuid.UUID
	}
	err = tx.QueryRow(ctx, `
		SELECT issue.provider,issue.api_base_url,issue.repository,issue.fingerprint,issue.path,issue.severity,issue.category,
		       issue.status,issue.occurrence_count,
		       COALESCE(finding.body,issue.body_preview),COALESCE(finding.suggestion,''),COALESCE(occurrence.review_number,0),
		       COALESCE(occurrence.run_id,'00000000-0000-0000-0000-000000000000'::uuid),COALESCE(occurrence.head_sha,''),
		       COALESCE(finding.start_line,0),COALESCE(finding.end_line,0),job.installation_id
		FROM review_issues issue
		LEFT JOIN LATERAL (
			SELECT occurrence.finding_id,occurrence.review_number,occurrence.job_id,occurrence.run_id,occurrence.head_sha
			FROM review_issue_occurrences occurrence
			WHERE occurrence.issue_id=issue.id
			ORDER BY occurrence.active DESC,occurrence.created_at DESC,occurrence.id DESC LIMIT 1
		) occurrence ON TRUE
		LEFT JOIN review_findings finding ON finding.id=occurrence.finding_id
		LEFT JOIN review_jobs job ON job.id=occurrence.job_id
		WHERE issue.id=$1 AND issue.tenant_id=$2 FOR UPDATE OF issue`, issueID, tenantID).
		Scan(&issue.Provider, &issue.APIBaseURL, &issue.Repository, &issue.Fingerprint, &issue.Path, &issue.Severity,
			&issue.Category, &issue.Status, &issue.OccurrenceCount, &issue.Evidence, &issue.Suggestion, &issue.ReviewNumber,
			&issue.RunID, &issue.HeadSHA, &issue.StartLine, &issue.EndLine, &issue.InstallationID)
	if err != nil {
		return fmt.Errorf("load issue for external publication: %w", err)
	}
	if issue.Status != domain.IssueOpen && issue.Status != domain.IssueRegressed || !issuePolicyMatches(policy, issue.Repository, issue.Severity, issue.Category) {
		return nil
	}
	trigger := ""
	if !prior.Exists && policy.TriggerFirstSeen {
		trigger = "first_seen"
	} else if prior.Status == domain.IssueResolved && issue.Status == domain.IssueRegressed && policy.TriggerRegressed {
		trigger = "regressed"
	} else if policy.RepeatOccurrenceThreshold > 0 && prior.OccurrenceCount < policy.RepeatOccurrenceThreshold && issue.OccurrenceCount >= policy.RepeatOccurrenceThreshold {
		trigger = "repeated"
	}
	if trigger == "" {
		return nil
	}
	var externalID, credentialRef string
	err = tx.QueryRow(ctx, `
		SELECT external_id,credential_ref FROM provider_installations
		WHERE id=$1 AND tenant_id=$2 AND active=TRUE AND verification_state IN ('legacy','verified')`, issue.InstallationID, tenantID).
		Scan(&externalID, &credentialRef)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // connection was explicitly stopped or has not passed verification.
	}
	if err != nil {
		return fmt.Errorf("load verified issue publication installation: %w", err)
	}
	marker := "open-review-platform:external-issue:" + issueID.String()
	reviewURL := providerReviewURL(issue.Provider, issue.APIBaseURL, issue.Repository, issue.ReviewNumber)
	fileURL := providerFileURL(issue.Provider, issue.APIBaseURL, issue.Repository, issue.HeadSHA, issue.Path, issue.StartLine, issue.EndLine)
	fileLabel := issue.Path
	if issue.StartLine > 0 {
		fileLabel = fmt.Sprintf("%s:%d", fileLabel, issue.StartLine)
	}
	fileLink := "`" + fileLabel + "`"
	if fileURL != "" {
		fileLink = "[" + fileLink + "](" + fileURL + ")"
	}
	reviewLink := fmt.Sprintf("#%d", issue.ReviewNumber)
	if reviewURL != "" {
		reviewLink = fmt.Sprintf("[#%d](%s)", issue.ReviewNumber, reviewURL)
	}
	suggestion := strings.TrimSpace(issue.Suggestion)
	if suggestion == "" {
		suggestion = "Address the root cause described above, add a focused regression test, and rerun Open Review on the updated commit."
	}
	values := map[string]string{
		"issue_id":         issueID.String(),
		"repository":       issue.Repository,
		"path":             issue.Path,
		"severity":         issue.Severity,
		"severity_upper":   strings.ToUpper(issue.Severity),
		"category":         issue.Category,
		"fingerprint":      issue.Fingerprint,
		"occurrence_count": fmt.Sprintf("%d", issue.OccurrenceCount),
		"review_number":    fmt.Sprintf("%d", issue.ReviewNumber),
		"review_link":      reviewLink,
		"file_link":        fileLink,
		"head_sha":         issue.HeadSHA,
		"run_id":           issue.RunID.String(),
		"evidence":         issue.Evidence,
		"suggestion":       suggestion,
	}
	title := renderIssueTemplate(policy.TitleTemplate, values)
	body := renderIssueTemplate(policy.BodyTemplate, values) + "\n\n<!-- " + marker + " -->"
	var receiptID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO external_issue_receipts (
			tenant_id,issue_id,installation_id,policy_revision,provider,api_base_url,repository,trigger,stable_marker,
			title,body,labels,assignee_external_id
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT (issue_id) DO NOTHING
		RETURNING id`, tenantID, issueID, issue.InstallationID, policy.Revision, issue.Provider, issue.APIBaseURL, issue.Repository, trigger, marker,
		title, body, policy.Labels, policy.AssigneeExternalID).Scan(&receiptID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // A prior policy-triggered Issue is the single active ticket for this fingerprint.
	}
	if err != nil {
		return fmt.Errorf("create external issue receipt: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata)
		VALUES ($1,'system','external_issue.queued',$2,jsonb_build_object('provider',$3::text,'repository',$4::text,'trigger',$5::text,'policy_revision',$6::int))`,
		tenantID, issueID.String(), string(issue.Provider), issue.Repository, trigger, policy.Revision); err != nil {
		return fmt.Errorf("audit external issue queued: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_messages (aggregate_type,aggregate_id,topic,dedupe_key,payload)
		VALUES ('external_issue',$1,'external.issue.create',$2,$3::jsonb)
		ON CONFLICT (dedupe_key) DO NOTHING`, receiptID, "external-issue:"+receiptID.String(), jsonPayload(map[string]any{"receipt_id": receiptID.String()})); err != nil {
		return fmt.Errorf("queue external issue outbox message: %w", err)
	}
	return nil
}

func renderIssueTemplate(template string, values map[string]string) string {
	for key, value := range values {
		template = strings.ReplaceAll(template, "{{"+key+"}}", strings.TrimSpace(value))
	}
	return strings.TrimSpace(template)
}

func providerFileURL(provider domain.Provider, apiBaseURL, repository, headSHA, filePath string, startLine, endLine int) string {
	reviewURL := providerReviewURL(provider, apiBaseURL, repository, 1)
	if reviewURL == "" || strings.TrimSpace(headSHA) == "" || strings.TrimSpace(filePath) == "" {
		return ""
	}
	base := ""
	switch provider {
	case domain.ProviderGitHub:
		base = strings.TrimSuffix(reviewURL, "/pull/1") + "/blob/" + url.PathEscape(headSHA)
	case domain.ProviderGitLab:
		base = strings.TrimSuffix(reviewURL, "/-/merge_requests/1") + "/-/blob/" + url.PathEscape(headSHA)
	default:
		return ""
	}
	parts := strings.Split(strings.Trim(filePath, "/"), "/")
	for index := range parts {
		parts[index] = url.PathEscape(parts[index])
	}
	link := base + "/" + strings.Join(parts, "/")
	if startLine > 0 {
		link += fmt.Sprintf("#L%d", startLine)
		if endLine > startLine {
			link += fmt.Sprintf("-L%d", endLine)
		}
	}
	return link
}

func (s *PostgresStore) ExternalIssuePublication(ctx context.Context, receiptID uuid.UUID) (domain.ExternalIssuePublication, error) {
	if receiptID == uuid.Nil {
		return domain.ExternalIssuePublication{}, ErrNotFound
	}
	var publication domain.ExternalIssuePublication
	err := s.pool.QueryRow(ctx, `
		SELECT receipt.id,receipt.issue_id,receipt.provider,receipt.repository,receipt.trigger,receipt.state,
		       receipt.external_id,receipt.external_url,receipt.attempts,receipt.last_error,receipt.created_at,receipt.updated_at,
		       receipt.tenant_id,receipt.api_base_url,installation.external_id,installation.credential_ref,receipt.stable_marker,
		       receipt.title,receipt.body,receipt.labels,receipt.assignee_external_id
		FROM external_issue_receipts receipt
		JOIN review_issues issue ON issue.id=receipt.issue_id AND issue.tenant_id=receipt.tenant_id
		JOIN issue_auto_create_policies policy ON policy.tenant_id=receipt.tenant_id AND policy.enabled=TRUE
		JOIN provider_installations installation ON installation.id=receipt.installation_id AND installation.tenant_id=receipt.tenant_id
			AND installation.active=TRUE AND installation.verification_state IN ('legacy','verified')
		WHERE receipt.id=$1 AND receipt.state <> 'cancelled'`, receiptID).
		Scan(&publication.ID, &publication.IssueID, &publication.Provider, &publication.Repository, &publication.Trigger,
			&publication.State, &publication.ExternalID, &publication.ExternalURL, &publication.Attempts, &publication.LastError,
			&publication.CreatedAt, &publication.UpdatedAt, &publication.TenantID, &publication.APIBaseURL, &publication.InstallationExternalID,
			&publication.CredentialRef, &publication.Marker, &publication.Title, &publication.Body, &publication.Labels,
			&publication.AssigneeExternalID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ExternalIssuePublication{}, ErrNotFound
	}
	if err != nil {
		return domain.ExternalIssuePublication{}, fmt.Errorf("load external issue publication: %w", err)
	}
	return publication, nil
}

func (s *PostgresStore) MarkExternalIssuePublicationCreated(ctx context.Context, receiptID uuid.UUID, externalID, externalURL string) error {
	externalID, externalURL = strings.TrimSpace(externalID), strings.TrimSpace(externalURL)
	if receiptID == uuid.Nil || externalID == "" || len(externalID) > 512 || len(externalURL) > 2048 {
		return ErrInvalidIssueAction
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin external issue completion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var tenantID, issueID uuid.UUID
	err = tx.QueryRow(ctx, `
		UPDATE external_issue_receipts SET state='created',external_id=$2,external_url=$3,last_error='',attempts=attempts+1,updated_at=now()
		WHERE id=$1 AND state IN ('queued','failed') RETURNING tenant_id,issue_id`, receiptID, externalID, externalURL).Scan(&tenantID, &issueID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // duplicate/replayed broker message already reached a durable receipt.
	}
	if err != nil {
		return fmt.Errorf("complete external issue receipt: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata)
		VALUES ($1,'system','external_issue.created',$2,jsonb_build_object('external_id',$3::text,'external_url',$4::text))`, tenantID, issueID.String(), externalID, externalURL); err != nil {
		return fmt.Errorf("audit external issue created: %w", err)
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) MarkExternalIssuePublicationFailed(ctx context.Context, receiptID uuid.UUID, message string) error {
	message = strings.TrimSpace(message)
	if receiptID == uuid.Nil || message == "" {
		return ErrInvalidIssueAction
	}
	if len(message) > 500 {
		message = message[:500]
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE external_issue_receipts SET state='failed',last_error=$2,attempts=attempts+1,updated_at=now()
		WHERE id=$1 AND state IN ('queued','failed')`, receiptID, message)
	if err != nil {
		return fmt.Errorf("mark external issue receipt failed: %w", err)
	}
	return nil
}

func (s *PostgresStore) MarkExternalIssuePublicationClosed(ctx context.Context, receiptID uuid.UUID) error {
	if receiptID == uuid.Nil {
		return ErrInvalidIssueAction
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin external issue closure: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var tenantID, issueID uuid.UUID
	err = tx.QueryRow(ctx, `
		UPDATE external_issue_receipts
		SET state='closed',last_error='',attempts=attempts+1,updated_at=now()
		WHERE id=$1 AND state='created'
		RETURNING tenant_id,issue_id`, receiptID).Scan(&tenantID, &issueID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("complete external issue closure: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata)
		VALUES ($1,'system','external_issue.closed',$2,jsonb_build_object('receipt_id',$3::text))`, tenantID, issueID.String(), receiptID.String()); err != nil {
		return fmt.Errorf("audit external issue closure: %w", err)
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) MarkExternalIssueSyncFailed(ctx context.Context, receiptID uuid.UUID, message string) error {
	message = strings.TrimSpace(message)
	if receiptID == uuid.Nil || message == "" {
		return ErrInvalidIssueAction
	}
	if len(message) > 500 {
		message = message[:500]
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE external_issue_receipts
		SET last_error=$2,attempts=attempts+1,updated_at=now()
		WHERE id=$1 AND state NOT IN ('closed','cancelled')`, receiptID, message)
	if err != nil {
		return fmt.Errorf("record external issue sync failure: %w", err)
	}
	return nil
}

func (s *PostgresStore) CancelExternalIssuePublication(ctx context.Context, receiptID uuid.UUID, reason string) error {
	reason = strings.TrimSpace(reason)
	if receiptID == uuid.Nil || reason == "" {
		return ErrInvalidIssueAction
	}
	if len(reason) > 500 {
		reason = reason[:500]
	}
	var tenantID, issueID uuid.UUID
	err := s.pool.QueryRow(ctx, `
		UPDATE external_issue_receipts
		SET state='cancelled',last_error=$2,updated_at=now()
		WHERE id=$1 AND state IN ('queued','failed')
		RETURNING tenant_id,issue_id`, receiptID, reason).Scan(&tenantID, &issueID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cancel external issue receipt: %w", err)
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata)
		VALUES ($1,'system','external_issue.cancelled',$2,jsonb_build_object('reason',$3::text))`, tenantID, issueID.String(), reason); err != nil {
		return fmt.Errorf("audit external issue cancellation: %w", err)
	}
	return nil
}
