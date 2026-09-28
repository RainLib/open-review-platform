package domain

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

type IssueStatus string

const (
	IssueOpen       IssueStatus = "open"
	IssueRegressed  IssueStatus = "regressed"
	IssueResolved   IssueStatus = "resolved"
	IssueSuppressed IssueStatus = "suppressed"
)

func (status IssueStatus) Valid() bool {
	return status == "" || status == IssueOpen || status == IssueRegressed || status == IssueResolved || status == IssueSuppressed
}

type IssueFilter struct {
	Status          IssueStatus
	Severity        string
	Repository      string
	AssigneeSubject string
	Category        string
	Query           string
	SeenAfter       *time.Time
	ActiveOnly      bool
	Cursor          string
	CursorDirection IssueCursorDirection
	Limit           int
	Filters         *IssueFilterExpression
	FilterTime      *time.Time
	FilterActor     string
	View            string
	SelectedIssueID *uuid.UUID
}

type IssueCursorDirection string

const (
	IssueCursorAfter  IssueCursorDirection = "after"
	IssueCursorBefore IssueCursorDirection = "before"
)

func (filter IssueFilter) Valid() bool {
	if filter.Limit < 1 || filter.Limit > 100 || !filter.Status.Valid() || len(strings.TrimSpace(filter.AssigneeSubject)) > 256 ||
		len(strings.TrimSpace(filter.Repository)) > 512 || len(strings.TrimSpace(filter.Category)) > 128 ||
		len(strings.TrimSpace(filter.Query)) > 256 || len(strings.TrimSpace(filter.Cursor)) > 1024 {
		return false
	}
	if filter.CursorDirection != "" && filter.CursorDirection != IssueCursorAfter && filter.CursorDirection != IssueCursorBefore {
		return false
	}
	if filter.Cursor == "" && filter.CursorDirection == IssueCursorBefore {
		return false
	}
	if filter.SeenAfter != nil && filter.SeenAfter.IsZero() {
		return false
	}
	if (filter.Filters != nil && !filter.Filters.Valid()) || (filter.FilterTime != nil && filter.FilterTime.IsZero()) {
		return false
	}
	if filter.View != "" && !ValidIssueView(filter.View) {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(filter.Severity)) {
	case "", "low", "medium", "high", "critical":
		return true
	default:
		return false
	}
}

// IssuePage is a stable, keyset-paginated inbox result. Cursors are bound to
// the normalized filters that produced them, so a link cannot silently skip
// items after a user changes the active inbox view.
type IssuePage struct {
	Issues         []IssueSummary   `json:"issues"`
	Counts         IssueInboxCounts `json:"counts"`
	Facets         IssueInboxFacets `json:"facets"`
	NextCursor     string           `json:"next_cursor,omitempty"`
	PreviousCursor string           `json:"previous_cursor,omitempty"`
	FilterTime     time.Time        `json:"filter_time"`
	TotalCount     int              `json:"total_count"`
	SelectedInView *bool            `json:"selected_in_view,omitempty"`
}

type IssueInboxFacets struct {
	Repositories []string `json:"repositories"`
	Categories   []string `json:"categories"`
}

type IssueInboxCounts struct {
	Open       int `json:"open"`
	Regressed  int `json:"regressed"`
	Critical   int `json:"critical"`
	Assigned   int `json:"assigned"`
	Resolved   int `json:"resolved"`
	Suppressed int `json:"suppressed"`
}

type issueCursor struct {
	Version      int    `json:"v"`
	StatusRank   int    `json:"status_rank"`
	SeverityRank int    `json:"severity_rank"`
	LastSeenAt   string `json:"last_seen_at"`
	ID           string `json:"id"`
	Scope        string `json:"scope"`
}

