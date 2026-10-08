package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

type dispatchStoreProbe struct {
	actions   *[]string
	attachErr error
}

type readinessStoreProbe struct {
	actions  *[]string
	pending  *domain.AgentTaskAttempt
	holdErr  error
	claimErr error
}

func (probe readinessStoreProbe) PendingAgentTaskAdapterStart(context.Context, string, domain.AgentTaskExecutionRequest) (*domain.AgentTaskAttempt, error) {
	*probe.actions = append(*probe.actions, "pending")
	if probe.pending != nil {
		return probe.pending, nil
	}
	return nil, store.ErrNoQueuedAgentTask
}
func (probe readinessStoreProbe) HoldAgentTaskExecutionForReadiness(context.Context, string, domain.AgentTaskExecutionRequest) error {
	*probe.actions = append(*probe.actions, "hold")
	return probe.holdErr
}
func (probe readinessStoreProbe) ClaimAgentTaskAttempt(context.Context, string, domain.AgentTaskExecutionRequest, time.Duration) (*domain.AgentTaskAttempt, error) {
	*probe.actions = append(*probe.actions, "claim")
	if probe.claimErr != nil {
		return nil, probe.claimErr
	}
	return &domain.AgentTaskAttempt{ID: uuid.New()}, nil
}

type readinessAdapterProbe struct {
	dispatchAdapterProbe
	readyErr error
}

func (probe readinessAdapterProbe) CheckExecutionReady(context.Context) error {
	*probe.actions = append(*probe.actions, "readiness")
	return probe.readyErr
}

func TestPreflightHoldsBeforeClaimAndRecoversOnlyAttachedStart(t *testing.T) {
	for _, tc := range []struct {
		name                                  string
		readyErr, holdErr                     error
		pending, absent, wantAttempt, wantErr bool
		want                                  []string
	}{
		{name: "ready", wantAttempt: true, want: []string{"pending", "readiness", "claim"}},
		{name: "model denied", readyErr: errors.New("403"), want: []string{"pending", "readiness", "hold"}},
		{name: "adapter absent", absent: true, want: []string{"hold"}},
		{name: "store failure", readyErr: errors.New("403"), holdErr: errors.New("DB failed"), wantErr: true, want: []string{"pending", "readiness", "hold"}},
		{name: "old delivery", readyErr: errors.New("403"), holdErr: store.ErrNoQueuedAgentTask, want: []string{"pending", "readiness", "hold", "pending"}},
		{name: "ambiguous start", pending: true, readyErr: errors.New("403"), want: []string{"pending", "start"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actions := []string{}
			database := readinessStoreProbe{actions: &actions, holdErr: tc.holdErr}
			if tc.pending {
				database.pending = &domain.AgentTaskAttempt{ID: uuid.New(), AdapterJobID: "original-job"}
			}
			var adapter agentTaskReadyAdapter
			var startID uuid.UUID
			var startJob string
			if !tc.absent {
				adapter = readinessAdapterProbe{dispatchAdapterProbe: dispatchAdapterProbe{actions: &actions, startAttemptID: &startID, startJobID: &startJob}, readyErr: tc.readyErr}
			}
			attempt, err := claimReadyAgentTaskAttempt(context.Background(), database, adapter, "worker", domain.AgentTaskExecutionRequest{}, time.Minute)
			if (attempt != nil) != tc.wantAttempt || (err != nil) != tc.wantErr || !reflect.DeepEqual(actions, tc.want) {
				t.Fatalf("attempt=%v err=%v actions=%v", attempt, err, actions)
			}
			if tc.pending && (startID != database.pending.ID || startJob != database.pending.AdapterJobID) {
				t.Fatal("recovery did not use original reservation")
			}
		})
	}
}

func (probe dispatchStoreProbe) MarkAgentTaskAttemptNeedsAttention(context.Context, uuid.UUID, string, string, string) error {
	*probe.actions = append(*probe.actions, "attention")
	return nil
}

