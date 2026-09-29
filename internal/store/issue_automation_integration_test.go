package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestIssueAutoCreatePolicyQueuesOneDurableProviderReceipt(t *testing.T) {
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
	tenantID, installationID := uuid.New(), uuid.New()
	tenantSlug := "issue-auto-" + tenantID.String()[:8]
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Issue automation')`, tenantID, tenantSlug)
	batch.Queue(`INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner','owner'),($1,'reviewer','reviewer')`, tenantID)
	batch.Queue(`INSERT INTO provider_installations (id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref,verification_state) VALUES ($1,$2,'github',$3,'RainLib/*','https://api.github.com','github-app','verified')`, installationID, tenantID, "issue-auto-install-"+tenantID.String())
	if err := postgres.pool.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatalf("seed issue automation tenant: %v", err)
	}
	defer func() { _, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) }()

	defaultPolicy, err := postgres.GetIssueAutoCreatePolicy(ctx, "owner", tenantSlug)
	if err != nil || defaultPolicy.Enabled || defaultPolicy.Revision != 0 || !defaultPolicy.CanManage {
		t.Fatalf("default policy=%#v err=%v", defaultPolicy, err)
	}
	policy, err := postgres.SaveIssueAutoCreatePolicy(ctx, "owner", tenantSlug, domain.IssueAutoCreatePolicy{
		Revision:         0,
		Enabled:          true,
		Target:           domain.IssueAutoCreateTargetProvider,
		RepositoryScopes: []string{"RainLib/*"},
		MinimumSeverity:  "high",
		Categories:       []string{"security"},
		TriggerFirstSeen: true,
		TriggerRegressed: true,
		Labels:           []string{"security", "open-review"},
		TitleTemplate:    "[Review] {{severity}} {{category}} {{path}}",
		BodyTemplate:     "{{repository}} {{fingerprint}} {{evidence}} {{suggestion}}",
	})
	if err != nil || policy.Revision != 1 || policy.UpdatedBy != "owner" {
		t.Fatalf("save policy=%#v err=%v", policy, err)
	}
	if _, err := postgres.SaveIssueAutoCreatePolicy(ctx, "reviewer", tenantSlug, policy); !errors.Is(err, ErrForbidden) {
		t.Fatalf("reviewer save error=%v, want ErrForbidden", err)
	}

	requestID := uuid.New()
	jobID := seedIssueJob(t, ctx, postgres, tenantID, installationID, requestID, 31, "issue-auto-head-1")
	finding := domain.Finding{Path: "internal/api/server.go", StartLine: 40, EndLine: 42, Severity: "high", Category: "security", Body: "Untrusted redirect target", Suggestion: "Validate the target."}
	if err := postgres.SaveFindings(ctx, jobID, []domain.Finding{finding}); err != nil {
		t.Fatalf("save first automated finding: %v", err)
	}
	var receiptID uuid.UUID
	var issueID uuid.UUID
	var marker, title, body, topic string
	if err := postgres.pool.QueryRow(ctx, `
		SELECT receipt.id,receipt.issue_id,receipt.stable_marker,receipt.title,receipt.body,outbox.topic
		FROM external_issue_receipts receipt
		JOIN outbox_messages outbox ON outbox.aggregate_id=receipt.id
		WHERE receipt.tenant_id=$1`, tenantID).Scan(&receiptID, &issueID, &marker, &title, &body, &topic); err != nil {
		t.Fatalf("load queued external issue receipt: %v", err)
	}
	if topic != "external.issue.create" || !strings.Contains(marker, issueID.String()) || !strings.Contains(title, "high security") || !strings.Contains(body, marker) {
		t.Fatalf("unexpected durable receipt: marker=%q title=%q body=%q topic=%q", marker, title, body, topic)
	}
	publication, err := postgres.ExternalIssuePublication(ctx, receiptID)
	if err != nil || publication.CredentialRef != "github-app" || publication.InstallationExternalID == "" || publication.State != domain.ExternalIssuePublicationQueued {
		t.Fatalf("worker publication=%#v err=%v", publication, err)
	}
	if err := postgres.MarkExternalIssuePublicationCreated(ctx, receiptID, "31", "https://github.example/RainLib/demo/issues/31"); err != nil {
		t.Fatal(err)
	}
	detail, err := postgres.GetIssue(ctx, "owner", tenantSlug, issueID)
	if err != nil || detail.ExternalIssue == nil || detail.ExternalIssue.State != domain.ExternalIssuePublicationCreated || detail.ExternalIssue.ExternalID != "31" {
		t.Fatalf("issue detail external receipt=%#v err=%v", detail.ExternalIssue, err)
	}
	if err := postgres.MarkExternalIssuePublicationCreated(ctx, receiptID, "31", "https://github.example/RainLib/demo/issues/31"); err != nil {
		t.Fatalf("replayed receipt completion must be safe: %v", err)
	}

	// A second occurrence of the stable fingerprint refreshes its aggregate but
	// cannot create another provider Issue because receipt uniqueness is the
	// authority across every trigger type and broker replay.
	secondJob := seedIssueJob(t, ctx, postgres, tenantID, installationID, requestID, 31, "issue-auto-head-2")
	if err := postgres.SaveFindings(ctx, secondJob, []domain.Finding{finding}); err != nil {
		t.Fatalf("save repeated finding: %v", err)
	}
	var receipts int
	if err := postgres.pool.QueryRow(ctx, `SELECT COUNT(*) FROM external_issue_receipts WHERE tenant_id=$1`, tenantID).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatalf("external receipt count=%d err=%v, want one", receipts, err)
	}
	previewPolicy := policy
	previewPolicy.Enabled = true
	preview, err := postgres.PreviewIssueAutoCreatePolicy(ctx, "reviewer", tenantSlug, previewPolicy)
	if err != nil || len(preview.Candidates) != 1 || !preview.Candidates[0].AlreadyPublished || preview.Candidates[0].IssueID != issueID {
		t.Fatalf("preview=%#v err=%v", preview, err)
	}
	resolvedJob := seedIssueJob(t, ctx, postgres, tenantID, installationID, requestID, 31, "issue-auto-head-resolved")
	if err := postgres.SaveFindings(ctx, resolvedJob, nil); err != nil {
		t.Fatalf("resolve automated finding: %v", err)
	}
	var closeTopic string
	if err := postgres.pool.QueryRow(ctx, `
		SELECT topic FROM outbox_messages
		WHERE aggregate_id=$1 AND topic='external.issue.close'`, receiptID).Scan(&closeTopic); err != nil || closeTopic != "external.issue.close" {
		t.Fatalf("resolved aggregate close topic=%q err=%v", closeTopic, err)
	}
	if err := postgres.MarkExternalIssuePublicationClosed(ctx, receiptID); err != nil {
		t.Fatal(err)
	}
	detail, err = postgres.GetIssue(ctx, "owner", tenantSlug, issueID)
	if err != nil || detail.Status != domain.IssueResolved || detail.ExternalIssue == nil || detail.ExternalIssue.State != domain.ExternalIssuePublicationClosed {
		t.Fatalf("resolved issue detail=%#v err=%v", detail, err)
	}

	// This distinct fingerprint is still queued when the owner turns the policy
	// off. Disabling must record an explicit terminal non-write rather than
	// leaving a stale receipt for a future broker retry to publish.
	queuedRequestID := uuid.New()
	queuedJob := seedIssueJob(t, ctx, postgres, tenantID, installationID, queuedRequestID, 32, "issue-auto-head-queued")
	queuedFinding := finding
	queuedFinding.Path = "internal/api/queued.go"
	if err := postgres.SaveFindings(ctx, queuedJob, []domain.Finding{queuedFinding}); err != nil {
		t.Fatalf("save queued-policy finding: %v", err)
	}
	var queuedReceiptID, queuedIssueID uuid.UUID
	if err := postgres.pool.QueryRow(ctx, `
		SELECT receipt.id,receipt.issue_id
		FROM external_issue_receipts receipt
		WHERE receipt.tenant_id=$1 AND receipt.issue_id <> $2`, tenantID, issueID).Scan(&queuedReceiptID, &queuedIssueID); err != nil {
		t.Fatalf("load queued receipt before disabling policy: %v", err)
	}
	if _, err := postgres.ExternalIssuePublication(ctx, queuedReceiptID); err != nil {
		t.Fatalf("queued receipt must be publishable before policy disable: %v", err)
	}

	disabled := policy
	disabled.Enabled = false
	if _, err := postgres.SaveIssueAutoCreatePolicy(ctx, "owner", tenantSlug, disabled); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.ExternalIssuePublication(ctx, queuedReceiptID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled policy must withdraw queued provider publication: %v", err)
	}
	if err := postgres.CancelExternalIssuePublication(ctx, queuedReceiptID, "worker observed disabled policy"); err != nil {
		t.Fatalf("replayed cancellation must be safe: %v", err)
	}
	queuedDetail, err := postgres.GetIssue(ctx, "owner", tenantSlug, queuedIssueID)
	if err != nil || queuedDetail.ExternalIssue == nil || queuedDetail.ExternalIssue.State != domain.ExternalIssuePublicationCancelled || !strings.Contains(queuedDetail.ExternalIssue.LastError, "Policy disabled") {
		t.Fatalf("cancelled external receipt=%#v err=%v", queuedDetail.ExternalIssue, err)
	}
	thirdJob := seedIssueJob(t, ctx, postgres, tenantID, installationID, queuedRequestID, 32, "issue-auto-head-3")
	newFinding := finding
	newFinding.Path = "internal/api/new.go"
	if err := postgres.SaveFindings(ctx, thirdJob, []domain.Finding{newFinding}); err != nil {
		t.Fatalf("save disabled-policy finding: %v", err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT COUNT(*) FROM external_issue_receipts WHERE tenant_id=$1`, tenantID).Scan(&receipts); err != nil || receipts != 2 {
		t.Fatalf("disabled policy created a receipt count=%d err=%v", receipts, err)
	}
}
