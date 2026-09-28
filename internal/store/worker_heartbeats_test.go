package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
)

func TestUpsertWorkerHeartbeatRejectsInvalidEvidence(t *testing.T) {
	store := &PostgresStore{}
	now := time.Now().UTC()
	err := store.UpsertWorkerHeartbeat(context.Background(), domain.WorkerHeartbeat{
		WorkerID: "runner", Kind: "review-runner", Capacity: 1, Busy: 2,
		StartedAt: now, HeartbeatAt: now, ExpiresAt: now.Add(time.Minute),
	})
	if !errors.Is(err, ErrInvalidWorkerHeartbeat) {
		t.Fatalf("error=%v, want ErrInvalidWorkerHeartbeat", err)
	}
}

func TestUpsertWorkerHeartbeatRejectsUnboundAdapterProbe(t *testing.T) {
	postgres := &PostgresStore{}
	now := time.Now().UTC()
	reachable := true
	for _, heartbeat := range []domain.WorkerHeartbeat{
		{WorkerID: "agent", Kind: "agent-task-runner", AdapterConfigured: false, AdapterProbeAt: &now, AdapterReachable: &reachable},
		{WorkerID: "agent", Kind: "agent-task-runner", AdapterConfigured: true, AdapterProbeAt: &now},
		{WorkerID: "agent", Kind: "review-runner", AdapterConfigured: true, AdapterProbeAt: &now, AdapterReachable: &reachable},
	} {
		heartbeat.Capacity = 1
		heartbeat.StartedAt = now.Add(-time.Minute)
		heartbeat.HeartbeatAt = now
		heartbeat.ExpiresAt = now.Add(time.Minute)
		if err := postgres.UpsertWorkerHeartbeat(context.Background(), heartbeat); !errors.Is(err, ErrInvalidWorkerHeartbeat) {
			t.Fatalf("unbound probe=%#v error=%v", heartbeat, err)
		}
	}
}