// EncodeIssueCursor records the complete ordering tuple, rather than an
// offset. New issues can arrive while an operator pages without duplicating or
// losing the rows that were already in the result set.
func EncodeIssueCursor(filter IssueFilter, issue IssueSummary) string {
	payload, err := json.Marshal(issueCursor{
		Version:      1,
		StatusRank:   issueStatusRank(issue.Status),
		SeverityRank: issueSeverityRank(issue.Severity),
		LastSeenAt:   issue.LastSeenAt.UTC().Format(time.RFC3339Nano),
		ID:           issue.ID.String(),
		Scope:        issueCursorScope(filter),
	})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

// DecodeIssueCursor validates an opaque cursor before it is used in a query.
// The cursor is not an authorization token; tenant authorization remains in
// the store. Its scope only protects the user experience from filter drift.
func DecodeIssueCursor(filter IssueFilter) (statusRank, severityRank int, lastSeenAt time.Time, id uuid.UUID, ok bool, err error) {
	if strings.TrimSpace(filter.Cursor) == "" {
		return 0, 0, time.Time{}, uuid.Nil, false, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(filter.Cursor)
	if err != nil {
		return 0, 0, time.Time{}, uuid.Nil, false, errors.New("decode issue cursor")
	}
	var cursor issueCursor
	if err := json.Unmarshal(payload, &cursor); err != nil {
		return 0, 0, time.Time{}, uuid.Nil, false, errors.New("unmarshal issue cursor")
	}
	if cursor.Version != 1 || cursor.StatusRank < 0 || cursor.StatusRank > 3 || cursor.SeverityRank < 0 || cursor.SeverityRank > 3 || cursor.Scope != issueCursorScope(filter) {
		return 0, 0, time.Time{}, uuid.Nil, false, errors.New("issue cursor is invalid")
	}
	lastSeenAt, err = time.Parse(time.RFC3339Nano, cursor.LastSeenAt)
	if err != nil || lastSeenAt.IsZero() {
		return 0, 0, time.Time{}, uuid.Nil, false, errors.New("issue cursor timestamp is invalid")
	}
	id, err = uuid.Parse(cursor.ID)
	if err != nil || id == uuid.Nil {
		return 0, 0, time.Time{}, uuid.Nil, false, errors.New("issue cursor id is invalid")
	}
	return cursor.StatusRank, cursor.SeverityRank, lastSeenAt.UTC(), id, true, nil
}

func issueCursorScope(filter IssueFilter) string {
	seenAfter := ""
	if filter.SeenAfter != nil {
		seenAfter = filter.SeenAfter.UTC().Format(time.RFC3339Nano)
	}
	value := strings.Join([]string{
		string(filter.Status),
		strings.ToLower(strings.TrimSpace(filter.Severity)),
		strings.TrimSpace(filter.Repository),
		strings.TrimSpace(filter.AssigneeSubject),
		strings.ToLower(strings.TrimSpace(filter.Category)),
		strings.ToLower(strings.TrimSpace(filter.Query)),
		seenAfter,
		strconv.FormatBool(filter.ActiveOnly),
	}, "\x00")
	if filter.View != "" {
		value += "\x00view:" + filter.View + "\x00" + filter.FilterActor
	}
	if filter.Filters != nil {
		anchor := ""
		if filter.FilterTime != nil {
			anchor = filter.FilterTime.UTC().Format(time.RFC3339Nano)
		}
		value += "\x00" + string(filter.Filters.CanonicalJSON()) + "\x00" + anchor + "\x00" + filter.FilterActor
	}
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func issueStatusRank(status IssueStatus) int {
	switch status {
	case IssueRegressed:
		return 0
	case IssueOpen:
		return 1
	case IssueSuppressed:
		return 2
	default:
		return 3
	}
}

func issueSeverityRank(severity string) int {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical":
		return 0
	case "high":
		return 1
	case "medium":
		return 2
	default:
		return 3
	}
}

type IssueSummary struct {
	ID                    uuid.UUID   `json:"id"`
	Revision              int         `json:"revision"`
	Provider              Provider    `json:"provider"`
	APIBaseURL            string      `json:"api_base_url"`
	Repository            string      `json:"repository"`
	Fingerprint           string      `json:"fingerprint"`
	Path                  string      `json:"path"`
	Severity              string      `json:"severity"`
	Category              string      `json:"category"`
	BodyPreview           string      `json:"body_preview"`
	Status                IssueStatus `json:"status"`
	AssigneeSubject       string      `json:"assignee_subject,omitempty"`
	DispositionKind       string      `json:"disposition_kind,omitempty"`
	DispositionReason     string      `json:"disposition_reason,omitempty"`
	OccurrenceCount       int         `json:"occurrence_count"`
	ActiveOccurrenceCount int         `json:"active_occurrence_count"`
	PullRequestCount      int         `json:"pull_request_count"`
	FirstSeenAt           time.Time   `json:"first_seen_at"`
	LastSeenAt            time.Time   `json:"last_seen_at"`
	ResolvedAt            *time.Time  `json:"resolved_at,omitempty"`
	UpdatedAt             time.Time   `json:"updated_at"`
}

type IssueOccurrence struct {
	ID               uuid.UUID                        `json:"id"`
	FindingID        uuid.UUID                        `json:"finding_id"`
	RequestID        *uuid.UUID                       `json:"request_id,omitempty"`
	RunID            *uuid.UUID                       `json:"run_id,omitempty"`
	ReviewNumber     int                              `json:"review_number"`
	HeadSHA          string                           `json:"head_sha"`
	Path             string                           `json:"path"`
	StartLine        int                              `json:"start_line"`
	EndLine          int                              `json:"end_line"`
	Severity         string                           `json:"severity"`
	Category         string                           `json:"category"`
	Body             string                           `json:"body"`
	Suggestion       string                           `json:"suggestion,omitempty"`
	RuleAttributions []FindingRuleAttributionEvidence `json:"rule_attributions"`
	Active           bool                             `json:"active"`
	CreatedAt        time.Time                        `json:"created_at"`
}

type IssueDetail struct {
	IssueSummary
	Occurrences      []IssueOccurrence      `json:"occurrences"`
	Events           []IssueEvent           `json:"events"`
	ExternalIssue    *ExternalIssueReceipt  `json:"external_issue,omitempty"`
	CanManage        bool                   `json:"can_manage"`
	ExceptionRequest *IssueExceptionRequest `json:"exception_request,omitempty"`
}

type IssueExceptionRequest struct {
	ID             uuid.UUID `json:"id"`
	RuleKey        string    `json:"rule_key"`
	State          string    `json:"state"`
	EffectiveState string    `json:"effective_state"`
	RequestedBy    string    `json:"requested_by"`
	ExpiresAt      time.Time `json:"expires_at"`
}

type IssueEvent struct {
	ID               uuid.UUID   `json:"id"`
	Revision         int         `json:"revision"`
	ActorSubject     string      `json:"actor_subject"`
	Action           string      `json:"action"`
	PreviousStatus   IssueStatus `json:"previous_status"`
	NextStatus       IssueStatus `json:"next_status"`
	PreviousAssignee string      `json:"previous_assignee,omitempty"`
	NextAssignee     string      `json:"next_assignee,omitempty"`
	Reason           string      `json:"reason,omitempty"`
	CreatedAt        time.Time   `json:"created_at"`
}

type IssueActionInput struct {
	Action           string `json:"action"`
	AssigneeSubject  string `json:"assignee_subject,omitempty"`
	Reason           string `json:"reason,omitempty"`
	ExpectedRevision int    `json:"expected_revision"`
}
