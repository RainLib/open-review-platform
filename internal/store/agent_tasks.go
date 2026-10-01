package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/agentdecision"
	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const agentTaskColumns = `id,tenant_id,installation_id,provider,api_base_url,repository,origin_kind,origin_number,origin_revision,intent,policy_revision,max_attempts,max_execution_seconds,max_feedback_cycles,executor_profile,decision_backend,feedback_cycle,execution_branch,parent_task_id,parent_attempt_id,source_state,source_base_ref,source_base_sha,source_captured_at,state,revision,requested_by,created_at,updated_at,workflow`
const agentTaskPolicyColumns = `id,provider,api_base_url,repository,mode,max_attempts,max_execution_seconds,max_feedback_cycles,executor_profile,decision_backend,auto_admission_enabled,auto_admission_label,revision,updated_by,updated_at,workflow`

// Feedback always updates the first Issue task's Draft. Its original target
// branch was resolved from provider metadata and frozen as that root task's
// source ref. Following at most three parent edges avoids a new browser- or
// webhook-supplied target and rejects malformed/cyclic task lineage.
const agentFeedbackTargetBranchSQL = `WITH RECURSIVE ancestry AS (
	SELECT id,parent_task_id,tenant_id,provider,api_base_url,repository,origin_kind,source_state,source_base_ref,0 AS depth FROM agent_tasks WHERE id=$1
	UNION ALL
	SELECT parent.id,parent.parent_task_id,parent.tenant_id,parent.provider,parent.api_base_url,parent.repository,parent.origin_kind,parent.source_state,parent.source_base_ref,child.depth+1
	FROM agent_tasks parent JOIN ancestry child ON parent.id=child.parent_task_id
	WHERE child.depth<3 AND parent.tenant_id=child.tenant_id AND parent.provider=child.provider
	  AND parent.api_base_url=child.api_base_url AND parent.repository=child.repository
)
SELECT source_base_ref FROM ancestry WHERE origin_kind='issue' AND parent_task_id IS NULL AND source_state='ready' AND depth>0`
const agentTaskClassificationColumns = `id,task_id,task_revision,source_revision,decision,risk_level,confidence,reasons,evaluation,next_action,snapshot_sha256,classifier_version,created_at`
const agentTaskAttemptColumns = `id,task_id,plan_id,task_revision,plan_revision,attempt,state,worker_id,adapter_job_id,locked_until,deadline_at,error_code,error_message,result_summary,branch_name,head_sha,pull_request_url,pull_request_number,patch_sha256,changed_file_count,diff_bytes,verification_profile_sha256,verification_output_sha256,verification_output_bytes,created_at,started_at,finished_at,updated_at`
const agentTaskPlanColumns = `id,task_id,revision,state,summary,sections,plan_sha256,created_by,COALESCE(approved_by,''),approved_at,created_at`
const agentTaskLeaseFloor = 15 * time.Second

// An automatic candidate freezes the labels as well as the Issue text, while
// an explicit request freezes only the text. Both identify the same work when
// the current Issue snapshot matches. Never choose one arbitrarily if older
// data already contains both variants for the same snapshot.
func findIssueAgentTaskTx(ctx context.Context, tx pgx.Tx, tenantID, installationID uuid.UUID, repository string, issueNumber int, revisions []string, lock bool) (domain.AgentTask, error) {
	query := `SELECT ` + agentTaskColumns + ` FROM agent_tasks
		WHERE tenant_id=$1 AND installation_id=$2 AND repository=$3
		  AND origin_kind='issue' AND origin_number=$4
		  AND origin_revision=ANY($5::text[]) AND intent='implement'
		ORDER BY created_at DESC,id DESC LIMIT 2`
	if lock {
		query += ` FOR UPDATE`
	}
	rows, err := tx.Query(ctx, query, tenantID, installationID, repository, issueNumber, revisions)
	if err != nil {
		return domain.AgentTask{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return domain.AgentTask{}, err
		}
		return domain.AgentTask{}, pgx.ErrNoRows
	}
	task, err := scanAgentTask(rows)
	if err != nil {
		return domain.AgentTask{}, err
	}
	if rows.Next() {
		return domain.AgentTask{}, ErrConflict
	}
	if err := rows.Err(); err != nil {
		return domain.AgentTask{}, err
	}
	return task, nil
}

func agentIssueCommandRevisions(event domain.AgentTaskCommandEvent) []string {
	revisions := []string{event.IssueRevision}
	if strings.TrimSpace(event.IssueTitle) == "" || domain.AgentIssueRevision(event.Provider, event.APIBaseURL, event.Repository, event.IssueNumber, event.IssueTitle, event.IssueBody) != event.IssueRevision {
		return revisions
	}
	return append(revisions, domain.AgentAutomaticIssueRevision(event.Provider, event.APIBaseURL, event.Repository, event.IssueNumber, event.IssueTitle, event.IssueBody, event.IssueLabels))
}

// Agent API requests do not carry an installation ID. Resolve the exact
// provider-qualified repository scope rather than selecting an arbitrary
// installation on the same host. Active legacy/verified installations retain
// the existing admission rule; historic overlapping scopes fail closed.
func authorizedAgentInstallationTx(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, provider domain.Provider, apiBaseURL, repository string) (uuid.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT id,repository_scope FROM provider_installations WHERE tenant_id=$1 AND provider=$2 AND api_base_url=$3 AND active=TRUE AND verification_state IN ('legacy','verified') ORDER BY id FOR UPDATE`, tenantID, provider, apiBaseURL)
	if err != nil {
		return uuid.Nil, fmt.Errorf("list agent task installations: %w", err)
	}
	defer rows.Close()
	var match uuid.UUID
	seen := false
	for rows.Next() {
		seen = true
		var id uuid.UUID
		var scope string
		if err := rows.Scan(&id, &scope); err != nil {
			return uuid.Nil, fmt.Errorf("scan agent task installation: %w", err)
		}
		if !repositoryScopeAllows(scope, repository) {
			continue
		}
		if match != uuid.Nil {
			return uuid.Nil, ErrAmbiguousInstallation
		}
		match = id
	}
	if err := rows.Err(); err != nil {
		return uuid.Nil, fmt.Errorf("iterate agent task installations: %w", err)
	}
	if match != uuid.Nil {
		return match, nil
	}
	if seen {
		return uuid.Nil, ErrForbidden
	}
	return uuid.Nil, ErrUnknownInstallation
}

func (s *PostgresStore) CreateAgentTask(ctx context.Context, actor, tenantSlug string, input domain.AgentTaskInput) (domain.AgentTask, error) {
	return s.createAgentTask(ctx, actor, tenantSlug, input, uuid.Nil, "")
}

func (s *PostgresStore) createAgentTask(ctx context.Context, actor, tenantSlug string, input domain.AgentTaskInput, expectedInstallationID uuid.UUID, automaticRevision string) (domain.AgentTask, error) {
	input.APIBaseURL = strings.TrimSuffix(strings.TrimSpace(input.APIBaseURL), "/")
	input.Repository = strings.Trim(strings.TrimSpace(input.Repository), "/")
	input.OriginKind = strings.ToLower(strings.TrimSpace(input.OriginKind))
	input.OriginRevision = strings.TrimSpace(input.OriginRevision)
	input.Intent = strings.ToLower(strings.TrimSpace(input.Intent))
	if !input.Valid() {
		return domain.AgentTask{}, ErrInvalidAgentTask
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.AgentTask{}, fmt.Errorf("begin agent task: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.AgentTask{}, err
	}
	if !canManageAgentTasks(role) {
		return domain.AgentTask{}, ErrForbidden
	}
	installationID, err := authorizedAgentInstallationTx(ctx, tx, tenantID, input.Provider, input.APIBaseURL, input.Repository)
	if err != nil {
		return domain.AgentTask{}, fmt.Errorf("load agent task installation: %w", err)
	}
	if expectedInstallationID != uuid.Nil && installationID != expectedInstallationID {
		return domain.AgentTask{}, ErrUnknownInstallation
	}
	var policyMode string
	var policyRevision, maxAttempts, maxExecutionSeconds, maxFeedbackCycles int
	var executorProfile, decisionBackend string
	err = tx.QueryRow(ctx, `SELECT mode,revision,max_attempts,max_execution_seconds,max_feedback_cycles,executor_profile,decision_backend FROM agent_task_policies WHERE tenant_id=$1 AND provider=$2 AND api_base_url=$3 AND repository=$4 FOR UPDATE`, tenantID, input.Provider, input.APIBaseURL, input.Repository).Scan(&policyMode, &policyRevision, &maxAttempts, &maxExecutionSeconds, &maxFeedbackCycles, &executorProfile, &decisionBackend)
	if errors.Is(err, pgx.ErrNoRows) || policyMode != "manual" {
		return domain.AgentTask{}, ErrAgentTaskDisabled
	}
	if err != nil {
		return domain.AgentTask{}, fmt.Errorf("load agent task policy: %w", err)
	}
	// Direct API callers do not supply labels. When triage has retained the
	// exact Issue text for this installation, use its server-held labels to
	// prevent a second task beside an automatic candidate for that snapshot.
	if input.OriginKind == "issue" && automaticRevision == "" {
		if strings.HasPrefix(input.OriginRevision, "issue-sha256:") {
			var title, body string
			var labels []string
			lookupErr := tx.QueryRow(ctx, `SELECT title,body,labels FROM provider_issue_analysis_jobs
			WHERE tenant_id=$1 AND installation_id=$2 AND provider=$3 AND api_base_url=$4
			  AND repository=$5 AND issue_number=$6
			ORDER BY updated_at DESC LIMIT 1`, tenantID, installationID, input.Provider, input.APIBaseURL, input.Repository, input.OriginNumber).Scan(&title, &body, &labels)
			if lookupErr != nil && !errors.Is(lookupErr, pgx.ErrNoRows) {
				return domain.AgentTask{}, fmt.Errorf("load retained Issue labels for Agent deduplication: %w", lookupErr)
			}
			if lookupErr == nil && domain.AgentIssueRevision(input.Provider, input.APIBaseURL, input.Repository, input.OriginNumber, title, body) == input.OriginRevision {
				automaticRevision = domain.AgentAutomaticIssueRevision(input.Provider, input.APIBaseURL, input.Repository, input.OriginNumber, title, body, labels)
			}
		}
		if automaticRevision == "" {
			// An automatic task may have been admitted before Issue triage
			// retained this revision. Without labels, a direct API request
			// cannot prove it is different work, so fail closed instead of
			// opening two agent branches for one Issue.
			var automaticTaskExists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_tasks WHERE tenant_id=$1 AND installation_id=$2 AND repository=$3 AND origin_kind='issue' AND origin_number=$4 AND intent='implement' AND requested_by='policy:auto')`, tenantID, installationID, input.Repository, input.OriginNumber).Scan(&automaticTaskExists); err != nil {
				return domain.AgentTask{}, fmt.Errorf("check unresolved automatic Issue task: %w", err)
			}
			if automaticTaskExists {
				return domain.AgentTask{}, ErrConflict
			}
		}
	}
	if input.OriginKind == "issue" && automaticRevision != "" {
		var existingTaskID uuid.UUID
		err = tx.QueryRow(ctx, `SELECT id FROM agent_tasks WHERE tenant_id=$1 AND installation_id=$2 AND repository=$3 AND origin_kind='issue' AND origin_number=$4 AND origin_revision=$5 AND intent='implement' LIMIT 1`, tenantID, installationID, input.Repository, input.OriginNumber, automaticRevision).Scan(&existingTaskID)
		if err == nil {
			return domain.AgentTask{}, ErrConflict
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return domain.AgentTask{}, fmt.Errorf("check automatic Issue task: %w", err)
		}
	}
	taskID := uuid.New()
	result, err := scanAgentTask(tx.QueryRow(ctx, `
		INSERT INTO agent_tasks(id,tenant_id,installation_id,provider,api_base_url,repository,origin_kind,origin_number,origin_revision,intent,policy_revision,max_attempts,max_execution_seconds,max_feedback_cycles,executor_profile,decision_backend,execution_branch,requested_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		RETURNING `+agentTaskColumns, taskID, tenantID, installationID, input.Provider, input.APIBaseURL, input.Repository, input.OriginKind, input.OriginNumber, input.OriginRevision, input.Intent, policyRevision, maxAttempts, maxExecutionSeconds, maxFeedbackCycles, executorProfile, decisionBackend, "agent/"+taskID.String(), actor))
	if isUniqueViolation(err) {
		return domain.AgentTask{}, ErrConflict
	}
	if err != nil {
		return domain.AgentTask{}, fmt.Errorf("create agent task: %w", err)
	}
	if _, err = recordAgentTaskClassification(ctx, tx, result, agentdecision.Classify(result, "", "", nil)); err != nil {
		return domain.AgentTask{}, err
	}
	if err = queueAgentTaskSourceResolution(ctx, tx, result.ID); err != nil {
		return domain.AgentTask{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata) VALUES($1,$2,'agent_task.created',$3,jsonb_build_object('provider',$4::text,'api_base_url',$5::text,'repository',$6::text,'origin_kind',$7::text,'origin_number',$8::int,'origin_revision',$9::text,'intent',$10::text,'revision',$11::int))`, tenantID, actor, result.ID.String(), result.Provider, result.APIBaseURL, result.Repository, result.OriginKind, result.OriginNumber, result.OriginRevision, result.Intent, result.Revision); err != nil {
		return domain.AgentTask{}, fmt.Errorf("audit agent task: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.AgentTask{}, fmt.Errorf("commit agent task: %w", err)
	}
	return result, nil
}

// CreateAgentTaskFromProviderIssue keeps the retained Issue body on the server.
// The caller supplies only the analysis identity and visible revision; the
// source worker must still reread the open provider Issue at this exact digest
// before planning or executing anything.
func (s *PostgresStore) CreateAgentTaskFromProviderIssue(ctx context.Context, actor, tenantSlug string, analysisID uuid.UUID, expectedRevision int) (domain.AgentTask, error) {
	if analysisID == uuid.Nil || expectedRevision < 1 {
		return domain.AgentTask{}, ErrInvalidAgentTask
	}
	tenantID, role, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.AgentTask{}, err
	}
	if !canManageAgentTasks(role) {
		return domain.AgentTask{}, ErrForbidden
	}
	var input domain.AgentTaskInput
	var title, body string
	var labels []string
	var revision int
	var installationID uuid.UUID
	err = s.pool.QueryRow(ctx, `
		SELECT installation_id,provider,api_base_url,repository,issue_number,revision,title,body,labels
		FROM provider_issue_analysis_jobs
		WHERE tenant_id=$1 AND id=$2`, tenantID, analysisID).Scan(
		&installationID, &input.Provider, &input.APIBaseURL, &input.Repository, &input.OriginNumber, &revision, &title, &body, &labels)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AgentTask{}, ErrNotFound
	}
	if err != nil {
		return domain.AgentTask{}, fmt.Errorf("load provider Issue for agent task: %w", err)
	}
	if revision != expectedRevision {
		return domain.AgentTask{}, ErrRevisionConflict
	}
	input.OriginKind = "issue"
	input.Intent = "implement"
	input.OriginRevision = domain.AgentIssueRevision(input.Provider, input.APIBaseURL, input.Repository, input.OriginNumber, title, body)
	automaticRevision := domain.AgentAutomaticIssueRevision(input.Provider, input.APIBaseURL, input.Repository, input.OriginNumber, title, body, labels)
	return s.createAgentTask(ctx, actor, tenantSlug, input, installationID, automaticRevision)
}

// ProcessAutomaticAgentTask creates a candidate only after an exact,
// repository-owned label matches. It never bypasses source capture, plan
// approval, executor isolation, or merge gates.
func (s *PostgresStore) ProcessAutomaticAgentTask(ctx context.Context, event domain.ProviderIssueEvent) (domain.AgentTaskCommandOutcome, error) {
	if !event.Valid() {
		return domain.AgentTaskCommandOutcome{}, ErrInvalidAgentTask
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.AgentTaskCommandOutcome{}, fmt.Errorf("begin automatic agent task: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	installation, err := resolveInboundInstallation(ctx, tx, event.Provider, event.APIBaseURL, event.InstallationExternalID, event.Repository)
	if err != nil {
		return domain.AgentTaskCommandOutcome{}, err
	}
	var mode, profile, label, decisionBackend string
	var policyRevision, maxAttempts, maxExecutionSeconds, maxFeedbackCycles int
	var enabled bool
	err = tx.QueryRow(ctx, `SELECT mode,revision,max_attempts,max_execution_seconds,max_feedback_cycles,executor_profile,decision_backend,auto_admission_enabled,auto_admission_label FROM agent_task_policies WHERE tenant_id=$1 AND provider=$2 AND api_base_url=$3 AND repository=$4 FOR UPDATE`, installation.TenantID, installation.Provider, installation.APIBaseURL, event.Repository).Scan(&mode, &policyRevision, &maxAttempts, &maxExecutionSeconds, &maxFeedbackCycles, &profile, &decisionBackend, &enabled, &label)
	if errors.Is(err, pgx.ErrNoRows) || mode != "manual" || !enabled || !agentTaskLabelMatches(event.Labels, label) {
		if err := tx.Commit(ctx); err != nil {
			return domain.AgentTaskCommandOutcome{}, fmt.Errorf("commit ignored automatic agent task: %w", err)
		}
		return domain.AgentTaskCommandOutcome{Reason: "automatic Agent admission is not enabled for this Issue"}, nil
	}
	if err != nil {
		return domain.AgentTaskCommandOutcome{}, fmt.Errorf("load automatic agent task policy: %w", err)
	}
	revision := automaticAgentTaskIssueRevision(event)
	var interactionID uuid.UUID
	err = tx.QueryRow(ctx, `INSERT INTO agent_task_interactions(tenant_id,provider,provider_delivery_id,actor_external_id,repository,issue_number,issue_revision,comment_external_id,command,normalized_input,result) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'auto',$9,'ignored') ON CONFLICT(provider,provider_delivery_id) DO NOTHING RETURNING id`, installation.TenantID, event.Provider, event.DeliveryID, "provider-issue:"+strings.TrimSpace(event.Author), event.Repository, event.IssueNumber, revision, "webhook:"+event.DeliveryID, "automatic label:"+strings.TrimSpace(label)).Scan(&interactionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AgentTaskCommandOutcome{Duplicate: true}, nil
	}
	if err != nil {
		return domain.AgentTaskCommandOutcome{}, fmt.Errorf("record automatic agent task interaction: %w", err)
	}
	var task domain.AgentTask
	created := false
	taskID := uuid.New()
	plainRevision := domain.AgentIssueRevision(event.Provider, installation.APIBaseURL, event.Repository, event.IssueNumber, event.Title, event.Body)
	task, err = findIssueAgentTaskTx(ctx, tx, installation.TenantID, installation.ID, event.Repository, event.IssueNumber, []string{plainRevision, revision}, true)
	if errors.Is(err, pgx.ErrNoRows) {
		task, err = scanAgentTask(tx.QueryRow(ctx, `INSERT INTO agent_tasks(id,tenant_id,installation_id,provider,api_base_url,repository,origin_kind,origin_number,origin_revision,intent,policy_revision,max_attempts,max_execution_seconds,max_feedback_cycles,executor_profile,decision_backend,execution_branch,requested_by) VALUES($1,$2,$3,$4,$5,$6,'issue',$7,$8,'implement',$9,$10,$11,$12,$13,$14,$15,'policy:auto') ON CONFLICT(tenant_id,installation_id,repository,origin_kind,origin_number,origin_revision,intent) DO NOTHING RETURNING `+agentTaskColumns, taskID, installation.TenantID, installation.ID, event.Provider, installation.APIBaseURL, event.Repository, event.IssueNumber, revision, policyRevision, maxAttempts, maxExecutionSeconds, maxFeedbackCycles, profile, decisionBackend, "agent/"+taskID.String()))
		if errors.Is(err, pgx.ErrNoRows) {
			task, err = findIssueAgentTaskTx(ctx, tx, installation.TenantID, installation.ID, event.Repository, event.IssueNumber, []string{plainRevision, revision}, true)
		} else if err == nil {
			created = true
		}
	}
	if err != nil {
		return domain.AgentTaskCommandOutcome{}, fmt.Errorf("create or load automatic agent task: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE agent_task_interactions SET result='accepted',task_id=$2 WHERE id=$1`, interactionID, task.ID); err != nil {
		return domain.AgentTaskCommandOutcome{}, err
	}
	classification := agentdecision.Classify(task, event.Title, event.Body, event.Labels)
	if created {
		if _, err := recordAgentTaskClassification(ctx, tx, task, classification); err != nil {
			return domain.AgentTaskCommandOutcome{}, err
		}
		if classification.Decision == "rejected" {
			task, err = scanAgentTask(tx.QueryRow(ctx, `UPDATE agent_tasks SET state='rejected',revision=revision+1,updated_at=now() WHERE id=$1 AND state='received' RETURNING `+agentTaskColumns, task.ID))
			if err != nil {
				return domain.AgentTaskCommandOutcome{}, fmt.Errorf("reject unsafe automatic agent task: %w", err)
			}
		}
		// Provider-originated work is released only after its first visible
		// acknowledgement has been accepted by the provider.
		if _, err := tx.Exec(ctx, `INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata) VALUES($1,'policy:auto','agent_task.auto_admitted',$2,jsonb_build_object('provider',$3::text,'repository',$4::text,'issue_number',$5::int,'issue_revision',$6::text,'policy_revision',$7::int,'label',$8::text))`, installation.TenantID, task.ID.String(), task.Provider, task.Repository, task.OriginNumber, task.OriginRevision, task.PolicyRevision, label); err != nil {
			return domain.AgentTaskCommandOutcome{}, err
		}
	}
	// The first task transaction already committed its acknowledgement to the
	// outbox. A later webhook with a new delivery ID but the same immutable
	// Issue revision keeps its interaction receipt without posting another
	// comment (including when the first acknowledgement used a legacy marker).
	if created {
		if err := queueAutomaticAgentTaskResponse(ctx, tx, installation, event, task, classification); err != nil {
			return domain.AgentTaskCommandOutcome{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.AgentTaskCommandOutcome{}, fmt.Errorf("commit automatic agent task: %w", err)
	}
	return domain.AgentTaskCommandOutcome{Accepted: true, TaskID: &task.ID, Reason: "automatic Agent candidate recorded"}, nil
}

func agentTaskLabelMatches(labels []string, wanted string) bool {
	for _, label := range labels {
		if strings.EqualFold(strings.TrimSpace(label), strings.TrimSpace(wanted)) {
			return true
		}
	}
	return false
}

func automaticAgentTaskIssueRevision(event domain.ProviderIssueEvent) string {
	return domain.AgentAutomaticIssueRevision(event.Provider, event.APIBaseURL, event.Repository, event.IssueNumber, event.Title, event.Body, event.Labels)
}

func (s *PostgresStore) ListAgentTasks(ctx context.Context, actor, tenantSlug string, limit int, before uuid.UUID) (domain.AgentTaskPage, error) {
	if limit < 1 || limit > 100 {
		return domain.AgentTaskPage{}, ErrInvalidAgentTask
	}
	tenantID, _, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.AgentTaskPage{}, err
	}
	var cursorCreatedAt any
	if before != uuid.Nil {
		var createdAt time.Time
		err = s.pool.QueryRow(ctx, `SELECT created_at FROM agent_tasks WHERE tenant_id=$1 AND id=$2`, tenantID, before).Scan(&createdAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.AgentTaskPage{}, ErrNotFound
		}
		if err != nil {
			return domain.AgentTaskPage{}, fmt.Errorf("load agent task cursor: %w", err)
		}
		cursorCreatedAt = createdAt
	}
	rows, err := s.pool.Query(ctx, `SELECT `+agentTaskColumns+` FROM agent_tasks
		WHERE tenant_id=$1 AND ($3::timestamptz IS NULL OR (created_at,id) < ($3::timestamptz,$4::uuid))
		ORDER BY created_at DESC,id DESC LIMIT $2`, tenantID, limit+1, cursorCreatedAt, before)
	if err != nil {
		return domain.AgentTaskPage{}, fmt.Errorf("list agent tasks: %w", err)
	}
	defer rows.Close()
	items := make([]domain.AgentTask, 0)
	for rows.Next() {
		item, err := scanAgentTask(rows)
		if err != nil {
			return domain.AgentTaskPage{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return domain.AgentTaskPage{}, fmt.Errorf("iterate agent tasks: %w", err)
	}
	page := domain.AgentTaskPage{Tasks: items}
	if len(items) > limit {
		page.Tasks = items[:limit]
		page.NextCursor = page.Tasks[len(page.Tasks)-1].ID.String()
	}
	return page, nil
}

func (s *PostgresStore) GetAgentTask(ctx context.Context, actor, tenantSlug string, taskID uuid.UUID) (domain.AgentTaskDetail, error) {
	if taskID == uuid.Nil {
		return domain.AgentTaskDetail{}, ErrInvalidAgentTask
	}
	tenantID, role, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.AgentTaskDetail{}, err
	}
	task, err := scanAgentTask(s.pool.QueryRow(ctx, `SELECT `+agentTaskColumns+` FROM agent_tasks WHERE tenant_id=$1 AND id=$2`, tenantID, taskID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AgentTaskDetail{}, ErrNotFound
	}
	if err != nil {
		return domain.AgentTaskDetail{}, fmt.Errorf("get agent task: %w", err)
	}
	rows, err := s.pool.Query(ctx, `SELECT `+agentTaskPlanColumns+` FROM agent_task_plans WHERE task_id=$1 ORDER BY revision DESC`, taskID)
	if err != nil {
		return domain.AgentTaskDetail{}, fmt.Errorf("list agent task plans: %w", err)
	}
	defer rows.Close()
	detail := domain.AgentTaskDetail{Task: task, Plans: []domain.AgentTaskPlan{}, Classifications: []domain.AgentTaskClassification{}, Attempts: []domain.AgentTaskAttempt{}, PublicationCheckpoints: []domain.AgentTaskPublicationCheckpoint{}, LinkedReviews: []domain.AgentTaskLinkedReview{}}
	if task.OriginKind == "issue" && task.SourceState == "ready" {
		detail.TargetBranch = task.SourceBaseRef
	}
	if task.OriginKind == "pull_request" {
		detail.TargetBranch, err = s.agentFeedbackTargetBranch(ctx, taskID)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.AgentTaskDetail{}, fmt.Errorf("feedback task has no frozen target branch: %w", ErrInvalidAgentTask)
		}
		if err != nil {
			return domain.AgentTaskDetail{}, fmt.Errorf("load agent feedback target branch: %w", err)
		}
		var feedback domain.AgentTaskFeedbackReference
		err = s.pool.QueryRow(ctx, `
			SELECT comment_external_id,actor_external_id,source_review_run_id,internal_repair_kind
			FROM agent_task_feedback_cycles
			WHERE tenant_id=$1 AND child_task_id=$2`, tenantID, taskID).
			Scan(&feedback.CommentExternalID, &feedback.ActorExternalID, &feedback.SourceReviewRunID, &feedback.InternalRepairKind)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.AgentTaskDetail{}, fmt.Errorf("feedback task has no admitted comment: %w", ErrInvalidAgentTask)
		}
		if err != nil {
			return domain.AgentTaskDetail{}, fmt.Errorf("load agent feedback reference: %w", err)
		}
		detail.Feedback = &feedback
	}
	for rows.Next() {
		plan, err := scanAgentTaskPlan(rows)
		if err != nil {
			return domain.AgentTaskDetail{}, err
		}
		detail.Plans = append(detail.Plans, plan)
	}
	if err := rows.Err(); err != nil {
		return domain.AgentTaskDetail{}, err
	}
	rows.Close()
	classificationRows, err := s.pool.Query(ctx, `SELECT `+agentTaskClassificationColumns+` FROM agent_task_classifications WHERE task_id=$1 ORDER BY created_at DESC,id DESC`, taskID)
	if err != nil {
		return domain.AgentTaskDetail{}, fmt.Errorf("list agent task classifications: %w", err)
	}
	defer classificationRows.Close()
	for classificationRows.Next() {
		classification, err := scanAgentTaskClassification(classificationRows)
		if err != nil {
			return domain.AgentTaskDetail{}, err
		}
		detail.Classifications = append(detail.Classifications, classification)
	}
	if err := classificationRows.Err(); err != nil {
		return domain.AgentTaskDetail{}, err
	}
	classificationRows.Close()
	attemptRows, err := s.pool.Query(ctx, `SELECT `+agentTaskAttemptColumns+` FROM agent_task_attempts WHERE task_id=$1 ORDER BY created_at DESC,id DESC`, taskID)
	if err != nil {
		return domain.AgentTaskDetail{}, fmt.Errorf("list agent task attempts: %w", err)
	}
	defer attemptRows.Close()
	for attemptRows.Next() {
		attempt, err := scanAgentTaskAttempt(attemptRows)
		if err != nil {
			return domain.AgentTaskDetail{}, err
		}
		detail.Attempts = append(detail.Attempts, attempt)
	}
	if err := attemptRows.Err(); err != nil {
		return domain.AgentTaskDetail{}, err
	}
	attemptRows.Close()
	publicationRows, err := s.pool.Query(ctx, `
		SELECT checkpoint.attempt_id,checkpoint.attempt_number,checkpoint.adapter_job_id,
		       checkpoint.branch_name,checkpoint.head_sha,checkpoint.patch_sha256,
		       checkpoint.changed_file_count,checkpoint.diff_bytes,
		       checkpoint.verification_profile_sha256,checkpoint.verification_output_sha256,checkpoint.verification_output_bytes,
		       checkpoint.recorded_at
		FROM agent_task_publication_checkpoints checkpoint
		JOIN agent_task_attempts attempt ON attempt.id=checkpoint.attempt_id
		WHERE attempt.task_id=$1
		ORDER BY checkpoint.recorded_at DESC,checkpoint.attempt_id DESC`, taskID)
	if err != nil {
		return domain.AgentTaskDetail{}, fmt.Errorf("list agent task publication checkpoints: %w", err)
	}
	defer publicationRows.Close()
	for publicationRows.Next() {
		var checkpoint domain.AgentTaskPublicationCheckpoint
		if err := publicationRows.Scan(&checkpoint.AttemptID, &checkpoint.AttemptNumber, &checkpoint.AdapterJobID, &checkpoint.BranchName, &checkpoint.HeadSHA, &checkpoint.PatchSHA256, &checkpoint.ChangedFileCount, &checkpoint.DiffBytes, &checkpoint.VerificationProfileSHA256, &checkpoint.VerificationOutputSHA256, &checkpoint.VerificationOutputBytes, &checkpoint.RecordedAt); err != nil {
			return domain.AgentTaskDetail{}, fmt.Errorf("scan agent task publication checkpoint: %w", err)
		}
		detail.PublicationCheckpoints = append(detail.PublicationCheckpoints, checkpoint)
	}
	if err := publicationRows.Err(); err != nil {
		return domain.AgentTaskDetail{}, fmt.Errorf("iterate agent task publication checkpoints: %w", err)
	}
	publicationRows.Close()
	// The provider webhook can arrive before the adapter's terminal callback or
	// after it. Derive this link at read time so either ordering is correct, and
	// only relate a review of the exact successfully published Agent head.
	linkedRows, err := s.pool.Query(ctx, `
		SELECT run.id,run.state,run.head_sha,request.review_number,run.created_at,
		       gate.run_id IS NOT NULL,COALESCE(gate.enabled,false),COALESCE(gate.threshold,''),
		       COALESCE(gate.conclusion,''),COALESCE(gate.blocking_findings,0),COALESCE(gate.finding_count,0)
		FROM review_runs run
		JOIN review_requests request ON request.id=run.request_id
		JOIN review_jobs job ON job.id=run.legacy_job_id
		LEFT JOIN review_merge_gate_decisions gate ON gate.run_id=run.id
		WHERE request.tenant_id=$1 AND request.installation_id=$2
		  AND request.provider=$3 AND request.api_base_url=$4
		  AND request.repository=$5 AND job.head_ref=$6
		  AND EXISTS (
			SELECT 1 FROM agent_task_attempts attempt
			WHERE attempt.task_id=$7 AND attempt.state='succeeded'
			  AND attempt.pull_request_number=request.review_number
			  AND attempt.branch_name=job.head_ref
			  AND attempt.head_sha=run.head_sha
		  )
		ORDER BY run.created_at DESC,run.id DESC LIMIT 20`,
		tenantID, task.InstallationID, task.Provider, task.APIBaseURL, task.Repository, task.ExecutionBranch, task.ID)
	if err != nil {
		return domain.AgentTaskDetail{}, fmt.Errorf("list reviews linked to agent task: %w", err)
	}
	defer linkedRows.Close()
	for linkedRows.Next() {
		var linked domain.AgentTaskLinkedReview
		var hasGate bool
		var gate domain.AgentTaskLinkedReviewGate
		if err := linkedRows.Scan(&linked.RunID, &linked.State, &linked.HeadSHA, &linked.ReviewNumber, &linked.CreatedAt,
			&hasGate, &gate.Enabled, &gate.Threshold, &gate.Conclusion, &gate.BlockingFindings, &gate.FindingCount); err != nil {
			return domain.AgentTaskDetail{}, fmt.Errorf("scan agent task linked review: %w", err)
		}
		if hasGate {
			linked.MergeGate = &gate
		}
		detail.LinkedReviews = append(detail.LinkedReviews, linked)
	}
	if err := linkedRows.Err(); err != nil {
		return domain.AgentTaskDetail{}, fmt.Errorf("iterate agent task linked reviews: %w", err)
	}
	acceptance, acceptanceErr := scanAgentAcceptance(s.pool.QueryRow(ctx, `SELECT `+agentAcceptanceColumns+` FROM agent_task_acceptances WHERE task_id=$1`, taskID))
	if acceptanceErr == nil {
		proofErr := s.pool.QueryRow(ctx, `SELECT verification_criteria FROM agent_task_publication_checkpoints WHERE attempt_id=$1 AND head_sha=$2 ORDER BY attempt_number DESC LIMIT 1`, acceptance.AttemptID, acceptance.HeadSHA).Scan(&acceptance.VerificationCriteria)
		if proofErr != nil && !errors.Is(proofErr, pgx.ErrNoRows) {
			return domain.AgentTaskDetail{}, proofErr
		}
		acceptance.CanRetryChecks = (role == "owner" || role == "admin") && acceptance.State != "superseded" && acceptance.ReviewRunID != nil
		acceptance.CanDecide = (role == "owner" || role == "admin") && (acceptance.State == "awaiting_acceptance" || acceptance.State == "checks_failed" || acceptance.State == "changes_requested")
		var childID *uuid.UUID
		childErr := s.pool.QueryRow(ctx, `SELECT child_task_id FROM agent_task_feedback_cycles WHERE parent_task_id=$1 AND (source_review_run_id IS NOT NULL OR internal_repair_kind<>'') ORDER BY created_at DESC LIMIT 1`, task.ID).Scan(&childID)
		if childErr != nil && !errors.Is(childErr, pgx.ErrNoRows) {
			return domain.AgentTaskDetail{}, childErr
		}
		acceptance.RemediationTaskID = childID

		detail.Acceptance = &acceptance
	} else if !errors.Is(acceptanceErr, pgx.ErrNoRows) {
		return domain.AgentTaskDetail{}, acceptanceErr
	}
	detail.PlanPermissions = agentTaskPlanPermissions(detail, role, actor)
	if task.Workflow.Enabled {
		var used int
		if err := s.pool.QueryRow(ctx, `SELECT COALESCE(sum(a.attempt),0) FROM agent_task_attempts a JOIN agent_tasks t ON t.id=a.task_id WHERE t.tenant_id=$1 AND t.execution_branch=$2`, task.TenantID, task.ExecutionBranch).Scan(&used); err != nil {
			return domain.AgentTaskDetail{}, err
		}
		if used >= task.Workflow.MaxTaskAttempts {
			detail.PlanPermissions.CanCreatePlan = false
			detail.PlanPermissions.CanApprovePlan = false
			detail.PlanPermissions.CreateBlockReason = "execution_budget_exhausted"
			detail.PlanPermissions.ApproveBlockReason = "execution_budget_exhausted"
		}
	}
	return detail, nil
}

