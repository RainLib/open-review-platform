package store

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestFindingExplorerFiltersAcrossFullHistoryAndPages(t *testing.T) {
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
	tenantID, installationID, deliveryID, jobID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	requestID, runID := uuid.New(), uuid.New()
	headSHA := strings.Repeat("a", 40)
	slug := "finding-explorer-" + tenantID.String()[:8]
	defer func() {
		_, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID)
		_, _ = postgres.pool.Exec(context.Background(), `DELETE FROM webhook_deliveries WHERE id=$1`, deliveryID)
	}()
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO tenants(id,slug,name) VALUES($1,$2,'Finding explorer test')`, tenantID, slug)
	batch.Queue(`INSERT INTO memberships(tenant_id,subject,role) VALUES($1,'owner','owner')`, tenantID)
	batch.Queue(`INSERT INTO provider_installations(id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref) VALUES($1,$2,'github',$3,'RainLib/*','https://api.github.com','secret://test')`, installationID, tenantID, uuid.NewString())
	batch.Queue(`INSERT INTO webhook_deliveries(id,provider,delivery_id,event_name,payload) VALUES($1,'github',$2,'pull_request','{}'::jsonb)`, deliveryID, uuid.NewString())
	batch.Queue(`INSERT INTO review_jobs(id,tenant_id,installation_id,delivery_id,provider,api_base_url,repository,clone_url,review_number,base_ref,head_ref,head_sha,state) VALUES($1,$2,$3,$4,'github','https://api.github.com','RainLib/open-review-platform','https://github.com/RainLib/open-review-platform.git',3,'main','finding-test',$5,'succeeded')`, jobID, tenantID, installationID, deliveryID, headSHA)
	batch.Queue(`INSERT INTO review_requests(id,tenant_id,installation_id,provider,api_base_url,repository,review_number) VALUES($1,$2,$3,'github','https://api.github.com','RainLib/open-review-platform',3)`, requestID, tenantID, installationID)
	batch.Queue(`INSERT INTO review_runs(id,request_id,legacy_job_id,state,trigger_kind,head_sha) VALUES($1,$2,$3,'completed','pull_request',$4)`, runID, requestID, jobID, headSHA)
	oldHighRiskID := uuid.New()
	for index := 0; index < 35; index++ {
		findingID := uuid.New()
		severity := "medium"
		if index == 0 {
			findingID, severity = oldHighRiskID, "high"
		}
		batch.Queue(`INSERT INTO review_findings(id,job_id,path,start_line,end_line,severity,category,body,fingerprint,created_at) VALUES($1,$2,$3,10,10,$4,'security',$5,$6,$7)`, findingID, jobID, fmt.Sprintf("src/file-%02d.go", index), severity, fmt.Sprintf("finding %02d", index), uuid.NewString(), time.Now().UTC().Add(time.Duration(index-36)*time.Hour))
	}
	actionedID := uuid.New()
	batch.Queue(`INSERT INTO review_findings(id,job_id,path,start_line,end_line,severity,category,body,fingerprint,created_at) VALUES($1,$2,'src/actioned.go',1,1,'critical','security','actioned finding',$3,now())`, actionedID, jobID, uuid.NewString())
	batch.Queue(`INSERT INTO finding_feedback(tenant_id,finding_id,provider,delivery_id,reaction_external_id,actor_external_id,kind) VALUES($1,$2,'console','console',$3,'owner','resolved')`, tenantID, actionedID, uuid.NewString())
	if err := postgres.pool.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatalf("seed findings: %v", err)
	}
	filter := domain.FindingFilter{View: domain.FindingActive, Limit: 25}
	first, err := postgres.ListFindings(ctx, "owner", slug, filter)
	if err != nil || len(first.Findings) != 25 || first.NextCursor == "" {
		t.Fatalf("first page count=%d next=%q err=%v", len(first.Findings), first.NextCursor, err)
	}
	if first.Findings[0].RunID == nil || *first.Findings[0].RunID != runID || first.Findings[0].HeadSHA != headSHA {
		t.Fatalf("finding lost its exact run or revision: %+v", first.Findings[0])
	}
	filter.Cursor = first.NextCursor
	second, err := postgres.ListFindings(ctx, "owner", slug, filter)
	if err != nil || len(second.Findings) != 10 || second.Findings[9].ID != oldHighRiskID || second.PreviousCursor == "" {
		t.Fatalf("second page=%#v err=%v", second, err)
	}
	filter.Cursor, filter.CursorDirection = second.PreviousCursor, domain.WorkQueueCursorBefore
	back, err := postgres.ListFindings(ctx, "owner", slug, filter)
	if err != nil || len(back.Findings) != 25 || back.Findings[0].ID != first.Findings[0].ID {
		t.Fatalf("backward page count=%d err=%v", len(back.Findings), err)
	}
	high, err := postgres.ListFindings(ctx, "owner", slug, domain.FindingFilter{View: domain.FindingHighRisk, Limit: 25})
	if err != nil || len(high.Findings) != 1 || high.Findings[0].ID != oldHighRiskID {
		t.Fatalf("historic high-risk finding lost: %#v err=%v", high, err)
	}
	if high.Findings[0].RunID == nil || *high.Findings[0].RunID != runID || high.Findings[0].HeadSHA != headSHA {
		t.Fatalf("historic high-risk finding lost exact evidence target: %+v", high.Findings[0])
	}
	actioned, err := postgres.ListFindings(ctx, "owner", slug, domain.FindingFilter{View: domain.FindingActioned, Limit: 25})
	if err != nil || len(actioned.Findings) != 1 || actioned.Findings[0].ID != actionedID || actioned.Findings[0].ActorDisposition != "resolved" {
		t.Fatalf("actor disposition view=%#v err=%v", actioned, err)
	}
	searched, err := postgres.ListFindings(ctx, "owner", slug, domain.FindingFilter{View: domain.FindingHighRisk, Repository: "RainLib/open-review-platform", Query: "file-00", Limit: 25})
	if err != nil || len(searched.Findings) != 1 || searched.Findings[0].ID != oldHighRiskID {
		t.Fatalf("filtered historic finding=%#v err=%v", searched, err)
	}
	filter.View = domain.FindingHighRisk
	if _, err := postgres.ListFindings(ctx, "owner", slug, filter); err != ErrInvalidFindingFilter {
		t.Fatalf("cursor reused in a different view err=%v", err)
	}
	if _, err := postgres.ListFindings(ctx, "outsider", slug, domain.FindingFilter{View: domain.FindingActive, Limit: 25}); err != ErrForbidden {
		t.Fatalf("outsider finding access err=%v", err)
	}
	dashboard, err := postgres.GetFindingFeedbackDashboard(ctx, "owner", slug, 10)
	if err != nil || len(dashboard.RecentFindings) == 0 || dashboard.RecentFindings[0].RunID == nil || *dashboard.RecentFindings[0].RunID != runID || dashboard.RecentFindings[0].HeadSHA != headSHA {
		t.Fatalf("recent finding exact evidence target=%#v err=%v", dashboard.RecentFindings, err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE review_runs SET head_sha=$2 WHERE id=$1`, runID, strings.Repeat("b", 40)); err != nil {
		t.Fatalf("change run revision: %v", err)
	}
	mismatch, err := postgres.ListFindings(ctx, "owner", slug, domain.FindingFilter{View: domain.FindingHighRisk, Limit: 25})
	if err != nil || len(mismatch.Findings) != 1 || mismatch.Findings[0].RunID != nil || mismatch.Findings[0].HeadSHA != headSHA {
		t.Fatalf("mismatched run must not be advertised as exact evidence: %#v err=%v", mismatch, err)
	}
}
