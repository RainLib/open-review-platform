import { createHash } from "node:crypto";
import {
  validateIssueFilters,
  type IssueFilterGroup,
  type IssueSavedView,
  type IssueView,
} from "@/lib/issue-filters";
import { assembleAgentTaskData } from "@/lib/agent-task-data";

import {
  getControlPlaneRequestConfiguration,
  type ControlPlaneRequestConfiguration,
} from "@/lib/control-plane-proxy";

export type RunState =
  | "acknowledged"
  | "admitted"
  | "preparing"
  | "analyzing"
  | "normalizing"
  | "publishing"
  | "completed"
  | "failed"
  | "cancelled"
  | "superseded"
  | "needs_attention";

export type ReviewRun = {
  id: string;
  revision: number;
  state: RunState;
  trigger_kind: string;
  review_mode?: "configured" | "standard" | "deep" | "security";
  head_sha: string;
  base_sha: string;
  failure_code?: string;
  failure_message?: string;
  created_at: string;
  started_at?: string;
  finished_at?: string;
  provider: "github" | "gitlab";
  api_base_url?: string;
  repository: string;
  review_number: number;
  title?: string;
  author?: string;
  intervention?: ReviewIntervention;
  queue_block?: {
    installation_id: string;
    reason: "installation_inactive" | "verification_required" | "acknowledgement_exhausted";
    verification_state: "legacy" | "pending" | "checking" | "verified" | "failed";
  };
};

export type ReviewInterventionState = "open" | "claimed" | "resolved";

export type ReviewIntervention = {
  id: string;
  run_id?: string;
  revision: number;
  state: ReviewInterventionState;
  assignee_subject?: string;
  opened_at: string;
  claimed_at?: string;
  resolved_at?: string;
  resolved_by?: string;
  resolution?: string;
  reason?: string;
  run_revision?: number;
  run_state?: RunState;
  failure_code?: string;
  failure_message?: string;
  provider?: "github" | "gitlab";
  api_base_url?: string;
  repository?: string;
  review_number?: number;
  title?: string;
  author?: string;
};

export type ReviewInterventionData = {
  source: DataSource;
  interventions: ReviewIntervention[];
  detail?: string;
};

export type CLIReviewRun = ReviewRun & {
  caller_subject: string;
};

export type ReviewScheduleState =
  | "scheduled"
  | "blocked"
  | "admitted"
  | "coalesced"
  | "cancelled";

export type ReviewSchedule = {
  id: string;
  source_run_id: string;
  admitted_run_id?: string;
  revision: number;
  state: ReviewScheduleState;
  provider: "github" | "gitlab";
  api_base_url?: string;
  repository: string;
  review_number: number;
  title?: string;
  author?: string;
  review_mode: "configured" | "standard" | "deep" | "security";
  base_sha: string;
  head_sha: string;
  scheduled_for: string;
  requested_by: string;
  blocked_reason?: string;
  cancelled_by?: string;
  cancelled_at?: string;
  created_at: string;
  updated_at: string;
};

export type ReviewScheduleData = {
  source: DataSource;
  schedules: ReviewSchedule[];
  detail?: string;
};

export type RuleSet = {
  id: string;
  name: string;
  description: string;
  created_by: string;
  created_at: string;
  updated_at: string;
  catalog?: {
    id: string;
    version: string;
    content_sha256: string;
    origin: string;
  };
  latest_version?: {
    id: string;
    version: number;
    revision: number;
    state: "draft" | "in_review" | "approved" | "published" | "retired";
    content_sha256: string;
    created_by: string;
    created_at: string;
    updated_at: string;
  };
};

export type RuleCatalogEntry = {
  id: string;
  version: string;
  title: string;
  description: string;
  tags: string[];
  rule_count: number;
  content_sha256: string;
  origin: string;
  can_install: boolean;
  installation?: {
    rule_set_id: string;
    rule_set_name: string;
    rule_version: number;
    rule_version_state:
      | "draft"
      | "in_review"
      | "approved"
      | "published"
      | "retired";
  };
};

export type RuleCatalogData = {
  source: DataSource;
  entries: RuleCatalogEntry[];
  detail?: string;
};

export type RuleApproval = {
  id: string;
  rule_set_id: string;
  rule_set_name: string;
  rule_version_id: string;
  version: number;
  version_state: "draft" | "in_review" | "approved" | "published" | "retired";
  content_sha256: string;
  requested_by: string;
  required_approvals: number;
  approval_count: number;
  rejection_count: number;
  actor_decision?: "approved" | "rejected";
  remediation_task_id?: string;
  can_decide: boolean;
  can_publish: boolean;
  state: "pending" | "approved" | "rejected" | "cancelled";
  created_at: string;
  decided_at?: string;
};

export type RuleApprovalData = {
  source: DataSource;
  approvals: RuleApproval[];
  detail?: string;
};

export type RuleImpactPreview = {
  rule_set_id: string;
  rule_set_name: string;
  rule_version_id: string;
  version: number;
  version_state: string;
  content_sha256: string;
  repository: string;
  provider: "github" | "gitlab";
  api_base_url: string;
  target_branch: string;
  precedence: number;
  valid: boolean;
  conflict?: string;
  baseline_sha256: string;
  candidate_sha256?: string;
  baseline_rule_count: number;
  candidate_rule_count: number;
  matched_bindings: number;
  added_rule_keys: string[];
  changed_rule_keys: string[];
  removed_rule_keys: string[];
  candidate_counts: {
    total: number;
    mandatory: number;
    critical: number;
    high: number;
    medium: number;
    low: number;
  };
  historical_sample: {
    run_count: number;
    finding_count: number;
    high_risk_finding_count: number;
    oldest_run_at?: string;
    newest_run_at?: string;
  };
  uncertainty: string[];
};

export type RuleTestRun = {
  id: string;
  rule_set_id: string;
  rule_set_name: string;
  rule_version_id: string;
  rule_version: number;
  source_run_id: string;
  snapshot_id: string;
  snapshot_sha256: string;
  state: "queued" | "running" | "completed" | "failed" | "cancelled";
  requested_by: string;
  provider: "github" | "gitlab";
  api_base_url?: string;
  repository: string;
  review_number: number;
  base_sha: string;
  head_sha: string;
  engine_version?: string;
  attempts: number;
  selected_path_count: number;
  deferred_path_count: number;
  finding_count: number;
  duration_ms: number;
  error_message?: string;
  findings: Array<{
    path: string;
    start_line: number;
    end_line: number;
    severity: string;
    category: string;
    body: string;
    suggestion?: string;
  }>;
  created_at: string;
  started_at?: string;
  finished_at?: string;
};

export type RuleBinding = {
  id: string;
  tenant_id: string;
  rule_version_id: string;
  scope_kind: "tenant" | "repository";
  scope_ref: string;
  scope_provider?: "github" | "gitlab";
  scope_api_base_url?: string;
  precedence: number;
  target_branch_glob?: string;
  path_include_glob?: string;
  path_exclude_glob?: string;
  state: "active" | "shadow" | "disabled";
  created_by: string;
  created_at: string;
  updated_at: string;
};

export type RuleRollout = {
  id: string;
  baseline_binding_id: string;
  candidate_binding_id: string;
  approved_shadow_comparison_id?: string;
  mode: "shadow" | "canary";
  state: "active" | "paused" | "promoted" | "rolled_back";
  canary_basis_points: number;
  auto_rollback_failed_runs: number;
  auto_rollback_window_minutes: number;
  auto_rollback_reason?: string;
  revision: number;
  created_by: string;
  created_at: string;
  updated_at: string;
};

export type RuleRolloutComparison = {
  id: string;
  rollout_id: string;
  baseline_run_id: string;
  candidate_test_run_id: string;
  state: "completed" | "failed";
  baseline_finding_count: number;
  candidate_finding_count: number;
  added_finding_count: number;
  removed_finding_count: number;
  matched_finding_count: number;
  error_message?: string;
  created_at: string;
  completed_at?: string;
};

export type RuleBindingData = {
  source: DataSource;
  bindings: RuleBinding[];
  rollouts: RuleRollout[];
  ruleSets: RuleSet[];
  detail?: string;
};

export type RuleException = {
  id: string;
  rule_set_id: string;
  rule_set_name: string;
  rule_version_id: string;
  rule_version: number;
  rule_key: string;
  scope_kind: "tenant" | "repository";
  scope_ref: string;
  scope_provider?: "github" | "gitlab";
  scope_api_base_url?: string;
  target_branch_glob?: string;
  reason: string;
  ticket_url?: string;
  requested_by: string;
  approved_by?: string;
  decision_comment?: string;
  state: "pending" | "approved" | "rejected" | "revoked";
  effective_state: "pending" | "approved" | "rejected" | "revoked" | "expired";
  remediation_task_id?: string;
  can_decide: boolean;
  can_revoke: boolean;
  expires_at: string;
  created_at: string;
  updated_at: string;
  source_issue_id?: string;
  source_issue_revision?: number;
};

export type RuleExceptionData = {
  source: DataSource;
  exceptions: RuleException[];
  ruleSets: RuleSet[];
  detail?: string;
};

export type UsageDashboard = {
  source: DataSource;
  period_start: string;
  period_end: string;
  entitlement: {
    monthly_review_limit: number;
    soft_warning_percent: number;
    updated_by?: string;
    updated_at: string;
  };
  settled: number;
  reserved: number;
  remaining?: number;
  warning: boolean;
  can_manage: boolean;
  reconciliation: {
    state: "clean" | "drift" | "unavailable";
    period_start: string;
    period_end: string;
    scanned_runs: number;
    drifted_runs: number;
    missing_reservations: number;
    state_mismatches: number;
    missing_ledger_proofs: number;
    last_reconciled_at?: string;
    last_reconciled_by?: string;
  };
  repositories: Array<{
    repository: string;
    settled: number;
    reserved: number;
  }>;
  ledger: Array<{
    id: string;
    run_id?: string;
    repository: string;
    metric: string;
    quantity: number;
    unit: string;
    event_kind: "reserve" | "settle" | "release" | "adjustment";
    metadata: Record<string, unknown>;
    occurred_at: string;
  }>;
  detail?: string;
};

export type FindingFeedbackMetric = {
  repository: string;
  finding_count: number;
  feedback_count: number;
  useful_finding_count: number;
  false_positive_count: number;
  resolved_finding_count: number;
  wont_fix_finding_count: number;
  high_risk_finding_count: number;
  distinct_snapshot_count: number;
};

export type FindingFeedbackDashboard = {
  source: DataSource;
  finding_count: number;
  feedback_count: number;
  useful_finding_count: number;
  false_positive_count: number;
  resolved_finding_count: number;
  wont_fix_finding_count: number;
  repositories: FindingFeedbackMetric[];
  recent_findings: Array<{
    id: string;
    run_id?: string;
    provider: "github" | "gitlab";
    api_base_url: string;
    repository: string;
    review_number: number;
    head_sha?: string;
    path: string;
    start_line: number;
    end_line: number;
    severity: string;
    category: string;
    body_preview: string;
    actor_disposition?: "resolved" | "wont_fix";
    useful_count: number;
    false_positive_count: number;
    created_at: string;
  }>;
  attribution_warning: string;
  detail?: string;
};

export type FindingExplorerPage = {
  source: DataSource;
  findings: FindingFeedbackDashboard["recent_findings"];
  next_cursor?: string;
  previous_cursor?: string;
  detail?: string;
};

export type FindingExplorerFilter = {
  view: "active" | "high-risk" | "actioned";
  repository?: string;
  query?: string;
  cursor?: string;
  direction?: "after" | "before";
};

export type ProviderInstallation = {
  id: string;
  provider: "github" | "gitlab";
  external_id: string;
  repository_scope: string;
  automatic_reviews: boolean;
  author_scope: "all" | "mine";
  minimum_severity: "low" | "medium" | "high" | "critical";
  api_base_url: string;
  active: boolean;
  verification_state: "legacy" | "pending" | "checking" | "verified" | "failed";
  verification?: {
    state: "queued" | "running" | "completed";
    health_state:
      | "live"
      | "degraded"
      | "critical"
      | "stale"
      | "configured_only";
    attempt: number;
    observed_at?: string;
    permissions: string[];
    inventory_count: number;
    inventory_state?: "synchronized" | "partial" | "invalid" | "unavailable";
    error_code?: string;
  };
};

export type ProviderProfile = {
  provider: "github" | "gitlab";
  api_base_url: string;
  mode: "cloud" | "self_managed";
  label: string;
  deployment_token_available: boolean;
};

export type ProviderProfileData = {
  source: DataSource;
  profiles: ProviderProfile[];
  detail?: string;
};

export type ProviderRepository = {
  external_id: string;
  name: string;
  default_branch?: string;
  visibility?: string;
  archived: boolean;
  last_seen_at: string;
};

export type InstallationRepositoryData = {
  source: DataSource;
  repositories: ProviderRepository[];
  detail?: string;
};

export type InstallationWebhookReceipt = {
  id: string;
  event_name: string;
  received_at: string;
  resource_kind: "pull_request" | "issue";
  action?: string;
  revision?: number;
  job_id: string;
  repository: string;
  review_number: number;
  job_state: "queued" | "running" | "succeeded" | "failed" | "cancelled";
  run_id?: string;
  run_state?: ReviewRun["state"];
  trigger_kind?: string;
};

export type InstallationWebhookReceiptData = {
  source: DataSource;
  receipts: InstallationWebhookReceipt[];
  detail?: string;
};

export type NotificationDestination = {
  id: string;
  tenant_id: string;
  name: string;
  provider: "dingtalk" | "feishu" | "slack" | "webhook";
  enabled: boolean;
  revision: number;
  created_at: string;
  updated_at: string;
};

export type NotificationRoute = {
  id: string;
  tenant_id: string;
  destination_id: string;
  repository_glob: string;
  branch_glob: string;
  event_types: string[];
  min_severity: "low" | "medium" | "high" | "critical";
  enabled: boolean;
  priority: number;
  revision: number;
  created_at: string;
  updated_at: string;
};

export type NotificationRoutePreviewMatch = {
  route_id: string;
  destination_id: string;
  destination_name: string;
  disposition: "selected" | "filtered" | "deduplicated";
  reason: string;
};

export type NotificationRoutePreview = {
  input: {
    repository: string;
    target_branch: string;
    event_type: string;
    highest_severity: string;
    finding_count: number;
  };
  matches: NotificationRoutePreviewMatch[];
};

export type NotificationDelivery = {
  id: string;
  event_id: string;
  event_type: string;
  run_id: string;
  destination_id: string;
  destination_name: string;
  state: "pending" | "sending" | "delivered" | "failed";
  attempt: number;
  response_code?: number;
  last_error?: string;
  delivered_at?: string;
  created_at: string;
  updated_at: string;
};

export type NotificationData = {
  source: DataSource;
  destinations: NotificationDestination[];
  routes: NotificationRoute[];
  deliveries: NotificationDelivery[];
  detail?: string;
};

export type AuditEvent = {
  id: string;
  actor_subject: string;
  action: string;
  target: string;
  metadata: Record<string, unknown>;
  created_at: string;
};

export type AuditData = {
  source: DataSource;
  events: AuditEvent[];
  nextCursor?: string;
  detail?: string;
};

export type AuditDetailData = {
  source: DataSource;
  event?: AuditEvent;
  detail?: string;
};

export type WorkspaceMember = {
  tenant_id: string;
  subject: string;
  role:
    | "owner"
    | "admin"
    | "rule_admin"
    | "reviewer"
    | "viewer"
    | "billing_viewer";
  active: boolean;
  deactivated_at?: string;
  deactivated_by?: string;
};

export type WorkspaceInvitation = {
  id: string;
  tenant_id: string;
  subject: string;
  role: WorkspaceMember["role"];
  status: "pending" | "accepted" | "revoked" | "expired";
  created_by: string;
  created_at: string;
  expires_at: string;
  accepted_at?: string;
  accepted_by?: string;
  revoked_at?: string;
  revoked_by?: string;
};

export type WorkspaceAccessRequest = {
  id: string;
  tenant_id: string;
  subject: string;
  note: string;
  status: "pending" | "approved" | "rejected";
  revision: number;
  created_at: string;
  decided_at?: string;
  decided_by?: string;
};

export type MemberData = {
  source: DataSource;
  members: WorkspaceMember[];
  invitations: WorkspaceInvitation[];
  accessRequests: WorkspaceAccessRequest[];
  actorSubject?: string;
  detail?: string;
};

export type WorkspaceAPIKey = {
  id: string;
  name: string;
  prefix: string;
  caller_subject: string;
  scopes: Array<"reviews:read" | "reviews:create" | "runs:cancel">;
  repositories: string[];
  created_by: string;
  created_at: string;
  expires_at?: string;
  last_used_at?: string;
  revoked_at?: string;
  revoked_by?: string;
};

export type APIKeyData = {
  source: DataSource;
  apiKeys: WorkspaceAPIKey[];
  detail?: string;
};

export type SSOConfiguration = {
  tenant_id: string;
  protocol: "oidc" | "saml";
  display_name: string;
  issuer_url?: string;
  metadata_url?: string;
  client_id?: string;
  secret_configured: boolean;
  group_claim: string;
  state:
    | "draft_saved"
    | "testing"
    | "verified"
    | "enforcement_ready"
    | "enforced"
    | "suspended";
  revision: number;
  tested_revision?: number;
  break_glass_subject?: string;
  enforced_at?: string;
  updated_by: string;
  created_at: string;
  updated_at: string;
};

export type SSODomain = {
  id: string;
  domain: string;
  challenge_token: string;
  state: "pending" | "verified";
  revision: number;
  created_by: string;
  created_at: string;
  verified_at?: string;
};

export type SSORoleMapping = {
  id: string;
  group_value: string;
  role: "admin" | "rule_admin" | "reviewer" | "viewer" | "billing_viewer";
  repository_scope: string;
  revision: number;
  created_by: string;
  created_at: string;
  updated_at: string;
};

export type SSOProbeReceipt = {
  id: string;
  config_revision: number;
  protocol: "oidc" | "saml";
  target_url: string;
  state: "queued" | "running" | "succeeded" | "failed";
  attempt: number;
  metadata_sha256?: string;
  error_code?: string;
  error_message?: string;
  requested_by: string;
  created_at: string;
  started_at?: string;
  finished_at?: string;
};

export type SSOOverview = {
  configuration?: SSOConfiguration;
  domains: SSODomain[];
  mappings: SSORoleMapping[];
  probes: SSOProbeReceipt[];
  readiness: { ready: boolean; blockers: string[] };
};

export type SSOData = SSOOverview & {
  source: DataSource;
  detail?: string;
};

export type DataClass =
  | "raw_webhook"
  | "findings"
  | "audit"
  | "usage"
  | "operational_logs";

export type DataScopeKind =
  | "tenant"
  | "repository"
  | "review_run"
  | "audit_range";

export type DataResidency = {
  configured: boolean;
  primary_region?: string;
  backup_region?: string;
  object_region?: string;
  queue_region?: string;
  model_boundary: string;
  revision?: number;
  effective_at?: string;
  observed_at?: string;
  updated_by?: string;
  updated_at?: string;
};

// `configured` is an admission-time shorthand, not a durable execution mode.
// Older records can still omit or contain that legacy value, so callers must
// render the absence honestly rather than implying an unresolved live choice.
export function displayReviewMode(mode?: ReviewRun["review_mode"]): string {
  return mode && mode !== "configured" ? mode : "Not retained (legacy)";
}

export type DataBoundary = {
  name: string;
  value: string;
  observed: boolean;
  detail: string;
  external: boolean;
};

export type RetentionPolicy = {
  id: string;
  scope_kind: "tenant" | "repository";
  scope_ref?: string;
  data_class: DataClass;
  retention_days: number;
  state: "awaiting_approval" | "active" | "rejected" | "superseded";
  revision: number;
  impact_records: number;
  impact_legal_hold: boolean;
  change_reason: string;
  requested_by: string;
  decided_by?: string;
  decided_at?: string;
  created_at: string;
  updated_at: string;
};

export type DataLegalHold = {
  id: string;
  scope_kind: "tenant" | "repository";
  scope_ref?: string;
  data_class: DataClass | "all";
  reason: string;
  state: "active" | "released";
  revision: number;
  created_by: string;
  created_at: string;
  released_by?: string;
  released_at?: string;
};

export type DataGovernanceJob = {
  id: string;
  parent_job_id?: string;
  kind: "export" | "deletion" | "region_migration";
  scope_kind: DataScopeKind;
  scope_ref?: string;
  data_classes: DataClass[];
  desired_region?: string;
  external_operation_id?: string;
  state:
    | "requested"
    | "awaiting_approval"
    | "queued"
    | "running"
    | "completed"
    | "failed"
    | "cancelled"
    | "rejected";
  progress: number;
  revision: number;
  idempotency_key: string;
  reason: string;
  requested_by: string;
  approved_by?: string;
  approved_at?: string;
  reversible_until?: string;
  artifact_ready: boolean;
  receipt: Record<string, unknown>;
  error_code?: string;
  error_message?: string;
  created_at: string;
  updated_at: string;
  started_at?: string;
  finished_at?: string;
  next_attempt_at?: string;
};

export type DataGovernanceOverview = {
  residency: DataResidency;
  boundaries: DataBoundary[];
  policies: RetentionPolicy[];
  legal_holds: DataLegalHold[];
  jobs: DataGovernanceJob[];
};

export type DataGovernanceData = DataGovernanceOverview & {
  source: DataSource;
  detail?: string;
};

export type HealthState =
  | "live"
  | "degraded"
  | "critical"
  | "stale"
  | "configured_only";

export type HealthComponent = {
  key: string;
  name: string;
  state: HealthState;
  detail: string;
  observed: boolean;
  sampled_at?: string;
  capability: string;
  recovery_hint: string;
};

