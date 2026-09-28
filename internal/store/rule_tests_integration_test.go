package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestRuleTestRunReclaimsExpiredLeaseWithSameWorkerID(t *testing.T) {
	postgres, testRunID := ruleTestLeaseFixture(t)
	ctx := context.Background()
	const workerID = "reused-rule-test-worker"
	first, err := postgres.ClaimRuleTestRun(ctx, workerID, &testRunID)
	if err != nil || first.Run.Attempts != 1 || first.Run.ID != testRunID {
		t.Fatalf("first claim=%#v error=%v", first.Run, err)
	}
	for _, contender := range []string{workerID, "different-worker"} {
		if _, err := postgres.ClaimRuleTestRun(ctx, contender, &testRunID); !errors.Is(err, ErrNoQueuedJob) {
			t.Fatalf("active lease was claimable by %q: %v", contender, err)
		}
	}
	assertRuleTestMutationFenced(t, postgres, testRunID, "different-worker", first.Run.Attempts)
	if _, err := postgres.pool.Exec(ctx, `UPDATE rule_test_runs SET locked_until=now()-interval '1 second' WHERE id=$1`, testRunID); err != nil {
		t.Fatal(err)
	}
	// Even before another worker recovers the row, an expired owner cannot
	// resurrect its own lease or publish a late result.
	assertRuleTestMutationFenced(t, postgres, testRunID, workerID, first.Run.Attempts)
	recovered, err := postgres.ClaimRuleTestRun(ctx, workerID, &testRunID)
	if err != nil || recovered.Run.Attempts != 2 || recovered.Run.ID != testRunID {
		t.Fatalf("same-ID recovery=%#v error=%v", recovered.Run, err)
	}
	assertRuleTestMutationFenced(t, postgres, testRunID, workerID, first.Run.Attempts)
	if err := postgres.RenewRuleTestRun(ctx, testRunID, workerID, recovered.Run.Attempts, time.Minute); err != nil {
		t.Fatalf("renew current claim: %v", err)
	}
	completion := domain.RuleTestCompletion{
		EngineVersion: "lease-test", SelectedPathCount: 1, DurationMS: 12,
		Findings: []domain.Finding{{Path: "main.go", StartLine: 3, EndLine: 3, Severity: "high", Category: "security", Body: "current attempt finding"}},
	}
	if err := postgres.CompleteRuleTestRun(ctx, testRunID, workerID, recovered.Run.Attempts, completion); err != nil {
		t.Fatalf("complete current claim: %v", err)
	}
	var runState string
	var attempts, findingCount int
	var lockedBy *string
	if err := postgres.pool.QueryRow(ctx, `SELECT state,attempts,finding_count,locked_by FROM rule_test_runs WHERE id=$1`, testRunID).
		Scan(&runState, &attempts, &findingCount, &lockedBy); err != nil {
		t.Fatal(err)
	}
	findings, err := postgres.ruleTestFindings(ctx, testRunID)
	if err != nil || runState != "completed" || attempts != 2 || findingCount != 1 || lockedBy != nil || len(findings) != 1 || findings[0].Body != "current attempt finding" {
		t.Fatalf("completed state=%s attempts=%d count=%d lock=%v findings=%#v error=%v", runState, attempts, findingCount, lockedBy, findings, err)
	}
	assertRuleTestMutationFenced(t, postgres, testRunID, workerID, recovered.Run.Attempts)
	if _, err := postgres.ClaimRuleTestRun(ctx, workerID, &testRunID); !errors.Is(err, ErrNoQueuedJob) {
		t.Fatalf("completed run was claimed: %v", err)
	}
}

