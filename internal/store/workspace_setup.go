package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func scanWorkspaceSetupCheckpoint(row pgx.Row) (domain.WorkspaceSetupCheckpoint, error) {
	var checkpoint domain.WorkspaceSetupCheckpoint
	var mode *string
	var exclusions []byte
	if err := row.Scan(
		&checkpoint.CurrentStep,
		&checkpoint.Revision,
		&mode,
		&exclusions,
		&checkpoint.UpdatedBy,
		&checkpoint.UpdatedAt,
		&checkpoint.CompletedAt,
	); err != nil {
		return domain.WorkspaceSetupCheckpoint{}, err
	}
	if mode == nil {
		return checkpoint, nil
	}
	boundary := domain.WorkspaceLearningBoundary{Mode: domain.WorkspaceLearningMode(*mode)}
	if len(exclusions) > 0 && json.Unmarshal(exclusions, &boundary.ReviewerExclusions) != nil {
		return domain.WorkspaceSetupCheckpoint{}, fmt.Errorf("decode persisted setup learning boundary")
	}
	normalized, valid := boundary.Normalize()
	if !valid {
		return domain.WorkspaceSetupCheckpoint{}, fmt.Errorf("validate persisted setup learning boundary")
	}
	checkpoint.LearningBoundary = &normalized
	return checkpoint, nil
}

func scanWorkspaceSetupReadiness(row pgx.Row) (*domain.WorkspaceSetupReadiness, error) {
	var snapshot domain.WorkspaceSetupReadiness
	var installationID uuid.UUID
	var provider string
	var learningMode *string
	if err := row.Scan(
		&snapshot.CheckpointRevision,
		&installationID,
		&provider,
		&snapshot.RepositoryScope,
		&snapshot.GeneralConfigRevision,
		&snapshot.GeneralConfigContentSHA256,
		&learningMode,
		&snapshot.RuleSetCount,
		&snapshot.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	snapshot.InstallationID = installationID.String()
	snapshot.Provider = domain.Provider(provider)
	if learningMode != nil {
		snapshot.LearningMode = domain.WorkspaceLearningMode(*learningMode)
	}
	return &snapshot, nil
}

func loadWorkspaceSetupReadiness(
	ctx context.Context,
	query interface {
		QueryRow(context.Context, string, ...any) pgx.Row
	},
	tenantID uuid.UUID,
) (*domain.WorkspaceSetupReadiness, error) {
	return scanWorkspaceSetupReadiness(query.QueryRow(ctx, `
		SELECT checkpoint_revision, installation_id, provider, repository_scope,
		       general_config_revision, general_config_content_sha256, learning_mode,
		       rule_set_count, created_at
		FROM workspace_setup_readiness_snapshots
		WHERE tenant_id = $1`, tenantID))
}

func createWorkspaceSetupReadiness(
	ctx context.Context,
	tx pgx.Tx,
	tenantID uuid.UUID,
	checkpoint domain.WorkspaceSetupCheckpoint,
) (*domain.WorkspaceSetupReadiness, error) {
	var installationID uuid.UUID
	var provider, repositoryScope, configSHA string
	var configRevision, ruleSetCount int
	err := tx.QueryRow(ctx, `
		SELECT installation.id, installation.provider, installation.repository_scope,
		       version.revision, version.content_sha256,
		       (SELECT COUNT(*) FROM rule_sets WHERE tenant_id = $1)
		FROM provider_installations installation
		JOIN review_configurations configuration
		  ON configuration.tenant_id = installation.tenant_id
		 AND configuration.section = 'general'
		 AND configuration.scope_kind = 'tenant'
		 AND configuration.scope_ref = ''
		 AND configuration.active = TRUE
		JOIN LATERAL (
		  SELECT revision, content_sha256
		  FROM review_configuration_versions
		  WHERE configuration_id = configuration.id
		  ORDER BY revision DESC
		  LIMIT 1
		) version ON TRUE
		WHERE installation.tenant_id = $1
		  AND installation.active = TRUE
		  AND installation.verification_state IN ('legacy', 'verified')
		ORDER BY installation.updated_at DESC, installation.id
		LIMIT 1`, tenantID).Scan(&installationID, &provider, &repositoryScope, &configRevision, &configSHA, &ruleSetCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrInvalidReviewConfig
	}
	if err != nil {
		return nil, fmt.Errorf("resolve setup readiness inputs: %w", err)
	}
	var learningMode *string
	if checkpoint.LearningBoundary != nil {
		mode := string(checkpoint.LearningBoundary.Mode)
		learningMode = &mode
	}
	inserted, err := scanWorkspaceSetupReadiness(tx.QueryRow(ctx, `
		INSERT INTO workspace_setup_readiness_snapshots (
		  tenant_id, checkpoint_revision, installation_id, provider, repository_scope,
		  general_config_revision, general_config_content_sha256, learning_mode, rule_set_count
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (tenant_id) DO NOTHING
		RETURNING checkpoint_revision, installation_id, provider, repository_scope,
		          general_config_revision, general_config_content_sha256, learning_mode,
		          rule_set_count, created_at`, tenantID, checkpoint.Revision, installationID, provider, repositoryScope, configRevision, configSHA, learningMode, ruleSetCount))
	if err != nil {
		return nil, fmt.Errorf("save setup readiness snapshot: %w", err)
	}
	if inserted != nil {
		return inserted, nil
	}
	return loadWorkspaceSetupReadiness(ctx, tx, tenantID)
}

// workspaceSetupAllowsReview is the execution-side counterpart of the
// console setup guard. New workspaces receive a durable checkpoint when their
// first installation is created and may admit work only after the owner has
// completed that baseline. Installations created before checkpoints existed
// have no row and deliberately retain their historical ready state.
//
// The caller must have already resolved an active, verified installation for
// tenantID. Keeping this check in the same admission transaction prevents a
// webhook or command from bypassing the browser-only setup redirect.
func workspaceSetupAllowsReview(
	ctx context.Context,
	query interface {
		QueryRow(context.Context, string, ...any) pgx.Row
	},
	tenantID uuid.UUID,
) (bool, error) {
	var step domain.WorkspaceSetupStep
	err := query.QueryRow(ctx, `
		SELECT current_step
		FROM workspace_setup_checkpoints
		WHERE tenant_id = $1`, tenantID).Scan(&step)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("load workspace setup admission state: %w", err)
	}
	return step == domain.WorkspaceSetupComplete, nil
}

