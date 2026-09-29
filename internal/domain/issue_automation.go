package domain

import (
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// IssueAutoCreatePolicy controls provider-Issue publication for the stable
// finding aggregates in a workspace. It intentionally describes an explicit
// repository scope instead of acting as a tenant-wide "open tickets" switch.
// Jira and Linear are future target adapters; the current provider target
// creates an Issue in the repository that owns the review finding.
type IssueAutoCreatePolicy struct {
	Revision                  int       `json:"revision"`
	Enabled                   bool      `json:"enabled"`
	Target                    string    `json:"target"`
	RepositoryScopes          []string  `json:"repository_scopes"`
	MinimumSeverity           string    `json:"minimum_severity"`
	Categories                []string  `json:"categories"`
	TriggerFirstSeen          bool      `json:"trigger_first_seen"`
	TriggerRegressed          bool      `json:"trigger_regressed"`
	RepeatOccurrenceThreshold int       `json:"repeat_occurrence_threshold"`
	Labels                    []string  `json:"labels"`
	AssigneeExternalID        string    `json:"assignee_external_id,omitempty"`
	TitleTemplate             string    `json:"title_template"`
	BodyTemplate              string    `json:"body_template"`
	UpdatedBy                 string    `json:"updated_by,omitempty"`
	UpdatedAt                 time.Time `json:"updated_at"`
	CanManage                 bool      `json:"can_manage"`
}

const (
	IssueAutoCreateTargetProvider = "provider"
	defaultIssueTitleTemplate     = "[Open Review] {{severity_upper}} {{category}} · {{path}}"
	defaultIssueBodyTemplate      = `## 🔎 Finding summary

> **{{severity_upper}} · {{category}}** — action is required before this finding can be considered resolved.

| Context | Evidence |
| --- | --- |
| **Repository** | {{repository}} |
| **Location** | {{file_link}} |
| **Pull request** | {{review_link}} |
| **Occurrences** | **{{occurrence_count}}** |

### Why this matters

{{evidence}}

### Recommended fix

{{suggestion}}

<details>
<summary><strong>Traceability</strong></summary>

- Review run: ` + "`{{run_id}}`" + `
- Reviewed commit: ` + "`{{head_sha}}`" + `
- Internal issue: ` + "`{{issue_id}}`" + `

</details>`
)

func DefaultIssueAutoCreatePolicy() IssueAutoCreatePolicy {
	return IssueAutoCreatePolicy{
		Target:           IssueAutoCreateTargetProvider,
		RepositoryScopes: []string{},
		MinimumSeverity:  "high",
		Categories:       []string{},
		Labels:           []string{},
		TitleTemplate:    defaultIssueTitleTemplate,
		BodyTemplate:     defaultIssueBodyTemplate,
	}
}

func (policy IssueAutoCreatePolicy) Valid() bool {
	if policy.Revision < 0 || policy.Target != IssueAutoCreateTargetProvider || !validIssueSeverity(policy.MinimumSeverity) ||
		len(policy.RepositoryScopes) > 100 || len(policy.Categories) > 100 || len(policy.Labels) > 50 ||
		len(policy.AssigneeExternalID) > 256 || len(policy.TitleTemplate) > 240 || len(policy.BodyTemplate) > 12000 ||
		policy.RepeatOccurrenceThreshold < 0 || policy.RepeatOccurrenceThreshold > 10000 {
		return false
	}
	if !policy.TriggerFirstSeen && !policy.TriggerRegressed && policy.RepeatOccurrenceThreshold == 0 {
		return false
	}
	for _, scope := range policy.RepositoryScopes {
		if scope = strings.TrimSpace(scope); scope == "" || len(scope) > 512 {
			return false
		}
	}
	for _, category := range policy.Categories {
		if category = strings.TrimSpace(category); category == "" || len(category) > 128 {
			return false
		}
	}
	for _, label := range policy.Labels {
		if label = strings.TrimSpace(label); label == "" || len(label) > 128 {
			return false
		}
	}
	return strings.TrimSpace(policy.TitleTemplate) != "" && strings.TrimSpace(policy.BodyTemplate) != ""
}

func NormalizeIssueAutoCreatePolicy(policy IssueAutoCreatePolicy) (IssueAutoCreatePolicy, bool) {
	policy.Target = strings.ToLower(strings.TrimSpace(policy.Target))
	if policy.Target == "" {
		policy.Target = IssueAutoCreateTargetProvider
	}
	policy.MinimumSeverity = strings.ToLower(strings.TrimSpace(policy.MinimumSeverity))
	if policy.MinimumSeverity == "" {
		policy.MinimumSeverity = "high"
	}
	policy.RepositoryScopes = normalizedIssueStrings(policy.RepositoryScopes, false)
	policy.Categories = normalizedIssueStrings(policy.Categories, true)
	policy.Labels = normalizedIssueStrings(policy.Labels, false)
	policy.AssigneeExternalID = strings.TrimSpace(policy.AssigneeExternalID)
	policy.TitleTemplate = strings.TrimSpace(policy.TitleTemplate)
	if policy.TitleTemplate == "" {
		policy.TitleTemplate = defaultIssueTitleTemplate
	}
	policy.BodyTemplate = strings.TrimSpace(policy.BodyTemplate)
	if policy.BodyTemplate == "" {
		policy.BodyTemplate = defaultIssueBodyTemplate
	}
	return policy, policy.Valid()
}

func normalizedIssueStrings(values []string, lower bool) []string {
	unique := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if lower {
			value = strings.ToLower(value)
		}
		if value != "" {
			unique[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(unique))
	for value := range unique {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func validIssueSeverity(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "low", "medium", "high", "critical":
		return true
	default:
		return false
	}
}

// IssueAutoCreatePreview is deliberately current-state-only. It tells an
// operator which retained aggregates match policy now; it neither creates an
// external Issue nor estimates an unbounded historical backfill.
type IssueAutoCreatePreview struct {
	WindowStart time.Time                    `json:"window_start"`
	WindowEnd   time.Time                    `json:"window_end"`
	Candidates  []IssueAutoCreatePreviewItem `json:"candidates"`
}

type IssueAutoCreatePreviewItem struct {
	IssueID          uuid.UUID   `json:"issue_id"`
	Repository       string      `json:"repository"`
	Path             string      `json:"path"`
	Severity         string      `json:"severity"`
	Category         string      `json:"category"`
	OccurrenceCount  int         `json:"occurrence_count"`
	Status           IssueStatus `json:"status"`
	AlreadyPublished bool        `json:"already_published"`
}

type ExternalIssuePublicationState string

const (
	ExternalIssuePublicationQueued    ExternalIssuePublicationState = "queued"
	ExternalIssuePublicationCreated   ExternalIssuePublicationState = "created"
	ExternalIssuePublicationFailed    ExternalIssuePublicationState = "failed"
	ExternalIssuePublicationCancelled ExternalIssuePublicationState = "cancelled"
	ExternalIssuePublicationClosed    ExternalIssuePublicationState = "closed"
)

type ExternalIssueReceipt struct {
	ID          uuid.UUID                     `json:"id"`
	IssueID     uuid.UUID                     `json:"issue_id"`
	Provider    Provider                      `json:"provider"`
	Repository  string                        `json:"repository"`
	Trigger     string                        `json:"trigger"`
	State       ExternalIssuePublicationState `json:"state"`
	ExternalID  string                        `json:"external_id,omitempty"`
	ExternalURL string                        `json:"external_url,omitempty"`
	Attempts    int                           `json:"attempts"`
	LastError   string                        `json:"last_error,omitempty"`
	CreatedAt   time.Time                     `json:"created_at"`
	UpdatedAt   time.Time                     `json:"updated_at"`
}

// ExternalIssuePublication is worker-only input. Credential references are
// not serialised to browsers or persisted in broker messages.
type ExternalIssuePublication struct {
	ExternalIssueReceipt
	TenantID               uuid.UUID `json:"-"`
	APIBaseURL             string    `json:"-"`
	InstallationExternalID string    `json:"-"`
	CredentialRef          string    `json:"-"`
	Marker                 string    `json:"-"`
	Title                  string    `json:"-"`
	Body                   string    `json:"-"`
	Labels                 []string  `json:"-"`
	AssigneeExternalID     string    `json:"-"`
}