func agentTaskPlanPermissions(detail domain.AgentTaskDetail, role, actor string) domain.AgentTaskPlanPermissions {
	result := domain.AgentTaskPlanPermissions{}
	var latestClassification domain.AgentTaskClassification
	if len(detail.Classifications) > 0 {
		latestClassification = detail.Classifications[0]
	}
	if !canManageAgentTasks(role) {
		result.CreateBlockReason = "reviewer_role_required"
	} else if detail.Task.State != "received" && detail.Task.State != "awaiting_approval" && detail.Task.State != "needs_attention" {
		result.CreateBlockReason = "task_not_plannable"
	} else if detail.Task.SourceState != "ready" || !(domain.AgentTaskSourceSnapshot{BaseRef: detail.Task.SourceBaseRef, BaseSHA: detail.Task.SourceBaseSHA}).Valid() {
		result.CreateBlockReason = "source_not_ready"
	} else if latestClassification.Decision != "requires_human" {
		result.CreateBlockReason = "classification_not_eligible"
	} else {
		result.CanCreatePlan = true
	}

	if role != "owner" && role != "admin" {
		result.ApproveBlockReason = "owner_admin_required"
	} else if detail.Task.State != "awaiting_approval" || len(detail.Plans) == 0 || detail.Plans[0].State != "awaiting_approval" {
		result.ApproveBlockReason = "no_pending_plan"
	} else if latestClassification.Decision != "requires_human" {
		result.ApproveBlockReason = "classification_not_eligible"
	} else if (latestClassification.RiskLevel == "high" || latestClassification.RiskLevel == "critical") && detail.Plans[0].CreatedBy == actor {
		result.ApproveBlockReason = "separate_approver_required"
	} else {
		result.CanApprovePlan = true
	}
	return result
}

