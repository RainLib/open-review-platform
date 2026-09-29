package exceptionexpiry

import (
	"context"
	"errors"
	"testing"
)

type recordingStore struct {
	limit      int
	reconciled int
	err        error
}

func (s *recordingStore) ReconcileExpiredRuleExceptions(_ context.Context, limit int) (int, error) {
	s.limit = limit
	return s.reconciled, s.err
}

func TestProcessorReportsWorkOnlyWhenIssueStateChanged(t *testing.T) {
	backend := &recordingStore{reconciled: 2}
	active := 0
	worked, err := (Processor{Store: backend, TenantLimit: 12, TaskStarted: func() func() {
		active++
		return func() { active-- }
	}}).RunOnce(context.Background())
	if err != nil || !worked || backend.limit != 12 || active != 0 {
		t.Fatalf("worked=%t limit=%d active=%d err=%v", worked, backend.limit, active, err)
	}
}

func TestProcessorReturnsNoWorkForAnEmptyPass(t *testing.T) {
	worked, err := (Processor{Store: &recordingStore{}}).RunOnce(context.Background())
	if err != nil || worked {
		t.Fatalf("worked=%t err=%v", worked, err)
	}
}

func TestProcessorRejectsInvalidConfigurationAndReturnsStoreErrors(t *testing.T) {
	if _, err := (Processor{Store: &recordingStore{}, TenantLimit: 1001}).RunOnce(context.Background()); err == nil {
		t.Fatal("expected invalid limit error")
	}
	if _, err := (Processor{Store: &recordingStore{err: errors.New("database unavailable")}}).RunOnce(context.Background()); err == nil {
		t.Fatal("expected store error")
	}
}

func TestParseTenantLimitUsesOnlyBoundedIntegers(t *testing.T) {
	for _, test := range []struct {
		value string
		want  int
	}{
		{"", 50}, {"17", 17}, {"0", 50}, {"1001", 50}, {"oops", 50},
	} {
		if got := ParseTenantLimit(test.value, 50); got != test.want {
			t.Fatalf("ParseTenantLimit(%q)=%d, want %d", test.value, got, test.want)
		}
	}
}
