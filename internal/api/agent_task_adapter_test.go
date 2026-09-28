package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/agentadapter"
	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

type adapterEventStore struct {
	*recordingStore
	event     domain.AgentTaskAdapterEvent
	lease     time.Duration
	duplicate bool
	err       error
}

type adapterStartStore struct {
	*recordingStore
	attemptID uuid.UUID
	jobID     string
	err       error
}

func (backend *adapterStartStore) ClaimAgentTaskAdapterStart(_ context.Context, attemptID uuid.UUID, jobID string) error {
	backend.attemptID, backend.jobID = attemptID, jobID
	return backend.err
}

func TestAgentTaskAdapterStartRequiresHMACAndOneDurableClaim(t *testing.T) {
	backend := &adapterStartStore{recordingStore: &recordingStore{}}
	server := New(backend, fixedAuthenticator{}, "", "")
	secret := "adapter-callback-secret-must-be-at-least-32-bytes"
	server.SetAgentTaskAdapterCallback(secret, time.Minute)
	server.now = func() time.Time { return time.Unix(1_800_000_000, 0) }
	mux := http.NewServeMux()
	server.Register(mux)
	attemptID := uuid.New()
	body, _ := json.Marshal(map[string]string{"attempt_id": attemptID.String(), "adapter_job_id": "adapter-job-17"})
	timestamp := "1800000000"
	request := httptest.NewRequest(http.MethodPost, "/v1/agent-adapter/starts", bytes.NewReader(body))
	request.Header.Set(agentadapter.HeaderTimestamp, timestamp)
	request.Header.Set(agentadapter.HeaderSignature, agentadapter.Sign(secret, timestamp, body))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || backend.attemptID != attemptID || backend.jobID != "adapter-job-17" {
		t.Fatalf("start gate status=%d attempt=%s job=%q", response.Code, backend.attemptID, backend.jobID)
	}
	backend.err = store.ErrAgentTaskClaimLost
	request = httptest.NewRequest(http.MethodPost, "/v1/agent-adapter/starts", bytes.NewReader(body))
	request.Header.Set(agentadapter.HeaderTimestamp, timestamp)
	request.Header.Set(agentadapter.HeaderSignature, agentadapter.Sign(secret, timestamp, body))
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("replayed start status=%d, want 409", response.Code)
	}
	request = httptest.NewRequest(http.MethodPost, "/v1/agent-adapter/starts", bytes.NewReader(body))
	request.Header.Set(agentadapter.HeaderTimestamp, timestamp)
	request.Header.Set(agentadapter.HeaderSignature, "sha256=invalid")
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned start status=%d, want 401", response.Code)
	}
}

func (store *adapterEventStore) RecordAgentTaskAdapterEvent(_ context.Context, event domain.AgentTaskAdapterEvent, lease time.Duration) (domain.AgentTaskAttempt, bool, error) {
	store.event = event
	store.lease = lease
	if store.err != nil {
		return domain.AgentTaskAttempt{}, false, store.err
	}
	return domain.AgentTaskAttempt{ID: event.AttemptID, State: "running"}, store.duplicate, nil
}

func TestAgentTaskAdapterEventRequiresValidHMACAndForwardsEvent(t *testing.T) {
	backend := &adapterEventStore{recordingStore: &recordingStore{}}
	server := New(backend, fixedAuthenticator{}, "", "")
	secret := "adapter-callback-secret-must-be-at-least-32-bytes"
	server.SetAgentTaskAdapterCallback(secret, time.Minute)
	server.now = func() time.Time { return time.Unix(1_800_000_000, 0) }
	mux := http.NewServeMux()
	server.Register(mux)
	event := domain.AgentTaskAdapterEvent{
		AttemptID:    uuid.New(),
		AdapterJobID: "sandbox-job-17",
		DeliveryID:   "delivery-17",
		Kind:         "heartbeat",
	}
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	timestamp := "1800000000"
	request := httptest.NewRequest(http.MethodPost, "/v1/agent-adapter/events", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(agentadapter.HeaderTimestamp, timestamp)
	request.Header.Set(agentadapter.HeaderSignature, agentadapter.Sign(secret, timestamp, body))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if backend.event != event || backend.lease != time.Minute {
		t.Fatalf("callback was not forwarded: event=%+v lease=%s", backend.event, backend.lease)
	}
	checkpoint := event
	checkpoint.DeliveryID = "delivery-pre-push-17"
	checkpoint.Kind = "publication_checkpoint"
	checkpoint.BranchName = "agent/bounded-task"
	checkpoint.HeadSHA = "0123456789abcdef0123456789abcdef01234567"
	checkpoint.PatchSHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	checkpoint.ChangedFileCount = 1
	checkpoint.DiffBytes = 80
	checkpointBody, _ := json.Marshal(checkpoint)
	request = httptest.NewRequest(http.MethodPost, "/v1/agent-adapter/events", bytes.NewReader(checkpointBody))
	request.Header.Set(agentadapter.HeaderTimestamp, timestamp)
	request.Header.Set(agentadapter.HeaderSignature, agentadapter.Sign(secret, timestamp, checkpointBody))
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || backend.event != checkpoint {
		t.Fatalf("signed pre-push checkpoint was not forwarded: status=%d event=%+v", response.Code, backend.event)
	}
	checkpoint.PullRequestNumber = 7
	invalidBody, _ := json.Marshal(checkpoint)
	request = httptest.NewRequest(http.MethodPost, "/v1/agent-adapter/events", bytes.NewReader(invalidBody))
	request.Header.Set(agentadapter.HeaderTimestamp, timestamp)
	request.Header.Set(agentadapter.HeaderSignature, agentadapter.Sign(secret, timestamp, invalidBody))
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("pre-push checkpoint smuggled a Draft result: status=%d", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/agent-adapter/events", bytes.NewReader(body))
	request.Header.Set(agentadapter.HeaderTimestamp, timestamp)
	request.Header.Set(agentadapter.HeaderSignature, "sha256=invalid")
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("invalid signature status = %d", response.Code)
	}
}
