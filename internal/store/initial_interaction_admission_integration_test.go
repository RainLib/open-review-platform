package store

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

func TestFirstReviewCommandAcknowledgesBeforeItCreatesOrReleasesTheRun(t *testing.T) {
	databaseURL := os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("OPEN_REVIEW_TEST_DATABASE_URL is not configured")
	}
	t.Setenv("OPEN_REVIEW_APP_URL", "https://review.example.test")
	ctx := context.Background()
	postgres, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer postgres.Close()

	tenantID, installationID := uuid.New(), uuid.New()
	tenantSlug := "initial-command-" + tenantID.String()[:8]
	installationExternalID := "initial-command-" + installationID.String()
	actorExternalID := "actor-" + tenantID.String()
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Initial command')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) }()
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'reviewer','reviewer')`, tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO provider_installations (id,tenant_id,provider,external_id,repository_scope,automatic_reviews,minimum_severity,api_base_url,credential_ref,verification_state)
		VALUES ($1,$2,'github',$3,'RainLib/*',TRUE,'medium','https://api.github.com','github-app','verified')`, installationID, tenantID, installationExternalID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO provider_actor_mappings (tenant_id,provider,external_id,subject) VALUES ($1,'github',$2,'reviewer')`, tenantID, actorExternalID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO workspace_setup_checkpoints (tenant_id,current_step,revision,updated_by,completed_at)
		VALUES ($1,'complete',5,'owner',$2)`, tenantID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	comment := domain.CommentEvent{
		Provider: domain.ProviderGitHub, APIBaseURL: "https://api.github.com", DeliveryID: "first-command-" + tenantID.String(),
		InstallationExternalID: installationExternalID, Repository: "RainLib/open-review-platform", CloneURL: "https://github.com/RainLib/open-review-platform.git",
		ReviewNumber: 21, CommentExternalID: "comment-21", ActorExternalID: actorExternalID, Body: "@openreview review security",
	}
	outcome, err := postgres.ProcessInteraction(ctx, domain.InteractionCommand{Event: comment, Command: "review", Mode: string(domain.ReviewModeSecurity), Normalized: "@openreview review security"})
	if err != nil || !outcome.Accepted || outcome.RunID != nil || outcome.Reason != "review admission acknowledged" {
		t.Fatalf("initial command outcome=%#v error=%v", outcome, err)
	}

	var interactionID uuid.UUID
	if err := postgres.pool.QueryRow(ctx, `SELECT id FROM review_interactions WHERE provider_delivery_id=$1`, comment.DeliveryID).Scan(&interactionID); err != nil {
		t.Fatal(err)
	}
	var runs, responses, admissions, acknowledged int
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM review_runs WHERE request_id=(SELECT request_id FROM review_interactions WHERE id=$1)`, interactionID).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM outbox_messages WHERE aggregate_id=$1 AND topic='review.interaction.response'`, interactionID).Scan(&responses); err != nil {
		t.Fatal(err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM outbox_messages WHERE aggregate_id=$1 AND topic='review.interaction.admission'`, interactionID).Scan(&admissions); err != nil {
		t.Fatal(err)
	}
	if runs != 0 || responses != 1 || admissions != 0 {
		t.Fatalf("first command must only acknowledge before provider publication, runs=%d responses=%d admissions=%d", runs, responses, admissions)
	}
	var initialReactionOnly bool
	var initialBody string
	if err := postgres.pool.QueryRow(ctx, `SELECT payload->>'reaction_only'='true', payload->>'body' FROM outbox_messages WHERE aggregate_id=$1 AND topic='review.interaction.response'`, interactionID).Scan(&initialReactionOnly, &initialBody); err != nil {
		t.Fatal(err)
	}
	if !initialReactionOnly || initialBody != "" {
		t.Fatalf("initial review command must only react, reaction_only=%t body=%q", initialReactionOnly, initialBody)
	}
	admission := domain.InteractionAdmission{InteractionID: interactionID, Event: comment, Mode: domain.ReviewModeSecurity, ActorSubject: "reviewer", CredentialRef: "github-app"}
	if err := postgres.ReleaseInitialInteractionAdmission(ctx, admission); err != nil {
		t.Fatal(err)
	}
	if err := postgres.ReleaseInitialInteractionAdmission(ctx, admission); err != nil {
		t.Fatal(err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM outbox_messages WHERE aggregate_id=$1 AND topic='review.interaction.admission'`, interactionID).Scan(&admissions); err != nil {
		t.Fatal(err)
	}
	if admissions != 1 {
		t.Fatalf("published progress reply must release one admission message, got %d", admissions)
	}

	event := domain.InboundEvent{
		Provider: domain.ProviderGitHub, APIBaseURL: comment.APIBaseURL, DeliveryID: "interaction-admission:" + interactionID.String(), EventName: "interaction_admission",
		InstallationExternalID: installationExternalID, Repository: comment.Repository, CloneURL: comment.CloneURL, ReviewNumber: comment.ReviewNumber,
		BaseRef: "main", BaseSHA: "base-sha", HeadRef: "feature/initial-command", HeadSHA: "head-sha", TriggerKind: "comment", ActorKind: "user", ActorSubject: "reviewer",
		ReviewMode: domain.ReviewModeSecurity, Action: "comment", DeferAcknowledgement: true, ReceivedAt: time.Now().UTC(), Payload: []byte(`{"source":"provider-current-revision"}`),
	}
	run, released, err := postgres.AdmitInitialInteractionReview(ctx, interactionID, comment, event)
	if err != nil || run.ID == uuid.Nil || !released || run.State != domain.RunAcknowledged || run.HeadSHA != "head-sha" {
		t.Fatalf("admit first command run=%#v released=%t error=%v", run, released, err)
	}
	findingID := uuid.New()
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO review_findings (id,job_id,path,start_line,end_line,severity,category,body,suggestion,fingerprint)
		VALUES ($1,$2,'internal/example.go',12,12,'medium','bug','Example finding','Fix the example','console-link-finding')`, findingID, *run.LegacyJobID); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		command string
		target  string
		want    string
	}{
		{command: "help", want: "https://review.example.test/" + tenantSlug + "/review-commands"},
		{command: "status", want: "https://review.example.test/" + tenantSlug + "/reviews/" + run.ID.String()},
		{command: "explain", target: findingID.String(), want: "https://review.example.test/" + tenantSlug + "/reviews/" + run.ID.String() + "?tab=findings&finding=" + findingID.String() + "#finding-" + findingID.String()},
	} {
		followup := comment
		followup.DeliveryID = tc.command + "-link-" + tenantID.String()
		followup.CommentExternalID = "comment-" + tc.command
		followup.Body = "@openreview " + tc.command + " " + tc.target
		outcome, err := postgres.ProcessInteraction(ctx, domain.InteractionCommand{Event: followup, Command: tc.command, Target: tc.target, Normalized: strings.TrimSpace(followup.Body)})
		if err != nil || !outcome.Accepted {
			t.Fatalf("%s interaction outcome=%#v error=%v", tc.command, outcome, err)
		}
		var body string
		if err := postgres.pool.QueryRow(ctx, `
			SELECT outbox.payload->>'body' FROM outbox_messages outbox
			JOIN review_interactions interaction ON interaction.id=outbox.aggregate_id
			WHERE interaction.provider_delivery_id=$1 AND outbox.topic='review.interaction.response'`, followup.DeliveryID).Scan(&body); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(body, tc.want) {
			t.Fatalf("%s response missing Console link %q: %q", tc.command, tc.want, body)
		}
	}
	var responseTenantID, admittedBody string
	var admittedReactionOnly bool
	if err := postgres.pool.QueryRow(ctx, `
		SELECT payload->>'tenant_id', payload->>'reaction_only'='true', payload->>'body'
		FROM outbox_messages
		WHERE aggregate_id=$1 AND topic='review.interaction.response'
		  AND dedupe_key=$2`, interactionID, "interaction:"+interactionID.String()+":admitted:"+run.ID.String()).Scan(&responseTenantID, &admittedReactionOnly, &admittedBody); err != nil {
		t.Fatal(err)
	}
	if responseTenantID != tenantID.String() || !admittedReactionOnly || admittedBody != "" {
		t.Fatalf("admitted response tenant=%q reaction_only=%t body=%q", responseTenantID, admittedReactionOnly, admittedBody)
	}
	duplicate, duplicateReleased, err := postgres.AdmitInitialInteractionReview(ctx, interactionID, comment, event)
	if err != nil || duplicate.ID != run.ID || duplicateReleased {
		t.Fatalf("replayed admission run=%#v released=%t error=%v", duplicate, duplicateReleased, err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM outbox_messages WHERE aggregate_id=$1 AND topic='review.run.acknowledged'`, run.ID).Scan(&acknowledged); err != nil {
		t.Fatal(err)
	}
	if acknowledged != 0 {
		t.Fatalf("run must not reach acknowledger before the provider update, acknowledged outbox=%d", acknowledged)
	}
	if err := postgres.ReleaseAcknowledgedRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM outbox_messages WHERE aggregate_id=$1 AND topic='review.run.acknowledged'`, run.ID).Scan(&acknowledged); err != nil {
		t.Fatal(err)
	}
	if acknowledged != 1 {
		t.Fatalf("provider-confirmed update must release exactly one run acknowledgement, got %d", acknowledged)
	}
	// A later force review must resolve the provider's current revision instead
	// of copying the terminal run's now-stale head SHA.
	if _, err := postgres.pool.Exec(ctx, `UPDATE review_runs SET state='completed', finished_at=now() WHERE id=$1`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE review_jobs SET state='succeeded', finished_at=now() WHERE id=$1`, *run.LegacyJobID); err != nil {
		t.Fatal(err)
	}
	forceComment := comment
	forceComment.DeliveryID = "force-command-" + tenantID.String()
	forceComment.CommentExternalID = "comment-22"
	forceComment.Body = "@openreview review --force --mode=security"
	forceOutcome, err := postgres.ProcessInteraction(ctx, domain.InteractionCommand{Event: forceComment, Command: "review", Mode: string(domain.ReviewModeSecurity), Normalized: forceComment.Body})
	if err != nil || !forceOutcome.Accepted || forceOutcome.RunID != nil || forceOutcome.Reason != "review admission acknowledged" {
		t.Fatalf("force command outcome=%#v error=%v", forceOutcome, err)
	}
	var forceInteractionID uuid.UUID
	if err := postgres.pool.QueryRow(ctx, `SELECT id FROM review_interactions WHERE provider_delivery_id=$1`, forceComment.DeliveryID).Scan(&forceInteractionID); err != nil {
		t.Fatal(err)
	}
	forceAdmission := domain.InteractionAdmission{InteractionID: forceInteractionID, Event: forceComment, Mode: domain.ReviewModeSecurity, ActorSubject: "reviewer", CredentialRef: "github-app"}
	if err := postgres.ReleaseInitialInteractionAdmission(ctx, forceAdmission); err != nil {
		t.Fatal(err)
	}
	currentEvent := event
	currentEvent.DeliveryID = "interaction-admission:" + forceInteractionID.String()
	currentEvent.HeadSHA = "current-head-sha"
	currentRun, currentReleased, err := postgres.AdmitInitialInteractionReview(ctx, forceInteractionID, forceComment, currentEvent)
	if err != nil || currentRun.ID == run.ID || !currentReleased || currentRun.HeadSHA != "current-head-sha" {
		t.Fatalf("force admission run=%#v released=%t error=%v", currentRun, currentReleased, err)
	}
}
