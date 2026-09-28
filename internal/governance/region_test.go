package governance

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

func TestHTTPRegionOrchestratorSignsStartAndReconcileAndRequiresObservedCompletion(t *testing.T) {
	secret := "01234567890123456789012345678901"
	observedAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	actions := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var command map[string]any
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get("X-Open-Review-Signature-256") != regionSignature([]byte(secret), body) {
			t.Errorf("request signature=%q", r.Header.Get("X-Open-Review-Signature-256"))
		}
		if err := json.Unmarshal(body, &command); err != nil {
			t.Error(err)
		}
		action, _ := command["action"].(string)
		actions = append(actions, action)
		result := map[string]any{
			"schema": "open-review.region-migration-result.v1",
			"status": "accepted", "operation_id": "migration-42", "progress": 5,
		}
		if action == "reconcile" {
			result = map[string]any{
				"schema": "open-review.region-migration-result.v1",
				"status": "completed", "operation_id": "migration-42", "progress": 100,
				"observed": map[string]any{
					"primary_region": "eu-west-1", "backup_region": "eu-central-1",
					"object_region": "eu-west-1", "queue_region": "eu-west-1",
					"model_boundary": "eu-west-1", "observed_at": observedAt,
				},
			}
		}
		encoded, _ := json.Marshal(result)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Open-Review-Signature-256", regionSignature([]byte(secret), encoded))
		_, _ = w.Write(encoded)
	}))
	defer server.Close()
	orchestrator, err := NewHTTPRegionOrchestrator(RegionOrchestratorOptions{
		Endpoint: server.URL, Secret: secret, AllowPrivateNetworks: true, AllowInsecureHTTP: true, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	target := domain.DataGovernanceJobTarget{ID: uuid.New(), TenantID: uuid.New(), Kind: domain.DataJobRegionMigration, DesiredRegion: "eu-west-1", Attempt: 1}
	accepted, err := orchestrator.Reconcile(context.Background(), target)
	if err != nil || accepted.Status != "accepted" || accepted.OperationID != "migration-42" {
		t.Fatalf("accepted=%#v error=%v", accepted, err)
	}
	target.ExternalOperationID = accepted.OperationID
	completed, err := orchestrator.Reconcile(context.Background(), target)
	if err != nil || completed.Status != "completed" || completed.Observed.PrimaryRegion != target.DesiredRegion || completed.Observed.ObservedAt == nil {
		t.Fatalf("completed=%#v error=%v", completed, err)
	}
	if len(actions) != 2 || actions[0] != "start" || actions[1] != "reconcile" {
		t.Fatalf("actions=%v", actions)
	}
}

func TestHTTPRegionOrchestratorRejectsUnsignedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"schema":"open-review.region-migration-result.v1","status":"accepted","operation_id":"x","progress":1}`))
	}))
	defer server.Close()
	orchestrator, err := NewHTTPRegionOrchestrator(RegionOrchestratorOptions{
		Endpoint: server.URL, Secret: "01234567890123456789012345678901", AllowPrivateNetworks: true, AllowInsecureHTTP: true, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = orchestrator.Reconcile(context.Background(), domain.DataGovernanceJobTarget{ID: uuid.New(), TenantID: uuid.New(), Kind: domain.DataJobRegionMigration, DesiredRegion: "eu-west-1"})
	if err == nil {
		t.Fatal("unsigned response must be rejected")
	}
}
