package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestFindingCursorBoundToActorAndFilter(t *testing.T) {
	filter := FindingFilter{View: FindingHighRisk, Repository: "RainLib/open-review-platform", Query: "auth", Limit: 25}
	item := FindingFeedbackItem{ID: uuid.New(), CreatedAt: time.Now().UTC()}
	filter.Cursor = EncodeFindingCursor(filter, "owner", item)
	at, id, ok, err := DecodeFindingCursor(filter, "owner")
	if err != nil || !ok || id != item.ID || !at.Equal(item.CreatedAt) {
		t.Fatalf("cursor roundtrip at=%s id=%s ok=%t err=%v", at, id, ok, err)
	}
	if _, _, _, err := DecodeFindingCursor(filter, "other"); err == nil {
		t.Fatal("cursor should reject a different actor")
	}
	filter.View = FindingActioned
	if _, _, _, err := DecodeFindingCursor(filter, "owner"); err == nil {
		t.Fatal("cursor should reject a different view")
	}
	filter.Cursor = "bad cursor"
	if _, _, _, err := DecodeFindingCursor(filter, "owner"); err == nil {
		t.Fatal("cursor should reject invalid encoding")
	}
	filter.Cursor = ""
	filter.CursorDirection = WorkQueueCursorBefore
	if filter.Valid() {
		t.Fatal("backward navigation needs a cursor")
	}
}