export type QueueHealth = {
  key: string;
  name: string;
  source: string;
  state: HealthState;
  ready: number;
  running: number;
  failed: number;
  expired_leases: number;
  oldest_age_seconds: number;
  sampled_at: string;
  detail: string;
};

export type GitLabAuthorAdmissionHealthItem = {
  delivery_id: string;
  installation_id: string;
  repository: string;
  review_number: number;
  api_base_url: string;
  head_sha: string;
  state: "queued" | "running" | "failed";
  attempt: number;
  error_code: string;
  updated_at: string;
};

export type WorkerHealth = {
  worker_id: string;
  kind: string;
  state: HealthState;
  active_leases: number;
  expired_leases: number;
  last_activity?: string;
  last_heartbeat?: string;
  version?: string;
  capacity: number;
  busy: number;
  detail: string;
};

export type ProviderHealth = {
  installation_id: string;
  provider: "github" | "gitlab";
  host: string;
  repository_scope: string;
  active: boolean;
  state: HealthState;
  last_webhook_at?: string;
  last_publish_at?: string;
  publish_failures: number;
  last_probe_at?: string;
  permissions: string[];
  rate_limit_remaining?: number;
  rate_limit_limit?: number;
  rate_limit_reset_at?: string;
  probe_latency_ms: number;
  detail: string;
};

export type PlatformIncident = {
  id: string;
  title: string;
  state: "active" | "mitigating" | "resolved";
  scope: string;
  started_at: string;
  resolved_at?: string;
  resolved_by?: string;
  resolution?: string;
  affected_area: string;
  created_by?: string;
  revision: number;
  updated_at: string;
};

export type PlatformRunbook = {
  key: string;
  title: string;
  version: string;
  required_role: string;
  steps: string[];
  executable: boolean;
  detail: string;
};

export type PlatformHealthOverview = {
  sampled_at: string;
  freshness_window_seconds: number;
  components: HealthComponent[];
  queues: QueueHealth[];
  gitlab_author_admissions: GitLabAuthorAdmissionHealthItem[];
  workers: WorkerHealth[];
  agent_executor?: {
    state: "unobserved" | "not_configured" | "configured_unverified" | "unreachable" | "reachable_unverified" | "partially_reachable";
    detail: string;
  };
  agent_decision?: {
    state: "unobserved" | "not_configured" | "configured_unverified";
    detail: string;
  };
  agent_credential_broker?: {
    state: "unobserved" | "configured_unverified";
    detail: string;
  };
  providers: ProviderHealth[];
  incidents: PlatformIncident[];
  incident_tracking_available: boolean;
  worker_heartbeat_available: boolean;
  broker_metrics_available: boolean;
  runbooks: PlatformRunbook[];
};

export type PlatformHealthData = PlatformHealthOverview & {
  source: DataSource;
  detail?: string;
};

export type RuleSnapshot = {
  id: string;
  sha256: string;
  compiler_version: string;
  engine: string;
  sources: Array<{
    rule_version_id: string;
    rule_set_id: string;
    version: number;
    precedence: number;
  }>;
  exceptions: Array<{
    exception_id: string;
    rule_version_id: string;
    rule_key: string;
    expires_at: string;
  }>;
  created_at: string;
};

export type RunEvent = {
  id: string;
  run_id: string;
  revision: number;
  event_type: string;
  actor_kind: string;
  actor_subject?: string;
  payload: Record<string, unknown>;
  created_at: string;
};

export type ReviewFindingEvidence = {
  id: string;
  path: string;
  start_line: number;
  end_line: number;
  severity: "low" | "medium" | "high" | "critical";
  category: string;
  body: string;
  suggestion?: string;
  code_excerpt?: string;
  code_excerpt_start_line?: number;
  proposed_patch?: string;
  fingerprint: string;
  provider_marker?: string;
  disposition?: "resolved" | "wont_fix";
  useful_feedback_count: number;
  false_positive_feedback_count: number;
  rule_attributions: Array<{
    rule_key: string;
    rule_version_id: string;
    rule_set_id: string;
    rule_set_name: string;
    version: number;
  }>;
  created_at: string;
};

export type ReviewRunStageEvidence = {
  id: string;
  stage: "ack" | "admit" | "prepare" | "analyze" | "normalize" | "publish";
  state: "pending" | "running" | "succeeded" | "failed" | "skipped";
  attempt: number;
  started_at?: string;
  finished_at?: string;
  details: Record<string, unknown>;
};

export type PublicationReceiptEvidence = {
  id: string;
  provider: ReviewRun["provider"];
  receipt_kind: "ack" | "summary" | "inline_finding" | "status";
  stable_marker: string;
  external_id?: string;
  payload_hash: string;
  published_at?: string;
  last_error?: string;
  created_at: string;
  updated_at: string;
};

export type ReviewEvidence = {
  run: ReviewRun;
  acknowledgement_recovery_available: boolean;
  related_runs: ReviewRun[];
  execution_attempts: number;
  execution_plan?: {
    mode: "standard" | "focused" | "critical";
    selected_paths: string[];
    deferred_files: number;
    /**
     * Deterministic, path-level reasons that raised this plan's priority.
     * This is deliberately not a runtime dependency or call-graph claim.
     */
    static_impact_signals?: string[];
    /** Source-free, immutable per-path admission decisions for new review runs. */
    file_scopes?: {
      path: string;
      selected: boolean;
      score: number;
      reasons: string[];
      change_type?: "added" | "modified" | "deleted" | "renamed" | "copied" | "type_changed";
      previous_path?: string;
      additions?: number;
      deletions?: number;
      stats_known?: boolean;
      binary?: boolean;
    }[];
  };
  merge_gate?: {
    enabled: boolean;
    threshold: "off" | "critical" | "high" | "medium" | "low";
    conclusion: "success" | "failure";
    blocking_findings: number;
    finding_count: number;
    configuration_content_sha256: string;
    origin_scope_kind: "default" | "tenant" | "repository";
    origin_scope_ref?: string;
    origin_revision: number;
    evaluation_version: string;
    decided_at: string;
  };
  provider_checks?: {
    provider: ReviewRun["provider"];
    repository: string;
    head_sha: string;
    state: "queued" | "running" | "observed" | "failed";
    observed_at?: string;
    checks: Array<{ kind: "check_run" | "commit_status" | "pipeline"; name: string; state: string; url?: string; origin?: "independent" | "open_review" | "unclassified" }>;
    truncated: boolean;
    stale: boolean;
    error_code?: string;
  };
  findings: ReviewFindingEvidence[];
  stages: ReviewRunStageEvidence[];
  receipts: PublicationReceiptEvidence[];
  configuration_snapshot: Array<{
    section: ReviewConfigSection;
    origin_scope_kind: "default" | "tenant" | "repository";
    origin_scope_ref?: string;
    origin_revision: number;
    content_sha256: string;
    content: Record<string, unknown>;
    created_at: string;
  }>;
  events: RunEvent[];
};

export type DataSource = "live" | "demo" | "unconfigured" | "unavailable";

export type WorkspaceApprovalPolicy = {
  allow_agent_plan_self_approval: boolean;
  allow_rule_self_approval: boolean;
  revision: number;
  updated_by: string;
  updated_at?: string;
  can_update: boolean;
};

export async function getWorkspaceApprovalPolicyData(org: string): Promise<{ source: DataSource; policy?: WorkspaceApprovalPolicy; detail?: string }> {
  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration || process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") return { source: "unconfigured", detail: "Sign in to read workspace approval settings." };
  try {
    return { source: "live", policy: await request<WorkspaceApprovalPolicy>(configuration, `/v1/tenants/${encodeURIComponent(org)}/approval-policy`) };
  } catch (error) {
    return { source: "unavailable", detail: error instanceof Error ? error.message : "Could not read approval settings." };
  }
}

export type IssueStatus = "open" | "regressed" | "resolved" | "suppressed";

export type ReviewIssue = {
  id: string;
  revision: number;
  provider: "github" | "gitlab";
  api_base_url?: string;
  repository: string;
  fingerprint: string;
  path: string;
  severity: "low" | "medium" | "high" | "critical";
  category: string;
  body_preview: string;
  status: IssueStatus;
  assignee_subject?: string;
  disposition_kind?: string;
  disposition_reason?: string;
  occurrence_count: number;
  active_occurrence_count: number;
  pull_request_count: number;
  first_seen_at: string;
  last_seen_at: string;
  resolved_at?: string;
  updated_at: string;
};

export type IssueOccurrence = {
  id: string;
  finding_id: string;
  request_id?: string;
  run_id?: string;
  review_number: number;
  head_sha: string;
  path: string;
  start_line: number;
  end_line: number;
  severity: ReviewIssue["severity"];
  category: string;
  body: string;
  suggestion?: string;
  rule_attributions?: Array<{ rule_key: string; rule_version_id: string; rule_set_id: string; rule_set_name: string; version: number }>;
  active: boolean;
  created_at: string;
};

export type IssueDetail = ReviewIssue & {
  occurrences: IssueOccurrence[];
  events: Array<{
    id: string;
    revision: number;
    actor_subject: string;
    action:
      | "assigned"
      | "unassigned"
      | "resolved"
      | "reopened"
      | "false_positive"
      | "suppression_cleared"
      | "exception_approved"
      | "exception_revoked"
      | "exception_expired";
    previous_status: IssueStatus;
    next_status: IssueStatus;
    previous_assignee?: string;
    next_assignee?: string;
    reason?: string;
    created_at: string;
  }>;
  exception_request?: {
    id: string;
    rule_key: string;
    state: "pending" | "approved" | "rejected" | "revoked";
    effective_state:
      | "pending"
      | "approved"
      | "rejected"
      | "revoked"
      | "expired";
    requested_by: string;
    expires_at: string;
  };
  external_issue?: {
    id: string;
    issue_id: string;
    provider: "github" | "gitlab";
    repository: string;
    trigger: "first_seen" | "regressed" | "repeated";
    state: "queued" | "created" | "failed" | "cancelled";
    external_id?: string;
    external_url?: string;
    attempts: number;
    last_error?: string;
    created_at: string;
    updated_at: string;
  };
  can_manage: boolean;
};

export type IssueInboxData = {
  source: DataSource;
  issues: ReviewIssue[];
  facets?: { repositories: string[]; categories: string[] };
  counts: {
    open: number;
    regressed: number;
    critical: number;
    assigned: number;
    resolved: number;
    suppressed: number;
  };
  nextCursor?: string;
  previousCursor?: string;
  filterTime?: string;
  totalCount?: number;
  selectedInView?: boolean;
  detail?: string;
};

export type IssueViewsData = {
  source: DataSource;
  views: IssueSavedView[];
  canCreateWorkspace: boolean;
  detail?: string;
};

export type IssueDetailData = {
  source: DataSource;
  issue?: IssueDetail;
  detail?: string;
};

export type ProviderIssueAnalysisState =
  | "queued"
  | "acknowledged"
  | "completed"
  | "failed";

export type ProviderIssueAnalysisFilterState = ProviderIssueAnalysisState | "needs_attention";

export type ProviderIssueAnalysis = {
  id: string;
  installation_id: string;
  provider: "github" | "gitlab";
  api_base_url: string;
  repository: string;
  issue_number: number;
  revision: number;
  analysis_attempt: number;
  action: string;
  title: string;
  author: string;
  labels: string[];
  state: ProviderIssueAnalysisState;
  last_error?: string;
  delivery_failure: boolean;
  skipped_delivery: boolean;
  connection_blocked: boolean;
  receipt_count: number;
  created_at: string;
  updated_at: string;
  completed_at?: string;
  model_route_sha256: string;
  prompt_config_sha256: string;
  issue_triage_config_sha256: string;
};

export type ProviderIssueAnalysisDetail = ProviderIssueAnalysis & {
  analysis?: string;
  retry_readiness: {
    can_retry: boolean;
    installation_ready: boolean;
    setup_ready: boolean;
    role_allowed: boolean;
  };
  agent_admission?: {
    policy_mode: "disabled" | "suggest" | "manual";
    installation_ready: boolean;
    can_request: boolean;
    existing_task_conflict?: boolean;
    existing_task_id?: string;
    existing_task_state?: string;
    existing_tasks?: { id: string; state: string }[];
  };
  useful_count: number;
  not_useful_count: number;
  feedback_sync?: {
    state: "queued" | "running" | "completed";
    attempt: number;
    observed_at?: string;
    reaction_count: number;
    last_error?: string;
    next_poll_at: string;
  };
  receipts: Array<{
    revision: number;
    action: string;
    event_name: string;
    admitted_at: string;
  }>;
};

export type ProviderIssueAnalysisData = {
  source: DataSource;
  items: ProviderIssueAnalysis[];
  counts: Record<ProviderIssueAnalysisState, number> & {
    delivery_failures: number;
    skipped_deliveries: number;
    connection_blocked: number;
    needs_attention: number;
  };
  selected?: ProviderIssueAnalysisDetail;
  detail?: string;
};

export type AgentWorkflowPolicy = {
 require_criterion_evidence?: boolean;
  enabled: boolean;
  max_repair_cycles: number;
  max_task_attempts: number;
  required_checks?: string[];
};
export type AgentTaskAcceptance = {
 can_retry_checks?: boolean;
 decision?: string;
 decision_reason?: string;
 recovery_reason?: string;
 verification_criteria?: {criterion:string;status:"passed"|"failed";evidence:string}[];
  remediation_task_id?: string;
  can_decide: boolean;
  evidence?: string[];
  task_id: string;
  attempt_id: string;
  head_sha: string;
  revision: number;
  state: string;
  criteria: string[];
  review_run_id?: string;
  reason?: string;
  decided_by?: string;
  decided_at?: string;
  updated_at: string;
};
export type AgentTaskPolicy = {
  workflow?: AgentWorkflowPolicy;
  id: string;
  provider: "github" | "gitlab";
  api_base_url: string;
  repository: string;
  mode: "disabled" | "suggest" | "manual";
  max_attempts: number;
  max_execution_seconds: number;
  max_feedback_cycles: number;
  executor_profile: "codex" | "claude";
  decision_backend: "jev" | "deterministic";
  auto_admission_enabled: boolean;
  auto_admission_label: string;
  revision: number;
  updated_by: string;
  updated_at: string;
};

export type AgentTask = {
  workflow?: AgentWorkflowPolicy;
  id: string;
  installation_id: string;
  provider: "github" | "gitlab";
  api_base_url: string;
  repository: string;
  origin_kind: "issue" | "pull_request" | "campaign";
  origin_number: number;
  origin_revision: string;
  intent: "implement";
  policy_revision: number;
  max_attempts: number;
  max_execution_seconds: number;
  max_feedback_cycles: number;
  executor_profile: "codex" | "claude";
  decision_backend: "jev" | "deterministic";
  feedback_cycle: number;
  execution_branch: string;
  parent_task_id?: string;
  parent_attempt_id?: string;
  source_state: "pending" | "ready" | "failed";
  source_base_ref?: string;
  source_base_sha?: string;
  source_captured_at?: string;
  state:
    | "received"
    | "plan_ready"
    | "awaiting_approval"
    | "execution_queued"
    | "executing"
    | "needs_attention"
    | "completed"
    | "failed"
    | "cancelled"
    | "rejected"
    | "superseded";
  revision: number;
  requested_by: string;
  created_at: string;
  updated_at: string;
};

export type AgentTaskClassification = {
  id: string;
  task_id: string;
  task_revision: number;
  source_revision: string;
  decision: "needs_context" | "requires_human" | "rejected";
  risk_level: "unknown" | "low" | "medium" | "high" | "critical";
  confidence: number;
  reasons: string[];
  evaluation: Array<{
    stage: "judge" | "evaluate" | "verify" | "model";
    outcome: "passed" | "blocked" | "requires_human" | "not_evaluated" | "advisory";
    summary: string;
    signals: string[];
  }>;
  next_action:
    | "reject"
    | "request_context"
    | "capture_source"
    | "await_plan_approval";
  snapshot_sha256: string;
  classifier_version: string;
  created_at: string;
};

export type AgentTaskPlan = {
  id: string;
  task_id: string;
  revision: number;
  state: "awaiting_approval" | "approved" | "rejected" | "superseded";
  summary: string;
  sections: AgentTaskPlanSections;
  plan_sha256: string;
  created_by: string;
  approved_by?: string;
  approved_at?: string;
  created_at: string;
};

export type AgentTaskPlanSections = {
 source_requirements?: string;
 repository_evidence?: string;
  acceptance_criteria?: string[];
  objective: string;
  scope: string;
  verification: string;
  risks: string;
  unknowns: string;
};

export type AgentTaskAttempt = {
  id: string;
  task_id: string;
  plan_id: string;
  task_revision: number;
  plan_revision: number;
  attempt: number;
  state:
    | "queued"
    | "running"
    | "succeeded"
    | "needs_attention"
    | "failed"
    | "superseded";
  worker_id?: string;
  adapter_job_id?: string;
  locked_until?: string;
  deadline_at?: string;
  error_code?: string;
  error_message?: string;
  result_summary?: string;
  branch_name?: string;
  head_sha?: string;
  pull_request_url?: string;
  pull_request_number?: number;
  patch_sha256?: string;
  changed_file_count?: number;
  diff_bytes?: number;
  verification_profile_sha256?: string;
  verification_output_sha256?: string;
  verification_output_bytes?: number;
  created_at: string;
  started_at?: string;
  finished_at?: string;
  updated_at: string;
};

export type AgentTaskPublicationCheckpoint = {
  attempt_id: string;
  attempt_number: number;
  adapter_job_id: string;
  branch_name: string;
  head_sha: string;
  patch_sha256: string;
  changed_file_count: number;
  diff_bytes: number;
  verification_profile_sha256?: string;
  verification_output_sha256?: string;
  verification_output_bytes?: number;
  recorded_at: string;
};

export type AgentTaskDetail = {
	execution_block?: { code: string; message: string; recorded_at: string };
	execution_budget?: { used: number; limit: number; successful_deliveries: number; attention_attempts: number };
  acceptance?: AgentTaskAcceptance;
  task: AgentTask;
  target_branch?: string;
  plans: AgentTaskPlan[];
  classifications: AgentTaskClassification[];
  attempts: AgentTaskAttempt[];
  publication_checkpoints?: AgentTaskPublicationCheckpoint[];
  linked_reviews: {
    run_id: string;
    state: string;
    head_sha: string;
    review_number: number;
    created_at: string;
    merge_gate?: {
      enabled: boolean;
      threshold: string;
      conclusion: "success" | "failure";
      blocking_findings: number;
      finding_count: number;
    };
  }[];
  feedback?: {
    internal_repair_kind?: string;
    source_review_run_id?: string;
    comment_external_id: string;
    actor_external_id: string;
  };
  plan_permissions?: {
    can_create_plan: boolean;
    can_approve_plan: boolean;
    create_block_reason?: string;
    approve_block_reason?: string;
  };
};

export type AgentTaskData = {
  source: DataSource | "partial";
  installations: ProviderInstallation[];
  policies: AgentTaskPolicy[];
  tasks: AgentTask[];
  taskCursor?: string;
  nextTaskCursor?: string;
  selected?: AgentTaskDetail;
  selectedTaskRequested: boolean;
  availability: {
    installations: boolean;
    policies: boolean;
    tasks: boolean;
    selected: boolean;
  };
  detail?: string;
};

export type IssueAutoCreatePolicy = {
  revision: number;
  enabled: boolean;
  target: "provider";
  repository_scopes: string[];
  minimum_severity: ReviewIssue["severity"];
  categories: string[];
  trigger_first_seen: boolean;
  trigger_regressed: boolean;
  repeat_occurrence_threshold: number;
  labels: string[];
  assignee_external_id?: string;
  title_template: string;
  body_template: string;
  updated_by?: string;
  updated_at?: string;
  can_manage: boolean;
};

export type IssueAutoCreatePreview = {
  window_start: string;
  window_end: string;
  candidates: Array<{
    issue_id: string;
    repository: string;
    path: string;
    severity: ReviewIssue["severity"];
    category: string;
    occurrence_count: number;
    status: IssueStatus;
    already_published: boolean;
  }>;
};

const demoIssueAutoCreatePolicy: IssueAutoCreatePolicy = {
  revision: 3,
  enabled: true,
  target: "provider",
  repository_scopes: ["RainLib/*", "platform/*"],
  minimum_severity: "high",
  categories: ["security", "reliability"],
  trigger_first_seen: true,
  trigger_regressed: true,
  repeat_occurrence_threshold: 3,
  labels: ["open-review", "needs-triage"],
  assignee_external_id: "platform-triage",
  title_template: "[Open Review] {{severity_upper}} {{category}} · {{path}}",
  body_template:
    "## 🔎 Finding summary\n\n> **{{severity_upper}} · {{category}}** — action is required.\n\n| Context | Evidence |\n| --- | --- |\n| **Repository** | {{repository}} |\n| **Location** | {{file_link}} |\n| **Pull request** | {{review_link}} |\n\n### Why this matters\n\n{{evidence}}\n\n### Recommended fix\n\n{{suggestion}}",
  updated_by: "platform@acme.example",
  updated_at: "2026-09-19T08:30:00Z",
  can_manage: false,
};

