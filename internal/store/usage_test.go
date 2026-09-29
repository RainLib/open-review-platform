package store

import (
	"testing"
	"time"
)

func TestUsagePeriodUsesUTCMonthBoundaries(t *testing.T) {
	input := time.Date(2026, time.March, 1, 0, 30, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	start, end := usagePeriod(input)
	wantStart := time.Date(2026, time.February, 1, 0, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)
	if !start.Equal(wantStart) || !end.Equal(wantEnd) {
		t.Fatalf("usagePeriod(%s)=(%s,%s), want (%s,%s)", input, start, end, wantStart, wantEnd)
	}
}
