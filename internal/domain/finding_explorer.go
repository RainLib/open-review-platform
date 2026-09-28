package domain

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

type FindingView string

const (
	FindingActive   FindingView = "active"
	FindingHighRisk FindingView = "high-risk"
	FindingActioned FindingView = "actioned"
)

type FindingFilter struct {
	View            FindingView
	Repository      string
	Query           string
	Cursor          string
	CursorDirection WorkQueueCursorDirection
	Limit           int
}

func (filter FindingFilter) Valid() bool {
	if filter.View != FindingActive && filter.View != FindingHighRisk && filter.View != FindingActioned {
		return false
	}
	if filter.Limit < 1 || filter.Limit > 100 || len(filter.Repository) > 512 ||
		len(filter.Query) > 128 || len(filter.Cursor) > 1024 {
		return false
	}
	if filter.CursorDirection != "" && filter.CursorDirection != WorkQueueCursorAfter && filter.CursorDirection != WorkQueueCursorBefore {
		return false
	}
	return filter.Cursor != "" || filter.CursorDirection != WorkQueueCursorBefore
}

type FindingPage struct {
	Findings       []FindingFeedbackItem `json:"findings"`
	NextCursor     string                `json:"next_cursor,omitempty"`
	PreviousCursor string                `json:"previous_cursor,omitempty"`
}

type findingCursor struct {
	Version   int    `json:"v"`
	CreatedAt string `json:"created_at"`
	ID        string `json:"id"`
	Scope     string `json:"scope"`
}

func findingCursorScope(filter FindingFilter, actor string) string {
	value := strings.Join([]string{string(filter.View), filter.Repository, strings.ToLower(filter.Query), actor}, "\x00")
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func EncodeFindingCursor(filter FindingFilter, actor string, item FindingFeedbackItem) string {
	payload, err := json.Marshal(findingCursor{
		Version: 1, CreatedAt: item.CreatedAt.UTC().Format(time.RFC3339Nano),
		ID: item.ID.String(), Scope: findingCursorScope(filter, actor),
	})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

func DecodeFindingCursor(filter FindingFilter, actor string) (time.Time, uuid.UUID, bool, error) {
	if filter.Cursor == "" {
		return time.Time{}, uuid.Nil, false, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(filter.Cursor)
	if err != nil {
		return time.Time{}, uuid.Nil, false, errors.New("decode finding cursor")
	}
	var cursor findingCursor
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.Version != 1 || cursor.Scope != findingCursorScope(filter, actor) {
		return time.Time{}, uuid.Nil, false, errors.New("finding cursor is invalid")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, cursor.CreatedAt)
	if err != nil || createdAt.IsZero() {
		return time.Time{}, uuid.Nil, false, errors.New("finding cursor timestamp is invalid")
	}
	id, err := uuid.Parse(cursor.ID)
	if err != nil || id == uuid.Nil {
		return time.Time{}, uuid.Nil, false, errors.New("finding cursor id is invalid")
	}
	return createdAt.UTC(), id, true, nil
}
