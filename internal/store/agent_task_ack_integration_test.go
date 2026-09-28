package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestProviderAgentSourceWaitsForBoundAcknowledgement(t *testing.T) {
	databaseURL := os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL")
	if databaseURL == "" || os.Getenv("OPEN_REVIEW_TEST_ISOLATED_DATABASE") != "true" {
		t.Skip("agent acknowledgement integration requires an isolated database")
	}
	ctx := context.Background()
	postgres, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer postgres.Close()

	tenantID, installationID, taskID, interactionID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	externalID := "agent-ack-" + installationID.String()
	repository := "RainLib/ack-integration"
	tenantSlug := "agent-ack-" + tenantID.String()[:8]
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants(id,slug,name) VALUES($1,$2,'Agent acknowledgement integration')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships(tenant_id,subject,role) VALUES($1,'ack-reviewer','reviewer')`, tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO provider_installations(id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref,verification_state) VALUES($1,$2,'github',$3,$4,'https://api.github.com','test-only','verified')`, installationID, tenantID, externalID, repository); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO agent_tasks(id,tenant_id,installation_id,provider,api_base_url,repository,origin_kind,origin_number,origin_revision,intent,execution_branch,requested_by) VALUES($1,$2,$3,'github','https://api.github.com',$4,'issue',19,'issue-revision','implement',$5,'provider-reviewer')`, taskID, tenantID, installationID, repository, "agent/"+taskID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO agent_task_interactions(id,tenant_id,task_id,provider,provider_delivery_id,actor_external_id,repository,issue_number,issue_revision,comment_external_id,command,normalized_input,result) VALUES($1,$2,$3,'github',$4,'reviewer',$5,19,'issue-revision','comment-19','implement','@openreview implement','accepted')`, interactionID, tenantID, taskID, "delivery-"+interactionID.String(), repository); err != nil {
		t.Fatal(err)
	}
	response := domain.InteractionResponse{
		TenantID: tenantID, Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com",
		InstallationExternalID: externalID, CredentialRef: "test-only", Repository: repository,
		ResourceKind: "issue", ReviewNumber: 19, Marker: agentTaskInteractionMarker(interactionID),
		SourceRelease: &domain.AgentTaskSourceRelease{TaskID: taskID, Revision: 1},
	}
	countSource := func(id uuid.UUID) int {
		t.Helper()
		var count int
		if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM outbox_messages WHERE aggregate_id=$1 AND topic='agent.task.source.resolve.requested'`, id).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	if got := countSource(taskID); got != 0 {
		t.Fatalf("source queued before acknowledgement: %d", got)
	}
	wrong := response
	wrong.Marker = agentTaskInteractionMarker(uuid.New())
	if err := postgres.WithAgentTaskAcknowledgementFence(ctx, wrong, func(context.Context) error { return nil }); !errors.Is(err, ErrConflict) || countSource(taskID) != 0 {
		t.Fatalf("unbound acknowledgement released source: %v", err)
	}
	wrong = response
	wrong.SourceRelease = &domain.AgentTaskSourceRelease{TaskID: taskID, Revision: 2}
	if err := postgres.WithAgentTaskAcknowledgementFence(ctx, wrong, func(context.Context) error { return nil }); err != nil || countSource(taskID) != 0 {
		t.Fatalf("stale revision released source: %v", err)
	}
	providerFailure := errors.New("provider acknowledgement unavailable")
	if err := postgres.WithAgentTaskAcknowledgementFence(ctx, response, func(context.Context) error { return providerFailure }); !errors.Is(err, providerFailure) || countSource(taskID) != 0 {
		t.Fatalf("failed provider acknowledgement released source: %v", err)
	}
	published := false
	if err := postgres.WithAgentTaskAcknowledgementFence(ctx, response, func(context.Context) error { published = true; return nil }); err != nil || !published {
		t.Fatalf("current task acknowledgement was not published: %v", err)
	}
	for range 2 {
		if err := postgres.WithAgentTaskAcknowledgementFence(ctx, response, func(context.Context) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if got := countSource(taskID); got != 1 {
		t.Fatalf("provider acknowledgement must release exactly one source job, got %d", got)
	}
	enteredPublish := make(chan struct{})
	releasePublish := make(chan struct{})
	defer func() {
		select {
		case <-releasePublish:
		default:
			close(releasePublish)
		}
	}()
	publicationResult := make(chan error, 1)
	go func() {
		publicationResult <- postgres.WithAgentTaskAcknowledgementFence(ctx, response, func(context.Context) error {
			close(enteredPublish)
			<-releasePublish
			return nil
		})
	}()
	select {
	case <-enteredPublish:
	case <-time.After(3 * time.Second):
		t.Fatal("provider publication never entered the task-row fence")
	}
	concurrent, err := postgres.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := concurrent.Exec(ctx, `SET LOCAL lock_timeout='100ms'`); err != nil {
		_ = concurrent.Rollback(ctx)
		t.Fatal(err)
	}
	_, lockErr := concurrent.Exec(ctx, `UPDATE agent_tasks SET state='cancelled',revision=revision+1 WHERE id=$1`, taskID)
	_ = concurrent.Rollback(ctx)
	var postgresError *pgconn.PgError
	if !errors.As(lockErr, &postgresError) || postgresError.Code != "55P03" {
		t.Fatalf("cancellation was not serialized behind provider publication: %v", lockErr)
	}
	close(releasePublish)
	if err := <-publicationResult; err != nil {
		t.Fatal(err)
	}

	autoTaskID, autoInteractionID := uuid.New(), uuid.New()
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO agent_tasks(id,tenant_id,installation_id,provider,api_base_url,repository,origin_kind,origin_number,origin_revision,intent,execution_branch,requested_by) VALUES($1,$2,$3,'github','https://api.github.com',$4,'issue',20,'auto-issue-revision','implement',$5,'policy:auto')`, autoTaskID, tenantID, installationID, repository, "agent/"+autoTaskID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO agent_task_interactions(id,tenant_id,task_id,provider,provider_delivery_id,actor_external_id,repository,issue_number,issue_revision,comment_external_id,command,normalized_input,result) VALUES($1,$2,$3,'github',$4,'provider-issue',$5,20,'auto-issue-revision','webhook','auto','automatic label','accepted')`, autoInteractionID, tenantID, autoTaskID, "delivery-"+autoInteractionID.String(), repository); err != nil {
		t.Fatal(err)
	}
	autoResponse := response
	autoResponse.ReviewNumber = 20
	autoResponse.Marker = "open-review-platform:agent-task:auto:" + autoTaskID.String()
	autoResponse.SourceRelease = &domain.AgentTaskSourceRelease{TaskID: autoTaskID, Revision: 1}
	if _, err := postgres.pool.Exec(ctx, `UPDATE agent_tasks SET state='cancelled',revision=revision+1 WHERE id=$1`, autoTaskID); err != nil {
		t.Fatal(err)
	}
	published = false
	if err := postgres.WithAgentTaskAcknowledgementFence(ctx, autoResponse, func(context.Context) error { published = true; return nil }); err != nil || published || countSource(autoTaskID) != 0 {
		t.Fatalf("cancelled automatic candidate released source: %v", err)
	}

	// A lost ACK becomes a visible, retryable task state without inventing a
	// provider comment. Already released source work must not be expired.
	timeoutTaskID, timeoutInteractionID := uuid.New(), uuid.New()
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO agent_tasks(id,tenant_id,installation_id,provider,api_base_url,repository,origin_kind,origin_number,origin_revision,intent,execution_branch,requested_by,created_at) VALUES($1,$2,$3,'github','https://api.github.com',$4,'issue',21,'timeout-issue-revision','implement',$5,'provider-reviewer',now()-interval '1 hour')`, timeoutTaskID, tenantID, installationID, repository, "agent/"+timeoutTaskID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO agent_task_interactions(id,tenant_id,task_id,provider,provider_delivery_id,actor_external_id,repository,issue_number,issue_revision,comment_external_id,command,normalized_input,result) VALUES($1,$2,$3,'github',$4,'reviewer',$5,21,'timeout-issue-revision','comment-21','implement','@openreview implement','accepted')`, timeoutInteractionID, tenantID, timeoutTaskID, "delivery-"+timeoutInteractionID.String(), repository); err != nil {
		t.Fatal(err)
	}
	// Keep the original immutable ACK in the outbox, just as admission does.
	// The provider cannot publish it until its credential is restored.
	ackPayload := map[string]any{
		"tenant_id": tenantID.String(), "provider": "github", "api_base_url": "https://api.github.com",
		"installation_external_id": externalID, "credential_ref": "test-only", "repository": repository,
		"resource_kind": "issue", "review_number": 21, "comment_external_id": "comment-21",
		"reaction": domain.InteractionReactionNone, "body": "Agent task received", "marker": agentTaskInteractionMarker(timeoutInteractionID),
		"release_agent_task_source_id": timeoutTaskID.String(), "release_agent_task_source_revision": 1,
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO outbox_messages(aggregate_type,aggregate_id,topic,dedupe_key,payload) VALUES('agent_task_interaction',$1,'review.interaction.response',$2,$3::jsonb)`, timeoutInteractionID, "agent-task-interaction:"+timeoutInteractionID.String()+":response", jsonPayload(ackPayload)); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE agent_tasks SET created_at=now()-interval '2 hours' WHERE id=$1`, taskID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.ExpireUnacknowledgedAgentTasks(ctx, 25, time.Minute); !errors.Is(err, ErrInvalidAgentTask) {
		t.Fatalf("short acknowledgement timeout accepted: %v", err)
	}
	if expired, err := postgres.ExpireUnacknowledgedAgentTasks(ctx, 1, 30*time.Minute); err != nil || expired != 1 {
		t.Fatalf("acknowledgement watchdog expired %d tasks: %v", expired, err)
	}
	var taskState, sourceState string
	if err := postgres.pool.QueryRow(ctx, `SELECT state,source_state FROM agent_tasks WHERE id=$1`, timeoutTaskID).Scan(&taskState, &sourceState); err != nil || taskState != "needs_attention" || sourceState != "failed" || countSource(timeoutTaskID) != 0 {
		t.Fatalf("unacknowledged task state=%s/%s error=%v", taskState, sourceState, err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT state,source_state FROM agent_tasks WHERE id=$1`, taskID).Scan(&taskState, &sourceState); err != nil || taskState != "received" || sourceState != "pending" {
		t.Fatalf("released source was expired: state=%s/%s error=%v", taskState, sourceState, err)
	}
	timeoutResponse := response
	timeoutResponse.ReviewNumber = 21
	timeoutResponse.Marker = agentTaskInteractionMarker(timeoutInteractionID)
	timeoutResponse.SourceRelease = &domain.AgentTaskSourceRelease{TaskID: timeoutTaskID, Revision: 1}
	published = false
	if err := postgres.WithAgentTaskAcknowledgementFence(ctx, timeoutResponse, func(context.Context) error { published = true; return nil }); err != nil || published {
		t.Fatalf("expired acknowledgement reached provider: %v", err)
	}
	if expired, err := postgres.ExpireUnacknowledgedAgentTasks(ctx, 25, 30*time.Minute); err != nil || expired != 0 {
		t.Fatalf("acknowledgement watchdog repeated expiration: %d %v", expired, err)
	}
	retried, err := postgres.RetryAgentTaskSource(ctx, "ack-reviewer", tenantSlug, timeoutTaskID, 2)
	if err != nil || retried.Revision != 3 || retried.State != "received" || retried.SourceState != "pending" {
		t.Fatalf("acknowledgement retry did not reopen task: task=%+v error=%v", retried, err)
	}
	if got := countSource(timeoutTaskID); got != 0 {
		t.Fatalf("retry bypassed the provider acknowledgement: %d source jobs", got)
	}
	var retryRevision int
	if err := postgres.pool.QueryRow(ctx, `SELECT (payload->>'release_agent_task_source_revision')::int FROM outbox_messages WHERE dedupe_key=$1`, "agent-task:"+timeoutTaskID.String()+":ack-retry:3").Scan(&retryRevision); err != nil || retryRevision != 3 {
		t.Fatalf("fresh acknowledgement was not queued for revision 3: revision=%d error=%v", retryRevision, err)
	}
	if _, err := postgres.RetryAgentTaskSource(ctx, "ack-reviewer", tenantSlug, timeoutTaskID, 2); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("duplicate acknowledgement retry accepted: %v", err)
	}
	timeoutResponse.SourceRelease.Revision = 3
	if err := postgres.WithAgentTaskAcknowledgementFence(ctx, timeoutResponse, func(context.Context) error { return providerFailure }); !errors.Is(err, providerFailure) || countSource(timeoutTaskID) != 0 {
		t.Fatalf("failed retried acknowledgement released source: %v", err)
	}
	if err := postgres.WithAgentTaskAcknowledgementFence(ctx, timeoutResponse, func(context.Context) error { return nil }); err != nil || countSource(timeoutTaskID) != 1 {
		t.Fatalf("confirmed retried acknowledgement did not release source: %v", err)
	}
	// A legacy retry row is not proof of provider publication. Without the
	// original ACK payload, reopening this task must fail closed.
	unboundTaskID := uuid.New()
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO agent_tasks(id,tenant_id,installation_id,provider,api_base_url,repository,origin_kind,origin_number,origin_revision,intent,execution_branch,requested_by,source_state,state,revision) VALUES($1,$2,$3,'github','https://api.github.com',$4,'issue',22,'unbound-revision','implement',$5,'provider-reviewer','failed','needs_attention',2)`, unboundTaskID, tenantID, installationID, repository, "agent/"+unboundTaskID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO outbox_messages(aggregate_type,aggregate_id,topic,dedupe_key,payload) VALUES('agent_task',$1,'agent.task.source.resolve.requested',$2,'{}'::jsonb)`, unboundTaskID, "agent-task:"+unboundTaskID.String()+":source-retry:1"); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.RetryAgentTaskSource(ctx, "ack-reviewer", tenantSlug, unboundTaskID, 2); !errors.Is(err, ErrConflict) {
		t.Fatalf("unbound source retry bypassed provider acknowledgement: %v", err)
	}
	var unboundRevision int
	if err := postgres.pool.QueryRow(ctx, `SELECT revision FROM agent_tasks WHERE id=$1`, unboundTaskID).Scan(&unboundRevision); err != nil || unboundRevision != 2 {
		t.Fatalf("unbound retry changed task revision=%d error=%v", unboundRevision, err)
	}
}
