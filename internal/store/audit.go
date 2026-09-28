package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) ListAuditEvents(ctx context.Context, actor, tenantSlug string, filter domain.AuditFilter) ([]domain.AuditEvent, error) {
	filter.Actor = strings.TrimSpace(filter.Actor)
	filter.Action = strings.TrimSpace(filter.Action)
	filter.Target = strings.TrimSpace(filter.Target)
	if filter.Limit < 1 || filter.Limit > 100 || len(filter.Actor) > 240 || len(filter.Action) > 120 || len(filter.Target) > 500 ||
		(filter.From != nil && filter.Until != nil && !filter.From.Before(*filter.Until)) {
		return nil, ErrInvalidAuditFilter
	}
	tenantID, role, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return nil, err
	}
	if role != "owner" && role != "admin" && role != "rule_admin" && role != "viewer" {
		return nil, ErrForbidden
	}
	var before any
	if filter.Before != nil {
		before = *filter.Before
	}
	var from, until any
	if filter.From != nil {
		from = *filter.From
	}
	if filter.Until != nil {
		until = *filter.Until
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, actor_subject, action, target, metadata, created_at
		FROM audit_events
		WHERE tenant_id = $1
		  AND ($2 = '' OR actor_subject = $2)
		  AND ($3 = '' OR starts_with(action, $3))
		  AND ($6 = '' OR strpos(lower(target), lower($6)) > 0)
		  AND ($7::timestamptz IS NULL OR created_at >= $7)
		  AND ($8::timestamptz IS NULL OR created_at < $8)
		  AND ($5::uuid IS NULL OR (created_at, id) < (
			SELECT created_at, id FROM audit_events WHERE tenant_id = $1 AND id = $5
		  ))
		ORDER BY created_at DESC, id DESC
		LIMIT $4`, tenantID, filter.Actor, filter.Action, filter.Limit+1, before, filter.Target, from, until)
	if err != nil {
		return nil, fmt.Errorf("list audit events: %w", err)
	}
	defer rows.Close()
	items := make([]domain.AuditEvent, 0)
	for rows.Next() {
		var item domain.AuditEvent
		if err := rows.Scan(&item.ID, &item.ActorSubject, &item.Action, &item.Target, &item.Metadata, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan audit event: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) GetAuditEvent(ctx context.Context, actor, tenantSlug string, eventID uuid.UUID) (domain.AuditEvent, error) {
	if eventID == uuid.Nil {
		return domain.AuditEvent{}, fmt.Errorf("audit event ID is required")
	}
	tenantID, role, err := s.authorizedTenant(ctx, actor, tenantSlug)
	if err != nil {
		return domain.AuditEvent{}, err
	}
	if role != "owner" && role != "admin" && role != "rule_admin" && role != "viewer" {
		return domain.AuditEvent{}, ErrForbidden
	}
	var item domain.AuditEvent
	err = s.pool.QueryRow(ctx, `
		SELECT id, actor_subject, action, target, metadata, created_at
		FROM audit_events WHERE tenant_id = $1 AND id = $2`, tenantID, eventID).
		Scan(&item.ID, &item.ActorSubject, &item.Action, &item.Target, &item.Metadata, &item.CreatedAt)
	if err == pgx.ErrNoRows {
		return domain.AuditEvent{}, ErrNotFound
	}
	if err != nil {
		return domain.AuditEvent{}, fmt.Errorf("get audit event: %w", err)
	}
	return item, nil
}