func (probe dispatchStoreProbe) AttachAgentTaskAdapterJob(_ context.Context, _ uuid.UUID, _, _ string) error {
	*probe.actions = append(*probe.actions, "attach")
	return probe.attachErr
}

type dispatchAdapterProbe struct {
	actions          *[]string
	cancelRequest    *domain.AgentTaskCancellationRequest
	cancelContextErr *error
	cancelErr        error
	startAttemptID   *uuid.UUID
	startJobID       *string
	startErr         error
}

func (probe dispatchAdapterProbe) Submit(context.Context, domain.AgentTaskAttemptTarget) (string, error) {
	*probe.actions = append(*probe.actions, "submit")
	return "adapter-job-1", nil
}

func (probe dispatchAdapterProbe) Start(_ context.Context, attemptID uuid.UUID, jobID string) error {
	*probe.actions = append(*probe.actions, "start")
	if probe.startAttemptID != nil {
		*probe.startAttemptID = attemptID
	}
	if probe.startJobID != nil {
		*probe.startJobID = jobID
	}
	return probe.startErr
}

func (probe dispatchAdapterProbe) Cancel(ctx context.Context, request domain.AgentTaskCancellationRequest) error {
	*probe.actions = append(*probe.actions, "cancel")
	if probe.cancelRequest != nil {
		*probe.cancelRequest = request
	}
	if probe.cancelContextErr != nil {
		*probe.cancelContextErr = ctx.Err()
	}
	return probe.cancelErr
}

func TestDispatchAttachesBeforeStartingAndRevokesLostLease(t *testing.T) {
	for _, tc := range []struct {
		name      string
		attachErr error
		want      []string
		wantErr   bool
	}{
		{name: "accepted", want: []string{"submit", "attach", "start"}},
		{name: "lost lease", attachErr: store.ErrAgentTaskClaimLost, want: []string{"submit", "attach", "cancel"}},
		{name: "store failure", attachErr: errors.New("database unavailable"), want: []string{"submit", "attach", "cancel"}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actions := []string{}
			var cancelled domain.AgentTaskCancellationRequest
			database := dispatchStoreProbe{actions: &actions, attachErr: tc.attachErr}
			adapter := dispatchAdapterProbe{actions: &actions, cancelRequest: &cancelled}
			target := domain.AgentTaskAttemptTarget{Task: domain.AgentTask{ID: uuid.New()}}
			attemptID := uuid.New()
			err := dispatchApprovedAgentTask(context.Background(), database, adapter, "worker-1", attemptID, target)
			if (err != nil) != tc.wantErr || !reflect.DeepEqual(actions, tc.want) {
				t.Fatalf("actions=%v err=%v, want %v error=%t", actions, err, tc.want, tc.wantErr)
			}
			if tc.attachErr != nil && (cancelled.TaskID != target.Task.ID || cancelled.AttemptID != attemptID || cancelled.AdapterJobID != "adapter-job-1") {
				t.Fatalf("cancelled reservation=%+v, want exact task, attempt and job", cancelled)
			}
		})
	}
}

func TestDispatchRevokesReservationAfterRequestContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	actions := []string{}
	var cleanupContextErr error
	storeErr := errors.New("attachment response lost")
	err := dispatchApprovedAgentTask(ctx,
		dispatchStoreProbe{actions: &actions, attachErr: storeErr},
		dispatchAdapterProbe{actions: &actions, cancelContextErr: &cleanupContextErr},
		"worker-1", uuid.New(), domain.AgentTaskAttemptTarget{Task: domain.AgentTask{ID: uuid.New()}},
	)
	if !errors.Is(err, storeErr) || cleanupContextErr != nil || !reflect.DeepEqual(actions, []string{"submit", "attach", "cancel"}) {
		t.Fatalf("actions=%v attachment error=%v cleanup context error=%v", actions, err, cleanupContextErr)
	}
}

type pendingStartStoreProbe struct {
	attempt *domain.AgentTaskAttempt
	err     error
	worker  *string
	request *domain.AgentTaskExecutionRequest
}

