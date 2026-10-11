package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/RainLib/open-review-platform/internal/agentdecision"
	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"strings"
)

const campaignColumns = `id,tenant_id,input,request_sha256,state,revision,requested_by,created_at,updated_at`
const campaignTargetColumns = `id,campaign_id,installation_id,provider,api_base_url,repository,state,scan,task_id,error_code,error_message,scan_attempts,locked_until,policy`

func scanCampaign(row rowScanner) (c domain.AgentCampaign, err error) {
	err = row.Scan(&c.ID, &c.TenantID, &c.Input, &c.RequestSHA256, &c.State, &c.Revision, &c.RequestedBy, &c.CreatedAt, &c.UpdatedAt)
	return
}
func scanCampaignTarget(row rowScanner) (t domain.AgentCampaignTarget, err error) {
	err = row.Scan(&t.ID, &t.CampaignID, &t.InstallationID, &t.Provider, &t.APIBaseURL, &t.Repository, &t.State, &t.Scan, &t.TaskID, &t.ErrorCode, &t.ErrorMessage, &t.ScanAttempts, &t.LockedUntil, &t.PlanningTask)
	return
}
func campaignAdmin(role string) bool { return role == "owner" || role == "admin" }

// Listing is complete for the retained installation scope; an incomplete
// provider inventory cannot authorize an "all repositories" selection.
type CampaignRepositoryInventory struct {
	Repositories []domain.AgentCampaignRepository `json:"repositories"`
	Complete     bool                             `json:"complete"`
}

