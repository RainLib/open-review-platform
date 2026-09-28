package terminal

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/messaging"
	"github.com/RainLib/open-review-platform/internal/publisher"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type terminalStaticResolver struct{}

func (terminalStaticResolver) Resolve(context.Context, domain.ReviewJob) (string, error) {
	return "provider-token", nil
}

// This integration covers both provider ambiguity windows in the terminal
// path. GitHub first accepts the Check creation and later accepts the lifecycle
// comment, but closes each TCP connection before the worker sees a response.
// Durable successors must discover and update those same provider objects;
// terminal recovery must never create duplicates or invoke review execution.
func TestTerminalReporterRecoversAcceptedProviderWritesWithoutDuplicates(t *testing.T) {
	for _, test := range []struct {
		name, jobState, runState, topic, failure, expectedSummary string
	}{
		{name: "cancelled", jobState: "cancelled", runState: "cancelled", topic: "review.run.cancelled", expectedSummary: "Review cancelled"},
		{name: "failed timeout", jobState: "failed", runState: "failed", topic: "review.run.failed", failure: domain.ErrReviewTimedOut.Error() + " after 30s", expectedSummary: "Review timed out"},
	} {
		t.Run(test.name, func(t *testing.T) {
			testTerminalReporterRecoversAcceptedProviderWritesWithoutDuplicates(t, test.jobState, test.runState, test.topic, test.failure, test.expectedSummary)
		})
	}
}

func testTerminalReporterRecoversAcceptedProviderWritesWithoutDuplicates(t *testing.T, jobState, runState, topic, failure, expectedSummary string) {
	const headSHA = "0123456789abcdef0123456789abcdef01234567"
	const baseSHA = "89abcdef0123456789abcdef0123456789abcdef"
	databaseURL := os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("OPEN_REVIEW_TEST_DATABASE_URL is not configured")
	}
	if os.Getenv("OPEN_REVIEW_TEST_ISOLATED_DATABASE") != "true" {
		t.Skip("terminal provider ambiguity verification requires an isolated database")
	}
	ctx := context.Background()
	database, err := store.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	var fixture struct {
		sync.Mutex
		checkCreated   bool
		commentCreated bool
		checkPosts     int
		checkPatches   int
		commentPosts   int
		commentPatches int
	}
	jobID := uuid.New()
	summaryMarker := "open-review-platform:summary:" + jobID.String()
	checkMarker := "open-review-platform:analysis-check:" + jobID.String()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.Lock()
		defer fixture.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/RainLib/demo/commits/"+headSHA+"/check-runs":
			if fixture.checkCreated {
				_ = json.NewEncoder(w).Encode(map[string]any{"check_runs": []map[string]any{{"id": 91, "status": "completed", "external_id": checkMarker}}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"check_runs": []any{}})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/RainLib/demo/check-runs":
			fixture.checkPosts++
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"name":"`+publisher.AnalysisCheckName+`"`) || !strings.Contains(string(body), `"head_sha":"`+headSHA+`"`) {
				t.Errorf("terminal check lost its stable identity: %s", body)
			}
			fixture.checkCreated = true
			disconnectAcceptedProviderWrite(t, w)
		case r.Method == http.MethodPatch && r.URL.Path == "/repos/RainLib/demo/check-runs/91":
			fixture.checkPatches++
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/repos/RainLib/demo/issues/3/comments":
			if fixture.commentCreated {
				_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 72, "body": "Cancelled\n\n<!-- " + summaryMarker + " -->"}})
				return
			}
			_ = json.NewEncoder(w).Encode([]any{})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/RainLib/demo/issues/3/comments":
			fixture.commentPosts++
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), summaryMarker) || !strings.Contains(string(body), expectedSummary) {
				t.Errorf("terminal lifecycle comment lost marker or specific outcome: %s", body)
			}
			fixture.commentCreated = true
			disconnectAcceptedProviderWrite(t, w)
		case r.Method == http.MethodPatch && r.URL.Path == "/repos/RainLib/demo/issues/comments/72":
			fixture.commentPatches++
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected provider request: %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	tenantID, installationID, deliveryID := uuid.New(), uuid.New(), uuid.New()
	requestID, runID := uuid.New(), uuid.New()
	tenantSlug := "terminal-ambiguity-" + tenantID.String()[:8]
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Terminal ambiguity')`, tenantID, tenantSlug)
	batch.Queue(`INSERT INTO provider_installations
		(id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref,verification_state)
		VALUES ($1,$2,'github',$3,'RainLib/*',$4,'test-provider','verified')`, installationID, tenantID, "terminal-install-"+installationID.String(), server.URL)
	batch.Queue(`INSERT INTO webhook_deliveries (id,provider,delivery_id,event_name,payload)
		VALUES ($1,'github',$2,'pull_request','{}'::jsonb)`, deliveryID, "terminal-delivery-"+deliveryID.String())
	batch.Queue(`INSERT INTO review_jobs
		(id,tenant_id,installation_id,delivery_id,provider,api_base_url,repository,clone_url,review_number,base_ref,base_sha,head_ref,head_sha,state,error_message)
		VALUES ($1,$2,$3,$4,'github',$5,'RainLib/demo','https://github.example/RainLib/demo.git',3,'main',$6,'feature/terminal',$7,$8,$9)`,
		jobID, tenantID, installationID, deliveryID, server.URL, baseSHA, headSHA, jobState, failure)
	batch.Queue(`INSERT INTO review_requests
		(id,tenant_id,installation_id,provider,api_base_url,repository,review_number)
		VALUES ($1,$2,$3,'github',$4,'RainLib/demo',3)`, requestID, tenantID, installationID, server.URL)
	batch.Queue(`INSERT INTO review_runs
		(id,request_id,legacy_job_id,state,trigger_kind,head_sha,base_sha,finished_at)
		VALUES ($1,$2,$3,$4,'pull_request',$5,$6,now())`, runID, requestID, jobID, runState, headSHA, baseSHA)
	if err := databaseBatch(ctx, pool, batch); err != nil {
		t.Fatalf("seed terminal ambiguity workflow: %v", err)
	}
	consumer := "terminal-provider-ambiguity-" + uuid.NewString()
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM inbox_messages WHERE consumer=$1`, consumer)
		_, _ = pool.Exec(context.Background(), `DELETE FROM outbox_messages WHERE aggregate_id=$1`, runID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM webhook_deliveries WHERE id=$1`, deliveryID)
	}()

	reporter := publisher.NewHTTPWithResolver(terminalStaticResolver{})
	handler := func(ctx context.Context, message domain.OutboxMessage) error {
		return Handle(ctx, database, database, reporter, reporter, message)
	}
	message := domain.OutboxMessage{
		ID: uuid.New(), AggregateID: runID, Topic: topic,
		Payload: map[string]any{"run_id": runID.String(), "terminal_publication_attempt": float64(1)},
	}
	if err := messaging.HandleExactlyOnce(ctx, database, consumer, message, handler); err != nil {
		t.Fatalf("first terminal attempt should hand off durable retry: %v", err)
	}
	assertTerminalReceipt(t, pool, runID, "status", false, "")

	second := terminalRetryMessage(t, ctx, pool, message.ID)
	if err := messaging.HandleExactlyOnce(ctx, database, consumer, second, handler); err != nil {
		t.Fatalf("second terminal attempt should hand off comment retry: %v", err)
	}
	assertTerminalReceipt(t, pool, runID, "status", true, "91")
	assertTerminalReceipt(t, pool, runID, "summary", false, "")

	third := terminalRetryMessage(t, ctx, pool, second.ID)
	if err := messaging.HandleExactlyOnce(ctx, database, consumer, third, handler); err != nil {
		t.Fatalf("third terminal attempt should complete recovered provider writes: %v", err)
	}
	assertTerminalReceipt(t, pool, runID, "status", true, "91")
	assertTerminalReceipt(t, pool, runID, "summary", true, "")

	fixture.Lock()
	checkPosts, checkPatches := fixture.checkPosts, fixture.checkPatches
	commentPosts, commentPatches := fixture.commentPosts, fixture.commentPatches
	fixture.Unlock()
	if checkPosts != 1 || checkPatches != 2 || commentPosts != 1 || commentPatches != 1 {
		t.Fatalf("provider writes check=POST:%d PATCH:%d comment=POST:%d PATCH:%d", checkPosts, checkPatches, commentPosts, commentPatches)
	}
	var inboxCompleted, successors int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM inbox_messages WHERE consumer=$1 AND state='completed'`, consumer).Scan(&inboxCompleted); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_messages WHERE aggregate_id=$1 AND topic=$2`, runID, topic).Scan(&successors); err != nil {
		t.Fatal(err)
	}
	if inboxCompleted != 3 || successors != 2 {
		t.Fatalf("durable handoff inbox_completed=%d successors=%d", inboxCompleted, successors)
	}
}

