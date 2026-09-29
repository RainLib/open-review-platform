package store

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestExhaustedInteractionResponseHasBoundedIdempotentRecovery(t *testing.T) {
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

	tenantID, installationID, requestID := uuid.New(), uuid.New(), uuid.New()
	runID, interactionID, responseID := uuid.New(), uuid.New(), uuid.New()
	slug := "ack-retry-" + tenantID.String()[:8]
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO tenants(id,slug,name) VALUES($1,$2,'Response recovery')`, tenantID, slug)
	batch.Queue(`INSERT INTO memberships(tenant_id,subject,role) VALUES($1,'owner','owner'),($1,'viewer','viewer')`, tenantID)
	batch.Queue(`INSERT INTO provider_installations(id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref,verification_state) VALUES($1,$2,'gitlab',$3,'RainLib/*','https://gitlab.example/api/v4','oauth:scoped','verified')`, installationID, tenantID, "recovery-"+installationID.String())
	batch.Queue(`INSERT INTO review_requests(id,tenant_id,installation_id,provider,api_base_url,repository,review_number) VALUES($1,$2,$3,'gitlab','https://gitlab.example/api/v4','RainLib/open-review-platform',7)`, requestID, tenantID, installationID)
	batch.Queue(`INSERT INTO review_runs(id,request_id,state,trigger_kind,review_mode,head_sha,base_sha) VALUES($1,$2,'acknowledged','comment','configured','head','base')`, runID, requestID)
	batch.Queue(`INSERT INTO review_interactions(id,tenant_id,request_id,provider,provider_delivery_id,actor_external_id,command,normalized_input,result,result_run_id) VALUES($1,$2,$3,'gitlab',$4,'reviewer','review','@openreview review','accepted',$5)`, interactionID, tenantID, requestID, "recovery-"+interactionID.String(), runID)
	batch.Queue(`
		INSERT INTO outbox_messages(id,aggregate_type,aggregate_id,topic,dedupe_key,payload,published_at)
		VALUES($1,'review_interaction',$2,'review.interaction.response',$3,
		  jsonb_build_object('provider','gitlab','api_base_url','https://gitlab.example/api/v4',
		    'repository','RainLib/open-review-platform','review_number',7,
		    'comment_external_id','note-7','reaction','eyes','reaction_only',true,'body','',
		    'marker',$4::text,'release_run_id',$5::text),now())`,
		responseID, interactionID, "interaction:"+interactionID.String()+":response", interactionResponseMarker(interactionID), runID.String())
	batch.Queue(`INSERT INTO inbox_messages(consumer,message_id,state,attempt) VALUES('interaction-responder-v1',$1,'released',2)`, responseID)
	if err := postgres.pool.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatalf("seed exhausted interaction response: %v", err)
	}
	defer func() {
		cleanup := context.Background()
		_, _ = postgres.pool.Exec(cleanup, `DELETE FROM inbox_messages WHERE message_id IN (SELECT id FROM outbox_messages WHERE aggregate_id=$1)`, interactionID)
		_, _ = postgres.pool.Exec(cleanup, `DELETE FROM outbox_messages WHERE aggregate_id=$1`, interactionID)
		_, _ = postgres.pool.Exec(cleanup, `DELETE FROM tenants WHERE id=$1`, tenantID)
	}()

	input := domain.RunRetryInput{ExpectedRevision: 1, IdempotencyKey: "response-recovery-0001"}
	evidence, err := postgres.GetReviewEvidence(ctx, "owner", slug, runID)
	if err != nil || evidence.AcknowledgementRecoveryAvailable {
		t.Fatalf("active delivery recovery availability=%v error=%v", evidence.AcknowledgementRecoveryAvailable, err)
	}
	if _, err := postgres.RetryInteractionResponse(ctx, "owner", slug, runID, input); !errors.Is(err, ErrInteractionResponseNotExhausted) {
		t.Fatalf("active broker retry error=%v, want not exhausted", err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE inbox_messages SET attempt=6 WHERE message_id=$1`, responseID); err != nil {
		t.Fatal(err)
	}
	evidence, err = postgres.GetReviewEvidence(ctx, "owner", slug, runID)
	if err != nil || !evidence.AcknowledgementRecoveryAvailable {
		t.Fatalf("exhausted delivery recovery availability=%v error=%v", evidence.AcknowledgementRecoveryAvailable, err)
	}
	evidence, err = postgres.GetReviewEvidence(ctx, "viewer", slug, runID)
	if err != nil || evidence.AcknowledgementRecoveryAvailable {
		t.Fatalf("viewer recovery availability=%v error=%v", evidence.AcknowledgementRecoveryAvailable, err)
	}
	result, err := postgres.RetryInteractionResponse(ctx, "owner", slug, runID, input)
	if err != nil || result.Replayed || result.Attempt != 1 || result.RunID != runID {
		t.Fatalf("first recovery=%#v error=%v", result, err)
	}
	replayed, err := postgres.RetryInteractionResponse(ctx, "owner", slug, runID, input)
	if err != nil || !replayed.Replayed || replayed.Attempt != 1 {
		t.Fatalf("idempotent recovery=%#v error=%v", replayed, err)
	}
	evidence, err = postgres.GetReviewEvidence(ctx, "owner", slug, runID)
	if err != nil || evidence.AcknowledgementRecoveryAvailable {
		t.Fatalf("new delivery recovery availability=%v error=%v", evidence.AcknowledgementRecoveryAvailable, err)
	}
	var retryID uuid.UUID
	var repairedTenant, marker, releasedRun, repairedBody string
	var reactionOnly bool
	if err := postgres.pool.QueryRow(ctx, `
		SELECT id,payload->>'tenant_id',payload->>'marker',payload->>'release_run_id',payload->>'reaction_only'='true',payload->>'body'
		FROM outbox_messages WHERE dedupe_key=$1`, "interaction:"+interactionID.String()+":ack-retry:"+input.IdempotencyKey).Scan(&retryID, &repairedTenant, &marker, &releasedRun, &reactionOnly, &repairedBody); err != nil {
		t.Fatal(err)
	}
	if repairedTenant != tenantID.String() || marker != interactionResponseMarker(interactionID) || releasedRun != runID.String() || !reactionOnly || repairedBody != "" {
		t.Fatalf("recovered reaction changed authority or marker: tenant=%q marker=%q run=%q reaction_only=%t body=%q", repairedTenant, marker, releasedRun, reactionOnly, repairedBody)
	}
	if _, err := postgres.RetryInteractionResponse(ctx, "viewer", slug, runID, domain.RunRetryInput{ExpectedRevision: 1, IdempotencyKey: "response-recovery-0002"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer recovery error=%v, want forbidden", err)
	}
	if _, err := postgres.RetryInteractionResponse(ctx, "owner", slug, runID, domain.RunRetryInput{ExpectedRevision: 2, IdempotencyKey: "response-recovery-0002"}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale revision error=%v, want conflict", err)
	}
	if _, err := postgres.RetryInteractionResponse(ctx, "owner", slug, runID, domain.RunRetryInput{ExpectedRevision: 1, IdempotencyKey: "response-recovery-0002"}); !errors.Is(err, ErrInteractionResponseNotExhausted) {
		t.Fatalf("pending recovery error=%v, want not exhausted", err)
	}
	latestID := retryID
	for attempt := 2; attempt <= 3; attempt++ {
		if _, err := postgres.pool.Exec(ctx, `UPDATE outbox_messages SET published_at=now() WHERE id=$1`, latestID); err != nil {
			t.Fatal(err)
		}
		if _, err := postgres.pool.Exec(ctx, `INSERT INTO inbox_messages(consumer,message_id,state,attempt) VALUES('interaction-responder-v1',$1,'released',6)`, latestID); err != nil {
			t.Fatal(err)
		}
		key := "response-recovery-000" + string(rune('0'+attempt))
		next, err := postgres.RetryInteractionResponse(ctx, "owner", slug, runID, domain.RunRetryInput{ExpectedRevision: 1, IdempotencyKey: key})
		if err != nil || next.Attempt != attempt || next.Replayed {
			t.Fatalf("bounded recovery attempt %d=%#v error=%v", attempt, next, err)
		}
		if err := postgres.pool.QueryRow(ctx, `SELECT id FROM outbox_messages WHERE dedupe_key=$1`, "interaction:"+interactionID.String()+":ack-retry:"+key).Scan(&latestID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE outbox_messages SET published_at=now() WHERE id=$1`, latestID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO inbox_messages(consumer,message_id,state,attempt) VALUES('interaction-responder-v1',$1,'released',6)`, latestID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.RetryInteractionResponse(ctx, "owner", slug, runID, domain.RunRetryInput{ExpectedRevision: 1, IdempotencyKey: "response-recovery-0004"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("fourth recovery error=%v, want bounded conflict", err)
	}
	evidence, err = postgres.GetReviewEvidence(ctx, "owner", slug, runID)
	if err != nil || evidence.AcknowledgementRecoveryAvailable {
		t.Fatalf("retry limit recovery availability=%v error=%v", evidence.AcknowledgementRecoveryAvailable, err)
	}
	var runState domain.RunState
	var runCount, releaseCount, auditCount int
	if err := postgres.pool.QueryRow(ctx, `SELECT state FROM review_runs WHERE id=$1`, runID).Scan(&runState); err != nil {
		t.Fatal(err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM review_runs WHERE request_id=$1`, requestID).Scan(&runCount); err != nil {
		t.Fatal(err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM outbox_messages WHERE aggregate_id=$1 AND topic='review.run.acknowledged'`, runID).Scan(&releaseCount); err != nil {
		t.Fatal(err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND action='review.interaction_response_retry'`, tenantID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if runState != domain.RunAcknowledged || runCount != 1 || releaseCount != 0 || auditCount != 3 {
		t.Fatalf("recovery bypassed provider barrier: state=%s runs=%d releases=%d audits=%d", runState, runCount, releaseCount, auditCount)
	}
}