func (s *PostgresStore) CreateAgentTaskPlan(ctx context.Context, actor, tenantSlug string, taskID uuid.UUID, input domain.AgentTaskPlanInput) (domain.AgentTaskPlan, error) {
	if taskID == uuid.Nil || !input.Valid() {
		return domain.AgentTaskPlan{}, ErrInvalidAgentTaskPlan
	}
	summary := input.CanonicalSummary()
	sections := domain.AgentTaskPlanSections{}
	if input.Sections != nil {
		sections = input.Sections.Normalized()
	}
	sectionsJSON, err := json.Marshal(sections)
	if err != nil {
		return domain.AgentTaskPlan{}, fmt.Errorf("encode agent task plan sections: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.AgentTaskPlan{}, fmt.Errorf("begin agent task plan: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.AgentTaskPlan{}, err
	}
	if !canManageAgentTasks(role) {
		return domain.AgentTaskPlan{}, ErrForbidden
	}
	task, err := scanAgentTask(tx.QueryRow(ctx, `SELECT `+agentTaskColumns+` FROM agent_tasks WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenantID, taskID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AgentTaskPlan{}, ErrNotFound
	}
	if err != nil {
		return domain.AgentTaskPlan{}, fmt.Errorf("load agent task for plan: %w", err)
	}
	if task.State == "cancelled" || task.State == "rejected" || task.State == "superseded" || task.State == "execution_queued" || task.State == "executing" || task.State == "completed" {
		return domain.AgentTaskPlan{}, ErrInvalidAgentTaskPlan
	}
	// A coding plan must name the exact source commit it is allowed to edit.
	// The provider-reading worker owns this state; Console/API callers cannot
	// manufacture a mutable branch name or source revision themselves.
	if task.SourceState != "ready" || !(domain.AgentTaskSourceSnapshot{BaseRef: task.SourceBaseRef, BaseSHA: task.SourceBaseSHA}).Valid() {
		return domain.AgentTaskPlan{}, ErrInvalidAgentTaskPlan
	}
	classification, err := latestAgentTaskClassification(ctx, tx, task.ID)
	if err != nil {
		return domain.AgentTaskPlan{}, err
	}
	if classification.Decision != "requires_human" {
		return domain.AgentTaskPlan{}, ErrInvalidAgentTaskPlan
	}
	if err := checkAgentWorkflowBudgetTx(ctx, tx, task); err != nil {
		return domain.AgentTaskPlan{}, err
	}
	if err := requireAgentWorkflowCriteriaTx(ctx, tx, task, sections); err != nil {
		return domain.AgentTaskPlan{}, err
	}
	var nextRevision int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(revision),0)+1 FROM agent_task_plans WHERE task_id=$1`, taskID).Scan(&nextRevision); err != nil {
		return domain.AgentTaskPlan{}, err
	}
	digest := sha256.Sum256([]byte(summary))
	result, err := scanAgentTaskPlan(tx.QueryRow(ctx, `INSERT INTO agent_task_plans(task_id,revision,summary,sections,plan_sha256,created_by) VALUES($1,$2,$3,$4,$5,$6) RETURNING `+agentTaskPlanColumns, taskID, nextRevision, summary, sectionsJSON, hex.EncodeToString(digest[:]), actor))
	if err != nil {
		return domain.AgentTaskPlan{}, fmt.Errorf("create agent task plan: %w", err)
	}
	// A newer plan is a replacement, not an additional approval choice. Keep
	// the earlier text as audit evidence, but make its approval impossible in
	// the same transaction that publishes the new revision.
	if _, err = tx.Exec(ctx, `UPDATE agent_task_plans SET state='superseded' WHERE task_id=$1 AND id<>$2 AND state='awaiting_approval'`, taskID, result.ID); err != nil {
		return domain.AgentTaskPlan{}, fmt.Errorf("supersede earlier agent task plans: %w", err)
	}
	if _, err = tx.Exec(ctx, `UPDATE agent_tasks SET state='awaiting_approval',revision=revision+1,updated_at=now() WHERE id=$1`, taskID); err != nil {
		return domain.AgentTaskPlan{}, err
	}
	task.State = "awaiting_approval"
	task.Revision++
	// The source-status marker is version-fenced. Reusing it for a new plan
	// replaces the earlier "source ready" message with the exact current
	// approval contract instead of leaving a stale actionable comment behind.
	if err = queueAgentTaskSourceProviderComment(ctx, tx, task, "plan", "", ""); err != nil {
		return domain.AgentTaskPlan{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata) VALUES($1,$2,'agent_task.plan_created',$3,jsonb_build_object('task_id',$4::text,'plan_revision',$5::int,'plan_sha256',$6::text))`, tenantID, actor, result.ID.String(), taskID, result.Revision, result.PlanSHA256); err != nil {
		return domain.AgentTaskPlan{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.AgentTaskPlan{}, fmt.Errorf("commit agent task plan: %w", err)
	}
	return result, nil
}

func (s *PostgresStore) ApproveAgentTaskPlan(ctx context.Context, actor, tenantSlug string, taskID, planID uuid.UUID, input domain.AgentTaskPlanApprovalInput) (domain.AgentTaskPlan, error) {
	if taskID == uuid.Nil || planID == uuid.Nil || !input.Valid() {
		return domain.AgentTaskPlan{}, ErrInvalidAgentTaskPlan
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.AgentTaskPlan{}, fmt.Errorf("begin agent task plan approval: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.AgentTaskPlan{}, err
	}
	if role != "owner" && role != "admin" {
		return domain.AgentTaskPlan{}, ErrForbidden
	}
	task, err := scanAgentTask(tx.QueryRow(ctx, `SELECT `+agentTaskColumns+` FROM agent_tasks WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenantID, taskID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AgentTaskPlan{}, ErrNotFound
	}
	if err != nil {
		return domain.AgentTaskPlan{}, err
	}
	result, err := approveAgentTaskPlanTx(ctx, tx, tenantID, actor, role, task, planID, input)
	if err != nil {
		return domain.AgentTaskPlan{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.AgentTaskPlan{}, fmt.Errorf("commit agent task plan approval: %w", err)
	}
	return result, nil
}

// approveAgentTaskPlanTx is shared by Console and verified Issue commands so
// both admission paths enforce the same risk, revision and duty-separation
// invariants before queuing execution in their caller's transaction.
func approveAgentTaskPlanTx(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, actor, role string, task domain.AgentTask, planID uuid.UUID, input domain.AgentTaskPlanApprovalInput) (domain.AgentTaskPlan, error) {
	if role != "owner" && role != "admin" {
		return domain.AgentTaskPlan{}, ErrForbidden
	}
	if planID == uuid.Nil || !input.Valid() {
		return domain.AgentTaskPlan{}, ErrInvalidAgentTaskPlan
	}
	if task.State != "awaiting_approval" {
		return domain.AgentTaskPlan{}, ErrConflict
	}
	if err := checkAgentWorkflowBudgetTx(ctx, tx, task); err != nil {
		return domain.AgentTaskPlan{}, err
	}
	if task.Workflow.Enabled {
		plan, e := scanAgentTaskPlan(tx.QueryRow(ctx, `SELECT `+agentTaskPlanColumns+` FROM agent_task_plans WHERE id=$1 AND task_id=$2`, planID, task.ID))
		if e != nil || requireAgentWorkflowCriteriaTx(ctx, tx, task, plan.Sections) != nil {
			return domain.AgentTaskPlan{}, ErrInvalidAgentTaskPlan
		}
	}
	classification, err := latestAgentTaskClassification(ctx, tx, task.ID)
	if err != nil {
		return domain.AgentTaskPlan{}, err
	}
	if classification.Decision != "requires_human" {
		return domain.AgentTaskPlan{}, ErrInvalidAgentTaskPlan
	}
	if classification.RiskLevel == "high" || classification.RiskLevel == "critical" {
		var createdBy string
		err = tx.QueryRow(ctx, `SELECT created_by FROM agent_task_plans WHERE id=$1 AND task_id=$2 AND revision=$3 AND state='awaiting_approval'`, planID, task.ID, input.Revision).Scan(&createdBy)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.AgentTaskPlan{}, ErrConflict
		}
		if err != nil {
			return domain.AgentTaskPlan{}, fmt.Errorf("load high-risk agent plan creator: %w", err)
		}
		if createdBy == actor {
			return domain.AgentTaskPlan{}, ErrForbidden
		}
	}
	result, err := scanAgentTaskPlan(tx.QueryRow(ctx, `UPDATE agent_task_plans SET state='approved',approved_by=$3,approved_at=now() WHERE id=$1 AND task_id=$2 AND revision=$4 AND state='awaiting_approval' AND revision=(SELECT MAX(revision) FROM agent_task_plans WHERE task_id=$2) RETURNING `+agentTaskPlanColumns, planID, task.ID, actor, input.Revision))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AgentTaskPlan{}, ErrConflict
	}
	if err != nil {
		return domain.AgentTaskPlan{}, fmt.Errorf("approve agent task plan: %w", err)
	}
	if _, err = tx.Exec(ctx, `UPDATE agent_task_plans SET state='superseded' WHERE task_id=$1 AND id<>$2 AND state='awaiting_approval'`, task.ID, planID); err != nil {
		return domain.AgentTaskPlan{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE agent_tasks SET state='execution_queued',revision=revision+1,updated_at=now() WHERE id=$1`, task.ID); err != nil {
		return domain.AgentTaskPlan{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbox_messages(aggregate_type,aggregate_id,topic,dedupe_key,payload) VALUES('agent_task',$1,'agent.task.execute.requested',$2,jsonb_build_object('task_id',$1::uuid::text,'task_revision',$3::int,'plan_id',$4::uuid::text,'plan_revision',$5::int,'plan_sha256',$6::text)) ON CONFLICT(dedupe_key) DO NOTHING`, task.ID, fmt.Sprintf("agent-task:%s:%d:execute", task.ID, task.Revision+1), task.Revision+1, planID, result.Revision, result.PlanSHA256); err != nil {
		return domain.AgentTaskPlan{}, fmt.Errorf("queue approved agent task: %w", err)
	}
	task.State = "execution_queued"
	task.Revision++
	if err = queueAgentTaskSourceProviderComment(ctx, tx, task, "approved", "", ""); err != nil {
		return domain.AgentTaskPlan{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata) VALUES($1,$2,'agent_task.plan_approved',$3,jsonb_build_object('task_id',$4::text,'plan_revision',$5::int,'plan_sha256',$6::text))`, tenantID, actor, result.ID.String(), task.ID, result.Revision, result.PlanSHA256); err != nil {
		return domain.AgentTaskPlan{}, err
	}
	return result, nil
}

// CancelAgentTask is the operator kill-switch for any nonterminal Agent task.
// It supersedes a live adapter lease inside the same transaction so a late
// callback cannot complete the task after cancellation. A provider write
// already in flight is not atomically rolled back by this local transaction.
func (s *PostgresStore) CancelAgentTask(ctx context.Context, actor, tenantSlug string, taskID uuid.UUID, input domain.AgentTaskCancellationInput) (domain.AgentTask, error) {
	input.Reason = strings.TrimSpace(input.Reason)
	if taskID == uuid.Nil || !input.Valid() {
		return domain.AgentTask{}, ErrInvalidAgentTask
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.AgentTask{}, fmt.Errorf("begin agent task cancellation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.AgentTask{}, err
	}
	if !canManageAgentTasks(role) {
		return domain.AgentTask{}, ErrForbidden
	}
	task, err := scanAgentTask(tx.QueryRow(ctx, `SELECT `+agentTaskColumns+` FROM agent_tasks WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenantID, taskID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AgentTask{}, ErrNotFound
	}
	if err != nil {
		return domain.AgentTask{}, fmt.Errorf("load agent task cancellation: %w", err)
	}
	if task.Revision != input.Revision || !agentTaskStateCancellable(task.State) {
		return domain.AgentTask{}, ErrConflict
	}
	task, err = cancelAgentTaskTx(ctx, tx, task, actor, input.Reason)
	if err != nil {
		return domain.AgentTask{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.AgentTask{}, fmt.Errorf("commit agent task cancellation: %w", err)
	}
	return task, nil
}

func agentTaskStateCancellable(state string) bool {
	return state == "received" || state == "awaiting_approval" || state == "execution_queued" || state == "executing" || state == "needs_attention"
}

func cancelAgentTaskTx(ctx context.Context, tx pgx.Tx, task domain.AgentTask, actor, reason string) (domain.AgentTask, error) {
	if !agentTaskStateCancellable(task.State) {
		return domain.AgentTask{}, ErrConflict
	}
	// Capture the exact adapter jobs while they are still running and lock
	// them before revoking their leases. The durable cancellation message is
	// emitted only for jobs an adapter has actually accepted; queued or local
	// attempts have nothing remote to stop.
	type adapterCancellation struct {
		attemptID    uuid.UUID
		adapterJobID string
	}
	rows, err := tx.Query(ctx, `SELECT id,adapter_job_id FROM agent_task_attempts WHERE task_id=$1 AND state='running' AND adapter_job_id IS NOT NULL AND adapter_job_id<>'' FOR UPDATE`, task.ID)
	if err != nil {
		return domain.AgentTask{}, fmt.Errorf("load running agent adapter jobs for cancellation: %w", err)
	}
	var cancellations []adapterCancellation
	for rows.Next() {
		var item adapterCancellation
		if err := rows.Scan(&item.attemptID, &item.adapterJobID); err != nil {
			rows.Close()
			return domain.AgentTask{}, fmt.Errorf("scan running agent adapter job for cancellation: %w", err)
		}
		cancellations = append(cancellations, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return domain.AgentTask{}, fmt.Errorf("iterate running agent adapter jobs for cancellation: %w", err)
	}
	rows.Close()
	if _, err := tx.Exec(ctx, `UPDATE agent_task_attempts SET state='superseded',locked_until=NULL,error_code='cancelled_by_operator',error_message=$2,result_summary=$2,finished_at=now(),updated_at=now() WHERE task_id=$1 AND state='running'`, task.ID, reason); err != nil {
		return domain.AgentTask{}, fmt.Errorf("supersede running agent attempts: %w", err)
	}
	updated, err := scanAgentTask(tx.QueryRow(ctx, `UPDATE agent_tasks SET state='cancelled',revision=revision+1,updated_at=now() WHERE id=$1 AND revision=$2 RETURNING `+agentTaskColumns, task.ID, task.Revision))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AgentTask{}, ErrConflict
	}
	if err != nil {
		return domain.AgentTask{}, fmt.Errorf("cancel agent task: %w", err)
	}
	for _, cancellation := range cancellations {
		if err := queueAgentAdapterCancellation(ctx, tx, task.ID, cancellation.attemptID, cancellation.adapterJobID); err != nil {
			return domain.AgentTask{}, fmt.Errorf("queue agent adapter cancellation: %w", err)
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata) VALUES($1,$2,'agent_task.cancelled',$3,jsonb_build_object('previous_state',$4::text,'reason',$5::text,'revision',$6::int))`, task.TenantID, actor, task.ID.String(), task.State, reason, updated.Revision); err != nil {
		return domain.AgentTask{}, fmt.Errorf("audit agent task cancellation: %w", err)
	}
	return updated, nil
}

// ExpireAgentTaskAttempts is the watchdog terminal boundary for adapters that
// stop heartbeating. It intentionally does not reclaim or re-run code work:
// an expired lease becomes needs_attention, keeps its evidence, and asks the
// remote adapter to reclaim only the matching sandbox job.
func (s *PostgresStore) ExpireAgentTaskAttempts(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		return 0, ErrInvalidAgentTask
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin agent task expiration: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT a.id,a.task_id,a.adapter_job_id,t.tenant_id,CASE WHEN a.deadline_at IS NOT NULL AND a.deadline_at <= now() THEN 'agent_execution_deadline_exceeded' ELSE 'agent_adapter_lease_expired' END FROM agent_task_attempts a JOIN agent_tasks t ON t.id=a.task_id WHERE a.state='running' AND (a.locked_until < now() OR (a.deadline_at IS NOT NULL AND a.deadline_at <= now())) ORDER BY LEAST(a.locked_until,COALESCE(a.deadline_at,a.locked_until)) ASC,a.id ASC LIMIT $1 FOR UPDATE OF a SKIP LOCKED`, limit)
	if err != nil {
		return 0, fmt.Errorf("select expired agent task attempts: %w", err)
	}
	type expiredAttempt struct {
		attemptID    uuid.UUID
		taskID       uuid.UUID
		adapterJobID string
		tenantID     uuid.UUID
		code         string
	}
	var expired []expiredAttempt
	for rows.Next() {
		var item expiredAttempt
		if err := rows.Scan(&item.attemptID, &item.taskID, &item.adapterJobID, &item.tenantID, &item.code); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan expired agent task attempt: %w", err)
		}
		expired = append(expired, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("iterate expired agent task attempts: %w", err)
	}
	rows.Close()
	for _, item := range expired {
		message := "The isolated adapter did not renew its execution lease before the deadline. No retry or provider write was started automatically."
		if item.code == "agent_execution_deadline_exceeded" {
			message = "The approved maximum execution time elapsed. The adapter lease was revoked and no retry or provider write was started automatically."
		}
		if _, err := tx.Exec(ctx, `UPDATE agent_task_attempts SET state='needs_attention',locked_until=NULL,error_code=$2,error_message=$3,result_summary=$3,finished_at=now(),updated_at=now() WHERE id=$1 AND state='running'`, item.attemptID, item.code, message); err != nil {
			return 0, fmt.Errorf("expire agent task attempt: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE agent_tasks SET state='needs_attention',revision=revision+1,updated_at=now() WHERE id=$1 AND state='executing'`, item.taskID); err != nil {
			return 0, fmt.Errorf("mark expired agent task needs attention: %w", err)
		}
		if err := queueAgentTaskAttemptStatus(ctx, tx, item.taskID, item.attemptID, item.code, message, item.adapterJobID != ""); err != nil {
			return 0, err
		}
		if item.adapterJobID != "" {
			if err := queueAgentAdapterCancellation(ctx, tx, item.taskID, item.attemptID, item.adapterJobID); err != nil {
				return 0, fmt.Errorf("queue expired agent adapter cancellation: %w", err)
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata) VALUES($1,'worker:agent-task-reaper','agent_task.attempt_expired',$2,jsonb_build_object('adapter_job_id',$3::text,'code',$4::text))`, item.tenantID, item.attemptID.String(), item.adapterJobID, item.code); err != nil {
			return 0, fmt.Errorf("audit expired agent task attempt: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit agent task expiration: %w", err)
	}
	return len(expired), nil
}

// ExpireUnacknowledgedAgentTasks makes a lost provider acknowledgement
// recoverable in Agent Work. Source resolution cannot start before that ACK,
// and an unreachable provider or missing worker credential must not leave a
// task looking indefinitely in progress. No provider write is queued here:
// the very credential needed for such a comment may be unavailable.
func (s *PostgresStore) ExpireUnacknowledgedAgentTasks(ctx context.Context, limit int, olderThan time.Duration) (int, error) {
	if limit < 1 || limit > 100 || olderThan < 5*time.Minute || olderThan > 24*time.Hour {
		return 0, ErrInvalidAgentTask
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin agent acknowledgement expiration: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT t.id,t.tenant_id FROM agent_tasks t
		WHERE t.state='received' AND t.source_state='pending' AND t.created_at <= now()-$1::interval
		AND NOT EXISTS(SELECT 1 FROM outbox_messages m WHERE m.aggregate_id=t.id AND m.topic='agent.task.source.resolve.requested')
		ORDER BY t.created_at,t.id LIMIT $2 FOR UPDATE OF t SKIP LOCKED`, fmt.Sprintf("%d seconds", int(olderThan.Seconds())), limit)
	if err != nil {
		return 0, fmt.Errorf("select unacknowledged agent tasks: %w", err)
	}
	type candidate struct{ id, tenantID uuid.UUID }
	var candidates []candidate
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.id, &item.tenantID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan unacknowledged agent task: %w", err)
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("iterate unacknowledged agent tasks: %w", err)
	}
	rows.Close()
	expired := 0
	for _, item := range candidates {
		// Recheck under the row lock using a fresh READ COMMITTED snapshot.
		// An ACK may have enqueued source resolution while we waited for it.
		var released bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM outbox_messages WHERE aggregate_id=$1 AND topic='agent.task.source.resolve.requested')`, item.id).Scan(&released); err != nil {
			return 0, fmt.Errorf("check acknowledged agent source: %w", err)
		}
		if released {
			continue
		}
		var revision int
		if err := tx.QueryRow(ctx, `UPDATE agent_tasks SET source_state='failed',state='needs_attention',revision=revision+1,updated_at=now() WHERE id=$1 RETURNING revision`, item.id).Scan(&revision); err != nil {
			return 0, fmt.Errorf("expire unacknowledged agent task: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata) VALUES($1,'worker:agent-task-runner','agent_task.acknowledgement_timeout',$2,jsonb_build_object('revision',$3::int,'timeout_seconds',$4::int))`, item.tenantID, item.id.String(), revision, int(olderThan.Seconds())); err != nil {
			return 0, fmt.Errorf("audit unacknowledged agent task: %w", err)
		}
		expired++
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit agent acknowledgement expiration: %w", err)
	}
	return expired, nil
}

func queueAgentAdapterCancellation(ctx context.Context, tx pgx.Tx, taskID, attemptID uuid.UUID, adapterJobID string) error {
	adapterJobID = strings.TrimSpace(adapterJobID)
	if taskID == uuid.Nil || attemptID == uuid.Nil || adapterJobID == "" {
		return ErrInvalidAgentTask
	}
	_, err := tx.Exec(ctx, `INSERT INTO outbox_messages(aggregate_type,aggregate_id,topic,dedupe_key,payload) VALUES('agent_task',$1,'agent.task.cancel.requested',$2,jsonb_build_object('task_id',$1::uuid::text,'attempt_id',$3::uuid::text,'adapter_job_id',$4::text)) ON CONFLICT(dedupe_key) DO NOTHING`, taskID, fmt.Sprintf("agent-task:%s:adapter-cancel", attemptID), attemptID, adapterJobID)
	return err
}

func queueAgentTaskSourceResolution(ctx context.Context, tx pgx.Tx, taskID uuid.UUID) error {
	if taskID == uuid.Nil {
		return ErrInvalidAgentTask
	}
	_, err := tx.Exec(ctx, `INSERT INTO outbox_messages(aggregate_type,aggregate_id,topic,dedupe_key,payload)
		SELECT 'agent_task',t.id,'agent.task.source.resolve.requested',$2,jsonb_build_object('task_id',t.id::text,'task_revision',t.revision)
		FROM agent_tasks t WHERE t.id=$1 ON CONFLICT(dedupe_key) DO NOTHING`, taskID, "agent-task:"+taskID.String()+":source")
	if err != nil {
		return fmt.Errorf("queue agent task source resolution: %w", err)
	}
	return nil
}

// WithAgentTaskAcknowledgementFence is the provider-confirmed ordering
// barrier for Issue commands, automatic Issue candidates and Draft feedback.
// The provider write and source release are serialized with cancellation,
// source failure, and new revisions; a redelivery uses the same marker.
func (s *PostgresStore) WithAgentTaskAcknowledgementFence(ctx context.Context, response domain.InteractionResponse, publish func(context.Context) error) error {
	release := response.SourceRelease
	if publish == nil || release == nil || release.TaskID == uuid.Nil || release.Revision < 1 || response.TenantID == uuid.Nil || !response.Provider.Valid() || response.APIBaseURL == "" || response.InstallationExternalID == "" || response.CredentialRef == "" || response.Repository == "" || response.ReviewNumber < 1 {
		return ErrInvalidAgentTask
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin acknowledged agent source release: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	installation, err := resolveInboundInstallation(ctx, tx, response.Provider, response.APIBaseURL, response.InstallationExternalID, response.Repository)
	if err != nil {
		return err
	}
	if installation.TenantID != response.TenantID || installation.CredentialRef != response.CredentialRef {
		return ErrConflict
	}
	task, err := scanAgentTask(tx.QueryRow(ctx, `SELECT `+agentTaskColumns+` FROM agent_tasks WHERE id=$1 FOR UPDATE`, release.TaskID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("load acknowledged agent task: %w", err)
	}
	if task.TenantID != installation.TenantID || task.InstallationID != installation.ID || task.Provider != response.Provider || task.APIBaseURL != installation.APIBaseURL || task.Repository != response.Repository || task.OriginNumber != response.ReviewNumber || (task.OriginKind == "issue" && response.ResourceKind != "issue") || (task.OriginKind == "pull_request" && response.ResourceKind != "merge_request") {
		return ErrConflict
	}
	var bound bool
	switch {
	case response.Marker == "open-review-platform:agent-task:auto:"+task.ID.String():
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_task_interactions WHERE task_id=$1 AND command='auto' AND result='accepted' AND tenant_id=$2)`, task.ID, task.TenantID).Scan(&bound)
	case strings.HasPrefix(response.Marker, "open-review-platform:agent-task-feedback:"):
		feedbackID, parseErr := uuid.Parse(strings.TrimPrefix(response.Marker, "open-review-platform:agent-task-feedback:"))
		if parseErr != nil || feedbackID == uuid.Nil {
			return ErrConflict
		}
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_task_feedback_cycles WHERE id=$1 AND child_task_id=$2 AND tenant_id=$3 AND provider=$4 AND repository=$5 AND pull_request_number=$6 AND state='received')`, feedbackID, task.ID, task.TenantID, response.Provider, response.Repository, response.ReviewNumber).Scan(&bound)
	case strings.HasPrefix(response.Marker, "open-review-platform:agent-task:"):
		interactionID, parseErr := uuid.Parse(strings.TrimPrefix(response.Marker, "open-review-platform:agent-task:"))
		if parseErr != nil || interactionID == uuid.Nil {
			return ErrConflict
		}
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_task_interactions WHERE id=$1 AND task_id=$2 AND tenant_id=$3 AND provider=$4 AND repository=$5 AND issue_number=$6 AND command='implement' AND result='accepted')`, interactionID, task.ID, task.TenantID, response.Provider, response.Repository, response.ReviewNumber).Scan(&bound)
	default:
		return ErrConflict
	}
	if err != nil {
		return fmt.Errorf("check agent acknowledgement binding: %w", err)
	}
	if !bound {
		return ErrConflict
	}
	if task.Revision != release.Revision || task.State != "received" || task.SourceState != "pending" {
		return tx.Commit(ctx)
	}
	if err := publish(ctx); err != nil {
		return err
	}
	if err := queueAgentTaskSourceResolution(ctx, tx, task.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RetryAgentTaskSource re-opens only a failed, never-planned source capture.
// If the provider acknowledgement never released the source, retry that
// acknowledgement first. The source worker must re-read the exact provider
// Issue/PR and current base before it can store new classification evidence;
// this API never supplies or reuses a model verdict, source SHA, or execution
// capability.
func (s *PostgresStore) RetryAgentTaskSource(ctx context.Context, actor, tenantSlug string, taskID uuid.UUID, expectedRevision int) (domain.AgentTask, error) {
	if taskID == uuid.Nil || expectedRevision < 1 {
		return domain.AgentTask{}, ErrInvalidAgentTask
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.AgentTask{}, fmt.Errorf("begin agent task source retry: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.AgentTask{}, err
	}
	if !canManageAgentTasks(role) {
		return domain.AgentTask{}, ErrForbidden
	}
	task, err := scanAgentTask(tx.QueryRow(ctx, `SELECT `+agentTaskColumns+` FROM agent_tasks WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenantID, taskID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AgentTask{}, ErrNotFound
	}
	if err != nil {
		return domain.AgentTask{}, fmt.Errorf("load agent task source retry: %w", err)
	}
	if task.Revision != expectedRevision {
		return domain.AgentTask{}, ErrRevisionConflict
	}
	if task.SourceState != "failed" || task.State != "needs_attention" || task.SourceBaseRef != "" || task.SourceBaseSHA != "" {
		return domain.AgentTask{}, ErrConflict
	}
	var plans, attempts int
	if err := tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM agent_task_plans WHERE task_id=$1),(SELECT count(*) FROM agent_task_attempts WHERE task_id=$1)`, taskID).Scan(&plans, &attempts); err != nil {
		return domain.AgentTask{}, fmt.Errorf("check agent task source retry history: %w", err)
	}
	if plans != 0 || attempts != 0 {
		return domain.AgentTask{}, ErrConflict
	}
	var active bool
	var verificationState, scope, externalID, credentialRef, apiBaseURL string
	if err := tx.QueryRow(ctx, `SELECT active,verification_state,repository_scope,external_id,credential_ref,api_base_url FROM provider_installations WHERE tenant_id=$1 AND id=$2 FOR SHARE`, tenantID, task.InstallationID).Scan(&active, &verificationState, &scope, &externalID, &credentialRef, &apiBaseURL); errors.Is(err, pgx.ErrNoRows) {
		return domain.AgentTask{}, ErrUnknownInstallation
	} else if err != nil {
		return domain.AgentTask{}, fmt.Errorf("load agent task source installation: %w", err)
	}
	if !active || (verificationState != "legacy" && verificationState != "verified") || apiBaseURL != task.APIBaseURL {
		return domain.AgentTask{}, ErrUnknownInstallation
	}
	if !repositoryScopeAllows(scope, task.Repository) {
		return domain.AgentTask{}, ErrForbidden
	}
	var sourceReleased bool
	// Only the stable first-release row proves that the provider ACK passed the
	// task-row fence. An older retry row alone is not such evidence.
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM outbox_messages WHERE aggregate_id=$1 AND topic='agent.task.source.resolve.requested' AND dedupe_key=$2)`, taskID, "agent-task:"+taskID.String()+":source").Scan(&sourceReleased); err != nil {
		return domain.AgentTask{}, fmt.Errorf("check agent task acknowledgement: %w", err)
	}
	var acknowledgementID uuid.UUID
	if !sourceReleased {
		resourceKind := "issue"
		if task.OriginKind == "pull_request" {
			resourceKind = "merge_request"
		}
		err = tx.QueryRow(ctx, `SELECT id FROM outbox_messages WHERE topic='review.interaction.response'
			AND payload->>'release_agent_task_source_id'=$1
			AND payload ? 'release_agent_task_source_revision'
			AND payload->>'tenant_id'=$2 AND payload->>'provider'=$3
			AND payload->>'api_base_url'=$4 AND payload->>'installation_external_id'=$5
			AND payload->>'credential_ref'=$6 AND payload->>'repository'=$7
			AND payload->>'resource_kind'=$8 AND payload->>'review_number'=$9
			ORDER BY created_at DESC,id DESC LIMIT 1`, taskID.String(), tenantID.String(), string(task.Provider), task.APIBaseURL, externalID, credentialRef, task.Repository, resourceKind, fmt.Sprint(task.OriginNumber)).Scan(&acknowledgementID)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.AgentTask{}, ErrConflict
		}
		if err != nil {
			return domain.AgentTask{}, fmt.Errorf("load original agent acknowledgement: %w", err)
		}
	}
	task, err = scanAgentTask(tx.QueryRow(ctx, `UPDATE agent_tasks SET source_state='pending',state='received',revision=revision+1,updated_at=now() WHERE id=$1 RETURNING `+agentTaskColumns, taskID))
	if err != nil {
		return domain.AgentTask{}, fmt.Errorf("reopen agent task source: %w", err)
	}
	auditAction := "agent_task.source_retry_requested"
	if sourceReleased {
		// A prior provider-visible acknowledgement already released source work.
		// Only this branch may queue the source worker directly.
		_, err = tx.Exec(ctx, `INSERT INTO outbox_messages(aggregate_type,aggregate_id,topic,dedupe_key,payload) VALUES('agent_task',$1,'agent.task.source.resolve.requested',$2,jsonb_build_object('task_id',$1::uuid::text,'task_revision',$3::int))`, taskID, fmt.Sprintf("agent-task:%s:source-retry:%d", taskID, task.Revision), task.Revision)
		if err != nil {
			return domain.AgentTask{}, fmt.Errorf("queue agent task source retry: %w", err)
		}
	} else {
		// Reuse the immutable provider acknowledgement and its stable comment
		// marker. Only the task revision changes. The responder's task-row fence
		// releases source after the provider confirms publication.
		result, queueErr := tx.Exec(ctx, `INSERT INTO outbox_messages(aggregate_type,aggregate_id,topic,dedupe_key,payload)
			SELECT aggregate_type,aggregate_id,'review.interaction.response',$2,
				jsonb_set(payload,'{release_agent_task_source_revision}',to_jsonb($3::int),false)
			FROM outbox_messages WHERE id=$1`, acknowledgementID, fmt.Sprintf("agent-task:%s:ack-retry:%d", taskID, task.Revision), task.Revision)
		if queueErr != nil {
			return domain.AgentTask{}, fmt.Errorf("queue agent task acknowledgement retry: %w", queueErr)
		}
		if result.RowsAffected() != 1 {
			return domain.AgentTask{}, ErrConflict
		}
		auditAction = "agent_task.acknowledgement_retry_requested"
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata) VALUES($1,$2,$3,$4,jsonb_build_object('origin_revision',$5::text,'task_revision',$6::int))`, tenantID, actor, auditAction, taskID.String(), task.OriginRevision, task.Revision); err != nil {
		return domain.AgentTask{}, fmt.Errorf("audit agent task source retry: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.AgentTask{}, fmt.Errorf("commit agent task source retry: %w", err)
	}
	return task, nil
}

func scanAgentTask(row rowScanner) (domain.AgentTask, error) {
	var item domain.AgentTask
	err := row.Scan(&item.ID, &item.TenantID, &item.InstallationID, &item.Provider, &item.APIBaseURL, &item.Repository, &item.OriginKind, &item.OriginNumber, &item.OriginRevision, &item.Intent, &item.PolicyRevision, &item.MaxAttempts, &item.MaxExecutionSeconds, &item.MaxFeedbackCycles, &item.ExecutorProfile, &item.DecisionBackend, &item.FeedbackCycle, &item.ExecutionBranch, &item.ParentTaskID, &item.ParentAttemptID, &item.SourceState, &item.SourceBaseRef, &item.SourceBaseSHA, &item.SourceCapturedAt, &item.State, &item.Revision, &item.RequestedBy, &item.CreatedAt, &item.UpdatedAt, &item.Workflow)
	return item, err
}

func scanAgentTaskPlan(row rowScanner) (domain.AgentTaskPlan, error) {
	var plan domain.AgentTaskPlan
	var rawSections []byte
	if err := row.Scan(&plan.ID, &plan.TaskID, &plan.Revision, &plan.State, &plan.Summary, &rawSections, &plan.PlanSHA256, &plan.CreatedBy, &plan.ApprovedBy, &plan.ApprovedAt, &plan.CreatedAt); err != nil {
		return domain.AgentTaskPlan{}, err
	}
	if err := json.Unmarshal(rawSections, &plan.Sections); err != nil {
		return domain.AgentTaskPlan{}, fmt.Errorf("decode agent task plan sections: %w", err)
	}
	if err := validateAgentTaskPlanIntegrity(plan); err != nil {
		return domain.AgentTaskPlan{}, err
	}
	return plan, nil
}

func validateAgentTaskPlanIntegrity(plan domain.AgentTaskPlan) error {
	if (plan.Sections.Objective != "" || plan.Sections.Scope != "" || plan.Sections.Verification != "" || plan.Sections.Risks != "" || plan.Sections.Unknowns != "" || len(plan.Sections.AcceptanceCriteria) > 0) && (!plan.Sections.Valid() || plan.Sections.Summary() != plan.Summary) {
		return fmt.Errorf("agent task plan sections disagree with approved summary")
	}
	digest := sha256.Sum256([]byte(plan.Summary))
	if plan.PlanSHA256 != hex.EncodeToString(digest[:]) {
		return fmt.Errorf("agent task plan summary does not match approval hash")
	}
	return nil
}

// LoadAgentTaskSourceTarget is intentionally worker-only. It returns the
// deployment-owned credential reference needed to read a repository's default
// branch, but that reference never appears in an outbox payload or adapter
// handoff.
func (s *PostgresStore) LoadAgentTaskSourceTarget(ctx context.Context, taskID uuid.UUID) (domain.AgentTaskSourceTarget, error) {
	if taskID == uuid.Nil {
		return domain.AgentTaskSourceTarget{}, ErrInvalidAgentTask
	}
	var target domain.AgentTaskSourceTarget
	err := s.pool.QueryRow(ctx, `SELECT t.`+strings.ReplaceAll(agentTaskColumns, ",", ",t.")+`,p.external_id,p.credential_ref
		FROM agent_tasks t JOIN provider_installations p ON p.id=t.installation_id
		WHERE t.id=$1 AND t.source_state='pending' AND t.state='received' AND p.active=TRUE AND p.verification_state IN ('legacy','verified')`, taskID).Scan(
		&target.Task.ID, &target.Task.TenantID, &target.Task.InstallationID, &target.Task.Provider, &target.Task.APIBaseURL, &target.Task.Repository,
		&target.Task.OriginKind, &target.Task.OriginNumber, &target.Task.OriginRevision, &target.Task.Intent, &target.Task.PolicyRevision,
		&target.Task.MaxAttempts, &target.Task.MaxExecutionSeconds, &target.Task.MaxFeedbackCycles, &target.Task.ExecutorProfile, &target.Task.DecisionBackend, &target.Task.FeedbackCycle, &target.Task.ExecutionBranch, &target.Task.ParentTaskID, &target.Task.ParentAttemptID, &target.Task.SourceState, &target.Task.SourceBaseRef, &target.Task.SourceBaseSHA,
		&target.Task.SourceCapturedAt, &target.Task.State, &target.Task.Revision, &target.Task.RequestedBy, &target.Task.CreatedAt, &target.Task.UpdatedAt, &target.Task.Workflow,
		&target.InstallationExternalID, &target.CredentialRef,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AgentTaskSourceTarget{}, ErrNoQueuedAgentTask
	}
	if err != nil {
		return domain.AgentTaskSourceTarget{}, fmt.Errorf("load agent task source target: %w", err)
	}
	if target.Task.OriginKind == "pull_request" {
		var feedback domain.AgentTaskFeedbackBinding
		err = s.pool.QueryRow(ctx, `SELECT comment_external_id,actor_external_id,instruction_sha256,source_review_run_id,system_instruction,internal_repair_kind,internal_repair_key FROM agent_task_feedback_cycles WHERE child_task_id=$1 AND state='received'`, taskID).Scan(&feedback.CommentExternalID, &feedback.ActorExternalID, &feedback.InstructionSHA256, &feedback.SourceReviewRunID, &feedback.SystemInstruction, &feedback.InternalRepairKind, &feedback.InternalRepairKey)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.AgentTaskSourceTarget{}, ErrInvalidAgentTask
		}
		if err != nil {
			return domain.AgentTaskSourceTarget{}, fmt.Errorf("load frozen agent feedback binding: %w", err)
		}
		feedback.TargetBranch, err = s.agentFeedbackTargetBranch(ctx, taskID)
		if err != nil || !feedback.ExecutionValid() {
			return domain.AgentTaskSourceTarget{}, ErrInvalidAgentTask
		}
		target.Feedback = &feedback
	}
	return target, nil
}

func (s *PostgresStore) agentFeedbackTargetBranch(ctx context.Context, childTaskID uuid.UUID) (string, error) {
	var branch string
	if err := s.pool.QueryRow(ctx, agentFeedbackTargetBranchSQL, childTaskID).Scan(&branch); err != nil {
		return "", err
	}
	return branch, nil
}

// RecordAgentTaskSourceSnapshot freezes the provider's default branch and
// commit before planning. Replayed broker deliveries are harmless only when
// they report the same immutable source pair.
func (s *PostgresStore) RecordAgentTaskSourceSnapshot(ctx context.Context, taskID uuid.UUID, snapshot domain.AgentTaskSourceSnapshot) (domain.AgentTask, error) {
	snapshot.BaseRef, snapshot.BaseSHA = strings.TrimSpace(snapshot.BaseRef), strings.TrimSpace(snapshot.BaseSHA)
	if taskID == uuid.Nil || !snapshot.Valid() {
		return domain.AgentTask{}, ErrInvalidAgentTask
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.AgentTask{}, fmt.Errorf("begin agent task source snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	task, err := scanAgentTask(tx.QueryRow(ctx, `SELECT `+agentTaskColumns+` FROM agent_tasks WHERE id=$1 FOR UPDATE`, taskID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AgentTask{}, ErrNotFound
	}
	if err != nil {
		return domain.AgentTask{}, fmt.Errorf("load agent task source snapshot: %w", err)
	}
	if task.SourceState == "ready" {
		if task.SourceBaseRef != snapshot.BaseRef || !strings.EqualFold(task.SourceBaseSHA, snapshot.BaseSHA) {
			return domain.AgentTask{}, ErrConflict
		}
		if err = tx.Commit(ctx); err != nil {
			return domain.AgentTask{}, err
		}
		return task, nil
	}
	if task.SourceState != "pending" || task.State != "received" {
		return domain.AgentTask{}, ErrNoQueuedAgentTask
	}
	if task.OriginKind == "issue" {
		if snapshot.Issue == nil || snapshot.Feedback != nil || strings.TrimSpace(snapshot.Issue.Title) == "" || snapshot.Issue.Revision != task.OriginRevision {
			return domain.AgentTask{}, ErrInvalidAgentTask
		}
		verifiedRevision := domain.AgentIssueRevision(task.Provider, task.APIBaseURL, task.Repository, task.OriginNumber, snapshot.Issue.Title, snapshot.Issue.Body)
		if task.RequestedBy == "policy:auto" {
			verifiedRevision = domain.AgentAutomaticIssueRevision(task.Provider, task.APIBaseURL, task.Repository, task.OriginNumber, snapshot.Issue.Title, snapshot.Issue.Body, snapshot.Issue.Labels)
		}
		if verifiedRevision != task.OriginRevision {
			return domain.AgentTask{}, ErrInvalidAgentTask
		}
	} else if task.OriginKind == "pull_request" {
		if snapshot.Feedback == nil || snapshot.Issue != nil || strings.TrimSpace(snapshot.Feedback.Instruction) == "" || snapshot.BaseRef != task.ExecutionBranch || !strings.EqualFold(snapshot.BaseSHA, task.OriginRevision) {
			return domain.AgentTask{}, ErrInvalidAgentTask
		}
		var originalTargetBranch string
		if err = tx.QueryRow(ctx, agentFeedbackTargetBranchSQL, taskID).Scan(&originalTargetBranch); err != nil || snapshot.TargetBranch != originalTargetBranch {
			return domain.AgentTask{}, ErrInvalidAgentTask
		}
		var binding domain.AgentTaskFeedbackBinding
		err = tx.QueryRow(ctx, `SELECT comment_external_id,actor_external_id,instruction_sha256,source_review_run_id,system_instruction,internal_repair_kind,internal_repair_key FROM agent_task_feedback_cycles WHERE child_task_id=$1 AND state='received'`, taskID).Scan(&binding.CommentExternalID, &binding.ActorExternalID, &binding.InstructionSHA256, &binding.SourceReviewRunID, &binding.SystemInstruction, &binding.InternalRepairKind, &binding.InternalRepairKey)
		if err != nil {
			return domain.AgentTask{}, fmt.Errorf("load agent feedback evidence: %w", err)
		}
		if binding.Internal() && !internalReviewBindingMatches(*snapshot.Feedback, binding) {
			return domain.AgentTask{}, ErrInvalidAgentTask
		}
		digest := sha256.Sum256([]byte(strings.TrimSpace(snapshot.Feedback.Instruction)))
		if snapshot.Feedback.CommentExternalID != binding.CommentExternalID || snapshot.Feedback.ActorExternalID != binding.ActorExternalID || hex.EncodeToString(digest[:]) != binding.InstructionSHA256 {
			return domain.AgentTask{}, ErrInvalidAgentTask
		}
	} else {
		return domain.AgentTask{}, ErrInvalidAgentTask
	}
	task, err = scanAgentTask(tx.QueryRow(ctx, `UPDATE agent_tasks SET source_state='ready',source_base_ref=$2,source_base_sha=$3,source_captured_at=now(),revision=revision+1,updated_at=now() WHERE id=$1 RETURNING `+agentTaskColumns, taskID, snapshot.BaseRef, snapshot.BaseSHA))
	if err != nil {
		return domain.AgentTask{}, fmt.Errorf("record agent task source snapshot: %w", err)
	}
	if snapshot.Issue != nil || snapshot.Feedback != nil {
		title, body := "", ""
		var labels []string
		if snapshot.Issue != nil {
			title, body, labels = snapshot.Issue.Title, snapshot.Issue.Body, snapshot.Issue.Labels
		} else {
			title, body = "Revise existing Draft PR", agentdecision.FeedbackClassificationBody(snapshot.Feedback.Instruction)
		}
		classification := agentdecision.Classify(task, title, body, labels)
		classification, err = agentdecision.ApplySignal(classification, snapshot.DecisionSignal, task.DecisionBackend)
		if err != nil {
			return domain.AgentTask{}, fmt.Errorf("validate frozen decision model evidence: %w", err)
		}
		if _, err = recordAgentTaskClassification(ctx, tx, task, classification); err != nil {
			return domain.AgentTask{}, err
		}
		if classification.Decision == "rejected" {
			task, err = scanAgentTask(tx.QueryRow(ctx, `UPDATE agent_tasks SET state='rejected',revision=revision+1,updated_at=now() WHERE id=$1 RETURNING `+agentTaskColumns, taskID))
			if err != nil {
				return domain.AgentTask{}, fmt.Errorf("reject unsafe current Issue snapshot: %w", err)
			}
		}
	}
	if task.Workflow.Enabled && snapshot.GeneratedPlan != nil {
		classification, classErr := latestAgentTaskClassification(ctx, tx, task.ID)
		if classErr != nil {
			return domain.AgentTask{}, classErr
		}
		if classification.Decision == "requires_human" {
			if snapshot.Issue != nil {
				snapshot.GeneratedPlan.SourceRequirements = strings.TrimSpace(snapshot.Issue.Body)
			} else {
				snapshot.GeneratedPlan.SourceRequirements = strings.TrimSpace(snapshot.Feedback.Instruction)
			}
			snapshot.GeneratedPlan.RepositoryEvidence = strings.TrimSpace(snapshot.RepositoryEvidence)
			if err = recordGeneratedAgentPlanTx(ctx, tx, &task, *snapshot.GeneratedPlan); err != nil {
				return domain.AgentTask{}, err
			}
		}
	}
	// The acknowledgement is intentionally early and cannot know the provider
	// revision or model verdict. Publish the terminal source-admission result on
	// first capture as well as retry, with one stable status marker per task.
	if err = queueAgentTaskSourceProviderComment(ctx, tx, task, func() string {
		if task.State == "awaiting_approval" {
			return "plan"
		}
		return "ready"
	}(), "", ""); err != nil {
		return domain.AgentTask{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata) VALUES($1,'worker:agent-task-source-admitter','agent_task.source_captured',$2,jsonb_build_object('base_ref',$3::text,'base_sha',$4::text,'revision',$5::int))`, task.TenantID, task.ID.String(), snapshot.BaseRef, snapshot.BaseSHA, task.Revision); err != nil {
		return domain.AgentTask{}, fmt.Errorf("audit agent task source snapshot: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.AgentTask{}, fmt.Errorf("commit agent task source snapshot: %w", err)
	}
	return task, nil
}

