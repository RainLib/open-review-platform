package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestAgentTaskHistoryPaginatesBeyondOneHundredWithoutCrossingTenants(t *testing.T) {
	databaseURL := os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL")
	if databaseURL == "" || os.Getenv("OPEN_REVIEW_TEST_ISOLATED_DATABASE") != "true" {
		t.Skip("Agent task pagination requires an isolated database")
	}
	ctx := context.Background()
	postgres, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(postgres.Close)

	tenantID, otherID := uuid.New(), uuid.New()
	installationID, otherInstallationID := uuid.New(), uuid.New()
	slug := "agent-task-page-" + tenantID.String()[:8]
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants(id,slug,name) VALUES($1,$2,'Agent task pagination'),($3,$4,'Other Agent task pagination')`, tenantID, slug, otherID, "other-"+slug); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships(tenant_id,subject,role,active) VALUES($1,'viewer','viewer',TRUE),($2,'other','owner',TRUE)`, tenantID, otherID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO provider_installations(id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref,verification_state)
		VALUES($1,$2,'github',$3,'team/*','https://api.github.com','test-only','verified'),($4,$5,'github',$6,'other/*','https://api.github.com','test-only','verified')`, installationID, tenantID, installationID.String(), otherInstallationID, otherID, otherInstallationID.String()); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO agent_tasks(id,tenant_id,installation_id,provider,api_base_url,repository,origin_kind,origin_number,origin_revision,intent,execution_branch,requested_by,created_at)
		SELECT id,$1,$2,'github','https://api.github.com','team/repo','issue',i,'revision-'||i,'implement','agent/'||id,'viewer',$3
		FROM (SELECT gen_random_uuid() AS id,i FROM generate_series(1,102) AS i) AS seeded`, tenantID, installationID, at); err != nil {
		t.Fatal(err)
	}
	foreignCursor := uuid.New()
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO agent_tasks(id,tenant_id,installation_id,provider,api_base_url,repository,origin_kind,origin_number,origin_revision,intent,execution_branch,requested_by,created_at)
		VALUES($1,$2,$3,'github','https://api.github.com','other/repo','issue',1,'foreign','implement',$5,'other',$4)`, foreignCursor, otherID, otherInstallationID, at, "agent/"+foreignCursor.String()); err != nil {
		t.Fatal(err)
	}
	seen := make(map[uuid.UUID]bool)
	cursor := uuid.Nil
	for pageNumber := 0; pageNumber < 3; pageNumber++ {
		page, err := postgres.ListAgentTasks(ctx, "viewer", slug, 40, cursor)
		if err != nil {
			t.Fatalf("page %d: %v", pageNumber, err)
		}
		want := 40
		if pageNumber == 2 {
			want = 22
		}
		if len(page.Tasks) != want {
			t.Fatalf("page %d has %d tasks, want %d", pageNumber, len(page.Tasks), want)
		}
		for _, task := range page.Tasks {
			if task.TenantID != tenantID || seen[task.ID] || task.ID == foreignCursor {
				t.Fatalf("page %d leaked or repeated task %s", pageNumber, task.ID)
			}
			seen[task.ID] = true
		}
		if pageNumber < 2 {
			if page.NextCursor == "" {
				t.Fatalf("page %d did not provide a next cursor", pageNumber)
			}
			cursor = uuid.MustParse(page.NextCursor)
		} else if page.NextCursor != "" {
			t.Fatalf("final page has cursor %s", page.NextCursor)
		}
	}
	if len(seen) != 102 {
		t.Fatalf("history has %d unique tasks, want 102", len(seen))
	}
	if _, err := postgres.ListAgentTasks(ctx, "viewer", slug, 40, foreignCursor); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign cursor error=%v, want not found", err)
	}
	if _, err := postgres.ListAgentTasks(ctx, "outsider", slug, 40, uuid.Nil); !errors.Is(err, ErrForbidden) {
		t.Fatalf("outsider error=%v, want forbidden", err)
	}
}

func TestAgentPolicyAndDirectTaskResolveRepositoryScopedInstallation(t *testing.T) {
	databaseURL := os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL")
	if databaseURL == "" || os.Getenv("OPEN_REVIEW_TEST_ISOLATED_DATABASE") != "true" {
		t.Skip("agent task installation routing requires an isolated database")
	}
	ctx := context.Background()
	postgres, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(postgres.Close)

	tenantID := uuid.New()
	tenantSlug := "agent-scope-" + tenantID.String()[:8]
	firstID := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	secondID := uuid.MustParse("00000000-0000-4000-8000-000000000002")
	thirdID := uuid.MustParse("00000000-0000-4000-8000-000000000003")
	apiBaseURL := "https://gitlab.example/api/v4"
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants(id,slug,name) VALUES($1,$2,'Agent scope integration')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships(tenant_id,subject,role,active) VALUES($1,'owner','owner',TRUE)`, tenantID); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		id         uuid.UUID
		externalID string
		scope      string
	}{
		{firstID, "agent-scope-first", "group-a/*"},
		{secondID, "agent-scope-second", "group-b/*"},
	} {
		if _, err := postgres.pool.Exec(ctx, `INSERT INTO provider_installations(id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref,verification_state) VALUES($1,$2,'gitlab',$3,$4,$5,'test-only','verified')`, item.id, tenantID, item.externalID, item.scope, apiBaseURL); err != nil {
			t.Fatal(err)
		}
	}
	policyInput := domain.AgentTaskPolicyInput{Provider: domain.ProviderGitLab, APIBaseURL: apiBaseURL, Repository: "group-b/repo", Mode: "manual", DecisionBackend: "deterministic"}
	policy, err := postgres.SaveAgentTaskPolicy(ctx, "owner", tenantSlug, policyInput)
	if err != nil || policy.Revision != 1 {
		t.Fatalf("policy for second installation=%#v error=%v", policy, err)
	}
	taskInput := domain.AgentTaskInput{Provider: domain.ProviderGitLab, APIBaseURL: apiBaseURL, Repository: "group-b/repo", OriginKind: "issue", OriginNumber: 1, OriginRevision: "verified-issue-one", Intent: "implement"}
	task, err := postgres.CreateAgentTask(ctx, "owner", tenantSlug, taskInput)
	if err != nil || task.InstallationID != secondID {
		t.Fatalf("task must bind to repository-scoped second installation: task=%#v error=%v", task, err)
	}
	// Old or imported overlapping records must not silently pick one writer.
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO provider_installations(id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref,verification_state) VALUES($1,$2,'gitlab','agent-scope-overlap','group-b/repo',$3,'test-only','verified')`, thirdID, tenantID, apiBaseURL); err != nil {
		t.Fatal(err)
	}
	policyInput.Revision = policy.Revision
	if _, err := postgres.SaveAgentTaskPolicy(ctx, "owner", tenantSlug, policyInput); !errors.Is(err, ErrAmbiguousInstallation) {
		t.Fatalf("overlapping policy scopes error=%v, want ambiguity", err)
	}
	taskInput.OriginNumber = 2
	taskInput.OriginRevision = "verified-issue-two"
	if _, err := postgres.CreateAgentTask(ctx, "owner", tenantSlug, taskInput); !errors.Is(err, ErrAmbiguousInstallation) {
		t.Fatalf("overlapping task scopes error=%v, want ambiguity", err)
	}
	var policyAudits, tasks int
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND action='agent_task.policy_saved'`, tenantID).Scan(&policyAudits); err != nil || policyAudits != 1 {
		t.Fatalf("failed policy mutation must not add an audit row: count=%d error=%v", policyAudits, err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM agent_tasks WHERE tenant_id=$1`, tenantID).Scan(&tasks); err != nil || tasks != 1 {
		t.Fatalf("ambiguous task must not be created: count=%d error=%v", tasks, err)
	}
}

func TestExactAgentPolicyLookupFindsRepositoryBeyondOverviewLimit(t *testing.T) {
	databaseURL := os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL")
	if databaseURL == "" || os.Getenv("OPEN_REVIEW_TEST_ISOLATED_DATABASE") != "true" {
		t.Skip("exact Agent policy lookup requires an isolated database")
	}
	ctx := context.Background()
	postgres, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(postgres.Close)

	tenantID, installationID := uuid.New(), uuid.New()
	tenantSlug := "agent-policy-page-" + tenantID.String()[:8]
	apiBaseURL := "https://api.github.com"
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants(id,slug,name) VALUES($1,$2,'Agent policy pagination integration')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships(tenant_id,subject,role,active) VALUES($1,'owner','owner',TRUE)`, tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO provider_installations(id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref,verification_state) VALUES($1,$2,'github',$3,'team/*',$4,'test-only','verified')`, installationID, tenantID, installationID.String(), apiBaseURL); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO agent_task_policies(tenant_id,provider,api_base_url,repository,mode,updated_by)
		SELECT $1,'github',$2,'team/repo-'||lpad(i::text,3,'0'),'disabled','seed' FROM generate_series(1,100) AS i`, tenantID, apiBaseURL); err != nil {
		t.Fatal(err)
	}
	target, err := postgres.SaveAgentTaskPolicy(ctx, "owner", tenantSlug, domain.AgentTaskPolicyInput{Provider: domain.ProviderGitHub, APIBaseURL: apiBaseURL, Repository: "team/zzz", Mode: "manual", DecisionBackend: "jev"})
	if err != nil {
		t.Fatalf("save policy beyond first page: %v", err)
	}
	firstPage, err := postgres.ListAgentTaskPolicies(ctx, "owner", tenantSlug, 100)
	if err != nil || len(firstPage) != 100 {
		t.Fatalf("first page count=%d error=%v", len(firstPage), err)
	}
	if firstPage[99].Repository == target.Repository {
		t.Fatalf("target policy unexpectedly appeared in the bounded overview")
	}
	found, err := postgres.GetAgentTaskPolicy(ctx, "owner", tenantSlug, domain.ProviderGitHub, apiBaseURL+"/", "/team/zzz/")
	if err != nil || found.ID != target.ID || found.Revision != target.Revision {
		t.Fatalf("exact policy=%#v error=%v", found, err)
	}
	if _, err := postgres.GetAgentTaskPolicy(ctx, "owner", tenantSlug, domain.ProviderGitHub, apiBaseURL, "team/missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing policy error=%v", err)
	}
	if _, err := postgres.GetAgentTaskPolicy(ctx, "outsider", tenantSlug, domain.ProviderGitHub, apiBaseURL, target.Repository); !errors.Is(err, ErrForbidden) {
		t.Fatalf("foreign actor policy error=%v", err)
	}
}

