package issuepublisher

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/messaging"
	"github.com/RainLib/open-review-platform/internal/publisher"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type staticResolver struct{}

func (staticResolver) Resolve(context.Context, domain.ReviewJob) (string, error) {
	return "provider-token", nil
}

// This integration reproduces the provider ambiguity window rather than
// returning a synthetic HTTP error: the provider records the Issue and then
// closes the TCP connection before the client receives a response. The first
// worker attempt therefore has to release its inbox claim and persist a failed
// receipt. Its retry must discover the stable marker, complete the same receipt
// and avoid a second provider create.
func TestProcessorRecoversProviderCreateAcceptedBeforeReceiptCommit(t *testing.T) {
	databaseURL := os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("OPEN_REVIEW_TEST_DATABASE_URL is not configured")
	}
	if os.Getenv("OPEN_REVIEW_TEST_ISOLATED_DATABASE") != "true" {
		t.Skip("provider ambiguity verification requires an isolated database")
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

	marker := "open-review-platform:external-issue:" + uuid.NewString()
	var created atomic.Bool
	var listRequests, createRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/RainLib/demo/issues" {
			t.Errorf("unexpected provider request: %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
			return
		}
		switch r.Method {
		case http.MethodGet:
			listRequests.Add(1)
			if r.URL.Query().Get("state") != "all" || r.URL.Query().Get("per_page") != "100" || r.URL.Query().Get("page") != "1" {
				t.Errorf("unexpected marker lookup query: %s", r.URL.RawQuery)
			}
			if created.Load() {
				_ = json.NewEncoder(w).Encode([]map[string]any{{
					"id": 701, "number": 17,
					"html_url": "https://github.example/RainLib/demo/issues/17",
					"body":     "Accepted before disconnect\n\n<!-- " + marker + " -->",
				}})
				return
			}
			_ = json.NewEncoder(w).Encode([]any{})
		case http.MethodPost:
			createRequests.Add(1)
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), marker) {
				t.Errorf("provider create lost stable marker: %s", body)
			}
			created.Store(true)
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Error("test provider does not support connection hijacking")
				return
			}
			connection, _, err := hijacker.Hijack()
			if err != nil {
				t.Errorf("hijack accepted provider connection: %v", err)
				return
			}
			_ = connection.Close()
		default:
			t.Errorf("unexpected provider method: %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	tenantID, installationID, issueID, receiptID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	tenantSlug := "provider-ambiguity-" + tenantID.String()[:8]
	if _, err := pool.Exec(ctx, `INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Provider ambiguity')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) }()
	if _, err := pool.Exec(ctx, `
		INSERT INTO provider_installations
			(id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref,verification_state)
		VALUES ($1,$2,'github',$3,'RainLib/*',$4,'test-provider','verified')`,
		installationID, tenantID, "ambiguity-install-"+installationID.String(), server.URL); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO issue_auto_create_policies
			(tenant_id,enabled,repository_scopes,title_template,body_template,updated_by)
		VALUES ($1,TRUE,$2,'[Open Review] {{path}}','{{evidence}}','owner')`, tenantID, []string{"RainLib/*"}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO review_issues
			(id,tenant_id,provider,api_base_url,repository,fingerprint,path,severity,category,body_preview)
		VALUES ($1,$2,'github',$3,'RainLib/demo',$4,'internal/api/server.go','high','security','unsafe redirect')`,
		issueID, tenantID, server.URL, "ambiguity-"+issueID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO external_issue_receipts
			(id,tenant_id,issue_id,installation_id,policy_revision,provider,api_base_url,repository,trigger,stable_marker,title,body,labels)
		VALUES ($1,$2,$3,$4,1,'github',$5,'RainLib/demo','first_seen',$6,'[Open Review] high security','Evidence\n\n<!-- ' || $6 || ' -->',ARRAY['open-review'])`,
		receiptID, tenantID, issueID, installationID, server.URL, marker); err != nil {
		t.Fatal(err)
	}

	provider := publisher.NewHTTPWithResolver(staticResolver{})
	processor := Processor{Store: database, Provider: provider}
	message := domain.OutboxMessage{
		ID: uuid.New(), AggregateID: receiptID, Topic: "external.issue.create",
		Payload: map[string]any{"receipt_id": receiptID.String()},
	}
	consumer := "provider-ambiguity-" + uuid.NewString()
	firstErr := messaging.HandleExactlyOnce(ctx, database, consumer, message, processor.Handle)
	if firstErr == nil {
		t.Fatal("provider disconnect after accepted create must release the first attempt")
	}
	var firstState, firstInboxState string
	var firstAttempts, firstInboxAttempts int
	if err := pool.QueryRow(ctx, `SELECT state,attempts FROM external_issue_receipts WHERE id=$1`, receiptID).Scan(&firstState, &firstAttempts); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT state,attempt FROM inbox_messages WHERE consumer=$1 AND message_id=$2`, consumer, message.ID).Scan(&firstInboxState, &firstInboxAttempts); err != nil {
		t.Fatal(err)
	}
	if firstState != "failed" || firstAttempts != 1 || firstInboxState != "released" || firstInboxAttempts != 1 {
		t.Fatalf("first attempt receipt=%s/%d inbox=%s/%d", firstState, firstAttempts, firstInboxState, firstInboxAttempts)
	}

	if err := messaging.HandleExactlyOnce(ctx, database, consumer, message, processor.Handle); err != nil {
		t.Fatalf("recover accepted provider create: %v", err)
	}
	var state, externalID, externalURL, inboxState string
	var attempts, inboxAttempts int
	if err := pool.QueryRow(ctx, `SELECT state,external_id,external_url,attempts FROM external_issue_receipts WHERE id=$1`, receiptID).Scan(&state, &externalID, &externalURL, &attempts); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT state,attempt FROM inbox_messages WHERE consumer=$1 AND message_id=$2`, consumer, message.ID).Scan(&inboxState, &inboxAttempts); err != nil {
		t.Fatal(err)
	}
	if state != "created" || externalID != "17" || !strings.HasSuffix(externalURL, "/issues/17") || attempts != 2 || inboxState != "completed" || inboxAttempts != 2 {
		t.Fatalf("recovered receipt=%s/%s/%s/%d inbox=%s/%d", state, externalID, externalURL, attempts, inboxState, inboxAttempts)
	}
	if createRequests.Load() != 1 || listRequests.Load() != 2 {
		t.Fatalf("provider requests create=%d list=%d, want 1/2", createRequests.Load(), listRequests.Load())
	}
	var createdAudits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND action='external_issue.created' AND target=$2`, tenantID, issueID.String()).Scan(&createdAudits); err != nil {
		t.Fatal(err)
	}
	if createdAudits != 1 {
		t.Fatalf("external issue created audit count=%d, want 1", createdAudits)
	}
	_, _ = pool.Exec(ctx, `DELETE FROM inbox_messages WHERE consumer=$1`, consumer)
}