// FailAgentTaskSourceSnapshot is fail-closed: a task without a trustworthy
// base commit cannot be planned or dispatched to a coding adapter.
func (s *PostgresStore) FailAgentTaskSourceSnapshot(ctx context.Context, taskID uuid.UUID, code, message string) error {
	code, message = strings.TrimSpace(code), strings.TrimSpace(message)
	if taskID == uuid.Nil || code == "" || len(code) > 96 || message == "" || len(message) > 2000 {
		return ErrInvalidAgentTask
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin agent task source failure: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	task, err := scanAgentTask(tx.QueryRow(ctx, `SELECT `+agentTaskColumns+` FROM agent_tasks WHERE id=$1 FOR UPDATE`, taskID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("load agent task source failure: %w", err)
	}
	if task.SourceState != "pending" || task.State != "received" {
		return nil
	}
	task, err = scanAgentTask(tx.QueryRow(ctx, `UPDATE agent_tasks SET source_state='failed',state='needs_attention',revision=revision+1,updated_at=now() WHERE id=$1 RETURNING `+agentTaskColumns, taskID))
	if err != nil {
		return fmt.Errorf("fail agent task source snapshot: %w", err)
	}
	if err = queueAgentTaskSourceProviderComment(ctx, tx, task, "failed", code, message); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata) VALUES($1,'worker:agent-task-source-admitter','agent_task.source_failed',$2,jsonb_build_object('code',$3::text,'revision',$4::int))`, task.TenantID, task.ID.String(), code, task.Revision); err != nil {
		return fmt.Errorf("audit agent task source failure: %w", err)
	}
	return tx.Commit(ctx)
}

func scanAgentTaskAttempt(row rowScanner) (domain.AgentTaskAttempt, error) {
	var item domain.AgentTaskAttempt
	var workerID, adapterJobID *string
	err := row.Scan(&item.ID, &item.TaskID, &item.PlanID, &item.TaskRevision, &item.PlanRevision, &item.Attempt, &item.State, &workerID, &adapterJobID, &item.LockedUntil, &item.DeadlineAt, &item.ErrorCode, &item.ErrorMessage, &item.ResultSummary, &item.BranchName, &item.HeadSHA, &item.PullRequestURL, &item.PullRequestNumber, &item.PatchSHA256, &item.ChangedFileCount, &item.DiffBytes, &item.VerificationProfileSHA256, &item.VerificationOutputSHA256, &item.VerificationOutputBytes, &item.CreatedAt, &item.StartedAt, &item.FinishedAt, &item.UpdatedAt)
	if workerID != nil {
		item.WorkerID = *workerID
	}
	if adapterJobID != nil {
		item.AdapterJobID = *adapterJobID
	}
	return item, err
}

// ClaimAgentTaskAttempt turns one immutable outbox admission into one durable
// lease. It does not start a CLI, clone a repository, or issue provider
// credentials. A caller must still supply a sandbox adapter after this method
// has verified the task/plan tuple under the same transaction.
func (s *PostgresStore) ClaimAgentTaskAttempt(ctx context.Context, workerID string, request domain.AgentTaskExecutionRequest, lease time.Duration) (*domain.AgentTaskAttempt, error) {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" || !request.Valid() || lease < agentTaskLeaseFloor {
		return nil, ErrInvalidAgentTask
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin agent task attempt claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	task, err := scanAgentTask(tx.QueryRow(ctx, `SELECT `+agentTaskColumns+` FROM agent_tasks WHERE id=$1 FOR UPDATE`, request.TaskID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoQueuedAgentTask
	}
	if err != nil {
		return nil, fmt.Errorf("load agent task for execution: %w", err)
	}
	// The first claimant advances the task revision to executing inside this
	// transaction. A broker redelivery may reclaim an expired pre-adapter
	// lease, but an attached job may already have run code and belongs to the
	// reaper rather than another automatic coding attempt.
	queued := task.State == "execution_queued" && task.Revision == request.TaskRevision
	reclaiming := task.State == "executing" && task.Revision == request.TaskRevision+1
	if !queued && !reclaiming {
		return nil, ErrNoQueuedAgentTask
	}
	classification, err := latestAgentTaskClassification(ctx, tx, task.ID)
	if err != nil {
		return nil, fmt.Errorf("load agent task execution classification: %w", err)
	}
	if classification.Decision != "requires_human" {
		return nil, ErrNoQueuedAgentTask
	}
	if task.SourceState != "ready" || !(domain.AgentTaskSourceSnapshot{BaseRef: task.SourceBaseRef, BaseSHA: task.SourceBaseSHA}).Valid() {
		return nil, ErrNoQueuedAgentTask
	}
	var planSHA string
	err = tx.QueryRow(ctx, `SELECT plan_sha256 FROM agent_task_plans WHERE id=$1 AND task_id=$2 AND revision=$3 AND state='approved'`, request.PlanID, request.TaskID, request.PlanRevision).Scan(&planSHA)
	if errors.Is(err, pgx.ErrNoRows) || planSHA != request.PlanSHA256 {
		return nil, ErrNoQueuedAgentTask
	}
	if err != nil {
		return nil, fmt.Errorf("load approved agent task plan: %w", err)
	}

	// The worker lease is short and renewable; the task deadline is not. Cap
	// each lease by the immutable task envelope so even a healthy adapter
	// heartbeat cannot exceed the approval it received.
	executionInterval := fmt.Sprintf("%f seconds", float64(task.MaxExecutionSeconds))
	var attempt domain.AgentTaskAttempt
	attempt, err = scanAgentTaskAttempt(tx.QueryRow(ctx, `SELECT `+agentTaskAttemptColumns+` FROM agent_task_attempts WHERE task_id=$1 AND task_revision=$2 AND plan_id=$3 AND plan_revision=$4 FOR UPDATE`, request.TaskID, request.TaskRevision, request.PlanID, request.PlanRevision))
	if errors.Is(err, pgx.ErrNoRows) {
		if e := checkAgentWorkflowBudgetTx(ctx, tx, task); e != nil {
			if !errors.Is(e, ErrInvalidAgentTaskPlan) {
				return nil, e
			}
			if stopErr := stopAgentWorkflowBudgetTx(ctx, tx, task, uuid.Nil); stopErr != nil {
				return nil, stopErr
			}
			if commitErr := tx.Commit(ctx); commitErr != nil {
				return nil, commitErr
			}
			return nil, ErrNoQueuedAgentTask
		}
		interval := fmt.Sprintf("%f seconds", lease.Seconds())
		attempt, err = scanAgentTaskAttempt(tx.QueryRow(ctx, `INSERT INTO agent_task_attempts(task_id,plan_id,task_revision,plan_revision,attempt,state,worker_id,locked_until,deadline_at,started_at) VALUES($1,$2,$3,$4,1,'running',$5,LEAST(now()+$6::interval,now()+$7::interval),now()+$7::interval,now()) RETURNING `+agentTaskAttemptColumns, request.TaskID, request.PlanID, request.TaskRevision, request.PlanRevision, workerID, interval, executionInterval))
	} else if err == nil {
		if !canReclaimAgentAttempt(attempt, task.MaxAttempts, time.Now()) {
			return nil, ErrNoQueuedAgentTask
		}
		if e := checkAgentWorkflowBudgetTx(ctx, tx, task); e != nil {
			if !errors.Is(e, ErrInvalidAgentTaskPlan) {
				return nil, e
			}
			if stopErr := stopAgentWorkflowBudgetTx(ctx, tx, task, attempt.ID); stopErr != nil {
				return nil, stopErr
			}
			if commitErr := tx.Commit(ctx); commitErr != nil {
				return nil, commitErr
			}
			return nil, ErrNoQueuedAgentTask
		}
		interval := fmt.Sprintf("%f seconds", lease.Seconds())
		// Only an attempt that never attached a job can be reclaimed. A new
		// reservation then receives a fresh one-use start gate; no prior CLI
		// could have acquired provider-write authority for this row.
		attempt, err = scanAgentTaskAttempt(tx.QueryRow(ctx, `UPDATE agent_task_attempts SET attempt=attempt+1,worker_id=$2,locked_until=LEAST(now()+$3::interval,now()+$4::interval),deadline_at=now()+$4::interval,error_code='',error_message='',result_summary='',branch_name='',head_sha='',pull_request_url='',pull_request_number=0,patch_sha256='',changed_file_count=0,diff_bytes=0,adapter_job_id=NULL,adapter_started_at=NULL,started_at=now(),finished_at=NULL,updated_at=now() WHERE id=$1 RETURNING `+agentTaskAttemptColumns, attempt.ID, workerID, interval, executionInterval))
	}
	if err != nil {
		return nil, fmt.Errorf("lease agent task attempt: %w", err)
	}
	if queued {
		if _, err = tx.Exec(ctx, `UPDATE agent_tasks SET state='executing',revision=revision+1,updated_at=now() WHERE id=$1 AND state='execution_queued' AND revision=$2`, request.TaskID, request.TaskRevision); err != nil {
			return nil, fmt.Errorf("mark agent task executing: %w", err)
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata) VALUES($1,$2,'agent_task.attempt_started',$3,jsonb_build_object('attempt',$4::int,'task_revision',$5::int,'plan_revision',$6::int,'lease_until',$7::timestamptz,'deadline_at',$8::timestamptz,'max_attempts',$9::int))`, task.TenantID, "worker:"+workerID, attempt.ID.String(), attempt.Attempt, request.TaskRevision, request.PlanRevision, attempt.LockedUntil, attempt.DeadlineAt, task.MaxAttempts); err != nil {
		return nil, fmt.Errorf("audit agent task attempt claim: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit agent task attempt claim: %w", err)
	}
	return &attempt, nil
}

// A broker may recover a crashed worker only before a job is attached. An
// attached job may already be executing or publishing, so its expired lease
// belongs to the reaper and exact-job cancellation instead of automatic
// re-execution under the same approved plan.
func canReclaimAgentAttempt(attempt domain.AgentTaskAttempt, maxAttempts int, now time.Time) bool {
	return attempt.State == "running" && attempt.LockedUntil != nil && attempt.LockedUntil.Before(now) &&
		attempt.Attempt < maxAttempts && attempt.AdapterJobID == ""
}

