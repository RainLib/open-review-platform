package credentialcleanup

import (
	"context"
	"testing"
	"time"
)

type cleanupCall struct {
	before time.Time
	limit  int
}

type cleanupStore struct {
	calls chan cleanupCall
}

func (s cleanupStore) RevokeOrphanedProviderOAuthCredentials(_ context.Context, before time.Time, limit int) (int, error) {
	s.calls <- cleanupCall{before: before, limit: limit}
	return 0, nil
}

func TestRunnerCleansOnStartAndRepeatsUntilCancelled(t *testing.T) {
	now := time.Date(2026, time.September, 27, 12, 0, 0, 0, time.FixedZone("+08", 8*60*60))
	store := cleanupStore{calls: make(chan cleanupCall, 3)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		(Runner{Store: store, Now: func() time.Time { return now }, Interval: 10 * time.Millisecond}).Run(ctx)
	}()
	for range 2 {
		select {
		case call := <-store.calls:
			if want := now.UTC().Add(-time.Hour); !call.before.Equal(want) {
				t.Fatalf("cleanup cutoff = %s, want %s", call.before, want)
			}
			if call.limit != 100 {
				t.Fatalf("cleanup batch = %d, want 100", call.limit)
			}
		case <-time.After(time.Second):
			t.Fatal("cleanup did not run")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cleanup runner did not stop")
	}
}
