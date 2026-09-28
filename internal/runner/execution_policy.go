package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/modelroute"
	"github.com/RainLib/open-review-platform/internal/rules"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
)

// These read-only interfaces deliberately expose no publication, run-state,
// finding aggregation, or merge-gate operation. Isolated policy experiments
// can share execution configuration without acquiring the review lifecycle.
type ModelRouteSnapshotReader interface {
	ModelRouteForJob(context.Context, uuid.UUID) (domain.ReviewConfigSnapshot, error)
}

type ReviewConfigSnapshotReader interface {
	ReviewConfigSnapshotForJob(context.Context, uuid.UUID, domain.ReviewConfigSection) (domain.ReviewConfigSnapshot, error)
}

type PublicationMinimumReader interface {
	PublicationMinimumForJob(context.Context, uuid.UUID) (string, error)
}

// ExecutionPolicy reads only the immutable snapshots admitted for a source
// job. Strict replay rejects legacy/deployment-default routes rather than
// presenting a run under today's environment as a comparable historical run.
type ExecutionPolicy struct {
	ModelRoutes        ModelRouteSnapshotReader
	Configurations     ReviewConfigSnapshotReader
	PublicationMinimum PublicationMinimumReader
	ModelSecrets       modelroute.SecretResolver
	Logger             *slog.Logger
	RequireSnapshots   bool
}

const replayRecovery = "configure a governed Models route, run a new source review to capture its immutable route and execution plan, then replay"

func (p ExecutionPolicy) WithModelRoute(ctx context.Context, jobID uuid.UUID) (context.Context, error) {
	if p.ModelRoutes == nil {
		if p.RequireSnapshots {
			return nil, fmt.Errorf("source model snapshot reader is unavailable; %s", replayRecovery)
		}
		return ctx, nil
	}
	snapshot, err := p.ModelRoutes.ModelRouteForJob(ctx, jobID)
	if errors.Is(err, store.ErrNotFound) && !p.RequireSnapshots {
		return ctx, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load source model route: %w; %s", err, replayRecovery)
	}
	if err := p.validateSnapshot(snapshot, domain.ReviewConfigModels); err != nil {
		return nil, err
	}
	route, err := domain.DecodeModelRoute(snapshot.Content)
	if err != nil {
		return nil, fmt.Errorf("decode model route snapshot: %w", err)
	}
	if !route.Enabled {
		if p.RequireSnapshots {
			return nil, fmt.Errorf("source review used mutable deployment model defaults, not a retained model route; %s", replayRecovery)
		}
		return ctx, nil
	}
	if p.ModelSecrets == nil {
		return nil, fmt.Errorf("model credential resolver is not configured")
	}
	token, err := p.ModelSecrets.ResolveModelCredential(ctx, route.CredentialRef)
	if err != nil {
		return nil, fmt.Errorf("resolve model credential: %w", err)
	}
	if p.Logger != nil {
		p.Logger.Info("using immutable model route", "job_id", jobID, "provider", route.Provider, "model", route.Model, "route_sha256", snapshot.ContentSHA256)
	}
	return modelroute.WithExecution(ctx, modelroute.Execution{Route: route, Token: token}), nil
}

func (p ExecutionPolicy) WithReviewPrompts(ctx context.Context, jobID uuid.UUID) (context.Context, error) {
	snapshot, found, err := p.configuration(ctx, jobID, domain.ReviewConfigPrompts)
	if err != nil || !found {
		return ctx, err
	}
	prompts, err := domain.DecodeReviewPromptConfig(snapshot.Content)
	if err != nil {
		return nil, fmt.Errorf("decode review prompts snapshot: %w", err)
	}
	if p.Logger != nil {
		p.Logger.Info("using immutable review prompts", "job_id", jobID, "prompt_config_sha256", snapshot.ContentSHA256, "max_prompt_tokens", prompts.MaxPromptTokens, "repository_instructions", prompts.AllowRepositoryInstructions)
	}
	return modelroute.WithPromptExecution(ctx, modelroute.PromptExecution{
		SystemInstruction: prompts.SystemInstruction, RepositoryContext: prompts.RepositoryContext,
		MaxPromptTokens: prompts.MaxPromptTokens, AllowRepositoryInstructions: prompts.AllowRepositoryInstructions,
	}), nil
}

// CategoryPolicy can be loaded before model execution so a missing/corrupt
// source policy fails closed without spending model tokens. A nil legacy
// policy preserves all findings, matching the normal runner's old behavior.
func (p ExecutionPolicy) CategoryPolicy(ctx context.Context, jobID uuid.UUID) (domain.ReviewCategoriesConfig, error) {
	snapshot, found, err := p.configuration(ctx, jobID, domain.ReviewConfigCategories)
	if err != nil || !found {
		return nil, err
	}
	policy, err := domain.DecodeReviewCategoriesConfig(snapshot.Content)
	if err != nil {
		return nil, fmt.Errorf("decode category policy snapshot: %w", err)
	}
	return policy, nil
}