const demoIssues: ReviewIssue[] = [
  {
    id: "issue-demo-auth-boundary",
    revision: 7,
    provider: "github",
    repository: "RainLib/open-review-platform",
    fingerprint: "auth-boundary-missing-audience",
    path: "internal/api/auth.go",
    severity: "critical",
    category: "Security",
    body_preview: "Token audience is not verified before a privileged action.",
    status: "open",
    assignee_subject: "demo-security-owner",
    occurrence_count: 5,
    active_occurrence_count: 2,
    pull_request_count: 3,
    first_seen_at: "2026-09-16T08:20:00Z",
    last_seen_at: "2026-09-19T10:42:00Z",
    updated_at: "2026-09-19T10:42:00Z",
  },
  {
    id: "issue-demo-retry-contract",
    revision: 3,
    provider: "gitlab",
    api_base_url: "https://gitlab.example.internal",
    repository: "platform/review-worker",
    fingerprint: "retry-contract-loses-receipt",
    path: "workers/publish.ts",
    severity: "high",
    category: "Reliability",
    body_preview:
      "A retry can publish a second provider comment after a timeout.",
    status: "regressed",
    occurrence_count: 4,
    active_occurrence_count: 1,
    pull_request_count: 2,
    first_seen_at: "2026-09-11T14:10:00Z",
    last_seen_at: "2026-09-18T16:25:00Z",
    updated_at: "2026-09-18T16:25:00Z",
  },
  {
    id: "issue-demo-tenant-lease",
    revision: 4,
    provider: "github",
    repository: "RainLib/open-review-platform",
    fingerprint: "tenant-lease-observation-scope",
    path: "internal/store/platform_health.go",
    severity: "medium",
    category: "Maintainability",
    body_preview:
      "A shared worker observation could be rendered outside its tenant scope.",
    status: "open",
    assignee_subject: "demo-platform-owner",
    occurrence_count: 2,
    active_occurrence_count: 1,
    pull_request_count: 2,
    first_seen_at: "2026-09-15T09:05:00Z",
    last_seen_at: "2026-09-19T07:18:00Z",
    updated_at: "2026-09-19T07:18:00Z",
  },
  {
    id: "issue-demo-log-redaction",
    revision: 2,
    provider: "gitlab",
    api_base_url: "https://gitlab.example.internal",
    repository: "platform/notification-gateway",
    fingerprint: "webhook-log-redaction",
    path: "src/delivery/redaction.ts",
    severity: "low",
    category: "Privacy",
    body_preview:
      "Delivery diagnostics may retain an unredacted provider response.",
    status: "suppressed",
    disposition_kind: "accepted_risk",
    disposition_reason: "Tracked by the provider migration programme.",
    occurrence_count: 1,
    active_occurrence_count: 0,
    pull_request_count: 1,
    first_seen_at: "2026-08-28T11:30:00Z",
    last_seen_at: "2026-09-02T12:15:00Z",
    updated_at: "2026-09-02T12:15:00Z",
  },
];

export type IssueAutoCreatePolicyData = {
  source: DataSource;
  policy?: IssueAutoCreatePolicy;
  detail?: string;
};

export type ConsoleData = {
  source: DataSource;
  runs: ReviewRun[];
  ruleSets: RuleSet[];
  installations: ProviderInstallation[];
  detail?: string;
};

export type WorkQueueView = "running" | "needs_attention";

export type WorkQueueData = {
  source: DataSource;
  runs: ReviewRun[];
  counts: {
    running: number;
    needs_attention: number;
  };
  nextCursor?: string;
  previousCursor?: string;
  detail?: string;
};

export type PullRequestView = "active" | "attention" | "completed" | "all";

export type PullRequestData = {
  source: DataSource;
  runs: ReviewRun[];
  counts: {
    active: number;
    attention: number;
    completed: number;
    all: number;
  };
  nextCursor?: string;
  previousCursor?: string;
  detail?: string;
};

export type CLIReviewData = {
  source: DataSource;
  runs: CLIReviewRun[];
  installations: ProviderInstallation[];
  detail?: string;
};

export type WorkspaceInitialization = {
  status:
    | "ready"
    | "access_denied"
    | "needs_connection"
    | "verifying_connection"
    | "connection_failed"
    | "needs_setup"
    | "unavailable";
  detail?: string;
  needsSignIn?: boolean;
  connection?: Pick<
    ProviderInstallation,
    | "id"
    | "active"
    | "provider"
    | "verification_state"
    | "verification"
    | "repository_scope"
    | "automatic_reviews"
    | "author_scope"
    | "minimum_severity"
  >;
};

export type WorkspaceSetupCheckpoint = {
  current_step:
    | "connect"
    | "review_scope"
    | "learning"
    | "severity"
    | "rules"
    | "complete";
  revision: number;
  learning_boundary?: {
    mode: "governed_policy" | "skipped";
    reviewer_exclusions: string[];
  };
  readiness?: {
    checkpoint_revision: number;
    installation_id: string;
    provider: "github" | "gitlab";
    repository_scope: string;
    general_config_revision: number;
    general_config_content_sha256: string;
    learning_mode?: "governed_policy" | "skipped";
    rule_set_count: number;
    created_at: string;
  };
  updated_by?: string;
  updated_at?: string;
  completed_at?: string;
};

export type AccessibleWorkspace = {
  slug: string;
  name: string;
  role: string;
};

export type AccessibleWorkspaceState = AccessibleWorkspace & {
  initialization: WorkspaceInitialization;
};

export type WorkspaceDirectory = {
  source: DataSource;
  workspaces: AccessibleWorkspaceState[];
  detail?: string;
  needsSignIn?: boolean;
};

export type RunDetailData = ConsoleData & {
  run?: ReviewRun;
  ruleSnapshot?: RuleSnapshot;
};

export type ReviewEvidenceData = {
  source: DataSource;
  evidence?: ReviewEvidence;
  ruleSnapshot?: RuleSnapshot;
  detail?: string;
};

export type ReviewConfigSection =
  | "general"
  | "categories"
  | "filters"
  | "prompts"
  | "summary"
  | "messages"
  | "models"
  | "issue-triage";

export type ReviewConfigView = {
  section: ReviewConfigSection;
  requested_scope_kind: "tenant" | "repository";
  requested_scope_ref?: string;
  requested_scope_provider?: "github" | "gitlab";
  requested_scope_api_base_url?: string;
  origin_scope_kind: "default" | "tenant" | "repository";
  origin_scope_ref?: string;
  origin_scope_provider?: "github" | "gitlab";
  origin_scope_api_base_url?: string;
  inherited: boolean;
  revision: number;
  content_sha256: string;
  content: Record<string, unknown>;
  updated_by?: string;
  updated_at?: string;
};

export type ReviewConfigVersion = {
  revision: number;
  content_sha256: string;
  created_by: string;
  created_at: string;
};

export type ReviewConfigHistory = {
  requested_scope_kind: "tenant" | "repository";
  requested_scope_ref?: string;
  requested_scope_provider?: "github" | "gitlab";
  requested_scope_api_base_url?: string;
  origin_scope_kind: "default" | "tenant" | "repository";
  origin_scope_ref?: string;
  origin_scope_provider?: "github" | "gitlab";
  origin_scope_api_base_url?: string;
  inherited: boolean;
  versions: ReviewConfigVersion[];
};

export type ReviewConfigData = {
  source: DataSource;
  config?: ReviewConfigView;
  detail?: string;
};

export type IssueFormatTemplate = {
  id: string;
  name: string;
  description: string;
  revision: number;
  content: Record<string, unknown>;
  content_sha256: string;
  created_by: string;
  created_at: string;
  updated_by: string;
  updated_at: string;
};

export type IssueFormatTemplateData = {
  source: DataSource;
  templates: IssueFormatTemplate[];
  detail?: string;
};

export type ReviewConfigChangeRequest = {
  id: string;
  section: ReviewConfigSection;
  scope_kind: "tenant" | "repository";
  scope_ref?: string;
  scope_provider?: "github" | "gitlab";
  scope_api_base_url?: string;
  base_revision: number;
  base_content_sha256: string;
  proposed_content: Record<string, unknown>;
  proposed_content_sha256: string;
  requested_by: string;
  reason: string;
  operation: "upsert" | "restore_inheritance";
  state: "pending" | "approved" | "rejected" | "superseded";
  approval_count: number;
  rejection_count: number;
  actor_decision?: string;
  remediation_task_id?: string;
  can_decide: boolean;
  applied_revision?: number;
  created_at: string;
  decided_at?: string;
};

export async function getReviewConfigChangeRequests(
  org: string,
): Promise<{
  source: DataSource;
  requests: ReviewConfigChangeRequest[];
  detail?: string;
}> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true")
    return { source: "demo", requests: [] };
  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration)
    return {
      source: "unconfigured",
      requests: [],
      detail:
        "Sign in and configure the control plane before viewing model-route approvals.",
    };
  try {
    const result = await request<{ requests: ReviewConfigChangeRequest[] }>(
      configuration,
      `/v1/tenants/${encodeURIComponent(org)}/review-config-change-requests?limit=50`,
    );
    return { source: "live", requests: result.requests ?? [] };
  } catch (error) {
    return {
      source: "unavailable",
      requests: [],
      detail:
        error instanceof Error
          ? error.message
          : "Model-route approvals could not be loaded.",
    };
  }
}

const demoReviewConfigContent: Record<
  ReviewConfigSection,
  Record<string, unknown>
> = {
  general: {
    automatic_review: true,
    review_drafts: false,
    rereview_on_push: true,
    merge_gate_enabled: true,
    review_language: "en",
    minimum_blocking_severity: "high",
  },
  categories: {
    bug: { enabled: true, minimum_severity: "medium" },
    security: { enabled: true, minimum_severity: "medium" },
    performance: { enabled: true, minimum_severity: "high" },
    maintainability: { enabled: true, minimum_severity: "medium" },
  },
  filters: {
    include_paths: ["apps/**", "internal/**"],
    exclude_paths: ["**/vendor/**", "**/generated/**"],
    exclude_authors: ["dependabot[bot]"],
    required_labels: [],
    target_branches: ["main", "release/**"],
    skip_generated: true,
    skip_vendor: true,
  },
  prompts: {
    system_instruction:
      "Focus on actionable defects supported by the changed code. State the exact file and evidence boundary.",
    repository_context:
      "Open Review is an evidence-first, multi-tenant control plane. Preserve durable receipts and immutable policy snapshots.",
    max_prompt_tokens: 12000,
    allow_repository_instructions: false,
  },
  summary: {
    sections: [
      "outcome",
      "scope",
      "risk",
      "acceptance",
      "invariants",
      "verification",
      "rollout",
      "rollback",
      "provenance",
    ],
    max_characters: 4000,
    include_change_contract: true,
    include_verification_evidence: true,
  },
  messages: {
    started: "Review started for {{repository}}#{{review_number}}.",
    progress: "Reviewing revision {{head_sha}} now.",
    success: "Review passed for revision {{head_sha}}.",
    recommendation:
      "Review completed with recommendations for revision {{head_sha}}.",
    blocked: "Merge gate blocked: review the actionable findings.",
    failed:
      "Review could not complete. Open the run detail for the safe error summary.",
    needs_attention:
      "Review requires human attention before trustworthy findings can be published.",
    superseded: "This review was superseded by a newer revision.",
  },
  models: {
    enabled: false,
    provider: "deployment",
    protocol: "deployment",
    base_url: "",
    model: "",
    credential_ref: "",
    effort: "low",
    max_prompt_tokens: 8000,
    token_budget: 128000,
    subtask_timeout_minutes: 5,
  },
  "issue-triage": {
    enabled: true,
    preset: "engineering",
    language: "inherit",
    required_issue_sections: [
      "outcome",
      "reproduction",
      "expected_behavior",
      "evidence",
      "acceptance_criteria",
    ],
    response_sections: [
      "assessment",
      "missing_context",
      "acceptance_criteria",
      "risk",
      "affected_areas",
      "next_steps",
      "provenance",
    ],
    collapse_secondary: true,
    link_file_references: true,
    reaction_feedback: true,
    max_items_per_section: 6,
    custom_guidance: "",
  },
};

function demoReviewConfigView(
  section: ReviewConfigSection,
  scopeKind: "tenant" | "repository",
  scopeRef: string,
): ReviewConfigView {
  // Never share the fixture object with a client editor: local changes must
  // not leak between requests or become a fake persisted configuration.
  const content = structuredClone(demoReviewConfigContent[section]);
  const canonicalContent = JSON.stringify(content);
  return {
    section,
    requested_scope_kind: scopeKind,
    requested_scope_ref: scopeKind === "repository" ? scopeRef : undefined,
    origin_scope_kind: "tenant",
    inherited: scopeKind === "repository",
    revision: 7,
    content_sha256: createHash("sha256").update(canonicalContent).digest("hex"),
    content,
    updated_by: "demo-control-plane",
    updated_at: "2026-09-20T00:00:00Z",
  };
}

function demoReviewConfigHistory(
  section: ReviewConfigSection,
  scopeKind: "tenant" | "repository",
  scopeRef: string,
): ReviewConfigHistory {
  const view = demoReviewConfigView(section, scopeKind, scopeRef);
  return {
    requested_scope_kind: scopeKind,
    requested_scope_ref: scopeKind === "repository" ? scopeRef : undefined,
    origin_scope_kind: "tenant",
    inherited: scopeKind === "repository",
    versions: [
      {
        revision: view.revision,
        content_sha256: view.content_sha256,
        created_by: "demo-control-plane",
        created_at: "2026-09-20T00:00:00Z",
      },
      {
        revision: view.revision - 1,
        content_sha256: createHash("sha256")
          .update(`${section}:preview-revision-${view.revision - 1}`)
          .digest("hex"),
        created_by: "platform@acme.example",
        created_at: "2026-09-12T09:15:00Z",
      },
    ],
  };
}

export type ModelProbeReceipt = {
  id: string;
  scope_kind: "tenant" | "repository";
  scope_ref?: string;
  scope_provider?: "github" | "gitlab";
  scope_api_base_url?: string;
  config_revision: number;
  content_sha256: string;
  provider: string;
  protocol: string;
  model: string;
  endpoint_host: string;
  state: "queued" | "running" | "succeeded" | "failed";
  attempt: number;
  response_sha256?: string;
  latency_ms?: number;
  error_code?: string;
  error_message?: string;
  requested_by: string;
  created_at: string;
  started_at?: string;
  finished_at?: string;
};

export type ModelProbeData = {
  source: DataSource;
  probes: ModelProbeReceipt[];
  detail?: string;
};

const demoRuns: ReviewRun[] = [
  {
    id: "demo-42a1",
    revision: 7,
    state: "analyzing",
    trigger_kind: "pull_request",
    review_mode: "security",
    head_sha: "bd91254e6af2",
    base_sha: "c21d09db9c74",
    created_at: "2026-09-18T02:04:00Z",
    started_at: "2026-09-18T02:04:14Z",
    provider: "github",
    api_base_url: "https://api.github.com",
    repository: "RainLib/open-review-platform",
    review_number: 42,
  },
  {
    id: "demo-31f9",
    revision: 4,
    state: "needs_attention",
    trigger_kind: "comment",
    review_mode: "deep",
    head_sha: "4e17c8f9a122",
    base_sha: "2ad4adf1c8b0",
    failure_code: "provider_receipt_unconfirmed",
    failure_message:
      "The review evidence is retained, but provider publication requires an explicit retry or acknowledgement.",
    created_at: "2026-09-18T01:36:00Z",
    started_at: "2026-09-18T01:36:09Z",
    finished_at: "2026-09-18T01:39:35Z",
    provider: "github",
    api_base_url: "https://api.github.com",
    repository: "RainLib/open-review-platform",
    review_number: 39,
    title: "Provider publication needs a human decision",
    author: "demo-maintainer",
    intervention: {
      id: "intervention-demo-31f9",
      run_id: "demo-31f9",
      revision: 1,
      state: "open",
      opened_at: "2026-09-18T01:39:35Z",
      reason:
        "The provider status receipt could not be confirmed after bounded retries.",
      run_revision: 4,
      run_state: "needs_attention",
      failure_code: "provider_receipt_unconfirmed",
      failure_message:
        "The review evidence is retained, but provider publication requires an explicit retry or acknowledgement.",
      provider: "github",
      api_base_url: "https://api.github.com",
      repository: "RainLib/open-review-platform",
      review_number: 39,
      title: "Provider publication needs a human decision",
      author: "demo-maintainer",
    },
  },
  {
    id: "demo-080c",
    revision: 3,
    state: "completed",
    trigger_kind: "pull_request",
    review_mode: "standard",
    head_sha: "c0f4e15b19a7b93f1d4a87e62c9be0d19a2f73d4",
    base_sha: "4a76d9ba0f11d237ea804bf5c6213dbe53a47e91",
    created_at: "2026-09-17T14:22:00Z",
    started_at: "2026-09-17T14:22:07Z",
    finished_at: "2026-09-17T14:25:17Z",
    provider: "gitlab",
    api_base_url: "https://gitlab.com/api/v4",
    repository: "platform/agent-harness",
    review_number: 118,
    title: "Make provider receipt retries idempotent",
    author: "demo-release-engineer",
  },
  {
    id: "demo-cli-security",
    revision: 2,
    state: "completed",
    trigger_kind: "cli",
    review_mode: "security",
    head_sha: "c7bd529c0a9dfe88b654249d1fe1f0c9a95c433d",
    base_sha: "0e6d9d1211a87bfad4d4e67d96a38da7b82b7cdd",
    created_at: "2026-09-19T09:28:00Z",
    started_at: "2026-09-19T09:28:06Z",
    finished_at: "2026-09-19T09:31:18Z",
    provider: "github",
    api_base_url: "https://api.github.com",
    repository: "RainLib/open-review-platform",
    review_number: 44,
    title: "Harden provider webhook verification",
    author: "demo-cli-engineer",
  },
];

const demoReviewSchedules: ReviewSchedule[] = [
  {
    id: "schedule-demo-next-release",
    source_run_id: "demo-080c",
    revision: 2,
    state: "scheduled",
    provider: "gitlab",
    api_base_url: "https://gitlab.com/api/v4",
    repository: "platform/agent-harness",
    review_number: 118,
    title: "Make provider receipt retries idempotent",
    author: "demo-release-engineer",
    review_mode: "security",
    base_sha: "0e6d9d1211a87bfad4d4e67d96a38da7b82b7cdd",
    head_sha: "c0f4e15b19a7b93f1d4a87e62c9be0d19a2f73d4",
    scheduled_for: "2026-09-22T02:00:00Z",
    requested_by: "release-engineering@acme.example",
    created_at: "2026-09-20T01:04:00Z",
    updated_at: "2026-09-20T01:04:00Z",
  },
  {
    id: "schedule-demo-coalesced",
    source_run_id: "demo-080c",
    admitted_run_id: "demo-42a1",
    revision: 3,
    state: "coalesced",
    provider: "github",
    api_base_url: "https://api.github.com",
    repository: "RainLib/open-review-platform",
    review_number: 42,
    title: "Harden provider webhook verification",
    author: "demo-cli-engineer",
    review_mode: "deep",
    base_sha: "c21d09db9c74",
    head_sha: "bd91254e6af2",
    scheduled_for: "2026-09-19T06:00:00Z",
    requested_by: "platform@acme.example",
    created_at: "2026-09-19T05:32:00Z",
    updated_at: "2026-09-19T06:00:08Z",
  },
  {
    id: "schedule-demo-blocked",
    source_run_id: "demo-31f9",
    revision: 1,
    state: "blocked",
    provider: "github",
    api_base_url: "https://api.github.com",
    repository: "RainLib/open-review-platform",
    review_number: 39,
    title: "Provider publication needs a human decision",
    author: "demo-maintainer",
    review_mode: "deep",
    base_sha: "2ad4adf1c8b0",
    head_sha: "4e17c8f9a122",
    scheduled_for: "2026-09-20T04:00:00Z",
    requested_by: "demo-maintainer",
    blocked_reason:
      "The source review still has an unresolved provider publication intervention.",
    created_at: "2026-09-20T00:40:00Z",
    updated_at: "2026-09-20T00:42:00Z",
  },
];

const demoRuleSets: RuleSet[] = [
  {
    id: "rule-auth-boundary",
    name: "Authentication boundary",
    description:
      "Require an explicit trust-boundary review when identity or token handling changes.",
    created_by: "security@acme.example",
    created_at: "2026-09-04T08:00:00Z",
    updated_at: "2026-09-17T10:31:00Z",
    latest_version: {
      id: "version-demo-auth-boundary-4",
      version: 4,
      revision: 11,
      state: "published",
      content_sha256:
        "12d0c0b68f2671b77d7e37c5079064fa677d6c42a2c67b769bb686ee4e1f121f",
      created_by: "security@acme.example",
      created_at: "2026-09-17T10:11:00Z",
      updated_at: "2026-09-17T10:31:00Z",
    },
  },
  {
    id: "rule-release-evidence",
    name: "Release evidence",
    description:
      "Ask for verification and rollback evidence when production paths are changed.",
    created_by: "platform@acme.example",
    created_at: "2026-08-29T08:00:00Z",
    updated_at: "2026-09-15T07:19:00Z",
    latest_version: {
      id: "version-demo-release-evidence-7",
      version: 7,
      revision: 18,
      state: "published",
      content_sha256:
        "cb746b3c04ffcb26a79730f0e756d92f0ca55f691195b9eb793b59e4b1ca924a",
      created_by: "platform@acme.example",
      created_at: "2026-09-15T06:42:00Z",
      updated_at: "2026-09-15T07:19:00Z",
    },
  },
];

// Preview records mirror control-plane shapes but contain only synthetic
// source examples, no credentials or actionable approval authority.
const demoRuleApprovals: RuleApproval[] = [
  {
    id: "approval-demo-release-evidence-8",
    rule_set_id: "rule-release-evidence",
    rule_set_name: "Release evidence",
    rule_version_id: "version-demo-release-evidence-8",
    version: 8,
    version_state: "in_review",
    content_sha256:
      "b2e3a62dc0d20d484a01ab1ea7a4b99f5a9670cae3a99ac3a55e6404f3942cf0",
    requested_by: "release-engineering@acme.example",
    required_approvals: 2,
    approval_count: 1,
    rejection_count: 0,
    can_decide: false,
    can_publish: false,
    state: "pending",
    created_at: "2026-09-19T08:24:00Z",
  },
  {
    id: "approval-demo-auth-boundary-4",
    rule_set_id: "rule-auth-boundary",
    rule_set_name: "Authentication boundary",
    rule_version_id: "version-demo-auth-boundary-4",
    version: 4,
    version_state: "approved",
    content_sha256:
      "12d0c0b68f2671b77d7e37c5079064fa677d6c42a2c67b769bb686ee4e1f121f",
    requested_by: "security@acme.example",
    required_approvals: 2,
    approval_count: 2,
    rejection_count: 0,
    actor_decision: "approved",
    can_decide: false,
    can_publish: false,
    state: "approved",
    created_at: "2026-09-17T09:05:00Z",
    decided_at: "2026-09-17T10:02:00Z",
  },
];

