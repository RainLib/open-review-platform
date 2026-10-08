package store

import (
	"context"
	"errors"
	"strings"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type AgentPublicationRecoveryTarget struct {
	Workflow   AgentWorkflowTarget
	Checkpoint domain.AgentTaskPublicationCheckpoint
}

// Eligibility is checked again under task/attempt locks before completing a
// recovery. A new plan, attempt or descendant fences the old publication.
const recoverableAgentPublicationSQL = `a.state='needs_attention' AND t.state='needs_attention'
 AND t.origin_kind='pull_request' AND t.workflow->>'enabled'='true'
 AND (a.error_code='agent_publication_unverified' OR
      (a.error_code='agent_adapter_execution_failed' AND a.error_message LIKE 'The adapter stopped at stage draft_publication %'))
 AND p.state='approved' AND p.revision=a.plan_revision
 AND EXISTS(SELECT 1 FROM memberships m WHERE m.tenant_id=t.tenant_id AND m.subject=p.approved_by AND m.active AND m.role IN ('owner','admin'))
 AND p.id=(SELECT id FROM agent_task_plans WHERE task_id=t.id ORDER BY revision DESC LIMIT 1)
 AND a.id=(SELECT id FROM agent_task_attempts WHERE task_id=t.id ORDER BY created_at DESC LIMIT 1)
 AND NOT EXISTS(SELECT 1 FROM agent_tasks newer WHERE newer.tenant_id=t.tenant_id
     AND newer.execution_branch=t.execution_branch AND newer.created_at>t.created_at
     AND newer.state NOT IN ('cancelled','rejected','superseded','failed'))`

func (s *PostgresStore) ClaimAgentPublicationRecovery(ctx context.Context, worker string) (*AgentPublicationRecoveryTarget, error) {
	if strings.TrimSpace(worker) == "" || len(worker) > 200 {
		return nil, ErrInvalidAgentTask
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO agent_task_publication_recoveries(attempt_id,original_error_code,original_error_message)
 SELECT a.id,a.error_code,a.error_message FROM agent_task_attempts a JOIN agent_tasks t ON t.id=a.task_id
 JOIN agent_task_plans p ON p.id=a.plan_id JOIN agent_task_publication_checkpoints c ON c.attempt_id=a.id AND c.attempt_number=a.attempt
 WHERE `+recoverableAgentPublicationSQL+` AND c.recorded_at<=a.deadline_at AND c.branch_name=t.execution_branch AND c.verification_profile_sha256<>''
 AND c.verification_output_sha256<>'' ON CONFLICT DO NOTHING`)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE agent_task_publication_recoveries SET state='rejected',reason='provider_read_attempts_exhausted',worker_id=NULL,locked_until=NULL,updated_at=now()
 WHERE state='queued' AND read_attempt>=3 AND (locked_until IS NULL OR locked_until<now())`); err != nil {
		return nil, err
	}
	var attemptID uuid.UUID
	err = tx.QueryRow(ctx, `WITH candidate AS (SELECT attempt_id FROM agent_task_publication_recoveries
 WHERE state='queued' AND read_attempt<3 AND available_at<=now() AND (locked_until IS NULL OR locked_until<now())
 ORDER BY available_at,attempt_id FOR UPDATE SKIP LOCKED LIMIT 1)
 UPDATE agent_task_publication_recoveries r SET read_attempt=read_attempt+1,worker_id=$1,
 locked_until=now()+interval '60 seconds',updated_at=now() FROM candidate c WHERE r.attempt_id=c.attempt_id RETURNING r.attempt_id`, worker).Scan(&attemptID)
	if errors.Is(err, pgx.ErrNoRows) {
		if err = tx.Commit(ctx); err != nil {
			return nil, err
		}
		return nil, ErrNoQueuedAgentTask
	}
	if err != nil {
		return nil, err
	}
	result := &AgentPublicationRecoveryTarget{}
	w := &result.Workflow
	w.Attempt, err = scanAgentTaskAttempt(tx.QueryRow(ctx, `SELECT `+agentTaskAttemptColumns+` FROM agent_task_attempts WHERE id=$1`, attemptID))
	if err != nil {
		return nil, err
	}
	w.Task, err = scanAgentTask(tx.QueryRow(ctx, `SELECT `+agentTaskColumns+` FROM agent_tasks WHERE id=$1`, w.Attempt.TaskID))
	if err != nil {
		return nil, err
	}
	c := &result.Checkpoint
	err = tx.QueryRow(ctx, `SELECT attempt_id,attempt_number,adapter_job_id,branch_name,head_sha,patch_sha256,changed_file_count,diff_bytes,
 verification_profile_sha256,verification_output_sha256,verification_output_bytes,verification_criteria,recorded_at
 FROM agent_task_publication_checkpoints WHERE attempt_id=$1 AND attempt_number=$2`, attemptID, w.Attempt.Attempt).Scan(
		&c.AttemptID, &c.AttemptNumber, &c.AdapterJobID, &c.BranchName, &c.HeadSHA, &c.PatchSHA256, &c.ChangedFileCount, &c.DiffBytes,
		&c.VerificationProfileSHA256, &c.VerificationOutputSHA256, &c.VerificationOutputBytes, &c.VerificationCriteria, &c.RecordedAt)
	if err != nil {
		return nil, err
	}
	w.Acceptance.HeadSHA = c.HeadSHA
	w.Attempt.PullRequestNumber = w.Task.OriginNumber
	w.RequireDraftOwnership = true
	branch, err := s.agentFeedbackTargetBranch(ctx, w.Task.ID)
	if err != nil {
		return nil, err
	}
	var externalID, credentialRef, slug string
	var active bool
	err = tx.QueryRow(ctx, `SELECT p.external_id,p.credential_ref,t.slug,p.active AND p.verification_state='verified'
 FROM provider_installations p JOIN tenants t ON t.id=p.tenant_id WHERE p.id=$1`, w.Task.InstallationID).Scan(&externalID, &credentialRef, &slug, &active)
	if err != nil {
		return nil, err
	}
	if !active {
		credentialRef = ""
	}
	w.Job = domain.ReviewJob{TenantID: w.Task.TenantID, TenantSlug: slug, InstallationID: w.Task.InstallationID,
		InstallationExternalID: externalID, CredentialRef: credentialRef, Provider: w.Task.Provider, APIBaseURL: w.Task.APIBaseURL,
		Repository: w.Task.Repository, ReviewNumber: w.Task.OriginNumber, HeadSHA: c.HeadSHA, BaseRef: branch}
	return result, tx.Commit(ctx)
}

// This is a fresh read-only provider confirmation of signed checkpoint
// evidence, not a replayed adapter callback or a new execution attempt.
func (s *PostgresStore) FinishAgentPublicationRecovery(ctx context.Context, worker string, target AgentPublicationRecoveryTarget, event domain.InboundEvent, draftURL, reason string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var reads int
	err = tx.QueryRow(ctx, `SELECT read_attempt FROM agent_task_publication_recoveries WHERE attempt_id=$1
 AND state='queued' AND worker_id=$2 AND locked_until>now() FOR UPDATE`, target.Workflow.Attempt.ID, worker).Scan(&reads)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAgentTaskClaimLost
	}
	if err != nil {
		return err
	}
	task, err := scanAgentTask(tx.QueryRow(ctx, `SELECT `+agentTaskColumns+` FROM agent_tasks WHERE id=$1 FOR UPDATE`, target.Workflow.Task.ID))
	if err != nil {
		return err
	}
	attempt, err := scanAgentTaskAttempt(tx.QueryRow(ctx, `SELECT `+agentTaskAttemptColumns+` FROM agent_task_attempts WHERE id=$1 FOR UPDATE`, target.Workflow.Attempt.ID))
	if err != nil {
		return err
	}
	var eligible bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_task_attempts a JOIN agent_tasks t ON t.id=a.task_id
 JOIN agent_task_plans p ON p.id=a.plan_id JOIN provider_installations i ON i.id=t.installation_id
 WHERE a.id=$1 AND i.active AND i.verification_state='verified' AND `+recoverableAgentPublicationSQL+`)`, attempt.ID).Scan(&eligible)
	if err != nil {
		return err
	}
	if !eligible || attempt.TaskID != task.ID {
		reason = "publication_recovery_fenced"
	}
	c := target.Checkpoint
	var proof domain.AgentTaskPublicationCheckpoint
	err = tx.QueryRow(ctx, `SELECT head_sha,patch_sha256,branch_name,adapter_job_id,changed_file_count,diff_bytes,
 verification_profile_sha256,verification_output_sha256,verification_output_bytes,verification_criteria
 FROM agent_task_publication_checkpoints WHERE attempt_id=$1 AND attempt_number=$2`, attempt.ID, attempt.Attempt).Scan(
		&proof.HeadSHA, &proof.PatchSHA256, &proof.BranchName, &proof.AdapterJobID, &proof.ChangedFileCount, &proof.DiffBytes,
		&proof.VerificationProfileSHA256, &proof.VerificationOutputSHA256, &proof.VerificationOutputBytes, &proof.VerificationCriteria)
	if err != nil {
		return err
	}
	var criteria []string
	err = tx.QueryRow(ctx, `SELECT sections->'acceptance_criteria' FROM agent_task_plans WHERE id=$1`, attempt.PlanID).Scan(&criteria)
	if err != nil {
		return err
	}
	branch, err := s.agentFeedbackTargetBranch(ctx, task.ID)
	if err != nil {
		return err
	}
	canonical, urlErr := s.agentDraftURLPolicy.Canonical(task.Provider, task.APIBaseURL, task.Repository, task.OriginNumber, draftURL)
	if reason == "" && (urlErr != nil || !event.IsDraft || event.Provider != task.Provider || event.APIBaseURL != task.APIBaseURL ||
		event.Repository != task.Repository || event.ReviewNumber != task.OriginNumber || event.HeadRef != task.ExecutionBranch || event.BaseRef != branch ||
		event.HeadSHA != proof.HeadSHA || c.HeadSHA != proof.HeadSHA || proof.BranchName != task.ExecutionBranch || proof.AdapterJobID != attempt.AdapterJobID ||
		proof.VerificationProfileSHA256 == "" || proof.VerificationOutputSHA256 == "" || !domain.CriteriaVerified(criteria, proof.VerificationCriteria)) {
		reason = "publication_recovery_evidence_mismatch"
	}
	if reason != "" {
		state := "rejected"
		if (reason == "provider_unavailable" || reason == "provider_head_pending") && reads < 3 {
			state = "queued"
		}
		_, err = tx.Exec(ctx, `UPDATE agent_task_publication_recoveries SET state=$2,reason=$3,worker_id=NULL,locked_until=NULL,
 available_at=now()+interval '15 seconds',updated_at=now() WHERE attempt_id=$1`, attempt.ID, state, reason)
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	summary := "Recovered the existing owned Draft at the signed validated checkpoint by a fresh provider read; no coding or provider write was repeated. Original publication failure remains in recovery and audit records."
	attempt, err = scanAgentTaskAttempt(tx.QueryRow(ctx, `UPDATE agent_task_attempts SET state='succeeded',error_code='',error_message=$2,result_summary=$2,
 branch_name=$3,head_sha=$4,pull_request_url=$5,pull_request_number=$6,patch_sha256=$7,changed_file_count=$8,diff_bytes=$9,
 verification_profile_sha256=$10,verification_output_sha256=$11,verification_output_bytes=$12,updated_at=now()
 WHERE id=$1 RETURNING `+agentTaskAttemptColumns, attempt.ID, summary, proof.BranchName, proof.HeadSHA, canonical, task.OriginNumber,
		proof.PatchSHA256, proof.ChangedFileCount, proof.DiffBytes, proof.VerificationProfileSHA256, proof.VerificationOutputSHA256, proof.VerificationOutputBytes))
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE agent_tasks SET state='completed',revision=revision+1,updated_at=now() WHERE id=$1`, task.ID); err != nil {
		return err
	}
	if err = recordAgentDeliveryTx(ctx, tx, task, attempt); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE agent_task_publication_recoveries SET state='verified',reason='exact_owned_draft_confirmed',worker_id=NULL,locked_until=NULL,updated_at=now() WHERE attempt_id=$1`, attempt.ID); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata)
 VALUES($1,'agent:delivery-monitor','agent_task.publication_recovered',$2,jsonb_build_object('head_sha',$3::text,'provider_writes_replayed',false,'executions_replayed',false))`, task.TenantID, attempt.ID.String(), proof.HeadSHA)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