func (s *PostgresStore) AgentCampaignRepositories(ctx context.Context, actor, slug string, ids []uuid.UUID) (CampaignRepositoryInventory, error) {
	tenant, role, err := s.authorizedTenant(ctx, actor, slug)
	if err != nil {
		return CampaignRepositoryInventory{}, err
	}
	if !campaignAdmin(role) {
		return CampaignRepositoryInventory{}, ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CampaignRepositoryInventory{}, err
	}
	defer tx.Rollback(ctx)
	return campaignRepositoriesTx(ctx, tx, tenant, ids)
}
func campaignRepositoriesTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, ids []uuid.UUID) (CampaignRepositoryInventory, error) {
	out := CampaignRepositoryInventory{Repositories: []domain.AgentCampaignRepository{}, Complete: true}
	if len(ids) == 0 || len(ids) > 30 {
		return out, ErrInvalidAgentTask
	}
	seen := map[uuid.UUID]bool{}
	for _, id := range ids {
		if id == uuid.Nil || seen[id] {
			return out, ErrInvalidAgentTask
		}
		seen[id] = true
		var scope, state string
		var fresh bool
		err := tx.QueryRow(ctx, `SELECT p.repository_scope,COALESCE(h.receipt->>'inventory_state','unknown'),COALESCE(h.observed_at>now()-interval '15 minutes',false) FROM provider_installations p LEFT JOIN provider_health_probes h ON h.installation_id=p.id WHERE p.id=$1 AND p.tenant_id=$2 AND p.active AND p.verification_state='verified' FOR SHARE OF p`, id, tenant).Scan(&scope, &state, &fresh)
		if errors.Is(err, pgx.ErrNoRows) {
			return out, ErrNotFound
		}
		if err != nil {
			return out, err
		}
		if state != "synchronized" || !fresh {
			out.Complete = false
		}
		rows, err := tx.Query(ctx, `SELECT i.name FROM provider_repository_inventory i JOIN provider_health_probes h ON h.installation_id=i.installation_id WHERE i.installation_id=$1 AND NOT i.archived AND i.last_seen_at=h.observed_at ORDER BY i.name`, id)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var repo string
			if err = rows.Scan(&repo); err != nil {
				rows.Close()
				return out, err
			}
			if repositoryScopeAllows(scope, repo) {
				out.Repositories = append(out.Repositories, domain.AgentCampaignRepository{InstallationID: id, Repository: repo})
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
		if len(out.Repositories) > 1000 {
			return out, fmt.Errorf("campaign selection exceeds 1000 repositories: %w", ErrConflict)
		}
	}
	return out, nil
}
func (s *PostgresStore) CreateAgentCampaign(ctx context.Context, actor, slug string, input domain.AgentCampaignInput) (domain.AgentCampaign, error) {
	if !input.Valid() {
		return domain.AgentCampaign{}, ErrInvalidAgentTask
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.AgentCampaign{}, err
	}
	defer tx.Rollback(ctx)
	tenant, role, err := authorizedTenantTx(ctx, tx, actor, slug)
	if err != nil {
		return domain.AgentCampaign{}, err
	}
	if !campaignAdmin(role) {
		return domain.AgentCampaign{}, ErrForbidden
	}
	hash := domain.CampaignDigest(input)
	existing, err := scanCampaign(tx.QueryRow(ctx, `SELECT `+campaignColumns+` FROM agent_campaigns WHERE tenant_id=$1 AND idempotency_key=$2`, tenant, input.IdempotencyKey))
	if err == nil {
		if existing.RequestSHA256 != hash {
			return domain.AgentCampaign{}, ErrConflict
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.AgentCampaign{}, err
	}
	selected := input.Repositories
	if input.AllRepositories {
		inventory, e := campaignRepositoriesTx(ctx, tx, tenant, input.InstallationIDs)
		if e != nil {
			return domain.AgentCampaign{}, e
		}
		if !inventory.Complete {
			return domain.AgentCampaign{}, fmt.Errorf("refresh the complete provider inventory before selecting all: %w", ErrConflict)
		}
		selected = inventory.Repositories
	}
	if len(selected) == 0 {
		return domain.AgentCampaign{}, ErrInvalidAgentTask
	}
	id := uuid.New()
	c, err := scanCampaign(tx.QueryRow(ctx, `INSERT INTO agent_campaigns(id,tenant_id,idempotency_key,input,request_sha256,requested_by) VALUES($1,$2,$3,$4,$5,$6) RETURNING `+campaignColumns, id, tenant, input.IdempotencyKey, campaignJSON(input), hash, actor))
	if isUniqueViolation(err) {
		return domain.AgentCampaign{}, ErrConflict
	}
	if err != nil {
		return c, err
	}
	seen := map[string]bool{}
	for _, selectedRepo := range selected {
		repo := selectedRepo.Repository
		key := selectedRepo.InstallationID.String() + ":" + repo
		if seen[key] || repo != strings.Trim(repo, "/") || strings.TrimSpace(repo) != repo {
			return c, ErrInvalidAgentTask
		}
		seen[key] = true
		var provider domain.Provider
		var api, scope string
		if err = tx.QueryRow(ctx, `SELECT provider,api_base_url,repository_scope FROM provider_installations WHERE id=$1 AND tenant_id=$2 AND active AND verification_state='verified' FOR SHARE`, selectedRepo.InstallationID, tenant).Scan(&provider, &api, &scope); errors.Is(err, pgx.ErrNoRows) {
			return c, ErrNotFound
		}
		if err != nil {
			return c, err
		}
		if !repositoryScopeAllows(scope, repo) {
			return c, ErrForbidden
		}
		var present bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM provider_repository_inventory WHERE installation_id=$1 AND name=$2 AND NOT archived)`, selectedRepo.InstallationID, repo).Scan(&present); err != nil {
			return c, err
		}
		if !present {
			return c, ErrNotFound
		}
		tid := uuid.New()
		task := domain.AgentTask{ID: uuid.New(), TenantID: tenant, InstallationID: selectedRepo.InstallationID, Provider: provider, APIBaseURL: api, Repository: repo, OriginKind: "campaign", OriginRevision: "campaign:" + tid.String() + ":" + hash, Intent: "implement", RequestedBy: actor}
		task.ExecutionBranch = "agent/" + task.ID.String()
		if input.Mode != "scan" {
			var mode string
			if err = tx.QueryRow(ctx, `SELECT mode,revision,max_attempts,max_execution_seconds,max_feedback_cycles,executor_profile,decision_backend,workflow FROM agent_task_policies WHERE tenant_id=$1 AND provider=$2 AND api_base_url=$3 AND repository=$4 FOR SHARE`, tenant, provider, api, repo).Scan(&mode, &task.PolicyRevision, &task.MaxAttempts, &task.MaxExecutionSeconds, &task.MaxFeedbackCycles, &task.ExecutorProfile, &task.DecisionBackend, &task.Workflow); err != nil {
				return c, fmt.Errorf("configure repository Agent workflow: %w", ErrAgentTaskDisabled)
			}
			if mode != "manual" || !task.Workflow.Enabled || !task.Workflow.RequireCriterionEvidence || len(task.Workflow.RequiredChecks) == 0 {
				return c, fmt.Errorf("complete workflow, criterion evidence and independent checks are required: %w", ErrAgentTaskDisabled)
			}
		}
		if _, err = tx.Exec(ctx, `INSERT INTO agent_campaign_targets(id,campaign_id,installation_id,provider,api_base_url,repository,policy) VALUES($1,$2,$3,$4,$5,$6,$7)`, tid, id, selectedRepo.InstallationID, provider, api, repo, campaignJSON(task)); err != nil {
			return c, err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata) VALUES($1,$2,'agent_campaign.created',$3,jsonb_build_object('request_sha256',$4::text,'repositories',$5::int))`, tenant, actor, id.String(), hash, len(selected)); err != nil {
		return c, err
	}
	err = tx.Commit(ctx)
	return c, err
}
func (s *PostgresStore) ListAgentCampaigns(ctx context.Context, actor, slug string, cursor uuid.UUID) ([]domain.AgentCampaign, string, error) {
	tenant, _, err := s.authorizedTenant(ctx, actor, slug)
	if err != nil {
		return nil, "", err
	}
	if cursor != uuid.Nil {
		var exists bool
		if err = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_campaigns WHERE id=$1 AND tenant_id=$2)`, cursor, tenant).Scan(&exists); err != nil {
			return nil, "", err
		}
		if !exists {
			return nil, "", ErrNotFound
		}
	}
	rows, err := s.pool.Query(ctx, `SELECT `+campaignColumns+` FROM agent_campaigns WHERE tenant_id=$1 AND ($2::uuid='00000000-0000-0000-0000-000000000000' OR (created_at,id)<(SELECT created_at,id FROM agent_campaigns WHERE id=$2)) ORDER BY created_at DESC,id DESC LIMIT 51`, tenant, cursor)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []domain.AgentCampaign{}
	for rows.Next() {
		c, e := scanCampaign(rows)
		if e != nil {
			return nil, "", e
		}
		out = append(out, c)
	}
	next := ""
	if len(out) > 50 {
		out = out[:50]
		next = out[49].ID.String()
	}
	return out, next, rows.Err()
}
func (s *PostgresStore) GetAgentCampaign(ctx context.Context, actor, slug string, id uuid.UUID) (domain.AgentCampaignDetail, error) {
	tenant, _, err := s.authorizedTenant(ctx, actor, slug)
	if err != nil {
		return domain.AgentCampaignDetail{}, err
	}
	out := domain.AgentCampaignDetail{}
	out.Campaign, err = scanCampaign(s.pool.QueryRow(ctx, `SELECT `+campaignColumns+` FROM agent_campaigns WHERE id=$1 AND tenant_id=$2`, id, tenant))
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	rows, err := s.pool.Query(ctx, `SELECT `+campaignTargetColumns+` FROM agent_campaign_targets WHERE campaign_id=$1 ORDER BY repository,id`, id)
	if err != nil {
		return out, err
	}
	out.Targets = []domain.AgentCampaignTarget{}
	for rows.Next() {
		t, e := scanCampaignTarget(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		out.Targets = append(out.Targets, t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	for j := range out.Targets {
		t := &out.Targets[j]
		if t.TaskID != nil { // Follow the newest live branch descendant, including failed repairs.
			var current uuid.UUID
			err = s.pool.QueryRow(ctx, `SELECT id FROM agent_tasks WHERE tenant_id=$1 AND execution_branch=(SELECT execution_branch FROM agent_tasks WHERE id=$2) AND state NOT IN ('superseded') ORDER BY created_at DESC,id DESC LIMIT 1`, tenant, *t.TaskID).Scan(&current)
			if err != nil {
				return out, err
			}
			d, e := s.GetAgentTask(ctx, actor, slug, current)
			if e != nil {
				return out, e
			}
			t.Detail = &d
		}
	}
	out.Summarize()
	return out, nil
}
func (s *PostgresStore) ClaimAgentCampaignScan(ctx context.Context, worker string) (domain.AgentCampaign, domain.AgentCampaignTarget, error) {
	var c domain.AgentCampaign
	var t domain.AgentCampaignTarget
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return c, t, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE agent_campaign_targets SET state='needs_attention',error_code='scan_retries_exhausted',locked_until=NULL WHERE scan_attempts>=3 AND state='scanning' AND locked_until<now()`); err != nil {
		return c, t, err
	}
	var id uuid.UUID
	err = tx.QueryRow(ctx, `SELECT t.id FROM agent_campaign_targets t JOIN agent_campaigns c ON c.id=t.campaign_id WHERE c.state='active' AND t.scan_attempts<3 AND (t.state='scan_queued' OR (t.state='scanning' AND t.locked_until<now())) ORDER BY t.created_at,t.id FOR UPDATE OF t SKIP LOCKED LIMIT 1`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = tx.Commit(ctx)
		return c, t, ErrNoQueuedAgentTask
	}
	if err != nil {
		return c, t, err
	}
	t, err = scanCampaignTarget(tx.QueryRow(ctx, `UPDATE agent_campaign_targets SET state='scanning',scan_attempts=scan_attempts+1,worker_id=$2,locked_until=now()+interval '10 minutes',updated_at=now() WHERE id=$1 RETURNING `+campaignTargetColumns, id, worker))
	if err != nil {
		return c, t, err
	}
	c, err = scanCampaign(tx.QueryRow(ctx, `SELECT `+campaignColumns+` FROM agent_campaigns WHERE id=$1`, t.CampaignID))
	if err != nil {
		return c, t, err
	}
	if err = tx.QueryRow(ctx, `SELECT external_id,credential_ref FROM provider_installations WHERE id=$1 AND active AND verification_state='verified'`, t.InstallationID).Scan(&t.InstallationExternalID, &t.CredentialRef); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return c, t, err
	}
	t.WorkerID = worker
	err = tx.Commit(ctx)
	return c, t, err
}
func CampaignBinding(c domain.AgentCampaign, t domain.AgentCampaignTarget) domain.AgentCampaignBinding {
	return domain.AgentCampaignBinding{CampaignID: c.ID, TargetID: t.ID, RequestSHA256: c.RequestSHA256, Mode: c.Input.Mode, Paths: c.Input.Paths, Search: c.Input.Search, Replacement: c.Input.Replacement, Files: t.Scan.Files, Requirements: c.Input.Requirements, Criteria: c.Input.AcceptanceCriteria}
}