const demoRuleBindings: RuleBinding[] = [
  {
    id: "binding-demo-auth-main",
    tenant_id: "acme",
    rule_version_id: "version-demo-auth-boundary-4",
    scope_kind: "repository",
    scope_ref: "RainLib/open-review-platform",
    precedence: 100,
    target_branch_glob: "main",
    path_include_glob: "apps/**",
    path_exclude_glob: "**/generated/**",
    state: "active",
    created_by: "security@acme.example",
    created_at: "2026-09-17T10:34:00Z",
    updated_at: "2026-09-17T10:34:00Z",
  },
  {
    id: "binding-demo-release-shadow",
    tenant_id: "acme",
    rule_version_id: "version-demo-release-evidence-7",
    scope_kind: "repository",
    scope_ref: "platform/agent-harness",
    precedence: 90,
    target_branch_glob: "release/*",
    state: "shadow",
    created_by: "platform@acme.example",
    created_at: "2026-09-18T11:17:00Z",
    updated_at: "2026-09-18T11:17:00Z",
  },
];

const demoRuleExceptions: RuleException[] = [
  {
    id: "exception-demo-release-evidence",
    rule_set_id: "rule-release-evidence",
    rule_set_name: "Release evidence",
    rule_version_id: "version-demo-release-evidence-7",
    rule_version: 7,
    rule_key: "release.rollback-evidence",
    scope_kind: "repository",
    scope_ref: "platform/agent-harness",
    target_branch_glob: "release/2026.09",
    reason:
      "The controlled rollout is covered by an incident runbook while the service migration is completed.",
    ticket_url: "https://tracker.example/PLAT-184",
    requested_by: "release-engineering@acme.example",
    approved_by: "governance@acme.example",
    decision_comment: "Approved with the stated compensating control.",
    state: "approved",
    effective_state: "approved",
    can_decide: false,
    can_revoke: false,
    expires_at: "2026-10-01T00:00:00Z",
    created_at: "2026-09-18T11:42:00Z",
    updated_at: "2026-09-18T12:05:00Z",
  },
];

const demoRuleCatalog: RuleCatalogEntry[] = [
  {
    id: "catalog-auth-boundary",
    version: "2026.09.1",
    title: "Authentication boundary",
    description:
      "Require explicit review evidence when identity, credentials, or authorization boundaries change.",
    tags: ["security", "identity", "governance"],
    rule_count: 6,
    content_sha256:
      "12d0c0b68f2671b77d7e37c5079064fa677d6c42a2c67b769bb686ee4e1f121f",
    origin: "open-review/core-catalog",
    can_install: false,
    installation: {
      rule_set_id: "rule-auth-boundary",
      rule_set_name: "Authentication boundary",
      rule_version: 4,
      rule_version_state: "published",
    },
  },
  {
    id: "catalog-release-evidence",
    version: "2026.09.1",
    title: "Release evidence",
    description:
      "Collect verification, rollout, and rollback evidence for production-sensitive changes.",
    tags: ["release", "verification", "risk"],
    rule_count: 5,
    content_sha256:
      "cb746b3c04ffcb26a79730f0e756d92f0ca55f691195b9eb793b59e4b1ca924a",
    origin: "open-review/core-catalog",
    can_install: false,
    installation: {
      rule_set_id: "rule-release-evidence",
      rule_set_name: "Release evidence",
      rule_version: 7,
      rule_version_state: "published",
    },
  },
  {
    id: "catalog-change-safety",
    version: "2026.09.1",
    title: "Change safety baseline",
    description:
      "A release-pinned baseline for tests, trust boundaries, and blast-radius documentation.",
    tags: ["quality", "safety", "baseline"],
    rule_count: 4,
    content_sha256:
      "c842459c18a470052791fd20753b2bdc0a84dc4ce9de52cb4538c74b262ea053",
    origin: "open-review/core-catalog",
    can_install: false,
  },
];

const demoInstallations: ProviderInstallation[] = [
  {
    id: "installation-demo-1",
    provider: "github",
    external_id: "123456",
    repository_scope: "RainLib/*",
    automatic_reviews: true,
    author_scope: "all",
    minimum_severity: "medium",
    api_base_url: "https://api.github.com",
    active: true,
    verification_state: "verified",
  },
  {
    id: "installation-demo-gitlab",
    provider: "gitlab",
    external_id: "platform-group",
    repository_scope: "platform/*",
    automatic_reviews: false,
    author_scope: "all",
    minimum_severity: "high",
    api_base_url: "https://gitlab.example.internal/api/v4",
    active: true,
    verification_state: "verified",
  },
];

// Preview fixtures normally show a completed workspace so the Console pages can
// be inspected. Keep the setup gate observable too: local reviewers can set
// OPEN_REVIEW_CONSOLE_DEMO_SETUP_STATE without making a control-plane request.
// This is deliberately server-only and never changes the production decision.
function getDemoWorkspaceInitialization(): WorkspaceInitialization {
  const configured = process.env.OPEN_REVIEW_CONSOLE_DEMO_SETUP_STATE;
  const connection = demoInstallations.find(
    (installation) => installation.active,
  );
  switch (configured) {
    case "needs_connection":
      return { status: "needs_connection" };
    case "verifying_connection":
      return {
        status: "verifying_connection",
        connection,
        detail:
          "Previewing the provider verification gate before workspace access is granted.",
      };
    case "connection_failed":
      return {
        status: "connection_failed",
        connection,
        detail:
          "Previewing a failed provider verification; no workspace access is granted.",
      };
    case "needs_setup":
      return {
        status: "needs_setup",
        connection,
        detail:
          "Previewing an incomplete setup checkpoint; finish the review baseline before opening the Console.",
      };
    case "unavailable":
      return {
        status: "unavailable",
        detail: "Previewing an unavailable control-plane initialization check.",
      };
    case "access_denied":
      return {
        status: "access_denied",
        detail:
          "Previewing a workspace that the signed-in identity cannot access.",
      };
    case "ready":
    case undefined:
      return connection
        ? { status: "ready", connection }
        : { status: "needs_connection" };
    default:
      return {
        status: "unavailable",
        detail:
          "OPEN_REVIEW_CONSOLE_DEMO_SETUP_STATE must be ready, access_denied, needs_connection, verifying_connection, connection_failed, needs_setup, or unavailable.",
      };
  }
}

function getDemoWorkspaceSetupCheckpoint(): WorkspaceSetupCheckpoint {
  switch (getDemoWorkspaceInitialization().status) {
    case "needs_setup":
      // Keep this before the merge-policy save step so the preview renders a
      // truthful resumable setup screen without pretending a demo mutation is
      // durable.
      return {
        current_step: "review_scope",
        revision: 1,
        updated_by: "demo-owner",
        updated_at: "2026-09-20T08:00:00Z",
      };
    case "ready":
      return {
        current_step: "complete",
        revision: 5,
        updated_by: "demo-owner",
        updated_at: "2026-09-20T08:00:00Z",
        completed_at: "2026-09-20T08:00:00Z",
      };
    default:
      return { current_step: "connect", revision: 0 };
  }
}

// Preview inventory is intentionally limited to safe repository metadata. It
// models both GitHub.com and a self-managed GitLab deployment without exposing
// provider credentials, permissions, or raw provider responses.
const demoInstallationRepositories: Record<string, ProviderRepository[]> = {
  "installation-demo-1": [
    {
      external_id: "github-rainlib-open-review-platform",
      name: "RainLib/open-review-platform",
      default_branch: "main",
      visibility: "private",
      archived: false,
      last_seen_at: "2026-09-20T00:25:00Z",
    },
    {
      external_id: "github-rainlib-review-rules",
      name: "RainLib/review-rules",
      default_branch: "main",
      visibility: "private",
      archived: false,
      last_seen_at: "2026-09-20T00:24:42Z",
    },
  ],
  "installation-demo-gitlab": [
    {
      external_id: "gitlab-platform-agent-harness",
      name: "platform/agent-harness",
      default_branch: "main",
      visibility: "internal",
      archived: false,
      last_seen_at: "2026-09-20T00:23:51Z",
    },
    {
      external_id: "gitlab-platform-notification-gateway",
      name: "platform/notification-gateway",
      default_branch: "main",
      visibility: "private",
      archived: false,
      last_seen_at: "2026-09-20T00:22:18Z",
    },
  ],
};

const demoInstallationWebhookReceipts: Record<
  string,
  InstallationWebhookReceipt[]
> = {
  "installation-demo-1": [
    {
      id: "webhook-demo-github-pr-42",
      event_name: "pull_request.opened",
      received_at: "2026-09-20T00:24:31Z",
      resource_kind: "pull_request",
      job_id: "job-demo-github-pr-42",
      repository: "RainLib/open-review-platform",
      review_number: 42,
      job_state: "succeeded",
      run_id: "demo-42a1",
      run_state: "analyzing",
      trigger_kind: "pull_request",
    },
  ],
  "installation-demo-gitlab": [
    {
      id: "webhook-demo-gitlab-mr-118",
      event_name: "Merge Request Hook",
      received_at: "2026-09-20T00:23:58Z",
      resource_kind: "pull_request",
      job_id: "job-demo-gitlab-mr-118",
      repository: "platform/agent-harness",
      review_number: 118,
      job_state: "succeeded",
      run_id: "demo-080c",
      run_state: "completed",
      trigger_kind: "merge_request",
    },
  ],
};

// SSO preview demonstrates the lifecycle evidence without ever returning an
// IdP client secret or making a preview workspace capable of enforcement.
const demoSSOOverview: SSOOverview = {
  configuration: {
    tenant_id: "acme",
    protocol: "oidc",
    display_name: "Acme workforce SSO",
    issuer_url: "https://identity.acme.example",
    client_id: "open-review-console",
    secret_configured: true,
    group_claim: "groups",
    state: "enforcement_ready",
    revision: 4,
    tested_revision: 4,
    updated_by: "identity.owner",
    created_at: "2026-09-14T08:00:00Z",
    updated_at: "2026-09-19T15:30:00Z",
  },
  domains: [
    {
      id: "sso-domain-demo-verified",
      domain: "acme.example",
      challenge_token: "open-review-demo-domain-proof",
      state: "verified",
      revision: 2,
      created_by: "identity.owner",
      created_at: "2026-09-14T08:10:00Z",
      verified_at: "2026-09-14T08:18:00Z",
    },
    {
      id: "sso-domain-demo-pending",
      domain: "engineering.acme.example",
      challenge_token: "open-review-demo-pending-proof",
      state: "pending",
      revision: 1,
      created_by: "identity.owner",
      created_at: "2026-09-19T15:35:00Z",
    },
  ],
  mappings: [
    {
      id: "sso-mapping-demo-reviewers",
      group_value: "engineering-reviewers",
      role: "reviewer",
      repository_scope: "RainLib/*",
      revision: 1,
      created_by: "identity.owner",
      created_at: "2026-09-19T15:40:00Z",
      updated_at: "2026-09-19T15:40:00Z",
    },
  ],
  probes: [
    {
      id: "sso-probe-demo-current",
      config_revision: 4,
      protocol: "oidc",
      target_url:
        "https://identity.acme.example/.well-known/openid-configuration",
      state: "succeeded",
      attempt: 1,
      metadata_sha256:
        "0f3f4d13c1bbf569c5dc76e289a19996051c41f0b7bb9375b995cc2ce3013f2b",
      requested_by: "identity.owner",
      created_at: "2026-09-19T15:31:00Z",
      started_at: "2026-09-19T15:31:02Z",
      finished_at: "2026-09-19T15:31:03Z",
    },
  ],
  readiness: { ready: true, blockers: [] },
};

// API key fixtures deliberately include only the non-sensitive lifecycle read
// model. A secret is returned only by the live create endpoint and is never a
// property of the persisted key record.
const demoAPIKeys: WorkspaceAPIKey[] = [
  {
    id: "api-key-demo-release-ci",
    name: "Release review CI",
    prefix: "kodus_demo_release",
    caller_subject: "ci:release-pipeline",
    scopes: ["reviews:create", "reviews:read"],
    repositories: ["RainLib/open-review-platform"],
    created_by: "platform.owner",
    created_at: "2026-09-01T08:00:00Z",
    expires_at: "2026-12-01T08:00:00Z",
    last_used_at: "2026-09-20T00:24:31Z",
  },
  {
    id: "api-key-demo-retired",
    name: "Retired migration client",
    prefix: "kodus_demo_retired",
    caller_subject: "ci:migration",
    scopes: ["reviews:read"],
    repositories: ["RainLib/open-review-platform"],
    created_by: "platform.owner",
    created_at: "2026-07-01T08:00:00Z",
    expires_at: "2026-10-01T08:00:00Z",
    last_used_at: "2026-09-08T10:12:00Z",
    revoked_at: "2026-09-12T08:00:00Z",
    revoked_by: "platform.owner",
  },
];

const demoMembers: WorkspaceMember[] = [
  { tenant_id: "acme", subject: "platform.owner", role: "owner", active: true },
  { tenant_id: "acme", subject: "identity.owner", role: "admin", active: true },
  {
    tenant_id: "acme",
    subject: "release.governance",
    role: "reviewer",
    active: true,
  },
  {
    tenant_id: "acme",
    subject: "former.contractor",
    role: "viewer",
    active: false,
    deactivated_at: "2026-09-18T09:30:00Z",
    deactivated_by: "platform.owner",
  },
];

const demoInvitations: WorkspaceInvitation[] = [
  {
    id: "invitation-demo-pending",
    tenant_id: "acme",
    subject: "security.reviewer",
    role: "rule_admin",
    status: "pending",
    created_by: "platform.owner",
    created_at: "2026-09-19T10:00:00Z",
    expires_at: "2026-09-26T10:00:00Z",
  },
  {
    id: "invitation-demo-accepted",
    tenant_id: "acme",
    subject: "observability.viewer",
    role: "viewer",
    status: "accepted",
    created_by: "identity.owner",
    created_at: "2026-09-15T08:00:00Z",
    expires_at: "2026-09-22T08:00:00Z",
    accepted_at: "2026-09-15T09:12:00Z",
    accepted_by: "observability.viewer",
  },
];

const demoUsageDashboard: Omit<UsageDashboard, "source"> = {
  period_start: "2026-09-01T00:00:00Z",
  period_end: "2026-10-01T00:00:00Z",
  entitlement: {
    monthly_review_limit: 200,
    soft_warning_percent: 80,
    updated_by: "platform.owner",
    updated_at: "2026-09-01T00:00:00Z",
  },
  settled: 128,
  reserved: 3,
  remaining: 69,
  warning: false,
  can_manage: false,
  reconciliation: {
    state: "clean",
    period_start: "2026-09-01T00:00:00Z",
    period_end: "2026-10-01T00:00:00Z",
    scanned_runs: 131,
    drifted_runs: 0,
    missing_reservations: 0,
    state_mismatches: 0,
    missing_ledger_proofs: 0,
    last_reconciled_at: "2026-09-20T00:20:00Z",
    last_reconciled_by: "platform.owner",
  },
  repositories: [
    { repository: "RainLib/open-review-platform", settled: 96, reserved: 2 },
    { repository: "platform/agent-harness", settled: 32, reserved: 1 },
  ],
  ledger: [
    {
      id: "usage-demo-reserve-42a1",
      run_id: "demo-42a1",
      repository: "RainLib/open-review-platform",
      metric: "review",
      quantity: 1,
      unit: "review",
      event_kind: "reserve",
      metadata: { source: "preview", revision: 1 },
      occurred_at: "2026-09-20T00:24:31Z",
    },
    {
      id: "usage-demo-settle-080c",
      run_id: "demo-080c",
      repository: "platform/agent-harness",
      metric: "review",
      quantity: 1,
      unit: "review",
      event_kind: "settle",
      metadata: { source: "preview", revision: 1 },
      occurred_at: "2026-09-20T00:23:58Z",
    },
  ],
};

const demoAuditEvents: AuditEvent[] = [
  {
    id: "audit-demo-installation-github-verified",
    actor_subject: "platform.owner",
    action: "installation.verified",
    target: "tenant/acme/installations/installation-demo-1",
    metadata: { provider: "github", scope: "RainLib/*", source: "preview" },
    created_at: "2026-09-20T00:25:12Z",
  },
  {
    id: "audit-demo-installation-gitlab-synchronized",
    actor_subject: "platform.owner",
    action: "installation.inventory_synchronized",
    target: "tenant/acme/installations/installation-demo-gitlab",
    metadata: {
      provider: "gitlab",
      mode: "self_managed",
      scope: "platform/*",
      repositories: 2,
    },
    created_at: "2026-09-20T00:23:52Z",
  },
  {
    id: "audit-demo-configuration",
    actor_subject: "platform.owner",
    action: "review_config.updated",
    target: "tenant/acme/review-config/general",
    metadata: { revision: 7, source: "preview" },
    created_at: "2026-09-20T01:12:00Z",
  },
  {
    id: "audit-demo-export",
    actor_subject: "compliance.owner",
    action: "data_governance.export_requested",
    target: "tenant/acme/audit-range",
    metadata: { data_class: "audit", state: "queued" },
    created_at: "2026-09-20T00:48:00Z",
  },
];

const demoDataGovernanceOverview: DataGovernanceOverview = {
  residency: {
    configured: true,
    primary_region: "us-east-1",
    backup_region: "us-west-2",
    object_region: "us-east-1",
    queue_region: "us-east-1",
    model_boundary: "external/byok",
    revision: 4,
    effective_at: "2026-09-16T08:00:00Z",
    observed_at: "2026-09-20T00:30:00Z",
    updated_by: "platform.owner",
    updated_at: "2026-09-16T08:00:00Z",
  },
  boundaries: [
    {
      name: "Control plane",
      value: "us-east-1",
      observed: true,
      detail: "Authoritative PostgreSQL workflow state.",
      external: false,
    },
    {
      name: "Artifact storage",
      value: "us-east-1",
      observed: true,
      detail: "Encrypted governance artifacts only.",
      external: false,
    },
    {
      name: "Message delivery",
      value: "us-east-1",
      observed: true,
      detail: "Outbox remains the recovery authority.",
      external: false,
    },
    {
      name: "Model boundary",
      value: "external/byok",
      observed: false,
      detail: "Provider processing depends on the configured route.",
      external: true,
    },
  ],
  policies: [
    {
      id: "retention-demo-findings",
      scope_kind: "tenant",
      data_class: "findings",
      retention_days: 180,
      state: "active",
      revision: 3,
      impact_records: 214,
      impact_legal_hold: false,
      change_reason: "Engineering evidence retention baseline",
      requested_by: "platform.owner",
      decided_by: "compliance.owner",
      decided_at: "2026-09-16T08:00:00Z",
      created_at: "2026-09-14T08:00:00Z",
      updated_at: "2026-09-16T08:00:00Z",
    },
  ],
  legal_holds: [
    {
      id: "legal-hold-demo-release",
      scope_kind: "repository",
      scope_ref: "RainLib/open-review-platform",
      data_class: "audit",
      reason:
        "Release evidence is retained during the previewed approval window.",
      state: "active",
      revision: 1,
      created_by: "compliance.owner",
      created_at: "2026-09-19T09:30:00Z",
    },
  ],
  jobs: [
    {
      id: "governance-demo-audit-export",
      kind: "export",
      scope_kind: "audit_range",
      scope_ref: "2026-08-21T00:00:00.000Z/2026-09-20T00:00:00.000Z",
      data_classes: ["audit"],
      state: "queued",
      progress: 0,
      revision: 1,
      idempotency_key: "demo-audit-export",
      reason: "Quarterly compliance evidence preview",
      requested_by: "compliance.owner",
      artifact_ready: false,
      receipt: { schema: "open-review.data-export.preview.v1" },
      created_at: "2026-09-20T00:48:00Z",
      updated_at: "2026-09-20T00:48:00Z",
    },
  ],
};

