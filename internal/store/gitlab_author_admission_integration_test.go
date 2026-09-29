package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/webhook"
	"github.com/google/uuid"
)

func TestGitLabAuthorAdmissionDefersDedupesAndAppliesVerifiedAuthor(t *testing.T) {
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
	slug := "gitlab-author-" + tenantID.String()[:8]
	repository := "group/service-" + tenantID.String()[:8]
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants(id,slug,name) VALUES($1,$2,'GitLab author admission test')`, tenantID, slug); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = postgres.pool.Exec(context.Background(), `DELETE FROM gitlab_author_admissions WHERE installation_id=$1`, installationID)
		_, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID)
		_, _ = postgres.pool.Exec(context.Background(), `DELETE FROM webhook_deliveries WHERE delivery_id LIKE $1`, "gitlab-author-"+tenantID.String()+"%")
	}()
	for _, query := range []string{
		`INSERT INTO memberships(tenant_id,subject,role) VALUES($1,'owner','owner')`,
		`INSERT INTO workspace_setup_checkpoints(tenant_id,current_step,revision,updated_by,completed_at) VALUES($1,'complete',1,'owner',now())`,
	} {
		if _, err := postgres.pool.Exec(ctx, query, tenantID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO provider_installations(id,tenant_id,provider,external_id,repository_scope,
			automatic_reviews,api_base_url,credential_ref,verification_state)
		VALUES($1,$2,'gitlab',$3,$4,TRUE,'https://gitlab.example/api/v4','gitlab-token','verified')`,
		installationID, tenantID, "scope-"+tenantID.String(), repository); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.SaveReviewConfig(ctx, "owner", slug, domain.ReviewConfigInput{
		Section: domain.ReviewConfigGeneral, ScopeKind: domain.ReviewConfigTenantScope,
		Content: json.RawMessage(`{"automatic_review":true,"review_drafts":true,"rereview_on_push":true}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.SaveReviewConfig(ctx, "owner", slug, domain.ReviewConfigInput{
		Section: domain.ReviewConfigFilters, ScopeKind: domain.ReviewConfigTenantScope,
		Content: json.RawMessage(`{"exclude_authors":["renovate"]}`),
	}); err != nil {
		t.Fatal(err)
	}
	makeEvent := func(number int) domain.InboundEvent {
		t.Helper()
		payload := []byte(fmt.Sprintf(`{"user":{"id":999,"username":"event-actor"},"project":{"id":17,"path_with_namespace":%q,"git_http_url":%q},"object_attributes":{"action":"update","author_id":321,"title":"Review author correctly","iid":%d,"target_branch":"main","source_branch":"feature","last_commit":{"id":"%s"}}}`, repository, "https://gitlab.example/"+repository+".git", number, strings.Repeat("a", 40)))
		event, accepted, err := webhook.NormalizeGitLab("gitlab-author-"+tenantID.String()+fmt.Sprintf("-%d", number), "Merge Request Hook", payload, time.Now().UTC())
		if err != nil || !accepted || event.Author != "" {
			t.Fatalf("normalize actor-not-author event: %#v accepted=%t err=%v", event, accepted, err)
		}
		return event
	}
	first := makeEvent(9)
	deferred, duplicate, err := postgres.Enqueue(ctx, first)
	if err != nil || duplicate || deferred.ID != uuid.Nil || deferred.State != domain.JobQueued {
		t.Fatalf("deferred webhook job=%#v duplicate=%t err=%v", deferred, duplicate, err)
	}
	overview, err := postgres.GetPlatformHealthOverview(ctx, "owner", slug)
	if err != nil {
		t.Fatal(err)
	}
	var pendingAuthorQueue *domain.QueueHealth
	for index := range overview.Queues {
		if overview.Queues[index].Key == "gitlab-author-admission" {
			pendingAuthorQueue = &overview.Queues[index]
			break
		}
	}
	if pendingAuthorQueue == nil || pendingAuthorQueue.Ready != 1 || pendingAuthorQueue.Failed != 0 {
		t.Fatalf("deferred GitLab author work is not visible to the tenant: %#v", pendingAuthorQueue)
	}
	if len(overview.GitLabAuthorAdmissions) != 1 || overview.GitLabAuthorAdmissions[0].InstallationID != installationID || overview.GitLabAuthorAdmissions[0].Repository != repository ||
		overview.GitLabAuthorAdmissions[0].ReviewNumber != 9 || overview.GitLabAuthorAdmissions[0].State != "queued" {
		t.Fatalf("deferred GitLab author detail is missing: %#v", overview.GitLabAuthorAdmissions)
	}
	if _, duplicate, err := postgres.Enqueue(ctx, first); err != nil || !duplicate {
		t.Fatalf("duplicate webhook admitted again: duplicate=%t err=%v", duplicate, err)
	}
	target, err := postgres.ClaimGitLabAuthorAdmission(ctx, "worker-a", time.Minute)
	if err != nil || target.Job.InstallationID != installationID || target.ExpectedAuthorID != "321" || target.Job.HeadSHA != first.HeadSHA {
		t.Fatalf("claim target=%#v err=%v", target, err)
	}
	verified, accepted, err := webhook.NormalizeGitLab(target.DeliveryExternalID, "Merge Request Hook", target.Payload, target.ReceivedAt)
	if err != nil || !accepted {
		t.Fatalf("normalize retained delivery: accepted=%t err=%v", accepted, err)
	}
	verified.Author = "developer"
	if err := postgres.CompleteGitLabAuthorAdmission(ctx, *target, verified); err != nil {
		t.Fatal(err)
	}
	if err := postgres.CompleteGitLabAuthorAdmission(ctx, *target, verified); !errors.Is(err, ErrJobClaimLost) {
		t.Fatalf("replayed lease completion=%v, want ErrJobClaimLost", err)
	}
	var jobCount, queuedCount int
	var author string
	if err := postgres.pool.QueryRow(ctx, `SELECT COUNT(*) FROM review_jobs WHERE tenant_id=$1`, tenantID).Scan(&jobCount); err != nil {
		t.Fatal(err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT author FROM review_requests WHERE tenant_id=$1 AND review_number=9`, tenantID).Scan(&author); err != nil {
		t.Fatal(err)
	}
	if jobCount != 1 || author != "developer" {
		t.Fatalf("verified author did not create one review: jobs=%d author=%q", jobCount, author)
	}
	second := makeEvent(10)
	if job, _, err := postgres.Enqueue(ctx, second); err != nil || job.State != domain.JobQueued {
		t.Fatalf("second deferred webhook=%#v err=%v", job, err)
	}
	secondTarget, err := postgres.ClaimGitLabAuthorAdmission(ctx, "worker-b", time.Minute)
	if err != nil || secondTarget.Job.ReviewNumber != 10 {
		t.Fatalf("claim second author=%#v err=%v", secondTarget, err)
	}
	excluded, _, err := webhook.NormalizeGitLab(secondTarget.DeliveryExternalID, "Merge Request Hook", secondTarget.Payload, secondTarget.ReceivedAt)
	if err != nil {
		t.Fatal(err)
	}
	excluded.Author = "renovate"
	if err := postgres.CompleteGitLabAuthorAdmission(ctx, *secondTarget, excluded); err != nil {
		t.Fatal(err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT COUNT(*) FROM review_jobs WHERE tenant_id=$1`, tenantID).Scan(&queuedCount); err != nil {
		t.Fatal(err)
	}
	if queuedCount != 1 {
		t.Fatalf("excluded author created a second review: %d jobs", queuedCount)
	}
	var state string
	if err := postgres.pool.QueryRow(ctx, `SELECT state FROM gitlab_author_admissions WHERE delivery_id=$1`, secondTarget.DeliveryID).Scan(&state); err != nil || state != "skipped" {
		t.Fatalf("excluded author state=%q err=%v", state, err)
	}

	oldHead := makeEvent(11)
	if job, _, err := postgres.Enqueue(ctx, oldHead); err != nil || job.State != domain.JobQueued {
		t.Fatalf("old head was not deferred: job=%#v err=%v", job, err)
	}
	oldTarget, err := postgres.ClaimGitLabAuthorAdmission(ctx, "worker-c", time.Minute)
	if err != nil || oldTarget.Job.ReviewNumber != 11 {
		t.Fatalf("claim old head target=%#v err=%v", oldTarget, err)
	}
	newHeadSHA := strings.Repeat("b", 40)
	newPayload := []byte(fmt.Sprintf(`{"user":{"id":321,"username":"developer"},"project":{"id":17,"path_with_namespace":%q,"git_http_url":%q},"object_attributes":{"action":"update","author_id":321,"title":"Review new head","iid":11,"target_branch":"main","source_branch":"feature","last_commit":{"id":"%s"}}}`, repository, "https://gitlab.example/"+repository+".git", newHeadSHA))
	newHead, accepted, err := webhook.NormalizeGitLab("gitlab-author-"+tenantID.String()+"-11-new", "Merge Request Hook", newPayload, time.Now().UTC())
	if err != nil || !accepted || newHead.Author != "developer" {
		t.Fatalf("normalize new head=%#v accepted=%t err=%v", newHead, accepted, err)
	}
	if job, duplicate, err := postgres.Enqueue(ctx, newHead); err != nil || duplicate || job.ID == uuid.Nil {
		t.Fatalf("newer head was not admitted: job=%#v duplicate=%t err=%v", job, duplicate, err)
	}
	stale, accepted, err := webhook.NormalizeGitLab(oldTarget.DeliveryExternalID, "Merge Request Hook", oldTarget.Payload, oldTarget.ReceivedAt)
	if err != nil || !accepted {
		t.Fatalf("normalize old head: accepted=%t err=%v", accepted, err)
	}
	stale.Author = "developer"
	if err := postgres.CompleteGitLabAuthorAdmission(ctx, *oldTarget, stale); err != nil {
		t.Fatal(err)
	}
	var currentHead string
	if err := postgres.pool.QueryRow(ctx, `
		SELECT run.head_sha FROM review_requests request
		JOIN review_runs run ON run.id=request.current_run_id
		WHERE request.tenant_id=$1 AND request.review_number=11`, tenantID).Scan(&currentHead); err != nil {
		t.Fatal(err)
	}
	if err := postgres.pool.QueryRow(ctx, `SELECT state FROM gitlab_author_admissions WHERE delivery_id=$1`, oldTarget.DeliveryID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if currentHead != newHeadSHA || state != "skipped" {
		t.Fatalf("old deferred head displaced newer head: current=%q admission=%q", currentHead, state)
	}

	if job, _, err := postgres.Enqueue(ctx, makeEvent(12)); err != nil || job.State != domain.JobQueued {
		t.Fatalf("failing author lookup was not deferred: job=%#v err=%v", job, err)
	}
	for attempt := 1; attempt <= 5; attempt++ {
		failureTarget, err := postgres.ClaimGitLabAuthorAdmission(ctx, "worker-failed", time.Minute)
		if err != nil || failureTarget.Job.ReviewNumber != 12 || failureTarget.Attempt != attempt {
			t.Fatalf("claim failing attempt %d: target=%#v err=%v", attempt, failureTarget, err)
		}
		if err := postgres.RetryGitLabAuthorAdmission(ctx, *failureTarget, time.Now().Add(time.Minute), "provider_read_unavailable"); err != nil {
			t.Fatal(err)
		}
		if attempt < 5 {
			if _, err := postgres.pool.Exec(ctx, `UPDATE gitlab_author_admissions SET available_at=now() WHERE delivery_id=$1`, failureTarget.DeliveryID); err != nil {
				t.Fatal(err)
			}
		}
	}
	overview, err = postgres.GetPlatformHealthOverview(ctx, "owner", slug)
	if err != nil {
		t.Fatal(err)
	}
	var failedAuthorQueue *domain.QueueHealth
	for index := range overview.Queues {
		if overview.Queues[index].Key == "gitlab-author-admission" {
			failedAuthorQueue = &overview.Queues[index]
			break
		}
	}
	if failedAuthorQueue == nil || failedAuthorQueue.Ready != 0 || failedAuthorQueue.Failed != 1 {
		t.Fatalf("terminal author failure is not visible to the tenant: %#v", failedAuthorQueue)
	}
	if len(overview.GitLabAuthorAdmissions) != 1 || overview.GitLabAuthorAdmissions[0].ReviewNumber != 12 ||
		overview.GitLabAuthorAdmissions[0].State != "failed" || overview.GitLabAuthorAdmissions[0].Attempt != 5 ||
		overview.GitLabAuthorAdmissions[0].ErrorCode != "provider_read_unavailable" {
		t.Fatalf("terminal GitLab author failure detail is missing: %#v", overview.GitLabAuthorAdmissions)
	}
	foreignTenantID, foreignInstallationID, foreignDeliveryID := uuid.New(), uuid.New(), uuid.New()
	defer func() {
		cleanup := context.Background()
		_, _ = postgres.pool.Exec(cleanup, `DELETE FROM gitlab_author_admissions WHERE delivery_id=$1`, foreignDeliveryID)
		_, _ = postgres.pool.Exec(cleanup, `DELETE FROM provider_installations WHERE id=$1`, foreignInstallationID)
		_, _ = postgres.pool.Exec(cleanup, `DELETE FROM tenants WHERE id=$1`, foreignTenantID)
		_, _ = postgres.pool.Exec(cleanup, `DELETE FROM webhook_deliveries WHERE id=$1`, foreignDeliveryID)
	}()
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants(id,slug,name) VALUES($1,$2,'Foreign GitLab health')`, foreignTenantID, "foreign-author-"+foreignTenantID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO provider_installations(id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref) VALUES($1,$2,'gitlab',$3,'private/repo','https://foreign.example/api/v4','foreign-secret')`, foreignInstallationID, foreignTenantID, "foreign-"+foreignInstallationID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO webhook_deliveries(id,provider,delivery_id,event_name,payload) VALUES($1,'gitlab',$2,'Merge Request Hook','{}'::jsonb)`, foreignDeliveryID, "foreign-"+foreignDeliveryID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO gitlab_author_admissions(delivery_id,installation_id,repository,review_number,head_sha,author_id,state,attempt,error_code) VALUES($1,$2,'private/repo',99,$3,'99','failed',5,'foreign-only')`, foreignDeliveryID, foreignInstallationID, strings.Repeat("c", 40)); err != nil {
		t.Fatal(err)
	}
	isolated, err := postgres.GetPlatformHealthOverview(ctx, "owner", slug)
	if err != nil {
		t.Fatal(err)
	}
	if len(isolated.GitLabAuthorAdmissions) != 1 || isolated.GitLabAuthorAdmissions[0].Repository != repository {
		t.Fatalf("foreign GitLab admission leaked into workspace health: %#v", isolated.GitLabAuthorAdmissions)
	}
	encodedHealth, err := json.Marshal(isolated)
	if err != nil || strings.Contains(string(encodedHealth), "foreign-only") || strings.Contains(string(encodedHealth), "foreign-secret") {
		t.Fatalf("health response leaked foreign admission or credential: %v", err)
	}
	var failureAuditCount int
	if err := postgres.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM audit_events
		WHERE tenant_id=$1 AND action='review.gitlab_author_admission_failed'`, tenantID).Scan(&failureAuditCount); err != nil {
		t.Fatal(err)
	}
	if failureAuditCount != 1 {
		t.Fatalf("terminal author admission audit count=%d, want 1", failureAuditCount)
	}
}
