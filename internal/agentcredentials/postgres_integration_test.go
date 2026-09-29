package agentcredentials

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/agentadapter"
	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// This exercises the signed broker against the real task/approval/lease store.
// It uses only a disposable database and a synthetic GitLab coding token; it
// never contacts a Git provider or makes a real credential available to a CLI.
func TestGitLabBrokerRequiresApprovedStartedTaskInPostgres(t *testing.T) {
	databaseURL := os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL")
	if databaseURL == "" || os.Getenv("OPEN_REVIEW_TEST_ISOLATED_DATABASE") != "true" {
		t.Skip("requires an isolated PostgreSQL database")
	}
	ctx := context.Background()
	postgres, err := store.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer postgres.Close()
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	tenantID, installationID := uuid.New(), uuid.New()
	tenantSlug := "gitlab-broker-" + tenantID.String()[:8]
	const repository = "group/coding-test"
	provider := newGitLabProjectIdentityFixture(t, repository, 41, "project_41_bot_coding", false)
	defer provider.Close()
	apiBaseURL := provider.URL + "/api/v4"
	if _, err := conn.Exec(ctx, `INSERT INTO tenants(id,slug,name) VALUES($1,$2,'GitLab broker integration')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO memberships(tenant_id,subject,role,active) VALUES($1,'owner','owner',TRUE)`, tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO provider_installations(id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref,verification_state) VALUES($1,$2,'gitlab','review-only-installation',$3,$4,'review-only-reference','verified')`, installationID, tenantID, repository, apiBaseURL); err != nil {
		t.Fatal(err)
	}
	issue := domain.AgentTaskIssueSnapshot{
		Title: "Retry state is lost after a worker restart",
		Body:  "Observed behavior: a retry duplicates work after a worker restart. Expected behavior: one result. Acceptance criteria: a regression test proves the state survives a restart.",
	}
	input := domain.AgentTaskInput{Provider: domain.ProviderGitLab, APIBaseURL: apiBaseURL, Repository: repository, OriginKind: "issue", OriginNumber: 17, Intent: "implement"}
	input.OriginRevision = domain.AgentIssueRevision(input.Provider, input.APIBaseURL, input.Repository, input.OriginNumber, issue.Title, issue.Body)
	issue.Revision = input.OriginRevision
	if _, err := postgres.SaveAgentTaskPolicy(ctx, "owner", tenantSlug, domain.AgentTaskPolicyInput{Provider: input.Provider, APIBaseURL: apiBaseURL, Repository: repository, Mode: "manual", DecisionBackend: "jev"}); err != nil {
		t.Fatal(err)
	}
	task, err := postgres.CreateAgentTask(ctx, "owner", tenantSlug, input)
	if err != nil || task.DecisionBackend != "jev" {
		t.Fatalf("create Jev-backed task: task=%#v err=%v", task, err)
	}

	path := filepath.Join(t.TempDir(), "gitlab-coding.json")
	const token = "synthetic-gitlab-coding-token"
	writeGitLabCodingMap(t, path, installationID, apiBaseURL, repository, provider.URL, token)
	secret := strings.Repeat("b", 32)
	server := httptest.NewServer((Service{Secret: secret, Store: postgres, Issuer: GitLabIssuer{Credentials: agentadapter.FileCredentialSource{Path: path}, HTTPClient: provider.Client()}}).Handler())
	defer server.Close()
	source, err := NewSource(server.URL, secret, true)
	if err != nil {
		t.Fatal(err)
	}
	scope := agentadapter.RepositoryCredentialScope{AttemptID: uuid.New(), AdapterJobID: "gitlab-adapter-job", InstallationID: installationID, Provider: input.Provider, APIBaseURL: apiBaseURL, Repository: repository}
	assertDenied := func(stage string) {
		t.Helper()
		if credential, err := source.Resolve(ctx, scope); err == nil || credential.Token != "" {
			t.Fatalf("%s unexpectedly received a coding credential", stage)
		}
	}
	assertDenied("before source admission")
	baseSHA := strings.Repeat("1", 40)
	snapshot := domain.AgentTaskSourceSnapshot{BaseRef: "main", BaseSHA: baseSHA, Issue: &issue}
	if _, err := postgres.RecordAgentTaskSourceSnapshot(ctx, task.ID, snapshot); err == nil {
		t.Fatal("Jev-backed task admitted without model evidence")
	}
	snapshot.DecisionSignal = &domain.AgentTaskDecisionSignal{Backend: "jev", Model: "jev-latest", Choice: "plan", Confidence: 90}
	if _, err := postgres.RecordAgentTaskSourceSnapshot(ctx, task.ID, snapshot); err != nil {
		t.Fatal(err)
	}
	plan, err := postgres.CreateAgentTaskPlan(ctx, "owner", tenantSlug, task.ID, domain.AgentTaskPlanInput{Summary: "Fix only the bounded worker retry state and add focused regression coverage."})
	if err != nil {
		t.Fatal(err)
	}
	assertDenied("before owner approval")
	approved, err := postgres.ApproveAgentTaskPlan(ctx, "owner", tenantSlug, task.ID, plan.ID, domain.AgentTaskPlanApprovalInput{Revision: plan.Revision})
	if err != nil {
		t.Fatal(err)
	}
	detail, err := postgres.GetAgentTask(ctx, "owner", tenantSlug, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := postgres.ClaimAgentTaskAttempt(ctx, "broker-integration-worker", domain.AgentTaskExecutionRequest{TaskID: task.ID, TaskRevision: detail.Task.Revision, PlanID: approved.ID, PlanRevision: approved.Revision, PlanSHA256: approved.PlanSHA256}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	scope.AttemptID = attempt.ID
	assertDenied("before adapter job attachment")
	if err := postgres.AttachAgentTaskAdapterJob(ctx, attempt.ID, "broker-integration-worker", scope.AdapterJobID); err != nil {
		t.Fatal(err)
	}
	assertDenied("before adapter start")
	if err := postgres.ClaimAgentTaskAdapterStart(ctx, attempt.ID, scope.AdapterJobID); err != nil {
		t.Fatal(err)
	}
	wrong := scope
	wrong.Repository = "group/other"
	if credential, err := source.Resolve(ctx, wrong); err == nil || credential.Token != "" {
		t.Fatal("changed repository received a coding credential")
	}
	credential, err := source.Resolve(ctx, scope)
	if err != nil || credential.Token != token || credential.CloneBaseURL != provider.URL {
		t.Fatalf("approved started task received no exact credential: token-match=%t clone=%q err=%v", credential.Token == token, credential.CloneBaseURL, err)
	}
	var state, issuedRepository string
	if err := conn.QueryRow(ctx, `SELECT state,repository FROM agent_task_credential_issuances WHERE attempt_id=$1`, attempt.ID).Scan(&state, &issuedRepository); err != nil || state != "issued" || issuedRepository != repository {
		t.Fatalf("credential issuance audit state=%q repository=%q err=%v", state, issuedRepository, err)
	}
	detail, err = postgres.GetAgentTask(ctx, "owner", tenantSlug, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.CancelAgentTask(ctx, "owner", tenantSlug, task.ID, domain.AgentTaskCancellationInput{Revision: detail.Task.Revision, Reason: "Stop synthetic integration task"}); err != nil {
		t.Fatal(err)
	}
	assertDenied("after task cancellation")
}
