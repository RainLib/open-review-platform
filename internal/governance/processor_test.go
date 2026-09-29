package governance

import (
	"context"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

type processorStore struct {
	target              domain.DataGovernanceJobTarget
	deleteReceipt       map[string]any
	completed           bool
	failedCode          string
	deferred            bool
	deferredOperationID string
	deferredProgress    int
	observedRegion      string
}

func (s *processorStore) ClaimDataGovernanceJob(context.Context, string, time.Duration) (*domain.DataGovernanceJobTarget, error) {
	target := s.target
	return &target, nil
}
func (*processorStore) RenewDataGovernanceJobClaim(context.Context, uuid.UUID, string, time.Duration) error {
	return nil
}
func (*processorStore) BuildDataGovernanceExport(context.Context, domain.DataGovernanceJobTarget, int) ([]byte, map[string]int64, error) {
	return nil, nil, nil
}
func (s *processorStore) ExecuteDataGovernanceDeletion(context.Context, domain.DataGovernanceJobTarget) (map[string]any, error) {
	return s.deleteReceipt, nil
}
func (*processorStore) CompleteDataGovernanceExport(context.Context, domain.DataGovernanceJobTarget, domain.DataGovernanceArtifact, map[string]any) error {
	return nil
}
func (s *processorStore) CompleteDataGovernanceOperation(context.Context, domain.DataGovernanceJobTarget, map[string]any) error {
	s.completed = true
	return nil
}
func (s *processorStore) DeferDataGovernanceRegionMigration(_ context.Context, _ domain.DataGovernanceJobTarget, operationID string, progress int, _ map[string]any, _ time.Time) error {
	s.deferred = true
	s.deferredOperationID = operationID
	s.deferredProgress = progress
	return nil
}
func (s *processorStore) CompleteDataGovernanceRegionMigration(_ context.Context, _ domain.DataGovernanceJobTarget, observed domain.DataResidency, _ map[string]any) error {
	s.completed = true
	s.observedRegion = observed.PrimaryRegion
	return nil
}
func (s *processorStore) FailDataGovernanceJob(_ context.Context, _ domain.DataGovernanceJobTarget, code, _ string) error {
	s.failedCode = code
	return nil
}

type staticRegionMigrator struct {
	result RegionMigrationResult
	err    error
}

func (m staticRegionMigrator) Reconcile(context.Context, domain.DataGovernanceJobTarget) (RegionMigrationResult, error) {
	return m.result, m.err
}

func TestProcessorDefersAndCompletesRegionMigrationFromOrchestratorEvidence(t *testing.T) {
	target := domain.DataGovernanceJobTarget{ID: uuid.New(), TenantID: uuid.New(), Kind: domain.DataJobRegionMigration, DesiredRegion: "eu-west-1", WorkerID: "worker", Attempt: 1}
	backend := &processorStore{target: target}
	now := time.Now().UTC()
	processor := Processor{
		Store: backend, WorkerID: "worker", Now: func() time.Time { return now },
		RegionMigrator: staticRegionMigrator{result: RegionMigrationResult{Status: "accepted", OperationID: "operation-42", Progress: 10, Receipt: map[string]any{"status": "accepted"}}},
	}
	worked, err := processor.RunOnce(context.Background())
	if err != nil || !worked || !backend.deferred || backend.deferredOperationID != "operation-42" || backend.deferredProgress != 10 {
		t.Fatalf("worked=%v deferred=%v operation=%q progress=%d error=%v", worked, backend.deferred, backend.deferredOperationID, backend.deferredProgress, err)
	}
	observedAt := now.Add(-time.Minute)
	backend.deferred = false
	backend.target.ExternalOperationID = "operation-42"
	processor.RegionMigrator = staticRegionMigrator{result: RegionMigrationResult{
		Status: "completed", OperationID: "operation-42", Progress: 100,
		Observed: domain.DataResidency{PrimaryRegion: "eu-west-1", ObservedAt: &observedAt},
		Receipt:  map[string]any{"status": "completed"},
	}}
	worked, err = processor.RunOnce(context.Background())
	if err != nil || !worked || !backend.completed || backend.observedRegion != "eu-west-1" {
		t.Fatalf("worked=%v completed=%v observed=%q error=%v", worked, backend.completed, backend.observedRegion, err)
	}
}

func TestProcessorExecutesApprovedDeletionButFailsRegionMigrationClosed(t *testing.T) {
	target := domain.DataGovernanceJobTarget{
		ID: uuid.New(), TenantID: uuid.New(), Kind: domain.DataJobDeletion,
		ScopeKind: domain.DataScopeTenant, DataClasses: []domain.DataClass{domain.DataClassFindings}, WorkerID: "worker",
	}
	backend := &processorStore{target: target, deleteReceipt: map[string]any{"schema": "open-review.data-erasure-receipt.v1"}}
	worked, err := (Processor{Store: backend, WorkerID: "worker"}).RunOnce(context.Background())
	if err != nil || !worked || !backend.completed || backend.failedCode != "" {
		t.Fatalf("worked=%v completed=%v failed=%q error=%v", worked, backend.completed, backend.failedCode, err)
	}
	backend.completed = false
	backend.target.Kind = domain.DataJobRegionMigration
	worked, err = (Processor{Store: backend, WorkerID: "worker"}).RunOnce(context.Background())
	if err != nil || !worked || backend.completed || backend.failedCode != "region_migration_executor_not_configured" {
		t.Fatalf("worked=%v completed=%v failed=%q error=%v", worked, backend.completed, backend.failedCode, err)
	}
}