const demoPlatformHealthOverview: PlatformHealthOverview = {
  sampled_at: "2026-09-20T00:30:00Z",
  freshness_window_seconds: 300,
  agent_executor: {
    state: "unobserved",
    detail: "Preview fixture only; no coding adapter configuration is observed.",
  },
  agent_decision: {
    state: "unobserved",
    detail: "Preview fixture only; no Jev source worker configuration is observed.",
  },
  agent_credential_broker: {
    state: "unobserved",
    detail: "Preview fixture only; no private coding-credential broker is observed.",
  },
  components: [
    {
      key: "control-api",
      name: "Control API",
      state: "live",
      detail: "Preview fixture: an authenticated tenant sample completed.",
      observed: true,
      sampled_at: "2026-09-20T00:30:00Z",
      capability: "admission and control-plane reads",
      recovery_hint: "Inspect API logs and database connectivity.",
    },
    {
      key: "postgresql",
      name: "PostgreSQL",
      state: "live",
      detail: "Preview fixture: the authoritative workflow store responded.",
      observed: true,
      sampled_at: "2026-09-20T00:30:00Z",
      capability: "authoritative workflow and policy state",
      recovery_hint: "Restore the authority store before accepting writes.",
    },
    {
      key: "rabbitmq",
      name: "RabbitMQ",
      state: "configured_only",
      detail: "Broker metrics are intentionally absent from this tenant view.",
      observed: false,
      capability: "event delivery acceleration",
      recovery_hint:
        "Inspect broker alarms, queue depth, and DLQ from an operator plane.",
    },
    {
      key: "workers",
      name: "Worker fleet",
      state: "live",
      detail:
        "Preview fixture: one worker has a current tenant execution lease and fresh heartbeat.",
      observed: true,
      sampled_at: "2026-09-20T00:30:00Z",
      capability: "review execution and governed jobs",
      recovery_hint:
        "Inspect tenant lease expiry and worker heartbeat freshness.",
    },
    {
      key: "publisher",
      name: "Provider integrations",
      state: "live",
      detail:
        "Preview fixture: one read-only provider probe is fresh; write access remains unproven.",
      observed: true,
      sampled_at: "2026-09-20T00:29:00Z",
      capability: "read access, comments, inline findings, and checks",
      recovery_hint:
        "Inspect read-only probes, receipt failures, and provider rate limits.",
    },
  ],
  queues: [
    {
      key: "review-execution",
      name: "Review execution",
      source: "postgresql",
      state: "live",
      ready: 1,
      running: 1,
      failed: 0,
      expired_leases: 0,
      oldest_age_seconds: 42,
      sampled_at: "2026-09-20T00:30:00Z",
      detail: "Tenant-scoped authoritative workflow state.",
    },
    {
      key: "notifications",
      name: "Notification deliveries",
      source: "postgresql",
      state: "degraded",
      ready: 0,
      running: 0,
      failed: 1,
      expired_leases: 0,
      oldest_age_seconds: 0,
      sampled_at: "2026-09-20T00:30:00Z",
      detail: "Tenant-scoped authoritative workflow state.",
    },
  ],
  gitlab_author_admissions: [],
  workers: [
    {
      worker_id: "review-runner/preview-42",
      kind: "review-runner",
      state: "live",
      active_leases: 1,
      expired_leases: 0,
      last_activity: "2026-09-20T00:29:40Z",
      last_heartbeat: "2026-09-20T00:30:00Z",
      version: "preview",
      capacity: 0,
      busy: 0,
      detail:
        "Preview fixture: heartbeat is associated with a current tenant execution lease.",
    },
  ],
  providers: [
    {
      installation_id: "installation-demo-1",
      provider: "github",
      host: "api.github.com",
      repository_scope: "RainLib/*",
      active: true,
      state: "live",
      last_webhook_at: "2026-09-20T00:25:00Z",
      last_publish_at: "2026-09-20T00:24:00Z",
      publish_failures: 0,
      last_probe_at: "2026-09-20T00:29:00Z",
      permissions: ["repository_inventory:read"],
      rate_limit_remaining: 4999,
      rate_limit_limit: 5000,
      rate_limit_reset_at: "2026-09-20T01:29:00Z",
      probe_latency_ms: 132,
      detail: "Preview fixture: read-only provider access evidence is fresh.",
    },
  ],
  incidents: [
    {
      id: "incident-demo-provider-delivery",
      title: "Provider delivery latency under observation",
      state: "mitigating",
      scope: "provider",
      affected_area: "GitHub publication delivery",
      started_at: "2026-09-20T00:12:00Z",
      created_by: "platform-operator",
      revision: 2,
      updated_at: "2026-09-20T00:28:00Z",
    },
  ],
  incident_tracking_available: true,
  worker_heartbeat_available: true,
  broker_metrics_available: false,
  runbooks: [
    {
      key: "review-backlog",
      title: "Review backlog recovery",
      version: "v1",
      required_role: "platform operator",
      steps: [
        "Confirm PostgreSQL authority",
        "Inspect expired leases",
        "Restore workers",
        "Observe backlog age",
      ],
      executable: false,
      detail: "Read-only guide; the console cannot execute shell commands.",
    },
    {
      key: "provider-publication",
      title: "Provider publication recovery",
      version: "v1",
      required_role: "workspace admin",
      steps: [
        "Inspect receipt errors",
        "Verify app permissions",
        "Check provider rate limits",
        "Retry an approved workflow",
      ],
      executable: false,
      detail: "Provider mutations remain outside this health read model.",
    },
  ],
};

const demoNotificationData: Omit<NotificationData, "source" | "detail"> = {
  destinations: [
    {
      id: "notification-demo-feishu",
      tenant_id: "demo-acme",
      name: "Platform engineering",
      provider: "feishu",
      enabled: true,
      revision: 3,
      created_at: "2026-09-15T08:00:00Z",
      updated_at: "2026-09-20T00:20:00Z",
    },
    {
      id: "notification-demo-dingtalk",
      tenant_id: "demo-acme",
      name: "Release governance",
      provider: "dingtalk",
      enabled: true,
      revision: 1,
      created_at: "2026-09-16T10:00:00Z",
      updated_at: "2026-09-16T10:00:00Z",
    },
  ],
  routes: [
    {
      id: "notification-route-demo-main",
      tenant_id: "demo-acme",
      destination_id: "notification-demo-feishu",
      repository_glob: "RainLib/*",
      branch_glob: "main",
      event_types: ["review.run.completed", "review.run.needs_attention"],
      min_severity: "high",
      enabled: true,
      priority: 1,
      revision: 2,
      created_at: "2026-09-15T08:05:00Z",
      updated_at: "2026-09-18T09:30:00Z",
    },
    {
      id: "notification-route-demo-release",
      tenant_id: "demo-acme",
      destination_id: "notification-demo-dingtalk",
      repository_glob: "RainLib/open-review-platform",
      branch_glob: "release/*",
      event_types: ["review.run.failed", "review.run.needs_attention"],
      min_severity: "critical",
      enabled: true,
      priority: 2,
      revision: 1,
      created_at: "2026-09-16T10:10:00Z",
      updated_at: "2026-09-16T10:10:00Z",
    },
  ],
  deliveries: [
    {
      id: "notification-delivery-demo-1",
      event_id: "event-demo-completed",
      event_type: "review.run.completed",
      run_id: "demo-080c",
      destination_id: "notification-demo-feishu",
      destination_name: "Platform engineering",
      state: "delivered",
      attempt: 1,
      response_code: 200,
      delivered_at: "2026-09-20T00:24:03Z",
      created_at: "2026-09-20T00:24:00Z",
      updated_at: "2026-09-20T00:24:03Z",
    },
    {
      id: "notification-delivery-demo-2",
      event_id: "event-demo-attention",
      event_type: "review.run.needs_attention",
      run_id: "demo-31f9",
      destination_id: "notification-demo-dingtalk",
      destination_name: "Release governance",
      state: "failed",
      attempt: 2,
      response_code: 429,
      last_error: "Provider retry window is pending.",
      created_at: "2026-09-20T00:22:00Z",
      updated_at: "2026-09-20T00:22:05Z",
    },
  ],
};

const demoEvents: Record<string, RunEvent[]> = {
  "demo-080c": [
    {
      id: "event-080-1",
      run_id: "demo-080c",
      revision: 1,
      event_type: "run.acknowledged",
      actor_kind: "provider",
      payload: { trigger: "pull_request" },
      created_at: "2026-09-17T14:22:00Z",
    },
    {
      id: "event-080-2",
      run_id: "demo-080c",
      revision: 2,
      event_type: "run.admitted",
      actor_kind: "system",
      payload: {
        configuration_revision: 7,
        rule_snapshot: "snapshot-demo-042",
      },
      created_at: "2026-09-17T14:22:04Z",
    },
    {
      id: "event-080-3",
      run_id: "demo-080c",
      revision: 3,
      event_type: "run.findings_normalized",
      actor_kind: "worker",
      payload: { finding_count: 1, highest_severity: "high" },
      created_at: "2026-09-17T14:24:48Z",
    },
    {
      id: "event-080-4",
      run_id: "demo-080c",
      revision: 3,
      event_type: "run.completed",
      actor_kind: "publisher",
      payload: { merge_gate: "passed", status_receipt: "published" },
      created_at: "2026-09-17T14:25:17Z",
    },
  ],
  "demo-31f9": [
    {
      id: "event-31-1",
      run_id: "demo-31f9",
      revision: 1,
      event_type: "run.acknowledged",
      actor_kind: "provider",
      payload: { trigger: "comment" },
      created_at: "2026-09-18T01:36:00Z",
    },
    {
      id: "event-31-2",
      run_id: "demo-31f9",
      revision: 2,
      event_type: "run.admitted",
      actor_kind: "system",
      payload: { snapshot: "resolved" },
      created_at: "2026-09-18T01:36:04Z",
    },
    {
      id: "event-31-3",
      run_id: "demo-31f9",
      revision: 4,
      event_type: "run.needs_attention",
      actor_kind: "publisher",
      payload: {
        code: "provider_receipt_unconfirmed",
        retry_after: "2026-09-18T01:44:00Z",
      },
      created_at: "2026-09-18T01:39:35Z",
    },
  ],
  "demo-cli-security": [
    {
      id: "event-cli-1",
      run_id: "demo-cli-security",
      revision: 1,
      event_type: "run.acknowledged",
      actor_kind: "cli",
      actor_subject: "cli-key:demo-security",
      payload: { trigger: "cli", mode: "security" },
      created_at: "2026-09-19T09:28:00Z",
    },
    {
      id: "event-cli-2",
      run_id: "demo-cli-security",
      revision: 2,
      event_type: "run.completed",
      actor_kind: "system",
      payload: { conclusion: "success" },
      created_at: "2026-09-19T09:31:18Z",
    },
  ],
  "demo-42a1": [
    {
      id: "event-42-1",
      run_id: "demo-42a1",
      revision: 1,
      event_type: "run.acknowledged",
      actor_kind: "provider",
      payload: { trigger: "pull_request" },
      created_at: "2026-09-18T02:04:00Z",
    },
    {
      id: "event-42-2",
      run_id: "demo-42a1",
      revision: 2,
      event_type: "run.admitted",
      actor_kind: "system",
      payload: { snapshot: "resolved" },
      created_at: "2026-09-18T02:04:03Z",
    },
    {
      id: "event-42-3",
      run_id: "demo-42a1",
      revision: 4,
      event_type: "run.preparing",
      actor_kind: "worker",
      payload: { checkout: "pinned" },
      created_at: "2026-09-18T02:04:14Z",
    },
    {
      id: "event-42-4",
      run_id: "demo-42a1",
      revision: 7,
      event_type: "run.analyzing",
      actor_kind: "worker",
      payload: { engine: "open-code-review" },
      created_at: "2026-09-18T02:05:10Z",
    },
  ],
};

async function request<T>(
  configuration: ControlPlaneRequestConfiguration,
  path: string,
): Promise<T> {
  const response = await fetch(`${configuration.baseURL}${path}`, {
    cache: "no-store",
    headers: configuration.headers,
  });

  if (!response.ok) {
    throw new ControlPlaneRequestError(response.status);
  }

  return response.json() as Promise<T>;
}

class ControlPlaneRequestError extends Error {
  constructor(readonly status: number) {
    super(`Control plane returned ${status}`);
    this.name = "ControlPlaneRequestError";
  }
}

// Public creation and invitation pages have no tenant yet, so they cannot use
// the initialized-workspace guard. Probe the tenant directory with a one-row
// read before showing a mutation form: a stale cookie must not look signed in.
export async function probeConsoleSession(): Promise<"accepted" | "rejected" | "unavailable"> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") return "accepted";
  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) return "unavailable";
  try {
    const response = await fetch(`${configuration.baseURL}/v1/tenants?limit=1`, {
      cache: "no-store",
      headers: configuration.headers,
      signal: AbortSignal.timeout(5_000),
    });
    if (response.status === 401) return "rejected";
    return response.ok ? "accepted" : "unavailable";
  } catch {
    return "unavailable";
  }
}

// Workspace pages are an authenticated control surface, not a preview of the
// onboarding UI. A newly recorded installation must also pass its durable,
// read-only provider probe before it can admit a review. Existing deployments
// retain their legacy eligibility during the migration rather than being
// silently rewritten as a fresh provider proof.
export async function getWorkspaceInitialization(
  org: string,
): Promise<WorkspaceInitialization> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    return getDemoWorkspaceInitialization();
  }

  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) {
    return {
      status: "unavailable",
      detail:
        "The signed-in browser is not connected to a configured control plane.",
    };
  }

  try {
    const [response, checkpoint] = await Promise.all([
      request<{ installations: ProviderInstallation[] }>(
        configuration,
        `/v1/tenants/${encodeURIComponent(org)}/installations?limit=25`,
      ),
      request<WorkspaceSetupCheckpoint>(
        configuration,
        `/v1/tenants/${encodeURIComponent(org)}/setup-checkpoint`,
      ),
    ]);
    const active = response.installations.filter(
      (installation) => installation.active,
    );
    const eligible = active.find(
      (installation) =>
        installation.verification_state === "legacy" ||
        installation.verification_state === "verified",
    );
    if (!eligible) {
      const connection = active[0];
      if (!connection) return { status: "needs_connection" };
      if (connection.verification_state === "failed") {
        return {
          status: "connection_failed",
          connection,
          detail:
            "The provider check did not complete. Review the deployment credential and installed repository permissions, then retry verification.",
        };
      }
      return {
        status: "verifying_connection",
        connection,
        detail: connection.verification?.state === "running" ||
          connection.verification_state === "checking"
          ? "A verification worker has claimed the read-only provider access check. Reviews remain disabled until its durable result is recorded."
          : "The read-only provider access check is queued. A verification worker must be running before it can complete; reviews remain disabled until then.",
      };
    }
    return checkpoint.current_step === "complete"
      ? { status: "ready", connection: eligible }
      : {
          status: "needs_setup",
          connection: eligible,
          detail: `Resume setup at ${checkpoint.current_step.replaceAll("_", " ")}.`,
        };
  } catch (error) {
    if (error instanceof ControlPlaneRequestError && error.status === 401) {
      return {
        status: "unavailable",
        needsSignIn: true,
        detail: "Your sign-in session is no longer accepted by the control plane.",
      };
    }
    if (
      error instanceof ControlPlaneRequestError &&
      [403, 404].includes(error.status)
    ) {
      // Do not disclose whether a requested slug exists. The directory itself
      // is independently filtered by membership in the control plane.
      return {
        status: "access_denied",
        detail:
          "This signed-in identity cannot access the requested workspace.",
      };
    }
    return {
      status: "unavailable",
      detail: "The initialization state could not be verified.",
    };
  }
}

// The control plane filters this list by membership. The console narrows it
// once more to active installations, so a switch cannot lead a user into a
// tenant that still belongs in the setup flow.
export async function getInitializedWorkspaces(): Promise<
  AccessibleWorkspace[]
> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    return [{ slug: "acme", name: "Acme", role: "owner" }];
  }

  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) return [];

  try {
    const response = await request<{ tenants: AccessibleWorkspace[] }>(
      configuration,
      "/v1/tenants?limit=100",
    );
    const initialized = await Promise.all(
      response.tenants.map(async (tenant) =>
        (await getWorkspaceInitialization(tenant.slug)).status === "ready"
          ? tenant
          : undefined,
      ),
    );
    return initialized.filter((tenant): tenant is AccessibleWorkspace =>
      Boolean(tenant),
    );
  } catch {
    return [];
  }
}

export async function getWorkspaceSetupCheckpoint(org: string): Promise<{
  source: DataSource;
  checkpoint?: WorkspaceSetupCheckpoint;
  detail?: string;
}> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    return {
      source: "demo",
      checkpoint: getDemoWorkspaceSetupCheckpoint(),
      detail:
        "Preview checkpoint only. Setup mutations remain unavailable until a live control plane is connected.",
    };
  }
  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) {
    return {
      source: "unconfigured",
      detail:
        "The signed-in browser is not connected to a configured control plane.",
    };
  }
  try {
    return {
      source: "live",
      checkpoint: await request<WorkspaceSetupCheckpoint>(
        configuration,
        `/v1/tenants/${encodeURIComponent(org)}/setup-checkpoint`,
      ),
    };
  } catch (error) {
    return {
      source: "unavailable",
      detail:
        error instanceof Error
          ? error.message
          : "The setup checkpoint could not be loaded.",
    };
  }
}

export async function getWorkspaceDirectory(): Promise<WorkspaceDirectory> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    return {
      source: "demo",
      workspaces: [
        {
          slug: "acme",
          name: "Acme",
          role: "owner",
          initialization: getDemoWorkspaceInitialization(),
        },
      ],
    };
  }

  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) {
    return {
      source: "unconfigured",
      workspaces: [],
      detail:
        "The signed-in browser is not connected to a configured control plane.",
    };
  }

  try {
    const response = await request<{ tenants: AccessibleWorkspace[] }>(
      configuration,
      "/v1/tenants?limit=100",
    );
    const workspaces = await Promise.all(
      response.tenants.map(async (workspace) => ({
        ...workspace,
        initialization: await getWorkspaceInitialization(workspace.slug),
      })),
    );
    if (workspaces.some((workspace) => workspace.initialization.needsSignIn)) {
      return {
        source: "unavailable",
        workspaces: [],
        needsSignIn: true,
        detail: "Your sign-in session is no longer accepted by the control plane. Sign in again to reload your workspaces.",
      };
    }
    return { source: "live", workspaces };
  } catch (error) {
    if (error instanceof ControlPlaneRequestError && error.status === 401) {
      return {
        source: "unavailable",
        workspaces: [],
        needsSignIn: true,
        detail: "Your sign-in session is no longer accepted by the control plane. Sign in again to reload your workspaces.",
      };
    }
    return {
      source: "unavailable",
      workspaces: [],
      detail: "The control plane could not load your workspaces. Retry when the connection is available.",
    };
  }
}

export async function getConsoleData(org: string): Promise<ConsoleData> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    return {
      source: "demo",
      runs: demoRuns,
      ruleSets: demoRuleSets,
      installations: demoInstallations,
    };
  }

  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) {
    return {
      source: "unconfigured",
      runs: [],
      ruleSets: [],
      installations: [],
      detail:
        "Sign in and configure CONTROL_API_URL before connecting this workspace to the control plane.",
    };
  }

  try {
    const [runs, ruleSets, installations] = await Promise.all([
      request<{ runs: ReviewRun[] }>(
        configuration,
        `/v1/tenants/${encodeURIComponent(org)}/runs?limit=25`,
      ),
      request<{ rule_sets: RuleSet[] }>(
        configuration,
        `/v1/tenants/${encodeURIComponent(org)}/rule-sets?limit=25`,
      ),
      request<{ installations: ProviderInstallation[] }>(
        configuration,
        `/v1/tenants/${encodeURIComponent(org)}/installations?limit=25`,
      ),
    ]);

    return {
      source: "live",
      runs: runs.runs,
      ruleSets: ruleSets.rule_sets,
      installations: installations.installations,
    };
  } catch (error) {
    return {
      source: "unavailable",
      runs: [],
      ruleSets: [],
      installations: [],
      detail:
        error instanceof Error
          ? error.message
          : "The control plane could not be reached.",
    };
  }
}

// A task and a connection detail route reference an immutable installation
// ID. The bounded overview is not authoritative for that exact identity.
export async function getProviderInstallationData(
  org: string,
  installationID: string,
): Promise<{ source: DataSource; installation?: ProviderInstallation; detail?: string }> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    return { source: "demo", installation: demoInstallations.find((item) => item.id === installationID) };
  }
  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) {
    return { source: "unconfigured", detail: "Sign in and configure the control plane before checking this connection." };
  }
  try {
    const installation = await request<ProviderInstallation>(
      configuration,
      `/v1/tenants/${encodeURIComponent(org)}/installations/${encodeURIComponent(installationID)}`,
    );
    return { source: "live", installation };
  } catch (error) {
    if (error instanceof ControlPlaneRequestError && error.status === 404) {
      return { source: "live" };
    }
    return {
      source: "unavailable",
      detail: error instanceof Error ? error.message : "The exact provider connection could not be loaded.",
    };
  }
}

// The work queue has its own server-side query because runs shown here are an
// operational inbox, not an arbitrary slice of review history. Keeping its
// cursors out of getConsoleData prevents the small navigation summary request
// from deciding which failures an operator can see.
export async function getWorkQueueData(
  org: string,
  options: {
    view: WorkQueueView;
    repository?: string;
    query?: string;
    cursor?: string;
    direction?: "after" | "before";
    limit?: number;
  },
): Promise<WorkQueueData> {
  const limit = options.limit ?? 25;
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    const query = options.query?.trim().toLocaleLowerCase();
    const runs = demoRuns
      .filter((run) =>
        options.view === "running"
          ? [
              "acknowledged",
              "admitted",
              "preparing",
              "analyzing",
              "normalizing",
              "publishing",
            ].includes(run.state)
          : run.state === "failed" || run.state === "needs_attention",
      )
      .filter(
        (run) => !options.repository || run.repository === options.repository,
      )
      .filter(
        (run) =>
          !query ||
          [
            run.repository,
            String(run.review_number),
            run.title,
            run.author,
            run.failure_message,
          ]
            .filter(Boolean)
            .some((value) => value!.toLocaleLowerCase().includes(query)),
      );
    return {
      source: "demo",
      runs: runs.slice(0, limit),
      counts: {
        running: demoRuns.filter((run) =>
          [
            "acknowledged",
            "admitted",
            "preparing",
            "analyzing",
            "normalizing",
            "publishing",
          ].includes(run.state),
        ).length,
        needs_attention: demoRuns.filter(
          (run) => run.state === "failed" || run.state === "needs_attention",
        ).length,
      },
    };
  }
  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) {
    return {
      source: "unconfigured",
      runs: [],
      counts: { running: 0, needs_attention: 0 },
      detail:
        "Sign in and configure CONTROL_API_URL before opening the workspace work queue.",
    };
  }
  const query = new URLSearchParams({
    view: options.view,
    limit: String(limit),
  });
  if (options.repository?.trim())
    query.set("repository", options.repository.trim());
  if (options.query?.trim()) query.set("q", options.query.trim());
  if (options.cursor?.trim()) query.set("cursor", options.cursor.trim());
  if (options.direction) query.set("cursor_direction", options.direction);
  try {
    const page = await request<{
      runs: ReviewRun[];
      counts: WorkQueueData["counts"];
      next_cursor?: string;
      previous_cursor?: string;
    }>(
      configuration,
      `/v1/tenants/${encodeURIComponent(org)}/work-queue?${query.toString()}`,
    );
    return {
      source: "live",
      runs: page.runs,
      counts: page.counts,
      nextCursor: page.next_cursor,
      previousCursor: page.previous_cursor,
    };
  } catch (error) {
    return {
      source: "unavailable",
      runs: [],
      counts: { running: 0, needs_attention: 0 },
      detail:
        error instanceof Error
          ? error.message
          : "The work queue could not be loaded.",
    };
  }
}