func TestRuleTestRunPollingRecoversExpiredExecutionAndFencesFailure(t *testing.T) {
	postgres, testRunID := ruleTestLeaseFixture(t)
	ctx := context.Background()
	first, err := postgres.ClaimRuleTestRun(ctx, "same-worker", &testRunID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE rule_test_runs SET locked_until=now()-interval '1 second' WHERE id=$1`, testRunID); err != nil {
		t.Fatal(err)
	}
	// Polling must recover the orphan even if its broker message was already
	// acknowledged before the original process stopped.
	recovered, err := postgres.ClaimRuleTestRun(ctx, "same-worker", nil)
	if err != nil || recovered.Run.ID != testRunID || recovered.Run.Attempts != first.Run.Attempts+1 {
		t.Fatalf("polling recovery=%#v error=%v", recovered.Run, err)
	}
	assertRuleTestMutationFenced(t, postgres, testRunID, "same-worker", first.Run.Attempts)
	if err := postgres.FailRuleTestRun(ctx, testRunID, "same-worker", recovered.Run.Attempts, "current execution failed"); err != nil {
		t.Fatalf("record current failure: %v", err)
	}
	var runState, message string
	var lockedUntil *time.Time
	if err := postgres.pool.QueryRow(ctx, `SELECT state,error_message,locked_until FROM rule_test_runs WHERE id=$1`, testRunID).
		Scan(&runState, &message, &lockedUntil); err != nil {
		t.Fatal(err)
	}
	if runState != "failed" || message != "current execution failed" || lockedUntil != nil {
		t.Fatalf("terminal failure state=%q message=%q lease=%v", runState, message, lockedUntil)
	}
	if _, err := postgres.ClaimRuleTestRun(ctx, "same-worker", nil); !errors.Is(err, ErrNoQueuedJob) {
		t.Fatalf("failed run must remain terminal: %v", err)
	}
}

func TestRuleTestRunConcurrentRecoveryHasOneOwner(t *testing.T) {
	postgres, testRunID := ruleTestLeaseFixture(t)
	ctx := context.Background()
	if _, err := postgres.ClaimRuleTestRun(ctx, "restarted-worker", &testRunID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE rule_test_runs SET locked_until=now()-interval '1 second' WHERE id=$1`, testRunID); err != nil {
		t.Fatal(err)
	}
	start, results := make(chan struct{}), make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := postgres.ClaimRuleTestRun(ctx, "restarted-worker", &testRunID)
			results <- err
		}()
	}
	close(start)
	var claimed, unavailable int
	for range 2 {
		err := <-results
		if err == nil {
			claimed++
		} else if errors.Is(err, ErrNoQueuedJob) {
			unavailable++
		} else {
			t.Fatalf("concurrent recovery: %v", err)
		}
	}
	if claimed != 1 || unavailable != 1 {
		t.Fatalf("concurrent claims=%d unavailable=%d", claimed, unavailable)
	}
}

func assertRuleTestMutationFenced(t *testing.T, postgres *PostgresStore, testRunID uuid.UUID, workerID string, attempt int) {
	t.Helper()
	ctx := context.Background()
	if err := postgres.RenewRuleTestRun(ctx, testRunID, workerID, attempt, time.Minute); !errors.Is(err, ErrJobClaimLost) {
		t.Fatalf("stale renew attempt=%d error=%v", attempt, err)
	}
	completion := domain.RuleTestCompletion{Findings: []domain.Finding{{Path: "stale.go", Body: "stale finding", Severity: "critical", Category: "security"}}}
	if err := postgres.CompleteRuleTestRun(ctx, testRunID, workerID, attempt, completion); !errors.Is(err, ErrJobClaimLost) {
		t.Fatalf("stale completion attempt=%d error=%v", attempt, err)
	}
	if err := postgres.FailRuleTestRun(ctx, testRunID, workerID, attempt, "stale failure"); !errors.Is(err, ErrJobClaimLost) {
		t.Fatalf("stale failure attempt=%d error=%v", attempt, err)
	}
}

