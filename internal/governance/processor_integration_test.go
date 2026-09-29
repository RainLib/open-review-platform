package governance_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/governance"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestProcessorCompletesEncryptedTenantExport(t *testing.T) {
	databaseURL := os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("OPEN_REVIEW_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	database, err := store.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	slug := "processor-" + uuid.NewString()[:8]
	if _, err := database.CreateTenant(ctx, "owner", slug, "Governance Processor Integration"); err != nil {
		t.Fatal(err)
	}
	cleanupPool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupPool.Close()
	defer func() { _, _ = cleanupPool.Exec(context.Background(), `DELETE FROM tenants WHERE slug=$1`, slug) }()
	job, err := database.CreateDataGovernanceJob(ctx, "owner", slug, domain.DataGovernanceJobInput{
		Kind: domain.DataJobExport, ScopeKind: domain.DataScopeTenant,
		DataClasses:    []domain.DataClass{domain.DataClassAudit, domain.DataClassOperationalLog},
		IdempotencyKey: "processor-export-0001", Reason: "verify encrypted export lifecycle",
	})
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index + 1)
	}
	cipher, err := governance.NewArtifactCipher(base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	processor := governance.Processor{
		Store: database, Cipher: cipher, WorkerID: "integration-worker", Lease: time.Minute,
		ArtifactTTL: time.Hour, MaxRecords: 100,
	}
	worked, err := processor.RunOnce(ctx)
	if err != nil || !worked {
		t.Fatalf("worked=%v error=%v", worked, err)
	}
	artifact, err := database.GetDataGovernanceArtifact(ctx, "owner", slug, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := cipher.Decrypt(artifact)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Schema string           `json:"schema"`
		Counts map[string]int64 `json:"counts"`
	}
	if err := json.Unmarshal(plaintext, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Schema != "open-review.data-export.v1" || envelope.Counts[string(domain.DataClassAudit)] < 1 {
		t.Fatalf("export envelope=%#v", envelope)
	}
	overview, err := database.GetDataGovernanceOverview(ctx, "owner", slug)
	if err != nil || len(overview.Jobs) != 1 || overview.Jobs[0].State != domain.DataJobCompleted || !overview.Jobs[0].ArtifactReady {
		t.Fatalf("overview=%#v error=%v", overview, err)
	}
}

func TestProcessorCompletesSignedRegionMigrationWorkflow(t *testing.T) {
	databaseURL := os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("OPEN_REVIEW_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	database, err := store.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	slug := "region-processor-" + uuid.NewString()[:8]
	tenant, err := database.CreateTenant(ctx, "owner", slug, "Region Processor Integration")
	if err != nil {
		t.Fatal(err)
	}
	cleanupPool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupPool.Close()
	defer func() { _, _ = cleanupPool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenant.ID) }()
	if _, err := cleanupPool.Exec(ctx, `INSERT INTO memberships (tenant_id,subject,role) VALUES ($1,'owner-two','owner')`, tenant.ID); err != nil {
		t.Fatal(err)
	}
	job, err := database.CreateDataGovernanceJob(ctx, "owner", slug, domain.DataGovernanceJobInput{
		Kind: domain.DataJobRegionMigration, ScopeKind: domain.DataScopeTenant, DesiredRegion: "eu-west-1",
		IdempotencyKey: "processor-region-0001", Reason: "verify signed orchestrator lifecycle",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.DecideDataGovernanceJob(ctx, "owner-two", slug, job.ID, domain.GovernanceDecisionInput{Decision: "approved", Reason: "independent deployment approval", ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	secret := "01234567890123456789012345678901"
	observedAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	orchestratorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get("X-Open-Review-Signature-256") != testRegionSignature(secret, body) {
			t.Errorf("invalid command signature")
		}
		var command struct {
			Action string `json:"action"`
		}
		_ = json.Unmarshal(body, &command)
		response := map[string]any{"schema": "open-review.region-migration-result.v1", "status": "accepted", "operation_id": "region-operation-7", "progress": 10}
		if command.Action == "reconcile" {
			response = map[string]any{
				"schema": "open-review.region-migration-result.v1", "status": "completed", "operation_id": "region-operation-7", "progress": 100,
				"observed": map[string]any{"primary_region": "eu-west-1", "object_region": "eu-west-1", "queue_region": "eu-west-1", "model_boundary": "eu-west-1", "observed_at": observedAt},
			}
		}
		encoded, _ := json.Marshal(response)
		w.Header().Set("X-Open-Review-Signature-256", testRegionSignature(secret, encoded))
		_, _ = w.Write(encoded)
	}))
	defer orchestratorServer.Close()
	orchestrator, err := governance.NewHTTPRegionOrchestrator(governance.RegionOrchestratorOptions{
		Endpoint: orchestratorServer.URL, Secret: secret, AllowPrivateNetworks: true, AllowInsecureHTTP: true, HTTPClient: orchestratorServer.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	processor := governance.Processor{Store: database, WorkerID: "region-integration-worker", Lease: time.Minute, RegionMigrator: orchestrator, RegionPollInterval: time.Minute}
	if worked, err := processor.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("start worked=%v error=%v", worked, err)
	}
	if _, err := cleanupPool.Exec(ctx, `UPDATE data_governance_jobs SET available_at=now()-interval '1 second' WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	if worked, err := processor.RunOnce(ctx); err != nil || !worked {
		t.Fatalf("reconcile worked=%v error=%v", worked, err)
	}
	overview, err := database.GetDataGovernanceOverview(ctx, "owner", slug)
	if err != nil || overview.Residency.PrimaryRegion != "eu-west-1" || overview.Residency.ObservedAt == nil || len(overview.Jobs) != 1 || overview.Jobs[0].State != domain.DataJobCompleted {
		t.Fatalf("overview=%#v error=%v", overview, err)
	}
}

func testRegionSignature(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