export async function getReviewScheduleData(
  org: string,
): Promise<ReviewScheduleData> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    return {
      source: "demo",
      schedules: structuredClone(demoReviewSchedules),
      detail:
        "Preview schedules are read-only examples of delayed, coalesced, and blocked admissions; no runner capacity or provider action is reserved.",
    };
  }
  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) {
    return {
      source: "unconfigured",
      schedules: [],
      detail:
        "Sign in and configure CONTROL_API_URL before opening scheduled admissions.",
    };
  }
  try {
    const response = await request<{ schedules: ReviewSchedule[] }>(
      configuration,
      `/v1/tenants/${encodeURIComponent(org)}/review-schedules?limit=50`,
    );
    return { source: "live", schedules: response.schedules };
  } catch (error) {
    return {
      source: "unavailable",
      schedules: [],
      detail:
        error instanceof Error
          ? error.message
          : "Scheduled admissions could not be loaded.",
    };
  }
}

export async function getReviewInterventionData(
  org: string,
  options: { runID?: string; activeOnly?: boolean; limit?: number } = {},
): Promise<ReviewInterventionData> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    const interventions = demoRuns
      .flatMap((run) => (run.intervention ? [{ ...run.intervention }] : []))
      .filter(
        (intervention) =>
          !options.runID || intervention.run_id === options.runID,
      )
      .filter(
        (intervention) =>
          !options.activeOnly || intervention.state !== "resolved",
      )
      .slice(0, options.limit ?? 50);
    return {
      source: "demo",
      interventions,
      detail: "Preview data only. Claim and acknowledgement are disabled.",
    };
  }
  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) {
    return {
      source: "unconfigured",
      interventions: [],
      detail:
        "Sign in and configure CONTROL_API_URL before managing review interventions.",
    };
  }
  const query = new URLSearchParams({
    active_only: String(options.activeOnly ?? true),
    limit: String(options.limit ?? 50),
  });
  if (options.runID) query.set("run_id", options.runID);
  try {
    const response = await request<{ interventions: ReviewIntervention[] }>(
      configuration,
      `/v1/tenants/${encodeURIComponent(org)}/review-interventions?${query.toString()}`,
    );
    return { source: "live", interventions: response.interventions };
  } catch (error) {
    return {
      source: "unavailable",
      interventions: [],
      detail:
        error instanceof Error
          ? error.message
          : "Review interventions could not be loaded.",
    };
  }
}

export async function getPullRequestData(
  org: string,
  options: {
    view: PullRequestView;
    repository?: string;
    query?: string;
    cursor?: string;
    direction?: "after" | "before";
    limit?: number;
  },
): Promise<PullRequestData> {
  const limit = options.limit ?? 25;
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    const latest = [
      ...new Map(
        demoRuns.map((run) => [
          `${run.provider}:${run.api_base_url ?? ""}:${run.repository}:${run.review_number}`,
          run,
        ]),
      ).values(),
    ];
    const matches = (run: ReviewRun) =>
      options.view === "all" ||
      (options.view === "active" &&
        [
          "acknowledged",
          "admitted",
          "preparing",
          "analyzing",
          "normalizing",
          "publishing",
        ].includes(run.state)) ||
      (options.view === "attention" &&
        (run.state === "failed" || run.state === "needs_attention")) ||
      (options.view === "completed" &&
        ["completed", "cancelled", "superseded"].includes(run.state));
    return {
      source: "demo",
      runs: latest.filter(matches).slice(0, limit),
      counts: {
        active: latest.filter((run) =>
          [
            "acknowledged",
            "admitted",
            "preparing",
            "analyzing",
            "normalizing",
            "publishing",
          ].includes(run.state),
        ).length,
        attention: latest.filter(
          (run) => run.state === "failed" || run.state === "needs_attention",
        ).length,
        completed: latest.filter((run) =>
          ["completed", "cancelled", "superseded"].includes(run.state),
        ).length,
        all: latest.length,
      },
    };
  }
  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) {
    return {
      source: "unconfigured",
      runs: [],
      counts: { active: 0, attention: 0, completed: 0, all: 0 },
      detail:
        "Sign in and configure CONTROL_API_URL before opening pull requests.",
    };
  }
  const query = new URLSearchParams({
    view: options.view,
    limit: String(limit),
  });
  if (options.repository?.trim())
    query.set("repository", options.repository.trim());
  if (options.query?.trim()) query.set("q", options.query.trim());
  if (options.cursor?.trim()) query.set("cursor", options.cursor.trim());
  if (options.direction) query.set("cursor_direction", options.direction);
  try {
    const page = await request<{
      runs: ReviewRun[];
      counts: PullRequestData["counts"];
      next_cursor?: string;
      previous_cursor?: string;
    }>(
      configuration,
      `/v1/tenants/${encodeURIComponent(org)}/pull-requests?${query.toString()}`,
    );
    return {
      source: "live",
      runs: page.runs,
      counts: page.counts,
      nextCursor: page.next_cursor,
      previousCursor: page.previous_cursor,
    };
  } catch (error) {
    return {
      source: "unavailable",
      runs: [],
      counts: { active: 0, attention: 0, completed: 0, all: 0 },
      detail:
        error instanceof Error
          ? error.message
          : "Pull requests could not be loaded.",
    };
  }
}

// Profiles are read from the control plane rather than frontend environment
// variables, keeping an offline deployment's trusted GitLab endpoint aligned
// with the API that will actually record the installation.
function demoProviderProfile(
  provider: ProviderProfile["provider"],
  configuredAPIBaseURL: string | undefined,
  publicAPIBaseURL: string,
  publicHost: string,
  publicLabel: string,
): ProviderProfile {
  const candidate = configuredAPIBaseURL?.trim() || publicAPIBaseURL;
  try {
    const endpoint = new URL(candidate);
    if (
      endpoint.protocol !== "https:" ||
      !endpoint.hostname ||
      endpoint.username ||
      endpoint.password ||
      endpoint.search ||
      endpoint.hash
    ) {
      throw new Error("untrusted provider endpoint");
    }
    endpoint.pathname = endpoint.pathname.replace(/\/+$/, "") || "/";
    const apiBaseURL = endpoint.toString().replace(/\/$/, "");
    if (endpoint.hostname === publicHost) {
      return {
        provider,
        api_base_url: apiBaseURL,
        mode: "cloud",
        label: publicLabel,
        deployment_token_available: false,
      };
    }
    const product = provider === "github" ? "GitHub" : "GitLab";
    return {
      provider,
      api_base_url: apiBaseURL,
      mode: "self_managed",
      label: `${product} self-managed · ${endpoint.hostname}`,
      deployment_token_available: false,
    };
  } catch {
    // A visual preview must not turn an invalid deployment value into a
    // browser-selectable endpoint. The live control plane remains authority.
    return {
      provider,
      api_base_url: publicAPIBaseURL,
      mode: "cloud",
      label: publicLabel,
      deployment_token_available: false,
    };
  }
}

export async function getProviderProfiles(
  org: string,
): Promise<ProviderProfileData> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    return {
      source: "demo",
      profiles: [
        demoProviderProfile(
          "github",
          process.env.GITHUB_API_URL,
          "https://api.github.com",
          "api.github.com",
          "GitHub.com",
        ),
        demoProviderProfile(
          "gitlab",
          process.env.GITLAB_API_URL,
          "https://gitlab.com/api/v4",
          "gitlab.com",
          "GitLab.com",
        ),
      ],
    };
  }
  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) {
    return {
      source: "unconfigured",
      profiles: [],
      detail:
        "Sign in and configure the control plane before viewing deployment provider profiles.",
    };
  }
  try {
    const response = await request<{ profiles: ProviderProfile[] }>(
      configuration,
      `/v1/tenants/${encodeURIComponent(org)}/provider-profiles`,
    );
    return { source: "live", profiles: response.profiles };
  } catch (error) {
    return {
      source: "unavailable",
      profiles: [],
      detail:
        error instanceof Error
          ? error.message
          : "Deployment provider profiles could not be loaded.",
    };
  }
}

// Webhook receipts are accepted-delivery evidence only. The control plane never
// serializes raw payloads, callback signatures, or provider delivery IDs here.
export async function getInstallationWebhookReceipts(
  org: string,
  installationID: string,
): Promise<InstallationWebhookReceiptData> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    return {
      source: "demo",
      receipts: structuredClone(
        demoInstallationWebhookReceipts[installationID] ?? [],
      ),
      detail:
        "Preview receipts are safe delivery summaries; raw callback payloads and secrets are never retained here.",
    };
  }
  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) {
    return {
      source: "unconfigured",
      receipts: [],
      detail:
        "Sign in and configure the control plane before viewing webhook delivery evidence.",
    };
  }
  try {
    const response = await request<{
      receipts: InstallationWebhookReceipt[];
    }>(
      configuration,
      `/v1/tenants/${encodeURIComponent(org)}/installations/${encodeURIComponent(installationID)}/webhook-receipts?limit=25`,
    );
    return { source: "live", receipts: response.receipts };
  } catch (error) {
    return {
      source: "unavailable",
      receipts: [],
      detail:
        error instanceof Error
          ? error.message
          : "Webhook delivery evidence could not be loaded.",
    };
  }
}

// Provider repository metadata is synchronized only by the worker-side
// credential boundary. This read model deliberately exposes no provider token
// or raw provider response to the console.
export async function getInstallationRepositories(
  org: string,
  installationID: string,
): Promise<InstallationRepositoryData> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    return {
      source: "demo",
      repositories: structuredClone(
        demoInstallationRepositories[installationID] ?? [],
      ),
      detail:
        "Preview inventory only. Repository scope changes require a live provider inventory.",
    };
  }
  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) {
    return {
      source: "unconfigured",
      repositories: [],
      detail:
        "Sign in and configure the control plane before viewing provider repository inventory.",
    };
  }
  try {
    const response = await request<{ repositories: ProviderRepository[] }>(
      configuration,
      `/v1/tenants/${encodeURIComponent(org)}/installations/${encodeURIComponent(installationID)}/repositories?limit=500`,
    );
    return { source: "live", repositories: response.repositories };
  } catch (error) {
    return {
      source: "unavailable",
      repositories: [],
      detail:
        error instanceof Error
          ? error.message
          : "Provider repository inventory could not be loaded.",
    };
  }
}

export async function getCLIReviewData(org: string): Promise<CLIReviewData> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    return {
      source: "demo",
      runs: demoRuns
        .filter((run) => run.trigger_kind === "cli")
        .map((run) => ({ ...run, caller_subject: "cli-key:demo-security" })),
      installations: demoInstallations,
      detail:
        "Preview data only. CLI commands and review mutations are disabled.",
    };
  }

  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) {
    return {
      source: "unconfigured",
      runs: [],
      installations: [],
      detail:
        "Sign in and configure the control plane before using CLI reviews.",
    };
  }

  try {
    const [runs, installations] = await Promise.all([
      request<{ runs: CLIReviewRun[] }>(
        configuration,
        `/v1/tenants/${encodeURIComponent(org)}/cli-reviews?limit=100`,
      ),
      request<{ installations: ProviderInstallation[] }>(
        configuration,
        `/v1/tenants/${encodeURIComponent(org)}/installations?limit=100`,
      ),
    ]);
    return {
      source: "live",
      runs: runs.runs,
      installations: installations.installations,
    };
  } catch (error) {
    return {
      source: "unavailable",
      runs: [],
      installations: [],
      detail:
        error instanceof Error
          ? error.message
          : "CLI review data could not be loaded.",
    };
  }
}

export async function getIssueInboxData(
  org: string,
  options: {
    view?: IssueView;
    filters?: IssueFilterGroup;
    filterTime?: string;
    assignedToMe?: boolean;
    status?: IssueStatus;
    severity?: ReviewIssue["severity"];
    repository?: string;
    category?: string;
    query?: string;
    activeOnly?: boolean;
    seenAfter?: string;
    cursor?: string;
    cursorDirection?: "after" | "before";
    selectedIssueID?: string;
    limit?: number;
  } = {},
): Promise<IssueInboxData> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    if (options.filters?.items.length)
      return {
        source: "unavailable",
        issues: [],
        counts: demoIssueCounts(),
        detail:
          "Grouped filters require live issue data; no preview result has been inferred.",
      };
    const issues = filterDemoIssues(options);
    return {
      source: "demo",
      issues,
      facets: {
        repositories: [...new Set(demoIssues.map((issue) => issue.repository))].sort(),
        categories: [...new Set(demoIssues.map((issue) => issue.category))].sort(),
      },
      counts: demoIssueCounts(),
      selectedInView: options.selectedIssueID ? issues.some((issue) => issue.id === options.selectedIssueID) : undefined,
      detail:
        "Preview data only. External Issue actions and policy changes are disabled.",
    };
  }

  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) {
    return {
      source: "unconfigured",
      issues: [],
      counts: {
        open: 0,
        regressed: 0,
        critical: 0,
        assigned: 0,
        resolved: 0,
        suppressed: 0,
      },
      detail:
        "Sign in and configure the control plane before viewing issue aggregates.",
    };
  }

  try {
    const query = new URLSearchParams({ limit: String(options.limit ?? 25) });
    if (options.view) query.set("view", options.view);
    if (options.filters?.items.length)
      query.set("filters", JSON.stringify(options.filters));
    if (options.filterTime) query.set("filter_time", options.filterTime);
    if (options.assignedToMe) query.set("assignee", "me");
    if (options.status) query.set("status", options.status);
    if (options.severity) query.set("severity", options.severity);
    if (options.repository) query.set("repository", options.repository);
    if (options.category) query.set("category", options.category);
    if (options.query) query.set("q", options.query);
    if (options.activeOnly) query.set("active", "true");
    if (options.seenAfter) query.set("seen_after", options.seenAfter);
    if (options.cursor) query.set("cursor", options.cursor);
    if (options.cursorDirection)
      query.set("cursor_direction", options.cursorDirection);
    if (options.selectedIssueID) query.set("selected", options.selectedIssueID);
    const response = await request<{
      issues: ReviewIssue[];
      counts?: IssueInboxData["counts"];
      facets?: IssueInboxData["facets"];
      next_cursor?: string;
      previous_cursor?: string;
      filter_time?: string;
      total_count?: number;
      selected_in_view?: boolean;
    }>(
      configuration,
      `/v1/tenants/${encodeURIComponent(org)}/issues?${query.toString()}`,
    );
    if (!response.counts || !Array.isArray(response.issues))
      throw new Error(
        "Issue aggregates returned incomplete counts or records. No empty result was inferred.",
      );
    if (response.facets && (!Array.isArray(response.facets.repositories) || !Array.isArray(response.facets.categories)))
      throw new Error("Issue filter options are incomplete. Refresh the inbox before applying a filter.");
    return {
      source: "live",
      issues: response.issues,
      facets: response.facets,
      counts: response.counts ?? {
        open: 0,
        regressed: 0,
        critical: 0,
        assigned: 0,
        resolved: 0,
        suppressed: 0,
      },
      nextCursor: response.next_cursor,
      previousCursor: response.previous_cursor,
      filterTime: response.filter_time,
      totalCount: response.total_count,
      selectedInView: response.selected_in_view,
    };
  } catch (error) {
    return {
      source: "unavailable",
      issues: [],
      counts: {
        open: 0,
        regressed: 0,
        critical: 0,
        assigned: 0,
        resolved: 0,
        suppressed: 0,
      },
      detail:
        error instanceof Error
          ? error.message
          : "Issue aggregates could not be loaded.",
    };
  }
}

export async function getIssueViewsData(org: string): Promise<IssueViewsData> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true")
    return {
      source: "demo",
      views: [],
      canCreateWorkspace: false,
      detail:
        "Saved views and grouped filters require a live workspace. Preview data is read-only.",
    };
  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration)
    return {
      source: "unconfigured",
      views: [],
      canCreateWorkspace: false,
      detail: "Sign in to load your saved views.",
    };
  try {
    const result = await request<{
      views: IssueSavedView[];
      can_create_workspace: boolean;
    }>(configuration, `/v1/tenants/${encodeURIComponent(org)}/issue-views`);
    if (!Array.isArray(result.views))
      throw new Error("Saved views returned an invalid response.");
    result.views.forEach((view) =>
      validateIssueFilters(view.definition.filters),
    );
    return {
      source: "live",
      views: result.views,
      canCreateWorkspace: result.can_create_workspace,
    };
  } catch (error) {
    return {
      source: "unavailable",
      views: [],
      canCreateWorkspace: false,
      detail:
        error instanceof Error
          ? error.message
          : "Saved views could not be loaded. Your issue filters are unchanged.",
    };
  }
}

export async function getProviderIssueAnalysisData(
  org: string,
  options: {
    state?: ProviderIssueAnalysisFilterState;
    query?: string;
    selected?: string;
  } = {},
): Promise<ProviderIssueAnalysisData> {
  const emptyCounts = { queued: 0, acknowledged: 0, completed: 0, failed: 0, delivery_failures: 0, skipped_deliveries: 0, connection_blocked: 0, needs_attention: 0 };
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    return {
      source: "demo",
      items: [],
      counts: emptyCounts,
      detail:
        "Provider Issue triage requires live GitHub or GitLab webhook evidence.",
    };
  }
  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) {
    return {
      source: "unconfigured",
      items: [],
      counts: emptyCounts,
      detail:
        "Sign in and configure the control plane before viewing Provider Issue triage.",
    };
  }
  try {
    const query = new URLSearchParams({ limit: "100" });
    if (options.state) query.set("state", options.state);
    if (options.query) query.set("q", options.query);
    const [page, selected] = await Promise.all([
      request<{
        items: ProviderIssueAnalysis[];
        counts: ProviderIssueAnalysisData["counts"];
      }>(
        configuration,
        `/v1/tenants/${encodeURIComponent(org)}/provider-issues?${query.toString()}`,
      ),
      options.selected
        ? optionalRequest<ProviderIssueAnalysisDetail>(
            configuration,
            `/v1/tenants/${encodeURIComponent(org)}/provider-issues/${encodeURIComponent(options.selected)}`,
          )
        : Promise.resolve(undefined),
    ]);
    return {
      source: "live",
      items: page.items,
      counts: page.counts ?? emptyCounts,
      selected,
    };
  } catch (error) {
    return {
      source: "unavailable",
      items: [],
      counts: emptyCounts,
      detail:
        error instanceof Error
          ? error.message
          : "Provider Issue triage could not be loaded.",
    };
  }
}

function demoIssueCounts(): IssueInboxData["counts"] {
  return {
    open: demoIssues.filter((issue) => issue.status === "open").length,
    regressed: demoIssues.filter((issue) => issue.status === "regressed")
      .length,
    critical: demoIssues.filter(
      (issue) =>
        issue.severity === "critical" && (issue.status === "open" || issue.status === "regressed") && issue.active_occurrence_count > 0,
    ).length,
    assigned: demoIssues.filter((issue) => Boolean(issue.assignee_subject) && (issue.status === "open" || issue.status === "regressed") && issue.active_occurrence_count > 0)
      .length,
    resolved: demoIssues.filter((issue) => issue.status === "resolved").length,
    suppressed: demoIssues.filter((issue) => issue.status === "suppressed")
      .length,
  };
}

function filterDemoIssues(
  options: NonNullable<Parameters<typeof getIssueInboxData>[1]>,
) {
  const query = options.query?.trim().toLocaleLowerCase();
  return demoIssues
    .filter((issue) => !options.assignedToMe || Boolean(issue.assignee_subject))
    .filter((issue) => !options.status || issue.status === options.status)
    .filter((issue) => !options.severity || issue.severity === options.severity)
    .filter(
      (issue) => !options.repository || issue.repository === options.repository,
    )
    .filter((issue) => !options.category || issue.category === options.category)
    .filter((issue) => !options.activeOnly || issue.active_occurrence_count > 0)
    .filter(
      (issue) =>
        !options.seenAfter ||
        new Date(issue.last_seen_at) >= new Date(options.seenAfter),
    )
    .filter(
      (issue) =>
        !query ||
        [
          issue.body_preview,
          issue.category,
          issue.fingerprint,
          issue.path,
          issue.repository,
        ].some((value) => value.toLocaleLowerCase().includes(query)),
    )
    .sort(
      (left, right) =>
        new Date(right.last_seen_at).getTime() -
        new Date(left.last_seen_at).getTime(),
    )
    .slice(0, options.limit ?? 25)
    .map((issue) => ({ ...issue }));
}

