package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The provider Issue queue has x-delivery-limit=5. The initial delivery plus
// five redeliveries leaves six released inbox claims when RabbitMQ dead-letters
// the message. Only a failed delivery for the current immutable attempt may
// be offered for manual recovery; transient redeliveries are not retryable.
const providerIssueTerminalDeliveryFailureSQL = `SELECT EXISTS (
	SELECT 1 FROM outbox_messages outgoing
	JOIN inbox_messages incoming ON incoming.message_id=outgoing.id
	WHERE outgoing.aggregate_id=$1 AND outgoing.topic=$4
	  AND outgoing.payload->>'revision'=$2::integer::text
	  AND outgoing.payload->>'attempt'=$3::integer::text
	  AND incoming.consumer='provider-issue-triager-v1'
	  AND incoming.state='released' AND incoming.attempt>=$5
	  AND incoming.last_error<>''
)`

const providerIssueSkippedDeliverySQL = `SELECT EXISTS (
	SELECT 1 FROM outbox_messages outgoing
	JOIN inbox_messages incoming ON incoming.message_id=outgoing.id
	WHERE outgoing.aggregate_id=$1 AND outgoing.topic=$4
	  AND outgoing.payload->>'revision'=$2::integer::text
	  AND outgoing.payload->>'attempt'=$3::integer::text
	  AND incoming.consumer='provider-issue-triager-v1'
	  AND incoming.state='completed'
)`

func providerIssuePendingTopic(state domain.ProviderIssueAnalysisState) string {
	switch state {
	case domain.ProviderIssueAnalysisQueued:
		return "provider.issue.acknowledge"
	case domain.ProviderIssueAnalysisAcknowledged:
		return "provider.issue.analyze"
	default:
		return ""
	}
}

func canRetryProviderIssue(role string) bool {
	return role == "owner" || role == "admin" || role == "rule_admin" || role == "reviewer"
}