func (probe pendingStartStoreProbe) PendingAgentTaskAdapterStart(_ context.Context, workerID string, request domain.AgentTaskExecutionRequest) (*domain.AgentTaskAttempt, error) {
	if probe.worker != nil {
		*probe.worker = workerID
	}
	if probe.request != nil {
		*probe.request = request
	}
	return probe.attempt, probe.err
}

func TestRetryPendingAdapterStartUsesOnlyTheAttachedJob(t *testing.T) {
	attempt := &domain.AgentTaskAttempt{ID: uuid.New(), AdapterJobID: "reserved-job-1"}
	request := domain.AgentTaskExecutionRequest{TaskID: uuid.New(), TaskRevision: 3, PlanID: uuid.New(), PlanRevision: 2, PlanSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}
	actions := []string{}
	var gotAttempt uuid.UUID
	var gotJob, gotWorker string
	var gotRequest domain.AgentTaskExecutionRequest
	startErr := errors.New("adapter start response lost")
	err := retryPendingAdapterStart(context.Background(), pendingStartStoreProbe{attempt: attempt, worker: &gotWorker, request: &gotRequest},
		dispatchAdapterProbe{actions: &actions, startAttemptID: &gotAttempt, startJobID: &gotJob, startErr: startErr}, "worker-1", request)
	if !errors.Is(err, startErr) || !reflect.DeepEqual(actions, []string{"start"}) || gotAttempt != attempt.ID || gotJob != attempt.AdapterJobID || gotWorker != "worker-1" || gotRequest != request {
		t.Fatalf("retry actions=%v attempt=%s job=%q worker=%q request=%+v error=%v", actions, gotAttempt, gotJob, gotWorker, gotRequest, err)
	}
	for _, tc := range []struct {
		name    string
		loadErr error
		wantErr bool
	}{
		{name: "not pending", loadErr: store.ErrNoQueuedAgentTask},
		{name: "database unavailable", loadErr: errors.New("database unavailable"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actions := []string{}
			err := retryPendingAdapterStart(context.Background(), pendingStartStoreProbe{err: tc.loadErr}, dispatchAdapterProbe{actions: &actions}, "worker-1", request)
			if (err != nil) != tc.wantErr || len(actions) != 0 {
				t.Fatalf("retry actions=%v error=%v, want error=%t", actions, err, tc.wantErr)
			}
		})
	}
}

func TestExecutionRequestFromPayloadRequiresExactImmutableTuple(t *testing.T) {
	taskID, planID := uuid.New(), uuid.New()
	request, err := executionRequestFromPayload(map[string]any{
		"task_id": taskID.String(), "task_revision": float64(3),
		"plan_id": planID.String(), "plan_revision": float64(2),
		"plan_sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	})
	if err != nil || request.TaskID != taskID || request.PlanID != planID || request.TaskRevision != 3 || request.PlanRevision != 2 {
		t.Fatalf("request=%#v err=%v", request, err)
	}
	if _, err := executionRequestFromPayload(map[string]any{"task_id": taskID.String()}); err == nil {
		t.Fatal("missing immutable tuple should fail")
	}
}

func TestCancellationRequestFromPayloadRequiresExactAdapterAttempt(t *testing.T) {
	taskID, attemptID := uuid.New(), uuid.New()
	request, err := cancellationRequestFromPayload(map[string]any{
		"task_id": taskID.String(), "attempt_id": attemptID.String(), "adapter_job_id": "adapter-job-1",
	})
	if err != nil || request.TaskID != taskID || request.AttemptID != attemptID || request.AdapterJobID != "adapter-job-1" {
		t.Fatalf("request=%#v err=%v", request, err)
	}
	if _, err := cancellationRequestFromPayload(map[string]any{"task_id": taskID.String(), "attempt_id": attemptID.String()}); err == nil {
		t.Fatal("missing adapter job must fail")
	}
}