function demoIssueDetail(issueID: string): IssueDetail | undefined {
  const issue = demoIssues.find((candidate) => candidate.id === issueID);
  if (!issue) return undefined;
  const occurrences: IssueOccurrence[] =
    issue.id === "issue-demo-auth-boundary"
      ? [
          {
            id: "occurrence-demo-auth-current",
            finding_id: "finding-demo-auth-current",
            run_id: "run-demo-auth-current",
            review_number: 42,
            head_sha: "f14a2f85a88a5c9f2ddfa2451140dd2458d952ce",
            path: issue.path,
            start_line: 118,
            end_line: 122,
            severity: issue.severity,
            category: issue.category,
            body: "The privileged endpoint accepts a token without asserting that its audience belongs to this control plane. A token minted for another audience could cross the authorization boundary.",
            suggestion:
              "Require the configured audience and reject tokens with an empty or mismatched aud claim before evaluating role permissions.",
            active: true,
            created_at: "2026-09-19T10:42:00Z",
          },
          {
            id: "occurrence-demo-auth-prior",
            finding_id: "finding-demo-auth-prior",
            run_id: "run-demo-auth-prior",
            review_number: 39,
            head_sha: "5d8fa2eb8b5ee2dc60f9c546a1d77ff2e55aa3a1",
            path: issue.path,
            start_line: 116,
            end_line: 120,
            severity: issue.severity,
            category: issue.category,
            body: "The same authorization invariant was previously violated on the token refresh path.",
            active: false,
            created_at: "2026-09-17T13:20:00Z",
          },
        ]
      : [
          {
            id: `occurrence-${issue.id}`,
            finding_id: `finding-${issue.id}`,
            run_id: `run-${issue.id}`,
            review_number: issue.id === "issue-demo-retry-contract" ? 87 : 24,
            head_sha:
              issue.id === "issue-demo-retry-contract"
                ? "65c0c4bcd7f62cf2b6b7c50f757dfb1aa9a18e4e"
                : "8d15b21ed1f114bdb2cc2b3c50309fefac8c0a7c",
            path: issue.path,
            start_line: issue.id === "issue-demo-retry-contract" ? 74 : 51,
            end_line: issue.id === "issue-demo-retry-contract" ? 81 : 55,
            severity: issue.severity,
            category: issue.category,
            body: issue.body_preview,
            suggestion:
              "Use the retained receipt as the idempotency boundary before retrying the provider request.",
            active: issue.active_occurrence_count > 0,
            created_at: issue.last_seen_at,
          },
        ];
  const events: IssueDetail["events"] = [
    {
      id: `event-created-${issue.id}`,
      revision: 1,
      actor_subject: "review-engine",
      action: "reopened",
      previous_status: "resolved",
      next_status: issue.status === "regressed" ? "regressed" : "open",
      reason:
        "A stable finding fingerprint was observed in a new review revision.",
      created_at: issue.first_seen_at,
    },
  ];
  if (issue.assignee_subject) {
    events.unshift({
      id: `event-assigned-${issue.id}`,
      revision: issue.revision,
      actor_subject: "demo-workspace-owner",
      action: "assigned",
      previous_status: issue.status,
      next_status: issue.status,
      next_assignee: issue.assignee_subject,
      created_at: issue.last_seen_at,
    });
  }
  return {
    ...issue,
    occurrences,
    events,
    can_manage: false,
    external_issue:
      issue.provider === "github"
        ? {
            id: `external-${issue.id}`,
            issue_id: issue.id,
            provider: "github",
            repository: issue.repository,
            trigger: "first_seen",
            state: "created",
            external_id: "142",
            external_url:
              "https://github.com/RainLib/open-review-platform/issues/142",
            attempts: 1,
            created_at: issue.first_seen_at,
            updated_at: issue.last_seen_at,
          }
        : undefined,
  };
}

export async function getIssueDetailData(
  org: string,
  issueID: string,
): Promise<IssueDetailData> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    const issue = demoIssueDetail(issueID);
    return {
      source: "demo",
      issue,
      detail: issue
        ? "Preview evidence only. It is not a persisted review record."
        : "This preview does not include the requested Issue.",
    };
  }

  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) {
    return {
      source: "unconfigured",
      detail:
        "Sign in and configure the control plane before viewing issue evidence.",
    };
  }

  try {
    const issue = await optionalRequest<IssueDetail>(
      configuration,
      `/v1/tenants/${encodeURIComponent(org)}/issues/${encodeURIComponent(issueID)}`,
    );
    return issue
      ? { source: "live", issue }
      : { source: "live", detail: "Issue not found." };
  } catch (error) {
    return {
      source: "unavailable",
      detail:
        error instanceof Error
          ? error.message
          : "Issue evidence could not be loaded.",
    };
  }
}

export async function getIssueAutoCreatePolicyData(
  org: string,
): Promise<IssueAutoCreatePolicyData> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    return {
      source: "demo",
      policy: structuredClone(demoIssueAutoCreatePolicy),
      detail:
        "Preview policy is read-only. No dry run, policy save, or provider Issue creation is available.",
    };
  }
  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) {
    return {
      source: "unconfigured",
      detail:
        "Sign in and configure the control plane before changing external Issue automation.",
    };
  }
  try {
    const policy = await request<IssueAutoCreatePolicy>(
      configuration,
      `/v1/tenants/${encodeURIComponent(org)}/issues/auto-create-policy`,
    );
    return { source: "live", policy };
  } catch (error) {
    return {
      source: "unavailable",
      detail:
        error instanceof Error
          ? error.message
          : "External Issue automation could not be loaded.",
    };
  }
}

export async function getNotificationData(
  org: string,
): Promise<NotificationData> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    return {
      source: "demo",
      ...structuredClone(demoNotificationData),
      detail:
        "Preview data only. Destinations, routing, tests, and retries are disabled.",
    };
  }
  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) {
    return {
      source: "unconfigured",
      destinations: [],
      routes: [],
      deliveries: [],
      detail:
        "Sign in and configure the control plane before managing notifications.",
    };
  }

  try {
    const [destinations, routes, deliveries] = await Promise.all([
      request<{ notification_destinations: NotificationDestination[] }>(
        configuration,
        `/v1/tenants/${encodeURIComponent(org)}/notification-destinations`,
      ),
      request<{ notification_routes: NotificationRoute[] }>(
        configuration,
        `/v1/tenants/${encodeURIComponent(org)}/notification-routes`,
      ),
      request<{ notification_deliveries: NotificationDelivery[] }>(
        configuration,
        `/v1/tenants/${encodeURIComponent(org)}/notification-deliveries?limit=50`,
      ),
    ]);
    return {
      source: "live",
      destinations: destinations.notification_destinations,
      routes: routes.notification_routes,
      deliveries: deliveries.notification_deliveries,
    };
  } catch (error) {
    return {
      source: "unavailable",
      destinations: [],
      routes: [],
      deliveries: [],
      detail:
        error instanceof Error
          ? error.message
          : "Notification configuration could not be loaded.",
    };
  }
}

export async function getAuditData(
  org: string,
  filter: { actor?: string; action?: string; target?: string; from?: string; until?: string; before?: string } = {},
): Promise<AuditData> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    const actor = filter.actor?.trim().toLowerCase();
    const action = filter.action?.trim().toLowerCase();
    const target = filter.target?.trim().toLowerCase();
    const filtered = demoAuditEvents.filter(
      (event) =>
        (!actor || event.actor_subject.toLowerCase().includes(actor)) &&
        (!action || event.action.toLowerCase().startsWith(action)) &&
        (!target || event.target.toLowerCase().includes(target)) &&
        (!filter.from || event.created_at >= filter.from) &&
        (!filter.until || event.created_at < filter.until),
    );
    const beforeIndex = filter.before
      ? filtered.findIndex((event) => event.id === filter.before) + 1
      : 0;
    return {
      source: "demo",
      events: filter.before && beforeIndex === 0 ? [] : filtered.slice(beforeIndex),
      detail: "Preview data only. It is not a persisted audit record.",
    };
  }
  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) {
    return {
      source: "unconfigured",
      events: [],
      detail:
        "Sign in and configure the control plane before viewing audit history.",
    };
  }
  const query = new URLSearchParams({ limit: "50" });
  if (filter.actor) query.set("actor", filter.actor);
  if (filter.action) query.set("action", filter.action);
  if (filter.target) query.set("target", filter.target);
  if (filter.from) query.set("from", filter.from);
  if (filter.until) query.set("until", filter.until);
  if (filter.before) query.set("before", filter.before);
  try {
    const response = await request<{ audit_events: AuditEvent[]; next_cursor?: string }>(
      configuration,
      `/v1/tenants/${encodeURIComponent(org)}/audit-events?${query.toString()}`,
    );
    return { source: "live", events: response.audit_events, nextCursor: response.next_cursor };
  } catch (error) {
    return {
      source: "unavailable",
      events: [],
      detail:
        error instanceof Error
          ? error.message
          : "Audit history could not be loaded.",
    };
  }
}

export async function getAuditEvent(org: string, eventId: string): Promise<AuditDetailData> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    return { source: "demo", event: demoAuditEvents.find((event) => event.id === eventId), detail: "Preview data only." };
  }
  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) {
    return { source: "unconfigured", detail: "Connect the control plane before viewing audit evidence." };
  }
  try {
    const response = await request<{ audit_event: AuditEvent }>(
      configuration,
      `/v1/tenants/${encodeURIComponent(org)}/audit-events/${encodeURIComponent(eventId)}`,
    );
    return { source: "live", event: response.audit_event };
  } catch (error) {
    return { source: "unavailable", detail: error instanceof Error ? error.message : "Audit event could not be loaded." };
  }
}

export async function getMemberData(org: string): Promise<MemberData> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    return {
      source: "demo",
      members: structuredClone(demoMembers),
      invitations: structuredClone(demoInvitations),
      accessRequests: [],
      actorSubject: "platform.owner",
      detail:
        "Preview membership data only. Invitations, role changes, and activation changes are disabled.",
    };
  }
  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) {
    return {
      source: "unconfigured",
      members: [],
      invitations: [],
      accessRequests: [],
      detail:
        "Sign in and configure the control plane before managing members.",
    };
  }
  try {
    const [members, invitations, accessRequests, actor] = await Promise.all([
      request<{ members: WorkspaceMember[] }>(
        configuration,
        `/v1/tenants/${encodeURIComponent(org)}/members?limit=100`,
      ),
      request<{ invitations: WorkspaceInvitation[] }>(
        configuration,
        `/v1/tenants/${encodeURIComponent(org)}/invitations`,
      ),
      request<{ requests: WorkspaceAccessRequest[] }>(
        configuration,
        `/v1/tenants/${encodeURIComponent(org)}/access-requests`,
      ),
      request<{ subject: string }>(configuration, "/v1/me"),
    ]);
    return {
      source: "live",
      members: members.members,
      invitations: invitations.invitations,
      accessRequests: accessRequests.requests,
      actorSubject: actor.subject,
    };
  } catch (error) {
    return {
      source: "unavailable",
      members: [],
      invitations: [],
      accessRequests: [],
      detail:
        error instanceof Error ? error.message : "Members could not be loaded.",
    };
  }
}

export async function getAPIKeyData(org: string): Promise<APIKeyData> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    return {
      source: "demo",
      apiKeys: structuredClone(demoAPIKeys),
      detail:
        "Preview key records only. Secret creation and lifecycle changes are disabled.",
    };
  }
  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) {
    return {
      source: "unconfigured",
      apiKeys: [],
      detail:
        "Sign in and configure the control plane before managing API keys.",
    };
  }
  try {
    const response = await request<{ api_keys: WorkspaceAPIKey[] }>(
      configuration,
      `/v1/tenants/${encodeURIComponent(org)}/api-keys?limit=100`,
    );
    return { source: "live", apiKeys: response.api_keys };
  } catch (error) {
    return {
      source: "unavailable",
      apiKeys: [],
      detail:
        error instanceof Error
          ? error.message
          : "API keys could not be loaded.",
    };
  }
}

export async function getSSOData(org: string): Promise<SSOData> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    return {
      source: "demo",
      ...structuredClone(demoSSOOverview),
      detail:
        "Preview identity data only. SSO configuration, probes, domains, mappings, and enforcement are disabled.",
    };
  }
  const empty: SSOOverview = {
    domains: [],
    mappings: [],
    probes: [],
    readiness: { ready: false, blockers: ["identity_provider_not_configured"] },
  };
  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) {
    return {
      source: "unconfigured",
      ...empty,
      detail:
        "Sign in and configure the control plane before managing workspace SSO.",
    };
  }
  try {
    const overview = await request<SSOOverview>(
      configuration,
      `/v1/tenants/${encodeURIComponent(org)}/sso`,
    );
    return { source: "live", ...overview };
  } catch (error) {
    return {
      source: "unavailable",
      ...empty,
      detail:
        error instanceof Error
          ? error.message
          : "SSO configuration could not be loaded.",
    };
  }
}

export async function getDataGovernanceData(
  org: string,
): Promise<DataGovernanceData> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    return {
      source: "demo",
      ...structuredClone(demoDataGovernanceOverview),
      detail: "Preview data only. Governance changes and exports are disabled.",
    };
  }
  const empty: DataGovernanceOverview = {
    residency: { configured: false, model_boundary: "external/unknown" },
    boundaries: [],
    policies: [],
    legal_holds: [],
    jobs: [],
  };
  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) {
    return {
      source: "unconfigured",
      ...empty,
      detail:
        "Sign in and configure the control plane before managing workspace data.",
    };
  }
  try {
    const overview = await request<DataGovernanceOverview>(
      configuration,
      `/v1/tenants/${encodeURIComponent(org)}/data-governance`,
    );
    return { source: "live", ...overview };
  } catch (error) {
    return {
      source: "unavailable",
      ...empty,
      detail:
        error instanceof Error
          ? error.message
          : "Data governance could not be loaded.",
    };
  }
}

export async function getPlatformHealthData(
  org: string,
): Promise<PlatformHealthData> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    return {
      source: "demo",
      ...structuredClone(demoPlatformHealthOverview),
      detail:
        "Preview data only. It does not represent deployed service health.",
    };
  }
  const empty: PlatformHealthOverview = {
    sampled_at: new Date(0).toISOString(),
    freshness_window_seconds: 300,
    components: [],
    queues: [],
    gitlab_author_admissions: [],
    workers: [],
    agent_executor: {
      state: "unobserved",
      detail: "Agent runner configuration is unavailable from this session.",
    },
    agent_decision: {
      state: "unobserved",
      detail: "Agent source worker configuration is unavailable from this session.",
    },
    agent_credential_broker: {
      state: "unobserved",
      detail: "Private coding-credential broker status is unavailable from this session.",
    },
    providers: [],
    incidents: [],
    incident_tracking_available: false,
    worker_heartbeat_available: false,
    broker_metrics_available: false,
    runbooks: [],
  };
  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) {
    return {
      source: "unconfigured",
      ...empty,
      detail:
        "Sign in and configure the control plane before viewing platform health.",
    };
  }
  try {
    const overview = await request<PlatformHealthOverview>(
      configuration,
      `/v1/tenants/${encodeURIComponent(org)}/platform-health`,
    );
    return { source: "live", ...overview };
  } catch (error) {
    return {
      source: "unavailable",
      ...empty,
      detail:
        error instanceof Error
          ? error.message
          : "Platform health could not be sampled.",
    };
  }
}

export async function getRuleApprovalData(
  org: string,
): Promise<RuleApprovalData> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    return {
      source: "demo",
      approvals: structuredClone(demoRuleApprovals),
      detail:
        "Preview approval evidence is read-only; no decision or publication is sent to a provider.",
    };
  }
  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) {
    return {
      source: "unconfigured",
      approvals: [],
      detail:
        "Sign in and configure the control plane before reviewing policy changes.",
    };
  }
  try {
    const response = await request<{ approval_requests: RuleApproval[] }>(
      configuration,
      `/v1/tenants/${encodeURIComponent(org)}/rule-approval-requests?limit=100`,
    );
    return { source: "live", approvals: response.approval_requests };
  } catch (error) {
    return {
      source: "unavailable",
      approvals: [],
      detail:
        error instanceof Error
          ? error.message
          : "Policy approvals could not be loaded.",
    };
  }
}

export async function getRuleCatalog(org: string): Promise<RuleCatalogData> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    return {
      source: "demo",
      entries: structuredClone(demoRuleCatalog),
      detail:
        "Preview catalog entries are release-pinned examples and cannot be installed.",
    };
  }
  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) {
    return {
      source: "unconfigured",
      entries: [],
      detail:
        "Sign in and configure the control plane before browsing release-pinned policy templates.",
    };
  }
  try {
    const response = await request<{ entries: RuleCatalogEntry[] }>(
      configuration,
      `/v1/tenants/${encodeURIComponent(org)}/rule-catalog`,
    );
    return { source: "live", entries: response.entries };
  } catch (error) {
    return {
      source: "unavailable",
      entries: [],
      detail:
        error instanceof Error
          ? error.message
          : "The release-pinned policy catalog could not be loaded.",
    };
  }
}

export async function getRuleBindingData(
  org: string,
): Promise<RuleBindingData> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    return {
      source: "demo",
      bindings: structuredClone(demoRuleBindings),
      rollouts: [],
      ruleSets: structuredClone(demoRuleSets),
      detail:
        "Preview rollout bindings are immutable examples; promotion and disable actions are unavailable.",
    };
  }
  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) {
    return {
      source: "unconfigured",
      bindings: [],
      rollouts: [],
      ruleSets: [],
      detail:
        "Sign in and configure the control plane before managing policy rollout.",
    };
  }
  try {
    const [bindings, ruleSets, rollouts] = await Promise.all([
      request<{ rule_bindings: RuleBinding[] }>(
        configuration,
        `/v1/tenants/${encodeURIComponent(org)}/rule-bindings?limit=100`,
      ),
      request<{ rule_sets: RuleSet[] }>(
        configuration,
        `/v1/tenants/${encodeURIComponent(org)}/rule-sets?limit=100`,
      ),
      request<{ rule_rollouts: RuleRollout[] }>(
        configuration,
        `/v1/tenants/${encodeURIComponent(org)}/rule-rollouts?limit=100`,
      ),
    ]);
    return {
      source: "live",
      bindings: bindings.rule_bindings,
      rollouts: rollouts.rule_rollouts,
      ruleSets: ruleSets.rule_sets,
    };
  } catch (error) {
    return {
      source: "unavailable",
      bindings: [],
      rollouts: [],
      ruleSets: [],
      detail:
        error instanceof Error
          ? error.message
          : "Policy bindings could not be loaded.",
    };
  }
}

export async function getRuleExceptionData(
  org: string,
): Promise<RuleExceptionData> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    return {
      source: "demo",
      exceptions: structuredClone(demoRuleExceptions),
      ruleSets: structuredClone(demoRuleSets),
      detail:
        "Preview exception records are read-only and do not create a future admission exception.",
    };
  }
  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) {
    return {
      source: "unconfigured",
      exceptions: [],
      ruleSets: [],
      detail:
        "Sign in and configure the control plane before managing policy exceptions.",
    };
  }
  try {
    const [exceptions, ruleSets] = await Promise.all([
      request<{ exceptions: RuleException[] }>(
        configuration,
        `/v1/tenants/${encodeURIComponent(org)}/rule-exceptions?limit=100`,
      ),
      request<{ rule_sets: RuleSet[] }>(
        configuration,
        `/v1/tenants/${encodeURIComponent(org)}/rule-sets?limit=100`,
      ),
    ]);
    return {
      source: "live",
      exceptions: exceptions.exceptions,
      ruleSets: ruleSets.rule_sets,
    };
  } catch (error) {
    return {
      source: "unavailable",
      exceptions: [],
      ruleSets: [],
      detail:
        error instanceof Error
          ? error.message
          : "Policy exceptions could not be loaded.",
    };
  }
}

export async function getFindingFeedbackDashboard(
  org: string,
): Promise<FindingFeedbackDashboard> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    return {
      source: "demo",
      finding_count: 18,
      feedback_count: 11,
      useful_finding_count: 8,
      false_positive_count: 2,
      resolved_finding_count: 6,
      wont_fix_finding_count: 1,
      repositories: [
        {
          repository: "RainLib/open-review-platform",
          finding_count: 12,
          feedback_count: 8,
          useful_finding_count: 6,
          false_positive_count: 1,
          resolved_finding_count: 5,
          wont_fix_finding_count: 1,
          high_risk_finding_count: 3,
          distinct_snapshot_count: 4,
        },
        {
          repository: "platform/agent-harness",
          finding_count: 6,
          feedback_count: 3,
          useful_finding_count: 2,
          false_positive_count: 1,
          resolved_finding_count: 1,
          wont_fix_finding_count: 0,
          high_risk_finding_count: 1,
          distinct_snapshot_count: 2,
        },
      ],
      recent_findings: [
        {
          id: "finding-feedback-demo-auth",
          provider: "github",
          api_base_url: "https://api.github.com",
          repository: "RainLib/open-review-platform",
          review_number: 42,
          path: "internal/api/auth.go",
          start_line: 118,
          end_line: 122,
          severity: "critical",
          category: "Security",
          body_preview:
            "The authorization boundary requires an explicit audience check before privileged role evaluation.",
          actor_disposition: "resolved",
          useful_count: 3,
          false_positive_count: 0,
          created_at: "2026-09-19T10:42:00Z",
        },
        {
          id: "finding-feedback-demo-retry",
          provider: "gitlab",
          api_base_url: "https://gitlab.example.internal",
          repository: "platform/agent-harness",
          review_number: 118,
          path: "src/publish/receipt.ts",
          start_line: 88,
          end_line: 94,
          severity: "high",
          category: "Reliability",
          body_preview:
            "A timeout after remote acceptance can publish a duplicate review summary without a stable receipt lookup.",
          useful_count: 1,
          false_positive_count: 1,
          created_at: "2026-09-17T14:25:17Z",
        },
      ],
      attribution_warning:
        "Preview fixture: rule attribution is retained only when the immutable run snapshot contains the matching rule version.",
      detail: "Preview data only. Finding dispositions are disabled.",
    };
  }
  const configuration = await getControlPlaneRequestConfiguration();
  const empty = {
    finding_count: 0,
    feedback_count: 0,
    useful_finding_count: 0,
    false_positive_count: 0,
    resolved_finding_count: 0,
    wont_fix_finding_count: 0,
    repositories: [],
    recent_findings: [],
    attribution_warning: "",
  };
  if (!configuration) {
    return {
      source: "unconfigured",
      ...empty,
      detail:
        "Sign in and configure the control plane before viewing feedback evidence.",
    };
  }
  try {
    const dashboard = await request<Omit<FindingFeedbackDashboard, "source">>(
      configuration,
      `/v1/tenants/${encodeURIComponent(org)}/finding-feedback?limit=50`,
    );
    return { source: "live", ...dashboard };
  } catch (error) {
    return {
      source: "unavailable",
      ...empty,
      detail:
        error instanceof Error
          ? error.message
          : "Finding feedback could not be loaded.",
    };
  }
}

