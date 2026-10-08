package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const approvalPolicyColumns = `allow_agent_plan_self_approval,allow_rule_self_approval,approval_policy_revision,approval_policy_updated_by,approval_policy_updated_at`

func scanApprovalPolicy(row rowScanner) (domain.WorkspaceApprovalPolicy, error) {
	var policy domain.WorkspaceApprovalPolicy
	err := row.Scan(&policy.AllowAgentPlanSelfApproval, &policy.AllowRuleSelfApproval, &policy.Revision, &policy.UpdatedBy, &policy.UpdatedAt)
	return policy, err
}

func (s *PostgresStore) GetWorkspaceApprovalPolicy(ctx context.Context, actor, slug string) (domain.WorkspaceApprovalPolicy, error) {
	tenant, role, err := s.authorizedTenant(ctx, actor, slug)
	if err != nil {
		return domain.WorkspaceApprovalPolicy{}, err
	}
	policy, err := scanApprovalPolicy(s.pool.QueryRow(ctx, `SELECT `+approvalPolicyColumns+` FROM tenants WHERE id=$1`, tenant))
	policy.CanUpdate = role == "owner"
	return policy, err
}

// Approval decisions hold a shared lock until commit, serializing with policy
// changes so a revoked opt-in cannot authorize a later concurrent decision.
func workspaceApprovalPolicyTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (domain.WorkspaceApprovalPolicy, error) {
	return scanApprovalPolicy(tx.QueryRow(ctx, `SELECT `+approvalPolicyColumns+` FROM tenants WHERE id=$1 FOR SHARE`, tenant))
}

func (s *PostgresStore) SaveWorkspaceApprovalPolicy(ctx context.Context, actor, slug string, input domain.WorkspaceApprovalPolicyInput) (domain.WorkspaceApprovalPolicy, error) {
	if !input.Valid() {
		return domain.WorkspaceApprovalPolicy{}, ErrInvalidApprovalPolicy
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.WorkspaceApprovalPolicy{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenant, role, err := authorizedTenantTx(ctx, tx, actor, slug)
	if err != nil {
		return domain.WorkspaceApprovalPolicy{}, err
	}
	if role != "owner" {
		return domain.WorkspaceApprovalPolicy{}, ErrForbidden
	}
	policy, err := scanApprovalPolicy(tx.QueryRow(ctx, `UPDATE tenants SET
	 allow_agent_plan_self_approval=$2,allow_rule_self_approval=$3,
	 approval_policy_revision=approval_policy_revision+1,approval_policy_updated_by=$4,approval_policy_updated_at=now()
	 WHERE id=$1 AND approval_policy_revision=$5 RETURNING `+approvalPolicyColumns,
		tenant, *input.AllowAgentPlanSelfApproval, *input.AllowRuleSelfApproval, actor, input.ExpectedRevision))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.WorkspaceApprovalPolicy{}, ErrRevisionConflict
	}
	if err != nil {
		return domain.WorkspaceApprovalPolicy{}, fmt.Errorf("save approval policy: %w", err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata)
	 VALUES($1,$2,'workspace.approval_policy_updated',$3,jsonb_build_object('revision',$4::int,'previous_revision',$5::int,'allow_agent_plan_self_approval',$6::bool,'allow_rule_self_approval',$7::bool))`,
		tenant, actor, tenant.String(), policy.Revision, input.ExpectedRevision, policy.AllowAgentPlanSelfApproval, policy.AllowRuleSelfApproval); err != nil {
		return domain.WorkspaceApprovalPolicy{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.WorkspaceApprovalPolicy{}, err
	}
	policy.CanUpdate = true
	return policy, nil
}