func (s *PostgresStore) FinishAgentCampaignScan(ctx context.Context, worker string, target domain.AgentCampaignTarget, receipt domain.AgentCampaignScan, snapshot domain.AgentTaskSourceSnapshot, code string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	t, err := scanCampaignTarget(tx.QueryRow(ctx, `SELECT `+campaignTargetColumns+` FROM agent_campaign_targets WHERE id=$1 FOR UPDATE`, target.ID))
	if err != nil {
		return err
	}
	var live bool
	if err = tx.QueryRow(ctx, `SELECT state='scanning' AND worker_id=$2 AND locked_until>now() FROM agent_campaign_targets WHERE id=$1`, t.ID, worker).Scan(&live); err != nil {
		return err
	}
	if !live || t.ScanAttempts != target.ScanAttempts {
		return ErrAgentTaskClaimLost
	}
	c, err := scanCampaign(tx.QueryRow(ctx, `SELECT `+campaignColumns+` FROM agent_campaigns WHERE id=$1 FOR SHARE`, t.CampaignID))
	if err != nil {
		return err
	}
	if c.State == "cancelled" {
		return ErrAgentTaskClaimLost
	}
	state := "scan_complete"
	if code != "" || !receipt.Complete {
		state = "needs_attention"
		if code == "" {
			code = receipt.ErrorCode
		}
		if code == "" {
			code = "scan_incomplete"
		}
	} else if c.Input.Mode != "scan" {
		if c.Input.ExpectedMatches != nil && receipt.Matches != *c.Input.ExpectedMatches {
			state = "needs_attention"
			code = "match_count_changed"
		} else if receipt.Matches == 0 && (c.Input.Mode == "replace" || c.Input.Search != "") {
			state = "no_match"
		} else if len(receipt.Files) == 0 {
			state = "no_match"
		} else {
			t.Scan = receipt
			b := CampaignBinding(c, t)
			if !b.Valid() || snapshot.Campaign == nil || domain.CampaignDigest(b) != domain.CampaignDigest(*snapshot.Campaign) || snapshot.BaseSHA != receipt.BaseSHA || snapshot.BaseRef != receipt.BaseRef || snapshot.GeneratedPlan == nil {
				return ErrInvalidAgentTaskPlan
			}
			task := t.PlanningTask
			task.SourceState = "ready"
			task.SourceBaseRef, task.SourceBaseSHA = receipt.BaseRef, receipt.BaseSHA
			classification := agentdecision.Classify(task, "Complete workspace campaign", b.Requirements+"\nAcceptance criteria:\n"+strings.Join(b.Criteria, "\n"), nil)
			classification, err = agentdecision.ApplySignal(classification, snapshot.DecisionSignal, task.DecisionBackend)
			if err != nil {
				return err
			}
			task, err = scanAgentTask(tx.QueryRow(ctx, `INSERT INTO agent_tasks(id,tenant_id,installation_id,provider,api_base_url,repository,origin_kind,origin_number,origin_revision,intent,policy_revision,max_attempts,max_execution_seconds,max_feedback_cycles,executor_profile,decision_backend,execution_branch,requested_by,source_state,source_base_ref,source_base_sha,source_captured_at) VALUES($1,$2,$3,$4,$5,$6,'campaign',0,$7,'implement',$8,$9,$10,$11,$12,$13,$14,$15,'ready',$16,$17,now()) RETURNING `+agentTaskColumns, task.ID, task.TenantID, task.InstallationID, task.Provider, task.APIBaseURL, task.Repository, task.OriginRevision, task.PolicyRevision, task.MaxAttempts, task.MaxExecutionSeconds, task.MaxFeedbackCycles, task.ExecutorProfile, task.DecisionBackend, task.ExecutionBranch, task.RequestedBy, receipt.BaseRef, receipt.BaseSHA))
			if err != nil {
				return err
			}
			classification.TaskID = task.ID
			classification.TaskRevision = task.Revision
			if _, err = recordAgentTaskClassification(ctx, tx, task, classification); err != nil {
				return err
			}
			if classification.Decision != "requires_human" {
				state = "needs_attention"
				code = "campaign_request_" + classification.Decision
			} else {
				p := *snapshot.GeneratedPlan
				p.SourceRequirements = strings.TrimSpace(c.Input.Requirements)
				p.RepositoryEvidence = snapshot.RepositoryEvidence
				p.AcceptanceCriteria = append([]string(nil), c.Input.AcceptanceCriteria...)
				if err = recordGeneratedAgentPlanTx(ctx, tx, &task, p); err != nil {
					return err
				}
				state = "plan_ready"
			}
			t.TaskID = &task.ID
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE agent_campaign_targets SET state=$2,scan=$3,task_id=$4,error_code=$5,error_message=$6,worker_id='',locked_until=NULL,updated_at=now() WHERE id=$1`, t.ID, state, campaignJSON(receipt), t.TaskID, code, func() string {
		if code != "" {
			return "Scan or planning could not close; inspect evidence and retry within the retained budget."
		}
		return ""
	}()); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata) VALUES($1,$2,'agent_campaign.scanned',$3,jsonb_build_object('base_sha',$4::text,'complete',$5::bool,'matches',$6::int,'state',$7::text))`, c.TenantID, "worker:"+worker, t.ID.String(), receipt.BaseSHA, receipt.Complete, receipt.Matches, state)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) ApproveAgentCampaign(ctx context.Context, actor, slug string, id uuid.UUID, input domain.AgentCampaignApproval) error {
	if input.Revision < 1 || len(input.Plans) == 0 || len(input.Plans) > 1000 {
		return ErrInvalidAgentTaskPlan
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tenant, role, err := authorizedTenantTx(ctx, tx, actor, slug)
	if err != nil {
		return err
	}
	if !campaignAdmin(role) {
		return ErrForbidden
	}
	c, err := scanCampaign(tx.QueryRow(ctx, `SELECT `+campaignColumns+` FROM agent_campaigns WHERE id=$1 AND tenant_id=$2 FOR UPDATE`, id, tenant))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if c.State != "active" || c.Revision != input.Revision {
		return ErrConflict
	}
	var pending int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM agent_campaign_targets WHERE campaign_id=$1 AND state IN ('scan_queued','scanning')`, id).Scan(&pending); err != nil {
		return err
	}
	if pending > 0 {
		return fmt.Errorf("finish every repository scan before approval: %w", ErrConflict)
	}
	seen := map[uuid.UUID]bool{}
	for _, p := range input.Plans {
		if seen[p.TaskID] {
			return ErrInvalidAgentTaskPlan
		}
		seen[p.TaskID] = true
		var belongs bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_campaign_targets ct JOIN agent_tasks root ON root.id=ct.task_id JOIN agent_tasks t ON t.execution_branch=root.execution_branch AND t.tenant_id=root.tenant_id WHERE ct.campaign_id=$1 AND t.id=$2)`, id, p.TaskID).Scan(&belongs); err != nil {
			return err
		}
		if !belongs {
			return ErrNotFound
		}
		task, e := scanAgentTask(tx.QueryRow(ctx, `SELECT `+agentTaskColumns+` FROM agent_tasks WHERE id=$1 AND tenant_id=$2 FOR UPDATE`, p.TaskID, tenant))
		if e != nil {
			return e
		}
		var hash string
		if err = tx.QueryRow(ctx, `SELECT plan_sha256 FROM agent_task_plans WHERE id=$1 AND task_id=$2 AND revision=$3`, p.PlanID, p.TaskID, p.Revision).Scan(&hash); err != nil {
			return err
		}
		if hash != p.SHA256 {
			return ErrConflict
		}
		if _, err = approveAgentTaskPlanTx(ctx, tx, tenant, actor, role, task, p.PlanID, domain.AgentTaskPlanApprovalInput{Revision: p.Revision}); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE agent_campaigns SET revision=revision+1,updated_at=now() WHERE id=$1`, id); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata) VALUES($1,$2,'agent_campaign.plans_approved',$3,$4)`, tenant, actor, id.String(), campaignJSON(input))
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *PostgresStore) ActAgentCampaign(ctx context.Context, actor, slug string, id uuid.UUID, input domain.AgentCampaignAction) error {
	if input.Revision < 1 || len(strings.TrimSpace(input.Reason)) < 3 || len(input.Reason) > 1000 {
		return ErrInvalidAgentTask
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tenant, role, err := authorizedTenantTx(ctx, tx, actor, slug)
	if err != nil {
		return err
	}
	if !campaignAdmin(role) {
		return ErrForbidden
	}
	c, err := scanCampaign(tx.QueryRow(ctx, `SELECT `+campaignColumns+` FROM agent_campaigns WHERE id=$1 AND tenant_id=$2 FOR UPDATE`, id, tenant))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if c.Revision != input.Revision || c.State == "cancelled" {
		return ErrConflict
	}
	state := c.State
	switch input.Action {
	case "pause":
		state = "paused"
	case "resume":
		state = "active"
	case "retry_failed":
		// Existing task retries require a fresh plan approval; attempts and branch
		// budgets are retained. Only scan failures without a task may be reread.
		if _, err = tx.Exec(ctx, `UPDATE agent_campaign_targets SET state='scan_queued',error_code='',error_message='' WHERE campaign_id=$1 AND state='needs_attention' AND task_id IS NULL AND scan_attempts<3`, id); err != nil {
			return err
		}
		if err = prepareCampaignRetryPlansTx(ctx, tx, id, actor); err != nil {
			return err
		}
	case "cancel":
		state = "cancelled"
		rows, e := tx.Query(ctx, `SELECT `+qualifiedAgentTaskColumns("t")+` FROM agent_tasks t JOIN agent_campaign_targets ct ON ct.task_id IN (SELECT r.id FROM agent_tasks r WHERE r.execution_branch=t.execution_branch AND r.tenant_id=t.tenant_id) WHERE ct.campaign_id=$1 AND t.state IN ('received','awaiting_approval','execution_queued','executing','needs_attention') ORDER BY t.id FOR UPDATE OF t`, id)
		if e != nil {
			return e
		}
		tasks := []domain.AgentTask{}
		for rows.Next() {
			task, e := scanAgentTask(rows)
			if e != nil {
				rows.Close()
				return e
			}
			tasks = append(tasks, task)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, task := range tasks {
			if _, err = cancelAgentTaskTx(ctx, tx, task, actor, input.Reason); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE agent_campaign_targets SET state='cancelled',worker_id='',locked_until=NULL WHERE campaign_id=$1 AND state IN ('scan_queued','scanning','plan_ready','needs_attention')`, id); err != nil {
			return err
		}
	default:
		return ErrInvalidAgentTask
	}
	if _, err = tx.Exec(ctx, `UPDATE agent_campaigns SET state=$2,revision=revision+1,updated_at=now() WHERE id=$1`, id, state); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata) VALUES($1,$2,'agent_campaign.action',$3,$4)`, tenant, actor, id.String(), campaignJSON(input))
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func qualifiedAgentTaskColumns(a string) string {
	return a + "." + strings.ReplaceAll(agentTaskColumns, ",", ","+a+".")
}

// Broker delivery can be acknowledged when a batch is paused or full. This
// durable scheduler republishes still-approved queued tuples on resume and
// capacity recovery. Waiting never creates or resets an execution attempt.
func (s *PostgresStore) ScheduleAgentCampaignExecutions(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `WITH due AS (UPDATE agent_tasks t SET execution_dispatch_after=now()+interval '15 seconds' WHERE t.state='execution_queued' AND t.execution_dispatch_after<=now() AND NOT EXISTS(SELECT 1 FROM agent_campaign_targets ct JOIN agent_tasks root ON root.id=ct.task_id JOIN agent_campaigns c ON c.id=ct.campaign_id WHERE root.tenant_id=t.tenant_id AND root.execution_branch=t.execution_branch AND c.state<>'active') RETURNING t.id,t.revision), queued AS (SELECT t.id,t.revision,p.id AS plan_id,p.revision AS plan_revision,p.plan_sha256 FROM due t JOIN agent_task_plans p ON p.task_id=t.id AND p.state='approved' AND p.revision=(SELECT max(revision) FROM agent_task_plans WHERE task_id=t.id)) INSERT INTO outbox_messages(aggregate_type,aggregate_id,topic,dedupe_key,payload) SELECT 'agent_task',id,'agent.task.execute.requested','campaign-dispatch:'||id||':'||revision||':'||floor(extract(epoch FROM now())/15),jsonb_build_object('task_id',id::text,'task_revision',revision,'plan_id',plan_id::text,'plan_revision',plan_revision,'plan_sha256',plan_sha256) FROM queued ON CONFLICT(dedupe_key) DO NOTHING`)

	return err
}
func campaignExecutionAllowedTx(ctx context.Context, tx pgx.Tx, task domain.AgentTask) (bool, error) {
	var campaign uuid.UUID
	var state string
	var concurrency int
	if _, e := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "campaign-repo:"+task.TenantID.String()+":"+string(task.Provider)+":"+task.APIBaseURL+":"+task.Repository); e != nil {
		return false, e
	}
	err := tx.QueryRow(ctx, `SELECT c.id,c.state,(c.input->>'concurrency')::int FROM agent_campaign_targets ct JOIN agent_campaigns c ON c.id=ct.campaign_id JOIN agent_tasks root ON root.id=ct.task_id WHERE root.tenant_id=$1 AND root.execution_branch=$2`, task.TenantID, task.ExecutionBranch).Scan(&campaign, &state, &concurrency)
	if errors.Is(err, pgx.ErrNoRows) {
		var busy bool
		e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_task_attempts a JOIN agent_tasks t ON t.id=a.task_id JOIN agent_tasks root ON root.tenant_id=t.tenant_id AND root.execution_branch=t.execution_branch JOIN agent_campaign_targets ct ON ct.task_id=root.id WHERE t.tenant_id=$1 AND t.provider=$2 AND t.api_base_url=$3 AND t.repository=$4 AND t.id<>$5 AND a.state='running')`, task.TenantID, task.Provider, task.APIBaseURL, task.Repository, task.ID).Scan(&busy)
		return !busy, e
	}
	if err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "campaign:"+campaign.String()); err != nil {
		return false, err
	}
	var running int
	err = tx.QueryRow(ctx, `SELECT count(*) FROM agent_task_attempts a JOIN agent_tasks t ON t.id=a.task_id JOIN agent_tasks root ON root.tenant_id=t.tenant_id AND root.execution_branch=t.execution_branch JOIN agent_campaign_targets ct ON ct.task_id=root.id WHERE ct.campaign_id=$1 AND a.state='running' AND t.id<>$2`, campaign, task.ID).Scan(&running)
	if err != nil {
		return false, err
	}
	var busy bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_task_attempts a JOIN agent_tasks t ON t.id=a.task_id WHERE t.tenant_id=$1 AND t.provider=$2 AND t.api_base_url=$3 AND t.repository=$4 AND t.id<>$5 AND a.state='running')`, task.TenantID, task.Provider, task.APIBaseURL, task.Repository, task.ID).Scan(&busy)
	return state == "active" && running < concurrency && !busy, err
}
func (s *PostgresStore) loadCampaignBinding(ctx context.Context, task domain.AgentTask) (*domain.AgentCampaignBinding, error) {
	var input domain.AgentCampaignInput
	var target domain.AgentCampaignTarget
	var c domain.AgentCampaign
	err := s.pool.QueryRow(ctx, `SELECT c.id,c.request_sha256,c.input,ct.id,ct.scan FROM agent_campaign_targets ct JOIN agent_campaigns c ON c.id=ct.campaign_id JOIN agent_tasks root ON root.id=ct.task_id WHERE root.tenant_id=$1 AND root.execution_branch=$2`, task.TenantID, task.ExecutionBranch).Scan(&c.ID, &c.RequestSHA256, &input, &target.ID, &target.Scan)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.Input = input
	b := CampaignBinding(c, target)
	return &b, nil
}

func campaignJSON(v any) string { raw, _ := json.Marshal(v); return string(raw) }

func prepareCampaignRetryPlansTx(ctx context.Context, tx pgx.Tx, campaign uuid.UUID, actor string) error {
	rows, err := tx.Query(ctx, `SELECT `+qualifiedAgentTaskColumns("t")+` FROM agent_tasks t JOIN agent_tasks root ON root.tenant_id=t.tenant_id AND root.execution_branch=t.execution_branch JOIN agent_campaign_targets ct ON ct.task_id=root.id WHERE ct.campaign_id=$1 AND t.state IN ('needs_attention','failed') AND t.source_state='ready' AND t.id=(SELECT id FROM agent_tasks newest WHERE newest.tenant_id=t.tenant_id AND newest.execution_branch=t.execution_branch ORDER BY created_at DESC,id DESC LIMIT 1) ORDER BY t.id FOR UPDATE OF t`, campaign)
	if err != nil {
		return err
	}
	tasks := []domain.AgentTask{}
	for rows.Next() {
		task, e := scanAgentTask(rows)
		if e != nil {
			rows.Close()
			return e
		}
		tasks = append(tasks, task)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, task := range tasks {
		classification, err := latestAgentTaskClassification(ctx, tx, task.ID)
		if err != nil {
			return err
		}
		if classification.Decision != "requires_human" {
			continue
		}
		if err = checkAgentWorkflowBudgetTx(ctx, tx, task); errors.Is(err, ErrInvalidAgentTaskPlan) {
			continue
		} else if err != nil {
			return err
		}
		p, err := scanAgentTaskPlan(tx.QueryRow(ctx, `SELECT `+agentTaskPlanColumns+` FROM agent_task_plans WHERE task_id=$1 ORDER BY revision DESC LIMIT 1`, task.ID))
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if err = requireAgentWorkflowCriteriaTx(ctx, tx, task, p.Sections); err != nil {
			return err
		}
		next := p.Revision + 1
		var planID uuid.UUID
		err = tx.QueryRow(ctx, `INSERT INTO agent_task_plans(task_id,revision,summary,sections,plan_sha256,created_by) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, task.ID, next, p.Summary, campaignJSON(p.Sections), p.PlanSHA256, actor).Scan(&planID)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE agent_task_plans SET state='superseded' WHERE task_id=$1 AND id<>$2 AND state='awaiting_approval'`, task.ID, planID); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE agent_tasks SET state='awaiting_approval',revision=revision+1,updated_at=now() WHERE id=$1`, task.ID); err != nil {
			return err
		}
		task.State = "awaiting_approval"
		task.Revision++
		if err = queueAgentTaskSourceProviderComment(ctx, tx, task, "plan", "", ""); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO audit_events(tenant_id,actor_subject,action,target,metadata) VALUES($1,$2,'agent_campaign.retry_plan_prepared',$3,jsonb_build_object('task_id',$4::text,'plan_revision',$5::int,'requires_new_approval',true))`, task.TenantID, actor, campaign.String(), task.ID.String(), next); err != nil {
			return err
		}
	}
	return nil
}
