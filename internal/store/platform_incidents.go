package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PlatformIncidentStore is deliberately additive: an older control plane can
// continue to expose a health sample without claiming that it has a durable
// incident timeline or that an empty array means "no incidents".
type PlatformIncidentStore interface {
	CreatePlatformIncident(context.Context, string, string, domain.PlatformIncidentInput) (domain.PlatformIncident, error)
	ResolvePlatformIncident(context.Context, string, string, uuid.UUID, domain.PlatformIncidentResolutionInput) (domain.PlatformIncident, error)
}

var _ PlatformIncidentStore = (*PostgresStore)(nil)

const platformIncidentColumns = `
	id,title,state,scope,started_at,resolved_at,COALESCE(resolved_by, ''),resolution,
	affected_area,created_by,revision,updated_at`

func (s *PostgresStore) CreatePlatformIncident(ctx context.Context, actor, tenantSlug string, input domain.PlatformIncidentInput) (domain.PlatformIncident, error) {
	input, valid := domain.NormalizePlatformIncidentInput(input, time.Now())
	if !valid {
		return domain.PlatformIncident{}, ErrInvalidPlatformIncident
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.PlatformIncident{}, fmt.Errorf("begin platform incident create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.PlatformIncident{}, err
	}
	if !platformIncidentRoleAllowed(role) {
		return domain.PlatformIncident{}, ErrForbidden
	}
	incident, err := scanPlatformIncident(tx.QueryRow(ctx, `
		INSERT INTO platform_incidents (
			id,tenant_id,title,state,scope,affected_area,started_at,created_by
		) VALUES ($1,$2,$3,'active',$4,$5,$6,$7)
		RETURNING `+platformIncidentColumns,
		uuid.New(), tenantID, input.Title, input.Scope, input.AffectedArea, *input.StartedAt, actor))
	if err != nil {
		return domain.PlatformIncident{}, fmt.Errorf("create platform incident: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata)
		VALUES ($1,$2,'platform_incident.opened',$3,
		jsonb_build_object('scope',$4::text,'affected_area',$5::text,'revision',$6::integer))`,
		tenantID, actor, incident.ID.String(), incident.Scope, incident.AffectedArea, incident.Revision); err != nil {
		return domain.PlatformIncident{}, fmt.Errorf("audit platform incident create: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.PlatformIncident{}, fmt.Errorf("commit platform incident create: %w", err)
	}
	return incident, nil
}

func (s *PostgresStore) ResolvePlatformIncident(ctx context.Context, actor, tenantSlug string, incidentID uuid.UUID, input domain.PlatformIncidentResolutionInput) (domain.PlatformIncident, error) {
	input, valid := domain.NormalizePlatformIncidentResolutionInput(input)
	if !valid || incidentID == uuid.Nil {
		return domain.PlatformIncident{}, ErrInvalidPlatformIncident
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.PlatformIncident{}, fmt.Errorf("begin platform incident resolution: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tenantID, role, err := authorizedTenantTx(ctx, tx, actor, tenantSlug)
	if err != nil {
		return domain.PlatformIncident{}, err
	}
	if !platformIncidentRoleAllowed(role) {
		return domain.PlatformIncident{}, ErrForbidden
	}
	current, err := scanPlatformIncident(tx.QueryRow(ctx, `
		SELECT `+platformIncidentColumns+`
		FROM platform_incidents WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenantID, incidentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PlatformIncident{}, ErrNotFound
	}
	if err != nil {
		return domain.PlatformIncident{}, fmt.Errorf("load platform incident for resolution: %w", err)
	}
	if current.Revision != input.ExpectedRevision {
		return domain.PlatformIncident{}, ErrRevisionConflict
	}
	if !current.State.Active() {
		return domain.PlatformIncident{}, ErrConflict
	}
	incident, err := scanPlatformIncident(tx.QueryRow(ctx, `
		UPDATE platform_incidents
		SET state='resolved',resolved_at=now(),resolved_by=$3,resolution=$4,
			revision=revision+1,updated_at=now()
		WHERE tenant_id=$1 AND id=$2
		RETURNING `+platformIncidentColumns,
		tenantID, incidentID, actor, input.Resolution))
	if err != nil {
		return domain.PlatformIncident{}, fmt.Errorf("resolve platform incident: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events (tenant_id,actor_subject,action,target,metadata)
		VALUES ($1,$2,'platform_incident.resolved',$3,
		jsonb_build_object('revision',$4::integer,'resolution',$5::text))`,
		tenantID, actor, incident.ID.String(), incident.Revision, incident.Resolution); err != nil {
		return domain.PlatformIncident{}, fmt.Errorf("audit platform incident resolution: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.PlatformIncident{}, fmt.Errorf("commit platform incident resolution: %w", err)
	}
	return incident, nil
}

func platformIncidentRoleAllowed(role string) bool {
	return role == "owner" || role == "admin"
}

func scanPlatformIncident(row pgx.Row) (domain.PlatformIncident, error) {
	var incident domain.PlatformIncident
	err := row.Scan(
		&incident.ID, &incident.Title, &incident.State, &incident.Scope,
		&incident.StartedAt, &incident.ResolvedAt, &incident.ResolvedBy,
		&incident.Resolution, &incident.AffectedArea, &incident.CreatedBy,
		&incident.Revision, &incident.UpdatedAt,
	)
	return incident, err
}