func (p ExecutionPolicy) ApplyCategoryPolicy(ctx context.Context, jobID uuid.UUID, findings []domain.Finding) ([]domain.Finding, error) {
	policy, err := p.CategoryPolicy(ctx, jobID)
	if err != nil {
		return nil, err
	}
	kept, suppressed := policy.FilterFindings(findings)
	if suppressed > 0 && p.Logger != nil {
		p.Logger.Info("applied immutable category policy", "job_id", jobID, "kept_findings", len(kept), "suppressed_findings", suppressed)
	}
	return kept, nil
}

func (p ExecutionPolicy) ApplyPublicationMinimum(ctx context.Context, jobID uuid.UUID, findings []domain.Finding, blockingMinimum string) ([]domain.Finding, error) {
	if p.PublicationMinimum == nil {
		if p.RequireSnapshots {
			return nil, fmt.Errorf("source publication minimum snapshot reader is unavailable")
		}
		return findings, nil
	}
	minimum, err := p.PublicationMinimum.PublicationMinimumForJob(ctx, jobID)
	if errors.Is(err, store.ErrNotFound) && !p.RequireSnapshots {
		return findings, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load source publication minimum: %w", err)
	}
	kept, suppressed, err := domain.FilterFindingsByPublicationMinimum(findings, minimum, blockingMinimum)
	if err != nil {
		return nil, fmt.Errorf("apply source publication minimum: %w", err)
	}
	if suppressed > 0 && p.Logger != nil {
		p.Logger.Info("applied immutable installation publication minimum", "job_id", jobID, "minimum_severity", minimum, "kept_findings", len(kept), "suppressed_findings", suppressed)
	}
	return kept, nil
}

func (p ExecutionPolicy) configuration(ctx context.Context, jobID uuid.UUID, section domain.ReviewConfigSection) (domain.ReviewConfigSnapshot, bool, error) {
	if p.Configurations == nil {
		if p.RequireSnapshots {
			return domain.ReviewConfigSnapshot{}, false, fmt.Errorf("source %s snapshot reader is unavailable; %s", section, replayRecovery)
		}
		return domain.ReviewConfigSnapshot{}, false, nil
	}
	snapshot, err := p.Configurations.ReviewConfigSnapshotForJob(ctx, jobID, section)
	if errors.Is(err, store.ErrNotFound) && !p.RequireSnapshots {
		return domain.ReviewConfigSnapshot{}, false, nil
	}
	if err != nil {
		return domain.ReviewConfigSnapshot{}, false, fmt.Errorf("load source %s snapshot: %w; %s", section, err, replayRecovery)
	}
	if err := p.validateSnapshot(snapshot, section); err != nil {
		return domain.ReviewConfigSnapshot{}, false, err
	}
	return snapshot, true, nil
}

func (p ExecutionPolicy) validateSnapshot(snapshot domain.ReviewConfigSnapshot, section domain.ReviewConfigSection) error {
	if !p.RequireSnapshots {
		return nil
	}
	_, digest, valid := domain.CanonicalReviewConfig(section, snapshot.Content)
	if !valid || snapshot.Section != section || digest != snapshot.ContentSHA256 {
		return fmt.Errorf("source %s snapshot is invalid or its content hash does not match; %s", section, replayRecovery)
	}
	return nil
}

// CompileExecutionRuleFile renders control-plane prompt policy and a frozen
// rule snapshot identically for production and isolated execution. Repository
// instructions retain the explicit opt-in and bounded in-workspace reads.
func CompileExecutionRuleFile(ctx context.Context, directory string, snapshot *domain.RuleSnapshot) (rules.OCRRuleFile, error) {
	prompt, err := reviewPromptRule(ctx, directory)
	if err != nil {
		return rules.OCRRuleFile{}, err
	}
	var file rules.OCRRuleFile
	if snapshot != nil {
		var compiled rules.Snapshot
		if err := json.Unmarshal(snapshot.CanonicalPayload, &compiled); err != nil {
			return file, fmt.Errorf("decode rule snapshot %s: %w", snapshot.ID, err)
		}
		file, err = rules.OCRRuleFileForSnapshot(compiled)
		if err != nil {
			return file, fmt.Errorf("compile OCR rule file from snapshot %s: %w", snapshot.ID, err)
		}
	}
	if prompt != "" {
		if len(file.Rules) == 0 {
			file.Rules = []rules.OCRRule{{Path: "**/*", Rule: prompt, MergeSystemRule: true}}
		} else {
			file.Rules[0].Rule += "\n\n---\n\n" + prompt
		}
	}
	return file, nil
}
