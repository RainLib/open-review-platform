package domain

import (
	"time"

	"github.com/google/uuid"
)

type NotificationProvider string

const (
	NotificationDingTalk NotificationProvider = "dingtalk"
	NotificationFeishu   NotificationProvider = "feishu"
	NotificationSlack    NotificationProvider = "slack"
	NotificationWebhook  NotificationProvider = "webhook"
)

func (p NotificationProvider) Valid() bool {
	return p == NotificationDingTalk || p == NotificationFeishu || p == NotificationSlack || p == NotificationWebhook
}

type NotificationDestination struct {
	ID        uuid.UUID            `json:"id"`
	TenantID  uuid.UUID            `json:"tenant_id"`
	Name      string               `json:"name"`
	Provider  NotificationProvider `json:"provider"`
	SecretRef string               `json:"-"`
	Enabled   bool                 `json:"enabled"`
	Revision  int                  `json:"revision"`
	CreatedAt time.Time            `json:"created_at"`
	UpdatedAt time.Time            `json:"updated_at"`
}

type NotificationDestinationInput struct {
	Name      string               `json:"name"`
	Provider  NotificationProvider `json:"provider"`
	SecretRef string               `json:"secret_ref"`
	Enabled   bool                 `json:"enabled"`
}

type NotificationDestinationUpdateInput struct {
	Name             *string `json:"name,omitempty"`
	SecretRef        *string `json:"secret_ref,omitempty"`
	Enabled          *bool   `json:"enabled,omitempty"`
	ExpectedRevision int     `json:"expected_revision"`
}

// NotificationTestInput pins a test request to the destination configuration
// that the administrator reviewed. The worker refuses a test if that revision
// is no longer active when it reaches the queue.
type NotificationTestInput struct {
	ExpectedRevision int `json:"expected_revision"`
}

type NotificationRoute struct {
	ID             uuid.UUID `json:"id"`
	TenantID       uuid.UUID `json:"tenant_id"`
	DestinationID  uuid.UUID `json:"destination_id"`
	RepositoryGlob string    `json:"repository_glob"`
	BranchGlob     string    `json:"branch_glob"`
	EventTypes     []string  `json:"event_types"`
	MinSeverity    string    `json:"min_severity"`
	Enabled        bool      `json:"enabled"`
	// Priority is tenant-local and defines which overlapping route wins for a
	// destination. Lower values are evaluated first.
	Priority  int       `json:"priority"`
	Revision  int       `json:"revision"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type NotificationRouteInput struct {
	DestinationID  uuid.UUID `json:"destination_id"`
	RepositoryGlob string    `json:"repository_glob"`
	BranchGlob     string    `json:"branch_glob"`
	EventTypes     []string  `json:"event_types"`
	MinSeverity    string    `json:"min_severity"`
	Enabled        bool      `json:"enabled"`
}

type NotificationRouteUpdateInput struct {
	RepositoryGlob   *string   `json:"repository_glob,omitempty"`
	BranchGlob       *string   `json:"branch_glob,omitempty"`
	EventTypes       *[]string `json:"event_types,omitempty"`
	MinSeverity      *string   `json:"min_severity,omitempty"`
	Enabled          *bool     `json:"enabled,omitempty"`
	ExpectedRevision int       `json:"expected_revision"`
}

// NotificationRouteOrderItem is an optimistic-concurrency entry for the
// complete tenant route order. Reordering must submit every route so a stale
// browser cannot silently move a subset around routes it has not observed.
type NotificationRouteOrderItem struct {
	ID               uuid.UUID `json:"id"`
	ExpectedRevision int       `json:"expected_revision"`
}

type NotificationRouteReorderInput struct {
	Routes []NotificationRouteOrderItem `json:"routes"`
}

// NotificationRoutePreviewInput describes a terminal review event without
// admitting it to the outbox. It is intentionally limited to fields already
// used by notification routing, so the preview cannot turn into a provider
// send or a secret-resolution path.
type NotificationRoutePreviewInput struct {
	Repository      string `json:"repository"`
	TargetBranch    string `json:"target_branch"`
	EventType       string `json:"event_type"`
	HighestSeverity string `json:"highest_severity"`
	FindingCount    int    `json:"finding_count"`
}

type NotificationRoutePreview struct {
	Input   NotificationRoutePreviewInput   `json:"input"`
	Matches []NotificationRoutePreviewMatch `json:"matches"`
}

// Disposition is selected, filtered, or deduplicated. A destination receives
// at most one delivery for an event; the first eligible route in the explicit
// tenant priority order selects it and later eligible routes are reported as deduplicated.
type NotificationRoutePreviewMatch struct {
	RouteID         uuid.UUID `json:"route_id"`
	DestinationID   uuid.UUID `json:"destination_id"`
	DestinationName string    `json:"destination_name"`
	Disposition     string    `json:"disposition"`
	Reason          string    `json:"reason"`
}

type NotificationDeliverySummary struct {
	ID              uuid.UUID  `json:"id"`
	EventID         string     `json:"event_id"`
	EventType       string     `json:"event_type"`
	RunID           *uuid.UUID `json:"run_id,omitempty"`
	DestinationID   uuid.UUID  `json:"destination_id"`
	DestinationName string     `json:"destination_name"`
	State           string     `json:"state"`
	Attempt         int        `json:"attempt"`
	ResponseCode    *int       `json:"response_code,omitempty"`
	LastError       string     `json:"last_error,omitempty"`
	DeliveredAt     *time.Time `json:"delivered_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type NotificationEvent struct {
	ID              string    `json:"id"`
	Type            string    `json:"type"`
	RunID           uuid.UUID `json:"run_id"`
	Revision        int       `json:"revision"`
	Repository      string    `json:"repository"`
	ReviewNumber    int       `json:"review_number"`
	ReviewURL       string    `json:"review_url"`
	Provider        Provider  `json:"source_provider"`
	APIBaseURL      string    `json:"api_base_url"`
	State           RunState  `json:"state"`
	HeadSHA         string    `json:"head_sha"`
	TargetBranch    string    `json:"target_branch"`
	HighestSeverity string    `json:"highest_severity"`
	FindingCount    int       `json:"finding_count"`
	Test            bool      `json:"test,omitempty"`
}

type NotificationDelivery struct {
	ID          uuid.UUID
	Event       NotificationEvent
	Destination NotificationDestination
	Attempt     int
}