// MarkAgentTaskAttemptNeedsAttention is the safe terminal boundary for an
// unavailable sandbox adapter, exhausted budget, or an unsupported execution
// profile. It must hold the live lease so a recovered worker cannot overwrite
// a newer operator decision.
func (s *PostgresStore) MarkAgentTaskAttemptNeedsAttention(ctx context.Context, attemptID uuid.UUID, workerID, code, message string) error {
	workerID, code, message = strings.TrimSpace(workerID), strings.TrimSpace(code), strings.TrimSpace(message)
	if attemptID == uuid.Nil || workerID == "" || code == "" || message == "" || len(code) > 96 || len(message) > 2000 {
		return ErrInvalidAgentTask
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin agent task attention: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var taskID, tenantID uuid.UUID
	var adapterJobID *string
	err = tx.QueryRow(ctx, `UPDATE agent_task_attempts SET state='needs_attention',locked_until=NULL,error_code=$3,error_message=$4,finished_at=now(),updated_at=now() WHERE id=$1 AND state='running' AND worker_id=$2 AND locked_until >= now() RETURNING task_id,(SELECT tenant_id FROM agent_tasks WHERE id=agent_task_attempts.task_id),adapter_job_id`, attemptID, workerID, code, message).Scan(&taskID, &tenantID, &adapterJobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAgentTaskClaimLost
	}
	if err != nil {
		return fmt.Errorf("mark agent attempt needs attention: %w", err)
	}
	if _, err = tx.Exec(ctx, `UPDATE agent_tasks SET state='needs_attention',revision=revision+1,updated_at=now() WHERE id=$1 AND state='executing'`, taskID); err != nil {
		return fmt.Errorf("mark agent task needs attention: %w", err)
	}
	attached := adapterJobID != nil && *adapterJobID != ""
	if err = queueAgentTaskAttemptStatus(ctx, tx, taskID, attemptID, code, message, attached); err != nil {
		return err
	}
	if attached {
		if err = queueAgentAdapterCancellation(ctx, tx, taskID, attemptID, *adapterJobID); err != nil {
			return fmt.Errorf("queue stopped agent adapter job: %w", err)
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata) VALUES($1,$2,'agent_task.attempt_needs_attention',$3,jsonb_build_object('error_code',$4::text))`, tenantID, "worker:"+workerID, attemptID.String(), code); err != nil {
		return fmt.Errorf("audit agent task attention: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit agent task attention: %w", err)
	}
	return nil
}

// RenewAgentTaskAttemptLease is intentionally separate from task mutation so
// an adapter can heartbeat while it works in a sandbox. A lease loss means the
// adapter must stop before it can publish or report a patch; another worker
// may already own a recovered attempt for the same immutable plan.
func (s *PostgresStore) RenewAgentTaskAttemptLease(ctx context.Context, attemptID uuid.UUID, workerID string, lease time.Duration) error {
	workerID = strings.TrimSpace(workerID)
	if attemptID == uuid.Nil || workerID == "" || lease < agentTaskLeaseFloor {
		return ErrInvalidAgentTask
	}
	interval := fmt.Sprintf("%f seconds", lease.Seconds())
	command, err := s.pool.Exec(ctx, `UPDATE agent_task_attempts SET locked_until=LEAST(now()+$3::interval,COALESCE(deadline_at,now()+$3::interval)),updated_at=now() WHERE id=$1 AND state='running' AND worker_id=$2 AND locked_until >= now() AND (deadline_at IS NULL OR deadline_at >= now())`, attemptID, workerID, interval)
	if err != nil {
		return fmt.Errorf("renew agent task attempt lease: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrAgentTaskClaimLost
	}
	return nil
}

// LoadAgentTaskAttemptTarget returns exactly the task and approved plan that
// the current lease holder may hand to an adapter. It is deliberately worker
// authenticated rather than exposed through the management API.
func (s *PostgresStore) LoadAgentTaskAttemptTarget(ctx context.Context, attemptID uuid.UUID, workerID string) (domain.AgentTaskAttemptTarget, error) {
	workerID = strings.TrimSpace(workerID)
	if attemptID == uuid.Nil || workerID == "" {
		return domain.AgentTaskAttemptTarget{}, ErrInvalidAgentTask
	}
	attempt, err := scanAgentTaskAttempt(s.pool.QueryRow(ctx, `SELECT `+agentTaskAttemptColumns+` FROM agent_task_attempts WHERE id=$1 AND state='running' AND worker_id=$2 AND locked_until >= now()`, attemptID, workerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AgentTaskAttemptTarget{}, ErrAgentTaskClaimLost
	}
	if err != nil {
		return domain.AgentTaskAttemptTarget{}, fmt.Errorf("load agent task attempt target: %w", err)
	}
	task, err := scanAgentTask(s.pool.QueryRow(ctx, `SELECT `+agentTaskColumns+` FROM agent_tasks WHERE id=$1`, attempt.TaskID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AgentTaskAttemptTarget{}, ErrNotFound
	}
	if err != nil {
		return domain.AgentTaskAttemptTarget{}, fmt.Errorf("load agent task target: %w", err)
	}
	plan, err := scanAgentTaskPlan(s.pool.QueryRow(ctx, `SELECT `+agentTaskPlanColumns+` FROM agent_task_plans WHERE id=$1 AND task_id=$2 AND state='approved'`, attempt.PlanID, attempt.TaskID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AgentTaskAttemptTarget{}, ErrNotFound
	}
	if err != nil {
		return domain.AgentTaskAttemptTarget{}, fmt.Errorf("load approved agent plan target: %w", err)
	}
	target := domain.AgentTaskAttemptTarget{Attempt: attempt, Task: task, Plan: plan}
	if task.OriginKind == "pull_request" {
		var binding domain.AgentTaskFeedbackBinding
		err = s.pool.QueryRow(ctx, `SELECT comment_external_id,actor_external_id,instruction_sha256,source_review_run_id,system_instruction,internal_repair_kind,internal_repair_key FROM agent_task_feedback_cycles WHERE child_task_id=$1 AND state='received'`, task.ID).Scan(&binding.CommentExternalID, &binding.ActorExternalID, &binding.InstructionSHA256, &binding.SourceReviewRunID, &binding.SystemInstruction, &binding.InternalRepairKind, &binding.InternalRepairKey)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !binding.Valid()) {
			return domain.AgentTaskAttemptTarget{}, ErrInvalidAgentTask
		}
		if err != nil {
			return domain.AgentTaskAttemptTarget{}, fmt.Errorf("load approved feedback binding: %w", err)
		}
		binding.TargetBranch, err = s.agentFeedbackTargetBranch(ctx, task.ID)
		if err != nil || !binding.ExecutionValid() {
			return domain.AgentTaskAttemptTarget{}, ErrInvalidAgentTask
		}
		target.Feedback = &binding
	}
	return target, nil
}

// AttachAgentTaskAdapterJob binds a running lease to the external adapter's
// opaque job identifier. An adapter callback must present this identifier as
// well as its authenticated delivery, so a stale job cannot finish a recovered
// lease after the next worker starts another attempt.
func (s *PostgresStore) AttachAgentTaskAdapterJob(ctx context.Context, attemptID uuid.UUID, workerID, adapterJobID string) error {
	workerID, adapterJobID = strings.TrimSpace(workerID), strings.TrimSpace(adapterJobID)
	if attemptID == uuid.Nil || workerID == "" || !validAgentAdapterJobID(adapterJobID) {
		return ErrInvalidAgentTask
	}
	command, err := s.pool.Exec(ctx, `UPDATE agent_task_attempts SET adapter_job_id=$3,updated_at=now() WHERE id=$1 AND state='running' AND worker_id=$2 AND locked_until >= now() AND adapter_job_id IS NULL`, attemptID, workerID, adapterJobID)
	if isUniqueViolation(err) {
		return ErrConflict
	}
	if err != nil {
		return fmt.Errorf("attach agent adapter job: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrAgentTaskClaimLost
	}
	return nil
}

// PendingAgentTaskAdapterStart recovers a lost response to the first Start
// request without submitting another job or claiming another attempt. Only the
// original worker's live lease, exact approved plan and attached job may be
// retried. Once the one-use start gate has been claimed, callbacks or expiry
// own recovery instead; a broker redelivery must not launch another sandbox.
func (s *PostgresStore) PendingAgentTaskAdapterStart(ctx context.Context, workerID string, request domain.AgentTaskExecutionRequest) (*domain.AgentTaskAttempt, error) {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" || !request.Valid() {
		return nil, ErrInvalidAgentTask
	}
	attempt, err := scanAgentTaskAttempt(s.pool.QueryRow(ctx, `SELECT `+agentTaskAttemptColumns+` FROM agent_task_attempts a
		WHERE a.task_id=$1 AND a.task_revision=$2 AND a.plan_id=$3 AND a.plan_revision=$4
		  AND a.state='running' AND a.worker_id=$5 AND a.adapter_job_id IS NOT NULL
		  AND a.locked_until >= now() AND (a.deadline_at IS NULL OR a.deadline_at >= now())
		  AND a.adapter_started_at IS NULL
		  AND EXISTS (SELECT 1 FROM agent_tasks t WHERE t.id=a.task_id AND t.state='executing' AND t.revision-1=$2)
		  AND EXISTS (SELECT 1 FROM agent_task_plans p WHERE p.id=a.plan_id AND p.task_id=a.task_id
		    AND p.state='approved' AND p.revision=$4 AND p.plan_sha256=$6)`,
		request.TaskID, request.TaskRevision, request.PlanID, request.PlanRevision, workerID, request.PlanSHA256))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoQueuedAgentTask
	}
	if err != nil {
		return nil, fmt.Errorf("load pending agent adapter start: %w", err)
	}
	return &attempt, nil
}

// ClaimAgentTaskAdapterStart is the durable, one-use gate between a reserved
// adapter job and running a coding CLI. It cannot be replayed after an adapter
// crash, even when a new replica receives the same signed start request.
func (s *PostgresStore) ClaimAgentTaskAdapterStart(ctx context.Context, attemptID uuid.UUID, adapterJobID string) error {
	adapterJobID = strings.TrimSpace(adapterJobID)
	if attemptID == uuid.Nil || !validAgentAdapterJobID(adapterJobID) {
		return ErrInvalidAgentTask
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin agent adapter start claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var taskID uuid.UUID
	err = tx.QueryRow(ctx, `UPDATE agent_task_attempts SET adapter_started_at=now(),updated_at=now()
		WHERE id=$1 AND adapter_job_id=$2 AND state='running' AND locked_until >= now()
		AND (deadline_at IS NULL OR deadline_at >= now()) AND adapter_started_at IS NULL
		RETURNING task_id`, attemptID, adapterJobID).Scan(&taskID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAgentTaskClaimLost
	}
	if err != nil {
		return fmt.Errorf("claim agent adapter start: %w", err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata)
		SELECT tenant_id,'agent-adapter','agent_task.adapter_started',$1::text,jsonb_build_object('adapter_job_id',$2::text)
		FROM agent_tasks WHERE id=$3`, attemptID.String(), adapterJobID, taskID); err != nil {
		return fmt.Errorf("audit agent adapter start: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit agent adapter start: %w", err)
	}
	return nil
}

// RecordAgentTaskAdapterEvent durably deduplicates an authenticated adapter
// callback and applies only a live lease's terminal state. It accepts a draft
// PR reference as evidence, never as an approval or merge request.
func (s *PostgresStore) RecordAgentTaskAdapterEvent(ctx context.Context, event domain.AgentTaskAdapterEvent, lease time.Duration) (domain.AgentTaskAttempt, bool, error) {
	event.AdapterJobID = strings.TrimSpace(event.AdapterJobID)
	event.DeliveryID = strings.TrimSpace(event.DeliveryID)
	event.Kind = strings.TrimSpace(event.Kind)
	event.Summary = strings.TrimSpace(event.Summary)
	event.BranchName = strings.TrimSpace(event.BranchName)
	event.HeadSHA = strings.TrimSpace(event.HeadSHA)
	event.PullRequestURL = strings.TrimSpace(event.PullRequestURL)
	event.ErrorCode = strings.TrimSpace(event.ErrorCode)
	if !event.Valid() || lease < agentTaskLeaseFloor || len(event.DeliveryID) > 200 || len(event.ErrorCode) > 96 {
		return domain.AgentTaskAttempt{}, false, ErrInvalidAgentTask
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return domain.AgentTaskAttempt{}, false, fmt.Errorf("encode agent adapter event: %w", err)
	}
	digest := sha256.Sum256(payload)
	payloadSHA := hex.EncodeToString(digest[:])
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.AgentTaskAttempt{}, false, fmt.Errorf("begin agent adapter event: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var receiptID uuid.UUID
	err = tx.QueryRow(ctx, `INSERT INTO agent_task_adapter_events(attempt_id,delivery_id,kind,payload_sha256) VALUES($1,$2,$3,$4) ON CONFLICT(attempt_id,delivery_id) DO NOTHING RETURNING id`, event.AttemptID, event.DeliveryID, event.Kind, payloadSHA).Scan(&receiptID)
	if errors.Is(err, pgx.ErrNoRows) {
		var existingSHA string
		if lookupErr := tx.QueryRow(ctx, `SELECT payload_sha256 FROM agent_task_adapter_events WHERE attempt_id=$1 AND delivery_id=$2`, event.AttemptID, event.DeliveryID).Scan(&existingSHA); lookupErr != nil {
			return domain.AgentTaskAttempt{}, false, fmt.Errorf("load duplicate agent adapter event: %w", lookupErr)
		}
		if existingSHA != payloadSHA {
			return domain.AgentTaskAttempt{}, false, ErrInvalidAgentTask
		}
		attempt, lookupErr := scanAgentTaskAttempt(tx.QueryRow(ctx, `SELECT `+agentTaskAttemptColumns+` FROM agent_task_attempts WHERE id=$1`, event.AttemptID))
		if lookupErr != nil {
			return domain.AgentTaskAttempt{}, false, lookupErr
		}
		if (event.Kind == "heartbeat" || event.Kind == "publication_checkpoint") &&
			(attempt.State != "running" || attempt.LockedUntil == nil || attempt.LockedUntil.Before(time.Now()) ||
				(attempt.DeadlineAt != nil && attempt.DeadlineAt.Before(time.Now())) || attempt.AdapterJobID != event.AdapterJobID) {
			return domain.AgentTaskAttempt{}, false, ErrAgentTaskClaimLost
		}
		if err = tx.Commit(ctx); err != nil {
			return domain.AgentTaskAttempt{}, false, err
		}
		return attempt, true, nil
	}
	if err != nil {
		return domain.AgentTaskAttempt{}, false, fmt.Errorf("record agent adapter receipt: %w", err)
	}
	attempt, err := scanAgentTaskAttempt(tx.QueryRow(ctx, `SELECT `+agentTaskAttemptColumns+` FROM agent_task_attempts WHERE id=$1 AND adapter_started_at IS NOT NULL FOR UPDATE`, event.AttemptID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AgentTaskAttempt{}, false, ErrAgentTaskClaimLost
	}
	if err != nil {
		return domain.AgentTaskAttempt{}, false, fmt.Errorf("load agent adapter attempt: %w", err)
	}
	if attempt.State != "running" || attempt.LockedUntil == nil || attempt.LockedUntil.Before(time.Now()) || (attempt.DeadlineAt != nil && attempt.DeadlineAt.Before(time.Now())) || attempt.AdapterJobID != event.AdapterJobID {
		return domain.AgentTaskAttempt{}, false, ErrAgentTaskClaimLost
	}
	if event.Kind == "heartbeat" {
		interval := fmt.Sprintf("%f seconds", lease.Seconds())
		attempt, err = scanAgentTaskAttempt(tx.QueryRow(ctx, `UPDATE agent_task_attempts SET locked_until=LEAST(now()+$2::interval,COALESCE(deadline_at,now()+$2::interval)),updated_at=now() WHERE id=$1 AND (deadline_at IS NULL OR deadline_at >= now()) RETURNING `+agentTaskAttemptColumns, event.AttemptID, interval))
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.AgentTaskAttempt{}, false, ErrAgentTaskClaimLost
		}
		if err != nil {
			return domain.AgentTaskAttempt{}, false, fmt.Errorf("renew adapter agent attempt lease: %w", err)
		}
		if err = tx.Commit(ctx); err != nil {
			return domain.AgentTaskAttempt{}, false, fmt.Errorf("commit agent adapter heartbeat: %w", err)
		}
		return attempt, false, nil
	}
	if event.Kind == "publication_checkpoint" || event.Kind == "completed" {
		var frozenWorkflow domain.AgentWorkflowPolicy
		var sectionsRaw []byte
		if err = tx.QueryRow(ctx, `SELECT t.workflow,p.sections FROM agent_tasks t JOIN agent_task_plans p ON p.id=$2 WHERE t.id=$1`, attempt.TaskID, attempt.PlanID).Scan(&frozenWorkflow, &sectionsRaw); err != nil {
			return domain.AgentTaskAttempt{}, false, err
		}
		var sections domain.AgentTaskPlanSections
		if err = json.Unmarshal(sectionsRaw, &sections); err != nil {
			return domain.AgentTaskAttempt{}, false, err
		}
		if frozenWorkflow.RequireCriterionEvidence && !domain.CriteriaVerified(sections.AcceptanceCriteria, event.VerificationCriteria) {
			return domain.AgentTaskAttempt{}, false, ErrInvalidAgentTask
		}
	}
	if event.Kind == "publication_checkpoint" {
		var branch string
		var tenantID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT execution_branch,tenant_id FROM agent_tasks WHERE id=$1`, attempt.TaskID).Scan(&branch, &tenantID); err != nil {
			return domain.AgentTaskAttempt{}, false, fmt.Errorf("load agent task for publication checkpoint: %w", err)
		}
		if event.BranchName != branch {
			return domain.AgentTaskAttempt{}, false, ErrInvalidAgentTask
		}
		var checkpointAttemptID uuid.UUID
		err = tx.QueryRow(ctx, `INSERT INTO agent_task_publication_checkpoints(attempt_id,attempt_number,adapter_job_id,branch_name,head_sha,patch_sha256,changed_file_count,diff_bytes,verification_profile_sha256,verification_output_sha256,verification_output_bytes,verification_criteria)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT(attempt_id,attempt_number) DO NOTHING RETURNING attempt_id`,
			event.AttemptID, attempt.Attempt, event.AdapterJobID, event.BranchName, event.HeadSHA, event.PatchSHA256, event.ChangedFileCount, event.DiffBytes, event.VerificationProfileSHA256, event.VerificationOutputSHA256, event.VerificationOutputBytes, criterionResultsJSON(event.VerificationCriteria)).Scan(&checkpointAttemptID)
		if errors.Is(err, pgx.ErrNoRows) {
			var existing domain.AgentTaskPublicationCheckpoint
			err = tx.QueryRow(ctx, `SELECT adapter_job_id,branch_name,head_sha,patch_sha256,changed_file_count,diff_bytes,verification_profile_sha256,verification_output_sha256,verification_output_bytes,verification_criteria
				FROM agent_task_publication_checkpoints WHERE attempt_id=$1 AND attempt_number=$2`, event.AttemptID, attempt.Attempt).
				Scan(&existing.AdapterJobID, &existing.BranchName, &existing.HeadSHA, &existing.PatchSHA256, &existing.ChangedFileCount, &existing.DiffBytes, &existing.VerificationProfileSHA256, &existing.VerificationOutputSHA256, &existing.VerificationOutputBytes, &existing.VerificationCriteria)
			if err != nil {
				return domain.AgentTaskAttempt{}, false, fmt.Errorf("load existing agent publication checkpoint: %w", err)
			}
			if existing.AdapterJobID != event.AdapterJobID || existing.BranchName != event.BranchName || existing.HeadSHA != event.HeadSHA || existing.PatchSHA256 != event.PatchSHA256 || existing.ChangedFileCount != event.ChangedFileCount || existing.DiffBytes != event.DiffBytes || existing.VerificationProfileSHA256 != event.VerificationProfileSHA256 || existing.VerificationOutputSHA256 != event.VerificationOutputSHA256 || existing.VerificationOutputBytes != event.VerificationOutputBytes || !equalCriterionResults(existing.VerificationCriteria, event.VerificationCriteria) {
				return domain.AgentTaskAttempt{}, false, ErrInvalidAgentTask
			}
		} else if err != nil {
			return domain.AgentTaskAttempt{}, false, fmt.Errorf("retain agent publication checkpoint: %w", err)
		} else {
			if _, err := tx.Exec(ctx, `INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata) VALUES($1,'agent-adapter','agent_task.publication_checkpoint',$2,jsonb_build_object('attempt_number',$3::int,'adapter_job_id',$4::text,'head_sha',$5::text,'patch_sha256',$6::text))`, tenantID, attempt.ID.String(), attempt.Attempt, event.AdapterJobID, event.HeadSHA, event.PatchSHA256); err != nil {
				return domain.AgentTaskAttempt{}, false, fmt.Errorf("audit agent publication checkpoint: %w", err)
			}
		}
		interval := fmt.Sprintf("%f seconds", lease.Seconds())
		attempt, err = scanAgentTaskAttempt(tx.QueryRow(ctx, `UPDATE agent_task_attempts SET locked_until=LEAST(now()+$2::interval,COALESCE(deadline_at,now()+$2::interval)),updated_at=now() WHERE id=$1 AND (deadline_at IS NULL OR deadline_at >= now()) RETURNING `+agentTaskAttemptColumns, event.AttemptID, interval))
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.AgentTaskAttempt{}, false, ErrAgentTaskClaimLost
		}
		if err != nil {
			return domain.AgentTaskAttempt{}, false, fmt.Errorf("renew agent attempt after publication checkpoint: %w", err)
		}
		if err = tx.Commit(ctx); err != nil {
			return domain.AgentTaskAttempt{}, false, fmt.Errorf("commit agent publication checkpoint: %w", err)
		}
		return attempt, false, nil
	}
	task, err := scanAgentTask(tx.QueryRow(ctx, `SELECT `+agentTaskColumns+` FROM agent_tasks WHERE id=$1 FOR UPDATE`, attempt.TaskID))
	if err != nil {
		return domain.AgentTaskAttempt{}, false, fmt.Errorf("load agent task for adapter terminal event: %w", err)
	}
	state, taskState := "failed", "failed"
	resultURL := event.PullRequestURL
	if event.Kind == "needs_attention" {
		state, taskState = "needs_attention", "needs_attention"
	}
	if event.Kind == "completed" {
		canonicalURL, canonicalErr := canonicalAgentResultURL(task, event.PullRequestURL, event.PullRequestNumber, s.agentDraftURLPolicy)
		if event.BranchName != task.ExecutionBranch || !validAgentHeadSHA(event.HeadSHA) || canonicalErr != nil {
			return domain.AgentTaskAttempt{}, false, ErrInvalidAgentTask
		}
		var checkpoint domain.AgentTaskPublicationCheckpoint
		checkpointErr := tx.QueryRow(ctx, `SELECT branch_name,head_sha,patch_sha256,changed_file_count,diff_bytes,verification_profile_sha256,verification_output_sha256,verification_output_bytes,verification_criteria
			FROM agent_task_publication_checkpoints WHERE attempt_id=$1 AND attempt_number=$2`, attempt.ID, attempt.Attempt).
			Scan(&checkpoint.BranchName, &checkpoint.HeadSHA, &checkpoint.PatchSHA256, &checkpoint.ChangedFileCount, &checkpoint.DiffBytes, &checkpoint.VerificationProfileSHA256, &checkpoint.VerificationOutputSHA256, &checkpoint.VerificationOutputBytes, &checkpoint.VerificationCriteria)
		if checkpointErr != nil && !errors.Is(checkpointErr, pgx.ErrNoRows) {
			return domain.AgentTaskAttempt{}, false, fmt.Errorf("load validated publication before completion: %w", checkpointErr)
		}
		if errors.Is(checkpointErr, pgx.ErrNoRows) && event.VerificationProfileSHA256 != "" {
			return domain.AgentTaskAttempt{}, false, ErrInvalidAgentTask
		}
		if checkpointErr == nil && (event.BranchName != checkpoint.BranchName || event.HeadSHA != checkpoint.HeadSHA || event.PatchSHA256 != checkpoint.PatchSHA256 || event.ChangedFileCount != checkpoint.ChangedFileCount || event.DiffBytes != checkpoint.DiffBytes || event.VerificationProfileSHA256 != checkpoint.VerificationProfileSHA256 || event.VerificationOutputSHA256 != checkpoint.VerificationOutputSHA256 || event.VerificationOutputBytes != checkpoint.VerificationOutputBytes || !equalCriterionResults(event.VerificationCriteria, checkpoint.VerificationCriteria)) {
			return domain.AgentTaskAttempt{}, false, ErrInvalidAgentTask
		}
		resultURL = canonicalURL
		state, taskState = "succeeded", "completed"
	}
	if event.Kind != "completed" && event.ErrorCode == "" {
		event.ErrorCode = "agent_adapter_failed"
	}
	attempt, err = scanAgentTaskAttempt(tx.QueryRow(ctx, `UPDATE agent_task_attempts SET state=$2,locked_until=NULL,error_code=$3,error_message=$4,result_summary=$4,branch_name=$5,head_sha=$6,pull_request_url=$7,pull_request_number=$8,patch_sha256=$9,changed_file_count=$10,diff_bytes=$11,verification_profile_sha256=$12,verification_output_sha256=$13,verification_output_bytes=$14,finished_at=now(),updated_at=now() WHERE id=$1 RETURNING `+agentTaskAttemptColumns, event.AttemptID, state, event.ErrorCode, event.Summary, event.BranchName, event.HeadSHA, resultURL, event.PullRequestNumber, event.PatchSHA256, event.ChangedFileCount, event.DiffBytes, event.VerificationProfileSHA256, event.VerificationOutputSHA256, event.VerificationOutputBytes))
	if err != nil {
		return domain.AgentTaskAttempt{}, false, fmt.Errorf("complete agent adapter attempt: %w", err)
	}
	if _, err = tx.Exec(ctx, `UPDATE agent_tasks SET state=$2,revision=revision+1,updated_at=now() WHERE id=$1 AND state='executing'`, task.ID, taskState); err != nil {
		return domain.AgentTaskAttempt{}, false, fmt.Errorf("complete agent task from adapter: %w", err)
	}
	if event.Kind == "completed" {
		if err = recordAgentDeliveryTx(ctx, tx, task, attempt); err != nil {
			return domain.AgentTaskAttempt{}, false, err
		}
	}
	if err = queueAgentTaskAdapterTerminalStatus(ctx, tx, task, attempt, event); err != nil {
		return domain.AgentTaskAttempt{}, false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata) VALUES($1,$2,$3,$4,jsonb_build_object('adapter_job_id',$5::text,'kind',$6::text,'delivery_id',$7::text))`, task.TenantID, "agent-adapter", "agent_task.adapter_"+event.Kind, attempt.ID.String(), event.AdapterJobID, event.Kind, event.DeliveryID); err != nil {
		return domain.AgentTaskAttempt{}, false, fmt.Errorf("audit agent adapter event: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.AgentTaskAttempt{}, false, fmt.Errorf("commit agent adapter terminal event: %w", err)
	}
	return attempt, false, nil
}

func validAgentAdapterJobID(value string) bool {
	if len(value) < 1 || len(value) > 256 {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}

func validAgentHeadSHA(value string) bool {
	if len(value) < 7 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if !(character >= '0' && character <= '9') && !(character >= 'a' && character <= 'f') && !(character >= 'A' && character <= 'F') {
			return false
		}
	}
	return true
}

func canonicalAgentResultURL(task domain.AgentTask, value string, number int, policy domain.AgentDraftURLPolicy) (string, error) {
	return policy.Canonical(task.Provider, task.APIBaseURL, task.Repository, number, value)
}

func canManageAgentTasks(role string) bool {
	return role == "owner" || role == "admin" || role == "reviewer"
}

// ProcessAgentTaskFeedback creates one new, separately approved child task
// from an explicit trusted PR/MR comment. It never resumes a finished sandbox
// or uses the comment text as a command. The source worker subsequently
// verifies that the same Draft PR branch is still at the completed head SHA.
func (s *PostgresStore) ProcessAgentTaskFeedback(ctx context.Context, event domain.AgentTaskFeedbackEvent) (domain.AgentTaskFeedbackOutcome, error) {
	event.Instruction = strings.TrimSpace(event.Instruction)
	if !event.Valid() {
		return domain.AgentTaskFeedbackOutcome{}, ErrInvalidAgentTask
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.AgentTaskFeedbackOutcome{}, fmt.Errorf("begin agent task feedback: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	installation, err := resolveInboundInstallation(ctx, tx, event.Provider, event.APIBaseURL, event.InstallationExternalID, event.Repository)
	if err != nil {
		return domain.AgentTaskFeedbackOutcome{}, err
	}
	var role, actorSubject string
	err = tx.QueryRow(ctx, `SELECT m.role,m.subject FROM provider_actor_mappings p JOIN memberships m ON m.tenant_id=p.tenant_id AND m.subject=p.subject AND m.active=TRUE WHERE p.tenant_id=$1 AND p.provider=$2 AND p.external_id=$3`, installation.TenantID, event.Provider, event.ActorExternalID).Scan(&role, &actorSubject)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AgentTaskFeedbackOutcome{}, ErrUnknownInstallation
	}
	if err != nil {
		return domain.AgentTaskFeedbackOutcome{}, fmt.Errorf("authorize agent feedback actor: %w", err)
	}
	if !canManageAgentTasks(role) {
		return domain.AgentTaskFeedbackOutcome{Reason: "your workspace role is not allowed to request Agent feedback"}, nil
	}
	var mode string
	var policyRevision, maxAttempts, maxExecutionSeconds, maxFeedbackCycles int
	var profile, decisionBackend string
	err = tx.QueryRow(ctx, `SELECT mode,revision,max_attempts,max_execution_seconds,max_feedback_cycles,executor_profile,decision_backend FROM agent_task_policies WHERE tenant_id=$1 AND provider=$2 AND api_base_url=$3 AND repository=$4 FOR UPDATE`, installation.TenantID, installation.Provider, installation.APIBaseURL, event.Repository).Scan(&mode, &policyRevision, &maxAttempts, &maxExecutionSeconds, &maxFeedbackCycles, &profile, &decisionBackend)
	if errors.Is(err, pgx.ErrNoRows) || mode != "manual" || maxFeedbackCycles == 0 {
		return domain.AgentTaskFeedbackOutcome{Reason: "Draft PR feedback is disabled for this repository"}, nil
	}
	if err != nil {
		return domain.AgentTaskFeedbackOutcome{}, fmt.Errorf("load agent feedback policy: %w", err)
	}
	var parentTaskID, parentAttemptID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT t.id,a.id
		FROM agent_task_attempts a JOIN agent_tasks t ON t.id=a.task_id
		WHERE t.tenant_id=$1 AND t.installation_id=$2 AND t.repository=$3 AND t.state='completed'
		  AND a.state='succeeded' AND a.pull_request_number=$4 AND a.branch_name=t.execution_branch
		ORDER BY a.finished_at DESC,a.id DESC LIMIT 1 FOR UPDATE OF t,a`, installation.TenantID, installation.ID, event.Repository, event.PullRequestNumber).Scan(&parentTaskID, &parentAttemptID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AgentTaskFeedbackOutcome{Reason: "no completed Open Review Draft PR matches this pull request"}, nil
	}
	if err != nil {
		return domain.AgentTaskFeedbackOutcome{}, fmt.Errorf("load agent feedback parent: %w", err)
	}
	parentTask, err := scanAgentTask(tx.QueryRow(ctx, `SELECT `+agentTaskColumns+` FROM agent_tasks WHERE id=$1`, parentTaskID))
	if err != nil {
		return domain.AgentTaskFeedbackOutcome{}, fmt.Errorf("load agent feedback parent task: %w", err)
	}
	// A policy edit may tighten a new feedback cycle, but it must never expand
	// the execution or cycle budget frozen when the parent was admitted. Each
	// child carries these ceilings forward for all subsequent Draft revisions.
	maxAttempts = min(maxAttempts, parentTask.MaxAttempts)
	maxExecutionSeconds = min(maxExecutionSeconds, parentTask.MaxExecutionSeconds)
	maxFeedbackCycles = min(maxFeedbackCycles, parentTask.MaxFeedbackCycles)
	parentAttempt, err := scanAgentTaskAttempt(tx.QueryRow(ctx, `SELECT `+agentTaskAttemptColumns+` FROM agent_task_attempts WHERE id=$1`, parentAttemptID))
	if err != nil {
		return domain.AgentTaskFeedbackOutcome{}, fmt.Errorf("load agent feedback parent attempt: %w", err)
	}
	var usedCycles int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM agent_task_feedback_cycles WHERE parent_task_id=$1 AND state IN ('received','superseded')`, parentTask.ID).Scan(&usedCycles); err != nil {
		return domain.AgentTaskFeedbackOutcome{}, fmt.Errorf("count agent feedback cycles: %w", err)
	}
	if usedCycles >= maxFeedbackCycles || parentTask.FeedbackCycle >= parentTask.MaxFeedbackCycles {
		return domain.AgentTaskFeedbackOutcome{Reason: "the frozen Draft PR feedback-cycle budget is exhausted"}, nil
	}
	digest := sha256.Sum256([]byte(event.Instruction))
	feedbackID := uuid.New()
	var existingTaskID *uuid.UUID
	err = tx.QueryRow(ctx, `INSERT INTO agent_task_feedback_cycles(id,tenant_id,parent_task_id,parent_attempt_id,provider,provider_delivery_id,repository,pull_request_number,comment_external_id,actor_external_id,instruction_sha256,state)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'received')
		ON CONFLICT(provider,provider_delivery_id) DO NOTHING RETURNING child_task_id`, feedbackID, installation.TenantID, parentTask.ID, parentAttempt.ID, event.Provider, event.DeliveryID, event.Repository, event.PullRequestNumber, event.CommentExternalID, event.ActorExternalID, hex.EncodeToString(digest[:])).Scan(&existingTaskID)
	if errors.Is(err, pgx.ErrNoRows) {
		if err = tx.QueryRow(ctx, `SELECT child_task_id FROM agent_task_feedback_cycles WHERE provider=$1 AND provider_delivery_id=$2`, event.Provider, event.DeliveryID).Scan(&existingTaskID); err != nil {
			return domain.AgentTaskFeedbackOutcome{}, fmt.Errorf("load duplicate agent feedback: %w", err)
		}
		if err = tx.Commit(ctx); err != nil {
			return domain.AgentTaskFeedbackOutcome{}, err
		}
		return domain.AgentTaskFeedbackOutcome{Accepted: existingTaskID != nil, Duplicate: true, TaskID: existingTaskID, Reason: "Draft PR feedback already recorded"}, nil
	}
	if err != nil {
		return domain.AgentTaskFeedbackOutcome{}, fmt.Errorf("record agent feedback receipt: %w", err)
	}
	childID := uuid.New()
	child, err := scanAgentTask(tx.QueryRow(ctx, `INSERT INTO agent_tasks(id,tenant_id,installation_id,provider,api_base_url,repository,origin_kind,origin_number,origin_revision,intent,policy_revision,max_attempts,max_execution_seconds,max_feedback_cycles,executor_profile,decision_backend,feedback_cycle,execution_branch,parent_task_id,parent_attempt_id,requested_by)
		VALUES($1,$2,$3,$4,$5,$6,'pull_request',$7,$8,'implement',$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
		RETURNING `+agentTaskColumns, childID, installation.TenantID, installation.ID, installation.Provider, installation.APIBaseURL, event.Repository, event.PullRequestNumber, parentAttempt.HeadSHA, policyRevision, maxAttempts, maxExecutionSeconds, maxFeedbackCycles, profile, decisionBackend, parentTask.FeedbackCycle+1, parentTask.ExecutionBranch, parentTask.ID, parentAttempt.ID, actorSubject))
	if err != nil {
		return domain.AgentTaskFeedbackOutcome{}, fmt.Errorf("create agent feedback task: %w", err)
	}
	if _, err = tx.Exec(ctx, `UPDATE agent_task_feedback_cycles SET child_task_id=$2 WHERE id=$1`, feedbackID, child.ID); err != nil {
		return domain.AgentTaskFeedbackOutcome{}, err
	}
	classification := agentdecision.Classify(child, "Revise existing Draft PR", agentdecision.FeedbackClassificationBody(event.Instruction), nil)
	if _, err = recordAgentTaskClassification(ctx, tx, child, classification); err != nil {
		return domain.AgentTaskFeedbackOutcome{}, err
	}
	if classification.Decision == "rejected" {
		child, err = scanAgentTask(tx.QueryRow(ctx, `UPDATE agent_tasks SET state='rejected',revision=revision+1,updated_at=now() WHERE id=$1 RETURNING `+agentTaskColumns, child.ID))
		if err != nil {
			return domain.AgentTaskFeedbackOutcome{}, err
		}
		if _, err = tx.Exec(ctx, `UPDATE agent_task_feedback_cycles SET state='rejected' WHERE id=$1`, feedbackID); err != nil {
			return domain.AgentTaskFeedbackOutcome{}, err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata) VALUES($1,$2,'agent_task.feedback_received',$3,jsonb_build_object('parent_task_id',$4::text,'parent_attempt_id',$5::text,'pull_request_number',$6::int,'feedback_cycle',$7::int,'instruction_sha256',$8::text))`, installation.TenantID, actorSubject, feedbackID.String(), parentTask.ID.String(), parentAttempt.ID.String(), event.PullRequestNumber, child.FeedbackCycle, hex.EncodeToString(digest[:])); err != nil {
		return domain.AgentTaskFeedbackOutcome{}, err
	}
	if err = queueAgentTaskFeedbackResponse(ctx, tx, installation, event, feedbackID, child, classification); err != nil {
		return domain.AgentTaskFeedbackOutcome{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.AgentTaskFeedbackOutcome{}, fmt.Errorf("commit agent feedback: %w", err)
	}
	return domain.AgentTaskFeedbackOutcome{Accepted: true, TaskID: &child.ID, Reason: "Draft PR feedback recorded"}, nil
}

// ProcessAgentTaskCommand accepts only a provider-verified Issue comment and
// records an idempotent receipt before replying. It does not plan, invoke a
// model, create a worktree, or grant any write capability.
func (s *PostgresStore) ProcessAgentTaskCommand(ctx context.Context, event domain.AgentTaskCommandEvent, command, normalized string) (domain.AgentTaskCommandOutcome, error) {
	if !event.Valid() || (command != "implement" && command != "cancel" && command != "status" && command != "approve" && command != "invalid") {
		return domain.AgentTaskCommandOutcome{}, ErrInvalidAgentTask
	}
	planSHA := ""
	if command == "approve" {
		planSHA = strings.TrimPrefix(normalized, "@openreview approve ")
		if normalized != "@openreview approve "+planSHA || len(planSHA) != 64 || !validAgentHeadSHA(planSHA) || planSHA != strings.ToLower(planSHA) {
			return domain.AgentTaskCommandOutcome{}, ErrInvalidAgentTask
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.AgentTaskCommandOutcome{}, fmt.Errorf("begin agent task command: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	installation, err := resolveInboundInstallation(ctx, tx, event.Provider, event.APIBaseURL, event.InstallationExternalID, event.Repository)
	if err != nil {
		return domain.AgentTaskCommandOutcome{}, err
	}
	var role, actorSubject string
	err = tx.QueryRow(ctx, `SELECT m.role,m.subject FROM provider_actor_mappings p JOIN memberships m ON m.tenant_id=p.tenant_id AND m.subject=p.subject AND m.active=TRUE WHERE p.tenant_id=$1 AND p.provider=$2 AND p.external_id=$3`, installation.TenantID, event.Provider, event.ActorExternalID).Scan(&role, &actorSubject)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AgentTaskCommandOutcome{}, ErrUnknownInstallation
	}
	if err != nil {
		return domain.AgentTaskCommandOutcome{}, fmt.Errorf("authorize agent task command actor: %w", err)
	}
	var interactionID uuid.UUID
	err = tx.QueryRow(ctx, `INSERT INTO agent_task_interactions(tenant_id,provider,provider_delivery_id,actor_external_id,repository,issue_number,issue_revision,comment_external_id,command,normalized_input,result) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'ignored') ON CONFLICT(provider,provider_delivery_id) DO NOTHING RETURNING id`, installation.TenantID, event.Provider, event.DeliveryID, event.ActorExternalID, event.Repository, event.IssueNumber, event.IssueRevision, event.CommentExternalID, command, normalized).Scan(&interactionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AgentTaskCommandOutcome{Duplicate: true}, nil
	}
	if err != nil {
		return domain.AgentTaskCommandOutcome{}, fmt.Errorf("record agent task interaction: %w", err)
	}
	reject := func(reason string) (domain.AgentTaskCommandOutcome, error) {
		if _, err := tx.Exec(ctx, `UPDATE agent_task_interactions SET result='rejected' WHERE id=$1`, interactionID); err != nil {
			return domain.AgentTaskCommandOutcome{}, err
		}
		if err := queueAgentTaskInteractionResponse(ctx, tx, installation, event, interactionID, "Agent task command not accepted: "+reason, domain.InteractionReactionConfused, nil); err != nil {
			return domain.AgentTaskCommandOutcome{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.AgentTaskCommandOutcome{}, err
		}
		return domain.AgentTaskCommandOutcome{Reason: reason}, nil
	}
	if command == "invalid" {
		return reject("invalid command; use `@openreview implement`, `@openreview status`, `@openreview approve <full-plan-sha256>`, or `@openreview cancel` for the exact current Issue revision")
	}
	if !canManageAgentTasks(role) {
		return reject("your workspace role is not allowed to request Agent tasks")
	}
	issueRevisions := agentIssueCommandRevisions(event)
	if command == "approve" {
		if role != "owner" && role != "admin" {
			return reject("only a mapped workspace owner or admin can approve an Agent plan")
		}
		task, err := findIssueAgentTaskTx(ctx, tx, installation.TenantID, installation.ID, event.Repository, event.IssueNumber, issueRevisions, true)
		if errors.Is(err, pgx.ErrNoRows) {
			return reject("there is no Agent task for this exact Issue revision")
		}
		if errors.Is(err, ErrConflict) {
			return reject("multiple Agent tasks match this Issue snapshot; resolve them in Open Review before approving a plan")
		}
		if err != nil {
			return domain.AgentTaskCommandOutcome{}, fmt.Errorf("load agent task approval command: %w", err)
		}
		if task.State != "awaiting_approval" {
			return reject("the task is not awaiting plan approval")
		}
		plan, err := scanAgentTaskPlan(tx.QueryRow(ctx, `SELECT `+agentTaskPlanColumns+` FROM agent_task_plans WHERE task_id=$1 ORDER BY revision DESC LIMIT 1`, task.ID))
		if errors.Is(err, pgx.ErrNoRows) {
			return reject("there is no plan awaiting approval")
		}
		if err != nil {
			return domain.AgentTaskCommandOutcome{}, fmt.Errorf("load agent task approval plan: %w", err)
		}
		if plan.State != "awaiting_approval" || plan.PlanSHA256 != planSHA || validateAgentTaskPlanIntegrity(plan) != nil {
			return reject("the plan digest is stale or does not match the current plan; inspect the task and copy its full SHA-256")
		}
		classification, err := latestAgentTaskClassification(ctx, tx, task.ID)
		if err != nil {
			return domain.AgentTaskCommandOutcome{}, fmt.Errorf("load agent task approval classification: %w", err)
		}
		if classification.Decision != "requires_human" {
			return reject("the latest admission classification does not permit human plan approval")
		}
		if (classification.RiskLevel == "high" || classification.RiskLevel == "critical") && plan.CreatedBy == actorSubject {
			return reject("high-risk plans require an owner/admin other than the plan author")
		}
		approved, err := approveAgentTaskPlanTx(ctx, tx, installation.TenantID, actorSubject, role, task, plan.ID, domain.AgentTaskPlanApprovalInput{Revision: plan.Revision})
		if err != nil {
			return domain.AgentTaskCommandOutcome{}, fmt.Errorf("approve agent task from provider command: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE agent_task_interactions SET result='accepted',task_id=$2 WHERE id=$1`, interactionID, task.ID); err != nil {
			return domain.AgentTaskCommandOutcome{}, err
		}
		body := fmt.Sprintf("### Agent plan approved ✅\n\nPlan revision **%d** (`%s`) for task `%s` was approved by a mapped workspace %s. Execution is **queued**, not yet complete; no Draft PR has been published by this acknowledgement.", approved.Revision, approved.PlanSHA256, task.ID, role)
		if taskURL := agentTaskConsoleURL(ctx, tx, installation.TenantID, task.ID); taskURL != "" {
			body += "\n\n[Open task status and evidence in Open Review](" + taskURL + ")"
		}
		if err := queueAgentTaskInteractionResponse(ctx, tx, installation, event, interactionID, body, domain.InteractionReactionEyes, nil); err != nil {
			return domain.AgentTaskCommandOutcome{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.AgentTaskCommandOutcome{}, fmt.Errorf("commit agent task provider approval: %w", err)
		}
		return domain.AgentTaskCommandOutcome{Accepted: true, TaskID: &task.ID, Reason: "agent plan approved and execution queued"}, nil
	}
	if command == "status" {
		var task domain.AgentTask
		task, err = findIssueAgentTaskTx(ctx, tx, installation.TenantID, installation.ID, event.Repository, event.IssueNumber, issueRevisions, false)
		if errors.Is(err, pgx.ErrNoRows) {
			return reject("there is no Agent task for this exact Issue revision")
		}
		if errors.Is(err, ErrConflict) {
			return reject("multiple Agent tasks match this Issue snapshot; resolve them in Open Review before requesting status")
		}
		if err != nil {
			return domain.AgentTaskCommandOutcome{}, fmt.Errorf("load agent task status command: %w", err)
		}
		var attempt domain.AgentTaskAttempt
		attempt, err = scanAgentTaskAttempt(tx.QueryRow(ctx, `SELECT `+agentTaskAttemptColumns+` FROM agent_task_attempts WHERE task_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1`, task.ID))
		if errors.Is(err, pgx.ErrNoRows) {
			attempt = domain.AgentTaskAttempt{}
		} else if err != nil {
			return domain.AgentTaskCommandOutcome{}, fmt.Errorf("load agent task status attempt: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE agent_task_interactions SET result='accepted',task_id=$2 WHERE id=$1`, interactionID, task.ID); err != nil {
			return domain.AgentTaskCommandOutcome{}, err
		}
		var pendingPlan *domain.AgentTaskPlan
		if task.State == "awaiting_approval" {
			plan, planErr := scanAgentTaskPlan(tx.QueryRow(ctx, `SELECT `+agentTaskPlanColumns+` FROM agent_task_plans WHERE task_id=$1 AND state='awaiting_approval' ORDER BY revision DESC LIMIT 1`, task.ID))
			if planErr != nil && !errors.Is(planErr, pgx.ErrNoRows) {
				return domain.AgentTaskCommandOutcome{}, fmt.Errorf("load pending agent plan for status: %w", planErr)
			}
			if planErr == nil {
				pendingPlan = &plan
			}
		}
		taskURL := agentTaskConsoleURL(ctx, tx, installation.TenantID, task.ID)
		if err := queueAgentTaskInteractionResponse(ctx, tx, installation, event, interactionID, agentTaskStatusComment(task, attempt, pendingPlan, taskURL), domain.InteractionReactionEyes, nil); err != nil {
			return domain.AgentTaskCommandOutcome{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.AgentTaskCommandOutcome{}, err
		}
		return domain.AgentTaskCommandOutcome{Accepted: true, TaskID: &task.ID, Reason: "agent task status"}, nil
	}
	if command == "cancel" {
		var task domain.AgentTask
		task, err = findIssueAgentTaskTx(ctx, tx, installation.TenantID, installation.ID, event.Repository, event.IssueNumber, issueRevisions, true)
		if errors.Is(err, pgx.ErrNoRows) {
			return reject("there is no Agent task for this exact Issue revision")
		}
		if errors.Is(err, ErrConflict) {
			return reject("multiple Agent tasks match this Issue snapshot; resolve them in Open Review before cancelling")
		}
		if err != nil {
			return domain.AgentTaskCommandOutcome{}, fmt.Errorf("load agent task cancellation command: %w", err)
		}
		if !agentTaskStateCancellable(task.State) {
			return reject("this Agent task is already terminal and cannot be cancelled")
		}
		task, err = cancelAgentTaskTx(ctx, tx, task, actorSubject, "Cancelled by a verified provider Issue command.")
		if err != nil {
			return domain.AgentTaskCommandOutcome{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE agent_task_interactions SET result='accepted',task_id=$2 WHERE id=$1`, interactionID, task.ID); err != nil {
			return domain.AgentTaskCommandOutcome{}, err
		}
		taskURL := agentTaskConsoleURL(ctx, tx, installation.TenantID, task.ID)
		body := agentTaskCancelledComment(task.ID, taskURL)
		if err := queueAgentTaskInteractionResponse(ctx, tx, installation, event, interactionID, body, domain.InteractionReactionEyes, nil); err != nil {
			return domain.AgentTaskCommandOutcome{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.AgentTaskCommandOutcome{}, err
		}
		return domain.AgentTaskCommandOutcome{Accepted: true, TaskID: &task.ID, Reason: "agent task cancelled"}, nil
	}
	var policyMode string
	var policyRevision, maxAttempts, maxExecutionSeconds, maxFeedbackCycles int
	var executorProfile, decisionBackend string
	err = tx.QueryRow(ctx, `SELECT mode,revision,max_attempts,max_execution_seconds,max_feedback_cycles,executor_profile,decision_backend FROM agent_task_policies WHERE tenant_id=$1 AND provider=$2 AND api_base_url=$3 AND repository=$4 FOR UPDATE`, installation.TenantID, installation.Provider, installation.APIBaseURL, event.Repository).Scan(&policyMode, &policyRevision, &maxAttempts, &maxExecutionSeconds, &maxFeedbackCycles, &executorProfile, &decisionBackend)
	if errors.Is(err, pgx.ErrNoRows) || policyMode != "manual" {
		return reject("Agent tasks are disabled for this repository; an administrator must explicitly enable manual mode")
	}
	if err != nil {
		return domain.AgentTaskCommandOutcome{}, fmt.Errorf("load agent task command policy: %w", err)
	}
	var task domain.AgentTask
	created := false
	taskID := uuid.New()
	task, err = findIssueAgentTaskTx(ctx, tx, installation.TenantID, installation.ID, event.Repository, event.IssueNumber, issueRevisions, true)
	if errors.Is(err, ErrConflict) {
		return reject("multiple Agent tasks match this Issue snapshot; resolve them in Open Review before requesting another")
	}
	if errors.Is(err, pgx.ErrNoRows) {
		task, err = scanAgentTask(tx.QueryRow(ctx, `INSERT INTO agent_tasks(id,tenant_id,installation_id,provider,api_base_url,repository,origin_kind,origin_number,origin_revision,intent,policy_revision,max_attempts,max_execution_seconds,max_feedback_cycles,executor_profile,decision_backend,execution_branch,requested_by) VALUES($1,$2,$3,$4,$5,$6,'issue',$7,$8,'implement',$9,$10,$11,$12,$13,$14,$15,$16) ON CONFLICT(tenant_id,installation_id,repository,origin_kind,origin_number,origin_revision,intent) DO NOTHING RETURNING `+agentTaskColumns, taskID, installation.TenantID, installation.ID, event.Provider, installation.APIBaseURL, event.Repository, event.IssueNumber, event.IssueRevision, policyRevision, maxAttempts, maxExecutionSeconds, maxFeedbackCycles, executorProfile, decisionBackend, "agent/"+taskID.String(), actorSubject))
		if errors.Is(err, pgx.ErrNoRows) {
			task, err = findIssueAgentTaskTx(ctx, tx, installation.TenantID, installation.ID, event.Repository, event.IssueNumber, issueRevisions, true)
		} else if err == nil {
			created = true
		}
	}
	if err != nil {
		return domain.AgentTaskCommandOutcome{}, fmt.Errorf("create or load agent task command task: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE agent_task_interactions SET result='accepted',task_id=$2 WHERE id=$1`, interactionID, task.ID); err != nil {
		return domain.AgentTaskCommandOutcome{}, err
	}
	if created {
		classification := agentdecision.Classify(task, event.IssueTitle, event.IssueBody, event.IssueLabels)
		if _, err := recordAgentTaskClassification(ctx, tx, task, classification); err != nil {
			return domain.AgentTaskCommandOutcome{}, err
		}
		if classification.Decision == "rejected" {
			task, err = scanAgentTask(tx.QueryRow(ctx, `UPDATE agent_tasks SET state='rejected',revision=revision+1,updated_at=now() WHERE id=$1 AND state='received' RETURNING `+agentTaskColumns, task.ID))
			if err != nil {
				return domain.AgentTaskCommandOutcome{}, fmt.Errorf("reject unsafe agent task: %w", err)
			}
		}
		// The initial response carries the source-release capability below.
		if _, err := tx.Exec(ctx, `INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata) VALUES($1,$2,'agent_task.created_from_issue_command',$3,jsonb_build_object('provider',$4::text,'repository',$5::text,'issue_number',$6::int,'issue_revision',$7::text,'revision',$8::int))`, installation.TenantID, actorSubject, task.ID.String(), task.Provider, task.Repository, task.OriginNumber, task.OriginRevision, task.Revision); err != nil {
			return domain.AgentTaskCommandOutcome{}, err
		}
	}
	classification := agentdecision.Classify(task, event.IssueTitle, event.IssueBody, event.IssueLabels)
	taskURL := agentTaskConsoleURL(ctx, tx, installation.TenantID, task.ID)
	var sourceRelease *domain.AgentTask
	if created && task.State == "received" {
		sourceRelease = &task
	}
	if err := queueAgentTaskInteractionResponse(ctx, tx, installation, event, interactionID, agentTaskCommandAcknowledgement(task, created, classification, taskURL), domain.InteractionReactionEyes, sourceRelease); err != nil {
		return domain.AgentTaskCommandOutcome{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.AgentTaskCommandOutcome{}, fmt.Errorf("commit agent task command: %w", err)
	}
	return domain.AgentTaskCommandOutcome{Accepted: true, TaskID: &task.ID, Reason: "agent task recorded"}, nil
}

func agentTaskCommandAcknowledgement(task domain.AgentTask, created bool, classification domain.AgentTaskClassification, taskURL string) string {
	link := ""
	if taskURL != "" {
		link = "\n\n[Open this Agent task in Open Review](" + taskURL + ")"
	}
	if !created {
		return fmt.Sprintf("### Agent task already recorded\n\nTask `%s` already tracks this exact Issue revision. It remains **%s**. No coding Agent, branch, or pull request has been created by this acknowledgement.\n\n%s%s", task.ID, task.State, agentTaskJEVVerdict(classification), link)
	}
	if task.State == "rejected" || classification.Decision == "rejected" {
		return fmt.Sprintf("### Agent task rejected\n\nTask `%s` failed the initial hard-policy check. **Source resolution and planning will not continue for this revision.** Update the Issue and submit a new command only after addressing the reason below. No coding Agent, branch, pull request, or merge action has started.\n\n%s%s", task.ID, agentTaskJEVVerdict(classification), link)
	}
	if classification.Decision == "needs_context" {
		return fmt.Sprintf("### Agent task needs context\n\nTask `%s` is recorded, but the initial check found insufficient context. Source verification may continue; **planning remains blocked** until the Issue is updated and a new revision is evaluated. No coding Agent, branch, pull request, or merge action has started.\n\n%s%s", task.ID, agentTaskJEVVerdict(classification), link)
	}
	return fmt.Sprintf("### Agent task received\n\nTask `%s` is **received**. Open Review will first capture the repository's immutable base commit; only then can an owner/admin approve a bounded plan. No coding Agent has started, and no branch or pull request has been created.\n\n%s\n\nUse the Open Review workspace to review the classification, captured source, plan, and exact plan revision.%s", task.ID, agentTaskJEVVerdict(classification), link)
}

func agentTaskJEVVerdict(classification domain.AgentTaskClassification) string {
	return "**Admission verdict:** `" + classification.Decision + "` · risk `" + classification.RiskLevel + "` · next `" + classification.NextAction + "`. " + strings.Join(classification.Reasons, " ")
}

func agentTaskStatusComment(task domain.AgentTask, attempt domain.AgentTaskAttempt, pendingPlan *domain.AgentTaskPlan, taskURL string) string {
	body := "### Agent task status\n\nTask `" + task.ID.String() + "` is **" + task.State + "** for this exact Issue revision."
	if task.SourceState == "ready" {
		body += "\n\n- Immutable source: `" + task.SourceBaseRef + "` @ `" + task.SourceBaseSHA + "`"
	} else if task.SourceState == "pending" {
		body += "\n\n- Immutable source: **resolving**"
	} else if task.SourceState == "failed" {
		body += "\n\n- Immutable source: **unavailable**; planning and execution remain blocked"
	}
	if pendingPlan != nil && pendingPlan.State == "awaiting_approval" && task.State == "awaiting_approval" {
		body += fmt.Sprintf("\n\n- Plan revision: **%d**, awaiting owner/admin approval\n- Exact plan SHA-256: `%s`\n- After reviewing the full plan and evidence in Open Review, a mapped owner/admin may comment `@openreview approve %s`.", pendingPlan.Revision, pendingPlan.PlanSHA256, pendingPlan.PlanSHA256)
	}
	if attempt.ID == uuid.Nil {
		body += "\n\nNo execution lease has been claimed. No coding Agent, branch, or pull request has been created."
	} else {
		body += "\n\n- Attempt: `" + fmt.Sprint(attempt.Attempt) + "` · **" + attempt.State + "**"
		if attempt.AdapterJobID != "" {
			body += "\n- Adapter job: `" + attempt.AdapterJobID + "`"
		}
		if attempt.PullRequestURL != "" {
			body += "\n- [Open draft pull request](" + attempt.PullRequestURL + ")"
		}
		if attempt.ErrorCode != "" {
			body += "\n- Last condition: `" + attempt.ErrorCode + "`"
		}
	}
	if taskURL != "" {
		body += "\n\n[Open task details in Open Review](" + taskURL + ")"
	}
	return body
}

func agentTaskConsoleURL(ctx context.Context, tx pgx.Tx, tenantID, taskID uuid.UUID) string {
	if taskID == uuid.Nil {
		return ""
	}
	return tenantConsoleLink(ctx, tx, tenantID, "/agent-work?task="+url.QueryEscape(taskID.String()))
}

func recordAgentTaskClassification(ctx context.Context, tx pgx.Tx, task domain.AgentTask, input domain.AgentTaskClassification) (domain.AgentTaskClassification, error) {
	reasons, err := json.Marshal(input.Reasons)
	if err != nil {
		return domain.AgentTaskClassification{}, fmt.Errorf("encode agent task classification reasons: %w", err)
	}
	evaluation, err := json.Marshal(input.Evaluation)
	if err != nil {
		return domain.AgentTaskClassification{}, fmt.Errorf("encode agent task JEV evaluation: %w", err)
	}
	var result domain.AgentTaskClassification
	var rawReasons []byte
	var rawEvaluation []byte
	err = tx.QueryRow(ctx, `INSERT INTO agent_task_classifications(task_id,task_revision,source_revision,decision,risk_level,confidence,reasons,evaluation,next_action,snapshot_sha256,classifier_version) VALUES($1,$2,$3,$4,$5,$6,$7::jsonb,$8::jsonb,$9,$10,$11) RETURNING `+agentTaskClassificationColumns, task.ID, task.Revision, input.SourceRevision, input.Decision, input.RiskLevel, input.Confidence, reasons, evaluation, input.NextAction, input.SnapshotSHA256, input.ClassifierVersion).Scan(&result.ID, &result.TaskID, &result.TaskRevision, &result.SourceRevision, &result.Decision, &result.RiskLevel, &result.Confidence, &rawReasons, &rawEvaluation, &result.NextAction, &result.SnapshotSHA256, &result.ClassifierVersion, &result.CreatedAt)
	if err != nil {
		return domain.AgentTaskClassification{}, fmt.Errorf("record agent task classification: %w", err)
	}
	if err := json.Unmarshal(rawReasons, &result.Reasons); err != nil {
		return domain.AgentTaskClassification{}, fmt.Errorf("decode agent task classification reasons: %w", err)
	}
	if err := json.Unmarshal(rawEvaluation, &result.Evaluation); err != nil {
		return domain.AgentTaskClassification{}, fmt.Errorf("decode agent task JEV evaluation: %w", err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata) SELECT tenant_id,requested_by,'agent_task.classified',$1::uuid::text,jsonb_build_object('task_revision',$2::int,'decision',$3::text,'risk_level',$4::text,'confidence',$5::int,'snapshot_sha256',$6::text,'classifier_version',$7::text) FROM agent_tasks WHERE id=$1::uuid`, task.ID, result.TaskRevision, result.Decision, result.RiskLevel, result.Confidence, result.SnapshotSHA256, result.ClassifierVersion); err != nil {
		return domain.AgentTaskClassification{}, fmt.Errorf("audit agent task classification: %w", err)
	}
	return result, nil
}

func scanAgentTaskClassification(row rowScanner) (domain.AgentTaskClassification, error) {
	var item domain.AgentTaskClassification
	var rawReasons []byte
	var rawEvaluation []byte
	if err := row.Scan(&item.ID, &item.TaskID, &item.TaskRevision, &item.SourceRevision, &item.Decision, &item.RiskLevel, &item.Confidence, &rawReasons, &rawEvaluation, &item.NextAction, &item.SnapshotSHA256, &item.ClassifierVersion, &item.CreatedAt); err != nil {
		return domain.AgentTaskClassification{}, err
	}
	if err := json.Unmarshal(rawReasons, &item.Reasons); err != nil {
		return domain.AgentTaskClassification{}, fmt.Errorf("decode agent task classification reasons: %w", err)
	}
	if err := json.Unmarshal(rawEvaluation, &item.Evaluation); err != nil {
		return domain.AgentTaskClassification{}, fmt.Errorf("decode agent task JEV evaluation: %w", err)
	}
	return item, nil
}

// latestAgentTaskClassification is the execution-facing read of the decision
// layer. It deliberately returns only an immutable decision recorded for this
// task rather than recomputing from current provider content: a later Issue
// edit must enter through a new provider revision and a new task.
func latestAgentTaskClassification(ctx context.Context, tx pgx.Tx, taskID uuid.UUID) (domain.AgentTaskClassification, error) {
	classification, err := scanAgentTaskClassification(tx.QueryRow(ctx, `SELECT `+agentTaskClassificationColumns+` FROM agent_task_classifications WHERE task_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1`, taskID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AgentTaskClassification{}, ErrInvalidAgentTaskPlan
	}
	if err != nil {
		return domain.AgentTaskClassification{}, fmt.Errorf("load agent task classification: %w", err)
	}
	return classification, nil
}

func queueAgentTaskInteractionResponse(ctx context.Context, tx pgx.Tx, installation domain.Installation, event domain.AgentTaskCommandEvent, interactionID uuid.UUID, body string, reaction domain.InteractionReaction, sourceRelease *domain.AgentTask) error {
	var createdAt time.Time
	if err := tx.QueryRow(ctx, `SELECT created_at FROM agent_task_interactions WHERE id=$1`, interactionID).Scan(&createdAt); err != nil {
		return fmt.Errorf("load agent task interaction marker boundary: %w", err)
	}
	payload := map[string]any{"tenant_id": installation.TenantID.String(), "provider": installation.Provider, "api_base_url": installation.APIBaseURL, "installation_external_id": installation.ExternalID, "credential_ref": installation.CredentialRef, "repository": event.Repository, "review_number": event.IssueNumber, "resource_kind": "issue", "comment_external_id": event.CommentExternalID, "reaction": reaction, "body": body, "marker": agentTaskInteractionMarker(interactionID), "marker_since": createdAt.UTC().Format(time.RFC3339Nano)}
	if sourceRelease != nil {
		payload["release_agent_task_source_id"] = sourceRelease.ID.String()
		payload["release_agent_task_source_revision"] = sourceRelease.Revision
	}
	_, err := tx.Exec(ctx, `INSERT INTO outbox_messages(aggregate_type,aggregate_id,topic,dedupe_key,payload) VALUES('agent_task_interaction',$1,'review.interaction.response',$2,$3::jsonb) ON CONFLICT(dedupe_key) DO NOTHING`, interactionID, "agent-task-interaction:"+interactionID.String()+":response", jsonPayload(payload))
	if err != nil {
		return fmt.Errorf("queue agent task interaction response: %w", err)
	}
	return nil
}

func queueAutomaticAgentTaskResponse(ctx context.Context, tx pgx.Tx, installation domain.Installation, event domain.ProviderIssueEvent, task domain.AgentTask, classification domain.AgentTaskClassification) error {
	body := automaticAgentTaskAcknowledgement(task, classification)
	if link := agentTaskConsoleURL(ctx, tx, installation.TenantID, task.ID); link != "" {
		body += "\n\n[Open task details in Open Review](" + link + ")"
	}
	payload := map[string]any{"tenant_id": installation.TenantID.String(), "provider": installation.Provider, "api_base_url": installation.APIBaseURL, "installation_external_id": installation.ExternalID, "credential_ref": installation.CredentialRef, "repository": event.Repository, "review_number": event.IssueNumber, "resource_kind": "issue", "comment_external_id": "", "reaction": domain.InteractionReactionNone, "body": body, "marker": "open-review-platform:agent-task:auto:" + task.ID.String(), "marker_since": task.CreatedAt.UTC().Format(time.RFC3339Nano)}
	if task.State == "received" {
		payload["release_agent_task_source_id"] = task.ID.String()
		payload["release_agent_task_source_revision"] = task.Revision
	}
	if _, err := tx.Exec(ctx, `INSERT INTO outbox_messages(aggregate_type,aggregate_id,topic,dedupe_key,payload) VALUES('agent_task',$1,'review.interaction.response',$2,$3::jsonb) ON CONFLICT(dedupe_key) DO NOTHING`, task.ID, "agent-task-auto:"+task.ID.String()+":ack", jsonPayload(payload)); err != nil {
		return fmt.Errorf("queue automatic agent task response: %w", err)
	}
	return nil
}

func automaticAgentTaskAcknowledgement(task domain.AgentTask, classification domain.AgentTaskClassification) string {
	intro := "### Agent candidate received\n\nThis Issue matched the repository's explicit automatic-admission label. Open Review recorded a **candidate only** and will verify the repository's immutable base commit before a bounded plan can be prepared."
	switch {
	case task.State == "rejected" || classification.Decision == "rejected":
		intro = "### Agent candidate rejected\n\nThis Issue matched the opt-in label, but the initial hard-policy check rejected it. **Source resolution and planning will not continue for this task.** Review the reason below and update the Issue before requesting a new task."
	case classification.Decision == "needs_context":
		intro = "### Agent candidate needs context\n\nThis Issue matched the opt-in label, but the initial check found insufficient task context. Source verification may continue; **planning remains blocked** until the Issue is updated and a new revision is evaluated."
	}
	return intro + "\n\n" + agentTaskJEVVerdict(classification) + "\n\n**No coding Agent, branch, pull request, or merge action has started.** Any future execution requires a separate bounded plan and owner/admin approval."
}

func queueAgentTaskFeedbackResponse(ctx context.Context, tx pgx.Tx, installation domain.Installation, event domain.AgentTaskFeedbackEvent, feedbackID uuid.UUID, task domain.AgentTask, classification domain.AgentTaskClassification) error {
	var createdAt time.Time
	if err := tx.QueryRow(ctx, `SELECT created_at FROM agent_task_feedback_cycles WHERE id=$1`, feedbackID).Scan(&createdAt); err != nil {
		return fmt.Errorf("load agent feedback marker boundary: %w", err)
	}
	body := "### Agent Draft PR feedback received\n\nOpen Review recorded a new **feedback candidate** for this exact Draft PR revision. It will first re-read the Draft PR branch and head SHA; then an owner/admin must approve a new bounded plan. **No coding Agent has started, and this command cannot merge or widen the existing change.**\n\n" + agentTaskJEVVerdict(classification)
	if link := agentTaskConsoleURL(ctx, tx, installation.TenantID, task.ID); link != "" {
		body += "\n\n[Open feedback task details in Open Review](" + link + ")"
	}
	payload := map[string]any{
		"tenant_id": installation.TenantID.String(), "provider": installation.Provider, "api_base_url": installation.APIBaseURL,
		"installation_external_id": installation.ExternalID, "credential_ref": installation.CredentialRef,
		"repository": event.Repository, "review_number": event.PullRequestNumber, "resource_kind": "merge_request", "comment_external_id": event.CommentExternalID,
		"reaction": domain.InteractionReactionEyes, "body": body,
		"marker": "open-review-platform:agent-task-feedback:" + feedbackID.String(), "marker_since": createdAt.UTC().Format(time.RFC3339Nano),
	}
	if task.State == "received" {
		payload["release_agent_task_source_id"] = task.ID.String()
		payload["release_agent_task_source_revision"] = task.Revision
	}
	if _, err := tx.Exec(ctx, `INSERT INTO outbox_messages(aggregate_type,aggregate_id,topic,dedupe_key,payload) VALUES('agent_task_feedback',$1,'review.interaction.response',$2,$3::jsonb) ON CONFLICT(dedupe_key) DO NOTHING`, feedbackID, "agent-task-feedback:"+feedbackID.String()+":response", jsonPayload(payload)); err != nil {
		return fmt.Errorf("queue agent feedback response: %w", err)
	}
	return nil
}

func agentTaskInteractionMarker(interactionID uuid.UUID) string {
	return "open-review-platform:agent-task:" + interactionID.String()
}

// queueAgentTaskAttemptStatus posts a compact terminal handoff only when the
// task originated from a verified provider Issue command. Console/API-created
// tasks have no provider comment to update, so their durable task detail is
// sufficient and this helper is intentionally a no-op for them.
func queueAgentTaskAttemptStatus(ctx context.Context, tx pgx.Tx, taskID, attemptID uuid.UUID, code, message string, adapterJobAttached bool) error {
	return queueAgentTaskAttemptProviderComment(ctx, tx, taskID, attemptID, "needs-attention", agentTaskAttemptStatusBody(code, message, adapterJobAttached))
}

func agentTaskAttemptStatusBody(code, message string, adapterJobAttached bool) string {
	body := "### Agent task needs attention\n\nThe approved plan received a leased execution attempt, but **no coding action was performed**. `" + code + "`: " + safeAgentTaskCommentText(message) + "\n\nDeploy or configure the isolated sandbox adapter, then create and approve a new bounded plan revision."
	if adapterJobAttached {
		body = "### Agent task needs attention\n\nThe task's execution lease was revoked while an adapter job was attached. `" + code + "`: " + safeAgentTaskCommentText(message) + "\n\nCoding or a provider write may already have started. Open Review requested cancellation of the exact adapter job, but that does not undo a branch push or Draft PR/MR. Check the repository branch and Draft before creating and approving a new bounded plan revision."
	}
	return body
}

// queueAgentTaskAdapterTerminalStatus reflects a sandbox result back to the
// originating Issue. A successful callback means only that an adapter reports
// a draft PR; it is intentionally never rendered as a review approval or a
// merge decision.
func queueAgentTaskAdapterTerminalStatus(ctx context.Context, tx pgx.Tx, task domain.AgentTask, attempt domain.AgentTaskAttempt, event domain.AgentTaskAdapterEvent) error {
	status, body := agentTaskAdapterTerminalMessage(task.OriginKind, attempt, event)
	return queueAgentTaskAttemptProviderComment(ctx, tx, task.ID, attempt.ID, status, body)
}

func agentTaskAdapterTerminalMessage(originKind string, attempt domain.AgentTaskAttempt, event domain.AgentTaskAdapterEvent) (string, string) {
	summary := safeAgentTaskCommentText(event.Summary)
	code := safeAgentTaskCommentText(event.ErrorCode)
	switch event.Kind {
	case "completed":
		body := "### Agent draft pull request ready\n\nThe isolated adapter completed the approved plan and reported a draft pull request. **It has not been reviewed, approved, or merged.**\n\n" + summary + "\n\n- Branch: `" + attempt.BranchName + "`\n- Revision: `" + attempt.HeadSHA + "`\n- [Open draft pull request](" + attempt.PullRequestURL + ")\n\nReview the diff and run normal repository checks before any merge decision."
		if originKind == "pull_request" {
			body = "### Agent Draft updated\n\nThe isolated adapter completed the approved feedback plan and updated the **same Draft PR/MR**. This revision has **not been re-reviewed, approved, or merged**.\n\n" + summary + "\n\n- Branch: `" + attempt.BranchName + "`\n- New revision: `" + attempt.HeadSHA + "`\n- [Open the existing Draft](" + attempt.PullRequestURL + ")\n\nReview the new diff and repository checks before any merge decision. Automatic review of this Draft depends on the repository's separate Draft-review settings."
		}
		if attempt.PatchSHA256 != "" {
			body += "\n\nValidated patch SHA-256: `" + attempt.PatchSHA256 + "` (" + fmt.Sprint(attempt.ChangedFileCount) + " file(s), " + fmt.Sprint(attempt.DiffBytes) + " diff byte(s))."
		}
		if attempt.VerificationProfileSHA256 != "" {
			body += "\n\nOne deployment-approved repository command passed (profile SHA-256 `" + attempt.VerificationProfileSHA256 + "`, output SHA-256 `" + attempt.VerificationOutputSHA256 + "`). This is not product acceptance or a complete CI/security/UI check."
		}
		return "completed", body
	case "needs_attention":
		body := "### Agent task needs attention\n\nThe adapter did not return a verified Draft PR/MR result. `" + code + "`: " + summary + "\n\nA provider push or Draft request may already have been accepted. Check the repository branch and Draft PR/MR before creating and approving a new bounded plan."
		return "needs-attention", body
	default:
		body := "### Agent task failed\n\nThe isolated adapter did not complete the approved plan. `" + code + "`: " + summary + "\n\nNo merge action was taken. Check for a branch or Draft PR/MR left by an in-flight provider write, then review the task details before preparing a new bounded plan."
		return "failed", body
	}
}

// queueAgentTaskAttemptProviderComment is the shared provider-boundary
// formatter. Console/API-created tasks do not have an Issue origin and are a
// safe no-op; provider writes are dispatched through the existing outbox.
func queueAgentTaskAttemptProviderComment(ctx context.Context, tx pgx.Tx, taskID, attemptID uuid.UUID, status, body string) error {
	var installation domain.Installation
	var repository string
	var issueNumber int
	var markerSince time.Time
	err := tx.QueryRow(ctx, `
		SELECT p.id,p.tenant_id,p.provider,p.external_id,p.repository_scope,p.automatic_reviews,p.minimum_severity,p.api_base_url,p.credential_ref,p.active,p.verification_state,
		       i.repository,i.issue_number,i.created_at
		FROM agent_task_interactions i
		JOIN agent_tasks t ON t.id=i.task_id
		JOIN provider_installations p ON p.id=t.installation_id
		WHERE i.task_id=$1 AND i.result='accepted'
		ORDER BY i.created_at DESC,i.id DESC
		LIMIT 1`, taskID).Scan(
		&installation.ID, &installation.TenantID, &installation.Provider, &installation.ExternalID, &installation.RepositoryScope,
		&installation.AutomaticReviews, &installation.MinimumSeverity, &installation.APIBaseURL, &installation.CredentialRef,
		&installation.Active, &installation.VerificationState, &repository, &issueNumber, &markerSince,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return queueAgentTaskFeedbackProviderComment(ctx, tx, taskID, "attempt:"+status+":"+attemptID.String(), "attempt:"+status+":"+attemptID.String(), body)
	}
	if err != nil {
		return fmt.Errorf("load provider origin for agent task status: %w", err)
	}
	link := agentTaskConsoleURL(ctx, tx, installation.TenantID, taskID)
	if link != "" {
		body += "\n\n[Open task details in Open Review](" + link + ")"
	}
	payload := map[string]any{
		"tenant_id": installation.TenantID.String(), "provider": installation.Provider, "api_base_url": installation.APIBaseURL,
		"installation_external_id": installation.ExternalID, "credential_ref": installation.CredentialRef,
		"repository": repository, "review_number": issueNumber, "resource_kind": "issue", "comment_external_id": "",
		"reaction": domain.InteractionReactionNone, "body": body,
		"marker": agentAttemptPublicationMarker(attemptID, status), "marker_since": markerSince.UTC().Format(time.RFC3339Nano),
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbox_messages(aggregate_type,aggregate_id,topic,dedupe_key,payload) VALUES('agent_task_attempt',$1,'review.interaction.response',$2,$3::jsonb) ON CONFLICT(dedupe_key) DO NOTHING`, attemptID, "agent-task-attempt:"+attemptID.String()+":"+status, jsonPayload(payload)); err != nil {
		return fmt.Errorf("queue agent task attempt status: %w", err)
	}
	return nil
}

// queueAgentTaskSourceProviderComment uses one version-fenced provider marker
// for source admission, retry recovery, plan submission, and approval.
// An older queued source result can never overwrite a newer plan revision.
func queueAgentTaskSourceProviderComment(ctx context.Context, tx pgx.Tx, task domain.AgentTask, status, code, message string) error {
	if status != "failed" && status != "ready" && status != "plan" && status != "approved" {
		return ErrInvalidAgentTask
	}
	taskID := task.ID
	var installation domain.Installation
	var repository string
	var issueNumber int
	var markerSince time.Time
	var body string
	var planRevision int
	if status == "failed" {
		body = "### Agent task needs attention\n\nOpen Review could not complete source verification, so **no plan or coding Agent was started**. `" + safeAgentTaskCommentText(code) + "`: " + safeAgentTaskCommentText(message) + "\n\nIf the Issue has not changed, retry source verification from Open Review after restoring the connection. If the Issue changed, submit a new command for its current revision."
	} else if status == "plan" || status == "approved" {
		if (status == "plan" && task.State != "awaiting_approval") || (status == "approved" && task.State != "execution_queued") {
			return ErrInvalidAgentTaskPlan
		}
		plan, planErr := scanAgentTaskPlan(tx.QueryRow(ctx, `SELECT `+agentTaskPlanColumns+` FROM agent_task_plans WHERE task_id=$1 ORDER BY revision DESC LIMIT 1`, taskID))
		if planErr != nil {
			return fmt.Errorf("load agent plan for provider status: %w", planErr)
		}
		wantPlanState := "awaiting_approval"
		if status == "approved" {
			wantPlanState = "approved"
		}
		if plan.State != wantPlanState || validateAgentTaskPlanIntegrity(plan) != nil {
			return ErrInvalidAgentTaskPlan
		}
		planRevision = plan.Revision
		if status == "approved" {
			body = agentTaskPlanApprovedBody(task, plan)
		} else {
			body = agentTaskPlanReadyBody(task, plan)
		}
	} else {
		classification, classificationErr := latestAgentTaskClassification(ctx, tx, taskID)
		if classificationErr != nil {
			return fmt.Errorf("load final agent source admission: %w", classificationErr)
		}
		body, classificationErr = agentTaskSourceReadyBody(task, classification)
		if classificationErr != nil {
			return classificationErr
		}
	}
	err := tx.QueryRow(ctx, `
		SELECT p.id,p.tenant_id,p.provider,p.external_id,p.repository_scope,p.automatic_reviews,p.minimum_severity,p.api_base_url,p.credential_ref,p.active,p.verification_state,
		       i.repository,i.issue_number,i.created_at
		FROM agent_task_interactions i
		JOIN agent_tasks t ON t.id=i.task_id
		JOIN provider_installations p ON p.id=t.installation_id
		WHERE i.task_id=$1 AND i.result='accepted'
		ORDER BY i.created_at ASC,i.id ASC LIMIT 1`, taskID).Scan(
		&installation.ID, &installation.TenantID, &installation.Provider, &installation.ExternalID, &installation.RepositoryScope,
		&installation.AutomaticReviews, &installation.MinimumSeverity, &installation.APIBaseURL, &installation.CredentialRef,
		&installation.Active, &installation.VerificationState, &repository, &issueNumber, &markerSince,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		if task.RequestedBy == "policy:auto" && task.OriginKind == "issue" {
			if err = tx.QueryRow(ctx, `SELECT tenant_id,provider,api_base_url,external_id,credential_ref FROM provider_installations WHERE id=$1 AND tenant_id=$2`, task.InstallationID, task.TenantID).Scan(&installation.TenantID, &installation.Provider, &installation.APIBaseURL, &installation.ExternalID, &installation.CredentialRef); err != nil {
				return fmt.Errorf("load automatic agent task installation for source status: %w", err)
			}
			repository, issueNumber, markerSince = task.Repository, task.OriginNumber, task.CreatedAt
		} else {
			feedbackBody := body
			if status == "failed" {
				feedbackBody = "### Agent Draft PR feedback needs attention\n\nOpen Review could not verify both the immutable Draft PR/MR head and the original feedback comment, so **no plan or coding Agent was started**. `" + safeAgentTaskCommentText(code) + "`: " + safeAgentTaskCommentText(message) + "\n\nIf the comment and Draft revision are unchanged, retry source verification in Open Review. If either changed, submit a new `@openreview revise <feedback>` comment for the current revision."
			}
			markerStatus := "source:failed"
			if status == "plan" {
				// A feedback child lives on a Draft PR/MR. Its plan revisions
				// are historical comments, not stale mutable commands.
				markerStatus = fmt.Sprintf("plan:%d", planRevision)
			} else if status == "approved" {
				markerStatus = fmt.Sprintf("approved:%d", planRevision)
			}
			return queueAgentTaskFeedbackProviderComment(ctx, tx, taskID, fmt.Sprintf("source:%s:%d", status, task.Revision), markerStatus, feedbackBody)
		}
	} else if err != nil {
		return fmt.Errorf("load provider origin for agent task source failure: %w", err)
	}
	if link := agentTaskConsoleURL(ctx, tx, installation.TenantID, taskID); link != "" {
		body += "\n\n[Open task details in Open Review](" + link + ")"
	}
	payload := map[string]any{
		"tenant_id": installation.TenantID.String(), "provider": installation.Provider, "api_base_url": installation.APIBaseURL,
		"installation_external_id": installation.ExternalID, "credential_ref": installation.CredentialRef,
		"repository": repository, "review_number": issueNumber, "resource_kind": "issue", "comment_external_id": "",
		"reaction": domain.InteractionReactionNone, "body": body,
		"marker": "open-review-platform:agent-task-source:" + taskID.String(), "marker_since": markerSince.UTC().Format(time.RFC3339Nano),
		"status_version": task.Revision,
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbox_messages(aggregate_type,aggregate_id,topic,dedupe_key,payload) VALUES('agent_task',$1,'review.interaction.response',$2,$3::jsonb) ON CONFLICT(dedupe_key) DO NOTHING`, taskID, fmt.Sprintf("agent-task-source:%s:%s:%d", taskID, status, task.Revision), jsonPayload(payload)); err != nil {
		return fmt.Errorf("queue agent task source status response: %w", err)
	}
	return nil
}

func agentTaskPlanReadyBody(task domain.AgentTask, plan domain.AgentTaskPlan) string {
	body := fmt.Sprintf("### Agent plan submitted for approval\n\nPlan revision **%d** for task `%s` is bound to SHA-256 `%s`. Review the complete bounded plan and frozen source in Open Review before deciding. **No coding Agent, branch, or Draft PR/MR was started by submitting this plan.**", plan.Revision, task.ID, plan.PlanSHA256)
	if task.OriginKind == "issue" {
		body += "\n\nA mapped workspace owner/admin may approve in Open Review or comment `@openreview approve " + plan.PlanSHA256 + "` on this exact Issue revision. High/critical plans require an approver other than the plan author. A newer plan revision supersedes this digest."
	} else {
		body += "\n\nA workspace owner/admin must approve this feedback plan in Open Review. Approval commands on the Draft PR/MR are not accepted; a newer plan revision supersedes this digest."
	}
	return body
}

func agentTaskPlanApprovedBody(task domain.AgentTask, plan domain.AgentTaskPlan) string {
	return fmt.Sprintf("### Agent plan approved ✅\n\nPlan revision **%d** (`%s`) for task `%s` was approved. Execution is **queued**, not yet complete. No coding result, Draft PR/MR, review approval, or merge is implied by this update. Open Review will post the attempt result after the isolated adapter reports it.", plan.Revision, plan.PlanSHA256, task.ID)
}

func agentTaskCancelledComment(taskID uuid.UUID, taskURL string) string {
	body := "### Agent task cancelled\n\nTask `" + taskID.String() + "` is **cancelled**. Its adapter lease was superseded and late result callbacks cannot complete the task. A provider write already in flight may still leave a branch or Draft PR/MR; check the repository and close it if needed. Audit evidence is retained."
	if taskURL != "" {
		body += "\n\n[Open task details in Open Review](" + taskURL + ")"
	}
	return body
}

func agentTaskSourceReadyBody(task domain.AgentTask, classification domain.AgentTaskClassification) (string, error) {
	origin, nextCandidate := "Issue", "Issue revision"
	if task.OriginKind == "pull_request" {
		origin, nextCandidate = "Draft PR/MR and original feedback comment", "Draft PR/MR revision and a new feedback comment"
	}
	body := "### Agent source verified\n\nOpen Review re-read the " + origin + " and froze base commit `" + task.SourceBaseSHA + "`. **No coding Agent has started.**"
	switch classification.Decision {
	case "rejected":
		body += " The current admission was **rejected**; planning and execution are blocked for this " + nextCandidate + "."
	case "needs_context":
		body += " Planning remains **blocked for missing context**. Add the expected behavior and acceptance criteria, then request a new task for a new " + nextCandidate + "."
	case "requires_human":
		body += " A bounded plan may now be prepared in Open Review; an owner/admin must approve its exact revision before execution."
	default:
		return "", fmt.Errorf("final agent source admission is not publishable")
	}
	return body + "\n\n" + agentTaskJEVVerdict(classification), nil
}

// AgentTaskSourceStatusCurrent skips a stale delivery before provider
// credential resolution. The publication fence repeats this check under a
// task-row lock because this unlocked precheck cannot guarantee ordering.
func (s *PostgresStore) AgentTaskSourceStatusCurrent(ctx context.Context, taskID uuid.UUID, statusVersion int) (bool, error) {
	if taskID == uuid.Nil || statusVersion < 0 {
		return false, ErrInvalidAgentTask
	}
	var taskRevision, newestStatusVersion int
	marker := "open-review-platform:agent-task-source:" + taskID.String()
	err := s.pool.QueryRow(ctx, `
		SELECT revision,(
			SELECT coalesce(max(greatest(
				coalesce((payload->>'status_version')::int,0),
				coalesce(substring(dedupe_key from ':([0-9]+)$')::int,0)
			)),0)
			FROM outbox_messages
			WHERE aggregate_id=$1 AND topic='review.interaction.response'
			  AND payload->>'marker'=$2
		)
		FROM agent_tasks WHERE id=$1`, taskID, marker).Scan(&taskRevision, &newestStatusVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check agent source publication version: %w", err)
	}
	if statusVersion > taskRevision {
		return false, ErrInvalidAgentTask
	}
	// The task may have advanced to execution, cancellation, or a terminal
	// result without queuing another source-marker update. In that case the
	// older prose is no longer a truthful current-status comment.
	return taskRevision == statusVersion && newestStatusVersion == statusVersion, nil
}

// WithAgentTaskSourcePublicationFence serializes provider writes for one
// task across responder replicas. The source worker updates the same task row
// before queuing a newer status, so the row lock also orders a concurrent
// source recovery after an in-flight provider write. A superseded outbox
// status is acknowledged without publishing even if it has never been sent.
func (s *PostgresStore) WithAgentTaskSourcePublicationFence(ctx context.Context, taskID uuid.UUID, statusVersion int, publish func(context.Context) error) error {
	if taskID == uuid.Nil || statusVersion < 0 || publish == nil {
		return ErrInvalidAgentTask
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin agent source publication fence: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var taskRevision int
	if err := tx.QueryRow(ctx, `SELECT revision FROM agent_tasks WHERE id=$1 FOR UPDATE`, taskID).Scan(&taskRevision); errors.Is(err, pgx.ErrNoRows) {
		// Tenant/task deletion revokes an undelivered external status.
		return tx.Commit(ctx)
	} else if err != nil {
		return fmt.Errorf("lock agent source publication: %w", err)
	}
	if statusVersion > taskRevision {
		return ErrInvalidAgentTask
	}
	if statusVersion < taskRevision {
		return tx.Commit(ctx)
	}
	var newestStatusVersion int
	marker := "open-review-platform:agent-task-source:" + taskID.String()
	if err := tx.QueryRow(ctx, `
		SELECT coalesce(max(greatest(
			coalesce((payload->>'status_version')::int,0),
			coalesce(substring(dedupe_key from ':([0-9]+)$')::int,0)
		)),0)
		FROM outbox_messages
		WHERE aggregate_id=$1 AND topic='review.interaction.response'
		  AND payload->>'marker'=$2`, taskID, marker).Scan(&newestStatusVersion); err != nil {
		return fmt.Errorf("load latest agent source publication: %w", err)
	}
	if newestStatusVersion != statusVersion {
		return tx.Commit(ctx)
	}
	if err := publish(ctx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit agent source publication fence: %w", err)
	}
	return nil
}

// queueAgentTaskFeedbackProviderComment mirrors Issue-origin task status onto
// the originating Draft PR/MR. Child feedback tasks do not have an Issue
// interaction row by design, so this separate lookup prevents a silent
// feedback execution while keeping provider writes out of the control-plane
// transaction.
func queueAgentTaskFeedbackProviderComment(ctx context.Context, tx pgx.Tx, taskID uuid.UUID, status, markerStatus, body string) error {
	var installation domain.Installation
	var repository string
	var pullRequestNumber int
	var markerSince time.Time
	err := tx.QueryRow(ctx, `
		SELECT p.id,p.tenant_id,p.provider,p.external_id,p.repository_scope,p.automatic_reviews,p.minimum_severity,p.api_base_url,p.credential_ref,p.active,p.verification_state,
		       f.repository,f.pull_request_number,f.created_at
		FROM agent_task_feedback_cycles f
		JOIN agent_tasks t ON t.id=f.child_task_id
		JOIN provider_installations p ON p.id=t.installation_id
		WHERE f.child_task_id=$1
		ORDER BY f.created_at DESC,f.id DESC LIMIT 1`, taskID).Scan(
		&installation.ID, &installation.TenantID, &installation.Provider, &installation.ExternalID, &installation.RepositoryScope,
		&installation.AutomaticReviews, &installation.MinimumSeverity, &installation.APIBaseURL, &installation.CredentialRef,
		&installation.Active, &installation.VerificationState, &repository, &pullRequestNumber, &markerSince,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load provider feedback origin for agent task status: %w", err)
	}
	if link := agentTaskConsoleURL(ctx, tx, installation.TenantID, taskID); link != "" {
		body += "\n\n[Open feedback task details in Open Review](" + link + ")"
	}
	payload := map[string]any{
		"tenant_id": installation.TenantID.String(), "provider": installation.Provider, "api_base_url": installation.APIBaseURL,
		"installation_external_id": installation.ExternalID, "credential_ref": installation.CredentialRef,
		"repository": repository, "review_number": pullRequestNumber, "resource_kind": "merge_request", "comment_external_id": "",
		"reaction": domain.InteractionReactionNone, "body": body,
		"marker": "open-review-platform:agent-task-feedback-status:" + taskID.String() + ":" + markerStatus, "marker_since": markerSince.UTC().Format(time.RFC3339Nano),
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbox_messages(aggregate_type,aggregate_id,topic,dedupe_key,payload) VALUES('agent_task',$1,'review.interaction.response',$2,$3::jsonb) ON CONFLICT(dedupe_key) DO NOTHING`, taskID, "agent-task-feedback-status:"+taskID.String()+":"+status, jsonPayload(payload)); err != nil {
		return fmt.Errorf("queue agent feedback task status: %w", err)
	}
	return nil
}

func safeAgentTaskCommentText(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Join(strings.Fields(value), " ")
	value = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "[", "\\[", "]", "\\]", "`", "'").Replace(value)
	return value
}

func (s *PostgresStore) ListAgentTaskPolicies(ctx context.Context, actor, tenantSlug string, limit int) ([]domain.AgentTaskPolicy, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrInvalidAgentTask
	}
	tenantID, _, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT `+agentTaskPolicyColumns+` FROM agent_task_policies WHERE tenant_id=$1 ORDER BY repository,id LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("list agent task policies: %w", err)
	}
	defer rows.Close()
	items := make([]domain.AgentTaskPolicy, 0)
	for rows.Next() {
		item, err := scanAgentTaskPolicy(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agent task policies: %w", err)
	}
	return items, nil
}

// GetAgentTaskPolicy resolves one exact repository identity independently of
// the bounded overview list. An older policy beyond the first page must still
// supply its current revision before the Console can attempt an update.
func (s *PostgresStore) GetAgentTaskPolicy(ctx context.Context, actor, tenantSlug string, provider domain.Provider, apiBaseURL, repository string) (domain.AgentTaskPolicy, error) {
	apiBaseURL = strings.TrimSuffix(strings.TrimSpace(apiBaseURL), "/")
	repository = strings.Trim(strings.TrimSpace(repository), "/")
	if !provider.Valid() || apiBaseURL == "" || repository == "" || len(apiBaseURL) > 2048 || len(repository) > 512 {
		return domain.AgentTaskPolicy{}, ErrInvalidAgentTask
	}
	tenantID, _, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.AgentTaskPolicy{}, err
	}
	item, err := scanAgentTaskPolicy(s.pool.QueryRow(ctx, `SELECT `+agentTaskPolicyColumns+` FROM agent_task_policies WHERE tenant_id=$1 AND provider=$2 AND api_base_url=$3 AND repository=$4`, tenantID, provider, apiBaseURL, repository))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AgentTaskPolicy{}, ErrNotFound
	}
	if err != nil {
		return domain.AgentTaskPolicy{}, fmt.Errorf("get agent task policy: %w", err)
	}
	return item, nil
}

func (s *PostgresStore) SaveAgentTaskPolicy(ctx context.Context, actor, tenantSlug string, input domain.AgentTaskPolicyInput) (domain.AgentTaskPolicy, error) {
	backendOmitted := strings.TrimSpace(input.DecisionBackend) == ""
	input.APIBaseURL = strings.TrimSuffix(strings.TrimSpace(input.APIBaseURL), "/")
	input.Repository = strings.Trim(strings.TrimSpace(input.Repository), "/")
	input.Mode = strings.ToLower(strings.TrimSpace(input.Mode))
	// Older Console/API clients did not send an execution envelope. Preserve
	// their safest compatible behavior while every newly saved policy returns
	// explicit bounded values to the caller.
	if input.MaxAttempts == 0 {
		input.MaxAttempts = 1
	}
	if input.MaxExecutionSeconds == 0 {
		input.MaxExecutionSeconds = 1800
	}
	if input.Mode != "manual" {
		input.MaxFeedbackCycles = 0
	}
	if input.ExecutorProfile == "" {
		input.ExecutorProfile = "codex"
	}
	if input.DecisionBackend == "" {
		input.DecisionBackend = "jev"
	}
	if input.AutoAdmissionLabel == "" {
		input.AutoAdmissionLabel = "openreview:implement"
	}
	input.ExecutorProfile = strings.ToLower(strings.TrimSpace(input.ExecutorProfile))
	input.DecisionBackend = strings.ToLower(strings.TrimSpace(input.DecisionBackend))
	input.AutoAdmissionLabel = strings.TrimSpace(input.AutoAdmissionLabel)
	if input.Mode != "manual" {
		input.AutoAdmissionEnabled = false
	}
	if !input.Valid() {
		return domain.AgentTaskPolicy{}, ErrInvalidAgentTask
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.AgentTaskPolicy{}, fmt.Errorf("begin agent task policy: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.AgentTaskPolicy{}, err
	}
	if role != "owner" && role != "admin" {
		return domain.AgentTaskPolicy{}, ErrForbidden
	}
	_, err = authorizedAgentInstallationTx(ctx, tx, tenantID, input.Provider, input.APIBaseURL, input.Repository)
	if err != nil {
		return domain.AgentTaskPolicy{}, fmt.Errorf("load agent policy installation: %w", err)
	}
	var currentRevision int
	var currentBackend string
	var currentWorkflow domain.AgentWorkflowPolicy
	err = tx.QueryRow(ctx, `SELECT revision,decision_backend,workflow FROM agent_task_policies WHERE tenant_id=$1 AND provider=$2 AND api_base_url=$3 AND repository=$4 FOR UPDATE`, tenantID, input.Provider, input.APIBaseURL, input.Repository).Scan(&currentRevision, &currentBackend, &currentWorkflow)
	if errors.Is(err, pgx.ErrNoRows) {
		if input.Revision != 0 {
			return domain.AgentTaskPolicy{}, ErrRevisionConflict
		}
	} else if err != nil {
		return domain.AgentTaskPolicy{}, err
	} else if currentRevision != input.Revision {
		return domain.AgentTaskPolicy{}, ErrRevisionConflict
	}
	if currentRevision > 0 && backendOmitted {
		// Old clients may update a budget or mode without knowing the newer
		// decision-backend field. Never silently switch their existing policy.
		input.DecisionBackend = currentBackend
	}
	var result domain.AgentTaskPolicy
	if currentRevision == 0 {
		result, err = scanAgentTaskPolicy(tx.QueryRow(ctx, `INSERT INTO agent_task_policies(tenant_id,provider,api_base_url,repository,mode,max_attempts,max_execution_seconds,max_feedback_cycles,executor_profile,decision_backend,auto_admission_enabled,auto_admission_label,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING `+agentTaskPolicyColumns, tenantID, input.Provider, input.APIBaseURL, input.Repository, input.Mode, input.MaxAttempts, input.MaxExecutionSeconds, input.MaxFeedbackCycles, input.ExecutorProfile, input.DecisionBackend, input.AutoAdmissionEnabled, input.AutoAdmissionLabel, actor))
	} else {
		result, err = scanAgentTaskPolicy(tx.QueryRow(ctx, `UPDATE agent_task_policies SET mode=$5,max_attempts=$6,max_execution_seconds=$7,max_feedback_cycles=$8,executor_profile=$9,decision_backend=$10,auto_admission_enabled=$11,auto_admission_label=$12,revision=revision+1,updated_by=$13,updated_at=now() WHERE tenant_id=$1 AND provider=$2 AND api_base_url=$3 AND repository=$4 RETURNING `+agentTaskPolicyColumns, tenantID, input.Provider, input.APIBaseURL, input.Repository, input.Mode, input.MaxAttempts, input.MaxExecutionSeconds, input.MaxFeedbackCycles, input.ExecutorProfile, input.DecisionBackend, input.AutoAdmissionEnabled, input.AutoAdmissionLabel, actor))
	}
	if err != nil {
		return domain.AgentTaskPolicy{}, fmt.Errorf("save agent task policy: %w", err)
	}
	workflow := currentWorkflow
	if input.Workflow != nil {
		workflow = *input.Workflow
	}
	if !workflow.Valid() {
		return domain.AgentTaskPolicy{}, ErrInvalidAgentTask
	}
	workflowJSON, _ := json.Marshal(workflow)
	if _, err = tx.Exec(ctx, `UPDATE agent_task_policies SET workflow=$2 WHERE id=$1`, result.ID, workflowJSON); err != nil {
		return domain.AgentTaskPolicy{}, err
	}
	result.Workflow = workflow
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata) VALUES($1,$2,'agent_task.policy_saved',$3,jsonb_build_object('provider',$4::text,'api_base_url',$5::text,'repository',$6::text,'mode',$7::text,'max_attempts',$8::int,'max_execution_seconds',$9::int,'max_feedback_cycles',$10::int,'executor_profile',$11::text,'decision_backend',$12::text,'auto_admission_enabled',$13::boolean,'auto_admission_label',$14::text,'revision',$15::int,'workflow',$16::jsonb))`, tenantID, actor, result.ID.String(), result.Provider, result.APIBaseURL, result.Repository, result.Mode, result.MaxAttempts, result.MaxExecutionSeconds, result.MaxFeedbackCycles, result.ExecutorProfile, result.DecisionBackend, result.AutoAdmissionEnabled, result.AutoAdmissionLabel, result.Revision, workflowJSON); err != nil {
		return domain.AgentTaskPolicy{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.AgentTaskPolicy{}, fmt.Errorf("commit agent task policy: %w", err)
	}
	return result, nil
}

func scanAgentTaskPolicy(row rowScanner) (domain.AgentTaskPolicy, error) {
	var item domain.AgentTaskPolicy
	err := row.Scan(&item.ID, &item.Provider, &item.APIBaseURL, &item.Repository, &item.Mode, &item.MaxAttempts, &item.MaxExecutionSeconds, &item.MaxFeedbackCycles, &item.ExecutorProfile, &item.DecisionBackend, &item.AutoAdmissionEnabled, &item.AutoAdmissionLabel, &item.Revision, &item.UpdatedBy, &item.UpdatedAt, &item.Workflow)
	return item, err
}

func criterionResultsJSON(results []domain.AgentCriterionResult) []byte {
	if results == nil {
		results = []domain.AgentCriterionResult{}
	}
	raw, _ := json.Marshal(results)
	return raw
}
func equalCriterionResults(a, b []domain.AgentCriterionResult) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}