func TestAgentTasksAreDisabledByDefaultAndRequirePolicyPlanApproval(t *testing.T) {
	databaseURL := os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL")
	if databaseURL == "" || os.Getenv("OPEN_REVIEW_TEST_ISOLATED_DATABASE") != "true" {
		t.Skip("agent task integration requires OPEN_REVIEW_TEST_DATABASE_URL and an isolated database")
	}
	ctx := context.Background()
	postgres, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(postgres.Close)

	tenantID, installationID := uuid.New(), uuid.New()
	tenantSlug := "agent-task-" + tenantID.String()[:8]
	if err := postgres.pool.SendBatch(ctx, func() *pgx.Batch {
		batch := &pgx.Batch{}
		batch.Queue(`INSERT INTO tenants(id,slug,name) VALUES($1,$2,'Agent task integration')`, tenantID, tenantSlug)
		batch.Queue(`INSERT INTO memberships(tenant_id,subject,role,active) VALUES($1,'owner','owner',TRUE),($1,'reviewer','reviewer',TRUE),($1,'viewer','viewer',TRUE),($1,'feedback-reviewer','reviewer',TRUE)`, tenantID)
		batch.Queue(`INSERT INTO provider_actor_mappings(tenant_id,provider,external_id,subject) VALUES($1,'github','provider-reviewer','reviewer')`, tenantID)
		batch.Queue(`INSERT INTO provider_actor_mappings(tenant_id,provider,external_id,subject) VALUES($1,'github','provider-owner','owner')`, tenantID)
		batch.Queue(`INSERT INTO provider_actor_mappings(tenant_id,provider,external_id,subject) VALUES($1,'github','55','feedback-reviewer')`, tenantID)
		batch.Queue(`INSERT INTO provider_installations(id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref,verification_state) VALUES($1,$2,'github',$3,'RainLib/open-review-platform','https://api.github.com','test-only','verified')`, installationID, tenantID, installationID.String())
		return batch
	}()).Close(); err != nil {
		t.Fatalf("seed agent task fixture: %v", err)
	}
	// Enqueue reserves review usage in an append-only ledger. The required
	// isolated test database is discarded by the caller; deleting this tenant
	// would violate the same ledger immutability that production enforces.
	releaseCommandSource := func(event domain.AgentTaskCommandEvent, taskID uuid.UUID) {
		t.Helper()
		var interactionID uuid.UUID
		if err := postgres.pool.QueryRow(ctx, `SELECT id FROM agent_task_interactions WHERE provider=$1 AND provider_delivery_id=$2`, event.Provider, event.DeliveryID).Scan(&interactionID); err != nil {
			t.Fatal(err)
		}
		var before int
		if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM outbox_messages WHERE aggregate_id=$1 AND topic='agent.task.source.resolve.requested'`, taskID).Scan(&before); err != nil || before != 0 {
			t.Fatalf("source queued before provider acknowledgement: count=%d error=%v", before, err)
		}
		response := domain.InteractionResponse{TenantID: tenantID, Provider: event.Provider, APIBaseURL: event.APIBaseURL, InstallationExternalID: event.InstallationExternalID, CredentialRef: "test-only", Repository: event.Repository, ResourceKind: "issue", ReviewNumber: event.IssueNumber, Marker: agentTaskInteractionMarker(interactionID), SourceRelease: &domain.AgentTaskSourceRelease{TaskID: taskID, Revision: 1}}
		if err := postgres.WithAgentTaskAcknowledgementFence(ctx, response, func(context.Context) error { return nil }); err != nil {
			t.Fatalf("release acknowledged command source: %v", err)
		}
	}

	directIssue := domain.AgentTaskIssueSnapshot{Title: "Retry state is lost", Body: "Observed behavior: retries duplicate work. Expected behavior: one result. Acceptance criteria: a focused regression test proves the deduplication boundary."}
	input := domain.AgentTaskInput{Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com", Repository: "RainLib/open-review-platform", OriginKind: "issue", OriginNumber: 42, Intent: "implement"}
	input.OriginRevision = domain.AgentIssueRevision(input.Provider, input.APIBaseURL, input.Repository, input.OriginNumber, directIssue.Title, directIssue.Body)
	directIssue.Revision = input.OriginRevision
	if _, err := postgres.CreateAgentTask(ctx, "reviewer", tenantSlug, input); !errors.Is(err, ErrAgentTaskDisabled) {
		t.Fatalf("task without policy error=%v, want disabled", err)
	}
	policy, err := postgres.SaveAgentTaskPolicy(ctx, "owner", tenantSlug, domain.AgentTaskPolicyInput{Provider: input.Provider, APIBaseURL: input.APIBaseURL, Repository: input.Repository, Mode: "suggest", DecisionBackend: "deterministic"})
	if err != nil || policy.Mode != "suggest" || policy.DecisionBackend != "deterministic" || policy.Revision != 1 {
		t.Fatalf("suggest policy=%#v error=%v", policy, err)
	}
	var auditedBackend string
	if err := postgres.pool.QueryRow(ctx, `SELECT metadata->>'decision_backend' FROM audit_events WHERE tenant_id=$1 AND action='agent_task.policy_saved' AND target=$2 AND (metadata->>'revision')::int=$3`, tenantID, policy.ID.String(), policy.Revision).Scan(&auditedBackend); err != nil || auditedBackend != "deterministic" {
		t.Fatalf("initial policy decision backend audit=%q error=%v", auditedBackend, err)
	}
	if _, err := postgres.CreateAgentTask(ctx, "reviewer", tenantSlug, input); !errors.Is(err, ErrAgentTaskDisabled) {
		t.Fatalf("suggest policy must not admit execution task: %v", err)
	}
	if _, err := postgres.SaveAgentTaskPolicy(ctx, "viewer", tenantSlug, domain.AgentTaskPolicyInput{Provider: input.Provider, APIBaseURL: input.APIBaseURL, Repository: input.Repository, Mode: "manual", Revision: policy.Revision}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer policy write error=%v, want forbidden", err)
	}
	policy, err = postgres.SaveAgentTaskPolicy(ctx, "owner", tenantSlug, domain.AgentTaskPolicyInput{Provider: input.Provider, APIBaseURL: input.APIBaseURL, Repository: input.Repository, Mode: "manual", MaxAttempts: 2, MaxExecutionSeconds: 1200, Revision: policy.Revision})
	if err != nil || policy.Mode != "manual" || policy.MaxAttempts != 2 || policy.MaxExecutionSeconds != 1200 || policy.ExecutorProfile != "codex" || policy.DecisionBackend != "deterministic" || policy.Revision != 2 {
		t.Fatalf("manual policy=%#v error=%v", policy, err)
	}
	commandEvent := domain.AgentTaskCommandEvent{Provider: domain.ProviderGitHub, APIBaseURL: input.APIBaseURL, DeliveryID: "agent-task-command-" + tenantID.String(), InstallationExternalID: installationID.String(), Repository: input.Repository, IssueNumber: 43, IssueRevision: "issue-43-revision", CommentExternalID: "99", ActorExternalID: "provider-reviewer", Body: "@openreview implement", IssueTitle: "Retry loses state", IssueBody: "Observed behavior: retries duplicate work. Expected behavior: one result. Acceptance criteria: a focused regression test proves the deduplication boundary."}
	commandEvent.IssueRevision = domain.AgentIssueRevision(commandEvent.Provider, commandEvent.APIBaseURL, commandEvent.Repository, commandEvent.IssueNumber, commandEvent.IssueTitle, commandEvent.IssueBody)
	command, err := postgres.ProcessAgentTaskCommand(ctx, commandEvent, "implement", "@openreview implement")
	if err != nil || !command.Accepted || command.TaskID == nil {
		t.Fatalf("command outcome=%#v error=%v", command, err)
	}
	releaseCommandSource(commandEvent, *command.TaskID)
	if duplicate, err := postgres.ProcessAgentTaskCommand(ctx, commandEvent, "implement", "@openreview implement"); err != nil || !duplicate.Duplicate {
		t.Fatalf("duplicate command outcome=%#v error=%v", duplicate, err)
	}
	var commandTaskState, responseResourceKind string
	if err := postgres.pool.QueryRow(ctx, `SELECT state FROM agent_tasks WHERE id=$1`, *command.TaskID).Scan(&commandTaskState); err != nil || commandTaskState != "received" {
		t.Fatalf("command task state=%q error=%v", commandTaskState, err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT payload->>'resource_kind' FROM outbox_messages WHERE aggregate_id=(SELECT id FROM agent_task_interactions WHERE provider=$1 AND provider_delivery_id=$2)`, domain.ProviderGitHub, commandEvent.DeliveryID).Scan(&responseResourceKind); err != nil || responseResourceKind != "issue" {
		t.Fatalf("command response resource kind=%q error=%v", responseResourceKind, err)
	}
	var decision, riskLevel string
	if err := postgres.pool.QueryRow(ctx, `SELECT decision,risk_level FROM agent_task_classifications WHERE task_id=$1`, *command.TaskID).Scan(&decision, &riskLevel); err != nil || decision != "requires_human" || riskLevel != "low" {
		t.Fatalf("command classification decision=%q risk=%q error=%v", decision, riskLevel, err)
	}
	taskDetail, err := postgres.GetAgentTask(ctx, "owner", tenantSlug, *command.TaskID)
	if err != nil || taskDetail.Task.State != "received" || taskDetail.Task.Revision != 1 || taskDetail.Task.InstallationID != installationID {
		t.Fatalf("command-created task=%#v error=%v", taskDetail.Task, err)
	}
	task := taskDetail.Task
	if task.PolicyRevision != policy.Revision || task.MaxAttempts != 2 || task.MaxExecutionSeconds != 1200 || task.ExecutorProfile != "codex" || task.SourceState != "pending" {
		t.Fatalf("task must freeze the admitted execution envelope: %#v policy=%#v", task, policy)
	}
	// The source-admitter is the only actor that can turn a mutable default
	// branch into plan-eligible evidence. This integration test simulates its
	// successful provider read without granting the test a provider writer.
	if _, err := postgres.CreateAgentTaskPlan(ctx, "reviewer", tenantSlug, task.ID, domain.AgentTaskPlanInput{Summary: "This plan must be rejected until the provider-reading source worker freezes the exact repository base commit."}); !errors.Is(err, ErrInvalidAgentTaskPlan) {
		t.Fatalf("plan before source snapshot error=%v, want invalid", err)
	}
	if _, err := postgres.RecordAgentTaskSourceSnapshot(ctx, task.ID, domain.AgentTaskSourceSnapshot{BaseRef: "main", BaseSHA: "0123456789abcdef0123456789abcdef01234567"}); !errors.Is(err, ErrInvalidAgentTask) {
		t.Fatalf("issue task without provider Issue snapshot error=%v, want invalid", err)
	}
	if _, err := postgres.RecordAgentTaskSourceSnapshot(ctx, task.ID, domain.AgentTaskSourceSnapshot{BaseRef: "main", BaseSHA: "0123456789abcdef0123456789abcdef01234567", Issue: &domain.AgentTaskIssueSnapshot{Title: commandEvent.IssueTitle, Body: "The Issue changed after the command", Revision: commandEvent.IssueRevision}}); !errors.Is(err, ErrInvalidAgentTask) {
		t.Fatalf("changed Issue with spoofed revision error=%v, want invalid", err)
	}
	task, err = postgres.RecordAgentTaskSourceSnapshot(ctx, task.ID, domain.AgentTaskSourceSnapshot{BaseRef: "main", BaseSHA: "0123456789abcdef0123456789abcdef01234567", Issue: &domain.AgentTaskIssueSnapshot{Title: commandEvent.IssueTitle, Body: commandEvent.IssueBody, Revision: commandEvent.IssueRevision}})
	if err != nil || task.SourceState != "ready" || task.SourceBaseRef != "main" {
		t.Fatalf("source snapshot task=%#v error=%v", task, err)
	}
	var firstSourceBody, firstSourceResource string
	var firstSourceVersion int
	if err := postgres.pool.QueryRow(ctx, `SELECT payload->>'body',payload->>'resource_kind',(payload->>'status_version')::int FROM outbox_messages WHERE aggregate_id=$1 AND topic='review.interaction.response' AND payload->>'marker'=$2`, task.ID, "open-review-platform:agent-task-source:"+task.ID.String()).Scan(&firstSourceBody, &firstSourceResource, &firstSourceVersion); err != nil || firstSourceResource != "issue" || firstSourceVersion != task.Revision || !strings.Contains(firstSourceBody, "bounded plan may now be prepared") {
		t.Fatalf("first source status body=%q resource=%q version=%d error=%v", firstSourceBody, firstSourceResource, firstSourceVersion, err)
	}
	// A policy edit applies only to future Issue revisions. The operator who
	// approves this task must be able to see and rely on the exact limits they
	// approved, rather than have a later settings edit widen or shrink them.
	policy, err = postgres.SaveAgentTaskPolicy(ctx, "owner", tenantSlug, domain.AgentTaskPolicyInput{Provider: input.Provider, APIBaseURL: input.APIBaseURL, Repository: input.Repository, Mode: "manual", MaxAttempts: 1, MaxExecutionSeconds: 1800, MaxFeedbackCycles: 2, ExecutorProfile: "claude", AutoAdmissionEnabled: true, AutoAdmissionLabel: "openreview:implement", Revision: policy.Revision})
	if err != nil || policy.Revision != 3 || policy.ExecutorProfile != "claude" {
		t.Fatalf("update future agent task policy=%#v error=%v", policy, err)
	}
	taskDetail, err = postgres.GetAgentTask(ctx, "owner", tenantSlug, task.ID)
	if err != nil || taskDetail.Task.PolicyRevision != 2 || taskDetail.Task.MaxAttempts != 2 || taskDetail.Task.MaxExecutionSeconds != 1200 || taskDetail.Task.ExecutorProfile != "codex" || taskDetail.Task.DecisionBackend != "deterministic" {
		t.Fatalf("existing task envelope changed after policy update: %#v error=%v", taskDetail.Task, err)
	}
	if taskDetail.TargetBranch != "main" {
		t.Fatalf("issue review target branch=%q, want frozen main", taskDetail.TargetBranch)
	}
	autoEvent := domain.ProviderIssueEvent{Provider: domain.ProviderGitHub, APIBaseURL: input.APIBaseURL, DeliveryID: "automatic-agent-task-" + tenantID.String(), EventName: "issues", InstallationExternalID: installationID.String(), Repository: input.Repository, IssueNumber: 44, Action: "opened", Title: "Bounded retry state", Body: "Observed behavior: retry state is not retained. Expected behavior: a retry remains bound to its exact task. Acceptance criteria: a focused regression test proves the persisted state survives one retry.", Author: "alice", Labels: []string{"openreview:implement"}, ReceivedAt: time.Now().UTC()}
	auto, err := postgres.ProcessAutomaticAgentTask(ctx, autoEvent)
	if err != nil || !auto.Accepted || auto.TaskID == nil {
		t.Fatalf("automatic candidate=%#v error=%v", auto, err)
	}
	var autoSourceJobs int
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM outbox_messages WHERE aggregate_id=$1 AND topic='agent.task.source.resolve.requested'`, *auto.TaskID).Scan(&autoSourceJobs); err != nil || autoSourceJobs != 0 {
		t.Fatalf("automatic source queued before acknowledgement: count=%d error=%v", autoSourceJobs, err)
	}
	if err := postgres.WithAgentTaskAcknowledgementFence(ctx, domain.InteractionResponse{TenantID: tenantID, Provider: autoEvent.Provider, APIBaseURL: autoEvent.APIBaseURL, InstallationExternalID: autoEvent.InstallationExternalID, CredentialRef: "test-only", Repository: autoEvent.Repository, ResourceKind: "issue", ReviewNumber: autoEvent.IssueNumber, Marker: "open-review-platform:agent-task:auto:" + auto.TaskID.String(), SourceRelease: &domain.AgentTaskSourceRelease{TaskID: *auto.TaskID, Revision: 1}}, func(context.Context) error { return nil }); err != nil {
		t.Fatalf("release acknowledged automatic candidate: %v", err)
	}
	autoDetail, err := postgres.GetAgentTask(ctx, "owner", tenantSlug, *auto.TaskID)
	if err != nil || autoDetail.Task.RequestedBy != "policy:auto" || autoDetail.Task.ExecutorProfile != "claude" || autoDetail.Task.SourceState != "pending" || autoDetail.Task.State != "received" {
		t.Fatalf("automatic candidate task=%#v error=%v", autoDetail.Task, err)
	}
	if duplicate, err := postgres.ProcessAutomaticAgentTask(ctx, autoEvent); err != nil || !duplicate.Duplicate {
		t.Fatalf("automatic duplicate=%#v error=%v", duplicate, err)
	}
	secondDelivery := autoEvent
	secondDelivery.DeliveryID += "-another-webhook"
	if repeated, err := postgres.ProcessAutomaticAgentTask(ctx, secondDelivery); err != nil || !repeated.Accepted || repeated.TaskID == nil || *repeated.TaskID != *auto.TaskID {
		t.Fatalf("same-revision automatic delivery=%#v error=%v", repeated, err)
	}
	var automaticAcknowledgements int
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM outbox_messages WHERE aggregate_id=$1 AND topic='review.interaction.response' AND dedupe_key=$2`, *auto.TaskID, "agent-task-auto:"+auto.TaskID.String()+":ack").Scan(&automaticAcknowledgements); err != nil || automaticAcknowledgements != 1 {
		t.Fatalf("automatic candidate acknowledgement count=%d error=%v", automaticAcknowledgements, err)
	}
	var automaticReceipts int
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM agent_task_interactions WHERE task_id=$1 AND command='auto'`, *auto.TaskID).Scan(&automaticReceipts); err != nil || automaticReceipts != 2 {
		t.Fatalf("automatic candidate delivery receipts=%d error=%v", automaticReceipts, err)
	}
	autoSource, err := postgres.RecordAgentTaskSourceSnapshot(ctx, *auto.TaskID, domain.AgentTaskSourceSnapshot{BaseRef: "main", BaseSHA: "0123456789abcdef0123456789abcdef01234567", Issue: &domain.AgentTaskIssueSnapshot{Title: autoEvent.Title, Body: autoEvent.Body, Labels: autoEvent.Labels, Revision: autoDetail.Task.OriginRevision}})
	if err != nil || autoSource.SourceState != "ready" {
		t.Fatalf("automatic candidate source=%#v error=%v", autoSource, err)
	}
	var automaticSourceBody, automaticSourceResource string
	var automaticSourceVersion int
	if err := postgres.pool.QueryRow(ctx, `SELECT payload->>'body',payload->>'resource_kind',(payload->>'status_version')::int FROM outbox_messages WHERE aggregate_id=$1 AND topic='review.interaction.response' AND payload->>'marker'=$2`, *auto.TaskID, "open-review-platform:agent-task-source:"+auto.TaskID.String()).Scan(&automaticSourceBody, &automaticSourceResource, &automaticSourceVersion); err != nil || automaticSourceResource != "issue" || automaticSourceVersion != autoSource.Revision || !strings.Contains(automaticSourceBody, "bounded plan may now be prepared") {
		t.Fatalf("automatic source status body=%q resource=%q version=%d error=%v", automaticSourceBody, automaticSourceResource, automaticSourceVersion, err)
	}
	unlabeled := autoEvent
	unlabeled.DeliveryID += "-unlabeled"
	unlabeled.IssueNumber = 45
	unlabeled.Labels = nil
	if ignored, err := postgres.ProcessAutomaticAgentTask(ctx, unlabeled); err != nil || ignored.Accepted || ignored.TaskID != nil {
		t.Fatalf("unlabeled automatic candidate=%#v error=%v", ignored, err)
	}
	// Direct API tasks remain blocked until the provider-reading worker supplies
	// the current Issue snapshot; then they use the same classification gate as
	// a command-created task.
	directTask, err := postgres.CreateAgentTask(ctx, "reviewer", tenantSlug, input)
	if err != nil || directTask.State != "received" {
		t.Fatalf("direct task=%#v error=%v", directTask, err)
	}
	if _, err := postgres.CreateAgentTaskPlan(ctx, "reviewer", tenantSlug, directTask.ID, domain.AgentTaskPlanInput{Summary: "This direct request lacks a trusted provider Issue snapshot and therefore must not enter a coding-agent plan."}); !errors.Is(err, ErrInvalidAgentTaskPlan) {
		t.Fatalf("direct task plan error=%v, want invalid", err)
	}
	directTask, err = postgres.RecordAgentTaskSourceSnapshot(ctx, directTask.ID, domain.AgentTaskSourceSnapshot{BaseRef: "main", BaseSHA: "0123456789abcdef0123456789abcdef01234567", Issue: &directIssue})
	if err != nil || directTask.SourceState != "ready" {
		t.Fatalf("direct task source=%#v error=%v", directTask, err)
	}
	if _, err := postgres.CreateAgentTaskPlan(ctx, "reviewer", tenantSlug, directTask.ID, domain.AgentTaskPlanInput{Summary: "Fix the bounded retry state, add a focused regression test, and review the exact frozen source commit."}); err != nil {
		t.Fatalf("verified direct API task should accept a bounded plan: %v", err)
	}
	var directPlanProviderMessages int
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM outbox_messages WHERE aggregate_id=$1 AND topic='review.interaction.response' AND payload->>'marker'=$2`, directTask.ID, "open-review-platform:agent-task-source:"+directTask.ID.String()).Scan(&directPlanProviderMessages); err != nil || directPlanProviderMessages != 0 {
		t.Fatalf("direct API task emitted unverified Issue plan comment count=%d error=%v", directPlanProviderMessages, err)
	}
	plan, err := postgres.CreateAgentTaskPlan(ctx, "reviewer", tenantSlug, task.ID, domain.AgentTaskPlanInput{Summary: "Validate the reported behavior, make the smallest scoped change, add focused regression coverage, then re-run the affected checks."})
	if err != nil || plan.State != "awaiting_approval" || plan.Revision != 1 || plan.PlanSHA256 == "" {
		t.Fatalf("created plan=%#v error=%v", plan, err)
	}
	if _, err := postgres.ApproveAgentTaskPlan(ctx, "reviewer", tenantSlug, task.ID, plan.ID, domain.AgentTaskPlanApprovalInput{Revision: plan.Revision}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("reviewer approval error=%v, want forbidden", err)
	}
	stalePlan := plan
	sections := domain.AgentTaskPlanSections{
		Objective:    "Fix the bounded retry regression without changing unrelated review behavior.",
		Scope:        "Only the worker retry path and focused regression tests.",
		Verification: "Run the focused regression test and the full Go test suite.",
		Risks:        "Low; preserve existing task state transitions.",
		Unknowns:     "None after source snapshot inspection.",
	}
	plan, err = postgres.CreateAgentTaskPlan(ctx, "reviewer", tenantSlug, task.ID, domain.AgentTaskPlanInput{Sections: &sections})
	if err != nil || plan.Revision != stalePlan.Revision+1 {
		t.Fatalf("replacement plan=%#v error=%v", plan, err)
	}
	if !reflect.DeepEqual(plan.Sections, sections) || plan.Summary != sections.Summary() {
		t.Fatalf("structured plan did not persist its canonical approval contract: %#v", plan)
	}
	var sourceAndPlanMessages, sourceAndPlanMarkers, oldestPlanStatusVersion, newestPlanStatusVersion int
	var newestPlanBody string
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*),count(DISTINCT payload->>'marker'),min((payload->>'status_version')::int),max((payload->>'status_version')::int) FROM outbox_messages WHERE aggregate_id=$1 AND topic='review.interaction.response' AND payload->>'marker'=$2`, task.ID, "open-review-platform:agent-task-source:"+task.ID.String()).Scan(&sourceAndPlanMessages, &sourceAndPlanMarkers, &oldestPlanStatusVersion, &newestPlanStatusVersion); err != nil || sourceAndPlanMessages != 3 || sourceAndPlanMarkers != 1 || oldestPlanStatusVersion >= newestPlanStatusVersion {
		t.Fatalf("version-fenced source/plan updates count=%d markers=%d oldest=%d newest=%d error=%v", sourceAndPlanMessages, sourceAndPlanMarkers, oldestPlanStatusVersion, newestPlanStatusVersion, err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT payload->>'body' FROM outbox_messages WHERE aggregate_id=$1 AND topic='review.interaction.response' AND payload->>'marker'=$2 AND (payload->>'status_version')::int=$3`, task.ID, "open-review-platform:agent-task-source:"+task.ID.String(), newestPlanStatusVersion).Scan(&newestPlanBody); err != nil || !strings.Contains(newestPlanBody, plan.PlanSHA256) || strings.Contains(newestPlanBody, stalePlan.PlanSHA256) || !strings.Contains(newestPlanBody, "@openreview approve "+plan.PlanSHA256) {
		t.Fatalf("latest Issue plan status=%q error=%v", newestPlanBody, err)
	}
	if current, err := postgres.AgentTaskSourceStatusCurrent(ctx, task.ID, oldestPlanStatusVersion); err != nil || current {
		t.Fatalf("old source comment can overwrite newer plan: current=%v error=%v", current, err)
	}
	reviewerDetail, err := postgres.GetAgentTask(ctx, "reviewer", tenantSlug, task.ID)
	if err != nil || !reviewerDetail.PlanPermissions.CanCreatePlan || reviewerDetail.PlanPermissions.CanApprovePlan || reviewerDetail.PlanPermissions.ApproveBlockReason != "owner_admin_required" {
		t.Fatalf("reviewer plan affordance=%#v error=%v", reviewerDetail.PlanPermissions, err)
	}
	ownerDetail, err := postgres.GetAgentTask(ctx, "owner", tenantSlug, task.ID)
	if err != nil || !ownerDetail.PlanPermissions.CanApprovePlan {
		t.Fatalf("owner approval affordance=%#v error=%v", ownerDetail.PlanPermissions, err)
	}
	var stalePlanState string
	if err := postgres.pool.QueryRow(ctx, `SELECT state FROM agent_task_plans WHERE id=$1`, stalePlan.ID).Scan(&stalePlanState); err != nil || stalePlanState != "superseded" {
		t.Fatalf("replaced plan state=%q error=%v", stalePlanState, err)
	}
	if _, err := postgres.ApproveAgentTaskPlan(ctx, "owner", tenantSlug, task.ID, stalePlan.ID, domain.AgentTaskPlanApprovalInput{Revision: stalePlan.Revision}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale plan approval error=%v, want conflict", err)
	}
	approvalEvent := commandEvent
	approvalEvent.DeliveryID = "agent-task-approval-reviewer-" + tenantID.String()
	approvalEvent.CommentExternalID = "100"
	approvalEvent.Body = "@openreview approve " + plan.PlanSHA256
	if denied, err := postgres.ProcessAgentTaskCommand(ctx, approvalEvent, "approve", approvalEvent.Body); err != nil || denied.Accepted || denied.Reason == "" {
		t.Fatalf("reviewer provider approval=%#v error=%v, want rejected", denied, err)
	}
	approvalEvent.ActorExternalID = "provider-owner"
	approvalEvent.DeliveryID = "agent-task-approval-stale-" + tenantID.String()
	approvalEvent.CommentExternalID = "101"
	approvalEvent.Body = "@openreview approve " + stalePlan.PlanSHA256
	if denied, err := postgres.ProcessAgentTaskCommand(ctx, approvalEvent, "approve", approvalEvent.Body); err != nil || denied.Accepted || denied.Reason == "" {
		t.Fatalf("stale provider approval=%#v error=%v, want rejected", denied, err)
	}
	approvalEvent.DeliveryID = "agent-task-approval-current-" + tenantID.String()
	approvalEvent.CommentExternalID = "102"
	approvalEvent.Body = "@openreview approve " + plan.PlanSHA256
	wrongRevisionEvent := approvalEvent
	wrongRevisionEvent.DeliveryID = "agent-task-approval-wrong-issue-" + tenantID.String()
	wrongRevisionEvent.CommentExternalID = "103"
	wrongRevisionEvent.IssueRevision = "different-issue-revision"
	if denied, err := postgres.ProcessAgentTaskCommand(ctx, wrongRevisionEvent, "approve", wrongRevisionEvent.Body); err != nil || denied.Accepted || denied.Reason == "" {
		t.Fatalf("wrong Issue revision approval=%#v error=%v, want rejected", denied, err)
	}
	if outcome, err := postgres.ProcessAgentTaskCommand(ctx, approvalEvent, "approve", approvalEvent.Body); err != nil || !outcome.Accepted || outcome.TaskID == nil || *outcome.TaskID != task.ID {
		t.Fatalf("owner provider approval=%#v error=%v", outcome, err)
	}
	if replay, err := postgres.ProcessAgentTaskCommand(ctx, approvalEvent, "approve", approvalEvent.Body); err != nil || !replay.Duplicate {
		t.Fatalf("provider approval replay=%#v error=%v", replay, err)
	}
	redundantEvent := approvalEvent
	redundantEvent.DeliveryID = "agent-task-approval-after-state-change-" + tenantID.String()
	redundantEvent.CommentExternalID = "104"
	if denied, err := postgres.ProcessAgentTaskCommand(ctx, redundantEvent, "approve", redundantEvent.Body); err != nil || denied.Accepted || denied.Reason == "" {
		t.Fatalf("second approval after task state change=%#v error=%v, want rejected", denied, err)
	}
	approvedDetail, err := postgres.GetAgentTask(ctx, "owner", tenantSlug, task.ID)
	if err != nil || approvedDetail.Task.State != "execution_queued" || len(approvedDetail.Plans) == 0 {
		t.Fatalf("approved task detail=%#v error=%v", approvedDetail, err)
	}
	approved := approvedDetail.Plans[0]
	if approved.State != "approved" || approved.ApprovedBy != "owner" || approved.ApprovedAt == nil {
		t.Fatalf("approved plan=%#v", approved)
	}
	var executionRequests int
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM outbox_messages WHERE aggregate_id=$1 AND topic='agent.task.execute.requested'`, task.ID).Scan(&executionRequests); err != nil || executionRequests != 1 {
		t.Fatalf("provider approval execution requests=%d error=%v", executionRequests, err)
	}
	var approvedStatusBody string
	var approvedStatusVersion int
	if err := postgres.pool.QueryRow(ctx, `SELECT payload->>'body',(payload->>'status_version')::int FROM outbox_messages WHERE aggregate_id=$1 AND topic='review.interaction.response' AND payload->>'marker'=$2 ORDER BY (payload->>'status_version')::int DESC LIMIT 1`, task.ID, "open-review-platform:agent-task-source:"+task.ID.String()).Scan(&approvedStatusBody, &approvedStatusVersion); err != nil || approvedStatusVersion != approvedDetail.Task.Revision || !strings.Contains(approvedStatusBody, "Execution is **queued**") || strings.Contains(approvedStatusBody, "@openreview approve") {
		t.Fatalf("approved Issue task status body=%q version=%d error=%v", approvedStatusBody, approvedStatusVersion, err)
	}
	if current, err := postgres.AgentTaskSourceStatusCurrent(ctx, task.ID, newestPlanStatusVersion); err != nil || current {
		t.Fatalf("pending plan comment can overwrite approval: current=%v error=%v", current, err)
	}
	if _, err := postgres.CreateAgentTaskPlan(ctx, "reviewer", tenantSlug, task.ID, domain.AgentTaskPlanInput{Summary: "This second plan must not be accepted once the exact prior plan has reached the admitted state."}); !errors.Is(err, ErrInvalidAgentTaskPlan) {
		t.Fatalf("admitted task plan error=%v, want invalid", err)
	}
	var state string
	var taskRevision int
	if err := postgres.pool.QueryRow(ctx, `SELECT state,revision FROM agent_tasks WHERE id=$1`, task.ID).Scan(&state, &taskRevision); err != nil || state != "execution_queued" || taskRevision < 1 {
		t.Fatalf("task state=%q revision=%d error=%v", state, taskRevision, err)
	}
	var executeTopic, executePlanSHA string
	if err := postgres.pool.QueryRow(ctx, `SELECT topic,payload->>'plan_sha256' FROM outbox_messages WHERE aggregate_id=$1 AND topic='agent.task.execute.requested'`, task.ID).Scan(&executeTopic, &executePlanSHA); err != nil || executeTopic != "agent.task.execute.requested" || executePlanSHA != approved.PlanSHA256 {
		t.Fatalf("approved execution dispatch topic=%q plan_sha=%q error=%v", executeTopic, executePlanSHA, err)
	}
	attempt, err := postgres.ClaimAgentTaskAttempt(ctx, "agent-worker", domain.AgentTaskExecutionRequest{TaskID: task.ID, TaskRevision: taskRevision, PlanID: approved.ID, PlanRevision: approved.Revision, PlanSHA256: approved.PlanSHA256}, time.Minute)
	if err != nil || attempt.State != "running" || attempt.Attempt != 1 || attempt.LockedUntil == nil {
		t.Fatalf("claimed agent attempt=%#v error=%v", attempt, err)
	}
	if current, err := postgres.AgentTaskSourceStatusCurrent(ctx, task.ID, approvedStatusVersion); err != nil || current {
		t.Fatalf("queued comment can publish after execution starts: current=%v error=%v", current, err)
	}
	lateApprovalPublications := 0
	if err := postgres.WithAgentTaskSourcePublicationFence(ctx, task.ID, approvedStatusVersion, func(context.Context) error {
		lateApprovalPublications++
		return nil
	}); err != nil || lateApprovalPublications != 0 {
		t.Fatalf("late approval comment published=%d error=%v", lateApprovalPublications, err)
	}
	target, err := postgres.LoadAgentTaskAttemptTarget(ctx, attempt.ID, "agent-worker")
	if err != nil || !reflect.DeepEqual(target.Plan.Sections, sections) || target.Plan.Summary != approved.Summary || target.Plan.PlanSHA256 != approved.PlanSHA256 {
		t.Fatalf("execution handoff lost approved structured plan: %#v error=%v", target.Plan, err)
	}
	if err := postgres.RenewAgentTaskAttemptLease(ctx, attempt.ID, "agent-worker", time.Minute); err != nil {
		t.Fatalf("renew agent attempt lease: %v", err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE agent_task_attempts SET locked_until=now()-interval '1 second' WHERE id=$1`, attempt.ID); err != nil {
		t.Fatalf("expire pre-adapter claim: %v", err)
	}
	preAdapterRetry := domain.AgentTaskExecutionRequest{TaskID: task.ID, TaskRevision: taskRevision, PlanID: approved.ID, PlanRevision: approved.Revision, PlanSHA256: approved.PlanSHA256}
	firstAttemptID := attempt.ID
	attempt, err = postgres.ClaimAgentTaskAttempt(ctx, "agent-worker", preAdapterRetry, time.Minute)
	if err != nil || attempt.ID != firstAttemptID || attempt.Attempt != 2 || attempt.AdapterJobID != "" {
		t.Fatalf("pre-adapter lease reclaim=%#v error=%v", attempt, err)
	}
	firstAdapterJobID := "adapter-job-first-" + tenantID.String()
	secondAdapterJobID := "adapter-job-second-" + tenantID.String()
	if err := postgres.AttachAgentTaskAdapterJob(ctx, attempt.ID, "agent-worker", firstAdapterJobID); err != nil {
		t.Fatalf("attach first adapter job: %v", err)
	}
	pendingStartRequest := domain.AgentTaskExecutionRequest{TaskID: task.ID, TaskRevision: taskRevision, PlanID: approved.ID, PlanRevision: approved.Revision, PlanSHA256: approved.PlanSHA256}
	if pending, err := postgres.PendingAgentTaskAdapterStart(ctx, "agent-worker", pendingStartRequest); err != nil || pending.ID != attempt.ID || pending.AdapterJobID != firstAdapterJobID {
		t.Fatalf("pending attached adapter start=%#v error=%v", pending, err)
	}
	if _, err := postgres.PendingAgentTaskAdapterStart(ctx, "other-agent-worker", pendingStartRequest); !errors.Is(err, ErrNoQueuedAgentTask) {
		t.Fatalf("other worker resumed adapter start: %v", err)
	}
	wrongStartRequest := pendingStartRequest
	wrongStartRequest.PlanSHA256 = strings.Repeat("a", 64)
	if _, err := postgres.PendingAgentTaskAdapterStart(ctx, "agent-worker", wrongStartRequest); !errors.Is(err, ErrNoQueuedAgentTask) {
		t.Fatalf("different plan digest resumed adapter start: %v", err)
	}
	if err := postgres.ClaimAgentTaskAdapterStart(ctx, attempt.ID, firstAdapterJobID); err != nil {
		t.Fatalf("start first adapter job: %v", err)
	}
	if _, err := postgres.PendingAgentTaskAdapterStart(ctx, "agent-worker", pendingStartRequest); !errors.Is(err, ErrNoQueuedAgentTask) {
		t.Fatalf("claimed start gate resumed adapter start: %v", err)
	}
	grant, err := postgres.LoadAgentTaskCredentialGrant(ctx, attempt.ID, firstAdapterJobID)
	if err != nil || grant.TenantID != tenantID || grant.InstallationID != installationID || grant.Provider != domain.ProviderGitHub || grant.Repository != input.Repository || grant.ReviewInstallationExternalID != installationID.String() {
		t.Fatalf("started adapter credential grant=%#v error=%v", grant, err)
	}
	wrongGrant := grant
	wrongGrant.Repository = "RainLib/other"
	if _, err := postgres.ReserveAgentTaskCredentialIssuance(ctx, attempt.ID, firstAdapterJobID, wrongGrant); !errors.Is(err, ErrAgentTaskClaimLost) {
		t.Fatalf("changed credential scope reserved an issuance: %v", err)
	}
	for index := 0; index < maxAgentCredentialIssuancesPerJob; index++ {
		issuanceID, err := postgres.ReserveAgentTaskCredentialIssuance(ctx, attempt.ID, firstAdapterJobID, grant)
		if err != nil || issuanceID == uuid.Nil {
			t.Fatalf("reserve bounded credential issuance %d: id=%s err=%v", index+1, issuanceID, err)
		}
		if index == 0 {
			if err := postgres.CompleteAgentTaskCredentialIssuance(ctx, issuanceID, "issued"); err != nil {
				t.Fatalf("complete active credential issuance: %v", err)
			}
			if err := postgres.CompleteAgentTaskCredentialIssuance(ctx, issuanceID, "issued"); !errors.Is(err, ErrAgentTaskClaimLost) {
				t.Fatalf("duplicate credential completion error=%v, want claim lost", err)
			}
		}
	}
	if _, err := postgres.ReserveAgentTaskCredentialIssuance(ctx, attempt.ID, firstAdapterJobID, grant); !errors.Is(err, ErrAgentCredentialIssuanceLimit) {
		t.Fatalf("unbounded credential issuance error=%v, want limit", err)
	}
	if _, err := postgres.LoadAgentTaskCredentialGrant(ctx, attempt.ID, "other-adapter-job"); !errors.Is(err, ErrAgentTaskClaimLost) {
		t.Fatalf("unbound adapter credential grant error=%v, want claim lost", err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE provider_installations SET repository_scope='RainLib/other' WHERE id=$1`, installationID); err != nil {
		t.Fatalf("narrow installation scope: %v", err)
	}
	if _, err := postgres.LoadAgentTaskCredentialGrant(ctx, attempt.ID, firstAdapterJobID); !errors.Is(err, ErrAgentTaskClaimLost) {
		t.Fatalf("out-of-scope adapter credential grant error=%v, want claim lost", err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE provider_installations SET repository_scope='RainLib/open-review-platform',active=FALSE WHERE id=$1`, installationID); err != nil {
		t.Fatalf("deactivate installation: %v", err)
	}
	if _, err := postgres.LoadAgentTaskCredentialGrant(ctx, attempt.ID, firstAdapterJobID); !errors.Is(err, ErrAgentTaskClaimLost) {
		t.Fatalf("deactivated installation credential grant error=%v, want claim lost", err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE provider_installations SET active=TRUE WHERE id=$1`, installationID); err != nil {
		t.Fatalf("restore installation for attempt test: %v", err)
	}
	if err := postgres.ClaimAgentTaskAdapterStart(ctx, attempt.ID, firstAdapterJobID); !errors.Is(err, ErrAgentTaskClaimLost) {
		t.Fatalf("duplicate first adapter start error=%v, want claim lost", err)
	}
	if _, err := postgres.ClaimAgentTaskAttempt(ctx, "other-agent-worker", domain.AgentTaskExecutionRequest{TaskID: task.ID, TaskRevision: taskRevision, PlanID: approved.ID, PlanRevision: approved.Revision, PlanSHA256: approved.PlanSHA256}, time.Minute); !errors.Is(err, ErrNoQueuedAgentTask) {
		t.Fatalf("second agent claim error=%v, want no queued", err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE agent_task_attempts SET locked_until=now()-interval '1 second' WHERE id=$1`, attempt.ID); err != nil {
		t.Fatalf("expire agent attempt lease: %v", err)
	}
	if _, err := postgres.PendingAgentTaskAdapterStart(ctx, "agent-worker", pendingStartRequest); !errors.Is(err, ErrNoQueuedAgentTask) {
		t.Fatalf("expired lease resumed adapter start: %v", err)
	}
	if _, err := postgres.LoadAgentTaskCredentialGrant(ctx, attempt.ID, firstAdapterJobID); !errors.Is(err, ErrAgentTaskClaimLost) {
		t.Fatalf("expired adapter credential grant error=%v, want claim lost", err)
	}
	if _, err := postgres.ClaimAgentTaskAttempt(ctx, "recovered-agent-worker", pendingStartRequest, time.Minute); !errors.Is(err, ErrNoQueuedAgentTask) {
		t.Fatalf("expired attached job was automatically re-executed: %v", err)
	}
	if expired, err := postgres.ExpireAgentTaskAttempts(ctx, 25); err != nil || expired != 1 {
		t.Fatalf("expire attached agent job=%d error=%v", expired, err)
	}
	if err := postgres.ClaimAgentTaskAdapterStart(ctx, attempt.ID, firstAdapterJobID); !errors.Is(err, ErrAgentTaskClaimLost) {
		t.Fatalf("stale adapter start error=%v, want claim lost", err)
	}
	if _, err := postgres.LoadAgentTaskCredentialGrant(ctx, attempt.ID, firstAdapterJobID); !errors.Is(err, ErrAgentTaskClaimLost) {
		t.Fatalf("expired adapter credential grant error=%v, want claim lost", err)
	}
	var firstCancellationJobID string
	if err := postgres.pool.QueryRow(ctx, `SELECT payload->>'adapter_job_id' FROM outbox_messages WHERE aggregate_id=$1 AND topic='agent.task.cancel.requested'`, task.ID).Scan(&firstCancellationJobID); err != nil || firstCancellationJobID != firstAdapterJobID {
		t.Fatalf("expired job cancellation=%q error=%v", firstCancellationJobID, err)
	}
	var expiredStatusBody string
	if err := postgres.pool.QueryRow(ctx, `SELECT payload->>'body' FROM outbox_messages WHERE aggregate_id=$1 AND topic='review.interaction.response'`, attempt.ID).Scan(&expiredStatusBody); err != nil || !strings.Contains(expiredStatusBody, "Coding or a provider write may already have started") || strings.Contains(expiredStatusBody, "no coding action was performed") {
		t.Fatalf("expired job status body=%q error=%v", expiredStatusBody, err)
	}
	recoveryPlan, err := postgres.CreateAgentTaskPlan(ctx, "reviewer", tenantSlug, task.ID, domain.AgentTaskPlanInput{Summary: "After inspecting the expired adapter job and provider branch, make only the bounded fix and rerun the focused regression test."})
	if err != nil || recoveryPlan.Revision != plan.Revision+1 || recoveryPlan.State != "awaiting_approval" {
		t.Fatalf("post-expiry recovery plan=%#v error=%v", recoveryPlan, err)
	}
	if _, err := postgres.ApproveAgentTaskPlan(ctx, "owner", tenantSlug, task.ID, approved.ID, domain.AgentTaskPlanApprovalInput{Revision: approved.Revision}); !errors.Is(err, ErrConflict) {
		t.Fatalf("previously executed plan approval error=%v, want conflict", err)
	}
	if recovered, err := postgres.ApproveAgentTaskPlan(ctx, "owner", tenantSlug, task.ID, recoveryPlan.ID, domain.AgentTaskPlanApprovalInput{Revision: recoveryPlan.Revision}); err != nil || recovered.State != "approved" {
		t.Fatalf("fresh recovery approval=%#v error=%v", recovered, err)
	}
	var recoveryTaskRevision int
	if err := postgres.pool.QueryRow(ctx, `SELECT revision FROM agent_tasks WHERE id=$1`, task.ID).Scan(&recoveryTaskRevision); err != nil {
		t.Fatalf("load recovery task revision: %v", err)
	}
	recoveryStartRequest := domain.AgentTaskExecutionRequest{TaskID: task.ID, TaskRevision: recoveryTaskRevision, PlanID: recoveryPlan.ID, PlanRevision: recoveryPlan.Revision, PlanSHA256: recoveryPlan.PlanSHA256}
	recovered, err := postgres.ClaimAgentTaskAttempt(ctx, "recovered-agent-worker", recoveryStartRequest, time.Minute)
	if err != nil || recovered.ID == attempt.ID || recovered.Attempt != 1 || recovered.WorkerID != "recovered-agent-worker" {
		t.Fatalf("new-plan agent attempt=%#v error=%v", recovered, err)
	}
	attempt = recovered
	if err := postgres.AttachAgentTaskAdapterJob(ctx, attempt.ID, "recovered-agent-worker", secondAdapterJobID); err != nil {
		t.Fatalf("attach new-plan adapter job: %v", err)
	}
	if _, err := postgres.PendingAgentTaskAdapterStart(ctx, "agent-worker", pendingStartRequest); !errors.Is(err, ErrNoQueuedAgentTask) {
		t.Fatalf("old worker resumed new-plan adapter job: %v", err)
	}
	if pending, err := postgres.PendingAgentTaskAdapterStart(ctx, "recovered-agent-worker", recoveryStartRequest); err != nil || pending.ID != attempt.ID || pending.AdapterJobID != secondAdapterJobID {
		t.Fatalf("new-plan pending adapter start=%#v error=%v", pending, err)
	}
	if err := postgres.ClaimAgentTaskAdapterStart(ctx, attempt.ID, secondAdapterJobID); err != nil {
		t.Fatalf("start reclaimed adapter job: %v", err)
	}
	if _, err := postgres.PendingAgentTaskAdapterStart(ctx, "recovered-agent-worker", recoveryStartRequest); !errors.Is(err, ErrNoQueuedAgentTask) {
		t.Fatalf("new-plan start gate resumed adapter start: %v", err)
	}
	if _, err := postgres.LoadAgentTaskCredentialGrant(ctx, attempt.ID, firstAdapterJobID); !errors.Is(err, ErrAgentTaskClaimLost) {
		t.Fatalf("stale adapter credential grant error=%v, want claim lost", err)
	}
	secondGrant, err := postgres.LoadAgentTaskCredentialGrant(ctx, attempt.ID, secondAdapterJobID)
	if err != nil {
		t.Fatalf("reclaimed adapter credential grant: %v", err)
	}
	type issuanceResult struct {
		id  uuid.UUID
		err error
	}
	issuanceResults := make(chan issuanceResult, 12)
	var issuanceWait sync.WaitGroup
	for index := 0; index < 12; index++ {
		issuanceWait.Add(1)
		go func() {
			defer issuanceWait.Done()
			id, reserveErr := postgres.ReserveAgentTaskCredentialIssuance(ctx, attempt.ID, secondAdapterJobID, secondGrant)
			issuanceResults <- issuanceResult{id: id, err: reserveErr}
		}()
	}
	issuanceWait.Wait()
	close(issuanceResults)
	var secondIssuanceID uuid.UUID
	var reserved, limited int
	for result := range issuanceResults {
		switch {
		case result.err == nil && result.id != uuid.Nil:
			reserved++
			secondIssuanceID = result.id
		case errors.Is(result.err, ErrAgentCredentialIssuanceLimit):
			limited++
		default:
			t.Fatalf("concurrent credential reservation returned id=%s err=%v", result.id, result.err)
		}
	}
	if reserved != maxAgentCredentialIssuancesPerJob || limited != 12-maxAgentCredentialIssuancesPerJob {
		t.Fatalf("reclaimed job credential budget: reserved=%d limited=%d", reserved, limited)
	}
	if err := postgres.ClaimAgentTaskAdapterStart(ctx, attempt.ID, secondAdapterJobID); !errors.Is(err, ErrAgentTaskClaimLost) {
		t.Fatalf("duplicate reclaimed adapter start error=%v, want claim lost", err)
	}
	if err := postgres.MarkAgentTaskAttemptNeedsAttention(ctx, attempt.ID, "recovered-agent-worker", "agent_operator_intervention", "An operator stopped the attached adapter job after inspecting its state."); err != nil {
		t.Fatalf("mark agent attempt needs attention: %v", err)
	}
	var secondCancellationJobID string
	if err := postgres.pool.QueryRow(ctx, `SELECT payload->>'adapter_job_id' FROM outbox_messages WHERE aggregate_id=$1 AND topic='agent.task.cancel.requested' AND payload->>'adapter_job_id'=$2`, task.ID, secondAdapterJobID).Scan(&secondCancellationJobID); err != nil || secondCancellationJobID != secondAdapterJobID {
		t.Fatalf("marked job cancellation=%q error=%v", secondCancellationJobID, err)
	}
	if _, err := postgres.PendingAgentTaskAdapterStart(ctx, "recovered-agent-worker", recoveryStartRequest); !errors.Is(err, ErrNoQueuedAgentTask) {
		t.Fatalf("terminal task resumed adapter start: %v", err)
	}
	if err := postgres.CompleteAgentTaskCredentialIssuance(ctx, secondIssuanceID, "issued"); !errors.Is(err, ErrAgentTaskClaimLost) {
		t.Fatalf("terminal task returned an issued credential: %v", err)
	}
	if err := postgres.CompleteAgentTaskCredentialIssuance(ctx, secondIssuanceID, "withheld"); err != nil {
		t.Fatalf("record withheld credential after task termination: %v", err)
	}
	if _, err := postgres.LoadAgentTaskCredentialGrant(ctx, attempt.ID, secondAdapterJobID); !errors.Is(err, ErrAgentTaskClaimLost) {
		t.Fatalf("terminal adapter credential grant error=%v, want claim lost", err)
	}
	detail, err := postgres.GetAgentTask(ctx, "owner", tenantSlug, task.ID)
	if err != nil || detail.Task.State != "needs_attention" || len(detail.Attempts) != 2 || detail.Attempts[0].State != "needs_attention" || detail.Attempts[0].ErrorCode != "agent_operator_intervention" {
		t.Fatalf("agent task detail after unavailable executor=%#v error=%v", detail, err)
	}
	var attentionBody string
	if err := postgres.pool.QueryRow(ctx, `SELECT payload->>'body' FROM outbox_messages WHERE aggregate_id=$1 AND topic='review.interaction.response'`, attempt.ID).Scan(&attentionBody); err != nil || !strings.Contains(attentionBody, "Agent task needs attention") || !strings.Contains(attentionBody, "Coding or a provider write may already have started") || strings.Contains(attentionBody, "no coding action was performed") {
		t.Fatalf("agent attention provider response body=%q error=%v", attentionBody, err)
	}
	completionCommandEvent := commandEvent
	completionCommandEvent.DeliveryID = "agent-task-completion-command-" + tenantID.String()
	completionCommandEvent.IssueNumber = 44
	completionCommandEvent.IssueRevision = "issue-44-revision"
	completionCommandEvent.CommentExternalID = "100"
	completionCommandEvent.IssueTitle = "Fix bounded retry status"
	completionCommandEvent.IssueBody = "Observed behavior: a recovered attempt does not retain a useful status message. Expected behavior: a compact status is retained. Acceptance criteria: focused regression coverage proves one recovery result."
	completionCommandEvent.IssueRevision = domain.AgentIssueRevision(completionCommandEvent.Provider, completionCommandEvent.APIBaseURL, completionCommandEvent.Repository, completionCommandEvent.IssueNumber, completionCommandEvent.IssueTitle, completionCommandEvent.IssueBody)
	completionCommand, err := postgres.ProcessAgentTaskCommand(ctx, completionCommandEvent, "implement", "@openreview implement")
	if err != nil || !completionCommand.Accepted || completionCommand.TaskID == nil {
		t.Fatalf("create adapter-completion task outcome=%#v error=%v", completionCommand, err)
	}
	completionTaskDetail, err := postgres.GetAgentTask(ctx, "owner", tenantSlug, *completionCommand.TaskID)
	if err != nil {
		t.Fatalf("load adapter-completion task: %v", err)
	}
	completionTask := completionTaskDetail.Task
	completionTask, err = postgres.RecordAgentTaskSourceSnapshot(ctx, completionTask.ID, domain.AgentTaskSourceSnapshot{BaseRef: "main", BaseSHA: "1123456789abcdef0123456789abcdef01234567", Issue: &domain.AgentTaskIssueSnapshot{Title: completionCommandEvent.IssueTitle, Body: completionCommandEvent.IssueBody, Revision: completionCommandEvent.IssueRevision}})
	if err != nil {
		t.Fatalf("freeze adapter-completion source: %v", err)
	}
	completionPlan, err := postgres.CreateAgentTaskPlan(ctx, "reviewer", tenantSlug, completionTask.ID, domain.AgentTaskPlanInput{Summary: "Make a narrowly scoped fix in the approved boundary, add the focused regression test, and publish only a draft pull request for review."})
	if err != nil {
		t.Fatalf("create adapter-completion plan: %v", err)
	}
	completionPlan, err = postgres.ApproveAgentTaskPlan(ctx, "owner", tenantSlug, completionTask.ID, completionPlan.ID, domain.AgentTaskPlanApprovalInput{Revision: completionPlan.Revision})
	if err != nil {
		t.Fatalf("approve adapter-completion plan: %v", err)
	}
	var completionTaskRevision int
	if err := postgres.pool.QueryRow(ctx, `SELECT revision FROM agent_tasks WHERE id=$1`, completionTask.ID).Scan(&completionTaskRevision); err != nil {
		t.Fatalf("load adapter-completion task revision: %v", err)
	}
	completionAttempt, err := postgres.ClaimAgentTaskAttempt(ctx, "adapter-worker", domain.AgentTaskExecutionRequest{TaskID: completionTask.ID, TaskRevision: completionTaskRevision, PlanID: completionPlan.ID, PlanRevision: completionPlan.Revision, PlanSHA256: completionPlan.PlanSHA256}, time.Minute)
	if err != nil {
		t.Fatalf("claim adapter-completion attempt: %v", err)
	}
	target, err = postgres.LoadAgentTaskAttemptTarget(ctx, completionAttempt.ID, "adapter-worker")
	if err != nil || target.Task.ID != completionTask.ID || target.Plan.ID != completionPlan.ID || target.Attempt.ID != completionAttempt.ID {
		t.Fatalf("adapter target=%#v error=%v", target, err)
	}
	completionJobID := "adapter-job-44-" + tenantID.String()
	if err := postgres.ClaimAgentTaskAdapterStart(ctx, completionAttempt.ID, completionJobID); !errors.Is(err, ErrAgentTaskClaimLost) {
		t.Fatalf("start before job attachment error=%v, want lost claim", err)
	}
	if err := postgres.AttachAgentTaskAdapterJob(ctx, completionAttempt.ID, "adapter-worker", completionJobID); err != nil {
		t.Fatalf("attach adapter job: %v", err)
	}
	if _, _, err := postgres.RecordAgentTaskAdapterEvent(ctx, domain.AgentTaskAdapterEvent{AttemptID: completionAttempt.ID, AdapterJobID: completionJobID, DeliveryID: "pre-start-heartbeat-44", Kind: "heartbeat"}, time.Minute); !errors.Is(err, ErrAgentTaskClaimLost) {
		t.Fatalf("callback before durable start error=%v, want lost claim", err)
	}
	if err := postgres.ClaimAgentTaskAdapterStart(ctx, completionAttempt.ID, "wrong-job"); !errors.Is(err, ErrAgentTaskClaimLost) {
		t.Fatalf("wrong job start error=%v, want lost claim", err)
	}
	if err := postgres.ClaimAgentTaskAdapterStart(ctx, completionAttempt.ID, completionJobID); err != nil {
		t.Fatalf("claim durable adapter start: %v", err)
	}
	if err := postgres.ClaimAgentTaskAdapterStart(ctx, completionAttempt.ID, completionJobID); !errors.Is(err, ErrAgentTaskClaimLost) {
		t.Fatalf("replayed adapter start error=%v, want lost claim", err)
	}
	if heartbeat, duplicate, err := postgres.RecordAgentTaskAdapterEvent(ctx, domain.AgentTaskAdapterEvent{AttemptID: completionAttempt.ID, AdapterJobID: completionJobID, DeliveryID: "adapter-heartbeat-44", Kind: "heartbeat"}, time.Minute); err != nil || duplicate || heartbeat.State != "running" || heartbeat.LockedUntil == nil {
		t.Fatalf("adapter heartbeat=%#v duplicate=%v error=%v", heartbeat, duplicate, err)
	}
	checkpointEvent := domain.AgentTaskAdapterEvent{AttemptID: completionAttempt.ID, AdapterJobID: completionJobID, DeliveryID: "adapter-checkpoint-44", Kind: "publication_checkpoint", BranchName: completionTask.ExecutionBranch, HeadSHA: "0123456789abcdef0123456789abcdef01234567", PatchSHA256: strings.Repeat("a", 64), ChangedFileCount: 2, DiffBytes: 512, VerificationProfileSHA256: strings.Repeat("c", 64), VerificationOutputSHA256: strings.Repeat("d", 64), VerificationOutputBytes: 42}
	if checked, duplicate, err := postgres.RecordAgentTaskAdapterEvent(ctx, checkpointEvent, time.Minute); err != nil || duplicate || checked.State != "running" || checked.PullRequestURL != "" {
		t.Fatalf("pre-push checkpoint was treated as a result: attempt=%#v duplicate=%v error=%v", checked, duplicate, err)
	}
	if _, duplicate, err := postgres.RecordAgentTaskAdapterEvent(ctx, checkpointEvent, time.Minute); err != nil || !duplicate {
		t.Fatalf("checkpoint replay was not idempotent: duplicate=%v error=%v", duplicate, err)
	}
	conflictingCheckpoint := checkpointEvent
	conflictingCheckpoint.DeliveryID = "adapter-conflicting-checkpoint-44"
	conflictingCheckpoint.HeadSHA = strings.Repeat("f", 40)
	if _, _, err := postgres.RecordAgentTaskAdapterEvent(ctx, conflictingCheckpoint, time.Minute); !errors.Is(err, ErrInvalidAgentTask) {
		t.Fatalf("conflicting publication checkpoint error=%v, want invalid", err)
	}
	conflictingVerification := checkpointEvent
	conflictingVerification.DeliveryID = "adapter-conflicting-verification-44"
	conflictingVerification.VerificationOutputSHA256 = strings.Repeat("e", 64)
	if _, _, err := postgres.RecordAgentTaskAdapterEvent(ctx, conflictingVerification, time.Minute); !errors.Is(err, ErrInvalidAgentTask) {
		t.Fatalf("conflicting verification checkpoint error=%v, want invalid", err)
	}
	preDraftDetail, err := postgres.GetAgentTask(ctx, "owner", tenantSlug, completionTask.ID)
	if err != nil || len(preDraftDetail.PublicationCheckpoints) != 1 || preDraftDetail.PublicationCheckpoints[0].HeadSHA != checkpointEvent.HeadSHA || preDraftDetail.PublicationCheckpoints[0].VerificationProfileSHA256 != checkpointEvent.VerificationProfileSHA256 || preDraftDetail.PublicationCheckpoints[0].VerificationOutputBytes != 42 || preDraftDetail.Attempts[0].PullRequestURL != "" {
		t.Fatalf("pre-Draft detail invented publication: detail=%#v error=%v", preDraftDetail, err)
	}
	completionEvent := domain.AgentTaskAdapterEvent{AttemptID: completionAttempt.ID, AdapterJobID: completionJobID, DeliveryID: "adapter-completed-44", Kind: "completed", Summary: "Validated patch and draft PR are ready for human review.", BranchName: completionTask.ExecutionBranch, HeadSHA: "0123456789abcdef0123456789abcdef01234567", PullRequestURL: "https://github.com/RainLib/open-review-platform/pull/44", PullRequestNumber: 44, PatchSHA256: strings.Repeat("a", 64), ChangedFileCount: 2, DiffBytes: 512, VerificationProfileSHA256: checkpointEvent.VerificationProfileSHA256, VerificationOutputSHA256: checkpointEvent.VerificationOutputSHA256, VerificationOutputBytes: checkpointEvent.VerificationOutputBytes}
	invalidCompletion := completionEvent
	invalidCompletion.DeliveryID = "adapter-invalid-completion-44"
	invalidCompletion.PullRequestURL = "https://github.com/other/repository/pull/44"
	if _, _, err := postgres.RecordAgentTaskAdapterEvent(ctx, invalidCompletion, time.Minute); !errors.Is(err, ErrInvalidAgentTask) {
		t.Fatalf("wrong-repository completion error=%v, want invalid agent task", err)
	}
	changedPatchCompletion := completionEvent
	changedPatchCompletion.DeliveryID = "adapter-changed-patch-completion-44"
	changedPatchCompletion.PatchSHA256 = strings.Repeat("b", 64)
	if _, _, err := postgres.RecordAgentTaskAdapterEvent(ctx, changedPatchCompletion, time.Minute); !errors.Is(err, ErrInvalidAgentTask) {
		t.Fatalf("completion changed after retained pre-push evidence: error=%v", err)
	}
	changedVerificationCompletion := completionEvent
	changedVerificationCompletion.DeliveryID = "adapter-changed-verification-completion-44"
	changedVerificationCompletion.VerificationOutputSHA256 = strings.Repeat("e", 64)
	if _, _, err := postgres.RecordAgentTaskAdapterEvent(ctx, changedVerificationCompletion, time.Minute); !errors.Is(err, ErrInvalidAgentTask) {
		t.Fatalf("completion changed retained verification evidence: error=%v", err)
	}
	var invalidReceiptCount int
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM agent_task_adapter_events WHERE attempt_id=$1 AND delivery_id=$2`, completionAttempt.ID, invalidCompletion.DeliveryID).Scan(&invalidReceiptCount); err != nil || invalidReceiptCount != 0 {
		t.Fatalf("invalid completion receipt count=%d error=%v, want transaction rollback", invalidReceiptCount, err)
	}
	var attemptState string
	if err := postgres.pool.QueryRow(ctx, `SELECT state FROM agent_task_attempts WHERE id=$1`, completionAttempt.ID).Scan(&attemptState); err != nil || attemptState != "running" {
		t.Fatalf("attempt after invalid completion state=%q error=%v, want running", attemptState, err)
	}
	completedAttempt, duplicate, err := postgres.RecordAgentTaskAdapterEvent(ctx, completionEvent, time.Minute)
	if err != nil || duplicate || completedAttempt.State != "succeeded" || completedAttempt.PullRequestURL != completionEvent.PullRequestURL || completedAttempt.PatchSHA256 != completionEvent.PatchSHA256 || completedAttempt.ChangedFileCount != 2 || completedAttempt.DiffBytes != 512 || completedAttempt.VerificationProfileSHA256 != completionEvent.VerificationProfileSHA256 || completedAttempt.VerificationOutputSHA256 != completionEvent.VerificationOutputSHA256 || completedAttempt.VerificationOutputBytes != 42 {
		t.Fatalf("adapter completion=%#v duplicate=%v error=%v", completedAttempt, duplicate, err)
	}
	var completedComment string
	if err := postgres.pool.QueryRow(ctx, `SELECT payload->>'body' FROM outbox_messages WHERE aggregate_id=$1 AND topic='review.interaction.response' AND dedupe_key=$2`, completionAttempt.ID, "agent-task-attempt:"+completionAttempt.ID.String()+":completed").Scan(&completedComment); err != nil || !strings.Contains(completedComment, completionEvent.PatchSHA256) || !strings.Contains(completedComment, completionEvent.PullRequestURL) || !strings.Contains(completedComment, completionEvent.VerificationProfileSHA256) || !strings.Contains(completedComment, "not product acceptance") {
		t.Fatalf("completed Issue comment omitted durable patch and Draft evidence: body=%q error=%v", completedComment, err)
	}
	if duplicatedAttempt, duplicate, err := postgres.RecordAgentTaskAdapterEvent(ctx, completionEvent, time.Minute); err != nil || !duplicate || duplicatedAttempt.ID != completionAttempt.ID {
		t.Fatalf("duplicate adapter completion=%#v duplicate=%v error=%v", duplicatedAttempt, duplicate, err)
	}
	if _, _, err := postgres.RecordAgentTaskAdapterEvent(ctx, checkpointEvent, time.Minute); !errors.Is(err, ErrAgentTaskClaimLost) {
		t.Fatalf("stale checkpoint replay after completion error=%v, want lost lease", err)
	}
	completionDetail, err := postgres.GetAgentTask(ctx, "owner", tenantSlug, completionTask.ID)
	if err != nil || completionDetail.Task.State != "completed" || len(completionDetail.Attempts) != 1 || completionDetail.Attempts[0].State != "succeeded" {
		t.Fatalf("completed adapter task detail=%#v error=%v", completionDetail, err)
	}
	// A completed Agent attempt does not bypass the ordinary Draft policy.
	// With the default review_drafts=false, the provider delivery is audited
	// but does not create a review. Opting in admits the next exact delivery.
	draftEvent := domain.InboundEvent{
		Provider: domain.ProviderGitHub, APIBaseURL: input.APIBaseURL,
		DeliveryID: "agent-task-review-" + tenantID.String() + "-skipped", EventName: "pull_request",
		InstallationExternalID: installationID.String(), Repository: input.Repository,
		CloneURL: "https://github.com/RainLib/open-review-platform.git", ReviewNumber: 44,
		BaseRef: "main", BaseSHA: "1123456789abcdef0123456789abcdef01234567",
		HeadRef: completionTask.ExecutionBranch, HeadSHA: completionEvent.HeadSHA,
		Payload: json.RawMessage(`{}`), ReceivedAt: time.Now().UTC(), Action: "opened",
		IsDraft: true, Author: "agent",
	}
	if skipped, duplicate, err := postgres.Enqueue(ctx, draftEvent); err != nil || duplicate || skipped.State != domain.JobCancelled || skipped.ID != uuid.Nil {
		t.Fatalf("default Draft review admission=%#v duplicate=%v error=%v", skipped, duplicate, err)
	}
	completionDetail, err = postgres.GetAgentTask(ctx, "owner", tenantSlug, completionTask.ID)
	if err != nil || len(completionDetail.LinkedReviews) != 0 {
		t.Fatalf("skipped Draft must not appear as Agent review: links=%#v error=%v", completionDetail.LinkedReviews, err)
	}
	if _, err := postgres.SaveReviewConfig(ctx, "owner", tenantSlug, domain.ReviewConfigInput{
		Section: domain.ReviewConfigGeneral, ScopeKind: domain.ReviewConfigTenantScope,
		ExpectedRevision: 0, Content: json.RawMessage(`{"automatic_review":true,"review_drafts":true,"rereview_on_push":true}`),
	}); err != nil {
		t.Fatalf("enable normal Draft review policy: %v", err)
	}
	draftEvent.DeliveryID = "agent-task-review-" + tenantID.String() + "-accepted"
	admittedJob, duplicate, err := postgres.Enqueue(ctx, draftEvent)
	if err != nil || duplicate || admittedJob.ID == uuid.Nil || admittedJob.State != domain.JobQueued {
		t.Fatalf("opted-in Draft review admission=%#v duplicate=%v error=%v", admittedJob, duplicate, err)
	}
	// Keep the append-only usage ledger intact, but retire this fixture's
	// claimable review job when the test ends. Claim fairness integration tests
	// run against the same isolated database and must not pick up this job.
	t.Cleanup(func() {
		_, _ = postgres.pool.Exec(context.Background(), `UPDATE review_jobs SET state='cancelled' WHERE id=$1 AND state='queued'`, admittedJob.ID)
	})
	var linkedRunID, requestID uuid.UUID
	if err := postgres.pool.QueryRow(ctx, `SELECT run.id,run.request_id FROM review_runs run WHERE run.legacy_job_id=$1`, admittedJob.ID).Scan(&linkedRunID, &requestID); err != nil {
		t.Fatalf("load admitted Agent Draft review run: %v", err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO review_merge_gate_decisions(run_id,enabled,threshold,conclusion,blocking_findings,finding_count,configuration_content_sha256,origin_scope_kind,origin_revision,evaluation_version)
		VALUES($1,true,'high','failure',1,2,$2,'repository',1,'integration-v1')`, linkedRunID, strings.Repeat("a", 64)); err != nil {
		t.Fatalf("seed exact-head Agent review gate: %v", err)
	}
	// The task read model must link only reviews of the exact published head
	// and frozen Agent branch, never another run for the same PR number.
	seedAgentReview := func(suffix, headRef, headSHA string) uuid.UUID {
		t.Helper()
		deliveryID, jobID, runID := uuid.New(), uuid.New(), uuid.New()
		if _, err := postgres.pool.Exec(ctx, `INSERT INTO webhook_deliveries(id,provider,delivery_id,event_name,payload) VALUES($1,'github',$2,'pull_request','{}'::jsonb)`, deliveryID, "agent-task-review-"+tenantID.String()+"-"+suffix); err != nil {
			t.Fatalf("seed Agent review delivery: %v", err)
		}
		if _, err := postgres.pool.Exec(ctx, `INSERT INTO review_jobs(id,tenant_id,installation_id,delivery_id,provider,api_base_url,repository,clone_url,review_number,base_ref,base_sha,head_ref,head_sha,state) VALUES($1,$2,$3,$4,'github','https://api.github.com','RainLib/open-review-platform','https://github.com/RainLib/open-review-platform.git',44,'main','1123456789abcdef0123456789abcdef01234567',$5,$6,'succeeded')`, jobID, tenantID, installationID, deliveryID, headRef, headSHA); err != nil {
			t.Fatalf("seed Agent review job: %v", err)
		}
		if _, err := postgres.pool.Exec(ctx, `INSERT INTO review_runs(id,request_id,legacy_job_id,state,trigger_kind,head_sha,base_sha) VALUES($1,$2,$3,'completed','pull_request',$4,'1123456789abcdef0123456789abcdef01234567')`, runID, requestID, jobID, headSHA); err != nil {
			t.Fatalf("seed Agent review run: %v", err)
		}
		return runID
	}
	seedAgentReview("other-head", completionTask.ExecutionBranch, "1123456789abcdef0123456789abcdef01234567")
	seedAgentReview("other-branch", "feature/unrelated", completionEvent.HeadSHA)
	completionDetail, err = postgres.GetAgentTask(ctx, "owner", tenantSlug, completionTask.ID)
	if err != nil || len(completionDetail.LinkedReviews) != 1 || completionDetail.LinkedReviews[0].RunID != linkedRunID || completionDetail.LinkedReviews[0].ReviewNumber != 44 {
		t.Fatalf("exact-head Agent review links=%#v error=%v", completionDetail.LinkedReviews, err)
	}
	if gate := completionDetail.LinkedReviews[0].MergeGate; gate == nil || !gate.Enabled || gate.Threshold != "high" || gate.Conclusion != "failure" || gate.BlockingFindings != 1 || gate.FindingCount != 2 {
		t.Fatalf("exact-head Agent review gate=%#v; a different head or branch must not supply the conclusion", gate)
	}
	// A later repository-policy edit must not widen the execution envelope of
	// this already approved task family when feedback creates a child task.
	if completionTask.MaxAttempts != 1 || completionTask.MaxExecutionSeconds != 1800 || completionTask.MaxFeedbackCycles != 2 {
		t.Fatalf("unexpected frozen parent feedback budget: %#v", completionTask)
	}
	policy, err = postgres.SaveAgentTaskPolicy(ctx, "owner", tenantSlug, domain.AgentTaskPolicyInput{Provider: input.Provider, APIBaseURL: input.APIBaseURL, Repository: input.Repository, Mode: "manual", MaxAttempts: 3, MaxExecutionSeconds: 7200, MaxFeedbackCycles: 3, ExecutorProfile: "codex", AutoAdmissionEnabled: true, AutoAdmissionLabel: "openreview:implement", Revision: policy.Revision})
	if err != nil {
		t.Fatalf("widen policy for future independent tasks: %v", err)
	}
	feedbackEvent := domain.AgentTaskFeedbackEvent{Provider: domain.ProviderGitHub, APIBaseURL: input.APIBaseURL, DeliveryID: "agent-task-feedback-" + tenantID.String(), InstallationExternalID: installationID.String(), Repository: input.Repository, PullRequestNumber: 44, CommentExternalID: "91", ActorExternalID: "55", Instruction: "Handle the missing retry result without widening the Draft PR scope, then add a focused regression test."}
	feedback, err := postgres.ProcessAgentTaskFeedback(ctx, feedbackEvent)
	if err != nil || !feedback.Accepted || feedback.TaskID == nil {
		t.Fatalf("feedback admission=%#v error=%v", feedback, err)
	}
	var feedbackID uuid.UUID
	var feedbackSourceJobs int
	if err := postgres.pool.QueryRow(ctx, `SELECT id FROM agent_task_feedback_cycles WHERE provider=$1 AND provider_delivery_id=$2`, feedbackEvent.Provider, feedbackEvent.DeliveryID).Scan(&feedbackID); err != nil {
		t.Fatal(err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM outbox_messages WHERE aggregate_id=$1 AND topic='agent.task.source.resolve.requested'`, *feedback.TaskID).Scan(&feedbackSourceJobs); err != nil || feedbackSourceJobs != 0 {
		t.Fatalf("feedback source queued before acknowledgement: count=%d error=%v", feedbackSourceJobs, err)
	}
	if err := postgres.WithAgentTaskAcknowledgementFence(ctx, domain.InteractionResponse{TenantID: tenantID, Provider: feedbackEvent.Provider, APIBaseURL: feedbackEvent.APIBaseURL, InstallationExternalID: feedbackEvent.InstallationExternalID, CredentialRef: "test-only", Repository: feedbackEvent.Repository, ResourceKind: "merge_request", ReviewNumber: feedbackEvent.PullRequestNumber, Marker: "open-review-platform:agent-task-feedback:" + feedbackID.String(), SourceRelease: &domain.AgentTaskSourceRelease{TaskID: *feedback.TaskID, Revision: 1}}, func(context.Context) error { return nil }); err != nil {
		t.Fatalf("release acknowledged feedback source: %v", err)
	}
	if duplicateFeedback, err := postgres.ProcessAgentTaskFeedback(ctx, feedbackEvent); err != nil || !duplicateFeedback.Duplicate || duplicateFeedback.TaskID == nil || *duplicateFeedback.TaskID != *feedback.TaskID {
		t.Fatalf("feedback duplicate=%#v error=%v", duplicateFeedback, err)
	}
	feedbackDetail, err := postgres.GetAgentTask(ctx, "owner", tenantSlug, *feedback.TaskID)
	if err != nil || feedbackDetail.Task.OriginKind != "pull_request" || feedbackDetail.Task.OriginRevision != completionEvent.HeadSHA || feedbackDetail.Task.ExecutionBranch != completionTask.ExecutionBranch || feedbackDetail.Task.FeedbackCycle != 1 || feedbackDetail.Task.MaxAttempts != completionTask.MaxAttempts || feedbackDetail.Task.MaxExecutionSeconds != completionTask.MaxExecutionSeconds || feedbackDetail.Task.MaxFeedbackCycles != completionTask.MaxFeedbackCycles || feedbackDetail.Task.SourceState != "pending" {
		t.Fatalf("feedback child task=%#v error=%v", feedbackDetail.Task, err)
	}
	if feedbackDetail.TargetBranch != "main" || feedbackDetail.Task.SourceBaseRef == "main" {
		t.Fatalf("feedback review target=%q source branch=%q, want frozen main separate from Draft head", feedbackDetail.TargetBranch, feedbackDetail.Task.SourceBaseRef)
	}
	if feedbackDetail.Feedback == nil || feedbackDetail.Feedback.CommentExternalID != feedbackEvent.CommentExternalID || feedbackDetail.Feedback.ActorExternalID != feedbackEvent.ActorExternalID {
		t.Fatalf("feedback read model lost admitted provider comment: %#v", feedbackDetail.Feedback)
	}
	feedbackTarget, err := postgres.LoadAgentTaskSourceTarget(ctx, feedbackDetail.Task.ID)
	if err != nil || feedbackTarget.Feedback == nil || feedbackTarget.Feedback.CommentExternalID != feedbackEvent.CommentExternalID || feedbackTarget.Feedback.ActorExternalID != feedbackEvent.ActorExternalID || feedbackTarget.Feedback.InstructionSHA256 == "" {
		t.Fatalf("feedback source binding=%#v error=%v", feedbackTarget.Feedback, err)
	}
	if err := postgres.FailAgentTaskSourceSnapshot(ctx, feedbackDetail.Task.ID, "agent_source_snapshot_unavailable", "The provider did not return the Draft PR head."); err != nil {
		t.Fatalf("fail feedback source capture: %v", err)
	}
	failedFeedback, err := postgres.GetAgentTask(ctx, "owner", tenantSlug, feedbackDetail.Task.ID)
	if err != nil || failedFeedback.Task.SourceState != "failed" {
		t.Fatalf("failed feedback source=%#v error=%v", failedFeedback.Task, err)
	}
	if _, err := postgres.RetryAgentTaskSource(ctx, "reviewer", tenantSlug, feedbackDetail.Task.ID, failedFeedback.Task.Revision); err != nil {
		t.Fatalf("retry feedback source: %v", err)
	}
	feedbackSnapshot := domain.AgentTaskSourceSnapshot{BaseRef: completionTask.ExecutionBranch, BaseSHA: completionEvent.HeadSHA, TargetBranch: "main", Feedback: &domain.AgentTaskFeedbackSnapshot{CommentExternalID: feedbackEvent.CommentExternalID, ActorExternalID: feedbackEvent.ActorExternalID, Instruction: feedbackEvent.Instruction}}
	if _, err := postgres.RecordAgentTaskSourceSnapshot(ctx, feedbackDetail.Task.ID, domain.AgentTaskSourceSnapshot{BaseRef: completionTask.ExecutionBranch, BaseSHA: completionEvent.HeadSHA}); !errors.Is(err, ErrInvalidAgentTask) {
		t.Fatalf("feedback without provider-read comment evidence error=%v, want invalid", err)
	}
	changedFeedback := feedbackSnapshot
	changedFeedback.Feedback = &domain.AgentTaskFeedbackSnapshot{CommentExternalID: feedbackEvent.CommentExternalID, ActorExternalID: feedbackEvent.ActorExternalID, Instruction: feedbackEvent.Instruction + " edited"}
	if _, err := postgres.RecordAgentTaskSourceSnapshot(ctx, feedbackDetail.Task.ID, changedFeedback); !errors.Is(err, ErrInvalidAgentTask) {
		t.Fatalf("edited feedback evidence error=%v, want invalid", err)
	}
	changedFeedback = feedbackSnapshot
	changedFeedback.BaseSHA = "ffffffffffffffffffffffffffffffffffffffff"
	if _, err := postgres.RecordAgentTaskSourceSnapshot(ctx, feedbackDetail.Task.ID, changedFeedback); !errors.Is(err, ErrInvalidAgentTask) {
		t.Fatalf("changed Draft head evidence error=%v, want invalid", err)
	}
	changedFeedback = feedbackSnapshot
	changedFeedback.TargetBranch = "release"
	if _, err := postgres.RecordAgentTaskSourceSnapshot(ctx, feedbackDetail.Task.ID, changedFeedback); !errors.Is(err, ErrInvalidAgentTask) {
		t.Fatalf("changed Draft target evidence error=%v, want invalid", err)
	}
	feedbackTask, err := postgres.RecordAgentTaskSourceSnapshot(ctx, feedbackDetail.Task.ID, feedbackSnapshot)
	if err != nil {
		t.Fatalf("freeze feedback source: %v", err)
	}
	feedbackDetail, err = postgres.GetAgentTask(ctx, "owner", tenantSlug, feedbackTask.ID)
	if err != nil || feedbackDetail.TargetBranch != "main" || feedbackDetail.Task.SourceBaseRef != completionTask.ExecutionBranch {
		t.Fatalf("feedback review target=%q source branch=%q error=%v, want frozen main separate from Draft head", feedbackDetail.TargetBranch, feedbackDetail.Task.SourceBaseRef, err)
	}
	var feedbackSourceMessages, feedbackSourceMarkers int
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*),count(DISTINCT payload->>'marker') FROM outbox_messages WHERE aggregate_id=$1 AND topic='review.interaction.response' AND payload->>'marker' LIKE 'open-review-platform:agent-task-feedback-status:%:source:failed'`, feedbackTask.ID).Scan(&feedbackSourceMessages, &feedbackSourceMarkers); err != nil || feedbackSourceMessages != 2 || feedbackSourceMarkers != 1 {
		t.Fatalf("feedback source updates=%d markers=%d error=%v", feedbackSourceMessages, feedbackSourceMarkers, err)
	}
	feedbackPlan, err := postgres.CreateAgentTaskPlan(ctx, "reviewer", tenantSlug, feedbackTask.ID, domain.AgentTaskPlanInput{Summary: "Address only the verified Draft PR review feedback, preserve the existing agent branch, and add focused regression coverage before updating the same Draft PR."})
	if err != nil {
		t.Fatalf("create feedback plan: %v", err)
	}
	var feedbackPlanBody, feedbackPlanResource string
	if err := postgres.pool.QueryRow(ctx, `SELECT payload->>'body',payload->>'resource_kind' FROM outbox_messages WHERE aggregate_id=$1 AND topic='review.interaction.response' AND payload->>'marker'=$2`, feedbackTask.ID, "open-review-platform:agent-task-feedback-status:"+feedbackTask.ID.String()+":plan:1").Scan(&feedbackPlanBody, &feedbackPlanResource); err != nil || feedbackPlanResource != "merge_request" || !strings.Contains(feedbackPlanBody, feedbackPlan.PlanSHA256) || strings.Contains(feedbackPlanBody, "@openreview approve") {
		t.Fatalf("feedback plan publication body=%q resource=%q error=%v", feedbackPlanBody, feedbackPlanResource, err)
	}
	if _, err = postgres.ApproveAgentTaskPlan(ctx, "owner", tenantSlug, feedbackTask.ID, feedbackPlan.ID, domain.AgentTaskPlanApprovalInput{Revision: feedbackPlan.Revision}); err != nil {
		t.Fatalf("approve feedback plan: %v", err)
	}
	var feedbackApprovedBody, feedbackApprovedResource string
	if err := postgres.pool.QueryRow(ctx, `SELECT payload->>'body',payload->>'resource_kind' FROM outbox_messages WHERE aggregate_id=$1 AND topic='review.interaction.response' AND payload->>'marker'=$2`, feedbackTask.ID, "open-review-platform:agent-task-feedback-status:"+feedbackTask.ID.String()+":approved:1").Scan(&feedbackApprovedBody, &feedbackApprovedResource); err != nil || feedbackApprovedResource != "merge_request" || !strings.Contains(feedbackApprovedBody, "Execution is **queued**") || strings.Contains(feedbackApprovedBody, "@openreview approve") {
		t.Fatalf("approved feedback status body=%q resource=%q error=%v", feedbackApprovedBody, feedbackApprovedResource, err)
	}
	approvedFeedback, err := postgres.GetAgentTask(ctx, "owner", tenantSlug, feedbackTask.ID)
	if err != nil {
		t.Fatalf("load approved feedback task: %v", err)
	}
	feedbackAttempt, err := postgres.ClaimAgentTaskAttempt(ctx, "feedback-fixture-worker", domain.AgentTaskExecutionRequest{TaskID: feedbackTask.ID, TaskRevision: approvedFeedback.Task.Revision, PlanID: feedbackPlan.ID, PlanRevision: feedbackPlan.Revision, PlanSHA256: feedbackPlan.PlanSHA256}, time.Minute)
	if err != nil || feedbackAttempt == nil {
		t.Fatalf("claim approved feedback attempt=%#v error=%v", feedbackAttempt, err)
	}
	feedbackHandoff, err := postgres.LoadAgentTaskAttemptTarget(ctx, feedbackAttempt.ID, "feedback-fixture-worker")
	if err != nil || feedbackHandoff.Feedback == nil || feedbackHandoff.Feedback.CommentExternalID != feedbackEvent.CommentExternalID || feedbackHandoff.Feedback.ActorExternalID != feedbackEvent.ActorExternalID || feedbackHandoff.Feedback.TargetBranch != "main" || !feedbackHandoff.Feedback.ExecutionValid() {
		t.Fatalf("approved feedback handoff=%#v error=%v", feedbackHandoff.Feedback, err)
	}
	var feedbackResponseKind string
	if err = postgres.pool.QueryRow(ctx, `SELECT payload->>'resource_kind' FROM outbox_messages WHERE aggregate_id=(SELECT id FROM agent_task_feedback_cycles WHERE child_task_id=$1)`, feedbackTask.ID).Scan(&feedbackResponseKind); err != nil || feedbackResponseKind != "merge_request" {
		t.Fatalf("feedback provider response kind=%q error=%v", feedbackResponseKind, err)
	}
	cancellationCommandEvent := commandEvent
	cancellationCommandEvent.DeliveryID = "agent-task-cancellation-command-" + tenantID.String()
	cancellationCommandEvent.IssueNumber = 45
	cancellationCommandEvent.IssueRevision = "issue-45-revision"
	cancellationCommandEvent.CommentExternalID = "101"
	cancellationCommandEvent.IssueTitle = "Stop an approved adapter task"
	cancellationCommandEvent.IssueBody = "Observed behavior: an approved sandbox task needs an explicit operator stop path. Expected behavior: the local lease and remote adapter job are both cancelled. Acceptance criteria: a durable cancellation event identifies only the exact attempt and adapter job."
	cancellationCommandEvent.IssueRevision = domain.AgentIssueRevision(cancellationCommandEvent.Provider, cancellationCommandEvent.APIBaseURL, cancellationCommandEvent.Repository, cancellationCommandEvent.IssueNumber, cancellationCommandEvent.IssueTitle, cancellationCommandEvent.IssueBody)
	cancellationCommand, err := postgres.ProcessAgentTaskCommand(ctx, cancellationCommandEvent, "implement", "@openreview implement")
	if err != nil || !cancellationCommand.Accepted || cancellationCommand.TaskID == nil {
		t.Fatalf("create adapter-cancellation task outcome=%#v error=%v", cancellationCommand, err)
	}
	cancellationTaskDetail, err := postgres.GetAgentTask(ctx, "owner", tenantSlug, *cancellationCommand.TaskID)
	if err != nil {
		t.Fatalf("load adapter-cancellation task: %v", err)
	}
	cancellationTask := cancellationTaskDetail.Task
	cancellationTask, err = postgres.RecordAgentTaskSourceSnapshot(ctx, cancellationTask.ID, domain.AgentTaskSourceSnapshot{BaseRef: "main", BaseSHA: "1123456789abcdef0123456789abcdef01234567", Issue: &domain.AgentTaskIssueSnapshot{Title: cancellationCommandEvent.IssueTitle, Body: cancellationCommandEvent.IssueBody, Revision: cancellationCommandEvent.IssueRevision}})
	if err != nil {
		t.Fatalf("freeze adapter-cancellation source: %v", err)
	}
	cancellationPlan, err := postgres.CreateAgentTaskPlan(ctx, "reviewer", tenantSlug, cancellationTask.ID, domain.AgentTaskPlanInput{Summary: "Validate the stop request, terminate the exact sandbox attempt, and retain the cancellation receipt without creating or updating a pull request."})
	if err != nil {
		t.Fatalf("create adapter-cancellation plan: %v", err)
	}
	cancellationPlan, err = postgres.ApproveAgentTaskPlan(ctx, "owner", tenantSlug, cancellationTask.ID, cancellationPlan.ID, domain.AgentTaskPlanApprovalInput{Revision: cancellationPlan.Revision})
	if err != nil {
		t.Fatalf("approve adapter-cancellation plan: %v", err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT revision FROM agent_tasks WHERE id=$1`, cancellationTask.ID).Scan(&cancellationTask.Revision); err != nil {
		t.Fatalf("load adapter-cancellation task revision: %v", err)
	}
	cancellationAttempt, err := postgres.ClaimAgentTaskAttempt(ctx, "adapter-cancel-worker", domain.AgentTaskExecutionRequest{TaskID: cancellationTask.ID, TaskRevision: cancellationTask.Revision, PlanID: cancellationPlan.ID, PlanRevision: cancellationPlan.Revision, PlanSHA256: cancellationPlan.PlanSHA256}, time.Minute)
	if err != nil {
		t.Fatalf("claim adapter-cancellation attempt: %v", err)
	}
	expectedCancellationJobID := "adapter-job-cancel-45-" + tenantID.String()
	if err := postgres.AttachAgentTaskAdapterJob(ctx, cancellationAttempt.ID, "adapter-cancel-worker", expectedCancellationJobID); err != nil {
		t.Fatalf("attach cancellation adapter job: %v", err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT revision FROM agent_tasks WHERE id=$1`, cancellationTask.ID).Scan(&cancellationTask.Revision); err != nil {
		t.Fatalf("load executing task revision before cancellation: %v", err)
	}
	cancelledTask, err := postgres.CancelAgentTask(ctx, "owner", tenantSlug, cancellationTask.ID, domain.AgentTaskCancellationInput{Revision: cancellationTask.Revision, Reason: "Operator requested a safe stop before any provider write."})
	if err != nil || cancelledTask.State != "cancelled" {
		t.Fatalf("cancel adapter task=%#v error=%v", cancelledTask, err)
	}
	var cancellationTopic, cancellationAttemptID, cancellationJobID string
	if err := postgres.pool.QueryRow(ctx, `SELECT topic,payload->>'attempt_id',payload->>'adapter_job_id' FROM outbox_messages WHERE aggregate_id=$1 AND topic='agent.task.cancel.requested'`, cancellationTask.ID).Scan(&cancellationTopic, &cancellationAttemptID, &cancellationJobID); err != nil || cancellationTopic != "agent.task.cancel.requested" || cancellationAttemptID != cancellationAttempt.ID.String() || cancellationJobID != expectedCancellationJobID {
		t.Fatalf("adapter cancellation dispatch topic=%q attempt=%q job=%q error=%v", cancellationTopic, cancellationAttemptID, cancellationJobID, err)
	}
	cancellationDetail, err := postgres.GetAgentTask(ctx, "owner", tenantSlug, cancellationTask.ID)
	if err != nil || len(cancellationDetail.Attempts) != 1 || cancellationDetail.Attempts[0].State != "superseded" {
		t.Fatalf("cancelled adapter task detail=%#v error=%v", cancellationDetail, err)
	}
	expiryCommandEvent := cancellationCommandEvent
	expiryCommandEvent.DeliveryID = "agent-task-expiry-command-" + tenantID.String()
	expiryCommandEvent.IssueNumber = 46
	expiryCommandEvent.IssueRevision = "issue-46-revision"
	expiryCommandEvent.CommentExternalID = "102"
	expiryCommandEvent.IssueTitle = "Surface a lost adapter heartbeat"
	expiryCommandEvent.IssueBody = "Observed behavior: a sandbox that stops heartbeating can leave a task running forever. Expected behavior: an expired lease becomes needs attention and sends a bounded remote stop. Acceptance criteria: the exact attempt is terminal and the cancellation outbox references its adapter job."
	expiryCommandEvent.IssueRevision = domain.AgentIssueRevision(expiryCommandEvent.Provider, expiryCommandEvent.APIBaseURL, expiryCommandEvent.Repository, expiryCommandEvent.IssueNumber, expiryCommandEvent.IssueTitle, expiryCommandEvent.IssueBody)
	expiryCommand, err := postgres.ProcessAgentTaskCommand(ctx, expiryCommandEvent, "implement", "@openreview implement")
	if err != nil || !expiryCommand.Accepted || expiryCommand.TaskID == nil {
		t.Fatalf("create adapter-expiry task outcome=%#v error=%v", expiryCommand, err)
	}
	expiryTaskDetail, err := postgres.GetAgentTask(ctx, "owner", tenantSlug, *expiryCommand.TaskID)
	if err != nil {
		t.Fatalf("load adapter-expiry task: %v", err)
	}
	expiryTask := expiryTaskDetail.Task
	expiryTask, err = postgres.RecordAgentTaskSourceSnapshot(ctx, expiryTask.ID, domain.AgentTaskSourceSnapshot{BaseRef: "main", BaseSHA: "1123456789abcdef0123456789abcdef01234567", Issue: &domain.AgentTaskIssueSnapshot{Title: expiryCommandEvent.IssueTitle, Body: expiryCommandEvent.IssueBody, Revision: expiryCommandEvent.IssueRevision}})
	if err != nil {
		t.Fatalf("freeze adapter-expiry source: %v", err)
	}
	expiryPlan, err := postgres.CreateAgentTaskPlan(ctx, "reviewer", tenantSlug, expiryTask.ID, domain.AgentTaskPlanInput{Summary: "Observe the isolated runner heartbeat, stop the exact sandbox when its lease expires, and retain a clear operator recovery path without rerunning code."})
	if err != nil {
		t.Fatalf("create adapter-expiry plan: %v", err)
	}
	expiryPlan, err = postgres.ApproveAgentTaskPlan(ctx, "owner", tenantSlug, expiryTask.ID, expiryPlan.ID, domain.AgentTaskPlanApprovalInput{Revision: expiryPlan.Revision})
	if err != nil {
		t.Fatalf("approve adapter-expiry plan: %v", err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT revision FROM agent_tasks WHERE id=$1`, expiryTask.ID).Scan(&expiryTask.Revision); err != nil {
		t.Fatalf("load adapter-expiry task revision: %v", err)
	}
	expiryAttempt, err := postgres.ClaimAgentTaskAttempt(ctx, "adapter-expiry-worker", domain.AgentTaskExecutionRequest{TaskID: expiryTask.ID, TaskRevision: expiryTask.Revision, PlanID: expiryPlan.ID, PlanRevision: expiryPlan.Revision, PlanSHA256: expiryPlan.PlanSHA256}, time.Minute)
	if err != nil {
		t.Fatalf("claim adapter-expiry attempt: %v", err)
	}
	expiryJobID := "adapter-job-expiry-46-" + tenantID.String()
	if err := postgres.AttachAgentTaskAdapterJob(ctx, expiryAttempt.ID, "adapter-expiry-worker", expiryJobID); err != nil {
		t.Fatalf("attach expiry adapter job: %v", err)
	}
	if err := postgres.ClaimAgentTaskAdapterStart(ctx, expiryAttempt.ID, expiryJobID); err != nil {
		t.Fatalf("start expiry adapter job: %v", err)
	}
	expiryCheckpoint := domain.AgentTaskAdapterEvent{AttemptID: expiryAttempt.ID, AdapterJobID: expiryJobID, DeliveryID: "adapter-expiry-checkpoint-46", Kind: "publication_checkpoint", BranchName: expiryTask.ExecutionBranch, HeadSHA: strings.Repeat("b", 40), PatchSHA256: strings.Repeat("c", 64), ChangedFileCount: 1, DiffBytes: 80}
	if _, _, err := postgres.RecordAgentTaskAdapterEvent(ctx, expiryCheckpoint, time.Minute); err != nil {
		t.Fatalf("retain checkpoint before adapter lease expiry: %v", err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE agent_task_attempts SET locked_until=now()-interval '1 second' WHERE id=$1`, expiryAttempt.ID); err != nil {
		t.Fatalf("expire adapter attempt lease: %v", err)
	}
	if expired, err := postgres.ExpireAgentTaskAttempts(ctx, 25); err != nil || expired != 1 {
		t.Fatalf("expire agent attempts=%d error=%v", expired, err)
	}
	expiryDetail, err := postgres.GetAgentTask(ctx, "owner", tenantSlug, expiryTask.ID)
	if err != nil || expiryDetail.Task.State != "needs_attention" || len(expiryDetail.Attempts) != 1 || expiryDetail.Attempts[0].State != "needs_attention" || expiryDetail.Attempts[0].ErrorCode != "agent_adapter_lease_expired" || len(expiryDetail.PublicationCheckpoints) != 1 || expiryDetail.PublicationCheckpoints[0].HeadSHA != expiryCheckpoint.HeadSHA || expiryDetail.Attempts[0].PullRequestURL != "" {
		t.Fatalf("expired adapter task detail=%#v error=%v", expiryDetail, err)
	}
	var expiryCancellationAttemptID, expiryCancellationJobID string
	if err := postgres.pool.QueryRow(ctx, `SELECT payload->>'attempt_id',payload->>'adapter_job_id' FROM outbox_messages WHERE aggregate_id=$1 AND topic='agent.task.cancel.requested'`, expiryTask.ID).Scan(&expiryCancellationAttemptID, &expiryCancellationJobID); err != nil || expiryCancellationAttemptID != expiryAttempt.ID.String() || expiryCancellationJobID != expiryJobID {
		t.Fatalf("expiry adapter cancellation attempt=%q job=%q error=%v", expiryCancellationAttemptID, expiryCancellationJobID, err)
	}
	policies, err := postgres.ListAgentTaskPolicies(ctx, "reviewer", tenantSlug, 10)
	if err != nil || len(policies) != 1 || policies[0].Mode != "manual" {
		t.Fatalf("policies=%#v error=%v", policies, err)
	}
	modelPolicy, err := postgres.SaveAgentTaskPolicy(ctx, "owner", tenantSlug, domain.AgentTaskPolicyInput{Provider: input.Provider, APIBaseURL: input.APIBaseURL, Repository: input.Repository, Mode: "manual", DecisionBackend: "jev", Revision: policies[0].Revision})
	if err != nil || modelPolicy.DecisionBackend != "jev" {
		t.Fatalf("save model-backed policy=%#v error=%v", modelPolicy, err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT metadata->>'decision_backend' FROM audit_events WHERE tenant_id=$1 AND action='agent_task.policy_saved' AND target=$2 AND (metadata->>'revision')::int=$3`, tenantID, modelPolicy.ID.String(), modelPolicy.Revision).Scan(&auditedBackend); err != nil || auditedBackend != "jev" {
		t.Fatalf("updated policy decision backend audit=%q error=%v", auditedBackend, err)
	}
	modelCommandEvent := expiryCommandEvent
	modelCommandEvent.DeliveryID = "agent-task-model-command-" + tenantID.String()
	modelCommandEvent.IssueNumber = 47
	modelCommandEvent.CommentExternalID = "103"
	modelCommandEvent.IssueTitle = "Bound an Agent admission decision"
	modelCommandEvent.IssueBody = "Observed behavior: a model response could be treated as an execution grant. Expected behavior: the model can only narrow deterministic admission. Acceptance criteria: a model-backed task retains owner plan approval and rejects missing evidence."
	modelCommandEvent.IssueRevision = domain.AgentIssueRevision(modelCommandEvent.Provider, modelCommandEvent.APIBaseURL, modelCommandEvent.Repository, modelCommandEvent.IssueNumber, modelCommandEvent.IssueTitle, modelCommandEvent.IssueBody)
	modelOutcome, err := postgres.ProcessAgentTaskCommand(ctx, modelCommandEvent, "implement", "@openreview implement")
	if err != nil || modelOutcome.TaskID == nil {
		t.Fatalf("create model-backed candidate=%#v error=%v", modelOutcome, err)
	}
	releaseCommandSource(modelCommandEvent, *modelOutcome.TaskID)
	modelTask, err := postgres.GetAgentTask(ctx, "owner", tenantSlug, *modelOutcome.TaskID)
	if err != nil || modelTask.Task.DecisionBackend != "jev" {
		t.Fatalf("frozen model backend=%#v error=%v", modelTask.Task, err)
	}
	if err := postgres.FailAgentTaskSourceSnapshot(ctx, *modelOutcome.TaskID, "agent_decision_unavailable", "The decision service timed out before an admission verdict was recorded."); err != nil {
		t.Fatalf("fail model-backed source capture: %v", err)
	}
	failedSource, err := postgres.GetAgentTask(ctx, "owner", tenantSlug, *modelOutcome.TaskID)
	if err != nil || failedSource.Task.SourceState != "failed" || failedSource.Task.State != "needs_attention" {
		t.Fatalf("failed source task=%#v error=%v", failedSource.Task, err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE provider_installations SET repository_scope='Elsewhere/*' WHERE id=$1`, installationID); err != nil {
		t.Fatalf("revoke source repository scope: %v", err)
	}
	if _, err := postgres.RetryAgentTaskSource(ctx, "reviewer", tenantSlug, *modelOutcome.TaskID, failedSource.Task.Revision); !errors.Is(err, ErrForbidden) {
		t.Fatalf("out-of-scope source retry error=%v, want forbidden", err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE provider_installations SET repository_scope=$2,active=FALSE WHERE id=$1`, installationID, input.Repository); err != nil {
		t.Fatalf("deactivate source installation: %v", err)
	}
	if _, err := postgres.RetryAgentTaskSource(ctx, "reviewer", tenantSlug, *modelOutcome.TaskID, failedSource.Task.Revision); !errors.Is(err, ErrUnknownInstallation) {
		t.Fatalf("inactive installation source retry error=%v, want unavailable installation", err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE provider_installations SET active=TRUE WHERE id=$1`, installationID); err != nil {
		t.Fatalf("restore source installation: %v", err)
	}
	if _, err := postgres.RetryAgentTaskSource(ctx, "viewer", tenantSlug, *modelOutcome.TaskID, failedSource.Task.Revision); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer source retry error=%v, want forbidden", err)
	}
	if _, err := postgres.RetryAgentTaskSource(ctx, "reviewer", tenantSlug, *modelOutcome.TaskID, modelTask.Task.Revision); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale source retry error=%v, want revision conflict", err)
	}
	reopened, err := postgres.RetryAgentTaskSource(ctx, "reviewer", tenantSlug, *modelOutcome.TaskID, failedSource.Task.Revision)
	if err != nil || reopened.State != "received" || reopened.SourceState != "pending" || reopened.Revision != failedSource.Task.Revision+1 {
		t.Fatalf("reopened source task=%#v error=%v", reopened, err)
	}
	if _, err := postgres.RetryAgentTaskSource(ctx, "reviewer", tenantSlug, *modelOutcome.TaskID, failedSource.Task.Revision); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("duplicate source retry error=%v, want revision conflict", err)
	}
	var sourceMessages int
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM outbox_messages WHERE aggregate_id=$1 AND topic='agent.task.source.resolve.requested'`, *modelOutcome.TaskID).Scan(&sourceMessages); err != nil || sourceMessages != 2 {
		t.Fatalf("source retry outbox messages=%d error=%v", sourceMessages, err)
	}
	modelSnapshot := domain.AgentTaskSourceSnapshot{BaseRef: "main", BaseSHA: "1123456789abcdef0123456789abcdef01234567", Issue: &domain.AgentTaskIssueSnapshot{Title: modelCommandEvent.IssueTitle, Body: modelCommandEvent.IssueBody, Revision: modelCommandEvent.IssueRevision}}
	if _, err := postgres.RecordAgentTaskSourceSnapshot(ctx, *modelOutcome.TaskID, modelSnapshot); err == nil {
		t.Fatal("model-backed source admission accepted a missing model signal")
	}
	modelSnapshot.DecisionSignal = &domain.AgentTaskDecisionSignal{Backend: "jev", Model: "jev-latest", Choice: "plan", Confidence: 90}
	modelAccepted, err := postgres.RecordAgentTaskSourceSnapshot(ctx, *modelOutcome.TaskID, modelSnapshot)
	if err != nil || modelAccepted.SourceState != "ready" {
		t.Fatalf("record model-backed source=%#v error=%v", modelAccepted, err)
	}
	var sourceStatusMessages, sourceStatusMarkers, oldestStatusVersion, newestStatusVersion int
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*),count(DISTINCT payload->>'marker'),min((payload->>'status_version')::int),max((payload->>'status_version')::int) FROM outbox_messages WHERE aggregate_id=$1 AND topic='review.interaction.response' AND payload->>'marker' LIKE 'open-review-platform:agent-task-source:%'`, *modelOutcome.TaskID).Scan(&sourceStatusMessages, &sourceStatusMarkers, &oldestStatusVersion, &newestStatusVersion); err != nil || sourceStatusMessages != 2 || sourceStatusMarkers != 1 || oldestStatusVersion >= newestStatusVersion || newestStatusVersion != modelAccepted.Revision {
		t.Fatalf("source status updates=%d markers=%d versions=%d..%d error=%v", sourceStatusMessages, sourceStatusMarkers, oldestStatusVersion, newestStatusVersion, err)
	}
	if current, err := postgres.AgentTaskSourceStatusCurrent(ctx, *modelOutcome.TaskID, oldestStatusVersion); err != nil || current {
		t.Fatalf("superseded source precheck current=%t error=%v", current, err)
	}
	if current, err := postgres.AgentTaskSourceStatusCurrent(ctx, *modelOutcome.TaskID, newestStatusVersion); err != nil || !current {
		t.Fatalf("latest source precheck current=%t error=%v", current, err)
	}
	publicationCalls := 0
	if err := postgres.WithAgentTaskSourcePublicationFence(ctx, *modelOutcome.TaskID, oldestStatusVersion, func(context.Context) error {
		publicationCalls++
		return nil
	}); err != nil || publicationCalls != 0 {
		t.Fatalf("superseded source status published=%d error=%v", publicationCalls, err)
	}
	if err := postgres.WithAgentTaskSourcePublicationFence(ctx, *modelOutcome.TaskID, newestStatusVersion, func(context.Context) error {
		publicationCalls++
		return nil
	}); err != nil || publicationCalls != 1 {
		t.Fatalf("current source status published=%d error=%v", publicationCalls, err)
	}
	// Old durable envelopes lacked status_version; their stable dedupe key still
	// carries the source revision and must prevent a legacy replay from winning.
	if command, err := postgres.pool.Exec(ctx, `UPDATE outbox_messages SET payload=payload-'status_version' WHERE aggregate_id=$1 AND topic='review.interaction.response' AND payload->>'marker'=$2`, *modelOutcome.TaskID, "open-review-platform:agent-task-source:"+modelOutcome.TaskID.String()); err != nil || command.RowsAffected() != 2 {
		t.Fatalf("prepare legacy source envelopes rows=%d error=%v", command.RowsAffected(), err)
	}
	if current, err := postgres.AgentTaskSourceStatusCurrent(ctx, *modelOutcome.TaskID, 0); err != nil || current {
		t.Fatalf("legacy source precheck current=%t error=%v", current, err)
	}
	if err := postgres.WithAgentTaskSourcePublicationFence(ctx, *modelOutcome.TaskID, 0, func(context.Context) error {
		publicationCalls++
		return nil
	}); err != nil || publicationCalls != 1 {
		t.Fatalf("legacy source replay published=%d error=%v", publicationCalls, err)
	}
	firstEntered, releaseFirst := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(releaseFirst) })
	defer release()
	firstResult := make(chan error, 1)
	go func() {
		firstResult <- postgres.WithAgentTaskSourcePublicationFence(ctx, *modelOutcome.TaskID, newestStatusVersion, func(context.Context) error {
			close(firstEntered)
			<-releaseFirst
			return nil
		})
	}()
	select {
	case <-firstEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("first source publication did not obtain its task fence")
	}
	secondEntered := make(chan struct{})
	secondResult := make(chan error, 1)
	go func() {
		secondResult <- postgres.WithAgentTaskSourcePublicationFence(ctx, *modelOutcome.TaskID, newestStatusVersion, func(context.Context) error {
			close(secondEntered)
			return nil
		})
	}()
	select {
	case <-secondEntered:
		t.Fatal("concurrent source publication bypassed the task row fence")
	case <-time.After(150 * time.Millisecond):
	}
	release()
	for _, result := range []<-chan error{firstResult, secondResult} {
		select {
		case err := <-result:
			if err != nil {
				t.Fatalf("ordered source publication: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("ordered source publication did not finish")
		}
	}
	if _, err := postgres.RetryAgentTaskSource(ctx, "owner", tenantSlug, *modelOutcome.TaskID, modelAccepted.Revision); !errors.Is(err, ErrConflict) {
		t.Fatalf("ready source retry error=%v, want conflict", err)
	}
	modelDetail, err := postgres.GetAgentTask(ctx, "owner", tenantSlug, *modelOutcome.TaskID)
	if err != nil || len(modelDetail.Classifications) < 2 || modelDetail.Classifications[0].Decision != "requires_human" || !strings.Contains(modelDetail.Classifications[0].ClassifierVersion, "jev:jev-latest") {
		t.Fatalf("model-backed candidate skipped human gate: %#v error=%v", modelDetail, err)
	}
	retryEvent := modelCommandEvent
	retryEvent.DeliveryID = "agent-task-transient-decision-" + tenantID.String()
	retryEvent.IssueNumber = 48
	retryEvent.CommentExternalID = "104"
	retryEvent.IssueRevision = domain.AgentIssueRevision(retryEvent.Provider, retryEvent.APIBaseURL, retryEvent.Repository, retryEvent.IssueNumber, retryEvent.IssueTitle, retryEvent.IssueBody)
	retryOutcome, err := postgres.ProcessAgentTaskCommand(ctx, retryEvent, "implement", "@openreview implement")
	if err != nil || retryOutcome.TaskID == nil {
		t.Fatalf("create transient decision candidate=%#v error=%v", retryOutcome, err)
	}
	releaseCommandSource(retryEvent, *retryOutcome.TaskID)
	var firstMessageID uuid.UUID
	var firstRevision int
	if err := postgres.pool.QueryRow(ctx, `SELECT id,(payload->>'task_revision')::int FROM outbox_messages WHERE aggregate_id=$1 AND topic='agent.task.source.resolve.requested'`, *retryOutcome.TaskID).Scan(&firstMessageID, &firstRevision); err != nil || firstRevision != 1 {
		t.Fatalf("source message id=%s revision=%d error=%v", firstMessageID, firstRevision, err)
	}
	firstMessage := domain.OutboxMessage{ID: firstMessageID, AggregateID: *retryOutcome.TaskID, Topic: "agent.task.source.resolve.requested", Payload: map[string]any{"task_id": retryOutcome.TaskID.String(), "task_revision": float64(firstRevision)}}
	beforeRetry := time.Now()
	for range 2 {
		if scheduled, err := postgres.ScheduleAgentTaskSourceDecisionRetry(ctx, firstMessage, time.Minute); err != nil || !scheduled {
			t.Fatalf("schedule deduplicated model retry=%t error=%v", scheduled, err)
		}
	}
	var secondMessageID uuid.UUID
	var secondAttempt, secondRevision, secondCount int
	var secondAvailable time.Time
	if err := postgres.pool.QueryRow(ctx, `SELECT id,(payload->>'source_decision_attempt')::int,(payload->>'task_revision')::int,available_at FROM outbox_messages WHERE dedupe_key=$1`, "agent-task-source-decision-retry:"+firstMessageID.String()).Scan(&secondMessageID, &secondAttempt, &secondRevision, &secondAvailable); err != nil || secondAttempt != 2 || secondRevision != 1 || secondAvailable.Before(beforeRetry.Add(time.Minute-time.Second)) {
		t.Fatalf("delayed model retry id=%s attempt=%d revision=%d available=%s error=%v", secondMessageID, secondAttempt, secondRevision, secondAvailable, err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM outbox_messages WHERE aggregate_id=$1 AND topic='agent.task.source.resolve.requested'`, *retryOutcome.TaskID).Scan(&secondCount); err != nil || secondCount != 2 {
		t.Fatalf("duplicate model retry rows=%d error=%v", secondCount, err)
	}
	secondMessage := domain.OutboxMessage{ID: secondMessageID, AggregateID: *retryOutcome.TaskID, Topic: firstMessage.Topic, Payload: map[string]any{"task_id": retryOutcome.TaskID.String(), "task_revision": float64(1), "source_decision_attempt": float64(2)}}
	if scheduled, err := postgres.ScheduleAgentTaskSourceDecisionRetry(ctx, secondMessage, 0); err != nil || !scheduled {
		t.Fatalf("schedule final bounded model retry=%t error=%v", scheduled, err)
	}
	thirdMessage := domain.OutboxMessage{ID: uuid.New(), AggregateID: *retryOutcome.TaskID, Topic: firstMessage.Topic, Payload: map[string]any{"task_id": retryOutcome.TaskID.String(), "task_revision": float64(1), "source_decision_attempt": float64(3)}}
	if scheduled, err := postgres.ScheduleAgentTaskSourceDecisionRetry(ctx, thirdMessage, 0); err != nil || scheduled {
		t.Fatalf("exhausted model retry=%t error=%v", scheduled, err)
	}
	if _, ok := AgentSourceTaskRevision(map[string]any{"task_id": retryOutcome.TaskID.String()}); !ok {
		t.Fatal("legacy revision-1 source message was rejected")
	}
	if err := postgres.FailAgentTaskSourceSnapshot(ctx, *retryOutcome.TaskID, "agent_decision_unavailable", "The bounded decision backend retries were exhausted."); err != nil {
		t.Fatalf("fail exhausted model task: %v", err)
	}
	if scheduled, err := postgres.ScheduleAgentTaskSourceDecisionRetry(ctx, domain.OutboxMessage{ID: uuid.New(), AggregateID: *retryOutcome.TaskID, Topic: firstMessage.Topic, Payload: firstMessage.Payload}, 0); err != nil || !scheduled {
		t.Fatalf("terminal source retry no-op=%t error=%v", scheduled, err)
	}
	var finalSourceMessages int
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM outbox_messages WHERE aggregate_id=$1 AND topic='agent.task.source.resolve.requested'`, *retryOutcome.TaskID).Scan(&finalSourceMessages); err != nil || finalSourceMessages != 3 {
		t.Fatalf("bounded source retry rows=%d error=%v", finalSourceMessages, err)
	}
	providerPolicy, err := postgres.SaveAgentTaskPolicy(ctx, "owner", tenantSlug, domain.AgentTaskPolicyInput{Provider: input.Provider, APIBaseURL: input.APIBaseURL, Repository: input.Repository, Mode: "manual", DecisionBackend: "deterministic", Revision: modelPolicy.Revision})
	if err != nil || providerPolicy.DecisionBackend != "deterministic" {
		t.Fatalf("save deterministic source policy=%#v error=%v", providerPolicy, err)
	}
	providerEvent := modelCommandEvent
	providerEvent.DeliveryID = "agent-task-provider-retry-" + tenantID.String()
	providerEvent.IssueNumber = 49
	providerEvent.CommentExternalID = "105"
	providerEvent.IssueRevision = domain.AgentIssueRevision(providerEvent.Provider, providerEvent.APIBaseURL, providerEvent.Repository, providerEvent.IssueNumber, providerEvent.IssueTitle, providerEvent.IssueBody)
	providerOutcome, err := postgres.ProcessAgentTaskCommand(ctx, providerEvent, "implement", "@openreview implement")
	if err != nil || providerOutcome.TaskID == nil {
		t.Fatalf("create deterministic provider-read candidate=%#v error=%v", providerOutcome, err)
	}
	releaseCommandSource(providerEvent, *providerOutcome.TaskID)
	var providerMessageID uuid.UUID
	if err := postgres.pool.QueryRow(ctx, `SELECT id FROM outbox_messages WHERE aggregate_id=$1 AND topic='agent.task.source.resolve.requested'`, *providerOutcome.TaskID).Scan(&providerMessageID); err != nil {
		t.Fatal(err)
	}
	providerMessage := domain.OutboxMessage{ID: providerMessageID, AggregateID: *providerOutcome.TaskID, Topic: "agent.task.source.resolve.requested", Payload: map[string]any{"task_id": providerOutcome.TaskID.String(), "task_revision": float64(1)}}
	if scheduled, err := postgres.ScheduleAgentTaskSourceRetry(ctx, providerMessage, 12*time.Second); err != nil || !scheduled {
		t.Fatalf("deterministic provider retry scheduled=%t error=%v", scheduled, err)
	}
	var providerRetryCount int
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM outbox_messages WHERE aggregate_id=$1 AND topic='agent.task.source.resolve.requested'`, *providerOutcome.TaskID).Scan(&providerRetryCount); err != nil || providerRetryCount != 2 {
		t.Fatalf("deterministic provider retry rows=%d error=%v", providerRetryCount, err)
	}
}
