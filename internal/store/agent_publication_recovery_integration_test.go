package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

func TestPublicationReadRecoveryPreservesFailureBudgetAndFences(t *testing.T) {
	if os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL") == "" || os.Getenv("OPEN_REVIEW_TEST_ISOLATED_DATABASE") != "true" {
		t.Skip("requires isolated PostgreSQL")
	}
	ctx := context.Background()
	s, err := Open(ctx, isolatedQueueDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	for _, scenario := range []string{"confirmed", "head changed", "foreign URL", "criterion failed", "new plan", "new descendant", "revoked approval", "revoked installation", "read failure limit", "crashed last read"} {
		t.Run(scenario, func(t *testing.T) {
			tenant, install, root, child, planID, attemptID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
			slug := "publication-" + tenant.String()[:8]
			branch := "agent/" + root.String()
			sha, previous := strings.Repeat("b", 40), strings.Repeat("a", 40)
			exec := func(sql string, args ...any) {
				t.Helper()
				if _, err := s.pool.Exec(ctx, sql, args...); err != nil {
					t.Fatal(err)
				}
			}
			exec(`INSERT INTO tenants(id,slug,name) VALUES($1,$2,'Read recovery fixture')`, tenant, slug)
			exec(`INSERT INTO memberships(tenant_id,subject,role,active) VALUES($1,'owner','owner',true),($1,'reviewer','reviewer',true)`, tenant)
			exec(`INSERT INTO provider_installations(id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref,verification_state) VALUES($1,$2,'github',$3,'team/repo','https://api.github.com','fixture','verified')`, install, tenant, install.String())
			workflow := domain.AgentWorkflowPolicy{Enabled: true, RequireCriterionEvidence: true, MaxRepairCycles: 2, MaxTaskAttempts: 3}
			if _, err := s.SaveAgentTaskPolicy(ctx, "owner", slug, domain.AgentTaskPolicyInput{Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com", Repository: "team/repo", Mode: "manual", DecisionBackend: "deterministic", Workflow: &workflow}); err != nil {
				t.Fatal(err)
			}
			for _, task := range []struct {
				id               uuid.UUID
				kind, state, ref string
				number           int
				parent           any
			}{{root, "issue", "completed", "main", 18, nil}, {child, "pull_request", "needs_attention", branch, 19, root}} {
				exec(`INSERT INTO agent_tasks(id,tenant_id,installation_id,provider,api_base_url,repository,origin_kind,origin_number,origin_revision,intent,state,requested_by,execution_branch,parent_task_id,source_state,source_base_ref,source_base_sha,source_captured_at)
 VALUES($1,$2,$3,'github','https://api.github.com','team/repo',$4,$5,$6,'implement',$7,'reviewer',$8,$9,'ready',$10,$6,now())`, task.id, tenant, install, task.kind, task.number, previous, task.state, branch, task.parent, task.ref)
			}
			sections := domain.AgentTaskPlanSections{Objective: "Keep complete passing evidence", Scope: "Only the approved pure function", Verification: "Run the independent fixed verifier", Risks: "Do not widen permissions", Unknowns: "Report unresolved checks", AcceptanceCriteria: []string{"Keep complete passing evidence"}}
			raw, _ := json.Marshal(sections)
			summary := sections.Summary()
			digest := sha256.Sum256([]byte(summary))
			exec(`INSERT INTO agent_task_plans(id,task_id,revision,state,summary,plan_sha256,sections,created_by,approved_by,approved_at) VALUES($1,$2,1,'approved',$3,$4,$5,'reviewer','owner',now())`, planID, child, summary, hex.EncodeToString(digest[:]), raw)
			jobID := "fixture-job-" + attemptID.String()
			exec(`INSERT INTO agent_task_attempts(id,task_id,plan_id,task_revision,plan_revision,attempt,state,adapter_job_id,adapter_started_at,deadline_at,error_code,error_message,started_at,finished_at)
 VALUES($1,$2,$3,1,1,1,'needs_attention',$4,now(),now()+interval '1 hour','agent_adapter_execution_failed','The adapter stopped at stage draft_publication without a completed delivery receipt.',now(),now())`, attemptID, child, planID, jobID)
			results, _ := json.Marshal([]domain.AgentCriterionResult{{Criterion: sections.AcceptanceCriteria[0], Status: "passed", Evidence: "Independent fixed verifier passed"}})
			exec(`INSERT INTO agent_task_publication_checkpoints(attempt_id,attempt_number,adapter_job_id,branch_name,head_sha,patch_sha256,changed_file_count,diff_bytes,verification_profile_sha256,verification_output_sha256,verification_output_bytes,verification_criteria)
 VALUES($1,1,$8,$2,$3,$4,1,100,$5,$6,100,$7)`, attemptID, branch, sha, strings.Repeat("d", 64), strings.Repeat("e", 64), strings.Repeat("f", 64), results, jobID)
			t.Cleanup(func() {
				_, _ = s.pool.Exec(ctx, `UPDATE agent_task_publication_recoveries SET state='rejected',locked_until=NULL WHERE attempt_id=$1`, attemptID)
				_, _ = s.pool.Exec(ctx, `UPDATE agent_task_acceptances SET state='superseded' WHERE task_id=$1`, child)
			})
			target, err := s.ClaimAgentPublicationRecovery(ctx, "reader")
			if err != nil || target.Workflow.Task.ID != child {
				t.Fatalf("claim: %+v %v", target, err)
			}
			if _, err = s.ClaimAgentPublicationRecovery(ctx, "competitor"); !errors.Is(err, ErrNoQueuedAgentTask) {
				t.Fatalf("duplicate recovery lease: %v", err)
			}
			event := domain.InboundEvent{Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com", Repository: "team/repo", ReviewNumber: 19, HeadRef: branch, HeadSHA: sha, BaseRef: "main", IsDraft: true}
			url := "https://github.com/team/repo/pull/19"
			if err = s.FinishAgentPublicationRecovery(ctx, "wrong-reader", *target, event, url, ""); !errors.Is(err, ErrAgentTaskClaimLost) {
				t.Fatalf("foreign recovery: %v", err)
			}
			switch scenario {
			case "head changed":
				event.HeadSHA = strings.Repeat("9", 40)
			case "foreign URL":
				url = "https://elsewhere.invalid/team/repo/pull/19"
			case "criterion failed":
				exec(`UPDATE agent_task_publication_checkpoints SET verification_criteria='[{"criterion":"Keep complete passing evidence","status":"failed","evidence":"Independent check failed"}]' WHERE attempt_id=$1`, attemptID)
			case "new descendant":
				exec(`INSERT INTO agent_tasks(tenant_id,installation_id,provider,api_base_url,repository,origin_kind,origin_number,origin_revision,intent,state,requested_by,execution_branch,parent_task_id) VALUES($1,$2,'github','https://api.github.com','team/repo','pull_request',19,$3,'implement','awaiting_approval','owner',$4,$5)`, tenant, install, strings.Repeat("7", 40), branch, child)
			case "new plan":
				exec(`INSERT INTO agent_task_plans(task_id,revision,summary,plan_sha256,created_by) VALUES($1,2,'New requirement',$2,'owner')`, child, strings.Repeat("8", 64))
			case "revoked approval":
				exec(`UPDATE memberships SET active=false,deactivated_at=now(),deactivated_by='fixture-admin' WHERE tenant_id=$1 AND subject='owner'`, tenant)
			case "revoked installation":
				exec(`UPDATE provider_installations SET active=false WHERE id=$1`, install)
			case "crashed last read":
				exec(`UPDATE agent_task_publication_recoveries SET read_attempt=3,locked_until=now()-interval '1 second' WHERE attempt_id=$1`, attemptID)
			}
			if scenario == "crashed last read" {
				if _, err = s.ClaimAgentPublicationRecovery(ctx, "restart"); !errors.Is(err, ErrNoQueuedAgentTask) {
					t.Fatal(err)
				}
			} else if scenario == "read failure limit" {
				for n := 0; n < 3; n++ {
					if err = s.FinishAgentPublicationRecovery(ctx, "reader", *target, event, "", "provider_unavailable"); err != nil {
						t.Fatal(err)
					}
					if n < 2 {
						exec(`UPDATE agent_task_publication_recoveries SET available_at=now() WHERE attempt_id=$1`, attemptID)
						target, err = s.ClaimAgentPublicationRecovery(ctx, "reader")
						if err != nil {
							t.Fatal(err)
						}
					}
				}
			} else if err = s.FinishAgentPublicationRecovery(ctx, "reader", *target, event, url, ""); err != nil {
				t.Fatal(err)
			}
			var state, attemptState, taskState, original string
			var used, acceptances, audits int
			if err = s.pool.QueryRow(ctx, `SELECT r.state,a.state,t.state,r.original_error_code,a.attempt FROM agent_task_publication_recoveries r JOIN agent_task_attempts a ON a.id=r.attempt_id JOIN agent_tasks t ON t.id=a.task_id WHERE a.id=$1`, attemptID).Scan(&state, &attemptState, &taskState, &original, &used); err != nil {
				t.Fatal(err)
			}
			_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM agent_task_acceptances WHERE task_id=$1`, child).Scan(&acceptances)
			_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE target=$1 AND action='agent_task.publication_recovered'`, attemptID.String()).Scan(&audits)
			if used != 1 || original != "agent_adapter_execution_failed" {
				t.Fatal("budget or original failure was rewritten")
			}
			if scenario == "confirmed" {
				if state != "verified" || attemptState != "succeeded" || taskState != "completed" || acceptances != 1 || audits != 1 {
					t.Fatalf("unclosed recovery: %s %s %s %d %d", state, attemptState, taskState, acceptances, audits)
				}
				if err = s.FinishAgentPublicationRecovery(ctx, "reader", *target, event, url, ""); !errors.Is(err, ErrAgentTaskClaimLost) {
					t.Fatal("recovered completion replayed")
				}
			} else if state != "rejected" || attemptState != "needs_attention" || taskState != "needs_attention" || acceptances != 0 || audits != 0 {
				t.Fatalf("unsafe recovery: %s %s %s %d %d", state, attemptState, taskState, acceptances, audits)
			}
		})
	}
}