func (s *PostgresStore) ListProviderIssueAnalyses(ctx context.Context, actor, tenantSlug string, filter domain.ProviderIssueAnalysisFilter) (domain.ProviderIssueAnalysisPage, error) {
	if !filter.Valid() {
		return domain.ProviderIssueAnalysisPage{}, ErrInvalidIssueFilter
	}
	tenantID, _, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.ProviderIssueAnalysisPage{}, err
	}
	var counts domain.ProviderIssueAnalysisCounts
	if err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FILTER (WHERE state='queued'),
		       COUNT(*) FILTER (WHERE state='acknowledged'),
		       COUNT(*) FILTER (WHERE state='completed'),
		       COUNT(*) FILTER (WHERE state='failed')
		FROM provider_issue_analysis_jobs WHERE tenant_id=$1`, tenantID).Scan(
		&counts.Queued, &counts.Acknowledged, &counts.Completed, &counts.Failed); err != nil {
		return domain.ProviderIssueAnalysisPage{}, fmt.Errorf("count provider issue analyses: %w", err)
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FILTER (WHERE delivery.exhausted),
		       COUNT(*) FILTER (WHERE skipped.completed),
		       COUNT(*) FILTER (WHERE connection.blocked),
		       COUNT(*) FILTER (WHERE job.state='failed' OR delivery.exhausted OR skipped.completed OR connection.blocked)
		FROM provider_issue_analysis_jobs job
		LEFT JOIN provider_installations installation
		  ON installation.id=job.installation_id AND installation.tenant_id=job.tenant_id
		CROSS JOIN LATERAL (SELECT job.state IN ('queued','acknowledged')
		  AND NOT COALESCE(installation.active AND installation.verification_state IN ('legacy','verified'), FALSE) AS blocked) connection
		CROSS JOIN LATERAL (SELECT job.state IN ('queued','acknowledged') AND EXISTS (
			SELECT 1 FROM outbox_messages outgoing
			JOIN inbox_messages incoming ON incoming.message_id=outgoing.id
			WHERE outgoing.aggregate_id=job.id
			  AND outgoing.topic=CASE job.state WHEN 'queued' THEN 'provider.issue.acknowledge' ELSE 'provider.issue.analyze' END
			  AND outgoing.payload->>'revision'=job.revision::text
			  AND outgoing.payload->>'attempt'=job.analysis_attempt::text
			  AND incoming.consumer='provider-issue-triager-v1'
			  AND incoming.state='released' AND incoming.attempt >= $2
			  AND incoming.last_error<>''
		  ) AS exhausted) delivery
		CROSS JOIN LATERAL (SELECT job.state IN ('queued','acknowledged') AND EXISTS (
			SELECT 1 FROM outbox_messages outgoing
			JOIN inbox_messages incoming ON incoming.message_id=outgoing.id
			WHERE outgoing.aggregate_id=job.id
			  AND outgoing.topic=CASE job.state WHEN 'queued' THEN 'provider.issue.acknowledge' ELSE 'provider.issue.analyze' END
			  AND outgoing.payload->>'revision'=job.revision::text
			  AND outgoing.payload->>'attempt'=job.analysis_attempt::text
			  AND incoming.consumer='provider-issue-triager-v1'
			  AND incoming.state='completed'
		  ) AS completed) skipped
		WHERE job.tenant_id=$1`, tenantID, domain.QuorumQueueRedeliveryLimit+1).Scan(
		&counts.DeliveryFailures, &counts.SkippedDeliveries, &counts.ConnectionBlocked, &counts.NeedsAttention); err != nil {
		return domain.ProviderIssueAnalysisPage{}, fmt.Errorf("count provider issue attention conditions: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT job.id,job.installation_id,job.provider,job.api_base_url,job.repository,
		       job.issue_number,job.revision,job.analysis_attempt,job.action,job.title,job.author,job.labels,
		       job.state,job.last_error,
		       delivery.exhausted,skipped.completed,connection.blocked,COUNT(receipt.delivery_id),job.created_at,job.updated_at,
		       job.completed_at,job.model_route_sha256,job.prompt_config_sha256,job.issue_triage_config_sha256
		FROM provider_issue_analysis_jobs job
		LEFT JOIN provider_installations installation
		  ON installation.id=job.installation_id AND installation.tenant_id=job.tenant_id
		CROSS JOIN LATERAL (SELECT job.state IN ('queued','acknowledged')
		  AND NOT COALESCE(installation.active AND installation.verification_state IN ('legacy','verified'), FALSE) AS blocked) connection
		CROSS JOIN LATERAL (SELECT job.state IN ('queued','acknowledged') AND EXISTS (
		         SELECT 1 FROM outbox_messages outgoing
		         JOIN inbox_messages incoming ON incoming.message_id=outgoing.id
		         WHERE outgoing.aggregate_id=job.id
		           AND outgoing.topic=CASE job.state WHEN 'queued' THEN 'provider.issue.acknowledge' ELSE 'provider.issue.analyze' END
		           AND outgoing.payload->>'revision'=job.revision::text
		           AND outgoing.payload->>'attempt'=job.analysis_attempt::text
		           AND incoming.consumer='provider-issue-triager-v1'
		           AND incoming.state='released' AND incoming.attempt >= $5
		           AND incoming.last_error<>''
		       ) AS exhausted) delivery
		CROSS JOIN LATERAL (SELECT job.state IN ('queued','acknowledged') AND EXISTS (
		         SELECT 1 FROM outbox_messages outgoing
		         JOIN inbox_messages incoming ON incoming.message_id=outgoing.id
		         WHERE outgoing.aggregate_id=job.id
		           AND outgoing.topic=CASE job.state WHEN 'queued' THEN 'provider.issue.acknowledge' ELSE 'provider.issue.analyze' END
		           AND outgoing.payload->>'revision'=job.revision::text
		           AND outgoing.payload->>'attempt'=job.analysis_attempt::text
		           AND incoming.consumer='provider-issue-triager-v1'
		           AND incoming.state='completed'
		       ) AS completed) skipped
		LEFT JOIN provider_issue_analysis_receipts receipt ON receipt.job_id=job.id
		WHERE job.tenant_id=$1
		  AND ($2='' OR job.state=$2 OR ($2='needs_attention' AND (job.state='failed' OR delivery.exhausted OR skipped.completed OR connection.blocked)))
		  AND ($3='' OR lower(concat_ws(' ',job.repository,job.title,job.author,job.issue_number::text)) LIKE '%%' || lower($3) || '%%')
		GROUP BY job.id,delivery.exhausted,skipped.completed,connection.blocked
		ORDER BY job.updated_at DESC,job.id DESC
		LIMIT $4`, tenantID, filter.State, strings.TrimSpace(filter.Query), filter.Limit, domain.QuorumQueueRedeliveryLimit+1)
	if err != nil {
		return domain.ProviderIssueAnalysisPage{}, fmt.Errorf("list provider issue analyses: %w", err)
	}
	defer rows.Close()
	items := make([]domain.ProviderIssueAnalysisSummary, 0)
	for rows.Next() {
		var item domain.ProviderIssueAnalysisSummary
		if err := rows.Scan(&item.ID, &item.InstallationID, &item.Provider, &item.APIBaseURL,
			&item.Repository, &item.IssueNumber, &item.Revision, &item.AnalysisAttempt, &item.Action, &item.Title,
			&item.Author, &item.Labels, &item.State, &item.LastError, &item.DeliveryFailure, &item.SkippedDelivery, &item.ConnectionBlocked, &item.ReceiptCount,
			&item.CreatedAt, &item.UpdatedAt, &item.CompletedAt, &item.ModelRouteSHA256,
			&item.PromptConfigSHA256, &item.IssueTriageConfigSHA256); err != nil {
			return domain.ProviderIssueAnalysisPage{}, fmt.Errorf("scan provider issue analysis: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return domain.ProviderIssueAnalysisPage{}, fmt.Errorf("iterate provider issue analyses: %w", err)
	}
	return domain.ProviderIssueAnalysisPage{Items: items, Counts: counts}, nil
}

func (s *PostgresStore) GetProviderIssueAnalysis(ctx context.Context, actor, tenantSlug string, analysisID uuid.UUID) (domain.ProviderIssueAnalysisDetail, error) {
	tenantID, role, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.ProviderIssueAnalysisDetail{}, err
	}
	var detail domain.ProviderIssueAnalysisDetail
	var retainedBody string
	err = s.pool.QueryRow(ctx, `
		SELECT job.id,job.installation_id,job.provider,job.api_base_url,job.repository,
		       job.issue_number,job.revision,job.analysis_attempt,job.action,job.title,job.body,job.author,job.labels,
		       job.state,job.last_error,(SELECT COUNT(*) FROM provider_issue_analysis_receipts receipt WHERE receipt.job_id=job.id),
		       job.created_at,job.updated_at,job.completed_at,job.model_route_sha256,
		       job.prompt_config_sha256,job.issue_triage_config_sha256,
		       CASE WHEN job.state='completed' THEN job.analysis ELSE '' END
		FROM provider_issue_analysis_jobs job
		WHERE job.tenant_id=$1 AND job.id=$2`, tenantID, analysisID).Scan(
		&detail.ID, &detail.InstallationID, &detail.Provider, &detail.APIBaseURL,
		&detail.Repository, &detail.IssueNumber, &detail.Revision, &detail.AnalysisAttempt, &detail.Action,
		&detail.Title, &retainedBody, &detail.Author, &detail.Labels, &detail.State, &detail.LastError,
		&detail.ReceiptCount, &detail.CreatedAt, &detail.UpdatedAt, &detail.CompletedAt,
		&detail.ModelRouteSHA256, &detail.PromptConfigSHA256, &detail.IssueTriageConfigSHA256, &detail.Analysis)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProviderIssueAnalysisDetail{}, ErrNotFound
	}
	if err != nil {
		return domain.ProviderIssueAnalysisDetail{}, fmt.Errorf("get provider issue analysis: %w", err)
	}
	detail.RetryReadiness.RoleAllowed = canRetryProviderIssue(role)
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM provider_installations
			WHERE id=$1 AND tenant_id=$2 AND active=TRUE
			  AND verification_state IN ('legacy','verified'))`, detail.InstallationID, tenantID).Scan(&detail.RetryReadiness.InstallationReady); err != nil {
		return domain.ProviderIssueAnalysisDetail{}, fmt.Errorf("check provider Issue retry installation: %w", err)
	}
	detail.ConnectionBlocked = (detail.State == domain.ProviderIssueAnalysisQueued || detail.State == domain.ProviderIssueAnalysisAcknowledged) && !detail.RetryReadiness.InstallationReady
	detail.RetryReadiness.SetupReady, err = workspaceSetupAllowsReview(ctx, s.pool, tenantID)
	if err != nil {
		return domain.ProviderIssueAnalysisDetail{}, fmt.Errorf("check provider Issue retry setup: %w", err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE((
		SELECT mode FROM agent_task_policies
		WHERE tenant_id=$1 AND provider=$2 AND api_base_url=$3 AND repository=$4
	), 'disabled')`, tenantID, detail.Provider, detail.APIBaseURL, detail.Repository).Scan(&detail.AgentAdmission.PolicyMode); err != nil {
		return domain.ProviderIssueAnalysisDetail{}, fmt.Errorf("load provider Issue Agent policy: %w", err)
	}
	issueRevisions := []string{
		domain.AgentIssueRevision(detail.Provider, detail.APIBaseURL, detail.Repository, detail.IssueNumber, detail.Title, retainedBody),
		domain.AgentAutomaticIssueRevision(detail.Provider, detail.APIBaseURL, detail.Repository, detail.IssueNumber, detail.Title, retainedBody, detail.Labels),
	}
	taskRows, err := s.pool.Query(ctx, `
		SELECT id,state FROM agent_tasks
		WHERE tenant_id=$1 AND installation_id=$2 AND provider=$3 AND api_base_url=$4
		  AND repository=$5 AND origin_kind='issue' AND origin_number=$6
		  AND origin_revision=ANY($7::text[]) AND intent='implement'
		ORDER BY created_at DESC,id DESC LIMIT 2`, tenantID, detail.InstallationID,
		detail.Provider, detail.APIBaseURL, detail.Repository, detail.IssueNumber,
		issueRevisions)
	if err != nil {
		return domain.ProviderIssueAnalysisDetail{}, fmt.Errorf("load provider Issue Agent task: %w", err)
	}
	for taskRows.Next() {
		var task domain.ProviderIssueAgentTask
		if err := taskRows.Scan(&task.ID, &task.State); err != nil {
			taskRows.Close()
			return domain.ProviderIssueAnalysisDetail{}, fmt.Errorf("scan provider Issue Agent task: %w", err)
		}
		detail.AgentAdmission.ExistingTasks = append(detail.AgentAdmission.ExistingTasks, task)
	}
	if len(detail.AgentAdmission.ExistingTasks) == 1 {
		task := detail.AgentAdmission.ExistingTasks[0]
		detail.AgentAdmission.ExistingTaskID = &task.ID
		detail.AgentAdmission.ExistingTaskState = task.State
	} else if len(detail.AgentAdmission.ExistingTasks) > 1 {
		detail.AgentAdmission.ExistingTaskConflict = true
	}
	if err := taskRows.Err(); err != nil {
		taskRows.Close()
		return domain.ProviderIssueAnalysisDetail{}, fmt.Errorf("iterate provider Issue Agent tasks: %w", err)
	}
	taskRows.Close()
	installations, err := s.pool.Query(ctx, `
		SELECT id,repository_scope FROM provider_installations
		WHERE tenant_id=$1 AND provider=$2 AND api_base_url=$3 AND active=TRUE
		  AND verification_state IN ('legacy','verified')`, tenantID, detail.Provider, detail.APIBaseURL)
	if err != nil {
		return domain.ProviderIssueAnalysisDetail{}, fmt.Errorf("load provider Issue installations: %w", err)
	}
	var matchingInstallation uuid.UUID
	var matchingCount int
	for installations.Next() {
		var installationID uuid.UUID
		var repositoryScope string
		if err := installations.Scan(&installationID, &repositoryScope); err != nil {
			installations.Close()
			return domain.ProviderIssueAnalysisDetail{}, fmt.Errorf("scan provider Issue installation: %w", err)
		}
		if repositoryScopeAllows(repositoryScope, detail.Repository) {
			matchingInstallation = installationID
			matchingCount++
		}
	}
	if err := installations.Err(); err != nil {
		installations.Close()
		return domain.ProviderIssueAnalysisDetail{}, fmt.Errorf("iterate provider Issue installations: %w", err)
	}
	installations.Close()
	detail.AgentAdmission.InstallationReady = matchingCount == 1 && matchingInstallation == detail.InstallationID
	detail.AgentAdmission.CanRequest = canManageAgentTasks(role) && detail.AgentAdmission.PolicyMode == "manual" && detail.AgentAdmission.InstallationReady && detail.AgentAdmission.ExistingTaskID == nil && !detail.AgentAdmission.ExistingTaskConflict
	if topic := providerIssuePendingTopic(detail.State); topic != "" {
		if err := s.pool.QueryRow(ctx, providerIssueTerminalDeliveryFailureSQL, detail.ID, detail.Revision, detail.AnalysisAttempt, topic, domain.QuorumQueueRedeliveryLimit+1).Scan(&detail.DeliveryFailure); err != nil {
			return domain.ProviderIssueAnalysisDetail{}, fmt.Errorf("check provider issue delivery failure: %w", err)
		}
		if err := s.pool.QueryRow(ctx, providerIssueSkippedDeliverySQL, detail.ID, detail.Revision, detail.AnalysisAttempt, topic).Scan(&detail.SkippedDelivery); err != nil {
			return domain.ProviderIssueAnalysisDetail{}, fmt.Errorf("check provider issue skipped delivery: %w", err)
		}
	}
	detail.RetryReadiness.CanRetry = detail.RetryReadiness.RoleAllowed && detail.RetryReadiness.InstallationReady && detail.RetryReadiness.SetupReady && detail.AnalysisAttempt < 100 &&
		(detail.State == domain.ProviderIssueAnalysisFailed || detail.DeliveryFailure || detail.SkippedDelivery)
	receipts, err := s.pool.Query(ctx, `
		SELECT receipt.revision,receipt.action,delivery.event_name,receipt.admitted_at
		FROM provider_issue_analysis_receipts receipt
		JOIN webhook_deliveries delivery ON delivery.id=receipt.delivery_id
		WHERE receipt.tenant_id=$1 AND receipt.job_id=$2
		ORDER BY receipt.revision DESC,receipt.admitted_at DESC`, tenantID, analysisID)
	if err != nil {
		return domain.ProviderIssueAnalysisDetail{}, fmt.Errorf("list provider issue analysis receipts: %w", err)
	}
	defer receipts.Close()
	detail.Receipts = make([]domain.ProviderIssueAnalysisReceipt, 0)
	for receipts.Next() {
		var receipt domain.ProviderIssueAnalysisReceipt
		if err := receipts.Scan(&receipt.Revision, &receipt.Action, &receipt.EventName, &receipt.AdmittedAt); err != nil {
			return domain.ProviderIssueAnalysisDetail{}, fmt.Errorf("scan provider issue analysis receipt: %w", err)
		}
		detail.Receipts = append(detail.Receipts, receipt)
	}
	if err := receipts.Err(); err != nil {
		return domain.ProviderIssueAnalysisDetail{}, fmt.Errorf("iterate provider issue analysis receipts: %w", err)
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FILTER (WHERE kind='useful' AND retracted_at IS NULL),
		       COUNT(*) FILTER (WHERE kind='not_useful' AND retracted_at IS NULL)
		FROM provider_issue_analysis_feedback
		WHERE tenant_id=$1 AND job_id=$2`, tenantID, analysisID).Scan(&detail.UsefulCount, &detail.NotUsefulCount); err != nil {
		return domain.ProviderIssueAnalysisDetail{}, fmt.Errorf("count provider issue analysis feedback: %w", err)
	}
	var feedbackSync domain.ProviderIssueFeedbackSync
	err = s.pool.QueryRow(ctx, `
		SELECT state,attempt,observed_at,reaction_count,last_error,available_at
		FROM provider_issue_feedback_polls WHERE job_id=$1`, analysisID).Scan(
		&feedbackSync.State, &feedbackSync.Attempt, &feedbackSync.ObservedAt,
		&feedbackSync.ReactionCount, &feedbackSync.LastError, &feedbackSync.NextPollAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return domain.ProviderIssueAnalysisDetail{}, fmt.Errorf("load provider issue feedback sync: %w", err)
	}
	if err == nil {
		detail.FeedbackSync = &feedbackSync
	}
	return detail, nil
}

func (s *PostgresStore) RetryProviderIssueAnalysis(ctx context.Context, actor, tenantSlug string, analysisID uuid.UUID, input domain.ProviderIssueAnalysisRetryInput) (domain.ProviderIssueAnalysisRetryResult, error) {
	input, valid := domain.NormalizeProviderIssueAnalysisRetryInput(input)
	if !valid || analysisID == uuid.Nil {
		return domain.ProviderIssueAnalysisRetryResult{}, ErrInvalidProviderIssueRetry
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.ProviderIssueAnalysisRetryResult{}, fmt.Errorf("begin provider issue analysis retry: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.ProviderIssueAnalysisRetryResult{}, err
	}
	if !canRetryProviderIssue(role) {
		return domain.ProviderIssueAnalysisRetryResult{}, ErrForbidden
	}
	setupComplete, err := workspaceSetupAllowsReview(ctx, tx, tenantID)
	if err != nil {
		return domain.ProviderIssueAnalysisRetryResult{}, err
	}
	if !setupComplete {
		return domain.ProviderIssueAnalysisRetryResult{}, ErrWorkspaceSetupIncomplete
	}

	var revision, currentAttempt int
	var state domain.ProviderIssueAnalysisState
	var retainedAnalysis string
	err = tx.QueryRow(ctx, `
		SELECT job.revision,job.analysis_attempt,job.state,job.analysis
		FROM provider_issue_analysis_jobs job
		JOIN provider_installations installation ON installation.id=job.installation_id
		WHERE job.id=$1 AND job.tenant_id=$2 AND installation.active=TRUE
		  AND installation.verification_state IN ('legacy','verified')
		FOR UPDATE`, analysisID, tenantID).Scan(&revision, &currentAttempt, &state, &retainedAnalysis)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProviderIssueAnalysisRetryResult{}, ErrNotFound
	}
	if err != nil {
		return domain.ProviderIssueAnalysisRetryResult{}, fmt.Errorf("load provider issue analysis for retry: %w", err)
	}
	if revision != input.ExpectedRevision {
		return domain.ProviderIssueAnalysisRetryResult{}, ErrRevisionConflict
	}

	var replayAttempt int
	err = tx.QueryRow(ctx, `
		SELECT analysis_attempt
		FROM provider_issue_analysis_retry_requests
		WHERE job_id=$1 AND idempotency_key=$2`, analysisID, input.IdempotencyKey).Scan(&replayAttempt)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return domain.ProviderIssueAnalysisRetryResult{}, fmt.Errorf("commit replayed provider issue analysis retry: %w", err)
		}
		return domain.ProviderIssueAnalysisRetryResult{AnalysisID: analysisID, Revision: revision, Attempt: replayAttempt, State: state, Replayed: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.ProviderIssueAnalysisRetryResult{}, fmt.Errorf("lookup provider issue analysis retry: %w", err)
	}
	if currentAttempt >= 100 {
		return domain.ProviderIssueAnalysisRetryResult{}, ErrConflict
	}
	if state != domain.ProviderIssueAnalysisFailed {
		topic := providerIssuePendingTopic(state)
		if topic == "" {
			return domain.ProviderIssueAnalysisRetryResult{}, ErrConflict
		}
		var terminalFailure bool
		if err := tx.QueryRow(ctx, providerIssueTerminalDeliveryFailureSQL, analysisID, revision, currentAttempt, topic, domain.QuorumQueueRedeliveryLimit+1).Scan(&terminalFailure); err != nil {
			return domain.ProviderIssueAnalysisRetryResult{}, fmt.Errorf("check provider issue retry delivery: %w", err)
		}
		var skippedDelivery bool
		if err := tx.QueryRow(ctx, providerIssueSkippedDeliverySQL, analysisID, revision, currentAttempt, topic).Scan(&skippedDelivery); err != nil {
			return domain.ProviderIssueAnalysisRetryResult{}, fmt.Errorf("check provider issue skipped delivery: %w", err)
		}
		if !terminalFailure && !skippedDelivery {
			return domain.ProviderIssueAnalysisRetryResult{}, ErrConflict
		}
	}
	nextState := domain.ProviderIssueAnalysisQueued
	nextTopic := "provider.issue.acknowledge"
	if state == domain.ProviderIssueAnalysisAcknowledged && retainedAnalysis != "" {
		// The model result is already frozen for this revision. Retry only the
		// failed marker-keyed provider publication, not generation.
		nextState = domain.ProviderIssueAnalysisAcknowledged
		nextTopic = "provider.issue.analyze"
	}
	nextAttempt := currentAttempt + 1
	if _, err := tx.Exec(ctx, `
		INSERT INTO provider_issue_analysis_retry_requests (
			job_id,idempotency_key,revision,analysis_attempt,requested_by
		) VALUES ($1,$2,$3,$4,$5)`, analysisID, input.IdempotencyKey, revision, nextAttempt, actor); err != nil {
		return domain.ProviderIssueAnalysisRetryResult{}, fmt.Errorf("record provider issue analysis retry: %w", err)
	}
	command, err := tx.Exec(ctx, `
		UPDATE provider_issue_analysis_jobs
		SET state=$6,analysis_attempt=$4,
		    analysis=CASE WHEN $6::text='acknowledged' THEN analysis ELSE '' END,
		    last_error='',completed_at=NULL,updated_at=now()
		WHERE id=$1 AND tenant_id=$2 AND revision=$3 AND state=$7 AND analysis_attempt=$5`,
		analysisID, tenantID, revision, nextAttempt, currentAttempt, nextState, state)
	if err != nil {
		return domain.ProviderIssueAnalysisRetryResult{}, fmt.Errorf("queue provider issue analysis retry: %w", err)
	}
	if command.RowsAffected() == 0 {
		return domain.ProviderIssueAnalysisRetryResult{}, ErrConflict
	}
	if err := insertProviderIssueOutbox(ctx, tx, analysisID, revision, nextAttempt, nextTopic); err != nil {
		return domain.ProviderIssueAnalysisRetryResult{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata)
		VALUES ($1,$2,'provider_issue.analysis_retry_requested',$3,jsonb_build_object(
			'revision',$4::integer,'analysis_attempt',$5::integer,
			'reused_staged_analysis',$6::boolean))`,
		tenantID, actor, analysisID.String(), revision, nextAttempt, nextState == domain.ProviderIssueAnalysisAcknowledged); err != nil {
		return domain.ProviderIssueAnalysisRetryResult{}, fmt.Errorf("audit provider issue analysis retry: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ProviderIssueAnalysisRetryResult{}, fmt.Errorf("commit provider issue analysis retry: %w", err)
	}
	return domain.ProviderIssueAnalysisRetryResult{AnalysisID: analysisID, Revision: revision, Attempt: nextAttempt, State: nextState}, nil
}

// RecordProviderIssueReaction persists thumbs feedback from the marker-keyed
// analysis comment. The provider delivery and reaction IDs make webhook replay
// idempotent; removing a reaction retracts the same row without deleting its
// audit history. This path never schedules another analysis.
func (s *PostgresStore) RecordProviderIssueReaction(ctx context.Context, input domain.ProviderIssueReaction) error {
	if !input.Valid() {
		return fmt.Errorf("provider issue reaction is invalid")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin provider issue reaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var tenantID, jobID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT tenant_id,id
		FROM provider_issue_analysis_jobs
		WHERE stable_marker=$1 AND provider=$2 AND repository=$3
		FOR UPDATE`, input.AnalysisMarker, input.Provider, input.Repository).Scan(&tenantID, &jobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("resolve provider issue reaction: %w", err)
	}
	if input.Action == "deleted" {
		_, err = tx.Exec(ctx, `
			UPDATE provider_issue_analysis_feedback
			SET retracted_at=now()
			WHERE provider=$1 AND reaction_external_id=$2 AND job_id=$3`, input.Provider, input.ReactionExternalID, jobID)
	} else {
		_, err = tx.Exec(ctx, `
			INSERT INTO provider_issue_analysis_feedback (
				tenant_id,job_id,provider,delivery_id,reaction_external_id,actor_external_id,kind
			) VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (provider,reaction_external_id) DO UPDATE
			SET delivery_id=EXCLUDED.delivery_id,actor_external_id=EXCLUDED.actor_external_id,
			    kind=EXCLUDED.kind,retracted_at=NULL`, tenantID, jobID, input.Provider,
			input.DeliveryID, input.ReactionExternalID, input.ActorExternalID, input.Kind)
	}
	if err != nil {
		return fmt.Errorf("record provider issue reaction: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata)
		VALUES ($1,$2,$3,$4,jsonb_build_object('kind',$5::text,'provider',$6::text))`,
		tenantID, "provider:"+input.ActorExternalID, "provider_issue.feedback_"+input.Action,
		jobID.String(), input.Kind, input.Provider); err != nil {
		return fmt.Errorf("audit provider issue reaction: %w", err)
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) EnqueueProviderIssueAnalysis(ctx context.Context, event domain.ProviderIssueEvent) (domain.ProviderIssueAnalysisEnqueue, error) {
	if !event.Valid() {
		return domain.ProviderIssueAnalysisEnqueue{}, fmt.Errorf("provider issue event is invalid")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.ProviderIssueAnalysisEnqueue{}, fmt.Errorf("begin provider issue triage admission: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	installation, err := resolveInboundInstallation(ctx, tx, event.Provider, event.APIBaseURL, event.InstallationExternalID, event.Repository)
	if err != nil {
		return domain.ProviderIssueAnalysisEnqueue{}, err
	}
	var deliveryID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO webhook_deliveries (provider, delivery_id, event_name, payload, received_at)
		VALUES ($1,$2,$3,$4::jsonb,$5)
		ON CONFLICT (provider,delivery_id) DO NOTHING
		RETURNING id`, event.Provider, event.DeliveryID, event.EventName, string(event.Payload), event.ReceivedAt).Scan(&deliveryID)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.Commit(ctx); err != nil {
			return domain.ProviderIssueAnalysisEnqueue{}, fmt.Errorf("commit duplicate provider issue delivery: %w", err)
		}
		return domain.ProviderIssueAnalysisEnqueue{Duplicate: true}, nil
	}
	if err != nil {
		return domain.ProviderIssueAnalysisEnqueue{}, fmt.Errorf("record provider issue delivery: %w", err)
	}
	if !installation.AutomaticReviews {
		if err := tx.Commit(ctx); err != nil {
			return domain.ProviderIssueAnalysisEnqueue{}, fmt.Errorf("commit disabled provider issue triage: %w", err)
		}
		return domain.ProviderIssueAnalysisEnqueue{Skipped: true, Reason: "automatic analysis is disabled for this connection"}, nil
	}
	setupComplete, err := workspaceSetupAllowsReview(ctx, tx, installation.TenantID)
	if err != nil {
		return domain.ProviderIssueAnalysisEnqueue{}, err
	}
	if !setupComplete {
		if err := tx.Commit(ctx); err != nil {
			return domain.ProviderIssueAnalysisEnqueue{}, fmt.Errorf("commit incomplete provider issue triage: %w", err)
		}
		return domain.ProviderIssueAnalysisEnqueue{Skipped: true, Reason: "workspace setup is incomplete"}, nil
	}
	configScope := reviewConfigScopeForInstallation(installation, event.Repository)
	modelSnapshot, err := resolveEffectiveReviewConfiguration(ctx, tx, installation.TenantID, configScope, domain.ReviewConfigModels)
	if err != nil {
		return domain.ProviderIssueAnalysisEnqueue{}, err
	}
	route, err := domain.DecodeModelRoute(modelSnapshot.Content)
	if err == nil && !route.Enabled {
		if legacy, ok := legacyIssueTriageModelRoute(); ok {
			encoded, marshalErr := json.Marshal(legacy)
			if marshalErr != nil {
				return domain.ProviderIssueAnalysisEnqueue{}, fmt.Errorf("encode legacy issue triage model route: %w", marshalErr)
			}
			canonical, hash, valid := domain.CanonicalReviewConfig(domain.ReviewConfigModels, encoded)
			if !valid {
				return domain.ProviderIssueAnalysisEnqueue{}, fmt.Errorf("legacy issue triage model route is invalid")
			}
			modelSnapshot.Content, modelSnapshot.ContentSHA256 = canonical, hash
			route = legacy
		}
	}
	if err != nil || !route.Enabled {
		if err := tx.Commit(ctx); err != nil {
			return domain.ProviderIssueAnalysisEnqueue{}, fmt.Errorf("commit unavailable provider issue triage model: %w", err)
		}
		return domain.ProviderIssueAnalysisEnqueue{Skipped: true, Reason: "no enabled model route is configured"}, nil
	}
	promptSnapshot, err := resolveEffectiveReviewConfiguration(ctx, tx, installation.TenantID, configScope, domain.ReviewConfigPrompts)
	if err != nil {
		return domain.ProviderIssueAnalysisEnqueue{}, err
	}
	if _, err := domain.DecodeReviewPromptConfig(promptSnapshot.Content); err != nil {
		return domain.ProviderIssueAnalysisEnqueue{}, fmt.Errorf("decode provider issue prompt snapshot: %w", err)
	}
	triageSnapshot, err := resolveEffectiveReviewConfiguration(ctx, tx, installation.TenantID, configScope, domain.ReviewConfigIssueTriage)
	if err != nil {
		return domain.ProviderIssueAnalysisEnqueue{}, err
	}
	triageConfig, err := domain.DecodeIssueTriageConfig(triageSnapshot.Content)
	if err != nil {
		return domain.ProviderIssueAnalysisEnqueue{}, fmt.Errorf("decode provider issue triage snapshot: %w", err)
	}
	if !triageConfig.Enabled {
		if err := tx.Commit(ctx); err != nil {
			return domain.ProviderIssueAnalysisEnqueue{}, fmt.Errorf("commit disabled provider issue triage policy: %w", err)
		}
		return domain.ProviderIssueAnalysisEnqueue{Skipped: true, Reason: "issue analysis is disabled by repository policy"}, nil
	}

	requestedID := uuid.New()
	stableMarker := "open-review-platform:issue-triage:" + requestedID.String()
	var job domain.ProviderIssueAnalysisJob
	var modelJSON, promptJSON, triageJSON []byte
	err = tx.QueryRow(ctx, `
		INSERT INTO provider_issue_analysis_jobs (
			id,tenant_id,installation_id,last_delivery_id,provider,api_base_url,repository,issue_number,
			revision,action,title,body,author,labels,state,stable_marker,
			model_route,model_route_sha256,prompt_config,prompt_config_sha256,
			issue_triage_config,issue_triage_config_sha256
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,1,$9,$10,$11,$12,$13,'queued',$14,$15::jsonb,$16,$17::jsonb,$18,$19::jsonb,$20)
		ON CONFLICT (tenant_id,provider,api_base_url,repository,issue_number) DO UPDATE SET
			installation_id=EXCLUDED.installation_id,last_delivery_id=EXCLUDED.last_delivery_id,
			revision=provider_issue_analysis_jobs.revision+1,action=EXCLUDED.action,title=EXCLUDED.title,
			body=EXCLUDED.body,author=EXCLUDED.author,labels=EXCLUDED.labels,state='queued',analysis_attempt=1,
			model_route=EXCLUDED.model_route,model_route_sha256=EXCLUDED.model_route_sha256,
			prompt_config=EXCLUDED.prompt_config,prompt_config_sha256=EXCLUDED.prompt_config_sha256,
			issue_triage_config=EXCLUDED.issue_triage_config,issue_triage_config_sha256=EXCLUDED.issue_triage_config_sha256,
			analysis='',last_error='',completed_at=NULL,updated_at=now()
		RETURNING id,tenant_id,installation_id,provider,api_base_url,repository,issue_number,revision,analysis_attempt,action,
			title,body,author,labels,state,stable_marker,model_route,model_route_sha256,prompt_config,prompt_config_sha256,
			issue_triage_config,issue_triage_config_sha256,analysis,last_error`,
		requestedID, installation.TenantID, installation.ID, deliveryID, event.Provider, installation.APIBaseURL,
		event.Repository, event.IssueNumber, event.Action, event.Title, event.Body, event.Author, event.Labels,
		stableMarker, modelSnapshot.Content, modelSnapshot.ContentSHA256, promptSnapshot.Content, promptSnapshot.ContentSHA256,
		triageSnapshot.Content, triageSnapshot.ContentSHA256,
	).Scan(&job.ID, &job.TenantID, &job.InstallationID, &job.Provider, &job.APIBaseURL, &job.Repository,
		&job.IssueNumber, &job.Revision, &job.AnalysisAttempt, &job.Action, &job.Title, &job.Body, &job.Author, &job.Labels,
		&job.State, &job.StableMarker, &modelJSON, &job.ModelRouteSHA256, &promptJSON,
		&job.PromptConfigSHA256, &triageJSON, &job.IssueTriageConfigSHA256, &job.Analysis, &job.LastError)
	if err != nil {
		return domain.ProviderIssueAnalysisEnqueue{}, fmt.Errorf("upsert provider issue analysis: %w", err)
	}
	job.InstallationExternalID, job.CredentialRef = installation.ExternalID, installation.CredentialRef
	if err := json.Unmarshal(modelJSON, &job.ModelRoute); err != nil {
		return domain.ProviderIssueAnalysisEnqueue{}, fmt.Errorf("decode stored provider issue model route: %w", err)
	}
	if err := json.Unmarshal(promptJSON, &job.PromptConfig); err != nil {
		return domain.ProviderIssueAnalysisEnqueue{}, fmt.Errorf("decode stored provider issue prompt config: %w", err)
	}
	if err := json.Unmarshal(triageJSON, &job.IssueTriageConfig); err != nil {
		return domain.ProviderIssueAnalysisEnqueue{}, fmt.Errorf("decode stored provider issue triage config: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO provider_issue_analysis_receipts (
			delivery_id,tenant_id,installation_id,job_id,revision,repository,issue_number,action
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (delivery_id) DO NOTHING`, deliveryID, job.TenantID, job.InstallationID,
		job.ID, job.Revision, job.Repository, job.IssueNumber, job.Action); err != nil {
		return domain.ProviderIssueAnalysisEnqueue{}, fmt.Errorf("record provider issue webhook receipt: %w", err)
	}
	if err := insertProviderIssueOutbox(ctx, tx, job.ID, job.Revision, job.AnalysisAttempt, "provider.issue.acknowledge"); err != nil {
		return domain.ProviderIssueAnalysisEnqueue{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata)
		VALUES ($1,$2,'provider_issue.analysis_queued',$3,jsonb_build_object(
			'provider',$4::text,'repository',$5::text,'issue_number',$6::integer,'revision',$7::integer,
			'model_route_sha256',$8::text,'prompt_config_sha256',$9::text,'issue_triage_config_sha256',$10::text))`,
		job.TenantID, "provider:"+string(job.Provider)+":"+strings.TrimSpace(event.Author), job.ID.String(),
		job.Provider, job.Repository, job.IssueNumber, job.Revision, job.ModelRouteSHA256, job.PromptConfigSHA256, job.IssueTriageConfigSHA256); err != nil {
		return domain.ProviderIssueAnalysisEnqueue{}, fmt.Errorf("audit provider issue analysis admission: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ProviderIssueAnalysisEnqueue{}, fmt.Errorf("commit provider issue analysis admission: %w", err)
	}
	return domain.ProviderIssueAnalysisEnqueue{Job: job}, nil
}

func legacyIssueTriageModelRoute() (domain.ModelRouteConfig, bool) {
	endpoint := strings.TrimSpace(os.Getenv("OCR_LLM_URL"))
	model := strings.TrimSpace(os.Getenv("OCR_LLM_MODEL"))
	if endpoint == "" || model == "" {
		return domain.ModelRouteConfig{}, false
	}
	provider, protocol := "openai-compatible", "openai-chat"
	if strings.EqualFold(strings.TrimSpace(os.Getenv("OCR_USE_ANTHROPIC")), "true") {
		provider, protocol = "anthropic", "anthropic-messages"
	}
	route := domain.ModelRouteConfig{
		Enabled: true, Provider: provider, Protocol: protocol, BaseURL: endpoint, Model: model,
		CredentialRef: "env://OPEN_REVIEW_MODEL_SECRET_PRIMARY", Effort: "low",
		MaxPromptTokens: 8000, TokenBudget: 128000, SubtaskTimeoutMinutes: 5, MaxConcurrentRuns: 2,
	}
	return route, route.Valid()
}

func insertProviderIssueOutbox(ctx context.Context, tx pgx.Tx, jobID uuid.UUID, revision, attempt int, topic string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO outbox_messages (aggregate_type,aggregate_id,topic,dedupe_key,payload)
		VALUES ('provider_issue_analysis',$1,$2,$3,jsonb_build_object(
			'job_id',$1::uuid::text,'revision',$4::integer,'attempt',$5::integer))
		ON CONFLICT (dedupe_key) DO NOTHING`, jobID, topic,
		fmt.Sprintf("%s:%s:%d:attempt:%d", topic, jobID, revision, attempt), revision, attempt)
	if err != nil {
		return fmt.Errorf("insert %s outbox message: %w", topic, err)
	}
	return nil
}

func (s *PostgresStore) ProviderIssueAnalysis(ctx context.Context, jobID uuid.UUID, revision int) (domain.ProviderIssueAnalysisJob, error) {
	var job domain.ProviderIssueAnalysisJob
	var modelJSON, promptJSON, triageJSON []byte
	err := s.pool.QueryRow(ctx, `
		SELECT job.id,job.tenant_id,tenant.slug,job.installation_id,installation.external_id,installation.credential_ref,
		       job.provider,job.api_base_url,job.repository,job.issue_number,job.revision,job.analysis_attempt,job.action,
		       job.title,job.body,job.author,job.labels,job.state,job.stable_marker,
		       job.model_route,job.model_route_sha256,job.prompt_config,job.prompt_config_sha256,
		       job.issue_triage_config,job.issue_triage_config_sha256,
		       job.analysis,job.last_error
		FROM provider_issue_analysis_jobs job
		JOIN provider_installations installation ON installation.id=job.installation_id
		JOIN tenants tenant ON tenant.id=job.tenant_id
		WHERE job.id=$1 AND job.revision=$2 AND installation.active=TRUE
		  AND installation.tenant_id=job.tenant_id
		  AND installation.verification_state IN ('legacy','verified')`, jobID, revision).Scan(
		&job.ID, &job.TenantID, &job.TenantSlug, &job.InstallationID, &job.InstallationExternalID, &job.CredentialRef,
		&job.Provider, &job.APIBaseURL, &job.Repository, &job.IssueNumber, &job.Revision, &job.AnalysisAttempt, &job.Action,
		&job.Title, &job.Body, &job.Author, &job.Labels, &job.State, &job.StableMarker,
		&modelJSON, &job.ModelRouteSHA256, &promptJSON, &job.PromptConfigSHA256,
		&triageJSON, &job.IssueTriageConfigSHA256, &job.Analysis, &job.LastError)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProviderIssueAnalysisJob{}, ErrNotFound
	}
	if err != nil {
		return domain.ProviderIssueAnalysisJob{}, fmt.Errorf("load provider issue analysis: %w", err)
	}
	if err := json.Unmarshal(modelJSON, &job.ModelRoute); err != nil {
		return domain.ProviderIssueAnalysisJob{}, fmt.Errorf("decode provider issue analysis model route: %w", err)
	}
	if err := json.Unmarshal(promptJSON, &job.PromptConfig); err != nil {
		return domain.ProviderIssueAnalysisJob{}, fmt.Errorf("decode provider issue analysis prompt config: %w", err)
	}
	if err := json.Unmarshal(triageJSON, &job.IssueTriageConfig); err != nil {
		return domain.ProviderIssueAnalysisJob{}, fmt.Errorf("decode provider issue analysis triage config: %w", err)
	}
	return job, nil
}

// WithCurrentProviderIssuePublication serializes a provider write with Issue
// edits and manual retries. The same job row is locked by admission/retry, so
// an older worker cannot pass a revision check and then overwrite the shared
// marker comment after a newer revision has been admitted. The provider write
// is deliberately bounded by the caller's context and must be marker-keyed:
// a transport failure after provider acceptance can still be redelivered.
func (s *PostgresStore) WithCurrentProviderIssuePublication(ctx context.Context, jobID uuid.UUID, revision, attempt int, expectedState domain.ProviderIssueAnalysisState, stagedAnalysis string, publish func(context.Context) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin provider issue publication fence: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var state domain.ProviderIssueAnalysisState
	var analysis string
	err = tx.QueryRow(ctx, `
		SELECT job.state,job.analysis
		FROM provider_issue_analysis_jobs job
		JOIN provider_installations installation ON installation.id=job.installation_id
		WHERE job.id=$1 AND job.revision=$2 AND job.analysis_attempt=$3
		  AND installation.active=TRUE AND installation.tenant_id=job.tenant_id
		  AND installation.verification_state IN ('legacy','verified')
		FOR UPDATE OF job FOR SHARE OF installation`, jobID, revision, attempt).Scan(&state, &analysis)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock provider issue publication: %w", err)
	}
	if state != expectedState || analysis != stagedAnalysis {
		return ErrNotFound
	}
	if err := publish(ctx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit provider issue publication fence: %w", err)
	}
	return nil
}

func (s *PostgresStore) MarkProviderIssueAcknowledged(ctx context.Context, jobID uuid.UUID, revision, attempt int) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin provider issue acknowledgement: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var activeAttempt int
	err = tx.QueryRow(ctx, `
		UPDATE provider_issue_analysis_jobs
		SET state='acknowledged',last_error='',updated_at=now()
		WHERE id=$1 AND revision=$2 AND analysis_attempt=$3 AND state='queued'
		RETURNING analysis_attempt`, jobID, revision, attempt).Scan(&activeAttempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("mark provider issue acknowledged: %w", err)
	}
	if err := insertProviderIssueOutbox(ctx, tx, jobID, revision, activeAttempt, "provider.issue.analyze"); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit provider issue acknowledgement: %w", err)
	}
	return nil
}

// StageProviderIssueAnalysis retains the model result before any provider
// publication. A redelivery reuses the first retained result for this exact
// revision/attempt rather than running the model again.
func (s *PostgresStore) StageProviderIssueAnalysis(ctx context.Context, jobID uuid.UUID, revision, attempt int, analysis string) (string, error) {
	if strings.TrimSpace(analysis) == "" {
		return "", ErrConflict
	}
	var retained string
	err := s.pool.QueryRow(ctx, `
		UPDATE provider_issue_analysis_jobs
		SET analysis=$4,updated_at=now()
		WHERE id=$1 AND revision=$2 AND analysis_attempt=$3
		  AND state='acknowledged' AND analysis=''
		RETURNING analysis`, jobID, revision, attempt, analysis).Scan(&retained)
	if err == nil {
		return retained, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("stage provider issue analysis: %w", err)
	}
	err = s.pool.QueryRow(ctx, `
		SELECT analysis FROM provider_issue_analysis_jobs
		WHERE id=$1 AND revision=$2 AND analysis_attempt=$3
		  AND state='acknowledged' AND analysis<>''`, jobID, revision, attempt).Scan(&retained)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("load staged provider issue analysis: %w", err)
	}
	return retained, nil
}

func (s *PostgresStore) CompleteProviderIssueAnalysis(ctx context.Context, jobID uuid.UUID, revision, attempt int, analysis string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin provider issue analysis completion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `UPDATE provider_issue_analysis_jobs SET state='completed',last_error='',completed_at=now(),updated_at=now() WHERE id=$1 AND revision=$2 AND analysis_attempt=$3 AND state='acknowledged' AND analysis=$4 AND analysis<>''`, jobID, revision, attempt, analysis)
	if err != nil {
		return fmt.Errorf("complete provider issue analysis: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO provider_issue_feedback_polls (job_id,state,available_at,worker_id,locked_until,last_error,updated_at)
		SELECT job.id,'queued',now(),NULL,NULL,'',now()
		FROM provider_issue_analysis_jobs job
		JOIN provider_installations installation ON installation.id=job.installation_id
		WHERE job.id=$1 AND job.provider='github' AND installation.active=TRUE
		  AND installation.verification_state IN ('legacy','verified')
		  AND COALESCE((job.issue_triage_config->>'reaction_feedback')::boolean,FALSE)=TRUE
		ON CONFLICT (job_id) DO UPDATE SET
			state='queued',available_at=now(),worker_id=NULL,locked_until=NULL,last_error='',updated_at=now()`, jobID); err != nil {
		return fmt.Errorf("queue provider issue feedback poll: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit provider issue analysis completion: %w", err)
	}
	return nil
}

func (s *PostgresStore) FailProviderIssueAnalysis(ctx context.Context, jobID uuid.UUID, revision, attempt int, message string) error {
	message = normalizeReviewRequestMetadata(message, 2000)
	command, err := s.pool.Exec(ctx, `UPDATE provider_issue_analysis_jobs SET state='failed',last_error=$4,updated_at=now() WHERE id=$1 AND revision=$2 AND analysis_attempt=$3 AND state IN ('queued','acknowledged') AND analysis=''`, jobID, revision, attempt, message)
	if err != nil {
		return fmt.Errorf("fail provider issue analysis: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
