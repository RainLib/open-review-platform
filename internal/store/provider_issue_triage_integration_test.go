package store

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestProviderIssueAnalysisAcknowledgesBeforeModelWorkAndSupersedesEdits(t *testing.T) {
	databaseURL := os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("OPEN_REVIEW_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	postgres, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer postgres.Close()
	tenantID, installationID, configurationID := uuid.New(), uuid.New(), uuid.New()
	modelRouteJSON, _ := json.Marshal(domain.ModelRouteConfig{
		Enabled: true, Provider: "openai-compatible", Protocol: "openai-chat",
		BaseURL: "https://models.example/v1/chat/completions", Model: "deepseek-v4-flash",
		CredentialRef: "env://OPEN_REVIEW_MODEL_SECRET_PRIMARY", Effort: "low",
		MaxPromptTokens: 8000, TokenBudget: 128000, SubtaskTimeoutMinutes: 2, MaxConcurrentRuns: 2,
	})
	batch := &pgx.Batch{}
	batch.Queue("INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Issue triage')", tenantID, "issue-triage-"+tenantID.String()[:8])
	batch.Queue("INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner','owner')", tenantID)
	batch.Queue("INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'viewer','viewer')", tenantID)
	batch.Queue("INSERT INTO provider_actor_mappings (tenant_id,provider,external_id,subject) VALUES ($1,'github','triage-provider-owner','owner')", tenantID)
	batch.Queue("INSERT INTO provider_installations (id,tenant_id,provider,external_id,repository_scope,automatic_reviews,api_base_url,credential_ref,verification_state) VALUES ($1,$2,'github',$3,'RainLib/*',TRUE,'https://api.github.com','github-app','verified')", installationID, tenantID, "triage-install-"+tenantID.String())
	batch.Queue("INSERT INTO review_configurations (id,tenant_id,section,scope_kind,scope_ref,active) VALUES ($1,$2,'models','tenant','',TRUE)", configurationID, tenantID)
	batch.Queue("INSERT INTO review_configuration_versions (configuration_id,revision,content,content_sha256,created_by) VALUES ($1,1,$2::jsonb,$3,'owner')", configurationID, modelRouteJSON, "route-sha")
	if err := postgres.pool.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), "DELETE FROM tenants WHERE id=$1", tenantID) }()

	event := domain.ProviderIssueEvent{
		Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com", DeliveryID: "triage-" + uuid.NewString(), EventName: "issues",
		InstallationExternalID: "triage-install-" + tenantID.String(), Repository: "RainLib/open-review-platform", IssueNumber: 9,
		Action: "opened", Title: "Retries lose state", Body: "Steps", Author: "alice", Labels: []string{"bug"}, Payload: json.RawMessage("{}"), ReceivedAt: time.Now().UTC(),
	}
	outcome, err := postgres.EnqueueProviderIssueAnalysis(ctx, event)
	if err != nil || outcome.Skipped || outcome.Duplicate || outcome.Job.Revision != 1 {
		t.Fatalf("outcome=%#v error=%v", outcome, err)
	}
	loaded, err := postgres.ProviderIssueAnalysis(ctx, outcome.Job.ID, 1)
	if err != nil || loaded.TenantSlug != "issue-triage-"+tenantID.String()[:8] {
		t.Fatalf("trusted Issue analysis workspace=%q error=%v", loaded.TenantSlug, err)
	}
	var receiptRevision int
	if err := postgres.pool.QueryRow(ctx, `SELECT revision FROM provider_issue_analysis_receipts WHERE job_id=$1`, outcome.Job.ID).Scan(&receiptRevision); err != nil || receiptRevision != 1 {
		t.Fatalf("initial receipt revision=%d error=%v", receiptRevision, err)
	}
	var topic string
	if err := postgres.pool.QueryRow(ctx, "SELECT topic FROM outbox_messages WHERE aggregate_id=$1 AND topic='provider.issue.acknowledge'", outcome.Job.ID).Scan(&topic); err != nil {
		t.Fatal(err)
	}
	if err := postgres.MarkProviderIssueAcknowledged(ctx, outcome.Job.ID, 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := postgres.pool.QueryRow(ctx, "SELECT topic FROM outbox_messages WHERE aggregate_id=$1 AND topic='provider.issue.analyze'", outcome.Job.ID).Scan(&topic); err != nil {
		t.Fatal(err)
	}

	publicationEntered := make(chan struct{})
	releasePublication := make(chan struct{})
	publicationDone := make(chan error, 1)
	go func() {
		publicationDone <- postgres.WithCurrentProviderIssuePublication(ctx, outcome.Job.ID, 1, 1, domain.ProviderIssueAnalysisAcknowledged, "", func(context.Context) error {
			close(publicationEntered)
			<-releasePublication
			return nil
		})
	}()
	select {
	case <-publicationEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("current revision did not enter its publication fence")
	}
	event.DeliveryID = "triage-edit-" + uuid.NewString()
	event.Action, event.Body = "edited", "Updated reproduction"
	type editResult struct {
		outcome domain.ProviderIssueAnalysisEnqueue
		err     error
	}
	editDone := make(chan editResult, 1)
	go func() {
		outcome, err := postgres.EnqueueProviderIssueAnalysis(ctx, event)
		editDone <- editResult{outcome: outcome, err: err}
	}()
	select {
	case result := <-editDone:
		t.Fatalf("Issue edit overtook the in-flight provider publication: %#v %v", result.outcome, result.err)
	case <-time.After(100 * time.Millisecond):
	}
	close(releasePublication)
	if err := <-publicationDone; err != nil {
		t.Fatal(err)
	}
	result := <-editDone
	edited, err := result.outcome, result.err
	if err != nil || edited.Job.ID != outcome.Job.ID || edited.Job.Revision != 2 || edited.Job.StableMarker != outcome.Job.StableMarker {
		t.Fatalf("edited=%#v error=%v", edited, err)
	}
	stalePublished := false
	if err := postgres.WithCurrentProviderIssuePublication(ctx, outcome.Job.ID, 1, 1, domain.ProviderIssueAnalysisAcknowledged, "", func(context.Context) error {
		stalePublished = true
		return nil
	}); err != ErrNotFound || stalePublished {
		t.Fatalf("stale revision published after edit: callback=%v error=%v", stalePublished, err)
	}
	var receiptCount, latestReceiptRevision int
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*),max(revision) FROM provider_issue_analysis_receipts WHERE job_id=$1`, outcome.Job.ID).Scan(&receiptCount, &latestReceiptRevision); err != nil || receiptCount != 2 || latestReceiptRevision != 2 {
		t.Fatalf("receipt count=%d latest revision=%d error=%v", receiptCount, latestReceiptRevision, err)
	}
	if _, err := postgres.ProviderIssueAnalysis(ctx, outcome.Job.ID, 1); err != ErrNotFound {
		t.Fatalf("stale revision error=%v, want ErrNotFound", err)
	}
	if err := postgres.MarkProviderIssueAcknowledged(ctx, edited.Job.ID, 2, 1); err != nil {
		t.Fatal(err)
	}
	staged, err := postgres.StageProviderIssueAnalysis(ctx, edited.Job.ID, 2, 1, "## Outcome\nA retry can lose state.")
	if err != nil {
		t.Fatal(err)
	}
	if repeated, err := postgres.StageProviderIssueAnalysis(ctx, edited.Job.ID, 2, 1, "late competing result"); err != nil || repeated != staged {
		t.Fatalf("staged analysis was overwritten: %q, %v", repeated, err)
	}
	stagedDetail, err := postgres.GetProviderIssueAnalysis(ctx, "owner", "issue-triage-"+tenantID.String()[:8], edited.Job.ID)
	if err != nil || stagedDetail.State != domain.ProviderIssueAnalysisAcknowledged || stagedDetail.Analysis != "" {
		t.Fatalf("unpublished analysis leaked as a completed result: %#v %v", stagedDetail, err)
	}
	if err := postgres.CompleteProviderIssueAnalysis(ctx, edited.Job.ID, 2, 1, staged); err != nil {
		t.Fatal(err)
	}
	if err := postgres.CompleteProviderIssueAnalysis(ctx, edited.Job.ID, 2, 1, "late overwrite"); err != ErrNotFound {
		t.Fatalf("terminal completion overwritten: %v", err)
	}
	poll, err := postgres.ClaimProviderIssueFeedbackPoll(ctx, "feedback-poller-test", time.Minute)
	if err != nil || poll.Job.ID != edited.Job.ID || poll.Job.StableMarker != edited.Job.StableMarker {
		t.Fatalf("feedback poll=%#v error=%v", poll, err)
	}
	polledReactionOne, polledReactionTwo := "polled-reaction-"+uuid.NewString(), "polled-reaction-"+uuid.NewString()
	observedAt := time.Now().UTC()
	if err := postgres.CompleteProviderIssueFeedbackPoll(ctx, *poll, []domain.ProviderIssueFeedbackReaction{
		{ExternalID: polledReactionOne, ActorExternalID: "71", Kind: "useful"},
		{ExternalID: polledReactionTwo, ActorExternalID: "72", Kind: "not_useful"},
	}, observedAt, observedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE provider_issue_feedback_polls SET available_at=now() WHERE job_id=$1`, edited.Job.ID); err != nil {
		t.Fatal(err)
	}
	poll, err = postgres.ClaimProviderIssueFeedbackPoll(ctx, "feedback-poller-test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	observedAt = time.Now().UTC()
	if err := postgres.CompleteProviderIssueFeedbackPoll(ctx, *poll, []domain.ProviderIssueFeedbackReaction{
		{ExternalID: polledReactionOne, ActorExternalID: "71", Kind: "useful"},
	}, observedAt, observedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	useful := domain.ProviderIssueReaction{
		Provider: domain.ProviderGitHub, DeliveryID: "reaction-delivery-" + uuid.NewString(),
		ReactionExternalID: "provider-reaction-" + uuid.NewString(), ActorExternalID: "42",
		Repository: event.Repository, AnalysisMarker: edited.Job.StableMarker, Kind: "useful", Action: "created",
	}
	if err := postgres.RecordProviderIssueReaction(ctx, useful); err != nil {
		t.Fatal(err)
	}
	notUseful := useful
	notUseful.DeliveryID, notUseful.ReactionExternalID, notUseful.Kind = "reaction-delivery-"+uuid.NewString(), "provider-reaction-"+uuid.NewString(), "not_useful"
	if err := postgres.RecordProviderIssueReaction(ctx, notUseful); err != nil {
		t.Fatal(err)
	}
	notUseful.Action, notUseful.DeliveryID = "deleted", "reaction-delivery-"+uuid.NewString()
	if err := postgres.RecordProviderIssueReaction(ctx, notUseful); err != nil {
		t.Fatal(err)
	}
	tenantSlug := "issue-triage-" + tenantID.String()[:8]
	page, err := postgres.ListProviderIssueAnalyses(ctx, "owner", tenantSlug, domain.ProviderIssueAnalysisFilter{State: domain.ProviderIssueAnalysisCompleted, Query: "Retries", Limit: 25})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != outcome.Job.ID || page.Items[0].ReceiptCount != 2 || page.Counts.Completed != 1 {
		t.Fatalf("management page=%#v error=%v", page, err)
	}
	detail, err := postgres.GetProviderIssueAnalysis(ctx, "owner", tenantSlug, outcome.Job.ID)
	if err != nil || detail.Analysis == "" || len(detail.Receipts) != 2 || detail.Receipts[0].Revision != 2 || detail.UsefulCount != 2 || detail.NotUsefulCount != 0 {
		t.Fatalf("management detail=%#v error=%v", detail, err)
	}
	if detail.AgentAdmission.PolicyMode != "disabled" || !detail.AgentAdmission.InstallationReady || detail.AgentAdmission.CanRequest || detail.AgentAdmission.ExistingTaskID != nil {
		t.Fatalf("disabled Agent admission=%#v", detail.AgentAdmission)
	}
	if _, err := postgres.GetProviderIssueAnalysis(ctx, "outsider", tenantSlug, outcome.Job.ID); err != ErrForbidden {
		t.Fatalf("outsider error=%v, want ErrForbidden", err)
	}
	if _, err := postgres.CreateAgentTaskFromProviderIssue(ctx, "owner", tenantSlug, outcome.Job.ID, 1); err != ErrRevisionConflict {
		t.Fatalf("stale Issue revision error=%v, want ErrRevisionConflict", err)
	}
	if _, err := postgres.CreateAgentTaskFromProviderIssue(ctx, "owner", tenantSlug, outcome.Job.ID, 2); err != ErrAgentTaskDisabled {
		t.Fatalf("disabled repository error=%v, want ErrAgentTaskDisabled", err)
	}
	if _, err := postgres.CreateAgentTaskFromProviderIssue(ctx, "outsider", tenantSlug, outcome.Job.ID, 2); err != ErrForbidden {
		t.Fatalf("outsider Agent request error=%v, want ErrForbidden", err)
	}
	if _, err := postgres.SaveAgentTaskPolicy(ctx, "owner", tenantSlug, domain.AgentTaskPolicyInput{
		Provider: domain.ProviderGitHub, APIBaseURL: event.APIBaseURL, Repository: event.Repository,
		Mode: "manual", DecisionBackend: "deterministic", AutoAdmissionEnabled: true, AutoAdmissionLabel: "bug",
	}); err != nil {
		t.Fatalf("enable manual Agent policy: %v", err)
	}
	detail, err = postgres.GetProviderIssueAnalysis(ctx, "owner", tenantSlug, outcome.Job.ID)
	if err != nil || detail.AgentAdmission.PolicyMode != "manual" || !detail.AgentAdmission.InstallationReady || !detail.AgentAdmission.CanRequest || detail.AgentAdmission.ExistingTaskID != nil {
		t.Fatalf("manual owner Agent admission=%#v error=%v", detail.AgentAdmission, err)
	}
	viewerDetail, err := postgres.GetProviderIssueAnalysis(ctx, "viewer", tenantSlug, outcome.Job.ID)
	if err != nil || viewerDetail.AgentAdmission.PolicyMode != "manual" || viewerDetail.AgentAdmission.CanRequest {
		t.Fatalf("viewer Agent admission=%#v error=%v", viewerDetail.AgentAdmission, err)
	}
	agentTask, err := postgres.CreateAgentTaskFromProviderIssue(ctx, "owner", tenantSlug, outcome.Job.ID, 2)
	wantRevision := domain.AgentIssueRevision(event.Provider, event.APIBaseURL, event.Repository, event.IssueNumber, event.Title, event.Body)
	if err != nil || agentTask.OriginRevision != wantRevision || agentTask.OriginNumber != event.IssueNumber || agentTask.InstallationID != installationID || agentTask.State != "received" {
		t.Fatalf("server-derived Agent task=%#v error=%v want revision=%s", agentTask, err, wantRevision)
	}
	detail, err = postgres.GetProviderIssueAnalysis(ctx, "owner", tenantSlug, outcome.Job.ID)
	if err != nil || detail.AgentAdmission.CanRequest || detail.AgentAdmission.ExistingTaskID == nil || *detail.AgentAdmission.ExistingTaskID != agentTask.ID || detail.AgentAdmission.ExistingTaskState != "received" {
		t.Fatalf("existing Agent task admission=%#v error=%v", detail.AgentAdmission, err)
	}
	if _, err := postgres.CreateAgentTaskFromProviderIssue(ctx, "owner", tenantSlug, outcome.Job.ID, 2); err != ErrConflict {
		t.Fatalf("duplicate Issue revision error=%v, want ErrConflict", err)
	}
	autoAfterManual := event
	autoAfterManual.DeliveryID = "triage-auto-after-manual-" + uuid.NewString()
	if result, err := postgres.ProcessAutomaticAgentTask(ctx, autoAfterManual); err != nil || !result.Accepted || result.TaskID == nil || *result.TaskID != agentTask.ID {
		t.Fatalf("automatic label must reuse the manual task: result=%#v error=%v", result, err)
	}
	otherInstallationID := uuid.New()
	if _, err := postgres.pool.Exec(ctx, `UPDATE provider_installations SET repository_scope='Elsewhere/*' WHERE id=$1`, installationID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO provider_installations (id,tenant_id,provider,external_id,repository_scope,automatic_reviews,api_base_url,credential_ref,verification_state) VALUES ($1,$2,'github',$3,$4,TRUE,'https://api.github.com','github-app','verified')`, otherInstallationID, tenantID, "replacement-"+otherInstallationID.String(), event.Repository); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.CreateAgentTaskFromProviderIssue(ctx, "owner", tenantSlug, outcome.Job.ID, 2); err != ErrUnknownInstallation {
		t.Fatalf("reassigned repository error=%v, want ErrUnknownInstallation", err)
	}
	detail, err = postgres.GetProviderIssueAnalysis(ctx, "owner", tenantSlug, outcome.Job.ID)
	if err != nil || detail.AgentAdmission.InstallationReady || detail.AgentAdmission.CanRequest {
		t.Fatalf("reassigned installation admission=%#v error=%v", detail.AgentAdmission, err)
	}
	if _, err := postgres.pool.Exec(ctx, `DELETE FROM provider_installations WHERE id=$1`, otherInstallationID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE provider_installations SET repository_scope='RainLib/*' WHERE id=$1`, installationID); err != nil {
		t.Fatal(err)
	}

	// A label-created task must be the same task that the detail page and
	// subsequent Issue commands observe. The plain and label-frozen hashes
	// differ by design; neither path may create a second coding branch.
	autoFirst := event
	autoFirst.IssueNumber = 10
	autoFirst.DeliveryID = "triage-auto-first-" + uuid.NewString()
	autoFirst.Title = "Retry state is lost"
	autoFirst.Body = "Observed: retry loses its lease. Expected: one durable result. Acceptance: a regression test proves lease recovery."
	autoAnalysis, err := postgres.EnqueueProviderIssueAnalysis(ctx, autoFirst)
	if err != nil || autoAnalysis.Skipped {
		t.Fatalf("enqueue automatic Issue analysis=%#v error=%v", autoAnalysis, err)
	}
	autoCandidate, err := postgres.ProcessAutomaticAgentTask(ctx, autoFirst)
	if err != nil || !autoCandidate.Accepted || autoCandidate.TaskID == nil {
		t.Fatalf("automatic-first candidate=%#v error=%v", autoCandidate, err)
	}
	autoDetail, err := postgres.GetProviderIssueAnalysis(ctx, "owner", tenantSlug, autoAnalysis.Job.ID)
	if err != nil || autoDetail.AgentAdmission.CanRequest || autoDetail.AgentAdmission.ExistingTaskID == nil || *autoDetail.AgentAdmission.ExistingTaskID != *autoCandidate.TaskID || len(autoDetail.AgentAdmission.ExistingTasks) != 1 {
		t.Fatalf("automatic candidate not linked from Issue detail: admission=%#v error=%v", autoDetail.AgentAdmission, err)
	}
	if _, err := postgres.CreateAgentTaskFromProviderIssue(ctx, "owner", tenantSlug, autoAnalysis.Job.ID, 1); err != ErrConflict {
		t.Fatalf("manual request beside automatic candidate error=%v, want ErrConflict", err)
	}
	if _, err := postgres.CreateAgentTask(ctx, "owner", tenantSlug, domain.AgentTaskInput{
		Provider: autoFirst.Provider, APIBaseURL: autoFirst.APIBaseURL, Repository: autoFirst.Repository,
		OriginKind: "issue", OriginNumber: autoFirst.IssueNumber,
		OriginRevision: domain.AgentIssueRevision(autoFirst.Provider, autoFirst.APIBaseURL, autoFirst.Repository, autoFirst.IssueNumber, autoFirst.Title, autoFirst.Body),
		Intent:         "implement",
	}); err != ErrConflict {
		t.Fatalf("direct request beside automatic candidate error=%v, want ErrConflict", err)
	}
	command := domain.AgentTaskCommandEvent{
		Provider: autoFirst.Provider, APIBaseURL: autoFirst.APIBaseURL, InstallationExternalID: autoFirst.InstallationExternalID,
		Repository: autoFirst.Repository, IssueNumber: autoFirst.IssueNumber,
		IssueRevision: domain.AgentIssueRevision(autoFirst.Provider, autoFirst.APIBaseURL, autoFirst.Repository, autoFirst.IssueNumber, autoFirst.Title, autoFirst.Body),
		IssueTitle:    autoFirst.Title, IssueBody: autoFirst.Body, IssueLabels: autoFirst.Labels,
		ActorExternalID: "triage-provider-owner", Body: "@openreview status",
	}
	for _, item := range []struct{ command, normalized, reason string }{
		{"status", "@openreview status", "agent task status"},
		{"implement", "@openreview implement", "agent task recorded"},
		{"approve", "@openreview approve " + strings.Repeat("a", 64), "the task is not awaiting plan approval"},
	} {
		command.DeliveryID, command.CommentExternalID = uuid.NewString(), uuid.NewString()
		result, err := postgres.ProcessAgentTaskCommand(ctx, command, item.command, item.normalized)
		if err != nil || result.Reason != item.reason || (result.Accepted && (result.TaskID == nil || *result.TaskID != *autoCandidate.TaskID)) {
			t.Fatalf("automatic task %s command=%#v error=%v", item.command, result, err)
		}
	}
	command.DeliveryID, command.CommentExternalID = uuid.NewString(), uuid.NewString()
	if result, err := postgres.ProcessAgentTaskCommand(ctx, command, "cancel", "@openreview cancel"); err != nil || !result.Accepted || result.TaskID == nil || *result.TaskID != *autoCandidate.TaskID {
		t.Fatalf("automatic candidate cancellation=%#v error=%v", result, err)
	}
	changedIssue := command
	changedIssue.DeliveryID, changedIssue.CommentExternalID = uuid.NewString(), uuid.NewString()
	changedIssue.IssueBody = "The Issue body changed after automatic admission."
	changedIssue.IssueRevision = domain.AgentIssueRevision(changedIssue.Provider, changedIssue.APIBaseURL, changedIssue.Repository, changedIssue.IssueNumber, changedIssue.IssueTitle, changedIssue.IssueBody)
	if result, err := postgres.ProcessAgentTaskCommand(ctx, changedIssue, "status", "@openreview status"); err != nil || result.Accepted || result.Reason != "there is no Agent task for this exact Issue revision" {
		t.Fatalf("changed Issue must not address an old automatic task: result=%#v error=%v", result, err)
	}
	var autoTaskCount int
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM agent_tasks WHERE tenant_id=$1 AND repository=$2 AND origin_kind='issue' AND origin_number=$3`, tenantID, autoFirst.Repository, autoFirst.IssueNumber).Scan(&autoTaskCount); err != nil || autoTaskCount != 1 {
		t.Fatalf("automatic Issue task count=%d error=%v", autoTaskCount, err)
	}
	// Older releases could create both revision variants. Their existence must
	// block another request without hiding the Issue's retained analysis or
	// arbitrarily linking one of the two tasks.
	historicalTaskID := uuid.New()
	plainRevision := domain.AgentIssueRevision(autoFirst.Provider, autoFirst.APIBaseURL, autoFirst.Repository, autoFirst.IssueNumber, autoFirst.Title, autoFirst.Body)
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO agent_tasks
		(id,tenant_id,installation_id,provider,api_base_url,repository,origin_kind,origin_number,origin_revision,intent,requested_by,execution_branch)
		SELECT $1,tenant_id,installation_id,provider,api_base_url,repository,origin_kind,origin_number,$2,intent,'historical-test',$3
		FROM agent_tasks WHERE id=$4`, historicalTaskID, plainRevision, "agent/"+historicalTaskID.String(), *autoCandidate.TaskID); err != nil {
		t.Fatal(err)
	}
	autoDetail, err = postgres.GetProviderIssueAnalysis(ctx, "owner", tenantSlug, autoAnalysis.Job.ID)
	if err != nil || autoDetail.ID != autoAnalysis.Job.ID || !autoDetail.AgentAdmission.ExistingTaskConflict || autoDetail.AgentAdmission.ExistingTaskID != nil || autoDetail.AgentAdmission.CanRequest || len(autoDetail.AgentAdmission.ExistingTasks) != 2 {
		t.Fatalf("historical task conflict must retain Issue analysis and fail closed: detail=%#v error=%v", autoDetail, err)
	}
	linkedTasks := map[uuid.UUID]bool{}
	for _, task := range autoDetail.AgentAdmission.ExistingTasks {
		linkedTasks[task.ID] = true
	}
	if !linkedTasks[*autoCandidate.TaskID] || !linkedTasks[historicalTaskID] {
		t.Fatalf("historical Issue tasks must both be discoverable: %#v", autoDetail.AgentAdmission.ExistingTasks)
	}
	withoutTriage := autoFirst
	withoutTriage.IssueNumber = 11
	withoutTriage.DeliveryID = "triage-absent-" + uuid.NewString()
	if result, err := postgres.ProcessAutomaticAgentTask(ctx, withoutTriage); err != nil || !result.Accepted {
		t.Fatalf("automatic candidate without retained triage=%#v error=%v", result, err)
	}
	if _, err := postgres.CreateAgentTask(ctx, "owner", tenantSlug, domain.AgentTaskInput{
		Provider: withoutTriage.Provider, APIBaseURL: withoutTriage.APIBaseURL, Repository: withoutTriage.Repository,
		OriginKind: "issue", OriginNumber: withoutTriage.IssueNumber,
		OriginRevision: domain.AgentIssueRevision(withoutTriage.Provider, withoutTriage.APIBaseURL, withoutTriage.Repository, withoutTriage.IssueNumber, withoutTriage.Title, withoutTriage.Body),
		Intent:         "implement",
	}); err != ErrConflict {
		t.Fatalf("direct request without labels must not duplicate an automatic candidate: %v", err)
	}

	// Keep the earlier completed Issue immutable. Exercise failure and retry
	// on a second, still-active Issue instead of rewriting a terminal result.
	retryJob := autoAnalysis.Job
	if err := postgres.MarkProviderIssueAcknowledged(ctx, retryJob.ID, retryJob.Revision, 1); err != nil {
		t.Fatal(err)
	}
	if err := postgres.FailProviderIssueAnalysis(ctx, retryJob.ID, retryJob.Revision, 1, "provider timeout"); err != nil {
		t.Fatal(err)
	}
	retryInput := domain.ProviderIssueAnalysisRetryInput{ExpectedRevision: retryJob.Revision, IdempotencyKey: "issue-retry:" + uuid.NewString()}
	retry, err := postgres.RetryProviderIssueAnalysis(ctx, "owner", tenantSlug, retryJob.ID, retryInput)
	if err != nil || retry.Attempt != 2 || retry.Revision != retryJob.Revision || retry.State != domain.ProviderIssueAnalysisQueued || retry.Replayed {
		t.Fatalf("retry=%#v error=%v", retry, err)
	}
	replayed, err := postgres.RetryProviderIssueAnalysis(ctx, "owner", tenantSlug, retryJob.ID, retryInput)
	if err != nil || !replayed.Replayed || replayed.Attempt != 2 || replayed.State != domain.ProviderIssueAnalysisQueued {
		t.Fatalf("replayed retry=%#v error=%v", replayed, err)
	}
	if _, err := postgres.RetryProviderIssueAnalysis(ctx, "owner", tenantSlug, retryJob.ID, domain.ProviderIssueAnalysisRetryInput{
		ExpectedRevision: retryJob.Revision,
		IdempotencyKey:   "issue-retry:" + uuid.NewString(),
	}); err != ErrConflict {
		t.Fatalf("concurrent retry error=%v, want ErrConflict", err)
	}

	retriedJob, err := postgres.ProviderIssueAnalysis(ctx, retryJob.ID, retryJob.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if retriedJob.AnalysisAttempt != 2 || retriedJob.State != domain.ProviderIssueAnalysisQueued || retriedJob.Body != retryJob.Body ||
		retriedJob.ModelRouteSHA256 != retryJob.ModelRouteSHA256 || retriedJob.PromptConfigSHA256 != retryJob.PromptConfigSHA256 ||
		retriedJob.IssueTriageConfigSHA256 != retryJob.IssueTriageConfigSHA256 {
		t.Fatalf("retry changed retained evidence: %#v", retriedJob)
	}
	var retryPayload map[string]any
	if err := postgres.pool.QueryRow(ctx, `
		SELECT payload FROM outbox_messages
		WHERE aggregate_id=$1 AND topic='provider.issue.acknowledge' AND payload->>'attempt'='2'`, retryJob.ID).Scan(&retryPayload); err != nil {
		t.Fatal(err)
	}
	if retryPayload["revision"] != float64(retryJob.Revision) || retryPayload["attempt"] != float64(2) {
		t.Fatalf("retry outbox payload=%#v", retryPayload)
	}
	var auditAttempt int
	if err := postgres.pool.QueryRow(ctx, `
		SELECT (metadata->>'analysis_attempt')::integer
		FROM audit_events
		WHERE tenant_id=$1 AND action='provider_issue.analysis_retry_requested' AND target=$2
		ORDER BY created_at DESC LIMIT 1`, tenantID, retryJob.ID.String()).Scan(&auditAttempt); err != nil || auditAttempt != 2 {
		t.Fatalf("retry audit attempt=%d error=%v", auditAttempt, err)
	}
	if err := postgres.MarkProviderIssueAcknowledged(ctx, retryJob.ID, retryJob.Revision, 1); err != nil {
		t.Fatal(err)
	}
	if err := postgres.CompleteProviderIssueAnalysis(ctx, retryJob.ID, retryJob.Revision, 1, "stale result"); err != ErrNotFound {
		t.Fatalf("stale completion error=%v, want ErrNotFound", err)
	}
	if _, err := postgres.StageProviderIssueAnalysis(ctx, retryJob.ID, retryJob.Revision, 1, "stale result"); err != ErrNotFound {
		t.Fatalf("stale staging error=%v, want ErrNotFound", err)
	}
	if err := postgres.FailProviderIssueAnalysis(ctx, retryJob.ID, retryJob.Revision, 1, "stale failure"); err != ErrNotFound {
		t.Fatalf("stale failure error=%v, want ErrNotFound", err)
	}
	active, err := postgres.ProviderIssueAnalysis(ctx, retryJob.ID, retryJob.Revision)
	if err != nil || active.AnalysisAttempt != 2 || active.State != domain.ProviderIssueAnalysisQueued || active.Analysis != "" || active.LastError != "" {
		t.Fatalf("stale attempt mutated job=%#v error=%v", active, err)
	}
}

func TestProviderIssueAnalysisRecoversOnlyTerminalCurrentDelivery(t *testing.T) {
	databaseURL := os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("OPEN_REVIEW_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	postgres, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer postgres.Close()
	tenantID, installationID, configurationID := uuid.New(), uuid.New(), uuid.New()
	tenantSlug := "issue-recovery-" + tenantID.String()[:8]
	modelRouteJSON, _ := json.Marshal(domain.ModelRouteConfig{
		Enabled: true, Provider: "openai-compatible", Protocol: "openai-chat",
		BaseURL: "https://models.example/v1/chat/completions", Model: "test-model",
		CredentialRef: "env://OPEN_REVIEW_MODEL_SECRET_PRIMARY", Effort: "low",
		MaxPromptTokens: 8000, TokenBudget: 128000, SubtaskTimeoutMinutes: 2, MaxConcurrentRuns: 2,
	})
	batch := &pgx.Batch{}
	batch.Queue("INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Issue recovery')", tenantID, tenantSlug)
	batch.Queue("INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner','owner')", tenantID)
	batch.Queue("INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'viewer','viewer')", tenantID)
	batch.Queue("INSERT INTO provider_installations (id,tenant_id,provider,external_id,repository_scope,automatic_reviews,api_base_url,credential_ref,verification_state) VALUES ($1,$2,'github',$3,'RainLib/*',TRUE,'https://api.github.com','github-app','verified')", installationID, tenantID, "triage-recovery-"+tenantID.String())
	batch.Queue("INSERT INTO review_configurations (id,tenant_id,section,scope_kind,scope_ref,active) VALUES ($1,$2,'models','tenant','',TRUE)", configurationID, tenantID)
	batch.Queue("INSERT INTO review_configuration_versions (configuration_id,revision,content,content_sha256,created_by) VALUES ($1,1,$2::jsonb,$3,'owner')", configurationID, modelRouteJSON, "route-sha")
	if err := postgres.pool.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), "DELETE FROM tenants WHERE id=$1", tenantID) }()

	event := domain.ProviderIssueEvent{
		Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com", DeliveryID: "recovery-" + uuid.NewString(), EventName: "issues",
		InstallationExternalID: "triage-recovery-" + tenantID.String(), Repository: "RainLib/open-review-platform", IssueNumber: 42,
		Action: "opened", Title: "Issue acknowledgement retry", Body: "Reproduction", Author: "alice", Labels: []string{},
		Payload: json.RawMessage("{}"), ReceivedAt: time.Now().UTC(),
	}
	outcome, err := postgres.EnqueueProviderIssueAnalysis(ctx, event)
	if err != nil || outcome.Skipped || outcome.Duplicate {
		t.Fatalf("enqueue=%#v error=%v", outcome, err)
	}
	jobID := outcome.Job.ID
	retryInput := domain.ProviderIssueAnalysisRetryInput{ExpectedRevision: 1, IdempotencyKey: "issue-retry:" + uuid.NewString()}
	if _, err := postgres.RetryProviderIssueAnalysis(ctx, "owner", tenantSlug, jobID, retryInput); err != ErrConflict {
		t.Fatalf("healthy queued delivery was retryable: %v", err)
	}
	var ackMessageID uuid.UUID
	if err := postgres.pool.QueryRow(ctx, `SELECT id FROM outbox_messages WHERE aggregate_id=$1 AND topic='provider.issue.acknowledge' AND payload->>'attempt'='1'`, jobID).Scan(&ackMessageID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO inbox_messages (consumer,message_id,state,attempt,last_error) VALUES ('provider-issue-triager-v1',$1,'released',5,'synthetic provider outage')`, ackMessageID); err != nil {
		t.Fatal(err)
	}
	detail, err := postgres.GetProviderIssueAnalysis(ctx, "owner", tenantSlug, jobID)
	if err != nil || detail.DeliveryFailure {
		t.Fatalf("transient delivery failure surfaced as terminal: %#v %v", detail, err)
	}
	page, err := postgres.ListProviderIssueAnalyses(ctx, "owner", tenantSlug, domain.ProviderIssueAnalysisFilter{Limit: 25})
	if err != nil || page.Counts.DeliveryFailures != 0 || len(page.Items) != 1 || page.Items[0].DeliveryFailure {
		t.Fatalf("transient delivery failure surfaced in list: %#v %v", page, err)
	}
	attention, err := postgres.ListProviderIssueAnalyses(ctx, "owner", tenantSlug, domain.ProviderIssueAnalysisFilter{State: domain.ProviderIssueAnalysisNeedsAttention, Limit: 25})
	if err != nil || len(attention.Items) != 0 {
		t.Fatalf("transient delivery appeared in attention filter: %#v %v", attention, err)
	}
	if _, err := postgres.RetryProviderIssueAnalysis(ctx, "owner", tenantSlug, jobID, retryInput); err != ErrConflict {
		t.Fatalf("transient delivery was retryable: %v", err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE provider_installations SET verification_state='failed' WHERE id=$1`, installationID); err != nil {
		t.Fatal(err)
	}
	page, err = postgres.ListProviderIssueAnalyses(ctx, "owner", tenantSlug, domain.ProviderIssueAnalysisFilter{Limit: 25})
	if err != nil || page.Counts.Queued != 1 || page.Counts.ConnectionBlocked != 1 || page.Counts.NeedsAttention != 1 ||
		page.Counts.DeliveryFailures != 0 || len(page.Items) != 1 || !page.Items[0].ConnectionBlocked || page.Items[0].DeliveryFailure {
		t.Fatalf("unverified connection did not block queued Issue analysis: %#v %v", page, err)
	}
	attention, err = postgres.ListProviderIssueAnalyses(ctx, "owner", tenantSlug, domain.ProviderIssueAnalysisFilter{State: domain.ProviderIssueAnalysisNeedsAttention, Limit: 25})
	if err != nil || len(attention.Items) != 1 || attention.Items[0].ID != jobID {
		t.Fatalf("connection-blocked Issue missing from attention filter: %#v %v", attention, err)
	}
	detail, err = postgres.GetProviderIssueAnalysis(ctx, "owner", tenantSlug, jobID)
	if err != nil || !detail.ConnectionBlocked || detail.RetryReadiness.CanRetry {
		t.Fatalf("connection-blocked Issue detail=%#v error=%v", detail, err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE provider_installations SET active=FALSE,verification_state='verified' WHERE id=$1`, installationID); err != nil {
		t.Fatal(err)
	}
	page, err = postgres.ListProviderIssueAnalyses(ctx, "owner", tenantSlug, domain.ProviderIssueAnalysisFilter{Limit: 25})
	if err != nil || page.Counts.ConnectionBlocked != 1 || page.Counts.NeedsAttention != 1 {
		t.Fatalf("inactive connection did not block queued Issue analysis: %#v %v", page, err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE provider_installations SET active=TRUE WHERE id=$1`, installationID); err != nil {
		t.Fatal(err)
	}
	page, err = postgres.ListProviderIssueAnalyses(ctx, "owner", tenantSlug, domain.ProviderIssueAnalysisFilter{Limit: 25})
	if err != nil || page.Counts.ConnectionBlocked != 0 || page.Counts.NeedsAttention != 0 || len(page.Items) != 1 || page.Items[0].ConnectionBlocked {
		t.Fatalf("verified connection did not release queued Issue analysis: %#v %v", page, err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE inbox_messages SET attempt=6 WHERE consumer='provider-issue-triager-v1' AND message_id=$1`, ackMessageID); err != nil {
		t.Fatal(err)
	}
	detail, err = postgres.GetProviderIssueAnalysis(ctx, "owner", tenantSlug, jobID)
	if err != nil || !detail.DeliveryFailure || detail.State != domain.ProviderIssueAnalysisQueued || !detail.RetryReadiness.CanRetry {
		t.Fatalf("terminal acknowledgement failure not surfaced: %#v %v", detail, err)
	}
	viewerDetail, err := postgres.GetProviderIssueAnalysis(ctx, "viewer", tenantSlug, jobID)
	if err != nil || viewerDetail.RetryReadiness.RoleAllowed || viewerDetail.RetryReadiness.CanRetry {
		t.Fatalf("viewer retry readiness=%#v error=%v", viewerDetail.RetryReadiness, err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE provider_installations SET verification_state='failed' WHERE id=$1`, installationID); err != nil {
		t.Fatal(err)
	}
	detail, err = postgres.GetProviderIssueAnalysis(ctx, "owner", tenantSlug, jobID)
	if err != nil || detail.RetryReadiness.InstallationReady || detail.RetryReadiness.CanRetry {
		t.Fatalf("failed connection retry readiness=%#v error=%v", detail.RetryReadiness, err)
	}
	if _, err := postgres.RetryProviderIssueAnalysis(ctx, "owner", tenantSlug, jobID, retryInput); err != ErrNotFound {
		t.Fatalf("failed connection retry error=%v, want ErrNotFound", err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE provider_installations SET verification_state='verified' WHERE id=$1`, installationID); err != nil {
		t.Fatal(err)
	}
	page, err = postgres.ListProviderIssueAnalyses(ctx, "owner", tenantSlug, domain.ProviderIssueAnalysisFilter{Limit: 25})
	if err != nil || page.Counts.Queued != 1 || page.Counts.DeliveryFailures != 1 || page.Counts.ConnectionBlocked != 0 || page.Counts.NeedsAttention != 1 || len(page.Items) != 1 || !page.Items[0].DeliveryFailure {
		t.Fatalf("terminal acknowledgement failure missing from list: %#v %v", page, err)
	}
	attention, err = postgres.ListProviderIssueAnalyses(ctx, "owner", tenantSlug, domain.ProviderIssueAnalysisFilter{State: domain.ProviderIssueAnalysisNeedsAttention, Limit: 25})
	if err != nil || len(attention.Items) != 1 || attention.Items[0].ID != jobID {
		t.Fatalf("terminal delivery missing from attention filter: %#v %v", attention, err)
	}
	if _, err := postgres.RetryProviderIssueAnalysis(ctx, "viewer", tenantSlug, jobID, retryInput); err != ErrForbidden {
		t.Fatalf("viewer retry error=%v, want ErrForbidden", err)
	}
	stale := retryInput
	stale.ExpectedRevision = 2
	if _, err := postgres.RetryProviderIssueAnalysis(ctx, "owner", tenantSlug, jobID, stale); err != ErrRevisionConflict {
		t.Fatalf("stale revision error=%v, want ErrRevisionConflict", err)
	}
	retry, err := postgres.RetryProviderIssueAnalysis(ctx, "owner", tenantSlug, jobID, retryInput)
	if err != nil || retry.Attempt != 2 || retry.State != domain.ProviderIssueAnalysisQueued {
		t.Fatalf("acknowledgement recovery=%#v error=%v", retry, err)
	}
	staleAttemptPublished := false
	if err := postgres.WithCurrentProviderIssuePublication(ctx, jobID, 1, 1, domain.ProviderIssueAnalysisQueued, "", func(context.Context) error {
		staleAttemptPublished = true
		return nil
	}); err != ErrNotFound || staleAttemptPublished {
		t.Fatalf("old delivery published over manual retry: callback=%v error=%v", staleAttemptPublished, err)
	}
	replayed, err := postgres.RetryProviderIssueAnalysis(ctx, "owner", tenantSlug, jobID, retryInput)
	if err != nil || !replayed.Replayed || replayed.Attempt != 2 {
		t.Fatalf("idempotent acknowledgement recovery=%#v error=%v", replayed, err)
	}
	detail, err = postgres.GetProviderIssueAnalysis(ctx, "owner", tenantSlug, jobID)
	if err != nil || detail.DeliveryFailure || detail.AnalysisAttempt != 2 {
		t.Fatalf("old delivery leaked into new attempt: %#v %v", detail, err)
	}
	page, err = postgres.ListProviderIssueAnalyses(ctx, "owner", tenantSlug, domain.ProviderIssueAnalysisFilter{Limit: 25})
	if err != nil || page.Counts.DeliveryFailures != 0 || len(page.Items) != 1 || page.Items[0].DeliveryFailure {
		t.Fatalf("old delivery leaked into retried list: %#v %v", page, err)
	}
	attention, err = postgres.ListProviderIssueAnalyses(ctx, "owner", tenantSlug, domain.ProviderIssueAnalysisFilter{State: domain.ProviderIssueAnalysisNeedsAttention, Limit: 25})
	if err != nil || len(attention.Items) != 0 {
		t.Fatalf("old delivery leaked into attention filter: %#v %v", attention, err)
	}
	if err := postgres.MarkProviderIssueAcknowledged(ctx, jobID, 1, 2); err != nil {
		t.Fatal(err)
	}
	var analysisMessageID uuid.UUID
	if err := postgres.pool.QueryRow(ctx, `SELECT id FROM outbox_messages WHERE aggregate_id=$1 AND topic='provider.issue.analyze' AND payload->>'attempt'='2'`, jobID).Scan(&analysisMessageID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO inbox_messages (consumer,message_id,state,attempt,last_error) VALUES ('provider-issue-triager-v1',$1,'released',6,'synthetic model outage')`, analysisMessageID); err != nil {
		t.Fatal(err)
	}
	detail, err = postgres.GetProviderIssueAnalysis(ctx, "owner", tenantSlug, jobID)
	if err != nil || !detail.DeliveryFailure || detail.State != domain.ProviderIssueAnalysisAcknowledged {
		t.Fatalf("terminal analysis delivery failure not surfaced: %#v %v", detail, err)
	}
	page, err = postgres.ListProviderIssueAnalyses(ctx, "owner", tenantSlug, domain.ProviderIssueAnalysisFilter{Limit: 25})
	if err != nil || page.Counts.Acknowledged != 1 || page.Counts.DeliveryFailures != 1 || len(page.Items) != 1 || !page.Items[0].DeliveryFailure {
		t.Fatalf("terminal analysis delivery failure missing from list: %#v %v", page, err)
	}
	second, err := postgres.RetryProviderIssueAnalysis(ctx, "owner", tenantSlug, jobID, domain.ProviderIssueAnalysisRetryInput{ExpectedRevision: 1, IdempotencyKey: "issue-retry:" + uuid.NewString()})
	if err != nil || second.Attempt != 3 || second.State != domain.ProviderIssueAnalysisQueued {
		t.Fatalf("analysis recovery=%#v error=%v", second, err)
	}
	if err := postgres.MarkProviderIssueAcknowledged(ctx, jobID, 1, 2); err != nil {
		t.Fatal(err)
	}
	active, err := postgres.ProviderIssueAnalysis(ctx, jobID, 1)
	if err != nil || active.AnalysisAttempt != 3 || active.State != domain.ProviderIssueAnalysisQueued {
		t.Fatalf("stale attempt changed recovered job: %#v %v", active, err)
	}
	if err := postgres.FailProviderIssueAnalysis(ctx, jobID, 1, 3, "synthetic final analysis failure"); err != nil {
		t.Fatal(err)
	}
	if err := postgres.FailProviderIssueAnalysis(ctx, jobID, 1, 3, "late overwrite"); err != ErrNotFound {
		t.Fatalf("terminal failure overwritten: %v", err)
	}
	attention, err = postgres.ListProviderIssueAnalyses(ctx, "owner", tenantSlug, domain.ProviderIssueAnalysisFilter{State: domain.ProviderIssueAnalysisNeedsAttention, Limit: 25})
	if err != nil || len(attention.Items) != 1 || attention.Items[0].State != domain.ProviderIssueAnalysisFailed || attention.Items[0].DeliveryFailure {
		t.Fatalf("failed analysis missing from attention filter: %#v %v", attention, err)
	}
	third, err := postgres.RetryProviderIssueAnalysis(ctx, "owner", tenantSlug, jobID, domain.ProviderIssueAnalysisRetryInput{ExpectedRevision: 1, IdempotencyKey: "issue-retry:" + uuid.NewString()})
	if err != nil || third.Attempt != 4 || third.State != domain.ProviderIssueAnalysisQueued {
		t.Fatalf("failed model retry=%#v error=%v", third, err)
	}
	if err := postgres.MarkProviderIssueAcknowledged(ctx, jobID, 1, third.Attempt); err != nil {
		t.Fatal(err)
	}
	const retainedReport = "## Outcome\nRetain this exact model report after provider publication fails."
	if staged, err := postgres.StageProviderIssueAnalysis(ctx, jobID, 1, third.Attempt, retainedReport); err != nil || staged != retainedReport {
		t.Fatalf("stage retry report=%q error=%v", staged, err)
	}
	var stagedMessageID uuid.UUID
	if err := postgres.pool.QueryRow(ctx, `SELECT id FROM outbox_messages WHERE aggregate_id=$1 AND topic='provider.issue.analyze' AND payload->>'attempt'='4'`, jobID).Scan(&stagedMessageID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO inbox_messages (consumer,message_id,state,attempt,last_error) VALUES ('provider-issue-triager-v1',$1,'released',6,'provider comment unavailable')`, stagedMessageID); err != nil {
		t.Fatal(err)
	}
	fourth, err := postgres.RetryProviderIssueAnalysis(ctx, "owner", tenantSlug, jobID, domain.ProviderIssueAnalysisRetryInput{ExpectedRevision: 1, IdempotencyKey: "issue-retry:" + uuid.NewString()})
	if err != nil || fourth.Attempt != 5 || fourth.State != domain.ProviderIssueAnalysisAcknowledged {
		t.Fatalf("staged publication retry=%#v error=%v", fourth, err)
	}
	retainedJob, err := postgres.ProviderIssueAnalysis(ctx, jobID, 1)
	if err != nil || retainedJob.Analysis != retainedReport || retainedJob.AnalysisAttempt != fourth.Attempt || retainedJob.State != domain.ProviderIssueAnalysisAcknowledged {
		t.Fatalf("staged publication reran or lost model output: %#v %v", retainedJob, err)
	}
	var resumedTopic string
	if err := postgres.pool.QueryRow(ctx, `SELECT topic FROM outbox_messages WHERE aggregate_id=$1 AND payload->>'attempt'='5'`, jobID).Scan(&resumedTopic); err != nil || resumedTopic != "provider.issue.analyze" {
		t.Fatalf("staged retry topic=%q error=%v", resumedTopic, err)
	}
	if err := postgres.CompleteProviderIssueAnalysis(ctx, jobID, 1, fourth.Attempt, retainedReport); err != nil {
		t.Fatal(err)
	}
}

func TestProviderIssueAnalysisRecoversConsumedDeliveryAfterConnectionRestored(t *testing.T) {
	databaseURL := os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("OPEN_REVIEW_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	postgres, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer postgres.Close()
	tenantID, installationID, configurationID := uuid.New(), uuid.New(), uuid.New()
	tenantSlug := "issue-skipped-" + tenantID.String()[:8]
	modelRouteJSON, _ := json.Marshal(domain.ModelRouteConfig{
		Enabled: true, Provider: "openai-compatible", Protocol: "openai-chat",
		BaseURL: "https://models.example/v1/chat/completions", Model: "test-model",
		CredentialRef: "env://OPEN_REVIEW_MODEL_SECRET_PRIMARY", Effort: "low",
		MaxPromptTokens: 8000, TokenBudget: 128000, SubtaskTimeoutMinutes: 2, MaxConcurrentRuns: 2,
	})
	batch := &pgx.Batch{}
	batch.Queue("INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Skipped Issue delivery')", tenantID, tenantSlug)
	batch.Queue("INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner','owner')", tenantID)
	batch.Queue("INSERT INTO provider_installations (id,tenant_id,provider,external_id,repository_scope,automatic_reviews,api_base_url,credential_ref,verification_state) VALUES ($1,$2,'github',$3,'RainLib/*',TRUE,'https://api.github.com','github-app','verified')", installationID, tenantID, "triage-skipped-"+tenantID.String())
	batch.Queue("INSERT INTO review_configurations (id,tenant_id,section,scope_kind,scope_ref,active) VALUES ($1,$2,'models','tenant','',TRUE)", configurationID, tenantID)
	batch.Queue("INSERT INTO review_configuration_versions (configuration_id,revision,content,content_sha256,created_by) VALUES ($1,1,$2::jsonb,$3,'owner')", configurationID, modelRouteJSON, "route-sha")
	if err := postgres.pool.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), "DELETE FROM tenants WHERE id=$1", tenantID) }()
	event := domain.ProviderIssueEvent{
		Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com", DeliveryID: "skipped-" + uuid.NewString(), EventName: "issues",
		InstallationExternalID: "triage-skipped-" + tenantID.String(), Repository: "RainLib/open-review-platform", IssueNumber: 43,
		Action: "opened", Title: "Skipped acknowledgement", Body: "Reproduction", Author: "alice", Labels: []string{},
		Payload: json.RawMessage("{}"), ReceivedAt: time.Now().UTC(),
	}
	outcome, err := postgres.EnqueueProviderIssueAnalysis(ctx, event)
	if err != nil || outcome.Skipped || outcome.Duplicate {
		t.Fatalf("enqueue=%#v error=%v", outcome, err)
	}
	jobID := outcome.Job.ID
	var messageID uuid.UUID
	if err := postgres.pool.QueryRow(ctx, `SELECT id FROM outbox_messages WHERE aggregate_id=$1 AND topic='provider.issue.acknowledge' AND payload->>'attempt'='1'`, jobID).Scan(&messageID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE provider_installations SET verification_state='failed' WHERE id=$1`, installationID); err != nil {
		t.Fatal(err)
	}
	// A worker that treats an ineligible installation as ErrNotFound completes
	// the inbox message without advancing the retained queued job.
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO inbox_messages (consumer,message_id,state,attempt,last_error) VALUES ('provider-issue-triager-v1',$1,'completed',1,'')`, messageID); err != nil {
		t.Fatal(err)
	}
	page, err := postgres.ListProviderIssueAnalyses(ctx, "owner", tenantSlug, domain.ProviderIssueAnalysisFilter{Limit: 25})
	if err != nil || page.Counts.Queued != 1 || page.Counts.ConnectionBlocked != 1 || page.Counts.SkippedDeliveries != 1 || page.Counts.NeedsAttention != 1 || len(page.Items) != 1 || !page.Items[0].SkippedDelivery {
		t.Fatalf("consumed blocked delivery missing from list: %#v %v", page, err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE provider_installations SET verification_state='verified' WHERE id=$1`, installationID); err != nil {
		t.Fatal(err)
	}
	page, err = postgres.ListProviderIssueAnalyses(ctx, "owner", tenantSlug, domain.ProviderIssueAnalysisFilter{State: domain.ProviderIssueAnalysisNeedsAttention, Limit: 25})
	if err != nil || page.Counts.ConnectionBlocked != 0 || page.Counts.SkippedDeliveries != 1 || page.Counts.NeedsAttention != 1 || len(page.Items) != 1 || page.Items[0].ConnectionBlocked {
		t.Fatalf("consumed delivery disappeared after connection recovery: %#v %v", page, err)
	}
	detail, err := postgres.GetProviderIssueAnalysis(ctx, "owner", tenantSlug, jobID)
	if err != nil || !detail.SkippedDelivery || !detail.RetryReadiness.CanRetry {
		t.Fatalf("consumed delivery retry readiness=%#v error=%v", detail, err)
	}
	retry, err := postgres.RetryProviderIssueAnalysis(ctx, "owner", tenantSlug, jobID, domain.ProviderIssueAnalysisRetryInput{ExpectedRevision: 1, IdempotencyKey: "issue-skipped:" + uuid.NewString()})
	if err != nil || retry.Attempt != 2 || retry.State != domain.ProviderIssueAnalysisQueued {
		t.Fatalf("consumed delivery recovery=%#v error=%v", retry, err)
	}
	page, err = postgres.ListProviderIssueAnalyses(ctx, "owner", tenantSlug, domain.ProviderIssueAnalysisFilter{State: domain.ProviderIssueAnalysisNeedsAttention, Limit: 25})
	if err != nil || page.Counts.SkippedDeliveries != 0 || page.Counts.NeedsAttention != 0 || len(page.Items) != 0 {
		t.Fatalf("old consumed delivery leaked into new attempt: %#v %v", page, err)
	}
}