export async function getFindingExplorerPage(
  org: string,
  filter: FindingExplorerFilter,
): Promise<FindingExplorerPage> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    const dashboard = await getFindingFeedbackDashboard(org);
    return {
      source: "demo",
      findings: dashboard.recent_findings.filter((finding) => {
        if (filter.repository && finding.repository !== filter.repository) return false;
        if (filter.query && !`${finding.repository} ${finding.path} ${finding.category} ${finding.body_preview}`.toLowerCase().includes(filter.query.toLowerCase())) return false;
        if (filter.view === "actioned") return Boolean(finding.actor_disposition);
        if (finding.actor_disposition) return false;
        return filter.view !== "high-risk" || finding.severity === "high" || finding.severity === "critical";
      }),
      detail: "Preview records only; no cross-run pagination or mutation is available.",
    };
  }
  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) {
    return { source: "unconfigured", findings: [], detail: "Sign in and configure the control plane to explore findings." };
  }
  const params = new URLSearchParams({ view: filter.view, limit: "25" });
  if (filter.repository) params.set("repository", filter.repository);
  if (filter.query) params.set("q", filter.query);
  if (filter.cursor) params.set("cursor", filter.cursor);
  if (filter.direction) params.set("cursor_direction", filter.direction);
  try {
    const page = await request<Omit<FindingExplorerPage, "source" | "detail">>(
      configuration,
      `/v1/tenants/${encodeURIComponent(org)}/findings?${params.toString()}`,
    );
    return { source: "live", ...page };
  } catch (error) {
    return {
      source: "unavailable",
      findings: [],
      detail: error instanceof Error ? error.message : "Findings could not be loaded.",
    };
  }
}

export async function getUsageDashboard(org: string): Promise<UsageDashboard> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    return {
      source: "demo",
      ...structuredClone(demoUsageDashboard),
      detail:
        "Preview usage data only. Entitlement changes are disabled and no billing service is configured.",
    };
  }
  const empty: Omit<UsageDashboard, "source"> = {
    period_start: new Date().toISOString(),
    period_end: new Date().toISOString(),
    entitlement: {
      monthly_review_limit: 0,
      soft_warning_percent: 80,
      updated_at: new Date(0).toISOString(),
    },
    settled: 0,
    reserved: 0,
    warning: false,
    can_manage: false,
    reconciliation: {
      state: "unavailable",
      period_start: new Date().toISOString(),
      period_end: new Date().toISOString(),
      scanned_runs: 0,
      drifted_runs: 0,
      missing_reservations: 0,
      state_mismatches: 0,
      missing_ledger_proofs: 0,
    },
    repositories: [],
    ledger: [],
  };
  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) {
    return {
      source: "unconfigured",
      ...empty,
      detail: "Sign in and configure the control plane before viewing usage.",
    };
  }
  try {
    const dashboard = await request<Omit<UsageDashboard, "source">>(
      configuration,
      `/v1/tenants/${encodeURIComponent(org)}/usage?limit=50`,
    );
    return { source: "live", ...dashboard };
  } catch (error) {
    return {
      source: "unavailable",
      ...empty,
      detail:
        error instanceof Error ? error.message : "Usage could not be loaded.",
    };
  }
}

async function optionalRequest<T>(
  configuration: ControlPlaneRequestConfiguration,
  path: string,
): Promise<T | undefined> {
  const response = await fetch(`${configuration.baseURL}${path}`, {
    cache: "no-store",
    headers: configuration.headers,
  });
  if (response.status === 404) return undefined;
  if (!response.ok)
    throw new Error(`Control plane returned ${response.status}`);
  return response.json() as Promise<T>;
}

export async function getRunDetail(
  org: string,
  runID: string,
): Promise<RunDetailData> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    const run = demoRuns.find((candidate) => candidate.id === runID);
    return {
      source: "demo",
      runs: demoRuns,
      ruleSets: demoRuleSets,
      installations: demoInstallations,
      run,
      ruleSnapshot: run
        ? {
            id: "snapshot-demo-042",
            sha256:
              "8c59b208d0ece6da835e7700fa9a3bb9012e49105a14d2e34f7ee870768a10cf",
            compiler_version: "rules-v1",
            engine: "open-code-review",
            sources: [
              {
                rule_version_id: "version-demo-1",
                rule_set_id: "rule-auth-boundary",
                version: 4,
                precedence: 100,
              },
            ],
            exceptions: [],
            created_at: "2026-09-18T02:04:02Z",
          }
        : undefined,
    };
  }

  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) {
    return {
      source: "unconfigured",
      runs: [],
      ruleSets: [],
      installations: [],
      detail:
        "Sign in and configure CONTROL_API_URL before connecting this workspace to the control plane.",
    };
  }

  try {
    const run = await request<ReviewRun>(
      configuration,
      `/v1/tenants/${encodeURIComponent(org)}/runs/${encodeURIComponent(runID)}`,
    );
    const ruleSnapshot = await optionalRequest<RuleSnapshot>(
      configuration,
      `/v1/tenants/${encodeURIComponent(org)}/runs/${encodeURIComponent(runID)}/rule-snapshot`,
    );
    return {
      source: "live",
      runs: [],
      ruleSets: [],
      installations: [],
      run,
      ruleSnapshot,
    };
  } catch (error) {
    return {
      source: "unavailable",
      runs: [],
      ruleSets: [],
      installations: [],
      detail:
        error instanceof Error
          ? error.message
          : "The control plane could not be reached.",
    };
  }
}

export async function getReviewEvidenceData(
  org: string,
  runID: string,
): Promise<ReviewEvidenceData> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    const run = demoRuns.find((candidate) => candidate.id === runID);
    return run
      ? {
          source: "demo",
          evidence: demoReviewEvidence(run),
          ruleSnapshot:
            run.id === "demo-080c"
              ? {
                  id: "snapshot-demo-042",
                  sha256:
                    "8c59b208d0ece6da835e7700fa9a3bb9012e49105a14d2e34f7ee870768a10cf",
                  compiler_version: "preview-v1",
                  engine: "open-code-review",
                  sources: [
                    {
                      rule_version_id: "rule-version-demo-receipts",
                      rule_set_id: "rule-release-evidence",
                      version: 3,
                      precedence: 100,
                    },
                  ],
                  exceptions: [],
                  created_at: "2026-09-17T14:22:04Z",
                }
              : undefined,
          detail: "Preview evidence only. It is not a persisted review record.",
        }
      : { source: "demo", detail: "The demo review run was not found." };
  }

  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) {
    return {
      source: "unconfigured",
      detail:
        "Sign in and configure the control plane before viewing review evidence.",
    };
  }

  try {
    const [evidence, ruleSnapshot] = await Promise.all([
      request<ReviewEvidence>(
        configuration,
        `/v1/tenants/${encodeURIComponent(org)}/runs/${encodeURIComponent(runID)}/evidence`,
      ),
      optionalRequest<RuleSnapshot>(
        configuration,
        `/v1/tenants/${encodeURIComponent(org)}/runs/${encodeURIComponent(runID)}/rule-snapshot`,
      ),
    ]);
    return { source: "live", evidence, ruleSnapshot };
  } catch (error) {
    return {
      source: "unavailable",
      detail:
        error instanceof Error
          ? error.message
          : "The review evidence could not be loaded.",
    };
  }
}

function demoReviewEvidence(run: ReviewRun): ReviewEvidence {
  const isCompletedFindingDemo = run.id === "demo-080c";
  const findings: ReviewFindingEvidence[] = isCompletedFindingDemo
    ? [
        {
          id: "finding-demo-080c-retry",
          path: "src/publish/receipt.ts",
          start_line: 89,
          end_line: 89,
          severity: "high",
          category: "Reliability",
          body: "The retry path can create a second provider summary when a timeout occurs after the remote service accepted the first request.",
          suggestion:
            "Persist and look up the stable provider marker before publishing a retry so a remote success is reused rather than duplicated.",
          code_excerpt: "const marker = receipt.marker;\nawait provider.createSummary(comment);",
          code_excerpt_start_line: 88,
          proposed_patch: "diff --git a/src/publish/receipt.ts b/src/publish/receipt.ts\n--- a/src/publish/receipt.ts\n+++ b/src/publish/receipt.ts\n@@ -88,2 +88,3 @@\n const marker = receipt.marker;\n-await provider.createSummary(comment);\n+const existing = await provider.findSummary(marker);\n+if (!existing) await provider.createSummary(comment);\n",
          fingerprint: "provider-receipt-idempotency",
          provider_marker: "open-review:demo-080c:summary",
          useful_feedback_count: 2,
          false_positive_feedback_count: 0,
          rule_attributions: [],
          created_at: run.finished_at ?? run.created_at,
        },
      ]
    : [];
  const stages: ReviewRunStageEvidence[] = [
    "ack",
    "admit",
    "prepare",
    "analyze",
    "normalize",
    "publish",
  ].map((stage, index) => ({
    id: `stage-${run.id}-${stage}`,
    stage: stage as ReviewRunStageEvidence["stage"],
    state:
      run.state === "analyzing" && stage === "analyze"
        ? "running"
        : run.state === "needs_attention" && stage === "publish"
          ? "failed"
          : index < 5 || run.state === "completed"
            ? "succeeded"
            : "pending",
    attempt: 0,
    started_at: run.started_at ?? run.created_at,
    finished_at:
      run.state === "completed" ||
      (run.state === "needs_attention" && stage === "publish")
        ? run.finished_at
        : undefined,
    details: {},
  }));
  const receipts: PublicationReceiptEvidence[] =
    run.state === "completed"
      ? [
          {
            id: `receipt-${run.id}-status`,
            provider: run.provider,
            receipt_kind: "status",
            stable_marker: `open-review:${run.id}:status`,
            external_id:
              run.provider === "github"
                ? "check-run-demo"
                : "commit-status-demo",
            payload_hash:
              "ef40e1a47e4f1e7ef917a6c6d0f804ad2510e1eaf74c56d1595b0b847dc5f7d2",
            published_at: run.finished_at,
            created_at: run.finished_at ?? run.created_at,
            updated_at: run.finished_at ?? run.created_at,
          },
        ]
      : [];
  return {
    run,
    acknowledgement_recovery_available: false,
    related_runs: demoRuns.filter(
      (candidate) =>
        candidate.repository === run.repository &&
        candidate.review_number === run.review_number,
    ),
    execution_attempts: 1,
    execution_plan: {
      mode: isCompletedFindingDemo ? "critical" : "focused",
      selected_paths: isCompletedFindingDemo
        ? ["src/publish/receipt.ts", "src/publish/marker.ts"]
        : ["internal/api/webhook.go"],
      deferred_files: isCompletedFindingDemo ? 8 : 3,
      static_impact_signals: isCompletedFindingDemo
        ? [
            "public boundary or asynchronous workflow",
            "persistent state or financial side effect",
          ]
        : ["public boundary or asynchronous workflow"],
      file_scopes: isCompletedFindingDemo
        ? [
            { path: "src/publish/receipt.ts", selected: true, score: 95, reasons: ["public boundary or asynchronous workflow", "persistent state or financial side effect"] },
            { path: "src/publish/marker.ts", selected: true, score: 70, reasons: ["persistent state or financial side effect"] },
            ...[
              "docs/release-notes.md",
              "docs/retry-plan.md",
              "tests/receipt.spec.ts",
              "src/generated/provider.ts",
              "pnpm-lock.yaml",
              "README.md",
              "src/publish/legacy.ts",
              "src/ui/receipt.tsx",
            ].map((path, index) => ({
              path,
              selected: false,
              score: index < 2 || index === 5 ? 10 : index === 3 || index === 4 ? 0 : 45,
              reasons: index < 2 || index === 5
                ? ["documentation-only change"]
                : index === 3 || index === 4
                  ? ["generated, vendored, fixture, coverage, or lock artifact"]
                  : ["focused-mode budget: deferred after the diverse high-signal files"],
            })),
          ]
        : [
            { path: "internal/api/webhook.go", selected: true, score: 70, reasons: ["public boundary or asynchronous workflow"] },
            ...["docs/guide.md", "tests/webhook.spec.ts", "README.md"].map((path) => ({
              path,
              selected: false,
              score: 10,
              reasons: ["documentation-only or test-only path"],
            })),
          ],
    },
    merge_gate:
      run.state === "completed"
        ? {
            enabled: true,
            threshold: "critical",
            conclusion: "success",
            blocking_findings: 0,
            finding_count: findings.length,
            configuration_content_sha256:
              "ce8c03f5324dbe1c041432a93bd1c2f7990df3f312a2898822277d2d47d79a99",
            origin_scope_kind: "tenant",
            origin_revision: 7,
            evaluation_version: "preview-v1",
            decided_at: run.finished_at ?? run.created_at,
          }
        : undefined,
    findings,
    stages,
    receipts,
    configuration_snapshot: [],
    events: getDemoRunEvents(run.id),
  };
}

export async function getReviewConfigData(
  org: string,
  section: ReviewConfigSection,
  scopeKind: "tenant" | "repository",
  scopeRef = "",
  scopeProvider = "",
  scopeAPIBaseURL = "",
): Promise<ReviewConfigData> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    return {
      source: "demo",
      config: demoReviewConfigView(section, scopeKind, scopeRef),
      detail:
        "Read-only fixture data for visual review. Save and history remain unavailable until the live control plane is connected.",
    };
  }
  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) {
    return {
      source: "unconfigured",
      detail:
        "Sign in and configure the control plane before editing review settings.",
    };
  }
  const query = new URLSearchParams({ scope_kind: scopeKind });
  if (scopeRef) query.set("scope_ref", scopeRef);
  if (scopeProvider) query.set("scope_provider", scopeProvider);
  if (scopeAPIBaseURL) query.set("scope_api_base_url", scopeAPIBaseURL);
  try {
    const config = await request<ReviewConfigView>(
      configuration,
      `/v1/tenants/${encodeURIComponent(org)}/review-config/${encodeURIComponent(section)}?${query.toString()}`,
    );
    return { source: "live", config };
  } catch (error) {
    return {
      source: "unavailable",
      detail:
        error instanceof Error
          ? error.message
          : "Review settings could not be loaded.",
    };
  }
}

export async function getIssueFormatTemplates(
  org: string,
): Promise<IssueFormatTemplateData> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    return {
      source: "demo",
      templates: [],
      detail:
        "Reusable workspace formats are read-only and empty in the visual preview.",
    };
  }
  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) {
    return {
      source: "unconfigured",
      templates: [],
      detail:
        "Sign in and configure the control plane before managing reusable Issue formats.",
    };
  }
  try {
    const response = await request<{ templates: IssueFormatTemplate[] }>(
      configuration,
      `/v1/tenants/${encodeURIComponent(org)}/issue-format-templates`,
    );
    return { source: "live", templates: response.templates ?? [] };
  } catch (error) {
    return {
      source: "unavailable",
      templates: [],
      detail:
        error instanceof Error
          ? error.message
          : "Reusable Issue formats could not be loaded.",
    };
  }
}

export async function getAgentTaskData(
  org: string,
  selectedTaskID?: string,
  taskCursor?: string,
): Promise<AgentTaskData> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    return {
      source: "demo",
      installations: [],
      policies: [],
      tasks: [],
      taskCursor,
      selectedTaskRequested: Boolean(selectedTaskID),
      availability: { installations: false, policies: false, tasks: false, selected: !selectedTaskID },
      detail:
        "Agent work is unavailable in the visual preview because it requires a verified provider installation.",
    };
  }
  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) {
    return {
      source: "unconfigured",
      installations: [],
      policies: [],
      tasks: [],
      taskCursor,
      selectedTaskRequested: Boolean(selectedTaskID),
      availability: { installations: false, policies: false, tasks: false, selected: !selectedTaskID },
      detail:
        "Sign in and configure the control plane before managing Agent work.",
    };
  }
  try {
    const selectedRequest = selectedTaskID
      ? request<AgentTaskDetail>(
          configuration,
          `/v1/tenants/${encodeURIComponent(org)}/agent-tasks/${encodeURIComponent(selectedTaskID)}`,
        )
      : Promise.resolve(undefined);
    const [policies, tasks, installations, selected] = await Promise.allSettled([
      request<{ agent_task_policies: AgentTaskPolicy[] }>(
        configuration,
        `/v1/tenants/${encodeURIComponent(org)}/agent-task-policies?limit=100`,
      ),
      request<{ agent_tasks: AgentTask[]; next_cursor?: string }>(
        configuration,
        `/v1/tenants/${encodeURIComponent(org)}/agent-tasks?limit=100${taskCursor ? `&cursor=${encodeURIComponent(taskCursor)}` : ""}`,
      ),
      request<{ installations: ProviderInstallation[] }>(
        configuration,
        `/v1/tenants/${encodeURIComponent(org)}/installations?limit=100`,
      ),
      selectedRequest,
    ]);
    // Provider inventory, policy and task reads have independent recovery
    // boundaries. Preserve successful sections when one dependency degrades.
    return assembleAgentTaskData({ policies, tasks, installations, selected }, Boolean(selectedTaskID), taskCursor);
  } catch (error) {
    return {
      source: "unavailable",
      installations: [],
      policies: [],
      tasks: [],
      taskCursor,
      selectedTaskRequested: Boolean(selectedTaskID),
      availability: { installations: false, policies: false, tasks: false, selected: !selectedTaskID },
      detail:
        error instanceof Error
          ? error.message
          : "Agent work could not be loaded.",
    };
  }
}

// The Agent overview is intentionally bounded to 100 policies. A selected
// task must read its exact repository policy instead of inheriting another
// repository's readiness from that overview.
export async function getAgentTaskPolicyData(
  org: string,
  provider: AgentTaskPolicy["provider"],
  apiBaseURL: string,
  repository: string,
): Promise<{ source: DataSource; policy?: AgentTaskPolicy; detail?: string }> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") return { source: "demo" };
  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration) return { source: "unconfigured" };
  const query = new URLSearchParams({ provider, api_base_url: apiBaseURL, repository });
  try {
    const response = await request<{ agent_task_policies: AgentTaskPolicy[] }>(
      configuration,
      `/v1/tenants/${encodeURIComponent(org)}/agent-task-policies?${query}`,
    );
    if (!Array.isArray(response.agent_task_policies) || response.agent_task_policies.length > 1 ||
        response.agent_task_policies.some((policy) => policy.provider !== provider ||
          policy.api_base_url.replace(/\/$/, "") !== apiBaseURL.replace(/\/$/, "") ||
          policy.repository !== repository)) {
      return { source: "unavailable", detail: "Exact repository policy response is invalid." };
    }
    return { source: "live", policy: response.agent_task_policies[0] };
  } catch (error) {
    return { source: "unavailable", detail: error instanceof Error ? error.message : "Exact repository policy could not be loaded." };
  }
}

export async function getReviewConfigHistory(
  org: string,
  section: ReviewConfigSection,
  scopeKind: "tenant" | "repository",
  scopeRef = "",
  scopeProvider = "",
  scopeAPIBaseURL = "",
): Promise<{
  source: DataSource;
  history?: ReviewConfigHistory;
  detail?: string;
}> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    return {
      source: "demo",
      history: demoReviewConfigHistory(section, scopeKind, scopeRef),
      detail:
        "Preview provenance is fixture metadata only; previous policy content and secrets are never displayed.",
    };
  }
  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration)
    return {
      source: "unconfigured",
      detail:
        "Sign in and configure the control plane before viewing configuration history.",
    };
  const query = new URLSearchParams({ scope_kind: scopeKind });
  if (scopeRef) query.set("scope_ref", scopeRef);
  if (scopeProvider) query.set("scope_provider", scopeProvider);
  if (scopeAPIBaseURL) query.set("scope_api_base_url", scopeAPIBaseURL);
  try {
    const history = await request<ReviewConfigHistory>(
      configuration,
      `/v1/tenants/${encodeURIComponent(org)}/review-config/${encodeURIComponent(section)}/history?${query.toString()}`,
    );
    return { source: "live", history };
  } catch (error) {
    return {
      source: "unavailable",
      detail:
        error instanceof Error
          ? error.message
          : "Review configuration history could not be loaded.",
    };
  }
}

export async function getModelProbeData(
  org: string,
  scopeKind: "tenant" | "repository",
  scopeRef = "",
  scopeProvider = "",
  scopeAPIBaseURL = "",
): Promise<ModelProbeData> {
  if (process.env.OPEN_REVIEW_CONSOLE_DEMO === "true") {
    return {
      source: "demo",
      probes: [],
      detail:
        "Connectivity probes are disabled in the read-only demo. Connect the live control plane to request or inspect durable probe receipts.",
    };
  }
  const configuration = await getControlPlaneRequestConfiguration();
  if (!configuration)
    return {
      source: "unconfigured",
      probes: [],
      detail:
        "Sign in and configure the control plane before viewing model probe history.",
    };
  const query = new URLSearchParams({ scope_kind: scopeKind, limit: "20" });
  if (scopeRef) query.set("scope_ref", scopeRef);
  if (scopeProvider) query.set("scope_provider", scopeProvider);
  if (scopeAPIBaseURL) query.set("scope_api_base_url", scopeAPIBaseURL);
  try {
    const response = await request<{ probes: ModelProbeReceipt[] }>(
      configuration,
      `/v1/tenants/${encodeURIComponent(org)}/model-probes?${query.toString()}`,
    );
    return { source: "live", probes: response.probes };
  } catch (error) {
    return {
      source: "unavailable",
      probes: [],
      detail:
        error instanceof Error
          ? error.message
          : "Model probe history could not be loaded.",
    };
  }
}

export function getDemoRunEvents(runID: string) {
  return demoEvents[runID] ?? [];
}
