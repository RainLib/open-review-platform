package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestIssueCursorIsFilterBoundAndRoundTripsOrderingTuple(t *testing.T) {
	seenAt := time.Date(2026, time.September, 19, 8, 12, 3, 456000000, time.UTC)
	filter := IssueFilter{
		Status:     IssueOpen,
		Repository: "RainLib/open-review-platform",
		Query:      "redirect",
		SeenAfter:  &seenAt,
		Limit:      25,
	}
	issue := IssueSummary{
		ID:         uuid.New(),
		Status:     IssueOpen,
		Severity:   "high",
		LastSeenAt: seenAt.Add(time.Minute),
	}
	filter.Cursor = EncodeIssueCursor(filter, issue)
	statusRank, severityRank, lastSeenAt, id, ok, err := DecodeIssueCursor(filter)
	if err != nil || !ok || statusRank != 1 || severityRank != 1 || !lastSeenAt.Equal(issue.LastSeenAt) || id != issue.ID {
		t.Fatalf("cursor decoded as rank=%d/%d seen=%s id=%s ok=%t err=%v", statusRank, severityRank, lastSeenAt, id, ok, err)
	}

	filter.Repository = "RainLib/another-repository"
	if _, _, _, _, _, err := DecodeIssueCursor(filter); err == nil {
		t.Fatal("expected cursor to reject a changed filter scope")
	}
}

func TestIssueFilterRejectsAnImpossibleBackwardCursor(t *testing.T) {
	if (IssueFilter{CursorDirection: IssueCursorBefore, Limit: 25}).Valid() {
		t.Fatal("a backward cursor without a boundary must be invalid")
	}
}
