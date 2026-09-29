package store

import (
	"context"
	"errors"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestClaimSustainsConcurrentWorkersWithoutDuplicatesOrTenantStarvation(t *testing.T) {
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

	const (
		tenantCount   = 6
		jobsPerTenant = 18
		workerCount   = 8
	)
	tenantIDs := make([]uuid.UUID, 0, tenantCount)
	type expectedJob struct {
		tenantID uuid.UUID
		mode     string
	}
	expected := make(map[uuid.UUID]expectedJob, tenantCount*jobsPerTenant)
	base := time.Now().UTC().Add(-time.Hour)
	for tenantIndex := 0; tenantIndex < tenantCount; tenantIndex++ {
		tenantID, installationID := uuid.New(), uuid.New()
		tenantIDs = append(tenantIDs, tenantID)
		if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id,slug,name) VALUES ($1,$2,$3)`, tenantID, "fair-load-"+tenantID.String()[:8], "Fair load tenant"); err != nil {
			t.Fatalf("seed tenant %d: %v", tenantIndex, err)
		}
		if _, err := postgres.pool.Exec(ctx, `
			INSERT INTO provider_installations (id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref,verification_state)
			VALUES ($1,$2,'github',$3,'RainLib/*','https://api.github.com','github-app','verified')`, installationID, tenantID, "fair-load-installation-"+installationID.String()); err != nil {
			t.Fatalf("seed installation %d: %v", tenantIndex, err)
		}
		for jobIndex := 0; jobIndex < jobsPerTenant; jobIndex++ {
			mode := "standard"
			switch {
			case jobIndex >= 4 && jobIndex < 10:
				mode = "security"
			case jobIndex >= 10 && jobIndex < 14:
				mode = "deep"
			}
			jobID := seedFairClaimJobWithMode(t, ctx, postgres, tenantID, installationID, tenantIndex*jobsPerTenant+jobIndex+1, base.Add(time.Duration(tenantIndex*jobsPerTenant+jobIndex)*time.Millisecond), mode)
			expected[jobID] = expectedJob{tenantID: tenantID, mode: mode}
		}
	}
	defer func() {
		_, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = ANY($1::uuid[])`, tenantIDs)
	}()

	type claimRecord struct {
		jobID    uuid.UUID
		tenantID uuid.UUID
		mode     string
		latency  time.Duration
	}
	var (
		mu              sync.Mutex
		records         = make([]claimRecord, 0, len(expected))
		seen            = make(map[uuid.UUID]string, len(expected))
		activeByTenant  = make(map[uuid.UUID]int, tenantCount)
		maximumByTenant = make(map[uuid.UUID]int, tenantCount)
		completed       atomic.Int64
	)
	runCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	errCh := make(chan error, workerCount*2)
	started := time.Now()
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for workerIndex := 0; workerIndex < workerCount; workerIndex++ {
		workerID := "fair-load-worker-" + uuid.NewString()
		go func() {
			defer workers.Done()
			for completed.Load() < int64(len(expected)) {
				claimStarted := time.Now()
				job, claimErr := postgres.Claim(runCtx, workerID)
				claimLatency := time.Since(claimStarted)
				if errors.Is(claimErr, ErrNoQueuedJob) {
					select {
					case <-runCtx.Done():
						return
					case <-time.After(2 * time.Millisecond):
						continue
					}
				}
				if claimErr != nil {
					errCh <- claimErr
					cancel()
					return
				}
				want, ok := expected[job.ID]
				if !ok {
					errCh <- errors.New("claimed an unexpected fair-load job " + job.ID.String())
					cancel()
					return
				}

				mu.Lock()
				if previousWorker, duplicate := seen[job.ID]; duplicate {
					mu.Unlock()
					errCh <- errors.New("job " + job.ID.String() + " was claimed by both " + previousWorker + " and " + workerID)
					cancel()
					return
				}
				seen[job.ID] = workerID
				activeByTenant[want.tenantID]++
				if activeByTenant[want.tenantID] > maximumByTenant[want.tenantID] {
					maximumByTenant[want.tenantID] = activeByTenant[want.tenantID]
				}
				records = append(records, claimRecord{jobID: job.ID, tenantID: want.tenantID, mode: want.mode, latency: claimLatency})
				mu.Unlock()

				// Keep leases active briefly so concurrent workers exercise the
				// database-enforced per-tenant capacity, rather than serially
				// draining an effectively idle queue.
				time.Sleep(4 * time.Millisecond)
				if finishErr := postgres.Succeed(runCtx, job.ID, workerID); finishErr != nil {
					errCh <- finishErr
					cancel()
					return
				}
				mu.Lock()
				activeByTenant[want.tenantID]--
				mu.Unlock()
				completed.Add(1)
			}
		}()
	}
	workers.Wait()
	close(errCh)
	for workerErr := range errCh {
		if workerErr != nil {
			t.Fatalf("concurrent fair claim: %v", workerErr)
		}
	}
	if runCtx.Err() != nil && completed.Load() != int64(len(expected)) {
		t.Fatalf("concurrent fair claim stopped after %d/%d jobs: %v", completed.Load(), len(expected), runCtx.Err())
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != len(expected) || len(records) != len(expected) {
		t.Fatalf("claimed unique jobs=%d records=%d, want %d", len(seen), len(records), len(expected))
	}
	firstClaimByTenant := make(map[uuid.UUID]int, tenantCount)
	latencies := make([]time.Duration, 0, len(records))
	priorityWindow := workerCount * 2
	securityInPriorityWindow := 0
	for index, record := range records {
		if _, observed := firstClaimByTenant[record.tenantID]; !observed {
			firstClaimByTenant[record.tenantID] = index
		}
		if index < priorityWindow && record.mode == "security" {
			securityInPriorityWindow++
		}
		latencies = append(latencies, record.latency)
	}
	for _, tenantID := range tenantIDs {
		if maximumByTenant[tenantID] > 2 {
			t.Errorf("tenant %s reached %d concurrent leases, want at most 2", tenantID, maximumByTenant[tenantID])
		}
		if first, ok := firstClaimByTenant[tenantID]; !ok || first >= tenantCount+workerCount {
			t.Errorf("tenant %s first claim position=%d present=%t, want before position %d", tenantID, first, ok, tenantCount+workerCount)
		}
	}
	if securityInPriorityWindow < priorityWindow/2 {
		t.Errorf("security claims in first %d positions=%d, want at least %d", priorityWindow, securityInPriorityWindow, priorityWindow/2)
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p95 := latencies[(len(latencies)*95+99)/100-1]
	if p95 > 2*time.Second {
		t.Errorf("claim latency p95=%s, want <=2s in isolated PostgreSQL integration", p95)
	}
	var succeeded, retryAttempts int
	if err := postgres.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE state='succeeded'), count(*) FILTER (WHERE attempts<>1)
		FROM review_jobs
		WHERE tenant_id = ANY($1::uuid[])`, tenantIDs).Scan(&succeeded, &retryAttempts); err != nil {
		t.Fatal(err)
	}
	if succeeded != len(expected) || retryAttempts != 0 {
		t.Errorf("durable jobs succeeded=%d non-single-attempt=%d, want %d and 0", succeeded, retryAttempts, len(expected))
	}
	elapsed := time.Since(started)
	t.Logf("claimed and completed %d jobs across %d tenants with %d workers in %s (p95 claim=%s)", len(records), tenantCount, workerCount, elapsed, p95)
}

func TestClaimRoundRobinsAcrossTenantsDuringRecovery(t *testing.T) {
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

	tenantA, tenantB := uuid.New(), uuid.New()
	installationA, installationB := uuid.New(), uuid.New()
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Fair claim A'),($3,$4,'Fair claim B')`, tenantA, "fair-a-"+tenantA.String()[:8], tenantB, "fair-b-"+tenantB.String()[:8])
	batch.Queue(`INSERT INTO provider_installations (id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref,verification_state) VALUES ($1,$2,'github',$3,'RainLib/*','https://api.github.com','github-app','verified'),($4,$5,'github',$6,'RainLib/*','https://api.github.com','github-app','verified')`, installationA, tenantA, "fair-claim-a-"+tenantA.String(), installationB, tenantB, "fair-claim-b-"+tenantB.String())
	if err := postgres.pool.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = ANY($1::uuid[])`, []uuid.UUID{tenantA, tenantB})
	}()

	base := time.Now().UTC().Add(-time.Hour)
	firstA := seedFairClaimJob(t, ctx, postgres, tenantA, installationA, 1, base)
	secondA := seedFairClaimJob(t, ctx, postgres, tenantA, installationA, 2, base.Add(time.Minute))
	thirdA := seedFairClaimJob(t, ctx, postgres, tenantA, installationA, 4, base.Add(3*time.Minute))
	firstB := seedFairClaimJob(t, ctx, postgres, tenantB, installationB, 3, base.Add(2*time.Minute))

	first, err := postgres.Claim(ctx, "fair-worker")
	if err != nil || first.ID != firstA {
		t.Fatalf("first claim=%#v err=%v, want tenant A oldest job %s", first, err, firstA)
	}
	second, err := postgres.Claim(ctx, "fair-worker")
	if err != nil || second.ID != firstB {
		t.Fatalf("second claim=%#v err=%v, want tenant B head %s", second, err, firstB)
	}
	third, err := postgres.Claim(ctx, "fair-worker")
	if err != nil || third.ID != secondA {
		t.Fatalf("third claim=%#v err=%v, want remaining tenant A job %s", third, err, secondA)
	}
	if claimed, err := postgres.Claim(ctx, "fair-worker"); !errors.Is(err, ErrNoQueuedJob) || claimed != nil {
		t.Fatalf("claim after tenant A reaches the default concurrency=%#v err=%v, want ErrNoQueuedJob for %s", claimed, err, thirdA)
	}

	var dispatches int
	if err := postgres.pool.QueryRow(ctx, `SELECT count(*) FROM tenant_review_dispatches WHERE tenant_id = ANY($1::uuid[])`, []uuid.UUID{tenantA, tenantB}).Scan(&dispatches); err != nil || dispatches != 2 {
		t.Fatalf("tenant dispatch cursors=%d err=%v, want 2", dispatches, err)
	}
}

func TestClaimPrioritizesSecurityRecoveryWithoutSkippingTenantFairness(t *testing.T) {
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

	tenantA, tenantB := uuid.New(), uuid.New()
	installationA, installationB := uuid.New(), uuid.New()
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Priority recovery A'),($3,$4,'Priority recovery B')`,
		tenantA, "priority-a-"+tenantA.String()[:8], tenantB, "priority-b-"+tenantB.String()[:8])
	batch.Queue(`INSERT INTO provider_installations (id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref,verification_state)
		VALUES ($1,$2,'github',$3,'RainLib/*','https://api.github.com','github-app','verified'),
		       ($4,$5,'github',$6,'RainLib/*','https://api.github.com','github-app','verified')`,
		installationA, tenantA, "priority-installation-a-"+installationA.String(), installationB, tenantB, "priority-installation-b-"+installationB.String())
	if err := postgres.pool.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = ANY($1::uuid[])`, []uuid.UUID{tenantA, tenantB})
	}()

	base := time.Now().UTC().Add(-time.Hour)
	standard := seedFairClaimJobWithMode(t, ctx, postgres, tenantA, installationA, 1, base, "standard")
	securityA := seedFairClaimJobWithMode(t, ctx, postgres, tenantA, installationA, 2, base.Add(time.Minute), "security")
	securityB := seedFairClaimJobWithMode(t, ctx, postgres, tenantB, installationB, 3, base.Add(2*time.Minute), "security")
	deepB := seedFairClaimJobWithMode(t, ctx, postgres, tenantB, installationB, 4, base.Add(3*time.Minute), "deep")

	first, err := postgres.Claim(ctx, "priority-worker")
	if err != nil || first.ID != securityA {
		t.Fatalf("first recovery claim=%#v err=%v, want earlier security job %s ahead of standard %s", first, err, securityA, standard)
	}
	second, err := postgres.Claim(ctx, "priority-worker")
	if err != nil || second.ID != securityB {
		t.Fatalf("second recovery claim=%#v err=%v, want other tenant security job %s before standard %s", second, err, securityB, standard)
	}
	third, err := postgres.Claim(ctx, "priority-worker")
	if err != nil || third.ID != deepB {
		t.Fatalf("third recovery claim=%#v err=%v, want deep job %s before standard %s", third, err, deepB, standard)
	}
	fourth, err := postgres.Claim(ctx, "priority-worker")
	if err != nil || fourth.ID != standard {
		t.Fatalf("fourth recovery claim=%#v err=%v, want deferred standard job %s", fourth, err, standard)
	}
}

func TestClaimForRunDefersBrokerWorkAtTenantConcurrencyLimit(t *testing.T) {
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
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Queue capacity')`, tenantID, "queue-capacity-"+tenantID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO provider_installations (id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref,verification_state) VALUES ($1,$2,'github',$3,'RainLib/*','https://api.github.com','github-app','verified')`, installationID, tenantID, "queue-capacity-"+tenantID.String()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID)
	}()

	base := time.Now().UTC().Add(-time.Hour)
	firstJob := seedFairClaimJob(t, ctx, postgres, tenantID, installationID, 1, base)
	secondJob := seedFairClaimJob(t, ctx, postgres, tenantID, installationID, 2, base.Add(time.Minute))
	thirdJob := seedFairClaimJob(t, ctx, postgres, tenantID, installationID, 3, base.Add(2*time.Minute))
	for _, jobID := range []uuid.UUID{firstJob, secondJob} {
		runID := fairClaimRunID(t, ctx, postgres, jobID)
		if claimed, err := postgres.ClaimForRun(ctx, "queue-capacity-worker", runID); err != nil || claimed == nil || claimed.ID != jobID {
			t.Fatalf("claim queue job=%s got=%#v err=%v", jobID, claimed, err)
		}
	}
	thirdRun := fairClaimRunID(t, ctx, postgres, thirdJob)
	if claimed, err := postgres.ClaimForRun(ctx, "queue-capacity-worker", thirdRun); !errors.Is(err, ErrNoQueuedJob) || claimed != nil {
		t.Fatalf("third queue claim=%#v err=%v, want deferred ErrNoQueuedJob", claimed, err)
	}
	var state string
	var availableAt time.Time
	if err := postgres.pool.QueryRow(ctx, `SELECT state,available_at FROM review_jobs WHERE id=$1`, thirdJob).Scan(&state, &availableAt); err != nil {
		t.Fatal(err)
	}
	if state != "queued" || !availableAt.After(time.Now().UTC()) {
		t.Fatalf("deferred job state=%q available_at=%s, want queued future retry", state, availableAt)
	}
}

func TestClaimsRecoverRunningJobsWhoseLeaseWasLost(t *testing.T) {
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
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Missing lease recovery')`, tenantID, "missing-lease-"+tenantID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO provider_installations (id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref,verification_state) VALUES ($1,$2,'github',$3,'RainLib/*','https://api.github.com','github-app','verified')`, installationID, tenantID, "missing-lease-"+installationID.String()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = postgres.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID)
	}()

	for number, claim := range []func(uuid.UUID) (*domain.ReviewJob, error){
		func(_ uuid.UUID) (*domain.ReviewJob, error) { return postgres.Claim(ctx, "poll-recovery") },
		func(runID uuid.UUID) (*domain.ReviewJob, error) {
			return postgres.ClaimForRun(ctx, "queue-recovery", runID)
		},
	} {
		jobID := seedFairClaimJob(t, ctx, postgres, tenantID, installationID, number+1, time.Now().UTC().Add(-time.Hour))
		if _, err := postgres.pool.Exec(ctx, `UPDATE review_jobs SET state='running',locked_by=NULL,locked_until=NULL WHERE id=$1`, jobID); err != nil {
			t.Fatal(err)
		}
		claimed, err := claim(fairClaimRunID(t, ctx, postgres, jobID))
		if err != nil || claimed == nil || claimed.ID != jobID || claimed.State != domain.JobRunning || claimed.LockedUntil == nil || !claimed.LockedUntil.After(time.Now().UTC()) {
			t.Fatalf("claim path %d did not recover missing lease: job=%#v err=%v", number, claimed, err)
		}
		if err := postgres.Succeed(ctx, jobID, claimed.LockedBy); err != nil {
			t.Fatal(err)
		}
	}
}

func fairClaimRunID(t *testing.T, ctx context.Context, postgres *PostgresStore, jobID uuid.UUID) uuid.UUID {
	t.Helper()
	var runID uuid.UUID
	if err := postgres.pool.QueryRow(ctx, `SELECT id FROM review_runs WHERE legacy_job_id=$1`, jobID).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	return runID
}

func seedFairClaimJob(t *testing.T, ctx context.Context, postgres *PostgresStore, tenantID, installationID uuid.UUID, number int, createdAt time.Time) uuid.UUID {
	return seedFairClaimJobWithMode(t, ctx, postgres, tenantID, installationID, number, createdAt, "standard")
}

func seedFairClaimJobWithMode(t *testing.T, ctx context.Context, postgres *PostgresStore, tenantID, installationID uuid.UUID, number int, createdAt time.Time, reviewMode string) uuid.UUID {
	t.Helper()
	deliveryID, jobID, requestID, runID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO webhook_deliveries (id,provider,delivery_id,event_name,payload,received_at) VALUES ($1,'github',$2,'pull_request','{}'::jsonb,$3)`, deliveryID, "fair-claim-delivery-"+deliveryID.String(), createdAt)
	batch.Queue(`INSERT INTO review_jobs (id,tenant_id,installation_id,delivery_id,provider,api_base_url,repository,clone_url,review_number,base_ref,base_sha,head_ref,head_sha,state,available_at,created_at) VALUES ($1,$2,$3,$4,'github','https://api.github.com','RainLib/open-review-platform','https://github.com/RainLib/open-review-platform.git',$5,'main','base','feature/fair','head','queued',$6,$6)`, jobID, tenantID, installationID, deliveryID, number, createdAt)
	batch.Queue(`INSERT INTO review_requests (id,tenant_id,installation_id,provider,api_base_url,repository,review_number,created_at,updated_at) VALUES ($1,$2,$3,'github','https://api.github.com','RainLib/open-review-platform',$4,$5,$5)`, requestID, tenantID, installationID, number, createdAt)
	batch.Queue(`INSERT INTO review_runs (id,request_id,legacy_job_id,state,trigger_kind,review_mode,head_sha,base_sha,created_at) VALUES ($1,$2,$3,'admitted','pull_request',$4,'head','base',$5)`, runID, requestID, jobID, reviewMode, createdAt)
	if err := postgres.pool.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatal(err)
	}
	return jobID
}
