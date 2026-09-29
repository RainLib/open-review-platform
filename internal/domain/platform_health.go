package domain

import (
	"time"

	"github.com/google/uuid"
)

type HealthState string

const (
	HealthLive           HealthState = "live"
	HealthDegraded       HealthState = "degraded"
	HealthCritical       HealthState = "critical"
	HealthStale          HealthState = "stale"
	HealthConfiguredOnly HealthState = "configured_only"
)

type HealthComponent struct {
	Key          string      `json:"key"`
	Name         string      `json:"name"`
	State        HealthState `json:"state"`
	Detail       string      `json:"detail"`
	Observed     bool        `json:"observed"`
	SampledAt    *time.Time  `json:"sampled_at,omitempty"`
	Capability   string      `json:"capability"`
	RecoveryHint string      `json:"recovery_hint"`
}

type QueueHealth struct {
	Key              string      `json:"key"`
	Name             string      `json:"name"`
	Source           string      `json:"source"`
	State            HealthState `json:"state"`
	Ready            int64       `json:"ready"`
	Running          int64       `json:"running"`
	Failed           int64       `json:"failed"`
	ExpiredLeases    int64       `json:"expired_leases"`
	OldestAgeSeconds int64       `json:"oldest_age_seconds"`
	SampledAt        time.Time   `json:"sampled_at"`
	Detail           string      `json:"detail"`
}

// GitLabAuthorAdmissionHealthItem exposes only tenant-owned, actionable
// metadata. The retained webhook payload and provider credential stay private.
type GitLabAuthorAdmissionHealthItem struct {
	DeliveryID     uuid.UUID `json:"delivery_id"`
	InstallationID uuid.UUID `json:"installation_id"`
	Repository     string    `json:"repository"`
	ReviewNumber   int       `json:"review_number"`
	APIBaseURL     string    `json:"api_base_url"`
	HeadSHA        string    `json:"head_sha"`
	State          string    `json:"state"`
	Attempt        int       `json:"attempt"`
	ErrorCode      string    `json:"error_code"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type WorkerHealth struct {
	WorkerID      string      `json:"worker_id"`
	Kind          string      `json:"kind"`
	State         HealthState `json:"state"`
	ActiveLeases  int64       `json:"active_leases"`
	ExpiredLeases int64       `json:"expired_leases"`
	LastActivity  *time.Time  `json:"last_activity,omitempty"`
	LastHeartbeat *time.Time  `json:"last_heartbeat,omitempty"`
	Version       string      `json:"version,omitempty"`
	Capacity      int         `json:"capacity"`
	Busy          int         `json:"busy"`
	Detail        string      `json:"detail"`
}

type WorkerHeartbeat struct {
	WorkerID           string
	Kind               string
	Version            string
	AdapterConfigured  bool
	AdapterProbeAt     *time.Time
	AdapterReachable   *bool
	DecisionConfigured bool
	Capacity           int
	Busy               int
	StartedAt          time.Time
	HeartbeatAt        time.Time
	ExpiresAt          time.Time
}

type AgentExecutorReadiness struct {
	State  string `json:"state"`
	Detail string `json:"detail"`
}

type AgentDecisionReadiness struct {
	State  string `json:"state"`
	Detail string `json:"detail"`
}

type AgentCredentialBrokerReadiness struct {
	State  string `json:"state"`
	Detail string `json:"detail"`
}

type ProviderHealth struct {
	InstallationID     uuid.UUID   `json:"installation_id"`
	Provider           Provider    `json:"provider"`
	Host               string      `json:"host"`
	RepositoryScope    string      `json:"repository_scope"`
	Active             bool        `json:"active"`
	State              HealthState `json:"state"`
	LastWebhookAt      *time.Time  `json:"last_webhook_at,omitempty"`
	LastPublishAt      *time.Time  `json:"last_publish_at,omitempty"`
	PublishFailures    int64       `json:"publish_failures"`
	LastProbeAt        *time.Time  `json:"last_probe_at,omitempty"`
	Permissions        []string    `json:"permissions"`
	RateLimitRemaining *int64      `json:"rate_limit_remaining,omitempty"`
	RateLimitLimit     *int64      `json:"rate_limit_limit,omitempty"`
	RateLimitResetAt   *time.Time  `json:"rate_limit_reset_at,omitempty"`
	ProbeLatencyMS     int64       `json:"probe_latency_ms"`
	Detail             string      `json:"detail"`
}

type ProviderProbeTarget struct {
	Installation Installation
	Attempt      int
	WorkerID     string
}

type ProviderProbeResult struct {
	HealthState         HealthState
	ObservedAt          time.Time
	CredentialExpiresAt *time.Time
	Permissions         []string
	RateLimitRemaining  *int64
	RateLimitLimit      *int64
	RateLimitResetAt    *time.Time
	LatencyMS           int64
	ErrorCode           string
	ErrorMessage        string
	Receipt             map[string]any
	Repositories        []ProviderRepository
}

type PlatformIncident struct {
	ID           uuid.UUID             `json:"id"`
	Title        string                `json:"title"`
	State        PlatformIncidentState `json:"state"`
	Scope        string                `json:"scope"`
	StartedAt    time.Time             `json:"started_at"`
	ResolvedAt   *time.Time            `json:"resolved_at,omitempty"`
	ResolvedBy   string                `json:"resolved_by,omitempty"`
	Resolution   string                `json:"resolution,omitempty"`
	AffectedArea string                `json:"affected_area"`
	CreatedBy    string                `json:"created_by,omitempty"`
	Revision     int                   `json:"revision"`
	UpdatedAt    time.Time             `json:"updated_at"`
}

type PlatformRunbook struct {
	Key          string   `json:"key"`
	Title        string   `json:"title"`
	Version      string   `json:"version"`
	RequiredRole string   `json:"required_role"`
	Steps        []string `json:"steps"`
	Executable   bool     `json:"executable"`
	Detail       string   `json:"detail"`
}

type PlatformHealthOverview struct {
	SampledAt                 time.Time                         `json:"sampled_at"`
	FreshnessWindowSeconds    int64                             `json:"freshness_window_seconds"`
	Components                []HealthComponent                 `json:"components"`
	Queues                    []QueueHealth                     `json:"queues"`
	GitLabAuthorAdmissions    []GitLabAuthorAdmissionHealthItem `json:"gitlab_author_admissions"`
	Workers                   []WorkerHealth                    `json:"workers"`
	AgentExecutor             AgentExecutorReadiness            `json:"agent_executor"`
	AgentDecision             AgentDecisionReadiness            `json:"agent_decision"`
	AgentCredentialBroker     AgentCredentialBrokerReadiness    `json:"agent_credential_broker"`
	Providers                 []ProviderHealth                  `json:"providers"`
	Incidents                 []PlatformIncident                `json:"incidents"`
	IncidentTrackingAvailable bool                              `json:"incident_tracking_available"`
	WorkerHeartbeatAvailable  bool                              `json:"worker_heartbeat_available"`
	BrokerMetricsAvailable    bool                              `json:"broker_metrics_available"`
	Runbooks                  []PlatformRunbook                 `json:"runbooks"`
}
