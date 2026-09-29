package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

func TestProviderChecksExactRunLeaseAndReadModel(t *testing.T) {
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
	tenantID, installationID, deliveryID, jobID, requestID, runID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	slug := "provider-checks-" + tenantID.String()[:8]
	const sha = "0123456789abcdef0123456789abcdef01234567"
	commands := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO tenants(id,slug,name) VALUES($1,$2,'Provider checks test')`, []any{tenantID, slug}},
		{`INSERT INTO memberships(tenant_id,subject,role) VALUES($1,'owner','owner')`, []any{tenantID}},
		{`INSERT INTO provider_installations(id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref,verification_state) VALUES($1,$2,'github',$3,'RainLib/*','https://api.github.com','secret://test','verified')`, []any{installationID, tenantID, "provider-checks-" + installationID.String()}},
		{`INSERT INTO webhook_deliveries(id,provider,delivery_id,event_name,payload) VALUES($1,'github',$2,'pull_request','{}'::jsonb)`, []any{deliveryID, "provider-checks-" + deliveryID.String()}},
		{`INSERT INTO review_jobs(id,tenant_id,installation_id,delivery_id,provider,api_base_url,repository,clone_url,review_number,base_ref,base_sha,head_ref,head_sha,state) VALUES($1,$2,$3,$4,'github','https://api.github.com','RainLib/open-review-platform','https://github.com/RainLib/open-review-platform.git',3,'main',$5,'feature/checks',$5,'succeeded')`, []any{jobID, tenantID, installationID, deliveryID, sha}},
		{`INSERT INTO review_requests(id,tenant_id,installation_id,provider,api_base_url,repository,review_number) VALUES($1,$2,$3,'github','https://api.github.com','RainLib/open-review-platform',3)`, []any{requestID, tenantID, installationID}},
		{`INSERT INTO review_runs(id,request_id,legacy_job_id,revision,state,trigger_kind,review_mode,head_sha,base_sha,finished_at) VALUES($1,$2,$3,1,'completed','pull_request','standard',$4,$4,now())`, []any{runID, requestID, jobID, sha}},
	}
	for _, command := range commands {
		if _, err := postgres.pool.Exec(ctx, command.query, command.args...); err != nil {
			t.Fatalf("seed provider checks fixture: %v", err)
		}
	}
	defer func() {
		_, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID)
		_, _ = postgres.pool.Exec(context.Background(), `DELETE FROM webhook_deliveries WHERE id=$1`, deliveryID)
	}()

	target, err := postgres.ClaimProviderCheckProbe(ctx, "worker-a", time.Minute)
	if err != nil || target.RunID != runID || target.Job.HeadSHA != sha || target.Job.CredentialRef != "secret://test" {
		t.Fatalf("claim target=%#v err=%v", target, err)
	}
	before, err := postgres.GetReviewEvidence(ctx, "owner", slug, runID)
	if err != nil || before.ProviderChecks == nil || before.ProviderChecks.State != "running" || before.ProviderChecks.HeadSHA != sha {
		t.Fatalf("in-progress evidence=%#v err=%v", before.ProviderChecks, err)
	}
	now := time.Now().UTC()
	observation := domain.ProviderCheckObservation{
		Provider: domain.ProviderGitHub, Repository: "RainLib/open-review-platform", HeadSHA: sha,
		State: "observed", ObservedAt: &now,
		Checks: []domain.ProviderCheck{{Kind: "check_run", Name: "CI / go", State: "success", URL: "https://github.com/RainLib/open-review-platform/actions/runs/1"}},
	}
	if err := postgres.CompleteProviderCheckProbe(ctx, *target, observation, now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	after, err := postgres.GetReviewEvidence(ctx, "owner", slug, runID)
	if err != nil || after.ProviderChecks == nil || after.ProviderChecks.State != "observed" || after.ProviderChecks.Stale || len(after.ProviderChecks.Checks) != 1 || after.ProviderChecks.Checks[0].Name != "CI / go" {
		t.Fatalf("completed evidence=%#v err=%v", after.ProviderChecks, err)
	}
	if _, err := postgres.ClaimProviderCheckProbe(ctx, "worker-b", time.Minute); !errors.Is(err, ErrNoProviderCheckProbe) {
		t.Fatalf("unexpected immediate re-observation: %v", err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE review_provider_check_observations SET available_at=now()-interval '1 second' WHERE run_id=$1`, runID); err != nil {
		t.Fatal(err)
	}
	second, err := postgres.ClaimProviderCheckProbe(ctx, "worker-b", time.Minute)
	if err != nil || second.RunID != runID {
		t.Fatalf("second claim target=%#v err=%v", second, err)
	}
	if err := postgres.CompleteProviderCheckProbe(ctx, *target, observation, now.Add(5*time.Minute)); !errors.Is(err, ErrJobClaimLost) {
		t.Fatalf("old lease completion=%v, want ErrJobClaimLost", err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE provider_installations SET active=FALSE WHERE id=$1`, installationID); err != nil {
		t.Fatal(err)
	}
	if err := postgres.CompleteProviderCheckProbe(ctx, *second, observation, now.Add(5*time.Minute)); !errors.Is(err, ErrJobClaimLost) {
		t.Fatalf("revoked installation completion=%v, want ErrJobClaimLost", err)
	}
}