func disconnectAcceptedProviderWrite(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		t.Fatal("test provider does not support connection hijacking")
	}
	connection, _, err := hijacker.Hijack()
	if err != nil {
		t.Fatalf("hijack accepted provider connection: %v", err)
	}
	_ = connection.Close()
}

func databaseBatch(ctx context.Context, pool *pgxpool.Pool, batch *pgx.Batch) error {
	return pool.SendBatch(ctx, batch).Close()
}

func terminalRetryMessage(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sourceID uuid.UUID) domain.OutboxMessage {
	t.Helper()
	var message domain.OutboxMessage
	var encoded []byte
	err := pool.QueryRow(ctx, `
		SELECT id,aggregate_id,topic,payload
		FROM outbox_messages
		WHERE dedupe_key=$1`, "terminal-publication-retry:"+sourceID.String()).
		Scan(&message.ID, &message.AggregateID, &message.Topic, &encoded)
	if err != nil {
		t.Fatalf("load durable terminal successor: %v", err)
	}
	if err := json.Unmarshal(encoded, &message.Payload); err != nil {
		t.Fatalf("decode durable terminal successor: %v", err)
	}
	return message
}

func assertTerminalReceipt(t *testing.T, pool *pgxpool.Pool, runID uuid.UUID, kind string, published bool, externalID string) {
	t.Helper()
	var storedExternalID, lastError string
	var isPublished bool
	err := pool.QueryRow(context.Background(), `
		SELECT external_id,published_at IS NOT NULL,COALESCE(last_error,'')
		FROM publication_receipts
		WHERE run_id=$1 AND receipt_kind=$2`, runID, kind).
		Scan(&storedExternalID, &isPublished, &lastError)
	if err != nil {
		t.Fatalf("load %s terminal receipt: %v", kind, err)
	}
	if isPublished != published || storedExternalID != externalID {
		t.Fatalf("%s receipt published=%t external_id=%q last_error=%q", kind, isPublished, storedExternalID, lastError)
	}
	if published && lastError != "" {
		t.Fatalf("published %s receipt retained error %q", kind, lastError)
	}
	if !published && lastError == "" {
		t.Fatalf("unpublished %s receipt lost recovery boundary", kind)
	}
}
