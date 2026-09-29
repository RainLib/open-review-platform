package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
)

var ErrInvalidWorkerHeartbeat = errors.New("worker heartbeat is invalid")

func (s *PostgresStore) UpsertWorkerHeartbeat(ctx context.Context, heartbeat domain.WorkerHeartbeat) error {
	heartbeat.WorkerID = strings.TrimSpace(heartbeat.WorkerID)
	heartbeat.Kind = strings.TrimSpace(heartbeat.Kind)
	heartbeat.Version = strings.TrimSpace(heartbeat.Version)
	invalidProbe := (heartbeat.AdapterProbeAt == nil) != (heartbeat.AdapterReachable == nil)
	if heartbeat.AdapterProbeAt != nil {
		invalidProbe = invalidProbe || !heartbeat.AdapterConfigured || heartbeat.Kind != "agent-task-runner" ||
			heartbeat.AdapterProbeAt.Before(heartbeat.StartedAt) || heartbeat.AdapterProbeAt.After(heartbeat.HeartbeatAt) ||
			heartbeat.HeartbeatAt.Sub(*heartbeat.AdapterProbeAt) > 5*time.Second
	}
	if invalidProbe || heartbeat.WorkerID == "" || len(heartbeat.WorkerID) > 200 || heartbeat.Kind == "" || len(heartbeat.Kind) > 80 || len(heartbeat.Version) > 200 || heartbeat.Capacity < 1 || heartbeat.Capacity > 10000 || heartbeat.Busy < 0 || heartbeat.Busy > heartbeat.Capacity || heartbeat.StartedAt.IsZero() || heartbeat.HeartbeatAt.IsZero() || heartbeat.HeartbeatAt.Before(heartbeat.StartedAt) || !heartbeat.ExpiresAt.After(heartbeat.HeartbeatAt) || heartbeat.ExpiresAt.Sub(heartbeat.HeartbeatAt) > 5*time.Minute {
		return ErrInvalidWorkerHeartbeat
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO worker_heartbeats (
			worker_id,kind,version,capacity,busy,started_at,heartbeat_at,expires_at,adapter_configured,adapter_probe_at,adapter_reachable,decision_configured,updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,now())
		ON CONFLICT (worker_id) DO UPDATE SET
			kind=EXCLUDED.kind,
			version=EXCLUDED.version,
			capacity=EXCLUDED.capacity,
			busy=EXCLUDED.busy,
			started_at=CASE
				WHEN worker_heartbeats.kind <> EXCLUDED.kind THEN EXCLUDED.started_at
				ELSE LEAST(worker_heartbeats.started_at,EXCLUDED.started_at)
			END,
			heartbeat_at=EXCLUDED.heartbeat_at,
			expires_at=EXCLUDED.expires_at,
			adapter_configured=EXCLUDED.adapter_configured,
			adapter_probe_at=EXCLUDED.adapter_probe_at,
			adapter_reachable=EXCLUDED.adapter_reachable,
			decision_configured=EXCLUDED.decision_configured,
			updated_at=now()
		WHERE worker_heartbeats.heartbeat_at <= EXCLUDED.heartbeat_at`,
		heartbeat.WorkerID, heartbeat.Kind, heartbeat.Version, heartbeat.Capacity, heartbeat.Busy,
		heartbeat.StartedAt.UTC(), heartbeat.HeartbeatAt.UTC(), heartbeat.ExpiresAt.UTC(), heartbeat.AdapterConfigured,
		heartbeat.AdapterProbeAt, heartbeat.AdapterReachable, heartbeat.DecisionConfigured)
	if err != nil {
		return fmt.Errorf("upsert worker heartbeat: %w", err)
	}
	return nil
}