func ruleTestLeaseFixture(t *testing.T) (*PostgresStore, uuid.UUID) {
	t.Helper()
	databaseURL := os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("OPEN_REVIEW_TEST_DATABASE_URL is not configured")
	}
	if os.Getenv("OPEN_REVIEW_TEST_ISOLATED_DATABASE") != "true" {
		t.Skip("rule test lease polling requires an isolated database")
	}
	ctx := context.Background()
	postgres, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(postgres.Close)
	tenantID, installationID, deliveryID := uuid.New(), uuid.New(), uuid.New()
	jobID, requestID, runID := uuid.New(), uuid.New(), uuid.New()
	ruleSetID, versionID, snapshotID, testRunID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	t.Cleanup(func() {
		batch := &pgx.Batch{}
		batch.Queue(`DELETE FROM rule_test_runs WHERE id=$1`, testRunID)
		batch.Queue(`DELETE FROM rule_versions WHERE id=$1`, versionID)
		batch.Queue(`DELETE FROM review_requests WHERE id=$1`, requestID)
		batch.Queue(`DELETE FROM review_jobs WHERE id=$1`, jobID)
		batch.Queue(`DELETE FROM tenants WHERE id=$1`, tenantID)
		batch.Queue(`DELETE FROM webhook_deliveries WHERE id=$1`, deliveryID)
		if err := postgres.pool.SendBatch(context.Background(), batch).Close(); err != nil {
			t.Errorf("clean rule test lease fixture: %v", err)
		}
	})
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Rule test lease')`, tenantID, "rule-lease-"+tenantID.String())
	batch.Queue(`INSERT INTO provider_installations (id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref) VALUES ($1,$2,'gitlab',$3,'tests/recovery','https://gitlab.example/api/v4','test-only')`, installationID, tenantID, installationID.String())
	batch.Queue(`INSERT INTO webhook_deliveries (id,provider,delivery_id,event_name,payload) VALUES ($1,'gitlab',$2,'merge_request','{}')`, deliveryID, deliveryID.String())
	batch.Queue(`INSERT INTO review_jobs (id,tenant_id,installation_id,delivery_id,provider,api_base_url,repository,clone_url,review_number,base_ref,base_sha,head_ref,head_sha,state) VALUES ($1,$2,$3,$4,'gitlab','https://gitlab.example/api/v4','tests/recovery','https://gitlab.example/tests/recovery.git',1,'main','base','feature/recovery','head','succeeded')`, jobID, tenantID, installationID, deliveryID)
	batch.Queue(`INSERT INTO review_requests (id,tenant_id,installation_id,provider,api_base_url,repository,review_number) VALUES ($1,$2,$3,'gitlab','https://gitlab.example/api/v4','tests/recovery',1)`, requestID, tenantID, installationID)
	batch.Queue(`INSERT INTO review_runs (id,request_id,legacy_job_id,state,trigger_kind,head_sha,base_sha) VALUES ($1,$2,$3,'completed','pull_request','head','base')`, runID, requestID, jobID)
	batch.Queue(`INSERT INTO rule_sets (id,tenant_id,name,created_by) VALUES ($1,$2,'Lease candidate','test')`, ruleSetID, tenantID)
	batch.Queue(`INSERT INTO rule_versions (id,rule_set_id,version,state,rules,content_sha256,created_by) VALUES ($1,$2,1,'published','[]','lease-fixture','test')`, versionID, ruleSetID)
	batch.Queue(`INSERT INTO rule_snapshots (id,tenant_id,sha256,compiler_version,canonical_payload) VALUES ($1,$2,'lease-fixture','rules-v2','{}')`, snapshotID, tenantID)
	batch.Queue(`INSERT INTO rule_test_runs (id,tenant_id,rule_version_id,source_run_id,source_job_id,snapshot_id,requested_by) VALUES ($1,$2,$3,$4,$5,$6,'test')`, testRunID, tenantID, versionID, runID, jobID, snapshotID)
	if err := postgres.pool.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatalf("seed rule test lease: %v", err)
	}
	return postgres, testRunID
}
