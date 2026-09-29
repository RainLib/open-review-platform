package health

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
)

type heartbeatRecorder struct {
	items []domain.WorkerHeartbeat
}

func (r *heartbeatRecorder) UpsertWorkerHeartbeat(_ context.Context, heartbeat domain.WorkerHeartbeat) error {
	r.items = append(r.items, heartbeat)
	return nil
}

func TestReporterCapturesBoundedOccupancy(t *testing.T) {
	recorder := &heartbeatRecorder{}
	reporter := &Reporter{
		Store: recorder, WorkerID: "runner-a", Kind: "review-runner", Version: "test",
		Capacity: 1, TTL: time.Minute, startedAt: time.Now().UTC().Add(-time.Minute),
	}
	doneFirst := reporter.BeginTask()
	doneSecond := reporter.BeginTask()
	reporter.write(context.Background())
	doneSecond()
	doneFirst()
	if len(recorder.items) != 1 {
		t.Fatalf("heartbeat count=%d, want 1", len(recorder.items))
	}
	item := recorder.items[0]
	if item.Busy != 1 || item.Capacity != 1 || item.WorkerID != "runner-a" || item.Kind != "review-runner" || !item.ExpiresAt.After(item.HeartbeatAt) {
		t.Fatalf("unexpected heartbeat: %#v", item)
	}
}

func TestAgentReporterCarriesConfigurationWithoutEndpoint(t *testing.T) {
	recorder := &heartbeatRecorder{}
	reporter := &Reporter{
		Store: recorder, WorkerID: "agent-a", Kind: "agent-task-runner",
		AdapterConfigured: true, Capacity: 1, TTL: time.Minute,
		startedAt: time.Now().UTC().Add(-time.Minute),
	}
	reporter.write(context.Background())
	if len(recorder.items) != 1 || !recorder.items[0].AdapterConfigured {
		t.Fatalf("agent configuration heartbeat missing: %#v", recorder.items)
	}
}

func TestAgentReporterSeparatesSignedProbeFromConfiguration(t *testing.T) {
	recorder := &heartbeatRecorder{}
	reporter := &Reporter{
		Store: recorder, WorkerID: "agent-probed", Kind: "agent-task-runner",
		AdapterConfigured: true, AdapterProbe: func(context.Context) error { return nil },
		Capacity: 1, TTL: time.Minute, startedAt: time.Now().UTC().Add(-time.Minute),
	}
	reporter.write(context.Background())
	if len(recorder.items) != 1 || recorder.items[0].AdapterProbeAt == nil || recorder.items[0].AdapterReachable == nil || !*recorder.items[0].AdapterReachable {
		t.Fatalf("successful adapter probe was not observed: %#v", recorder.items)
	}
	reporter.AdapterProbe = func(context.Context) error { return errors.New("adapter unavailable") }
	reporter.write(context.Background())
	if len(recorder.items) != 2 || recorder.items[1].AdapterReachable == nil || *recorder.items[1].AdapterReachable {
		t.Fatalf("failed adapter probe appeared reachable: %#v", recorder.items)
	}
	reporter.AdapterConfigured = false
	reporter.write(context.Background())
	if recorder.items[2].AdapterProbeAt != nil || recorder.items[2].AdapterReachable != nil {
		t.Fatalf("unconfigured adapter carried probe evidence: %#v", recorder.items[2])
	}
}