func (s *PostgresStore) GetWorkspaceSetupCheckpoint(ctx context.Context, actor, tenantSlug string) (domain.WorkspaceSetupCheckpoint, error) {
	tenantID, _, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.WorkspaceSetupCheckpoint{}, err
	}
	checkpoint, err := s.loadWorkspaceSetupCheckpoint(ctx, tenantID)
	if errors.Is(err, ErrNotFound) {
		// Existing deployments predate checkpoints. An already-active installation
		// remains usable rather than being retroactively locked out.
		var active bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM provider_installations WHERE tenant_id = $1 AND active = TRUE AND verification_state IN ('legacy','verified'))`, tenantID).Scan(&active); err != nil {
			return domain.WorkspaceSetupCheckpoint{}, fmt.Errorf("inspect setup installation: %w", err)
		}
		if active {
			return domain.WorkspaceSetupCheckpoint{CurrentStep: domain.WorkspaceSetupComplete}, nil
		}
		return domain.WorkspaceSetupCheckpoint{CurrentStep: domain.WorkspaceSetupConnect}, nil
	}
	if err != nil {
		return checkpoint, err
	}
	if checkpoint.CurrentStep == domain.WorkspaceSetupComplete {
		checkpoint.Readiness, err = loadWorkspaceSetupReadiness(ctx, s.pool, tenantID)
		if err != nil {
			return domain.WorkspaceSetupCheckpoint{}, fmt.Errorf("load setup readiness snapshot: %w", err)
		}
	}
	return checkpoint, nil
}

func (s *PostgresStore) UpdateWorkspaceSetupCheckpoint(ctx context.Context, actor, tenantSlug string, input domain.WorkspaceSetupCheckpointInput) (domain.WorkspaceSetupCheckpoint, error) {
	if !input.CurrentStep.Valid() || input.ExpectedRevision < 0 {
		return domain.WorkspaceSetupCheckpoint{}, ErrInvalidReviewConfig
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.WorkspaceSetupCheckpoint{}, fmt.Errorf("begin setup checkpoint update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.WorkspaceSetupCheckpoint{}, err
	}
	if role != "owner" && role != "admin" {
		return domain.WorkspaceSetupCheckpoint{}, ErrForbidden
	}
	current, err := scanWorkspaceSetupCheckpoint(tx.QueryRow(ctx, `
		SELECT current_step, revision, learning_mode, learning_reviewer_exclusions, updated_by, updated_at, completed_at
		FROM workspace_setup_checkpoints
		WHERE tenant_id = $1
		FOR UPDATE`, tenantID))
	if errors.Is(err, pgx.ErrNoRows) {
		if input.ExpectedRevision != 0 {
			return domain.WorkspaceSetupCheckpoint{}, ErrRevisionConflict
		}
		current.CurrentStep, current.Revision = domain.WorkspaceSetupConnect, 0
	} else if err != nil {
		return domain.WorkspaceSetupCheckpoint{}, fmt.Errorf("lock setup checkpoint: %w", err)
	} else if input.CurrentStep == current.CurrentStep && input.ExpectedRevision+1 == current.Revision {
		// The preceding request committed but its response was lost. Return the
		// established checkpoint without duplicating an audit event or revision.
		if current.CurrentStep == domain.WorkspaceSetupComplete {
			current.Readiness, err = loadWorkspaceSetupReadiness(ctx, tx, tenantID)
			if err != nil {
				return domain.WorkspaceSetupCheckpoint{}, fmt.Errorf("load setup readiness retry: %w", err)
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.WorkspaceSetupCheckpoint{}, fmt.Errorf("commit setup checkpoint retry: %w", err)
		}
		return current, nil
	} else if input.ExpectedRevision != current.Revision {
		return domain.WorkspaceSetupCheckpoint{}, ErrRevisionConflict
	}
	if !domain.CanAdvanceWorkspaceSetup(current.CurrentStep, input.CurrentStep) {
		return domain.WorkspaceSetupCheckpoint{}, ErrInvalidReviewConfig
	}
	if input.LearningBoundary != nil && !(current.CurrentStep == domain.WorkspaceSetupLearning && input.CurrentStep == domain.WorkspaceSetupSeverity) {
		return domain.WorkspaceSetupCheckpoint{}, ErrInvalidReviewConfig
	}
	learningBoundary := current.LearningBoundary
	if current.CurrentStep == domain.WorkspaceSetupLearning && input.CurrentStep == domain.WorkspaceSetupSeverity {
		if input.LearningBoundary == nil {
			return domain.WorkspaceSetupCheckpoint{}, ErrInvalidReviewConfig
		}
		normalized, valid := input.LearningBoundary.Normalize()
		if !valid {
			return domain.WorkspaceSetupCheckpoint{}, ErrInvalidReviewConfig
		}
		learningBoundary = &normalized
	}
	if input.CurrentStep != domain.WorkspaceSetupConnect {
		var eligible bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM provider_installations WHERE tenant_id = $1 AND active = TRUE AND verification_state IN ('legacy','verified'))`, tenantID).Scan(&eligible); err != nil {
			return domain.WorkspaceSetupCheckpoint{}, fmt.Errorf("verify setup installation: %w", err)
		}
		if !eligible {
			return domain.WorkspaceSetupCheckpoint{}, ErrInvalidReviewConfig
		}
	}
	// A completed provider identity alone does not prove that this workspace has
	// selected a repository it may review. In particular, GitLab OAuth proves a
	// human authorization while inbound routing is still repository-scoped.
	// Keep the checkpoint at review_scope until the worker-synchronized inventory
	// contains at least one repository covered by an active verified installation.
	if current.CurrentStep == domain.WorkspaceSetupReviewScope && input.CurrentStep == domain.WorkspaceSetupLearning {
		rows, err := tx.Query(ctx, `
			SELECT installation.repository_scope, inventory.name
			FROM provider_installations installation
			JOIN provider_repository_inventory inventory ON inventory.installation_id=installation.id
			LEFT JOIN provider_health_probes probe ON probe.installation_id=installation.id
			WHERE installation.tenant_id=$1
			  AND installation.active=TRUE
			  AND installation.verification_state IN ('legacy','verified')
			  AND (probe.receipt->>'inventory_state' IS NULL OR inventory.last_seen_at=probe.observed_at)
			ORDER BY installation.id, inventory.name`, tenantID)
		if err != nil {
			return domain.WorkspaceSetupCheckpoint{}, fmt.Errorf("list setup repository inventory: %w", err)
		}
		hasAuthorizedRepository := false
		for rows.Next() {
			var scope, repository string
			if err := rows.Scan(&scope, &repository); err != nil {
				rows.Close()
				return domain.WorkspaceSetupCheckpoint{}, fmt.Errorf("scan setup repository inventory: %w", err)
			}
			if repositoryScopeAllows(scope, repository) {
				hasAuthorizedRepository = true
				break
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return domain.WorkspaceSetupCheckpoint{}, fmt.Errorf("iterate setup repository inventory: %w", err)
		}
		rows.Close()
		if !hasAuthorizedRepository {
			return domain.WorkspaceSetupCheckpoint{}, ErrSetupRepositoryScopeIncomplete
		}
	}
	// The browser saves the merge-blocking threshold before advancing, but the
	// checkpoint is the authority. Require an active, versioned tenant general
	// policy here as well so a direct checkpoint call cannot claim a governed
	// baseline without recording the threshold that the UI displayed.
	if current.CurrentStep == domain.WorkspaceSetupSeverity && input.CurrentStep == domain.WorkspaceSetupRules {
		var hasRecordedMergePolicy bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1
				FROM review_configurations configuration
				JOIN review_configuration_versions version
				  ON version.configuration_id = configuration.id
				WHERE configuration.tenant_id = $1
				  AND configuration.section = 'general'
				  AND configuration.scope_kind = 'tenant'
				  AND configuration.scope_ref = ''
				  AND configuration.active = TRUE
			)`, tenantID).Scan(&hasRecordedMergePolicy); err != nil {
			return domain.WorkspaceSetupCheckpoint{}, fmt.Errorf("verify setup merge policy: %w", err)
		}
		if !hasRecordedMergePolicy {
			return domain.WorkspaceSetupCheckpoint{}, ErrSetupReviewPolicyIncomplete
		}
	}
	var learningMode *string
	learningExclusions := []byte("[]")
	if learningBoundary != nil {
		mode := string(learningBoundary.Mode)
		learningMode = &mode
		learningExclusions, err = json.Marshal(learningBoundary.ReviewerExclusions)
		if err != nil {
			return domain.WorkspaceSetupCheckpoint{}, fmt.Errorf("encode setup learning boundary: %w", err)
		}
	}
	checkpoint, err := scanWorkspaceSetupCheckpoint(tx.QueryRow(ctx, `
		INSERT INTO workspace_setup_checkpoints (tenant_id, current_step, revision, learning_mode, learning_reviewer_exclusions, learning_recorded_at, updated_by, completed_at)
		VALUES ($1, $2, $3, $4::text, $5::jsonb, CASE WHEN $4::text IS NULL THEN NULL ELSE now() END, $6, CASE WHEN $2 = 'complete' THEN now() ELSE NULL END)
		ON CONFLICT (tenant_id) DO UPDATE SET current_step = EXCLUDED.current_step, revision = EXCLUDED.revision,
		  learning_mode = COALESCE(EXCLUDED.learning_mode, workspace_setup_checkpoints.learning_mode),
		  learning_reviewer_exclusions = CASE WHEN EXCLUDED.learning_mode IS NULL THEN workspace_setup_checkpoints.learning_reviewer_exclusions ELSE EXCLUDED.learning_reviewer_exclusions END,
		  learning_recorded_at = CASE WHEN EXCLUDED.learning_mode IS NULL THEN workspace_setup_checkpoints.learning_recorded_at ELSE now() END,
		  updated_by = EXCLUDED.updated_by, completed_at = CASE WHEN EXCLUDED.current_step = 'complete' THEN now() ELSE NULL END, updated_at = now()
		RETURNING current_step, revision, learning_mode, learning_reviewer_exclusions, updated_by, updated_at, completed_at`, tenantID, input.CurrentStep, current.Revision+1, learningMode, learningExclusions, actor))
	if err != nil {
		return domain.WorkspaceSetupCheckpoint{}, fmt.Errorf("save setup checkpoint: %w", err)
	}
	if input.CurrentStep == domain.WorkspaceSetupComplete {
		checkpoint.Readiness, err = createWorkspaceSetupReadiness(ctx, tx, tenantID, checkpoint)
		if err != nil {
			return domain.WorkspaceSetupCheckpoint{}, err
		}
	}
	auditMetadata := map[string]any{"step": input.CurrentStep, "revision": checkpoint.Revision}
	if current.CurrentStep == domain.WorkspaceSetupLearning && input.CurrentStep == domain.WorkspaceSetupSeverity && learningBoundary != nil {
		auditMetadata["learning_mode"] = learningBoundary.Mode
		auditMetadata["reviewer_exclusion_count"] = len(learningBoundary.ReviewerExclusions)
	}
	auditJSON, err := json.Marshal(auditMetadata)
	if err != nil {
		return domain.WorkspaceSetupCheckpoint{}, fmt.Errorf("encode setup checkpoint audit: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (tenant_id, actor_subject, action, target, metadata) VALUES ($1, $2, 'workspace.setup_checkpoint.saved', $3, $4::jsonb)`, tenantID, actor, tenantID.String(), auditJSON); err != nil {
		return domain.WorkspaceSetupCheckpoint{}, fmt.Errorf("audit setup checkpoint: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.WorkspaceSetupCheckpoint{}, fmt.Errorf("commit setup checkpoint: %w", err)
	}
	return checkpoint, nil
}

func (s *PostgresStore) loadWorkspaceSetupCheckpoint(ctx context.Context, tenantID uuid.UUID) (domain.WorkspaceSetupCheckpoint, error) {
	checkpoint, err := scanWorkspaceSetupCheckpoint(s.pool.QueryRow(ctx, `
		SELECT current_step, revision, learning_mode, learning_reviewer_exclusions, updated_by, updated_at, completed_at
		FROM workspace_setup_checkpoints
		WHERE tenant_id = $1`, tenantID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.WorkspaceSetupCheckpoint{}, ErrNotFound
	}
	if err != nil {
		return domain.WorkspaceSetupCheckpoint{}, fmt.Errorf("load setup checkpoint: %w", err)
	}
	return checkpoint, nil
}
