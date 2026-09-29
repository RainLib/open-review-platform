package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/jackc/pgx/v5"
)

const platformHealthFreshness = 5 * time.Minute

func (s *PostgresStore) GetPlatformHealthOverview(ctx context.Context, actor, tenantSlug string) (domain.PlatformHealthOverview, error) {
	tenantID, role, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.PlatformHealthOverview{}, err
	}
	if role != "owner" && role != "admin" {
		return domain.PlatformHealthOverview{}, ErrForbidden
	}
	now := time.Now().UTC()
	overview := domain.PlatformHealthOverview{
		SampledAt:              now,
		FreshnessWindowSeconds: int64(platformHealthFreshness.Seconds()),
		Components: []domain.HealthComponent{
			{Key: "control-api", Name: "Control API", State: domain.HealthLive, Detail: "This tenant-scoped sample completed through the authenticated API.", Observed: true, SampledAt: &now, Capability: "admission and control-plane reads", RecoveryHint: "Inspect API logs and database connectivity."},
			{Key: "postgresql", Name: "PostgreSQL", State: domain.HealthLive, Detail: "The authoritative database answered this sampled transaction.", Observed: true, SampledAt: &now, Capability: "authoritative workflow and policy state", RecoveryHint: "Fail over or restore PostgreSQL before accepting writes."},
			{Key: "rabbitmq", Name: "RabbitMQ", State: domain.HealthConfiguredOnly, Detail: "Broker metrics are not ingested by the control API; PostgreSQL outbox remains authoritative.", Observed: false, Capability: "event delivery acceleration", RecoveryHint: "Inspect the broker management API, alarms, queue depth, and DLQ."},
			{Key: "workers", Name: "Worker fleet", State: domain.HealthConfiguredOnly, Detail: "No recent durable worker heartbeat is available.", Observed: false, Capability: "review execution, notifications, SSO probes, and governance jobs", RecoveryHint: "Inspect worker process health, lease renewal, and executor logs."},
			{Key: "publisher", Name: "Provider integrations", State: domain.HealthConfiguredOnly, Detail: "Publication receipts show outcomes, but no recent read-only access probe is available.", Observed: false, Capability: "credential validity, read access, comments, inline findings, and checks", RecoveryHint: "Inspect access probes, failed receipts, provider permissions, and rate limits."},
		},
		Queues:                 []domain.QueueHealth{},
		GitLabAuthorAdmissions: []domain.GitLabAuthorAdmissionHealthItem{},
		Workers:                []domain.WorkerHealth{},
		AgentExecutor: domain.AgentExecutorReadiness{
			State:  "unobserved",
			Detail: "No fresh Agent runner configuration heartbeat was observed. Jev classification and coding execution are separate capabilities.",
		},
		AgentDecision: domain.AgentDecisionReadiness{
			State:  "unobserved",
			Detail: "No fresh Agent source worker configuration heartbeat was observed. Jev availability is not established by repository policy alone.",
		},
		AgentCredentialBroker: domain.AgentCredentialBrokerReadiness{
			State:  "unobserved",
			Detail: "No fresh private coding-credential broker heartbeat was observed. Provider write credentials and Draft PR/MR publication remain unverified.",
		},
		Providers:                 []domain.ProviderHealth{},
		Incidents:                 []domain.PlatformIncident{},
		IncidentTrackingAvailable: false,
		WorkerHeartbeatAvailable:  false,
		BrokerMetricsAvailable:    false,
		Runbooks: []domain.PlatformRunbook{
			{Key: "review-backlog", Title: "Review backlog recovery", Version: "v1", RequiredRole: "platform operator", Steps: []string{"Confirm PostgreSQL authority", "Inspect expired leases", "Restore workers", "Observe backlog age"}, Executable: false, Detail: "Read-only guide; the console does not execute arbitrary shell commands."},
			{Key: "gitlab-author-admission", Title: "GitLab author admission recovery", Version: "v1", RequiredRole: "workspace admin", Steps: []string{"Inspect deferred author-admission queue and final failure count", "Verify the GitLab installation and credential without exposing the token", "Restore provider access before requesting a fresh MR event or authorized review"}, Executable: false, Detail: "A failed author lookup never admits the old webhook automatically; a fresh event must pass the exact-current-head check."},
			{Key: "provider-publication", Title: "Provider publication recovery", Version: "v1", RequiredRole: "workspace admin", Steps: []string{"Inspect receipt errors", "Verify app permissions", "Check provider rate limits", "Retry through an approved workflow"}, Executable: false, Detail: "Provider mutations remain outside this health read model."},
			{Key: "broker-rebuild", Title: "Broker rebuild from outbox", Version: "v1", RequiredRole: "platform operator", Steps: []string{"Keep PostgreSQL online", "Restore RabbitMQ", "Start outbox relay", "Verify inbox deduplication"}, Executable: false, Detail: "PostgreSQL outbox is the recovery authority; never purge the DLQ as a shortcut."},
		},
	}
	// Aggregate signed adapter reachability without exposing worker identity,
	// URLs, secrets, counts, or load. A successful ping is not coding readiness.
	var knownAgentRunners, configuredAgentRunners, reachableAdapter, unreachableAdapter, unprobedAdapter bool
	if err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FILTER (WHERE adapter_configured IS NOT NULL) > 0,
		       COALESCE(bool_or(adapter_configured), false),
		       COALESCE(bool_or(adapter_configured AND adapter_reachable IS TRUE
		         AND adapter_probe_at > now()-interval '45 seconds'
		         AND adapter_probe_at BETWEEN heartbeat_at-interval '5 seconds' AND heartbeat_at), false),
		       COALESCE(bool_or(adapter_configured AND adapter_reachable IS FALSE
		         AND adapter_probe_at > now()-interval '45 seconds'
		         AND adapter_probe_at BETWEEN heartbeat_at-interval '5 seconds' AND heartbeat_at), false),
		       COALESCE(bool_or(adapter_configured IS NOT TRUE OR adapter_probe_at IS NULL
		         OR adapter_probe_at <= now()-interval '45 seconds'
		         OR adapter_probe_at NOT BETWEEN heartbeat_at-interval '5 seconds' AND heartbeat_at), false)
		FROM worker_heartbeats
		WHERE kind='agent-task-runner' AND expires_at > now()
		  AND heartbeat_at > now()-interval '5 minutes'`).Scan(&knownAgentRunners, &configuredAgentRunners, &reachableAdapter, &unreachableAdapter, &unprobedAdapter); err != nil {
		return domain.PlatformHealthOverview{}, fmt.Errorf("sample agent runner configuration: %w", err)
	}
	if knownAgentRunners {
		if configuredAgentRunners {
			switch {
			case reachableAdapter && (unreachableAdapter || unprobedAdapter):
				overview.AgentExecutor = domain.AgentExecutorReadiness{State: "partially_reachable", Detail: "At least one runner passed a signed adapter probe, but another failed or has no fresh probe. Dispatch may fail; model credentials, sandbox isolation and provider writes remain unverified."}
			case reachableAdapter:
				overview.AgentExecutor = domain.AgentExecutorReadiness{State: "reachable_unverified", Detail: "A live runner completed a signed adapter probe within 45 seconds. Model credentials, sandbox isolation and provider writes remain unverified."}
			case unreachableAdapter:
				overview.AgentExecutor = domain.AgentExecutorReadiness{State: "unreachable", Detail: "A configured live runner's recent signed adapter probe failed. Approved plans may not reach the coding adapter."}
			default:
				overview.AgentExecutor = domain.AgentExecutorReadiness{State: "configured_unverified", Detail: "A live Agent runner has adapter client configuration, but no fresh signed probe. Adapter reachability and coding capability are unverified."}
			}
		} else {
			overview.AgentExecutor = domain.AgentExecutorReadiness{
				State:  "not_configured",
				Detail: "A live Agent runner has no coding adapter configured. Approved plans cannot execute until an isolated adapter is deployed.",
			}
		}
	}
	// This is a source-worker configuration bit, not a Jev request or a
	// coding-executor probe. Legacy heartbeats remain unknown after migration.
	var knownDecisionWorkers, configuredDecisionWorkers bool
	if err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FILTER (WHERE decision_configured IS NOT NULL) > 0,
		       COALESCE(bool_or(decision_configured), false)
		FROM worker_heartbeats
		WHERE kind='agent-task-source-admitter' AND expires_at > now()
		  AND heartbeat_at > now()-interval '5 minutes'`).Scan(&knownDecisionWorkers, &configuredDecisionWorkers); err != nil {
		return domain.PlatformHealthOverview{}, fmt.Errorf("sample Agent decision configuration: %w", err)
	}
	if knownDecisionWorkers {
		if configuredDecisionWorkers {
			overview.AgentDecision = domain.AgentDecisionReadiness{
				State:  "configured_unverified",
				Detail: "A live Agent source worker has a locally valid Jev endpoint and API key. Remote service availability and decision quality are not verified by this heartbeat.",
			}
		} else {
			overview.AgentDecision = domain.AgentDecisionReadiness{
				State:  "not_configured",
				Detail: "A live Agent source worker has no valid Jev endpoint and API key. New Jev-classified Agent tasks cannot complete source admission.",
			}
		}
	}
	// Only report that a separately configured broker process is heartbeating.
	// Do not expose its identity, installation map, credentials, or tenant scope;
	// this is not a token issuance or provider-write probe.
	var brokerObserved bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM worker_heartbeats
			WHERE kind='agent-credential-broker' AND expires_at > now()
			  AND heartbeat_at > now()-interval '5 minutes'
		)`).Scan(&brokerObserved); err != nil {
		return domain.PlatformHealthOverview{}, fmt.Errorf("sample Agent credential broker heartbeat: %w", err)
	}
	if brokerObserved {
		overview.AgentCredentialBroker = domain.AgentCredentialBrokerReadiness{
			State:  "configured_unverified",
			Detail: "A private coding-credential broker passed startup configuration checks and has a fresh process heartbeat. Tenant mapping, token issuance, provider write access, and Draft PR/MR publication remain unverified.",
		}
	}

	// Incidents are explicit operator records, not inferred from a single
	// sampled queue or provider state. Preserve resolved incidents in the
	// bounded timeline so the health page can explain a recovery without
	// rewriting the incident that originally captured it.
	incidentRows, err := s.pool.Query(ctx, `
		SELECT `+platformIncidentColumns+`
		FROM platform_incidents
		WHERE tenant_id=$1
		ORDER BY CASE state WHEN 'active' THEN 0 WHEN 'mitigating' THEN 1 ELSE 2 END,
		         started_at DESC,id DESC
		LIMIT 100`, tenantID)
	if err != nil {
		return domain.PlatformHealthOverview{}, fmt.Errorf("list platform incidents: %w", err)
	}
	for incidentRows.Next() {
		incident, err := scanPlatformIncident(incidentRows)
		if err != nil {
			incidentRows.Close()
			return domain.PlatformHealthOverview{}, fmt.Errorf("scan platform incident: %w", err)
		}
		overview.Incidents = append(overview.Incidents, incident)
	}
	if err := incidentRows.Err(); err != nil {
		incidentRows.Close()
		return domain.PlatformHealthOverview{}, fmt.Errorf("iterate platform incidents: %w", err)
	}
	incidentRows.Close()
	overview.IncidentTrackingAvailable = true

	queueSpecs := []struct {
		key, name, query string
	}{
		{"review-admission", "Review admission", `SELECT COUNT(*) FILTER (WHERE run.state='acknowledged'),0::bigint,0::bigint,0::bigint,COALESCE(EXTRACT(EPOCH FROM (now()-MIN(run.created_at) FILTER (WHERE run.state='acknowledged'))),0)::bigint FROM review_runs run JOIN review_requests request ON request.id=run.request_id WHERE request.tenant_id=$1`},
		{"gitlab-author-admission", "GitLab author verification", `SELECT
			COUNT(*) FILTER (WHERE admission.state='queued'),
			COUNT(*) FILTER (WHERE admission.state='running'),
			COUNT(*) FILTER (WHERE admission.state='failed'),
			COUNT(*) FILTER (WHERE admission.state='running' AND admission.locked_until<now()),
			COALESCE(EXTRACT(EPOCH FROM (now()-MIN(admission.created_at) FILTER (WHERE admission.state='queued'))),0)::bigint
			FROM gitlab_author_admissions admission
			JOIN provider_installations installation ON installation.id=admission.installation_id
			WHERE installation.tenant_id=$1`},
		{"review-execution", "Review execution", `SELECT COUNT(*) FILTER (WHERE state='queued'),COUNT(*) FILTER (WHERE state='running'),COUNT(*) FILTER (WHERE state='failed'),COUNT(*) FILTER (WHERE state='running' AND locked_until < now()),COALESCE(EXTRACT(EPOCH FROM (now()-MIN(created_at) FILTER (WHERE state='queued'))),0)::bigint FROM review_jobs WHERE tenant_id=$1`},
		{"agent-source", "Agent source capture", `SELECT COUNT(*) FILTER (WHERE source_state='pending'),0::bigint,COUNT(*) FILTER (WHERE source_state='failed'),0::bigint,COALESCE(EXTRACT(EPOCH FROM (now()-MIN(created_at) FILTER (WHERE source_state='pending'))),0)::bigint FROM agent_tasks WHERE tenant_id=$1 AND state IN ('received','plan_ready','awaiting_approval')`},
		{"agent-execution", "Agent execution handoff", `SELECT COUNT(*) FILTER (WHERE attempt.state='queued'),COUNT(*) FILTER (WHERE attempt.state='running'),COUNT(*) FILTER (WHERE attempt.state IN ('failed','needs_attention')),COUNT(*) FILTER (WHERE attempt.state='running' AND attempt.locked_until < now()),COALESCE(EXTRACT(EPOCH FROM (now()-MIN(attempt.created_at) FILTER (WHERE attempt.state='queued'))),0)::bigint FROM agent_task_attempts attempt JOIN agent_tasks task ON task.id=attempt.task_id WHERE task.tenant_id=$1`},
		{"provider-issue-triage", "Provider Issue triage", `SELECT
			COUNT(*) FILTER (WHERE job.state IN ('queued','acknowledged')),
			0::bigint,
			COUNT(*) FILTER (WHERE job.state='failed' OR (job.state IN ('queued','acknowledged') AND EXISTS (
				SELECT 1 FROM outbox_messages outgoing JOIN inbox_messages incoming ON incoming.message_id=outgoing.id
				WHERE outgoing.aggregate_id=job.id AND outgoing.topic IN ('provider.issue.acknowledge','provider.issue.analyze')
				  AND outgoing.payload->>'revision'=job.revision::text
				  AND outgoing.payload->>'attempt'=job.analysis_attempt::text
				  AND incoming.consumer='provider-issue-triager-v1' AND incoming.state='released' AND incoming.last_error<>''
			))),
			0::bigint,
			COALESCE(EXTRACT(EPOCH FROM (now()-MIN(job.created_at) FILTER (WHERE job.state IN ('queued','acknowledged')))),0)::bigint
			FROM provider_issue_analysis_jobs job WHERE job.tenant_id=$1`},
		{"rule-tests", "Rule test runs", `SELECT COUNT(*) FILTER (WHERE state='queued'),COUNT(*) FILTER (WHERE state='running'),COUNT(*) FILTER (WHERE state='failed'),0::bigint,COALESCE(EXTRACT(EPOCH FROM (now()-MIN(created_at) FILTER (WHERE state='queued'))),0)::bigint FROM rule_test_runs WHERE tenant_id=$1`},
		{"data-governance", "Data governance jobs", `SELECT COUNT(*) FILTER (WHERE state='queued'),COUNT(*) FILTER (WHERE state='running'),COUNT(*) FILTER (WHERE state='failed'),0::bigint,COALESCE(EXTRACT(EPOCH FROM (now()-MIN(created_at) FILTER (WHERE state='queued'))),0)::bigint FROM data_governance_jobs WHERE tenant_id=$1`},
		{"sso-probes", "SSO metadata probes", `SELECT COUNT(*) FILTER (WHERE state='queued'),COUNT(*) FILTER (WHERE state='running'),COUNT(*) FILTER (WHERE state='failed'),COUNT(*) FILTER (WHERE state='running' AND locked_until < now()),COALESCE(EXTRACT(EPOCH FROM (now()-MIN(created_at) FILTER (WHERE state='queued'))),0)::bigint FROM workspace_sso_probe_receipts WHERE tenant_id=$1`},
		{"notifications", "Notification deliveries", `SELECT COUNT(*) FILTER (WHERE delivery.state='pending'),COUNT(*) FILTER (WHERE delivery.state='sending'),COUNT(*) FILTER (WHERE delivery.state='failed'),0::bigint,COALESCE(EXTRACT(EPOCH FROM (now()-MIN(delivery.created_at) FILTER (WHERE delivery.state='pending'))),0)::bigint FROM notification_deliveries delivery JOIN notification_destinations destination ON destination.id=delivery.destination_id WHERE destination.tenant_id=$1`},
	}
	for _, spec := range queueSpecs {
		item := domain.QueueHealth{Key: spec.key, Name: spec.name, Source: "postgresql", SampledAt: now, Detail: "Tenant-scoped authoritative workflow state."}
		if err := s.pool.QueryRow(ctx, spec.query, tenantID).Scan(&item.Ready, &item.Running, &item.Failed, &item.ExpiredLeases, &item.OldestAgeSeconds); err != nil {
			return domain.PlatformHealthOverview{}, fmt.Errorf("sample %s queue: %w", spec.key, err)
		}
		item.State = queueHealthState(item)
		if spec.key == "review-admission" {
			item.Detail = "Acknowledged reviews awaiting provider progress publication or admission; stale items require interaction responder, credential, outbox, and DLQ inspection before retry."
		}
		if spec.key == "gitlab-author-admission" {
			item.Detail = "GitLab MR webhooks waiting for a credential-bearing author reread. Failed attempts never start a review; inspect the installation and request a fresh provider event after recovery."
		}
		if spec.key == "agent-execution" {
			item.Detail = "Tenant-scoped attempt leases and handoff state; this does not probe the external coding adapter."
		}
		if spec.key == "agent-source" {
			item.Detail = "Tenant-scoped source snapshots pending or failed; this does not prove model or provider reachability."
		}
		if spec.key == "provider-issue-triage" {
			item.Detail = "Tenant-scoped Issue jobs awaiting acknowledgement or analysis. Failed includes current-revision consumer delivery errors; inspect the inbox/DLQ and provider credential resolver before retrying. RabbitMQ depth is not sampled here."
		}
		overview.Queues = append(overview.Queues, item)
	}
	// Bound the detail read independently of the aggregate count. This list is
	// actionable but must never include a webhook payload, token, or another
	// tenant's repository. Failed items sort first for operator triage.
	authorRows, err := s.pool.Query(ctx, `
		SELECT admission.delivery_id,admission.installation_id,
		       admission.repository,admission.review_number,
		       installation.api_base_url,admission.head_sha,admission.state,
		       admission.attempt,admission.error_code,admission.updated_at
		FROM gitlab_author_admissions admission
		JOIN provider_installations installation ON installation.id=admission.installation_id
		WHERE installation.tenant_id=$1 AND admission.state IN ('queued','running','failed')
		ORDER BY CASE admission.state WHEN 'failed' THEN 0 WHEN 'running' THEN 1 ELSE 2 END,
		         admission.updated_at DESC,admission.delivery_id DESC
		LIMIT 12`, tenantID)
	if err != nil {
		return domain.PlatformHealthOverview{}, fmt.Errorf("list GitLab author admissions: %w", err)
	}
	for authorRows.Next() {
		var item domain.GitLabAuthorAdmissionHealthItem
		if err := authorRows.Scan(&item.DeliveryID, &item.InstallationID,
			&item.Repository, &item.ReviewNumber,
			&item.APIBaseURL, &item.HeadSHA, &item.State,
			&item.Attempt, &item.ErrorCode, &item.UpdatedAt); err != nil {
			authorRows.Close()
			return domain.PlatformHealthOverview{}, fmt.Errorf("scan GitLab author admission: %w", err)
		}
		overview.GitLabAuthorAdmissions = append(overview.GitLabAuthorAdmissions, item)
	}
	if err := authorRows.Err(); err != nil {
		authorRows.Close()
		return domain.PlatformHealthOverview{}, fmt.Errorf("iterate GitLab author admissions: %w", err)
	}
	authorRows.Close()

	type workerLeaseSample struct {
		kind         string
		active       int64
		expired      int64
		lastActivity *time.Time
	}
	leaseSamples := map[string]workerLeaseSample{}
	workerRows, err := s.pool.Query(ctx, `
		WITH tenant_worker_activity AS (
			SELECT locked_by AS worker_id,'review-runner' AS kind,state,locked_until,COALESCE(started_at,created_at) AS activity_at
			FROM review_jobs WHERE tenant_id=$1 AND locked_by IS NOT NULL
			UNION ALL
			SELECT locked_by,'rule-test-runner',state,locked_until,COALESCE(started_at,created_at)
			FROM rule_test_runs WHERE tenant_id=$1 AND locked_by IS NOT NULL
			UNION ALL
			SELECT worker_id,'data-governance-worker',state,locked_until,COALESCE(started_at,created_at)
			FROM data_governance_jobs WHERE tenant_id=$1 AND worker_id IS NOT NULL
			UNION ALL
			SELECT admission.worker_id,'gitlab-author-admitter',admission.state,admission.locked_until,admission.updated_at
			FROM gitlab_author_admissions admission
			JOIN provider_installations installation ON installation.id=admission.installation_id
			WHERE installation.tenant_id=$1 AND admission.worker_id IS NOT NULL
			UNION ALL
			SELECT worker_id,'sso-prober',state,locked_until,COALESCE(started_at,created_at)
			FROM workspace_sso_probe_receipts WHERE tenant_id=$1 AND worker_id IS NOT NULL
			UNION ALL
			SELECT attempt.worker_id,'agent-task-runner',attempt.state,attempt.locked_until,COALESCE(attempt.started_at,attempt.created_at)
			FROM agent_task_attempts attempt JOIN agent_tasks task ON task.id=attempt.task_id
			WHERE task.tenant_id=$1 AND attempt.worker_id IS NOT NULL
		)
		SELECT worker_id,MIN(kind),
		       COUNT(*) FILTER (WHERE state='running' AND locked_until >= now()),
		       COUNT(*) FILTER (WHERE state='running' AND locked_until < now()),
		       MAX(activity_at)
		FROM tenant_worker_activity
		GROUP BY worker_id ORDER BY worker_id`, tenantID)
	if err != nil {
		return domain.PlatformHealthOverview{}, fmt.Errorf("sample tenant worker activity: %w", err)
	}
	for workerRows.Next() {
		var workerID string
		var sample workerLeaseSample
		if err := workerRows.Scan(&workerID, &sample.kind, &sample.active, &sample.expired, &sample.lastActivity); err != nil {
			workerRows.Close()
			return domain.PlatformHealthOverview{}, fmt.Errorf("scan tenant worker activity: %w", err)
		}
		leaseSamples[workerID] = sample
	}
	if err := workerRows.Err(); err != nil {
		return domain.PlatformHealthOverview{}, fmt.Errorf("iterate tenant worker activity: %w", err)
	}
	workerRows.Close()

	// A heartbeat registry is shared infrastructure. Only read heartbeats for
	// workers that have a current lease on this tenant's durable work; reading
	// the full registry would disclose other tenants' fleet topology and load.
	workerIDs := make([]string, 0, len(leaseSamples))
	for workerID := range leaseSamples {
		workerIDs = append(workerIDs, workerID)
	}
	sort.Strings(workerIDs)
	var heartbeatRows pgx.Rows
	if len(workerIDs) > 0 {
		heartbeatRows, err = s.pool.Query(ctx, `
			SELECT worker_id,kind,version,heartbeat_at,expires_at
			FROM worker_heartbeats
			WHERE worker_id = ANY($1::text[]) AND heartbeat_at > now()-interval '24 hours'
			ORDER BY kind,worker_id`, workerIDs)
		if err != nil {
			return domain.PlatformHealthOverview{}, fmt.Errorf("sample tenant worker heartbeats: %w", err)
		}
	}
	var latestHeartbeat *time.Time
	freshWorkers := 0
	staleWorkers := 0
	for heartbeatRows != nil && heartbeatRows.Next() {
		var rawWorkerID, kind, version string
		var heartbeatAt, expiresAt time.Time
		if err := heartbeatRows.Scan(&rawWorkerID, &kind, &version, &heartbeatAt, &expiresAt); err != nil {
			heartbeatRows.Close()
			return domain.PlatformHealthOverview{}, fmt.Errorf("scan worker heartbeat: %w", err)
		}
		sample := leaseSamples[rawWorkerID]
		delete(leaseSamples, rawWorkerID)
		worker := domain.WorkerHealth{
			WorkerID: anonymizedWorkerID(tenantID.String(), kind, rawWorkerID), Kind: kind,
			State: domain.HealthLive, ActiveLeases: sample.active, ExpiredLeases: sample.expired,
			LastActivity: sample.lastActivity, LastHeartbeat: &heartbeatAt, Version: version,
			Detail: "A durable process heartbeat is associated with a current tenant execution lease.",
		}
		if latestHeartbeat == nil || heartbeatAt.After(*latestHeartbeat) {
			copy := heartbeatAt
			latestHeartbeat = &copy
		}
		if !expiresAt.After(now) || now.Sub(heartbeatAt) > platformHealthFreshness {
			worker.State = domain.HealthStale
			worker.Detail = "The last durable process heartbeat is outside its freshness window."
			staleWorkers++
		} else if sample.expired > 0 {
			worker.State = domain.HealthDegraded
			worker.Detail = "Heartbeat is fresh, but a tenant execution lease expired."
			freshWorkers++
		} else {
			freshWorkers++
		}
		overview.Workers = append(overview.Workers, worker)
	}
	if heartbeatRows != nil {
		if err := heartbeatRows.Err(); err != nil {
			heartbeatRows.Close()
			return domain.PlatformHealthOverview{}, fmt.Errorf("iterate worker heartbeats: %w", err)
		}
		heartbeatRows.Close()
	}
	if latestHeartbeat != nil {
		overview.WorkerHeartbeatAvailable = true
		for index := range overview.Components {
			if overview.Components[index].Key != "workers" {
				continue
			}
			overview.Components[index].Observed = true
			overview.Components[index].SampledAt = latestHeartbeat
			overview.Components[index].State = domain.HealthLive
			overview.Components[index].Detail = fmt.Sprintf("Durable registry observed %d fresh and %d stale worker process(es) with current tenant leases.", freshWorkers, staleWorkers)
			if freshWorkers == 0 {
				overview.Components[index].State = domain.HealthStale
			} else if staleWorkers > 0 {
				overview.Components[index].State = domain.HealthDegraded
			}
		}
	}
	remainingWorkerIDs := make([]string, 0, len(leaseSamples))
	for rawWorkerID := range leaseSamples {
		remainingWorkerIDs = append(remainingWorkerIDs, rawWorkerID)
	}
	sort.Strings(remainingWorkerIDs)
	for _, rawWorkerID := range remainingWorkerIDs {
		sample := leaseSamples[rawWorkerID]
		worker := domain.WorkerHealth{
			WorkerID: anonymizedWorkerID(tenantID.String(), sample.kind, rawWorkerID), Kind: sample.kind,
			State: domain.HealthConfiguredOnly, ActiveLeases: sample.active, ExpiredLeases: sample.expired,
			LastActivity: sample.lastActivity, Detail: "Tenant lease activity exists, but this worker has no recent durable heartbeat.",
		}
		if sample.expired > 0 {
			worker.State = domain.HealthDegraded
			worker.Detail = "At least one tenant lease expired and no recent durable heartbeat is available."
		}
		overview.Workers = append(overview.Workers, worker)
	}

	providerRows, err := s.pool.Query(ctx, `
		SELECT installation.id,installation.provider,installation.api_base_url,installation.repository_scope,installation.active,
		       MAX(delivery.received_at),MAX(receipt.published_at),COUNT(DISTINCT receipt.id) FILTER (WHERE receipt.last_error IS NOT NULL AND receipt.updated_at > now()-interval '24 hours'),
		       probe.health_state,probe.observed_at,COALESCE(probe.permissions,'[]'::jsonb),
		       probe.rate_limit_remaining,probe.rate_limit_limit,probe.rate_limit_reset_at,
		       COALESCE(probe.latency_ms,0),COALESCE(probe.error_code,''),COALESCE(probe.error_message,'')
		FROM provider_installations installation
		LEFT JOIN review_jobs job ON job.installation_id=installation.id
		LEFT JOIN webhook_deliveries delivery ON delivery.id=job.delivery_id
		LEFT JOIN review_requests request ON request.installation_id=installation.id
		LEFT JOIN review_runs run ON run.request_id=request.id
		LEFT JOIN publication_receipts receipt ON receipt.run_id=run.id
		LEFT JOIN provider_health_probes probe ON probe.installation_id=installation.id
		WHERE installation.tenant_id=$1
		GROUP BY installation.id,probe.installation_id ORDER BY installation.created_at DESC`, tenantID)
	if err != nil {
		return domain.PlatformHealthOverview{}, fmt.Errorf("sample providers: %w", err)
	}
	observedProviders := 0
	staleProviders := 0
	degradedProviders := 0
	criticalProviders := 0
	var latestProviderProbe *time.Time
	for providerRows.Next() {
		var item domain.ProviderHealth
		var apiBase string
		var probeState *string
		var permissionsJSON []byte
		var errorCode, errorMessage string
		if err := providerRows.Scan(
			&item.InstallationID, &item.Provider, &apiBase, &item.RepositoryScope, &item.Active,
			&item.LastWebhookAt, &item.LastPublishAt, &item.PublishFailures,
			&probeState, &item.LastProbeAt, &permissionsJSON,
			&item.RateLimitRemaining, &item.RateLimitLimit, &item.RateLimitResetAt,
			&item.ProbeLatencyMS, &errorCode, &errorMessage,
		); err != nil {
			providerRows.Close()
			return domain.PlatformHealthOverview{}, fmt.Errorf("scan provider health: %w", err)
		}
		if err := json.Unmarshal(permissionsJSON, &item.Permissions); err != nil {
			providerRows.Close()
			return domain.PlatformHealthOverview{}, fmt.Errorf("decode provider permissions: %w", err)
		}
		parsed, parseErr := url.Parse(apiBase)
		if parseErr == nil {
			item.Host = parsed.Host
		}
		item.State = domain.HealthConfiguredOnly
		item.Detail = "Activity timestamps are observed, but no read-only provider access/rate-limit probe has completed."
		if !item.Active {
			item.State = domain.HealthCritical
			item.Detail = "Installation is disabled."
		} else if probeState != nil && item.LastProbeAt != nil {
			observedProviders++
			if latestProviderProbe == nil || item.LastProbeAt.After(*latestProviderProbe) {
				copy := *item.LastProbeAt
				latestProviderProbe = &copy
			}
			item.State = domain.HealthState(*probeState)
			item.Detail = "Read-only provider access and rate-limit evidence are fresh. Write permissions are not inferred without a side effect."
			if now.Sub(*item.LastProbeAt) > 15*time.Minute {
				item.State = domain.HealthStale
				item.Detail = "The last provider access probe is outside its 15-minute freshness window."
				staleProviders++
			} else if errorCode != "" {
				item.Detail = errorMessage
			}
		}
		if item.State == domain.HealthLive {
			switch providerIssueTriageCapability(item.Permissions) {
			case "ready":
				item.Detail = "Read-only provider access is fresh and the GitHub App declares the Issue write permission and Issues event subscription."
			case "missing_event":
				item.State = domain.HealthDegraded
				item.Detail = "Repository access is healthy, but the GitHub App is not subscribed to Issues events; user-authored Issue analysis will not start automatically."
			case "missing_write_permission":
				item.State = domain.HealthDegraded
				item.Detail = "Repository access is healthy, but the GitHub App does not declare Issues write permission; Issue analysis cannot maintain its result comment."
			case "unobserved":
				item.Detail = "Repository access is healthy, but the GitHub App Issue-event registration could not be observed by the latest probe."
			}
		}
		if item.PublishFailures > 0 && item.State == domain.HealthLive {
			item.State = domain.HealthDegraded
			item.Detail = "Recent publication failures require receipt and permission review."
		}
		switch item.State {
		case domain.HealthCritical:
			criticalProviders++
		case domain.HealthDegraded:
			degradedProviders++
		}
		overview.Providers = append(overview.Providers, item)
	}
	if err := providerRows.Err(); err != nil {
		return domain.PlatformHealthOverview{}, fmt.Errorf("iterate providers: %w", err)
	}
	providerRows.Close()
	if observedProviders > 0 {
		for index := range overview.Components {
			if overview.Components[index].Key != "publisher" {
				continue
			}
			overview.Components[index].Observed = true
			overview.Components[index].SampledAt = latestProviderProbe
			overview.Components[index].State = domain.HealthLive
			overview.Components[index].Detail = fmt.Sprintf("Read-only provider probes observed %d installation(s): %d critical, %d degraded, and %d stale.", observedProviders, criticalProviders, degradedProviders, staleProviders)
			if criticalProviders > 0 {
				overview.Components[index].State = domain.HealthCritical
			} else if degradedProviders > 0 || staleProviders > 0 {
				overview.Components[index].State = domain.HealthDegraded
			}
		}
	}
	return overview, nil
}

func providerIssueTriageCapability(permissions []string) string {
	for _, permission := range permissions {
		if state, ok := strings.CutPrefix(permission, "issue_triage:"); ok {
			switch state {
			case "ready", "missing_event", "missing_write_permission", "unobserved":
				return state
			}
		}
	}
	return ""
}

func queueHealthState(item domain.QueueHealth) domain.HealthState {
	if item.ExpiredLeases > 0 || item.Failed > 0 {
		return domain.HealthDegraded
	}
	if item.OldestAgeSeconds > int64((15 * time.Minute).Seconds()) {
		return domain.HealthDegraded
	}
	return domain.HealthLive
}

func anonymizedWorkerID(tenantID, kind, workerID string) string {
	sum := sha256.Sum256([]byte(tenantID + "\x00" + workerID))
	return kind + "/" + hex.EncodeToString(sum[:6])
}
