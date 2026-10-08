package store

import (
	"context"
	"errors"
	"strings"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/jackc/pgx/v5"
)

// HoldAgentTaskExecutionForReadiness fences an exact queued approval without
// creating an attempt or consuming its frozen execution budget. Old deliveries
// cannot change executing/cancelled tasks or overwrite a replacement plan.
func (s *PostgresStore) HoldAgentTaskExecutionForReadiness(ctx context.Context, workerID string, request domain.AgentTaskExecutionRequest) error {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" || !request.Valid() {
		return ErrInvalidAgentTask
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	task, err := scanAgentTask(tx.QueryRow(ctx, `SELECT `+agentTaskColumns+` FROM agent_tasks WHERE id=$1 FOR UPDATE`, request.TaskID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNoQueuedAgentTask
	}
	if err != nil {
		return err
	}
	if task.State != "execution_queued" || task.Revision != request.TaskRevision {
		return ErrNoQueuedAgentTask
	}
	var eligible bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_task_plans p WHERE p.id=$1 AND p.task_id=$2 AND p.revision=$3 AND p.plan_sha256=$4 AND p.state='approved') AND NOT EXISTS(SELECT 1 FROM agent_task_attempts a WHERE a.task_id=$2 AND a.task_revision=$5 AND a.plan_id=$1 AND a.plan_revision=$3)`, request.PlanID, request.TaskID, request.PlanRevision, request.PlanSHA256, request.TaskRevision).Scan(&eligible)
	if err != nil {
		return err
	}
	if !eligible {
		return ErrNoQueuedAgentTask
	}
	const code = "agent_execution_preflight_blocked"
	const message = "The isolated adapter or its coding model is unavailable. No execution attempt was leased and no task budget was consumed. Restore adapter/model readiness, inspect worker health, then create and approve a new plan."
	if _, err = tx.Exec(ctx, `UPDATE agent_tasks SET state='needs_attention',revision=revision+1,updated_at=now() WHERE id=$1`, task.ID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata) VALUES($1,$2,'agent_task.execution_preflight_blocked',$3,jsonb_build_object('code',$4::text,'message',$5::text,'task_revision',$6::int,'plan_revision',$7::int,'plan_sha256',$8::text))`, task.TenantID, "worker:"+workerID, task.ID.String(), code, message, task.Revision+1, request.PlanRevision, request.PlanSHA256); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
