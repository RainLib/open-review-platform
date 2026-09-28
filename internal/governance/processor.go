package governance

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

type Processor struct {
	Store              ExecutorStore
	Cipher             ArtifactCipher
	WorkerID           string
	Lease              time.Duration
	ArtifactTTL        time.Duration
	MaxRecords         int
	Now                func() time.Time
	RegionMigrator     RegionMigrator
	RegionPollInterval time.Duration
	TaskStarted        func() func()
}

type ExecutorStore interface {
	ClaimDataGovernanceJob(context.Context, string, time.Duration) (*domain.DataGovernanceJobTarget, error)
	RenewDataGovernanceJobClaim(context.Context, uuid.UUID, string, time.Duration) error
	BuildDataGovernanceExport(context.Context, domain.DataGovernanceJobTarget, int) ([]byte, map[string]int64, error)
	ExecuteDataGovernanceDeletion(context.Context, domain.DataGovernanceJobTarget) (map[string]any, error)
	CompleteDataGovernanceExport(context.Context, domain.DataGovernanceJobTarget, domain.DataGovernanceArtifact, map[string]any) error
	CompleteDataGovernanceOperation(context.Context, domain.DataGovernanceJobTarget, map[string]any) error
	DeferDataGovernanceRegionMigration(context.Context, domain.DataGovernanceJobTarget, string, int, map[string]any, time.Time) error
	CompleteDataGovernanceRegionMigration(context.Context, domain.DataGovernanceJobTarget, domain.DataResidency, map[string]any) error
	FailDataGovernanceJob(context.Context, domain.DataGovernanceJobTarget, string, string) error
}

func (p Processor) RunOnce(ctx context.Context) (bool, error) {
	lease := p.Lease
	if lease == 0 {
		lease = 2 * time.Minute
	}
	target, err := p.Store.ClaimDataGovernanceJob(ctx, p.WorkerID, lease)
	if errors.Is(err, store.ErrNoQueuedGovernanceJob) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	done := func() {}
	if p.TaskStarted != nil {
		if release := p.TaskStarted(); release != nil {
			done = release
		}
	}
	defer done()
	if target.Kind == domain.DataJobDeletion {
		receipt, deleteErr := p.Store.ExecuteDataGovernanceDeletion(ctx, *target)
		if deleteErr != nil {
			code := "deletion_failed"
			if errors.Is(deleteErr, store.ErrLegalHold) {
				code = "legal_hold_active"
			}
			return true, p.fail(ctx, *target, code, deleteErr)
		}
		if err := p.Store.CompleteDataGovernanceOperation(ctx, *target, receipt); err != nil {
			return true, err
		}
		return true, nil
	}
	if target.Kind == domain.DataJobRegionMigration {
		if p.RegionMigrator == nil {
			return true, p.Store.FailDataGovernanceJob(ctx, *target, "region_migration_executor_not_configured", "Region migration requires a deployment orchestrator; residency was not changed.")
		}
		result, migrateErr := p.RegionMigrator.Reconcile(ctx, *target)
		if migrateErr != nil {
			if target.Attempt >= 5 {
				return true, p.fail(ctx, *target, "region_orchestrator_unreachable", migrateErr)
			}
			backoff := time.Duration(1<<min(target.Attempt, 4)) * 15 * time.Second
			receipt := map[string]any{"schema": "open-review.region-migration-retry.v1", "error": migrateErr.Error(), "attempt": target.Attempt}
			return true, p.Store.DeferDataGovernanceRegionMigration(ctx, *target, target.ExternalOperationID, 0, receipt, p.now().Add(backoff))
		}
		switch result.Status {
		case "accepted", "running":
			poll := p.RegionPollInterval
			if poll == 0 {
				poll = 30 * time.Second
			}
			return true, p.Store.DeferDataGovernanceRegionMigration(ctx, *target, result.OperationID, result.Progress, result.Receipt, p.now().Add(poll))
		case "completed":
			return true, p.Store.CompleteDataGovernanceRegionMigration(ctx, *target, result.Observed, result.Receipt)
		case "failed":
			return true, p.Store.FailDataGovernanceJob(ctx, *target, "region_migration_failed", result.Message)
		default:
			return true, p.Store.FailDataGovernanceJob(ctx, *target, "region_migration_invalid_state", "Region orchestrator returned an unsupported state.")
		}
	}
	if target.Kind != domain.DataJobExport {
		code := "executor_not_configured"
		message := "This operation requires a deployment-specific executor and was not performed."
		return true, p.Store.FailDataGovernanceJob(ctx, *target, code, message)
	}
	maxRecords := p.MaxRecords
	if maxRecords == 0 {
		maxRecords = 10000
	}
	workCtx, cancelWork := context.WithCancel(ctx)
	leaseErrors := make(chan error, 1)
	go p.renewDuring(workCtx, cancelWork, *target, lease, leaseErrors)
	payload, counts, err := p.Store.BuildDataGovernanceExport(workCtx, *target, maxRecords)
	cancelWork()
	select {
	case leaseErr := <-leaseErrors:
		if leaseErr != nil {
			return true, leaseErr
		}
	default:
	}
	if err != nil {
		return true, p.fail(ctx, *target, "export_build_failed", err)
	}
	if err := p.Store.RenewDataGovernanceJobClaim(ctx, target.ID, target.WorkerID, lease); err != nil {
		return true, err
	}
	artifact, err := p.Cipher.Encrypt(target.ID.String(), target.TenantID.String(), payload)
	if err != nil {
		return true, p.fail(ctx, *target, "artifact_encryption_failed", err)
	}
	now := time.Now().UTC()
	if p.Now != nil {
		now = p.Now().UTC()
	}
	ttl := p.ArtifactTTL
	if ttl == 0 {
		ttl = 24 * time.Hour
	}
	artifact.JobID = target.ID
	artifact.TenantID = target.TenantID
	artifact.Filename = "open-review-export-" + target.ID.String() + ".json"
	artifact.ContentType = "application/json"
	artifact.ExpiresAt = now.Add(ttl)
	receipt := map[string]any{
		"schema":           "open-review.data-export-receipt.v1",
		"counts":           counts,
		"plaintext_sha256": artifact.PlaintextSHA256,
		"plaintext_bytes":  artifact.PlaintextBytes,
		"key_version":      artifact.KeyVersion,
		"expires_at":       artifact.ExpiresAt,
	}
	if err := p.Store.CompleteDataGovernanceExport(ctx, *target, artifact, receipt); err != nil {
		return true, err
	}
	return true, nil
}

func (p Processor) now() time.Time {
	if p.Now != nil {
		return p.Now().UTC()
	}
	return time.Now().UTC()
}

func (p Processor) renewDuring(ctx context.Context, cancel context.CancelFunc, target domain.DataGovernanceJobTarget, lease time.Duration, errors chan<- error) {
	interval := lease / 3
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := p.Store.RenewDataGovernanceJobClaim(ctx, target.ID, target.WorkerID, lease); err != nil {
				select {
				case errors <- err:
				default:
				}
				cancel()
				return
			}
		}
	}
}

func (p Processor) fail(ctx context.Context, target domain.DataGovernanceJobTarget, code string, cause error) error {
	message := cause.Error()
	if len(message) > 500 {
		message = message[:500]
	}
	if err := p.Store.FailDataGovernanceJob(ctx, target, code, message); err != nil {
		return fmt.Errorf("record data governance failure after %v: %w", cause, err)
	}
	return nil
}
