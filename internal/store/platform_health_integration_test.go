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

func TestPlatformHealthUsesTenantScopedObservedState(t *testing.T) {
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
	tenantID := uuid.New()
	tenantSlug := "health-" + tenantID.String()[:8]
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO tenants (id,slug,name) VALUES ($1,$2,'Platform Health Integration')`, tenantID, tenantSlug); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner','owner'),($1,'viewer','viewer')`, tenantID); err != nil {
		t.Fatal(err)
	}
	workerID := "health-worker-" + tenantID.String()
	agentWorkerID := "agent-health-worker-" + tenantID.String()
	agentProbeWorkerID := "agent-probe-worker-" + tenantID.String()
	decisionWorkerID := "agent-decision-health-worker-" + tenantID.String()
	brokerWorkerID := "agent-credential-broker-health-worker-" + tenantID.String()
	foreignWorkerID := "foreign-health-worker-" + tenantID.String()
	installationID, deliveryID, jobID := uuid.New(), uuid.New(), uuid.New()
	issueJobID, issueOutboxID := uuid.New(), uuid.New()
	taskID, sourceTaskID, planID := uuid.New(), uuid.New(), uuid.New()
	defer func() {
		cleanup := context.Background()
		for _, step := range []struct {
			query string
			id    uuid.UUID
		}{
			{`DELETE FROM inbox_messages WHERE message_id=$1`, issueOutboxID},
			{`DELETE FROM outbox_messages WHERE id=$1`, issueOutboxID},
			{`DELETE FROM provider_issue_analysis_jobs WHERE id=$1`, issueJobID},
			{`DELETE FROM agent_task_attempts WHERE task_id=$1`, taskID},
			{`DELETE FROM agent_task_plans WHERE task_id=$1`, taskID},
			{`DELETE FROM agent_tasks WHERE id=$1`, taskID},
			{`DELETE FROM agent_tasks WHERE id=$1`, sourceTaskID},
			{`DELETE FROM tenants WHERE id=$1`, tenantID},
			{`DELETE FROM webhook_deliveries WHERE id=$1`, deliveryID},
		} {
			if _, err := postgres.pool.Exec(cleanup, step.query, step.id); err != nil {
				t.Errorf("clean platform health fixture: %v", err)
			}
		}
		if _, err := postgres.pool.Exec(cleanup, `DELETE FROM worker_heartbeats WHERE worker_id = ANY($1::text[])`, []string{workerID, agentWorkerID, agentProbeWorkerID, decisionWorkerID, brokerWorkerID, foreignWorkerID}); err != nil {
			t.Errorf("clean platform health worker heartbeats: %v", err)
		}
	}()
	now := time.Now().UTC()
	if err := postgres.UpsertWorkerHeartbeat(ctx, domain.WorkerHeartbeat{
		WorkerID: workerID, Kind: "review-runner", Version: "integration", Capacity: 2, Busy: 1,
		StartedAt: now.Add(-time.Minute), HeartbeatAt: now, ExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if err := postgres.UpsertWorkerHeartbeat(ctx, domain.WorkerHeartbeat{
		WorkerID: foreignWorkerID, Kind: "review-runner", Version: "foreign", Capacity: 50, Busy: 49,
		StartedAt: now.Add(-time.Minute), HeartbeatAt: now, ExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if err := postgres.UpsertWorkerHeartbeat(ctx, domain.WorkerHeartbeat{
		WorkerID: agentWorkerID, Kind: "agent-task-runner", Version: "agent-integration", Capacity: 1, Busy: 1,
		StartedAt: now.Add(-time.Minute), HeartbeatAt: now, ExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO provider_installations (id,tenant_id,provider,external_id,repository_scope,api_base_url,credential_ref)
		VALUES ($1,$2,'github','health-installation','RainLib/health','https://api.github.com','secret://health')`, installationID, tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `INSERT INTO webhook_deliveries (id,provider,delivery_id,event_name,payload) VALUES ($1,'github',$2,'pull_request','{}'::jsonb)`, deliveryID, "health-delivery-"+tenantID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO review_jobs (id,tenant_id,installation_id,delivery_id,provider,api_base_url,repository,clone_url,review_number,base_ref,base_sha,head_ref,head_sha,state,locked_by,locked_until,started_at)
		VALUES ($1,$2,$3,$4,'github','https://api.github.com','RainLib/health','https://github.com/RainLib/health.git',1,'main','base','feature/health','head','running',$5,now()+interval '1 minute',now())`, jobID, tenantID, installationID, deliveryID, workerID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO agent_tasks (id,tenant_id,installation_id,provider,api_base_url,repository,origin_kind,origin_number,origin_revision,intent,state,requested_by,source_state,source_base_ref,source_base_sha,source_captured_at,execution_branch)
		VALUES ($1::uuid,$2,$3,'github','https://api.github.com','RainLib/health','issue',42,'issue-v1','implement','executing','owner','ready','main',$4,now(),'agent/' || $1::uuid::text)`, taskID, tenantID, installationID, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO agent_tasks (id,tenant_id,installation_id,provider,api_base_url,repository,origin_kind,origin_number,origin_revision,intent,requested_by,execution_branch)
		VALUES ($1::uuid,$2,$3,'github','https://api.github.com','RainLib/health','issue',43,'issue-v2','implement','owner','agent/' || $1::uuid::text)`, sourceTaskID, tenantID, installationID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO agent_task_plans (id,task_id,revision,state,summary,plan_sha256,created_by,approved_by,approved_at)
		VALUES ($1,$2,1,'approved','Bounded test plan','sha256:test','owner','owner',now())`, planID, taskID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO agent_task_attempts (task_id,plan_id,task_revision,plan_revision,state,worker_id,locked_until,started_at)
		VALUES ($1,$2,1,1,'running',$3,now()+interval '1 minute',now())`, taskID, planID, agentWorkerID); err != nil {
		t.Fatal(err)
	}

	overview, err := postgres.GetPlatformHealthOverview(ctx, "owner", tenantSlug)
	if err != nil {
		t.Fatal(err)
	}
	if overview.SampledAt.IsZero() || overview.FreshnessWindowSeconds != 300 || len(overview.Components) < 5 || len(overview.Queues) != 10 || len(overview.Runbooks) < 4 {
		t.Fatalf("unexpected health overview: %#v", overview)
	}
	if overview.BrokerMetricsAvailable || !overview.WorkerHeartbeatAvailable || !overview.IncidentTrackingAvailable {
		t.Fatalf("observation source availability is incorrect: %#v", overview)
	}
	var observed *domain.WorkerHealth
	for index := range overview.Workers {
		if overview.Workers[index].Version == "integration" {
			observed = &overview.Workers[index]
			break
		}
	}
	if observed == nil || observed.WorkerID == workerID || observed.State != domain.HealthLive || observed.Capacity != 0 || observed.Busy != 0 || observed.LastHeartbeat == nil {
		t.Fatalf("unexpected anonymized heartbeat: %#v", overview.Workers)
	}
	for _, worker := range overview.Workers {
		if worker.Version == "foreign" {
			t.Fatalf("foreign worker heartbeat leaked into tenant overview: %#v", overview.Workers)
		}
	}
	var agentObserved bool
	for _, worker := range overview.Workers {
		if worker.Version == "agent-integration" && worker.Kind == "agent-task-runner" && worker.ActiveLeases == 1 && worker.State == domain.HealthLive {
			agentObserved = true
		}
	}
	if !agentObserved {
		t.Fatalf("agent runner lease was not reflected in tenant health: %#v", overview.Workers)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE worker_heartbeats SET adapter_configured=NULL WHERE worker_id=$1`, agentWorkerID); err != nil {
		t.Fatal(err)
	}
	legacyOverview, err := postgres.GetPlatformHealthOverview(ctx, "owner", tenantSlug)
	if err != nil {
		t.Fatal(err)
	}
	if legacyOverview.AgentExecutor.State != "unobserved" {
		t.Fatalf("legacy runner heartbeat must not be interpreted as an explicit false: %#v", legacyOverview.AgentExecutor)
	}
	if err := postgres.UpsertWorkerHeartbeat(ctx, domain.WorkerHeartbeat{
		WorkerID: agentWorkerID, Kind: "agent-task-runner", Version: "agent-integration",
		Capacity: 1, Busy: 1, StartedAt: now.Add(-time.Minute), HeartbeatAt: now.Add(time.Millisecond), ExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	overview, err = postgres.GetPlatformHealthOverview(ctx, "owner", tenantSlug)
	if err != nil {
		t.Fatal(err)
	}
	if overview.AgentExecutor.State != "not_configured" {
		t.Fatalf("unconfigured live agent runner must not appear ready: %#v", overview.AgentExecutor)
	}
	if err := postgres.UpsertWorkerHeartbeat(ctx, domain.WorkerHeartbeat{
		WorkerID: agentWorkerID, Kind: "agent-task-runner", Version: "agent-integration", AdapterConfigured: true,
		Capacity: 1, Busy: 1, StartedAt: now.Add(-time.Minute), HeartbeatAt: now.Add(time.Second), ExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	configuredOverview, err := postgres.GetPlatformHealthOverview(ctx, "owner", tenantSlug)
	if err != nil {
		t.Fatal(err)
	}
	if configuredOverview.AgentExecutor.State != "configured_unverified" {
		t.Fatalf("configuration must not be reported as verified reachability: %#v", configuredOverview.AgentExecutor)
	}
	if configuredOverview.AgentCredentialBroker.State != "unobserved" {
		t.Fatalf("an absent coding credential broker must not imply provider write access: %#v", configuredOverview.AgentCredentialBroker)
	}
	if err := postgres.UpsertWorkerHeartbeat(ctx, domain.WorkerHeartbeat{
		WorkerID: brokerWorkerID, Kind: "agent-credential-broker", Version: "private-broker-integration",
		Capacity: 1, StartedAt: now.Add(-time.Minute), HeartbeatAt: now, ExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	brokerOverview, err := postgres.GetPlatformHealthOverview(ctx, "owner", tenantSlug)
	if err != nil || brokerOverview.AgentCredentialBroker.State != "configured_unverified" {
		t.Fatalf("fresh private broker heartbeat is configuration evidence only: state=%#v err=%v", brokerOverview.AgentCredentialBroker, err)
	}
	for _, worker := range brokerOverview.Workers {
		if worker.Kind == "agent-credential-broker" || worker.WorkerID == brokerWorkerID {
			t.Fatalf("private broker identity must not appear in tenant worker cards: %#v", brokerOverview.Workers)
		}
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE worker_heartbeats SET heartbeat_at=now()-interval '30 seconds', expires_at=now()-interval '1 second' WHERE worker_id=$1`, brokerWorkerID); err != nil {
		t.Fatal(err)
	}
	expiredBroker, err := postgres.GetPlatformHealthOverview(ctx, "owner", tenantSlug)
	if err != nil || expiredBroker.AgentCredentialBroker.State != "unobserved" {
		t.Fatalf("expired broker heartbeat must not imply a live credential source: state=%#v err=%v", expiredBroker.AgentCredentialBroker, err)
	}
	probeAt := time.Now().UTC()
	probeFailed := false
	if err := postgres.UpsertWorkerHeartbeat(ctx, domain.WorkerHeartbeat{
		WorkerID: agentProbeWorkerID, Kind: "agent-task-runner", Version: "probe-integration", AdapterConfigured: true,
		AdapterProbeAt: &probeAt, AdapterReachable: &probeFailed,
		Capacity: 1, StartedAt: probeAt.Add(-time.Minute), HeartbeatAt: probeAt, ExpiresAt: probeAt.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	unreachableOverview, err := postgres.GetPlatformHealthOverview(ctx, "owner", tenantSlug)
	if err != nil || unreachableOverview.AgentExecutor.State != "unreachable" {
		t.Fatalf("failed signed probe must not appear reachable: state=%#v err=%v", unreachableOverview.AgentExecutor, err)
	}
	probeSucceeded := true
	probeAt = time.Now().UTC()
	if err := postgres.UpsertWorkerHeartbeat(ctx, domain.WorkerHeartbeat{
		WorkerID: agentProbeWorkerID, Kind: "agent-task-runner", Version: "probe-integration", AdapterConfigured: true,
		AdapterProbeAt: &probeAt, AdapterReachable: &probeSucceeded,
		Capacity: 1, StartedAt: probeAt.Add(-time.Minute), HeartbeatAt: probeAt, ExpiresAt: probeAt.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	reachableOverview, err := postgres.GetPlatformHealthOverview(ctx, "owner", tenantSlug)
	if err != nil || reachableOverview.AgentExecutor.State != "partially_reachable" {
		t.Fatalf("unprobed runner must prevent an all-reachable claim: state=%#v err=%v", reachableOverview.AgentExecutor, err)
	}
	allProbedAt := time.Now().UTC().Add(time.Second)
	if err := postgres.UpsertWorkerHeartbeat(ctx, domain.WorkerHeartbeat{
		WorkerID: agentWorkerID, Kind: "agent-task-runner", Version: "agent-integration", AdapterConfigured: true,
		AdapterProbeAt: &allProbedAt, AdapterReachable: &probeSucceeded,
		Capacity: 1, Busy: 1, StartedAt: now.Add(-time.Minute), HeartbeatAt: allProbedAt, ExpiresAt: allProbedAt.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	reachableOverview, err = postgres.GetPlatformHealthOverview(ctx, "owner", tenantSlug)
	if err != nil || reachableOverview.AgentExecutor.State != "reachable_unverified" {
		t.Fatalf("all configured live runners with fresh signed probes remain coding-unverified: state=%#v err=%v", reachableOverview.AgentExecutor, err)
	}
	if configuredOverview.AgentDecision.State != "unobserved" {
		t.Fatalf("missing source worker must not imply missing Jev credentials: %#v", configuredOverview.AgentDecision)
	}
	if err := postgres.UpsertWorkerHeartbeat(ctx, domain.WorkerHeartbeat{
		WorkerID: decisionWorkerID, Kind: "agent-task-source-admitter", Version: "decision-integration",
		Capacity: 1, StartedAt: now.Add(-time.Minute), HeartbeatAt: now, ExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `UPDATE worker_heartbeats SET decision_configured=NULL WHERE worker_id=$1`, decisionWorkerID); err != nil {
		t.Fatal(err)
	}
	legacyDecision, err := postgres.GetPlatformHealthOverview(ctx, "owner", tenantSlug)
	if err != nil || legacyDecision.AgentDecision.State != "unobserved" {
		t.Fatalf("legacy source heartbeat must remain unknown: state=%#v err=%v", legacyDecision.AgentDecision, err)
	}
	if err := postgres.UpsertWorkerHeartbeat(ctx, domain.WorkerHeartbeat{
		WorkerID: decisionWorkerID, Kind: "agent-task-source-admitter", Version: "decision-integration",
		Capacity: 1, StartedAt: now.Add(-time.Minute), HeartbeatAt: now.Add(time.Millisecond), ExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	unconfiguredDecision, err := postgres.GetPlatformHealthOverview(ctx, "owner", tenantSlug)
	if err != nil || unconfiguredDecision.AgentDecision.State != "not_configured" {
		t.Fatalf("missing Jev credentials must not appear configured: state=%#v err=%v", unconfiguredDecision.AgentDecision, err)
	}
	if err := postgres.UpsertWorkerHeartbeat(ctx, domain.WorkerHeartbeat{
		WorkerID: decisionWorkerID, Kind: "agent-task-source-admitter", Version: "decision-integration", DecisionConfigured: true,
		Capacity: 1, StartedAt: now.Add(-time.Minute), HeartbeatAt: now.Add(time.Second), ExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	configuredDecision, err := postgres.GetPlatformHealthOverview(ctx, "owner", tenantSlug)
	if err != nil || configuredDecision.AgentDecision.State != "configured_unverified" || configuredDecision.AgentExecutor.State != "reachable_unverified" {
		t.Fatalf("decision configuration and executor probe must remain independent: decision=%#v executor=%#v err=%v", configuredDecision.AgentDecision, configuredDecision.AgentExecutor, err)
	}
	for _, queue := range overview.Queues {
		wantRunning := int64(0)
		if queue.Key == "review-execution" || queue.Key == "agent-execution" {
			wantRunning = 1
		}
		wantReady := int64(0)
		if queue.Key == "agent-source" {
			wantReady = 1
		}
		if queue.State != "live" || queue.Source != "postgresql" || queue.Ready != wantReady || queue.Running != wantRunning || queue.Failed != 0 {
			t.Fatalf("unexpected empty queue sample: %#v", queue)
		}
	}
	requestID := uuid.New()
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO review_requests (id,tenant_id,installation_id,provider,api_base_url,repository,review_number)
		VALUES ($1,$2,$3,'github','https://api.github.com','RainLib/health',2)`, requestID, tenantID, installationID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO review_runs (request_id,state,trigger_kind,review_mode,head_sha,base_sha,created_at)
		VALUES ($1,'acknowledged','comment','configured','head','base',now()-interval '20 minutes')`, requestID); err != nil {
		t.Fatal(err)
	}
	staleOverview, err := postgres.GetPlatformHealthOverview(ctx, "owner", tenantSlug)
	if err != nil {
		t.Fatal(err)
	}
	var admissionObserved bool
	for _, queue := range staleOverview.Queues {
		if queue.Key != "review-admission" {
			continue
		}
		admissionObserved = true
		if queue.State != domain.HealthDegraded || queue.Ready != 1 || queue.Running != 0 || queue.Failed != 0 || queue.OldestAgeSeconds < 1200 {
			t.Fatalf("stale acknowledged review admission was not surfaced: %#v", queue)
		}
	}
	if !admissionObserved {
		t.Fatalf("review admission queue is absent: %#v", staleOverview.Queues)
	}
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO provider_issue_analysis_jobs (
			id,tenant_id,installation_id,last_delivery_id,provider,api_base_url,repository,issue_number,
			action,title,stable_marker,model_route,model_route_sha256,prompt_config,prompt_config_sha256
		) VALUES ($1,$2,$3,$4,'github','https://api.github.com','RainLib/health',44,
			'opened','OAuth Issue delivery fixture',$5,'{}'::jsonb,'model-fixture','{}'::jsonb,'prompt-fixture')`,
		issueJobID, tenantID, installationID, deliveryID, "health-issue-"+issueJobID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO outbox_messages (id,aggregate_type,aggregate_id,topic,dedupe_key,payload)
		VALUES ($1,'provider_issue',$2,'provider.issue.acknowledge',$3,
			jsonb_build_object('job_id',$4::text,'revision',1,'attempt',1))`,
		issueOutboxID, issueJobID, "health-issue-"+issueOutboxID.String(), issueJobID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.pool.Exec(ctx, `
		INSERT INTO inbox_messages (consumer,message_id,state,attempt,last_error)
		VALUES ('provider-issue-triager-v1',$1,'released',6,'synthetic credential resolver unavailable')`, issueOutboxID); err != nil {
		t.Fatal(err)
	}
	issueOverview, err := postgres.GetPlatformHealthOverview(ctx, "owner", tenantSlug)
	if err != nil {
		t.Fatal(err)
	}
	var issueObserved bool
	for _, queue := range issueOverview.Queues {
		if queue.Key == "provider-issue-triage" {
			issueObserved = true
			if queue.State != domain.HealthDegraded || queue.Ready != 1 || queue.Failed != 1 || queue.Running != 0 || queue.Source != "postgresql" {
				t.Fatalf("released Issue acknowledgement was not visible without leaking its error: %#v", queue)
			}
		}
	}
	if !issueObserved {
		t.Fatalf("provider Issue triage queue is absent: %#v", issueOverview.Queues)
	}
	if _, err := postgres.GetPlatformHealthOverview(ctx, "viewer", tenantSlug); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer health error=%v, want ErrForbidden", err)
	}
}
