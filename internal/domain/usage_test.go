package domain

import (
	"testing"
	"time"
)

func TestNormalizeUsageReconciliationInput(t *testing.T) {
	input, period, ok := NormalizeUsageReconciliationInput(UsageReconciliationInput{
		PeriodStart:    " 2026-09-01 ",
		Reason:         " monthly ledger integrity check ",
		IdempotencyKey: " reconcile-september-1 ",
	})
	if !ok {
		t.Fatal("valid reconciliation input was rejected")
	}
	if input.PeriodStart != "2026-09-01" || input.Reason != "monthly ledger integrity check" || input.IdempotencyKey != "reconcile-september-1" {
		t.Fatalf("input was not normalized: %#v", input)
	}
	wantPeriod := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	if !period.Equal(wantPeriod) {
		t.Fatalf("period=%s, want %s", period, wantPeriod)
	}
}

func TestNormalizeUsageReconciliationInputRejectsInvalidRequests(t *testing.T) {
	tests := []UsageReconciliationInput{
		{PeriodStart: "2026-09-02", Reason: "valid reason", IdempotencyKey: "valid-key"},
		{PeriodStart: "2026-13-01", Reason: "valid reason", IdempotencyKey: "valid-key"},
		{PeriodStart: "2026-09-01", Reason: "x", IdempotencyKey: "valid-key"},
		{PeriodStart: "2026-09-01", Reason: "valid reason", IdempotencyKey: "short"},
		{PeriodStart: "2026-09-01", Reason: "valid reason", IdempotencyKey: "invalid\nkey"},
	}
	for _, input := range tests {
		if _, _, ok := NormalizeUsageReconciliationInput(input); ok {
			t.Fatalf("invalid input was accepted: %#v", input)
		}
	}
}

func TestNormalizeUsageExportPeriodUsesUTCAndRejectsFutureMonths(t *testing.T) {
	now := time.Date(2026, time.September, 30, 23, 30, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	period, ok := NormalizeUsageExportPeriod(" 2026-08-01 ", now)
	if !ok || !period.Equal(time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("period=%s ok=%v", period, ok)
	}
	for _, value := range []string{"2026-08-02", "2026-10-01", "not-a-date"} {
		if _, ok := NormalizeUsageExportPeriod(value, now); ok {
			t.Fatalf("invalid export period %q was accepted", value)
		}
	}
}
